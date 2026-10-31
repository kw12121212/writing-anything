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
	want := []string{"wa-assemble", "wa-auto", "wa-brief", "wa-continuity", "wa-draft", "wa-illustrate", "wa-init", "wa-outline", "wa-plan", "wa-publish", "wa-review", "wa-revise", "wa-scene"}
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
	if len(ps) > 0 || len(r.Skills) != 13 || !r.Skills[0].Changed || r.Skills[0].Path != ".claude/skills/wa-assemble/SKILL.md" ||
		!slices.Equal(r.Created[:2], []string{".claude", ".claude/skills"}) || len(r.Created) != 15 {
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
	if r, ps := Install(root, ".claude/skills", true); len(ps) > 0 || len(r.Skills) != 13 {
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

// cx-tb-2：使用阅读页（preview --serve）的内置技能必须先读取 config.reviewSurface：browser 时说明它是作者确认期间持续运行的
// 服务——在后台运行、交出地址前确认可以访问、作者打不开时重新运行；dsh 时不启动 --serve、请作者到 DSH 面板的「待审阅」入口审阅
// 并在完成后复查（preview-serve-guidance）。
func TestPreviewServeGuidance(t *testing.T) {
	var using []string
	for _, s := range All() {
		body := string(s.Content)
		if !strings.Contains(body, "--serve") {
			continue
		}
		using = append(using, s.Name)
		for _, want := range []string{"config.reviewSurface", "为 browser 时", "持续运行", "后台", "确认该地址可以访问", "打不开",
			"为 dsh 时", "不要启动 `wa-cli preview --serve`", "127.0.0.1", "DSH 插件", "「待审阅」", "复查",
			"为 zcode 时", "ZCode 插件",
			"面板里没有「待审阅」入口", "按运行环境自动给出", "不要自己判断"} {
			if !strings.Contains(body, want) {
				t.Errorf("%s 使用 preview --serve 但没有说明「%s」", s.Name, want)
			}
		}
		if !strings.Contains(body, "wa-cli review show") && !strings.Contains(body, "wa-cli next --json") {
			t.Errorf("%s 没有给出作者在 DSH 审阅完成后的复查命令（wa-cli review show 或 wa-cli next）", s.Name)
		}
	}
	if !slices.Equal(using, []string{"wa-assemble", "wa-auto", "wa-publish", "wa-revise"}) {
		t.Errorf("使用 preview --serve 的技能 = %v", using)
	}
}

// every-handoff-step-branches：任何交出阅读页地址（preview.url、pendingAuthorReviews 的 command、「阅读页地址」）的编号步骤都必须
// 写明审阅位置为 dsh 时的做法，否则 review_surface 为 dsh 时 Agent 仍会交出 127.0.0.1 链接（preview-serve-guidance）。
func TestPreviewHandoffStepsBranch(t *testing.T) {
	step := regexp.MustCompile(`^\d+\. `)
	for _, s := range All() {
		for _, line := range strings.Split(string(s.Content), "\n") {
			if !step.MatchString(line) || !(strings.Contains(line, "preview.url") || strings.Contains(line, "阅读页地址")) {
				continue
			}
			if !strings.Contains(line, "为 dsh 时") && !strings.Contains(line, "reviewSurface 为 dsh") {
				t.Errorf("%s 的步骤交出阅读页地址但没有 dsh 分支：%s", s.Name, line)
			}
			if !strings.Contains(line, "为 zcode 时") && !strings.Contains(line, "reviewSurface 为 zcode") {
				t.Errorf("%s 的步骤交出阅读页地址但没有 zcode 分支：%s", s.Name, line)
			}
			if !strings.Contains(line, "面板里没有「待审阅」入口") {
				t.Errorf("%s 的步骤交出阅读页地址但 dsh 分支没有兜底：%s", s.Name, line)
			}
		}
	}
}

// wa-illustrate 指导可选的配图环节（illustrate-skill），wa-publish 与 wa-auto 在验收前询问配图（illustrate-skill-prompt）。
func TestIllustrateSkills(t *testing.T) {
	byName := map[string]Skill{}
	for _, s := range All() {
		byName[s.Name] = s
	}
	ill := string(byName["wa-illustrate"].Content)
	for _, want := range []string{"lemo-opuscar", "LEMO_OPUSCAR_HOME", "~/lemo-opuscar", "setup.sh deps", "~/lemo-my-styles", "STYLE.md", "still.mjs", "--size", "配图计划", "--reaffirm", "必须停下来问作者", "--origin author", ".writing-anything/` 之外"} {
		if !strings.Contains(ill, want) {
			t.Errorf("wa-illustrate lacks %q", want)
		}
	}
	cmds := Commands(byName["wa-illustrate"].Content)
	for _, want := range []string{"illustration add", "illustration list", "illustration remove", "draft write", "review record", "claim list", "source list", "decision add", "decide"} {
		if !slices.Contains(cmds, want) {
			t.Errorf("wa-illustrate commands = %v, missing %s", cmds, want)
		}
	}
	for _, name := range []string{"wa-publish", "wa-auto"} {
		body := string(byName[name].Content)
		if !strings.Contains(body, "wa-illustrate") || !strings.Contains(body, "不需要时跳过") || !slices.Contains(Commands(byName[name].Content), "illustration list") {
			t.Errorf("%s lacks the illustration prompt", name)
		}
		if i, j := strings.Index(body, "wa-illustrate"), strings.Index(body, "accept <单元>"); name == "wa-publish" && (i < 0 || j < 0 || i > j) {
			t.Errorf("%s must ask about illustrations before acceptance", name)
		}
	}
	pub := string(byName["wa-publish"].Content)
	for _, want := range []string{".assets/", "export.assets", "配图清单"} {
		if !strings.Contains(pub, want) {
			t.Errorf("wa-publish export step lacks %q", want)
		}
	}
}

// wa-publish 说明连图带文复制：可粘贴页、两种「复制图文」入口、不含 AI 参与说明与封面、按配图清单补传（publish-copy-guidance）。
func TestPublishCopyGuidance(t *testing.T) {
	for _, s := range All() {
		if s.Name != "wa-publish" {
			continue
		}
		body := string(s.Content)
		export := body[strings.Index(body, "wa-cli export <作品> --format markdown"):]
		for _, want := range []string{"--format paste-html", "可粘贴页（Paste-ready Page）", "「复制图文」", "DSH 插件面板", "不含 AI 参与说明与封面图",
			"export.disclosure", "「【配图 n】」", "配图清单（export.imageList）", "约 1 MiB", "7 张", "「复制第 n 段」", "「复制全文」"} {
			if !strings.Contains(export, want) {
				t.Errorf("wa-publish export step lacks %q", want)
			}
		}
		if !slices.Contains(Commands(s.Content), "export") {
			t.Errorf("wa-publish commands lack export")
		}
		return
	}
	t.Fatal("wa-publish not found")
}

// 只有 wa-illustrate 提到 lemo-opuscar：wa-cli 不运行它（illustrate-skill-no-runtime-dependency）。
func TestOnlyIllustrateMentionsRenderer(t *testing.T) {
	for _, s := range All() {
		if s.Name != "wa-illustrate" && strings.Contains(string(s.Content), "lemo-opuscar") {
			t.Errorf("%s mentions lemo-opuscar", s.Name)
		}
	}
}

// illustrate-skill-no-runtime-dependency 的另一半：wa-cli 非测试源码（cmd/ 与 internal/，不含
// internal/tools 的开发工具）不得出现渲染器 lemo-opuscar、still.mjs 或其运行时（node、npx）的字面量，
// 防止未来变更把渲染调用悄悄带进单文件二进制。夹具与测试源码不受此限。
func TestProductSourceHasNoRenderer(t *testing.T) {
	var files []string
	for _, root := range []string{"../..", "../../cmd"} {
		err := filepath.Walk(root, func(p string, info os.FileInfo, err error) error {
			if err != nil {
				return err
			}
			if info.IsDir() {
				if info.Name() == "tools" && strings.HasSuffix(filepath.ToSlash(p), "internal/tools") {
					return filepath.SkipDir
				}
				return nil
			}
			name := info.Name()
			if !strings.HasSuffix(name, ".go") || strings.HasSuffix(name, "_test.go") {
				return nil
			}
			files = append(files, p)
			return nil
		})
		if err != nil {
			t.Fatal(err)
		}
	}
	if len(files) < 100 {
		t.Fatalf("扫描到的源码文件数 = %d，异常", len(files))
	}
	for _, p := range files {
		rel, err := filepath.Rel("../..", p)
		if err != nil {
			t.Fatal(err)
		}
		data, err := os.ReadFile(p)
		if err != nil {
			t.Fatal(err)
		}
		for _, line := range strings.Split(string(data), "\n") {
			trimmed := strings.TrimSpace(line)
			if strings.HasPrefix(trimmed, "//") {
				continue
			}
			for _, banned := range []string{"lemo-opuscar", "still.mjs", `"node"`, `"npx"`} {
				if strings.Contains(line, banned) {
					t.Errorf("%s 出现渲染器字面量 %s：%s", rel, banned, trimmed)
				}
			}
		}
	}
}

// skill-confirmation-guard（confirmation-guard-guidance）：wa-publish 与 wa-auto 写明无作者原话不得带
// --confirmed-by-author、试用在 standard 或 fast 关卡不带确认验收；wa-revise 写明小改走 --reaffirm 的分界。
func TestSkillConfirmationGuard(t *testing.T) {
	byName := map[string]Skill{}
	for _, s := range All() {
		byName[s.Name] = s
	}
	for _, name := range []string{"wa-publish", "wa-auto"} {
		body := string(byName[name].Content)
		for _, want := range []string{
			"不得带 --confirmed-by-author",
			"「假设」「试用」",
			"--confirmation",
			"standard 或 fast 关卡",
			"confirmedByAuthor 为 false",
			"strict 关卡要求作者确认",
		} {
			if !strings.Contains(body, want) {
				t.Errorf("%s lacks %q", name, want)
			}
		}
		if !strings.Contains(body, "必须停下来问作者") {
			t.Errorf("%s lacks the stop-and-ask section", name)
		}
	}
	revise := string(byName["wa-revise"].Content)
	for _, want := range []string{
		"--reaffirm",
		"--summary <变化摘要>",
		"重申只解除审稿过期",
		"reopen",
		"再请作者重新确认验收",
		"涉及观点、事实或论断时仍走完整审稿",
	} {
		if !strings.Contains(revise, want) {
			t.Errorf("wa-revise lacks %q", want)
		}
	}
	if !slices.Contains(Commands(byName["wa-revise"].Content), "review record") {
		t.Error("wa-revise commands lack review record")
	}
}
