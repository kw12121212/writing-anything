package canon

import (
	"os"
	"path/filepath"
	"strings"
	"testing"
	"time"

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

// 系列设定集目录尚不存在时 --scope series 首次登记创建目录与文件；作品级覆盖把 override（原因、作者确认与
// 注入时钟给出的 confirmed_at）写入作品设定集，重新读取可得到同一记录。
func TestAddSeriesFirstWriteAndPersistedOverride(t *testing.T) {
	root := t.TempDir()
	if _, ps := workspace.Init(root, nil); len(ps) > 0 {
		t.Fatal(ps)
	}
	writeAt(t, root, ".writing-anything/series/guide.yaml", "schema_version: 1\nid: guide\ntitle: 入门指南\nworks: [part-1, part-2]\npacks: []\nsources: []\nfacts: []\n")
	var ws []*work.Work
	for _, id := range []string{"part-1", "part-2"} {
		w, _, ps := work.Create(root, "short-story", id)
		if len(ps) > 0 {
			t.Fatal(ps)
		}
		ws = append(ws, w)
	}
	if _, err := os.Stat(filepath.Join(root, ".writing-anything", "series", "guide")); !os.IsNotExist(err) {
		t.Fatalf("系列设定集目录不应预先存在: %v", err)
	}
	if _, changed, ps := Add(root, ws[0], AddInput{Type: TypeWorldRule, Text: "雾天每隔三十秒鸣笛一次", ID: "rule-horn", Series: true}); ps != nil || len(changed) != 1 || changed[0] != SeriesPath("guide") {
		t.Fatalf("首次登记系列级条目: changed = %v, ps = %+v", changed, ps)
	}
	at := time.Date(2026, 6, 20, 12, 0, 0, 0, time.FixedZone("CST", 8*3600))
	e, _, ps := Add(root, ws[1], AddInput{Type: TypeWorldRule, Text: "改为电动雾笛，每隔二十秒鸣笛一次", ID: "rule-horn",
		OverrideReason: "第二部时隔十年，雾笛已更换", OverrideConfirm: "作者：按十年后的设备写", At: at})
	if ps != nil {
		t.Fatal(ps)
	}
	want := Override{Reason: "第二部时隔十年，雾笛已更换", Confirmation: "作者：按十年后的设备写", ConfirmedAt: "2026-06-20T04:00:00Z"}
	if e.Override == nil || *e.Override != want {
		t.Fatalf("override = %+v", e.Override)
	}
	entries, ps := List(root, ws[1])
	if ps != nil || len(entries) != 1 || entries[0].Override == nil || *entries[0].Override != want {
		t.Fatalf("重新读取的 override = %+v, ps = %+v", entries, ps)
	}
	data, err := os.ReadFile(filepath.Join(root, filepath.FromSlash(Path("part-2"))))
	if err != nil || !strings.Contains(string(data), `confirmed_at: "2026-06-20T04:00:00Z"`) {
		t.Fatalf("canon.yaml = %s (%v)", data, err)
	}
}

// 系列级设定集途经符号链接（.writing-anything/series 或 .writing-anything/series/<series> 被替换为指向项目目录之外的
// 符号链接）时，canon add --scope series 按不存在处理并报告 artifact.not-found，不在项目目录之外创建任何文件；
// 系列级读取同样报告该问题（validated-path-segments）。
func TestAddSeriesScopeRejectsSymlinkedDirs(t *testing.T) {
	for _, link := range []string{".writing-anything/series", ".writing-anything/series/guide"} {
		t.Run(link, func(t *testing.T) {
			root := t.TempDir()
			if _, ps := workspace.Init(root, nil); len(ps) > 0 {
				t.Fatal(ps)
			}
			outside := t.TempDir()
			seriesFile := "schema_version: 1\nid: guide\ntitle: 入门指南\nworks: [part-1]\npacks: []\nsources: []\nfacts: []\n"
			if link == ".writing-anything/series" {
				// 系列文件随目录一起位于外部。
				writeAt(t, outside, "guide.yaml", seriesFile)
			} else {
				writeAt(t, root, ".writing-anything/series/guide.yaml", seriesFile)
			}
			if err := os.Symlink(outside, filepath.Join(root, filepath.FromSlash(link))); err != nil {
				t.Fatal(err)
			}
			w, _, ps := work.Create(root, "short-story", "part-1")
			if len(ps) > 0 {
				t.Fatal(ps)
			}
			_, changed, ps := Add(root, w, AddInput{Type: TypeWorldRule, Text: "雾天鸣笛", ID: "rule-horn", Series: true})
			if len(ps) != 1 || ps[0].Code != "artifact.not-found" || changed != nil {
				t.Fatalf("changed = %v, ps = %+v", changed, ps)
			}
			entries, err := os.ReadDir(outside)
			if err != nil {
				t.Fatal(err)
			}
			for _, e := range entries {
				if e.Name() != "guide.yaml" {
					t.Fatalf("项目目录之外被写入了 %s", e.Name())
				}
			}
			if _, ps := SeriesList(root, "guide"); len(ps) != 1 || ps[0].Code != "artifact.not-found" {
				t.Fatalf("SeriesList ps = %+v", ps)
			}
		})
	}
}

// 系列成员关系不跨工作区缓存：同一进程先在属于系列的工作区解析 part-1，再在另一个 part-1 不属于任何系列的工作区
// 登记系列级条目，后者报告 canon.not-in-series；把作品加入系列后立即生效。
func TestSeriesMembershipNotCachedAcrossWorkspaces(t *testing.T) {
	root1, w1 := scopeProject(t)
	if scoped, ps := Resolve(root1, w1); ps != nil || len(scoped) == 0 {
		t.Fatalf("scoped = %+v, ps = %+v", scoped, ps)
	}
	root2 := t.TempDir()
	if _, ps := workspace.Init(root2, nil); len(ps) > 0 {
		t.Fatal(ps)
	}
	w2, _, ps := work.Create(root2, "short-story", "part-1")
	if len(ps) > 0 {
		t.Fatal(ps)
	}
	if _, _, ps := Add(root2, w2, AddInput{Type: TypeWorldRule, Text: "雾天鸣笛", Series: true}); len(ps) != 1 || ps[0].Code != CodeNotInSeries {
		t.Fatalf("ps = %+v", ps)
	}
	writeAt(t, root2, ".writing-anything/series/solo.yaml", "schema_version: 1\nid: solo\ntitle: 单部\nworks: [part-1]\npacks: []\nsources: []\nfacts: []\n")
	if _, changed, ps := Add(root2, w2, AddInput{Type: TypeWorldRule, Text: "雾天鸣笛", Series: true}); ps != nil || len(changed) != 1 || changed[0] != SeriesPath("solo") {
		t.Fatalf("加入系列后: changed = %v, ps = %+v", changed, ps)
	}
}
