package cli

import (
	"path/filepath"
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
	if code, _ := runWSIn(t, dir, "done: [场景二收口]\ntodo: [场景三草拟]\nrisks: [伏笔接近回收]\n", "handoff", "write", "--dir", dir); code != 0 {
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
	// 无记录：handoff 为 null 且其余不变。
	dir3 := newWork(t)
	if code, res2 := runWS(t, dir3, "next", "my-post"); code != 0 || !strings.Contains(res2.stdout, `"handoff":null`) {
		t.Fatalf("next no handoff: exit code = %d, stdout = %s", code, res.stdout)
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
}
