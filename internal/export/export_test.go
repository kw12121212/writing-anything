package export

import (
	"os"
	"path/filepath"
	"strings"
	"testing"
	"time"
)

// 去掉作者批注与单处豁免：独占一行时整行去掉，可以跨行但不能跨空行；围栏代码块（与文本检查相同的规则）、行内代码、
// 其他 HTML 注释与不满足边界的写法原样保留。
func TestStripComments(t *testing.T) {
	cases := []struct{ name, in, want string }{
		{"basic", "第一段。<!-- @me: 太长 -->\n\n<!-- lint-ignore ai-cliche: 引用原话 -->\n第二段 `<!-- @me: 代码 -->` 保留。\n\n<!-- @me(question):\n跨行的批注 -->\n<!-- 普通注释 -->\n",
			"第一段。\n\n第二段 `<!-- @me: 代码 -->` 保留。\n\n<!-- 普通注释 -->\n"},
		{"unclosed-keeps-paragraphs", "段落 A。<!-- @me: 这里太长\n\n段落 B 是作者的重要内容。\n\n段落 C。<!-- lint-ignore ai-cliche: 引用原话 -->\n",
			"段落 A。<!-- @me: 这里太长\n\n段落 B 是作者的重要内容。\n\n段落 C。\n"},
		{"nested", "甲 <!-- 外 <!-- @me: 内 --> 乙\n", "甲 <!-- 外  乙\n"},
		{"fence-length", "````markdown\n```html\n<!-- lint-ignore ai-cliche: demo -->\n<!-- @me: 演示 -->\n```\n````\n后文。<!-- @me: x -->\n",
			"````markdown\n```html\n<!-- lint-ignore ai-cliche: demo -->\n<!-- @me: 演示 -->\n```\n````\n后文。\n"},
		{"double-backticks", "``a`b`` 之后 <!-- @me: x --> 文字\n", "``a`b`` 之后  文字\n"},
		{"lookalikes", "<!-- @meta: x -->\n<!-- lint-ignore-all: y -->\n", "<!-- @meta: x -->\n<!-- lint-ignore-all: y -->\n"},
		{"no-double-blank", "甲。\n\n<!-- @me: 删掉 -->\n\n乙。\n", "甲。\n\n乙。\n"},
		{"leading-waiver-spans-blank", "<!-- lint-ignore ai-cliche: 这是受访者原话\n\n不能改 -->\n值得注意的是，这里引用原话。\n", "值得注意的是，这里引用原话。\n"},
		{"inline-waiver-no-blank", "正文 <!-- lint-ignore ai-cliche: 原话\n\n不能改 --> 后文\n", "正文 <!-- lint-ignore ai-cliche: 原话\n\n不能改 --> 后文\n"},
		{"no-trailing-space", "text <!-- @me: 删掉 -->\n", "text\n"},
		{"keeps-hard-break", "a <!-- @me: x --> b  \n第二行。\n", "a  b  \n第二行。\n"},
		{"waiver-with-backtick", "<!-- lint-ignore ai-cliche: 用 ` 号 --> 值得注意的是 `code` 这样。\n", " 值得注意的是 `code` 这样。\n"},
		{"bom-crlf", "\uFEFF标题\r\n<!-- @me: x -->\r\n正文\r\n", "标题\n正文\n"},
	}
	for _, tc := range cases {
		if got := string(StripComments([]byte(tc.in))); got != tc.want {
			t.Errorf("%s: got %q, want %q", tc.name, got, tc.want)
		}
	}
	// 规模：1 MB 单行中有 4 万个单处豁免，耗时与正文长度成线性。
	big := strings.Repeat("字<!-- lint-ignore x: y -->", 40000)
	start := time.Now()
	if got := StripComments([]byte(big)); len(got) != len(strings.Repeat("字", 40000)) || time.Since(start) > 2*time.Second {
		t.Fatalf("scale: %d bytes in %v", len(got), time.Since(start))
	}
}

// front matter 的标题取围栏之外的第一个一级标题；结尾的 # 只有与文字之间有空白时才是关闭序列。
func TestFirstHeading(t *testing.T) {
	for in, want := range map[string]string{
		"```bash\n# 安装依赖\n```\n# 真正的标题\n": "真正的标题",
		"# 从 Java 迁移到 C#\n":               "从 Java 迁移到 C#",
		"## 二级\n#  标题 ##\n":               "标题",
		"没有标题\n":                          "",
	} {
		if got := FirstHeading(in); got != want {
			t.Errorf("FirstHeading(%q) = %q, want %q", in, got, want)
		}
	}
}

// 输出路径必须在项目目录之内、不能覆盖工作区产物，途经的目录与输出文件不能是符号链接（validated-path-segments）。
func TestOutputPath(t *testing.T) {
	for _, bad := range []string{"/tmp/x.md", "../x.md", "a/../../x.md", ".", "dir/", `a\b.md`, ".writing-anything/config.yaml",
		".writing-anything/works/my-post/draft.md", ".writing-anything", ".writing-anything/exports", ".Writing-Anything/config.yaml", ".WRITING-ANYTHING/Works/x/draft.md"} {
		if _, p := cleanOutput(bad, CodeInvalidOutput); p == nil || p.Code != CodeInvalidOutput {
			t.Errorf("%q accepted", bad)
		}
	}
	for in, want := range map[string]string{"./out/../dist/post.md": "dist/post.md", ".writing-anything/exports/a/b.md": ".writing-anything/exports/a/b.md"} {
		if rel, p := cleanOutput(in, CodeInvalidOutput); p != nil || rel != want {
			t.Fatalf("clean(%q) = %q %v", in, rel, p)
		}
	}
	root := t.TempDir()
	if created, ps := ensureParents(root, "dist/blog/post.md", CodeInvalidOutput); len(ps) > 0 || len(created) != 2 {
		t.Fatalf("created: %v %v", created, ps)
	}
	// 按文件身份复查：即使路径绕过了字符串比较（不区分大小写的文件系统上换个大小写），途经工作区目录时也只能写到 exports 下。
	os.MkdirAll(filepath.Join(root, ".writing-anything", "works"), 0o755)
	for rel, ok := range map[string]bool{".writing-anything/config.yaml": false, ".writing-anything/works/x.md": false, ".writing-anything/exports/a/b.md": true, ".writing-anything/EXPORTS/c.md": false} {
		if _, ps := ensureParents(root, rel, CodeInvalidOutput); (len(ps) == 0) != ok {
			t.Errorf("ensureParents(%q) = %+v", rel, ps)
		}
	}
	outside := t.TempDir()
	if err := os.Symlink(outside, filepath.Join(root, "link")); err != nil {
		t.Skip("symlink not supported:", err)
	}
	if _, ps := ensureParents(root, "link/post.md", CodeInvalidOutput); len(ps) != 1 || ps[0].Code != CodeInvalidOutput || ps[0].Path != "link" {
		t.Fatalf("symlinked dir: %+v", ps)
	}
	os.Symlink(filepath.Join(outside, "f.md"), filepath.Join(root, "dist", "f.md"))
	if _, ps := ensureParents(root, "dist/f.md", CodeInvalidOutput); len(ps) != 1 {
		t.Fatalf("symlinked file: %+v", ps)
	}
}
