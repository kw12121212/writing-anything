package resolve

import (
	"os"
	"path/filepath"
	"slices"
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
