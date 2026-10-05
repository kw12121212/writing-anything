package draft

import (
	"fmt"
	"os"
	"path/filepath"
	"slices"
	"strings"
	"testing"
	"time"

	"github.com/kw12121212/writing-anything/internal/source"
	"github.com/kw12121212/writing-anything/internal/work"
	"github.com/kw12121212/writing-anything/internal/workspace"
)

// newWork 返回已初始化的临时工作区，其中有 minimal 文体的作品 my-post 与子单元 intro。
func newWork(t *testing.T) (string, *work.Work) {
	t.Helper()
	root := t.TempDir()
	if _, ps := workspace.Init(root, nil); len(ps) > 0 {
		t.Fatal(ps)
	}
	if _, _, ps := work.Create(root, "minimal", "my-post"); len(ps) > 0 {
		t.Fatal(ps)
	}
	w, _, ps := work.AddUnit(root, "my-post", "section", "intro", work.Anchor{})
	if len(ps) > 0 {
		t.Fatal(ps)
	}
	return root, w
}

func unit(t *testing.T, root, ref string) (*work.Work, *work.Unit) {
	t.Helper()
	w, u, ps := work.Resolve(root, ref)
	if len(ps) > 0 {
		t.Fatal(ps)
	}
	return w, u
}

func origins(ps []Paragraph) []string {
	var out []string
	for _, p := range ps {
		out = append(out, p.Origin)
	}
	return out
}

func TestSplit(t *testing.T) {
	content := "第一段\n第一段第二行\n\n  \n说明：\n```go\nfunc a() {\n\n}\n```\n\n~~~\n未闭合\n\n仍在围栏中"
	ps := Split([]byte(content))
	if len(ps) != 3 {
		t.Fatalf("got %d paragraphs, want 3: %+v", len(ps), ps)
	}
	if lines := []int{ps[0].Line, ps[1].Line, ps[2].Line}; !slices.Equal(lines, []int{1, 5, 12}) {
		t.Errorf("lines = %v", lines)
	}
	if ps[0].Chars != len([]rune("第一段第一段第二行")) {
		t.Errorf("chars = %d", ps[0].Chars)
	}
	// 行尾空白与换行符不影响哈希。
	a, b := Split([]byte("甲 乙\n丙")), Split([]byte("甲 乙  \r\n丙\t\r\n"))
	if a[0].Hash != b[0].Hash {
		t.Error("trailing whitespace changed the hash")
	}
	if Split([]byte(" \n\n")) != nil {
		t.Error("blank content must have no paragraphs")
	}
}

func para(hashes ...string) []Paragraph {
	var out []Paragraph
	for _, h := range hashes {
		out = append(out, Paragraph{Hash: h, Chars: 10})
	}
	return out
}

func rec(pairs ...string) []recorded {
	var out []recorded
	for i := 0; i+1 < len(pairs); i += 2 {
		out = append(out, recorded{Hash: pairs[i], Origin: pairs[i+1]})
	}
	return out
}

func TestDerive(t *testing.T) {
	agent3 := rec("a", "agent", "b", "agent", "c", "agent")
	for _, tc := range []struct {
		name string
		cur  []Paragraph
		rec  []recorded
		has  bool
		want []string
	}{
		{"edit one", para("a", "x", "c"), agent3, true, []string{"agent", "agent-draft-human-edited", "agent"}},
		{"insert one", para("a", "x", "b", "c"), agent3, true, []string{"agent", "human", "agent", "agent"}},
		{"delete one", para("a", "c"), agent3, true, []string{"agent", "agent"}},
		{"append", para("a", "b", "c", "x"), agent3, true, []string{"agent", "agent", "agent", "human"}},
		{"replace two with three", para("x", "y", "z"), rec("a", "agent", "b", "agent"), true, []string{"agent-draft-human-edited", "agent-draft-human-edited", "human"}},
		{"edit human", para("x"), rec("a", "human"), true, []string{"human"}},
		{"keeps recorded origin", para("a", "b"), rec("a", "human", "b", "agent-draft-human-edited"), true, []string{"human", "agent-draft-human-edited"}},
		{"no record", para("a", "b"), nil, false, []string{"human", "human"}},
		{"empty record", para("a"), nil, true, []string{"human"}},
	} {
		derive(tc.cur, tc.rec, tc.has)
		if got := origins(tc.cur); !slices.Equal(got, tc.want) {
			t.Errorf("%s: origins = %v, want %v", tc.name, got, tc.want)
		}
	}
}

func TestSummarize(t *testing.T) {
	ps := []Paragraph{{Chars: 30, Origin: "agent"}, {Chars: 30, Origin: "agent"}, {Chars: 40, Origin: "human"}}
	shares, total := summarize(ps)
	want := []Share{{"human", 1, 40, 0.4}, {"agent", 2, 60, 0.6}, {"agent-draft-human-edited", 0, 0, 0}}
	if total != 100 || !slices.Equal(shares, want) {
		t.Fatalf("summarize = %v, %d", shares, total)
	}
	if shares, total := summarize(nil); total != 0 || shares[0].Ratio != 0 || len(shares) != 3 {
		t.Fatalf("summarize(nil) = %v, %d", shares, total)
	}
	third, _ := summarize([]Paragraph{{Chars: 1, Origin: "human"}, {Chars: 2, Origin: "agent"}})
	if third[0].Ratio != 0.3333 {
		t.Errorf("ratio = %v, want 0.3333", third[0].Ratio)
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

func write(t *testing.T, root, rel, content string) {
	t.Helper()
	if err := os.WriteFile(filepath.Join(root, filepath.FromSlash(rel)), []byte(content), 0o644); err != nil {
		t.Fatal(err)
	}
}

func TestWriteAndProvenance(t *testing.T) {
	root, _ := newWork(t)
	w, u := unit(t, root, "my-post")
	body := "第一段。\n\n第二段。\n\n第三段。\n"
	res, changed, ps := Write(root, w, u, []byte(body), "", time.Date(2026, 6, 20, 12, 0, 0, 0, time.UTC))
	if len(ps) > 0 {
		t.Fatal(ps)
	}
	draftRel, recordRel := ".writing-anything/works/my-post/draft.md", ".writing-anything/works/my-post/provenance.yaml"
	if res.Paragraphs != 3 || res.Path != draftRel || res.SHA256 != source.Hash([]byte(body)) {
		t.Errorf("written = %+v", res)
	}
	if !slices.Equal(changed, []string{draftRel, recordRel}) {
		t.Errorf("changed = %v", changed)
	}
	if read(t, root, draftRel) != body {
		t.Error("draft.md is not the input")
	}
	if n := strings.Count(read(t, root, recordRel), "origin: agent"); n != 3 {
		t.Errorf("record has %d agent paragraphs, want 3", n)
	}

	// 同样的内容再次写入：不写文件。
	info, _ := os.Stat(filepath.Join(root, filepath.FromSlash(recordRel)))
	time.Sleep(10 * time.Millisecond)
	if _, changed, ps := Write(root, w, u, []byte(body), "", time.Date(2026, 6, 20, 12, 0, 0, 0, time.UTC)); len(ps) > 0 || len(changed) != 0 {
		t.Fatalf("rewrite: changed = %v, %v", changed, ps)
	}
	if after, _ := os.Stat(filepath.Join(root, filepath.FromSlash(recordRel))); !after.ModTime().Equal(info.ModTime()) {
		t.Error("unchanged rewrite touched provenance.yaml")
	}

	// 作者改第二段并追加一段。
	write(t, root, draftRel, "第一段。\n\n第二段，作者改过。\n\n第三段。\n\n作者新增。\n")
	r, ps := Provenance(root, w, u)
	if len(ps) > 0 {
		t.Fatal(ps)
	}
	if got := origins(r.Paragraphs); !slices.Equal(got, []string{"agent", "agent-draft-human-edited", "agent", "human"}) {
		t.Errorf("origins = %v", got)
	}
	if r.Paragraphs[3].Line != 7 || r.Summary[0].Paragraphs != 1 {
		t.Errorf("report = %+v", r)
	}

	// Agent 保留作者的段落、改写第一段后重写：作者段落的出处保留。
	_, _, ps = Write(root, w, u, []byte("改写后的第一段。\n\n第二段，作者改过。\n\n第三段。\n\n作者新增。\n"), source.Hash([]byte(read(t, root, draftRel))), time.Date(2026, 6, 20, 12, 0, 0, 0, time.UTC))
	if len(ps) > 0 {
		t.Fatal(ps)
	}
	r, _ = Provenance(root, w, u)
	if got := origins(r.Paragraphs); !slices.Equal(got, []string{"agent", "agent-draft-human-edited", "agent", "human"}) {
		t.Errorf("after rewrite origins = %v", got)
	}
}

func TestWriteChildCreatesUnitDir(t *testing.T) {
	root, _ := newWork(t)
	w, u := unit(t, root, "my-post/intro")
	_, changed, ps := Write(root, w, u, []byte("引言"), "", time.Date(2026, 6, 20, 12, 0, 0, 0, time.UTC))
	dir := ".writing-anything/works/my-post/units/intro"
	if len(ps) > 0 || !slices.Equal(changed, []string{dir, dir + "/draft.md", dir + "/provenance.yaml"}) {
		t.Fatalf("changed = %v, %v", changed, ps)
	}
	// 单元目录已存在时不再列出。
	if _, changed, _ := Write(root, w, u, []byte("引言二"), "", time.Date(2026, 6, 20, 12, 0, 0, 0, time.UTC)); slices.Contains(changed, dir) {
		t.Errorf("changed = %v lists an existing unit directory", changed)
	}
}

func TestWriteRejects(t *testing.T) {
	root, _ := newWork(t)
	w, u := unit(t, root, "my-post")
	if _, _, ps := Write(root, w, u, []byte(" \n\t\n"), "", time.Date(2026, 6, 20, 12, 0, 0, 0, time.UTC)); len(ps) != 1 || ps[0].Code != "draft.empty" {
		t.Fatalf("empty: %v", ps)
	}
	if _, err := os.Stat(filepath.Join(root, ".writing-anything", "works", "my-post", "draft.md")); err == nil {
		t.Fatal("empty write created draft.md")
	}
	// 写入记录不合法时拒绝写入，也拒绝报告。
	write(t, root, ".writing-anything/works/my-post/draft.md", "作者")
	write(t, root, ".writing-anything/works/my-post/provenance.yaml", "schema_version: 1\n")
	if _, _, ps := Write(root, w, u, []byte("x"), "", time.Date(2026, 6, 20, 12, 0, 0, 0, time.UTC)); len(ps) == 0 || ps[0].Code != "artifact.schema-violation" {
		t.Fatalf("invalid record: %v", ps)
	}
	if _, ps := Provenance(root, w, u); len(ps) == 0 || ps[0].Code != "artifact.schema-violation" {
		t.Fatalf("invalid record report: %v", ps)
	}
	write(t, root, ".writing-anything/works/my-post/provenance.yaml", "a: [\n")
	if _, ps := Provenance(root, w, u); len(ps) == 0 || ps[0].Code != "artifact.parse-error" {
		t.Fatalf("unparsable record: %v", ps)
	}

	// 子单元的 units/ 是符号链接时不跟随写出。
	root2, _ := newWork(t)
	w2, u2 := unit(t, root2, "my-post/intro")
	outside := t.TempDir()
	os.Symlink(outside, filepath.Join(root2, ".writing-anything", "works", "my-post", "units"))
	if _, _, ps := Write(root2, w2, u2, []byte("x"), "", time.Date(2026, 6, 20, 12, 0, 0, 0, time.UTC)); len(ps) != 1 || ps[0].Code != "workspace.write-failed" {
		t.Fatalf("symlinked units/: %v", ps)
	}
	if entries, _ := os.ReadDir(outside); len(entries) != 0 {
		t.Fatal("wrote through a symlink")
	}
}

func TestProvenanceWithoutRecordOrDraft(t *testing.T) {
	root, _ := newWork(t)
	w, u := unit(t, root, "my-post")
	if _, ps := Provenance(root, w, u); len(ps) != 1 || ps[0].Code != "draft.not-found" || ps[0].Path != ".writing-anything/works/my-post/draft.md" {
		t.Fatalf("no draft: %v", ps)
	}
	write(t, root, ".writing-anything/works/my-post/draft.md", "作者写的。\n\n也是作者写的。\n")
	r, ps := Provenance(root, w, u)
	if len(ps) > 0 || !slices.Equal(origins(r.Paragraphs), []string{"human", "human"}) || r.Summary[0].Ratio != 1 {
		t.Fatalf("no record: %+v, %v", r, ps)
	}
	write(t, root, ".writing-anything/works/my-post/draft.md", "\n\n")
	if r, _ := Provenance(root, w, u); r.Paragraphs == nil || len(r.Paragraphs) != 0 || r.TotalChars != 0 {
		t.Fatalf("blank draft: %+v", r)
	}
}

// 单元目录途经符号链接时，读取当作不存在，不读取项目目录之外的文件（workspace-artifacts/validated-path-segments）。
func TestSymlinkedUnitDirNotRead(t *testing.T) {
	root, _ := newWork(t)
	outside := t.TempDir()
	os.WriteFile(filepath.Join(outside, "draft.md"), []byte("外部正文"), 0o644)
	os.WriteFile(filepath.Join(outside, "claims.yaml"), []byte("schema_version: 1\nclaims: {}\n"), 0o644)
	units := filepath.Join(root, ".writing-anything", "works", "my-post", "units")
	os.Mkdir(units, 0o755)
	os.Symlink(outside, filepath.Join(units, "intro"))
	w, u := unit(t, root, "my-post/intro")
	if _, ps := Provenance(root, w, u); len(ps) != 1 || ps[0].Code != "draft.not-found" {
		t.Fatalf("Provenance through a symlink = %v", ps)
	}
	if checked, ps := CheckUnitArtifacts(root, []*work.Work{w}); len(checked) != 0 || len(ps) != 0 {
		t.Fatalf("CheckUnitArtifacts through a symlink = %v, %v", checked, ps)
	}
	if _, _, ps := Write(root, w, u, []byte("x"), "", time.Date(2026, 6, 20, 12, 0, 0, 0, time.UTC)); len(ps) != 1 || ps[0].Code != "workspace.write-failed" {
		t.Fatalf("Write through a symlink = %v", ps)
	}
}

func TestTooManyParagraphs(t *testing.T) {
	root, _ := newWork(t)
	w, u := unit(t, root, "my-post")
	big := strings.Repeat("段\n\n", MaxParagraphs+1)
	if _, _, ps := Write(root, w, u, []byte(big), "", time.Date(2026, 6, 20, 12, 0, 0, 0, time.UTC)); len(ps) != 1 || ps[0].Code != "draft.too-large" {
		t.Fatalf("Write(too large) = %v", ps)
	}
	write(t, root, ".writing-anything/works/my-post/draft.md", big)
	if _, ps := Provenance(root, w, u); len(ps) != 1 || ps[0].Code != "draft.too-large" || ps[0].Path != ".writing-anything/works/my-post/draft.md" {
		t.Fatalf("Provenance(too large) = %v", ps)
	}
}

func TestGuard(t *testing.T) {
	root, _ := newWork(t)
	w, u := unit(t, root, "my-post")
	draftRel := ".writing-anything/works/my-post/draft.md"
	// 没有正文时不需要 --base。
	if ps := Guard(root, w, u, []byte("x"), ""); ps != nil {
		t.Fatal(ps)
	}
	// 作者直接写的正文（没有写入记录）。
	write(t, root, draftRel, "作者写的。")
	for _, tc := range []struct{ content, base, code string }{
		{"新内容", "", "draft.human-edited"},
		{"新内容", strings.Repeat("0", 64), "draft.base-mismatch"},
		{"新内容", "short", "draft.base-mismatch"},
		{"作者写的。", "", ""},
		{"新内容", source.Hash([]byte("作者写的。")), ""},
	} {
		ps := Guard(root, w, u, []byte(tc.content), tc.base)
		if tc.code == "" && ps != nil || tc.code != "" && (len(ps) != 1 || ps[0].Code != tc.code || ps[0].Path != draftRel) {
			t.Errorf("%q/%q: %v", tc.content, tc.base, ps)
		}
	}
	if _, _, ps := Write(root, w, u, []byte("新内容"), "", time.Date(2026, 6, 20, 12, 0, 0, 0, time.UTC)); len(ps) != 1 || ps[0].Code != "draft.human-edited" || read(t, root, draftRel) != "作者写的。" {
		t.Fatalf("Write over author draft = %v", ps)
	}
	// 原样写回建立基线，之后不改动时不需要 --base；给出的 --base 仍必须正确。
	if _, _, ps := Write(root, w, u, []byte("作者写的。"), "", time.Date(2026, 6, 20, 12, 0, 0, 0, time.UTC)); len(ps) > 0 {
		t.Fatal(ps)
	}
	if _, _, ps := Write(root, w, u, []byte("Agent 改写。"), "", time.Date(2026, 6, 20, 12, 0, 0, 0, time.UTC)); len(ps) > 0 {
		t.Fatal(ps)
	}
	if ps := Guard(root, w, u, []byte("再改"), "bad"); len(ps) != 1 || ps[0].Code != "draft.base-mismatch" {
		t.Errorf("wrong base without edits = %v", ps)
	}
	write(t, root, ".writing-anything/works/my-post/provenance.yaml", "a: [\n")
	if ps := Guard(root, w, u, []byte("再改"), ""); len(ps) != 1 || ps[0].Code != "artifact.parse-error" {
		t.Errorf("broken record = %v", ps)
	}
}

func TestDiff(t *testing.T) {
	root, _ := newWork(t)
	w, u := unit(t, root, "my-post")
	draftRel := ".writing-anything/works/my-post/draft.md"
	if _, ps := Diff(root, w, u); len(ps) != 1 || ps[0].Code != "draft.not-found" {
		t.Fatalf("no draft = %v", ps)
	}
	Write(root, w, u, []byte("A。\n\nB。\n\nC。\n"), "", time.Date(2026, 6, 20, 12, 0, 0, 0, time.UTC))
	c, ps := Diff(root, w, u)
	if len(ps) > 0 || c.Edited || len(c.Changes) != 0 || c.Base != c.Recorded || c.Path != draftRel {
		t.Fatalf("clean = %+v, %v", c, ps)
	}
	kinds := func(c *Changes) []string {
		var out []string
		for _, ch := range c.Changes {
			out = append(out, fmt.Sprintf("%s %d %d %s", ch.Kind, ch.At, ch.Line, ch.Text))
		}
		return out
	}
	for _, tc := range []struct {
		body string
		want []string
	}{
		{"A。\n\nB2。\n", []string{"edited 2 3 B2。", "removed 3 0 "}},
		{"A。\n\nX。\n\nB。\n\nC。\n", []string{"added 2 3 X。"}},
		{"新开头。\n\nA。\n\nB。\n\nC。\n\n结尾。\n", []string{"added 1 1 新开头。", "added 5 9 结尾。"}},
		{"B。\n\nC。\n", []string{"removed 1 0 "}},
	} {
		write(t, root, draftRel, tc.body)
		c, _ := Diff(root, w, u)
		if got := kinds(c); !c.Edited || !slices.Equal(got, tc.want) || c.Base != source.Hash([]byte(tc.body)) {
			t.Errorf("%q: %v, want %v", tc.body, got, tc.want)
		}
	}
	// 没有写入记录时每段都是 added。
	os.Remove(filepath.Join(root, ".writing-anything", "works", "my-post", "provenance.yaml"))
	write(t, root, draftRel, "甲\n\n乙\n")
	if c, _ := Diff(root, w, u); !c.Edited || c.Recorded != "" || !slices.Equal(kinds(c), []string{"added 1 1 甲", "added 2 3 乙"}) {
		t.Errorf("no record = %+v", c)
	}
}

func TestRebaseline(t *testing.T) {
	root, _ := newWork(t)
	w, u := unit(t, root, "my-post")
	draftRel := ".writing-anything/works/my-post/draft.md"
	old := "Agent 第一段。<!-- 注 -->\n\n作者段。\n"
	Write(root, w, u, []byte("Agent 第一段。<!-- 注 -->\n\nAgent 第二段。\n"), "", time.Date(2026, 6, 20, 12, 0, 0, 0, time.UTC))
	write(t, root, draftRel, old)
	// 正文已被作者改过：不重建。
	if changed, ps := Rebaseline(root, w, u, []byte(old), []byte("x")); changed != nil || ps != nil {
		t.Fatalf("edited draft rebaselined: %v %v", changed, ps)
	}
	Write(root, w, u, []byte(old), "", time.Date(2026, 6, 20, 12, 0, 0, 0, time.UTC))
	now := "Agent 第一段。\n\n作者段。\n"
	write(t, root, draftRel, now)
	changed, ps := Rebaseline(root, w, u, []byte(old), []byte(now))
	if len(ps) > 0 || len(changed) != 1 {
		t.Fatalf("rebaseline = %v, %v", changed, ps)
	}
	r, _ := Provenance(root, w, u)
	if got := origins(r.Paragraphs); !slices.Equal(got, []string{"agent", "agent-draft-human-edited"}) {
		t.Errorf("origins after rebaseline = %v", got)
	}
	if ps := Guard(root, w, u, []byte("Agent 新写"), ""); ps != nil {
		t.Errorf("wa-cli's own edit counted as human edit: %v", ps)
	}
}
