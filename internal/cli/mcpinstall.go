package cli

import (
	"fmt"
	"io"
	"os"
	"os/exec"
	"path/filepath"
	"slices"
	"strings"

	"github.com/kw12121212/writing-anything/internal/brief"
	"github.com/kw12121212/writing-anything/internal/mcpinstall"
	"github.com/kw12121212/writing-anything/internal/workspace"
)

var mcpInstallCommand = &command{
	name: "mcp install",
	summary: "把 wa-cli 的 MCP 服务（MCP Server）注册到 MCP 客户端：claude-code 与 codex 调用各自的官方 CLI，json 写入 --config 指定的 mcpServers 配置文件；" +
		"重复执行结果相同，保留客户端已有的其他条目",
	flags: []flagSpec{dirFlag,
		{"client", flagList, "要注册的客户端：" + strings.Join(mcpinstall.Clients, "、") + "；可重复"},
		{"scope", flagString, "作用域：project（项目级，默认）或 user（用户级）；codex 只支持 user"},
		{"name", flagString, "注册的名称，默认 writing-anything"},
		{"config", flagString, "json 客户端写入的配置文件，相对 --dir 或为绝对路径"},
	},
	outputKeys: []outputKey{{"installs", "每个客户端的注册结果：client、scope、name、command、args、status（registered、updated、unchanged、skipped 或 failed）、path（json 的配置文件，其他为 null）与 message"}},
	exitCodes:  standardExitCodes,
	run:        runMCPInstall,
}

type installView struct {
	Client  string   `json:"client"`
	Scope   string   `json:"scope"`
	Name    string   `json:"name"`
	Command string   `json:"command"`
	Args    []string `json:"args"`
	Status  string   `json:"status"`
	Path    *string  `json:"path"`
	Message string   `json:"message"`
}

type mcpInstallResult struct {
	Result
	Installs []installView `json:"installs"`
}

// mcpInstallEnv 与 mcpExecutable 是注册时与外部环境的交互，测试可以替换。
var (
	mcpInstallEnv = mcpinstall.System
	mcpExecutable = stableExecutable
)

// stableExecutable 返回要注册的 wa-cli 路径：PATH 中的 wa-cli 与当前可执行文件是同一个文件时用它（如包管理器的链接，升级后仍然
// 有效），否则用当前可执行文件的绝对路径。
func stableExecutable() (string, error) {
	exe, err := os.Executable()
	if err != nil {
		return "", err
	}
	if p, err := exec.LookPath("wa-cli"); err == nil {
		if abs, err := filepath.Abs(p); err == nil && sameFile(abs, exe) {
			return abs, nil
		}
	}
	return exe, nil
}

func sameFile(a, b string) bool {
	x, err1 := os.Stat(a)
	y, err2 := os.Stat(b)
	return err1 == nil && err2 == nil && os.SameFile(x, y)
}

// insideWorkspace 报告 path 是否落在项目目录 dir 的工作区 .writing-anything/ 之中：按文件身份比较 path 途经的每一级已存在的目录
// 与工作区目录，符号链接与大小写不同的写法都不能绕过（workspace-artifacts/validated-path-segments）。
func insideWorkspace(dir, path string) bool {
	ws, err := os.Stat(filepath.Join(dir, workspace.Dir))
	if err != nil {
		return false
	}
	for p := filepath.Dir(path); ; p = filepath.Dir(p) {
		if info, err := os.Stat(p); err == nil && os.SameFile(info, ws) {
			return true
		}
		if parent := filepath.Dir(p); parent == p {
			return false
		}
	}
}

func runMCPInstall(inv *invocation) int {
	dir := projectDir(inv)
	res := &mcpInstallResult{Installs: []installView{}}
	scope := inv.values["scope"]
	if scope == "" {
		scope = mcpinstall.ScopeProject
	}
	name := inv.values["name"]
	if name == "" {
		name = "writing-anything"
	}
	var clients []string
	for _, c := range inv.lists["client"] {
		if !slices.Contains(clients, c) {
			clients = append(clients, c) // 重复的客户端只注册一次
		}
	}
	var problems []workspace.Problem
	switch {
	case len(clients) == 0:
		problems = []workspace.Problem{invalidValue("client", "", "至少给出一个 --client："+strings.Join(mcpinstall.Clients, "、"))}
	case !slices.Contains(mcpinstall.Scopes, scope):
		problems = []workspace.Problem{invalidValue("scope", scope, "应为 project 或 user")}
	case !brief.ValidID(name):
		problems = []workspace.Problem{invalidValue("name", name, "应以小写字母开头，由小写字母、数字与连字符组成")}
	case slices.Contains(clients, mcpinstall.ClientJSON) && inv.values["config"] == "":
		problems = []workspace.Problem{invalidValue("config", "", "--client json 需要用 --config 指定要写入的配置文件")}
	}
	for _, c := range clients {
		if problems == nil && !slices.Contains(mcpinstall.Clients, c) {
			problems = []workspace.Problem{invalidValue("client", c, "应为 "+strings.Join(mcpinstall.Clients, "、")+" 之一")}
		}
	}
	absDir, err := filepath.Abs(dir)
	if info, statErr := os.Stat(absDir); problems == nil && (err != nil || statErr != nil || !info.IsDir()) {
		p := invalidValue("dir", dir, "项目目录必须是已存在的目录")
		p.Field = "dir"
		problems = []workspace.Problem{p}
	}
	exe, exeErr := mcpExecutable()
	if problems == nil && exeErr != nil {
		problems = []workspace.Problem{{Code: workspace.CodeReadFailed, Severity: workspace.SeverityError, Message: fmt.Sprintf("无法确定 wa-cli 的绝对路径：%v", exeErr)}}
	}
	config := inv.values["config"]
	if problems == nil && config != "" {
		if !filepath.IsAbs(config) {
			config = filepath.Join(absDir, config)
		}
		rel, err := filepath.Rel(absDir, config)
		if err == nil && strings.EqualFold(strings.Split(filepath.ToSlash(rel), "/")[0], workspace.Dir) || insideWorkspace(absDir, config) {
			problems = []workspace.Problem{invalidValue("config", inv.values["config"], "配置文件不能在工作区 "+workspace.Dir+"/ 中")}
		}
	}
	if problems == nil {
		env := mcpInstallEnv()
		for _, c := range clients {
			r := mcpinstall.Request{Client: c, Scope: scope, Name: name, Dir: absDir, Command: exe}
			if c == mcpinstall.ClientJSON {
				r.Config = config
			}
			out, ps := mcpinstall.Install(env, r)
			v := installView{Client: c, Scope: scope, Name: name, Command: exe, Args: r.Args(), Status: out.Status, Message: out.Message}
			// 诊断的 path 与 changedPaths 只列项目目录之内的路径（workspace-artifacts/workspace-relative-paths）；
			// 项目之外的配置文件（如用户级配置）只在 installs 的 path 中给出。
			if out.Path != "" {
				shown, inside := shownPath(absDir, out.Path)
				v.Path = &shown
				for i := range ps {
					ps[i].Path = ""
					if inside {
						ps[i].Path = shown
					}
				}
				if out.Changed != "" && inside {
					res.ChangedPaths = append(res.ChangedPaths, shown)
				}
			}
			res.Installs = append(res.Installs, v)
			problems = append(problems, ps...)
		}
	}
	res.Diagnostics = diagnostics(problems, dir)
	// 诊断与 installs 按客户端顺序一一对应（参数问题时没有 installs）。
	k := 0
	for i, d := range res.Diagnostics {
		client := ""
		if k < len(res.Installs) && d.Code != codeInvalidValue {
			for k < len(res.Installs) && res.Installs[k].Status != mcpinstall.StatusSkipped && res.Installs[k].Status != mcpinstall.StatusFailed {
				k++
			}
			if k < len(res.Installs) {
				client = res.Installs[k].Client
				k++
			}
		}
		install := func(extra ...string) string {
			return shellCommand(append(append([]string{"wa-cli", "mcp", "install"}, extra...), "--name", name, "--dir", dir, "--json")...)
		}
		switch {
		case d.Code == mcpinstall.CodeUnsupportedScope:
			res.Diagnostics[i].Next = []string{install("--client", "codex", "--scope", "user")}
		case d.Code == mcpinstall.CodeClientNotFound || d.Code == mcpinstall.CodeClientFailed:
			res.Diagnostics[i].Next = []string{"wa-cli help mcp install --json", install("--client", client, "--scope", scope)}
			if client == mcpinstall.ClientClaudeCode && scope == mcpinstall.ScopeProject {
				// Claude Code 也读取项目目录下的 .mcp.json，可以不经官方 CLI 直接写入。
				res.Diagnostics[i].Next = append(res.Diagnostics[i].Next, install("--client", "json", "--config", ".mcp.json"))
			}
		case d.Code == codeInvalidValue || d.Code == mcpinstall.CodeInvalidConfig || d.Code == workspace.CodeWriteFailed:
			res.Diagnostics[i].Next = []string{"wa-cli help mcp install --json"}
		}
	}
	return finish(inv, res, func(w io.Writer) error {
		var b strings.Builder
		for _, v := range res.Installs {
			fmt.Fprintf(&b, "%s（%s）：%s——%s\n", v.Client, v.Scope, v.Status, v.Message)
		}
		_, err := io.WriteString(w, b.String())
		return err
	})
}

// shownPath 返回配置文件在输出中的路径：在项目目录之内时为相对路径（以 / 分隔，inside 为 true），否则为绝对路径。
func shownPath(dir, path string) (string, bool) {
	if rel, err := filepath.Rel(dir, path); err == nil && rel != ".." && !strings.HasPrefix(rel, ".."+string(filepath.Separator)) {
		return filepath.ToSlash(rel), true
	}
	return path, false
}
