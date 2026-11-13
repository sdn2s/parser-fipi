package main

import (
	"fmt"
	"regexp"
	"sort"
	"strings"
)

// Короткие имена предметов → название на странице банка.
// GUID предметов берутся с сайта при каждом запуске, поэтому
// смена GUID в новом учебном году ничего не ломает.
var subjectAliases = map[string]map[string]string{
	"ege": {
		"eng":          "Английский язык",
		"bio":          "Биология",
		"geo":          "География",
		"inf":          "Информатика и ИКТ",
		"esp":          "Испанский язык",
		"his":          "История",
		"chn":          "Китайский язык",
		"lit":          "Литература",
		"math-base":    "Математика. Базовый уровень",
		"math-profile": "Математика. Профильный уровень",
		"ger":          "Немецкий язык",
		"soc":          "Обществознание",
		"rus":          "Русский язык",
		"phys":         "Физика",
		"fra":          "Французский язык",
		"chem":         "Химия",
		"izl":          "Итоговое изложение",
	},
	"oge": {
		"eng":     "Английский язык",
		"bio":     "Биология",
		"geo":     "География",
		"inf":     "Информатика",
		"esp":     "Испанский язык",
		"his":     "История",
		"lit":     "Литература",
		"math":    "Математика",
		"ger":     "Немецкий язык",
		"soc":     "Обществознание",
		"rus":     "Русский язык",
		"phys":    "Физика",
		"fra":     "Французский язык",
		"chem":    "Химия",
		"natsci":  "Банк заданий для оценки естественнонаучной грамотности",
		"reading": "Банк заданий для оценки читательской грамотности",
		"rus-os":  "Открытый банк оценочных средств по русскому языку",
	},
}

// Синонимы, чтобы `-subject math` работал и для ЕГЭ.
var aliasSynonyms = map[string]map[string]string{
	"ege": {"math": "math-profile", "mathp": "math-profile", "mathb": "math-base", "info": "inf", "physics": "phys"},
	"oge": {"info": "inf", "physics": "phys"},
}

var reGUID = regexp.MustCompile(`^[0-9A-Fa-f]{32}$`)

func examHost(exam string) (string, error) {
	switch exam {
	case "ege":
		return "ege.fipi.ru", nil
	case "oge":
		return "oge.fipi.ru", nil
	}
	return "", fmt.Errorf("неизвестный экзамен %q: используйте ege или oge", exam)
}

func canonicalAlias(exam, s string) string {
	s = strings.ToLower(strings.TrimSpace(s))
	if a, ok := aliasSynonyms[exam][s]; ok {
		return a
	}
	return s
}

func aliasFor(exam, name string) string {
	for a, n := range subjectAliases[exam] {
		if strings.EqualFold(n, name) {
			return a
		}
	}
	return ""
}

// resolveSubject ищет предмет по короткому имени, GUID или названию.
func resolveSubject(exam, query string, subjects []Subject) (Subject, string, error) {
	alias := canonicalAlias(exam, query)
	want := subjectAliases[exam][alias]
	for _, s := range subjects {
		switch {
		case want != "" && strings.EqualFold(s.Name, want),
			reGUID.MatchString(query) && strings.EqualFold(s.GUID, query),
			strings.EqualFold(s.Name, strings.TrimSpace(query)):
			a := aliasFor(exam, s.Name)
			if a == "" {
				a = strings.ToLower(s.GUID)
			}
			return s, a, nil
		}
	}
	if reGUID.MatchString(query) { // GUID, которого нет в списке, — пробуем как есть
		return Subject{GUID: strings.ToUpper(query), Name: query}, strings.ToLower(query), nil
	}
	return Subject{}, "", fmt.Errorf("предмет %q не найден; список: -list-subjects", query)
}

func printSubjects(exam string, subjects []Subject) {
	sort.SliceStable(subjects, func(i, j int) bool { return subjects[i].Group < subjects[j].Group })
	group := ""
	for _, s := range subjects {
		if s.Group != group {
			group = s.Group
			fmt.Printf("\n%s\n", group)
		}
		a := aliasFor(exam, s.Name)
		if a == "" {
			a = "-"
		}
		fmt.Printf("  %-13s %s  %s\n", a, s.GUID, s.Name)
	}
}
