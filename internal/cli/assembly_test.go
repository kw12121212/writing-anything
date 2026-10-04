package cli

import (
	"os"
	"path/filepath"
	"strings"
	"testing"
)

// 组稿命令：成功导出（Markdown 与 text）、三类阻塞的拒绝与解除后通过。
func TestAssembleSuccess(t *testing.T) {
	dir := assemblyWork(t)
	code, res := runWS(t, dir, "assemble", "my-novel")
	if code != 0 {
		t.Fatalf("assemble: exit code = %d, result = %+v", code, res)
	}
	out := filepath.Join(dir, ".writing-anything", "works", "my-novel", "exports")
	_ = out
	// 读交付文件验证内容。
	data, err := os.ReadFile(filepath.Join(dir, ".writing-anything", "exports", "my-novel-manuscript.md"))
	if err != nil {
		// 路径可能带 exports 相对前缀；从结果取。
		t.Fatalf("read manuscript: %v", err)
	}
	text := string(data)
	for _, want := range []string{"# my-novel", "## 目录", "chapter-one", "正文，没有批注。"} {
		if !strings.Contains(text, want) {
			t.Fatalf("manuscript missing %q: %s...", want, text[:200])
		}
	}
	// text 格式。
	if code, res := runWS(t, dir, "assemble", "my-novel", "--format", "text"); code != 0 {
		t.Fatalf("assemble text: exit code = %d, result = %+v", code, res)
	}
	if data2, err := os.ReadFile(filepath.Join(dir, ".writing-anything", "exports", "my-novel-manuscript.txt")); err != nil || !strings.Contains(string(data2), "《my-novel》") {
		t.Fatalf("text manuscript: %v", err)
	}
}

func TestAssembleGates(t *testing.T) {
	// 场景未完成。
	dir := newWorkspace(t, "--genre", "novel", "--gate-level", "fast")
	run(t, "new", "novel", "my-novel", "--dir", dir)
	run(t, "unit", "add", "my-novel", "part", "part-one", "--dir", dir)
	run(t, "unit", "add", "my-novel/part-one", "chapter", "chapter-one", "--dir", dir)
	run(t, "unit", "add", "my-novel/chapter-one", "scene", "scene-1", "--dir", dir)
	code, res := runWS(t, dir, "assemble", "my-novel")
	if code != 1 || diag(t, res, "assemble.scenes-open").Code == "" {
		t.Fatalf("scenes-open: exit code = %d, diagnostics = %+v", code, res.Diagnostics)
	}

	// 连续性阻塞：出场人物未建卡（扩展契约触发）。
	dir2 := assemblyWork(t)
	sceneDir := filepath.Join(dir2, ".writing-anything", "works", "my-novel", "units", "scene-1")
	writeFile(t, filepath.Join(sceneDir, "outline.yaml"),
		"schema_version: 1\nsections:\n  - {id: sc1, title: 场景, reason: 推进情节, pov: 高人, characters: [高人]}\n")
	code, res = runWS(t, dir2, "assemble", "my-novel")
	if code != 1 || diag(t, res, "assemble.continuity").Code == "" {
		t.Fatalf("continuity: exit code = %d, diagnostics = %+v", code, res.Diagnostics)
	}
	// 补卡后通过。
	run(t, "canon", "add", "my-novel", "--type", "character", "--name", "高人", "--text", "过客", "--dir", dir2)
	if code, res = runWS(t, dir2, "assemble", "my-novel"); code != 0 {
		t.Fatalf("after canon add: exit code = %d, result = %+v", code, res)
	}

	// 复查标记阻塞。
	dir3 := assemblyWork(t)
	// 直接手写一条 open 复查标记（impact mark 需要引用边，这里聚焦关卡本身）。
	os.MkdirAll(filepath.Join(dir3, ".writing-anything", "rechecks"), 0o755)
	writeFile(t, filepath.Join(dir3, ".writing-anything", "rechecks", "my-novel.yaml"),
		"schema_version: 1\nscenes:\n  - {scene: my-novel/scene-1, target: my-novel/scene-0, via: cause-event, reason: 测试, marked_at: \"2026-10-04T08:00:00Z\", status: open}\n")
	code, res = runWS(t, dir3, "assemble", "my-novel")
	if code != 1 || diag(t, res, "assemble.rechecks").Code == "" {
		t.Fatalf("rechecks: exit code = %d, diagnostics = %+v", code, res.Diagnostics)
	}
	d := diag(t, res, "assemble.rechecks")
	if !strings.Contains(strings.Join(d.Next, "\n"), "wa-cli impact list my-novel") {
		t.Fatalf("next = %v", d.Next)
	}
}
