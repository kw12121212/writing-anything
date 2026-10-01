package cli

import (
	"fmt"
	"io"
	"os"
	"path/filepath"
	"strings"

	"github.com/kw12121212/writing-anything/internal/catalog"
	"github.com/kw12121212/writing-anything/internal/draft"
	"github.com/kw12121212/writing-anything/internal/feedback"
	"github.com/kw12121212/writing-anything/internal/schema"
	"github.com/kw12121212/writing-anything/internal/source"
	"github.com/kw12121212/writing-anything/internal/work"
	"github.com/kw12121212/writing-anything/internal/workspace"
)

// codeInputReadFailed 表示 --file 指定的输入文件无法读取。已发布的错误码不得改名或复用。
const codeInputReadFailed = "input.read-failed"

var fileFlag = flagSpec{"file", flagString, "输入文件，相对 --dir 或为绝对路径；省略时从标准输入读取"}

var sourceAddCommand = &command{
	name:    "source add",
	summary: "登记作者素材（Source）：原文逐字节存档到素材库（Source Library），分配稳定 id",
	flags: []flagSpec{
		dirFlag,
		{"type", flagString, "素材类型：" + strings.Join(source.Types, "、")},
		fileFlag,
		{"title", flagString, "素材标题"},
		{"id", flagString, "素材 id；省略时为 src- 加内容 SHA-256 的前 12 个字符"},
	},
	outputKeys: []outputKey{{"source", "新登记的素材：id、type、title、path（存档文件路径）与 sha256；失败时为 null"}},
	exitCodes:  standardExitCodes,
	run:        runSourceAdd,
}

var sourceListCommand = &command{
	name:       "source list",
	summary:    "列出素材库（Source Library）中已登记的素材（Source）",
	flags:      []flagSpec{dirFlag},
	outputKeys: []outputKey{{"sources", "按 id 排序的素材数组，每项含 id、type、title、path 与 sha256"}},
	exitCodes:  standardExitCodes,
	run:        runSourceList,
}

var draftWriteCommand = &command{
	name:    "draft write",
	summary: "把正文写入单元（Unit）的 draft.md，并记录每个段落的哈希与出处（Provenance）",
	args:    []argSpec{{"unit", unitRefHelp, true}},
	flags: []flagSpec{dirFlag, fileFlag,
		{"base", flagString, "作者改过正文时必填：当前 draft.md 的 SHA-256（wa-cli diff 输出的 base），声明基于作者的当前版本写入"}},
	outputKeys: []outputKey{{"draft", "写入的正文：unit、path、paragraphs（段落数）与 sha256；失败时为 null"}},
	exitCodes:  standardExitCodes,
	run:        runDraftWrite,
}

var provenanceCommand = &command{
	name:       "provenance",
	summary:    "逐段列出单元（Unit）正文的出处（Provenance），并汇总各类出处的占比",
	args:       []argSpec{{"unit", unitRefHelp, true}},
	flags:      []flagSpec{dirFlag},
	outputKeys: []outputKey{{"provenance", "出处报告：unit、path、paragraphs、summary、totalParagraphs 与 totalChars；失败时为 null"}},
	exitCodes:  standardExitCodes,
	run:        runProvenance,
}

var claimAddCommand = &command{
	name:    "claim add",
	summary: "在单元（Unit）中登记一个论断（Claim），并关联作者素材（Source）",
	args:    []argSpec{{"unit", unitRefHelp, true}},
	flags: []flagSpec{
		dirFlag,
		{"kind", flagString, "论断类型：experience、opinion、case、data 或 fact"},
		{"text", flagString, "论断内容"},
		{"sources", flagString, "关联的素材 id，用逗号分隔；省略时表示尚未关联素材"},
		{"id", flagString, "论断 id；省略时为 claim-<n>"},
	},
	outputKeys: []outputKey{{"claim", "新论断：unit、id、kind、text 与 sources；失败时为 null"}},
	exitCodes:  standardExitCodes,
	run:        runClaimAdd,
}

var claimSetCommand = &command{
	name:    "claim set",
	summary: "修改单元（Unit）中一个论断（Claim）的类型、内容或关联的素材（Source），保留其他内容与注释",
	args:    []argSpec{{"unit", unitRefHelp, true}, {"claim", "论断 id", true}},
	flags: []flagSpec{
		dirFlag,
		{"kind", flagString, "论断类型：experience、opinion、case、data 或 fact；省略时不修改"},
		{"text", flagString, "论断内容；省略时不修改"},
		{"sources", flagString, "关联的素材 id，用逗号分隔，整体替换原有关联；给出空值时清空；省略时不修改"},
	},
	outputKeys: []outputKey{{"claim", "修改后的论断：unit、id、kind、text 与 sources；失败时为 null"}},
	exitCodes:  standardExitCodes,
	run:        runClaimSet,
}

var claimListCommand = &command{
	name:       "claim list",
	summary:    "列出单元（Unit）中登记的论断（Claim）",
	args:       []argSpec{{"unit", unitRefHelp, true}},
	flags:      []flagSpec{dirFlag},
	outputKeys: []outputKey{{"claims", "按登记顺序排列的论断数组，每项含 unit、id、kind、text 与 sources"}},
	exitCodes:  standardExitCodes,
	run:        runClaimList,
}

var claimRemoveCommand = &command{
	name:       "claim remove",
	summary:    "删除单元（Unit）中的一个论断（Claim），保留其他内容与注释",
	args:       []argSpec{{"unit", unitRefHelp, true}, {"claim", "论断 id", true}},
	flags:      []flagSpec{dirFlag},
	outputKeys: []outputKey{{"removed", "被删除的论断 id；失败时为 null"}},
	exitCodes:  standardExitCodes,
	run:        runClaimRemove,
}

type sourceView struct {
	ID     string  `json:"id"`
	Type   string  `json:"type"`
	Title  *string `json:"title"`
	Path   string  `json:"path"`
	SHA256 string  `json:"sha256"`
}

func viewOf(s *source.Source) sourceView {
	v := sourceView{ID: s.ID, Type: s.Type, Path: s.FilePath(), SHA256: s.SHA256}
	if s.Title != "" {
		v.Title = &s.Title
	}
	return v
}

type sourceAddResult struct {
	Result
	Source *sourceView `json:"source"`
}

type sourceListResult struct {
	Result
	Sources []sourceView `json:"sources"`
}

type draftView struct {
	Unit       string `json:"unit"`
	Path       string `json:"path"`
	Paragraphs int    `json:"paragraphs"`
	SHA256     string `json:"sha256"`
}

type draftWriteResult struct {
	Result
	Draft *draftView `json:"draft"`
}

type paragraphView struct {
	Index  int    `json:"index"`
	Line   int    `json:"line"`
	Origin string `json:"origin"`
	Chars  int    `json:"chars"`
}

type provenanceView struct {
	Unit            string          `json:"unit"`
	Path            string          `json:"path"`
	Paragraphs      []paragraphView `json:"paragraphs"`
	Summary         []draft.Share   `json:"summary"`
	TotalParagraphs int             `json:"totalParagraphs"`
	TotalChars      int             `json:"totalChars"`
}

type provenanceResult struct {
	Result
	Provenance *provenanceView `json:"provenance"`
}

type claimView struct {
	Unit string `json:"unit"`
	draft.Claim
}

type claimAddResult struct {
	Result
	Claim *claimView `json:"claim"`
}

type claimListResult struct {
	Result
	Claims []claimView `json:"claims"`
}

type claimRemoveResult struct {
	Result
	Removed *string `json:"removed"`
}

// readInput 读取 --file 指定的文件（相对 dir 解析），省略 --file 时读取标准输入。返回内容与输入文件的文件名
// （来自标准输入时为空）。
func readInput(inv *invocation, dir string) ([]byte, string, []workspace.Problem) {
	given, ok := inv.values["file"]
	if !ok {
		data, err := io.ReadAll(inv.stdin)
		if err != nil {
			return nil, "", []workspace.Problem{{Code: codeInputReadFailed, Severity: workspace.SeverityError, Message: "读取标准输入失败：" + err.Error()}}
		}
		return data, "", nil
	}
	path := given
	if !filepath.IsAbs(path) {
		path = filepath.Join(dir, path)
	}
	data, err := os.ReadFile(path)
	if err != nil {
		return nil, "", []workspace.Problem{{Code: codeInputReadFailed, Severity: workspace.SeverityError, Message: fmt.Sprintf("无法读取输入文件 %s：%v", given, err)}}
	}
	return data, filepath.Base(path), nil
}

// helpNext 把诊断 ds 中错误码属于 codes 的下一步改为查看 name 命令的用法与 extra。
func helpNext(ds []Diagnostic, name string, codes []string, extra ...string) {
	for i := range ds {
		for _, c := range codes {
			if ds[i].Code == c {
				ds[i].Next = append([]string{"wa-cli help " + name + " --json"}, extra...)
			}
		}
	}
}

func runSourceAdd(inv *invocation) int {
	dir := projectDir(inv)
	res := &sourceAddResult{}
	problems := workspace.Require(dir)
	if problems == nil {
		var content []byte
		var name string
		if content, name, problems = readInput(inv, dir); problems == nil {
			var s *source.Source
			var created []string
			if s, created, problems = source.Add(dir, content, inv.values["type"], inv.values["title"], inv.values["id"], name); s != nil {
				v := viewOf(s)
				res.Source, res.ChangedPaths = &v, created
			}
		}
	}
	res.Diagnostics = diagnostics(problems, dir)
	helpNext(res.Diagnostics, "source add", []string{codeInputReadFailed, source.CodeEmpty, schema.CodeSchemaViolation})
	return finish(inv, res, func(w io.Writer) error {
		if res.Source == nil {
			return nil
		}
		_, err := fmt.Fprintf(w, "已登记素材（Source）%s：类型 %s，原文存档于 %s\n", res.Source.ID, res.Source.Type, res.Source.Path)
		return err
	})
}

func runSourceList(inv *invocation) int {
	dir := projectDir(inv)
	res := &sourceListResult{Sources: []sourceView{}}
	problems := workspace.Require(dir)
	if problems == nil {
		var sources []*source.Source
		sources, _, problems = source.LoadAll(dir)
		for _, s := range sources {
			res.Sources = append(res.Sources, viewOf(s))
		}
	}
	res.Diagnostics = diagnostics(problems, dir)
	return finish(inv, res, func(w io.Writer) error {
		var b strings.Builder
		if len(res.Sources) == 0 && !hasError(res.Diagnostics) {
			b.WriteString("还没有登记素材（Source）\n")
		}
		for _, s := range res.Sources {
			title := ""
			if s.Title != nil {
				title = " " + *s.Title
			}
			fmt.Fprintf(&b, "%s（%s）%s：%s\n", s.ID, s.Type, title, s.Path)
		}
		_, err := io.WriteString(w, b.String())
		return err
	})
}

func runDraftWrite(inv *invocation) int {
	dir := projectDir(inv)
	ref := inv.args[0]
	res := &draftWriteResult{}
	problems := workspace.Require(dir)
	if problems == nil {
		var content []byte
		if content, _, problems = readInput(inv, dir); problems == nil {
			var w *work.Work
			var u *work.Unit
			if w, u, problems = work.Resolve(dir, ref); u != nil {
				base := inv.values["base"]
				var imported []string
				// 检查通过后、覆盖正文之前，把当前正文中的批注记入反馈，使它们不因新正文而丢失。
				if problems = draft.Precheck(dir, w, u, content, base); problems == nil {
					imported, problems = feedback.ImportComments(dir, w, u)
				}
				var written *draft.Written
				var changed []string
				if problems == nil {
					if written, changed, problems = draft.Write(dir, w, u, content, base); written != nil {
						res.Draft = &draftView{Unit: u.Ref, Path: written.Path, Paragraphs: written.Paragraphs, SHA256: written.SHA256}
					}
				}
				res.ChangedPaths = append(changed, imported...)
			}
		}
	}
	res.Diagnostics = diagnostics(problems, dir)
	helpNext(res.Diagnostics, "draft write", []string{codeInputReadFailed, draft.CodeEmpty, draft.CodeTooLarge})
	return finish(inv, res, func(w io.Writer) error {
		if res.Draft == nil {
			return nil
		}
		verb := "已写入"
		if len(res.ChangedPaths) == 0 {
			verb = "正文未变化，未写入"
		}
		_, err := fmt.Fprintf(w, "%s单元（Unit）%s 的正文 %s：%d 个段落\n", verb, res.Draft.Unit, res.Draft.Path, res.Draft.Paragraphs)
		return err
	})
}

// originNames 是出处取值的人读名称。
var originNames = map[string]string{draft.OriginHuman: "作者", draft.OriginAgent: "Agent", draft.OriginEdited: "Agent 起草、作者修改"}

func runProvenance(inv *invocation) int {
	dir := projectDir(inv)
	res := &provenanceResult{}
	problems := workspace.Require(dir)
	if problems == nil {
		var w *work.Work
		var u *work.Unit
		if w, u, problems = work.Resolve(dir, inv.args[0]); u != nil {
			var r *draft.Report
			if r, problems = draft.Provenance(dir, w, u); r != nil {
				v := &provenanceView{Unit: u.Ref, Path: r.Path, Paragraphs: []paragraphView{}, Summary: r.Summary,
					TotalParagraphs: len(r.Paragraphs), TotalChars: r.TotalChars}
				for i, p := range r.Paragraphs {
					v.Paragraphs = append(v.Paragraphs, paragraphView{Index: i + 1, Line: p.Line, Origin: p.Origin, Chars: p.Chars})
				}
				res.Provenance = v
			}
		}
	}
	res.Diagnostics = diagnostics(problems, dir)
	return finish(inv, res, func(w io.Writer) error {
		v := res.Provenance
		if v == nil {
			return nil
		}
		var b strings.Builder
		fmt.Fprintf(&b, "单元（Unit）%s 的正文 %s：%d 个段落，%d 个字符\n", v.Unit, v.Path, v.TotalParagraphs, v.TotalChars)
		for _, s := range v.Summary {
			fmt.Fprintf(&b, "  %s（%s）：%d 段，%d 字符，占 %.2f%%\n", originNames[s.Origin], s.Origin, s.Paragraphs, s.Chars, s.Ratio*100)
		}
		for _, p := range v.Paragraphs {
			fmt.Fprintf(&b, "  第 %d 段（第 %d 行）：%s\n", p.Index, p.Line, p.Origin)
		}
		_, err := io.WriteString(w, b.String())
		return err
	})
}

func runClaimAdd(inv *invocation) int {
	dir := projectDir(inv)
	res := &claimAddResult{}
	problems := workspace.Require(dir)
	edit := false
	if problems == nil {
		var w *work.Work
		var u *work.Unit
		if w, u, problems = work.Resolve(dir, inv.args[0]); u != nil {
			sources := splitSources(inv.values["sources"])
			var c *draft.Claim
			var changed []string
			if c, changed, problems = draft.AddClaim(dir, w, u, inv.values["kind"], inv.values["text"], inv.values["id"], sources); c != nil {
				res.Claim, res.ChangedPaths = &claimView{Unit: u.Ref, Claim: *c}, changed
			}
			edit = true
		}
	}
	res.Diagnostics = diagnostics(problems, dir)
	if edit {
		claimParamNext(res.Diagnostics, "claim add", dir)
	}
	return finish(inv, res, func(w io.Writer) error {
		if res.Claim == nil {
			return nil
		}
		_, err := fmt.Fprintf(w, "已在单元（Unit）%s 登记论断（Claim）%s（%s）：%s\n", res.Claim.Unit, res.Claim.ID, res.Claim.Kind, claimSourcesText(res.Claim.Sources))
		return err
	})
}

// splitSources 把逗号分隔的素材 id 切开，去掉空白与空项；结果不为 nil。
func splitSources(v string) []string {
	out := []string{}
	for _, s := range strings.Split(v, ",") {
		if s = strings.TrimSpace(s); s != "" {
			out = append(out, s)
		}
	}
	return out
}

func claimSourcesText(sources []string) string {
	if len(sources) == 0 {
		return "尚未关联素材"
	}
	return "关联素材 " + strings.Join(sources, "、")
}

// claimParamNext 把论断参数引起的问题（参数尚未写入，整体校验检不出）的下一步改为查看 name 的用法与素材列表。
// 已有 claims.yaml 中的问题（字段以 claims 开头）保留默认的复查命令。
func claimParamNext(ds []Diagnostic, name, dir string) {
	var own []Diagnostic
	var idx []int
	for i, d := range ds {
		if d.Code == draft.CodeAnchoredValue || paramField(d.Field, "id", "kind", "text", "sources") {
			own, idx = append(own, d), append(idx, i)
		}
	}
	helpNext(own, name, []string{schema.CodeSchemaViolation, draft.CodeUnknownSource, catalog.CodeDuplicateID, draft.CodeAnchoredValue},
		shellCommand("wa-cli", "source", "list", "--dir", dir, "--json"))
	for k, i := range idx {
		ds[i] = own[k]
	}
}

// claimNotFoundNext 把 claim.not-found 的下一步改为列出该单元已有的论断。
func claimNotFoundNext(ds []Diagnostic, ref, dir string) {
	for i := range ds {
		if ds[i].Code == draft.CodeClaimNotFound {
			ds[i].Next = []string{shellCommand("wa-cli", "claim", "list", ref, "--dir", dir, "--json")}
		}
	}
}

func runClaimList(inv *invocation) int {
	dir := projectDir(inv)
	res := &claimListResult{Claims: []claimView{}}
	problems := workspace.Require(dir)
	if problems == nil {
		var w *work.Work
		var u *work.Unit
		if w, u, problems = work.Resolve(dir, inv.args[0]); u != nil {
			var cs []draft.Claim
			cs, problems = draft.ListClaims(dir, w, u)
			for _, c := range cs {
				if c.Sources == nil {
					c.Sources = []string{}
				}
				res.Claims = append(res.Claims, claimView{Unit: u.Ref, Claim: c})
			}
		}
	}
	res.Diagnostics = diagnostics(problems, dir)
	return finish(inv, res, func(w io.Writer) error {
		var b strings.Builder
		if len(res.Claims) == 0 && !hasError(res.Diagnostics) {
			fmt.Fprintf(&b, "单元（Unit）%s 还没有登记论断（Claim）\n", inv.args[0])
		}
		for _, c := range res.Claims {
			fmt.Fprintf(&b, "%s（%s）%s：%s\n", c.ID, c.Kind, c.Text, claimSourcesText(c.Sources))
		}
		_, err := io.WriteString(w, b.String())
		return err
	})
}

func runClaimSet(inv *invocation) int {
	dir := projectDir(inv)
	res := &claimAddResult{}
	problems := workspace.Require(dir)
	edit := false
	if problems == nil {
		var w *work.Work
		var u *work.Unit
		if w, u, problems = work.Resolve(dir, inv.args[0]); u != nil {
			var kind, text *string
			var sources *[]string
			if v, ok := inv.values["kind"]; ok {
				kind = &v
			}
			if v, ok := inv.values["text"]; ok {
				text = &v
			}
			if v, ok := inv.values["sources"]; ok {
				s := splitSources(v)
				sources = &s
			}
			var c *draft.Claim
			var changed []string
			if c, changed, problems = draft.SetClaim(dir, w, u, inv.args[1], kind, text, sources); c != nil {
				res.Claim, res.ChangedPaths = &claimView{Unit: u.Ref, Claim: *c}, changed
			}
			edit = true
		}
	}
	res.Diagnostics = diagnostics(problems, dir)
	if edit {
		claimParamNext(res.Diagnostics, "claim set", dir)
		claimNotFoundNext(res.Diagnostics, inv.args[0], dir)
	}
	return finish(inv, res, func(w io.Writer) error {
		if res.Claim == nil {
			return nil
		}
		verb := "已修改"
		if len(res.ChangedPaths) == 0 {
			verb = "取值未变，未修改"
		}
		_, err := fmt.Fprintf(w, "%s单元（Unit）%s 的论断（Claim）%s（%s）：%s\n", verb, res.Claim.Unit, res.Claim.ID, res.Claim.Kind, claimSourcesText(res.Claim.Sources))
		return err
	})
}

func runClaimRemove(inv *invocation) int {
	dir := projectDir(inv)
	res := &claimRemoveResult{}
	problems := workspace.Require(dir)
	if problems == nil {
		var w *work.Work
		var u *work.Unit
		if w, u, problems = work.Resolve(dir, inv.args[0]); u != nil {
			var changed []string
			var ok bool
			if ok, changed, problems = draft.RemoveClaim(dir, w, u, inv.args[1]); ok {
				id := inv.args[1]
				res.Removed, res.ChangedPaths = &id, changed
			}
		}
	}
	res.Diagnostics = diagnostics(problems, dir)
	claimNotFoundNext(res.Diagnostics, inv.args[0], dir)
	helpNext(res.Diagnostics, "claim remove", []string{draft.CodeAnchoredValue})
	return finish(inv, res, func(w io.Writer) error {
		if res.Removed == nil {
			return nil
		}
		_, err := fmt.Fprintf(w, "已删除单元（Unit）%s 的论断（Claim）%s\n", inv.args[0], *res.Removed)
		return err
	})
}
