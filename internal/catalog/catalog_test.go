package catalog

import (
	"math"
	"os"
	"path/filepath"
	"slices"
	"strings"
	"testing"

	"github.com/kw12121212/writing-anything/internal/schema"
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
	if len(BuiltinMethods()) != 17 {
		t.Errorf("builtin methods = %v", BuiltinMethods())
	}
	for _, id := range BuiltinMethods() {
		m, ps, ok := LoadMethod(id)
		hasParts := m != nil && (m.Kind == MethodArticle || m.Kind == MethodStructure)
		if !ok || m == nil || len(ps) > 0 || (hasParts && len(m.Parts()) == 0) || (!hasParts && len(m.Steps) == 0) {
			t.Errorf("method %s: ok = %v, method = %+v, problems = %+v", id, ok, m, ps)
		}
		if m.Kind == MethodStructure {
			for _, b := range m.Beats {
				if b.Position == nil || b.Tension == nil || *b.Tension < 0 || *b.Tension > 10 || *b.Tension != math.Trunc(*b.Tension) {
					t.Errorf("structure method %s beat %s: position = %+v, tension = %+v", id, b.ID, b.Position, b.Tension)
				}
			}
		}
		if m.Kind == MethodPlanning {
			for _, s := range m.Steps {
				switch s.Product {
				case "", "characters", "scenes", "scene-contracts":
				default:
					t.Errorf("planning method %s step %s: product = %q", id, s.ID, s.Product)
				}
			}
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

// short-story 文体：以场景为单元，论断出处检查对虚构内容关闭（不声明论断类规则），
// 评分标准覆盖冲突、节奏与人物动机（genres-and-packs 主规格的 short-story-genre）。
func TestShortStoryGenre(t *testing.T) {
	g, ps, ok := LoadGenre("short-story")
	if !ok || g == nil || len(ps) > 0 {
		t.Fatalf("LoadGenre(short-story) = %+v, %+v, %v", g, ps, ok)
	}
	if u := g.UnitTypes[0]; u.ID != "story" || !slices.Equal(u.Children, []string{"scene"}) ||
		!slices.Equal(u.BriefRequired, []string{"premise", "pov", "length", "key_messages"}) {
		t.Errorf("short-story story = %#v", u)
	}
	if u := g.UnitTypes[1]; u.ID != "scene" || len(u.BriefRequired) != 0 || u.Children == nil {
		t.Errorf("short-story scene = %#v", u)
	}
	if len(g.Stages) != 4 {
		t.Fatalf("short-story stages = %+v", g.Stages)
	}
	for _, s := range g.Stages {
		if s.ID == "outline" && !slices.Equal(s.RequiredArtifacts, []string{"outline"}) {
			t.Errorf("short-story outline stage = %+v", s)
		}
	}
	for _, id := range []string{"claims", "claim-candidates"} {
		if _, has := g.Rules[id]; has {
			t.Errorf("short-story declares fiction-inapplicable rule %s", id)
		}
	}
	if r := g.Rules["length"]; r.Type != "length-range" || r.Severity != "warning" {
		t.Errorf("short-story length rule = %+v", r)
	}
	var rubric []string
	for _, r := range g.Rubric {
		rubric = append(rubric, r.ID)
	}
	if !slices.Equal(rubric, []string{"conflict", "pacing", "motivation"}) {
		t.Errorf("short-story rubric = %v", rubric)
	}
	if g.ContextBudget != 16000 || g.DefaultMethod != "three-act" {
		t.Errorf("short-story budget = %d, default method = %q", g.ContextBudget, g.DefaultMethod)
	}
}

// 五个内置结构类写作法（methods 主规格的 method-data）：three-act 节拍位置与 beat-positions 场景一致，
// save-the-cat 为标准 15 节拍，起承转合与英雄之旅节拍齐全（structure-builtin-methods）。
func TestBuiltinStructureMethods(t *testing.T) {
	for _, id := range []string{"three-act", "save-the-cat", "seven-point", "qichengzhuanhe", "hero-journey"} {
		m, ps, ok := LoadMethod(id)
		if !ok || m == nil || len(ps) > 0 || m.Kind != MethodStructure {
			t.Fatalf("method %s: ok = %v, kind = %q, problems = %+v", id, ok, m.Kind, ps)
		}
		if !slices.Contains(BuiltinMethods(), id) {
			t.Errorf("builtin methods miss %s", id)
		}
	}
	act, _, _ := LoadMethod("three-act")
	var beatIDs []string
	for _, b := range act.Beats {
		beatIDs = append(beatIDs, b.ID)
	}
	if !slices.Equal(beatIDs, []string{"setup", "confrontation", "climax", "resolution"}) {
		t.Errorf("three-act beats = %v", beatIDs)
	}
	if s := act.Beats[0].Position; s.Min != 0 || s.Max != 0.3 {
		t.Errorf("three-act setup position = %+v", s)
	}
	if c := act.Beats[2].Position; c.Min != 0.7 || c.Max != 1 {
		t.Errorf("three-act climax position = %+v", c)
	}
	cat, _, _ := LoadMethod("save-the-cat")
	if len(cat.Beats) != 15 {
		t.Errorf("save-the-cat beats = %d, want 15", len(cat.Beats))
	}
	qi, _, _ := LoadMethod("qichengzhuanhe")
	if len(qi.Beats) != 4 || qi.Beats[0].ID != "qi" || qi.Beats[3].ID != "he" {
		t.Errorf("qichengzhuanhe beats = %+v", qi.Beats)
	}
	hj, _, _ := LoadMethod("hero-journey")
	if len(hj.Beats) != 12 || hj.Beats[0].ID != "ordinary-world" || hj.Beats[11].ID != "return-with-elixir" {
		t.Errorf("hero-journey beats = %+v", hj.Beats)
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

// 规范包结构类写作法的目标张力（Target Tension）：越界与非整数报告 method.invalid-tension（field 指向 beats[i].tension），省略合法。
func TestPackMethodTension(t *testing.T) {
	twelve, minus, half := 12.0, -1.0, 3.5
	p := &Pack{ID: "house", Origin: OriginWorkspace, Path: ".writing-anything/packs/house.yaml", Methods: []Method{
		{ID: "m", Kind: MethodStructure, Beats: []MethodPart{
			{ID: "a", Position: &Position{Min: 0, Max: 1}, Tension: &twelve},
			{ID: "b", Position: &Position{Min: 0, Max: 1}, Tension: &minus},
			{ID: "c", Position: &Position{Min: 0, Max: 1}, Tension: &half},
			{ID: "d", Position: &Position{Min: 0, Max: 1}},
		}},
	}}
	ps := checkPack(p)
	want := []string{"method.invalid-tension", "method.invalid-tension", "method.invalid-tension"}
	if !slices.Equal(codes(ps), want) {
		t.Fatalf("codes = %v, want %v", codes(ps), want)
	}
	wantFields := []string{"methods[0].beats[0].tension", "methods[0].beats[1].tension", "methods[0].beats[2].tension"}
	for i, p := range ps {
		if p.Field != wantFields[i] {
			t.Errorf("problem[%d].field = %q, want %q", i, p.Field, wantFields[i])
		}
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

func TestGenreSummaryLength(t *testing.T) {
	doc, _ := schema.Parse([]byte("schema_version: 1\nid: g\ntitle: g\nunit_types: [{id: article, title: 文章}]\nstages: [{id: draft, title: 草稿, required_artifacts: [draft]}]\nsummary_length: {min: 50, max: 300}\n"))
	if ps := schema.Named("genre").ValidateNode(doc); len(ps) > 0 {
		t.Fatal(ps)
	}
	if g := genreOf(doc); g.SummaryLength != (Range{Min: 50, Max: 300}) {
		t.Fatalf("declared = %+v", g.SummaryLength)
	}
	if g, _, _ := LoadGenre("minimal"); g.SummaryLength != DefaultSummaryLength {
		t.Fatalf("default = %+v", g.SummaryLength)
	}
}

// 内置 novel 文体：四层单元类型（novel/part/chapter/scene），part 与 chapter 为汇总单元，
// 场景走完整生命周期；声明与 short-story 相同的四条小说规则，不声明写作法与评分指标。
func TestNovelGenre(t *testing.T) {
	g, ps, ok := LoadGenre("novel")
	if !ok || g == nil || len(ps) > 0 {
		t.Fatalf("LoadGenre(novel) = %+v, %+v, %v", g, ps, ok)
	}
	aggregated := map[bool][]string{}
	for _, u := range g.UnitTypes {
		aggregated[u.StageFromChildren] = append(aggregated[u.StageFromChildren], u.ID)
	}
	if !slices.Equal(aggregated[true], []string{"part", "chapter"}) || !slices.Equal(aggregated[false], []string{"novel", "scene"}) {
		t.Fatalf("aggregated unit types = %v / %v", aggregated[true], aggregated[false])
	}
	root := g.UnitTypes[0]
	if root.ID != "novel" || !slices.Equal(root.Children, []string{"part"}) ||
		!slices.Equal(root.BriefRequired, []string{"premise", "pov", "length", "key_messages"}) {
		t.Fatalf("root unit type = %+v", root)
	}
	if g.ContextBudget != 16000 || g.DefaultMethod != "snowflake" || len(g.ScoredMetrics) != 0 || len(g.PacingChecks) != 0 {
		t.Fatalf("budget/method/metrics = %d/%q/%d/%d", g.ContextBudget, g.DefaultMethod, len(g.ScoredMetrics), len(g.PacingChecks))
	}
	for _, id := range []string{"length", "fiction-cliches", "dialogue", "pov"} {
		if _, ok := g.Rules[id]; !ok || g.Rules[id].Severity != "warning" {
			t.Fatalf("rule %s = %+v", id, g.Rules[id])
		}
	}
	if _, ok := g.Rules["claims"]; ok {
		t.Fatalf("fiction genre must not declare claim rules")
	}
	var stages []string
	for _, s := range g.Stages {
		stages = append(stages, s.ID)
	}
	if !slices.Equal(stages, []string{"brief", "outline", "draft", "review"}) {
		t.Fatalf("stages = %v", stages)
	}
}

// stage_from_children 只能声明在带 children 的单元类型上：叶子类型声明时按 Schema 违例报告。
func TestStageFromChildrenLeaf(t *testing.T) {
	doc, p := schema.Parse([]byte("schema_version: 1\nid: g\ntitle: 测试文体\nunit_types:\n" +
		"  - {id: story, title: 故事, children: [scene]}\n  - {id: scene, title: 场景, stage_from_children: true}\n" +
		"stages:\n  - {id: brief, title: 简报, required_artifacts: [brief]}\n"))
	if p != nil {
		t.Fatal(p)
	}
	if ps := schema.Named("genre").ValidateNode(doc); len(ps) > 0 {
		t.Fatalf("boolean field must pass the JSON Schema: %+v", ps)
	}
	g := genreOf(doc)
	ps := checkGenre(g)
	if len(ps) != 1 || ps[0].Code != "artifact.schema-violation" || ps[0].Field != "unit_types[1].stage_from_children" {
		t.Fatalf("leaf aggregation problems = %+v", ps)
	}
}
