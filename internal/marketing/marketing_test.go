package marketing

import (
	"os"
	"path/filepath"
	"slices"
	"strings"
	"testing"
	"time"

	"github.com/kw12121212/writing-anything/internal/decision"
	"github.com/kw12121212/writing-anything/internal/workspace"
)

const dir = ".writing-anything/works/post"

func setup(t *testing.T) string {
	t.Helper()
	root := t.TempDir()
	if err := os.MkdirAll(filepath.Join(root, filepath.FromSlash(dir)), 0o755); err != nil {
		t.Fatal(err)
	}
	return root
}

func write(t *testing.T, root, rel, content string) {
	t.Helper()
	if err := os.WriteFile(filepath.Join(root, filepath.FromSlash(rel)), []byte(content), 0o644); err != nil {
		t.Fatal(err)
	}
}

func read(t *testing.T, root, rel string) string {
	t.Helper()
	b, _ := os.ReadFile(filepath.Join(root, filepath.FromSlash(rel)))
	return string(b)
}

func noEnsure() ([]string, []workspace.Problem) { return nil, nil }

func ptr(s string) *string { return &s }

func codes(ps []workspace.Problem) []string {
	var out []string
	for _, p := range ps {
		out = append(out, p.Code)
	}
	return out
}

func questionCodes(qs []Question) []string {
	var out []string
	for _, q := range qs {
		out = append(out, q.Code+":"+q.Field)
	}
	return out
}

func TestSet(t *testing.T) {
	root := setup(t)
	m, changed, ps := Set(root, dir, Edit{Headline: &HeadlineEdit{ID: "h1", Text: ptr("三步搞定连接池")}, Digest: ptr("一篇短文")}, noEnsure)
	if ps != nil || len(changed) != 1 || m.Headlines[0].Text != "三步搞定连接池" || m.Digest != "一篇短文" {
		t.Fatalf("create: %+v %v %+v", m, changed, ps)
	}
	if got := questionCodes(Questions(m, Queue{}, true)); !slices.Equal(got, []string{"marketing.missing-field:headlines", "marketing.missing-field:opening", "marketing.missing-field:cta"}) {
		t.Fatalf("questions: %v", got)
	}
	// 修改已有候选、追加候选、删除字段；作者的注释保留。
	write(t, root, Path(dir), read(t, root, Path(dir))+"# 作者的注释\n")
	_, changed, ps = Set(root, dir, Edit{Headline: &HeadlineEdit{ID: "h2", Text: ptr("连接池为什么总不够用")}, Opening: ptr("上周又是连接池。"), CTA: ptr("关注我们")}, noEnsure)
	if ps != nil || len(changed) != 1 || !strings.Contains(read(t, root, Path(dir)), "# 作者的注释") {
		t.Fatalf("append: %v %+v\n%s", changed, ps, read(t, root, Path(dir)))
	}
	before := read(t, root, Path(dir))
	if _, changed, ps := Set(root, dir, Edit{Digest: ptr("一篇短文")}, noEnsure); ps != nil || len(changed) != 0 || read(t, root, Path(dir)) != before {
		t.Fatalf("unchanged: %v %+v", changed, ps)
	}
	if _, _, ps := Set(root, dir, Edit{Remove: []string{"h9"}}, noEnsure); !slices.Equal(codes(ps), []string{"marketing.headline-not-found"}) || ps[0].Field != "remove-headline[0]" {
		t.Fatalf("remove missing: %+v", ps)
	}
	// 写前复查：只含空白的文字与新候选缺少文字，字段为参数名、不填路径。
	for _, e := range []Edit{{Headline: &HeadlineEdit{ID: "h3", Text: ptr("  ")}}, {Headline: &HeadlineEdit{ID: "h3"}}, {Digest: ptr(" ")}} {
		_, _, ps := Set(root, dir, e, noEnsure)
		if len(ps) == 0 || ps[0].Code != "artifact.schema-violation" || ps[0].Path != "" || ps[0].Field == "" {
			t.Errorf("%+v: %+v", e, ps)
		}
	}
	if read(t, root, Path(dir)) != before {
		t.Fatal("failed edits changed the file")
	}
	// 删除两个候选后 headlines 字段一并删除；删除 digest。
	m, _, ps = Set(root, dir, Edit{Remove: []string{"h1", "h2"}, Digest: ptr("")}, noEnsure)
	if ps != nil || len(m.Headlines) != 0 || m.Digest != "" || strings.Contains(read(t, root, Path(dir)), "headlines") {
		t.Fatalf("remove all: %+v %+v", m, ps)
	}
	// ensure 失败时不写文件。
	if _, _, ps := Set(root, dir, Edit{CTA: ptr("新的")}, func() ([]string, []workspace.Problem) {
		return nil, []workspace.Problem{{Code: "workspace.write-failed"}}
	}); len(ps) != 1 {
		t.Fatalf("ensure: %+v", ps)
	}
}

func TestAnchored(t *testing.T) {
	root := setup(t)
	write(t, root, Path(dir), "schema_version: 1\nheadlines: &hs\n  - {id: h1, text: 甲}\ndigest: &d 摘要\n")
	for _, e := range []Edit{{Digest: ptr("新")}, {Headline: &HeadlineEdit{ID: "h2", Text: ptr("乙")}}} {
		if _, _, ps := Set(root, dir, e, noEnsure); !slices.Equal(codes(ps), []string{"marketing.anchored-value"}) {
			t.Errorf("%+v: %+v", e, ps)
		}
	}
	write(t, root, Path(dir), "schema_version: 1\nheadlines:\n  - &h {id: h1, text: 甲}\n")
	for _, e := range []Edit{{Remove: []string{"h1"}}, {Headline: &HeadlineEdit{ID: "h1", Text: ptr("乙")}}} {
		if _, _, ps := Set(root, dir, e, noEnsure); !slices.Equal(codes(ps), []string{"marketing.anchored-value"}) {
			t.Errorf("%+v: %+v", e, ps)
		}
	}
	write(t, root, Path(dir), "&root\nschema_version: 1\n")
	if _, _, ps := Set(root, dir, Edit{Digest: ptr("新")}, noEnsure); !slices.Equal(codes(ps), []string{"marketing.anchored-value"}) {
		t.Fatalf("root anchor: %+v", ps)
	}
}

func TestLoadAndCheck(t *testing.T) {
	root := setup(t)
	if m, ps := Load(root, dir); m != nil || ps != nil {
		t.Fatalf("absent: %+v %+v", m, ps)
	}
	if g := Check(root, dir, false, false, false, Queue{}); g != nil {
		t.Fatalf("no artifact: %+v", g)
	}
	write(t, root, Path(dir), "schema_version: 1\nheadlines:\n  - {id: h1, text: 甲}\n  - {id: h1, text: 乙}\n")
	if _, ps := Load(root, dir); len(ps) != 1 || ps[0].Code != "artifact.duplicate-id" || ps[0].Field != "headlines[1].id" {
		t.Fatalf("duplicate: %+v", ps)
	}
	if g := Check(root, dir, true, false, true, Queue{}); g == nil || len(g.Invalid) != 1 {
		t.Fatalf("invalid gate: %+v", g)
	}
	checked, ps := CheckUnitArtifacts(root, []string{dir, ".writing-anything/works/other"})
	if len(checked) != 1 || len(ps) != 1 || len(CheckArtifact(root, Path(dir))) != 1 {
		t.Fatalf("check: %v %+v", checked, ps)
	}
	write(t, root, Path(dir), "schema_version: [\n")
	if _, ps := Load(root, dir); len(ps) != 1 || ps[0].Code != "artifact.parse-error" {
		t.Fatalf("parse error: %+v", ps)
	}
	write(t, root, Path(dir), "schema_version: 2\n")
	if _, ps := Load(root, dir); len(ps) == 0 {
		t.Fatal("schema violation not reported")
	}
	// 只有 marketing.md：视为没有填写任何要素。
	os.Remove(filepath.Join(root, filepath.FromSlash(Path(dir))))
	if g := Check(root, dir, false, true, true, Queue{}); g == nil || g.Path != dir+"/marketing.md" || len(g.Questions) != 4 {
		t.Fatalf("md gate: %+v", g)
	}
	// 途经符号链接按不存在处理并报告 artifact.not-found。
	if err := os.Symlink("elsewhere.yaml", filepath.Join(root, filepath.FromSlash(Path(dir)))); err != nil {
		t.Skip(err)
	}
	if _, ps := Load(root, dir); len(ps) != 1 || ps[0].Code != "artifact.not-found" {
		t.Fatalf("symlink: %+v", ps)
	}
}

func TestHeadlineState(t *testing.T) {
	m := &Marketing{Headlines: []Headline{{"h1", "甲"}, {"h2", "乙"}}, Digest: "d", Opening: "o", CTA: "c"}
	if s, _ := m.HeadlineState(Queue{}); s != StateNone || !slices.Equal(questionCodes(Questions(m, Queue{}, true)), []string{"marketing.headline-unasked:headline_decision"}) {
		t.Fatalf("none: %s", s)
	}
	m.HeadlineDecision = "dec-1"
	b := "b"
	z := "z"
	opts := []decision.Option{{ID: "a", Text: "甲"}, {ID: "b", Text: "乙"}}
	for name, tt := range map[string]struct {
		q     Queue
		state string
	}{
		"unknown":    {Queue{Invalid: true}, StateUnknown},
		"missing":    {Queue{}, StateStale},
		"fewer":      {Queue{Decisions: []decision.Decision{{ID: "dec-1", Options: opts[:1]}}}, StateStale},
		"edited":     {Queue{Decisions: []decision.Decision{{ID: "dec-1", Options: []decision.Option{{ID: "a", Text: "甲"}, {ID: "b", Text: "丙"}}}}}, StateStale},
		"open":       {Queue{Decisions: []decision.Decision{{ID: "dec-1", Options: opts, Status: decision.StatusOpen}}}, StateOpen},
		"bad choice": {Queue{Decisions: []decision.Decision{{ID: "dec-1", Options: opts, Status: decision.StatusDecided, Choice: &z}}}, StateStale},
		"decided":    {Queue{Decisions: []decision.Decision{{ID: "dec-1", Options: opts, Status: decision.StatusDecided, Choice: &b}}}, StateDecided},
	} {
		s, h := m.HeadlineState(tt.q)
		if s != tt.state || (s == StateDecided) != (h != nil) || h != nil && h.ID != "h2" {
			t.Errorf("%s: state %s, headline %+v", name, s, h)
		}
		qs := questionCodes(Questions(m, tt.q, true))
		if want := tt.state == StateStale; want != slices.Contains(qs, "marketing.headline-stale:headline_decision") {
			t.Errorf("%s: questions %v", name, qs)
		}
		// 正文未写入时 headline-unasked 不报（stale 不受影响）。
		qs = questionCodes(Questions(m, tt.q, false))
		if slices.Contains(qs, "marketing.headline-unasked:headline_decision") {
			t.Errorf("%s: unasked without a draft: %v", name, qs)
		}
		if want := tt.state == StateStale; want != slices.Contains(qs, "marketing.headline-stale:headline_decision") {
			t.Errorf("%s: questions without a draft %v", name, qs)
		}
	}
}

func TestAsk(t *testing.T) {
	root := setup(t)
	if _, _, _, ps := Ask(root, dir, "post", "", ""); !slices.Equal(codes(ps), []string{"artifact.not-found"}) {
		t.Fatalf("no marketing: %+v", ps)
	}
	Set(root, dir, Edit{Headline: &HeadlineEdit{ID: "h1", Text: ptr("甲")}}, noEnsure)
	if _, _, _, ps := Ask(root, dir, "post", "", ""); !slices.Equal(codes(ps), []string{"marketing.missing-field"}) || ps[0].Field != "headlines" {
		t.Fatalf("one candidate: %+v", ps)
	}
	Set(root, dir, Edit{Headline: &HeadlineEdit{ID: "h2", Text: ptr("乙")}}, noEnsure)
	if _, _, _, ps := Ask(root, dir, "post", "h9", ""); !slices.Equal(codes(ps), []string{"marketing.headline-not-found"}) || ps[0].Field != "recommend" {
		t.Fatalf("bad recommend: %+v", ps)
	}
	if _, err := os.Stat(filepath.Join(root, ".writing-anything", "decisions.yaml")); err == nil {
		t.Fatal("failed ask wrote the decision queue")
	}
	d, m, changed, ps := Ask(root, dir, "post", "h2", "更具体")
	if ps != nil || d.ID != "dec-1" || !d.Blocking || *d.Recommendation != "b" || d.Options[1].Text != "乙" || !slices.Equal(d.Units, []string{"post"}) ||
		m.HeadlineDecision != "dec-1" || !slices.Equal(changed, []string{decision.Path, Path(dir)}) {
		t.Fatalf("ask: %+v %+v %v %+v", d, m, changed, ps)
	}
	queue := read(t, root, decision.Path)
	if _, _, _, ps := Ask(root, dir, "post", "", ""); !slices.Equal(codes(ps), []string{"marketing.already-asked"}) || read(t, root, decision.Path) != queue {
		t.Fatalf("ask twice: %+v", ps)
	}
	// 候选在提问之后改过、决策尚未决定：原地替换 dec-1 的选项，不另建决策，marketing.yaml 不变。
	Set(root, dir, Edit{Headline: &HeadlineEdit{ID: "h2", Text: ptr("丙")}}, noEnsure)
	before := read(t, root, Path(dir))
	d, m, changed, ps = Ask(root, dir, "post", "", "")
	if ps != nil || d.ID != "dec-1" || d.Options[1].Text != "丙" || d.Recommendation != nil || d.Rationale != nil || !slices.Equal(changed, []string{decision.Path}) ||
		read(t, root, Path(dir)) != before {
		t.Fatalf("re-ask open: %+v %v %+v", d, changed, ps)
	}
	if s, _ := m.HeadlineState(LoadQueue(root)); s != StateOpen {
		t.Fatalf("state after re-ask: %s", s)
	}
	// 已决定之后候选又改过：另建新的决策。
	if _, ps := decision.Decide(root, "dec-1", "a", "", "作者：用甲", time.Now()); ps != nil {
		t.Fatal(ps)
	}
	Set(root, dir, Edit{Headline: &HeadlineEdit{ID: "h1", Text: ptr("丁")}}, noEnsure)
	if d, _, _, ps := Ask(root, dir, "post", "", ""); ps != nil || d.ID != "dec-2" {
		t.Fatalf("re-ask decided: %+v %+v", d, ps)
	}
	// 决策队列无法加载、营销要素不通过校验、候选超过 26 个时不写任何文件。
	write(t, root, decision.Path, "schema_version: 9\n")
	if _, _, _, ps := Ask(root, dir, "post", "", ""); len(ps) == 0 {
		t.Fatal("broken queue not reported")
	}
	os.Remove(filepath.Join(root, filepath.FromSlash(decision.Path)))
	var many strings.Builder
	many.WriteString("schema_version: 1\nheadlines:\n")
	for i := range 27 {
		many.WriteString("  - {id: h" + string(rune('a'+i%26)) + strings.Repeat("x", i/26) + ", text: t" + string(rune('a'+i%26)) + strings.Repeat("x", i/26) + "}\n")
	}
	write(t, root, Path(dir), many.String())
	if _, _, _, ps := Ask(root, dir, "post", "", ""); !slices.Equal(codes(ps), []string{"artifact.schema-violation"}) {
		t.Fatalf("too many: %+v", ps)
	}
	write(t, root, Path(dir), "schema_version: 1\nheadlines: oops\n")
	if _, _, _, ps := Ask(root, dir, "post", "", ""); len(ps) == 0 {
		t.Fatal("invalid marketing not reported")
	}
}

// 追加决策之后写入 marketing.yaml 失败：如实报告已修改的决策队列，并说明要先处理的决策。
func TestAskWriteFailure(t *testing.T) {
	if os.Getuid() == 0 {
		t.Skip("root 不受目录权限限制")
	}
	root := setup(t)
	Set(root, dir, Edit{Headline: &HeadlineEdit{ID: "h1", Text: ptr("甲")}}, noEnsure)
	Set(root, dir, Edit{Headline: &HeadlineEdit{ID: "h2", Text: ptr("乙")}}, noEnsure)
	unit := filepath.Join(root, filepath.FromSlash(dir))
	if err := os.Chmod(unit, 0o555); err != nil {
		t.Fatal(err)
	}
	defer os.Chmod(unit, 0o755)
	d, _, changed, ps := Ask(root, dir, "post", "", "")
	if d != nil || !slices.Equal(changed, []string{decision.Path}) || !slices.Equal(codes(ps), []string{"workspace.write-failed"}) || !strings.Contains(ps[0].Message, "dec-1") {
		t.Fatalf("write failure: %+v %v %+v", d, changed, ps)
	}
}

// 合并键或别名得到的 id：按 id 修改或删除候选时报告 marketing.anchored-value。
func TestMergeKeyHeadlines(t *testing.T) {
	root := setup(t)
	write(t, root, Path(dir), "schema_version: 1\nheadlines:\n  - <<: {id: h1}\n    text: 甲\n")
	for _, e := range []Edit{{Remove: []string{"h1"}}, {Headline: &HeadlineEdit{ID: "h1", Text: ptr("乙")}}, {Headline: &HeadlineEdit{ID: "h2", Text: ptr("乙")}}} {
		if _, _, ps := Set(root, dir, e, noEnsure); !slices.Equal(codes(ps), []string{"marketing.anchored-value"}) || ps[0].Field != "headlines[0]" {
			t.Errorf("merge key %+v: %+v", e, ps)
		}
	}
}

// 候选超过标题决策的选项上限时，关卡说明无法交给作者选定。
func TestTooManyHeadlines(t *testing.T) {
	m := &Marketing{Digest: "d", Opening: "o", CTA: "c"}
	for i := range decision.MaxOptions + 1 {
		m.Headlines = append(m.Headlines, Headline{ID: "h" + string(rune('a'+i%26)) + strings.Repeat("x", i/26), Text: "t"})
	}
	if got := questionCodes(Questions(m, Queue{}, true)); !slices.Equal(got, []string{"marketing.too-many-headlines:headlines"}) {
		t.Fatalf("questions: %v", got)
	}
}
