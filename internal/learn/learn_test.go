package learn

import (
	"os"
	"path/filepath"
	"runtime"
	"slices"
	"strings"
	"testing"
	"time"

	"github.com/kw12121212/writing-anything/internal/catalog"
	"github.com/kw12121212/writing-anything/internal/packinstall"
	"github.com/kw12121212/writing-anything/internal/resolve"
	"github.com/kw12121212/writing-anything/internal/source"
	"github.com/kw12121212/writing-anything/internal/workspace"
)

var now = time.Date(2026, 10, 2, 8, 0, 0, 0, time.UTC)

func newRoot(t *testing.T) string {
	t.Helper()
	root := t.TempDir()
	if _, ps := workspace.Init(root, nil); len(ps) > 0 {
		t.Fatal(ps)
	}
	return root
}

func addSource(t *testing.T, root, id, typ, content string) {
	t.Helper()
	if _, _, ps := source.Add(root, []byte(content), typ, "", id, "a.md"); len(ps) > 0 {
		t.Fatal(ps)
	}
}

func codes(ps []workspace.Problem) []string {
	var out []string
	for _, p := range ps {
		out = append(out, p.Code)
	}
	return out
}

func TestComputeStats(t *testing.T) {
	s := Compute([][]byte{[]byte("今天下雨。我们出门了吗？没有！")})
	if s.Chars != 15 || s.Sentences.Count != 3 || s.Sentences.Mean != 5 || s.Sentences.Median != 5 || s.Sentences.P10 != 3 || s.Sentences.P90 != 7 || s.Sentences.Stdev != 1.63 {
		t.Fatalf("sentences = %+v", s)
	}
	if len(s.Punctuation) != 3 || s.Punctuation["。"] != 66.67 || s.Punctuation["？"] != 66.67 || s.Paragraphs.Count != 1 || s.Paragraphs.Mean != 15 {
		t.Fatalf("punctuation = %+v", s)
	}
	// 句末标点后的右引号归入该句；换行也切分句子。
	if got := splitSentences("他说：“走吧。”然后走了\n第二行"); !slices.Equal(got, []int{8, 4, 3}) {
		t.Fatalf("closers = %v", got)
	}
	if got := splitSentences("真的？！好……"); !slices.Equal(got, []int{4, 3}) {
		t.Fatalf("repeated enders = %v", got)
	}
	terms := Compute([][]byte{[]byte("规范包很好。规范包很稳。规范包很新。规范包有用。Agent agent AGENT 写作")}).Terms
	var names []string
	for _, tm := range terms {
		names = append(names, tm.Term)
	}
	if !slices.Contains(names, "规范包") || slices.Contains(names, "规范") || slices.Contains(names, "范包") || !slices.Contains(names, "agent") || terms[0].Term != "规范包" || terms[0].Count != 4 {
		t.Fatalf("terms = %+v", terms)
	}
	if s := Compute(nil); s.Chars != 0 || s.Sentences.Count != 0 || s.Terms == nil || s.Punctuation == nil {
		t.Fatalf("empty = %+v", s)
	}
	// 确定性：同样的输入得到同样的结果。
	a, b := Compute([][]byte{[]byte(strings.Repeat("今天我们写作。明天我们改稿！\n\n", 20))}), Compute([][]byte{[]byte(strings.Repeat("今天我们写作。明天我们改稿！\n\n", 20))})
	if !slices.Equal(a.Terms, b.Terms) || len(a.Terms) == 0 || len(a.Terms) > 20 {
		t.Fatalf("terms not deterministic: %+v %+v", a.Terms, b.Terms)
	}
}

func TestLearnLifecycle(t *testing.T) {
	root := newRoot(t)
	addSource(t, root, "a1", "past-article", "今天下雨。我们出门了吗？没有！")
	addSource(t, root, "a2", "past-article", "第二篇。\n\n很短。")
	addSource(t, root, "a3", "past-article", "第三篇文章。")
	config, _ := os.ReadFile(filepath.Join(root, workspace.ConfigPath))
	pr, changed, ps := Learn(root, Request{ID: "me", Pack: "mine", Sources: []string{"a1", "a2"}, Styles: []string{" 多用短句 "}, Avoids: []string{"赋能"}})
	if ps != nil || !slices.Equal(changed, []string{".writing-anything/proposals/me.yaml"}) || pr.Pack != "mine" || !slices.Equal(pr.Sources, []string{"a1", "a2"}) {
		t.Fatalf("create: %+v %v %+v", pr, changed, ps)
	}
	var ids []string
	for _, it := range pr.Items {
		ids = append(ids, it.ID+":"+it.Kind+":"+it.Text+it.Term+":"+it.Status)
	}
	if !slices.Equal(ids, []string{"baseline:baseline::proposed", "style-1:style:多用短句:proposed", "avoid-1:banned-term:赋能:proposed"}) {
		t.Fatalf("items = %v", ids)
	}
	// 未确认的提议不影响规则。
	if after, _ := os.ReadFile(filepath.Join(root, workspace.ConfigPath)); string(after) != string(config) {
		t.Fatal("config changed")
	}
	if _, err := os.Stat(filepath.Join(root, workspace.PacksPath)); err == nil {
		t.Fatal("packs/ created by learn")
	}
	// 没有变化时不写文件。
	if _, changed, ps := Learn(root, Request{ID: "me"}); ps != nil || changed != nil {
		t.Fatalf("unchanged: %v %+v", changed, ps)
	}

	pr, changed, ps = Confirm(root, "me", []string{"baseline", "style-1"}, "作者：就按这个来", now)
	if ps != nil || !slices.Equal(changed, []string{".writing-anything/packs/mine.yaml", ".writing-anything/proposals/me.yaml"}) ||
		pr.Find("style-1").Status != "confirmed" || pr.Find("style-1").Confirmation != "作者：就按这个来" || pr.Find("baseline").DecidedAt != "2026-10-02T08:00:00Z" {
		t.Fatalf("confirm: %+v %v %+v", pr, changed, ps)
	}
	pack, ps := catalog.LoadPackFile(root, ".writing-anything/packs/mine.yaml")
	if ps != nil || pack.Title != "mine 的文风" || len(pack.Style) != 1 || pack.Style[0].Text != "多用短句" || pack.Style[0].AppliesTo != nil || len(pack.BannedTerms) != 0 || pack.StyleProfile["chars"] != float64(pr.Stats.Chars) {
		t.Fatalf("pack = %+v %+v", pack, ps)
	}
	if sources, _ := pack.StyleProfile["sources"].([]any); len(sources) != 2 {
		t.Fatalf("profile sources = %v", pack.StyleProfile)
	}
	// 再次确认已确认的条目：不写文件。
	if _, changed, ps := Confirm(root, "me", []string{"style-1"}, "再确认", now); ps != nil || changed != nil {
		t.Fatalf("again: %v %+v", changed, ps)
	}

	// 拒绝后不再重复提出；已确认的不能拒绝。
	if _, _, ps := Reject(root, "me", []string{"avoid-1"}, now); ps != nil {
		t.Fatal(ps)
	}
	_, _, warnings := Learn(root, Request{ID: "me", Avoids: []string{"赋能"}})
	if !slices.Equal(codes(warnings), []string{"proposal.previously-rejected"}) || warnings[0].Severity != "warning" {
		t.Fatalf("rejected: %+v", warnings)
	}
	if _, _, ps := Reject(root, "me", []string{"style-1"}, now); !slices.Equal(codes(ps), []string{"proposal.already-confirmed"}) {
		t.Fatalf("reject confirmed: %+v", ps)
	}
	// 被拒绝的可以再确认：写入 banned_terms。
	if _, _, ps := Confirm(root, "me", []string{"avoid-1"}, "作者改主意了", now); ps != nil {
		t.Fatal(ps)
	}
	if pack, _ := catalog.LoadPackFile(root, ".writing-anything/packs/mine.yaml"); len(pack.BannedTerms) != 1 || pack.BannedTerms[0].Term != "赋能" {
		t.Fatalf("banned = %+v", pack.BannedTerms)
	}

	// 追加素材：重新计算，baseline 回到 proposed。
	pr, _, ps = Learn(root, Request{ID: "me", Sources: []string{"a3", "a1"}})
	if ps != nil || !slices.Equal(pr.Sources, []string{"a1", "a2", "a3"}) || pr.Find("baseline").Status != "proposed" || pr.Find("baseline").Confirmation != "" || pr.Find("style-1").Status != "confirmed" {
		t.Fatalf("more sources: %+v %+v", pr, ps)
	}
	if checked, ps := CheckAll(root); len(checked) != 1 || ps != nil {
		t.Fatalf("check: %v %+v", checked, ps)
	}
}

func TestLearnRejects(t *testing.T) {
	root := newRoot(t)
	addSource(t, root, "a1", "past-article", "文章。")
	addSource(t, root, "n1", "author-note", "笔记。")
	addSource(t, root, "bin", "past-article", "\xff\xfe")
	tests := []struct {
		req  Request
		code string
	}{
		{Request{ID: "me", Sources: []string{"a1"}}, "proposal.missing-pack"},
		{Request{ID: "me", Pack: "mine"}, "proposal.no-sources"},
		{Request{ID: "me", Pack: "mine", Sources: []string{"nope"}}, "proposal.unknown-source"},
		{Request{ID: "me", Pack: "mine", Sources: []string{"n1"}}, "proposal.not-past-article"},
		{Request{ID: "me", Pack: "mine", Sources: []string{"bin"}}, "proposal.source-not-text"},
		{Request{ID: "me", Pack: "default", Sources: []string{"a1"}}, "pack.id-conflict"},
	}
	for _, tt := range tests {
		if _, _, ps := Learn(root, tt.req); !slices.Equal(codes(ps), []string{tt.code}) {
			t.Errorf("%+v: %+v, want %s", tt.req, ps, tt.code)
		}
	}
	if _, err := os.Stat(filepath.Join(root, Dir)); err == nil {
		t.Fatal("proposals/ created on failure")
	}
	if _, _, ps := Learn(root, Request{ID: "me", Pack: "mine", Sources: []string{"a1"}}); ps != nil {
		t.Fatal(ps)
	}
	if _, _, ps := Learn(root, Request{ID: "me", Pack: "other"}); !slices.Equal(codes(ps), []string{"proposal.pack-mismatch"}) {
		t.Fatalf("mismatch: %+v", ps)
	}
	// 素材原文被改动。
	s, _ := source.Load(root, "a1")
	os.WriteFile(filepath.Join(root, filepath.FromSlash(s.FilePath())), []byte("改了"), 0o644)
	if _, _, ps := Learn(root, Request{ID: "me"}); !slices.Equal(codes(ps), []string{"source.modified"}) {
		t.Fatalf("modified: %+v", ps)
	}
	big := strings.Repeat("字", MaxChars+1)
	addSource(t, root, "big", "past-article", big)
	if _, _, ps := Learn(root, Request{ID: "big", Pack: "mine", Sources: []string{"big"}}); !slices.Equal(codes(ps), []string{"proposal.too-large"}) {
		t.Fatalf("too large: %+v", ps)
	}
	for _, f := range []func() []workspace.Problem{
		func() []workspace.Problem {
			_, _, ps := Confirm(root, "nope", []string{"baseline"}, "c", now)
			return ps
		},
		func() []workspace.Problem { _, _, ps := Reject(root, "nope", []string{"baseline"}, now); return ps },
	} {
		if ps := f(); !slices.Equal(codes(ps), []string{"proposal.not-found"}) {
			t.Fatalf("not found: %+v", ps)
		}
	}
	if _, _, ps := Confirm(root, "me", []string{"x"}, "c", now); !slices.Equal(codes(ps), []string{"proposal.unknown-item"}) || !strings.Contains(ps[0].Message, "baseline") {
		t.Fatalf("unknown item: %+v", ps)
	}
	if _, _, ps := Reject(root, "me", []string{"x"}, now); !slices.Equal(codes(ps), []string{"proposal.unknown-item"}) {
		t.Fatalf("reject unknown: %+v", ps)
	}
}

func TestConfirmTargets(t *testing.T) {
	root := newRoot(t)
	addSource(t, root, "a1", "past-article", "今天下雨。")
	Learn(root, Request{ID: "me", Pack: "mine", Sources: []string{"a1"}, Styles: []string{"多用短句"}})
	// 作者已有的规范包：只追加，保留注释与其他字段。
	packFile := filepath.Join(root, ".writing-anything/packs/mine.yaml")
	os.MkdirAll(filepath.Dir(packFile), 0o755)
	os.WriteFile(packFile, []byte("# 我的规范\nschema_version: 1\nid: mine\ntitle: 我的\nstyle: [少用形容词] # 行尾注释\nglossary:\n  - {term: 规范包}\n"), 0o644)
	if _, _, ps := Confirm(root, "me", []string{"style-1", "baseline"}, "作者确认", now); ps != nil {
		t.Fatal(ps)
	}
	got, _ := os.ReadFile(packFile)
	for _, want := range []string{"# 我的规范", "title: 我的", "- 少用形容词", "- 多用短句", "行尾注释", "term: 规范包", "style_profile:"} {
		if !strings.Contains(string(got), want) {
			t.Fatalf("pack lacks %q:\n%s", want, got)
		}
	}
	run := func(cfg string) *resolve.Resolved {
		os.WriteFile(filepath.Join(root, workspace.ConfigPath), []byte(cfg), 0o644)
		r, ps := resolve.Resolve(root)
		if ps != nil {
			t.Fatal(ps)
		}
		return r
	}
	if r := run("schema_version: 1\nlanguage: zh-CN\npacks: [mine]\ngate_level: standard\n"); r.StyleProfile == nil || r.StyleProfile.Source != "pack:mine" {
		t.Fatalf("resolved = %+v", r.StyleProfile)
	}

	// 已安装（有锁定条目）的规范包不能作为目标。
	other := filepath.Join(t.TempDir(), "pack.yaml")
	os.WriteFile(other, []byte("schema_version: 1\nid: theirs\ntitle: 第三方\n"), 0o644)
	runInstall(t, root, other)
	if _, _, ps := Learn(root, Request{ID: "t", Pack: "theirs", Sources: []string{"a1"}}); !slices.Equal(codes(ps), []string{"proposal.installed-pack"}) {
		t.Fatalf("installed learn: %+v", ps)
	}
	theirs := filepath.Join(root, ".writing-anything/packs/theirs.yaml")
	handWritten := strings.NewReplacer("pack: mine", "pack: theirs", "id: me", "id: t").Replace(mustRead(t, filepath.Join(root, Path("me"))))
	os.WriteFile(filepath.Join(root, Path("t")), []byte(handWritten), 0o644)
	before := mustRead(t, theirs)
	if _, _, ps := Confirm(root, "t", []string{"style-1"}, "c", now); !slices.Equal(codes(ps), []string{"proposal.installed-pack"}) {
		t.Fatalf("confirm into installed pack: %+v", ps)
	}
	if mustRead(t, theirs) != before || mustRead(t, filepath.Join(root, Path("t"))) != handWritten {
		t.Fatal("files changed on installed-pack refusal")
	}
	// 手写提议指向内置规范包时，confirm 同样拒绝。
	os.WriteFile(filepath.Join(root, Path("t")), []byte(strings.Replace(handWritten, "pack: theirs", "pack: default", 1)), 0o644)
	if _, _, ps := Confirm(root, "t", []string{"style-1"}, "c", now); !slices.Equal(codes(ps), []string{"pack.id-conflict"}) {
		t.Fatalf("confirm into builtin pack: %+v", ps)
	}

	// 锚点：不修改。
	os.WriteFile(packFile, []byte("schema_version: 1\nid: mine\ntitle: 我的\nstyle: &s [a]\n"), 0o644)
	Learn(root, Request{ID: "me", Styles: []string{"再一条"}})
	if _, _, ps := Confirm(root, "me", []string{"style-2"}, "c", now); !slices.Equal(codes(ps), []string{"proposal.anchored-value"}) {
		t.Fatalf("anchored: %+v", ps)
	}
	// 写出后的规范包不合法（如原文件已有问题）时不写。
	os.WriteFile(packFile, []byte("schema_version: 1\nid: mine\ntitle: 我的\nrules:\n  r1: {type: run-shell}\n"), 0o644)
	if _, _, ps := Confirm(root, "me", []string{"style-2"}, "c", now); !slices.Equal(codes(ps), []string{"rule.unknown-type"}) {
		t.Fatalf("invalid pack: %+v", ps)
	}
	if pr, _ := Load(root, Path("me")); pr.Find("style-2").Status != "proposed" {
		t.Fatal("proposal updated although the pack was not written")
	}
}

func mustRead(t *testing.T, path string) string {
	t.Helper()
	data, err := os.ReadFile(path)
	if err != nil {
		t.Fatal(err)
	}
	return string(data)
}

func TestProposalValidation(t *testing.T) {
	root := newRoot(t)
	addSource(t, root, "a1", "past-article", "今天下雨。")
	Learn(root, Request{ID: "me", Pack: "mine", Sources: []string{"a1"}})
	data := mustRead(t, filepath.Join(root, Path("me")))
	os.WriteFile(filepath.Join(root, Path("you")), []byte(data), 0o644)
	if _, ps := CheckAll(root); !slices.Equal(codes(ps), []string{"proposal.id-mismatch"}) || ps[0].Field != "id" {
		t.Fatalf("mismatch: %+v", ps)
	}
	os.Remove(filepath.Join(root, Path("you")))
	os.WriteFile(filepath.Join(root, Path("me")), []byte(data+"  - {id: baseline, kind: baseline, status: proposed}\n"), 0o644)
	if ps := CheckArtifact(root, Path("me")); !slices.Equal(codes(ps), []string{"artifact.duplicate-id"}) {
		t.Fatalf("duplicate: %+v", ps)
	}
	os.WriteFile(filepath.Join(root, Path("me")), []byte("items: ["), 0o644)
	if _, ps := Load(root, Path("me")); !slices.Equal(codes(ps), []string{"artifact.parse-error"}) {
		t.Fatalf("parse: %+v", ps)
	}
	if runtime.GOOS != "windows" {
		os.Remove(filepath.Join(root, Path("me")))
		os.Symlink(filepath.Join(root, workspace.ConfigPath), filepath.Join(root, Path("me")))
		if _, ps := Load(root, Path("me")); !slices.Equal(codes(ps), []string{"artifact.not-found"}) {
			t.Fatalf("symlink: %+v", ps)
		}
		os.Remove(filepath.Join(root, Path("me")))
		os.RemoveAll(filepath.Join(root, Dir))
		os.Symlink(t.TempDir(), filepath.Join(root, Dir))
		if _, _, ps := Learn(root, Request{ID: "x", Pack: "mine", Sources: []string{"a1"}}); !slices.Equal(codes(ps), []string{"workspace.write-failed"}) {
			t.Fatalf("symlinked dir: %+v", ps)
		}
	}
}

func runInstall(t *testing.T, root, src string) {
	t.Helper()
	if _, ps := packinstall.Install(packinstall.Env{}, root, src, ""); ps != nil {
		t.Fatal(ps)
	}
}

func TestTermOrderAndLimit(t *testing.T) {
	var b strings.Builder
	words := []string{"alpha", "bravo", "charlie", "delta", "echo", "foxtrot", "golf", "hotel", "india", "juliet", "kilo", "lima",
		"mike", "november", "oscar", "papa", "quebec", "romeo", "sierra", "tango", "uniform", "victor"}
	for i, w := range words {
		n := 3
		if i < 2 {
			n = 5 // 次数多的排在前面
		}
		for range n {
			b.WriteString(w + " ")
		}
	}
	terms := Compute([][]byte{[]byte(b.String())}).Terms
	if len(terms) != 20 || terms[0].Term != "alpha" || terms[1].Term != "bravo" || terms[2].Term != "charlie" || terms[19].Term != "tango" {
		t.Fatalf("terms = %+v", terms)
	}
	if !slices.IsSortedFunc(terms[2:], func(a, b Term) int { return strings.Compare(a.Term, b.Term) }) {
		t.Fatalf("ties not in dictionary order: %+v", terms)
	}
	// 撇号与连字符不属于拉丁字母词。
	if terms := Compute([][]byte{[]byte("it's it's it's")}).Terms; len(terms) != 0 {
		t.Fatalf("apostrophes = %+v", terms)
	}
}

func TestConfirmDeduplicatesAgainstPack(t *testing.T) {
	root := newRoot(t)
	addSource(t, root, "a1", "past-article", "今天下雨。")
	Learn(root, Request{ID: "me", Pack: "mine", Sources: []string{"a1"}, Styles: []string{"多用短句"}, Avoids: []string{"赋能"}})
	packFile := filepath.Join(root, ".writing-anything/packs/mine.yaml")
	os.MkdirAll(filepath.Dir(packFile), 0o755)
	os.WriteFile(packFile, []byte("schema_version: 1\nid: mine\ntitle: 我的\nstyle: [多用短句]\nbanned_terms:\n  - {term: 赋能, reason: 空泛}\n"), 0o644)
	_, changed, ps := Confirm(root, "me", []string{"style-1", "avoid-1"}, "作者确认", now)
	if ps != nil || !slices.Equal(changed, []string{Path("me")}) {
		t.Fatalf("confirm: %v %+v", changed, ps)
	}
	pack, _ := catalog.LoadPackFile(root, ".writing-anything/packs/mine.yaml")
	if len(pack.Style) != 1 || len(pack.BannedTerms) != 1 || pack.BannedTerms[0].Reason != "空泛" {
		t.Fatalf("pack = %+v", pack)
	}
}

// 规范包中已有同文的对象形式文风条目（{text, applies_to}）时，确认不再追加字符串条目，保留其适用范围。
func TestConfirmDeduplicatesScopedStyle(t *testing.T) {
	root := newRoot(t)
	addSource(t, root, "a1", "past-article", "今天下雨。")
	Learn(root, Request{ID: "me", Pack: "mine", Sources: []string{"a1"}, Styles: []string{"多用短句", "少用形容词"}})
	packFile := filepath.Join(root, ".writing-anything/packs/mine.yaml")
	os.MkdirAll(filepath.Dir(packFile), 0o755)
	os.WriteFile(packFile, []byte("schema_version: 1\nid: mine\ntitle: 我的\nstyle:\n  - {text: 多用短句, applies_to: [fiction]}\n"), 0o644)
	if _, _, ps := Confirm(root, "me", []string{"style-1", "style-2"}, "作者确认", now); ps != nil {
		t.Fatal(ps)
	}
	pack, ps := catalog.LoadPackFile(root, ".writing-anything/packs/mine.yaml")
	if ps != nil || len(pack.Style) != 2 || pack.Style[0].Text != "多用短句" || !slices.Equal(pack.Style[0].AppliesTo, []string{"fiction"}) ||
		pack.Style[1].Text != "少用形容词" || pack.Style[1].AppliesTo != nil {
		t.Fatalf("pack = %+v %+v", pack, ps)
	}
}

// 手写的提议（流式 stats、条目内部带注释）：没有变化的操作不写文件，改动其他条目时保留未改条目的注释。
func TestHandWrittenProposalKept(t *testing.T) {
	root := newRoot(t)
	addSource(t, root, "a1", "past-article", "今天下雨。")
	Learn(root, Request{ID: "me", Pack: "mine", Sources: []string{"a1"}})
	pr, _ := Load(root, Path("me"))
	stats := mustRead(t, filepath.Join(root, Path("me")))
	stats = stats[strings.Index(stats, "stats:"):strings.Index(stats, "items:")]
	hand := "# 手写的提议\nschema_version: 1\nid: me\npack: mine\nsources: [a1]\n" + stats + "items:\n  - id: baseline # 作者喜欢\n    kind: baseline\n    status: proposed\n  - id: style-1\n    kind: style\n    # 关于文字\n    text: 多用短句\n    status: proposed\n  - &a {id: avoid-1, kind: banned-term, term: 赋能, status: proposed}\n"
	file := filepath.Join(root, Path("me"))
	os.WriteFile(file, []byte(hand), 0o644)
	if _, changed, ps := Learn(root, Request{ID: "me"}); ps != nil || changed != nil || mustRead(t, file) != hand {
		t.Fatalf("no-op learn: %v %+v", changed, ps)
	}
	_ = pr
	if _, _, ps := Reject(root, "me", []string{"baseline"}, now); ps != nil {
		t.Fatalf("reject unrelated to the anchored item: %+v", ps)
	}
	got := mustRead(t, file)
	if !strings.Contains(got, "# 关于文字") || !strings.Contains(got, "# 手写的提议") || !strings.Contains(got, "&a") {
		t.Fatalf("comments lost:\n%s", got)
	}
	// 要改的条目带锚点时拒绝修改。
	if _, _, ps := Reject(root, "me", []string{"avoid-1"}, now); !slices.Equal(codes(ps), []string{"proposal.anchored-value"}) {
		t.Fatalf("anchored item: %+v", ps)
	}
	// baseline 的 id 与 kind 必须一致。
	os.WriteFile(file, []byte(strings.Replace(hand, "  - id: style-1\n", "  - id: baseline-2\n", 1)), 0o644)
	os.WriteFile(file, []byte(strings.Replace(hand, "    kind: baseline\n", "    kind: style\n    text: x\n", 1)), 0o644)
	if ps := CheckArtifact(root, Path("me")); !slices.Equal(codes(ps), []string{"proposal.invalid-baseline"}) {
		t.Fatalf("invalid baseline: %+v", ps)
	}
}
