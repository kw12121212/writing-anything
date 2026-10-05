package closeout

import (
	"time"
	"go.yaml.in/yaml/v3"
	"os"
	"path/filepath"
	"strings"
	"testing"

	"github.com/kw12121212/writing-anything/internal/knowledge"
	"github.com/kw12121212/writing-anything/internal/state"
	"github.com/kw12121212/writing-anything/internal/work"
	"github.com/kw12121212/writing-anything/internal/workspace"
)

// newNovel 初始化 novel 工作区：my-novel/part-one/chapter-one/{scene-1,scene-2} 与
// my-novel/chapter-two/scene-3，设定集 canon-1（守灯人）与 canon-2（世界规则），伏笔台账含
// fs-1（scene-1 埋设计划 chapter-two 回收）与 f-1（scene-1 埋设、scene-2 计划回收、未回收），scene-1 的
// 大纲声明契约：state_changes sc-1、plants fs-1、recovers f-1、events ev-2，出场人物守灯人与精灵、地点灯室，
// 并有正文。scene-2 无契约、无正文。
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
		{"my-novel/part-one", "chapter", "chapter-two"},
		{"my-novel/chapter-two", "scene", "scene-3"},
	} {
		if _, _, ps = work.AddUnit(root, add.parent, add.typ, add.id, work.Anchor{}); len(ps) > 0 {
			t.Fatal(ps)
		}
	}
	writeAt(t, root, ".writing-anything/works/my-novel/canon.yaml", `schema_version: 1
entries:
  - id: canon-1
    type: character
    name: 守灯人
    text: 五十岁，左手有旧伤
    sources: []
    protection: normal
  - id: canon-2
    type: world-rule
    text: 灯油只能燃烧到黎明
    sources: []
    protection: normal
`)
	writeAt(t, root, ".writing-anything/works/my-novel/threads.yaml", `schema_version: 1
entries:
  - {id: fs-1, text: 灯塔的旧账, planted_in: my-novel/scene-1, planned_recover: my-novel/chapter-two, status: open}
  - {id: f-1, text: 灯芯的来历, planted_in: my-novel/scene-1, planned_recover: my-novel/scene-2, status: open}
`)
	writeAt(t, root, ".writing-anything/works/my-novel/units/scene-1/outline.yaml", `schema_version: 1
sections:
  - id: sc1
    title: 灯下对峙
    pov: 守灯人
    characters: [守灯人, 精灵]
    location: 灯室
    state_changes:
      - {id: sc-1, text: 精灵交出灯芯}
    plants: [fs-1]
    recovers: [f-1]
    events: [ev-2]
`)
	writeAt(t, root, ".writing-anything/works/my-novel/units/scene-1/draft.md", "正文第一段。\n正文第二段。\n")
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

func has(ps []workspace.Problem, code, field string) bool {
	for _, p := range ps {
		if p.Code == code && (field == "" || p.Field == field) {
			return true
		}
	}
	return false
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

// fullInput 是满足 scene-1 契约的完整载荷。
func fullInput() Input {
	return Input{
		StateChanges: []StateChangeInput{{Entry: "canon-1", State: "灯芯易主给精灵", Contract: "sc-1"}},
		Knowledge:    []KnowledgeInput{{Entry: "canon-2", Character: knowledge.Reader}},
		Recovers:     []string{"f-1"},
		Events:       []string{"ev-2"},
		Waived:       []string{"精灵", "灯室"},
	}
}

func readYAML(t *testing.T, root, rel string) map[string]any {
	t.Helper()
	data, err := os.ReadFile(filepath.Join(root, filepath.FromSlash(rel)))
	if err != nil {
		t.Fatal(err)
	}
	var out map[string]any
	if err := yaml.Unmarshal(data, &out); err != nil {
		t.Fatal(err)
	}
	return out
}

func TestSubmitFull(t *testing.T) {
	root, w := newNovel(t)
	u := unit(t, w, "my-novel/scene-1")
	v, changed, ps := Submit(root, w, u, fullInput(), false)
	if len(ps) != 0 {
		t.Fatalf("problems = %+v, want none", ps)
	}
	if v == nil || v.Unit != u.Ref {
		t.Fatalf("view = %+v", v)
	}
	// 台账写入：状态提议与知情提议绑定本场景，伏笔回收于本场景。
	stateYAML := readYAML(t, root, ".writing-anything/works/my-novel/state.yaml")
	events := stateYAML["events"].([]any)
	first := events[0].(map[string]any)
	if first["scene"] != u.Ref || first["status"] != "proposed" || first["entry"] != "canon-1" {
		t.Fatalf("state entry = %+v", first)
	}
	knowYAML := readYAML(t, root, ".writing-anything/works/my-novel/knowledge.yaml")
	ke := knowYAML["events"].([]any)[0].(map[string]any)
	if ke["character"] != knowledge.Reader || ke["scene"] != u.Ref {
		t.Fatalf("knowledge entry = %+v", ke)
	}
	threadYAML := readYAML(t, root, ".writing-anything/works/my-novel/threads.yaml")
	for _, e := range threadYAML["entries"].([]any) {
		entry := e.(map[string]any)
		if entry["id"] == "f-1" && (entry["status"] != "paid" || entry["recovered_in"] != u.Ref) {
			t.Fatalf("f-1 after recover = %+v", entry)
		}
	}
	// 收口记录：正文指纹、state 的 contract 关联、recovers 与 events。
	rec := readYAML(t, root, ".writing-anything/works/my-novel/units/scene-1/closeout.yaml")
	if rec["draft_sha256"] != v.DraftSHA256 || rec["draft_sha256"] == "" {
		t.Fatalf("draft_sha256 = %v", rec["draft_sha256"])
	}
	stateRefs := rec["state"].([]any)
	sr := stateRefs[0].(map[string]any)
	if !strings.HasPrefix(sr["id"].(string), "state-") || sr["contract"] != "sc-1" {
		t.Fatalf("state ref = %+v", sr)
	}
	if rec["recovers"].([]any)[0] != "f-1" || rec["events"].([]any)[0] != "ev-2" {
		t.Fatalf("rec = %+v", rec)
	}
	for _, rel := range []string{
		"works/my-novel/state.yaml", "works/my-novel/knowledge.yaml", "works/my-novel/threads.yaml",
		"works/my-novel/units/scene-1/closeout.yaml",
	} {
		found := false
		for _, c := range changed {
			if strings.HasSuffix(c, rel) {
				found = true
			}
		}
		if !found {
			t.Fatalf("changedPaths %v missing %s", changed, rel)
		}
	}
}

func TestSubmitContractGaps(t *testing.T) {
	cases := []struct {
		name  string
		input func() Input
		code  string
	}{
		{"missing-state-change", func() Input { in := fullInput(); in.StateChanges = nil; return in }, CodeMissingStateChange},
		{"missing-recovery", func() Input { in := fullInput(); in.Recovers = nil; return in }, CodeMissingRecovery},
		{"missing-event", func() Input { in := fullInput(); in.Events = nil; return in }, CodeMissingEvent},
		{"unknown-event", func() Input { in := fullInput(); in.Events = []string{"ev-2", "ev-9"}; return in }, CodeUnknownEvent},
	}
	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			root, w := newNovel(t)
			u := unit(t, w, "my-novel/scene-1")
			before := snapshotTree(t, root, ".writing-anything")
			v, changed, ps := Submit(root, w, u, tc.input(), false)
			if v != nil || changed != nil {
				t.Fatalf("submit unexpectedly succeeded")
			}
			if !has(ps, tc.code, "") {
				t.Fatalf("problems = %+v, want %s", ps, tc.code)
			}
			if after := snapshotTree(t, root, ".writing-anything"); before != after {
				t.Fatal("workspace changed on rejected submit")
			}
		})
	}
}

func TestSubmitMissingPlant(t *testing.T) {
	root, w := newNovel(t)
	// 契约 plants 引用台账中不存在的 fs-9：作者手写大纲绕过 scene contract 的写前检查。
	writeAt(t, root, ".writing-anything/works/my-novel/units/scene-1/outline.yaml", `schema_version: 1
sections:
  - id: sc1
    title: 灯下对峙
    state_changes:
      - {id: sc-1, text: 精灵交出灯芯}
    plants: [fs-9]
    recovers: [f-1]
    events: [ev-2]
`)
	w, ps := work.Load(root, "my-novel")
	if len(ps) != 0 {
		t.Fatal(ps)
	}
	u := unit(t, w, "my-novel/scene-1")
	if _, _, ps = Submit(root, w, u, fullInput(), false); !has(ps, CodeMissingPlant, "") {
		t.Fatalf("problems = %+v, want %s", ps, CodeMissingPlant)
	}
}

func TestSubmitSuspectedNames(t *testing.T) {
	root, w := newNovel(t)
	u := unit(t, w, "my-novel/scene-1")
	in := fullInput()
	in.Waived = nil // 不豁免：精灵与灯室应提示，守灯人命中设定集不提示
	v, _, ps := Submit(root, w, u, in, false)
	if v == nil {
		t.Fatalf("problems = %+v", ps)
	}
	names := map[string]bool{}
	for _, s := range v.Suspected {
		names[s.Name] = true
	}
	if !names["精灵"] || !names["灯室"] || names["守灯人"] {
		t.Fatalf("suspected = %+v", v.Suspected)
	}
	for _, p := range ps {
		if p.Code == CodeSuspectedName && p.Severity != workspace.SeverityWarning {
			t.Fatalf("suspected-name severity = %+v", p)
		}
	}
}

func TestSubmitCanonInPayload(t *testing.T) {
	root, w := newNovel(t)
	u := unit(t, w, "my-novel/scene-1")
	in := fullInput()
	in.Canon = []CanonInput{{Type: "character", Name: "精灵", Text: "灯之精灵"}}
	// 载荷 canon 节缺省 id 为 canon-3（已有两条）；豁免精灵改为补录后由扫描命中 name。
	in.Waived = []string{"灯室"}
	v, changed, ps := Submit(root, w, u, in, false)
	if v == nil {
		t.Fatalf("problems = %+v", ps)
	}
	rec := readYAML(t, root, ".writing-anything/works/my-novel/canon.yaml")
	var names []string
	for _, e := range rec["entries"].([]any) {
		entry := e.(map[string]any)
		if entry["name"] != nil {
			names = append(names, entry["name"].(string))
		}
	}
	if !contains(names, "精灵") {
		t.Fatalf("canon entries = %+v", rec)
	}
	if len(v.Suspected) != 0 {
		t.Fatalf("suspected = %+v, want empty（精灵已补录）", v.Suspected)
	}
	found := false
	for _, c := range changed {
		if strings.HasSuffix(c, "works/my-novel/canon.yaml") {
			found = true
		}
	}
	if !found {
		t.Fatalf("changedPaths %v missing canon.yaml", changed)
	}
}

func contains(vs []string, v string) bool {
	for _, x := range vs {
		if x == v {
			return true
		}
	}
	return false
}

func TestSubmitNotSceneAndNoDraft(t *testing.T) {
	root, w := newNovel(t)
	if _, _, ps := Submit(root, w, unit(t, w, "my-novel/part-one"), Input{}, false); !has(ps, "scene.not-scene", "") {
		t.Fatalf("problems = %+v", ps)
	}
	// scene-2 没有正文。
	if _, _, ps := Submit(root, w, unit(t, w, "my-novel/scene-2"), Input{}, false); !has(ps, "draft.not-found", "") {
		t.Fatalf("problems = %+v", ps)
	}
}

func TestSubmitAlreadyClosedAndReplace(t *testing.T) {
	root, w := newNovel(t)
	u := unit(t, w, "my-novel/scene-1")
	if _, _, ps := Submit(root, w, u, fullInput(), false); len(ps) != 0 {
		t.Fatal(ps)
	}
	if _, _, ps := Submit(root, w, u, fullInput(), false); !has(ps, CodeAlreadyClosed, "") {
		t.Fatalf("problems = %+v, want %s", ps, CodeAlreadyClosed)
	}
	// 作者改契约新增 sc-2 后 --replace 重收口：closeout.yaml 整体替换，台账不清理。
	writeAt(t, root, ".writing-anything/works/my-novel/units/scene-1/outline.yaml", `schema_version: 1
sections:
  - id: sc1
    title: 灯下对峙
    state_changes:
      - {id: sc-1, text: 精灵交出灯芯}
      - {id: sc-2, text: 守灯人受伤}
    plants: [fs-1]
    recovers: [f-1]
    events: [ev-2]
`)
	w, ps := work.Load(root, "my-novel")
	if len(ps) != 0 {
		t.Fatal(ps)
	}
	u = unit(t, w, "my-novel/scene-1")
	in := fullInput()
	in.StateChanges = append(in.StateChanges, StateChangeInput{Entry: "canon-1", State: "受伤", Contract: "sc-2"})
	v, _, ps := Submit(root, w, u, in, true)
	if v == nil {
		t.Fatalf("problems = %+v", ps)
	}
	rec := readYAML(t, root, ".writing-anything/works/my-novel/units/scene-1/closeout.yaml")
	if got := len(rec["state"].([]any)); got != 2 {
		t.Fatalf("state refs = %d, want 2", got)
	}
}

func TestSubmitEmptyPayloadNoContract(t *testing.T) {
	// 无契约要求字段（只有 characters 与 reveal）的场景：空载荷收口只写 closeout.yaml。
	root, w := newNovel(t)
	writeAt(t, root, ".writing-anything/works/my-novel/units/scene-1/outline.yaml", `schema_version: 1
sections:
  - id: sc1
    title: 灯下对峙
    characters: [守灯人]
    reveal: 精灵的真实身份
`)
	w, ps := work.Load(root, "my-novel")
	if len(ps) != 0 {
		t.Fatal(ps)
	}
	u := unit(t, w, "my-novel/scene-1")
	v, changed, ps := Submit(root, w, u, Input{}, false)
	if v == nil {
		t.Fatalf("problems = %+v", ps)
	}
	if len(changed) != 1 || !strings.HasSuffix(changed[0], "units/scene-1/closeout.yaml") {
		t.Fatalf("changedPaths = %v", changed)
	}
}

// 预检漏洞回归（fresh-context 审查发现）：写入阶段的失败必须发生在预检，被拒提交不留下任何已写入文件。
func TestSubmitPrecheckCoversWriteRejections(t *testing.T) {
	t.Run("knowledge-id-duplicate", func(t *testing.T) {
		root, w := newNovel(t)
		u := unit(t, w, "my-novel/scene-1")
		in := fullInput()
		in.Knowledge = append(in.Knowledge, KnowledgeInput{ID: "", Entry: "canon-2", Character: knowledge.Reader})
		// 同一载荷两条缺省 id 的知情提交会撞出同一个 know-1？不会（Propose 逐条递增）；改为显式重复。
		in.Knowledge = []KnowledgeInput{{ID: "know-9", Entry: "canon-2", Character: knowledge.Reader}, {ID: "know-9", Entry: "canon-2", Character: knowledge.Reader}}
		before := snapshotTree(t, root, ".writing-anything")
		_, _, ps := Submit(root, w, u, in, false)
		if !has(ps, "artifact.duplicate-id", "knowledge[1].id") {
			t.Fatalf("problems = %+v", ps)
		}
		if after := snapshotTree(t, root, ".writing-anything"); before != after {
			t.Fatal("workspace changed on rejected submit")
		}
	})
	t.Run("canon-late-entry-invalid", func(t *testing.T) {
		root, w := newNovel(t)
		u := unit(t, w, "my-novel/scene-1")
		in := fullInput()
		in.Canon = []CanonInput{
			{Type: "character", Name: "精灵", Text: "灯之精灵"},
			{Type: "world-rule", Text: "规则", Sources: []string{"src-9"}},
		}
		before := snapshotTree(t, root, ".writing-anything")
		_, _, ps := Submit(root, w, u, in, false)
		if !has(ps, "canon.unknown-source", "canon[1].sources[0]") {
			t.Fatalf("problems = %+v", ps)
		}
		if after := snapshotTree(t, root, ".writing-anything"); before != after {
			t.Fatal("workspace changed on rejected submit")
		}
	})
	t.Run("anchored-ledger", func(t *testing.T) {
		root, w := newNovel(t)
		u := unit(t, w, "my-novel/scene-1")
		writeAt(t, root, ".writing-anything/works/my-novel/state.yaml", "schema_version: 1\nevents: &evs []\n")
		in := fullInput()
		in.Canon = []CanonInput{{Type: "character", Name: "精灵", Text: "灯之精灵"}}
		before := snapshotTree(t, root, ".writing-anything")
		_, _, ps := Submit(root, w, u, in, false)
		if !has(ps, CodeInvalid, "events") {
			t.Fatalf("problems = %+v", ps)
		}
		if after := snapshotTree(t, root, ".writing-anything"); before != after {
			t.Fatal("workspace changed on rejected submit")
		}
	})
	t.Run("recover-at-ancestor-chapter", func(t *testing.T) {
		// threads add 允许计划回收位置是包含埋设场景的章：收口预检用同一语义，不拒绝。
		root, w := newNovel(t)
		u := unit(t, w, "my-novel/scene-1")
		in := fullInput()
		in.Plants = []PlantInput{{Text: "灯塔的旧账", RecoverAt: "my-novel/chapter-one"}}
		if v, _, ps := Submit(root, w, u, in, false); v == nil {
			t.Fatalf("problems = %+v, want accepted（章包含埋设场景）", ps)
		}
	})
}

func TestWaive(t *testing.T) {
	root, w := newNovel(t)
	u := unit(t, w, "my-novel/scene-1")
	in := fullInput()
	in.Waived = nil
	if _, _, ps := Submit(root, w, u, in, false); len(ps) != 2 {
		t.Fatalf("problems = %+v, want two suspected-name warnings", ps)
	}
	v, changed, ps := Waive(root, w, u, []string{"精灵"})
	if v == nil {
		t.Fatalf("problems = %+v", ps)
	}
	if len(changed) != 1 || !strings.HasSuffix(changed[0], "closeout.yaml") {
		t.Fatalf("changedPaths = %v", changed)
	}
	if !contains(v.Waived, "精灵") || len(v.Suspected) != 1 || v.Suspected[0].Name != "灯室" {
		t.Fatalf("view = %+v", v)
	}
	// 幂等：已豁免的名字不再写文件。
	if v2, changed2, ps2 := Waive(root, w, u, []string{"精灵"}); v2 == nil || len(changed2) != 0 {
		t.Fatalf("second waive = %+v, %v, %+v", v2, changed2, ps2)
	}
	// 未收口的场景报告 closeout.not-closed。
	if _, _, ps := Waive(root, w, unit(t, w, "my-novel/scene-2"), []string{"灯室"}); !has(ps, CodeNotClosed, "") {
		t.Fatalf("problems = %+v", ps)
	}
}

func TestAcceptanceBlockers(t *testing.T) {
	root, w := newNovel(t)
	u := unit(t, w, "my-novel/scene-1")
	// 未收口：closeout.missing（所有关卡强度共用）。
	if ps := AcceptanceBlockers(root, w, u); !has(ps, CodeMissing, "") {
		t.Fatalf("problems = %+v", ps)
	}
	if _, _, ps := Submit(root, w, u, fullInput(), false); len(ps) != 0 {
		t.Fatal(ps)
	}
	// 收口后提议未处理：closeout.pending-proposals。
	if ps := AcceptanceBlockers(root, w, u); !has(ps, CodePendingProposals, "") {
		t.Fatalf("problems = %+v, want %s", ps, CodePendingProposals)
	}
	// 作者处理全部提议后不再阻塞。
	if _, _, ps := state.Confirm(root, w, "state-1", "作者确认灯芯易主", time.Date(2026, 6, 20, 12, 0, 0, 0, time.UTC)); len(ps) != 0 {
		t.Fatal(ps)
	}
	if _, _, ps := knowledge.Confirm(root, w, "know-1", "作者确认读者知情", time.Date(2026, 6, 20, 12, 0, 0, 0, time.UTC)); len(ps) != 0 {
		t.Fatal(ps)
	}
	if ps := AcceptanceBlockers(root, w, u); len(ps) != 0 {
		t.Fatalf("problems = %+v, want none", ps)
	}
	// 正文被修改：closeout.stale。
	writeAt(t, root, ".writing-anything/works/my-novel/units/scene-1/draft.md", "修改后的正文。\n")
	if ps := AcceptanceBlockers(root, w, u); !has(ps, CodeStale, "") {
		t.Fatalf("problems = %+v, want %s", ps, CodeStale)
	}
	// 收口后契约扩展：closeout.missing-state-change。
	writeAt(t, root, ".writing-anything/works/my-novel/units/scene-1/outline.yaml", `schema_version: 1
sections:
  - id: sc1
    title: 灯下对峙
    state_changes:
      - {id: sc-1, text: 精灵交出灯芯}
      - {id: sc-9, text: 新要求}
    plants: [fs-1]
    recovers: [f-1]
    events: [ev-2]
`)
	w, ps := work.Load(root, "my-novel")
	if len(ps) != 0 {
		t.Fatal(ps)
	}
	if ps = AcceptanceBlockers(root, w, unit(t, w, "my-novel/scene-1")); !has(ps, CodeMissingStateChange, "") {
		t.Fatalf("problems = %+v, want %s", ps, CodeMissingStateChange)
	}
	// 无契约字段且无收口产物的场景：不做收口检查。
	if ps := AcceptanceBlockers(root, w, unit(t, w, "my-novel/part-one")); len(ps) != 0 {
		t.Fatalf("non-scene problems = %+v", ps)
	}
}

func TestCheckRecordAndWorks(t *testing.T) {
	root, w := newNovel(t)
	u := unit(t, w, "my-novel/scene-1")
	if _, _, ps := Submit(root, w, u, fullInput(), false); len(ps) != 0 {
		t.Fatal(ps)
	}
	checked, ps := CheckWorks(root, []*work.Work{w})
	found := false
	for _, c := range checked {
		if strings.HasSuffix(c, "units/scene-1/closeout.yaml") {
			found = true
		}
	}
	if !found || len(ps) != 0 {
		t.Fatalf("checked = %v, problems = %+v", checked, ps)
	}
	// 手写产物：悬空 state 引用与未声明事件。
	writeAt(t, root, ".writing-anything/works/my-novel/units/scene-2/closeout.yaml", `schema_version: 1
draft_sha256: e3b0c44298fc1c149afbf4c8996fb92427ae41e4649b934ca495991b7852b855
state: [{id: state-9}]
events: [ev-9]
`)
	w, ps2 := work.Load(root, "my-novel")
	if len(ps2) != 0 {
		t.Fatal(ps2)
	}
	_, ps = CheckWorks(root, []*work.Work{w})
	if !has(ps, CodeUnknownEntry, "state[0].id") || !has(ps, CodeUnknownEvent, "events[0]") {
		t.Fatalf("problems = %+v", ps)
	}
	// validate <path> 的单产物检查。
	rel := ".writing-anything/works/my-novel/units/scene-2/closeout.yaml"
	if ps := CheckArtifact(root, rel); !has(ps, CodeUnknownEntry, "") {
		t.Fatalf("artifact problems = %+v", ps)
	}
}

// snapshotTree 汇总 root 下子树的文件内容，用于断言被拒绝的命令没有写入任何文件。
func snapshotTree(t *testing.T, root, rel string) string {
	t.Helper()
	var b strings.Builder
	err := filepath.WalkDir(filepath.Join(root, rel), func(path string, d os.DirEntry, err error) error {
		if err != nil || d.IsDir() {
			return err
		}
		data, err := os.ReadFile(path)
		if err != nil {
			return err
		}
		rel, _ := filepath.Rel(root, path)
		b.WriteString(filepath.ToSlash(rel))
		b.WriteByte(':')
		b.Write(data)
		b.WriteByte('\n')
		return nil
	})
	if err != nil {
		t.Fatal(err)
	}
	return b.String()
}

