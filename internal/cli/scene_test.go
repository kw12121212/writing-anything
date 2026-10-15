package cli

import (
	"os"
	"path/filepath"
	"slices"
	"strings"
	"testing"
)

// mkdir 创建目录（含父目录）。
func mkdir(t *testing.T, path string) {
	t.Helper()
	if err := os.MkdirAll(path, 0o755); err != nil {
		t.Fatal(err)
	}
}

func sceneOutlinePath(dir, scene string) string {
	return filepath.Join(dir, ".writing-anything", "works", "my-novel", "units", scene, "outline.yaml")
}

func sceneRel(scene string) string {
	return ".writing-anything/works/my-novel/units/" + scene + "/outline.yaml"
}

// scene contract：命名参数写入长篇场景契约（Scene Contract）与因果链（scene-contract-set、scene-causality）。
func TestSceneContractCommand(t *testing.T) {
	dir := newNovel(t)
	// 根单元大纲的节是可引用的情节节拍（Story Beat）。
	writeFile(t, filepath.Join(dir, ".writing-anything", "works", "my-novel", "outline.yaml"),
		"schema_version: 1\nsections:\n  - {id: b1, title: 开端, reason: 节拍}\n")

	// 伏笔先登记进伏笔台账（Foreshadowing Ledger），plants 才能引用它（threads-contract-checks）。
	if code, _ := runWS(t, dir, "threads", "add", "my-novel", "--text", "灯塔的旧账", "--plant-in", "my-novel/scene-1",
		"--recover-at", "my-novel/scene-1", "--id", "fs-1"); code != 0 {
		t.Fatalf("threads add fs-1: exit code = %d", code)
	}

	// contract-new-scene：首个场景声明完整契约。
	code, res := runOutline(t, dir, "scene", "contract", "my-novel/scene-1", "sc1", "--title", "灯下对峙",
		"--pov", "守灯人", "--characters", "守灯人,精灵", "--location", "灯室", "--beats", "b1",
		"--reveal", "精灵的真实身份", "--event", "ev-1", "--state-change", "sc-1=精灵交出灯芯", "--plant", "fs-1")
	if code != 0 || !res.Valid {
		t.Fatalf("contract scene-1: exit code = %d, result = %+v", code, res)
	}
	if !slices.Equal(res.ChangedPaths, []string{".writing-anything/works/my-novel/units/scene-1", sceneRel("scene-1")}) {
		t.Fatalf("changedPaths = %v", res.ChangedPaths)
	}
	src := readFile(t, sceneOutlinePath(dir, "scene-1"))
	for _, want := range []string{"pov: 守灯人", "location: 灯室", "characters: [守灯人, 精灵]", "beats: [b1]",
		"reveal: 精灵的真实身份", "events: [ev-1]", "id: sc-1", "text: 精灵交出灯芯", "plants: [fs-1]"} {
		if !strings.Contains(src, want) {
			t.Fatalf("scene-1 outline missing %q:\n%s", want, src)
		}
	}
	// contract-modify-keeps：只改给出的字段，未涉及的字段与注释保留。
	writeFile(t, sceneOutlinePath(dir, "scene-1"), strings.Replace(src, "pov: 守灯人", "# 视角\n    pov: 守灯人", 1))
	code, res = runOutline(t, dir, "scene", "contract", "my-novel/scene-1", "sc1", "--location", "灯室")
	if code != 0 {
		t.Fatalf("modify: exit code = %d, result = %+v", code, res)
	}
	if src = readFile(t, sceneOutlinePath(dir, "scene-1")); !strings.Contains(src, "# 视角") || !strings.Contains(src, "pov: 守灯人") {
		t.Fatalf("comment or pov lost:\n%s", src)
	}
	// 取值未变时不写文件。
	if code, res = runOutline(t, dir, "scene", "contract", "my-novel/scene-1", "sc1", "--location", "灯室"); code != 0 || len(res.ChangedPaths) != 0 {
		t.Fatalf("unchanged: exit code = %d, changed = %v", code, res.ChangedPaths)
	}

	// 因果链：scene-2 起因引用 scene-1 声明的 ev-1，并产生 ev-2；scene-3 起因引用 ev-2。
	for _, args := range [][]string{
		{"scene", "contract", "my-novel/scene-2", "sc2", "--title", "夜行", "--cause", "ev-1", "--event", "ev-2"},
		{"scene", "contract", "my-novel/scene-3", "sc3", "--title", "破晓", "--cause", "ev-2"},
	} {
		if code, res := runOutline(t, dir, args...); code != 0 {
			t.Fatalf("%v: exit code = %d, result = %+v", args, code, res)
		}
	}
	// 覆盖缺口不阻止 scene contract 写入；补上 reason 后整体校验通过（因果链合法）。
	for _, sec := range []string{"sc1", "sc2", "sc3"} {
		unit := "my-novel/scene-1"
		switch sec {
		case "sc2":
			unit = "my-novel/scene-2"
		case "sc3":
			unit = "my-novel/scene-3"
		}
		if code, res := runOutline(t, dir, "outline", "set", unit, sec, "--reason", "推进情节"); code != 0 {
			t.Fatalf("reason %s: exit code = %d, result = %+v", sec, code, res)
		}
	}
	if code, res := runWS(t, dir, "validate"); code != 0 || !res.Valid {
		t.Fatalf("valid chain validate: exit code = %d, result = %+v", code, res)
	}

	// outline show 输出场景契约扩展字段（show-novel-contract）。
	code, res = runOutline(t, dir, "outline", "show", "my-novel/scene-3")
	if code != 0 {
		t.Fatalf("show: exit code = %d", code)
	}
	sec := res.Outline["sections"].([]any)[0].(map[string]any)
	if sec["pov"] != nil || sec["location"] != nil {
		t.Fatalf("unset texts must be null: %v", sec)
	}
	if !slices.Equal(toStrings(sec["causes"]), []string{"ev-2"}) || len(sec["events"].([]any)) != 0 ||
		len(sec["recovers"].([]any)) != 0 || len(sec["state_changes"].([]any)) != 0 {
		t.Fatalf("show fields = %v", sec)
	}

	// contract-clear-list：空字符串清空 causes。
	if code, res = runOutline(t, dir, "scene", "contract", "my-novel/scene-3", "sc3", "--cause="); code != 0 {
		t.Fatalf("clear: exit code = %d", code)
	} else if src = readFile(t, sceneOutlinePath(dir, "scene-3")); strings.Contains(src, "causes") {
		t.Fatalf("causes not cleared:\n%s", src)
	}
	runOutline(t, dir, "scene", "contract", "my-novel/scene-3", "sc3", "--cause", "ev-2")

	// contract-not-scene：非场景单元拒绝。
	code, res = runOutline(t, dir, "scene", "contract", "my-novel/chapter-one", "sc1", "--title", "灯下对峙")
	if code != 1 {
		t.Fatalf("not-scene: exit code = %d", code)
	}
	if d := diag(t, res.result, "scene.not-scene"); !strings.Contains(d.Message, "chapter") || d.Path != "" ||
		!slices.Contains(d.Next, "wa-cli help scene contract --json") {
		t.Fatalf("not-scene diagnostic = %+v", d)
	}

	// contract-immediate-causality：起因引用没有人声明的事件，拒绝写入且不创建文件。
	code, res = runOutline(t, dir, "scene", "contract", "my-novel/scene-2", "sc9", "--title", "x", "--cause", "ev-9")
	if code != 1 {
		t.Fatalf("unknown-event: exit code = %d", code)
	}
	if d := diag(t, res.result, "scene.unknown-event"); d.Path != "" || d.Field != "causes[0]" ||
		!slices.Contains(d.Next, "wa-cli help scene contract --json") {
		t.Fatalf("unknown-event diagnostic = %+v", d)
	}
	if src = readFile(t, sceneOutlinePath(dir, "scene-2")); strings.Contains(src, "sc9") {
		t.Fatalf("rejected contract must not add the section:\n%s", src)
	}
	// contract-self-cause：起因引用本场景自己声明的事件。
	code, res = runOutline(t, dir, "scene", "contract", "my-novel/scene-1", "sc1", "--cause", "ev-1")
	if code != 1 || diag(t, res.result, "scene.event-not-yet-happened").Code == "" {
		t.Fatalf("self-cause: exit code = %d, diagnostics = %+v", code, res.Diagnostics)
	}
	// 起因引用更靠后场景声明的事件。
	before := readFile(t, sceneOutlinePath(dir, "scene-1"))
	code, res = runOutline(t, dir, "scene", "contract", "my-novel/scene-1", "sc1", "--cause", "ev-2")
	if code != 1 {
		t.Fatalf("not-yet-happened: exit code = %d", code)
	}
	if d := diag(t, res.result, "scene.event-not-yet-happened"); !strings.Contains(d.Message, "ev-2") ||
		!strings.Contains(d.Message, "my-novel/scene-2") || readFile(t, sceneOutlinePath(dir, "scene-1")) != before {
		t.Fatalf("not-yet-happened diagnostic = %+v", d)
	}
	// 重复事件：scene-3 再次声明 ev-1，即时检查拒绝。
	code, res = runOutline(t, dir, "scene", "contract", "my-novel/scene-3", "sc3", "--event", "ev-1")
	if code != 1 || diag(t, res.result, "scene.duplicate-event").Code == "" {
		t.Fatalf("duplicate-event: exit code = %d, diagnostics = %+v", code, res.Diagnostics)
	}

	// contract-new-without-title：新增节缺少 --title。
	code, res = runOutline(t, dir, "scene", "contract", "my-novel/scene-1", "sc2", "--pov", "守灯人")
	if code != 1 {
		t.Fatalf("without title: exit code = %d", code)
	}
	if d := diag(t, res.result, "input.invalid-value"); !strings.Contains(d.Message, "--title") ||
		!concreteFirst(d, shellCommand("wa-cli", "outline", "show", "my-novel/scene-1", "--dir", dir, "--json"), "wa-cli help scene contract --json") {
		t.Fatalf("without-title diagnostic = %+v", d)
	}
	// 已存在而没有给出任何修改参数。
	code, res = runOutline(t, dir, "scene", "contract", "my-novel/scene-1", "sc1")
	if code != 1 || diag(t, res.result, "input.invalid-value").Code == "" {
		t.Fatalf("no changes: exit code = %d, diagnostics = %+v", code, res.Diagnostics)
	}
	// --state-change 的格式与空白文本。
	code, res = runOutline(t, dir, "scene", "contract", "my-novel/scene-1", "sc1", "--state-change", "sc-1")
	if code != 1 || diag(t, res.result, "input.invalid-value").Code == "" {
		t.Fatalf("bad state-change format: exit code = %d", code)
	}
	code, res = runOutline(t, dir, "scene", "contract", "my-novel/scene-1", "sc1", "--state-change", "sc-2= ")
	if code != 1 || diag(t, res.result, "input.invalid-value").Code == "" {
		t.Fatalf("blank state-change text: exit code = %d", code)
	}
}

// 因果链的校验与关卡（scene-causality）：整体校验、单产物校验报告 scene.* 错误码，大纲关卡以 outline.invalid 阻塞推进。
func TestSceneCausalityValidateAndGate(t *testing.T) {
	dir := newNovel(t)
	for _, scene := range []string{"scene-1", "scene-2", "scene-3"} {
		mkdir(t, filepath.Join(dir, ".writing-anything", "works", "my-novel", "units", scene))
	}
	writeFile(t, sceneOutlinePath(dir, "scene-1"), "schema_version: 1\nsections:\n  - {id: sc1, title: 灯下, events: [ev-1]}\n")
	writeFile(t, sceneOutlinePath(dir, "scene-2"), "schema_version: 1\nsections:\n  - {id: sc2, title: 夜行, reason: 推进情节, causes: [ev-1], events: [ev-2]}\n")
	// 重复事件与未知事件：整体校验分别报告在后声明者与引用者的 outline.yaml 上。
	writeFile(t, sceneOutlinePath(dir, "scene-3"), "schema_version: 1\nsections:\n  - {id: sc3, title: 破晓, events: [ev-1], causes: [ev-9]}\n")

	code, res := runWS(t, dir, "validate")
	if code != 1 {
		t.Fatalf("validate: exit code = %d", code)
	}
	d := diag(t, res, "scene.duplicate-event")
	if d.Path != sceneRel("scene-3") || d.Field != "sections[0].events[0]" ||
		!strings.Contains(d.Message, "my-novel/scene-1") {
		t.Fatalf("duplicate-event diagnostic = %+v", d)
	}
	if !slices.Contains(d.Next, shellCommand("wa-cli", "scene", "contract", "my-novel/scene-3", "sc3", "--dir", dir, "--json")) {
		t.Fatalf("duplicate-event next = %v", d.Next)
	}
	d = diag(t, res, "scene.unknown-event")
	if d.Path != sceneRel("scene-3") || d.Field != "sections[0].causes[0]" {
		t.Fatalf("unknown-event diagnostic = %+v", d)
	}

	// 单产物校验：只报告该文件自己的因果链问题。
	code, res = runWS(t, dir, "validate", sceneRel("scene-3"))
	if code != 1 {
		t.Fatalf("validate path: exit code = %d", code)
	}
	if diag(t, res, "scene.duplicate-event").Path != sceneRel("scene-3") ||
		diag(t, res, "scene.unknown-event").Path != sceneRel("scene-3") {
		t.Fatalf("single-path diagnostics = %+v", res.Diagnostics)
	}
	code, res = runWS(t, dir, "validate", sceneRel("scene-2"))
	if code != 0 || !res.Valid {
		t.Fatalf("valid scene outline: exit code = %d, diagnostics = %+v", code, res.Diagnostics)
	}

	// 大纲关卡：场景处于大纲阶段、起因引用不存在的事件时，advance 以 outline.invalid 拒绝。
	scene2Dir := filepath.Join(dir, ".writing-anything", "works", "my-novel", "units", "scene-2")
	writeFile(t, filepath.Join(scene2Dir, "brief.yaml"), "schema_version: 1\n")
	run(t, "advance", "my-novel/scene-2", "--dir", dir)
	writeFile(t, sceneOutlinePath(dir, "scene-2"), "schema_version: 1\nsections:\n  - {id: sc2, title: 夜行, reason: 推进情节, causes: [ev-9]}\n")
	before := readFile(t, workFile(dir, "my-novel"))
	code, ures := runUnits(t, dir, "advance", "my-novel/scene-2")
	if code != 1 {
		t.Fatalf("advance blocked: exit code = %d", code)
	}
	if d := diag(t, ures.result, "outline.invalid"); d.Path != sceneRel("scene-2") ||
		!slices.Contains(d.Next, shellCommand("wa-cli", "validate", sceneRel("scene-2"), "--dir", dir, "--json")) {
		t.Fatalf("outline.invalid diagnostic = %+v", d)
	}
	if readFile(t, workFile(dir, "my-novel")) != before {
		t.Fatal("work.yaml changed after rejected advance")
	}
}

// short-story 的场景同样可以用 scene contract 写入轻量契约之上的扩展字段。
func TestSceneContractShortStory(t *testing.T) {
	dir := newWorkspace(t)
	run(t, "new", "short-story", "my-story", "--dir", dir)
	run(t, "unit", "add", "my-story", "scene", "s1", "--dir", dir)
	code, res := runOutline(t, dir, "scene", "contract", "my-story/s1", "scene-1", "--title", "灯下对峙",
		"--pov", "守灯人", "--location", "灯室", "--cause", "ev-x")
	if code != 1 || diag(t, res.result, "scene.unknown-event").Code == "" {
		t.Fatalf("short-story unknown cause: exit code = %d, diagnostics = %+v", code, res.Diagnostics)
	}
	code, res = runOutline(t, dir, "scene", "contract", "my-story/s1", "scene-1", "--title", "灯下对峙", "--pov", "守灯人")
	if code != 0 || !res.Valid {
		t.Fatalf("short-story contract: exit code = %d, result = %+v", code, res)
	}
}

// --state-change 与该节既有条目按 id 合并：新增保留其他条目、更新替换同 id 文本，不整体替换。
func TestSceneContractStateChangeMerge(t *testing.T) {
	dir := newNovel(t)
	runOutline(t, dir, "scene", "contract", "my-novel/scene-1", "sc1", "--title", "灯下", "--state-change", "sc-1=第一条")
	if code, res := runOutline(t, dir, "scene", "contract", "my-novel/scene-1", "sc1", "--state-change", "sc-2=第二条"); code != 0 {
		t.Fatalf("add sc-2: exit code = %d, result = %+v", code, res)
	}
	src := readFile(t, sceneOutlinePath(dir, "scene-1"))
	if !strings.Contains(src, "text: 第一条") || !strings.Contains(src, "text: 第二条") {
		t.Fatalf("existing state change lost:\n%s", src)
	}
	if code, res := runOutline(t, dir, "scene", "contract", "my-novel/scene-1", "sc1", "--state-change", "sc-1=改写"); code != 0 {
		t.Fatalf("update sc-1: exit code = %d, result = %+v", code, res)
	}
	src = readFile(t, sceneOutlinePath(dir, "scene-1"))
	if !strings.Contains(src, "text: 改写") || strings.Contains(src, "第一条") || !strings.Contains(src, "text: 第二条") {
		t.Fatalf("update must replace same id and keep others:\n%s", src)
	}
}

// 即时因果链检查只拦截被编辑节上的本次问题：其他节的既有问题与其他场景的重复不阻止写入（cross-artifact-references）。
func TestSceneContractPrecheckScope(t *testing.T) {
	dir := newNovel(t)
	for _, scene := range []string{"scene-1", "scene-2"} {
		mkdir(t, filepath.Join(dir, ".writing-anything", "works", "my-novel", "units", scene))
	}
	// s1 的节 a 已有悬空起因（既有问题）；编辑另一个节 b 声明事件，照常写入。
	writeFile(t, sceneOutlinePath(dir, "scene-1"), "schema_version: 1\nsections:\n  - {id: a, title: 灯下, causes: [ev-9]}\n  - {id: b, title: 夜行}\n")
	if code, res := runOutline(t, dir, "scene", "contract", "my-novel/scene-1", "b", "--event", "ev-5"); code != 0 {
		t.Fatalf("unrelated section edit: exit code = %d, result = %+v", code, res)
	}
	// s2 已声明 ev-1；s1 作为先声明者写入 ev-1 不被拒，重复由整体校验报告在 s2 上。
	writeFile(t, sceneOutlinePath(dir, "scene-2"), "schema_version: 1\nsections:\n  - {id: a, title: 夜行, events: [ev-1]}\n")
	writeFile(t, sceneOutlinePath(dir, "scene-1"), "schema_version: 1\nsections:\n  - {id: a, title: 灯下}\n")
	if code, res := runOutline(t, dir, "scene", "contract", "my-novel/scene-1", "a", "--event", "ev-1"); code != 0 {
		t.Fatalf("first declarer write: exit code = %d, result = %+v", code, res)
	}
	code, res := runWS(t, dir, "validate")
	if code != 1 {
		t.Fatalf("validate: exit code = %d", code)
	}
	if d := diag(t, res, "scene.duplicate-event"); d.Path != sceneRel("scene-2") {
		t.Fatalf("duplicate-event diagnostic = %+v", d)
	}
}

// TestSceneContractAnchoredNext：场景大纲经由 YAML 锚点得到的取值无法只改一处，next 先给出该场景的大纲（cli/diagnostic-next-concrete）。
func TestSceneContractAnchoredNext(t *testing.T) {
	dir := newNovel(t)
	rel := ".writing-anything/works/my-novel/units/scene-1/outline.yaml"
	if err := os.MkdirAll(filepath.Dir(wsPath(dir, rel)), 0o755); err != nil {
		t.Fatal(err)
	}
	writeFile(t, wsPath(dir, rel), "schema_version: 1\nsections:\n  - &s {id: sc1, title: 灯下对峙, reason: r}\n")
	code, res := runOutline(t, dir, "scene", "contract", "my-novel/scene-1", "sc1", "--pov", "守灯人")
	if code != 1 || !concreteFirst(diag(t, res.result, "outline.anchored-value"),
		shellCommand("wa-cli", "outline", "show", "my-novel/scene-1", "--dir", dir, "--json"), "wa-cli help scene contract --json") {
		t.Fatalf("exit code = %d, diagnostics = %+v", code, res.Diagnostics)
	}
}
