package cli

import (
	"fmt"
	"strings"
)

// 用法错误的错误码（Error Code）。已发布的错误码不得改名或复用。
const (
	codeMissingCommand     = "usage.missing-command"
	codeUnknownCommand     = "usage.unknown-command"
	codeUnknownFlag        = "usage.unknown-flag"
	codeMissingFlagValue   = "usage.missing-flag-value"
	codeFlagTakesNoValue   = "usage.flag-takes-no-value"
	codeUnexpectedArgument = "usage.unexpected-argument"
)

// parseArgs 按 c 的声明解析 args（不含命令名）。只接受 --name 与 --name=value 形式的长参数；
// 位置参数按声明顺序匹配。出错时返回一条用法诊断。
func parseArgs(c *command, args []string) (*invocation, *Diagnostic) {
	inv := &invocation{values: map[string]string{}}
	flags := c.allFlags()
	for i := 0; i < len(args); i++ {
		arg := args[i]
		if !strings.HasPrefix(arg, "-") || arg == "-" {
			if len(inv.args) == len(c.args) {
				return nil, usageDiagnostic(c, codeUnexpectedArgument, fmt.Sprintf("多余的参数：%s", arg))
			}
			inv.args = append(inv.args, arg)
			continue
		}
		name, value, hasValue := strings.Cut(strings.TrimPrefix(arg, "--"), "=")
		spec := findFlag(flags, name)
		if !strings.HasPrefix(arg, "--") || spec == nil {
			return nil, usageDiagnostic(c, codeUnknownFlag, fmt.Sprintf("未知参数：%s", arg))
		}
		switch {
		case spec.Type == flagBool && hasValue:
			return nil, usageDiagnostic(c, codeFlagTakesNoValue, fmt.Sprintf("参数 --%s 不接受取值：%s", name, arg))
		case spec.Type == flagBool:
			value = "true"
		case !hasValue && i+1 < len(args) && !strings.HasPrefix(args[i+1], "--"):
			// 下一个以 -- 开头的参数不当作取值，避免吞掉 --json 等参数。
			i++
			value = args[i]
		case !hasValue:
			return nil, usageDiagnostic(c, codeMissingFlagValue, fmt.Sprintf("参数 --%s 缺少取值", name))
		}
		inv.values[name] = value
	}
	return inv, nil
}

func findFlag(flags []flagSpec, name string) *flagSpec {
	for i := range flags {
		if flags[i].Name == name {
			return &flags[i]
		}
	}
	return nil
}

// usageDiagnostic 生成一条用法错误诊断；c 为 nil 表示错误发生在选择命令之前。
func usageDiagnostic(c *command, code, message string) *Diagnostic {
	next := "wa-cli help --json"
	if c != nil {
		next = "wa-cli help " + c.name + " --json"
	}
	return &Diagnostic{Code: code, Severity: severityError, Message: message, Next: []string{next}}
}
