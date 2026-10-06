package catalog

import (
	"fmt"
	"strings"
)

// 节奏检查的错误码（Error Code）。已发布的错误码不得改名或复用。
const (
	CodeUnknownPacingMetric = "pacing.unknown-metric"
	CodeInvalidPacingParams = "pacing.invalid-params"
)

// PacingCheck 是文体（Genre）声明的节奏检查（Pacing Check）：片段（Segment）的指标取值达到 MinScore 视为节拍点，
// 相邻两个节拍点片段之间的非空白字符数超过 MaxGapChars 时由 wa-cli curve 报告发现项。判定只基于已记录的分数与
// 确定性指标，wa-cli 不调用模型。
type PacingCheck struct {
	ID          string `json:"id"`
	Metric      string `json:"metric"`
	MinScore    int    `json:"minScore"`
	MaxGapChars int    `json:"maxGapChars"`
}

// pacingCheckFile 是文件中的节奏检查：字段名与 YAML 一致。
type pacingCheckFile struct {
	ID       string `json:"id"`
	Metric   string `json:"metric"`
	MinScore int    `json:"min_score"`
	MaxGap   int    `json:"max_gap_chars"`
}

func pacingChecks(fs []pacingCheckFile) []PacingCheck {
	var out []PacingCheck
	for _, f := range fs {
		out = append(out, PacingCheck{ID: f.ID, Metric: f.Metric, MinScore: f.MinScore, MaxGapChars: f.MaxGap})
	}
	return out
}

// checkPacingChecks 检查节奏检查 id 不重复、max_gap_chars 为正整数；metric 是否为已声明的 AI 评分指标（Metric）
// 与 min_score 是否在该指标的 scale 内由 CheckPacingRefs 在合并后检查（指标可以来自规范包）。
func checkPacingChecks(cs []PacingCheck, add func(code, field, msg string)) {
	for _, d := range duplicates(len(cs), func(i int) string { return cs[i].ID }) {
		add(CodeDuplicateID, fmt.Sprintf("pacing_checks[%d].id", d), "节奏检查 "+cs[d].ID+" 重复")
	}
	for i, c := range cs {
		if c.MaxGapChars <= 0 {
			add(CodeInvalidPacingParams, fmt.Sprintf("pacing_checks[%d].max_gap_chars", i),
				fmt.Sprintf("节奏检查 %s 的 max_gap_chars 必须为正整数，给出的是 %d", c.ID, c.MaxGapChars))
		}
	}
}

// CheckPacingRefs 检查节奏检查引用的指标已在 ms 中声明、min_score 在该指标的 scale 内；add 收问题（field 为
// pacing_checks[i].metric 或 pacing_checks[i].min_score）。节奏检查只能由文体声明，指标可以来自规范包，
// 因此这项检查在合并全部配置层之后由 internal/resolve 调用。
func CheckPacingRefs(cs []PacingCheck, ms []ScoredMetric, add func(code, field, msg string)) {
	var known []string
	for _, m := range ms {
		known = append(known, m.ID)
	}
	joined := "无"
	if known != nil {
		joined = strings.Join(known, "、")
	}
	for i, c := range cs {
		j := -1
		for k, m := range ms {
			if m.ID == c.Metric {
				j = k
				break
			}
		}
		if j < 0 {
			add(CodeUnknownPacingMetric, fmt.Sprintf("pacing_checks[%d].metric", i),
				fmt.Sprintf("节奏检查 %s 引用的 AI 评分指标（Metric）%s 不在任何配置层声明；已声明的指标为 %s", c.ID, c.Metric, joined))
			continue
		}
		if c.MinScore < ms[j].Scale.Min || c.MinScore > ms[j].Scale.Max {
			add(CodeInvalidPacingParams, fmt.Sprintf("pacing_checks[%d].min_score", i),
				fmt.Sprintf("节奏检查 %s 的 min_score 为 %d，不在指标 %s 的 scale（%d 到 %d）内", c.ID, c.MinScore, c.Metric, ms[j].Scale.Min, ms[j].Scale.Max))
		}
	}
}
