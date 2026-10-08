package cli

import (
	"encoding/json"
	"os"
	"path/filepath"
	"slices"
	"strings"
	"testing"
)

// volumeAcceptFixture：novel 两部各一场景，part-one 的场景 done；part-two 的 scene-9 推进到审稿阶段。
func volumeAcceptFixture(t *testing.T, gate string) string {
	t.Helper()
	dir := newWorkspace(t, "--genre", "novel", "--gate-level", gate)
	for _, args := range [][]string{
		{"new", "novel", "my-novel"},
		{"unit", "add", "my-novel", "part", "part-one"},
		{"unit", "add", "my-novel/part-one", "chapter", "chapter-one"},
		{"unit", "add", "my-novel/chapter-one", "scene", "scene-1"},
		{"unit", "add", "my-novel", "part", "part-two"},
		{"unit", "add", "my-novel/part-two", "chapter", "chapter-two"},
		{"unit", "add", "my-novel/chapter-two", "scene", "scene-9"},
	} {
		if code, stdout, stderr := run(t, append(args, "--dir", dir)...); code != 0 {
			t.Fatalf("%v: exit code = %d; stdout: %s; stderr: %s", args, code, stdout, stderr)
		}
	}
	for _, s := range []string{"scene-1", "scene-9"} {
		sceneDir := filepath.Join(dir, ".writing-anything", "works", "my-novel", "units", s)
		if err := os.MkdirAll(sceneDir, 0o755); err != nil {
			t.Fatal(err)
		}
		writeFile(t, filepath.Join(sceneDir, "brief.yaml"), "schema_version: 1\n")
		run(t, "advance", "my-novel/"+s, "--dir", dir)
		writeFile(t, filepath.Join(sceneDir, "outline.yaml"), "schema_version: 1\nsections:\n  - {id: sc1, title: 场景, reason: 推进情节}\n")
		run(t, "advance", "my-novel/"+s, "--dir", dir)
		writeFile(t, filepath.Join(sceneDir, "draft.md"), "正文。\n")
		run(t, "advance", "my-novel/"+s, "--dir", dir)
	}
	// scene-1 完成验收（fast 记录不影响 standard/strict 下的推进：acceptance.weaker-gate 只在 advance 时检查，
	// 这里 scene-1 的推进在验收前完成）。
	run(t, "review", "record", "my-novel/scene-1", "--file", reviewInput(t, dir), "--dir", dir)
	run(t, "accept", "my-novel/scene-1", "--confirmed-by-author", "--confirmation", "作者：可以", "--dir", dir)
	run(t, "advance", "my-novel/scene-1", "--dir", dir)
	return dir
}

// strict 下：前部检查点未确认时下一部场景验收被拒；确认后通过。
func TestVolumeAcceptGate(t *testing.T) {
	dir := volumeAcceptFixture(t, "fast")
	// 生成 part-one 的检查点。
	if code, res := runWS(t, dir, "volume", "checkpoint", "my-novel/part-one"); code != 0 {
		t.Fatalf("checkpoint: exit code = %d, result = %+v", code, res)
	}
	// 切到 strict：part-one 的 fast 验收记录会阻塞 scene-1 的推进，但 scene-9 的验收只受收口与卷末检查点影响
	// （scene-1 已 done）。重新验收 scene-9 的前提条件在 fast 下本就满足。
	run(t, "config", "set", "gate_level", "strict", "--dir", dir)
	run(t, "review", "record", "my-novel/scene-9", "--file", reviewInput(t, dir), "--dir", dir)
	code, res := runWS(t, dir, "accept", "my-novel/scene-9", "--confirmed-by-author", "--confirmation", "作者：可以")
	if code != 1 {
		t.Fatalf("accept under strict: exit code = %d, result = %+v", code, res)
	}
	if d := diag(t, res, "volume.unconfirmed"); d.Code == "" {
		t.Fatalf("diagnostics = %+v", res.Diagnostics)
	}
	if _, err := os.Stat(filepath.Join(dir, ".writing-anything", "works", "my-novel", "units", "scene-9", "acceptance.yaml")); err == nil {
		t.Fatal("acceptance.yaml must not be written while blocked")
	}
	// 缺少作者原话摘要时拒绝确认，next 先给出该部的下一步（cli/diagnostic-next-concrete）。
	if code, res := runWS(t, dir, "volume", "confirm", "my-novel/part-one", "--confirmed-by-author"); code != 1 ||
		!concreteFirst(diag(t, res, "volume.incomplete-confirmation"), shellCommand("wa-cli", "next", "my-novel/part-one", "--dir", dir, "--json"), "wa-cli help volume confirm --json") {
		t.Fatalf("incomplete confirmation: exit code = %d, result = %+v", code, res)
	}
	// 确认检查点后通过。
	if code, res := runWS(t, dir, "volume", "confirm", "my-novel/part-one", "--confirmed-by-author", "--confirmation", "作者：本部核对无误"); code != 0 {
		t.Fatalf("confirm: exit code = %d, result = %+v", code, res)
	}
	if code, res := runWS(t, dir, "accept", "my-novel/scene-9", "--confirmed-by-author", "--confirmation", "作者：可以"); code != 0 {
		t.Fatalf("accept after confirm: exit code = %d, result = %+v", code, res)
	}
}

// fast 关卡强度下检查点只提示不阻塞。
func TestVolumeAcceptFastWarns(t *testing.T) {
	dir := volumeAcceptFixture(t, "fast")
	runWS(t, dir, "volume", "checkpoint", "my-novel/part-one")
	code, res := runWS(t, dir, "accept", "my-novel/scene-9", "--confirmed-by-author", "--confirmation", "作者：可以")
	if code != 0 {
		t.Fatalf("accept under fast: exit code = %d, result = %+v", code, res)
	}
	for _, d := range res.Diagnostics {
		if d.Code == "volume.unconfirmed" && d.Severity != "warning" {
			t.Fatalf("volume.unconfirmed under fast should warn: %+v", d)
		}
	}
}

// volume checkpoint 的场景未完成拒绝与人读输出。
func TestVolumeCheckpointCLI(t *testing.T) {
	dir := volumeAcceptFixture(t, "fast")
	// scene-9 还没验收：part-two 的检查点被拒。
	if code, res := runWS(t, dir, "volume", "checkpoint", "my-novel/part-two"); code != 1 || diag(t, res, "volume.scenes-open").Code == "" {
		t.Fatalf("part-two: exit code = %d, diagnostics = %+v", code, res.Diagnostics)
	}
	code, stdout, stderr := run(t, "volume", "checkpoint", "my-novel/part-one", "--dir", dir)
	if code != 0 || stderr != "" {
		t.Fatalf("checkpoint: exit code = %d, stderr = %q", code, stderr)
	}
	if !strings.Contains(stdout, "卷末检查点") {
		t.Fatalf("human output: %q", stdout)
	}
	// 非部单元被拒。
	if code, res := runWS(t, dir, "volume", "checkpoint", "my-novel/chapter-one"); code != 1 ||
		!concreteFirst(diag(t, res, "volume.not-volume"), shellCommand("wa-cli", "status", "--dir", dir, "--json"), "wa-cli help volume checkpoint --json") {
		t.Fatalf("chapter: exit code = %d, diagnostics = %+v", code, res.Diagnostics)
	}
	// validate 计入 checked。
	code, res := runWS(t, dir, "validate")
	if code != 0 {
		t.Fatalf("validate: exit code = %d, result = %+v", code, res)
	}
	if !strings.Contains(strings.Join(res.Checked, "\n"), "units/part-one/checkpoint.yaml") {
		t.Fatalf("checked = %v", res.Checked)
	}
	// validate <path>。
	if code, res := runWS(t, dir, "validate", ".writing-anything/works/my-novel/units/part-one/checkpoint.yaml"); code != 0 {
		t.Fatalf("validate path: exit code = %d, result = %+v", code, res)
	}
}

// partOneCheckpoint 返回 part-one 卷末检查点的文件路径。
func partOneCheckpoint(dir string) string {
	return filepath.Join(dir, ".writing-anything", "works", "my-novel", "units", "part-one", "checkpoint.yaml")
}

// 部的最后一个场景验收后自动生成检查点（只写汇总、不代作者确认），changedPaths 依次列出验收记录与检查点；
// next 对该部列出 volume confirm，确认后不再列出。
func TestVolumeAutoCheckpoint(t *testing.T) {
	dir := volumeAcceptFixture(t, "strict")
	if c := readFile(t, partOneCheckpoint(dir)); !strings.Contains(c, "confirmed_by_author: false") || !strings.Contains(c, "scene: my-novel/scene-1") {
		t.Fatalf("auto checkpoint: %s", c)
	}
	var next struct {
		Steps []struct {
			Action   string   `json:"action"`
			Commands []string `json:"commands"`
		} `json:"steps"`
	}
	_, res := runWS(t, dir, "next", "my-novel/part-one")
	if err := json.Unmarshal([]byte(res.stdout), &next); err != nil || len(next.Steps) != 1 {
		t.Fatalf("next: %v %s", err, res.stdout)
	}
	want := []string{"wa-cli volume confirm my-novel/part-one --dir " + dir + " --json", "wa-cli help volume confirm --json"}
	if !slices.Equal(next.Steps[0].Commands, want) || !strings.Contains(next.Steps[0].Action, "wa-cli volume confirm") {
		t.Fatalf("next part-one = %+v", next.Steps[0])
	}
	// status 的部单元与 next 列出同样的命令。
	if _, res := runWS(t, dir, "status"); !strings.Contains(res.stdout, `"wa-cli volume confirm my-novel/part-one --dir `) {
		t.Fatalf("status lacks volume confirm: %s", res.stdout)
	}
	if code, res := runWS(t, dir, "volume", "confirm", "my-novel/part-one", "--confirmed-by-author", "--confirmation", "作者：本部核对无误"); code != 0 {
		t.Fatalf("confirm: %d %+v", code, res)
	}
	_, res = runWS(t, dir, "next", "my-novel/part-one")
	if err := json.Unmarshal([]byte(res.stdout), &next); err != nil || len(next.Steps) != 1 || len(next.Steps[0].Commands) != 0 {
		t.Fatalf("next after confirm: %v %s", err, res.stdout)
	}
	// part-two 的唯一场景验收：changedPaths 依次为验收记录与 part-two 的检查点。
	run(t, "review", "record", "my-novel/scene-9", "--file", reviewInput(t, dir), "--dir", dir)
	code, res := runWS(t, dir, "accept", "my-novel/scene-9", "--confirmed-by-author", "--confirmation", "作者：可以")
	wantPaths := []string{".writing-anything/works/my-novel/units/scene-9/acceptance.yaml", ".writing-anything/works/my-novel/units/part-two/checkpoint.yaml"}
	if code != 0 || !slices.Equal(res.ChangedPaths, wantPaths) {
		t.Fatalf("accept scene-9: %d %v %+v", code, res.ChangedPaths, res.Diagnostics)
	}
}

// strict 下更早的部缺少检查点时阻塞验收（volume.missing），生成并确认后放行；standard 只提示。
func TestVolumeMissingGate(t *testing.T) {
	dir := volumeAcceptFixture(t, "strict")
	if err := os.Remove(partOneCheckpoint(dir)); err != nil {
		t.Fatal(err)
	}
	run(t, "review", "record", "my-novel/scene-9", "--file", reviewInput(t, dir), "--dir", dir)
	code, res := runWS(t, dir, "accept", "my-novel/scene-9", "--confirmed-by-author", "--confirmation", "作者：可以")
	d := diag(t, res, "volume.missing")
	if code != 1 || d.Severity != "error" || d.Path != ".writing-anything/works/my-novel/units/part-one/checkpoint.yaml" ||
		!hasNext(d, "wa-cli volume checkpoint my-novel/part-one --dir") || !hasNext(d, "wa-cli review show my-novel/scene-9") {
		t.Fatalf("missing: %d %+v", code, res.Diagnostics)
	}
	if _, err := os.Stat(filepath.Join(dir, ".writing-anything", "works", "my-novel", "units", "scene-9", "acceptance.yaml")); err == nil {
		t.Fatal("acceptance.yaml must not be written while blocked")
	}
	// review show 同样列出阻塞项。
	if _, res := runWS(t, dir, "review", "show", "my-novel/scene-9"); !strings.Contains(res.stdout, `"code":"volume.missing"`) {
		t.Fatalf("review show: %s", res.stdout)
	}
	// 不通过校验的检查点：volume.invalid，next 给出校验命令而不是确认。
	writeFile(t, partOneCheckpoint(dir), "schema_version: 2\n")
	code, res = runWS(t, dir, "accept", "my-novel/scene-9", "--confirmed-by-author", "--confirmation", "作者：可以")
	if d := diag(t, res, "volume.invalid"); code != 1 || !hasNext(d, "wa-cli validate .writing-anything/works/my-novel/units/part-one/checkpoint.yaml") || hasNext(d, "wa-cli volume confirm") {
		t.Fatalf("invalid: %d %+v", code, res.Diagnostics)
	}
	if err := os.Remove(partOneCheckpoint(dir)); err != nil {
		t.Fatal(err)
	}
	mustRunWS(t, dir, "volume", "checkpoint", "my-novel/part-one")
	if code, res := runWS(t, dir, "accept", "my-novel/scene-9", "--confirmed-by-author", "--confirmation", "作者：可以"); code != 1 || diag(t, res, "volume.unconfirmed").Code == "" {
		t.Fatalf("unconfirmed: %d %+v", code, res.Diagnostics)
	}
	mustRunWS(t, dir, "volume", "confirm", "my-novel/part-one", "--confirmed-by-author", "--confirmation", "作者：本部核对无误")
	if code, res := runWS(t, dir, "accept", "my-novel/scene-9", "--confirmed-by-author", "--confirmation", "作者：可以"); code != 0 {
		t.Fatalf("after confirm: %d %+v", code, res.Diagnostics)
	}

	// standard：缺失只提示，验收成功。
	dir = volumeAcceptFixture(t, "standard")
	if err := os.Remove(partOneCheckpoint(dir)); err != nil {
		t.Fatal(err)
	}
	run(t, "review", "record", "my-novel/scene-9", "--file", reviewInput(t, dir), "--dir", dir)
	code, res = runWS(t, dir, "accept", "my-novel/scene-9")
	if d := diag(t, res, "volume.missing"); code != 0 || d.Severity != "warning" {
		t.Fatalf("standard missing: %d %+v", code, res.Diagnostics)
	}
}

// 部内其他场景还没推进到 done 时，最后场景的验收照常成功，不生成检查点，给出 warning 级 volume.scenes-open。
func TestVolumeAutoCheckpointOthersOpen(t *testing.T) {
	dir := newWorkspace(t, "--genre", "novel", "--gate-level", "fast")
	for _, args := range [][]string{
		{"new", "novel", "my-novel"},
		{"unit", "add", "my-novel", "part", "part-one"},
		{"unit", "add", "my-novel/part-one", "chapter", "chapter-one"},
		{"unit", "add", "my-novel/chapter-one", "scene", "scene-a"},
		{"unit", "add", "my-novel/chapter-one", "scene", "scene-b"},
	} {
		mustRunWS(t, dir, args...)
	}
	sceneDir := filepath.Join(dir, ".writing-anything", "works", "my-novel", "units", "scene-b")
	if err := os.MkdirAll(sceneDir, 0o755); err != nil {
		t.Fatal(err)
	}
	writeFile(t, filepath.Join(sceneDir, "brief.yaml"), "schema_version: 1\n")
	run(t, "advance", "my-novel/scene-b", "--dir", dir)
	writeFile(t, filepath.Join(sceneDir, "outline.yaml"), "schema_version: 1\nsections:\n  - {id: sc1, title: 场景, reason: 推进情节}\n")
	run(t, "advance", "my-novel/scene-b", "--dir", dir)
	writeFile(t, filepath.Join(sceneDir, "draft.md"), "正文。\n")
	run(t, "advance", "my-novel/scene-b", "--dir", dir)
	run(t, "review", "record", "my-novel/scene-b", "--file", reviewInput(t, dir), "--dir", dir)
	code, res := runWS(t, dir, "accept", "my-novel/scene-b")
	d := diag(t, res, "volume.scenes-open")
	if code != 0 || d.Severity != "warning" || !strings.Contains(d.Message, "my-novel/scene-a") ||
		!hasNext(d, "wa-cli volume checkpoint my-novel/part-one --dir") || slices.Contains(res.ChangedPaths, ".writing-anything/works/my-novel/units/part-one/checkpoint.yaml") {
		t.Fatalf("others open: %d %v %+v", code, res.ChangedPaths, res.Diagnostics)
	}
	if _, err := os.Stat(partOneCheckpoint(dir)); err == nil {
		t.Fatal("checkpoint must not be written")
	}
}
