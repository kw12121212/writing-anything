package decision

import (
	"os"
	"path/filepath"
	"regexp"
	"slices"
	"strings"
	"testing"
	"time"

	"github.com/kw12121212/writing-anything/internal/workspace"
)

func newRoot(t *testing.T) string {
	t.Helper()
	root := t.TempDir()
	if _, ps := workspace.Init(root, nil); len(ps) > 0 {
		t.Fatal(ps)
	}
	return root
}

// units 只把 my-post 与 my-post/intro 当作存在的单元。
func units(ref string) []workspace.Problem {
	if ref == "my-post" || ref == "my-post/intro" {
		return nil
	}
	return []workspace.Problem{{Code: "work.not-found", Severity: "error", Message: ref}}
}

func exists(ref string) bool { return units(ref) == nil }

func file(root string) string { return filepath.Join(root, filepath.FromSlash(Path)) }

func read(t *testing.T, root string) string {
	t.Helper()
	data, err := os.ReadFile(file(root))
	if err != nil {
		t.Fatal(err)
	}
	return string(data)
}

func write(t *testing.T, root, content string) {
	t.Helper()
	if err := os.WriteFile(file(root), []byte(content), 0o644); err != nil {
		t.Fatal(err)
	}
}

var when = time.Date(2026, 9, 30, 12, 0, 0, 0, time.FixedZone("CST", 8*3600))

func TestAdd(t *testing.T) {
	root := newRoot(t)
	d, ps := Add(root, Input{Question: "标题用哪个", Options: []string{"迁移实录", "踩坑复盘"}, Recommend: "b", Rationale: "更贴近读者", Blocking: true, Units: []string{"my-post"}}, units)
	if len(ps) > 0 {
		t.Fatal(ps)
	}
	if d.ID != "dec-1" || !slices.Equal(d.Options, []Option{{"a", "迁移实录"}, {"b", "踩坑复盘"}}) || *d.Recommendation != "b" || !d.Blocking ||
		!slices.Equal(d.Units, []string{"my-post"}) || d.Status != "open" || d.Choice != nil {
		t.Fatalf("decision = %+v", d)
	}
	write(t, root, "# 作者的注释\n"+read(t, root))
	d, ps = Add(root, Input{Question: "字数", ID: "words"}, units)
	if len(ps) > 0 || d.ID != "words" || d.Blocking || d.Options == nil || len(d.Options) != 0 || d.Recommendation != nil {
		t.Fatalf("second = %+v, %v", d, ps)
	}
	if !strings.Contains(read(t, root), "# 作者的注释") {
		t.Error("comment lost")
	}
	if d, _ = Add(root, Input{Question: "q"}, units); d.ID != "dec-3" {
		t.Errorf("next id = %s", d.ID)
	}
}

func TestAddRejects(t *testing.T) {
	root := newRoot(t)
	many := make([]string, 27)
	for i := range many {
		many[i] = "x"
	}
	Add(root, Input{Question: "q", ID: "dup"}, units)
	before := read(t, root)
	for _, tc := range []struct {
		name        string
		in          Input
		code, field string
	}{
		{"unknown recommendation", Input{Question: "q", Options: []string{"x"}, Recommend: "c"}, "decision.unknown-option", "recommendation"},
		{"recommendation without options", Input{Question: "q", Recommend: "a"}, "decision.unknown-option", "recommendation"},
		{"unknown unit", Input{Question: "q", Units: []string{"nope"}}, "work.not-found", ""},
		{"blank question", Input{Question: " "}, "artifact.schema-violation", "question"},
		{"too many options", Input{Question: "q", Options: many}, "artifact.schema-violation", "options"},
		{"blank option", Input{Question: "q", Options: []string{" "}}, "artifact.schema-violation", "options[0].text"},
		{"bad id", Input{Question: "q", ID: "Bad"}, "artifact.schema-violation", "id"},
		{"duplicate id", Input{Question: "q", ID: "dup"}, "artifact.duplicate-id", "id"},
	} {
		if _, ps := Add(root, tc.in, units); len(ps) != 1 || ps[0].Code != tc.code || ps[0].Field != tc.field {
			t.Errorf("%s: %+v", tc.name, ps)
		}
	}
	if read(t, root) != before {
		t.Fatal("decisions.yaml changed")
	}
	write(t, root, "schema_version: 1\ndecisions: &d []\n")
	if _, ps := Add(root, Input{Question: "q"}, units); len(ps) != 1 || ps[0].Code != "decision.anchored-value" {
		t.Errorf("anchored: %v", ps)
	}
}

func TestDecide(t *testing.T) {
	root := newRoot(t)
	Add(root, Input{Question: "标题", Options: []string{"甲", "乙"}, Blocking: true, Units: []string{"my-post"}}, units)
	Add(root, Input{Question: "字数"}, units)
	d, ps := Decide(root, "dec-1", "b", "", "作者说就用乙", when)
	if len(ps) > 0 || d.Status != "decided" || *d.Choice != "b" || *d.Confirmation != "作者说就用乙" || *d.DecidedAt != "2026-09-30T04:00:00Z" || d.Answer != nil {
		t.Fatalf("decide = %+v, %v", d, ps)
	}
	d, ps = Decide(root, "dec-2", "", "控制在 3000 字", "作者确认 3000 字", when)
	if len(ps) > 0 || d.Choice != nil || *d.Answer != "控制在 3000 字" {
		t.Fatalf("free answer = %+v, %v", d, ps)
	}
	Add(root, Input{Question: "开头", Options: []string{"甲", "乙"}}, units)
	before := read(t, root)
	for _, tc := range []struct {
		name, id, option, answer, confirmation string
		code, field                            string
	}{
		{"missing", "dec-9", "a", "", "x", "decision.not-found", ""},
		{"twice", "dec-1", "a", "", "x", "decision.already-decided", "decisions[0].status"},
		{"option required", "dec-3", "", "说明", "x", "decision.choice-required", ""},
		{"unknown option", "dec-3", "c", "", "x", "decision.unknown-option", "choice"},
		{"no confirmation", "dec-3", "a", "", " ", "artifact.schema-violation", "confirmation"},
	} {
		if _, ps := Decide(root, tc.id, tc.option, tc.answer, tc.confirmation, when); len(ps) != 1 || ps[0].Code != tc.code || ps[0].Field != tc.field {
			t.Errorf("%s: %+v", tc.name, ps)
		}
	}
	if read(t, root) != before {
		t.Fatal("rejected decide changed decisions.yaml")
	}
	Add(root, Input{Question: "自由"}, units)
	if _, ps := Decide(root, "dec-4", "", "", "x", when); len(ps) != 1 || ps[0].Code != "decision.choice-required" {
		t.Errorf("answer required: %v", ps)
	}
	// 有选项时 --answer 作为补充说明。
	if d, ps := Decide(root, "dec-3", "a", "开头用故事", "作者同意", when); len(ps) > 0 || *d.Answer != "开头用故事" {
		t.Errorf("option with answer = %+v, %v", d, ps)
	}
	write(t, root, "schema_version: 1\ndecisions:\n  - {id: dec-1, question: q, options: [], blocking: true, units: [], status: &s open}\n")
	if _, ps := Decide(root, "dec-1", "", "x", "y", when); len(ps) != 1 || ps[0].Code != "decision.anchored-value" || ps[0].Field != "decisions[0].status" {
		t.Errorf("anchored status: %v", ps)
	}
}

func TestBlockingAndCheck(t *testing.T) {
	root := newRoot(t)
	if ds, invalid := Blocking(root, "my-post"); ds != nil || invalid {
		t.Fatal("no queue must not block")
	}
	if checked, ps := Check(root, exists); checked != nil || ps != nil {
		t.Fatal("no queue must not be checked")
	}
	Add(root, Input{Question: "阻塞", Blocking: true, Units: []string{"my-post", "my-post/intro"}}, units)
	Add(root, Input{Question: "非阻塞", Units: []string{"my-post"}}, units)
	Add(root, Input{Question: "已决定", Blocking: true, Units: []string{"my-post"}}, units)
	Decide(root, "dec-3", "", "x", "y", when)
	if ds, _ := Blocking(root, "my-post"); len(ds) != 1 || ds[0].ID != "dec-1" {
		t.Errorf("Blocking(my-post) = %+v", ds)
	}
	if ds, _ := Blocking(root, "other"); len(ds) != 0 {
		t.Errorf("Blocking(other) = %+v", ds)
	}
	if checked, ps := Check(root, exists); !slices.Equal(checked, []string{Path}) || len(ps) != 0 {
		t.Fatalf("Check = %v, %v", checked, ps)
	}
	if _, ps := Check(root, func(ref string) bool { return ref == "my-post" }); len(ps) != 1 || ps[0].Code != "decision.unknown-unit" || ps[0].Field != "decisions[0].units[1]" {
		t.Errorf("unknown unit: %v", ps)
	}
	for _, tc := range []struct{ content, code, field string }{
		{"a: [\n", "artifact.parse-error", ""},
		{"schema_version: 1\ndecisions:\n  - {id: dec-1, question: q, options: [], blocking: true, units: [], status: decided, decided_at: \"2026-09-30T12:00:00Z\"}\n", "artifact.schema-violation", "decisions[0].confirmation"},
		{"schema_version: 1\ndecisions:\n  - {id: dec-1, question: q, options: [{id: b, text: x}], blocking: true, units: [], status: open}\n", "artifact.schema-violation", "decisions[0].options[0].id"},
		{"schema_version: 1\ndecisions:\n  - {id: dec-1, question: q, options: [], recommendation: a, blocking: true, units: [], status: open}\n", "decision.unknown-option", "decisions[0].recommendation"},
		{"schema_version: 1\ndecisions:\n  - {id: dec-1, question: q, options: [], blocking: true, units: [], status: open}\n  - {id: dec-1, question: q, options: [], blocking: true, units: [], status: open}\n", "artifact.duplicate-id", "decisions[1].id"},
	} {
		write(t, root, tc.content)
		if _, ps := Check(root, exists); len(ps) != 1 || ps[0].Code != tc.code || ps[0].Field != tc.field {
			t.Errorf("%q: %+v", tc.content, ps)
		}
		if _, invalid := Blocking(root, "my-post"); !invalid {
			t.Errorf("%q: an invalid queue must block", tc.content)
		}
	}
	// 决策队列是符号链接：不读取，按无法加载处理。
	os.Remove(file(root))
	outside := filepath.Join(t.TempDir(), "d.yaml")
	os.WriteFile(outside, []byte("schema_version: 1\ndecisions: []\n"), 0o644)
	os.Symlink(outside, file(root))
	if _, invalid := Blocking(root, "my-post"); !invalid {
		t.Error("a symlinked queue must block")
	}
}

func TestDecidedAtFormat(t *testing.T) {
	root := newRoot(t)
	Add(root, Input{Question: "q"}, units)
	d, _ := Decide(root, "dec-1", "", "a", "b", time.Now())
	if !regexp.MustCompile(`^\d{4}-\d{2}-\d{2}T\d{2}:\d{2}:\d{2}Z$`).MatchString(*d.DecidedAt) {
		t.Errorf("decided_at = %s", *d.DecidedAt)
	}
}

func TestReplace(t *testing.T) {
	root := t.TempDir()
	os.MkdirAll(filepath.Join(root, ".writing-anything"), 0o755)
	if _, ps := Replace(root, "dec-1", []string{"a"}, "", ""); len(ps) != 1 || ps[0].Code != CodeNotFound {
		t.Fatalf("not found: %+v", ps)
	}
	if _, ps := Add(root, Input{Question: "用哪个？", Options: []string{"甲", "乙"}, Recommend: "a", Rationale: "短", Units: []string{"post"}}, func(string) []workspace.Problem { return nil }); ps != nil {
		t.Fatal(ps)
	}
	d, ps := Replace(root, "dec-1", []string{"丙", "丁", "戊"}, "c", "")
	if ps != nil || len(d.Options) != 3 || d.Options[2].ID != "c" || *d.Recommendation != "c" || d.Rationale != nil || d.Question != "用哪个？" || d.Units[0] != "post" {
		t.Fatalf("replace: %+v %+v", d, ps)
	}
	for name, tc := range map[string]struct {
		options   []string
		recommend string
		code      string
	}{
		"bad recommend": {[]string{"甲"}, "b", CodeUnknownOption},
		"too many":      {make([]string, MaxOptions+1), "", "artifact.schema-violation"},
	} {
		if _, ps := Replace(root, "dec-1", tc.options, tc.recommend, ""); len(ps) != 1 || ps[0].Code != tc.code {
			t.Errorf("%s: %+v", name, ps)
		}
	}
	if _, ps := Decide(root, "dec-1", "a", "", "作者：甲", time.Now()); ps != nil {
		t.Fatal(ps)
	}
	if _, ps := Replace(root, "dec-1", []string{"甲"}, "", ""); len(ps) != 1 || ps[0].Code != CodeAlreadyDecided {
		t.Fatalf("decided: %+v", ps)
	}
	// 选项经由锚点得到时不修改。
	os.WriteFile(filepath.Join(root, filepath.FromSlash(Path)), []byte("schema_version: 1\ndecisions:\n  - id: dec-1\n    question: q\n    options: &o [{id: a, text: 甲}]\n    blocking: true\n    units: []\n    status: open\n"), 0o644)
	if _, ps := Replace(root, "dec-1", []string{"乙"}, "", ""); len(ps) != 1 || ps[0].Code != CodeAnchoredValue {
		t.Fatalf("anchored: %+v", ps)
	}
}

func TestAddAll(t *testing.T) {
	root := newRoot(t)
	none := func(string) []workspace.Problem { return nil }
	if _, ps := Add(root, Input{Question: "先有的", Options: []string{"甲"}}, none); len(ps) > 0 {
		t.Fatal(ps)
	}
	ds, ps := AddAll(root, []Input{{Question: "一", Options: []string{"甲", "乙"}, Recommend: "b"}, {Question: "二"}}, none)
	if len(ps) > 0 || len(ds) != 2 || ds[0].ID != "dec-2" || ds[1].ID != "dec-3" || ds[1].Question != "二" {
		t.Fatalf("got %+v %+v", ds, ps)
	}
	all, _, _ := Load(root)
	if len(all) != 3 {
		t.Fatalf("queue = %+v", all)
	}
	before := readQueue(t, root)
	if _, ps := AddAll(root, []Input{{Question: "好"}, {Question: "坏", Options: []string{"甲"}, Recommend: "z"}}, none); len(ps) != 1 || ps[0].Code != "decision.unknown-option" {
		t.Fatalf("bad recommend: %+v", ps)
	}
	if _, ps := AddAll(root, []Input{{ID: "x"}, {ID: "x"}}, none); len(ps) != 1 || ps[0].Code != "artifact.duplicate-id" {
		t.Fatalf("duplicate in batch: %+v", ps)
	}
	if readQueue(t, root) != before {
		t.Fatal("queue written on failure")
	}
}

func readQueue(t *testing.T, root string) string {
	t.Helper()
	data, err := os.ReadFile(filepath.Join(root, Path))
	if err != nil {
		t.Fatal(err)
	}
	return string(data)
}
