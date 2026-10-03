package cli

import (
	"encoding/json"
	"os"
	"path/filepath"
	"strings"
	"testing"
)

type stateEvent struct {
	ID           string  `json:"id"`
	Scene        string  `json:"scene"`
	Entry        string  `json:"entry"`
	State        string  `json:"state"`
	Status       string  `json:"status"`
	Confirmation *string `json:"confirmation"`
	DecidedAt    *string `json:"decided_at"`
}

type stateEventOut struct {
	Result
	Event *stateEvent `json:"event"`
}

type stateListOut struct {
	Result
	Events []stateEvent `json:"events"`
}

type stateAtOut struct {
	Result
	Snapshot []struct {
		Entry    string  `json:"entry"`
		Name     *string `json:"name"`
		State    string  `json:"state"`
		Proposal string  `json:"proposal"`
		Scene    string  `json:"scene"`
	} `json:"snapshot"`
}

// stateFixture 在 novel 工作区登记设定条目 canon-1（守灯人）并提交状态变更提议 state-1（绑定 scene-1）。
func stateFixture(t *testing.T) string {
	t.Helper()
	dir := newNovel(t)
	for _, args := range [][]string{
		{"canon", "add", "my-novel", "--type", "character", "--name", "守灯人", "--text", "五十岁，左手有旧伤"},
		{"state", "propose", "my-novel", "--scene", "my-novel/scene-1", "--entry", "canon-1", "--state", "左手旧伤复发"},
	} {
		if code, res := runWS(t, dir, args...); code != 0 || !res.Valid {
			t.Fatalf("%v: exit code = %d, result = %+v", args, code, res)
		}
	}
	return dir
}

func stateEventOf(t *testing.T, r result) *stateEvent {
	t.Helper()
	var out stateEventOut
	if err := json.Unmarshal([]byte(r.stdout), &out); err != nil {
		t.Fatal(err)
	}
	return out.Event
}

func stateListOf(t *testing.T, r result) []stateEvent {
	t.Helper()
	var out stateListOut
	if err := json.Unmarshal([]byte(r.stdout), &out); err != nil {
		t.Fatal(err)
	}
	return out.Events
}

func diagOf(res result, code string) *Diagnostic {
	for i := range res.Diagnostics {
		if res.Diagnostics[i].Code == code {
			return &res.Diagnostics[i]
		}
	}
	return nil
}

// state propose 与 state list：--id 缺省生成、默认只列 proposed、--all 列出全部、无台账为空、退出码与人读输出。
func TestStateProposeAndList(t *testing.T) {
	dir := stateFixture(t)

	code, res := runWS(t, dir, "state", "propose", "my-novel", "--scene", "my-novel/scene-2", "--entry", "canon-1", "--state", "伤愈")
	if code != 0 || !res.Valid {
		t.Fatalf("propose: exit code = %d, result = %+v", code, res)
	}
	if e := stateEventOf(t, res); e.ID != "state-2" || e.Status != "proposed" || e.Confirmation != nil || e.DecidedAt != nil {
		t.Fatalf("event = %+v", e)
	}

	code, res = runWS(t, dir, "state", "confirm", "my-novel", "state-2", "--confirmed-by-author", "--confirmation", "作者确认伤愈")
	if code != 0 {
		t.Fatalf("confirm: exit code = %d", code)
	}

	// 默认只列 proposed（作者待处理清单）。
	code, res = runWS(t, dir, "state", "list", "my-novel")
	if code != 0 {
		t.Fatalf("list: exit code = %d", code)
	}
	events := stateListOf(t, res)
	if len(events) != 1 || events[0].ID != "state-1" || events[0].Status != "proposed" {
		t.Fatalf("proposed events = %+v", events)
	}
	// --all 列出全部，按绑定场景先序排序。
	code, res = runWS(t, dir, "state", "list", "my-novel", "--all")
	if code != 0 {
		t.Fatalf("list --all: exit code = %d", code)
	}
	events = stateListOf(t, res)
	if len(events) != 2 || events[0].ID != "state-1" || events[1].ID != "state-2" {
		t.Fatalf("all events = %+v", events)
	}

	// 无台账作品为空列表。
	empty := newNovel(t)
	code, res = runWS(t, empty, "state", "list", "my-novel")
	if code != 0 || len(stateListOf(t, res)) != 0 || len(res.ChangedPaths) != 0 {
		t.Fatalf("empty ledger: exit code = %d, res = %+v", code, res)
	}

	// 人读输出标注状态。
	code, stdout, stderr := run(t, "state", "list", "my-novel", "--all", "--dir", dir)
	if code != 0 || stderr != "" {
		t.Fatalf("human list: exit code = %d, stderr = %q", code, stderr)
	}
	if !strings.Contains(stdout, "state-2") || !strings.Contains(stdout, "［已确认］") {
		t.Fatalf("human output = %q", stdout)
	}
}

// state propose 的拒绝分支：单元类型、设定条目、id 重复；失败不写文件；next 指向复查命令。
func TestStateProposeRejects(t *testing.T) {
	dir := newNovel(t)
	runWS(t, dir, "canon", "add", "my-novel", "--type", "character", "--name", "守灯人", "--text", "五十岁")

	// 绑定章而非场景。
	code, res := runWS(t, dir, "state", "propose", "my-novel", "--scene", "my-novel/chapter-one", "--entry", "canon-1", "--state", "受伤")
	if code != 1 || res.Valid {
		t.Fatalf("chapter scene: exit code = %d", code)
	}
	if d := diagOf(res, "state.unknown-unit"); d == nil || d.Field != "scene" {
		t.Fatalf("diagnostics = %+v", res.Diagnostics)
	}
	// 设定条目不存在；next 含 canon list。
	code, res = runWS(t, dir, "state", "propose", "my-novel", "--scene", "my-novel/scene-1", "--entry", "canon-9", "--state", "受伤")
	if code != 1 {
		t.Fatalf("unknown entry: exit code = %d", code)
	}
	d := diagOf(res, "state.unknown-entry")
	if d == nil || d.Field != "entry" {
		t.Fatalf("diagnostics = %+v", res.Diagnostics)
	}
	if len(d.Next) == 0 || !strings.Contains(d.Next[0], "canon list") {
		t.Fatalf("next = %v", d.Next)
	}
	// 失败不创建 state.yaml。
	if _, err := os.Stat(filepath.Join(dir, ".writing-anything", "works", "my-novel", "state.yaml")); err == nil {
		t.Fatal("rejected propose must not create state.yaml")
	}
	// id 重复：参数问题不填 path，next 给用法而不是复查未写入的文件。
	runWS(t, dir, "state", "propose", "my-novel", "--scene", "my-novel/scene-1", "--entry", "canon-1", "--state", "受伤", "--id", "s-1")
	code, res = runWS(t, dir, "state", "propose", "my-novel", "--scene", "my-novel/scene-1", "--entry", "canon-1", "--state", "受伤", "--id", "s-1")
	if code != 1 {
		t.Fatalf("duplicate id: exit code = %d", code)
	}
	if d := diagOf(res, "artifact.duplicate-id"); d == nil || d.Field != "id" || d.Path != "" || !strings.Contains(d.Next[0], "help state propose") {
		t.Fatalf("duplicate id diagnostic = %+v", d)
	}
	// 空白 --state：参数问题 next 给用法。
	code, res = runWS(t, dir, "state", "propose", "my-novel", "--scene", "my-novel/scene-1", "--entry", "canon-1", "--state", " ")
	if code != 1 {
		t.Fatalf("blank state: exit code = %d", code)
	}
	if d := diagOf(res, "artifact.schema-violation"); d == nil || d.Field != "state" || d.Path != "" || !strings.Contains(d.Next[0], "help state propose") {
		t.Fatalf("blank state diagnostic = %+v", d)
	}
}

// state confirm / state reject：确认记录、缺参数、重复决定、撤销已确认条目。
func TestStateConfirmReject(t *testing.T) {
	dir := stateFixture(t)

	// 缺 --confirmed-by-author。
	code, res := runWS(t, dir, "state", "confirm", "my-novel", "state-1", "--confirmation", "作者确认")
	if code != 1 || diagOf(res, "state.incomplete-confirmation") == nil {
		t.Fatalf("missing flag: exit code = %d, diagnostics = %+v", code, res.Diagnostics)
	}
	// 确认写入 confirmation 与 decided_at。
	code, res = runWS(t, dir, "state", "confirm", "my-novel", "state-1", "--confirmed-by-author", "--confirmation", "作者确认旧伤复发")
	if code != 0 || !res.Valid {
		t.Fatalf("confirm: exit code = %d, res = %+v", code, res)
	}
	e := stateEventOf(t, res)
	if e.Status != "confirmed" || e.Confirmation == nil || *e.Confirmation != "作者确认旧伤复发" || e.DecidedAt == nil || !strings.HasSuffix(*e.DecidedAt, "Z") {
		t.Fatalf("confirmed event = %+v", e)
	}
	// 重复确认。
	code, res = runWS(t, dir, "state", "confirm", "my-novel", "state-1", "--confirmed-by-author", "--confirmation", "再次")
	if code != 1 || diagOf(res, "state.already-decided") == nil {
		t.Fatalf("confirm twice: exit code = %d, diagnostics = %+v", code, res.Diagnostics)
	}
	// 撤销已确认条目。
	code, res = runWS(t, dir, "state", "reject", "my-novel", "state-1", "--confirmed-by-author", "--confirmation", "作者撤销该变化")
	if code != 0 {
		t.Fatalf("reject confirmed: exit code = %d", code)
	}
	if e := stateEventOf(t, res); e.Status != "rejected" {
		t.Fatalf("rejected event = %+v", e)
	}
	// rejected 为终态。
	code, res = runWS(t, dir, "state", "reject", "my-novel", "state-1", "--confirmed-by-author", "--confirmation", "再拒绝")
	if code != 1 || diagOf(res, "state.already-decided") == nil {
		t.Fatalf("reject twice: exit code = %d, diagnostics = %+v", code, res.Diagnostics)
	}
	// 条目不存在；next 含 state list --all。
	code, res = runWS(t, dir, "state", "confirm", "my-novel", "state-9", "--confirmed-by-author", "--confirmation", "x")
	if code != 1 {
		t.Fatalf("not found: exit code = %d", code)
	}
	if d := diagOf(res, "state.not-found"); d == nil || !strings.Contains(d.Next[0], "state list") {
		t.Fatalf("diagnostics = %+v", res.Diagnostics)
	}
}

// state set：proposed 自由修改；confirmed 须 --confirmed-by-author 与 --confirmation。
func TestStateSet(t *testing.T) {
	dir := stateFixture(t)

	code, res := runWS(t, dir, "state", "set", "my-novel", "state-1", "--state", "旧伤复发，隐痛")
	if code != 0 || !res.Valid {
		t.Fatalf("set proposed: exit code = %d, res = %+v", code, res)
	}
	if e := stateEventOf(t, res); e.State != "旧伤复发，隐痛" || e.Status != "proposed" {
		t.Fatalf("event = %+v", e)
	}

	runWS(t, dir, "state", "confirm", "my-novel", "state-1", "--confirmed-by-author", "--confirmation", "作者确认")
	// 修改 confirmed 条目缺作者重新确认。
	code, res = runWS(t, dir, "state", "set", "my-novel", "state-1", "--state", "重伤")
	if code != 1 || diagOf(res, "state.incomplete-confirmation") == nil {
		t.Fatalf("set confirmed without confirmation: exit code = %d, diagnostics = %+v", code, res.Diagnostics)
	}
	// 带作者重新确认后写入。
	code, res = runWS(t, dir, "state", "set", "my-novel", "state-1", "--state", "重伤", "--confirmed-by-author", "--confirmation", "作者修正为重伤")
	if code != 0 {
		t.Fatalf("set confirmed: exit code = %d", code)
	}
	if e := stateEventOf(t, res); e.State != "重伤" || e.Status != "confirmed" || *e.Confirmation != "作者修正为重伤" {
		t.Fatalf("event = %+v", e)
	}
	// --scene 类型校验。
	code, res = runWS(t, dir, "state", "set", "my-novel", "state-1", "--scene", "my-novel/part-one", "--confirmed-by-author", "--confirmation", "作者改绑")
	if code != 1 || diagOf(res, "state.unknown-unit") == nil {
		t.Fatalf("set bad scene: exit code = %d, diagnostics = %+v", code, res.Diagnostics)
	}
}

// state at：快照输出、目标场景自身排除、proposed/rejected 不参与、非 scene 单元拒绝。
func TestStateAt(t *testing.T) {
	dir := stateFixture(t)
	// scene-2 上再登记一条 proposed；快照只看 confirmed。
	runWS(t, dir, "state", "propose", "my-novel", "--scene", "my-novel/scene-2", "--entry", "canon-1", "--state", "误写", "--id", "s-2")

	// 目标场景自身与 proposed 不参与：scene-1 开始时为空。
	code, res := runWS(t, dir, "state", "at", "my-novel/scene-1")
	if code != 0 {
		t.Fatalf("at scene-1: exit code = %d", code)
	}
	var out stateAtOut
	if err := json.Unmarshal([]byte(res.stdout), &out); err != nil {
		t.Fatal(err)
	}
	if len(out.Snapshot) != 0 {
		t.Fatalf("scene-1 snapshot = %+v", out.Snapshot)
	}

	// 确认 state-1 后，scene-2 与 scene-3 的快照含它；目标场景自身的条目不参与。
	runWS(t, dir, "state", "confirm", "my-novel", "state-1", "--confirmed-by-author", "--confirmation", "作者确认")
	code, res = runWS(t, dir, "state", "at", "my-novel/scene-3")
	if code != 0 {
		t.Fatalf("at scene-3: exit code = %d", code)
	}
	out = stateAtOut{}
	if err := json.Unmarshal([]byte(res.stdout), &out); err != nil {
		t.Fatal(err)
	}
	if len(out.Snapshot) != 1 {
		t.Fatalf("scene-3 snapshot = %+v", out.Snapshot)
	}
	item := out.Snapshot[0]
	if item.Entry != "canon-1" || item.State != "左手旧伤复发" || item.Proposal != "state-1" || item.Scene != "my-novel/scene-1" {
		t.Fatalf("snapshot item = %+v", item)
	}
	if item.Name == nil || *item.Name != "守灯人" {
		t.Fatalf("name = %v", item.Name)
	}

	// 非 scene 单元与不存在的单元都报告 state.unknown-unit（field 为 unit）。
	code, res = runWS(t, dir, "state", "at", "my-novel/chapter-one")
	if code != 1 {
		t.Fatalf("at chapter: exit code = %d", code)
	}
	if d := diagOf(res, "state.unknown-unit"); d == nil || d.Field != "unit" {
		t.Fatalf("at chapter diagnostics = %+v", res.Diagnostics)
	}
	code, res = runWS(t, dir, "state", "at", "my-novel/scene-9")
	if code != 1 || diagOf(res, "state.unknown-unit") == nil || diagOf(res, "unit.not-found") != nil {
		t.Fatalf("at missing unit: exit code = %d, diagnostics = %+v", code, res.Diagnostics)
	}
	// 人读输出。
	code, stdout, stderr := run(t, "state", "at", "my-novel/scene-3", "--dir", dir)
	if code != 0 || stderr != "" || !strings.Contains(stdout, "守灯人") {
		t.Fatalf("human at: exit code = %d, stdout = %q, stderr = %q", code, stdout, stderr)
	}

	// 修改早期已确认条目后，后续场景的快照随之重新计算；撤销后不再包含。
	runWS(t, dir, "state", "set", "my-novel", "state-1", "--state", "重伤", "--confirmed-by-author", "--confirmation", "作者修正为重伤")
	code, res = runWS(t, dir, "state", "at", "my-novel/scene-3")
	if code != 0 {
		t.Fatalf("at after edit: exit code = %d", code)
	}
	out = stateAtOut{}
	if err := json.Unmarshal([]byte(res.stdout), &out); err != nil {
		t.Fatal(err)
	}
	if len(out.Snapshot) != 1 || out.Snapshot[0].State != "重伤" {
		t.Fatalf("snapshot after edit = %+v", out.Snapshot)
	}
	runWS(t, dir, "state", "reject", "my-novel", "state-1", "--confirmed-by-author", "--confirmation", "作者撤销")
	code, res = runWS(t, dir, "state", "at", "my-novel/scene-3")
	if code != 0 {
		t.Fatalf("at after revoke: exit code = %d", code)
	}
	out = stateAtOut{}
	if err := json.Unmarshal([]byte(res.stdout), &out); err != nil {
		t.Fatal(err)
	}
	if len(out.Snapshot) != 0 {
		t.Fatalf("snapshot after revoke = %+v", out.Snapshot)
	}

}

// 整体校验与单产物校验纳入 state.yaml；无台账作品零新增诊断。
func TestStateValidate(t *testing.T) {
	dir := stateFixture(t)
	rel := ".writing-anything/works/my-novel/state.yaml"

	// 合法台账：计入 checked、零诊断。
	code, res := runWS(t, dir, "validate")
	if code != 0 || !res.Valid {
		t.Fatalf("validate: exit code = %d, diagnostics = %+v", code, res.Diagnostics)
	}
	if !containsPath(res.Checked, rel) {
		t.Fatalf("checked = %v", res.Checked)
	}

	// 领域问题按错误码与 field 报告（propose 会当场拒绝悬空 entry，这里手写产物构造既有问题）。
	relPath := filepath.Join(dir, filepath.FromSlash(rel))
	data, err := os.ReadFile(relPath)
	if err != nil {
		t.Fatal(err)
	}
	os.WriteFile(relPath, append(data, []byte("  - id: bad\n    scene: my-novel/scene-1\n    entry: canon-9\n    state: 受伤\n    status: proposed\n")...), 0o644)
	code, res = runWS(t, dir, "validate")
	if code != 1 || res.Valid {
		t.Fatalf("validate with problems: exit code = %d", code)
	}
	found := false
	for _, d := range res.Diagnostics {
		if d.Code == "state.unknown-entry" && strings.Contains(d.Field, "entry") {
			found = true
		}
	}
	if !found {
		t.Fatalf("diagnostics = %+v", res.Diagnostics)
	}
	// validate <path> 对单个 state.yaml 的检查（entry 以该作品 canon.yaml 为准）。
	code, res = runWS(t, dir, "validate", rel)
	if code != 1 {
		t.Fatalf("validate <path>: exit code = %d", code)
	}
	if diagOf(res, "state.unknown-entry") == nil {
		t.Fatalf("validate <path> diagnostics = %+v", res.Diagnostics)
	}

	// 字面量 field 断言：非法 status 的 Schema 诊断指向 events[1].status。
	os.WriteFile(relPath, append(data, []byte("  - id: bad-status\n    scene: my-novel/scene-1\n    entry: canon-1\n    state: 受伤\n    status: decided\n    confirmation: 作者确认\n    decided_at: 2026-10-03T08:00:00Z\n")...), 0o644)
	code, res = runWS(t, dir, "validate", rel)
	if code != 1 {
		t.Fatalf("validate <path> bad status: exit code = %d", code)
	}
	if d := diagOf(res, "artifact.schema-violation"); d == nil || !strings.Contains(d.Field, "events[1].status") {
		t.Fatalf("bad status diagnostic = %+v", res.Diagnostics)
	}

	// 无台账作品的 validate 与既有断言一致（无 state 诊断）。
	clean := newNovel(t)
	code, res = runWS(t, clean, "validate")
	if code != 0 || !res.Valid {
		t.Fatalf("clean validate: exit code = %d, diagnostics = %+v", code, res.Diagnostics)
	}
	for _, d := range res.Diagnostics {
		if strings.HasPrefix(d.Code, "state.") {
			t.Fatalf("unexpected state diagnostic without ledger: %+v", d)
		}
	}
}

