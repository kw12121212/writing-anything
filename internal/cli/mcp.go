package cli

import (
	"bytes"
	"io"
	"io/fs"
	"os"
	"path"
	"path/filepath"
	"slices"
	"sort"
	"strings"
	"time"

	"github.com/kw12121212/writing-anything/internal/draft"
	"github.com/kw12121212/writing-anything/internal/mcp"
	"github.com/kw12121212/writing-anything/internal/schema"
	"github.com/kw12121212/writing-anything/internal/skills"
	"github.com/kw12121212/writing-anything/internal/version"
	"github.com/kw12121212/writing-anything/internal/workspace"
)

// codeMissingDir 表示 wa-cli mcp 没有给出项目根目录。已发布的错误码不得改名或复用。
const codeMissingDir = "mcp.missing-dir"

var mcpCommand = &command{
	name: "mcp",
	summary: "以 stdio 方式运行 MCP 服务（MCP Server，双代服务端（Dual-era Server），支持协议版本 " + strings.Join(mcp.SupportedVersions, "、") + "，旧版客户端以 initialize 握手进入旧版会话（Legacy Session））：tools 由命令注册表生成并调用同一条 wa-cli 命令，" +
		"resources 暴露工作区产物、生效配置与作者反馈（可订阅更新），prompts 对应各技能（Skill）；必须用 --dir 指定项目根目录",
	flags:      []flagSpec{dirFlag, {"interval", flagString, "资源订阅的轮询间隔（毫秒），正整数，默认 1000"}},
	outputKeys: []outputKey{{"mcp", "加 --json 时，标准输入结束、退出前写出的汇总：requests（处理的请求数）；标准输出的其余各行是 MCP 消息"}},
	exitCodes:  standardExitCodes,
	run:        runMCP,
}

type mcpSummary struct {
	Requests int `json:"requests"`
}

type mcpResult struct {
	Result
	MCP *mcpSummary `json:"mcp"`
}

// mcpURIPrefix 是工作区文件资源的 URI 前缀；其后为相对项目目录的路径。
const mcpURIPrefix = "wa://file/"

// 虚拟资源：生效配置与作者反馈，由只读命令生成。
var mcpVirtual = map[string][]string{"wa://resolved": {"config", "resolved"}, "wa://feedback": {"feedback", "list"}}

func runMCP(inv *invocation) int {
	res := &mcpResult{}
	var problems []workspace.Problem
	dir, given := inv.values["dir"]
	if !given || dir == "" {
		problems = []workspace.Problem{{Code: codeMissingDir, Severity: workspace.SeverityError,
			Message: "wa-cli mcp 必须用 --dir 显式指定项目根目录（包含 .writing-anything/ 的目录），MCP 客户端的工作目录不可靠"}}
	} else {
		problems = workspace.Require(dir)
	}
	interval, p := positive(inv, "interval", 1000)
	if problems == nil && p != nil {
		problems = []workspace.Problem{*p}
	}
	if problems != nil {
		res.Diagnostics = diagnostics(problems, projectDir(inv))
		helpNext(res.Diagnostics, "mcp", []string{codeMissingDir, codeInvalidValue})
		return finish(inv, res, func(io.Writer) error { return nil })
	}
	set := inv.all
	run := func(args []string, stdin io.Reader, stdout, stderr io.Writer) int {
		return set.run(args, stdin, stdout, stderr)
	}
	quietRun := func(args []string, stdin io.Reader, stdout, stderr io.Writer) int {
		return set.runQuiet(args, stdin, stdout, stderr)
	}
	v, _ := version.Info()
	cfg := mcp.Config{
		Version:      v,
		Instructions: "wa-cli 帮 Agent 规范地协助作者写作：tool 与 wa-cli 命令一一对应（命令名的空格换成下划线），结果为命令的 JSON 输出信封；先调用 next 查看下一步。订阅 wa://feedback 可以即时收到作者的新反馈。",
		Dir:          dir,
		Tools:        mcpTools(set),
		Prompts:      mcpPrompts(),
		Run:          run,
		List:         func() []mcp.Resource { return mcpResources(dir) },
		Read:         func(uri string) (string, string, bool) { return mcpRead(dir, uri, run) },
		// 订阅轮询用不写审计日志（Audit Log）的运行变体：服务自身发起的轮询不是调用方的动作。
		ReadSub:      func(uri string) (string, string, bool) { return mcpRead(dir, uri, quietRun) },
		Interval:     time.Duration(interval) * time.Millisecond,
	}
	n, err := mcp.Serve(cfg, inv.stdin, inv.stdout)
	if err != nil {
		return writeFailed(inv.stderr, err)
	}
	if inv.json() {
		res.MCP = &mcpSummary{Requests: n}
		if err := writeJSON(inv.stdout, res); err != nil {
			return writeFailed(inv.stderr, err)
		}
	}
	return exitOK
}

// mcpExcluded 是不对应 tool 的命令：mcp 本身、注册客户端的 mcp install（会写项目目录之外的客户端配置）、安装规范包的 pack install
// （来源可以是项目目录之外的本地路径或 file:// 仓库，诊断会带出其中的内容），以及会一直等待作者操作的 feedback watch（用资源订阅
// wa://feedback 代替）。
var mcpExcluded = []string{"mcp", "mcp install", "pack install", "feedback watch"}

// mcpHidden 是不作为 tool 参数的命名参数：--dir 由服务给出；file 与 skills install 的 target 可以指向项目目录之外（读取标准输入的
// 命令改用 input）；preview 的 serve、port 与 max 需要常驻的本地服务。
func mcpHidden(c *command, flag string) bool {
	return flag == dirFlag.Name || flag == "file" || c == skillsInstallCommand && flag == "target" ||
		c == previewCommand && slices.Contains([]string{"serve", "port", "max"}, flag)
}

// mcpTools 从命令注册表生成 tool（mcpExcluded 除外）：位置参数与命名参数（mcpHidden 除外）对应 tool 的参数，读取 --file 或标准输入
// 的命令另有 input；命令接受 --dir 时调用时加上服务的项目根目录。
func mcpTools(set commandSet) []mcp.Tool {
	var out []mcp.Tool
	for _, c := range set {
		if slices.Contains(mcpExcluded, c.name) {
			continue
		}
		t := mcp.Tool{Name: strings.ReplaceAll(c.name, " ", "_"), Description: c.summary, Command: strings.Fields(c.name)}
		for _, a := range c.args {
			t.Params = append(t.Params, mcp.Param{Name: a.Name, Kind: "positional", Required: a.Required, Description: a.Description})
		}
		for _, f := range c.flags {
			if f.Name == dirFlag.Name {
				t.Dir = true
			}
			if f.Name == "file" {
				t.Params = append(t.Params, mcp.Param{Name: "input", Kind: "input", Description: "作为标准输入交给命令的内容（代替 --file）"})
			}
			if mcpHidden(c, f.Name) {
				continue
			}
			kind := map[string]string{flagBool: "bool", flagString: "string", flagList: "list"}[f.Type]
			t.Params = append(t.Params, mcp.Param{Name: f.Name, Kind: kind, Description: f.Description})
		}
		out = append(out, t)
	}
	return out
}

// mcpPrompts 为每个内置技能返回一个 prompt，内容为技能正文（去掉文件头）。
func mcpPrompts() []mcp.Prompt {
	var out []mcp.Prompt
	for _, s := range skills.All() {
		body := s.Content
		if _, rest, ok := bytes.Cut(bytes.TrimPrefix(body, []byte("---\n")), []byte("\n---\n")); ok {
			body = rest
		}
		out = append(out, mcp.Prompt{Name: s.Name, Description: s.Description, Text: strings.TrimSpace(string(body))})
	}
	return out
}

// mcpReadable 报告工作区中的路径 rel 能否作为资源：由 Schema 登记了产物类型的文件，或作品中单元目录下的 draft.md。
func mcpReadable(rel string) bool {
	if path.Clean(rel) != rel || !strings.HasPrefix(rel, workspace.Dir+"/") {
		return false
	}
	return schema.KindFor(rel) != nil || path.Base(rel) == draft.FileName && strings.HasPrefix(rel, workspace.WorksPath+"/")
}

// workspaceDirReal 报告工作区目录 .writing-anything 本身是真实目录（不是符号链接）；列出与读取资源都要求这一点。
func workspaceDirReal(dir string) bool {
	info, err := os.Lstat(filepath.Join(dir, workspace.Dir))
	return err == nil && info.IsDir()
}

// mcpResources 列出工作区中可读取的产物（不跟随符号链接，按路径排序）与虚拟资源。
func mcpResources(dir string) []mcp.Resource {
	var rels []string
	walk := filepath.WalkDir
	if !workspaceDirReal(dir) {
		walk = func(string, fs.WalkDirFunc) error { return nil }
	}
	walk(filepath.Join(dir, workspace.Dir), func(p string, d fs.DirEntry, err error) error {
		if err != nil || d.Type()&os.ModeSymlink != 0 {
			return nil
		}
		rel, _ := filepath.Rel(dir, p)
		rel = filepath.ToSlash(rel)
		if d.Type().IsRegular() && mcpReadable(rel) {
			rels = append(rels, rel)
		}
		return nil
	})
	sort.Strings(rels)
	out := []mcp.Resource{{URI: "wa://resolved", Name: "生效配置（含规范包）", MimeType: "application/json"},
		{URI: "wa://feedback", Name: "作者反馈", MimeType: "application/json"}}
	for _, rel := range rels {
		out = append(out, mcp.Resource{URI: mcpURIPrefix + rel, Name: rel, MimeType: mimeOf(rel)})
	}
	return out
}

func mimeOf(rel string) string {
	if strings.HasSuffix(rel, ".md") {
		return "text/markdown"
	}
	return "application/yaml"
}

// mcpRead 读取资源：工作区文件经 workspace.ReadRegular（途经的每一级都必须是真实目录），虚拟资源运行对应的只读命令。
func mcpRead(dir, uri string, run func([]string, io.Reader, io.Writer, io.Writer) int) (string, string, bool) {
	if args, ok := mcpVirtual[uri]; ok {
		var stdout bytes.Buffer
		run(slices.Concat(args, []string{"--dir", dir, "--json"}), strings.NewReader(""), &stdout, io.Discard)
		return stdout.String(), "application/json", true
	}
	rel, ok := strings.CutPrefix(uri, mcpURIPrefix)
	if !ok || !mcpReadable(rel) || !workspaceDirReal(dir) {
		return "", "", false
	}
	data, ok, err := workspace.ReadRegular(dir, rel)
	if err != nil || !ok {
		return "", "", false
	}
	return string(data), mimeOf(rel), true
}
