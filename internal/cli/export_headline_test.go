package cli

import (
	"strings"
	"testing"
)

// 导出的标题与摘要取作者选定的值（export-headline-title）：公众号夹具用真实命令走完生命周期——正文没有一级标题、
// 标题决策 dec-1 已决定为 h2、marketing.yaml 有 digest，断言 front matter 的 title/description、可粘贴页的页面 title、
// 复制标题/复制摘要按钮与 --print --json 的 headline/digest；标题决策过期时退回正文首个一级标题；没有营销要素的作品行为不变。
const (
	decidedHeadline = "下周六会员日：手冲体验课和新豆试饮"
	marketingDigest = "本月会员日的活动内容与报名方式"

	// wechatDraftBody 与带配图的公众号 e2e 的正文一致：只有二级标题（公众号正文不写一级标题）。
	wechatDraftBody = "## 开头\n\n上个月有几位老顾客说，会员日来得太突然，没来得及安排时间。\n\n" +
		"## 为什么提前公布\n\n会员日每月第一个周六。老顾客说希望提前知道活动内容，好安排时间。\n\n" +
		"## 这个月有什么\n\n从这个月起，会员日的活动提前一周公布。本月有手冲体验课和新豆试饮。\n\n" +
		"## 怎么报名\n\n在公众号回复「报名」，我们会把时间和地址发给你。\n"
)

// wechatAcceptedWork 返回一个已验收的 wechat-marketing 作品 post：正文为 body，标题决策已决定为 h2（decidedHeadline），
// marketing.yaml 有 digest（marketingDigest）。body 由调用者给出，用于区分「正文没有一级标题」与「退回一级标题」两种夹具。
func wechatAcceptedWork(t *testing.T, body string) string {
	t.Helper()
	dir := t.TempDir()
	step := func(input string, args ...string) {
		t.Helper()
		if code, res := runWSIn(t, dir, input, args...); code != 0 || !res.Valid {
			t.Fatalf("%v: exit code = %d, diagnostics = %+v", args, code, res.Diagnostics)
		}
	}
	step("", "init", "--genre", "wechat-marketing", "--gate-level", "strict")
	step("", "new", "wechat-marketing", "post")
	step("会员日每月第一个周六。老顾客说希望提前知道活动内容，好安排时间。报名在公众号回复即可。", "source", "add", "--type", "author-note", "--id", "note")
	step("", "brief", "set", "post", "--audience", "常来店里的老顾客", "--audience-sources", "note", "--purpose", "让读者报名本月的会员日", "--purpose-sources", "note", "--sources", "note")
	step("", "brief", "set", "post", "--message", "early", "--message-text", "会员日的活动提前一周公布", "--message-sources", "note")
	step("", "advance", "post")
	for _, s := range [][]string{
		{"open", "开头", "attention", "--reason", "用老顾客的心声引起注意"},
		{"why", "为什么提前公布", "interest", "--reason", "说明提前公布的原因"},
		{"offer", "这个月有什么", "desire", "--covers", "early"},
		{"join", "怎么报名", "action", "--reason", "给出报名方式"},
	} {
		step("", "outline", "set", "post", s[0], "--title", s[1], "--role", s[2], s[3], s[4])
	}
	step("", "advance", "post")
	step(body, "draft", "write", "post")
	step("", "claim", "add", "post", "--kind", "experience", "--text", "老顾客说希望提前知道活动内容，好安排时间", "--sources", "note")
	step("", "marketing", "set", "post", "--headline", "h1", "--headline-text", "会员日活动以后提前一周公布", "--digest", marketingDigest,
		"--opening", "上个月有几位老顾客说，会员日来得太突然。", "--cta", "在公众号回复「报名」")
	step("", "marketing", "set", "post", "--headline", "h2", "--headline-text", decidedHeadline)
	step("", "marketing", "ask", "post", "--recommend", "h2", "--rationale", "有具体的时间与内容")
	step("", "decide", "dec-1", "--option", "b", "--confirmation", "作者：用第二个标题")
	step("", "advance", "post")
	review := "rubric:\n"
	for _, id := range []string{"reader-benefit", "hook", "cta", "compliance"} {
		review += "  - {item: " + id + ", verdict: pass, evidence: 作者选定的标题与正文一致, paragraphs: [1]}\n"
	}
	for _, id := range []string{"headline-useful", "headline-urgent", "headline-unique", "headline-specific"} {
		review += "  - {item: " + id + ", verdict: pass, evidence: 作者选定的标题给出了读者收益与具体场景}\n"
	}
	step(review+"findings: []\n", "review", "record", "post")
	step("", "accept", "post", "--confirmed-by-author", "--confirmation", "作者：内容可以")
	return dir
}

// front-matter-decided-headline：正文没有一级标题时，front matter 的 title 为作者选定的标题、description 为 digest。
func TestExportFrontMatterDecidedHeadline(t *testing.T) {
	dir := wechatAcceptedWork(t, wechatDraftBody)
	code, res := runExportJSON(t, dir, "post", "--front-matter")
	if code != 0 {
		t.Fatalf("export: %d %+v", code, res.Diagnostics)
	}
	doc := readFile(t, wsPath(dir, ".writing-anything/exports/post.md"))
	for _, want := range []string{"title: " + decidedHeadline + "\n", "description: " + marketingDigest + "\n"} {
		if !strings.Contains(doc, want) {
			t.Errorf("front matter lacks %q:\n%s", want, doc[:min(len(doc), 300)])
		}
	}
}

// paste-title-copy-buttons：页面 title 为选定的标题，标题与摘要文字在正文容器之外的容器中，
// 复制按钮复用 data-copy；正文 HTML 不插入一级标题；脚本仍只有一个且 CSP 不变。
func TestExportPasteTitleAndCopyButtons(t *testing.T) {
	dir := wechatAcceptedWork(t, wechatDraftBody)
	code, res := runExportJSON(t, dir, "post", "--format", "paste-html")
	const rel = ".writing-anything/exports/post.paste.html"
	if code != 0 || res.Export.Path == nil || *res.Export.Path != rel {
		t.Fatalf("export: %d %+v", code, res)
	}
	page := readFile(t, wsPath(dir, rel))
	for _, want := range []string{
		"<title>" + decidedHeadline + "</title>",
		`<button data-copy="wa-headline" type="button"` + " ", ">复制标题</button>",
		`<button data-copy="wa-digest" type="button"` + " ", ">复制摘要</button>",
		`<div id="wa-headline"` + " ", decidedHeadline + "</div>",
		`<div id="wa-digest"` + " ", marketingDigest + "</div>",
	} {
		if !strings.Contains(page, want) {
			t.Errorf("page lacks %q", want)
		}
	}
	// 标题与摘要容器在正文容器之外（复制全文不会带上它们），正文没有因标题解析而插入的一级标题。
	for _, id := range []string{`id="wa-headline"`, `id="wa-digest"`} {
		if head, article := strings.Index(page, id), strings.Index(page, `<div id="wa-article"`); head < 0 || article < 0 || head > article {
			t.Errorf("%s container not outside the article container", id)
		}
	}
	if strings.Contains(page, "<h1") {
		t.Errorf("page contains an h1:\n%s", page[:min(len(page), 400)])
	}
	if n := strings.Count(page, "<script"); n != 1 {
		t.Errorf("page has %d script elements", n)
	}
}

// print-headline-digest：--print --json 的 headline 与 digest 取作者选定的值，html 与 segments 不含标题与摘要。
func TestExportPrintHeadlineDigest(t *testing.T) {
	dir := wechatAcceptedWork(t, wechatDraftBody)
	code, res := runExportJSON(t, dir, "post", "--format", "paste-html", "--print")
	if code != 0 || res.Export.HTML == nil || len(res.Export.Segments) == 0 {
		t.Fatalf("export: %d %+v", code, res)
	}
	if res.Export.Headline == nil || *res.Export.Headline == nil || **res.Export.Headline != decidedHeadline ||
		res.Export.Digest == nil || *res.Export.Digest == nil || **res.Export.Digest != marketingDigest {
		t.Fatalf("headline/digest = %v %v", res.Export.Headline, res.Export.Digest)
	}
	if strings.Contains(*res.Export.HTML, decidedHeadline) || strings.Contains(*res.Export.HTML, "复制标题") {
		t.Errorf("print fragment contains the headline or a copy button")
	}
}

// front-matter-stale-fallback：标题候选在决策之后改过（决策过期）时，title 退回正文首个一级标题，
// description 仍为 digest；页面没有「复制标题」按钮（仍有「复制摘要」）；--print 的 headline 为 null。
func TestExportStaleHeadlineFallsBack(t *testing.T) {
	dir := wechatAcceptedWork(t, "# 连接池实战\n\n"+wechatDraftBody)
	mustRunWS(t, dir, "marketing", "set", "post", "--headline", "h2", "--headline-text", "连接池不够用的三个原因")
	code, res := runExportJSON(t, dir, "post", "--front-matter")
	if code != 0 {
		t.Fatalf("export: %d %+v", code, res.Diagnostics)
	}
	doc := readFile(t, wsPath(dir, ".writing-anything/exports/post.md"))
	if !strings.Contains(doc, "title: 连接池实战\n") || !strings.Contains(doc, "description: "+marketingDigest+"\n") {
		t.Errorf("stale front matter:\n%s", doc[:min(len(doc), 300)])
	}
	code, res = runExportJSON(t, dir, "post", "--format", "paste-html")
	if code != 0 {
		t.Fatalf("paste export: %d %+v", code, res.Diagnostics)
	}
	page := readFile(t, wsPath(dir, ".writing-anything/exports/post.paste.html"))
	if !strings.Contains(page, "<title>连接池实战</title>") || strings.Contains(page, "复制标题") || !strings.Contains(page, ">复制摘要</button>") {
		t.Errorf("stale page title or buttons wrong")
	}
	if strings.Contains(page, `id="wa-headline"`) {
		t.Errorf("stale page has a headline container")
	}
	// --print 的 headline 为 null（字段仍出现），digest 仍为选定摘要。
	code, res = runExportJSON(t, dir, "post", "--format", "paste-html", "--print")
	if code != 0 || !strings.Contains(res.stdout, `"headline":null`) || !strings.Contains(res.stdout, `"digest":"`+marketingDigest+`"`) {
		t.Fatalf("stale print: %d %s", code, res.stdout[:min(len(res.stdout), 400)])
	}
}

// paste-no-headline-no-buttons 与 print-headline-digest-null：没有营销要素的作品（tech-blog 夹具）页面没有两个新按钮，
// title 退回正文首个一级标题；--print 的 headline 与 digest 字段仍出现且为 null。
func TestExportHeadlineAbsentUnchanged(t *testing.T) {
	dir := acceptedWork(t, "# 连接池实战\n\n正文。\n")
	code, res := runExportJSON(t, dir, "my-post", "--format", "paste-html")
	if code != 0 {
		t.Fatalf("export: %d %+v", code, res.Diagnostics)
	}
	page := readFile(t, wsPath(dir, ".writing-anything/exports/my-post.paste.html"))
	if !strings.Contains(page, "<title>连接池实战</title>") {
		t.Errorf("page title is not the first heading")
	}
	// 没有标题与摘要时，工具栏与正文容器之间的输出与本变更之前逐字节相同（不因空的条件块留下空行）。
	if !strings.Contains(page, "</div>\n<div id=\"wa-article\">") {
		t.Errorf("blank lines between the toolbar and the article container")
	}
	for _, bad := range []string{"复制标题", "复制摘要", `id="wa-headline"`, `id="wa-digest"`, `data-copy="wa-headline"`, `data-copy="wa-digest"`} {
		if strings.Contains(page, bad) {
			t.Errorf("page contains %q", bad)
		}
	}
	code, res = runExportJSON(t, dir, "my-post", "--format", "paste-html", "--print")
	if code != 0 || !strings.Contains(res.stdout, `"headline":null`) || !strings.Contains(res.stdout, `"digest":null`) {
		t.Fatalf("print nulls: %d %s", code, res.stdout[:min(len(res.stdout), 400)])
	}
}

// 决策队列无法加载或 marketing.yaml 不通过校验时，导出把根单元当作没有作者选定标题与摘要处理：
// 不新增诊断、title 与 description 照回退链取值、--print 的 headline 与 digest 为 null。
func TestExportBrokenMarketingInputs(t *testing.T) {
	// 决策队列无法加载：标题决策状态为 unknown，headline 为 null；digest 来自 marketing.yaml，不受影响。
	dir := wechatAcceptedWork(t, "# 连接池实战\n\n"+wechatDraftBody)
	writeFile(t, wsPath(dir, ".writing-anything/decisions.yaml"), "schema_version: 9\n")
	code, res := runExportJSON(t, dir, "post", "--front-matter")
	if code != 0 || !res.Valid || len(res.Diagnostics) != 0 {
		t.Fatalf("broken decisions: %d %+v", code, res.Diagnostics)
	}
	if doc := readFile(t, wsPath(dir, ".writing-anything/exports/post.md")); !strings.Contains(doc, "title: 连接池实战\n") || !strings.Contains(doc, "description: "+marketingDigest+"\n") {
		t.Errorf("broken decisions front matter:\n%s", doc[:min(len(doc), 300)])
	}
	if code, res := runExportJSON(t, dir, "post", "--format", "paste-html", "--print"); code != 0 || len(res.Diagnostics) != 0 ||
		!strings.Contains(res.stdout, `"headline":null`) || !strings.Contains(res.stdout, `"digest":"`+marketingDigest+`"`) {
		t.Fatalf("broken decisions print: %d %s", code, res.stdout[:min(len(res.stdout), 400)])
	}
	// marketing.yaml 不通过校验：选定标题与 digest 都取不到，description 回退到写作简报的 purpose。
	dir = wechatAcceptedWork(t, "# 连接池实战\n\n"+wechatDraftBody)
	writeFile(t, wsPath(dir, ".writing-anything/works/post/marketing.yaml"), "schema_version: 1\nheadlines:\n  - {id: h1, text: 甲}\n  - {id: h1, text: 乙}\n")
	code, res = runExportJSON(t, dir, "post", "--front-matter")
	if code != 0 || !res.Valid || len(res.Diagnostics) != 0 {
		t.Fatalf("broken marketing: %d %+v", code, res.Diagnostics)
	}
	if doc := readFile(t, wsPath(dir, ".writing-anything/exports/post.md")); !strings.Contains(doc, "title: 连接池实战\n") || !strings.Contains(doc, "description: 让读者报名本月的会员日\n") {
		t.Errorf("broken marketing front matter:\n%s", doc[:min(len(doc), 300)])
	}
	if code, res = runExportJSON(t, dir, "post", "--format", "paste-html", "--print"); code != 0 ||
		!strings.Contains(res.stdout, `"headline":null`) || !strings.Contains(res.stdout, `"digest":null`) {
		t.Fatalf("broken marketing print: %d %s", code, res.stdout[:min(len(res.stdout), 400)])
	}
	if _, res = runExportJSON(t, dir, "post", "--format", "paste-html"); res.Export.Path == nil {
		t.Fatalf("paste export: %+v", res)
	}
	if page := readFile(t, wsPath(dir, ".writing-anything/exports/post.paste.html")); strings.Contains(page, "复制标题") || strings.Contains(page, "复制摘要") {
		t.Errorf("broken marketing page has copy buttons")
	}
}
