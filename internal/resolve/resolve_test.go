package resolve

import (
	"os"
	"path/filepath"
	"slices"
	"strings"
	"testing"

	"github.com/kw12121212/writing-anything/internal/workspace"
)

// setup 返回一个已初始化的临时工作区：config.yaml 为 cfg（为空时保留 init 写出的配置），packs/ 下写入 packs。
func setup(t *testing.T, cfg string, packs map[string]string) string {
	t.Helper()
	root := t.TempDir()
	if _, ps := workspace.Init(root, nil); len(ps) > 0 {
		t.Fatal(ps)
	}
	write := func(rel, content string) {
		p := filepath.Join(root, filepath.FromSlash(rel))
		if err := os.MkdirAll(filepath.Dir(p), 0o755); err != nil {
			t.Fatal(err)
		}
		if err := os.WriteFile(p, []byte(content), 0o644); err != nil {
			t.Fatal(err)
		}
	}
	if cfg != "" {
		write(workspace.ConfigPath, cfg)
	}
	for id, content := range packs {
		write(workspace.PacksPath+"/"+id+".yaml", content)
	}
	return root
}

func config(genre, packs, extra string) string {
	s := "schema_version: 1\nlanguage: zh-CN\npacks: " + packs + "\ngate_level: standard\n"
	if genre != "" {
		s += "default_genre: " + genre + "\n"
	}
	return s + extra
}

const house = `schema_version: 1
id: house
title: 团队规范
style: [短句优先]
glossary:
  - {term: 规范包, english: Pack}
banned_terms:
  - {term: 赋能}
rules:
  length:
    severity: error
    params: {max: 20}
rubric:
  - {id: clarity, title: 清楚}
`

func mustResolve(t *testing.T, root string) *Resolved {
	t.Helper()
	r, ps := Resolve(root)
	if r == nil || len(ps) > 0 {
		t.Fatalf("Resolve: resolved = %+v, problems = %+v", r, ps)
	}
	return r
}

func codes(ps []workspace.Problem) []string {
	var out []string
	for _, p := range ps {
		out = append(out, p.Code)
	}
	return out
}

func TestDefaultPackWhenPacksEmpty(t *testing.T) {
	r := mustResolve(t, setup(t, "", nil))
	if r.Genre != nil || !slices.Equal(r.Packs, []Ref{{ID: "default", Origin: "builtin"}}) || len(r.UnitTypes) != 0 || len(r.Stages) != 0 {
		t.Fatalf("resolved = %+v", r)
	}
	if len(r.Rules) == 0 {
		t.Fatal("default pack must contribute rules")
	}
	for id, rule := range r.Rules {
		if rule.Type.Source != "pack:default" || rule.Severity.Source != "pack:default" || !rule.Enabled || rule.Waiver != nil {
			t.Errorf("rule %s = %+v, want enabled and from pack:default", id, rule)
		}
	}
}

func TestExplicitPacksReplaceDefault(t *testing.T) {
	r := mustResolve(t, setup(t, config("minimal", "[house]", ""), map[string]string{"house": house}))
	if !slices.Equal(r.Packs, []Ref{{ID: "house", Origin: "workspace", Path: ".writing-anything/packs/house.yaml"}}) {
		t.Fatalf("packs = %+v", r.Packs)
	}
	if _, ok := r.Rules["cjk-typography"]; ok {
		t.Fatal("default pack rules must not apply when packs is not empty")
	}
}

func TestLayersAndSources(t *testing.T) {
	r := mustResolve(t, setup(t, config("minimal", "[house]", ""), map[string]string{"house": house}))
	rule := r.Rules["length"]
	if rule.Type != (Value{"length-range", "genre:minimal"}) || rule.Severity != (Value{"error", "pack:house"}) ||
		rule.Params["max"] != (Value{20.0, "pack:house"}) || rule.Params["min"] != (Value{300.0, "genre:minimal"}) {
		t.Fatalf("length = %+v", rule)
	}
	if r.Genre == nil || r.Genre.ID != "minimal" || r.UnitTypes[0].Source != "genre:minimal" || r.Stages[0].Source != "genre:minimal" {
		t.Fatalf("genre = %+v, unitTypes = %+v, stages = %+v", r.Genre, r.UnitTypes, r.Stages)
	}
	// 评分项按 id 合并：house 替换 minimal 的 clarity，位置不变。
	if len(r.Rubric) != 1 || r.Rubric[0].Title != "清楚" || r.Rubric[0].Source != "pack:house" {
		t.Fatalf("rubric = %+v", r.Rubric)
	}
	if r.Style[0] != (Style{"短句优先", "pack:house"}) || r.Glossary[0].Source != "pack:house" || r.BannedTerms[0].Term != "赋能" {
		t.Fatalf("style = %+v, glossary = %+v, banned = %+v", r.Style, r.Glossary, r.BannedTerms)
	}

	r = mustResolve(t, setup(t, config("minimal", "[house]", "rules:\n  length:\n    params: {max: 30}\n"), map[string]string{"house": house}))
	if rule := r.Rules["length"]; rule.Params["max"] != (Value{30.0, "project"}) || rule.Severity.Source != "pack:house" {
		t.Fatalf("project override: length = %+v", rule)
	}
}

func TestLaterPackWins(t *testing.T) {
	a := "schema_version: 1\nid: a\ntitle: A\nglossary:\n  - {term: 规范包, english: Pack}\n  - {term: 文体}\nbanned_terms:\n  - {term: 赋能, reason: 空泛}\n"
	b := "schema_version: 1\nid: b\ntitle: B\nglossary:\n  - {term: 规范包, english: Spec Pack}\nbanned_terms:\n  - {term: 赋能, replacement: 帮助}\n"
	r := mustResolve(t, setup(t, config("", "[a, b]", ""), map[string]string{"a": a, "b": b}))
	if len(r.Glossary) != 2 || r.Glossary[0].English != "Spec Pack" || r.Glossary[0].Source != "pack:b" || r.Glossary[1].Source != "pack:a" {
		t.Fatalf("glossary = %+v", r.Glossary)
	}
	if len(r.BannedTerms) != 1 || r.BannedTerms[0].Replacement != "帮助" || r.BannedTerms[0].Reason != "" || r.BannedTerms[0].Source != "pack:b" {
		t.Fatalf("bannedTerms = %+v", r.BannedTerms)
	}
}

func TestWaivers(t *testing.T) {
	waived := house + "waivers:\n  - {rule: length, reason: 本团队不限制长度}\n"
	r := mustResolve(t, setup(t, config("minimal", "[house]", ""), map[string]string{"house": waived}))
	if rule := r.Rules["length"]; rule.Enabled || rule.Waiver == nil || *rule.Waiver != (Waiver{"本团队不限制长度", "pack:house"}) {
		t.Fatalf("length = %+v", rule)
	}
	r = mustResolve(t, setup(t, config("minimal", "[]", "waivers:\n  - {rule: ai-cliche, reason: 引用原话}\n"), nil))
	if rule := r.Rules["ai-cliche"]; rule.Enabled || rule.Waiver.Source != "project" {
		t.Fatalf("project waiver: ai-cliche = %+v", rule)
	}
}

// 没有任何配置层给出 severity 时为 warning，来源为定义该规则的配置层。
func TestDefaultSeverity(t *testing.T) {
	pack := "schema_version: 1\nid: house\ntitle: t\nrules:\n  r1: {type: paragraph-length}\n"
	r := mustResolve(t, setup(t, config("", "[house]", ""), map[string]string{"house": pack}))
	if rule := r.Rules["r1"]; rule.Severity != (Value{"warning", "pack:house"}) || len(rule.Params) != 0 || rule.Params == nil {
		t.Fatalf("r1 = %+v", rule)
	}
}

// 项目配置覆盖被规范包豁免的规则时，规则仍保持关闭，豁免来源不变。
func TestProjectCannotReenableWaivedRule(t *testing.T) {
	waived := house + "waivers:\n  - {rule: length, reason: 不限长度}\n"
	r := mustResolve(t, setup(t, config("minimal", "[house]", "rules:\n  length: {severity: warning}\n"), map[string]string{"house": waived}))
	if rule := r.Rules["length"]; rule.Enabled || rule.Waiver.Source != "pack:house" || rule.Severity.Source != "project" {
		t.Fatalf("length = %+v", rule)
	}
}

func TestCheckArtifact(t *testing.T) {
	root := setup(t, config("", "[]", "rules:\n  r1: {type: run-shell}\n"), map[string]string{"house": "schema_version: 1\nid: office\ntitle: t\n"})
	if ps := CheckArtifact(root, workspace.ConfigPath); !slices.Equal(codes(ps), []string{"rule.unknown-type"}) || ps[0].Path != workspace.ConfigPath {
		t.Errorf("config: problems = %+v", ps)
	}
	if ps := CheckArtifact(root, ".writing-anything/packs/house.yaml"); !slices.Equal(codes(ps), []string{"pack.id-mismatch"}) {
		t.Errorf("pack: problems = %+v", ps)
	}
	os.Remove(filepath.Join(root, filepath.FromSlash(workspace.ConfigPath)))
	if ps := CheckArtifact(root, workspace.ConfigPath); !slices.Equal(codes(ps), []string{"artifact.not-found"}) {
		t.Errorf("missing config: problems = %+v", ps)
	}
}

func TestMergeProblems(t *testing.T) {
	tests := []struct {
		name  string
		cfg   string
		pack  string
		code  string
		path  string
		field string
	}{
		{"missing-type", config("minimal", "[house]", ""), "schema_version: 1\nid: house\ntitle: t\nrules:\n  r9: {severity: error}\n",
			"rule.missing-type", ".writing-anything/packs/house.yaml", "rules.r9"},
		{"type-conflict", config("minimal", "[house]", ""), "schema_version: 1\nid: house\ntitle: t\nrules:\n  length: {type: paragraph-length}\n",
			"rule.type-conflict", ".writing-anything/packs/house.yaml", "rules.length.type"},
		{"unknown-waiver-target", config("", "[house]", ""), "schema_version: 1\nid: house\ntitle: t\nwaivers:\n  - {rule: nope, reason: x}\n",
			"rule.unknown-waiver-target", ".writing-anything/packs/house.yaml", "waivers[0].rule"},
		{"unknown-genre", config("no-such-genre", "[]", ""), "", "config.unknown-genre", ".writing-anything/config.yaml", "default_genre"},
		{"unknown-pack", config("", "[default, no-such-pack]", ""), "", "config.unknown-pack", ".writing-anything/config.yaml", "packs[1]"},
		{"project-unknown-type", config("", "[]", "rules:\n  r1: {type: run-shell}\n"), "", "rule.unknown-type", ".writing-anything/config.yaml", "rules.r1.type"},
		{"project-missing-type", config("", "[]", "rules:\n  r1: {severity: error}\n"), "", "rule.missing-type", ".writing-anything/config.yaml", "rules.r1"},
		{"pack-executable", config("", "[house]", ""), "schema_version: 1\nid: house\ntitle: t\nrules:\n  r:\n    type: length-range\n    params: {script: x}\n",
			"artifact.executable-content", ".writing-anything/packs/house.yaml", "rules.r.params.script"},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			packs := map[string]string{}
			if tt.pack != "" {
				packs["house"] = tt.pack
			}
			r, ps := Resolve(setup(t, tt.cfg, packs))
			if r != nil || len(ps) != 1 || ps[0].Code != tt.code || ps[0].Path != tt.path || ps[0].Field != tt.field || ps[0].Severity != "error" {
				t.Fatalf("resolved = %+v, problems = %+v, want one %s at %s %s", r, ps, tt.code, tt.path, tt.field)
			}
		})
	}
}

// 内置规范包出问题时不填 path，由说明指明出处。
func TestBuiltinLayerProblems(t *testing.T) {
	a := "schema_version: 1\nid: a\ntitle: A\nrules:\n  ai-cliche: {type: banned-terms}\n"
	_, ps := Resolve(setup(t, config("", "[a, default]", ""), map[string]string{"a": a}))
	if len(ps) != 1 || ps[0].Code != "rule.type-conflict" || ps[0].Path != "" || ps[0].Origin != "内置规范包 default" {
		t.Fatalf("problems = %+v", ps)
	}
}

func TestResolveInvalidConfig(t *testing.T) {
	if r, ps := Resolve(setup(t, "gate_level: bogus\n", nil)); r != nil || len(ps) == 0 || ps[0].Code != "artifact.schema-violation" {
		t.Fatalf("resolved = %+v, problems = %+v", r, ps)
	}
	root := setup(t, "", nil)
	os.Remove(filepath.Join(root, filepath.FromSlash(workspace.ConfigPath)))
	if r, ps := Resolve(root); r != nil || !slices.Equal(codes(ps), []string{"artifact.not-found"}) {
		t.Fatalf("missing config: resolved = %+v, problems = %+v", r, ps)
	}
}

func TestCheck(t *testing.T) {
	broken := "schema_version: 1\nid: house\ntitle: t\nrules:\n  r9: {type: run-shell}\n"
	root := setup(t, config("", "[house]", ""), map[string]string{"house": broken, "spare": "schema_version: 1\nid: spare\ntitle: s\n"})
	checked, ps := Check(root)
	if !slices.Equal(checked, []string{".writing-anything/packs/house.yaml", ".writing-anything/packs/spare.yaml"}) {
		t.Fatalf("checked = %v", checked)
	}
	// 被引用的规范包的问题只报告一次。
	if !slices.Equal(codes(ps), []string{"rule.unknown-type"}) {
		t.Fatalf("problems = %+v", ps)
	}
	// config.yaml 自身不合法时由 workspace.Check 报告，这里只校验规范包文件。
	root = setup(t, "gate_level: bogus\n", map[string]string{"spare": "schema_version: 1\nid: spare\ntitle: s\n"})
	if checked, ps := Check(root); len(checked) != 1 || ps != nil {
		t.Fatalf("invalid config: checked = %v, problems = %+v", checked, ps)
	}
	if _, ps := Check(setup(t, "", nil)); ps != nil {
		t.Fatalf("fresh workspace: problems = %+v", ps)
	}
}

// 上下文预算按 文体 → 规范包 → 项目配置 合并，都没有声明时为默认值；ForGenre 以给定文体作为文体层。
func TestContextBudget(t *testing.T) {
	if r := mustResolve(t, setup(t, config("", "[]", ""), nil)); r.ContextBudget != (Value{DefaultContextBudget, SourceDefault}) {
		t.Fatalf("default: %+v", r.ContextBudget)
	}
	if r := mustResolve(t, setup(t, config("minimal", "[]", ""), nil)); r.ContextBudget != (Value{12000, "genre:minimal"}) {
		t.Fatalf("genre: %+v", r.ContextBudget)
	}
	packed := map[string]string{"house": house + "context_budget: 8000\n"}
	if r := mustResolve(t, setup(t, config("minimal", "[house]", ""), packed)); r.ContextBudget != (Value{8000, "pack:house"}) {
		t.Fatalf("pack: %+v", r.ContextBudget)
	}
	root := setup(t, config("", "[]", "context_budget: 500\n"), nil)
	if r := mustResolve(t, root); r.ContextBudget != (Value{500, "project"}) {
		t.Fatalf("project: %+v", r.ContextBudget)
	}
	r, ps := ForGenre(root, "minimal")
	if r == nil || ps != nil || r.Genre.ID != "minimal" || r.ContextBudget != (Value{500, "project"}) {
		t.Fatalf("ForGenre: %+v %+v", r, ps)
	}
	if r, ps := ForGenre(setup(t, "k: [\n", nil), "minimal"); r != nil || len(ps) == 0 {
		t.Fatalf("broken config: %+v %+v", r, ps)
	}
	if r, ps := ForGenre(setup(t, "schema_version: 2\n", nil), "minimal"); r != nil || len(ps) == 0 {
		t.Fatalf("invalid config: %+v %+v", r, ps)
	}
}

func metricPack(id, title, extra string) string {
	return "schema_version: 1\nid: " + id + "\ntitle: " + id + "\nscored_metrics:\n  - id: tension\n    title: " + title +
		"\n    scale: {min: 1, max: 3}\n    anchors:\n      - {score: 1, description: 平静, example: 午后。}\n      - {score: 2, description: 隐忧, example: 门外有人。}\n      - {score: 3, description: 危机, example: 门把手转动。}\n" + extra
}

func TestScoredMetrics(t *testing.T) {
	r := mustResolve(t, setup(t, config("", "[a, b]", ""), map[string]string{
		"a": metricPack("a", "紧张度A", "    sessions: 2\n"),
		"b": metricPack("b", "紧张度B", ""),
	}))
	if len(r.ScoredMetrics) != 1 || r.ScoredMetrics[0].Title != "紧张度B" || r.ScoredMetrics[0].Source != "pack:b" || r.ScoredMetrics[0].Sessions != 3 ||
		r.ScoredMetrics[0].MaxSpread != 1 || r.ScoredMetrics[0].Tolerance != 1 || r.ScoredMetrics[0].MinSamples != 5 || r.ScoredMetrics[0].MinAgreement != 0.7 {
		t.Fatalf("metrics = %+v", r.ScoredMetrics)
	}
	if r := mustResolve(t, setup(t, config("", "[a]", ""), map[string]string{"a": metricPack("a", "x", "    sessions: 2\n    min_agreement: 0.5\n")})); r.ScoredMetrics[0].Sessions != 2 || r.ScoredMetrics[0].MinAgreement != 0.5 {
		t.Fatalf("declared thresholds: %+v", r.ScoredMetrics)
	}
	if r := mustResolve(t, setup(t, "", nil)); r.ScoredMetrics == nil || len(r.ScoredMetrics) != 0 {
		t.Fatalf("none declared: %#v", r.ScoredMetrics)
	}
}

func TestScoredMetricProblems(t *testing.T) {
	tests := map[string]struct{ pack, code, field string }{
		"missing-anchor": {strings.Replace(metricPack("a", "x", ""), "      - {score: 2, description: 隐忧, example: 门外有人。}\n", "", 1), "metrics.invalid-anchors", "scored_metrics[0].anchors"},
		"extra-anchor":   {strings.Replace(metricPack("a", "x", ""), "score: 3,", "score: 4,", 1), "metrics.invalid-anchors", "scored_metrics[0].anchors"},
		"repeated":       {strings.Replace(metricPack("a", "x", ""), "score: 3,", "score: 2,", 1), "metrics.invalid-anchors", "scored_metrics[0].anchors"},
		"bad-scale":      {strings.Replace(metricPack("a", "x", ""), "{min: 1, max: 3}", "{min: 3, max: 3}", 1), "metrics.invalid-scale", "scored_metrics[0].scale"},
		"wide-scale":     {strings.Replace(metricPack("a", "x", ""), "{min: 1, max: 3}", "{min: 0, max: 26}", 1), "metrics.invalid-scale", "scored_metrics[0].scale"},
		"duplicate":      {metricPack("a", "x", "") + strings.SplitN(metricPack("a", "x", ""), "scored_metrics:\n", 2)[1], "artifact.duplicate-id", "scored_metrics[1].id"},
	}
	for name, tt := range tests {
		_, ps := Resolve(setup(t, config("", "[a]", ""), map[string]string{"a": tt.pack}))
		if !slices.ContainsFunc(ps, func(p workspace.Problem) bool {
			return p.Code == tt.code && p.Field == tt.field && p.Path == ".writing-anything/packs/a.yaml"
		}) {
			t.Errorf("%s: %+v, want %s at %s", name, ps, tt.code, tt.field)
		}
	}
	if _, ps := Resolve(setup(t, config("", "[a]", ""), map[string]string{"a": strings.Replace(metricPack("a", "x", ""), "score: 2,", "score: 4,", 1)})); !strings.Contains(ps[0].Message, "缺少 2 分") || !strings.Contains(ps[0].Message, "4 分不在 scale 内") {
		t.Fatalf("message: %+v", ps)
	}
}

func TestStyleProfile(t *testing.T) {
	profile := "style_profile:\n  sources: [a1]\n  chars: 15\n  sentences: {count: 3, mean: 5, median: 5, stdev: 1.63, p10: 3, p90: 7}\n  paragraphs: {count: 1, mean: 15, median: 15, stdev: 0, p10: 15, p90: 15}\n  punctuation: {}\n  terms: []\n"
	r := mustResolve(t, setup(t, config("", "[a, b, c]", ""), map[string]string{
		"a": "schema_version: 1\nid: a\ntitle: a\n" + profile,
		"b": "schema_version: 1\nid: b\ntitle: b\n" + strings.Replace(profile, "chars: 15", "chars: 30", 1),
		"c": "schema_version: 1\nid: c\ntitle: c\n",
	}))
	if r.StyleProfile == nil || r.StyleProfile.Source != "pack:b" || r.StyleProfile.Value.(map[string]any)["chars"] != float64(30) {
		t.Fatalf("profile = %+v", r.StyleProfile)
	}
	if r := mustResolve(t, setup(t, "", nil)); r.StyleProfile != nil {
		t.Fatalf("default = %+v", r.StyleProfile)
	}
}

func TestForWorkLayer(t *testing.T) {
	pack := func(id, max string) string {
		return "schema_version: 1\nid: " + id + "\ntitle: " + id + "\nrules:\n  r1: {type: length-range, params: {max: " + max + "}}\nglossary: [{term: 词, english: " + id + "}]\n"
	}
	root := setup(t, config("", "[a]", "rules:\n  r1: {params: {min: 1}}\n"), map[string]string{"a": pack("a", "10"), "b": pack("b", "20")})
	layer := &Layer{Path: ".writing-anything/series/s.yaml", Packs: []string{"b", "a"}}
	r, ps := ForWork(root, "", layer)
	if ps != nil {
		t.Fatal(ps)
	}
	// 系列的 b 排在 config.yaml 的 a 之后，覆盖 a；config.yaml 已列出的 a 不重复应用；项目配置最后生效。
	if got := r.Rules["r1"].Params["max"]; got.Source != "pack:b" || got.Value != float64(20) {
		t.Fatalf("max = %+v", got)
	}
	if got := r.Rules["r1"].Params["min"]; got.Source != "project" {
		t.Fatalf("min = %+v", got)
	}
	var ids []string
	for _, p := range r.Packs {
		ids = append(ids, p.ID)
	}
	if !slices.Equal(ids, []string{"a", "b"}) || r.Glossary[0].Source != "pack:b" {
		t.Fatalf("packs = %v, glossary = %+v", ids, r.Glossary)
	}
	if r, _ := ForWork(root, "", nil); r.Rules["r1"].Params["max"].Source != "pack:a" {
		t.Fatal("nil layer differs from ForGenre")
	}
	_, ps = ForWork(root, "", &Layer{Path: ".writing-anything/series/s.yaml", Packs: []string{"nope"}})
	if len(ps) != 1 || ps[0].Code != "config.unknown-pack" || ps[0].Path != ".writing-anything/series/s.yaml" || ps[0].Field != "packs[0]" {
		t.Fatalf("unknown series pack: %+v", ps)
	}
}

func TestDriftThreshold(t *testing.T) {
	if r := mustResolve(t, setup(t, "", nil)); r.DriftThreshold != (Value{2.0, "default"}) {
		t.Fatalf("default = %+v", r.DriftThreshold)
	}
	r := mustResolve(t, setup(t, config("", "[a, b]", ""), map[string]string{
		"a": "schema_version: 1\nid: a\ntitle: a\ndrift_threshold: 3\n",
		"b": "schema_version: 1\nid: b\ntitle: b\ndrift_threshold: 1.5\n",
	}))
	if r.DriftThreshold != (Value{1.5, "pack:b"}) {
		t.Fatalf("declared = %+v", r.DriftThreshold)
	}
}

func TestStyleByGenreCategory(t *testing.T) {
	// novel 属于小说类：default 规范包的两条文章类文风被排除，并说明原因。
	r := mustResolve(t, setup(t, config("novel", "[]", ""), nil))
	if r.Genre.Category != "fiction" || len(r.Style) != 0 || len(r.ExcludedStyle) != 2 {
		t.Fatalf("novel: genre = %+v, style = %+v, excluded = %+v", r.Genre, r.Style, r.ExcludedStyle)
	}
	for _, e := range r.ExcludedStyle {
		if e.Source != "pack:default" || !slices.Equal(e.AppliesTo, []string{"article"}) ||
			!strings.Contains(e.Reason, "novel") || !strings.Contains(e.Reason, "fiction") || !strings.Contains(e.Reason, "article") {
			t.Fatalf("excluded = %+v", e)
		}
	}
	if r.ExcludedStyle[0].Text != "结论先行，先说结果再说过程。" {
		t.Fatalf("excluded[0] = %+v", r.ExcludedStyle[0])
	}
	if r := mustResolve(t, setup(t, config("short-story", "[]", ""), nil)); len(r.Style) != 0 || len(r.ExcludedStyle) != 2 {
		t.Fatalf("short-story: style = %+v", r.Style)
	}

	// 文章类文体保留，没有文体或文体未声明类别时全部生效。
	for _, genre := range []string{"tech-blog", "wechat-marketing", "minimal", ""} {
		r := mustResolve(t, setup(t, config(genre, "[]", ""), nil))
		if len(r.Style) != 2 || r.Style[0].Text != "结论先行，先说结果再说过程。" || r.Style[0].Source != "pack:default" ||
			r.ExcludedStyle == nil || len(r.ExcludedStyle) != 0 {
			t.Fatalf("%q: style = %+v, excluded = %+v", genre, r.Style, r.ExcludedStyle)
		}
	}
	if r := mustResolve(t, setup(t, config("tech-blog", "[]", ""), nil)); r.Genre.Category != "article" {
		t.Fatalf("tech-blog genre = %+v", r.Genre)
	}
	if r := mustResolve(t, setup(t, config("minimal", "[]", ""), nil)); r.Genre.Category != "" {
		t.Fatalf("minimal genre = %+v", r.Genre)
	}

	// 字符串条目对所有文体生效；对象条目按 applies_to 过滤，顺序与来源不变。
	scoped := `schema_version: 1
id: scoped
title: 分类文风
style:
  - 短句为主
  - {text: 先给结论, applies_to: [article]}
  - {text: 多用感官细节, applies_to: [fiction]}
  - {text: 两类都适用, applies_to: [article, fiction]}
`
	r = mustResolve(t, setup(t, config("novel", "[scoped]", ""), map[string]string{"scoped": scoped}))
	var texts []string
	for _, s := range r.Style {
		if s.Source != "pack:scoped" {
			t.Fatalf("style = %+v", s)
		}
		texts = append(texts, s.Text)
	}
	if !slices.Equal(texts, []string{"短句为主", "多用感官细节", "两类都适用"}) || len(r.ExcludedStyle) != 1 || r.ExcludedStyle[0].Text != "先给结论" {
		t.Fatalf("novel scoped: %v %+v", texts, r.ExcludedStyle)
	}
	r = mustResolve(t, setup(t, config("tech-blog", "[scoped]", ""), map[string]string{"scoped": scoped}))
	if len(r.Style) != 3 || r.Style[1].Text != "先给结论" || len(r.ExcludedStyle) != 1 || r.ExcludedStyle[0].Text != "多用感官细节" {
		t.Fatalf("tech-blog scoped: %+v %+v", r.Style, r.ExcludedStyle)
	}

	// ForGenre 按作品自己的文体过滤，而不是 default_genre。
	r, ps := ForGenre(setup(t, config("tech-blog", "[]", ""), nil), "novel")
	if len(ps) > 0 || len(r.Style) != 0 || len(r.ExcludedStyle) != 2 {
		t.Fatalf("ForGenre novel: %+v %+v", r, ps)
	}
}

func TestStyleAppliesToSchema(t *testing.T) {
	for name, style := range map[string]string{
		"unknown category": "[{text: 先给结论, applies_to: [poetry]}]",
		"empty applies_to": "[{text: 先给结论, applies_to: []}]",
		"missing text":     "[{applies_to: [article]}]",
		"extra field":      "[{text: 先给结论, applies_to: [article], genre: novel}]",
		"empty string":     "[\"\"]",
	} {
		pack := "schema_version: 1\nid: bad\ntitle: 坏\nstyle: " + style + "\n"
		r, ps := Resolve(setup(t, config("novel", "[bad]", ""), map[string]string{"bad": pack}))
		if r != nil || !slices.Contains(codes(ps), "artifact.schema-violation") {
			t.Fatalf("%s: resolved = %+v, problems = %+v", name, r, ps)
		}
	}
}
