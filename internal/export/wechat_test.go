package export

import (
	"regexp"
	"slices"
	"strings"
	"testing"
	"time"

	"github.com/kw12121212/writing-anything/internal/catalog"
)

var tagRE = regexp.MustCompile(`<(/?)([A-Za-z][A-Za-z0-9]*)([^>]*)>`)

// CheckAllowed 报告 HTML 中不在允许列表中的标签与 style 以外的属性。
func checkAllowed(t *testing.T, html string) {
	t.Helper()
	for _, m := range tagRE.FindAllStringSubmatch(html, -1) {
		if !slices.Contains(AllowedTags, m[2]) {
			t.Errorf("tag %s not allowed", m[0])
		}
		if attrs := strings.TrimSpace(m[3]); attrs != "" && !regexp.MustCompile(`^style="[^"<>]*"$`).MatchString(attrs) {
			t.Errorf("attributes %q not allowed in %s", attrs, m[0])
		}
	}
}

func render(md string) (string, []Image) { return RenderWechat(md, catalog.DefaultHTMLTheme, "") }

func TestRenderBlocks(t *testing.T) {
	md := "# 一级\n\n## 二级 ##\n\n#### 四级\n\n###### 六级\n\n第一行\n第二行\nEnglish line\nnext  \n硬换行\\\n反斜杠换行\n\n" +
		"> 引用第一段\n> 继续\n>\n> 第二段\n\n- 甲\n  续行\n- 乙\n\n- 丙\n\n1. 一\n2) 二\n\n---\n\n* * *\n\n```go\nfunc main() {\n\t<b>\n}\n```\n\n~~~\n未闭合"
	html, images := render(md)
	checkAllowed(t, html)
	for _, want := range []string{
		"<h1 style=", ">一级</h1>", ">二级</h2>", ">四级</h4>", "<h4 style=\"" + catalog.DefaultHTMLTheme["h4"] + "\">六级</h4>",
		">第一行第二行 English line next<br>硬换行<br>反斜杠换行</p>",
		"<blockquote style=", "引用第一段继续</p>", ">第二段</p></blockquote>",
		"<ul style=", "<li style=\"" + catalog.DefaultHTMLTheme["li"] + "\">甲续行</li>", ">乙</li>", ">丙</li></ul>",
		"<ol style=", ">一</li>", ">二</li></ol>",
		"<hr style=", "<pre style=", "func main() {\n\t&lt;b&gt;\n}</pre>", "未闭合</pre>",
	} {
		if !strings.Contains(html, want) {
			t.Errorf("html lacks %q:\n%s", want, html)
		}
	}
	if strings.Count(html, "<hr") != 2 || len(images) != 0 || !strings.HasPrefix(html, "<section style=") || !strings.HasSuffix(html, "</section>\n") {
		t.Fatalf("structure: %s", html)
	}
}

func TestRenderInline(t *testing.T) {
	md := "**加粗** 与 __加粗__，*斜体* 与 _斜体_，snake_case_name 不是斜体，`a < b` 与 `` `x` ``。\n\n" +
		"<script>alert(1)</script> & \"引号\" <!-- 注释 --> 与 `<!-- 代码中的注释 -->`\n\n" +
		"见[文档](https://go.dev/doc \"标题\")和[同一个](https://go.dev/doc)，以及[另一个](https://x.cn)。\\*不是斜体\\* \\[不是链接](x)\n\n" +
		"行内![图标](icon.png)图片。\n\n![店内实拍](shop.jpg \"周六的店\")\n\n![无标题](a.png)\n\n<!-- 整段注释 -->"
	html, images := render(md)
	checkAllowed(t, html)
	for _, want := range []string{
		"<strong style=", ">加粗</strong> 与 <strong", "<em style=", ">斜体</em> 与 <em", "snake_case_name 不是斜体",
		">a &lt; b</code>", ">`x`</code>",
		"&lt;script&gt;alert(1)&lt;/script&gt; &amp; &#34;引号&#34;  与 <code", "&lt;!-- 代码中的注释 --&gt;</code>",
		"见文档<sup style=", ">[1]</sup>和同一个<sup", ">[1]</sup>，以及另一个<sup", ">[2]</sup>", "*不是斜体* [不是链接](x)",
		"行内【配图 1：图标】图片", ">【配图 2】店内实拍</p><p style=\"" + catalog.DefaultHTMLTheme["caption"] + "\">周六的店</p></section>",
		">【配图 3】无标题</p>", ">a.png</p>",
		"参考链接<br>[1] https://go.dev/doc<br>[2] https://x.cn</p>",
	} {
		if !strings.Contains(html, want) {
			t.Errorf("html lacks %q:\n%s", want, html)
		}
	}
	if strings.Contains(html, "注释 --") && !strings.Contains(html, "代码中的注释") || strings.Contains(html, "整段注释") || strings.Contains(html, "<script") {
		t.Errorf("comments or script leaked:\n%s", html)
	}
	if len(images) != 3 || images[1] != (Image{2, "店内实拍", "shop.jpg", "周六的店"}) || images[0].Src != "icon.png" {
		t.Fatalf("images: %+v", images)
	}
}

func TestRenderTheme(t *testing.T) {
	theme := map[string]string{"wrapper": "color: #111", "p": "margin: 0"}
	html, _ := RenderWechat("段落", theme, "")
	if html != "<section style=\"color: #111\"><p style=\"margin: 0\">段落</p></section>\n" {
		t.Fatalf("theme: %q", html)
	}
	if ImageListPath("dist/a.html") != "dist/a.images.md" || ImageListPath("dist/a") != "dist/a.images.md" {
		t.Fatal("image list path")
	}
}

// 样式主题中的不安全取值不输出；任何取值都不能让输出出现允许列表之外的标签或 style 之外的属性。
func TestRenderUnsafeTheme(t *testing.T) {
	md := "**a** 与 **b**，*c _d_ e*，`code`，[链接](https://x.cn)\n\n## 标题\n\n![图](a.png)\n\n> 引用\n\n- 项\n\n```\nx\n```\n\n---\n"
	for _, v := range []string{
		"width: calc(1em*2)", "color: red onclick=alert(1) x", "font-family: $1", `color: red" onclick="x`, "color: red>",
		"background: url(x.png)", "background: URL(x.png)", "width: expression(1)", "color: javascript", "color: red\ue102",
		"color: red; /* x */", "color: @red", "color: red\nfont: x", "",
	} {
		theme := map[string]string{}
		for _, k := range catalog.HTMLThemeKeys {
			theme[k] = v
		}
		html, _ := RenderWechat(md, theme, "")
		checkAllowed(t, html)
		if catalog.SafeCSS(v) {
			t.Errorf("%q should be unsafe", v)
		}
		if strings.Contains(html, "onclick") || strings.Contains(html, "url(") || strings.Contains(html, "$1") || !strings.Contains(html, `<strong style="">a</strong>`) {
			t.Errorf("%q leaked into:\n%s", v, html)
		}
	}
	for _, v := range catalog.DefaultHTMLTheme {
		if !catalog.SafeCSS(v) {
			t.Errorf("default %q is not safe", v)
		}
	}
}

func TestRenderEdgeCases(t *testing.T) {
	html, images := render("### 三级\n\n___\n\n**a _b** c_ 与 _a_ _b_ 与 中_文_字\n\n私用区\ue100\ue102\ue25f字符 `\ue102`\n\n" +
		"[t](x\\>y) ![店内\n实拍\\_图](x\\_y.png \"周六\n的店\")\n\n![跨行\n替代](a.png)\n\n<!-- 跨越\n\n空行的注释 -->")
	checkAllowed(t, html)
	for _, want := range []string{">三级</h3>", "<hr style=", "<strong style=", ">a _b</strong> c_", "<em style=\"font-style: italic\">a</em> <em style=\"font-style: italic\">b</em>",
		"中_文_字", "私用区\ue100\ue102\ue25f字符", ">\ue102</code>", "[1] x&gt;y", "【配图 1：店内 实拍_图】", "【配图 2】跨行 替代", "&lt;!-- 跨越</p>"} {
		if !strings.Contains(html, want) {
			t.Errorf("html lacks %q:\n%s", want, html)
		}
	}
	if strings.Contains(html, "&amp;gt;") {
		t.Errorf("url escaped twice:\n%s", html)
	}
	if len(images) != 2 || images[0] != (Image{1, "店内 实拍_图", "x_y.png", "周六 的店"}) {
		t.Fatalf("images: %+v", images)
	}
}

// 超长、嵌套很深或标记很多的正文在线性时间内完成（workspace-artifacts/input-limits）。
func TestRenderLargeInputs(t *testing.T) {
	const size = 2 << 20
	for name, md := range map[string]string{
		"one paragraph":  strings.Repeat("ab\n", size/3),
		"nested quotes":  strings.Repeat(">", size),
		"many links":     strings.Repeat("[a](https://x.cn/", 1) + strings.Repeat("[t](u) ", size/7),
		"open brackets":  strings.Repeat("[", size),
		"stars":          strings.Repeat("*a ", size/3),
		"underscores":    strings.Repeat("_", size),
		"nested stars":   strings.Repeat("*a ", 50000) + strings.Repeat("b* ", 50000),
		"long link text": strings.Repeat("["+strings.Repeat("x", 999)+"](", size/1002),
	} {
		start := time.Now()
		html, _ := render(md)
		if d := time.Since(start); d > 10*time.Second {
			t.Errorf("%s took %v", name, d)
		} else {
			t.Logf("%s: %v", name, d)
		}
		if !strings.HasPrefix(html, "<section") {
			t.Errorf("%s: %.40s", name, html)
		}
	}
}
