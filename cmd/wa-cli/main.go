// wa-cli 是 writing-anything 的命令行工具（CLI）。
package main

import (
	"os"

	"github.com/kw12121212/writing-anything/internal/cli"
)

func main() {
	os.Exit(cli.Run(os.Args[1:], os.Stdout, os.Stderr))
}
