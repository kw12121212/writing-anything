// Package preview 生成阅读页（Reading View）：按单元树先序逐段展示单元正文的原文与出处（Provenance），并在所在段落之后列出
// 文本检查（Lint）与审稿（Review）发现项（Finding）。本包只读取产物并生成 HTML，不修改任何文件；文本检查由调用方传入，
// 本包不重新合并配置。
package preview

import (
	"bytes"
	"crypto/sha256"
	"encoding/base64"
	"fmt"
	"html/template"
	"strings"

	"github.com/kw12121212/writing-anything/internal/draft"
	"github.com/kw12121212/writing-anything/internal/lint"
	"github.com/kw12121212/writing-anything/internal/review"
	"github.com/kw12121212/writing-anything/internal/work"
	"github.com/kw12121212/writing-anything/internal/workspace"
)

// 阅读页的错误码（Error Code）。已发布的错误码不得改名或复用。
const (
	CodeInvalidOutput = "preview.invalid-output"
	CodeListenFailed  = "preview.listen-failed"
	CodeClosing       = "preview.closing"
)

// MaxBytes 是阅读页展示的单个单元正文的字节数上限，与文本检查一致（workspace-artifacts/input-limits）。
const MaxBytes = lint.MaxBytes

// Finding 是显示在阅读页中的一项发现项：文本检查（Kind 为 lint）或审稿（Kind 为 review）。
type Finding struct {
	Kind       string // lint 或 review
	Label      string // 文本检查为 severity 与规则 id，审稿为发现项 id 与状态
	Severity   string // 文本检查的 severity；审稿为空
	Message    string
	Suggestion string
}

// Paragraph 是阅读页中的一个段落。
type Paragraph struct {
	Index    int // 从 1 开始
	Line     int
	Text     string
	Origin   string // human、agent、agent-draft-human-edited；无法推导时为空
	Findings []Finding
}

// Unit 是阅读页中的一个单元。
type Unit struct {
	Ref        string
	Stage      string
	HasDraft   bool
	Notes      []string  // 文本检查无法运行、出处无法推导等说明
	Global     []Finding // 全文级发现项与无法归入段落的发现项
	Paragraphs []Paragraph
}

// Page 是一个阅读页。Serve 为 true 时页面内联提交意见、作者确认与复核的脚本，并列出待作者确认的事项（Confirms）；
// Strict 为 true 时另列出待作者复核（Pending Author Review）的命令行渠道确认（Reviews）。
type Page struct {
	Title    string
	Units    []Unit
	Serve    bool
	Confirms []Confirm
	Strict   bool
	Reviews  []Review
	// Failures 是最近失败的命令摘要（审计日志（Audit Log）的最近 5 条 exit 非 0 记录），只读展示（preview-audit-summary）。
	Failures []AuditFailure
}

// AuditFailure 是阅读页最近失败区块中的一项。
type AuditFailure struct {
	At      string
	Command string
	Exit    int
}

// Option 是待确认决策的一个选项。
type Option struct {
	ID   string
	Text string
}

// Confirm 是阅读页待作者确认队列中的一项：Kind 为 acceptance、decision、plan、state、knowledge 或 volume，Unit 与 ID
// 标识目标（按 Kind 取用），Label 与 Detail 为人读说明；决策带选项（Options）或需要答复（NeedsAnswer），状态与知情
// 提议可以拒绝（CanReject）。
type Confirm struct {
	Kind        string
	Unit        string
	ID          string
	Label       string
	Detail      string
	Options     []Option
	Recommend   string
	NeedsAnswer bool
	CanReject   bool
	// Units 是事项关联的单元引用（作品级事项为作品根单元），供 wa-cli next 的 pendingConfirmations 使用，不进入阅读页。
	Units []string
}

// Review 是阅读页待作者复核区的一项：Key 为待复核确认的键，Label 为人读说明。
type Review struct {
	Key   string
	Label string
}

// LintFunc 对单元运行文本检查，结果与 wa-cli lint 相同；out 为 nil 时文本检查没有运行，problems 为其原因。
type LintFunc func(u *work.Unit) (out *lint.Result, problems []workspace.Problem)

// Subtree 按单元树先序返回单元 u 及其全部后代。
func Subtree(u *work.Unit) []*work.Unit {
	out := []*work.Unit{u}
	for _, c := range u.Units {
		out = append(out, Subtree(c)...)
	}
	return out
}

// Build 读取 units 中每个单元的正文、出处、文本检查与审稿结果，生成阅读页的数据。
func Build(root string, w *work.Work, units []*work.Unit, run LintFunc) *Page {
	p := &Page{Title: units[0].Ref}
	for _, u := range units {
		p.Units = append(p.Units, buildUnit(root, w, u, run))
	}
	return p
}

func buildUnit(root string, w *work.Work, u *work.Unit, run LintFunc) Unit {
	out := Unit{Ref: u.Ref, Stage: u.Stage}
	content, ok, ps := draft.Read(root, w, u)
	switch {
	case len(ps) > 0:
		out.Notes = append(out.Notes, messages("无法读取正文：", ps)...)
		return out
	case !ok:
		return out
	case len(content) > MaxBytes:
		out.Notes = append(out.Notes, fmt.Sprintf("正文有 %d 字节，超过阅读页的上限 %d 字节，不展示；请拆分为多个单元", len(content), MaxBytes))
		return out
	}
	out.HasDraft = true
	paras := draft.Split(content)
	if rep, ps := draft.Provenance(root, w, u); len(ps) > 0 {
		out.Notes = append(out.Notes, messages("无法推导段落出处：", ps)...)
	} else {
		paras = rep.Paragraphs
	}
	for i, para := range paras {
		out.Paragraphs = append(out.Paragraphs, Paragraph{Index: i + 1, Line: para.Line, Text: para.Text, Origin: para.Origin})
	}
	if res, ps := run(u); res == nil {
		out.Notes = append(out.Notes, messages("文本检查没有运行：", ps)...)
	} else {
		out.Notes = append(out.Notes, messages("文本检查提示：", ps)...)
		for _, f := range res.Findings {
			label := f.Severity + " " + f.Rule
			out.place(f.Line, Finding{Kind: "lint", Label: label, Severity: f.Severity, Message: f.Message, Suggestion: f.Suggestion})
		}
	}
	r, ps := review.Load(root, w.UnitDir(u))
	switch {
	case len(ps) > 0:
		out.Notes = append(out.Notes, messages("审稿记录 review.yaml 不通过校验：", ps)...)
	case r != nil:
		if r.DraftSHA256 != review.SHA256(content) {
			out.Notes = append(out.Notes, "审稿基于旧版正文：正文在审稿之后改过，段落编号可能已经变化")
		}
		for _, f := range r.Findings {
			rf := Finding{Kind: "review", Label: "审稿 " + f.ID + "（" + f.Status + "）", Message: f.Message, Suggestion: deref(f.Suggestion)}
			if f.Paragraph >= 1 && f.Paragraph <= len(out.Paragraphs) {
				out.Paragraphs[f.Paragraph-1].Findings = append(out.Paragraphs[f.Paragraph-1].Findings, rf)
			} else {
				rf.Label += fmt.Sprintf("，第 %d 段已不存在", f.Paragraph)
				out.Global = append(out.Global, rf)
			}
		}
	}
	return out
}

// place 把第 line 行（从 1 开始；0 为全文级）的发现项放到所在段落之后；不在任何段落中时放到单元开头。
func (u *Unit) place(line int, f Finding) {
	for i := range u.Paragraphs {
		p := &u.Paragraphs[i]
		if line >= p.Line && line <= p.Line+strings.Count(p.Text, "\n") {
			p.Findings = append(p.Findings, f)
			return
		}
	}
	u.Global = append(u.Global, f)
}

func messages(prefix string, ps []workspace.Problem) []string {
	var out []string
	for _, p := range ps {
		msg := p.Message
		if p.Origin != "" {
			msg = p.Origin + "：" + msg
		}
		out = append(out, prefix+msg+"（"+p.Code+"）")
	}
	return out
}

func deref(s *string) string {
	if s == nil {
		return ""
	}
	return *s
}

// ScriptCSP 是 serve 模式响应头 Content-Security-Policy 的取值：只放行哈希与内联脚本一致的脚本、内联样式与同源请求。
var ScriptCSP = func() string {
	sum := sha256.Sum256([]byte(pageScript))
	return "default-src 'none'; style-src 'unsafe-inline'; script-src 'sha256-" + base64.StdEncoding.EncodeToString(sum[:]) +
		"'; connect-src 'self'; base-uri 'none'; form-action 'none'; frame-ancestors 'none'"
}()

var page = template.Must(template.New("page").Funcs(template.FuncMap{
	"script": func() template.JS { return template.JS(pageScript) },
	"originName": func(o string) string {
		if n, ok := draft.OriginNames[o]; ok {
			return n
		}
		return "出处未知"
	},
}).Parse(pageHTML))

// Render 生成阅读页的 HTML 文档。
func Render(p *Page) ([]byte, error) {
	var b bytes.Buffer
	if err := page.Execute(&b, p); err != nil {
		return nil, err
	}
	return b.Bytes(), nil
}
