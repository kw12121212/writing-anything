package cli

import (
	"encoding/json"
	"os"
	"path/filepath"
	"strings"
	"testing"
)

// 审计日志（Audit Log）：每次调用（成功、失败与用法错误）追加一行 JSONL 运维痕迹；不进 changedPaths；
// 无工作区不写；回捞提示在同命令×单元成功重试后消失。
func TestAuditLog(t *testing.T) {
	dir := newWork(t)
	rel := ".writing-anything/audit.log"
	read := func() []map[string]any {
		t.Helper()
		data, err := os.ReadFile(filepath.Join(dir, filepath.FromSlash(rel)))
		if err != nil {
			t.Fatal(err)
		}
		var out []map[string]any
		for _, line := range strings.Split(strings.TrimRight(string(data), "\n"), "\n") {
			var m map[string]any
			if json.Unmarshal([]byte(line), &m) != nil {
				t.Fatalf("audit line is not JSON: %q", line)
			}
			out = append(out, m)
		}
		return out
	}
	// 成功调用：codes 为空数组，changedPaths 不含 audit.log。
	if code, res := runWS(t, dir, "config", "resolved"); code != 0 {
		t.Fatalf("resolved: %d %+v", code, res.Diagnostics)
	} else if !res.Valid || len(res.ChangedPaths) != 0 {
		t.Fatalf("changedPaths = %v（audit.log 不是产物，不进 changedPaths）", res.ChangedPaths)
	}
	// 用法错误也记录（exit 2）。
	if code, _, _ := run(t, "version", "--json", "--json", "--dir", dir); code != 2 {
		t.Fatalf("duplicate flag: exit = %d", code)
	}
	// 失败调用：记 codes 与关键命令的 unit。
	if code, _ := runWS(t, dir, "review", "record", "my-post"); code != 1 {
		t.Fatalf("review record should fail: %d", code)
	}
	// 参数截断：给 --question 一个超长参数，审计里的该参数不超过 200 个字符。
	long := strings.Repeat("长", 300)
	if code, _ := runWS(t, dir, "decision", "add", "--question", long); code != 0 {
		t.Fatalf("decision add with long question: %d", code)
	}
	entries := read()
	// 夹具（init、new）的调用也在日志里：按命令名定位本用例的条目。
	var ok0, usage, failed, longEntry map[string]any
	for _, e := range entries {
		switch e["command"] {
		case "config resolved":
			ok0 = e
		case "version":
			usage = e
		case "review record":
			failed = e
		case "decision add":
			longEntry = e
		}
	}
	if ok0 == nil || usage == nil || failed == nil || longEntry == nil {
		t.Fatalf("audit entries = %v", entries)
	}
	if ok0["exit"] != float64(0) || len(ok0["codes"].([]any)) != 0 {
		t.Fatalf("success entry = %v", ok0)
	}
	for _, a := range longEntry["args"].([]any) {
		if len([]rune(a.(string))) > auditArgMax {
			t.Fatalf("arg not truncated: %d runes", len([]rune(a.(string))))
		}
	}
	if failed["exit"] != float64(1) || failed["unit"] != "my-post" {
		t.Fatalf("failed entry = %v", failed)
	}
	if codes, _ := failed["codes"].([]any); len(codes) == 0 {
		t.Fatalf("failed entry codes = %v", failed["codes"])
	}
	if usage["exit"] != float64(2) || !strings.Contains(fmtCodes(usage["codes"]), "usage.duplicate-flag") {
		t.Fatalf("usage entry = %v", usage)
	}
	// next 给出回捞提示；同命令×单元成功重试后消失。
	_, res := runWS(t, dir, "next", "my-post")
	if !strings.Contains(res.stdout, "review record 在本单元失败后尚未成功重试") || !strings.Contains(res.stdout, "wa-cli help review record --json") {
		t.Fatalf("next lacks recovery: %s", res.stdout)
	}
	// 让 review record 成功：写入正文与完整简报后推进到审稿阶段再整份录入。
	writeFile(t, wsPath(dir, ".writing-anything/works/my-post/brief.yaml"), completeBrief)
	runWS(t, dir, "advance", "my-post")
	if code, _, _ := runIn(t, "正文一段。", "draft", "write", "my-post", "--dir", dir); code != 0 {
		t.Fatal("draft write failed")
	}
	if code, _, _ := runIn(t, "rubric:\n  - {item: clarity, verdict: pass, evidence: 清楚, paragraphs: [1]}\n", "review", "record", "my-post", "--dir", dir); code != 0 {
		t.Fatal("review record retry failed")
	}
	_, res = runWS(t, dir, "next", "my-post")
	if strings.Contains(res.stdout, "尚未成功重试") {
		t.Fatalf("recovery should clear after successful retry: %s", res.stdout)
	}
}

// wa-cli --version 的审计记录：command 为 version，args 为原始参数（cli 主规格 version-command）。
func TestAuditRootVersionFlag(t *testing.T) {
	dir := newWork(t)
	t.Chdir(dir)
	if code, _, _ := run(t, "--version"); code != 0 {
		t.Fatalf("--version: exit = %d", code)
	}
	entries := readAudit(".")
	last := entries[len(entries)-1]
	if last.Command != "version" || strings.Join(last.Args, " ") != "--version" || last.Exit != 0 {
		t.Fatalf("audit entry = %+v, want command version, args [--version], exit 0", last.auditRecord)
	}
	// 用法错误同样记作 version。
	if code, _, _ := run(t, "--version", "extra"); code != 2 {
		t.Fatalf("--version extra: exit = %d", code)
	}
	entries = readAudit(".")
	last = entries[len(entries)-1]
	if last.Command != "version" || strings.Join(last.Args, " ") != "--version extra" || last.Exit != 2 || !strings.Contains(strings.Join(last.Codes, ","), "usage.unexpected-argument") {
		t.Fatalf("audit entry = %+v, want command version, args [--version extra], exit 2, usage.unexpected-argument", last.auditRecord)
	}
}

// 轮转：audit.log 超过 1 MiB 时轮转为 audit.log.1（替换旧备份），新行写进新文件。
func TestAuditRotation(t *testing.T) {
	dir := newWork(t)
	path := filepath.Join(dir, ".writing-anything", "audit.log")
	if err := os.WriteFile(path, make([]byte, auditMaxBytes), 0o644); err != nil {
		t.Fatal(err)
	}
	if code, _ := runWS(t, dir, "config", "resolved"); code != 0 {
		t.Fatalf("resolved: %d", code)
	}
	if _, err := os.Stat(path + ".1"); err != nil {
		t.Fatalf("rotated backup missing: %v", err)
	}
	data, err := os.ReadFile(path)
	if err != nil || len(data) == 0 || !strings.HasSuffix(string(data), "\n") {
		t.Fatalf("new audit.log = %q, err = %v", string(data), err)
	}
	if !strings.Contains(string(data), `"command":"config resolved"`) && !strings.Contains(string(data), `"command":"resolved"`) {
		t.Fatalf("new audit.log lacks the new entry: %q", string(data))
	}
}

// 无工作区不写审计。
func TestAuditNoWorkspace(t *testing.T) {
	dir := t.TempDir()
	if code, _, _ := run(t, "version"); code != 0 {
		t.Fatal("version failed")
	}
	if _, err := os.Stat(filepath.Join(dir, ".writing-anything", "audit.log")); !os.IsNotExist(err) {
		t.Fatalf("audit.log written without workspace: %v", err)
	}
}

func fmtCodes(v any) string {
	b, _ := json.Marshal(v)
	return string(b)
}

// 阅读页（静态与 --serve 页同构）展示最近失败的命令摘要（preview-audit-summary）：有失败时含区块，无失败时不含。
func TestPreviewAuditSummary(t *testing.T) {
	dir := newWork(t)
	if code, _ := runWS(t, dir, "review", "record", "my-post"); code != 1 {
		t.Fatal("review record should fail")
	}
	code, res := runWS(t, dir, "preview", "my-post")
	if code != 0 {
		t.Fatalf("preview: %d %v", code, res.Diagnostics)
	}
	page := readFile(t, wsPath(dir, ".writing-anything/exports/my-post.preview.html"))
	for _, want := range []string{"最近失败的命令", "review record（exit 1）"} {
		if !strings.Contains(page, want) {
			t.Errorf("page lacks %q", want)
		}
	}
	// 无失败的工作区不含该区块。
	dir2 := newWork(t)
	if code, _ := runWS(t, dir2, "preview", "my-post"); code != 0 {
		t.Fatal("preview clean failed")
	}
	page2 := readFile(t, wsPath(dir2, ".writing-anything/exports/my-post.preview.html"))
	if strings.Contains(page2, "最近失败的命令") {
		t.Error("clean workspace shows failure section")
	}
}
