package catalog

import (
	"slices"
	"testing"
)

func problemsOf(add func(func(string, string, string))) []string {
	var codes []string
	add(func(code, field, msg string) { codes = append(codes, code+" "+field) })
	return codes
}

// TestPacingChecksLocal 覆盖文体层就能判定的节奏检查问题：id 重复与 max_gap_chars 非正整数。
func TestPacingChecksLocal(t *testing.T) {
	cs := []PacingCheck{
		{ID: "gap", Metric: "emotion", MinScore: 3, MaxGapChars: 2000},
		{ID: "gap", Metric: "tension", MinScore: 2, MaxGapChars: 0},
	}
	got := problemsOf(func(add func(string, string, string)) { checkPacingChecks(cs, add) })
	want := []string{"artifact.duplicate-id pacing_checks[1].id", "pacing.invalid-params pacing_checks[1].max_gap_chars"}
	if !slices.Equal(got, want) {
		t.Fatalf("checkPacingChecks = %v, want %v", got, want)
	}
}

// TestPacingRefs 覆盖合并后才能判定的引用问题：metric 未声明与 min_score 越界。
func TestPacingRefs(t *testing.T) {
	ms := []ScoredMetric{{ID: "emotion", Scale: Scale{Min: 1, Max: 5}}}
	// 引用未声明的指标。
	got := problemsOf(func(add func(string, string, string)) {
		CheckPacingRefs([]PacingCheck{{ID: "c1", Metric: "tension", MinScore: 3, MaxGapChars: 100}}, ms, add)
	})
	if !slices.Equal(got, []string{"pacing.unknown-metric pacing_checks[0].metric"}) {
		t.Fatalf("unknown metric: %v", got)
	}
	// min_score 不在该指标的 scale 内。
	got = problemsOf(func(add func(string, string, string)) {
		CheckPacingRefs([]PacingCheck{{ID: "c1", Metric: "emotion", MinScore: 7, MaxGapChars: 100}}, ms, add)
	})
	if !slices.Equal(got, []string{"pacing.invalid-params pacing_checks[0].min_score"}) {
		t.Fatalf("min_score out of range: %v", got)
	}
	// 合法时没有问题。
	if got := problemsOf(func(add func(string, string, string)) {
		CheckPacingRefs([]PacingCheck{{ID: "c1", Metric: "emotion", MinScore: 3, MaxGapChars: 100}}, ms, add)
	}); got != nil {
		t.Fatalf("valid: %v", got)
	}
}

// TestShortStoryPacingAndMetrics 校验内置 short-story 文体声明的指标与节奏检查本身通过文体校验。
func TestShortStoryPacingAndMetrics(t *testing.T) {
	g, ps, ok := LoadGenre("short-story")
	if !ok || g == nil {
		t.Fatalf("load short-story: ok = %v, problems = %+v", ok, ps)
	}
	if len(ps) > 0 {
		t.Fatalf("short-story problems: %+v", ps)
	}
	if !slices.ContainsFunc(g.ScoredMetrics, func(m ScoredMetric) bool { return m.ID == "emotion" }) ||
		!slices.ContainsFunc(g.ScoredMetrics, func(m ScoredMetric) bool { return m.ID == "tension" }) {
		t.Fatalf("scored metrics: %+v", g.ScoredMetrics)
	}
	if len(g.PacingChecks) != 1 || g.PacingChecks[0].ID != "emotional-beat-gap" || g.PacingChecks[0].Metric != "emotion" ||
		g.PacingChecks[0].MinScore != 3 || g.PacingChecks[0].MaxGapChars != 2000 {
		t.Fatalf("pacing checks: %+v", g.PacingChecks)
	}
}
