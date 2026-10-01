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

const housePack = `schema_version: 1
id: house
title: 团队规范
rules:
  length:
    severity: error
    params: {max: 20}
`

// writePack 在 dir 的工作区中写入 .writing-anything/packs/<name>.yaml。
func writePack(t *testing.T, dir, name, content string) {
	t.Helper()
	packs := filepath.Join(dir, ".writing-anything", "packs")
	if err := os.MkdirAll(packs, 0o755); err != nil {
		t.Fatal(err)
	}
	writeFile(t, filepath.Join(packs, name+".yaml"), content)
}

// mustRunWS 以 --json 运行工作区命令并断言成功。
func mustRunWS(t *testing.T, dir string, args ...string) {
	t.Helper()
	if code, res := runWS(t, dir, args...); code != 0 || !res.Valid {
		t.Fatalf("%v: exit code = %d, result = %+v", args, code, res)
	}
}

// noNullArrays 断言 JSON 值中除 genre、waiver 与 resolved 这类可为 null 的对象字段外没有 null（shared-envelope）。
func noNullArrays(t *testing.T, v any, path string) {
	t.Helper()
	switch v := v.(type) {
	case map[string]any:
		for k, c := range v {
			if c == nil && k != "genre" && k != "waiver" && k != "resolved" {
				t.Errorf("%s.%s is null", path, k)
			}
			noNullArrays(t, c, path+"."+k)
		}
	case []any:
		for i, c := range v {
			noNullArrays(t, c, fmt.Sprintf("%s[%d]", path, i))
		}
	}
}

// resolved 解码 config resolved --json 输出中的 resolved 字段。
func (r result) resolved(t *testing.T) map[string]any {
	t.Helper()
	if r.Resolved == nil || string(*r.Resolved) == "null" {
		return nil
	}
	var m map[string]any
	if err := json.Unmarshal(*r.Resolved, &m); err != nil {
		t.Fatal(err)
	}
	return m
}

// get 按点分路径读取嵌套的 JSON 对象。
func get(v any, path string) any {
	for _, k := range strings.Split(path, ".") {
		m, _ := v.(map[string]any)
		v = m[k]
	}
	return v
}

func TestConfigResolvedDefault(t *testing.T) {
	dir := newWorkspace(t)
	code, res := runWS(t, dir, "config", "resolved")
	r := res.resolved(t)
	if code != 0 || !res.Valid || len(res.ChangedPaths) != 0 || r["genre"] != nil {
		t.Fatalf("exit code = %d, result = %+v", code, res)
	}
	if packs := r["packs"].([]any); len(packs) != 1 || get(packs[0], "id") != "default" || get(packs[0], "origin") != "builtin" {
		t.Fatalf("packs = %v", packs)
	}
	for id, rule := range r["rules"].(map[string]any) {
		if get(rule, "type.source") != "pack:default" || get(rule, "enabled") != true || get(rule, "waiver") != nil {
			t.Errorf("rule %s = %v", id, rule)
		}
	}
}

func TestConfigResolvedLayers(t *testing.T) {
	dir := newWorkspace(t, "--genre", "minimal")
	code, res := runWS(t, dir, "config", "resolved")
	r := res.resolved(t)
	if code != 0 || get(r, "genre.id") != "minimal" || len(r["unitTypes"].([]any)) == 0 || get(r["stages"].([]any)[0], "source") != "genre:minimal" {
		t.Fatalf("builtin-genre-resolves: exit code = %d, resolved = %v", code, r)
	}
	noNullArrays(t, r, "resolved")
	if children, ok := get(r["unitTypes"].([]any)[1], "children").([]any); !ok || len(children) != 0 {
		t.Errorf("section children = %v, want []", get(r["unitTypes"].([]any)[1], "children"))
	}
	// resolved-brief-required：article 声明 [audience, purpose]，section 为空数组。
	if req, ok := get(r["unitTypes"].([]any)[0], "briefRequired").([]any); !ok || len(req) != 2 || req[0] != "audience" || req[1] != "purpose" {
		t.Errorf("article briefRequired = %v", get(r["unitTypes"].([]any)[0], "briefRequired"))
	}
	if req, ok := get(r["unitTypes"].([]any)[1], "briefRequired").([]any); !ok || len(req) != 0 {
		t.Errorf("section briefRequired = %v, want []", get(r["unitTypes"].([]any)[1], "briefRequired"))
	}
	writePack(t, dir, "house", housePack)
	mustRunWS(t, dir, "config", "set", "packs", "house")
	_, res = runWS(t, dir, "config", "resolved")
	length := get(res.resolved(t), "rules.length")
	for path, want := range map[string]any{
		"type.value": "length-range", "type.source": "genre:minimal",
		"severity.value": "error", "severity.source": "pack:house",
		"params.max.value": 20.0, "params.max.source": "pack:house",
		"params.min.source": "genre:minimal",
	} {
		if got := get(length, path); got != want {
			t.Errorf("pack-overrides-genre: rules.length.%s = %v, want %v", path, got, want)
		}
	}
	writeFile(t, configPath(dir), readFile(t, configPath(dir))+"rules:\n  length:\n    params: {max: 30}\n")
	_, res = runWS(t, dir, "config", "resolved")
	if length := get(res.resolved(t), "rules.length"); get(length, "params.max.value") != 30.0 || get(length, "params.max.source") != "project" || get(length, "severity.source") != "pack:house" {
		t.Errorf("project-overrides-pack: rules.length = %v", length)
	}
}

// config resolved 只读：不写文件，也不改动任何文件的修改时间。
func TestConfigResolvedReadOnly(t *testing.T) {
	dir := newWorkspace(t, "--genre", "minimal")
	writePack(t, dir, "house", housePack)
	mustRunWS(t, dir, "config", "set", "packs", "house")
	old := time.Now().Add(-time.Hour)
	var files []string
	filepath.Walk(dir, func(p string, info os.FileInfo, err error) error {
		files = append(files, p)
		return os.Chtimes(p, old, old)
	})
	if code, res := runWS(t, dir, "config", "resolved"); code != 0 || len(res.ChangedPaths) != 0 {
		t.Fatalf("exit code = %d, result = %+v", code, res)
	}
	for _, p := range files {
		if info, err := os.Stat(p); err != nil || !info.ModTime().Equal(old) {
			t.Errorf("%s was modified", p)
		}
	}
}

func TestConfigResolvedProblems(t *testing.T) {
	dir := newWorkspace(t)
	writePack(t, dir, "house", "schema_version: 1\nid: house\ntitle: t\nrules:\n  r9: {severity: error}\nwaivers:\n  - {rule: nope, reason: x}\n")
	mustRunWS(t, dir, "config", "set", "packs", "house")
	code, res := runWS(t, dir, "config", "resolved")
	if code != 1 || res.Valid || res.resolved(t) != nil {
		t.Fatalf("exit code = %d, result = %+v", code, res)
	}
	if d := diag(t, res, "rule.missing-type"); d.Path != ".writing-anything/packs/house.yaml" || d.Field != "rules.r9" || !slices.Contains(d.Next, "wa-cli validate --dir "+dir+" --json") {
		t.Errorf("missing-type: %+v", d)
	}
	if d := diag(t, res, "rule.unknown-waiver-target"); d.Field != "waivers[0].rule" {
		t.Errorf("waiver-unknown-rule: %+v", d)
	}
	writePack(t, dir, "house", "schema_version: 1\nid: house\ntitle: t\nrules:\n  r:\n    type: length-range\n    params: {script: x}\n")
	code, res = runWS(t, dir, "config", "resolved")
	if d := diag(t, res, "artifact.executable-content"); code != 1 || d.Field != "rules.r.params.script" || !strings.Contains(d.Message, "第 7 行") {
		t.Errorf("script-field-in-params: exit code = %d, %+v", code, d)
	}
	writePack(t, dir, "house", "schema_version: 1\nid: house\ntitle: t\nrules:\n  length: {type: length-range}\n")
	mustRunWS(t, dir, "config", "set", "default_genre", "minimal")
	writePack(t, dir, "house", "schema_version: 1\nid: house\ntitle: t\nrules:\n  length: {type: paragraph-length}\n")
	if code, res = runWS(t, dir, "config", "resolved"); code != 1 || diag(t, res, "rule.type-conflict").Field != "rules.length.type" {
		t.Errorf("type-conflict: exit code = %d, %+v", code, res)
	}
	empty := t.TempDir()
	if code, res := runWS(t, empty, "config", "resolved"); code != 1 || !slices.Contains(diag(t, res, "workspace.not-found").Next, "wa-cli init --dir "+empty+" --json") {
		t.Errorf("no workspace: exit code = %d, %+v", code, res)
	}
}

func TestValidatePacksAndReferences(t *testing.T) {
	dir := newWorkspace(t)
	if code, res := runWS(t, dir, "validate"); code != 0 || !res.Valid {
		t.Fatalf("no-packs-dir: exit code = %d, result = %+v", code, res)
	}
	writePack(t, dir, "house", "schema_version: 1\nid: house\ntitle: t\n")
	mustRunWS(t, dir, "config", "set", "packs", "house")
	if code, res := runWS(t, dir, "validate"); code != 0 || !slices.Equal(res.Checked, []string{".writing-anything/config.yaml", ".writing-anything/packs/house.yaml"}) {
		t.Fatalf("checks-pack-files: exit code = %d, result = %+v", code, res)
	}

	mustRunWS(t, dir, "config", "set", "default_genre", "no-such-genre")
	code, res := runWS(t, dir, "validate")
	d := diag(t, res, "config.unknown-genre")
	if code != 1 || d.Path != ".writing-anything/config.yaml" || d.Field != "default_genre" || !slices.Contains(d.Next, "wa-cli config set default_genre minimal --dir "+dir+" --json") {
		t.Fatalf("unknown-genre: exit code = %d, %+v", code, d)
	}
	code, res = runWS(t, dir, "status")
	if diag(t, res, "config.unknown-genre"); code != 1 || res.Valid || res.config(t)["defaultGenre"] != "no-such-genre" {
		t.Fatalf("status-unknown-genre: exit code = %d, result = %+v", code, res)
	}
	mustRunWS(t, dir, "config", "set", "default_genre", "")

	mustRunWS(t, dir, "config", "set", "packs", "default,no-such-pack")
	code, res = runWS(t, dir, "validate")
	if d := diag(t, res, "config.unknown-pack"); code != 1 || d.Field != "packs[1]" || !slices.Contains(d.Next, "wa-cli help config set --json") || !slices.Contains(d.Next, "wa-cli validate --dir "+dir+" --json") {
		t.Fatalf("unknown-pack: exit code = %d, %+v", code, d)
	}
	mustRunWS(t, dir, "config", "set", "packs", "")

	for name, tt := range map[string]struct{ content, code, field string }{
		"office":  {"schema_version: 1\nid: house\ntitle: t\n", "pack.id-mismatch", "id"},
		"default": {"schema_version: 1\nid: default\ntitle: t\n", "pack.id-conflict", "id"},
		"typo":    {"schema_version: 1\nid: typo\ntitle: t\nrules:\n  r9: {type: run-shell}\n", "rule.unknown-type", "rules.r9.type"},
		"waiver":  {"schema_version: 1\nid: waiver\ntitle: t\nwaivers:\n  - {rule: r}\n", "artifact.schema-violation", "waivers[0].reason"},
		"tagged":  {"schema_version: 1\nid: tagged\ntitle: t\ndescription: !exec boom\n", "artifact.executable-content", "description"},
		"dupes":   {"schema_version: 1\nid: dupes\ntitle: t\nglossary:\n  - {term: 规范包}\n  - {term: 规范包}\n", "artifact.duplicate-id", "glossary[1].term"},
	} {
		t.Run(name, func(t *testing.T) {
			dir := newWorkspace(t)
			writePack(t, dir, name, tt.content)
			rel := ".writing-anything/packs/" + name + ".yaml"
			for _, args := range [][]string{{"validate"}, {"validate", rel}} {
				code, res := runWS(t, dir, args...)
				if d := diag(t, res, tt.code); code != 1 || d.Path != rel || d.Field != tt.field || len(res.Diagnostics) != 1 {
					t.Errorf("%v: exit code = %d, diagnostics = %+v", args, code, res.Diagnostics)
				}
				if d := res.Diagnostics[0]; name == "typo" && !strings.Contains(d.Message, "ad-compliance") {
					t.Errorf("unknown-rule-type: message %q lacks ad-compliance", d.Message)
				}
				if d := res.Diagnostics[0]; name == "tagged" && !strings.Contains(d.Message, "第 4 行") {
					t.Errorf("custom-tag: message %q lacks the line number", d.Message)
				}
			}
		})
	}
}

func TestConfigResolvedText(t *testing.T) {
	dir := newWorkspace(t, "--genre", "minimal")
	writePack(t, dir, "house", housePack+`waivers:
  - {rule: length, reason: 不限长度}
style: [短句优先]
glossary:
  - {term: 规范包, english: Pack, avoid: [插件包]}
banned_terms:
  - {term: 赋能, reason: 空泛, replacement: 帮助}
`)
	mustRunWS(t, dir, "config", "set", "packs", "house")
	code, stdout, stderr := run(t, "config", "resolved", "--dir", dir)
	for _, line := range []string{
		"文体（Genre）：minimal（builtin）\n",
		"  house（workspace，.writing-anything/packs/house.yaml）\n",
		"  article 文章，子单元类型 section（来自 genre:minimal）\n",
		"  brief 写作简报，必需产物 brief（来自 genre:minimal）\n",
		"  length：length-range（来自 genre:minimal），error（来自 pack:house），max=20（来自 pack:house），min=300（来自 genre:minimal）；已豁免（来自 pack:house）：不限长度\n",
		"  规范包（Pack），避免写作 插件包（来自 pack:house）\n",
		"  赋能：空泛，改用 帮助（来自 pack:house）\n",
		"  短句优先（来自 pack:house）\n",
		"  clarity 清晰：读者读一遍就能说出核心观点。（来自 genre:minimal）\n",
	} {
		if code != 0 || stderr != "" || !strings.Contains(stdout, line) {
			t.Errorf("exit code = %d, stdout lacks %q:\n%s%s", code, line, stdout, stderr)
		}
	}
	mustRunWS(t, dir, "config", "set", "packs", "nope")
	code, stdout, stderr = run(t, "config", "resolved", "--dir", dir)
	if code != 1 || stdout != "" || !strings.Contains(stderr, "错误 [config.unknown-pack]") {
		t.Fatalf("broken: exit code = %d, stdout = %q, stderr = %q", code, stdout, stderr)
	}
	code, stdout, _ = run(t, "config", "resolved", "--dir", newWorkspace(t))
	for _, line := range []string{"文体（Genre）：未设置\n", "  default（builtin）\n", "单元类型（Unit Type）：无\n", "阶段（Stage）：无\n", "术语表（Glossary）：无\n"} {
		if code != 0 || !strings.Contains(stdout, line) {
			t.Errorf("no genre: exit code = %d, stdout lacks %q:\n%s", code, line, stdout)
		}
	}
}

// 内置产物的问题不填 path，message 以其出处开头；next 中的整体校验能重新检出同一问题（S1）。
func TestBuiltinLayerDiagnostic(t *testing.T) {
	dir := newWorkspace(t)
	writePack(t, dir, "a", "schema_version: 1\nid: a\ntitle: A\nrules:\n  ai-cliche: {type: banned-terms}\n")
	mustRunWS(t, dir, "config", "set", "packs", "a,default")
	code, res := runWS(t, dir, "config", "resolved")
	d := diag(t, res, "rule.type-conflict")
	if code != 1 || d.Path != "" || !strings.HasPrefix(d.Message, "内置规范包 default：") || !slices.Equal(d.Next, []string{"wa-cli validate --dir " + dir + " --json"}) {
		t.Fatalf("builtin-layer-problem: exit code = %d, %+v", code, d)
	}
	if code, res := runWS(t, dir, "validate"); code != 1 || diag(t, res, "rule.type-conflict").Message != d.Message {
		t.Fatalf("next must re-detect the problem: exit code = %d, %+v", code, res)
	}
}

// validate <config.yaml> 也检查项目配置中的规则类型。
func TestValidateConfigRuleTypes(t *testing.T) {
	dir := newWorkspace(t)
	writeFile(t, configPath(dir), readFile(t, configPath(dir))+"rules:\n  r1: {type: run-shell}\n")
	for _, args := range [][]string{{"validate"}, {"validate", ".writing-anything/config.yaml"}} {
		if code, res := runWS(t, dir, args...); code != 1 || diag(t, res, "rule.unknown-type").Field != "rules.r1.type" {
			t.Errorf("%v: exit code = %d, %+v", args, code, res)
		}
	}
}
