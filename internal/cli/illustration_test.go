package cli

import (
	"bytes"
	"encoding/base64"
	"image"
	"image/color"
	"image/jpeg"
	"image/png"
	"os"
	"path/filepath"
	"slices"
	"strings"
	"testing"
)

// writeTestPNG 在 path 写出一张 16×9 的 PNG。
func writeTestPNG(t *testing.T, path string) {
	t.Helper()
	writeTestImage(t, path, "png", 16, 9, 0)
}

// writeTestImage 在 path 写出一张宽 w、高 h 的 PNG 或 JPEG；shade 改变像素使内容不同。
func writeTestImage(t *testing.T, path, format string, w, h int, shade uint8) {
	t.Helper()
	img := image.NewRGBA(image.Rect(0, 0, w, h))
	img.Set(0, 0, color.RGBA{shade, shade, shade, 255})
	var b bytes.Buffer
	var err error
	if format == "png" {
		err = png.Encode(&b, img)
	} else {
		err = jpeg.Encode(&b, img, nil)
	}
	if err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(path, b.Bytes(), 0o644); err != nil {
		t.Fatal(err)
	}
}

func addIllustration(t *testing.T, dir string, args ...string) (int, map[string]any) {
	t.Helper()
	return runJSON(t, append(append([]string{"illustration", "add"}, args...), "--dir", dir, "--json")...)
}

func diagNext(out map[string]any, code string) []string {
	ds, _ := out["diagnostics"].([]any)
	for _, d := range ds {
		m := d.(map[string]any)
		if m["code"] == code {
			var next []string
			for _, n := range m["next"].([]any) {
				next = append(next, n.(string))
			}
			return next
		}
	}
	return nil
}

func TestIllustrationAddListRemove(t *testing.T) {
	dir := newWork(t)
	writeTestImage(t, filepath.Join(dir, "fig.jpg"), "jpeg", 1600, 900, 0)
	code, out := addIllustration(t, dir, "my-post", "--id", "fig-1", "--file", "fig.jpg", "--alt", "店内一角", "--origin", "agent", "--tool", "lemo-opuscar", "--style", "watercolor")
	if code != 0 {
		t.Fatalf("add: %d %v", code, out)
	}
	ill := out["illustration"].(map[string]any)
	if ill["path"] != ".writing-anything/works/my-post/illustrations/fig-1.jpg" || ill["format"] != "jpeg" || ill["width"] != float64(1600) ||
		ill["markdown"] != "![店内一角](illustrations/fig-1.jpg)" || ill["referenced"] != false || ill["content"] != "mood" {
		t.Errorf("illustration = %v", ill)
	}
	if got := out["changedPaths"].([]any); len(got) != 3 {
		t.Errorf("changedPaths = %v", got)
	}
	writeFile(t, filepath.Join(dir, ".writing-anything", "works", "my-post", "draft.md"), "正文。\n\n![店内一角](illustrations/fig-1.jpg)\n")
	code, out = runJSON(t, "illustration", "list", "my-post", "--dir", dir, "--json")
	list := out["illustrations"].([]any)
	if code != 0 || len(list) != 1 || list[0].(map[string]any)["referenced"] != true || len(list[0].(map[string]any)["problems"].([]any)) != 0 {
		t.Fatalf("list = %d %v", code, out)
	}
	// 覆盖图片后 list 照常列出并标出问题，validate 报告 hash-mismatch。
	writeTestImage(t, filepath.Join(dir, ".writing-anything", "works", "my-post", "illustrations", "fig-1.jpg"), "jpeg", 10, 10, 9)
	_, out = runJSON(t, "illustration", "list", "my-post", "--dir", dir, "--json")
	if p := out["illustrations"].([]any)[0].(map[string]any)["problems"].([]any); len(p) != 1 || p[0] != "illustration.hash-mismatch" {
		t.Errorf("problems = %v", p)
	}
	code, out = runJSON(t, "illustration", "remove", "my-post", "fig-1", "--dir", dir, "--json")
	if code != 0 || !slices.Contains(diagCodes(out), "illustration.unregistered-reference") {
		t.Fatalf("remove = %d %v", code, out)
	}
	if next := diagNext(out, "illustration.unregistered-reference"); len(next) == 0 || !strings.Contains(next[0], "illustration list my-post") {
		t.Errorf("next = %v", next)
	}
	code, out = runJSON(t, "illustration", "remove", "my-post", "fig-1", "--dir", dir, "--json")
	if code != 1 || !slices.Contains(diagCodes(out), "illustration.not-found") {
		t.Errorf("remove again = %d %v", code, out)
	}
}

func TestIllustrationAddDiagnostics(t *testing.T) {
	dir := newWork(t)
	run(t, "unit", "add", "my-post", "section", "s1", "--dir", dir)
	writeTestPNG(t, filepath.Join(dir, "fig.png"))
	writeFile(t, filepath.Join(dir, "fake.png"), "RIFF0000WEBPVP8 not an image")
	if code, out := addIllustration(t, dir, "my-post", "--id", "fig-1", "--file", "fig.png", "--alt", "图", "--origin", "agent"); code != 0 {
		t.Fatal(out)
	}
	code, out := addIllustration(t, dir, "my-post", "--id", "fig-1", "--file", "fig.png", "--alt", "图", "--origin", "agent")
	if next := diagNext(out, "artifact.duplicate-id"); code != 1 || len(next) == 0 || !strings.Contains(next[0], "--replace") {
		t.Errorf("duplicate = %d %v", code, out)
	}
	code, out = addIllustration(t, dir, "my-post", "--id", "logo", "--file", "fake.png", "--alt", "标志", "--origin", "author")
	if code != 1 || !slices.Contains(diagCodes(out), "illustration.unsupported-format") {
		t.Errorf("format = %d %v", code, out)
	}
	if _, err := os.Stat(filepath.Join(dir, ".writing-anything", "works", "my-post", "illustrations", "logo.png")); err == nil {
		t.Error("rejected image was written")
	}
	code, out = addIllustration(t, dir, "my-post", "--id", "fig-2", "--file", "fig.png", "--alt", "图")
	if code != 1 || !slices.Contains(diagCodes(out), "artifact.schema-violation") {
		t.Errorf("missing origin = %d %v", code, out)
	}
	code, out = addIllustration(t, dir, "my-post", "--id", "chart", "--content", "infographic", "--file", "fig.png", "--alt", "延迟对比", "--origin", "agent")
	if next := diagNext(out, "illustration.missing-provenance"); code != 1 || len(next) != 2 || !strings.Contains(next[0], "claim list my-post") {
		t.Errorf("provenance = %d %v", code, out)
	}
	runWS(t, dir, "claim", "add", "my-post", "--kind", "data", "--text", "延迟降低了 90%")
	if code, out := addIllustration(t, dir, "my-post", "--id", "chart", "--content", "infographic", "--claims", "claim-1", "--file", "fig.png", "--alt", "延迟对比", "--origin", "agent"); code != 0 {
		t.Errorf("infographic with claim = %d %v", code, out)
	}
	code, out = addIllustration(t, dir, "my-post/s1", "--id", "cover", "--role", "cover", "--file", "fig.png", "--alt", "封面", "--origin", "agent")
	if next := diagNext(out, "illustration.cover-not-root"); code != 1 || len(next) == 0 || !strings.Contains(next[0], "illustration add my-post --id cover") {
		t.Errorf("cover on child = %d %v", code, out)
	}
	if code, out := addIllustration(t, dir, "my-post", "--id", "cover", "--role", "cover", "--file", "fig.png", "--alt", "封面", "--origin", "agent"); code != 0 {
		t.Fatal(out)
	}
	code, out = addIllustration(t, dir, "my-post", "--id", "cover-2", "--role", "cover", "--file", "fig.png", "--alt", "新封面", "--origin", "agent")
	next := diagNext(out, "illustration.cover-exists")
	if code != 1 || len(next) != 2 || !strings.Contains(next[0], "--id cover ") || !strings.Contains(next[0], "--replace") || !strings.Contains(next[1], "illustration remove my-post cover") {
		t.Errorf("second cover = %d %v", code, out)
	}
	if out["illustration"] != nil {
		t.Errorf("illustration = %v, want null", out["illustration"])
	}
}

// 标准输入（MCP 的 input）可以是 base64 文本。
func TestIllustrationAddStdinBase64(t *testing.T) {
	dir := newWork(t)
	path := filepath.Join(t.TempDir(), "fig.png")
	writeTestPNG(t, path)
	data, _ := os.ReadFile(path)
	code, stdout, _ := runIn(t, "data:image/png;base64,"+base64.StdEncoding.EncodeToString(data), "illustration", "add", "my-post", "--id", "fig-1", "--alt", "图", "--origin", "agent", "--dir", dir, "--json")
	if code != 0 || !strings.Contains(stdout, `"format":"png"`) {
		t.Fatalf("stdin base64 = %d %s", code, stdout)
	}
	got, _ := os.ReadFile(filepath.Join(dir, ".writing-anything", "works", "my-post", "illustrations", "fig-1.png"))
	if !bytes.Equal(got, data) {
		t.Error("decoded bytes differ")
	}
}

func TestValidateIllustrations(t *testing.T) {
	dir := newWork(t)
	run(t, "unit", "add", "my-post", "section", "s1", "--dir", dir)
	writeTestPNG(t, filepath.Join(dir, "fig.png"))
	if code, out := addIllustration(t, dir, "my-post/s1", "--id", "fig-1", "--file", "fig.png", "--alt", "图", "--origin", "agent"); code != 0 {
		t.Fatal(out)
	}
	unit := filepath.Join(dir, ".writing-anything", "works", "my-post", "units", "s1")
	writeFile(t, filepath.Join(unit, "draft.md"), "正文。\n\n![图](illustrations/fig-1.png)\n\n![缺](illustrations/missing.jpg)\n")
	code, out := runJSON(t, "validate", "--dir", dir, "--json")
	if code != 1 || !slices.Contains(diagCodes(out), "illustration.unregistered-reference") {
		t.Fatalf("validate = %d %v", code, out)
	}
	if next := diagNext(out, "illustration.unregistered-reference"); len(next) != 2 || !strings.Contains(next[0], "illustration list my-post/s1") {
		t.Errorf("next = %v", next)
	}
	checked := out["checked"].([]any)
	if !slices.Contains(checked, any(".writing-anything/works/my-post/units/s1/illustrations.yaml")) {
		t.Errorf("checked = %v", checked)
	}
	writeFile(t, filepath.Join(unit, "draft.md"), "正文。\n\n![图](illustrations/fig-1.png)\n")
	writeTestImage(t, filepath.Join(unit, "illustrations", "fig-1.png"), "png", 4, 4, 7)
	code, out = runJSON(t, "validate", ".writing-anything/works/my-post/units/s1/illustrations.yaml", "--dir", dir, "--json")
	if code != 1 || !slices.Equal(diagCodes(out), []string{"illustration.hash-mismatch"}) {
		t.Errorf("validate path = %d %v", code, diagCodes(out))
	}
	if next := diagNext(out, "illustration.hash-mismatch"); len(next) == 0 || !strings.Contains(next[0], "illustration list my-post/s1") {
		t.Errorf("hash next = %v", next)
	}
}

const illustratedDraft = "# 周六的店\n\n店里新来了一台烘豆机。\n\n![店内一角](illustrations/fig-1.jpg \"周六的店\")\n"

// illustratedWork 返回推进到审稿阶段、已审稿、正文引用 fig-1 且登记了 fig-1 与封面 cover 的作品 my-post（尚未验收）。
func illustratedWork(t *testing.T) string {
	t.Helper()
	dir := newWork(t)
	writeFile(t, filepath.Join(dir, ".writing-anything", "works", "my-post", "brief.yaml"), completeBrief)
	mustRunWS(t, dir, "advance", "my-post")
	if code, res := runWSIn(t, dir, illustratedDraft, "draft", "write", "my-post"); code != 0 {
		t.Fatalf("draft write: %+v", res)
	}
	mustRunWS(t, dir, "advance", "my-post")
	if code, res := runWSIn(t, dir, "rubric:\n  - {item: clarity, verdict: pass, evidence: 清楚, paragraphs: [1]}\n", "review", "record", "my-post"); code != 0 {
		t.Fatalf("review record: %+v", res)
	}
	writeTestImage(t, filepath.Join(dir, "fig.jpg"), "jpeg", 1600, 900, 0)
	writeTestImage(t, filepath.Join(dir, "cover.jpg"), "jpeg", 1800, 766, 0)
	if code, out := addIllustration(t, dir, "my-post", "--id", "fig-1", "--file", "fig.jpg", "--alt", "店内一角", "--title", "周六的店", "--origin", "agent", "--tool", "lemo-opuscar"); code != 0 {
		t.Fatal(out)
	}
	if code, out := addIllustration(t, dir, "my-post", "--id", "cover", "--role", "cover", "--file", "cover.jpg", "--alt", "周六的店", "--origin", "agent"); code != 0 {
		t.Fatal(out)
	}
	return dir
}

func TestAcceptLocksIllustrations(t *testing.T) {
	dir := illustratedWork(t)
	_, show := runJSON(t, "review", "show", "my-post", "--dir", dir, "--json")
	gate := show["gate"].(map[string]any)
	digest, _ := gate["illustrationsSha256"].(string)
	if len(digest) != 64 || gate["ready"] != true {
		t.Fatalf("gate = %v", gate)
	}
	code, out := runJSON(t, "accept", "my-post", "--confirmed-by-author", "--confirmation", "作者：图文都可以", "--dir", dir, "--json")
	if code != 0 || out["acceptance"].(map[string]any)["illustrationsSha256"] != digest {
		t.Fatalf("accept = %d %v", code, out)
	}
	if !strings.Contains(readFile(t, filepath.Join(dir, ".writing-anything", "works", "my-post", "acceptance.yaml")), "illustrations_sha256: "+digest) {
		t.Error("acceptance.yaml lacks illustrations_sha256")
	}
	// 替换封面后验收失效：review show 标出过期，advance 报告 acceptance.stale。
	writeTestImage(t, filepath.Join(dir, "cover2.jpg"), "jpeg", 900, 383, 5)
	if code, out := addIllustration(t, dir, "my-post", "--id", "cover", "--role", "cover", "--file", "cover2.jpg", "--alt", "周六的店", "--origin", "agent", "--replace"); code != 0 {
		t.Fatal(out)
	}
	_, show = runJSON(t, "review", "show", "my-post", "--dir", dir, "--json")
	acc := show["acceptance"].(map[string]any)
	if acc["stale"] != true || acc["illustrationsSha256"] == show["gate"].(map[string]any)["illustrationsSha256"] {
		t.Errorf("acceptance = %v", acc)
	}
	code, out = runJSON(t, "advance", "my-post", "--dir", dir, "--json")
	if code != 1 || !slices.Contains(diagCodes(out), "acceptance.stale") {
		t.Errorf("advance = %d %v", code, out)
	}
}

func TestIllustrationBlocksAccept(t *testing.T) {
	dir := illustratedWork(t)
	writeTestImage(t, filepath.Join(dir, ".writing-anything", "works", "my-post", "illustrations", "fig-1.jpg"), "jpeg", 10, 10, 9)
	code, out := runJSON(t, "accept", "my-post", "--confirmed-by-author", "--confirmation", "作者：图文都可以", "--dir", dir, "--json")
	if code != 1 || !slices.Contains(diagCodes(out), "illustration.hash-mismatch") {
		t.Fatalf("accept = %d %v", code, out)
	}
	next := diagNext(out, "illustration.hash-mismatch")
	if len(next) != 2 || !strings.Contains(next[0], "illustration list my-post") || !strings.Contains(next[1], "review show my-post") {
		t.Errorf("next = %v", next)
	}
	if _, err := os.Stat(filepath.Join(dir, ".writing-anything", "works", "my-post", "acceptance.yaml")); err == nil {
		t.Error("acceptance written despite blocker")
	}
}

func TestAcceptWithoutIllustrationsUnchanged(t *testing.T) {
	dir := acceptedWork(t, "正文。\n\n![图标](https://example.com/icon.png)\n")
	data := readFile(t, filepath.Join(dir, ".writing-anything", "works", "my-post", "acceptance.yaml"))
	if strings.Contains(data, "illustrations_sha256") {
		t.Errorf("acceptance.yaml = %s", data)
	}
	_, show := runJSON(t, "review", "show", "my-post", "--dir", dir, "--json")
	if show["gate"].(map[string]any)["illustrationsSha256"] != nil || show["acceptance"].(map[string]any)["stale"] != false {
		t.Errorf("show = %v", show)
	}
	// 验收时没有配图、之后登记了一张：验收失效。
	writeTestPNG(t, filepath.Join(dir, "fig.png"))
	addIllustration(t, dir, "my-post", "--id", "fig-1", "--file", "fig.png", "--alt", "图", "--origin", "agent")
	if code, out := runJSON(t, "advance", "my-post", "--dir", dir, "--json"); code != 1 || !slices.Contains(diagCodes(out), "acceptance.stale") {
		t.Errorf("advance = %d %v", code, out)
	}
}
