package volume

import (
	"crypto/sha256"
	"encoding/hex"
	"os"
	"path/filepath"
	"strings"
	"testing"
	"time"

	"github.com/kw12121212/writing-anything/internal/work"
	"github.com/kw12121212/writing-anything/internal/workspace"
)

// newTwoParts 初始化 novel 工作区：part-one/chapter-one/{scene-1,scene-2} 与 part-two/chapter-two/scene-9。
// scene-1 已收口（canon 新增 canon-3、状态变化 state-1 与知情 know-1，均经作者确认）并写入摘要，全部场景 done；
// scene-2 与 scene-9 无契约直接走完生命周期并写摘要。
func newTwoParts(t *testing.T, skipAdvance ...string) (string, *work.Work) {
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
		{"my-novel", "part", "part-two"},
		{"my-novel/part-two", "chapter", "chapter-two"},
		{"my-novel/chapter-two", "scene", "scene-9"},
	} {
		if _, _, ps = work.AddUnit(root, add.parent, add.typ, add.id, work.Anchor{}); len(ps) > 0 {
			t.Fatal(ps)
		}
	}
	// 设定集与台账：canon-1 守灯人；canon-3 为 scene-1 收口新增；state-1 已确认。
	writeAt(t, root, ".writing-anything/works/my-novel/canon.yaml", `schema_version: 1
entries:
  - {id: canon-1, type: character, name: 守灯人, text: 五十岁, sources: [], protection: normal}
  - {id: canon-3, type: character, name: 精灵, text: 灯之精灵, sources: [], protection: normal}
`)
	writeAt(t, root, ".writing-anything/works/my-novel/state.yaml", `schema_version: 1
events:
  - {id: state-1, scene: my-novel/scene-1, entry: canon-1, state: 灯芯易主给精灵, status: confirmed, confirmation: 作者确认, decided_at: "2026-10-04T08:00:00Z"}
`)
	writeAt(t, root, ".writing-anything/works/my-novel/knowledge.yaml", `schema_version: 1
events:
  - {id: know-1, scene: my-novel/scene-1, entry: canon-3, character: reader, status: confirmed, confirmation: 作者确认, decided_at: "2026-10-04T08:00:00Z"}
`)
	writeAt(t, root, ".writing-anything/works/my-novel/threads.yaml", `schema_version: 1
entries:
  - {id: f-1, text: 灯芯的来历, planted_in: my-novel/scene-1, planned_recover: my-novel/scene-1, status: paid, recovered_in: my-novel/scene-1}
`)
	skip := map[string]bool{}
	for _, s := range skipAdvance {
		skip[s] = true
	}
	for _, s := range []string{"scene-1", "scene-2", "scene-9"} {
		ref := "my-novel/" + s
		dir := filepath.Join(root, ".writing-anything", "works", "my-novel", "units", s)
		if err := os.MkdirAll(dir, 0o755); err != nil {
			t.Fatal(err)
		}
		writeAt(t, root, ".writing-anything/works/my-novel/units/"+s+"/brief.yaml", "schema_version: 1\n")
		if skip[s] {
			continue
		}
		if _, _, _, ps = work.Advance(root, ref); len(ps) > 0 {
			t.Fatal(ps)
		}
		writeAt(t, root, ".writing-anything/works/my-novel/units/"+s+"/outline.yaml", "schema_version: 1\nsections:\n  - {id: sc1, title: 场景, reason: 推进情节}\n")
		if _, _, _, ps = work.Advance(root, ref); len(ps) > 0 {
			t.Fatal(ps)
		}
		draft := "这是场景 " + s + " 的正文，包含足够的内容。\n"
		writeAt(t, root, ".writing-anything/works/my-novel/units/"+s+"/draft.md", draft)
		if _, _, _, ps = work.Advance(root, ref); len(ps) > 0 {
			t.Fatal(ps)
		}
		sha := sha(draft)
		writeAt(t, root, ".writing-anything/works/my-novel/units/"+s+"/review.yaml",
			"schema_version: 1\ndraft_sha256: "+sha+"\nrubric: []\nfindings: []\n")
		writeAt(t, root, ".writing-anything/works/my-novel/units/"+s+"/acceptance.yaml",
			"schema_version: 1\ngate_level: standard\ndraft_sha256: "+sha+"\nconfirmed_by_author: true\nconfirmation: ok\naccepted_at: \"2026-10-04T08:00:00Z\"\n")
		if _, _, _, ps = work.Advance(root, ref); len(ps) > 0 {
			t.Fatal(ps)
		}
		writeAt(t, root, ".writing-anything/works/my-novel/units/"+s+"/summary.yaml",
			"schema_version: 1\ntext: 场景摘要，交代本场景的情节推进与人物变化。\ndraft_sha256: "+sha+"\nrecorded_at: \"2026-10-04T08:00:00Z\"\n")
		if s == "scene-1" {
			writeAt(t, root, ".writing-anything/works/my-novel/units/scene-1/closeout.yaml",
				"schema_version: 1\ndraft_sha256: "+sha+"\ncanon: [canon-3]\nstate: [{id: state-1, contract: sc-1}]\nknowledge: [know-1]\n")
		}
	}
	if w, ps = work.Load(root, "my-novel"); len(ps) > 0 {
		t.Fatal(ps)
	}
	return root, w
}

func sha(s string) string {
	sum := sha256.Sum256([]byte(s))
	return hex.EncodeToString(sum[:])
}

func writeAt(t *testing.T, root, rel, content string) {
	t.Helper()
	if err := os.WriteFile(filepath.Join(root, filepath.FromSlash(rel)), []byte(content), 0o644); err != nil {
		t.Fatal(err)
	}
}

func has(ps []workspace.Problem, code string) bool {
	for _, p := range ps {
		if p.Code == code {
			return true
		}
	}
	return false
}

func part(t *testing.T, w *work.Work, ref string) *work.Unit {
	t.Helper()
	for _, u := range w.Units() {
		if u.Ref == ref {
			return u
		}
	}
	t.Fatalf("unit %s not found", ref)
	return nil
}

func TestCheckpointScenesOpen(t *testing.T) {
	root, w := newTwoParts(t, "scene-2")
	// chapter-one 的场景 stage 存储值仍为 brief（AddUnit 初始），视为未完成。
	if _, _, ps := Checkpoint(root, w, part(t, w, "my-novel/part-one"), time.Now()); !has(ps, CodeScenesOpen) {
		t.Fatalf("problems = %+v, want %s", ps, CodeScenesOpen)
	}
}

func TestCheckpointNotVolume(t *testing.T) {
	root, w := newTwoParts(t)
	if _, _, ps := Checkpoint(root, w, part(t, w, "my-novel/chapter-one"), time.Now()); !has(ps, CodeNotVolume) {
		t.Fatalf("problems = %+v, want %s（章不是部）", ps, CodeNotVolume)
	}
}

func TestCheckpointAggregatesAndSamples(t *testing.T) {
	root, w := newTwoParts(t)
	p := part(t, w, "my-novel/part-one")
	v, changed, ps := Checkpoint(root, w, p, time.Now())
	if v == nil {
		t.Fatalf("problems = %+v", ps)
	}
	if v.Scene != "my-novel/scene-2" {
		t.Fatalf("scene = %s, want my-novel/scene-2（先序最后）", v.Scene)
	}
	if len(v.Canon) != 1 || v.Canon[0] != "canon-3" {
		t.Fatalf("canon = %v", v.Canon)
	}
	if len(v.States) != 1 || v.States[0] != "state-1" {
		t.Fatalf("states = %v", v.States)
	}
	if len(v.Threads) != 1 || v.Threads[0] != "f-1" {
		t.Fatalf("threads = %v", v.Threads)
	}
	if len(v.Sampled) != 2 {
		t.Fatalf("sampled = %+v, want 2 条（state-1 与 know-1）", v.Sampled)
	}
	kinds := map[string]bool{}
	for _, s := range v.Sampled {
		kinds[s.Kind+"/"+s.ID] = true
	}
	if !kinds["state/state-1"] || !kinds["knowledge/know-1"] {
		t.Fatalf("sampled = %+v", v.Sampled)
	}
	if len(changed) != 1 || !strings.HasSuffix(changed[0], "units/part-one/checkpoint.yaml") {
		t.Fatalf("changedPaths = %v", changed)
	}
	// 幂等：再次生成不写文件，抽样完全一致。
	v2, changed2, ps2 := Checkpoint(root, w, p, time.Now())
	if v2 == nil || len(changed2) != 0 {
		t.Fatalf("re-checkpoint: changed = %v, problems = %+v", changed2, ps2)
	}
	if strings.Join(sampledKeys(v2), ",") != strings.Join(sampledKeys(v), ",") {
		t.Fatalf("sampling not deterministic: %+v vs %+v", v2.Sampled, v.Sampled)
	}
	// 正文变化后：指纹不同 → 重新生成（changedPaths 非空）。
	writeAt(t, root, ".writing-anything/works/my-novel/units/scene-2/draft.md", "改写后的正文，内容不同。\n")
	if v3, changed3, _ := Checkpoint(root, w, p, time.Now()); v3 == nil || len(changed3) == 0 {
		t.Fatalf("re-checkpoint after edit: changed = %v", changed3)
	}
}

func sampledKeys(v *View) []string {
	out := []string{}
	for _, s := range v.Sampled {
		out = append(out, s.Kind+"/"+s.ID)
	}
	return out
}

func TestConfirm(t *testing.T) {
	root, w := newTwoParts(t)
	p := part(t, w, "my-novel/part-one")
	if _, _, ps := Confirm(root, w, p, "  ", time.Now()); !has(ps, CodeIncompleteConfirmation) {
		t.Fatalf("problems = %+v, want %s", ps, CodeIncompleteConfirmation)
	}
	if _, _, ps := Confirm(root, w, part(t, w, "my-novel/part-two"), "ok", time.Now()); !has(ps, CodeNotFound) {
		t.Fatalf("problems = %+v, want %s", ps, CodeNotFound)
	}
	if _, _, ps := Checkpoint(root, w, p, time.Now()); ps != nil {
		t.Fatal(ps)
	}
	v, changed, ps := Confirm(root, w, p, "作者：本部核对无误", time.Now())
	if v == nil {
		t.Fatalf("problems = %+v", ps)
	}
	if !v.ConfirmedByAuthor || v.Confirmation == nil || *v.Confirmation != "作者：本部核对无误" || v.ConfirmedAt == nil || v.Channel == nil || *v.Channel != "cli" {
		t.Fatalf("view = %+v", v)
	}
	if len(changed) != 1 {
		t.Fatalf("changedPaths = %v", changed)
	}
	if _, _, ps := Confirm(root, w, p, "再次确认", time.Now()); !has(ps, CodeAlreadyConfirmed) {
		t.Fatalf("problems = %+v, want %s", ps, CodeAlreadyConfirmed)
	}
}

func TestAcceptanceBlockers(t *testing.T) {
	root, w := newTwoParts(t)
	p1 := part(t, w, "my-novel/part-one")
	scene9 := part(t, w, "my-novel/scene-9")
	// 更早的部没有检查点：strict 报 error 级 volume.missing，fast 只提示；本部场景不受影响。
	if ps := AcceptanceBlockers(root, w, scene9, "strict"); len(ps) != 1 || ps[0].Code != CodeMissing || ps[0].Severity != workspace.SeverityError ||
		ps[0].Path != ".writing-anything/works/my-novel/units/part-one/checkpoint.yaml" || !strings.Contains(ps[0].Message, "my-novel/part-one") {
		t.Fatalf("missing problems = %+v", ps)
	}
	if ps := AcceptanceBlockers(root, w, scene9, "fast"); len(ps) != 1 || ps[0].Code != CodeMissing || ps[0].Severity != workspace.SeverityWarning {
		t.Fatalf("fast missing problems = %+v", ps)
	}
	if ps := AcceptanceBlockers(root, w, part(t, w, "my-novel/scene-2"), "strict"); len(ps) != 0 {
		t.Fatalf("same-part missing problems = %+v, want none", ps)
	}
	if _, _, ps := Checkpoint(root, w, p1, time.Now()); ps != nil {
		t.Fatal(ps)
	}
	// strict：下一部场景被拒；本部场景不受影响。
	if ps := AcceptanceBlockers(root, w, scene9, "strict"); !has(ps, CodeUnconfirmed) {
		t.Fatalf("strict problems = %+v", ps)
	}
	for _, p := range AcceptanceBlockers(root, w, scene9, "strict") {
		if p.Severity != workspace.SeverityError {
			t.Fatalf("severity = %+v", p)
		}
	}
	if ps := AcceptanceBlockers(root, w, part(t, w, "my-novel/scene-1"), "strict"); len(ps) != 0 {
		t.Fatalf("same-part problems = %+v, want none", ps)
	}
	// fast：提示不阻塞。
	ps := AcceptanceBlockers(root, w, scene9, "fast")
	if !has(ps, CodeUnconfirmed) {
		t.Fatalf("fast problems = %+v", ps)
	}
	for _, p := range ps {
		if p.Severity != workspace.SeverityWarning {
			t.Fatalf("fast severity = %+v", p)
		}
	}
	// 确认后：strict 阻塞清除。
	if _, _, err := Confirm(root, w, p1, "作者：本部核对无误", time.Now()); err != nil {
		t.Fatal(err)
	}
	if ps := AcceptanceBlockers(root, w, scene9, "strict"); len(ps) != 0 {
		t.Fatalf("after confirm problems = %+v", ps)
	}
}

func TestCheckRecord(t *testing.T) {
	root, w := newTwoParts(t)
	if _, _, ps := Checkpoint(root, w, part(t, w, "my-novel/part-one"), time.Now()); ps != nil {
		t.Fatal(ps)
	}
	// 手写悬空引用与不完整确认记录。
	writeAt(t, root, ".writing-anything/works/my-novel/units/part-one/checkpoint.yaml", `schema_version: 1
part: my-novel/part-one
scene: my-novel/scene-2
states: [state-9]
confirmed_by_author: true
`)
	w2, _ := work.Load(root, "my-novel")
	_, problems := CheckWorks(root, []*work.Work{w2})
	if !has(problems, CodeUnknownEntry) || !has(problems, CodeIncompleteConfirmation) {
		t.Fatalf("problems = %+v", problems)
	}
	if ps := CheckArtifact(root, ".writing-anything/works/my-novel/units/part-one/checkpoint.yaml"); !has(ps, CodeUnknownEntry) {
		t.Fatalf("artifact problems = %+v", ps)
	}
}

// 部的最后一个场景验收后自动生成检查点；不是最后场景、没有部祖先或部内其他场景未完成时不生成。
func TestOnAccept(t *testing.T) {
	root, w := newTwoParts(t)
	// scene-1 不是 part-one 的最后场景。
	if v, changed, ps := OnAccept(root, w, part(t, w, "my-novel/scene-1")); v != nil || changed != nil || ps != nil {
		t.Fatalf("scene-1: %+v %v %+v", v, changed, ps)
	}
	// 部单元本身不是场景。
	if v, _, _ := OnAccept(root, w, part(t, w, "my-novel/part-one")); v != nil {
		t.Fatalf("part: %+v", v)
	}
	v, changed, ps := OnAccept(root, w, part(t, w, "my-novel/scene-2"))
	if v == nil || ps != nil || v.ConfirmedByAuthor || v.Part != "my-novel/part-one" || v.Scene != "my-novel/scene-2" {
		t.Fatalf("scene-2: %+v %+v", v, ps)
	}
	if want := ".writing-anything/works/my-novel/units/part-one/checkpoint.yaml"; len(changed) != 1 || changed[0] != want {
		t.Fatalf("changed = %v", changed)
	}
	// 同指纹再次验收不写文件。
	if v, changed, ps := OnAccept(root, w, part(t, w, "my-novel/scene-2")); v == nil || len(changed) != 0 || ps != nil {
		t.Fatalf("again: %+v %v %+v", v, changed, ps)
	}
}

// 最后场景刚验收（阶段仍为 review）视为完成；部内其他场景未完成时只给出 warning、不生成。
func TestOnAcceptOthersOpen(t *testing.T) {
	root, w := newTwoParts(t, "scene-1")
	// scene-2 处于 done；把它视为刚验收的场景，scene-1 仍在 brief 阶段。
	v, changed, ps := OnAccept(root, w, part(t, w, "my-novel/scene-2"))
	if v != nil || changed != nil || len(ps) != 1 || ps[0].Code != CodeScenesOpen || ps[0].Severity != workspace.SeverityWarning ||
		ps[0].Path != ".writing-anything/works/my-novel/units/part-one/checkpoint.yaml" || !strings.Contains(ps[0].Message, "my-novel/scene-1") {
		t.Fatalf("others open: %+v %v %+v", v, changed, ps)
	}
	if _, err := os.Stat(filepath.Join(root, ".writing-anything", "works", "my-novel", "units", "part-one", FileName)); err == nil {
		t.Fatal("checkpoint must not be written")
	}
}

// 没有场景的部不参与缺失检查；没有部祖先的场景验收不生成检查点。
func TestEmptyPartAndNoPart(t *testing.T) {
	root := t.TempDir()
	if _, ps := workspace.Init(root, nil); len(ps) > 0 {
		t.Fatal(ps)
	}
	if _, _, ps := work.Create(root, "novel", "my-novel"); len(ps) > 0 {
		t.Fatal(ps)
	}
	for _, add := range []struct{ parent, typ, id string }{
		{"my-novel", "part", "part-zero"},
		{"my-novel", "part", "part-one"},
		{"my-novel/part-one", "chapter", "chapter-one"},
		{"my-novel/chapter-one", "scene", "scene-1"},
	} {
		if _, _, ps := work.AddUnit(root, add.parent, add.typ, add.id, work.Anchor{}); len(ps) > 0 {
			t.Fatal(ps)
		}
	}
	w, _ := work.Load(root, "my-novel")
	if ps := AcceptanceBlockers(root, w, part(t, w, "my-novel/scene-1"), "strict"); len(ps) != 0 {
		t.Fatalf("empty part problems = %+v, want none", ps)
	}
	if p := PartOf(w, part(t, w, "my-novel/part-one")); p != nil {
		t.Fatalf("PartOf(part) = %+v", p)
	}
}
