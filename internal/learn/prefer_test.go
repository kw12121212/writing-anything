package learn

import (
	"time"
	"fmt"
	"os"
	"path/filepath"
	"slices"
	"strings"
	"testing"

	"github.com/kw12121212/writing-anything/internal/catalog"
	"github.com/kw12121212/writing-anything/internal/draft"
	"github.com/kw12121212/writing-anything/internal/feedback"
	"github.com/kw12121212/writing-anything/internal/source"
	"github.com/kw12121212/writing-anything/internal/work"
)

// editedWork 创建作品 post：Agent 写入 agent，作者把正文改为 author（不经 wa-cli）。
func editedWork(t *testing.T, agent, author string) (string, *work.Work, *work.Unit) {
	t.Helper()
	root := newRoot(t)
	if _, _, ps := work.Create(root, "minimal", "post"); len(ps) > 0 {
		t.Fatal(ps)
	}
	w, u, _ := work.Resolve(root, "post")
	if _, _, ps := draft.Write(root, w, u, []byte(agent), "", time.Date(2026, 6, 20, 12, 0, 0, 0, time.UTC)); len(ps) > 0 {
		t.Fatal(ps)
	}
	if author != "" {
		os.WriteFile(filepath.Join(root, ".writing-anything/works/post/draft.md"), []byte(author), 0o644)
	}
	return root, w, u
}

func keys(pr *Proposal) []string {
	var out []string
	for _, it := range pr.Items {
		out = append(out, it.Key)
	}
	return out
}

func TestPreferRemovedTermsAndLength(t *testing.T) {
	agent := "首先，我们需要赋能团队，让大家都能快速上手这个工具。\n\n其次，我们要赋能业务，把流程全部梳理清楚再推进。\n\n最后，赋能用户，让他们愿意持续使用并推荐给别人。\n"
	author := "先让团队上手。\n\n再理清业务流程。\n\n最后让用户愿意用。\n"
	root, _, _ := editedWork(t, agent, author)
	config := mustRead(t, filepath.Join(root, ".writing-anything/config.yaml"))
	pr, obs, changed, ps := Prefer(root, "prefs", "mine")
	if ps != nil || obs.Edits != 3 || obs.Feedback != 0 || !slices.Equal(changed, []string{Path("prefs")}) || pr.Stats != nil || len(pr.Sources) != 0 {
		t.Fatalf("prefer: %+v %+v %v %+v", pr, obs, changed, ps)
	}
	ks := keys(pr)
	if !slices.Contains(ks, "removed:赋能") || !slices.Contains(ks, "length:shorter") || slices.ContainsFunc(ks, func(k string) bool { return k == "removed:赋" || k == "removed:能" }) {
		t.Fatalf("keys = %v", ks)
	}
	avoid := pr.Items[slices.Index(ks, "removed:赋能")]
	if avoid.Kind != KindBannedTerm || avoid.Term != "赋能" || avoid.ID != "avoid-1" || avoid.Status != "proposed" || len(avoid.Evidence) != 3 || avoid.Evidence[0].Unit != "post" {
		t.Fatalf("avoid = %+v", avoid)
	}
	short := pr.Items[slices.Index(ks, "length:shorter")]
	if short.Kind != KindStyle || !strings.Contains(short.Text, "%") || len(short.Evidence) != 3 {
		t.Fatalf("shorter = %+v", short)
	}
	// 只有两个修改对删除的词不算反复删除；作者加入过的词也不算。
	for _, it := range pr.Items {
		if it.Key == "removed:团队" || it.Key == "removed:用户" {
			t.Fatalf("term added back by the author proposed: %+v", it)
		}
	}
	if mustRead(t, filepath.Join(root, ".writing-anything/config.yaml")) != config {
		t.Fatal("config changed")
	}
	if _, err := os.Stat(filepath.Join(root, ".writing-anything/packs")); err == nil {
		t.Fatal("packs/ created")
	}
	// 再次归纳：没有变化时不写文件；被拒绝的 key 不再提出。
	if _, _, changed, ps := Prefer(root, "prefs", ""); ps != nil || changed != nil {
		t.Fatalf("again: %v %+v", changed, ps)
	}
	Reject(root, "prefs", []string{"avoid-1"}, now)
	_, _, _, warnings := Prefer(root, "prefs", "")
	if !slices.Equal(codes(warnings), []string{"proposal.previously-rejected"}) {
		t.Fatalf("rejected: %+v", warnings)
	}
	if pr, _ := Load(root, Path("prefs")); pr.Find("avoid-1").Status != "rejected" || len(pr.Items) != 2 {
		t.Fatalf("after reject: %+v", pr.Items)
	}
	// 确认后写入规范包；pack learn 之后可以给同一提议加入素材。
	if _, _, ps := Confirm(root, "prefs", []string{"style-1"}, "作者：对", now); ps != nil {
		t.Fatal(ps)
	}
	if pack, _ := catalog.LoadPackFile(root, ".writing-anything/packs/mine.yaml"); len(pack.Style) != 1 || !strings.Contains(pack.Style[0], "短") {
		t.Fatalf("pack = %+v", pack)
	}
	addSource(t, root, "a1", "past-article", "今天下雨。")
	pr, _, ps = Learn(root, Request{ID: "prefs", Sources: []string{"a1"}})
	if ps != nil || pr.Stats == nil || pr.Items[0].ID != "baseline" || pr.Find("style-1").Status != "confirmed" {
		t.Fatalf("learn after prefer: %+v %+v", pr, ps)
	}
	// 没有素材时 learn 不生成统计。
	Prefer(root, "only", "mine")
	if pr, _, ps := Learn(root, Request{ID: "only"}); ps != nil || pr.Stats != nil || slices.ContainsFunc(pr.Items, func(it Item) bool { return it.Kind == KindBaseline }) {
		t.Fatalf("learn without sources: %+v %+v", pr, ps)
	}
}

func TestPreferLoggedEditsAccumulate(t *testing.T) {
	root, w, u := editedWork(t, "我们需要赋能团队。\n", "让团队上手。\n")
	// Agent 再次写入：修改对进入 edits.yaml，写入记录被覆盖。
	draftFile := filepath.Join(root, ".writing-anything/works/post/draft.md")
	base := sha(t, draftFile)
	for i, pair := range [][2]string{{"然后赋能业务。\n", "然后理清业务。\n"}, {"最后赋能用户。\n", "最后服务用户。\n"}} {
		if _, _, ps := draft.Write(root, w, u, []byte(pair[0]), base, time.Date(2026, 6, 20, 12, 0, 0, 0, time.UTC)); len(ps) > 0 {
			t.Fatalf("write %d: %+v", i, ps)
		}
		os.WriteFile(draftFile, []byte(pair[1]), 0o644)
		base = sha(t, draftFile)
	}
	edits, _ := draft.LoadEdits(root, w, u)
	if len(edits) != 2 {
		t.Fatalf("logged = %+v", edits)
	}
	pr, obs, _, ps := Prefer(root, "prefs", "mine")
	if ps != nil || obs.Edits != 3 || !slices.Contains(keys(pr), "removed:赋能") {
		t.Fatalf("prefer: %+v %+v %+v", pr, obs, ps)
	}
}

func sha(t *testing.T, path string) string {
	t.Helper()
	data, err := os.ReadFile(path)
	if err != nil {
		t.Fatal(err)
	}
	return source.Hash(data)
}

func TestPreferFeedback(t *testing.T) {
	long := strings.Repeat("很长的一段话", 20)
	body := "短。\n\n短句。\n\n" + long + "甲。" + long + "乙。" + long + "丙。\n\n短短。\n"
	root, w, u := editedWork(t, body, "")
	for i, fb := range []struct{ kind, text, quote string }{
		{"suggestion", "这段太啰嗦", "甲"},
		{"must-fix", "有点啰嗦", "乙"},
		{"question", "啰嗦，删掉？", "丙"},
		{"praise", "啰嗦也挺可爱", ""},
	} {
		if _, _, ps := feedback.Add(root, w, u, fb.kind, fb.text, fb.quote, ""); len(ps) > 0 {
			t.Fatalf("feedback %d: %+v", i, ps)
		}
	}
	pr, obs, _, ps := Prefer(root, "prefs", "mine")
	if ps != nil || obs.Feedback != 3 {
		t.Fatalf("prefer: %+v %+v", obs, ps)
	}
	ks := keys(pr)
	theme := pr.Items[slices.Index(ks, "feedback:啰嗦")]
	if !strings.Contains(theme.Text, "3 条") || len(theme.Evidence) != 3 || !strings.HasPrefix(theme.Evidence[0].Text, "fb-1：") {
		t.Fatalf("theme = %+v", theme)
	}
	if !slices.Contains(ks, "feedback:long-paragraphs") {
		t.Fatalf("keys = %v", ks)
	}
	// 被拒绝的反馈不参与归纳。
	feedback.Resolve(root, w, u, "fb-3", "rejected", "", false)
	if pr, obs, _, _ := Prefer(root, "other", "mine"); obs.Feedback != 2 || slices.Contains(keys(pr), "feedback:啰嗦") {
		t.Fatalf("after reject: %+v %v", obs, keys(pr))
	}
}

func TestPreferRejects(t *testing.T) {
	root := newRoot(t)
	if _, _, _, ps := Prefer(root, "prefs", ""); !slices.Equal(codes(ps), []string{"proposal.missing-pack"}) {
		t.Fatalf("missing pack: %+v", ps)
	}
	if _, _, _, ps := Prefer(root, "prefs", "default"); !slices.Equal(codes(ps), []string{"pack.id-conflict"}) {
		t.Fatalf("builtin: %+v", ps)
	}
	// 没有归纳出任何偏好时不新建空的提议文件。
	if pr, obs, changed, ps := Prefer(root, "prefs", "mine"); ps != nil || obs != (Observed{}) || len(pr.Items) != 0 || changed != nil {
		t.Fatalf("empty workspace: %+v %+v %v %+v", pr, obs, changed, ps)
	}
	if _, err := os.Stat(filepath.Join(root, Path("prefs"))); err == nil {
		t.Fatal("empty proposal created")
	}
	addSource(t, root, "a1", "past-article", "今天下雨。")
	Learn(root, Request{ID: "prefs", Pack: "mine", Sources: []string{"a1"}})
	if _, _, _, ps := Prefer(root, "prefs", "other"); !slices.Equal(codes(ps), []string{"proposal.pack-mismatch"}) {
		t.Fatalf("mismatch: %+v", ps)
	}
}

func mkPairs(befores ...string) []pair {
	var out []pair
	for _, b := range befores {
		out = append(out, pair{unit: "post", Edit: draft.Edit{Before: b, After: "x"}})
	}
	return out
}

func TestRemovedTermsRules(t *testing.T) {
	words := []string{"bravo", "charlie", "delta", "echo", "foxtrot", "golf", "hotel", "india", "juliet", "kilo", "lima"}
	ws := strings.Join(words, " ")
	pairs := mkPairs("alpha "+ws+" 规范包", "alpha "+ws+" 规范包", "alpha "+ws+" 规范包 zulu", "alpha only twice")
	pairs = append(pairs, pair{unit: "post", Edit: draft.Edit{Before: "zulu", After: "zulu zulu"}}) // 作者加入过 zulu
	items := removedTerms(pairs)
	var got []string
	for _, it := range items {
		got = append(got, it.Term)
	}
	// alpha 在 4 对中被删除排第一；其余并列 3 对按字典序；只取前 10 个；规范包去掉了被包含的子串；zulu 被加入过而排除。
	want := []string{"alpha", "bravo", "charlie", "delta", "echo", "foxtrot", "golf", "hotel", "india", "juliet"}
	if !slices.Equal(got, want) {
		t.Fatalf("terms = %v", got)
	}
	all := removedTerms(mkPairs("规范包 x", "规范包 y", "规范包 z"))
	if len(all) != 1 || all[0].Key != "removed:规范包" || len(all[0].Evidence) != 3 {
		t.Fatalf("dedupe = %+v", all)
	}
	if items := removedTerms(mkPairs("规范包", "规范包")); len(items) != 0 {
		t.Fatalf("below threshold = %+v", items)
	}
	// 证据只取真正删除了该词的修改对，且围绕该词截取、不超过 80 个字符。
	long := strings.Repeat("前文", 60) + "赋能" + strings.Repeat("后文", 60)
	ev := removedTerms(mkPairs(long, long+"一", long+"二"))
	for _, it := range ev {
		if it.Term != "赋能" {
			continue
		}
		for _, e := range it.Evidence {
			if n := len([]rune(e.Text)); n > maxEvidenceLength || !strings.Contains(e.Text, "赋能") {
				t.Fatalf("evidence %q (%d runes)", e.Text, n)
			}
		}
	}
}

func TestLengthAndParagraphRules(t *testing.T) {
	shorter := lengthChange([]pair{{"p", draft.Edit{Before: strings.Repeat("字", 100), After: strings.Repeat("字", 60)}},
		{"p", draft.Edit{Before: strings.Repeat("字", 100), After: strings.Repeat("字", 60)}}, {"p", draft.Edit{Before: strings.Repeat("字", 100), After: strings.Repeat("字", 60)}}})
	if len(shorter) != 1 || shorter[0].Key != "length:shorter" || !strings.Contains(shorter[0].Text, "40%") {
		t.Fatalf("shorter = %+v", shorter)
	}
	for _, e := range shorter[0].Evidence {
		if n := len([]rune(e.Text)); n > maxEvidenceLength || !strings.HasPrefix(e.Text, "100 字 → 60 字：") {
			t.Fatalf("evidence %q", e.Text)
		}
	}
	longer := lengthChange([]pair{{"p", draft.Edit{Before: "一二三四", After: "一二三四五六"}}, {"p", draft.Edit{Before: "一二三四", After: "一二三四五六"}}, {"p", draft.Edit{Before: "一二三四", After: "一二三四五六"}}})
	if len(longer) != 1 || longer[0].Key != "length:longer" || !strings.Contains(longer[0].Text, "50%") {
		t.Fatalf("longer = %+v", longer)
	}
	if got := lengthChange([]pair{{"p", draft.Edit{Before: "一二三四五六七八九十", After: "一二三四五六七八九"}}, {"p", draft.Edit{Before: "一二三四五六七八九十", After: "一二三四五六七八九"}}, {"p", draft.Edit{Before: "一二三四五六七八九十", After: "一二三四五六七八九"}}}); got != nil {
		t.Fatalf("within range = %+v", got)
	}
	if got := lengthChange(mkPairs("很长很长很长", "很长很长很长")); got != nil {
		t.Fatalf("too few pairs = %+v", got)
	}
	// 已定位的反馈不到三分之二在长段落时不生成。
	signals := []signal{{unit: "p", id: "f1", paraChars: 100, meanChars: 20}, {unit: "p", id: "f2", paraChars: 10, meanChars: 20}, {unit: "p", id: "f3", paraChars: 12, meanChars: 20}}
	if got := longParagraphs(signals); got != nil {
		t.Fatalf("not concentrated = %+v", got)
	}
	signals[1].paraChars = 90
	if got := longParagraphs(signals); len(got) != 1 || len(got[0].Evidence) != 2 {
		t.Fatalf("concentrated = %+v", got)
	}
	// 反馈主题词最多 5 个。
	var many []signal
	for i := range 3 {
		many = append(many, signal{unit: "p", id: fmt.Sprint("f", i), text: "alpha bravo charlie delta echo foxtrot golf"})
	}
	if got := feedbackTerms(many); len(got) != 5 || got[0].Key != "feedback:alpha" {
		t.Fatalf("feedback terms = %+v", got)
	}
}

func TestPreferUpdatesProposedAndChecksKind(t *testing.T) {
	agent := "首先，我们需要赋能团队，让大家都能快速上手这个工具。\n\n其次，我们要赋能业务，把流程全部梳理清楚再推进。\n\n最后，赋能用户，让他们愿意持续使用并推荐给别人。\n"
	root, _, _ := editedWork(t, agent, "先让团队上手。\n\n再理清业务流程。\n\n最后让用户愿意用。\n")
	pr, _, _, _ := Prefer(root, "prefs", "mine")
	first := pr.Items[slices.Index(keys(pr), "length:shorter")].Text
	// 作者把第三段改得更短：同 key 的 proposed 条目更新文字与证据。
	os.WriteFile(filepath.Join(root, ".writing-anything/works/post/draft.md"), []byte("先让团队上手。\n\n再理清业务流程。\n\n用。\n"), 0o644)
	pr, _, changed, _ := Prefer(root, "prefs", "")
	updated := pr.Items[slices.Index(keys(pr), "length:shorter")]
	if updated.Text == first || updated.ID != "style-1" || len(changed) != 1 {
		t.Fatalf("not updated: %q → %+v %v", first, updated, changed)
	}
	// 手写提议中 key 相同但种类不同的条目不被改写，另建 banned-term 条目。
	file := filepath.Join(root, Path("hand"))
	os.WriteFile(file, []byte("schema_version: 1\nid: hand\npack: mine\nsources: []\nitems:\n  - {id: style-1, kind: style, text: 手写的, status: proposed, key: 'removed:赋能'}\n"), 0o644)
	pr, _, _, ps := Prefer(root, "hand", "")
	if ps != nil {
		t.Fatal(ps)
	}
	n := 0
	for _, it := range pr.Items {
		if it.Key == "removed:赋能" {
			n++
		}
	}
	if n != 2 || pr.Find("style-1").Kind != KindStyle || pr.Find("style-1").Text != "手写的" || pr.Find("avoid-1").Term != "赋能" {
		t.Fatalf("kind mismatch: %+v", pr.Items)
	}
}
