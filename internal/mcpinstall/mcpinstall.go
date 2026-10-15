// Package mcpinstall 把 wa-cli 的 MCP 服务（MCP Server）注册到 MCP 客户端。每个客户端的注册逻辑相互隔离：claude-code 与 codex
// 调用各自的官方 CLI，json 写入通用的 mcpServers 配置文件。外部命令的查找与执行经 Env 注入，测试可以替换；本包只返回结构化的
// 结果与问题，由命令层转换为输出与诊断（Diagnostic）。
package mcpinstall

import (
	"bytes"
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"io/fs"
	"os"
	"os/exec"
	"path/filepath"
	"reflect"
	"strings"
	"time"

	"github.com/kw12121212/writing-anything/internal/workspace"
)

// 注册的错误码（Error Code）。已发布的错误码不得改名或复用。
const (
	CodeClientNotFound   = "mcp.client-not-found"
	CodeClientFailed     = "mcp.client-failed"
	CodeUnsupportedScope = "mcp.unsupported-scope"
	CodeInvalidConfig    = "mcp.invalid-config"
)

// 客户端与作用域。
const (
	ClientClaudeCode = "claude-code"
	ClientCodex      = "codex"
	ClientJSON       = "json"
	ScopeProject     = "project"
	ScopeUser        = "user"
)

// Clients 与 Scopes 是可用的取值。
var (
	Clients = []string{ClientClaudeCode, ClientCodex, ClientJSON}
	Scopes  = []string{ScopeProject, ScopeUser}
)

// 结果的状态。
const (
	StatusRegistered = "registered"
	StatusUpdated    = "updated"
	StatusUnchanged  = "unchanged"
	StatusSkipped    = "skipped"
	StatusFailed     = "failed"
)

// Env 是注册时与外部环境的交互：查找可执行文件，在目录 dir 中运行命令（返回合并的标准输出与标准错误，以及错误）。
type Env struct {
	LookPath func(name string) (string, error)
	Run      func(dir, name string, args ...string) (output string, err error)
}

// Timeout 是一次外部命令的时长上限；官方 CLI 卡住（如首次运行的引导、自动更新）时不让 wa-cli 一直等待。
const Timeout = 2 * time.Minute

// WaitDelay 是超时杀掉外部命令后等待输出管道关闭的上限：它派生的子进程仍持有管道时，不让 Wait 一直等下去。
const WaitDelay = 5 * time.Second

// timeout 是 System 使用的时长上限，测试可以缩短。
var timeout = Timeout

// maxOutput 是诊断中保留的外部命令输出的字节数上限。
const maxOutput = 4 << 10

// System 返回使用真实 PATH 与进程的 Env：参数不经 shell 传递，不连接 wa-cli 的标准输入与输出，有时长上限。
func System() Env {
	return Env{
		LookPath: exec.LookPath,
		Run: func(dir, name string, args ...string) (string, error) {
			ctx, cancel := context.WithTimeout(context.Background(), timeout)
			defer cancel()
			cmd := exec.CommandContext(ctx, name, args...)
			cmd.Dir, cmd.WaitDelay = dir, WaitDelay
			var out bytes.Buffer
			cmd.Stdout, cmd.Stderr = &out, &out
			err := cmd.Run()
			if ctx.Err() != nil {
				err = fmt.Errorf("超过 %v 没有结束", timeout)
			}
			text := out.String()
			if len(text) > maxOutput {
				text = text[:maxOutput] + "…"
			}
			return text, err
		},
	}
}

// Request 是一次注册：Dir 为项目目录的绝对路径；Command 为 wa-cli 可执行文件的绝对路径；Config 为 json 的配置文件（绝对路径）。
type Request struct {
	Client, Scope, Name string
	Dir, Command        string
	Config              string
}

// Args 返回注册的服务参数：mcp --dir <项目目录>。
func (r Request) Args() []string { return []string{"mcp", "--dir", r.Dir} }

// Result 是一个客户端的注册结果。Changed 为写入的文件（只有 json）；Path 为该客户端的配置文件（json 的配置文件、claude-code
// 项目级的 .mcp.json），没有时为空。
type Result struct {
	Status  string
	Message string
	Changed string
	Path    string
}

// Install 按 r.Client 注册；problems 为该客户端的问题（找不到官方 CLI 时为 warning，其他为 error）。
func Install(env Env, r Request) (Result, []workspace.Problem) {
	switch r.Client {
	case ClientClaudeCode:
		return claudeCode(env, r)
	case ClientCodex:
		return codex(env, r)
	default:
		return jsonConfig(r)
	}
}

func problem(code, severity, path, msg string) workspace.Problem {
	return workspace.Problem{Code: code, Severity: severity, Path: path, Message: msg}
}

// official 用官方 CLI 注册：先移除同名条目（失败忽略），再添加；找不到 CLI 时跳过。
func official(env Env, r Request, bin string, remove, add []string) (Result, []workspace.Problem) {
	path, err := env.LookPath(bin)
	if err != nil {
		msg := "找不到 " + bin + " 命令，跳过 " + r.Client + "；请先安装它，或用 --client json 写入配置文件"
		return Result{Status: StatusSkipped, Message: msg}, []workspace.Problem{problem(CodeClientNotFound, workspace.SeverityWarning, "", msg)}
	}
	_, removeErr := env.Run(r.Dir, path, remove...) // 没有同名条目时移除会失败，忽略
	if output, err := env.Run(r.Dir, path, add...); err != nil {
		msg := bin + " " + strings.Join(add[:2], " ") + " 失败：" + err.Error()
		if out := strings.TrimSpace(output); out != "" {
			msg += "；" + out
		}
		if removeErr == nil {
			msg += "；已有的同名条目 " + r.Name + " 已被移除，修复后请重新运行 wa-cli mcp install"
		}
		return Result{Status: StatusFailed, Message: msg}, []workspace.Problem{problem(CodeClientFailed, workspace.SeverityError, "", msg)}
	}
	msg := "已用 " + bin + " 注册 " + r.Name
	if removeErr == nil {
		msg += "（同名条目重新创建）"
	}
	return Result{Status: StatusRegistered, Message: msg}, nil
}

// claudeCode 用 claude mcp add 注册；project 作用域写入项目目录下的 .mcp.json，user 作用域写入用户配置。
func claudeCode(env Env, r Request) (Result, []workspace.Problem) {
	res, ps := official(env, r, "claude",
		[]string{"mcp", "remove", "--scope", r.Scope, r.Name},
		append([]string{"mcp", "add", "--scope", r.Scope, r.Name, "--", r.Command}, r.Args()...))
	if r.Scope == ScopeProject {
		res.Path = filepath.Join(r.Dir, ".mcp.json")
		if res.Status == StatusRegistered {
			res.Message += "；在项目目录 " + r.Dir + " 中启动 Claude Code 才会加载；项目级配置 .mcp.json 中的路径是本机的绝对路径，其他人使用前需要按自己的环境重新注册"
		}
	} else if res.Status == StatusRegistered {
		res.Message += userScopeHint(r)
	}
	return res, ps
}

// codex 用 codex mcp add 注册；codex 的官方 CLI 只写用户级配置。
func codex(env Env, r Request) (Result, []workspace.Problem) {
	if r.Scope != ScopeUser {
		msg := "codex 的官方 CLI 只能注册到用户级配置；请用 --scope user"
		return Result{Status: StatusFailed, Message: msg}, []workspace.Problem{problem(CodeUnsupportedScope, workspace.SeverityError, "", msg)}
	}
	res, ps := official(env, r, "codex", []string{"mcp", "remove", r.Name}, append([]string{"mcp", "add", r.Name, "--", r.Command}, r.Args()...))
	if res.Status == StatusRegistered {
		res.Message += userScopeHint(r)
	}
	return res, ps
}

// userScopeHint 说明用户级注册固定指向一个项目目录：换到其他项目时需要在该项目中用同一名称重新注册（mcp-install）。
func userScopeHint(r Request) string {
	return "；用户级注册指向项目目录 " + r.Dir + "，换到其他项目时需在该项目重新运行 wa-cli mcp install --client " + r.Client + " --scope user --name " + r.Name
}

// jsonConfig 把 mcpServers.<name> 写入 r.Config：保留其他键与条目，取值未变时不写；先写临时文件再重命名。
func jsonConfig(r Request) (Result, []workspace.Problem) {
	bad := func(msg string) (Result, []workspace.Problem) {
		return Result{Status: StatusFailed, Message: msg, Path: r.Config}, []workspace.Problem{problem(CodeInvalidConfig, workspace.SeverityError, r.Config, msg)}
	}
	var doc map[string]json.RawMessage
	info, err := os.Lstat(r.Config)
	switch {
	case err == nil && !info.Mode().IsRegular():
		return bad("配置文件不是普通文件（如符号链接），wa-cli 不会写入它；请给出实际的文件路径")
	case err == nil:
		data, err := os.ReadFile(r.Config)
		if err != nil {
			return bad("读取配置文件失败：" + err.Error())
		}
		if len(bytes.TrimSpace(data)) > 0 && json.Unmarshal(data, &doc) != nil {
			return bad("配置文件不是 JSON 对象")
		}
	case !errors.Is(err, fs.ErrNotExist):
		return bad("读取配置文件失败：" + err.Error())
	}
	if doc == nil {
		doc = map[string]json.RawMessage{} // 空文件或 null：从空对象开始
	}
	var servers map[string]json.RawMessage
	if raw, ok := doc["mcpServers"]; ok && (json.Unmarshal(raw, &servers) != nil || servers == nil) {
		return bad("mcpServers 不是对象")
	}
	if servers == nil {
		servers = map[string]json.RawMessage{}
	}
	// 只改自己的条目中的 command 与 args，保留作者加的其他字段（如 env）。
	var entry map[string]json.RawMessage
	if old, ok := servers[r.Name]; ok && (json.Unmarshal(old, &entry) != nil || entry == nil) {
		return bad("mcpServers." + r.Name + " 不是对象")
	}
	if entry == nil {
		entry = map[string]json.RawMessage{}
	}
	if sameJSON(entry["command"], encode(r.Command)) && sameJSON(entry["args"], encode(r.Args())) {
		return Result{Status: StatusUnchanged, Message: r.Name + " 已是当前配置", Path: r.Config}, nil
	}
	entry["command"], entry["args"] = encode(r.Command), encode(r.Args())
	servers[r.Name] = encode(entry)
	doc["mcpServers"] = encode(servers)
	var buf bytes.Buffer
	enc := json.NewEncoder(&buf)
	enc.SetEscapeHTML(false)
	enc.SetIndent("", "  ")
	if err := enc.Encode(doc); err != nil {
		return bad("生成配置失败：" + err.Error())
	}
	if err := writeAtomic(r.Config, buf.Bytes()); err != nil {
		return Result{Status: StatusFailed, Message: err.Error()}, []workspace.Problem{problem(workspace.CodeWriteFailed, workspace.SeverityError, r.Config, "写入配置文件失败："+err.Error())}
	}
	return Result{Status: StatusUpdated, Message: "已写入 " + r.Name, Changed: r.Config, Path: r.Config}, nil
}

// encode 编码 v，不转义 HTML 字符，使作者文件中的其他取值保持原样。
func encode(v any) json.RawMessage {
	var buf bytes.Buffer
	enc := json.NewEncoder(&buf)
	enc.SetEscapeHTML(false)
	enc.Encode(v)
	return json.RawMessage(bytes.TrimSpace(buf.Bytes()))
}

func sameJSON(a, b json.RawMessage) bool {
	var x, y any
	return len(a) > 0 && json.Unmarshal(a, &x) == nil && json.Unmarshal(b, &y) == nil && reflect.DeepEqual(x, y)
}

// writeAtomic 在同目录写临时文件后重命名；已有文件保留其权限，目录不存在时创建。
func writeAtomic(path string, data []byte) error {
	dir := filepath.Dir(path)
	if err := os.MkdirAll(dir, 0o755); err != nil {
		return err
	}
	mode := os.FileMode(0o644)
	if info, err := os.Stat(path); err == nil {
		mode = info.Mode().Perm()
	}
	tmp, err := os.CreateTemp(dir, "."+filepath.Base(path)+".tmp-*")
	if err != nil {
		return err
	}
	defer os.Remove(tmp.Name())
	if _, err := tmp.Write(data); err != nil {
		tmp.Close()
		return err
	}
	if err := tmp.Chmod(mode); err != nil {
		tmp.Close()
		return err
	}
	if err := tmp.Close(); err != nil {
		return err
	}
	return os.Rename(tmp.Name(), path)
}
