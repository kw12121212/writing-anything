package outline

import (
	"crypto/sha256"
	"encoding/hex"
	"os"
	"path/filepath"
	"slices"
	"strings"
	"testing"

	"github.com/kw12121212/writing-anything/internal/brief"
	"github.com/kw12121212/writing-anything/internal/catalog"
	"github.com/kw12121212/writing-anything/internal/workspace"
)

const unitDir = ".writing-anything/works/my-post"

var rel = Path(unitDir)

func abs(root, p string) string { return filepath.Join(root, filepath.FromSlash(p)) }

func put(t *testing.T, root, p, content string) {
	t.Helper()
	if err := os.MkdirAll(filepath.Dir(abs(root, p)), 0o755); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(abs(root, p), []byte(content), 0o644); err != nil {
		t.Fatal(err)
	}
}

func read(t *testing.T, root, p string) string {
	t.Helper()
	data, err := os.ReadFile(abs(root, p))
	if err != nil {
		t.Fatal(err)
	}
	return string(data)
}

// project 返回含已登记素材 src-a、写作简报（核心观点 km-1 与 km-2）的项目目录。
func project(t *testing.T) string {
	t.Helper()
	root := t.TempDir()
	h := sha256.Sum256([]byte("笔记"))
	put(t, root, ".writing-anything/sources/src-a/original.txt", "笔记")
	put(t, root, ".writing-anything/sources/src-a/source.yaml",
		"schema_version: 1\nid: src-a\ntype: author-note\nfile: original.txt\nsha256: "+hex.EncodeToString(h[:])+"\nbytes: 6\n")
	put(t, root, unitDir+"/brief.yaml", "schema_version: 1\nkey_messages:\n  - {id: km-1, text: 甲, sources: [src-a]}\n  - {id: km-2, text: 乙, sources: [src-a]}\n")
	return root
}

func messages(t *testing.T, root string) Messages {
	t.Helper()
	ms := LoadMessages(root, unitDir, brief.Queue{}, Picker{})
	if ms.Problems != nil || len(ms.List) != 2 {
		t.Fatalf("messages = %+v", ms)
	}
	return ms
}

func codes(ps []workspace.Problem) []string {
	var out []string
	for _, p := range ps {
		out = append(out, p.Code)
	}
	return out
}

func ptr[T any](v T) *T { return &v }

func noEnsure() ([]string, []workspace.Problem) { return nil, nil }

func gapSummary(gs []Gap) []string {
	var out []string
	for _, g := range gs {
		out = append(out, g.Code+" "+g.KeyMessage+g.Section)
	}
	return out
}

func TestLoadAndCheck(t *testing.T) {
	root := project(t)
	ms := messages(t, root)
	if o, ps := Load(root, unitDir, ms); o != nil || ps != nil {
		t.Fatalf("missing outline: %+v %+v", o, ps)
	}
	tests := []struct{ name, content, code, field string }{
		{"duplicate section", "schema_version: 1\nsections:\n  - {id: a, title: t, reason: r}\n  - {id: a, title: t, reason: r}\n", "artifact.duplicate-id", "sections[1].id"},
		{"unknown message", "schema_version: 1\nsections:\n  - {id: a, title: t, covers: [km-1, km-9]}\n", "outline.unknown-message", "sections[0].covers[1]"},
		{"unknown source", "schema_version: 1\nsections:\n  - {id: a, title: t, reason: r, uses: [src-nope]}\n", "outline.unknown-source", "sections[0].uses[0]"},
		{"schema violation", "schema_version: 1\nsections:\n  - {id: a, title: t, note: x}\n", "artifact.schema-violation", "sections[0].note"},
		{"parse error", "sections: [\n", "artifact.parse-error", ""},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			put(t, root, rel, tt.content)
			o, ps := Load(root, unitDir, ms)
			if o != nil || len(ps) != 1 || ps[0].Code != tt.code || ps[0].Field != tt.field || ps[0].Path != rel {
				t.Fatalf("Load = %+v, %+v; want one %s at %s", o, ps, tt.code, tt.field)
			}
			if ps := CheckArtifact(root, rel, "", nil, nil); !slices.Equal(codes(ps), []string{tt.code}) {
				t.Fatalf("CheckArtifact = %+v", ps)
			}
		})
	}
	// 写作简报不通过校验时不检查 covers 引用。
	put(t, root, rel, "schema_version: 1\nsections:\n  - {id: a, title: t, covers: [km-9]}\n")
	if o, ps := Load(root, unitDir, Messages{Problems: []workspace.Problem{{Code: "x"}}}); o == nil || ps != nil {
		t.Fatalf("invalid brief: %+v %+v", o, ps)
	}

	// 合法时 validate 报告覆盖缺口。
	put(t, root, rel, "schema_version: 1\nsections:\n  - {id: why, title: 为什么, covers: [km-1], uses: [src-a]}\n  - {id: bg, title: 背景}\n")
	checked, ps := CheckUnitArtifacts(root, []string{unitDir, unitDir + "/units/intro"}, brief.Queue{}, noDefault, noMetrics)
	if !slices.Equal(checked, []string{rel}) || !slices.Equal(codes(ps), []string{"outline.uncovered-message", "outline.missing-reason"}) {
		t.Fatalf("CheckUnitArtifacts = %v %+v", checked, ps)
	}
	if ps[0].Field != "" || !strings.Contains(ps[0].Message, "km-2") || ps[1].Field != "sections[1]" || ps[1].Path != rel {
		t.Fatalf("gap problems = %+v", ps)
	}
	// 写作简报不通过校验时不报告覆盖缺口（由写作简报自己的校验报告）。
	put(t, root, unitDir+"/brief.yaml", "k: v\n")
	if ps := CheckArtifact(root, rel, "", nil, nil); ps != nil {
		t.Fatalf("invalid brief gaps: %+v", ps)
	}
}

// 场景契约（Scene Contract）：beats 引用同一作品根单元大纲的节（outline.unknown-beat），根单元没有大纲时不检查。
func TestSceneContractBeats(t *testing.T) {
	root := project(t)
	child := unitDir + "/units/intro"
	put(t, root, rel, "schema_version: 1\nsections:\n  - {id: b1, title: 灯变暗, reason: r}\n  - {id: clash, title: 灯下对峙, reason: r}\n")
	put(t, root, child+"/outline.yaml", "schema_version: 1\nsections:\n  - {id: scene-1, title: 灯下对峙, characters: [守灯人, 精灵], beats: [b1, b9], reveal: 精灵的真实身份}\n")
	o, fatal, refs := LoadLenient(root, child, Messages{})
	if o == nil || fatal != nil || len(refs) != 1 || refs[0].Code != CodeUnknownBeat || refs[0].Field != "sections[0].beats[1]" || refs[0].Path != child+"/outline.yaml" {
		t.Fatalf("LoadLenient = %+v, %+v, %+v", o, fatal, refs)
	}
	if _, ps := Load(root, child, Messages{}); len(ps) != 1 || ps[0].Code != CodeUnknownBeat {
		t.Fatalf("Load = %+v", ps)
	}
	if s := o.Sections[0]; !slices.Equal(s.Characters, []string{"守灯人", "精灵"}) || !slices.Equal(s.Beats, []string{"b1", "b9"}) || s.Reveal != "精灵的真实身份" {
		t.Fatalf("section = %+v", s)
	}
	// 根单元大纲缺失时不做引用检查。
	os.Remove(abs(root, rel))
	if o, fatal, refs = LoadLenient(root, child, Messages{}); o == nil || fatal != nil || refs != nil {
		t.Fatalf("no root outline: %+v %+v %+v", o, fatal, refs)
	}
	// 根单元大纲不合法时同样不检查。
	put(t, root, rel, "schema_version: 1\nsections:\n  - {id: b1, title: t, note: x}\n")
	if o, fatal, refs = LoadLenient(root, child, Messages{}); o == nil || fatal != nil || refs != nil {
		t.Fatalf("invalid root outline: %+v %+v %+v", o, fatal, refs)
	}
	// 作品 id 与结构段 units 同名时同样按固定前缀解析（structural-path-parsing）。
	sameName := ".writing-anything/works/units"
	put(t, root, sameName+"/outline.yaml", "schema_version: 1\nsections:\n  - {id: b1, title: 灯变暗, reason: r}\n")
	put(t, root, sameName+"/units/s1/outline.yaml", "schema_version: 1\nsections:\n  - {id: scene-1, title: 灯下对峙, beats: [b9]}\n")
	if _, fatal, refs := LoadLenient(root, sameName+"/units/s1", Messages{}); fatal != nil || len(refs) != 1 || refs[0].Code != CodeUnknownBeat || refs[0].Field != "sections[0].beats[0]" {
		t.Fatalf("work named units: %+v %+v", fatal, refs)
	}

	// 根单元大纲自身的节也可以引用同大纲的节（节拍组）。
	put(t, root, rel, "schema_version: 1\nsections:\n  - {id: b1, title: 灯变暗, reason: r, beats: [b1]}\n")
	if o, ps := Load(root, unitDir, messages(t, root)); o == nil || ps != nil {
		t.Fatalf("self reference: %+v %+v", o, ps)
	}
}

// 场景契约字段经 outline set 写入与删除（outline-set）。
func TestSetSceneContract(t *testing.T) {
	root := project(t)
	child := unitDir + "/units/intro"
	ensure := func() ([]string, []workspace.Problem) { return nil, nil }
	put(t, root, child+"/brief.yaml", "schema_version: 1\n")
	put(t, root, rel, "schema_version: 1\nsections:\n  - {id: clash, title: 灯下对峙, reason: r}\n")
	o, changed, ps := Set(root, child, Edit{ID: "scene-1", Title: ptr("灯下对峙"), Characters: &[]string{"守灯人", "精灵"}, Beats: &[]string{"clash"}, Reveal: ptr("精灵的真实身份")}, Messages{}, ensure)
	if ps != nil || !slices.Equal(changed, []string{child + "/outline.yaml"}) || o.Sections[0].ID != "scene-1" ||
		!slices.Equal(o.Sections[0].Characters, []string{"守灯人", "精灵"}) || !slices.Equal(o.Sections[0].Beats, []string{"clash"}) || o.Sections[0].Reveal != "精灵的真实身份" {
		t.Fatalf("set contract: %+v %v %+v", o, changed, ps)
	}
	want := "# 大纲（Outline）；请用 wa-cli outline set、wa-cli outline remove 与 wa-cli scene contract 修改。\n\nschema_version: 1\nsections:\n  - id: scene-1\n    title: 灯下对峙\n    characters: [守灯人, 精灵]\n    beats: [clash]\n    reveal: 精灵的真实身份\n"
	if got := read(t, root, child+"/outline.yaml"); got != want {
		t.Fatalf("outline.yaml =\n%s\nwant\n%s", got, want)
	}
	// 删除契约字段。
	if o, changed, ps = Set(root, child, Edit{ID: "scene-1", Characters: &[]string{}, Beats: &[]string{}, Reveal: ptr("")}, Messages{}, noEnsure); ps != nil ||
		o.Sections[0].Characters != nil || o.Sections[0].Beats != nil || o.Sections[0].Reveal != "" || changed == nil {
		t.Fatalf("clear contract: %+v %v %+v", o, changed, ps)
	}
	// 本次参数给出的 beats 引用根单元大纲中不存在的节时拒绝，不写文件。
	before := read(t, root, child+"/outline.yaml")
	if _, changed, ps = Set(root, child, Edit{ID: "scene-1", Beats: &[]string{"b9"}}, Messages{}, noEnsure); changed != nil ||
		len(ps) != 1 || ps[0].Code != CodeUnknownBeat || ps[0].Field != "beats[0]" || ps[0].Path != "" {
		t.Fatalf("unknown beat: %v %+v", changed, ps)
	}
	if got := read(t, root, child+"/outline.yaml"); got != before {
		t.Fatalf("file changed:\n%s", got)
	}
	// 已有节中的悬空 beats 引用不阻止修改其他字段（cross-artifact-references）。
	put(t, root, child+"/outline.yaml", "schema_version: 1\nsections:\n  - {id: scene-1, title: 灯下对峙, beats: [b9]}\n")
	if o, changed, ps = Set(root, child, Edit{ID: "scene-1", Beats: &[]string{"clash"}}, Messages{}, noEnsure); ps != nil || changed == nil ||
		!slices.Equal(o.Sections[0].Beats, []string{"clash"}) {
		t.Fatalf("repair dangling: %+v %v %+v", o, changed, ps)
	}
}

func TestCoverageAndGaps(t *testing.T) {
	root := project(t)
	ms := messages(t, root)
	if got := gapSummary(Gaps(nil, ms)); !slices.Equal(got, []string{"outline.uncovered-message km-1", "outline.uncovered-message km-2"}) {
		t.Fatalf("no outline: %v", got)
	}
	o := &Outline{Sections: []Section{
		{ID: "why", Title: "为什么", Covers: []string{"km-1"}},
		{ID: "bg", Title: "背景", Reason: " "},
		{ID: "also", Title: "再说", Covers: []string{"km-1"}},
		{ID: "intro", Title: "引子", Reason: "引出问题"},
	}}
	cov := Coverage(o, ms)
	if len(cov) != 2 || !slices.Equal(cov[0].Sections, []string{"why", "also"}) || cov[1].Sections != nil || cov[0].Text != "甲" {
		t.Fatalf("coverage = %+v", cov)
	}
	gaps := Gaps(o, ms)
	if got := gapSummary(gaps); !slices.Equal(got, []string{"outline.uncovered-message km-2", "outline.missing-reason bg"}) || gaps[1].Index != 1 {
		t.Fatalf("gaps = %v", got)
	}
	if ps := Problems(rel, gaps); ps[1].Field != "sections[1]" || ps[0].Field != "" {
		t.Fatalf("problems = %+v", ps)
	}
	if got := Coverage(o, Messages{}); got != nil {
		t.Fatalf("no brief coverage = %+v", got)
	}
}

func TestGateCheck(t *testing.T) {
	root := project(t)
	if g := Check(root, unitDir, false, false, brief.Queue{}, Picker{}); g != nil {
		t.Fatalf("no outline: %+v", g)
	}
	if g := Check(root, unitDir, false, true, brief.Queue{}, Picker{}); g == nil || g.Path != unitDir+"/outline.md" || len(g.Gaps) != 2 {
		t.Fatalf("outline.md: %+v", g)
	}
	put(t, root, rel, "schema_version: 1\nsections:\n  - {id: why, title: t, covers: [km-1, km-2]}\n")
	if g := Check(root, unitDir, true, false, brief.Queue{}, Picker{}); g == nil || g.Path != rel || len(g.Gaps) != 0 || g.Invalid != nil {
		t.Fatalf("covered: %+v", g)
	}
	put(t, root, rel, "k: v\n")
	if g := Check(root, unitDir, true, false, brief.Queue{}, Picker{}); g == nil || len(g.Invalid) == 0 {
		t.Fatalf("invalid outline: %+v", g)
	}
	put(t, root, unitDir+"/brief.yaml", "k: v\n")
	if g := Check(root, unitDir, false, true, brief.Queue{}, Picker{}); g == nil || g.Path != unitDir+"/outline.md" || len(g.Invalid) == 0 {
		t.Fatalf("invalid brief: %+v", g)
	}
	os.Remove(abs(root, rel))
	os.Remove(abs(root, unitDir+"/brief.yaml"))
	// outline.yaml 在两次检查之间被删除。
	if g := Check(root, unitDir, true, false, brief.Queue{}, Picker{}); g != nil {
		t.Fatalf("vanished: %+v", g)
	}
}

func TestSetAndRemove(t *testing.T) {
	root := project(t)
	ms := messages(t, root)
	ensured := false
	ensure := func() ([]string, []workspace.Problem) { ensured = true; return []string{"new-dir"}, nil }
	o, changed, ps := Set(root, unitDir, Edit{ID: "why", Title: ptr("为什么"), Covers: &[]string{"km-1"}}, ms, ensure)
	if ps != nil || !ensured || !slices.Equal(changed, []string{"new-dir", rel}) || len(o.Sections) != 1 || o.Path != rel {
		t.Fatalf("create: %+v %v %+v", o, changed, ps)
	}
	want := "# 大纲（Outline）；请用 wa-cli outline set、wa-cli outline remove 与 wa-cli scene contract 修改。\n\nschema_version: 1\nsections:\n  - id: why\n    title: 为什么\n    covers: [km-1]\n"
	if got := read(t, root, rel); got != want {
		t.Fatalf("outline.yaml =\n%s\nwant\n%s", got, want)
	}
	order := func() []string {
		o, _ := Load(root, unitDir, ms)
		var ids []string
		for _, s := range o.Sections {
			ids = append(ids, s.ID)
		}
		return ids
	}
	for _, e := range []Edit{
		{ID: "how", Title: ptr("怎么做"), Covers: &[]string{"km-2"}},
		{ID: "intro", Title: ptr("引子"), Reason: ptr("引出问题"), Before: "why"},
	} {
		if _, _, ps := Set(root, unitDir, e, ms, noEnsure); ps != nil {
			t.Fatal(ps)
		}
	}
	if got := order(); !slices.Equal(got, []string{"intro", "why", "how"}) {
		t.Fatalf("order = %v", got)
	}
	// 修改保留注释与未涉及的字段；移动到指定节之后。
	put(t, root, rel, strings.Replace(read(t, root, rel), "  - id: how\n", "  # 作者的注释\n  - id: how\n", 1))
	o, _, ps = Set(root, unitDir, Edit{ID: "how", Uses: &[]string{"src-a"}}, ms, noEnsure)
	if ps != nil || o.Sections[2].Title != "怎么做" || !slices.Equal(o.Sections[2].Covers, []string{"km-2"}) || !strings.Contains(read(t, root, rel), "# 作者的注释") {
		t.Fatalf("modify: %+v %+v\n%s", o, ps, read(t, root, rel))
	}
	if _, _, ps := Set(root, unitDir, Edit{ID: "intro", After: "how"}, ms, noEnsure); ps != nil || !slices.Equal(order(), []string{"why", "how", "intro"}) {
		t.Fatalf("move after: %+v %v", ps, order())
	}
	// 清空字段；取值未变时不写文件。
	o, _, ps = Set(root, unitDir, Edit{ID: "how", Uses: &[]string{}, Reason: ptr("")}, ms, noEnsure)
	if ps != nil || o.Sections[1].Uses != nil {
		t.Fatalf("clear: %+v %+v", o, ps)
	}
	before := read(t, root, rel)
	if _, changed, ps := Set(root, unitDir, Edit{ID: "why", Title: ptr("为什么")}, ms, noEnsure); ps != nil || changed != nil || read(t, root, rel) != before {
		t.Fatalf("unchanged: %v %+v", changed, ps)
	}
	// 删除。
	o, changed, ps = Remove(root, unitDir, "intro", ms)
	if ps != nil || !slices.Equal(changed, []string{rel}) || len(o.Sections) != 2 {
		t.Fatalf("remove: %+v %v %+v", o, changed, ps)
	}
	if _, _, ps := Remove(root, unitDir, "intro", ms); !slices.Equal(codes(ps), []string{"outline.section-not-found"}) || ps[0].Path != rel {
		t.Fatalf("remove missing: %+v", ps)
	}
	os.Remove(abs(root, rel))
	if _, _, ps := Remove(root, unitDir, "why", ms); !slices.Equal(codes(ps), []string{"outline.section-not-found"}) || ps[0].Path != "" {
		t.Fatalf("remove without outline: %+v", ps)
	}
}

func TestSetRejects(t *testing.T) {
	root := project(t)
	ms := messages(t, root)
	put(t, root, rel, "schema_version: 1\nsections:\n  - {id: why, title: 为什么, covers: [km-1]}\n")
	tests := []struct {
		name, code, field string
		e                 Edit
	}{
		{"unknown message", "outline.unknown-message", "covers[1]", Edit{ID: "why", Covers: &[]string{"km-1", "km-9"}}},
		{"unknown source", "outline.unknown-source", "uses[0]", Edit{ID: "why", Uses: &[]string{"src-nope"}}},
		{"missing anchor section", "outline.section-not-found", "before", Edit{ID: "why", Before: "nope"}},
		{"missing after section", "outline.section-not-found", "after", Edit{ID: "new", Title: ptr("t"), After: "nope"}},
		{"new without title", "artifact.schema-violation", "title", Edit{ID: "new", Reason: ptr("r")}},
		{"blank reason", "artifact.schema-violation", "reason", Edit{ID: "why", Reason: ptr(" ")}},
	}
	before := read(t, root, rel)
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			o, changed, ps := Set(root, unitDir, tt.e, ms, noEnsure)
			if o != nil || changed != nil || len(ps) != 1 || ps[0].Code != tt.code || ps[0].Field != tt.field || ps[0].Path != "" {
				t.Fatalf("Set = %+v %v %+v; want %s at %s", o, changed, ps, tt.code, tt.field)
			}
			if read(t, root, rel) != before {
				t.Fatal("outline.yaml changed")
			}
		})
	}
	// 给出 covers 而写作简报不通过校验时报告写作简报的问题。
	bad := Messages{Problems: []workspace.Problem{{Code: "artifact.schema-violation", Path: unitDir + "/brief.yaml"}}}
	if _, _, ps := Set(root, unitDir, Edit{ID: "why", Covers: &[]string{"km-1"}}, bad, noEnsure); len(ps) != 1 || ps[0].Path != unitDir+"/brief.yaml" {
		t.Fatalf("invalid brief: %+v", ps)
	}
	// 已有 outline.yaml 不通过校验；写出前确保单元目录的失败原样返回。
	put(t, root, rel, "schema_version: 1\nsections: {}\n")
	if _, _, ps := Set(root, unitDir, Edit{ID: "why", Title: ptr("t")}, ms, noEnsure); !slices.Equal(codes(ps), []string{"artifact.schema-violation"}) || ps[0].Path != rel {
		t.Fatalf("invalid existing: %+v", ps)
	}
	os.Remove(abs(root, rel))
	fail := func() ([]string, []workspace.Problem) {
		return nil, []workspace.Problem{{Code: "artifact.write-failed"}}
	}
	if _, _, ps := Set(root, unitDir, Edit{ID: "why", Title: ptr("t")}, ms, fail); !slices.Equal(codes(ps), []string{"artifact.write-failed"}) {
		t.Fatalf("ensure failure: %+v", ps)
	}
}

func TestAnchored(t *testing.T) {
	root := project(t)
	ms := messages(t, root)
	tests := []struct {
		name, content, field string
		remove               bool
		e                    Edit
	}{
		{"anchored root", "&r\nschema_version: 1\nsections: []\n", "", false, Edit{ID: "why", Title: ptr("t")}},
		{"anchored sections", "schema_version: 1\nsections: &s []\n", "sections", false, Edit{ID: "why", Title: ptr("t")}},
		{"anchored section", "schema_version: 1\nsections:\n  - &a {id: why, title: t, reason: r}\n", "sections[0]", false, Edit{ID: "why", Title: ptr("u")}},
		{"anchored title", "schema_version: 1\nsections:\n  - {id: why, title: &t 标题, reason: r}\n  - {id: how, title: *t, reason: r}\n", "sections[0].title", false, Edit{ID: "why", Title: ptr("u")}},
		{"move anchored subtree", "schema_version: 1\nsections:\n  - {id: why, title: &t 标题, reason: r}\n  - {id: how, title: *t, reason: r}\n", "sections[0]", false, Edit{ID: "why", After: "how"}},
		{"remove anchored subtree", "schema_version: 1\nsections:\n  - {id: why, title: &t 标题, reason: r}\n  - {id: how, title: *t, reason: r}\n", "sections[0]", true, Edit{ID: "why"}},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			put(t, root, rel, tt.content)
			var ps []workspace.Problem
			if tt.remove {
				_, _, ps = Remove(root, unitDir, tt.e.ID, ms)
			} else {
				_, _, ps = Set(root, unitDir, tt.e, ms, noEnsure)
			}
			if len(ps) != 1 || ps[0].Code != "outline.anchored-value" || ps[0].Field != tt.field {
				t.Fatalf("problems = %+v; want outline.anchored-value at %q", ps, tt.field)
			}
			if read(t, root, rel) != tt.content {
				t.Fatal("outline.yaml changed")
			}
		})
	}
}

func TestSymlinkedOutline(t *testing.T) {
	root := project(t)
	outside := filepath.Join(t.TempDir(), "outline.yaml")
	if err := os.WriteFile(outside, []byte("schema_version: 1\nsections: []\n"), 0o644); err != nil {
		t.Fatal(err)
	}
	if err := os.Symlink(outside, abs(root, rel)); err != nil {
		t.Skip("symlinks unavailable:", err)
	}
	ms := messages(t, root)
	if _, ps := Load(root, unitDir, ms); !slices.Equal(codes(ps), []string{"artifact.not-found"}) {
		t.Fatalf("Load = %+v", ps)
	}
	if _, _, ps := Set(root, unitDir, Edit{ID: "why", Title: ptr("t")}, ms, noEnsure); !slices.Equal(codes(ps), []string{"artifact.not-found"}) {
		t.Fatalf("Set = %+v", ps)
	}
	if checked, ps := CheckUnitArtifacts(root, []string{unitDir}, brief.Queue{}, noDefault, noMetrics); len(checked) != 1 || !slices.Equal(codes(ps), []string{"artifact.not-found"}) {
		t.Fatalf("CheckUnitArtifacts = %v %+v", checked, ps)
	}
	if g := Check(root, unitDir, true, false, brief.Queue{}, Picker{}); g == nil || len(g.Invalid) != 1 {
		t.Fatalf("Check = %+v", g)
	}
}

func TestParamOf(t *testing.T) {
	for field, want := range map[string]string{"sections[2].title": "title", "sections[0].covers[1]": "covers[1]", "sections[0].reason": "reason", "sections[0].id": "", "schema_version": "", "sections[0]": ""} {
		if got := paramOf(field); got != want {
			t.Errorf("paramOf(%q) = %q, want %q", field, got, want)
		}
	}
}

// 写作简报删除核心观点后，引用它的大纲仍能用 Set 与 Remove 修复；本次修改引入的引用问题照常拒绝。
func TestDanglingReferences(t *testing.T) {
	root := project(t)
	put(t, root, rel, "schema_version: 1\nsections:\n  - {id: why, title: 为什么, covers: [km-1]}\n  - {id: how, title: 怎么做, covers: [km-9], uses: [src-gone]}\n")
	ms := messages(t, root)
	if o, ps := Load(root, unitDir, ms); o != nil || !slices.Equal(codes(ps), []string{"outline.unknown-message", "outline.unknown-source"}) {
		t.Fatalf("Load = %+v %+v", o, ps)
	}
	o, fatal, refs := LoadLenient(root, unitDir, ms)
	if o == nil || fatal != nil || len(refs) != 2 {
		t.Fatalf("LoadLenient = %+v %+v %+v", o, fatal, refs)
	}
	if _, _, ps := Set(root, unitDir, Edit{ID: "why", Title: ptr("新标题")}, ms, noEnsure); ps != nil {
		t.Fatalf("unrelated edit: %+v", ps)
	}
	if _, _, ps := Set(root, unitDir, Edit{ID: "how", Covers: &[]string{}, Reason: ptr("背景")}, ms, noEnsure); ps != nil {
		t.Fatalf("fix covers: %+v", ps)
	}
	if _, _, ps := Set(root, unitDir, Edit{ID: "why", Covers: &[]string{"km-8"}}, ms, noEnsure); !slices.Equal(codes(ps), []string{"outline.unknown-message"}) {
		t.Fatalf("new dangling reference: %+v", ps)
	}
	if o, _, ps := Remove(root, unitDir, "how", ms); ps != nil || len(o.Sections) != 1 {
		t.Fatalf("remove: %+v %+v", o, ps)
	}
	if o, ps := Load(root, unitDir, ms); o == nil || ps != nil {
		t.Fatalf("repaired: %+v %+v", o, ps)
	}
}

func noDefault(string) string { return "" }

// noMetrics 表示调用方没有解析生效配置：跳过场景契约 targets 的检查。
func noMetrics(string) []catalog.ScoredMetric { return nil }

// 写作法缺口：未知 role、缺少的章节、文章类的顺序；规划类写作法与没有写作法时不检查（method-outline-check）。
func TestMethodGaps(t *testing.T) {
	article := &catalog.Method{ID: "a", Kind: catalog.MethodArticle, Sections: []catalog.MethodPart{{ID: "x", Title: "甲"}, {ID: "y", Title: "乙"}, {ID: "z", Title: "丙"}}}
	o := &Outline{Sections: []Section{{ID: "s1", Role: "z"}, {ID: "s2", Role: "x"}, {ID: "s3", Role: "q"}, {ID: "s4"}}}
	var got []string
	for _, g := range methodGaps(o, article) {
		got = append(got, g.Code+" "+g.Section)
	}
	want := []string{"outline.unknown-role s3", "outline.missing-method-part ", "outline.method-order s1"}
	if !slices.Equal(got, want) {
		t.Fatalf("gaps = %v, want %v", got, want)
	}
	if ps := Problems("o.yaml", methodGaps(o, article)); ps[0].Field != "sections[2].role" || ps[2].Field != "sections[0].role" || ps[1].Field != "" {
		t.Fatalf("fields: %+v", ps)
	}
	// 结构类写作法不检查顺序；规划类与 nil 不检查。
	structure := &catalog.Method{ID: "s", Kind: catalog.MethodStructure, Beats: []catalog.MethodPart{{ID: "x"}, {ID: "z"}}}
	if g := methodGaps(&Outline{Sections: []Section{{ID: "s1", Role: "z"}, {ID: "s2", Role: "x"}}}, structure); len(g) != 0 {
		t.Fatalf("structure order: %+v", g)
	}
	planning := &catalog.Method{ID: "p", Kind: catalog.MethodPlanning, Steps: []catalog.MethodPart{{ID: "x"}}}
	if methodGaps(o, planning) != nil || methodGaps(o, nil) != nil || len(methodGaps(nil, article)) != 3 {
		t.Fatal("planning, nil method or nil outline")
	}
}

// 节拍位置：按标题行之前的字符比例核对；围栏代码块中的 # 行不算标题，CRLF 与 LF 结果相同。
func TestBeatPositions(t *testing.T) {
	m := &catalog.Method{ID: "three", Kind: catalog.MethodStructure, Beats: []catalog.MethodPart{
		{ID: "setup", Title: "建置", Position: &catalog.Position{Min: 0, Max: 0.2}},
		{ID: "end", Title: "结局", Position: &catalog.Position{Min: 0.8, Max: 1}},
		{ID: "lost", Title: "找不到", Position: &catalog.Position{Min: 0, Max: 0.1}}}}
	o := &Outline{Sections: []Section{{ID: "a", Title: "开端", Role: "setup"}, {ID: "b", Title: "结尾", Role: "end"}, {ID: "c", Title: "无", Role: "lost"}}}
	body := strings.Repeat("字", 80)
	lf := "```\n# 结尾\n```\n\n# 开端\n\n" + body + "\n\n## 结尾 ##\n\n" + body + "\n"
	for _, content := range []string{lf, strings.ReplaceAll(lf, "\n", "\r\n")} {
		ps := BeatPositions([]byte(content), "d.md", o, m)
		if len(ps) != 1 || ps[0].Code != CodeBeatPosition || ps[0].Line != 9 || ps[0].Severity != "warning" || !strings.Contains(ps[0].Message, "end") {
			t.Fatalf("positions: %+v", ps)
		}
	}
	if BeatPositions([]byte(lf), "d.md", o, &catalog.Method{Kind: catalog.MethodArticle}) != nil || BeatPositions([]byte(""), "d.md", o, m) != nil || BeatPositions([]byte(lf), "d.md", nil, m) != nil {
		t.Fatal("non-structure, empty or nil outline")
	}
}
