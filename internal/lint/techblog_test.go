package lint

import (
	"fmt"
	"slices"
	"strings"
	"testing"
	"time"

	"github.com/kw12121212/writing-anything/internal/catalog"
)

func findingSummary(fs []Finding) []string {
	var out []string
	for _, f := range fs {
		out = append(out, fmt.Sprintf("%d:%d:%s", f.Line, f.Column, f.Text))
	}
	return out
}

// 代码块必须标注语言（code-block-language）。
func TestCodeBlockLanguage(t *testing.T) {
	r := rules(map[string]string{"code": "code-block-language"}, nil)
	content := "正文。\n\n```\nfmt.Println()\n```\n\n```go\nx := 1\n```\n\n~~~ \ny\n~~~\n\n  ```text\nz\n  ```\n"
	res, _ := Run(content, r, "draft.md", Input{})
	if got := strings.Join(findingSummary(res.Findings), "|"); got != "3:1:```|11:1:~~~" {
		t.Fatalf("findings = %q", got)
	}
	if !strings.Contains(res.Findings[0].Message, "```go") {
		t.Errorf("message: %s", res.Findings[0].Message)
	}
}

// 链接格式（link-format）：链接文字、地址、空白、协议与主机名；行内代码与围栏中的不检查。
func TestLinkFormat(t *testing.T) {
	r := rules(map[string]string{"links": "link-format"}, nil)
	content := strings.Join([]string{
		"好的 [文档](https://go.dev/doc) 与 [相对](../a.md) 与 [锚点](#x) 与 ![](img.png) 与 [标题](https://a.b \"T\") 与 [尖括号](<a b.md>)。",
		"[](https://go.dev) 与 [空]() 与 [空格](a b.md) 与 [坏](javascript:void) 与 [无主机](https:///x) 与 [协议](ftp://a.b)。",
		"裸链接 https:// 与 https://go.dev/x，`[](x)` 不算。",
		"```", "[](x)", "```",
	}, "\n")
	res, _ := Run(content, r, "draft.md", Input{})
	var msgs []string
	for _, f := range res.Findings {
		msgs = append(msgs, fmt.Sprintf("%d:%s", f.Line, string([]rune(strings.TrimPrefix(f.Message, "链接格式："))[:5])))
	}
	want := []string{"2:链接文字为", "2:链接地址为", "2:链接地址中", "2:不允许的协", "2:链接地址缺", "2:不允许的协", "3:URL 缺"}
	if strings.Join(msgs, "|") != strings.Join(want, "|") {
		t.Fatalf("findings = %q\nwant %q", msgs, want)
	}
	// schemes 参数：允许 ftp。
	r = rules(map[string]string{"links": "link-format"}, map[string]map[string]any{"links": {"schemes": []any{"https", "ftp"}}})
	if res, _ := Run("[协议](ftp://a.b) 与 [邮件](mailto:a@b.c)。", r, "draft.md", Input{}); len(res.Findings) != 1 || !strings.Contains(res.Findings[0].Message, "mailto") {
		t.Fatalf("schemes: %+v", res.Findings)
	}
}

// 版本号与 API 说法需要 reference 素材的论断覆盖（reference-sources）。
func TestReferenceSources(t *testing.T) {
	r := rules(map[string]string{"ref": "reference-sources"}, nil)
	content := "从 Go 1.22 开始，循环变量每轮重新创建；调用 `http.Get(url)` 会复用连接，`x` 不算。\n\n提升了 1.5 倍，见 v2.3.1 发布说明。\n\n```go\nGo 1.21 `os.Open()`\n```\n"
	res, _ := Run(content, r, "draft.md", Input{Unit: "my-post"})
	if got := strings.Join(findingSummary(res.Findings), "|"); got != "1:3:Go 1.22|1:29:http.Get(url)|3:13:v2.3.1" {
		t.Fatalf("findings = %q", got)
	}
	if !strings.Contains(res.Findings[0].Message, "wa-cli claim add my-post") || !strings.Contains(res.Findings[1].Message, "API 说法") {
		t.Fatalf("message: %+v", res.Findings)
	}
	// 关联 reference 素材的论断覆盖提及处时不报告；只关联其他素材的论断不算；参数可以关闭检测。
	in := Input{Claims: []Claim{
		{Kind: "fact", Text: "从 Go 1.22 开始，循环变量每轮重新创建", Sources: []ClaimSource{{"doc", "reference"}}},
		{Kind: "fact", Text: "调用 `http.Get(url)` 会复用连接", Sources: []ClaimSource{{"note", "author-note"}}},
	}}
	if res, _ := Run(content, r, "draft.md", in); len(res.Findings) != 2 || res.Findings[0].Text != "http.Get(url)" {
		t.Fatalf("covered: %+v", res.Findings)
	}
	r = rules(map[string]string{"ref": "reference-sources"}, map[string]map[string]any{"ref": {"versions": false, "source_types": []any{"author-note"}}})
	if res, _ := Run(content, r, "draft.md", in); len(res.Findings) != 0 {
		t.Fatalf("params: %+v", res.Findings)
	}
	if res, _ := Run(content, rules(map[string]string{"ref": "reference-sources"}, nil), "draft.md", Input{ClaimsInvalid: true}); len(res.Findings) != 0 {
		t.Fatal("invalid claims must skip the rule")
	}
}

// HTML 注释（屏蔽区域）中的链接与 API 说法不检查；source_types 为空数组时报告参数问题而不是崩溃。
func TestTechBlogMaskedAndParams(t *testing.T) {
	r := rules(map[string]string{"links": "link-format", "ref": "reference-sources"}, nil)
	content := "<!-- @me: 这里的 [链接]() 要补地址，`http.Get(x)` 也是 -->\n正文 <!--\n多行注释 [](x)\n--> 结束。\n"
	if res, _ := Run(content, r, "draft.md", Input{}); len(res.Findings) != 0 {
		t.Fatalf("masked: %+v", res.Findings)
	}
	r = rules(map[string]string{"ref": "reference-sources"}, map[string]map[string]any{"ref": {"source_types": []any{}}})
	res, ps := Run("从 Go 1.22 开始。", r, "draft.md", Input{})
	if len(res.Findings) != 0 || len(ps) != 1 || ps[0].Code != CodeInvalidParams {
		t.Fatalf("empty source_types: %+v %+v", res.Findings, ps)
	}
}

// 规模：三种规则在 2 MB 的正文上耗时与正文长度成线性（含大量反引号段、链接与版本号）。
func TestTechBlogScale(t *testing.T) {
	r := rules(map[string]string{"code": "code-block-language", "links": "link-format", "ref": "reference-sources"}, nil)
	var b strings.Builder
	for b.Len() < MaxBytes-200 {
		b.WriteString("见 [文档](https://a.b/c) 与 `x()` ``y`` Go 1.22 ")
	}
	start := time.Now()
	res, _ := Run(b.String(), r, "draft.md", Input{})
	if d := time.Since(start); d > 5*time.Second || len(res.Findings) == 0 {
		t.Fatalf("scale: %v, %d findings", d, len(res.Findings))
	}
}

// 合并校验认可的规则类型与规则引擎实现的一致：两边漂移时规则会通过校验却从不执行。
func TestRuleTypesMatchCatalog(t *testing.T) {
	var impl []string
	for k := range ruleTypes {
		impl = append(impl, k)
	}
	slices.Sort(impl)
	known := slices.Clone(catalog.RuleTypes)
	slices.Sort(known)
	if !slices.Equal(impl, known) {
		t.Fatalf("lint rule types %v != catalog.RuleTypes %v", impl, known)
	}
}
