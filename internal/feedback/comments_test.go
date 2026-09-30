package feedback

import (
	"fmt"
	"slices"
	"strings"
	"testing"
	"time"

	"github.com/kw12121212/writing-anything/internal/draft"
	"github.com/kw12121212/writing-anything/internal/source"
)

func TestComments(t *testing.T) {
	body := "第一段。<!-- @me: 这里要给数据 -->\r\n\r\n<!--@me(must-fix):\n  跨行的\n  批注 -->\n\n```\n<!-- @me: 示例 -->\n```\n\n<!-- @me: 这里要给数据 -->\n<!-- @me(unknown): 忽略 --><!-- @me:   -->"
	cs := Comments([]byte(body), nil)
	var got []string
	for _, c := range cs {
		got = append(got, c.Kind+"|"+c.Text)
	}
	if !slices.Equal(got, []string{"suggestion|这里要给数据", "must-fix|跨行的\n  批注", "suggestion|这里要给数据"}) {
		t.Fatalf("comments = %q", got)
	}
	if !strings.HasPrefix(cs[0].ID, "cm-") || len(cs[0].ID) != 11 || cs[2].ID != cs[0].ID+"-2" || cs[1].ID == cs[0].ID {
		t.Errorf("ids = %s %s %s", cs[0].ID, cs[1].ID, cs[2].ID)
	}
	if body[cs[1].Start:cs[1].End] != cs[1].Raw {
		t.Errorf("raw span mismatch")
	}
	// 未闭合的围栏延续到末尾。
	if cs := Comments([]byte("```\n<!-- @me: x -->"), nil); len(cs) != 0 {
		t.Errorf("unclosed fence: %v", cs)
	}
}

func TestRemoveComment(t *testing.T) {
	for _, tc := range []struct{ body, want string }{
		{"第一段。<!-- @me: 注 -->\n\n第二段。", "第一段。\n\n第二段。"},
		{"第一段。\n  <!-- @me: 注 -->  \n第二段。", "第一段。\n第二段。"},
		{"第一段。\n<!-- @me: 注 -->", "第一段。\n"},
	} {
		c := Comments([]byte(tc.body), nil)[0]
		if got := string(removeComment([]byte(tc.body), c)); got != tc.want {
			t.Errorf("%q: got %q, want %q", tc.body, got, tc.want)
		}
	}
}

func TestListCommentsAndImport(t *testing.T) {
	root := newWork(t)
	w, u := unit(t, root, "my-post")
	write(t, root, rootDraft, "第一段。\n\n第二段。<!-- @me(must-fix): 这里要给数据 -->\n")
	Add(root, w, u, "", "对话中的意见", "", "")
	items, ps := List(root, w, u)
	if len(ps) > 0 || len(items) != 2 || items[0].ID != "fb-1" || items[1].SourceOf() != "comment" || items[1].Kind != "must-fix" ||
		items[1].Location != (Location{StateLocated, 3, 2}) || items[0].SourceOf() != "conversation" {
		t.Fatalf("items = %+v, %v", items, ps)
	}
	before := read(t, root, rootFeedback)
	List(root, w, u)
	if read(t, root, rootFeedback) != before {
		t.Fatal("List wrote feedback.yaml")
	}
	changed, ps := ImportComments(root, w, u)
	if len(ps) > 0 || !slices.Equal(changed, []string{rootFeedback}) || !strings.Contains(read(t, root, rootFeedback), "source: comment") {
		t.Fatalf("import = %v, %v\n%s", changed, ps, read(t, root, rootFeedback))
	}
	// 记入后只出现一次；再次记入不写文件。
	if items, _ := List(root, w, u); len(items) != 2 {
		t.Errorf("after import = %+v", items)
	}
	if changed, _ := ImportComments(root, w, u); changed != nil {
		t.Errorf("second import = %v", changed)
	}
	// 没有正文时什么也不做。
	_, child := unit(t, root, "my-post/intro")
	if changed, ps := ImportComments(root, w, child); changed != nil || ps != nil {
		t.Errorf("no draft = %v %v", changed, ps)
	}
}

func TestResolveComment(t *testing.T) {
	root := newWork(t)
	w, u := unit(t, root, "my-post")
	draft.Write(root, w, u, []byte("Agent 第一段。\n\nAgent 第二段。\n"), "")
	body := "Agent 第一段。\n\nAgent 第二段。<!-- @me: 这里要给数据 -->\n"
	write(t, root, rootDraft, body)
	id := Comments([]byte(body), nil)[0].ID
	// 状态改为 accepted：记入但不移除批注。
	it, changed, ps := Resolve(root, w, u, id, "accepted", "", false)
	if len(ps) > 0 || it.Status != "accepted" || !slices.Equal(changed, []string{rootFeedback}) || read(t, root, rootDraft) != body {
		t.Fatalf("accept = %+v, %v, %v", it, changed, ps)
	}
	// 解决：移除批注；正文已被作者改过（加了批注），不重建写入记录。
	it, changed, ps = Resolve(root, w, u, id, "", "已补充", true)
	if len(ps) > 0 || it.Status != "resolved" || !slices.Equal(changed, []string{rootFeedback, rootDraft}) ||
		read(t, root, rootDraft) != "Agent 第一段。\n\nAgent 第二段。\n" {
		t.Fatalf("resolve = %+v, %v, %v\n%s", it, changed, ps, read(t, root, rootDraft))
	}
	if it.State != StateOrphaned {
		t.Errorf("resolved comment location = %+v", it.Location)
	}
	// Agent 以含批注的正文写入后（正文未被作者改过），解决批注时重建写入记录。
	body2 := "Agent 第一段。<!-- @me(question): 为什么 -->\n\nAgent 第二段。\n"
	draft.Write(root, w, u, []byte(body2), source.Hash([]byte(read(t, root, rootDraft))))
	id2 := Comments([]byte(body2), nil)[0].ID
	_, changed, ps = Resolve(root, w, u, id2, "rejected", "", false)
	record := ".writing-anything/works/my-post/provenance.yaml"
	if len(ps) > 0 || !slices.Equal(changed, []string{rootFeedback, rootDraft, record}) {
		t.Fatalf("reject = %v, %v", changed, ps)
	}
	if ps := draft.Guard(root, w, u, []byte("Agent 新写"), ""); ps != nil {
		t.Errorf("comment removal counted as human edit: %v", ps)
	}
	if _, _, ps := Resolve(root, w, u, "cm-00000000", "", "", false); len(ps) != 1 || ps[0].Code != "feedback.not-found" {
		t.Errorf("missing comment = %v", ps)
	}
}

// 漏写 --> 的批注不跨空行、不跨围栏，不会吞掉作者的段落；行内代码中的批注语法不是批注。
func TestCommentBoundaries(t *testing.T) {
	body := "A。<!-- @me: 忘了闭合\n\n作者的重要段落。\n\nB。<!-- @me: 真批注 -->\n\n```html\n<!-- @me: 示例 -->\n```\n\n在正文里写 `<!-- @me: 文本 -->` 即可。\n\n<!-- @me: 外层 <!-- @me: 内层 -->"
	var got []string
	for _, c := range Comments([]byte(body), nil) {
		got = append(got, c.Text)
	}
	if !slices.Equal(got, []string{"真批注", "内层"}) {
		t.Fatalf("comments = %q", got)
	}
	// 未闭合的批注后面跟着围栏：批注结束位置在围栏内，不是批注。
	if cs := Comments([]byte("A。<!-- @me: 未闭合\n```\n代码 -->\n```\n"), nil); len(cs) != 0 {
		t.Errorf("comment ending inside a fence = %v", cs)
	}
	c := Comments([]byte(body), nil)[0]
	if got := string(removeComment([]byte(body), c)); !strings.Contains(got, "作者的重要段落。") || !strings.Contains(got, "```html\n<!-- @me: 示例 -->\n```") {
		t.Errorf("removal damaged the text:\n%s", got)
	}
}

// 两条相同的批注：解决一条后，另一条仍可列出与处理；已关闭记录的 id 不再占用正文中的批注。
func TestDuplicateComments(t *testing.T) {
	root := newWork(t)
	w, u := unit(t, root, "my-post")
	draft.Write(root, w, u, []byte("A。\n\nB。\n"), "")
	write(t, root, rootDraft, "A。<!-- @me: 同一句 -->\n\nB。<!-- @me: 同一句 -->\n")
	items, _ := List(root, w, u)
	if len(items) != 2 || items[1].ID != items[0].ID+"-2" {
		t.Fatalf("items = %+v", items)
	}
	first, second := items[0].ID, items[1].ID
	if _, _, ps := Resolve(root, w, u, first, "", "", false); len(ps) > 0 {
		t.Fatal(ps)
	}
	if read(t, root, rootDraft) != "A。\n\nB。<!-- @me: 同一句 -->\n" {
		t.Fatalf("draft = %q", read(t, root, rootDraft))
	}
	items, _ = List(root, w, u)
	if len(items) != 2 || items[0].Status != "resolved" || items[1].ID != second || items[1].Status != "open" {
		t.Fatalf("after resolving the first = %+v", items)
	}
	if _, _, ps := Resolve(root, w, u, second, "", "", false); len(ps) > 0 || read(t, root, rootDraft) != "A。\n\nB。\n" {
		t.Fatalf("resolve second = %v, %q", ps, read(t, root, rootDraft))
	}
	// 作者重新写出与已解决批注相同的文字：作为新的批注反馈出现。
	write(t, root, rootDraft, "A。<!-- @me: 同一句 -->\n\nB。\n")
	items, _ = List(root, w, u)
	if len(items) != 3 || items[2].Status != "open" || items[2].ID != first+"-3" {
		t.Fatalf("reappeared = %+v", items)
	}
}

func TestImportSkipsFeedbackFileWithoutComments(t *testing.T) {
	root := newWork(t)
	w, u := unit(t, root, "my-post")
	write(t, root, rootDraft, "没有批注。")
	write(t, root, rootFeedback, "schema_version: 1\nfeedback: [{id: fb-1, kind: bogus, status: open, text: x}]\n")
	if changed, ps := ImportComments(root, w, u); changed != nil || ps != nil {
		t.Fatalf("import without comments read feedback.yaml: %v %v", changed, ps)
	}
}

// 大量批注的解析与定位耗时与正文长度成线性（workspace-artifacts/input-limits）。
func TestManyComments(t *testing.T) {
	root := newWork(t)
	w, u := unit(t, root, "my-post")
	var b strings.Builder
	for i := 0; i < 5000; i++ {
		fmt.Fprintf(&b, "句子%d。<!-- @me: 批注 %d -->", i, i)
	}
	write(t, root, rootDraft, b.String())
	start := time.Now()
	items, ps := List(root, w, u)
	if len(items) != MaxComments || len(ps) != 1 || ps[0].Code != "feedback.too-many-comments" || time.Since(start) > 3*time.Second {
		t.Fatalf("List = %d items, %v in %v", len(items), ps, time.Since(start))
	}
	if changed, ps := ImportComments(root, w, u); changed != nil || len(ps) != 1 || ps[0].Code != "feedback.too-many-comments" {
		t.Fatalf("import over the limit = %v %v", changed, ps)
	}
}

func TestManyRecordedComments(t *testing.T) {
	root := newWork(t)
	w, u := unit(t, root, "my-post")
	var b strings.Builder
	for i := 0; i < MaxComments; i++ {
		fmt.Fprintf(&b, "句子%d。<!-- @me: 批注 %d -->", i, i)
	}
	write(t, root, rootDraft, b.String())
	start := time.Now()
	if _, ps := ImportComments(root, w, u); len(ps) > 0 {
		t.Fatal(ps)
	}
	if items, _ := List(root, w, u); len(items) != MaxComments || time.Since(start) > 5*time.Second {
		t.Fatalf("recorded List = %d items in %v", len(items), time.Since(start))
	}
}
