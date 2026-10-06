package cli

import (
	"encoding/json"
	"fmt"
	"maps"
	"os"
	"path/filepath"
	"slices"
	"strings"
	"testing"
	"unicode/utf8"
)

type contextOut struct {
	result
	Context map[string]any `json:"context"`
}

func runContextJSON(t *testing.T, dir string, args ...string) (int, contextOut) {
	t.Helper()
	code, res := runWS(t, dir, append([]string{"context"}, args...)...)
	var v any
	json.Unmarshal([]byte(res.stdout), &v)
	contextNoNulls(t, v, "")
	var out contextOut
	if err := json.Unmarshal([]byte(res.stdout), &out); err != nil {
		t.Fatal(err)
	}
	out.result = res
	return code, out
}

// contextNoNulls 断言上下文包输出中只有可为 null 的字段为 null，数组永不为 null（cli-contract/shared-envelope）。
func contextNoNulls(t *testing.T, v any, path string) {
	t.Helper()
	nullable := []string{"context", "brief", "outline", "section", "audience", "purpose", "length", "reason", "title", "excerpt",
		"english", "replacement", "note", "quote", "line", "paragraph", "source", "anchor", "series"}
	switch v := v.(type) {
	case map[string]any:
		for k, c := range v {
			if c == nil && !slices.Contains(nullable, k) && !(k == "id" && strings.HasSuffix(path, ".trimmed[]")) {
				t.Errorf("%s.%s is null", path, k)
			}
			contextNoNulls(t, c, path+"."+k)
		}
	case []any:
		for _, c := range v {
			contextNoNulls(t, c, path+"[]")
		}
	}
}

// contextWork 返回含素材 src-a（原文 note）、src-b，写作简报、大纲与子单元 intro、why、how 的作品 my-post。
func contextWork(t *testing.T, note string) string {
	t.Helper()
	dir := newWork(t)
	if code, stdout, _ := runIn(t, note, "source", "add", "--type", "interview", "--id", "src-a", "--title", "访谈", "--dir", dir); code != 0 {
		t.Fatal(stdout)
	}
	if code, stdout, _ := runIn(t, "资料", "source", "add", "--type", "reference", "--id", "src-b", "--dir", dir); code != 0 {
		t.Fatal(stdout)
	}
	writeFile(t, wsPath(dir, briefRel), completeBrief+"key_messages:\n  - {id: km-1, text: 连接池降低延迟, sources: [src-a]}\nsources: [src-b]\n")
	for _, id := range []string{"intro", "why", "how"} {
		mustRunWS(t, dir, "unit", "add", "my-post", "section", id)
	}
	writeFile(t, wsPath(dir, outlineRel), "schema_version: 1\nsections:\n  - {id: intro, title: 引子, reason: 引出问题}\n  - {id: why, title: 为什么, covers: [km-1], uses: [src-b]}\n  - {id: how, title: 怎么做, reason: 实践}\n")
	return dir
}

func TestContext(t *testing.T) {
	dir := contextWork(t, "作者的原话。")
	before := readFile(t, wsPath(dir, briefRel))
	// context-json
	code, res := runContextJSON(t, dir, "my-post")
	c := res.Context
	if code != 0 || !res.Valid || len(res.ChangedPaths) != 0 || c["unit"] != "my-post" || c["genre"] != "minimal" ||
		get(c, "budget.value") != 12000.0 || get(c, "budget.source") != "genre:minimal" || readFile(t, wsPath(dir, briefRel)) != before {
		t.Fatalf("context-json: exit code = %d, context = %v", code, c)
	}
	// root-sources：写作简报的 sources，然后是核心观点的 sources，再是本单元大纲各节的 uses（去重）。
	// 非 scene 单元没有场景级内容：characters、priorScenes 与 reveal 为空数组（scene-context 的 non-scene-empty）。
	if c["section"] != nil || len(c["neighbors"].([]any)) != 0 || c["outline"] == nil ||
		len(c["characters"].([]any)) != 0 || len(c["priorScenes"].([]any)) != 0 || len(c["reveal"].([]any)) != 0 {
		t.Fatalf("root: context = %v", c)
	}
	var ids []string
	for _, s := range c["sources"].([]any) {
		ids = append(ids, s.(map[string]any)["id"].(string))
	}
	if !slices.Equal(ids, []string{"src-b", "src-a"}) {
		t.Fatalf("root-sources: %v", ids)
	}
	if len(c["trimmed"].([]any)) != 0 || c["used"].(float64) <= 0 {
		t.Fatalf("within-budget: used = %v, trimmed = %v", c["used"], c["trimmed"])
	}
	// context-markdown
	code, stdout, _ := run(t, "context", "my-post", "--dir", dir)
	if code != 0 || !strings.HasPrefix(stdout, "# 上下文包（Context Pack）") || !strings.Contains(stdout, "## 写作简报（Brief）") || !strings.Contains(stdout, "作者的原话。") {
		t.Fatalf("context-markdown: exit code = %d, stdout = %q", code, stdout)
	}

	// inherit-brief 与 neighbors
	os.MkdirAll(wsPath(dir, ".writing-anything/works/my-post/units/how"), 0o755)
	writeFile(t, wsPath(dir, ".writing-anything/works/my-post/units/how/draft.md"), "第一段关于怎么做。\n\n第二段。\n")
	code, res = runContextJSON(t, dir, "my-post/why")
	c = res.Context
	sec := c["section"].(map[string]any)
	if code != 0 || get(c, "brief.unit") != "my-post" || sec["id"] != "why" || sec["keyMessages"].([]any)[0].(map[string]any)["id"] != "km-1" {
		t.Fatalf("inherit-brief: exit code = %d, context = %v", code, c)
	}
	ids = nil
	for _, s := range c["sources"].([]any) {
		ids = append(ids, s.(map[string]any)["id"].(string))
	}
	if !slices.Equal(ids, []string{"src-b", "src-a"}) || c["outline"] != nil {
		t.Fatalf("section sources = %v, outline = %v", ids, c["outline"])
	}
	ns := c["neighbors"].([]any)
	prev, next := ns[0].(map[string]any), ns[1].(map[string]any)
	if len(ns) != 2 || prev["unit"] != "my-post/intro" || prev["position"] != "previous" || prev["excerpt"] != nil || prev["title"] != "引子" ||
		next["unit"] != "my-post/how" || next["position"] != "next" || next["title"] != "怎么做" || next["excerpt"] != "第一段关于怎么做。" {
		t.Fatalf("neighbors = %v", ns)
	}

	// feedback-and-decisions
	runWS(t, dir, "feedback", "add", "my-post", "--text", "多举例")
	runWS(t, dir, "feedback", "add", "my-post", "--text", "已处理")
	runWS(t, dir, "feedback", "resolve", "my-post", "fb-2")
	runWS(t, dir, "decision", "add", "--question", "标题用哪个", "--option", "甲", "--option", "乙", "--unit", "my-post", "--blocking")
	runWS(t, dir, "decision", "add", "--question", "已决定", "--unit", "my-post")
	mustRunWS(t, dir, "decide", "dec-2", "--answer", "是", "--confirmation", "作者同意")
	_, res = runContextJSON(t, dir, "my-post")
	fb, ds := res.Context["feedback"].([]any), res.Context["decisions"].([]any)
	if len(fb) != 1 || fb[0].(map[string]any)["id"] != "fb-1" || len(ds) != 1 || get(ds[0], "id") != "dec-1" || get(ds[0], "blocking") != true || len(get(ds[0], "options").([]any)) != 2 {
		t.Fatalf("feedback-and-decisions: feedback = %v, decisions = %v", fb, ds)
	}

	// norms：被豁免的规则不出现。
	writeFile(t, configPath(dir), readFile(t, configPath(dir))+"waivers:\n  - {rule: length, reason: 本文不限字数}\n")
	_, res = runContextJSON(t, dir, "my-post")
	var rules []string
	for _, r := range get(res.Context, "norms.rules").([]any) {
		rules = append(rules, get(r, "id").(string))
	}
	if slices.Contains(rules, "length") || !slices.Contains(rules, "cjk-typography") || len(get(res.Context, "norms.style").([]any)) == 0 {
		t.Fatalf("norms: rules = %v, norms = %v", rules, res.Context["norms"])
	}
}

func TestContextTrim(t *testing.T) {
	note := strings.Repeat("作者访谈原话：连接池让延迟从 200ms 降到 20ms。", 100)
	dir := contextWork(t, note)
	runWS(t, dir, "feedback", "add", "my-post/why", "--text", "多举例")
	// truncate-source
	mustRunWS(t, dir, "config", "set", "context_budget", "600")
	code, res := runContextJSON(t, dir, "my-post/why")
	c := res.Context
	if code != 0 || c["used"].(float64) > 600 || get(c, "budget.source") != "project" {
		t.Fatalf("truncate-source: exit code = %d, context = %v", code, c)
	}
	var truncated map[string]any
	for _, s := range c["sources"].([]any) {
		if get(s, "truncated") == true {
			truncated = s.(map[string]any)
		}
	}
	var trim map[string]any
	for _, tr := range c["trimmed"].([]any) {
		if get(tr, "part") == "source" && get(tr, "keptChars").(float64) > 0 {
			trim = tr.(map[string]any)
		}
	}
	if truncated == nil || trim == nil || trim["id"] != truncated["id"] || trim["chars"].(float64) <= trim["keptChars"].(float64) || utf8.RuneCountInString(truncated["text"].(string)) >= utf8.RuneCountInString(note) {
		t.Fatalf("truncate-source: sources = %v, trimmed = %v", c["sources"], c["trimmed"])
	}
	// Markdown 中各内容项之和等于 used：去掉单元信息头、各部分标题与裁剪报告后不超过预算。
	_, stdout, _ := run(t, "context", "my-post/why", "--dir", dir)
	if !strings.Contains(stdout, "## 被裁剪的内容") || !strings.Contains(stdout, "截断为") {
		t.Fatalf("markdown trim report: %q", stdout)
	}

	// drop-low-priority：预算只放得下写作简报。
	mustRunWS(t, dir, "config", "set", "context_budget", "120")
	_, res = runContextJSON(t, dir, "my-post/why")
	c = res.Context
	parts := map[string]bool{}
	for _, tr := range c["trimmed"].([]any) {
		if get(tr, "keptChars").(float64) != 0 {
			t.Fatalf("drop-low-priority: %v", tr)
		}
		parts[get(tr, "part").(string)] = true
	}
	if c["brief"] == nil || c["section"] != nil || len(c["feedback"].([]any)) != 0 || len(c["neighbors"].([]any)) != 0 || len(get(c, "norms.rules").([]any)) != 0 ||
		!parts["feedback"] || !parts["neighbor"] || !parts["rules"] || !parts["section"] || c["used"].(float64) > 120 {
		t.Fatalf("drop-low-priority: context = %v", c)
	}
	// 预算为 1 时连写作简报也被裁剪，单元信息头照常输出。
	mustRunWS(t, dir, "config", "set", "context_budget", "1")
	if _, res = runContextJSON(t, dir, "my-post"); res.Context["brief"] != nil || res.Context["used"].(float64) != 0 {
		t.Fatalf("tiny budget: %v", res.Context)
	}
}

func TestContextErrors(t *testing.T) {
	dir := contextWork(t, "原话")
	// context-invalid-brief
	writeFile(t, wsPath(dir, briefRel), "k: v\n")
	if code, res := runContextJSON(t, dir, "my-post"); code != 1 || res.Context != nil || diag(t, res.result, "artifact.schema-violation").Path != briefRel {
		t.Fatalf("invalid brief: exit code = %d", code)
	}
	if code, stdout, _ := run(t, "context", "my-post", "--dir", dir); code != 1 || strings.Contains(stdout, "# 上下文包") {
		t.Fatalf("invalid brief text: exit code = %d, stdout = %q", code, stdout)
	}
	writeFile(t, wsPath(dir, briefRel), completeBrief)
	for _, tt := range []struct{ rel, content, code string }{
		{outlineRel, "schema_version: 1\nsections: {}\n", "artifact.schema-violation"},
		{".writing-anything/works/my-post/units/why/feedback.yaml", "k: v\n", "artifact.schema-violation"},
		{".writing-anything/decisions.yaml", "decisions: [\n", "artifact.parse-error"},
		{".writing-anything/works/my-post/units/why/brief.yaml", "k: v\n", "artifact.schema-violation"},
	} {
		os.MkdirAll(wsPath(dir, tt.rel[:strings.LastIndex(tt.rel, "/")]), 0o755)
		old, had := os.ReadFile(wsPath(dir, tt.rel))
		writeFile(t, wsPath(dir, tt.rel), tt.content)
		if code, res := runContextJSON(t, dir, "my-post/why"); code != 1 || diag(t, res.result, tt.code).Path != tt.rel {
			t.Fatalf("%s: exit code = %d", tt.rel, code)
		}
		if had == nil {
			writeFile(t, wsPath(dir, tt.rel), string(old))
		} else {
			os.Remove(wsPath(dir, tt.rel))
		}
	}
	writeFile(t, configPath(dir), readFile(t, configPath(dir))+"packs: [nope]\n")
	if code, _ := runContextJSON(t, dir, "my-post"); code != 1 {
		t.Fatalf("invalid config: exit code = %d", code)
	}
	if code, res := runContextJSON(t, dir, "my-post/nope"); code != 1 || diag(t, res.result, "unit.not-found").Code == "" {
		t.Fatalf("unknown unit: exit code = %d", code)
	}
}

// 非 UTF-8 的素材原文不放入上下文包；config set 与 config resolved 的上下文预算（context-budget）。
func TestContextBudgetSetting(t *testing.T) {
	dir := newWork(t)
	if code, _, _ := runIn(t, "\xff\xfe", "source", "add", "--type", "reference", "--id", "bin", "--dir", dir); code != 0 {
		t.Fatal("source add")
	}
	writeFile(t, wsPath(dir, briefRel), completeBrief+"sources: [bin]\n")
	if _, res := runContextJSON(t, dir, "my-post"); len(res.Context["sources"].([]any)) != 0 {
		t.Fatalf("binary source: %v", res.Context["sources"])
	}
	// work-genre：未设置 default_genre 时使用作品的文体。
	if _, res := runContextJSON(t, dir, "my-post"); get(res.Context, "budget.source") != "genre:minimal" {
		t.Fatalf("work-genre: %v", res.Context["budget"])
	}
	// default-budget 与 resolved-budget
	_, res := runWS(t, dir, "config", "resolved")
	if r := res.resolved(t); get(r, "contextBudget.value") != 12000.0 || get(r, "contextBudget.source") != "default" {
		t.Fatalf("default-budget: %v", r["contextBudget"])
	}
	mustRunWS(t, dir, "config", "set", "default_genre", "minimal")
	if _, res := runWS(t, dir, "config", "resolved"); get(res.resolved(t), "contextBudget.source") != "genre:minimal" {
		t.Fatalf("resolved-budget")
	}
	// set-context-budget 与 project-overrides
	code, res := runWS(t, dir, "config", "set", "context_budget", "5000")
	if code != 0 || !strings.Contains(readFile(t, configPath(dir)), "context_budget: 5000") || get(res.config(t), "contextBudget") != 5000.0 {
		t.Fatalf("set-context-budget: exit code = %d, config = %v", code, res.config(t))
	}
	if _, res := runWS(t, dir, "config", "resolved"); get(res.resolved(t), "contextBudget.value") != 5000.0 || get(res.resolved(t), "contextBudget.source") != "project" {
		t.Fatalf("project-overrides")
	}
	if _, stdout, _ := run(t, "status", "--dir", dir); !strings.Contains(stdout, "上下文预算（Context Budget）：5000 字符") {
		t.Fatalf("status text: %q", stdout)
	}
	if _, stdout, _ := run(t, "config", "resolved", "--dir", dir); !strings.Contains(stdout, "上下文预算（Context Budget）：5000 字符（来自 project）") {
		t.Fatalf("resolved text: %q", stdout)
	}
	// set-bad-context-budget
	before := readFile(t, configPath(dir))
	for _, v := range []string{"0", "abc", "1.5"} {
		code, res := runWS(t, dir, "config", "set", "context_budget", v)
		if code != 1 || diag(t, res, "artifact.schema-violation").Field != "context_budget" || readFile(t, configPath(dir)) != before {
			t.Fatalf("set-bad-context-budget %q: exit code = %d", v, code)
		}
	}
	if code, res := runWS(t, dir, "config", "set", "context_budget", ""); code != 0 || res.config(t)["contextBudget"] != nil {
		t.Fatalf("clear: exit code = %d", code)
	}
}

// snapshot 返回工作区内每个文件的内容与修改时间，用于断言命令只读。
func snapshot(t *testing.T, dir string) map[string]string {
	t.Helper()
	out := map[string]string{}
	filepath.WalkDir(dir, func(p string, d os.DirEntry, err error) error {
		if err != nil || d.IsDir() {
			return err
		}
		info, _ := d.Info()
		data, _ := os.ReadFile(p)
		out[p] = info.ModTime().String() + string(data)
		return nil
	})
	return out
}

// 子单元有自己的写作简报时，本节素材仍取自父单元写作简报中被覆盖的核心观点；只读；合并问题的复查命令。
func TestContextSectionSourcesAndReadOnly(t *testing.T) {
	dir := contextWork(t, "原话")
	os.MkdirAll(wsPath(dir, ".writing-anything/works/my-post/units/why"), 0o755)
	writeFile(t, wsPath(dir, ".writing-anything/works/my-post/units/why/brief.yaml"), "schema_version: 1\nkey_messages:\n  - {id: km-9, text: 子单元观点, sources: [src-b]}\n")
	before := snapshot(t, dir)
	code, res := runContextJSON(t, dir, "my-post/why")
	var ids []string
	for _, s := range res.Context["sources"].([]any) {
		ids = append(ids, get(s, "id").(string))
	}
	if code != 0 || get(res.Context, "brief.unit") != "my-post/why" || !slices.Equal(ids, []string{"src-b", "src-a"}) {
		t.Fatalf("section sources: exit code = %d, sources = %v", code, ids)
	}
	if after := snapshot(t, dir); !maps.Equal(before, after) {
		t.Fatal("context modified the workspace")
	}
	// Markdown 头中的已用字符数与 JSON 的 used 一致，且不超过预算。
	_, stdout, _ := run(t, "context", "my-post/why", "--dir", dir)
	if used := res.Context["used"].(float64); !strings.Contains(stdout, fmt.Sprintf("已用 %d 字符", int(used))) || used > 12000 {
		t.Fatalf("used = %v, markdown = %q", used, stdout)
	}
	// 改写过的素材原文以 warning 报告，不放入上下文包。
	writeFile(t, wsPath(dir, ".writing-anything/sources/src-a/original.txt"), "被篡改的原话")
	code, res = runContextJSON(t, dir, "my-post/why")
	if d := diag(t, res.result, "source.modified"); code != 0 || !res.Valid || d.Severity != "warning" || len(res.Context["sources"].([]any)) != 1 {
		t.Fatalf("modified source: exit code = %d, result = %+v", code, res)
	}
	// 规则合并问题取决于作品的文体，next 用 context 复查。
	writePack(t, dir, "house", "schema_version: 1\nid: house\ntitle: 团队\nrules:\n  length:\n    type: ai-cliche\n")
	mustRunWS(t, dir, "config", "set", "packs", "house")
	code, res = runContextJSON(t, dir, "my-post")
	if d := diag(t, res.result, "rule.type-conflict"); code != 1 || !hasNext(d, "wa-cli context my-post") {
		t.Fatalf("type conflict: exit code = %d, result = %+v", code, res)
	}
}

// 相邻节的正文与素材原文途经符号链接时不读取项目目录之外的文件（validated-path-segments）。
func TestContextSymlinks(t *testing.T) {
	dir := contextWork(t, "原话")
	outDir := t.TempDir()
	writeFile(t, filepath.Join(outDir, "draft.md"), "外部正文\n")
	os.MkdirAll(wsPath(dir, ".writing-anything/works/my-post/units"), 0o755)
	if err := os.Symlink(outDir, wsPath(dir, ".writing-anything/works/my-post/units/how")); err != nil {
		t.Skip("symlinks unavailable:", err)
	}
	outside := filepath.Join(outDir, "original.txt")
	writeFile(t, outside, "原话")
	orig := wsPath(dir, ".writing-anything/sources/src-a/original.txt")
	os.Remove(orig)
	if err := os.Symlink(outside, orig); err != nil {
		t.Fatal(err)
	}
	code, res := runContextJSON(t, dir, "my-post/why")
	next := res.Context["neighbors"].([]any)[1]
	if d := diag(t, res.result, "context.source-skipped"); code != 0 || get(next, "excerpt") != nil || d.Severity != "warning" || !hasNext(d, "wa-cli source list") || !hasNext(d, "wa-cli context my-post/why") {
		t.Fatalf("exit code = %d, next = %v, diagnostics = %+v", code, next, res.Diagnostics)
	}
	for _, s := range res.Context["sources"].([]any) {
		if get(s, "id") == "src-a" {
			t.Fatal("read the symlinked source original")
		}
	}
}
