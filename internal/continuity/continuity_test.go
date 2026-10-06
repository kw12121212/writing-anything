package continuity

import (
	"crypto/sha256"
	"encoding/hex"
	"encoding/json"
	"os"
	"path/filepath"
	"strings"
	"testing"
	"time"

	"github.com/kw12121212/writing-anything/internal/decision"
	"github.com/kw12121212/writing-anything/internal/work"
	"github.com/kw12121212/writing-anything/internal/workspace"
)

// newNovel 初始化 novel 工作区：chapter-one/{scene-1,scene-2}；canon-1 守灯人（别名老灯匠）；可选状态与伏笔。
func newNovel(t *testing.T) (string, *work.Work) {
	t.Helper()
	root := t.TempDir()
	if _, ps := workspace.Init(root, nil); len(ps) > 0 {
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
	writeAt(t, root, ".writing-anything/works/my-novel/canon.yaml", `schema_version: 1
entries:
  - {id: canon-1, type: character, name: 守灯人, text: 五十岁, aliases: [老灯匠], sources: [], protection: normal}
  - {id: canon-2, type: character, name: 精灵, text: 灯之精灵, sources: [], protection: normal}
`)
	if w, ps = work.Load(root, "my-novel"); len(ps) > 0 {
		t.Fatal(ps)
	}
	return root, w
}

func writeAt(t *testing.T, root, rel, content string) {
	t.Helper()
	path := filepath.Join(root, filepath.FromSlash(rel))
	if err := os.MkdirAll(filepath.Dir(path), 0o755); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(path, []byte(content), 0o644); err != nil {
		t.Fatal(err)
	}
}

func outlineAt(t *testing.T, root, scene string, sections string) {
	t.Helper()
	writeAt(t, root, ".writing-anything/works/my-novel/units/"+scene+"/outline.yaml",
		"schema_version: 1\nsections:\n"+sections)
}

func unit(t *testing.T, w *work.Work, ref string) *work.Unit {
	t.Helper()
	for _, u := range w.Units() {
		if u.Ref == ref {
			return u
		}
	}
	t.Fatalf("unit %s not found", ref)
	return nil
}

func has(fs []Finding, code string) bool {
	for _, f := range fs {
		if f.Code == code {
			return true
		}
	}
	return false
}

func TestUnknownCharacter(t *testing.T) {
	root, w := newNovel(t)
	outlineAt(t, root, "scene-1", "  - {id: sc1, title: 场景, pov: 守灯人, characters: [守灯人, 高人]}\n")
	fs, fatal := Run(root, w, unit(t, w, "my-novel/scene-1"))
	if len(fatal) > 0 {
		t.Fatal(fatal)
	}
	if !has(fs, CodeUnknownCharacter) {
		t.Fatalf("findings = %+v", fs)
	}
	for _, f := range fs {
		if f.Code == CodeUnknownCharacter && !strings.Contains(f.Message, "高人") {
			t.Fatalf("message = %s", f.Message)
		}
	}
	// 别名命中不报。
	outlineAt(t, root, "scene-2", "  - {id: sc1, title: 场景, pov: 守灯人, characters: [老灯匠]}\n")
	if fs, _ = Run(root, w, unit(t, w, "my-novel/scene-2")); has(fs, CodeUnknownCharacter) {
		t.Fatalf("alias hit reported: %+v", fs)
	}
}

func TestDeadActing(t *testing.T) {
	root, w := newNovel(t)
	writeAt(t, root, ".writing-anything/works/my-novel/state.yaml", `schema_version: 1
events:
  - {id: state-1, scene: my-novel/scene-1, entry: canon-1, state: 死亡, status: confirmed, confirmation: 作者确认, decided_at: "2026-10-04T08:00:00Z"}
  - {id: state-2, scene: my-novel/scene-1, entry: canon-2, state: 殒命于灯塔, status: confirmed, confirmation: 作者确认, decided_at: "2026-10-04T08:00:00Z"}
`)
	outlineAt(t, root, "scene-2", "  - {id: sc1, title: 场景, pov: 守灯人, characters: [守灯人, 精灵]}\n")
	fs, fatal := Run(root, w, unit(t, w, "my-novel/scene-2"))
	if len(fatal) > 0 {
		t.Fatal(fatal)
	}
	if !has(fs, CodeDeadActing) {
		t.Fatalf("findings = %+v, want dead-acting for 守灯人", fs)
	}
	// 非保留词（殒命于灯塔）不判；保留词场景自身不判（先序严格早于）。
	outlineAt(t, root, "scene-1", "  - {id: sc1, title: 场景, characters: [守灯人]}\n")
	if fs, _ = Run(root, w, unit(t, w, "my-novel/scene-1")); has(fs, CodeDeadActing) {
		t.Fatalf("own-scene state should not trigger: %+v", fs)
	}
}

func TestAliasMismatch(t *testing.T) {
	root, w := newNovel(t)
	outlineAt(t, root, "scene-1", "  - {id: sc1, title: 场景, pov: 守灯人, characters: [守灯人, 老灯匠]}\n")
	fs, _ := Run(root, w, unit(t, w, "my-novel/scene-1"))
	if !has(fs, CodeAliasMismatch) {
		t.Fatalf("findings = %+v", fs)
	}
}

// finishScene 把场景推进到 done（合法关卡的产物齐备）。
func finishScene(t *testing.T, root, s string) {
	t.Helper()
	ref := "my-novel/" + s
	dir := filepath.Join(root, ".writing-anything", "works", "my-novel", "units", s)
	writeAt(t, root, ".writing-anything/works/my-novel/units/"+s+"/brief.yaml", "schema_version: 1\n")
	if _, _, _, ps := work.Advance(root, ref); len(ps) > 0 {
		t.Fatal(ps)
	}
	writeAt(t, root, ".writing-anything/works/my-novel/units/"+s+"/outline.yaml", "schema_version: 1\nsections:\n  - {id: sc1, title: 场景, reason: 推进情节}\n")
	if _, _, _, ps := work.Advance(root, ref); len(ps) > 0 {
		t.Fatal(ps)
	}
	draft := "正文。\n"
	writeAt(t, root, ".writing-anything/works/my-novel/units/"+s+"/draft.md", draft)
	if _, _, _, ps := work.Advance(root, ref); len(ps) > 0 {
		t.Fatal(ps)
	}
	sum := sha(draft)
	writeAt(t, root, ".writing-anything/works/my-novel/units/"+s+"/review.yaml", "schema_version: 1\ndraft_sha256: "+sum+"\nrubric: []\nfindings: []\n")
	writeAt(t, root, ".writing-anything/works/my-novel/units/"+s+"/acceptance.yaml", "schema_version: 1\ngate_level: standard\ndraft_sha256: "+sum+"\nconfirmed_by_author: true\nconfirmation: ok\naccepted_at: \"2026-10-04T08:00:00Z\"\n")
	if _, _, _, ps := work.Advance(root, ref); len(ps) > 0 {
		t.Fatal(ps)
	}
	_ = dir
}

func sha(s string) string {
	sum := sha256.Sum256([]byte(s))
	return hex.EncodeToString(sum[:])
}

func TestThreadAnomalyAndScope(t *testing.T) {
	root, w := newNovel(t)
	writeAt(t, root, ".writing-anything/works/my-novel/threads.yaml", `schema_version: 1
entries:
  - {id: f-1, text: 灯芯的来历, planted_in: my-novel/scene-1, planned_recover: my-novel/scene-1, status: open}
`)
	// scene-1 推到 done：计划回收位置已 done 而伏笔仍 open → overdue，报 thread-anomaly。
	finishScene(t, root, "scene-1")
	w, ps := work.Load(root, "my-novel")
	if len(ps) > 0 {
		t.Fatal(ps)
	}
	writeAt(t, root, ".writing-anything/works/my-novel/units/scene-1/outline.yaml", "schema_version: 1\nsections:\n  - {id: sc1, title: 场景, reason: 推进情节, pov: 守灯人, characters: [守灯人]}\n")
	fs, fatal := Run(root, w, unit(t, w, "my-novel"))
	if len(fatal) > 0 {
		t.Fatal(fatal)
	}
	if !has(fs, CodeThreadAnomaly) {
		t.Fatalf("findings = %+v, want thread-anomaly on work scope", fs)
	}
	for _, f := range fs {
		if f.Code == CodeThreadAnomaly && f.Scene != "my-novel/scene-1" {
			t.Fatalf("anomaly on wrong scene: %+v", f)
		}
	}
}

func TestWaiveFlow(t *testing.T) {
	root, w := newNovel(t)
	outlineAt(t, root, "scene-1", "  - {id: sc1, title: 场景, pov: 守灯人, characters: [高人]}\n")
	s1 := unit(t, w, "my-novel/scene-1")
	// 首次：阻塞并生成决策。
	blockers, changed, ps := AcceptanceBlockers(root, w, s1)
	if len(ps) > 0 {
		t.Fatal(ps)
	}
	if len(blockers) != 1 || blockers[0].Code != CodeUnknownCharacter {
		t.Fatalf("blockers = %+v", blockers)
	}
	if len(changed) != 1 || changed[0] != decision.Path {
		t.Fatalf("changed = %v", changed)
	}
	// 幂等：再次生成不追加。
	if _, changed2, _ := AcceptanceBlockers(root, w, s1); len(changed2) != 0 {
		t.Fatalf("second generation changed = %v, want none", changed2)
	}
	// 作者允许后豁免。
	ds, _, _ := decision.Load(root)
	var id string
	for _, d := range ds {
		if strings.Contains(d.Question, "continuity.unknown-character") {
			id = d.ID
		}
	}
	if id == "" {
		t.Fatal("waiver decision not found")
	}
	if _, ps := decision.Decide(root, id, "a", "作者允许这次豁免", "作者允许这次豁免", time.Now()); len(ps) > 0 {
		t.Fatal(ps)
	}
	if blockers, _, _ = AcceptanceBlockers(root, w, s1); len(blockers) != 0 {
		t.Fatalf("after waive blockers = %+v", blockers)
	}
	// Run 的 finding 标注 waived。
	fs, _ := Run(root, w, s1)
	if len(fs) != 1 || !fs[0].Waived {
		t.Fatalf("findings = %+v, want one waived", fs)
	}
}

func TestKeepChoiceSuppressesAndNonScene(t *testing.T) {
	root, w := newNovel(t)
	outlineAt(t, root, "scene-1", "  - {id: sc1, title: 场景, pov: 守灯人, characters: [高人]}\n")
	s1 := unit(t, w, "my-novel/scene-1")
	AcceptanceBlockers(root, w, s1)
	ds, _, _ := decision.Load(root)
	var id string
	for _, d := range ds {
		if strings.Contains(d.Question, "continuity.unknown-character") {
			id = d.ID
		}
	}
	if _, ps := decision.Decide(root, id, "b", "作者保持不变", "作者保持不变", time.Now()); len(ps) > 0 {
		t.Fatal(ps)
	}
	blockers, changed, _ := AcceptanceBlockers(root, w, s1)
	if len(blockers) != 1 {
		t.Fatalf("kept blockers = %+v", blockers)
	}
	if len(changed) != 0 {
		t.Fatalf("kept changed = %v, want none（不重复追加）", changed)
	}
	// 非 scene 单元零阻塞。
	if b, c, _ := AcceptanceBlockers(root, w, unit(t, w, "my-novel/chapter-one")); len(b) != 0 || len(c) != 0 {
		t.Fatalf("non-scene = %+v %v", b, c)
	}
}

// 问题文本逐字断言（refusal-side-decision-gating：固定格式是授权与幂等的契约）。
func TestQuestionVerbatim(t *testing.T) {
	got := Question("my-novel/scene-1", CodeUnknownCharacter, "任何首句")
	want := "是否豁免场景 my-novel/scene-1 的连续性检查 continuity.unknown-character？"
	if got != want {
		t.Fatalf("question = %q, want %q", got, want)
	}
}

// 决策队列无法加载：不追加、不修改任何文件。
func TestQueueUnloadable(t *testing.T) {
	root, w := newNovel(t)
	outlineAt(t, root, "scene-1", "  - {id: sc1, title: 场景, pov: 守灯人, characters: [高人]}\n")
	writeAt(t, root, ".writing-anything/decisions.yaml", "schema_version: 1\nquestions: [\n")
	s1 := unit(t, w, "my-novel/scene-1")
	before, _ := os.ReadFile(filepath.Join(root, ".writing-anything", "decisions.yaml"))
	blockers, changed, ps := AcceptanceBlockers(root, w, s1)
	if len(ps) == 0 {
		t.Fatalf("ps = %+v, want queue diagnostics", ps)
	}
	if blockers != nil || changed != nil {
		t.Fatalf("blockers = %+v, changed = %v, want nil", blockers, changed)
	}
	after, _ := os.ReadFile(filepath.Join(root, ".writing-anything", "decisions.yaml"))
	if string(before) != string(after) {
		t.Fatal("decisions.yaml changed while queue unloadable")
	}
}

// JSON 序列化稳定（输出信封的字段名）。
func TestFindingJSON(t *testing.T) {
	f := Finding{Scene: "s", Code: CodeDeadActing, Severity: "error", Message: "m"}
	data, _ := json.Marshal(f)
	for _, key := range []string{`"scene"`, `"code"`, `"severity"`, `"waived"`, `"message"`} {
		if !strings.Contains(string(data), key) {
			t.Fatalf("json = %s missing %s", data, key)
		}
	}
}
