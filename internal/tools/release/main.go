// release 以交叉编译（Cross-compilation）生成 wa-cli 的发布产物（Release Asset）：
// linux、darwin、windows 各自 amd64 与 arm64 的二进制 wa-cli-<os>-<arch>[.exe]，
// 以及 SHA-256 校验文件（Checksum File）SHA256SUMS。
//
// 用法：go run ./internal/tools/release -version v1.2.3 [-commit <提交>] [-out dist] [-targets linux/amd64,windows/arm64]
package main

import (
	"crypto/sha256"
	"encoding/hex"
	"errors"
	"fmt"
	"io"
	"os"
	"os/exec"
	"path/filepath"
	"slices"
	"sort"
	"strings"
)

const (
	module   = "github.com/kw12121212/writing-anything"
	sumsFile = "SHA256SUMS"
)

const stampChars = "abcdefghijklmnopqrstuvwxyzABCDEFGHIJKLMNOPQRSTUVWXYZ0123456789._+-"

// allTargets 是发布覆盖的全部 <os>/<arch> 目标。
var allTargets = []string{"linux/amd64", "linux/arm64", "darwin/amd64", "darwin/arm64", "windows/amd64", "windows/arm64"}

const usage = "用法：go run ./internal/tools/release -version <版本> [-commit <提交>] [-out <目录>] [-targets <os>/<arch>,...]"

func main() {
	os.Exit(run(os.Args[1:], ".", os.Stdout, os.Stderr))
}

type options struct {
	version, commit, out string
	targets              []string
}

// run 在模块根目录 dir 中构建发布产物，相对路径的 -out 也相对 dir 解析；返回进程退出码：0 成功，1 构建失败，2 用法错误。
func run(args []string, dir string, stdout, stderr io.Writer) int {
	opts, err := parseArgs(args)
	if err != nil {
		fmt.Fprintf(stderr, "release：%v\n%s\n", err, usage)
		return 2
	}
	if opts.commit == "" {
		out, err := exec.Command("git", "-C", dir, "rev-parse", "HEAD").Output()
		if err != nil {
			fmt.Fprintf(stderr, "release：无法用 git rev-parse HEAD 取得当前提交（%v），请用 -commit 指定\n", err)
			return 1
		}
		opts.commit = strings.TrimSpace(string(out))
	}
	out := opts.out
	if !filepath.IsAbs(out) {
		out = filepath.Join(dir, out)
	}
	if err := build(dir, out, opts, stdout); err != nil {
		fmt.Fprintf(stderr, "release：%v\n", err)
		return 1
	}
	fmt.Fprintf(stdout, "已生成 %d 个发布产物与 %s：%s\n", len(opts.targets), sumsFile, out)
	return 0
}

// parseArgs 手动解析参数，使报错与项目的中文诊断一致；接受 -name value、-name=value 与 --name 形式。
func parseArgs(args []string) (options, error) {
	opts := options{out: "dist"}
	targets := strings.Join(allTargets, ",")
	for i := 0; i < len(args); i++ {
		arg := args[i]
		if !strings.HasPrefix(arg, "-") || arg == "-" || arg == "--" {
			return opts, fmt.Errorf("多余的参数：%s", arg)
		}
		name, value, hasValue := strings.Cut(strings.TrimPrefix(strings.TrimPrefix(arg, "-"), "-"), "=")
		var dst *string
		switch name {
		case "version":
			dst = &opts.version
		case "commit":
			dst = &opts.commit
		case "out":
			dst = &opts.out
		case "targets":
			dst = &targets
		default:
			return opts, fmt.Errorf("未知参数：%s", arg)
		}
		if !hasValue {
			if i+1 >= len(args) {
				return opts, fmt.Errorf("参数 %s 缺少取值", arg)
			}
			i++
			value = args[i]
		}
		*dst = value
	}
	if opts.version == "" {
		return opts, errors.New("缺少 -version")
	}
	// -version 与 -commit 写进 -ldflags，只允许不会被 go 的参数引号规则改写的字符。
	for flag, value := range map[string]string{"-version": opts.version, "-commit": opts.commit} {
		if i := strings.IndexFunc(value, func(r rune) bool { return !strings.ContainsRune(stampChars, r) }); i >= 0 {
			return opts, fmt.Errorf("%s 含有不允许的字符 %q（只允许字母、数字与 ._+-）", flag, value[i:i+1])
		}
	}
	if opts.out == "" {
		return opts, errors.New("-out 不能为空")
	}
	for _, t := range strings.Split(targets, ",") {
		t = strings.TrimSpace(t)
		if !slices.Contains(allTargets, t) {
			return opts, fmt.Errorf("不支持的目标：%s（支持 %s）", t, strings.Join(allTargets, "、"))
		}
		if !slices.Contains(opts.targets, t) {
			opts.targets = append(opts.targets, t)
		}
	}
	return opts, nil
}

// assetName 返回目标 <os>/<arch> 的发布产物文件名。
func assetName(target string) string {
	goos, goarch, _ := strings.Cut(target, "/")
	name := "wa-cli-" + goos + "-" + goarch
	if goos == "windows" {
		name += ".exe"
	}
	return name
}

// build 先删除 out 中旧的 SHA256SUMS 与发布产物，逐个目标构建，全部成功后才写出新的 SHA256SUMS。
func build(dir, out string, opts options, stdout io.Writer) error {
	if err := os.MkdirAll(out, 0o755); err != nil {
		return err
	}
	sumsPath := filepath.Join(out, sumsFile)
	stale := []string{sumsPath}
	for _, target := range allTargets {
		stale = append(stale, filepath.Join(out, assetName(target)))
	}
	for _, path := range stale {
		if err := os.Remove(path); err != nil && !errors.Is(err, os.ErrNotExist) {
			return err
		}
	}
	ldflags := fmt.Sprintf("-s -w -X %s/internal/version.Version=%s -X %s/internal/version.Commit=%s", module, opts.version, module, opts.commit)
	sums := make(map[string]string, len(opts.targets))
	for _, target := range opts.targets {
		goos, goarch, _ := strings.Cut(target, "/")
		name := assetName(target)
		path := filepath.Join(out, name)
		cmd := exec.Command("go", "build", "-trimpath", "-ldflags", ldflags, "-o", path, "./cmd/wa-cli")
		cmd.Dir = dir
		cmd.Env = append(os.Environ(), "GOOS="+goos, "GOARCH="+goarch, "CGO_ENABLED=0")
		if output, err := cmd.CombinedOutput(); err != nil {
			return fmt.Errorf("构建 %s 失败：%v\n%s", target, err, output)
		}
		sum, err := fileSHA256(path)
		if err != nil {
			return err
		}
		sums[name] = sum
		fmt.Fprintf(stdout, "%s  %s\n", sum, path)
	}
	names := make([]string, 0, len(sums))
	for name := range sums {
		names = append(names, name)
	}
	sort.Strings(names)
	var b strings.Builder
	for _, name := range names {
		fmt.Fprintf(&b, "%s  %s\n", sums[name], name)
	}
	return os.WriteFile(sumsPath, []byte(b.String()), 0o644)
}

func fileSHA256(path string) (string, error) {
	f, err := os.Open(path)
	if err != nil {
		return "", err
	}
	defer f.Close()
	h := sha256.New()
	if _, err := io.Copy(h, f); err != nil {
		return "", err
	}
	return hex.EncodeToString(h.Sum(nil)), nil
}
