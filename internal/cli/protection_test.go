package cli

import (
	"encoding/json"
	"os"
	"slices"
	"strings"
	"testing"
)

type diffOut struct {
	result
	Diff *struct {
		Unit     string  `json:"unit"`
		Path     string  `json:"path"`
		Edited   bool    `json:"edited"`
		Base     string  `json:"base"`
		Recorded *string `json:"recorded"`
		Changes  []struct {
			Kind string  `json:"kind"`
			At   int     `json:"at"`
			Line *int    `json:"line"`
			Text *string `json:"text"`
		} `json:"changes"`
	} `json:"diff"`
}

func diffJSON(t *testing.T, dir string) (int, diffOut) {
	t.Helper()
	code, res := runUnits(t, dir, "diff", "my-post")
	var out diffOut
	if err := json.Unmarshal([]byte(res.stdout), &out); err != nil {
		t.Fatal(err)
	}
	out.result = res.result
	return code, out
}

func TestHumanEditProtection(t *testing.T) {
	dir := newWork(t)
	draft := wsPath(dir, draftPath)
	record := wsPath(dir, ".writing-anything/works/my-post/provenance.yaml")
	runSrc(t, dir, "A。\n\nB。\n\nC。\n", "draft", "write", "my-post")
	writeFile(t, draft, "A。\n\nB2。\n")
	writeFile(t, wsPath(dir, "new.md"), "新正文。\n")
	beforeDraft, beforeRecord := readFile(t, draft), readFile(t, record)
	code, res := runSrc(t, dir, "", "draft", "write", "my-post", "--file", "new.md")
	d := diag(t, res.result, "draft.human-edited")
	if code != 1 || d.Path != draftPath || d.Next[0] != shellCommand("wa-cli", "diff", "my-post", "--dir", dir, "--json") ||
		readFile(t, draft) != beforeDraft || readFile(t, record) != beforeRecord || len(res.ChangedPaths) != 0 {
		t.Fatalf("refuse-overwrite: code = %d, %+v", code, d)
	}
	// diff 列出作者的改动，并给出 base。
	code, out := diffJSON(t, dir)
	df := out.Diff
	if code != 0 || !df.Edited || df.Base != sha(beforeDraft) || df.Recorded == nil || len(df.Changes) != 2 ||
		df.Changes[0].Kind != "edited" || df.Changes[0].At != 2 || *df.Changes[0].Text != "B2。" || *df.Changes[0].Line != 3 ||
		df.Changes[1].Kind != "removed" || df.Changes[1].At != 3 || df.Changes[1].Line != nil || df.Changes[1].Text != nil || len(out.ChangedPaths) != 0 {
		t.Fatalf("diff: code = %d, %+v", code, df)
	}
	base := df.Base
	// 作者再次修改后旧 base 失效。
	writeFile(t, draft, "A。\n\nB3。\n")
	code, res = runSrc(t, dir, "", "draft", "write", "my-post", "--file", "new.md", "--base", base)
	if d := diag(t, res.result, "draft.base-mismatch"); code != 1 || !strings.Contains(d.Next[0], "wa-cli diff my-post") {
		t.Fatalf("stale base: code = %d, %+v", code, d)
	}
	_, out = diffJSON(t, dir)
	if code, res = runSrc(t, dir, "", "draft", "write", "my-post", "--file", "new.md", "--base", out.Diff.Base); code != 0 || readFile(t, draft) != "新正文。\n" {
		t.Fatalf("write-with-base: code = %d, %v", code, res.Diagnostics)
	}
	if _, out = diffJSON(t, dir); out.Diff.Edited || len(out.Diff.Changes) != 0 {
		t.Errorf("diff-clean = %+v", out.Diff)
	}
	// 原样写回不需要 base。
	writeFile(t, draft, "作者改的。\n")
	if code, _ = runSrc(t, dir, "作者改的。\n", "draft", "write", "my-post"); code != 0 {
		t.Errorf("identical-rewrite: code = %d", code)
	}
	if _, out = diffJSON(t, dir); out.Diff.Edited {
		t.Errorf("identical rewrite left the draft edited")
	}
}

func TestAuthorOnlyDraftAndDiffText(t *testing.T) {
	dir := newWork(t)
	writeFile(t, wsPath(dir, draftPath), "作者写的。\n\n第二段。\n")
	code, res := runSrc(t, dir, "Agent 写的。", "draft", "write", "my-post")
	if code != 1 {
		t.Fatalf("author-only-draft: code = %d", code)
	}
	diag(t, res.result, "draft.human-edited")
	code, out := diffJSON(t, dir)
	if code != 0 || !out.Diff.Edited || out.Diff.Recorded != nil || len(out.Diff.Changes) != 2 || out.Diff.Changes[1].Kind != "added" {
		t.Fatalf("diff without record = %+v", out.Diff)
	}
	if code, stdout, _ := run(t, "diff", "my-post", "--dir", dir); code != 0 || !strings.Contains(stdout, "新增：第 2 段（第 3 行）") || !strings.Contains(stdout, shellCommand("wa-cli", "draft", "write", "my-post", "--base", out.Diff.Base, "--dir", dir)) {
		t.Errorf("diff text = %q", stdout)
	}
	writeFile(t, wsPath(dir, draftPath), "第二段。\n")
	runSrc(t, dir, "第二段。\n", "draft", "write", "my-post")
	if _, stdout, _ := run(t, "diff", "my-post", "--dir", dir); !strings.Contains(stdout, "作者没有修改") {
		t.Errorf("clean text = %q", stdout)
	}
	writeFile(t, wsPath(dir, draftPath), "")
	if _, stdout, _ := run(t, "diff", "my-post", "--dir", dir); !strings.Contains(stdout, "删除：正文末尾原有的一段") {
		t.Errorf("removed text = %q", stdout)
	}
	os.Remove(wsPath(dir, draftPath))
	if code, out := diffJSON(t, dir); code != 1 || out.Diff != nil {
		t.Errorf("no draft: code = %d", code)
	} else {
		diag(t, out.result, "draft.not-found")
	}
}

func TestCommentFeedback(t *testing.T) {
	dir := newWork(t)
	runSrc(t, dir, "第一段。\n\n第二段。\n", "draft", "write", "my-post")
	body := "第一段。\n\n第二段。<!-- @me(must-fix): 这里要给数据 -->\n\n```\n<!-- @me: 示例 -->\n```\n"
	writeFile(t, wsPath(dir, draftPath), body)
	code, res := runFb(t, dir, "feedback", "list", "my-post")
	if code != 0 || len(res.Many) != 1 {
		t.Fatalf("list: code = %d, %v", code, ids(res.Many))
	}
	c := res.Many[0]
	if !strings.HasPrefix(c.ID, "cm-") || c.Kind != "must-fix" || c.Text != "这里要给数据" || c.Status != "open" || c.Anchor.State != "located" || *c.Anchor.Line != 3 {
		t.Fatalf("comment feedback = %+v", c)
	}
	var raw map[string]any
	json.Unmarshal([]byte(res.stdout), &raw)
	if raw["feedback"].([]any)[0].(map[string]any)["source"] != "comment" {
		t.Errorf("source = %v", raw["feedback"])
	}
	if _, err := os.Stat(wsPath(dir, ".writing-anything/works/my-post/feedback.yaml")); err == nil {
		t.Fatal("list created feedback.yaml")
	}
	// next 列出批注反馈。
	if _, res := runUnits(t, dir, "next", "my-post"); len(res.Steps[0]["feedback"].([]any)) != 1 {
		t.Errorf("next feedback = %v", res.Steps[0]["feedback"])
	}
	// draft write 覆盖前记入批注。
	code, sres := runSrc(t, dir, "第一段。\n\n第二段，补充了数据。\n", "draft", "write", "my-post", "--base", currentBase(t, dir))
	fbRel := ".writing-anything/works/my-post/feedback.yaml"
	if code != 0 || !slices.Contains(sres.ChangedPaths, fbRel) || !strings.Contains(readFile(t, wsPath(dir, fbRel)), "source: comment") {
		t.Fatalf("import-before-write: code = %d, %v", code, sres.ChangedPaths)
	}
	if _, res = runFb(t, dir, "feedback", "list", "my-post"); len(res.Many) != 1 || res.Many[0].ID != c.ID || res.Many[0].Anchor.State != "orphaned" {
		t.Errorf("after write = %+v", res.Many)
	}
	// 解决批注反馈：正文中已没有批注，只改状态。
	if code, res = runFb(t, dir, "feedback", "resolve", "my-post", c.ID, "--note", "已补充"); code != 0 || !slices.Equal(res.ChangedPaths, []string{fbRel}) {
		t.Errorf("resolve after write: code = %d, %v", code, res.ChangedPaths)
	}
}

func TestResolveCommentRemovesIt(t *testing.T) {
	dir := newWork(t)
	runSrc(t, dir, "第一段。\n\n第二段。\n", "draft", "write", "my-post")
	writeFile(t, wsPath(dir, draftPath), "第一段。\n\n第二段。<!-- @me: 这里要给数据 -->\n")
	_, res := runFb(t, dir, "feedback", "list", "my-post")
	id := res.Many[0].ID
	code, res := runFb(t, dir, "feedback", "resolve", "my-post", id, "--note", "已补充")
	fbRel := ".writing-anything/works/my-post/feedback.yaml"
	if code != 0 || res.One.Status != "resolved" || res.One.Source != "comment" || !slices.Equal(res.ChangedPaths, []string{fbRel, draftPath}) ||
		readFile(t, wsPath(dir, draftPath)) != "第一段。\n\n第二段。\n" {
		t.Fatalf("resolve-comment: code = %d, %+v, %v", code, res.One, res.ChangedPaths)
	}
	if !strings.Contains(readFile(t, wsPath(dir, fbRel)), "source: comment") {
		t.Error("record not kept")
	}
	// watch 把新出现的批注当作新增反馈输出。
	out, wait := watchBuf(t, "feedback", "watch", "--all", "--max", "2", "--interval", "20", "--dir", dir, "--json")
	runFb(t, dir, "feedback", "add", "my-post", "--text", "哨兵")
	waitFor(t, out, "哨兵")
	atomicWrite(t, wsPath(dir, draftPath), "第一段。<!-- @me: 新批注 -->\n\n第二段。\n")
	code, stderr := wait()
	if got := envelopeLines(t, out.String()); code != 0 || stderr != "" || len(got) != 2 || !strings.HasSuffix(got[1], "新批注") {
		t.Errorf("watch comment: code = %d, %v", code, got)
	}
}

// 被拒绝的写入不修改任何文件，包括 feedback.yaml（正文有批注时）；已有正文过大时同样不记入批注。
func TestRejectedWriteTouchesNothing(t *testing.T) {
	dir := newWork(t)
	fbPath := wsPath(dir, ".writing-anything/works/my-post/feedback.yaml")
	runSrc(t, dir, "A。\n", "draft", "write", "my-post")
	writeFile(t, wsPath(dir, draftPath), "A。<!-- @me: 批注 -->\n")
	if code, res := runSrc(t, dir, "新正文", "draft", "write", "my-post"); code != 1 || len(res.ChangedPaths) != 0 {
		t.Fatalf("refused write: code = %d, %v", code, res.ChangedPaths)
	}
	if _, err := os.Stat(fbPath); err == nil {
		t.Fatal("refused write created feedback.yaml")
	}
	writeFile(t, wsPath(dir, draftPath), "首段<!-- @me: 批注 -->\n\n"+strings.Repeat("段\n\n", 10001))
	code, res := runSrc(t, dir, "新正文", "draft", "write", "my-post", "--base", currentBase(t, dir))
	if code != 1 || len(res.ChangedPaths) != 0 {
		t.Fatalf("too large: code = %d, %v", code, res.ChangedPaths)
	}
	diag(t, res.result, "draft.too-large")
	if _, err := os.Stat(fbPath); err == nil {
		t.Fatal("too-large write created feedback.yaml")
	}
	// 没有批注时不读取 feedback.yaml：它不合法也不影响写入。
	writeFile(t, wsPath(dir, draftPath), "A。\n")
	runSrc(t, dir, "A。\n", "draft", "write", "my-post")
	writeFile(t, fbPath, "schema_version: 1\nfeedback: [{id: fb-1, kind: bogus, status: open, text: x}]\n")
	if code, res := runSrc(t, dir, "A2。\n", "draft", "write", "my-post"); code != 0 {
		t.Errorf("write with broken feedback.yaml and no comments: code = %d, %v", code, res.Diagnostics)
	}
	// 行内代码中的批注语法不是批注。
	writeFile(t, fbPath, "schema_version: 1\nfeedback: []\n")
	writeFile(t, wsPath(dir, draftPath), "在正文里写 `<!-- @me: 文本 -->` 即可。\n")
	if _, res := runFb(t, dir, "feedback", "list", "my-post"); len(res.Many) != 0 {
		t.Errorf("inline code comment = %v", ids(res.Many))
	}
}
