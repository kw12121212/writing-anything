package knowledge

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
// 带名字）与 canon-2（世界规则，无名字，作为被获知的信息）。
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

func itemsJSON(t *testing.T, items []Item) string {
	t.Helper()
	data, err := json.Marshal(items)
	if err != nil {
		t.Fatal(err)
	}
	return string(data)
}

func TestProposeCreatesAndDefaults(t *testing.T) {
	root, w := newNovel(t)
	rel := Path("my-novel")
	e, changed, ps := Propose(root, w, ProposeInput{Scene: "my-novel/scene-1", Entry: "canon-2", Character: "canon-1"})
	if len(ps) > 0 {
		t.Fatal(ps)
	}
	if e.ID != "know-1" || e.Status != StatusProposed || e.Confirmation != nil || e.DecidedAt != nil {
		t.Fatalf("event = %+v", e)
	}
	if !slices.Equal(changed, []string{rel}) {
		t.Fatalf("changed = %v", changed)
	}
	// reader 作为获知者。
	e, changed, ps = Propose(root, w, ProposeInput{Scene: "my-novel/scene-2", Entry: "canon-2", Character: Reader})
	if len(ps) > 0 {
		t.Fatal(ps)
	}
	if e.ID != "know-2" || e.Character != Reader || len(changed) != 1 {
		t.Fatalf("reader event = %+v, changed = %v", e, changed)
	}
	data, err := os.ReadFile(filepath.Join(root, filepath.FromSlash(rel)))
	if err != nil {
		t.Fatal(err)
	}
	for _, want := range []string{"schema_version: 1", "id: know-1", "scene: my-novel/scene-1", "entry: canon-2", "character: canon-1", "status: proposed"} {
		if !strings.Contains(string(data), want) {
			t.Fatalf("knowledge.yaml missing %q:\n%s", want, data)
		}
	}
}

func TestProposeRejects(t *testing.T) {
	root, w := newNovel(t)
	// 绑定场景不是 scene 单元。
	if _, _, ps := Propose(root, w, ProposeInput{Scene: "my-novel/chapter-one", Entry: "canon-2", Character: "canon-1"}); !has(ps, CodeUnknownUnit, "scene") {
		t.Fatalf("want knowledge.unknown-unit, got %v", ps)
	}
	// 绑定场景不存在。
	if _, _, ps := Propose(root, w, ProposeInput{Scene: "my-novel/scene-9", Entry: "canon-2", Character: "canon-1"}); !has(ps, CodeUnknownUnit, "scene") {
		t.Fatalf("want knowledge.unknown-unit, got %v", ps)
	}
	// 设定条目不在设定集中。
	if _, _, ps := Propose(root, w, ProposeInput{Scene: "my-novel/scene-1", Entry: "canon-9", Character: "canon-1"}); !has(ps, CodeUnknownEntry, "entry") {
		t.Fatalf("want knowledge.unknown-entry, got %v", ps)
	}
	// 获知者是设定条目但不是 character 类型。
	if _, _, ps := Propose(root, w, ProposeInput{Scene: "my-novel/scene-1", Entry: "canon-2", Character: "canon-2"}); !has(ps, CodeUnknownCharacter, "character") {
		t.Fatalf("want knowledge.unknown-character for non-character entry, got %v", ps)
	}
	// 获知者不在设定集中。
	if _, _, ps := Propose(root, w, ProposeInput{Scene: "my-novel/scene-1", Entry: "canon-2", Character: "canon-9"}); !has(ps, CodeUnknownCharacter, "character") {
		t.Fatalf("want knowledge.unknown-character for missing entry, got %v", ps)
	}
	// 没有设定集时条目集合按空处理（lesson：canon.List 的 nil 与问题不可混用）：entry 检查先命中，
	// reader 仍可作获知者（character 检查对其放行）。
	root2, w2 := newNovel(t)
	if err := os.Remove(filepath.Join(root2, ".writing-anything", "works", "my-novel", "canon.yaml")); err != nil {
		t.Fatal(err)
	}
	if _, _, ps := Propose(root2, w2, ProposeInput{Scene: "my-novel/scene-1", Entry: "canon-2", Character: "canon-1"}); !has(ps, CodeUnknownEntry, "entry") {
		t.Fatalf("want knowledge.unknown-entry without canon.yaml, got %v", ps)
	}
	if _, _, ps := Propose(root2, w2, ProposeInput{Scene: "my-novel/scene-1", Entry: "canon-2", Character: Reader}); !has(ps, CodeUnknownEntry, "entry") {
		t.Fatalf("want knowledge.unknown-entry without canon.yaml, got %v", ps)
	}
	// id 不合法、id 重复。
	if _, _, ps := Propose(root, w, ProposeInput{Scene: "my-novel/scene-1", Entry: "canon-2", Character: "canon-1", ID: "Bad"}); !has(ps, "artifact.schema-violation", "id") {
		t.Fatalf("want schema-violation id, got %v", ps)
	}
	if _, _, ps := Propose(root, w, ProposeInput{Scene: "my-novel/scene-1", Entry: "canon-2", Character: "canon-1"}); len(ps) > 0 {
		t.Fatal(ps)
	}
	if _, _, ps := Propose(root, w, ProposeInput{Scene: "my-novel/scene-1", Entry: "canon-2", Character: "canon-1", ID: "know-1"}); !has(ps, "artifact.duplicate-id", "id") {
		t.Fatalf("want duplicate-id, got %v", ps)
	}
	// 失败不写文件。
	if _, err := os.Stat(filepath.Join(root, ".writing-anything", "works", "my-novel", "knowledge.yaml")); err != nil {
		t.Fatalf("failed propose should not create knowledge.yaml: %v", err)
	}
}

func TestConfirmRejectTransitions(t *testing.T) {
	root, w := newNovel(t)
	if _, _, ps := Propose(root, w, ProposeInput{Scene: "my-novel/scene-1", Entry: "canon-2", Character: "canon-1"}); len(ps) > 0 {
		t.Fatal(ps)
	}
	// 摘要为空白。
	if _, _, ps := Confirm(root, w, "know-1", " ", time.Date(2026, 6, 20, 12, 0, 0, 0, time.UTC)); !has(ps, CodeIncompleteConfirmation, "confirmation") {
		t.Fatalf("want incomplete-confirmation, got %v", ps)
	}
	e, changed, ps := Confirm(root, w, "know-1", "作者确认守灯人得知灯油规则", time.Date(2026, 6, 20, 12, 0, 0, 0, time.UTC))
	if len(ps) > 0 {
		t.Fatal(ps)
	}
	if e.Status != StatusConfirmed || e.Confirmation == nil || *e.Confirmation != "作者确认守灯人得知灯油规则" || e.DecidedAt == nil || !strings.HasSuffix(*e.DecidedAt, "Z") {
		t.Fatalf("confirmed event = %+v", e)
	}
	if len(changed) != 1 {
		t.Fatalf("changed = %v", changed)
	}
	// 重复确认。
	if _, _, ps := Confirm(root, w, "know-1", "再次确认", time.Date(2026, 6, 20, 12, 0, 0, 0, time.UTC)); !has(ps, CodeAlreadyDecided, "") {
		t.Fatalf("want already-decided, got %v", ps)
	}
	// 撤销已确认条目。
	e, _, ps = RejectVia(root, w, "know-1", "作者撤销该知情", "preview", time.Date(2026, 6, 20, 12, 0, 0, 0, time.UTC))
	if len(ps) > 0 {
		t.Fatal(ps)
	}
	if e.Status != StatusRejected || e.Channel == nil || *e.Channel != "preview" {
		t.Fatalf("rejected event = %+v", e)
	}
	// rejected 是终态。
	if _, _, ps := Reject(root, w, "know-1", "再拒绝", time.Date(2026, 6, 20, 12, 0, 0, 0, time.UTC)); !has(ps, CodeAlreadyDecided, "") {
		t.Fatalf("want already-decided for rejected, got %v", ps)
	}
	if _, _, ps := Confirm(root, w, "know-1", "再确认", time.Date(2026, 6, 20, 12, 0, 0, 0, time.UTC)); !has(ps, CodeAlreadyDecided, "") {
		t.Fatalf("want already-decided confirming rejected, got %v", ps)
	}
	// 条目不存在。
	if _, _, ps := Confirm(root, w, "know-9", "x", time.Date(2026, 6, 20, 12, 0, 0, 0, time.UTC)); !has(ps, CodeNotFound, "") {
		t.Fatalf("want not-found, got %v", ps)
	}
}

func TestSetRules(t *testing.T) {
	root, w := newNovel(t)
	Propose(root, w, ProposeInput{Scene: "my-novel/scene-1", Entry: "canon-2", Character: "canon-1", ID: "p-1"})
	Propose(root, w, ProposeInput{Scene: "my-novel/scene-1", Entry: "canon-2", Character: Reader, ID: "c-1"})
	// proposed 条目自由修改。
	e, changed, ps := Set(root, w, "p-1", SetInput{Character: ptr("reader")}, time.Date(2026, 6, 20, 12, 0, 0, 0, time.UTC))
	if len(ps) > 0 {
		t.Fatal(ps)
	}
	if e.Character != Reader || e.Status != StatusProposed || len(changed) != 1 {
		t.Fatalf("set proposed = %+v, changed = %v", e, changed)
	}
	// 取值未变不写文件。
	_, changed, ps = Set(root, w, "p-1", SetInput{Character: ptr(Reader)}, time.Date(2026, 6, 20, 12, 0, 0, 0, time.UTC))
	if len(ps) > 0 {
		t.Fatal(ps)
	}
	if len(changed) != 0 {
		t.Fatalf("no-op set changed = %v", changed)
	}
	// confirmed 条目缺作者重新确认。
	Confirm(root, w, "c-1", "作者确认", time.Date(2026, 6, 20, 12, 0, 0, 0, time.UTC))
	if _, _, ps := Set(root, w, "c-1", SetInput{Entry: ptr("canon-1")}, time.Date(2026, 6, 20, 12, 0, 0, 0, time.UTC)); !has(ps, CodeIncompleteConfirmation, "confirmation") {
		t.Fatalf("want incomplete-confirmation, got %v", ps)
	}
	e, _, ps = Set(root, w, "c-1", SetInput{Entry: ptr("canon-1"), ConfirmedByAuthor: true, Confirmation: "作者改绑条目"}, time.Date(2026, 6, 20, 12, 0, 0, 0, time.UTC))
	if len(ps) > 0 {
		t.Fatal(ps)
	}
	if e.Entry != "canon-1" || e.Status != StatusConfirmed || *e.Confirmation != "作者改绑条目" {
		t.Fatalf("set confirmed = %+v", e)
	}
	// rejected 条目是终态。
	Reject(root, w, "p-1", "作者拒绝", time.Date(2026, 6, 20, 12, 0, 0, 0, time.UTC))
	if _, _, ps := Set(root, w, "p-1", SetInput{Character: ptr("canon-1")}, time.Date(2026, 6, 20, 12, 0, 0, 0, time.UTC)); !has(ps, CodeAlreadyDecided, "") {
		t.Fatalf("want already-decided, got %v", ps)
	}
	// --scene / --entry / --character 参数校验。
	Propose(root, w, ProposeInput{Scene: "my-novel/scene-1", Entry: "canon-2", Character: "canon-1", ID: "p-2"})
	if _, _, ps := Set(root, w, "p-2", SetInput{Scene: ptr("my-novel/chapter-one")}, time.Date(2026, 6, 20, 12, 0, 0, 0, time.UTC)); !has(ps, CodeUnknownUnit, "scene") {
		t.Fatalf("want unknown-unit, got %v", ps)
	}
	if _, _, ps := Set(root, w, "p-2", SetInput{Entry: ptr("canon-9")}, time.Date(2026, 6, 20, 12, 0, 0, 0, time.UTC)); !has(ps, CodeUnknownEntry, "entry") {
		t.Fatalf("want unknown-entry, got %v", ps)
	}
	if _, _, ps := Set(root, w, "p-2", SetInput{Character: ptr("canon-2")}, time.Date(2026, 6, 20, 12, 0, 0, 0, time.UTC)); !has(ps, CodeUnknownCharacter, "character") {
		t.Fatalf("want unknown-character, got %v", ps)
	}
	if _, _, ps := Set(root, w, "know-9", SetInput{Character: ptr(Reader)}, time.Date(2026, 6, 20, 12, 0, 0, 0, time.UTC)); !has(ps, CodeNotFound, "") {
		t.Fatalf("want not-found, got %v", ps)
	}
}

func TestListFilterAndOrder(t *testing.T) {
	root, w := newNovel(t)
	Propose(root, w, ProposeInput{Scene: "my-novel/scene-2", Entry: "canon-2", Character: "canon-1", ID: "ev-2"})
	Propose(root, w, ProposeInput{Scene: "my-novel/scene-1", Entry: "canon-2", Character: Reader, ID: "ev-1"})
	Propose(root, w, ProposeInput{Scene: "my-novel/scene-3", Entry: "canon-2", Character: "canon-1", ID: "ev-3"})
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

// 重放语义（里程碑完成标准）：只含按先序严格早于目标场景的 confirmed 条目；同获知者同条目取最早一条
// （earliest wins）；确定性；修改与撤销早期条目后重新计算；reader 与人物互不串扰。
func TestKnownAtReplay(t *testing.T) {
	root, w := newNovel(t)
	// scene-1 上 canon-1 获知 canon-2（confirmed）、reader 获知 canon-2（confirmed）；scene-2 上 canon-1 的 proposed。
	Propose(root, w, ProposeInput{Scene: "my-novel/scene-1", Entry: "canon-2", Character: "canon-1", ID: "ev-1"})
	Propose(root, w, ProposeInput{Scene: "my-novel/scene-1", Entry: "canon-2", Character: Reader, ID: "ev-r"})
	Propose(root, w, ProposeInput{Scene: "my-novel/scene-2", Entry: "canon-2", Character: "canon-1", ID: "ev-2"})
	Confirm(root, w, "ev-1", "作者确认守灯人得知", time.Date(2026, 6, 20, 12, 0, 0, 0, time.UTC))
	Confirm(root, w, "ev-r", "作者确认读者得知", time.Date(2026, 6, 20, 12, 0, 0, 0, time.UTC))
	scene1 := unitByRef(t, w, "my-novel/scene-1")
	scene2 := unitByRef(t, w, "my-novel/scene-2")
	scene3 := unitByRef(t, w, "my-novel/scene-3")

	// 场景开始时不含自身发生的获知；proposed 不参与。
	items, ps := KnownAt(root, w, scene1, "canon-1")
	if len(ps) > 0 {
		t.Fatal(ps)
	}
	if len(items) != 0 {
		t.Fatalf("scene-1 start knowledge = %+v", items)
	}
	items, ps = KnownAt(root, w, scene2, "canon-1")
	if len(ps) > 0 {
		t.Fatal(ps)
	}
	if len(items) != 1 || items[0].Entry != "canon-2" || items[0].Event != "ev-1" || items[0].Scene != "my-novel/scene-1" {
		t.Fatalf("scene-2 start knowledge = %+v", items)
	}
	if items[0].Name != nil {
		t.Fatalf("world-rule entry has no name, got %v", *items[0].Name)
	}

	// 重复登记不改变结果：earliest wins——scene-2 上 canon-1 再次获知 canon-2（confirmed）。
	Confirm(root, w, "ev-2", "作者确认再次获知", time.Date(2026, 6, 20, 12, 0, 0, 0, time.UTC))
	items, _ = KnownAt(root, w, scene3, "canon-1")
	if len(items) != 1 || items[0].Event != "ev-1" || items[0].Scene != "my-novel/scene-1" {
		t.Fatalf("earliest event should win, got %+v", items)
	}

	// 确定性：同一输入两次运行输出完全一致。
	again, _ := KnownAt(root, w, scene3, "canon-1")
	if itemsJSON(t, items) != itemsJSON(t, again) {
		t.Fatalf("knowledge not deterministic: %s vs %s", itemsJSON(t, items), itemsJSON(t, again))
	}

	// reader 与人物互不串扰。
	readerItems, ps := KnownAt(root, w, scene3, Reader)
	if len(ps) > 0 {
		t.Fatal(ps)
	}
	if len(readerItems) != 1 || readerItems[0].Event != "ev-r" {
		t.Fatalf("reader knowledge = %+v", readerItems)
	}
	// name 来自设定集：canon-1（人物）获知 canon-1 自身（人物条目）时 name 为守灯人。
	Propose(root, w, ProposeInput{Scene: "my-novel/scene-1", Entry: "canon-1", Character: "canon-1", ID: "ev-self"})
	Confirm(root, w, "ev-self", "作者确认自知", time.Date(2026, 6, 20, 12, 0, 0, 0, time.UTC))
	items, _ = KnownAt(root, w, scene3, "canon-1")
	if len(items) != 2 || items[0].Entry != "canon-1" || items[0].Name == nil || *items[0].Name != "守灯人" {
		t.Fatalf("items with name = %+v", items)
	}

	// 修改早期条目并重新确认后，已知列表随之变化：ev-1 的获知者改为 reader，canon-2 回落到 ev-2，
	// canon-1 自身（ev-self）不受影响。
	if _, _, ps := Set(root, w, "ev-1", SetInput{Character: ptr(Reader), ConfirmedByAuthor: true, Confirmation: "作者改判为读者获知"}, time.Date(2026, 6, 20, 12, 0, 0, 0, time.UTC)); len(ps) > 0 {
		t.Fatal(ps)
	}
	items, _ = KnownAt(root, w, scene3, "canon-1")
	if len(items) != 2 || items[0].Entry != "canon-1" || items[0].Event != "ev-self" || items[1].Entry != "canon-2" || items[1].Event != "ev-2" {
		t.Fatalf("after re-assigning ev-1 to reader, canon-1 should know canon-1 via ev-self and canon-2 via ev-2, got %+v", items)
	}
	// 撤销早期条目后，已知列表不再含该条目带来的信息。
	if _, _, ps := Reject(root, w, "ev-r", "作者撤销读者获知", time.Date(2026, 6, 20, 12, 0, 0, 0, time.UTC)); len(ps) > 0 {
		t.Fatal(ps)
	}
	readerItems, _ = KnownAt(root, w, scene3, Reader)
	if len(readerItems) != 1 || readerItems[0].Event != "ev-1" {
		t.Fatalf("reader should fall back to re-assigned ev-1, got %+v", readerItems)
	}
}

func TestKnownAtCrossChapterOrder(t *testing.T) {
	// 章节点在先序中先于其子场景，重放只按场景先序比较（lesson：不能直接比较章与场景的位置）。
	root, w := newNovel(t)
	Propose(root, w, ProposeInput{Scene: "my-novel/scene-3", Entry: "canon-2", Character: "canon-1", ID: "ev-late"})
	Propose(root, w, ProposeInput{Scene: "my-novel/scene-1", Entry: "canon-2", Character: "canon-1", ID: "ev-early"})
	Confirm(root, w, "ev-late", "作者确认", time.Date(2026, 6, 20, 12, 0, 0, 0, time.UTC))
	Confirm(root, w, "ev-early", "作者确认", time.Date(2026, 6, 20, 12, 0, 0, 0, time.UTC))
	items, ps := KnownAt(root, w, unitByRef(t, w, "my-novel/scene-3"), "canon-1")
	if len(ps) > 0 {
		t.Fatal(ps)
	}
	if len(items) != 1 || items[0].Event != "ev-early" {
		t.Fatalf("cross-chapter replay = %+v", items)
	}
}

func TestKnownAtValidationProblems(t *testing.T) {
	root, w := newNovel(t)
	scene3 := unitByRef(t, w, "my-novel/scene-3")
	// 没有台账时为空。
	if items, ps := KnownAt(root, w, scene3, "canon-1"); len(items) != 0 || len(ps) > 0 {
		t.Fatalf("empty ledger knowledge = %v, ps = %v", items, ps)
	}
	// 获知者参数不合法。
	if _, ps := KnownAt(root, w, scene3, "canon-2"); !has(ps, CodeUnknownCharacter, "character") {
		t.Fatalf("want unknown-character, got %v", ps)
	}
	if _, ps := KnownAt(root, w, scene3, "canon-9"); !has(ps, CodeUnknownCharacter, "character") {
		t.Fatalf("want unknown-character, got %v", ps)
	}
	// 悬空 entry / scene / character 引用、确认记录不完整时报告并返回 nil。
	rel := Path("my-novel")
	os.MkdirAll(filepath.Dir(filepath.Join(root, filepath.FromSlash(rel))), 0o755)
	os.WriteFile(filepath.Join(root, filepath.FromSlash(rel)), []byte(`schema_version: 1
events:
  - id: know-1
    scene: my-novel/scene-1
    entry: canon-9
    character: canon-1
    status: confirmed
    confirmation: 作者确认
    decided_at: 2026-10-03T08:00:00Z
  - id: know-2
    scene: my-novel/scene-9
    entry: canon-1
    character: canon-1
    status: confirmed
    confirmation: 作者确认
    decided_at: 2026-10-03T08:00:00Z
  - id: know-3
    scene: my-novel/scene-1
    entry: canon-1
    character: canon-2
    status: confirmed
    confirmation: 作者确认
    decided_at: 2026-10-03T08:00:00Z
  - id: know-4
    scene: my-novel/scene-1
    entry: canon-1
    character: canon-1
    status: confirmed
`), 0o644)
	items, ps := KnownAt(root, w, scene3, "canon-1")
	if items != nil {
		t.Fatalf("knowledge should be nil with problems, got %+v", items)
	}
	if !has(ps, CodeUnknownEntry, "events[0].entry") || !has(ps, CodeUnknownUnit, "events[1].scene") || !has(ps, CodeUnknownCharacter, "events[2].character") || !has(ps, CodeIncompleteConfirmation, "events[3]") {
		t.Fatalf("problems = %v", ps)
	}
	// 设定集损坏时报告其问题。
	os.WriteFile(filepath.Join(root, ".writing-anything", "works", "my-novel", "canon.yaml"), []byte("schema_version: 1\nentries: 3\n"), 0o644)
	if _, ps := KnownAt(root, w, scene3, "canon-1"); len(ps) == 0 {
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
  - id: know-1
    scene: my-novel/scene-1
    entry: canon-2
    character: canon-1
    status: confirmed
    confirmation: null
    decided_at: 2026-10-03T08:00:00Z
`,
		"confirmed-missing-confirmation": `schema_version: 1
events:
  - id: know-1
    scene: my-novel/scene-1
    entry: canon-2
    character: canon-1
    status: confirmed
    decided_at: 2026-10-03T08:00:00Z
`,
		"rejected-missing-decided-at": `schema_version: 1
events:
  - id: know-1
    scene: my-novel/scene-1
    entry: canon-2
    character: canon-1
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
  - id: know-1
    scene: my-novel/scene-1
    entry: canon-2
    character: canon-1
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
  - id: know-1
    scene: my-novel/scene-1
    entry: canon-2
    character: canon-1
    status: proposed
  - id: know-1
    scene: my-novel/chapter-one
    entry: canon-2
    character: canon-1
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
	if err := os.Symlink(outside, filepath.Join(root2, ".writing-anything", "works", "my-novel", "knowledge.yaml")); err != nil {
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
  # 守灯人在灯室对峙后得知灯油规则
  - id: hand-made
    scene: my-novel/scene-9 # 绑错了场景，悬空引用
    entry: canon-2
    character: canon-1
    status: proposed
`), 0o644)
	_, changed, warns := Propose(root, w, ProposeInput{Scene: "my-novel/scene-1", Entry: "canon-2", Character: "canon-1"})
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
	if !strings.Contains(string(data), "# 守灯人在灯室对峙后得知灯油规则") {
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
	if _, _, ps := Propose(root, w, ProposeInput{Scene: "my-novel/scene-1", Entry: "canon-2", Character: "canon-1"}); !has(ps, CodeAnchoredValue, "events") {
		t.Fatalf("anchored events should be rejected, got %v", ps)
	}
}

// 目标条目经由锚点得到时 confirm / reject / set 报告 knowledge.anchored-value（条目级分支，不同于 events 序列级）。
func TestAnchoredEntryRejectedByDecideAndSet(t *testing.T) {
	root, w := newNovel(t)
	rel := Path("my-novel")
	os.MkdirAll(filepath.Dir(filepath.Join(root, filepath.FromSlash(rel))), 0o755)
	os.WriteFile(filepath.Join(root, filepath.FromSlash(rel)), []byte(`schema_version: 1
events:
  - &entry
    id: know-1
    scene: my-novel/scene-1
    entry: canon-2
    character: canon-1
    status: proposed
  - *entry
`), 0o644)
	// know-1 出现两次（重复 id 由整体校验报告）；IndexByID 命中的目标条目带锚点时拒绝修改。
	for _, call := range []struct {
		name string
		run  func() []workspace.Problem
	}{
		{"confirm", func() []workspace.Problem { _, _, ps := Confirm(root, w, "know-1", "作者确认", time.Date(2026, 6, 20, 12, 0, 0, 0, time.UTC)); return ps }},
		{"reject", func() []workspace.Problem { _, _, ps := Reject(root, w, "know-1", "作者确认", time.Date(2026, 6, 20, 12, 0, 0, 0, time.UTC)); return ps }},
		{"set", func() []workspace.Problem { _, _, ps := Set(root, w, "know-1", SetInput{Character: ptr(Reader)}, time.Date(2026, 6, 20, 12, 0, 0, 0, time.UTC)); return ps }},
	} {
		if ps := call.run(); !has(ps, CodeAnchoredValue, "events[0]") {
			t.Fatalf("%s: want knowledge.anchored-value on the target entry, got %v", call.name, ps)
		}
	}
}

// 揭示词（Reveal Terms）：propose 写入、set 整体替换与删除、重复拒绝、已确认条目须重新确认、
// 读取时形态校验。
func TestTermsLifecycle(t *testing.T) {
	root, w := newNovel(t)
	T := time.Date(2026, 6, 20, 12, 0, 0, 0, time.UTC)

	// propose 携带揭示词。
	e, _, ps := Propose(root, w, ProposeInput{Scene: "my-novel/scene-1", Entry: "canon-2", Character: Reader, ID: "kn-t", Terms: []string{"为了救人", "救了潮生"}})
	if len(ps) > 0 {
		t.Fatal(ps)
	}
	if len(e.Terms) != 2 || e.Terms[0] != "为了救人" {
		t.Fatalf("terms = %v", e.Terms)
	}
	if _, _, ps := Confirm(root, w, "kn-t", "作者确认", T); len(ps) > 0 {
		t.Fatal(ps)
	}

	// 重复揭示词拒绝且不写文件。
	if _, _, ps := Propose(root, w, ProposeInput{Scene: "my-novel/scene-1", Entry: "canon-2", Character: Reader, Terms: []string{"为了救人", "为了救人"}}); !has(ps, "artifact.schema-violation", "terms[1]") {
		t.Fatalf("want schema-violation on duplicate term, got %v", ps)
	}

	// 修改已确认条目的揭示词须作者重新确认。
	terms := []string{"出生的时刻"}
	if _, _, ps := Set(root, w, "kn-t", SetInput{Terms: &terms}, T); !has(ps, CodeIncompleteConfirmation, "confirmation") {
		t.Fatalf("want incomplete-confirmation, got %v", ps)
	}
	e, changed, ps := Set(root, w, "kn-t", SetInput{Terms: &terms, ConfirmedByAuthor: true, Confirmation: "作者：更新揭示词"}, T)
	if len(ps) > 0 {
		t.Fatal(ps)
	}
	if len(e.Terms) != 1 || e.Terms[0] != "出生的时刻" || e.Status != StatusConfirmed {
		t.Fatalf("set terms event = %+v", e)
	}
	if len(changed) != 1 {
		t.Fatalf("changed = %v", changed)
	}

	// 空值删除该字段（恢复缺省空数组），文件里不再有 terms 键。
	empty := []string{}
	if _, changed, ps := Set(root, w, "kn-t", SetInput{Terms: &empty, ConfirmedByAuthor: true, Confirmation: "作者：清空揭示词"}, T); len(ps) > 0 {
		t.Fatal(ps)
	} else if len(changed) != 1 {
		t.Fatalf("changed = %v", changed)
	}
	data, err := os.ReadFile(filepath.Join(root, ".writing-anything", "works", "my-novel", "knowledge.yaml"))
	if err != nil {
		t.Fatal(err)
	}
	if strings.Contains(string(data), "terms:") {
		t.Fatalf("清空后不应残留 terms 键：\n%s", data)
	}

	// 读取时形态校验：空白揭示词在 List 时报 schema-violation。
	writeAtKnowledge(t, root, "schema_version: 1\nevents:\n  - id: kn-bad\n    scene: my-novel/scene-1\n    entry: canon-2\n    character: reader\n    terms: [\"  \"]\n    status: proposed\n")
	if _, ps := List(root, w, true); !has(ps, "artifact.schema-violation", "events[0].terms[0]") {
		t.Fatalf("want schema-violation on blank term, got %v", ps)
	}
}

func writeAtKnowledge(t *testing.T, root, content string) {
	t.Helper()
	path := filepath.Join(root, ".writing-anything", "works", "my-novel", "knowledge.yaml")
	if err := os.WriteFile(path, []byte(content), 0o644); err != nil {
		t.Fatal(err)
	}
}

// 缺揭示词提示（knowledge.missing-terms）：已确认事件所在条目没有任何已确认事件登记揭示词时以 warning 报告；同条目
// 已有揭示词、proposed 与 rejected 事件不提示；不影响重放与列表（不进入阻塞判断）。
func TestMissingTerms(t *testing.T) {
	root, w := newNovel(t)
	rel := Path("my-novel")
	writeAtKnowledge(t, root, `schema_version: 1
events:
  - {id: kn-1, scene: my-novel/scene-1, entry: canon-2, character: canon-1, status: confirmed, confirmation: 作者确认, decided_at: 2026-10-03T08:00:00Z}
  - {id: kn-2, scene: my-novel/scene-1, entry: canon-1, character: reader, status: proposed}
  - {id: kn-3, scene: my-novel/scene-2, entry: canon-1, character: reader, status: rejected, confirmation: 作者否定, decided_at: 2026-10-03T08:00:00Z}
`)
	checked, ps := CheckWorks(root, []*work.Work{w})
	if len(checked) != 1 || len(ps) != 1 || ps[0].Code != CodeMissingTerms || ps[0].Severity != workspace.SeverityWarning ||
		ps[0].Path != rel || ps[0].Field != "events[0]" || !strings.Contains(ps[0].Message, "wa-cli knowledge set my-novel kn-1 --terms") {
		t.Fatalf("missing terms: %+v", ps)
	}
	if ps := CheckArtifact(root, rel); !has(ps, CodeMissingTerms, "events[0]") {
		t.Fatalf("artifact: %+v", ps)
	}
	// 重放与列表不受影响。
	if items, ps := KnownAt(root, w, unitByRef(t, w, "my-novel/scene-2"), "canon-1"); len(ps) != 0 || len(items) != 1 {
		t.Fatalf("known at: %+v %+v", items, ps)
	}
	if _, ps := List(root, w, true); len(ps) != 0 {
		t.Fatalf("list: %+v", ps)
	}

	// 同条目另一条已确认事件登记了揭示词：两条都不提示。
	writeAtKnowledge(t, root, `schema_version: 1
events:
  - {id: kn-1, scene: my-novel/scene-1, entry: canon-2, character: canon-1, status: confirmed, confirmation: 作者确认, decided_at: 2026-10-03T08:00:00Z}
  - {id: kn-2, scene: my-novel/scene-2, entry: canon-2, character: reader, terms: [黎明], status: confirmed, confirmation: 作者确认, decided_at: 2026-10-03T08:00:00Z}
`)
	if _, ps := CheckWorks(root, []*work.Work{w}); len(ps) != 0 {
		t.Fatalf("covered entry: %+v", ps)
	}
	// 揭示词只在 proposed 事件上：不算登记。
	writeAtKnowledge(t, root, `schema_version: 1
events:
  - {id: kn-1, scene: my-novel/scene-1, entry: canon-2, character: canon-1, status: confirmed, confirmation: 作者确认, decided_at: 2026-10-03T08:00:00Z}
  - {id: kn-2, scene: my-novel/scene-2, entry: canon-2, character: reader, terms: [黎明], status: proposed}
`)
	if _, ps := CheckWorks(root, []*work.Work{w}); !has(ps, CodeMissingTerms, "events[0]") {
		t.Fatalf("proposed terms: %+v", ps)
	}
}

// Hidden：获知者在单元开始时未获知、且登记了揭示词的条目；揭示词取全部已确认事件（不论场景先后）的并集。
func TestReplayHidden(t *testing.T) {
	root, w := newNovel(t)
	writeAtKnowledge(t, root, `schema_version: 1
events:
  - {id: kn-1, scene: my-novel/scene-1, entry: canon-2, character: canon-1, terms: [黎明], status: confirmed, confirmation: 作者确认, decided_at: 2026-10-03T08:00:00Z}
  - {id: kn-2, scene: my-novel/scene-3, entry: canon-2, character: reader, terms: [燃烧, 黎明], status: confirmed, confirmation: 作者确认, decided_at: 2026-10-03T08:00:00Z}
  - {id: kn-3, scene: my-novel/scene-3, entry: canon-1, character: reader, terms: [旧伤], status: proposed}
`)
	rep, ps := For(root, w, unitByRef(t, w, "my-novel/scene-2"), nil)
	if ps != nil {
		t.Fatal(ps)
	}
	if h := rep.Hidden("canon-1"); len(h) != 0 {
		t.Fatalf("canon-1 knows canon-2: %v", h)
	}
	if h := rep.Hidden(Reader); !slices.Equal(h["canon-2"], []string{"燃烧", "黎明"}) || len(h) != 1 {
		t.Fatalf("reader hidden: %v", h)
	}
	if h := rep.Hidden(""); len(h) != 1 {
		t.Fatalf("no character: %v", h)
	}
	// 没有台账：没有隐藏条目。
	root2, w2 := newNovel(t)
	rep, _ = For(root2, w2, unitByRef(t, w2, "my-novel/scene-2"), nil)
	if h := rep.Hidden(""); len(h) != 0 {
		t.Fatalf("no ledger: %v", h)
	}
}
