package continuity

import (
	"os"
	"path/filepath"
	"strings"
	"testing"

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
	writeAt2(t, root, ".writing-anything/series/guide/series.yaml", `schema_version: 1
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
