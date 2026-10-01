package cli

import (
	"fmt"
	"io"
	"path/filepath"
	"slices"
	"strings"

	"github.com/kw12121212/writing-anything/internal/skills"
	"github.com/kw12121212/writing-anything/internal/workspace"
)

var skillsListCommand = &command{
	name:       "skills list",
	summary:    "列出随 wa-cli 提供的技能（Skill）：每个技能说明何时调用哪些 wa-cli 命令、何时停下来询问作者",
	outputKeys: []outputKey{{"skills", "按名称排序的技能数组，每项含 name、description 与 commands（引用的 wa-cli 命令）"}},
	exitCodes:  standardExitCodes,
	run:        runSkillsList,
}

var skillsInstallCommand = &command{
	name: "skills install",
	summary: "把内置技能（Skill）安装到 Agent 的技能目录：每个技能写入 <目录>/<技能名>/SKILL.md，格式与通用的 Agent Skills 兼容；" +
		"不需要工作区",
	flags: []flagSpec{dirFlag,
		{"agent", flagString, "Agent：" + strings.Join(skills.AgentNames(), "、") + "；分别安装到项目目录下的 .claude/skills 与 .agents/skills"},
		{"target", flagString, "技能目录，相对 --dir 或为绝对路径（如 ~/.claude/skills 的绝对路径）；给出时不需要 --agent"},
		{"force", flagBool, "覆盖内容与内置版本不同（可能被改过）的已有技能文件"}},
	outputKeys: []outputKey{{"skills", "安装结果数组，每项含 name、path（在项目目录之内时相对项目目录，否则为绝对路径）与 changed；检查不通过时为空数组、不写任何文件，写入中途失败时只列出已写入的技能"}},
	exitCodes:  standardExitCodes,
	run:        runSkillsInstall,
}

type skillView struct {
	Name        string   `json:"name"`
	Description string   `json:"description"`
	Commands    []string `json:"commands"`
}

type skillsListResult struct {
	Result
	Skills []skillView `json:"skills"`
}

type installedView struct {
	Name    string `json:"name"`
	Path    string `json:"path"`
	Changed bool   `json:"changed"`
}

type skillsInstallResult struct {
	Result
	Skills []installedView `json:"skills"`
}

func runSkillsList(inv *invocation) int {
	res := &skillsListResult{Skills: []skillView{}}
	for _, s := range skills.All() {
		res.Skills = append(res.Skills, skillView{Name: s.Name, Description: s.Description, Commands: skillCommands(inv.all, s.Content)})
	}
	return finish(inv, res, func(w io.Writer) error {
		var b strings.Builder
		for _, s := range res.Skills {
			fmt.Fprintf(&b, "%s：%s\n", s.Name, s.Description)
		}
		_, err := io.WriteString(w, b.String())
		return err
	})
}

func runSkillsInstall(inv *invocation) int {
	dir := projectDir(inv)
	res := &skillsInstallResult{Skills: []installedView{}}
	agent, target := inv.values["agent"], inv.values["target"]
	_, agentGiven := inv.values["agent"]
	var problems []workspace.Problem
	switch {
	case agentGiven && skills.Agents[agent] == "":
		problems = []workspace.Problem{invalidValue("agent", agent, "支持的 Agent 为 "+strings.Join(skills.AgentNames(), "、")+"；其他 Agent 用 --target 指定技能目录")}
	case target == "" && !agentGiven:
		problems = []workspace.Problem{{Code: codeInvalidValue, Severity: workspace.SeverityError, Field: "agent",
			Message: "需要 --agent（" + strings.Join(skills.AgentNames(), "、") + "）或 --target 指明技能目录"}}
	case target == "":
		target = skills.Agents[agent]
	}
	if problems == nil && skills.InWorkspace(dir, target) {
		problems = []workspace.Problem{invalidValue("target", target, "技能目录不能在工作区 "+workspace.Dir+"/ 中")}
	}
	if problems == nil {
		var r *skills.Result
		if r, problems = skills.Install(dir, target, inv.flag("force")); r != nil {
			res.ChangedPaths = append(res.ChangedPaths, r.Created...)
			for _, s := range r.Skills {
				res.Skills = append(res.Skills, installedView(s))
				if s.Changed && !filepath.IsAbs(s.Path) { // 项目目录之外的技能文件只在 skills 中以绝对路径列出
					res.ChangedPaths = append(res.ChangedPaths, s.Path)
				}
			}
		}
	}
	res.Diagnostics = diagnostics(problems, dir)
	for i := range res.Diagnostics {
		// 技能不是工作区产物，validate 检不出这些问题；重新运行本命令复查。
		res.Diagnostics[i].Next = []string{"wa-cli help skills install --json"}
	}
	return finish(inv, res, func(w io.Writer) error {
		changed := 0
		for _, s := range res.Skills {
			if s.Changed {
				changed++
			}
		}
		_, err := fmt.Fprintf(w, "已安装 %d 个技能（Skill），%d 个有变化\n", len(res.Skills), changed)
		return err
	})
}

// skillCommands 返回技能引用的命令，按命令注册表规范化：两个词组成已登记的命令时取两个词，否则取第一个词（之后是实参）。
func skillCommands(all commandSet, content []byte) []string {
	out := []string{}
	for _, c := range skills.Commands(content) {
		first, _, two := strings.Cut(c, " ")
		if two && all.lookup(c) == nil {
			c = first
		}
		if !slices.Contains(out, c) {
			out = append(out, c)
		}
	}
	return out
}
