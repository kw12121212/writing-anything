package reviewpacket

import (
	"fmt"
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

// addChapterTwo 在 part-one 下追加 chapter-two{scene-3, scene-4}（scene-4 有正文），返回重新加载的作品。
func addChapterTwo(t *testing.T, root string) *work.Work {
	t.Helper()
	for _, add := range []struct{ parent, typ, id string }{
		{"my-novel/part-one", "chapter", "chapter-two"},
		{"my-novel/chapter-two", "scene", "scene-3"},
		{"my-novel/chapter-two", "scene", "scene-4"},
	} {
		if _, _, ps := work.AddUnit(root, add.parent, add.typ, add.id, work.Anchor{}); len(ps) > 0 {
			t.Fatal(ps)
		}
	}
	writeAt(t, root, ".writing-anything/works/my-novel/units/scene-4/draft.md", "第四场的正文。\n")
	w, ps := work.Load(root, "my-novel")
	if len(ps) > 0 {
		t.Fatal(ps)
	}
	return w
}

// largeLedger 写入 near 条 scene-1（章内）与 far 条 scene-4（章外、距离更远）的守灯人状态台账。
func largeLedger(t *testing.T, root string, near, far int) {
	t.Helper()
	var b strings.Builder
	b.WriteString("schema_version: 1\nevents:\n")
	n := 0
	for i := 0; i < near; i++ {
		n++
		fmt.Fprintf(&b, "  - {id: state-%d, scene: my-novel/scene-1, entry: canon-1, state: 近处状态%d, status: confirmed, confirmation: ok, decided_at: \"2026-10-04T08:00:00Z\"}\n", n, n)
	}
	for i := 0; i < far; i++ {
		n++
		fmt.Fprintf(&b, "  - {id: state-%d, scene: my-novel/scene-4, entry: canon-1, state: 远处状态%d，%s, status: confirmed, confirmation: ok, decided_at: \"2026-10-04T08:00:00Z\"}\n", n, n, strings.Repeat("细节", 10))
	}
	writeAt(t, root, ".writing-anything/works/my-novel/state.yaml", b.String())
}

// fileChars 从落盘的任务包重新计算 chars（excerpt.text + kind：text + id：question）。
func fileChars(t *testing.T, root, id string) (int, *fileRecord) {
	t.Helper()
	f, ps := loadPacket(root, Dir+"/my-novel/"+id+".yaml")
	if f == nil || len(ps) > 0 {
		t.Fatalf("load %s: %+v", id, ps)
	}
	return packetChars(f), f
}

// 大台账夹具：每包 chars 不超过预算，章外远处的切片先被省略，章内近处的全部保留，omitted_slices 落盘。
func TestPlanLargeLedgerWithinBudget(t *testing.T) {
	root, _ := newNovel(t)
	w := addChapterTwo(t, root)
	largeLedger(t, root, 10, 200)
	const budget = 1000
	packets, _, ps := Plan(root, w, nil, "", budget, time.Now())
	if len(ps) > 0 {
		t.Fatal(ps)
	}
	var char *Packet
	for i, p := range packets {
		if p.Chars > budget {
			t.Fatalf("packet %s chars = %d exceeds %d", p.ID, p.Chars, budget)
		}
		if p.Dimension == DimCharacter && p.Scope == "my-novel/chapter-one" {
			char = &packets[i]
		}
	}
	if char == nil || char.OmittedSlices == 0 {
		t.Fatalf("character packet = %+v, want omitted slices", char)
	}
	chars, f := fileChars(t, root, char.ID)
	if chars != char.Chars || chars > budget || f.OmittedSlices != char.OmittedSlices {
		t.Fatalf("file chars = %d, omitted = %d; output = %+v", chars, f.OmittedSlices, char)
	}
	text := ""
	for _, s := range f.Slices {
		text += s.Text + "\n"
	}
	for i := 1; i <= 10; i++ {
		if !strings.Contains(text, fmt.Sprintf("state-%d：近处状态%d", i, i)) {
			t.Fatalf("near ledger state-%d dropped before far ones:\n%s", i, text)
		}
	}
	// 同等级先省略靠后的：保留的远处切片是最靠前的若干条。
	if strings.Contains(text, "state-210：") || !strings.Contains(text, "state-11：") {
		t.Fatalf("far slices not dropped from the end:\n%s", text)
	}
	data, _ := os.ReadFile(filepath.Join(root, ".writing-anything/packets/my-novel/"+char.ID+".yaml"))
	if !strings.Contains(string(data), "omitted_slices: ") {
		t.Fatalf("omitted_slices not written:\n%s", data)
	}
}

// 清单与最小正文放不下时按清单项拆包；done 的拆分包重 plan 时按清单集合保留。
func TestPlanSplitsChecklist(t *testing.T) {
	root, w := newNovel(t)
	const budget = 180
	packets, _, ps := Plan(root, w, nil, DimCharacter, budget, time.Now())
	if len(ps) > 0 {
		t.Fatal(ps)
	}
	if len(packets) < 2 {
		t.Fatalf("want split packets, got %+v", packets)
	}
	var items []string
	for _, p := range packets {
		if p.Scope != "my-novel/chapter-one" || p.Chars > budget {
			t.Fatalf("split packet = %+v", p)
		}
		_, f := fileChars(t, root, p.ID)
		for _, c := range f.Checklist {
			items = append(items, c.ID)
		}
	}
	if strings.Join(items, ",") != "behavior-state,naming,knowledge-boundary,state-change" {
		t.Fatalf("split checklist = %v", items)
	}
	// 提交第一个拆分包，重 plan 时不重写它，也不重复生成同清单的 open 包。
	_, first := fileChars(t, root, packets[0].ID)
	var in SubmitInput
	for _, c := range first.Checklist {
		in.Verdicts = append(in.Verdicts, Result{Item: c.ID, Verdict: "pass"})
	}
	if _, _, ps := Submit(root, w, packets[0].ID, in, time.Now()); len(ps) > 0 {
		t.Fatal(ps)
	}
	again, changed, ps := Plan(root, w, nil, DimCharacter, budget, time.Now())
	if len(ps) > 0 || len(again) != len(packets) {
		t.Fatalf("replan = %+v, %+v", again, ps)
	}
	for _, c := range changed {
		if strings.HasSuffix(c, "/"+packets[0].ID+".yaml") {
			t.Fatalf("done split packet rewritten: %v", changed)
		}
	}
	entries, _ := os.ReadDir(filepath.Join(root, ".writing-anything/packets/my-novel"))
	if len(entries) != len(packets) {
		t.Fatalf("packet files = %d, want %d", len(entries), len(packets))
	}
}

// 单个清单项与最小正文也放不下：报告 packet.over-budget，不写入或删除任何文件。
func TestPlanOverBudget(t *testing.T) {
	root, w := newNovel(t)
	if _, _, ps := Plan(root, w, nil, "", 16000, time.Now()); len(ps) > 0 {
		t.Fatal(ps)
	}
	dir := filepath.Join(root, ".writing-anything/packets/my-novel")
	before, _ := os.ReadDir(dir)
	packets, changed, ps := Plan(root, w, nil, "", 50, time.Now())
	if packets != nil || changed != nil || len(ps) != 1 || !codes(ps, "packet.over-budget") || ps[0].Field != "context_budget" ||
		ps[0].Severity != workspace.SeverityError || !strings.Contains(ps[0].Message, "上下文预算只有 50") {
		t.Fatalf("over budget = %+v, %v, %+v", packets, changed, ps)
	}
	after, _ := os.ReadDir(dir)
	if len(before) != len(after) {
		t.Fatalf("files changed: %d → %d", len(before), len(after))
	}
	// 新工作区：失败时不创建包目录。
	root2, w2 := newNovel(t)
	if _, _, ps := Plan(root2, w2, nil, DimThread, 50, time.Now()); !codes(ps, "packet.over-budget") {
		t.Fatalf("problems = %+v", ps)
	}
	if _, err := os.Stat(filepath.Join(root2, ".writing-anything/packets")); err == nil {
		t.Fatal("packets dir created on over-budget")
	}
}

func TestFitBudgetRules(t *testing.T) {
	base := newPacket("w", DimThread, "f-1", []Slice{
		{Kind: "thread", Text: "契约", rank: rankContract},
		{Kind: "state", Text: strings.Repeat("远", 50), rank: 9},
		{Kind: "state", Text: strings.Repeat("近", 50), rank: 1},
	})
	cl := checklistChars(base.Checklist)
	// 预算充足：原样保留、不截断。
	fs, p := fitBudget(base, "短正文", false, 16000)
	if p != 0 || len(fs) != 1 || fs[0].OmittedSlices != 0 || fs[0].Excerpt.Truncated || fs[0].Excerpt.Text != "短正文" {
		t.Fatalf("roomy = %+v %+v", fs, p)
	}
	// 只够最小正文 + 契约 + 近处：远处先省略，正文截到剩余预算。
	budget := cl + 100 + sliceChars(base.Slices[0]) + sliceChars(base.Slices[2]) + 5
	fs, p = fitBudget(base, strings.Repeat("文", 500), false, budget)
	if p != 0 || len(fs) != 1 || fs[0].OmittedSlices != 1 || len(fs[0].Slices) != 2 || fs[0].Slices[1].Text != base.Slices[2].Text ||
		!fs[0].Excerpt.Truncated || packetChars(&fs[0]) != budget {
		t.Fatalf("tight = %+v %+v", fs, p)
	}
	// 无正文：占位说明不可裁。
	fs, p = fitBudget(base, "  ", false, 16000)
	if p != 0 || !strings.Contains(fs[0].Excerpt.Text, "还没有正文") {
		t.Fatalf("placeholder = %+v", fs)
	}
	// 固定说明文字过长：over-budget。
	fixed := base
	fixed.Excerpt.Text = strings.Repeat("说", 300)
	maxItem := 0
	for _, c := range base.Checklist {
		maxItem = max(maxItem, itemChars(c))
	}
	if fs, p = fitBudget(fixed, "", true, 200); fs != nil || p != 300+maxItem {
		t.Fatalf("fixed over budget = %+v, need %d", fs, p)
	}
	// 正文开头的大段空白不计入片段：截断后不会只剩空白。
	fs, p = fitBudget(base, strings.Repeat(" \n", 200)+strings.Repeat("文", 300), false, cl+100)
	if p != 0 || !strings.HasPrefix(fs[0].Excerpt.Text, "文") || fs[0].OmittedSlices != 3 {
		t.Fatalf("leading whitespace = %+v", fs)
	}
}

func TestLedgerRank(t *testing.T) {
	order := map[string]int{"a": 0, "b": 1, "c": 2, "d": 3, "e": 4}
	if r := ledgerRank(order, 2, 2, "c"); r != 1 {
		t.Fatalf("inside = %d", r)
	}
	if before, after := ledgerRank(order, 2, 2, "b"), ledgerRank(order, 2, 2, "d"); before >= after {
		t.Fatalf("before %d should outrank after %d", before, after)
	}
	if near, far := ledgerRank(order, 2, 2, "b"), ledgerRank(order, 2, 2, "a"); near >= far {
		t.Fatalf("near %d should outrank far %d", near, far)
	}
	if r := ledgerRank(order, 2, 2, "zz"); r != rankUnlocated {
		t.Fatalf("unlocated = %d", r)
	}
	if r := ledgerRank(order, -1, -1, "a"); r != rankUnlocated {
		t.Fatalf("empty range = %d", r)
	}
}

// 重 plan 的 open 包按清单集合原位重建，不留下重复包；预算收紧后旧的超预算 open 包被替换。
func TestReplanRebuildsOpenInPlace(t *testing.T) {
	root, w := newNovel(t)
	if _, _, ps := Plan(root, w, nil, "", 16000, time.Now()); len(ps) > 0 {
		t.Fatal(ps)
	}
	dir := filepath.Join(root, ".writing-anything/packets/my-novel")
	before, _ := os.ReadDir(dir)
	packets, _, ps := Plan(root, w, nil, "", 400, time.Now())
	if len(ps) > 0 {
		t.Fatal(ps)
	}
	after, _ := os.ReadDir(dir)
	if len(after) != len(packets) || len(after) < len(before) {
		t.Fatalf("files %d → %d, packets %d", len(before), len(after), len(packets))
	}
	for _, e := range after {
		if n, _ := fileChars(t, root, strings.TrimSuffix(e.Name(), ".yaml")); n > 400 {
			t.Fatalf("%s chars = %d exceeds 400", e.Name(), n)
		}
	}
}

// 起因事件声明场景的结尾、视角人物已知、章摘要与 AI 评分切片都进入对应维度的任务包。
func TestPlanSliceSources(t *testing.T) {
	root, w := newNovel(t)
	writeAt(t, root, ".writing-anything/works/my-novel/canon.yaml", `schema_version: 1
entries:
  - {id: canon-1, type: character, name: 守灯人, aliases: [老灯匠], text: 五十岁, sources: [], protection: normal}
  - {id: canon-2, type: location, name: 灯塔, text: 旧灯塔, sources: [], protection: normal}
`)
	writeAt(t, root, ".writing-anything/works/my-novel/knowledge.yaml", `schema_version: 1
events:
  - {id: know-1, scene: my-novel/scene-1, entry: canon-2, character: reader, status: confirmed, confirmation: ok, decided_at: "2026-10-04T08:00:00Z"}
  - {id: know-2, scene: my-novel/scene-1, entry: canon-1, character: canon-1, status: confirmed, confirmation: ok, decided_at: "2026-10-04T08:00:00Z"}
`)
	writeAt(t, root, ".writing-anything/works/my-novel/units/scene-2/outline.yaml", `schema_version: 1
sections:
  - {id: sc1, title: 场景, pov: 守灯人, characters: [守灯人], causes: [ev-1], reveal: 灯芯的来历}
`)
	writeAt(t, root, ".writing-anything/works/my-novel/units/chapter-one/summary.yaml",
		"schema_version: 1\ntext: 守灯人守夜。\ndraft_sha256: \""+strings.Repeat("a", 64)+"\"\nrecorded_at: \"2026-10-04T08:00:00Z\"\n")
	writeAt(t, root, ".writing-anything/works/my-novel/units/scene-1/metrics.yaml",
		"schema_version: 1\ndraft_sha256: \""+strings.Repeat("a", 64)+"\"\nsessions:\n  - {id: s1, scores: [{metric: tension, paragraph: 1, score: 3}]}\n")
	w, _ = work.Load(root, "my-novel")
	packets, _, ps := Plan(root, w, nil, "", 16000, time.Now())
	if len(ps) > 0 {
		t.Fatal(ps)
	}
	kinds := map[string]bool{}
	for _, p := range packets {
		_, f := fileChars(t, root, p.ID)
		for _, s := range f.Slices {
			kinds[p.Dimension+"/"+s.Kind] = true
		}
	}
	for _, k := range []string{"causality/cause", "causality/cause-scene", "pov-reveal/reveal", "pov-reveal/pov-knowledge",
		"pov-reveal/reader-only", "style/chapter-summary", "style/metric", "character/knowledge", "thread/thread"} {
		if !kinds[k] {
			t.Errorf("missing slice %s in %v", k, kinds)
		}
	}
	if _, _, ps := Plan(root, w, nil, "nope", 16000, time.Now()); len(ps) != 1 || ps[0].Code != "input.invalid-value" {
		t.Fatalf("invalid dimension = %+v", ps)
	}
}

// newSeries 初始化两部 short-story 作品 book-1、book-2（各一个场景 s1，守灯人都出场；book-2/s1 引用 book-1 声明的 ev-1）。
func newSeries(t *testing.T) (string, []*work.Work) {
	t.Helper()
	root := t.TempDir()
	if _, ps := workspace.Init(root, nil); len(ps) > 0 {
		t.Fatal(ps)
	}
	var ws []*work.Work
	for _, id := range []string{"book-1", "book-2"} {
		if _, _, ps := work.Create(root, "novel", id); len(ps) > 0 {
			t.Fatal(ps)
		}
		for _, add := range []struct{ parent, typ, id string }{
			{id, "part", "p1"}, {id + "/p1", "chapter", "c1"}, {id + "/c1", "scene", "s1"},
		} {
			if _, _, ps := work.AddUnit(root, add.parent, add.typ, add.id, work.Anchor{}); len(ps) > 0 {
				t.Fatal(ps)
			}
		}
		base := ".writing-anything/works/" + id
		writeAt(t, root, base+"/canon.yaml", "schema_version: 1\nentries:\n  - {id: canon-1, type: character, name: 守灯人, text: 系列, sources: [], protection: normal}\n")
		writeAt(t, root, base+"/state.yaml", "schema_version: 1\nevents:\n  - {id: state-1, scene: "+id+"/s1, entry: canon-1, state: 受伤, status: confirmed, confirmation: ok, decided_at: \"2026-10-04T08:00:00Z\"}\n")
		writeAt(t, root, base+"/units/s1/draft.md", id+" 的正文。守灯人点亮了灯。\n")
	}
	writeAt(t, root, ".writing-anything/works/book-1/units/s1/outline.yaml",
		"schema_version: 1\nsections:\n  - {id: sc1, title: 场景, characters: [守灯人], events: [ev-1]}\n")
	writeAt(t, root, ".writing-anything/works/book-2/units/s1/outline.yaml",
		"schema_version: 1\nsections:\n  - {id: sc1, title: 场景, characters: [守灯人], causes: [ev-1]}\n")
	for _, id := range []string{"book-1", "book-2"} {
		w, ps := work.Load(root, id)
		if len(ps) > 0 {
			t.Fatal(ps)
		}
		ws = append(ws, w)
	}
	return root, ws
}

func TestPlanSeries(t *testing.T) {
	root, ws := newSeries(t)
	packets, changed, ps := PlanSeries(root, "guide", ws, "", 16000, time.Now())
	if len(ps) > 0 {
		t.Fatal(ps)
	}
	if !has(packets, DimSeriesCausality, "book-2/s1") || !has(packets, DimCharacter, "book-1/c1+book-2/c1") || len(changed) != len(packets) {
		t.Fatalf("series packets = %+v, changed = %v", packets, changed)
	}
	for _, c := range changed {
		if !strings.HasPrefix(c, Dir+"/guide/") {
			t.Fatalf("series packet path = %s", c)
		}
	}
	// 预算同样约束系列包；放不下时报告 over-budget 且不写文件。
	small, _, ps := PlanSeries(root, "guide2", ws, DimCharacter, 200, time.Now())
	if len(ps) > 0 {
		t.Fatal(ps)
	}
	for _, p := range small {
		if p.Chars > 200 {
			t.Fatalf("series packet chars = %d", p.Chars)
		}
	}
	if _, _, ps := PlanSeries(root, "guide3", ws, "", 50, time.Now()); !codes(ps, "packet.over-budget") {
		t.Fatalf("series over budget = %+v", ps)
	}
	if _, err := os.Stat(filepath.Join(root, Dir, "guide3")); err == nil {
		t.Fatal("over-budget series wrote files")
	}
	if _, _, ps := PlanSeries(root, "guide", ws, "nope", 16000, time.Now()); len(ps) != 1 || ps[0].Code != "input.invalid-value" {
		t.Fatalf("invalid dimension = %+v", ps)
	}
	// 单作品：退化为 Plan。
	one, _, ps := PlanSeries(root, "guide", ws[:1], DimCausality, 16000, time.Now())
	if len(ps) > 0 || !has(one, DimCausality, "book-1/s1") {
		t.Fatalf("single-work series = %+v %+v", one, ps)
	}
}

func TestParseInputAndView(t *testing.T) {
	in, ps := ParseInput([]byte("verdicts:\n  - {item: a, verdict: fail, note: 太快}\n"))
	if len(ps) > 0 || len(in.Verdicts) != 1 || in.Verdicts[0].Note != "太快" {
		t.Fatalf("parse = %+v %+v", in, ps)
	}
	if _, ps := ParseInput([]byte("verdicts: [\n")); len(ps) == 0 {
		t.Fatal("parse error not reported")
	}
	if _, ps := ParseInput([]byte("verdicts:\n  - {item: a, verdict: maybe}\n")); len(ps) == 0 {
		t.Fatal("schema violation not reported")
	}
	v := ViewOf(&File{ID: "x-1", Dimension: DimThread, OmittedSlices: 2})
	if v.Results == nil || v.OmittedSlices != 2 {
		t.Fatalf("view = %+v", v)
	}
}

func TestCheckArtifactDomain(t *testing.T) {
	root, w := newNovel(t)
	head := "schema_version: 1\nwork: my-novel\ndimension: thread\nscope: f-1\nexcerpt: {text: t, truncated: false}\nslices: []\nchecklist:\n  - {id: a, question: q}\n"
	cases := map[string]string{
		"ok-1":       "id: ok-1\n" + head + "status: done\nresults:\n  - {item: a, verdict: pass}\nrecorded_at: \"2026-10-04T08:00:00Z\"\n",
		"unknown-1":  "id: unknown-1\n" + head + "status: done\nresults:\n  - {item: a, verdict: pass}\n  - {item: b, verdict: pass}\n",
		"dup-1":      "id: dup-1\n" + head + "status: done\nresults:\n  - {item: a, verdict: pass}\n  - {item: a, verdict: fail}\n",
		"mismatch-1": "id: other-1\n" + head + "status: open\n",
	}
	want := map[string]string{"ok-1": "", "unknown-1": CodeUnknownItem, "dup-1": CodeUnknownItem, "mismatch-1": CodeInvalid}
	for id, content := range cases {
		rel := Dir + "/my-novel/" + id + ".yaml"
		writeAt(t, root, rel, content)
		ps := CheckArtifact(root, rel)
		if want[id] == "" && len(ps) > 0 || want[id] != "" && !codes(ps, want[id]) {
			t.Errorf("%s: problems = %+v, want %q", id, ps, want[id])
		}
	}
	if _, _, ps := FindPacket(root, "my-novel", "mismatch-1"); !codes(ps, CodeInvalid) {
		t.Fatalf("find mismatch = %+v", ps)
	}
	_, problems := CheckWorks(root, []*work.Work{w})
	if !codes(problems, CodeUnknownItem) || !codes(problems, CodeInvalid) {
		t.Fatalf("check works = %+v", problems)
	}
	// 途经符号链接的包文件按不存在报告。
	target := filepath.Join(root, "elsewhere.yaml")
	os.WriteFile(target, []byte(cases["ok-1"]), 0o644)
	link := filepath.Join(root, ".writing-anything/packets/my-novel/link-1.yaml")
	if err := os.Symlink(target, link); err == nil {
		if ps := CheckArtifact(root, Dir+"/my-novel/link-1.yaml"); !codes(ps, "artifact.not-found") {
			t.Fatalf("symlink = %+v", ps)
		}
	}
}

// 同一章的不同人物包以 subject 区分：提交其中一个后重 plan，只保留它，其他人物包原位重建、不被当作已完成。
func TestReplanCharacterSubjects(t *testing.T) {
	root, w := newNovel(t)
	writeAt(t, root, ".writing-anything/works/my-novel/canon.yaml", `schema_version: 1
entries:
  - {id: canon-1, type: character, name: 守灯人, aliases: [老灯匠], text: 五十岁, sources: [], protection: normal}
  - {id: canon-3, type: character, name: 林晚, text: 少女, sources: [], protection: normal}
`)
	writeAt(t, root, ".writing-anything/works/my-novel/units/scene-1/outline.yaml", `schema_version: 1
sections:
  - {id: sc1, title: 场景, pov: 守灯人, characters: [守灯人, 林晚], location: 灯室, events: [ev-1]}
`)
	w, _ = work.Load(root, "my-novel")
	packets, _, ps := Plan(root, w, nil, DimCharacter, 16000, time.Now())
	if len(ps) > 0 || len(packets) != 2 {
		t.Fatalf("packets = %+v %+v", packets, ps)
	}
	_, f1 := fileChars(t, root, "character-1")
	_, f2 := fileChars(t, root, "character-2")
	if f1.Subject != "canon-1" || f2.Subject != "canon-3" {
		t.Fatalf("subjects = %q %q", f1.Subject, f2.Subject)
	}
	in := SubmitInput{}
	for _, c := range f1.Checklist {
		in.Verdicts = append(in.Verdicts, Result{Item: c.ID, Verdict: "pass"})
	}
	if _, _, ps := Submit(root, w, "character-1", in, time.Now()); len(ps) > 0 {
		t.Fatal(ps)
	}
	again, changed, ps := Plan(root, w, nil, DimCharacter, 16000, time.Now())
	if len(ps) > 0 || len(again) != 2 || again[0].ID != "character-1" || again[0].Status != "done" ||
		again[1].ID != "character-2" || again[1].Status != "open" {
		t.Fatalf("replan = %+v %+v", again, ps)
	}
	if len(changed) != 1 || !strings.HasSuffix(changed[0], "/character-2.yaml") {
		t.Fatalf("changed = %v", changed)
	}
}

// 重 plan 删除与 done 包同键的 open 重复包；给出 unit 时不删除范围外场景的 open 包。
func TestReplanCleansDuplicatesWithinScope(t *testing.T) {
	root, w := newNovel(t)
	if _, _, ps := Plan(root, w, nil, "", 16000, time.Now()); len(ps) > 0 {
		t.Fatal(ps)
	}
	in := SubmitInput{Verdicts: []Result{
		{Item: "plant-natural", Verdict: "pass"}, {Item: "payoff-earned", Verdict: "pass"}, {Item: "continuity", Verdict: "pass"}}}
	if _, _, ps := Submit(root, w, "thread-1", in, time.Now()); len(ps) > 0 {
		t.Fatal(ps)
	}
	// 旧版本留下的同键 open 副本。
	dup, _ := os.ReadFile(filepath.Join(root, Dir, "my-novel", "causality-1.yaml"))
	writeAt(t, root, Dir+"/my-novel/causality-9.yaml", strings.Replace(string(dup), "id: causality-1", "id: causality-9", 1))
	data, _ := os.ReadFile(filepath.Join(root, Dir, "my-novel", "thread-1.yaml"))
	open := strings.Replace(strings.Replace(string(data), "id: thread-1", "id: thread-7", 1), "status: done", "status: open", 1)
	open = open[:strings.Index(open, "results:")]
	writeAt(t, root, Dir+"/my-novel/thread-7.yaml", open)

	_, changed, ps := Plan(root, w, unit(t, w, "my-novel/scene-1"), "", 16000, time.Now())
	if len(ps) > 0 {
		t.Fatal(ps)
	}
	exists := func(id string) bool {
		_, err := os.Stat(filepath.Join(root, Dir, "my-novel", id+".yaml"))
		return err == nil
	}
	if exists("thread-7") || exists("causality-9") {
		t.Fatalf("duplicates kept; changed = %v", changed)
	}
	// scene-2 的 open 包在范围外，保留不动。
	if !exists("causality-2") || !exists("pov-reveal-2") || !exists("thread-1") {
		t.Fatalf("out-of-scope or done packet removed; changed = %v", changed)
	}
}
