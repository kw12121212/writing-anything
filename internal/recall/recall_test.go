package recall

import (
	"os"
	"path/filepath"
	"strings"
	"testing"

	"github.com/kw12121212/writing-anything/internal/canon"
	"github.com/kw12121212/writing-anything/internal/work"
	"github.com/kw12121212/writing-anything/internal/workspace"
)

// newWorkspace 初始化工作区并创建 minimal 作品 id；返回项目目录。
func newWorkspace(t *testing.T, id string) string {
	t.Helper()
	root := t.TempDir()
	if _, ps := workspace.Init(root, nil); len(ps) > 0 {
		t.Fatal(ps)
	}
	if _, _, ps := work.Create(root, "minimal", id); len(ps) > 0 {
		t.Fatal(ps)
	}
	return root
}

// doneWork 把作品 id 的 work.yaml 改为 stage: done（units 给出时子单元同为 done），并写出各单元的正文。
// drafts 的键是单元 id（根单元为空字符串），值为正文内容。
func doneWork(t *testing.T, root, id string, units []string, drafts map[string]string) {
	t.Helper()
	var b strings.Builder
	b.WriteString("schema_version: 1\nid: " + id + "\ngenre: minimal\ntype: article\nstage: done\n")
	if len(units) == 0 {
		b.WriteString("units: []\n")
	} else {
		b.WriteString("units:\n")
		for _, u := range units {
			b.WriteString("  - id: " + u + "\n    type: section\n    stage: done\n")
		}
	}
	writeFile(t, filepath.Join(root, work.Path(id)), b.String())
	for u, text := range drafts {
		dir := filepath.Join(root, work.Dir(id))
		if u != "" {
			dir = filepath.Join(dir, "units", u)
		}
		writeFile(t, filepath.Join(dir, "draft.md"), text)
	}
}

func writeFile(t *testing.T, path, content string) {
	t.Helper()
	if err := os.MkdirAll(filepath.Dir(path), 0o755); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(path, []byte(content), 0o644); err != nil {
		t.Fatal(err)
	}
}

func writeCanon(t *testing.T, root, id, content string) {
	t.Helper()
	writeFile(t, filepath.Join(root, canon.Path(id)), content)
}

func run(t *testing.T, root, query, workID string) (*Output, []string, []workspace.Problem) {
	t.Helper()
	out, changed, ps := Run(root, Input{Query: query, Work: workID})
	if out == nil {
		t.Fatal("no output:", ps)
	}
	return out, changed, ps
}

func TestTokenizeUnit(t *testing.T) {
	got := tokenizeUnit("守灯人提灯")
	want := []string{"人提", "守灯", "提灯", "灯人"}
	if !equal(got, want) {
		t.Errorf("tokenizeUnit(守灯人提灯) = %v, want %v", got, want)
	}
	got = tokenizeUnit("JSON parse with Wa-CLI")
	want = []string{"cli", "json", "parse", "wa", "with"}
	if !equal(got, want) {
		t.Errorf("tokenizeUnit(JSON parse with Wa-CLI) = %v, want %v", got, want)
	}
	if got := tokenizeUnit("AAAA"); !equal(got, []string{"aaaa"}) {
		t.Errorf("tokenizeUnit(AAAA) = %v, want the whole word [aaaa]", got)
	}
	if got := tokenizeUnit("，。  \xff"); len(got) != 0 {
		t.Errorf("tokenizeUnit(标点与无效字节) = %v, want no terms", got)
	}
}

func equal(a, b []string) bool {
	if len(a) != len(b) {
		return false
	}
	for i := range a {
		if a[i] != b[i] {
			return false
		}
	}
	return true
}

func TestRunKeywordAndRewrite(t *testing.T) {
	root := newWorkspace(t, "my-story")
	doneWork(t, root, "my-story", nil, map[string]string{"": "开头一段。\n\n守灯人把灯点亮。"})
	out, changed, ps := run(t, root, "守灯人", "")
	if len(ps) != 0 || len(out.Results) != 1 {
		t.Fatalf("results = %+v, problems = %+v", out.Results, ps)
	}
	h := out.Results[0]
	if h.Work != "my-story" || h.Unit != "my-story" || h.Paragraph != 2 || h.Entry != nil || !strings.Contains(h.Snippet, "守灯人") {
		t.Errorf("hit = %+v", h)
	}
	if len(changed) != 1 || changed[0] != IndexPath {
		t.Errorf("changedPaths = %v, want [%s]", changed, IndexPath)
	}
	if len(out.Indexed) != 1 || out.Indexed[0] != "my-story" {
		t.Errorf("indexed = %v", out.Indexed)
	}
	// 内容未变：再次检索不写盘、不重建。
	out, changed, _ = run(t, root, "守灯人", "")
	if len(changed) != 0 || len(out.Indexed) != 0 || len(out.Results) != 1 {
		t.Errorf("second run: changed = %v, indexed = %v, results = %d", changed, out.Indexed, len(out.Results))
	}
}

func TestRunNoResultsAndEmptyQuery(t *testing.T) {
	root := newWorkspace(t, "my-story")
	doneWork(t, root, "my-story", nil, map[string]string{"": "正文"})
	out, changed, ps := run(t, root, "无踪之物", "")
	// 首次检索建立索引（changed 含缓存文件），但没有命中。
	if len(ps) != 0 || len(out.Results) != 0 || !equal(changed, []string{IndexPath}) {
		t.Fatalf("results = %+v, problems = %+v, changed = %v", out.Results, ps, changed)
	}
	if out.Results == nil || out.Indexed == nil {
		t.Error("results 与 indexed 不得为 null")
	}
	_, _, ps = Run(root, Input{Query: "  "})
	if len(ps) != 1 || ps[0].Code != "input.invalid-value" || ps[0].Field != "query" {
		t.Fatalf("problems = %+v, want input.invalid-value on query", ps)
	}
}

func TestRunNonDoneNotIndexed(t *testing.T) {
	root := newWorkspace(t, "my-story")
	// 根单元停在 brief：正文存在但未验收，不进入索引。
	writeFile(t, filepath.Join(root, work.Dir("my-story"), "draft.md"), "守灯人出现在草稿里。")
	out, _, ps := run(t, root, "守灯人", "")
	if len(ps) != 0 || len(out.Results) != 0 {
		t.Fatalf("results = %+v, problems = %+v", out.Results, ps)
	}
}

func TestRunAliasAndEntryID(t *testing.T) {
	root := newWorkspace(t, "my-story")
	doneWork(t, root, "my-story", nil, map[string]string{"": "守灯人守着灯。\n\n夜里只有老灯匠。"})
	writeCanon(t, root, "my-story", "schema_version: 1\nentries:\n"+
		"  - id: canon-1\n    type: character\n    name: 守灯人\n    aliases: [老灯匠]\n    text: 五十岁\n    sources: []\n    protection: normal\n")
	// 按别名检索：词集扩展为该条目全部称谓，两段都命中并标注条目 id。
	out, _, ps := run(t, root, "老灯匠", "")
	if len(ps) != 0 || len(out.Results) != 2 {
		t.Fatalf("alias results = %+v, problems = %+v", out.Results, ps)
	}
	if got := out.Results[1]; got.Entry == nil || *got.Entry != "canon-1" || got.Matched != "老灯匠" || got.Paragraph != 2 {
		t.Errorf("hit = %+v", got)
	}
	if got := out.Results[0]; got.Matched != "守灯人" || got.Paragraph != 1 {
		t.Errorf("hit = %+v", got)
	}
	// 条目 id：检索 name 与别名的并集；同一段落多个称谓只输出一项，matched 为首次出现的称谓。
	out, _, _ = run(t, root, "canon-1", "")
	if len(out.Results) != 2 {
		t.Fatalf("entry id results = %+v", out.Results)
	}
	if out.Results[0].Paragraph != 1 || out.Results[0].Matched != "守灯人" || *out.Results[0].Entry != "canon-1" {
		t.Errorf("first hit = %+v", out.Results[0])
	}
}

func TestRunCrossWorkIDs(t *testing.T) {
	root := newWorkspace(t, "a-story")
	doneWork(t, root, "a-story", nil, map[string]string{"": "守灯人擦灯。"})
	if _, _, ps := work.Create(root, "minimal", "b-story"); len(ps) != 0 {
		t.Fatal(ps)
	}
	doneWork(t, root, "b-story", nil, map[string]string{"": "黑塔立在山口。"})
	writeCanon(t, root, "a-story", "schema_version: 1\nentries:\n"+
		"  - id: canon-1\n    type: character\n    name: 守灯人\n    text: 五十岁\n    sources: []\n    protection: normal\n")
	writeCanon(t, root, "b-story", "schema_version: 1\nentries:\n"+
		"  - id: canon-1\n    type: location\n    name: 黑塔\n    text: 要塞\n    sources: []\n    protection: normal\n")
	out, _, ps := run(t, root, "canon-1", "")
	if len(ps) != 0 || len(out.Results) != 2 {
		t.Fatalf("results = %+v, problems = %+v", out.Results, ps)
	}
	if out.Results[0].Work != "a-story" || out.Results[0].Matched != "守灯人" || out.Results[1].Work != "b-story" || out.Results[1].Matched != "黑塔" {
		t.Errorf("hits = %+v", out.Results)
	}
	if *out.Results[0].Entry != "canon-1" || *out.Results[1].Entry != "canon-1" {
		t.Errorf("entry ids = %+v", out.Results)
	}
}

func TestRunWorkScope(t *testing.T) {
	root := newWorkspace(t, "a-story")
	doneWork(t, root, "a-story", nil, map[string]string{"": "守灯人。"})
	if _, _, ps := work.Create(root, "minimal", "b-story"); len(ps) != 0 {
		t.Fatal(ps)
	}
	doneWork(t, root, "b-story", nil, map[string]string{"": "守灯人。"})
	out, _, ps := run(t, root, "守灯人", "a-story")
	if len(ps) != 0 || len(out.Results) != 1 || out.Results[0].Work != "a-story" {
		t.Fatalf("results = %+v, problems = %+v", out.Results, ps)
	}
	// --work 只接受作品根单元 id：指向子单元或不存在的产品按引用解析的问题报告。
	_, _, ps = Run(root, Input{Query: "守灯人", Work: "a-story/s1"})
	if len(ps) == 0 || ps[0].Severity != workspace.SeverityError {
		t.Fatalf("child --work problems = %+v", ps)
	}
}

func TestRunSameNameFallsBackToKeyword(t *testing.T) {
	root := newWorkspace(t, "my-story")
	doneWork(t, root, "my-story", nil, map[string]string{"": "年轻的守灯人值夜。"})
	// 场景级上下文包允许同名条目：同一 name 命中两个条目时不做条目解析，按原词检索。
	writeCanon(t, root, "my-story", "schema_version: 1\nentries:\n"+
		"  - id: canon-1\n    type: character\n    name: 守灯人\n    text: 年轻\n    sources: []\n    protection: normal\n"+
		"  - id: canon-2\n    type: character\n    name: 守灯人\n    text: 年老\n    sources: []\n    protection: normal\n")
	out, _, ps := run(t, root, "守灯人", "")
	if len(ps) != 0 || len(out.Results) != 1 {
		t.Fatalf("results = %+v, problems = %+v", out.Results, ps)
	}
	if out.Results[0].Entry != nil || out.Results[0].Matched != "守灯人" {
		t.Errorf("hit = %+v：同名条目不唯一时按原词检索", out.Results[0])
	}
}

func TestRunLatinPartialSubstring(t *testing.T) {
	root := newWorkspace(t, "my-story")
	doneWork(t, root, "my-story", nil, map[string]string{"": "JSON 配置生效。"})
	// 非 CJK 的词条按整词索引：查询是正文单词的一段（son ⊂ json）时词条判不出，仍须以子串复核命中。
	out, _, ps := run(t, root, "son", "")
	if len(ps) != 0 || len(out.Results) != 1 {
		t.Fatalf("results = %+v, problems = %+v", out.Results, ps)
	}
	if out.Results[0].Matched != "son" {
		t.Errorf("matched = %q", out.Results[0].Matched)
	}
}

func TestRunIDAliasCollisionFallsBack(t *testing.T) {
	root := newWorkspace(t, "my-story")
	doneWork(t, root, "my-story", nil, map[string]string{"": "长命灯亮着。"})
	// 查询词同时等于一个条目的 id 与另一个条目的别名：命中不唯一，回退按原词检索。
	writeCanon(t, root, "my-story", "schema_version: 1\nentries:\n"+
		"  - id: canon-1\n    type: character\n    name: 守灯人\n    text: a\n    sources: []\n    protection: normal\n"+
		"  - id: canon-2\n    type: item\n    name: 长命灯\n    aliases: [canon-1]\n    text: b\n    sources: []\n    protection: normal\n")
	out, _, ps := run(t, root, "canon-1", "")
	if len(ps) != 0 || len(out.Results) != 0 {
		t.Fatalf("results = %+v, problems = %+v", out.Results, ps)
	}
	// 正文含 canon-1 时按原词命中，entry 为 null。
	doneWork(t, root, "my-story", nil, map[string]string{"": "canon-1 编号的长命灯。"})
	out, _, _ = run(t, root, "canon-1", "")
	if len(out.Results) != 1 || out.Results[0].Entry != nil || out.Results[0].Matched != "canon-1" {
		t.Errorf("results = %+v：命中不唯一时按原词检索", out.Results)
	}
}

func TestScopedRefreshKeepsOtherWorks(t *testing.T) {
	root := newWorkspace(t, "a-story")
	doneWork(t, root, "a-story", nil, map[string]string{"": "守灯人。"})
	if _, _, ps := work.Create(root, "minimal", "b-story"); len(ps) != 0 {
		t.Fatal(ps)
	}
	doneWork(t, root, "b-story", nil, map[string]string{"": "黑塔。"})
	run(t, root, "守灯人", "")
	// --work 限定的检索不重写缓存：范围外作品的条目原样保留。
	_, changed, _ := run(t, root, "黑塔", "a-story")
	if len(changed) != 0 {
		t.Errorf("scoped recall changedPaths = %v, want empty", changed)
	}
	out, changed, _ := run(t, root, "黑塔", "")
	if len(changed) != 0 || len(out.Indexed) != 0 {
		t.Errorf("full recall after scoped: changed = %v, indexed = %v：范围外作品不应被重建", changed, out.Indexed)
	}
}

func TestTooManyParagraphsSkipped(t *testing.T) {
	root := newWorkspace(t, "my-story")
	var b strings.Builder
	for i := 0; i < 10001; i++ {
		b.WriteString("一句正文。\n\n")
	}
	doneWork(t, root, "my-story", nil, map[string]string{"": b.String()})
	out, _, ps := run(t, root, "一句正文", "")
	if len(out.Results) != 0 {
		t.Errorf("results = %+v, want skipped", out.Results)
	}
	warned := false
	for _, p := range ps {
		if p.Code == "draft.too-large" && p.Severity == workspace.SeverityWarning {
			warned = true
		}
	}
	if !warned {
		t.Errorf("problems = %+v, want draft.too-large warning", ps)
	}
}

func TestRunInvalidCanonDegrades(t *testing.T) {
	root := newWorkspace(t, "my-story")
	doneWork(t, root, "my-story", nil, map[string]string{"": "守灯人守着灯。"})
	writeCanon(t, root, "my-story", "schema_version: 1\nentries:\n"+
		"  - id: canon-1\n    type: character\n    name: 守灯人\n    text: a\n    sources: []\n    protection: normal\n"+
		"  - id: canon-1\n    type: character\n    name: 重复\n    text: b\n    sources: []\n    protection: normal\n")
	out, _, ps := run(t, root, "守灯人", "")
	if len(ps) == 0 || ps[0].Severity != workspace.SeverityError {
		t.Fatalf("problems = %+v, want error from canon.yaml", ps)
	}
	if len(out.Results) != 1 || out.Results[0].Entry != nil {
		t.Errorf("results = %+v：坏 canon 的作品仍按原词检索、不做别名扩展", out.Results)
	}
}

func TestRefreshIncrementalAndRemoval(t *testing.T) {
	root := newWorkspace(t, "my-story")
	doneWork(t, root, "my-story", []string{"s1", "s2"}, map[string]string{
		"":  "守灯人开头。",
		"s1": "老灯匠在阁楼。",
		"s2": "守灯人收灯。",
	})
	out, _, _ := run(t, root, "守灯人", "")
	if len(out.Results) != 2 {
		t.Fatalf("results = %+v", out.Results)
	}
	// 作者直接改 s1 的正文：下一次检索只重建 s1，旧称谓不再命中 s1。
	writeFile(t, filepath.Join(root, work.Dir("my-story"), "units", "s1", "draft.md"), "阁楼空了。")
	out, _, _ = run(t, root, "阁楼", "")
	if !equal(out.Indexed, []string{"my-story/s1"}) {
		t.Errorf("indexed = %v, want only my-story/s1", out.Indexed)
	}
	out, _, _ = run(t, root, "守灯人", "")
	if len(out.Results) != 2 || out.Results[1].Unit != "my-story/s2" {
		t.Errorf("results = %+v：s2 与根单元命中，s1 不再命中", out.Results)
	}
	// 删除子单元：其出现随之移除（s1 已不在单元树中，只剩根单元与 s2）。
	writeFile(t, filepath.Join(root, work.Path("my-story")),
		"schema_version: 1\nid: my-story\ngenre: minimal\ntype: article\nstage: done\nunits:\n"+
			"  - id: s2\n    type: section\n    stage: done\n")
	out, _, _ = run(t, root, "守灯人", "")
	if len(out.Results) != 2 || out.Results[0].Unit != "my-story" || out.Results[1].Unit != "my-story/s2" {
		t.Errorf("results = %+v, want root and s2 only", out.Results)
	}
	if out, _, _ = run(t, root, "老灯匠", ""); len(out.Results) != 0 {
		t.Errorf("removed unit still searchable: %+v", out.Results)
	}
}

func TestCorruptedIndexRebuilds(t *testing.T) {
	root := newWorkspace(t, "my-story")
	doneWork(t, root, "my-story", nil, map[string]string{"": "守灯人把灯点亮。"})
	if _, _, ps := run(t, root, "守灯人", ""); len(ps) != 0 {
		t.Fatal(ps)
	}
	writeFile(t, filepath.Join(root, IndexPath), "{{{不是合法索引")
	out, _, ps := run(t, root, "守灯人", "")
	if len(ps) != 0 || len(out.Results) != 1 {
		t.Fatalf("results = %+v, problems = %+v：损坏索引应全量重建并照常检索", out.Results, ps)
	}
	// 版本不符同样重建。
	writeFile(t, filepath.Join(root, IndexPath), "schema_version: 99\nworks: []\n")
	out, _, ps = run(t, root, "守灯人", "")
	if len(ps) != 0 || len(out.Results) != 1 {
		t.Fatalf("results = %+v, problems = %+v：版本不符应全量重建", out.Results, ps)
	}
}

func TestMatchCaseInsensitiveAndSingleRune(t *testing.T) {
	root := newWorkspace(t, "my-story")
	doneWork(t, root, "my-story", nil, map[string]string{"": "JSON 配置生效。\n\n灯，又亮了。"})
	out, _, ps := run(t, root, "json", "")
	if len(ps) != 0 || len(out.Results) != 1 || out.Results[0].Paragraph != 1 {
		t.Fatalf("case-insensitive results = %+v, problems = %+v", out.Results, ps)
	}
	// 单个汉字没有二元组词条：退化为线性扫描，仍要命中。
	out, _, ps = run(t, root, "灯", "")
	if len(ps) != 0 || len(out.Results) != 1 || out.Results[0].Paragraph != 2 {
		t.Fatalf("single-rune results = %+v, problems = %+v", out.Results, ps)
	}
}

func TestSnippetWindow(t *testing.T) {
	long := strings.Repeat("夜风穿过走廊。", 60) + "守灯人" + strings.Repeat("夜色沉了下去。", 60)
	pos := len([]rune(strings.Repeat("夜风穿过走廊。", 60))) // 命中位置的码点下标
	s := snippet(long, pos)
	rs := []rune(s)
	if len(rs) > snippetWidth+2 {
		t.Errorf("snippet = %d runes, want at most %d + ellipses", len(rs), snippetWidth)
	}
	if !strings.HasPrefix(s, "…") || !strings.HasSuffix(s, "…") {
		t.Errorf("truncated snippet should carry ellipses on both ends: %q", s)
	}
	if !strings.Contains(s, "守灯人") {
		t.Error("snippet should contain the match")
	}
	short := "短短一句守灯人。"
	if got := snippet(short, strings.Index(short, "守灯人")); got != short {
		t.Errorf("snippet(short) = %q, want the whole text without ellipses", got)
	}
}

func TestSymlinkedDraftSkipped(t *testing.T) {
	root := newWorkspace(t, "my-story")
	doneWork(t, root, "my-story", nil, map[string]string{"": "守灯人把灯点亮。"})
	target := filepath.Join(root, "outside.md")
	writeFile(t, target, "守灯人在项目目录之外。")
	unitDraft := filepath.Join(root, work.Dir("my-story"), "draft.md")
	if err := os.Remove(unitDraft); err != nil {
		t.Fatal(err)
	}
	if err := os.Symlink(target, unitDraft); err != nil {
		t.Fatal(err)
	}
	out, _, ps := run(t, root, "守灯人", "")
	if len(ps) != 0 || len(out.Results) != 0 {
		t.Fatalf("results = %+v, problems = %+v：途经符号链接的正文按缺失处理", out.Results, ps)
	}
}
