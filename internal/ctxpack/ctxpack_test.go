package ctxpack

import (
	"os"
	"path/filepath"
	"slices"
	"strings"
	"testing"

	"github.com/kw12121212/writing-anything/internal/source"
	"github.com/kw12121212/writing-anything/internal/work"
	"github.com/kw12121212/writing-anything/internal/workspace"

	"github.com/kw12121212/writing-anything/internal/brief"
	"github.com/kw12121212/writing-anything/internal/catalog"
	"github.com/kw12121212/writing-anything/internal/feedback"
	"github.com/kw12121212/writing-anything/internal/outline"
	"github.com/kw12121212/writing-anything/internal/resolve"
)

func TestExcerpt(t *testing.T) {
	long := strings.Repeat("字", ExcerptChars+10)
	for in, want := range map[string]string{
		"\n\n  第一段\r\n第一段续\r\n\r\n第二段":     "第一段 第一段续",
		"# 标题\n\n正文<!-- @me: 批注\n跨行 -->继续": "正文继续",
		long:   strings.Repeat("字", ExcerptChars),
		"\n\n": "",
	} {
		if got := excerpt(in); got != want {
			t.Errorf("excerpt(%q) = %q, want %q", in, got, want)
		}
	}
}

func TestMarkdownItems(t *testing.T) {
	n := Norms{
		Rules:       []Rule{{ID: "length", Type: "length-range", Severity: "error", Params: map[string]any{"min": 1, "max": 2}}},
		Glossary:    []resolve.Term{{Term: catalog.Term{Term: "规范包", English: "Pack", Avoid: []string{"配置包"}}}},
		BannedTerms: []resolve.BannedTerm{{BannedTerm: catalog.BannedTerm{Term: "赋能", Replacement: "帮助"}}},
		Style:       []string{"短句优先"},
	}
	items := n.items(&n)
	var parts []string
	var body strings.Builder
	for _, it := range items {
		parts = append(parts, it.part)
		body.WriteString(it.body)
	}
	for _, want := range []string{"length：length-range，error，max=2 min=1", "规范包（Pack）；不要写作 配置包", "赋能 → 帮助", "- 短句优先"} {
		if !strings.Contains(body.String(), want) {
			t.Errorf("norms markdown lacks %q:\n%s", want, body.String())
		}
	}
	for _, it := range items {
		it.drop()
	}
	if len(parts) != 4 || n.Rules != nil || n.Glossary != nil || n.BannedTerms != nil || n.Style != nil {
		t.Fatalf("parts = %v, norms after drop = %+v", parts, n)
	}
	if got := outlineMarkdown(&outline.Outline{}); !strings.Contains(got, "没有大纲节") {
		t.Fatalf("empty outline = %q", got)
	}
	if got := outlineMarkdown(&outline.Outline{Sections: []outline.Section{{ID: "a", Title: "甲", Reason: "引出"}}}); !strings.Contains(got, "不覆盖核心观点：引出") {
		t.Fatalf("outline = %q", got)
	}
	f := feedback.Item{Feedback: feedback.Feedback{ID: "fb-1", Kind: "must-fix", Status: "open", Text: "改", Anchor: &feedback.Anchor{Quote: "原句"}}}
	if got := feedbackMarkdown(f); !strings.Contains(got, "原文：原句") {
		t.Fatalf("feedback = %q", got)
	}
	s := &Section{ID: "why", Title: "为什么", Reason: "r", Uses: []string{"src-a"}}
	if got := s.markdown(); !strings.Contains(got, "不覆盖核心观点的理由：r") || !strings.Contains(got, "使用素材：src-a") {
		t.Fatalf("section = %q", got)
	}
	b := &Brief{Unit: "w", Length: &brief.Length{Min: 1, Max: 2}, Avoid: []string{"甲", "乙"}}
	if got := b.markdown(); !strings.Contains(got, "字数范围：1-2") || !strings.Contains(got, "禁区（Off-limits）：甲；乙") {
		t.Fatalf("brief = %q", got)
	}
	if find(nil, "x") != nil || find(&outline.Outline{}, "x") != nil {
		t.Fatal("find")
	}
}

// fit 按优先级放入内容项：放不下的项整项裁剪，素材原文截断到剩余预算，并继续尝试之后更小的项。
func TestFit(t *testing.T) {
	var dropped []string
	var truncated string
	mk := func(part, id, body string) item {
		return item{part: part, id: id, body: body, drop: func() { dropped = append(dropped, part+id) }}
	}
	src := mk(PartSource, "s", "### s\n\n"+strings.Repeat("原", 50)+"\n")
	src.prefix = "### s\n\n"
	src.truncate = func(t string) { truncated = t }
	p := &Pack{Budget: resolve.Value{Value: 30}}
	p.fit([]item{mk(PartBrief, "", strings.Repeat("简", 10)), mk(PartRules, "", strings.Repeat("规", 25)), src, mk(PartNeighbor, "n", "邻")})
	if p.Used != 30 || len(p.items) != 2 || p.items[1].part != PartSource || chars(p.items[1].body) != 20 || !strings.HasPrefix(truncated, "原") {
		t.Fatalf("used = %d, items = %+v, truncated = %q", p.Used, p.items, truncated)
	}
	if len(dropped) != 2 || dropped[0] != PartRules || dropped[1] != PartNeighbor+"n" || len(p.Trimmed) != 3 || p.Trimmed[1].KeptChars != 20 {
		t.Fatalf("dropped = %v, trimmed = %+v", dropped, p.Trimmed)
	}
	// 剩余预算放不下素材的标题时整项裁剪。
	dropped = nil
	q := &Pack{Budget: resolve.Value{Value: 5}}
	q.fit([]item{src})
	if len(q.items) != 0 || len(dropped) != 1 || q.Trimmed[0].KeptChars != 0 {
		t.Fatalf("tiny: items = %+v, trimmed = %+v", q.items, q.Trimmed)
	}
}

func put(t *testing.T, root, rel, content string) {
	t.Helper()
	p := filepath.Join(root, filepath.FromSlash(rel))
	if err := os.MkdirAll(filepath.Dir(p), 0o755); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(p, []byte(content), 0o644); err != nil {
		t.Fatal(err)
	}
}

// project 返回一个工作区：作品 my-post（minimal）有子单元 intro、why、how，写作简报、大纲、素材 src-a 与 src-b。
func project(t *testing.T) string {
	t.Helper()
	root := t.TempDir()
	if _, ps := workspace.Init(root, nil); len(ps) > 0 {
		t.Fatal(ps)
	}
	if _, _, ps := work.Create(root, "minimal", "my-post"); len(ps) > 0 {
		t.Fatal(ps)
	}
	for _, id := range []string{"intro", "why", "how"} {
		if _, _, ps := work.AddUnit(root, "my-post", "section", id); len(ps) > 0 {
			t.Fatal(ps)
		}
	}
	for id, content := range map[string]string{"src-a": "作者的原话。", "src-b": "\xff\xfe"} {
		if _, _, ps := source.Add(root, []byte(content), "interview", "", id, ""); len(ps) > 0 {
			t.Fatal(ps)
		}
	}
	dir := work.Dir("my-post")
	put(t, root, dir+"/brief.yaml", "schema_version: 1\naudience: 读者\npurpose: 目的\nkey_messages:\n  - {id: km-1, text: 观点, sources: [src-a, src-b]}\n")
	put(t, root, dir+"/outline.yaml", "schema_version: 1\nsections:\n  - {id: intro, title: 引子, reason: r}\n  - {id: why, title: 为什么, covers: [km-1]}\n")
	put(t, root, dir+"/units/how/draft.md", "怎么做的第一段。\n")
	return root
}

func TestBuild(t *testing.T) {
	root := project(t)
	w, u, ps := work.Resolve(root, "my-post/why")
	if ps != nil {
		t.Fatal(ps)
	}
	p, ps := Build(root, w, u)
	// 非 UTF-8 的 src-b 以 warning 报告，不放入上下文包。
	if len(ps) != 1 || ps[0].Code != "context.source-skipped" || ps[0].Severity != workspace.SeverityWarning {
		t.Fatal(ps)
	}
	if p.Brief == nil || p.Brief.Unit != "my-post" || p.Section == nil || p.Section.KeyMessages[0].ID != "km-1" || p.Outline != nil {
		t.Fatalf("brief = %+v, section = %+v, outline = %+v", p.Brief, p.Section, p.Outline)
	}
	// 非 UTF-8 的 src-b 不放入；src-a 是 UTF-8 文本。
	if len(p.Sources) != 1 || p.Sources[0].ID != "src-a" || p.Sources[0].Text != "作者的原话。" {
		t.Fatalf("sources = %+v", p.Sources)
	}
	if len(p.Neighbors) != 2 || p.Neighbors[0].Title != "引子" || p.Neighbors[1].Excerpt == nil || *p.Neighbors[1].Excerpt != "怎么做的第一段。" || p.Neighbors[1].Title != "" {
		t.Fatalf("neighbors = %+v", p.Neighbors)
	}
	if p.Budget != (resolve.Value{Value: 12000, Source: "genre:minimal"}) || p.Used == 0 || len(p.Trimmed) != 0 || len(p.Norms.Rules) == 0 {
		t.Fatalf("budget = %+v, used = %d, trimmed = %+v", p.Budget, p.Used, p.Trimmed)
	}
	// used 等于保留的各内容项的字符数之和，也就是 Markdown 中各内容项文本的字符数之和。
	sum := 0
	for _, it := range p.items {
		sum += chars(it.body)
		if !strings.Contains(p.Markdown(), it.body) {
			t.Fatalf("markdown lacks item %s", it.part)
		}
	}
	if sum != p.Used {
		t.Fatalf("used = %d, items = %d", p.Used, sum)
	}
	md := p.Markdown()
	for _, want := range []string{"# 上下文包（Context Pack）：my-post/why", "## 本节大纲条目", "## 素材原文", "## 相邻节（Neighbor Section）"} {
		if !strings.Contains(md, want) {
			t.Errorf("markdown lacks %q", want)
		}
	}
	// 根单元：本单元大纲，素材按写作简报的顺序。
	w, root0, _ := work.Resolve(root, "my-post")
	if p, ps = Build(root, w, root0); len(ps) != 1 || p.Outline == nil || p.Section != nil || len(p.Neighbors) != 0 {
		t.Fatalf("root: %+v %+v", p, ps)
	}
	// 读取的产物不通过校验时返回其问题。
	for _, tt := range []struct{ rel, content string }{
		{work.Dir("my-post") + "/outline.yaml", "k: v\n"},
		{work.Dir("my-post") + "/units/why/brief.yaml", "k: v\n"},
		{work.Dir("my-post") + "/units/why/outline.yaml", "k: v\n"},
		{work.Dir("my-post") + "/units/why/feedback.yaml", "k: v\n"},
		{".writing-anything/decisions.yaml", "k: [\n"},
		{workspace.ConfigPath, "packs: [nope]\nschema_version: 1\nlanguage: zh-CN\ngate_level: standard\n"},
	} {
		old, err := os.ReadFile(filepath.Join(root, filepath.FromSlash(tt.rel)))
		put(t, root, tt.rel, tt.content)
		if p, ps := Build(root, w, u); p != nil || len(ps) == 0 {
			t.Fatalf("%s: %+v %+v", tt.rel, p, ps)
		}
		if err == nil {
			put(t, root, tt.rel, string(old))
		} else {
			os.Remove(filepath.Join(root, filepath.FromSlash(tt.rel)))
		}
	}
}

// 本节素材取自父单元写作简报中被覆盖的核心观点，即使子单元有自己的写作简报；改写过的素材原文不放入。
func TestSectionSourcesAndModified(t *testing.T) {
	root := project(t)
	put(t, root, work.Dir("my-post")+"/units/why/brief.yaml", "schema_version: 1\nkey_messages:\n  - {id: km-1, text: 同名观点, sources: [src-b]}\n")
	w, u, _ := work.Resolve(root, "my-post/why")
	p, ps := Build(root, w, u)
	// 父单元写作简报中 km-1 的 sources 为 [src-a, src-b]；src-b 不是 UTF-8 文本，以 warning 报告。
	if p == nil || p.Brief.Unit != "my-post/why" || len(p.Sources) != 1 || p.Sources[0].ID != "src-a" || len(ps) != 1 {
		t.Fatalf("sources = %+v, brief = %+v, problems = %+v", p.Sources, p.Brief, ps)
	}
	put(t, root, source.Dir("src-a")+"/original.txt", "被篡改的原话")
	p, ps = Build(root, w, u)
	if len(p.Sources) != 0 || len(ps) != 2 || ps[0].Code != "source.modified" || ps[0].Severity != workspace.SeverityWarning {
		t.Fatalf("modified: sources = %+v, problems = %+v", p.Sources, ps)
	}
	// 大纲中的悬空引用以 warning 报告。
	put(t, root, work.Dir("my-post")+"/outline.yaml", "schema_version: 1\nsections:\n  - {id: why, title: 为什么, covers: [km-1, km-9]}\n")
	if _, ps = Build(root, w, u); !slices.ContainsFunc(ps, func(p workspace.Problem) bool {
		return p.Code == "outline.unknown-message" && p.Severity == workspace.SeverityWarning
	}) {
		t.Fatalf("dangling: %+v", ps)
	}
}

func TestScan(t *testing.T) {
	big := strings.Repeat("字", chunkSize) // 多字节字符跨越读取块的边界
	for _, tt := range []struct {
		in           string
		keep         int
		prefix       string
		chars        int
		whole, valid bool
	}{
		{"甲\r\n乙\r\n\n", 10, "甲\n乙", 3, true, true},
		{"甲乙\n\n", 1, "甲", 2, false, true}, // 截断时末尾换行同样不计入字符数
		{"a\rb", 10, "a\rb", 3, true, true},
		{"末尾\r", 10, "末尾\r", 3, true, true},
		{big, 3, "字字字", chunkSize, false, true},
		{"\xff\xfe", 10, "\ufffd\ufffd", 2, true, false},
	} {
		got, sum, valid, err := scan(strings.NewReader(tt.in), tt.keep)
		if err != nil || got.Prefix != tt.prefix || got.Chars != tt.chars || got.Whole != tt.whole || valid != tt.valid || sum != source.Hash([]byte(tt.in)) {
			t.Errorf("scan(%.20q) = %+v %v %v", tt.in, got, valid, err)
		}
	}
	if got := prefixChars("甲乙丙", 2); got != "甲乙" {
		t.Fatalf("prefixChars = %q", got)
	}
}
