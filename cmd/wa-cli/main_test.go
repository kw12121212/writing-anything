package main

import (
	"bytes"
	"encoding/json"
	"errors"
	"os"
	"os/exec"
	"path/filepath"
	"strings"
	"testing"
)

const module = "github.com/kw12121212/writing-anything"

// TestBinary 构建注入版本号的 wa-cli 二进制，验证其输出与退出码。
func TestBinary(t *testing.T) {
	if testing.Short() {
		t.Skip("构建二进制较慢，-short 时跳过")
	}
	bin := filepath.Join(t.TempDir(), "wa-cli")
	build := exec.Command("go", "build", "-ldflags", "-X "+module+"/internal/version.Version=v9.9.9-test", "-o", bin, ".")
	if out, err := build.CombinedOutput(); err != nil {
		t.Fatalf("go build: %v\n%s", err, out)
	}

	tests := []struct {
		args       []string
		wantCode   int
		wantStdout string
		wantStderr string
	}{
		{[]string{"version"}, 0, "wa-cli v9.9.9-test\n", ""},
		{[]string{"version", "--json"}, 0, "{\"valid\":true,\"diagnostics\":[],\"changedPaths\":[],\"version\":\"v9.9.9-test\"}\n", ""},
		{nil, 2, "", "用法：wa-cli"},
		{[]string{"nope"}, 2, "", "未知命令：nope"},
		{[]string{"version", "--unknown"}, 2, "", "未知参数：--unknown"},
		{[]string{"nope", "--json"}, 2, "{\"valid\":false,\"diagnostics\":[{\"code\":\"usage.unknown-command\",\"severity\":\"error\",\"message\":\"未知命令：nope\",\"next\":[\"wa-cli help --json\"]}],\"changedPaths\":[]}\n", ""},
	}
	for _, tt := range tests {
		var stdout, stderr bytes.Buffer
		cmd := exec.Command(bin, tt.args...)
		cmd.Stdout, cmd.Stderr = &stdout, &stderr
		code := 0
		if err := cmd.Run(); err != nil {
			var exitErr *exec.ExitError
			if !errors.As(err, &exitErr) {
				t.Fatalf("%v: %v", tt.args, err)
			}
			code = exitErr.ExitCode()
		}
		if code != tt.wantCode {
			t.Errorf("%v: exit code = %d, want %d", tt.args, code, tt.wantCode)
		}
		if stdout.String() != tt.wantStdout {
			t.Errorf("%v: stdout = %q, want %q", tt.args, stdout.String(), tt.wantStdout)
		}
		if tt.wantStderr == "" && stderr.Len() > 0 {
			t.Errorf("%v: stderr = %q, want empty", tt.args, stderr.String())
		}
		if !strings.Contains(stderr.String(), tt.wantStderr) {
			t.Errorf("%v: stderr %q does not contain %q", tt.args, stderr.String(), tt.wantStderr)
		}
	}

	// 二进制位于仓库之外，并在另一个临时目录中运行：Schema 随二进制提供，不依赖仓库文件。
	project := t.TempDir()
	for _, args := range [][]string{{"init", "--json"}, {"validate", "--json"}} {
		cmd := exec.Command(bin, args...)
		cmd.Dir = project
		out, err := cmd.Output()
		var res struct {
			Valid bool `json:"valid"`
		}
		if err != nil || json.Unmarshal(out, &res) != nil || !res.Valid {
			t.Fatalf("%v in %s: err = %v, stdout = %s", args, project, err, out)
		}
	}

	var rootStderr bytes.Buffer
	rootCmd := exec.Command(bin, "--help")
	rootCmd.Stderr = &rootStderr
	rootHelp, err := rootCmd.Output()
	if err != nil || rootStderr.Len() > 0 {
		t.Fatalf("--help: err = %v, stderr = %q", err, rootStderr.String())
	}
	if help, _ := exec.Command(bin, "help").Output(); string(rootHelp) != string(help) {
		t.Fatalf("--help = %q, want output of help %q", rootHelp, help)
	}

	if full, err := os.OpenFile("/dev/full", os.O_WRONLY, 0); err == nil {
		var stderr bytes.Buffer
		cmd := exec.Command(bin, "nope", "--json")
		cmd.Stdout, cmd.Stderr = full, &stderr
		err := cmd.Run()
		full.Close()
		var exitErr *exec.ExitError
		if !errors.As(err, &exitErr) || exitErr.ExitCode() != 1 || !strings.Contains(stderr.String(), "写入输出失败") {
			t.Fatalf("nope --json > /dev/full: err = %v, stderr = %q; want exit code 1 and a write failure report", err, stderr.String())
		}
	} else {
		t.Logf("跳过写入失败检查：无法打开 /dev/full：%v", err)
	}

	out, err := exec.Command(bin, "help", "--json").Output()
	if err != nil {
		t.Fatalf("help --json: %v", err)
	}
	var help struct {
		Valid    bool `json:"valid"`
		Commands []struct {
			Name string `json:"name"`
		} `json:"commands"`
	}
	if err := json.Unmarshal(out, &help); err != nil || !help.Valid || len(help.Commands) == 0 {
		t.Fatalf("help --json = %s (err %v), want a valid envelope listing commands", out, err)
	}
}

// TestNoDevToolDependency 确认产品二进制不依赖 internal/tools/** 下的开发工具。
func TestNoDevToolDependency(t *testing.T) {
	out, err := exec.Command("go", "list", "-deps", ".").Output()
	if err != nil {
		t.Fatalf("go list -deps: %v", err)
	}
	for _, pkg := range strings.Fields(string(out)) {
		if strings.HasPrefix(pkg, module+"/internal/tools/") {
			t.Errorf("wa-cli depends on dev tool package %s", pkg)
		}
	}
}
