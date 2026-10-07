package continuity

import (
	"os"
	"path/filepath"
	"strings"
	"testing"
	"time"

	"github.com/kw12121212/writing-anything/internal/decision"
	"github.com/kw12121212/writing-anything/internal/workspace"
)

// 因果冲突：场景起因（causes）引用尚未发生（由同一或更靠后的场景声明）或不存在的事件时，Run 把因果链的
// error 级问题转为该场景的 error 级发现（错误码统一为 continuity.dangling-reference，message 保留原错误码），
// 并且只归属到问题所在的场景。
func TestCausalityConflictFindings(t *testing.T) {
	root, w := newNovel(t)
	outlineAt(t, root, "scene-1", "  - {id: sc1, title: 场景, pov: 守灯人, characters: [守灯人], causes: [ev-later, ev-none]}\n")
	outlineAt(t, root, "scene-2", "  - {id: sc1, title: 场景, pov: 守灯人, characters: [守灯人], events: [ev-later]}\n")
	fs, fatal := Run(root, w, unit(t, w, "my-novel"))
	if len(fatal) > 0 {
		t.Fatal(fatal)
	}
	var got []Finding
	for _, f := range fs {
		if f.Code == "continuity.dangling-reference" {
			got = append(got, f)
		}
	}
	if len(got) != 2 {
		t.Fatalf("findings = %+v, want two causality findings on scene-1", fs)
	}
	for i, want := range []string{"scene.event-not-yet-happened：sections[0].causes[0]", "scene.unknown-event：sections[0].causes[1]"} {
		f := got[i]
		if f.Scene != "my-novel/scene-1" || f.Severity != "error" || f.Waived || !strings.Contains(f.Message, want) {
			t.Errorf("finding[%d] = %+v, want error on scene-1 containing %q", i, f, want)
		}
	}
	// 只读取 scene-2 时没有因果发现（问题归属起因所在的场景）。
	if fs, _ := Run(root, w, unit(t, w, "my-novel/scene-2")); has(fs, "continuity.dangling-reference") {
		t.Fatalf("scene-2 findings = %+v", fs)
	}
}

// 因果冲突阻塞验收：Blockers 只读地报告阻塞且不写决策队列；EnsureWaivers 幂等地生成一条（场景， 错误码）豁免决策；
// AcceptanceBlockers 随后报告「已在队列中」；作者允许后不再阻塞；修正起因后发现消失。
func TestCausalityConflictBlocksAcceptance(t *testing.T) {
	root, w := newNovel(t)
	outlineAt(t, root, "scene-1", "  - {id: sc1, title: 场景, pov: 守灯人, characters: [守灯人], causes: [ev-later]}\n")
	outlineAt(t, root, "scene-2", "  - {id: sc1, title: 场景, pov: 守灯人, characters: [守灯人], events: [ev-later]}\n")
	s1 := unit(t, w, "my-novel/scene-1")

	blockers, ps := Blockers(root, w, s1)
	if len(ps) > 0 || len(blockers) != 1 || blockers[0].Code != "continuity.dangling-reference" || blockers[0].Severity != workspace.SeverityError ||
		blockers[0].Path != ".writing-anything/works/my-novel/units/scene-1/outline.yaml" || !strings.Contains(blockers[0].Message, "scene.event-not-yet-happened") {
		t.Fatalf("Blockers = %+v, %+v", blockers, ps)
	}
	if ds, _, _ := decision.Load(root); len(ds) != 0 {
		t.Fatalf("Blockers must not write decisions: %+v", ds)
	}
	// 非 scene 单元没有阻塞，也不生成决策。
	if b, ps := Blockers(root, w, unit(t, w, "my-novel/chapter-one")); b != nil || ps != nil {
		t.Fatalf("non-scene Blockers = %+v %+v", b, ps)
	}
	if c, ps := EnsureWaivers(root, w, unit(t, w, "my-novel/chapter-one")); c != nil || ps != nil {
		t.Fatalf("non-scene EnsureWaivers = %v %+v", c, ps)
	}

	changed, ps := EnsureWaivers(root, w, s1)
	if len(ps) > 0 || len(changed) != 1 || changed[0] != decision.Path {
		t.Fatalf("EnsureWaivers = %v, %+v", changed, ps)
	}
	if changed, _ := EnsureWaivers(root, w, s1); len(changed) != 0 {
		t.Fatalf("second EnsureWaivers changed = %v, want none", changed)
	}
	ds, _, _ := decision.Load(root)
	if len(ds) != 1 || ds[0].Question != "是否豁免场景 my-novel/scene-1 的连续性检查 continuity.dangling-reference？" || !ds[0].Blocking {
		t.Fatalf("decisions = %+v", ds)
	}
	blockers, changed, ps = AcceptanceBlockers(root, w, s1)
	if len(ps) > 0 || len(changed) != 0 || len(blockers) != 1 || !strings.Contains(blockers[0].Message, "豁免决策 "+ds[0].ID+" 已在队列中") {
		t.Fatalf("AcceptanceBlockers pending = %+v %v %+v", blockers, changed, ps)
	}
	if blockers, _ := Blockers(root, w, s1); len(blockers) != 1 || !strings.Contains(blockers[0].Message, "等待作者决定") {
		t.Fatalf("Blockers pending = %+v", blockers)
	}
	// 作者允许后豁免：不再阻塞，Run 的发现项标注 waived。
	if _, ps := decision.Decide(root, ds[0].ID, "a", "作者允许这次豁免", "作者允许这次豁免", time.Now()); len(ps) > 0 {
		t.Fatal(ps)
	}
	if blockers, ps := Blockers(root, w, s1); len(blockers) != 0 || ps != nil {
		t.Fatalf("Blockers after waive = %+v %+v", blockers, ps)
	}
	if fs, _ := Run(root, w, s1); len(fs) != 1 || !fs[0].Waived {
		t.Fatalf("findings after waive = %+v", fs)
	}

	// 修正起因（改为引用前文已发生的事件）后发现消失，验收不再被因果冲突阻塞。
	outlineAt(t, root, "scene-1", "  - {id: sc1, title: 场景, pov: 守灯人, characters: [守灯人], events: [ev-earlier]}\n")
	outlineAt(t, root, "scene-2", "  - {id: sc1, title: 场景, pov: 守灯人, characters: [守灯人], causes: [ev-earlier]}\n")
	for _, ref := range []string{"my-novel/scene-1", "my-novel/scene-2"} {
		if blockers, ps := Blockers(root, w, unit(t, w, ref)); len(blockers) != 0 || ps != nil {
			t.Fatalf("%s after fix = %+v %+v", ref, blockers, ps)
		}
	}
}

// 决策队列无法加载时：Blockers 按原问题阻塞（偏安全），EnsureWaivers 不追加决策，decisions.yaml 不变。
func TestCausalityQueueUnloadable(t *testing.T) {
	root, w := newNovel(t)
	outlineAt(t, root, "scene-1", "  - {id: sc1, title: 场景, pov: 守灯人, characters: [守灯人], causes: [ev-none]}\n")
	writeAt(t, root, ".writing-anything/decisions.yaml", "schema_version: 1\nquestions: [\n")
	before, _ := os.ReadFile(filepath.Join(root, ".writing-anything", "decisions.yaml"))
	s1 := unit(t, w, "my-novel/scene-1")
	if b, ps := Blockers(root, w, s1); len(b) != 1 || b[0].Code != "artifact.parse-error" || ps != nil {
		t.Fatalf("Blockers = %+v %+v", b, ps)
	}
	if c, ps := EnsureWaivers(root, w, s1); c != nil || ps != nil {
		t.Fatalf("EnsureWaivers = %v %+v", c, ps)
	}
	if after, _ := os.ReadFile(filepath.Join(root, ".writing-anything", "decisions.yaml")); string(after) != string(before) {
		t.Fatal("decisions.yaml changed while queue unloadable")
	}
}
