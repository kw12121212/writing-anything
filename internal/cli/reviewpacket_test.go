package cli

import (
	"path/filepath"
	"strings"
	"testing"
)

// review packet 命令组：plan 生成、submit 校验与写回、report 汇总。
func TestReviewPacketCommands(t *testing.T) {
	dir := packetWork(t)
	code, res := runWS(t, dir, "review", "plan", "my-story/s1", "--dimension", "causality", "--dir", dir)
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
