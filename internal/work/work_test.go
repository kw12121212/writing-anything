package work

import (
	"os"
	"path/filepath"
	"slices"
	"strings"
	"testing"

	"github.com/kw12121212/writing-anything/internal/catalog"
	"github.com/kw12121212/writing-anything/internal/decision"
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

// genres 在内置文体之外提供 testdata/genres/book.yaml 中的测试文体 book。
func genres(t *testing.T) genreLookup {
	t.Helper()
	doc, p := schema.Parse(read(t, filepath.Join("testdata", "genres", "book.yaml")))
	if p != nil {
		t.Fatal(p)
	}
	// 测试文体同样要通过文体的 Schema，夹具写错时及早失败。
	if ps := schema.Named("genre").ValidateNode(doc); len(ps) > 0 {
		t.Fatalf("testdata genre book: %+v", ps)
	}
	var f struct {
		ID        string             `json:"id"`
		UnitTypes []catalog.UnitType `json:"unit_types"`
		Stages    []struct {
			ID                string   `json:"id"`
			RequiredArtifacts []string `json:"required_artifacts"`
		} `json:"stages"`
	}
	catalog.Decode(doc, &f)
	book := &catalog.Genre{ID: f.ID, UnitTypes: f.UnitTypes}
	for _, s := range f.Stages {
		book.Stages = append(book.Stages, catalog.Stage{ID: s.ID, RequiredArtifacts: s.RequiredArtifacts})
	}
	return func(id string) (*catalog.Genre, []workspace.Problem, bool) {
		if id == "book" {
			return book, nil, true
		}
		return catalog.LoadGenre(id)
	}
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
	if _, u, ps := addUnit(root, "saga/ch-2", "scene", "s3", g); len(ps) > 0 || u.Ref != "saga/s3" || u.Parent.ID != "ch-2" || u.Stage != "plan" {
		t.Fatalf("add s3: unit = %+v, problems = %+v", u, ps)
	}
	if _, _, ps := addUnit(root, "saga/part-2", "scene", "s4", g); !slices.Equal(codes(ps), []string{"work.child-type-not-allowed"}) || !strings.Contains(ps[0].Message, "chapter") {
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
	put(t, root, ".writing-anything/works/my-post/brief.yaml", "k: v\n")
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
			put(t, root, unitDir+"/brief.yaml", "k: v\n")
			var ps []workspace.Problem
			if tc.op == "advance" {
				_, _, _, ps = Advance(root, tc.ref)
			} else {
				_, _, ps = AddUnit(root, tc.ref, "section", "new")
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
	w, u, ps := AddUnit(root, "my-post", "section", "intro")
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
		if _, _, ps := AddUnit(root, tc.parent, tc.typ, tc.id); !slices.Equal(codes(ps), []string{tc.code}) {
			t.Errorf("AddUnit(%s, %s, %s) = %+v, want %s", tc.parent, tc.typ, tc.id, ps, tc.code)
		}
	}
	if got := string(read(t, abs(root, Path("my-post")))); got != src {
		t.Fatalf("refused additions changed work.yaml:\n%s", got)
	}
	// 父单元没有 units 字段时新建该字段。
	put(t, root, Path("bare"), "schema_version: 1\nid: bare\ngenre: minimal\ntype: article\nstage: brief\n")
	if _, u, ps := AddUnit(root, "bare", "section", "one"); len(ps) > 0 || u.Ref != "bare/one" {
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
	put(t, outside, "brief.yaml", "k: v\n")
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
	if _, _, ps := AddUnit(root, "my-post", "section", "intro"); len(ps) > 0 {
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
	put(t, root, Dir("my-post")+"/brief.yaml", "k: v\n")
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
