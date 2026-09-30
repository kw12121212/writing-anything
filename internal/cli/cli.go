// Package cli 实现 wa-cli 的命令框架：命令注册表（Command Registry）、参数解析、
// help 与所有命令共用的 --json 输出信封（Output Envelope）和诊断（Diagnostic）。
package cli

import (
	"fmt"
	"io"
	"slices"
	"strings"
)

// Run 执行 args 指定的命令（不含程序名），并返回进程退出码。stdin 供从标准输入读取内容的命令使用。
func Run(args []string, stdin io.Reader, stdout, stderr io.Writer) int {
	return registry.run(args, stdin, stdout, stderr)
}

func (s commandSet) run(args []string, stdin io.Reader, stdout, stderr io.Writer) int {
	// 只要出现 --json，用法错误也以 JSON 信封写到标准输出，便于 Agent 解析。
	asJSON := slices.Contains(args, "--json")
	if len(args) > 0 && args[0] == "--help" {
		// wa-cli --help [...] 等同 wa-cli help [...]。
		args = append([]string{helpCommand.name}, args[1:]...)
	}
	if len(args) == 0 || strings.HasPrefix(args[0], "-") {
		return usageError(stdout, stderr, asJSON, usageDiagnostic(nil, codeMissingCommand, "缺少命令"), s.overview())
	}
	c, rest := s.find(args)
	if c == nil {
		return usageError(stdout, stderr, asJSON, s.unknownCommand(args), s.overview())
	}
	inv, d := parseArgs(c, rest)
	if d != nil {
		return usageError(stdout, stderr, asJSON, d, c.helpText())
	}
	inv.all, inv.stdin, inv.stdout, inv.stderr = s, stdin, stdout, stderr
	// help 的位置参数就是要查看的命令，help <command> --help 与 help <command> 相同。
	if inv.flag("help") && (c != helpCommand || len(inv.args) == 0) {
		return writeHelp(inv, []*command{c}, false)
	}
	return c.run(inv)
}

// usageError 写出用法错误并返回退出码 2：JSON 模式把诊断写到标准输出，否则把说明与用法写到标准错误。
// 写入失败属于执行失败，返回退出码 1。
func usageError(stdout, stderr io.Writer, asJSON bool, d *Diagnostic, usage string) int {
	var err error
	if asJSON {
		err = writeJSON(stdout, &Result{Diagnostics: []Diagnostic{*d}})
	} else {
		_, err = fmt.Fprintf(stderr, "%s\n\n%s", d.Message, usage)
	}
	if err != nil {
		return writeFailed(stderr, err)
	}
	return exitUsage
}

// writeFailed 报告输出写入失败并返回退出码 1。
func writeFailed(stderr io.Writer, err error) int {
	fmt.Fprintf(stderr, "写入输出失败：%v\n", err)
	return exitError
}
