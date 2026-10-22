package cli

import (
	"encoding/json"
	"fmt"
	"io"
	"net/http"
	"slices"
	"strings"

	"github.com/kw12121212/writing-anything/internal/authorreview"
	"github.com/kw12121212/writing-anything/internal/decision"
	"github.com/kw12121212/writing-anything/internal/draft"
	"github.com/kw12121212/writing-anything/internal/knowledge"
	"github.com/kw12121212/writing-anything/internal/plan"
	"github.com/kw12121212/writing-anything/internal/preview"
	"github.com/kw12121212/writing-anything/internal/review"
	"github.com/kw12121212/writing-anything/internal/state"
	"github.com/kw12121212/writing-anything/internal/volume"
	"github.com/kw12121212/writing-anything/internal/work"
	"github.com/kw12121212/writing-anything/internal/workspace"
)

// 本文件是本地阅读页（Reading View）的作者确认与复核：待作者确认队列、POST /confirm 与 POST /author-review。
// 写入复用各命令的同一套函数，确认渠道（Confirmation Channel）为 preview；令牌与 Host 校验在 ServeHTTP 中完成。

// 待作者确认队列中的确认类型（POST /confirm 的 kind）。
const (
	confirmAcceptance = "acceptance"
	confirmDecision   = "decision"
	confirmPlan       = "plan"
	confirmState      = "state"
	confirmKnowledge  = "knowledge"
	confirmVolume     = "volume"
)

// confirmedView 是阅读页记录的一次作者确认。id 只在待确认事项有条目 id 时出现（decision、plan、state、knowledge）；
// acceptance 与 volume 没有条目 id，省略该键，不输出空串。
type confirmedView struct {
	Kind    string `json:"kind"`
	Unit    string `json:"unit"`
	ID      string `json:"id,omitempty"`
	Action  string `json:"action"`
	Channel string `json:"channel"`
}

// authorReviewView 是阅读页写入的一次批量复核。
type authorReviewView struct {
	Reviewed []authorreview.Item `json:"reviewed"`
	Channel  string              `json:"channel"`
}

type previewConfirmResult struct {
	Result
	Confirmation *confirmedView `json:"confirmation"`
}

type previewReviewResult struct {
	Result
	AuthorReview *authorReviewView `json:"authorReview"`
}

// confirmRequest 是 POST /confirm 的请求体。
type confirmRequest struct {
	Kind         string `json:"kind"`
	Unit         string `json:"unit"`
	ID           string `json:"id"`
	Action       string `json:"action"`
	Option       string `json:"option"`
	Answer       string `json:"answer"`
	Confirmation string `json:"confirmation"`
}

// reviewRequest 是 POST /author-review 的请求体。
type reviewRequest struct {
	Keys []string `json:"keys"`
}

// inWork 报告单元引用 ref 是否属于作品 w。
func inWork(w *work.Work, ref string) bool {
	return ref == w.ID || strings.HasPrefix(ref, w.ID+"/")
}

// confirmInputs 是待作者确认队列中按工作区只需加载一次的输入：关卡强度与决策队列（workspace-artifacts/load-once-per-command）。
// decisionsOK 为 false 时决策队列无法加载，跳过决策（其问题由决策命令报告）。
type confirmInputs struct {
	level       string
	decisions   []decision.Decision
	decisionsOK bool
}

func loadConfirmInputs(dir string) confirmInputs {
	in := confirmInputs{level: workspace.DefaultGateLevel}
	if cfg, _ := workspace.Load(dir); cfg != nil && cfg.GateLevel != "" {
		in.level = cfg.GateLevel
	}
	var ps []workspace.Problem
	in.decisions, _, ps = decision.Load(dir)
	in.decisionsOK = len(ps) == 0
	return in
}

// confirmQueue 返回作品 w 待作者确认的事项：需要验收且没有有效验收的单元、关联本作品（或不关联任何单元）的 open
// 决策、待确认的规划步骤、待确认的状态变更提议与知情事件，以及未确认的卷末检查点。无法加载的产物跳过（其问题由
// 各自的命令报告）。阅读页（GET / 与 GET /page.json）与 wa-cli next 的 pendingConfirmations 都只通过它得到这些事项。
func confirmQueue(dir string, w *work.Work) []preview.Confirm {
	return confirmQueueWith(dir, w, loadConfirmInputs(dir))
}

// confirmQueueWith 同 confirmQueue，使用已经加载的工作区级输入（逐作品计算时只加载一次）。
func confirmQueueWith(dir string, w *work.Work, in confirmInputs) []preview.Confirm {
	out := []preview.Confirm{}
	level := in.level
	for _, u := range w.Units() {
		unitDir := w.UnitDir(u)
		if !acceptsHere(w, u) {
			continue
		}
		a, ps := review.LoadAcceptance(dir, unitDir)
		if a == nil || len(ps) > 0 || len(review.AcceptanceBlockers(dir, unitDir, level)) > 0 {
			out = append(out, preview.Confirm{Kind: confirmAcceptance, Unit: u.Ref, Units: []string{u.Ref}, Label: "验收（Acceptance）" + u.Ref,
				Detail: "阶段 " + u.Stage + "；确认后按关卡强度 " + level + " 检查阻塞项，有阻塞项时不写入（可先用 wa-cli review show " + u.Ref + " 查看）"})
		}
	}
	if in.decisionsOK {
		for _, d := range in.decisions {
			if d.Status != decision.StatusOpen || len(d.Units) > 0 && !slices.ContainsFunc(d.Units, func(r string) bool { return inWork(w, r) }) {
				continue
			}
			c := preview.Confirm{Kind: confirmDecision, ID: d.ID, Label: "决策（Decision）" + d.ID + "：" + d.Question,
				Detail: deref(d.Rationale), Recommend: deref(d.Recommendation), NeedsAnswer: len(d.Options) == 0,
				Units: slices.DeleteFunc(slices.Clone(d.Units), func(r string) bool { return !inWork(w, r) })}
			if len(c.Units) == 0 {
				c.Units = []string{w.ID}
			}
			if cf := d.Conflict; cf != nil {
				c.Detail = "冲突（Conflict）「" + cf.About + "」：" + cf.A.Source + " 说「" + cf.A.Quote + "」；" +
					cf.B.Source + " 说「" + cf.B.Quote + "」。请作者裁定采信哪一方。" + c.Detail
			}
			for _, o := range d.Options {
				c.Options = append(c.Options, preview.Option{ID: o.ID, Text: o.Text})
			}
			out = append(out, c)
		}
	}
	for _, u := range w.Units() {
		m, _, _ := planMethod(dir, w, u)
		if m == nil {
			continue
		}
		f, ps := plan.Load(dir, w.UnitDir(u))
		if f == nil || len(ps) > 0 {
			continue
		}
		if at := plan.CurrentStep(f, m); at >= 0 && at < len(f.Steps) {
			s := f.Steps[at]
			out = append(out, preview.Confirm{Kind: confirmPlan, Unit: u.Ref, Units: []string{u.Ref}, ID: s.ID, Label: "规划步骤 " + u.Ref + " / " + s.ID + "（" + s.Title + "）", Detail: s.Text})
		}
	}
	if es, ps := state.List(dir, w, false); len(ps) == 0 || es != nil {
		for _, e := range es {
			out = append(out, preview.Confirm{Kind: confirmState, ID: e.ID, CanReject: true, Units: sceneUnits(w, e.Scene),
				Label: "状态变更提议 " + e.ID + "（" + e.Scene + "，设定 " + e.Entry + "）", Detail: e.State})
		}
	}
	if es, ps := knowledge.List(dir, w, false); len(ps) == 0 || es != nil {
		for _, e := range es {
			out = append(out, preview.Confirm{Kind: confirmKnowledge, ID: e.ID, CanReject: true, Units: sceneUnits(w, e.Scene),
				Label: "知情事件 " + e.ID + "（" + e.Scene + "，" + knowerText(e.Character) + " 获知 " + e.Entry + "）"})
		}
	}
	for _, u := range w.Units() {
		if !volume.IsVolume(w, u) {
			continue
		}
		if v, ps := volume.Load(dir, w, u); v != nil && len(ps) == 0 && !v.ConfirmedByAuthor {
			out = append(out, preview.Confirm{Kind: confirmVolume, Unit: u.Ref, Units: []string{u.Ref}, Label: "卷末检查点（Volume Checkpoint）" + u.Ref,
				Detail: strings.TrimSuffix(volumeText(v), "\n")})
		}
	}
	return out
}

// sceneUnits 返回提议绑定场景的单元引用；场景不属于作品 w 时为作品根单元。
func sceneUnits(w *work.Work, scene string) []string {
	if scene != "" && inWork(w, scene) {
		return []string{scene}
	}
	return []string{w.ID}
}

// reviewQueue 返回作品 w 的待作者复核区：strict 关卡下的待复核确认；其他关卡强度为空。
func reviewQueue(dir string, w *work.Work) (strict bool, out []preview.Review) {
	if !authorreview.Strict(dir) {
		return false, nil
	}
	items, _ := authorreview.Pending(dir, w)
	for _, it := range items {
		out = append(out, preview.Review{Key: it.Key, Label: authorreview.Describe(it)})
	}
	return true, out
}

// decodeBody 把请求体解码为 v：不超过 previewMaxBody 字节的单个 JSON 对象，不允许未定义的字段。
func decodeBody(rw http.ResponseWriter, r *http.Request, v any) error {
	dec := json.NewDecoder(http.MaxBytesReader(rw, r.Body, previewMaxBody))
	dec.DisallowUnknownFields()
	err := dec.Decode(v)
	if err == nil && dec.Decode(&struct{}{}) != io.EOF {
		err = fmt.Errorf("JSON 对象之后还有其他内容")
	}
	return err
}

func badRequest(msg string) []workspace.Problem {
	return []workspace.Problem{{Code: codeInvalidValue, Severity: workspace.SeverityError, Message: msg}}
}

// statusFor 返回写入失败时的 HTTP 状态：读写文件失败为 500，服务正在关闭为 503，其他为 400。
func statusFor(problems []workspace.Problem) int {
	switch {
	case slices.ContainsFunc(problems, func(p workspace.Problem) bool {
		return p.Code == workspace.CodeWriteFailed || p.Code == workspace.CodeReadFailed
	}):
		return http.StatusInternalServerError
	case slices.ContainsFunc(problems, func(p workspace.Problem) bool { return p.Code == preview.CodeClosing }):
		return http.StatusServiceUnavailable
	}
	return http.StatusBadRequest
}

// pageNext 为阅读页请求本身的问题（请求体、目标、服务正在关闭、未知的复核键）给出 wa-cli help preview --json；
// 产物与领域命令的问题保留各自的下一步命令（能复查该问题）。
func pageNext(ds []Diagnostic) {
	for i, d := range ds {
		if slices.Contains([]string{codeInvalidValue, preview.CodeClosing, authorreview.CodeUnknownKey}, d.Code) {
			ds[i].Next = []string{"wa-cli help preview --json"}
		}
	}
}

// respond 写出 JSON 响应。
func respond(rw http.ResponseWriter, status int, v enveloped) {
	rw.Header().Set("Content-Type", "application/json; charset=utf-8")
	rw.WriteHeader(status)
	writeJSON(rw, v)
}

// closingProblems 在已保存 --max 条反馈、服务正在关闭时返回 preview.closing。调用方持有 s.mu。
func (s *previewServer) closingProblems() []workspace.Problem {
	if s.limit > 0 && s.saved >= s.limit {
		return []workspace.Problem{{Code: preview.CodeClosing, Severity: workspace.SeverityError,
			Message: fmt.Sprintf("阅读页已保存 --max 指定的 %d 条反馈，正在关闭，不再接受新的操作", s.limit)}}
	}
	return nil
}

// confirm 处理 POST /confirm：目标必须在按当前工作区重新计算的待作者确认队列中，写入渠道为 preview 的确认记录。
func (s *previewServer) confirm(rw http.ResponseWriter, r *http.Request) {
	res := &previewConfirmResult{}
	var req confirmRequest
	var problems []workspace.Problem
	if err := decodeBody(rw, r, &req); err != nil {
		problems = badRequest(fmt.Sprintf("请求体应为不超过 %d 字节的 JSON 对象，含 kind、unit、id、action、option、answer 与 confirmation：%v", previewMaxBody, err))
	}
	s.mu.Lock()
	defer s.mu.Unlock()
	if problems == nil {
		problems = s.closingProblems()
	}
	var w *work.Work
	if problems == nil {
		var ps []workspace.Problem
		if w, _, ps = work.Resolve(s.dir, s.ref); w == nil {
			problems = ps
		}
	}
	if problems == nil {
		problems = s.applyConfirm(w, req, res)
	}
	ds := diagnostics(problems, s.dir)
	pageNext(ds)
	res.Diagnostics = append(ds, res.Diagnostics...)
	status := http.StatusOK
	if res.Confirmation == nil {
		status = statusFor(problems)
	}
	respond(rw, status, res)
	if res.Confirmation == nil {
		return
	}
	c := res.Confirmation
	line := &previewResult{Result: Result{ChangedPaths: res.ChangedPaths}, Preview: s.view, Confirmation: c}
	if err := s.emit(line, fmt.Sprintf("作者在阅读页确认（渠道 preview）：%s %s%s\n", c.Kind, c.Unit, c.ID)); err != nil {
		s.err = err
		s.closing.Do(func() { close(s.done) })
	}
}

// applyConfirm 校验 POST /confirm 的目标并写入确认；成功时设置 res.Confirmation 与 res.ChangedPaths。验收关卡的阻塞项
// 以诊断写入 res.Diagnostics（带解除它的下一步命令）并作为问题返回。
func (s *previewServer) applyConfirm(w *work.Work, req confirmRequest, res *previewConfirmResult) []workspace.Problem {
	action := req.Action
	if action == "" {
		action = "confirm"
	}
	reject := slices.Contains([]string{confirmState, confirmKnowledge}, req.Kind)
	switch {
	case action != "confirm" && !(action == "reject" && reject):
		return badRequest("操作 " + action + " 不合法：状态变更提议与知情事件可以 confirm 或 reject，其他事项只能 confirm")
	case !slices.ContainsFunc(confirmQueue(s.dir, w), func(c preview.Confirm) bool {
		return c.Kind == req.Kind && c.Unit == req.Unit && c.ID == req.ID
	}):
		return badRequest(fmt.Sprintf("%s %s%s 不在作品 %s 的待作者确认队列中：可能已经确认、已被修改或不属于这个阅读页；请刷新阅读页后重试", req.Kind, req.Unit, req.ID, w.ID))
	case strings.TrimSpace(req.Confirmation) == "":
		return []workspace.Problem{{Code: codeInvalidValue, Severity: workspace.SeverityError, Field: "confirmation",
			Message: "作者确认必须填写非空白的原话摘要（confirmation）"}}
	}
	ch := workspace.ChannelPreview
	var changed []string
	var problems []workspace.Problem
	switch req.Kind {
	case confirmAcceptance:
		// 单元与作品取自同一次解析：卷末检查点等按单元树判定祖先的检查依赖同一棵树。
		uw, u, ps := work.Resolve(s.dir, req.Unit)
		if u == nil {
			return ps
		}
		var gate []workspace.Problem
		ar := &acceptResult{}
		problems, gate = acceptVia(s.dir, uw, u, true, req.Confirmation, ch, ar)
		for _, d := range diagnostics(gate, s.dir) {
			d.Next = gateNext(s.dir, u.Ref, review.Blocker{Code: d.Code, Path: d.Path})
			res.Diagnostics = append(res.Diagnostics, d)
		}
		changed = ar.ChangedPaths
		if ar.Acceptance == nil {
			// 关卡阻塞项已作为诊断写入 res.Diagnostics；收集关卡的副作用（如连续性豁免决策）如实计入 changedPaths。
			res.ChangedPaths = changed
			return problems
		}
	case confirmDecision:
		var d *decision.Decision
		if d, problems = decision.DecideVia(s.dir, req.ID, req.Option, req.Answer, req.Confirmation, ch, now()); d != nil {
			changed = []string{decision.Path}
		}
	case confirmPlan:
		_, u, ps := work.Resolve(s.dir, req.Unit)
		if u == nil {
			return ps
		}
		m, _, ps := planMethod(s.dir, w, u)
		if m == nil {
			return ps
		}
		ensure := func() ([]string, []workspace.Problem) { return draft.EnsureUnitDir(s.dir, w, u) }
		var f *plan.File
		if f, changed, problems = plan.ConfirmVia(s.dir, w, w.UnitDir(u), m, req.ID, req.Confirmation, ch, ensure, now()); f == nil {
			changed = nil
		}
	case confirmState:
		decide := state.ConfirmVia
		if action == "reject" {
			decide = state.RejectVia
		}
		var e *state.Event
		if e, changed, problems = decide(s.dir, w, req.ID, req.Confirmation, ch, now()); e == nil {
			changed = nil
		}
	case confirmKnowledge:
		decide := knowledge.ConfirmVia
		if action == "reject" {
			decide = knowledge.RejectVia
		}
		var e *knowledge.Event
		if e, changed, problems = decide(s.dir, w, req.ID, req.Confirmation, ch, now()); e == nil {
			changed = nil
		}
	case confirmVolume:
		_, u, ps := work.Resolve(s.dir, req.Unit)
		if u == nil {
			return ps
		}
		var v *volume.View
		if v, changed, problems = volume.ConfirmVia(s.dir, w, u, req.Confirmation, ch, now()); v == nil {
			changed = nil
		}
	}
	if hasErrorProblem(problems) || changed == nil && req.Kind != confirmAcceptance {
		return problems
	}
	res.Confirmation = &confirmedView{Kind: req.Kind, Unit: req.Unit, ID: req.ID, Action: action, Channel: ch}
	res.ChangedPaths = changed
	return problems
}

// hasErrorProblem 报告 ps 中是否有 error 级的问题。
func hasErrorProblem(ps []workspace.Problem) bool {
	return slices.ContainsFunc(ps, func(p workspace.Problem) bool { return p.Severity == workspace.SeverityError })
}

// authorReview 处理 POST /author-review：strict 关卡下为作者勾选的待复核确认写入复核记录（channel 为 preview）。
func (s *previewServer) authorReview(rw http.ResponseWriter, r *http.Request) {
	res := &previewReviewResult{}
	var req reviewRequest
	var problems []workspace.Problem
	if err := decodeBody(rw, r, &req); err != nil {
		problems = badRequest(fmt.Sprintf("请求体应为不超过 %d 字节的 JSON 对象，含 keys（待复核确认的 key 数组）：%v", previewMaxBody, err))
	}
	s.mu.Lock()
	defer s.mu.Unlock()
	if problems == nil {
		problems = s.closingProblems()
	}
	if problems == nil && !authorreview.Strict(s.dir) {
		problems = badRequest("工作区的关卡强度不是 strict，没有需要作者复核的确认")
	}
	var reviewed []authorreview.Item
	if problems == nil {
		w, _, ps := work.Resolve(s.dir, s.ref)
		problems = ps
		if w != nil {
			reviewed, res.ChangedPaths, problems = authorreview.Review(s.dir, w, req.Keys, now())
		}
	}
	res.Diagnostics = diagnostics(problems, s.dir)
	pageNext(res.Diagnostics)
	status := http.StatusOK
	if reviewed == nil {
		status = statusFor(problems)
	} else {
		res.AuthorReview = &authorReviewView{Reviewed: reviewed, Channel: workspace.ChannelPreview}
	}
	respond(rw, status, res)
	if reviewed == nil {
		return
	}
	line := &previewResult{Result: Result{ChangedPaths: res.ChangedPaths}, Preview: s.view, AuthorReview: res.AuthorReview}
	if err := s.emit(line, fmt.Sprintf("作者在阅读页复核了 %d 项命令行渠道的确认（渠道 preview）\n", len(reviewed))); err != nil {
		s.err = err
		s.closing.Do(func() { close(s.done) })
	}
}
