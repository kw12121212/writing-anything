package cli

import (
	"encoding/json"
	"os"
	"path/filepath"
	"slices"
	"strings"
	"testing"
	"time"
)

// AI 腔（AI Tone）防护的命令级场景：写作简报的 voice（brief/brief-voice）、上下文包的 voice 与文风（context/context-voice）、
// 内置文章类文体的 tone 规则与 natural-voice 指标（genres-and-packs/article-tone-rules、article-natural-voice-metric）。

func TestBriefVoice(t *testing.T) {
	dir := newWork(t)
	writeFile(t, wsPath(dir, briefRel), "# 作者的注释\n"+completeBrief)
	// set-voice：voice 放在 purpose 之后，其他字段与注释不变。
	code, res := runBrief(t, dir, "brief", "set", "my-post", "--voice", "像跟同事聊天，第一人称")
	if code != 0 || !res.Valid || res.Brief["voice"] != "像跟同事聊天，第一人称" || !slices.Equal(res.ChangedPaths, []string{briefRel}) {
		t.Fatalf("set-voice: exit code = %d, result = %+v", code, res)
	}
	want := "# 作者的注释\nschema_version: 1\naudience: 后端工程师\npurpose: 讲清楚为什么要做连接池\nvoice: 像跟同事聊天，第一人称\nprovenance:\n"
	if got := readFile(t, wsPath(dir, briefRel)); !strings.HasPrefix(got, want) {
		t.Fatalf("brief.yaml =\n%s", got)
	}
	// 取值不变时不写文件；voice 不是出处映射的键，--decision 不为它补位。
	if code, res := runBrief(t, dir, "brief", "set", "my-post", "--voice", "像跟同事聊天，第一人称"); code != 0 || len(res.ChangedPaths) != 0 {
		t.Fatalf("unchanged: exit code = %d, result = %+v", code, res)
	}
	if code, res := runBrief(t, dir, "brief", "set", "my-post", "--voice", "口语", "--decision", "dec-1"); code != 1 || diag(t, res.result, "input.invalid-value").Field != "decision" {
		t.Fatalf("decision fill: exit code = %d, result = %+v", code, res.Diagnostics)
	}
	// 人读输出显示口吻。
	if code, text, _ := run(t, "brief", "show", "my-post", "--dir", dir); code != 0 || !strings.Contains(text, "口吻：像跟同事聊天，第一人称") {
		t.Fatalf("show text: %s", text)
	}
	// blank-voice：只含空白时报告 input.invalid-value，brief.yaml 不变。
	before := readFile(t, wsPath(dir, briefRel))
	if code, res := runBrief(t, dir, "brief", "set", "my-post", "--voice", "  "); code != 1 || diag(t, res.result, "input.invalid-value").Path != "" ||
		!strings.Contains(diag(t, res.result, "input.invalid-value").Message, "--voice") || !hasNext(diag(t, res.result, "input.invalid-value"), "wa-cli help brief set --json") {
		t.Fatalf("blank-voice: exit code = %d, result = %+v", code, res.Diagnostics)
	}
	if readFile(t, wsPath(dir, briefRel)) != before {
		t.Fatal("blank-voice changed brief.yaml")
	}
	// clear-voice：空字符串删除 voice。
	code, res = runBrief(t, dir, "brief", "set", "my-post", "--voice", "")
	if code != 0 || res.Brief["voice"] != nil || strings.Contains(readFile(t, wsPath(dir, briefRel)), "voice") {
		t.Fatalf("clear-voice: exit code = %d, brief = %v", code, res.Brief)
	}
	// show-voice-null：没有 voice 时为 null，questions 中没有与 voice 有关的问题。
	code, res = runBrief(t, dir, "brief", "show", "my-post")
	if v, ok := res.Brief["voice"]; code != 0 || !ok || v != nil {
		t.Fatalf("show-voice-null: %v", res.Brief)
	}
	for _, q := range res.Questions {
		if q["field"] == "voice" {
			t.Fatalf("question about voice: %v", q)
		}
	}
	// validate-bad-voice：voice 为空字符串时 Schema 校验报告 artifact.schema-violation。
	writeFile(t, wsPath(dir, briefRel), completeBrief+"voice: \"\"\n")
	code, vr := runWS(t, dir, "validate")
	if d := diag(t, vr, "artifact.schema-violation"); code != 1 || d.Field != "voice" || d.Path != briefRel {
		t.Fatalf("validate-bad-voice: exit code = %d, %+v", code, vr.Diagnostics)
	}
}

// techBlogWork 返回 packs 为空、default_genre 为 tech-blog、含作品 my-post 的工作区。
func techBlogWork(t *testing.T) string {
	t.Helper()
	dir := t.TempDir()
	mustRunWS(t, dir, "init", "--genre", "tech-blog")
	mustRunWS(t, dir, "new", "tech-blog", "my-post")
	return dir
}

func TestContextVoiceAndToneNorms(t *testing.T) {
	dir := techBlogWork(t)
	mustRunWS(t, dir, "brief", "set", "my-post", "--voice", "像跟同事聊天")
	mustRunWS(t, dir, "unit", "add", "my-post", "section", "intro")
	// context-voice：子单元没有 brief.yaml 时取父单元的 voice。
	code, ctx := runContextJSON(t, dir, "my-post/intro")
	if b, _ := ctx.Context["brief"].(map[string]any); code != 0 || b["unit"] != "my-post" || b["voice"] != "像跟同事聊天" || len(ctx.ChangedPaths) != 0 {
		t.Fatalf("context-voice: exit code = %d, brief = %+v", code, ctx.Context["brief"])
	}
	// context-tone-norms：norms.rules 含两条 tone 规则及其参数，norms.style 含口语文风。
	code, raw := runWS(t, dir, "context", "my-post")
	var out struct {
		Context struct {
			Brief map[string]any `json:"brief"`
			Norms struct {
				Rules []struct {
					ID     string         `json:"id"`
					Params map[string]any `json:"params"`
				} `json:"rules"`
				Style []string `json:"style"`
			} `json:"norms"`
		} `json:"context"`
	}
	if err := json.Unmarshal([]byte(raw.stdout), &out); err != nil || code != 0 {
		t.Fatalf("context: %v %d", err, code)
	}
	var ids []string
	for _, r := range out.Context.Norms.Rules {
		ids = append(ids, r.ID)
		if r.ID == "tone-patterns" && len(r.Params["patterns"].([]any)) != 17 || r.ID == "tone-density" && r.Params["max_connectives_per_1000"] != 12.0 {
			t.Errorf("rule %s params = %v", r.ID, r.Params)
		}
	}
	if !slices.Contains(ids, "tone-patterns") || !slices.Contains(ids, "tone-density") ||
		!slices.ContainsFunc(out.Context.Norms.Style, func(s string) bool { return strings.HasPrefix(s, "像当面讲给读者听") }) {
		t.Fatalf("context-tone-norms: rules = %v, style = %v", ids, out.Context.Norms.Style)
	}
	// context-voice-null：写作简报没有 voice 时为 null。
	mustRunWS(t, dir, "brief", "set", "my-post", "--voice", "")
	code, ctx = runContextJSON(t, dir, "my-post")
	b, _ := ctx.Context["brief"].(map[string]any)
	if v, ok := b["voice"]; code != 0 || b == nil || !ok || v != nil {
		t.Fatalf("context-voice-null: %+v", ctx.Context["brief"])
	}
}

// resolvedOf 返回 config resolved --json 的 resolved。
func resolvedOf(t *testing.T, dir string) map[string]any {
	t.Helper()
	code, res := runWS(t, dir, "config", "resolved")
	var out struct {
		Resolved map[string]any `json:"resolved"`
	}
	if err := json.Unmarshal([]byte(res.stdout), &out); err != nil || code != 0 {
		t.Fatalf("config resolved: exit code = %d, %v", code, res.Diagnostics)
	}
	return out.Resolved
}

func TestArticleToneRulesResolved(t *testing.T) {
	for _, genre := range []string{"tech-blog", "wechat-marketing"} {
		dir := t.TempDir()
		mustRunWS(t, dir, "init", "--genre", genre)
		r := resolvedOf(t, dir)
		rules := r["rules"].(map[string]any)
		// tech-blog-resolved 与 wechat-resolved
		for id, typ := range map[string]string{"tone-patterns": "formulaic-patterns", "tone-density": "tone-density"} {
			rule, _ := rules[id].(map[string]any)
			sev, _ := rule["severity"].(map[string]any)
			typv, _ := rule["type"].(map[string]any)
			if rule == nil || typv["value"] != typ || sev["value"] != "warning" || sev["source"] != "genre:"+genre {
				t.Fatalf("%s %s: %v", genre, id, rule)
			}
		}
		params := rules["tone-patterns"].(map[string]any)["params"].(map[string]any)
		patterns := params["patterns"].(map[string]any)["value"].([]any)
		var pids []string
		for _, p := range patterns {
			pids = append(pids, p.(map[string]any)["id"].(string))
		}
		if len(patterns) != 17 || !slices.Contains(pids, "not-but") || !slices.Contains(pids, "lets") {
			t.Fatalf("%s patterns = %v", genre, pids)
		}
		// density-params-resolved
		dp := rules["tone-density"].(map[string]any)["params"].(map[string]any)
		value := func(k string) any { return dp[k].(map[string]any)["value"] }
		if value("max_connectives_per_1000") != 12.0 || value("max_four_char_per_1000") != 25.0 || value("min_sentence_stdev") != 5.0 ||
			value("max_list_line_ratio") != 0.5 || value("min_section_length_cv") != 0.15 || len(value("connectives").([]any)) != 10 {
			t.Fatalf("%s density params = %v", genre, dp)
		}
		// ai-cliche 的词表不变。
		cliche := rules["ai-cliche"].(map[string]any)["params"].(map[string]any)["terms"].(map[string]any)["value"].([]any)
		if len(cliche) != 5 {
			t.Fatalf("%s ai-cliche = %v", genre, cliche)
		}
		// metric-resolved：natural-voice 在 scoredMetrics 中、不在 rubric 中。
		var nv map[string]any
		for _, m := range r["scoredMetrics"].([]any) {
			if m.(map[string]any)["id"] == "natural-voice" {
				nv = m.(map[string]any)
			}
		}
		scale, _ := nv["scale"].(map[string]any)
		if nv == nil || nv["source"] != "genre:"+genre || scale["min"] != 1.0 || scale["max"] != 5.0 || len(nv["anchors"].([]any)) != 5 || nv["sessions"] != 2.0 || nv["title"] != "口吻自然" {
			t.Fatalf("%s natural-voice = %v", genre, nv)
		}
		for _, item := range r["rubric"].([]any) {
			if strings.Contains(mustJSON(t, item), "natural-voice") {
				t.Fatalf("%s rubric has natural-voice: %v", genre, item)
			}
		}
	}
	// novel-unaffected：小说文体没有 tone 规则与 natural-voice。
	dir := t.TempDir()
	mustRunWS(t, dir, "init", "--genre", "novel")
	r := resolvedOf(t, dir)
	rules := r["rules"].(map[string]any)
	if rules["tone-patterns"] != nil || rules["tone-density"] != nil || rules["fiction-cliches"] == nil || strings.Contains(mustJSON(t, r["scoredMetrics"]), "natural-voice") {
		t.Fatalf("novel: rules = %v", rules)
	}
}

// known-rule-types 的 unknown-rule-type 与 tone-types-known。
func TestToneRuleTypesKnown(t *testing.T) {
	dir := newWorkspace(t)
	pack := "schema_version: 1\nid: house\ntitle: 团队规范\nrules:\n  p1:\n    type: formulaic-patterns\n    severity: warning\n    params:\n      patterns:\n        - {id: not-but, terms: [不是, 而是]}\n  d1:\n    type: tone-density\n    severity: warning\n    params: {max_four_char_per_1000: 20}\n"
	if err := os.MkdirAll(filepath.Join(dir, ".writing-anything", "packs"), 0o755); err != nil {
		t.Fatal(err)
	}
	writeFile(t, filepath.Join(dir, ".writing-anything", "packs", "house.yaml"), pack)
	cfg := filepath.Join(dir, ".writing-anything", "config.yaml")
	writeFile(t, cfg, strings.Replace(readFile(t, cfg), "packs: []", "packs: [house]", 1))
	if code, res := runWS(t, dir, "validate"); code != 0 || !res.Valid {
		t.Fatalf("validate: %+v", res.Diagnostics)
	}
	rules := resolvedOf(t, dir)["rules"].(map[string]any)
	if rules["p1"] == nil || rules["d1"] == nil {
		t.Fatalf("resolved rules = %v", rules)
	}
	writeFile(t, filepath.Join(dir, ".writing-anything", "packs", "house.yaml"), pack+"  r9:\n    type: run-shell\n")
	code, res := runWS(t, dir, "validate")
	d := diag(t, res, "rule.unknown-type")
	if code != 1 || d.Field != "rules.r9.type" || !strings.Contains(d.Message, "formulaic-patterns") || !strings.Contains(d.Message, "tone-density") || !strings.Contains(d.Message, "pov-consistency") {
		t.Fatalf("unknown-rule-type: %+v", res.Diagnostics)
	}
}

// warning-not-blocking、project-override、plan-includes-metric 与 accept-not-blocked：tech-blog 作品从草稿走到验收。
func TestToneGuardLifecycle(t *testing.T) {
	dir := t.TempDir()
	now = func() time.Time { return time.Date(2026, 11, 13, 9, 30, 0, 0, time.UTC) }
	defer func() { now = time.Now }()
	step := func(input string, args ...string) result {
		t.Helper()
		code, res := runWSIn(t, dir, input, args...)
		if code != 0 || !res.Valid {
			t.Fatalf("%v: exit code = %d, diagnostics = %+v", args, code, res.Diagnostics)
		}
		return res
	}
	step("", "init", "--genre", "tech-blog")
	step("", "new", "tech-blog", "pool")
	for _, s := range [][]string{{"note", "author-note", "author-note.md"}, {"talk", "interview", "interview.md"}, {"go-docs", "reference", "go-docs.md"}} {
		step(fixture(t, s[2]), "source", "add", "--type", s[1], "--id", s[0], "--title", s[2])
	}
	step("", "brief", "set", "pool", "--audience", "后端工程师", "--audience-sources", "note", "--purpose", "讲清楚怎样用连接复用降低接口延迟", "--purpose-sources", "note",
		"--voice", "像跟同事聊天，第一人称", "--sources", "note,talk,go-docs")
	step("", "brief", "set", "pool", "--message", "reuse", "--message-text", "复用连接是降低延迟最直接的办法", "--message-sources", "note,go-docs")
	step("", "brief", "set", "pool", "--message", "body", "--message-text", "读完并关闭响应体，连接才能回到连接池", "--message-sources", "talk,go-docs")
	step("", "advance", "pool")
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
	draft := fixture(t, "draft.md") + "\n这不是工具的问题，而是流程的问题。\n"
	step(draft, "draft", "write", "pool")
	step("", "claim", "add", "pool", "--kind", "experience", "--text", "我曾经负责这个服务的性能优化，压测时发现大量时间花在建立连接上", "--sources", "note")
	step("", "claim", "add", "pool", "--kind", "data", "--text", "接口的 P99 延迟降低了 40%", "--sources", "note")
	step("", "claim", "add", "pool", "--kind", "fact", "--text", "Go 1.22 的 http.Client 会缓存连接，前提是读完并关闭响应体", "--sources", "go-docs")
	lintOf := func() (int, lintOut) {
		code, res := runWS(t, dir, "lint", "pool")
		var out lintOut
		if err := json.Unmarshal([]byte(res.stdout), &out); err != nil {
			t.Fatal(err)
		}
		out.result = res
		return code, out
	}
	// warning-not-blocking：只命中 tone 规则时 valid 为 true，advance 成功。
	code, lr := lintOf()
	tp := findingsOf(lr.Findings, "tone-patterns")
	if code != 0 || !lr.Valid || len(tp) != 1 || tp[0]["severity"] != "warning" || tp[0]["text"] != "不是…而是" || tp[0]["type"] != "formulaic-patterns" {
		t.Fatalf("warning-not-blocking: exit code = %d, findings = %v", code, lr.Findings)
	}
	// project-override：config.yaml 把 tone-patterns 调成 error 后 valid 为 false，来源仍为文体。
	cfg := filepath.Join(dir, ".writing-anything", "config.yaml")
	orig := readFile(t, cfg)
	writeFile(t, cfg, orig+"rules:\n  tone-patterns:\n    severity: error\n")
	code, lr = lintOf()
	if tp := findingsOf(lr.Findings, "tone-patterns"); code != 1 || lr.Valid || len(tp) != 1 || tp[0]["severity"] != "error" || tp[0]["source"] != "genre:tech-blog" {
		t.Fatalf("project-override: exit code = %d, findings = %v", code, lr.Findings)
	}
	writeFile(t, cfg, orig)
	step("", "advance", "pool")
	// plan-includes-metric：打分任务含 natural-voice，sessions 为 2。
	res := step("", "metrics", "plan", "pool")
	var plan struct {
		Plan struct {
			Metrics []map[string]any `json:"metrics"`
		} `json:"plan"`
	}
	if err := json.Unmarshal([]byte(res.stdout), &plan); err != nil {
		t.Fatal(err)
	}
	if !slices.ContainsFunc(plan.Plan.Metrics, func(m map[string]any) bool { return m["id"] == "natural-voice" && m["sessions"] == 2.0 }) {
		t.Fatalf("plan-includes-metric: %v", plan.Plan.Metrics)
	}
	// accept-not-blocked：没有任何 natural-voice 打分时，验收没有与指标有关的阻塞项。
	step(fixture(t, "review.yaml"), "review", "record", "pool")
	step("", "review", "resolve", "pool", "rf-1", "--status", "waived", "--note", "作者：压测条件放在附录")
	res = step("", "accept", "pool")
	if strings.Contains(res.stdout, "natural-voice") || strings.Contains(res.stdout, "metrics.") {
		t.Fatalf("accept-not-blocked: %s", res.stdout)
	}
}

// genre-style-scope：default 规范包的四条文章类文风（no-genre-keeps-style、tech-blog-keeps-style、novel-excludes-article-style）。
func TestDefaultPackArticleStyle(t *testing.T) {
	want := []string{"结论先行，先说结果再说过程。", "用具体的事实、数据和例子代替空泛的形容词。",
		"像当面讲给读者听，多用短句和口语，少用连接词、四字词和排比。", "直接讲事，不在段末总结或升华，不用「不是…而是」「不仅…更是」这类套路句式。"}
	texts := func(v any) []string {
		var out []string
		for _, s := range v.([]any) {
			out = append(out, s.(map[string]any)["text"].(string))
		}
		return out
	}
	for genre, keep := range map[string]bool{"": true, "tech-blog": true, "novel": false} {
		dir := t.TempDir()
		args := []string{"init"}
		if genre != "" {
			args = append(args, "--genre", genre)
		}
		mustRunWS(t, dir, args...)
		r := resolvedOf(t, dir)
		style, excluded := texts(r["style"]), texts(r["excludedStyle"])
		if keep && (!slices.Equal(style, want) || len(excluded) != 0) || !keep && (len(style) != 0 || !slices.Equal(excluded, want)) {
			t.Errorf("%q: style = %v, excluded = %v", genre, style, excluded)
		}
	}
}

// skills 的 ai-tone-skill-guidance：四个技能写明去 AI 腔的步骤与停下来问作者的条目。
func TestToneSkillGuidance(t *testing.T) {
	read := func(name string) string {
		data, err := os.ReadFile(filepath.Join("..", "skills", "skills", name, "SKILL.md"))
		if err != nil {
			t.Fatal(err)
		}
		return string(data)
	}
	stop := func(s string) string { _, after, _ := strings.Cut(s, "## 必须停下来问作者"); return after }
	draft := read("wa-draft")
	for _, s := range []string{"去 AI 腔自查", "tone-patterns", "tone-density", "brief.voice", "也不要自己写 lint-ignore", "wa-cli context", "wa-cli draft write", "wa-cli lint"} {
		if !strings.Contains(draft, s) {
			t.Errorf("wa-draft 缺少 %q", s)
		}
	}
	if !strings.Contains(stop(draft), "tone-patterns") {
		t.Error("wa-draft 的「必须停下来问作者」缺少 tone 发现项")
	}
	for _, name := range []string{"wa-brief", "wa-draft", "wa-review", "wa-revise"} {
		// 四个技能的「必须停下来问作者」：改还是保留由作者决定；tone-density 是全文级发现项，不能单处豁免。
		if st := stop(read(name)); !strings.Contains(st, "tone-patterns") || !strings.Contains(st, "单处豁免") || !strings.Contains(st, "不能单处豁免") || !strings.Contains(st, "waivers") {
			t.Errorf("%s 的「必须停下来问作者」缺少保留 tone 发现项的说明", name)
		}
	}
	if b := read("wa-brief"); !strings.Contains(b, "wa-cli brief set <单元> --voice") || !strings.Contains(stop(b), "口吻") {
		t.Error("wa-brief 缺少 --voice 说明")
	}
	review := read("wa-review")
	if !strings.Contains(review, "natural-voice") || !strings.Contains(review, "wa-cli metrics show") || !strings.Contains(stop(review), "natural-voice") {
		t.Error("wa-review 缺少 natural-voice 交给作者的说明")
	}
	revise := read("wa-revise")
	if !strings.Contains(revise, "natural-voice") || !strings.Contains(revise, "wa-cli lint <单元> --json` 与 `wa-cli metrics compute") || !strings.Contains(stop(revise), "tone-patterns") {
		t.Error("wa-revise 缺少按低分段落修订的说明")
	}
}

// 命令层的 lint 场景 invalid-patterns 与 density-connectives：参数错误时报告 rule.invalid-params（path 为 config.yaml）、
// 该规则不执行、其他规则照常执行；只有 warning 级的 tone-density 发现项时 valid 为 true。
func TestToneLintCommand(t *testing.T) {
	dir := techBlogWork(t)
	cfg := filepath.Join(dir, ".writing-anything", "config.yaml")
	orig := readFile(t, cfg)
	writeFile(t, cfg, orig+"rules:\n  tone-patterns:\n    params:\n      patterns:\n        - {id: x, terms: [甲, 乙, 丙, 丁]}\n")
	code, res := runLintJSON(t, dir, "这不是工具的问题，而是流程的问题。")
	d := diag(t, res.result, "rule.invalid-params")
	if code != 1 || res.Valid || d.Field != "rules.tone-patterns.params.patterns[0].terms" || d.Path != ".writing-anything/config.yaml" ||
		len(findingsOf(res.Findings, "tone-patterns")) != 0 || len(findingsOf(res.Findings, "length")) != 1 {
		t.Fatalf("invalid-patterns: exit code = %d, result = %+v", code, res)
	}
	writeFile(t, cfg, orig)
	body := strings.Repeat("此外，我们改了配置。因此，接口快了。同时，数据库也轻松了。", 12)
	code, res = runLintJSON(t, dir, body)
	td := findingsOf(res.Findings, "tone-density")
	if code != 0 || !res.Valid || len(td) == 0 || td[0]["line"] != nil || td[0]["severity"] != "warning" || !strings.Contains(td[0]["message"].(string), "连接词密度") {
		t.Fatalf("density-connectives: exit code = %d, findings = %v", code, res.Findings)
	}
}
