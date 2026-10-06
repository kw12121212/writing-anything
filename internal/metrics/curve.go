package metrics

import (
	"fmt"
	"math"
	"slices"
	"unicode"

	"github.com/kw12121212/writing-anything/internal/catalog"
	"github.com/kw12121212/writing-anything/internal/decision"
	"github.com/kw12121212/writing-anything/internal/resolve"
)

// CurveSection 是曲线目标解析需要的大纲节（Outline Section）：声明的目标锚定分值与承担的情节节拍（Story Beat）。
type CurveSection struct {
	ID      string
	Role    string
	Beats   []string
	Targets map[string]int
}

// CurveUnit 是曲线聚合的一个单元：正文（没有时 HasDraft 为 false）、AI 评分记录 metrics.yaml（没有时 File 为 nil）
// 与大纲节。片段取值与 metrics show 的语义相同：agreed 为中位数、decided 为作者选择的分值，其余为 nil。
type CurveUnit struct {
	Ref      string
	Content  []byte
	HasDraft bool
	File     *File
	Sections []CurveSection
}

// CurvePoint 是曲线上的一个数据点：单元的取值与目标分值，任一没有时为 nil。
type CurvePoint struct {
	Unit   string   `json:"unit"`
	Value  *float64 `json:"value"`
	Target *float64 `json:"target"`
}

// Deviation 是一个偏离发现项：单元取值与目标之差大于该指标的 tolerance。只在 curve 输出，不阻塞验收。
type Deviation struct {
	Unit       string  `json:"unit"`
	Metric     string  `json:"metric"`
	Value      float64 `json:"value"`
	Target     float64 `json:"target"`
	Difference float64 `json:"difference"`
	Message    string  `json:"message"`
}

// CurveResult 是一个已声明指标的曲线：按单元树先序的数据点与偏离发现项。
type CurveResult struct {
	Metric     string       `json:"metric"`
	Title      string       `json:"title"`
	Source     string       `json:"source"`
	Scale      catalog.Scale `json:"scale"`
	Advisory   bool         `json:"advisory"`
	Points     []CurvePoint `json:"points"`
	Deviations []Deviation  `json:"deviations"`
}

// PacingFinding 是一个节奏发现项：相邻两个节拍点片段之间的非空白字符数超过文体声明的上限。只在 curve 输出，不阻塞验收。
type PacingFinding struct {
	Check    string `json:"check"`
	Metric   string `json:"metric"`
	From     string `json:"from"`
	To       string `json:"to"`
	GapChars int    `json:"gapChars"`
	Message  string `json:"message"`
}

// medianF 返回非空取值的中位数：偶数个时取中间两值的平均。
func medianF(vs []float64) float64 {
	s := slices.Sorted(slices.Values(vs))
	n := len(s)
	if n%2 == 1 {
		return s[n/2]
	}
	return (s[n/2-1] + s[n/2]) / 2
}

// CurveTarget 按三层解析单元 u 上指标 m 的目标分值：节 targets 声明的锚定分值优先（取第一个为该指标声明目标的节）；
// 未声明且指标 id 为 tension 时，由该单元大纲节 beats 引用的情节节拍（Story Beat，根单元大纲的节）携带的目标张力
// （Target Tension，beatTension 按节 id 给出 0 到 10 的原始张力）线性映射到该指标的 scale，多个节拍取映射值的平均后
// 四舍五入（远离零）；其他情况没有目标。
func CurveTarget(u CurveUnit, m resolve.ScoredMetric, beatTension map[string]float64) *float64 {
	for _, s := range u.Sections {
		if v, ok := s.Targets[m.ID]; ok {
			t := float64(v)
			return &t
		}
	}
	if m.ID != "tension" {
		return nil // 节拍数据只有 tension 一个目标字段，只有该指标享有写作法走势的默认目标
	}
	n, sum := 0, 0.0
	for _, s := range u.Sections {
		for _, b := range s.Beats {
			if t, ok := beatTension[b]; ok {
				sum += float64(m.Scale.Min) + t/10*float64(m.Scale.Max-m.Scale.Min)
				n++
			}
		}
	}
	if n == 0 {
		return nil
	}
	t := math.Round(sum / float64(n))
	return &t
}

// Curve 汇总全部单元的 AI 评分走势：对每个已声明指标输出一条曲线（数据点按 units 的顺序，调用方按单元树先序给出），
// 与目标对比输出偏离发现项；并执行文体声明的节奏检查。全部只读，wa-cli 不调用模型；发现项不影响退出码。
func Curve(units []CurveUnit, ms []resolve.ScoredMetric, beatTension map[string]float64, checks []resolve.PacingCheck, queue []decision.Decision, cal *Calibration) ([]CurveResult, []PacingFinding) {
	// 每个单元只切分一次片段：取值、曲线与节奏检查共用同一份结果（workspace-artifacts/input-limits）。
	segs := make([][]Segment, len(units))
	for i, u := range units {
		if u.HasDraft {
			segs[i] = Segments(u.Content)
		}
	}
	// 每个单元每个指标的片段取值（paragraph → 已定值），复用 metrics show 的取值语义（Summarize）。
	values := map[string]map[int]map[string]*float64{} // unit ref → paragraph → metric id → 取值
	for i, u := range units {
		sum := Summarize(u.File, segs[i], Digest(u.Content), ms, queue, cal)
		perUnit := map[int]map[string]*float64{}
		values[u.Ref] = perUnit
		for _, it := range sum.Items {
			for _, seg := range it.Segments {
				if seg.Value != nil {
					v := *seg.Value
					if perUnit[seg.Paragraph] == nil {
						perUnit[seg.Paragraph] = map[string]*float64{}
					}
					perUnit[seg.Paragraph][it.Metric] = &v
				}
			}
		}
	}
	segValue := func(ref string, paragraph int, metric string) *float64 {
		return values[ref][paragraph][metric]
	}
	var curves []CurveResult
	for _, m := range ms {
		c := CurveResult{Metric: m.ID, Title: m.Title, Source: m.Source, Scale: m.Scale, Advisory: cal.Agreement(m).Advisory, Points: []CurvePoint{}, Deviations: []Deviation{}}
		for i, u := range units {
			var vs []float64
			for _, seg := range segs[i] {
				if v := segValue(u.Ref, seg.Paragraph, m.ID); v != nil {
					vs = append(vs, *v)
				}
			}
			p := CurvePoint{Unit: u.Ref}
			if len(vs) > 0 {
				v := medianF(vs)
				p.Value = &v
			}
			p.Target = CurveTarget(u, m, beatTension)
			if p.Value != nil && p.Target != nil && math.Abs(*p.Value-*p.Target) > float64(m.Tolerance) {
				d := *p.Value - *p.Target
				direction := "偏低"
				if d > 0 {
					direction = "偏高"
				}
				c.Deviations = append(c.Deviations, Deviation{Unit: u.Ref, Metric: m.ID, Value: *p.Value, Target: *p.Target, Difference: d,
					Message: fmt.Sprintf("单元 %s 的%s为 %s，目标为 %s，相差 %s，%s；只衡量偏离，不评判好坏", u.Ref, m.Title, format(*p.Value), format(*p.Target), format(d), direction)})
			}
			c.Points = append(c.Points, p)
		}
		curves = append(curves, c)
	}
	return curves, pacingFindings(units, segs, checks, segValue)
}

// pacingFindings 按单元先序拼接正文片段执行节奏检查：片段取值达到 min_score 为节拍点，相邻两个节拍点片段之间
// 的非空白字符数（其间所有片段之和，相邻片段间隔为 0）超过 max_gap_chars 时输出一条发现项。
func pacingFindings(units []CurveUnit, segs [][]Segment, checks []resolve.PacingCheck, segValue func(ref string, paragraph int, metric string) *float64) []PacingFinding {
	var out []PacingFinding
	for _, c := range checks {
		type cursor struct {
			from string
			gap  int
			seen bool
		}
		var cur cursor
		for i, u := range units {
			for _, seg := range segs[i] {
				at := fmt.Sprintf("%s 第 %d 段", u.Ref, seg.Paragraph)
				if v := segValue(u.Ref, seg.Paragraph, c.Metric); v != nil && *v >= float64(c.MinScore) {
					if cur.seen && cur.gap > c.MaxGapChars {
						out = append(out, PacingFinding{Check: c.ID, Metric: c.Metric, From: cur.from, To: at, GapChars: cur.gap,
							Message: fmt.Sprintf("节奏检查（Pacing Check）%s：从 %s 到 %s 之间相隔 %d 个非空白字符，超过允许的 %d", c.ID, cur.from, at, cur.gap, c.MaxGapChars)})
					}
					cur = cursor{from: at, seen: true}
					continue
				}
				cur.gap += nonSpace(seg.Text)
			}
		}
	}
	return out
}

// nonSpace 返回 text 中非空白字符的个数。
func nonSpace(text string) int {
	n := 0
	for _, r := range text {
		if !unicode.IsSpace(r) {
			n++
		}
	}
	return n
}
