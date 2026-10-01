package cli

import (
	"fmt"

	"github.com/kw12121212/writing-anything/internal/version"
)

var versionCommand = &command{
	name:    "version",
	summary: "显示 wa-cli 版本与提交",
	outputKeys: []outputKey{
		{"version", "当前 wa-cli 版本字符串；未注入且构建信息中没有模块版本时为 dev"},
		{"commit", "构建时的源码提交；未知时为 unknown，源码有未提交的修改时带 -dirty 后缀"},
	},
	exitCodes: standardExitCodes,
	run:       runVersion,
}

type versionResult struct {
	Result
	Version string `json:"version"`
	Commit  string `json:"commit"`
}

func runVersion(inv *invocation) int {
	v, c := version.Info()
	var err error
	if inv.json() {
		err = writeJSON(inv.stdout, &versionResult{Version: v, Commit: c})
	} else {
		_, err = fmt.Fprintf(inv.stdout, "wa-cli %s (commit %s)\n", v, c)
	}
	if err != nil {
		return writeFailed(inv.stderr, err)
	}
	return exitOK
}
