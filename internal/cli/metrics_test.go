package cli

import (
	"encoding/json"
	"os"
	"path/filepath"
	"slices"
	"strings"
	"testing"

	"github.com/kw12121212/writing-anything/internal/metrics"
)

const metricsPackYAML = "schema_version: 1\nid: house\ntitle: 本团队规范\nscored_metrics:\n  - id: tension\n    title: 紧张度\n    scale: {min: 1, max: 5}\n" +
	"    anchors:\n      - {score: 1, description: 平静, example: 午后阳光正好。}\n      - {score: 2, description: 略有不安, example: 他看了一眼门。}\n" +
	"      - {score: 3, description: 有隐忧, example: 门外好像有人。}\n      - {score: 4, description: 紧迫, example: 脚步声停在门口。}\n" +
	"      - {score: 5, description: 危机爆发, example: 门被撞开。}\n"

// metricsWork 返回一个启用了声明 tension 指标的规范包 house、作品 my-post 有三段正文的工作区。
func metricsWork(t *testing.T, extra string) string {
	t.Helper()
	dir := newWork(t)
	if err := os.MkdirAll(filepath.Join(dir, ".writing-anything", "packs"), 0o755); err != nil {
		t.Fatal(err)
	}
	writeFile(t, filepath.Join(dir, ".writing-anything", "packs", "house.yaml"), metricsPackYAML+extra)
	run(t, "config", "set", "packs", "house", "--dir", dir)
	writeFile(t, filepath.Join(dir, ".writing-anything", "works", "my-post", "draft.md"), "第一段。\n\n第二段。\n\n第三段。\n")
	return dir
}

func scoreSession(id string, scores ...int) string {
	s := "session: " + id + "\nscores:\n"
	for i, v := range scores {
		s += "  - {metric: tension, paragraph: " + string(rune('1'+i)) + ", score: " + string(rune('0'+v)) + "}\n"
	}
	return s
}

type metricsOut struct {
	result
	Plan        *planView           `json:"plan"`
	Session     *sessionView        `json:"session"`
	Decisions   []string            `json:"decisions"`
	Metrics     *metricsSummaryView `json:"metrics"`
	Calibration *calibrationView    `json:"calibration"`
}

func runMetrics(t *testing.T, input string, args ...string) (int, metricsOut) {
	t.Helper()
	code, stdout, _ := runIn(t, input, append([]string{"metrics"}, append(args, "--json")...)...)
	var out metricsOut
	if err := json.Unmarshal([]byte(stdout), &out); err != nil {
		t.Fatalf("%v: %v; %s", args, err, stdout)
	}
	return code, out
}

func TestMetricsPlan(t *testing.T) {
	dir := metricsWork(t, "")
	code, out := runMetrics(t, "", "plan", "my-post", "--dir", dir)
	p := out.Plan
	if code != 0 || p == nil || len(p.Segments) != 3 || p.Segments[2].Text != "第三段。" || len(p.Metrics) != 1 || p.Metrics[0].Source != "pack:house" ||
		p.Metrics[0].Sessions != 3 || !strings.Contains(p.Instructions, "独立会话") || len(out.ChangedPaths) != 0 || len(p.DraftSHA256) != 64 {
		t.Fatalf("plan: %d %+v", code, out)
	}
	if code, out := runMetrics(t, "", "plan", "my-post", "--metric", "pace", "--dir", dir); code != 1 || !hasNext(diag(t, out.result, "metrics.unknown-metric"), "wa-cli help metrics plan") {
		t.Fatalf("unknown: %d %+v", code, out)
	}
	if code, stdout, _ := run(t, "metrics", "plan", "my-post", "--dir", dir); code != 0 || !strings.Contains(stdout, "tension（紧张度，来自 pack:house）：需要 3 个独立会话") {
		t.Fatalf("text: %q", stdout)
	}
	plain := newWork(t)
	writeFile(t, filepath.Join(plain, ".writing-anything", "works", "my-post", "draft.md"), "正文。\n")
	if code, out := runMetrics(t, "", "plan", "my-post", "--dir", plain); code != 1 || !hasNext(diag(t, out.result, "metrics.none-declared"), "wa-cli pack list") {
		t.Fatalf("none: %d %+v", code, out)
	}
	if code, out := runMetrics(t, "", "plan", "my-post", "--dir", newWork(t)); code != 1 || len(diag(t, out.result, "draft.not-found").Next) == 0 {
		t.Fatalf("no draft: %d %+v", code, out)
	}
}

func TestMetricsRecordShowAndDecide(t *testing.T) {
	dir := metricsWork(t, "")
	for i, s := range []string{scoreSession("s1", 3, 2, 4), scoreSession("s2", 3, 3, 4)} {
		if code, out := runMetrics(t, s, "record", "my-post", "--dir", dir); code != 0 || out.Session.Replaced || len(out.Decisions) != 0 {
			t.Fatalf("session %d: %d %+v", i, code, out)
		}
	}
	code, out := runMetrics(t, scoreSession("s3", 3, 5, 4), "record", "my-post", "--dir", dir)
	if code != 0 || !slices.Equal(out.Decisions, []string{"dec-1"}) || !slices.Equal(out.ChangedPaths, []string{".writing-anything/decisions.yaml", ".writing-anything/works/my-post/metrics.yaml"}) {
		t.Fatalf("s3: %d %+v", code, out)
	}
	if code, out := runMetrics(t, scoreSession("s3", 3, 5, 4), "record", "my-post", "--dir", dir); code != 0 || !out.Session.Replaced || len(out.Decisions) != 0 || len(out.ChangedPaths) != 0 {
		t.Fatalf("again: %d %+v", code, out)
	}
	code, out = runMetrics(t, "", "show", "my-post", "--dir", dir)
	seg := out.Metrics.Items[0].Segments[1]
	if code != 0 || out.Metrics.Stale || seg.Status != "disputed" || *seg.Decision != "dec-1" || *seg.Median != 3 || *seg.Spread != 3 || out.Metrics.Path != ".writing-anything/works/my-post/metrics.yaml" {
		t.Fatalf("show: %d %+v", code, out)
	}
	run(t, "decide", "dec-1", "--option", "d", "--confirmation", "作者：4 分", "--dir", dir)
	if _, out := runMetrics(t, "", "show", "my-post", "--dir", dir); out.Metrics.Items[0].Segments[1].Status != "decided" || *out.Metrics.Items[0].Segments[1].Value != 4 {
		t.Fatalf("decided: %+v", out.Metrics.Items[0].Segments[1])
	}
	if code, stdout, _ := run(t, "metrics", "show", "my-post", "--dir", dir); code != 0 || !strings.Contains(stdout, "tension（紧张度）：3/3 个会话") || !strings.Contains(stdout, "第 2 段：decided [2 3 5] → 4") {
		t.Fatalf("text: %q", stdout)
	}
	// decision 不阻塞单元。
	if code, res := runJSON(t, "validate", "--dir", dir, "--json"); code != 0 || !strings.Contains(mustJSON(t, res["checked"]), "metrics.yaml") {
		t.Fatalf("validate: %d %v", code, res)
	}
	if code, _ := runJSON(t, "validate", ".writing-anything/works/my-post/metrics.yaml", "--dir", dir, "--json"); code != 0 {
		t.Fatalf("validate artifact: %d", code)
	}

	// 输入问题：不写文件，next 给出用法与打分任务。
	code, out = runMetrics(t, scoreSession("s4", 3, 9, 4), "record", "my-post", "--dir", dir)
	if d := diag(t, out.result, "metrics.score-out-of-range"); code != 1 || d.Field != "scores[1].score" || d.Path != "" || !hasNext(d, "wa-cli metrics plan my-post") || out.Session != nil {
		t.Fatalf("out of range: %d %+v", code, out)
	}
	if code, out := runMetrics(t, scoreSession("s4", 3, 3), "record", "my-post", "--dir", dir); code != 1 || !strings.Contains(diag(t, out.result, "metrics.incomplete-session").Message, "缺少第 3 段") {
		t.Fatalf("incomplete: %d %+v", code, out)
	}
	if code, out := runMetrics(t, "", "record", "my-post", "--file", "nope.yaml", "--dir", dir); code != 1 || out.Session != nil {
		t.Fatalf("missing file: %d %+v", code, out)
	}

	// 正文改变后丢弃旧会话。
	writeFile(t, filepath.Join(dir, ".writing-anything", "works", "my-post", "draft.md"), "第一段。\n\n第二段改了。\n\n第三段。\n")
	if _, out := runMetrics(t, "", "show", "my-post", "--dir", dir); !out.Metrics.Stale || out.Metrics.Items[0].Segments[0].Status != "pending" {
		t.Fatalf("stale: %+v", out.Metrics)
	}
	code, out = runMetrics(t, scoreSession("s9", 1, 1, 1), "record", "my-post", "--dir", dir)
	if d := diag(t, out.result, "metrics.sessions-dropped"); code != 0 || d.Severity != "warning" || !hasNext(d, "wa-cli metrics show my-post") {
		t.Fatalf("dropped: %d %+v", code, out)
	}

	// 不再声明的指标给出 warning。
	writeFile(t, filepath.Join(dir, ".writing-anything", "packs", "house.yaml"), "schema_version: 1\nid: house\ntitle: 本团队规范\n")
	if code, out := runMetrics(t, "", "show", "my-post", "--dir", dir); code != 0 || len(out.Metrics.Items) != 0 || diag(t, out.result, "metrics.unknown-metric").Severity != "warning" {
		t.Fatalf("unknown: %d %+v", code, out)
	}
	writeFile(t, filepath.Join(dir, ".writing-anything", "works", "my-post", "metrics.yaml"), "sessions: [")
	if code, out := runMetrics(t, "", "show", "my-post", "--dir", dir); code != 1 || out.Metrics != nil {
		t.Fatalf("broken: %d %+v", code, out)
	}
}

func TestMetricsCalibrate(t *testing.T) {
	dir := metricsWork(t, "    sessions: 1\n    min_samples: 2\n")
	calibrate := func(paragraph, score string) (int, metricsOut) {
		return runMetrics(t, "", "calibrate", "my-post", "--metric", "tension", "--paragraph", paragraph, "--score", score, "--confirmation", "作者：这段 "+score+" 分", "--dir", dir)
	}
	if code, out := calibrate("1", "3"); code != 1 || !hasNext(diag(t, out.result, "metrics.not-scored"), "wa-cli metrics show my-post") {
		t.Fatalf("not scored: %d %+v", code, out)
	}
	runMetrics(t, scoreSession("s1", 3, 1, 5), "record", "my-post", "--dir", dir)
	code, out := calibrate("1", "3")
	if c := out.Calibration; code != 0 || c.Samples != 1 || c.Agreed != 1 || *c.Rate != 1 || c.Advisory || !slices.Equal(out.ChangedPaths, []string{".writing-anything/calibration.yaml"}) {
		t.Fatalf("first: %d %+v", code, out)
	}
	code, out = calibrate("2", "5")
	if c := out.Calibration; code != 0 || c.Samples != 2 || c.Agreed != 1 || *c.Rate != 0.5 || !c.Advisory || c.Source != "pack:house" {
		t.Fatalf("downgrade: %d %+v", code, out)
	}
	if code, stdout, _ := run(t, "metrics", "calibrate", "my-post", "--metric", "tension", "--paragraph", "2", "--score", "5", "--confirmation", "作者确认", "--dir", dir); code != 0 || !strings.Contains(stdout, "已降级为仅供参考") {
		t.Fatalf("text: %q", stdout)
	}
	// 降级后：plan 与 show 标为仅供参考，record 不再新建分歧决策。
	if _, out := runMetrics(t, "", "plan", "my-post", "--dir", dir); !out.Plan.Metrics[0].Advisory {
		t.Fatalf("plan advisory: %+v", out.Plan.Metrics)
	}
	if code, out := runMetrics(t, scoreSession("s2", 1, 5, 1), "record", "my-post", "--dir", dir); code != 0 || len(out.Decisions) != 0 {
		t.Fatalf("record after downgrade: %d %+v", code, out)
	}
	if _, out := runMetrics(t, "", "show", "my-post", "--dir", dir); !out.Metrics.Items[0].Advisory || out.Metrics.Items[0].Agreement.Samples != 2 {
		t.Fatalf("show advisory: %+v", out.Metrics.Items[0])
	}
	if code, _ := runJSON(t, "validate", ".writing-anything/calibration.yaml", "--dir", dir, "--json"); code != 0 {
		t.Fatalf("validate calibration: %d", code)
	}

	for _, args := range [][]string{
		{"--metric", "tension", "--paragraph", "1", "--score", "3"},
		{"--metric", "tension", "--paragraph", "x", "--score", "3", "--confirmation", "c"},
		{"--metric", "tension", "--paragraph", "0", "--score", "3", "--confirmation", "c"},
		{"--metric", "tension", "--paragraph", "1", "--score", "x", "--confirmation", "c"},
	} {
		if code, out := runMetrics(t, "", append([]string{"calibrate", "my-post", "--dir", dir}, args...)...); code != 1 || !hasNext(diag(t, out.result, "input.invalid-value"), "wa-cli help metrics calibrate") {
			t.Fatalf("%v: %d %+v", args, code, out)
		}
	}
	for args, want := range map[string]string{"pace 1 3": "metrics.unknown-metric", "tension 4 3": "metrics.unknown-paragraph", "tension 1 6": "metrics.score-out-of-range"} {
		f := strings.Fields(args)
		if code, out := runMetrics(t, "", "calibrate", "my-post", "--metric", f[0], "--paragraph", f[1], "--score", f[2], "--confirmation", "c", "--dir", dir); code != 1 || len(diag(t, out.result, want).Next) == 0 {
			t.Fatalf("%s: %d %+v", args, code, out)
		}
	}
}

func TestMetricsWritesNothingOnFailure(t *testing.T) {
	dir := metricsWork(t, "    sessions: 1\n")
	metricsFile := filepath.Join(dir, ".writing-anything", "works", "my-post", "metrics.yaml")
	if code, _ := runMetrics(t, scoreSession("s1", 3, 3), "record", "my-post", "--dir", dir); code != 1 {
		t.Fatal("incomplete session accepted")
	}
	if _, err := os.Stat(metricsFile); err == nil {
		t.Fatal("metrics.yaml created by a failed record")
	}
	// 没有正文时 show 的 segments 为空数组。
	if code, out := runMetrics(t, "", "show", "my-post", "--dir", newWork(t)); code != 0 || out.Metrics.Stale || len(out.Metrics.Items) != 0 {
		t.Fatalf("no metrics: %d %+v", code, out)
	}
	bare := metricsWork(t, "")
	os.Remove(filepath.Join(bare, ".writing-anything", "works", "my-post", "draft.md"))
	code, stdout, _ := run(t, "metrics", "show", "my-post", "--dir", bare, "--json")
	if code != 0 || !strings.Contains(stdout, `"segments":[]`) {
		t.Fatalf("no draft: %d %s", code, stdout)
	}
	// 正文改变后，已有的打分过期：calibrate 报告 metrics.not-scored，不创建 calibration.yaml。
	runMetrics(t, scoreSession("s1", 3, 3, 3), "record", "my-post", "--dir", dir)
	writeFile(t, filepath.Join(dir, ".writing-anything", "works", "my-post", "draft.md"), "改过。\n\n第二段。\n\n第三段。\n")
	if code, out := runMetrics(t, "", "calibrate", "my-post", "--metric", "tension", "--paragraph", "1", "--score", "3", "--confirmation", "作者确认", "--dir", dir); code != 1 || len(diag(t, out.result, "metrics.not-scored").Next) != 2 {
		t.Fatalf("stale calibrate: %d %+v", code, out)
	}
	if _, err := os.Stat(filepath.Join(dir, ".writing-anything", "calibration.yaml")); err == nil {
		t.Fatal("calibration.yaml created by a failed calibrate")
	}
	// metrics.yaml 合法但不是 wa-cli 写出的（会话没有覆盖全部段落）：show 与 calibrate 不崩溃。
	sum := metrics.Digest([]byte(readFile(t, filepath.Join(dir, ".writing-anything", "works", "my-post", "draft.md"))))
	writeFile(t, metricsFile, "schema_version: 1\ndraft_sha256: "+sum+"\nsessions:\n  - {id: a, scores: [{metric: tension, paragraph: 1, score: 3}]}\n")
	if code, out := runMetrics(t, "", "show", "my-post", "--dir", dir); code != 0 || out.Metrics.Items[0].Segments[1].Status != "pending" {
		t.Fatalf("hand-written show: %d %+v", code, out)
	}
	if code, out := runMetrics(t, "", "calibrate", "my-post", "--metric", "tension", "--paragraph", "2", "--score", "3", "--confirmation", "作者确认", "--dir", dir); code != 1 || len(diag(t, out.result, "metrics.not-scored").Next) == 0 {
		t.Fatalf("hand-written calibrate: %d %+v", code, out)
	}
}
