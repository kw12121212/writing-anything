// Package packinstall 安装、锁定与移除规范包（Pack）：从 git 地址或本地路径取得规范包文件，按工作区规范包的全部检查校验后
// 写入 .writing-anything/packs/<id>.yaml，并在锁定文件（Lock File）.writing-anything/packs.lock.yaml 中记录来源、git 提交与
// 内容的 SHA-256。git 的查找与执行经 Env 注入，测试可以替换；本包只返回结构化的结果与问题，由命令层转换为诊断（Diagnostic）。
package packinstall

import (
	"bytes"
	"context"
	"crypto/sha256"
	"encoding/hex"
	"errors"
	"fmt"
	"io"
	"io/fs"
	"os"
	"os/exec"
	"path/filepath"
	"regexp"
	"slices"
	"strings"
	"time"

	"github.com/kw12121212/writing-anything/internal/catalog"
	"github.com/kw12121212/writing-anything/internal/schema"
	"github.com/kw12121212/writing-anything/internal/workspace"
	"go.yaml.in/yaml/v3"
)

// 安装与锁定的错误码（Error Code）。已发布的错误码不得改名或复用。
const (
	CodeSourceNotFound      = "pack.source-not-found"
	CodeInvalidSource       = "pack.invalid-source"
	CodeRegistryUnsupported = "pack.registry-unsupported"
	CodeGitUnavailable      = "pack.git-unavailable"
	CodeGitFailed           = "pack.git-failed"
	CodeExists              = "pack.exists"
	CodeNotInstalled        = "pack.not-installed"
	CodeInUse               = "pack.in-use"
	CodeLockMismatch        = "pack.lock-mismatch"
	CodeLockMissingFile     = "pack.lock-missing-file"
	CodeAnchoredValue       = "pack.anchored-value"
)

// LockPath 是锁定文件相对项目目录的路径；不在 packs/ 中，避免被当成规范包。
const LockPath = workspace.Dir + "/packs.lock.yaml"

// SourceFile 是目录或 git 仓库中规范包文件的文件名。
const SourceFile = "pack.yaml"

// MaxSize 是规范包文件的字节数上限。
const MaxSize = 1 << 20

// 安装结果的状态。
const (
	StatusInstalled = "installed"
	StatusUpdated   = "updated"
	StatusUnchanged = "unchanged"
)

// Timeout 是一次 git 命令的时长上限；拉取卡住（如等待网络或凭据）时不让 wa-cli 一直等待。
const Timeout = 2 * time.Minute

// WaitDelay 是超时杀掉 git 后等待输出管道关闭的上限：git 派生的 ssh、git-remote-https 等子进程仍持有管道时，不让 Wait 一直等下去。
const WaitDelay = 5 * time.Second

// timeout 是 System 使用的时长上限，测试可以缩短。
var timeout = Timeout

// maxOutput 是诊断中保留的 git 输出的字节数上限。
const maxOutput = 4 << 10

// Source 是规范包的来源：git 地址与可选的 ref，或本地路径。
type Source struct {
	Git  string `json:"git,omitempty" yaml:"git,omitempty"`
	Ref  string `json:"ref,omitempty" yaml:"ref,omitempty"`
	Path string `json:"path,omitempty" yaml:"path,omitempty"`
}

// Entry 是锁定文件中的一个条目。Commit 只有 git 来源有。
type Entry struct {
	ID     string `json:"id" yaml:"id"`
	Source Source `json:"source" yaml:"source"`
	Commit string `json:"commit,omitempty" yaml:"commit,omitempty"`
	SHA256 string `json:"sha256" yaml:"sha256"`
}

// Lock 是已读取的锁定文件；文件不存在时 Entries 为空、doc 为 nil。
type Lock struct {
	Entries []Entry
	doc     *yaml.Node
}

// Find 返回 id 的锁定条目；没有时为 nil。
func (l *Lock) Find(id string) *Entry {
	for i := range l.Entries {
		if l.Entries[i].ID == id {
			return &l.Entries[i]
		}
	}
	return nil
}

// Env 是安装时与外部环境的交互：查找可执行文件，在目录 dir 中运行命令（返回合并的标准输出与标准错误，以及错误）。
type Env struct {
	LookPath func(name string) (string, error)
	Run      func(dir, name string, args ...string) (output string, err error)
}

// System 返回使用真实 PATH 与进程的 Env：参数不经 shell 传递，不连接 wa-cli 的标准输入与输出，关闭 git 的交互提示，有时长上限。
func System() Env {
	return Env{
		LookPath: exec.LookPath,
		Run: func(dir, name string, args ...string) (string, error) {
			ctx, cancel := context.WithTimeout(context.Background(), timeout)
			defer cancel()
			cmd := exec.CommandContext(ctx, name, args...)
			cmd.Dir, cmd.WaitDelay = dir, WaitDelay
			cmd.Env = append(os.Environ(), "GIT_TERMINAL_PROMPT=0")
			var out bytes.Buffer
			cmd.Stdout, cmd.Stderr = &out, &out
			err := cmd.Run()
			if ctx.Err() != nil {
				err = fmt.Errorf("超过 %v 没有结束", timeout)
			}
			return truncate(out.String()), err
		},
	}
}

func truncate(s string) string {
	if len(s) > maxOutput {
		return strings.ToValidUTF8(s[:maxOutput], "") + "…"
	}
	return s
}

var (
	gitURL      = regexp.MustCompile(`^(?:(?:https?|ssh|git|file)://|[A-Za-z0-9._-]+@[A-Za-z0-9.-]+:)`)
	registryRef = regexp.MustCompile(`^[a-z][a-z0-9]*(?:-[a-z0-9]+)*/[a-z][a-z0-9]*(?:-[a-z0-9]+)*$`)
	commitID    = regexp.MustCompile(`^[0-9a-f]{40}$`)
	validRef    = regexp.MustCompile(`^[^-\s]\S*$`)
)

// IsGit 报告来源参数 src 是否为 git 地址。
func IsGit(src string) bool { return gitURL.MatchString(src) }

// ValidRef 报告 ref 能否作为 git 的分支、标签或提交传给 git：不以 - 开头（不会被当成选项）且不含空白。
func ValidRef(ref string) bool { return validRef.MatchString(ref) }

// Result 是一次安装的结果。Changed 为写入的路径（相对项目目录）。
type Result struct {
	Entry   Entry
	Title   string
	Path    string
	Status  string
	Changed []string
}

func errorAt(code, path, msg string) workspace.Problem {
	return workspace.Problem{Code: code, Severity: workspace.SeverityError, Path: path, Message: msg}
}

// Install 从 src 安装规范包到项目目录 root 的工作区。ref 只用于 git 地址（调用方保证 ValidRef）。全部检查都在第一次写入前完成，
// 任何问题都不写入文件。
func Install(env Env, root, src, ref string) (*Result, []workspace.Problem) {
	var (
		data     []byte
		source   Source
		commit   string
		problems []workspace.Problem
	)
	label := "规范包来源 " + src
	if IsGit(src) {
		source = Source{Git: src, Ref: ref}
		data, commit, problems = fetchGit(env, src, ref, label)
	} else {
		data, source, problems = readLocal(root, src, label)
	}
	if problems != nil {
		return nil, problems
	}
	pack, problems := catalog.LoadPackData(data, label)
	if len(problems) > 0 {
		return nil, problems
	}
	lock, problems := ReadLock(root)
	if problems != nil {
		return nil, problems
	}
	target := pack.Path
	if p := checkDirs(root); p != nil {
		return nil, []workspace.Problem{*p}
	}
	entry := Entry{ID: pack.ID, Source: source, Commit: commit, SHA256: digest(data)}
	prev := lock.Find(pack.ID)
	cur, exists, err := workspace.ReadRegular(root, target)
	switch info, lerr := os.Lstat(abs(root, target)); {
	case err != nil:
		return nil, []workspace.Problem{errorAt(workspace.CodeReadFailed, target, "读取失败："+err.Error())}
	case lerr == nil && !exists:
		return nil, []workspace.Problem{errorAt(CodeExists, target, fmt.Sprintf("%s 已存在但不是普通文件（%v），不覆盖", target, info.Mode().Type()))}
	case exists && prev == nil:
		return nil, []workspace.Problem{errorAt(CodeExists, target, fmt.Sprintf("%s 是没有锁定条目的工作区规范包（可能是作者手写的），不覆盖；如要改用安装的版本，请先移走该文件", target))}
	}
	res := &Result{Entry: entry, Title: pack.Title, Path: target, Status: StatusInstalled}
	if prev != nil {
		res.Status = StatusUpdated
		if *prev == entry && exists && digest(cur) == entry.SHA256 {
			res.Status = StatusUnchanged
			return res, nil
		}
	}
	lockData, problems := lock.with(entry)
	if problems != nil {
		return nil, problems
	}
	if err := os.MkdirAll(abs(root, workspace.PacksPath), 0o755); err != nil {
		return nil, []workspace.Problem{errorAt(workspace.CodeWriteFailed, workspace.PacksPath, "创建目录失败："+err.Error())}
	}
	if !exists || !bytes.Equal(cur, data) {
		if err := workspace.WriteFile(root, target, data); err != nil {
			return nil, []workspace.Problem{errorAt(workspace.CodeWriteFailed, target, "写入失败："+err.Error())}
		}
		res.Changed = append(res.Changed, target)
	}
	if lockData != nil {
		if err := workspace.WriteFile(root, LockPath, lockData); err != nil {
			return res, []workspace.Problem{errorAt(workspace.CodeWriteFailed, LockPath, "写入失败："+err.Error())}
		}
		res.Changed = append(res.Changed, LockPath)
	}
	return res, nil
}

// Remove 删除已安装的规范包 id（调用方保证 id 格式）的文件与锁定条目；enabled 为 config.yaml 的 packs。返回变更的路径。
func Remove(root, id string, enabled []string) ([]string, []workspace.Problem) {
	lock, problems := ReadLock(root)
	if problems != nil {
		return nil, problems
	}
	target := catalog.PackPath(id)
	if lock.Find(id) == nil {
		why := "不存在"
		switch {
		case slices.Contains(catalog.BuiltinPacks(), id):
			why = "是内置规范包"
		case exists(root, target):
			why = "是没有锁定条目的工作区规范包（可能是作者手写的），wa-cli 不删除它"
		}
		return nil, []workspace.Problem{errorAt(CodeNotInstalled, "", fmt.Sprintf("规范包 %s 不是用 wa-cli pack install 安装的：%s", id, why))}
	}
	if slices.Contains(enabled, id) {
		return nil, []workspace.Problem{errorAt(CodeInUse, workspace.ConfigPath, fmt.Sprintf("规范包 %s 仍在 config.yaml 的 packs 中；请先从 packs 中去掉它再移除", id))}
	}
	lockData, problems := lock.without(id)
	if problems != nil {
		return nil, problems
	}
	if p := checkDirs(root); p != nil {
		return nil, []workspace.Problem{*p}
	}
	var changed []string
	switch err := os.Remove(abs(root, target)); {
	case err == nil:
		changed = append(changed, target)
	case !errors.Is(err, fs.ErrNotExist):
		return nil, []workspace.Problem{errorAt(workspace.CodeWriteFailed, target, "删除失败："+err.Error())}
	}
	if err := workspace.WriteFile(root, LockPath, lockData); err != nil {
		return changed, []workspace.Problem{errorAt(workspace.CodeWriteFailed, LockPath, "写入失败："+err.Error())}
	}
	return append(changed, LockPath), nil
}

// Status 是一个锁定条目对应文件的状态：installed（与锁定一致）、modified（内容不同）或 missing（文件不存在）。
func Status(root string, e Entry) string {
	data, ok, _ := workspace.ReadRegular(root, catalog.PackPath(e.ID))
	switch {
	case !ok:
		return "missing"
	case digest(data) != e.SHA256:
		return "modified"
	}
	return StatusInstalled
}

// Check 做整体校验中与锁定文件有关的部分：锁定文件合法，且每个锁定条目的文件存在、内容与锁定的 sha256 一致。
// 没有锁定文件时不检查。返回已校验的路径。
func Check(root string) (checked []string, problems []workspace.Problem) {
	if _, err := os.Lstat(abs(root, LockPath)); errors.Is(err, fs.ErrNotExist) {
		return nil, nil
	}
	lock, problems := ReadLock(root)
	if problems != nil {
		return []string{LockPath}, problems
	}
	return []string{LockPath}, VerifyEntries(root, lock)
}

// VerifyEntries 报告锁定条目与已安装文件不一致的问题（error 级），按条目顺序。
func VerifyEntries(root string, lock *Lock) []workspace.Problem {
	var out []workspace.Problem
	for _, e := range lock.Entries {
		target := catalog.PackPath(e.ID)
		switch Status(root, e) {
		case "missing":
			out = append(out, errorAt(CodeLockMissingFile, target, fmt.Sprintf("锁定文件记录了规范包 %s，但 %s 不存在或不是普通文件", e.ID, target)))
		case "modified":
			out = append(out, errorAt(CodeLockMismatch, target, fmt.Sprintf("内容的 SHA-256 与锁定文件记录的 %s 不同，安装后被修改过", e.SHA256)))
		}
	}
	return out
}

// CheckLockArtifact 对已通过 Schema 校验的锁定文件做领域检查：条目 id 不重复。
func CheckLockArtifact(root string) []workspace.Problem {
	_, ps := ReadLock(root)
	return ps
}

// ReadLock 读取并校验锁定文件；文件不存在时返回空的锁定。锁定文件不是普通文件、无法解析、不符合 Schema 或条目 id 重复时返回问题。
func ReadLock(root string) (*Lock, []workspace.Problem) {
	data, ok, err := workspace.ReadRegular(root, LockPath)
	switch info, lerr := os.Lstat(abs(root, LockPath)); {
	case err != nil:
		return nil, []workspace.Problem{errorAt(workspace.CodeReadFailed, LockPath, "读取失败："+err.Error())}
	case errors.Is(lerr, fs.ErrNotExist):
		return &Lock{Entries: []Entry{}}, nil
	case !ok:
		mode := "无法访问"
		if lerr == nil {
			mode = info.Mode().Type().String()
		}
		return nil, []workspace.Problem{errorAt(schema.CodeNotFound, LockPath, "锁定文件途经符号链接或不是普通文件（"+mode+"），按不存在处理")}
	}
	doc, p := schema.Parse(data)
	if p != nil {
		return nil, workspace.FromSchema(LockPath, []schema.Problem{*p})
	}
	return decodeLock(doc)
}

func decodeLock(doc *yaml.Node) (*Lock, []workspace.Problem) {
	if ps := schema.Named("pack-lock").ValidateNode(doc); len(ps) > 0 {
		return nil, workspace.FromSchema(LockPath, ps)
	}
	var f struct {
		Packs []Entry `json:"packs"`
	}
	catalog.Decode(doc, &f)
	l := &Lock{Entries: f.Packs, doc: doc}
	if l.Entries == nil {
		l.Entries = []Entry{}
	}
	seen := map[string]bool{}
	var out []workspace.Problem
	for i, e := range l.Entries {
		if seen[e.ID] {
			out = append(out, workspace.Problem{Code: catalog.CodeDuplicateID, Severity: workspace.SeverityError, Path: LockPath,
				Field: fmt.Sprintf("packs[%d].id", i), Message: "规范包 " + e.ID + " 的锁定条目重复"})
		}
		seen[e.ID] = true
	}
	if out != nil {
		return nil, out
	}
	return l, nil
}

// with 返回写入（替换或按 id 顺序插入）条目 e 后的锁定文件内容；内容不变时返回 nil。
func (l *Lock) with(e Entry) ([]byte, []workspace.Problem) {
	return l.edit(e.ID, func(seq *yaml.Node, at int) []workspace.Problem {
		var n yaml.Node
		if err := n.Encode(e); err != nil {
			return []workspace.Problem{errorAt(workspace.CodeWriteFailed, LockPath, "生成锁定条目失败："+err.Error())}
		}
		if at >= 0 {
			old := seq.Content[at]
			n.HeadComment, n.LineComment, n.FootComment = old.HeadComment, old.LineComment, old.FootComment
			seq.Content[at] = &n
			return nil
		}
		i := 0
		for i < len(seq.Content) && entryID(seq.Content[i]) < e.ID {
			i++
		}
		seq.Content = slices.Insert(seq.Content, i, &n)
		return nil
	})
}

// without 返回移除条目 id 后的锁定文件内容。
func (l *Lock) without(id string) ([]byte, []workspace.Problem) {
	return l.edit(id, func(seq *yaml.Node, at int) []workspace.Problem {
		seq.Content = slices.Delete(seq.Content, at, at+1)
		return nil
	})
}

// edit 在锁定文件的节点树上修改 packs 序列：change 收到序列与条目 id 的下标（没有时为 -1）。只改目标条目，保留其他条目与注释；
// 目标条目、序列或途经的映射带锚点、是别名或含合并键时不修改；写出前重新解析并校验（workspace-artifacts/structured-edits）。
func (l *Lock) edit(id string, change func(seq *yaml.Node, at int) []workspace.Problem) ([]byte, []workspace.Problem) {
	doc := l.doc
	if doc == nil || len(doc.Content) == 0 {
		root := &yaml.Node{Kind: yaml.MappingNode}
		root.Content = append(root.Content, workspace.Str("schema_version"), workspace.Int(1),
			workspace.Str("packs"), &yaml.Node{Kind: yaml.SequenceNode, Tag: "!!seq", Style: yaml.FlowStyle})
		doc = &yaml.Node{Kind: yaml.DocumentNode, HeadComment: "规范包锁定文件（Lock File）；请用 wa-cli pack install 与 wa-cli pack remove 修改。",
			Content: []*yaml.Node{root}}
	}
	var old []byte
	if l.doc != nil {
		old, _ = workspace.Encode(l.doc) // 修改是在节点树上原地进行的，先记下原来的编码结果
	}
	root := doc.Content[0]
	seq, ok := workspace.Lookup(root, "packs")
	anchored := func(field string) []workspace.Problem {
		return []workspace.Problem{{Code: CodeAnchoredValue, Severity: workspace.SeverityError, Path: LockPath, Field: field,
			Message: "锁定文件经由 YAML 锚点、别名或合并键共享内容，wa-cli 无法只修改一个条目；请先去掉锚点、别名与合并键"}}
	}
	if !workspace.Plain(root) || workspace.HasMerge(root) || !ok {
		return nil, anchored("packs")
	}
	at := workspace.IndexByID(seq, id)
	if at >= 0 && workspace.HasAnchor(seq.Content[at]) {
		return nil, anchored(fmt.Sprintf("packs[%d]", at))
	}
	if ps := change(seq, at); ps != nil {
		return nil, ps
	}
	if len(seq.Content) > 0 {
		workspace.BlockSequence(root, "packs")
	}
	data, err := workspace.Encode(doc)
	if err != nil {
		return nil, []workspace.Problem{errorAt(workspace.CodeWriteFailed, LockPath, "生成锁定文件失败："+err.Error())}
	}
	written, p := schema.Parse(data)
	if p != nil {
		return nil, workspace.FromSchema(LockPath, []schema.Problem{*p})
	}
	if _, ps := decodeLock(written); ps != nil {
		return nil, ps
	}
	if old != nil && bytes.Equal(old, data) {
		return nil, nil
	}
	return data, nil
}

// entryID 返回锁定条目节点的 id。
func entryID(n *yaml.Node) string {
	if v, _ := workspace.Lookup(n, "id"); v != nil {
		return v.Value
	}
	return ""
}

// readLocal 读取本地来源：相对 root 或绝对的 .yaml 文件，或含 pack.yaml 的目录。
func readLocal(root, src, label string) ([]byte, Source, []workspace.Problem) {
	p := src
	if !filepath.IsAbs(p) {
		p = filepath.Join(root, p)
	}
	info, err := os.Stat(p)
	switch {
	case errors.Is(err, fs.ErrNotExist) && registryRef.MatchString(src):
		return nil, Source{}, []workspace.Problem{errorAt(CodeRegistryUnsupported, "", fmt.Sprintf(
			"%s 形如 <owner>/<name>，这种写法保留给以后的中心化规范包仓库（Registry），目前尚未支持；请使用 git 地址（如 https://example.com/%s.git）或本地路径", src, src))}
	case err != nil:
		return nil, Source{}, []workspace.Problem{errorAt(CodeSourceNotFound, "", fmt.Sprintf("%s 既不是 git 地址，也不是存在的本地路径：%v", src, err))}
	}
	file := p
	switch {
	case info.IsDir():
		file = filepath.Join(p, SourceFile)
	case filepath.Ext(p) != ".yaml":
		return nil, Source{}, []workspace.Problem{errorAt(CodeInvalidSource, "", fmt.Sprintf("%s 不是 .yaml 文件；本地来源应为规范包的 .yaml 文件或含 %s 的目录", src, SourceFile))}
	}
	data, problems := readSource(file, label, false)
	if problems != nil {
		return nil, Source{}, problems
	}
	return data, Source{Path: recordedPath(root, src, p)}, nil
}

// recordedPath 返回锁定文件中记录的本地路径：参数 src 为绝对路径时用规整后的绝对路径；为相对路径时用规整后相对 root 的路径
// （无法表示为相对路径时用绝对路径）。
func recordedPath(root, src, p string) string {
	ap, err := filepath.Abs(p)
	if err != nil || filepath.IsAbs(src) {
		return filepath.ToSlash(filepath.Clean(p))
	}
	if ar, err := filepath.Abs(root); err == nil {
		if rel, err := filepath.Rel(ar, ap); err == nil {
			return filepath.ToSlash(rel)
		}
	}
	return filepath.ToSlash(ap)
}

// readSource 读取规范包文件 file：必须是普通文件（noSymlink 为 true 时自身也不能是符号链接），且不超过 MaxSize。
func readSource(file, label string, noSymlink bool) ([]byte, []workspace.Problem) {
	invalid := func(msg string) []workspace.Problem {
		return []workspace.Problem{{Code: CodeInvalidSource, Severity: workspace.SeverityError, Origin: label, Message: msg}}
	}
	stat := os.Stat
	if noSymlink {
		stat = os.Lstat
	}
	info, err := stat(file)
	switch {
	case errors.Is(err, fs.ErrNotExist):
		return nil, invalid("没有规范包文件 " + SourceFile)
	case err != nil:
		return nil, invalid("读取失败：" + err.Error())
	case !info.Mode().IsRegular():
		return nil, invalid(fmt.Sprintf("规范包文件不是普通文件（%v）", info.Mode().Type()))
	case info.Size() > MaxSize:
		return nil, invalid(fmt.Sprintf("规范包文件有 %d 字节，超过上限 %d 字节", info.Size(), MaxSize))
	}
	f, err := os.Open(file)
	if err != nil {
		return nil, invalid("读取失败：" + err.Error())
	}
	defer f.Close()
	data, err := io.ReadAll(io.LimitReader(f, MaxSize+1))
	switch {
	case err != nil:
		return nil, invalid("读取失败：" + err.Error())
	case len(data) > MaxSize:
		return nil, invalid(fmt.Sprintf("规范包文件超过上限 %d 字节", MaxSize))
	}
	return data, nil
}

// fetchGit 用 git 把 url 克隆到临时目录，检出 ref（为空时为默认分支）并读取仓库根目录的 pack.yaml。钩子被关闭、不递归子模块，
// 地址与选项之间用 -- 分隔；返回规范包内容与解析到的提交号。
func fetchGit(env Env, url, ref, label string) ([]byte, string, []workspace.Problem) {
	git, err := env.LookPath("git")
	if err != nil {
		return nil, "", []workspace.Problem{errorAt(CodeGitUnavailable, "", "找不到 git 可执行文件，无法从 git 地址安装；请安装 git，或改用本地路径")}
	}
	tmp, err := os.MkdirTemp("", "wa-pack-*")
	if err != nil {
		return nil, "", []workspace.Problem{errorAt(workspace.CodeWriteFailed, "", "创建临时目录失败："+err.Error())}
	}
	defer os.RemoveAll(tmp)
	hooks, repo := filepath.Join(tmp, "hooks"), filepath.Join(tmp, "repo")
	if err := os.Mkdir(hooks, 0o700); err != nil {
		return nil, "", []workspace.Problem{errorAt(workspace.CodeWriteFailed, "", "创建临时目录失败："+err.Error())}
	}
	run := func(args ...string) (string, error) {
		return env.Run(tmp, git, append([]string{"-c", "core.hooksPath=" + hooks, "-c", "protocol.ext.allow=never"}, args...)...)
	}
	failed := func(step, out string, err error) []workspace.Problem {
		return []workspace.Problem{{Code: CodeGitFailed, Severity: workspace.SeverityError, Origin: label,
			Message: fmt.Sprintf("%s 失败（%v）：%s", step, err, strings.TrimSpace(out))}}
	}
	if out, err := run("clone", "--quiet", "--no-checkout", "--", url, repo); err != nil {
		return nil, "", failed("git clone", out, err)
	}
	rev := "HEAD"
	if ref != "" {
		rev = ref
	}
	out, err := run("-C", repo, "rev-parse", "--verify", "--quiet", rev+"^{commit}")
	if err != nil && ref != "" {
		// 克隆只为默认分支建立本地分支，其他分支在 origin/ 下。
		out, err = run("-C", repo, "rev-parse", "--verify", "--quiet", "origin/"+ref+"^{commit}")
	}
	commit := strings.TrimSpace(out)
	if err == nil && !commitID.MatchString(commit) {
		err = errors.New("输出不是 40 位提交号")
	}
	if err != nil {
		return nil, "", failed("解析 "+rev, out, err)
	}
	if out, err := run("-C", repo, "checkout", "--quiet", "--detach", commit); err != nil {
		return nil, "", failed("git checkout", out, err)
	}
	data, problems := readSource(filepath.Join(repo, SourceFile), label, true)
	return data, commit, problems
}

// checkDirs 确认工作区目录与 packs/（存在时）都是真实目录，避免经符号链接写到项目目录之外
// （workspace-artifacts/validated-path-segments）。
func checkDirs(root string) *workspace.Problem {
	for _, rel := range []string{workspace.Dir, workspace.PacksPath} {
		info, err := os.Lstat(abs(root, rel))
		if errors.Is(err, fs.ErrNotExist) && rel == workspace.PacksPath {
			continue
		}
		if err != nil || !info.IsDir() {
			p := errorAt(workspace.CodeWriteFailed, rel, "不是真实目录（可能是符号链接），wa-cli 不经由它写入")
			return &p
		}
	}
	return nil
}

func exists(root, rel string) bool {
	_, err := os.Lstat(abs(root, rel))
	return err == nil
}

func digest(data []byte) string {
	sum := sha256.Sum256(data)
	return hex.EncodeToString(sum[:])
}

func abs(root, rel string) string { return filepath.Join(root, filepath.FromSlash(rel)) }

// Item 是 List 列出的一个规范包。Title 在文件缺失或无法加载时为 nil；Path 对内置规范包为 nil；Lock 在没有锁定条目时为 nil。
type Item struct {
	ID     string
	Title  *string
	Origin string
	Path   *string
	Status string
	Lock   *Entry
}

// 列表中规范包的状态；installed、modified 与 missing 见 Status。
const (
	StatusBuiltin = "builtin"
	StatusLocal   = "local"
)

// List 只读地列出内置规范包（按 id 排序），再列出工作区 packs/ 中的规范包与锁定文件中有条目但文件缺失的规范包（合并后按 id 排序）。
// 与锁定不一致的条目给出 warning 级问题；锁定文件本身不合法时返回 nil 与 error 级问题。
func List(root string) ([]Item, []workspace.Problem) {
	lock, problems := ReadLock(root)
	if problems != nil {
		return nil, problems
	}
	var out []Item
	for _, id := range catalog.BuiltinPacks() {
		it := Item{ID: id, Origin: catalog.OriginBuiltin, Status: StatusBuiltin}
		if p, _, _ := catalog.FindPack(root, id); p != nil {
			it.Title = &p.Title
		}
		out = append(out, it)
	}
	var local []Item
	for _, name := range workspace.YAMLFiles(root, workspace.PacksPath) {
		rel := workspace.PacksPath + "/" + name
		it := Item{ID: strings.TrimSuffix(name, ".yaml"), Origin: catalog.OriginWorkspace, Path: &rel, Status: StatusLocal}
		if p, _ := catalog.LoadPackFile(root, rel); p != nil {
			it.Title = &p.Title
		}
		local = append(local, it)
	}
	for _, e := range lock.Entries {
		i := slices.IndexFunc(local, func(it Item) bool { return it.ID == e.ID })
		if i < 0 {
			rel := catalog.PackPath(e.ID)
			local = append(local, Item{ID: e.ID, Origin: catalog.OriginWorkspace, Path: &rel})
			i = len(local) - 1
		}
		local[i].Lock, local[i].Status = &e, Status(root, e)
	}
	slices.SortStableFunc(local, func(a, b Item) int { return strings.Compare(a.ID, b.ID) })
	problems = VerifyEntries(root, lock)
	for i := range problems {
		problems[i].Severity = workspace.SeverityWarning
	}
	return append(out, local...), problems
}
