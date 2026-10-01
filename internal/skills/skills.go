// Package skills 提供随 wa-cli 二进制分发的技能（Skill）：每个技能是一个目录，含 SKILL.md（YAML 文件头含 name 与
// description，正文说明何时调用哪些 wa-cli 命令、何时必须停下来询问作者），目录结构与通用的 Agent Skills 格式兼容。
// 技能只做薄封装，不实现业务逻辑；本包只返回结构化的问题，由命令层转换为诊断（Diagnostic）。
package skills

import (
	"bytes"
	"embed"
	"errors"
	"fmt"
	"io/fs"
	"os"
	"path/filepath"
	"regexp"
	"sort"
	"strings"

	"github.com/kw12121212/writing-anything/internal/workspace"
	"go.yaml.in/yaml/v3"
)

// 技能的错误码（Error Code）。已发布的错误码不得改名或复用。
const (
	CodeModified    = "skills.modified"
	CodeInvalidPath = "skills.invalid-path"
)

// FileName 是技能目录中的技能文件名。
const FileName = "SKILL.md"

//go:embed skills
var embedded embed.FS

// Agents 是支持的 Agent 与它们在项目目录中的技能目录。
var Agents = map[string]string{
	"claude-code": ".claude/skills",
	"codex":       ".agents/skills", // Codex 在仓库中扫描 .agents/skills
}

// AgentNames 返回支持的 Agent 名，按字母顺序。
func AgentNames() []string {
	var out []string
	for k := range Agents {
		out = append(out, k)
	}
	sort.Strings(out)
	return out
}

// Skill 是一个内置技能。
type Skill struct {
	Name        string
	Description string
	Content     []byte // SKILL.md 的完整内容
}

// nameRE 是技能名的格式：小写字母、数字与连字符，与 Agent Skills 格式一致。
var nameRE = regexp.MustCompile(`^[a-z][a-z0-9]*(-[a-z0-9]+)*$`)

// Parse 解析 SKILL.md：开头为 --- 包住的 YAML 文件头，含 name 与 description。返回技能与格式问题的说明（没有问题时为空）。
func Parse(dir string, content []byte) (*Skill, string) {
	rest, ok := bytes.CutPrefix(content, []byte("---\n"))
	if !ok {
		return nil, "SKILL.md 必须以 --- 开头的 YAML 文件头开始"
	}
	head, body, ok := bytes.Cut(rest, []byte("\n---\n"))
	if !ok {
		return nil, "SKILL.md 的 YAML 文件头没有以 --- 结束"
	}
	var meta struct {
		Name        string `yaml:"name"`
		Description string `yaml:"description"`
	}
	dec := yaml.NewDecoder(bytes.NewReader(head))
	dec.KnownFields(true)
	if err := dec.Decode(&meta); err != nil {
		return nil, "SKILL.md 的文件头不是合法的 YAML（只能含 name 与 description）：" + err.Error()
	}
	switch {
	case !nameRE.MatchString(meta.Name) || len(meta.Name) > 64:
		return nil, fmt.Sprintf("name %q 不合法：应为小写字母、数字与连字符，不超过 64 个字符", meta.Name)
	case meta.Name != dir:
		return nil, fmt.Sprintf("name %s 与目录名 %s 不一致", meta.Name, dir)
	case strings.TrimSpace(meta.Description) == "" || len([]rune(meta.Description)) > 1024:
		return nil, "description 不能为空，且不超过 1024 个字符"
	case len(bytes.TrimSpace(body)) == 0:
		return nil, "SKILL.md 的正文为空"
	}
	return &Skill{Name: meta.Name, Description: meta.Description, Content: content}, ""
}

// All 返回全部内置技能，按名称排序。内置技能的格式由测试保证；格式不对的技能会使测试失败。
func All() []Skill {
	entries, _ := embedded.ReadDir("skills")
	var out []Skill
	for _, e := range entries {
		data, err := embedded.ReadFile("skills/" + e.Name() + "/" + FileName)
		if err != nil {
			continue
		}
		if s, _ := Parse(e.Name(), data); s != nil {
			out = append(out, *s)
		}
	}
	return out
}

// Installed 是一个已安装（或取值未变）的技能文件。
type Installed struct {
	Name    string
	Path    string // 技能文件路径：在项目目录之内时相对项目目录，否则为绝对路径
	Changed bool
}

// Result 是一次安装的结果：Skills 为每个技能的结果，Created 为新建的目录，两者在项目目录之内的路径相对项目目录。
type Result struct {
	Skills  []Installed
	Created []string
}

// locator 把绝对路径转换为输出用的路径：在项目目录 root 之内时为相对 root 的路径（inside 为 true），否则为绝对路径。
type locator struct{ root string }

func (l locator) show(p string) (string, bool) {
	rel, err := filepath.Rel(l.root, p)
	if err != nil || rel == ".." || strings.HasPrefix(rel, ".."+string(filepath.Separator)) {
		return p, false
	}
	return filepath.ToSlash(rel), true
}

// problem 生成问题：路径在项目目录之内时填入 Path（相对项目目录），否则不填 Path、把绝对路径写进 Message
// （cli-contract/workspace-relative-paths）。
func (l locator) problem(code, p, msg string) workspace.Problem {
	shown, inside := l.show(p)
	if inside {
		return workspace.Problem{Code: code, Severity: workspace.SeverityError, Path: shown, Message: msg}
	}
	return workspace.Problem{Code: code, Severity: workspace.SeverityError, Message: shown + "：" + msg}
}

// InWorkspace 报告技能目录 target（相对项目目录 root 或绝对路径）是否落在工作区 .writing-anything/ 之中：先把两者解析为绝对
// 路径并按不区分大小写比较（macOS 与 Windows 的默认文件系统不区分大小写），再从 target 向上逐级用文件身份（os.SameFile）
// 与工作区目录比较，使绝对路径、经由上级目录的相对路径与大小写变体都不能绕过。
func InWorkspace(root, target string) bool {
	rootAbs, err := filepath.Abs(root)
	if err != nil {
		return false
	}
	base := target
	if !filepath.IsAbs(base) {
		base = filepath.Join(rootAbs, filepath.FromSlash(target))
	}
	base = filepath.Clean(base)
	ws := filepath.Join(rootAbs, workspace.Dir)
	if rel, err := filepath.Rel(strings.ToLower(ws), strings.ToLower(base)); err == nil && rel != ".." && !strings.HasPrefix(rel, ".."+string(filepath.Separator)) {
		return true
	}
	wsInfo, err := os.Lstat(ws)
	if err != nil {
		return false
	}
	for p := base; ; p = filepath.Dir(p) {
		if info, err := os.Lstat(p); err == nil && os.SameFile(info, wsInfo) {
			return true
		}
		if filepath.Dir(p) == p {
			return false
		}
	}
}

// plan 是一个技能的安装计划。
type plan struct {
	skill Skill
	dir   string // 技能目录（绝对路径）
	file  string // SKILL.md（绝对路径）
	write bool   // 需要写入
}

// Install 把全部内置技能写入技能目录 target 下的 <name>/SKILL.md：target 为相对项目目录 root 的路径或绝对路径。先完成全部
// 检查，任何一项不通过都不写任何文件（workspace-artifacts/precheck-before-side-effects）：从技能目录向上第一个已存在的路径、
// 已存在的技能目录都必须是真实目录，已存在的技能文件必须是普通文件（不跟随符号链接）；技能文件与内置内容不同（可能被改过）时
// 只有 force 为 true 才覆盖，否则报告 skills.modified。然后创建缺少的目录并原子写入。
func Install(root, target string, force bool) (*Result, []workspace.Problem) {
	rootAbs, err := filepath.Abs(root)
	if err != nil {
		return nil, []workspace.Problem{{Code: workspace.CodeReadFailed, Severity: workspace.SeverityError, Message: "无法确定项目目录：" + err.Error()}}
	}
	l := locator{rootAbs}
	base := target
	if !filepath.IsAbs(base) {
		base = filepath.Join(rootAbs, filepath.FromSlash(target))
	}
	base = filepath.Clean(base)
	var problems []workspace.Problem
	// 技能目录：向上找到第一个已存在的路径，它必须是真实目录；其下缺少的目录在写入时创建。
	var missing []string
	for p := base; ; p = filepath.Dir(p) {
		info, err := os.Lstat(p)
		if err == nil {
			if !info.IsDir() {
				return nil, []workspace.Problem{l.problem(CodeInvalidPath, p, "不是真实目录（如符号链接），wa-cli 不会在其中写入技能")}
			}
			break
		}
		if !errors.Is(err, fs.ErrNotExist) {
			return nil, []workspace.Problem{l.problem(workspace.CodeReadFailed, p, "读取失败："+err.Error())}
		}
		missing = append(missing, p)
		if filepath.Dir(p) == p {
			break
		}
	}
	var plans []plan
	for _, s := range All() {
		pl := plan{skill: s, dir: filepath.Join(base, s.Name)}
		pl.file = filepath.Join(pl.dir, FileName)
		if info, err := os.Lstat(pl.dir); err == nil && !info.IsDir() {
			problems = append(problems, l.problem(CodeInvalidPath, pl.dir, "不是真实目录（如符号链接），wa-cli 不会在其中写入技能"))
			continue
		} else if err != nil && !errors.Is(err, fs.ErrNotExist) {
			problems = append(problems, l.problem(workspace.CodeReadFailed, pl.dir, "读取失败："+err.Error()))
			continue
		}
		info, err := os.Lstat(pl.file)
		switch {
		case errors.Is(err, fs.ErrNotExist):
			pl.write = true
		case err != nil:
			problems = append(problems, l.problem(workspace.CodeReadFailed, pl.file, "读取失败："+err.Error()))
			continue
		case !info.Mode().IsRegular():
			problems = append(problems, l.problem(CodeInvalidPath, pl.file, "不是普通文件（如符号链接），wa-cli 不会写入它"))
			continue
		default:
			cur, err := os.ReadFile(pl.file)
			switch {
			case err != nil:
				problems = append(problems, l.problem(workspace.CodeReadFailed, pl.file, "读取失败："+err.Error()))
				continue
			case bytes.Equal(cur, s.Content):
			case force:
				pl.write = true
			default:
				problems = append(problems, l.problem(CodeModified, pl.file, "技能 "+s.Name+" 已存在且内容与内置版本不同（可能被改过）；确认后加 --force 覆盖"))
				continue
			}
		}
		plans = append(plans, pl)
	}
	if len(problems) > 0 {
		return nil, problems
	}
	res := &Result{}
	created := func(p string) {
		if shown, inside := l.show(p); inside {
			res.Created = append(res.Created, shown)
		}
	}
	for i := len(missing) - 1; i >= 0; i-- {
		if err := os.Mkdir(missing[i], 0o755); err != nil && !errors.Is(err, fs.ErrExist) {
			return res, []workspace.Problem{l.problem(workspace.CodeWriteFailed, missing[i], "创建目录失败："+err.Error())}
		}
		created(missing[i])
	}
	for _, pl := range plans {
		if _, err := os.Lstat(pl.dir); errors.Is(err, fs.ErrNotExist) {
			if err := os.Mkdir(pl.dir, 0o755); err != nil {
				return res, []workspace.Problem{l.problem(workspace.CodeWriteFailed, pl.dir, "创建目录失败："+err.Error())}
			}
			created(pl.dir)
		}
		if pl.write {
			if err := workspace.WriteFile(pl.dir, FileName, pl.skill.Content); err != nil {
				return res, []workspace.Problem{l.problem(workspace.CodeWriteFailed, pl.file, "写入技能失败："+err.Error())}
			}
		}
		shown, _ := l.show(pl.file)
		res.Skills = append(res.Skills, Installed{pl.skill.Name, shown, pl.write})
	}
	return res, nil
}

// Commands 返回技能正文中引用的 wa-cli 命令（wa-cli 之后的一或两个小写词），用于校验它们都存在于命令注册表中。
func Commands(content []byte) []string {
	seen := map[string]bool{}
	var out []string
	for _, m := range commandRE.FindAllSubmatch(content, -1) {
		c := string(m[1])
		if !seen[c] {
			seen[c] = true
			out = append(out, c)
		}
	}
	return out
}

// commandRE 匹配技能中的 wa-cli 命令：命令名为一个或两个由空格分隔的小写词（如 brief set）。
var commandRE = regexp.MustCompile("wa-cli ([a-z][a-z-]*(?: [a-z][a-z-]*)?)")
