package packinstall

import (
	"crypto/sha256"
	"encoding/hex"
	"errors"
	"os"
	"os/exec"
	"path/filepath"
	"runtime"
	"slices"
	"strings"
	"testing"
	"time"
	"unicode/utf8"

	"github.com/kw12121212/writing-anything/internal/workspace"
)

const housePack = "schema_version: 1\nid: house\ntitle: 本团队规范\n# 来源中的注释原样保留\nstyle: [短句]\n"

func newWorkspace(t *testing.T) string {
	t.Helper()
	dir := t.TempDir()
	if _, ps := workspace.Init(dir, nil); len(ps) > 0 {
		t.Fatalf("init: %+v", ps)
	}
	return dir
}

func write(t *testing.T, path, content string) {
	t.Helper()
	if err := os.MkdirAll(filepath.Dir(path), 0o755); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(path, []byte(content), 0o644); err != nil {
		t.Fatal(err)
	}
}

func read(t *testing.T, path string) string {
	t.Helper()
	data, err := os.ReadFile(path)
	if err != nil {
		t.Fatal(err)
	}
	return string(data)
}

func sum(s string) string {
	h := sha256.Sum256([]byte(s))
	return hex.EncodeToString(h[:])
}

func codes(ps []workspace.Problem) []string {
	var out []string
	for _, p := range ps {
		out = append(out, p.Code)
	}
	return out
}

// noGit 是不允许调用 git 的环境：本地来源不得运行外部程序。
var noGit = Env{
	LookPath: func(string) (string, error) { return "", errors.New("unexpected LookPath") },
	Run:      func(string, string, ...string) (string, error) { return "", errors.New("unexpected Run") },
}

func TestInstallFromPath(t *testing.T) {
	root := newWorkspace(t)
	src := filepath.Join(filepath.Dir(root), filepath.Base(root)+"-house")
	write(t, filepath.Join(src, "pack.yaml"), housePack)
	rel := "../" + filepath.Base(src)
	config := read(t, filepath.Join(root, workspace.ConfigPath))

	res, ps := Install(noGit, root, rel, "")
	if ps != nil {
		t.Fatalf("problems: %+v", ps)
	}
	if res.Status != StatusInstalled || res.Title != "本团队规范" || res.Path != ".writing-anything/packs/house.yaml" ||
		!slices.Equal(res.Changed, []string{".writing-anything/packs/house.yaml", LockPath}) {
		t.Fatalf("result = %+v", res)
	}
	if res.Entry != (Entry{ID: "house", Source: Source{Path: rel}, SHA256: sum(housePack)}) {
		t.Fatalf("entry = %+v", res.Entry)
	}
	if got := read(t, filepath.Join(root, ".writing-anything/packs/house.yaml")); got != housePack {
		t.Fatalf("installed = %q, want byte-for-byte copy", got)
	}
	if got := read(t, filepath.Join(root, workspace.ConfigPath)); got != config {
		t.Fatal("config.yaml changed")
	}
	lock, ps := ReadLock(root)
	if ps != nil || len(lock.Entries) != 1 || lock.Entries[0] != res.Entry {
		t.Fatalf("lock = %+v %+v", lock, ps)
	}
	if _, ps := Check(root); ps != nil {
		t.Fatalf("check: %+v", ps)
	}

	// 来源未变：不写入任何文件。
	before, _ := os.Stat(filepath.Join(root, LockPath))
	packBefore, _ := os.Stat(filepath.Join(root, ".writing-anything/packs/house.yaml"))
	if res, ps := Install(noGit, root, rel, ""); ps != nil || res.Status != StatusUnchanged || res.Changed != nil {
		t.Fatalf("again: %+v %+v", res, ps)
	}
	if after, _ := os.Stat(filepath.Join(root, LockPath)); !after.ModTime().Equal(before.ModTime()) {
		t.Fatal("lock rewritten")
	}
	if after, _ := os.Stat(filepath.Join(root, ".writing-anything/packs/house.yaml")); !after.ModTime().Equal(packBefore.ModTime()) {
		t.Fatal("pack file rewritten")
	}

	// 来源改变：替换文件与条目。
	updated := strings.Replace(housePack, "本团队规范", "新规范", 1)
	write(t, filepath.Join(src, "pack.yaml"), updated)
	res, ps = Install(noGit, root, rel, "")
	if ps != nil || res.Status != StatusUpdated || res.Entry.SHA256 != sum(updated) || len(res.Changed) != 2 {
		t.Fatalf("update: %+v %+v", res, ps)
	}

	// 安装后被修改的文件：重新安装恢复它，锁定文件不变。
	write(t, filepath.Join(root, ".writing-anything/packs/house.yaml"), housePack)
	res, ps = Install(noGit, root, rel, "")
	if ps != nil || res.Status != StatusUpdated || !slices.Equal(res.Changed, []string{".writing-anything/packs/house.yaml"}) {
		t.Fatalf("restore: %+v %+v", res, ps)
	}

	// 单个 .yaml 文件与绝对路径。
	file := filepath.Join(t.TempDir(), "brand.yaml")
	write(t, file, "schema_version: 1\nid: brand\ntitle: 品牌\n")
	res, ps = Install(noGit, root, file, "")
	if ps != nil || res.Entry.ID != "brand" || res.Entry.Source.Path != filepath.ToSlash(file) {
		t.Fatalf("file: %+v %+v", res, ps)
	}
	lock, _ = ReadLock(root)
	if len(lock.Entries) != 2 || lock.Entries[0].ID != "brand" || lock.Entries[1].ID != "house" {
		t.Fatalf("lock not sorted by id: %+v", lock.Entries)
	}
}

func TestInstallRejects(t *testing.T) {
	tests := []struct {
		name, content, code string
	}{
		{"executable", "schema_version: 1\nid: bad\ntitle: x\nrules:\n  r1:\n    type: length-range\n    params: {script: rm}\n", "artifact.executable-content"},
		{"schema", "schema_version: 1\nid: bad\n", "artifact.schema-violation"},
		{"parse", "id: [", "artifact.parse-error"},
		{"unknown-rule-type", "schema_version: 1\nid: bad\ntitle: x\nrules:\n  r1: {type: run-shell}\n", "rule.unknown-type"},
		{"builtin-id", "schema_version: 1\nid: default\ntitle: x\n", "pack.id-conflict"},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			root := newWorkspace(t)
			file := filepath.Join(t.TempDir(), "pack.yaml")
			write(t, file, tt.content)
			res, ps := Install(noGit, root, file, "")
			if res != nil || !slices.Contains(codes(ps), tt.code) {
				t.Fatalf("got %+v %+v, want %s", res, ps, tt.code)
			}
			for _, p := range ps {
				if p.Path != "" || !strings.HasPrefix(p.Origin, "规范包来源 ") {
					t.Errorf("problem %+v: want no path and a source origin", p)
				}
			}
			for _, rel := range []string{workspace.PacksPath, LockPath} {
				if _, err := os.Stat(filepath.Join(root, rel)); err == nil {
					t.Errorf("%s created", rel)
				}
			}
		})
	}
}

func TestInstallKeepsHandwritten(t *testing.T) {
	root := newWorkspace(t)
	own := filepath.Join(root, ".writing-anything/packs/house.yaml")
	write(t, own, "schema_version: 1\nid: house\ntitle: 手写\n")
	file := filepath.Join(t.TempDir(), "pack.yaml")
	write(t, file, housePack)
	res, ps := Install(noGit, root, file, "")
	if res != nil || !slices.Equal(codes(ps), []string{"pack.exists"}) || ps[0].Path != ".writing-anything/packs/house.yaml" {
		t.Fatalf("got %+v %+v", res, ps)
	}
	if read(t, own) != "schema_version: 1\nid: house\ntitle: 手写\n" {
		t.Fatal("handwritten pack overwritten")
	}
	if runtime.GOOS != "windows" {
		os.Remove(own)
		if err := os.Symlink(file, own); err != nil {
			t.Fatal(err)
		}
		if _, ps := Install(noGit, root, file, ""); !slices.Equal(codes(ps), []string{"pack.exists"}) {
			t.Fatalf("symlink target: %+v", ps)
		}
	}
}

func TestInstallSourceErrors(t *testing.T) {
	root := newWorkspace(t)
	write(t, filepath.Join(root, "notes.txt"), housePack)
	if err := os.Mkdir(filepath.Join(root, "empty"), 0o755); err != nil {
		t.Fatal(err)
	}
	write(t, filepath.Join(root, "big", "pack.yaml"), "schema_version: 1\nid: big\ntitle: x\ndescription: "+strings.Repeat("x", MaxSize)+"\n")
	if err := os.Mkdir(filepath.Join(root, "dirpack"), 0o755); err != nil {
		t.Fatal(err)
	}
	if err := os.Mkdir(filepath.Join(root, "dirpack", "pack.yaml"), 0o755); err != nil {
		t.Fatal(err)
	}
	tests := map[string]string{
		"acme/house": "pack.registry-unsupported",
		"nope/x/y":   "pack.source-not-found",
		"missing":    "pack.source-not-found",
		"notes.txt":  "pack.invalid-source",
		"empty":      "pack.invalid-source",
		"big":        "pack.invalid-source",
		"dirpack":    "pack.invalid-source",
	}
	for src, code := range tests {
		if res, ps := Install(noGit, root, src, ""); res != nil || !slices.Equal(codes(ps), []string{code}) {
			t.Errorf("%s: %+v %+v, want %s", src, res, ps, code)
		}
	}
	// 已存在的同名目录不是保留的仓库写法。
	write(t, filepath.Join(root, "acme", "house", "pack.yaml"), housePack)
	if _, ps := Install(noGit, root, "acme/house", ""); ps != nil {
		t.Fatalf("existing acme/house: %+v", ps)
	}
}

func TestIsGit(t *testing.T) {
	for src, want := range map[string]bool{
		"https://example.com/a.git": true, "http://h/a": true, "ssh://git@h/a": true, "git://h/a": true, "file:///tmp/a": true,
		"git@github.com:acme/house.git": true, "../house": false, "acme/house": false, "/abs/house.yaml": false,
		"ext::sh -c touch% x": false, `C:\packs\house.yaml`: false,
	} {
		if IsGit(src) != want {
			t.Errorf("IsGit(%q) = %v", src, !want)
		}
	}
	for ref, want := range map[string]bool{"v1": true, "main": true, "-c": false, "--upload-pack=x": false, "a b": false, "": false} {
		if ValidRef(ref) != want {
			t.Errorf("ValidRef(%q) = %v", ref, !want)
		}
	}
}

func gitRun(t *testing.T, dir string, args ...string) string {
	t.Helper()
	cmd := exec.Command("git", args...)
	cmd.Dir = dir
	cmd.Env = append(os.Environ(), "GIT_AUTHOR_NAME=t", "GIT_AUTHOR_EMAIL=t@x", "GIT_COMMITTER_NAME=t", "GIT_COMMITTER_EMAIL=t@x")
	out, err := cmd.CombinedOutput()
	if err != nil {
		t.Fatalf("git %v: %v\n%s", args, err, out)
	}
	return strings.TrimSpace(string(out))
}

// gitRepo 创建含 pack.yaml 的 git 仓库：默认分支为第一版并打标签 v1，分支 next 为第二版。返回 file:// 地址与两个提交号。
func gitRepo(t *testing.T) (url, v1, v2 string) {
	t.Helper()
	if _, err := exec.LookPath("git"); err != nil {
		t.Skip("没有 git")
	}
	dir := t.TempDir()
	gitRun(t, dir, "init", "--quiet", "-b", "main")
	write(t, filepath.Join(dir, "pack.yaml"), housePack)
	gitRun(t, dir, "add", "pack.yaml")
	gitRun(t, dir, "commit", "--quiet", "-m", "v1")
	gitRun(t, dir, "tag", "v1")
	v1 = gitRun(t, dir, "rev-parse", "HEAD")
	gitRun(t, dir, "checkout", "--quiet", "-b", "next")
	write(t, filepath.Join(dir, "pack.yaml"), strings.Replace(housePack, "本团队规范", "第二版", 1))
	gitRun(t, dir, "commit", "--quiet", "-am", "v2")
	v2 = gitRun(t, dir, "rev-parse", "HEAD")
	gitRun(t, dir, "checkout", "--quiet", "main")
	return "file://" + filepath.ToSlash(dir), v1, v2
}

func TestInstallFromGit(t *testing.T) {
	url, v1, v2 := gitRepo(t)
	root := newWorkspace(t)
	res, ps := Install(System(), root, url, "v1")
	if ps != nil || res.Status != StatusInstalled || res.Entry != (Entry{ID: "house", Source: Source{Git: url, Ref: "v1"}, Commit: v1, SHA256: sum(housePack)}) {
		t.Fatalf("tag: %+v %+v", res, ps)
	}
	if _, ps := Check(root); ps != nil {
		t.Fatalf("check: %+v", ps)
	}
	if res, ps := Install(System(), root, url, "v1"); ps != nil || res.Status != StatusUnchanged {
		t.Fatalf("again: %+v %+v", res, ps)
	}
	// 不是默认分支的分支在 origin/ 下；完整提交号与默认分支同样可用。
	if res, ps := Install(System(), root, url, "next"); ps != nil || res.Status != StatusUpdated || res.Entry.Commit != v2 || res.Title != "第二版" {
		t.Fatalf("branch: %+v %+v", res, ps)
	}
	if res, ps := Install(System(), root, url, v1); ps != nil || res.Entry.Commit != v1 {
		t.Fatalf("commit: %+v %+v", res, ps)
	}
	if res, ps := Install(System(), root, url, ""); ps != nil || res.Entry.Commit != v1 || res.Entry.Source.Ref != "" {
		t.Fatalf("default: %+v %+v", res, ps)
	}
	if _, ps := Install(System(), root, url, "no-such-ref"); !slices.Equal(codes(ps), []string{"pack.git-failed"}) {
		t.Fatalf("missing ref: %+v", ps)
	}
	if _, ps := Install(System(), root, url+"-missing", ""); !slices.Equal(codes(ps), []string{"pack.git-failed"}) || !strings.Contains(ps[0].Message, "git clone") {
		t.Fatalf("missing repo: %+v", ps)
	}
}

func TestInstallGitSymlinkRejected(t *testing.T) {
	if runtime.GOOS == "windows" {
		t.Skip("符号链接")
	}
	if _, err := exec.LookPath("git"); err != nil {
		t.Skip("没有 git")
	}
	dir := t.TempDir()
	outside := filepath.Join(t.TempDir(), "secret.yaml")
	write(t, outside, housePack)
	gitRun(t, dir, "init", "--quiet")
	if err := os.Symlink(outside, filepath.Join(dir, "pack.yaml")); err != nil {
		t.Fatal(err)
	}
	gitRun(t, dir, "add", "pack.yaml")
	gitRun(t, dir, "commit", "--quiet", "-m", "link")
	_, ps := Install(System(), newWorkspace(t), "file://"+dir, "")
	if !slices.Equal(codes(ps), []string{"pack.invalid-source"}) || !strings.Contains(ps[0].Message, "不是普通文件") {
		t.Fatalf("got %+v", ps)
	}
}

func TestFetchGitCommandLine(t *testing.T) {
	var calls [][]string
	env := Env{
		LookPath: func(name string) (string, error) { return "/usr/bin/" + name, nil },
		Run: func(dir, name string, args ...string) (string, error) {
			calls = append(calls, append([]string{name}, args...))
			if slices.Contains(args, "rev-parse") {
				return "not-a-commit\n", nil
			}
			return "", nil
		},
	}
	_, ps := Install(env, newWorkspace(t), "https://example.com/house.git", "v1")
	if !slices.Equal(codes(ps), []string{"pack.git-failed"}) || !strings.Contains(ps[0].Message, "40 位提交号") {
		t.Fatalf("got %+v", ps)
	}
	clone := calls[0]
	if clone[0] != "/usr/bin/git" || !strings.HasPrefix(clone[2], "core.hooksPath=") || clone[4] != "protocol.ext.allow=never" ||
		!slices.Equal(clone[5:9], []string{"clone", "--quiet", "--no-checkout", "--"}) || clone[9] != "https://example.com/house.git" {
		t.Fatalf("clone = %v", clone)
	}
	if slices.Contains(clone, "--recurse-submodules") {
		t.Fatal("submodules recursed")
	}
	if rev := calls[1]; !slices.Equal(rev[len(rev)-4:], []string{"rev-parse", "--verify", "--quiet", "v1^{commit}"}) {
		t.Fatalf("rev-parse = %v", rev)
	}

	env.LookPath = func(string) (string, error) { return "", errors.New("not found") }
	if _, ps := Install(env, newWorkspace(t), "https://example.com/house.git", ""); !slices.Equal(codes(ps), []string{"pack.git-unavailable"}) {
		t.Fatalf("no git: %+v", ps)
	}
	env.LookPath = func(name string) (string, error) { return name, nil }
	env.Run = func(string, string, ...string) (string, error) {
		return "fatal: repository not found", errors.New("exit status 128")
	}
	if _, ps := Install(env, newWorkspace(t), "https://example.com/house.git", ""); !slices.Equal(codes(ps), []string{"pack.git-failed"}) || !strings.Contains(ps[0].Message, "repository not found") {
		t.Fatalf("clone failure: %+v", ps)
	}
}

func TestSystemEnv(t *testing.T) {
	env := System()
	sh, err := env.LookPath("sh")
	if err != nil {
		t.Skip(err)
	}
	if out, err := env.Run(t.TempDir(), sh, "-c", "echo prompt=$GIT_TERMINAL_PROMPT; echo oops >&2; exit 3"); err == nil || !strings.Contains(out, "prompt=0") || !strings.Contains(out, "oops") {
		t.Fatalf("run: %q %v", out, err)
	}
	if out, err := env.Run(t.TempDir(), sh, "-c", "head -c 9000 /dev/zero | tr '\\0' x"); err != nil || len(out) > maxOutput+10 {
		t.Fatalf("truncate: %d %v", len(out), err)
	}
	// 超时后，仍持有输出管道的子进程（如 git 派生的 ssh）不让 Run 一直等待。
	defer func(d time.Duration) { timeout = d }(timeout)
	timeout = 200 * time.Millisecond
	start := time.Now()
	if out, err := env.Run(t.TempDir(), sh, "-c", "sleep 30 & sleep 30"); err == nil || !strings.Contains(err.Error(), "没有结束") {
		t.Fatalf("timeout: %q %v", out, err)
	}
	if d := time.Since(start); d > WaitDelay+5*time.Second {
		t.Fatalf("Run returned after %v", d)
	}
}

func TestTruncateKeepsUTF8(t *testing.T) {
	if s := truncate(strings.Repeat("中", maxOutput)); !utf8.ValidString(s) || len(s) > maxOutput+len("…") {
		t.Fatalf("truncate: %d bytes, valid %v", len(s), utf8.ValidString(s))
	}
}

func TestRemove(t *testing.T) {
	root := newWorkspace(t)
	file := filepath.Join(t.TempDir(), "pack.yaml")
	write(t, file, housePack)
	if _, ps := Install(noGit, root, file, ""); ps != nil {
		t.Fatal(ps)
	}
	write(t, filepath.Join(root, ".writing-anything/packs/office.yaml"), "schema_version: 1\nid: office\ntitle: 手写\n")

	if _, ps := Remove(root, "house", []string{"default", "house"}); !slices.Equal(codes(ps), []string{"pack.in-use"}) {
		t.Fatalf("in use: %+v", ps)
	}
	for _, id := range []string{"office", "default", "nope"} {
		if changed, ps := Remove(root, id, nil); changed != nil || !slices.Equal(codes(ps), []string{"pack.not-installed"}) {
			t.Fatalf("%s: %v %+v", id, changed, ps)
		}
	}
	if _, err := os.Stat(filepath.Join(root, ".writing-anything/packs/office.yaml")); err != nil {
		t.Fatal("handwritten pack removed")
	}
	changed, ps := Remove(root, "house", nil)
	if ps != nil || !slices.Equal(changed, []string{".writing-anything/packs/house.yaml", LockPath}) {
		t.Fatalf("remove: %v %+v", changed, ps)
	}
	if _, err := os.Stat(filepath.Join(root, ".writing-anything/packs/house.yaml")); err == nil {
		t.Fatal("pack file kept")
	}
	if lock, _ := ReadLock(root); len(lock.Entries) != 0 {
		t.Fatalf("lock = %+v", lock.Entries)
	}

	// 文件已缺失时只移除条目。
	if _, ps := Install(noGit, root, file, ""); ps != nil {
		t.Fatal(ps)
	}
	os.Remove(filepath.Join(root, ".writing-anything/packs/house.yaml"))
	if changed, ps := Remove(root, "house", nil); ps != nil || !slices.Equal(changed, []string{LockPath}) {
		t.Fatalf("missing file: %v %+v", changed, ps)
	}
}

func TestCheckLock(t *testing.T) {
	root := newWorkspace(t)
	if checked, ps := Check(root); checked != nil || ps != nil {
		t.Fatalf("no lock: %v %+v", checked, ps)
	}
	file := filepath.Join(t.TempDir(), "pack.yaml")
	write(t, file, housePack)
	if _, ps := Install(noGit, root, file, ""); ps != nil {
		t.Fatal(ps)
	}
	installed := filepath.Join(root, ".writing-anything/packs/house.yaml")
	write(t, installed, housePack+"description: 改过\n")
	if _, ps := Check(root); !slices.Equal(codes(ps), []string{"pack.lock-mismatch"}) || ps[0].Path != ".writing-anything/packs/house.yaml" {
		t.Fatalf("modified: %+v", ps)
	}
	if lock, _ := ReadLock(root); Status(root, lock.Entries[0]) != "modified" {
		t.Fatal("status != modified")
	}
	os.Remove(installed)
	if _, ps := Check(root); !slices.Equal(codes(ps), []string{"pack.lock-missing-file"}) {
		t.Fatalf("missing: %+v", ps)
	}

	h := sum("x")
	lockFile := filepath.Join(root, LockPath)
	write(t, lockFile, "schema_version: 1\npacks:\n  - {id: a, source: {path: a}, sha256: "+h+"}\n  - {id: a, source: {path: b}, sha256: "+h+"}\n")
	if _, ps := Check(root); !slices.Equal(codes(ps), []string{"artifact.duplicate-id"}) || ps[0].Field != "packs[1].id" {
		t.Fatalf("duplicate: %+v", ps)
	}
	if ps := CheckLockArtifact(root); len(ps) != 1 {
		t.Fatalf("artifact: %+v", ps)
	}
	write(t, lockFile, "schema_version: 1\npacks:\n  - {id: a, source: {path: a}, sha256: xyz}\n")
	if _, ps := Check(root); !slices.Equal(codes(ps), []string{"artifact.schema-violation"}) || ps[0].Field != "packs[0].sha256" {
		t.Fatalf("schema: %+v", ps)
	}
	write(t, lockFile, "packs: [")
	if _, ps := ReadLock(root); !slices.Equal(codes(ps), []string{"artifact.parse-error"}) {
		t.Fatalf("parse: %+v", ps)
	}
	if runtime.GOOS != "windows" {
		os.Remove(lockFile)
		if err := os.Symlink(file, lockFile); err != nil {
			t.Fatal(err)
		}
		if _, ps := ReadLock(root); !slices.Equal(codes(ps), []string{"artifact.not-found"}) {
			t.Fatalf("symlink: %+v", ps)
		}
	}
}

func TestLockEditsPreserveComments(t *testing.T) {
	root := newWorkspace(t)
	h := sum(housePack)
	write(t, filepath.Join(root, LockPath), "# 团队约定：只用 wa-cli 修改\nschema_version: 1\npacks:\n  # 品牌规范\n  - id: zeta\n    source: {path: z}\n    sha256: "+h+"\n")
	write(t, filepath.Join(root, ".writing-anything/packs/zeta.yaml"), housePack)
	file := filepath.Join(t.TempDir(), "pack.yaml")
	write(t, file, housePack)
	if _, ps := Install(noGit, root, file, ""); ps != nil {
		t.Fatal(ps)
	}
	got := read(t, filepath.Join(root, LockPath))
	if !strings.Contains(got, "# 团队约定：只用 wa-cli 修改") || !strings.Contains(got, "# 品牌规范") || strings.Index(got, "id: house") > strings.Index(got, "id: zeta") {
		t.Fatalf("lock = %s", got)
	}

	write(t, filepath.Join(root, LockPath), "schema_version: 1\npacks:\n  - &e {id: house, source: {path: z}, sha256: "+h+"}\n")
	if _, ps := Install(noGit, root, file, ""); !slices.Equal(codes(ps), []string{"pack.anchored-value"}) {
		t.Fatalf("anchored entry: %+v", ps)
	}
	os.Remove(filepath.Join(root, ".writing-anything/packs/house.yaml"))
	write(t, filepath.Join(root, LockPath), "schema_version: 1\npacks: &p []\n")
	if _, ps := Install(noGit, root, file, ""); !slices.Equal(codes(ps), []string{"pack.anchored-value"}) {
		t.Fatalf("anchored seq: %+v", ps)
	}
	write(t, filepath.Join(root, LockPath), "base: &b {schema_version: 1}\n<<: *b\npacks: []\n")
	if _, ps := Install(noGit, root, file, ""); ps == nil {
		t.Fatal("merge key accepted")
	}
	// 移除路径同样不改带锚点的条目。
	write(t, filepath.Join(root, LockPath), "schema_version: 1\npacks:\n  - &e {id: house, source: {path: z}, sha256: "+h+"}\n")
	if _, ps := Remove(root, "house", nil); !slices.Equal(codes(ps), []string{"pack.anchored-value"}) {
		t.Fatalf("anchored remove: %+v", ps)
	}
}

func TestSymlinkedWorkspaceDir(t *testing.T) {
	if runtime.GOOS == "windows" {
		t.Skip("符号链接")
	}
	real := newWorkspace(t)
	root := t.TempDir()
	if err := os.Symlink(filepath.Join(real, workspace.Dir), filepath.Join(root, workspace.Dir)); err != nil {
		t.Fatal(err)
	}
	file := filepath.Join(t.TempDir(), "pack.yaml")
	write(t, file, housePack)
	if _, ps := Install(noGit, root, file, ""); !slices.Equal(codes(ps), []string{"workspace.write-failed"}) || ps[0].Path != workspace.Dir {
		t.Fatalf("got %+v", ps)
	}
	if _, err := os.Stat(filepath.Join(real, workspace.PacksPath)); err == nil {
		t.Fatal("wrote through symlinked workspace")
	}
}

func TestSymlinkedPacksDir(t *testing.T) {
	if runtime.GOOS == "windows" {
		t.Skip("符号链接")
	}
	root := newWorkspace(t)
	outside := t.TempDir()
	if err := os.Symlink(outside, filepath.Join(root, workspace.PacksPath)); err != nil {
		t.Fatal(err)
	}
	file := filepath.Join(t.TempDir(), "pack.yaml")
	write(t, file, housePack)
	if _, ps := Install(noGit, root, file, ""); !slices.Equal(codes(ps), []string{"workspace.write-failed"}) {
		t.Fatalf("got %+v", ps)
	}
	if entries, _ := os.ReadDir(outside); len(entries) != 0 {
		t.Fatal("wrote through symlink")
	}
}

func TestList(t *testing.T) {
	root := newWorkspace(t)
	if items, ps := List(root); ps != nil || len(items) != 1 || items[0].Status != StatusBuiltin || items[0].Title == nil || items[0].Path != nil {
		t.Fatalf("fresh: %+v %+v", items, ps)
	}
	file := filepath.Join(t.TempDir(), "pack.yaml")
	write(t, file, housePack)
	if _, ps := Install(noGit, root, file, ""); ps != nil {
		t.Fatal(ps)
	}
	write(t, filepath.Join(root, ".writing-anything/packs/office.yaml"), "schema_version: 1\nid: office\ntitle: 手写\n")
	write(t, filepath.Join(root, ".writing-anything/packs/.draft.yaml"), "x")
	write(t, filepath.Join(root, ".writing-anything/packs/broken.yaml"), "id: [")
	items, ps := List(root)
	var got []string
	for _, it := range items {
		got = append(got, it.ID+":"+it.Status)
	}
	if ps != nil || !slices.Equal(got, []string{"default:builtin", "broken:local", "house:installed", "office:local"}) || items[1].Title != nil || items[2].Lock == nil {
		t.Fatalf("list: %v %+v", got, ps)
	}
	os.Remove(filepath.Join(root, ".writing-anything/packs/house.yaml"))
	items, ps = List(root)
	if !slices.Equal(codes(ps), []string{"pack.lock-missing-file"}) || ps[0].Severity != workspace.SeverityWarning || items[3].ID != "office" || items[2].Status != "missing" {
		t.Fatalf("missing: %+v %+v", items, ps)
	}
	write(t, filepath.Join(root, LockPath), "packs: [")
	if items, ps := List(root); items != nil || len(ps) != 1 {
		t.Fatalf("broken lock: %+v %+v", items, ps)
	}
}
