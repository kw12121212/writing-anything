package state

import (
	"time"
	"encoding/json"
	"os"
	"path/filepath"
	"slices"
	"strings"
	"testing"

	"github.com/kw12121212/writing-anything/internal/work"
	"github.com/kw12121212/writing-anything/internal/workspace"
)

// newNovel 初始化 novel 工作区：my-novel/part-one/chapter-one/{scene-1,scene-2} 与
// my-novel/part-one/chapter-two/scene-3（先序 scene-1、scene-2、scene-3），并登记设定条目 canon-1（人物，
// 带名字）与 canon-2（世界规则，无名字）。
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

func TestProposeCreatesAndDefaults(t *testing.T) {
	root, w := newNovel(t)
	rel := Path("my-novel")
	e, changed, ps := Propose(root, w, ProposeInput{Scene: "my-novel/scene-1", Entry: "canon-1", State: "左手旧伤复发"})
	if len(ps) > 0 {
		t.Fatal(ps)
	}
	if e.ID != "state-1" || e.Status != StatusProposed || e.Confirmation != nil || e.DecidedAt != nil {
		t.Fatalf("event = %+v", e)
	}
	if !slices.Equal(changed, []string{rel}) {
		t.Fatalf("changed = %v", changed)
	}
	e, changed, ps = Propose(root, w, ProposeInput{Scene: "my-novel/scene-2", Entry: "canon-2", State: "守灯人灯枯"})
	if len(ps) > 0 {
		t.Fatal(ps)
	}
	if e.ID != "state-2" || len(changed) != 1 {
		t.Fatalf("second event = %+v, changed = %v", e, changed)
	}
	data, err := os.ReadFile(filepath.Join(root, filepath.FromSlash(rel)))
	if err != nil {
		t.Fatal(err)
	}
	for _, want := range []string{"schema_version: 1", "id: state-1", "scene: my-novel/scene-1", "entry: canon-1", "status: proposed"} {
		if !strings.Contains(string(data), want) {
			t.Fatalf("state.yaml missing %q:\n%s", want, data)
		}
	}
}

func TestProposeRejects(t *testing.T) {
	root, w := newNovel(t)
	// 绑定场景不是 scene 单元。
	if _, _, ps := Propose(root, w, ProposeInput{Scene: "my-novel/chapter-one", Entry: "canon-1", State: "受伤"}); !has(ps, CodeUnknownUnit, "scene") {
		t.Fatalf("want state.unknown-unit, got %v", ps)
	}
	// 绑定场景不存在。
	if _, _, ps := Propose(root, w, ProposeInput{Scene: "my-novel/scene-9", Entry: "canon-1", State: "受伤"}); !has(ps, CodeUnknownUnit, "scene") {
		t.Fatalf("want state.unknown-unit, got %v", ps)
	}
	// 设定条目不在设定集中。
	if _, _, ps := Propose(root, w, ProposeInput{Scene: "my-novel/scene-1", Entry: "canon-9", State: "受伤"}); !has(ps, CodeUnknownEntry, "entry") {
		t.Fatalf("want state.unknown-entry, got %v", ps)
	}
	// 没有设定集时所有 entry 引用都悬空。
	root2, w2 := newNovel(t)
	if err := os.Remove(filepath.Join(root2, ".writing-anything", "works", "my-novel", "canon.yaml")); err != nil {
		t.Fatal(err)
	}
	if _, _, ps := Propose(root2, w2, ProposeInput{Scene: "my-novel/scene-1", Entry: "canon-1", State: "受伤"}); !has(ps, CodeUnknownEntry, "entry") {
		t.Fatalf("want state.unknown-entry without canon.yaml, got %v", ps)
	}
	// 状态描述为空白、id 不合法、id 重复。
	if _, _, ps := Propose(root, w, ProposeInput{Scene: "my-novel/scene-1", Entry: "canon-1", State: " "}); !has(ps, "artifact.schema-violation", "state") {
		t.Fatalf("want schema-violation state, got %v", ps)
	}
	if _, _, ps := Propose(root, w, ProposeInput{Scene: "my-novel/scene-1", Entry: "canon-1", State: "受伤", ID: "Bad"}); !has(ps, "artifact.schema-violation", "id") {
		t.Fatalf("want schema-violation id, got %v", ps)
	}
	if _, _, ps := Propose(root, w, ProposeInput{Scene: "my-novel/scene-1", Entry: "canon-1", State: "受伤"}); len(ps) > 0 {
		t.Fatal(ps)
	}
	if _, _, ps := Propose(root, w, ProposeInput{Scene: "my-novel/scene-1", Entry: "canon-1", State: "受伤", ID: "state-1"}); !has(ps, "artifact.duplicate-id", "id") {
		t.Fatalf("want duplicate-id, got %v", ps)
	}
	// 失败不写文件。
	if _, err := os.Stat(filepath.Join(root, ".writing-anything", "works", "my-novel", "state.yaml")); err != nil {
		t.Fatalf("failed propose should not create state.yaml: %v", err)
	}
}

func TestConfirmRejectTransitions(t *testing.T) {
	root, w := newNovel(t)
	if _, _, ps := Propose(root, w, ProposeInput{Scene: "my-novel/scene-1", Entry: "canon-1", State: "左手旧伤复发"}); len(ps) > 0 {
		t.Fatal(ps)
	}
	// 摘要为空白。
	if _, _, ps := Confirm(root, w, "state-1", " ", time.Date(2026, 6, 20, 12, 0, 0, 0, time.UTC)); !has(ps, CodeIncompleteConfirmation, "confirmation") {
		t.Fatalf("want incomplete-confirmation, got %v", ps)
	}
	e, changed, ps := Confirm(root, w, "state-1", "作者确认旧伤复发", time.Date(2026, 6, 20, 12, 0, 0, 0, time.UTC))
	if len(ps) > 0 {
		t.Fatal(ps)
	}
	if e.Status != StatusConfirmed || e.Confirmation == nil || *e.Confirmation != "作者确认旧伤复发" || e.DecidedAt == nil || !strings.HasSuffix(*e.DecidedAt, "Z") {
		t.Fatalf("confirmed event = %+v", e)
	}
	if len(changed) != 1 {
		t.Fatalf("changed = %v", changed)
	}
	// 重复确认。
	if _, _, ps := Confirm(root, w, "state-1", "再次确认", time.Date(2026, 6, 20, 12, 0, 0, 0, time.UTC)); !has(ps, CodeAlreadyDecided, "") {
		t.Fatalf("want already-decided, got %v", ps)
	}
	// 撤销已确认条目。
	e, _, ps = Reject(root, w, "state-1", "作者撤销该变化", time.Date(2026, 6, 20, 12, 0, 0, 0, time.UTC))
	if len(ps) > 0 {
		t.Fatal(ps)
	}
	if e.Status != StatusRejected {
		t.Fatalf("rejected event = %+v", e)
	}
	// rejected 是终态。
	if _, _, ps := Reject(root, w, "state-1", "再拒绝", time.Date(2026, 6, 20, 12, 0, 0, 0, time.UTC)); !has(ps, CodeAlreadyDecided, "") {
		t.Fatalf("want already-decided for rejected, got %v", ps)
	}
	if _, _, ps := Confirm(root, w, "state-1", "再确认", time.Date(2026, 6, 20, 12, 0, 0, 0, time.UTC)); !has(ps, CodeAlreadyDecided, "") {
		t.Fatalf("want already-decided confirming rejected, got %v", ps)
	}
	// 条目不存在。
	if _, _, ps := Confirm(root, w, "state-9", "x", time.Date(2026, 6, 20, 12, 0, 0, 0, time.UTC)); !has(ps, CodeNotFound, "") {
		t.Fatalf("want not-found, got %v", ps)
	}
}

func TestSetRules(t *testing.T) {
	root, w := newNovel(t)
	Propose(root, w, ProposeInput{Scene: "my-novel/scene-1", Entry: "canon-1", State: "左手旧伤复发", ID: "p-1"})
	Propose(root, w, ProposeInput{Scene: "my-novel/scene-1", Entry: "canon-1", State: "旧伤复发，握不住灯杆", ID: "c-1"})
	// proposed 条目自由修改。
	e, changed, ps := Set(root, w, "p-1", SetInput{State: ptr("旧伤复发，隐痛")}, time.Date(2026, 6, 20, 12, 0, 0, 0, time.UTC))
	if len(ps) > 0 {
		t.Fatal(ps)
	}
	if e.State != "旧伤复发，隐痛" || e.Status != StatusProposed || len(changed) != 1 {
		t.Fatalf("set proposed = %+v, changed = %v", e, changed)
	}
	// 取值未变不写文件。
	_, changed, ps = Set(root, w, "p-1", SetInput{State: ptr("旧伤复发，隐痛")}, time.Date(2026, 6, 20, 12, 0, 0, 0, time.UTC))
	if len(ps) > 0 {
		t.Fatal(ps)
	}
	if len(changed) != 0 {
		t.Fatalf("no-op set changed = %v", changed)
	}
	// confirmed 条目缺作者重新确认。
	Confirm(root, w, "c-1", "作者确认", time.Date(2026, 6, 20, 12, 0, 0, 0, time.UTC))
	if _, _, ps := Set(root, w, "c-1", SetInput{State: ptr("重伤")}, time.Date(2026, 6, 20, 12, 0, 0, 0, time.UTC)); !has(ps, CodeIncompleteConfirmation, "confirmation") {
		t.Fatalf("want incomplete-confirmation, got %v", ps)
	}
	e, _, ps = Set(root, w, "c-1", SetInput{State: ptr("重伤"), ConfirmedByAuthor: true, Confirmation: "作者修正为重伤"}, time.Date(2026, 6, 20, 12, 0, 0, 0, time.UTC))
	if len(ps) > 0 {
		t.Fatal(ps)
	}
	if e.State != "重伤" || e.Status != StatusConfirmed || *e.Confirmation != "作者修正为重伤" {
		t.Fatalf("set confirmed = %+v", e)
	}
	// rejected 条目是终态。
	Reject(root, w, "p-1", "作者拒绝", time.Date(2026, 6, 20, 12, 0, 0, 0, time.UTC))
	if _, _, ps := Set(root, w, "p-1", SetInput{State: ptr("x")}, time.Date(2026, 6, 20, 12, 0, 0, 0, time.UTC)); !has(ps, CodeAlreadyDecided, "") {
		t.Fatalf("want already-decided, got %v", ps)
	}
	// --scene 校验与空白 --state。
	Propose(root, w, ProposeInput{Scene: "my-novel/scene-1", Entry: "canon-1", State: "x", ID: "p-2"})
	if _, _, ps := Set(root, w, "p-2", SetInput{Scene: ptr("my-novel/chapter-one")}, time.Date(2026, 6, 20, 12, 0, 0, 0, time.UTC)); !has(ps, CodeUnknownUnit, "scene") {
		t.Fatalf("want unknown-unit, got %v", ps)
	}
	if _, _, ps := Set(root, w, "p-2", SetInput{State: ptr(" ")}, time.Date(2026, 6, 20, 12, 0, 0, 0, time.UTC)); !has(ps, "artifact.schema-violation", "state") {
		t.Fatalf("want schema-violation, got %v", ps)
	}
	if _, _, ps := Set(root, w, "state-9", SetInput{State: ptr("x")}, time.Date(2026, 6, 20, 12, 0, 0, 0, time.UTC)); !has(ps, CodeNotFound, "") {
		t.Fatalf("want not-found, got %v", ps)
	}
}

func TestListFilterAndOrder(t *testing.T) {
	root, w := newNovel(t)
	Propose(root, w, ProposeInput{Scene: "my-novel/scene-2", Entry: "canon-1", State: "b", ID: "ev-2"})
	Propose(root, w, ProposeInput{Scene: "my-novel/scene-1", Entry: "canon-1", State: "a", ID: "ev-1"})
	Propose(root, w, ProposeInput{Scene: "my-novel/scene-3", Entry: "canon-2", State: "c", ID: "ev-3"})
	Confirm(root, w, "ev-1", "作者确认 a", time.Date(2026, 6, 20, 12, 0, 0, 0, time.UTC))
	events, ps := List(root, w, false)
	if len(ps) > 0 {
		t.Fatal(ps)
	}
	if len(events) != 2 || events[0].ID != "ev-2" || events[1].ID != "ev-3" {
		t.Fatalf("proposed events = %+v", events)
	}
	events, _ = List(root, w, true)
	if len(events) != 3 || events[0].ID != "ev-1" || events[1].ID != "ev-2" || events[2].ID != "ev-3" {
		t.Fatalf("all events should follow scene pre-order, got %+v", events)
	}
	// 没有台账时为空。
	root2, w2 := newNovel(t)
	if events, ps = List(root2, w2, true); len(events) != 0 || len(ps) > 0 {
		t.Fatalf("empty ledger: events = %v, ps = %v", events, ps)
	}
}

func TestSnapshotReplay(t *testing.T) {
	root, w := newNovel(t)
	// scene-1 上 canon-1 的 confirmed 提议、canon-2 的 proposed 提议；scene-2 上 canon-1 的 rejected 提议。
	Propose(root, w, ProposeInput{Scene: "my-novel/scene-1", Entry: "canon-1", State: "左手旧伤复发", ID: "ev-1"})
	Propose(root, w, ProposeInput{Scene: "my-novel/scene-1", Entry: "canon-2", State: "灯枯", ID: "ev-2"})
	Propose(root, w, ProposeInput{Scene: "my-novel/scene-2", Entry: "canon-1", State: "误写的状态", ID: "ev-3"})
	Confirm(root, w, "ev-1", "作者确认旧伤复发", time.Date(2026, 6, 20, 12, 0, 0, 0, time.UTC))
	Reject(root, w, "ev-3", "作者撤销", time.Date(2026, 6, 20, 12, 0, 0, 0, time.UTC))
	scene1 := unitByRef(t, w, "my-novel/scene-1")
	scene2 := unitByRef(t, w, "my-novel/scene-2")
	scene3 := unitByRef(t, w, "my-novel/scene-3")

	// 场景开始时不含自身发生的变化；proposed 与 rejected 不参与。
	items, ps := Snapshot(root, w, scene1)
	if len(ps) > 0 {
		t.Fatal(ps)
	}
	if len(items) != 0 {
		t.Fatalf("scene-1 start snapshot = %+v", items)
	}
	items, ps = Snapshot(root, w, scene2)
	if len(ps) > 0 {
		t.Fatal(ps)
	}
	if len(items) != 1 || items[0].Entry != "canon-1" || items[0].State != "左手旧伤复发" || items[0].Proposal != "ev-1" || items[0].Scene != "my-novel/scene-1" {
		t.Fatalf("scene-2 start snapshot = %+v", items)
	}
	if items[0].Name == nil || *items[0].Name != "守灯人" {
		t.Fatalf("name = %v", items[0].Name)
	}

	// 后写覆盖：scene-2 上 canon-1 的 confirmed 提议生效后，scene-3 看到的是它。
	Propose(root, w, ProposeInput{Scene: "my-novel/scene-2", Entry: "canon-1", State: "伤愈", ID: "ev-4"})
	Confirm(root, w, "ev-4", "作者确认伤愈", time.Date(2026, 6, 20, 12, 0, 0, 0, time.UTC))
	items, _ = Snapshot(root, w, scene3)
	if len(items) != 1 || items[0].State != "伤愈" || items[0].Proposal != "ev-4" {
		t.Fatalf("later event should win, got %+v", items)
	}

	// 确定性：同一输入两次运行输出完全一致（Name 是指针，按 JSON 输出比较）。
	again, _ := Snapshot(root, w, scene3)
	if snapshotJSON(t, items) != snapshotJSON(t, again) {
		t.Fatalf("snapshot not deterministic: %s vs %s", snapshotJSON(t, items), snapshotJSON(t, again))
	}

	// 修改早期条目并重新确认后，后续快照随之变化。
	if _, _, ps := Set(root, w, "ev-1", SetInput{State: ptr("重伤"), ConfirmedByAuthor: true, Confirmation: "作者修正为重伤"}, time.Date(2026, 6, 20, 12, 0, 0, 0, time.UTC)); len(ps) > 0 {
		t.Fatal(ps)
	}
	if _, _, ps := Set(root, w, "ev-4", SetInput{State: ptr("重伤初愈"), ConfirmedByAuthor: true, Confirmation: "作者修正"}, time.Date(2026, 6, 20, 12, 0, 0, 0, time.UTC)); len(ps) > 0 {
		t.Fatal(ps)
	}
	// scene-2 仍看到 ev-1 的新描述。
	items, _ = Snapshot(root, w, scene2)
	if items[0].State != "重伤" {
		t.Fatalf("early edit should recompute snapshot, got %+v", items)
	}
	// 撤销早期条目后，后续快照不再含该状态。
	if _, _, ps := Reject(root, w, "ev-4", "作者撤销伤愈", time.Date(2026, 6, 20, 12, 0, 0, 0, time.UTC)); len(ps) > 0 {
		t.Fatal(ps)
	}
	items, _ = Snapshot(root, w, scene3)
	if len(items) != 1 || items[0].State != "重伤" {
		t.Fatalf("after rejecting ev-4, scene-3 should fall back to ev-1, got %+v", items)
	}
	if _, _, ps := Reject(root, w, "ev-1", "作者撤销重伤", time.Date(2026, 6, 20, 12, 0, 0, 0, time.UTC)); len(ps) > 0 {
		t.Fatal(ps)
	}
	items, _ = Snapshot(root, w, scene3)
	if len(items) != 0 {
		t.Fatalf("all events revoked, snapshot = %+v", items)
	}
}

func TestSnapshotCrossChapterOrder(t *testing.T) {
	// 章节点在先序中先于其子场景，重放只按场景先序比较（lesson：不能直接比较章与场景的位置）。
	root, w := newNovel(t)
	Propose(root, w, ProposeInput{Scene: "my-novel/scene-3", Entry: "canon-1", State: "章二的变化", ID: "ev-late"})
	Propose(root, w, ProposeInput{Scene: "my-novel/scene-1", Entry: "canon-1", State: "章一的变化", ID: "ev-early"})
	Confirm(root, w, "ev-late", "作者确认", time.Date(2026, 6, 20, 12, 0, 0, 0, time.UTC))
	Confirm(root, w, "ev-early", "作者确认", time.Date(2026, 6, 20, 12, 0, 0, 0, time.UTC))
	items, ps := Snapshot(root, w, unitByRef(t, w, "my-novel/scene-3"))
	if len(ps) > 0 {
		t.Fatal(ps)
	}
	// scene-3 自身的条目不参与，快照取 scene-1 的早期变化。
	if len(items) != 1 || items[0].Proposal != "ev-early" {
		t.Fatalf("cross-chapter replay = %+v", items)
	}
}

func TestSnapshotValidationProblems(t *testing.T) {
	root, w := newNovel(t)
	scene3 := unitByRef(t, w, "my-novel/scene-3")
	// 没有台账时为空快照。
	if items, ps := Snapshot(root, w, scene3); len(items) != 0 || len(ps) > 0 {
		t.Fatalf("empty ledger snapshot = %v, ps = %v", items, ps)
	}
	// 悬空 entry / scene 引用、确认记录不完整时报告并返回 nil。
	rel := Path("my-novel")
	os.MkdirAll(filepath.Dir(filepath.Join(root, filepath.FromSlash(rel))), 0o755)
	os.WriteFile(filepath.Join(root, filepath.FromSlash(rel)), []byte(`schema_version: 1
events:
  - id: state-1
    scene: my-novel/scene-1
    entry: canon-9
    state: 受伤
    status: confirmed
    confirmation: 作者确认
    decided_at: 2026-10-03T08:00:00Z
  - id: state-2
    scene: my-novel/scene-9
    entry: canon-1
    state: 受伤
    status: confirmed
    confirmation: 作者确认
    decided_at: 2026-10-03T08:00:00Z
  - id: state-3
    scene: my-novel/scene-1
    entry: canon-1
    state: 受伤
    status: confirmed
`), 0o644)
	items, ps := Snapshot(root, w, scene3)
	if items != nil {
		t.Fatalf("snapshot should be nil with problems, got %+v", items)
	}
	if !has(ps, CodeUnknownEntry, "events[0].entry") || !has(ps, CodeUnknownUnit, "events[1].scene") || !has(ps, CodeIncompleteConfirmation, "events[2]") {
		t.Fatalf("problems = %v", ps)
	}
	// 设定集损坏时报告其问题。
	os.WriteFile(filepath.Join(root, ".writing-anything", "works", "my-novel", "canon.yaml"), []byte("schema_version: 1\nentries: 3\n"), 0o644)
	if _, ps := Snapshot(root, w, scene3); len(ps) == 0 {
		t.Fatal("broken canon.yaml should be reported")
	}
}

func TestCheckConfirmationForms(t *testing.T) {
	// 取值化字段（value-based-optional-fields）：「非 proposed 且 null/缺省」报领域错误码，「proposed 且 null」通过。
	root, w := newNovel(t)
	rel := Path("my-novel")
	os.MkdirAll(filepath.Dir(filepath.Join(root, filepath.FromSlash(rel))), 0o755)
	for name, body := range map[string]string{
		"confirmed-null-confirmation": `schema_version: 1
events:
  - id: state-1
    scene: my-novel/scene-1
    entry: canon-1
    state: 受伤
    status: confirmed
    confirmation: null
    decided_at: 2026-10-03T08:00:00Z
`,
		"confirmed-missing-confirmation": `schema_version: 1
events:
  - id: state-1
    scene: my-novel/scene-1
    entry: canon-1
    state: 受伤
    status: confirmed
    decided_at: 2026-10-03T08:00:00Z
`,
		"rejected-missing-decided-at": `schema_version: 1
events:
  - id: state-1
    scene: my-novel/scene-1
    entry: canon-1
    state: 受伤
    status: rejected
    confirmation: 作者撤销
`,
	} {
		os.WriteFile(filepath.Join(root, filepath.FromSlash(rel)), []byte(body), 0o644)
		checked, ps := CheckWorks(root, []*work.Work{w})
		if len(checked) != 1 || !has(ps, CodeIncompleteConfirmation, "events[0]") {
			t.Fatalf("%s: checked = %v, problems = %v", name, checked, ps)
		}
	}
	os.WriteFile(filepath.Join(root, filepath.FromSlash(rel)), []byte(`schema_version: 1
events:
  - id: state-1
    scene: my-novel/scene-1
    entry: canon-1
    state: 受伤
    status: proposed
    confirmation: null
`), 0o644)
	checked, ps := CheckWorks(root, []*work.Work{w})
	if len(checked) != 1 || len(ps) != 0 {
		t.Fatalf("proposed with null confirmation should pass, checked = %v, problems = %v", checked, ps)
	}
}

func TestCheckWorksAndArtifact(t *testing.T) {
	root, w := newNovel(t)
	// 没有台账的作品不做任何检查。
	if checked, ps := CheckWorks(root, []*work.Work{w}); len(checked) != 0 || len(ps) != 0 {
		t.Fatalf("no ledger: checked = %v, ps = %v", checked, ps)
	}
	rel := Path("my-novel")
	os.MkdirAll(filepath.Dir(filepath.Join(root, filepath.FromSlash(rel))), 0o755)
	os.WriteFile(filepath.Join(root, filepath.FromSlash(rel)), []byte(`schema_version: 1
events:
  - id: state-1
    scene: my-novel/scene-1
    entry: canon-1
    state: 受伤
    status: proposed
  - id: state-1
    scene: my-novel/chapter-one
    entry: canon-1
    state: 受伤
    status: proposed
`), 0o644)
	checked, ps := CheckWorks(root, []*work.Work{w})
	if !slices.Equal(checked, []string{rel}) {
		t.Fatalf("checked = %v", checked)
	}
	if !has(ps, "artifact.duplicate-id", "events[1].id") || !has(ps, CodeUnknownUnit, "events[1].scene") {
		t.Fatalf("problems = %v", ps)
	}
	// validate <path> 单产物校验。
	ps = CheckArtifact(root, rel)
	if !has(ps, "artifact.duplicate-id", "") || !has(ps, CodeUnknownUnit, "events[1].scene") {
		t.Fatalf("artifact problems = %v", ps)
	}
	// 台账途经符号链接按 artifact.not-found 处理。
	root2, w2 := newNovel(t)
	outside := filepath.Join(root2, "outside.yaml")
	os.WriteFile(outside, []byte("schema_version: 1\nevents: []\n"), 0o644)
	os.MkdirAll(filepath.Join(root2, ".writing-anything", "works", "my-novel"), 0o755)
	if err := os.Symlink(outside, filepath.Join(root2, ".writing-anything", "works", "my-novel", "state.yaml")); err != nil {
		t.Fatal(err)
	}
	if checked, ps := CheckWorks(root2, []*work.Work{w2}); len(checked) != 1 || !has(ps, "artifact.not-found", "") {
		t.Fatalf("symlink: checked = %v, ps = %v", checked, ps)
	}
}

func TestProposePreservesCommentsAndDanglingTolerated(t *testing.T) {
	root, w := newNovel(t)
	rel := Path("my-novel")
	os.MkdirAll(filepath.Dir(filepath.Join(root, filepath.FromSlash(rel))), 0o755)
	os.WriteFile(filepath.Join(root, filepath.FromSlash(rel)), []byte(`schema_version: 1
events:
  # 守灯人的旧伤在灯室对峙后复发
  - id: hand-made
    scene: my-novel/scene-9 # 绑错了场景，悬空引用
    entry: canon-1
    state: 受伤
    status: proposed
`), 0o644)
	_, changed, warns := Propose(root, w, ProposeInput{Scene: "my-novel/scene-1", Entry: "canon-1", State: "左手旧伤复发"})
	for _, p := range warns {
		if p.Severity == workspace.SeverityError {
			t.Fatalf("dangling refs elsewhere should not block propose: %v", warns)
		}
	}
	if !has(warns, CodeUnknownUnit, "events[0].scene") {
		t.Fatalf("dangling refs should be reported as warnings: %v", warns)
	}
	if len(changed) != 1 {
		t.Fatalf("changed = %v", changed)
	}
	data, err := os.ReadFile(filepath.Join(root, filepath.FromSlash(rel)))
	if err != nil {
		t.Fatal(err)
	}
	if !strings.Contains(string(data), "# 守灯人的旧伤在灯室对峙后复发") {
		t.Fatalf("comments should be preserved:\n%s", data)
	}
	// 悬空引用在列出时以 warning 报告。
	_, listWarns := List(root, w, true)
	if !has(listWarns, CodeUnknownUnit, "events[0].scene") {
		t.Fatalf("warnings = %v", warns)
	}
	for _, p := range listWarns {
		if p.Severity != workspace.SeverityWarning {
			t.Fatalf("dangling refs should be warnings, got %v", p)
		}
	}
}

func TestAnchoredEventsRejected(t *testing.T) {
	root, w := newNovel(t)
	rel := Path("my-novel")
	os.MkdirAll(filepath.Dir(filepath.Join(root, filepath.FromSlash(rel))), 0o755)
	os.WriteFile(filepath.Join(root, filepath.FromSlash(rel)), []byte("schema_version: 1\nevents: &anchor []\n"), 0o644)
	if _, _, ps := Propose(root, w, ProposeInput{Scene: "my-novel/scene-1", Entry: "canon-1", State: "受伤"}); !has(ps, CodeAnchoredValue, "events") {
		t.Fatalf("anchored events should be rejected, got %v", ps)
	}
}

func TestUnitRefUsedByStateAt(t *testing.T) {
	root, w := newNovel(t)
	Propose(root, w, ProposeInput{Scene: "my-novel/scene-1", Entry: "canon-2", State: "灯枯", ID: "ev-1"})
	Confirm(root, w, "ev-1", "作者确认", time.Date(2026, 6, 20, 12, 0, 0, 0, time.UTC))
	items, ps := Snapshot(root, w, unitByRef(t, w, "my-novel/scene-2"))
	if len(ps) > 0 {
		t.Fatal(ps)
	}
	// world-rule 条目没有 name，快照中为 null。
	if len(items) != 1 || items[0].Entry != "canon-2" || items[0].Name != nil {
		t.Fatalf("world-rule snapshot item = %+v", items)
	}
}

func unitByRef(t *testing.T, w *work.Work, ref string) *work.Unit {
	t.Helper()
	for _, u := range w.Units() {
		if u.Ref == ref {
			return u
		}
	}
	t.Fatalf("unit %s not found", ref)
	return nil
}

func ptr(v string) *string { return &v }

func snapshotJSON(t *testing.T, items []Item) string {
	t.Helper()
	data, err := json.Marshal(items)
	if err != nil {
		t.Fatal(err)
	}
	return string(data)
}
