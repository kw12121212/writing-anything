package skills

import (
	"bytes"
	"os"
	"path/filepath"
	"regexp"
	"slices"
	"strings"
	"testing"
)

// 每个内置技能都符合 Agent Skills 格式：目录名即 name，文件头只含 name 与 description，正文非空。
func TestBuiltinSkills(t *testing.T) {
	entries, err := embedded.ReadDir("skills")
	if err != nil {
		t.Fatal(err)
	}
	var names []string
	for _, e := range entries {
		data, err := embedded.ReadFile("skills/" + e.Name() + "/" + FileName)
		if err != nil {
			t.Fatalf("%s: %v", e.Name(), err)
		}
		s, problem := Parse(e.Name(), data)
		if s == nil {
			t.Fatalf("%s: %s", e.Name(), problem)
		}
		if !strings.Contains(string(data), "必须停下来问作者") || len(Commands(data)) == 0 {
			t.Errorf("%s: must say when to stop for the author and reference wa-cli commands", e.Name())
		}
		names = append(names, s.Name)
	}
	want := []string{"wa-assemble", "wa-auto", "wa-brief", "wa-continuity", "wa-draft", "wa-init", "wa-outline", "wa-plan", "wa-publish", "wa-review", "wa-revise", "wa-scene"}
	if !slices.Equal(names, want) || len(All()) != len(want) {
		t.Fatalf("skills = %v", names)
	}
}

func TestParse(t *testing.T) {
	for in, want := range map[string]string{
		"# 没有文件头\n":      "必须以 ---",
		"---\nname: x\n": "没有以 --- 结束",
		"---\nname: x\ndescription: d\nextra: 1\n---\n正文\n": "不是合法的 YAML",
		"---\nname: Bad_Name\ndescription: d\n---\n正文\n":    "不合法",
		"---\nname: y\ndescription: d\n---\n正文\n":           "不一致",
		"---\nname: x\ndescription: \" \"\n---\n正文\n":       "description",
		"---\nname: x\ndescription: d\n---\n\n":             "正文为空",
	} {
		if s, problem := Parse("x", []byte(in)); s != nil || !strings.Contains(problem, want) {
			t.Errorf("Parse(%q) = %v, %q; want %q", in, s, problem, want)
		}
	}
	if cmds := Commands([]byte("运行 `wa-cli brief set <单元>`、`wa-cli next --json` 与 wa-cli brief set；`wa-cli` 本身")); !slices.Equal(cmds, []string{"brief set", "next"}) {
		t.Fatalf("Commands = %v", cmds)
	}
}

// 安装：新建、取值未变、被改过时一个都不写（--force 才覆盖）、符号链接不写入、相对与绝对路径的输出。
func TestInstall(t *testing.T) {
	root := t.TempDir()
	r, ps := Install(root, ".claude/skills", false)
	if len(ps) > 0 || len(r.Skills) != 12 || !r.Skills[0].Changed || r.Skills[0].Path != ".claude/skills/wa-assemble/SKILL.md" ||
		!slices.Equal(r.Created[:2], []string{".claude", ".claude/skills"}) || len(r.Created) != 14 {
		t.Fatalf("install: %+v %+v", r, ps)
	}
	if r, ps := Install(root, ".claude/skills", false); len(ps) > 0 || len(r.Created) != 0 || slices.ContainsFunc(r.Skills, func(i Installed) bool { return i.Changed }) {
		t.Fatalf("reinstall: %+v %+v", r, ps)
	}
	file := filepath.Join(root, ".claude", "skills", "wa-init", FileName)
	other := filepath.Join(root, ".claude", "skills", "wa-auto", FileName)
	os.WriteFile(file, []byte("作者改过\n"), 0o600)
	os.Chmod(file, 0o600)
	os.Remove(other)
	if r, ps := Install(root, ".claude/skills", false); r != nil || len(ps) != 1 || ps[0].Code != CodeModified || ps[0].Path != ".claude/skills/wa-init/SKILL.md" {
		t.Fatalf("modified: %+v %+v", r, ps)
	}
	if _, err := os.Stat(other); err == nil {
		t.Fatal("a modified skill must stop every write")
	}
	if r, ps := Install(root, ".claude/skills", true); len(ps) > 0 || len(r.Skills) != 12 {
		t.Fatalf("force: %+v %+v", r, ps)
	}
	if info, _ := os.Stat(file); info.Mode().Perm() != 0o600 {
		t.Errorf("overwrite changed permissions to %v", info.Mode().Perm())
	}
	// 项目目录之外的目标：输出绝对路径，诊断不填 path、把绝对路径写进 message；
	// message 中的绝对路径是归一化后的真实路径拼写（Install 用 EvalSymlinks 归一）。
	abs := t.TempDir()
	if r, ps := Install(root, abs, false); len(ps) > 0 || !filepath.IsAbs(r.Skills[0].Path) || len(r.Created) != 0 {
		t.Fatalf("absolute target: %+v %+v", r, ps)
	}
	os.WriteFile(filepath.Join(abs, "wa-init", FileName), []byte("改过"), 0o644)
	absResolved, _ := filepath.EvalSymlinks(abs)
	if _, ps := Install(root, abs, false); len(ps) != 1 || ps[0].Path != "" || !strings.Contains(ps[0].Message, absResolved) {
		t.Fatalf("outside diagnostic: %+v", ps)
	}
	// 相对的项目目录：路径仍相对项目目录，项目之外的为绝对路径。
	wd, _ := os.Getwd()
	defer os.Chdir(wd)
	os.Chdir(filepath.Dir(root))
	rel := filepath.Base(root)
	if r, ps := Install(rel, "../"+filepath.Base(root)+"-out", false); len(ps) > 0 || !filepath.IsAbs(r.Skills[0].Path) {
		t.Fatalf("relative root, outside target: %+v %+v", r, ps)
	}
	if r, ps := Install(rel, filepath.Join(root, "inner"), false); len(ps) > 0 || r.Skills[0].Path != "inner/wa-assemble/SKILL.md" || r.Created[0] != "inner" {
		t.Fatalf("relative root, absolute inner target: %+v %+v", r, ps)
	}
	// 符号链接：技能目录本身、链接目标中已有技能子目录、技能文件。
	outside := t.TempDir()
	os.Mkdir(filepath.Join(outside, "wa-auto"), 0o755)
	if err := os.Symlink(outside, filepath.Join(root, "linked")); err != nil {
		t.Skip("symlink not supported:", err)
	}
	if r, ps := Install(root, "linked", false); r != nil || len(ps) != 1 || ps[0].Code != CodeInvalidPath || ps[0].Path != "linked" {
		t.Fatalf("symlinked target: %+v %+v", r, ps)
	}
	os.Remove(file)
	os.Symlink(filepath.Join(outside, "f"), file)
	if _, ps := Install(root, ".claude/skills", true); len(ps) != 1 || ps[0].Code != CodeInvalidPath {
		t.Fatalf("symlinked file: %+v", ps)
	}
	if entries, _ := os.ReadDir(filepath.Join(outside, "wa-auto")); len(entries) != 0 {
		t.Fatalf("wrote through a symlink: %v", entries)
	}
}

// 技能目录落在工作区中：相对路径、绝对路径、经由上级目录的相对路径与大小写变体都算。
func TestInWorkspace(t *testing.T) {
	root := t.TempDir()
	os.Mkdir(filepath.Join(root, ".writing-anything"), 0o755)
	for target, want := range map[string]bool{
		".writing-anything/skills":                            true,
		".writing-anything":                                   true,
		filepath.Join(root, ".writing-anything", "x"):         true,
		"../" + filepath.Base(root) + "/.writing-anything/s2": true,
		".Writing-Anything/skills":                            true,
		".claude/skills":                                      false,
		filepath.Join(t.TempDir(), "skills"):                  false,
		".writing-anything-other/skills":                      false,
	} {
		if got := InWorkspace(root, target); got != want {
			t.Errorf("InWorkspace(%q) = %v, want %v", target, got, want)
		}
	}
}

// cx-tb-1：技能文件是旧版内置内容（wa-cli 升级前安装、作者没有改过）时，重新安装直接更新，不报告 skills.modified
// （skills-install）。夹具为 5ca203b 时的内置 wa-auto。
func TestInstallUpdatesOutdatedBuiltin(t *testing.T) {
	root := t.TempDir()
	if _, ps := Install(root, ".agents/skills", false); len(ps) > 0 {
		t.Fatal(ps)
	}
	old, err := os.ReadFile(filepath.Join("testdata", "wa-auto-5ca203b.md"))
	if err != nil {
		t.Fatal(err)
	}
	file := filepath.Join(root, ".agents", "skills", "wa-auto", FileName)
	if err := os.WriteFile(file, old, 0o644); err != nil {
		t.Fatal(err)
	}
	r, ps := Install(root, ".agents/skills", false)
	if len(ps) > 0 {
		t.Fatalf("outdated builtin reported: %+v", ps)
	}
	for _, s := range r.Skills {
		if s.Changed != (s.Name == "wa-auto") {
			t.Errorf("%s changed = %v", s.Name, s.Changed)
		}
	}
	got, _ := os.ReadFile(file)
	for _, s := range All() {
		if s.Name == "wa-auto" && !bytes.Equal(got, s.Content) {
			t.Error("wa-auto was not updated to the current builtin")
		}
	}
}

// 历史清单格式正确、只登记内置技能，且含每个内置技能的当前内容：改动 SKILL.md 后必须追加新哈希。
func TestSkillHistoryCoversCurrent(t *testing.T) {
	names := map[string]bool{}
	for _, s := range All() {
		names[s.Name] = true
		if h := contentHash(s.Content); !history()[s.Name][h] {
			t.Errorf("internal/skills/history.txt 缺少 %s 当前内容的哈希；请追加一行：%s %s", s.Name, s.Name, h)
		}
	}
	seen := map[string]bool{}
	for i, line := range strings.Split(historyText, "\n") {
		if line == "" || strings.HasPrefix(line, "#") {
			continue
		}
		f := strings.Fields(line)
		if len(f) != 2 || !names[f[0]] || !regexp.MustCompile(`^[0-9a-f]{64}$`).MatchString(f[1]) || seen[line] {
			t.Errorf("history.txt 第 %d 行格式不对、技能不存在或重复：%q", i+1, line)
		}
		seen[line] = true
	}
}

// 每个技能都带「会话协议」一节：可执行 wrapper 模板（set -euo pipefail、失败即停）、吞错禁止与
// harness 提问回写决策队列的映射（skill-session-protocol-scripts）。
func TestSessionProtocolSection(t *testing.T) {
	for _, s := range All() {
		body := string(s.Content)
		for _, want := range []string{
			"## 会话协议（可执行模板）",
			"set -euo pipefail",
			"绝不 `|| true`、管道或输出重定向吞掉失败",
			"wa-cli decision add --question",
			"wa-cli decide <决策 id>",
		} {
			if !strings.Contains(body, want) {
				t.Errorf("%s: session protocol lacks %q", s.Name, want)
			}
		}
	}
}

// 旧版本只按本技能自己的历史判定；混合情况下作者改过的技能阻止全部写入，旧版本文件也不更新；CRLF 的旧版本同样识别。
func TestInstallOutdatedEdgeCases(t *testing.T) {
	old, err := os.ReadFile(filepath.Join("testdata", "wa-auto-5ca203b.md"))
	if err != nil {
		t.Fatal(err)
	}
	setup := func(t *testing.T) string {
		t.Helper()
		root := t.TempDir()
		if _, ps := Install(root, ".agents/skills", false); len(ps) > 0 {
			t.Fatal(ps)
		}
		return root
	}
	file := func(root, name string) string { return filepath.Join(root, ".agents", "skills", name, FileName) }

	t.Run("其他技能的旧版本", func(t *testing.T) {
		root := setup(t)
		os.WriteFile(file(root, "wa-init"), old, 0o644)
		if _, ps := Install(root, ".agents/skills", false); len(ps) != 1 || ps[0].Code != "skills.modified" || ps[0].Path != ".agents/skills/wa-init/SKILL.md" {
			t.Fatalf("problems = %+v", ps)
		}
	})
	t.Run("旧版本与改过的技能同时存在", func(t *testing.T) {
		root := setup(t)
		os.WriteFile(file(root, "wa-auto"), old, 0o644)
		os.WriteFile(file(root, "wa-init"), []byte("作者改过\n"), 0o644)
		if r, ps := Install(root, ".agents/skills", false); r != nil || len(ps) != 1 || ps[0].Code != "skills.modified" || ps[0].Path != ".agents/skills/wa-init/SKILL.md" {
			t.Fatalf("problems = %+v", ps)
		}
		if got, _ := os.ReadFile(file(root, "wa-auto")); !bytes.Equal(got, old) {
			t.Error("outdated wa-auto was written although another skill blocked the install")
		}
	})
	t.Run("CRLF 的旧版本", func(t *testing.T) {
		root := setup(t)
		os.WriteFile(file(root, "wa-auto"), bytes.ReplaceAll(old, []byte("\n"), []byte("\r\n")), 0o644)
		if r, ps := Install(root, ".agents/skills", false); len(ps) > 0 || !slices.ContainsFunc(r.Skills, func(i Installed) bool { return i.Name == "wa-auto" && i.Changed }) {
			t.Fatalf("crlf: %+v %+v", r, ps)
		}
	})
}

// cx-tb-2：使用阅读页（preview --serve）的内置技能必须说明它是作者确认期间持续运行的服务——在后台运行、交出地址前确认可以
// 访问、作者打不开时重新运行（preview-serve-guidance）。
func TestPreviewServeGuidance(t *testing.T) {
	var using []string
	for _, s := range All() {
		body := string(s.Content)
		if !strings.Contains(body, "--serve") {
			continue
		}
		using = append(using, s.Name)
		for _, want := range []string{"持续运行", "后台", "确认该地址可以访问", "打不开"} {
			if !strings.Contains(body, want) {
				t.Errorf("%s 使用 preview --serve 但没有说明「%s」", s.Name, want)
			}
		}
	}
	if !slices.Equal(using, []string{"wa-assemble", "wa-publish", "wa-revise"}) {
		t.Errorf("使用 preview --serve 的技能 = %v", using)
	}
}
