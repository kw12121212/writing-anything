package mcp

import (
	"go/parser"
	"go/token"
	"path/filepath"
	"strings"
	"testing"
)

// 协议层不 import internal/cli 与任何领域包，只能经命令层传入的函数调用 wa-cli（cli-contract/mcp-thin-wrapper）。
func TestNoInternalImports(t *testing.T) {
	files, _ := filepath.Glob("*.go")
	for _, f := range files {
		if strings.HasSuffix(f, "_test.go") {
			continue
		}
		file, err := parser.ParseFile(token.NewFileSet(), f, nil, parser.ImportsOnly)
		if err != nil {
			t.Fatal(err)
		}
		for _, imp := range file.Imports {
			if strings.Contains(imp.Path.Value, "writing-anything/") {
				t.Errorf("%s imports %s", f, imp.Path.Value)
			}
		}
	}
}
