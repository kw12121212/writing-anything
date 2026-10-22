package cli

import (
	"encoding/json"
	"os"
	"path/filepath"
	"strings"
	"testing"
)

// 带配图的公众号文章：wechat-marketing 作品在 strict 关卡下走完生命周期，修订后、验收前登记封面、氛围图与信息图，
// 插入正文引用并重申审稿，作者验收锁定图文，导出公众号 HTML 与 Markdown，资产目录、配图清单与 AI 参与说明都包含配图；
// 验收后替换配图使验收失效、导出拒绝（scene illustrate-before-acceptance）。
func TestWechatIllustratedEndToEnd(t *testing.T) {
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
	draft := "## 开头\n\n上个月有几位老顾客说，会员日来得太突然，没来得及安排时间。\n\n" +
		"## 为什么提前公布\n\n会员日每月第一个周六。老顾客说希望提前知道活动内容，好安排时间。\n\n" +
		"## 这个月有什么\n\n从这个月起，会员日的活动提前一周公布。本月有手冲体验课和新豆试饮。\n\n" +
		"## 怎么报名\n\n在公众号回复「报名」，我们会把时间和地址发给你。\n"
	step(draft, "draft", "write", "post")
	step("", "claim", "add", "post", "--kind", "experience", "--text", "老顾客说希望提前知道活动内容，好安排时间", "--sources", "note")
	step("", "marketing", "set", "post", "--headline", "h1", "--headline-text", "会员日活动以后提前一周公布", "--digest", "本月会员日的活动内容与报名方式",
		"--opening", "上个月有几位老顾客说，会员日来得太突然。", "--cta", "在公众号回复「报名」")
	step("", "marketing", "set", "post", "--headline", "h2", "--headline-text", "下周六会员日：手冲体验课和新豆试饮")
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

	// 配图环节：作者确认配图计划（经决策队列登记）后，Agent 渲染并登记封面、氛围图与信息图。
	step("", "decision", "add", "--question", "配图计划：封面、开头氛围图与提前公布的信息图，可以吗？", "--unit", "post")
	step("", "decide", "dec-2", "--answer", "可以，按这个计划配图", "--confirmation", "作者：按这个计划配图")
	writeTestImage(t, filepath.Join(dir, "cover.jpg"), "jpeg", 1800, 766, 1)
	writeTestImage(t, filepath.Join(dir, "shop.jpg"), "jpeg", 1600, 900, 2)
	writeTestImage(t, filepath.Join(dir, "chart.png"), "png", 1600, 900, 3)
	refuse("illustration.missing-provenance", "illustration", "add", "post", "--id", "chart", "--content", "infographic", "--file", "chart.png", "--alt", "提前一周公布", "--origin", "agent")
	step("", "illustration", "add", "post", "--id", "cover", "--role", "cover", "--file", "cover.jpg", "--alt", "周六的会员日", "--origin", "agent", "--tool", "lemo-opuscar", "--style", "watercolor")
	step("", "illustration", "add", "post", "--id", "shop", "--file", "shop.jpg", "--alt", "店内一角", "--origin", "agent", "--tool", "lemo-opuscar", "--style", "watercolor")
	step("", "illustration", "add", "post", "--id", "chart", "--content", "infographic", "--claims", "claim-1", "--file", "chart.png", "--alt", "提前一周公布", "--origin", "agent", "--tool", "lemo-opuscar")
	// 登记了但还没插入正文：验收关卡只给 warning，不阻塞；插入引用后审稿过期，重申沿用原结论。
	illustrated := strings.Replace(draft, "没来得及安排时间。\n\n", "没来得及安排时间。\n\n![店内一角](illustrations/shop.jpg)\n\n", 1)
	illustrated = strings.Replace(illustrated, "本月有手冲体验课和新豆试饮。\n\n", "本月有手冲体验课和新豆试饮。\n\n![提前一周公布](illustrations/chart.png)\n\n", 1)
	step(illustrated, "draft", "write", "post")
	refuse("accept.review-stale", "accept", "post", "--confirmed-by-author", "--confirmation", "作者：图文都可以")
	step("", "review", "record", "post", "--reaffirm", "--summary", "插入 2 张配图引用")
	code, show := runWS(t, dir, "review", "show", "post")
	if code != 0 || !strings.Contains(show.stdout, `"ready":true`) {
		t.Fatalf("review show: %s", show.stdout)
	}
	step("", "accept", "post", "--confirmed-by-author", "--confirmation", "作者：图文都可以")
	if got := readFile(t, filepath.Join(dir, ".writing-anything", "works", "post", "acceptance.yaml")); !strings.Contains(got, "illustrations_sha256:") {
		t.Fatalf("acceptance.yaml: %s", got)
	}
	step("", "advance", "post")

	// 公众号 HTML：配图清单列出封面与两张配图的资产路径并标注 AI 生成；图片复制到资产目录。
	res := step("", "export", "post", "--format", "wechat-html")
	var out struct {
		Export struct {
			ImageList  *string `json:"imageList"`
			Assets     []map[string]any
			Disclosure *struct {
				Text       string `json:"text"`
				Paragraphs int    `json:"paragraphs"`
				AIImages   int    `json:"aiImages"`
				Images     int    `json:"images"`
			} `json:"disclosure"`
		} `json:"export"`
	}
	if err := json.Unmarshal([]byte(res.stdout), &out); err != nil {
		t.Fatal(err)
	}
	if out.Export.ImageList == nil || len(out.Export.Assets) != 3 {
		t.Fatalf("export = %s", res.stdout)
	}
	list := readFile(t, filepath.Join(dir, ".writing-anything", "exports", "post.images.md"))
	for _, want := range []string{"封面", "post.assets/post.cover.jpg", "post.assets/post.shop.jpg", "post.assets/post.chart.png", "AI 生成"} {
		if !strings.Contains(list, want) {
			t.Errorf("image list lacks %q:\n%s", want, list)
		}
	}
	for _, name := range []string{"post.cover.jpg", "post.shop.jpg", "post.chart.png"} {
		if _, err := os.Stat(filepath.Join(dir, ".writing-anything", "exports", "post.assets", name)); err != nil {
			t.Errorf("asset %s: %v", name, err)
		}
	}
	d := out.Export.Disclosure
	if d == nil || d.AIImages != 3 || d.Images != 3 || d.Paragraphs != 8 || !strings.Contains(d.Text, "文中 3 张配图由 AI 生成。") {
		t.Fatalf("disclosure = %+v（只含配图引用的段落不计入段落数）", d)
	}
	html := readFile(t, filepath.Join(dir, ".writing-anything", "exports", "post.html"))
	allowedHTML(t, html)
	if !strings.Contains(html, "【配图 1】店内一角") || !strings.Contains(html, "【配图 2】提前一周公布") {
		t.Errorf("wechat-html placeholders: %s", html)
	}

	// Markdown：封面写入文件头，引用改写为资产路径。
	step("", "export", "post", "--front-matter")
	md := readFile(t, filepath.Join(dir, ".writing-anything", "exports", "post.md"))
	if !strings.Contains(md, "cover: post.assets/post.cover.jpg") || !strings.Contains(md, "![店内一角](post.assets/post.shop.jpg)") || strings.Contains(md, "](illustrations/") {
		t.Errorf("markdown export:\n%s", md)
	}
	step("", "validate")

	// 验收后替换配图：验收失效，导出拒绝并给出 reopen。
	writeTestImage(t, filepath.Join(dir, "shop2.jpg"), "jpeg", 1600, 900, 9)
	step("", "illustration", "add", "post", "--id", "shop", "--file", "shop2.jpg", "--alt", "店内一角", "--origin", "agent", "--replace")
	code, exp := runWS(t, dir, "export", "post")
	if code != 1 || diag(t, exp, "export.nothing-accepted").Code == "" || !strings.Contains(exp.stdout, "wa-cli reopen post") {
		t.Fatalf("export after replace: %d %s", code, exp.stdout)
	}
}

// 正文全部出自作者、Agent 只插入了配图引用段落时，AI 参与说明只含配图说明（ai-disclosure 的 image-only-paragraphs-not-counted）。
func TestImageOnlyParagraphsNotCounted(t *testing.T) {
	dir := newWork(t)
	unit := filepath.Join(dir, ".writing-anything", "works", "my-post")
	writeFile(t, filepath.Join(unit, "brief.yaml"), completeBrief)
	mustRunWS(t, dir, "advance", "my-post")
	body := "作者自己写的第一段。\n\n作者自己写的第二段。\n"
	writeFile(t, filepath.Join(unit, "draft.md"), body)
	mustRunWS(t, dir, "advance", "my-post")
	writeTestImage(t, filepath.Join(dir, "shop.jpg"), "jpeg", 16, 9, 1)
	if code, out := addIllustration(t, dir, "my-post", "--id", "shop", "--file", "shop.jpg", "--alt", "店内一角", "--origin", "agent"); code != 0 {
		t.Fatal(out)
	}
	_, diff := runWS(t, dir, "diff", "my-post")
	var d struct {
		Diff struct {
			Base string `json:"base"`
		} `json:"diff"`
	}
	if err := json.Unmarshal([]byte(diff.stdout), &d); err != nil || d.Diff.Base == "" {
		t.Fatalf("diff: %s", diff.stdout)
	}
	if code, res := runWSIn(t, dir, body+"\n![店内一角](illustrations/shop.jpg)\n", "draft", "write", "my-post", "--base", d.Diff.Base); code != 0 {
		t.Fatalf("draft write: %+v", res)
	}
	if code, res := runWSIn(t, dir, "rubric:\n  - {item: clarity, verdict: pass, evidence: 清楚, paragraphs: [1]}\n", "review", "record", "my-post"); code != 0 {
		t.Fatalf("review record: %+v", res)
	}
	mustRunWS(t, dir, "accept", "my-post")
	code, out := runExportJSON(t, dir, "my-post")
	if code != 0 || out.Export.Disclosure == nil {
		t.Fatalf("export: %d %s", code, out.stdout)
	}
	if !strings.Contains(out.stdout, `"aiParagraphs":0`) || !strings.Contains(out.stdout, `"paragraphs":2`) || !strings.Contains(out.stdout, `"text":"文中 1 张配图由 AI 生成。"`) {
		t.Errorf("disclosure: %s", out.stdout)
	}
}
