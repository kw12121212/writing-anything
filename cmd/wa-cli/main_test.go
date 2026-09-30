package main

import (
	"bytes"
	"errors"
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
		{[]string{"version", "--json"}, 0, "{\"version\":\"v9.9.9-test\"}\n", ""},
		{nil, 2, "", "用法：wa-cli"},
		{[]string{"nope"}, 2, "", "未知命令：nope"},
		{[]string{"version", "--unknown"}, 2, "", "未知参数：--unknown"},
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
		if !strings.Contains(stderr.String(), tt.wantStderr) {
			t.Errorf("%v: stderr %q does not contain %q", tt.args, stderr.String(), tt.wantStderr)
		}
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
