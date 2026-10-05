package cli

import (
	"fmt"
	"io"
	"strings"

	"github.com/kw12121212/writing-anything/internal/continuity"
	"github.com/kw12121212/writing-anything/internal/series"
	"github.com/kw12121212/writing-anything/internal/work"
	"github.com/kw12121212/writing-anything/internal/workspace"
)

var continuityCommand = &command{
	name:    "continuity",
	summary: "确定性连续性审查（Continuity Review）：在全书范围对场景执行不依赖模型的检查——未建卡出场人物、保留词终态人物出场（死亡、失踪等）、同场景多名一卡、悬空引用与伏笔超期；发现即阻塞验收，作者经决策队列豁免",
	args: []argSpec{
		{"unit", "单元（Unit）引用：scene 单元只查该场景，其他单元或作品查其子树全部场景", true},
	},
	flags:      []flagSpec{dirFlag},
	outputKeys: []outputKey{{"findings", "按场景先序排列的发现项数组，每项含 scene、code、severity、waived 与 message；没有发现时为空数组"}},
	exitCodes:  standardExitCodes,
	run:        runContinuity,
}

type continuityResult struct {
	Result
	Findings []continuity.Finding `json:"findings"`
}

func runContinuity(inv *invocation) int {
	dir := projectDir(inv)
	res := &continuityResult{Findings: []continuity.Finding{}}
	problems := workspace.Require(dir)
	if problems == nil {
		// 系列引用：展开为成员作品的跨作品连续性检查（series-continuity-review 主规格）。
		if ws, sps := continuityWorks(dir, inv.args[0]); len(ws) > 0 || (sps != nil && len(sps) > 0) {
			if len(sps) > 0 {
				problems = sps
			} else {
				var findings []continuity.Finding
				if findings, problems = continuity.RunSeries(dir, ws); findings != nil {
					res.Findings = findings
				}
			}
		} else {
			var w *work.Work
			var u *work.Unit
			if w, u, problems = work.Resolve(dir, inv.args[0]); u != nil {
				var findings []continuity.Finding
				if findings, problems = continuity.Run(dir, w, u); findings != nil {
					res.Findings = findings
				}
			}
		}
	}
	res.Diagnostics = diagnostics(problems, dir)
	continuityNext(res.Diagnostics, inv.args[0], dir)
	return finish(inv, res, func(w io.Writer) error {
		if len(res.Findings) == 0 && !hasError(res.Diagnostics) {
			fmt.Fprintf(w, "%s 没有连续性发现\n", inv.args[0])
			return nil
		}
		var b strings.Builder
		scene := ""
		for _, f := range res.Findings {
			if f.Scene != scene {
				scene = f.Scene
				fmt.Fprintf(&b, "场景 %s：\n", scene)
			}
			waived := ""
			if f.Waived {
				waived = "［已豁免］"
			}
			fmt.Fprintf(&b, "  [%s] %s%s：%s\n", f.Severity, f.Code, waived, f.Message)
		}
		_, err := io.WriteString(w, b.String())
		return err
	})
}

// continuityWorks 把系列引用展开为成员作品（无法解析为系列时返回空，由调用方按单元/作品解析）。
func continuityWorks(dir, ref string) ([]*work.Work, []workspace.Problem) {
	rel := ".writing-anything/series/" + ref + ".yaml"
	if _, ok, _ := workspace.ReadRegular(dir, rel); !ok {
		return nil, nil
	}
	s, ps := series.Load(dir, rel)
	if s == nil {
		return nil, ps
	}
	var out []*work.Work
	for _, id := range s.Works {
		if w, _, wps := work.Resolve(dir, id); w != nil && len(wps) == 0 {
			out = append(out, w)
		}
	}
	return out, nil
}

// continuityNext 为连续性发现给出下一步命令。
func continuityNext(ds []Diagnostic, ref, dir string) {
	for i := range ds {
		switch {
		case ds[i].Code == "continuity.knowledge-early":
			ds[i].Next = []string{
				shellCommand("wa-cli", "knowledge", "list", ref, "--all", "--dir", dir, "--json"),
				shellCommand("wa-cli", "continuity", ref, "--dir", dir, "--json"),
			}
		case strings.HasPrefix(ds[i].Code, "continuity."):
			ds[i].Next = []string{
				shellCommand("wa-cli", "continuity", ref, "--dir", dir, "--json"),
				shellCommand("wa-cli", "decisions", "--dir", dir, "--json"),
			}
		}
	}
}
