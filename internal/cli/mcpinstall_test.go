package cli

import (
	"encoding/json"
	"os"
	"path/filepath"
	"runtime"
	"strings"
	"testing"
)

type installOut struct {
	result
	Installs []installView `json:"installs"`
}

func runInstall(t *testing.T, dir string, args ...string) (int, installOut) {
	t.Helper()
	code, res := runWS(t, dir, append([]string{"mcp", "install"}, args...)...)
	var out installOut
	if err := json.Unmarshal([]byte(res.stdout), &out); err != nil {
		t.Fatalf("%v: %v\n%s", args, err, res.stdout)
	}
	out.result = res
	return code, out
}

// fakeClients 在一个临时目录中放入假的 claude 与 codex（把参数与工作目录追加到 log），并把它设为唯一的 PATH。
// failAdd 为 true 时 mcp add 以退出码 1 结束；环境变量 FAKE_NO_ENTRY 非空时 mcp remove 以退出码 1 结束（没有同名条目）。
func fakeClients(t *testing.T, failAdd bool, names ...string) string {
	t.Helper()
	if runtime.GOOS == "windows" {
		t.Skip("假客户端是 shell 脚本")
	}
	bin := t.TempDir()
	log := filepath.Join(bin, "log")
	for _, name := range names {
		script := "#!/bin/sh\necho \"$(pwd) " + name + " $*\" >> " + log + "\n"
		if failAdd {
			script += "if [ \"$2\" = add ]; then echo '配置写入失败'; exit 1; fi\n" // 错误写在标准输出
		}
		script += "if [ -n \"$FAKE_NO_ENTRY\" ] && [ \"$2\" = remove ]; then exit 1; fi\n"
		if err := os.WriteFile(filepath.Join(bin, name), []byte(script), 0o755); err != nil {
			t.Fatal(err)
		}
	}
	t.Setenv("PATH", bin)
	return log
}

func TestMCPInstallClients(t *testing.T) {
	dir := t.TempDir()
	abs, _ := filepath.Abs(dir)
	exe, _ := os.Executable()
	log := fakeClients(t, false, "claude", "codex")
	// claude-project
	code, res := runInstall(t, dir, "--client", "claude-code")
	if code != 0 || res.Installs[0].Status != "registered" || res.Installs[0].Command != exe || strings.Join(res.Installs[0].Args, " ") != "mcp --dir "+abs ||
		res.Installs[0].Path == nil || *res.Installs[0].Path != ".mcp.json" || len(res.ChangedPaths) != 0 {
		t.Fatalf("claude: %d %+v", code, res)
	}
	// codex-user；codex 不支持 project。
	if code, res := runInstall(t, dir, "--client", "codex", "--scope", "user"); code != 0 || res.Installs[0].Status != "registered" {
		t.Fatalf("codex: %d %+v", code, res)
	}
	code, res = runInstall(t, dir, "--client", "codex")
	if d := diag(t, res.result, "mcp.unsupported-scope"); code != 1 || res.Installs[0].Status != "failed" || !hasNext(d, "wa-cli mcp install --client codex --scope user") {
		t.Fatalf("codex project: %d %+v", code, res)
	}
	got := readFile(t, log)
	for _, want := range []string{
		abs + " claude mcp remove --scope project writing-anything",
		abs + " claude mcp add --scope project writing-anything -- " + exe + " mcp --dir " + abs,
		abs + " codex mcp remove writing-anything",
		abs + " codex mcp add writing-anything -- " + exe + " mcp --dir " + abs,
	} {
		if !strings.Contains(got, want+"\n") {
			t.Errorf("log lacks %q:\n%s", want, got)
		}
	}
	if strings.Count(got, "codex mcp add") != 1 {
		t.Errorf("codex project scope ran codex:\n%s", got)
	}
	// 人读输出。
	if code, stdout, _ := run(t, "mcp", "install", "--client", "claude-code", "--scope", "user", "--name", "wa", "--dir", dir); code != 0 || !strings.Contains(stdout, "claude-code（user）：registered") {
		t.Fatalf("text: %d %s", code, stdout)
	}
}

// client-not-found 与 client-failed；json 照常写入，其他客户端不受影响。
func TestMCPInstallMissingAndFailing(t *testing.T) {
	dir := t.TempDir()
	fakeClients(t, false) // PATH 中没有 claude
	code, res := runInstall(t, dir, "--client", "claude-code", "--client", "json", "--config", "mcp.json")
	if d := diag(t, res.result, "mcp.client-not-found"); code != 0 || d.Severity != "warning" || res.Installs[0].Status != "skipped" || res.Installs[1].Status != "updated" ||
		strings.Join(res.ChangedPaths, ",") != "mcp.json" || !hasNext(d, "wa-cli mcp install --client json") {
		t.Fatalf("not found: %d %+v", code, res)
	}
	fakeClients(t, true, "claude")
	code, res = runInstall(t, dir, "--client", "claude-code", "--name", "my-wa")
	if d := diag(t, res.result, "mcp.client-failed"); code != 1 || res.Installs[0].Status != "failed" || !strings.Contains(d.Message, "配置写入失败") ||
		!strings.Contains(d.Message, "已被移除") || !hasNext(d, "wa-cli mcp install --client claude-code --scope project --name my-wa") ||
		!hasNext(d, "wa-cli mcp install --client json --config .mcp.json --name my-wa") {
		t.Fatalf("failed: %d %+v", code, res)
	}
	// 作用域不支持：next 给出改用 user 的命令。
	fakeClients(t, false, "codex")
	if _, res := runInstall(t, dir, "--client", "codex"); !hasNext(diag(t, res.result, "mcp.unsupported-scope"), "wa-cli mcp install --client codex --scope user --name writing-anything") {
		t.Fatalf("unsupported scope next: %+v", res.Diagnostics)
	}
}

// json-idempotent：保留已有条目与其他键，第二次不写；项目之外的配置文件不列在 changedPaths 中。
func TestMCPInstallJSON(t *testing.T) {
	dir := t.TempDir()
	writeFile(t, filepath.Join(dir, "mcp.json"), `{"mcpServers": {"other": {"command": "x"}}, "theme": "dark"}`)
	code, res := runInstall(t, dir, "--client", "json", "--config", "mcp.json")
	if code != 0 || res.Installs[0].Status != "updated" || *res.Installs[0].Path != "mcp.json" || strings.Join(res.ChangedPaths, ",") != "mcp.json" {
		t.Fatalf("first: %d %+v", code, res)
	}
	var doc map[string]any
	json.Unmarshal([]byte(readFile(t, filepath.Join(dir, "mcp.json"))), &doc)
	servers := doc["mcpServers"].(map[string]any)
	abs, _ := filepath.Abs(dir)
	if doc["theme"] != "dark" || servers["other"] == nil || servers["writing-anything"].(map[string]any)["args"].([]any)[2] != abs {
		t.Fatalf("content: %v", doc)
	}
	before := readFile(t, filepath.Join(dir, "mcp.json"))
	if code, res := runInstall(t, dir, "--client", "json", "--config", "mcp.json"); code != 0 || res.Installs[0].Status != "unchanged" || len(res.ChangedPaths) != 0 ||
		readFile(t, filepath.Join(dir, "mcp.json")) != before {
		t.Fatalf("second: %d %+v", code, res)
	}
	outside := filepath.Join(t.TempDir(), "user.json")
	if code, res := runInstall(t, dir, "--client", "json", "--config", outside); code != 0 || *res.Installs[0].Path != outside || len(res.ChangedPaths) != 0 {
		t.Fatalf("outside: %d %+v", code, res)
	}
	// 已有的自身条目：只改 command 与 args，保留 env；不转义 & < >。
	writeFile(t, filepath.Join(dir, "env.json"), `{"mcpServers":{"writing-anything":{"command":"old","env":{"A":"1"}}},"url":"https://x?a=1&b=<2>"}`)
	runInstall(t, dir, "--client", "json", "--config", "env.json")
	if got := readFile(t, filepath.Join(dir, "env.json")); !strings.Contains(got, `"A": "1"`) || !strings.Contains(got, "a=1&b=<2>") || strings.Contains(got, `"old"`) {
		t.Fatalf("merge entry: %s", got)
	}
	for name, content := range map[string]string{"null.json": "null", "nullservers.json": `{"mcpServers":null}`} {
		writeFile(t, filepath.Join(dir, name), content)
		code, res := runInstall(t, dir, "--client", "json", "--config", name)
		if name == "null.json" && (code != 0 || res.Installs[0].Status != "updated") || name == "nullservers.json" && (code != 1 || diag(t, res.result, "mcp.invalid-config").Code == "") {
			t.Errorf("%s: %d %+v", name, code, res)
		}
	}
	writeFile(t, filepath.Join(dir, "bad.json"), "[1]")
	if code, res := runInstall(t, dir, "--client", "json", "--config", "bad.json"); code != 1 || diag(t, res.result, "mcp.invalid-config").Path != "bad.json" {
		t.Fatalf("invalid: %d %+v", code, res)
	}
}

func TestMCPInstallArguments(t *testing.T) {
	dir := t.TempDir()
	for _, args := range [][]string{
		{},
		{"--client", "cursor"},
		{"--client", "json"},
		{"--client", "claude-code", "--scope", "global"},
		{"--client", "claude-code", "--name", "Bad Name"},
		{"--client", "json", "--config", ".writing-anything/mcp.json"},
		{"--client", "json", "--config", ".WRITING-ANYTHING/mcp.json"},
	} {
		code, res := runInstall(t, dir, args...)
		if d := diag(t, res.result, "input.invalid-value"); code != 1 || d.Next[0] != "wa-cli help mcp install --json" || len(res.Installs) != 0 {
			t.Errorf("%v: %d %+v", args, code, res)
		}
	}
}

// 经符号链接指向工作区的配置路径同样被拒绝；不存在的 --dir 不会被创建。
func TestMCPInstallGuards(t *testing.T) {
	dir := newWorkspace(t)
	if err := os.Symlink(".writing-anything", filepath.Join(dir, "ws")); err != nil {
		t.Skip(err)
	}
	code, res := runInstall(t, dir, "--client", "json", "--config", "ws/evil.json")
	if code != 1 || diag(t, res.result, "input.invalid-value").Code == "" || fileExists(filepath.Join(dir, ".writing-anything", "evil.json")) {
		t.Fatalf("symlinked workspace: %d %+v", code, res)
	}
	missing := filepath.Join(t.TempDir(), "nope", "deep")
	code, stdout, _ := runIn(t, "", "mcp", "install", "--client", "json", "--config", "mcp.json", "--dir", missing, "--json")
	var out installOut
	json.Unmarshal([]byte(stdout), &out)
	if code != 1 || diag(t, out.result, "input.invalid-value").Field != "dir" || fileExists(missing) {
		t.Fatalf("missing dir: %d %s", code, stdout)
	}
	// 重复的客户端只注册一次。
	if _, res := runInstall(t, dir, "--client", "json", "--client", "json", "--config", "mcp.json"); len(res.Installs) != 1 {
		t.Fatalf("dedupe: %+v", res.Installs)
	}
}

// install-launch-hints：首次注册（没有同名条目）不说「重新创建」，并说明在哪个项目目录启动或换项目时如何重新注册。
func TestMCPInstallLaunchHints(t *testing.T) {
	dir := t.TempDir()
	abs, _ := filepath.Abs(dir)
	fakeClients(t, false, "claude", "codex")
	t.Setenv("FAKE_NO_ENTRY", "1")
	code, res := runInstall(t, dir, "--client", "claude-code", "--client", "codex", "--scope", "user", "--name", "my-wa")
	if code != 0 || len(res.Installs) != 2 {
		t.Fatalf("user: %d %+v", code, res)
	}
	for _, in := range res.Installs {
		want := "用户级注册指向项目目录 " + abs + "，换到其他项目时需在该项目重新运行 wa-cli mcp install --client " + in.Client + " --scope user --name my-wa"
		if in.Status != "registered" || strings.Contains(in.Message, "重新创建") || !strings.Contains(in.Message, want) {
			t.Errorf("%s: %+v", in.Client, in)
		}
	}
	code, res = runInstall(t, dir, "--client", "claude-code")
	if m := res.Installs[0].Message; code != 0 || strings.Contains(m, "重新创建") || !strings.Contains(m, "在项目目录 "+abs+" 中启动 Claude Code") || strings.Contains(m, "用户级") {
		t.Errorf("project: %d %+v", code, res.Installs)
	}
	t.Setenv("FAKE_NO_ENTRY", "")
	if code, res = runInstall(t, dir, "--client", "claude-code"); code != 0 || !strings.Contains(res.Installs[0].Message, "（同名条目重新创建）") {
		t.Errorf("again: %d %+v", code, res.Installs)
	}
}
