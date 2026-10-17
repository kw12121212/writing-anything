package cli

import (
	"path/filepath"
	"regexp"
	"strings"
	"testing"
)

// handoff 命令组：write 两入口、show 倒序、next 呈现最近一条。
func TestHandoffCommands(t *testing.T) {
	dir := newWorkspace(t)
	writeFile(t, filepath.Join(dir, "h1.yaml"), "done: [完成场景一]\ntodo: [场景二待收口]\n")
	if code, res := runWS(t, dir, "handoff", "write", "--file", "h1.yaml"); code != 0 {
		t.Fatalf("write: exit code = %d, result = %+v", code, res)
	}
	// stdin 入口。
	if code, _ := runWSIn(t, dir, "done: [场景二收口]\ntodo: [场景三草拟]\nrisks: [伏笔接近回收]\n", "handoff", "write"); code != 0 {
		t.Fatalf("write stdin: exit code = %d", code)
	}
	// show 默认最近一条。
	code, res := runWS(t, dir, "handoff", "show")
	if code != 0 || !strings.Contains(res.stdout, "handoff-2") || strings.Contains(res.stdout, "handoff-1") {
		t.Fatalf("show latest: exit code = %d, stdout = %s", code, res.stdout)
	}
	// --last 2 倒序。
	if code, res := runWS(t, dir, "handoff", "show", "--last", "2"); code != 0 || !strings.Contains(res.stdout, "handoff-1") {
		t.Fatalf("show last 2: exit code = %d", code)
	}
	// --last 越界。
	if code, _ := runWS(t, dir, "handoff", "show", "--last", "51"); code != 1 {
		t.Fatalf("last 51: exit code = %d", code)
	}
	// next 的 handoff 字段。
	dir2 := newWork(t)
	writeFile(t, filepath.Join(dir2, "h1.yaml"), "done: [完成场景一]\ntodo: [场景二待收口]\n")
	run(t, "handoff", "write", "--file", "h1.yaml", "--dir", dir2)
	nCode, nRes := runWS(t, dir2, "next", "my-post")
	if nCode != 0 || !strings.Contains(nRes.stdout, `"handoff":{"id":"handoff-1"`) || !strings.Contains(nRes.stdout, "场景二待收口") {
		t.Fatalf("next handoff: exit code = %d, stdout = %s", nCode, nRes.stdout)
	}
	if !strings.Contains(nRes.stdout, `"instructions":[]`) {
		t.Fatalf("next handoff instructions 应为空数组: %s", nRes.stdout)
	}
	// 无记录：handoff 为 null 且其余不变；文本输出不含交接段。
	dir3 := newWork(t)
	if code, res2 := runWS(t, dir3, "next", "my-post"); code != 0 || !strings.Contains(res2.stdout, `"handoff":null`) {
		t.Fatalf("next no handoff: exit code = %d, stdout = %s", code, res.stdout)
	}
	if code, out, _ := run(t, "next", "--dir", dir3); code != 0 || strings.Contains(out, "交接记录") || !strings.HasPrefix(out, "my-post") {
		t.Fatalf("next text no handoff: exit code = %d, stdout = %s", code, out)
	}
	// validate <path>。
	if code, res := runWS(t, dir2, "validate", ".writing-anything/handoffs.yaml"); code != 0 {
		t.Fatalf("validate path: exit code = %d, result = %+v", code, res)
	}
	// 上限拒绝的 CLI 面（手写 200 条）由包测试覆盖；此处验证诊断 next。
	writeFile(t, filepath.Join(dir2, "h2.yaml"), "done: [x]\ntodo: [y]\n")
	if code, _ := runWS(t, dir2, "handoff", "write", "--file", "h2.yaml"); code != 0 {
		t.Fatalf("second write: %d", code)
	}
	// 干净收尾：省略 todo（或给空数组）合法，写入该条 todo 为空数组。
	writeFile(t, filepath.Join(dir2, "h3.yaml"), "done: [全部场景已收口]\n")
	if code, res := runWS(t, dir2, "handoff", "write", "--file", "h3.yaml"); code != 0 {
		t.Fatalf("write without todo: exit code = %d, result = %+v", code, res)
	} else if !strings.Contains(res.stdout, `"todo":[]`) {
		t.Fatalf("note todo should be empty array: %s", res.stdout)
	}
	if code, res := runWS(t, dir2, "handoff", "show"); code != 0 || !strings.Contains(res.stdout, "handoff-3") || strings.Contains(res.stdout, "未完成") {
		t.Fatalf("show no-todo note: exit code = %d, stdout = %s", code, res.stdout)
	}
	if code, res := runWS(t, dir2, "validate", ".writing-anything/handoffs.yaml"); code != 0 {
		t.Fatalf("validate with empty todo: exit code = %d, result = %+v", code, res)
	}
	// 空数组形态同样合法。
	writeFile(t, filepath.Join(dir2, "h4.yaml"), "done: [再次收口]\ntodo: []\n")
	if code, _ := runWS(t, dir2, "handoff", "write", "--file", "h4.yaml"); code != 0 {
		t.Fatalf("write empty todo: exit code = %d", code)
	}
	// 最近一条（空 todo）在人读文本里不列未完成事项：show 文本无「待办：」，next 文本有交接段但无「未完成：」。
	if code, out, _ := run(t, "handoff", "show", "--dir", dir2); code != 0 || strings.Contains(out, "待办：") {
		t.Fatalf("show text with empty todo: exit code = %d, stdout = %s", code, out)
	}
	if code, out, _ := run(t, "next", "--dir", dir2); code != 0 || !strings.Contains(out, "最近交接记录") || strings.Contains(out, "未完成：") {
		t.Fatalf("next text with empty todo: exit code = %d, stdout = %s", code, out)
	}
}

// next 文本模式在顶部显示一次最近交接记录（id、写入时间、未完成事项与作者新指示），与 JSON 的 handoff 字段一致。
func TestNextTextShowsLatestHandoff(t *testing.T) {
	dir := newWork(t)
	if code, _, stderr := run(t, "unit", "add", "my-post", "section", "intro", "--dir", dir); code != 0 {
		t.Fatalf("unit add: exit code = %d, stderr = %s", code, stderr)
	}
	writeFile(t, filepath.Join(dir, "h1.yaml"), "done: [完成场景一]\ntodo: [旧待办]\n")
	writeFile(t, filepath.Join(dir, "h2.yaml"), "done: [完成场景二]\ntodo: [场景三待收口, 补素材]\ninstructions: [保持第一人称]\n")
	for _, f := range []string{"h1.yaml", "h2.yaml"} {
		if code, _, stderr := run(t, "handoff", "write", "--file", f, "--dir", dir); code != 0 {
			t.Fatalf("write %s: exit code = %d, stderr = %s", f, code, stderr)
		}
	}
	code, out, _ := run(t, "next", "--dir", dir)
	if code != 0 {
		t.Fatalf("next text: exit code = %d, stdout = %s", code, out)
	}
	lines := strings.Split(out, "\n")
	if !regexp.MustCompile(`^最近交接记录（Handoff Note）handoff-2（\d{4}-\d\d-\d\dT\d\d:\d\d:\d\dZ）$`).MatchString(lines[0]) {
		t.Fatalf("交接记录（含写入时间）应在顶部: %s", out)
	}
	for _, want := range []string{"  未完成：场景三待收口", "  未完成：补素材", "  作者新指示：保持第一人称"} {
		if !strings.Contains(out, want+"\n") {
			t.Errorf("stdout 缺少 %q: %s", want, out)
		}
	}
	if strings.Contains(out, "旧待办") || strings.Count(out, "handoff-2") != 1 {
		t.Errorf("只应显示一次最近一条交接记录: %s", out)
	}
	if !strings.Contains(out, "my-post（阶段") || !strings.Contains(out, "my-post/intro（阶段") {
		t.Errorf("交接段之后仍应列出单元步骤: %s", out)
	}
	// JSON 的 handoff 字段承载同样的 id、todo 与 instructions。
	_, res := runWS(t, dir, "next")
	if !strings.Contains(res.stdout, `"handoff":{"id":"handoff-2","todo":["场景三待收口","补素材"],"instructions":["保持第一人称"]}`) {
		t.Fatalf("next json handoff: %s", res.stdout)
	}
}

// handoffs.yaml 不合法时 next 的 handoff 为 null、文本不含交接段；没有可推进单元时文本也不含交接段。
func TestNextHandoffInvalidAndNoSteps(t *testing.T) {
	dir := newWork(t)
	_, textBefore, _ := run(t, "next", "--dir", dir)
	writeFile(t, filepath.Join(dir, ".writing-anything", "handoffs.yaml"),
		"schema_version: 1\nnotes:\n  - {id: handoff-2, written_at: \"2026-10-04T08:00:00Z\", done: [x], todo: [y]}\n")
	if _, res := runWS(t, dir, "next", "my-post"); !strings.Contains(res.stdout, `"handoff":null`) {
		t.Fatalf("invalid handoffs.yaml: handoff 应为 null: %s", res.stdout)
	}
	if code, out, _ := run(t, "next", "--dir", dir); code != 0 || out != textBefore {
		t.Fatalf("invalid handoffs.yaml: 文本输出应与之前一致: exit code = %d, stdout = %s", code, out)
	}
	// 有交接记录但没有需要推进的单元：只输出「没有需要推进的单元」。
	empty := newWorkspace(t)
	writeFile(t, filepath.Join(empty, "h1.yaml"), "done: [x]\ntodo: [y]\ninstructions: [z]\n")
	if code, _, stderr := run(t, "handoff", "write", "--file", "h1.yaml", "--dir", empty); code != 0 {
		t.Fatalf("write: exit code = %d, stderr = %s", code, stderr)
	}
	if code, out, _ := run(t, "next", "--dir", empty); code != 0 || out != "没有需要推进的单元（Unit）\n" {
		t.Fatalf("no steps: exit code = %d, stdout = %q", code, out)
	}
}
