package mcpinstall

import (
	"encoding/json"
	"errors"
	"os"
	"path/filepath"
	"slices"
	"strings"
	"testing"
	"time"
)

// fakeEnv 记录运行的命令；fail 中的子命令（如 "mcp add"）以错误结束，missing 中的可执行文件找不到。
type fakeEnv struct {
	calls   [][]string
	dirs    []string
	fail    string
	missing string
}

func (f *fakeEnv) env() Env {
	return Env{
		LookPath: func(name string) (string, error) {
			if name == f.missing {
				return "", errors.New("not found")
			}
			return "/bin/" + name, nil
		},
		Run: func(dir, name string, args ...string) (string, error) {
			f.calls = append(f.calls, append([]string{name}, args...))
			f.dirs = append(f.dirs, dir)
			if f.fail != "" && strings.Join(args[:2], " ") == f.fail {
				return "已存在同名条目", errors.New("exit status 1")
			}
			return "", nil
		},
	}
}

func req(client, scope string) Request {
	return Request{Client: client, Scope: scope, Name: "writing-anything", Dir: "/proj", Command: "/usr/bin/wa-cli"}
}

func TestClaudeCode(t *testing.T) {
	for _, scope := range []string{ScopeProject, ScopeUser} {
		f := &fakeEnv{fail: "mcp remove"} // 没有同名条目时移除失败，忽略
		res, ps := Install(f.env(), req(ClientClaudeCode, scope))
		want := [][]string{
			{"/bin/claude", "mcp", "remove", "--scope", scope, "writing-anything"},
			{"/bin/claude", "mcp", "add", "--scope", scope, "writing-anything", "--", "/usr/bin/wa-cli", "mcp", "--dir", "/proj"},
		}
		if ps != nil || res.Status != StatusRegistered || len(f.calls) != 2 || !slices.Equal(f.calls[0], want[0]) || !slices.Equal(f.calls[1], want[1]) || f.dirs[1] != "/proj" {
			t.Fatalf("%s: %+v %+v %q", scope, res, ps, f.calls)
		}
	}
	f := &fakeEnv{fail: "mcp add"}
	res, ps := Install(f.env(), req(ClientClaudeCode, ScopeProject))
	if res.Status != StatusFailed || len(ps) != 1 || ps[0].Code != CodeClientFailed || ps[0].Severity != "error" || !strings.Contains(ps[0].Message, "已存在同名条目") ||
		!strings.Contains(ps[0].Message, "已被移除") {
		t.Fatalf("add failed: %+v %+v", res, ps)
	}
	f = &fakeEnv{missing: "claude"}
	res, ps = Install(f.env(), req(ClientClaudeCode, ScopeProject))
	if res.Status != StatusSkipped || len(ps) != 1 || ps[0].Code != CodeClientNotFound || ps[0].Severity != "warning" || len(f.calls) != 0 {
		t.Fatalf("not found: %+v %+v", res, ps)
	}
}

func TestCodex(t *testing.T) {
	f := &fakeEnv{}
	res, ps := Install(f.env(), req(ClientCodex, ScopeUser))
	if ps != nil || res.Status != StatusRegistered || !slices.Equal(f.calls[1], []string{"/bin/codex", "mcp", "add", "writing-anything", "--", "/usr/bin/wa-cli", "mcp", "--dir", "/proj"}) ||
		!slices.Equal(f.calls[0], []string{"/bin/codex", "mcp", "remove", "writing-anything"}) {
		t.Fatalf("codex: %+v %+v %q", res, ps, f.calls)
	}
	f = &fakeEnv{}
	if res, ps := Install(f.env(), req(ClientCodex, ScopeProject)); res.Status != StatusFailed || len(ps) != 1 || ps[0].Code != CodeUnsupportedScope || len(f.calls) != 0 {
		t.Fatalf("project scope: %+v %+v", res, ps)
	}
}

func TestJSONConfig(t *testing.T) {
	dir := t.TempDir()
	path := filepath.Join(dir, "sub", "mcp.json")
	r := req(ClientJSON, ScopeProject)
	r.Config = path
	// 文件不存在：创建，含目录。
	res, ps := Install(Env{}, r)
	if ps != nil || res.Status != StatusUpdated || res.Changed != path {
		t.Fatalf("create: %+v %+v", res, ps)
	}
	var doc map[string]map[string]map[string]any
	data, _ := os.ReadFile(path)
	if json.Unmarshal(data, &doc) != nil || doc["mcpServers"]["writing-anything"]["command"] != "/usr/bin/wa-cli" {
		t.Fatalf("created: %s", data)
	}
	// 已有其他条目与其他键：保留；第二次不写。
	os.WriteFile(path, []byte(`{"theme":"dark","mcpServers":{"other":{"command":"x","args":["y"]}}}`), 0o600)
	os.Chmod(path, 0o600) // 写入后保留已有文件的权限
	if res, ps := Install(Env{}, r); ps != nil || res.Status != StatusUpdated {
		t.Fatalf("merge: %+v %+v", res, ps)
	}
	data, _ = os.ReadFile(path)
	var merged map[string]any
	json.Unmarshal(data, &merged)
	servers := merged["mcpServers"].(map[string]any)
	if merged["theme"] != "dark" || servers["other"].(map[string]any)["command"] != "x" || servers["writing-anything"] == nil {
		t.Fatalf("merged: %s", data)
	}
	if info, _ := os.Stat(path); info.Mode().Perm() != 0o600 {
		t.Fatalf("mode: %v", info.Mode())
	}
	before, _ := os.ReadFile(path)
	if res, ps := Install(Env{}, r); ps != nil || res.Status != StatusUnchanged || res.Changed != "" {
		t.Fatalf("idempotent: %+v %+v", res, ps)
	}
	if after, _ := os.ReadFile(path); string(after) != string(before) {
		t.Fatal("unchanged config was rewritten")
	}
	// 不合法的配置：不是对象、mcpServers 不是对象、符号链接、目录。
	for name, content := range map[string]string{"array": "[1]", "servers": `{"mcpServers":[1]}`, "broken": "{"} {
		os.WriteFile(path, []byte(content), 0o644)
		if res, ps := Install(Env{}, r); res.Status != StatusFailed || len(ps) != 1 || ps[0].Code != CodeInvalidConfig {
			t.Errorf("%s: %+v %+v", name, res, ps)
		}
	}
	os.WriteFile(path, []byte(""), 0o644) // 空文件从空对象开始
	if res, ps := Install(Env{}, r); ps != nil || res.Status != StatusUpdated {
		t.Fatalf("empty: %+v %+v", res, ps)
	}
	link := filepath.Join(dir, "link.json")
	if err := os.Symlink(path, link); err == nil {
		r.Config = link
		if res, ps := Install(Env{}, r); res.Status != StatusFailed || ps[0].Code != CodeInvalidConfig {
			t.Errorf("symlink: %+v %+v", res, ps)
		}
	}
	r.Config = filepath.Join(path, "x.json") // 上级是文件，无法创建目录
	if res, ps := Install(Env{}, r); res.Status != StatusFailed || len(ps) != 1 {
		t.Errorf("write failure: %+v %+v", res, ps)
	}
}

func TestSystemEnv(t *testing.T) {
	env := System()
	if _, err := env.LookPath("definitely-not-a-command-wa"); err == nil {
		t.Fatal("LookPath found a missing command")
	}
	sh, err := env.LookPath("sh")
	if err != nil {
		t.Skip(err)
	}
	if out, err := env.Run(t.TempDir(), sh, "-c", "echo out; echo oops >&2; exit 3"); err == nil || !strings.Contains(out, "oops") || !strings.Contains(out, "out") {
		t.Fatalf("run: %q %v", out, err)
	}
	if out, err := env.Run(t.TempDir(), sh, "-c", "head -c 9000 /dev/zero | tr '\\0' x"); err != nil || len(out) > maxOutput+10 {
		t.Fatalf("truncate: %d %v", len(out), err)
	}
	// 超时后，仍持有输出管道的子进程不让 Run 一直等待。
	defer func(d time.Duration) { timeout = d }(timeout)
	timeout = 200 * time.Millisecond
	start := time.Now()
	if _, err := env.Run(t.TempDir(), sh, "-c", "sleep 30 & sleep 30"); err == nil {
		t.Fatal("timeout not reported")
	}
	if d := time.Since(start); d > WaitDelay+5*time.Second {
		t.Fatalf("Run returned after %v", d)
	}
}

// cc-tb-1：注册结果说明要在哪个项目目录启动客户端；只有确实移除了同名条目时才说「重新创建」。
func TestInstallLaunchHints(t *testing.T) {
	f := &fakeEnv{fail: "mcp remove"} // 首次注册：没有同名条目
	res, _ := Install(f.env(), req(ClientClaudeCode, ScopeProject))
	if !strings.Contains(res.Message, "在项目目录 /proj 中启动 Claude Code") || strings.Contains(res.Message, "重新创建") {
		t.Errorf("claude-code first: %q", res.Message)
	}
	f = &fakeEnv{} // 已有同名条目
	if res, _ := Install(f.env(), req(ClientClaudeCode, ScopeProject)); !strings.Contains(res.Message, "同名条目重新创建") {
		t.Errorf("claude-code again: %q", res.Message)
	}
	f = &fakeEnv{}
	if res, _ := Install(f.env(), req(ClientCodex, ScopeUser)); !strings.Contains(res.Message, "指向项目目录 /proj") || !strings.Contains(res.Message, "换到其他项目") {
		t.Errorf("codex: %q", res.Message)
	}
}

// 首次注册的 codex 不说「重新创建」；add 失败时不追加启动或换项目的提示。
func TestInstallLaunchHintsNegative(t *testing.T) {
	f := &fakeEnv{fail: "mcp remove"}
	if res, _ := Install(f.env(), req(ClientCodex, ScopeUser)); strings.Contains(res.Message, "重新创建") || !strings.Contains(res.Message, "--client codex --scope user --name writing-anything") {
		t.Errorf("codex first: %q", res.Message)
	}
	for _, c := range []struct{ client, scope string }{{ClientClaudeCode, ScopeProject}, {ClientClaudeCode, ScopeUser}, {ClientCodex, ScopeUser}} {
		f := &fakeEnv{fail: "mcp add"}
		res, _ := Install(f.env(), req(c.client, c.scope))
		if res.Status != StatusFailed || strings.Contains(res.Message, "启动") || strings.Contains(res.Message, "用户级注册指向") {
			t.Errorf("%s %s failed add: %q", c.client, c.scope, res.Message)
		}
	}
}
