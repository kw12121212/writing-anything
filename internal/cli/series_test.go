package cli

import (
	"encoding/json"
	"fmt"
	"os"
	"path/filepath"
	"slices"
	"strings"
	"testing"

	"github.com/kw12121212/writing-anything/internal/metrics"
)

// seriesWork 返回含作品 part-1、part-2 与 solo 的工作区；part-1 写入正文并有与之一致的验收记录。
func seriesWork(t *testing.T) string {
	t.Helper()
	dir := newWorkspace(t)
	for _, w := range []string{"part-1", "part-2", "solo"} {
		if code, stdout, _ := run(t, "new", "minimal", w, "--dir", dir); code != 0 {
			t.Fatal(stdout)
		}
	}
	acceptedDraft(t, dir, "part-1", "第一篇的正文。\n")
	return dir
}

// acceptedDraft 写入正文，并写出与当前正文一致的验收记录（验收流程本身由 review 主规格覆盖）。
func acceptedDraft(t *testing.T, dir, work, body string) {
	t.Helper()
	writeFile(t, filepath.Join(dir, "in.md"), body)
	if code, stdout, _ := run(t, "draft", "write", work, "--file", "in.md", "--dir", dir); code != 0 {
		t.Fatal(stdout)
	}
	sha := metrics.Digest([]byte(body))
	writeFile(t, filepath.Join(dir, ".writing-anything", "works", work, "acceptance.yaml"),
		"schema_version: 1\ngate_level: fast\ndraft_sha256: "+sha+"\nconfirmed_by_author: false\naccepted_at: '2026-10-02T08:00:00Z'\n")
}

func runSeries(t *testing.T, args ...string) (int, map[string]any) {
	t.Helper()
	code, stdout, _ := run(t, append(args, "--json")...)
	var out map[string]any
	if err := json.Unmarshal([]byte(stdout), &out); err != nil {
		t.Fatalf("%v: %v; %s", args, err, stdout)
	}
	return code, out
}

func codesOf(out map[string]any) []string {
	var cs []string
	for _, d := range out["diagnostics"].([]any) {
		cs = append(cs, d.(map[string]any)["code"].(string))
	}
	return cs
}

func TestSeriesContextAndSummaries(t *testing.T) {
	dir := seriesWork(t)
	writeFile(t, filepath.Join(dir, "s1.md"), "访谈记录")
	run(t, "source", "add", "--type", "interview", "--id", "s1", "--file", "s1.md", "--dir", dir)
	if code, out := runSeries(t, "series", "set", "guide", "--title", "入门指南", "--sources", "s1", "--dir", dir); code != 0 || mustJSON(t, out["changedPaths"]) != `[".writing-anything/series/guide.yaml"]` {
		t.Fatalf("set: %d %v", code, out)
	}
	for _, w := range []string{"part-1", "part-2"} {
		if code, out := runSeries(t, "series", "add", "guide", w, "--dir", dir); code != 0 {
			t.Fatalf("add %s: %v", w, out)
		}
	}
	if code, out := runSeries(t, "series", "fact", "guide", "audience", "--text", "读者是刚入门的后端工程师", "--sources", "s1", "--dir", dir); code != 0 ||
		!strings.Contains(mustJSON(t, out["series"]), `"facts":[{"id":"audience","sources":["s1"],"text":"读者是刚入门的后端工程师"}]`) {
		t.Fatalf("fact: %d %v", code, out)
	}
	// 前序作品还没有摘要：context 给出 warning。
	code, out := runSeries(t, "context", "part-2", "--dir", dir)
	if code != 0 || !strings.Contains(strings.Join(codesOf(out), ","), "context.summary-missing") {
		t.Fatalf("missing summary: %d %v", code, out)
	}
	summary := strings.Repeat("第一篇介绍了规范包的安装与锁定。", 2)
	if code, out := runSeries(t, "summary", "set", "part-1", "--text", summary, "--dir", dir); code != 0 || out["summary"].(map[string]any)["stale"] != false {
		t.Fatalf("summary set: %d %v", code, out)
	}
	code, out = runSeries(t, "context", "part-2", "--dir", dir)
	ctx := mustJSON(t, out["context"])
	if code != 0 || !strings.Contains(ctx, `"series":{"facts":[{"id":"audience"`) || !strings.Contains(ctx, `"id":"guide"`) ||
		!strings.Contains(ctx, `"summaries":[{"stale":false,"text":"`+summary+`","work":"part-1"}],"title":"入门指南"}`) || !strings.Contains(ctx, `"id":"s1"`) {
		t.Fatalf("context: %s", ctx)
	}
	// part-1 自己的 context 没有前序摘要；不属于系列的作品 series 为 null。
	if _, out := runSeries(t, "context", "part-1", "--dir", dir); !strings.Contains(mustJSON(t, out["context"]), `"summaries":[]`) {
		t.Fatalf("first: %v", out["context"])
	}
	if _, out := runSeries(t, "context", "solo", "--dir", dir); !strings.Contains(mustJSON(t, out["context"]), `"series":null`) {
		t.Fatalf("solo: %v", out["context"])
	}
	// 正文被修改后摘要过期：context 与 summary show 都注明。
	writeFile(t, filepath.Join(dir, ".writing-anything", "works", "part-1", "draft.md"), "改过的第一篇。\n")
	if _, out := runSeries(t, "context", "part-2", "--dir", dir); !strings.Contains(mustJSON(t, out["context"]), `"stale":true`) {
		t.Fatalf("stale context: %v", out["context"])
	}
	if code, stdout, _ := run(t, "context", "part-2", "--dir", dir); code != 0 || !strings.Contains(stdout, "- 前序作品 part-1 的摘要（Summary）（已过期：正文在摘要之后被修改）：") ||
		!strings.Contains(stdout, "## 系列（Series）\n\n本作品属于系列 guide（入门指南）") || strings.Count(stdout, "## 系列（Series）") != 1 {
		t.Fatalf("markdown: %s", stdout)
	}
	if code, out := runSeries(t, "summary", "show", "part-1", "--dir", dir); code != 0 || out["summary"].(map[string]any)["stale"] != true {
		t.Fatalf("summary show: %v", out)
	}
	code, out = runSeries(t, "series", "show", "guide", "--dir", dir)
	if code != 0 || mustJSON(t, out["summaries"]) != `[{"stale":true,"text":"`+summary+`","work":"part-1"},{"stale":false,"text":null,"work":"part-2"}]` {
		t.Fatalf("series show: %v", out)
	}
	if code, stdout, _ := run(t, "series", "show", "guide", "--dir", dir); code != 0 || !strings.Contains(stdout, "- part-1（已过期）") || !strings.Contains(stdout, "- part-2：还没有摘要") {
		t.Fatalf("show text: %s", stdout)
	}
	// 裁剪优先级：事实与摘要排在决策之后、规则之前。预算只够放下事实与摘要时，规则被裁剪而它们保留。
	_, full := runSeries(t, "context", "part-2", "--dir", dir)
	var fc struct {
		Context struct {
			Used    int
			Trimmed []struct{ Part string }
		}
	}
	data, _ := json.Marshal(full)
	json.Unmarshal(data, &fc)
	if len(fc.Context.Trimmed) != 0 {
		t.Fatalf("full context trimmed: %+v", fc.Context.Trimmed)
	}

	keep := len([]rune("- 事实 audience：读者是刚入门的后端工程师（素材 s1）\n")) + len([]rune("- 前序作品 part-1 的摘要（Summary）（已过期：正文在摘要之后被修改）：\n\n> "+summary+"\n\n"))
	run(t, "config", "set", "context_budget", fmt.Sprint(keep), "--dir", dir)
	_, small := runSeries(t, "context", "part-2", "--dir", dir)
	data, _ = json.Marshal(small)
	var sc struct {
		Context struct {
			Series struct {
				Facts     []any
				Summaries []any
			}
			Trimmed []struct{ Part, ID string }
		}
	}
	json.Unmarshal(data, &sc)
	var parts []string
	for _, tr := range sc.Context.Trimmed {
		parts = append(parts, tr.Part)
	}
	if len(sc.Context.Series.Facts) != 1 || len(sc.Context.Series.Summaries) != 1 || !slices.Contains(parts, "rules") || slices.Contains(parts, "fact") || slices.Contains(parts, "summary") {
		t.Fatalf("priority: series %+v trimmed %v", sc.Context.Series, parts)
	}
	run(t, "config", "set", "context_budget", "1", "--dir", dir)
	_, tiny := runSeries(t, "context", "part-2", "--dir", dir)
	if ctx := mustJSON(t, tiny["context"]); !strings.Contains(ctx, `"id":"audience","keptChars":0,"part":"fact"`) || !strings.Contains(ctx, `"id":"part-1","keptChars":0,"part":"summary"`) {
		t.Fatalf("trimmed ids: %s", ctx)
	}
	run(t, "config", "set", "context_budget", "", "--dir", dir)
	if code, out := runSeries(t, "validate", "--dir", dir); code != 0 || !strings.Contains(mustJSON(t, out["checked"]), ".writing-anything/series/guide.yaml") || !strings.Contains(mustJSON(t, out["checked"]), "summary.yaml") {
		t.Fatalf("validate: %v", out)
	}
	for _, rel := range []string{".writing-anything/series/guide.yaml", ".writing-anything/works/part-1/summary.yaml"} {
		if code, _ := runSeries(t, "validate", rel, "--dir", dir); code != 0 {
			t.Fatalf("validate %s", rel)
		}
	}
}

func TestSeriesPackInheritance(t *testing.T) {
	dir := seriesWork(t)
	os.MkdirAll(filepath.Join(dir, ".writing-anything", "packs"), 0o755)
	writeFile(t, filepath.Join(dir, ".writing-anything", "packs", "house.yaml"), "schema_version: 1\nid: house\ntitle: 系列规范\nrules:\n  house-length: {type: length-range, severity: error, params: {max: 3}}\n")
	run(t, "series", "set", "guide", "--packs", "house", "--dir", dir)
	run(t, "series", "add", "guide", "part-1", "--dir", dir)
	acceptedDraft(t, dir, "solo", "第一篇的正文。\n")
	_, member := runSeries(t, "lint", "part-1", "--dir", dir)
	_, solo := runSeries(t, "lint", "solo", "--dir", dir)
	if !strings.Contains(mustJSON(t, member["findings"]), `"rule":"house-length"`) || !strings.Contains(mustJSON(t, member["findings"]), `pack:house`) || strings.Contains(mustJSON(t, solo["findings"]), "house-length") {
		t.Fatalf("member %v\nsolo %v", member["findings"], solo["findings"])
	}
	// config resolved 不受系列影响。
	if _, out := runSeries(t, "config", "resolved", "--dir", dir); strings.Contains(mustJSON(t, out["resolved"]), "house-length") {
		t.Fatal("config resolved includes series packs")
	}
}

func TestSeriesErrors(t *testing.T) {
	dir := seriesWork(t)
	if code, out := runSeries(t, "summary", "set", "part-2", "--text", strings.Repeat("字", 30), "--dir", dir); code != 1 || codesOf(out)[0] != "summary.not-accepted" {
		t.Fatalf("not accepted: %v", out)
	}
	if code, out := runSeries(t, "summary", "set", "part-1", "--text", "太短", "--dir", dir); code != 1 || codesOf(out)[0] != "summary.length" || !strings.Contains(mustJSON(t, out), "20 到 500") {
		t.Fatalf("too short: %v", out)
	}
	if code, out := runSeries(t, "summary", "set", "part-1", "--text", " ", "--dir", dir); code != 1 || codesOf(out)[0] != "input.invalid-value" {
		t.Fatalf("blank: %v", out)
	}
	writeFile(t, filepath.Join(dir, "sum.md"), strings.Repeat("从文件读取的摘要。", 3))
	if code, out := runSeries(t, "summary", "set", "part-1", "--file", "sum.md", "--dir", dir); code != 0 {
		t.Fatalf("from file: %v", out)
	}
	if _, out := runSeries(t, "summary", "show", "part-2", "--dir", dir); out["summary"] != nil {
		t.Fatalf("no summary: %v", out)
	}
	run(t, "series", "set", "a", "--dir", dir)
	run(t, "series", "set", "b", "--dir", dir)
	run(t, "series", "add", "a", "part-1", "--dir", dir)
	for args, want := range map[string]string{
		"series add b part-1":                      "series.already-member",
		"series add nope part-1":                   "series.not-found",
		"series add a ghost":                       "work.not-found",
		"series set a --packs nope":                "config.unknown-pack",
		"series set a --sources nope":              "series.unknown-source",
		"series set Bad":                           "input.invalid-value",
		"series fact a f1 --text x --sources nope": "series.unknown-source",
		"series fact a f1":                         "input.invalid-value",
		"series show nope":                         "series.not-found",
	} {
		code, out := runSeries(t, append(strings.Fields(args), "--dir", dir)...)
		if code != 1 || codesOf(out)[0] != want {
			t.Errorf("%s: %d %v", args, code, codesOf(out))
		}
	}
	writeFile(t, filepath.Join(dir, ".writing-anything", "series", "a.yaml"), "schema_version: 1\nid: a\ntitle: A\nworks: [ghost]\npacks: []\nsources: []\nfacts: []\n")
	if code, out := runSeries(t, "validate", "--dir", dir); code != 1 || !strings.Contains(strings.Join(codesOf(out), ","), "work.not-found") {
		t.Fatalf("validate: %v", out)
	}
}

func TestSeriesInheritanceAcrossCommands(t *testing.T) {
	dir := seriesWork(t)
	os.MkdirAll(filepath.Join(dir, ".writing-anything", "packs"), 0o755)
	writeFile(t, filepath.Join(dir, ".writing-anything", "packs", "house.yaml"), metricsPackYAML+"rules:\n  house-length: {type: length-range, severity: warning, params: {max: 3}}\n")
	run(t, "series", "set", "guide", "--packs", "house", "--dir", dir)
	run(t, "series", "add", "guide", "part-1", "--dir", dir)
	acceptedDraft(t, dir, "solo", "第一篇的正文。\n")
	_, member := runSeries(t, "context", "part-1", "--dir", dir)
	_, solo := runSeries(t, "context", "solo", "--dir", dir)
	if !strings.Contains(mustJSON(t, member["context"]), `"id":"house-length"`) || strings.Contains(mustJSON(t, solo["context"]), "house-length") {
		t.Fatalf("context norms: member %v solo %v", member["context"], solo["context"])
	}
	if code, out := runSeries(t, "metrics", "plan", "part-1", "--dir", dir); code != 0 || !strings.Contains(mustJSON(t, out["plan"]), `"source":"pack:house"`) {
		t.Fatalf("metrics member: %d %v", code, out)
	}
	if code, out := runSeries(t, "metrics", "plan", "solo", "--dir", dir); code != 1 || codesOf(out)[0] != "metrics.none-declared" {
		t.Fatalf("metrics solo: %d %v", code, out)
	}
}

func TestSeriesSummaryEdges(t *testing.T) {
	dir := seriesWork(t)
	accFile := filepath.Join(dir, ".writing-anything", "works", "part-1", "acceptance.yaml")
	writeFile(t, accFile, "gate_level: [")
	code, out := runSeries(t, "summary", "set", "part-1", "--text", strings.Repeat("字", 30), "--dir", dir)
	if code != 1 || codesOf(out)[0] != "summary.not-accepted" || !strings.Contains(mustJSON(t, out["diagnostics"]), "wa-cli review show part-1") {
		t.Fatalf("invalid acceptance: %v", out)
	}
	acceptedDraft(t, dir, "part-1", "第一篇的正文。\n")
	writeFile(t, filepath.Join(dir, "s.md"), strings.Repeat("字", 30))
	if code, out := runSeries(t, "summary", "set", "part-1", "--text", "x", "--file", "s.md", "--dir", dir); code != 1 || codesOf(out)[0] != "input.invalid-value" {
		t.Fatalf("text and file: %v", out)
	}
	writeFile(t, filepath.Join(dir, "bad.md"), "摘要\xff\xfe"+strings.Repeat("字", 30))
	if code, out := runSeries(t, "summary", "set", "part-1", "--file", "bad.md", "--dir", dir); code != 1 || codesOf(out)[0] != "input.invalid-value" {
		t.Fatalf("non-UTF-8: %v", out)
	}
	writeFile(t, filepath.Join(dir, ".writing-anything", "works", "part-1", "summary.yaml"), "text: 1\n")
	if code, out := runSeries(t, "summary", "show", "part-1", "--dir", dir); code != 1 || out["summary"] != nil {
		t.Fatalf("invalid summary show: %v", out)
	}
	// 前序作品的摘要不合法：后序作品的 context 照常组装，以 warning 报告。
	run(t, "series", "set", "guide", "--dir", dir)
	run(t, "series", "add", "guide", "part-1", "--dir", dir)
	run(t, "series", "add", "guide", "part-2", "--dir", dir)
	code, out = runSeries(t, "context", "part-2", "--dir", dir)
	if code != 0 || out["context"] == nil || !strings.Contains(mustJSON(t, out["diagnostics"]), `"severity":"warning"`) {
		t.Fatalf("broken preceding summary: %d %v", code, out)
	}
	os.Remove(filepath.Join(dir, ".writing-anything", "works", "part-1", "summary.yaml"))
	_, out = runSeries(t, "context", "part-2", "--dir", dir)
	missing := out["diagnostics"].([]any)[0].(map[string]any)
	if missing["code"] != "context.summary-missing" || missing["severity"] != "warning" || !strings.Contains(mustJSON(t, missing["next"]), "wa-cli help summary set --json") {
		t.Fatalf("summary missing: %v", missing)
	}
}

func TestSeriesSourcesOrderAndRobustness(t *testing.T) {
	dir := seriesWork(t)
	for _, id := range []string{"s1", "s2"} {
		writeFile(t, filepath.Join(dir, id+".md"), "素材 "+id)
		run(t, "source", "add", "--type", "interview", "--id", id, "--file", id+".md", "--dir", dir)
	}
	run(t, "brief", "set", "part-2", "--sources", "s2", "--dir", dir)
	run(t, "series", "set", "guide", "--sources", "s1,s2", "--dir", dir)
	run(t, "series", "add", "guide", "part-2", "--dir", dir)
	_, out := runSeries(t, "context", "part-2", "--dir", dir)
	ctx := mustJSON(t, out["context"])
	if i, j := strings.Index(ctx, `"id":"s2"`), strings.Index(ctx, `"id":"s1"`); i < 0 || j < 0 || i > j || strings.Count(ctx, `"id":"s2"`) != 1 {
		t.Fatalf("source order: %s", ctx)
	}
	// 与作品无关的系列文件坏了：非成员作品照常检查，以 warning 报告；validate 仍按 error 报告。
	writeFile(t, filepath.Join(dir, ".writing-anything", "series", "broken.yaml"), "id: [")
	acceptedDraft(t, dir, "solo", "正文。\n")
	if code, out := runSeries(t, "lint", "solo", "--dir", dir); code != 0 || !strings.Contains(mustJSON(t, out["diagnostics"]), `"severity":"warning"`) {
		t.Fatalf("unrelated broken series: %d %v", code, out)
	}
	if code, _ := runSeries(t, "validate", "--dir", dir); code != 1 {
		t.Fatal("validate passed with a broken series file")
	}
	os.Remove(filepath.Join(dir, ".writing-anything", "series", "broken.yaml"))
	// 系列规范包与文体规则冲突：成员作品的合并问题 validate 也能检出。
	os.MkdirAll(filepath.Join(dir, ".writing-anything", "packs"), 0o755)
	writeFile(t, filepath.Join(dir, ".writing-anything", "packs", "clash.yaml"), "schema_version: 1\nid: clash\ntitle: x\nrules:\n  length: {type: banned-terms}\n")
	run(t, "config", "set", "default_genre", "minimal", "--dir", dir)
	run(t, "series", "set", "guide", "--packs", "clash", "--dir", dir)
	if code, out := runSeries(t, "validate", "--dir", dir); code != 1 || !strings.Contains(strings.Join(codesOf(out), ","), "rule.type-conflict") {
		t.Fatalf("per-work merge: %v", out)
	}
	// 参数问题在修改前报告。
	for args, want := range map[string]string{
		"series set guide --packs default,default":      "input.invalid-value",
		"series set guide --sources s1,s1":              "input.invalid-value",
		"series fact guide f1 --text x --sources s1,s1": "input.invalid-value",
	} {
		if code, out := runSeries(t, append(strings.Fields(args), "--dir", dir)...); code != 1 || codesOf(out)[0] != want || !strings.Contains(mustJSON(t, out["diagnostics"]), "wa-cli help series") {
			t.Errorf("%s: %v", args, out)
		}
	}
	if code, out := runSeries(t, "series", "set", "guide", "--title", "\xff", "--dir", dir); code != 1 || codesOf(out)[0] != "input.invalid-value" {
		t.Fatalf("non-UTF-8 title: %v", out)
	}
}
