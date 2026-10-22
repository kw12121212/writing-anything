package cli

import (
	"bytes"
	"encoding/json"
	"net/http"
	"os"
	"path/filepath"
	"strings"
	"testing"

	"github.com/kw12121212/writing-anything/internal/preview"
)

// 本文件是阅读页显示配图（Illustration）与封面图（Cover Image）的测试：preview-static、preview-content、preview-serve、
// preview-illustration 与 preview-page-json 中与配图相关的行为。

func illustrationFile(dir, name string) string {
	return filepath.Join(dir, ".writing-anything", "works", "my-post", "illustrations", name)
}

func staticPreview(t *testing.T, dir string) string {
	t.Helper()
	code, res := runPreviewJSON(t, dir, "my-post")
	if code != 0 || res.Preview == nil {
		t.Fatalf("preview: %d %+v", code, res)
	}
	return readFile(t, filepath.Join(dir, filepath.FromSlash(*res.Preview.Path)))
}

func TestPreviewStaticIllustrations(t *testing.T) {
	dir := illustratedWork(t)
	writeTestPNG(t, filepath.Join(dir, "chart.png"))
	runWS(t, dir, "claim", "add", "my-post", "--kind", "data", "--text", "延迟降低了 90%")
	if code, out := addIllustration(t, dir, "my-post", "--id", "chart", "--content", "infographic", "--claims", "claim-1", "--file", "chart.png", "--alt", "延迟对比", "--origin", "agent"); code != 0 {
		t.Fatal(out)
	}
	html := staticPreview(t, dir)
	// static-embedded-illustration：封面、fig-1 与未引用的 chart 都以 data URI 内嵌，不引用 illustrations/ 路径或外部资源。
	if n := strings.Count(html, `src="data:image/`); n != 3 {
		t.Errorf("embedded images = %d, want 3", n)
	}
	if strings.Contains(html, `src="illustrations/`) || strings.Contains(html, `src="http`) || !strings.Contains(html, "img-src data:") {
		t.Error("static page references images outside the page or lacks img-src data:")
	}
	cover, para1, fig1, unused := strings.Index(html, "封面</span>"), strings.Index(html, `data-index="1"`), strings.Index(html, `data-id="fig-1"`), strings.Index(html, "未引用</span>")
	if cover < 0 || para1 < 0 || fig1 < 0 || unused < 0 || !(cover < para1 && para1 < fig1 && fig1 < unused) {
		t.Errorf("order: cover %d, paragraph 1 %d, fig-1 %d, unreferenced %d", cover, para1, fig1, unused)
	}
	// content-illustration-after-paragraph：信息图列出类型、出处与关联的论断。
	if !strings.Contains(html, "chart · 延迟对比 · 信息图（Infographic） · 出处 agent · 论断 claim-1") ||
		!strings.Contains(html, "fig-1 · 店内一角 · 氛围图（Mood Image） · 出处 agent · 周六的店") {
		t.Error("figure captions missing")
	}
}

func TestPreviewIllustrationProblems(t *testing.T) {
	dir := illustratedWork(t)
	writeFile(t, filepath.Join(dir, ".writing-anything", "works", "my-post", "draft.md"),
		"![鬼](illustrations/ghost.png)\n\n![店内一角](illustrations/fig-1.jpg)\n")
	writeTestImage(t, illustrationFile(dir, "fig-1.jpg"), "jpeg", 10, 10, 9)
	html := staticPreview(t, dir)
	if !strings.Contains(html, "illustrations/ghost.png 不是已登记的配图（未登记）") || !strings.Contains(html, "配图 fig-1 不可用（哈希不符）") {
		t.Errorf("problem notes missing:\n%s", html)
	}
	if strings.Contains(html, `data-id="fig-1"`) {
		t.Error("unavailable fig-1 should not be shown")
	}
	// illustrations.yaml 不通过校验时在单元开头说明原因，不显示配图。
	writeFile(t, filepath.Join(dir, ".writing-anything", "works", "my-post", "illustrations.yaml"), "bogus: true\n")
	if html := staticPreview(t, dir); !strings.Contains(html, "配图登记 illustrations.yaml 不通过校验") || strings.Contains(html, "<img") {
		t.Error("invalid registry not reported")
	}
}

func TestPreviewEmbedBudget(t *testing.T) {
	dir := previewWork(t, "![一](illustrations/fig-1.png)\n\n![二](illustrations/fig-2.png)\n\n![三](illustrations/fig-3.png)\n\n![四](illustrations/fig-4.png)\n")
	src := filepath.Join(dir, "base.png")
	writeTestPNG(t, src)
	base, _ := os.ReadFile(src)
	for i, id := range []string{"fig-1", "fig-2", "fig-3", "fig-4"} {
		// PNG 文件头之后补足到 9 MiB：仍按文件头识别为 PNG，内容各不相同。
		data := append(bytes.Clone(base), bytes.Repeat([]byte{byte(i + 1)}, 9<<20-len(base))...)
		p := filepath.Join(dir, id+".png")
		if err := os.WriteFile(p, data, 0o644); err != nil {
			t.Fatal(err)
		}
		if code, out := addIllustration(t, dir, "my-post", "--id", id, "--file", id+".png", "--alt", "图", "--origin", "agent"); code != 0 {
			t.Fatal(out)
		}
	}
	html := staticPreview(t, dir)
	if n := strings.Count(html, `src="data:image/png;base64,`); n != 3 {
		t.Errorf("embedded = %d, want 3", n)
	}
	if !strings.Contains(html, "配图过大，未内嵌：.writing-anything/works/my-post/illustrations/fig-4.png") {
		t.Error("fig-4 should be reported as not embedded")
	}
}

func TestPreviewServeIllustrations(t *testing.T) {
	dir := illustratedWork(t)
	before := workspaceFiles(t, dir)
	s := newPage(dir, "my-post", &bytes.Buffer{})
	// serve-illustration-relative：页面中的图像地址相对页面、带令牌，不内嵌；CSP 放行同源与 data: 图片。
	rec := pageRequest(s, http.MethodGet, "/?token=page-token")
	html := rec.Body.String()
	if !strings.Contains(html, `src="illustration?unit=my-post&amp;id=fig-1&amp;token=page-token"`) || strings.Contains(html, "data:image/") {
		t.Errorf("serve page images:\n%s", html)
	}
	if csp := rec.Header().Get("Content-Security-Policy"); !strings.Contains(csp, "img-src 'self' data:") || !strings.Contains(csp, "script-src 'sha256-") {
		t.Errorf("CSP = %q", csp)
	}
	// illustration-ok：返回图片原始字节与指定的响应头；HEAD 不带正文。
	rec = pageRequest(s, http.MethodGet, "/illustration?unit=my-post&id=fig-1&token=page-token")
	want, _ := os.ReadFile(illustrationFile(dir, "fig-1.jpg"))
	h := rec.Header()
	if rec.Code != 200 || h.Get("Content-Type") != "image/jpeg" || h.Get("Cache-Control") != "no-store" || h.Get("X-Content-Type-Options") != "nosniff" ||
		h.Get("Referrer-Policy") != "no-referrer" || !bytes.Equal(rec.Body.Bytes(), want) {
		t.Fatalf("GET /illustration: %d %v", rec.Code, h)
	}
	if rec := pageRequest(s, http.MethodHead, "/illustration?unit=my-post&id=cover&token=page-token"); rec.Code != 200 || rec.Body.Len() != 0 {
		t.Errorf("HEAD = %d, body %d bytes", rec.Code, rec.Body.Len())
	}
	// illustration-rejected 与 illustration-method。
	for _, c := range []struct {
		method, path string
		mutate       func(*http.Request)
		code         int
	}{
		{http.MethodGet, "/illustration?unit=my-post&id=fig-1", nil, 403},
		{http.MethodGet, "/illustration?unit=my-post&id=fig-1&token=wrong", nil, 403},
		{http.MethodGet, "/illustration?unit=my-post&id=fig-1&token=page-token", func(r *http.Request) { r.Host = "evil.example:4170" }, 403},
		{http.MethodPost, "/illustration?unit=my-post&id=fig-1&token=page-token", func(r *http.Request) { r.Header.Set("X-WA-Token", "page-token") }, 405},
		{http.MethodGet, "/illustration?unit=my-post&id=ghost&token=page-token", nil, 404},
		{http.MethodGet, "/illustration?unit=other-post&id=fig-1&token=page-token", nil, 404},
		{http.MethodGet, "/illustration?id=fig-1&token=page-token", nil, 404},
	} {
		var ms []func(*http.Request)
		if c.mutate != nil {
			ms = append(ms, c.mutate)
		}
		rec := pageRequest(s, c.method, c.path, ms...)
		if rec.Code != c.code || bytes.Contains(rec.Body.Bytes(), want[:16]) {
			t.Errorf("%s %s = %d, want %d", c.method, c.path, rec.Code, c.code)
		}
	}
	// illustration-not-available：哈希不符的配图返回 404。
	if !equalFiles(before, workspaceFiles(t, dir)) {
		t.Error("serving changed the workspace")
	}
	writeTestImage(t, illustrationFile(dir, "fig-1.jpg"), "jpeg", 10, 10, 9)
	if rec := pageRequest(s, http.MethodGet, "/illustration?unit=my-post&id=fig-1&token=page-token"); rec.Code != 404 {
		t.Errorf("hash mismatch = %d, want 404", rec.Code)
	}
}

func equalFiles(a, b map[string]string) bool {
	if len(a) != len(b) {
		return false
	}
	for k, v := range a {
		if b[k] != v {
			return false
		}
	}
	return true
}

type illustrationPage struct {
	Units []struct {
		Notes      []string `json:"notes"`
		Paragraphs []struct {
			Illustrations []string `json:"illustrations"`
		} `json:"paragraphs"`
		Illustrations []struct {
			ID         string  `json:"id"`
			Role       string  `json:"role"`
			Title      *string `json:"title"`
			Referenced bool    `json:"referenced"`
			Available  bool    `json:"available"`
			URL        *string `json:"url"`
		} `json:"illustrations"`
	} `json:"units"`
}

func TestPreviewPageJSONIllustrations(t *testing.T) {
	dir := illustratedWork(t)
	writeTestPNG(t, filepath.Join(dir, "fig2.png"))
	if code, out := addIllustration(t, dir, "my-post", "--id", "fig-2", "--file", "fig2.png", "--alt", "二", "--origin", "author"); code != 0 {
		t.Fatal(out)
	}
	os.Remove(illustrationFile(dir, "fig-2.png"))
	writeFile(t, filepath.Join(dir, ".writing-anything", "works", "my-post", "draft.md"),
		"开头。\n\n![店内一角](illustrations/fig-1.jpg) 与 ![再一次](illustrations/fig-1.jpg) ![鬼](illustrations/ghost.png)\n")
	s := newPage(dir, "my-post", &bytes.Buffer{})
	body := pageRequest(s, http.MethodGet, "/page.json?token=page-token").Body.Bytes()
	if err := preview.ValidateJSON(body); err != nil {
		t.Fatalf("schema: %v\n%s", err, body)
	}
	if bytes.Contains(body, []byte("page-token")) {
		t.Error("page.json leaks the token")
	}
	var p illustrationPage
	json.Unmarshal(body, &p)
	u := p.Units[0]
	if len(u.Illustrations) != 3 {
		t.Fatalf("illustrations = %+v", u.Illustrations)
	}
	fig1, cover, fig2 := u.Illustrations[0], u.Illustrations[1], u.Illustrations[2]
	if fig1.ID != "fig-1" || fig1.Role != "body" || !fig1.Referenced || !fig1.Available || fig1.URL == nil || *fig1.URL != "illustration?unit=my-post&id=fig-1" || fig1.Title == nil {
		t.Errorf("fig-1 = %+v", fig1)
	}
	if cover.ID != "cover" || cover.Role != "cover" || cover.Referenced || cover.URL == nil {
		t.Errorf("cover = %+v", cover)
	}
	if fig2.ID != "fig-2" || fig2.Available || fig2.URL != nil {
		t.Errorf("fig-2 = %+v", fig2)
	}
	if len(u.Paragraphs[0].Illustrations) != 0 || strings.Join(u.Paragraphs[1].Illustrations, ",") != "fig-1" {
		t.Errorf("paragraph illustrations = %+v", u.Paragraphs)
	}
	if !strings.Contains(strings.Join(u.Notes, "；"), "illustrations/ghost.png 不是已登记的配图") {
		t.Errorf("notes = %v", u.Notes)
	}
}

// page-json-no-illustrations：没有配图登记时只多出空数组。
func TestPreviewPageJSONNoIllustrations(t *testing.T) {
	dir := pageWork(t)
	body := pageRequest(newPage(dir, "my-post", &bytes.Buffer{}), http.MethodGet, "/page.json?token=page-token").Body.String()
	if !strings.Contains(body, `"illustrations":[]`) || strings.Contains(body, `"illustrations":null`) {
		t.Errorf("page.json = %s", body)
	}
	var p illustrationPage
	json.Unmarshal([]byte(body), &p)
	for _, u := range p.Units {
		if u.Illustrations == nil || len(u.Illustrations) != 0 {
			t.Errorf("unit illustrations = %+v", u.Illustrations)
		}
		for _, para := range u.Paragraphs {
			if para.Illustrations == nil || len(para.Illustrations) != 0 {
				t.Errorf("paragraph illustrations = %+v", para.Illustrations)
			}
		}
	}
	if html := pageGet(t, newPage(dir, "my-post", &bytes.Buffer{})); strings.Contains(html, "<img") || strings.Contains(html, "figure.ill") {
		t.Error("page without illustrations should have no images or illustration styles")
	}
}
