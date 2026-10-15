package cli

import (
	"fmt"
	"io"
	"strings"

	"github.com/kw12121212/writing-anything/internal/resolve"
	"github.com/kw12121212/writing-anything/internal/reviewpacket"
	"github.com/kw12121212/writing-anything/internal/series"
	"github.com/kw12121212/writing-anything/internal/work"
	"github.com/kw12121212/writing-anything/internal/workspace"
)

var reviewPlanCommand = &command{
	name:    "review plan",
	summary: "按维度生成审查任务包（Review Task Packet）：人物一致性、伏笔回收质量、因果与动机、视角与揭示边界、文风与情绪节奏；每个任务包只含一段正文切片、相关台账切片与检查清单，不超过上下文预算，由接入的 Agent 调度执行",
	args: []argSpec{
		{"unit", "单元（Unit）引用或作品 id：给单元时只生成其范围内的任务包", true},
	},
	flags: []flagSpec{
		dirFlag,
		{"dimension", flagString, "只生成该维度（character、thread、causality、pov-reveal、style）；省略时生成全部"},
	},
	outputKeys: []outputKey{{"packets", "生成的任务包数组，每项含 id、dimension、scope、chars、truncated 与 omittedSlices；按维度与范围顺序；chars 不超过上下文预算，放不下时省略低相关切片或按清单项拆包"}},
	exitCodes:  standardExitCodes,
	run:        runReviewPlan,
}

var reviewSubmitCommand = &command{
	name:    "review submit",
	summary: "把审查任务包（Review Task Packet）的结论写回任务包：每个清单项都必须有且仅有一个结论（pass、fail 或 na），齐全后任务包标记为 done",
	args: []argSpec{
		{"packet", "任务包引用：<work>/<packet-id>（packets/<work>/ 下的文件名去扩展名）", true},
	},
	flags: []flagSpec{
		dirFlag,
		{"file", flagString, "结论输入文件（YAML：verdicts 数组）；省略时从标准输入读取"},
	},
	outputKeys: []outputKey{{"packet", "提交后的任务包：id、dimension、scope、status 与 results；失败时为 null"}, {"fails", "verdict 为 fail 的结论数"}},
	exitCodes:  standardExitCodes,
	run:        runReviewSubmit,
}

var reviewReportCommand = &command{
	name:    "review report",
	summary: "只读汇总作品全部审查任务包（Review Task Packet）：每个维度的 open 与 done 包数、fail 结论清单与就绪结论；任务由接入的 Agent 执行，本命令只汇总",
	args: []argSpec{
		{"work", "作品 id", true},
	},
	flags:      []flagSpec{dirFlag},
	outputKeys: []outputKey{{"dimensions", "每个维度的 open 与 done 包数"}, {"failures", "done 包中 verdict 为 fail 的结论清单"}, {"ready", "全部包 done 且无 fail 时为 true"}},
	exitCodes:  standardExitCodes,
	run:        runReviewReport,
}

type reviewPlanResult struct {
	Result
	Packets []reviewpacket.Packet `json:"packets"`
}

type reviewSubmitResult struct {
	Result
	Packet *struct {
		reviewpacket.Packet
	} `json:"packet"`
	Fails int `json:"fails"`
}

type reviewReportResult struct {
	Result
	Dimensions []reviewpacket.DimensionStat `json:"dimensions"`
	Failures   []reviewpacket.Failure       `json:"failures"`
	Ready      bool                         `json:"ready"`
}

func runReviewPlan(inv *invocation) int {
	dir := projectDir(inv)
	res := &reviewPlanResult{Packets: []reviewpacket.Packet{}}
	problems := workspace.Require(dir)
	if problems == nil {
		// 系列引用：展开为成员作品的系列级任务包（series-continuity-review 主规格）。
		if ws := seriesWorksOf(dir, inv.args[0]); len(ws) > 1 {
			var pk []reviewpacket.Packet
			var ch []string
			if pk, ch, problems = reviewpacket.PlanSeries(dir, inv.args[0], ws, inv.values["dimension"], budgetOf(dir, ws[0]), now()); pk != nil {
				res.Packets, res.ChangedPaths = pk, ch
			}
			res.Diagnostics = diagnostics(problems, dir)
			packetsNext(res.Diagnostics, inv.args[0], dir)
			return finish(inv, res, nil)
		}
		var w *work.Work
		var u *work.Unit
		if w, u, problems = work.Resolve(dir, inv.args[0]); w != nil {
			budget := resolve.DefaultContextBudget
			if r, _, warnings := series.ForWork(dir, w); r != nil {
				if v, ok := r.ContextBudget.Value.(int); ok && v > 0 {
					budget = v
				}
				problems = warnings
			}
			var pk []reviewpacket.Packet
			var changed []string
			if pk, changed, problems = reviewpacket.Plan(dir, w, u, inv.values["dimension"], budget, now()); pk != nil {
				res.Packets, res.ChangedPaths = pk, changed
			}
		}
	}
	res.Diagnostics = diagnostics(problems, dir)
	packetsNext(res.Diagnostics, inv.args[0], dir)
	return finish(inv, res, func(w io.Writer) error {
		if len(res.Packets) == 0 {
			if len(problems) == 0 {
				fmt.Fprintf(w, "没有生成任何任务包（没有可审查的内容或维度为空）\n")
			}
			return nil
		}
		var b strings.Builder
		dim := ""
		for _, p := range res.Packets {
			if p.Dimension != dim {
				dim = p.Dimension
				fmt.Fprintf(&b, "%s：\n", dim)
			}
			trunc := ""
			if p.Truncated {
				trunc = "（正文已截断）"
			}
			if p.OmittedSlices > 0 {
				trunc += fmt.Sprintf("（按预算省略 %d 条切片）", p.OmittedSlices)
			}
			fmt.Fprintf(&b, "  %s：范围 %s，%d 字符%s\n", p.ID, p.Scope, p.Chars, trunc)
		}
		_, err := io.WriteString(w, b.String())
		return err
	})
}

func runReviewSubmit(inv *invocation) int {
	dir := projectDir(inv)
	res := &reviewSubmitResult{}
	problems := workspace.Require(dir)
	if problems == nil {
		ref := inv.args[0]
		workID, id, found := strings.Cut(ref, "/")
		if !found || id == "" {
			problems = []workspace.Problem{{Code: reviewpacket.CodeNotFound, Severity: workspace.SeverityError,
				Message: "任务包引用应为 <work>/<packet-id>，如 my-novel/character-1"}}
		} else {
			var w *work.Work
			if w, _, problems = work.Resolve(dir, workID); w != nil {
				var data []byte
				if data, _, problems = readInput(inv, dir); len(problems) == 0 {
					var in reviewpacket.SubmitInput
					if in, problems = reviewpacket.ParseInput(data); len(problems) == 0 {
						f, changed, ps := reviewpacket.Submit(dir, w, id, in, now())
						problems = ps
						if f != nil {
							pkt := reviewpacket.ViewOf(f)
							res.Packet = &struct{ reviewpacket.Packet }{pkt}
							for _, r := range f.Results {
								if r.Verdict == "fail" {
									res.Fails++
								}
							}
							res.ChangedPaths = changed
						}
					}
				}
			}
		}
	}
	res.Diagnostics = diagnostics(problems, dir)
	packetsNext(res.Diagnostics, inv.args[0], dir)
	return finish(inv, res, func(w io.Writer) error {
		if res.Packet == nil {
			return nil
		}
		_, err := fmt.Fprintf(w, "已提交任务包 %s（%s，范围 %s）：%d 项结论，%d 项 fail\n",
			res.Packet.ID, res.Packet.Dimension, res.Packet.Scope, len(res.Packet.Results), res.Fails)
		return err
	})
}

func runReviewReport(inv *invocation) int {
	dir := projectDir(inv)
	res := &reviewReportResult{Dimensions: []reviewpacket.DimensionStat{}, Failures: []reviewpacket.Failure{}}
	problems := workspace.Require(dir)
	if problems == nil {
		var w *work.Work
		if w, _, problems = work.Resolve(dir, inv.args[0]); w != nil {
			var ready bool
			res.Dimensions, res.Failures, ready, problems = reviewpacket.Report(dir, w)
			res.Ready = ready
		}
	}
	res.Diagnostics = diagnostics(problems, dir)
	packetsNext(res.Diagnostics, inv.args[0], dir)
	return finish(inv, res, func(w io.Writer) error {
		var b strings.Builder
		for _, st := range res.Dimensions {
			fmt.Fprintf(&b, "%s：open %d，done %d\n", st.Dimension, st.Open, st.Done)
		}
		for _, f := range res.Failures {
			fmt.Fprintf(&b, "fail：%s（%s）%s：%s\n", f.Packet, f.Scope, f.Item, f.Note)
		}
		state := "未就绪"
		if res.Ready {
			state = "全部完成且无 fail"
		}
		total := 0
		for _, st := range res.Dimensions {
			total += st.Open + st.Done
		}
		if total == 0 {
			state = "还没有生成任何任务包（先运行 wa-cli review plan）"
		}
		fmt.Fprintf(&b, "审查任务包：%s\n", state)
		_, err := io.WriteString(w, b.String())
		return err
	})
}

// seriesWorksOf 返回系列引用的成员作品（不是系列或成员不足时为空）。
func seriesWorksOf(dir, ref string) []*work.Work {
	s, _ := series.Load(dir, ".writing-anything/series/"+ref+".yaml")
	if s == nil {
		return nil
	}
	var out []*work.Work
	for _, id := range s.Works {
		if w, _, ps := work.Resolve(dir, id); w != nil && len(ps) == 0 {
			out = append(out, w)
		}
	}
	return out
}

// packetsNext 为任务包诊断给出下一步命令。
func packetsNext(ds []Diagnostic, ref, dir string) {
	for i, d := range ds {
		if d.Path != "" && d.Code != reviewpacket.CodeInvalid {
			continue
		}
		switch d.Code {
		case reviewpacket.CodeNotFound:
			planRef := ref
			if w, _, found := strings.Cut(ref, "/"); found {
				planRef = w
			}
			ds[i].Next = []string{"wa-cli help review plan --json",
				shellCommand("wa-cli", "review", "plan", planRef, "--dir", dir, "--json")}
		case reviewpacket.CodeMissingVerdict, reviewpacket.CodeUnknownItem, reviewpacket.CodeAlreadyDone:
			ds[i].Next = []string{"wa-cli help review submit --json"}
		case "input.invalid-value":
			ds[i].Next = []string{"wa-cli help review plan --json"}
		case reviewpacket.CodeOverBudget:
			ds[i].Next = overBudgetNext(dir)
		}
	}
}

// overBudgetNext 是 packet.over-budget 的下一步命令：查看当前上下文预算，再按 message 给出的最小预算调整。
func overBudgetNext(dir string) []string {
	return []string{shellCommand("wa-cli", "config", "resolved", "--dir", dir, "--json"), "wa-cli help config set --json"}
}
