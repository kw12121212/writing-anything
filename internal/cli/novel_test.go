package cli

import (
	"encoding/json"
	"os"
	"path/filepath"
	"slices"
	"strings"
	"testing"
)

// newNovel 返回一个已初始化的 novel 工作区，其中创建了作品 my-novel：
// part-one 下有 chapter-one（scene-1、scene-2）与 chapter-two（scene-3）。
func newNovel(t *testing.T) string {
	t.Helper()
	dir := newWorkspace(t, "--genre", "novel")
	for _, args := range [][]string{
		{"new", "novel", "my-novel"},
		{"unit", "add", "my-novel", "part", "part-one"},
		{"unit", "add", "my-novel/part-one", "chapter", "chapter-one"},
		{"unit", "add", "my-novel/chapter-one", "scene", "scene-1"},
		{"unit", "add", "my-novel/chapter-one", "scene", "scene-2"},
		{"unit", "add", "my-novel/part-one", "chapter", "chapter-two"},
		{"unit", "add", "my-novel/chapter-two", "scene", "scene-3"},
	} {
		if code, stdout, stderr := run(t, append(args, "--dir", dir)...); code != 0 {
			t.Fatalf("%v: exit code = %d; stdout: %s; stderr: %s", args, code, stdout, stderr)
		}
	}
	return dir
}

// novel 文体与层级：new 创建 novel 作品；config resolved 输出 unitTypes 的 stageFromChildren；
// 层级外的类型被拒绝（novel-genre、aggregated-unit-types）。
func TestNovelHierarchy(t *testing.T) {
	dir := newWorkspace(t, "--genre", "novel")
	code, res := runWS(t, dir, "new", "novel", "my-novel")
	if code != 0 || !res.Valid {
		t.Fatalf("new novel: exit code = %d, result = %+v", code, res)
	}
	if code, res := runWS(t, dir, "config", "resolved"); code != 0 {
		t.Fatalf("config resolved: exit code = %d", code)
	} else {
		var out struct {
			Resolved struct {
				UnitTypes []struct {
					ID                string `json:"id"`
					StageFromChildren bool   `json:"stageFromChildren"`
				} `json:"unitTypes"`
				Rules map[string]struct {
					Severity struct {
						Value string `json:"value"`
					} `json:"severity"`
				} `json:"rules"`
			} `json:"resolved"`
		}
		if err := json.Unmarshal([]byte(res.stdout), &out); err != nil {
			t.Fatal(err)
		}
		var got []string
		for _, u := range out.Resolved.UnitTypes {
			if u.StageFromChildren {
				got = append(got, u.ID)
			}
		}
		if !slices.Equal(got, []string{"part", "chapter"}) {
			t.Fatalf("aggregated unit types = %v", got)
		}
		for _, id := range []string{"length", "fiction-cliches", "dialogue", "pov"} {
			if out.Resolved.Rules[id].Severity.Value != "warning" {
				t.Fatalf("rule %s missing from resolved config", id)
			}
		}
		if _, ok := out.Resolved.Rules["claims"]; ok {
			t.Fatalf("novel must not declare claim rules")
		}
	}
	// novel 根类型只允许 part 子单元。
	if code, res := runUnits(t, dir, "unit", "add", "my-novel", "scene", "sx"); code != 1 || diag(t, res.result, "work.child-type-not-allowed").Code == "" {
		t.Fatalf("scene under novel root: exit code = %d", code)
	}
}

// 汇总单元在 status 与 next 中的输出：推导阶段、progress、汇总阻塞项与空 commands；
// advance 汇总单元被拒绝（derived-stage、status-aggregated-units）。
func TestAggregatedStatusNext(t *testing.T) {
	dir := newNovel(t)
	// scene-1 走到 done：写入全部必需产物并推进。
	completeScene(t, dir, "scene-1")
	// chapter-one 推导为 draft（scene-2 还在 brief，scene-1 已 done），chapter-two 为 brief。
	code, res := runUnits(t, dir, "status")
	if code != 0 {
		t.Fatalf("status: exit code = %d", code)
	}
	view := map[string]map[string]any{}
	for _, u := range res.Units {
		view[u["unit"].(string)] = u
	}
	ch1 := view["my-novel/chapter-one"]
	if ch1["stage"] != "brief" {
		t.Fatalf("chapter-one stage = %v, want brief (scene-2 still in brief)", ch1["stage"])
	}
	if p := ch1["progress"].(map[string]any); p["done"] != float64(1) || p["total"] != float64(2) {
		t.Fatalf("chapter-one progress = %v, want 1/2", ch1["progress"])
	}
	part := view["my-novel/part-one"]
	if p := part["progress"].(map[string]any); p["done"] != float64(1) || p["total"] != float64(3) {
		t.Fatalf("part-one progress = %v, want 1/3", part["progress"])
	}
	if view["my-novel/scene-1"]["progress"] != nil {
		t.Fatalf("non-aggregated unit progress = %v, want null", view["my-novel/scene-1"]["progress"])
	}
	// 汇总单元的阻塞项为并集，commands 为空数组。
	if code, res := runUnits(t, dir, "next", "my-novel/chapter-one"); code != 0 || len(res.Steps) != 1 {
		t.Fatalf("next chapter-one: exit code = %d, steps = %v", code, res.Steps)
	} else {
		s := res.Steps[0]
		if len(s["blockers"].([]any)) != 1 { // 只有 scene-2 缺 brief 产物
			t.Fatalf("chapter-one blockers = %v", s["blockers"])
		}
		if len(s["commands"].([]any)) != 0 {
			t.Fatalf("aggregated commands = %v, want empty", s["commands"])
		}
		if !strings.Contains(s["action"].(string), "1") {
			t.Fatalf("action = %v, want unfinished scene count", s["action"])
		}
	}
	// advance 汇总单元被拒绝且不修改文件。
	before := readFile(t, workFile(dir, "my-novel"))
	if code, res := runUnits(t, dir, "advance", "my-novel/chapter-one"); code != 1 {
		t.Fatalf("advance aggregated: exit code = %d", code)
	} else if d := diag(t, res.result, "lifecycle.derived-stage"); d.Path != ".writing-anything/works/my-novel/work.yaml" ||
		!slices.Contains(d.Next, shellCommand("wa-cli", "status", "--dir", dir, "--json")) {
		t.Fatalf("derived-stage diagnostic = %+v", d)
	}
	if after := readFile(t, workFile(dir, "my-novel")); after != before {
		t.Fatalf("work.yaml changed after rejected advance")
	}
	// next 不带参数时不再列出推导阶段为 done 的汇总单元。
	code, res = runUnits(t, dir, "next")
	if code != 0 {
		t.Fatalf("next: exit code = %d", code)
	}
	for _, s := range res.Steps {
		if s["unit"] == "my-novel/chapter-one" && s["stage"] == "done" {
			t.Fatalf("done aggregated unit listed: %v", s)
		}
	}
}

// unit add 的 --before/--after 插入与互斥校验（unit-insert）。
func TestUnitInsertFlags(t *testing.T) {
	dir := newNovel(t)
	code, res := runUnits(t, dir, "unit", "add", "my-novel/chapter-one", "scene", "scene-0", "--before", "scene-1")
	if code != 0 || res.Unit["unit"] != "my-novel/scene-0" || res.Unit["parent"] != "my-novel/chapter-one" {
		t.Fatalf("insert before: exit code = %d, unit = %v", code, res.Unit)
	}
	src := readFile(t, workFile(dir, "my-novel"))
	i1, i0, i2 := strings.Index(src, "id: scene-1"), strings.Index(src, "id: scene-0"), strings.Index(src, "id: scene-2")
	if !(i0 < i1 && i1 < i2) {
		t.Fatalf("order after insert before: scene-0 at %d, scene-1 at %d, scene-2 at %d", i0, i1, i2)
	}
	if code, _ := runUnits(t, dir, "unit", "add", "my-novel/chapter-one", "scene", "scene-9", "--after", "scene-2"); code != 0 {
		t.Fatalf("insert after: exit code = %d", code)
	} else if src := readFile(t, workFile(dir, "my-novel")); strings.Index(src, "id: scene-2") > strings.Index(src, "id: scene-9") {
		t.Fatalf("order after insert after")
	}
	before := readFile(t, workFile(dir, "my-novel"))
	// 锚点在别的父单元之下。
	if code, res := runUnits(t, dir, "unit", "add", "my-novel/chapter-one", "scene", "sx", "--before", "scene-3"); code != 1 {
		t.Fatalf("anchor elsewhere: exit code = %d", code)
	} else if d := diag(t, res.result, "unit.not-sibling"); !strings.Contains(d.Message, "scene-3") ||
		!slices.Contains(d.Next, "wa-cli help unit add --json") {
		t.Fatalf("not-sibling diagnostic = %+v", d)
	}
	// 锚点不存在。
	if code, res := runUnits(t, dir, "unit", "add", "my-novel/chapter-one", "scene", "sx", "--before", "nope"); code != 1 ||
		diag(t, res.result, "unit.not-found").Code == "" {
		t.Fatalf("anchor missing: exit code = %d", code)
	}
	// 两个锚点参数同时给出，或取值为空。
	if code, res := runUnits(t, dir, "unit", "add", "my-novel/chapter-one", "scene", "sx", "--before", "scene-1", "--after", "scene-1"); code != 1 {
		t.Fatalf("both flags: exit code = %d", code)
	} else if d := diag(t, res.result, "input.invalid-value"); d.Field != "before" {
		t.Fatalf("both flags diagnostic = %+v", d)
	}
	if code, res := runUnits(t, dir, "unit", "add", "my-novel/chapter-one", "scene", "sx", "--before", ""); code != 1 ||
		diag(t, res.result, "input.invalid-value").Code == "" {
		t.Fatalf("empty anchor value: exit code = %d", code)
	}
	if after := readFile(t, workFile(dir, "my-novel")); after != before {
		t.Fatalf("work.yaml changed after rejected inserts")
	}
	// 不带锚点仍在末尾追加。
	if code, _ := runUnits(t, dir, "unit", "add", "my-novel/chapter-two", "scene", "scene-4"); code != 0 {
		t.Fatalf("append without anchor: exit code = %d", code)
	} else if src := readFile(t, workFile(dir, "my-novel")); strings.Index(src, "id: scene-3") > strings.Index(src, "id: scene-4") {
		t.Fatalf("append at end")
	}
}

// unit move：跨父移动、同父重排序与全部拒绝路径；id、引用与单元目录保持不变（unit-move）。
func TestUnitMoveCommand(t *testing.T) {
	dir := newNovel(t)
	unitDir := filepath.Join(dir, ".writing-anything", "works", "my-novel", "units", "scene-1")
	if err := os.MkdirAll(unitDir, 0o755); err != nil {
		t.Fatal(err)
	}
	// 跨父移动：scene-1 移到 chapter-two，插到 scene-3 之前。
	code, res := runUnits(t, dir, "unit", "move", "my-novel/scene-1", "my-novel/chapter-two", "--before", "scene-3")
	if code != 0 || res.Unit["unit"] != "my-novel/scene-1" || res.Unit["parent"] != "my-novel/chapter-two" || res.Unit["type"] != "scene" {
		t.Fatalf("move between chapters: exit code = %d, unit = %v", code, res.Unit)
	}
	if !slices.Equal(res.ChangedPaths, []string{".writing-anything/works/my-novel/work.yaml"}) {
		t.Fatalf("changedPaths = %v", res.ChangedPaths)
	}
	if _, err := os.Stat(unitDir); err != nil {
		t.Fatalf("unit directory must not move: %v", err)
	}
	src := readFile(t, workFile(dir, "my-novel"))
	if !(strings.Index(src, "id: scene-1") > strings.Index(src, "id: chapter-two")) {
		t.Fatalf("scene-1 must be nested under chapter-two")
	}
	// 同父重排序：chapter-two 内把 scene-1 移到末尾。
	if code, _ := runUnits(t, dir, "unit", "move", "my-novel/scene-1", "my-novel/chapter-two"); code != 0 {
		t.Fatalf("reorder same parent: exit code = %d", code)
	} else if src := readFile(t, workFile(dir, "my-novel")); strings.Index(src, "id: scene-1") < strings.Index(src, "id: scene-3") {
		t.Fatalf("scene-1 must come after scene-3")
	}
	before := readFile(t, workFile(dir, "my-novel"))
	for _, tc := range []struct {
		name  string
		args  []string
		code  string
		field string
	}{
		{"under-self", []string{"unit", "move", "my-novel/part-one", "my-novel/chapter-one"}, "unit.move-under-self", ""},
		{"root", []string{"unit", "move", "my-novel", "my-novel/part-one"}, "unit.move-root", ""},
		{"disallowed", []string{"unit", "move", "my-novel/chapter-one", "my-novel"}, "work.child-type-not-allowed", ""},
		{"anchor-elsewhere", []string{"unit", "move", "my-novel/scene-2", "my-novel/chapter-two", "--before", "part-one"}, "unit.not-sibling", ""},
		{"unit-missing", []string{"unit", "move", "my-novel/nope", "my-novel/part-one"}, "unit.not-found", ""},
		{"parent-missing", []string{"unit", "move", "my-novel/scene-2", "my-novel/nope"}, "unit.not-found", ""},
		{"cross-work", []string{"unit", "move", "my-novel/scene-2", "other-work/chapter"}, "input.invalid-value", "parent"},
		{"both-flags", []string{"unit", "move", "my-novel/scene-2", "my-novel/chapter-one", "--before", "scene-2", "--after", "scene-2"}, "input.invalid-value", "before"},
	} {
		code, res := runUnits(t, dir, tc.args...)
		if code != 1 || diag(t, res.result, tc.code).Code == "" {
			t.Fatalf("%s: exit code = %d, diagnostics = %+v", tc.name, code, res.Diagnostics)
		}
		if tc.field != "" && diag(t, res.result, tc.code).Field != tc.field {
			t.Fatalf("%s: field = %q, want %q", tc.name, diag(t, res.result, tc.code).Field, tc.field)
		}
	}
	if after := readFile(t, workFile(dir, "my-novel")); after != before {
		t.Fatalf("work.yaml changed after rejected moves")
	}
	// 注释保留：手工加注释后移动。
	commented := strings.Replace(before, "id: part-one", "# 部一\n    id: part-one", 1)
	writeFile(t, workFile(dir, "my-novel"), commented)
	if code, _ := runUnits(t, dir, "unit", "move", "my-novel/scene-2", "my-novel/chapter-two"); code != 0 {
		t.Fatalf("move with comments: exit code = %d", code)
	} else if src := readFile(t, workFile(dir, "my-novel")); !strings.Contains(src, "# 部一") {
		t.Fatalf("comment lost:\n%s", src)
	}
}

// completeScene 把场景 scene 走完 brief 到 done 的全部阶段（fast 关卡下验收）。
func completeScene(t *testing.T, dir, scene string) {
	t.Helper()
	run(t, "config", "set", "gate_level", "fast", "--dir", dir)
	unit := "my-novel/" + scene
	sceneDir := filepath.Join(dir, ".writing-anything", "works", "my-novel", "units", scene)
	if err := os.MkdirAll(sceneDir, 0o755); err != nil {
		t.Fatal(err)
	}
	writeFile(t, filepath.Join(sceneDir, "brief.yaml"), "schema_version: 1\n")
	run(t, "advance", unit, "--dir", dir)
	writeFile(t, filepath.Join(sceneDir, "outline.yaml"), "schema_version: 1\nsections:\n  - {id: s1, title: 场景, reason: 推进情节}\n")
	run(t, "advance", unit, "--dir", dir)
	writeFile(t, filepath.Join(sceneDir, "draft.md"), "正文。\n")
	run(t, "advance", unit, "--dir", dir)
	run(t, "review", "record", unit, "--file", reviewInput(t, dir), "--dir", dir)
	run(t, "accept", unit, "--confirmed-by-author", "--confirmation", "作者：可以", "--dir", dir)
	run(t, "advance", unit, "--dir", dir)
}

// reviewInput 写入并通过场景的审稿记录文件，返回其路径。
func reviewInput(t *testing.T, dir string) string {
	t.Helper()
	p := filepath.Join(dir, "review-input.yaml")
	writeFile(t, p, "rubric: []\nfindings: []\n")
	return p
}

// 长篇端到端：init --genre novel → new → 建 部/章/场景 → 插入与移动（id 与单元目录稳定）→
// 场景走完整生命周期 → status/next 输出汇总进度 → advance 章被拒绝 → 全部完成后部与章推导为 done。
func TestNovelE2E(t *testing.T) {
	dir := newWorkspace(t, "--genre", "novel", "--gate-level", "fast")
	runOK := func(args ...string) {
		t.Helper()
		if code, stdout, stderr := run(t, append(args, "--dir", dir)...); code != 0 {
			t.Fatalf("%v: exit code = %d; stdout: %s; stderr: %s", args, code, stdout, stderr)
		}
	}
	runOK("new", "novel", "my-novel")
	runOK("unit", "add", "my-novel", "part", "part-one")
	runOK("unit", "add", "my-novel/part-one", "chapter", "chapter-one")
	runOK("unit", "add", "my-novel/chapter-one", "scene", "scene-2")
	// 插入：scene-1 插到 scene-2 之前，成为章一的首个场景。
	runOK("unit", "add", "my-novel/chapter-one", "scene", "scene-1", "--before", "scene-2")
	runOK("unit", "add", "my-novel/part-one", "chapter", "chapter-two")
	runOK("unit", "add", "my-novel/chapter-two", "scene", "scene-3")
	// 移动：scene-1 从章一移到章二末尾；id、引用与单元目录不变。
	sceneDir := filepath.Join(dir, ".writing-anything", "works", "my-novel", "units", "scene-1")
	if err := os.MkdirAll(sceneDir, 0o755); err != nil {
		t.Fatal(err)
	}
	code, res := runUnits(t, dir, "unit", "move", "my-novel/scene-1", "my-novel/chapter-two")
	if code != 0 || res.Unit["unit"] != "my-novel/scene-1" || res.Unit["parent"] != "my-novel/chapter-two" {
		t.Fatalf("move scene-1: exit code = %d, unit = %v", code, res.Unit)
	}
	if _, err := os.Stat(sceneDir); err != nil {
		t.Fatalf("unit directory must not move: %v", err)
	}
	// 场景一走完整生命周期。
	completeScene(t, dir, "scene-1")
	// 章一只剩 scene-2（brief）、章二有 scene-3（brief）与 scene-1（done）：两者推导都为 brief。
	code, res = runUnits(t, dir, "status")
	if code != 0 {
		t.Fatalf("status: exit code = %d", code)
	}
	view := map[string]map[string]any{}
	for _, u := range res.Units {
		view[u["unit"].(string)] = u
	}
	if ch := view["my-novel/chapter-two"]; ch["stage"] != "brief" || ch["progress"].(map[string]any)["done"] != float64(1) {
		t.Fatalf("chapter-two = %v", ch)
	}
	// advance 汇总单元被拒绝。
	if code, res := runUnits(t, dir, "advance", "my-novel/chapter-two"); code != 1 || diag(t, res.result, "lifecycle.derived-stage").Code == "" {
		t.Fatalf("advance chapter-two: exit code = %d", code)
	}
	// 其余场景全部推进到 done：章一、章二与部一推导为 done，next 不再列出它们。
	for _, scene := range []string{"scene-2", "scene-3"} {
		completeScene(t, dir, scene)
	}
	code, res = runUnits(t, dir, "next")
	if code != 0 {
		t.Fatalf("next: exit code = %d", code)
	}
	for _, s := range res.Steps {
		if u := s["unit"].(string); u == "my-novel/chapter-one" || u == "my-novel/chapter-two" || u == "my-novel/part-one" {
			t.Fatalf("done aggregated unit still listed: %v", s)
		}
	}
	code, res = runUnits(t, dir, "status")
	view = map[string]map[string]any{}
	for _, u := range res.Units {
		view[u["unit"].(string)] = u
	}
	for _, u := range []string{"my-novel/chapter-one", "my-novel/chapter-two", "my-novel/part-one"} {
		if view[u]["stage"] != "done" {
			t.Fatalf("%s stage = %v, want done", u, view[u]["stage"])
		}
	}
	// validate 全绿。
	if code, _ := runWS(t, dir, "validate"); code != 0 {
		t.Fatalf("validate: exit code = %d", code)
	}
}
