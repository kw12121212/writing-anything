package review

import (
	"os"
	"path/filepath"
	"slices"
	"strings"
	"testing"
	"time"

	"github.com/kw12121212/writing-anything/internal/catalog"
	"github.com/kw12121212/writing-anything/internal/workspace"
)

func codes[T any](xs []T, code func(T) string) []string {
	var out []string
	for _, x := range xs {
		out = append(out, code(x))
	}
	return out
}

func blockerCodes(bs []Blocker) []string { return codes(bs, func(b Blocker) string { return b.Code }) }

func problemCodes(ps []workspace.Problem) []string {
	return codes(ps, func(p workspace.Problem) string { return p.Code })
}

func put(t *testing.T, root, rel, content string) {
	t.Helper()
	p := filepath.Join(root, filepath.FromSlash(rel))
	if err := os.MkdirAll(filepath.Dir(p), 0o755); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(p, []byte(content), 0o644); err != nil {
		t.Fatal(err)
	}
}

var rubric = []catalog.RubricItem{{ID: "clarity", Title: "清晰"}, {ID: "facts", Title: "事实", Blocking: true}}

func TestGateLevels(t *testing.T) {
	sum := strings.Repeat("a", 64)
	r := &Review{DraftSHA256: sum, Rubric: []Verdict{{Item: "clarity", Verdict: "fail"}, {Item: "facts", Verdict: "fail"}},
		Findings: []Finding{{ID: "rf-1", Paragraph: 1, Message: "x", Status: "open"}, {ID: "rf-2", Status: "waived"}}}
	base := GateInput{Dir: "d", Review: r, DraftSHA: sum, Rubric: rubric, Feedback: []Pending{{"fb-1", "多举例"}}, LintErrors: 1,
		LintFailed: []workspace.Problem{{Code: "lint.too-large", Path: "d/draft.md", Message: "太大"}}, Decisions: []Pending{{"dec-1", "q"}}}
	cases := []struct {
		level     string
		confirmed bool
		want      []string
		warn      []string
	}{
		{"fast", false, []string{CodeLintErrors, "lint.too-large", "decision.blocking"}, []string{CodeUnconfirmed}},
		{"standard", true, []string{CodeLintErrors, "lint.too-large", "decision.blocking", CodeOpenFinding, CodeOpenFeedback}, nil},
		// 只有 strict 下声明 blocking 的评分项结论为 fail 才阻塞；clarity 的 fail 只是预审意见。
		{"strict", false, []string{CodeLintErrors, "lint.too-large", "decision.blocking", CodeBlockingRubric, CodeOpenFinding, CodeOpenFeedback, CodeUnconfirmed}, nil},
	}
	for _, tc := range cases {
		in := base
		in.Level, in.Confirmed = tc.level, tc.confirmed
		bs, ws := Gate(in)
		if !slices.Equal(blockerCodes(bs), tc.want) || !slices.Equal(blockerCodes(ws), tc.warn) {
			t.Errorf("%s: blockers = %v, warnings = %v", tc.level, blockerCodes(bs), blockerCodes(ws))
		}
	}
	// 审稿缺失、过期、不通过校验与缺少评分项结论。
	for _, tc := range []struct {
		in   GateInput
		want []string
	}{
		{GateInput{Level: "standard", Dir: "d", Confirmed: true}, []string{CodeReviewMissing}},
		{GateInput{Level: "standard", Dir: "d", Confirmed: true, Review: r, DraftSHA: "other"}, []string{CodeReviewStale}},
		{GateInput{Level: "standard", Dir: "d", Confirmed: true, ReviewBad: []workspace.Problem{{Code: "artifact.schema-violation"}}}, []string{"artifact.schema-violation"}},
		{GateInput{Level: "strict", Dir: "d", Confirmed: true, Review: &Review{DraftSHA256: sum}, DraftSHA: sum, Rubric: rubric}, []string{CodeMissingVerdict, CodeMissingVerdict}},
		{GateInput{Level: "fast", Dir: "d", Confirmed: true}, nil},
	} {
		if bs, _ := Gate(tc.in); !slices.Equal(blockerCodes(bs), tc.want) {
			t.Errorf("%+v: blockers = %v, want %v", tc.in, blockerCodes(bs), tc.want)
		}
	}
}

func TestRecordResolveAccept(t *testing.T) {
	root := t.TempDir()
	dir := ".writing-anything/works/my-post"
	put(t, root, dir+"/draft.md", "一\n\n二\n")
	sum := DraftSHA256(root, dir)
	if sum != SHA256([]byte("一\n\n二\n")) || DraftSHA256(root, "nope") != "" {
		t.Fatalf("sha = %q", sum)
	}
	in := "rubric:\n  - {item: clarity, verdict: pass, evidence: 清楚, paragraphs: [1, 2]}\n  - {item: facts, verdict: pass, evidence: 有出处, paragraphs: [2]}\nfindings: []\n"
	r, changed, ps := Record(root, dir, []byte(in), []string{"一", "二"}, sum, rubric)
	if len(ps) > 0 || !slices.Equal(changed, []string{Path(dir)}) || len(r.Findings) != 0 {
		t.Fatalf("record: %+v %v", r, ps)
	}
	if got, ps := Load(root, dir); len(ps) > 0 || got.DraftSHA256 != sum || !slices.Equal(got.Rubric[0].Paragraphs, []int{1, 2}) {
		t.Fatalf("load: %+v %v", got, ps)
	}
	if _, _, ps := Resolve(root, dir, "rf-1", "resolved", nil); !slices.Equal(problemCodes(ps), []string{CodeFindingNotFound}) {
		t.Fatalf("resolve missing: %v", ps)
	}
	// 发现项途经锚点时不修改。
	put(t, root, Path(dir), "schema_version: 1\ndraft_sha256: "+sum+"\nrubric: []\nfindings:\n  - &f {id: rf-1, paragraph: 1, message: x, status: open}\n")
	if _, _, ps := Resolve(root, dir, "rf-1", "resolved", nil); !slices.Equal(problemCodes(ps), []string{CodeAnchoredValue}) {
		t.Fatalf("anchored: %v", ps)
	}
	// 没有 review.yaml、review.yaml 不通过校验与状态取值错误。
	os.Remove(filepath.Join(root, Path(dir)))
	if _, _, ps := Resolve(root, dir, "rf-1", "resolved", nil); !slices.Equal(problemCodes(ps), []string{CodeFindingNotFound}) || ps[0].Path == "" {
		t.Fatalf("no review: %v", ps)
	}
	put(t, root, Path(dir), "schema_version: 1\n")
	if _, ps := Load(root, dir); len(ps) == 0 {
		t.Fatal("invalid review loaded")
	}
	if ps := CheckArtifact(root, Path(dir)); len(ps) == 0 || CheckArtifact(root, dir+"/other.yaml") != nil {
		t.Fatalf("check artifact: %v", ps)
	}
	if _, _, ps := Resolve(root, dir, "rf-1", "bogus", nil); !slices.Equal(problemCodes(ps), []string{"artifact.schema-violation"}) {
		t.Fatalf("bad status: %v", ps)
	}
	// Accept 写出通过 Schema 的验收记录；验收之后正文被改动时 AcceptanceBlockers 报告 acceptance.stale。
	a, changed, ps := Accept(root, dir, "strict", sum, true, "作者：好", time.Date(2026, 10, 1, 9, 0, 0, 0, time.UTC))
	if len(ps) > 0 || !slices.Equal(changed, []string{AcceptancePath(dir)}) || *a.Confirmation != "作者：好" || a.AcceptedAt != "2026-10-01T09:00:00Z" {
		t.Fatalf("accept: %+v %v", a, ps)
	}
	if got, ps := LoadAcceptance(root, dir); len(ps) > 0 || !got.ConfirmedByAuthor || len(AcceptanceBlockers(root, dir, "strict")) != 0 {
		t.Fatalf("load acceptance: %+v %v", got, ps)
	}
	put(t, root, dir+"/draft.md", "改过\n")
	if bs := AcceptanceBlockers(root, dir, "strict"); !slices.Equal(blockerCodes(bs), []string{CodeAcceptanceStale}) {
		t.Fatalf("stale: %v", bs)
	}
	checked, cps := CheckUnitArtifacts(root, []string{dir})
	if !slices.Equal(checked, []string{Path(dir), AcceptancePath(dir)}) || len(cps) == 0 {
		t.Fatalf("check unit artifacts: %v %v", checked, cps)
	}
}

// Record 的输入校验、id 编号跳过已用的 id；Resolve 的合并键、已有处理说明与取值未变；Accept 未确认时不记录原话摘要；
// AcceptanceBlockers 的 acceptance.md、不通过校验与关卡强度。
func TestReviewEdges(t *testing.T) {
	root := t.TempDir()
	dir := ".writing-anything/works/my-post"
	put(t, root, dir+"/draft.md", "一\n\n二\n")
	sum := DraftSHA256(root, dir)
	verdicts := "rubric:\n  - {item: clarity, verdict: pass, evidence: x, paragraphs: [1]}\n  - {item: facts, verdict: pass, evidence: x, paragraphs: [2]}\n"
	for _, tc := range []struct {
		input string
		want  []string
	}{
		{"rubric: [\n", []string{"artifact.parse-error"}},
		{"rubric: 1\n", []string{"artifact.schema-violation"}},
		{"rubric:\n  - {item: nope, verdict: pass, evidence: x, paragraphs: [3]}\n", []string{CodeUnknownRubricItem, CodeUnknownParagraph, CodeMissingVerdict, CodeMissingVerdict}},
		{verdicts + "findings:\n  - {paragraph: 1, quote: 二, message: x, rubric: nope}\n", []string{CodeUnknownRubricItem, CodeQuoteNotFound}},
	} {
		if _, _, ps := Record(root, dir, []byte(tc.input), []string{"一", "二"}, sum, rubric); !slices.Equal(problemCodes(ps), tc.want) {
			t.Errorf("%q: %v, want %v", tc.input, problemCodes(ps), tc.want)
		}
	}
	if _, _, ps := Record(root, dir, []byte("rubric: []\n"), nil, sum, nil); len(ps) > 0 {
		t.Errorf("empty rubric: %v", ps)
	}
	in := verdicts + "findings:\n  - {paragraph: 1, message: a}\n  - {id: rf-1, paragraph: 2, quote: 二, message: b}\n  - {paragraph: 1, message: c}\n"
	r, _, ps := Record(root, dir, []byte(in), []string{"一", "二"}, sum, rubric)
	if len(ps) > 0 || codes(r.Findings, func(f Finding) string { return f.ID })[0] != "rf-2" || r.Findings[2].ID != "rf-3" {
		t.Fatalf("ids: %+v %v", r, ps)
	}
	note := "作者说保留"
	if _, _, ps := Resolve(root, dir, "rf-2", "waived", nil); !slices.Equal(problemCodes(ps), []string{CodeMissingNote}) {
		t.Fatalf("waive without note: %v", ps)
	}
	if f, changed, ps := Resolve(root, dir, "rf-2", "resolved", &note); len(ps) > 0 || len(changed) != 1 || *f.Note != note {
		t.Fatalf("resolve with note: %+v %v", f, ps)
	}
	// 已有处理说明时可以直接豁免；取值未变时不写文件。
	if f, changed, ps := Resolve(root, dir, "rf-2", "waived", nil); len(ps) > 0 || len(changed) != 1 || f.Status != "waived" {
		t.Fatalf("waive with existing note: %+v %v", f, ps)
	}
	if _, changed, ps := Resolve(root, dir, "rf-2", "waived", nil); len(ps) > 0 || len(changed) != 0 {
		t.Fatalf("unchanged: %v %v", changed, ps)
	}
	blank := " "
	if _, _, ps := Resolve(root, dir, "rf-2", "waived", &blank); !slices.Equal(problemCodes(ps), []string{"artifact.schema-violation"}) {
		t.Fatalf("blank note: %v", ps)
	}
	put(t, root, Path(dir), "schema_version: 1\ndraft_sha256: "+sum+"\nrubric: []\nfindings:\n  - {id: rf-1, paragraph: 1, message: x, status: open}\n  - {id: rf-1, paragraph: 1, message: y, status: open}\n")
	if _, _, ps := Resolve(root, dir, "rf-1", "resolved", nil); !slices.Equal(problemCodes(ps), []string{catalog.CodeDuplicateID}) {
		t.Fatalf("duplicate: %v", ps)
	}
	put(t, root, Path(dir), "schema_version: 1\ndraft_sha256: "+sum+"\nrubric: []\nbase: &b {paragraph: 1, message: x, status: open}\nfindings:\n  - {<<: *b, id: rf-1}\n")
	if _, _, ps := Resolve(root, dir, "rf-1", "resolved", nil); len(ps) == 0 {
		t.Fatal("merge key accepted")
	}
	a, _, ps := Accept(root, dir, "fast", sum, false, "忽略", time.Date(2026, 10, 1, 0, 0, 0, 0, time.UTC))
	if len(ps) > 0 || a.Confirmation != nil || strings.Contains(string(readAll(t, root, AcceptancePath(dir))), "confirmation:") {
		t.Fatalf("unconfirmed accept: %+v %v", a, ps)
	}
	if bs := AcceptanceBlockers(root, dir, "fast"); len(bs) != 0 {
		t.Fatalf("same gate: %v", bs)
	}
	if bs := AcceptanceBlockers(root, dir, "standard"); !slices.Equal(blockerCodes(bs), []string{CodeAcceptanceWeakerGate}) {
		t.Fatalf("weaker gate: %v", bs)
	}
	put(t, root, AcceptancePath(dir), "schema_version: 1\n")
	if bs := AcceptanceBlockers(root, dir, "fast"); !slices.Equal(blockerCodes(bs), []string{CodeAcceptanceInvalid}) {
		t.Fatalf("invalid: %v", bs)
	}
	os.Remove(filepath.Join(root, AcceptancePath(dir)))
	if bs := AcceptanceBlockers(root, dir, "fast"); len(bs) != 0 {
		t.Fatalf("none: %v", bs)
	}
	put(t, root, dir+"/acceptance.md", "ok\n")
	if bs := AcceptanceBlockers(root, dir, "fast"); !slices.Equal(blockerCodes(bs), []string{CodeAcceptanceInvalid}) || bs[0].Path != dir+"/acceptance.md" {
		t.Fatalf("acceptance.md: %v", bs)
	}
	// 决策队列无法加载时阻塞验收。
	if bs, _ := Gate(GateInput{Level: "fast", Dir: dir, Confirmed: true, QueueBad: []workspace.Problem{{Message: "坏了"}}}); !slices.Equal(blockerCodes(bs), []string{"decision.invalid-queue"}) {
		t.Fatalf("queue: %v", bs)
	}
}

func readAll(t *testing.T, root, rel string) []byte {
	t.Helper()
	data, err := os.ReadFile(filepath.Join(root, filepath.FromSlash(rel)))
	if err != nil {
		t.Fatal(err)
	}
	return data
}
