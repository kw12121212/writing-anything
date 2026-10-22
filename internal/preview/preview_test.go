package preview_test

import (
	"bytes"
	"image"
	"image/png"
	"os"
	"path/filepath"
	"strings"
	"testing"

	"github.com/kw12121212/writing-anything/internal/cli"
	"github.com/kw12121212/writing-anything/internal/lint"
	"github.com/kw12121212/writing-anything/internal/preview"
	"github.com/kw12121212/writing-anything/internal/work"
	"github.com/kw12121212/writing-anything/internal/workspace"
)

func wa(t *testing.T, input string, args ...string) {
	t.Helper()
	var out, errOut bytes.Buffer
	if code := cli.Run(args, strings.NewReader(input), &out, &errOut); code != 0 {
		t.Fatalf("%v: exit code %d\n%s%s", args, code, out.String(), errOut.String())
	}
}

func write(t *testing.T, dir, rel, content string) {
	t.Helper()
	p := filepath.Join(dir, filepath.FromSlash(rel))
	if err := os.WriteFile(p, []byte(content), 0o644); err != nil {
		t.Fatal(err)
	}
}

// setup 返回作品 my-post：根单元正文两段由 Agent 写入，子单元 intro 没有正文。
func setup(t *testing.T) (string, *work.Work, *work.Unit) {
	t.Helper()
	dir := t.TempDir()
	wa(t, "", "init", "--dir", dir)
	wa(t, "", "new", "minimal", "my-post", "--dir", dir)
	wa(t, "第一段。\n第二行。\n\n第二段。\n", "draft", "write", "my-post", "--dir", dir)
	wa(t, "", "unit", "add", "my-post", "section", "intro", "--dir", dir)
	w, u, ps := work.Resolve(dir, "my-post")
	if u == nil {
		t.Fatal(ps)
	}
	return dir, w, u
}

func TestBuild(t *testing.T) {
	dir, w, u := setup(t)
	if refs := preview.Subtree(u); len(refs) != 2 || refs[1].Ref != "my-post/intro" {
		t.Fatalf("subtree: %v", refs)
	}
	run := func(*work.Unit) (*lint.Result, []workspace.Problem) {
		return &lint.Result{Findings: []lint.Finding{
			{Rule: "r", Severity: "error", Line: 2, Message: "第二行的问题", Suggestion: "改写"},
			{Rule: "g", Severity: "warning", Message: "全文级"},
			{Rule: "x", Severity: "warning", Line: 3, Message: "空行上的问题"},
		}}, []workspace.Problem{{Code: "lint.invalid-waiver", Message: "豁免格式不对", Origin: "内置规范包 default"}}
	}
	// 在三段的正文上审稿，再由作者删掉第三段：审稿过期，引用第 3 段的发现项无处可放。
	wa(t, "第一段。\n第二行。\n\n第二段。\n\n第三段。\n", "draft", "write", "my-post", "--dir", dir)
	wa(t, "rubric:\n  - {item: clarity, verdict: pass, evidence: 清楚, paragraphs: [1]}\nfindings:\n  - {id: r1, paragraph: 2, message: 第二段}\n  - {id: r9, paragraph: 3, message: 第三段}\n",
		"review", "record", "my-post", "--dir", dir)
	write(t, dir, ".writing-anything/works/my-post/draft.md", "第一段。\n第二行。\n\n第二段。\n")
	p := preview.Build(dir, w, preview.Subtree(u), run)
	root := p.Units[0]
	if !root.HasDraft || len(root.Paragraphs) != 2 || root.Paragraphs[0].Origin != "agent" || root.Paragraphs[0].Line != 1 || root.Paragraphs[1].Line != 4 {
		t.Fatalf("paragraphs: %+v", root.Paragraphs)
	}
	if fs := root.Paragraphs[0].Findings; len(fs) != 1 || fs[0].Label != "error r" || fs[0].Suggestion != "改写" {
		t.Fatalf("paragraph 1 findings: %+v", fs)
	}
	if fs := root.Paragraphs[1].Findings; len(fs) != 1 || fs[0].Kind != "review" || !strings.Contains(fs[0].Label, "r1") {
		t.Fatalf("paragraph 2 findings: %+v", fs)
	}
	if len(root.Global) != 3 || !strings.Contains(root.Global[2].Label, "第 3 段已不存在") {
		t.Fatalf("global: %+v", root.Global)
	}
	notes := strings.Join(root.Notes, "\n")
	if !strings.Contains(notes, "内置规范包 default：豁免格式不对") || !strings.Contains(notes, "审稿基于旧版正文") {
		t.Fatalf("notes: %s", notes)
	}
	if intro := p.Units[1]; intro.HasDraft || len(intro.Notes) != 0 {
		t.Fatalf("intro: %+v", intro)
	}
	html, err := preview.Render(p)
	if err != nil || !bytes.Contains(html, []byte("还没有正文")) || !bytes.Contains(html, []byte("· Agent</div>")) || bytes.Contains(html, []byte("<script")) {
		t.Fatalf("render: %v\n%s", err, html)
	}
	p.Serve = true
	if html, _ := preview.Render(p); !bytes.Contains(html, []byte("<script")) || !bytes.Contains(html, []byte("X-WA-Token")) {
		t.Fatal("serve page lacks the feedback script")
	}
}

// 写入记录损坏、审稿记录不通过校验、文本检查没有运行时，阅读页照常生成并在单元开头说明原因。
func TestBuildNotes(t *testing.T) {
	dir, w, u := setup(t)
	write(t, dir, ".writing-anything/works/my-post/provenance.yaml", "schema_version: 99\n")
	write(t, dir, ".writing-anything/works/my-post/review.yaml", "schema_version: 1\n")
	notRun := func(*work.Unit) (*lint.Result, []workspace.Problem) {
		return nil, []workspace.Problem{{Code: "config.unknown-pack", Message: "未知规范包"}}
	}
	root := preview.Build(dir, w, []*work.Unit{u}, notRun).Units[0]
	notes := strings.Join(root.Notes, "\n")
	for _, want := range []string{"无法推导段落出处", "文本检查没有运行：未知规范包（config.unknown-pack）", "review.yaml 不通过校验"} {
		if !strings.Contains(notes, want) {
			t.Errorf("notes lack %q: %s", want, notes)
		}
	}
	if len(root.Paragraphs) != 2 || root.Paragraphs[0].Origin != "" {
		t.Fatalf("paragraphs without provenance: %+v", root.Paragraphs)
	}
	if html, _ := preview.Render(&preview.Page{Title: "x", Units: []preview.Unit{root}}); !bytes.Contains(html, []byte("出处未知")) {
		t.Fatal("unknown origin not rendered")
	}
	// 正文无法读取（单元目录中的 draft.md 是目录）。
	os.Remove(filepath.Join(dir, ".writing-anything", "works", "my-post", "draft.md"))
	os.Mkdir(filepath.Join(dir, ".writing-anything", "works", "my-post", "draft.md"), 0o755)
	if root := preview.Build(dir, w, []*work.Unit{u}, notRun).Units[0]; root.HasDraft {
		t.Fatalf("draft directory treated as draft: %+v", root)
	}
}

func writePNG(t *testing.T, path string, pad int, shade byte) {
	t.Helper()
	img := image.NewRGBA(image.Rect(0, 0, 8, 4))
	var b bytes.Buffer
	if err := png.Encode(&b, img); err != nil {
		t.Fatal(err)
	}
	data := append(b.Bytes(), bytes.Repeat([]byte{shade}, pad)...)
	if err := os.WriteFile(path, data, 0o644); err != nil {
		t.Fatal(err)
	}
}

// illustrated 返回作品 my-post：根单元正文第 2 段引用 fig-1，第 3 段引用未登记的 ghost 与封面，另登记了封面 cover、
// 未被引用的信息图 chart 与图片文件缺失的 gone。
func illustrated(t *testing.T) (string, *work.Work, *work.Unit) {
	t.Helper()
	dir := t.TempDir()
	wa(t, "", "init", "--dir", dir)
	wa(t, "", "new", "minimal", "my-post", "--dir", dir)
	wa(t, "开头。\n\n![店](illustrations/fig-1.png) ![店](illustrations/fig-1.png)\n\n![鬼](illustrations/ghost.png) ![封](illustrations/cover.png) ![外](https://example.com/a.png)\n", "draft", "write", "my-post", "--dir", dir)
	wa(t, "", "claim", "add", "my-post", "--kind", "data", "--text", "数据", "--dir", dir)
	for i, id := range []string{"fig-1", "cover", "chart", "gone"} {
		writePNG(t, filepath.Join(dir, id+".png"), 0, byte(i))
		args := []string{"illustration", "add", "my-post", "--id", id, "--file", id + ".png", "--alt", "图 " + id, "--origin", "agent", "--dir", dir}
		switch id {
		case "cover":
			args = append(args, "--role", "cover")
		case "chart":
			args = append(args, "--content", "infographic", "--claims", "claim-1", "--title", "对比")
		}
		wa(t, "", args...)
	}
	os.Remove(filepath.Join(dir, ".writing-anything", "works", "my-post", "illustrations", "gone.png"))
	w, u, ps := work.Resolve(dir, "my-post")
	if u == nil {
		t.Fatal(ps)
	}
	return dir, w, u
}

func noLint(*work.Unit) (*lint.Result, []workspace.Problem) { return &lint.Result{}, nil }

func TestIllustrations(t *testing.T) {
	dir, w, u := illustrated(t)
	p := preview.Build(dir, w, []*work.Unit{u}, noLint)
	pu := p.Units[0]
	if len(pu.Illustrations) != 4 || len(pu.Covers()) != 1 || len(pu.Unreferenced()) != 2 {
		t.Fatalf("illustrations = %+v", pu.Illustrations)
	}
	if got := pu.Paragraphs[1].Illustrations; len(got) != 1 || got[0] != "fig-1" || len(pu.Images(pu.Paragraphs[1])) != 1 {
		t.Errorf("paragraph 2 = %+v", pu.Paragraphs[1])
	}
	if notes := strings.Join(pu.Paragraphs[2].ImageNotes, "；"); !strings.Contains(notes, "未登记") || !strings.Contains(notes, "封面图") || len(pu.ImageNotes) != 1 {
		t.Errorf("paragraph 3 notes = %q, unit notes = %v", notes, pu.ImageNotes)
	}
	gone := pu.Illustrations[3]
	if gone.Available || gone.Reason != "文件缺失" || gone.URL() != "" || pu.Illustrations[0].URL() != "illustration?unit=my-post&id=fig-1" {
		t.Errorf("gone = %+v", gone)
	}
	html := render(t, p)
	if strings.Count(html, `src="data:image/png;base64,`) != 3 || !strings.Contains(html, "配图不可用（文件缺失）") || !strings.Contains(html, "img-src data:") ||
		!strings.Contains(html, "信息图（Infographic） · 出处 agent · 对比 · 论断 claim-1") {
		t.Errorf("static html:\n%s", html)
	}
	s := preview.Build(dir, w, []*work.Unit{u}, noLint)
	s.Serve, s.Token = true, "tok"
	if html := render(t, s); !strings.Contains(html, `src="illustration?unit=my-post&amp;id=cover&amp;token=tok"`) || strings.Contains(html, "data:image/") {
		t.Errorf("serve html:\n%s", html)
	}
	data, err := preview.JSON(p)
	if err != nil {
		t.Fatal(err)
	}
	if err := preview.ValidateJSON(data); err != nil {
		t.Fatalf("schema: %v", err)
	}
	if !strings.Contains(string(data), `"url":"illustration?unit=my-post&id=fig-1"`) || !strings.Contains(string(data), `"illustrations":["fig-1"]`) {
		t.Errorf("json = %s", data)
	}
	// 哈希不符与格式不符：不显示图像并说明原因。
	writePNG(t, filepath.Join(dir, ".writing-anything", "works", "my-post", "illustrations", "fig-1.png"), 3, 9)
	reg := filepath.Join(dir, ".writing-anything", "works", "my-post", "illustrations.yaml")
	b, _ := os.ReadFile(reg)
	os.WriteFile(reg, bytes.Replace(b, []byte("file: illustrations/chart.png"), []byte("file: illustrations/chart.jpg"), 1), 0o644)
	p = preview.Build(dir, w, []*work.Unit{u}, noLint)
	if r := p.Units[0].Illustrations; r[0].Reason != "哈希不符" || r[2].Reason != "格式不符" {
		t.Errorf("reasons = %q %q", r[0].Reason, r[2].Reason)
	}
	if html := render(t, p); !strings.Contains(html, "配图 fig-1 不可用（哈希不符）") {
		t.Error("hash mismatch note missing")
	}
	os.WriteFile(reg, []byte("bogus: 1\n"), 0o644)
	if p := preview.Build(dir, w, []*work.Unit{u}, noLint); len(p.Units[0].Illustrations) != 0 || !strings.Contains(strings.Join(p.Units[0].Notes, ""), "不通过校验") {
		t.Errorf("invalid registry = %+v", p.Units[0])
	}
}

func TestIllustrationEmbedBudget(t *testing.T) {
	dir := t.TempDir()
	wa(t, "", "init", "--dir", dir)
	wa(t, "", "new", "minimal", "my-post", "--dir", dir)
	wa(t, "![一](illustrations/a.png)\n\n![二](illustrations/b.png)\n\n![三](illustrations/c.png)\n\n![四](illustrations/d.png)\n", "draft", "write", "my-post", "--dir", dir)
	for i, id := range []string{"a", "b", "c", "d"} {
		writePNG(t, filepath.Join(dir, id+".png"), 9<<20-100, byte(i))
		wa(t, "", "illustration", "add", "my-post", "--id", id, "--file", id+".png", "--alt", id, "--origin", "agent", "--dir", dir)
	}
	w, u, _ := work.Resolve(dir, "my-post")
	html := render(t, preview.Build(dir, w, []*work.Unit{u}, noLint))
	if strings.Count(html, `src="data:image/png;base64,`) != 3 || !strings.Contains(html, "配图过大，未内嵌：.writing-anything/works/my-post/illustrations/d.png") {
		t.Error("embed budget not applied")
	}
}

func render(t *testing.T, p *preview.Page) string {
	t.Helper()
	data, err := preview.Render(p)
	if err != nil {
		t.Fatal(err)
	}
	return string(data)
}
