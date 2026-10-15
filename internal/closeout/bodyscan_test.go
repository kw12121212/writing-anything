package closeout

import (
	"strings"
	"testing"

	"github.com/kw12121212/writing-anything/internal/canon"
	"github.com/kw12121212/writing-anything/internal/outline"
	"github.com/kw12121212/writing-anything/internal/workspace"
)

func strPtr(s string) *string { return &s }

// bodyCanon 是正文扫描用例的设定集：人物林晚（别名晚晚）、人物陈守拙与物品钟楼大钟。
func bodyCanon() map[string]canon.Entry {
	return map[string]canon.Entry{
		"c1": {ID: "c1", Type: canon.TypeCharacter, Name: strPtr("林晚"), Aliases: []string{"晚晚"}},
		"c2": {ID: "c2", Type: canon.TypeCharacter, Name: strPtr("陈守拙")},
		"c3": {ID: "c3", Type: "item", Name: strPtr("钟楼大钟")},
	}
}

func names(ss []Suspected) []string {
	out := []string{}
	for _, s := range ss {
		out = append(out, s.Name+"/"+s.Source)
	}
	return out
}

func TestScanBody(t *testing.T) {
	cases := []struct {
		name   string
		ol     *outline.Outline
		body   string
		waived []string
		want   []string
	}{
		{name: "dialogue new name", body: "赵铁柱说：“灯灭了。”\n赵铁柱问：“谁在那儿？”", want: []string{"赵铁柱/draft"}},
		{name: "dialogue lead needs two occurrences", body: "赵铁柱说：“灯灭了。”", want: []string{}},
		{name: "dialogue lead with 道 needs a following boundary", body: "孙二娘道：走。孙二娘道路不熟。", want: []string{"孙二娘/draft"}},
		{name: "naming introduction", body: "他说他叫王小虎。后来我叫做欧阳明。他叫李四", want: []string{"王小虎/draft", "欧阳明/draft", "李四/draft"}},
		{name: "honorifics", body: "，周老板，来碗茶。\n老张说：好。", want: []string{"周老板/draft", "老张/draft"}},
		{name: "first occurrence order and dedupe", body: "赵铁柱说。孙二娘问。赵铁柱道：走。孙二娘答。", want: []string{"赵铁柱/draft", "孙二娘/draft"}},
		{name: "registered names, aliases and surnames", body: "林晚说。林晚没再问。我叫林晚。晚晚，回来。\n陈守拙说。陈师傅，劳你。老陈说。林晚轻声说。", want: []string{}},
		{name: "common phrases", body: "他想了想，高声说。方才说的。他说。\n常常说起这事。街上，白天说好的。，金色大道。高兴道：好。", want: []string{}},
		{name: "honorific before speech verb resolves by surname", body: "陈师傅说：走。陈师傅说完就走。\n，陈叔问。", want: []string{}},
		{name: "preposition surname and shouting are not names", body: "，向老师请教。他大叫王八蛋！她尖叫赵四。", want: []string{}},
		{name: "honorific for non-character entry is not resolved", body: "，钟师傅说。", want: []string{"钟师傅/draft"}},
		{name: "comments ignored", body: "<!-- @me: 我叫赵铁柱。 -->\n正文。<!-- lint-ignore x: 我叫孙二娘。 -->", want: []string{}},
		{name: "waived", body: "我叫赵铁柱。", waived: []string{"赵铁柱"}, want: []string{}},
		{name: "outline source wins dedupe", ol: &outline.Outline{Sections: []outline.Section{{Characters: []string{"赵铁柱"}}}},
			body: "我叫赵铁柱。", want: []string{"赵铁柱/characters"}},
		{name: "empty body", body: "", want: []string{}},
	}
	for _, c := range cases {
		t.Run(c.name, func(t *testing.T) {
			got := names(scan(c.ol, []byte(c.body), bodyCanon(), c.waived))
			if strings.Join(got, ",") != strings.Join(c.want, ",") {
				t.Fatalf("scan = %v, want %v", got, c.want)
			}
		})
	}
}

// 收口提交与豁免都按正文扫描：正文新人名以 warning 提示且注明来自正文，豁免后不再列出。
func TestSubmitAndWaiveBodyNames(t *testing.T) {
	root, w := newNovel(t)
	writeAt(t, root, ".writing-anything/works/my-novel/units/scene-1/draft.md", "赵铁柱说：“灯灭了。”\n守灯人没有回头。赵铁柱问：“你听见了吗？”\n")
	u := unit(t, w, "my-novel/scene-1")
	v, _, ps := Submit(root, w, u, fullInput(), false)
	if v == nil {
		t.Fatalf("problems = %+v", ps)
	}
	if got := names(v.Suspected); strings.Join(got, ",") != "赵铁柱/draft" {
		t.Fatalf("suspected = %v", got)
	}
	var warn *workspace.Problem
	for i := range ps {
		if ps[i].Code == CodeSuspectedName {
			warn = &ps[i]
		}
	}
	if warn == nil || warn.Severity != workspace.SeverityWarning || !strings.Contains(warn.Message, "正文：赵铁柱") {
		t.Fatalf("problems = %+v", ps)
	}
	v, changed, ps := Waive(root, w, u, []string{"赵铁柱"})
	if v == nil || len(changed) != 1 || len(v.Suspected) != 0 {
		t.Fatalf("waive = %+v, %v, %+v", v, changed, ps)
	}
	// 幂等的再次豁免同样按正文重算，名单仍为空。
	if v, changed, _ = Waive(root, w, u, []string{"赵铁柱"}); v == nil || len(changed) != 0 || len(v.Suspected) != 0 {
		t.Fatalf("second waive = %+v, %v", v, changed)
	}
}
