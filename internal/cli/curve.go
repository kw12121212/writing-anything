package cli

import (
	"fmt"
	"io"
	"math"
	"strings"

	"github.com/kw12121212/writing-anything/internal/brief"
	"github.com/kw12121212/writing-anything/internal/catalog"
	"github.com/kw12121212/writing-anything/internal/decision"
	"github.com/kw12121212/writing-anything/internal/draft"
	"github.com/kw12121212/writing-anything/internal/metrics"
	"github.com/kw12121212/writing-anything/internal/outline"
	"github.com/kw12121212/writing-anything/internal/resolve"
	"github.com/kw12121212/writing-anything/internal/series"
	"github.com/kw12121212/writing-anything/internal/work"
	"github.com/kw12121212/writing-anything/internal/workspace"
)

var curveCommand = &command{
	name:    "curve",
	summary: "把作品的 AI 评分按单元树先序汇总为曲线（如情绪曲线（Emotion Curve）与张力曲线（Tension Curve）），与场景契约目标或" +
		"结构类写作法节拍的目标张力（Target Tension）对比，输出偏离发现项与文体声明的节奏检查（Pacing Check）；只读，" +
		"发现项不阻塞验收，wa-cli 不调用模型",
	args:  []argSpec{{"work", "作品（Work）的引用：<work> 表示作品的根单元；曲线覆盖作品的全部单元", true}},
	flags: []flagSpec{dirFlag},
	outputKeys: []outputKey{
		{"curves", "每个已声明 AI 评分指标的一条曲线：metric、title、source、scale、advisory、points（unit、value、target）与 deviations" +
			"（unit、metric、value、target、difference、message）；失败时为空数组"},
		{"pacing", "节奏检查（Pacing Check）发现项数组：check、metric、from、to、gapChars 与 message；失败时为空数组"},
	},
	exitCodes: standardExitCodes,
	run:       runCurve,
}

type curveResult struct {
	Result
	Curves []metrics.CurveResult   `json:"curves"`
	Pacing []metrics.PacingFinding `json:"pacing"`
}

// metricDefs 剥离合并结果的来源层，供只关心指标定义（id 与 scale）的检查（如场景契约 targets）共用。
func metricDefs(ms []resolve.ScoredMetric) []catalog.ScoredMetric {
	out := make([]catalog.ScoredMetric, len(ms))
	for i, m := range ms {
		out[i] = m.ScoredMetric
	}
	return out
}

func runCurve(inv *invocation) int {
	dir, ref := projectDir(inv), inv.args[0]
	res := &curveResult{Curves: []metrics.CurveResult{}, Pacing: []metrics.PacingFinding{}}
	problems := workspace.Require(dir)
	if problems == nil {
		var w *work.Work
		if w, _, problems = work.Resolve(dir, ref); w != nil {
			problems = curve(dir, w, res)
		}
	}
	res.Diagnostics = diagnostics(problems, dir)
	for i := range res.Diagnostics {
		if res.Diagnostics[i].Code == metrics.CodeNoneDeclared {
			res.Diagnostics[i].Next = []string{shellCommand("wa-cli", "config", "resolved", "--dir", dir, "--json"), shellCommand("wa-cli", "pack", "list", "--dir", dir, "--json")}
		}
	}
	return finish(inv, res, func(out io.Writer) error {
		_, err := io.WriteString(out, curveText(res))
		return err
	})
}

// curve 聚合作品全部单元（单元树先序）的 AI 评分走势。目标分值来自场景契约（Scene Contract）的 targets，未声明且指标
// id 为 tension 时由节拍的目标张力线性映射；偏离判定用该指标的 tolerance。wa-cli 不修改任何产物。
func curve(dir string, w *work.Work, res *curveResult) []workspace.Problem {
	r, _, warnings := series.ForWork(dir, w)
	if r == nil {
		return warnings
	}
	if len(r.ScoredMetrics) == 0 {
		return []workspace.Problem{{Code: metrics.CodeNoneDeclared, Severity: workspace.SeverityError,
			Message: "作品的文体与启用的规范包都没有声明 AI 评分指标（scored_metrics）；请在规范包中声明后启用"}}
	}
	units := w.Units()
	root := units[0]
	// 根单元选用的结构类写作法给出节拍的目标张力：情节节拍 id（根单元大纲的节 id）→ 该节 role 承担的节拍声明的张力。
	beatTension := map[string]float64{}
	ms := outline.LoadMessages(dir, w.UnitDir(root), brief.LoadQueue(dir), outline.Picker{Methods: catalog.LoadMethods(dir), Default: unitDefaultMethod(w, root)})
	if o, fatal, _ := outline.LoadLenient(dir, w.UnitDir(root), ms); len(fatal) == 0 && o != nil && ms.Method != nil && ms.Method.Kind == catalog.MethodStructure {
		for _, s := range o.Sections {
			for _, p := range ms.Method.Parts() {
				if s.Role == p.ID && p.Tension != nil {
					beatTension[s.ID] = *p.Tension
				}
			}
		}
	}
	queue, _, ps := decision.Load(dir)
	if ps != nil {
		return ps
	}
	cal, ps := metrics.LoadCalibration(dir)
	if ps != nil {
		return ps
	}
	cu := make([]metrics.CurveUnit, 0, len(units))
	for _, u := range units {
		unitDir := w.UnitDir(u)
		c := metrics.CurveUnit{Ref: u.Ref}
		if content, ok, ps := draft.Read(dir, w, u); len(ps) > 0 {
			return ps
		} else if ok {
			c.Content, c.HasDraft = content, true
		}
		if f, ps := metrics.Load(dir, unitDir); len(ps) > 0 {
			return ps
		} else if f != nil {
			c.File = f
		}
		// 只需要大纲节的 targets 与 beats：以空 Messages 加载（不解析写作简报），引用问题（refs）被丢弃、不报告。
		if o, fatal, _ := outline.LoadLenient(dir, unitDir, outline.Messages{}); len(fatal) > 0 {
			return fatal
		} else if o != nil {
			for _, s := range o.Sections {
				c.Sections = append(c.Sections, metrics.CurveSection{ID: s.ID, Role: s.Role, Beats: s.Beats, Targets: s.Targets})
			}
		}
		cu = append(cu, c)
	}
	res.Curves, res.Pacing = metrics.Curve(cu, r.ScoredMetrics, beatTension, r.PacingChecks, queue, cal)
	if res.Curves == nil {
		res.Curves = []metrics.CurveResult{}
	}
	if res.Pacing == nil {
		res.Pacing = []metrics.PacingFinding{}
	}
	return warnings
}

// curveText 以人读文字输出每个指标的字符条形与发现项列表。
func curveText(res *curveResult) string {
	var b strings.Builder
	for _, c := range res.Curves {
		fmt.Fprintf(&b, "%s（指标 %s，来自 %s）：%d 到 %d 分\n", c.Title, c.Metric, c.Source, c.Scale.Min, c.Scale.Max)
		for _, p := range c.Points {
			fmt.Fprintf(&b, "  %-24s %s  %s\n", p.Unit, bar(p, c.Scale), label(p))
		}
	}
	findings := func(title string, msgs []string) {
		if len(msgs) == 0 {
			fmt.Fprintf(&b, "%s：无\n", title)
			return
		}
		fmt.Fprintf(&b, "%s（不阻塞验收）：\n", title)
		for _, m := range msgs {
			fmt.Fprintf(&b, "  - %s\n", m)
		}
	}
	var deviations, pacing []string
	for _, c := range res.Curves {
		for _, d := range c.Deviations {
			deviations = append(deviations, d.Message)
		}
	}
	for _, p := range res.Pacing {
		pacing = append(pacing, p.Message)
	}
	findings("偏离发现项", deviations)
	findings("节奏检查发现项", pacing)
	return b.String()
}

// bar 返回一个数据点在指标刻度上的字符条形：█ 到取值位置，┆ 标记目标位置。
func bar(p metrics.CurvePoint, s catalog.Scale) string {
	const width = 20
	track := []rune(strings.Repeat("·", width))
	pos := func(x float64) int {
		at := int(math.Round((x - float64(s.Min)) / float64(s.Max-s.Min) * float64(width-1)))
		return min(max(at, 0), width-1)
	}
	if p.Target != nil {
		track[pos(*p.Target)] = '┆'
	}
	if p.Value != nil {
		for i := 0; i <= pos(*p.Value); i++ {
			if track[i] == '·' {
				track[i] = '█'
			}
		}
	}
	return string(track)
}

// label 说明一个数据点的取值与目标。
func label(p metrics.CurvePoint) string {
	v, t := "未定", "未定"
	if p.Value != nil {
		v = fmt.Sprintf("%g", *p.Value)
	}
	if p.Target != nil {
		t = fmt.Sprintf("%g", *p.Target)
	}
	return fmt.Sprintf("值 %s，目标 %s", v, t)
}
