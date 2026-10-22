package illustration

import (
	"bytes"
	"image"
	"image/color"
	"image/jpeg"
	"image/png"
	"os"
	"path/filepath"
	"slices"
	"strings"
	"testing"
	"time"

	"github.com/kw12121212/writing-anything/internal/workspace"
)

var now = time.Date(2026, 10, 22, 8, 0, 0, 0, time.UTC)

const rootDir = ".writing-anything/works/post"

func pngBytes(t *testing.T, w, h int, c uint8) []byte {
	t.Helper()
	img := image.NewRGBA(image.Rect(0, 0, w, h))
	img.Set(0, 0, color.RGBA{c, c, c, 255})
	var b bytes.Buffer
	if err := png.Encode(&b, img); err != nil {
		t.Fatal(err)
	}
	return b.Bytes()
}

func jpegBytes(t *testing.T, w, h int) []byte {
	t.Helper()
	var b bytes.Buffer
	if err := jpeg.Encode(&b, image.NewRGBA(image.Rect(0, 0, w, h)), nil); err != nil {
		t.Fatal(err)
	}
	return b.Bytes()
}

func setup(t *testing.T) string {
	t.Helper()
	root := t.TempDir()
	if err := os.MkdirAll(filepath.Join(root, filepath.FromSlash(rootDir)), 0o755); err != nil {
		t.Fatal(err)
	}
	return root
}

func noDir() ([]string, []workspace.Problem) { return nil, nil }

func yes(string) bool { return true }
func no(string) bool  { return false }

func add(t *testing.T, root string, in AddInput) (*AddResult, []string, []workspace.Problem) {
	t.Helper()
	if in.Role == "" {
		in.Role = RoleBody
	}
	if in.Content == "" {
		in.Content = ContentMood
	}
	if in.Origin == "" {
		in.Origin = OriginAgent
	}
	if in.Alt == "" {
		in.Alt = "店内一角"
	}
	if in.Claim == nil {
		in.Claim = yes
	}
	if in.Source == nil {
		in.Source = yes
	}
	return Add(root, rootDir, in, noDir, now)
}

func mustAdd(t *testing.T, root string, in AddInput) *AddResult {
	t.Helper()
	r, _, ps := add(t, root, in)
	if len(ps) > 0 {
		t.Fatalf("Add(%s) problems = %+v", in.ID, ps)
	}
	return r
}

func codes(ps []workspace.Problem) []string {
	var out []string
	for _, p := range ps {
		out = append(out, p.Code)
	}
	return out
}

func TestDetect(t *testing.T) {
	if f, w, h := Detect(pngBytes(t, 16, 9, 0)); f != FormatPNG || w != 16 || h != 9 {
		t.Errorf("png = %s %d %d", f, w, h)
	}
	if f, w, h := Detect(jpegBytes(t, 20, 10)); f != FormatJPEG || w != 20 || h != 10 {
		t.Errorf("jpeg = %s %d %d", f, w, h)
	}
	for _, data := range [][]byte{[]byte("RIFF....WEBPVP8 "), []byte("\x89PNG\r\n\x1a\n broken"), nil} {
		if f, _, _ := Detect(data); f != "" {
			t.Errorf("Detect(%q) = %s, want none", data, f)
		}
	}
}

func TestAddBodyAndList(t *testing.T) {
	root := setup(t)
	data := jpegBytes(t, 1600, 900)
	tool, style := "lemo-opuscar", "watercolor"
	r, changed, ps := add(t, root, AddInput{ID: "fig-1", Data: data, Tool: &tool, Style: &style})
	if len(ps) > 0 {
		t.Fatal(ps)
	}
	if r.Item.File != "illustrations/fig-1.jpg" || r.Item.Format != FormatJPEG || r.Item.Width != 1600 || r.Item.Height != 900 || Markdown(r.Item) != "![店内一角](illustrations/fig-1.jpg)" {
		t.Errorf("item = %+v", r.Item)
	}
	want := []string{rootDir + "/illustrations", rootDir + "/illustrations/fig-1.jpg", rootDir + "/illustrations.yaml"}
	if !slices.Equal(changed, want) {
		t.Errorf("changed = %v, want %v", changed, want)
	}
	got, _ := os.ReadFile(filepath.Join(root, rootDir, "illustrations", "fig-1.jpg"))
	if !bytes.Equal(got, data) {
		t.Error("image bytes differ")
	}
	items, ok, ps := Load(root, rootDir)
	if !ok || len(ps) > 0 || len(items) != 1 || *items[0].Tool != tool || items[0].Claims == nil {
		t.Fatalf("Load = %+v %v %v", items, ok, ps)
	}
}

func TestAddRejections(t *testing.T) {
	root := setup(t)
	mustAdd(t, root, AddInput{ID: "fig-1", Data: pngBytes(t, 4, 4, 0)})
	big := append(pngBytes(t, 4, 4, 0), make([]byte, MaxBytes)...)
	cases := []struct {
		name  string
		in    AddInput
		code  string
		field string
	}{
		{"format", AddInput{ID: "logo", Data: []byte("RIFF....WEBP")}, CodeUnsupportedFormat, "file"},
		{"too-large", AddInput{ID: "big", Data: big}, CodeTooLarge, "file"},
		{"bad-id", AddInput{ID: "Bad_ID", Data: pngBytes(t, 4, 4, 0)}, "artifact.schema-violation", "id"},
		{"multiline-alt", AddInput{ID: "x", Alt: "第一行\n第二行", Data: pngBytes(t, 4, 4, 0)}, "artifact.schema-violation", "alt"},
		{"long-alt", AddInput{ID: "x", Alt: strings.Repeat("图", 201), Data: pngBytes(t, 4, 4, 0)}, "artifact.schema-violation", "alt"},
		{"bad-origin", AddInput{ID: "x", Origin: "ai", Data: pngBytes(t, 4, 4, 0)}, "artifact.schema-violation", "origin"},
		{"duplicate", AddInput{ID: "fig-1", Data: pngBytes(t, 4, 4, 1)}, "artifact.duplicate-id", "id"},
		{"replace-missing", AddInput{ID: "fig-9", Replace: true, Data: pngBytes(t, 4, 4, 0)}, CodeNotFound, "id"},
		{"replace-role", AddInput{ID: "fig-1", Replace: true, Role: RoleCover, IsRoot: true, Data: pngBytes(t, 4, 4, 0)}, "artifact.schema-violation", "role"},
		{"cover-child", AddInput{ID: "cover", Role: RoleCover, Data: pngBytes(t, 4, 4, 0)}, CodeCoverNotRoot, "role"},
		{"infographic", AddInput{ID: "chart", Content: ContentInfographic, Data: pngBytes(t, 4, 4, 0)}, CodeMissingProvenance, "content"},
		{"unknown-claim", AddInput{ID: "chart", Content: ContentInfographic, Claims: []string{"claim-1"}, Claim: no, Data: pngBytes(t, 4, 4, 0)}, CodeUnknownClaim, "claims[0]"},
		{"unknown-source", AddInput{ID: "chart", Sources: []string{"src-x"}, Source: no, Data: pngBytes(t, 4, 4, 0)}, CodeUnknownSource, "sources[0]"},
	}
	before, _ := os.ReadFile(filepath.Join(root, rootDir, FileName))
	for _, c := range cases {
		t.Run(c.name, func(t *testing.T) {
			_, _, ps := add(t, root, c.in)
			if len(ps) != 1 || ps[0].Code != c.code || ps[0].Field != c.field {
				t.Fatalf("problems = %+v, want %s %s", ps, c.code, c.field)
			}
		})
	}
	after, _ := os.ReadFile(filepath.Join(root, rootDir, FileName))
	if !bytes.Equal(before, after) {
		t.Error("illustrations.yaml changed by rejected adds")
	}
	if entries, _ := os.ReadDir(filepath.Join(root, rootDir, "illustrations")); len(entries) != 1 {
		t.Errorf("illustrations/ has %d entries, want 1", len(entries))
	}
}

func TestCover(t *testing.T) {
	root := setup(t)
	r := mustAdd(t, root, AddInput{ID: "cover", Role: RoleCover, IsRoot: true, Data: jpegBytes(t, 1800, 766)})
	if Markdown(r.Item) != "" {
		t.Error("cover markdown should be empty")
	}
	_, _, ps := add(t, root, AddInput{ID: "cover-2", Role: RoleCover, IsRoot: true, Data: jpegBytes(t, 10, 10)})
	if len(ps) != 1 || ps[0].Code != CodeCoverExists || !strings.Contains(ps[0].Message, "cover") {
		t.Fatalf("second cover = %+v", ps)
	}
	mustAdd(t, root, AddInput{ID: "cover", Role: RoleCover, IsRoot: true, Replace: true, Data: jpegBytes(t, 900, 383)})
}

func TestReplaceKeepsPositionAndComments(t *testing.T) {
	root := setup(t)
	mustAdd(t, root, AddInput{ID: "fig-1", Data: jpegBytes(t, 4, 4)})
	mustAdd(t, root, AddInput{ID: "fig-2", Data: jpegBytes(t, 4, 4)})
	rel := filepath.Join(root, rootDir, FileName)
	data, _ := os.ReadFile(rel)
	os.WriteFile(rel, append([]byte("# 作者的注释\n"), data...), 0o644)
	r, changed, ps := add(t, root, AddInput{ID: "fig-1", Replace: true, Data: pngBytes(t, 8, 8, 3)})
	if len(ps) > 0 {
		t.Fatal(ps)
	}
	if r.PreviousPath != rootDir+"/illustrations/fig-1.jpg" || !slices.Contains(changed, r.PreviousPath) {
		t.Errorf("previous = %q changed = %v", r.PreviousPath, changed)
	}
	if _, err := os.Stat(filepath.Join(root, rootDir, "illustrations", "fig-1.jpg")); err == nil {
		t.Error("old jpg not removed")
	}
	items, _, _ := Load(root, rootDir)
	if items[0].ID != "fig-1" || items[0].Format != FormatPNG || items[1].ID != "fig-2" {
		t.Errorf("items = %+v", items)
	}
	data, _ = os.ReadFile(rel)
	if !strings.Contains(string(data), "作者的注释") {
		t.Error("comment lost")
	}
}

func TestCheck(t *testing.T) {
	root := setup(t)
	mustAdd(t, root, AddInput{ID: "fig-1", Data: jpegBytes(t, 4, 4)})
	mustAdd(t, root, AddInput{ID: "fig-2", Data: jpegBytes(t, 4, 4)})
	mustAdd(t, root, AddInput{ID: "chart", Content: ContentInfographic, Claims: []string{"claim-1"}, Data: pngBytes(t, 4, 4, 0)})
	mustAdd(t, root, AddInput{ID: "cover", Role: RoleCover, IsRoot: true, Data: jpegBytes(t, 4, 4)})
	os.WriteFile(filepath.Join(root, rootDir, "illustrations", "fig-2.jpg"), []byte("changed"), 0o644)
	os.WriteFile(filepath.Join(root, rootDir, "illustrations", "stray.png"), []byte("x"), 0o644)
	draft := []byte("![一](illustrations/fig-1.jpg)\n\n![二](illustrations/fig-2.jpg)\n\n![缺](illustrations/missing.jpg)\n\n![封面](illustrations/cover.jpg)\n\n```\n![代码](illustrations/code.png)\n```\n")
	c := Check(Context{Root: root, Dir: rootDir, IsRoot: true, Draft: draft, Claim: no, Source: yes})
	got := codes(c.Problems())
	for _, want := range []string{CodeHashMismatch, CodeUnknownClaim, CodeUnregisteredReference, CodeCoverReferenced, CodeUnreferenced, CodeOrphanFile} {
		if !slices.Contains(got, want) {
			t.Errorf("codes = %v, missing %s", got, want)
		}
	}
	if slices.Contains(got, CodeFileMissing) || len(c.Errors()) != 3 {
		t.Errorf("errors = %+v", c.Errors())
	}
	if !c.Referenced["fig-1"] || c.Referenced["chart"] || c.Referenced["cover"] {
		t.Errorf("referenced = %v", c.Referenced)
	}
	if codes := c.Codes(1); !slices.Equal(codes, []string{CodeHashMismatch}) {
		t.Errorf("fig-2 codes = %v", codes)
	}
	if child := Check(Context{Root: root, Dir: rootDir, IsRoot: false, Claim: yes, Source: yes}); !slices.Contains(codes(child.Errors()), CodeCoverNotRoot) {
		t.Errorf("child check = %v", codes(child.Errors()))
	}
	empty := Check(Context{Root: root, Dir: rootDir + "/units/none", Draft: []byte("![x](https://e.com/a.png)\n")})
	if len(empty.Issues) != 0 || empty.Exists {
		t.Errorf("no-registry check = %+v", empty)
	}
	if Active(root, rootDir+"/units/none", []byte("![x](https://e.com/a.png)")) {
		t.Error("external image should not activate checks")
	}
}

func TestDigest(t *testing.T) {
	root := setup(t)
	if Digest(root, rootDir) != nil {
		t.Fatal("digest without registry should be nil")
	}
	mustAdd(t, root, AddInput{ID: "fig-1", Data: jpegBytes(t, 4, 4)})
	mustAdd(t, root, AddInput{ID: "fig-2", Data: pngBytes(t, 4, 4, 0)})
	d1 := *Digest(root, rootDir)
	rel := filepath.Join(root, rootDir, FileName)
	data, _ := os.ReadFile(rel)
	// 对调两项并加注释：摘要不变。
	s := string(data)
	i1, i2 := strings.Index(s, "  - id: fig-1"), strings.Index(s, "  - id: fig-2")
	swapped := "# 注释\n" + s[:i1] + s[i2:] + s[i1:i2]
	os.WriteFile(rel, []byte(swapped), 0o644)
	if items, _, ps := Load(root, rootDir); len(ps) > 0 || items[0].ID != "fig-2" {
		t.Fatalf("swap failed: %v %v", items, ps)
	}
	if d := *Digest(root, rootDir); d != d1 {
		t.Error("digest changed by reorder")
	}
	mustAdd(t, root, AddInput{ID: "fig-1", Replace: true, Data: jpegBytes(t, 6, 6)})
	if d := *Digest(root, rootDir); d == d1 {
		t.Error("digest unchanged after replace")
	}
	os.WriteFile(rel, []byte("schema_version: 1\nillustrations: []\n"), 0o644)
	if Digest(root, rootDir) != nil {
		t.Error("empty registry digest should be nil")
	}
	os.WriteFile(rel, []byte("bogus: true\n"), 0o644)
	if Digest(root, rootDir) == nil {
		t.Error("invalid registry digest should be non-nil")
	}
}

func TestRemove(t *testing.T) {
	root := setup(t)
	mustAdd(t, root, AddInput{ID: "fig-1", Data: jpegBytes(t, 4, 4)})
	it, changed, ps := Remove(root, rootDir, "fig-1")
	if len(ps) > 0 || it.ID != "fig-1" || !slices.Equal(changed, []string{rootDir + "/illustrations.yaml", rootDir + "/illustrations/fig-1.jpg"}) {
		t.Fatalf("Remove = %+v %v %v", it, changed, ps)
	}
	if items, ok, _ := Load(root, rootDir); !ok || len(items) != 0 {
		t.Errorf("after remove = %+v %v", items, ok)
	}
	if _, _, ps := Remove(root, rootDir, "fig-1"); len(ps) != 1 || ps[0].Code != CodeNotFound {
		t.Errorf("remove missing = %+v", ps)
	}
}

func TestSymlinkedImageDirRejected(t *testing.T) {
	root := setup(t)
	outside := t.TempDir()
	if err := os.Symlink(outside, filepath.Join(root, rootDir, "illustrations")); err != nil {
		t.Skip("symlinks unavailable:", err)
	}
	_, _, ps := add(t, root, AddInput{ID: "fig-1", Data: jpegBytes(t, 4, 4)})
	if len(ps) != 1 || ps[0].Code != workspace.CodeWriteFailed {
		t.Fatalf("problems = %+v", ps)
	}
	if entries, _ := os.ReadDir(outside); len(entries) != 0 {
		t.Error("wrote through symlink")
	}
	if _, err := os.Stat(filepath.Join(root, rootDir, FileName)); err == nil {
		t.Error("registry written despite failure")
	}
}

func TestReadImage(t *testing.T) {
	root := setup(t)
	data := jpegBytes(t, 4, 4)
	r := mustAdd(t, root, AddInput{ID: "fig-1", Data: data})
	if got, ok := ReadImage(root, rootDir, r.Item); !ok || !bytes.Equal(got, data) {
		t.Error("ReadImage failed")
	}
	os.WriteFile(filepath.Join(root, rootDir, "illustrations", "fig-1.jpg"), []byte("x"), 0o644)
	if _, ok := ReadImage(root, rootDir, r.Item); ok {
		t.Error("ReadImage accepted modified image")
	}
	if MIME(FormatPNG) != "image/png" || MIME(FormatJPEG) != "image/jpeg" {
		t.Error("MIME")
	}
}

func TestMarkdownEscapesAlt(t *testing.T) {
	it := Item{Role: RoleBody, Alt: `a]b[c\`, File: "illustrations/x.png"}
	md := Markdown(it)
	refs := Refs([]byte(md))
	if len(refs) != 1 || refs[0].Src != "illustrations/x.png" {
		t.Fatalf("Markdown %q not recognized: %+v", md, refs)
	}
	if !Referenced([]byte(md), it) {
		t.Error("Referenced")
	}
}
