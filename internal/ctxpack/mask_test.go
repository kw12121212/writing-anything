package ctxpack

import (
	"slices"
	"strings"
	"testing"

	"github.com/kw12121212/writing-anything/internal/work"
)

// 切句与遮蔽：句末标点与收尾引号、相邻被遮蔽句合并、换行分隔的句子各自占位、没有揭示词时原样返回。
func TestMaskText(t *testing.T) {
	if got := splitSentences("他说：“秘密。”然后走了。尾巴"); !slices.Equal(got, []string{"他说：“秘密。”", "然后走了。", "尾巴"}) {
		t.Fatalf("split = %q", got)
	}
	if got := splitSentences("等等……真的？！好"); !slices.Equal(got, []string{"等等……", "真的？！", "好"}) {
		t.Fatalf("split ellipsis = %q", got)
	}
	cases := []struct {
		text  string
		terms []string
		want  string
		n     int
	}{
		{"甲。乙有秘密。丙也有秘密！丁。", []string{"秘密"}, "甲。" + Placeholder + "丁。", 2},
		{"秘密一。\n秘密二。\n平常。", []string{"秘密"}, Placeholder + "\n" + Placeholder + "\n平常。", 2},
		{"甲。乙。", []string{"秘密"}, "甲。乙。", 0},
		{"甲有秘密。", nil, "甲有秘密。", 0},
		{"", []string{"秘密"}, "", 0},
	}
	for _, c := range cases {
		got, n := maskText(c.text, c.terms)
		if got != c.want || n != c.n {
			t.Errorf("maskText(%q) = %q, %d; want %q, %d", c.text, got, n, c.want, c.n)
		}
	}
}

// maskProject 是三个场景的短篇：守灯人（canon-1）与学徒（canon-4）的人物卡、守灯人已知的 canon-2 都写到灯芯
// 的秘密（canon-3，揭示词「灯芯所化」）；守灯人在 s2 获知 canon-3，读者在 s1 获知；canon-5（揭示词「灯塔会塌」）
// 只在最后一个场景 s3 由学徒获知，任何场景开始时都没有获知者已知。
func maskProject(t *testing.T, pov string) string {
	t.Helper()
	root := storyProject(t)
	if _, _, ps := work.AddUnit(root, "my-story", "scene", "s3", work.Anchor{}); len(ps) > 0 {
		t.Fatal(ps)
	}
	dir := work.Dir("my-story")
	put(t, root, dir+"/canon.yaml", "schema_version: 1\nentries:\n"+
		"  - {id: canon-1, type: character, name: 守灯人, text: 五十岁，左手有旧伤。他知道学徒是灯芯所化的精灵。, sources: [], protection: normal}\n"+
		"  - {id: canon-2, type: world-rule, text: 灯油只能燃烧到黎明。灯芯所化的精灵会在黎明消失。, sources: [], protection: normal}\n"+
		"  - {id: canon-3, type: world-rule, text: 学徒是灯芯所化。, sources: [], protection: normal}\n"+
		"  - {id: canon-4, type: character, name: 学徒, text: 十六岁，沉默。其实是灯芯所化的精灵！夜里添灯油。灯塔会塌的事他最后才知道。, sources: [], protection: normal}\n"+
		"  - {id: canon-5, type: world-rule, text: 灯塔会塌。, sources: [], protection: normal}\n")
	section := "  - {id: sc, title: 灯下, characters: [守灯人, 学徒]}\n"
	if pov != "" {
		section = "  - {id: sc, title: 灯下, characters: [守灯人, 学徒], pov: " + pov + "}\n"
	}
	for _, s := range []string{"s1", "s2", "s3"} {
		put(t, root, dir+"/units/"+s+"/outline.yaml", "schema_version: 1\nsections:\n"+section)
	}
	put(t, root, dir+"/knowledge.yaml", `schema_version: 1
events:
  - {id: know-1, scene: my-story/s1, entry: canon-2, character: canon-1, status: confirmed, confirmation: 作者确认, decided_at: 2026-10-03T08:00:00Z}
  - {id: know-2, scene: my-story/s1, entry: canon-3, character: reader, terms: [灯芯所化], status: confirmed, confirmation: 作者确认, decided_at: 2026-10-03T08:00:00Z}
  - {id: know-3, scene: my-story/s2, entry: canon-3, character: canon-1, status: confirmed, confirmation: 作者确认, decided_at: 2026-10-03T08:00:00Z}
  - {id: know-4, scene: my-story/s3, entry: canon-5, character: canon-4, terms: [灯塔会塌], status: confirmed, confirmation: 作者确认, decided_at: 2026-10-03T08:00:00Z}
  - {id: know-5, scene: my-story/s1, entry: canon-5, character: canon-1, terms: [不会登记], status: proposed}
`)
	return root
}

func buildAt(t *testing.T, root, ref string) *Pack {
	t.Helper()
	w, u, ps := work.Resolve(root, ref)
	if ps != nil {
		t.Fatal(ps)
	}
	p, ps := Build(root, w, u)
	if p == nil {
		t.Fatal(ps)
	}
	return p
}

func characterText(p *Pack, id string) string {
	for _, c := range p.Characters {
		if c.ID == id {
			return c.Text
		}
	}
	return ""
}

// 秘密遮蔽（Secret Masking）：视角人物获知前人物卡与已知条目中的秘密句被占位替换，获知后恢复；读者已知、人物
// 未知的条目不遮蔽；没有任何获知者已知的受约束句不出现在任何视角；没有视角人物时全部已确认揭示词参与遮蔽。
func TestSecretMasking(t *testing.T) {
	root := maskProject(t, "守灯人")

	// s2 开始时守灯人尚未获知 canon-3：两张人物卡与守灯人已知的 canon-2 中含「灯芯所化」的句子都被遮蔽。
	p := buildAt(t, root, "my-story/s2")
	if got := characterText(p, "canon-1"); got != "五十岁，左手有旧伤。"+Placeholder {
		t.Fatalf("s2 canon-1 = %q", got)
	}
	if got := characterText(p, "canon-4"); got != "十六岁，沉默。"+Placeholder+"夜里添灯油。"+Placeholder {
		t.Fatalf("s2 canon-4 = %q", got)
	}
	if len(p.Knowledge) != 1 || p.Knowledge[0].Text != "灯油只能燃烧到黎明。"+Placeholder {
		t.Fatalf("s2 knowledge = %+v", p.Knowledge)
	}
	// 读者已知、人物未知的单列保持不变。
	if len(p.ReaderOnly) != 1 || p.ReaderOnly[0].Entry != "canon-3" || p.ReaderOnly[0].Text != "学徒是灯芯所化。" {
		t.Fatalf("s2 readerOnly = %+v", p.ReaderOnly)
	}
	want := []Masked{{PartCharacter, "canon-1", 1}, {PartCharacter, "canon-4", 2}, {PartKnowledge, "canon-2", 1}}
	if !slices.Equal(p.Masked, want) {
		t.Fatalf("s2 masked = %+v", p.Masked)
	}
	md := p.Markdown()
	if !strings.Contains(md, "## 被遮蔽的设定") || !strings.Contains(md, "- character canon-4：遮蔽 2 句") || strings.Contains(md, "左手有旧伤。他知道") {
		t.Fatalf("markdown:\n%s", md)
	}

	// s3 开始时守灯人已获知 canon-3：秘密句恢复；canon-5 仍无人获知，其揭示词所在句继续遮蔽。
	p = buildAt(t, root, "my-story/s3")
	if got := characterText(p, "canon-1"); got != "五十岁，左手有旧伤。他知道学徒是灯芯所化的精灵。" {
		t.Fatalf("s3 canon-1 = %q", got)
	}
	if got := characterText(p, "canon-4"); got != "十六岁，沉默。其实是灯芯所化的精灵！夜里添灯油。"+Placeholder {
		t.Fatalf("s3 canon-4 = %q", got)
	}
	if !slices.Equal(p.Masked, []Masked{{PartCharacter, "canon-4", 1}}) {
		t.Fatalf("s3 masked = %+v", p.Masked)
	}

	// 没有任何获知者已知的受约束句（canon-5）不出现在任何视角、任何场景的上下文中（守灯人在 s1 的 proposed 事件不算获知）。
	for _, pov := range []string{"守灯人", "学徒", ""} {
		root := maskProject(t, pov)
		for _, ref := range []string{"my-story/s1", "my-story/s2", "my-story/s3"} {
			md := buildAt(t, root, ref).Markdown()
			if strings.Contains(md, "灯塔会塌") {
				t.Fatalf("pov %q %s leaks canon-5:\n%s", pov, ref, md)
			}
		}
	}

	// 学徒为视角：始终不知道 canon-3，s3 中守灯人人物卡的秘密句仍被遮蔽。
	p = buildAt(t, maskProject(t, "学徒"), "my-story/s3")
	if got := characterText(p, "canon-1"); got != "五十岁，左手有旧伤。"+Placeholder {
		t.Fatalf("apprentice pov canon-1 = %q", got)
	}

	// 没有声明视角人物：全部已确认揭示词参与遮蔽。
	p = buildAt(t, maskProject(t, ""), "my-story/s3")
	if got := characterText(p, "canon-1"); got != "五十岁，左手有旧伤。"+Placeholder {
		t.Fatalf("no pov canon-1 = %q", got)
	}

	// 非 scene 单元：没有遮蔽报告。
	if p = buildAt(t, root, "my-story"); len(p.Masked) != 0 {
		t.Fatalf("story masked = %+v", p.Masked)
	}
}

// 场景优先级（context-trim）：出场人物设定项排在本节大纲条目之后、状态快照与知情条目之前。
func TestSceneCharacterRank(t *testing.T) {
	root := maskProject(t, "守灯人")
	put(t, root, work.Dir("my-story")+"/outline.yaml", "schema_version: 1\nsections:\n  - {id: s2, title: 第二场, reason: 对峙}\n")
	p := buildAt(t, root, "my-story/s2")
	var parts []string
	for _, it := range p.items {
		if len(parts) == 0 || parts[len(parts)-1] != it.part {
			parts = append(parts, it.part)
		}
	}
	at := func(part string) int { return slices.Index(parts, part) }
	if at(PartSection) < 0 || !(at(PartOutline) < at(PartSection) && at(PartSection) < at(PartCharacter) && at(PartCharacter) < at(PartKnowledge)) {
		t.Fatalf("parts = %v", parts)
	}
}

// 句末空白属于前一句：空白分隔的被遮蔽句各自占位并保留空白；ASCII 双引号不作为收尾引号；被遮蔽后又被裁剪的
// 内容项仍在 masked 中。
func TestMaskWhitespaceAndTrimmed(t *testing.T) {
	if got, n := maskText("甲有秘密。 乙有秘密。丙。", []string{"秘密"}); got != Placeholder+" "+Placeholder+"丙。" || n != 2 {
		t.Fatalf("whitespace: %q %d", got, n)
	}
	if got := splitSentences(`他说完。"下一句。"`); !slices.Equal(got, []string{"他说完。", `"下一句。`, `"`}) {
		t.Fatalf("ascii quote: %q", got)
	}
	root := maskProject(t, "守灯人")
	put(t, root, ".writing-anything/config.yaml", "schema_version: 1\nlanguage: zh-CN\ngate_level: standard\npacks: []\ncontext_budget: 10\n")
	p := buildAt(t, root, "my-story/s2")
	if len(p.Characters) != 0 || !slices.ContainsFunc(p.Masked, func(m Masked) bool { return m.ID == "canon-4" }) {
		t.Fatalf("trimmed but masked: characters = %+v, masked = %+v", p.Characters, p.Masked)
	}
}
