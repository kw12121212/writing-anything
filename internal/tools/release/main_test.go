package main

import (
	"bytes"
	"crypto/sha256"
	"encoding/hex"
	"encoding/json"
	"os"
	"os/exec"
	"path/filepath"
	"runtime"
	"slices"
	"strings"
	"testing"
)

// repoRoot 是模块根目录，相对本包目录。
const repoRoot = "../../.."

func release(t *testing.T, args ...string) (code int, stdout, stderr string) {
	t.Helper()
	var out, errOut bytes.Buffer
	code = run(args, repoRoot, &out, &errOut)
	return code, out.String(), errOut.String()
}

func hostTarget() string { return runtime.GOOS + "/" + runtime.GOARCH }

func TestUsageErrors(t *testing.T) {
	tests := []struct {
		name       string
		args       []string
		wantStderr string
	}{
		{"missing version", []string{"-out", "x"}, "缺少 -version"},
		{"unknown flag", []string{"-version", "v1", "-nope"}, "未知参数：-nope"},
		{"missing value", []string{"-version"}, "参数 -version 缺少取值"},
		{"extra argument", []string{"-version", "v1", "extra"}, "多余的参数：extra"},
		{"unsupported target", []string{"-version", "v1", "-targets", "plan9/mips"}, "不支持的目标：plan9/mips"},
		{"empty target", []string{"-version", "v1", "-targets", "linux/amd64,"}, "不支持的目标："},
		{"whitespace in version", []string{"-version", "v1 -X x=y"}, "-version 含有不允许的字符 \" \""},
		{"quote in commit", []string{"-version", "v1", "-commit", "a'b"}, "-commit 含有不允许的字符 \"'\""},
		{"triple dash", []string{"---version", "v1"}, "未知参数：---version"},
		{"empty out", []string{"-version", "v1", "-out="}, "-out 不能为空"},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			out := t.TempDir()
			args := append([]string{"-out", out}, tt.args...)
			code, _, stderr := release(t, args...)
			if code != 2 {
				t.Fatalf("exit code = %d, want 2; stderr: %s", code, stderr)
			}
			if !strings.Contains(stderr, tt.wantStderr) || !strings.Contains(stderr, "用法：") {
				t.Fatalf("stderr = %q, want %q and usage", stderr, tt.wantStderr)
			}
			if _, err := os.Stat(filepath.Join(out, "SHA256SUMS")); !os.IsNotExist(err) {
				t.Fatalf("SHA256SUMS exists after usage error (err = %v)", err)
			}
		})
	}
}

func TestParseArgs(t *testing.T) {
	opts, err := parseArgs([]string{"--version=v1.2.3", "-commit", "abc", "-targets", "linux/amd64, linux/amd64 ,windows/arm64"})
	if err != nil {
		t.Fatal(err)
	}
	if opts.version != "v1.2.3" || opts.commit != "abc" || opts.out != "dist" {
		t.Fatalf("opts = %+v", opts)
	}
	if want := []string{"linux/amd64", "windows/arm64"}; !slices.Equal(opts.targets, want) {
		t.Fatalf("targets = %v, want %v", opts.targets, want)
	}
	opts, err = parseArgs([]string{"-version", "v1"})
	if err != nil || !slices.Equal(opts.targets, allTargets) {
		t.Fatalf("default targets = %v (err %v), want %v", opts.targets, err, allTargets)
	}
}

func TestAssetName(t *testing.T) {
	for target, want := range map[string]string{
		"linux/amd64":   "wa-cli-linux-amd64",
		"darwin/arm64":  "wa-cli-darwin-arm64",
		"windows/amd64": "wa-cli-windows-amd64.exe",
	} {
		if got := assetName(target); got != want {
			t.Errorf("assetName(%q) = %q, want %q", target, got, want)
		}
	}
}

// readSums 解析 SHA256SUMS，返回文件名到哈希的映射，并断言每行格式为「<64 位小写十六进制>  <文件名>」。
func readSums(t *testing.T, dir string) map[string]string {
	t.Helper()
	data, err := os.ReadFile(filepath.Join(dir, "SHA256SUMS"))
	if err != nil {
		t.Fatal(err)
	}
	sums := map[string]string{}
	for _, line := range strings.Split(strings.TrimSuffix(string(data), "\n"), "\n") {
		sum, name, ok := strings.Cut(line, "  ")
		if !ok || len(sum) != 64 || strings.ToLower(sum) != sum {
			t.Fatalf("SHA256SUMS line %q is not '<hex>  <name>'", line)
		}
		if _, err := hex.DecodeString(sum); err != nil {
			t.Fatalf("SHA256SUMS line %q: %v", line, err)
		}
		sums[name] = sum
	}
	return sums
}

func fileSum(t *testing.T, path string) string {
	t.Helper()
	data, err := os.ReadFile(path)
	if err != nil {
		t.Fatal(err)
	}
	h := sha256.Sum256(data)
	return hex.EncodeToString(h[:])
}

// TestStampedBinary 为本机平台构建发布产物，运行它确认注入了版本与提交。
func TestStampedBinary(t *testing.T) {
	if testing.Short() {
		t.Skip("构建二进制较慢，-short 时跳过")
	}
	if !slices.Contains(allTargets, hostTarget()) {
		t.Skipf("本机平台 %s 不在发布目标中", hostTarget())
	}
	out := t.TempDir()
	// 旧的 SHA256SUMS 会被替换而不是追加，其他目标的旧产物会被删除，其他文件保留。
	for _, name := range []string{"SHA256SUMS", "wa-cli-windows-arm64.exe", "notes.txt"} {
		if err := os.WriteFile(filepath.Join(out, name), []byte("stale\n"), 0o644); err != nil {
			t.Fatal(err)
		}
	}
	code, stdout, stderr := release(t, "-version", "v1.2.3", "-commit", "abc1234", "-targets", hostTarget(), "-out", out)
	if code != 0 {
		t.Fatalf("exit code = %d; stderr: %s", code, stderr)
	}
	if !strings.Contains(stdout, "已生成 1 个发布产物与 SHA256SUMS") {
		t.Fatalf("stdout = %q", stdout)
	}
	name := assetName(hostTarget())
	if _, err := os.Stat(filepath.Join(out, "wa-cli-windows-arm64.exe")); hostTarget() != "windows/arm64" && !os.IsNotExist(err) {
		t.Fatalf("stale asset of another target was kept (err = %v)", err)
	}
	if _, err := os.Stat(filepath.Join(out, "notes.txt")); err != nil {
		t.Fatalf("unrelated file was removed: %v", err)
	}
	sums := readSums(t, out)
	if len(sums) != 1 || sums[name] != fileSum(t, filepath.Join(out, name)) {
		t.Fatalf("SHA256SUMS = %v, want one matching entry for %s", sums, name)
	}
	if _, err := exec.LookPath("sha256sum"); err == nil {
		cmd := exec.Command("sha256sum", "-c", "SHA256SUMS")
		cmd.Dir = out
		if output, err := cmd.CombinedOutput(); err != nil {
			t.Fatalf("sha256sum -c: %v\n%s", err, output)
		}
	}
	output, err := exec.Command(filepath.Join(out, name), "version", "--json").Output()
	if err != nil {
		t.Fatalf("version --json: %v", err)
	}
	var got struct{ Version, Commit string }
	if err := json.Unmarshal(output, &got); err != nil || got.Version != "v1.2.3" || got.Commit != "abc1234" {
		t.Fatalf("version --json = %s (err %v), want version v1.2.3 and commit abc1234", output, err)
	}
}

// TestAllTargets 构建全部 6 个目标，确认产物文件名与 SHA256SUMS 一一对应。
func TestAllTargets(t *testing.T) {
	if testing.Short() {
		t.Skip("交叉编译全部目标较慢，-short 时跳过")
	}
	out := t.TempDir()
	code, _, stderr := release(t, "-version", "v1.2.3", "-commit", "abc1234", "-out", out)
	if code != 0 {
		t.Fatalf("exit code = %d; stderr: %s", code, stderr)
	}
	entries, err := os.ReadDir(out)
	if err != nil {
		t.Fatal(err)
	}
	var files []string
	for _, e := range entries {
		files = append(files, e.Name())
	}
	want := []string{"SHA256SUMS", "wa-cli-darwin-amd64", "wa-cli-darwin-arm64", "wa-cli-linux-amd64", "wa-cli-linux-arm64", "wa-cli-windows-amd64.exe", "wa-cli-windows-arm64.exe"}
	if !slices.Equal(files, want) {
		t.Fatalf("files = %v, want %v", files, want)
	}
	sums := readSums(t, out)
	if len(sums) != 6 {
		t.Fatalf("SHA256SUMS has %d entries, want 6", len(sums))
	}
	for name, sum := range sums {
		if got := fileSum(t, filepath.Join(out, name)); got != sum {
			t.Errorf("%s: SHA-256 = %s, SHA256SUMS = %s", name, got, sum)
		}
	}
}

func TestDefaultCommitFromGit(t *testing.T) {
	if testing.Short() {
		t.Skip("构建二进制较慢，-short 时跳过")
	}
	head, err := exec.Command("git", "-C", repoRoot, "rev-parse", "HEAD").Output()
	if err != nil {
		t.Skipf("无法取得 git 提交：%v", err)
	}
	if !slices.Contains(allTargets, hostTarget()) {
		t.Skipf("本机平台 %s 不在发布目标中", hostTarget())
	}
	out := t.TempDir()
	if code, _, stderr := release(t, "-version", "v1.2.3", "-targets", hostTarget(), "-out", out); code != 0 {
		t.Fatalf("exit code = %d; stderr: %s", code, stderr)
	}
	output, err := exec.Command(filepath.Join(out, assetName(hostTarget())), "version").Output()
	if want := "wa-cli v1.2.3 (commit " + strings.TrimSpace(string(head)) + ")\n"; err != nil || string(output) != want {
		t.Fatalf("version = %q (err %v), want %q", output, err, want)
	}
}

func TestBuildFailure(t *testing.T) {
	// 模块根目录不存在时 go build 失败：以退出码 1 退出，不写出 SHA256SUMS。
	out := t.TempDir()
	var stdout, stderr bytes.Buffer
	code := run([]string{"-version", "v1", "-commit", "abc", "-targets", "linux/amd64", "-out", out}, t.TempDir(), &stdout, &stderr)
	if code != 1 || !strings.Contains(stderr.String(), "构建 linux/amd64 失败") {
		t.Fatalf("exit code = %d, stderr = %q; want 1 and a build failure", code, stderr.String())
	}
	if _, err := os.Stat(filepath.Join(out, "SHA256SUMS")); !os.IsNotExist(err) {
		t.Fatalf("SHA256SUMS exists after build failure (err = %v)", err)
	}
}

func TestMissingGit(t *testing.T) {
	dir := t.TempDir()
	// 阻止 git 向上找到临时目录之外的仓库。
	t.Setenv("GIT_CEILING_DIRECTORIES", filepath.Dir(dir))
	var stdout, stderr bytes.Buffer
	code := run([]string{"-version", "v1"}, dir, &stdout, &stderr)
	if code != 1 || !strings.Contains(stderr.String(), "请用 -commit 指定") {
		t.Fatalf("exit code = %d, stderr = %q; want 1 and a hint to pass -commit", code, stderr.String())
	}
}
