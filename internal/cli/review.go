package cli

import (
	"fmt"
	"io"
	"slices"
	"strings"
	"time"

	"github.com/kw12121212/writing-anything/internal/catalog"
	"github.com/kw12121212/writing-anything/internal/closeout"
	"github.com/kw12121212/writing-anything/internal/continuity"
	"github.com/kw12121212/writing-anything/internal/decision"
	"github.com/kw12121212/writing-anything/internal/draft"
	"github.com/kw12121212/writing-anything/internal/feedback"
	"github.com/kw12121212/writing-anything/internal/resolve"
	"github.com/kw12121212/writing-anything/internal/review"
	"github.com/kw12121212/writing-anything/internal/schema"
	"github.com/kw12121212/writing-anything/internal/series"
	"github.com/kw12121212/writing-anything/internal/volume"
	"github.com/kw12121212/writing-anything/internal/work"
	"github.com/kw12121212/writing-anything/internal/workspace"
)

// now 是验收记录的时间来源；测试可以替换。
var now = time.Now

const reviewViewHelp = "unit、path、stale（审稿之后正文是否被修改）、draftSha256、rubric（每项含 item、title、verdict、evidence、paragraphs 与 blocking）与 findings（每项含 id、rubric、paragraph、quote、message、suggestion、status 与 note）"

var reviewRecordCommand = &command{
	name: "review record",
	summary: "记录单元（Unit）的审稿（Review）：Agent 的 AI 预审（Pre-review）按评分标准（Rubric）逐项给出结论、证据与段落，" +
		"并列出发现项（Finding）；整体替换已有的 review.yaml",
	args:       []argSpec{{"unit", unitRefHelp, true}},
	flags:      []flagSpec{dirFlag, {"file", flagString, "审稿输入（YAML）：rubric 为结论数组（item、verdict: pass|fail、evidence、paragraphs），findings 为发现项数组（paragraph、message，可选 id、rubric、quote、suggestion）；相对 --dir 或为绝对路径，省略时从标准输入读取"}},
	outputKeys: []outputKey{{"review", "记录的审稿：" + reviewViewHelp + "；失败时为 null"}},
	exitCodes:  standardExitCodes,
	run:        runReviewRecord,
}

var reviewShowCommand = &command{
	name: "review show",
	summary: "汇总单元（Unit）的审稿（Review）、文本检查（Lint）结果、修订任务（Revision Task）与决策，" +
		"并按关卡强度（Gate Level）列出验收（Acceptance）的阻塞项；只读",
	args:  []argSpec{{"unit", unitRefHelp, true}},
	flags: []flagSpec{dirFlag},
	outputKeys: []outputKey{
		{"review", "审稿：" + reviewViewHelp + "；没有审稿或审稿不通过校验时为 null"},
		{"lint", "文本检查结果：ran（是否已运行）、errors 与 warnings（未被豁免的发现项数）"},
		{"revisions", "修订任务数组：未处理的审稿发现项（source 为 review）与作者反馈（source 为 feedback），每项含 source、id、status、paragraph 与 message"},
		{"decisions", "关联该单元、尚未决定的决策数组，每项含 id、question 与 blocking"},
		{"gate", "验收关卡：level、ready（除作者确认外没有阻塞项）、confirmationRequired（strict 下为 true）与 blockers（每项含 code、path、ref 与 message）"},
		{"acceptance", "已有的验收记录：gateLevel、draftSha256、confirmedByAuthor、confirmation、channel（确认渠道（Confirmation Channel），未经作者确认为 null）、acceptedAt 与 stale；没有时为 null"},
		{"pendingAuthorReviews", "单元所在作品的" + pendingAuthorReviewsHelp},
	},
	exitCodes: standardExitCodes,
	run:       runReviewShow,
}

var reviewResolveCommand = &command{
	name:    "review resolve",
	summary: "把审稿（Review）发现项（Finding）标为已解决（resolved）、豁免（waived，须写明理由）或重新打开（open）",
	args:    []argSpec{{"unit", unitRefHelp, true}, {"finding", "发现项 id", true}},
	flags: []flagSpec{dirFlag,
		{"status", flagString, "新状态：" + strings.Join(review.Statuses, "、") + "；省略时为 resolved"},
		{"note", flagString, "处理说明；waived 时必填"}},
	outputKeys: []outputKey{{"finding", "修改后的发现项：unit、id、rubric、paragraph、quote、message、suggestion、status 与 note；失败时为 null"}},
	exitCodes:  standardExitCodes,
	run:        runReviewResolve,
}

var acceptCommand = &command{
	name: "accept",
	summary: "验收（Acceptance）单元（Unit）：按工作区的关卡强度（Gate Level）检查文本检查、审稿（Review）、修订任务（Revision Task）、" +
		"阻塞决策与作者确认，没有阻塞项时写入 acceptance.yaml；之后用 wa-cli advance 推进",
	args: []argSpec{{"unit", unitRefHelp, true}},
	flags: []flagSpec{dirFlag,
		{"confirmed-by-author", flagBool, "作者已确认验收；必须同时给出 --confirmation"},
		{"confirmation", flagString, "作者确认的原话摘要（确认记录（Confirmation Record））；必须同时给出 --confirmed-by-author"}},
	outputKeys: []outputKey{{"acceptance", "写入的验收记录：unit、path、gateLevel、draftSha256、confirmedByAuthor、confirmation、channel（确认渠道（Confirmation Channel），未经作者确认为 null）与 acceptedAt；失败时为 null"}},
	exitCodes:  standardExitCodes,
	run:        runAccept,
}

type verdictView struct {
	Item       string `json:"item"`
	Title      string `json:"title"`
	Verdict    string `json:"verdict"`
	Evidence   string `json:"evidence"`
	Paragraphs []int  `json:"paragraphs"`
	Blocking   bool   `json:"blocking"`
}

type findingItemView struct {
	Unit       string  `json:"unit"`
	ID         string  `json:"id"`
	Rubric     *string `json:"rubric"`
	Paragraph  int     `json:"paragraph"`
	Quote      *string `json:"quote"`
	Message    string  `json:"message"`
	Suggestion *string `json:"suggestion"`
	Status     string  `json:"status"`
	Note       *string `json:"note"`
}

type reviewView struct {
	Unit        string            `json:"unit"`
	Path        string            `json:"path"`
	Stale       bool              `json:"stale"`
	DraftSHA256 string            `json:"draftSha256"`
	Rubric      []verdictView     `json:"rubric"`
	Findings    []findingItemView `json:"findings"`
}

func findingOf(ref string, f review.Finding) findingItemView {
	return findingItemView{Unit: ref, ID: f.ID, Rubric: f.Rubric, Paragraph: f.Paragraph, Quote: f.Quote, Message: f.Message,
		Suggestion: f.Suggestion, Status: f.Status, Note: f.Note}
}

func viewReview(ref, dir string, r *review.Review, current string, rubric []catalog.RubricItem) *reviewView {
	v := &reviewView{Unit: ref, Path: review.Path(dir), Stale: r.DraftSHA256 != current, DraftSHA256: r.DraftSHA256,
		Rubric: []verdictView{}, Findings: []findingItemView{}}
	for _, it := range r.Rubric {
		vv := verdictView{Item: it.Item, Verdict: it.Verdict, Evidence: it.Evidence, Paragraphs: it.Paragraphs}
		if i := slices.IndexFunc(rubric, func(ri catalog.RubricItem) bool { return ri.ID == it.Item }); i >= 0 {
			vv.Title, vv.Blocking = rubric[i].Title, rubric[i].Blocking
		}
		v.Rubric = append(v.Rubric, vv)
	}
	for _, f := range r.Findings {
		v.Findings = append(v.Findings, findingOf(ref, f))
	}
	return v
}

// rubricOf 返回单元的评分标准：合并结果中的评分项，再加上单元选用的写作法（Method Template）附带的评分项（与已有 id
// 相同的不重复加入）。
func rubricOf(r *resolve.Resolved, m *catalog.Method) []catalog.RubricItem {
	out := []catalog.RubricItem{}
	for _, it := range r.Rubric {
		out = append(out, it.RubricItem)
	}
	if m != nil {
		for _, it := range m.Rubric {
			if !slices.ContainsFunc(out, func(x catalog.RubricItem) bool { return x.ID == it.ID }) {
				out = append(out, it)
			}
		}
	}
	return out
}

type reviewRecordResult struct {
	Result
	Review *reviewView `json:"review"`
}

func runReviewRecord(inv *invocation) int {
	dir := projectDir(inv)
	res := &reviewRecordResult{}
	problems := workspace.Require(dir)
	input := false
	if problems == nil {
		var w *work.Work
		var u *work.Unit
		if w, u, problems = work.Resolve(dir, inv.args[0]); u != nil {
			problems = recordReview(inv, dir, w, u, res, &input)
		}
	}
	res.Diagnostics = diagnostics(problems, dir)
	if input {
		// 审稿输入中的问题：输入尚未写入，整体校验检不出，给出用法与段落编号。
		for i, d := range res.Diagnostics {
			if d.Path == "" {
				res.Diagnostics[i].Next = []string{"wa-cli help review record --json", shellCommand("wa-cli", "provenance", inv.args[0], "--dir", dir, "--json"),
					shellCommand("wa-cli", "config", "resolved", "--dir", dir, "--json"), shellCommand("wa-cli", "brief", "show", inv.args[0], "--dir", dir, "--json")}
			}
		}
	}
	helpNext(res.Diagnostics, "review record", []string{codeInputReadFailed})
	return finish(inv, res, func(w io.Writer) error {
		if res.Review == nil {
			return nil
		}
		_, err := fmt.Fprintf(w, "已记录单元（Unit）%s 的审稿（Review）%s：%d 个评分项结论，%d 个发现项（Finding）\n",
			res.Review.Unit, res.Review.Path, len(res.Review.Rubric), len(res.Review.Findings))
		return err
	})
}

func recordReview(inv *invocation, dir string, w *work.Work, u *work.Unit, res *reviewRecordResult, input *bool) []workspace.Problem {
	data, _, ps := readInput(inv, dir)
	if len(ps) > 0 {
		return ps
	}
	content, ok, ps := draft.Read(dir, w, u)
	if len(ps) > 0 {
		return ps
	}
	unitDir := w.UnitDir(u)
	if !ok {
		return []workspace.Problem{{Code: draft.CodeNotFound, Severity: workspace.SeverityError, Path: unitDir + "/" + draft.FileName,
			Message: "单元 " + u.Ref + " 还没有正文，无法审稿；请先用 wa-cli draft write 写入"}}
	}
	r, ps := series.Resolve(dir, w)
	if r == nil {
		return ps
	}
	var paragraphs []string
	for _, p := range draft.Split(content) {
		paragraphs = append(paragraphs, p.Text)
	}
	sum := review.SHA256(content)
	rubric := rubricOf(r, unitMethod(dir, w, u))
	rv, changed, ps := review.Record(dir, unitDir, data, paragraphs, sum, rubric)
	if rv == nil {
		*input = true
		return ps
	}
	res.Review, res.ChangedPaths = viewReview(u.Ref, unitDir, rv, sum, rubric), changed
	return nil
}

type revisionView struct {
	Source    string `json:"source"`
	ID        string `json:"id"`
	Status    string `json:"status"`
	Paragraph *int   `json:"paragraph"`
	Message   string `json:"message"`
}

type decisionItemView struct {
	ID       string `json:"id"`
	Question string `json:"question"`
	Blocking bool   `json:"blocking"`
}

type gateBlockerView struct {
	Code    string  `json:"code"`
	Path    string  `json:"path"`
	Ref     *string `json:"ref"`
	Message string  `json:"message"`
}

type gateView struct {
	Level                string            `json:"level"`
	Ready                bool              `json:"ready"`
	ConfirmationRequired bool              `json:"confirmationRequired"`
	Blockers             []gateBlockerView `json:"blockers"`
}

type lintSummary struct {
	Ran      bool `json:"ran"`
	Errors   int  `json:"errors"`
	Warnings int  `json:"warnings"`
}

type acceptanceView struct {
	Unit              string  `json:"unit,omitempty"`
	Path              string  `json:"path,omitempty"`
	GateLevel         string  `json:"gateLevel"`
	DraftSHA256       string  `json:"draftSha256"`
	ConfirmedByAuthor bool    `json:"confirmedByAuthor"`
	Confirmation      *string `json:"confirmation"`
	Channel           *string `json:"channel"`
	AcceptedAt        string  `json:"acceptedAt"`
	Stale             *bool   `json:"stale,omitempty"`
}

type reviewShowResult struct {
	Result
	Review     *reviewView        `json:"review"`
	Lint       lintSummary        `json:"lint"`
	Revisions  []revisionView     `json:"revisions"`
	Decisions  []decisionItemView `json:"decisions"`
	Gate       gateView           `json:"gate"`
	Acceptance *acceptanceView    `json:"acceptance"`
	// PendingAuthorReviews 是单元所在作品待作者复核（Pending Author Review）的确认（strict 关卡下）。
	PendingAuthorReviews []pendingReviewView `json:"pendingAuthorReviews"`
}

// gateState 是一次命令中为验收关卡收集的全部输入与展示用的数据。
type gateState struct {
	in        review.GateInput
	review    *review.Review
	rubric    []catalog.RubricItem
	lint      lintSummary
	feedback  []feedbackView
	decisions []decision.Decision
	problems  []workspace.Problem // 无法收集关卡输入的问题（如没有正文、合并失败）以及不影响关卡的 warning
	changed   []string            // 收集关卡时产生的副作用路径（如连续性豁免决策的决策队列）
}

// collectGate 为单元 u 收集验收关卡的输入：文本检查、审稿、未处理的作者反馈、关联的决策与评分标准。
func collectGate(dir string, w *work.Work, u *work.Unit) *gateState {
	unitDir := w.UnitDir(u)
	g := &gateState{in: review.GateInput{Dir: unitDir, FeedbackRel: unitDir + "/" + feedback.FileName, DecisionRel: decision.Path, Level: workspace.DefaultGateLevel}}
	if cfg, _ := workspace.Load(dir); cfg != nil && cfg.GateLevel != "" {
		g.in.Level = cfg.GateLevel
	}
	out, r, content, ps := runUnitLint(dir, w, u)
	for _, p := range ps {
		if p.Severity == workspace.SeverityError {
			g.in.LintFailed = append(g.in.LintFailed, p)
		} else {
			g.problems = append(g.problems, p)
		}
	}
	if out != nil {
		g.lint.Ran = true
		for _, f := range out.Findings {
			if f.Severity == workspace.SeverityError {
				g.lint.Errors++
			} else {
				g.lint.Warnings++
			}
		}
		g.in.LintErrors = g.lint.Errors
	}
	if content != nil {
		g.in.DraftSHA = review.SHA256(content)
	}
	// 没有正文或合并失败时 r 为 nil，评分标准为空；其原因已记入 LintFailed，同样阻塞验收。
	if r != nil {
		g.rubric = rubricOf(r, unitMethod(dir, w, u))
		g.problems = append(g.problems, danglingMethod(dir, w, u)...)
	}
	g.in.Rubric = g.rubric
	g.review, g.in.ReviewBad = review.Load(dir, unitDir)
	g.in.Review = g.review
	g.feedback = pendingFeedback(dir, w, u)
	for _, f := range g.feedback {
		g.in.Feedback = append(g.in.Feedback, review.Pending{ID: f.ID, Text: f.Text})
	}
	all, _, dps := decision.Load(dir)
	g.in.QueueBad = dps
	for _, d := range all {
		if d.Status == decision.StatusOpen && slices.Contains(d.Units, u.Ref) {
			g.decisions = append(g.decisions, d)
		}
	}
	for _, d := range decision.BlockingIn(all, u.Ref) {
		g.in.Decisions = append(g.in.Decisions, review.Pending{ID: d.ID, Text: d.Question})
	}
	// 写后收口（Close-out）阻塞项在所有关卡强度下生效：契约要求收口的场景未收口、收口过期、提议未处理或
	// 契约对照失败时阻塞验收（closeout 主规格）；review show 的 gate.blockers 与 accept 共用这份输入。
	g.in.Closeout = closeout.AcceptanceBlockers(dir, w, u)
	// 卷末检查点（Volume Checkpoint）阻塞项按关卡强度定级：strict 为阻塞项，fast 与 standard 为提示
	// （volume 主规格的 volume-accept-gate）。
	g.in.Volume = volume.AcceptanceBlockers(dir, w, u, g.in.Level)
	// 连续性审查（Continuity Review）：只读计算 error 级阻塞项（review show 同用此路径，不产生副作用）；
	// 豁免决策只在 accept 的阻塞路径上生成（见 accept 函数），只读命令不得写入决策队列。
	var cps []workspace.Problem
	if g.in.Continuity, cps = continuity.Blockers(dir, w, u); len(cps) > 0 {
		g.problems = append(g.problems, cps...)
	}
	return g
}

// gateNext 返回验收阻塞项的下一步命令：解除它的命令，以及能复查它的 wa-cli review show。
func gateNext(dir, ref string, b review.Blocker) []string {
	show := shellCommand("wa-cli", "review", "show", ref, "--dir", dir, "--json")
	lintCmd := shellCommand("wa-cli", "lint", ref, "--dir", dir, "--json")
	if strings.HasPrefix(b.Code, "continuity.") {
		return []string{shellCommand("wa-cli", "continuity", ref, "--dir", dir, "--json"),
			shellCommand("wa-cli", "decisions", "--dir", dir, "--json"), show}
	}
	switch b.Code {
	case review.CodeLintErrors:
		return []string{lintCmd, "wa-cli help draft write --json", show}
	case decision.CodeBlocking, decision.CodeInvalidQueue:
		return []string{shellCommand("wa-cli", "decisions", "--dir", dir, "--json"), show}
	case review.CodeReviewMissing, review.CodeReviewStale, review.CodeMissingVerdict:
		// 评分标准来自合并后的配置与单元选用的写作法（见 brief show 的 method 与 method show）。
		return []string{"wa-cli help review record --json", shellCommand("wa-cli", "provenance", ref, "--dir", dir, "--json"),
			shellCommand("wa-cli", "brief", "show", ref, "--dir", dir, "--json"), show}
	case review.CodeOpenFinding, review.CodeBlockingRubric:
		return []string{show, "wa-cli help review resolve --json", "wa-cli help review record --json"}
	case review.CodeOpenFeedback:
		return []string{shellCommand("wa-cli", "feedback", "list", ref, "--dir", dir, "--json"), "wa-cli help feedback resolve --json", show}
	case review.CodeUnconfirmed:
		return []string{"wa-cli help accept --json", show}
	case closeout.CodeMissing, closeout.CodeStale, closeout.CodeInvalid,
		closeout.CodeMissingStateChange, closeout.CodeMissingRecovery, closeout.CodeMissingPlant, closeout.CodeMissingEvent:
		return []string{shellCommand("wa-cli", "scene", "close-out", ref, "--dir", dir, "--json"),
			"wa-cli help scene close-out --json", show}
	case volume.CodeUnconfirmed, volume.CodeInvalid:
		next := []string{"wa-cli help volume confirm --json", show}
		if b.Path != "" {
			next = append([]string{shellCommand("wa-cli", "volume", "confirm", volumePartRef(ref, b.Path), "--dir", dir, "--json")}, next...)
		}
		return next
	case closeout.CodePendingProposals:
		work := ref
		if w, _, found := strings.Cut(ref, "/"); found {
			work = w
		}
		return []string{shellCommand("wa-cli", "state", "list", work, "--dir", dir, "--json"),
			shellCommand("wa-cli", "knowledge", "list", work, "--dir", dir, "--json"),
			shellCommand("wa-cli", "scene", "close-out", ref, "--dir", dir, "--json"),
			"wa-cli help scene close-out --json", show}
	}
	if schema.KindFor(b.Path) != nil {
		return []string{shellCommand("wa-cli", "validate", b.Path, "--dir", dir, "--json"), show}
	}
	return []string{lintCmd, show}
}

func runReviewShow(inv *invocation) int {
	dir := projectDir(inv)
	res := &reviewShowResult{Revisions: []revisionView{}, Decisions: []decisionItemView{}, Gate: gateView{Blockers: []gateBlockerView{}},
		PendingAuthorReviews: []pendingReviewView{}}
	problems := workspace.Require(dir)
	if problems == nil {
		var w *work.Work
		var u *work.Unit
		if w, u, problems = work.Resolve(dir, inv.args[0]); u != nil {
			g := collectGate(dir, w, u)
			unitDir := w.UnitDir(u)
			res.Lint = g.lint
			problems = append(problems, g.problems...)
			// review.yaml 不通过校验时如实报告：审稿无法展示，也是验收的阻塞项。
			problems = append(problems, g.in.ReviewBad...)
			if g.review != nil {
				res.Review = viewReview(u.Ref, unitDir, g.review, g.in.DraftSHA, g.rubric)
				for _, f := range g.review.Findings {
					if f.Status == "open" {
						p := f.Paragraph
						res.Revisions = append(res.Revisions, revisionView{Source: "review", ID: f.ID, Status: f.Status, Paragraph: &p, Message: f.Message})
					}
				}
			}
			for _, f := range g.feedback {
				v := revisionView{Source: "feedback", ID: f.ID, Status: f.Status, Message: f.Text}
				if f.Anchor.Paragraph != nil {
					v.Paragraph = f.Anchor.Paragraph
				}
				res.Revisions = append(res.Revisions, v)
			}
			for _, d := range g.decisions {
				res.Decisions = append(res.Decisions, decisionItemView{ID: d.ID, Question: d.Question, Blocking: d.Blocking})
			}
			// 就绪情况不含作者确认：确认由 accept 的参数给出。
			in := g.in
			in.Confirmed = true
			blockers, _ := review.Gate(in)
			res.Gate.Level, res.Gate.Ready, res.Gate.ConfirmationRequired = in.Level, len(blockers) == 0, in.Level == "strict"
			for _, b := range blockers {
				res.Gate.Blockers = append(res.Gate.Blockers, gateBlockerView{Code: b.Code, Path: b.Path, Ref: optional(b.Ref), Message: b.Message})
			}
			a, aps := review.LoadAcceptance(dir, unitDir)
			problems = append(problems, aps...)
			if a != nil {
				stale := a.DraftSHA256 != g.in.DraftSHA
				res.Acceptance = &acceptanceView{GateLevel: a.GateLevel, DraftSHA256: a.DraftSHA256, ConfirmedByAuthor: a.ConfirmedByAuthor,
					Confirmation: a.Confirmation, Channel: channelView(a.ConfirmedByAuthor, a.Channel), AcceptedAt: a.AcceptedAt, Stale: &stale}
			}
			var rps []workspace.Problem
			res.PendingAuthorReviews, rps = pendingReviews(dir, []*work.Work{w})
			problems = append(problems, rps...)
		}
	}
	res.Diagnostics = diagnostics(problems, dir)
	return finish(inv, res, func(w io.Writer) error {
		if res.Gate.Level == "" {
			return nil
		}
		var b strings.Builder
		switch {
		case res.Review == nil:
			fmt.Fprintf(&b, "单元（Unit）%s 还没有可用的审稿（Review）\n", inv.args[0])
		case res.Review.Stale:
			fmt.Fprintf(&b, "审稿（Review）%s：审稿之后正文已被修改\n", res.Review.Path)
		default:
			fmt.Fprintf(&b, "审稿（Review）%s：%d 个评分项结论，%d 个发现项（Finding）\n", res.Review.Path, len(res.Review.Rubric), len(res.Review.Findings))
		}
		if res.Review != nil {
			for _, v := range res.Review.Rubric {
				fmt.Fprintf(&b, "  评分项 %s（%s）：%s，%s\n", v.Item, v.Title, v.Verdict, v.Evidence)
			}
		}
		fmt.Fprintf(&b, "文本检查（Lint）：%d 条 error，%d 条 warning\n", res.Lint.Errors, res.Lint.Warnings)
		fmt.Fprintf(&b, "修订任务（Revision Task）：%d 项\n", len(res.Revisions))
		for _, r := range res.Revisions {
			fmt.Fprintf(&b, "  %s %s：%s\n", r.Source, r.ID, r.Message)
		}
		state := "可以验收"
		if !res.Gate.Ready {
			state = fmt.Sprintf("有 %d 个阻塞项", len(res.Gate.Blockers))
		}
		fmt.Fprintf(&b, "验收（Acceptance，关卡强度 %s）：%s", res.Gate.Level, state)
		if res.Gate.ConfirmationRequired {
			b.WriteString("；需要作者确认")
		}
		b.WriteString("\n")
		for _, bl := range res.Gate.Blockers {
			fmt.Fprintf(&b, "  阻塞项：%s\n", bl.Message)
		}
		b.WriteString(pendingReviewsText(res.PendingAuthorReviews))
		_, err := io.WriteString(w, b.String())
		return err
	})
}

type reviewResolveResult struct {
	Result
	Finding *findingItemView `json:"finding"`
}

func runReviewResolve(inv *invocation) int {
	dir := projectDir(inv)
	res := &reviewResolveResult{}
	problems := workspace.Require(dir)
	if problems == nil {
		var w *work.Work
		var u *work.Unit
		if w, u, problems = work.Resolve(dir, inv.args[0]); u != nil {
			status := inv.values["status"]
			if status == "" {
				status = "resolved"
			}
			var note *string
			if v, ok := inv.values["note"]; ok {
				note = &v
			}
			var f *review.Finding
			var changed []string
			if f, changed, problems = review.Resolve(dir, w.UnitDir(u), inv.args[1], status, note); f != nil {
				v := findingOf(u.Ref, *f)
				res.Finding, res.ChangedPaths = &v, changed
			}
		}
	}
	res.Diagnostics = diagnostics(problems, dir)
	show := shellCommand("wa-cli", "review", "show", inv.args[0], "--dir", dir, "--json")
	for i, d := range res.Diagnostics {
		switch {
		case d.Code == review.CodeFindingNotFound:
			res.Diagnostics[i].Next = []string{show, "wa-cli help review record --json"}
		case d.Path == "" || d.Code == review.CodeAnchoredValue:
			res.Diagnostics[i].Next = []string{"wa-cli help review resolve --json"}
		}
	}
	return finish(inv, res, func(w io.Writer) error {
		if res.Finding == nil {
			return nil
		}
		verb := "已把"
		if len(res.ChangedPaths) == 0 {
			verb = "取值未变："
		}
		_, err := fmt.Fprintf(w, "%s单元（Unit）%s 的发现项（Finding）%s 标为 %s\n", verb, res.Finding.Unit, res.Finding.ID, res.Finding.Status)
		return err
	})
}

type acceptResult struct {
	Result
	Acceptance *acceptanceView `json:"acceptance"`
}

// acceptsHere 报告单元当前阶段的必需产物（Required Artifact）是否含 acceptance。
func acceptsHere(w *work.Work, u *work.Unit) bool {
	for _, s := range w.Genre.Stages {
		if s.ID == u.Stage {
			return slices.Contains(s.RequiredArtifacts, "acceptance")
		}
	}
	return false
}

func runAccept(inv *invocation) int {
	dir := projectDir(inv)
	res := &acceptResult{}
	problems := workspace.Require(dir)
	var gate []workspace.Problem
	if problems == nil {
		var w *work.Work
		var u *work.Unit
		if w, u, problems = work.Resolve(dir, inv.args[0]); u != nil {
			problems, gate = accept(inv, dir, w, u, res)
		}
	}
	res.Diagnostics = diagnostics(problems, dir)
	ref := ""
	if len(inv.args) > 0 {
		ref = inv.args[0]
	}
	// 关卡的每个阻塞项与 warning：给出解除它的命令与能复查它的 wa-cli review show（accept-gate）。
	for _, d := range diagnostics(gate, dir) {
		d.Next = gateNext(dir, ref, review.Blocker{Code: d.Code, Path: d.Path})
		res.Diagnostics = append(res.Diagnostics, d)
	}
	for i, d := range res.Diagnostics {
		switch d.Code {
		case review.CodeWrongStage:
			res.Diagnostics[i].Next = []string{shellCommand("wa-cli", "next", ref, "--dir", dir, "--json")}
		case review.CodeIncompleteConfirmation:
			res.Diagnostics[i].Next = []string{"wa-cli help accept --json"}
		}
	}
	return finish(inv, res, func(w io.Writer) error {
		if res.Acceptance == nil {
			return nil
		}
		who := "未经作者确认"
		if res.Acceptance.ConfirmedByAuthor {
			who = "经作者确认：" + *res.Acceptance.Confirmation
		}
		_, err := fmt.Fprintf(w, "已验收（Acceptance）单元（Unit）%s（关卡强度 %s，%s）：%s；用 wa-cli advance %s 推进\n",
			res.Acceptance.Unit, res.Acceptance.GateLevel, who, res.Acceptance.Path, res.Acceptance.Unit)
		return err
	})
}

// accept 按命令行参数检查并写入验收（确认渠道为 cli）。problems 为关卡之外的问题，gate 为关卡的阻塞项（error）与
// warning，由调用方给出各自的下一步。
func accept(inv *invocation, dir string, w *work.Work, u *work.Unit, res *acceptResult) (problems, gate []workspace.Problem) {
	if !acceptsHere(w, u) {
		return []workspace.Problem{wrongStage(w, u)}, nil
	}
	_, confirmed := inv.values["confirmed-by-author"]
	confirmation, given := inv.values["confirmation"]
	if confirmed != given || given && strings.TrimSpace(confirmation) == "" {
		return []workspace.Problem{{Code: review.CodeIncompleteConfirmation, Severity: workspace.SeverityError, Field: "confirmation",
			Message: "--confirmed-by-author 与 --confirmation <作者原话摘要> 必须同时给出，且原话摘要不能为空"}}, nil
	}
	return acceptVia(dir, w, u, confirmed, confirmation, workspace.ChannelCLI, res)
}

// wrongStage 是单元当前阶段不需要验收时的问题。
func wrongStage(w *work.Work, u *work.Unit) workspace.Problem {
	return workspace.Problem{Code: review.CodeWrongStage, Severity: workspace.SeverityError, Path: w.Path,
		Message: fmt.Sprintf("单元 %s 的当前阶段 %s 不需要验收（Acceptance）；推进到必需产物含 acceptance 的阶段后再验收", u.Ref, u.Stage)}
}

// acceptVia 按关卡检查并写入验收，确认记录的渠道为 channel（命令行为 cli，阅读页为 preview）；调用方已检查阶段与
// 确认参数。返回值同 accept。
func acceptVia(dir string, w *work.Work, u *work.Unit, confirmed bool, confirmation, channel string, res *acceptResult) (problems, gate []workspace.Problem) {
	unitDir := w.UnitDir(u)
	g := collectGate(dir, w, u)
	g.in.Confirmed = confirmed
	blockers, warnings := review.Gate(g.in)
	// 阻塞项里有连续性发现时，为未豁免的（场景， 错误码）幂等生成豁免决策（阻塞路径的副作用，如实计入
	// changedPaths；只读的 review show 不走这里）。
	if slices.ContainsFunc(blockers, func(b review.Blocker) bool { return strings.HasPrefix(b.Code, "continuity.") }) {
		changed, cps := continuity.EnsureWaivers(dir, w, u)
		if len(cps) > 0 {
			problems = append(problems, cps...)
		}
		g.changed = append(g.changed, changed...)
	}
	problems = g.problems
	for _, b := range blockers {
		gate = append(gate, workspace.Problem{Code: b.Code, Severity: workspace.SeverityError, Path: b.Path, Message: b.Message})
	}
	if len(blockers) > 0 {
		// 关卡收集阶段的副作用（如连续性豁免决策）如实计入 changedPaths，即使验收被拒。
		res.ChangedPaths = append(res.ChangedPaths, g.changed...)
		return problems, gate
	}
	for _, b := range warnings {
		gate = append(gate, workspace.Problem{Code: b.Code, Severity: workspace.SeverityWarning, Path: b.Path, Message: b.Message})
	}
	a, changed, ps := review.AcceptVia(dir, unitDir, g.in.Level, g.in.DraftSHA, confirmed, confirmation, channel, now())
	if a == nil {
		return append(problems, ps...), gate
	}
	res.Acceptance = &acceptanceView{Unit: u.Ref, Path: review.AcceptancePath(unitDir), GateLevel: a.GateLevel, DraftSHA256: a.DraftSHA256,
		ConfirmedByAuthor: a.ConfirmedByAuthor, Confirmation: a.Confirmation, Channel: channelView(a.ConfirmedByAuthor, a.Channel), AcceptedAt: a.AcceptedAt}
	res.ChangedPaths = changed
	return problems, gate
}

// refUnitOf 把验收单元引用与其中的部名组合为 wa-cli volume confirm 可用的部引用（部是作品的直接或更深层
// 子单元，按单元引用规则 <work>/<unit-id> 拼接）。
func refUnitOf(ref, unitID string) string {
	if w, _, found := strings.Cut(ref, "/"); found {
		return w + "/" + unitID
	}
	return unitID
}

// volumePartRef 从检查点产物路径推导部单元引用（.writing-anything/works/<work>/units/<part>/checkpoint.yaml →
// <work>/<part>；固定结构前缀逐段解析，workspace-artifacts/structural-path-parsing）。
func volumePartRef(ref, path string) string {
	parts := strings.Split(strings.TrimSuffix(path, "/checkpoint.yaml"), "/")
	if len(parts) >= 2 {
		if w, _, found := strings.Cut(ref, "/"); found {
			return w + "/" + parts[len(parts)-1]
		}
	}
	return ref
}
