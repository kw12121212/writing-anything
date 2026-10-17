package cli

import (
	"os"
	"path/filepath"
	"strings"
	"testing"
)

// review packet 命令组：plan 生成、submit 校验与写回、report 汇总。
func TestReviewPacketCommands(t *testing.T) {
	dir := packetWork(t)
	code, res := runWS(t, dir, "review", "plan", "my-story/s1", "--dimension", "causality")
	if code != 0 {
		t.Fatalf("plan: exit code = %d, result = %+v", code, res)
	}
	if !strings.Contains(res.stdout, `"id":"causality-1"`) {
		t.Fatalf("plan output = %s", res.stdout)
	}
	// submit 缺结论被拒且不写文件。
	writeFile(t, filepath.Join(dir, "v-in.yaml"), "verdicts:\n  - {item: cause-holds, verdict: pass}\n")
	if code, res := runWS(t, dir, "review", "submit", "my-story/causality-1", "--file", "v-in.yaml"); code != 1 {
		t.Fatalf("submit missing: exit code = %d, result = %+v", code, res)
	} else if !strings.Contains(res.stdout, "packet.missing-verdict") {
		t.Fatalf("diagnostics = %s", res.stdout)
	}
	// 齐全后成功。
	writeFile(t, filepath.Join(dir, "v-in.yaml"),
		"verdicts:\n  - {item: cause-holds, verdict: pass}\n  - {item: event-produced, verdict: na}\n  - {item: motivation, verdict: fail, note: 动机不足}\n")
	code2, res2 := runWS(t, dir, "review", "submit", "my-story/causality-1", "--file", "v-in.yaml")
	if code2 != 0 {
		t.Fatalf("submit ok: exit code = %d, result = %+v", code2, res2)
	}
	if !strings.Contains(res2.stdout, `"fails":1`) {
		t.Fatalf("submit output = %s", res2.stdout)
	}
	// 重复提交被拒。
	if code, _ := runWS(t, dir, "review", "submit", "my-story/causality-1", "--file", "v-in.yaml"); code != 1 {
		t.Fatalf("resubmit: exit code = %d", code)
	}
	// report 汇总。
	code3, res3 := runWS(t, dir, "review", "report", "my-story")
	if code3 != 0 {
		t.Fatalf("report: exit code = %d, result = %+v", code3, res3)
	}
	if !strings.Contains(res3.stdout, `"ready":false`) || !strings.Contains(res3.stdout, "motivation") {
		t.Fatalf("report output = %s", res3.stdout)
	}
	// validate 计入包。
	code4, res4 := runWS(t, dir, "validate")
	if code4 != 0 {
		t.Fatalf("validate: exit code = %d, result = %+v", code4, res4)
	}
	if !strings.Contains(strings.Join(res4.Checked, "\n"), "packets/my-story/causality-1.yaml") {
		t.Fatalf("checked = %v", res4.Checked)
	}
}

// 上下文预算放不下单个清单项与最小正文片段：review plan 报告 packet.over-budget（error），不写任何包文件，
// next 给出查看与调整预算的命令；预算足够时人读输出标注省略的切片条数。
func TestReviewPlanOverBudget(t *testing.T) {
	dir := packetWork(t)
	mustRunWS(t, dir, "config", "set", "context_budget", "20")
	code, res := runWS(t, dir, "review", "plan", "my-story/s1", "--dimension", "causality")
	if code != 1 || res.Valid || !strings.Contains(res.stdout, `"packets":[]`) {
		t.Fatalf("plan over budget: exit code = %d, stdout = %s", code, res.stdout)
	}
	d := diag(t, res, "packet.over-budget")
	if d.Severity != "error" || d.Field != "context_budget" || !hasNext(d, "wa-cli config resolved --dir ") || !hasNext(d, "wa-cli help config set --json") {
		t.Fatalf("diagnostic = %+v", d)
	}
	if _, err := os.Stat(filepath.Join(dir, ".writing-anything", "packets")); err == nil {
		t.Fatal("packets written on over-budget")
	}
	if code, stdout, _ := run(t, "review", "plan", "my-story/s1", "--dir", dir); code != 1 || strings.Contains(stdout, "没有可审查的内容") {
		t.Fatalf("plan text over budget: exit code = %d, stdout = %s", code, stdout)
	}
	// 切片放不下时省略并在人读输出中标注。
	mustRunWS(t, dir, "config", "set", "context_budget", "260")
	writeFile(t, filepath.Join(dir, ".writing-anything", "works", "my-story", "units", "s1", "outline.yaml"),
		"schema_version: 1\nsections:\n  - {id: sc1, title: 场景, reveal: "+strings.Repeat("揭示", 200)+"}\n")
	if code, stdout, stderr := run(t, "review", "plan", "my-story/s1", "--dimension", "pov-reveal", "--dir", dir); code != 0 || !strings.Contains(stdout, "按预算省略 1 条切片") {
		t.Fatalf("plan text: exit code = %d, stdout = %s, stderr = %s", code, stdout, stderr)
	}
}
