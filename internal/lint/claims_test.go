package lint

import (
	"fmt"
	"strings"
	"testing"
	"time"
)

func TestClaimSources(t *testing.T) {
	r := rules(map[string]string{"claims": "claim-sources"}, nil)
	in := Input{Claims: []Claim{
		{ID: "claim-1", Kind: "experience", Text: "我们去年把连接池调到 50"},
		{ID: "claim-2", Kind: "data", Text: "不在正文中的数据", Sources: []ClaimSource{{"src-r", "reference"}}},
		{ID: "claim-3", Kind: "opinion", Text: "观点", Sources: []ClaimSource{{"src-a", "interview"}}},
		{ID: "claim-4", Kind: "fact", Text: "事实"},
	}}
	in.Unit = "my-post"
	res, _ := Run("开头。我们去年把连接池调到 50。", r, "draft.md", in)
	if len(res.Findings) != 2 {
		t.Fatalf("findings = %+v", res.Findings)
	}
	// 全文级发现项排在最前。
	f2, f1 := res.Findings[0], res.Findings[1]
	if f2.Line != 0 || !strings.Contains(f2.Message, "claim-2") || !strings.Contains(f2.Message, "src-r（reference）") || !strings.Contains(f2.Message, "wa-cli claim set my-post claim-2") {
		t.Fatalf("reference only: %+v", f2)
	}
	if f1.Line != 1 || f1.Column != 4 || f1.Text != "我们去年把连接池调到 50" || !strings.Contains(f1.Message, "claim-1") ||
		!strings.Contains(f1.Message, "wa-cli claim set my-post claim-1 --sources") || !strings.Contains(f1.Message, "wa-cli source add") {
		t.Fatalf("without source: %+v", f1)
	}
	// kinds 与 source_types 参数。
	r = rules(map[string]string{"claims": "claim-sources"}, map[string]map[string]any{"claims": {"kinds": []any{"fact"}, "source_types": []any{"reference"}}})
	res, _ = Run("事实", r, "draft.md", in)
	if len(res.Findings) != 1 || !strings.Contains(res.Findings[0].Message, "claim-4") {
		t.Fatalf("params: %+v", res.Findings)
	}
	if res, _ := Run("事实", r, "draft.md", Input{Claims: in.Claims, ClaimsInvalid: true}); len(res.Findings) != 0 {
		t.Fatal("invalid claims must skip the rule")
	}
}

func TestClaimCandidates(t *testing.T) {
	r := rules(map[string]string{"cc": "claim-candidates"}, nil)
	content := "我曾经在生产环境踩过这个坑。我们可以看到连接池的作用。延迟降低了 90%！\n\n```\n我曾经踩坑 50%\n```\n\n吞吐提升 3 倍；`我曾经 10%` 不算。"
	res, _ := Run(content, r, "draft.md", Input{})
	var got []string
	for _, f := range res.Findings {
		got = append(got, f.Text)
	}
	want := []string{"我曾经在生产环境踩过这个坑。", "延迟降低了 90%！", "吞吐提升 3 倍；"}
	if strings.Join(got, "|") != strings.Join(want, "|") {
		t.Fatalf("candidates = %q, want %q", got, want)
	}
	if res.Findings[0].Column != 1 || res.Findings[1].Column != 28 || res.Findings[2].Line != 7 || !strings.Contains(res.Findings[1].Message, "数据") || !strings.Contains(res.Findings[0].Message, "亲历") {
		t.Fatalf("positions: %+v", res.Findings)
	}
	// 被已登记论断覆盖的句子不报告；论断文字的句末标点不参与比较。
	in := Input{Claims: []Claim{{ID: "c1", Kind: "experience", Text: " 我曾经在生产环境踩过这个坑。"}, {ID: "c2", Kind: "data", Text: "延迟降低了 90%"}}}
	if res, _ := Run(content, r, "draft.md", in); len(res.Findings) != 1 || res.Findings[0].Text != "吞吐提升 3 倍；" {
		t.Fatalf("covered: %+v", res.Findings)
	}
	// 参数：自定义代词与标记词，关闭数据检测。
	r = rules(map[string]string{"cc": "claim-candidates"}, map[string]map[string]any{"cc": {"pronouns": []any{"笔者"}, "experience_markers": []any{"见过"}, "data": false}})
	if res, _ := Run("笔者见过这种情况。我曾经踩坑。提升 50%。", r, "draft.md", Input{}); len(res.Findings) != 1 || res.Findings[0].Text != "笔者见过这种情况。" {
		t.Fatalf("params: %+v", res.Findings)
	}
	if res, _ := Run("我曾经踩坑。", rules(map[string]string{"cc": "claim-candidates"}, nil), "draft.md", Input{ClaimsInvalid: true}); len(res.Findings) != 0 {
		t.Fatal("invalid claims must skip the rule")
	}
}

// 论断定位：一遍扫描找出每个论断第一次出现的位置，行内代码中的论断文字也能找到；耗时与论断数无关。
func TestClaimLocate(t *testing.T) {
	r := rules(map[string]string{"claims": "claim-sources"}, nil)
	in := Input{Claims: []Claim{
		{ID: "a", Kind: "data", Text: "把 `pool` 调到 50。」"},
		{ID: "b", Kind: "data", Text: "调到 50"},
		{ID: "c", Kind: "data", Text: "代码中的论断"},
	}}
	res, _ := Run("```\n代码中的论断\n```\n\n先把 `pool` 调到 50，再调到 50。", r, "draft.md", in)
	got := map[string]string{}
	for _, f := range res.Findings {
		got[f.Message[len("论断（Claim）"):len("论断（Claim）")+1]] = fmt.Sprintf("%d:%d:%s", f.Line, f.Column, f.Text)
	}
	// b 的第一处出现是 a 的后缀：同一结束位置只取最长的论断，b 定位到它下一处单独的出现。
	if got["a"] != "5:2:把 `pool` 调到 50" || got["b"] != "5:18:调到 50" || got["c"] != "0:0:" {
		t.Fatalf("locations = %v", got)
	}
	// 规模：一万个论断、一万行正文。
	var claims []Claim
	var lines []string
	for i := range 10000 {
		claims = append(claims, Claim{ID: fmt.Sprint(i), Kind: "data", Text: fmt.Sprintf("第%d号论断", i)})
		lines = append(lines, fmt.Sprintf("这是第%d号论断。", i))
	}
	start := time.Now()
	res, _ = Run(strings.Join(lines, "\n\n"), r, "draft.md", Input{Claims: claims})
	if d := time.Since(start); d > 5*time.Second || len(res.Findings) != 10000 || res.Findings[0].Line == 0 {
		t.Fatalf("scale: %v, %d findings", d, len(res.Findings))
	}
}

// 候选覆盖按位置判断：论断文字覆盖代词到经历标记词的区间或数据时才算已登记；cover_kinds 之外的论断不算。
func TestCandidateCoverage(t *testing.T) {
	r := rules(map[string]string{"cc": "claim-candidates"}, nil)
	content := "我曾经负责迁移，延迟降低了 90%。"
	cases := []struct {
		claims []Claim
		want   string
	}{
		{nil, "亲历、数据"},
		{[]Claim{{Kind: "experience", Text: "我曾经负责迁移"}}, "数据"},
		// 论断必须覆盖从代词到所在分句末尾的整段：很短的论断文字不能覆盖。
		{[]Claim{{Kind: "experience", Text: "我曾"}}, "亲历、数据"},
		{[]Claim{{Kind: "experience", Text: "我曾经负责"}}, "亲历、数据"},
		{[]Claim{{Kind: "data", Text: "90%"}}, "亲历"},
		{[]Claim{{Kind: "experience", Text: "我曾经负责迁移"}, {Kind: "data", Text: "降低了 90%"}}, ""},
		// 论断只覆盖了句子中无关的部分，或是 fact 类型：不算已登记。
		{[]Claim{{Kind: "experience", Text: "迁移"}}, "亲历、数据"},
		{[]Claim{{Kind: "fact", Text: "我曾经负责迁移，延迟降低了 90%"}}, "亲历、数据"},
		// 关联了素材的 fact 论断覆盖数据候选；亲历候选仍只认 cover_kinds。
		{[]Claim{{Kind: "fact", Text: "我曾经负责迁移，延迟降低了 90%", Sources: []ClaimSource{{"src-r", "reference"}}}}, "亲历"},
	}
	// 亲历区间从标记词之前最近的代词开始：前面泛指的「我们」不要求论断一并覆盖。
	if res, _ := Run("我们知道，我曾经踩过这个坑。", r, "draft.md", Input{Claims: []Claim{{Kind: "experience", Text: "我曾经踩过这个坑"}}}); len(res.Findings) != 0 {
		t.Errorf("nearest pronoun: %+v", res.Findings)
	}
	// 同一句中的两段亲历分别需要登记。
	two := "我曾经负责迁移，后来我们去年又遇到了事故。"
	if res, _ := Run(two, r, "draft.md", Input{Claims: []Claim{{Kind: "experience", Text: "我曾经负责迁移"}}}); len(res.Findings) != 1 {
		t.Errorf("second clause: %+v", res.Findings)
	}
	if res, _ := Run(two, r, "draft.md", Input{Claims: []Claim{{Kind: "experience", Text: "我曾经负责迁移"}, {Kind: "experience", Text: "我们去年又遇到了事故"}}}); len(res.Findings) != 0 {
		t.Errorf("both clauses: %+v", res.Findings)
	}
	if res, _ := Run("我们的请求要经历三次握手。我们选择最近的节点。", r, "draft.md", Input{}); len(res.Findings) != 0 {
		t.Errorf("default markers: %+v", res.Findings)
	}
	for _, tc := range cases {
		res, _ := Run(content, r, "draft.md", Input{Claims: tc.claims})
		got := ""
		if len(res.Findings) == 1 {
			m := res.Findings[0].Message
			got = m[strings.Index(m, "，")+len("，") : strings.Index(m, "）")]
		} else if len(res.Findings) > 1 {
			got = fmt.Sprint(res.Findings)
		}
		if got != tc.want {
			t.Errorf("%v: kinds = %q, want %q", tc.claims, got, tc.want)
		}
	}
	r = rules(map[string]string{"cc": "claim-candidates"}, map[string]map[string]any{"cc": {"cover_kinds": []any{"fact"}}})
	if res, _ := Run(content, r, "draft.md", Input{Claims: []Claim{{Kind: "fact", Text: "我曾经负责迁移，延迟降低了 90%"}}}); len(res.Findings) != 0 {
		t.Errorf("cover_kinds: %+v", res.Findings)
	}
}

// 关联了素材的 fact 论断覆盖数据候选；未参与覆盖的论断在发现项 message 末尾按 claims.yaml 顺序说明原因。
func TestCandidateFactCoverAndNotes(t *testing.T) {
	r := rules(map[string]string{"cc": "claim-candidates"}, nil)
	sourced := []Claim{{ID: "claim-1", Kind: "fact", Text: "我曾经负责迁移，延迟降低了 90%", Sources: []ClaimSource{{"src-r", "reference"}}}}
	// 关联了素材的 fact 论断：数据候选被覆盖，亲历候选仍报告，说明其类型不在 cover_kinds 内。
	res, _ := Run("我曾经负责迁移，延迟降低了 90%。", r, "draft.md", Input{Claims: sourced})
	if len(res.Findings) != 1 || !strings.Contains(res.Findings[0].Message, "亲历") || strings.Contains(res.Findings[0].Message, "数据") {
		t.Fatalf("sourced fact: %+v", res.Findings)
	}
	if msg := res.Findings[0].Message; !strings.Contains(msg, "论断 claim-1（fact）的类型不在 claim-candidates 的 cover_kinds（默认 experience、opinion、case、data）内，未参与覆盖") ||
		strings.Contains(msg, "未关联素材") {
		t.Fatalf("sourced fact note: %s", msg)
	}
	// 数据句子被关联了素材的 fact 论断整段覆盖：不再报告。
	sourcedData := []Claim{{ID: "claim-1b", Kind: "fact", Text: "延迟降低了 90%", Sources: []ClaimSource{{"src-r", "reference"}}}}
	if res, _ = Run("延迟降低了 90%。", r, "draft.md", Input{Claims: sourcedData}); len(res.Findings) != 0 {
		t.Fatalf("sourced fact data: %+v", res.Findings)
	}
	// 未关联素材的 fact 论断不覆盖数据候选，说明原因是未关联素材而不是类型。
	res, _ = Run("延迟降低了 90%。", r, "draft.md", Input{Claims: []Claim{{ID: "claim-2", Kind: "fact", Text: "延迟降低了 90%"}}})
	if len(res.Findings) != 1 || !strings.Contains(res.Findings[0].Message, "论断 claim-2（fact）未关联素材，不能覆盖数据候选") ||
		strings.Contains(res.Findings[0].Message, "未参与覆盖") {
		t.Fatalf("unsourced fact data: %+v", res.Findings)
	}
	// 同一论断未覆盖亲历与数据两种区间时，两句说明都追加。
	res, _ = Run("我曾经负责迁移，延迟降低了 90%。", r, "draft.md", Input{Claims: []Claim{{ID: "claim-3", Kind: "fact", Text: "我曾经负责迁移，延迟降低了 90%"}}})
	if len(res.Findings) != 1 {
		t.Fatalf("unsourced fact both: %+v", res.Findings)
	}
	msg := res.Findings[0].Message
	if !strings.Contains(msg, "论断 claim-3（fact）的类型不在 claim-candidates 的 cover_kinds（默认 experience、opinion、case、data）内，未参与覆盖") ||
		!strings.Contains(msg, "论断 claim-3（fact）未关联素材，不能覆盖数据候选") {
		t.Fatalf("unsourced fact notes: %s", msg)
	}
	// cover_kinds 配置为 [fact] 时，experience 论断不参与覆盖，说明列出实际取值。
	r2 := rules(map[string]string{"cc": "claim-candidates"}, map[string]map[string]any{"cc": {"cover_kinds": []any{"fact"}}})
	res, _ = Run("我曾经踩过坑。", r2, "draft.md", Input{Claims: []Claim{{ID: "claim-4", Kind: "experience", Text: "我曾经踩过坑"}}})
	if len(res.Findings) != 1 || !strings.Contains(res.Findings[0].Message, "论断 claim-4（experience）的类型不在 claim-candidates 的 cover_kinds（fact）内，未参与覆盖") {
		t.Fatalf("custom cover_kinds: %+v", res.Findings)
	}
}

// 句子切分与数据模式的边界：标题与引用块不检查；全角空白后的列、全角数字、百分之与中文倍数；text 为原文。
func TestCandidateEdges(t *testing.T) {
	r := rules(map[string]string{"cc": "claim-candidates"}, nil)
	content := "## 我曾经踩过的坑\n\n> 我们去年增长了 50%。\n\n\u3000吞吐提升５０％。成本降为百分之十。性能提高三倍。用 `50%` 说明我曾经负责。"
	res, _ := Run(content, r, "draft.md", Input{})
	var got []string
	for _, f := range res.Findings {
		got = append(got, fmt.Sprintf("%d:%d:%s", f.Line, f.Column, f.Text))
	}
	want := []string{"3:1:> 我们去年增长了 50%。", "5:2:吞吐提升５０％。", "5:10:成本降为百分之十。", "5:19:性能提高三倍。", "5:26:用 `50%` 说明我曾经负责。"}
	if strings.Join(got, "|") != strings.Join(want, "|") {
		t.Fatalf("candidates = %q, want %q", got, want)
	}
	// 引用块只检测数据；行内代码中的数据不算。
	if strings.Contains(res.Findings[0].Message, "亲历") || !strings.Contains(res.Findings[4].Message, "亲历") || strings.Contains(res.Findings[4].Message, "数据") ||
		!strings.Contains(res.Findings[1].Message, "wa-cli claim add <单元>") {
		t.Fatalf("quote, masked data and message: %+v", res.Findings)
	}
	// 候选超过 MaxHits 时截断并报告 lint.truncated。
	res, ps := Run(strings.Repeat("提升 50%。", MaxHits+5), r, "draft.md", Input{})
	if len(res.Findings) != MaxHits || len(ps) != 1 || ps[0].Code != CodeTruncated {
		t.Fatalf("truncated: %d findings, %+v", len(res.Findings), ps)
	}
}
