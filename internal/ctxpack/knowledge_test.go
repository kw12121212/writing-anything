package ctxpack

import (
	"os"
	"path/filepath"
	"slices"
	"strings"
	"testing"

	"github.com/kw12121212/writing-anything/internal/work"
	"github.com/kw12121212/writing-anything/internal/workspace"
)

// knowledgeProject 在 storyProject 之上给 s2 的场景契约加 pov（守灯人），并写入 knowledge.yaml：
// s1 上守灯人（canon-1）与读者分别获知 canon-2（灯塔的秘密），另有只有读者获知的 canon-3。
func knowledgeProject(t *testing.T) string {
	t.Helper()
	root := storyProject(t)
	dir := work.Dir("my-story")
	put(t, root, dir+"/canon.yaml",
		"schema_version: 1\nentries:\n"+
			"  - {id: canon-1, type: character, name: 守灯人, text: 五十岁，左手有旧伤, sources: [], protection: normal}\n"+
			"  - {id: canon-2, type: world-rule, text: 灯油只能燃烧到黎明, sources: [], protection: normal}\n"+
			"  - {id: canon-3, type: world-rule, text: 精灵是灯芯所化, sources: [], protection: normal}\n")
	put(t, root, dir+"/units/s2/outline.yaml",
		"schema_version: 1\nsections:\n  - id: scene-2\n    title: 灯下对峙\n    characters: [守灯人]\n    pov: 守灯人\n    reveal: 精灵的真实身份\n")
	put(t, root, dir+"/knowledge.yaml", `schema_version: 1
events:
  - {id: know-1, scene: my-story/s1, entry: canon-2, character: canon-1, status: confirmed, confirmation: 作者确认, decided_at: 2026-10-03T08:00:00Z}
  - {id: know-2, scene: my-story/s1, entry: canon-2, character: reader, status: confirmed, confirmation: 作者确认, decided_at: 2026-10-03T08:00:00Z}
  - {id: know-3, scene: my-story/s1, entry: canon-3, character: reader, status: confirmed, confirmation: 作者确认, decided_at: 2026-10-03T08:00:00Z}
  - {id: know-4, scene: my-story/s2, entry: canon-3, character: canon-1, status: proposed}
`)
	return root
}

// 场景级知情（scene-context 的 knowledge 与 readerOnly）：视角人物已知、读者已知人物未知、揭示边界配合悬念；
// pov 未解析、无台账、台账不合法与非 scene 单元。
func TestSceneKnowledge(t *testing.T) {
	root := knowledgeProject(t)
	w, u, ps := work.Resolve(root, "my-story/s2")
	if ps != nil {
		t.Fatal(ps)
	}
	p, ps := Build(root, w, u)
	if p == nil {
		t.Fatal(ps)
	}
	// 守灯人在 s2 开始时已知 canon-2（含 text 与最早获知事件）；s2 自身的 proposed 条目不参与。
	if len(p.Knowledge) != 1 || p.Knowledge[0].Entry != "canon-2" || p.Knowledge[0].Event != "know-1" ||
		p.Knowledge[0].Scene != "my-story/s1" || p.Knowledge[0].Text != "灯油只能燃烧到黎明" {
		t.Fatalf("knowledge = %+v", p.Knowledge)
	}
	// 读者已知 canon-2 与 canon-3，去掉视角人物已知后只剩 canon-3。
	if len(p.ReaderOnly) != 1 || p.ReaderOnly[0].Entry != "canon-3" || p.ReaderOnly[0].Event != "know-3" {
		t.Fatalf("readerOnly = %+v", p.ReaderOnly)
	}
	md := p.Markdown()
	for _, want := range []string{"## 视角人物已知（POV Knowledge）", "## 读者已知、人物未知（Reader Only）", "灯油只能燃烧到黎明", "获知于 my-story/s1"} {
		if !strings.Contains(md, want) {
			t.Errorf("markdown lacks %q", want)
		}
	}

	// pov 取第一个声明 pov 的大纲节：两节中只有第二节声明时也解析到它。
	root = knowledgeProject(t)
	put(t, root, work.Dir("my-story")+"/units/s2/outline.yaml",
		"schema_version: 1\nsections:\n  - {id: lead, title: 引子}\n  - {id: scene-2, title: 灯下对峙, pov: 守灯人}\n")
	w, u, _ = work.Resolve(root, "my-story/s2")
	p, ps = Build(root, w, u)
	if p == nil || len(p.Knowledge) != 1 || len(p.ReaderOnly) != 1 {
		t.Fatalf("second-section pov: %+v %+v", p, ps)
	}

	// pov 未解析到 character 条目：warning（knowledge.pov-not-in-canon），两个字段为空数组。
	root = knowledgeProject(t)
	put(t, root, work.Dir("my-story")+"/units/s2/outline.yaml",
		"schema_version: 1\nsections:\n  - {id: scene-2, title: 灯下对峙, pov: 高人}\n")
	w, u, _ = work.Resolve(root, "my-story/s2")
	p, ps = Build(root, w, u)
	if p == nil {
		t.Fatal(ps)
	}
	if len(p.Knowledge) != 0 || len(p.ReaderOnly) != 0 || !slices.ContainsFunc(ps, func(q workspace.Problem) bool {
		return q.Code == CodePOVNotInCanon && q.Severity == workspace.SeverityWarning && strings.Contains(q.Message, "高人")
	}) {
		t.Fatalf("pov not in canon: %+v %+v", p, ps)
	}

	// 没有声明 pov：两个字段为空数组；台账仍被校验（不合法时阻止组装）。
	root = knowledgeProject(t)
	put(t, root, work.Dir("my-story")+"/units/s2/outline.yaml",
		"schema_version: 1\nsections:\n  - {id: scene-2, title: 灯下对峙, characters: [守灯人]}\n")
	w, u, _ = work.Resolve(root, "my-story/s2")
	p, ps = Build(root, w, u)
	if p == nil || len(p.Knowledge) != 0 || len(p.ReaderOnly) != 0 {
		t.Fatalf("no pov: %+v %+v", p, ps)
	}
	put(t, root, work.Dir("my-story")+"/knowledge.yaml", "k: v\n")
	if p, ps = Build(root, w, u); p != nil || len(ps) == 0 {
		t.Fatalf("broken ledger without pov: %+v %+v", p, ps)
	}

	// 作品没有 knowledge.yaml：两个字段为空数组且不报告知情诊断。
	root = knowledgeProject(t)
	os.Remove(filepath.Join(root, filepath.FromSlash(work.Dir("my-story")+"/knowledge.yaml")))
	w, u, _ = work.Resolve(root, "my-story/s2")
	p, ps = Build(root, w, u)
	if p == nil || len(p.Knowledge) != 0 || len(p.ReaderOnly) != 0 || slices.ContainsFunc(ps, func(q workspace.Problem) bool {
		return q.Code == CodePOVNotInCanon
	}) {
		t.Fatalf("no ledger: %+v %+v", p, ps)
	}

	// 台账不通过校验：报告其诊断，context 为 null（同 threads.yaml 的处理）。
	root = knowledgeProject(t)
	put(t, root, work.Dir("my-story")+"/knowledge.yaml", "schema_version: 1\nevents:\n  - {id: know-1, scene: my-story/s1, entry: canon-9, character: canon-1, status: proposed}\n")
	w, u, _ = work.Resolve(root, "my-story/s2")
	if p, ps = Build(root, w, u); p != nil || !slices.ContainsFunc(ps, func(q workspace.Problem) bool {
		return q.Code == "knowledge.unknown-entry" && q.Severity == workspace.SeverityError
	}) {
		t.Fatalf("broken ledger: %+v %+v", p, ps)
	}

	// 非 scene 单元（故事根单元）：两个字段为空且没有场景级诊断。
	root = knowledgeProject(t)
	w, rootUnit, _ := work.Resolve(root, "my-story")
	if p, ps = Build(root, w, rootUnit); p == nil || len(p.Knowledge) != 0 || len(p.ReaderOnly) != 0 || len(ps) != 0 {
		t.Fatalf("story unit: %+v %+v", p, ps)
	}
}

// 知情内容项计入预算并按优先级裁剪（context-trim）：位于 canon 人物设定项之后、揭示边界之前。
func TestSceneKnowledgeBudget(t *testing.T) {
	// 预算很小时知情项被整项裁剪并出现在 trimmed 中（part 为 knowledge 与 reader-only，id 为设定条目 id）。
	root := knowledgeProject(t)
	put(t, root, workspace.ConfigPath, "schema_version: 1\nlanguage: zh-CN\ngate_level: standard\npacks: []\ncontext_budget: 10\n")
	w, u, _ := work.Resolve(root, "my-story/s2")
	p, ps := Build(root, w, u)
	if p == nil {
		t.Fatal(ps)
	}
	for _, part := range []string{PartKnowledge, PartReaderOnly} {
		var tr *Trim
		for i := range p.Trimmed {
			if p.Trimmed[i].Part == part {
				tr = &p.Trimmed[i]
			}
		}
		if tr == nil || tr.KeptChars != 0 {
			t.Fatalf("part %s not trimmed: %+v", part, p.Trimmed)
		}
		if part == PartKnowledge && tr.ID != "canon-2" {
			t.Fatalf("trimmed knowledge entry id = %q, want canon-2", tr.ID)
		}
	}
	if len(p.Knowledge) != 0 || len(p.ReaderOnly) != 0 {
		t.Fatalf("fields = %+v %+v", p.Knowledge, p.ReaderOnly)
	}

	// 优先级：预算只放得下人物设定与一项知情时，人物设定与视角人物已知保留，读者已知与揭示边界被裁剪。
	root = knowledgeProject(t)
	put(t, root, workspace.ConfigPath, "schema_version: 1\nlanguage: zh-CN\ngate_level: standard\npacks: []\ncontext_budget: 95\n")
	w, u, _ = work.Resolve(root, "my-story/s2")
	p, ps = Build(root, w, u)
	if p == nil {
		t.Fatal(ps)
	}
	if len(p.Characters) != 1 || len(p.Knowledge) != 1 || len(p.Reveal) != 0 {
		t.Fatalf("priority: characters = %+v, knowledge = %+v, reveal = %+v, trimmed = %+v", p.Characters, p.Knowledge, p.Reveal, p.Trimmed)
	}
	if !slices.ContainsFunc(p.Trimmed, func(x Trim) bool { return x.Part == PartReaderOnly && x.KeptChars == 0 }) {
		t.Fatalf("reader-only should be trimmed before knowledge: %+v", p.Trimmed)
	}
}
