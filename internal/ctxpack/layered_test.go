package ctxpack

import (
	"crypto/sha256"
	"encoding/hex"
	"os"
	"path/filepath"
	"strings"
	"testing"

	"github.com/kw12121212/writing-anything/internal/work"
	"github.com/kw12121212/writing-anything/internal/workspace"
)

// novelProject 返回至少三章的长篇工作区：part-one 下 chapter-one{scene-1,scene-2} 与 chapter-two{scene-3}，
// part-two 下 chapter-three{scene-4}。各场景有正文与摘要，章与部有分层摘要；canon、state、knowledge、threads 齐。
func novelProject(t *testing.T) string {
	t.Helper()
	root := t.TempDir()
	if _, ps := workspace.Init(root, nil); len(ps) > 0 {
		t.Fatal(ps)
	}
	if _, _, ps := work.Create(root, "novel", "my-novel"); len(ps) > 0 {
		t.Fatal(ps)
	}
	for _, add := range []struct{ parent, typ, id string }{
		{"my-novel", "part", "part-one"},
		{"my-novel/part-one", "chapter", "chapter-one"},
		{"my-novel/chapter-one", "scene", "scene-1"},
		{"my-novel/chapter-one", "scene", "scene-2"},
		{"my-novel/part-one", "chapter", "chapter-two"},
		{"my-novel/chapter-two", "scene", "scene-3"},
		{"my-novel", "part", "part-two"},
		{"my-novel/part-two", "chapter", "chapter-three"},
		{"my-novel/chapter-three", "scene", "scene-4"},
	} {
		if _, _, ps := work.AddUnit(root, add.parent, add.typ, add.id, work.Anchor{}); len(ps) > 0 {
			t.Fatal(ps)
		}
	}
	dir := work.Dir("my-novel")
	put(t, root, dir+"/canon.yaml", "schema_version: 1\nentries:\n  - {id: canon-1, type: character, name: 守灯人, text: 五十岁，左手有旧伤, sources: [], protection: normal}\n")
	put(t, root, dir+"/state.yaml", "schema_version: 1\nevents:\n  - {id: state-1, scene: my-novel/scene-1, entry: canon-1, state: 受伤, status: confirmed, confirmation: ok, decided_at: \"2026-10-04T08:00:00Z\"}\n")
	put(t, root, dir+"/knowledge.yaml", "schema_version: 1\nevents:\n  - {id: know-1, scene: my-novel/scene-1, entry: canon-1, character: reader, status: confirmed, confirmation: ok, decided_at: \"2026-10-04T08:00:00Z\"}\n")
	put(t, root, dir+"/threads.yaml", "schema_version: 1\nentries:\n  - {id: f-1, text: 灯芯的来历, planted_in: my-novel/scene-1, planned_recover: my-novel/scene-4, status: open}\n")
	for i, s := range []string{"scene-1", "scene-2", "scene-3", "scene-4"} {
		draft := strings.Repeat("第"+strings.Repeat("很", 30)+"长的正文段落。", 3)
		put(t, root, dir+"/units/"+s+"/draft.md", draft)
		sha := shaHex(draft)
		put(t, root, dir+"/units/"+s+"/summary.yaml", "schema_version: 1\ntext: 场景"+s+"的情节摘要，交代推进与人物变化，保持足够长度以计入预算。\ndraft_sha256: \""+sha+"\"\nrecorded_at: \"2026-10-04T08:00:00Z\"\n")
		if i < 3 {
			// 前三个场景属于 part-one；scene-4 属于 part-two。
		}
	}
	for _, s := range []string{"scene-2", "scene-3", "scene-4"} {
		chars := "守灯人"
		if s == "scene-4" {
			chars = "守灯人, 精灵"
		}
		put(t, root, dir+"/units/"+s+"/outline.yaml",
			"schema_version: 1\nsections:\n  - id: sc1\n    title: 场景\n    pov: 守灯人\n    characters: ["+chars+"]\n    location: 灯室\n    events: [ev-x]\n")
	}
	// 分层摘要：章、部与全书（指纹与 stale 在此测试中不敏感：context 只读 text 与 stale）。
	put(t, root, dir+"/units/chapter-one/summary.yaml", "schema_version: 1\ntext: 第一章摘要：守灯人接下委托并与精灵对峙，灯芯易主。\ndraft_sha256: \""+strings.Repeat("1", 64)+"\"\nrecorded_at: \"2026-10-04T08:00:00Z\"\n")
	put(t, root, dir+"/units/chapter-two/summary.yaml", "schema_version: 1\ntext: 第二章摘要：风暴逼近，守灯人追查灯芯的下落。\ndraft_sha256: \""+strings.Repeat("1", 64)+"\"\nrecorded_at: \"2026-10-04T08:00:00Z\"\n")
	put(t, root, dir+"/units/chapter-three/summary.yaml", "schema_version: 1\ntext: 第三章摘要：真相在灯室揭开，伏笔进入回收位置。\ndraft_sha256: \""+strings.Repeat("1", 64)+"\"\nrecorded_at: \"2026-10-04T08:00:00Z\"\n")
	put(t, root, dir+"/units/part-one/summary.yaml", "schema_version: 1\ntext: 第一部摘要：委托、对峙与追查，守灯人的世界被灯芯改写。\ndraft_sha256: \""+strings.Repeat("2", 64)+"\"\nrecorded_at: \"2026-10-04T08:00:00Z\"\n")
	put(t, root, dir+"/units/part-two/summary.yaml", "schema_version: 1\ntext: 第二部摘要：回收与揭晓。\ndraft_sha256: \""+strings.Repeat("2", 64)+"\"\nrecorded_at: \"2026-10-04T08:00:00Z\"\n")
	put(t, root, dir+"/summary.yaml", "schema_version: 1\ntext: 全书摘要：守灯人与精灵围绕灯芯的归属完成各自的抉择。\ndraft_sha256: \""+strings.Repeat("3", 64)+"\"\nrecorded_at: \"2026-10-04T08:00:00Z\"\n")
	return root
}

func shaHex(s string) string {
	sum := sha256.Sum256([]byte(s))
	return hex.EncodeToString(sum[:])
}

// 里程碑完成标准：至少三章的长篇中，任一场景的 context 不超过预算，同一输入两次输出完全一致。
func TestSceneBudgetAndDeterminism(t *testing.T) {
	root := novelProject(t)
	for _, ref := range []string{"my-novel/scene-2", "my-novel/scene-3", "my-novel/scene-4"} {
		w, u, ps := work.Resolve(root, ref)
		if len(ps) > 0 {
			t.Fatal(ps)
		}
		p, ps := Build(root, w, u)
		if p == nil {
			t.Fatalf("%s: %+v", ref, ps)
		}
		budget, _ := p.Budget.Value.(int)
		if p.Used > budget {
			t.Fatalf("%s: used %d exceeds budget %d", ref, p.Used, budget)
		}
		for _, tr := range p.Trimmed {
			if tr.KeptChars != 0 && tr.Part != PartSource && tr.Part != PartPriorEnding {
				t.Fatalf("%s: unexpected partial trim %+v", ref, tr)
			}
		}
		// 同一输入两次组装完全一致（含裁剪结果）。
		p2, _ := Build(root, w, u)
		if p.Used != p2.Used || len(p.Trimmed) != len(p2.Trimmed) {
			t.Fatalf("%s: nondeterministic build", ref)
		}
	}
}

// scene 单元的新内容项：states、priorEndings 与分层摘要。
func TestSceneLayeredItems(t *testing.T) {
	root := novelProject(t)
	w, u, ps := work.Resolve(root, "my-novel/scene-4")
	if len(ps) > 0 {
		t.Fatal(ps)
	}
	p, ps := Build(root, w, u)
	if p == nil {
		t.Fatalf("%+v", ps)
	}
	// states：canon-1 的 confirmed 状态（state-1 绑定 scene-1，先序早于 scene-4）。
	if len(p.States) != 1 || p.States[0].Entry != "canon-1" || p.States[0].State != "受伤" {
		t.Fatalf("states = %+v", p.States)
	}
	// priorEndings：最近两个场景（scene-3、scene-2），最近在前，各至多 400 字符。
	if len(p.PriorEndings) != 2 || p.PriorEndings[0].Unit != "my-novel/scene-3" || p.PriorEndings[1].Unit != "my-novel/scene-2" {
		t.Fatalf("priorEndings = %+v", p.PriorEndings)
	}
	for _, e := range p.PriorEndings {
		if len([]rune(e.Excerpt)) > PriorEndingChars {
			t.Fatalf("prior ending too long: %d", len([]rune(e.Excerpt)))
		}
	}
	// 分层摘要：所属章（chapter-three）、部（part-two）与全书。
	if len(p.Summaries) != 3 || p.Summaries[0].Level != "chapter" || p.Summaries[1].Level != "part" || p.Summaries[2].Level != "book" {
		t.Fatalf("summaries = %+v", p.Summaries)
	}
}

// scene 优先级：契约（大纲）最高，被裁剪项按场景优先级顺序出现。
func TestScenePriorityOrder(t *testing.T) {
	root := novelProject(t)
	w, u, _ := work.Resolve(root, "my-novel/scene-4")
	// 把预算压到只够契约与部分状态：通过极小的 context_budget（项目配置覆盖）。
	put(t, root, ".writing-anything/config.yaml", "schema_version: 1\nlanguage: zh-CN\ndefault_genre: novel\ngate_level: standard\npacks: []\ncontext_budget: 300\n")
	p, ps := Build(root, w, u)
	if p == nil {
		t.Fatalf("%+v", ps)
	}
	if p.Used > 300 {
		t.Fatalf("used = %d", p.Used)
	}
	if p.Outline == nil && len(p.Trimmed) > 0 {
		t.Fatalf("outline should survive first: trimmed = %+v", p.Trimmed)
	}
	// 高优先级（state）先于低优先级（knowledge）被保留或至少不晚于其被裁剪。
	var stateKept, knowKept bool
	for _, it := range p.items {
		if it.part == PartState {
			stateKept = true
		}
		if it.part == PartKnowledge {
			knowKept = true
		}
	}
	if !stateKept && knowKept {
		t.Fatal("state trimmed while knowledge kept：优先级顺序错误")
	}
}

// 审查回归：无 pov 时 states 仍含 characters 解析的条目；priorEndings 跳过没有正文的场景。
func TestSceneStatesWithoutPOVAndDraftSkipping(t *testing.T) {
	root := novelProject(t)
	dir := work.Dir("my-novel")
	// scene-3：去掉 pov、保留 characters；scene-2 删除正文 → priorEndings 只取 scene-1（scene-2 无 draft）与
	// scene-1 之外更早的场景（scene-1 之前没有）→ scene-4 的最近两个有正文的场景为 scene-3 与 scene-1。
	put(t, root, dir+"/units/scene-3/outline.yaml",
		"schema_version: 1\nsections:\n  - id: sc1\n    title: 场景\n    characters: [守灯人]\n    location: 灯室\n")
	os.Remove(filepath.Join(root, filepath.FromSlash(dir+"/units/scene-2/draft.md")))
	w, u, ps := work.Resolve(root, "my-novel/scene-3")
	if len(ps) > 0 {
		t.Fatal(ps)
	}
	p, ps := Build(root, w, u)
	if p == nil {
		t.Fatalf("%+v", ps)
	}
	if len(p.States) != 1 || p.States[0].Entry != "canon-1" {
		t.Fatalf("states without pov = %+v, want canon-1（characters 解析）", p.States)
	}
	w4, u4, _ := work.Resolve(root, "my-novel/scene-4")
	p4, _ := Build(root, w4, u4)
	if len(p4.PriorEndings) != 2 || p4.PriorEndings[0].Unit != "my-novel/scene-3" || p4.PriorEndings[1].Unit != "my-novel/scene-1" {
		t.Fatalf("priorEndings = %+v, want [scene-3, scene-1]（scene-2 无正文被跳过）", p4.PriorEndings)
	}
}

// 非 scene 单元不输出场景级新内容项。
func TestNonSceneNoLayeredItems(t *testing.T) {
	root := novelProject(t)
	w, u, _ := work.Resolve(root, "my-novel/chapter-three")
	p, ps := Build(root, w, u)
	if p == nil {
		t.Fatal(ps)
	}
	if len(p.States) != 0 || len(p.PriorEndings) != 0 || len(p.Summaries) != 0 {
		t.Fatalf("non-scene pack has scene items: %+v", p)
	}
}
