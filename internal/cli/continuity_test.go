package cli

import (
	"encoding/json"
	"os"
	"path/filepath"
	"slices"
	"strings"
	"testing"
)

// continuityFixture：novel 场景 scene-1 推进到审稿阶段，契约出场人物含未建卡的 高人。
func continuityFixture(t *testing.T) string {
	t.Helper()
	dir := newWorkspace(t, "--genre", "novel", "--gate-level", "fast")
	for _, args := range [][]string{
		{"new", "novel", "my-novel"},
		{"unit", "add", "my-novel", "part", "part-one"},
		{"unit", "add", "my-novel/part-one", "chapter", "chapter-one"},
		{"unit", "add", "my-novel/chapter-one", "scene", "scene-1"},
	} {
		if code, stdout, stderr := run(t, append(args, "--dir", dir)...); code != 0 {
			t.Fatalf("%v: exit code = %d; stdout: %s; stderr: %s", args, code, stdout, stderr)
		}
	}
	if code, res := runWS(t, dir, "canon", "add", "my-novel", "--type", "character", "--name", "守灯人", "--text", "五十岁"); code != 0 {
		t.Fatalf("canon add: %+v", res)
	}
	sceneDir := filepath.Join(dir, ".writing-anything", "works", "my-novel", "units", "scene-1")
	if err := os.MkdirAll(sceneDir, 0o755); err != nil {
		t.Fatal(err)
	}
	writeFile(t, filepath.Join(sceneDir, "brief.yaml"), "schema_version: 1\n")
	run(t, "advance", "my-novel/scene-1", "--dir", dir)
	writeFile(t, filepath.Join(sceneDir, "outline.yaml"), "schema_version: 1\nsections:\n  - {id: sc1, title: 场景, reason: 推进情节, pov: 守灯人, characters: [守灯人, 高人]}\n")
	run(t, "advance", "my-novel/scene-1", "--dir", dir)
	writeFile(t, filepath.Join(sceneDir, "draft.md"), "正文。\n")
	run(t, "advance", "my-novel/scene-1", "--dir", dir)
	return dir
}

// continuity 只读输出发现项；accept 被阻塞并生成豁免决策；作者允许后通过。
func TestContinuityAcceptFlow(t *testing.T) {
	dir := continuityFixture(t)
	code, res := runWS(t, dir, "continuity", "my-novel/scene-1")
	if code != 0 {
		t.Fatalf("continuity: exit code = %d, result = %+v", code, res)
	}
	if !contains(res.stdout, "continuity.unknown-character") || !contains(res.stdout, "高人") {
		t.Fatalf("findings = %s", res.stdout)
	}
	code, res = runWS(t, dir, "accept", "my-novel/scene-1", "--confirmed-by-author", "--confirmation", "作者：可以")
	if code != 1 {
		t.Fatalf("accept: exit code = %d, result = %+v", code, res)
	}
	d := diag(t, res, "continuity.unknown-character")
	if d.Code == "" {
		t.Fatalf("diagnostics = %+v", res.Diagnostics)
	}
	if !contains(strings.Join(d.Next, "\n"), "wa-cli continuity my-novel/scene-1") {
		t.Fatalf("next = %v", d.Next)
	}
	// 幂等：豁免决策只生成一条。
	_, ds := runWS(t, dir, "decisions")
	if got := countSubstr(ds.stdout, "是否豁免场景"); got != 1 {
		t.Fatalf("waiver decisions = %d, want 1", got)
	}
	// 作者允许后验收通过。
	run(t, "decide", "dec-1", "--option", "a", "--confirmation", "作者允许这次豁免", "--dir", dir)
	code, res = runWS(t, dir, "accept", "my-novel/scene-1", "--confirmed-by-author", "--confirmation", "作者：可以")
	if code != 0 {
		t.Fatalf("accept after waive: exit code = %d, result = %+v", code, res)
	}
}

// 无发现与非契约场景不受影响。
func TestContinuityCleanScene(t *testing.T) {
	dir := continuityFixture(t)
	// 去掉未建卡人物后无发现。
	sceneOutline := filepath.Join(dir, ".writing-anything", "works", "my-novel", "units", "scene-1", "outline.yaml")
	writeFile(t, sceneOutline, "schema_version: 1\nsections:\n  - {id: sc1, title: 场景, reason: 推进情节, pov: 守灯人, characters: [守灯人]}\n")
	code, res := runWS(t, dir, "continuity", "my-novel/scene-1")
	if code != 0 || contains(res.stdout, "continuity.") {
		t.Fatalf("clean scene: exit code = %d, stdout = %s", code, res.stdout)
	}
	if code, res = runWS(t, dir, "accept", "my-novel/scene-1", "--confirmed-by-author", "--confirmation", "作者：可以"); code != 0 {
		t.Fatalf("accept clean: exit code = %d, result = %+v", code, res)
	}
}

// review show 只读：连续性发现不写决策队列。
func TestContinuityReviewShowReadOnly(t *testing.T) {
	dir := continuityFixture(t)
	code, res := runWS(t, dir, "review", "show", "my-novel/scene-1")
	if code != 0 {
		t.Fatalf("review show: exit code = %d, result = %+v", code, res)
	}
	if !contains(res.stdout, "continuity.unknown-character") {
		t.Fatalf("gate blockers missing continuity: %s", res.stdout)
	}
	if len(res.ChangedPaths) != 0 {
		t.Fatalf("review show changedPaths = %v, want empty", res.ChangedPaths)
	}
	if _, err := os.Stat(filepath.Join(dir, ".writing-anything", "decisions.yaml")); err == nil {
		t.Fatal("review show must not create decisions.yaml")
	}
}

// 轻量契约场景（只有 characters 与 reveal）不参与连续性检查。
func TestContinuityLightContractSkipped(t *testing.T) {
	dir := newWorkspace(t)
	run(t, "new", "short-story", "my-story", "--dir", dir)
	run(t, "unit", "add", "my-story", "scene", "s1", "--dir", dir)
	sceneDir := filepath.Join(dir, ".writing-anything", "works", "my-story", "units", "s1")
	if err := os.MkdirAll(sceneDir, 0o755); err != nil {
		t.Fatal(err)
	}
	writeFile(t, filepath.Join(sceneDir, "outline.yaml"), "schema_version: 1\nsections:\n  - {id: sec1, title: 灯下, reason: 推进, characters: [守灯人], reveal: 秘密}\n")
	code, res := runWS(t, dir, "continuity", "my-story/s1")
	if code != 0 || contains(res.stdout, "continuity.") {
		t.Fatalf("light contract: exit code = %d, stdout = %s", code, res.stdout)
	}
	if !contains(res.stdout, `"findings":[]`) {
		t.Fatalf("findings = %s", res.stdout)
	}
}

func contains(s, sub string) bool { return strings.Contains(s, sub) }

func countSubstr(s, sub string) int {
	n := 0
	for i := 0; i+len(sub) <= len(s); i++ {
		if s[i:i+len(sub)] == sub {
			n++
			i += len(sub) - 1
		}
	}
	return n
}

// 时间线检查接入 continuity：同 time 双出场阻塞验收，修正声明后通过。
func TestContinuityTimelineGate(t *testing.T) {
	dir := continuityFixture(t)
	sceneDir := filepath.Join(dir, ".writing-anything", "works", "my-novel", "units", "scene-1")
	// 第二个场景补 time 声明（同为「第三日夜」且出场守灯人）触发 same-time-presence。
	run(t, "unit", "add", "my-novel/chapter-one", "scene", "scene-2", "--dir", dir)
	s2 := filepath.Join(dir, ".writing-anything", "works", "my-novel", "units", "scene-2")
	os.MkdirAll(s2, 0o755)
	writeFile(t, filepath.Join(s2, "outline.yaml"),
		"schema_version: 1\nsections:\n  - {id: sc1, title: 场景, reason: 推进, pov: 守灯人, characters: [守灯人], time: 第三日夜}\n")
	writeFile(t, filepath.Join(sceneDir, "outline.yaml"),
		"schema_version: 1\nsections:\n  - {id: sc1, title: 场景, reason: 推进, pov: 守灯人, characters: [守灯人], time: 第三日夜}\n")
	code, res := runWS(t, dir, "continuity", "my-novel/scene-1")
	if code != 0 || !contains(res.stdout, "timeline.same-time-presence") {
		t.Fatalf("continuity: exit code = %d, stdout = %s", code, res.stdout)
	}
	code, res = runWS(t, dir, "accept", "my-novel/scene-1", "--confirmed-by-author", "--confirmation", "作者：可以")
	if code != 1 || diag(t, res, "timeline.same-time-presence").Code == "" {
		t.Fatalf("accept: exit code = %d, diagnostics = %+v", code, res.Diagnostics)
	}
	// 时间线发现同走（场景， 错误码）豁免通道：生成豁免决策，next 指向 continuity 与 decisions。
	if !slices.Contains(res.ChangedPaths, ".writing-anything/decisions.yaml") {
		t.Fatalf("changedPaths = %v", res.ChangedPaths)
	}
	if next := strings.Join(diag(t, res, "timeline.same-time-presence").Next, "\n"); !contains(next, "wa-cli continuity my-novel/scene-1") || !contains(next, "wa-cli decisions") {
		t.Fatalf("next = %s", next)
	}
	// 修正声明（scene-2 晚于 scene-1）后通过。
	writeFile(t, filepath.Join(s2, "outline.yaml"),
		"schema_version: 1\nsections:\n  - {id: sc1, title: 场景, reason: 推进, pov: 守灯人, characters: [守灯人], time: 第三日夜, after: [scene-1]}\n")
	if code, res = runWS(t, dir, "continuity", "my-novel/scene-1"); code != 0 || contains(res.stdout, "timeline.") {
		t.Fatalf("after fix: exit code = %d, stdout = %s", code, res.stdout)
	}
	if code, res = runWS(t, dir, "accept", "my-novel/scene-1", "--confirmed-by-author", "--confirmation", "作者：可以"); code != 0 {
		t.Fatalf("accept after fix: exit code = %d, result = %+v", code, res)
	}
}

// 因果冲突经连续性审查阻塞验收：场景起因引用后续场景才声明的事件时，wa-cli continuity 报告 error 级发现（保留
// scene.event-not-yet-happened 原码），accept 被阻塞并生成豁免决策；修正起因后 continuity 全绿、accept 通过。
func TestContinuityCausalityBlocksAccept(t *testing.T) {
	dir := continuityFixture(t)
	run(t, "unit", "add", "my-novel/chapter-one", "scene", "scene-2", "--dir", dir)
	unitsDir := filepath.Join(dir, ".writing-anything", "works", "my-novel", "units")
	s2 := filepath.Join(unitsDir, "scene-2")
	if err := os.MkdirAll(s2, 0o755); err != nil {
		t.Fatal(err)
	}
	writeFile(t, filepath.Join(s2, "outline.yaml"),
		"schema_version: 1\nsections:\n  - {id: sc1, title: 场景, reason: 推进, pov: 守灯人, characters: [守灯人], events: [ev-lamp-lit]}\n")
	s1Outline := filepath.Join(unitsDir, "scene-1", "outline.yaml")
	writeFile(t, s1Outline,
		"schema_version: 1\nsections:\n  - {id: sc1, title: 场景, reason: 推进情节, pov: 守灯人, characters: [守灯人], causes: [ev-lamp-lit]}\n")

	code, res := runWS(t, dir, "continuity", "my-novel/scene-1")
	if code != 0 {
		t.Fatalf("continuity: exit = %d, diagnostics = %+v", code, res.Diagnostics)
	}
	var out struct {
		Findings []struct {
			Scene, Code, Severity, Message string
			Waived                         bool
		} `json:"findings"`
	}
	if err := json.Unmarshal([]byte(res.stdout), &out); err != nil {
		t.Fatal(err)
	}
	if len(out.Findings) != 1 {
		t.Fatalf("findings = %+v", out.Findings)
	}
	f := out.Findings[0]
	if f.Scene != "my-novel/scene-1" || f.Code != "continuity.dangling-reference" || f.Severity != "error" || f.Waived ||
		!contains(f.Message, "scene.event-not-yet-happened") || !contains(f.Message, "ev-lamp-lit") {
		t.Fatalf("finding = %+v", f)
	}
	code, res = runWS(t, dir, "accept", "my-novel/scene-1", "--confirmed-by-author", "--confirmation", "作者：可以")
	if d := diag(t, res, "continuity.dangling-reference"); code != 1 || d.Severity != "error" || !contains(d.Message, "scene.event-not-yet-happened") ||
		!slices.Contains(res.ChangedPaths, ".writing-anything/decisions.yaml") {
		t.Fatalf("accept: exit = %d, changedPaths = %v, diagnostics = %+v", code, res.ChangedPaths, res.Diagnostics)
	}
	if _, err := os.Stat(filepath.Join(unitsDir, "scene-1", "acceptance.yaml")); err == nil {
		t.Fatal("blocked accept must not write acceptance.yaml")
	}

	// 修正：删去引用尚未发生事件的起因（scene-2 仍声明该事件，后文可以引用）。
	writeFile(t, s1Outline,
		"schema_version: 1\nsections:\n  - {id: sc1, title: 场景, reason: 推进情节, pov: 守灯人, characters: [守灯人]}\n")
	if code, res = runWS(t, dir, "continuity", "my-novel"); code != 0 || !contains(res.stdout, `"findings":[]`) {
		t.Fatalf("continuity after fix: exit = %d, stdout = %s", code, res.stdout)
	}
	if code, res = runWS(t, dir, "accept", "my-novel/scene-1", "--confirmed-by-author", "--confirmation", "作者：可以"); code != 0 {
		t.Fatalf("accept after fix: exit = %d, diagnostics = %+v", code, res.Diagnostics)
	}
}
