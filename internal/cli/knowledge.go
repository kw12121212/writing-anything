package cli

import (
	"fmt"
	"io"
	"strings"
	"time"

	"github.com/kw12121212/writing-anything/internal/knowledge"
	"github.com/kw12121212/writing-anything/internal/outline"
	"github.com/kw12121212/writing-anything/internal/work"
	"github.com/kw12121212/writing-anything/internal/workspace"
)

const knowledgeHelp = "获知者（--character，设定集 character 条目 id 或 reader 表示读者（Reader））对设定集条目（--entry）的获知"

var knowledgeProposeCommand = &command{
	name:    "knowledge propose",
	summary: "在作品（Work）的知情表（Knowledge State）中登记一条知情事件（Knowledge Event）：" + knowledgeHelp + "；初始为待确认（proposed），作者确认后才参与已知列表计算",
	args:    []argSpec{workArg},
	flags: []flagSpec{
		dirFlag,
		{"scene", flagString, "获知发生的场景；本作品的 scene 类型单元引用"},
		{"entry", flagString, "被获知的设定集（Canon）条目 id"},
		{"character", flagString, "获知者；设定集 character 条目 id 或固定值 reader（读者）"},
		{"id", flagString, "条目 id；省略时为 know-<n>"},
	},
	outputKeys: []outputKey{{"event", "新条目：id、scene、entry、character、status、confirmation 与 decided_at；失败时为 null"}},
	exitCodes:  standardExitCodes,
	run:        runKnowledgePropose,
}

var knowledgeListCommand = &command{
	name:    "knowledge list",
	summary: "列出作品（Work）知情表（Knowledge State）中待作者确认（proposed）的知情事件（Knowledge Event）；--all 列出全部",
	args:    []argSpec{workArg},
	flags: []flagSpec{
		dirFlag,
		{"all", flagBool, "列出全部条目，不只列待确认（proposed）的"},
	},
	outputKeys: []outputKey{{"events", "按绑定场景的单元树先序排列的条目数组，每项含 id、scene、entry、character、status、confirmation 与 decided_at"}},
	exitCodes:  standardExitCodes,
	run:        runKnowledgeList,
}

var knowledgeConfirmCommand = &command{
	name:    "knowledge confirm",
	summary: "记录作者对一条知情事件（Knowledge Event）的确认：--confirmed-by-author 与 --confirmation 必须同给；确认后条目参与已知列表计算",
	args: []argSpec{
		workArg,
		{"id", "知情事件的条目 id", true},
	},
	flags: []flagSpec{
		dirFlag,
		{"confirmed-by-author", flagBool, "作者确认本条知情事件"},
		{"confirmation", flagString, "作者确认原话摘要"},
	},
	outputKeys: []outputKey{{"event", "确认后的条目：id、scene、entry、character、status、confirmation 与 decided_at；失败时为 null"}},
	exitCodes:  standardExitCodes,
	run:        runKnowledgeConfirm,
}

var knowledgeRejectCommand = &command{
	name:    "knowledge reject",
	summary: "记录作者对一条知情事件（Knowledge Event）的拒绝或撤销：--confirmed-by-author 与 --confirmation 必须同给；已确认条目撤销后不再参与已知列表计算",
	args: []argSpec{
		workArg,
		{"id", "知情事件的条目 id", true},
	},
	flags: []flagSpec{
		dirFlag,
		{"confirmed-by-author", flagBool, "作者拒绝本条知情事件（或撤销已确认的条目）"},
		{"confirmation", flagString, "作者拒绝或撤销的原话摘要"},
	},
	outputKeys: []outputKey{{"event", "拒绝或撤销后的条目：id、scene、entry、character、status、confirmation 与 decided_at；失败时为 null"}},
	exitCodes:  standardExitCodes,
	run:        runKnowledgeReject,
}

var knowledgeSetCommand = &command{
	name:    "knowledge set",
	summary: "修改知情表（Knowledge State）中一条知情事件（Knowledge Event）的绑定场景、设定条目或获知者，保留其他内容与注释；修改已确认条目须作者重新确认",
	args: []argSpec{
		workArg,
		{"id", "知情事件的条目 id", true},
	},
	flags: []flagSpec{
		dirFlag,
		{"scene", flagString, "获知发生的场景；省略时不修改，仍须是 scene 单元"},
		{"entry", flagString, "被获知的设定集条目 id；省略时不修改"},
		{"character", flagString, "获知者；省略时不修改，仍须是 character 条目或 reader"},
		{"confirmed-by-author", flagBool, "作者重新确认本条已确认条目的修改"},
		{"confirmation", flagString, "作者重新确认的原话摘要；修改已确认（confirmed）条目时必填"},
	},
	outputKeys: []outputKey{{"event", "修改后的条目：id、scene、entry、character、status、confirmation 与 decided_at；失败时为 null"}},
	exitCodes:  standardExitCodes,
	run:        runKnowledgeSet,
}

var knowledgeAtCommand = &command{
	name:    "knowledge at",
	summary: "重放此前已确认的知情事件，输出场景（Scene）开始时某获知者（人物或读者）已知的设定条目：同一输入总是得到同一结果；未确认的事件不参与计算",
	args: []argSpec{
		{"unit", "场景单元（Unit）引用；必须是 scene 类型", true},
	},
	flags: []flagSpec{
		dirFlag,
		{"character", flagString, "获知者；设定集 character 条目 id 或固定值 reader（读者）"},
	},
	outputKeys: []outputKey{{"knowledge", "按 entry 升序的已知条目数组，每项含 entry（设定条目 id）、name、event（最早获知事件的 id）与 scene"}},
	exitCodes:  standardExitCodes,
	run:        runKnowledgeAt,
}

type knowledgeEventView struct {
	Work string `json:"work"`
	knowledge.Event
}

type knowledgeEventResult struct {
	Result
	Event *knowledgeEventView `json:"event"`
}

type knowledgeListResult struct {
	Result
	Events []knowledgeEventView `json:"events"`
}

type knowledgeAtResult struct {
	Result
	Knowledge []knowledge.Item `json:"knowledge"`
}

// knowledgeStatusText 返回知情事件状态的人读标注；proposed 不标注。
func knowledgeStatusText(status string) string {
	if text, ok := knowledge.Statuses[status]; ok && status != knowledge.StatusProposed {
		return "［" + text + "］"
	}
	return ""
}

// knowerText 返回获知者的人读写法；reader 标注为读者。
func knowerText(character string) string {
	if character == knowledge.Reader {
		return "读者（reader）"
	}
	return character
}

// resolveKnowledgeWork 按作品引用加载 dir 下的作品；知情表是作品级产物，子单元引用不可用。
func resolveKnowledgeWork(dir, ref string) (*work.Work, []workspace.Problem) {
	w, u, ps := work.Resolve(dir, ref)
	if u != nil && u.Parent != nil {
		return nil, []workspace.Problem{{Code: work.CodeNotFound, Severity: workspace.SeverityError, Path: w.Path,
			Message: "知情表（Knowledge State）是作品级产物，请使用作品 id " + w.ID + "，不要带子单元"}}
	}
	return w, ps
}

func runKnowledgePropose(inv *invocation) int {
	dir := projectDir(inv)
	res := &knowledgeEventResult{}
	problems := workspace.Require(dir)
	if problems == nil {
		var w *work.Work
		if w, problems = resolveKnowledgeWork(dir, inv.args[0]); w != nil {
			var e *knowledge.Event
			var changed []string
			in := knowledge.ProposeInput{Scene: inv.values["scene"], Entry: inv.values["entry"], Character: inv.values["character"], ID: inv.values["id"]}
			if e, changed, problems = knowledge.Propose(dir, w, in); e != nil {
				res.Event, res.ChangedPaths = &knowledgeEventView{Work: w.ID, Event: *e}, changed
			}
		}
	}
	res.Diagnostics = diagnostics(problems, dir)
	knowledgeParamNext(res.Diagnostics, dir, inv.args[0], "knowledge propose")
	return finish(inv, res, func(w io.Writer) error {
		if res.Event == nil {
			return nil
		}
		_, err := fmt.Fprintf(w, "已在作品 %s 登记知情事件（Knowledge Event）%s（%s 于 %s 获知 %s）\n",
			res.Event.Work, res.Event.ID, knowerText(res.Event.Character), res.Event.Scene, res.Event.Entry)
		return err
	})
}

func runKnowledgeList(inv *invocation) int {
	dir := projectDir(inv)
	res := &knowledgeListResult{Events: []knowledgeEventView{}}
	problems := workspace.Require(dir)
	if problems == nil {
		var w *work.Work
		if w, problems = resolveKnowledgeWork(dir, inv.args[0]); w != nil {
			var events []knowledge.Event
			if events, problems = knowledge.List(dir, w, inv.flag("all")); events != nil {
				for _, e := range events {
					res.Events = append(res.Events, knowledgeEventView{Work: w.ID, Event: e})
				}
			}
		}
	}
	res.Diagnostics = diagnostics(problems, dir)
	knowledgeNotFoundNext(res.Diagnostics, inv.args[0], dir)
	return finish(inv, res, func(w io.Writer) error {
		var b strings.Builder
		if len(res.Events) == 0 && !hasError(res.Diagnostics) {
			if inv.flag("all") {
				fmt.Fprintf(&b, "作品 %s 还没有知情事件（Knowledge Event）\n", inv.args[0])
			} else {
				fmt.Fprintf(&b, "作品 %s 没有待作者确认（proposed）的知情事件\n", inv.args[0])
			}
		}
		for _, e := range res.Events {
			if e.Confirmation != nil {
				fmt.Fprintf(&b, "%s（%s，%s 获知 %s）%s（作者：%s，%s）\n", e.ID, e.Scene, knowerText(e.Character), e.Entry, knowledgeStatusText(e.Status), *e.Confirmation, deref(e.DecidedAt))
			} else {
				fmt.Fprintf(&b, "%s（%s，%s 获知 %s）%s\n", e.ID, e.Scene, knowerText(e.Character), e.Entry, knowledgeStatusText(e.Status))
			}
		}
		_, err := io.WriteString(w, b.String())
		return err
	})
}

// runKnowledgeDecide 是 knowledge confirm 与 knowledge reject 的共同入口；name 为命令名（用于诊断的 next）。
func runKnowledgeDecide(inv *invocation, name, verb string, decide func(root string, w *work.Work, id, confirmation string, now time.Time) (*knowledge.Event, []string, []workspace.Problem)) int {
	dir := projectDir(inv)
	res := &knowledgeEventResult{}
	problems := workspace.Require(dir)
	if problems == nil {
		var w *work.Work
		if w, problems = resolveKnowledgeWork(dir, inv.args[0]); w != nil {
			if !inv.flag("confirmed-by-author") || strings.TrimSpace(inv.values["confirmation"]) == "" {
				problems = []workspace.Problem{{Code: knowledge.CodeIncompleteConfirmation, Severity: workspace.SeverityError, Field: "confirmation",
					Message: "作者" + verb + "必须给出 --confirmed-by-author 与非空白的 --confirmation（作者原话摘要）"}}
			} else {
				var e *knowledge.Event
				var changed []string
				if e, changed, problems = decide(dir, w, inv.args[1], inv.values["confirmation"], now()); e != nil {
					res.Event, res.ChangedPaths = &knowledgeEventView{Work: w.ID, Event: *e}, changed
				}
			}
		}
	}
	res.Diagnostics = diagnostics(problems, dir)
	knowledgeParamNext(res.Diagnostics, dir, inv.args[0], name)
	knowledgeNotFoundNext(res.Diagnostics, inv.args[0], dir)
	return finish(inv, res, func(w io.Writer) error {
		if res.Event == nil {
			return nil
		}
		_, err := fmt.Fprintf(w, "已%s作品 %s 的知情事件（Knowledge Event）%s（%s 于 %s 获知 %s）%s\n",
			verb, res.Event.Work, res.Event.ID, knowerText(res.Event.Character), res.Event.Scene, res.Event.Entry, knowledgeStatusText(res.Event.Status))
		return err
	})
}

func runKnowledgeConfirm(inv *invocation) int {
	return runKnowledgeDecide(inv, "knowledge confirm", "确认", knowledge.Confirm)
}

func runKnowledgeReject(inv *invocation) int {
	return runKnowledgeDecide(inv, "knowledge reject", "拒绝或撤销", knowledge.Reject)
}

func runKnowledgeSet(inv *invocation) int {
	dir := projectDir(inv)
	res := &knowledgeEventResult{}
	problems := workspace.Require(dir)
	if problems == nil {
		var w *work.Work
		if w, problems = resolveKnowledgeWork(dir, inv.args[0]); w != nil {
			var in knowledge.SetInput
			if v, ok := inv.values["scene"]; ok {
				in.Scene = &v
			}
			if v, ok := inv.values["entry"]; ok {
				in.Entry = &v
			}
			if v, ok := inv.values["character"]; ok {
				in.Character = &v
			}
			in.ConfirmedByAuthor = inv.flag("confirmed-by-author")
			in.Confirmation = inv.values["confirmation"]
			var e *knowledge.Event
			var changed []string
			if e, changed, problems = knowledge.Set(dir, w, inv.args[1], in, now()); e != nil {
				res.Event, res.ChangedPaths = &knowledgeEventView{Work: w.ID, Event: *e}, changed
			}
		}
	}
	res.Diagnostics = diagnostics(problems, dir)
	knowledgeParamNext(res.Diagnostics, dir, inv.args[0], "knowledge set")
	knowledgeNotFoundNext(res.Diagnostics, inv.args[0], dir)
	return finish(inv, res, func(w io.Writer) error {
		if res.Event == nil {
			return nil
		}
		verb := "已修改"
		if len(res.ChangedPaths) == 0 {
			verb = "取值未变，未修改"
		}
		_, err := fmt.Fprintf(w, "%s作品 %s 的知情事件（Knowledge Event）%s（%s 于 %s 获知 %s）%s\n",
			verb, res.Event.Work, res.Event.ID, knowerText(res.Event.Character), res.Event.Scene, res.Event.Entry, knowledgeStatusText(res.Event.Status))
		return err
	})
}

func runKnowledgeAt(inv *invocation) int {
	dir := projectDir(inv)
	res := &knowledgeAtResult{Knowledge: []knowledge.Item{}}
	problems := workspace.Require(dir)
	character := inv.values["character"]
	if problems == nil && character == "" {
		// --character 必填（knowledge-at 主规格）：给出取值域而不是复查命令。
		problems = []workspace.Problem{{Code: knowledge.CodeUnknownCharacter, Severity: workspace.SeverityError, Field: "character",
			Message: "--character 必填：取值为设定集 character 条目 id 或 reader（读者）"}}
	}
	if problems == nil {
		if w, u, ps := work.Resolve(dir, inv.args[0]); u != nil {
			problems = ps
			switch {
			case u.Type != outline.SceneType:
				problems = []workspace.Problem{{Code: knowledge.CodeUnknownUnit, Severity: workspace.SeverityError, Field: "unit",
					Message: "单元 " + inv.args[0] + " 的类型是 " + u.Type + "，不是场景（scene）；knowledge at 只接受 scene 单元"}}
			default:
				var items []knowledge.Item
				if items, problems = knowledge.KnownAt(dir, w, u, character); len(problems) == 0 {
					res.Knowledge = items
				}
			}
		} else {
			problems = ps
			// 单元引用指向不存在的单元时按 knowledge-at 主规格报告 knowledge.unknown-unit（field 为 unit）；
			// 作品不存在等问题保留 work 包的诊断。
			for i, p := range ps {
				if p.Code == work.CodeUnitNotFound {
					ps[i] = workspace.Problem{Code: knowledge.CodeUnknownUnit, Severity: workspace.SeverityError, Field: "unit",
						Message: "单元引用 " + inv.args[0] + " 不存在；knowledge at 只接受 scene 单元"}
					break
				}
			}
		}
	}
	res.Diagnostics = diagnostics(problems, dir)
	knowledgeCharacterNext(res.Diagnostics, inv.args[0], dir)
	helpNext(res.Diagnostics, "knowledge at", []string{knowledge.CodeUnknownUnit})
	return finish(inv, res, func(w io.Writer) error {
		var b strings.Builder
		if len(res.Knowledge) == 0 && !hasError(res.Diagnostics) {
			fmt.Fprintf(&b, "场景 %s 开始时，获知者 %s 还没有已确认的知情事件\n", inv.args[0], knowerText(character))
		}
		for _, it := range res.Knowledge {
			name := ""
			if it.Name != nil {
				name = "（" + *it.Name + "）"
			}
			fmt.Fprintf(&b, "%s%s：获知于 %s\n", it.Entry, name, it.Scene)
		}
		_, err := io.WriteString(w, b.String())
		return err
	})
}

// knowledgeParamNext 把参数引起的问题（不填 path）的下一步改为 knowledge 命令用法与该作品的条目列表；已有
// knowledge.yaml 中其他条目的问题保留默认的复查命令。name 为本次运行的命令名。
func knowledgeParamNext(ds []Diagnostic, dir, ref, name string) {
	var own []Diagnostic
	var idx []int
	for i, d := range ds {
		// 已有 knowledge.yaml 中其他条目的问题（带 path）保留默认的复查命令，只改写参数引起的问题（不填 path）。
		if d.Path != "" {
			continue
		}
		if d.Code == knowledge.CodeAnchoredValue || d.Code == knowledge.CodeUnknownUnit || d.Code == knowledge.CodeUnknownEntry ||
			d.Code == knowledge.CodeUnknownCharacter || d.Code == knowledge.CodeIncompleteConfirmation || d.Code == knowledge.CodeAlreadyDecided ||
			paramField(d.Field, "id", "scene", "entry", "character", "confirmation") {
			own, idx = append(own, d), append(idx, i)
		}
	}
	// artifact.schema-violation（非法 --id 等写入产物字段的参数问题）与 artifact.duplicate-id（--id 与既有条目
	// 重复）同样由本次参数引起，next 给用法而不是单文件复查（cli-contract/next-rechecks-problem）。
	helpNext(own, name, []string{"input.invalid-value", "artifact.schema-violation", "artifact.duplicate-id", knowledge.CodeUnknownUnit, knowledge.CodeUnknownCharacter, knowledge.CodeAnchoredValue},
		shellCommand("wa-cli", "knowledge", "list", ref, "--all", "--dir", dir, "--json"))
	for i := range own {
		switch own[i].Code {
		case knowledge.CodeUnknownEntry, knowledge.CodeUnknownCharacter:
			// --entry 引用的设定条目不存在或 --character 不是 character 条目：先看设定集里有什么，再查用法。
			own[i].Next = []string{
				shellCommand("wa-cli", "canon", "list", ref, "--dir", dir, "--json"),
				"wa-cli help " + name + " --json",
			}
		case knowledge.CodeIncompleteConfirmation, knowledge.CodeAlreadyDecided:
			own[i].Next = []string{
				shellCommand("wa-cli", "knowledge", "list", ref, "--all", "--dir", dir, "--json"),
				"wa-cli help " + name + " --json",
			}
		}
	}
	for k, i := range idx {
		ds[i] = own[k]
	}
}

// knowledgeNotFoundNext 把 knowledge.not-found 的下一步改为列出该作品的全部知情事件。
func knowledgeNotFoundNext(ds []Diagnostic, ref, dir string) {
	for i := range ds {
		if ds[i].Code == knowledge.CodeNotFound {
			ds[i].Next = []string{shellCommand("wa-cli", "knowledge", "list", ref, "--all", "--dir", dir, "--json")}
		}
	}
}

// knowledgeCharacterNext 把 knowledge at 中获知者参数问题的下一步改为设定集列表与用法。
func knowledgeCharacterNext(ds []Diagnostic, ref, dir string) {
	for i := range ds {
		if ds[i].Code == knowledge.CodeUnknownCharacter {
			ds[i].Next = []string{
				shellCommand("wa-cli", "canon", "list", canonWork(ref), "--dir", dir, "--json"),
				"wa-cli help knowledge at --json",
			}
		}
	}
}
