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
	if code != 0 || len(list["skills"].([]any)) != 9 || len(list["skills"].([]any)[0].(map[string]any)["commands"].([]any)) == 0 {
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
	if code != 0 || len(out.Skills) != 9 || out.Skills[0]["path"] != ".agents/skills/wa-auto/SKILL.md" || len(res.ChangedPaths) != 20 {
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
