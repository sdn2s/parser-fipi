package main

import (
	"fmt"
	"os"
	"path/filepath"
	"sort"
	"strings"
	"testing"
)

// Синтетическая страница в разметке questions.php (не реальные задания ФИПИ).
const fixturePage = `<html><head><script>var qfiles_location='../../';</script></head><body>
<div class="qblock" id='qAAA111'> <div id="hint" class="hint" name="hint">Впишите правильный ответ.</div>
<form name='checkformAAA111'><input type="Hidden" name="guid" value="GUID1">
<script language='JavaScript'> function setAnswerAAA111(aform){;} </script>
<TABLE><TR><TD class='cell_0'><p class="MsoNormal">Даны векторы <m:math><m:mi>a</m:mi><m:mo>(</m:mo><m:mn>3</m:mn><m:mo>)</m:mo></m:math> и <a name="_GoBack"></a>b. Найдите длину.</p>
<p><script language='javascript'> ShowPictureQ('docs/P/questions/X(copy1)/pic_1.gif');</script></p></TD></TR>
<TR><TD><TABLE class='submit-outblock'><TR><TD class='varinats-block'><div><table><tr><td><input type=text name="answer"></td><td>%&nbsp;</td></tr></table></div></TD></TR></TABLE></TD></TR></TABLE></form></div>
<div id='iAAA111'><div class="task-header-panel"><div class="task-info-panel"><div class="task-info-content"><table><tbody>
<tr><td class="param-name">КЭС:</td><td class="param-row"><div>7.5 Координаты и векторы</div></td></tr>
<tr><td class="param-name">Тип ответа:</td><td>Краткий ответ</td></tr></tbody></table></div></div>
<div class="id-text">Номер: <span class="canselect">AAA111</span></div></div></div>

<div class="qblock">
<div id="hint" class="hint" name="hint">Прочитайте текст и выполните задания.</div> <script language='JavaScript'>
files_location='../../docs/P/docs/D1/';
</script><p>Общий текст про шины.</p><script language='javascript'> ShowPicture('xs3docsrc_1.jpg');</script></div>
<div class="qblock" id='qBBB222'> <div id="hint" class="hint" name="hint">Задание №2. Выберите один или несколько правильных ответов.</div>
<form><input type="Hidden" name="guid" value="GUID2"><TABLE><TR><TD class='cell_0'><p>Выберите верные утверждения.</p></TD></TR>
<TR><TD><TABLE><TR><TD class='varinats-block'><table class="distractors-table">
<tr><td><input type="checkbox" name="test0"></td><td>&nbsp;<b>1)</b>&nbsp;</td><td onclick='x()'><p>Первое</p></td></tr>
<tr><td><input type="checkbox" name="test1"></td><td>&nbsp;<b>2)</b>&nbsp;</td><td><p>Второе</p></td></tr>
</table></TD></TR></TABLE></TD></TR></TABLE></form></div>
<div id='iBBB222'><div class="task-info-panel"><table><tbody>
<tr><td class="param-name">КЭС:</td><td class="param-row"><div>3.3 Решение текстовых задач</div><div>7.5 Измерение</div></td></tr>
<tr><td class="param-name">Тип ответа:</td><td>Выбор ответов из предложенных вариантов</td></tr></tbody></table></div></div>
<script>setQCount(2); setQCount(2, 1, 100);</script>
</body></html>`

func TestParseQuestionsPage(t *testing.T) {
	qp, err := parseQuestionsPage(fixturePage, "https://ege.fipi.ru/bank/questions.php")
	if err != nil {
		t.Fatal(err)
	}
	if qp.Total != 2 || len(qp.Tasks) != 2 {
		t.Fatalf("total=%d tasks=%d, want 2/2", qp.Total, len(qp.Tasks))
	}

	a := qp.Tasks[0]
	if a.ID != "AAA111" || a.GUID != "GUID1" || a.Kind != "short" || a.AnswerSuffix != "%" {
		t.Errorf("task A: %+v", a)
	}
	if len(a.KES) != 1 || a.KES[0].Code != "7.5" || a.KES[0].Name != "Координаты и векторы" {
		t.Errorf("KES: %+v", a.KES)
	}
	if !strings.Contains(a.HTML, `<math xmlns="http://www.w3.org/1998/Math/MathML">`) || strings.Contains(a.HTML, "m:") {
		t.Errorf("MathML не нормализован: %s", a.HTML)
	}
	if strings.Contains(a.HTML, "<script") || strings.Contains(a.HTML, "_GoBack") {
		t.Errorf("скрипты/якоря не вычищены: %s", a.HTML)
	}
	if !strings.Contains(a.Text, "a(3)") || !strings.Contains(a.Text, "Найдите длину") {
		t.Errorf("text: %q", a.Text)
	}
	if len(a.Images) != 1 || a.Images[0].URL != "https://ege.fipi.ru/docs/P/questions/X(copy1)/pic_1.gif" ||
		!strings.Contains(a.HTML, `src="`+a.Images[0].File+`"`) {
		t.Errorf("images: %+v\n%s", a.Images, a.HTML)
	}

	b := qp.Tasks[1]
	if b.Kind != "multi" || b.GroupPos != 2 || len(b.Option) != 2 || b.Option[1].N != "2" || b.Option[1].Text != "Второе" {
		t.Errorf("task B: %+v", b)
	}
	if strings.Contains(b.Option[0].HTML, "onclick") {
		t.Errorf("onclick остался: %s", b.Option[0].HTML)
	}
	cx := qp.Contexts[b.ContextID]
	if cx == nil || !strings.Contains(cx.Text, "Общий текст про шины") || strings.Contains(cx.Text, "Прочитайте") {
		t.Fatalf("context: %+v", cx)
	}
	if len(cx.Images) != 1 || cx.Images[0].URL != "https://ege.fipi.ru/docs/P/docs/D1/xs3docsrc_1.jpg" {
		t.Errorf("context images: %+v", cx.Images)
	}
}

func TestMappingsCompile(t *testing.T) {
	for _, name := range AvailableMappings() {
		exam, subj, _ := strings.Cut(name, "_")
		m, err := LoadMapping("", exam, subj)
		if err != nil || m == nil {
			t.Fatalf("%s: %v", name, err)
		}
		if len(m.LineList()) == 0 {
			t.Errorf("%s: пустой маппинг", name)
		}
	}
}

func TestExpandThemes(t *testing.T) {
	th := []Theme{{Code: "7"}, {Code: "7.1"}, {Code: "7.10"}, {Code: "17.1"}, {Code: "2.1"}}
	got := strings.Join(expandThemes([]string{"7", "2.1"}, th), ",")
	if got != "2.1,7,7.1,7.10" {
		t.Errorf("got %s", got)
	}
}

// Прогон по реальному дампу страниц (не входит в репозиторий):
//
//	FIPI_DUMP_DIR=/path/to/dump go test -run Dump -v
//
// Файлы: <exam>_<page>.html в UTF-8.
func TestDumpDistribution(t *testing.T) {
	dir := os.Getenv("FIPI_DUMP_DIR")
	if dir == "" {
		t.Skip("FIPI_DUMP_DIR не задан")
	}
	for exam, subj := range map[string]string{"ege": "math-profile", "oge": "math"} {
		m, _ := LoadMapping("", exam, subj)
		files, _ := filepath.Glob(filepath.Join(dir, exam+"_*.html"))
		counts := map[string]int{}
		n, withText, withCtx := 0, 0, 0
		for _, f := range files {
			data, _ := os.ReadFile(f)
			host := exam + ".fipi.ru"
			qp, err := parseQuestionsPage(string(data), "https://"+host+"/bank/questions.php")
			if err != nil {
				t.Fatal(err)
			}
			for _, tk := range qp.Tasks {
				n++
				if tk.Text != "" || len(tk.Images) > 0 {
					withText++
				}
				if tk.ContextID != "" {
					withCtx++
				}
				key := "—"
				if r := m.Classify(tk); r != nil {
					key = fmt.Sprint(r.Lines)
				}
				counts[key]++
			}
		}
		var keys []string
		for k := range counts {
			keys = append(keys, k)
		}
		sort.Strings(keys)
		t.Logf("%s: заданий %d, с условием %d, с общим текстом %d", exam, n, withText, withCtx)
		for _, k := range keys {
			t.Logf("  %-14s %d", k, counts[k])
		}
	}
}
