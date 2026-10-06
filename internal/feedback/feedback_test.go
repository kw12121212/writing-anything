package feedback

import (
	"os"
	"path/filepath"
	"slices"
	"strings"
	"testing"
	"time"

	"github.com/kw12121212/writing-anything/internal/draft"
	"github.com/kw12121212/writing-anything/internal/work"
	"github.com/kw12121212/writing-anything/internal/workspace"
)

// newWork 返回已初始化的临时工作区，其中有 minimal 文体的作品 my-post 与子单元 intro。
func newWork(t *testing.T) string {
	t.Helper()
	root := t.TempDir()
	if _, ps := workspace.Init(root, nil); len(ps) > 0 {
		t.Fatal(ps)
	}
	if _, _, ps := work.Create(root, "minimal", "my-post"); len(ps) > 0 {
		t.Fatal(ps)
	}
	if _, _, ps := work.AddUnit(root, "my-post", "section", "intro", work.Anchor{}); len(ps) > 0 {
		t.Fatal(ps)
	}
	return root
}

func unit(t *testing.T, root, ref string) (*work.Work, *work.Unit) {
	t.Helper()
	w, u, ps := work.Resolve(root, ref)
	if len(ps) > 0 {
		t.Fatal(ps)
	}
	return w, u
}

const rootDraft = ".writing-anything/works/my-post/draft.md"
const rootFeedback = ".writing-anything/works/my-post/feedback.yaml"

func write(t *testing.T, root, rel, content string) {
	t.Helper()
	if err := os.WriteFile(filepath.Join(root, filepath.FromSlash(rel)), []byte(content), 0o644); err != nil {
		t.Fatal(err)
	}
}

func read(t *testing.T, root, rel string) string {
	t.Helper()
	data, err := os.ReadFile(filepath.Join(root, filepath.FromSlash(rel)))
	if err != nil {
		t.Fatal(err)
	}
	return string(data)
}

func TestAddAnchored(t *testing.T) {
	root := newWork(t)
	w, u := unit(t, root, "my-post")
	write(t, root, rootDraft, "第一段讲背景。\r\n\r\n第二段讲方案。")
	it, changed, ps := Add(root, w, u, "must-fix", "方案要给出数据", "讲方案", "")
	if len(ps) > 0 {
		t.Fatal(ps)
	}
	if it.ID != "fb-1" || it.Kind != "must-fix" || it.Status != "open" || it.Unit != "my-post" || !slices.Equal(changed, []string{rootFeedback}) {
		t.Fatalf("item = %+v, changed = %v", it, changed)
	}
	a := it.Anchor
	if a.Quote != "讲方案" || !strings.HasSuffix(a.Before, "第二段") || a.After != "。" || a.Paragraph != draft.Split([]byte("第二段讲方案。"))[0].Hash {
		t.Errorf("anchor = %+v", a)
	}
	if it.Location != (Location{StateLocated, 3, 2}) {
		t.Errorf("location = %+v", it.Location)
	}
	// 前后文至多 30 个字符。
	write(t, root, rootDraft, strings.Repeat("前", 40)+"目标"+strings.Repeat("后", 40))
	it, _, _ = Add(root, w, u, "", "x", "目标", "")
	if len([]rune(it.Anchor.Before)) != 30 || len([]rune(it.Anchor.After)) != 30 || it.Kind != "suggestion" || it.ID != "fb-2" {
		t.Errorf("context = %+v", it)
	}
}

func TestAddUnitLevelAndChild(t *testing.T) {
	root := newWork(t)
	w, u := unit(t, root, "my-post/intro")
	it, changed, ps := Add(root, w, u, "question", "整体太长", "", "")
	dir := ".writing-anything/works/my-post/units/intro"
	if len(ps) > 0 || it.Anchor != nil || it.State != StateNone || !slices.Equal(changed, []string{dir, dir + "/feedback.yaml"}) {
		t.Fatalf("item = %+v, changed = %v, %v", it, changed, ps)
	}
}

func TestAddRejects(t *testing.T) {
	root := newWork(t)
	w, u := unit(t, root, "my-post")
	if _, _, ps := Add(root, w, u, "", "x", "方案", ""); len(ps) != 1 || ps[0].Code != "draft.not-found" {
		t.Fatalf("no draft: %v", ps)
	}
	write(t, root, rootDraft, "方案一。\n\n方案二。")
	if _, _, ps := Add(root, w, u, "", "x", "dup", "fb-1"); len(ps) != 1 || ps[0].Code != "feedback.quote-not-found" {
		t.Fatalf("missing quote: %v", ps)
	}
	if _, _, ps := Add(root, w, u, "", "x", "", "fb-1"); len(ps) > 0 {
		t.Fatal(ps)
	}
	before := read(t, root, rootFeedback)
	for _, tc := range []struct {
		name, kind, text, quote, id string
		code, field                 string
	}{
		{"ambiguous", "", "x", "方案", "", "feedback.ambiguous-quote", ""},
		{"bad kind", "rumor", "x", "", "", "artifact.schema-violation", "kind"},
		{"blank text", "", " ", "", "", "artifact.schema-violation", "text"},
		{"missing text", "", "", "", "", "artifact.schema-violation", "text"},
		{"bad id", "", "x", "", "Bad", "artifact.schema-violation", "id"},
		{"duplicate id", "", "x", "", "fb-1", "artifact.duplicate-id", "id"},
	} {
		_, changed, ps := Add(root, w, u, tc.kind, tc.text, tc.quote, tc.id)
		if len(ps) != 1 || ps[0].Code != tc.code || ps[0].Field != tc.field || changed != nil {
			t.Errorf("%s: %+v", tc.name, ps)
		}
	}
	if read(t, root, rootFeedback) != before {
		t.Fatal("feedback.yaml changed")
	}
	write(t, root, rootFeedback, "schema_version: 1\nfeedback: &f []\n")
	if _, _, ps := Add(root, w, u, "", "x", "", ""); len(ps) != 1 || ps[0].Code != "feedback.anchored-value" {
		t.Fatalf("anchored: %v", ps)
	}
	write(t, root, rootFeedback, "schema_version: 1\nfeedback: {}\n")
	if _, _, ps := Add(root, w, u, "", "x", "", ""); len(ps) == 0 || ps[0].Code != "artifact.schema-violation" {
		t.Fatalf("invalid file: %v", ps)
	}
}

func TestRelocation(t *testing.T) {
	root := newWork(t)
	w, u := unit(t, root, "my-post")
	write(t, root, rootDraft, "第一段讲背景。\n\n第二段讲方案。\n")
	if _, _, ps := Add(root, w, u, "must-fix", "x", "讲方案", ""); len(ps) > 0 {
		t.Fatal(ps)
	}
	for _, tc := range []struct {
		name, body string
		want       Location
	}{
		{"unchanged", "第一段讲背景。\n\n第二段讲方案。\n", Location{StateLocated, 3, 2}},
		{"moved", "新开头。\n\n第一段讲背景。\n\n第二段讲方案。\n", Location{StateLocated, 5, 3}},
		{"rewritten", "第一段讲背景。\n\n第二段讲设计。\n", Location{StateOrphaned, 0, 0}},
		{"duplicate after edit", "第一段也讲方案。\n\n第二段讲方案。\n", Location{StateLocated, 3, 2}},
		{"paragraph edited, context wins", "讲方案在这里。\n\n第二段讲方案，改过。\n", Location{StateLocated, 3, 2}},
	} {
		write(t, root, rootDraft, tc.body)
		items, ps := List(root, w, u)
		if len(ps) > 0 || len(items) != 1 || items[0].Location != tc.want {
			t.Errorf("%s: %+v, %v", tc.name, items, ps)
		}
	}
	os.Remove(filepath.Join(root, filepath.FromSlash(rootDraft)))
	if items, _ := List(root, w, u); items[0].State != StateOrphaned {
		t.Errorf("no draft: %+v", items[0])
	}
	if loc := Locate([]byte("x"), nil); loc.State != StateNone {
		t.Errorf("Locate(nil anchor) = %+v", loc)
	}
	// 同分取最靠前的一处。
	if loc := Locate([]byte("甲乙\n\n甲乙"), &Anchor{Quote: "甲"}); loc != (Location{StateLocated, 1, 1}) {
		t.Errorf("tie = %+v", loc)
	}
}

func TestResolve(t *testing.T) {
	root := newWork(t)
	w, u := unit(t, root, "my-post")
	Add(root, w, u, "", "意见一", "", "")
	Add(root, w, u, "", "意见二", "", "")
	write(t, root, rootFeedback, "# 作者的注释\n"+read(t, root, rootFeedback))
	it, changed, ps := Resolve(root, w, u, "fb-1", "", "已在第二段补充数据", true)
	if len(ps) > 0 || len(changed) != 1 || it.Status != "resolved" || *it.Note != "已在第二段补充数据" {
		t.Fatalf("resolve: %+v, %v, %v", it, changed, ps)
	}
	text := read(t, root, rootFeedback)
	if !strings.Contains(text, "# 作者的注释") || !strings.Contains(text, "note: 已在第二段补充数据") {
		t.Errorf("feedback.yaml:\n%s", text)
	}
	if _, changed, _ := Resolve(root, w, u, "fb-1", "resolved", "", false); len(changed) != 0 {
		t.Error("same status rewrote the file")
	}
	if _, changed, _ := Resolve(root, w, u, "fb-1", "resolved", "已在第二段补充数据", true); len(changed) != 0 {
		t.Error("same note rewrote the file")
	}
	if it, changed, _ := Resolve(root, w, u, "fb-1", "open", "重开", true); len(changed) != 1 || it.Status != "open" || *it.Note != "重开" {
		t.Errorf("reopen = %+v", it)
	}
	for _, tc := range []struct{ id, status, code, field string }{
		{"fb-9", "", "feedback.not-found", ""},
		{"fb-1", "done", "artifact.schema-violation", "status"},
	} {
		if _, _, ps := Resolve(root, w, u, tc.id, tc.status, "", false); len(ps) != 1 || ps[0].Code != tc.code || ps[0].Field != tc.field {
			t.Errorf("%s/%s: %v", tc.id, tc.status, ps)
		}
	}
	write(t, root, rootFeedback, "schema_version: 1\nfeedback:\n  - {id: fb-1, kind: question, status: &s open, text: x}\n")
	if _, _, ps := Resolve(root, w, u, "fb-1", "", "", false); len(ps) != 1 || ps[0].Code != "feedback.anchored-value" || ps[0].Field != "feedback[0].status" {
		t.Errorf("anchored status: %v", ps)
	}
	write(t, root, rootFeedback, "schema_version: 1\nfeedback:\n  - {id: fb-1, kind: question, status: open, text: x, note: &n a}\n")
	if _, _, ps := Resolve(root, w, u, "fb-1", "", "b", true); len(ps) != 1 || ps[0].Field != "feedback[0].note" {
		t.Errorf("anchored note: %v", ps)
	}
}

func TestListAllAndCheck(t *testing.T) {
	root := newWork(t)
	w, u := unit(t, root, "my-post")
	_, child := unit(t, root, "my-post/intro")
	Add(root, w, u, "", "根", "", "")
	Add(root, w, child, "", "子", "", "")
	w, _ = unit(t, root, "my-post")
	items, ps := ListAll(root, []*work.Work{w})
	if len(ps) > 0 || len(items) != 2 || items[0].Unit != "my-post" || items[1].Unit != "my-post/intro" {
		t.Fatalf("ListAll = %+v, %v", items, ps)
	}
	checked, ps := CheckUnitArtifacts(root, []*work.Work{w})
	if len(ps) > 0 || !slices.Equal(checked, []string{rootFeedback, ".writing-anything/works/my-post/units/intro/feedback.yaml"}) {
		t.Fatalf("checked = %v, %v", checked, ps)
	}
	write(t, root, rootFeedback, read(t, root, rootFeedback)+"  - {id: fb-1, kind: praise, status: open, text: y}\n")
	if ps := CheckArtifact(root, rootFeedback); len(ps) != 1 || ps[0].Code != "artifact.duplicate-id" || ps[0].Field != "feedback[1].id" {
		t.Errorf("duplicate: %v", ps)
	}
	write(t, root, rootFeedback, "a: [\n")
	if _, ps := ListAll(root, []*work.Work{w}); len(ps) != 1 || ps[0].Code != "artifact.parse-error" {
		t.Errorf("parse error: %v", ps)
	}
	if !Pending("open") || !Pending("accepted") || Pending("resolved") || Pending("rejected") {
		t.Error("Pending")
	}
}

func TestAddPreservesCommentsAndSkipsIDs(t *testing.T) {
	root := newWork(t)
	w, u := unit(t, root, "my-post")
	write(t, root, rootFeedback, "# 头部注释\nschema_version: 1\nfeedback: [{id: fb-2, kind: praise, status: open, text: 旧}]  # 行尾注释\n")
	it, _, ps := Add(root, w, u, "", "新", "", "")
	if len(ps) > 0 || it.ID != "fb-3" {
		t.Fatalf("add = %+v, %v", it, ps)
	}
	text := read(t, root, rootFeedback)
	for _, want := range []string{"# 头部注释", "# 行尾注释", "text: 新"} {
		if !strings.Contains(text, want) {
			t.Errorf("feedback.yaml lacks %q:\n%s", want, text)
		}
	}
	// 空的流式序列上的行尾注释同样保留。
	write(t, root, rootFeedback, "schema_version: 1\nfeedback: []  # 空列表注释\n")
	if _, _, ps := Add(root, w, u, "", "x", "", ""); len(ps) > 0 || !strings.Contains(read(t, root, rootFeedback), "# 空列表注释") {
		t.Errorf("empty flow comment lost: %v\n%s", ps, read(t, root, rootFeedback))
	}
}

func TestListReadOnlyAndDuplicates(t *testing.T) {
	root := newWork(t)
	w, u := unit(t, root, "my-post")
	write(t, root, rootDraft, "第二段讲方案。")
	Add(root, w, u, "", "x", "讲方案", "")
	before := read(t, root, rootFeedback)
	write(t, root, rootDraft, "改写了。")
	if items, ps := List(root, w, u); len(ps) > 0 || items[0].State != StateOrphaned || read(t, root, rootFeedback) != before {
		t.Fatalf("List must not write back the location: %+v, %v", items, ps)
	}
	write(t, root, rootFeedback, before+"  - {id: fb-1, kind: praise, status: open, text: y}\n")
	if items, ps := List(root, w, u); len(items) != 2 || len(ps) != 1 || ps[0].Code != "artifact.duplicate-id" {
		t.Fatalf("duplicates: %+v, %v", items, ps)
	}
}

func TestSymlinks(t *testing.T) {
	root := newWork(t)
	outside := t.TempDir()
	os.WriteFile(filepath.Join(outside, "feedback.yaml"), []byte("schema_version: 1\nfeedback:\n  - {id: fb-1, kind: praise, status: open, text: 外部}\n"), 0o644)
	units := filepath.Join(root, ".writing-anything", "works", "my-post", "units")
	os.Mkdir(units, 0o755)
	os.Symlink(outside, filepath.Join(units, "intro"))
	w, u := unit(t, root, "my-post/intro")
	if items, ps := List(root, w, u); len(items) != 0 || len(ps) != 0 {
		t.Fatalf("List through a symlink = %v, %v", items, ps)
	}
	if checked, ps := CheckUnitArtifacts(root, []*work.Work{w}); len(checked) != 0 || len(ps) != 0 {
		t.Fatalf("CheckUnitArtifacts through a symlink = %v, %v", checked, ps)
	}
	if _, _, ps := Add(root, w, u, "", "x", "", ""); len(ps) != 1 || ps[0].Code != "workspace.write-failed" {
		t.Fatalf("Add through a symlink = %v", ps)
	}
	// feedback.yaml 本身是符号链接：不读取，也不替换。
	_, rootUnit := unit(t, root, "my-post")
	os.Symlink(filepath.Join(outside, "feedback.yaml"), filepath.Join(root, filepath.FromSlash(rootFeedback)))
	if _, _, ps := Add(root, w, rootUnit, "", "x", "", ""); len(ps) != 1 || ps[0].Code != "workspace.write-failed" {
		t.Fatalf("Add over a symlinked feedback.yaml = %v", ps)
	}
	if info, _ := os.Lstat(filepath.Join(root, filepath.FromSlash(rootFeedback))); info.Mode()&os.ModeSymlink == 0 {
		t.Fatal("the symlink was replaced")
	}
}

// 多处匹配的定位耗时与正文长度成线性，不在每处匹配重新扫描前文。
func TestLocateManyMatches(t *testing.T) {
	var b strings.Builder
	for i := 0; i < 9000; i++ {
		b.WriteString("讲方案" + strings.Repeat("字", 60) + "\n\n")
	}
	start := time.Now()
	loc := Locate([]byte(b.String()), &Anchor{Quote: "讲方案", Before: strings.Repeat("字", 28) + "\n\n", After: strings.Repeat("字", 30)})
	if loc.State != StateLocated || loc.Paragraph != 2 || time.Since(start) > 2*time.Second {
		t.Fatalf("Locate = %+v in %v", loc, time.Since(start))
	}
}
