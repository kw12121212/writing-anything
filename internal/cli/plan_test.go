package cli

import (
	"encoding/json"
	"os"
	"path/filepath"
	"strings"
	"testing"
)

// planStepOut 是 plan 命令输出中的一个步骤。
type planStepOut struct {
	ID                string   `json:"id"`
	Title             string   `json:"title"`
	Product           *string  `json:"product"`
	Status            string   `json:"status"`
	Text              string   `json:"text"`
	Sources           []string `json:"sources"`
	Links             []string `json:"links"`
	ConfirmedByAuthor bool     `json:"confirmedByAuthor"`
	Confirmation      *string  `json:"confirmation"`
	ConfirmedAt       *string  `json:"confirmedAt"`
}

// planOut 是 plan 命令的 --json 输出。
type planOut struct {
	result
	Plan *struct {
		Method string         `json:"method"`
		Source string         `json:"source"`
		Path   *string        `json:"path"`
		Steps  []planStepOut  `json:"steps"`
	} `json:"plan"`
	CurrentStep *string `json:"currentStep"`
}

func runPlan(t *testing.T, dir string, args ...string) (int, planOut) {
	t.Helper()
	code, res := runWS(t, dir, args...)
	var out planOut
	if err := json.Unmarshal([]byte(res.stdout), &out); err != nil {
		t.Fatal(err)
	}
	out.result = res
	return code, out
}

func stepOf(t *testing.T, r planOut, id string) planStepOut {
	t.Helper()
	for _, s := range r.Plan.Steps {
		if s.ID == id {
			return s
		}
	}
	t.Fatalf("no step %s in %+v", id, r.Plan.Steps)
	return planStepOut{}
}

// canonOut 是 canon list 的 --json 输出（这里只用条目 id）。
type canonOut struct {
	Entries []struct {
		ID   string `json:"id"`
		Type string `json:"type"`
	} `json:"entries"`
}

func canonIDs(t *testing.T, dir, work string) []string {
	t.Helper()
	_, res := runWS(t, dir, "canon", "list", work)
	var out canonOut
	if err := json.Unmarshal([]byte(res.stdout), &out); err != nil {
		t.Fatal(err)
	}
	var ids []string
	for _, e := range out.Entries {
		ids = append(ids, e.ID)
	}
	return ids
}

// newNovelPlan 初始化 novel 工作区并创建作品 my-novel（文体默认写作法为 snowflake）。
func newNovelPlan(t *testing.T) string {
	t.Helper()
	dir := newWorkspace(t, "--genre", "novel")
	mustRunWS(t, dir, "new", "novel", "my-novel")
	return dir
}

func containsPath(paths []string, want string) bool {
	for _, p := range paths {
		if p == want {
			return true
		}
	}
	return false
}

// 新建的 novel 作品：plan show 列出 snowflake 十步，全部 pending，currentStep 为 one-sentence
//（show-fresh 与 novel-default-method）。
func TestPlanShowFresh(t *testing.T) {
	dir := newNovelPlan(t)
	code, r := runPlan(t, dir, "plan", "show", "my-novel")
	if code != 0 || len(r.Diagnostics) != 0 {
		t.Fatalf("code = %d, diagnostics = %+v", code, r.Diagnostics)
	}
	if r.Plan == nil || r.Plan.Method != "snowflake" || r.Plan.Source != "builtin" || r.Plan.Path != nil {
		t.Fatalf("plan = %+v", r.Plan)
	}
	if len(r.Plan.Steps) != 10 || r.Plan.Steps[0].ID != "one-sentence" || r.Plan.Steps[9].ID != "start-writing" {
		t.Fatalf("steps = %+v", r.Plan.Steps)
	}
	for _, s := range r.Plan.Steps {
		if s.Status != "pending" || s.Text != "" {
			t.Fatalf("step = %+v", s)
		}
	}
	if r.CurrentStep == nil || *r.CurrentStep != "one-sentence" {
		t.Fatalf("currentStep = %v", r.CurrentStep)
	}
	// 人物与场景步骤带 product 声明。
	for id, want := range map[string]string{"character-sketch": "characters", "scene-list": "scenes", "scene-details": "scene-contracts"} {
		if p := stepOf(t, r, id).Product; p == nil || *p != want {
			t.Fatalf("step %s product = %v", id, p)
		}
	}
	if p := stepOf(t, r, "one-sentence").Product; p != nil {
		t.Fatalf("one-sentence product = %v", p)
	}
}

// 未选用规划类写作法的单元：plan show 以 warning 报告 plan.not-planning，plan 与 currentStep 为 null，退出码 0
//（show-not-planning）。
func TestPlanShowNotPlanning(t *testing.T) {
	dir := newWork(t)
	code, r := runPlan(t, dir, "plan", "show", "my-post")
	if code != 0 {
		t.Fatalf("code = %d, diagnostics = %+v", code, r.Diagnostics)
	}
	d := diag(t, r.result, "plan.not-planning")
	if d.Severity != "warning" || r.Plan != nil || r.CurrentStep != nil {
		t.Fatalf("diagnostic = %+v, plan = %v", d, r.Plan)
	}
}

// 写入当前步、越序被拒、未知步骤与未知素材；取值未变时不写文件。
func TestPlanStepCommands(t *testing.T) {
	dir := newNovelPlan(t)
	// 越序：one-sentence 未确认时写后面的步骤（step-skip-blocked）。
	code, r := runPlan(t, dir, "plan", "step", "my-novel", "one-paragraph", "--text", "一段话")
	if code != 1 {
		t.Fatalf("code = %d", code)
	}
	d := diag(t, r.result, "plan.step-order")
	if !strings.Contains(d.Message, "one-sentence") || !hasNext(d, "wa-cli plan show") {
		t.Fatalf("diagnostic = %+v", d)
	}
	// 未知步骤（plan.unknown-step）。
	if code, r = runPlan(t, dir, "plan", "step", "my-novel", "nope", "--text", "x"); code != 1 {
		t.Fatalf("code = %d", code)
	} else if d := diag(t, r.result, "plan.unknown-step"); !hasNext(d, "wa-cli method show snowflake") {
		t.Fatalf("diagnostic = %+v", d)
	}
	// 未知素材。
	if code, r = runPlan(t, dir, "plan", "step", "my-novel", "one-sentence", "--text", "一句话", "--source", "src-x"); code != 1 {
		t.Fatalf("code = %d", code)
	} else if d := diag(t, r.result, "plan.unknown-source"); d.Field != "sources[0]" || !hasNext(d, "wa-cli source list") {
		t.Fatalf("diagnostic = %+v", d)
	}
	// 正常写入（step-first）。
	if code, r = runPlan(t, dir, "plan", "step", "my-novel", "one-sentence", "--text", "守灯人必须做出选择"); code != 0 || len(r.Diagnostics) != 0 {
		t.Fatalf("code = %d, diagnostics = %+v", code, r.Diagnostics)
	}
	if !containsPath(r.ChangedPaths, ".writing-anything/works/my-novel/plan.yaml") {
		t.Fatalf("changedPaths = %v", r.ChangedPaths)
	}
	if r.CurrentStep == nil || *r.CurrentStep != "one-sentence" {
		t.Fatalf("currentStep = %v", r.CurrentStep)
	}
	if code, r = runPlan(t, dir, "plan", "show", "my-novel"); code != 0 {
		t.Fatal(code)
	}
	if s := stepOf(t, r, "one-sentence"); s.Status != "drafted" || s.Text != "守灯人必须做出选择" {
		t.Fatalf("step = %+v", s)
	}
	// 取值未变时不写文件。
	if code, r = runPlan(t, dir, "plan", "step", "my-novel", "one-sentence", "--text", "守灯人必须做出选择"); code != 0 || len(r.ChangedPaths) != 0 {
		t.Fatalf("code = %d, changedPaths = %v", code, r.ChangedPaths)
	}
}

// 确认命令：缺少确认参数、确认推进、重确认被拒与下一步未起草。
func TestPlanConfirmCommands(t *testing.T) {
	dir := newNovelPlan(t)
	// 还没有规划时确认当前步（confirm-not-drafted）。
	code, r := runPlan(t, dir, "plan", "confirm", "my-novel", "one-sentence", "--confirmed-by-author", "--confirmation", "作者：好")
	if code != 1 {
		t.Fatalf("code = %d", code)
	}
	if d := diag(t, r.result, "plan.not-drafted"); !hasNext(d, "wa-cli plan show") {
		t.Fatalf("diagnostic = %+v", d)
	}
	mustRunWS(t, dir, "plan", "step", "my-novel", "one-sentence", "--text", "一句话")
	// 缺 --confirmed-by-author（confirm-incomplete）。
	if code, r = runPlan(t, dir, "plan", "confirm", "my-novel", "one-sentence", "--confirmation", "作者：好"); code != 1 {
		t.Fatalf("code = %d", code)
	} else if d := diag(t, r.result, "plan.incomplete-confirmation"); !hasNext(d, "wa-cli help plan confirm") {
		t.Fatalf("diagnostic = %+v", d)
	}
	// 确认成功（confirm-advance）。
	if code, r = runPlan(t, dir, "plan", "confirm", "my-novel", "one-sentence", "--confirmed-by-author", "--confirmation", "作者：这句话就是这本书"); code != 0 || len(r.Diagnostics) != 0 {
		t.Fatalf("code = %d, diagnostics = %+v", code, r.Diagnostics)
	}
	if r.CurrentStep == nil || *r.CurrentStep != "one-paragraph" || !containsPath(r.ChangedPaths, ".writing-anything/works/my-novel/plan.yaml") {
		t.Fatalf("currentStep = %v, changedPaths = %v", r.CurrentStep, r.ChangedPaths)
	}
	// 重确认被拒（plan.step-order）。
	if code, r = runPlan(t, dir, "plan", "confirm", "my-novel", "one-sentence", "--confirmed-by-author", "--confirmation", "再来"); code != 1 {
		t.Fatalf("code = %d", code)
	} else if d := diag(t, r.result, "plan.step-order"); !strings.Contains(d.Message, "one-paragraph") {
		t.Fatalf("diagnostic = %+v", d)
	}
	// 确认记录写入 plan.yaml。
	data, err := os.ReadFile(filepath.Join(dir, ".writing-anything", "works", "my-novel", "plan.yaml"))
	if err != nil {
		t.Fatal(err)
	}
	for _, want := range []string{"confirmed_by_author: true", "confirmation: 作者：这句话就是这本书", "confirmed_at: \""} {
		if !strings.Contains(string(data), want) {
			t.Fatalf("plan.yaml missing %q:\n%s", want, data)
		}
	}
}

// 人物步骤：先 canon add 写人物卡再 --link 关联；链接不存在被拒；确认时要求链接（plan.missing-links）。
func TestPlanCharacterStepCommands(t *testing.T) {
	dir := newNovelPlan(t)
	mustRunWS(t, dir, "plan", "step", "my-novel", "one-sentence", "--text", "一句话")
	mustRunWS(t, dir, "plan", "confirm", "my-novel", "one-sentence", "--confirmed-by-author", "--confirmation", "作者：好")
	mustRunWS(t, dir, "plan", "step", "my-novel", "one-paragraph", "--text", "一段话")
	mustRunWS(t, dir, "plan", "confirm", "my-novel", "one-paragraph", "--confirmed-by-author", "--confirmation", "作者：好")
	// 链接指向不存在的条目（step-link-unknown）。
	code, r := runPlan(t, dir, "plan", "step", "my-novel", "character-sketch", "--text", "人物概要", "--link", "c9")
	if code != 1 {
		t.Fatalf("code = %d", code)
	}
	if d := diag(t, r.result, "plan.unknown-link"); d.Path != "" || d.Field != "links[0]" || !hasNext(d, "wa-cli help plan step") {
		t.Fatalf("diagnostic = %+v", d)
	}
	// 先落地人物卡再关联（step-link-character）。
	mustRunWS(t, dir, "canon", "add", "my-novel", "--type", "character", "--name", "守灯人", "--text", "外冷内热")
	if code, r = runPlan(t, dir, "plan", "step", "my-novel", "character-sketch", "--text", "人物概要", "--link", "canon-1"); code != 0 {
		t.Fatalf("code = %d, diagnostics = %+v", code, r.Diagnostics)
	}
	// 清空链接后确认被拒（confirm-missing-links）。
	if code, r = runPlan(t, dir, "plan", "step", "my-novel", "character-sketch", "--text", "人物概要", "--link", ""); code != 0 {
		t.Fatalf("clear links: %d %+v", code, r.Diagnostics)
	}
	if code, r = runPlan(t, dir, "plan", "confirm", "my-novel", "character-sketch", "--confirmed-by-author", "--confirmation", "作者：好"); code != 1 {
		t.Fatalf("code = %d", code)
	} else if d := diag(t, r.result, "plan.missing-links"); d.Field != "links" || !strings.Contains(d.Message, "canon add") {
		t.Fatalf("diagnostic = %+v", d)
	}
	// 重新关联后确认成功。
	mustRunWS(t, dir, "plan", "step", "my-novel", "character-sketch", "--text", "人物概要", "--link", "canon-1")
	mustRunWS(t, dir, "plan", "confirm", "my-novel", "character-sketch", "--confirmed-by-author", "--confirmation", "作者：好")
}

// 非规划写作法的单元上执行 plan step 报告 plan.not-planning（step-not-planning），不创建 plan.yaml。
func TestPlanStepNotPlanning(t *testing.T) {
	dir := newWork(t)
	code, r := runPlan(t, dir, "plan", "step", "my-post", "one-sentence", "--text", "一句话")
	if code != 1 {
		t.Fatalf("code = %d", code)
	}
	if d := diag(t, r.result, "plan.not-planning"); d.Severity != "error" || !hasNext(d, "wa-cli method list") {
		t.Fatalf("diagnostic = %+v", d)
	}
	if _, err := os.Stat(filepath.Join(dir, ".writing-anything", "works", "my-post", "plan.yaml")); !os.IsNotExist(err) {
		t.Fatalf("plan.yaml was created")
	}
}

// --redo：重置已确认的步骤并删除其后记录；对未确认的步骤报告 plan.not-confirmed；缺少确认参数报告
// plan.incomplete-confirmation（step-redo、step-redo-unconfirmed）。
func TestPlanStepRedo(t *testing.T) {
	dir := newNovelPlan(t)
	for _, s := range []string{"one-sentence", "one-paragraph"} {
		mustRunWS(t, dir, "plan", "step", "my-novel", s, "--text", s)
		mustRunWS(t, dir, "plan", "confirm", "my-novel", s, "--confirmed-by-author", "--confirmation", "作者：好")
	}
	mustRunWS(t, dir, "plan", "step", "my-novel", "character-sketch", "--text", "人物概要")
	// 未确认的步骤给出 --redo。
	code, r := runPlan(t, dir, "plan", "step", "my-novel", "character-sketch", "--text", "重写", "--redo", "--confirmed-by-author", "--confirmation", "作者：改")
	if code != 1 {
		t.Fatalf("code = %d", code)
	}
	if d := diag(t, r.result, "plan.not-confirmed"); !hasNext(d, "wa-cli help plan step") {
		t.Fatalf("diagnostic = %+v", d)
	}
	// --redo 缺确认参数。
	if code, r = runPlan(t, dir, "plan", "step", "my-novel", "one-sentence", "--text", "重写", "--redo", "--confirmed-by-author"); code != 1 {
		t.Fatalf("code = %d", code)
	} else if d := diag(t, r.result, "plan.incomplete-confirmation"); d.Field != "confirmation" {
		t.Fatalf("diagnostic = %+v", d)
	}
	// 改写已确认的步骤不给出 --redo 时被拒（step-confirmed-immutable）。
	if code, r = runPlan(t, dir, "plan", "step", "my-novel", "one-sentence", "--text", "重写"); code != 1 {
		t.Fatalf("code = %d", code)
	} else if diag(t, r.result, "plan.step-order").Code != "plan.step-order" {
		t.Fatalf("diagnostics = %+v", r.Diagnostics)
	}
	// 重置 one-sentence（step-redo）。
	if code, r = runPlan(t, dir, "plan", "step", "my-novel", "one-sentence", "--text", "新的一句话", "--redo", "--confirmed-by-author", "--confirmation", "作者：前提要改"); code != 0 || len(r.Diagnostics) != 0 {
		t.Fatalf("code = %d, diagnostics = %+v", code, r.Diagnostics)
	}
	if r.CurrentStep == nil || *r.CurrentStep != "one-sentence" || !containsPath(r.ChangedPaths, ".writing-anything/works/my-novel/plan.yaml") {
		t.Fatalf("currentStep = %v, changedPaths = %v", r.CurrentStep, r.ChangedPaths)
	}
	if code, r = runPlan(t, dir, "plan", "show", "my-novel"); code != 0 {
		t.Fatal(code)
	}
	if s := stepOf(t, r, "one-sentence"); s.Status != "drafted" || s.ConfirmedByAuthor || s.Text != "新的一句话" {
		t.Fatalf("redone step = %+v", s)
	}
	if s := stepOf(t, r, "one-paragraph"); s.Status != "pending" {
		t.Fatalf("later step = %+v", s)
	}
	data, err := os.ReadFile(filepath.Join(dir, ".writing-anything", "works", "my-novel", "plan.yaml"))
	if err != nil {
		t.Fatal(err)
	}
	if strings.Count(string(data), "- id:") != 1 {
		t.Fatalf("plan.yaml after redo:\n%s", data)
	}
}

// 整体校验与单产物校验检查 plan.yaml：顺序违反、悬空素材与确认记录缺失（plan-artifact）。
func TestPlanValidate(t *testing.T) {
	dir := newNovelPlan(t)
	planPath := filepath.Join(dir, ".writing-anything", "works", "my-novel", "plan.yaml")
	// 步骤记录不是前缀（validate-order-violation）。
	writeFile(t, planPath, "schema_version: 1\nmethod: snowflake\nsteps:\n  - id: one-paragraph\n    title: 一段话概括\n    text: x\n    confirmed_by_author: false\n")
	code, r := runWS(t, dir, "validate")
	if code != 1 {
		t.Fatalf("code = %d", code)
	}
	if d := diag(t, r, "plan.step-order"); d.Field != "steps[0]" || !containsPath(r.Checked, ".writing-anything/works/my-novel/plan.yaml") {
		t.Fatalf("diagnostic = %+v, checked = %v", d, r.Checked)
	}
	// 单产物校验。
	if code, r = runWS(t, dir, "validate", ".writing-anything/works/my-novel/plan.yaml"); code != 1 {
		t.Fatalf("code = %d", code)
	} else if diag(t, r, "plan.step-order").Field != "steps[0]" {
		t.Fatalf("diagnostics = %+v", r.Diagnostics)
	}
	// 悬空素材（validate-unknown-source）。
	writeFile(t, planPath, "schema_version: 1\nmethod: snowflake\nsteps:\n  - id: one-sentence\n    title: 一句话概括\n    text: x\n    sources: [src-x]\n    confirmed_by_author: false\n")
	if code, r = runWS(t, dir, "validate"); code != 1 {
		t.Fatalf("code = %d", code)
	} else if d := diag(t, r, "plan.unknown-source"); d.Field != "steps[0].sources[0]" {
		t.Fatalf("diagnostic = %+v", d)
	}
	// 确认的步骤缺确认记录：领域校验报告 plan.incomplete-confirmation（field 为该步骤）。
	writeFile(t, planPath, "schema_version: 1\nmethod: snowflake\nsteps:\n  - id: one-sentence\n    title: 一句话概括\n    text: x\n    confirmed_by_author: true\n")
	if code, r = runWS(t, dir, "validate", ".writing-anything/works/my-novel/plan.yaml"); code != 1 {
		t.Fatalf("code = %d", code)
	} else if d := diag(t, r, "plan.incomplete-confirmation"); d.Field != "steps[0]" {
		t.Fatalf("diagnostics = %+v", r.Diagnostics)
	}
	// 未确认的步骤写 confirmation: null（「未确认为 null」）是合法产物。
	writeFile(t, planPath, "schema_version: 1\nmethod: snowflake\nsteps:\n  - id: one-sentence\n    title: 一句话概括\n    text: x\n    confirmed_by_author: false\n    confirmation: null\n    confirmed_at: null\n")
	if code, r = runWS(t, dir, "validate", ".writing-anything/works/my-novel/plan.yaml"); code != 0 || len(r.Diagnostics) != 0 {
		t.Fatalf("code = %d, diagnostics = %+v", code, r.Diagnostics)
	}
	// 悬空的人物链接（validate-dangling-link）：character-sketch 的步骤对位时按 product 校验。
	mustRunWS(t, dir, "canon", "add", "my-novel", "--type", "character", "--name", "守灯人", "--text", "外冷内热")
	writeFile(t, planPath, "schema_version: 1\nmethod: snowflake\nsteps:\n  - id: one-sentence\n    title: 一句话概括\n    text: x\n    confirmed_by_author: false\n  - id: one-paragraph\n    title: 一段话概括\n    text: x\n    confirmed_by_author: false\n  - id: character-sketch\n    title: 人物概要\n    text: x\n    links: [canon-9]\n    confirmed_by_author: false\n")
	if code, r = runWS(t, dir, "validate"); code != 1 {
		t.Fatalf("code = %d", code)
	} else if d := diag(t, r, "plan.unknown-link"); d.Field != "steps[2].links[0]" {
		t.Fatalf("diagnostics = %+v", r.Diagnostics)
	}
}

// 参数问题：--text 只含空白与 --link 重复取值都报告 input.invalid-value，field 为参数名。
func TestPlanStepParamValues(t *testing.T) {
	dir := newNovelPlan(t)
	code, r := runPlan(t, dir, "plan", "step", "my-novel", "one-sentence", "--text", "   ")
	if code != 1 {
		t.Fatalf("code = %d", code)
	}
	if d := diag(t, r.result, "input.invalid-value"); d.Field != "text" {
		t.Fatalf("diagnostic = %+v", d)
	}
	if code, r = runPlan(t, dir, "plan", "step", "my-novel", "one-sentence", "--text", "一句话", "--link", "canon-1,canon-1"); code != 1 {
		t.Fatalf("code = %d", code)
	} else if d := diag(t, r.result, "input.invalid-value"); d.Field != "link" {
		t.Fatalf("diagnostic = %+v", d)
	}
	if _, err := os.Stat(filepath.Join(dir, ".writing-anything", "works", "my-novel", "plan.yaml")); !os.IsNotExist(err) {
		t.Fatalf("plan.yaml was created")
	}
}

// 规范包规划写作法的非法 product 由整体校验报告 artifact.schema-violation（pack-product-invalid）。
func TestPackProductInvalid(t *testing.T) {
	dir := newWorkspace(t)
	writePack(t, dir, "house", `schema_version: 1
id: house
title: 团队规范
methods:
  - id: outline-first
    title: 先大纲后动笔
    kind: planning
    steps:
      - {id: cast, title: 人物表, product: robots}
`)
	mustRunWS(t, dir, "config", "set", "packs", "house")
	code, r := runWS(t, dir, "validate")
	if code != 1 {
		t.Fatalf("code = %d", code)
	}
	// $ref 内的枚举失败会把兄弟字段一并标为未评估（draft 2020-12 的求值语义），诊断按 field 定位。
	var found *Diagnostic
	for i, d := range r.Diagnostics {
		if d.Code == "artifact.schema-violation" && d.Field == "methods[0].steps[0].product" && d.Path == ".writing-anything/packs/house.yaml" {
			found = &r.Diagnostics[i]
		}
	}
	if found == nil {
		t.Fatalf("diagnostics = %+v", r.Diagnostics)
	}
}

// e2e：novel 作品从创建到开始写作的雪花十步推进——人物三步关联 canon add 的人物卡，场景列表关联 unit add
// 登记的场景单元，场景详述关联 scene contract 写入的场景契约；全部确认后 currentStep 为 null，validate 通过。
func TestNovelSnowflakeE2E(t *testing.T) {
	dir := newNovelPlan(t)
	// 一句话与一段话：素材关联作者笔记。
	writeFile(t, filepath.Join(dir, "idea.md"), "作者的设定笔记")
	mustRunWS(t, dir, "source", "add", "--type", "author-note", "--file", "idea.md", "--title", "设定", "--id", "idea")
	mustRunWS(t, dir, "plan", "step", "my-novel", "one-sentence", "--text", "守灯人在灯塔与精灵共生百年后必须做出选择", "--source", "idea")
	mustRunWS(t, dir, "plan", "confirm", "my-novel", "one-sentence", "--confirmed-by-author", "--confirmation", "作者：这句话就是这本书")
	mustRunWS(t, dir, "plan", "step", "my-novel", "one-paragraph", "--text", "破裂的一夜与百年共生的代价")
	mustRunWS(t, dir, "plan", "confirm", "my-novel", "one-paragraph", "--confirmed-by-author", "--confirmation", "作者：好")
	// 人物概要 → 一页纲要 → 人物详述 → 四页纲要 → 人物宝典：人物步骤先 canon add 人物卡再 --link 最新条目。
	for _, step := range []string{"character-sketch", "one-page-synopsis", "character-details", "four-page-synopsis", "character-bible"} {
		args := []string{"plan", "step", "my-novel", step, "--text", step}
		if strings.Contains(step, "character") {
			mustRunWS(t, dir, "canon", "add", "my-novel", "--type", "character", "--name", "守灯人", "--text", step)
			ids := canonIDs(t, dir, "my-novel")
			args = append(args, "--link", ids[len(ids)-1])
		}
		mustRunWS(t, dir, append(args, "--dir", dir)...)
		mustRunWS(t, dir, "plan", "confirm", "my-novel", step, "--confirmed-by-author", "--confirmation", "作者：好")
	}
	// 场景列表：部 → 章 → 场景（confirm-scene-list）。
	mustRunWS(t, dir, "unit", "add", "my-novel", "part", "part-one")
	mustRunWS(t, dir, "unit", "add", "my-novel/part-one", "chapter", "chapter-one")
	mustRunWS(t, dir, "unit", "add", "my-novel/chapter-one", "scene", "scene-1")
	code, r := runPlan(t, dir, "plan", "step", "my-novel", "scene-list", "--text", "场景表", "--link", "my-novel/scene-1")
	if code != 0 || len(r.Diagnostics) != 0 {
		t.Fatalf("code = %d, diagnostics = %+v", code, r.Diagnostics)
	}
	mustRunWS(t, dir, "plan", "confirm", "my-novel", "scene-list", "--confirmed-by-author", "--confirmation", "作者：场景表可以")
	// 场景详述：scene contract 写入场景契约后关联 <场景单元引用>:<大纲节 id>。
	mustRunWS(t, dir, "scene", "contract", "my-novel/scene-1", "sc1", "--title", "灯下对峙", "--pov", "守灯人", "--reveal", "精灵的真实身份")
	mustRunWS(t, dir, "outline", "set", "my-novel/scene-1", "sc1", "--reason", "按场景契约推进灯下对峙")
	if code, r = runPlan(t, dir, "plan", "step", "my-novel", "scene-details", "--text", "场景详述", "--link", "my-novel/scene-1:sc1"); code != 0 || len(r.Diagnostics) != 0 {
		t.Fatalf("code = %d, diagnostics = %+v", code, r.Diagnostics)
	}
	mustRunWS(t, dir, "plan", "confirm", "my-novel", "scene-details", "--confirmed-by-author", "--confirmation", "作者：好")
	// 开始写作：全部确认后 currentStep 为 null。
	mustRunWS(t, dir, "plan", "step", "my-novel", "start-writing", "--text", "按场景契约逐场景推进")
	code, r = runPlan(t, dir, "plan", "confirm", "my-novel", "start-writing", "--confirmed-by-author", "--confirmation", "作者：开始吧")
	if code != 0 || len(r.Diagnostics) != 0 {
		t.Fatalf("code = %d, diagnostics = %+v", code, r.Diagnostics)
	}
	if r.CurrentStep != nil {
		t.Fatalf("currentStep = %v", r.CurrentStep)
	}
	// 规划产物落位：人物卡在设定集，场景在单元树，契约在场景大纲；工作区校验通过。
	if code, vres := runWS(t, dir, "validate"); code != 0 {
		t.Fatalf("code = %d, diagnostics = %+v", code, vres.Diagnostics)
	}
	if ids := canonIDs(t, dir, "my-novel"); len(ids) != 3 {
		t.Fatalf("canon entries = %v", ids)
	}
}

// 网文结构模板：部与章单元经写作简报选用 volume-structure 与 golden-three 后，大纲关卡报告写作法缺口。
func TestWebStructureMethods(t *testing.T) {
	dir := newNovelPlan(t)
	mustRunWS(t, dir, "unit", "add", "my-novel", "part", "part-one")
	mustRunWS(t, dir, "unit", "add", "my-novel/part-one", "chapter", "chapter-one")
	mustRunWS(t, dir, "brief", "set", "my-novel/part-one", "--method", "volume-structure")
	mustRunWS(t, dir, "brief", "set", "my-novel/chapter-one", "--method", "golden-three")
	// 部的大纲只有一节，三个节拍都无人承担。
	writeFile(t, filepath.Join(dir, ".writing-anything", "works", "my-novel", "units", "part-one", "outline.yaml"),
		"schema_version: 1\nsections:\n  - id: opening\n    title: 开卷\n    reason: 交代本卷目标\n")
	var og outlineGapsResult
	code, res := runWS(t, dir, "outline", "show", "my-novel/part-one")
	if code != 0 {
		t.Fatalf("code = %d, diagnostics = %+v", code, res.Diagnostics)
	}
	if err := json.Unmarshal([]byte(res.stdout), &og); err != nil {
		t.Fatal(err)
	}
	if len(og.Gaps) != 3 {
		t.Fatalf("gaps = %v", og.Gaps)
	}
	for i, want := range []string{"goal", "climax", "cliffhanger"} {
		if g := og.Gaps[i]; !strings.Contains(g["message"].(string), want) || g["code"] != "outline.missing-method-part" {
			t.Fatalf("gap %d = %+v", i, g)
		}
	}
	// 章：黄金三章的缺口。
	og = outlineGapsResult{}
	if code, res = runWS(t, dir, "outline", "show", "my-novel/chapter-one"); code != 0 {
		t.Fatalf("code = %d, diagnostics = %+v", code, res.Diagnostics)
	}
	if err := json.Unmarshal([]byte(res.stdout), &og); err != nil {
		t.Fatal(err)
	}
	if len(og.Gaps) != 3 {
		t.Fatalf("chapter gaps = %v", og.Gaps)
	}
	for i, want := range []string{"hook", "protagonist", "core-conflict"} {
		if g := og.Gaps[i]; !strings.Contains(g["message"].(string), want) {
			t.Fatalf("gap %d = %+v", i, g)
		}
	}
}

// outlineGapsResult 是 outline show 的 --json 输出（网文结构模板测试只用 gaps）。
type outlineGapsResult struct {
	Gaps []map[string]any `json:"gaps"`
}
