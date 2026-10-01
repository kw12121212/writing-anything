package cli

import (
	"fmt"
	"io"
	"strings"
)

// 退出码（Exit Code）。
const (
	exitOK    = 0
	exitError = 1
	exitUsage = 2
)

// 参数类型。
const (
	flagBool   = "bool"
	flagString = "string"
	flagList   = "list" // 可重复出现，按出现顺序收集全部取值
)

// command 是命令注册表（Command Registry）中的一条命令声明。
// 参数解析、help 文本与 help --json 都只从这些声明派生。
type command struct {
	name       string // 命令名；命令组中的命令由组名与子命令两个词组成，如 config set
	summary    string
	args       []argSpec   // 位置参数，按顺序匹配；必需参数排在可选参数之前
	flags      []flagSpec  // 命令专属参数；--json 与 --help 由框架附加
	outputKeys []outputKey // 命令专属输出键；信封字段由框架附加
	exitCodes  []exitCode
	run        func(inv *invocation) int
}

type argSpec struct {
	Name        string `json:"name"`
	Description string `json:"description"`
	Required    bool   `json:"required"`
}

type flagSpec struct {
	Name        string `json:"name"`
	Type        string `json:"type"`
	Description string `json:"description"`
}

type outputKey struct {
	Name        string `json:"name"`
	Description string `json:"description"`
}

type exitCode struct {
	Code    int    `json:"code"`
	Meaning string `json:"meaning"`
}

// 所有命令共有的参数与输出键。
var (
	commonFlags = []flagSpec{
		{"json", flagBool, "以 JSON 输出信封（Output Envelope）写出结果"},
		{"help", flagBool, "显示本命令的用法"},
	}
	envelopeKeys = []outputKey{
		{"valid", "没有 error 级诊断时为 true"},
		{"diagnostics", "诊断（Diagnostic）数组，每项含 code、severity、message、path、field、next"},
		{"changedPaths", "本次命令变更的产物路径数组"},
	}
	standardExitCodes = []exitCode{
		{exitOK, "成功"},
		{exitError, "校验失败或执行失败"},
		{exitUsage, "用法错误"},
	}
)

// commandSet 是一组按 help 列出顺序排列的命令。
type commandSet []*command

// registry 登记 wa-cli 的全部命令。
var registry = commandSet{helpCommand, versionCommand, initCommand, validateCommand, statusCommand, configSetCommand, configResolvedCommand,
	newCommand, unitAddCommand, advanceCommand, nextCommand, briefSetCommand, briefShowCommand,
	outlineSetCommand, outlineRemoveCommand, outlineShowCommand, contextCommand, lintCommand,
	sourceAddCommand, sourceListCommand, draftWriteCommand, diffCommand, provenanceCommand, claimAddCommand, claimSetCommand, claimListCommand, claimRemoveCommand,
	methodListCommand, methodShowCommand, reviewRecordCommand, reviewShowCommand, reviewResolveCommand, acceptCommand, exportCommand, skillsListCommand, skillsInstallCommand,
	feedbackAddCommand, feedbackListCommand, feedbackResolveCommand, feedbackWatchCommand,
	decisionAddCommand, decisionsCommand, decideCommand}

func (s commandSet) lookup(name string) *command {
	for _, c := range s {
		if c.name == name {
			return c
		}
	}
	return nil
}

// find 按最长命令名匹配 words 开头的命令，返回命令与其余参数；没有匹配时返回 nil。
func (s commandSet) find(words []string) (*command, []string) {
	if len(words) >= 2 && !strings.HasPrefix(words[1], "-") {
		if c := s.lookup(words[0] + " " + words[1]); c != nil {
			return c, words[2:]
		}
	}
	if len(words) > 0 {
		if c := s.lookup(words[0]); c != nil {
			return c, words[1:]
		}
	}
	return nil, words
}

// unknownCommand 生成未知命令的用法诊断；words[0] 是命令组名时列出该组可用的命令。
func (s commandSet) unknownCommand(words []string) *Diagnostic {
	name := words[0]
	var members []string
	for _, c := range s {
		if strings.HasPrefix(c.name, words[0]+" ") {
			members = append(members, c.name)
		}
	}
	if len(members) == 0 {
		return usageDiagnostic(nil, codeUnknownCommand, "未知命令："+name)
	}
	if len(words) >= 2 && !strings.HasPrefix(words[1], "-") {
		name += " " + words[1]
	}
	return usageDiagnostic(nil, codeUnknownCommand, fmt.Sprintf("未知命令：%s；%s 命令组可用的命令：%s", name, words[0], strings.Join(members, "、")))
}

func (c *command) allFlags() []flagSpec {
	return append(append([]flagSpec{}, c.flags...), commonFlags...)
}

func (c *command) allOutputKeys() []outputKey {
	return append(append([]outputKey{}, envelopeKeys...), c.outputKeys...)
}

// invocation 是一次已解析的命令调用。
type invocation struct {
	all    commandSet // 所在的命令注册表；help 通过它列出命令，避免包级变量初始化循环
	args   []string
	values map[string]string   // 出现过的参数；bool 参数的值为 "true"，list 参数为最后一次的取值
	lists  map[string][]string // list 参数按出现顺序的全部取值
	stdin  io.Reader
	stdout io.Writer
	stderr io.Writer
}

func (inv *invocation) flag(name string) bool {
	_, ok := inv.values[name]
	return ok
}

func (inv *invocation) json() bool { return inv.flag("json") }
