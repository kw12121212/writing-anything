package catalog

import (
	"fmt"
	"slices"
	"strings"
)

// 指标的错误码（Error Code）。已发布的错误码不得改名或复用。
const (
	CodeInvalidScale   = "metrics.invalid-scale"
	CodeInvalidAnchors = "metrics.invalid-anchors"
)

// AI 评分指标未声明时的默认取值。
const (
	DefaultSessions     = 3
	DefaultMaxSpread    = 1
	DefaultTolerance    = 1
	DefaultMinSamples   = 5
	DefaultMinAgreement = 0.7
	// MaxScaleRange 是 scale 的 max − min 上限：每个分值都是分歧决策的一个选项，决策最多 26 个选项。
	MaxScaleRange = 25
)

// Scale 是 AI 评分指标的整数分值范围。
type Scale struct {
	Min int `json:"min"`
	Max int `json:"max"`
}

// Anchor 是锚定评分（Anchored Scale）中一个分值的说明与示例片段。
type Anchor struct {
	Score       int    `json:"score"`
	Description string `json:"description"`
	Example     string `json:"example"`
}

// ScoredMetric 是文体或规范包声明的 AI 评分指标（Metric），未声明的阈值已补齐默认值。
type ScoredMetric struct {
	ID           string   `json:"id"`
	Title        string   `json:"title"`
	Description  string   `json:"description"`
	Scale        Scale    `json:"scale"`
	Anchors      []Anchor `json:"anchors"`
	Sessions     int      `json:"sessions"`
	MaxSpread    int      `json:"maxSpread"`
	Tolerance    int      `json:"tolerance"`
	MinSamples   int      `json:"minSamples"`
	MinAgreement float64  `json:"minAgreement"`
}

// Anchor 返回分值 score 的锚定说明；没有时为 nil。
func (m ScoredMetric) Anchor(score int) *Anchor {
	for i := range m.Anchors {
		if m.Anchors[i].Score == score {
			return &m.Anchors[i]
		}
	}
	return nil
}

// scoredMetricFile 是文件中的指标：字段名与 YAML 一致，未声明的阈值为 nil。
type scoredMetricFile struct {
	ID           string   `json:"id"`
	Title        string   `json:"title"`
	Description  string   `json:"description"`
	Scale        Scale    `json:"scale"`
	Anchors      []Anchor `json:"anchors"`
	Sessions     *int     `json:"sessions"`
	MaxSpread    *int     `json:"max_spread"`
	Tolerance    *int     `json:"tolerance"`
	MinSamples   *int     `json:"min_samples"`
	MinAgreement *float64 `json:"min_agreement"`
}

func or[T any](p *T, def T) T {
	if p == nil {
		return def
	}
	return *p
}

func scoredMetrics(fs []scoredMetricFile) []ScoredMetric {
	var out []ScoredMetric
	for _, f := range fs {
		out = append(out, ScoredMetric{ID: f.ID, Title: f.Title, Description: f.Description, Scale: f.Scale, Anchors: f.Anchors,
			Sessions: or(f.Sessions, DefaultSessions), MaxSpread: or(f.MaxSpread, DefaultMaxSpread), Tolerance: or(f.Tolerance, DefaultTolerance),
			MinSamples: or(f.MinSamples, DefaultMinSamples), MinAgreement: or(f.MinAgreement, DefaultMinAgreement)})
	}
	return out
}

// checkScoredMetrics 检查指标 id 不重复、scale 合理、锚定分值恰好覆盖 scale 内的每个整数。
func checkScoredMetrics(ms []ScoredMetric, add func(code, field, msg string)) {
	for _, d := range duplicates(len(ms), func(i int) string { return ms[i].ID }) {
		add(CodeDuplicateID, fmt.Sprintf("scored_metrics[%d].id", d), "AI 评分指标 "+ms[d].ID+" 重复")
	}
	for i, m := range ms {
		if r := m.Scale.Max - m.Scale.Min; r < 1 || r > MaxScaleRange {
			add(CodeInvalidScale, fmt.Sprintf("scored_metrics[%d].scale", i),
				fmt.Sprintf("指标 %s 的 scale 为 %d 到 %d；max 必须大于 min，且相差不超过 %d", m.ID, m.Scale.Min, m.Scale.Max, MaxScaleRange))
			continue
		}
		var missing, extra, repeated []string
		for s := m.Scale.Min; s <= m.Scale.Max; s++ {
			switch n := len(slices.DeleteFunc(slices.Clone(m.Anchors), func(a Anchor) bool { return a.Score != s })); {
			case n == 0:
				missing = append(missing, fmt.Sprint(s))
			case n > 1:
				repeated = append(repeated, fmt.Sprint(s))
			}
		}
		for _, a := range m.Anchors {
			if a.Score < m.Scale.Min || a.Score > m.Scale.Max {
				extra = append(extra, fmt.Sprint(a.Score))
			}
		}
		var why []string
		if missing != nil {
			why = append(why, "缺少 "+strings.Join(missing, "、")+" 分")
		}
		if repeated != nil {
			why = append(why, strings.Join(repeated, "、")+" 分重复")
		}
		if extra != nil {
			why = append(why, strings.Join(extra, "、")+" 分不在 scale 内")
		}
		if why != nil {
			add(CodeInvalidAnchors, fmt.Sprintf("scored_metrics[%d].anchors", i),
				fmt.Sprintf("指标 %s 的锚定评分（Anchored Scale）必须为 %d 到 %d 的每个分值各给一项说明与示例：%s", m.ID, m.Scale.Min, m.Scale.Max, strings.Join(why, "；")))
		}
	}
}
