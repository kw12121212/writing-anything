package work

import (
	"fmt"
	"os"
	"path/filepath"
	"slices"
	"strings"
	"testing"

	"github.com/kw12121212/writing-anything/internal/catalog"
	"github.com/kw12121212/writing-anything/internal/decision"
	"github.com/kw12121212/writing-anything/internal/review"
	"github.com/kw12121212/writing-anything/internal/schema"
	"github.com/kw12121212/writing-anything/internal/workspace"
)

// project 返回一个含 works/ 的临时项目目录，并放入 testdata/works 中列出的作品。
func project(t *testing.T, works ...string) string {
	t.Helper()
	root := t.TempDir()
	if err := os.MkdirAll(filepath.Join(root, ".writing-anything", "works"), 0o755); err != nil {
		t.Fatal(err)
	}
	for _, w := range works {
		put(t, root, Path(w), string(read(t, filepath.Join("testdata", "works", w, FileName))))
	}
	// completeBrief 的字段出处（Field Provenance）指向这份标准素材。
	put(t, root, ".writing-anything/sources/note/source.yaml", "schema_version: 1\nid: note\ntype: author-note\nfile: original.txt\nsha256: aaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaa\nbytes: 1\n")
	put(t, root, ".writing-anything/sources/note/original.txt", "作者原话\n")
	return root
}

func read(t *testing.T, path string) []byte {
	t.Helper()
	data, err := os.ReadFile(path)
	if err != nil {
		t.Fatal(err)
	}
	return data
}

// put 在 root 下写入 rel，必要时创建目录。
func put(t *testing.T, root, rel, content string) {
	t.Helper()
	p := abs(root, rel)
	if err := os.MkdirAll(filepath.Dir(p), 0o755); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(p, []byte(content), 0o644); err != nil {
		t.Fatal(err)
	}
}

// genres 在内置文体之外提供 testdata/genres/ 中的测试文体 book 与 essay。
func genres(t *testing.T) genreLookup {
	t.Helper()
	test := map[string]*catalog.Genre{}
	for _, id := range []string{"book", "essay", "nested"} {
		doc, p := schema.Parse(read(t, filepath.Join("testdata", "genres", id+".yaml")))
		if p != nil {
			t.Fatal(p)
		}
		// 测试文体同样要通过文体的 Schema，夹具写错时及早失败。
		if ps := schema.Named("genre").ValidateNode(doc); len(ps) > 0 {
			t.Fatalf("testdata genre %s: %+v", id, ps)
		}
		var f struct {
			ID        string `json:"id"`
			UnitTypes []struct {
				ID                string   `json:"id"`
				Children          []string `json:"children"`
				StageFromChildren bool     `json:"stage_from_children"`
			} `json:"unit_types"`
			Stages []struct {
				ID                string   `json:"id"`
				RequiredArtifacts []string `json:"required_artifacts"`
			} `json:"stages"`
		}
		catalog.Decode(doc, &f)
		g := &catalog.Genre{ID: f.ID}
		for _, u := range f.UnitTypes {
			g.UnitTypes = append(g.UnitTypes, catalog.UnitType{ID: u.ID, Children: nonNilT(u.Children), StageFromChildren: u.StageFromChildren})
		}
		for _, s := range f.Stages {
			g.Stages = append(g.Stages, catalog.Stage{ID: s.ID, RequiredArtifacts: s.RequiredArtifacts})
		}
		test[id] = g
	}
	return func(id string) (*catalog.Genre, []workspace.Problem, bool) {
		if g, ok := test[id]; ok {
			return g, nil, true
		}
		return catalog.LoadGenre(id)
	}
}

func nonNilT(s []string) []string {
	if s == nil {
		return []string{}
	}
	return s
}

func codes(ps []workspace.Problem) []string {
	var out []string
	for _, p := range ps {
		out = append(out, p.Code)
	}
	return out
}

func refs(us []*Unit) []string {
	var out []string
	for _, u := range us {
		out = append(out, u.Ref)
	}
	return out
}

// 单层单元树：minimal 文体的 article → section。
func TestLoadSingleLevel(t *testing.T) {
	root := project(t, "my-post")
	w, ps := Load(root, "my-post")
	if len(ps) > 0 {
		t.Fatalf("problems = %+v", ps)
	}
	if got := refs(w.Units()); !slices.Equal(got, []string{"my-post", "my-post/intro", "my-post/body"}) {
		t.Fatalf("units = %v", got)
	}
	intro := w.find("intro")
	if intro.Parent != w.Root || w.UnitDir(intro) != ".writing-anything/works/my-post/units/intro" || w.UnitDir(w.Root) != ".writing-anything/works/my-post" {
		t.Fatalf("intro = %+v, dir = %s", intro, w.UnitDir(intro))
	}
	if w.NextStage(w.Root) != "draft" || w.NextStage(w.find("body")) != "done" {
		t.Fatalf("next stages = %s, %s", w.NextStage(w.Root), w.NextStage(w.find("body")))
	}
}

// 多层单元树：测试文体 book → part → chapter → scene；深层单元同样用 <work>/<unit-id> 引用。
func TestMultiLevel(t *testing.T) {
	root := project(t, "saga")
	g := genres(t)
	w, ps := load(root, "saga", g)
	if len(ps) > 0 {
		t.Fatalf("problems = %+v", ps)
	}
	want := []string{"saga", "saga/part-1", "saga/ch-1", "saga/s1", "saga/s2", "saga/ch-2", "saga/part-2"}
	if got := refs(w.Units()); !slices.Equal(got, want) {
		t.Fatalf("units = %v, want %v", got, want)
	}
	_, s1, ps := resolve(root, "saga/s1", g)
	if len(ps) > 0 || s1.Parent.ID != "ch-1" || s1.Parent.Parent.ID != "part-1" || s1.field != "units[0].units[0].units[0]" {
		t.Fatalf("s1 = %+v, problems = %+v", s1, ps)
	}
	if _, _, _, ps := advance(root, "saga/s1", g); !slices.Equal(codes(ps), []string{"lifecycle.missing-artifact"}) || ps[0].Path != ".writing-anything/works/saga/units/s1" {
		t.Fatalf("advance without plan: problems = %+v", ps)
	}
	put(t, root, ".writing-anything/works/saga/units/s1/plan.md", "# 规划\n")
	w, s1, prev, ps := advance(root, "saga/s1", g)
	if len(ps) > 0 || prev != "plan" || s1.Stage != "draft" {
		t.Fatalf("advance: unit = %+v, previous = %s, problems = %+v", s1, prev, ps)
	}
	// 推进深层单元不影响其他单元（independent-units）。
	for _, u := range w.Units() {
		if u.ID != "s1" && u.ID != "s2" && u.ID != "ch-2" && u.Stage != "plan" {
			t.Errorf("%s stage = %s, want plan", u.Ref, u.Stage)
		}
	}
	// 当前阶段的每个缺失产物各报告一条诊断。
	if _, _, _, ps := advance(root, "saga/s2", g); !slices.Equal(codes(ps), []string{"lifecycle.missing-artifact", "lifecycle.missing-artifact"}) ||
		!strings.Contains(ps[0].Message, "draft.yaml") || !strings.Contains(ps[1].Message, "notes.yaml") {
		t.Fatalf("two missing artifacts: problems = %+v", ps)
	}
	if _, u, ps := addUnit(root, "saga/ch-2", "scene", "s3", Anchor{}, g); len(ps) > 0 || u.Ref != "saga/s3" || u.Parent.ID != "ch-2" || u.Stage != "plan" {
		t.Fatalf("add s3: unit = %+v, problems = %+v", u, ps)
	}
	if _, _, ps := addUnit(root, "saga/part-2", "scene", "s4", Anchor{}, g); !slices.Equal(codes(ps), []string{"work.child-type-not-allowed"}) || !strings.Contains(ps[0].Message, "chapter") {
		t.Fatalf("scene under part: problems = %+v", ps)
	}
}

// 作品校验的每种问题都给出错误码与字段路径。
func TestParseProblems(t *testing.T) {
	head := "schema_version: 1\nid: my-post\ngenre: minimal\ntype: article\nstage: brief\n"
	for _, tc := range []struct {
		name, src, code, field string
	}{
		{"unknown-unit-type", head + "units:\n  - {id: a, type: chapter, stage: brief}\n", "work.unknown-unit-type", "units[0].type"},
		{"child-type", head + "units:\n  - id: a\n    type: section\n    stage: brief\n    units:\n      - {id: b, type: section, stage: brief}\n", "work.child-type-not-allowed", "units[0].units[0].type"},
		{"unknown-stage", strings.Replace(head, "stage: brief", "stage: bogus", 1), "work.unknown-stage", "stage"},
		{"duplicate-id", head + "units:\n  - {id: intro, type: section, stage: brief}\n  - {id: intro, type: section, stage: brief}\n", "artifact.duplicate-id", "units[1].id"},
		{"unit-equals-work", head + "units:\n  - {id: my-post, type: section, stage: brief}\n", "artifact.duplicate-id", "units[0].id"},
		{"id-mismatch", strings.Replace(head, "id: my-post", "id: other", 1), "work.id-mismatch", "id"},
		{"root-type", strings.Replace(head, "type: article", "type: section", 1), "work.root-type-mismatch", "type"},
		{"unknown-genre", strings.Replace(head, "genre: minimal", "genre: nope", 1), "work.unknown-genre", "genre"},
		{"schema", strings.Replace(head, "stage: brief\n", "", 1), "artifact.schema-violation", "stage"},
	} {
		t.Run(tc.name, func(t *testing.T) {
			root := project(t)
			put(t, root, Path("my-post"), tc.src)
			w, ps := Load(root, "my-post")
			if w != nil || !slices.Contains(codes(ps), tc.code) {
				t.Fatalf("problems = %+v, want %s", ps, tc.code)
			}
			for _, p := range ps {
				if p.Code == tc.code && (p.Field != tc.field || p.Path != ".writing-anything/works/my-post/work.yaml") {
					t.Errorf("problem %+v, want field %s", p, tc.field)
				}
			}
			if tc.code == "work.unknown-genre" && !strings.Contains(ps[0].Message, "minimal") {
				t.Errorf("message %q must list the available genres", ps[0].Message)
			}
		})
	}
}

func TestResolve(t *testing.T) {
	root := project(t, "my-post")
	for ref, want := range map[string]string{"nope": "work.not-found", "../x": "work.not-found", "my-post/nope": "unit.not-found", "my-post/a/b": "unit.not-found", "my-post/my-post": "unit.not-found"} {
		if _, u, ps := Resolve(root, ref); u != nil || !slices.Equal(codes(ps), []string{want}) {
			t.Errorf("Resolve(%q) = %+v, %+v, want %s", ref, u, ps, want)
		}
	}
	if _, _, ps := Resolve(root, "nope"); ps[0].Path != ".writing-anything/works/nope" {
		t.Errorf("work.not-found path = %q", ps[0].Path)
	}
	if _, _, ps := Resolve(root, "../x"); ps[0].Path != "" {
		t.Errorf("invalid work id must have no path: %q", ps[0].Path)
	}
	if _, u, ps := Resolve(root, "my-post"); len(ps) > 0 || u.Parent != nil {
		t.Fatalf("root ref = %+v, %+v", u, ps)
	}
	// 作品目录存在但没有 work.yaml。
	os.MkdirAll(abs(root, Dir("empty")), 0o755)
	if _, _, ps := Resolve(root, "empty"); !slices.Equal(codes(ps), []string{"artifact.not-found"}) {
		t.Fatalf("empty work dir: %+v", ps)
	}
}

func TestCreate(t *testing.T) {
	root := project(t)
	w, created, ps := Create(root, "minimal", "my-post")
	if len(ps) > 0 || !slices.Equal(created, []string{".writing-anything/works/my-post", ".writing-anything/works/my-post/work.yaml"}) {
		t.Fatalf("created = %v, problems = %+v", created, ps)
	}
	if w.Root.Type != "article" || w.Root.Stage != "brief" || len(w.Root.Units) != 0 || w.Genre.ID != "minimal" {
		t.Fatalf("work = %+v", w.Root)
	}
	before := read(t, abs(root, Path("my-post")))
	if !strings.Contains(string(before), "units: []") {
		t.Errorf("work.yaml = %s, want an empty units array", before)
	}
	if _, _, ps := Create(root, "minimal", "my-post"); !slices.Equal(codes(ps), []string{"work.exists"}) || ps[0].Path != ".writing-anything/works/my-post" {
		t.Fatalf("existing: %+v", ps)
	}
	if after := read(t, abs(root, Path("my-post"))); string(after) != string(before) {
		t.Fatal("existing work.yaml was modified")
	}
	if _, _, ps := Create(root, "nope", "x"); !slices.Equal(codes(ps), []string{"work.unknown-genre"}) || ps[0].Path != "" || !strings.Contains(ps[0].Message, "minimal") {
		t.Fatalf("unknown genre: %+v", ps)
	}
	if _, _, ps := Create(root, "minimal", "My_Post"); !slices.Equal(codes(ps), []string{"artifact.schema-violation"}) || ps[0].Field != "id" || ps[0].Path != "" {
		t.Fatalf("invalid slug: %+v", ps)
	}
	entries, _ := os.ReadDir(abs(root, workspace.WorksPath))
	if len(entries) != 1 {
		t.Fatalf("works/ has %d entries, want only my-post", len(entries))
	}
	// works/ 缺失时无法创建作品目录。
	bare := t.TempDir()
	if _, _, ps := Create(bare, "minimal", "x"); !slices.Equal(codes(ps), []string{"workspace.write-failed"}) {
		t.Fatalf("without works/: %+v", ps)
	}
}

func TestAdvance(t *testing.T) {
	root := project(t, "my-post")
	before := string(read(t, abs(root, Path("my-post"))))
	if _, _, _, ps := Advance(root, "my-post"); !slices.Equal(codes(ps), []string{"lifecycle.missing-artifact"}) ||
		ps[0].Path != ".writing-anything/works/my-post" || !strings.Contains(ps[0].Message, "brief.yaml") || !strings.Contains(ps[0].Message, "brief.md") {
		t.Fatalf("missing brief: %+v", ps)
	}
	if string(read(t, abs(root, Path("my-post")))) != before {
		t.Fatal("work.yaml changed although advance was refused")
	}
	// 根单元的 brief.yaml 不满足子单元的必需产物。
	put(t, root, ".writing-anything/works/my-post/brief.yaml", completeBrief)
	if _, _, _, ps := Advance(root, "my-post/intro"); !slices.Equal(codes(ps), []string{"lifecycle.missing-artifact"}) || ps[0].Path != ".writing-anything/works/my-post/units/intro" {
		t.Fatalf("child without brief: %+v", ps)
	}
	w, u, prev, ps := Advance(root, "my-post")
	if len(ps) > 0 || prev != "brief" || u.Stage != "draft" || u != w.Root {
		t.Fatalf("advance: %+v %s %+v", u, prev, ps)
	}
	after := string(read(t, abs(root, Path("my-post"))))
	if want := strings.Replace(before, "stage: brief # 作者的行尾注释", "stage: draft # 作者的行尾注释", 1); after != want {
		t.Fatalf("work.yaml =\n%s\nwant only the root stage changed:\n%s", after, want)
	}
	// body 在最后一个阶段 review，推进后为 done；再推进报告 lifecycle.already-done。
	put(t, root, ".writing-anything/works/my-post/units/body/review.md", "ok\n")
	// 手写的 acceptance.md 不算验收；验收记录的正文 SHA-256 与当前正文不同时报告 acceptance.stale。
	put(t, root, ".writing-anything/works/my-post/units/body/acceptance.md", "ok\n")
	if _, _, _, ps := Advance(root, "my-post/body"); !slices.Equal(codes(ps), []string{"acceptance.invalid"}) {
		t.Fatalf("acceptance.md: %+v", ps)
	}
	put(t, root, ".writing-anything/works/my-post/units/body/draft.md", "正文\n")
	accepted := "schema_version: 1\ngate_level: fast\ndraft_sha256: %s\nconfirmed_by_author: false\naccepted_at: \"2026-10-01T00:00:00Z\"\n"
	put(t, root, ".writing-anything/works/my-post/units/body/acceptance.yaml", fmt.Sprintf(accepted, strings.Repeat("a", 64)))
	if _, _, _, ps := Advance(root, "my-post/body"); !slices.Equal(codes(ps), []string{"acceptance.stale"}) {
		t.Fatalf("stale acceptance: %+v", ps)
	}
	put(t, root, ".writing-anything/works/my-post/units/body/acceptance.yaml", "schema_version: 1\n")
	if _, _, _, ps := Advance(root, "my-post/body"); !slices.Equal(codes(ps), []string{"acceptance.invalid"}) {
		t.Fatalf("invalid acceptance: %+v", ps)
	}
	// 验收时的关卡强度（fast）比工作区当前的（standard）宽松时报告 acceptance.weaker-gate。
	put(t, root, ".writing-anything/works/my-post/units/body/acceptance.yaml", fmt.Sprintf(accepted, review.SHA256([]byte("正文\n"))))
	if _, _, _, ps := Advance(root, "my-post/body"); !slices.Equal(codes(ps), []string{"acceptance.weaker-gate"}) {
		t.Fatalf("weaker gate: %+v", ps)
	}
	put(t, root, ".writing-anything/works/my-post/units/body/acceptance.yaml", strings.Replace(fmt.Sprintf(accepted, review.SHA256([]byte("正文\n"))), "fast", "standard", 1))
	if _, u, _, ps := Advance(root, "my-post/body"); len(ps) > 0 || u.Stage != "done" {
		t.Fatalf("to done: %+v %+v", u, ps)
	}
	if _, _, _, ps := Advance(root, "my-post/body"); !slices.Equal(codes(ps), []string{"lifecycle.already-done"}) {
		t.Fatalf("already done: %+v", ps)
	}
	if _, _, _, ps := Advance(root, "my-post/nope"); !slices.Equal(codes(ps), []string{"unit.not-found"}) {
		t.Fatalf("unknown unit: %+v", ps)
	}
}

// 经由锚点、别名或合并键得到的节点不修改（workspace-artifacts/structured-edits）。
func TestAnchoredEdits(t *testing.T) {
	head := "schema_version: 1\nid: my-post\ngenre: minimal\ntype: article\n"
	for name, tc := range map[string]struct{ src, ref, op string }{
		"anchored-stage": {head + "stage: &s brief\nunits:\n  - {id: a, type: section, stage: *s}\n", "my-post", "advance"},
		"merged-unit":    {head + "stage: brief\nunits:\n  - &a {id: a, type: section, stage: brief}\n  - {<<: *a, id: b}\n", "my-post/b", "advance"},
		"anchored-units": {head + "stage: brief\nunits: &u []\n", "my-post", "add"},
	} {
		t.Run(name, func(t *testing.T) {
			root := project(t)
			put(t, root, Path("my-post"), tc.src)
			dir := strings.TrimPrefix(strings.TrimPrefix(tc.ref, "my-post"), "/")
			unitDir := Dir("my-post")
			if dir != "" {
				unitDir += "/units/" + dir
			}
			put(t, root, unitDir+"/brief.yaml", completeBrief)
			var ps []workspace.Problem
			if tc.op == "advance" {
				_, _, _, ps = Advance(root, tc.ref)
			} else {
				_, _, ps = AddUnit(root, tc.ref, "section", "new", Anchor{})
			}
			if !slices.Equal(codes(ps), []string{"work.anchored-value"}) {
				t.Fatalf("problems = %+v, want work.anchored-value", ps)
			}
			if got := string(read(t, abs(root, Path("my-post")))); got != tc.src {
				t.Fatalf("work.yaml changed:\n%s", got)
			}
		})
	}
}

func TestAddUnit(t *testing.T) {
	root := project(t)
	if _, _, ps := Create(root, "minimal", "my-post"); len(ps) > 0 {
		t.Fatal(ps)
	}
	put(t, root, Path("my-post"), "# 作者的注释\n"+string(read(t, abs(root, Path("my-post")))))
	w, u, ps := AddUnit(root, "my-post", "section", "intro", Anchor{})
	if len(ps) > 0 || u.Ref != "my-post/intro" || u.Parent != w.Root || u.Stage != "brief" || u.Type != "section" {
		t.Fatalf("add intro: %+v %+v", u, ps)
	}
	src := string(read(t, abs(root, Path("my-post"))))
	if !strings.Contains(src, "# 作者的注释") || !strings.Contains(src, "units:\n  - id: intro") {
		t.Fatalf("work.yaml =\n%s", src)
	}
	for _, tc := range []struct{ parent, typ, id, code string }{
		{"my-post", "article", "nested", "work.child-type-not-allowed"},
		{"my-post/intro", "section", "deep", "work.child-type-not-allowed"},
		{"my-post", "section", "intro", "artifact.duplicate-id"},
		{"my-post", "section", "my-post", "artifact.duplicate-id"},
		{"my-post", "section", "Bad_Id", "artifact.schema-violation"},
		{"my-post/nope", "section", "x", "unit.not-found"},
	} {
		if _, _, ps := AddUnit(root, tc.parent, tc.typ, tc.id, Anchor{}); !slices.Equal(codes(ps), []string{tc.code}) {
			t.Errorf("AddUnit(%s, %s, %s) = %+v, want %s", tc.parent, tc.typ, tc.id, ps, tc.code)
		}
	}
	if got := string(read(t, abs(root, Path("my-post")))); got != src {
		t.Fatalf("refused additions changed work.yaml:\n%s", got)
	}
	// 父单元没有 units 字段时新建该字段。
	put(t, root, Path("bare"), "schema_version: 1\nid: bare\ngenre: minimal\ntype: article\nstage: brief\n")
	if _, u, ps := AddUnit(root, "bare", "section", "one", Anchor{}); len(ps) > 0 || u.Ref != "bare/one" {
		t.Fatalf("add to bare: %+v %+v", u, ps)
	}
	if msg := allowedChildren(catalog.UnitType{ID: "section"}); !strings.Contains(msg, "不能包含子单元") {
		t.Errorf("allowedChildren = %q", msg)
	}
}

func TestLoadAll(t *testing.T) {
	root := project(t, "my-post")
	os.MkdirAll(abs(root, Dir(".hidden")), 0o755)
	os.MkdirAll(abs(root, Dir("draft-notes")), 0o755)
	put(t, root, workspace.WorksPath+"/readme.md", "不是作品\n")
	put(t, root, Path("broken"), "schema_version: 1\nid: broken\ngenre: minimal\ntype: article\nstage: bogus\n")
	works, checked, ps := LoadAll(root)
	if len(works) != 1 || works[0].ID != "my-post" {
		t.Fatalf("works = %+v", works)
	}
	if !slices.Equal(checked, []string{".writing-anything/works/broken/work.yaml", ".writing-anything/works/my-post/work.yaml"}) {
		t.Fatalf("checked = %v", checked)
	}
	if !slices.Equal(codes(ps), []string{"work.unknown-stage", "artifact.not-found"}) || ps[1].Path != ".writing-anything/works/draft-notes/work.yaml" {
		t.Fatalf("problems = %+v", ps)
	}
	if ps := CheckArtifact(root, ".writing-anything/works/broken/work.yaml"); !slices.Equal(codes(ps), []string{"work.unknown-stage"}) {
		t.Fatalf("CheckArtifact = %+v", ps)
	}
	if works, checked, ps := LoadAll(t.TempDir()); works != nil || checked != nil || ps != nil {
		t.Fatal("LoadAll without works/ must return nothing")
	}
}

// 符号链接的作品目录不当作作品，避免读写项目目录之外的文件。
func TestSymlinkedWorkDir(t *testing.T) {
	root := project(t)
	outside := t.TempDir()
	put(t, outside, "work.yaml", "schema_version: 1\nid: evil\ngenre: minimal\ntype: article\nstage: brief\n")
	put(t, outside, "brief.yaml", completeBrief)
	if err := os.Symlink(outside, abs(root, Dir("evil"))); err != nil {
		t.Skip("symlinks unavailable:", err)
	}
	if _, _, _, ps := Advance(root, "evil"); !slices.Equal(codes(ps), []string{"work.not-found"}) {
		t.Fatalf("advance through symlink: %+v", ps)
	}
	if works, _, _ := LoadAll(root); len(works) != 0 {
		t.Fatalf("LoadAll followed a symlink: %+v", works)
	}
	if got := string(read(t, filepath.Join(outside, "work.yaml"))); !strings.Contains(got, "stage: brief") {
		t.Fatalf("file outside the project was modified:\n%s", got)
	}
}

func TestRefOf(t *testing.T) {
	root := project(t, "saga")
	w, _ := load(root, "saga", genres(t))
	for _, u := range w.Units() {
		if got := RefOf(w.UnitDir(u)); got != u.Ref {
			t.Errorf("RefOf(%s) = %s, want %s", w.UnitDir(u), got, u.Ref)
		}
	}
}

func TestAddUnitKeepsFlowComment(t *testing.T) {
	root := project(t)
	if _, _, ps := Create(root, "minimal", "my-post"); len(ps) > 0 {
		t.Fatal(ps)
	}
	src := strings.Replace(string(read(t, abs(root, Path("my-post")))), "units: []", "units: []  # 子单元", 1)
	put(t, root, Path("my-post"), src)
	if _, _, ps := AddUnit(root, "my-post", "section", "intro", Anchor{}); len(ps) > 0 {
		t.Fatal(ps)
	}
	if got := string(read(t, abs(root, Path("my-post")))); !strings.Contains(got, "# 子单元") {
		t.Fatalf("work.yaml =\n%s", got)
	}
}

// 未决定的阻塞决策是关联单元的阻塞项；决策队列无法加载时偏安全地阻塞（workspace-artifacts/single-blocker-source）。
func TestDecisionBlockers(t *testing.T) {
	root := project(t)
	if _, _, ps := Create(root, "minimal", "my-post"); len(ps) > 0 {
		t.Fatal(ps)
	}
	put(t, root, Dir("my-post")+"/brief.yaml", completeBrief)
	put(t, root, decision.Path, "schema_version: 1\ndecisions:\n"+
		"  - {id: dec-1, question: 标题, options: [], blocking: true, units: [my-post], status: open}\n"+
		"  - {id: dec-2, question: 字数, options: [], blocking: false, units: [my-post], status: open}\n")
	w, u, _ := Resolve(root, "my-post")
	bs := w.Blockers(root, u)
	if len(bs) != 1 || bs[0].Code != "decision.blocking" || bs[0].Decision != "dec-1" || bs[0].Artifact != "decisions" || bs[0].Path != ".writing-anything/decisions.yaml" {
		t.Fatalf("blockers = %+v", bs)
	}
	if _, _, _, ps := Advance(root, "my-post"); len(ps) != 1 || ps[0].Code != "decision.blocking" {
		t.Fatalf("advance = %v", ps)
	}
	if !strings.Contains(bs[0].Message, "标题") {
		t.Errorf("message %q lacks the question", bs[0].Message)
	}
	put(t, root, decision.Path, "a: [\n")
	w, u, _ = Resolve(root, "my-post")
	if bs := w.Blockers(root, u); len(bs) != 1 || bs[0].Code != "decision.invalid-queue" || bs[0].Decision != "" {
		t.Fatalf("invalid queue blockers = %+v", bs)
	}
	// 已完成的单元没有阻塞项，即使决策队列损坏。
	put(t, root, Path("my-post"), strings.Replace(string(read(t, abs(root, Path("my-post")))), "stage: brief", "stage: done", 1))
	w, u, _ = Resolve(root, "my-post")
	if bs := w.Blockers(root, u); len(bs) != 0 {
		t.Fatalf("done unit blockers = %+v", bs)
	}
	put(t, root, Path("my-post"), strings.Replace(string(read(t, abs(root, Path("my-post")))), "stage: done", "stage: brief", 1))
	os.Remove(abs(root, decision.Path))
	if _, _, _, ps := Advance(root, "my-post"); len(ps) != 0 {
		t.Fatalf("advance without queue = %v", ps)
	}
}

// 当前阶段要求 brief 时，写作简报的问题是阻塞项，排在缺失产物之后、决策阻塞项之前（brief-gate）。
func TestBriefBlockers(t *testing.T) {
	root := project(t, "my-post")
	w, u, _ := Resolve(root, "my-post")
	if bs := w.Blockers(root, u); len(bs) != 1 || bs[0].Code != "lifecycle.missing-artifact" {
		t.Fatalf("no brief: %+v", bs)
	}
	summary := func(bs []Blocker) []string {
		var out []string
		for _, b := range bs {
			out = append(out, b.Code+" "+b.Artifact+" "+b.Path)
		}
		return out
	}
	briefPath := Dir("my-post") + "/brief.yaml"
	put(t, root, briefPath, "schema_version: 1\naudience: 读者\nprovenance:\n  audience: {sources: [note]}\nkey_messages:\n  - {id: km-1, text: 没有出处}\n  - {id: km-2, text: 待确认, decision: dec-1}\n")
	put(t, root, decision.Path, "schema_version: 1\ndecisions:\n  - {id: dec-1, question: 观点, options: [], blocking: true, units: [my-post], status: open}\n")
	w, u, _ = Resolve(root, "my-post")
	want := []string{"brief.missing-field brief " + briefPath, "brief.unsourced-message brief " + briefPath, "brief.unconfirmed-message brief " + briefPath, "decision.blocking decisions .writing-anything/decisions.yaml"}
	bs := w.Blockers(root, u)
	if got := summary(bs); !slices.Equal(got, want) {
		t.Fatalf("blockers = %v, want %v", got, want)
	}
	if !strings.Contains(bs[0].Message, "purpose") || bs[0].Decision != "" || bs[2].Decision != "" {
		t.Fatalf("blockers = %+v", bs)
	}
	if _, _, _, ps := Advance(root, "my-post"); len(ps) != 4 || ps[0].Code != "brief.missing-field" || ps[0].Path != briefPath {
		t.Fatalf("advance = %+v", ps)
	}

	put(t, root, briefPath, "k: v\n")
	w, u, _ = Resolve(root, "my-post")
	if bs := w.Blockers(root, u); bs[0].Code != "brief.invalid" || bs[0].Path != briefPath || !strings.Contains(bs[0].Message, "未定义的字段 k") {
		t.Fatalf("invalid brief: %+v", bs)
	}

	os.Remove(abs(root, briefPath))
	os.Remove(abs(root, decision.Path))
	put(t, root, Dir("my-post")+"/brief.md", "自由文本的简报\n")
	w, u, _ = Resolve(root, "my-post")
	if got := summary(w.Blockers(root, u)); !slices.Equal(got, []string{"brief.missing-field brief " + Dir("my-post") + "/brief.md", "brief.missing-field brief " + Dir("my-post") + "/brief.md"}) {
		t.Fatalf("brief.md = %v", got)
	}

	// section 没有必填项：合法的写作简报即可推进；不要求 brief 的阶段不检查写作简报。
	put(t, root, Dir("my-post")+"/units/intro/brief.yaml", "schema_version: 1\n")
	if _, u, _, ps := Advance(root, "my-post/intro"); len(ps) != 0 || u.Stage != "draft" {
		t.Fatalf("advance intro = %+v", ps)
	}
	put(t, root, Path("my-post"), strings.Replace(string(read(t, abs(root, Path("my-post")))), "stage: brief", "stage: draft", 1))
	put(t, root, Dir("my-post")+"/draft.md", "正文\n")
	if _, u, _, ps := Advance(root, "my-post"); len(ps) != 0 || u.Stage != "review" {
		t.Fatalf("advance from draft = %+v", ps)
	}
}

// 当前阶段要求 outline 时，覆盖缺口是阻塞项，排在写作简报阻塞项之后、决策阻塞项之前（outline-gate）。
func TestOutlineBlockers(t *testing.T) {
	root := project(t)
	put(t, root, Path("post"), "schema_version: 1\nid: post\ngenre: essay\ntype: essay\nstage: outline\nunits: []\n")
	g := genres(t)
	blockers := func() []Blocker {
		t.Helper()
		w, ps := load(root, "post", g)
		if w == nil {
			t.Fatal(ps)
		}
		return w.Blockers(root, w.Root)
	}
	summary := func(bs []Blocker) []string {
		var out []string
		for _, b := range bs {
			out = append(out, b.Code+" "+b.Artifact+" "+b.Path)
		}
		return out
	}
	dir := Dir("post")
	if got := summary(blockers()); !slices.Equal(got, []string{"lifecycle.missing-artifact outline " + dir}) {
		t.Fatalf("no outline: %v", got)
	}
	put(t, root, dir+"/brief.yaml", "schema_version: 1\nkey_messages:\n  - {id: km-1, text: 甲, decision: dec-1}\n  - {id: km-2, text: 乙, decision: dec-1}\n")
	put(t, root, decision.Path, "schema_version: 1\ndecisions:\n  - {id: dec-1, question: 观点, options: [], blocking: true, units: [post], status: open}\n")
	put(t, root, dir+"/outline.yaml", "schema_version: 1\nsections:\n  - {id: why, title: 为什么, covers: [km-1]}\n  - {id: bg, title: 背景}\n")
	rel := dir + "/outline.yaml"
	bs := blockers()
	if got := summary(bs); !slices.Equal(got, []string{"outline.uncovered-message outline " + rel, "outline.missing-reason outline " + rel, "decision.blocking decisions " + decision.Path}) {
		t.Fatalf("gaps: %v", got)
	}
	if !strings.Contains(bs[0].Message, "km-2") || !strings.Contains(bs[1].Message, "bg") || bs[0].Decision != "" {
		t.Fatalf("messages: %+v", bs)
	}
	if _, _, _, ps := advance(root, "post", g); len(ps) != 3 || ps[0].Code != "outline.uncovered-message" || ps[0].Path != rel {
		t.Fatalf("advance = %+v", ps)
	}

	// 大纲或写作简报不通过校验时只有一条 outline.invalid。
	put(t, root, rel, "schema_version: 1\nsections:\n  - {id: why, title: 为什么, covers: [km-9]}\n")
	if got := summary(blockers()); got[0] != "outline.invalid outline "+rel || len(got) != 2 {
		t.Fatalf("invalid outline: %v", got)
	}
	put(t, root, dir+"/brief.yaml", "k: v\n")
	if bs := blockers(); bs[0].Code != "outline.invalid" || bs[0].Path != dir+"/brief.yaml" || !strings.Contains(bs[0].Message, "brief.yaml") {
		t.Fatalf("invalid brief: %+v", bs)
	}

	// 只有 outline.md 时视为没有任何节；全部覆盖后可以推进。
	os.Remove(abs(root, decision.Path))
	put(t, root, dir+"/brief.yaml", "schema_version: 1\nkey_messages:\n  - {id: km-1, text: 甲, sources: []}\n")
	os.Remove(abs(root, rel))
	put(t, root, dir+"/outline.md", "自由文本\n")
	if got := summary(blockers()); !slices.Equal(got, []string{"outline.uncovered-message outline " + dir + "/outline.md"}) {
		t.Fatalf("outline.md: %v", got)
	}
	os.Remove(abs(root, dir+"/outline.md"))
	put(t, root, rel, "schema_version: 1\nsections:\n  - {id: why, title: 为什么, covers: [km-1]}\n")
	// 文体的默认写作法（总分总）要求的章节缺失时同样阻塞推进（method-outline-check）；写作简报选 none 时不检查。
	g2 := func(id string) (*catalog.Genre, []workspace.Problem, bool) {
		gen, ps, ok := g(id)
		if gen != nil {
			c := *gen
			c.DefaultMethod = "general-specific-general"
			gen = &c
		}
		return gen, ps, ok
	}
	if _, _, _, ps := advance(root, "post", g2); len(ps) != 3 || ps[0].Code != "outline.missing-method-part" || ps[0].Path != rel {
		t.Fatalf("default method: %+v", ps)
	}
	// 选用的写作法不存在时，大纲关卡为一条 outline.invalid，path 为 brief.yaml。
	put(t, root, dir+"/brief.yaml", "schema_version: 1\nkey_messages:\n  - {id: km-1, text: 甲, sources: []}\nmethod: gone\n")
	if _, _, _, ps := advance(root, "post", g2); len(ps) != 1 || ps[0].Code != "outline.invalid" || ps[0].Path != dir+"/brief.yaml" || !strings.Contains(ps[0].Message, "gone") {
		t.Fatalf("dangling method: %+v", ps)
	}
	put(t, root, dir+"/brief.yaml", "schema_version: 1\nkey_messages:\n  - {id: km-1, text: 甲, sources: []}\nmethod: none\n")
	if _, u, _, ps := advance(root, "post", g2); len(ps) != 0 || u.Stage != "draft" {
		t.Fatalf("covered: %+v", ps)
	}
}

// completeBrief 是 minimal 文体中填齐必填项（audience 与 purpose）的写作简报。
const completeBrief = "schema_version: 1\naudience: 后端工程师\npurpose: 讲清楚为什么要做连接池\nprovenance:\n  audience: {sources: [note]}\n  purpose: {sources: [note]}\n"

// 汇总单元（Aggregated Unit）：部与章的推导阶段取子场景中最落后者（最短板），空章为第一个阶段，
// 全部 done 时为 done；推进汇总单元被拒绝且不修改文件。
func TestAggregatedStage(t *testing.T) {
	root := project(t, "saga")
	g := genres(t)
	w, ps := load(root, "saga", g)
	if len(ps) > 0 {
		t.Fatalf("problems = %+v", ps)
	}
	ch1, part1, ch2 := w.find("ch-1"), w.find("part-1"), w.find("ch-2")
	if !w.Aggregated(ch1) || !w.Aggregated(part1) || !w.Aggregated(ch2) || w.Aggregated(w.Root) || w.Aggregated(w.find("s1")) {
		t.Fatalf("aggregated flags: root=%v part-1=%v ch-1=%v ch-2=%v s1=%v",
			w.Aggregated(w.Root), w.Aggregated(part1), w.Aggregated(ch1), w.Aggregated(ch2), w.Aggregated(w.find("s1")))
	}
	// 最短板：s1 还在 plan，ch-1 与 part-1 的推导阶段都是 plan，虽然 s2 已到 draft。
	if w.DerivedStage(ch1) != "plan" || w.EffectiveStage(ch1) != "plan" || w.DerivedStage(part1) != "plan" {
		t.Fatalf("derived stages: ch-1=%s part-1=%s", w.DerivedStage(ch1), w.DerivedStage(part1))
	}
	// ch-2 与 part-2 没有任何场景：推导为文体的第一个阶段。
	if w.DerivedStage(ch2) != "plan" || w.DerivedStage(w.find("part-2")) != "plan" {
		t.Fatalf("empty derived stages: ch-2=%s part-2=%s", w.DerivedStage(ch2), w.DerivedStage(w.find("part-2")))
	}
	if done, total := w.Progress(ch1); done != 0 || total != 2 {
		t.Fatalf("ch-1 progress = %d/%d, want 0/2", done, total)
	}
	if done, total := w.Progress(ch2); done != 0 || total != 0 {
		t.Fatalf("ch-2 progress = %d/%d, want 0/0", done, total)
	}
	// 全部场景 done 时，章与部的推导阶段为 done。
	src := strings.Replace(string(read(t, filepath.Join("testdata", "works", "saga", FileName))),
		"{id: s1, type: scene, stage: plan}", "{id: s1, type: scene, stage: done}", 1)
	src = strings.Replace(src, "{id: s2, type: scene, stage: draft}", "{id: s2, type: scene, stage: done}", 1)
	put(t, root, Path("saga"), src)
	w, ps = load(root, "saga", g)
	if len(ps) > 0 {
		t.Fatalf("problems after edit = %+v", ps)
	}
	if w.DerivedStage(w.find("ch-1")) != "done" || w.DerivedStage(w.find("part-1")) != "done" {
		t.Fatalf("all done: ch-1=%s part-1=%s", w.DerivedStage(w.find("ch-1")), w.DerivedStage(w.find("part-1")))
	}
}

func TestAdvanceAggregatedRejected(t *testing.T) {
	root := project(t, "saga")
	g := genres(t)
	before := string(read(t, abs(root, Path("saga"))))
	w, u, prev, ps := advance(root, "saga/ch-1", g)
	if w != nil || u != nil || prev != "" || !slices.Equal(codes(ps), []string{"lifecycle.derived-stage"}) ||
		ps[0].Path != Path("saga") || !strings.Contains(ps[0].Message, "汇总单元") {
		t.Fatalf("advance aggregated: problems = %+v", ps)
	}
	if after := string(read(t, abs(root, Path("saga")))); after != before {
		t.Fatalf("work.yaml changed after rejected advance")
	}
}

// 汇总阻塞项：未完成子单元的阻塞项按出现顺序去重后的并集；已完成的不计入。
func TestAggregatedBlockers(t *testing.T) {
	root := project(t, "saga")
	g := genres(t)
	w, _ := load(root, "saga", g)
	bs := w.AggregatedBlockers(root, w.find("part-1"))
	if len(bs) != 3 {
		t.Fatalf("aggregated blockers = %+v", bs)
	}
	want := []string{".writing-anything/works/saga/units/s1", ".writing-anything/works/saga/units/s2", ".writing-anything/works/saga/units/s2"}
	for i, b := range bs {
		if b.Code != "lifecycle.missing-artifact" || b.Path != want[i] {
			t.Fatalf("blocker %d = %+v, want path %s", i, b, want[i])
		}
	}
	// s2 已在 draft 阶段：只有 s1 的缺失产物计入。
	put(t, root, ".writing-anything/works/saga/units/s2/draft.md", "草稿\n")
	put(t, root, ".writing-anything/works/saga/units/s2/notes.md", "笔记\n")
	w, _ = load(root, "saga", g)
	if bs = w.AggregatedBlockers(root, w.find("ch-1")); len(bs) != 1 || bs[0].Path != ".writing-anything/works/saga/units/s1" {
		t.Fatalf("after s2 artifacts: blockers = %+v", bs)
	}
}

// unit add 按锚点插入：--before/--after 指定位置，锚点在别处或不存在时拒绝且不修改文件。
func TestAddUnitAnchor(t *testing.T) {
	root := project(t, "saga")
	g := genres(t)
	w, u, ps := addUnit(root, "saga/ch-1", "scene", "s0", Anchor{Sibling: "s1"}, g)
	if len(ps) > 0 || u.Ref != "saga/s0" || u.Stage != "plan" {
		t.Fatalf("insert before s1: unit = %+v, problems = %+v", u, ps)
	}
	if got := ids(w.find("ch-1").Units); !slices.Equal(got, []string{"s0", "s1", "s2"}) {
		t.Fatalf("after insert before: %v", got)
	}
	if _, _, ps := addUnit(root, "saga/ch-1", "scene", "s3", Anchor{Sibling: "s2", After: true}, g); len(ps) > 0 {
		t.Fatalf("insert after s2: %+v", ps)
	}
	w, _ = load(root, "saga", g)
	if got := ids(w.find("ch-1").Units); !slices.Equal(got, []string{"s0", "s1", "s2", "s3"}) {
		t.Fatalf("after insert after: %v", got)
	}
	before := string(read(t, abs(root, Path("saga"))))
	// 锚点单元在别的父单元下。
	if _, _, ps := addUnit(root, "saga/ch-1", "scene", "sx", Anchor{Sibling: "part-2"}, g); !slices.Equal(codes(ps), []string{"unit.not-sibling"}) ||
		!strings.Contains(ps[0].Message, "part-2") || !strings.Contains(ps[0].Message, "saga/ch-1") || ps[0].Path != "" {
		t.Fatalf("anchor elsewhere: %+v", ps)
	}
	// 锚点单元不存在。
	if _, _, ps := addUnit(root, "saga/ch-1", "scene", "sx", Anchor{Sibling: "nope"}, g); !slices.Equal(codes(ps), []string{"unit.not-found"}) {
		t.Fatalf("anchor missing: %+v", ps)
	}
	if after := string(read(t, abs(root, Path("saga")))); after != before {
		t.Fatalf("work.yaml changed after rejected inserts")
	}
}

// unit move：跨父移动子树、同父重排序；防环、根单元、类型不被允许与锚点问题都拒绝且不修改文件；
// 单元 id 与单元目录不变，注释保留。
func TestMoveUnit(t *testing.T) {
	root := project(t, "saga")
	g := genres(t)
	// 先为场景建好单元目录：单元目录按 id 寻址，移动不应触碰它们。
	for _, id := range []string{"s1", "s2"} {
		if err := os.MkdirAll(abs(root, ".writing-anything/works/saga/units/"+id), 0o755); err != nil {
			t.Fatal(err)
		}
	}
	// 跨父移动到末尾：s2 从 ch-1 移到 ch-2。
	w, u, ps := moveUnit(root, "saga/s2", "saga/ch-2", Anchor{}, g)
	if len(ps) > 0 || u.Ref != "saga/s2" || u.Parent.ID != "ch-2" || u.ID != "s2" {
		t.Fatalf("move s2 to ch-2: unit = %+v, problems = %+v", u, ps)
	}
	if got := ids(w.find("ch-1").Units); !slices.Equal(got, []string{"s1"}) {
		t.Fatalf("ch-1 after move: %v", got)
	}
	if got := ids(w.find("ch-2").Units); !slices.Equal(got, []string{"s2"}) {
		t.Fatalf("ch-2 after move: %v", got)
	}
	if !isDir(root, ".writing-anything/works/saga/units/s2") {
		t.Fatalf("unit directory must not move")
	}
	if isDir(root, ".writing-anything/works/saga/units/ch-2/units/s2") {
		t.Fatalf("unit directory must not be nested under the new parent")
	}
	if src := string(read(t, abs(root, Path("saga")))); !strings.Contains(src, "# 多层单元树") {
		t.Fatalf("head comment lost:\n%s", src)
	}
	// 同父重排序：ch-2 内把 s2 移到开头（无锚点时移到末尾，这里用 --before s2）。
	if _, _, ps := moveUnit(root, "saga/s1", "saga/ch-2", Anchor{Sibling: "s2"}, g); len(ps) > 0 {
		t.Fatalf("reorder: %+v", ps)
	}
	w, _ = load(root, "saga", g)
	if got := ids(w.find("ch-2").Units); !slices.Equal(got, []string{"s1", "s2"}) {
		t.Fatalf("ch-2 after reorder: %v", got)
	}
	before := string(read(t, abs(root, Path("saga"))))
	// 移动到自身子孙之下。
	if _, _, ps := moveUnit(root, "saga/part-1", "saga/ch-1", Anchor{}, g); !slices.Equal(codes(ps), []string{"unit.move-under-self"}) || ps[0].Path != "" {
		t.Fatalf("move under self: %+v", ps)
	}
	// 移动根单元。
	if _, _, ps := moveUnit(root, "saga", "saga/part-1", Anchor{}, g); !slices.Equal(codes(ps), []string{"unit.move-root"}) {
		t.Fatalf("move root: %+v", ps)
	}
	// 类型不被新父单元允许：chapter 不能作为 book 的子单元。
	if _, _, ps := moveUnit(root, "saga/ch-1", "saga", Anchor{}, g); !slices.Equal(codes(ps), []string{"work.child-type-not-allowed"}) ||
		!strings.Contains(ps[0].Message, "part") {
		t.Fatalf("disallowed type: %+v", ps)
	}
	// 锚点不是新父单元的直接子单元。
	if _, _, ps := moveUnit(root, "saga/s2", "saga/ch-2", Anchor{Sibling: "part-2"}, g); !slices.Equal(codes(ps), []string{"unit.not-sibling"}) {
		t.Fatalf("anchor elsewhere: %+v", ps)
	}
	// 被移动单元引用不存在。
	if _, _, ps := moveUnit(root, "saga/nope", "saga/ch-2", Anchor{}, g); !slices.Equal(codes(ps), []string{"unit.not-found"}) {
		t.Fatalf("unit missing: %+v", ps)
	}
	// 父单元引用不存在。
	if _, _, ps := moveUnit(root, "saga/s2", "saga/nope", Anchor{}, g); !slices.Equal(codes(ps), []string{"unit.not-found"}) {
		t.Fatalf("parent missing: %+v", ps)
	}
	// 跨作品移动。
	if _, _, ps := moveUnit(root, "saga/s2", "other/ch-1", Anchor{}, g); !slices.Equal(codes(ps), []string{"input.invalid-value"}) || ps[0].Field != "parent" {
		t.Fatalf("cross work: %+v", ps)
	}
	if after := string(read(t, abs(root, Path("saga")))); after != before {
		t.Fatalf("work.yaml changed after rejected moves")
	}
}

func ids(us []*Unit) []string {
	var out []string
	for _, u := range us {
		out = append(out, u.ID)
	}
	return out
}


// 递归文体下的移动回归：把单元移到「同级后面单元的子孙」之下时，摘除子树会使后面的单元左移；
// 目标序列必须按解析时的位置先取到，否则会挂错父单元或误报锚点值（审查发现项 1 的回归）。
func TestMoveUnderLaterSibling(t *testing.T) {
	root := project(t)
	put(t, root, Path("saga2"), `schema_version: 1
id: saga2
genre: nested
type: nested
stage: plan
units:
  - id: sec-a
    type: sec
    stage: plan
  - id: sec-b
    type: sec
    stage: plan
  - id: sec-c
    type: sec
    stage: plan
    units:
      - id: sec-c-1
        type: sec
        stage: plan
        units:
          - {id: scene-t, type: scene, stage: plan}
`)
	g := genres(t)
	w, u, ps := moveUnit(root, "saga2/sec-a", "saga2/sec-c-1", Anchor{}, g)
	if len(ps) > 0 || u == nil || u.Parent == nil || u.Parent.ID != "sec-c-1" {
		t.Fatalf("move sec-a under sec-c-1: unit = %+v, problems = %+v", u, ps)
	}
	if got := ids(w.find("sec-c").Units); !slices.Equal(got, []string{"sec-c-1"}) {
		t.Fatalf("sec-c children = %v", got)
	}
	if got := ids(w.find("sec-c-1").Units); !slices.Equal(got, []string{"scene-t", "sec-a"}) {
		t.Fatalf("sec-c-1 children = %v, want [scene-t sec-a]（无锚点时追加到末尾）", got)
	}
	if got := ids(w.Root.Units); !slices.Equal(got, []string{"sec-b", "sec-c"}) {
		t.Fatalf("root children = %v, want [sec-b sec-c]", got)
	}
}

// 混合层级的推导阶段：非汇总中间层（chapter）自身走生命周期，其子场景也参与最短板；
// 汇总的 part 取所有非汇总子孙中最落后者（审查发现项 3 的回归）。
func TestDerivedStageMixedLevels(t *testing.T) {
	root := project(t)
	put(t, root, Path("mix"), `schema_version: 1
id: mix
genre: nested
type: nested
stage: plan
units:
  - id: part-1
    type: part
    stage: plan
    units:
      - id: ch-1
        type: chapter
        stage: draft
        units:
          - {id: s1, type: scene, stage: plan}
          - {id: s2, type: scene, stage: done}
`)
	g := genres(t)
	w, ps := load(root, "mix", g)
	if len(ps) > 0 {
		t.Fatalf("problems = %+v", ps)
	}
	part := w.find("part-1")
	// 最落后者是 s1（plan），不是已推进到 draft 的非汇总章。
	if w.DerivedStage(part) != "plan" {
		t.Fatalf("derived stage = %s, want plan (weakest scene s1)", w.DerivedStage(part))
	}
	if done, total := w.Progress(part); done != 1 || total != 3 {
		t.Fatalf("progress = %d/%d, want 1/3 (ch-1 与两个场景都计入)", done, total)
	}
}

// 移动被 YAML 锚点沾到的子树时拒绝且不写文件（unit move 的 work.anchored-value）。
func TestMoveAnchored(t *testing.T) {
	root := project(t, "saga")
	g := genres(t)
	put(t, root, Path("saga"), `schema_version: 1
id: saga
genre: book
type: book
stage: plan
units:
  - id: &part part-1
    type: part
    stage: plan
    units:
      - id: ch-1
        type: chapter
        stage: plan
        units:
          - {id: s1, type: scene, stage: plan}
`)
	before := string(read(t, abs(root, Path("saga"))))
	if _, _, ps := moveUnit(root, "saga/part-1", "saga", Anchor{}, g); !slices.Equal(codes(ps), []string{"work.anchored-value"}) {
		t.Fatalf("anchored subtree move: %+v", ps)
	}
	if _, _, ps := moveUnit(root, "saga/s1", "saga/ch-1", Anchor{Sibling: "s1"}, g); !slices.Equal(codes(ps), []string{"unit.not-sibling"}) {
		t.Fatalf("anchor is moved unit: %+v", ps)
	}
	if after := string(read(t, abs(root, Path("saga")))); after != before {
		t.Fatalf("work.yaml changed after rejected moves")
	}
}

// 移动到没有 units 键的父单元之下时创建序列；存储阶段为 done 的汇总单元仍拒绝推进。
func TestMoveIntoBareParentAndAdvanceDone(t *testing.T) {
	root := project(t)
	put(t, root, Path("bare"), `schema_version: 1
id: bare
genre: book
type: book
stage: plan
units:
  - id: part-1
    type: part
    stage: done
    units:
      - id: ch-1
        type: chapter
        stage: done
        units:
          - {id: s1, type: scene, stage: done}
  - id: part-2
    type: part
    stage: plan
`)
	g := genres(t)
	// part-2 没有 units 键：把 ch-1 移过去时创建序列。
	w, u, ps := moveUnit(root, "bare/ch-1", "bare/part-2", Anchor{}, g)
	if len(ps) > 0 || u.Parent.ID != "part-2" {
		t.Fatalf("move into bare parent: unit = %+v, problems = %+v", u, ps)
	}
	if got := ids(w.find("part-2").Units); !slices.Equal(got, []string{"ch-1"}) {
		t.Fatalf("part-2 children = %v", got)
	}
	// 存储阶段为 done 的汇总单元仍报告 lifecycle.derived-stage（先于 already-done 检查）。
	if _, _, _, ps := advance(root, "bare/part-1", g); !slices.Equal(codes(ps), []string{"lifecycle.derived-stage"}) {
		t.Fatalf("advance aggregated with stored done: %+v", ps)
	}
}

// reopen-stage：只有验收失效或没有验收记录的 done 单元可以退回最后一个需要验收的阶段，只改 stage。
func TestReopen(t *testing.T) {
	root := project(t, "my-post")
	g := genres(t)
	body := ".writing-anything/works/my-post/units/body/"
	accepted := "schema_version: 1\ngate_level: standard\ndraft_sha256: %s\nconfirmed_by_author: false\naccepted_at: \"2026-10-01T00:00:00Z\"\n"
	put(t, root, body+"review.md", "ok\n")
	put(t, root, body+"draft.md", "正文\n")
	put(t, root, body+"acceptance.yaml", fmt.Sprintf(accepted, review.SHA256([]byte("正文\n"))))
	if _, _, _, ps := Reopen(root, "my-post/body"); !slices.Equal(codes(ps), []string{"lifecycle.not-done"}) || !strings.Contains(ps[0].Message, "review") {
		t.Fatalf("not done: %+v", ps)
	}
	if _, u, _, ps := Advance(root, "my-post/body"); len(ps) > 0 || u.Stage != "done" {
		t.Fatalf("to done: %+v", ps)
	}
	before := string(read(t, abs(root, Path("my-post"))))
	w, _, _ := resolve(root, "my-post/body", g)
	if bs := w.Blockers(root, w.find("body")); len(bs) != 0 {
		t.Fatalf("valid acceptance blockers = %+v", bs)
	}
	if _, _, _, ps := Reopen(root, "my-post/body"); !slices.Equal(codes(ps), []string{"lifecycle.acceptance-current"}) || ps[0].Path != body+"acceptance.yaml" {
		t.Fatalf("acceptance current: %+v", ps)
	}
	if string(read(t, abs(root, Path("my-post")))) != before {
		t.Fatal("work.yaml changed although reopen was refused")
	}
	// 正文改过：done 单元的阻塞项为 acceptance.stale，reopen 退回 review 且只改该单元的 stage。
	put(t, root, body+"draft.md", "改过的正文\n")
	w, _, _ = resolve(root, "my-post/body", g)
	if bs := w.Blockers(root, w.find("body")); len(bs) != 1 || bs[0].Code != "acceptance.stale" || bs[0].Artifact != "acceptance" || bs[0].Path != body+"acceptance.yaml" {
		t.Fatalf("stale blockers = %+v", bs)
	}
	w, u, prev, ps := Reopen(root, "my-post/body")
	if len(ps) > 0 || prev != "done" || u.Stage != "review" || u.Ref != "my-post/body" || w.Path != Path("my-post") {
		t.Fatalf("reopen: %+v %s %+v", u, prev, ps)
	}
	want := strings.Replace(before, "  - id: body\n    type: section\n    stage: done", "  - id: body\n    type: section\n    stage: review", 1)
	if got := string(read(t, abs(root, Path("my-post")))); got != want || got == before {
		t.Fatalf("work.yaml =\n%s\nwant only body's stage changed:\n%s", got, want)
	}
	// 没有验收记录的 done 单元不是阻塞项，但可以退回。
	put(t, root, Path("my-post"), strings.Replace(before, "stage: brief # 作者的行尾注释", "stage: done # 作者的行尾注释", 1))
	w, _, _ = resolve(root, "my-post", g)
	if bs := w.Blockers(root, w.Root); len(bs) != 0 {
		t.Fatalf("done without acceptance blockers = %+v", bs)
	}
	if _, u, prev, ps := Reopen(root, "my-post"); len(ps) > 0 || prev != "done" || u.Stage != "review" {
		t.Fatalf("reopen without acceptance: %+v %+v", u, ps)
	}
	if _, _, _, ps := Reopen(root, "my-post/nope"); !slices.Equal(codes(ps), []string{"unit.not-found"}) {
		t.Fatalf("unknown unit: %+v", ps)
	}
}

func TestReopenRejected(t *testing.T) {
	g := genres(t)
	// 汇总单元的阶段由子单元汇总，不能退回。
	root := project(t, "saga")
	if _, _, _, ps := reopen(root, "saga/ch-2", g); !slices.Equal(codes(ps), []string{"lifecycle.derived-stage"}) || !strings.Contains(ps[0].Message, "不能退回") {
		t.Fatalf("aggregated: %+v", ps)
	}
	// 文体没有需要验收的阶段：done 单元没有阻塞项，reopen 报告 lifecycle.no-acceptance-stage。
	put(t, root, Path("note"), "schema_version: 1\nid: note\ngenre: essay\ntype: essay\nstage: done\n")
	w, _, _ := resolve(root, "note", g)
	if bs := w.Blockers(root, w.Root); len(bs) != 0 {
		t.Fatalf("essay done blockers = %+v", bs)
	}
	if _, _, _, ps := reopen(root, "note", g); !slices.Equal(codes(ps), []string{"lifecycle.no-acceptance-stage"}) {
		t.Fatalf("no acceptance stage: %+v", ps)
	}
	// stage 经由锚点得到时不修改（structured-edits）。
	src := "schema_version: 1\nid: my-post\ngenre: minimal\ntype: article\nstage: &s done\nunits:\n  - {id: a, type: section, stage: *s}\n"
	put(t, root, Path("my-post"), src)
	if _, _, _, ps := Reopen(root, "my-post"); !slices.Equal(codes(ps), []string{"work.anchored-value"}) {
		t.Fatalf("anchored: %+v", ps)
	}
	if got := string(read(t, abs(root, Path("my-post")))); got != src {
		t.Fatalf("work.yaml changed:\n%s", got)
	}
}

// done 单元验收失效的三种其余情况：关卡强度变严、只有手写的 acceptance.md、acceptance.yaml 不通过校验；
// 都是阻塞项、都可以退回（reopen-stage）。
func TestReopenInvalidAcceptance(t *testing.T) {
	g := genres(t)
	body := ".writing-anything/works/my-post/units/body/"
	fast := "schema_version: 1\ngate_level: fast\ndraft_sha256: " + review.SHA256([]byte("正文\n")) + "\nconfirmed_by_author: false\naccepted_at: \"2026-10-01T00:00:00Z\"\n"
	for name, tc := range map[string]struct {
		files map[string]string
		code  string
	}{
		"weaker-gate": {map[string]string{"acceptance.yaml": fast}, "acceptance.weaker-gate"},
		"markdown":    {map[string]string{"acceptance.md": "ok\n"}, "acceptance.invalid"},
		"invalid":     {map[string]string{"acceptance.yaml": "schema_version: 1\n"}, "acceptance.invalid"},
	} {
		t.Run(name, func(t *testing.T) {
			root := project(t, "my-post")
			put(t, root, Path("my-post"), strings.Replace(string(read(t, filepath.Join("testdata", "works", "my-post", FileName))), "    stage: review", "    stage: done", 1))
			put(t, root, body+"draft.md", "正文\n")
			for f, c := range tc.files {
				put(t, root, body+f, c)
			}
			w, _, _ := resolve(root, "my-post/body", g)
			u := w.find("body")
			if bs := w.Blockers(root, u); len(bs) != 1 || bs[0].Code != tc.code || bs[0].Artifact != "acceptance" || !w.Reopenable(root, u) {
				t.Fatalf("blockers = %+v, reopenable = %v", bs, w.Reopenable(root, u))
			}
			if _, u, prev, ps := Reopen(root, "my-post/body"); len(ps) > 0 || prev != "done" || u.Stage != "review" {
				t.Fatalf("reopen: %+v %+v", u, ps)
			}
		})
	}
}

func TestReopenable(t *testing.T) {
	g := genres(t)
	root := project(t, "my-post", "saga")
	w, _, _ := resolve(root, "my-post", g)
	// 未完成的单元与没有验收记录的 done 单元。
	if w.Reopenable(root, w.find("body")) {
		t.Error("review-stage unit is reopenable")
	}
	put(t, root, Path("my-post"), strings.Replace(string(read(t, abs(root, Path("my-post")))), "    stage: review", "    stage: done", 1))
	w, _, _ = resolve(root, "my-post", g)
	if !w.Reopenable(root, w.find("body")) {
		t.Error("done unit without acceptance is not reopenable")
	}
	s, _, _ := resolve(root, "saga", g)
	if s.Reopenable(root, s.find("ch-2")) {
		t.Error("aggregated unit is reopenable")
	}
}
