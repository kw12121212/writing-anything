package series

import (
	"crypto/sha256"
	"encoding/hex"
	"os"
	"path/filepath"
	"strings"
	"testing"
	"time"

	"github.com/kw12121212/writing-anything/internal/catalog"

	"github.com/kw12121212/writing-anything/internal/work"
	"github.com/kw12121212/writing-anything/internal/workspace"
)

// layeredNovel 初始化 novel 工作区：chapter-one/{scene-1,scene-2}。推进场景到 done（合法关卡的 fast 验收），
// 每个场景写正文与摘要；acceptDraft 控制是否跳过推进（供就绪检查的失败分支）。
func layeredNovel(t *testing.T) (string, *work.Work) {
	t.Helper()
	root := t.TempDir()
	if _, ps := workspace.Init(root, map[string]string{"gate_level": "fast"}); len(ps) > 0 {
		t.Fatal(ps)
	}
	var w *work.Work
	var ps []workspace.Problem
	if w, _, ps = work.Create(root, "novel", "my-novel"); len(ps) > 0 {
		t.Fatal(ps)
	}
	for _, add := range []struct{ parent, typ, id string }{
		{"my-novel", "part", "part-one"},
		{"my-novel/part-one", "chapter", "chapter-one"},
		{"my-novel/chapter-one", "scene", "scene-1"},
		{"my-novel/chapter-one", "scene", "scene-2"},
	} {
		if _, _, ps = work.AddUnit(root, add.parent, add.typ, add.id, work.Anchor{}); len(ps) > 0 {
			t.Fatal(ps)
		}
	}
	if w, ps = work.Load(root, "my-novel"); len(ps) > 0 {
		t.Fatal(ps)
	}
	return root, w
}

func finishScene(t *testing.T, root, ref, draft string) {
	t.Helper()
	s := ref[strings.LastIndex(ref, "/")+1:]
	dir := filepath.Join(root, ".writing-anything", "works", "my-novel", "units", s)
	if err := os.MkdirAll(dir, 0o755); err != nil {
		t.Fatal(err)
	}
	write := func(name, content string) {
		if err := os.WriteFile(filepath.Join(dir, name), []byte(content), 0o644); err != nil {
			t.Fatal(err)
		}
	}
	write("brief.yaml", "schema_version: 1\n")
	if _, _, _, ps := work.Advance(root, ref); len(ps) > 0 {
		t.Fatal(ps)
	}
	write("outline.yaml", "schema_version: 1\nsections:\n  - {id: sc1, title: 场景, reason: 推进情节}\n")
	if _, _, _, ps := work.Advance(root, ref); len(ps) > 0 {
		t.Fatal(ps)
	}
	write("draft.md", draft)
	if _, _, _, ps := work.Advance(root, ref); len(ps) > 0 {
		t.Fatal(ps)
	}
	sum := sha256.Sum256([]byte(draft))
	sha := hex.EncodeToString(sum[:])
	write("review.yaml", "schema_version: 1\ndraft_sha256: "+sha+"\nrubric: []\nfindings: []\n")
	write("acceptance.yaml", "schema_version: 1\ngate_level: fast\ndraft_sha256: "+sha+"\nconfirmed_by_author: true\nconfirmation: ok\naccepted_at: \"2026-10-04T08:00:00Z\"\n")
	if _, _, _, ps := work.Advance(root, ref); len(ps) > 0 {
		t.Fatal(ps)
	}
}

func unitOf(t *testing.T, w *work.Work, ref string) *work.Unit {
	t.Helper()
	for _, u := range w.Units() {
		if u.Ref == ref {
			return u
		}
	}
	t.Fatalf("unit %s not found", ref)
	return nil
}

var length20to500 = catalog.Range{Min: 20, Max: 500}

func TestChapterSummaryChildrenMissing(t *testing.T) {
	root, w := layeredNovel(t)
	finishScene(t, root, "my-novel/scene-1", "第一个场景的正文，两段话。\\n第二段。\\n")
	// scene-2 未推进：章摘要的子层不齐。
	_, _, ps := SetSummary(root, w, unitOf(t, w, "my-novel/chapter-one"), "第一章：守灯人接下委托，与精灵在灯下对峙，埋下灯芯的来历。", length20to500, time.Now())
	found := false
	for _, p := range ps {
		if p.Code == CodeChildrenMissing && strings.Contains(p.Message, "my-novel/scene-2") {
			found = true
		}
	}
	if !found {
		t.Fatalf("problems = %+v, want summary.children-missing naming scene-2", ps)
	}
}

func TestChapterSummaryFingerprint(t *testing.T) {
	root, w := layeredNovel(t)
	finishScene(t, root, "my-novel/scene-1", "第一个场景的正文，两段话。\\n第二段。\\n")
	finishScene(t, root, "my-novel/scene-2", "第二个场景的正文，交代转折。\\n再一段。\\n")
	w, ps := work.Load(root, "my-novel")
	if len(ps) > 0 {
		t.Fatal(ps)
	}
	// 场景摘要（非汇总单元路径，直接写产物以聚焦分层校验）。
	for _, s := range []string{"scene-1", "scene-2"} {
		sum := sha256.Sum256([]byte("正文指纹无关紧要"))
		if _, _, ps := SetSummary(root, w, unitOf(t, w, "my-novel/"+s), "这是场景 "+s+" 的情节摘要，交代本场景的推进与人物变化。", length20to500, time.Now()); len(ps) > 0 {
			t.Fatalf("scene summary %s: %+v", s, ps)
		}
		_ = sum
	}
	ch := unitOf(t, w, "my-novel/chapter-one")
	v, changed, ps := SetSummary(root, w, ch, "第一章：守灯人接下委托并与精灵对峙，灯芯易主，埋下伏笔。", length20to500, time.Now())
	if v == nil {
		t.Fatalf("chapter summary: %+v", ps)
	}
	if len(changed) != 1 {
		t.Fatalf("changedPaths = %v", changed)
	}
	// 指纹 = 两个场景正文 SHA 的拼接哈希；show 的 stale 依此推导。
	fp, ok := Fingerprint(root, w, ch)
	if !ok || fp != v.DraftSHA256 {
		t.Fatalf("fingerprint = %s, recorded = %s", fp, v.DraftSHA256)
	}
	shown, ps := LoadSummary(root, w, ch)
	if len(ps) > 0 || shown == nil || shown.Stale {
		t.Fatalf("summary show: %+v %+v", shown, ps)
	}
	// 子场景正文变化 → 章 stale。
	if err := os.WriteFile(filepath.Join(root, ".writing-anything", "works", "my-novel", "units", "scene-2", "draft.md"), []byte("改写后的正文。\\n"), 0o644); err != nil {
		t.Fatal(err)
	}
	if shown, _ = LoadSummary(root, w, ch); shown == nil || !shown.Stale {
		t.Fatalf("summary show after edit: %+v, want stale", shown)
	}
}

func TestPartSummaryNeedsChapters(t *testing.T) {
	root, w := layeredNovel(t)
	finishScene(t, root, "my-novel/scene-1", "第一个场景的正文，两段话。\\n第二段。\\n")
	finishScene(t, root, "my-novel/scene-2", "第二个场景的正文，交代转折。\\n再一段。\\n")
	w, _ = work.Load(root, "my-novel")
	// 场景有摘要但章没有：部摘要的子层（章）不齐。
	if _, _, ps := SetSummary(root, w, unitOf(t, w, "my-novel/scene-1"), "这是场景 scene-1 的情节摘要，交代本场景的推进与人物变化。", length20to500, time.Now()); len(ps) > 0 {
		t.Fatal(ps)
	}
	if _, _, ps := SetSummary(root, w, unitOf(t, w, "my-novel/scene-2"), "这是场景 scene-2 的情节摘要，交代本场景的推进与人物变化。", length20to500, time.Now()); len(ps) > 0 {
		t.Fatal(ps)
	}
	_, _, ps := SetSummary(root, w, unitOf(t, w, "my-novel/part-one"), "第一部：守灯人接下委托，与精灵对峙后灯芯易主，伏笔展开。", length20to500, time.Now())
	found := false
	for _, p := range ps {
		if p.Code == CodeChildrenMissing && strings.Contains(p.Message, "my-novel/chapter-one") {
			found = true
		}
	}
	if !found {
		t.Fatalf("problems = %+v, want summary.children-missing naming chapter-one", ps)
	}
}

func TestAggregatedDetection(t *testing.T) {
	_, w := layeredNovel(t)
	if !Aggregated(w, unitOf(t, w, "my-novel/chapter-one")) || !Aggregated(w, unitOf(t, w, "my-novel/part-one")) || !Aggregated(w, w.Root) {
		t.Fatal("chapter/part/root should be aggregated")
	}
	if Aggregated(w, unitOf(t, w, "my-novel/scene-1")) {
		t.Fatal("scene should not be aggregated")
	}
}
