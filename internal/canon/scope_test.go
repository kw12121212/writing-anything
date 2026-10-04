package canon

import (
	"os"
	"path/filepath"
	"testing"

	"github.com/kw12121212/writing-anything/internal/work"
	"github.com/kw12121212/writing-anything/internal/workspace"
)

// scopeProject 初始化系列 guide 与两部 short-story 作品（part-1、part-2 均为成员），系列级 canon 有
// canon-1（守灯人）与 canon-2（灯塔）。
func scopeProject(t *testing.T) (string, *work.Work) {
	t.Helper()
	root := t.TempDir()
	if _, ps := workspace.Init(root, nil); len(ps) > 0 {
		t.Fatal(ps)
	}
	writeAt(t, root, ".writing-anything/series/guide.yaml", `schema_version: 1
id: guide
title: 入门指南
works: [part-1, part-2]
packs: []
sources: []
facts: []
`)
	writeAt(t, root, ".writing-anything/series/guide/canon.yaml", `schema_version: 1
entries:
  - {id: canon-1, type: character, name: 守灯人, text: 系列级：五十岁, sources: [], protection: normal, scope: series}
  - {id: canon-2, type: location, name: 灯塔, text: 系列级：海角旧灯塔, sources: [], protection: normal, scope: series}
`)
	for _, id := range []string{"part-1", "part-2"} {
		if _, _, ps := work.Create(root, "short-story", id); len(ps) > 0 {
			t.Fatal(ps)
		}
	}
	// part-1 有作品级 canon-1（覆盖系列级同 id）。
	writeAt(t, root, ".writing-anything/works/part-1/canon.yaml", `schema_version: 1
entries:
  - id: canon-1
    type: character
    name: 守灯人
    text: 作品级：本作里他已退休
    sources: []
    protection: normal
    scope: work
    override: {reason: 本作为十年后的时间线, confirmation: 作者确认这条分歧, confirmed_at: "2026-10-04T08:00:00Z"}
`)
	w, ps := work.Load(root, "part-1")
	if len(ps) > 0 {
		t.Fatal(ps)
	}
	return root, w
}

func writeAt(t *testing.T, root, rel, content string) {
	t.Helper()
	path := filepath.Join(root, filepath.FromSlash(rel))
	if err := os.MkdirAll(filepath.Dir(path), 0o755); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(path, []byte(content), 0o644); err != nil {
		t.Fatal(err)
	}
}

func scopeOf(t *testing.T, es []ScopedEntry, id string) *ScopedEntry {
	t.Helper()
	for i := range es {
		if es[i].ID == id {
			return &es[i]
		}
	}
	return nil
}

func TestResolveWorkOverridesSeries(t *testing.T) {
	root, w := scopeProject(t)
	scoped, ps := Resolve(root, w)
	if ps != nil {
		t.Fatal(ps)
	}
	c1 := scopeOf(t, scoped, "canon-1")
	if c1 == nil || c1.Scope != "work" || c1.Text != "作品级：本作里他已退休" {
		t.Fatalf("canon-1 = %+v", c1)
	}
	if c1.Override == nil || c1.Override.Reason != "本作为十年后的时间线" {
		t.Fatalf("override = %+v", c1.Override)
	}
	c2 := scopeOf(t, scoped, "canon-2")
	if c2 == nil || c2.Scope != "series:guide" {
		t.Fatalf("canon-2 = %+v", c2)
	}
	// part-2（无作品级 canon）：全部来自系列级。
	w2, _ := work.Load(root, "part-2")
	scoped2, ps := Resolve(root, w2)
	if ps != nil {
		t.Fatal(ps)
	}
	if len(scoped2) != 2 {
		t.Fatalf("scoped2 = %+v", scoped2)
	}
	for _, se := range scoped2 {
		if se.Scope != "series:guide" {
			t.Fatalf("scope = %+v", se)
		}
	}
}

func TestResolveBadSeriesFile(t *testing.T) {
	root, w := scopeProject(t)
	writeAt(t, root, ".writing-anything/series/guide/canon.yaml", "schema_version: 1\nentries: [\n")
	if scoped, ps := Resolve(root, w); scoped != nil || ps == nil {
		t.Fatalf("scoped = %+v ps = %+v", scoped, ps)
	}
}

func TestAddSeriesScopeAndOverride(t *testing.T) {
	root, w := scopeProject(t)
	// --scope series：写入系列文件（id 缺省生成）。
	e, changed, ps := Add(root, w, AddInput{Type: TypeItem, Name: "长命灯", Text: "系列级的重要道具", Series: true})
	if ps != nil {
		t.Fatal(ps)
	}
	if e.Scope != "series" {
		t.Fatalf("scope = %+v", e)
	}
	if len(changed) != 1 || changed[0] != SeriesPath("guide") {
		t.Fatalf("changed = %v", changed)
	}
	// 覆盖系列级同 id 条目而不给确认：拒绝。
	if _, _, ps := Add(root, w, AddInput{Type: TypeCharacter, Name: "灯塔看守", Text: "作品级新角色", ID: "canon-x"}); ps != nil {
		t.Fatal(ps)
	}
	if _, _, ps := Add(root, w, AddInput{Type: TypeItem, Name: "长明灯罩", Text: "本作里已经蒙尘", ID: "canon-x2"}); ps != nil {
		t.Fatal(ps)
	}
	// canon-x2 在 part-1 上不覆盖任何系列级条目（前面 canon-x/canon-x2 都是作品级新 id），先清掉这两条
	// 便于后续断言；然后直接构造同 id 覆盖：canon-2（系列级）在 part-2 上覆盖。
	w2, _ := work.Load(root, "part-2")
	if _, _, ps := Add(root, w2, AddInput{Type: TypeLocation, Name: "灯塔", Text: "作品级：已废弃", ID: "canon-2"}); ps == nil || ps[0].Code != CodeOverrideUnconfirmed {
		t.Fatalf("ps = %+v", ps)
	}
	e2, changed, ps := Add(root, w2, AddInput{Type: TypeLocation, Name: "灯塔", Text: "作品级：已废弃", ID: "canon-2",
		OverrideReason: "本作开头灯塔已废弃", OverrideConfirm: "作者确认这条分歧"})
	if ps != nil {
		t.Fatal(ps)
	}
	if e2.Override == nil || e2.Override.Reason != "本作开头灯塔已废弃" {
		t.Fatalf("override = %+v", e2.Override)
	}
	// 覆盖后解析视图只看到作品级版本。
	scoped, _ := Resolve(root, w2)
	c2 := scopeOf(t, scoped, "canon-2")
	if c2 == nil || c2.Scope != "work" || c2.Text != "作品级：已废弃" {
		t.Fatalf("resolved canon-2 = %+v", c2)
	}
	// 非覆盖条目给覆盖参数：拒绝。
	if _, _, ps := Add(root, w2, AddInput{Type: TypeItem, Name: "新道具", Text: "t", ID: "canon-new", OverrideReason: "r", OverrideConfirm: "c"}); ps == nil {
		t.Fatal("non-override with override params accepted")
	}
}

func TestAddSeriesNoMembership(t *testing.T) {
	root := t.TempDir()
	if _, ps := workspace.Init(root, nil); len(ps) > 0 {
		t.Fatal(ps)
	}
	w, _, ps := work.Create(root, "short-story", "solo")
	if len(ps) > 0 {
		t.Fatal(ps)
	}
	if _, _, ps := Add(root, w, AddInput{Type: TypeCharacter, Name: "甲", Text: "t", Series: true}); ps == nil || ps[0].Code != CodeNotInSeries {
		t.Fatalf("ps = %+v", ps)
	}
}

func TestSeriesListAndCheck(t *testing.T) {
	root, w := scopeProject(t)
	entries, ps := SeriesList(root, "guide")
	if ps != nil || len(entries) != 2 {
		t.Fatalf("entries = %+v ps = %+v", entries, ps)
	}
	if _, ps := SeriesList(root, "nope"); ps != nil || entries == nil {
		t.Fatalf("missing series: %+v", ps)
	}
	_ = w
}
