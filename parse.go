package main

import (
	"bytes"
	"crypto/sha1"
	"encoding/hex"
	"fmt"
	"net/url"
	"path"
	"regexp"
	"strconv"
	"strings"

	"golang.org/x/net/html"
)

type Subject struct {
	GUID  string `json:"guid"`
	Name  string `json:"name"`
	Group string `json:"group"` // "Открытый банк заданий ЕГЭ", ...
}

type Theme struct {
	Code string `json:"code"`
	Name string `json:"name"`
}

type KES struct {
	Code string `json:"code"`
	Name string `json:"name"`
}

type Option struct {
	N    string `json:"n"`
	HTML string `json:"html"`
	Text string `json:"text"`
}

type Image struct {
	URL  string `json:"url"`
	File string `json:"file,omitempty"` // путь относительно JSON-файла
}

// Context — общий текст для группы заданий (ОГЭ №1–5, тексты в русском и т.п.).
type Context struct {
	ID     string  `json:"id"`
	HTML   string  `json:"html"`
	Text   string  `json:"text"`
	Images []Image `json:"images,omitempty"`
}

type Task struct {
	ID         string `json:"id"`   // номер задания ФИПИ, например 40B442
	GUID       string `json:"guid"` // нужен для проверки ответа через solve.php
	Lines      []int  `json:"lines,omitempty"`
	LineTitle  string `json:"line_title,omitempty"`
	KES        []KES  `json:"kes"`
	AnswerType string `json:"answer_type"` // как на сайте: «Краткий ответ», ...
	Kind       string `json:"kind"`        // short | full | choice | multi | match | sequence | ...
	Hint       string `json:"hint"`
	GroupPos   int    `json:"group_pos,omitempty"` // «Задание №N» внутри группы
	ContextID  string `json:"context_id,omitempty"`

	HTML   string   `json:"html"` // условие; MathML без префикса m:, картинки локальные
	Text   string   `json:"text"` // условие простым текстом
	Images []Image  `json:"images,omitempty"`
	Option []Option `json:"options,omitempty"` // варианты для choice/multi

	MatchLetters []string `json:"match_letters,omitempty"` // А, Б, В… для соответствия
	AnswerSuffix string   `json:"answer_suffix,omitempty"` // единицы рядом с полем ответа («%»)

	context *Context
}

type QuestionsPage struct {
	Total    int
	Tasks    []*Task
	Contexts map[string]*Context
}

var (
	reSubject   = regexp.MustCompile(`<li id='p_([0-9A-Fa-f]{32})'[^>]*>([^<]+)</li>`)
	rePGroup    = regexp.MustCompile(`<li id='pg_([0-9A-Fa-f]{32})'[^>]*><span>([^<]+)</span>`)
	rePGBlock   = regexp.MustCompile(`(?s)<ul id='pgp_([0-9A-Fa-f]{32})'.*?</ul>`)
	reTheme     = regexp.MustCompile(`name='theme' value='([^']+)'>\s*([^<]*)</label>`)
	reQCount    = regexp.MustCompile(`setQCount\((\d+)`)
	reQFilesLoc = regexp.MustCompile(`qfiles_location\s*=\s*'([^']*)'`)
	reFilesLoc  = regexp.MustCompile(`files_location\s*=\s*'([^']*)'`)
	rePicture   = regexp.MustCompile(`(ShowPicture\w*)\(\s*['"]([^'"]+)['"]`)
	reGroupPos  = regexp.MustCompile(`^Задание №\s*(\d+)`)
	reSpaces    = regexp.MustCompile(`[ \t\f\r\x{00A0}\x{2009}\x{202F}]+`)
	reBlankLns  = regexp.MustCompile(`\n\s*\n+`)
)

func parseSubjects(page string) []Subject {
	groups := map[string]string{}
	for _, m := range rePGroup.FindAllStringSubmatch(page, -1) {
		groups[m[1]] = strings.TrimSpace(m[2])
	}
	var out []Subject
	for _, block := range rePGBlock.FindAllStringSubmatch(page, -1) {
		for _, m := range reSubject.FindAllStringSubmatch(block[0], -1) {
			out = append(out, Subject{GUID: m[1], Name: strings.TrimSpace(html.UnescapeString(m[2])), Group: groups[block[1]]})
		}
	}
	return out
}

func parseThemes(page string) []Theme {
	var out []Theme
	seen := map[string]bool{}
	for _, m := range reTheme.FindAllStringSubmatch(page, -1) {
		if seen[m[1]] {
			continue
		}
		seen[m[1]] = true
		out = append(out, Theme{Code: m[1], Name: strings.TrimSpace(html.UnescapeString(m[2]))})
	}
	return out
}

// ---------- страница questions.php ----------

func parseQuestionsPage(doc, pageURL string) (*QuestionsPage, error) {
	res := &QuestionsPage{Contexts: map[string]*Context{}}
	if ms := reQCount.FindAllStringSubmatch(doc, -1); len(ms) > 0 {
		res.Total, _ = strconv.Atoi(ms[len(ms)-1][1])
	}
	qfiles := "../../"
	if m := reQFilesLoc.FindStringSubmatch(doc); m != nil {
		qfiles = m[1]
	}
	base, err := url.Parse(pageURL)
	if err != nil {
		return nil, err
	}
	root, err := html.Parse(strings.NewReader(doc))
	if err != nil {
		return nil, fmt.Errorf("разбор HTML: %w", err)
	}

	// Блоки идут подряд: [контекст группы] qblock#qID, div#iID (свойства), ...
	var blocks []*html.Node
	infos := map[string]*html.Node{}
	var walk func(*html.Node)
	walk = func(n *html.Node) {
		if n.Type == html.ElementNode && n.Data == "div" {
			if hasClass(n, "qblock") {
				blocks = append(blocks, n)
				return
			}
			if id := attr(n, "id"); len(id) > 1 && id[0] == 'i' && hasDescendantClass(n, "task-info-panel") {
				infos[id[1:]] = n
				return
			}
		}
		for c := n.FirstChild; c != nil; c = c.NextSibling {
			walk(c)
		}
	}
	walk(root)

	var curCtx *Context
	for _, b := range blocks {
		id := strings.TrimPrefix(attr(b, "id"), "q")
		if id == "" {
			curCtx = parseContext(b, base, qfiles)
			res.Contexts[curCtx.ID] = curCtx
			continue
		}
		t := parseTask(b, id, base, qfiles)
		if info := infos[id]; info != nil {
			parseInfo(info, t)
		}
		t.Kind = kindOf(t.AnswerType)
		if m := reGroupPos.FindStringSubmatch(t.Hint); m != nil {
			t.GroupPos, _ = strconv.Atoi(m[1])
			if curCtx != nil {
				t.context = curCtx
				t.ContextID = curCtx.ID
			}
		} else {
			curCtx = nil
		}
		res.Tasks = append(res.Tasks, t)
	}
	return res, nil
}

func parseContext(b *html.Node, base *url.URL, qfiles string) *Context {
	files := ""
	for _, s := range findAll(b, func(n *html.Node) bool { return n.Data == "script" }) {
		if m := reFilesLoc.FindStringSubmatch(textOf(s)); m != nil {
			files = m[1]
		}
	}
	for _, h := range findAll(b, func(n *html.Node) bool { return hasClass(n, "hint") }) {
		h.Parent.RemoveChild(h)
	}
	r := &resolver{base: base, qfiles: qfiles, files: files}
	r.transform(b)
	c := &Context{HTML: renderInner(b), Text: plainText(b), Images: r.images}
	sum := sha1.Sum([]byte(c.HTML))
	c.ID = "ctx_" + hex.EncodeToString(sum[:])[:12]
	return c
}

func parseTask(b *html.Node, id string, base *url.URL, qfiles string) *Task {
	t := &Task{ID: id}
	if h := findFirst(b, func(n *html.Node) bool { return hasClass(n, "hint") }); h != nil {
		t.Hint = cleanText(textOf(h))
	}
	if in := findFirst(b, func(n *html.Node) bool { return n.Data == "input" && attr(n, "name") == "guid" }); in != nil {
		t.GUID = attr(in, "value")
	}
	r := &resolver{base: base, qfiles: qfiles}

	if cell := findFirst(b, func(n *html.Node) bool { return n.Data == "td" && hasClass(n, "cell_0") }); cell != nil {
		r.transform(cell)
		t.HTML = renderInner(cell)
		t.Text = plainText(cell)
	}

	if vb := findFirst(b, func(n *html.Node) bool { return n.Data == "td" && hasClass(n, "varinats-block") }); vb != nil {
		if dt := findFirst(vb, func(n *html.Node) bool { return n.Data == "table" && hasClass(n, "distractors-table") }); dt != nil {
			for _, tr := range findAll(dt, func(n *html.Node) bool { return n.Data == "tr" }) {
				tds := childElems(tr, "td")
				if len(tds) == 0 {
					continue
				}
				num := ""
				if bn := findFirst(tr, func(n *html.Node) bool { return n.Data == "b" }); bn != nil {
					num = strings.TrimSuffix(cleanText(textOf(bn)), ")")
				}
				content := tds[len(tds)-1]
				r.transform(content)
				t.Option = append(t.Option, Option{N: num, HTML: renderInner(content), Text: plainText(content)})
			}
		} else if at := findFirst(vb, func(n *html.Node) bool { return n.Data == "table" && hasClass(n, "answer-table") }); at != nil {
			if tr := findFirst(at, func(n *html.Node) bool { return n.Data == "tr" }); tr != nil {
				for _, bn := range findAll(tr, func(n *html.Node) bool { return n.Data == "b" }) {
					t.MatchLetters = append(t.MatchLetters, cleanText(textOf(bn)))
				}
			}
		} else {
			for _, in := range findAll(vb, func(n *html.Node) bool { return n.Data == "input" || n.Data == "select" }) {
				in.Parent.RemoveChild(in)
			}
			t.AnswerSuffix = cleanText(textOf(vb))
		}
	}
	t.Images = r.images
	return t
}

func parseInfo(info *html.Node, t *Task) {
	for _, tr := range findAll(info, func(n *html.Node) bool { return n.Data == "tr" }) {
		tds := childElems(tr, "td")
		if len(tds) < 2 {
			continue
		}
		switch strings.TrimSpace(textOf(tds[0])) {
		case "КЭС:":
			for _, d := range childElems(tds[1], "div") {
				s := cleanText(textOf(d))
				code, name, _ := strings.Cut(s, " ")
				t.KES = append(t.KES, KES{Code: code, Name: name})
			}
		case "Тип ответа:":
			t.AnswerType = cleanText(textOf(tds[1]))
		}
	}
}

// kindOf переводит подпись «Тип ответа» в короткий код.
func kindOf(label string) string {
	switch {
	case label == "Краткий ответ":
		return "short"
	case strings.HasPrefix(label, "Краткий ответ"):
		return "vshort"
	case strings.HasPrefix(label, "Развернутый"), strings.HasPrefix(label, "Развёрнутый"):
		return "full"
	case strings.HasPrefix(label, "Выбор ответов"):
		return "multi"
	case strings.HasPrefix(label, "Выбор ответа"):
		return "choice"
	case strings.HasPrefix(label, "Установление соответствия"):
		return "match"
	case strings.HasPrefix(label, "Последовательность"):
		return "sequence"
	case strings.HasPrefix(label, "Расстановка"):
		return "terms"
	case label == "":
		return "unknown"
	}
	return "other"
}

// kindCodes — значения фильтра qkind на сайте.
var kindCodes = map[string]string{
	"short":    "ILI_STD_SHORT",
	"vshort":   "ILI_EXT_VSHORT",
	"full":     "ILI_STD_FULL",
	"choice":   "ILI_STD_SELECTONE",
	"multi":    "ILI_STD_SELECTN",
	"match":    "ILI_EXT_ACCORD",
	"sequence": "ILI_EXT_SEQUENCE",
	"terms":    "ILI_EXT_TXTPARTCHOICE",
}

// ---------- очистка HTML ----------

type resolver struct {
	base   *url.URL
	qfiles string // префикс для ShowPictureQ* (картинки заданий)
	files  string // префикс для ShowPicture* (картинки общих текстов)
	images []Image
}

func (r *resolver) image(prefix, p string) (string, bool) {
	ref, err := url.Parse(strings.ReplaceAll(prefix+p, " ", "%20"))
	if err != nil {
		return "", false
	}
	abs := r.base.ResolveReference(ref).String()
	ext := strings.ToLower(path.Ext(ref.Path))
	if ext == "" || len(ext) > 5 {
		ext = ".bin"
	}
	sum := sha1.Sum([]byte(abs))
	img := Image{URL: abs, File: "images/" + hex.EncodeToString(sum[:])[:16] + ext}
	for _, have := range r.images {
		if have.URL == abs {
			return have.File, true
		}
	}
	r.images = append(r.images, img)
	return img.File, true
}

// transform чистит поддерево: скрипты → <img>, m:math → math, убирает
// поля ввода и обработчики событий.
func (r *resolver) transform(n *html.Node) {
	for c := n.FirstChild; c != nil; {
		next := c.NextSibling
		if c.Type == html.ElementNode {
			switch {
			case c.Data == "script":
				for _, m := range rePicture.FindAllStringSubmatch(textOf(c), -1) {
					prefix := r.qfiles
					if !strings.HasPrefix(m[1], "ShowPictureQ") {
						prefix = r.files
					}
					if file, ok := r.image(prefix, m[2]); ok {
						n.InsertBefore(&html.Node{Type: html.ElementNode, Data: "img", Attr: []html.Attribute{{Key: "src", Val: file}}}, c)
					}
				}
				n.RemoveChild(c)
			case c.Data == "style" || c.Data == "input" || c.Data == "select" || c.Data == "button" || c.Data == "noscript":
				n.RemoveChild(c)
			case c.Data == "a" && attr(c, "href") == "":
				// <a name="_GoBack"> и подобные якоря: разворачиваем, детей
				// переносим на место ссылки и обрабатываем их следующими.
				first := c.FirstChild
				for gc := c.FirstChild; gc != nil; {
					gnext := gc.NextSibling
					c.RemoveChild(gc)
					n.InsertBefore(gc, c)
					gc = gnext
				}
				n.RemoveChild(c)
				if first != nil {
					next = first
				}
			default:
				r.cleanElem(c)
				r.transform(c)
			}
		}
		c = next
	}
}

func (r *resolver) cleanElem(c *html.Node) {
	c.Data = strings.TrimPrefix(c.Data, "m:")
	attrs := c.Attr[:0]
	for _, a := range c.Attr {
		k := strings.ToLower(a.Key)
		if strings.HasPrefix(k, "on") || k == "style" || k == "bgcolor" {
			continue
		}
		if c.Data == "img" && k == "src" {
			if strings.Contains(a.Val, "loading_spinner") {
				continue
			}
			if !strings.HasPrefix(a.Val, "images/") {
				if file, ok := r.image("", a.Val); ok {
					a.Val = file
				}
			}
		}
		attrs = append(attrs, a)
	}
	c.Attr = attrs
	if c.Data == "math" && attr(c, "xmlns") == "" {
		c.Attr = append(c.Attr, html.Attribute{Key: "xmlns", Val: "http://www.w3.org/1998/Math/MathML"})
	}
}

// ---------- текст ----------

var blockTags = map[string]bool{"p": true, "div": true, "br": true, "tr": true, "li": true, "table": true, "h1": true, "h2": true, "h3": true}

func plainText(n *html.Node) string {
	var sb strings.Builder
	var walk func(*html.Node, bool)
	walk = func(n *html.Node, inMath bool) {
		switch n.Type {
		case html.TextNode:
			if inMath {
				sb.WriteString(strings.TrimSpace(n.Data))
			} else {
				sb.WriteString(n.Data)
			}
			return
		case html.ElementNode:
			tag := strings.TrimPrefix(n.Data, "m:")
			if tag == "script" || tag == "style" {
				return
			}
			if tag == "img" {
				sb.WriteString(" [рисунок] ")
				return
			}
			if tag == "math" {
				sb.WriteString(" ")
				inMath = true
			}
			if blockTags[tag] {
				sb.WriteString("\n")
			} else if tag == "td" {
				sb.WriteString(" ")
			}
			for c := n.FirstChild; c != nil; c = c.NextSibling {
				walk(c, inMath)
			}
			if tag == "math" || tag == "td" {
				sb.WriteString(" ")
			}
			if blockTags[tag] {
				sb.WriteString("\n")
			}
			return
		}
		for c := n.FirstChild; c != nil; c = c.NextSibling {
			walk(c, inMath)
		}
	}
	walk(n, false)
	return cleanText(sb.String())
}

func cleanText(s string) string {
	s = reSpaces.ReplaceAllString(s, " ")
	lines := strings.Split(s, "\n")
	for i, l := range lines {
		lines[i] = strings.TrimSpace(l)
	}
	s = strings.Join(lines, "\n")
	s = reBlankLns.ReplaceAllString(s, "\n")
	return strings.TrimSpace(s)
}

// ---------- утилиты DOM ----------

func attr(n *html.Node, key string) string {
	for _, a := range n.Attr {
		if strings.EqualFold(a.Key, key) {
			return a.Val
		}
	}
	return ""
}

func hasClass(n *html.Node, cls string) bool {
	if n.Type != html.ElementNode {
		return false
	}
	for _, c := range strings.Fields(attr(n, "class")) {
		if c == cls {
			return true
		}
	}
	return false
}

func hasDescendantClass(n *html.Node, cls string) bool {
	return findFirst(n, func(x *html.Node) bool { return hasClass(x, cls) }) != nil
}

func findFirst(n *html.Node, pred func(*html.Node) bool) *html.Node {
	for c := n.FirstChild; c != nil; c = c.NextSibling {
		if c.Type == html.ElementNode && pred(c) {
			return c
		}
		if f := findFirst(c, pred); f != nil {
			return f
		}
	}
	return nil
}

func findAll(n *html.Node, pred func(*html.Node) bool) []*html.Node {
	var out []*html.Node
	var walk func(*html.Node)
	walk = func(n *html.Node) {
		for c := n.FirstChild; c != nil; c = c.NextSibling {
			if c.Type == html.ElementNode && pred(c) {
				out = append(out, c)
			}
			walk(c)
		}
	}
	walk(n)
	return out
}

func childElems(n *html.Node, tag string) []*html.Node {
	var out []*html.Node
	for c := n.FirstChild; c != nil; c = c.NextSibling {
		if c.Type == html.ElementNode && c.Data == tag {
			out = append(out, c)
		}
	}
	return out
}

func textOf(n *html.Node) string {
	var sb strings.Builder
	var walk func(*html.Node)
	walk = func(n *html.Node) {
		if n.Type == html.TextNode {
			sb.WriteString(n.Data)
		}
		for c := n.FirstChild; c != nil; c = c.NextSibling {
			walk(c)
		}
	}
	walk(n)
	return sb.String()
}

func renderInner(n *html.Node) string {
	var buf bytes.Buffer
	for c := n.FirstChild; c != nil; c = c.NextSibling {
		_ = html.Render(&buf, c)
	}
	s := reSpaces.ReplaceAllString(buf.String(), " ")
	return strings.TrimSpace(s)
}
