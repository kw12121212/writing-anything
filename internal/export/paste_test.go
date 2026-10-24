package export

import (
	"bytes"
	"crypto/sha256"
	"encoding/base64"
	"image"
	"image/png"
	"regexp"
	"slices"
	"strings"
	"testing"
	"time"

	"github.com/kw12121212/writing-anything/internal/catalog"
	"github.com/kw12121212/writing-anything/internal/illustration"
)

// pastePNG 返回一张 8×4 的 PNG，末尾补 pad 个字节（文件头仍可识别）。
func pastePNG(t *testing.T, pad int) []byte {
	t.Helper()
	var b bytes.Buffer
	if err := png.Encode(&b, image.NewRGBA(image.Rect(0, 0, 8, 4))); err != nil {
		t.Fatal(err)
	}
	return append(b.Bytes(), make([]byte, pad)...)
}

// renderPaste 用 embedder 渲染正文片段。
func renderPaste(md string, theme map[string]string, assets []Asset, occ []occurrence) string {
	tree, _ := renderTree(md, theme, "", embedder(assets, occ))
	var b bytes.Buffer
	if err := page.ExecuteTemplate(&b, "block", tree); err != nil {
		panic(err)
	}
	return b.String()
}

var pasteImgRE = regexp.MustCompile(`^src="data:image/(png|jpeg);base64,[A-Za-z0-9+/=&#;]*" alt="[^"<>]*" style="[^"<>]*"$`)

// checkPasteAllowed 断言片段只含公众号 HTML 的允许标签与 img，img 只带 data URI 的 src、alt 与 style。
func checkPasteAllowed(t *testing.T, html string) {
	t.Helper()
	for _, m := range tagRE.FindAllStringSubmatch(html, -1) {
		attrs := strings.TrimSpace(m[3])
		switch {
		case m[2] == "img":
			if !pasteImgRE.MatchString(attrs) {
				t.Errorf("img attributes %q not allowed", attrs)
			}
		case !slices.Contains(AllowedTags, m[2]):
			t.Errorf("tag %s not allowed", m[0])
		case attrs != "" && !regexp.MustCompile(`^style="[^"<>]*"$`).MatchString(attrs):
			t.Errorf("attributes %q not allowed in %s", attrs, m[0])
		}
	}
}

// 独占一段的正文配图内嵌，行内图片、封面、非图片字节与外链保留占位；同一张图重复出现只计一次预算（paste-html-export）。
func TestEmbedderChoices(t *testing.T) {
	img := pastePNG(t, 0)
	assets := []Asset{
		{ID: "cover", Role: illustration.RoleCover, data: img},
		{ID: "fig", Role: illustration.RoleBody, data: img},
		{ID: "bad", Role: illustration.RoleBody, data: []byte("not an image")},
	}
	occ := []occurrence{
		{"illustrations/fig.png", 1}, {"illustrations/fig.png", 1}, {"illustrations/cover.png", 0},
		{"illustrations/bad.png", 2}, {"https://example.com/x.png", -1}, {"illustrations/fig.png", 1},
	}
	md := "![甲](illustrations/fig.png \"图说\")\n\n行内 ![乙](illustrations/fig.png)。\n\n![封](illustrations/cover.png)\n\n" +
		"![坏](illustrations/bad.png)\n\n![外](https://example.com/x.png)\n\n![丙](<illustrations/fig.png>)\n"
	html := renderPaste(md, catalog.DefaultHTMLTheme, assets, occ)
	checkPasteAllowed(t, html)
	uri := "data:image/png;base64," + base64.StdEncoding.EncodeToString(img)
	if n := strings.Count(html, "<img "); n != 2 {
		t.Errorf("img count = %d:\n%s", n, html)
	}
	for _, want := range []string{`alt="甲"`, ">图说</p>", `alt="丙"`, ">丙</p>", "【配图 2：乙】", "【配图 3】封", "【配图 4】坏", "【配图 5】外"} {
		if !strings.Contains(html, want) {
			t.Errorf("missing %q in:\n%s", want, html)
		}
	}
	if strings.Contains(html, "example.com/x.png\"") || !strings.Contains(html, strings.ReplaceAll(uri, "+", "&#43;")) {
		t.Errorf("unexpected src:\n%s", html)
	}
}

// 内嵌按内嵌后的字节累计，超出 PasteEmbedBudget 后改为占位（paste-embed-budget）：33 张略小于 1 MiB 的图，前 32 张内嵌。
func TestEmbedderBudget(t *testing.T) {
	var assets []Asset
	var occ []occurrence
	var md strings.Builder
	img := pastePNG(t, PasteImageMaxBytes-200)
	for i := 0; i < 33; i++ {
		id := "f" + itoa(i+1)
		assets = append(assets, Asset{ID: id, Role: illustration.RoleBody, data: append([]byte(nil), img...)})
		occ = append(occ, occurrence{"illustrations/" + id + ".png", i})
		md.WriteString("![" + id + "](illustrations/" + id + ".png)\n\n")
	}
	html := renderPaste(md.String(), catalog.DefaultHTMLTheme, assets, occ)
	if strings.Count(html, "<img ") != 32 || !strings.Contains(html, "【配图 33】f33") {
		t.Errorf("budget not applied: %d images", strings.Count(html, "<img "))
	}
}

// 对抗性的样式取值不进入可粘贴片段（与公众号 HTML 的允许列表相同，另加 img）。
func TestPasteUnsafeTheme(t *testing.T) {
	img := pastePNG(t, 0)
	assets := []Asset{{ID: "fig", Role: illustration.RoleBody, data: img}}
	md := "**a**，[链接](https://x.cn)\n\n![图](illustrations/fig.png)\n"
	for _, v := range []string{"color: red onclick=alert(1) x", `color: red" onclick="x`, "background: url(x.png)", "font-family: $1"} {
		theme := map[string]string{}
		for _, k := range catalog.HTMLThemeKeys {
			theme[k] = v
		}
		html := renderPaste(md, theme, assets, []occurrence{{"illustrations/fig.png", 0}})
		checkPasteAllowed(t, html)
		if strings.Contains(html, "onclick") || strings.Contains(html, "url(") || strings.Contains(html, "$1") || strings.Count(html, "<img ") != 1 {
			t.Errorf("%q leaked into:\n%s", v, html)
		}
	}
}

// 2 MiB 的对抗性输入在有内嵌判定时同样在线性时间内完成。
func TestPasteLargeInputs(t *testing.T) {
	const size = 2 << 20
	img := pastePNG(t, 0)
	assets := []Asset{{ID: "fig", Role: illustration.RoleBody, data: img}}
	for name, md := range map[string]string{
		"many images":      strings.Repeat("![a](illustrations/fig.png)\n\n", size/30),
		"unmatched images": strings.Repeat("![a](illustrations/x.png) ", size/27),
		"open brackets":    strings.Repeat("![", size/2),
	} {
		var occ []occurrence
		for i := 0; i < size/30; i++ {
			occ = append(occ, occurrence{"illustrations/fig.png", 0})
		}
		start := time.Now()
		html := renderPaste(md, catalog.DefaultHTMLTheme, assets, occ)
		if d := time.Since(start); d > 10*time.Second {
			t.Errorf("%s took %v", name, d)
		}
		if !strings.HasPrefix(html, "<section") {
			t.Errorf("%s: %.40s", name, html)
		}
	}
}

// 页面的复制脚本内容固定，CSP 按其哈希放行。
func TestPasteScriptHash(t *testing.T) {
	sum := sha256.Sum256([]byte(pasteScript))
	if !strings.Contains(pasteCSP, "script-src 'sha256-"+base64.StdEncoding.EncodeToString(sum[:])+"'") || !strings.Contains(pasteCSP, "img-src data:") ||
		!strings.HasPrefix(pasteCSP, "default-src 'none'") {
		t.Errorf("csp = %s", pasteCSP)
	}
	var b bytes.Buffer
	tree, _ := renderTree("正文。", catalog.DefaultHTMLTheme, "", nil)
	if err := pastePage.ExecuteTemplate(&b, "paste", struct {
		Title    string
		Segments []pasteSegment
	}{"标题 <b>", segment(tree)}); err != nil {
		t.Fatal(err)
	}
	if !strings.Contains(b.String(), "<script>"+pasteScript+"</script>") || !strings.Contains(b.String(), "<title>标题 &lt;b&gt;</title>") {
		t.Errorf("page:\n%s", b.String())
	}
}

// segmentFixture 返回 n 张独占一段的可内嵌配图的正文与资产。
func segmentFixture(t *testing.T, n int, prefix string) (string, []Asset, []occurrence) {
	t.Helper()
	img := pastePNG(t, 0)
	var assets []Asset
	var occ []occurrence
	var md strings.Builder
	md.WriteString(prefix)
	for i := 0; i < n; i++ {
		id := "f" + itoa(i+1)
		assets = append(assets, Asset{ID: id, Role: illustration.RoleBody, data: img})
		occ = append(occ, occurrence{"illustrations/" + id + ".png", i})
		md.WriteString("第 " + itoa(i+1) + " 段。\n\n![" + id + "](illustrations/" + id + ".png)\n\n")
	}
	md.WriteString("见[文档](https://x.cn/doc)。\n")
	return md.String(), assets, occ
}

// 超过 7 张时按顶层块分段：9 张为 7+2，参考链接在最后一段；不超过 7 张时只有一段且输出与不分段相同（paste-segments）。
func TestSegmentByImages(t *testing.T) {
	md, assets, occ := segmentFixture(t, 9, "")
	tree, _ := renderTree(md, catalog.DefaultHTMLTheme, "", embedder(assets, occ))
	segs := segment(tree)
	if len(segs) != 2 || countImages(segs[0].Root) != 7 || countImages(segs[1].Root) != 2 {
		t.Fatalf("segments = %d", len(segs))
	}
	if segs[0].Label != "复制第 1 段（图 1–7）" || segs[1].Label != "复制第 2 段（图 8–9）" || segs[0].ID != "wa-seg-1" || segs[1].ID != "wa-seg-2" {
		t.Errorf("labels = %q %q", segs[0].Label, segs[1].Label)
	}
	last := segs[1].Root.Children[len(segs[1].Root.Children)-1]
	if last.Kind != "references" || segs[0].Root.Style != tree.Style {
		t.Errorf("tail = %s, style = %q", last.Kind, segs[0].Root.Style)
	}
	// 7 张：一段，输出与整棵树相同。
	md, assets, occ = segmentFixture(t, 7, "")
	tree, _ = renderTree(md, catalog.DefaultHTMLTheme, "", embedder(assets, occ))
	segs = segment(tree)
	var whole, one bytes.Buffer
	page.ExecuteTemplate(&whole, "block", tree)
	page.ExecuteTemplate(&one, "block", segs[0].Root)
	if len(segs) != 1 || whole.String() != one.String() {
		t.Errorf("single segment differs (%d)", len(segs))
	}
	// 没有图像的正文只有一段，按钮文字不影响单段页面。
	tree, _ = renderTree("只有文字。\n", catalog.DefaultHTMLTheme, "", embedder(nil, nil))
	if segs := segment(tree); len(segs) != 1 || countImages(segs[0].Root) != 0 {
		t.Errorf("text-only segments = %d", len(segs))
	}
}

// 单个顶层块的图像超过上限时独占一段，其前后的块各自成段。
func TestSegmentOversizedBlock(t *testing.T) {
	img := pastePNG(t, 0)
	var assets []Asset
	var occ []occurrence
	var q strings.Builder
	for i := 0; i < 8; i++ {
		id := "q" + itoa(i+1)
		assets = append(assets, Asset{ID: id, Role: illustration.RoleBody, data: img})
		occ = append(occ, occurrence{"illustrations/" + id + ".png", i})
		q.WriteString("> ![" + id + "](illustrations/" + id + ".png)\n>\n")
	}
	assets = append(assets, Asset{ID: "a", Role: illustration.RoleBody, data: img})
	occ = append([]occurrence{{"illustrations/a.png", 8}}, occ...)
	md := "![a](illustrations/a.png)\n\n" + q.String() + "\n结尾。\n"
	tree, _ := renderTree(md, catalog.DefaultHTMLTheme, "", embedder(assets, occ))
	segs := segment(tree)
	if len(segs) != 2 || countImages(segs[0].Root) != 1 || countImages(segs[1].Root) != 8 {
		t.Fatalf("segments = %d", len(segs))
	}
	// 没有图像的结尾归入最后一段，不另起一段。
	if tail := segs[1].Root.Children[len(segs[1].Root.Children)-1]; tail.Kind != "p" {
		t.Errorf("tail = %s", tail.Kind)
	}
	if segs[1].Label != "复制第 2 段（图 2–9）" {
		t.Errorf("label = %q", segs[1].Label)
	}
}
