package export

import (
	"strings"
	"testing"
	"time"
)

// 纯文本（Plain Text）渲染：去掉 Markdown 标记，只保留文字；围栏代码块去掉围栏行、内容原样保留。
func TestRenderPlainText(t *testing.T) {
	cases := []struct{ name, in, want string }{
		{"heading", "## 二级标题\n正文。\n", "二级标题\n正文。"},
		{"emphasis-and-code", "**加粗** 与 *斜体* 与 __下划线__ 与 _另一处_ 与 `code`。\n", "加粗 与 斜体 与 下划线 与 另一处 与 code。"},
		{"underscore-in-word", "some_var_name 保持不变。", "some_var_name 保持不变。"},
		{"inline-code-verbatim", "行内 `a*b*c` 与 **b**。", "行内 a*b*c 与 b。"},
		{"links", "见 [链接文字](https://example.com/a) 与 [空文字]()。\n", "见 链接文字 与 空文字。"},
		{"image", "![店内实拍](shop.jpg)\n行内 ![图标](icon.png)。\n", "【图：店内实拍】\n行内 【图：图标】。"},
		{"quote", "> 引用第一行\n> 引用第二行\n>没有空格\n", "引用第一行\n引用第二行\n没有空格"},
		{"fence", "````markdown\n```go\nfmt.Println(\"hi\")\n```\n````\n", "```go\nfmt.Println(\"hi\")\n```"},
		{"comment", "文字 <!-- 注释 --> 后文\n单独的 <!-- 注释 -->\n", "文字  后文\n单独的"},
		{"comment-crosses-blank", "甲 <!-- 跨越\n\n空行 --> 乙\n", "甲 <!-- 跨越\n\n空行 --> 乙"},
		{"marker-only-line", "## \n###只有标题标记后无空格保留\n", "###只有标题标记后无空格保留"},
		{"escaped", "星号 \\* 不是斜体\\。\n", "星号 * 不是斜体\\。"},
		{"no-consecutive-blanks", "甲。\n\n<!-- @me: 已在 StripComments 去掉 -->\n\n乙。\n", "甲。\n\n乙。"},
		{"bom-crlf", "\uFEFF标题\r\n正文\r\n", "标题\n正文"},
	}
	for _, tc := range cases {
		if got := RenderPlainText(tc.in); got != tc.want {
			t.Errorf("%s: got %q, want %q", tc.name, got, tc.want)
		}
	}
	// 规模：100 万字符的正文在线性时间内完成。
	big := strings.Repeat("「说」**加粗**[链](u)`c`<!-- x -->", 40000)
	start := time.Now()
	if got := RenderPlainText(big); len(got) == 0 || time.Since(start) > 2*time.Second {
		t.Fatalf("scale: %d chars in %v", len(got), time.Since(start))
	}
}
