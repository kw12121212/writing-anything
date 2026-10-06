package series

import (
	"os"
	"path/filepath"
	"slices"
	"strings"
	"testing"
	"time"

	"github.com/kw12121212/writing-anything/internal/draft"
	"github.com/kw12121212/writing-anything/internal/review"
	"github.com/kw12121212/writing-anything/internal/source"
	"github.com/kw12121212/writing-anything/internal/work"
	"github.com/kw12121212/writing-anything/internal/workspace"
)

var now = time.Date(2026, 10, 2, 8, 0, 0, 0, time.UTC)

func newRoot(t *testing.T, works ...string) string {
	t.Helper()
	root := t.TempDir()
	if _, ps := workspace.Init(root, nil); len(ps) > 0 {
		t.Fatal(ps)
	}
	for _, id := range works {
		if _, _, ps := work.Create(root, "minimal", id); len(ps) > 0 {
			t.Fatal(ps)
		}
	}
	return root
}

func codes(ps []workspace.Problem) []string {
	var out []string
	for _, p := range ps {
		out = append(out, p.Code)
	}
	return out
}

func strs(s ...string) *[]string { return &s }

func TestSeriesLifecycle(t *testing.T) {
	root := newRoot(t, "part-1", "part-2", "other")
	if _, _, ps := source.Add(root, []byte("访谈"), "interview", "", "s1", "a.md"); len(ps) > 0 {
		t.Fatal(ps)
	}
	title := "入门指南"
	s, changed, ps := Set(root, "guide", Edit{Title: &title, Packs: strs("default")})
	if ps != nil || !slices.Equal(changed, []string{".writing-anything/series/guide.yaml"}) || s.Title != title || !slices.Equal(s.Packs, []string{"default"}) || s.Works == nil || s.Facts == nil {
		t.Fatalf("create: %+v %v %+v", s, changed, ps)
	}
	if _, changed, ps := Set(root, "guide", Edit{Title: &title}); ps != nil || changed != nil {
		t.Fatalf("unchanged: %v %+v", changed, ps)
	}
	file := filepath.Join(root, ".writing-anything/series/guide.yaml")
	os.WriteFile(file, []byte("# 作者的注释\n"+string(mustRead(t, file))), 0o644)
	for _, w := range []string{"part-1", "part-2", "part-1"} {
		if _, _, ps := Add(root, "guide", w); ps != nil {
			t.Fatal(ps)
		}
	}
	s, _, ps = SetFact(root, "guide", Fact{ID: "audience", Text: "读者是后端工程师", Sources: []string{"s1"}})
	if ps != nil || !slices.Equal(s.Works, []string{"part-1", "part-2"}) || len(s.Facts) != 1 {
		t.Fatalf("add/fact: %+v %+v", s, ps)
	}
	s, _, _ = SetFact(root, "guide", Fact{ID: "audience", Text: "读者是前端工程师"})
	if len(s.Facts) != 1 || s.Facts[0].Text != "读者是前端工程师" || s.Facts[0].Sources == nil {
		t.Fatalf("replace fact: %+v", s.Facts)
	}
	if !strings.Contains(string(mustRead(t, file)), "# 作者的注释") {
		t.Fatal("comment lost")
	}
	if got, _ := Of(root, "part-2"); got == nil || got.ID != "guide" {
		t.Fatalf("of = %+v", got)
	}
	if got, _ := Of(root, "other"); got != nil {
		t.Fatalf("other = %+v", got)
	}
	Set(root, "second", Edit{})
	if _, _, ps := Add(root, "second", "part-1"); !slices.Equal(codes(ps), []string{"series.already-member"}) {
		t.Fatalf("already member: %+v", ps)
	}
	if _, _, ps := Add(root, "nope", "part-1"); !slices.Equal(codes(ps), []string{"series.not-found"}) {
		t.Fatalf("not found: %+v", ps)
	}
	if _, _, ps := Add(root, "guide", "ghost"); !slices.Equal(codes(ps), []string{"work.not-found"}) {
		t.Fatalf("ghost: %+v", ps)
	}
	if _, _, ps := Set(root, "guide", Edit{Packs: strs("nope")}); !slices.Equal(codes(ps), []string{"config.unknown-pack"}) {
		t.Fatalf("pack: %+v", ps)
	}
	if _, _, ps := Set(root, "guide", Edit{Sources: strs("nope")}); !slices.Equal(codes(ps), []string{"series.unknown-source"}) {
		t.Fatalf("source: %+v", ps)
	}
	if _, _, ps := SetFact(root, "guide", Fact{ID: "x", Text: "y", Sources: []string{"nope"}}); !slices.Equal(codes(ps), []string{"series.unknown-source"}) {
		t.Fatalf("fact source: %+v", ps)
	}
	if checked, ps := Check(root); len(checked) != 2 || ps != nil {
		t.Fatalf("check: %v %+v", checked, ps)
	}
}

func mustRead(t *testing.T, path string) []byte {
	t.Helper()
	data, err := os.ReadFile(path)
	if err != nil {
		t.Fatal(err)
	}
	return data
}

func TestCheckProblems(t *testing.T) {
	root := newRoot(t, "part-1")
	dir := filepath.Join(root, ".writing-anything/series")
	os.MkdirAll(dir, 0o755)
	os.WriteFile(filepath.Join(dir, "a.yaml"), []byte("schema_version: 1\nid: a\ntitle: A\nworks: [ghost, part-1]\npacks: [nope]\nsources: [s9]\nfacts:\n  - {id: f, text: t, sources: [s9]}\n  - {id: f, text: t, sources: []}\n"), 0o644)
	if _, ps := Check(root); !slices.Equal(codes(ps), []string{"artifact.duplicate-id"}) {
		t.Fatalf("duplicate: %+v", ps)
	}
	os.WriteFile(filepath.Join(dir, "a.yaml"), []byte("schema_version: 1\nid: a\ntitle: A\nworks: [ghost, part-1]\npacks: [nope]\nsources: [s9]\nfacts:\n  - {id: f, text: t, sources: [s9]}\n"), 0o644)
	os.WriteFile(filepath.Join(dir, "b.yaml"), []byte("schema_version: 1\nid: c\ntitle: B\nworks: [part-1]\npacks: []\nsources: []\nfacts: []\n"), 0o644)
	_, ps := Check(root)
	var got []string
	for _, p := range ps {
		got = append(got, p.Code+" "+p.Field)
	}
	want := []string{"series.id-mismatch id", "work.not-found works[0]", "config.unknown-pack packs[0]", "series.unknown-source sources[0]", "series.unknown-source facts[0].sources[0]"}
	if !slices.Equal(got, want) {
		t.Fatalf("problems = %v", got)
	}
	os.WriteFile(filepath.Join(dir, "b.yaml"), []byte("schema_version: 1\nid: b\ntitle: B\nworks: [part-1]\npacks: []\nsources: []\nfacts: []\n"), 0o644)
	if _, ps := Check(root); !slices.Contains(codes(ps), "series.already-member") {
		t.Fatalf("member twice: %+v", ps)
	}
	if ps := CheckArtifact(root, ".writing-anything/series/a.yaml"); ps != nil {
		t.Fatalf("artifact: %+v", ps)
	}
	os.Remove(filepath.Join(dir, "b.yaml"))
	os.WriteFile(filepath.Join(dir, "a.yaml"), []byte("schema_version: 1\nid: a\ntitle: A\nworks: &w []\npacks: []\nsources: []\nfacts: []\n"), 0o644)
	if _, _, ps := Add(root, "a", "part-1"); !slices.Equal(codes(ps), []string{"series.anchored-value"}) {
		t.Fatalf("anchored: %+v", ps)
	}
}

// accepted 写入正文并记录与之一致的验收。
func accepted(t *testing.T, root, ref, body string) (*work.Work, *work.Unit) {
	t.Helper()
	w, u, _ := work.Resolve(root, ref)
	if _, _, ps := draft.Write(root, w, u, []byte(body), "", time.Date(2026, 6, 20, 12, 0, 0, 0, time.UTC)); len(ps) > 0 {
		t.Fatal(ps)
	}
	if _, _, ps := review.Accept(root, w.UnitDir(u), "fast", review.SHA256([]byte(body)), false, "", now); len(ps) > 0 {
		t.Fatal(ps)
	}
	return w, u
}

func TestSummary(t *testing.T) {
	root := newRoot(t, "part-1", "part-2")
	w, u, _ := work.Resolve(root, "part-1")
	text := strings.Repeat("摘", 30)
	length := w.Genre.SummaryLength
	if length.Min != 20 || length.Max != 500 {
		t.Fatalf("default length = %+v", length)
	}
	if _, _, ps := SetSummary(root, w, u, text, length, now); !slices.Equal(codes(ps), []string{"summary.not-accepted"}) {
		t.Fatalf("not accepted: %+v", ps)
	}
	w, u = accepted(t, root, "part-1", "第一篇正文。\n")
	if _, _, ps := SetSummary(root, w, u, "太短", length, now); !slices.Equal(codes(ps), []string{"summary.length"}) || !strings.Contains(ps[0].Message, "20 到 500") {
		t.Fatalf("too short: %+v", ps)
	}
	s, changed, ps := SetSummary(root, w, u, text, length, now)
	if ps != nil || !slices.Equal(changed, []string{".writing-anything/works/part-1/summary.yaml"}) || s.Stale || s.Chars != 30 {
		t.Fatalf("set: %+v %v %+v", s, changed, ps)
	}
	if _, changed, _ := SetSummary(root, w, u, text, length, now); changed != nil {
		t.Fatalf("unchanged rewrite: %v", changed)
	}
	if s, _ := LoadSummary(root, w, u); s == nil || s.Stale || s.Text != text {
		t.Fatalf("load: %+v", s)
	}
	os.WriteFile(filepath.Join(root, ".writing-anything/works/part-1/draft.md"), []byte("改过的正文。\n"), 0o644)
	if s, _ := LoadSummary(root, w, u); !s.Stale {
		t.Fatal("not stale after edit")
	}
	// 验收之后正文被修改：不能再写摘要。
	if _, _, ps := SetSummary(root, w, u, text, length, now); !slices.Equal(codes(ps), []string{"summary.not-accepted"}) {
		t.Fatalf("stale acceptance: %+v", ps)
	}
	all, _, _ := work.LoadAll(root)
	if checked, ps := CheckSummaries(root, all); len(checked) != 1 || ps != nil {
		t.Fatalf("check: %v %+v", checked, ps)
	}
	os.WriteFile(filepath.Join(root, ".writing-anything/works/part-1/summary.yaml"), []byte("text: ["), 0o644)
	if ps := CheckArtifact(root, ".writing-anything/works/part-1/summary.yaml"); !slices.Equal(codes(ps), []string{"artifact.parse-error"}) {
		t.Fatalf("parse: %+v", ps)
	}
}

func TestPrecedingSummaries(t *testing.T) {
	root := newRoot(t, "part-1", "part-2", "part-3")
	Set(root, "guide", Edit{})
	for _, w := range []string{"part-1", "part-2", "part-3"} {
		Add(root, "guide", w)
	}
	w, u := accepted(t, root, "part-1", "第一篇。\n")
	SetSummary(root, w, u, strings.Repeat("一", 25), w.Genre.SummaryLength, now)
	s, _ := Of(root, "part-3")
	got := PrecedingSummaries(root, s, "part-3")
	if len(got) != 2 || got[0].Work != "part-1" || got[0].Summary == nil || got[1].Work != "part-2" || got[1].Summary != nil {
		t.Fatalf("preceding = %+v", got)
	}
	if got := PrecedingSummaries(root, s, "part-1"); len(got) != 0 {
		t.Fatalf("first = %+v", got)
	}
}

func TestSetFactKeepsComments(t *testing.T) {
	root := newRoot(t)
	dir := filepath.Join(root, ".writing-anything/series")
	os.MkdirAll(dir, 0o755)
	hand := "schema_version: 1\nid: h\ntitle: H # 标题注释\nworks: []\npacks: []\nsources: []\nfacts:\n  - {id: f, text: 同样的事实, sources: []} # 事实注释\n  - id: g\n    # 内部注释\n    text: 旧的\n    sources: []\n"
	file := filepath.Join(dir, "h.yaml")
	os.WriteFile(file, []byte(hand), 0o644)
	if _, changed, ps := SetFact(root, "h", Fact{ID: "f", Text: "同样的事实"}); ps != nil || changed != nil || string(mustRead(t, file)) != hand {
		t.Fatalf("unchanged fact rewrote: %v %+v", changed, ps)
	}
	if _, changed, ps := SetFact(root, "h", Fact{ID: "g", Text: "新的"}); ps != nil || len(changed) != 1 {
		t.Fatalf("change: %v %+v", changed, ps)
	}
	got := string(mustRead(t, file))
	for _, want := range []string{"# 标题注释", "# 事实注释", "# 内部注释", "text: 新的"} {
		if !strings.Contains(got, want) {
			t.Fatalf("lost %q:\n%s", want, got)
		}
	}
	title := "新标题"
	if _, _, ps := Set(root, "h", Edit{Title: &title}); ps != nil || !strings.Contains(string(mustRead(t, file)), "# 内部注释") {
		t.Fatalf("set lost comments: %+v", ps)
	}
	// 带锚点的事实不修改。
	os.WriteFile(file, []byte(strings.Replace(hand, "  - id: g\n", "  - &g\n    id: g\n", 1)), 0o644)
	if _, _, ps := SetFact(root, "h", Fact{ID: "g", Text: "x"}); !slices.Equal(codes(ps), []string{"series.anchored-value"}) {
		t.Fatalf("anchored: %+v", ps)
	}
}

func TestOfToleratesUnrelatedBrokenFiles(t *testing.T) {
	root := newRoot(t, "part-1")
	Set(root, "guide", Edit{})
	Add(root, "guide", "part-1")
	os.WriteFile(filepath.Join(root, ".writing-anything/series/broken.yaml"), []byte("id: ["), 0o644)
	s, ps := Of(root, "part-1")
	if s == nil || s.ID != "guide" || len(ps) != 1 || ps[0].Severity != workspace.SeverityWarning {
		t.Fatalf("of = %+v %+v", s, ps)
	}
	if _, ps := Check(root); len(ps) != 1 || ps[0].Severity != workspace.SeverityError {
		t.Fatalf("check keeps the error: %+v", ps)
	}
}
