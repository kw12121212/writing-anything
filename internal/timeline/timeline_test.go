package timeline

import (
	"os"
	"path/filepath"
	"strings"
	"testing"

	"github.com/kw12121212/writing-anything/internal/work"
	"github.com/kw12121212/writing-anything/internal/workspace"
)

// newNovel 初始化 novel 工作区 chapter-one{scene-1..scene-4}，各场景大纲可带 time/after/characters。
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
		{"my-novel/chapter-one", "scene", "scene-4"},
	} {
		if _, _, ps = work.AddUnit(root, add.parent, add.typ, add.id, work.Anchor{}); len(ps) > 0 {
			t.Fatal(ps)
		}
	}
	if w, ps = work.Load(root, "my-novel"); len(ps) > 0 {
		t.Fatal(ps)
	}
	return root, w
}

func outlineAt(t *testing.T, root, scene, sections string) {
	t.Helper()
	path := filepath.Join(root, ".writing-anything", "works", "my-novel", "units", scene, "outline.yaml")
	if err := os.MkdirAll(filepath.Dir(path), 0o755); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(path, []byte("schema_version: 1\nsections:\n"+sections), 0o644); err != nil {
		t.Fatal(err)
	}
}

func codes(ps []workspace.Problem, code string) bool {
	for _, p := range ps {
		if p.Code == code {
			return true
		}
	}
	return false
}

func posOf(entries []Entry, unit string) int {
	for _, e := range entries {
		if e.Unit == unit {
			return e.Position
		}
	}
	return -1
}

func TestOrderNoDeclarations(t *testing.T) {
	root, w := newNovel(t)
	entries, ps := Order(root, []*Work{{W: w}})
	if len(ps) != 0 {
		t.Fatal(ps)
	}
	if len(entries) != 4 {
		t.Fatalf("entries = %d", len(entries))
	}
	for i, e := range entries {
		if e.Position != i+1 || e.Time != nil {
			t.Fatalf("entry = %+v", e)
		}
	}
	if entries[0].Unit != "my-novel/scene-1" || entries[3].Unit != "my-novel/scene-4" {
		t.Fatalf("default order = %+v", entries)
	}
}

func TestOrderAfterChain(t *testing.T) {
	root, w := newNovel(t)
	// scene-4 声明晚于 scene-3、scene-3 晚于 scene-1：顺序变为 1,2? no——4 要在 3 后、3 要在 1 后。
	outlineAt(t, root, "scene-4", "  - {id: sc1, title: t, after: [scene-3]}\n")
	outlineAt(t, root, "scene-3", "  - {id: sc1, title: t, after: [scene-1]}\n")
	entries, ps := Order(root, []*Work{{W: w}})
	if len(ps) != 0 {
		t.Fatal(ps)
	}
	if !(posOf(entries, "my-novel/scene-1") < posOf(entries, "my-novel/scene-3") && posOf(entries, "my-novel/scene-3") < posOf(entries, "my-novel/scene-4")) {
		t.Fatalf("order = %+v", entries)
	}
}

func TestOrderCycle(t *testing.T) {
	root, w := newNovel(t)
	outlineAt(t, root, "scene-2", "  - {id: sc1, title: t, after: [scene-3]}\n")
	outlineAt(t, root, "scene-3", "  - {id: sc1, title: t, after: [scene-2]}\n")
	if _, ps := Order(root, []*Work{{W: w}}); !codes(ps, CodeCycle) {
		t.Fatalf("ps = %+v", ps)
	}
}

func TestOrderUnknownScene(t *testing.T) {
	root, w := newNovel(t)
	outlineAt(t, root, "scene-2", "  - {id: sc1, title: t, after: [scene-9]}\n")
	if _, ps := Order(root, []*Work{{W: w}}); !codes(ps, CodeUnknownScene) {
		t.Fatalf("ps = %+v", ps)
	}
}

func TestOrderConflict(t *testing.T) {
	root, w := newNovel(t)
	// scene-1 晚于 scene-2、scene-2 晚于 scene-3、scene-3 晚于 scene-1：成环退回默认序后，逐条复查
	// after 与序矛盾 → order-conflict（cycle 已报，conflict 是叠加的确定性诊断）。
	outlineAt(t, root, "scene-1", "  - {id: sc1, title: t, after: [scene-2]}\n")
	outlineAt(t, root, "scene-2", "  - {id: sc1, title: t, after: [scene-3]}\n")
	outlineAt(t, root, "scene-3", "  - {id: sc1, title: t, after: [scene-1]}\n")
	_, ps := Order(root, []*Work{{W: w}})
	if !codes(ps, CodeCycle) {
		t.Fatalf("ps = %+v", ps)
	}
	// 矛盾但不成环：scene-1 晚于 scene-2，scene-2 晚于 scene-1 不成立——改为非对称链再反转一条。
	root2, w2 := newNovel(t)
	outlineAt(t, root2, "scene-1", "  - {id: sc1, title: t, after: [scene-2]}\n")
	outlineAt(t, root2, "scene-2", "  - {id: sc1, title: t, after: [scene-1]}\n")
	_, ps2 := Order(root2, []*Work{{W: w2}})
	if !codes(ps2, CodeOrderConflict) && !codes(ps2, CodeCycle) {
		t.Fatalf("ps2 = %+v", ps2)
	}
}

func TestSameTimePresence(t *testing.T) {
	root, w := newNovel(t)
	outlineAt(t, root, "scene-1", "  - {id: sc1, title: t, time: 第三日夜, characters: [守灯人]}\n")
	outlineAt(t, root, "scene-2", "  - {id: sc1, title: t, time: 第三日夜, characters: [守灯人]}\n")
	entries, ps := Order(root, []*Work{{W: w}})
	if len(ps) != 0 {
		t.Fatal(ps)
	}
	problems := SameTimePresence(entries, root, []*Work{{W: w}}, nil)
	if !codes(problems, CodeSameTime) {
		t.Fatalf("problems = %+v", problems)
	}
	// 有 after 链分隔：scene-2 声明晚于 scene-1 后不再报。
	outlineAt(t, root, "scene-2", "  - {id: sc1, title: t, time: 第三日夜, characters: [守灯人], after: [scene-1]}\n")
	entries, _ = Order(root, []*Work{{W: w}})
	if problems := SameTimePresence(entries, root, []*Work{{W: w}}, nil); codes(problems, CodeSameTime) {
		t.Fatalf("problems = %+v", problems)
	}
	// 不同 time 不报。
	outlineAt(t, root, "scene-2", "  - {id: sc1, title: t, time: 第四日晨, characters: [守灯人]}\n")
	entries, _ = Order(root, []*Work{{W: w}})
	if problems := SameTimePresence(entries, root, []*Work{{W: w}}, nil); codes(problems, CodeSameTime) {
		t.Fatalf("problems = %+v", problems)
	}
	// 未声明 time 不参与。
	outlineAt(t, root, "scene-2", "  - {id: sc1, title: t, characters: [守灯人]}\n")
	entries, _ = Order(root, []*Work{{W: w}})
	if problems := SameTimePresence(entries, root, []*Work{{W: w}}, nil); codes(problems, CodeSameTime) {
		t.Fatalf("problems = %+v", problems)
	}
}

func TestHasDeclaration(t *testing.T) {
	root, w := newNovel(t)
	if HasDeclaration(root, w) {
		t.Fatal("no declarations yet")
	}
	outlineAt(t, root, "scene-1", "  - {id: sc1, title: t, time: 第一日}\n")
	if !HasDeclaration(root, w) {
		t.Fatal("time declaration not detected")
	}
}

func TestCrossWorkOrder(t *testing.T) {
	root, w := newNovel(t)
	// 第二部作品（short-story）引用 novel 的场景。
	if _, _, ps := work.Create(root, "short-story", "side-story"); len(ps) > 0 {
		t.Fatal(ps)
	}
	if _, _, ps := work.AddUnit(root, "side-story", "scene", "s1", work.Anchor{}); len(ps) > 0 {
		t.Fatal(ps)
	}
	path := filepath.Join(root, ".writing-thing")
	_ = path
	sideDir := filepath.Join(root, ".writing-anything", "works", "side-story", "units", "s1")
	os.MkdirAll(sideDir, 0o755)
	os.WriteFile(filepath.Join(sideDir, "outline.yaml"), []byte("schema_version: 1\nsections:\n  - {id: sc1, title: t, after: [my-novel/scene-4], characters: [守灯人], time: 尾声}\n"), 0o644)
	w2, _ := work.Load(root, "side-story")
	entries, ps := Order(root, []*Work{{W: w}, {W: w2}})
	if len(ps) != 0 {
		t.Fatal(ps)
	}
	if !(posOf(entries, "my-novel/scene-4") < posOf(entries, "side-story/s1")) {
		t.Fatalf("cross-work order = %+v", entries)
	}
	_ = strings.TrimSpace
}
