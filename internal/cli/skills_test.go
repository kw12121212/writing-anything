package cli

import (
	"encoding/json"
	"path/filepath"
	"regexp"
	"slices"
	"strings"
	"testing"

	"github.com/kw12121212/writing-anything/internal/skills"
)

// 每个技能引用的 wa-cli 命令都存在于 wa-cli help --json 中，命令之后写出的参数也都是该命令声明的参数（skills-reference-commands）。
func TestSkillCommandsExist(t *testing.T) {
	_, help := runJSON(t, "help", "--json")
	flags := map[string][]string{}
	for _, c := range help["commands"].([]any) {
		cm := c.(map[string]any)
		var names []string
		for _, f := range cm["flags"].([]any) {
			names = append(names, f.(map[string]any)["name"].(string))
		}
		flags[cm["name"].(string)] = names
	}
	ref := regexp.MustCompile("wa-cli ([a-z][a-z-]*)(?: ([a-z][a-z-]*))?([^`\n]*)")
	for _, s := range skills.All() {
		for _, m := range ref.FindAllStringSubmatch(string(s.Content), -1) {
			name, rest := m[1], m[3]
			if _, ok := flags[name+" "+m[2]]; m[2] != "" && ok {
				name = name + " " + m[2]
			} else if m[2] != "" {
				if _, group := flags[name]; !group {
					t.Errorf("skill %s references unknown command %q", s.Name, name+" "+m[2])
					continue
				}
				rest = " " + m[2] + rest
			}
			declared, ok := flags[name]
			if !ok {
				t.Errorf("skill %s references unknown command %q", s.Name, name)
				continue
			}
			for _, f := range regexp.MustCompile(`--([a-z][a-z-]*)`).FindAllStringSubmatch(rest, -1) {
				if !slices.Contains(declared, f[1]) {
					t.Errorf("skill %s: wa-cli %s has no flag --%s", s.Name, name, f[1])
				}
			}
		}
	}
	// skills list 的 commands 按命令注册表规范化：命令加实参时只取命令名。
	_, list := runJSON(t, "skills", "list", "--json")
	for _, sk := range list["skills"].([]any) {
		for _, c := range sk.(map[string]any)["commands"].([]any) {
			if _, ok := flags[c.(string)]; !ok {
				t.Errorf("skills list command %q is not a registered command", c)
			}
		}
	}
}

func TestSkillsCommands(t *testing.T) {
	code, list := runJSON(t, "skills", "list", "--json")
	if code != 0 || len(list["skills"].([]any)) != 12 || len(list["skills"].([]any)[0].(map[string]any)["commands"].([]any)) == 0 {
		t.Fatalf("skills list: %v", list)
	}
	dir := t.TempDir()
	mustRunWS(t, dir, "init")
	code, res := runWS(t, dir, "skills", "install", "--agent", "codex")
	var out struct {
		Skills []map[string]any `json:"skills"`
	}
	if err := json.Unmarshal([]byte(res.stdout), &out); err != nil {
		t.Fatal(err)
	}
	if code != 0 || len(out.Skills) != 12 || out.Skills[0]["path"] != ".agents/skills/wa-assemble/SKILL.md" || len(res.ChangedPaths) != 26 {
		t.Fatalf("install codex: code = %d, %+v %v", code, out.Skills, res.ChangedPaths)
	}
	if !strings.Contains(readFile(t, filepath.Join(dir, ".agents", "skills", "wa-auto", "SKILL.md")), "name: wa-auto") {
		t.Fatal("skill content")
	}
	if code, res := runWS(t, dir, "skills", "install", "--agent", "codex"); code != 0 || len(res.ChangedPaths) != 0 {
		t.Fatalf("reinstall: %+v", res)
	}
	writeFile(t, filepath.Join(dir, ".agents", "skills", "wa-init", "SKILL.md"), "改过\n")
	if code, res := runWS(t, dir, "skills", "install", "--agent", "codex"); code != 1 || diag(t, res, "skills.modified").Next[0] != "wa-cli help skills install --json" {
		t.Fatalf("modified: %+v", res.Diagnostics)
	}
	if code, _ := runWS(t, dir, "skills", "install", "--agent", "codex", "--force"); code != 0 {
		t.Fatal("force")
	}
	for _, tc := range []struct {
		args []string
		code string
	}{{[]string{"--agent", "vim"}, "input.invalid-value"}, {[]string{"--agent", "vim", "--target", "x"}, "input.invalid-value"},
		{nil, "input.invalid-value"}, {[]string{"--target", ".writing-anything/skills"}, "input.invalid-value"},
		{[]string{"--target", filepath.Join(dir, ".writing-anything", "skills")}, "input.invalid-value"},
		{[]string{"--target", "../" + filepath.Base(dir) + "/.writing-anything/s2"}, "input.invalid-value"},
		{[]string{"--target", ".Writing-Anything/skills"}, "input.invalid-value"}} {
		if code, res := runWS(t, dir, append([]string{"skills", "install"}, tc.args...)...); code != 1 || len(diag(t, res, tc.code).Next) == 0 {
			t.Errorf("%v: %+v", tc.args, res.Diagnostics)
		}
	}
	// --target 为项目目录之外的绝对路径：changedPaths 只列项目之内的产物。
	if code, res := runWS(t, dir, "skills", "install", "--target", t.TempDir()); code != 0 || len(res.ChangedPaths) != 0 {
		t.Fatalf("absolute target: %+v", res)
	}
	for _, args := range [][]string{{"skills", "list"}, {"skills", "install", "--agent", "claude-code", "--dir", dir}} {
		if code, stdout, _ := run(t, args...); code != 0 || stdout == "" {
			t.Errorf("%v text: %q", args, stdout)
		}
	}
}

// 内置技能覆盖长篇生命周期：关键命令都至少被一个技能引用，且都是 help --json 登记的命令；wa-auto 按文体分出 novel 路径
// 并说明交接记录的写入与接续（skills/novel-skill-coverage）。
func TestNovelSkillCoverage(t *testing.T) {
	_, help := runJSON(t, "help", "--json")
	registered := map[string]bool{}
	for _, c := range help["commands"].([]any) {
		registered[c.(map[string]any)["name"].(string)] = true
	}
	_, list := runJSON(t, "skills", "list", "--json")
	referenced := map[string][]string{}
	for _, sk := range list["skills"].([]any) {
		m := sk.(map[string]any)
		for _, c := range m["commands"].([]any) {
			referenced[c.(string)] = append(referenced[c.(string)], m["name"].(string))
		}
	}
	for _, cmd := range []string{"scene contract", "scene close-out", "state confirm", "knowledge confirm", "continuity", "review plan",
		"review submit", "impact mark", "volume checkpoint", "volume confirm", "assemble", "handoff write"} {
		if !registered[cmd] {
			t.Errorf("novel command %q is not in wa-cli help --json", cmd)
		}
		if len(referenced[cmd]) == 0 {
			t.Errorf("novel command %q is not referenced by any built-in skill", cmd)
		}
	}
	var auto string
	for _, s := range skills.All() {
		if s.Name == "wa-auto" {
			auto = string(s.Content)
		}
	}
	for _, want := range []string{"小说路径（novel）", "wa-plan", "wa-scene", "wa-continuity", "wa-assemble", "wa-cli handoff write", "wa-cli handoff show", "`handoff`"} {
		if !strings.Contains(auto, want) {
			t.Errorf("wa-auto does not mention %q", want)
		}
	}
}

// cc-tb-1：按 --agent 安装时说明要在项目目录启动客户端（launchDir 与文本提示）；--target 时为 null。
func TestSkillsInstallLaunchDir(t *testing.T) {
	dir := t.TempDir()
	abs, _ := filepath.EvalSymlinks(dir)
	code, stdout, _ := run(t, "skills", "install", "--agent", "codex", "--dir", dir, "--json")
	var out struct {
		LaunchDir *string `json:"launchDir"`
	}
	json.Unmarshal([]byte(stdout), &out)
	if code != 0 || out.LaunchDir == nil || *out.LaunchDir != abs {
		t.Errorf("json: %d %s", code, stdout)
	}
	if code, stdout, _ := run(t, "skills", "install", "--agent", "claude-code", "--dir", dir); code != 0 || !strings.Contains(stdout, "在项目目录 "+abs+" 中启动 claude-code") {
		t.Errorf("text: %d %q", code, stdout)
	}
	code, stdout, _ = run(t, "skills", "install", "--target", "custom", "--dir", dir, "--json")
	out.LaunchDir = nil
	if json.Unmarshal([]byte(stdout), &out); code != 0 || out.LaunchDir != nil || !strings.Contains(stdout, `"launchDir":null`) {
		t.Errorf("target: %d %s", code, stdout)
	}
	// --agent 与 --target 同时给出时技能不在客户端扫描的位置，不给 launchDir。
	if code, stdout, _ := run(t, "skills", "install", "--agent", "codex", "--target", "custom", "--dir", dir, "--json"); code != 0 || !strings.Contains(stdout, `"launchDir":null`) {
		t.Errorf("agent+target: %d %s", code, stdout)
	}
	if code, stdout, _ := run(t, "skills", "install", "--agent", "codex", "--target", "custom", "--dir", dir); code != 0 || strings.Contains(stdout, "在项目目录") {
		t.Errorf("agent+target text: %d %q", code, stdout)
	}
	// 安装失败（技能被改过）时同样为 null，也不提示。
	writeFile(t, filepath.Join(dir, ".agents", "skills", "wa-init", "SKILL.md"), "作者改过\n")
	if code, stdout, _ := run(t, "skills", "install", "--agent", "codex", "--dir", dir, "--json"); code != 1 || !strings.Contains(stdout, `"launchDir":null`) {
		t.Errorf("failed: %d %s", code, stdout)
	}
	if code, stdout, _ := run(t, "skills", "install", "--agent", "codex", "--dir", dir); code != 1 || strings.Contains(stdout, "在项目目录") {
		t.Errorf("failed text: %d %q", code, stdout)
	}
}

// cx-tb-3：命令组下不存在的子命令同时提示同名前缀的独立命令（如 decision show → decisions）。
func TestUnknownSubcommandRelated(t *testing.T) {
	code, out := runJSON(t, "decision", "show", "dec-1", "--json")
	diags, _ := out["diagnostics"].([]any)
	if code != 2 || len(diags) != 1 {
		t.Fatalf("code %d, %v", code, out)
	}
	d := diags[0].(map[string]any)
	next, _ := d["next"].([]any)
	if !strings.Contains(d["message"].(string), "相关命令：decisions") || len(next) != 2 || next[0] != "wa-cli help decisions --json" || next[1] != "wa-cli help --json" {
		t.Errorf("diagnostic = %v", d)
	}
	// 只给组名与 help 的写法同样提示；没有相关命令的组保持原样。
	for _, tc := range []struct {
		args    []string
		related bool
	}{
		{[]string{"decision", "--json"}, true},
		{[]string{"help", "decision", "show", "--json"}, true},
		{[]string{"config", "nope", "--json"}, false},
	} {
		code, out := runJSON(t, tc.args...)
		d := out["diagnostics"].([]any)[0].(map[string]any)
		next, _ := d["next"].([]any)
		got := strings.Contains(d["message"].(string), "相关命令：")
		if code != 2 || d["code"] != "usage.unknown-command" || got != tc.related || (tc.related && next[0] != "wa-cli help decisions --json") || (!tc.related && (len(next) != 1 || next[0] != "wa-cli help --json")) {
			t.Errorf("%v: %d %v", tc.args, code, d)
		}
	}
}
