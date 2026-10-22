package cli

import (
	"bytes"
	"os"
	"path/filepath"
	"strings"
	"testing"
)

const exportsDir = ".writing-anything/exports"

// illustratedAccepted 创建作品 my-post：正文为 body（agent 为 true 时经 draft write 写入，否则由作者直接写入 draft.md），推进到审稿并审稿，
// 再逐个运行 adds 中的 illustration add 参数（相对项目目录的图片由调用方准备），最后验收。
func illustratedAccepted(t *testing.T, body string, agent bool, adds ...[]string) string {
	t.Helper()
	dir := newWork(t)
	writeFile(t, wsPath(dir, ".writing-anything/works/my-post/brief.yaml"), completeBrief)
	mustRunWS(t, dir, "advance", "my-post")
	if agent {
		if code, res := runWSIn(t, dir, body, "draft", "write", "my-post"); code != 0 {
			t.Fatalf("draft write: %+v", res)
		}
	} else {
		writeFile(t, wsPath(dir, ".writing-anything/works/my-post/draft.md"), body)
	}
	mustRunWS(t, dir, "advance", "my-post")
	if code, res := runWSIn(t, dir, "rubric:\n  - {item: clarity, verdict: pass, evidence: 清楚, paragraphs: [1]}\n", "review", "record", "my-post"); code != 0 {
		t.Fatalf("review record: %+v", res)
	}
	writeTestImage(t, filepath.Join(dir, "shop.jpg"), "jpeg", 1600, 900, 1)
	writeTestImage(t, filepath.Join(dir, "cover.jpg"), "jpeg", 1800, 766, 2)
	writeTestImage(t, filepath.Join(dir, "photo.png"), "png", 800, 600, 3)
	for _, a := range adds {
		if code, out := addIllustration(t, dir, a...); code != 0 {
			t.Fatalf("illustration add %v: %v", a, out)
		}
	}
	mustRunWS(t, dir, "accept", "my-post")
	return dir
}

var (
	addShop  = []string{"my-post", "--id", "shop", "--file", "shop.jpg", "--alt", "店门口", "--origin", "agent"}
	addCover = []string{"my-post", "--id", "cover", "--role", "cover", "--file", "cover.jpg", "--alt", "周六的店", "--origin", "agent"}
	addPhoto = []string{"my-post", "--id", "photo", "--file", "photo.png", "--alt", "实拍", "--origin", "author"}
)

func sameBytes(t *testing.T, a, b string) {
	t.Helper()
	x, err1 := os.ReadFile(a)
	y, err2 := os.ReadFile(b)
	if err1 != nil || err2 != nil || !bytes.Equal(x, y) {
		t.Errorf("%s and %s differ (%v %v)", a, b, err1, err2)
	}
}

// copy-illustrations、ai-images-appended
func TestExportMarkdownCopiesIllustrations(t *testing.T) {
	dir := illustratedAccepted(t, "# 周六的店\n\n店里新来了一台烘豆机。\n\n![店门口](illustrations/shop.jpg \"周六的店\")\n", true, addShop, addCover)
	mustRunWS(t, dir, "config", "set", "ai_disclosure", "footer")
	code, res := runExportJSON(t, dir, "my-post", "--front-matter")
	if code != 0 {
		t.Fatalf("export: %d %+v", code, res)
	}
	got := readFile(t, wsPath(dir, exportsDir+"/my-post.md"))
	if !strings.Contains(got, "![店门口](my-post.assets/my-post.shop.jpg \"周六的店\")") || !strings.Contains(got, "\ncover: my-post.assets/my-post.cover.jpg\n") {
		t.Errorf("exported:\n%s", got)
	}
	const label = "本文在 AI 辅助下完成：约 100% 的内容由 AI 起草，均经作者审阅确认。文中 2 张配图由 AI 生成。"
	if d := res.Export.Disclosure; d == nil || d.Text != label || d.AIImages != 2 || d.Images != 2 || !strings.HasSuffix(got, "\n---\n\n"+label+"\n") {
		t.Errorf("disclosure = %+v", res.Export.Disclosure)
	}
	sameBytes(t, wsPath(dir, exportsDir+"/my-post.assets/my-post.shop.jpg"), wsPath(dir, ".writing-anything/works/my-post/illustrations/shop.jpg"))
	sameBytes(t, wsPath(dir, exportsDir+"/my-post.assets/my-post.cover.jpg"), wsPath(dir, ".writing-anything/works/my-post/illustrations/cover.jpg"))
	as := res.Export.Assets
	if len(as) != 2 || as[0].ID != "cover" || as[0].Role != "cover" || as[1].ID != "shop" || as[1].Origin != "agent" ||
		as[1].Source != ".writing-anything/works/my-post/illustrations/shop.jpg" || as[1].Path != exportsDir+"/my-post.assets/my-post.shop.jpg" || as[1].Unit != "my-post" {
		t.Errorf("assets = %+v", as)
	}
	if len(res.Export.Images) != 0 || res.Export.ImageList != nil {
		t.Errorf("images = %+v %v", res.Export.Images, res.Export.ImageList)
	}
	want := []string{exportsDir + "/my-post.md", exportsDir + "/my-post.assets", exportsDir + "/my-post.assets/my-post.cover.jpg", exportsDir + "/my-post.assets/my-post.shop.jpg"}
	if cp := res.ChangedPaths; len(cp) < 4 || strings.Join(cp[len(cp)-4:], ",") != strings.Join(want, ",") {
		t.Errorf("changedPaths = %v", cp)
	}
	// 再次导出：资产目录已存在，只写资产文件；目录中其他文件不删除。
	writeFile(t, wsPath(dir, exportsDir+"/my-post.assets/keep.txt"), "x")
	if _, res := runExportJSON(t, dir, "my-post"); strings.Join(res.ChangedPaths, ",") != strings.Join([]string{want[0], want[2], want[3]}, ",") || !fileExists(wsPath(dir, exportsDir+"/my-post.assets/keep.txt")) {
		t.Errorf("second export changedPaths = %v", res.ChangedPaths)
	}
}

// child-unit-asset-name
func TestExportChildUnitAssetNames(t *testing.T) {
	dir := illustratedAccepted(t, "根单元。\n\n![根图](illustrations/fig.jpg)\n", true,
		[]string{"my-post", "--id", "fig", "--file", "shop.jpg", "--alt", "根图", "--origin", "agent"})
	mustRunWS(t, dir, "unit", "add", "my-post", "section", "intro")
	intro := ".writing-anything/works/my-post/units/intro"
	if err := os.MkdirAll(wsPath(dir, intro), 0o755); err != nil {
		t.Fatal(err)
	}
	writeFile(t, wsPath(dir, intro+"/brief.yaml"), "schema_version: 1\n")
	mustRunWS(t, dir, "advance", "my-post/intro")
	writeFile(t, wsPath(dir, intro+"/draft.md"), "子单元。\n\n![子图](illustrations/fig.jpg)\n")
	mustRunWS(t, dir, "advance", "my-post/intro")
	if code, res := runWSIn(t, dir, "rubric:\n  - {item: clarity, verdict: pass, evidence: 清楚, paragraphs: [1]}\n", "review", "record", "my-post/intro"); code != 0 {
		t.Fatalf("review record: %+v", res)
	}
	if code, out := addIllustration(t, dir, "my-post/intro", "--id", "fig", "--file", "photo.png", "--alt", "子图", "--origin", "author"); code != 0 {
		t.Fatal(out)
	}
	writeFile(t, wsPath(dir, intro+"/draft.md"), "子单元。\n\n![子图](illustrations/fig.png)\n")
	if code, res := runWSIn(t, dir, "rubric:\n  - {item: clarity, verdict: pass, evidence: 清楚, paragraphs: [1]}\n", "review", "record", "my-post/intro"); code != 0 {
		t.Fatalf("review record: %+v", res)
	}
	mustRunWS(t, dir, "accept", "my-post/intro")
	code, res := runExportJSON(t, dir, "my-post")
	got := readFile(t, wsPath(dir, exportsDir+"/my-post.md"))
	if code != 0 || len(res.Export.Assets) != 2 || !strings.Contains(got, "](my-post.assets/my-post.fig.jpg)") || !strings.Contains(got, "](my-post.assets/my-post.intro.fig.png)") {
		t.Fatalf("export: %d %+v\n%s", code, res.Export, got)
	}
	if res.Export.Assets[1].Unit != "my-post/intro" || !fileExists(wsPath(dir, exportsDir+"/my-post.assets/my-post.intro.fig.png")) {
		t.Errorf("assets = %+v", res.Export.Assets)
	}
}

// no-illustrations-unchanged
func TestExportWithoutIllustrationsUnchanged(t *testing.T) {
	body := "正文。\n\n![图](https://example.com/a.png) 与 `![x](illustrations/x.jpg)`\n"
	dir := acceptedWork(t, body)
	code, res := runExportJSON(t, dir, "my-post")
	if code != 0 || res.Export.Assets == nil || len(res.Export.Assets) != 0 || fileExists(wsPath(dir, exportsDir+"/my-post.assets")) {
		t.Fatalf("export: %d %+v", code, res.Export)
	}
	if got := readFile(t, wsPath(dir, exportsDir+"/my-post.md")); !strings.Contains(got, "![图](https://example.com/a.png) 与 `![x](illustrations/x.jpg)`") {
		t.Errorf("exported: %s", got)
	}
	for _, p := range res.ChangedPaths {
		if strings.Contains(p, ".assets") {
			t.Errorf("changedPaths = %v", res.ChangedPaths)
		}
	}
}

// illustration-replaced-after-accept
func TestExportStaleAfterReplace(t *testing.T) {
	dir := illustratedAccepted(t, "正文。\n\n![店门口](illustrations/shop.jpg)\n", true, addShop)
	writeTestImage(t, filepath.Join(dir, "shop2.jpg"), "jpeg", 10, 10, 9)
	if code, out := addIllustration(t, dir, "my-post", "--id", "shop", "--file", "shop2.jpg", "--alt", "店门口", "--origin", "agent", "--replace"); code != 0 {
		t.Fatal(out)
	}
	code, res := runExportJSON(t, dir, "my-post")
	if code != 1 || diag(t, res.result, "export.unaccepted").Path != ".writing-anything/works/my-post" || diag(t, res.result, "export.nothing-accepted").Code == "" {
		t.Fatalf("export: %d %+v", code, res.Diagnostics)
	}
	if fileExists(wsPath(dir, exportsDir+"/my-post.md")) || fileExists(wsPath(dir, exportsDir+"/my-post.assets")) {
		t.Error("files written")
	}
}

// asset-dir-symlink
func TestExportAssetDirSymlink(t *testing.T) {
	dir := illustratedAccepted(t, "正文。\n", true, addCover)
	if err := os.MkdirAll(wsPath(dir, exportsDir), 0o755); err != nil {
		t.Fatal(err)
	}
	outside := t.TempDir()
	symlink(t, outside, wsPath(dir, exportsDir+"/my-post.assets"))
	code, res := runExportJSON(t, dir, "my-post")
	if code != 1 || diag(t, res.result, "export.invalid-output").Path != exportsDir+"/my-post.assets" {
		t.Fatalf("export: %d %+v", code, res.Diagnostics)
	}
	if fileExists(wsPath(dir, exportsDir+"/my-post.md")) {
		t.Error("export file written")
	}
	if es, _ := os.ReadDir(outside); len(es) != 0 {
		t.Error("wrote through symlink")
	}
	// 公众号 HTML 同样在写入前检查资产路径。
	if code, res := runExportJSON(t, dir, "my-post", "--format", "wechat-html"); code != 1 || fileExists(wsPath(dir, exportsDir+"/my-post.html")) || fileExists(wsPath(dir, exportsDir+"/my-post.images.md")) {
		t.Errorf("wechat: %d %+v", code, res.Diagnostics)
	}
}

// wechat-cover-and-local
func TestExportWechatCoverAndLocal(t *testing.T) {
	dir := illustratedAccepted(t, "开头。\n\n![店门口](illustrations/shop.jpg)\n\n图标 ![图标](https://example.com/icon.png) 在这里。\n", true, addShop, addCover)
	code, res := runExportJSON(t, dir, "my-post", "--format", "wechat-html")
	if code != 0 {
		t.Fatalf("export: %d %+v", code, res)
	}
	html := readFile(t, wsPath(dir, exportsDir+"/my-post.html"))
	if !strings.Contains(html, "【配图 1】店门口") || !strings.Contains(html, "【配图 2：图标】") {
		t.Errorf("html: %s", html)
	}
	allowedHTML(t, html)
	list := readFile(t, wsPath(dir, exportsDir+"/my-post.images.md"))
	want := "# 配图清单\n\n- 封面：周六的店；文件：my-post.assets/my-post.cover.jpg；AI 生成\n\n" +
		"1. 店门口：illustrations/shop.jpg；文件：my-post.assets/my-post.shop.jpg；AI 生成\n2. 图标：https://example.com/icon.png\n"
	if list != want {
		t.Errorf("image list:\n%s\nwant:\n%s", list, want)
	}
	if im := res.Export.Images; len(im) != 2 || im[0].Src != "illustrations/shop.jpg" || im[1].Src != "https://example.com/icon.png" {
		t.Errorf("images = %+v", im)
	}
	if as := res.Export.Assets; len(as) != 2 || as[0].ID != "cover" || as[1].ID != "shop" {
		t.Errorf("assets = %+v", as)
	}
	cp := strings.Join(res.ChangedPaths, ",")
	if !strings.HasSuffix(cp, exportsDir+"/my-post.html,"+exportsDir+"/my-post.images.md,"+exportsDir+"/my-post.assets,"+exportsDir+"/my-post.assets/my-post.cover.jpg,"+exportsDir+"/my-post.assets/my-post.shop.jpg") {
		t.Errorf("changedPaths = %s", cp)
	}
}

// wechat-cover-only
func TestExportWechatCoverOnly(t *testing.T) {
	dir := illustratedAccepted(t, "没有图片的正文。\n", true, addCover)
	code, res := runExportJSON(t, dir, "my-post", "--format", "wechat-html")
	if code != 0 || len(res.Export.Images) != 0 || res.Export.ImageList == nil || *res.Export.ImageList != exportsDir+"/my-post.images.md" {
		t.Fatalf("export: %d %+v", code, res.Export)
	}
	if list := readFile(t, wsPath(dir, exportsDir+"/my-post.images.md")); list != "# 配图清单\n\n- 封面：周六的店；文件：my-post.assets/my-post.cover.jpg；AI 生成\n" {
		t.Errorf("list = %q", list)
	}
	if !fileExists(wsPath(dir, exportsDir+"/my-post.assets/my-post.cover.jpg")) {
		t.Error("cover not copied")
	}
}

// ai-images-only、image-template-override、text-ignores-illustrations
func TestExportAIImagesOnlyAndText(t *testing.T) {
	dir := illustratedAccepted(t, "作者写的正文。\n\n![实拍](illustrations/photo.png)\n", false, addPhoto, addCover)
	code, res := runExportJSON(t, dir, "my-post")
	d := res.Export.Disclosure
	if code != 0 || d == nil || d.Text != "文中 1 张配图由 AI 生成。" || d.AIParagraphs != 0 || d.AIPercent != 0 || d.AIImages != 1 || d.Images != 2 {
		t.Fatalf("disclosure = %+v", d)
	}
	code, res = runExportJSON(t, dir, "my-post", "--format", "text")
	got := readFile(t, wsPath(dir, exportsDir+"/my-post.txt"))
	if code != 0 || !strings.Contains(got, "【图：实拍】") || len(res.Export.Assets) != 0 || res.Export.Disclosure != nil || strings.Contains(strings.Join(res.ChangedPaths, ","), ".assets") {
		t.Errorf("text: %d %+v\n%s", code, res.Export, got)
	}
	writePack(t, dir, "brand", "schema_version: 1\nid: brand\ntitle: 品牌\nai_disclosure_image_template: \"{ai_images}/{images} 张图由 AI 绘制\"\n")
	mustRunWS(t, dir, "config", "set", "packs", "default,brand")
	if _, res := runExportJSON(t, dir, "my-post"); res.Export.Disclosure == nil || res.Export.Disclosure.Text != "1/2 张图由 AI 绘制" {
		t.Errorf("override = %+v", res.Export.Disclosure)
	}
	// resolved-ai-image-template
	_, rr := runWS(t, dir, "config", "resolved")
	r := rr.resolved(t)
	if get(r, "aiDisclosure.imageTemplate.value") != "{ai_images}/{images} 张图由 AI 绘制" || get(r, "aiDisclosure.imageTemplate.source") != "pack:brand" {
		t.Errorf("resolved = %v", r["aiDisclosure"])
	}
	// bad-image-template
	writePack(t, dir, "brand", "schema_version: 1\nid: brand\ntitle: 品牌\nai_disclosure_image_template: \"{model} 画的\"\n")
	if code, res := runWS(t, dir, "validate"); code != 1 || diag(t, res, "artifact.schema-violation").Field != "ai_disclosure_image_template" {
		t.Errorf("validate = %d %+v", code, res.Diagnostics)
	}
}

func TestResolvedDefaultImageTemplate(t *testing.T) {
	dir := newWork(t)
	_, rr := runWS(t, dir, "config", "resolved")
	r := rr.resolved(t)
	if get(r, "aiDisclosure.imageTemplate.value") != "文中 {ai_images} 张配图由 AI 生成。" || get(r, "aiDisclosure.imageTemplate.source") != "default" {
		t.Errorf("resolved = %v", r["aiDisclosure"])
	}
}
