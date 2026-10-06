package main

import (
	"crypto/sha256"
	"encoding/hex"
	"encoding/json"
	"errors"
	"fmt"
	"net/http"
	"net/http/httptest"
	"os"
	"os/exec"
	"path/filepath"
	"runtime"
	"slices"
	"strings"
	"sync"
	"testing"
)

// assetServer 在 /latest/download/ 与 /download/<version>/ 下提供发布产物，并记录请求的路径。
type assetServer struct {
	*httptest.Server
	mu    sync.Mutex
	files map[string][]byte
	paths []string
}

func newAssetServer(t *testing.T, files map[string][]byte) *assetServer {
	t.Helper()
	s := &assetServer{files: files}
	s.Server = httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		s.mu.Lock()
		s.paths = append(s.paths, r.URL.Path)
		s.mu.Unlock()
		data, ok := files[filepath.Base(r.URL.Path)]
		if !ok || !(strings.HasPrefix(r.URL.Path, "/latest/download/") || strings.HasPrefix(r.URL.Path, "/download/")) {
			http.NotFound(w, r)
			return
		}
		w.Write(data)
	}))
	t.Cleanup(s.Close)
	return s
}

func (s *assetServer) requested() []string {
	s.mu.Lock()
	defer s.mu.Unlock()
	return slices.Clone(s.paths)
}

// fakeAssets 为每个目标生成内容可区分的伪产物与匹配的 SHA256SUMS。
func fakeAssets() map[string][]byte {
	files := map[string][]byte{}
	var sums strings.Builder
	for _, target := range allTargets {
		name := assetName(target)
		data := []byte("fake " + target + "\n")
		files[name] = data
		h := sha256.Sum256(data)
		fmt.Fprintf(&sums, "%s  %s\n", hex.EncodeToString(h[:]), name)
	}
	files["SHA256SUMS"] = []byte(sums.String())
	return files
}

// fakeUname 返回一个目录，其中的 uname 对 -s 与 -m 输出给定的值。
func fakeUname(t *testing.T, system, machine string) string {
	t.Helper()
	dir := t.TempDir()
	script := fmt.Sprintf("#!/bin/sh\ncase \"$1\" in\n-s) echo %q ;;\n-m) echo %q ;;\n*) exit 1 ;;\nesac\n", system, machine)
	if err := os.WriteFile(filepath.Join(dir, "uname"), []byte(script), 0o755); err != nil {
		t.Fatal(err)
	}
	return dir
}

type installResult struct {
	code           int
	stdout, stderr string
}

// runInstallSh 在给定环境变量下运行仓库根目录的 install.sh；pathPrefix 非空时放在 PATH 最前面。
func runInstallSh(t *testing.T, pathPrefix string, env ...string) installResult {
	t.Helper()
	cmd := exec.Command("sh", filepath.Join(repoRoot, "install.sh"))
	path := os.Getenv("PATH")
	if pathPrefix != "" {
		path = pathPrefix + string(os.PathListSeparator) + path
	}
	cmd.Env = append(os.Environ(), "PATH="+path, "HOME="+t.TempDir(), "WA_VERSION=", "WA_INSTALL_DIR=", "WA_BASE_URL=")
	cmd.Env = append(cmd.Env, env...)
	var stdout, stderr strings.Builder
	cmd.Stdout, cmd.Stderr = &stdout, &stderr
	code := 0
	if err := cmd.Run(); err != nil {
		var exitErr *exec.ExitError
		if !errors.As(err, &exitErr) {
			t.Fatal(err)
		}
		code = exitErr.ExitCode()
	}
	return installResult{code, stdout.String(), stderr.String()}
}

func requireShellInstaller(t *testing.T) {
	t.Helper()
	if runtime.GOOS == "windows" {
		t.Skip("install.sh 只用于 Linux 与 macOS")
	}
	for _, tool := range []string{"sh", "awk", "mktemp"} {
		if _, err := exec.LookPath(tool); err != nil {
			t.Skipf("缺少 %s", tool)
		}
	}
	_, curl := exec.LookPath("curl")
	_, wget := exec.LookPath("wget")
	if curl != nil && wget != nil {
		t.Skip("缺少 curl 与 wget")
	}
}

func readFile(t *testing.T, path string) string {
	t.Helper()
	data, err := os.ReadFile(path)
	if err != nil {
		t.Fatal(err)
	}
	return string(data)
}

func TestInstallShPlatforms(t *testing.T) {
	requireShellInstaller(t)
	server := newAssetServer(t, fakeAssets())
	tests := []struct{ system, machine, target string }{
		{"Linux", "x86_64", "linux/amd64"},
		{"Linux", "aarch64", "linux/arm64"},
		{"Linux", "arm64", "linux/arm64"},
		{"Darwin", "x86_64", "darwin/amd64"},
		{"Darwin", "arm64", "darwin/arm64"},
	}
	for _, tt := range tests {
		t.Run(tt.system+"-"+tt.machine, func(t *testing.T) {
			dir := filepath.Join(t.TempDir(), "bin")
			res := runInstallSh(t, fakeUname(t, tt.system, tt.machine), "WA_BASE_URL="+server.URL, "WA_INSTALL_DIR="+dir)
			if res.code != 0 {
				t.Fatalf("exit code = %d; stderr: %s", res.code, res.stderr)
			}
			installed := filepath.Join(dir, "wa-cli")
			if got := readFile(t, installed); got != "fake "+tt.target+"\n" {
				t.Fatalf("installed %q, want the %s asset", got, tt.target)
			}
			if info, err := os.Stat(installed); err != nil || info.Mode().Perm()&0o111 == 0 {
				t.Fatalf("installed file is not executable: %v %v", info, err)
			}
			if !strings.Contains(res.stdout, "已安装 wa-cli 到 "+installed) || !strings.Contains(res.stdout, "不在 PATH 中") {
				t.Fatalf("stdout = %q, want install path and PATH hint", res.stdout)
			}
			if entries, _ := os.ReadDir(dir); len(entries) != 1 {
				t.Fatalf("install dir has %d entries, want only wa-cli", len(entries))
			}
		})
	}
}

func TestInstallShVersionAndDefaults(t *testing.T) {
	requireShellInstaller(t)
	server := newAssetServer(t, fakeAssets())
	home := t.TempDir()
	res := runInstallSh(t, "", "WA_BASE_URL="+server.URL, "WA_VERSION=v1.2.3", "HOME="+home,
		"PATH="+fakeUname(t, "Linux", "x86_64")+string(os.PathListSeparator)+filepath.Join(home, ".local", "bin")+string(os.PathListSeparator)+os.Getenv("PATH"))
	if res.code != 0 {
		t.Fatalf("exit code = %d; stderr: %s", res.code, res.stderr)
	}
	if got := readFile(t, filepath.Join(home, ".local", "bin", "wa-cli")); got != "fake linux/amd64\n" {
		t.Fatalf("installed %q in the default directory", got)
	}
	if strings.Contains(res.stdout, "不在 PATH 中") {
		t.Fatalf("stdout = %q, want no PATH hint when the directory is in PATH", res.stdout)
	}
	want := []string{"/download/v1.2.3/wa-cli-linux-amd64", "/download/v1.2.3/SHA256SUMS"}
	if got := server.requested(); !slices.Equal(got, want) {
		t.Fatalf("requested %v, want %v", got, want)
	}
}

func TestInstallShFailures(t *testing.T) {
	requireShellInstaller(t)
	good := fakeAssets()
	mismatch := fakeAssets()
	mismatch["wa-cli-linux-amd64"] = []byte("tampered\n")
	missing := fakeAssets()
	missing["SHA256SUMS"] = []byte(strings.Join(slices.DeleteFunc(strings.Split(string(missing["SHA256SUMS"]), "\n"), func(l string) bool {
		return strings.HasSuffix(l, " wa-cli-linux-amd64")
	}), "\n"))
	noAsset := fakeAssets()
	delete(noAsset, "wa-cli-linux-amd64")

	tests := []struct {
		name            string
		files           map[string][]byte
		system, machine string
		wantStderr      string
	}{
		{"checksum mismatch", mismatch, "Linux", "x86_64", "校验失败：wa-cli-linux-amd64 的 SHA-256"},
		{"missing checksum entry", missing, "Linux", "x86_64", "SHA256SUMS 中没有 wa-cli-linux-amd64 的记录"},
		{"download failure", noAsset, "Linux", "x86_64", "无法下载"},
		{"unsupported os", good, "FreeBSD", "x86_64", "不支持的操作系统：FreeBSD"},
		{"windows shell", good, "MINGW64_NT-10.0", "x86_64", "请使用 install.ps1"},
		{"unsupported arch", good, "Linux", "i686", "不支持的 CPU 架构：i686"},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			server := newAssetServer(t, tt.files)
			dir := t.TempDir()
			installed := filepath.Join(dir, "wa-cli")
			if err := os.WriteFile(installed, []byte("old version\n"), 0o755); err != nil {
				t.Fatal(err)
			}
			res := runInstallSh(t, fakeUname(t, tt.system, tt.machine), "WA_BASE_URL="+server.URL, "WA_INSTALL_DIR="+dir)
			if res.code == 0 {
				t.Fatalf("exit code = 0, want non-zero; stdout: %s", res.stdout)
			}
			if !strings.Contains(res.stderr, "wa-cli 安装失败：") || !strings.Contains(res.stderr, tt.wantStderr) {
				t.Fatalf("stderr = %q, want %q", res.stderr, tt.wantStderr)
			}
			if got := readFile(t, installed); got != "old version\n" {
				t.Fatalf("existing wa-cli was changed to %q", got)
			}
			if entries, _ := os.ReadDir(dir); len(entries) != 1 {
				t.Fatalf("install dir has %d entries, want only the old wa-cli", len(entries))
			}
		})
	}
}

func TestInstallShEdgeCases(t *testing.T) {
	requireShellInstaller(t)
	uname := fakeUname(t, "Linux", "x86_64")

	t.Run("crlf checksums", func(t *testing.T) {
		files := fakeAssets()
		files["SHA256SUMS"] = []byte(strings.ReplaceAll(string(files["SHA256SUMS"]), "\n", "\r\n"))
		server := newAssetServer(t, files)
		dir := t.TempDir()
		if res := runInstallSh(t, uname, "WA_BASE_URL="+server.URL, "WA_INSTALL_DIR="+dir); res.code != 0 {
			t.Fatalf("exit code = %d; stderr: %s", res.code, res.stderr)
		}
		if got := readFile(t, filepath.Join(dir, "wa-cli")); got != "fake linux/amd64\n" {
			t.Fatalf("installed %q", got)
		}
	})

	t.Run("target is a directory", func(t *testing.T) {
		server := newAssetServer(t, fakeAssets())
		dir := t.TempDir()
		if err := os.Mkdir(filepath.Join(dir, "wa-cli"), 0o755); err != nil {
			t.Fatal(err)
		}
		res := runInstallSh(t, uname, "WA_BASE_URL="+server.URL, "WA_INSTALL_DIR="+dir)
		if res.code == 0 || !strings.Contains(res.stderr, "是目录，无法安装") {
			t.Fatalf("exit code = %d, stderr = %q; want non-zero and a directory error", res.code, res.stderr)
		}
		if entries, _ := os.ReadDir(filepath.Join(dir, "wa-cli")); len(entries) != 0 {
			t.Fatalf("files were moved into the wa-cli directory: %v", entries)
		}
	})

	t.Run("home unset", func(t *testing.T) {
		cmd := exec.Command("sh", filepath.Join(repoRoot, "install.sh"))
		cmd.Env = []string{"PATH=" + uname + string(os.PathListSeparator) + os.Getenv("PATH")}
		out, err := cmd.CombinedOutput()
		if err == nil || !strings.Contains(string(out), "未设置 HOME，请用 WA_INSTALL_DIR 指定安装目录") {
			t.Fatalf("err = %v, output = %q; want a HOME error", err, out)
		}
	})
}

// TestInstallShEndToEnd 用发布工具为本机构建真实产物，经 install.sh 安装后运行 wa-cli version。
func TestInstallShEndToEnd(t *testing.T) {
	requireShellInstaller(t)
	if testing.Short() {
		t.Skip("构建二进制较慢，-short 时跳过")
	}
	if !slices.Contains([]string{"linux/amd64", "linux/arm64", "darwin/amd64", "darwin/arm64"}, hostTarget()) {
		t.Skipf("本机平台 %s 不由 install.sh 安装", hostTarget())
	}
	dist := t.TempDir()
	if code, _, stderr := release(t, "-version", "v1.2.3", "-commit", "abc1234", "-targets", hostTarget(), "-out", dist); code != 0 {
		t.Fatalf("release: exit code = %d; stderr: %s", code, stderr)
	}
	files := map[string][]byte{}
	for _, name := range []string{assetName(hostTarget()), "SHA256SUMS"} {
		files[name] = []byte(readFile(t, filepath.Join(dist, name)))
	}
	server := newAssetServer(t, files)
	dir := t.TempDir()
	if res := runInstallSh(t, "", "WA_BASE_URL="+server.URL, "WA_INSTALL_DIR="+dir); res.code != 0 {
		t.Fatalf("install.sh: exit code = %d; stderr: %s", res.code, res.stderr)
	}
	output, err := exec.Command(filepath.Join(dir, "wa-cli"), "version", "--json").Output()
	var got struct{ Version, Commit string }
	if err != nil || json.Unmarshal(output, &got) != nil || got.Version != "v1.2.3" || got.Commit != "abc1234" {
		t.Fatalf("installed wa-cli version --json = %s (err %v)", output, err)
	}
}

// TestInstallPs1 在有 PowerShell（pwsh）时运行 install.ps1；PROCESSOR_ARCHITECTURE 与 OS 由环境变量模拟。
// 每个用例分别以 -File 与模拟 irm | iex（按 UTF-8 解码脚本后交给 Invoke-Expression）两种方式运行。
func TestInstallPs1(t *testing.T) {
	pwsh, err := exec.LookPath("pwsh")
	if err != nil {
		t.Skip("没有 pwsh，跳过 install.ps1 的测试")
	}
	script, err := filepath.Abs(filepath.Join(repoRoot, "install.ps1"))
	if err != nil {
		t.Fatal(err)
	}
	if data := readFile(t, script); strings.HasPrefix(data, "\uFEFF") {
		t.Fatal("install.ps1 带 BOM：irm | iex 会把 BOM 当作命令")
	}
	modes := map[string][]string{
		"file": {"-File", script},
		"iex":  {"-Command", "[Text.Encoding]::UTF8.GetString([IO.File]::ReadAllBytes($env:WA_TEST_SCRIPT)) | Invoke-Expression"},
	}
	run := func(t *testing.T, mode string, files map[string][]byte, dir string, env ...string) installResult {
		server := newAssetServer(t, files)
		cmd := exec.Command(pwsh, append([]string{"-NoProfile", "-NonInteractive"}, modes[mode]...)...)
		cmd.Env = append(os.Environ(), "WA_TEST_SCRIPT="+script, "OS=Windows_NT", "PROCESSOR_ARCHITEW6432=", "WA_VERSION=", "WA_BASE_URL="+server.URL, "WA_INSTALL_DIR="+dir)
		cmd.Env = append(cmd.Env, env...)
		var stdout, stderr strings.Builder
		cmd.Stdout, cmd.Stderr = &stdout, &stderr
		code := 0
		if err := cmd.Run(); err != nil {
			var exitErr *exec.ExitError
			if !errors.As(err, &exitErr) {
				t.Fatal(err)
			}
			code = exitErr.ExitCode()
		}
		return installResult{code, stdout.String(), stderr.String()}
	}

	for mode := range modes {
		t.Run(mode, func(t *testing.T) {
			for arch, target := range map[string]string{"AMD64": "windows/amd64", "ARM64": "windows/arm64"} {
				t.Run(arch, func(t *testing.T) {
					dir := t.TempDir()
					res := run(t, mode, fakeAssets(), dir, "PROCESSOR_ARCHITECTURE="+arch)
					if res.code != 0 || res.stderr != "" {
						t.Fatalf("exit code = %d, stderr = %q; want 0 and empty stderr", res.code, res.stderr)
					}
					installed := filepath.Join(dir, "wa-cli.exe")
					if got := readFile(t, installed); got != "fake "+target+"\n" {
						t.Fatalf("installed %q, want the %s asset", got, target)
					}
					if !strings.Contains(res.stdout, "已安装 wa-cli 到 "+installed) || !strings.Contains(res.stdout, "不在 PATH 中") {
						t.Fatalf("stdout = %q, want install path and PATH hint", res.stdout)
					}
					if entries, _ := os.ReadDir(dir); len(entries) != 1 {
						t.Fatalf("install dir has %d entries, want only wa-cli.exe", len(entries))
					}
				})
			}
		})
	}

	mismatch := fakeAssets()
	mismatch["wa-cli-windows-amd64.exe"] = []byte("tampered\n")
	missing := fakeAssets()
	missing["SHA256SUMS"] = []byte(strings.Join(slices.DeleteFunc(strings.Split(string(missing["SHA256SUMS"]), "\n"), func(l string) bool {
		return strings.HasSuffix(l, " wa-cli-windows-amd64.exe")
	}), "\n"))
	noAsset := fakeAssets()
	delete(noAsset, "wa-cli-windows-amd64.exe")
	failures := []struct {
		name       string
		files      map[string][]byte
		env        []string
		wantStderr string
	}{
		{"checksum mismatch", mismatch, []string{"PROCESSOR_ARCHITECTURE=AMD64"}, "校验失败：wa-cli-windows-amd64.exe 的 SHA-256"},
		{"missing checksum entry", missing, []string{"PROCESSOR_ARCHITECTURE=AMD64"}, "SHA256SUMS 中没有 wa-cli-windows-amd64.exe 的记录"},
		{"download failure", noAsset, []string{"PROCESSOR_ARCHITECTURE=AMD64"}, "无法下载"},
		{"unsupported arch", fakeAssets(), []string{"PROCESSOR_ARCHITECTURE=x86"}, "不支持的 CPU 架构：x86"},
		{"not windows", fakeAssets(), []string{"PROCESSOR_ARCHITECTURE=AMD64", "OS=", "WA_INSTALL_DIR=", "LOCALAPPDATA="}, "不支持的操作系统"},
	}
	for _, tt := range failures {
		for mode := range modes {
			t.Run(tt.name+"/"+mode, func(t *testing.T) {
				dir := t.TempDir()
				installed := filepath.Join(dir, "wa-cli.exe")
				if err := os.WriteFile(installed, []byte("old version\n"), 0o755); err != nil {
					t.Fatal(err)
				}
				res := run(t, mode, tt.files, dir, tt.env...)
				if res.code == 0 || !strings.Contains(res.stderr, "wa-cli 安装失败：") || !strings.Contains(res.stderr, tt.wantStderr) {
					t.Fatalf("exit code = %d, stderr = %q; want non-zero and %q", res.code, res.stderr, tt.wantStderr)
				}
				if got := readFile(t, installed); got != "old version\n" {
					t.Fatalf("existing wa-cli.exe was changed to %q", got)
				}
				if entries, _ := os.ReadDir(dir); len(entries) != 1 {
					t.Fatalf("install dir has %d entries, want only the old wa-cli.exe", len(entries))
				}
			})
		}
	}

	// 成功安装后，脚本的设置、变量与函数不留在调用者的会话里。
	server := newAssetServer(t, fakeAssets())
	cmd := exec.Command(pwsh, "-NoProfile", "-NonInteractive", "-Command",
		"$ErrorActionPreference = 'Continue'; [Text.Encoding]::UTF8.GetString([IO.File]::ReadAllBytes($env:WA_TEST_SCRIPT)) | Invoke-Expression | Out-Null; "+
			"Write-Output \"$ErrorActionPreference|$(Test-Path function:Fail)|$([bool](Get-Variable target, installDir -ErrorAction SilentlyContinue))\"")
	cmd.Env = append(os.Environ(), "WA_TEST_SCRIPT="+script, "OS=Windows_NT", "PROCESSOR_ARCHITECTURE=AMD64", "PROCESSOR_ARCHITEW6432=",
		"WA_VERSION=", "WA_BASE_URL="+server.URL, "WA_INSTALL_DIR="+t.TempDir())
	out, err := cmd.Output()
	if err != nil || strings.TrimSpace(string(out)) != "Continue|False|False" {
		t.Fatalf("session state after iex = %q (err %v), want Continue|False|False", out, err)
	}
}
