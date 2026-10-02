package metrics

import (
	"fmt"
	"os"
	"path/filepath"
	"runtime"
	"slices"
	"strings"
	"testing"
	"time"

	"github.com/kw12121212/writing-anything/internal/catalog"
	"github.com/kw12121212/writing-anything/internal/decision"
	"github.com/kw12121212/writing-anything/internal/resolve"
	"github.com/kw12121212/writing-anything/internal/workspace"
)

const unitDir = ".writing-anything/works/post"

var content = []byte("第一段。\n\n第二段。\n\n第三段。\n")

func tension(sessions int) resolve.ScoredMetric {
	var anchors []catalog.Anchor
	for s := 1; s <= 5; s++ {
		anchors = append(anchors, catalog.Anchor{Score: s, Description: "说明" + string(rune('0'+s)), Example: "例"})
	}
	return resolve.ScoredMetric{ScoredMetric: catalog.ScoredMetric{ID: "tension", Title: "紧张度", Scale: catalog.Scale{Min: 1, Max: 5}, Anchors: anchors,
		Sessions: sessions, MaxSpread: 1, Tolerance: 1, MinSamples: 5, MinAgreement: 0.7}, Source: "pack:house"}
}

func newRoot(t *testing.T) string {
	t.Helper()
	root := t.TempDir()
	if _, ps := workspace.Init(root, nil); len(ps) > 0 {
		t.Fatal(ps)
	}
	if err := os.MkdirAll(filepath.Join(root, unitDir), 0o755); err != nil {
		t.Fatal(err)
	}
	return root
}

func codes(ps []workspace.Problem) []string {
	var out []string
	for _, p := range ps {
		out = append(out, p.Code)
	}
	return out
}

func session(id string, scores ...int) string {
	s := "session: " + id + "\nscores:\n"
	for i, v := range scores {
		s += "  - {metric: tension, paragraph: " + string(rune('1'+i)) + ", score: " + string(rune('0'+v)) + "}\n"
	}
	return s
}

func never(resolve.ScoredMetric) bool { return false }

func record(t *testing.T, root string, in string, ms []resolve.ScoredMetric, text []byte) (*Recorded, []workspace.Problem) {
	t.Helper()
	segs := Segments(text)
	parsed, ps := ParseInput([]byte(in), segs, ms)
	if ps != nil {
		return nil, ps
	}
	return Record(root, unitDir, "post", parsed, segs, Digest(text), ms, never)
}

func TestSegments(t *testing.T) {
	segs := Segments(content)
	if len(segs) != 3 || segs[1].Paragraph != 2 || segs[1].Text != "第二段。" || len(segs[1].SHA256) != 64 {
		t.Fatalf("segments = %+v", segs)
	}
	if s := Segments(nil); s == nil || len(s) != 0 {
		t.Fatalf("empty = %#v", s)
	}
}

func TestParseInputRejects(t *testing.T) {
	ms := []resolve.ScoredMetric{tension(3)}
	segs := Segments(content)
	tests := []struct{ in, code, field string }{
		{"session: s1\nscores: [", "artifact.parse-error", ""},
		{"session: S 1\nscores: [{metric: tension, paragraph: 1, score: 1}]\n", "artifact.schema-violation", "session"},
		{"session: s1\nscores:\n  - {metric: pace, paragraph: 1, score: 1}\n", "metrics.unknown-metric", "scores[0].metric"},
		{"session: s1\nscores:\n  - {metric: tension, paragraph: 4, score: 1}\n", "metrics.unknown-paragraph", "scores[0].paragraph"},
		{session("s1", 7, 1, 1), "metrics.score-out-of-range", "scores[0].score"},
		{session("s1", 1, 1, 1) + "  - {metric: tension, paragraph: 1, score: 2}\n", "artifact.duplicate-id", "scores[3]"},
		{session("s1", 1, 1), "metrics.incomplete-session", "scores"},
	}
	for _, tt := range tests {
		_, ps := ParseInput([]byte(tt.in), segs, ms)
		if len(ps) != 1 || ps[0].Code != tt.code || ps[0].Field != tt.field || ps[0].Path != "" {
			t.Errorf("%q: %+v, want %s at %s", tt.in, ps, tt.code, tt.field)
		}
	}
	if _, ps := ParseInput([]byte(session("s1", 1, 1)), segs, ms); !strings.Contains(ps[0].Message, "缺少第 3 段") {
		t.Fatalf("message: %+v", ps)
	}
}

func TestRecordAndDisputes(t *testing.T) {
	root := newRoot(t)
	ms := []resolve.ScoredMetric{tension(3)}
	res, ps := record(t, root, session("s1", 3, 2, 4), ms, content)
	if ps != nil || res.Replaced || res.Dropped || res.Decisions != nil || !slices.Equal(res.Changed, []string{unitDir + "/metrics.yaml"}) {
		t.Fatalf("s1: %+v %+v", res, ps)
	}
	// 只有两个会话时，即使分歧很大也不建决策。
	if res, ps := record(t, root, session("s2", 3, 3, 4), ms, content); ps != nil || res.Decisions != nil {
		t.Fatalf("s2: %+v %+v", res, ps)
	}
	res, ps = record(t, root, session("s3", 3, 5, 4), ms, content)
	if ps != nil || !slices.Equal(res.Decisions, []string{"dec-1"}) || !slices.Equal(res.Changed, []string{decision.Path, unitDir + "/metrics.yaml"}) {
		t.Fatalf("s3: %+v %+v", res, ps)
	}
	ds, _, _ := decision.Load(root)
	d := ds[0]
	if d.Blocking || !slices.Equal(d.Units, []string{"post"}) || !strings.Contains(d.Question, "第 2 段") || !strings.Contains(d.Question, "紧张度") ||
		!strings.Contains(*d.Rationale, "2、3、5") || len(d.Options) != 5 || d.Options[2].Text != "3：说明3" || *d.Recommendation != "c" || !strings.Contains(*d.Rationale, "中位数为 3") {
		t.Fatalf("decision = %+v", d)
	}
	f, _ := Load(root, unitDir)
	if len(f.Sessions) != 3 || !slices.Equal(f.Decisions, []DecisionRef{{"tension", 2, "dec-1"}}) || f.DraftSHA256 != Digest(content) {
		t.Fatalf("file = %+v", f)
	}
	// 重新记录同一会话：替换，不重复建决策，内容未变时不写文件。
	res, ps = record(t, root, session("s3", 3, 5, 4), ms, content)
	if ps != nil || !res.Replaced || res.Decisions != nil || res.Changed != nil {
		t.Fatalf("again: %+v %+v", res, ps)
	}

	sum := Summarize(f, Segments(content), Digest(content), ms, ds, nil)
	it := sum.Items[0]
	seg := it.Segments[1]
	if sum.Stale || it.Sessions != 3 || it.Required != 3 || !slices.Equal(seg.Scores, []int{2, 3, 5}) || *seg.Median != 3 || *seg.Spread != 3 ||
		seg.Status != "disputed" || *seg.Decision != "dec-1" || seg.Value != nil || it.Segments[0].Status != "agreed" || *it.Segments[0].Value != 3 || it.Agreement.Rate != nil {
		t.Fatalf("summary = %+v %+v", it, seg)
	}
	if _, ps := decision.Decide(root, "dec-1", "d", "", "作者：4 分", fixedNow); ps != nil {
		t.Fatal(ps)
	}
	ds, _, _ = decision.Load(root)
	if seg := Summarize(f, Segments(content), Digest(content), ms, ds, nil).Items[0].Segments[1]; seg.Status != "decided" || *seg.Value != 4 {
		t.Fatalf("decided = %+v", seg)
	}

	// 正文改变：丢弃旧会话与决策记录。
	edited := append([]byte("新开头。\n\n"), content...)
	if sum := Summarize(f, Segments(edited), Digest(edited), ms, ds, nil); !sum.Stale || sum.Items[0].Segments[0].Status != "pending" {
		t.Fatalf("stale summary = %+v", sum)
	}
	res, ps = record(t, root, "session: s9\nscores:\n  - {metric: tension, paragraph: 1, score: 1}\n  - {metric: tension, paragraph: 2, score: 1}\n  - {metric: tension, paragraph: 3, score: 1}\n  - {metric: tension, paragraph: 4, score: 1}\n", ms, edited)
	if ps != nil || !res.Dropped {
		t.Fatalf("dropped: %+v %+v", res, ps)
	}
	if f, _ := Load(root, unitDir); len(f.Sessions) != 1 || f.Decisions != nil || f.DraftSHA256 != Digest(edited) {
		t.Fatalf("after drop = %+v", f)
	}
}

func TestAdvisoryAndAverageMedian(t *testing.T) {
	root := newRoot(t)
	ms := []resolve.ScoredMetric{tension(2)}
	segs := Segments(content)
	in, _ := ParseInput([]byte(session("s1", 1, 1, 1)), segs, ms)
	Record(root, unitDir, "post", in, segs, Digest(content), ms, func(resolve.ScoredMetric) bool { return true })
	in, _ = ParseInput([]byte(session("s2", 4, 1, 2)), segs, ms)
	res, ps := Record(root, unitDir, "post", in, segs, Digest(content), ms, func(resolve.ScoredMetric) bool { return true })
	if ps != nil || res.Decisions != nil {
		t.Fatalf("advisory metric got decisions: %+v %+v", res, ps)
	}
	f, _ := Load(root, unitDir)
	if seg := Summarize(f, segs, Digest(content), ms, nil, nil).Items[0].Segments[0]; *seg.Median != 2.5 || seg.Status != "disputed" {
		t.Fatalf("median = %+v", seg)
	}
	// 不再声明的指标的分数不列出，记为 Unknown。
	if sum := Summarize(f, segs, Digest(content), nil, nil, nil); len(sum.Items) != 0 || !slices.Equal(sum.Unknown, []string{"tension"}) || !slices.Equal(sum.Sessions, []string{"s1", "s2"}) {
		t.Fatalf("unknown = %+v", sum)
	}
	if sum := Summarize(nil, segs, Digest(content), ms, nil, nil); sum.Stale || len(sum.Items[0].Segments[0].Scores) != 0 || sum.Items[0].Segments[0].Median != nil {
		t.Fatalf("no file = %+v", sum)
	}
}

func TestCalibration(t *testing.T) {
	root := newRoot(t)
	m := tension(3)
	cal, ps := LoadCalibration(root)
	if ps != nil || len(cal.Samples) != 0 {
		t.Fatal(ps)
	}
	add := func(unit string, ai float64, author int) {
		t.Helper()
		if _, ps := cal.Calibrate(root, Sample{Metric: "tension", Source: "pack:house", Unit: unit, Paragraph: 1, ParagraphSHA256: strings.Repeat("a", 64),
			AIMedian: ai, AuthorScore: author, Confirmation: "作者确认", RecordedAt: "2026-10-02T08:00:00Z"}); ps != nil {
			t.Fatal(ps)
		}
	}
	add("a", 3, 3)
	if a := cal.Agreement(m); a.Samples != 1 || a.Agreed != 1 || *a.Rate != 1 || a.Advisory {
		t.Fatalf("one = %+v", a)
	}
	add("a", 3, 5) // 同一单元、段落与内容：替换
	add("b", 2, 3)
	add("c", 2, 5)
	add("d", 1, 5)
	if a := cal.Agreement(m); a.Samples != 4 || a.Agreed != 1 || a.Advisory {
		t.Fatalf("four = %+v", a)
	}
	add("e", 2, 5)
	if a := cal.Agreement(m); a.Samples != 5 || a.Agreed != 1 || *a.Rate != 0.2 || !a.Advisory {
		t.Fatalf("five = %+v", a)
	}
	other := m
	other.Source = "pack:other"
	if a := cal.Agreement(other); a.Samples != 0 || a.Advisory {
		t.Fatalf("other source = %+v", a)
	}
	reloaded, ps := LoadCalibration(root)
	if ps != nil || len(reloaded.Samples) != 5 || reloaded.Samples[0].AuthorScore != 5 {
		t.Fatalf("reloaded = %+v %+v", reloaded, ps)
	}
	if checked, ps := CheckCalibration(root); !slices.Equal(checked, []string{CalibrationPath}) || ps != nil {
		t.Fatalf("check: %v %+v", checked, ps)
	}
	// 内容不变时不写文件。
	if changed, ps := reloaded.Calibrate(root, reloaded.Samples[4]); changed != nil || ps != nil {
		t.Fatalf("unchanged: %v %+v", changed, ps)
	}
	write(t, root, CalibrationPath, "schema_version: 1\nsamples: &s []\n")
	c, _ := LoadCalibration(root)
	if _, ps := c.Calibrate(root, reloaded.Samples[0]); !slices.Equal(codes(ps), []string{"metrics.anchored-value"}) {
		t.Fatalf("anchored: %+v", ps)
	}
	write(t, root, CalibrationPath, "schema_version: 1\nsamples: [{metric: x}]\n")
	if _, ps := LoadCalibration(root); len(ps) == 0 || ps[0].Code != "artifact.schema-violation" {
		t.Fatalf("invalid: %+v", ps)
	}
	if ps := CheckArtifact(root, CalibrationPath); ps == nil {
		t.Fatal("artifact check passed")
	}
}

func write(t *testing.T, root, rel, content string) {
	t.Helper()
	if err := os.WriteFile(filepath.Join(root, filepath.FromSlash(rel)), []byte(content), 0o644); err != nil {
		t.Fatal(err)
	}
}

func TestValidation(t *testing.T) {
	root := newRoot(t)
	if checked, ps := CheckUnitArtifacts(root, []string{unitDir}); checked != nil || ps != nil {
		t.Fatalf("absent: %v %+v", checked, ps)
	}
	if checked, ps := CheckCalibration(root); checked != nil || ps != nil {
		t.Fatalf("no calibration: %v %+v", checked, ps)
	}
	h := strings.Repeat("a", 64)
	rel := unitDir + "/metrics.yaml"
	write(t, root, rel, "schema_version: 1\ndraft_sha256: "+h+"\nsessions:\n  - {id: s1, scores: [{metric: t, paragraph: 1, score: 1}, {metric: t, paragraph: 1, score: 2}]}\n  - {id: s1, scores: [{metric: t, paragraph: 1, score: 1}]}\ndecisions:\n  - {metric: t, paragraph: 1, decision: dec-1}\n  - {metric: t, paragraph: 1, decision: dec-2}\n")
	checked, ps := CheckUnitArtifacts(root, []string{unitDir})
	var fields []string
	for _, p := range ps {
		fields = append(fields, p.Code+" "+p.Field)
	}
	if !slices.Equal(checked, []string{rel}) || !slices.Equal(fields, []string{"artifact.duplicate-id sessions[0].scores[1]", "artifact.duplicate-id sessions[1].id", "artifact.duplicate-id decisions[1]"}) {
		t.Fatalf("duplicates: %v %v", checked, fields)
	}
	if ps := CheckArtifact(root, rel); len(ps) != 3 {
		t.Fatalf("artifact: %+v", ps)
	}
	write(t, root, rel, "sessions: [")
	if _, ps := Load(root, unitDir); !slices.Equal(codes(ps), []string{"artifact.parse-error"}) {
		t.Fatalf("parse: %+v", ps)
	}
	// 带锚点的会话不修改。
	write(t, root, rel, "schema_version: 1\ndraft_sha256: "+Digest(content)+"\nsessions:\n  - &s {id: s1, scores: [{metric: tension, paragraph: 1, score: 1}]}\n")
	if _, ps := record(t, root, session("s1", 1, 1, 1), []resolve.ScoredMetric{tension(3)}, content); !slices.Equal(codes(ps), []string{"metrics.anchored-value"}) {
		t.Fatalf("anchored: %+v", ps)
	}
	if runtime.GOOS != "windows" {
		os.Remove(filepath.Join(root, rel))
		if err := os.Symlink(filepath.Join(root, workspace.ConfigPath), filepath.Join(root, rel)); err != nil {
			t.Fatal(err)
		}
		if checked, ps := CheckUnitArtifacts(root, []string{unitDir}); len(checked) != 1 || !slices.Equal(codes(ps), []string{"artifact.not-found"}) {
			t.Fatalf("symlink: %v %+v", checked, ps)
		}
	}
}

var fixedNow = time.Date(2026, 10, 2, 8, 0, 0, 0, time.UTC)

func TestOpenDecisionUpdatedInPlace(t *testing.T) {
	root := newRoot(t)
	ms := []resolve.ScoredMetric{tension(2)}
	record(t, root, session("s1", 1, 1, 1), ms, content)
	if res, _ := record(t, root, session("s2", 1, 5, 1), ms, content); !slices.Equal(res.Decisions, []string{"dec-1"}) {
		t.Fatalf("first dispute: %+v", res)
	}
	// 替换会话、分数改变：原地更新尚未决定的决策，不另建。
	res, ps := record(t, root, session("s2", 1, 4, 1), ms, content)
	if ps != nil || res.Decisions != nil || !slices.Equal(res.Updated, []string{"dec-1"}) || !slices.Contains(res.Changed, decision.Path) {
		t.Fatalf("update: %+v %+v", res, ps)
	}
	ds, _, _ := decision.Load(root)
	if len(ds) != 1 || !strings.Contains(*ds[0].Rationale, "1、4") || *ds[0].Recommendation != "c" {
		t.Fatalf("queue = %+v", ds)
	}
	// 正文改变：未决定的决策记录保留，同一片段再有分歧时仍更新它。
	edited := []byte("改过的第一段。\n\n第二段。\n\n第三段。\n")
	record(t, root, session("s3", 1, 1, 1), ms, edited)
	res, _ = record(t, root, session("s4", 1, 5, 1), ms, edited)
	if res.Decisions != nil || !slices.Equal(res.Updated, []string{"dec-1"}) {
		t.Fatalf("after edit: %+v", res)
	}
	if ds, _, _ := decision.Load(root); len(ds) != 1 {
		t.Fatalf("new decision created: %+v", ds)
	}
	// 已决定的决策在正文改变后不再沿用。
	decision.Decide(root, "dec-1", "e", "", "作者：5 分", fixedNow)
	again := []byte("又改了。\n\n第二段。\n\n第三段。\n")
	record(t, root, session("s5", 1, 1, 1), ms, again)
	if res, _ := record(t, root, session("s6", 1, 5, 1), ms, again); !slices.Equal(res.Decisions, []string{"dec-2"}) {
		t.Fatalf("after decided: %+v", res)
	}
}

func TestDecidedValueFromOptionText(t *testing.T) {
	root := newRoot(t)
	ms := []resolve.ScoredMetric{tension(2)}
	record(t, root, session("s1", 1, 1, 1), ms, content)
	record(t, root, session("s2", 1, 5, 1), ms, content)
	decision.Decide(root, "dec-1", "b", "", "作者：2 分", fixedNow)
	ds, _, _ := decision.Load(root)
	f, _ := Load(root, unitDir)
	if seg := Summarize(f, Segments(content), Digest(content), ms, ds, nil).Items[0].Segments[1]; *seg.Value != 2 {
		t.Fatalf("value = %+v", seg)
	}
	// scale 改变：作者的选择仍是 2 分，不按新的 scale 反推。
	shifted := ms[0]
	shifted.Scale = catalog.Scale{Min: 0, Max: 4}
	if seg := Summarize(f, Segments(content), Digest(content), []resolve.ScoredMetric{shifted}, ds, nil).Items[0].Segments[1]; seg.Status != "decided" || *seg.Value != 2 {
		t.Fatalf("shifted = %+v", seg)
	}
	narrow := ms[0]
	narrow.Scale = catalog.Scale{Min: 3, Max: 5}
	if seg := Summarize(f, Segments(content), Digest(content), []resolve.ScoredMetric{narrow}, ds, nil).Items[0].Segments[1]; seg.Status != "disputed" || seg.Value != nil {
		t.Fatalf("out of scale = %+v", seg)
	}
}

// validate 判为合法、但不是 wa-cli 写出的 metrics.yaml（会话没有覆盖全部段落）不能让汇总崩溃。
func TestSummarizeHandWrittenFile(t *testing.T) {
	f := &File{DraftSHA256: Digest(content), Sessions: []Session{
		{ID: "a", Scores: []Score{{Metric: "tension", Paragraph: 1, Score: 3}}},
		{ID: "b", Scores: []Score{{Metric: "tension", Paragraph: 1, Score: 3}}},
		{ID: "c", Scores: []Score{{Metric: "tension", Paragraph: 1, Score: 3}}}}}
	segs := Summarize(f, Segments(content), Digest(content), []resolve.ScoredMetric{tension(3)}, nil, nil).Items[0].Segments
	if segs[0].Status != "agreed" || segs[1].Status != "pending" || segs[1].Median != nil {
		t.Fatalf("segments = %+v", segs)
	}
}

func TestRecordTooLarge(t *testing.T) {
	root := newRoot(t)
	ms := []resolve.ScoredMetric{tension(3)}
	var text, in strings.Builder
	for i := 1; i <= 8000; i++ {
		text.WriteString("段。\n\n")
		fmt.Fprintf(&in, "  - {metric: tension, paragraph: %d, score: 1}\n", i)
	}
	if _, ps := record(t, root, "session: s1\nscores:\n"+in.String(), ms, []byte(text.String())); ps != nil {
		t.Fatal(ps)
	}
	before, _ := os.ReadFile(filepath.Join(root, unitDir, "metrics.yaml"))
	_, ps := record(t, root, "session: s2\nscores:\n"+in.String(), ms, []byte(text.String()))
	if !slices.Equal(codes(ps), []string{"metrics.too-large"}) || ps[0].Path != "" {
		t.Fatalf("got %+v", ps)
	}
	if after, _ := os.ReadFile(filepath.Join(root, unitDir, "metrics.yaml")); string(after) != string(before) {
		t.Fatal("metrics.yaml modified")
	}
}

func TestRecordFailureWritesNothing(t *testing.T) {
	root := newRoot(t)
	ms := []resolve.ScoredMetric{tension(3)}
	if _, ps := record(t, root, session("s1", 1, 1), ms, content); ps == nil {
		t.Fatal("incomplete accepted")
	}
	if _, err := os.Stat(filepath.Join(root, unitDir, "metrics.yaml")); err == nil {
		t.Fatal("metrics.yaml created")
	}
	h := Digest(content)
	for _, body := range []string{
		"schema_version: 1\ndraft_sha256: " + h + "\nsessions: &s []\n",
	} {
		write(t, root, unitDir+"/metrics.yaml", body)
		if _, ps := record(t, root, session("s1", 1, 1, 1), ms, content); !slices.Equal(codes(ps), []string{"metrics.anchored-value"}) {
			t.Fatalf("%q: %+v", body, ps)
		}
	}
	// decisions 带锚点只在需要追加决策时才影响写入。
	write(t, root, unitDir+"/metrics.yaml", "schema_version: 1\ndraft_sha256: "+h+"\nsessions:\n  - {id: a, scores: [{metric: tension, paragraph: 1, score: 1}, {metric: tension, paragraph: 2, score: 1}, {metric: tension, paragraph: 3, score: 1}]}\n  - {id: b, scores: [{metric: tension, paragraph: 1, score: 1}, {metric: tension, paragraph: 2, score: 1}, {metric: tension, paragraph: 3, score: 1}]}\ndecisions: &d []\n")
	before, _ := os.ReadFile(filepath.Join(root, unitDir, "metrics.yaml"))
	if _, ps := record(t, root, session("c", 5, 1, 1), ms, content); !slices.Equal(codes(ps), []string{"metrics.anchored-value"}) {
		t.Fatalf("decisions anchored: %+v", ps)
	}
	if after, _ := os.ReadFile(filepath.Join(root, unitDir, "metrics.yaml")); string(after) != string(before) {
		t.Fatal("metrics.yaml modified")
	}
	if _, err := os.Stat(filepath.Join(root, decision.Path)); err == nil {
		t.Fatal("decision added before the failing write check")
	}
}
