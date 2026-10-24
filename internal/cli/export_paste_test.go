package cli

import (
	"bytes"
	"encoding/base64"
	"encoding/binary"
	"encoding/json"
	"hash/crc32"
	"image"
	"image/jpeg"
	"image/png"
	"os"
	"path/filepath"
	"regexp"
	"slices"
	"strconv"
	"strings"
	"testing"

	"github.com/kw12121212/writing-anything/internal/export"
)

var pasteImgAttrs = regexp.MustCompile(`^src="data:image/(png|jpeg);base64,[A-Za-z0-9+/=&#;]*" alt="[^"<>]*" style="[^"<>]*"$`)

// allowedPasteHTML 断言可粘贴正文片段只含公众号 HTML 的允许标签与 img（只带 data URI 的 src、alt 与 style）。
func allowedPasteHTML(t *testing.T, html string) {
	t.Helper()
	allowed := []string{"section", "h1", "h2", "h3", "h4", "p", "strong", "em", "code", "pre", "blockquote", "ul", "ol", "li", "hr", "br", "sup"}
	for _, m := range htmlTagRE.FindAllStringSubmatch(html, -1) {
		a := strings.TrimSpace(m[3])
		switch {
		case m[2] == "img":
			if !pasteImgAttrs.MatchString(a) {
				t.Errorf("img attributes %q not allowed", a)
			}
		case !slices.Contains(allowed, m[2]):
			t.Errorf("tag %s not allowed", m[0])
		case a != "" && !regexp.MustCompile(`^style="[^"<>]*"$`).MatchString(a):
			t.Errorf("attributes %q not allowed in %s", a, m[0])
		}
	}
	for _, bad := range []string{"<script", "<style", "<link", "<html", "class=", " id=", "href=", "onclick"} {
		if strings.Contains(html, bad) {
			t.Errorf("fragment contains %q", bad)
		}
	}
}

// paste-embedded
func TestExportPasteEmbedded(t *testing.T) {
	dir := illustratedAccepted(t, "# 周六的店\n\n店里新来了一台烘豆机。\n\n![连接池示意](illustrations/shop.jpg \"连接池\")\n", true, addShop, addCover)
	code, res := runExportJSON(t, dir, "my-post", "--format", "paste-html")
	const rel = exportsDir + "/my-post.paste.html"
	if code != 0 || res.Export.Path == nil || *res.Export.Path != rel || res.Export.HTML != nil || res.Export.Format != "paste-html" {
		t.Fatalf("export: %d %+v", code, res)
	}
	page := readFile(t, wsPath(dir, rel))
	if strings.Count(page, "<img ") != 1 || !strings.Contains(page, `<img src="data:image/jpeg;base64,`) || !strings.Contains(page, `alt="连接池示意"`) ||
		!strings.Contains(page, ">连接池</p>") || strings.Contains(page, "【配图 1】") {
		t.Errorf("page images:\n%s", page)
	}
	d := res.Export.Disclosure
	if d == nil || d.Placement != "both" || d.AIImages != 2 || strings.Contains(page, d.Text) || strings.Contains(page, "AI 辅助") {
		t.Errorf("disclosure = %+v", d)
	}
	for _, p := range []string{rel, exportsDir + "/my-post.paste.images.md", exportsDir + "/my-post.paste.assets/my-post.cover.jpg", exportsDir + "/my-post.paste.assets/my-post.shop.jpg"} {
		if !slices.Contains(res.ChangedPaths, p) {
			t.Errorf("changedPaths %v lacks %s", res.ChangedPaths, p)
		}
	}
	if as := res.Export.Assets; len(as) != 2 || as[0].ID != "cover" || as[1].ID != "shop" {
		t.Errorf("assets = %+v", as)
	}
	list := readFile(t, wsPath(dir, exportsDir+"/my-post.paste.images.md"))
	if !strings.HasPrefix(list, "# 配图清单\n\n- 封面：周六的店；文件：my-post.paste.assets/my-post.cover.jpg；AI 生成\n") {
		t.Errorf("image list:\n%s", list)
	}
	// 页面的正文容器只含片段：按钮与说明在容器之外，且不可选中。
	start := strings.Index(page, `<div id="wa-article">`)
	if start < 0 || strings.Index(page, `id="wa-copy"`) > start || !strings.Contains(page, "user-select: none") {
		t.Errorf("layout:\n%s", page)
	}
	// 不加 --json 时说明路径与资产目录。
	if code, stdout, _ := run(t, "export", "my-post", "--format", "paste-html", "--dir", dir); code != 0 ||
		!strings.Contains(stdout, rel) || !strings.Contains(stdout, exportsDir+"/my-post.paste.assets") ||
		!strings.Contains(stdout, "AI 参与说明（可粘贴页不含，需要时在发布平台自行声明）：本文在 AI 辅助下完成") ||
		!strings.Contains(stdout, "配图清单（1 张）："+exportsDir+"/my-post.paste.images.md") {
		t.Errorf("text output: %d %s", code, stdout)
	}
}

// paste-safe-page
func TestExportPasteSafePage(t *testing.T) {
	scripts := map[string]bool{}
	for _, body := range []string{wechatDraft, "# 另一篇\n\n完全不同的正文。\n"} {
		dir := acceptedWork(t, body)
		if code, res := runExportJSON(t, dir, "my-post", "--format", "paste-html"); code != 0 {
			t.Fatalf("export: %d %+v", code, res)
		}
		page := readFile(t, wsPath(dir, exportsDir+"/my-post.paste.html"))
		if strings.Count(page, "<script") != 1 {
			t.Fatalf("script count:\n%s", page)
		}
		scripts[page[strings.Index(page, "<script>"):strings.Index(page, "</script>")]] = true
		if !strings.Contains(page, `http-equiv="Content-Security-Policy"`) || !strings.Contains(page, "default-src &#39;none&#39;") ||
			!strings.Contains(page, "img-src data:") || !strings.Contains(page, "script-src &#39;sha256-") {
			t.Errorf("csp missing:\n%s", page)
		}
		if regexp.MustCompile(`(src|href)="https?://`).MatchString(page) || strings.Contains(page, "<link") {
			t.Errorf("external resource:\n%s", page)
		}
		if body != wechatDraft {
			continue
		}
		if !strings.Contains(page, "&lt;script&gt;alert(1)&lt;/script&gt;") || !strings.Contains(page, "【配图 2：图标】") ||
			!strings.Contains(page, "【配图 1】店内实拍") || !strings.Contains(page, "[1]") || !strings.Contains(page, "https://example.com/day") {
			t.Errorf("content:\n%s", page)
		}
	}
	if len(scripts) != 1 {
		t.Errorf("script differs between exports: %d variants", len(scripts))
	}
}

// writePaddedPNG 写一张末尾补 pad 个字节的 8×4 PNG（文件头仍可识别）。
func writePaddedPNG(t *testing.T, path string, pad int, shade byte) {
	t.Helper()
	var b bytes.Buffer
	if err := png.Encode(&b, image.NewRGBA(image.Rect(0, 0, 8, 4))); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(path, append(b.Bytes(), bytes.Repeat([]byte{shade}, pad)...), 0o644); err != nil {
		t.Fatal(err)
	}
}

// acceptedWithImages 创建已验收的作品 my-post：正文依次为 files 中每个图片文件的独占一段引用（id 为去掉扩展名的文件名），
// 图片由 write 写到项目目录后登记。
func acceptedWithImages(t *testing.T, files []string, write func(path string, i int)) string {
	t.Helper()
	dir := newWork(t)
	writeFile(t, wsPath(dir, ".writing-anything/works/my-post/brief.yaml"), completeBrief)
	mustRunWS(t, dir, "advance", "my-post")
	var body strings.Builder
	for _, f := range files {
		id := strings.TrimSuffix(f, filepath.Ext(f))
		body.WriteString("![" + id + "](illustrations/" + f + ")\n\n")
	}
	body.WriteString("见[文档](https://example.com/doc)。\n")
	if code, res := runWSIn(t, dir, body.String(), "draft", "write", "my-post"); code != 0 {
		t.Fatalf("draft write: %+v", res)
	}
	mustRunWS(t, dir, "advance", "my-post")
	if code, res := runWSIn(t, dir, "rubric:\n  - {item: clarity, verdict: pass, evidence: 清楚, paragraphs: [1]}\n", "review", "record", "my-post"); code != 0 {
		t.Fatalf("review record: %+v", res)
	}
	for i, f := range files {
		write(filepath.Join(dir, f), i)
		id := strings.TrimSuffix(f, filepath.Ext(f))
		if code, out := addIllustration(t, dir, "my-post", "--id", id, "--file", f, "--alt", id, "--origin", "agent"); code != 0 {
			t.Fatalf("illustration add %s: %v", f, out)
		}
	}
	mustRunWS(t, dir, "accept", "my-post")
	return dir
}

func pngFiles(prefix string, n int) []string {
	var out []string
	for i := 1; i <= n; i++ {
		out = append(out, prefix+itoaTest(i)+".png")
	}
	return out
}

func itoaTest(i int) string { return strconv.Itoa(i) }

// paste-embed-budget：33 张略小于 1 MiB 的图（不重新编码），前 32 张内嵌，第 33 张占位。
func TestExportPasteEmbedBudget(t *testing.T) {
	files := pngFiles("f", 33)
	dir := acceptedWithImages(t, files, func(path string, i int) { writePaddedPNG(t, path, export.PasteImageMaxBytes-300, byte(i)) })
	if code, res := runExportJSON(t, dir, "my-post", "--format", "paste-html"); code != 0 || len(res.Export.Assets) != 33 {
		t.Fatalf("export: %d %+v", code, res.Export)
	}
	page := readFile(t, wsPath(dir, exportsDir+"/my-post.paste.html"))
	if strings.Count(page, `<img src="data:image/png;base64,`) != 32 || !strings.Contains(page, "【配图 33】f33") {
		t.Errorf("embed budget not applied: %d", strings.Count(page, "<img "))
	}
	list := readFile(t, wsPath(dir, exportsDir+"/my-post.paste.images.md"))
	if !strings.Contains(list, "my-post.paste.assets/my-post.f33.png") {
		t.Errorf("image list lacks f33:\n%s", list)
	}
}

// paste-reencode-large：超过 1 MiB 的配图在可粘贴正文中重新编码为不超过 1 MiB、宽 1600 的 JPEG，小图原样，资产目录为原图。
func TestExportPasteReencode(t *testing.T) {
	dir := acceptedWithImages(t, []string{"big.png", "small.png"}, func(path string, i int) {
		w, h := 2000, 1500
		if i == 1 {
			w, h = 8, 4
		}
		img := image.NewNRGBA(image.Rect(0, 0, w, h))
		for k := range img.Pix {
			img.Pix[k] = uint8(k / 7)
		}
		var b bytes.Buffer
		if err := png.Encode(&b, img); err != nil {
			t.Fatal(err)
		}
		if i == 0 && b.Len() < 3<<20 {
			b.Write(make([]byte, 3<<20-b.Len()))
		}
		if err := os.WriteFile(path, b.Bytes(), 0o644); err != nil {
			t.Fatal(err)
		}
	})
	if code, res := runExportJSON(t, dir, "my-post", "--format", "paste-html"); code != 0 {
		t.Fatalf("export: %d %+v", code, res)
	}
	page := readFile(t, wsPath(dir, exportsDir+"/my-post.paste.html"))
	m := regexp.MustCompile(`<img src="data:image/jpeg;base64,([^"]*)" alt="big"`).FindStringSubmatch(page)
	if m == nil || !strings.Contains(page, `<img src="data:image/png;base64,`) {
		t.Fatalf("big not reencoded or small not kept")
	}
	data, err := base64.StdEncoding.DecodeString(strings.ReplaceAll(m[1], "&#43;", "+"))
	if err != nil || len(data) > export.PasteImageMaxBytes {
		t.Fatalf("jpeg: %v %d", err, len(data))
	}
	if cfg, err := jpeg.DecodeConfig(bytes.NewReader(data)); err != nil || cfg.Width != 1600 || cfg.Height != 1200 {
		t.Errorf("jpeg size = %+v %v", cfg, err)
	}
	sameBytes(t, filepath.Join(dir, "big.png"), wsPath(dir, exportsDir+"/my-post.paste.assets/my-post.big.png"))
}

// paste-pixel-limit：宽高之积超过 4000 万像素的配图不内嵌，改为占位，资产目录照常有原图。
func TestExportPastePixelLimit(t *testing.T) {
	dir := acceptedWithImages(t, []string{"huge.png"}, func(path string, _ int) {
		ihdr := make([]byte, 13)
		binary.BigEndian.PutUint32(ihdr[0:], 8000)
		binary.BigEndian.PutUint32(ihdr[4:], 6000)
		ihdr[8], ihdr[9] = 8, 2
		var b bytes.Buffer
		b.WriteString("\x89PNG\r\n\x1a\n")
		binary.Write(&b, binary.BigEndian, uint32(len(ihdr)))
		b.WriteString("IHDR")
		b.Write(ihdr)
		binary.Write(&b, binary.BigEndian, crc32.ChecksumIEEE(append([]byte("IHDR"), ihdr...)))
		b.Write(make([]byte, 2<<20))
		if err := os.WriteFile(path, b.Bytes(), 0o644); err != nil {
			t.Fatal(err)
		}
	})
	if code, res := runExportJSON(t, dir, "my-post", "--format", "paste-html"); code != 0 || len(res.Export.Assets) != 1 {
		t.Fatalf("export: %d %+v", code, res)
	}
	page := readFile(t, wsPath(dir, exportsDir+"/my-post.paste.html"))
	if strings.Contains(page, "<img ") || !strings.Contains(page, "【配图 1】huge") {
		t.Errorf("huge embedded")
	}
	if !fileExists(wsPath(dir, exportsDir+"/my-post.paste.assets/my-post.huge.png")) {
		t.Error("asset missing")
	}
}

// paste-segments 与 print-segments：9 张图分为 7+2 两段，页面有分段按钮与「复制全文」；7 张时只有一个「复制图文」。
func TestExportPasteSegments(t *testing.T) {
	small := func(path string, i int) { writePaddedPNG(t, path, 0, byte(i)) }
	dir := acceptedWithImages(t, pngFiles("p", 9), small)
	if code, res := runExportJSON(t, dir, "my-post", "--format", "paste-html"); code != 0 || res.Export.HTML != nil || res.Export.Segments != nil {
		t.Fatalf("export: %d %+v", code, res.Export)
	}
	page := readFile(t, wsPath(dir, exportsDir+"/my-post.paste.html"))
	for _, want := range []string{`data-copy="wa-seg-1"`, "复制第 1 段（图 1–7）", `data-copy="wa-seg-2"`, "复制第 2 段（图 8–9）", `data-copy="wa-article"`, "复制全文",
		`<div id="wa-seg-1">`, `<div id="wa-seg-2">`, "公众号每次粘贴最多 7 张图"} {
		if !strings.Contains(page, want) {
			t.Errorf("page lacks %q", want)
		}
	}
	if strings.Contains(page, `id="wa-copy"`) {
		t.Error("multi-segment page still has the single button")
	}
	seg2 := page[strings.Index(page, `<div id="wa-seg-2">`):]
	if strings.Count(seg2, "<img ") != 2 || !strings.Contains(seg2, "参考链接") {
		t.Errorf("segment 2:\n%.300s", seg2)
	}
	code, res := runExportJSON(t, dir, "my-post", "--format", "paste-html", "--print")
	if code != 0 || len(res.Export.Segments) != 2 || strings.Count(res.Export.Segments[0], "<img ") != 7 || strings.Count(res.Export.Segments[1], "<img ") != 2 ||
		*res.Export.HTML != res.Export.Segments[0]+res.Export.Segments[1] {
		t.Fatalf("print segments: %d %d", code, len(res.Export.Segments))
	}
	for i, sg := range res.Export.Segments {
		allowedPasteHTML(t, sg)
		if !strings.Contains(page, `<div id="wa-seg-`+itoaTest(i+1)+`">`+sg+`</div>`) {
			t.Errorf("segment %d differs from the page", i+1)
		}
	}
	seven := acceptedWithImages(t, pngFiles("p", 7), small)
	if code, res := runExportJSON(t, seven, "my-post", "--format", "paste-html", "--print"); code != 0 || len(res.Export.Segments) != 1 || res.Export.Segments[0] != *res.Export.HTML {
		t.Fatalf("seven: %d %+v", code, res.Export.Segments)
	}
	runExportJSON(t, seven, "my-post", "--format", "paste-html")
	if p := readFile(t, wsPath(seven, exportsDir+"/my-post.paste.html")); !strings.Contains(p, `id="wa-copy"`) || strings.Contains(p, "复制全文") || strings.Contains(p, "wa-seg-") {
		t.Error("seven images should give the single-button page")
	}
}

// paste-front-matter、paste-nothing-accepted、print-wrong-format、print-nothing-accepted 与 --print 加 --output。
func TestExportPasteRejections(t *testing.T) {
	dir := acceptedWork(t, "正文。\n")
	for _, c := range []struct {
		args        []string
		code, field string
	}{
		{[]string{"--format", "paste-html", "--front-matter"}, "input.invalid-value", "front-matter"},
		{[]string{"--format", "wechat-html", "--print"}, "input.invalid-value", "print"},
		{[]string{"--print"}, "input.invalid-value", "print"},
		{[]string{"--format", "paste-html", "--print", "--output", "dist/x.html"}, "input.invalid-value", "output"},
	} {
		code, res := runExportJSON(t, dir, append([]string{"my-post"}, c.args...)...)
		if d := diag(t, res.result, c.code); code != 1 || d.Field != c.field || res.Export != nil || len(res.ChangedPaths) != 0 || !hasNext(d, "wa-cli help export --json") {
			t.Errorf("%v: %d %+v", c.args, code, res)
		}
	}
	if entries, _ := os.ReadDir(wsPath(dir, exportsDir)); len(entries) != 0 {
		t.Errorf("exports written: %v", entries)
	}
	empty := newWork(t)
	for _, args := range [][]string{{"--format", "paste-html"}, {"--format", "paste-html", "--print"}} {
		code, res := runExportJSON(t, empty, append([]string{"my-post"}, args...)...)
		if code != 1 || res.Export != nil || len(res.ChangedPaths) != 0 || !hasNext(diag(t, res.result, "export.nothing-accepted"), "wa-cli next") {
			t.Errorf("%v: %d %+v", args, code, res)
		}
	}
	if fileExists(wsPath(empty, exportsDir+"/my-post.paste.html")) {
		t.Error("page written without accepted units")
	}
}

// print-readonly
func TestExportPastePrint(t *testing.T) {
	dir := illustratedAccepted(t, "# 周六的店\n\n店里新来了一台烘豆机。\n\n![店门口](illustrations/shop.jpg)\n", true, addShop, addCover)
	code, res := runExportJSON(t, dir, "my-post", "--format", "paste-html", "--print")
	if code != 0 || res.Export.HTML == nil || res.Export.Path != nil || res.Export.ImageList != nil || len(res.Export.Assets) != 0 ||
		len(res.ChangedPaths) != 0 || res.Export.Disclosure == nil || len(res.Export.Images) != 1 {
		t.Fatalf("print: %d %+v", code, res.Export)
	}
	html := *res.Export.HTML
	allowedPasteHTML(t, html)
	if strings.Count(html, "<img ") != 1 || !strings.Contains(html, `src="data:image/jpeg;base64,`) || strings.Contains(html, res.Export.Disclosure.Text) {
		t.Errorf("fragment:\n%s", html)
	}
	if fileExists(wsPath(dir, exportsDir)) {
		t.Error("print wrote into exports/")
	}
	// 片段与写出的可粘贴页正文容器中的内容相同。
	if code, _ := runExportJSON(t, dir, "my-post", "--format", "paste-html"); code != 0 {
		t.Fatal("export failed")
	}
	if page := readFile(t, wsPath(dir, exportsDir+"/my-post.paste.html")); !strings.Contains(page, `<div id="wa-article">`+html+`</div>`) {
		t.Error("fragment differs from the page's article")
	}
	// 不加 --json 时把片段写到标准输出。
	if code, stdout, _ := run(t, "export", "my-post", "--format", "paste-html", "--print", "--dir", dir); code != 0 || stdout != html+"\n" {
		t.Errorf("stdout: %d %.80s", code, stdout)
	}
}

// audit-print-not-paired：export --print 的审计行不含 unit，不参与回捞配对。
func TestExportPastePrintNotPaired(t *testing.T) {
	lastAudit := func(t *testing.T, dir string) map[string]any {
		t.Helper()
		lines := strings.Split(strings.TrimRight(readFile(t, wsPath(dir, ".writing-anything/audit.log")), "\n"), "\n")
		var m map[string]any
		if err := json.Unmarshal([]byte(lines[len(lines)-1]), &m); err != nil {
			t.Fatal(err)
		}
		return m
	}
	// 真实导出失败后，--print 成功不消掉回捞提示。
	dir := acceptedWork(t, "正文。\n")
	if code, _ := runExportJSON(t, dir, "my-post", "--output", "../x.md"); code != 1 {
		t.Fatal("export should fail")
	}
	if code, _ := runExportJSON(t, dir, "my-post", "--format", "paste-html", "--print"); code != 0 {
		t.Fatal("print failed")
	}
	if e := lastAudit(t, dir); e["command"] != "export" || e["exit"] != float64(0) || e["unit"] != nil {
		t.Errorf("print audit line = %v", e)
	}
	if _, res := runWS(t, dir, "next", "my-post"); !strings.Contains(res.stdout, "export 在本单元失败后尚未成功重试") {
		t.Errorf("recovery cleared by --print: %s", res.stdout)
	}
	// --print 失败不留下待回捞的失败。
	empty := newWork(t)
	if code, _ := runExportJSON(t, empty, "my-post", "--format", "paste-html", "--print"); code != 1 {
		t.Fatal("print should fail without accepted units")
	}
	if e := lastAudit(t, empty); e["exit"] != float64(1) || e["unit"] != nil {
		t.Errorf("failed print audit line = %v", e)
	}
	if _, res := runWS(t, empty, "next", "my-post"); strings.Contains(res.stdout, "尚未成功重试") {
		t.Errorf("failed --print left a recovery hint: %s", res.stdout)
	}
}

// print-readonly 与 enum-scoped-output-fields：html 与 segments 只在 paste-html --print 时出现，其他格式与不带 --print 的
// paste-html 的 --json 输出中没有这两个键；--print 除追加一行审计日志外不改动工作区中的任何文件。
func TestExportPastePrintKeysAndReadOnly(t *testing.T) {
	dir := illustratedAccepted(t, "# 周六的店\n\n![店门口](illustrations/shop.jpg)\n", true, addShop, addCover)
	keys := func(args ...string) map[string]any {
		t.Helper()
		_, res := runWS(t, dir, append([]string{"export", "my-post"}, args...)...)
		var out struct {
			Export map[string]any `json:"export"`
		}
		if err := json.Unmarshal([]byte(res.stdout), &out); err != nil || out.Export == nil {
			t.Fatalf("%v: %v %s", args, err, res.stdout)
		}
		return out.Export
	}
	for _, args := range [][]string{{}, {"--format", "wechat-html"}, {"--format", "text"}, {"--format", "paste-html"}} {
		e := keys(args...)
		for _, k := range []string{"html", "segments"} {
			if _, ok := e[k]; ok {
				t.Errorf("%v: export has key %s", args, k)
			}
		}
	}
	// 快照工作区（含 exports/），--print 之后只有 audit.log 变化。
	snapshot := func() map[string]string {
		out := map[string]string{}
		filepath.WalkDir(dir, func(p string, d os.DirEntry, err error) error {
			if err == nil && !d.IsDir() {
				data, _ := os.ReadFile(p)
				out[p] = string(data)
			}
			return nil
		})
		return out
	}
	before := snapshot()
	e := keys("--format", "paste-html", "--print")
	if _, ok := e["html"]; !ok {
		t.Fatal("print lacks html")
	}
	if _, ok := e["segments"]; !ok {
		t.Fatal("print lacks segments")
	}
	after := snapshot()
	audit := filepath.Join(dir, ".writing-anything", "audit.log")
	for p, v := range after {
		if p == audit {
			if added := strings.TrimPrefix(v, before[p]); strings.Count(added, "\n") != 1 || !strings.HasPrefix(v, before[p]) {
				t.Errorf("audit.log changed beyond one appended line")
			}
			continue
		}
		if before[p] != v {
			t.Errorf("%s changed by --print", p)
		}
	}
	if len(after) != len(before) {
		t.Errorf("files: %d before, %d after", len(before), len(after))
	}
}
