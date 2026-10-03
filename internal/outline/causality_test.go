package outline

import (
	"slices"
	"strings"
	"testing"

)

// novelScenes 在 root 下写入三个场景（s1、s2、s3）的大纲并返回先序场景列表；content 为 nil 时跳过该场景。
func novelScenes(t *testing.T, root string, contents [3]string) []Scene {
	t.Helper()
	refs := []string{"my-novel/s1", "my-novel/s2", "my-novel/s3"}
	dirs := [3]string{}
	for i, id := range []string{"s1", "s2", "s3"} {
		dirs[i] = ".writing-anything/works/my-novel/units/" + id
		if contents[i] != "" {
			put(t, root, Path(dirs[i]), contents[i])
		}
	}
	return []Scene{{Ref: refs[0], Dir: dirs[0]}, {Ref: refs[1], Dir: dirs[1]}, {Ref: refs[2], Dir: dirs[2]}}
}

// 因果链（scene-causality）：链合法时没有问题；事件 id 重复、起因引用不存在或尚未发生的事件时报告对应错误码。
func TestCausality(t *testing.T) {
	root := t.TempDir()
	scenes := novelScenes(t, root, [3]string{
		"schema_version: 1\nsections:\n  - {id: sc1, title: 灯下, events: [ev-1]}\n",
		"schema_version: 1\nsections:\n  - {id: sc2, title: 夜行, causes: [ev-1], events: [ev-2]}\n",
		"schema_version: 1\nsections:\n  - {id: sc3, title: 破晓, causes: [ev-2]}\n",
	})
	if ps := Causality(root, scenes); ps != nil {
		t.Fatalf("valid chain: %+v", ps)
	}

	// 重复事件：s3 再次声明 ev-1，报告在 s3 的大纲上，message 含事件 id 与先声明的场景。
	root = t.TempDir()
	scenes = novelScenes(t, root, [3]string{
		"schema_version: 1\nsections:\n  - {id: sc1, title: 灯下, events: [ev-1]}\n",
		"",
		"schema_version: 1\nsections:\n  - {id: sc3, title: 破晓, events: [ev-1]}\n",
	})
	ps := Causality(root, scenes)
	if len(ps) != 1 || ps[0].Code != "scene.duplicate-event" || ps[0].Path != Path(scenes[2].Dir) || ps[0].Field != "sections[0].events[0]" {
		t.Fatalf("duplicate-event: %+v", ps)
	}
	if !strings.Contains(ps[0].Message, "ev-1") || !strings.Contains(ps[0].Message, "my-novel/s1") {
		t.Fatalf("duplicate-event message = %q", ps[0].Message)
	}
	// CausalityOn 只取报告在该单元大纲上的问题。
	if len(CausalityOn(root, scenes, scenes[0].Dir)) != 0 {
		t.Fatalf("CausalityOn s1 must be empty")
	}
	if got := CausalityOn(root, scenes, scenes[2].Dir); len(got) != 1 || got[0].Code != "scene.duplicate-event" {
		t.Fatalf("CausalityOn s3 = %+v", got)
	}

	// 起因引用不存在的事件：scene.unknown-event。
	root = t.TempDir()
	scenes = novelScenes(t, root, [3]string{
		"",
		"schema_version: 1\nsections:\n  - {id: sc2, title: 夜行, causes: [ev-9]}\n",
		"",
	})
	ps = Causality(root, scenes)
	if len(ps) != 1 || ps[0].Code != "scene.unknown-event" || ps[0].Path != Path(scenes[1].Dir) || ps[0].Field != "sections[0].causes[0]" {
		t.Fatalf("unknown-event: %+v", ps)
	}

	// 起因引用更靠后场景声明的事件：scene.event-not-yet-happened。
	root = t.TempDir()
	scenes = novelScenes(t, root, [3]string{
		"schema_version: 1\nsections:\n  - {id: sc1, title: 灯下, causes: [ev-2]}\n",
		"schema_version: 1\nsections:\n  - {id: sc2, title: 夜行, events: [ev-2]}\n",
		"",
	})
	ps = Causality(root, scenes)
	if len(ps) != 1 || ps[0].Code != "scene.event-not-yet-happened" || ps[0].Path != Path(scenes[0].Dir) || ps[0].Field != "sections[0].causes[0]" {
		t.Fatalf("not-yet-happened: %+v", ps)
	}
	if !strings.Contains(ps[0].Message, "ev-2") || !strings.Contains(ps[0].Message, "my-novel/s2") {
		t.Fatalf("not-yet-happened message = %q", ps[0].Message)
	}

	// 起因引用本场景自己声明的事件：同样视为尚未发生。
	root = t.TempDir()
	scenes = novelScenes(t, root, [3]string{
		"schema_version: 1\nsections:\n  - {id: sc1, title: 灯下, causes: [ev-1], events: [ev-1]}\n",
		"", "",
	})
	ps = Causality(root, scenes)
	if len(ps) != 1 || ps[0].Code != "scene.event-not-yet-happened" {
		t.Fatalf("self cause: %+v", ps)
	}

	// 大纲不通过 Schema 检查的场景不参与因果链：其事件视为未声明（其自身问题由整体校验报告）。
	root = t.TempDir()
	scenes = novelScenes(t, root, [3]string{
		"schema_version: 1\nsections:\n  - {id: sc1, title: 灯下, events: [ev-1], note: x}\n",
		"schema_version: 1\nsections:\n  - {id: sc2, title: 夜行, causes: [ev-1]}\n",
		"",
	})
	ps = Causality(root, scenes)
	if len(ps) != 1 || ps[0].Code != "scene.unknown-event" {
		t.Fatalf("broken scene outline: %+v", ps)
	}

	// 没有大纲文件的场景跳过；非场景单元（根单元）大纲节上的 causes 与 events 不参与因果链。
	root = t.TempDir()
	scenes = novelScenes(t, root, [3]string{"", "", ""})
	put(t, root, Path(".writing-anything/works/my-novel"), "schema_version: 1\nsections:\n  - {id: beat, title: 开端, causes: [ev-9], events: [ev-9]}\n")
	if ps := Causality(root, scenes); ps != nil {
		t.Fatalf("non-scene outline must not join causality: %+v", ps)
	}
}

// Outline 非 nil 的场景代替磁盘上的大纲：写前检查以写入后的状态运行。
func TestCausalityOverride(t *testing.T) {
	root := t.TempDir()
	scenes := novelScenes(t, root, [3]string{
		"schema_version: 1\nsections:\n  - {id: sc1, title: 灯下, events: [ev-1]}\n",
		"schema_version: 1\nsections:\n  - {id: sc2, title: 夜行, events: [ev-2]}\n",
		"",
	})
	// 写入后的 s1 以 ev-2 为起因：ev-2 由更靠后的 s2 声明，尚未发生。
	cand := &Outline{Sections: []Section{{ID: "sc1", Title: "灯下", Causes: []string{"ev-2"}}}}
	scenes[0].Outline = cand
	ps := Causality(root, scenes)
	if len(ps) != 1 || ps[0].Code != "scene.event-not-yet-happened" || ps[0].Path != Path(scenes[0].Dir) {
		t.Fatalf("override: %+v", ps)
	}
}

// Candidate 把修改应用到已有大纲的值上，不写文件：字符串与列表整体替换、状态变化按 id 新增或更新。
func TestEditCandidate(t *testing.T) {
	existing := &Outline{Sections: []Section{{
		ID: "sc1", Title: "灯下对峙", Pov: "守灯人", Characters: []string{"守灯人"},
		StateChanges: []StateChange{{ID: "sc-1", Text: "精灵交出灯芯"}},
	}}}
	clear, plant := "", []string{"fs-1"}
	e := Edit{ID: "sc1", Location: ptr("灯室"), Plants: &plant, Reveal: &clear,
		StateChanges: &[]StateChange{{ID: "sc-1", Text: "精灵交出灯芯并离开"}, {ID: "sc-2", Text: "守灯人决定远行"}}}
	got := e.Candidate(existing)
	s := got.Sections[0]
	if s.Title != "灯下对峙" || s.Pov != "守灯人" || s.Location != "灯室" || s.Reveal != "" ||
		!slices.Equal(s.Plants, []string{"fs-1"}) || !slices.Equal(s.Characters, []string{"守灯人"}) {
		t.Fatalf("candidate section = %+v", s)
	}
	if len(s.StateChanges) != 2 || s.StateChanges[0].Text != "精灵交出灯芯并离开" || s.StateChanges[1].ID != "sc-2" {
		t.Fatalf("candidate state changes = %+v", s.StateChanges)
	}
	if existing.Sections[0].Location != "" || existing.Sections[0].Plants != nil || len(existing.Sections[0].StateChanges) != 1 {
		t.Fatalf("existing outline mutated: %+v", existing.Sections[0])
	}
	// 新增节缺少 --title 时返回 nil。
	if got := (Edit{ID: "new"}).Candidate(existing); got != nil {
		t.Fatalf("new section without title = %+v", got)
	}
}

// state_changes 的 id 在节内唯一：id 相同而描述不同的条目由领域校验报告（Schema 的 uniqueItems 只拦截完全相同的条目）。
func TestStateChangeDuplicateID(t *testing.T) {
	root := project(t)
	put(t, root, rel, "schema_version: 1\nsections:\n  - {id: a, title: t, state_changes: [{id: sc-1, text: 甲}, {id: sc-1, text: 乙}]}\n")
	o, ps := Load(root, unitDir, messages(t, root))
	if o != nil || len(ps) != 1 || ps[0].Code != "artifact.schema-violation" || ps[0].Field != "sections[0].state_changes[1].id" {
		t.Fatalf("duplicate state change id: outline = %+v, problems = %+v", o, ps)
	}
}

// scene contract 字段的写前复查：参数引起的 Schema 问题不填 path，字段换成参数名（outline/set 的 save 约定）。
func TestSceneContractSetRejects(t *testing.T) {
	root := project(t)
	put(t, root, rel, "schema_version: 1\nsections:\n  - {id: a, title: t}\n")
	ms := messages(t, root)
	bad := []string{"Ev_2"}
	e := Edit{ID: "a", Events: &bad}
	if _, _, ps := Set(root, unitDir, e, ms, noEnsure); len(ps) != 1 || ps[0].Code != "artifact.schema-violation" ||
		ps[0].Path != "" || ps[0].Field != "events[0]" {
		t.Fatalf("bad event id: %+v", ps)
	}
	empty := []string{""}
	e = Edit{ID: "a", Plants: &empty}
	if _, _, ps := Set(root, unitDir, e, ms, noEnsure); len(ps) != 1 || ps[0].Code != "artifact.schema-violation" || ps[0].Field != "plants[0]" {
		t.Fatalf("bad plant id: %+v", ps)
	}
}

// Set 写入长篇场景契约扩展字段并保留其他内容。
func TestSceneContractSet(t *testing.T) {
	root := project(t)
	put(t, root, rel, "schema_version: 1\nsections:\n  - {id: a, title: t} # 注释\n")
	ms := messages(t, root)
	pov, location := "守灯人", "灯室"
	list := []string{"ev-2"}
	sc := []StateChange{{ID: "sc-1", Text: "精灵交出灯芯"}}
	e := Edit{ID: "a", Pov: &pov, Location: &location, Events: &list, StateChanges: &sc}
	o, changed, ps := Set(root, unitDir, e, ms, noEnsure)
	if o == nil || ps != nil {
		t.Fatalf("Set = %+v, %+v", o, ps)
	}
	src := read(t, root, rel)
	for _, want := range []string{"pov: 守灯人", "location: 灯室", "events: [ev-2]", "id: sc-1", "text: 精灵交出灯芯", "# 注释"} {
		if !strings.Contains(src, want) {
			t.Fatalf("outline.yaml missing %q:\n%s", want, src)
		}
	}
	if !slices.Equal(changed, []string{rel}) {
		t.Fatalf("changed = %v", changed)
	}
	// 取值未变时不写文件。
	if _, changed, _ := Set(root, unitDir, e, ms, noEnsure); changed != nil {
		t.Fatalf("unchanged write: %v", changed)
	}
}
