package cli

import (
	"fmt"
	"io"
	"path"
	"slices"
	"strings"

	"github.com/kw12121212/writing-anything/internal/brief"
	"github.com/kw12121212/writing-anything/internal/catalog"
	"github.com/kw12121212/writing-anything/internal/packinstall"
	"github.com/kw12121212/writing-anything/internal/workspace"
)

const packLockHelp = "锁定条目：source（git 与 ref，或 path）、commit（本地来源为 null）与 sha256"

var packInstallCommand = &command{
	name: "pack install",
	summary: "从 git 地址或本地路径安装规范包（Pack）到工作区 packs/<id>.yaml，并在锁定文件（Lock File）" + packinstall.LockPath +
		" 中记录来源、提交与内容哈希；安装前做与工作区规范包相同的校验，不修改 config.yaml（启用请用 wa-cli config set packs）",
	args: []argSpec{{"source", "git 地址（https、http、ssh、git、file 协议或 user@host:path），或相对 --dir 的本地路径：规范包的 .yaml 文件或含 " +
		packinstall.SourceFile + " 的目录", true}},
	flags:      []flagSpec{dirFlag, {"ref", flagString, "git 来源要检出的分支、标签或提交；默认为默认分支"}},
	outputKeys: []outputKey{{"pack", "安装的规范包：id、title、path、source（git 与 ref，或 path）、commit（本地来源为 null）、sha256 与 status（installed、updated 或 unchanged）；失败时为 null"}},
	exitCodes:  standardExitCodes,
	run:        runPackInstall,
}

var packListCommand = &command{
	name:    "pack list",
	summary: "列出内置、工作区自有与已安装的规范包（Pack）及其锁定状态",
	flags:   []flagSpec{dirFlag},
	outputKeys: []outputKey{{"packs", "规范包数组：先是内置规范包，再是工作区中的规范包与锁定文件中有条目但文件缺失的规范包；每项含 id、title、origin（builtin 或 workspace）、path、enabled、" +
		"status（builtin、local、installed、modified 或 missing）与 lock（" + packLockHelp + "；没有锁定条目时为 null）"}},
	exitCodes: standardExitCodes,
	run:       runPackList,
}

var packRemoveCommand = &command{
	name:      "pack remove",
	summary:   "移除用 wa-cli pack install 安装的规范包（Pack）：删除 packs/<id>.yaml 与锁定条目；仍在 config.yaml 的 packs 中时拒绝",
	args:      []argSpec{{"id", "已安装的规范包 id", true}},
	flags:     []flagSpec{dirFlag},
	exitCodes: standardExitCodes,
	run:       runPackRemove,
}

// packInstallEnv 是安装时与外部环境（git）的交互，测试可以替换。
var packInstallEnv = packinstall.System

type packSourceView struct {
	Git  string `json:"git,omitempty"`
	Ref  string `json:"ref,omitempty"`
	Path string `json:"path,omitempty"`
}

type packInstallView struct {
	ID     string         `json:"id"`
	Title  string         `json:"title"`
	Path   string         `json:"path"`
	Source packSourceView `json:"source"`
	Commit *string        `json:"commit"`
	SHA256 string         `json:"sha256"`
	Status string         `json:"status"`
}

type packInstallResult struct {
	Result
	Pack *packInstallView `json:"pack"`
}

type packLockView struct {
	Source packSourceView `json:"source"`
	Commit *string        `json:"commit"`
	SHA256 string         `json:"sha256"`
}

type packItemView struct {
	ID      string        `json:"id"`
	Title   *string       `json:"title"`
	Origin  string        `json:"origin"`
	Path    *string       `json:"path"`
	Enabled bool          `json:"enabled"`
	Status  string        `json:"status"`
	Lock    *packLockView `json:"lock"`
}

type packListResult struct {
	Result
	Packs []packItemView `json:"packs"`
}

func commitOf(e packinstall.Entry) *string {
	if e.Commit == "" {
		return nil
	}
	return &e.Commit
}

func runPackInstall(inv *invocation) int {
	dir := projectDir(inv)
	res := &packInstallResult{}
	src := inv.args[0]
	ref, hasRef := inv.values["ref"]
	problems := workspace.Require(dir)
	switch {
	case problems != nil:
	case hasRef && !packinstall.IsGit(src):
		problems = []workspace.Problem{invalidValue("ref", ref, "--ref 只用于 git 地址，"+src+" 是本地路径")}
	case hasRef && !packinstall.ValidRef(ref):
		problems = []workspace.Problem{invalidValue("ref", ref, "应为分支、标签或提交，不以 - 开头且不含空白")}
	}
	if problems == nil {
		var r *packinstall.Result
		if r, problems = packinstall.Install(packInstallEnv(), dir, src, ref); r != nil {
			res.ChangedPaths = r.Changed
			if problems == nil {
				res.Pack = &packInstallView{ID: r.Entry.ID, Title: r.Title, Path: r.Path, Source: packSourceView(r.Entry.Source),
					Commit: commitOf(r.Entry), SHA256: r.Entry.SHA256, Status: r.Status}
			}
		}
	}
	res.Diagnostics = diagnostics(problems, dir)
	for i, p := range problems {
		if p.Path == "" {
			// 来源与参数的问题不在工作区中，validate 检不出来；查看用法后改正来源重新安装。
			res.Diagnostics[i].Next = []string{"wa-cli help pack install --json"}
		}
	}
	return finish(inv, res, func(w io.Writer) error {
		if res.Pack == nil {
			return nil
		}
		verb := map[string]string{packinstall.StatusInstalled: "已安装", packinstall.StatusUpdated: "已更新", packinstall.StatusUnchanged: "没有变化"}[res.Pack.Status]
		_, err := fmt.Fprintf(w, "规范包（Pack）%s（%s）%s：%s；启用请运行 %s\n", res.Pack.ID, res.Pack.Title, verb, res.Pack.Path,
			shellCommand("wa-cli", "config", "set", "packs", res.Pack.ID, "--dir", dir))
		return err
	})
}

func runPackList(inv *invocation) int {
	dir := projectDir(inv)
	res := &packListResult{Packs: []packItemView{}}
	problems := workspace.Require(dir)
	if problems == nil {
		var items []packinstall.Item
		items, problems = packinstall.List(dir)
		enabled := []string{}
		if cfg, _ := workspace.Load(dir); cfg != nil {
			enabled = cfg.Packs
		}
		if len(enabled) == 0 {
			enabled = []string{catalog.DefaultPack}
		}
		for _, it := range items {
			v := packItemView{ID: it.ID, Title: it.Title, Origin: it.Origin, Path: it.Path, Enabled: slices.Contains(enabled, it.ID), Status: it.Status}
			if it.Lock != nil {
				v.Lock = &packLockView{Source: packSourceView(it.Lock.Source), Commit: commitOf(*it.Lock), SHA256: it.Lock.SHA256}
			}
			res.Packs = append(res.Packs, v)
		}
	}
	res.Diagnostics = diagnostics(problems, dir)
	return finish(inv, res, func(w io.Writer) error {
		var b strings.Builder
		for _, p := range res.Packs {
			title, on := "（无法加载）", ""
			if p.Title != nil {
				title = *p.Title
			}
			if p.Enabled {
				on = "，已启用"
			}
			fmt.Fprintf(&b, "%s：%s（%s，%s%s）", p.ID, title, p.Origin, p.Status, on)
			if p.Path != nil {
				fmt.Fprintf(&b, " %s", *p.Path)
			}
			if l := p.Lock; l != nil {
				src := l.Source.Path
				if l.Source.Git != "" {
					src = l.Source.Git
					if l.Source.Ref != "" {
						src += " " + l.Source.Ref
					}
					src += " @ " + *l.Commit
				}
				fmt.Fprintf(&b, "；锁定来源 %s，sha256 %s", src, l.SHA256)
			}
			b.WriteString("\n")
		}
		_, err := io.WriteString(w, b.String())
		return err
	})
}

func runPackRemove(inv *invocation) int {
	dir := projectDir(inv)
	res := &Result{}
	id := inv.args[0]
	problems := workspace.Require(dir)
	var enabled []string
	switch {
	case problems != nil:
	case !brief.ValidID(id):
		problems = []workspace.Problem{{Code: codeInvalidValue, Severity: workspace.SeverityError,
			Message: fmt.Sprintf("参数 <id> 的取值 %q 不合法：应为 id 格式（小写字母开头，由小写字母、数字与连字符组成）", id)}}
	default:
		cfg, ps := workspace.Load(dir)
		if cfg == nil {
			problems = ps
		} else {
			enabled = cfg.Packs
		}
	}
	if problems == nil {
		res.ChangedPaths, problems = packinstall.Remove(dir, id, enabled)
	}
	res.Diagnostics = diagnostics(problems, dir)
	for i, p := range problems {
		switch p.Code {
		case codeInvalidValue:
			res.Diagnostics[i].Next = []string{"wa-cli help pack remove --json"}
		case packinstall.CodeInUse:
			rest := slices.DeleteFunc(slices.Clone(enabled), func(s string) bool { return s == id })
			if len(rest) == 0 {
				res.Diagnostics[i].Message += "；packs 去掉它后为空，将隐式启用内置 " + catalog.DefaultPack + " 规范包"
			}
			res.Diagnostics[i].Next = []string{shellCommand("wa-cli", "config", "set", "packs", strings.Join(rest, ","), "--dir", dir, "--json")}
		}
	}
	return finish(inv, res, func(w io.Writer) error {
		if hasError(res.Diagnostics) {
			return nil
		}
		_, err := fmt.Fprintf(w, "已移除规范包（Pack）%s\n", id)
		return err
	})
}

// packNext 返回锁定一致性问题的下一步命令：按锁定来源重新安装（git 来源以锁定的提交作为 --ref），或移除该规范包。
func packNext(p workspace.Problem, dir string) []string {
	id := strings.TrimSuffix(path.Base(p.Path), ".yaml")
	out := []string{}
	if lock, _ := packinstall.ReadLock(dir); lock != nil {
		if e := lock.Find(id); e != nil && e.Source.Git != "" {
			out = append(out, shellCommand("wa-cli", "pack", "install", e.Source.Git, "--ref", e.Commit, "--dir", dir, "--json"))
		} else if e != nil {
			out = append(out, shellCommand("wa-cli", "pack", "install", e.Source.Path, "--dir", dir, "--json"))
		}
	}
	return append(out, shellCommand("wa-cli", "pack", "remove", id, "--dir", dir, "--json"))
}
