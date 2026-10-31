package handoff

import (
	"os"
	"path/filepath"
	"strconv"
	"strings"
	"testing"
	"time"

	"github.com/kw12121212/writing-anything/internal/workspace"
)

func newWorkspace(t *testing.T) string {
	t.Helper()
	root := t.TempDir()
	if _, ps := workspace.Init(root, nil); len(ps) > 0 {
		t.Fatal(ps)
	}
	return root
}

func writeAt(t *testing.T, root, rel, content string) {
	t.Helper()
	if err := os.WriteFile(filepath.Join(root, filepath.FromSlash(rel)), []byte(content), 0o644); err != nil {
		t.Fatal(err)
	}
}

func in(done, todo string) View {
	return View{Done: []string{done}, Todo: []string{todo}, Instructions: []string{}, Risks: []string{}}
}

func TestWriteAndShow(t *testing.T) {
	root := newWorkspace(t)
	// 首条：创建文件。
	v, changed, ps := Write(root, in("完成场景一", "场景二待收口"), time.Now())
	if ps != nil {
		t.Fatal(ps)
	}
	if v.ID != "handoff-1" || v.WrittenAt == "" {
		t.Fatalf("view = %+v", v)
	}
	if len(changed) != 1 || changed[0] != Path() {
		t.Fatalf("changed = %v", changed)
	}
	// 第二条：追加并保留注释。
	data, _ := os.ReadFile(filepath.Join(root, Path()))
	os.WriteFile(filepath.Join(root, Path()), append([]byte("# 作者备注\n"), data...), 0o644)
	if _, _, ps = Write(root, in("场景二收口", "场景三草拟"), time.Now()); ps != nil {
		t.Fatal(ps)
	}
	after, _ := os.ReadFile(filepath.Join(root, Path()))
	if !strings.Contains(string(after), "作者备注") || !strings.Contains(string(after), "handoff-2") {
		t.Fatalf("file lost comment or second note")
	}
	// Show：倒序最近在前。
	notes, ps := Show(root, 2)
	if ps != nil {
		t.Fatal(ps)
	}
	if len(notes) != 2 || notes[0].ID != "handoff-2" || notes[1].ID != "handoff-1" {
		t.Fatalf("notes = %+v", notes)
	}
	// VisibleLatest：未关联记录对所有作品可见。
	for _, workID := range []string{"", "my-novel", "other"} {
		if latest := VisibleLatest(notes, workID); latest == nil || latest.ID != "handoff-2" {
			t.Fatalf("VisibleLatest(%q) = %+v", workID, latest)
		}
	}
}

func TestWriteCap(t *testing.T) {
	root := newWorkspace(t)
	var sb strings.Builder
	sb.WriteString("schema_version: 1\nnotes:\n")
	for i := 1; i <= MaxNotes; i++ {
		sb.WriteString("  - {id: handoff-" + itoa(i) + ", written_at: \"2026-10-04T08:00:00Z\", done: [x], todo: [y]}\n")
	}
	writeAt(t, root, Path(), sb.String())
	before, _ := os.ReadFile(filepath.Join(root, Path()))
	if _, _, ps := Write(root, in("再多一条", "会被拒绝"), time.Now()); ps == nil || ps[0].Code != CodeTooMany {
		t.Fatalf("ps = %+v", ps)
	}
	after, _ := os.ReadFile(filepath.Join(root, Path()))
	if string(before) != string(after) {
		t.Fatal("file changed on cap rejection")
	}
}

func itoa(n int) string { return strconv.Itoa(n) }

func TestShowEmpty(t *testing.T) {
	root := newWorkspace(t)
	notes, ps := Show(root, 1)
	if ps != nil || len(notes) != 0 {
		t.Fatalf("notes = %+v ps = %+v", notes, ps)
	}
	if notes, ps := ShowFor(root, "my-novel", 1); ps != nil || len(notes) != 0 {
		t.Fatalf("ShowFor notes = %+v ps = %+v", notes, ps)
	}
	if latest := VisibleLatest(notes, "my-novel"); latest != nil {
		t.Fatalf("latest = %+v", latest)
	}
}

// 交接记录可选关联作品：work 仅在非空时写入；ShowFor 过滤含未关联记录；VisibleLatest 取关联与未关联中较新者。
func TestWorkAssociation(t *testing.T) {
	root := newWorkspace(t)
	first := in("完成场景一", "w1 待办")
	first.Work = "w-one"
	if v, _, ps := Write(root, first, time.Now()); ps != nil || v.Work != "w-one" {
		t.Fatalf("write with work: v = %+v ps = %+v", v, ps)
	}
	data, _ := os.ReadFile(filepath.Join(root, Path()))
	if !strings.Contains(string(data), "work: w-one") {
		t.Fatalf("work key missing: %s", data)
	}
	// 未关联条目不写 work 键。
	if _, _, ps := Write(root, in("全工作区收尾", "跨作品待办"), time.Now()); ps != nil {
		t.Fatal(ps)
	}
	data, _ = os.ReadFile(filepath.Join(root, Path()))
	if strings.Count(string(data), "work:") != 1 {
		t.Fatalf("unassociated note should not add a work key: %s", data)
	}
	// 再写一条 w-one 的记录（最新）。
	third := in("继续 w1", "w1 新待办")
	third.Work = "w-one"
	Write(root, third, time.Now())

	// ShowFor：关联该作品的与未关联的，倒序；作品过滤对另一作品不可见。
	notes, ps := ShowFor(root, "w-one", 10)
	if ps != nil {
		t.Fatal(ps)
	}
	if len(notes) != 3 || notes[0].ID != "handoff-3" || notes[1].ID != "handoff-2" || notes[2].ID != "handoff-1" || notes[1].Work != "" {
		t.Fatalf("ShowFor(w-one) = %+v", notes)
	}
	if notes, ps = ShowFor(root, "w-two", 10); ps != nil || len(notes) != 1 || notes[0].ID != "handoff-2" {
		t.Fatalf("ShowFor(w-two) = %+v ps = %+v", notes, ps)
	}
	// Show 不带作品：全部记录。
	if notes, ps = Show(root, 10); ps != nil || len(notes) != 3 {
		t.Fatalf("Show = %+v ps = %+v", notes, ps)
	}

	// VisibleLatest：w-one 可见的最新是自己的 handoff-3；w-two 只见未关联的 handoff-2。
	if latest := VisibleLatest(notes, "w-one"); latest == nil || latest.ID != "handoff-3" || latest.Todo[0] != "w1 新待办" {
		t.Fatalf("VisibleLatest(w-one) = %+v", latest)
	}
	if latest := VisibleLatest(notes, "w-two"); latest == nil || latest.ID != "handoff-2" {
		t.Fatalf("VisibleLatest(w-two) = %+v", latest)
	}
	// 未关联记录较新时对关联作品同样可见（取较新者）。
	writeAt(t, root, Path(), "schema_version: 1\nnotes:\n"+
		"  - {id: handoff-1, written_at: \"2026-10-04T08:00:00Z\", work: w-one, done: [x], todo: [旧]}\n"+
		"  - {id: handoff-2, written_at: \"2026-10-04T09:00:00Z\", done: [x], todo: [新]}\n")
	all, ps := Show(root, 10)
	if ps != nil {
		t.Fatal(ps)
	}
	if latest := VisibleLatest(all, "w-one"); latest == nil || latest.ID != "handoff-2" || latest.Todo[0] != "新" {
		t.Fatalf("newer unassociated should win: %+v", latest)
	}
}

func TestParseInput(t *testing.T) {
	// 干净收尾：省略 todo 或给空数组都合法，todo 归一为空数组；done 仍必填。
	v, ps := ParseInput([]byte("done: [x]\n"))
	if ps != nil || len(v.Done) != 1 || v.Todo == nil || len(v.Todo) != 0 {
		t.Fatalf("no todo: v = %+v ps = %+v", v, ps)
	}
	if v, ps = ParseInput([]byte("done: [x]\ntodo: []\n")); ps != nil || v.Todo == nil || len(v.Todo) != 0 {
		t.Fatalf("empty todo: v = %+v ps = %+v", v, ps)
	}
	if v, ps = ParseInput([]byte("done: [x]\ntodo: [y]\n")); ps != nil || len(v.Done) != 1 || len(v.Todo) != 1 || v.Instructions == nil {
		t.Fatalf("v = %+v ps = %+v", v, ps)
	}
	if v, ps = ParseInput([]byte("done: [x]\nwork: my-novel\n")); ps != nil || v.Work != "my-novel" {
		t.Fatalf("work input: v = %+v ps = %+v", v, ps)
	}
	if _, ps = ParseInput([]byte("todo: [y]\n")); ps == nil {
		t.Fatal("missing done accepted")
	}
}

func TestCheckArtifactAndDomain(t *testing.T) {
	root := newWorkspace(t)
	writeAt(t, root, Path(), "schema_version: 1\nnotes:\n  - {id: handoff-2, written_at: \"2026-10-04T08:00:00Z\", done: [x], todo: [y]}\n")
	if ps := CheckArtifact(root, Path()); len(ps) == 0 || ps[0].Code != "artifact.duplicate-id" {
		t.Fatalf("ps = %+v", ps)
	}
	if ps := CheckArtifact(root, "other.yaml"); ps != nil {
		t.Fatalf("ps = %+v", ps)
	}
}
