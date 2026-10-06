package continuity

import (
	"strings"
	"testing"

	"github.com/kw12121212/writing-anything/internal/work"
)

// 揭示词（Reveal Terms）检查：正文出现某条目的揭示词且早于获知者的 confirmed 获知点时，
// 报 error 级 continuity.knowledge-early；获知点及之后干净；没有任何 terms 的作品零新增发现。
func TestKnowledgeEarly(t *testing.T) {
	const draftEarly = "他早就知道，父亲是为了救人才走的。\n"
	const draftLate = "真相在灯下摊开：他是为了救人才走的。\n"
	drafts := map[string]string{"scene-1": draftEarly, "scene-2": draftLate}
	knowledgeYAML := func(terms string) string {
		return "schema_version: 1\nevents:\n  - id: know-1\n    scene: my-novel/scene-2\n" +
			"    entry: canon-3\n    character: reader\n" + terms +
			"    status: confirmed\n    confirmation: 作者确认\n    decided_at: \"2026-06-20T12:00:00Z\"\n"
	}

	setup := func(terms string) (string, *work.Work) {
		root, _ := newNovel(t)
		writeAt(t, root, ".writing-anything/works/my-novel/canon.yaml", `schema_version: 1
entries:
  - {id: canon-1, type: character, name: 守灯人, text: 五十岁, sources: [], protection: normal}
  - {id: canon-3, type: item, name: 旧怀表, text: 一块停摆的怀表, sources: [], protection: normal}
`)
		for scene, text := range drafts {
			writeAt(t, root, ".writing-anything/works/my-novel/units/"+scene+"/draft.md", text)
			outlineAt(t, root, scene, "  - {id: sc, title: 场景, reason: 推进, characters: [守灯人], beats: [b1], pov: 守灯人, location: 灯室}\n")
		}
		writeAt(t, root, ".writing-anything/works/my-novel/outline.yaml",
			"schema_version: 1\nsections:\n  - {id: b1, title: 节拍, reason: 推进情节}\n")
		writeAt(t, root, ".writing-anything/works/my-novel/knowledge.yaml", knowledgeYAML(terms))
		w2, ps := work.Load(root, "my-novel")
		if len(ps) > 0 {
			t.Fatal(ps)
		}
		return root, w2
	}

	t.Run("early-reveal", func(t *testing.T) {
		root, w := setup("    terms: [为了救人]\n")
		fs, ps := Run(root, w, unit(t, w, "my-novel/scene-1"))
		if len(ps) > 0 {
			t.Fatal(ps)
		}
		if !has(fs, CodeKnowledgeEarly) {
			t.Fatalf("提前揭示应报 %s，实际 %v", CodeKnowledgeEarly, fs)
		}
		// scene-2 是获知点本身，不报。
		fs2, _ := Run(root, w, unit(t, w, "my-novel/scene-2"))
		if has(fs2, CodeKnowledgeEarly) {
			t.Fatalf("获知点场景不应报：%v", fs2)
		}
	})

	t.Run("present-character-without-event", func(t *testing.T) {
		// 守灯人出场但没有对 canon-3 的 confirmed 事件：只有 reader 判定生效，仍应报。
		root, w := setup("    terms: [为了救人]\n")
		fs, _ := Run(root, w, unit(t, w, "my-novel/scene-1"))
		n := 0
		for _, f := range fs {
			if f.Code == CodeKnowledgeEarly {
				n++
			}
		}
		if n != 1 {
			t.Fatalf("无人物事件时只应有一条 reader 发现，实际 %d：%v", n, fs)
		}
	})

	t.Run("no-terms-zero-findings", func(t *testing.T) {
		root, w := setup("")
		fs, ps := Run(root, w, unit(t, w, "my-novel/scene-1"))
		if len(ps) > 0 {
			t.Fatal(ps)
		}
		if len(fs) != 0 {
			t.Fatalf("无揭示词时不应有任何发现：%v", fs)
		}
	})

	t.Run("point-decoupled-from-terms", func(t *testing.T) {
		// reader 在 scene-1 已有无 terms 的 confirmed 获知，scene-2 的事件才带词：
		// 获知点按全部 confirmed 事件取最早（scene-1），scene-1 出现揭示词不报（词表条件不污染获知点）。
		root, w := setup("    terms: [四点十七分]\n")
		writeAt(t, root, ".writing-anything/works/my-novel/knowledge.yaml", `schema_version: 1
events:
  - id: know-0
    scene: my-novel/scene-1
    entry: canon-3
    character: reader
    status: confirmed
    confirmation: 作者确认
    decided_at: "2026-06-20T12:00:00Z"
  - id: know-1
    scene: my-novel/scene-2
    entry: canon-3
    character: reader
    terms: [为了救人]
    status: confirmed
    confirmation: 作者确认
    decided_at: "2026-06-20T12:00:00Z"
`)
		writeAt(t, root, ".writing-anything/works/my-novel/units/scene-1/draft.md", "灯下写着：他是为了救人才走的。\n")
		fs, _ := Run(root, w, unit(t, w, "my-novel/scene-1"))
		if has(fs, CodeKnowledgeEarly) {
			t.Fatalf("更早的无 terms 获知也是获知点，不应报：%v", fs)
		}
		// 反向：把无 terms 事件移到 scene-2、带词事件在 scene-2，scene-1 即早于获知点，应报。
		writeAt(t, root, ".writing-anything/works/my-novel/knowledge.yaml", `schema_version: 1
events:
  - id: know-1
    scene: my-novel/scene-2
    entry: canon-3
    character: reader
    terms: [为了救人]
    status: confirmed
    confirmation: 作者确认
    decided_at: "2026-06-20T12:00:00Z"
`)
		fs, _ = Run(root, w, unit(t, w, "my-novel/scene-1"))
		if !has(fs, CodeKnowledgeEarly) {
			t.Fatalf("唯一获知点在 scene-2 时 scene-1 应报：%v", fs)
		}
	})

	t.Run("character-channel", func(t *testing.T) {
		// 守灯人对 canon-3 的获知点在 scene-2：scene-1 出现揭示词时人物通道应报，
		// message 含出场人物与其条目 id，severity 为 error。
		root, w := setup("    terms: [为了救人]\n")
		writeAt(t, root, ".writing-anything/works/my-novel/knowledge.yaml", `schema_version: 1
events:
  - id: know-1
    scene: my-novel/scene-2
    entry: canon-3
    character: canon-1
    terms: [为了救人]
    status: confirmed
    confirmation: 作者确认
    decided_at: "2026-06-20T12:00:00Z"
`)
		writeAt(t, root, ".writing-anything/works/my-novel/units/scene-1/draft.md", "他早就知道，父亲是为了救人才走的。\n")
		fs, _ := Run(root, w, unit(t, w, "my-novel/scene-1"))
		var char *Finding
		for i := range fs {
			if fs[i].Code == CodeKnowledgeEarly && strings.Contains(fs[i].Message, "守灯人") {
				char = &fs[i]
			}
		}
		if char == nil {
			t.Fatalf("出场人物通道应报含「守灯人」的发现：%v", fs)
		}
		if char.Severity != "error" || !strings.Contains(char.Message, "canon-1") || !strings.Contains(char.Message, "canon-3") {
			t.Fatalf("人物通道发现形态不符：%+v", char)
		}
	})

	t.Run("term-absent-clean", func(t *testing.T) {
		root, w := setup("    terms: [四点十七分]\n")
		fs, _ := Run(root, w, unit(t, w, "my-novel/scene-1"))
		if has(fs, CodeKnowledgeEarly) {
			t.Fatalf("正文没有揭示词时不应报：%v", fs)
		}
	})
}
