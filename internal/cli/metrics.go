package cli

import (
	"fmt"
	"io"
	"slices"
	"strconv"
	"strings"

	"github.com/kw12121212/writing-anything/internal/decision"
	"github.com/kw12121212/writing-anything/internal/draft"
	"github.com/kw12121212/writing-anything/internal/metrics"
	"github.com/kw12121212/writing-anything/internal/resolve"
	"github.com/kw12121212/writing-anything/internal/series"
	"github.com/kw12121212/writing-anything/internal/work"
	"github.com/kw12121212/writing-anything/internal/workspace"
)

var metricsPlanCommand = &command{
	name:    "metrics plan",
	summary: "为单元（Unit）生成 AI 评分指标（Metric）的打分任务：按段落的片段、每个指标的锚定评分（Anchored Scale）与需要的独立会话数；只读，wa-cli 不调用模型",
	args:    []argSpec{{"unit", unitRefHelp, true}},
	flags:   []flagSpec{dirFlag, {"metric", flagList, "只规划该指标；可重复，省略时规划全部指标"}},
	outputKeys: []outputKey{{"plan", "打分任务：unit、draftSha256、segments（paragraph、text、sha256）、metrics（id、title、description、source、scale、anchors、sessions、maxSpread、advisory）" +
		"与 instructions；失败时为 null"}},
	exitCodes: standardExitCodes,
	run:       runMetricsPlan,
}

var metricsRecordCommand = &command{
	name: "metrics record",
	summary: "从 --file 或标准输入记录一次独立打分会话（Scoring Session）到单元的 metrics.yaml；会话数足够后，分歧（Score Disagreement）超过阈值的片段登记为" +
		"非阻塞的作者决策",
	args:  []argSpec{{"unit", unitRefHelp, true}},
	flags: []flagSpec{dirFlag, {"file", flagString, "打分会话文件（YAML：session 与 scores，每项含 metric、paragraph、score 与可选的 rationale），相对 --dir 或为绝对路径；省略时读取标准输入"}},
	outputKeys: []outputKey{
		{"session", "记录的会话：id 与 replaced（是否替换了同 id 的会话）；失败时为 null"},
		{"decisions", "本次新建的分歧决策 id 数组"},
	},
	exitCodes: standardExitCodes,
	run:       runMetricsRecord,
}

var metricsShowCommand = &command{
	name:    "metrics show",
	summary: "汇总单元的 AI 评分：每个片段每个指标的打分、中位数、分歧与作者决定，以及每个指标的校准一致率（Agreement Rate）与是否仅供参考",
	args:    []argSpec{{"unit", unitRefHelp, true}},
	flags:   []flagSpec{dirFlag},
	outputKeys: []outputKey{{"metrics", "unit、path、stale、sessions 与 items（metric、title、source、advisory、sessions、required、agreement、segments：paragraph、scores、median、spread、" +
		"status（pending、agreed、disputed 或 decided）、decision 与 value）；失败时为 null"}},
	exitCodes: standardExitCodes,
	run:       runMetricsShow,
}

var metricsCalibrateCommand = &command{
	name:    "metrics calibrate",
	summary: "记录作者对一个片段的打分作为校准（Calibration）样本：样本足够且与 AI 的一致率低于阈值的指标降级为仅供参考，不再产生分歧决策",
	args:    []argSpec{{"unit", unitRefHelp, true}},
	flags: []flagSpec{dirFlag,
		{"metric", flagString, "指标 id"},
		{"paragraph", flagString, "段落编号，从 1 开始，与 wa-cli provenance 一致"},
		{"score", flagString, "作者给出的分值"},
		{"confirmation", flagString, "作者原话的摘要，作为作者确认"}},
	outputKeys: []outputKey{{"calibration", "该指标在其配置层的校准统计：metric、source、samples、agreed、rate 与 advisory；失败时为 null"}},
	exitCodes:  standardExitCodes,
	run:        runMetricsCalibrate,
}

type metricView struct {
	ID          string `json:"id"`
	Title       string `json:"title"`
	Description string `json:"description"`
	Source      string `json:"source"`
	Scale       any    `json:"scale"`
	Anchors     any    `json:"anchors"`
	Sessions    int    `json:"sessions"`
	MaxSpread   int    `json:"maxSpread"`
	Advisory    bool   `json:"advisory"`
}

type planView struct {
	Unit         string            `json:"unit"`
	DraftSHA256  string            `json:"draftSha256"`
	Segments     []metrics.Segment `json:"segments"`
	Metrics      []metricView      `json:"metrics"`
	Instructions string            `json:"instructions"`
}

type metricsPlanResult struct {
	Result
	Plan *planView `json:"plan"`
}

type sessionView struct {
	ID       string `json:"id"`
	Replaced bool   `json:"replaced"`
}

type metricsRecordResult struct {
	Result
	Session   *sessionView `json:"session"`
	Decisions []string     `json:"decisions"`
}

type metricsSummaryView struct {
	Unit     string                 `json:"unit"`
	Path     string                 `json:"path"`
	Stale    bool                   `json:"stale"`
	Sessions []string               `json:"sessions"`
	Items    []metrics.MetricResult `json:"items"`
}

type metricsShowResult struct {
	Result
	Metrics *metricsSummaryView `json:"metrics"`
}

type calibrationView struct {
	Metric   string   `json:"metric"`
	Source   string   `json:"source"`
	Samples  int      `json:"samples"`
	Agreed   int      `json:"agreed"`
	Rate     *float64 `json:"rate"`
	Advisory bool     `json:"advisory"`
}

type metricsCalibrateResult struct {
	Result
	Calibration *calibrationView `json:"calibration"`
}

// metricsUnit 是 metrics 命令共用的上下文：单元、正文（没有正文时 content 为 nil、hasDraft 为 false）、按作品文体合并的 AI 评分指标与校准样本。
type metricsUnit struct {
	w        *work.Work
	u        *work.Unit
	dir      string
	content  []byte
	hasDraft bool
	metrics  []resolve.ScoredMetric
	cal      *metrics.Calibration
}

// loadMetricsUnit 解析单元并加载上下文；needDraft 为 true 时没有正文报告 draft.not-found，needMetrics 为 true 时没有任何指标报告 metrics.none-declared。
func loadMetricsUnit(root, ref string, needDraft, needMetrics bool) (*metricsUnit, []workspace.Problem) {
	w, u, ps := work.Resolve(root, ref)
	if u == nil {
		return nil, ps
	}
	mu := &metricsUnit{w: w, u: u, dir: w.UnitDir(u)}
	content, ok, ps := draft.Read(root, w, u)
	if len(ps) > 0 {
		return nil, ps
	}
	mu.content, mu.hasDraft = content, ok
	if needDraft && !ok {
		return nil, []workspace.Problem{{Code: draft.CodeNotFound, Severity: workspace.SeverityError, Path: mu.dir + "/" + draft.FileName,
			Message: "单元 " + u.Ref + " 还没有正文，无法打分；请先用 wa-cli draft write 写入"}}
	}
	r, ps := series.Resolve(root, w)
	if r == nil {
		return nil, ps
	}
	mu.metrics = r.ScoredMetrics
	if needMetrics && len(mu.metrics) == 0 {
		return nil, []workspace.Problem{{Code: metrics.CodeNoneDeclared, Severity: workspace.SeverityError,
			Message: "作品的文体与启用的规范包都没有声明 AI 评分指标（scored_metrics）；请在规范包中声明后启用"}}
	}
	if mu.cal, ps = metrics.LoadCalibration(root); ps != nil {
		return nil, ps
	}
	return mu, nil
}

func (mu *metricsUnit) advisory(m resolve.ScoredMetric) bool { return mu.cal.Agreement(m).Advisory }

func (mu *metricsUnit) find(id string) (resolve.ScoredMetric, *workspace.Problem) {
	if i := slices.IndexFunc(mu.metrics, func(m resolve.ScoredMetric) bool { return m.ID == id }); i >= 0 {
		return mu.metrics[i], nil
	}
	var known []string
	for _, m := range mu.metrics {
		known = append(known, m.ID)
	}
	return resolve.ScoredMetric{}, &workspace.Problem{Code: metrics.CodeUnknownMetric, Severity: workspace.SeverityError,
		Message: fmt.Sprintf("指标 %s 不是已声明的 AI 评分指标；已声明的指标为 %s", id, strings.Join(known, "、"))}
}

// metricsNext 改写 metrics 命令中参数与输入问题（不填 path）的下一步：查看用法、打分任务与生效的指标。
func metricsNext(ds []Diagnostic, name, ref, dir string) {
	for i := range ds {
		if ds[i].Path != "" {
			continue
		}
		ds[i].Next = []string{"wa-cli help " + name + " --json", shellCommand("wa-cli", "metrics", "plan", ref, "--dir", dir, "--json"),
			shellCommand("wa-cli", "config", "resolved", "--dir", dir, "--json")}
		if ds[i].Code == metrics.CodeNoneDeclared {
			ds[i].Next = []string{shellCommand("wa-cli", "config", "resolved", "--dir", dir, "--json"), shellCommand("wa-cli", "pack", "list", "--dir", dir, "--json")}
		}
	}
}

func runMetricsPlan(inv *invocation) int {
	dir, ref := projectDir(inv), inv.args[0]
	res := &metricsPlanResult{}
	problems := workspace.Require(dir)
	if problems == nil {
		var mu *metricsUnit
		if mu, problems = loadMetricsUnit(dir, ref, true, true); mu != nil {
			plan := &planView{Unit: mu.u.Ref, DraftSHA256: metrics.Digest(mu.content), Segments: metrics.Segments(mu.content), Metrics: []metricView{}, Instructions: metrics.Instructions}
			selected := mu.metrics
			if ids := inv.lists["metric"]; ids != nil {
				selected = nil
				for _, id := range ids {
					m, p := mu.find(id)
					if p != nil {
						problems = append(problems, *p)
					} else if !slices.ContainsFunc(selected, func(s resolve.ScoredMetric) bool { return s.ID == id }) {
						selected = append(selected, m)
					}
				}
			}
			for _, m := range selected {
				plan.Metrics = append(plan.Metrics, metricView{ID: m.ID, Title: m.Title, Description: m.Description, Source: m.Source, Scale: m.Scale, Anchors: m.Anchors,
					Sessions: m.Sessions, MaxSpread: m.MaxSpread, Advisory: mu.advisory(m)})
			}
			if problems == nil {
				res.Plan = plan
			}
		}
	}
	res.Diagnostics = diagnostics(problems, dir)
	metricsNext(res.Diagnostics, "metrics plan", ref, dir)
	return finish(inv, res, func(w io.Writer) error {
		if res.Plan == nil {
			return nil
		}
		var b strings.Builder
		fmt.Fprintf(&b, "单元（Unit）%s 的打分任务：%d 个片段（Segment）\n", res.Plan.Unit, len(res.Plan.Segments))
		for _, m := range res.Plan.Metrics {
			fmt.Fprintf(&b, "- %s（%s，来自 %s）：需要 %d 个独立会话\n", m.ID, m.Title, m.Source, m.Sessions)
		}
		b.WriteString(res.Plan.Instructions + "\n")
		_, err := io.WriteString(w, b.String())
		return err
	})
}

func runMetricsRecord(inv *invocation) int {
	dir, ref := projectDir(inv), inv.args[0]
	res := &metricsRecordResult{Decisions: []string{}}
	problems := workspace.Require(dir)
	var warnings []workspace.Problem
	if problems == nil {
		var data []byte
		data, _, problems = readInput(inv, dir)
		var mu *metricsUnit
		if problems == nil {
			mu, problems = loadMetricsUnit(dir, ref, true, true)
		}
		if mu != nil {
			segs := metrics.Segments(mu.content)
			var in *metrics.Input
			if in, problems = metrics.ParseInput(data, segs, mu.metrics); in != nil {
				var r *metrics.Recorded
				if r, problems = metrics.Record(dir, mu.dir, mu.u.Ref, in, segs, metrics.Digest(mu.content), mu.metrics, mu.advisory); r != nil {
					res.ChangedPaths = r.Changed
					if r.Decisions != nil {
						res.Decisions = r.Decisions
					}
					if problems == nil {
						res.Session = &sessionView{ID: r.Session, Replaced: r.Replaced}
					}
					if r.Dropped {
						warnings = append(warnings, workspace.Problem{Code: metrics.CodeSessionsDropped, Severity: workspace.SeverityWarning, Path: metrics.Path(mu.dir),
							Message: "正文在之前的打分之后被修改，已丢弃针对旧正文的会话与分歧决策记录，从本次会话重新开始"})
					}
				}
			}
		}
	}
	res.Diagnostics = diagnostics(append(problems, warnings...), dir)
	metricsNext(res.Diagnostics, "metrics record", ref, dir)
	helpNext(res.Diagnostics, "metrics record", []string{codeInputReadFailed})
	for i, d := range res.Diagnostics {
		if d.Code == metrics.CodeSessionsDropped {
			res.Diagnostics[i].Next = []string{shellCommand("wa-cli", "metrics", "show", ref, "--dir", dir, "--json")}
		}
	}
	return finish(inv, res, func(w io.Writer) error {
		if res.Session == nil {
			return nil
		}
		verb := "已记录"
		if res.Session.Replaced {
			verb = "已替换"
		}
		_, err := fmt.Fprintf(w, "%s单元（Unit）%s 的打分会话（Scoring Session）%s；新建分歧决策 %d 个\n", verb, ref, res.Session.ID, len(res.Decisions))
		return err
	})
}

// summarize 汇总单元的 AI 评分；problems 为 metrics.yaml 或决策队列的问题，warnings 为不再声明的指标。
func summarize(root string, mu *metricsUnit) (metrics.Summary, []workspace.Problem, []workspace.Problem) {
	f, ps := metrics.Load(root, mu.dir)
	if ps != nil {
		return metrics.Summary{}, ps, nil
	}
	queue, _, ps := decision.Load(root)
	if ps != nil {
		return metrics.Summary{}, ps, nil
	}
	sha := ""
	if mu.hasDraft {
		sha = metrics.Digest(mu.content)
	}
	sum := metrics.Summarize(f, metrics.Segments(mu.content), sha, mu.metrics, queue, mu.cal)
	var warnings []workspace.Problem
	for _, id := range sum.Unknown {
		warnings = append(warnings, workspace.Problem{Code: metrics.CodeUnknownMetric, Severity: workspace.SeverityWarning, Path: metrics.Path(mu.dir),
			Message: "指标 " + id + " 已不再由文体或启用的规范包声明，它的分数不列出"})
	}
	return sum, nil, warnings
}

func runMetricsShow(inv *invocation) int {
	dir, ref := projectDir(inv), inv.args[0]
	res := &metricsShowResult{}
	problems := workspace.Require(dir)
	if problems == nil {
		var mu *metricsUnit
		if mu, problems = loadMetricsUnit(dir, ref, false, false); mu != nil {
			sum, ps, warnings := summarize(dir, mu)
			if problems = append(ps, warnings...); ps == nil {
				res.Metrics = &metricsSummaryView{Unit: mu.u.Ref, Path: metrics.Path(mu.dir), Stale: sum.Stale, Sessions: sum.Sessions, Items: sum.Items}
			}
		}
	}
	res.Diagnostics = diagnostics(problems, dir)
	for i, d := range res.Diagnostics {
		if d.Code == metrics.CodeUnknownMetric {
			res.Diagnostics[i].Next = []string{shellCommand("wa-cli", "config", "resolved", "--dir", dir, "--json")}
		}
	}
	return finish(inv, res, func(w io.Writer) error {
		if res.Metrics == nil {
			return nil
		}
		var b strings.Builder
		fmt.Fprintf(&b, "单元（Unit）%s 的 AI 评分：%d 个会话", res.Metrics.Unit, len(res.Metrics.Sessions))
		if res.Metrics.Stale {
			b.WriteString("（正文已修改，需要重新打分）")
		}
		b.WriteString("\n")
		for _, it := range res.Metrics.Items {
			note := ""
			if it.Advisory {
				note = "，仅供参考"
			}
			fmt.Fprintf(&b, "%s（%s）：%d/%d 个会话%s\n", it.Metric, it.Title, it.Sessions, it.Required, note)
			for _, s := range it.Segments {
				v := "—"
				if s.Value != nil {
					v = strconv.FormatFloat(*s.Value, 'f', -1, 64)
				}
				fmt.Fprintf(&b, "  第 %d 段：%s %v → %s\n", s.Paragraph, s.Status, s.Scores, v)
			}
		}
		_, err := io.WriteString(w, b.String())
		return err
	})
}

func runMetricsCalibrate(inv *invocation) int {
	dir, ref := projectDir(inv), inv.args[0]
	res := &metricsCalibrateResult{}
	problems := workspace.Require(dir)
	var paragraph, score int
	if problems == nil {
		for _, f := range []string{"metric", "paragraph", "score", "confirmation"} {
			if strings.TrimSpace(inv.values[f]) == "" {
				problems = append(problems, invalidValue(f, inv.values[f], "必须给出非空的取值"))
			}
		}
		var err error
		if paragraph, err = strconv.Atoi(inv.values["paragraph"]); err != nil && inv.values["paragraph"] != "" || err == nil && paragraph < 1 {
			problems = append(problems, invalidValue("paragraph", inv.values["paragraph"], "应为从 1 开始的段落编号"))
		}
		if score, err = strconv.Atoi(inv.values["score"]); err != nil && inv.values["score"] != "" {
			problems = append(problems, invalidValue("score", inv.values["score"], "应为整数分值"))
		}
	}
	if problems == nil {
		res.Calibration, res.ChangedPaths, problems = calibrate(dir, ref, inv.values["metric"], paragraph, score, inv.values["confirmation"])
	}
	res.Diagnostics = diagnostics(problems, dir)
	metricsNext(res.Diagnostics, "metrics calibrate", ref, dir)
	for i, d := range res.Diagnostics {
		if d.Code == metrics.CodeNotScored {
			res.Diagnostics[i].Next = []string{shellCommand("wa-cli", "metrics", "show", ref, "--dir", dir, "--json"), shellCommand("wa-cli", "metrics", "plan", ref, "--dir", dir, "--json")}
		}
	}
	return finish(inv, res, func(w io.Writer) error {
		c := res.Calibration
		if c == nil {
			return nil
		}
		rate := "—"
		if c.Rate != nil {
			rate = strconv.FormatFloat(*c.Rate, 'f', -1, 64)
		}
		note := ""
		if c.Advisory {
			note = "；已降级为仅供参考"
		}
		_, err := fmt.Fprintf(w, "已记录校准（Calibration）样本：%s（%s）共 %d 个样本，%d 个相符，一致率（Agreement Rate）%s%s\n", c.Metric, c.Source, c.Samples, c.Agreed, rate, note)
		return err
	})
}

func calibrate(root, ref, metric string, paragraph, score int, confirmation string) (*calibrationView, []string, []workspace.Problem) {
	mu, ps := loadMetricsUnit(root, ref, true, true)
	if mu == nil {
		return nil, nil, ps
	}
	m, p := mu.find(metric)
	if p != nil {
		return nil, nil, []workspace.Problem{*p}
	}
	segs := metrics.Segments(mu.content)
	switch {
	case paragraph > len(segs):
		return nil, nil, []workspace.Problem{{Code: metrics.CodeUnknownParagraph, Severity: workspace.SeverityError, Message: fmt.Sprintf("第 %d 段不存在；正文共 %d 段", paragraph, len(segs))}}
	case score < m.Scale.Min || score > m.Scale.Max:
		return nil, nil, []workspace.Problem{{Code: metrics.CodeScoreOutOfRange, Severity: workspace.SeverityError,
			Message: fmt.Sprintf("指标 %s 的分值为 %d 到 %d，%d 不在范围内", m.ID, m.Scale.Min, m.Scale.Max, score)}}
	}
	sum, ps, _ := summarize(root, mu)
	if ps != nil {
		return nil, nil, ps
	}
	seg := sum.Items[slices.IndexFunc(sum.Items, func(r metrics.MetricResult) bool { return r.Metric == m.ID })].Segments[paragraph-1]
	if seg.Status == metrics.StatusPending {
		return nil, nil, []workspace.Problem{{Code: metrics.CodeNotScored, Severity: workspace.SeverityError, Path: metrics.Path(mu.dir),
			Message: fmt.Sprintf("第 %d 段的指标 %s 还没有完成 AI 打分（需要 %d 个会话，或正文已修改需要重新打分），无法与作者的打分比较", paragraph, m.ID, m.Sessions)}}
	}
	sample := metrics.Sample{Metric: m.ID, Source: m.Source, Unit: mu.u.Ref, Paragraph: paragraph, ParagraphSHA256: segs[paragraph-1].SHA256,
		AIMedian: *seg.Median, AuthorScore: score, Confirmation: confirmation, RecordedAt: now().UTC().Format("2006-01-02T15:04:05Z")}
	changed, ps := mu.cal.Calibrate(root, sample)
	if ps != nil {
		return nil, nil, ps
	}
	a := mu.cal.Agreement(m)
	return &calibrationView{Metric: m.ID, Source: m.Source, Samples: a.Samples, Agreed: a.Agreed, Rate: a.Rate, Advisory: a.Advisory}, changed, nil
}
