package main

import (
	"bytes"
	"os"
	"path/filepath"
	"strings"
	"testing"
)

func check(t *testing.T, dir string, args ...string) (code int, stdout, stderr string) {
	t.Helper()
	var out, errOut bytes.Buffer
	code = run(args, dir, &out, &errOut)
	return code, out.String(), errOut.String()
}

// fixture 返回 testdata 下夹具的绝对路径。
func fixture(t *testing.T, name string) string {
	t.Helper()
	p, err := filepath.Abs(filepath.Join("testdata", name))
	if err != nil {
		t.Fatal(err)
	}
	return p
}

func writeFile(t *testing.T, path, content string) {
	t.Helper()
	if err := os.MkdirAll(filepath.Dir(path), 0o755); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(path, []byte(content), 0o644); err != nil {
		t.Fatal(err)
	}
}

// moduleDir 返回一个临时目录，其中的 go.mod 声明模块 example.com/m。
func moduleDir(t *testing.T) string {
	t.Helper()
	dir := t.TempDir()
	writeFile(t, filepath.Join(dir, "go.mod"), "module example.com/m\n\ngo 1.27\n")
	return dir
}

func TestCoverage(t *testing.T) {
	tests := []struct {
		name       string
		args       []string
		wantCode   int
		wantStdout string
		wantStderr string
	}{
		{"at threshold passes", []string{"-min", "80", "-profile", fixture(t, "pass.out")}, 0, "80.00%（4/5 条语句）", ""},
		{"below threshold fails", []string{"-min", "81", "-profile", fixture(t, "pass.out")}, 1, "", "80.00%（4/5 条语句）低于阈值 81.00%"},
		{"just below threshold is visible", []string{"-profile", fixture(t, "low.out")}, 1, "", "79.96%（7996/10000 条语句）低于阈值 80.00%"},
		{"default threshold is 80", []string{"-profile", fixture(t, "pass.out")}, 0, "阈值 80.00%", ""},
		{"out-of-scope files excluded", []string{"-min", "100", "-profile", fixture(t, "excluded.out")}, 0, "100.00%（1/1 条语句）", ""},
		{"duplicate blocks merged", []string{"-min", "75", "-profile", fixture(t, "duplicate.out")}, 0, "75.00%（3/4 条语句）", ""},
		{"empty scope fails", []string{"-profile", fixture(t, "empty-scope.out")}, 1, "", "没有 internal/**"},
		{"malformed count fails", []string{"-profile", fixture(t, "malformed.out")}, 1, "", "malformed.out:2"},
		{"malformed position fails", []string{"-profile", fixture(t, "badpos.out")}, 1, "", "badpos.out:2"},
		{"missing mode fails", []string{"-profile", fixture(t, "nomode.out")}, 1, "", "缺少 mode 行"},
		{"empty profile fails", []string{"-profile", fixture(t, "empty.out")}, 1, "", "为空"},
		{"missing profile fails", []string{"-profile", fixture(t, "absent.out")}, 1, "", "读取覆盖率文件失败"},
		{"unknown flag is usage error", []string{"-bogus"}, 2, "", "-bogus"},
		{"extra argument is usage error", []string{"-profile", fixture(t, "pass.out"), "extra"}, 2, "", "未知参数：extra"},
		{"NaN threshold is usage error", []string{"-min", "NaN", "-profile", fixture(t, "empty-scope.out")}, 2, "", "-min 必须在 0 到 100 之间"},
		{"negative threshold is usage error", []string{"-min", "-1", "-profile", fixture(t, "pass.out")}, 2, "", "-min 必须在 0 到 100 之间"},
		{"threshold above 100 is usage error", []string{"-min", "101", "-profile", fixture(t, "pass.out")}, 2, "", "-min 必须在 0 到 100 之间"},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			code, stdout, stderr := check(t, moduleDir(t), tt.args...)
			if code != tt.wantCode {
				t.Fatalf("exit code = %d, want %d; stdout: %q; stderr: %q", code, tt.wantCode, stdout, stderr)
			}
			if !strings.Contains(stdout, tt.wantStdout) {
				t.Errorf("stdout %q does not contain %q", stdout, tt.wantStdout)
			}
			if tt.wantStdout == "" && stdout != "" {
				t.Errorf("stdout = %q, want empty", stdout)
			}
			if !strings.Contains(stderr, tt.wantStderr) {
				t.Errorf("stderr %q does not contain %q", stderr, tt.wantStderr)
			}
		})
	}
}

func TestProfilePathRelativeToDir(t *testing.T) {
	pass, err := os.ReadFile(fixture(t, "pass.out"))
	if err != nil {
		t.Fatal(err)
	}
	t.Run("default profile is coverage.out", func(t *testing.T) {
		dir := moduleDir(t)
		writeFile(t, filepath.Join(dir, "coverage.out"), string(pass))
		if code, stdout, stderr := check(t, dir); code != 0 || !strings.Contains(stdout, "4/5") {
			t.Fatalf("exit code = %d, stdout = %q, stderr = %q", code, stdout, stderr)
		}
	})
	t.Run("relative profile", func(t *testing.T) {
		dir := moduleDir(t)
		writeFile(t, filepath.Join(dir, "sub", "p.out"), string(pass))
		if code, stdout, stderr := check(t, dir, "-profile", filepath.Join("sub", "p.out")); code != 0 || !strings.Contains(stdout, "4/5") {
			t.Fatalf("exit code = %d, stdout = %q, stderr = %q", code, stdout, stderr)
		}
	})
}

func TestModulePath(t *testing.T) {
	tests := []struct {
		name       string
		gomod      *string
		wantCode   int
		wantStderr string
	}{
		{"missing go.mod", nil, 1, "读取 go.mod 失败"},
		{"no module directive", ptr("go 1.27\n"), 1, "没有 module 指令"},
		{"quoted module with comment", ptr("module \"example.com/m\" // 注释\n"), 0, ""},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			dir := t.TempDir()
			if tt.gomod != nil {
				writeFile(t, filepath.Join(dir, "go.mod"), *tt.gomod)
			}
			code, _, stderr := check(t, dir, "-profile", fixture(t, "pass.out"))
			if code != tt.wantCode {
				t.Fatalf("exit code = %d, want %d; stderr: %q", code, tt.wantCode, stderr)
			}
			if !strings.Contains(stderr, tt.wantStderr) {
				t.Errorf("stderr %q does not contain %q", stderr, tt.wantStderr)
			}
		})
	}
}

func ptr(s string) *string { return &s }
