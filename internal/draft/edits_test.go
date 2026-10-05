package draft

import (
	"time"
	"os"
	"path/filepath"
	"slices"
	"strings"
	"testing"

	"github.com/kw12121212/writing-anything/internal/source"
	"github.com/kw12121212/writing-anything/internal/work"
)

func TestRecordKeepsAgentText(t *testing.T) {
	root, _ := newWork(t)
	w, u := unit(t, root, "my-post")
	if _, _, ps := Write(root, w, u, []byte("第一段。\n\n第二段。\n"), "", time.Date(2026, 6, 20, 12, 0, 0, 0, time.UTC)); len(ps) > 0 {
		t.Fatal(ps)
	}
	rec, _ := loadRecord(root, w.UnitDir(u))
	if rec.Paragraphs[0].Text != "第一段。" || rec.Paragraphs[1].Text != "第二段。" {
		t.Fatalf("record = %+v", rec.Paragraphs)
	}
	// 作者加的段落保留为 human，不记原文。
	write(t, root, ".writing-anything/works/my-post/draft.md", "第一段。\n\n第二段。\n\n作者的。\n")
	if _, _, ps := Write(root, w, u, []byte("第一段。\n\n第二段改。\n\n作者的。\n"), sha(t, root, ".writing-anything/works/my-post/draft.md"), time.Date(2026, 6, 20, 12, 0, 0, 0, time.UTC)); len(ps) > 0 {
		t.Fatal(ps)
	}
	rec, _ = loadRecord(root, w.UnitDir(u))
	if rec.Paragraphs[2].Origin != OriginHuman || rec.Paragraphs[2].Text != "" || rec.Paragraphs[1].Text != "第二段改。" {
		t.Fatalf("record = %+v", rec.Paragraphs)
	}
}

func sha(t *testing.T, root, rel string) string {
	t.Helper()
	data, err := os.ReadFile(filepath.Join(root, filepath.FromSlash(rel)))
	if err != nil {
		t.Fatal(err)
	}
	return source.Hash(data)
}

func TestEditsLogged(t *testing.T) {
	root, _ := newWork(t)
	w, u := unit(t, root, "my-post")
	draftRel, editsRel := ".writing-anything/works/my-post/draft.md", ".writing-anything/works/my-post/edits.yaml"
	Write(root, w, u, []byte("首先，我们需要赋能团队。\n\n第二段。\n"), "", time.Date(2026, 6, 20, 12, 0, 0, 0, time.UTC))
	write(t, root, draftRel, "先让团队上手。\n\n第二段。\n")
	if ps, _ := Pairs(root, w, u); len(ps) != 1 || ps[0].Before != "首先，我们需要赋能团队。" || ps[0].After != "先让团队上手。" {
		t.Fatalf("pairs = %+v", ps)
	}
	_, changed, ps := Write(root, w, u, []byte("先让团队上手。\n\n第二段改写。\n"), sha(t, root, draftRel), time.Date(2026, 6, 20, 12, 0, 0, 0, time.UTC))
	if len(ps) > 0 || !slices.Contains(changed, editsRel) {
		t.Fatalf("write: %v %+v", changed, ps)
	}
	edits, ps := LoadEdits(root, w, u)
	if len(ps) > 0 || len(edits) != 1 || edits[0].Before != "首先，我们需要赋能团队。" || edits[0].RecordedAt == "" {
		t.Fatalf("edits = %+v %+v", edits, ps)
	}
	rec, _ := loadRecord(root, w.UnitDir(u))
	if rec.Paragraphs[0].Origin != OriginEdited || rec.Paragraphs[0].Text != "" || rec.Paragraphs[1].Origin != OriginAgent || rec.Paragraphs[1].Text != "第二段改写。" {
		t.Fatalf("record = %+v", rec.Paragraphs)
	}
	// 没有新的修改：不追加、不改动 edits.yaml。
	before, _ := os.ReadFile(filepath.Join(root, editsRel))
	_, changed, _ = Write(root, w, u, []byte("先让团队上手。\n\n又一版。\n"), "", time.Date(2026, 6, 20, 12, 0, 0, 0, time.UTC))
	if slices.Contains(changed, editsRel) {
		t.Fatalf("edits rewritten: %v", changed)
	}
	if after, _ := os.ReadFile(filepath.Join(root, editsRel)); string(after) != string(before) {
		t.Fatal("edits changed")
	}
	if checked, ps := CheckEdits(root, []*work.Work{w}); len(checked) != 1 || len(ps) > 0 {
		t.Fatalf("check: %v %+v", checked, ps)
	}
}

func TestAppendEditsLimitAndDedup(t *testing.T) {
	root, _ := newWork(t)
	dir := ".writing-anything/works/my-post"
	var add []Edit
	for i := range MaxEdits + 5 {
		add = append(add, Edit{Before: "前" + strings.Repeat("x", i+1), After: "后"})
	}
	add = append(add, add[0]) // 同一对只记一次
	data, ps := appendEdits(root, dir, add, time.Date(2026, 6, 20, 12, 0, 0, 0, time.UTC))
	if len(ps) > 0 {
		t.Fatal(ps)
	}
	write(t, root, dir+"/edits.yaml", string(data))
	_, edits, _, _ := loadEdits(root, dir)
	if len(edits) != MaxEdits || edits[0].Before != "前"+strings.Repeat("x", 6) {
		t.Fatalf("edits = %d, first %q", len(edits), edits[0].Before)
	}
	if data, ps := appendEdits(root, dir, add[len(add)-3:len(add)-1], time.Date(2026, 6, 20, 12, 0, 0, 0, time.UTC)); data != nil || len(ps) > 0 {
		t.Fatalf("existing pairs appended again: %v %+v", data != nil, ps)
	}
	write(t, root, dir+"/edits.yaml", "schema_version: 1\nedits: &e []\n")
	if _, ps := appendEdits(root, dir, add[:1], time.Date(2026, 6, 20, 12, 0, 0, 0, time.UTC)); len(ps) != 1 || ps[0].Code != "draft.edits-anchored" {
		t.Fatalf("anchored: %+v", ps)
	}
	write(t, root, dir+"/edits.yaml", "edits: [")
	if _, _, _, ps := loadEdits(root, dir); len(ps) != 1 || ps[0].Code != "artifact.parse-error" {
		t.Fatalf("parse: %+v", ps)
	}
}
