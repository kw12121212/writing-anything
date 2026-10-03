package cli

import (
	"encoding/json"
	"fmt"
	"os"
	"path/filepath"
	"slices"
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

// 端到端：short-story 文体。前提与关键转折关联作者素材，场景以情节节拍与轻量场景契约（Scene Contract）推进，
// 前提或关键转折没有出处时 advance 被阻塞（short-story-genre、brief-gate、outline-artifact）。
func TestShortStoryEndToEnd(t *testing.T) {
	dir := t.TempDir()
	step := func(input string, args ...string) result {
		t.Helper()
		code, res := runWSIn(t, dir, input, args...)
		if code != 0 || !res.Valid {
			t.Fatalf("%v: exit code = %d, diagnostics = %+v", args, code, res.Diagnostics)
		}
		return res
	}
	fail := func(code string, args ...string) Diagnostic {
		t.Helper()
		c, res := runWS(t, dir, args...)
		if c != 1 {
			t.Fatalf("%v: exit code = %d, diagnostics = %+v", args, c, res.Diagnostics)
		}
		return diag(t, res, code)
	}
	step("", "init", "--genre", "short-story")
	step("", "new", "short-story", "lamp")
	// new-short-story：根单元类型为 story，简报必填项与解析出的规则、评分标准符合文体声明（含三条小说专用规则）。
	if code, res := runWS(t, dir, "config", "resolved"); code != 0 {
		t.Fatalf("resolved: %+v", res.Diagnostics)
	} else {
		for _, want := range []string{`"id":"short-story"`, `"briefRequired":["premise","pov","length","key_messages"]`, `"id":"conflict"`, `"id":"pacing"`, `"id":"motivation"`,
			`"fiction-cliches"`, `"dialogue-punctuation"`, `"pov-consistency"`, `"眼底闪过一丝"`} {
			if !strings.Contains(res.stdout, want) {
				t.Errorf("resolved lacks %s", want)
			}
		}
		if strings.Contains(res.stdout, `"claims"`) {
			t.Error("resolved declares claim rules for fiction")
		}
	}
	step("守灯人笔记：灯里的精灵每撒一次谎，灯就暗一分；它说它说的是真话。", "source", "add", "--type", "author-note", "--id", "idea", "--title", "前提笔记")
	// 人物卡（Character Sheet）：场景上下文按 name 匹配设定集中的 character 条目。
	step("", "canon", "add", "lamp", "--type", "character", "--name", "守灯人", "--text", "五十岁，左手有旧伤，守灯三十年")
	// 写作简报：前提与视角先不关联素材，关键转折（核心观点）也没有出处。
	step("", "brief", "set", "lamp", "--premise", "灯里的精灵每撒一次谎，灯就暗一分", "--theme", "善意的谎言",
		"--pov-person", "third", "--pov-character", "守灯人", "--length", "2000-8000",
		"--message", "turn-1", "--message-text", "守灯人发现谎言与灯焰的联系")
	d := fail("brief.unsourced-premise", "advance", "lamp")
	if d.Path != ".writing-anything/works/lamp/brief.yaml" || !hasNext(d, "wa-cli help brief set --json") ||
		!hasNext(d, "wa-cli brief show lamp --dir "+dir+" --json") {
		t.Fatalf("unsourced premise: %+v", d)
	}
	if _, res := runWS(t, dir, "next", "lamp"); !strings.Contains(res.stdout, `"brief.unsourced-premise"`) ||
		!strings.Contains(res.stdout, `"brief.unsourced-message"`) {
		t.Fatalf("next: %s", res.stdout)
	}
	// 只关联未决定决策的前提阻塞推进，下一步命令指向决策队列。
	step("", "decision", "add", "--question", "这个前提是你的设想吗", "--unit", "lamp", "--blocking")
	step("", "brief", "set", "lamp", "--premise-decision", "dec-1")
	if d := fail("brief.unconfirmed-premise", "advance", "lamp"); !hasNext(d, "wa-cli decisions --dir "+dir+" --json") {
		t.Fatalf("unconfirmed premise: %+v", d)
	}
	step("", "decide", "dec-1", "--answer", "是", "--confirmation", "作者：前提出自我的笔记")
	// 关键转折（核心观点）仍没有出处，关联素材后推进。
	step("", "brief", "set", "lamp", "--message", "turn-1", "--message-sources", "idea")
	if res := step("", "advance", "lamp"); !strings.Contains(res.stdout, `"stage":"outline"`) {
		t.Fatalf("advance to outline: %s", res.stdout)
	}
	// 故事大纲：节即情节节拍（Story Beat），节拍覆盖关键转折。
	step("", "outline", "set", "lamp", "dim", "--title", "灯变暗", "--reason", "建立日常与异常")
	step("", "outline", "set", "lamp", "clash", "--title", "灯下对峙", "--covers", "turn-1")
	step("", "advance", "lamp")
	// 场景：自己的大纲声明场景契约（出场人物、承担的节拍、揭示边界）。
	step("", "unit", "add", "lamp", "scene", "scene-1")
	step("", "brief", "set", "lamp/scene-1", "--purpose", "守灯人与精灵在灯下对峙")
	step("", "advance", "lamp/scene-1")
	step("", "outline", "set", "lamp/scene-1", "scene-1", "--title", "灯下对峙", "--characters", "守灯人,精灵",
		"--beats", "clash", "--reveal", "精灵的真实身份", "--reason", "场景契约：承担 clash 节拍，不透露精灵身份")
	// outline show 的 --json 输出契约字段，人读文本同样列出（show-contract）。
	if code, res := runWS(t, dir, "outline", "show", "lamp/scene-1"); code != 0 ||
		!strings.Contains(res.stdout, `"characters":["守灯人","精灵"]`) || !strings.Contains(res.stdout, `"beats":["clash"]`) ||
		!strings.Contains(res.stdout, `"reveal":"精灵的真实身份"`) {
		t.Fatalf("outline show contract: exit code = %d, %s", code, res.stdout)
	}
	if code, stdout, _ := run(t, "outline", "show", "lamp/scene-1", "--dir", dir); code != 0 ||
		!strings.Contains(stdout, "出场人物 守灯人、精灵") || !strings.Contains(stdout, "承担节拍 clash") || !strings.Contains(stdout, "不提前透露：精灵的真实身份") {
		t.Fatalf("outline show text: %q", stdout)
	}
	if d := fail("outline.unknown-beat", "outline", "set", "lamp/scene-1", "scene-1", "--beats", "nope"); d.Field != "beats[0]" {
		t.Fatalf("unknown beat: %+v", d)
	}
	if code, res := runWS(t, dir, "validate"); code != 0 {
		t.Fatalf("validate with contract: %+v", res.Diagnostics)
	}
	step("", "advance", "lamp/scene-1")
	// 场景草稿与审稿：虚构内容不做论断出处检查（没有 claim 类发现项）。
	scene := "守灯人把油芯拧亮，问精灵为什么灯又暗了。\n\n精灵说它从不说谎，可灯焰抖了一下。\n\n守灯人想起笔记里那句话，没有拆穿。"
	step(scene, "draft", "write", "lamp/scene-1")
	code, lint := runWS(t, dir, "lint", "lamp/scene-1")
	var lr struct {
		Findings []map[string]any `json:"findings"`
	}
	if err := json.Unmarshal([]byte(lint.stdout), &lr); err != nil || lr.Findings == nil {
		t.Fatalf("lint output: %v %s", err, lint.stdout)
	}
	for _, f := range lr.Findings {
		if f["severity"] == "error" || strings.HasPrefix(fmt.Sprint(f["rule"]), "claim") {
			t.Errorf("lint finding: %v", f)
		}
	}
	if code != 0 {
		t.Fatalf("lint: exit code = %d, %+v", code, lint.Diagnostics)
	}
	step("", "advance", "lamp/scene-1")
	step(fixture(t, "story-review.yaml"), "review", "record", "lamp/scene-1")
	step("", "accept", "lamp/scene-1", "--confirmed-by-author", "--confirmation", "作者：场景可以定稿")
	if res := step("", "advance", "lamp/scene-1"); !strings.Contains(res.stdout, `"stage":"done"`) {
		t.Fatalf("scene to done: %s", res.stdout)
	}
	// 已验收场景的摘要：第二个场景的上下文包会带上它作为前情（scene-context）。
	step("", "summary", "set", "lamp/scene-1", "--text", "守灯人发现灯焰随精灵的谎言变暗，想起笔记里的话，决定暂不拆穿。")
	// 第二个场景：先写违例文本夹具，断言三条小说规则的发现项后换回合规文本。
	step("", "unit", "add", "lamp", "scene", "scene-2")
	step("", "brief", "set", "lamp/scene-2", "--purpose", "守灯人按住精灵的手")
	step("", "advance", "lamp/scene-2")
	step("", "outline", "set", "lamp/scene-2", "scene-2", "--title", "按住手", "--characters", "守灯人,精灵",
		"--beats", "clash", "--reveal", "精灵的身世", "--reason", "场景契约：承担 clash 节拍，不透露身世")
	// 场景级上下文包：出场人物的当前设定、前序场景摘要与揭示边界；精灵没有同名条目，以 warning 提示。
	code, ctx := runContextJSON(t, dir, "lamp/scene-2")
	if code != 0 {
		t.Fatalf("context scene-2: %+v", ctx.Diagnostics)
	}
	cs := ctx.Context
	if chars := cs["characters"].([]any); len(chars) != 1 || chars[0].(map[string]any)["name"] != "守灯人" ||
		chars[0].(map[string]any)["protection"] != "normal" {
		t.Fatalf("context characters: %+v", chars)
	}
	if prior := cs["priorScenes"].([]any); len(prior) != 1 || prior[0].(map[string]any)["unit"] != "lamp/scene-1" ||
		prior[0].(map[string]any)["stale"] != false {
		t.Fatalf("context priorScenes: %+v", prior)
	}
	if reveal := cs["reveal"].([]any); len(reveal) != 1 || reveal[0].(map[string]any)["text"] != "精灵的身世" {
		t.Fatalf("context reveal: %+v", reveal)
	}
	if d := diag(t, ctx.result, "context.character-not-in-canon"); d.Severity != "warning" || !hasNext(d, "wa-cli canon list lamp --dir "+dir+" --json") {
		t.Fatalf("character-not-in-canon: %+v", d)
	}
	// 违例文本夹具：半角引号对话、第三人称叙述中的「我」与小说 AI 腔各报告发现项（都是 warning）。
	step("", "advance", "lamp/scene-2")
	step("他说\"我不去。\"然后离开。\n\n我看着灯焰发呆。\n\n她的眼底闪过一丝慌乱。\n", "draft", "write", "lamp/scene-2")
	if code, lint := runWS(t, dir, "lint", "lamp/scene-2"); code != 0 {
		t.Fatalf("lint violations: exit code = %d, %+v", code, lint.Diagnostics)
	} else {
		var lr struct {
			Findings []map[string]any `json:"findings"`
		}
		if err := json.Unmarshal([]byte(lint.stdout), &lr); err != nil {
			t.Fatal(err)
		}
		for _, want := range []string{"dialogue", "pov", "fiction-cliches"} {
			if !slices.ContainsFunc(lr.Findings, func(f map[string]any) bool { return f["rule"] == want }) {
				t.Errorf("lint violations lack rule %s: %v", want, lr.Findings)
			}
		}
	}
	// 合规文本：三条小说规则零发现项。
	scene2 := "守灯人按住精灵的手，灯焰晃了一下又稳住。\n\n精灵说灯不会熄，守灯人选择相信。\n\n夜色沉进海里。"
	step(scene2, "draft", "write", "lamp/scene-2")
	if code, lint := runWS(t, dir, "lint", "lamp/scene-2"); code != 0 {
		t.Fatalf("lint compliant: exit code = %d, %+v", code, lint.Diagnostics)
	} else {
		var lr struct {
			Findings []map[string]any `json:"findings"`
		}
		if err := json.Unmarshal([]byte(lint.stdout), &lr); err != nil {
			t.Fatal(err)
		}
		for _, f := range lr.Findings {
			if f["rule"] == "dialogue" || f["rule"] == "pov" || f["rule"] == "fiction-cliches" {
				t.Errorf("lint compliant finding: %v", f)
			}
		}
	}
	step("", "advance", "lamp/scene-2")
	step(fixture(t, "story-review.yaml"), "review", "record", "lamp/scene-2")
	step("", "accept", "lamp/scene-2", "--confirmed-by-author", "--confirmation", "作者：第二个场景可以定稿")
	if res := step("", "advance", "lamp/scene-2"); !strings.Contains(res.stdout, `"stage":"done"`) {
		t.Fatalf("scene-2 to done: %s", res.stdout)
	}
	// 纯文本导出：两个场景拼接，AI 参与说明按 footer 追加；--front-matter 被拒绝。
	res := step("", "export", "lamp", "--format", "text")
	var er struct {
		Export map[string]any `json:"export"`
	}
	if err := json.Unmarshal([]byte(res.stdout), &er); err != nil {
		t.Fatal(err)
	}
	if units := er.Export["units"].([]any); len(units) != 2 || units[0] != "lamp/scene-1" || units[1] != "lamp/scene-2" {
		t.Fatalf("export units: %+v", er.Export)
	}
	if er.Export["format"] != "text" || er.Export["path"] != ".writing-anything/exports/lamp.txt" || er.Export["imageList"] != nil {
		t.Fatalf("export text: %+v", er.Export)
	}
	if skipped := er.Export["skipped"].([]any); len(skipped) != 1 {
		t.Fatalf("export skipped: %+v", er.Export)
	}
	if got := readFile(t, filepath.Join(dir, ".writing-anything", "exports", "lamp.txt")); !strings.Contains(got, "守灯人把油芯拧亮") || !strings.Contains(got, "守灯人按住精灵的手") || !strings.Contains(got, "AI 辅助") {
		t.Fatalf("export text content = %q", got)
	}
	// Markdown 导出照常；--front-matter 与 text 互斥。
	step("", "export", "lamp")
	if got := readFile(t, filepath.Join(dir, ".writing-anything", "exports", "lamp.md")); !strings.Contains(got, "守灯人把油芯拧亮") {
		t.Fatalf("export markdown content = %q", got)
	}
	if d := fail("input.invalid-value", "export", "lamp", "--format", "text", "--front-matter"); d.Field != "front-matter" {
		t.Fatalf("text front matter: %+v", d)
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
	// 纯文本（Plain Text）导出：默认输出 exports/<work>.txt；placement 为 metadata 时纯文本没有元数据位置，
	// 同样按 footer 追加 AI 参与说明（plain-text-export）；--front-matter 被拒绝。
	mustRunWS(t, dir, "config", "set", "ai_disclosure", "metadata")
	code, res = runWS(t, dir, "export", "my-post", "--format", "text")
	if code != 0 || res.ChangedPaths[len(res.ChangedPaths)-1] != ".writing-anything/exports/my-post.txt" {
		t.Fatalf("export text: code = %d, %+v %v", code, res.Diagnostics, res.ChangedPaths)
	}
	if got := readFile(t, filepath.Join(dir, ".writing-anything", "exports", "my-post.txt")); got != reviewDraft+"\n---\n\n"+fullAIDisclosure+"\n" {
		t.Fatalf("export text content = %q", got)
	}
	if code, res = runWS(t, dir, "export", "my-post", "--format", "text", "--front-matter"); code != 1 ||
		diag(t, res, "input.invalid-value").Field != "front-matter" {
		t.Fatalf("text front matter: code = %d, %+v", code, res.Diagnostics)
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

// 端到端：short-story 文体下登记设定集（人物卡、人物关系、作者钦定的世界规则），Agent 修改钦定条目被拒绝并生成
// 阻塞决策，作者决定允许后携带 --decision 重试成功（characters-and-light-canon）。
func TestShortStoryCanonEndToEnd(t *testing.T) {
	dir := t.TempDir()
	step := func(input string, args ...string) result {
		t.Helper()
		code, res := runWSIn(t, dir, input, args...)
		if code != 0 || !res.Valid {
			t.Fatalf("%v: exit code = %d, diagnostics = %+v", args, code, res.Diagnostics)
		}
		return res
	}
	refuse := func(args ...string) (int, result) {
		t.Helper()
		code, res := runWSIn(t, dir, "", args...)
		if code == 0 {
			t.Fatalf("%v: expected refusal", args)
		}
		return code, res
	}
	step("", "init", "--genre", "short-story")
	step("", "new", "short-story", "lighthouse")
	step("守灯人五十岁，左手有旧伤。精灵只在夜里出现。灯油只能燃烧到黎明。", "source", "add", "--type", "author-note", "--id", "notes")
	// 设定集：两张人物卡（character 条目）、一条人物关系（relationship 引用人物条目）与一条作者钦定的世界规则。
	step("", "canon", "add", "lighthouse", "--type", "character", "--name", "守灯人", "--text", "五十岁，左手有旧伤", "--sources", "notes")
	step("", "canon", "add", "lighthouse", "--type", "character", "--name", "精灵", "--text", "只在夜里出现", "--sources", "notes")
	step("", "canon", "add", "lighthouse", "--type", "relationship", "--text", "守灯人是精灵的旧识", "--members", "canon-1,canon-2")
	step("", "canon", "add", "lighthouse", "--type", "world-rule", "--text", "灯油只能燃烧到黎明", "--sources", "notes", "--pin")
	// 写作简报：前提与关键转折都关联作者素材（short-story 的推进关卡）。
	step("", "brief", "set", "lighthouse", "--premise", "守灯人必须在天亮前弄清精灵的身份", "--premise-sources", "notes")
	step("", "brief", "set", "lighthouse", "--pov-person", "third")
	step("", "brief", "set", "lighthouse", "--length", "2000-8000")
	step("", "brief", "set", "lighthouse", "--message", "reveal", "--message-text", "精灵就是当年救过守灯人的船长", "--message-sources", "notes")
	// Agent 试图修改作者钦定的条目：被拒绝，自动生成阻塞决策，canon.yaml 不变。
	_, res := refuse("canon", "set", "lighthouse", "canon-4", "--text", "灯油能烧两天")
	var pinned Diagnostic
	for _, d := range res.Diagnostics {
		if d.Code == "canon.author-pinned" {
			pinned = d
		}
	}
	if pinned.Code == "" || !strings.Contains(pinned.Message, "作者钦定") {
		t.Fatalf("diagnostics = %+v", res.Diagnostics)
	}
	if len(res.ChangedPaths) != 1 || res.ChangedPaths[0] != ".writing-anything/decisions.yaml" {
		t.Fatalf("changedPaths = %v", res.ChangedPaths)
	}
	// 阻塞决策阻塞推进（decision.blocking）。
	if _, res = refuse("advance", "lighthouse"); len(res.Diagnostics) == 0 || res.Diagnostics[0].Code != "decision.blocking" {
		t.Fatalf("advance diagnostics = %+v", res.Diagnostics)
	}
	// 作者决定允许后，携带 --decision 重试成功，条目记录该决策。
	step("", "decide", "dec-1", "--option", "a", "--confirmation", "作者同意放宽这条世界规则")
	code, res := runWSIn(t, dir, "", "canon", "set", "lighthouse", "canon-4", "--text", "灯油能烧两天", "--decision", "dec-1")
	if code != 0 || !res.Valid {
		t.Fatalf("retry: exit code = %d, diagnostics = %+v", code, res.Diagnostics)
	}
	if b, _ := os.ReadFile(filepath.Join(dir, ".writing-anything", "works", "lighthouse", "canon.yaml")); !strings.Contains(string(b), "decision: dec-1") {
		t.Errorf("canon.yaml:\n%s", b)
	}
	// 阻塞解除：推进进入大纲阶段；删除普通条目、列出与整体校验全绿。
	step("", "advance", "lighthouse")
	step("", "canon", "remove", "lighthouse", "canon-3")
	step("", "canon", "list", "lighthouse")
	step("", "validate")
}
