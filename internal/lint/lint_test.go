package lint

import (
	"slices"
	"strconv"
	"strings"
	"testing"
	"time"

	"github.com/kw12121212/writing-anything/internal/catalog"
	"github.com/kw12121212/writing-anything/internal/resolve"
	"github.com/kw12121212/writing-anything/internal/workspace"
)

// rules 构造只含给定规则的合并结果；params 为 规则 id → 参数。
func rules(defs map[string]string, params map[string]map[string]any) *resolve.Resolved {
	r := &resolve.Resolved{Rules: map[string]*resolve.Rule{}}
	for id, typ := range defs {
		rule := &resolve.Rule{Type: resolve.Value{Value: typ, Source: "pack:default"}, Severity: resolve.Value{Value: "error", Source: "pack:default"},
			Params: map[string]resolve.Value{}, Enabled: true}
		for k, v := range params[id] {
			rule.Params[k] = resolve.Value{Value: v, Source: "project"}
		}
		r.Rules[id] = rule
	}
	return r
}

func run(t *testing.T, content string, r *resolve.Resolved) (*Result, []workspace.Problem) {
	t.Helper()
	res, ps := Run(content, r, "draft.md", Input{})
	return res, ps
}

func summary(fs []Finding) []string {
	var out []string
	for _, f := range fs {
		out = append(out, strings.Join([]string{f.Rule, itoa(f.Line), itoa(f.Column), f.Text, f.Suggestion}, "|"))
	}
	return out
}

func itoa(n int) string {
	if n == 0 {
		return "-"
	}
	return strconv.Itoa(n)
}

func codes(ps []workspace.Problem) []string {
	var out []string
	for _, p := range ps {
		out = append(out, p.Code)
	}
	return out
}

func TestMasking(t *testing.T) {
	r := rules(map[string]string{"c": "ai-cliche"}, map[string]map[string]any{"c": {"terms": []any{"套话"}}})
	content := "正文套话。\n\n```go\n套话\n```\n\n行内 `套话` 与 <!-- 套话 --> 与 https://x.cn/套话 与 [链接](https://y/套话) 与 [套话](z)。\n~~~\n未闭合的套话"
	res, ps := run(t, content, r)
	if ps != nil {
		t.Fatal(ps)
	}
	// 只有第一段与链接文字中的「套话」被检查（URL 路径中的汉字属于 URL）。
	if got := summary(res.Findings); !slices.Equal(got, []string{"c|1|3|套话|", "c|7|65|套话|"}) {
		t.Fatalf("findings = %v", got)
	}
	tx := prepare(content)
	if len(tx.paragraphs) != 2 || tx.paragraphs[1] != (paragraph{6, 6}) || tx.paragraphOf(3) != -1 || tx.paragraphOf(99) != -1 {
		t.Fatalf("paragraphs = %+v", tx.paragraphs)
	}
	if n := prepare("# 标题\n\n正文 abc\r\n").chars(); n != 7 {
		t.Fatalf("chars = %d", n)
	}
}

func TestCJK(t *testing.T) {
	r := rules(map[string]string{"cjk": "cjk-typography"}, nil)
	// 「.3」「.env」与「...」中的点不是句读，不报告。
	res, _ := run(t, "使用Go语言,很好.3个。见文件.env等等...结束.", r)
	want := []string{"cjk|1|2|用G|用 G", "cjk|1|4|o语|o 语", "cjk|1|7|,|，", "cjk|1|11|3个|3 个", "cjk|1|20|v等|v 等", "cjk|1|28|.|。"}
	if got := summary(res.Findings); !slices.Equal(got, want) {
		t.Fatalf("findings = %v, want %v", got, want)
	}
	r = rules(map[string]string{"cjk": "cjk-typography"}, map[string]map[string]any{"cjk": {"spacing": false, "punctuation": false}})
	if res, _ := run(t, "使用Go语言,很好", r); len(res.Findings) != 0 {
		t.Fatalf("disabled: %v", summary(res.Findings))
	}
}

func TestTermRules(t *testing.T) {
	r := rules(map[string]string{"ban": "banned-terms", "terms": "term-consistency", "c": "ai-cliche"},
		map[string]map[string]any{"ban": {"terms": []any{"Go"}}})
	r.BannedTerms = []resolve.BannedTerm{{BannedTerm: catalog.BannedTerm{Term: "赋能", Replacement: "帮助", Reason: "空泛"}}}
	r.Glossary = []resolve.Term{{Term: catalog.Term{Term: "规范包", Avoid: []string{"配置包"}}}}
	res, _ := run(t, "Google 与 Go 语言赋能配置包", r)
	byRule := map[string][]Finding{}
	for _, f := range res.Findings {
		byRule[f.Rule] = append(byRule[f.Rule], f)
	}
	if b := byRule["ban"]; len(b) != 2 || b[0].Text != "Go" || b[0].Column != 10 || b[1].Text != "赋能" || b[1].Suggestion != "帮助" || !strings.Contains(b[1].Message, "空泛") {
		t.Fatalf("banned = %+v", b)
	}
	if tc := byRule["terms"]; len(tc) != 1 || tc[0].Suggestion != "规范包" || tc[0].Column != 17 {
		t.Fatalf("terms = %+v", tc)
	}
}

func TestDocumentRules(t *testing.T) {
	r := rules(map[string]string{"len": "length-range", "para": "paragraph-length", "sec": "required-sections", "c": "ai-cliche"},
		map[string]map[string]any{"len": {"min": 300.0, "max": 5000.0}, "para": {"max": 5.0}, "sec": {"sections": []any{"背景", "结论"}}, "c": {"max_dashes_per_1000": 1.0}})
	res, ps := run(t, "## 背景\n\n短。\n\n这一段有八个字——啊——", r)
	if ps != nil {
		t.Fatal(ps)
	}
	var doc, para []Finding
	for _, f := range res.Findings {
		if f.Line == 0 {
			doc = append(doc, f)
		} else {
			para = append(para, f)
		}
	}
	if len(doc) != 3 || len(para) != 1 || para[0].Rule != "para" || para[0].Line != 5 || para[0].Column != 1 {
		t.Fatalf("doc = %+v, para = %+v", doc, para)
	}
	msgs := doc[0].Message + doc[1].Message + doc[2].Message
	for _, want := range []string{"少于下限 300", "结论", "破折号密度"} {
		if !strings.Contains(msgs, want) {
			t.Errorf("messages lack %q: %s", want, msgs)
		}
	}
	r = rules(map[string]string{"len": "length-range"}, map[string]map[string]any{"len": {"max": 3.0}})
	if res, _ := run(t, "四个汉字", r); len(res.Findings) != 1 || !strings.Contains(res.Findings[0].Message, "超过上限 3") {
		t.Fatalf("max: %+v", res.Findings)
	}
	if res, _ := run(t, "", rules(map[string]string{"c": "ai-cliche"}, map[string]map[string]any{"c": {"max_dashes_per_1000": 1.0}})); len(res.Findings) != 0 {
		t.Fatal("empty text")
	}
}

func TestParams(t *testing.T) {
	for _, tt := range []struct {
		typ    string
		params map[string]any
		field  string
	}{
		{"paragraph-length", map[string]any{"max": "many"}, "rules.x.params.max"},
		{"paragraph-length", nil, "rules.x.params.max"},
		{"paragraph-length", map[string]any{"max": 1.5}, "rules.x.params.max"},
		{"paragraph-length", map[string]any{"max": -1.0}, "rules.x.params.max"},
		{"required-sections", map[string]any{"sections": []any{"a", ""}}, "rules.x.params.sections"},
		{"required-sections", map[string]any{"sections": "a"}, "rules.x.params.sections"},
		{"cjk-typography", map[string]any{"spacing": "yes"}, "rules.x.params.spacing"},
		{"term-consistency", map[string]any{"unknown": 1.0}, "rules.x.params.unknown"},
		{"length-range", nil, "rules.x.params"},
		{"length-range", map[string]any{"min": 9.0, "max": 3.0}, "rules.x.params"},
	} {
		r := rules(map[string]string{"x": tt.typ, "c": "ai-cliche"}, map[string]map[string]any{"x": tt.params, "c": {"terms": []any{"套话"}}})
		res, ps := run(t, "套话", r)
		if len(ps) != 1 || ps[0].Code != "rule.invalid-params" || ps[0].Field != tt.field || len(res.Findings) != 1 || res.Findings[0].Rule != "c" {
			t.Errorf("%s %v: problems = %+v, findings = %v", tt.typ, tt.params, ps, summary(res.Findings))
		}
	}
	// 被规则级豁免的规则与未知类型的规则不执行。
	r := rules(map[string]string{"c": "ai-cliche", "u": "nope"}, map[string]map[string]any{"c": {"terms": []any{"套话"}}})
	r.Rules["c"].Enabled = false
	if res, ps := run(t, "套话", r); len(res.Findings) != 0 || ps != nil {
		t.Fatalf("disabled: %+v %+v", res.Findings, ps)
	}
	if v, ok := convert(kindPositiveInt, 3); !ok || v != 3 {
		t.Fatal("int")
	}
	if v, ok := convert(kindPositiveInt, int64(4)); !ok || v != 4 {
		t.Fatal("int64")
	}
}

func TestWaivers(t *testing.T) {
	r := rules(map[string]string{"c": "ai-cliche", "len": "length-range"}, map[string]map[string]any{"c": {"terms": []any{"套话"}}, "len": {"min": 100.0}})
	content := "套话一。<!-- lint-ignore c: 作者原话 -->\n\n<!-- lint-ignore c：下一段 -->\n\n套话二。\n\n套话三。\n\n<!-- lint-ignore c -->\n<!-- lint-ignore nope: x -->\n<!-- lint-ignore len: 全文 -->"
	res, ps := run(t, content, r)
	if got := codes(ps); !slices.Equal(got, []string{"lint.invalid-waiver", "lint.unknown-waiver-rule"}) || ps[0].Severity != "warning" || !strings.Contains(ps[0].Message, "第 9 行") {
		t.Fatalf("problems = %+v", ps)
	}
	var waived []string
	for _, f := range res.Waived {
		waived = append(waived, itoa(f.Line)+" "+f.Reason)
	}
	if !slices.Equal(waived, []string{"1 作者原话", "5 下一段"}) {
		t.Fatalf("waived = %v", waived)
	}
	// 第三段与全文级发现项不被豁免。
	if got := summary(res.Findings); len(got) != 2 || !strings.HasPrefix(got[0], "len|-") || !strings.HasPrefix(got[1], "c|7|") {
		t.Fatalf("findings = %v", got)
	}
	// 位于文末、之后没有段落的单独注释不生效。
	if res, _ := run(t, "套话。\n\n<!-- lint-ignore c: 理由 -->", r); len(res.Waived) != 0 {
		t.Fatal("trailing waiver")
	}
}

// 输入规模：注释、链接标记与词条很多时耗时与正文长度成线性关系；超过上限时报告 lint.too-large（input-limits）。
func TestInputLimits(t *testing.T) {
	r := rules(map[string]string{"c": "ai-cliche", "ban": "banned-terms"}, map[string]map[string]any{"c": {"terms": []any{"套话"}}})
	for i := range 2000 {
		r.BannedTerms = append(r.BannedTerms, resolve.BannedTerm{BannedTerm: catalog.BannedTerm{Term: "词" + strconv.Itoa(i)}})
	}
	for name, content := range map[string]string{
		"waivers": strings.Repeat("<!-- lint-ignore c: x -->\n", 20000),
		"links":   strings.Repeat("](", 300000),
		"ticks":   strings.Repeat("`a", 300000),
		"comment": strings.Repeat("<!--", 200000),
		"terms":   strings.Repeat("正文里没有禁用词的一行文字。\n", 30000),
	} {
		start := time.Now()
		if _, ps := run(t, content, r); len(ps) != 0 && name != "waivers" {
			t.Fatalf("%s: %+v", name, ps[0])
		}
		if d := time.Since(start); d > 5*time.Second {
			t.Errorf("%s: took %v", name, d)
		}
	}
	res, ps := run(t, strings.Repeat("a", MaxBytes+1), r)
	if len(ps) != 1 || ps[0].Code != "lint.too-large" || len(res.Findings) != 0 {
		t.Fatalf("too large: %+v", ps)
	}
}

func TestTextModelEdges(t *testing.T) {
	r := rules(map[string]string{"c": "ai-cliche"}, map[string]map[string]any{"c": {"terms": []any{"套话"}}})
	for _, tt := range []struct {
		name, content string
		want          int
	}{
		{"unclosed comment is text", "正文 <!-- 未闭合\n套话\n\n套话", 2},
		{"inline code line is not a fence", "```js``` 套话", 1},
		{"four backtick fence", "````md\n```\n套话\n````\n套话", 1},
		{"double backtick code", "``套 ` 话`` 套话", 1},
		{"tilde fence", "~~~\n套话\n~~~\n套话", 1},
		{"unclosed fence", "```\n套话", 0},
	} {
		if res, _ := run(t, tt.content, r); len(res.Findings) != tt.want {
			t.Errorf("%s: findings = %v, want %d", tt.name, summary(res.Findings), tt.want)
		}
	}
	// BOM 不计入列号与字数；单独的 CR 视为换行。
	res, _ := run(t, "\ufeff套话\r套话", r)
	if got := summary(res.Findings); !slices.Equal(got, []string{"c|1|1|套话|", "c|2|1|套话|"}) || res.Chars != 4 {
		t.Fatalf("bom and cr: %v, chars = %d", got, res.Chars)
	}
	// 区分大小写；连字符连着的 ASCII 字母数字是同一个词。
	b := rules(map[string]string{"ban": "banned-terms"}, map[string]map[string]any{"ban": {"terms": []any{"go", "k8s", "go"}}})
	if res, _ := run(t, "Go go-kit k8s-x k8s 用go", b); !slices.Equal(summary(res.Findings), []string{"ban|1|17|k8s|", "ban|1|22|go|"}) {
		t.Fatalf("boundaries: %v", summary(res.Findings))
	}
}

func TestTermOverlapAndInactive(t *testing.T) {
	r := rules(map[string]string{"terms": "term-consistency"}, nil)
	r.Glossary = []resolve.Term{{Term: catalog.Term{Term: "大语言模型", Avoid: []string{"语言模型"}}}}
	res, _ := run(t, "大语言模型很强，语言模型也行", r)
	if got := summary(res.Findings); !slices.Equal(got, []string{"terms|1|9|语言模型|大语言模型"}) {
		t.Fatalf("overlap: %v", got)
	}
	// 有禁用词与 avoid 写法却没有生效的对应规则时给出 warning。
	r = rules(map[string]string{"c": "ai-cliche"}, nil)
	r.BannedTerms = []resolve.BannedTerm{{BannedTerm: catalog.BannedTerm{Term: "赋能"}}}
	r.Glossary = []resolve.Term{{Term: catalog.Term{Term: "甲", Avoid: []string{"乙"}}}}
	if _, ps := run(t, "赋能", r); !slices.Equal(codes(ps), []string{"lint.inactive-terms", "lint.inactive-terms"}) || ps[0].Severity != "warning" {
		t.Fatalf("inactive: %+v", ps)
	}
}

func TestDashDensity(t *testing.T) {
	r := rules(map[string]string{"c": "ai-cliche"}, map[string]map[string]any{"c": {"max_dashes_per_1000": 110.0}})
	// 连续的 — 记为一个（破折号本身也计入字数）：19 个字中 2 处破折号为每千字 105 个，不超过上限。
	content := strings.Repeat("字", 10) + "——" + strings.Repeat("字", 4) + "———"
	if res, _ := run(t, content, r); len(res.Findings) != 0 {
		t.Fatalf("consecutive dashes: %v", summary(res.Findings))
	}
	if res, _ := run(t, content+"—", r); len(res.Findings) != 0 {
		t.Fatalf("appended to a run: %v", summary(res.Findings))
	}
	if res, _ := run(t, content+"字—", r); len(res.Findings) != 1 {
		t.Fatalf("third dash: %v", summary(res.Findings))
	}
}

func TestWaiverScope(t *testing.T) {
	r := rules(map[string]string{"c": "ai-cliche", "cjk": "cjk-typography"}, map[string]map[string]any{"c": {"terms": []any{"套话"}}})
	// 两条豁免各自成段，都作用于其后的第一个散文段落；跨空行的注释同样作用于注释之后的散文段落。
	res, ps := run(t, "<!-- lint-ignore c: 甲 -->\n\n<!-- lint-ignore cjk: 乙 -->\n\n套话Go\n\n<!-- lint-ignore c: 原话\n\n更多说明 -->\n\n套话", r)
	if len(ps) != 0 || len(res.Findings) != 0 || len(res.Waived) != 3 {
		t.Fatalf("findings = %v, waived = %v, problems = %+v", summary(res.Findings), summary(res.Waived), ps)
	}
	// 格式不对的豁免给出 warning，不生效。
	for _, c := range []string{"<!-- lint-ignore c 作者原话 -->", "<!-- lint-ignore -->", "<!-- lint-ignore c:   -->"} {
		res, ps := run(t, "套话。"+c, r)
		if len(res.Findings) != 1 || !slices.Equal(codes(ps), []string{"lint.invalid-waiver"}) {
			t.Errorf("%s: findings = %v, problems = %+v", c, summary(res.Findings), ps)
		}
	}
	// 出自 Agent 的段落中的豁免不生效。
	agent := func(int) string { return "agent" }
	res, ps = Run("套话。<!-- lint-ignore c: 理由 -->", r, "draft.md", Input{Origin: agent})
	if len(res.Findings) != 1 || !slices.Equal(codes(ps), []string{"lint.agent-waiver"}) {
		t.Fatalf("agent waiver: %v %+v", summary(res.Findings), ps)
	}
	edited := func(int) string { return "agent-draft-human-edited" }
	if res, _ := Run("套话。<!-- lint-ignore c: 理由 -->", r, "draft.md", Input{Origin: edited}); len(res.Waived) != 1 {
		t.Fatalf("edited waiver: %v", summary(res.Findings))
	}
}

func TestParamProblemLayers(t *testing.T) {
	r := rules(map[string]string{"x": "paragraph-length"}, map[string]map[string]any{"x": {"max": "many"}})
	r.Packs = []resolve.Ref{{ID: "house", Origin: "workspace", Path: ".writing-anything/packs/house.yaml"}}
	_, ps := run(t, "正文", r)
	if len(ps) != 1 || ps[0].Path != ".writing-anything/config.yaml" || !strings.Contains(ps[0].Message, "来自 project") {
		t.Fatalf("project layer: %+v", ps)
	}
	for source, want := range map[string][2]string{
		"pack:house":    {".writing-anything/packs/house.yaml", ""},
		"pack:default":  {"", "内置规范包 default"},
		"genre:minimal": {"", "内置文体 minimal"},
		"other":         {"", ""},
	} {
		if p, o := layerOf(r, source); p != want[0] || o != want[1] {
			t.Errorf("layerOf(%s) = %q, %q", source, p, o)
		}
	}
}

// 对抗性输入：大量首字符相同的词、大量需要去重的词、命中次数超过上限。
func TestAdversarialTerms(t *testing.T) {
	var terms []any
	for i := range 20000 {
		terms = append(terms, "词"+strconv.Itoa(i))
	}
	r := rules(map[string]string{"c": "ai-cliche"}, map[string]map[string]any{"c": {"terms": terms}})
	start := time.Now()
	res, ps := run(t, strings.Repeat("词", 600000), r)
	if d := time.Since(start); d > 5*time.Second || len(res.Findings) != 0 || len(ps) != 0 {
		t.Fatalf("shared first rune: %v, %d findings, %+v", d, len(res.Findings), ps)
	}
	dup := make([]any, 90000)
	for i := range dup {
		dup[i] = "x" + strconv.Itoa(i%3)
	}
	start = time.Now()
	if res, _ := run(t, "x0", rules(map[string]string{"c": "ai-cliche"}, map[string]map[string]any{"c": {"terms": dup}})); time.Since(start) > 5*time.Second || len(res.Findings) != 1 {
		t.Fatalf("dedupe: %v, %v", time.Since(start), summary(res.Findings))
	}
	// 命中超过 MaxHits 时截断，并给出 lint.truncated。
	res, ps = run(t, strings.Repeat("套话 ", MaxHits+10), rules(map[string]string{"c": "ai-cliche"}, map[string]map[string]any{"c": {"terms": []any{"套话"}}}))
	if len(res.Findings) != MaxHits || !slices.Equal(codes(ps), []string{"lint.truncated"}) {
		t.Fatalf("truncated: %d findings, %+v", len(res.Findings), ps)
	}
	// 重叠的词都被找到（自动机的输出链）。
	m := newMatcher([]string{"语言模型", "大语言模型", "模型"})
	hits, _ := m.find(prepare("大语言模型"))
	if len(hits) != 3 || hits[0].column != 1 || hits[1].column != 2 || hits[2].column != 4 {
		t.Fatalf("overlapping hits = %+v", hits)
	}
}

func TestCommentAndURLBounds(t *testing.T) {
	r := rules(map[string]string{"c": "ai-cliche"}, map[string]map[string]any{"c": {"terms": []any{"套话"}}})
	for _, tt := range []struct {
		name, content string
		want          int
	}{
		// 不在行首的 <!-- 不能跨越空行，与后面的注释配对。
		{"inline comment does not span paragraphs", "正文 <!-- 未闭合\n\n套话\n\n<!-- 注释 -->", 1},
		// 行首的 <!--（HTML 块）可以跨越空行。
		{"block comment spans paragraphs", "<!-- 说明\n\n套话\n\n-->\n套话", 1},
		{"url path with cjk is masked", "见 https://example.com/套话", 0},
		{"url ends at fullwidth punctuation", "见 https://example.com，套话", 1},
	} {
		if res, _ := run(t, tt.content, r); len(res.Findings) != tt.want {
			t.Errorf("%s: findings = %v, want %d", tt.name, summary(res.Findings), tt.want)
		}
	}
}

// 互为后缀的 ASCII 词不会让每个位置走完整条输出链；标准写法的覆盖范围不因命中上限截断。
func TestSuffixTermsAndCoverage(t *testing.T) {
	var terms []any
	for n := 1; n <= 1500; n++ {
		terms = append(terms, strings.Repeat("a", n))
	}
	r := rules(map[string]string{"c": "ai-cliche"}, map[string]map[string]any{"c": {"terms": terms}})
	start := time.Now()
	if res, _ := run(t, strings.Repeat("a", 2000000), r); time.Since(start) > 5*time.Second || len(res.Findings) != 0 {
		t.Fatalf("suffix terms: %v, %d findings", time.Since(start), len(res.Findings))
	}
	tc := rules(map[string]string{"terms": "term-consistency"}, nil)
	tc.Glossary = []resolve.Term{
		{Term: catalog.Term{Term: "规范包", Avoid: []string{"配置包"}}},
		{Term: catalog.Term{Term: "大语言模型", Avoid: []string{"语言模型"}}},
	}
	res, ps := run(t, strings.Repeat("规范包。", MaxHits)+"\n\n大语言模型很强。", tc)
	if len(res.Findings) != 0 || len(ps) != 0 {
		t.Fatalf("coverage truncated: %v %+v", summary(res.Findings), ps)
	}
	// 非 ASCII 标准写法互为后缀时，覆盖只取每个位置最长的词，耗时与正文长度成线性。
	var std []resolve.Term
	for n := 1; n <= 1500; n++ {
		std = append(std, resolve.Term{Term: catalog.Term{Term: strings.Repeat("禁", n)}})
	}
	std[0].Avoid = []string{"止"}
	tc.Glossary = std
	start = time.Now()
	if res, _ := run(t, strings.Repeat("禁", 600000)+"止", tc); time.Since(start) > 5*time.Second || len(res.Findings) != 1 {
		t.Fatalf("cjk suffix coverage: %v, %d findings", time.Since(start), len(res.Findings))
	}
	// 截断时保留按位置最前的命中：ASCII 词与非 ASCII 词混在同一行。
	mix := rules(map[string]string{"c": "ai-cliche"}, map[string]map[string]any{"c": {"terms": []any{"go", "套话"}}})
	res, _ = run(t, strings.Repeat("套话 go ", MaxHits), mix)
	if len(res.Findings) != MaxHits || res.Findings[0].Text != "套话" || res.Findings[1].Text != "go" || res.Findings[MaxHits-1].Column != (MaxHits/2-1)*6+4 {
		t.Fatalf("truncation order: first = %+v, last = %+v", res.Findings[:2], res.Findings[MaxHits-1])
	}
	// ASCII 标准写法同样按整词覆盖。
	tc.Glossary = []resolve.Term{{Term: catalog.Term{Term: "go-kit", Avoid: []string{"go"}}}}
	if res, _ := run(t, "go-kit 与 go", tc); !slices.Equal(summary(res.Findings), []string{"terms|1|10|go|go-kit"}) {
		t.Fatalf("ascii coverage: %v", summary(res.Findings))
	}
}

func TestCountTerms(t *testing.T) {
	if n := CountTerms("哈哈哈，总而言之。`总而言之`", []string{"总而言之", "总而言之", "哈哈"}); n != 3 {
		t.Fatalf("count = %d", n) // 「哈哈」在「哈哈哈」中重叠计 2 次，与 ai-cliche 规则相同；行内代码中的不计
	}
	if n := CountTerms("任何文字", nil); n != 0 {
		t.Fatalf("no terms = %d", n)
	}
}
