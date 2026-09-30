package cli

import (
	"bytes"
	"encoding/json"
	"errors"
	"strings"
	"testing"

	"github.com/kw12121212/writing-anything/internal/version"
)

func run(t *testing.T, args ...string) (code int, stdout, stderr string) {
	t.Helper()
	var out, errOut bytes.Buffer
	code = Run(args, &out, &errOut)
	return code, out.String(), errOut.String()
}

func TestVersionText(t *testing.T) {
	code, stdout, stderr := run(t, "version")
	if code != 0 {
		t.Fatalf("exit code = %d, want 0; stderr: %s", code, stderr)
	}
	if want := "wa-cli " + version.Version + "\n"; stdout != want {
		t.Fatalf("stdout = %q, want %q", stdout, want)
	}
	if stderr != "" {
		t.Fatalf("stderr = %q, want empty", stderr)
	}
}

func TestVersionJSON(t *testing.T) {
	code, stdout, stderr := run(t, "version", "--json")
	if code != 0 {
		t.Fatalf("exit code = %d, want 0; stderr: %s", code, stderr)
	}
	var got map[string]any
	if err := json.Unmarshal([]byte(stdout), &got); err != nil {
		t.Fatalf("stdout is not JSON: %v; stdout: %q", err, stdout)
	}
	if got["version"] != version.Version {
		t.Fatalf("version = %v, want %q", got["version"], version.Version)
	}
}

func TestUsageErrors(t *testing.T) {
	tests := []struct {
		name       string
		args       []string
		wantStderr []string
	}{
		{"no command", nil, []string{"用法：wa-cli"}},
		{"unknown command", []string{"frobnicate"}, []string{"未知命令：frobnicate", "用法：wa-cli"}},
		{"unknown flag", []string{"version", "--unknown"}, []string{"未知参数：--unknown", "用法：wa-cli version"}},
		{"flag with value", []string{"version", "--json=maybe"}, []string{"未知参数：--json=maybe", "用法：wa-cli version"}},
		{"unexpected argument", []string{"version", "extra"}, []string{"未知参数：extra", "用法：wa-cli version"}},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			code, stdout, stderr := run(t, tt.args...)
			if code != 2 {
				t.Fatalf("exit code = %d, want 2", code)
			}
			if stdout != "" {
				t.Fatalf("stdout = %q, want empty", stdout)
			}
			for _, want := range tt.wantStderr {
				if !strings.Contains(stderr, want) {
					t.Errorf("stderr %q does not contain %q", stderr, want)
				}
			}
		})
	}
}

type failingWriter struct{}

func (failingWriter) Write([]byte) (int, error) { return 0, errors.New("disk full") }

func TestVersionWriteFailure(t *testing.T) {
	for _, args := range [][]string{{"version"}, {"version", "--json"}} {
		var errOut bytes.Buffer
		if code := Run(args, failingWriter{}, &errOut); code != 1 {
			t.Fatalf("%v: exit code = %d, want 1", args, code)
		}
		if !strings.Contains(errOut.String(), "disk full") {
			t.Fatalf("%v: stderr %q does not report the write error", args, errOut.String())
		}
	}
}
