package reviewpacket

import (
	"os"
	"path/filepath"
	"strings"
	"testing"
	"time"

	"github.com/kw12121212/writing-anything/internal/work"
	"github.com/kw12121212/writing-anything/internal/workspace"
)

// newNovel 初始化 novel 工作区：chapter-one{scene-1,scene-2}，canon-1 守灯人（别名老灯匠）、伏笔 f-1（埋设
// scene-1、计划回收 scene-2），scene-1 有 confirmed 状态与读者知情，两场景有正文；scene-1 大纲声明扩展契约。
func newNovel(t *testing.T) (string, *work.Work) {
	t.Helper()
	root := t.TempDir()
	if _, ps := workspace.Init(root, nil); len(ps) > 0 {
		t.Fatal(ps)
	}
	var w *work.Work
	var ps []workspace.Problem
	if w, _, ps = work.Create(root, "novel", "my-novel"); len(ps) > 0 {
		t.Fatal(ps)
	}
	for _, add := range []struct{ parent, typ, id string }{
		{"my-novel", "part", "part-one"},
		{"my-novel/part-one", "chapter", "chapter-one"},
		{"my-novel/chapter-one", "scene", "scene-1"},
		{"my-novel/chapter-one", "scene", "scene-2"},
	} {
		if _, _, ps = work.AddUnit(root, add.parent, add.typ, add.id, work.Anchor{}); len(ps) > 0 {
			t.Fatal(ps)
		}
	}
	writeAt(t, root, ".writing-anything/works/my-novel/canon.yaml", `schema_version: 1
entries:
  - {id: canon-1, type: character, name: 守灯人, aliases: [老灯匠], text: 五十岁, sources: [], protection: normal}
`)
	writeAt(t, root, ".writing-anything/works/my-novel/state.yaml", `schema_version: 1
events:
  - {id: state-1, scene: my-novel/scene-1, entry: canon-1, state: 受伤, status: confirmed, confirmation: ok, decided_at: "2026-10-04T08:00:00Z"}
`)
	writeAt(t, root, ".writing-anything/works/my-novel/knowledge.yaml", `schema_version: 1
events:
  - {id: know-1, scene: my-novel/scene-1, entry: canon-1, character: reader, status: confirmed, confirmation: ok, decided_at: "2026-10-04T08:00:00Z"}
`)
	writeAt(t, root, ".writing-anything/works/my-novel/threads.yaml", `schema_version: 1
entries:
  - {id: f-1, text: 灯芯的来历, planted_in: my-novel/scene-1, planned_recover: my-novel/scene-2, status: open}
`)
	writeAt(t, root, ".writing-anything/works/my-novel/units/scene-1/outline.yaml", `schema_version: 1
sections:
  - {id: sc1, title: 场景, pov: 守灯人, characters: [守灯人], location: 灯室, events: [ev-1]}
`)
	for _, s := range []string{"scene-1", "scene-2"} {
		writeAt(t, root, ".writing-anything/works/my-novel/units/"+s+"/draft.md",
			"这是场景 "+s+" 的正文。守灯人在灯室里擦拭灯罩，回忆起很多年前的一个雨夜。\\n")
	}
	if w, ps = work.Load(root, "my-novel"); len(ps) > 0 {
		t.Fatal(ps)
	}
	return root, w
}

func writeAt(t *testing.T, root, rel, content string) {
	t.Helper()
	path := filepath.Join(root, filepath.FromSlash(rel))
	if err := os.MkdirAll(filepath.Dir(path), 0o755); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(path, []byte(content), 0o644); err != nil {
		t.Fatal(err)
	}
}

func unit(t *testing.T, w *work.Work, ref string) *work.Unit {
	t.Helper()
	for _, u := range w.Units() {
		if u.Ref == ref {
			return u
		}
	}
	t.Fatalf("unit %s not found", ref)
	return nil
}

func has(ps []Packet, dim, scope string) bool {
	for _, p := range ps {
		if p.Dimension == dim && (scope == "" || p.Scope == scope) {
			return true
		}
	}
	return false
}

func TestPlanDimensionsAndScope(t *testing.T) {
	root, w := newNovel(t)
	packets, changed, ps := Plan(root, w, nil, "", 16000, time.Now())
	if len(ps) > 0 {
		t.Fatal(ps)
	}
	// character：章 × 守灯人一包。
	if !has(packets, DimCharacter, "my-novel/chapter-one") {
		t.Fatalf("missing character packet: %+v", packets)
	}
	// thread：f-1 一包。
	if !has(packets, DimThread, "f-1") {
		t.Fatalf("missing thread packet: %+v", packets)
	}
	// causality 与 pov-reveal：每场景一包。
	for _, s := range []string{"my-novel/scene-1", "my-novel/scene-2"} {
		if !has(packets, DimCausality, s) || !has(packets, DimPovReveal, s) {
			t.Fatalf("missing scene packets for %s: %+v", s, packets)
		}
	}
	// style：章一包。
	if !has(packets, DimStyle, "my-novel/chapter-one") {
		t.Fatalf("missing style packet: %+v", packets)
	}
	// 预算。
	for _, p := range packets {
		if p.Chars > 16000 {
			t.Fatalf("packet %s chars = %d exceeds budget", p.ID, p.Chars)
		}
	}
	if len(changed) == 0 {
		t.Fatal("changedPaths empty on first plan")
	}
	// unit 范围收窄：只生成 chapter-one 子树。
	p2, _, _ := Plan(root, w, unit(t, w, "my-novel/chapter-one"), DimCausality, 16000, time.Now())
	for _, p := range p2 {
		if p.Scope != "my-novel/scene-1" && p.Scope != "my-novel/scene-2" {
			t.Fatalf("out-of-scope packet: %+v", p)
		}
	}
}

func TestPlanBudgetTruncates(t *testing.T) {
	root, w := newNovel(t)
	writeAt(t, root, ".writing-anything/works/my-novel/units/scene-1/draft.md",
		"这是一段很长的正文。"+strings.Repeat("夜色与潮声在灯塔下往复。", 100)+"\n")
	writeAt(t, root, ".writing-anything/works/my-novel/units/scene-2/draft.md",
		"这是一段很长的正文。"+strings.Repeat("守灯人守着微弱的火光。", 100)+"\n")
	packets, _, ps := Plan(root, w, nil, DimCausality, 300, time.Now())
	if len(ps) > 0 {
		t.Fatal(ps)
	}
	for _, p := range packets {
		if p.Chars > 300 {
			t.Fatalf("chars = %d exceeds 300", p.Chars)
		}
		if !p.Truncated {
			t.Fatalf("expected truncation: %+v", p)
		}
	}
}

func TestPlanPreservesDone(t *testing.T) {
	root, w := newNovel(t)
	if _, _, ps := Plan(root, w, nil, DimThread, 16000, time.Now()); len(ps) > 0 {
		t.Fatal(ps)
	}
	in := SubmitInput{Verdicts: []Result{
		{Item: "plant-natural", Verdict: "pass"},
		{Item: "payoff-earned", Verdict: "na"},
		{Item: "continuity", Verdict: "pass"},
	}}
	if _, _, ps := Submit(root, w, "thread-1", in, time.Now()); len(ps) > 0 {
		t.Fatal(ps)
	}
	before, _ := os.ReadFile(filepath.Join(root, ".writing-anything/packets/my-novel/thread-1.yaml"))
	_, changed, _ := Plan(root, w, nil, "", 16000, time.Now())
	for _, c := range changed {
		if strings.HasSuffix(c, "thread-1.yaml") {
			t.Fatalf("done packet rewritten: %v", changed)
		}
	}
	after, _ := os.ReadFile(filepath.Join(root, ".writing-anything/packets/my-novel/thread-1.yaml"))
	if string(before) != string(after) {
		t.Fatal("done packet content changed")
	}
}

func TestSubmitValidation(t *testing.T) {
	root, w := newNovel(t)
	if _, _, ps := Plan(root, w, nil, DimThread, 16000, time.Now()); len(ps) > 0 {
		t.Fatal(ps)
	}
	// 缺结论。
	if _, _, ps := Submit(root, w, "thread-1", SubmitInput{Verdicts: []Result{{Item: "plant-natural", Verdict: "pass"}}}, time.Now()); !codes(ps, CodeMissingVerdict) {
		t.Fatalf("problems = %+v, want missing-verdict", ps)
	}
	// 未知项。
	if _, _, ps := Submit(root, w, "thread-1", SubmitInput{Verdicts: []Result{
		{Item: "plant-natural", Verdict: "pass"}, {Item: "payoff-earned", Verdict: "pass"},
		{Item: "continuity", Verdict: "pass"}, {Item: "nope", Verdict: "pass"}}}, time.Now()); !codes(ps, CodeUnknownItem) {
		t.Fatalf("problems = %+v, want unknown-item", ps)
	}
	// 不存在。
	if _, _, ps := Submit(root, w, "nope-1", SubmitInput{}, time.Now()); !codes(ps, CodeNotFound) {
		t.Fatalf("problems = %+v, want not-found", ps)
	}
	// 成功后重复提交。
	in := SubmitInput{Verdicts: []Result{
		{Item: "plant-natural", Verdict: "pass"}, {Item: "payoff-earned", Verdict: "fail", Note: "回收仓促"},
		{Item: "continuity", Verdict: "pass"}}}
	if f, changed, ps := Submit(root, w, "thread-1", in, time.Now()); f == nil || len(changed) != 1 {
		t.Fatalf("submit: %+v", ps)
	}
	if _, _, ps := Submit(root, w, "thread-1", in, time.Now()); !codes(ps, CodeAlreadyDone) {
		t.Fatalf("problems = %+v, want already-done", ps)
	}
}

func TestReport(t *testing.T) {
	root, w := newNovel(t)
	stats, failures, ready, ps := Report(root, w)
	if len(ps) > 0 || ready {
		t.Fatalf("empty report: %+v ready=%v", ps, ready)
	}
	if _, _, ps := Plan(root, w, nil, DimThread, 16000, time.Now()); len(ps) > 0 {
		t.Fatal(ps)
	}
	in := SubmitInput{Verdicts: []Result{
		{Item: "plant-natural", Verdict: "pass"}, {Item: "payoff-earned", Verdict: "fail", Note: "回收仓促"},
		{Item: "continuity", Verdict: "pass"}}}
	if _, _, ps := Submit(root, w, "thread-1", in, time.Now()); len(ps) > 0 {
		t.Fatal(ps)
	}
	stats, failures, ready, ps = Report(root, w)
	if len(ps) > 0 || ready {
		t.Fatalf("report: %+v ready=%v", ps, ready)
	}
	for _, st := range stats {
		if st.Dimension == DimThread && (st.Done != 1) {
			t.Fatalf("thread stat = %+v", st)
		}
	}
	if len(failures) != 1 || failures[0].Packet != "thread-1" || failures[0].Item != "payoff-earned" {
		t.Fatalf("failures = %+v", failures)
	}
}

func TestCheckWorks(t *testing.T) {
	root, w := newNovel(t)
	if _, _, ps := Plan(root, w, nil, DimThread, 16000, time.Now()); len(ps) > 0 {
		t.Fatal(ps)
	}
	// 手写 done 包缺结论。
	writeAt(t, root, ".writing-anything/packets/my-novel/thread-9.yaml", `schema_version: 1
id: thread-9
work: my-novel
dimension: thread
scope: f-9
excerpt:
  text: t
  truncated: false
slices: []
checklist:
  - {id: plant-natural, question: q}
status: done
`)
	w2, _ := work.Load(root, "my-novel")
	checked, problems := CheckWorks(root, []*work.Work{w2})
	if len(checked) == 0 {
		t.Fatal("checked empty")
	}
	if !codes(problems, CodeMissingVerdict) {
		t.Fatalf("problems = %+v, want missing-verdict", problems)
	}
	if ps := CheckArtifact(root, ".writing-anything/packets/my-novel/thread-9.yaml"); !codes(ps, CodeMissingVerdict) {
		t.Fatalf("artifact problems = %+v", ps)
	}
}

// 审查回归：重 plan 的 id 不覆盖 done、范围收窄不删范围外 done、submit 保留注释、无正文场景占位。
func TestReplanKeepsDoneIds(t *testing.T) {
	root, w := newNovel(t)
	writeAt(t, root, ".writing-anything/works/my-novel/threads.yaml", `schema_version: 1
entries:
  - {id: f-1, text: 灯芯的来历, planted_in: my-novel/scene-1, planned_recover: my-novel/scene-2, status: open}
  - {id: f-2, text: 老灯匠的账本, planted_in: my-novel/scene-1, planned_recover: my-novel/scene-2, status: open}
`)
	w, _ = work.Load(root, "my-novel")
	if _, _, ps := Plan(root, w, nil, DimThread, 16000, time.Now()); len(ps) > 0 {
		t.Fatal(ps)
	}
	in := SubmitInput{Verdicts: []Result{
		{Item: "plant-natural", Verdict: "pass"}, {Item: "payoff-earned", Verdict: "pass"}, {Item: "continuity", Verdict: "pass"}}}
	if _, _, ps := Submit(root, w, "thread-1", in, time.Now()); len(ps) > 0 {
		t.Fatal(ps)
	}
	before, _ := os.ReadFile(filepath.Join(root, ".writing-anything/packets/my-novel/thread-1.yaml"))
	_, changed, ps := Plan(root, w, nil, DimThread, 16000, time.Now())
	if len(ps) > 0 {
		t.Fatal(ps)
	}
	after, _ := os.ReadFile(filepath.Join(root, ".writing-anything/packets/my-novel/thread-1.yaml"))
	if string(before) != string(after) {
		t.Fatal("done packet thread-1 was rewritten by re-plan")
	}
	for _, c := range changed {
		if strings.HasSuffix(c, "thread-1.yaml") {
			t.Fatalf("changedPaths touched done packet: %v", changed)
		}
	}
	// f-2 的新包存在且 id 不与 thread-1 冲突。
	if _, err := os.Stat(filepath.Join(root, ".writing-anything/packets/my-novel/thread-2.yaml")); err != nil {
		t.Fatalf("f-2 packet missing: %v", err)
	}
}

func TestNarrowScopeKeepsOutsideDone(t *testing.T) {
	root, w := newNovel(t)
	if _, _, ps := Plan(root, w, nil, DimCausality, 16000, time.Now()); len(ps) > 0 {
		t.Fatal(ps)
	}
	in := SubmitInput{Verdicts: []Result{
		{Item: "cause-holds", Verdict: "pass"}, {Item: "event-produced", Verdict: "pass"}, {Item: "motivation", Verdict: "pass"}}}
	// causality-2 是先序第二个场景的包。
	if _, _, ps := Submit(root, w, "causality-2", in, time.Now()); len(ps) > 0 {
		t.Fatal(ps)
	}
	_, changed, ps := Plan(root, w, unit(t, w, "my-novel/scene-1"), DimCausality, 16000, time.Now())
	if len(ps) > 0 {
		t.Fatal(ps)
	}
	for _, c := range changed {
		if strings.HasSuffix(c, "causality-2.yaml") {
			t.Fatalf("out-of-scope done packet deleted: %v", changed)
		}
	}
	if _, err := os.Stat(filepath.Join(root, ".writing-anything/packets/my-novel/causality-2.yaml")); err != nil {
		t.Fatal("out-of-scope done packet removed")
	}
}

func TestSubmitPreservesComments(t *testing.T) {
	root, w := newNovel(t)
	if _, _, ps := Plan(root, w, nil, DimThread, 16000, time.Now()); len(ps) > 0 {
		t.Fatal(ps)
	}
	rel := filepath.Join(root, ".writing-anything/packets/my-novel/thread-1.yaml")
	data, _ := os.ReadFile(rel)
	os.WriteFile(rel, append([]byte("# 作者备注：重点看回收场景\n"), data...), 0o644)
	in := SubmitInput{Verdicts: []Result{
		{Item: "plant-natural", Verdict: "pass"}, {Item: "payoff-earned", Verdict: "pass"}, {Item: "continuity", Verdict: "pass"}}}
	if _, _, ps := Submit(root, w, "thread-1", in, time.Now()); len(ps) > 0 {
		t.Fatal(ps)
	}
	after, _ := os.ReadFile(rel)
	if !strings.Contains(string(after), "作者备注") {
		t.Fatal("author comment lost on submit")
	}
}

func TestPlanNoDraftPlaceholder(t *testing.T) {
	root, w := newNovel(t)
	os.Remove(filepath.Join(root, ".writing-anything/works/my-novel/units/scene-2/draft.md"))
	packets, _, ps := Plan(root, w, nil, DimCausality, 16000, time.Now())
	if len(ps) > 0 {
		t.Fatalf("plan without draft failed: %+v", ps)
	}
	if !has(packets, DimCausality, "my-novel/scene-2") {
		t.Fatalf("scene-2 packet missing: %+v", packets)
	}
}

func TestReportInvalidPacket(t *testing.T) {
	root, w := newNovel(t)
	_ = w
	writeAt(t, root, ".writing-anything/packets/my-novel/broken-1.yaml", "schema_version: 1\nquestions: [\n")
	w2, _ := work.Load(root, "my-novel")
	_, _, ready, ps := Report(root, w2)
	if len(ps) == 0 || ready {
		t.Fatalf("report on broken packet: ps=%+v ready=%v", ps, ready)
	}
}

func codes(ps []workspace.Problem, code string) bool {
	for _, p := range ps {
		if p.Code == code {
			return true
		}
	}
	return false
}
