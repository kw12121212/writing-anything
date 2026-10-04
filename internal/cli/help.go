package cli

import (
	"fmt"
	"strings"
	"text/tabwriter"
)

var helpCommand = &command{
	name:    "help",
	summary: "显示命令的用法；加 --json 列出参数、输出键和退出码",
	args: []argSpec{
		{"command", "要查看用法的命令；省略时列出全部命令", false},
		{"subcommand", "命令组中的子命令，如 wa-cli help config set 中的 set", false},
	},
	outputKeys: []outputKey{{"commands", "命令说明数组，每项含 name、summary、usage、args、flags、outputKeys、exitCodes"}},
	exitCodes:  standardExitCodes,
	run:        runHelp,
}

func runHelp(inv *invocation) int {
	if len(inv.args) == 0 {
		return writeHelp(inv, inv.all, true)
	}
	c, rest := inv.all.find(inv.args)
	switch {
	case c == nil:
		return usageError(inv.stdout, inv.stderr, inv.json(), inv.all.unknownCommand(inv.args), inv.all.overview())
	case len(rest) > 0:
		// 单词命令后多出的参数，如 help version extra。
		help := inv.all.lookup("help")
		d := usageDiagnostic(help, codeUnexpectedArgument, "多余的参数："+rest[0])
		return usageError(inv.stdout, inv.stderr, inv.json(), d, help.helpText())
	}
	return writeHelp(inv, []*command{c}, false)
}

// commandDoc 是 help --json 中一条命令的说明。
type commandDoc struct {
	Name       string      `json:"name"`
	Summary    string      `json:"summary"`
	Usage      string      `json:"usage"`
	Args       []argSpec   `json:"args"`
	Flags      []flagSpec  `json:"flags"`
	OutputKeys []outputKey `json:"outputKeys"`
	ExitCodes  []exitCode  `json:"exitCodes"`
}

type helpResult struct {
	Result
	Commands []commandDoc `json:"commands"`
}

// writeHelp 输出 cmds 的用法：JSON 模式输出 commands 数组，文本模式输出总览（overview 为 true）或单条命令的用法。
func writeHelp(inv *invocation, cmds commandSet, overview bool) int {
	var err error
	switch {
	case inv.json():
		res := &helpResult{Commands: []commandDoc{}}
		for _, c := range cmds {
			res.Commands = append(res.Commands, c.doc())
		}
		err = writeJSON(inv.stdout, res)
	case overview:
		_, err = fmt.Fprint(inv.stdout, cmds.overview())
	default:
		_, err = fmt.Fprint(inv.stdout, cmds[0].helpText())
	}
	if err != nil {
		return writeFailed(inv.stderr, err)
	}
	return exitOK
}

func (c *command) doc() commandDoc {
	return commandDoc{
		Name:       c.name,
		Summary:    c.summary,
		Usage:      c.usage(),
		Args:       append([]argSpec{}, c.args...),
		Flags:      c.allFlags(),
		OutputKeys: c.allOutputKeys(),
		ExitCodes:  append([]exitCode{}, c.exitCodes...),
	}
}

// usage 返回一行用法，如 wa-cli help [<command>] [<subcommand>] [--json] [--help]。
func (c *command) usage() string {
	parts := []string{"wa-cli", c.name}
	for _, a := range c.args {
		if a.Required {
			parts = append(parts, "<"+a.Name+">")
		} else {
			parts = append(parts, "[<"+a.Name+">]")
		}
	}
	for _, f := range c.allFlags() {
		switch f.Type {
		case flagString:
			parts = append(parts, "[--"+f.Name+" <value>]")
		case flagList:
			parts = append(parts, "[--"+f.Name+" <value>]...")
		default:
			parts = append(parts, "[--"+f.Name+"]")
		}
	}
	return strings.Join(parts, " ")
}

// overview 返回列出全部命令的总览用法。
func (s commandSet) overview() string {
	var b strings.Builder
	b.WriteString("用法：wa-cli <command> [<args>]\n\n命令：\n")
	tw := tabwriter.NewWriter(&b, 0, 0, 3, ' ', 0)
	for _, c := range s {
		fmt.Fprintf(tw, "  %s\t%s\n", c.name, c.summary)
	}
	tw.Flush()
	b.WriteString("\n运行 wa-cli help <command> 查看命令的用法；加 --json 输出 JSON。\n")
	return b.String()
}

// helpText 返回单条命令的完整用法。
func (c *command) helpText() string {
	var b strings.Builder
	fmt.Fprintf(&b, "用法：%s\n\n%s\n", c.usage(), c.summary)
	section := func(title string, rows [][2]string) {
		if len(rows) == 0 {
			return
		}
		fmt.Fprintf(&b, "\n%s：\n", title)
		tw := tabwriter.NewWriter(&b, 0, 0, 3, ' ', 0)
		for _, r := range rows {
			fmt.Fprintf(tw, "  %s\t%s\n", r[0], r[1])
		}
		tw.Flush()
	}
	var rows [][2]string
	for _, a := range c.args {
		rows = append(rows, [2]string{"<" + a.Name + ">", a.Description})
	}
	section("位置参数", rows)
	rows = nil
	for _, f := range c.allFlags() {
		rows = append(rows, [2]string{"--" + f.Name, f.Description})
	}
	section("参数", rows)
	rows = nil
	for _, k := range c.allOutputKeys() {
		rows = append(rows, [2]string{k.Name, k.Description})
	}
	section("输出键（--json）", rows)
	rows = nil
	for _, e := range c.exitCodes {
		rows = append(rows, [2]string{fmt.Sprint(e.Code), e.Meaning})
	}
	section("退出码", rows)
	return b.String()
}
