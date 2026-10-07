package cli

import (
	"fmt"
	"io"
	"slices"
	"strings"

	"github.com/kw12121212/writing-anything/internal/brief"
	"github.com/kw12121212/writing-anything/internal/catalog"
	"github.com/kw12121212/writing-anything/internal/decision"
	"github.com/kw12121212/writing-anything/internal/handoff"
	"github.com/kw12121212/writing-anything/internal/impact"
	"github.com/kw12121212/writing-anything/internal/marketing"
	"github.com/kw12121212/writing-anything/internal/outline"
	"github.com/kw12121212/writing-anything/internal/review"
	"github.com/kw12121212/writing-anything/internal/schema"
	"github.com/kw12121212/writing-anything/internal/work"
	"github.com/kw12121212/writing-anything/internal/workspace"
)

const unitRefHelp = "单元引用（Unit Reference）：<work> 表示作品的根单元，<work>/<unit-id> 表示作品中任意深度的单元"

var newCommand = &command{
	name:    "new",
	summary: "按文体（Genre）创建作品（Work）；作品本身是单元树（Unit Tree）的根单元（Root Unit）",
	args: []argSpec{
		{"genre", "文体 id，如 minimal", true},
		{"slug", "作品 id：小写字母开头，由小写字母、数字与连字符组成", true},
	},
	flags:      []flagSpec{dirFlag},
	outputKeys: []outputKey{{"work", "新建的作品：id、genre、type（根单元类型）与 stage；失败时为 null"}},
	exitCodes:  standardExitCodes,
	run:        runNew,
}

var unitAddCommand = &command{
	name:    "unit add",
	summary: "在父单元的子单元中追加或按锚点插入一个单元（Unit），从文体的第一个阶段开始",
	args: []argSpec{
		{"parent", "父单元的" + unitRefHelp, true},
		{"type", "新单元的单元类型，必须是父单元类型允许的子单元类型", true},
		{"id", "新单元的 id，在作品内唯一", true},
	},
	flags: []flagSpec{
		dirFlag,
		{"before", "string", "插入锚点：新单元插到该兄弟单元之前；省略 --before/--after 时追加到末尾"},
		{"after", "string", "插入锚点：新单元插到该兄弟单元之后；与 --before 互斥"},
	},
	outputKeys: []outputKey{{"unit", "新单元：unit（单元引用）、type、parent（父单元引用）与 stage；失败时为 null"}},
	exitCodes:  standardExitCodes,
	run:        runUnitAdd,
}

var unitMoveCommand = &command{
	name:    "unit move",
	summary: "把单元（Unit）连同子树移动为另一父单元的子单元（含同父内重排序）；单元 id、单元引用与单元目录保持不变",
	args: []argSpec{
		{"unit", "要移动的" + unitRefHelp, true},
		{"parent", "新父单元的" + unitRefHelp + "；必须是同一作品中的单元", true},
	},
	flags: []flagSpec{
		dirFlag,
		{"before", "string", "插入锚点：单元插到新父单元下该兄弟单元之前；省略 --before/--after 时移到末尾"},
		{"after", "string", "插入锚点：单元插到新父单元下该兄弟单元之后；与 --before 互斥"},
	},
	outputKeys: []outputKey{{"unit", "移动后的单元：unit、type、parent（新父单元引用）与 stage（汇总单元为推导阶段）；失败时为 null"}},
	exitCodes:  standardExitCodes,
	run:        runUnitMove,
}

var advanceCommand = &command{
	name:       "advance",
	summary:    "把单元（Unit）推进到文体声明的下一个阶段（Stage）；缺少必需产物（Required Artifact）时拒绝推进",
	args:       []argSpec{{"unit", unitRefHelp, true}},
	flags:      []flagSpec{dirFlag},
	outputKeys: []outputKey{{"unit", "推进后的单元：unit、type、previousStage 与 stage；失败时为 null"}},
	exitCodes:  standardExitCodes,
	run:        runAdvance,
}

var nextCommand = &command{
	name:    "next",
	summary: "列出单元（Unit）的当前阶段（Stage）、阻塞项（Blocker）与下一步命令；汇总单元（Aggregated Unit）的阶段与阻塞项由子单元汇总",
	args:    []argSpec{{"unit", "只列出该单元；省略时列出所有未完成的单元。" + unitRefHelp, false}},
	flags:   []flagSpec{dirFlag},
	outputKeys: []outputKey{{"steps", "下一步数组，每项含 unit、stage、blockers、action、commands、feedback（未处理完的作者反馈）与 progress（汇总单元的进度，其他单元为 null）"},
		{"pendingAuthorReviews", "给出单元时为其所在作品、省略时为全部作品的" + pendingAuthorReviewsHelp}},
	exitCodes: standardExitCodes,
	run:       runNext,
}

type workView struct {
	ID    string `json:"id"`
	Genre string `json:"genre"`
	Type  string `json:"type"`
	Stage string `json:"stage"`
}

type newResult struct {
	Result
	Work *workView `json:"work"`
}

type addedUnit struct {
	Unit   string `json:"unit"`
	Type   string `json:"type"`
	Parent string `json:"parent"`
	Stage  string `json:"stage"`
}

type unitAddResult struct {
	Result
	Unit *addedUnit `json:"unit"`
}

type unitMoveResult struct {
	Result
	Unit *addedUnit `json:"unit"`
}

type advancedUnit struct {
	Unit          string `json:"unit"`
	Type          string `json:"type"`
	PreviousStage string `json:"previousStage"`
	Stage         string `json:"stage"`
}

type advanceResult struct {
	Result
	Unit *advancedUnit `json:"unit"`
}

type blockerView struct {
	Code     string  `json:"code"`
	Artifact string  `json:"artifact"`
	Path     string  `json:"path"`
	Decision *string `json:"decision"` // 决策阻塞项的决策 id；其他阻塞项为 null
	Message  string  `json:"message"`
}

// handoffRef 是 next 条目附带的最近交接记录摘要。
type handoffRef struct {
	ID   string   `json:"id"`
	Todo []string `json:"todo"`
}

type stepView struct {
	Unit     string         `json:"unit"`
	Stage    string         `json:"stage"`
	Blockers []blockerView  `json:"blockers"`
	Action   string         `json:"action"`
	Commands []string       `json:"commands"`
	Feedback []feedbackView `json:"feedback"`
	// Progress 只对汇总单元（Aggregated Unit）非空：子单元树中走生命周期的单元的完成进度。
	Progress *progressView `json:"progress"`
	// Handoff 是工作区最近一条交接记录（Handoff Note）的 id 与待办，供新会话接续；没有记录时为 null。
	Handoff *handoffRef `json:"handoff"`
}

// progressView 是汇总单元（Aggregated Unit）的进度：非汇总子孙单元的总数与其中已完成的数量。
type progressView struct {
	Done  int `json:"done"`
	Total int `json:"total"`
}

type nextResult struct {
	Result
	Steps []stepView `json:"steps"`
	// PendingAuthorReviews 是待作者复核（Pending Author Review）的确认（strict 关卡下），组稿前须由作者在阅读页复核。
	PendingAuthorReviews []pendingReviewView `json:"pendingAuthorReviews"`
}

// unitView 是 status 输出 units 中的一项。
type unitView struct {
	Unit     string        `json:"unit"`
	Work     string        `json:"work"`
	Type     string        `json:"type"`
	Parent   *string       `json:"parent"`
	Stage    string        `json:"stage"`
	Blockers []blockerView `json:"blockers"`
	Commands []string      `json:"commands"`
	Progress *progressView `json:"progress"`
	depth    int
}

func runNew(inv *invocation) int {
	dir := projectDir(inv)
	genre, slug := inv.args[0], inv.args[1]
	res := &newResult{}
	problems := workspace.Require(dir)
	if problems == nil {
		var w *work.Work
		var created []string
		if w, created, problems = work.Create(dir, genre, slug); w != nil {
			res.Work = &workView{ID: w.ID, Genre: w.Genre.ID, Type: w.Root.Type, Stage: w.Root.Stage}
			res.ChangedPaths = created
		}
	}
	res.Diagnostics = diagnostics(problems, dir)
	for i, p := range problems {
		switch p.Code {
		case work.CodeUnknownGenre:
			var next []string
			for _, g := range catalog.BuiltinGenres() {
				next = append(next, shellCommand("wa-cli", "new", g, slug, "--dir", dir, "--json"))
			}
			res.Diagnostics[i].Next = next
		case schema.CodeSchemaViolation:
			res.Diagnostics[i].Next = []string{"wa-cli help new --json"}
		}
	}
	return finish(inv, res, func(w io.Writer) error {
		if res.Work == nil {
			return nil
		}
		_, err := fmt.Fprintf(w, "已创建作品（Work）%s：文体 %s，根单元类型 %s，阶段 %s\n", res.Work.ID, res.Work.Genre, res.Work.Type, res.Work.Stage)
		return err
	})
}

// anchor 从 invocation 读取插入锚点；--before 与 --after 同时给出或取值为空时返回参数问题的说明。
func anchor(inv *invocation) (work.Anchor, string) {
	if inv.flag("before") && inv.flag("after") {
		return work.Anchor{}, "--before 与 --after 互斥，只能给出其中一个"
	}
	for _, name := range []string{"before", "after"} {
		if inv.flag(name) && inv.values[name] == "" {
			return work.Anchor{}, fmt.Sprintf("参数 --%s 的取值为空；锚点必须是同父单元下的兄弟单元 id", name)
		}
	}
	return work.Anchor{Sibling: inv.values["before"], After: inv.flag("after")}, ""
}

func runUnitAdd(inv *invocation) int {
	dir := projectDir(inv)
	parent, typ, id := inv.args[0], inv.args[1], inv.args[2]
	res := &unitAddResult{}
	problems := workspace.Require(dir)
	a, problem := anchor(inv)
	if problem != "" {
		problems = append(problems, workspace.Problem{Code: codeInvalidValue, Severity: workspace.SeverityError,
			Field: "before", Message: problem})
	}
	edit := false
	if problems == nil {
		// 先单独解析父单元：作品本身的问题由 validate 复查，追加单元引起的问题另给下一步。
		if _, _, problems = work.Resolve(dir, parent); problems == nil {
			var w *work.Work
			var u *work.Unit
			if w, u, problems = work.AddUnit(dir, parent, typ, id, a); u != nil {
				res.Unit = &addedUnit{Unit: u.Ref, Type: u.Type, Parent: u.Parent.Ref, Stage: u.Stage}
				res.ChangedPaths = []string{w.Path}
			}
			edit = true
		}
	}
	res.Diagnostics = diagnostics(problems, dir)
	if edit {
		for i := range res.Diagnostics {
			res.Diagnostics[i].Next = []string{"wa-cli help unit add --json", shellCommand("wa-cli", "status", "--dir", dir, "--json")}
		}
	}
	rewriteParamNext(res.Diagnostics, "unit add", work.CodeNotSibling)
	return finish(inv, res, func(w io.Writer) error {
		if res.Unit == nil {
			return nil
		}
		verb := "追加"
		if a.Sibling != "" {
			verb = "插入"
		}
		_, err := fmt.Fprintf(w, "已在 %s 下%s单元（Unit）%s：类型 %s，阶段 %s\n", res.Unit.Parent, verb, res.Unit.Unit, res.Unit.Type, res.Unit.Stage)
		return err
	})
}

func runUnitMove(inv *invocation) int {
	dir := projectDir(inv)
	res := &unitMoveResult{}
	problems := workspace.Require(dir)
	a, problem := anchor(inv)
	if problem != "" {
		problems = append(problems, workspace.Problem{Code: codeInvalidValue, Severity: workspace.SeverityError,
			Field: "before", Message: problem})
	}
	edit := false
	if problems == nil {
		// 先单独解析两个引用：引用不存在的问题由 resolve 报告，移动引起的问题另给下一步。
		if _, _, problems = work.Resolve(dir, inv.args[0]); problems == nil {
			var w *work.Work
			var u *work.Unit
			if w, u, problems = work.MoveUnit(dir, inv.args[0], inv.args[1], a); u != nil {
				res.Unit = &addedUnit{Unit: u.Ref, Type: u.Type, Parent: u.Parent.Ref, Stage: w.EffectiveStage(u)}
				res.ChangedPaths = []string{w.Path}
			}
			edit = true
		}
	}
	res.Diagnostics = diagnostics(problems, dir)
	if edit {
		for i := range res.Diagnostics {
			res.Diagnostics[i].Next = []string{"wa-cli help unit move --json", shellCommand("wa-cli", "status", "--dir", dir, "--json")}
		}
	}
	rewriteParamNext(res.Diagnostics, "unit move", work.CodeNotSibling, work.CodeMoveRoot, work.CodeMoveUnderSelf)
	return finish(inv, res, func(w io.Writer) error {
		if res.Unit == nil {
			return nil
		}
		_, err := fmt.Fprintf(w, "已把单元（Unit）%s 移动到 %s 之下：类型 %s，生效阶段 %s\n", res.Unit.Unit, res.Unit.Parent, res.Unit.Type, res.Unit.Stage)
		return err
	})
}

// rewriteParamNext 把参数引起的问题的下一步改为查看命令用法；这些问题的取值不由既有产物决定，
// 整体校验不能复查（cli-contract/next-rechecks-problem）。
func rewriteParamNext(ds []Diagnostic, command string, codes ...string) {
	for i := range ds {
		if slices.Contains(codes, ds[i].Code) || ds[i].Code == codeInvalidValue {
			ds[i].Next = []string{"wa-cli help " + command + " --json"}
		}
	}
}

func runAdvance(inv *invocation) int {
	dir := projectDir(inv)
	res := &advanceResult{}
	problems := workspace.Require(dir)
	if problems == nil {
		var w *work.Work
		var u *work.Unit
		var previous string
		if w, u, previous, problems = work.Advance(dir, inv.args[0]); u != nil {
			res.Unit = &advancedUnit{Unit: u.Ref, Type: u.Type, PreviousStage: previous, Stage: u.Stage}
			res.ChangedPaths = []string{w.Path}
		}
	}
	res.Diagnostics = diagnostics(problems, dir)
	for i, p := range problems {
		if p.Code == work.CodeAnchoredValue {
			res.Diagnostics[i].Next = []string{"wa-cli help advance --json"}
		}
	}
	return finish(inv, res, func(w io.Writer) error {
		if res.Unit == nil {
			return nil
		}
		_, err := fmt.Fprintf(w, "单元（Unit）%s 已从 %s 推进到 %s\n", res.Unit.Unit, res.Unit.PreviousStage, res.Unit.Stage)
		return err
	})
}

func runNext(inv *invocation) int {
	dir := projectDir(inv)
	res := &nextResult{Steps: []stepView{}, PendingAuthorReviews: []pendingReviewView{}}
	problems := workspace.Require(dir)
	if problems == nil {
		var works []*work.Work
		if len(inv.args) == 1 {
			var w *work.Work
			var u *work.Unit
			if w, u, problems = work.Resolve(dir, inv.args[0]); u != nil {
				res.Steps = append(res.Steps, step(dir, w, u))
				works = []*work.Work{w}
			}
		} else {
			works, _, problems = work.LoadAll(dir)
			for _, w := range works {
				for _, u := range w.Units() {
					// 汇总单元的阶段由子单元汇总：推导阶段为 done 时不再列出（同 next-skips-done）。
					if w.EffectiveStage(u) != catalog.StageDone {
						res.Steps = append(res.Steps, step(dir, w, u))
					}
				}
			}
		}
		var rps []workspace.Problem
		res.PendingAuthorReviews, rps = pendingReviews(dir, works)
		problems = append(problems, rps...)
	}
	res.Diagnostics = diagnostics(problems, dir)
	return finish(inv, res, func(w io.Writer) error {
		var b strings.Builder
		if len(res.Steps) == 0 && len(res.PendingAuthorReviews) == 0 && !hasError(res.Diagnostics) {
			b.WriteString("没有需要推进的单元（Unit）\n")
		}
		for _, s := range res.Steps {
			stage := s.Stage
			if s.Progress != nil {
				stage = fmt.Sprintf("%s，进度 %d/%d", stage, s.Progress.Done, s.Progress.Total)
			}
			fmt.Fprintf(&b, "%s（阶段 %s）：%s\n", s.Unit, stage, s.Action)
			for _, bl := range s.Blockers {
				fmt.Fprintf(&b, "  阻塞项：%s\n", bl.Message)
			}
			for _, c := range s.Commands {
				fmt.Fprintf(&b, "  下一步：%s\n", c)
			}
			for _, f := range s.Feedback {
				fmt.Fprintf(&b, "  反馈：%s\n", feedbackText(f))
			}
		}
		b.WriteString(pendingReviewsText(res.PendingAuthorReviews))
		_, err := io.WriteString(w, b.String())
		return err
	})
}

func step(dir string, w *work.Work, u *work.Unit) stepView {
	blockers, commands := progress(dir, w, u)
	s := stepView{Unit: u.Ref, Stage: w.EffectiveStage(u), Blockers: blockers, Action: action(dir, w, u, blockers), Commands: commands,
		Handoff:  latestHandoff(dir),
		Feedback: pendingFeedback(dir, w, u)}
	if w.Aggregated(u) {
		done, total := w.Progress(u)
		s.Progress = &progressView{Done: done, Total: total}
	}
	if n := len(s.Feedback); n > 0 {
		s.Action = fmt.Sprintf("先处理 %d 条未处理的作者反馈（Feedback）；%s", n, s.Action)
	}
	return s
}

// progress 返回单元的阻塞项与可以直接运行的下一步命令，next 与 status 共用；数组永不为 nil。
// 汇总单元（Aggregated Unit）不能推进：阻塞项为未完成子单元的汇总，commands 恒为空数组
// （逐场景的可执行命令列在各自的条目上）。
func progress(dir string, w *work.Work, u *work.Unit) ([]blockerView, []string) {
	blockers, commands := []blockerView{}, []string{}
	if w.Aggregated(u) {
		for _, b := range w.AggregatedBlockers(dir, u) {
			v := blockerView{Code: b.Code, Artifact: b.Artifact, Path: b.Path, Message: b.Message}
			if b.Decision != "" {
				v.Decision = &b.Decision
			}
			blockers = append(blockers, v)
		}
		return blockers, commands
	}
	for _, b := range w.Blockers(dir, u) {
		v := blockerView{Code: b.Code, Artifact: b.Artifact, Path: b.Path, Message: b.Message}
		if b.Decision != "" {
			v.Decision = &b.Decision
		}
		blockers = append(blockers, v)
		// 被缺失产物阻塞时只能列出为该产物登记的生成命令（见 artifactNext）。
		cmds := blockerNext(dir, b.Code, b.Path)
		if b.Code == work.CodeMissingArtifact {
			cmds = artifactNext(dir, u, b.Artifact, b.Path)
		}
		for _, c := range cmds {
			if !slices.Contains(commands, c) {
				commands = append(commands, c)
			}
		}
	}
	if u.Stage != catalog.StageDone && len(blockers) == 0 {
		commands = append(commands, shellCommand("wa-cli", "advance", u.Ref, "--dir", dir, "--json"))
	}
	return blockers, commands
}

// blockerNext 返回解除一个阻塞项的下一步命令（brief-gate、decision-blocking）；缺失产物的命令由 artifactNext 登记。advance 的诊断
// （经 next）与 next、status 的 commands 都由它给出；path 为阻塞项的路径。
func blockerNext(dir, code, path string) []string {
	switch code {
	case brief.CodeInvalid:
		return []string{shellCommand("wa-cli", "validate", path, "--dir", dir, "--json")}
	case brief.CodeMissingField, brief.CodeUnsourcedMessage, brief.CodeUnsourcedPremise:
		ref := work.RefOf(path[:strings.LastIndex(path, "/")])
		return []string{shellCommand("wa-cli", "brief", "show", ref, "--dir", dir, "--json"), "wa-cli help brief set --json"}
	case outline.CodeInvalid:
		return []string{shellCommand("wa-cli", "validate", path, "--dir", dir, "--json"), shellCommand("wa-cli", "validate", "--dir", dir, "--json")}
	case outline.CodeUncoveredMessage, outline.CodeMissingReason, outline.CodeMissingPart, outline.CodeMethodOrder, outline.CodeUnknownRole:
		ref := work.RefOf(path[:strings.LastIndex(path, "/")])
		return []string{shellCommand("wa-cli", "outline", "show", ref, "--dir", dir, "--json"), "wa-cli help outline set --json"}
	case marketing.CodeInvalid:
		return []string{shellCommand("wa-cli", "validate", path, "--dir", dir, "--json")}
	case marketing.CodeMissingField, marketing.CodeTooManyHeadlines:
		ref := work.RefOf(path[:strings.LastIndex(path, "/")])
		return []string{shellCommand("wa-cli", "marketing", "show", ref, "--dir", dir, "--json"), "wa-cli help marketing set --json"}
	case marketing.CodeHeadlineUnasked, marketing.CodeHeadlineStale:
		ref := work.RefOf(path[:strings.LastIndex(path, "/")])
		return []string{shellCommand("wa-cli", "marketing", "show", ref, "--dir", dir, "--json"), "wa-cli help marketing ask --json"}
	case brief.CodeUnconfirmedMessage, brief.CodeUnconfirmedPremise, decision.CodeBlocking, decision.CodeInvalidQueue:
		return []string{shellCommand("wa-cli", "decisions", "--dir", dir, "--json")}
	case review.CodeAcceptanceStale, review.CodeAcceptanceInvalid, review.CodeAcceptanceWeakerGate:
		// 验收记录在单元目录下；重新审稿与验收后，review show 与 advance 能复查它。
		ref := work.RefOf(path[:strings.LastIndex(path, "/")])
		return []string{shellCommand("wa-cli", "review", "show", ref, "--dir", dir, "--json"), "wa-cli help accept --json"}
	}
	return nil
}

// artifactNext 返回为缺失的必需产物（Required Artifact）登记的命令：每个登记的产物至少有一条不是 wa-cli help 的可执行命令
// （查看现状或起草所需的上下文），写入命令需要作者或 Agent 给出取值，以 help 给出用法（cli-contract/runnable-next）。
// 场景（scene）单元的大纲以场景契约（Scene Contract）写入。没有登记的产物返回 nil。
func artifactNext(dir string, u *work.Unit, artifact, unitDir string) []string {
	ref := work.RefOf(unitDir)
	switch artifact {
	case "brief":
		return []string{shellCommand("wa-cli", "brief", "show", ref, "--dir", dir, "--json"), "wa-cli help brief set --json"}
	case "outline":
		if u.Type == outline.SceneType {
			return []string{shellCommand("wa-cli", "outline", "show", ref, "--dir", dir, "--json"), "wa-cli help scene contract --json"}
		}
		return []string{shellCommand("wa-cli", "outline", "show", ref, "--dir", dir, "--json"), "wa-cli help outline set --json"}
	case "draft":
		return []string{shellCommand("wa-cli", "context", ref, "--dir", dir, "--json"), "wa-cli help draft write --json"}
	case "marketing":
		return []string{shellCommand("wa-cli", "marketing", "show", ref, "--dir", dir, "--json"), "wa-cli help marketing set --json"}
	case "review":
		return []string{"wa-cli help review record --json", shellCommand("wa-cli", "provenance", ref, "--dir", dir, "--json")}
	case "acceptance":
		return []string{shellCommand("wa-cli", "review", "show", ref, "--dir", dir, "--json"), "wa-cli help accept --json"}
	}
	return nil
}

// artifactWriter 返回产出必需产物的 wa-cli 写入命令与产物文件名，供 action 说明；没有登记时 ok 为 false。
func artifactWriter(u *work.Unit, artifact string) (command, file string, ok bool) {
	switch artifact {
	case "brief":
		return "wa-cli brief set", "brief.yaml", true
	case "outline":
		if u.Type == outline.SceneType {
			return "wa-cli scene contract", "outline.yaml", true
		}
		return "wa-cli outline set", "outline.yaml", true
	case "draft":
		return "wa-cli draft write", "draft.md", true
	case "marketing":
		return "wa-cli marketing set", "marketing.yaml", true
	case "review":
		return "wa-cli review record", "review.yaml", true
	case "acceptance":
		return "wa-cli accept", "acceptance.yaml", true
	}
	return "", "", false
}

// latestHandoff 返回工作区最近一条交接记录的摘要（没有记录或文件不合法时为 nil，问题由命令层报告）。
func latestHandoff(dir string) *handoffRef {
	note, ps := handoff.Latest(dir)
	if note == nil || len(ps) > 0 {
		return nil
	}
	return &handoffRef{ID: note.ID, Todo: note.Todo}
}

// action 以人读文字说明单元的下一步。
func action(dir string, w *work.Work, u *work.Unit, blockers []blockerView) string {
	if w.Aggregated(u) {
		done, total := w.Progress(u)
		switch {
		case total == 0:
			return "汇总单元（Aggregated Unit）：下面还没有场景，先用 wa-cli unit add 添加场景"
		case done < total:
			return fmt.Sprintf("汇总单元（Aggregated Unit）：已完成 %d/%d 个场景，还有 %d 个未完成；先推进未完成的场景", done, total, total-done)
		default:
			return fmt.Sprintf("汇总单元（Aggregated Unit）：全部 %d 个场景已完成", total)
		}
	}
	switch {
	case u.Stage == catalog.StageDone:
		return "已走完全部阶段"
	case len(blockers) > 0:
		var missing, decisions, questions, gaps, accepts, elements []string
		broken := false
		for _, b := range blockers {
			switch {
			case b.Decision != nil:
				decisions = append(decisions, *b.Decision)
			case strings.HasPrefix(b.Code, "decision."):
				broken = true
			case b.Code == brief.CodeInvalid:
				todo := "修复不通过校验的写作简报（Brief）" + b.Path
				questions = append(questions, todo)
			case strings.HasPrefix(b.Code, "brief."):
				questions = append(questions, strings.TrimSuffix(b.Message, "。"))
			case strings.HasPrefix(b.Code, "acceptance."):
				accepts = append(accepts, strings.TrimSuffix(b.Message, "。"))
			case b.Code == outline.CodeInvalid:
				gaps = append(gaps, "修复不通过校验的大纲（Outline）或写作简报")
			case strings.HasPrefix(b.Code, "outline."):
				gaps = append(gaps, strings.TrimSuffix(b.Message, "。"))
			case strings.HasPrefix(b.Code, "marketing."):
				elements = append(elements, strings.TrimSuffix(b.Message, "。"))
			default:
				if cmd, file, ok := artifactWriter(u, b.Artifact); ok {
					missing = append(missing, fmt.Sprintf("%s（用 %s 写入 %s/%s）", b.Artifact, cmd, b.Path, file))
				} else {
					missing = append(missing, fmt.Sprintf("%s（在 %s/ 下写入 %s.yaml 或 %s.md）", b.Artifact, b.Path, b.Artifact, b.Artifact))
				}
			}
		}
		var todo []string
		if len(missing) > 0 {
			todo = append(todo, "产出必需产物（Required Artifact）"+strings.Join(missing, "、"))
		}
		if len(questions) > 0 {
			todo = append(todo, "向作者确认写作简报（Brief）："+strings.Join(questions, "；"))
		}
		if len(gaps) > 0 {
			todo = append(todo, "补齐大纲（Outline）的覆盖："+strings.Join(gaps, "；"))
		}
		if len(elements) > 0 {
			todo = append(todo, "补齐营销要素（Marketing Elements）："+strings.Join(elements, "；"))
		}
		if len(accepts) > 0 {
			todo = append(todo, "重新审稿（Review）并用 wa-cli accept 验收（Acceptance）："+strings.Join(accepts, "；"))
		}
		if len(decisions) > 0 {
			todo = append(todo, "请作者决定阻塞决策（Blocking）"+strings.Join(decisions, "、"))
		}
		if broken {
			todo = append(todo, "修复无法加载的决策队列（Decision Queue）")
		}
		return "先" + strings.Join(todo, "，并") + "，再推进"
	}
	if marks := impact.OpenMarks(dir, w.ID); len(marks) > 0 {
		var mine []string
		for _, m := range marks {
			if m.Scene == u.Ref {
				mine = append(mine, m.Target)
			}
		}
		if len(mine) > 0 {
			return fmt.Sprintf("阶段 %s 的必需产物已齐备，可以推进到 %s；注意：本场景有 %d 个 open 复查标记（Recheck Mark，目标 %s），复查通过后 wa-cli impact clear 解除（组稿前必须清零）",
				u.Stage, w.NextStage(u), len(mine), strings.Join(mine, "、"))
		}
	}
	return fmt.Sprintf("阶段 %s 的必需产物已齐备，可以推进到 %s", u.Stage, w.NextStage(u))
}

// unitViews 按作品与单元树先序列出每个单元，供 status 输出；汇总单元的阶段与阻塞项取汇总值。
func unitViews(dir string, works []*work.Work) []unitView {
	out := []unitView{}
	for _, w := range works {
		for _, u := range w.Units() {
			v := unitView{Unit: u.Ref, Work: w.ID, Type: u.Type, Stage: w.EffectiveStage(u)}
			if u.Parent != nil {
				v.Parent = &u.Parent.Ref
			}
			for p := u.Parent; p != nil; p = p.Parent {
				v.depth++
			}
			v.Blockers, v.Commands = progress(dir, w, u)
			if w.Aggregated(u) {
				done, total := w.Progress(u)
				v.Progress = &progressView{Done: done, Total: total}
			}
			out = append(out, v)
		}
	}
	return out
}
