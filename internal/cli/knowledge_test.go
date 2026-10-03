package cli

import (
	"encoding/json"
	"os"
	"path/filepath"
	"slices"
	"strings"
	"testing"
)

type knowledgeEvent struct {
	ID           string  `json:"id"`
	Scene        string  `json:"scene"`
	Entry        string  `json:"entry"`
	Character    string  `json:"character"`
	Status       string  `json:"status"`
	Confirmation *string `json:"confirmation"`
	DecidedAt    *string `json:"decided_at"`
}

type knowledgeEventOut struct {
	Result
	Event *knowledgeEvent `json:"event"`
}

type knowledgeListOut struct {
	Result
	Events []knowledgeEvent `json:"events"`
}

type knowledgeAtOut struct {
	Result
	Knowledge []struct {
		Entry string  `json:"entry"`
		Name  *string `json:"name"`
		Event string  `json:"event"`
		Scene string  `json:"scene"`
	} `json:"knowledge"`
}

// knowledgeFixture 在 novel 工作区登记设定条目 canon-1（守灯人）与 canon-2（世界规则），
// 并提交知情事件 know-1（守灯人于 scene-1 获知 canon-2）。
func knowledgeFixture(t *testing.T) string {
	t.Helper()
	dir := newNovel(t)
	for _, args := range [][]string{
		{"canon", "add", "my-novel", "--type", "character", "--name", "守灯人", "--text", "五十岁，左手有旧伤"},
		{"canon", "add", "my-novel", "--type", "world-rule", "--text", "灯油只能燃烧到黎明"},
		{"knowledge", "propose", "my-novel", "--scene", "my-novel/scene-1", "--entry", "canon-2", "--character", "canon-1"},
	} {
		if code, res := runWS(t, dir, args...); code != 0 || !res.Valid {
			t.Fatalf("%v: exit code = %d, result = %+v", args, code, res)
		}
	}
	return dir
}

func knowledgeEventOf(t *testing.T, r result) *knowledgeEvent {
	t.Helper()
	var out knowledgeEventOut
	if err := json.Unmarshal([]byte(r.stdout), &out); err != nil {
		t.Fatal(err)
	}
	return out.Event
}

func knowledgeListOf(t *testing.T, r result) []knowledgeEvent {
	t.Helper()
	var out knowledgeListOut
	if err := json.Unmarshal([]byte(r.stdout), &out); err != nil {
		t.Fatal(err)
	}
	return out.Events
}

// knowledge propose 与 knowledge list：--id 缺省生成、--character reader、默认只列 proposed、--all 列出全部、
// 无台账为空、退出码与人读输出。
func TestKnowledgeProposeAndList(t *testing.T) {
	dir := knowledgeFixture(t)

	code, res := runWS(t, dir, "knowledge", "propose", "my-novel", "--scene", "my-novel/scene-2", "--entry", "canon-2", "--character", "reader")
	if code != 0 || !res.Valid {
		t.Fatalf("propose: exit code = %d, result = %+v", code, res)
	}
	if e := knowledgeEventOf(t, res); e.ID != "know-2" || e.Character != "reader" || e.Status != "proposed" || e.Confirmation != nil || e.DecidedAt != nil {
		t.Fatalf("event = %+v", e)
	}
	if !slices.Equal(res.ChangedPaths, []string{".writing-anything/works/my-novel/knowledge.yaml"}) {
		t.Fatalf("changedPaths = %v", res.ChangedPaths)
	}

	code, res = runWS(t, dir, "knowledge", "confirm", "my-novel", "know-2", "--confirmed-by-author", "--confirmation", "作者确认读者得知")
	if code != 0 {
		t.Fatalf("confirm: exit code = %d", code)
	}

	// 默认只列 proposed（作者待处理清单）。
	code, res = runWS(t, dir, "knowledge", "list", "my-novel")
	if code != 0 {
		t.Fatalf("list: exit code = %d", code)
	}
	events := knowledgeListOf(t, res)
	if len(events) != 1 || events[0].ID != "know-1" || events[0].Status != "proposed" {
		t.Fatalf("proposed events = %+v", events)
	}
	// --all 列出全部，按绑定场景先序排序。
	code, res = runWS(t, dir, "knowledge", "list", "my-novel", "--all")
	if code != 0 {
		t.Fatalf("list --all: exit code = %d", code)
	}
	events = knowledgeListOf(t, res)
	if len(events) != 2 || events[0].ID != "know-1" || events[1].ID != "know-2" {
		t.Fatalf("all events = %+v", events)
	}

	// 人读输出：确认条目带确认记录标注。
	code, stdout, _ := run(t, "knowledge", "list", "my-novel", "--all", "--dir", dir)
	if code != 0 || !strings.Contains(stdout, "know-1（my-novel/scene-1，canon-1 获知 canon-2）") ||
		!strings.Contains(stdout, "know-2（my-novel/scene-2，读者（reader） 获知 canon-2）［已确认］（作者：作者确认读者得知") {
		t.Fatalf("human list = %s", stdout)
	}

	// 无台账作品为空列表。
	empty := newNovel(t)
	code, res = runWS(t, empty, "knowledge", "list", "my-novel")
	if code != 0 {
		t.Fatalf("empty list: exit code = %d", code)
	}
	if events := knowledgeListOf(t, res); len(events) != 0 {
		t.Fatalf("empty ledger events = %+v", events)
	}
	// 知情表是作品级产物：子单元引用报 work.not-found。
	if code, res = runWS(t, empty, "knowledge", "list", "my-novel/scene-1"); code != 1 || diag(t, res, "work.not-found").Code == "" {
		t.Fatalf("sub-unit ref: exit code = %d, diagnostics = %+v", code, res.Diagnostics)
	} else if !strings.Contains(diag(t, res, "work.not-found").Message, "作品级") {
		t.Fatalf("message = %s", diag(t, res, "work.not-found").Message)
	}
}

// knowledge propose 的拒绝路径：--scene / --entry / --character 问题不写文件；--id 重复；next 指向 canon list。
func TestKnowledgeProposeRejects(t *testing.T) {
	dir := knowledgeFixture(t)
	ledger := filepath.Join(dir, ".writing-anything", "works", "my-novel", "knowledge.yaml")
	before := readFile(t, ledger)

	for _, tc := range []struct {
		name  string
		args  []string
		code  string
		field string
	}{
		{"unknown-unit", []string{"knowledge", "propose", "my-novel", "--scene", "my-novel/chapter-one", "--entry", "canon-2", "--character", "canon-1"}, "knowledge.unknown-unit", "scene"},
		{"unknown-entry", []string{"knowledge", "propose", "my-novel", "--scene", "my-novel/scene-2", "--entry", "canon-9", "--character", "canon-1"}, "knowledge.unknown-entry", "entry"},
		{"unknown-character", []string{"knowledge", "propose", "my-novel", "--scene", "my-novel/scene-2", "--entry", "canon-2", "--character", "canon-2"}, "knowledge.unknown-character", "character"},
		{"duplicate-id", []string{"knowledge", "propose", "my-novel", "--scene", "my-novel/scene-2", "--entry", "canon-2", "--character", "canon-1", "--id", "know-1"}, "artifact.duplicate-id", "id"},
		{"bad-id", []string{"knowledge", "propose", "my-novel", "--scene", "my-novel/scene-2", "--entry", "canon-2", "--character", "canon-1", "--id", "Bad"}, "artifact.schema-violation", "id"},
	} {
		code, res := runWS(t, dir, tc.args...)
		if code != 1 || diag(t, res, tc.code).Code == "" {
			t.Fatalf("%s: exit code = %d, diagnostics = %+v", tc.name, code, res.Diagnostics)
		}
		if tc.field != "" && diag(t, res, tc.code).Field != tc.field {
			t.Fatalf("%s: field = %q, want %q", tc.name, diag(t, res, tc.code).Field, tc.field)
		}
	}
	// --character 问题（参数引起、不填 path）的 next 先给出设定集列表。
	code, res := runWS(t, dir, "knowledge", "propose", "my-novel", "--scene", "my-novel/scene-2", "--entry", "canon-2", "--character", "canon-2")
	if code != 1 {
		t.Fatalf("unknown-character: exit code = %d", code)
	}
	d := diag(t, res, "knowledge.unknown-character")
	if !slices.Contains(d.Next, shellCommand("wa-cli", "canon", "list", "my-novel", "--dir", dir, "--json")) {
		t.Fatalf("next = %v", d.Next)
	}
	if after := readFile(t, ledger); after != before {
		t.Fatalf("rejected propose modified knowledge.yaml")
	}
}

// knowledge confirm / reject：确认记录、缺 --confirmed-by-author、重复确认、撤销后已知列表更新。
func TestKnowledgeConfirmReject(t *testing.T) {
	dir := knowledgeFixture(t)

	// 缺 --confirmed-by-author。
	if code, res := runWS(t, dir, "knowledge", "confirm", "my-novel", "know-1", "--confirmation", "作者确认"); code != 1 ||
		diag(t, res, "knowledge.incomplete-confirmation").Code == "" {
		t.Fatalf("missing flag: exit code = %d, diagnostics = %+v", code, res.Diagnostics)
	}
	code, res := runWS(t, dir, "knowledge", "confirm", "my-novel", "know-1", "--confirmed-by-author", "--confirmation", "作者确认守灯人得知灯油规则")
	if code != 0 {
		t.Fatalf("confirm: exit code = %d", code)
	}
	e := knowledgeEventOf(t, res)
	if e.Status != "confirmed" || e.Confirmation == nil || *e.Confirmation != "作者确认守灯人得知灯油规则" || e.DecidedAt == nil || !strings.HasSuffix(*e.DecidedAt, "Z") {
		t.Fatalf("confirmed event = %+v", e)
	}
	// 重复确认。
	if code, res = runWS(t, dir, "knowledge", "confirm", "my-novel", "know-1", "--confirmed-by-author", "--confirmation", "再确认"); code != 1 ||
		diag(t, res, "knowledge.already-decided").Code == "" {
		t.Fatalf("confirm twice: exit code = %d, diagnostics = %+v", code, res.Diagnostics)
	}
	// 已确认后 scene-2 可见；撤销后为空。
	var out knowledgeAtOut
	code, res = runWS(t, dir, "knowledge", "at", "my-novel/scene-2", "--character", "canon-1")
	if code != 0 {
		t.Fatalf("at: exit code = %d", code)
	}
	if err := json.Unmarshal([]byte(res.stdout), &out); err != nil {
		t.Fatal(err)
	}
	if len(out.Knowledge) != 1 || out.Knowledge[0].Entry != "canon-2" || out.Knowledge[0].Name != nil {
		t.Fatalf("knowledge at = %+v", out.Knowledge)
	}
	code, res = runWS(t, dir, "knowledge", "reject", "my-novel", "know-1", "--confirmed-by-author", "--confirmation", "作者撤销该知情")
	if code != 0 {
		t.Fatalf("reject: exit code = %d", code)
	}
	if e := knowledgeEventOf(t, res); e.Status != "rejected" {
		t.Fatalf("rejected event = %+v", e)
	}
	code, res = runWS(t, dir, "knowledge", "at", "my-novel/scene-2", "--character", "canon-1")
	if code != 0 {
		t.Fatalf("at after reject: exit code = %d", code)
	}
	if err := json.Unmarshal([]byte(res.stdout), &out); err != nil {
		t.Fatal(err)
	}
	if len(out.Knowledge) != 0 {
		t.Fatalf("knowledge after reject = %+v", out.Knowledge)
	}
	// not-found 的 next 列出全部条目。
	if code, res = runWS(t, dir, "knowledge", "confirm", "my-novel", "know-9", "--confirmed-by-author", "--confirmation", "x"); code != 1 {
		t.Fatalf("missing entry: exit code = %d", code)
	} else if d := diag(t, res, "knowledge.not-found"); !slices.Contains(d.Next, shellCommand("wa-cli", "knowledge", "list", "my-novel", "--all", "--dir", dir, "--json")) {
		t.Fatalf("not-found next = %v", d.Next)
	}
}

// knowledge set：修改 proposed 条目免确认、修改 confirmed 条目须重新确认、--character 校验。
func TestKnowledgeSet(t *testing.T) {
	dir := knowledgeFixture(t)

	code, res := runWS(t, dir, "knowledge", "set", "my-novel", "know-1", "--character", "reader")
	if code != 0 {
		t.Fatalf("set proposed: exit code = %d", code)
	}
	if e := knowledgeEventOf(t, res); e.Character != "reader" || e.Status != "proposed" {
		t.Fatalf("set event = %+v", e)
	}
	// 取值未变不写文件。
	code, res = runWS(t, dir, "knowledge", "set", "my-novel", "know-1", "--character", "reader")
	if code != 0 || len(res.ChangedPaths) != 0 {
		t.Fatalf("no-op set: exit code = %d, changed = %v", code, res.ChangedPaths)
	}
	// confirmed 条目缺 --confirmed-by-author 与 --confirmation。
	runWS(t, dir, "knowledge", "confirm", "my-novel", "know-1", "--confirmed-by-author", "--confirmation", "作者确认")
	if code, res = runWS(t, dir, "knowledge", "set", "my-novel", "know-1", "--scene", "my-novel/scene-2"); code != 1 ||
		diag(t, res, "knowledge.incomplete-confirmation").Code == "" {
		t.Fatalf("set confirmed without confirmation: exit code = %d, diagnostics = %+v", code, res.Diagnostics)
	}
	code, res = runWS(t, dir, "knowledge", "set", "my-novel", "know-1", "--scene", "my-novel/scene-2", "--confirmed-by-author", "--confirmation", "作者改绑场景")
	if code != 0 {
		t.Fatalf("set confirmed: exit code = %d", code)
	}
	if e := knowledgeEventOf(t, res); e.Scene != "my-novel/scene-2" || *e.Confirmation != "作者改绑场景" {
		t.Fatalf("set confirmed event = %+v", e)
	}
	// --character 引用非 character 条目。
	if code, res = runWS(t, dir, "knowledge", "set", "my-novel", "know-1", "--character", "canon-2", "--confirmed-by-author", "--confirmation", "x"); code != 1 ||
		diag(t, res, "knowledge.unknown-character").Code == "" {
		t.Fatalf("set character: exit code = %d, diagnostics = %+v", code, res.Diagnostics)
	}
}

// knowledge at：--character 为条目 id 与 reader、unit 非 scene、--character 缺失或非法、无适用条目与人读输出。
func TestKnowledgeAt(t *testing.T) {
	dir := knowledgeFixture(t)
	runWS(t, dir, "knowledge", "confirm", "my-novel", "know-1", "--confirmed-by-author", "--confirmation", "作者确认")
	runWS(t, dir, "knowledge", "propose", "my-novel", "--scene", "my-novel/scene-1", "--entry", "canon-2", "--character", "reader", "--id", "know-r")
	runWS(t, dir, "knowledge", "confirm", "my-novel", "know-r", "--confirmed-by-author", "--confirmation", "作者确认读者得知")

	var out knowledgeAtOut
	code, res := runWS(t, dir, "knowledge", "at", "my-novel/scene-2", "--character", "canon-1")
	if code != 0 {
		t.Fatalf("at character: exit code = %d", code)
	}
	if err := json.Unmarshal([]byte(res.stdout), &out); err != nil {
		t.Fatal(err)
	}
	if len(out.Knowledge) != 1 || out.Knowledge[0].Entry != "canon-2" || out.Knowledge[0].Event != "know-1" {
		t.Fatalf("at character = %+v", out.Knowledge)
	}
	// reader 与人物互不串扰。
	code, res = runWS(t, dir, "knowledge", "at", "my-novel/scene-2", "--character", "reader")
	if code != 0 {
		t.Fatalf("at reader: exit code = %d", code)
	}
	if err := json.Unmarshal([]byte(res.stdout), &out); err != nil {
		t.Fatal(err)
	}
	if len(out.Knowledge) != 1 || out.Knowledge[0].Event != "know-r" {
		t.Fatalf("at reader = %+v", out.Knowledge)
	}
	// 人读输出。
	code, stdout, _ := run(t, "knowledge", "at", "my-novel/scene-2", "--character", "canon-1", "--dir", dir)
	if code != 0 || !strings.Contains(stdout, "canon-2：获知于 my-novel/scene-1") {
		t.Fatalf("human at = %s", stdout)
	}
	// unit 非 scene。
	if code, res = runWS(t, dir, "knowledge", "at", "my-novel/chapter-one", "--character", "canon-1"); code != 1 ||
		diag(t, res, "knowledge.unknown-unit").Field != "unit" {
		t.Fatalf("not scene: exit code = %d, diagnostics = %+v", code, res.Diagnostics)
	}
	// unit 不存在。
	if code, res = runWS(t, dir, "knowledge", "at", "my-novel/scene-9", "--character", "canon-1"); code != 1 ||
		diag(t, res, "knowledge.unknown-unit").Code == "" {
		t.Fatalf("missing unit: exit code = %d, diagnostics = %+v", code, res.Diagnostics)
	}
	// --character 缺失与非法：next 给出设定集列表与用法。
	if code, res = runWS(t, dir, "knowledge", "at", "my-novel/scene-2"); code != 1 ||
		diag(t, res, "knowledge.unknown-character").Code == "" {
		t.Fatalf("missing character: exit code = %d, diagnostics = %+v", code, res.Diagnostics)
	}
	if code, res = runWS(t, dir, "knowledge", "at", "my-novel/scene-2", "--character", "canon-2"); code != 1 {
		t.Fatalf("bad character: exit code = %d", code)
	} else if d := diag(t, res, "knowledge.unknown-character"); !slices.Contains(d.Next, shellCommand("wa-cli", "canon", "list", "my-novel", "--dir", dir, "--json")) {
		t.Fatalf("next = %v", d.Next)
	}
	// 无适用条目：空数组、退出码 0。
	code, res = runWS(t, dir, "knowledge", "at", "my-novel/scene-1", "--character", "canon-1")
	if code != 0 {
		t.Fatalf("scene start: exit code = %d", code)
	}
	if err := json.Unmarshal([]byte(res.stdout), &out); err != nil {
		t.Fatal(err)
	}
	if len(out.Knowledge) != 0 {
		t.Fatalf("scene start knowledge = %+v", out.Knowledge)
	}
}

// 整体校验与 validate <path> 纳入 knowledge.yaml：计入 checked、领域问题按错误码与 field 报告、
// 无台账作品零新增诊断。
func TestKnowledgeValidate(t *testing.T) {
	dir := knowledgeFixture(t)
	runWS(t, dir, "knowledge", "confirm", "my-novel", "know-1", "--confirmed-by-author", "--confirmation", "作者确认")
	rel := ".writing-anything/works/my-novel/knowledge.yaml"

	// 合法台账：validate 全绿且计入 checked。
	code, res := runWS(t, dir, "validate")
	if code != 0 {
		t.Fatalf("validate: exit code = %d, diagnostics = %+v", code, res.Diagnostics)
	}
	if !slices.Contains(res.Checked, rel) {
		t.Fatalf("checked = %v", res.Checked)
	}

	// 领域问题：character 引用非 character 条目（error 与 field）。
	runWS(t, dir, "canon", "add", "my-novel", "--type", "world-rule", "--text", "备用规则", "--id", "canon-3")
	runWS(t, dir, "knowledge", "propose", "my-novel", "--scene", "my-novel/scene-1", "--entry", "canon-2", "--character", "canon-1", "--id", "hand")
	ledger := filepath.Join(dir, filepath.FromSlash(rel))
	data := readFile(t, ledger)
	writeFile(t, ledger, strings.Replace(data, "character: canon-1\n    status: proposed", "character: canon-3\n    status: proposed", 1))
	code, res = runWS(t, dir, "validate")
	if code != 1 {
		t.Fatalf("invalid ledger validate: exit code = %d", code)
	}
	if d := diag(t, res, "knowledge.unknown-character"); d.Field != "events[1].character" {
		t.Fatalf("unknown-character diagnostic = %+v", d)
	}

	// validate <path> 对单个 knowledge.yaml 的检查。
	code, res = runWS(t, dir, "validate", rel)
	if code != 1 || diag(t, res, "knowledge.unknown-character").Code == "" {
		t.Fatalf("validate path: exit code = %d, diagnostics = %+v", code, res.Diagnostics)
	}

	// 无台账作品：不新增 checked 与诊断。
	empty := newNovel(t)
	code, res = runWS(t, empty, "validate")
	if code != 0 {
		t.Fatalf("empty validate: exit code = %d, diagnostics = %+v", code, res.Diagnostics)
	}
	if slices.Contains(res.Checked, ".writing-anything/works/my-novel/knowledge.yaml") {
		t.Fatalf("checked should not contain knowledge.yaml, got %v", res.Checked)
	}

	// 台账途经符号链接按 artifact.not-found 处理。
	outside := filepath.Join(dir, "outside.yaml")
	writeFile(t, outside, "schema_version: 1\nevents: []\n")
	writeFile(t, ledger, "schema_version: 1\nevents: []\n")
	if err := os.Remove(ledger); err != nil {
		t.Fatal(err)
	}
	if err := os.Symlink(outside, ledger); err != nil {
		t.Fatal(err)
	}
	if code, res = runWS(t, dir, "validate"); code != 1 || diag(t, res, "artifact.not-found").Path != rel {
		t.Fatalf("symlink: exit code = %d, diagnostics = %+v", code, res.Diagnostics)
	}
}

// 场景级 context 的 knowledge 与 readerOnly 字段（--json 与人读 Markdown）与 pov 未解析的 warning。
func TestContextKnowledge(t *testing.T) {
	dir := knowledgeFixture(t)
	runWS(t, dir, "knowledge", "confirm", "my-novel", "know-1", "--confirmed-by-author", "--confirmation", "作者确认")
	runWS(t, dir, "knowledge", "propose", "my-novel", "--scene", "my-novel/scene-1", "--entry", "canon-2", "--character", "reader", "--id", "know-r")
	runWS(t, dir, "knowledge", "confirm", "my-novel", "know-r", "--confirmed-by-author", "--confirmation", "作者确认读者得知")
	// scene-2 的场景契约声明 pov 守灯人与揭示边界。
	runWS(t, dir, "scene", "contract", "my-novel/scene-2", "sc1", "--title", "灯下对峙", "--pov", "守灯人", "--reveal", "精灵的真实身份")

	code, res := runWS(t, dir, "context", "my-novel/scene-2")
	if code != 0 {
		t.Fatalf("context: exit code = %d, diagnostics = %+v", code, res.Diagnostics)
	}
	var out struct {
		Context *struct {
			Knowledge []struct {
				Entry string  `json:"entry"`
				Name  *string `json:"name"`
				Text  string  `json:"text"`
				Event string  `json:"event"`
				Scene string  `json:"scene"`
			} `json:"knowledge"`
			ReaderOnly []struct {
				Entry string `json:"entry"`
				Event string `json:"event"`
			} `json:"readerOnly"`
		} `json:"context"`
	}
	if err := json.Unmarshal([]byte(res.stdout), &out); err != nil {
		t.Fatal(err)
	}
	if out.Context == nil || len(out.Context.Knowledge) != 1 || out.Context.Knowledge[0].Entry != "canon-2" ||
		out.Context.Knowledge[0].Text != "灯油只能燃烧到黎明" || out.Context.Knowledge[0].Event != "know-1" {
		t.Fatalf("context.knowledge = %+v", out.Context)
	}
	if len(out.Context.ReaderOnly) != 0 {
		t.Fatalf("context.readerOnly = %+v（canon-2 双方已知，不应出现在 readerOnly）", out.Context.ReaderOnly)
	}
	// 人读 Markdown 小节（此时 readerOnly 为空，小节不输出；canon-3 加入后再检查）。
	_, stdout, _ := run(t, "context", "my-novel/scene-2", "--dir", dir)
	for _, want := range []string{"## 视角人物已知（POV Knowledge）", "灯油只能燃烧到黎明"} {
		if !strings.Contains(stdout, want) {
			t.Fatalf("markdown lacks %q:\n%s", want, stdout)
		}
	}
	if strings.Contains(stdout, "## 读者已知、人物未知（Reader Only）") {
		t.Fatalf("empty reader-only section should not be printed:\n%s", stdout)
	}

	// 只有读者获知的信息进入 readerOnly；人读 Markdown 带出该条目。
	runWS(t, dir, "canon", "add", "my-novel", "--type", "world-rule", "--text", "精灵是灯芯所化", "--id", "canon-3")
	runWS(t, dir, "knowledge", "propose", "my-novel", "--scene", "my-novel/scene-1", "--entry", "canon-3", "--character", "reader", "--id", "know-r2")
	runWS(t, dir, "knowledge", "confirm", "my-novel", "know-r2", "--confirmed-by-author", "--confirmation", "作者确认")
	code, res = runWS(t, dir, "context", "my-novel/scene-2")
	if code != 0 {
		t.Fatalf("context again: exit code = %d", code)
	}
	if err := json.Unmarshal([]byte(res.stdout), &out); err != nil {
		t.Fatal(err)
	}
	if len(out.Context.ReaderOnly) != 1 || out.Context.ReaderOnly[0].Entry != "canon-3" || out.Context.ReaderOnly[0].Event != "know-r2" {
		t.Fatalf("context.readerOnly = %+v", out.Context.ReaderOnly)
	}
	// 读者已知、人物未知的条目进入对应 Markdown 小节。
	_, stdout, _ = run(t, "context", "my-novel/scene-2", "--dir", dir)
	if !strings.Contains(stdout, "## 读者已知、人物未知（Reader Only）") || !strings.Contains(stdout, "精灵是灯芯所化") {
		t.Fatalf("markdown lacks reader-only section:\n%s", stdout)
	}

	// pov 未解析到 character 条目：warning knowledge.pov-not-in-canon，两个字段为空数组，退出码 0。
	runWS(t, dir, "scene", "contract", "my-novel/scene-2", "sc1", "--pov", "高人")
	code, res = runWS(t, dir, "context", "my-novel/scene-2")
	if code != 0 {
		t.Fatalf("context unresolved pov: exit code = %d, diagnostics = %+v", code, res.Diagnostics)
	}
	if d := diag(t, res, "knowledge.pov-not-in-canon"); d.Code == "" || !slices.Contains(d.Next, shellCommand("wa-cli", "canon", "list", "my-novel", "--dir", dir, "--json")) ||
		!slices.Contains(d.Next, "wa-cli help scene contract --json") {
		t.Fatalf("pov diagnostic = %+v", d)
	}
	if err := json.Unmarshal([]byte(res.stdout), &out); err != nil {
		t.Fatal(err)
	}
	if len(out.Context.Knowledge) != 0 || len(out.Context.ReaderOnly) != 0 {
		t.Fatalf("unresolved pov fields = %+v %+v", out.Context.Knowledge, out.Context.ReaderOnly)
	}

	// 非 scene 单元：knowledge 与 readerOnly 为空数组。
	code, res = runWS(t, dir, "context", "my-novel")
	if code != 0 {
		t.Fatalf("context root: exit code = %d", code)
	}
	if err := json.Unmarshal([]byte(res.stdout), &out); err != nil {
		t.Fatal(err)
	}
	if len(out.Context.Knowledge) != 0 || len(out.Context.ReaderOnly) != 0 {
		t.Fatalf("root unit fields = %+v %+v", out.Context.Knowledge, out.Context.ReaderOnly)
	}

	// knowledge.yaml 不合法时 context 为 null、退出码 1。
	ledger := filepath.Join(dir, ".writing-anything", "works", "my-novel", "knowledge.yaml")
	writeFile(t, ledger, "k: v\n")
	if code, res = runWS(t, dir, "context", "my-novel/scene-2"); code != 1 {
		t.Fatalf("broken ledger: exit code = %d", code)
	}
	if err := json.Unmarshal([]byte(res.stdout), &out); err != nil {
		t.Fatal(err)
	}
	if out.Context != nil {
		t.Fatalf("context should be null with broken ledger")
	}
}
