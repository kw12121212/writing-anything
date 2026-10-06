package cli

import (
	"fmt"
	"io"
	"slices"
	"strings"

	"github.com/kw12121212/writing-anything/internal/draft"
	"github.com/kw12121212/writing-anything/internal/resolve"
	"github.com/kw12121212/writing-anything/internal/review"
	"github.com/kw12121212/writing-anything/internal/series"
	"github.com/kw12121212/writing-anything/internal/stylemetric"
	"github.com/kw12121212/writing-anything/internal/work"
	"github.com/kw12121212/writing-anything/internal/workspace"
)

var metricsComputeCommand = &command{
	name: "metrics compute",
	summary: "计算单元（Unit）及其有正文的子孙单元的确定性文风与节奏指标（Metric），与作者文风画像（Style Profile）和本作品已验收单元对比，" +
		"列出文风漂移（Style Drift）；只衡量偏离、不评判好坏，不阻塞验收，wa-cli 不调用模型",
	args:  []argSpec{{"unit", unitRefHelp, true}},
	flags: []flagSpec{dirFlag},
	outputKeys: []outputKey{
		{"units", "按单元树先序的单元：unit、metrics（chars、sentenceMean、sentenceStdev、paragraphMean、dialogueRatio、vocabularyRichness、functionWords、fourChar、aiCliche、punctuation）、" +
			"paragraphs（paragraph、line、sentences、sentenceMean）与 drift（metric、baseline、value、mean、stdev、threshold、paragraph、line、message）"},
		{"baselines", "使用的基准：profile（文风画像的 source，没有时为 null）、accepted（作为基准的已验收单元，不足 3 个时为空数组）与 threshold（value 与 source）；失败时为 null"},
	},
	exitCodes: standardExitCodes,
	run:       runMetricsCompute,
}

type computedUnit struct {
	Unit       string                  `json:"unit"`
	Metrics    stylemetric.Metrics     `json:"metrics"`
	Paragraphs []stylemetric.Paragraph `json:"paragraphs"`
	Drift      []stylemetric.Finding   `json:"drift"`
}

type profileRef struct {
	Source string `json:"source"`
}

type baselinesView struct {
	Profile   *profileRef   `json:"profile"`
	Accepted  []string      `json:"accepted"`
	Threshold resolve.Value `json:"threshold"`
}

type computeResult struct {
	Result
	Units     []computedUnit `json:"units"`
	Baselines *baselinesView `json:"baselines"`
}

// aiTerms 返回合并结果中生效的 ai-cliche 规则的词表。
func aiTerms(r *resolve.Resolved) []string {
	var out []string
	for _, id := range sortedRuleIDs(r) {
		rule := r.Rules[id]
		if rule.Type.Value != "ai-cliche" || !rule.Enabled {
			continue
		}
		if terms, ok := rule.Params["terms"].Value.([]any); ok {
			for _, t := range terms {
				if s, ok := t.(string); ok {
					out = append(out, s)
				}
			}
		}
	}
	return out
}

func sortedRuleIDs(r *resolve.Resolved) []string {
	var ids []string
	for id := range r.Rules {
		ids = append(ids, id)
	}
	slices.Sort(ids)
	return ids
}

// within 报告单元 x 是否为 u 本身或它的子孙。
func within(x, u *work.Unit) bool {
	for ; x != nil; x = x.Parent {
		if x == u {
			return true
		}
	}
	return false
}

func runMetricsCompute(inv *invocation) int {
	dir, ref := projectDir(inv), inv.args[0]
	res := &computeResult{Units: []computedUnit{}}
	problems := workspace.Require(dir)
	if problems == nil {
		var w *work.Work
		var u *work.Unit
		if w, u, problems = work.Resolve(dir, ref); u != nil {
			problems = compute(dir, w, u, res)
		}
	}
	res.Diagnostics = diagnostics(problems, dir)
	return finish(inv, res, func(out io.Writer) error {
		var b strings.Builder
		for _, cu := range res.Units {
			fmt.Fprintf(&b, "%s：%d 字，平均句长 %v，平均段长 %v；文风漂移（Style Drift）%d 项\n", cu.Unit, cu.Metrics.Chars, cu.Metrics.SentenceMean, cu.Metrics.ParagraphMean, len(cu.Drift))
			for _, d := range cu.Drift {
				fmt.Fprintf(&b, "  - %s\n", d.Message)
			}
		}
		_, err := io.WriteString(out, b.String())
		return err
	})
}

func compute(dir string, w *work.Work, u *work.Unit, res *computeResult) []workspace.Problem {
	r, _, warnings := series.ForWork(dir, w)
	if r == nil {
		return warnings
	}
	terms := aiTerms(r)
	k, ok := r.DriftThreshold.Value.(float64)
	if !ok || k <= 0 {
		k = resolve.DefaultDriftThreshold // 合并结果总是给出正数；防御性地不让阈值变成 0
	}
	base := &baselinesView{Accepted: []string{}, Threshold: r.DriftThreshold}
	var profile map[string]any
	if r.StyleProfile != nil {
		profile, _ = r.StyleProfile.Value.(map[string]any)
		base.Profile = &profileRef{Source: r.StyleProfile.Source}
	}
	// 本作品已验收、且不在本次计算范围内的单元：验收记录合法且与当前正文一致。
	var accepted []stylemetric.Metrics
	for _, x := range w.Units() {
		if within(x, u) {
			continue
		}
		content, ok, ps := draft.Read(dir, w, x)
		a, aps := review.LoadAcceptance(dir, w.UnitDir(x))
		if !ok || len(ps) > 0 || len(aps) > 0 || a == nil || a.DraftSHA256 != review.SHA256(content) {
			continue
		}
		m, _ := stylemetric.Measure(content, terms)
		accepted = append(accepted, m)
		base.Accepted = append(base.Accepted, x.Ref)
	}
	if len(accepted) < stylemetric.MinAccepted {
		accepted, base.Accepted = nil, []string{}
	}
	for _, x := range w.Units() {
		if !within(x, u) {
			continue
		}
		content, ok, ps := draft.Read(dir, w, x)
		if len(ps) > 0 {
			return append(warnings, ps...)
		}
		if !ok {
			continue
		}
		m, paragraphs := stylemetric.Measure(content, terms)
		drift := append(stylemetric.ProfileDrift(m, paragraphs, profile, k), stylemetric.AcceptedDrift(m, accepted, k)...)
		res.Units = append(res.Units, computedUnit{Unit: x.Ref, Metrics: m, Paragraphs: paragraphs, Drift: drift})
	}
	if len(res.Units) == 0 {
		return append(warnings, workspace.Problem{Code: draft.CodeNotFound, Severity: workspace.SeverityError, Path: w.UnitDir(u) + "/" + draft.FileName,
			Message: "单元 " + u.Ref + " 及其子孙单元都没有可读取的正文 draft.md（不存在、途经符号链接或不是普通文件）；请用 wa-cli draft write 写入"})
	}
	res.Baselines = base
	return warnings
}
