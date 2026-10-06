package cli

import (
	"encoding/json"
	"fmt"
	"os"
	"strings"
	"testing"
)

type curveOut struct {
	result
	Curves []map[string]any `json:"curves"`
	Pacing []map[string]any `json:"pacing"`
}

func runCurveCmd(t *testing.T, dir string, args ...string) (int, curveOut) {
	t.Helper()
	code, res := runWS(t, dir, args...)
	var out curveOut
	if err := json.Unmarshal([]byte(res.stdout), &out); err != nil {
		t.Fatal(err)
	}
	return code, out
}

// storySession 构造一次对 ref 的打分会话：scores 为 metric、paragraph、score 的三元组。
func storySession(id string, scores ...[3]any) string {
	var b strings.Builder
	fmt.Fprintf(&b, "session: %s\nscores:\n", id)
	for _, s := range scores {
		fmt.Fprintf(&b, "  - {metric: %s, paragraph: %d, score: %d}\n", s[0], s[1], s[2])
	}
	return b.String()
}

// recordAll 记录三次取值相同的会话，使各片段 agreed。
func recordAll(t *testing.T, dir, ref, session string) {
	t.Helper()
	for _, id := range []string{"s1", "s2", "s3"} {
		if code, res := runWSIn(t, dir, strings.Replace(session, "session: sX", "session: "+id, 1), "metrics", "record", ref); code != 0 {
			t.Fatalf("record %s: exit code = %d, diagnostics = %+v", id, code, res.Diagnostics)
		}
	}
}

// storyWork 返回一个 short-story 工作区：作品 lamp 的根单元大纲有一节 role 为 setup，两个场景 s1（承担节拍 dim）与 s2。
func storyWork(t *testing.T) string {
	t.Helper()
	dir := t.TempDir()
	if code, res := runWS(t, dir, "init", "--genre", "short-story"); code != 0 {
		t.Fatalf("init: %+v", res.Diagnostics)
	}
	if code, res := runWS(t, dir, "new", "short-story", "lamp"); code != 0 {
		t.Fatalf("new: %+v", res.Diagnostics)
	}
	writeFile(t, wsPath(dir, ".writing-anything/works/lamp/outline.yaml"),
		"schema_version: 1\nsections:\n  - {id: dim, title: 灯变暗, reason: 建立日常与异常, role: setup}\n")
	for _, s := range []string{"s1", "s2"} {
		if code, res := runWS(t, dir, "unit", "add", "lamp", "scene", s); code != 0 {
			t.Fatalf("unit add %s: %+v", s, res.Diagnostics)
		}
	}
	return dir
}

func sceneOutline(targets, beats string) string {
	s := "schema_version: 1\nsections:\n  - {id: act, title: 场景"
	if beats != "" {
		s += ", beats: [" + beats + "]"
	}
	if targets != "" {
		s += ", targets: {" + targets + "}"
	}
	return s + "}\n"
}

// TestCurveContractTarget 覆盖契约声明目标的偏离发现项：value 与 target 都在、相差大于 tolerance 时报告。
func TestCurveContractTarget(t *testing.T) {
	dir := metricsWork(t, "") // house 规范包声明 tension（1 到 5 分、tolerance 1）
	writeFile(t, wsPath(dir, outlineRel), "schema_version: 1\nsections:\n  - {id: why, title: t, reason: r, targets: {tension: 4}}\n")
	recordAll(t, dir, "my-post", storySession("sX", [3]any{"tension", 1, 2}, [3]any{"tension", 2, 2}, [3]any{"tension", 3, 2}))
	code, res := runCurveCmd(t, dir, "curve", "my-post", "--json")
	if code != 0 || !res.Valid || len(res.ChangedPaths) != 0 || len(res.Curves) != 1 {
		t.Fatalf("curve: exit code = %d, result = %+v", code, res)
	}
	c := res.Curves[0]
	if c["metric"] != "tension" || c["advisory"] != false {
		t.Fatalf("curve metric: %v", c)
	}
	p := c["points"].([]any)[0].(map[string]any)
	if p["unit"] != "my-post" || p["value"] != float64(2) || p["target"] != float64(4) {
		t.Fatalf("point: %v", p)
	}
	ds := c["deviations"].([]any)
	if len(ds) != 1 {
		t.Fatalf("deviations: %v", ds)
	}
	d := ds[0].(map[string]any)
	if d["unit"] != "my-post" || d["value"] != float64(2) || d["target"] != float64(4) || d["difference"] != float64(-2) {
		t.Fatalf("deviation: %v", d)
	}
	// 文本图表：字符条形标注值与目标，发现项注明不阻塞验收。
	if _, stdout, _ := run(t, "curve", "my-post", "--dir", dir); !strings.Contains(stdout, "█") || !strings.Contains(stdout, "┆") ||
		!strings.Contains(stdout, "值 2，目标 4") || !strings.Contains(stdout, "偏离发现项（不阻塞验收）") || !strings.Contains(stdout, "偏低") {
		t.Fatalf("text: %q", stdout)
	}
}

// TestCurveMethodDefault 覆盖未声明 targets 时由结构类写作法节拍的目标张力线性映射的目标（three-act 的 setup 为 3，
// 映射 1 + 3 ÷ 10 × 4 = 2.2 后四舍五入为 2），以及声明了 targets 优先于节拍。
func TestCurveMethodDefault(t *testing.T) {
	dir := storyWork(t)
	for _, s := range []string{"s1", "s2"} {
		if err := os.MkdirAll(wsPath(dir, ".writing-anything/works/lamp/units/"+s), 0o755); err != nil {
			t.Fatal(err)
		}
	}
	writeFile(t, wsPath(dir, ".writing-anything/works/lamp/units/s1/outline.yaml"), sceneOutline("", "dim"))
	writeFile(t, wsPath(dir, ".writing-anything/works/lamp/units/s2/outline.yaml"), sceneOutline("tension: 4", "dim"))
	writeFile(t, wsPath(dir, ".writing-anything/works/lamp/units/s1/draft.md"), "第一段，紧张开场。\n")
	writeFile(t, wsPath(dir, ".writing-anything/works/lamp/units/s2/draft.md"), "第一段，接近目标。\n")
	recordAll(t, dir, "lamp/s1", storySession("sX", [3]any{"tension", 1, 4}))
	recordAll(t, dir, "lamp/s2", storySession("sX", [3]any{"tension", 1, 4}))
	code, res := runCurveCmd(t, dir, "curve", "lamp", "--json")
	if code != 0 {
		t.Fatalf("curve: exit code = %d, diagnostics = %+v", code, res.Diagnostics)
	}
	var tension map[string]any
	for _, c := range res.Curves {
		if c["metric"] == "tension" {
			tension = c
		}
	}
	if tension == nil {
		t.Fatalf("curves: %v", res.Curves)
	}
	// 根单元与两个场景按先序共三个点；未打分的根单元 value 为 null。
	points := tension["points"].([]any)
	if len(points) != 3 || points[1].(map[string]any)["unit"] != "lamp/s1" {
		t.Fatalf("points: %v", points)
	}
	if v := points[0].(map[string]any)["value"]; v != nil {
		t.Fatalf("root value: %v", v)
	}
	s1, s2 := points[1].(map[string]any), points[2].(map[string]any)
	if s1["value"] != float64(4) || s1["target"] != float64(2) {
		t.Fatalf("s1: %v", s1) // 节拍 setup 的目标张力 3 映射为 2，|4 − 2| > 1，有偏离发现项
	}
	if s2["value"] != float64(4) || s2["target"] != float64(4) {
		t.Fatalf("s2: %v", s2) // 声明的 targets 优先于节拍，|4 − 4| 不大于 tolerance
	}
	if ds := tension["deviations"].([]any); len(ds) != 1 || ds[0].(map[string]any)["unit"] != "lamp/s1" {
		t.Fatalf("deviations: %v", tension["deviations"])
	}
}

// TestCurveUnscoredAndStale 覆盖会话不足与正文改动后的点值：均为 null，不输出发现项。
func TestCurveUnscoredAndStale(t *testing.T) {
	dir := metricsWork(t, "")
	writeFile(t, wsPath(dir, outlineRel), "schema_version: 1\nsections:\n  - {id: why, title: t, reason: r, targets: {tension: 4}}\n")
	// 只记录两次会话：片段 pending，点值为 null。
	for _, id := range []string{"s1", "s2"} {
		runWSIn(t, dir, storySession(id, [3]any{"tension", 1, 2}, [3]any{"tension", 2, 2}, [3]any{"tension", 3, 2}), "metrics", "record", "my-post")
	}
	code, res := runCurveCmd(t, dir, "curve", "my-post", "--json")
	if code != 0 {
		t.Fatalf("curve: exit code = %d", code)
	}
	p := res.Curves[0]["points"].([]any)[0].(map[string]any)
	if p["value"] != nil || len(res.Curves[0]["deviations"].([]any)) != 0 {
		t.Fatalf("pending point: %v", p)
	}
	// 记录第三次会话后作者修改正文：metrics.yaml 过期，点值仍为 null。
	recordAll(t, dir, "my-post", storySession("sX", [3]any{"tension", 1, 2}, [3]any{"tension", 2, 2}, [3]any{"tension", 3, 2}))
	writeFile(t, wsPath(dir, ".writing-anything/works/my-post/draft.md"), "第一段改了。\n\n第二段。\n\n第三段。\n")
	if code, res = runCurveCmd(t, dir, "curve", "my-post", "--json"); code != 0 {
		t.Fatalf("curve after edit: exit code = %d", code)
	}
	if p = res.Curves[0]["points"].([]any)[0].(map[string]any); p["value"] != nil {
		t.Fatalf("stale point: %v", p)
	}
}

// TestCurvePacing 覆盖文体声明的节奏检查：两个达到 min_score 的片段之间非空白字符数超过上限时输出发现项。
func TestCurvePacing(t *testing.T) {
	dir := storyWork(t)
	long := strings.Repeat("他擦着灯罩，想起白天的对话。", 80) // 14 × 80 = 1120 个非空白字符
	writeFile(t, wsPath(dir, ".writing-anything/works/lamp/draft.md"),
		"第一段，情绪高涨。\n\n"+long+"\n\n"+long+"\n\n第四段，情绪再度高涨。\n")
	// emotion 只有第 1、4 段达到 3 分（节拍点），其间 2 × 1120 = 2240 个非空白字符，超过 2000。
	recordAll(t, dir, "lamp", storySession("sX",
		[3]any{"emotion", 1, 4}, [3]any{"emotion", 2, 1}, [3]any{"emotion", 3, 1}, [3]any{"emotion", 4, 5},
		[3]any{"tension", 1, 2}, [3]any{"tension", 2, 2}, [3]any{"tension", 3, 2}, [3]any{"tension", 4, 2}))
	code, res := runCurveCmd(t, dir, "curve", "lamp", "--json")
	if code != 0 || !res.Valid {
		t.Fatalf("curve: exit code = %d, diagnostics = %+v", code, res.Diagnostics)
	}
	if len(res.Pacing) != 1 {
		t.Fatalf("pacing: %v", res.Pacing)
	}
	f := res.Pacing[0]
	if f["check"] != "emotional-beat-gap" || f["gapChars"] != float64(2240) ||
		f["from"] != "lamp 第 1 段" || f["to"] != "lamp 第 4 段" {
		t.Fatalf("finding: %v", f)
	}
	if _, stdout, _ := run(t, "curve", "lamp", "--dir", dir); !strings.Contains(stdout, "节奏检查发现项（不阻塞验收）") ||
		!strings.Contains(stdout, "2240 个非空白字符，超过允许的 2000") {
		t.Fatalf("text: %q", stdout)
	}
}

// TestCurveNoMetrics 覆盖没有声明任何 AI 评分指标的作品：报告 metrics.none-declared，退出码为 1。
func TestCurveNoMetrics(t *testing.T) {
	dir := newWork(t) // minimal 文体不声明 AI 评分指标
	code, res := runCurveCmd(t, dir, "curve", "my-post", "--json")
	if code != 1 || len(res.Curves) != 0 || len(res.Pacing) != 0 {
		t.Fatalf("curve: exit code = %d, result = %+v", code, res)
	}
	d := diag(t, res.result, "metrics.none-declared")
	if !hasNext(d, "wa-cli config resolved --dir "+dir+" --json") {
		t.Fatalf("diagnostic: %+v", d)
	}
}

// TestPacingInvalidParams 覆盖 resolve 层的节奏检查校验：规范包按 id 覆盖指标、收窄 scale 后，
// short-story 声明的 min_score 越界，config resolved 与 validate 报告 pacing.invalid-params。
func TestPacingInvalidParams(t *testing.T) {
	dir := t.TempDir()
	if code, res := runWS(t, dir, "init", "--genre", "short-story"); code != 0 {
		t.Fatalf("init: %+v", res.Diagnostics)
	}
	if err := os.MkdirAll(wsPath(dir, ".writing-anything/packs"), 0o755); err != nil {
		t.Fatal(err)
	}
	writeFile(t, wsPath(dir, ".writing-anything/packs/override.yaml"),
		"schema_version: 1\nid: override\ntitle: 覆盖指标\nscored_metrics:\n  - id: emotion\n    title: 情绪\n    scale: {min: 1, max: 2}\n"+
			"    anchors:\n      - {score: 1, description: 平静, example: 她喝茶。}\n      - {score: 2, description: 起伏, example: 她放下茶杯。}\n")
	runWS(t, dir, "config", "set", "packs", "override")
	code, res := runWS(t, dir, "config", "resolved", "--json")
	if code != 1 {
		t.Fatalf("config resolved: exit code = %d, diagnostics = %+v", code, res.Diagnostics)
	}
	d := diag(t, res, "pacing.invalid-params")
	if d.Field != "pacing_checks[0].min_score" || !strings.Contains(d.Message, "内置文体 short-story") || !strings.Contains(d.Message, "emotion") {
		t.Fatalf("diagnostic: %+v", d)
	}
	if code, vres := runWS(t, dir, "validate"); code != 1 {
		t.Fatalf("validate: exit code = %d, diagnostics = %+v", code, vres.Diagnostics)
	}
}
