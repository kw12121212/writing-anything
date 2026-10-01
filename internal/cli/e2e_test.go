package cli

import (
	"encoding/json"
	"os"
	"path/filepath"
	"strings"
	"testing"
	"time"
)

// fixture 读取 testdata/e2e/ 下的测试夹具。
func fixture(t *testing.T, name string) string {
	t.Helper()
	data, err := os.ReadFile(filepath.Join("testdata", "e2e", name))
	if err != nil {
		t.Fatal(err)
	}
	return string(data)
}

// 端到端：用测试夹具中的作者素材，按 tech-blog 文体从写作简报走到 Markdown 导出（tech-blog-e2e）。
func TestTechBlogEndToEnd(t *testing.T) {
	dir := t.TempDir()
	now = func() time.Time { return time.Date(2026, 10, 1, 9, 30, 0, 0, time.UTC) }
	defer func() { now = time.Now }()
	step := func(input string, args ...string) result {
		t.Helper()
		code, res := runWSIn(t, dir, input, args...)
		if code != 0 || !res.Valid {
			t.Fatalf("%v: exit code = %d, diagnostics = %+v", args, code, res.Diagnostics)
		}
		return res
	}
	step("", "init", "--genre", "tech-blog", "--gate-level", "strict")
	step("", "new", "tech-blog", "pool")
	for _, s := range [][]string{{"note", "author-note", "author-note.md"}, {"talk", "interview", "interview.md"}, {"go-docs", "reference", "go-docs.md"}} {
		step(fixture(t, s[2]), "source", "add", "--type", s[1], "--id", s[0], "--title", s[2])
	}
	// 写作简报：受众、目的与有出处的核心观点；默认写作法为金字塔原理与 SCQA。
	step("", "brief", "set", "pool", "--audience", "后端工程师", "--purpose", "讲清楚怎样用连接复用降低接口延迟", "--sources", "note,talk,go-docs")
	step("", "brief", "set", "pool", "--message", "reuse", "--message-text", "复用连接是降低延迟最直接的办法", "--message-sources", "note,go-docs")
	step("", "brief", "set", "pool", "--message", "body", "--message-text", "读完并关闭响应体，连接才能回到连接池", "--message-sources", "talk,go-docs")
	step("", "advance", "pool")
	// 大纲：按写作法的五个章节组织，覆盖每个核心观点。
	for _, s := range [][]string{
		{"bg", "背景", "situation", "--reason", "交代订单服务的现状"},
		{"challenge", "挑战", "complication", "--reason", "说明新建连接的代价"},
		{"question", "问题", "question", "--reason", "提出要回答的问题"},
		{"answer", "结论", "answer", "--covers", "reuse"},
		{"how", "做法", "support", "--covers", "body"},
	} {
		step("", "outline", "set", "pool", s[0], "--title", s[1], "--role", s[2], s[3], s[4])
	}
	step("", "advance", "pool")
	// 草稿：亲历、数据与版本号都登记为论断并关联对应类型的素材，文本检查没有 error 级发现项。
	step(fixture(t, "draft.md"), "draft", "write", "pool")
	step("", "claim", "add", "pool", "--kind", "experience", "--text", "我曾经负责这个服务的性能优化，压测时发现大量时间花在建立连接上", "--sources", "note")
	step("", "claim", "add", "pool", "--kind", "data", "--text", "接口的 P99 延迟降低了 40%", "--sources", "note")
	step("", "claim", "add", "pool", "--kind", "fact", "--text", "Go 1.22 的 http.Client 会缓存连接，前提是读完并关闭响应体", "--sources", "go-docs")
	code, lint := runWSIn(t, dir, "", "lint", "pool")
	var lr struct {
		Findings []map[string]any `json:"findings"`
	}
	if err := json.Unmarshal([]byte(lint.stdout), &lr); err != nil || lr.Findings == nil {
		t.Fatalf("lint output: %v %s", err, lint.stdout)
	}
	for _, f := range lr.Findings {
		if f["severity"] == "error" || f["rule"] == "references" || f["rule"] == "claim-candidates" {
			t.Errorf("lint finding: %v", f)
		}
	}
	if code != 0 {
		t.Fatalf("lint: exit code = %d, %+v", code, lint.Diagnostics)
	}
	step("", "advance", "pool")
	// 审稿与验收：strict 关卡下处理修订任务并记录作者确认。
	step(fixture(t, "review.yaml"), "review", "record", "pool")
	step("", "review", "resolve", "pool", "rf-1", "--status", "waived", "--note", "作者：压测条件放在附录")
	if code, res := runWS(t, dir, "accept", "pool"); code != 1 || diag(t, res, "accept.unconfirmed").Severity != "error" {
		t.Fatalf("strict accept without confirmation: %+v", res.Diagnostics)
	}
	step("", "accept", "pool", "--confirmed-by-author", "--confirmation", "作者：可以发布")
	if res := step("", "advance", "pool"); !strings.Contains(res.stdout, `"stage":"done"`) {
		t.Fatalf("advance to done: %s", res.stdout)
	}
	// 导出：只导出已验收的单元，附带 front matter。
	res := step("", "export", "pool", "--front-matter")
	var er struct {
		Export map[string]any `json:"export"`
	}
	if err := json.Unmarshal([]byte(res.stdout), &er); err != nil {
		t.Fatal(err)
	}
	if er.Export["path"] != ".writing-anything/exports/pool.md" || len(er.Export["units"].([]any)) != 1 {
		t.Fatalf("export: %+v", er.Export)
	}
	got := readFile(t, filepath.Join(dir, ".writing-anything", "exports", "pool.md"))
	if os.Getenv("UPDATE_GOLDEN") != "" {
		writeFile(t, filepath.Join("testdata", "e2e", "export.md"), got)
	}
	if want := fixture(t, "export.md"); got != want {
		t.Fatalf("export.md =\n%s\nwant:\n%s", got, want)
	}
	if code, res := runWS(t, dir, "validate"); code != 0 {
		t.Fatalf("validate after export: %+v", res.Diagnostics)
	}
}

// export：只导出已验收的单元，跳过的单元以 warning 报告；没有已验收单元、输出路径不合法、格式不支持或给出子单元时失败。
func TestExportEdges(t *testing.T) {
	dir := reviewWork(t)
	mustRunWS(t, dir, "config", "set", "gate_level", "fast")
	if code, res := runWS(t, dir, "export", "my-post"); code != 1 || !hasNext(diag(t, res, "export.nothing-accepted"), "wa-cli next") ||
		!hasNext(diag(t, res, "export.unaccepted"), "wa-cli review show my-post") {
		t.Fatalf("nothing accepted: %+v", res.Diagnostics)
	}
	mustRunWS(t, dir, "accept", "my-post")
	mustRunWS(t, dir, "unit", "add", "my-post", "section", "intro")
	code, res := runWS(t, dir, "export", "my-post", "--output", "dist/post.md")
	if d := diag(t, res, "export.unaccepted"); code != 0 || d.Severity != "warning" || d.Path != ".writing-anything/works/my-post/units/intro" ||
		!hasNext(d, "wa-cli review show my-post/intro") || res.ChangedPaths[0] != "dist" || res.ChangedPaths[1] != "dist/post.md" {
		t.Fatalf("export with skipped unit: code = %d, %+v %v", code, res.Diagnostics, res.ChangedPaths)
	}
	// 正文由 Agent 写入：文末带 AI 参与说明（ai-disclosure）。
	if got := readFile(t, filepath.Join(dir, "dist", "post.md")); got != reviewDraft+"\n---\n\n"+fullAIDisclosure+"\n" {
		t.Fatalf("export without front matter = %q", got)
	}
	for _, tc := range []struct {
		args []string
		code string
	}{
		{[]string{"export", "my-post", "--output", "../x.md"}, "export.invalid-output"},
		{[]string{"export", "my-post", "--format", "html"}, "input.invalid-value"},
		{[]string{"export", "my-post", "--output", ".writing-anything/config.yaml"}, "export.invalid-output"},
		{[]string{"export", "my-post", "--output", ".writing-anything/works/my-post/draft.md"}, "export.invalid-output"},
		{[]string{"export", "my-post/intro"}, "work.not-found"},
	} {
		if code, res := runWS(t, dir, tc.args...); code != 1 || len(diag(t, res, tc.code).Next) == 0 {
			t.Errorf("%v: code = %d, %+v", tc.args, code, res.Diagnostics)
		}
	}
	if code, stdout, _ := run(t, "export", "my-post", "--dir", dir); code != 0 || !strings.Contains(stdout, "已导出作品 my-post 的 1 个已验收单元") {
		t.Errorf("text: %q", stdout)
	}
}

// 内置 tech-blog 文体（tech-blog-genre）；导出去掉写作过程的注释（strip-process-comments）。
func TestTechBlogGenreAndStrip(t *testing.T) {
	dir := t.TempDir()
	mustRunWS(t, dir, "init", "--genre", "tech-blog")
	code, res := runWS(t, dir, "new", "tech-blog", "post")
	if code != 0 || !strings.Contains(readFile(t, workFile(dir, "post")), "type: article") {
		t.Fatalf("new tech-blog: %+v", res.Diagnostics)
	}
	_, res = runWS(t, dir, "config", "resolved")
	for _, want := range []string{`"code-language"`, `"links"`, `"references"`, `"id":"accuracy"`, `"blocking":true`} {
		if !strings.Contains(res.stdout, want) {
			t.Errorf("resolved lacks %s", want)
		}
	}
	dir = reviewWork(t)
	mustRunWS(t, dir, "config", "set", "gate_level", "fast")
	writeFile(t, filepath.Join(dir, ".writing-anything", "works", "my-post", "draft.md"), "<!-- lint-ignore ai-cliche: 引用原话 -->\n第一段。<!-- @me: 太长 -->\n")
	mustRunWS(t, dir, "accept", "my-post")
	mustRunWS(t, dir, "export", "my-post")
	if got := readFile(t, filepath.Join(dir, ".writing-anything", "exports", "my-post.md")); got != "第一段。\n\n---\n\n"+fullAIDisclosure+"\n" {
		t.Fatalf("stripped export = %q", got)
	}
}

// fullAIDisclosure 是全部段落出自 Agent（或 Agent 起草、作者改过）时的内置 AI 参与说明。
const fullAIDisclosure = "本文在 AI 辅助下完成：约 100% 的内容由 AI 起草，均经作者审阅确认。"
