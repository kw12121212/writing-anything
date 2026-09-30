package cli

import (
	"fmt"

	"github.com/kw12121212/writing-anything/internal/version"
)

var versionCommand = &command{
	name:       "version",
	summary:    "显示 wa-cli 版本",
	outputKeys: []outputKey{{"version", "当前 wa-cli 版本字符串"}},
	exitCodes:  standardExitCodes,
	run:        runVersion,
}

type versionResult struct {
	Result
	Version string `json:"version"`
}

func runVersion(inv *invocation) int {
	var err error
	if inv.json() {
		err = writeJSON(inv.stdout, &versionResult{Version: version.Version})
	} else {
		_, err = fmt.Fprintf(inv.stdout, "wa-cli %s\n", version.Version)
	}
	if err != nil {
		return writeFailed(inv.stderr, err)
	}
	return exitOK
}
