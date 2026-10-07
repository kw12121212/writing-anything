package cli

import (
	"fmt"
	"io"
	"strings"
	"time"

	"github.com/kw12121212/writing-anything/internal/impact"
	"github.com/kw12121212/writing-anything/internal/outline"
	"github.com/kw12121212/writing-anything/internal/state"
	"github.com/kw12121212/writing-anything/internal/work"
	"github.com/kw12121212/writing-anything/internal/workspace"
)

const stateHelp = "绑定场景（scene 单元）与设定集条目（--entry）的状态变化描述"

var stateProposeCommand = &command{
	name:    "state propose",
	summary: "在作品（Work）的状态台账（State Ledger）中登记一条状态变更提议（State Change Proposal）：" + stateHelp + "；初始为待确认（proposed），作者确认后才参与状态快照计算",
	args:    []argSpec{workArg},
	flags: []flagSpec{
		dirFlag,
		{"scene", flagString, "变化发生的场景；本作品的 scene 类型单元引用"},
		{"entry", flagString, "发生变化的设定集（Canon）条目 id"},
		{"state", flagString, "变化后的状态描述"},
		{"id", flagString, "条目 id；省略时为 state-<n>"},
	},
	outputKeys: []outputKey{{"event", "新条目：id、scene、entry、state、status、confirmation、channel（确认渠道（Confirmation Channel）：cli 或 preview，待确认为 null）与 decided_at；失败时为 null"}},
	exitCodes:  standardExitCodes,
	run:        runStatePropose,
}

var stateListCommand = &command{
	name:    "state list",
	summary: "列出作品（Work）状态台账（State Ledger）中待作者确认（proposed）的状态变更提议（State Change Proposal）；--all 列出全部",
	args:    []argSpec{workArg},
	flags: []flagSpec{
		dirFlag,
		{"all", flagBool, "列出全部条目，不只列待确认（proposed）的"},
	},
	outputKeys: []outputKey{{"events", "按绑定场景的单元树先序排列的条目数组，每项含 id、scene、entry、state、status、confirmation、channel（确认渠道（Confirmation Channel）：cli 或 preview，待确认为 null）与 decided_at"}},
	exitCodes:  standardExitCodes,
	run:        runStateList,
}

var stateConfirmCommand = &command{
	name:    "state confirm",
	summary: "记录作者对一条状态变更提议（State Change Proposal）的确认：--confirmed-by-author 与 --confirmation 必须同给；确认后条目参与状态快照（State Snapshot）计算",
	args: []argSpec{
		workArg,
		{"id", "状态变更提议的条目 id", true},
	},
	flags: []flagSpec{
		dirFlag,
		{"confirmed-by-author", flagBool, "作者确认本条提议"},
		{"confirmation", flagString, "作者确认原话摘要"},
	},
	outputKeys: []outputKey{{"event", "确认后的条目：id、scene、entry、state、status、confirmation、channel（确认渠道（Confirmation Channel）：cli 或 preview，待确认为 null）与 decided_at；失败时为 null"}},
	exitCodes:  standardExitCodes,
	run:        runStateConfirm,
}

var stateRejectCommand = &command{
	name:    "state reject",
	summary: "记录作者对一条状态变更提议（State Change Proposal）的拒绝或撤销：--confirmed-by-author 与 --confirmation 必须同给；已确认条目撤销后不再参与状态快照计算",
	args: []argSpec{
		workArg,
		{"id", "状态变更提议的条目 id", true},
	},
	flags: []flagSpec{
		dirFlag,
		{"confirmed-by-author", flagBool, "作者拒绝本条提议（或撤销已确认的条目）"},
		{"confirmation", flagString, "作者拒绝或撤销的原话摘要"},
	},
	outputKeys: []outputKey{{"event", "拒绝或撤销后的条目：id、scene、entry、state、status、confirmation、channel（确认渠道（Confirmation Channel）：cli 或 preview，待确认为 null）与 decided_at；失败时为 null"}},
	exitCodes:  standardExitCodes,
	run:        runStateReject,
}

var stateSetCommand = &command{
	name:    "state set",
	summary: "修改状态台账（State Ledger）中一条状态变更提议（State Change Proposal）的绑定场景或状态描述，保留其他内容与注释；修改已确认条目须作者重新确认",
	args: []argSpec{
		workArg,
		{"id", "状态变更提议的条目 id", true},
	},
	flags: []flagSpec{
		dirFlag,
		{"scene", flagString, "变化发生的场景；省略时不修改，仍须是 scene 单元"},
		{"state", flagString, "变化后的状态描述；省略时不修改"},
		{"confirmed-by-author", flagBool, "作者重新确认本条已确认条目的修改"},
		{"confirmation", flagString, "作者重新确认的原话摘要；修改已确认（confirmed）条目时必填"},
	},
	outputKeys: []outputKey{{"event", "修改后的条目：id、scene、entry、state、status、confirmation、channel（确认渠道（Confirmation Channel）：cli 或 preview，待确认为 null）与 decided_at；失败时为 null"}, impactOutputKey},
	exitCodes:  standardExitCodes,
	run:        runStateSet,
}

var stateAtCommand = &command{
	name:    "state at",
	summary: "重放此前已确认的状态变更提议，输出场景（Scene）开始时的状态快照（State Snapshot）：同一输入总是得到同一结果；未确认的提议不参与计算",
	args: []argSpec{
		{"unit", "场景单元（Unit）引用；必须是 scene 类型", true},
	},
	flags:      []flagSpec{dirFlag},
	outputKeys: []outputKey{{"snapshot", "按 entry 升序的状态快照数组，每项含 entry（设定条目 id）、name、state、proposal（最后生效的条目 id）与 scene"}},
	exitCodes:  standardExitCodes,
	run:        runStateAt,
}

type stateEventView struct {
	Work string `json:"work"`
	state.Event
}

// stateViewOf 返回条目的输出形态：已决定的条目 channel 缺失时输出 cli，待确认的条目为 null。
func stateViewOf(work string, e state.Event) *stateEventView {
	e.Channel = channelView(e.Status != state.StatusProposed, e.Channel)
	return &stateEventView{Work: work, Event: e}
}

type stateEventResult struct {
	Result
	Event *stateEventView `json:"event"`
}

type stateSetResult struct {
	Result
	Event  *stateEventView `json:"event"`
	Impact []impact.Ripple `json:"impact"`
}

type stateListResult struct {
	Result
	Events []stateEventView `json:"events"`
}

type stateAtResult struct {
	Result
	Snapshot []state.Item `json:"snapshot"`
}

// statusText 返回状态变更提议状态的人读标注；proposed 不标注。
func stateStatusText(status string) string {
	if text, ok := state.Statuses[status]; ok && status != state.StatusProposed {
		return "［" + text + "］"
	}
	return ""
}

// resolveStateWork 按作品引用加载 dir 下的作品；状态台账是作品级产物，子单元引用不可用。
func resolveStateWork(dir, ref string) (*work.Work, []workspace.Problem) {
	w, u, ps := work.Resolve(dir, ref)
	if u != nil && u.Parent != nil {
		return nil, []workspace.Problem{{Code: work.CodeNotFound, Severity: workspace.SeverityError, Path: w.Path,
			Message: "状态台账（State Ledger）是作品级产物，请使用作品 id " + w.ID + "，不要带子单元"}}
	}
	return w, ps
}

func runStatePropose(inv *invocation) int {
	dir := projectDir(inv)
	res := &stateEventResult{}
	problems := workspace.Require(dir)
	if problems == nil {
		var w *work.Work
		if w, problems = resolveStateWork(dir, inv.args[0]); w != nil {
			var e *state.Event
			var changed []string
			in := state.ProposeInput{Scene: inv.values["scene"], Entry: inv.values["entry"], State: inv.values["state"], ID: inv.values["id"]}
			if e, changed, problems = state.Propose(dir, w, in); e != nil {
				res.Event, res.ChangedPaths = stateViewOf(w.ID, *e), changed
			}
		}
	}
	res.Diagnostics = diagnostics(problems, dir)
	stateParamNext(res.Diagnostics, dir, inv.args[0], "state propose")
	return finish(inv, res, func(w io.Writer) error {
		if res.Event == nil {
			return nil
		}
		_, err := fmt.Fprintf(w, "已在作品 %s 登记状态变更提议（State Change Proposal）%s（%s → %s）：%s\n",
			res.Event.Work, res.Event.ID, res.Event.Scene, res.Event.Entry, res.Event.State)
		return err
	})
}

func runStateList(inv *invocation) int {
	dir := projectDir(inv)
	res := &stateListResult{Events: []stateEventView{}}
	problems := workspace.Require(dir)
	if problems == nil {
		var w *work.Work
		if w, problems = resolveStateWork(dir, inv.args[0]); w != nil {
			var events []state.Event
			if events, problems = state.List(dir, w, inv.flag("all")); events != nil {
				for _, e := range events {
					res.Events = append(res.Events, *stateViewOf(w.ID, e))
				}
			}
		}
	}
	res.Diagnostics = diagnostics(problems, dir)
	stateNotFoundNext(res.Diagnostics, inv.args[0], dir)
	return finish(inv, res, func(w io.Writer) error {
		var b strings.Builder
		if len(res.Events) == 0 && !hasError(res.Diagnostics) {
			if inv.flag("all") {
				fmt.Fprintf(&b, "作品 %s 还没有状态变更提议（State Change Proposal）\n", inv.args[0])
			} else {
				fmt.Fprintf(&b, "作品 %s 没有待作者确认（proposed）的状态变更提议\n", inv.args[0])
			}
		}
		for _, e := range res.Events {
			if e.Confirmation != nil {
				fmt.Fprintf(&b, "%s（%s → %s）%s：%s（作者：%s，%s）\n", e.ID, e.Scene, e.Entry, stateStatusText(e.Status), e.State, *e.Confirmation, deref(e.DecidedAt))
			} else {
				fmt.Fprintf(&b, "%s（%s → %s）%s：%s\n", e.ID, e.Scene, e.Entry, stateStatusText(e.Status), e.State)
			}
		}
		_, err := io.WriteString(w, b.String())
		return err
	})
}

// deref 返回指针指向的字符串；指针为 nil 时返回空串（人读输出用）。
func deref(p *string) string {
	if p == nil {
		return ""
	}
	return *p
}

// runStateDecide 是 state confirm 与 state reject 的共同入口；name 为命令名（用于诊断的 next）。
func runStateDecide(inv *invocation, name, verb string, decide func(root string, w *work.Work, id, confirmation string, now time.Time) (*state.Event, []string, []workspace.Problem)) int {
	dir := projectDir(inv)
	res := &stateEventResult{}
	problems := workspace.Require(dir)
	if problems == nil {
		var w *work.Work
		if w, problems = resolveStateWork(dir, inv.args[0]); w != nil {
			if !inv.flag("confirmed-by-author") || strings.TrimSpace(inv.values["confirmation"]) == "" {
				problems = []workspace.Problem{{Code: state.CodeIncompleteConfirmation, Severity: workspace.SeverityError, Field: "confirmation",
					Message: "作者" + verb + "必须给出 --confirmed-by-author 与非空白的 --confirmation（作者原话摘要）"}}
			} else {
				var e *state.Event
				var changed []string
				if e, changed, problems = decide(dir, w, inv.args[1], inv.values["confirmation"], now()); e != nil {
					res.Event, res.ChangedPaths = stateViewOf(w.ID, *e), changed
				}
			}
		}
	}
	res.Diagnostics = diagnostics(problems, dir)
	stateParamNext(res.Diagnostics, dir, inv.args[0], name)
	stateNotFoundNext(res.Diagnostics, inv.args[0], dir)
	return finish(inv, res, func(w io.Writer) error {
		if res.Event == nil {
			return nil
		}
		_, err := fmt.Fprintf(w, "已%s作品 %s 的状态变更提议（State Change Proposal）%s（%s → %s）%s：%s\n",
			verb, res.Event.Work, res.Event.ID, res.Event.Scene, res.Event.Entry, stateStatusText(res.Event.Status), res.Event.State)
		return err
	})
}

func runStateConfirm(inv *invocation) int {
	return runStateDecide(inv, "state confirm", "确认", state.Confirm)
}

func runStateReject(inv *invocation) int {
	return runStateDecide(inv, "state reject", "拒绝或撤销", state.Reject)
}

func runStateSet(inv *invocation) int {
	dir := projectDir(inv)
	res := &stateSetResult{Impact: []impact.Ripple{}}
	var auto []Diagnostic
	problems := workspace.Require(dir)
	if problems == nil {
		var w *work.Work
		if w, problems = resolveStateWork(dir, inv.args[0]); w != nil {
			var in state.SetInput
			if v, ok := inv.values["scene"]; ok {
				in.Scene = &v
			}
			if v, ok := inv.values["state"]; ok {
				in.State = &v
			}
			in.ConfirmedByAuthor = inv.flag("confirmed-by-author")
			in.Confirmation = inv.values["confirmation"]
			var before *state.Event
			if events, _ := state.List(dir, w, true); events != nil {
				for i := range events {
					if events[i].ID == inv.args[1] {
						before = &events[i]
					}
				}
			}
			var pre []impact.Affected
			if before != nil {
				pre = preAffected(dir, w, before.Entry)
			}
			var e *state.Event
			var changed []string
			if e, changed, problems = state.Set(dir, w, inv.args[1], in, now()); e != nil {
				res.Event, res.ChangedPaths = stateViewOf(w.ID, *e), changed
				// 语义字段：绑定场景与状态描述；影响目标为绑定的设定条目，只计不早于新旧绑定场景的场景。修改前读不到
				// 原条目（台账有问题）时无从比较，按有变化处理。
				if len(changed) > 0 && (before == nil || impact.SemanticKey(before.Scene, before.State) != impact.SemanticKey(e.Scene, e.State)) {
					from := e.Scene
					if before != nil {
						from = earlierScene(w, before.Scene, e.Scene)
					}
					var more []string
					res.Impact, more, auto = autoImpact(dir, w, []impactTarget{{target: e.Entry, from: from, pre: pre}})
					res.ChangedPaths = append(res.ChangedPaths, more...)
				}
			}
		}
	}
	res.Diagnostics = diagnostics(problems, dir)
	stateParamNext(res.Diagnostics, dir, inv.args[0], "state set")
	stateNotFoundNext(res.Diagnostics, inv.args[0], dir)
	res.Diagnostics = append(res.Diagnostics, auto...)
	return finish(inv, res, func(w io.Writer) error {
		if res.Event == nil {
			return nil
		}
		verb := "已修改"
		if len(res.ChangedPaths) == 0 {
			verb = "取值未变，未修改"
		}
		_, err := fmt.Fprintf(w, "%s作品 %s 的状态变更提议（State Change Proposal）%s（%s → %s）%s：%s\n%s",
			verb, res.Event.Work, res.Event.ID, res.Event.Scene, res.Event.Entry, stateStatusText(res.Event.Status), res.Event.State, impactText(res.Impact))
		return err
	})
}

func runStateAt(inv *invocation) int {
	dir := projectDir(inv)
	res := &stateAtResult{Snapshot: []state.Item{}}
	problems := workspace.Require(dir)
	if problems == nil {
		if w, u, ps := work.Resolve(dir, inv.args[0]); u != nil {
			problems = ps
			switch {
			case u.Type != outline.SceneType:
				problems = []workspace.Problem{{Code: state.CodeUnknownUnit, Severity: workspace.SeverityError, Field: "unit",
					Message: "单元 " + inv.args[0] + " 的类型是 " + u.Type + "，不是场景（scene）；state at 只接受 scene 单元"}}
			default:
				var items []state.Item
				if items, problems = state.Snapshot(dir, w, u); len(problems) == 0 {
					res.Snapshot = items
				}
			}
		} else {
			problems = ps
			// 单元引用指向不存在的单元时按 state-at 主规格报告 state.unknown-unit（field 为 unit）；
			// 作品不存在等问题保留 work 包的诊断。
			for i, p := range ps {
				if p.Code == work.CodeUnitNotFound {
					ps[i] = workspace.Problem{Code: state.CodeUnknownUnit, Severity: workspace.SeverityError, Field: "unit",
						Message: "单元引用 " + inv.args[0] + " 不存在；state at 只接受 scene 单元"}
					break
				}
			}
		}
	}
	res.Diagnostics = diagnostics(problems, dir)
	helpNext(res.Diagnostics, "state at", []string{state.CodeUnknownUnit})
	return finish(inv, res, func(w io.Writer) error {
		var b strings.Builder
		if len(res.Snapshot) == 0 && !hasError(res.Diagnostics) {
			fmt.Fprintf(&b, "场景 %s 开始时没有已生效的状态变化，各设定条目处于设定集（Canon）定义的初始状态\n", inv.args[0])
		}
		for _, it := range res.Snapshot {
			name := ""
			if it.Name != nil {
				name = "（" + *it.Name + "）"
			}
			fmt.Fprintf(&b, "%s%s：%s（生效于 %s）\n", it.Entry, name, it.State, it.Scene)
		}
		_, err := io.WriteString(w, b.String())
		return err
	})
}

// stateParamNext 把参数引起的问题（不填 path）的下一步改为 state 命令用法与该作品的条目列表；已有 state.yaml
// 中其他条目的问题保留默认的复查命令。name 为本次运行的命令名。
func stateParamNext(ds []Diagnostic, dir, ref, name string) {
	var own []Diagnostic
	var idx []int
	for i, d := range ds {
		// 已有 state.yaml 中其他条目的问题（带 path）保留默认的复查命令，只改写参数引起的问题（不填 path）。
		if d.Path != "" {
			continue
		}
		if d.Code == state.CodeAnchoredValue || d.Code == state.CodeUnknownUnit || d.Code == state.CodeUnknownEntry ||
			d.Code == state.CodeIncompleteConfirmation || d.Code == state.CodeAlreadyDecided ||
			paramField(d.Field, "id", "scene", "entry", "state", "confirmation") {
			own, idx = append(own, d), append(idx, i)
		}
	}
	// artifact.schema-violation（空白 --state、非法 --id 等写入产物字段的参数问题）与 artifact.duplicate-id
	// （--id 与既有条目重复）同样由本次参数引起，next 给用法而不是单文件复查（cli-contract/next-rechecks-problem）。
	helpNext(own, name, []string{"input.invalid-value", "artifact.schema-violation", "artifact.duplicate-id", state.CodeUnknownUnit, state.CodeAnchoredValue},
		shellCommand("wa-cli", "state", "list", ref, "--all", "--dir", dir, "--json"))
	for i := range own {
		switch own[i].Code {
		case state.CodeUnknownEntry:
			// --entry 引用的设定条目不存在：先看设定集里有什么，再查用法。
			own[i].Next = []string{
				shellCommand("wa-cli", "canon", "list", ref, "--dir", dir, "--json"),
				"wa-cli help " + name + " --json",
			}
		case state.CodeIncompleteConfirmation, state.CodeAlreadyDecided:
			own[i].Next = []string{
				shellCommand("wa-cli", "state", "list", ref, "--all", "--dir", dir, "--json"),
				"wa-cli help " + name + " --json",
			}
		}
	}
	for k, i := range idx {
		ds[i] = own[k]
	}
}

// stateNotFoundNext 把 state.not-found 的下一步改为列出该作品的全部状态变更提议。
func stateNotFoundNext(ds []Diagnostic, ref, dir string) {
	for i := range ds {
		if ds[i].Code == state.CodeNotFound {
			ds[i].Next = []string{shellCommand("wa-cli", "state", "list", ref, "--all", "--dir", dir, "--json")}
		}
	}
}
