// covercheck 读取 go test -coverprofile 生成的覆盖率文件，按语句加权计算
// internal/**（不含 internal/tools/**）的覆盖率（Coverage），低于阈值时以非零退出。
//
// 用法：go run ./internal/tools/covercheck -min 80 -profile coverage.out
package main

import (
	"bufio"
	"errors"
	"flag"
	"fmt"
	"io"
	"math"
	"os"
	"path/filepath"
	"strconv"
	"strings"
)

func main() {
	os.Exit(run(os.Args[1:], ".", os.Stdout, os.Stderr))
}

// run 以 dir 下的 go.mod 确定模块路径，相对路径的 profile 也相对 dir 解析；返回进程退出码：0 达标，1 未达标或输入错误，2 用法错误。
func run(args []string, dir string, stdout, stderr io.Writer) int {
	fs := flag.NewFlagSet("covercheck", flag.ContinueOnError)
	fs.SetOutput(stderr)
	minPct := fs.Float64("min", 80, "最低覆盖率百分比")
	profile := fs.String("profile", "coverage.out", "go test -coverprofile 生成的文件")
	if err := fs.Parse(args); err != nil {
		return 2
	}
	if fs.NArg() > 0 {
		fmt.Fprintf(stderr, "covercheck：未知参数：%s\n", fs.Arg(0))
		return 2
	}
	if math.IsNaN(*minPct) || *minPct < 0 || *minPct > 100 {
		fmt.Fprintf(stderr, "covercheck：-min 必须在 0 到 100 之间\n")
		return 2
	}
	profilePath := *profile
	if !filepath.IsAbs(profilePath) {
		profilePath = filepath.Join(dir, profilePath)
	}

	module, err := readModulePath(filepath.Join(dir, "go.mod"))
	if err != nil {
		fmt.Fprintf(stderr, "covercheck：%v\n", err)
		return 1
	}
	covered, total, err := measure(profilePath, module)
	if err != nil {
		fmt.Fprintf(stderr, "covercheck：%v\n", err)
		return 1
	}
	if total == 0 {
		fmt.Fprintf(stderr, "covercheck：%s 中没有 internal/**（不含 internal/tools/**）的语句\n", *profile)
		return 1
	}
	pct := float64(covered) * 100 / float64(total)
	if pct < *minPct {
		fmt.Fprintf(stderr, "covercheck：覆盖率 %.2f%%（%d/%d 条语句）低于阈值 %.2f%%\n", pct, covered, total, *minPct)
		return 1
	}
	fmt.Fprintf(stdout, "covercheck：覆盖率 %.2f%%（%d/%d 条语句），阈值 %.2f%%\n", pct, covered, total, *minPct)
	return 0
}

// readModulePath 返回 go.mod 中 module 指令声明的模块路径。
func readModulePath(path string) (string, error) {
	data, err := os.ReadFile(path)
	if err != nil {
		return "", fmt.Errorf("读取 go.mod 失败：%w", err)
	}
	for _, line := range strings.Split(string(data), "\n") {
		if i := strings.Index(line, "//"); i >= 0 {
			line = line[:i]
		}
		fields := strings.Fields(line)
		if len(fields) != 2 || fields[0] != "module" {
			continue
		}
		mod := fields[1]
		if unquoted, err := strconv.Unquote(mod); err == nil {
			mod = unquoted
		}
		return mod, nil
	}
	return "", fmt.Errorf("%s 中没有 module 指令", path)
}

// inScope 判断模块内的文件是否计入覆盖率统计。
func inScope(file, module string) bool {
	rel, ok := strings.CutPrefix(file, module+"/")
	return ok && strings.HasPrefix(rel, "internal/") && !strings.HasPrefix(rel, "internal/tools/")
}

type block struct {
	stmts   int
	covered bool
}

// measure 返回范围内已覆盖的语句数与语句总数。同一代码块出现多次时合并，任一记录执行过即视为已覆盖。
func measure(path, module string) (covered, total int, err error) {
	f, err := os.Open(path)
	if err != nil {
		return 0, 0, fmt.Errorf("读取覆盖率文件失败：%w", err)
	}
	defer f.Close()

	blocks := map[string]*block{}
	sc := bufio.NewScanner(f)
	lineNo := 0
	for sc.Scan() {
		lineNo++
		line := strings.TrimSpace(sc.Text())
		if lineNo == 1 {
			if !strings.HasPrefix(line, "mode:") {
				return 0, 0, fmt.Errorf("%s:1：缺少 mode 行", path)
			}
			continue
		}
		if line == "" {
			continue
		}
		key, stmts, count, err := parseLine(line)
		if err != nil {
			return 0, 0, fmt.Errorf("%s:%d：%w", path, lineNo, err)
		}
		if !inScope(key[:strings.LastIndex(key, ":")], module) {
			continue
		}
		b, ok := blocks[key]
		if !ok {
			b = &block{stmts: stmts}
			blocks[key] = b
		}
		b.covered = b.covered || count > 0
	}
	if err := sc.Err(); err != nil {
		return 0, 0, fmt.Errorf("读取覆盖率文件失败：%w", err)
	}
	if lineNo == 0 {
		return 0, 0, fmt.Errorf("%s 为空", path)
	}
	for _, b := range blocks {
		total += b.stmts
		if b.covered {
			covered += b.stmts
		}
	}
	return covered, total, nil
}

var errMalformed = errors.New("格式应为 file:startLine.startCol,endLine.endCol numStmts count")

// parseLine 解析一行覆盖记录，key 为「文件:位置」。
func parseLine(line string) (key string, stmts, count int, err error) {
	fields := strings.Fields(line)
	if len(fields) != 3 {
		return "", 0, 0, errMalformed
	}
	i := strings.LastIndex(fields[0], ":")
	if i <= 0 || !validPosition(fields[0][i+1:]) {
		return "", 0, 0, errMalformed
	}
	stmts, err = strconv.Atoi(fields[1])
	if err != nil || stmts < 0 {
		return "", 0, 0, errMalformed
	}
	count, err = strconv.Atoi(fields[2])
	if err != nil || count < 0 {
		return "", 0, 0, errMalformed
	}
	return fields[0], stmts, count, nil
}

// validPosition 判断位置是否形如 startLine.startCol,endLine.endCol。
func validPosition(pos string) bool {
	start, end, ok := strings.Cut(pos, ",")
	return ok && validLineCol(start) && validLineCol(end)
}

func validLineCol(s string) bool {
	line, col, ok := strings.Cut(s, ".")
	return ok && isDigits(line) && isDigits(col)
}

func isDigits(s string) bool {
	if s == "" {
		return false
	}
	for _, r := range s {
		if r < '0' || r > '9' {
			return false
		}
	}
	return true
}
