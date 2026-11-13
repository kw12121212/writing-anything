package lint

import (
	"fmt"
	"math/rand"
	"runtime"
	"slices"
	"strings"
	"testing"
	"unicode"

	"github.com/kw12121212/writing-anything/internal/resolve"
)

// pat 构造一个模式参数（YAML 解码后的形状）。
func pat(id string, terms []string, opts ...string) map[string]any {
	m := map[string]any{"id": id, "terms": toAny(terms)}
	for i := 0; i+1 < len(opts); i += 2 {
		v := any(opts[i+1])
		if opts[i] == "at_sentence_start" {
			v = opts[i+1] == "true"
		}
		m[opts[i]] = v
	}
	return m
}

func toAny(ss []string) []any {
	out := []any{}
	for _, s := range ss {
		out = append(out, s)
	}
	return out
}

// patternRules 构造只含一条 formulaic-patterns 规则 p 的合并结果，severity 为 warning。
func patternRules(patterns ...map[string]any) *resolve.Resolved {
	var items []any
	for _, p := range patterns {
		items = append(items, p)
	}
	r := rules(map[string]string{"p": "formulaic-patterns"}, map[string]map[string]any{"p": {"patterns": items}})
	r.Rules["p"].Severity.Value = "warning"
	return r
}

func densityRules(params map[string]any) *resolve.Resolved {
	r := rules(map[string]string{"d": "tone-density"}, map[string]map[string]any{"d": params})
	r.Rules["d"].Severity.Value = "warning"
	return r
}

// lint 场景 pattern-sentence-hit：同句内按顺序出现的词组报告一项，位置为第一个词，text 以「…」连接。
func TestPatternSentenceHit(t *testing.T) {
	r := patternRules(pat("not-but", []string{"不是", "而是"}))
	res, ps := run(t, "# 标题\n\n这不是工具的问题，而是流程的问题。", r)
	if ps != nil || len(res.Findings) != 1 {
		t.Fatalf("findings = %+v, problems = %+v", res.Findings, ps)
	}
	f := res.Findings[0]
	if f.Rule != "p" || f.Type != "formulaic-patterns" || f.Severity != "warning" || f.Line != 3 || f.Column != 2 ||
		f.Text != "不是…而是" || !strings.Contains(f.Message, "not-but") || f.Suggestion != "" {
		t.Fatalf("finding = %+v", f)
	}
	// 给出 message 时原样使用。
	r = patternRules(pat("not-but", []string{"不是", "而是"}, "message", "直接说。"))
	if res, _ := run(t, "不是甲而是乙。", r); len(res.Findings) != 1 || res.Findings[0].Message != "直接说。" {
		t.Fatalf("message = %+v", res.Findings)
	}
}

// lint 场景 pattern-cross-sentence-miss：词分在两句时不命中；scope 为 paragraph 时命中。
func TestPatternScope(t *testing.T) {
	content := "这不是重点。重点而是流程。"
	if res, _ := run(t, content, patternRules(pat("not-but", []string{"不是", "而是"}))); len(res.Findings) != 0 {
		t.Fatalf("cross sentence = %v", summary(res.Findings))
	}
	if res, _ := run(t, content, patternRules(pat("not-but", []string{"不是", "而是"}, "scope", "paragraph"))); len(res.Findings) != 1 {
		t.Fatalf("paragraph scope = %v", summary(res.Findings))
	}
	// 换行也切分句子；段落之间不连成一段。
	if res, _ := run(t, "首先这样\n其次那样。", patternRules(pat("fs", []string{"首先", "其次"}))); len(res.Findings) != 0 {
		t.Fatalf("newline = %v", summary(res.Findings))
	}
	if res, _ := run(t, "首先这样。\n\n其次那样。", patternRules(pat("fs", []string{"首先", "其次"}, "scope", "paragraph"))); len(res.Findings) != 0 {
		t.Fatalf("two paragraphs = %v", summary(res.Findings))
	}
	// 跨行的段落：位置换算到第二行。
	res, _ := run(t, "第一行。\n然后首先这样，其次那样。", patternRules(pat("fs", []string{"首先", "其次"}, "scope", "paragraph")))
	if got := summary(res.Findings); !slices.Equal(got, []string{"p|2|3|首先…其次|"}) {
		t.Fatalf("multi-line = %v", got)
	}
}

// 词依次出现、互不重叠；同一句中多次命中各报告一项，从上一次命中的最后一个词之后继续查找。
func TestPatternOrderAndRepeat(t *testing.T) {
	r := patternRules(pat("ab", []string{"甲", "乙", "丙"}))
	for content, want := range map[string]int{
		"甲乙丙。":     1,
		"丙乙甲。":     0,
		"甲丙乙。":     0,
		"甲乙丙甲乙丙。":  2,
		"甲甲乙乙丙。":   1,
		"甲乙丙乙丙。":   1,
		"甲乙。丙。":    0,
		"乙丙甲乙丙。":   1,
		"甲乙丙，甲乙丙！": 2,
	} {
		if res, _ := run(t, content, r); len(res.Findings) != want {
			t.Errorf("%s: %v", content, summary(res.Findings))
		}
	}
	// 重叠的词不算两次出现：「不是」之后的「是」不能再作为下一个词的开头。
	if res, _ := run(t, "不是。", patternRules(pat("x", []string{"不是", "是"}))); len(res.Findings) != 0 {
		t.Fatalf("overlap = %v", summary(res.Findings))
	}
	// 同一处原文被多个模式命中时各自报告。
	r = patternRules(pat("a", []string{"赋能"}), pat("b", []string{"赋能"}))
	if res, _ := run(t, "赋能。", r); len(res.Findings) != 2 {
		t.Fatalf("two patterns = %v", summary(res.Findings))
	}
	// ASCII 词要求前后边界，区分大小写。
	r = patternRules(pat("ai", []string{"AI", "赋能"}))
	for content, want := range map[string]int{"AI 赋能。": 1, "AIGC 赋能。": 0, "ai 赋能。": 0, "用AI赋能。": 1} {
		if res, _ := run(t, content, r); len(res.Findings) != want {
			t.Errorf("%s: %v", content, summary(res.Findings))
		}
	}
}

// lint 场景 pattern-paragraph-end 与段首：position 限定第一个词在第一句、最后一个词在最后一句。
func TestPatternPosition(t *testing.T) {
	r := patternRules(pat("closing-summary", []string{"总之"}, "position", "paragraph-end"))
	content := "缓存加好了。总之效果不错。\n\n总之先说结论。然后讲过程。最后讲问题。"
	res, _ := run(t, content, r)
	if got := summary(res.Findings); !slices.Equal(got, []string{"p|1|7|总之|"}) {
		t.Fatalf("paragraph-end = %v", got)
	}
	// 多个词：最后一个词落在最后一句即可（scope 为 paragraph）。
	r = patternRules(pat("fs", []string{"首先", "其次"}, "scope", "paragraph", "position", "paragraph-end"))
	if res, _ := run(t, "首先甲。其次乙。", r); len(res.Findings) != 1 {
		t.Fatalf("paragraph-end multi = %v", summary(res.Findings))
	}
	if res, _ := run(t, "首先甲。其次乙。最后丙。", r); len(res.Findings) != 0 {
		t.Fatalf("paragraph-end not last = %v", summary(res.Findings))
	}
	// 「其次」在前一句也出现时，取最后一句中的那一个。
	if res, _ := run(t, "首先甲，其次乙。再其次丙。", r); len(res.Findings) != 1 {
		t.Fatalf("paragraph-end later occurrence = %v", summary(res.Findings))
	}
	r = patternRules(pat("open", []string{"在", "的今天"}, "position", "paragraph-start"))
	if res, _ := run(t, "在 AI 爆发的今天，我们写博客。后面一句。", r); len(res.Findings) != 1 {
		t.Fatalf("paragraph-start = %v", summary(res.Findings))
	}
	if res, _ := run(t, "开头一句。在 AI 爆发的今天，我们写博客。", r); len(res.Findings) != 0 {
		t.Fatalf("paragraph-start second sentence = %v", summary(res.Findings))
	}
	r = patternRules(pat("open", []string{"首先", "其次"}, "scope", "paragraph", "position", "paragraph-start"))
	if res, _ := run(t, "开头。首先甲。其次乙。", r); len(res.Findings) != 0 {
		t.Fatalf("paragraph-start paragraph scope = %v", summary(res.Findings))
	}
	if res, _ := run(t, "首先甲。其次乙。", r); len(res.Findings) != 1 {
		t.Fatalf("paragraph-start paragraph scope hit = %v", summary(res.Findings))
	}
}

// lint 场景 pattern-sentence-start：at_sentence_start 要求第一个词前没有非空白字符。
func TestPatternSentenceStart(t *testing.T) {
	r := patternRules(pat("closing-visible", []string{"可见"}, "position", "paragraph-end", "at_sentence_start", "true"))
	content := "重试有间隔。每次重试的间隔在日志里清晰可见。\n\n老店不做广告。  可见，酒香不怕巷子深。"
	res, _ := run(t, content, r)
	if got := summary(res.Findings); !slices.Equal(got, []string{"p|3|10|可见|"}) {
		t.Fatalf("sentence start = %v", got)
	}
	// 段落中的句子：第一个词在句首、后续词在同句。
	r = patternRules(pat("open", []string{"在", "的今天"}, "at_sentence_start", "true"))
	for content, want := range map[string]int{
		"在 AI 爆发的今天，写作变了。":  1,
		"我们在 AI 爆发的今天写作。":   0,
		"前一句。在大模型的今天，写作变了。": 1,
		"> 在大模型的今天，写作变了。":   0,
	} {
		if res, _ := run(t, content, r); len(res.Findings) != want {
			t.Errorf("%s: %v", content, summary(res.Findings))
		}
	}
	// 句首的屏蔽区域按空白处理。
	if res, _ := run(t, "`code` 在大模型的今天，写作变了。", r); len(res.Findings) != 1 {
		t.Fatalf("masked lead = %v", summary(res.Findings))
	}
}

// lint 场景 pattern-masked：只出现在屏蔽区域中的词不命中。
func TestPatternMasked(t *testing.T) {
	r := patternRules(pat("empower", []string{"赋能"}))
	content := "正文。\n\n```go\n// 赋能\n```\n\n行内 `赋能` 与 <!-- 赋能 --> 与 https://x.cn/赋能。"
	if res, _ := run(t, content, r); len(res.Findings) != 0 {
		t.Fatalf("masked = %v", summary(res.Findings))
	}
}

// lint 场景 author-waiver：作者的单处豁免把命中移入 waived；Agent 写入的豁免不生效。
func TestPatternWaiver(t *testing.T) {
	r := patternRules(pat("not-but", []string{"不是", "而是"}))
	content := "这不是工具的问题，而是流程的问题。<!-- lint-ignore p: 我就要这个对比 -->"
	res, _ := run(t, content, r)
	if len(res.Findings) != 0 || len(res.Waived) != 1 || res.Waived[0].Reason != "我就要这个对比" {
		t.Fatalf("waived = %+v, findings = %+v", res.Waived, res.Findings)
	}
	res, ps := Run(content, r, "draft.md", Input{Origin: func(int) string { return "agent" }})
	if len(res.Findings) != 1 || len(res.Waived) != 0 || !slices.Contains(codes(ps), "lint.agent-waiver") {
		t.Fatalf("agent waiver = %+v %+v", res.Findings, ps)
	}
}

// lint 场景 invalid-patterns 与其余参数校验：field 指向出错的子字段，该规则不执行，其他规则照常执行。
func TestPatternParams(t *testing.T) {
	good := pat("ok", []string{"甲"})
	for _, tt := range []struct {
		name     string
		patterns any
		field    string
	}{
		{"four terms", []any{pat("x", []string{"a", "b", "c", "d"})}, "rules.p.params.patterns[0].terms"},
		{"empty terms", []any{good, pat("x", nil)}, "rules.p.params.patterns[1].terms"},
		{"blank term", []any{pat("x", []string{"甲", " "})}, "rules.p.params.patterns[0].terms"},
		{"terms not list", []any{map[string]any{"id": "x", "terms": "甲"}}, "rules.p.params.patterns[0].terms"},
		{"missing terms", []any{map[string]any{"id": "x"}}, "rules.p.params.patterns[0].terms"},
		{"missing id", []any{map[string]any{"terms": []any{"甲"}}}, "rules.p.params.patterns[0].id"},
		{"bad id", []any{pat("Bad_ID", []string{"甲"})}, "rules.p.params.patterns[0].id"},
		{"duplicate id", []any{good, pat("ok", []string{"乙"})}, "rules.p.params.patterns[1].id"},
		{"bad scope", []any{pat("x", []string{"甲"}, "scope", "line")}, "rules.p.params.patterns[0].scope"},
		{"bad position", []any{pat("x", []string{"甲"}, "position", "middle")}, "rules.p.params.patterns[0].position"},
		{"start not bool", []any{map[string]any{"id": "x", "terms": []any{"甲"}, "at_sentence_start": "yes"}}, "rules.p.params.patterns[0].at_sentence_start"},
		{"blank message", []any{pat("x", []string{"甲"}, "message", " ")}, "rules.p.params.patterns[0].message"},
		{"unknown field", []any{map[string]any{"id": "x", "terms": []any{"甲"}, "postion": "any"}}, "rules.p.params.patterns[0].postion"},
		{"item not object", []any{"甲"}, "rules.p.params.patterns[0]"},
		{"empty patterns", []any{}, "rules.p.params.patterns"},
		{"patterns not list", "甲", "rules.p.params.patterns"},
	} {
		r := rules(map[string]string{"p": "formulaic-patterns", "c": "ai-cliche"}, map[string]map[string]any{"p": {"patterns": tt.patterns}, "c": {"terms": []any{"甲"}}})
		res, ps := run(t, "甲乙丙丁。", r)
		if len(ps) != 1 || ps[0].Code != "rule.invalid-params" || ps[0].Field != tt.field || ps[0].Path != ".writing-anything/config.yaml" ||
			len(res.Findings) != 1 || res.Findings[0].Rule != "c" {
			t.Errorf("%s: problems = %+v, findings = %v", tt.name, ps, summary(res.Findings))
		}
	}
	// 缺少 patterns。
	r := rules(map[string]string{"p": "formulaic-patterns"}, nil)
	if _, ps := run(t, "甲。", r); len(ps) != 1 || ps[0].Field != "rules.p.params.patterns" {
		t.Fatalf("missing patterns = %+v", ps)
	}
}

// lint 场景 density-connectives：连接词每千字超过阈值时报告一项全文级发现项，message 含实测值与阈值。
func TestDensityConnectives(t *testing.T) {
	r := densityRules(map[string]any{"connectives": []any{"此外", "同时", "因此", "此外"}, "max_connectives_per_1000": 5.0})
	// 1000 个非空白字符，连接词共 9 次（每次 2 字）。
	body := strings.Repeat("此外", 3) + strings.Repeat("同时", 3) + strings.Repeat("因此", 3) + strings.Repeat("字", 1000-18)
	res, ps := run(t, body, r)
	if ps != nil || len(res.Findings) != 1 {
		t.Fatalf("findings = %+v %+v", res.Findings, ps)
	}
	f := res.Findings[0]
	if f.Type != "tone-density" || f.Line != 0 || f.Column != 0 || f.Text != "" || f.Severity != "warning" ||
		!strings.Contains(f.Message, "9.00") || !strings.Contains(f.Message, "阈值 5") || !strings.Contains(f.Message, "连接词") {
		t.Fatalf("finding = %+v", f)
	}
	// 等于阈值时不报告（「此外」3 次，每千字 3.00）。
	r = densityRules(map[string]any{"connectives": []any{"此外"}, "max_connectives_per_1000": 3.0})
	if res, _ := run(t, body, r); len(res.Findings) != 0 {
		t.Fatalf("at limit: %v", res.Findings)
	}
	// 全文级发现项不能单处豁免。
	if _, ok := (waiverSet{0: {"d": "x"}}).waive(nil, Finding{Rule: "d"}); ok {
		t.Fatal("document-level finding waived")
	}
}

// lint 场景 density-uniform-sentences 与 density-prose-sentences-only。
func TestDensitySentences(t *testing.T) {
	sentence := strings.Repeat("字", 19) + "。"
	r := densityRules(map[string]any{"min_sentence_stdev": 4.0})
	res, _ := run(t, strings.Repeat(sentence, 16), r)
	if len(res.Findings) != 1 || !strings.Contains(res.Findings[0].Message, "句长标准差为 0.00") || !strings.Contains(res.Findings[0].Message, "阈值 4") {
		t.Fatalf("uniform = %+v", res.Findings)
	}
	// 只统计标题行与列表项行之外的句子：散文只有 2 句时不判定。
	var b strings.Builder
	for i := range 12 {
		fmt.Fprintf(&b, "## 第%d节标题。真的。\n\n- %s\n", i, sentence)
	}
	b.WriteString("\n" + sentence + sentence)
	r = densityRules(map[string]any{"min_sentence_stdev": 5.0})
	if res, _ := run(t, b.String(), r); len(res.Findings) != 0 {
		t.Fatalf("prose only = %+v", res.Findings)
	}
	// 长短交替时不报告。
	varied := strings.Repeat("短。"+strings.Repeat("长", 30)+"。", 6)
	if res, _ := run(t, varied+strings.Repeat("字", 300), r); len(res.Findings) != 0 {
		t.Fatalf("varied = %+v", res.Findings)
	}
}

// lint 场景 density-uniform-sections：二级标题节的非空白字符数变异系数低于阈值时报告。
func TestDensitySections(t *testing.T) {
	section := func(n int) string { return strings.Repeat("字", n) }
	content := "# 标题\n\n" + section(2000) + "\n\n## 一\n\n" + section(300) + "\n\n## 二\n\n" + section(300) + "\n\n### 小节\n\n" +
		"\n\n## 三\n\n" + section(330) + "\n\n## 四\n\n" + section(150) + "\n" + section(150)
	r := densityRules(map[string]any{"min_section_length_cv": 0.2})
	res, _ := run(t, content, r)
	// 第一个二级标题之前的内容不成节；节不含二级标题行，但含三级标题的文字：300、302、330、300，变异系数约 0.04。
	if len(res.Findings) != 1 || !strings.Contains(res.Findings[0].Message, "节长变异系数为 0.04") || !strings.Contains(res.Findings[0].Message, "阈值 0.2") {
		t.Fatalf("sections = %+v", res.Findings)
	}
	equal := "## 一\n" + section(300) + "\n## 二\n" + section(300) + "\n## 三\n" + section(300) + "\n## 四\n" + section(300)
	if res, _ := run(t, equal, r); len(res.Findings) != 1 || !strings.Contains(res.Findings[0].Message, "0.00") {
		t.Fatalf("equal sections = %+v", res.Findings)
	}
	// 少于 3 节、围栏代码块中的 ## 与「##标题」（没有空白）都不成节。
	few := "## 一\n" + section(300) + "\n## 二\n" + section(300) + "\n```\n## 三\n```\n##四\n" + section(300)
	if res, _ := run(t, few, r); len(res.Findings) != 0 {
		t.Fatalf("few sections = %+v", res.Findings)
	}
	// 各节都为空时不判定（均值为 0）。
	empty := section(300) + "\n## 一\n## 二\n## 三\n"
	if res, _ := run(t, empty, r); len(res.Findings) != 0 {
		t.Fatalf("empty sections = %+v", res.Findings)
	}
}

// 四字片段、列表行占比与少于 300 字（lint 场景 density-short-text）。
func TestDensityFourCharAndLists(t *testing.T) {
	r := densityRules(map[string]any{"max_four_char_per_1000": 25.0})
	content := strings.Repeat("一石二鸟，", 20) + strings.Repeat("平常的说法。", 40)
	res, _ := run(t, content, r)
	if len(res.Findings) != 1 || !strings.Contains(res.Findings[0].Message, "四字片段") {
		t.Fatalf("four char = %+v", res.Findings)
	}
	// 五字串与被屏蔽区域打断的串不算四字片段。
	if n := FourCharRuns("一石二鸟。一石二鸟飞。AB一二三四"); n != 2 {
		t.Fatalf("FourCharRuns = %d", n)
	}
	r = densityRules(map[string]any{"max_list_line_ratio": 0.5})
	list := "说明一段。" + strings.Repeat("字", 300) + "\n\n- 一\n* 二\n+ 三\n1. 四\n2) 五\n   - 六\n    - 缩进四格不算\n-七\n---\n"
	res, _ = run(t, list, r)
	// 列表项 6 行，非空行 10 行（含 ---）。
	if len(res.Findings) != 1 || !strings.Contains(res.Findings[0].Message, "0.60") || !strings.Contains(res.Findings[0].Message, "6 / 10") {
		t.Fatalf("list ratio = %+v", res.Findings)
	}
	// 屏蔽后少于 300 字时任何阈值都不报告。
	r = densityRules(map[string]any{"connectives": []any{"此外"}, "max_connectives_per_1000": 1.0, "max_list_line_ratio": 0.1, "max_four_char_per_1000": 1.0})
	short := strings.Repeat("此外，一石二鸟。\n- 列表\n", 10) + "```\n" + strings.Repeat("字", 1000) + "\n```\n"
	if res, _ := run(t, short, r); len(res.Findings) != 0 {
		t.Fatalf("short text = %+v", res.Findings)
	}
}

// tone-density 参数校验：至少一个阈值，连接词表与阈值成对给出，列表行占比不超过 1。
func TestDensityParams(t *testing.T) {
	for _, tt := range []struct {
		name   string
		params map[string]any
		field  string
	}{
		{"no threshold", nil, "rules.d.params"},
		{"only connectives", map[string]any{"connectives": []any{"此外"}}, "rules.d.params.max_connectives_per_1000"},
		{"only limit", map[string]any{"max_connectives_per_1000": 5.0}, "rules.d.params.connectives"},
		{"ratio over 1", map[string]any{"max_list_line_ratio": 1.5}, "rules.d.params.max_list_line_ratio"},
		{"ratio zero", map[string]any{"max_list_line_ratio": 0.0}, "rules.d.params.max_list_line_ratio"},
		{"negative stdev", map[string]any{"min_sentence_stdev": -1.0}, "rules.d.params.min_sentence_stdev"},
		{"unknown", map[string]any{"min_sentence_stdev": 1.0, "max_dashes": 1.0}, "rules.d.params.max_dashes"},
	} {
		r := rules(map[string]string{"d": "tone-density"}, map[string]map[string]any{"d": tt.params})
		_, ps := run(t, "正文。", r)
		var fields []string
		for _, p := range ps {
			fields = append(fields, p.Code+" "+p.Field)
		}
		if !slices.Equal(fields, []string{"rule.invalid-params " + tt.field}) {
			t.Errorf("%s: problems = %v", tt.name, fields)
		}
	}
	if _, ps := run(t, "正文。", densityRules(map[string]any{"max_list_line_ratio": 1.0})); ps != nil {
		t.Fatalf("ratio 1 = %+v", ps)
	}
}

// warning 级的 tone 发现项不使 valid 为 false：Result 中 severity 照规则给出，由命令层按 error 计算 valid。
func TestToneSeverityFromRule(t *testing.T) {
	r := patternRules(pat("x", []string{"赋能"}))
	r.Rules["p"].Severity = resolve.Value{Value: "error", Source: "project"}
	res, _ := run(t, "赋能。", r)
	if len(res.Findings) != 1 || res.Findings[0].Severity != "error" || res.Findings[0].Source != "pack:default" {
		t.Fatalf("severity = %+v", res.Findings)
	}
}

// 命中超过 MaxHits 时截断并报告 lint.truncated。
func TestPatternTruncated(t *testing.T) {
	r := patternRules(pat("x", []string{"赋能"}))
	res, ps := run(t, strings.Repeat("赋能。", MaxHits+5), r)
	if len(res.Findings) != MaxHits || !slices.Contains(codes(ps), "lint.truncated") {
		t.Fatalf("findings = %d, problems = %v", len(res.Findings), codes(ps))
	}
}

// oldSplitSentences 是改为共用 SentenceSpans 之前 internal/learn 的切句实现，用于差分对照。
func oldSplitSentences(text string) []int {
	end, closers := []rune("。！？!?；;…"), []rune("”」』）】〕)]’\"'")
	var out []int
	rs := []rune(text)
	n := 0
	flush := func() {
		if n > 0 {
			out = append(out, n)
		}
		n = 0
	}
	for i := 0; i < len(rs); i++ {
		r := rs[i]
		if r == '\n' {
			flush()
			continue
		}
		if !unicode.IsSpace(r) {
			n++
		}
		if slices.Contains(end, r) {
			for i+1 < len(rs) && (slices.Contains(end, rs[i+1]) || slices.Contains(closers, rs[i+1])) {
				i++
				n++
			}
			flush()
		}
	}
	flush()
	return out
}

// 共用的切句与原文风统计的切句在随机输入上逐项一致（差分探针）；区间含句首空白、不含换行。
func TestSentenceSpansMatchStyleStats(t *testing.T) {
	alphabet := []rune("甲乙 \n\t。！？!?；;…”」』）】〕)]’\"'a")
	rng := rand.New(rand.NewSource(1))
	for range 5000 {
		rs := make([]rune, rng.Intn(24))
		for i := range rs {
			rs[i] = alphabet[rng.Intn(len(alphabet))]
		}
		s := string(rs)
		if got, want := SentenceLengths(s), oldSplitSentences(s); !slices.Equal(got, want) && !(len(got) == 0 && len(want) == 0) {
			t.Fatalf("%q: got %v, want %v", s, got, want)
		}
		for _, sp := range SentenceSpans(rs) {
			if slices.Contains(rs[sp[0]:sp[1]], '\n') {
				t.Fatalf("%q: span %v contains newline", s, sp)
			}
		}
	}
	if got := SentenceSpans([]rune("  甲。乙")); !slices.Equal(got, [][2]int{{0, 4}, {4, 5}}) {
		t.Fatalf("spans = %v", got)
	}
}

// 标题行紧贴正文（不隔空行）时单独成块：段首、段末与句首按标题之外的正文判定。
func TestPatternHeadingBlocks(t *testing.T) {
	end := patternRules(pat("closing-in-short", []string{"总之"}, "position", "paragraph-end", "at_sentence_start", "true"))
	if res, _ := run(t, "缓存加好了。总之，问题解决了。\n## 下一节", end); len(res.Findings) != 1 {
		t.Fatalf("heading after = %v", summary(res.Findings))
	}
	start := patternRules(pat("grand-opening", []string{"在", "的今天"}, "position", "paragraph-start", "at_sentence_start", "true"))
	res, _ := run(t, "## 背景\n在数字化转型的今天，缓存很重要。", start)
	if got := summary(res.Findings); !slices.Equal(got, []string{"p|2|1|在…的今天|"}) {
		t.Fatalf("heading before = %v", got)
	}
	// 标题行自己也是一块：标题中的词照常检查。
	if res, _ := run(t, "## 赋能业务\n正文。", patternRules(pat("w", []string{"赋能"}))); len(res.Findings) != 1 {
		t.Fatalf("heading text = %v", summary(res.Findings))
	}
}

// 约 2MB 的正文命中极多时，结果在收集过程中就截断：内存不随命中数放大（workspace-artifacts/input-limits）。
func TestPatternHitsBounded(t *testing.T) {
	r := patternRules(pat("a", []string{"赋能"}), pat("b", []string{"能"}), pat("c", []string{"赋"}))
	content := strings.Repeat("赋能赋能赋能。\n\n", (MaxBytes-1024)/len("赋能赋能赋能。\n\n"))
	var before, after runtime.MemStats
	runtime.GC()
	runtime.ReadMemStats(&before)
	res, ps := run(t, content, r)
	runtime.ReadMemStats(&after)
	if len(res.Findings) != MaxHits || !slices.Contains(codes(ps), "lint.truncated") {
		t.Fatalf("findings = %d, problems = %v", len(res.Findings), codes(ps))
	}
	if alloc := after.TotalAlloc - before.TotalAlloc; alloc > 300<<20 {
		t.Fatalf("allocated %d MB", alloc>>20)
	}
	// 一个巨大段落中的命中同样有界。
	res, _ = run(t, strings.Repeat("赋能。", (MaxBytes-1024)/len("赋能。")), r)
	if len(res.Findings) != MaxHits {
		t.Fatalf("single paragraph findings = %d", len(res.Findings))
	}
}
