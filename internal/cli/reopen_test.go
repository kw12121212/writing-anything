package cli

import (
	"bytes"
	"os"
	"path/filepath"
	"strings"
	"testing"

	"github.com/kw12121212/writing-anything/internal/export"
	"github.com/kw12121212/writing-anything/internal/review"
	"github.com/kw12121212/writing-anything/internal/workspace"
)

// doneThenEdited 返回已验收并推进到 done、之后用 draft write 修改过正文的作品 my-post（真实接入验证 cc-tb-3）。
func doneThenEdited(t *testing.T) string {
	t.Helper()
	dir := acceptedWork(t, "第一版正文。\n")
	mustRunWS(t, dir, "advance", "my-post")
	if code, res := runWSIn(t, dir, "按作者反馈改过的正文。\n", "draft", "write", "my-post"); code != 0 {
		t.Fatalf("draft write: %+v", res)
	}
	return dir
}

// TestReopenStaleDone 复现 cc-tb-3：验收失效的 done 单元原本无路可走；reopen 退回 review 后按原有流程重新审稿、
// 验收、推进并导出（reopen-stage、next-steps、export-reopen-next）。
func TestReopenStaleDone(t *testing.T) {
	dir := doneThenEdited(t)
	unitDir := filepath.Join(dir, ".writing-anything", "works", "my-post")

	// 原有命令仍各自拒绝（不变项）。
	code, res := runWS(t, dir, "export", "my-post", "--format", "wechat-html")
	d := diag(t, res, "export.unaccepted")
	if code != 1 || len(d.Next) != 2 || d.Next[0] != "wa-cli reopen my-post --dir "+dir+" --json" || d.Next[1] != "wa-cli review show my-post --dir "+dir+" --json" {
		t.Errorf("export: code %d, next %v", code, d.Next)
	}
	diag(t, res, "export.nothing-accepted")
	if code, res := runWS(t, dir, "accept", "my-post"); code != 1 {
		t.Errorf("accept: code %d", code)
	} else {
		diag(t, res, "accept.wrong-stage")
	}
	if code, res := runWS(t, dir, "advance", "my-post"); code != 1 {
		t.Errorf("advance: code %d", code)
	} else {
		diag(t, res, "lifecycle.already-done")
	}

	// next 列出该单元，下一步只有 reopen。
	code, out := runUnits(t, dir, "next")
	if code != 0 || len(out.Steps) != 1 {
		t.Fatalf("next: code %d, steps %v", code, out.Steps)
	}
	s := out.Steps[0]
	blockers, _ := s["blockers"].([]any)
	commands, _ := s["commands"].([]any)
	// 审计回捞：本用例里 accept 与 export 失败后未重试，next 在 reopen 命令后追加两条提示（非阻塞）。
	if s["unit"] != "my-post" || s["stage"] != "done" || len(blockers) != 1 || blockers[0].(map[string]any)["code"] != "acceptance.stale" ||
		blockers[0].(map[string]any)["artifact"] != "acceptance" || len(commands) != 3 || commands[0] != "wa-cli reopen my-post --dir "+dir+" --json" ||
		commands[1] != "wa-cli help accept --json" || commands[2] != "wa-cli help export --json" ||
		!strings.Contains(s["action"].(string), "wa-cli reopen") ||
		!strings.Contains(s["action"].(string), "accept 在本单元失败后尚未成功重试") {
		t.Errorf("next step = %v", s)
	}

	// reopen 只改 stage，不动审稿与验收记录。
	reviewBefore, _ := os.ReadFile(filepath.Join(unitDir, "review.yaml"))
	acceptBefore, _ := os.ReadFile(filepath.Join(unitDir, "acceptance.yaml"))
	code, out = runUnits(t, dir, "reopen", "my-post")
	if code != 0 || out.Unit["unit"] != "my-post" || out.Unit["previousStage"] != "done" || out.Unit["stage"] != "review" ||
		len(out.ChangedPaths) != 1 || out.ChangedPaths[0] != ".writing-anything/works/my-post/work.yaml" {
		t.Fatalf("reopen: code %d, %+v", code, out)
	}
	reviewAfter, _ := os.ReadFile(filepath.Join(unitDir, "review.yaml"))
	acceptAfter, _ := os.ReadFile(filepath.Join(unitDir, "acceptance.yaml"))
	if !bytes.Equal(reviewBefore, reviewAfter) || !bytes.Equal(acceptBefore, acceptAfter) {
		t.Error("reopen changed review.yaml or acceptance.yaml")
	}

	// 之后与第一次验收相同。
	if code, res := runWSIn(t, dir, "rubric:\n  - {item: clarity, verdict: pass, evidence: 清楚, paragraphs: [1]}\n", "review", "record", "my-post"); code != 0 {
		t.Fatalf("review record: %+v", res)
	}
	mustRunWS(t, dir, "accept", "my-post", "--confirmed-by-author", "--confirmation", "同意")
	mustRunWS(t, dir, "advance", "my-post")
	if code, res := runExportJSON(t, dir, "my-post"); code != 0 {
		t.Fatalf("export after reopen: %+v", res)
	}
	got, _ := os.ReadFile(filepath.Join(dir, ".writing-anything", "exports", "my-post.md"))
	if !strings.Contains(string(got), "按作者反馈改过的正文。") {
		t.Errorf("export = %q", got)
	}
	// 重新验收后 next 不再列出（next-skips-done）。
	if code, out := runUnits(t, dir, "next"); code != 0 || len(out.Steps) != 0 {
		t.Errorf("next after reaccept: %v", out.Steps)
	}
}

// reopen 拒绝时的诊断与下一步，被拒绝时 work.yaml 逐字节不变（reopen-stage、diagnostic-next-concrete）。
func TestReopenRejected(t *testing.T) {
	dir := acceptedWork(t, "正文。\n")
	work := filepath.Join(dir, ".writing-anything", "works", "my-post", "work.yaml")
	unchanged := func(name string, f func()) {
		t.Helper()
		before, _ := os.ReadFile(work)
		f()
		if after, _ := os.ReadFile(work); !bytes.Equal(before, after) {
			t.Errorf("%s: work.yaml changed", name)
		}
	}
	unchanged("not done", func() {
		code, res := runWS(t, dir, "reopen", "my-post")
		if d := diag(t, res, "lifecycle.not-done"); code != 1 || len(d.Next) != 1 || d.Next[0] != "wa-cli next my-post --dir "+dir+" --json" {
			t.Errorf("not done: code %d, %+v", code, d)
		}
	})
	mustRunWS(t, dir, "advance", "my-post")
	unchanged("acceptance current", func() {
		code, res := runWS(t, dir, "reopen", "my-post")
		if d := diag(t, res, "lifecycle.acceptance-current"); code != 1 || d.Path != ".writing-anything/works/my-post/acceptance.yaml" ||
			len(d.Next) != 1 || d.Next[0] != "wa-cli review show my-post --dir "+dir+" --json" {
			t.Errorf("acceptance current: code %d, %+v", code, d)
		}
	})
	if code, res := runWS(t, dir, "reopen", "my-post/nope"); code != 1 {
		t.Errorf("unknown unit: code %d", code)
	} else {
		diag(t, res, "unit.not-found")
	}

	// stage 经由锚点得到时不修改（structured-edits）。
	writeFile(t, work, "schema_version: 1\nid: my-post\ngenre: minimal\ntype: article\nstage: &s done\nunits:\n  - {id: a, type: section, stage: *s}\n")
	unchanged("anchored", func() {
		code, res := runWS(t, dir, "reopen", "my-post/a")
		if d := diag(t, res, "work.anchored-value"); code != 1 || len(d.Next) != 2 || d.Next[0] != "wa-cli next my-post/a --dir "+dir+" --json" || d.Next[1] != "wa-cli help reopen --json" {
			t.Errorf("anchored: code %d, %+v", code, d)
		}
	})

	// 汇总单元的阶段由子单元汇总，不能退回。
	novel := newNovel(t)
	code, res := runWS(t, novel, "reopen", "my-novel/chapter-one")
	if d := diag(t, res, "lifecycle.derived-stage"); code != 1 || len(d.Next) != 1 || d.Next[0] != "wa-cli status --dir "+novel+" --json" {
		t.Errorf("derived stage: code %d, %+v", code, d)
	}
	// 内置文体都有验收阶段；lifecycle.no-acceptance-stage 的下一步按错误码给出 status。
	if got := next(workspace.Problem{Code: "lifecycle.no-acceptance-stage", Path: ".writing-anything/works/x/work.yaml"}, dir); len(got) != 1 || got[0] != "wa-cli status --dir "+dir+" --json" {
		t.Errorf("no acceptance stage next = %v", got)
	}
}

// reopen-missing-acceptance：手写为 done、没有验收记录的单元可以退回，但不出现在 next 中（next-skips-done）。
func TestReopenMissingAcceptance(t *testing.T) {
	dir := newWork(t)
	work := filepath.Join(dir, ".writing-anything", "works", "my-post", "work.yaml")
	writeFile(t, work, "schema_version: 1\nid: my-post\ngenre: minimal\ntype: article\nstage: done\n")
	if code, out := runUnits(t, dir, "next"); code != 0 || len(out.Steps) != 0 {
		t.Errorf("next: %v", out.Steps)
	}
	code, out := runUnits(t, dir, "reopen", "my-post")
	if code != 0 || out.Unit["stage"] != "review" || len(out.ChangedPaths) != 1 || out.ChangedPaths[0] != ".writing-anything/works/my-post/work.yaml" {
		t.Errorf("reopen: code %d, %+v", code, out)
	}
}

// 验收仍有效、只因正文过大被跳过的 done 单元不给出 reopen（export-reopen-next 的 export-done-oversized-next）。
func TestExportOversizedDoneNoReopen(t *testing.T) {
	dir := newWork(t)
	unit := filepath.Join(dir, ".writing-anything", "works", "my-post")
	writeFile(t, filepath.Join(unit, "work.yaml"), "schema_version: 1\nid: my-post\ngenre: minimal\ntype: article\nstage: done\n")
	big := strings.Repeat("正文。", export.MaxBytes/9+1) + "\n"
	writeFile(t, filepath.Join(unit, "draft.md"), big)
	writeFile(t, filepath.Join(unit, "acceptance.yaml"), "schema_version: 1\ngate_level: standard\ndraft_sha256: "+review.SHA256([]byte(big))+
		"\nconfirmed_by_author: false\naccepted_at: \"2026-10-01T00:00:00Z\"\n")
	code, res := runWS(t, dir, "export", "my-post")
	if d := diag(t, res, "export.unaccepted"); code != 1 || len(d.Next) != 1 || d.Next[0] != "wa-cli review show my-post --dir "+dir+" --json" || !strings.Contains(d.Message, "超过导出的上限") {
		t.Errorf("oversized: code %d, %+v", code, d)
	}
}

// 三种导出格式对 done 单元都先给出 reopen；指定单元的 next 与 status 同样给出（export-reopen-next、next-steps）。
func TestReopenNextEverywhere(t *testing.T) {
	dir := doneThenEdited(t)
	for _, format := range []string{"markdown", "text"} {
		code, res := runWS(t, dir, "export", "my-post", "--format", format)
		if d := diag(t, res, "export.unaccepted"); code != 1 || len(d.Next) != 2 || d.Next[0] != "wa-cli reopen my-post --dir "+dir+" --json" {
			t.Errorf("%s: code %d, next %v", format, code, d.Next)
		}
	}
	reopen := "wa-cli reopen my-post --dir " + dir + " --json"
	code, out := runUnits(t, dir, "next", "my-post")
	if commands, _ := out.Steps[0]["commands"].([]any); code != 0 || len(out.Steps) != 1 || len(commands) != 2 ||
		commands[0] != reopen || commands[1] != "wa-cli help export --json" {
		t.Errorf("next my-post: %v", out.Steps)
	}
	code, out = runUnits(t, dir, "status")
	if code != 0 || len(out.Units) != 1 {
		t.Fatalf("status: %v", out.Units)
	}
	if commands, _ := out.Units[0]["commands"].([]any); out.Units[0]["stage"] != "done" || len(commands) != 1 || commands[0] != reopen {
		t.Errorf("status unit = %v", out.Units[0])
	}
	// 不加 --json 时的文本输出。
	if code, stdout, _ := run(t, "reopen", "my-post", "--dir", dir); code != 0 || !strings.Contains(stdout, "已从 done 退回到 review") {
		t.Errorf("text: %d %q", code, stdout)
	}
}
