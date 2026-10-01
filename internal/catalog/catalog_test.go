package catalog

import (
	"os"
	"path/filepath"
	"slices"
	"strings"
	"testing"

	"github.com/kw12121212/writing-anything/internal/workspace"
)

// project 返回一个临时项目目录，packs/ 下放入 testdata/packs 中列出的规范包文件。
func project(t *testing.T, files ...string) string {
	t.Helper()
	root := t.TempDir()
	dir := filepath.Join(root, ".writing-anything", "packs")
	if err := os.MkdirAll(dir, 0o755); err != nil {
		t.Fatal(err)
	}
	for _, f := range files {
		data, err := os.ReadFile(filepath.Join("testdata", "packs", f))
		if err != nil {
			t.Fatal(err)
		}
		if err := os.WriteFile(filepath.Join(dir, f), data, 0o644); err != nil {
			t.Fatal(err)
		}
	}
	return root
}

func codes(ps []workspace.Problem) []string {
	var out []string
	for _, p := range ps {
		out = append(out, p.Code)
	}
	return out
}

// 每个内置文体与规范包都通过与工作区规范包相同的校验（workspace-artifacts/builtin-catalog）。
func TestBuiltinsAreValid(t *testing.T) {
	if !slices.Contains(BuiltinGenres(), "minimal") || !slices.Contains(BuiltinPacks(), "default") {
		t.Fatalf("builtins = %v %v, want minimal genre and default pack", BuiltinGenres(), BuiltinPacks())
	}
	for _, id := range BuiltinGenres() {
		g, ps, ok := LoadGenre(id)
		if !ok || g == nil || len(ps) > 0 || len(g.UnitTypes) == 0 || len(g.Stages) == 0 {
			t.Errorf("genre %s: ok = %v, genre = %+v, problems = %+v", id, ok, g, ps)
		}
	}
	for _, id := range BuiltinPacks() {
		p, ps, ok := FindPack(t.TempDir(), id)
		if !ok || p == nil || len(ps) > 0 || p.Origin != "builtin" || p.Path != "" {
			t.Errorf("pack %s: ok = %v, pack = %+v, problems = %+v", id, ok, p, ps)
		}
	}
	if len(BuiltinMethods()) != 9 {
		t.Errorf("builtin methods = %v", BuiltinMethods())
	}
	for _, id := range BuiltinMethods() {
		m, ps, ok := LoadMethod(id)
		if !ok || m == nil || len(ps) > 0 || m.Kind != MethodArticle || len(m.Parts()) == 0 {
			t.Errorf("method %s: ok = %v, method = %+v, problems = %+v", id, ok, m, ps)
		}
	}
	if _, _, ok := LoadMethod("nope"); ok {
		t.Error("unknown method loaded")
	}
	g, _, _ := LoadGenre("minimal")
	if s := g.Stages[0]; s.ID != "brief" || !slices.Equal(s.RequiredArtifacts, []string{"brief"}) {
		t.Errorf("minimal stages[0] = %+v", s)
	}
	// 省略的 children 与 brief_required 记为空数组而不是 nil，输出中不出现 null。
	if u := g.UnitTypes[1]; u.ID != "section" || u.Children == nil || u.BriefRequired == nil || len(u.BriefRequired) != 0 {
		t.Errorf("minimal unit_types[1] = %#v", u)
	}
	if u := g.UnitTypes[0]; !slices.Equal(u.BriefRequired, []string{"audience", "purpose"}) {
		t.Errorf("minimal article brief_required = %v", u.BriefRequired)
	}
}

func TestUnknownGenre(t *testing.T) {
	if g, ps, ok := LoadGenre("no-such-genre"); ok || g != nil || ps != nil {
		t.Fatalf("LoadGenre(no-such-genre) = %+v, %+v, %v", g, ps, ok)
	}
}

func TestFindWorkspacePack(t *testing.T) {
	root := project(t, "house.yaml")
	p, ps, ok := FindPack(root, "house")
	if !ok || len(ps) > 0 || p.Origin != "workspace" || p.Path != ".writing-anything/packs/house.yaml" || p.Rules["length"].Params["max"] != 3000.0 {
		t.Fatalf("FindPack(house) = %+v, %+v, %v", p, ps, ok)
	}
	if p, ps, ok := FindPack(root, "nope"); ok || p != nil || ps != nil {
		t.Fatalf("FindPack(nope) = %+v, %+v, %v", p, ps, ok)
	}
}

func TestPackProblems(t *testing.T) {
	root := project(t, "office.yaml", "default.yaml", "dupes.yaml", "broken.yaml")
	tests := []struct {
		file   string
		codes  []string
		fields []string
	}{
		{"office.yaml", []string{"pack.id-mismatch"}, []string{"id"}},
		{"default.yaml", []string{"pack.id-conflict"}, []string{"id"}},
		{"dupes.yaml", []string{"artifact.duplicate-id", "artifact.duplicate-id", "artifact.duplicate-id", "rule.unknown-type"},
			[]string{"glossary[1].term", "banned_terms[1].term", "rubric[1].id", "rules.r9.type"}},
		{"broken.yaml", []string{"artifact.parse-error"}, []string{""}},
	}
	for _, tt := range tests {
		rel := ".writing-anything/packs/" + tt.file
		_, ps := LoadPackFile(root, rel)
		var fields []string
		for _, p := range ps {
			fields = append(fields, p.Field)
			if p.Path != rel {
				t.Errorf("%s: path = %q", tt.file, p.Path)
			}
		}
		if !slices.Equal(codes(ps), tt.codes) || !slices.Equal(fields, tt.fields) {
			t.Errorf("%s: problems = %+v, want codes %v fields %v", tt.file, ps, tt.codes, tt.fields)
		}
	}
	_, ps := LoadPackFile(root, ".writing-anything/packs/dupes.yaml")
	if msg := ps[3].Message; !strings.Contains(msg, "run-shell") || !strings.Contains(msg, "length-range") {
		t.Errorf("unknown type message %q must name the type and list the known types", msg)
	}
	if _, ps := LoadPackFile(root, ".writing-anything/packs/missing.yaml"); !slices.Equal(codes(ps), []string{"artifact.not-found"}) {
		t.Errorf("missing file: problems = %+v", ps)
	}
}

func TestCheckGenre(t *testing.T) {
	g := &Genre{
		ID:        "g",
		UnitTypes: []UnitType{{ID: "a", Children: []string{"b", "x"}}, {ID: "b"}, {ID: "a"}},
		Stages:    []Stage{{ID: "s"}, {ID: "s"}},
		Rubric:    []RubricItem{{ID: "r"}, {ID: "r"}},
		Rules:     map[string]Rule{"z": {Type: "nope"}},
		// 默认写作法必须是内置写作法。
		DefaultMethod: "nope",
	}
	ps := checkGenre(g)
	want := []string{"artifact.duplicate-id", "genre.unknown-unit-type", "artifact.duplicate-id", "artifact.duplicate-id", "genre.unknown-method", "rule.unknown-type"}
	if !slices.Equal(codes(ps), want) {
		t.Fatalf("codes = %v, want %v", codes(ps), want)
	}
	for _, p := range ps {
		if p.Origin != "内置文体 g" || p.Path != "" {
			t.Errorf("built-in genre problem %+v must name the genre and have no path", p)
		}
	}
}

// 阶段 id done 保留为生命周期终点。
func TestCheckGenreReservedStage(t *testing.T) {
	g := &Genre{ID: "g", UnitTypes: []UnitType{{ID: "a"}}, Stages: []Stage{{ID: "draft"}, {ID: "done"}}}
	ps := checkGenre(g)
	if len(ps) != 1 || ps[0].Code != "genre.reserved-stage-id" || ps[0].Field != "stages[1].id" {
		t.Fatalf("problems = %+v, want one genre.reserved-stage-id at stages[1].id", ps)
	}
}

func TestCheckWorkspacePacks(t *testing.T) {
	root := project(t, "house.yaml", "office.yaml")
	// 隐藏文件（如编辑器的临时文件）不当作规范包。
	os.WriteFile(filepath.Join(root, ".writing-anything", "packs", ".house.swp.yaml"), []byte("x: [\n"), 0o644)
	checked, ps := CheckWorkspacePacks(root)
	if !slices.Equal(checked, []string{".writing-anything/packs/house.yaml", ".writing-anything/packs/office.yaml"}) || !slices.Equal(codes(ps), []string{"pack.id-mismatch"}) {
		t.Fatalf("checked = %v, problems = %+v", checked, ps)
	}
	if checked, ps := CheckWorkspacePacks(t.TempDir()); checked != nil || ps != nil {
		t.Fatalf("without packs/: checked = %v, problems = %+v", checked, ps)
	}
}

// 规范包中的写作法：id 重复、部分 id 重复与节拍位置范围（method-validation）；结构类与规划类写作法的 Parts。
func TestPackMethods(t *testing.T) {
	p := &Pack{ID: "house", Origin: OriginWorkspace, Path: ".writing-anything/packs/house.yaml", Methods: []Method{
		{ID: "m", Kind: MethodStructure, Beats: []MethodPart{{ID: "a", Position: &Position{Min: 0.8, Max: 0.2}}, {ID: "a"}}, Rubric: []RubricItem{{ID: "r"}, {ID: "r"}}},
		{ID: "m", Kind: MethodPlanning, Steps: []MethodPart{{ID: "s"}}},
	}}
	got := codes(checkPack(p))
	want := []string{"artifact.duplicate-id", "artifact.duplicate-id", "method.invalid-position", "artifact.duplicate-id"}
	if !slices.Equal(got, want) {
		t.Fatalf("codes = %v, want %v", got, want)
	}
	if len(p.Methods[0].Parts()) != 2 || p.Methods[1].Parts() != nil {
		t.Fatalf("parts: %v %v", p.Methods[0].Parts(), p.Methods[1].Parts())
	}
}

// MethodSet.Pick：写作简报的取值优先，空时用文体默认，none 不用写作法，不存在时返回其 id；规范包中保留 id none 的写作法不列出。
func TestMethodSetPick(t *testing.T) {
	root := t.TempDir()
	pack := "schema_version: 1\nid: house\ntitle: 团队\nmethods:\n  - {id: none, title: 保留, kind: planning, steps: [{id: a, title: 甲}]}\n  - {id: mine, title: 自有, kind: planning, steps: [{id: a, title: 甲}]}\n"
	for rel, content := range map[string]string{".writing-anything/config.yaml": "schema_version: 1\nlanguage: zh-CN\npacks: [house]\ngate_level: standard\n", ".writing-anything/packs/house.yaml": pack} {
		p := filepath.Join(root, filepath.FromSlash(rel))
		os.MkdirAll(filepath.Dir(p), 0o755)
		os.WriteFile(p, []byte(content), 0o644)
	}
	set := LoadMethods(root)
	if slices.Contains(set.IDs(), "none") || !slices.Contains(set.IDs(), "mine") || !slices.Contains(set.IDs(), "pyramid-scqa") {
		t.Fatalf("ids = %v", set.IDs())
	}
	for _, tc := range []struct{ raw, def, want, missing string }{
		{"mine", "pyramid-scqa", "mine", ""},
		{"", "pyramid-scqa", "pyramid-scqa", ""},
		{"none", "pyramid-scqa", "", ""},
		{"", "", "", ""},
		{"gone", "", "", "gone"},
	} {
		m, missing := set.Pick(tc.raw, tc.def)
		got := ""
		if m != nil {
			got = m.ID
		}
		if got != tc.want || missing != tc.missing {
			t.Errorf("Pick(%q, %q) = %q, %q", tc.raw, tc.def, got, missing)
		}
	}
	if _, ok := set.Find("nope"); ok {
		t.Error("found unknown method")
	}
}
