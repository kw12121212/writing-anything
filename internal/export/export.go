// Package export 把作品中已验收（Acceptance）的单元导出为交付格式（目前为 Markdown）：按单元树先序拼接各单元的正文，
// 去掉写作过程中的批注与单处豁免注释，可选附带文件头元数据（Front Matter）。只导出验收记录有效（基于当前正文、通过校验、
// 关卡强度不低于当前配置）的单元；本包只返回结构化的问题，由命令层转换为诊断（Diagnostic）。
package export

import (
	"errors"
	"fmt"
	"io/fs"
	"os"
	"path"
	"path/filepath"
	"strings"

	"github.com/kw12121212/writing-anything/internal/brief"
	"github.com/kw12121212/writing-anything/internal/draft"
	"github.com/kw12121212/writing-anything/internal/review"
	"github.com/kw12121212/writing-anything/internal/work"
	"github.com/kw12121212/writing-anything/internal/workspace"
	"go.yaml.in/yaml/v3"
)

// 导出的错误码（Error Code）。已发布的错误码不得改名或复用。
const (
	CodeUnaccepted    = "export.unaccepted"
	CodeNothing       = "export.nothing-accepted"
	CodeInvalidOutput = "export.invalid-output"
)

// MaxBytes 是导出时单个单元正文的字节数上限，与正文写入的上限一致（workspace-artifacts/input-limits）。
const MaxBytes = 2 << 20

// Formats 是支持的导出格式。
var Formats = []string{"markdown"}

// ExportsDir 是工作区中存放导出文件的目录。
const ExportsDir = workspace.Dir + "/exports"

// DefaultPath 返回作品 id 默认的导出路径。
func DefaultPath(id string) string { return ExportsDir + "/" + id + ".md" }

// Skipped 是一个没有导出的单元与原因。
type Skipped struct {
	Unit   string
	Reason string
}

// Result 是一次导出的结果：Path 为写出的文件，Units 为按顺序导出的单元引用。
type Result struct {
	Path    string
	Units   []string
	Skipped []Skipped
	Chars   int
}

// Options 是导出的选项：FrontMatter 为 true 时附带 front matter；Output 为相对项目目录的输出路径，空时为 DefaultPath。
type Options struct {
	FrontMatter bool
	Output      string
	Level       string // 工作区配置的关卡强度，用于判断验收是否仍然有效
}

// Markdown 把作品 w 中已验收的单元导出为一个 Markdown 文件。没有可导出的单元时报告 export.nothing-accepted，不写文件；
// 跳过的单元各以 warning 级 export.unaccepted 报告。返回结果与变更的路径。
func Markdown(root string, w *work.Work, opt Options) (*Result, []string, []workspace.Problem) {
	out := opt.Output
	if out == "" {
		out = DefaultPath(w.ID)
	}
	rel, p := cleanOutput(out)
	if p != nil {
		return nil, nil, []workspace.Problem{*p}
	}
	res := &Result{Path: rel, Units: []string{}, Skipped: []Skipped{}}
	var problems []workspace.Problem
	var parts []string
	var accepted []*review.Acceptance
	for _, u := range w.Units() {
		dir := w.UnitDir(u)
		content, ok, ps := draft.Read(root, w, u)
		reason := ""
		var a *review.Acceptance
		switch {
		case len(ps) > 0:
			reason = ps[0].Message
		case !ok:
			reason = "没有正文"
		case len(content) > MaxBytes:
			reason = fmt.Sprintf("正文有 %d 字节，超过导出的上限 %d 字节", len(content), MaxBytes)
		default:
			a, _ = review.LoadAcceptance(root, dir)
			if bs := review.AcceptanceBlockers(root, dir, opt.Level); len(bs) > 0 {
				reason = bs[0].Message // 不通过校验、过期或关卡强度低于当前配置
			} else if a == nil {
				reason = "尚未验收"
			}
		}
		if reason != "" {
			res.Skipped = append(res.Skipped, Skipped{u.Ref, reason})
			problems = append(problems, workspace.Problem{Code: CodeUnaccepted, Severity: workspace.SeverityWarning, Path: dir,
				Message: fmt.Sprintf("单元 %s 没有导出：%s", u.Ref, reason)})
			continue
		}
		body := strings.TrimRight(strings.ReplaceAll(string(StripComments(content)), "\r\n", "\n"), "\n")
		parts = append(parts, body)
		res.Units = append(res.Units, u.Ref)
		accepted = append(accepted, a)
	}
	if len(parts) == 0 {
		return nil, nil, append(problems, workspace.Problem{Code: CodeNothing, Severity: workspace.SeverityError, Path: work.Dir(w.ID),
			Message: "作品 " + w.ID + " 没有已验收的单元，无法导出；请先审稿（Review）并用 wa-cli accept 验收"})
	}
	doc := strings.Join(parts, "\n\n") + "\n"
	if opt.FrontMatter {
		fm, err := frontMatter(root, w, parts[0], res.Units, accepted)
		if err != nil {
			return nil, nil, append(problems, workspace.Problem{Code: workspace.CodeWriteFailed, Severity: workspace.SeverityError, Path: rel, Message: "生成 front matter 失败：" + err.Error()})
		}
		doc = fm + doc
	}
	changed, ps := ensureParents(root, rel)
	if len(ps) > 0 {
		return nil, nil, append(problems, ps...)
	}
	if err := workspace.WriteFile(root, rel, []byte(doc)); err != nil {
		return nil, nil, append(problems, workspace.Problem{Code: workspace.CodeWriteFailed, Severity: workspace.SeverityError, Path: rel, Message: "写入导出文件失败：" + err.Error()})
	}
	res.Chars = len([]rune(doc))
	return res, append(changed, rel), problems
}

// cleanOutput 校验输出路径：相对项目目录，不得是绝对路径或离开项目目录，不得是目录，在工作区中时只能在 exports/ 之下。
func cleanOutput(out string) (string, *workspace.Problem) {
	bad := func(msg string) (string, *workspace.Problem) {
		return "", &workspace.Problem{Code: CodeInvalidOutput, Severity: workspace.SeverityError, Field: "output", Message: msg}
	}
	if filepath.IsAbs(out) || strings.HasPrefix(out, "/") || strings.Contains(out, "\\") {
		return bad("输出路径 " + out + " 必须是相对项目目录（--dir）的路径，使用 / 分隔")
	}
	rel := path.Clean(out)
	if rel == "." || rel == ".." || strings.HasPrefix(rel, "../") || strings.HasSuffix(out, "/") {
		return bad("输出路径 " + out + " 必须是项目目录之内的文件路径")
	}
	// 工作区中的产物只能由对应的 wa-cli 命令修改：导出只能写到工作区之外，或工作区的 exports/ 之下。按不区分大小写比较
	// （macOS 与 Windows 的默认文件系统不区分大小写）；写入前 ensureParents 再按文件身份复查。
	segs := strings.Split(rel, "/")
	if strings.EqualFold(segs[0], workspace.Dir) && !(len(segs) > 2 && strings.EqualFold(segs[1], path.Base(ExportsDir))) {
		return bad(workspaceOutput(out))
	}
	return rel, nil
}

func workspaceOutput(out string) string {
	return "输出路径 " + out + " 在工作区 " + workspace.Dir + "/ 中：导出不能覆盖工作区产物，只能写到 " + ExportsDir + "/ 之下或工作区之外"
}

// ensureParents 确保输出文件所在的每一级目录都是项目目录之内的真实目录（不跟随符号链接），不存在时创建；输出文件已存在时
// 必须是普通文件。按文件身份（os.SameFile）判断途经的目录是否为工作区目录：是时下一级必须是工作区的 exports 目录（或尚不存在、
// 名为 exports），使不区分大小写的文件系统上换个大小写也写不进工作区产物。返回新建的目录（workspace-artifacts/validated-path-segments）。
func ensureParents(root, rel string) ([]string, []workspace.Problem) {
	segs := strings.Split(rel, "/")
	var created []string
	bad := func(p, msg string) []workspace.Problem {
		return []workspace.Problem{{Code: CodeInvalidOutput, Severity: workspace.SeverityError, Path: p, Field: "output", Message: msg}}
	}
	ws, wsErr := os.Lstat(filepath.Join(root, workspace.Dir))
	exports, exErr := os.Lstat(filepath.Join(root, filepath.FromSlash(ExportsDir)))
	inWorkspace := false // 上一级是工作区目录，这一级必须是 exports
	for i := 1; i <= len(segs); i++ {
		p := strings.Join(segs[:i], "/")
		info, err := os.Lstat(filepath.Join(root, filepath.FromSlash(p)))
		last := i == len(segs)
		if inWorkspace {
			isExports := err == nil && exErr == nil && os.SameFile(info, exports) || err != nil && segs[i-1] == path.Base(ExportsDir)
			if last || !isExports {
				return nil, bad(p, workspaceOutput(rel))
			}
			inWorkspace = false
		} else if err == nil && wsErr == nil && os.SameFile(info, ws) {
			inWorkspace = true
		}
		switch {
		case err == nil && !last && info.IsDir():
			continue
		case err == nil && last && info.Mode().IsRegular():
			continue
		case err == nil:
			return nil, bad(p, p+" 不是真实目录或普通文件（如符号链接），wa-cli 不会写入它")
		case !errors.Is(err, fs.ErrNotExist):
			return nil, []workspace.Problem{{Code: workspace.CodeReadFailed, Severity: workspace.SeverityError, Path: p, Message: "读取失败：" + err.Error()}}
		case last:
			continue
		}
		if err := os.Mkdir(filepath.Join(root, filepath.FromSlash(p)), 0o755); err != nil {
			return nil, []workspace.Problem{{Code: workspace.CodeWriteFailed, Severity: workspace.SeverityError, Path: p, Message: "创建目录失败：" + err.Error()}}
		}
		created = append(created, p)
	}
	return created, nil
}

// frontMatter 生成 front matter：title（第一个导出单元正文中的第一个一级标题，没有时为作品 id）、description（根单元写作简报的
// purpose，没有时省略）、genre、units 与 accepted_at（导出单元中最晚的验收时间）。
func frontMatter(root string, w *work.Work, first string, units []string, accepted []*review.Acceptance) (string, error) {
	title := w.ID
	if h := FirstHeading(first); h != "" {
		title = h
	}
	latest := ""
	for _, a := range accepted {
		if a != nil && a.AcceptedAt > latest {
			latest = a.AcceptedAt
		}
	}
	type fm struct {
		Title       string   `yaml:"title"`
		Description string   `yaml:"description,omitempty"`
		Genre       string   `yaml:"genre"`
		Units       []string `yaml:"units,flow"`
		AcceptedAt  string   `yaml:"accepted_at"`
	}
	v := fm{Title: title, Genre: w.Genre.ID, Units: units, AcceptedAt: latest}
	if b, _ := brief.Load(root, w.UnitDir(w.Root), brief.Queue{Invalid: true}); b != nil {
		v.Description = b.Purpose
	}
	data, err := yaml.Marshal(v)
	if err != nil {
		return "", err
	}
	return "---\n" + string(data) + "---\n\n", nil
}
