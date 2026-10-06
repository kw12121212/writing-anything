package cli

import (
	"encoding/json"
	"os"
	"path/filepath"
	"regexp"
	"slices"
	"strings"
	"testing"

	"github.com/kw12121212/writing-anything/internal/catalog"
	"github.com/kw12121212/writing-anything/internal/export"
)

type exportOut struct {
	result
	Export *exportView `json:"export"`
}

func runExportJSON(t *testing.T, dir string, args ...string) (int, exportOut) {
	t.Helper()
	code, res := runWS(t, dir, append([]string{"export"}, args...)...)
	var out exportOut
	if err := json.Unmarshal([]byte(res.stdout), &out); err != nil {
		t.Fatalf("%v: %v\n%s", args, err, res.stdout)
	}
	out.result = res
	return code, out
}

// acceptedWork 返回一个正文为 body、已审稿并验收的作品 my-post。
func acceptedWork(t *testing.T, body string) string {
	t.Helper()
	dir := newWork(t)
	writeFile(t, filepath.Join(dir, ".writing-anything", "works", "my-post", "brief.yaml"), completeBrief)
	mustRunWS(t, dir, "advance", "my-post")
	if code, res := runWSIn(t, dir, body, "draft", "write", "my-post"); code != 0 {
		t.Fatalf("draft write: %+v", res)
	}
	mustRunWS(t, dir, "advance", "my-post")
	if code, res := runWSIn(t, dir, "rubric:\n  - {item: clarity, verdict: pass, evidence: 清楚, paragraphs: [1]}\n", "review", "record", "my-post"); code != 0 {
		t.Fatalf("review record: %+v", res)
	}
	mustRunWS(t, dir, "accept", "my-post")
	return dir
}

var htmlTagRE = regexp.MustCompile(`<(/?)([A-Za-z][A-Za-z0-9]*)([^>]*)>`)

// allowedHTML 报告 HTML 中不在允许列表中的标签与 style 以外的属性（only-inline-styles）。
func allowedHTML(t *testing.T, html string) {
	t.Helper()
	allowed := []string{"section", "h1", "h2", "h3", "h4", "p", "strong", "em", "code", "pre", "blockquote", "ul", "ol", "li", "hr", "br", "sup"}
	for _, m := range htmlTagRE.FindAllStringSubmatch(html, -1) {
		if !slices.Contains(allowed, m[2]) {
			t.Errorf("tag %s not allowed", m[0])
		}
		if a := strings.TrimSpace(m[3]); a != "" && !regexp.MustCompile(`^style="[^"<>]*"$`).MatchString(a) {
			t.Errorf("attributes %q not allowed in %s", a, m[0])
		}
	}
	for _, bad := range []string{"<script", "<style", "<link", "class=", " id=", "href=", "src=", "onclick"} {
		if strings.Contains(html, bad) {
			t.Errorf("html contains %q", bad)
		}
	}
}

const wechatDraft = "# 会员日\n\n本月有**手冲体验课**，回复 `报名` 即可。<script>alert(1)</script>\n\n> 老顾客说想提前知道。\n\n- 时间：周六\n- 地点：本店\n\n" +
	"```text\n<b>不是标签</b>\n```\n\n详情见[活动页](https://example.com/day)。\n\n![店内实拍](shop.jpg \"周六的店\")\n\n行内![图标](icon.png)。\n"

func TestExportWechatHTML(t *testing.T) {
	dir := acceptedWork(t, wechatDraft)
	// only-inline-styles 与 image-placeholders
	code, res := runExportJSON(t, dir, "my-post", "--format", "wechat-html")
	const rel, list = ".writing-anything/exports/my-post.html", ".writing-anything/exports/my-post.images.md"
	if code != 0 || res.Export.Format != "wechat-html" || res.Export.Path != rel || res.Export.ImageList == nil || *res.Export.ImageList != list ||
		len(res.Export.Images) != 2 || res.Export.Images[0].Alt != "店内实拍" || *res.Export.Images[0].Title != "周六的店" || res.Export.Images[1].Title != nil ||
		!slices.Contains(res.ChangedPaths, rel) || !slices.Contains(res.ChangedPaths, list) {
		t.Fatalf("export: %d %+v", code, res)
	}
	html := readFile(t, wsPath(dir, rel))
	allowedHTML(t, html)
	for _, want := range []string{"&lt;script&gt;alert(1)&lt;/script&gt;", "&lt;b&gt;不是标签&lt;/b&gt;</pre>", "活动页<sup", "[1] https://example.com/day",
		"【配图 1】店内实拍", "周六的店", "【配图 2：图标】", ">会员日</h1>", "<strong style="} {
		if !strings.Contains(html, want) {
			t.Errorf("html lacks %q", want)
		}
	}
	if got := readFile(t, wsPath(dir, list)); got != "# 配图清单\n\n1. 店内实拍：shop.jpg（周六的店）\n2. 图标：icon.png\n" {
		t.Fatalf("image list: %q", got)
	}
	// 人读输出列出配图清单；没有配图时不写清单，imageList 为 null。
	if code, stdout, _ := run(t, "export", "my-post", "--format", "wechat-html", "--dir", dir); code != 0 || !strings.Contains(stdout, "配图清单（2 张）") {
		t.Fatalf("text: %s", stdout)
	}
	// Markdown 导出：images 为空数组、imageList 为 null（export-markdown）。
	if _, res := runExportJSON(t, dir, "my-post"); res.Export.Images == nil || len(res.Export.Images) != 0 || res.Export.ImageList != nil || res.Export.Format != "markdown" {
		t.Fatalf("markdown: %+v", res.Export)
	}
	// wechat-front-matter 与不支持的格式。
	code, res = runExportJSON(t, dir, "my-post", "--format", "wechat-html", "--front-matter")
	if d := diag(t, res.result, "input.invalid-value"); code != 1 || d.Field != "front-matter" || res.Export != nil {
		t.Fatalf("front matter: %+v", res.Diagnostics)
	}
	if code, res := runExportJSON(t, dir, "my-post", "--format", "pdf"); code != 1 || !strings.Contains(diag(t, res.result, "input.invalid-value").Message, "wechat-html") {
		t.Fatalf("format: %+v", res.Diagnostics)
	}
	// 配图清单的路径同样经过输出路径检查：清单落在符号链接上时两个文件都不写。
	if err := os.MkdirAll(wsPath(dir, "out"), 0o755); err != nil {
		t.Fatal(err)
	}
	writeFile(t, wsPath(dir, "out/elsewhere.md"), "")
	symlink(t, "elsewhere.md", wsPath(dir, "out/post.images.md"))
	if code, res := runExportJSON(t, dir, "my-post", "--format", "wechat-html", "--output", "out/post.html"); code != 1 || diag(t, res.result, "export.invalid-output").Code == "" {
		t.Fatalf("image list symlink: %+v", res.Diagnostics)
	}
	if fileExists(wsPath(dir, "out/post.html")) {
		t.Fatal("html written although the image list path is invalid")
	}
	if !strings.HasSuffix(export.ImageListPath("out/post.html"), "post.images.md") {
		t.Fatal("image list path")
	}
}

// 规范包的样式主题覆盖默认主题并记录来源；不安全的取值被拒绝（pack-html-theme、theme-override、theme-unsafe）。
func TestHTMLTheme(t *testing.T) {
	dir := acceptedWork(t, "## 小标题\n\n正文。\n")
	writePack(t, dir, "brand", "schema_version: 1\nid: brand\ntitle: 品牌\nhtml_theme:\n  h2: \"color: #c00\"\n")
	mustRunWS(t, dir, "config", "set", "packs", "default,brand")
	_, res := runWS(t, dir, "config", "resolved")
	r := res.resolved(t)
	if get(r, "htmlTheme.h2.value") != "color: #c00" || get(r, "htmlTheme.h2.source") != "pack:brand" || get(r, "htmlTheme.p.source") != "default" {
		t.Fatalf("resolved: %v", r["htmlTheme"])
	}
	if code, stdout, _ := run(t, "config", "resolved", "--dir", dir); code != 0 || !strings.Contains(stdout, "h2：color: #c00（来自 pack:brand）") {
		t.Fatalf("resolved text: %s", stdout)
	}
	mustRunWS(t, dir, "export", "my-post", "--format", "wechat-html")
	if html := readFile(t, wsPath(dir, ".writing-anything/exports/my-post.html")); !strings.Contains(html, `<h2 style="color: #c00">小标题</h2>`) {
		t.Fatalf("themed html: %s", html)
	}
	for name, value := range map[string]string{
		"url":        "background: url(http://x/a.png)",
		"expression": "width: expression(alert(1))",
		"quote":      `font-family: "x"`,
		"angle":      "color: red<",
		"no-colon":   "red",
		"at":         "color: @red",
	} {
		writePack(t, dir, "brand", "schema_version: 1\nid: brand\ntitle: 品牌\nhtml_theme:\n  p: '"+strings.ReplaceAll(value, "'", "''")+"'\n")
		if code, res := runWS(t, dir, "validate"); code != 1 || diag(t, res, "artifact.schema-violation").Field != "html_theme.p" {
			t.Errorf("%s: %d %+v", name, code, res.Diagnostics)
		}
	}
	writePack(t, dir, "brand", "schema_version: 1\nid: brand\ntitle: 品牌\nhtml_theme:\n  table: \"color: red\"\n")
	if code, res := runWS(t, dir, "validate"); code != 1 || diag(t, res, "artifact.schema-violation").Code == "" {
		t.Errorf("unknown element: %d %+v", code, res.Diagnostics)
	}
	// 内置默认主题的每个取值都符合规范包的取值格式。
	var b strings.Builder
	b.WriteString("schema_version: 1\nid: brand\ntitle: 品牌\nhtml_theme:\n")
	for k, v := range defaultThemeForTest() {
		b.WriteString("  " + k + ": '" + v + "'\n")
	}
	writePack(t, dir, "brand", b.String())
	if code, res := runWS(t, dir, "validate"); code != 0 {
		t.Fatalf("default theme as pack: %+v", res.Diagnostics)
	}
}

func symlink(t *testing.T, target, path string) {
	t.Helper()
	if err := os.Symlink(target, path); err != nil {
		t.Skip(err)
	}
}

func fileExists(path string) bool {
	_, err := os.Lstat(path)
	return err == nil
}

func defaultThemeForTest() map[string]string { return catalog.DefaultHTMLTheme }

// 新建的目录列在 changedPaths 中；没有配图时不写清单；未验收的单元跳过（wechat-html-export）。
func TestExportWechatPaths(t *testing.T) {
	dir := acceptedWork(t, wechatDraft)
	code, res := runExportJSON(t, dir, "my-post", "--format", "wechat-html", "--output", "dist/sub/post.html")
	if code != 0 || strings.Join(res.ChangedPaths, ",") != "dist,dist/sub,dist/sub/post.html,dist/sub/post.images.md" {
		t.Fatalf("changedPaths: %d %v %+v", code, res.ChangedPaths, res.Diagnostics)
	}
	dir = acceptedWork(t, "## 没有配图\n\n正文。\n")
	mustRunWS(t, dir, "unit", "add", "my-post", "section", "intro")
	code, res = runExportJSON(t, dir, "my-post", "--format", "wechat-html")
	if d := diag(t, res.result, "export.unaccepted"); code != 0 || res.Export.ImageList != nil || len(res.Export.Images) != 0 || d.Severity != "warning" ||
		strings.Join(res.ChangedPaths, ",") != ".writing-anything/exports,.writing-anything/exports/my-post.html" || fileExists(wsPath(dir, ".writing-anything/exports/my-post.images.md")) {
		t.Fatalf("no images: %d %+v", code, res)
	}
}

// 样式主题的合并顺序与不合法的声明位置（pack-html-theme）。
func TestHTMLThemeMerge(t *testing.T) {
	dir := newWorkspace(t)
	writePack(t, dir, "a", "schema_version: 1\nid: a\ntitle: A\nhtml_theme:\n  h2: \"color: #a00\"\n  p: \"margin: 0\"\n")
	writePack(t, dir, "b", "schema_version: 1\nid: b\ntitle: B\nhtml_theme:\n  h2: \"color: #b00\"\n")
	mustRunWS(t, dir, "config", "set", "packs", "a,b")
	_, res := runWS(t, dir, "config", "resolved")
	r := res.resolved(t)
	if get(r, "htmlTheme.h2.value") != "color: #b00" || get(r, "htmlTheme.h2.source") != "pack:b" || get(r, "htmlTheme.p.source") != "pack:a" {
		t.Fatalf("merge: %v", r["htmlTheme"])
	}
	// 不合法的取值：config resolved 也报告，resolved 为 null；未知的元素名指明字段。
	writePack(t, dir, "b", "schema_version: 1\nid: b\ntitle: B\nhtml_theme:\n  h2: \"background: url(x)\"\n  table: \"color: red\"\n")
	code, res := runWS(t, dir, "config", "resolved")
	if code != 1 || !strings.Contains(res.stdout, `"resolved":null`) || diag(t, res, "artifact.schema-violation").Code == "" {
		t.Fatalf("resolved with bad theme: %d %s", code, res.stdout)
	}
	fields := []string{}
	for _, d := range res.Diagnostics {
		fields = append(fields, d.Field)
	}
	if !slices.Contains(fields, "html_theme.h2") || !slices.Contains(fields, "html_theme.table") {
		t.Fatalf("fields: %v", fields)
	}
	// config.yaml 不能声明 html_theme。
	mustRunWS(t, dir, "config", "set", "packs", "a")
	writeFile(t, configPath(dir), readFile(t, configPath(dir))+"html_theme:\n  p: \"margin: 0\"\n")
	if code, res := runWS(t, dir, "validate"); code != 1 || diag(t, res, "artifact.schema-violation").Field != "html_theme" {
		t.Fatalf("config html_theme: %d %+v", code, res.Diagnostics)
	}
}

// acceptWork 在正文已写好的作品 my-post 上推进到审稿、记录审稿并验收；write 写入正文（Agent 或作者）。
func acceptWork(t *testing.T, write func(dir string)) string {
	t.Helper()
	dir := newWork(t)
	writeFile(t, filepath.Join(dir, ".writing-anything", "works", "my-post", "brief.yaml"), completeBrief)
	mustRunWS(t, dir, "advance", "my-post")
	write(dir)
	mustRunWS(t, dir, "advance", "my-post")
	if code, res := runWSIn(t, dir, "rubric:\n  - {item: clarity, verdict: pass, evidence: 清楚, paragraphs: [1]}\n", "review", "record", "my-post"); code != 0 {
		t.Fatalf("review record: %+v", res)
	}
	mustRunWS(t, dir, "accept", "my-post")
	return dir
}

// AI 参与说明（ai-disclosure）：按段落出处生成，放置位置与模板来自配置。
func TestAIDisclosure(t *testing.T) {
	const exported = ".writing-anything/exports/my-post.md"
	// agent-paragraphs：默认 both，front matter 与文末都有说明。
	dir := acceptedWork(t, "第一段。\n\n第二段。\n")
	code, res := runExportJSON(t, dir, "my-post", "--front-matter")
	if d := res.Export.Disclosure; code != 0 || d == nil || d.Placement != "both" || d.AIParagraphs != 2 || d.Paragraphs != 2 || d.AIPercent != 100 || d.Text != fullAIDisclosure {
		t.Fatalf("agent-paragraphs: %d %+v", code, res.Export)
	}
	if got := readFile(t, wsPath(dir, exported)); !strings.Contains(got, "ai_disclosure: "+fullAIDisclosure) || !strings.HasSuffix(got, "第二段。\n\n---\n\n"+fullAIDisclosure+"\n") {
		t.Fatalf("export: %s", got)
	}
	if code, stdout, _ := run(t, "export", "my-post", "--dir", dir); code != 0 || !strings.Contains(stdout, "AI 参与说明（both）") {
		t.Fatalf("text: %s", stdout)
	}
	// 没有配置时放置位置与模板都来自内置默认（resolved-ai-disclosure）。
	_, rd := runWS(t, dir, "config", "resolved")
	if r := rd.resolved(t); get(r, "aiDisclosure.placement.value") != "both" || get(r, "aiDisclosure.placement.source") != "default" || get(r, "aiDisclosure.template.source") != "default" {
		t.Fatalf("default disclosure: %v", r["aiDisclosure"])
	}
	// metadata：只写进 front matter；没有 --front-matter 时只在输出中给出。
	mustRunWS(t, dir, "config", "set", "ai_disclosure", "metadata")
	runExportJSON(t, dir, "my-post", "--front-matter")
	if got := readFile(t, wsPath(dir, exported)); !strings.Contains(got, "ai_disclosure:") || strings.Contains(got, "---\n\n本文") {
		t.Fatalf("metadata: %s", got)
	}
	if _, res := runExportJSON(t, dir, "my-post"); res.Export.Disclosure == nil || strings.Contains(readFile(t, wsPath(dir, exported)), "AI 辅助") {
		t.Fatalf("metadata without front matter: %+v", res.Export)
	}
	// footer-only-wechat：规范包的模板，公众号 HTML 文末的说明段落。
	mustRunWS(t, dir, "config", "set", "ai_disclosure", "footer")
	writePack(t, dir, "brand", "schema_version: 1\nid: brand\ntitle: 品牌\nai_disclosure_template: \"AI 参与了 {ai_paragraphs}/{paragraphs} 段（{ai_percent}%）\"\n")
	mustRunWS(t, dir, "config", "set", "packs", "default,brand")
	code, res = runExportJSON(t, dir, "my-post", "--format", "wechat-html")
	html := readFile(t, wsPath(dir, ".writing-anything/exports/my-post.html"))
	if code != 0 || res.Export.Disclosure.Text != "AI 参与了 2/2 段（100%）" || !strings.HasSuffix(html, ">AI 参与了 2/2 段（100%）</p></section>\n") {
		t.Fatalf("wechat footer: %+v\n%s", res.Export.Disclosure, html)
	}
	allowedHTML(t, html)
	// resolved-ai-disclosure
	_, rr := runWS(t, dir, "config", "resolved")
	r := rr.resolved(t)
	if get(r, "aiDisclosure.placement.value") != "footer" || get(r, "aiDisclosure.placement.source") != "project" || get(r, "aiDisclosure.template.source") != "pack:brand" {
		t.Fatalf("resolved: %v", r["aiDisclosure"])
	}
	if _, stdout, _ := run(t, "config", "resolved", "--dir", dir); !strings.Contains(stdout, "AI 参与说明（AI-generated Content Label）：放在 footer（来自 project）") {
		t.Fatalf("resolved text: %s", stdout)
	}
	// bad-template
	writePack(t, dir, "brand", "schema_version: 1\nid: brand\ntitle: 品牌\nai_disclosure_template: \"{model} 写的\"\n")
	if code, res := runWS(t, dir, "validate"); code != 1 || diag(t, res, "artifact.schema-violation").Field != "ai_disclosure_template" {
		t.Fatalf("bad-template: %d %+v", code, res.Diagnostics)
	}
	// unknown-provenance：写入记录损坏时给出 warning，全部段落算作 AI 参与。
	writePack(t, dir, "brand", "schema_version: 1\nid: brand\ntitle: 品牌\n")
	writeFile(t, wsPath(dir, ".writing-anything/works/my-post/provenance.yaml"), "schema_version: 99\n")
	code, res = runExportJSON(t, dir, "my-post")
	if d := diag(t, res.result, "export.unknown-provenance"); code != 0 || d.Severity != "warning" || !hasNext(d, "wa-cli provenance my-post") ||
		res.Export.Disclosure.AIParagraphs != res.Export.Disclosure.Paragraphs {
		t.Fatalf("unknown-provenance: %+v", res)
	}
}

func TestAIDisclosureOrigins(t *testing.T) {
	// human-only：作者直接写入的正文（没有写入记录）不生成说明。
	dir := acceptWork(t, func(dir string) {
		writeFile(t, wsPath(dir, draftRel), "作者写的第一段。\n\n作者写的第二段。\n")
	})
	if code, res := runExportJSON(t, dir, "my-post", "--front-matter"); code != 0 || res.Export.Disclosure != nil ||
		strings.Contains(readFile(t, wsPath(dir, ".writing-anything/exports/my-post.md")), "ai_disclosure") {
		t.Fatalf("human-only: %+v", res.Export)
	}
	// edited-paragraph：作者改写的 Agent 段落也算 AI 参与，作者新加的段落不算。
	dir = acceptWork(t, func(dir string) {
		if code, res := runWSIn(t, dir, "甲段。\n\n乙段。\n", "draft", "write", "my-post"); code != 0 {
			t.Fatalf("draft write: %+v", res)
		}
		writeFile(t, wsPath(dir, draftRel), "甲段改过了。\n\n乙段。\n\n作者新加的丙段。\n")
	})
	if _, res := runExportJSON(t, dir, "my-post"); res.Export.Disclosure == nil || res.Export.Disclosure.AIParagraphs != 2 || res.Export.Disclosure.Paragraphs != 3 {
		t.Fatalf("edited-paragraph: %+v", res.Export.Disclosure)
	}
	// set-ai-disclosure：只接受三个取值；空字符串删除该项。
	if code, res := runWS(t, dir, "config", "set", "ai_disclosure", "never"); code != 1 || diag(t, res, "artifact.schema-violation").Field != "ai_disclosure" {
		t.Fatalf("bad value: %+v", res.Diagnostics)
	}
	mustRunWS(t, dir, "config", "set", "ai_disclosure", "both")
	if _, res := runWS(t, dir, "config", "set", "ai_disclosure", ""); strings.Contains(readFile(t, configPath(dir)), "ai_disclosure") || res.config(t)["aiDisclosure"] != nil {
		t.Fatalf("delete: %s", readFile(t, configPath(dir)))
	}
}

// 多个单元的汇总、四舍五入的比例、只放文末的 Markdown，以及没有写入记录但段落数超过推导上限的正文（ai-disclosure）。
func TestAIDisclosureTotals(t *testing.T) {
	dir := acceptedWork(t, "第一段。\n\n第二段。\n") // 两段出自 Agent，共 8 字
	mustRunWS(t, dir, "unit", "add", "my-post", "section", "intro")
	intro := ".writing-anything/works/my-post/units/intro"
	if err := os.MkdirAll(wsPath(dir, intro), 0o755); err != nil {
		t.Fatal(err)
	}
	writeFile(t, wsPath(dir, intro+"/brief.yaml"), "schema_version: 1\n")
	mustRunWS(t, dir, "advance", "my-post/intro")
	writeFile(t, wsPath(dir, intro+"/draft.md"), "作者的段落\n") // 作者写的 5 字
	mustRunWS(t, dir, "advance", "my-post/intro")
	if code, res := runWSIn(t, dir, "rubric:\n  - {item: clarity, verdict: pass, evidence: 清楚, paragraphs: [1]}\n", "review", "record", "my-post/intro"); code != 0 {
		t.Fatalf("review record: %+v", res)
	}
	mustRunWS(t, dir, "accept", "my-post/intro")
	mustRunWS(t, dir, "config", "set", "ai_disclosure", "footer")
	code, res := runExportJSON(t, dir, "my-post", "--front-matter")
	if d := res.Export.Disclosure; code != 0 || d == nil || d.AIParagraphs != 2 || d.Paragraphs != 3 || d.AIPercent != 62 {
		t.Fatalf("totals: %d %+v", code, res.Export)
	}
	got := readFile(t, wsPath(dir, ".writing-anything/exports/my-post.md"))
	if strings.Contains(got, "ai_disclosure") || !strings.HasSuffix(got, "作者的段落\n\n---\n\n本文在 AI 辅助下完成：约 62% 的内容由 AI 起草，均经作者审阅确认。\n") {
		t.Fatalf("footer only: %s", got)
	}
	// 作者写的正文段落数超过出处推导的上限：没有写入记录，仍然全部出自作者，不生成说明也不报告 unknown-provenance。
	dir = acceptWork(t, func(dir string) {
		writeFile(t, wsPath(dir, draftRel), strings.Repeat("段。\n\n", 10001))
	})
	code, res = runExportJSON(t, dir, "my-post")
	if code != 0 || res.Export.Disclosure != nil || strings.Contains(res.stdout, "export.unknown-provenance") {
		t.Fatalf("large human draft: %d %+v", code, res.Diagnostics)
	}
}

// 不合法的模板：validate 与 config resolved 都报告（多行、过长、未知占位符）。
func TestAIDisclosureTemplateRules(t *testing.T) {
	dir := newWorkspace(t)
	mustRunWS(t, dir, "config", "set", "packs", "default,brand")
	for name, tmpl := range map[string]string{
		"placeholder": `"{model} 写的"`,
		"multiline":   `"第一行\n---\ntitle: x"`,
		"too long":    `"` + strings.Repeat("长", 501) + `"`,
		"blank":       `"  "`,
	} {
		writePack(t, dir, "brand", "schema_version: 1\nid: brand\ntitle: 品牌\nai_disclosure_template: "+tmpl+"\n")
		for _, args := range [][]string{{"validate"}, {"config", "resolved"}} {
			if code, res := runWS(t, dir, args...); code != 1 || diag(t, res, "artifact.schema-violation").Field != "ai_disclosure_template" {
				t.Errorf("%s %v: %d %+v", name, args, code, res.Diagnostics)
			}
		}
	}
}
