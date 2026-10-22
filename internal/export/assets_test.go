package export

import (
	"bytes"
	"image"
	"image/png"
	"os"
	"path/filepath"
	"strings"
	"testing"
	"time"

	"github.com/kw12121212/writing-anything/internal/illustration"
	"github.com/kw12121212/writing-anything/internal/review"
	"github.com/kw12121212/writing-anything/internal/work"
	"github.com/kw12121212/writing-anything/internal/workspace"
)

func TestAssetDir(t *testing.T) {
	for in, want := range map[string]string{
		".writing-anything/exports/post.md": ".writing-anything/exports/post.assets",
		"dist/post.tar.gz":                  "dist/post.tar.assets",
		"post":                              "post.assets",
		"dist/.hidden":                      "dist/.hidden.assets",
	} {
		if got := AssetDir(in); got != want {
			t.Errorf("AssetDir(%q) = %q, want %q", in, got, want)
		}
	}
}

func testPNG(t *testing.T, shade uint8) []byte {
	t.Helper()
	img := image.NewGray(image.Rect(0, 0, 4, 3))
	img.Pix[0] = shade
	var b bytes.Buffer
	if err := png.Encode(&b, img); err != nil {
		t.Fatal(err)
	}
	return b.Bytes()
}

// illustratedRoot 创建只有根单元的已验收作品 post：正文为 body（作者直接写入），登记 adds 中的配图（origin 为 agent），验收记录含配图摘要。
func illustratedRoot(t *testing.T, body string, adds ...illustration.AddInput) (string, *work.Work) {
	t.Helper()
	root := t.TempDir()
	if _, ps := workspace.Init(root, nil); len(ps) > 0 {
		t.Fatal(ps)
	}
	dir := ".writing-anything/works/post"
	if err := os.MkdirAll(filepath.Join(root, dir), 0o755); err != nil {
		t.Fatal(err)
	}
	write := func(rel, s string) {
		if err := os.WriteFile(filepath.Join(root, rel), []byte(s), 0o644); err != nil {
			t.Fatal(err)
		}
	}
	write(dir+"/work.yaml", "schema_version: 1\nid: post\ngenre: minimal\ntype: article\nstage: review\nunits: []\n")
	write(dir+"/draft.md", body)
	now := time.Date(2026, 10, 22, 8, 0, 0, 0, time.UTC)
	for _, in := range adds {
		in.IsRoot, in.Origin, in.Content = true, illustration.OriginAgent, illustration.ContentMood
		if in.Role == "" {
			in.Role = illustration.RoleBody
		}
		if _, _, ps := illustration.Add(root, dir, in, func() ([]string, []workspace.Problem) { return nil, nil }, now); len(ps) > 0 {
			t.Fatal(ps)
		}
	}
	if _, _, ps := review.Accept(root, dir, "fast", review.SHA256([]byte(body)), false, "", now); len(ps) > 0 {
		t.Fatal(ps)
	}
	w, _, ps := work.Resolve(root, "post")
	if w == nil {
		t.Fatal(ps)
	}
	return root, w
}

func TestExportAssetsAndDisclosure(t *testing.T) {
	body := "# 标题\n\n开头。\n\n![店门口](<illustrations/shop.png> \"周六\")\n\n再引用一次 ![店门口](illustrations/shop.png) 与 ![外部](https://e.com/a.png)。\n"
	root, w := illustratedRoot(t, body,
		illustration.AddInput{ID: "shop", Data: testPNG(t, 1), Alt: "店门口"},
		illustration.AddInput{ID: "cover", Role: illustration.RoleCover, Data: testPNG(t, 2), Alt: "封面"})
	cfg := &DisclosureConfig{Placement: PlacementBoth, Template: "模板"}
	res, changed, ps := Markdown(root, w, Options{FrontMatter: true, Level: "fast", Disclosure: cfg})
	if res == nil || len(ps) > 0 {
		t.Fatalf("Markdown: %v", ps)
	}
	got, _ := os.ReadFile(filepath.Join(root, res.Path))
	doc := string(got)
	if !strings.Contains(doc, "![店门口](<post.assets/post.shop.png> \"周六\")") || strings.Count(doc, "post.assets/post.shop.png") != 2 ||
		!strings.Contains(doc, "![外部](https://e.com/a.png)") || !strings.Contains(doc, "cover: post.assets/post.cover.png") {
		t.Errorf("doc:\n%s", doc)
	}
	if len(res.Assets) != 2 || res.Assets[0].ID != "cover" || res.Assets[1].ID != "shop" || len(changed) != 5 {
		t.Errorf("assets = %+v changed = %v", res.Assets, changed)
	}
	if d := res.Disclosure; d == nil || d.Text != "文中 2 张配图由 AI 生成。" || d.AIImages != 2 || d.Images != 2 || d.AIPercent != 0 {
		t.Errorf("disclosure = %+v", d)
	}
	// 公众号：配图清单先列封面，同一配图的两次引用都对应到资产，外部图片没有资产路径。
	res, _, ps = WechatHTML(root, w, Options{Level: "fast", Disclosure: &DisclosureConfig{Placement: PlacementFooter, Template: "模板", ImageTemplate: "{ai_images}/{images} 张"}}, map[string]string{})
	if res == nil || len(ps) > 0 {
		t.Fatalf("WechatHTML: %v", ps)
	}
	list, _ := os.ReadFile(filepath.Join(root, res.ImageList))
	want := "# 配图清单\n\n- 封面：封面；文件：post.assets/post.cover.png；AI 生成\n\n" +
		"1. 店门口：<illustrations/shop.png>（周六）；文件：post.assets/post.shop.png；AI 生成\n" +
		"2. 店门口：illustrations/shop.png；文件：post.assets/post.shop.png；AI 生成\n" +
		"3. 外部：https://e.com/a.png\n"
	if string(list) != want {
		t.Errorf("list:\n%s\nwant:\n%s", list, want)
	}
	if res.Disclosure.Text != "2/2 张" {
		t.Errorf("text = %q", res.Disclosure.Text)
	}
	// 纯文本不复制。
	res, changed, _ = PlainText(root, w, Options{Level: "fast", Disclosure: cfg})
	if res == nil || len(res.Assets) != 0 || res.Disclosure != nil || len(changed) != 1 {
		t.Errorf("text: %+v %v", res, changed)
	}
}

func TestExportAssetsBlockedPath(t *testing.T) {
	root, w := illustratedRoot(t, "正文。\n", illustration.AddInput{ID: "cover", Role: illustration.RoleCover, Data: testPNG(t, 2), Alt: "封面"})
	if err := os.MkdirAll(filepath.Join(root, ".writing-anything/exports/post.assets"), 0o755); err != nil {
		t.Fatal(err)
	}
	// 已存在的资产文件是目录（不是普通文件）：导出文件与清单都不写。
	if err := os.Mkdir(filepath.Join(root, ".writing-anything/exports/post.assets/post.cover.png"), 0o755); err != nil {
		t.Fatal(err)
	}
	for _, f := range []func() (*Result, []string, []workspace.Problem){
		func() (*Result, []string, []workspace.Problem) { return Markdown(root, w, Options{Level: "fast"}) },
		func() (*Result, []string, []workspace.Problem) {
			return WechatHTML(root, w, Options{Level: "fast"}, map[string]string{})
		},
	} {
		if res, _, ps := f(); res != nil || len(ps) == 0 || ps[len(ps)-1].Code != CodeInvalidOutput {
			t.Errorf("result = %+v problems = %+v", res, ps)
		}
	}
	for _, name := range []string{"post.md", "post.html", "post.images.md"} {
		if _, err := os.Stat(filepath.Join(root, ".writing-anything/exports", name)); err == nil {
			t.Errorf("%s written", name)
		}
	}
}

func TestDiscloseWithoutConfig(t *testing.T) {
	if d, ps := disclose("", nil, nil, nil, 1, 1); d != nil || ps != nil {
		t.Error("nil config should not disclose")
	}
	if d, _ := disclose("", nil, nil, &DisclosureConfig{Placement: PlacementBoth}, 1, 3); d == nil || d.Text != "文中 1 张配图由 AI 生成。" {
		t.Errorf("default image template: %+v", d)
	}
}
