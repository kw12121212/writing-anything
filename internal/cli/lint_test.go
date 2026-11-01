package cli

import (
	"encoding/json"
	"path/filepath"
	"strings"
	"testing"
)

type lintOut struct {
	result
	Findings []map[string]any `json:"findings"`
	Waived   []map[string]any `json:"waived"`
	Chars    int              `json:"chars"`
	CJKChars int              `json:"cjkChars"`
}

const draftRel = ".writing-anything/works/my-post/draft.md"

func runLintJSON(t *testing.T, dir, draft string) (int, lintOut) {
	t.Helper()
	if draft != "" {
		writeFile(t, wsPath(dir, draftRel), draft)
	}
	code, res := runWS(t, dir, "lint", "my-post")
	var out lintOut
	if err := json.Unmarshal([]byte(res.stdout), &out); err != nil {
		t.Fatal(err)
	}
	out.result = res
	return code, out
}

func findingsOf(fs []map[string]any, rule string) []map[string]any {
	var out []map[string]any
	for _, f := range fs {
		if f["rule"] == rule {
			out = append(out, f)
		}
	}
	return out
}

func TestLint(t *testing.T) {
	dir := newWork(t)
	// lint-no-draft
	if code, res := runLintJSON(t, dir, ""); code != 1 || !hasNext(diag(t, res.result, "draft.not-found"), "wa-cli help draft write --json") || len(res.Findings) != 0 {
		t.Fatalf("lint-no-draft: exit code = %d", code)
	}
	// lint-clean：字数 10（非空白字符数，含标点）、汉字 9；minimal 的 length 规则报告字数不足（warning），不影响退出码。
	code, res := runLintJSON(t, dir, "连接池可以降低延迟。")
	if code != 0 || !res.Valid || res.Chars != 10 || res.CJKChars != 9 || len(res.Waived) != 0 || len(res.ChangedPaths) != 0 {
		t.Fatalf("lint-clean: exit code = %d, result = %+v", code, res)
	}
	if l := findingsOf(res.Findings, "length"); len(l) != 1 || l[0]["line"] != nil || !strings.Contains(l[0]["message"].(string), "300") || len(res.Findings) != 1 {
		t.Fatalf("length-range: %v", res.Findings)
	}
	// lint-error
	code, res = runLintJSON(t, dir, "值得注意的是，连接池很重要。")
	f := findingsOf(res.Findings, "ai-cliche")
	if d := diag(t, res.result, "lint.findings"); code != 1 || res.Valid || len(f) != 1 || f[0]["severity"] != "error" || f[0]["source"] != "pack:default" ||
		f[0]["line"] != 1.0 || f[0]["column"] != 1.0 || f[0]["text"] != "值得注意的是" || d.Path != draftRel ||
		!hasNext(d, "wa-cli help draft write --json") || !hasNext(d, "wa-cli lint my-post") || !strings.Contains(d.Message, "1 条") {
		t.Fatalf("lint-error: exit code = %d, result = %+v", code, res)
	}
	// lint-warning-only
	code, res = runLintJSON(t, dir, "使用Go语言。")
	if c := findingsOf(res.Findings, "cjk-typography"); code != 0 || !res.Valid || len(c) != 2 || c[0]["severity"] != "warning" || c[0]["suggestion"] != "用 G" {
		t.Fatalf("lint-warning-only: exit code = %d, result = %+v", code, res)
	}
	// skip-code 与 skip-url
	if _, res = runLintJSON(t, dir, "```\n值得注意的是\n```\n\n`值得注意的是` 见 https://example.com/a?q=1\n"); len(findingsOf(res.Findings, "ai-cliche")) != 0 || len(findingsOf(res.Findings, "cjk-typography")) != 0 {
		t.Fatalf("skip-code: %v", res.Findings)
	}
	// skip-url：URL 在非 ASCII 字符处结束，紧随其后的中文照常检查。
	if _, res = runLintJSON(t, dir, "见 https://example.com，值得注意的是"); len(findingsOf(res.Findings, "ai-cliche")) != 1 || len(findingsOf(res.Findings, "cjk-typography")) != 0 {
		t.Fatalf("skip-url: %v", res.Findings)
	}
	// waive-paragraph 与 waiver-without-reason
	code, res = runLintJSON(t, dir, "值得注意的是，这是作者原话。<!-- lint-ignore ai-cliche: 引用作者原话 -->")
	if code != 0 || len(findingsOf(res.Findings, "ai-cliche")) != 0 || len(res.Waived) != 1 || res.Waived[0]["reason"] != "引用作者原话" {
		t.Fatalf("waive-paragraph: exit code = %d, result = %+v", code, res)
	}
	code, res = runLintJSON(t, dir, "值得注意的是。<!-- lint-ignore ai-cliche -->")
	if d := diag(t, res.result, "lint.invalid-waiver"); code != 1 || d.Severity != "warning" || !hasNext(d, "wa-cli help lint --json") || len(findingsOf(res.Findings, "ai-cliche")) != 1 {
		t.Fatalf("waiver-without-reason: exit code = %d, result = %+v", code, res)
	}
	// waive-next-paragraph
	code, res = runLintJSON(t, dir, "<!-- lint-ignore ai-cliche: 原话 -->\n\n值得注意的是一。\n\n值得注意的是二。")
	if a := findingsOf(res.Findings, "ai-cliche"); code != 1 || len(res.Waived) != 1 || res.Waived[0]["line"] != 3.0 || len(a) != 1 || a[0]["line"] != 5.0 {
		t.Fatalf("waive-next-paragraph: exit code = %d, result = %+v", code, res)
	}
	// 人读输出
	code, stdout, _ := run(t, "lint", "my-post", "--dir", dir)
	if code != 1 || !strings.Contains(stdout, "第 5 行第 1 列 [error ai-cliche]") || !strings.Contains(stdout, "1 条已豁免") {
		t.Fatalf("text: exit code = %d, stdout = %q", code, stdout)
	}
	writeFile(t, wsPath(dir, draftRel), "使用Go语言")
	if _, stdout, _ := run(t, "lint", "my-post", "--dir", dir); !strings.Contains(stdout, "建议：用 G") || !strings.Contains(stdout, "全文 [warning length]") {
		t.Fatalf("text suggestions: %q", stdout)
	}
	// lint-waived-rule
	writeFile(t, configPath(dir), readFile(t, configPath(dir))+"waivers:\n  - {rule: ai-cliche, reason: 本文引用原话}\n")
	if code, res := runLintJSON(t, dir, "值得注意的是"); code != 0 || len(findingsOf(res.Findings, "ai-cliche")) != 0 {
		t.Fatalf("lint-waived-rule: exit code = %d, result = %+v", code, res)
	}
	if code, res := runWS(t, t.TempDir(), "lint", "my-post"); code != 1 || diag(t, res, "workspace.not-found").Code == "" {
		t.Fatalf("no workspace: exit code = %d", code)
	}
}

func TestLintConfiguredRules(t *testing.T) {
	dir := newWork(t)
	writePack(t, dir, "house", "schema_version: 1\nid: house\ntitle: 团队\nglossary:\n  - {term: 规范包, avoid: [配置包]}\nbanned_terms:\n  - {term: 赋能, replacement: 帮助, reason: 空泛}\n  - {term: Go}\n")
	mustRunWS(t, dir, "config", "set", "packs", "house,default")
	code, res := runLintJSON(t, dir, "赋能团队，Google 与 Go 语言，配置包。")
	b, tc := findingsOf(res.Findings, "banned-terms"), findingsOf(res.Findings, "term-consistency")
	if code != 1 || len(b) != 2 || b[0]["text"] != "赋能" || b[0]["suggestion"] != "帮助" || !strings.Contains(b[0]["message"].(string), "空泛") || b[1]["column"] != 15.0 {
		t.Fatalf("banned-term: exit code = %d, findings = %v", code, res.Findings)
	}
	if len(tc) != 1 || tc[0]["suggestion"] != "规范包" || tc[0]["severity"] != "warning" {
		t.Fatalf("term-consistency: %v", tc)
	}
	// paragraph-length、required-sections 与 dash-density 由项目配置定义。
	writeFile(t, configPath(dir), readFile(t, configPath(dir))+"rules:\n  para: {type: paragraph-length, params: {max: 5}}\n  sections: {type: required-sections, params: {sections: [背景, 结论]}}\n  ai-cliche: {params: {max_dashes_per_1000: 1}}\n")
	_, res = runLintJSON(t, dir, "## 背景\n\n短。\n\n这一段有八个字——啊——")
	p, s, a := findingsOf(res.Findings, "para"), findingsOf(res.Findings, "sections"), findingsOf(res.Findings, "ai-cliche")
	if len(p) != 1 || p[0]["line"] != 5.0 || p[0]["column"] != 1.0 || p[0]["source"] != "project" {
		t.Fatalf("paragraph-length: %v", p)
	}
	if len(s) != 1 || s[0]["line"] != nil || !strings.Contains(s[0]["message"].(string), "结论") {
		t.Fatalf("required-sections: %v", s)
	}
	if len(a) != 1 || a[0]["line"] != nil || !strings.Contains(a[0]["message"].(string), "破折号密度") {
		t.Fatalf("dash-density: %v", a)
	}
	// invalid-params 与 missing-param
	writeFile(t, configPath(dir), strings.Replace(readFile(t, configPath(dir)), "max: 5}", "max: many}", 1))
	code, res = runLintJSON(t, dir, "")
	if d := diag(t, res.result, "rule.invalid-params"); code != 1 || d.Field != "rules.para.params.max" || !hasNext(d, "wa-cli config resolved") || len(findingsOf(res.Findings, "para")) != 0 {
		t.Fatalf("invalid-params: exit code = %d, result = %+v", code, res)
	}
	writeFile(t, configPath(dir), strings.Replace(readFile(t, configPath(dir)), "sections: {type: required-sections, params: {sections: [背景, 结论]}}", "sections: {type: required-sections}", 1))
	if _, res = runLintJSON(t, dir, ""); !strings.Contains(res.stdout, "rules.sections.params.sections") {
		t.Fatalf("missing-param: %+v", res.Diagnostics)
	}
	// 合并有 error 级问题时报告其诊断。
	mustRunWS(t, dir, "config", "set", "packs", "nope")
	if code, res := runLintJSON(t, dir, ""); code != 1 || diag(t, res.result, "config.unknown-pack").Code == "" {
		t.Fatalf("merge error: exit code = %d", code)
	}
}

// 内置 default 规范包的四条规则（default-pack-rules）。
func TestDefaultPackRules(t *testing.T) {
	_, res := runWS(t, newWorkspace(t), "config", "resolved")
	r := res.resolved(t)
	for id, sev := range map[string]string{"cjk-typography": "warning", "ai-cliche": "error", "banned-terms": "error", "term-consistency": "warning"} {
		if get(r, "rules."+id+".severity.value") != sev || get(r, "rules."+id+".severity.source") != "pack:default" {
			t.Errorf("rule %s = %v", id, get(r, "rules."+id))
		}
	}
}

// Agent 用 draft write 写入的豁免注释不生效；作者改写该段后生效（inline-waiver）。
func TestLintAgentWaiver(t *testing.T) {
	dir := newWork(t)
	body := "值得注意的是，这句是作者原话。<!-- lint-ignore ai-cliche: 作者原话 -->\n"
	if code, stdout, _ := runIn(t, body, "draft", "write", "my-post", "--dir", dir); code != 0 {
		t.Fatal(stdout)
	}
	code, res := runLintJSON(t, dir, "")
	if d := diag(t, res.result, "lint.agent-waiver"); code != 1 || d.Severity != "warning" || !hasNext(d, "wa-cli provenance my-post") || len(res.Waived) != 0 {
		t.Fatalf("agent waiver: exit code = %d, result = %+v", code, res)
	}
	// 写入记录损坏时无法推导出处：豁免不生效，并以 warning 报告写入记录的问题。
	record := wsPath(dir, ".writing-anything/works/my-post/provenance.yaml")
	good := readFile(t, record)
	writeFile(t, record, good+"schema_version: 99\n")
	if code, res := runLintJSON(t, dir, ""); code != 1 || len(res.Waived) != 0 || diag(t, res.result, "artifact.parse-error").Severity != "warning" {
		t.Fatalf("broken record: exit code = %d, result = %+v", code, res)
	}
	writeFile(t, record, good)
	// 作者直接修改这一段（改动了豁免注释），出处变为 agent-draft-human-edited，豁免生效。
	writeFile(t, wsPath(dir, draftRel), strings.Replace(body, "作者原话 -->", "我确认这是我的原话 -->", 1))
	if code, res := runLintJSON(t, dir, ""); code != 0 || len(res.Waived) != 1 || res.Waived[0]["reason"] != "我确认这是我的原话" {
		t.Fatalf("edited waiver: exit code = %d, result = %+v", code, res)
	}
}

// packs 不含 default 时，规范包的禁用词没有生效的规则，给出 warning；参数问题的 next 用 lint 复查。
func TestLintInactiveTerms(t *testing.T) {
	dir := newWork(t)
	writePack(t, dir, "house", "schema_version: 1\nid: house\ntitle: 团队\nbanned_terms:\n  - {term: 赋能}\n")
	mustRunWS(t, dir, "config", "set", "packs", "house")
	code, res := runLintJSON(t, dir, "赋能团队")
	if d := diag(t, res.result, "lint.inactive-terms"); code != 0 || d.Severity != "warning" || len(findingsOf(res.Findings, "banned-terms")) != 0 {
		t.Fatalf("inactive terms: exit code = %d, result = %+v", code, res)
	}
	writeFile(t, configPath(dir), readFile(t, configPath(dir))+"rules:\n  para: {type: paragraph-length, params: {max: many}}\n")
	if _, res := runLintJSON(t, dir, ""); !hasNext(diag(t, res.result, "rule.invalid-params"), "wa-cli lint my-post") {
		t.Fatalf("invalid params next: %+v", res.Diagnostics)
	}
}

// 论断出处检查（claim-rule-types）：minimal 文体的 claims（claim-sources）与 claim-candidates。
func TestLintClaims(t *testing.T) {
	dir := newWork(t)
	for _, src := range [][]string{{"src-a", "interview"}, {"src-r", "reference"}} {
		if code, stdout, _ := runIn(t, "作者的原话", "source", "add", "--type", src[1], "--id", src[0], "--dir", dir); code != 0 {
			t.Fatal(stdout)
		}
	}
	const sentence = "我们去年把连接池调到 50"
	mustRunWS(t, dir, "claim", "add", "my-post", "--kind", "experience", "--text", sentence)
	// claim-without-source
	code, res := runLintJSON(t, dir, "开头。"+sentence+"。")
	c := findingsOf(res.Findings, "claims")
	if code != 1 || len(c) != 1 || c[0]["severity"] != "error" || c[0]["line"] != 1.0 || c[0]["column"] != 4.0 || !strings.Contains(c[0]["message"].(string), "wa-cli claim set my-post claim-1") ||
		!hasNext(diag(t, res.result, "lint.findings"), "wa-cli lint my-post") || !hasNext(diag(t, res.result, "lint.findings"), "wa-cli help claim set --json") ||
		!hasNext(diag(t, res.result, "lint.findings"), "wa-cli claim list my-post") {
		t.Fatalf("claim-without-source: exit code = %d, findings = %v", code, res.Findings)
	}
	// 该句同时是亲历候选，但已登记为论断，不报告 claim-candidates。
	if len(findingsOf(res.Findings, "claim-candidates")) != 0 {
		t.Fatalf("covered candidate: %v", res.Findings)
	}
	// claim-with-reference-only：写入校验会拒绝新造这种论断，这里模拟旧工作区遗留的误分类状态（lint 仍须报告）。
	mustRunWS(t, dir, "claim", "add", "my-post", "--kind", "fact", "--text", "数据", "--sources", "src-r")
	writeFile(t, wsPath(dir, ".writing-anything/works/my-post/claims.yaml"),
		strings.Replace(readFile(t, wsPath(dir, ".writing-anything/works/my-post/claims.yaml")), "kind: fact", "kind: data", 1))
	_, res = runLintJSON(t, dir, "")
	if c := findingsOf(res.Findings, "claims"); len(c) != 2 || !strings.Contains(c[0]["message"].(string)+c[1]["message"].(string), "src-r（reference）") {
		t.Fatalf("claim-with-reference-only: %v", c)
	}
	// claim-with-author-source 与 fact-claim-not-required
	claimsFile := wsPath(dir, ".writing-anything/works/my-post/claims.yaml")
	mustRunWS(t, dir, "claim", "set", "my-post", "claim-1", "--sources", "src-a")
	mustRunWS(t, dir, "claim", "set", "my-post", "claim-2", "--kind", "fact", "--text", "事实", "--sources", "")
	if code, res := runLintJSON(t, dir, ""); code != 0 || len(findingsOf(res.Findings, "claims")) != 0 {
		t.Fatalf("claim-with-author-source: exit code = %d, findings = %v", code, res.Findings)
	}
	// experience-candidate、generic-we、data-candidate
	code, res = runLintJSON(t, dir, "我曾经在生产环境踩过这个坑。我们可以看到连接池的作用。延迟降低了 90%。")
	cc := findingsOf(res.Findings, "claim-candidates")
	if code != 0 || len(cc) != 2 || cc[0]["severity"] != "warning" || cc[0]["text"] != "我曾经在生产环境踩过这个坑。" || !strings.Contains(cc[1]["message"].(string), "数据") {
		t.Fatalf("candidates: exit code = %d, findings = %v", code, cc)
	}
	// invalid-claims
	writeFile(t, claimsFile, "k: v\n")
	if code, res := runLintJSON(t, dir, ""); code != 1 || diag(t, res.result, "artifact.schema-violation").Path != ".writing-anything/works/my-post/claims.yaml" || len(findingsOf(res.Findings, "claim-candidates")) != 0 {
		t.Fatalf("invalid-claims: exit code = %d, result = %+v", code, res)
	}
	// 两条论断规则都被豁免时不读取 claims.yaml。
	writeFile(t, configPath(dir), readFile(t, configPath(dir))+"waivers:\n  - {rule: claims, reason: 小说}\n  - {rule: claim-candidates, reason: 小说}\n")
	if code, res := runLintJSON(t, dir, ""); code != 0 || len(res.Diagnostics) != 0 {
		t.Fatalf("claim rules waived: exit code = %d, diagnostics = %+v", code, res.Diagnostics)
	}
}

// 广告法极限词检查（ad-compliance-rule-type）：项目配置启用、规范包扩展行业词表、单处豁免、参数校验与 tech-blog 默认关闭。
func TestLintAdCompliance(t *testing.T) {
	dir := newWork(t)
	base := readFile(t, configPath(dir))
	writeFile(t, configPath(dir), base+"rules:\n  ads: {type: ad-compliance, severity: error}\n")
	code, res := runLintJSON(t, dir, "这是全网最好的咖啡机。")
	f := findingsOf(res.Findings, "ads")
	if code != 1 || len(f) != 1 || f[0]["type"] != "ad-compliance" || f[0]["source"] != "project" || f[0]["text"] != "最好" || f[0]["suggestion"] != nil ||
		!strings.Contains(f[0]["message"].(string), "《中华人民共和国广告法》第九条第（三）项") || !strings.Contains(f[0]["message"].(string), "辅助检查，不构成法律意见") ||
		diag(t, res.result, "lint.findings").Code == "" {
		t.Fatalf("builtin-superlative: exit code = %d, result = %+v", code, res)
	}
	// waive-superlative
	code, res = runLintJSON(t, dir, "本店是国家级非遗老字号。<!-- lint-ignore ads: 已取得国家级非遗认定 -->")
	if len(findingsOf(res.Findings, "ads")) != 0 || len(res.Waived) != 1 || res.Waived[0]["reason"] != "已取得国家级非遗认定" ||
		res.Waived[0]["rule"] != "ads" || res.Waived[0]["text"] != "国家级" {
		t.Fatalf("waive-superlative: exit code = %d, result = %+v", code, res)
	}
	// invalid-basis
	writeFile(t, configPath(dir), base+"rules:\n  ads: {type: ad-compliance, params: {basis: 3}}\n")
	if code, res := runLintJSON(t, dir, "最好"); code != 1 || diag(t, res.result, "rule.invalid-params").Field != "rules.ads.params.basis" || len(findingsOf(res.Findings, "ads")) != 0 {
		t.Fatalf("invalid-basis: exit code = %d, result = %+v", code, res)
	}
	// empty-ad-terms：不用内置词表又没有追加词时按参数问题报告。
	writeFile(t, configPath(dir), base+"rules:\n  ads: {type: ad-compliance, params: {builtin: false}}\n")
	if code, res := runLintJSON(t, dir, "最好"); code != 1 || diag(t, res.result, "rule.invalid-params").Field != "rules.ads.params.terms" {
		t.Fatalf("empty-ad-terms: exit code = %d, result = %+v", code, res)
	}
	// pack-industry-terms
	writeFile(t, configPath(dir), base)
	writePack(t, dir, "medical", "schema_version: 1\nid: medical\ntitle: 医疗\nrules:\n  ad-medical:\n    type: ad-compliance\n    severity: error\n    params: {builtin: false, terms: [根治], basis: 《医疗广告管理办法》}\n")
	mustRunWS(t, dir, "config", "set", "packs", "default,medical")
	code, res = runLintJSON(t, dir, "三天根治，效果最佳。")
	f = findingsOf(res.Findings, "ad-medical")
	if code != 1 || len(f) != 1 || f[0]["source"] != "pack:medical" || f[0]["text"] != "根治" || !strings.Contains(f[0]["message"].(string), "《医疗广告管理办法》") {
		t.Fatalf("pack-industry-terms: exit code = %d, result = %+v", code, res)
	}
	for _, x := range res.Findings {
		if x["text"] == "最佳" {
			t.Fatalf("builtin false: %v", res.Findings)
		}
	}
}

// tech-blog 文体与 default 规范包不声明 ad-compliance（tech-blog-off）。
func TestLintAdComplianceTechBlogOff(t *testing.T) {
	dir := t.TempDir()
	mustRunWS(t, dir, "init", "--genre", "tech-blog")
	mustRunWS(t, dir, "new", "tech-blog", "my-post")
	_, res := runLintJSON(t, dir, "这是最佳实践。")
	for _, f := range res.Findings {
		if f["type"] == "ad-compliance" {
			t.Fatalf("tech-blog-off: %v", res.Findings)
		}
	}
	// 规则确实执行了：tech-blog 的 length 规则报告字数不足。
	if len(findingsOf(res.Findings, "length")) != 1 {
		t.Fatalf("rules did not run: %v", res.Findings)
	}
}

// length-counting-clarity：字数口径写明「非空白字符数，含标点」，lint --json 与文本摘要行输出汉字数
// （cjkChars，只数汉字）；length-range 的诊断 message 与 help 写明同一口径；brief set --length 的 help 同句。
func TestLintCountingClarity(t *testing.T) {
	dir := newWork(t)
	// 含标点、英文与空白的夹具：chars 数全部非空白字符（含标点与英文字母数字），cjkChars 只数汉字。
	code, res := runLintJSON(t, dir, "连接池，Go 的 10 个要点。")
	if code != 0 || res.Chars != 13 || res.CJKChars != 7 {
		t.Fatalf("mixed text: exit code = %d, chars = %d, cjkChars = %d", code, res.Chars, res.CJKChars)
	}
	l := findingsOf(res.Findings, "length")
	if len(l) != 1 || !strings.Contains(l[0]["message"].(string), "字数（Word Count）为 13（非空白字符数，含标点），少于下限 300") {
		t.Fatalf("length message should state the counting scope: %v", res.Findings)
	}
	// 屏蔽区域不计入两个口径：围栏代码块、URL 与 HTML 注释里的英文与汉字都不数。
	if code, res = runLintJSON(t, dir, "正文七个字。\n\n```\nGo 10 连接池\n```\n"); code != 0 || res.Chars != 6 || res.CJKChars != 5 {
		t.Fatalf("masked code block: exit code = %d, chars = %d, cjkChars = %d", code, res.Chars, res.CJKChars)
	}
	if code, res = runLintJSON(t, dir, "见字\n\nhttps://example.com/连接池Go\n"); code != 0 || res.Chars != 2 || res.CJKChars != 2 {
		t.Fatalf("masked url: exit code = %d, chars = %d, cjkChars = %d", code, res.Chars, res.CJKChars)
	}
	if code, res = runLintJSON(t, dir, "见字\n\n<!-- 连接池，Go -->\n"); code != 0 || res.Chars != 2 || res.CJKChars != 2 {
		t.Fatalf("masked comment: exit code = %d, chars = %d, cjkChars = %d", code, res.Chars, res.CJKChars)
	}
	// 文本摘要行同时显示字数（含口径）与汉字数。
	if code, out, _ := run(t, "lint", "my-post", "--dir", dir); code != 0 ||
		!strings.Contains(out, "字数 2（非空白字符数，含标点），汉字 2") {
		t.Fatalf("text summary line: exit code = %d, stdout = %s", code, out)
	}
	// help 与 brief set --length 的 help 写明同一口径。
	for _, doc := range helpDocs(t, "help", "lint", "--json") {
		if doc.Name != "lint" {
			continue
		}
		byKey := map[string]string{}
		for _, k := range doc.OutputKeys {
			byKey[k.Name] = k.Description
		}
		if !strings.Contains(byKey["chars"], "含标点") || !strings.Contains(byKey["cjkChars"], "汉字") {
			t.Fatalf("lint help output keys lack counting scope: %+v", doc.OutputKeys)
		}
	}
	for _, doc := range helpDocs(t, "help", "brief", "set", "--json") {
		if doc.Name != "brief set" {
			continue
		}
		for _, f := range doc.Flags {
			if f.Name == "length" && !strings.Contains(f.Description, "非空白字符数，含标点") {
				t.Fatalf("brief set --length help lacks counting scope: %+v", doc.Flags)
			}
		}
	}
}

// numeric-claim-candidates：counters 开启时「数字＋常见量词」是数据候选——wechat-marketing 报 warning、
// tech-blog 不报（参数未开启）、已登记并关联素材的论断覆盖时不报。
func TestLintCounters(t *testing.T) {
	// wechat-marketing：counters 随文体开启。
	dir := t.TempDir()
	mustRunWS(t, dir, "init", "--genre", "wechat-marketing")
	mustRunWS(t, dir, "new", "wechat-marketing", "post")
	_, res := runWS(t, dir, "config", "resolved")
	r := res.resolved(t)
	if get(r, "rules.claim-candidates.params.counters.value") != true {
		t.Fatalf("counters not enabled in wechat-marketing: %v", get(r, "rules.claim-candidates"))
	}
	writeFile(t, wsPath(dir, ".writing-anything/works/post/draft.md"), "共同好友 12 人都拿到了内测资格。\n")
	code, res := runWS(t, dir, "lint", "post")
	if code != 0 || !strings.Contains(res.stdout, `"rule":"claim-candidates"`) || !strings.Contains(res.stdout, "，数据）") ||
		!strings.Contains(res.stdout, "示意数字请在正文中注明") {
		t.Fatalf("wechat-marketing counter candidate: exit code = %d, stdout = %s", code, res.stdout)
	}
	// 已登记并关联素材的论断覆盖该数字时不报。
	writeFile(t, filepath.Join(dir, "note.md"), "作者：共同好友 12 人都拿到了内测资格。\n")
	mustRunWS(t, dir, "source", "add", "--type", "author-note", "--id", "note", "--file", "note.md")
	mustRunWS(t, dir, "claim", "add", "post", "--kind", "data", "--text", "共同好友 12 人都拿到了内测资格", "--sources", "note")
	if code, res = runWS(t, dir, "lint", "post"); code != 0 || strings.Contains(res.stdout, `"rule":"claim-candidates"`) {
		t.Fatalf("covered counter candidate: exit code = %d, stdout = %s", code, res.stdout)
	}
	// tech-blog：未开启 counters，「3 个步骤」不是数据候选。
	tb := t.TempDir()
	mustRunWS(t, tb, "init", "--genre", "tech-blog")
	mustRunWS(t, tb, "new", "tech-blog", "post")
	writeFile(t, wsPath(tb, ".writing-anything/works/post/draft.md"), "整个迁移分 3 个步骤就能完成。\n")
	if code, res = runWS(t, tb, "lint", "post"); code != 0 || strings.Contains(res.stdout, "claim-candidates") {
		t.Fatalf("tech-blog must not report counter candidates: exit code = %d, stdout = %s", code, res.stdout)
	}
}
