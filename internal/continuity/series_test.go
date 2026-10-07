package continuity

import (
	"os"
	"path/filepath"
	"slices"
	"strings"
	"testing"

	"github.com/kw12121212/writing-anything/internal/decision"
	"github.com/kw12121212/writing-anything/internal/timeline"
	"github.com/kw12121212/writing-anything/internal/work"
	"github.com/kw12121212/writing-anything/internal/workspace"
)

// newSeries 初始化系列 guide（works 为 [book-1, book-2]）与两部 short-story 作品：book-1 的 s1 确立
// canon-1 的 confirmed 状态 受伤 与事件 ev-1；book-2 的 s1 有冲突形态（由用例写入）。
func newSeries(t *testing.T) (string, []*work.Work) {
	t.Helper()
	root := t.TempDir()
	if _, ps := workspace.Init(root, nil); len(ps) > 0 {
		t.Fatal(ps)
	}
	writeAt2(t, root, ".writing-anything/series/guide.yaml", `schema_version: 1
id: guide
title: 指南
works: [book-1, book-2]
packs: []
sources: []
facts: []
`)
	writeAt2(t, root, ".writing-anything/series/guide/canon.yaml", `schema_version: 1
entries:
  - {id: canon-1, type: character, name: 守灯人, text: 系列级设定, sources: [], protection: normal, scope: series}
`)
	var ws []*work.Work
	var ps []workspace.Problem
	for _, id := range []string{"book-1", "book-2"} {
		var w *work.Work
		if w, _, ps = work.Create(root, "short-story", id); len(ps) > 0 {
			t.Fatal(ps)
		}
		if _, _, ps = work.AddUnit(root, id, "scene", "s1", work.Anchor{}); len(ps) > 0 {
			t.Fatal(ps)
		}
		if w, ps = work.Load(root, id); len(ps) > 0 {
			t.Fatal(ps)
		}
		ws = append(ws, w)
	}
	// book-1：状态（canon-1 受伤）与事件 ev-1。
	writeAt2(t, root, ".writing-anything/works/book-1/state.yaml", `schema_version: 1
events:
  - {id: state-1, scene: book-1/s1, entry: canon-1, state: 受伤, status: confirmed, confirmation: 作者确认, decided_at: "2026-10-04T08:00:00Z"}
`)
	writeAt2(t, root, ".writing-anything/works/book-1/units/s1/outline.yaml", "schema_version: 1\nsections:\n  - {id: sc1, title: 场景, pov: 守灯人, characters: [守灯人], events: [ev-1], time: 第一日}\n")
	writeAt2(t, root, ".writing-anything/works/book-1/units/s1/draft.md", "正文。\n")
	// book-2：默认冲突夹具由用例覆盖。
	writeAt2(t, root, ".writing-anything/works/book-2/units/s1/outline.yaml", "schema_version: 1\nsections:\n  - {id: sc1, title: 场景, pov: 守灯人, characters: [守灯人], time: 第一日}\n")
	writeAt2(t, root, ".writing-anything/works/book-2/units/s1/draft.md", "正文。\n")
	return root, ws
}

func writeAt2(t *testing.T, root, rel, content string) {
	t.Helper()
	path := filepath.Join(root, filepath.FromSlash(rel))
	if err := os.MkdirAll(filepath.Dir(path), 0o755); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(path, []byte(content), 0o644); err != nil {
		t.Fatal(err)
	}
}

func hasCode(fs []Finding, code string) bool {
	for _, f := range fs {
		if f.Code == code {
			return true
		}
	}
	return false
}

// 完成标准：第二部中与第一部冲突的场景被阻塞（同 time 双出场的跨作品形态）。
func TestRunSeriesSameTimeCrossWork(t *testing.T) {
	root, ws := newSeries(t)
	fs, ps := RunSeries(root, ws)
	if ps != nil {
		t.Fatal(ps)
	}
	if !hasCode(fs, timeline.CodeSameTime) {
		t.Fatalf("findings = %+v", fs)
	}
	for _, f := range fs {
		if f.Code == timeline.CodeSameTime && f.Scene != "book-1/s1" {
			t.Fatalf("cross-work finding on wrong scene: %+v", f)
		}
		if !strings.Contains(f.Message, "守灯人") || !strings.Contains(f.Message, "book-2/s1") {
			t.Fatalf("message missing work/scene: %s", f.Message)
		}
	}
	// 修正：book-2 声明晚于 book-1/s1 后不再报。
	writeAt2(t, root, ".writing-anything/works/book-2/units/s1/outline.yaml",
		"schema_version: 1\nsections:\n  - {id: sc1, title: 场景, pov: 守灯人, characters: [守灯人], time: 第一日, after: [book-1/s1]}\n")
	if fs, _ = RunSeries(root, ws); hasCode(fs, timeline.CodeSameTime) {
		t.Fatalf("after fix findings = %+v", fs)
	}
}

func TestRunSeriesConflictingState(t *testing.T) {
	root, ws := newSeries(t)
	writeAt2(t, root, ".writing-anything/works/book-2/state.yaml", `schema_version: 1
events:
  - {id: state-9, scene: book-2/s1, entry: canon-1, state: 痊愈, status: confirmed, confirmation: 作者确认, decided_at: "2026-10-04T09:00:00Z"}
`)
	fs, _ := RunSeries(root, ws)
	if !hasCode(fs, CodeConflictingState) {
		t.Fatalf("findings = %+v", fs)
	}
	for _, f := range fs {
		if f.Code == CodeConflictingState {
			if f.Scene != "book-1/s1" || !strings.Contains(f.Message, "受伤") || !strings.Contains(f.Message, "痊愈") {
				t.Fatalf("finding = %+v", f)
			}
		}
	}
	// 描述一致不是冲突。
	writeAt2(t, root, ".writing-anything/works/book-2/state.yaml", `schema_version: 1
events:
  - {id: state-9, scene: book-2/s1, entry: canon-1, state: 受伤, status: confirmed, confirmation: 作者确认, decided_at: "2026-10-04T09:00:00Z"}
`)
	if fs, _ = RunSeries(root, ws); hasCode(fs, CodeConflictingState) {
		t.Fatalf("consistent states reported: %+v", fs)
	}
}

func TestRunSeriesSingleWorkUnchanged(t *testing.T) {
	root, ws := newSeries(t)
	single, _ := Run(root, ws[0], ws[0].Root)
	series, _ := RunSeries(root, ws[:1])
	if len(single) != len(series) {
		t.Fatalf("single = %+v series = %+v", single, series)
	}
}

// sceneUnit 返回作品 w 中引用为 ref 的单元。
func sceneUnit(t *testing.T, w *work.Work, ref string) *work.Unit {
	t.Helper()
	for _, u := range w.Units() {
		if u.Ref == ref {
			return u
		}
	}
	t.Fatalf("unit %s not found", ref)
	return nil
}

// 跨作品终态人物出场：book-1 已确认 canon-1 死亡，book-2/s1 仍出场守灯人。
func TestRunSeriesDeadActing(t *testing.T) {
	root, ws := newSeries(t)
	dead := func(status string) {
		writeAt2(t, root, ".writing-anything/works/book-1/state.yaml", `schema_version: 1
events:
  - {id: state-1, scene: book-1/s1, entry: canon-1, state: 受伤, status: confirmed, confirmation: 作者确认, decided_at: "2026-10-04T08:00:00Z"}
  - {id: state-2, scene: book-1/s1, entry: canon-1, state: 死亡, status: `+status+`, confirmation: 作者确认, decided_at: "2026-10-04T09:00:00Z"}
`)
	}
	dead("confirmed")
	fs, _ := RunSeries(root, ws)
	var got []Finding
	for _, f := range fs {
		if f.Code == CodeDeadActing {
			got = append(got, f)
		}
	}
	if len(got) != 1 || got[0].Scene != "book-2/s1" || got[0].Severity != "error" ||
		!strings.Contains(got[0].Message, "守灯人") || !strings.Contains(got[0].Message, "book-1") || !strings.Contains(got[0].Message, "死亡") {
		t.Fatalf("dead-acting = %+v", got)
	}
	// 单作品检查看不到前作状态。
	if single, _ := Run(root, ws[1], ws[1].Root); hasCode(single, CodeDeadActing) {
		t.Fatalf("single-work findings = %+v", single)
	}
	// 只有 proposed 的终态不判。
	dead("proposed")
	if fs, _ = RunSeries(root, ws); hasCode(fs, CodeDeadActing) {
		t.Fatalf("proposed state reported: %+v", fs)
	}
	// 本作品在该场景之前已有已确认状态时以本作品为准（由单作品检查负责）。
	dead("confirmed")
	writeAt2(t, root, ".writing-anything/works/book-2/units/s0/outline.yaml", "schema_version: 1\nsections:\n  - {id: sc1, title: 场景, pov: 守灯人}\n")
	if _, _, ps := work.AddUnit(root, "book-2", "scene", "s0", work.Anchor{Sibling: "s1"}); len(ps) > 0 {
		t.Fatal(ps)
	}
	b2, ps := work.Load(root, "book-2")
	if len(ps) > 0 {
		t.Fatal(ps)
	}
	writeAt2(t, root, ".writing-anything/works/book-2/state.yaml", `schema_version: 1
events:
  - {id: state-1, scene: book-2/s0, entry: canon-1, state: 复活, status: confirmed, confirmation: 作者确认, decided_at: "2026-10-04T08:00:00Z"}
`)
	fs, _ = RunSeries(root, []*work.Work{ws[0], b2})
	for _, f := range fs {
		if f.Code == CodeDeadActing && f.Scene == "book-2/s1" {
			t.Fatalf("revived character reported: %+v", f)
		}
	}
}

// 验收门禁：系列成员场景的阻塞项含涉及该场景的跨作品发现（归属改写为当前场景），不属于系列的作品不受影响。
func TestSeriesGateBlockers(t *testing.T) {
	root, ws := newSeries(t)
	gate := SeriesGateFindings(root, ws[1])
	if !hasCode(gate, timeline.CodeSameTime) {
		t.Fatalf("gate findings = %+v", gate)
	}
	for _, ref := range []string{"book-1/s1", "book-2/s1"} {
		w := ws[0]
		if strings.HasPrefix(ref, "book-2") {
			w = ws[1]
		}
		bs, fatal := Blockers(root, w, sceneUnit(t, w, ref))
		if fatal != nil || !slices.ContainsFunc(bs, func(p workspace.Problem) bool { return p.Code == timeline.CodeSameTime }) {
			t.Fatalf("%s blockers = %+v fatal = %+v", ref, bs, fatal)
		}
	}
	// 豁免键为（当前场景， 错误码）：AcceptanceBlockers 生成决策，作者允许后不再阻塞。
	u := sceneUnit(t, ws[1], "book-2/s1")
	bs, changed, ps := AcceptanceBlockers(root, ws[1], u)
	if ps != nil || len(changed) != 1 || len(bs) == 0 {
		t.Fatalf("AcceptanceBlockers = %+v %v %+v", bs, changed, ps)
	}
	ds, _, _ := decision.Load(root)
	found := false
	for _, d := range ds {
		if d.Question == Question("book-2/s1", timeline.CodeSameTime, "") {
			found = true
		}
	}
	if !found {
		t.Fatalf("decisions = %+v", ds)
	}
	// 修正后不再有系列阻塞项。
	writeAt2(t, root, ".writing-anything/works/book-2/units/s1/outline.yaml",
		"schema_version: 1\nsections:\n  - {id: sc1, title: 场景, pov: 守灯人, characters: [守灯人], time: 第一日, after: [book-1/s1]}\n")
	if gate = SeriesGateFindings(root, ws[1]); len(gate) != 0 {
		t.Fatalf("after fix gate = %+v", gate)
	}
	// 系列文件无法加载（缺少必需字段 title）：按不属于系列处理。
	writeAt2(t, root, ".writing-anything/series/guide.yaml", "schema_version: 1\nid: guide\nworks: [book-1, book-2]\npacks: []\nsources: []\nfacts: []\n")
	writeAt2(t, root, ".writing-anything/works/book-2/units/s1/outline.yaml",
		"schema_version: 1\nsections:\n  - {id: sc1, title: 场景, pov: 守灯人, characters: [守灯人], time: 第一日}\n")
	if gate = SeriesGateFindings(root, ws[1]); gate != nil {
		t.Fatalf("broken series gate = %+v", gate)
	}
	// 不属于系列的作品：没有系列范围发现。
	writeAt2(t, root, ".writing-anything/series/guide.yaml", "schema_version: 1\nid: guide\ntitle: 指南\nworks: [book-1]\npacks: []\nsources: []\nfacts: []\n")
	if gate = SeriesGateFindings(root, ws[1]); gate != nil {
		t.Fatalf("non-member gate = %+v", gate)
	}
	if gate = SeriesGateFindings(root, ws[0]); gate != nil {
		t.Fatalf("single-member series gate = %+v", gate)
	}
}

func TestMentions(t *testing.T) {
	for _, c := range []struct {
		msg, ref string
		want     bool
	}{
		{"场景 book-2/s1 与", "book-2/s1", true},
		{"场景 book-2/s10 与", "book-2/s1", false},
		{"场景 xbook-2/s1 与", "book-2/s1", false},
		{"book-2/s10、book-2/s1", "book-2/s1", true},
		{"book-2/s1", "book-2/s1", true},
		{"无", "book-2/s1", false},
	} {
		if got := mentions(c.msg, c.ref); got != c.want {
			t.Errorf("mentions(%q, %q) = %v", c.msg, c.ref, got)
		}
	}
}
