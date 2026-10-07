package cli

import (
	"encoding/json"
	"os"
	"path/filepath"
	"slices"
	"strings"
	"testing"
)

type marketingOut struct {
	result
	Marketing *marketingView          `json:"marketing"`
	Questions []marketingQuestionView `json:"questions"`
	Decision  *decisionView           `json:"decision"`
}

func runMarketing(t *testing.T, dir string, args ...string) (int, marketingOut) {
	t.Helper()
	code, res := runWS(t, dir, append([]string{"marketing"}, args...)...)
	var out marketingOut
	if err := json.Unmarshal([]byte(res.stdout), &out); err != nil {
		t.Fatalf("%v: %v\n%s", args, err, res.stdout)
	}
	out.result = res
	return code, out
}

func questionList(qs []marketingQuestionView) string {
	var out []string
	for _, q := range qs {
		out = append(out, q.Code+":"+q.Field)
	}
	return strings.Join(out, ",")
}

const marketingRel = ".writing-anything/works/my-post/marketing.yaml"

func TestMarketingSetShow(t *testing.T) {
	dir := newWork(t)
	if code, res := runMarketing(t, dir, "show", "my-post"); code != 0 || res.Marketing != nil || len(res.Questions) != 4 {
		t.Fatalf("show without marketing: %d %+v", code, res)
	}
	// set-elements
	code, res := runMarketing(t, dir, "set", "my-post", "--headline", "h1", "--headline-text", "三步搞定连接池", "--digest", "一篇讲连接池的短文")
	if code != 0 || res.Marketing.Headlines[0].Text != "三步搞定连接池" || *res.Marketing.Digest != "一篇讲连接池的短文" || res.Marketing.HeadlineState != "none" ||
		questionList(res.Questions) != "marketing.missing-field:headlines,marketing.missing-field:opening,marketing.missing-field:cta" ||
		strings.Join(res.ChangedPaths, ",") != marketingRel {
		t.Fatalf("set-elements: %d %+v", code, res)
	}
	// set-unchanged
	before := readFile(t, wsPath(dir, marketingRel))
	if code, res := runMarketing(t, dir, "set", "my-post", "--digest", "一篇讲连接池的短文"); code != 0 || len(res.ChangedPaths) != 0 || readFile(t, wsPath(dir, marketingRel)) != before {
		t.Fatalf("set-unchanged: %d %+v", code, res)
	}
	// remove-missing-headline 与参数问题：不修改文件，next 给出用法。
	for _, tc := range []struct {
		args []string
		code string
	}{
		{[]string{"--remove-headline", "h9"}, "marketing.headline-not-found"},
		{[]string{}, "input.invalid-value"},
		{[]string{"--headline-text", "孤立的文字"}, "input.invalid-value"},
		{[]string{"--headline", "h2"}, "input.invalid-value"},
		{[]string{"--headline", "h1"}, "input.invalid-value"},
		{[]string{"--headline", "H1", "--headline-text", "x"}, "artifact.schema-violation"},
		{[]string{"--digest", "  "}, "input.invalid-value"},
	} {
		code, res := runMarketing(t, dir, append([]string{"set", "my-post"}, tc.args...)...)
		if d := diag(t, res.result, tc.code); code != 1 || !hasNext(d, "wa-cli help marketing set --json") || readFile(t, wsPath(dir, marketingRel)) != before {
			t.Errorf("%v: %d %+v", tc.args, code, res.Diagnostics)
		}
	}
	// 人读输出。
	if code, stdout, _ := run(t, "marketing", "set", "my-post", "--opening", "上周的故障，又是连接池。", "--dir", dir); code != 0 || !strings.Contains(stdout, "开头钩子：上周的故障") || !strings.Contains(stdout, "待补齐") {
		t.Fatalf("text: %s", stdout)
	}
	if code, stdout, _ := run(t, "marketing", "show", "my-post", "--dir", dir); code != 0 || !strings.Contains(stdout, "标题候选 h1：三步搞定连接池") {
		t.Fatalf("show text: %s", stdout)
	}
	if _, stdout, _ := run(t, "marketing", "show", "my-post/nope", "--dir", dir); stdout != "" {
		t.Fatalf("show error text: %q", stdout)
	}
	// marketing.yaml 不通过校验：show 与 set 报告其诊断；validate 整体与按路径都报告重复的候选 id（duplicate-headline）。
	writeFile(t, wsPath(dir, marketingRel), "schema_version: 1\nheadlines:\n  - {id: h1, text: 甲}\n  - {id: h1, text: 乙}\n")
	if code, res := runMarketing(t, dir, "show", "my-post"); code != 1 || diag(t, res.result, "artifact.duplicate-id").Field != "headlines[1].id" {
		t.Fatalf("show invalid: %+v", res.Diagnostics)
	}
	for _, args := range [][]string{{"validate"}, {"validate", marketingRel}} {
		if code, res := runWS(t, dir, args...); code != 1 || diag(t, res, "artifact.duplicate-id").Field != "headlines[1].id" {
			t.Errorf("%v: %d %+v", args, code, res.Diagnostics)
		}
	}
}

func TestMarketingAsk(t *testing.T) {
	dir := newWork(t)
	// marketing.yaml 不存在与只有一个候选时不能提问。
	if code, res := runMarketing(t, dir, "ask", "my-post"); code != 1 || !hasNext(diag(t, res.result, "artifact.not-found"), "wa-cli help marketing set --json") {
		t.Fatalf("no marketing: %+v", res.Diagnostics)
	}
	mustRunWS(t, dir, "marketing", "set", "my-post", "--headline", "h1", "--headline-text", "三步搞定连接池")
	// ask-one-candidate
	if code, res := runMarketing(t, dir, "ask", "my-post"); code != 1 || diag(t, res.result, "marketing.missing-field").Field != "headlines" {
		t.Fatalf("ask-one-candidate: %+v", res.Diagnostics)
	}
	mustRunWS(t, dir, "marketing", "set", "my-post", "--headline", "h2", "--headline-text", "连接池为什么总不够用")
	if code, res := runMarketing(t, dir, "ask", "my-post", "--recommend", "h9"); code != 1 || !hasNext(diag(t, res.result, "marketing.headline-not-found"), "wa-cli help marketing ask --json") {
		t.Fatalf("bad recommend: %+v", res.Diagnostics)
	}
	// ask-headline
	code, res := runMarketing(t, dir, "ask", "my-post", "--recommend", "h2", "--rationale", "更具体")
	if d := res.Decision; code != 0 || d == nil || d.ID != "dec-1" || !d.Blocking || *d.Recommendation != "b" || d.Options[0].Text != "三步搞定连接池" ||
		strings.Join(d.Units, ",") != "my-post" || res.Marketing.HeadlineState != "open" || strings.Join(res.ChangedPaths, ",") != ".writing-anything/decisions.yaml,"+marketingRel {
		t.Fatalf("ask-headline: %d %+v", code, res)
	}
	// ask-twice
	queue, before := readFile(t, wsPath(dir, ".writing-anything/decisions.yaml")), readFile(t, wsPath(dir, marketingRel))
	if code, res := runMarketing(t, dir, "ask", "my-post"); code != 1 || !hasNext(diag(t, res.result, "marketing.already-asked"), "wa-cli decisions") ||
		readFile(t, wsPath(dir, ".writing-anything/decisions.yaml")) != queue || readFile(t, wsPath(dir, marketingRel)) != before {
		t.Fatalf("ask-twice: %+v", res.Diagnostics)
	}
	// show-decided
	mustRunWS(t, dir, "decide", "dec-1", "--option", "b", "--confirmation", "作者：用第二个")
	if _, res := runMarketing(t, dir, "show", "my-post"); res.Marketing.HeadlineState != "decided" || res.Marketing.Headline.ID != "h2" || *res.Marketing.HeadlineDecision != "dec-1" {
		t.Fatalf("show-decided: %+v", res.Marketing)
	}
	// show-stale：候选在提问之后改过，需要重新提问。
	mustRunWS(t, dir, "marketing", "set", "my-post", "--headline", "h2", "--headline-text", "连接池不够用的三个原因")
	_, res = runMarketing(t, dir, "show", "my-post")
	if res.Marketing.HeadlineState != "stale" || res.Marketing.Headline != nil || !strings.Contains(questionList(res.Questions), "marketing.headline-stale") {
		t.Fatalf("show-stale: %+v %+v", res.Marketing, res.Questions)
	}
	if code, stdout, _ := run(t, "marketing", "ask", "my-post", "--dir", dir); code != 0 || !strings.Contains(stdout, "已登记标题决策 dec-2") {
		t.Fatalf("re-ask: %d %s", code, stdout)
	}
	// 决策队列无法加载时 show 不判断标题决策。
	writeFile(t, wsPath(dir, ".writing-anything/decisions.yaml"), "schema_version: 9\n")
	if _, res := runMarketing(t, dir, "show", "my-post"); res.Marketing.HeadlineState != "unknown" {
		t.Fatalf("unknown: %+v", res.Marketing)
	}
	if code, _ := runMarketing(t, dir, "ask", "my-post"); code != 1 {
		t.Fatal("ask with a broken queue succeeded")
	}
}

// 内置 wechat-marketing 文体与营销写作法（wechat-marketing-genre、builtin-marketing-methods）。
func TestWechatMarketingGenre(t *testing.T) {
	dir := t.TempDir()
	mustRunWS(t, dir, "init", "--genre", "wechat-marketing")
	_, res := runWS(t, dir, "new", "wechat-marketing", "post")
	if !strings.Contains(res.stdout, `"genre":"wechat-marketing"`) || !strings.Contains(res.stdout, `"stage":"brief"`) {
		t.Fatalf("new: %s", res.stdout)
	}
	_, res = runWS(t, dir, "config", "resolved")
	r := res.resolved(t)
	if get(r, "rules.ads.type.value") != "ad-compliance" || get(r, "rules.ads.severity.value") != "error" || get(r, "rules.ads.type.source") != "genre:wechat-marketing" {
		t.Fatalf("ads rule: %v", get(r, "rules.ads"))
	}
	rubric := map[string]any{}
	for _, item := range r["rubric"].([]any) {
		rubric[item.(map[string]any)["id"].(string)] = item
	}
	for _, id := range []string{"reader-benefit", "hook", "cta", "compliance", "headline-useful", "headline-urgent", "headline-unique", "headline-specific"} {
		if rubric[id] == nil {
			t.Errorf("rubric lacks %s", id)
		}
	}
	if get(rubric["compliance"].(map[string]any), "blocking") != true {
		t.Errorf("compliance not blocking: %v", rubric["compliance"])
	}
	for id, sections := range map[string]string{"aida": "attention,interest,desire,action", "pas": "problem,agitate,solution"} {
		_, res := runMethod(t, dir, "method", "show", id)
		var got []string
		for _, s := range res.Method["sections"].([]any) {
			got = append(got, s.(map[string]any)["id"].(string))
		}
		if res.Method["kind"] != "article" || res.Method["source"] != "builtin" || strings.Join(got, ",") != sections {
			t.Errorf("%s: %v", id, res.Method)
		}
	}
	// superlative-fails-lint
	mustRunWS(t, dir, "brief", "set", "post", "--audience", "老顾客")
	writeFile(t, wsPath(dir, ".writing-anything/works/post/draft.md"), "本店全网最低价。\n")
	code, lint := runWS(t, dir, "lint", "post")
	if code != 1 || !strings.Contains(lint.stdout, `"rule":"ads"`) || !strings.Contains(lint.stdout, `"source":"genre:wechat-marketing"`) {
		t.Fatalf("superlative-fails-lint: %d %s", code, lint.stdout)
	}
	// 选用 PAS 而大纲按 AIDA 组织时，validate 报告写作法缺口。
	mustRunWS(t, dir, "outline", "set", "post", "hook", "--title", "开头", "--role", "attention", "--reason", "引起注意")
	mustRunWS(t, dir, "brief", "set", "post", "--method", "pas")
	if code, res := runWS(t, dir, "validate"); code != 1 || diag(t, res, "outline.missing-method-part").Code == "" {
		t.Fatalf("pas gaps: %+v", res.Diagnostics)
	}
}

// 营销要素关卡与 next（marketing-gate、next-blocked-by-marketing）。
func TestMarketingGate(t *testing.T) {
	dir := t.TempDir()
	mustRunWS(t, dir, "init", "--genre", "wechat-marketing")
	mustRunWS(t, dir, "new", "wechat-marketing", "post")
	// 直接把作品放到草稿阶段并写入正文。
	work := wsPath(dir, ".writing-anything/works/post/work.yaml")
	writeFile(t, work, strings.Replace(readFile(t, work), "stage: brief", "stage: draft", 1))
	writeFile(t, wsPath(dir, ".writing-anything/works/post/draft.md"), "正文。\n")
	code, res := runWS(t, dir, "next", "post")
	if code != 0 || !strings.Contains(res.stdout, `"artifact":"marketing"`) || !strings.Contains(res.stdout, "wa-cli help marketing set --json") {
		t.Fatalf("missing marketing: %s", res.stdout)
	}
	// next-blocked-by-marketing
	mustRunWS(t, dir, "marketing", "set", "post", "--headline", "h1", "--headline-text", "会员日提前一周公布")
	mustRunWS(t, dir, "marketing", "set", "post", "--headline", "h2", "--headline-text", "会员日怎么报名")
	_, res = runWS(t, dir, "next", "post")
	for _, want := range []string{`"code":"marketing.missing-field"`, `"code":"marketing.headline-unasked"`, "wa-cli marketing show post", "wa-cli help marketing set --json", "wa-cli help marketing ask --json", "补齐营销要素"} {
		if !strings.Contains(res.stdout, want) {
			t.Errorf("next lacks %q: %s", want, res.stdout)
		}
	}
	// gate-missing-cta
	mustRunWS(t, dir, "marketing", "set", "post", "--digest", "会员日的报名方式", "--opening", "上个月有老顾客没赶上")
	mustRunWS(t, dir, "marketing", "ask", "post")
	mustRunWS(t, dir, "decide", "dec-1", "--option", "a", "--confirmation", "作者：用第一个")
	before := readFile(t, work)
	code, res = runWS(t, dir, "advance", "post")
	if d := diag(t, res, "marketing.missing-field"); code != 1 || !strings.Contains(d.Message, "cta") || !hasNext(d, "wa-cli help marketing set --json") || readFile(t, work) != before {
		t.Fatalf("gate-missing-cta: %+v", res.Diagnostics)
	}
	// gate-open-decision：候选改过后标题决策过期；重新提问后未决定时由决策阻塞，决定后可以推进。
	mustRunWS(t, dir, "marketing", "set", "post", "--cta", "回复「报名」即可", "--headline", "h2", "--headline-text", "会员日报名只要三步")
	if code, res := runWS(t, dir, "advance", "post"); code != 1 || !hasNext(diag(t, res, "marketing.headline-stale"), "wa-cli help marketing ask --json") {
		t.Fatalf("stale: %+v", res.Diagnostics)
	}
	mustRunWS(t, dir, "marketing", "ask", "post")
	if code, res := runWS(t, dir, "advance", "post"); code != 1 || diag(t, res, "decision.blocking").Code == "" {
		t.Fatalf("gate-open-decision: %+v", res.Diagnostics)
	}
	mustRunWS(t, dir, "decide", "dec-2", "--option", "b", "--confirmation", "作者：用新的第二个")
	mustRunWS(t, dir, "advance", "post")
	// 不通过校验的 marketing.yaml 与只有 marketing.md 的情况（在草稿阶段检查）。
	writeFile(t, work, strings.Replace(readFile(t, work), "stage: review", "stage: draft", 1))
	writeFile(t, wsPath(dir, ".writing-anything/works/post/marketing.yaml"), "schema_version: 1\nbogus: 1\n")
	if code, res := runWS(t, dir, "advance", "post"); code != 1 || !hasNext(diag(t, res, "marketing.invalid"), "wa-cli validate .writing-anything/works/post/marketing.yaml") {
		t.Fatalf("invalid: %+v", res.Diagnostics)
	}
	if err := os.Remove(wsPath(dir, ".writing-anything/works/post/marketing.yaml")); err != nil {
		t.Fatal(err)
	}
	writeFile(t, wsPath(dir, ".writing-anything/works/post/marketing.md"), "标题：随便写写\n")
	if _, res := runWS(t, dir, "next", "post"); strings.Count(res.stdout, `"code":"marketing.missing-field"`) != 4 || !strings.Contains(res.stdout, "marketing.md") {
		t.Fatalf("md only: %s", res.stdout)
	}
}

// 端到端：一篇公众号营销文章从写作简报到验收与导出，只用 wa-cli 命令（wechat-e2e）。
func TestWechatMarketingEndToEnd(t *testing.T) {
	dir := t.TempDir()
	step := func(input string, args ...string) result {
		t.Helper()
		code, res := runWSIn(t, dir, input, args...)
		if code != 0 || !res.Valid {
			t.Fatalf("%v: exit code = %d, diagnostics = %+v", args, code, res.Diagnostics)
		}
		return res
	}
	refuse := func(code string, args ...string) {
		t.Helper()
		if c, res := runWS(t, dir, args...); c != 1 || diag(t, res, code).Code == "" {
			t.Fatalf("%v: want %s, got %d %+v", args, code, c, res.Diagnostics)
		}
	}
	step("", "init", "--genre", "wechat-marketing", "--gate-level", "strict")
	step("", "new", "wechat-marketing", "post")
	step("会员日每月第一个周六。老顾客说希望提前知道活动内容，好安排时间。报名在公众号回复即可。", "source", "add", "--type", "author-note", "--id", "note")
	step("", "brief", "set", "post", "--audience", "常来店里的老顾客", "--purpose", "让读者报名本月的会员日", "--sources", "note")
	step("", "brief", "set", "post", "--message", "early", "--message-text", "会员日的活动提前一周公布", "--message-sources", "note")
	step("", "advance", "post")
	// 大纲按 AIDA 的四个章节组织，覆盖核心观点。
	for _, s := range [][]string{
		{"open", "开头", "attention", "--reason", "用老顾客的心声引起注意"},
		{"why", "为什么提前公布", "interest", "--reason", "说明提前公布的原因"},
		{"offer", "这个月有什么", "desire", "--covers", "early"},
		{"join", "怎么报名", "action", "--reason", "给出报名方式"},
	} {
		step("", "outline", "set", "post", s[0], "--title", s[1], "--role", s[2], s[3], s[4])
	}
	step("", "advance", "post")
	draft := "## 开头\n\n上个月有几位老顾客说，会员日来得太突然，没来得及安排时间。\n\n" +
		"## 为什么提前公布\n\n会员日每月第一个周六。老顾客说希望提前知道活动内容，好安排时间。\n\n" +
		"## 这个月有什么\n\n从这个月起，会员日的活动提前一周公布。本月有手冲体验课和新豆试饮。\n\n" +
		"## 怎么报名\n\n在公众号回复「报名」，我们会把时间和地址发给你。\n"
	step(draft, "draft", "write", "post")
	step("", "claim", "add", "post", "--kind", "experience", "--text", "老顾客说希望提前知道活动内容，好安排时间", "--sources", "note")
	// 营销要素：缺少时不能进入审稿；标题由作者通过决策选定。
	refuse("lifecycle.missing-artifact", "advance", "post")
	step("", "marketing", "set", "post", "--headline", "h1", "--headline-text", "会员日活动以后提前一周公布", "--digest", "本月会员日的活动内容与报名方式",
		"--opening", "上个月有几位老顾客说，会员日来得太突然。", "--cta", "在公众号回复「报名」")
	step("", "marketing", "set", "post", "--headline", "h2", "--headline-text", "下周六会员日：手冲体验课和新豆试饮")
	refuse("marketing.headline-unasked", "advance", "post")
	step("", "marketing", "ask", "post", "--recommend", "h2", "--rationale", "有具体的时间与内容")
	refuse("decision.blocking", "advance", "post")
	step("", "decide", "dec-1", "--option", "b", "--confirmation", "作者：用第二个标题")
	code, lint := runWSIn(t, dir, "", "lint", "post")
	if code != 0 || strings.Contains(lint.stdout, `"severity":"error"`) {
		t.Fatalf("lint: %d %s", code, lint.stdout)
	}
	step("", "advance", "post")
	// 审稿：评分标准含 4U 标题法的评分项，strict 关卡下需要作者确认。
	review := "rubric:\n"
	for _, id := range []string{"reader-benefit", "hook", "cta", "compliance", "headline-useful", "headline-urgent", "headline-unique", "headline-specific"} {
		review += "  - {item: " + id + ", verdict: pass, evidence: 作者选定的标题与正文一致, paragraphs: [1]}\n"
	}
	step(review+"findings: []\n", "review", "record", "post")
	refuse("accept.unconfirmed", "accept", "post")
	step("", "accept", "post", "--confirmed-by-author", "--confirmation", "作者：可以发")
	if res := step("", "advance", "post"); !strings.Contains(res.stdout, `"stage":"done"`) {
		t.Fatalf("advance to done: %s", res.stdout)
	}
	step("", "export", "post")
	if got := readFile(t, filepath.Join(dir, ".writing-anything", "exports", "post.md")); !strings.Contains(got, "手冲体验课") {
		t.Fatalf("export: %s", got)
	}
	// 公众号 HTML：只含内联样式（only-inline-styles）。
	step("", "export", "post", "--format", "wechat-html")
	html := readFile(t, filepath.Join(dir, ".writing-anything", "exports", "post.html"))
	allowedHTML(t, html)
	if !strings.Contains(html, "手冲体验课") || !strings.Contains(html, ">怎么报名</h2>") {
		t.Fatalf("wechat-html: %s", html)
	}
	step("", "validate")
}

// 尚未决定的标题决策在候选改过后重新提问：原地替换选项，决定后即可推进，旧的选项不会继续阻塞单元。
func TestMarketingReaskOpenDecision(t *testing.T) {
	dir := t.TempDir()
	mustRunWS(t, dir, "init", "--genre", "wechat-marketing")
	mustRunWS(t, dir, "new", "wechat-marketing", "post")
	work := wsPath(dir, ".writing-anything/works/post/work.yaml")
	writeFile(t, work, strings.Replace(readFile(t, work), "stage: brief", "stage: draft", 1))
	writeFile(t, wsPath(dir, ".writing-anything/works/post/draft.md"), "正文。\n")
	mustRunWS(t, dir, "marketing", "set", "post", "--headline", "h1", "--headline-text", "甲", "--digest", "摘要", "--opening", "开头", "--cta", "回复报名")
	mustRunWS(t, dir, "marketing", "set", "post", "--headline", "h2", "--headline-text", "乙")
	mustRunWS(t, dir, "marketing", "ask", "post")
	mustRunWS(t, dir, "marketing", "set", "post", "--headline", "h2", "--headline-text", "丙")
	code, res := runMarketing(t, dir, "ask", "post")
	if code != 0 || res.Decision.ID != "dec-1" || res.Decision.Options[1].Text != "丙" || strings.Join(res.ChangedPaths, ",") != ".writing-anything/decisions.yaml" {
		t.Fatalf("re-ask: %d %+v", code, res)
	}
	mustRunWS(t, dir, "decide", "dec-1", "--option", "b", "--confirmation", "作者：用丙")
	mustRunWS(t, dir, "advance", "post")
	// marketing.yaml 途经符号链接时，ask 不把下一步指向 marketing set。
	rel := ".writing-anything/works/post/marketing.yaml"
	if err := os.Remove(wsPath(dir, rel)); err != nil {
		t.Fatal(err)
	}
	if err := os.Symlink("elsewhere.yaml", wsPath(dir, rel)); err != nil {
		t.Skip(err)
	}
	if code, res := runMarketing(t, dir, "ask", "post"); code != 1 || hasNext(diag(t, res.result, "artifact.not-found"), "wa-cli help marketing set") {
		t.Fatalf("symlink: %+v", res.Diagnostics)
	}
}

// AIDA 缺段负向用例：默认写作法 AIDA 下大纲只承担 attention、interest、desire 时，validate 以非零退出并报告缺少 action 章节的
// outline.missing-method-part（只报这一段）；补上 action 一节后不再报告写作法缺口。
func TestWechatMarketingAIDAMissingPart(t *testing.T) {
	dir := t.TempDir()
	mustRunWS(t, dir, "init", "--genre", "wechat-marketing")
	mustRunWS(t, dir, "new", "wechat-marketing", "post")
	for _, s := range [][]string{
		{"open", "开头", "attention"},
		{"why", "为什么", "interest"},
		{"offer", "有什么", "desire"},
	} {
		mustRunWS(t, dir, "outline", "set", "post", s[0], "--title", s[1], "--role", s[2], "--reason", "推进")
	}
	code, res := runWS(t, dir, "validate")
	var gaps []Diagnostic
	for _, d := range res.Diagnostics {
		if d.Code == "outline.missing-method-part" {
			gaps = append(gaps, d)
		}
	}
	if code != 1 || len(gaps) != 1 || gaps[0].Severity != "error" || !strings.Contains(gaps[0].Message, "aida") || !strings.Contains(gaps[0].Message, "action") ||
		strings.Contains(gaps[0].Message, "attention") {
		t.Fatalf("aida missing action: exit = %d, diagnostics = %+v", code, res.Diagnostics)
	}
	mustRunWS(t, dir, "outline", "set", "post", "join", "--title", "怎么报名", "--role", "action", "--reason", "给出报名方式")
	if code, res := runWS(t, dir, "validate"); code != 0 || slices.ContainsFunc(res.Diagnostics, func(d Diagnostic) bool { return strings.HasPrefix(d.Code, "outline.") }) {
		t.Fatalf("aida complete: %+v", res.Diagnostics)
	}
}
