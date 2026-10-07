package cli

import (
	"fmt"
	"io"
	"strings"

	"github.com/kw12121212/writing-anything/internal/impact"
	"github.com/kw12121212/writing-anything/internal/resolve"
	"github.com/kw12121212/writing-anything/internal/reviewpacket"
	"github.com/kw12121212/writing-anything/internal/series"
	"github.com/kw12121212/writing-anything/internal/work"
	"github.com/kw12121212/writing-anything/internal/workspace"
)

var impactCommand = &command{
	name:    "impact",
	summary: "只读预览影响传播（Ripple Analysis）：目标（场景、设定条目、事件或伏笔）被修改时，按磁盘引用图列出受影响的后续场景与引用依据",
	args: []argSpec{
		{"target", "被修改的目标：场景单元（Unit）引用、设定条目 id、事件 id 或伏笔 id", true},
	},
	flags:      []flagSpec{dirFlag},
	outputKeys: []outputKey{{"affected", "受影响后续场景数组，每项含 scene、via 与 reason，按先序排列"}},
	exitCodes:  standardExitCodes,
	run:        runImpactPreview,
}

var impactMarkCommand = &command{
	name:    "impact mark",
	summary: "把影响传播（Ripple Analysis）的范围落盘为复查标记（Recheck Mark）：受影响后续场景追加写入 rechecks/<work>.yaml，并为每个场景生成 causality 维度的审查任务包",
	args: []argSpec{
		{"target", "被修改的目标：场景单元引用、设定条目 id、事件 id 或伏笔 id", true},
	},
	flags:      []flagSpec{dirFlag},
	outputKeys: []outputKey{{"marked", "本次新增的复查标记数组，每项含 scene、target、via、reason 与 marked_at；无受影响场景时为空数组"}},
	exitCodes:  standardExitCodes,
	run:        runImpactMark,
}

var impactClearCommand = &command{
	name:    "impact clear",
	summary: "解除复查标记（Recheck Mark）：作者确认无影响（--confirmed-by-author 与 --confirmation），或该场景已重新审查通过（审稿基于当前正文且无 fail 结论，此时可省略确认）",
	args: []argSpec{
		{"scene", "场景单元（Unit）引用", true},
		{"target", "标记的目标", true},
	},
	flags: []flagSpec{
		dirFlag,
		{"confirmed-by-author", flagBool, "作者确认本次修改无影响"},
		{"confirmation", flagString, "作者确认原话摘要"},
	},
	outputKeys: []outputKey{{"cleared", "解除后的复查标记；失败时为 null"}},
	exitCodes:  standardExitCodes,
	run:        runImpactClear,
}

var impactListCommand = &command{
	name:    "impact list",
	summary: "列出作品的复查标记（Recheck Mark）：默认只列 open（--all 列全部，含已解除与依据）",
	args:    []argSpec{{"work", "作品 id", true}},
	flags: []flagSpec{
		dirFlag,
		{"all", flagBool, "列出全部标记（含已解除）"},
	},
	outputKeys: []outputKey{{"marks", "复查标记数组，每项含 scene、target、via、reason、marked_at、status 与解除依据"}},
	exitCodes:  standardExitCodes,
	run:        runImpactList,
}

type impactResult struct {
	Result
	Affected []impact.Affected `json:"affected"`
}

type impactMarkResult struct {
	Result
	Marked []impact.MarkView `json:"marked"`
}

type impactClearResult struct {
	Result
	Cleared *impact.MarkView `json:"cleared"`
}

type impactListResult struct {
	Result
	Marks []impact.MarkView `json:"marks"`
}

// resolveImpactWork 从目标解析所属作品：单元引用直接解析；条目/事件/伏笔 id 需要给出所在作品——约定 id 形
// 目标不带作品前缀时无法定位作品，impact 系列只接受单元引用或 <work>/<id> 形式的目标。
func impactWork(inv *invocation, dir string) (*work.Work, *work.Unit, string, []workspace.Problem) {
	ref := inv.args[0]
	if w, u, ps := work.Resolve(dir, ref); u != nil {
		return w, u, u.Ref, ps
	}
	if workID, _, found := strings.Cut(ref, "/"); found {
		if w, _, ps := work.Resolve(dir, workID); w != nil {
			return w, nil, strings.TrimPrefix(ref, workID+"/"), ps
		} else if len(ps) > 0 {
			return nil, nil, "", ps
		}
	}
	return nil, nil, "", []workspace.Problem{{Code: impact.CodeUnknownTarget, Severity: workspace.SeverityError, Field: "target",
		Message: "目标 " + ref + " 无法解析：给出场景单元引用（如 my-novel/scene-1）或 <work>/<id> 形式的条目、事件或伏笔引用"}}
}

func budgetOf(dir string, w *work.Work) int {
	if r, _, _ := series.ForWork(dir, w); r != nil {
		if v, ok := r.ContextBudget.Value.(int); ok && v > 0 {
			return v
		}
	}
	return resolve.DefaultContextBudget
}

func runImpactPreview(inv *invocation) int {
	dir := projectDir(inv)
	res := &impactResult{Affected: []impact.Affected{}}
	problems := workspace.Require(dir)
	if problems == nil {
		if w, _, target, ps := impactWork(inv, dir); w != nil || len(ps) > 0 {
			if len(ps) > 0 {
				problems = ps
			} else {
				var affected []impact.Affected
				if affected, problems = impact.Compute(dir, w, target); affected != nil {
					res.Affected = affected
				}
			}
		}
	}
	res.Diagnostics = diagnostics(problems, dir)
	impactNext(res.Diagnostics, dir)
	return finish(inv, res, func(w io.Writer) error {
		if len(res.Affected) == 0 {
			fmt.Fprintf(w, "没有受影响的后续场景\n")
			return nil
		}
		for _, a := range res.Affected {
			fmt.Fprintf(w, "%s（%s）：%s\n", a.Scene, a.Via, a.Reason)
		}
		return nil
	})
}

func runImpactMark(inv *invocation) int {
	dir := projectDir(inv)
	res := &impactMarkResult{Marked: []impact.MarkView{}}
	problems := workspace.Require(dir)
	if problems == nil {
		if w, _, target, ps := impactWork(inv, dir); w != nil || len(ps) > 0 {
			if len(ps) > 0 {
				problems = ps
			} else {
				var marked []impact.FileMark
				var changed []string
				if marked, changed, problems = impact.MarkTarget(dir, w, target, budgetOf(dir, w), now()); marked != nil {
					res.Marked, res.ChangedPaths = impact.ViewAll(marked), changed
				} else if problems == nil {
					res.Marked, res.ChangedPaths = []impact.MarkView{}, []string{}
				}
			}
		}
	}
	res.Diagnostics = diagnostics(problems, dir)
	impactNext(res.Diagnostics, dir)
	return finish(inv, res, func(w io.Writer) error {
		if len(res.Marked) == 0 {
			fmt.Fprintf(w, "没有受影响的后续场景，未写入复查标记\n")
			return nil
		}
		for _, m := range res.Marked {
			fmt.Fprintf(w, "已标记 %s（%s）：%s\n", m.Scene, m.Target, m.Reason)
		}
		return nil
	})
}

func runImpactClear(inv *invocation) int {
	dir := projectDir(inv)
	res := &impactClearResult{}
	problems := workspace.Require(dir)
	if problems == nil {
		var w *work.Work
		if w, _, problems = work.Resolve(dir, inv.args[0]); w != nil {
			confirmation := ""
			if inv.flag("confirmed-by-author") {
				confirmation = inv.values["confirmation"]
				if strings.TrimSpace(confirmation) == "" {
					problems = []workspace.Problem{{Code: impact.CodeIncompleteConfirmation, Severity: workspace.SeverityError, Field: "confirmation",
						Message: "作者确认必须给出 --confirmed-by-author 与非空白的 --confirmation（作者原话摘要）"}}
				}
			}
			if problems == nil {
				var m *impact.FileMark
				var changed []string
				if m, changed, problems = impact.Clear(dir, w, inv.args[0], inv.args[1], confirmation, now()); m != nil {
					v := impact.ViewOf(*m)
					res.Cleared, res.ChangedPaths = &v, changed
				}
			}
		}
	}
	res.Diagnostics = diagnostics(problems, dir)
	impactNext(res.Diagnostics, dir)
	return finish(inv, res, func(w io.Writer) error {
		if res.Cleared == nil {
			return nil
		}
		_, err := fmt.Fprintf(w, "已解除 %s 对 %s 的复查标记\n", res.Cleared.Scene, res.Cleared.Target)
		return err
	})
}

func runImpactList(inv *invocation) int {
	dir := projectDir(inv)
	res := &impactListResult{Marks: []impact.MarkView{}}
	problems := workspace.Require(dir)
	if problems == nil {
		var w *work.Work
		if w, _, problems = work.Resolve(dir, inv.args[0]); w != nil {
			if marks, ps := impact.List(dir, w.ID, inv.flag("all")); ps != nil {
				problems = ps
			} else {
				res.Marks = impact.ViewAll(marks)
			}
		}
	}
	res.Diagnostics = diagnostics(problems, dir)
	return finish(inv, res, func(w io.Writer) error {
		if len(res.Marks) == 0 {
			fmt.Fprintf(w, "没有复查标记\n")
			return nil
		}
		for _, m := range res.Marks {
			fmt.Fprintf(w, "%s → %s（%s，%s）：%s\n", m.Scene, m.Target, m.Via, m.Status, m.Reason)
		}
		return nil
	})
}

// impactNext 为影响传播诊断给出下一步命令。
func impactNext(ds []Diagnostic, dir string) {
	for i, d := range ds {
		if d.Path != "" {
			continue
		}
		switch d.Code {
		case impact.CodeUnknownTarget:
			ds[i].Next = []string{"wa-cli help impact --json", shellCommand("wa-cli", "status", "--dir", dir, "--json")}
		case impact.CodeIncompleteConfirmation:
			ds[i].Next = []string{"wa-cli help impact clear --json"}
		case impact.CodeNotFound:
			ds[i].Next = []string{"wa-cli help impact mark --json", shellCommand("wa-cli", "impact", "list", "--dir", dir, "--json")}
		case reviewpacket.CodeOverBudget:
			ds[i].Next = overBudgetNext(dir)
		}
	}
}
