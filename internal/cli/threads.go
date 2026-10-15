package cli

import (
	"fmt"
	"io"
	"strings"

	"github.com/kw12121212/writing-anything/internal/impact"
	"github.com/kw12121212/writing-anything/internal/threads"
	"github.com/kw12121212/writing-anything/internal/work"
	"github.com/kw12121212/writing-anything/internal/workspace"
)

var threadsListCommand = &command{
	name:    "threads",
	summary: "列出作品（Work）伏笔台账（Foreshadowing Ledger）中未回收的伏笔条目（Thread Entry），标出已超过计划回收位置的条目；--all 列出全部",
	args:    []argSpec{workArg},
	flags: []flagSpec{
		dirFlag,
		{"all", flagBool, "列出全部条目，不只列未回收（open）的"},
	},
	outputKeys: []outputKey{{"entries", "按埋设场景的单元树先序排列的条目数组，每项含 work、id、text、planted_in、planned_recover、status、recovered_in、decision 与 overdue"}},
	exitCodes:  standardExitCodes,
	run:        runThreadsList,
}

var threadsAddCommand = &command{
	name:    "threads add",
	summary: "在作品（Work）的伏笔台账（Foreshadowing Ledger）中登记一个伏笔条目（Thread Entry）：内容、埋设场景与计划回收位置，初始状态为未回收（open）",
	args:    []argSpec{workArg},
	flags: []flagSpec{
		dirFlag,
		{"text", flagString, "伏笔内容"},
		{"plant-in", flagString, "埋设场景；本作品的 scene 类型单元引用"},
		{"recover-at", flagString, "计划回收位置；本作品的 chapter 或 scene 类型单元引用"},
		{"id", flagString, "条目 id；省略时为 thread-<n>"},
	},
	outputKeys: []outputKey{{"thread", "新条目：work、id、text、planted_in、planned_recover、status、recovered_in 与 decision；失败时为 null"}},
	exitCodes:  standardExitCodes,
	run:        runThreadsAdd,
}

var threadsSetCommand = &command{
	name:    "threads set",
	summary: "修改伏笔台账（Foreshadowing Ledger）中一个伏笔条目（Thread Entry），保留其他内容与注释；放弃（--status dropped）需要作者确认",
	args:    []argSpec{workArg, {"entry", "伏笔条目 id", true}},
	flags: []flagSpec{
		dirFlag,
		{"text", flagString, "伏笔内容；省略时不修改"},
		{"recover-at", flagString, "计划回收位置；整体替换，须仍不早于埋设场景"},
		{"status", flagString, "状态：open（未回收）、paid（已回收）或 dropped（放弃）；放弃须经决策队列（Decision Queue）作者确认"},
		{"recovered-in", flagString, "回收发生的场景；--status paid 必填，指向 scene 单元；给出空值时清除"},
		{"decision", flagString, "放弃条目的授权：指向 wa-cli decide 已决定且选择为允许的决策 id"},
	},
	outputKeys: []outputKey{{"thread", "修改后的条目：work、id、text、planted_in、planned_recover、status、recovered_in 与 decision；失败时为 null"}, impactOutputKey},
	exitCodes:  standardExitCodes,
	run:        runThreadsSet,
}

type threadEntryView struct {
	Work string `json:"work"`
	threads.Item
}

type threadsAddResult struct {
	Result
	Thread *threadEntryView `json:"thread"`
}

type threadsListResult struct {
	Result
	Entries []threadEntryView `json:"entries"`
}

// threadOverdueText 返回条目的人读标注；未超过计划回收位置时为空。
func threadOverdueText(t threadEntryView) string {
	if !t.Overdue {
		return ""
	}
	return "，已超过计划回收位置（下一步：wa-cli threads set " + t.Work + " " + t.ID + " --status paid --recovered-in <场景>，或 --status dropped）"
}

// resolveThreadsWork 按作品引用加载 dir 下的作品；伏笔台账是作品级产物，子单元引用不可用（提示用 threads 的说法）。
func resolveThreadsWork(dir, ref string) (*work.Work, []workspace.Problem) {
	w, u, ps := work.Resolve(dir, ref)
	if u != nil && u.Parent != nil {
		return nil, []workspace.Problem{{Code: work.CodeNotFound, Severity: workspace.SeverityError, Path: w.Path,
			Message: "伏笔台账（Foreshadowing Ledger）是作品级产物，请使用作品 id " + w.ID + "，不要带子单元"}}
	}
	return w, ps
}

func runThreadsList(inv *invocation) int {
	dir := projectDir(inv)
	res := &threadsListResult{Entries: []threadEntryView{}}
	problems := workspace.Require(dir)
	if problems == nil {
		var w *work.Work
		if w, problems = resolveThreadsWork(dir, inv.args[0]); w != nil {
			var items []threads.Item
			items, problems = threads.List(dir, w, inv.flag("all"))
			for _, it := range items {
				res.Entries = append(res.Entries, threadEntryView{Work: w.ID, Item: it})
			}
		}
	}
	res.Diagnostics = diagnostics(problems, dir)
	return finish(inv, res, func(w io.Writer) error {
		var b strings.Builder
		if len(res.Entries) == 0 && !hasError(res.Diagnostics) {
			if inv.flag("all") {
				fmt.Fprintf(&b, "作品 %s 还没有伏笔条目（Thread Entry）\n", inv.args[0])
			} else {
				fmt.Fprintf(&b, "作品 %s 没有未回收的伏笔（Open Foreshadowing）\n", inv.args[0])
			}
		}
		for _, t := range res.Entries {
			fmt.Fprintf(&b, "%s（%s → %s%s）%s：%s\n", t.ID, t.PlantedIn, t.PlannedRecover, threadOverdueText(t), statusText(t.Status), t.Text)
		}
		_, err := io.WriteString(w, b.String())
		return err
	})
}

func statusText(status string) string {
	switch status {
	case threads.StatusPaid:
		return "［已回收］"
	case threads.StatusDropped:
		return "［已放弃］"
	}
	return ""
}

func runThreadsAdd(inv *invocation) int {
	dir := projectDir(inv)
	res := &threadsAddResult{}
	problems := workspace.Require(dir)
	if problems == nil {
		var w *work.Work
		if w, problems = resolveThreadsWork(dir, inv.args[0]); w != nil {
			var e *threads.Entry
			var changed []string
			in := threads.AddInput{Text: inv.values["text"], PlantIn: inv.values["plant-in"], RecoverAt: inv.values["recover-at"], ID: inv.values["id"]}
			if e, changed, problems = threads.Add(dir, w, in); e != nil {
				res.Thread, res.ChangedPaths = &threadEntryView{Work: w.ID, Item: threads.Item{Entry: *e}}, changed
			}
		}
	}
	res.Diagnostics = diagnostics(problems, dir)
	threadsParamNext(res.Diagnostics, dir, inv.args[0], "threads add")
	return finish(inv, res, func(w io.Writer) error {
		if res.Thread == nil {
			return nil
		}
		_, err := fmt.Fprintf(w, "已在作品 %s 登记伏笔条目（Thread Entry）%s（%s → %s）：%s\n",
			res.Thread.Work, res.Thread.ID, res.Thread.PlantedIn, res.Thread.PlannedRecover, res.Thread.Text)
		return err
	})
}

type threadsSetResult struct {
	Result
	Thread *threadEntryView `json:"thread"`
	Impact []impact.Ripple  `json:"impact"`
}

// threadSemantic 返回伏笔条目的语义比较键（伏笔计划与回收位置：text、planned_recover、status 与 recovered_in）。
func threadSemantic(e *threads.Entry) string {
	if e == nil {
		return ""
	}
	recovered := ""
	if e.RecoveredIn != nil {
		recovered = *e.RecoveredIn
	}
	return impact.SemanticKey(e.Text, e.PlannedRecover, e.Status, recovered)
}

func runThreadsSet(inv *invocation) int {
	dir := projectDir(inv)
	res := &threadsSetResult{Impact: []impact.Ripple{}}
	var auto []Diagnostic
	problems := workspace.Require(dir)
	if problems == nil {
		var w *work.Work
		if w, problems = resolveThreadsWork(dir, inv.args[0]); w != nil {
			var in threads.SetInput
			if v, ok := inv.values["text"]; ok {
				in.Text = &v
			}
			if v, ok := inv.values["recover-at"]; ok {
				in.RecoverAt = &v
			}
			if v, ok := inv.values["status"]; ok {
				in.Status = &v
			}
			if v, ok := inv.values["recovered-in"]; ok {
				in.RecoveredIn = &v
			}
			in.Decision = inv.values["decision"]
			var before *threads.Entry
			if items, _ := threads.List(dir, w, true); items != nil {
				for i := range items {
					if items[i].ID == inv.args[1] {
						before = &items[i].Entry
					}
				}
			}
			var e *threads.Entry
			var changed []string
			pre := preAffected(dir, w, inv.args[1])
			if e, changed, problems = threads.Set(dir, w, inv.args[1], in); e != nil {
				res.Thread, res.ChangedPaths = &threadEntryView{Work: w.ID, Item: threads.Item{Entry: *e}}, changed
				if len(changed) > 0 && threadSemantic(before) != threadSemantic(e) {
					var more []string
					res.Impact, more, auto = autoImpact(dir, w, []impactTarget{{target: e.ID, pre: pre}})
					res.ChangedPaths = append(res.ChangedPaths, more...)
				}
			} else if changed != nil {
				// 拒绝放弃条目时 changed 为追加的决策队列路径，thread 为 null。
				res.ChangedPaths = changed
			}
		}
	}
	res.Diagnostics = diagnostics(problems, dir)
	threadsParamNext(res.Diagnostics, dir, inv.args[0], "threads set")
	threadsAuthorNext(res.Diagnostics, dir)
	threadsNotFoundNext(res.Diagnostics, inv.args[0], dir)
	res.Diagnostics = append(res.Diagnostics, auto...)
	return finish(inv, res, func(w io.Writer) error {
		if res.Thread == nil {
			return nil
		}
		verb := "已修改"
		if len(res.ChangedPaths) == 0 {
			verb = "取值未变，未修改"
		}
		_, err := fmt.Fprintf(w, "%s作品 %s 的伏笔条目（Thread Entry）%s（%s → %s%s）：%s\n%s",
			verb, res.Thread.Work, res.Thread.ID, res.Thread.PlantedIn, res.Thread.PlannedRecover, statusText(res.Thread.Status), res.Thread.Text, impactText(res.Impact))
		return err
	})
}

// threadsParamNext 把参数引起的问题（不填 path）的下一步改为 threads 命令用法与该作品的条目列表；已有 threads.yaml
// 中的问题保留默认的复查命令。name 为本次运行的命令名（threads、threads add 或 threads set）。
func threadsParamNext(ds []Diagnostic, dir, ref, name string) {
	var own []Diagnostic
	var idx []int
	for i, d := range ds {
		// 已有 threads.yaml 中其他条目的问题（带 path）保留默认的复查命令，只改写参数引起的问题（不填 path）。
		if d.Path != "" {
			continue
		}
		if d.Code == threads.CodeAnchoredValue || d.Code == threads.CodeUnknownUnit || d.Code == threads.CodeRecoverBeforePlant ||
			paramField(d.Field, "id", "text", "plant-in", "recover-at", "status", "recovered-in", "decision") {
			own, idx = append(own, d), append(idx, i)
		}
	}
	helpNext(own, name, []string{"input.invalid-value", threads.CodeUnknownUnit, threads.CodeRecoverBeforePlant, threads.CodeAnchoredValue},
		shellCommand("wa-cli", "threads", ref, "--dir", dir, "--json"))
	for k, i := range idx {
		ds[i] = own[k]
	}
}

// threadsAuthorNext 把 threads.author-confirmation 的下一步改为查看决策队列与 threads set 的用法。
func threadsAuthorNext(ds []Diagnostic, dir string) {
	for i := range ds {
		if ds[i].Code == threads.CodeAuthorConfirmation {
			ds[i].Next = []string{
				shellCommand("wa-cli", "decisions", "--dir", dir, "--json"),
				"wa-cli help threads set --json",
			}
		}
	}
}

// threadsNotFoundNext 把 threads.not-found 的下一步改为列出该作品的伏笔条目。
func threadsNotFoundNext(ds []Diagnostic, ref, dir string) {
	for i := range ds {
		if ds[i].Code == threads.CodeNotFound {
			ds[i].Next = []string{shellCommand("wa-cli", "threads", ref, "--dir", dir, "--json")}
		}
	}
}
