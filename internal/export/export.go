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
	"slices"
	"strings"

	"github.com/kw12121212/writing-anything/internal/brief"
	"github.com/kw12121212/writing-anything/internal/draft"
	"github.com/kw12121212/writing-anything/internal/illustration"
	"github.com/kw12121212/writing-anything/internal/marketing"
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

// 导出格式。
const (
	FormatMarkdown = "markdown"
	FormatWechat   = "wechat-html"
	FormatText     = "text"
)

// Formats 是支持的导出格式。
var Formats = []string{FormatMarkdown, FormatWechat, FormatText, FormatPaste}

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
	Path       string
	Units      []string
	Skipped    []Skipped
	Chars      int
	Images     []Image     // wechat-html 的配图；Markdown 为空
	ImageList  string      // 配图清单的路径；没有配图时为空
	Assets     []Asset     // 复制到资产目录的配图与封面（封面在前）；没有时为空
	Disclosure *Disclosure // AI 参与说明；没有 AI 参与时为 nil
	HTML       string      // 可粘贴页只读模式（Print）的全文片段；其他情况为空
	Segments   []string    // 可粘贴页只读模式（Print）的分段片段（至少一项）；其他情况为 nil
	Headline   *string     // 可粘贴页只读模式（Print）的作者选定标题（标题决策为 decided 时）；其他情况为 nil
	Digest     *string     // 可粘贴页只读模式（Print）的文章摘要（Digest）；其他情况为 nil
}

// Options 是导出的选项：FrontMatter 为 true 时附带 front matter；Output 为相对项目目录的输出路径，空时为 DefaultPath。
type Options struct {
	FrontMatter bool
	Output      string
	Level       string            // 工作区配置的关卡强度，用于判断验收是否仍然有效
	Disclosure  *DisclosureConfig // AI 参与说明的配置；nil 时不生成说明
	Print       bool              // 只用于 paste-html：只读，不写任何文件，正文片段放在 Result.HTML
}

// Markdown 把作品 w 中已验收的单元导出为一个 Markdown 文件。没有可导出的单元时报告 export.nothing-accepted，不写文件；
// 跳过的单元各以 warning 级 export.unaccepted 报告。返回结果与变更的路径。
func Markdown(root string, w *work.Work, opt Options) (*Result, []string, []workspace.Problem) {
	out := opt.Output
	if out == "" {
		out = DefaultPath(w.ID)
	}
	rel, p := cleanOutput(out, CodeInvalidOutput)
	if p != nil {
		return nil, nil, []workspace.Problem{*p}
	}
	res, parts, units, accepted, problems := collect(root, w, opt.Level)
	if len(parts) == 0 {
		return nil, nil, problems
	}
	res.Path = rel
	parts, res.Assets, _ = planAssets(root, w, units, parts, rel)
	d, ps := disclose(root, w, units, opt.Disclosure, countAI(res.Assets), len(res.Assets))
	problems, res.Disclosure = append(problems, ps...), d
	doc := strings.Join(parts, "\n\n") + "\n"
	if d.footer() {
		doc += "\n---\n\n" + d.Text + "\n"
	}
	if opt.FrontMatter {
		label := ""
		if d.metadata() {
			label = d.Text
		}
		coverPath := ""
		if c := cover(res.Assets); c != nil {
			coverPath = relToExport(*c)
		}
		fm, err := frontMatter(root, w, parts[0], res.Units, accepted, label, coverPath)
		if err != nil {
			return nil, nil, append(problems, workspace.Problem{Code: workspace.CodeWriteFailed, Severity: workspace.SeverityError, Path: rel, Message: "生成 front matter 失败：" + err.Error()})
		}
		doc = fm + doc
	}
	// 导出文件与全部资产文件的路径都在写入任何文件之前检查（export-output）。
	if len(res.Assets) > 0 {
		if ps := slices.Concat(checkParents(root, rel, CodeInvalidOutput), checkAssets(root, res.Assets, CodeInvalidOutput)); len(ps) > 0 {
			return nil, nil, append(problems, ps...)
		}
	}
	changed, ps := write(root, rel, []byte(doc), CodeInvalidOutput)
	if len(ps) > 0 {
		return nil, nil, append(problems, ps...)
	}
	more, ps := writeAssets(root, res.Assets, CodeInvalidOutput)
	if changed = append(changed, more...); len(ps) > 0 {
		return nil, changed, append(problems, ps...)
	}
	res.Chars = len([]rune(doc))
	return res, changed, problems
}

// WechatHTML 把作品 w 中已验收的单元导出为只含内联样式的公众号 HTML（见 RenderWechat），theme 为合并后的样式主题。存在配图时
// 在导出文件旁写出配图清单（ImageListPath）；两个路径都在写入任何文件之前检查。没有可导出的单元时报告 export.nothing-accepted。
func WechatHTML(root string, w *work.Work, opt Options, theme map[string]string) (*Result, []string, []workspace.Problem) {
	out := opt.Output
	if out == "" {
		out = ExportsDir + "/" + w.ID + ".html"
	}
	rel, p := cleanOutput(out, CodeInvalidOutput)
	if p != nil {
		return nil, nil, []workspace.Problem{*p}
	}
	res, parts, units, _, problems := collect(root, w, opt.Level)
	if len(parts) == 0 {
		return nil, nil, problems
	}
	res.Path = rel
	var occ []occurrence
	_, res.Assets, occ = planAssets(root, w, units, parts, rel)
	d, ps := disclose(root, w, units, opt.Disclosure, countAI(res.Assets), len(res.Assets))
	problems, res.Disclosure = append(problems, ps...), d
	footer := ""
	if d.footer() {
		footer = d.Text
	}
	doc, images := RenderWechat(strings.Join(parts, "\n\n"), theme, footer)
	res.Images = images
	changed, ps, ok := writeWithImages(root, rel, []byte(doc), res, occ)
	if problems = append(problems, ps...); !ok {
		return nil, changed, problems
	}
	res.Chars = len([]rune(doc))
	return res, changed, problems
}

// writeWithImages 写出带配图的导出文件 rel（内容 doc，res.Images 与 res.Assets 已填写）：有正文图片或导出封面时在同目录写出配图清单
// （ImageListPath，填写 res.ImageList），并把资产复制到资产目录。先不带副作用地检查导出文件、配图清单与全部资产文件的路径，
// 要么都写，要么都不写（export-output）。返回变更的路径、问题与是否成功。
func writeWithImages(root, rel string, doc []byte, res *Result, occ []occurrence) ([]string, []workspace.Problem, bool) {
	var list []byte
	c := cover(res.Assets)
	if len(res.Images) > 0 || c != nil {
		lp, p := cleanOutput(ImageListPath(rel), CodeInvalidOutput)
		if p != nil {
			return nil, []workspace.Problem{*p}, false
		}
		res.ImageList = lp
		list = []byte(imageList(res.Images, res.Assets, occ, c))
	}
	if list != nil || len(res.Assets) > 0 {
		ps := slices.Concat(checkParents(root, rel, CodeInvalidOutput), checkAssets(root, res.Assets, CodeInvalidOutput))
		if list != nil {
			ps = append(ps, checkParents(root, res.ImageList, CodeInvalidOutput)...)
		}
		if len(ps) > 0 {
			return nil, ps, false
		}
	}
	changed, ps := write(root, rel, doc, CodeInvalidOutput)
	if len(ps) > 0 {
		return nil, ps, false
	}
	if list != nil {
		more, ps := write(root, res.ImageList, list, CodeInvalidOutput)
		if len(ps) > 0 {
			return changed, ps, false
		}
		for _, c := range more {
			if !slices.Contains(changed, c) {
				changed = append(changed, c)
			}
		}
	}
	more, ps := writeAssets(root, res.Assets, CodeInvalidOutput)
	if changed = append(changed, more...); len(ps) > 0 {
		return changed, ps, false
	}
	return changed, nil, true
}

// imageList 生成配图清单（Image List）：标题「配图清单」，有导出封面时先列「封面」项，再按编号列出正文配图的替代文字、地址与标题；
// 已复制到资产目录的配图另列资产文件相对清单的路径，出处为 agent 的另标注「AI 生成」。没有配图登记时与只有替代文字、地址与标题时相同。
func imageList(images []Image, assets []Asset, occ []occurrence, c *Asset) string {
	var b strings.Builder
	b.WriteString("# 配图清单\n\n")
	asset := func(a Asset) {
		fmt.Fprintf(&b, "；文件：%s", relToExport(a))
		if a.Origin == illustration.OriginAgent {
			b.WriteString("；AI 生成")
		}
	}
	if c != nil {
		fmt.Fprintf(&b, "- 封面：%s", c.Alt)
		asset(*c)
		b.WriteString("\n")
		if len(images) > 0 {
			b.WriteString("\n")
		}
	}
	// 公众号 HTML 的配图按出现顺序编号；按地址依次对应到正文中的图片，得到已复制的资产。
	cursor := 0
	for _, img := range images {
		fmt.Fprintf(&b, "%d. %s：%s", img.Index, img.Alt, img.Src)
		if img.Title != "" {
			fmt.Fprintf(&b, "（%s）", img.Title)
		}
		src := img.Src
		if strings.HasPrefix(src, "<") && strings.HasSuffix(src, ">") {
			src = src[1 : len(src)-1] // 公众号渲染保留尖括号包住的地址
		}
		for j := cursor; j < len(occ); j++ {
			if occ[j].src == src {
				if occ[j].asset >= 0 {
					asset(assets[occ[j].asset])
				}
				cursor = j + 1
				break
			}
		}
		b.WriteString("\n")
	}
	return b.String()
}

// ImageListPath 返回导出文件 rel 的配图清单路径：去掉 .html 后缀（没有时为整个文件名）加 .images.md。
func ImageListPath(rel string) string { return strings.TrimSuffix(rel, ".html") + ".images.md" }

// PlainText 把作品 w 中已验收的单元导出为一个纯文本（Plain Text）文件：按 export-markdown 同样的规则选出单元、去掉过程注释，
// 再把正文去掉 Markdown 标记（见 RenderPlainText）。AI 参与说明按 Markdown 的文末格式追加：纯文本没有元数据位置，placement 为
// metadata 或 both 时同样只按 footer 追加（有说明就追加）。--front-matter 由命令层拒绝（plain-text-export）。
func PlainText(root string, w *work.Work, opt Options) (*Result, []string, []workspace.Problem) {
	out := opt.Output
	if out == "" {
		out = ExportsDir + "/" + w.ID + ".txt"
	}
	rel, p := cleanOutput(out, CodeInvalidOutput)
	if p != nil {
		return nil, nil, []workspace.Problem{*p}
	}
	res, parts, units, _, problems := collect(root, w, opt.Level)
	if len(parts) == 0 {
		return nil, nil, problems
	}
	res.Path = rel
	// 纯文本不复制配图与封面，AI 参与说明不计配图（plain-text-export）。
	d, ps := disclose(root, w, units, opt.Disclosure, 0, 0)
	problems, res.Disclosure = append(problems, ps...), d
	doc := RenderPlainText(strings.Join(parts, "\n\n")) + "\n"
	if d != nil {
		doc += "\n---\n\n" + d.Text + "\n"
	}
	changed, ps := write(root, rel, []byte(doc), CodeInvalidOutput)
	if len(ps) > 0 {
		return nil, nil, append(problems, ps...)
	}
	res.Chars = len([]rune(doc))
	return res, changed, problems
}

// collect 选出作品 w 中验收记录有效的单元（level 为工作区当前的关卡强度），返回结果（不含 Path 与 Chars）、各单元去掉过程注释后的
// 正文、导出的单元与验收记录；跳过的单元以 warning 级 export.unaccepted 报告，没有可导出的单元时追加 export.nothing-accepted。
func collect(root string, w *work.Work, level string) (*Result, []string, []*work.Unit, []*review.Acceptance, []workspace.Problem) {
	res := &Result{Units: []string{}, Skipped: []Skipped{}, Images: []Image{}}
	var problems []workspace.Problem
	var parts []string
	var units []*work.Unit
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
			if bs := review.AcceptanceBlockers(root, dir, level); len(bs) > 0 {
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
		units = append(units, u)
		res.Units = append(res.Units, u.Ref)
		accepted = append(accepted, a)
	}
	if len(parts) == 0 {
		problems = append(problems, workspace.Problem{Code: CodeNothing, Severity: workspace.SeverityError, Path: work.Dir(w.ID),
			Message: "作品 " + w.ID + " 没有已验收的单元，无法导出；请先审稿（Review）并用 wa-cli accept 验收"})
	}
	return res, parts, units, accepted, problems
}

// WriteOutput 按导出的输出路径规则（export-output）把 data 原子写入相对项目目录的 out：路径不合法、途经符号链接或落在工作区中
// exports/ 之外时以错误码 code 报告，不写文件。返回规整后的路径与变更的路径（新建的目录与该文件）。
func WriteOutput(root, out string, data []byte, code string) (string, []string, []workspace.Problem) {
	rel, p := cleanOutput(out, code)
	if p != nil {
		return "", nil, []workspace.Problem{*p}
	}
	changed, ps := write(root, rel, data, code)
	return rel, changed, ps
}

// write 确保 rel 的上级目录后原子写入 data，返回新建的目录与 rel。
func write(root, rel string, data []byte, code string) ([]string, []workspace.Problem) {
	changed, ps := ensureParents(root, rel, code)
	if len(ps) > 0 {
		return nil, ps
	}
	if err := workspace.WriteFile(root, rel, data); err != nil {
		return nil, []workspace.Problem{{Code: workspace.CodeWriteFailed, Severity: workspace.SeverityError, Path: rel, Message: "写入导出文件失败：" + err.Error()}}
	}
	return append(changed, rel), nil
}

// cleanOutput 校验输出路径：相对项目目录，不得是绝对路径或离开项目目录，不得是目录，在工作区中时只能在 exports/ 之下。
func cleanOutput(out, code string) (string, *workspace.Problem) {
	bad := func(msg string) (string, *workspace.Problem) {
		return "", &workspace.Problem{Code: code, Severity: workspace.SeverityError, Field: "output", Message: msg}
	}
	if filepath.IsAbs(out) || strings.HasPrefix(out, "/") || strings.Contains(out, "\\") {
		return bad("输出路径 " + out + " 必须是相对项目目录（--dir）的路径，使用 / 分隔")
	}
	rel := path.Clean(out)
	if rel == "." || rel == ".." || strings.HasPrefix(rel, "../") || strings.HasSuffix(out, "/") {
		return bad("输出路径 " + out + " 必须是项目目录之内的文件路径")
	}
	// 工作区中的产物只能由对应的 wa-cli 命令修改：导出只能写到工作区之外，或工作区的 exports/ 之下。按不区分大小写比较
	// （macOS 与 Windows 的默认文件系统不区分大小写）；写入前 ensureParents 再按文件身份复查。这里的 EqualFold 只是快路径，
	// 大小写变体（如 EXPORTS）最终由 ensureParents 按文件身份与字面段名拒绝，两处口径以 ensureParents 为准。
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
func ensureParents(root, rel, code string) ([]string, []workspace.Problem) {
	return parents(root, rel, code, true)
}

// checkParents 做与 ensureParents 相同的检查，但不创建目录（写入多个交付文件前的无副作用预检查）。
func checkParents(root, rel, code string) []workspace.Problem {
	_, ps := parents(root, rel, code, false)
	return ps
}

// parents 检查 rel 途经的目录与 rel 本身；create 为 true 时创建不存在的目录并返回它们。
func parents(root, rel, code string, create bool) ([]string, []workspace.Problem) {
	segs := strings.Split(rel, "/")
	var created []string
	bad := func(p, msg string) []workspace.Problem {
		return []workspace.Problem{{Code: code, Severity: workspace.SeverityError, Path: p, Field: "output", Message: msg}}
	}
	ws, wsErr := os.Lstat(filepath.Join(root, workspace.Dir))
	exports, exErr := os.Lstat(filepath.Join(root, filepath.FromSlash(ExportsDir)))
	inWorkspace := false // 上一级是工作区目录，这一级必须是 exports
	for i := 1; i <= len(segs); i++ {
		p := strings.Join(segs[:i], "/")
		info, err := os.Lstat(filepath.Join(root, filepath.FromSlash(p)))
		last := i == len(segs)
		if inWorkspace {
			// 段名必须与 exports 字面严格相等：大小写不敏感的文件系统上 Lstat 大小写变体也能命中
			// 同一目录，仅靠文件身份会把 EXPORTS 这类变体误判为 exports（export-output 规格的「只能是 exports」）。
			isExports := segs[i-1] == path.Base(ExportsDir) && (err == nil && exErr == nil && os.SameFile(info, exports) || err != nil)
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
		case last || !create:
			continue
		}
		if err := os.Mkdir(filepath.Join(root, filepath.FromSlash(p)), 0o755); err != nil {
			return nil, []workspace.Problem{{Code: workspace.CodeWriteFailed, Severity: workspace.SeverityError, Path: p, Message: "创建目录失败：" + err.Error()}}
		}
		created = append(created, p)
	}
	return created, nil
}

// marketingTexts 返回根单元 marketing.yaml 中作者选定的标题与文章摘要（Digest）：标题只在标题决策状态为 decided 时
// 非空（marketing.HeadlineState 推导，不把选择复制进产物），摘要为 marketing.yaml 的 digest。marketing.yaml 缺失、
// 无法加载或状态不是 decided 时对应取值为空——导出把它当作没有作者选定值处理，不新增诊断（产物校验由 wa-cli validate 负责）。
func marketingTexts(root string, w *work.Work) (decided, digest string) {
	m, _ := marketing.Load(root, w.UnitDir(w.Root))
	if m == nil {
		return "", ""
	}
	if _, h := m.HeadlineState(marketing.LoadQueue(root)); h != nil {
		decided = h.Text
	}
	return decided, m.Digest
}

// exportTitle 返回导出标题（export-markdown 的 front matter title 与可粘贴页的页面 title 共用）：
// 依次取作者选定的标题（decided，见 marketingTexts）、正文 first 中围栏代码块之外的首个一级标题、作品 id。
func exportTitle(decided, workID, first string) string {
	if decided != "" {
		return decided
	}
	if h := FirstHeading(first); h != "" {
		return h
	}
	return workID
}

// frontMatter 生成 front matter：title（依次取作者选定的标题、第一个导出单元正文中的第一个一级标题、作品 id，见
// exportTitle）、description（依次取根单元 marketing.yaml 的文章摘要（Digest）与根单元写作简报的 purpose，都没有时省略）、
// genre、units 与 accepted_at（导出单元中最晚的验收时间）。
// coverPath 非空时另含 cover（封面在资产目录中的路径，相对导出文件）。
func frontMatter(root string, w *work.Work, first string, units []string, accepted []*review.Acceptance, label, coverPath string) (string, error) {
	decided, digest := marketingTexts(root, w)
	title := exportTitle(decided, w.ID, first)
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
		Cover       string   `yaml:"cover,omitempty"`
		Disclosure  string   `yaml:"ai_disclosure,omitempty"`
	}
	v := fm{Title: title, Genre: w.Genre.ID, Units: units, AcceptedAt: latest, Cover: coverPath, Disclosure: label}
	if digest == "" {
		if b, _ := brief.Load(root, w.UnitDir(w.Root), brief.Queue{Invalid: true}); b != nil {
			digest = b.Purpose
		}
	}
	v.Description = digest
	data, err := yaml.Marshal(v)
	if err != nil {
		return "", err
	}
	return "---\n" + string(data) + "---\n\n", nil
}
