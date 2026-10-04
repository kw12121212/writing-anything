package cli

import (
	"fmt"
	"io"
	"strings"

	"github.com/kw12121212/writing-anything/internal/canon"
	"github.com/kw12121212/writing-anything/internal/series"
	"github.com/kw12121212/writing-anything/internal/timeline"
	"github.com/kw12121212/writing-anything/internal/work"
	"github.com/kw12121212/writing-anything/internal/workspace"
)

var timelineCommand = &command{
	name:    "timeline",
	summary: "只读输出时间线（Timeline）：按场景大纲的 time（故事时间标记）与 after（相对顺序声明）推导场景全序，检查 after 成环、引用不存在与声明矛盾；不声明时按单元树先序（系列按 works 顺序）",
	args: []argSpec{
		{"ref", "单元（Unit）引用、作品 id 或系列 id", true},
	},
	flags:      []flagSpec{dirFlag},
	outputKeys: []outputKey{{"timeline", "按推导全序排列的场景数组，每项含 unit、time（未声明为 null）、after 与 position"}},
	exitCodes:  standardExitCodes,
	run:        runTimeline,
}

type timelineResult struct {
	Result
	Timeline []timeline.Entry `json:"timeline"`
}

// timelineWorks 解析引用并返回参与时间线的作品列表（系列展开为成员作品，按 works 顺序）。
func timelineWorks(dir, ref string) ([]*timeline.Work, []workspace.Problem) {
	// 先按系列解析：.writing-anything/series/<ref>/series.yaml 存在时展开为成员作品。
	if s, ps := series.Load(dir, ".writing-anything/series/"+ref+".yaml"); s != nil {
		var out []*timeline.Work
		for _, id := range s.Works {
			w, _, wps := work.Resolve(dir, id)
			if w == nil || len(wps) > 0 {
				continue
			}
			out = append(out, &timeline.Work{W: w, SeriesID: s.ID})
		}
		return out, nil
	} else if ps != nil {
		// 系列文件存在但不合法：按其诊断报告；不存在（常见情形）继续按作品解析。
		if _, ok, _ := workspace.ReadRegular(dir, ".writing-anything/series/"+ref+".yaml"); ok {
			return nil, ps
		}
	}
	w, _, ps := work.Resolve(dir, ref)
	if w == nil {
		return nil, ps
	}
	tw := &timeline.Work{W: w}
	if _, s, _ := series.ForWork(dir, w); s != nil {
		tw.SeriesID = s.ID
	}
	return []*timeline.Work{tw}, nil
}

func runTimeline(inv *invocation) int {
	dir := projectDir(inv)
	res := &timelineResult{Timeline: []timeline.Entry{}}
	problems := workspace.Require(dir)
	if problems == nil {
		ws, ps := timelineWorks(dir, inv.args[0])
		if len(ps) > 0 {
			problems = ps
		} else if len(ws) > 0 {
			ordered, ops := timeline.Order(dir, ws)
			problems = ops
			// 同 time 双出场以解析后的设定集为词典（无 canon 时按称谓原文）。
			var entries map[string]canon.Entry
			if scoped, sps := canon.Resolve(dir, ws[0].W); sps == nil {
				entries = map[string]canon.Entry{}
				for _, se := range scoped {
					entries[se.ID] = se.Entry
				}
			}
			problems = append(problems, timeline.SameTimePresence(ordered, dir, ws, entries)...)
			res.Timeline = ordered
		}
	}
	res.Diagnostics = diagnostics(problems, dir)
	for i := range res.Diagnostics {
		if strings.HasPrefix(res.Diagnostics[i].Code, "timeline.") {
			res.Diagnostics[i].Next = []string{"wa-cli help scene contract --json", shellCommand("wa-cli", "timeline", inv.args[0], "--dir", dir, "--json")}
		}
	}
	return finish(inv, res, func(w io.Writer) error {
		if len(res.Timeline) == 0 {
			fmt.Fprintf(w, "没有场景可输出时间线\n")
			return nil
		}
		for _, e := range res.Timeline {
			at := "（未声明时间）"
			if e.Time != nil {
				at = "（" + *e.Time + "）"
			}
			after := ""
			if len(e.After) > 0 {
				after = "，晚于 " + strings.Join(e.After, "、")
			}
			fmt.Fprintf(w, "%d. %s%s%s\n", e.Position, e.Unit, at, after)
		}
		return nil
	})
}
