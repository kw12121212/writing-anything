package cli

import (
	"io/fs"
	"os"
	"path/filepath"
	"regexp"
	"strings"
	"testing"
)

// go-project-structure/env-read-in-cli：按取值读取进程环境变量（os.Getenv、os.LookupEnv）只能放在 internal/cli（与开发工具
// internal/tools）中，领域包不得据环境变量改变行为；把整个环境原样传给子进程（os.Environ）不在此列。
func TestEnvReadOnlyInCLI(t *testing.T) {
	pattern := regexp.MustCompile(`\bos\.(Getenv|LookupEnv)\(`)
	root := filepath.Join("..")
	err := filepath.WalkDir(root, func(path string, d fs.DirEntry, err error) error {
		if err != nil {
			return err
		}
		rel, _ := filepath.Rel(root, path)
		rel = filepath.ToSlash(rel)
		if d.IsDir() {
			if rel == "cli" || rel == "tools" || strings.HasSuffix(rel, "/testdata") || rel == "testdata" {
				return filepath.SkipDir
			}
			return nil
		}
		if !strings.HasSuffix(path, ".go") || strings.HasSuffix(path, "_test.go") {
			return nil
		}
		data, err := os.ReadFile(path)
		if err != nil {
			return err
		}
		if pattern.Match(data) {
			t.Errorf("internal/%s 读取了环境变量；只在 internal/cli 中读取，再把结果作为参数传给领域包", rel)
		}
		return nil
	})
	if err != nil {
		t.Fatal(err)
	}
	// 扫描器自测：认出两种按取值读取的调用，放过原样传给子进程的 os.Environ。
	for _, src := range []string{`os.Getenv("X")`, `v, ok := os.LookupEnv("X")`} {
		if !pattern.MatchString(src) {
			t.Errorf("pattern misses %s", src)
		}
	}
	if pattern.MatchString(`cmd.Env = append(os.Environ(), "A=1")`) {
		t.Error("pattern flags os.Environ passed to a subprocess")
	}
}
