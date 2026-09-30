// Package cli 实现 wa-cli 的命令分发。
package cli

import (
	"encoding/json"
	"fmt"
	"io"

	"github.com/kw12121212/writing-anything/internal/version"
)

// 退出码。
const (
	exitOK    = 0
	exitError = 1
	exitUsage = 2
)

const usage = `用法：wa-cli <命令> [参数]

命令：
  version   显示 wa-cli 版本；加 --json 输出 JSON
`

const versionUsage = "用法：wa-cli version [--json]\n"

// Run 执行 args 指定的命令（不含程序名），并返回进程退出码。
func Run(args []string, stdout, stderr io.Writer) int {
	if len(args) == 0 {
		fmt.Fprint(stderr, usage)
		return exitUsage
	}
	switch args[0] {
	case "version":
		return runVersion(args[1:], stdout, stderr)
	default:
		fmt.Fprintf(stderr, "未知命令：%s\n\n%s", args[0], usage)
		return exitUsage
	}
}

func runVersion(args []string, stdout, stderr io.Writer) int {
	asJSON := false
	for _, arg := range args {
		if arg != "--json" && arg != "-json" {
			fmt.Fprintf(stderr, "未知参数：%s\n\n%s", arg, versionUsage)
			return exitUsage
		}
		asJSON = true
	}

	var err error
	if asJSON {
		err = json.NewEncoder(stdout).Encode(struct {
			Version string `json:"version"`
		}{version.Version})
	} else {
		_, err = fmt.Fprintf(stdout, "wa-cli %s\n", version.Version)
	}
	if err != nil {
		fmt.Fprintf(stderr, "写入输出失败：%v\n", err)
		return exitError
	}
	return exitOK
}
