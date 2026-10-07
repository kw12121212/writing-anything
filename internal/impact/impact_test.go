package impact

import (
	"os"
	"path/filepath"
	"strings"
	"testing"
	"time"

	"github.com/kw12121212/writing-anything/internal/draft"
	"github.com/kw12121212/writing-anything/internal/review"
	"github.com/kw12121212/writing-anything/internal/work"
	"github.com/kw12121212/writing-anything/internal/workspace"
)

// newNovel 初始化 novel 工作区：chapter-one{scene-1,scene-2,scene-3}。scene-1 声明事件 ev-1；
// scene-2 的起因引用 ev-1 且声明 ev-2；scene-3 的起因引用 ev-2（两层链）。canon-1 守灯人出场于全部场景。
func newNovel(t *testing.T) (string, *work.Work) {
	t.Helper()
	root := t.TempDir()
	if _, ps := workspace.Init(root, nil); len(ps) > 0 {
		t.Fatal(ps)
	}
	var w *work.Work
	var ps []workspace.Problem
	if w, _, ps = work.Create(root, "novel", "my-novel"); len(ps) > 0 {
		t.Fatal(ps)
	}
	for _, add := range []struct{ parent, typ, id string }{
		{"my-novel", "part", "part-one"},
		{"my-novel/part-one", "chapter", "chapter-one"},
		{"my-novel/chapter-one", "scene", "scene-1"},
		{"my-novel/chapter-one", "scene", "scene-2"},
		{"my-novel/chapter-one", "scene", "scene-3"},
	} {
		if _, _, ps = work.AddUnit(root, add.parent, add.typ, add.id, work.Anchor{}); len(ps) > 0 {
			t.Fatal(ps)
		}
	}
	writeAt(t, root, ".writing-anything/works/my-novel/canon.yaml", `schema_version: 1
entries:
  - {id: canon-1, type: character, name: 守灯人, text: 五十岁, sources: [], protection: normal}
`)
	writeAt(t, root, ".writing-anything/works/my-novel/units/scene-1/outline.yaml", `schema_version: 1
sections:
  - {id: sc1, title: 场景, pov: 守灯人, characters: [守灯人], events: [ev-1]}
`)
	writeAt(t, root, ".writing-anything/works/my-novel/units/scene-2/outline.yaml", `schema_version: 1
sections:
  - {id: sc1, title: 场景, pov: 守灯人, characters: [守灯人], causes: [ev-1], events: [ev-2]}
`)
	writeAt(t, root, ".writing-anything/works/my-novel/units/scene-3/outline.yaml", `schema_version: 1
sections:
  - {id: sc1, title: 场景, pov: 守灯人, characters: [守灯人], causes: [ev-2]}
`)
	for _, s := range []string{"scene-1", "scene-2", "scene-3"} {
		writeAt(t, root, ".writing-anything/works/my-novel/units/"+s+"/draft.md", "正文。\\n")
	}
	if w, ps = work.Load(root, "my-novel"); len(ps) > 0 {
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

func scenesOf(as []Affected) []string {
	out := []string{}
	for _, a := range as {
		out = append(out, a.Scene)
	}
	return out
}

func containsStr(vs []string, v string) bool {
	for _, x := range vs {
		if x == v {
			return true
		}
	}
	return false
}

func TestComputeEventChain(t *testing.T) {
	root, w := newNovel(t)
	// 修改 scene-1：后续场景 scene-2（引用 ev-1）；一层传递使 scene-3（引用 ev-2）也命中。
	as, ps := Compute(root, w, "my-novel/scene-1")
	if ps != nil {
		t.Fatal(ps)
	}
	if !containsStr(scenesOf(as), "my-novel/scene-2") || !containsStr(scenesOf(as), "my-novel/scene-3") {
		t.Fatalf("affected = %+v", as)
	}
	// 修改事件 ev-1：同样命中 scene-2 与 scene-3。
	as, _ = Compute(root, w, "ev-1")
	if !containsStr(scenesOf(as), "my-novel/scene-2") {
		t.Fatalf("event target affected = %+v", as)
	}
	// 修改 canon-1：三个后续场景（对 scene-1 而言）都出场守灯人。
	as, _ = Compute(root, w, "canon-1")
	if len(as) != 3 {
		t.Fatalf("canon target affected = %+v", as)
	}
	for _, a := range as {
		if a.Via != ViaCharacter {
			t.Fatalf("via = %+v", a)
		}
	}
	// 更早场景不受影响：修改 scene-3 无后续。
	as, _ = Compute(root, w, "my-novel/scene-3")
	if len(as) != 0 {
		t.Fatalf("latest scene affected = %+v", as)
	}
	// 未知目标。
	if _, ps := Compute(root, w, "nope"); ps == nil || ps[0].Code != CodeUnknownTarget {
		t.Fatalf("ps = %+v", ps)
	}
}

func TestComputeThread(t *testing.T) {
	root, w := newNovel(t)
	writeAt(t, root, ".writing-anything/works/my-novel/threads.yaml", `schema_version: 1
entries:
  - {id: f-1, text: 灯芯的来历, planted_in: my-novel/scene-1, planned_recover: my-novel/scene-3, status: open}
`)
	w, _ = work.Load(root, "my-novel")
	as, ps := Compute(root, w, "f-1")
	if ps != nil {
		t.Fatal(ps)
	}
	if !containsStr(scenesOf(as), "my-novel/scene-3") {
		t.Fatalf("thread affected = %+v（回收场景应命中）", as)
	}
}

func TestMarkIdempotentAndPackets(t *testing.T) {
	root, w := newNovel(t)
	marked, changed, ps := MarkTarget(root, w, "my-novel/scene-1", 16000, time.Now())
	if ps != nil {
		t.Fatal(ps)
	}
	if len(marked) != 2 {
		t.Fatalf("marked = %+v", marked)
	}
	found := false
	for _, c := range changed {
		if strings.HasSuffix(c, "rechecks/my-novel.yaml") {
			found = true
		}
	}
	if !found {
		t.Fatalf("changedPaths missing rechecks: %v", changed)
	}
	// 任务包联动：causality 包生成。
	pk := false
	for _, c := range changed {
		if strings.Contains(c, "packets/my-novel/causality-") {
			pk = true
		}
	}
	if !pk {
		t.Fatalf("changedPaths missing packets: %v", changed)
	}
	// 幂等：再次 mark 不新增。
	if marked2, changed2, _ := MarkTarget(root, w, "my-novel/scene-1", 16000, time.Now()); len(marked2) != 0 || len(changed2) != 0 {
		t.Fatalf("second mark = %+v %v", marked2, changed2)
	}
	// 无受影响目标不写文件。
	if m3, c3, _ := MarkTarget(root, w, "my-novel/scene-3", 16000, time.Now()); m3 != nil || c3 != nil {
		t.Fatalf("no-op mark = %+v %v", m3, c3)
	}
}

func TestClearRequiresConfirmationOrReview(t *testing.T) {
	root, w := newNovel(t)
	if _, _, ps := MarkTarget(root, w, "my-novel/scene-1", 16000, time.Now()); ps != nil {
		t.Fatal(ps)
	}
	// 无确认、无合格审稿：拒绝。
	if _, _, ps := Clear(root, w, "my-novel/scene-2", "my-novel/scene-1", "", time.Now()); ps == nil || ps[0].Code != CodeIncompleteConfirmation {
		t.Fatalf("ps = %+v", ps)
	}
	// 作者确认解除。
	m, changed, ps := Clear(root, w, "my-novel/scene-2", "my-novel/scene-1", "作者确认无影响", time.Now())
	if ps != nil {
		t.Fatal(ps)
	}
	if m == nil || m.Status != "cleared" || m.Confirmation == nil || *m.Confirmation != "作者确认无影响" {
		t.Fatalf("cleared = %+v", m)
	}
	if len(changed) != 1 {
		t.Fatalf("changed = %v", changed)
	}
	// 再次解除同目标：not-found。
	if _, _, ps := Clear(root, w, "my-novel/scene-2", "my-novel/scene-1", "再确认", time.Now()); ps == nil || ps[0].Code != CodeNotFound {
		t.Fatalf("ps = %+v", ps)
	}
}

func byRefUnitForTest(w *work.Work, ref string) *work.Unit {
	for _, u := range w.Units() {
		if u.Ref == ref {
			return u
		}
	}
	return nil
}

func TestClearReviewBasedAndOpenMarks(t *testing.T) {
	root, w := newNovel(t)
	if _, _, ps := MarkTarget(root, w, "my-novel/scene-1", 16000, time.Now()); ps != nil {
		t.Fatal(ps)
	}
	if marks := OpenMarks(root, "my-novel"); len(marks) != 2 {
		t.Fatalf("open marks = %+v", marks)
	}
	// scene-2 写基于当前正文的合格审稿（无 fail）→ 免作者确认解除（review_based）。
	content, ok, dps := draft.Read(root, w, byRefUnitForTest(w, "my-novel/scene-2"))
	if len(dps) > 0 || !ok {
		t.Fatalf("draft read: %+v ok=%v", dps, ok)
	}
	sum := review.SHA256(content)
	writeAt(t, root, ".writing-anything/works/my-novel/units/scene-2/review.yaml",
		"schema_version: 1\ndraft_sha256: "+sum+"\nrubric: []\nfindings: []\n")
	m, changed, ps := Clear(root, w, "my-novel/scene-2", "my-novel/scene-1", "", time.Now())
	if ps != nil {
		t.Fatalf("review-based clear: %+v", ps)
	}
	if m == nil || m.Status != "cleared" || m.ReviewBased == nil || !*m.ReviewBased {
		t.Fatalf("cleared = %+v", m)
	}
	if len(changed) != 1 {
		t.Fatalf("changed = %v", changed)
	}
	if marks := OpenMarks(root, "my-novel"); len(marks) != 1 {
		t.Fatalf("open marks after clear = %+v", marks)
	}
	// scene-3 没有合格审稿：仍需作者确认。
	if _, _, ps := Clear(root, w, "my-novel/scene-3", "my-novel/scene-1", "", time.Now()); ps == nil || ps[0].Code != CodeIncompleteConfirmation {
		t.Fatalf("ps = %+v", ps)
	}
}

func TestListAndCheckWorks(t *testing.T) {
	root, w := newNovel(t)
	if marks, ps := List(root, "my-novel", false); ps != nil || len(marks) != 0 {
		t.Fatalf("empty list = %+v %+v", marks, ps)
	}
	MarkTarget(root, w, "my-novel/scene-1", 16000, time.Now())
	marks, ps := List(root, "my-novel", false)
	if ps != nil || len(marks) != 2 {
		t.Fatalf("list = %+v %+v", marks, ps)
	}
	if all, _ := List(root, "my-novel", true); len(all) != 2 {
		t.Fatalf("all = %+v", all)
	}
	// 手写非法：open 带 cleared_at。
	writeAt(t, root, ".writing-anything/rechecks/my-novel.yaml", `schema_version: 1
scenes:
  - {scene: my-novel/scene-2, target: my-novel/scene-1, via: cause-event, reason: r, marked_at: "2026-10-04T08:00:00Z", status: open, cleared_at: "2026-10-04T09:00:00Z"}
`)
	w2, _ := work.Load(root, "my-novel")
	_, problems := CheckWorks(root, []*work.Work{w2})
	if len(problems) == 0 || problems[0].Code != CodeInvalid {
		t.Fatalf("problems = %+v", problems)
	}
}

// 两个受影响场景的任务包都保留（逐场景生成不互相删除）；预算放不下时报告 packet.over-budget，复查标记不写。
func TestMarkPacketsPerSceneAndOverBudget(t *testing.T) {
	root, w := newNovel(t)
	if _, _, ps := MarkTarget(root, w, "my-novel/scene-1", 5, time.Now()); len(ps) != 1 || ps[0].Code != "packet.over-budget" {
		t.Fatalf("over budget = %+v", ps)
	}
	if _, err := os.Stat(filepath.Join(root, ".writing-anything", "rechecks", "my-novel.yaml")); err == nil {
		t.Fatal("rechecks written although packets failed")
	}
	marked, changed, ps := MarkTarget(root, w, "my-novel/scene-1", 16000, time.Now())
	if ps != nil || len(marked) != 2 {
		t.Fatalf("mark = %+v %+v", marked, ps)
	}
	entries, _ := os.ReadDir(filepath.Join(root, ".writing-anything", "packets", "my-novel"))
	if len(entries) != 2 {
		t.Fatalf("packets = %d, changed = %v", len(entries), changed)
	}
}
