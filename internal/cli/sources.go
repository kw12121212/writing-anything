package cli

import (
	"fmt"
	"io"
	"maps"
	"os"
	"path/filepath"
	"slices"
	"strings"

	"github.com/kw12121212/writing-anything/internal/canon"
	"github.com/kw12121212/writing-anything/internal/catalog"
	"github.com/kw12121212/writing-anything/internal/decision"
	"github.com/kw12121212/writing-anything/internal/draft"
	"github.com/kw12121212/writing-anything/internal/feedback"
	"github.com/kw12121212/writing-anything/internal/lint"
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
		{"url", flagString, "一手网页地址（http 或 https，长度上限 2048）；可选，只登记不抓取"},
		{"tier", flagString, "信源等级（Source Tier）：primary（一手，作者亲历或原始出处）或 secondary（二手转述，引用转述引语时须向作者明示）；可选"},
	},
	outputKeys: []outputKey{{"source", "新登记的素材：id、type、title、path（存档文件路径）与 sha256，给出时另含 url 与 tier；失败时为 null"}},
	exitCodes:  standardExitCodes,
	run:        runSourceAdd,
}

var sourceListCommand = &command{
	name:       "source list",
	summary:    "列出素材库（Source Library）中已登记的素材（Source）",
	flags:      []flagSpec{dirFlag},
	outputKeys: []outputKey{{"sources", "按 id 排序的素材数组，每项含 id、type、title、path 与 sha256，给出时另含 url 与 tier（信源等级（Source Tier））"}},
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
		{"decision", flagString, "授权以该类型登记的决策 id：写入校验拒绝时生成的阻塞决策经作者选择允许后，用其 id 重试"},
		{"text", flagString, "论断内容"},
		{"sources", flagString, "关联的素材 id，用逗号分隔；省略时表示尚未关联素材"},
		{"id", flagString, "论断 id；省略时为 claim-<n>"},
	},
	outputKeys: []outputKey{{"claim", "新论断：unit、id、kind、text、sources 与 decision（授权以该类型登记的决策 id，给出时）；失败时为 null"}},
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
		{"decision", flagString, "授权以该类型登记的决策 id：写入校验拒绝（--kind 或 --sources 触发）时生成的阻塞决策经作者允许后使用"},
		{"text", flagString, "论断内容；省略时不修改"},
		{"sources", flagString, "关联的素材 id，用逗号分隔，整体替换原有关联；给出空值时清空；省略时不修改"},
	},
	outputKeys: []outputKey{{"claim", "修改后的论断：unit、id、kind、text、sources 与 decision（授权以该类型登记的决策 id，给出时）；失败时为 null"}},
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
	URL    string  `json:"url,omitempty"`
	Tier   string  `json:"tier,omitempty"`
	Path   string  `json:"path"`
	SHA256 string  `json:"sha256"`
}

func viewOf(s *source.Source) sourceView {
	v := sourceView{ID: s.ID, Type: s.Type, Path: s.FilePath(), SHA256: s.SHA256, URL: s.URL, Tier: s.Tier}
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

// concreteHelpNext 把诊断 ds 中错误码属于 codes 的下一步改为具体命令 first 在前、查看 name 命令的用法在后
// （生命周期主路径命令，见 cli 主规格 diagnostic-next-concrete）。
func concreteHelpNext(ds []Diagnostic, first, name string, codes []string) {
	for i := range ds {
		if slices.Contains(codes, ds[i].Code) {
			ds[i].Next = []string{first, "wa-cli help " + name + " --json"}
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
			if s, created, problems = source.Add(dir, content, inv.values["type"], inv.values["title"], inv.values["id"], name, inv.values["url"], inv.values["tier"]); s != nil {
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
				if problems = draft.Precheck(dir, w, u, content, base, now()); problems == nil {
					imported, problems = feedback.ImportComments(dir, w, u)
				}
				var written *draft.Written
				var changed []string
				if problems == nil {
					if written, changed, problems = draft.Write(dir, w, u, content, base, now()); written != nil {
						res.Draft = &draftView{Unit: u.Ref, Path: written.Path, Paragraphs: written.Paragraphs, SHA256: written.SHA256}
					}
				}
				res.ChangedPaths = append(changed, imported...)
			}
		}
	}
	res.Diagnostics = diagnostics(problems, dir)
	concreteHelpNext(res.Diagnostics, shellCommand("wa-cli", "context", inv.args[0], "--dir", dir, "--json"), "draft write",
		[]string{codeInputReadFailed, draft.CodeEmpty, draft.CodeTooLarge, draft.CodeEditsAnchored})
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
			fmt.Fprintf(&b, "  %s（%s）：%d 段，%d 字符，占 %.2f%%\n", draft.OriginNames[s.Origin], s.Origin, s.Paragraphs, s.Chars, s.Ratio*100)
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
			var gateTriggered bool
			gateTriggered, problems = claimAuthorSourceGate(dir, w, u, inv, res)
			if problems == nil && inv.values["decision"] != "" && !gateTriggered {
				// --decision 只用于授权写入校验的拒绝路径：校验未触发时它是参数错误，不得把未核对的决策 id 写进论断。
				problems = []workspace.Problem{{Code: codeInvalidValue, Severity: workspace.SeverityError, Field: "decision",
					Message: "--decision 只在论断类型×素材触发写入校验拒绝时使用（授权作者允许的例外）；本次输入没有触发拒绝，不需要 --decision"}}
			}
			if problems == nil {
				if c, changed, problems = draft.AddClaim(dir, w, u, inv.values["kind"], inv.values["text"], inv.values["id"], sources, inv.values["decision"]); c != nil {
					res.Claim, res.ChangedPaths = &claimView{Unit: u.Ref, Claim: *c}, changed
				}
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

// claimAuthorSourceGate 是 claim add / claim set --kind 的写入校验：按合并结果生效的 claim-sources 规则（与 lint 共用
// 同一张规则表），kind 为需要作者亲历素材的类型、给出了素材且全部素材类型都不算亲历时拒绝（claim.needs-author-source），
// 建议 --kind fact 或先登记亲历素材。未关联素材（显式未完成，由 claim set 补齐）、素材读不到（由存在性检查报告）、
// 合并失败或没有生效的 claim-sources 规则时不拦。
func claimAuthorSourceGate(dir string, w *work.Work, u *work.Unit, inv *invocation, res *claimAddResult) (triggered bool, problems []workspace.Problem) {
	kind, sources, text := inv.values["kind"], splitSources(inv.values["sources"]), inv.values["text"]
	if len(sources) == 0 {
		return false, nil
	}
	r, ps := workResolver(dir, w)()
	if r == nil || len(ps) > 0 {
		return false, nil
	}
	srcTypes := make([]string, 0, len(sources))
	var got []string
	for _, id := range sources {
		typ := ""
		if src, _ := source.Load(dir, id); src != nil {
			typ = src.Type
		}
		if typ == "" {
			return false, nil
		}
		srcTypes = append(srcTypes, typ)
		got = append(got, id+"（"+typ+"）")
	}
	for _, id := range slices.Sorted(maps.Keys(r.Rules)) {
		rule := r.Rules[id]
		if !rule.Enabled || fmt.Sprint(rule.Type.Value) != "claim-sources" {
			continue
		}
		kinds, sourceTypes, valid := lint.ClaimSourceRequirement(rule)
		if !valid {
			continue // 参数非法：lint 报 rule.invalid-params 且不执行该规则，写入校验同样跳过
		}
		if !lint.LacksAuthorSource(kind, srcTypes, kinds, sourceTypes) {
			continue
		}
		// 留痕例外通道（refusal-side-decision-gating）：--decision 授权核对；未给出时幂等生成阻塞决策。
		// add 路径按固定文本构造；set 路径的合成 invocation 直接给出 set 形态的问题文本（question 键）。
		question := inv.values["question"]
		if question == "" {
			question = claimKindQuestion(u.Ref, kind, text)
		}
		if decisionID := inv.values["decision"]; decisionID != "" {
			if msg := claimRejectDecision(dir, question, decisionID); msg != "" {
				return true, []workspace.Problem{{Code: draft.CodeNeedsAuthorSource, Severity: workspace.SeverityError, Field: "decision",
					Message: msg}}
			}
			return true, nil // 作者已允许：通过拒绝，决策 id 由调用方写进论断
		}
		changed, p := claimEnsureBlockingDecision(dir, u, question, kind, got, sourceTypes)
		if len(changed) > 0 && res != nil {
			res.ChangedPaths = append(res.ChangedPaths, changed...)
		}
		return true, []workspace.Problem{p}
	}
	return false, nil
}

// claimKindQuestion 是授权「以需亲历素材的类型登记纯外部论断」的固定问题文本（逐字测试：按文本识别对象与操作，
// 与规格 claim-add 的固定文本一致）。
func claimKindQuestion(ref, kind, text string) string {
	return fmt.Sprintf("论断（Claim）%s 以 %s 登记「%s」只关联了非作者亲历素材，允许吗？", ref, kind, text)
}

// claimSetKindQuestion 是 claim set --kind 的固定问题文本：以「<单元>/<论断 id> 改为 <kind>」标识（规格 claim-edit）。
func claimSetKindQuestion(ref, id, kind string) string {
	return fmt.Sprintf("论断（Claim）%s/%s 改为 %s 且只关联了非作者亲历素材，允许吗？", ref, id, kind)
}

// claimEnsureBlockingDecision 在拒绝路径上幂等生成阻塞决策（选项 允许 与 保持不变，推荐保持不变）：
// 同问题文本且同单元的 open 决策已存在时不重复追加，作者已选保持不变的已决定决策同样抑制追加，
// 作者允许后的再次未授权尝试追加新的阻塞决策。返回追加的路径（未追加时为空）与拒绝问题。
func claimEnsureBlockingDecision(dir string, u *work.Unit, question, kind string, got, sourceTypes []string) ([]string, workspace.Problem) {
	reject := func(msg string) ([]string, workspace.Problem) {
		return nil, workspace.Problem{Code: draft.CodeNeedsAuthorSource, Severity: workspace.SeverityError, Field: "kind",
			Message: fmt.Sprintf("论断（Claim）类型 %s 需要关联作者亲历素材（%s 类型的素材（Source）），给出的素材全部为其他类型：%s；外部报道的事实请改用 --kind fact 登记，或先用 wa-cli source add 登记作者亲历素材再关联；%s",
				kind, strings.Join(sourceTypes, "、"), strings.Join(got, "、"), msg)}
	}
	ds, _, ps := decision.Load(dir)
	if len(ps) > 0 {
		return reject("决策队列无法加载，未生成阻塞决策；请先修复 " + decision.Path)
	}
	for _, d := range ds {
		if d.Question != question || !slices.Contains(d.Units, u.Ref) {
			continue
		}
		if d.Status == decision.StatusOpen {
			return reject("已生成阻塞决策 " + d.ID + "，等待作者决定；作者允许后用 --decision " + d.ID + " 重试")
		}
		if d.Status == decision.StatusDecided && d.Choice != nil && *d.Choice != canon.ChoiceAllow {
			return reject("作者已决定保持不变（决策 " + d.ID + "），不能以该类型登记")
		}
	}
	rec, ps2 := decision.Add(dir, decision.Input{Question: question,
		Options:   []string{"允许", "保持不变"},
		Recommend: "b",
		Rationale: "需要亲历素材的论断类型只关联了非亲历素材：默认改用 fact 或补登亲历素材，等待作者决定是否允许例外",
		Blocking:  true,
		Units:     []string{u.Ref}}, func(ref string) []workspace.Problem { return nil })
	if len(ps2) > 0 {
		return reject("生成阻塞决策失败：" + ps2[0].Message)
	}
	return []string{decision.Path}, workspace.Problem{Code: draft.CodeNeedsAuthorSource, Severity: workspace.SeverityError, Field: "kind",
		Message: fmt.Sprintf("论断（Claim）类型 %s 需要关联作者亲历素材（%s 类型的素材（Source）），给出的素材全部为其他类型：%s；外部报道的事实请改用 --kind fact 登记，或先用 wa-cli source add 登记作者亲历素材再关联；已生成阻塞决策 %s，作者允许后用 --decision %s 重试",
			kind, strings.Join(sourceTypes, "、"), strings.Join(got, "、"), rec.ID, rec.ID)}
}

// claimRejectDecision 核对 --decision 能否授权：决策存在、问题匹配、已决定且作者选择允许；不能时返回原因。
func claimRejectDecision(dir, question, decisionID string) string {
	ds, _, ps := decision.Load(dir)
	if len(ps) > 0 {
		return "决策 " + decisionID + " 不能授权这次登记：决策队列无法加载，未能核对"
	}
	for _, d := range ds {
		if d.ID != decisionID {
			continue
		}
		switch {
		case d.Question != question:
			return "决策 " + decisionID + " 不是为该论断生成的，不能授权"
		case d.Status != decision.StatusDecided:
			return "决策 " + decisionID + " 尚未由作者决定，不能授权这次登记"
		case d.Choice == nil || *d.Choice != canon.ChoiceAllow:
			return "决策 " + decisionID + " 中作者选择了保持不变，不能以该类型登记"
		default:
			return ""
		}
	}
	return "决策 " + decisionID + " 不存在"
}

// claimSetGate 是 claim set 的写入校验：按替换后的 kind 与素材（--kind 与 --sources 都省略时不触发）复用
// claimAuthorSourceGate。论断不存在时交由 SetClaim 报告 claim.not-found，不在这里拦。
func claimSetGate(dir string, w *work.Work, u *work.Unit, id string, kind, text *string, sources *[]string, decisionID string, res *claimAddResult) (bool, []workspace.Problem) {
	if kind == nil && sources == nil {
		return false, nil
	}
	// LoadClaims 在 claims.yaml 有任何问题（含其他论断的悬空引用）时返回 nil：此时放行，由 SetClaim 自己的加载
	// 报告问题（悬空引用降为 warning 照常写入），写入校验不叠加一道更严的门槛。
	cs, _ := draft.LoadClaims(dir, w, u)
	effectiveKind, effectiveSources := "", []string{}
	found := false
	for _, c := range cs {
		if c.ID != id {
			continue
		}
		found = true
		effectiveKind, effectiveSources = c.Kind, c.Sources
	}
	if !found {
		return false, nil
	}
	if kind != nil {
		effectiveKind = *kind
	}
	if sources != nil {
		effectiveSources = *sources
	}
	if len(effectiveSources) == 0 {
		return false, nil
	}
	// 与 add 相同的拒绝与授权通道；问题文本以「<单元>/<论断 id> 改为 <kind>」标识（与 add 的文本形态不同，
	// 避免 add 生成的决策授权 set 或同文本论断串扰）。
	for _, c := range cs {
		if c.ID != id {
			continue
		}
		gateInv := &invocation{values: map[string]string{"kind": effectiveKind, "question": claimSetKindQuestion(u.Ref, id, effectiveKind),
			"decision": decisionID, "sources": strings.Join(effectiveSources, ",")}}
		return claimAuthorSourceGate(dir, w, u, gateInv, res)
	}
	return false, nil
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
		if d.Code == draft.CodeAnchoredValue || paramField(d.Field, "id", "kind", "text", "sources", "decision") {
			own, idx = append(own, d), append(idx, i)
		}
	}
	helpNext(own, name, []string{schema.CodeSchemaViolation, draft.CodeUnknownSource, catalog.CodeDuplicateID, draft.CodeAnchoredValue, draft.CodeNeedsAuthorSource},
		shellCommand("wa-cli", "source", "list", "--dir", dir, "--json"))
	for k, i := range idx {
		ds[i] = own[k]
	}
	// 阻塞决策相关的拒绝/授权问题：先看决策队列。
	for i, d := range ds {
		if d.Code == draft.CodeNeedsAuthorSource && (d.Field == "decision" || strings.Contains(d.Message, "已生成阻塞决策") || strings.Contains(d.Message, "保持不变")) {
			ds[i].Next = append([]string{shellCommand("wa-cli", "decisions", "--dir", dir, "--json")}, ds[i].Next...)
		}
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
			var gateTriggered bool
			gateTriggered, problems = claimSetGate(dir, w, u, inv.args[1], kind, text, sources, inv.values["decision"], res)
			if problems == nil && inv.values["decision"] != "" && !gateTriggered {
				problems = []workspace.Problem{{Code: codeInvalidValue, Severity: workspace.SeverityError, Field: "decision",
					Message: "--decision 只在论断类型×素材触发写入校验拒绝时使用（授权作者允许的例外）；本次输入没有触发拒绝，不需要 --decision"}}
			}
			if problems == nil {
				if c, changed, problems = draft.SetClaim(dir, w, u, inv.args[1], kind, text, sources, inv.values["decision"]); c != nil {
					res.Claim, res.ChangedPaths = &claimView{Unit: u.Ref, Claim: *c}, changed
				}
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
