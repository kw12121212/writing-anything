package cli

import (
	"bytes"
	"encoding/json"
	"os"
	"path/filepath"
	"slices"
	"strings"
	"testing"
)

// newWorkspace 返回一个用 wa-cli init 初始化过的临时项目目录。
func newWorkspace(t *testing.T, flags ...string) string {
	t.Helper()
	dir := t.TempDir()
	if code, stdout, stderr := run(t, append([]string{"init", "--dir", dir}, flags...)...); code != 0 {
		t.Fatalf("init: exit code = %d; stdout: %s; stderr: %s", code, stdout, stderr)
	}
	return dir
}

func configPath(dir string) string { return filepath.Join(dir, ".writing-anything", "config.yaml") }

func readFile(t *testing.T, path string) string {
	t.Helper()
	data, err := os.ReadFile(path)
	if err != nil {
		t.Fatal(err)
	}
	return string(data)
}

func writeFile(t *testing.T, path, content string) {
	t.Helper()
	if err := os.WriteFile(path, []byte(content), 0o644); err != nil {
		t.Fatal(err)
	}
}

// result 是工作区命令 --json 输出中测试关心的部分。
type result struct {
	Valid        bool             `json:"valid"`
	Diagnostics  []Diagnostic     `json:"diagnostics"`
	ChangedPaths []string         `json:"changedPaths"`
	Workspace    string           `json:"workspace"`
	Checked      []string         `json:"checked"`
	Config       *json.RawMessage `json:"config"`
	Sources      count            `json:"sources"`
	Works        int              `json:"works"`
	Resolved     *json.RawMessage `json:"resolved"`
	stdout       string           // 原始输出，供解析命令专属字段
}

// count 是 status 输出的条目数；source list 的同名字段是数组，解码时忽略非数字的取值。
type count int

func (c *count) UnmarshalJSON(data []byte) error {
	var n int
	if json.Unmarshal(data, &n) == nil {
		*c = count(n)
	}
	return nil
}

// runWS 以 --json 运行工作区命令，并断言路径都相对项目目录、以 / 分隔（workspace-artifacts/workspace-relative-paths）。
func runWS(t *testing.T, dir string, args ...string) (int, result) {
	t.Helper()
	return runWSIn(t, dir, "", args...)
}

// runWSIn 与 runWS 相同，但以 input 作为标准输入。
func runWSIn(t *testing.T, dir, input string, args ...string) (int, result) {
	t.Helper()
	args = append(args, "--dir", dir, "--json")
	code, stdout, stderr := runIn(t, input, args...)
	if stderr != "" {
		t.Fatalf("%v: stderr = %q, want empty", args, stderr)
	}
	res := result{stdout: stdout}
	if err := json.Unmarshal([]byte(stdout), &res); err != nil {
		t.Fatalf("%v: stdout is not JSON: %v; %q", args, err, stdout)
	}
	paths := slices.Clone(res.ChangedPaths)
	for _, d := range res.Diagnostics {
		paths = append(paths, d.Path)
		if len(d.Next) == 0 {
			t.Errorf("%v: diagnostic %s has no next command", args, d.Code)
		}
		for _, n := range d.Next {
			// next 必须能直接粘贴运行：不含 <value> 这类会被 shell 当作重定向的占位符。
			if strings.ContainsAny(n, "<>") {
				t.Errorf("%v: next %q contains a placeholder", args, n)
			}
		}
	}
	for _, p := range paths {
		if strings.Contains(p, dir) || strings.Contains(p, `\`) || filepath.IsAbs(p) {
			t.Errorf("%v: path %q is not relative to the project directory", args, p)
		}
	}
	return code, res
}

func (r result) config(t *testing.T) map[string]any {
	t.Helper()
	if r.Config == nil {
		return nil
	}
	var m map[string]any
	if err := json.Unmarshal(*r.Config, &m); err != nil {
		t.Fatal(err)
	}
	return m
}

// diag 返回第一条错误码为 code 的诊断；没有时测试失败。
func diag(t *testing.T, r result, code string) Diagnostic {
	t.Helper()
	for _, d := range r.Diagnostics {
		if d.Code == code {
			return d
		}
	}
	t.Fatalf("no %s diagnostic in %+v", code, r.Diagnostics)
	return Diagnostic{}
}

func hasNext(d Diagnostic, prefix string) bool {
	return slices.ContainsFunc(d.Next, func(n string) bool { return strings.HasPrefix(n, prefix) })
}

func TestInitFresh(t *testing.T) {
	dir := t.TempDir()
	code, res := runWS(t, dir, "init")
	if code != 0 || !res.Valid || res.Workspace != ".writing-anything" {
		t.Fatalf("exit code = %d, result = %+v", code, res)
	}
	want := []string{".writing-anything", ".writing-anything/config.yaml", ".writing-anything/sources", ".writing-anything/works"}
	if !slices.Equal(res.ChangedPaths, want) {
		t.Fatalf("changedPaths = %v, want %v", res.ChangedPaths, want)
	}
	for _, sub := range []string{"sources", "works"} {
		if info, err := os.Stat(filepath.Join(dir, ".writing-anything", sub)); err != nil || !info.IsDir() {
			t.Fatalf("%s/ missing: %v", sub, err)
		}
	}
	cfg := readFile(t, configPath(dir))
	for _, line := range []string{"schema_version: 1\n", "language: zh-CN\n", "packs: []\n", "gate_level: standard\n"} {
		if !strings.Contains(cfg, line) {
			t.Errorf("config.yaml lacks %q:\n%s", line, cfg)
		}
	}
	if strings.Contains(cfg, "default_genre") {
		t.Errorf("config.yaml has default_genre without --genre:\n%s", cfg)
	}
	// init-then-validate
	if code, res := runWS(t, dir, "validate"); code != 0 || !res.Valid || len(res.Diagnostics) != 0 {
		t.Fatalf("validate after init: exit code = %d, result = %+v", code, res)
	}
}

func TestInitFromFlags(t *testing.T) {
	dir := newWorkspace(t, "--language", "en", "--genre", "minimal", "--gate-level", "strict")
	cfg := readFile(t, configPath(dir))
	for _, line := range []string{"language: en\n", "default_genre: minimal\n", "gate_level: strict\n"} {
		if !strings.Contains(cfg, line) {
			t.Errorf("config.yaml lacks %q:\n%s", line, cfg)
		}
	}
}

func TestInitRerun(t *testing.T) {
	dir := newWorkspace(t)
	edited := strings.Replace(readFile(t, configPath(dir)), "language: zh-CN", "language: en # 作者改过", 1)
	writeFile(t, configPath(dir), edited)
	code, res := runWS(t, dir, "init")
	if code != 0 || len(res.ChangedPaths) != 0 || readFile(t, configPath(dir)) != edited {
		t.Fatalf("rerun-no-change: exit code = %d, result = %+v", code, res)
	}
	// rerun-fills-missing
	os.Remove(filepath.Join(dir, ".writing-anything", "works"))
	code, res = runWS(t, dir, "init")
	if code != 0 || !slices.Equal(res.ChangedPaths, []string{".writing-anything/works"}) || readFile(t, configPath(dir)) != edited {
		t.Fatalf("rerun-fills-missing: exit code = %d, result = %+v", code, res)
	}
}

func TestInitConflictingFlag(t *testing.T) {
	dir := newWorkspace(t)
	code, res := runWS(t, dir, "init", "--gate-level", "strict")
	if code != 0 || !res.Valid {
		t.Fatalf("exit code = %d, result = %+v", code, res)
	}
	d := diag(t, res, "workspace.config-kept")
	if d.Severity != "warning" || d.Field != "gate_level" || d.Path != ".writing-anything/config.yaml" {
		t.Fatalf("diagnostic = %+v", d)
	}
	if !slices.Contains(d.Next, "wa-cli config set gate_level strict --dir "+dir) {
		t.Fatalf("next = %v, want the config set command that applies the flag", d.Next)
	}
	if !strings.Contains(readFile(t, configPath(dir)), "gate_level: standard\n") {
		t.Fatal("config.yaml was overwritten")
	}
}

func TestInitInvalidValue(t *testing.T) {
	dir := t.TempDir()
	code, res := runWS(t, dir, "init", "--gate-level", "bogus")
	if code != 1 || res.Valid {
		t.Fatalf("exit code = %d, result = %+v", code, res)
	}
	d := diag(t, res, "artifact.schema-violation")
	want := []string{
		"wa-cli init --dir " + dir + " --gate-level strict --json",
		"wa-cli init --dir " + dir + " --gate-level standard --json",
		"wa-cli init --dir " + dir + " --gate-level fast --json",
		"wa-cli help init --json",
	}
	if d.Field != "gate_level" || !slices.Equal(d.Next, want) {
		t.Fatalf("diagnostic = %+v, want next %v", d, want)
	}
	if _, res := runWS(t, dir, "init", "--language", "!!"); !slices.Equal(diag(t, res, "artifact.schema-violation").Next, []string{"wa-cli help init --json"}) {
		t.Fatalf("language next = %v", res.Diagnostics)
	}
	if _, err := os.Stat(filepath.Join(dir, ".writing-anything")); !os.IsNotExist(err) {
		t.Fatalf(".writing-anything was created: %v", err)
	}
}

func TestValidateMissingParts(t *testing.T) {
	dir := newWorkspace(t)
	os.Remove(filepath.Join(dir, ".writing-anything", "sources"))
	code, res := runWS(t, dir, "validate")
	d := diag(t, res, "workspace.missing-directory")
	if code != 1 || res.Valid || d.Path != ".writing-anything/sources" || !hasNext(d, "wa-cli init --dir ") {
		t.Fatalf("missing-directory: exit code = %d, diagnostic = %+v", code, d)
	}
	os.Remove(configPath(dir))
	code, res = runWS(t, dir, "validate")
	d = diag(t, res, "artifact.not-found")
	if code != 1 || d.Path != ".writing-anything/config.yaml" || !hasNext(d, "wa-cli init --dir ") || len(res.Checked) != 0 {
		t.Fatalf("missing-config: exit code = %d, result = %+v", code, res)
	}
}

func TestValidateConfigDiagnostics(t *testing.T) {
	base := "schema_version: 1\nlanguage: zh-CN\npacks: []\ngate_level: standard\n"
	tests := []struct {
		name, content, code, field, message string
	}{
		{"schema-violation", strings.Replace(base, "standard", "bogus", 1), "artifact.schema-violation", "gate_level", "第 4 行 gate_level：取值 \"bogus\""},
		{"unknown-field", base + "colour: red\n", "artifact.schema-violation", "colour", "colour"},
		{"array-field-path", strings.Replace(base, "packs: []", "packs: [Not_A_Id]", 1), "artifact.schema-violation", "packs[0]", "packs[0]"},
		{"parse-error", "schema_version: 1\nlanguage: zh-CN\npacks: [a, b\ngate_level: standard\n", "artifact.parse-error", "", "第 3 行"},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			dir := newWorkspace(t)
			writeFile(t, configPath(dir), tt.content)
			code, res := runWS(t, dir, "validate")
			d := diag(t, res, tt.code)
			if code != 1 || res.Valid || d.Severity != "error" || d.Path != ".writing-anything/config.yaml" || d.Field != tt.field || !strings.Contains(d.Message, tt.message) {
				t.Fatalf("exit code = %d, diagnostic = %+v", code, d)
			}
			if !hasNext(d, "wa-cli validate .writing-anything/config.yaml --dir ") {
				t.Errorf("next = %v, want a validate command", d.Next)
			}
			if tt.name == "schema-violation" {
				for _, level := range []string{"strict", "standard", "fast"} {
					if !slices.Contains(d.Next, "wa-cli config set gate_level "+level+" --dir "+dir+" --json") {
						t.Errorf("next = %v, want a config set command for %s", d.Next, level)
					}
				}
				if !strings.Contains(d.Message, "可用 wa-cli config set gate_level <value> 修改") {
					t.Errorf("message = %q, want a hint naming config set", d.Message)
				}
			}
			if tt.name == "array-field-path" && !slices.Contains(d.Next, "wa-cli help config set --json") {
				t.Errorf("next = %v, want help config set", d.Next)
			}
		})
	}
}

func TestValidateSingleArtifact(t *testing.T) {
	dir := newWorkspace(t)
	code, res := runWS(t, dir, "validate", ".writing-anything/config.yaml")
	if code != 0 || !res.Valid || !slices.Equal(res.Checked, []string{".writing-anything/config.yaml"}) {
		t.Fatalf("single-valid: exit code = %d, result = %+v", code, res)
	}
	writeFile(t, filepath.Join(dir, "notes.yaml"), "a: 1\n")
	code, res = runWS(t, dir, "validate", "notes.yaml")
	if d := diag(t, res, "artifact.unknown-kind"); code != 1 || res.Valid || d.Path != "notes.yaml" || !hasNext(d, "wa-cli help validate") {
		t.Fatalf("unknown-kind: exit code = %d, result = %+v", code, res)
	}
	outside := filepath.Join(filepath.Dir(dir), "outside.yaml")
	code, res = runWS(t, dir, "validate", outside)
	if d := diag(t, res, "artifact.unknown-kind"); code != 1 || d.Path != "../outside.yaml" {
		t.Fatalf("outside absolute path: exit code = %d, diagnostic = %+v", code, d)
	}
	os.Remove(configPath(dir))
	code, res = runWS(t, dir, "validate", ".writing-anything/config.yaml")
	if diag(t, res, "artifact.not-found"); code != 1 {
		t.Fatalf("artifact-missing: exit code = %d", code)
	}
}

func TestNoWorkspace(t *testing.T) {
	for _, args := range [][]string{{"validate"}, {"status"}, {"config", "set", "gate_level", "fast"}} {
		dir := t.TempDir()
		code, res := runWS(t, dir, args...)
		d := diag(t, res, "workspace.not-found")
		if code != 1 || res.Valid || !slices.Contains(d.Next, "wa-cli init --dir "+dir+" --json") {
			t.Fatalf("%v: exit code = %d, diagnostic = %+v", args, code, d)
		}
	}
}

func TestStatus(t *testing.T) {
	dir := newWorkspace(t, "--genre", "minimal")
	code, res := runWS(t, dir, "status")
	cfg := res.config(t)
	if code != 0 || !res.Valid || res.Workspace != ".writing-anything" || cfg["defaultGenre"] != "minimal" || cfg["gateLevel"] != "standard" || res.Sources != 0 || res.Works != 0 {
		t.Fatalf("status-fresh: exit code = %d, result = %+v, config = %v", code, res, cfg)
	}
	ws := filepath.Join(dir, ".writing-anything")
	for _, slug := range []string{"a", "b"} {
		if code, _ := runWS(t, dir, "new", "minimal", slug); code != 0 {
			t.Fatalf("new %s: exit code = %d", slug, code)
		}
	}
	writeFile(t, filepath.Join(ws, "sources", "interview.md"), "")
	if _, res := runWS(t, dir, "status"); !res.Valid || res.Sources != 1 || res.Works != 2 {
		t.Fatalf("status-counts: valid = %v, sources = %d, works = %d", res.Valid, res.Sources, res.Works)
	}
	writeFile(t, configPath(dir), strings.Replace(readFile(t, configPath(dir)), "gate_level: standard", "gate_level: bogus", 1))
	code, res = runWS(t, dir, "status")
	if diag(t, res, "artifact.schema-violation"); code != 1 || res.Valid || res.Workspace != ".writing-anything" || res.Works != 2 || res.config(t)["gateLevel"] != "bogus" {
		t.Fatalf("status-invalid-config: exit code = %d, result = %+v", code, res)
	}
	writeFile(t, configPath(dir), "a: [\n")
	if _, res := runWS(t, dir, "status"); res.Config != nil && string(*res.Config) != "null" {
		t.Fatalf("config = %s, want null for an unparsable config", *res.Config)
	}
}

func TestConfigSet(t *testing.T) {
	dir := newWorkspace(t, "--genre", "minimal")
	writeFile(t, configPath(dir), "# 作者的注释\n"+readFile(t, configPath(dir)))
	code, res := runWS(t, dir, "config", "set", "gate_level", "strict")
	if code != 0 || !res.Valid || !slices.Equal(res.ChangedPaths, []string{".writing-anything/config.yaml"}) || res.config(t)["gateLevel"] != "strict" {
		t.Fatalf("set-gate-level: exit code = %d, result = %+v", code, res)
	}
	if cfg := readFile(t, configPath(dir)); !strings.Contains(cfg, "# 作者的注释\n") || !strings.Contains(cfg, "gate_level: strict\n") || !strings.Contains(cfg, "default_genre: minimal\n") {
		t.Fatalf("config.yaml =\n%s", cfg)
	}
	if code, _ := runWS(t, dir, "config", "set", "packs", "house-style, glossary-zh"); code != 0 || !strings.Contains(readFile(t, configPath(dir)), "packs: [house-style, glossary-zh]\n") {
		t.Fatalf("set-packs: exit code = %d; config.yaml =\n%s", code, readFile(t, configPath(dir)))
	}
	code, res = runWS(t, dir, "config", "set", "default_genre", "")
	if code != 0 || res.config(t)["defaultGenre"] != nil || strings.Contains(readFile(t, configPath(dir)), "default_genre") {
		t.Fatalf("clear-default-genre: exit code = %d, result = %+v", code, res)
	}
	code, res = runWS(t, dir, "config", "set", "gate_level", "strict")
	if code != 0 || len(res.ChangedPaths) != 0 {
		t.Fatalf("set-same-value: exit code = %d, result = %+v", code, res)
	}
}

func TestConfigSetRejects(t *testing.T) {
	dir := newWorkspace(t)
	before := readFile(t, configPath(dir))
	code, res := runWS(t, dir, "config", "set", "gate_level", "bogus")
	if d := diag(t, res, "artifact.schema-violation"); code != 1 || res.Valid || d.Field != "gate_level" || len(res.ChangedPaths) != 0 {
		t.Fatalf("set-invalid-value: exit code = %d, result = %+v", code, res)
	}
	code, res = runWS(t, dir, "config", "set", "schema_version", "2")
	if d := diag(t, res, "config.unknown-key"); code != 1 || !slices.Contains(d.Next, "wa-cli help config set --json") {
		t.Fatalf("set-unknown-key: exit code = %d, result = %+v", code, res)
	}
	if readFile(t, configPath(dir)) != before {
		t.Fatal("rejected config set modified config.yaml")
	}
}

func TestConfigSetAnchored(t *testing.T) {
	dir := newWorkspace(t)
	content := "schema_version: 1\nlanguage: zh-CN\ndefault_genre: &genre tech-blog\npacks: [*genre]\ngate_level: fast\n"
	writeFile(t, configPath(dir), content)
	code, res := runWS(t, dir, "config", "set", "default_genre", "")
	if d := diag(t, res, "config.anchored-value"); code != 1 || d.Field != "default_genre" || !strings.Contains(d.Message, "第 3 行") {
		t.Fatalf("exit code = %d, diagnostic = %+v", code, d)
	}
	if readFile(t, configPath(dir)) != content {
		t.Fatal("config.yaml changed")
	}
}

func TestWriteFailedNext(t *testing.T) {
	dir := t.TempDir()
	writeFile(t, filepath.Join(dir, ".writing-anything"), "")
	code, res := runWS(t, dir, "init")
	if d := diag(t, res, "workspace.write-failed"); code != 1 || !slices.Equal(d.Next, []string{"wa-cli init --dir " + dir + " --json"}) {
		t.Fatalf("exit code = %d, diagnostic = %+v", code, d)
	}
}

func TestConfigSetRepairs(t *testing.T) {
	dir := newWorkspace(t)
	writeFile(t, configPath(dir), strings.Replace(readFile(t, configPath(dir)), "gate_level: standard", "gate_level: bogus", 1))
	if code, res := runWS(t, dir, "config", "set", "gate_level", "fast"); code != 0 || !res.Valid {
		t.Fatalf("repair-broken-field: exit code = %d, result = %+v", code, res)
	}
	if code, res := runWS(t, dir, "validate"); code != 0 || !res.Valid {
		t.Fatalf("validate after repair: exit code = %d, result = %+v", code, res)
	}
}

func TestCommandGroups(t *testing.T) {
	code, got := runJSON(t, "config", "set", "gate_level", "--json")
	ds := got["diagnostics"].([]any)
	d := ds[0].(map[string]any)
	if code != 2 || got["valid"] != false || d["code"] != "usage.missing-argument" || !strings.Contains(d["message"].(string), "<value>") || !slices.Contains(d["next"].([]any), any("wa-cli help config set --json")) {
		t.Fatalf("missing-argument: exit code = %d, got = %v", code, got)
	}
	docs := helpDocs(t, "help", "config", "set", "--json")
	if len(docs) != 1 || docs[0].Name != "config set" || len(docs[0].Args) != 2 || !docs[0].Args[0].Required || !docs[0].Args[1].Required {
		t.Fatalf("group-help: docs = %+v", docs)
	}
	_, helpOut, _ := run(t, "help", "config", "set")
	code, flagOut, stderr := run(t, "config", "set", "--help")
	if code != 0 || stderr != "" || flagOut != helpOut || !strings.Contains(flagOut, "wa-cli config set <key> <value>") {
		t.Fatalf("group-help-flag: exit code = %d, stdout = %q, help = %q", code, flagOut, helpOut)
	}
	for _, args := range [][]string{{"config", "--json"}, {"help", "config", "nope", "--json"}, {"config", "nope", "--json"}} {
		code, got := runJSON(t, args...)
		d := got["diagnostics"].([]any)[0].(map[string]any)
		if code != 2 || d["code"] != "usage.unknown-command" || !strings.Contains(d["message"].(string), "config set") {
			t.Fatalf("%v: exit code = %d, got = %v", args, code, got)
		}
	}
	// 单词命令后的多余参数仍是 usage.unexpected-argument。
	code, got = runJSON(t, "help", "version", "extra", "--json")
	if d := got["diagnostics"].([]any)[0].(map[string]any); code != 2 || d["code"] != "usage.unexpected-argument" || d["message"] != "多余的参数：extra" {
		t.Fatalf("help version extra: exit code = %d, got = %v", code, got)
	}
	code, _, stderr = run(t, "help", "version", "extra")
	if code != 2 || !strings.Contains(stderr, "用法：wa-cli help [<command>] [<subcommand>]") {
		t.Fatalf("help version extra (text): exit code = %d, stderr = %q", code, stderr)
	}
}

func TestWorkspaceText(t *testing.T) {
	dir := t.TempDir()
	code, stdout, stderr := run(t, "init", "--dir", dir)
	if code != 0 || stderr != "" || !strings.Contains(stdout, "已创建 .writing-anything/config.yaml\n") {
		t.Fatalf("init: exit code = %d, stdout = %q, stderr = %q", code, stdout, stderr)
	}
	if _, stdout, _ := run(t, "init", "--dir", dir); !strings.Contains(stdout, "已存在，没有需要补齐的文件") {
		t.Fatalf("init rerun: stdout = %q", stdout)
	}
	code, stdout, stderr = run(t, "init", "--dir", dir, "--gate-level", "fast")
	if code != 0 || !strings.Contains(stderr, "警告 [workspace.config-kept]") || !strings.Contains(stderr, "下一步：wa-cli config set gate_level fast --dir ") {
		t.Fatalf("init conflicting flag: exit code = %d, stderr = %q", code, stderr)
	}
	if code, stdout, _ := run(t, "validate", "--dir", dir); code != 0 || stdout != "校验通过：.writing-anything/config.yaml\n" {
		t.Fatalf("validate: exit code = %d, stdout = %q", code, stdout)
	}
	code, stdout, _ = run(t, "status", "--dir", dir)
	for _, line := range []string{"工作区（Workspace）：.writing-anything\n", "默认文体（Genre）：未设置\n", "规范包（Pack）：无\n", "关卡强度（Gate Level）：standard\n", "素材（Source）：0\n", "校验通过\n"} {
		if code != 0 || !strings.Contains(stdout, line) {
			t.Errorf("status: exit code = %d, stdout lacks %q:\n%s", code, line, stdout)
		}
	}
	if _, stdout, _ := run(t, "config", "set", "packs", "a,b", "--dir", dir); stdout != "已把 packs 设为 \"a,b\"\n" {
		t.Fatalf("config set: stdout = %q", stdout)
	}
	if _, stdout, _ := run(t, "config", "set", "packs", "a, b", "--dir", dir); stdout != "packs 已是 \"a, b\"，未修改\n" {
		t.Fatalf("config set same: stdout = %q", stdout)
	}
	if _, stdout, _ := run(t, "status", "--dir", dir); !strings.Contains(stdout, "规范包（Pack）：a、b\n") {
		t.Fatalf("status packs: stdout = %q", stdout)
	}
	writeFile(t, configPath(dir), "a: [\n")
	code, stdout, stderr = run(t, "status", "--dir", dir)
	if code != 1 || strings.Contains(stdout, "校验通过") || !strings.Contains(stderr, "错误 [artifact.parse-error]") {
		t.Fatalf("status broken: exit code = %d, stdout = %q, stderr = %q", code, stdout, stderr)
	}
	for _, args := range [][]string{{"validate"}, {"config", "set", "gate_level", "x"}, {"init", "--gate-level", "x"}} {
		code, stdout, stderr = run(t, append(args, "--dir", dir)...)
		if code != 1 || stdout != "" || !strings.Contains(stderr, "错误 [") {
			t.Errorf("%v: exit code = %d, stdout = %q, stderr = %q", args, code, stdout, stderr)
		}
	}
	empty := t.TempDir()
	code, stdout, stderr = run(t, "status", "--dir", empty)
	if code != 1 || stdout != "" || !strings.Contains(stderr, "错误 [workspace.not-found]") || !strings.Contains(stderr, "下一步：wa-cli init --dir "+empty) {
		t.Fatalf("status without workspace: exit code = %d, stdout = %q, stderr = %q", code, stdout, stderr)
	}
}

func TestShellCommandQuoting(t *testing.T) {
	dir := filepath.Join(t.TempDir(), "my dir")
	code, res := runWS(t, dir, "validate")
	if d := diag(t, res, "workspace.not-found"); code != 1 || !slices.Contains(d.Next, "wa-cli init --dir '"+dir+"' --json") {
		t.Fatalf("next = %v", d.Next)
	}
	if got := shellCommand("wa-cli", "config", "set", "default_genre", "", "--dir", "it's"); got != `wa-cli config set default_genre '' --dir 'it'\''s'` {
		t.Fatalf("shellCommand = %q", got)
	}
}

func TestDefaultProjectDir(t *testing.T) {
	// 未指定 --dir 时使用当前目录；这里只断言不会在当前目录创建工作区的只读命令。
	code, res := runJSON(t, "status", "--json")
	if code != 1 || res["valid"] != false {
		t.Fatalf("status in the package directory: exit code = %d, result = %v", code, res)
	}
}

func TestWorkspaceWriteFailure(t *testing.T) {
	dir := newWorkspace(t)
	for _, args := range [][]string{{"status", "--dir", dir}, {"status", "--dir", dir, "--json"}, {"init", "--dir", dir}, {"validate", "--dir", dir}} {
		var errOut bytes.Buffer
		if code := Run(args, strings.NewReader(""), failingWriter{}, &errOut); code != 1 || !strings.Contains(errOut.String(), "写入输出失败：disk full") {
			t.Fatalf("%v: exit code = %d, stderr = %q", args, code, errOut.String())
		}
	}
	// 标准错误不可写时，诊断写入失败同样是执行失败。
	os.Remove(configPath(dir))
	var out bytes.Buffer
	if code := Run([]string{"validate", "--dir", dir}, strings.NewReader(""), &out, failingWriter{}); code != 1 {
		t.Fatalf("exit code = %d, want 1", code)
	}
}

func TestValidateAliasExpansionLimit(t *testing.T) {
	dir := newWorkspace(t)
	var b strings.Builder
	b.WriteString("a0: &a0 [x, x, x, x, x, x, x, x, x, x]\n")
	for i := 1; i <= 8; i++ {
		refs := strings.Repeat(", *a"+string(rune('0'+i-1)), 10)[2:]
		b.WriteString("a" + string(rune('0'+i)) + ": &a" + string(rune('0'+i)) + " [" + refs + "]\n")
	}
	writeFile(t, configPath(dir), b.String())
	code, res := runWS(t, dir, "validate")
	if d := diag(t, res, "artifact.parse-error"); code != 1 || !strings.Contains(d.Message, "展开别名后超过") {
		t.Fatalf("exit code = %d, diagnostic = %+v", code, d)
	}
}
