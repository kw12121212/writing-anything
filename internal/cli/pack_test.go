package cli

import (
	"encoding/json"
	"os"
	"os/exec"
	"path/filepath"
	"runtime"
	"slices"
	"strings"
	"testing"
)

const testPack = "schema_version: 1\nid: house\ntitle: 本团队规范\n"

type packOut struct {
	result
	Pack  *packInstallView `json:"pack"`
	Packs []packItemView   `json:"packs"`
}

func runPack(t *testing.T, args ...string) (int, packOut) {
	t.Helper()
	code, stdout, _ := run(t, append([]string{"pack"}, append(args, "--json")...)...)
	var out packOut
	if err := json.Unmarshal([]byte(stdout), &out); err != nil {
		t.Fatalf("%v: %v; %s", args, err, stdout)
	}
	return code, out
}

// packSource 在项目目录 dir 旁边创建含 pack.yaml 的目录，返回相对 dir 的路径。
func packSource(t *testing.T, dir, name, content string) string {
	t.Helper()
	src := filepath.Join(filepath.Dir(dir), filepath.Base(dir)+"-"+name)
	if err := os.MkdirAll(src, 0o755); err != nil {
		t.Fatal(err)
	}
	writeFile(t, filepath.Join(src, "pack.yaml"), content)
	return "../" + filepath.Base(src)
}

func TestPackInstallFromPath(t *testing.T) {
	dir := newWorkspace(t)
	src := packSource(t, dir, "house", testPack)
	config := readFile(t, configPath(dir))
	code, out := runPack(t, "install", src, "--dir", dir)
	p := out.Pack
	if code != 0 || p == nil || p.ID != "house" || p.Status != "installed" || p.Source.Path != src || p.Commit != nil || len(p.SHA256) != 64 ||
		!slices.Equal(out.ChangedPaths, []string{".writing-anything/packs/house.yaml", ".writing-anything/packs.lock.yaml"}) {
		t.Fatalf("install: %d %+v %+v", code, out, p)
	}
	if readFile(t, filepath.Join(dir, ".writing-anything/packs/house.yaml")) != testPack || readFile(t, configPath(dir)) != config {
		t.Fatal("pack not copied byte-for-byte, or config.yaml changed")
	}
	if code, out := runPack(t, "install", src, "--dir", dir); code != 0 || out.Pack.Status != "unchanged" || len(out.ChangedPaths) != 0 {
		t.Fatalf("again: %d %+v", code, out)
	}
	if code, stdout, _ := run(t, "pack", "install", src, "--dir", dir); code != 0 || !strings.Contains(stdout, "没有变化") || !strings.Contains(stdout, "wa-cli config set packs house") {
		t.Fatalf("text: %d %q", code, stdout)
	}
	// 锁定文件能单独校验，整体校验通过。
	if code, res := runJSON(t, "validate", ".writing-anything/packs.lock.yaml", "--dir", dir, "--json"); code != 0 || res["valid"] != true {
		t.Fatalf("validate lock: %d %v", code, res)
	}
	if code, _ := runJSON(t, "validate", "--dir", dir, "--json"); code != 0 {
		t.Fatalf("validate: %d", code)
	}
	// 启用后与手写的工作区规范包一样参与合并。
	run(t, "config", "set", "packs", "house", "--dir", dir)
	if code, res := runJSON(t, "config", "resolved", "--dir", dir, "--json"); code != 0 || !strings.Contains(mustJSON(t, res["resolved"]), `"id":"house"`) {
		t.Fatalf("resolved: %d %v", code, res)
	}
}

func mustJSON(t *testing.T, v any) string {
	t.Helper()
	data, err := json.Marshal(v)
	if err != nil {
		t.Fatal(err)
	}
	return string(data)
}

func TestPackInstallRejects(t *testing.T) {
	dir := newWorkspace(t)
	bad := packSource(t, dir, "bad", "schema_version: 1\nid: bad\ntitle: x\nrules:\n  r1:\n    type: length-range\n    params: {script: rm}\n")
	code, out := runPack(t, "install", bad, "--dir", dir)
	if d := diag(t, out.result, "artifact.executable-content"); code != 1 || out.Pack != nil || len(out.ChangedPaths) != 0 || d.Path != "" ||
		!strings.HasPrefix(d.Message, "规范包来源 "+bad) || !slices.Equal(d.Next, []string{"wa-cli help pack install --json"}) {
		t.Fatalf("executable: %d %+v", code, out)
	}
	if _, err := os.Stat(filepath.Join(dir, ".writing-anything/packs")); err == nil {
		t.Fatal("packs/ created")
	}

	builtin := packSource(t, dir, "default", "schema_version: 1\nid: default\ntitle: x\n")
	if code, out := runPack(t, "install", builtin, "--dir", dir); code != 1 || diag(t, out.result, "pack.id-conflict").Path != "" {
		t.Fatalf("builtin id: %d %+v", code, out)
	}
	code, out = runPack(t, "install", "acme/house", "--dir", dir)
	if d := diag(t, out.result, "pack.registry-unsupported"); code != 1 || !strings.Contains(d.Message, "git 地址") {
		t.Fatalf("registry: %d %+v", code, out)
	}
	for _, args := range [][]string{{"install", "../x", "--ref", "v1"}, {"install", "https://example.com/x.git", "--ref=-c"}} {
		if code, out := runPack(t, append(args, "--dir", dir)...); code != 1 || !slices.Equal(diag(t, out.result, "input.invalid-value").Next, []string{"wa-cli help pack install --json"}) {
			t.Fatalf("%v: %d %+v", args, code, out)
		}
	}

	if err := os.MkdirAll(filepath.Join(dir, ".writing-anything/packs"), 0o755); err != nil {
		t.Fatal(err)
	}
	writeFile(t, filepath.Join(dir, ".writing-anything/packs/house.yaml"), "schema_version: 1\nid: house\ntitle: 手写\n")
	code, out = runPack(t, "install", packSource(t, dir, "house", testPack), "--dir", dir)
	if d := diag(t, out.result, "pack.exists"); code != 1 || d.Path != ".writing-anything/packs/house.yaml" || !hasNext(d, "wa-cli pack list") {
		t.Fatalf("handwritten: %d %+v", code, out)
	}
	if code, out := runPack(t, "install", "x", "--dir", t.TempDir()); code != 1 || len(diag(t, out.result, "workspace.not-found").Next) == 0 {
		t.Fatalf("no workspace: %d %+v", code, out)
	}
}

func TestPackListAndValidate(t *testing.T) {
	dir := newWorkspace(t)
	writeFile(t, configPath(dir), strings.Replace(readFile(t, configPath(dir)), "packs: []", "packs: [house]", 1))
	runPack(t, "install", packSource(t, dir, "house", testPack), "--dir", dir)
	runPack(t, "install", packSource(t, dir, "brand", "schema_version: 1\nid: brand\ntitle: 品牌\n"), "--dir", dir)
	writeFile(t, filepath.Join(dir, ".writing-anything/packs/office.yaml"), "schema_version: 1\nid: office\ntitle: 手写\n")
	brand := filepath.Join(dir, ".writing-anything/packs/brand.yaml")
	writeFile(t, brand, "schema_version: 1\nid: brand\ntitle: 改过\n")

	code, out := runPack(t, "list", "--dir", dir)
	var got []string
	for _, p := range out.Packs {
		got = append(got, p.ID+":"+p.Status)
	}
	if code != 0 || !out.Valid || !slices.Equal(got, []string{"default:builtin", "brand:modified", "house:installed", "office:local"}) || len(out.ChangedPaths) != 0 {
		t.Fatalf("list: %d %v %+v", code, got, out)
	}
	if out.Packs[0].Enabled || !out.Packs[2].Enabled || out.Packs[2].Lock == nil || out.Packs[3].Lock != nil || out.Packs[0].Path != nil {
		t.Fatalf("fields: %+v", out.Packs)
	}
	if d := diag(t, out.result, "pack.lock-mismatch"); d.Severity != "warning" {
		t.Fatalf("list warning: %+v", d)
	}

	code, res := runPack(t, "remove", "brand", "--dir", dir) // 先确认 validate 的 next 指向的命令可以运行
	if code != 0 {
		t.Fatalf("remove modified: %d %+v", code, res)
	}
	runPack(t, "install", packSource(t, dir, "brand", "schema_version: 1\nid: brand\ntitle: 品牌\n"), "--dir", dir)
	writeFile(t, brand, "schema_version: 1\nid: brand\ntitle: 改过\n")
	vcode, vout := runValidateJSON(t, dir)
	d := diag(t, vout, "pack.lock-mismatch")
	if vcode != 1 || d.Path != ".writing-anything/packs/brand.yaml" || !hasNext(d, "wa-cli pack install ../") || !hasNext(d, "wa-cli pack remove brand") {
		t.Fatalf("validate modified: %d %+v", vcode, vout)
	}
	// 按 next 重新安装后通过校验。
	argv := strings.Fields(d.Next[0])[1:]
	if code, _ := runJSON(t, argv...); code != 0 {
		t.Fatalf("reinstall %v: %d", argv, code)
	}
	if vcode, vout := runValidateJSON(t, dir); vcode != 0 {
		t.Fatalf("after reinstall: %+v", vout)
	}
	os.Remove(brand)
	if vcode, vout := runValidateJSON(t, dir); vcode != 1 || len(diag(t, vout, "pack.lock-missing-file").Next) != 2 {
		t.Fatalf("missing: %+v", vout)
	}
	code, out = runPack(t, "list", "--dir", dir)
	if code != 0 || out.Packs[1].ID != "brand" || out.Packs[1].Status != "missing" || out.Packs[1].Title != nil {
		t.Fatalf("list missing: %+v", out.Packs)
	}
	if code, stdout, _ := run(t, "pack", "list", "--dir", dir); code != 0 || !strings.Contains(stdout, "house：本团队规范（workspace，installed，已启用） .writing-anything/packs/house.yaml；锁定来源 ../") ||
		!strings.Contains(stdout, "brand：（无法加载）（workspace，missing）") || !strings.Contains(stdout, "default：默认规范包（builtin，builtin）\n") {
		t.Fatalf("text: %q", stdout)
	}

	h := strings.Repeat("a", 64)
	writeFile(t, filepath.Join(dir, ".writing-anything/packs.lock.yaml"), "schema_version: 1\npacks:\n  - {id: a, source: {path: a}, sha256: "+h+"}\n  - {id: a, source: {path: b}, sha256: "+h+"}\n")
	code, stdout, _ := run(t, "validate", ".writing-anything/packs.lock.yaml", "--dir", dir, "--json")
	var dup result
	json.Unmarshal([]byte(stdout), &dup)
	if d := diag(t, dup, "artifact.duplicate-id"); code != 1 || d.Field != "packs[1].id" || d.Path != ".writing-anything/packs.lock.yaml" {
		t.Fatalf("duplicate: %d %+v", code, dup)
	}
	writeFile(t, filepath.Join(dir, ".writing-anything/packs.lock.yaml"), "schema_version: 1\npacks: [")
	if code, out := runPack(t, "list", "--dir", dir); code != 1 || out.Valid {
		t.Fatalf("broken lock: %d %+v", code, out)
	}
}

func runValidateJSON(t *testing.T, dir string) (int, result) {
	t.Helper()
	code, stdout, _ := run(t, "validate", "--dir", dir, "--json")
	var r result
	if err := json.Unmarshal([]byte(stdout), &r); err != nil {
		t.Fatal(err)
	}
	return code, r
}

func TestPackRemove(t *testing.T) {
	dir := newWorkspace(t)
	runPack(t, "install", packSource(t, dir, "house", testPack), "--dir", dir)
	run(t, "config", "set", "packs", "default,house", "--dir", dir)
	code, out := runPack(t, "remove", "house", "--dir", dir)
	if d := diag(t, out.result, "pack.in-use"); code != 1 || !slices.Equal(d.Next, []string{"wa-cli config set packs default --dir " + dir + " --json"}) || len(out.ChangedPaths) != 0 {
		t.Fatalf("in use: %d %+v", code, out)
	}
	run(t, "config", "set", "packs", "house", "--dir", dir)
	if code, out := runPack(t, "remove", "house", "--dir", dir); code != 1 || !strings.Contains(diag(t, out.result, "pack.in-use").Message, "隐式启用内置 default") {
		t.Fatalf("in use alone: %d %+v", code, out)
	}
	run(t, "config", "set", "packs", "", "--dir", dir)
	code, out = runPack(t, "remove", "house", "--dir", dir)
	if code != 0 || !slices.Equal(out.ChangedPaths, []string{".writing-anything/packs/house.yaml", ".writing-anything/packs.lock.yaml"}) {
		t.Fatalf("remove: %d %+v", code, out)
	}
	if code, stdout, _ := run(t, "pack", "remove", "house", "--dir", dir); code != 1 || stdout != "" {
		t.Fatalf("again: %d %q", code, stdout)
	}
	if code, out := runPack(t, "remove", "default", "--dir", dir); code != 1 || !hasNext(diag(t, out.result, "pack.not-installed"), "wa-cli pack list") {
		t.Fatalf("builtin: %d %+v", code, out)
	}
	if code, out := runPack(t, "remove", "../x", "--dir", dir); code != 1 || !slices.Equal(diag(t, out.result, "input.invalid-value").Next, []string{"wa-cli help pack remove --json"}) {
		t.Fatalf("bad id: %d %+v", code, out)
	}
	writeFile(t, configPath(dir), "packs: [")
	if code, _ := runPack(t, "remove", "house", "--dir", dir); code != 1 {
		t.Fatalf("broken config: %d", code)
	}
}

// fakeGit 在 PATH 中放一个假 git：记录工作目录、参数与 GIT_TERMINAL_PROMPT，clone 时把错误写在标准输出并失败。
func fakeGit(t *testing.T) string {
	t.Helper()
	if runtime.GOOS == "windows" {
		t.Skip("假 git 是 shell 脚本")
	}
	bin := t.TempDir()
	// 归一化 TMPDIR 拼写：被测代码的临时目录取自 TMPDIR 原文（macOS 上为 /var/...），而脚本的 pwd
	// 经 getcwd 返回真实路径拼写（/private/var/...，/var 是指向它的符号链接）；两侧拼写一致断言才可靠。
	if resolved, err := filepath.EvalSymlinks(os.TempDir()); err == nil {
		t.Setenv("TMPDIR", resolved)
	}
	log := filepath.Join(bin, "log")
	script := "#!/bin/sh\necho \"$(pwd)|$GIT_TERMINAL_PROMPT|$*\" >> " + log + "\necho 'fatal: repository not found'\nexit 128\n"
	if err := os.WriteFile(filepath.Join(bin, "git"), []byte(script), 0o755); err != nil {
		t.Fatal(err)
	}
	t.Setenv("PATH", bin)
	return log
}

func TestPackInstallFakeGit(t *testing.T) {
	dir := newWorkspace(t)
	log := fakeGit(t)
	code, out := runPack(t, "install", "https://example.com/acme/house.git", "--ref", "v1", "--dir", dir)
	if d := diag(t, out.result, "pack.git-failed"); code != 1 || !strings.Contains(d.Message, "repository not found") || len(out.ChangedPaths) != 0 {
		t.Fatalf("got %d %+v", code, out)
	}
	line := strings.TrimSpace(readFile(t, log))
	parts := strings.SplitN(line, "|", 3)
	if len(parts) != 3 || !strings.Contains(parts[0], "wa-pack-") || parts[1] != "0" ||
		!strings.HasPrefix(parts[2], "-c core.hooksPath=") || !strings.HasSuffix(parts[2], "-c protocol.ext.allow=never clone --quiet --no-checkout -- https://example.com/acme/house.git "+filepath.Join(parts[0], "repo")) {
		t.Fatalf("command line = %q", line)
	}
	t.Setenv("PATH", t.TempDir())
	if code, out := runPack(t, "install", "git@example.com:acme/house.git", "--dir", dir); code != 1 || len(diag(t, out.result, "pack.git-unavailable").Next) != 1 {
		t.Fatalf("no git: %d %+v", code, out)
	}
}

func TestPackInstallFromGit(t *testing.T) {
	if _, err := exec.LookPath("git"); err != nil {
		t.Skip("没有 git")
	}
	repo := t.TempDir()
	gitEnv := append(os.Environ(), "GIT_AUTHOR_NAME=t", "GIT_AUTHOR_EMAIL=t@x", "GIT_COMMITTER_NAME=t", "GIT_COMMITTER_EMAIL=t@x")
	for _, args := range [][]string{{"init", "--quiet"}, {"add", "pack.yaml"}, {"commit", "--quiet", "-m", "v1"}, {"tag", "v1"}} {
		if args[0] == "add" {
			writeFile(t, filepath.Join(repo, "pack.yaml"), testPack)
		}
		cmd := exec.Command("git", args...)
		cmd.Dir, cmd.Env = repo, gitEnv
		if out, err := cmd.CombinedOutput(); err != nil {
			t.Fatalf("git %v: %v %s", args, err, out)
		}
	}
	commit, _ := exec.Command("git", "-C", repo, "rev-parse", "HEAD").Output()
	url := "file://" + filepath.ToSlash(repo)
	dir := newWorkspace(t)
	code, out := runPack(t, "install", url, "--ref", "v1", "--dir", dir)
	if p := out.Pack; code != 0 || p.Source.Git != url || p.Source.Ref != "v1" || p.Commit == nil || *p.Commit != strings.TrimSpace(string(commit)) {
		t.Fatalf("git: %d %+v %+v", code, out, out.Pack)
	}
	lock := readFile(t, filepath.Join(dir, ".writing-anything/packs.lock.yaml"))
	if !strings.Contains(lock, "commit: "+strings.TrimSpace(string(commit))) || !strings.Contains(lock, "ref: v1") {
		t.Fatalf("lock = %s", lock)
	}
	writeFile(t, filepath.Join(dir, ".writing-anything/packs/house.yaml"), testPack+"description: x\n")
	_, vout := runValidateJSON(t, dir)
	if d := diag(t, vout, "pack.lock-mismatch"); !hasNext(d, "wa-cli pack install "+url+" --ref "+strings.TrimSpace(string(commit))) {
		t.Fatalf("next: %+v", d)
	}
}
