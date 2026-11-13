// parser-fipi — выгрузка заданий из открытого банка ФИПИ (ЕГЭ / ОГЭ).
//
// Примеры:
//
//	go run . -exam ege -subject math -n 5 -count 20
//	go run . -exam oge -subject math -n 10 -count 15 -random
//	go run . -exam ege -subject phys -kes 3.1 -kind short -count 50
//	go run . -exam ege -subject math -id 40B442
//	go run . -exam oge -list-subjects
//	go run . -exam ege -subject math -list-lines
package main

import (
	"context"
	"encoding/json"
	"errors"
	"flag"
	"fmt"
	"math/rand/v2"
	"os"
	"os/signal"
	"path/filepath"
	"slices"
	"strconv"
	"strings"
	"time"
)

const maxPageSize = 100

type Output struct {
	Source      string              `json:"source"`
	Exam        string              `json:"exam"`
	Subject     string              `json:"subject"`
	SubjectName string              `json:"subject_name"`
	SubjectGUID string              `json:"subject_guid"`
	Line        int                 `json:"line,omitempty"`
	LineTitle   string              `json:"line_title,omitempty"`
	Filter      map[string]string   `json:"filter,omitempty"`
	FetchedAt   time.Time           `json:"fetched_at"`
	FoundOnSite int                 `json:"found_on_site"` // по серверному фильтру
	Count       int                 `json:"count"`
	Tasks       []*Task             `json:"tasks"`
	Contexts    map[string]*Context `json:"contexts,omitempty"`
}

type options struct {
	exam, subject   string
	line, count     int
	random          bool
	seed            uint64
	kes, kind, id   string
	out             string
	noImages        bool
	delay           time.Duration
	mappingPath     string
	listSubjects    bool
	listLines       bool
	saveHTML        string
	verbose, stdout bool
}

func main() {
	var o options
	flag.StringVar(&o.exam, "exam", "", "экзамен: ege | oge (обязательно)")
	flag.StringVar(&o.subject, "subject", "math", "предмет: короткое имя (math, phys, rus…), GUID или название; см. -list-subjects")
	flag.IntVar(&o.line, "n", 0, "номер задания в варианте КИМ (по маппингу, см. -list-lines)")
	flag.IntVar(&o.count, "count", 10, "сколько заданий выгрузить; 0 = все найденные")
	flag.BoolVar(&o.random, "random", false, "случайная выборка из всех подходящих (выкачивает всю выдачу)")
	flag.Uint64Var(&o.seed, "seed", 0, "seed для -random (0 = от времени)")
	flag.StringVar(&o.kes, "kes", "", "темы КЭС через запятую: 7.5,2.1 (раздел 7 = все 7.x)")
	flag.StringVar(&o.kind, "kind", "", "типы ответа через запятую: short,full,choice,multi,match,sequence,vshort,terms")
	flag.StringVar(&o.id, "id", "", "номер задания ФИПИ, например 40B442")
	flag.StringVar(&o.out, "out", "fipi_out", "папка для JSON и картинок")
	flag.BoolVar(&o.noImages, "no-images", false, "не скачивать картинки (в HTML останутся ссылки на сайт)")
	flag.DurationVar(&o.delay, "delay", time.Second, "пауза между запросами к сайту")
	flag.StringVar(&o.mappingPath, "mapping", "", "свой файл маппинга номеров (по умолчанию встроенный)")
	flag.BoolVar(&o.listSubjects, "list-subjects", false, "показать предметы банка и выйти")
	flag.BoolVar(&o.listLines, "list-lines", false, "показать номера заданий из маппинга и выйти")
	flag.StringVar(&o.saveHTML, "save-html", "", "сохранять сырые страницы выдачи в папку (для проверки маппинга тестом)")
	flag.BoolVar(&o.stdout, "stdout", false, "писать JSON в stdout вместо файла")
	flag.BoolVar(&o.verbose, "v", false, "подробный лог запросов")
	flag.Usage = func() {
		fmt.Fprintf(os.Stderr, "Выгрузка заданий из открытого банка ФИПИ.\n\nИспользование:\n  parser-fipi -exam ege|oge [-subject math] [-n 5] [-count 10] [флаги]\n\n")
		flag.PrintDefaults()
		fmt.Fprintf(os.Stderr, "\nВстроенные маппинги номеров: %s\n", strings.Join(AvailableMappings(), ", "))
	}
	flag.Parse()

	ctx, stop := signal.NotifyContext(context.Background(), os.Interrupt)
	defer stop()
	if err := run(ctx, o); err != nil {
		fmt.Fprintln(os.Stderr, "ошибка:", err)
		os.Exit(1)
	}
}

func run(ctx context.Context, o options) error {
	o.exam = strings.ToLower(strings.TrimSpace(o.exam))
	host, err := examHost(o.exam)
	if err != nil {
		return err
	}
	if o.count < 0 {
		return errors.New("-count должен быть >= 0")
	}

	// -list-lines по короткому имени работает без сети.
	if o.listLines && !o.listSubjects {
		alias := canonicalAlias(o.exam, o.subject)
		m, err := LoadMapping(o.mappingPath, o.exam, alias)
		if err != nil {
			return err
		}
		if m == nil {
			return fmt.Errorf("нет маппинга номеров для %s/%s; есть: %s", o.exam, alias, strings.Join(AvailableMappings(), ", "))
		}
		fmt.Printf("%s / %s (%d): %s\n\n", m.Exam, m.Subject, m.Year, m.Note)
		for _, l := range m.LineList() {
			fmt.Printf("  %2d  %s\n", l.N, l.Title)
		}
		return nil
	}

	c := NewClient(host, o.delay)
	c.Verbose = o.verbose
	c.SaveDir = o.saveHTML

	subjects, err := c.Subjects(ctx)
	if err != nil {
		return fmt.Errorf("список предметов: %w", err)
	}
	if o.listSubjects {
		printSubjects(o.exam, subjects)
		return nil
	}
	subj, alias, err := resolveSubject(o.exam, o.subject, subjects)
	if err != nil {
		return err
	}

	mapping, err := LoadMapping(o.mappingPath, o.exam, alias)
	if err != nil {
		return err
	}
	if o.line != 0 {
		if mapping == nil {
			return fmt.Errorf("-n: нет маппинга номеров для %s/%s (есть: %s); используйте -kes/-kind или свой -mapping",
				o.exam, alias, strings.Join(AvailableMappings(), ", "))
		}
		if !mapping.HasLine(o.line) {
			return fmt.Errorf("-n %d: такого номера нет в маппинге, см. -list-lines", o.line)
		}
	}

	// ---- фильтр ----
	userKes := splitList(o.kes)
	userKinds := splitList(o.kind)
	for _, k := range userKinds {
		if _, ok := kindCodes[k]; !ok {
			return fmt.Errorf("-kind: неизвестный тип %q", k)
		}
	}
	srvKes, srvKinds := userKes, userKinds
	if o.line != 0 {
		mk, mt := mapping.ServerFilter(o.line)
		if len(srvKinds) == 0 {
			srvKinds = mk
		}
		if len(srvKes) == 0 {
			srvKes = mt
		}
	}
	f := Filter{QID: strings.ToUpper(strings.TrimSpace(o.id))}
	for _, k := range srvKinds {
		f.Kinds = append(f.Kinds, kindCodes[k])
	}
	if len(srvKes) > 0 {
		themes, err := c.Themes(ctx, subj.GUID)
		if err != nil {
			return fmt.Errorf("темы КЭС: %w", err)
		}
		f.Themes = expandThemes(srvKes, themes)
	}

	keep := func(t *Task) bool {
		if len(userKinds) > 0 && !slices.Contains(userKinds, t.Kind) {
			return false
		}
		if len(userKes) > 0 && !kesMatch(t.KES, userKes) {
			return false
		}
		if o.line != 0 && !slices.Contains(t.Lines, o.line) {
			return false
		}
		return true
	}

	// ---- выгрузка ----
	limit := o.count
	if o.random || f.QID != "" {
		limit = 0
	}
	// Для небольших выборок не тянем по 100 заданий: берём с запасом
	// под отсев маппингом. Размер страницы постоянен в пределах запуска.
	pageSize := maxPageSize
	if limit > 0 {
		pageSize = min(maxPageSize, max(20, limit*3))
	}
	var picked []*Task
	contexts := map[string]*Context{}
	total, scanned := 0, 0
	for page := 0; ; page++ {
		qp, err := c.Search(ctx, subj.GUID, f, page, pageSize)
		if err != nil {
			return fmt.Errorf("страница %d: %w", page+1, err)
		}
		if page == 0 {
			total = qp.Total
			fmt.Fprintf(os.Stderr, "%s / %s: найдено на сайте по фильтру: %d\n", strings.ToUpper(o.exam), subj.Name, total)
		}
		for _, t := range qp.Tasks {
			scanned++
			if mapping != nil {
				if r := mapping.Classify(t); r != nil {
					t.Lines, t.LineTitle = r.Lines, r.Title
				}
			}
			if !keep(t) {
				continue
			}
			picked = append(picked, t)
			if t.context != nil {
				contexts[t.context.ID] = t.context
			}
		}
		fmt.Fprintf(os.Stderr, "  страница %d/%d: подходит %d\n", page+1, max(1, (total+pageSize-1)/pageSize), len(picked))
		if len(qp.Tasks) == 0 || (page+1)*pageSize >= total || (limit > 0 && len(picked) >= limit) {
			break
		}
	}

	if o.random && len(picked) > 0 {
		seed := o.seed
		if seed == 0 {
			seed = uint64(time.Now().UnixNano())
		}
		rng := rand.New(rand.NewPCG(seed, seed^0x9e3779b97f4a7c15))
		rng.Shuffle(len(picked), func(i, j int) { picked[i], picked[j] = picked[j], picked[i] })
	}
	if o.count > 0 && len(picked) > o.count {
		picked = picked[:o.count]
	}
	// оставляем только контексты выбранных заданий
	used := map[string]*Context{}
	for _, t := range picked {
		if t.ContextID != "" {
			used[t.ContextID] = contexts[t.ContextID]
		}
	}

	// ---- картинки ----
	if err := handleImages(ctx, c, o, picked, used); err != nil {
		return err
	}

	// ---- запись ----
	res := Output{
		Source:      "ФИПИ, открытый банк заданий: " + c.BankURL() + "index.php?proj=" + subj.GUID,
		Exam:        o.exam,
		Subject:     alias,
		SubjectName: subj.Name,
		SubjectGUID: subj.GUID,
		Line:        o.line,
		FetchedAt:   time.Now().UTC().Truncate(time.Second),
		FoundOnSite: total,
		Count:       len(picked),
		Tasks:       picked,
		Contexts:    used,
	}
	if res.Tasks == nil {
		res.Tasks = []*Task{}
	}
	if o.line != 0 {
		for _, l := range mapping.LineList() {
			if l.N == o.line {
				res.LineTitle = l.Title
			}
		}
	}
	res.Filter = map[string]string{}
	for k, v := range map[string]string{"kes": strings.Join(f.Themes, ","), "kind": strings.Join(srvKinds, ","), "id": f.QID} {
		if v != "" {
			res.Filter[k] = v
		}
	}

	data, err := json.MarshalIndent(res, "", "  ")
	if err != nil {
		return err
	}
	if o.stdout {
		_, err = os.Stdout.Write(append(data, '\n'))
		return err
	}
	if err := os.MkdirAll(o.out, 0o755); err != nil {
		return err
	}
	file := filepath.Join(o.out, outputName(o, alias))
	if err := os.WriteFile(file, append(data, '\n'), 0o644); err != nil {
		return err
	}
	fmt.Fprintf(os.Stderr, "просмотрено %d, сохранено %d заданий → %s\n", scanned, len(picked), file)
	if len(picked) < o.count && !o.random && f.QID == "" {
		fmt.Fprintf(os.Stderr, "внимание: нашлось меньше, чем запрошено (%d из %d)\n", len(picked), o.count)
	}
	return nil
}

func handleImages(ctx context.Context, c *Client, o options, tasks []*Task, contexts map[string]*Context) error {
	var all []*[]Image
	var htmls []*string
	for _, t := range tasks {
		all = append(all, &t.Images)
		htmls = append(htmls, &t.HTML)
		for i := range t.Option {
			htmls = append(htmls, &t.Option[i].HTML)
		}
	}
	for _, cx := range contexts {
		all = append(all, &cx.Images)
		htmls = append(htmls, &cx.HTML)
	}

	if o.noImages || o.stdout {
		// В HTML подставляем адреса на сайте вместо локальных путей.
		repl := map[string]string{}
		for _, imgs := range all {
			for i := range *imgs {
				repl[(*imgs)[i].File] = (*imgs)[i].URL
				(*imgs)[i].File = ""
			}
		}
		for _, h := range htmls {
			for from, to := range repl {
				*h = strings.ReplaceAll(*h, `src="`+from+`"`, `src="`+to+`"`)
			}
		}
		return nil
	}

	seen := map[string]bool{}
	var queue []Image
	for _, imgs := range all {
		for _, im := range *imgs {
			if !seen[im.File] {
				seen[im.File] = true
				queue = append(queue, im)
			}
		}
	}
	failed := map[string]bool{}
	for i, im := range queue {
		if err := c.Download(ctx, im.URL, filepath.Join(o.out, im.File)); err != nil {
			if ctx.Err() != nil {
				return ctx.Err()
			}
			fmt.Fprintf(os.Stderr, "  картинка не скачалась: %v\n", err)
			failed[im.File] = true
		}
		if (i+1)%20 == 0 || i+1 == len(queue) {
			fmt.Fprintf(os.Stderr, "  картинки: %d/%d\n", i+1, len(queue))
		}
	}
	if len(failed) > 0 { // для нескачанных оставляем ссылку на сайт
		for _, imgs := range all {
			for i := range *imgs {
				im := &(*imgs)[i]
				if failed[im.File] {
					for _, h := range htmls {
						*h = strings.ReplaceAll(*h, `src="`+im.File+`"`, `src="`+im.URL+`"`)
					}
					im.File = ""
				}
			}
		}
	}
	return nil
}

func outputName(o options, alias string) string {
	parts := []string{o.exam, alias}
	if o.line != 0 {
		parts = append(parts, "n"+strconv.Itoa(o.line))
	}
	if o.id != "" {
		parts = append(parts, "id"+strings.ToUpper(o.id))
	}
	if o.kes != "" {
		parts = append(parts, "kes"+strings.ReplaceAll(o.kes, ",", "+"))
	}
	if o.kind != "" {
		parts = append(parts, strings.ReplaceAll(o.kind, ",", "+"))
	}
	return strings.Join(parts, "_") + ".json"
}

func splitList(s string) []string {
	var out []string
	for _, p := range strings.Split(s, ",") {
		if p = strings.ToLower(strings.TrimSpace(p)); p != "" {
			out = append(out, p)
		}
	}
	return out
}

// expandThemes раскрывает раздел в подтемы: "7" → 7, 7.1, 7.2, …
// Сайт при выборе раздела отправляет все подтемы, делаем так же.
func expandThemes(patterns []string, themes []Theme) []string {
	var out []string
	for _, p := range patterns {
		found := false
		for _, t := range themes {
			code := strings.TrimSuffix(t.Code, ".")
			if code == p || strings.HasPrefix(code, p+".") {
				out = append(out, t.Code)
				found = true
			}
		}
		if !found {
			out = append(out, p)
		}
	}
	slices.Sort(out)
	return slices.Compact(out)
}
