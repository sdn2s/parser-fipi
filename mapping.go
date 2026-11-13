package main

import (
	"embed"
	"encoding/json"
	"fmt"
	"io/fs"
	"os"
	"regexp"
	"slices"
	"sort"
	"strings"
)

// Банк ФИПИ не хранит номер задания в КИМ («задание №5»), поэтому номер
// определяется по правилам: тип ответа + темы КЭС + регулярки по тексту.
// Правила проверяются сверху вниз, задание получает первое подходящее.
// Это приближение: правила лежат в mappings/*.json и их можно править.

//go:embed mappings/*.json
var embeddedMappings embed.FS

type Rule struct {
	Lines    []int    `json:"lines"`
	Title    string   `json:"title"`
	Kind     []string `json:"kind,omitempty"`
	KesAny   []string `json:"kes_any,omitempty"`
	KesNone  []string `json:"kes_none,omitempty"`
	Hint     string   `json:"hint,omitempty"`
	TextAny  []string `json:"text_any,omitempty"`
	TextNone []string `json:"text_none,omitempty"`

	hint     *regexp.Regexp
	textAny  []*regexp.Regexp
	textNone []*regexp.Regexp
}

type Mapping struct {
	Exam    string  `json:"exam"`
	Subject string  `json:"subject"`
	Year    int     `json:"year"`
	Note    string  `json:"note"`
	Rules   []*Rule `json:"rules"`
}

func mappingFile(exam, subject string) string { return exam + "_" + subject + ".json" }

// LoadMapping берёт файл из -mapping, иначе встроенный для exam/subject.
// Возвращает nil, nil, если маппинга для предмета нет.
func LoadMapping(path, exam, subject string) (*Mapping, error) {
	var data []byte
	var err error
	if path != "" {
		data, err = os.ReadFile(path)
	} else {
		data, err = embeddedMappings.ReadFile("mappings/" + mappingFile(exam, subject))
		if err != nil {
			return nil, nil
		}
	}
	if err != nil {
		return nil, err
	}
	var m Mapping
	if err := json.Unmarshal(data, &m); err != nil {
		return nil, fmt.Errorf("маппинг %s: %w", path, err)
	}
	for i, r := range m.Rules {
		if err := r.compile(); err != nil {
			return nil, fmt.Errorf("маппинг: правило #%d (%v): %w", i+1, r.Lines, err)
		}
	}
	return &m, nil
}

// AvailableMappings — список встроенных маппингов «exam_subject».
func AvailableMappings() []string {
	var out []string
	_ = fs.WalkDir(embeddedMappings, "mappings", func(p string, d fs.DirEntry, err error) error {
		if err == nil && !d.IsDir() {
			out = append(out, strings.TrimSuffix(d.Name(), ".json"))
		}
		return nil
	})
	return out
}

func (r *Rule) compile() error {
	var err error
	if r.Hint != "" {
		if r.hint, err = regexp.Compile(r.Hint); err != nil {
			return err
		}
	}
	for _, p := range r.TextAny {
		re, err := regexp.Compile(p)
		if err != nil {
			return err
		}
		r.textAny = append(r.textAny, re)
	}
	for _, p := range r.TextNone {
		re, err := regexp.Compile(p)
		if err != nil {
			return err
		}
		r.textNone = append(r.textNone, re)
	}
	return nil
}

func kesMatch(kes []KES, patterns []string) bool {
	for _, k := range kes {
		for _, p := range patterns {
			if k.Code == p || strings.HasPrefix(k.Code, p+".") {
				return true
			}
		}
	}
	return false
}

func (r *Rule) Match(t *Task) bool {
	if len(r.Kind) > 0 && !slices.Contains(r.Kind, t.Kind) {
		return false
	}
	if len(r.KesAny) > 0 && !kesMatch(t.KES, r.KesAny) {
		return false
	}
	if len(r.KesNone) > 0 && kesMatch(t.KES, r.KesNone) {
		return false
	}
	if r.hint != nil && !r.hint.MatchString(t.Hint) {
		return false
	}
	if len(r.textAny) > 0 {
		ok := false
		for _, re := range r.textAny {
			if re.MatchString(t.Text) {
				ok = true
				break
			}
		}
		if !ok {
			return false
		}
	}
	for _, re := range r.textNone {
		if re.MatchString(t.Text) {
			return false
		}
	}
	return true
}

func (m *Mapping) Classify(t *Task) *Rule {
	for _, r := range m.Rules {
		if r.Match(t) {
			return r
		}
	}
	return nil
}

// ServerFilter — фильтр для сайта, который заведомо включает все задания
// номера line (объединение условий правил). Пустой срез = без ограничения.
func (m *Mapping) ServerFilter(line int) (kinds, kes []string) {
	anyKind, anyKes := false, false
	for _, r := range m.Rules {
		if !slices.Contains(r.Lines, line) {
			continue
		}
		if len(r.Kind) == 0 {
			anyKind = true
		}
		if len(r.KesAny) == 0 {
			anyKes = true
		}
		kinds = append(kinds, r.Kind...)
		kes = append(kes, r.KesAny...)
	}
	if anyKind {
		kinds = nil
	}
	if anyKes {
		kes = nil
	}
	slices.Sort(kinds)
	slices.Sort(kes)
	return slices.Compact(kinds), slices.Compact(kes)
}

type LineInfo struct {
	N     int
	Title string
}

func (m *Mapping) LineList() []LineInfo {
	seen := map[int]string{}
	for _, r := range m.Rules {
		for _, n := range r.Lines {
			if _, ok := seen[n]; !ok {
				seen[n] = r.Title
			}
		}
	}
	var out []LineInfo
	for n, t := range seen {
		out = append(out, LineInfo{n, t})
	}
	sort.Slice(out, func(i, j int) bool { return out[i].N < out[j].N })
	return out
}

func (m *Mapping) HasLine(n int) bool {
	for _, r := range m.Rules {
		if slices.Contains(r.Lines, n) {
			return true
		}
	}
	return false
}
