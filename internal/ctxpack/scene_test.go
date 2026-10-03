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

// storyProject 返回一个 short-story 工作区：故事 my-story 有场景 s1 与 s2，canon.yaml 有守灯人（作者钦定）与灯塔两个条目，
// s1 有摘要（draft_sha256 与当前正文不一致，stale 为 true），s2 的场景大纲声明出场人物（守灯人、精灵）与揭示边界。
func storyProject(t *testing.T) string {
	t.Helper()
	root := t.TempDir()
	if _, ps := workspace.Init(root, nil); len(ps) > 0 {
		t.Fatal(ps)
	}
	if _, _, ps := work.Create(root, "short-story", "my-story"); len(ps) > 0 {
		t.Fatal(ps)
	}
	for _, id := range []string{"s1", "s2"} {
		if _, _, ps := work.AddUnit(root, "my-story", "scene", id, work.Anchor{}); len(ps) > 0 {
			t.Fatal(ps)
		}
	}
	dir := work.Dir("my-story")
	put(t, root, dir+"/canon.yaml",
		"schema_version: 1\nentries:\n  - {id: canon-1, type: character, name: 守灯人, text: 五十岁，左手有旧伤, sources: [], protection: author-pinned}\n  - {id: canon-2, type: location, name: 灯塔, text: 海角的旧灯塔, sources: [], protection: normal}\n")
	put(t, root, dir+"/units/s1/draft.md", "守灯人点亮了灯。\n")
	put(t, root, dir+"/units/s1/summary.yaml",
		"schema_version: 1\ntext: 守灯人点亮灯塔，等待换班的船。\ndraft_sha256: \""+strings.Repeat("0", 64)+"\"\nrecorded_at: 2026-10-01T09:30:00Z\n")
	put(t, root, dir+"/units/s2/outline.yaml",
		"schema_version: 1\nsections:\n  - id: scene-2\n    title: 灯下对峙\n    characters: [守灯人, 精灵]\n    reveal: 精灵的真实身份\n")
	return root
}

// 场景级上下文（scene-context）：出场人物、前序场景摘要与揭示边界；人物未登记、canon 无效与非 scene 单元。
func TestSceneContext(t *testing.T) {
	root := storyProject(t)
	w, u, ps := work.Resolve(root, "my-story/s2")
	if ps != nil {
		t.Fatal(ps)
	}
	p, ps := Build(root, w, u)
	// 精灵没有同名 character 条目：一条 warning（message 含人物名）；location 条目不参与匹配。
	var miss *workspace.Problem
	for i := range ps {
		if ps[i].Code == CodeCharacterNotInCanon {
			miss = &ps[i]
		}
	}
	if miss == nil || miss.Severity != workspace.SeverityWarning || !strings.Contains(miss.Message, "精灵") {
		t.Fatalf("problems = %+v", ps)
	}
	if len(p.Characters) != 1 || p.Characters[0].ID != "canon-1" || p.Characters[0].Name != "守灯人" ||
		p.Characters[0].Text != "五十岁，左手有旧伤" || p.Characters[0].Protection != "author-pinned" {
		t.Fatalf("characters = %+v", p.Characters)
	}
	if len(p.Reveal) != 1 || p.Reveal[0].Section != "scene-2" || p.Reveal[0].Text != "精灵的真实身份" {
		t.Fatalf("reveal = %+v", p.Reveal)
	}
	if len(p.PriorScenes) != 1 || p.PriorScenes[0].Unit != "my-story/s1" ||
		p.PriorScenes[0].Text != "守灯人点亮灯塔，等待换班的船。" || !p.PriorScenes[0].Stale {
		t.Fatalf("priorScenes = %+v", p.PriorScenes)
	}
	md := p.Markdown()
	for _, want := range []string{"## 出场人物（Characters）", "## 前序场景（Prior Scenes）", "## 揭示边界（Reveal Boundary）",
		"守灯人", "精灵的真实身份", "已过期：正文在摘要之后被修改"} {
		if !strings.Contains(md, want) {
			t.Errorf("markdown lacks %q", want)
		}
	}

	// 前序场景的 summary.yaml 不合法时降为 warning 并跳过，不阻塞组装。
	put(t, root, work.Dir("my-story")+"/units/s1/summary.yaml", "k: v\n")
	p, ps = Build(root, w, u)
	if p == nil || len(p.PriorScenes) != 0 || !slices.ContainsFunc(ps, func(q workspace.Problem) bool {
		return q.Severity == workspace.SeverityWarning && strings.HasSuffix(q.Path, "s1/summary.yaml")
	}) {
		t.Fatalf("broken summary: %+v %+v", p, ps)
	}

	// 前序场景没有摘要时以 warning 提示。
	os.Remove(filepath.Join(root, filepath.FromSlash(work.Dir("my-story")+"/units/s1/summary.yaml")))
	if _, ps = Build(root, w, u); !slices.ContainsFunc(ps, func(q workspace.Problem) bool {
		return q.Code == CodeSummaryMissing && q.Severity == workspace.SeverityWarning
	}) {
		t.Fatalf("missing summary: %+v", ps)
	}

	// 作品没有 canon.yaml 时出场人物缺失但不提示。
	os.Remove(filepath.Join(root, filepath.FromSlash(work.Dir("my-story")+"/canon.yaml")))
	p, ps = Build(root, w, u)
	if p == nil || len(p.Characters) != 0 || slices.ContainsFunc(ps, func(q workspace.Problem) bool {
		return q.Code == CodeCharacterNotInCanon
	}) {
		t.Fatalf("no canon: %+v %+v", p, ps)
	}

	// canon.yaml 不通过校验时上下文包不组装。
	root = storyProject(t)
	w, u, _ = work.Resolve(root, "my-story/s2")
	put(t, root, work.Dir("my-story")+"/canon.yaml", "k: v\n")
	if p, ps = Build(root, w, u); p != nil || len(ps) == 0 {
		t.Fatalf("broken canon: %+v %+v", p, ps)
	}

	// 非 scene 单元（故事根单元）没有场景级内容与诊断。
	w, rootUnit, _ := work.Resolve(root, "my-story")
	if p, ps = Build(root, w, rootUnit); p == nil || len(p.Characters) != 0 || len(p.PriorScenes) != 0 || len(p.Reveal) != 0 || len(ps) != 0 {
		t.Fatalf("story unit: %+v %+v", p, ps)
	}
}

// 场景级内容计入预算：预算很小时人物设定、揭示边界与前序场景摘要都被裁剪并出现在 trimmed 中。
func TestSceneBudget(t *testing.T) {
	root := storyProject(t)
	put(t, root, workspace.ConfigPath, "schema_version: 1\nlanguage: zh-CN\ngate_level: standard\npacks: []\ncontext_budget: 10\n")
	w, u, ps := work.Resolve(root, "my-story/s2")
	if ps != nil {
		t.Fatal(ps)
	}
	p, ps := Build(root, w, u)
	if p == nil {
		t.Fatal(ps)
	}
	for _, part := range []string{PartCharacter, PartReveal, PartSceneSummary} {
		var tr *Trim
		for i := range p.Trimmed {
			if p.Trimmed[i].Part == part {
				tr = &p.Trimmed[i]
			}
		}
		if tr == nil || tr.KeptChars != 0 {
			t.Fatalf("part %s not trimmed: %+v", part, p.Trimmed)
		}
	}
	if len(p.Characters) != 0 || len(p.Reveal) != 0 || len(p.PriorScenes) != 0 {
		t.Fatalf("fields = %+v %+v %+v", p.Characters, p.Reveal, p.PriorScenes)
	}
	// 优先级（context-trim）：预算只放得下人物设定与揭示边界之一时，人物设定保留、揭示边界被裁剪。
	root = storyProject(t)
	put(t, root, workspace.ConfigPath, "schema_version: 1\nlanguage: zh-CN\ngate_level: standard\npacks: []\ncontext_budget: 60\n")
	w, u, _ = work.Resolve(root, "my-story/s2")
	p, ps = Build(root, w, u)
	if p == nil {
		t.Fatal(ps)
	}
	if len(p.Characters) != 1 || len(p.Reveal) != 0 {
		t.Fatalf("priority: characters = %+v, reveal = %+v, trimmed = %+v", p.Characters, p.Reveal, p.Trimmed)
	}
	if slices.ContainsFunc(p.Trimmed, func(x Trim) bool { return x.Part == PartCharacter }) ||
		!slices.ContainsFunc(p.Trimmed, func(x Trim) bool { return x.Part == PartReveal && x.KeptChars == 0 }) {
		t.Fatalf("priority trimmed = %+v", p.Trimmed)
	}
	// 同名条目都保留：canon 中有两个「守灯人」时 characters 有两项。
	root = storyProject(t)
	put(t, root, work.Dir("my-story")+"/canon.yaml",
		"schema_version: 1\nentries:\n  - {id: canon-1, type: character, name: 守灯人, text: 年轻的守灯人, sources: [], protection: normal}\n  - {id: canon-2, type: character, name: 守灯人, text: 年老的守灯人, sources: [], protection: normal}\n")
	w, u, _ = work.Resolve(root, "my-story/s2")
	p, _ = Build(root, w, u)
	if len(p.Characters) != 2 || p.Characters[0].ID != "canon-1" || p.Characters[1].ID != "canon-2" {
		t.Fatalf("duplicated names: %+v", p.Characters)
	}
}
