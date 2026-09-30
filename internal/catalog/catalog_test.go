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
	g, _, _ := LoadGenre("minimal")
	if s := g.Stages[0]; s.ID != "brief" || !slices.Equal(s.RequiredArtifacts, []string{"brief"}) {
		t.Errorf("minimal stages[0] = %+v", s)
	}
	// 省略的 children 记为空数组而不是 nil，输出中不出现 null。
	if u := g.UnitTypes[1]; u.ID != "section" || u.Children == nil {
		t.Errorf("minimal unit_types[1] = %#v", u)
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
	}
	ps := checkGenre(g)
	want := []string{"artifact.duplicate-id", "genre.unknown-unit-type", "artifact.duplicate-id", "artifact.duplicate-id", "rule.unknown-type"}
	if !slices.Equal(codes(ps), want) {
		t.Fatalf("codes = %v, want %v", codes(ps), want)
	}
	for _, p := range ps {
		if p.Origin != "内置文体 g" || p.Path != "" {
			t.Errorf("built-in genre problem %+v must name the genre and have no path", p)
		}
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
