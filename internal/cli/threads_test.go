package cli

import (
	"encoding/json"
	"os"
	"regexp"
	"slices"
	"strings"
	"testing"
)

type threadEntry struct {
	ID             string  `json:"id"`
	Text           string  `json:"text"`
	PlantedIn      string  `json:"planted_in"`
	PlannedRecover string  `json:"planned_recover"`
	Status         string  `json:"status"`
	RecoveredIn    *string `json:"recovered_in"`
	Decision       *string `json:"decision"`
	Overdue        bool    `json:"overdue"`
}

type threadsListOut struct {
	Result
	Entries []threadEntry `json:"entries"`
}

type threadsAddOut struct {
	Result
	Thread *threadEntry `json:"thread"`
}

// threadsFixture 在 novel 工作区登记两条伏笔：f-1 埋在 scene-1、计划回收于 scene-2；
// f-2 埋在 scene-2、计划回收于 chapter-two。
func threadsFixture(t *testing.T) string {
	t.Helper()
	dir := newNovel(t)
	args := [][]string{
		{"threads", "add", "my-novel", "--text", "灯芯的来历", "--plant-in", "my-novel/scene-1", "--recover-at", "my-novel/scene-2", "--id", "f-1"},
		{"threads", "add", "my-novel", "--text", "老灯匠的怀表", "--plant-in", "my-novel/scene-2", "--recover-at", "my-novel/chapter-two", "--id", "f-2"},
	}
	for _, a := range args {
		if code, res := runWS(t, dir, a...); code != 0 || !res.Valid {
			t.Fatalf("%v: exit code = %d, result = %+v", a, code, res)
		}
	}
	return dir
}

func threadsOf(t *testing.T, r result) []threadEntry {
	t.Helper()
	var out threadsListOut
	if err := json.Unmarshal([]byte(r.stdout), &out); err != nil {
		t.Fatal(err)
	}
	return out.Entries
}

// threads 与 threads add：默认只列 open、按埋设场景先序排序、--id 缺省生成与重复拒绝、单元类型校验。
func TestThreadsListAndAdd(t *testing.T) {
	dir := threadsFixture(t)

	code, res := runWS(t, dir, "threads", "my-novel")
	if code != 0 {
		t.Fatalf("threads: exit code = %d", code)
	}
	entries := threadsOf(t, res)
	if len(entries) != 2 || entries[0].ID != "f-1" || entries[1].ID != "f-2" {
		t.Fatalf("entries = %+v", entries)
	}
	if entries[0].Status != "open" || entries[0].PlantedIn != "my-novel/scene-1" || entries[0].PlannedRecover != "my-novel/scene-2" {
		t.Fatalf("f-1 = %+v", entries[0])
	}

	// --id 缺省生成 thread-<n>；重复的显式 id 拒绝。
	if code, res := runWS(t, dir, "threads", "add", "my-novel", "--text", "精灵的誓言", "--plant-in", "my-novel/scene-1", "--recover-at", "my-novel/scene-3"); code != 0 {
		t.Fatalf("threads add default id: exit code = %d", code)
	} else {
		var out threadsAddOut
		json.Unmarshal([]byte(res.stdout), &out)
		if out.Thread == nil || out.Thread.ID != "thread-3" {
			t.Fatalf("default id = %+v", out.Thread)
		}
	}
	if code, _ := runWS(t, dir, "threads", "add", "my-novel", "--text", "重复", "--plant-in", "my-novel/scene-1", "--recover-at", "my-novel/scene-2", "--id", "f-1"); code != 1 {
		t.Fatalf("duplicate id: exit code = %d, want 1", code)
	}
	// 埋设位置必须是 scene 单元。
	if code, res := runWS(t, dir, "threads", "add", "my-novel", "--text", "章里埋", "--plant-in", "my-novel/chapter-one", "--recover-at", "my-novel/chapter-two"); code != 1 {
		t.Fatalf("plant-in chapter: exit code = %d, want 1", code)
	} else if d := diag(t, res, "threads.unknown-unit"); d.Field != "plant-in" {
		t.Fatalf("plant-in field = %q", d.Field)
	}
	// 计划回收位置早于埋设场景。
	if code, res := runWS(t, dir, "threads", "add", "my-novel", "--text", "倒收", "--plant-in", "my-novel/scene-2", "--recover-at", "my-novel/scene-1"); code != 1 {
		t.Fatalf("recover before plant: exit code = %d, want 1", code)
	} else {
		diag(t, res, "threads.recover-before-plant")
	}
	// 埋设在本章场景、计划在该章回收：合法（章含埋设场景之后的场景）。
	if code, _ := runWS(t, dir, "threads", "add", "my-novel", "--text", "章内收", "--plant-in", "my-novel/scene-1", "--recover-at", "my-novel/chapter-one", "--id", "f-ch"); code != 0 {
		t.Fatalf("chapter after plant: exit code = %d, want 0", code)
	}

	// 没有台账的作品列出空列表、退出码 0。
	dir2 := newNovel(t)
	if code, res := runWS(t, dir2, "threads", "my-novel"); code != 0 || len(threadsOf(t, res)) != 0 {
		t.Fatalf("no ledger: exit code = %d", code)
	}
}

// threads set：paid 须带 --recovered-in；paid 记录回收场景；not-found。
func TestThreadsSet(t *testing.T) {
	dir := threadsFixture(t)
	if code, res := runWS(t, dir, "threads", "set", "my-novel", "f-1", "--status", "paid"); code != 1 {
		t.Fatalf("paid without recovered-in: exit code = %d, want 1", code)
	} else if d := diag(t, res, "input.invalid-value"); d.Field != "status" {
		t.Fatalf("paid field = %q", d.Field)
	}
	if code, res := runWS(t, dir, "threads", "set", "my-novel", "f-1", "--status", "paid", "--recovered-in", "my-novel/scene-2"); code != 0 {
		t.Fatalf("paid: exit code = %d", code)
	} else {
		var out threadsAddOut
		json.Unmarshal([]byte(res.stdout), &out)
		if out.Thread == nil || out.Thread.Status != "paid" || out.Thread.RecoveredIn == nil || *out.Thread.RecoveredIn != "my-novel/scene-2" {
			t.Fatalf("paid entry = %+v", out.Thread)
		}
	}
	// paid 后默认列表不再包含；--all 时包含。
	if _, res := runWS(t, dir, "threads", "my-novel"); len(threadsOf(t, res)) != 1 {
		t.Fatalf("open entries = %+v", threadsOf(t, res))
	}
	if _, res := runWS(t, dir, "threads", "my-novel", "--all"); len(threadsOf(t, res)) != 2 {
		t.Fatalf("all entries = %+v", threadsOf(t, res))
	}
	if code, res := runWS(t, dir, "threads", "set", "my-novel", "f-9", "--text", "没有"); code != 1 {
		t.Fatalf("not-found: exit code = %d, want 1", code)
	} else {
		d := diag(t, res, "threads.not-found")
		if !hasNext(d, "wa-cli threads my-novel") {
			t.Fatalf("not-found next = %v", d.Next)
		}
	}
}

// 放弃需要作者确认：未授权拒绝并生成阻塞决策，作者允许后携带 --decision 写入。
func TestThreadsDrop(t *testing.T) {
	dir := threadsFixture(t)
	code, res := runWS(t, dir, "threads", "set", "my-novel", "f-1", "--status", "dropped")
	if code != 1 {
		t.Fatalf("drop without decision: exit code = %d, want 1", code)
	}
	d := diag(t, res, "threads.author-confirmation")
	if !hasNext(d, "wa-cli decisions") || !hasNext(d, "wa-cli help threads set") {
		t.Fatalf("author-confirmation next = %v", d.Next)
	}
	if !slices.Equal(res.ChangedPaths, []string{".writing-anything/decisions.yaml"}) {
		t.Fatalf("changed = %v", res.ChangedPaths)
	}
	// 再次尝试幂等：不重复追加决策。
	runWS(t, dir, "threads", "set", "my-novel", "f-1", "--status", "dropped")
	if code, res := runWS(t, dir, "decisions"); code != 0 {
		t.Fatalf("decisions: exit code = %d", code)
	} else {
		var out struct {
			Blocking []map[string]any `json:"blocking"`
		}
		json.Unmarshal([]byte(res.stdout), &out)
		if len(out.Blocking) != 1 {
			t.Fatalf("blocking decisions = %d, want 1", len(out.Blocking))
		}
	}
	// 作者允许后携带 --decision 写入。
	runWS(t, dir, "decide", "dec-1", "--option", "a", "--confirmation", "作者：放弃这条线")
	if code, res := runWS(t, dir, "threads", "set", "my-novel", "f-1", "--status", "dropped", "--decision", "dec-1"); code != 0 {
		t.Fatalf("drop with decision: exit code = %d", code)
	} else {
		var out threadsAddOut
		json.Unmarshal([]byte(res.stdout), &out)
		if out.Thread == nil || out.Thread.Status != "dropped" || out.Thread.Decision == nil || *out.Thread.Decision != "dec-1" {
			t.Fatalf("dropped entry = %+v", out.Thread)
		}
	}
	// 台账条目记录的授权决策消失时 validate 报告 threads.unknown-decision。
	// （决策队列不能删，这里直接改写台账引用不存在的决策来触发。）
	writeFile(t, dir+"/.writing-anything/works/my-novel/threads.yaml",
		"schema_version: 1\nentries:\n  - {id: f-1, text: 灯芯的来历, planted_in: my-novel/scene-1, planned_recover: my-novel/scene-2, status: dropped, decision: dec-9}\n")
	if code, res := runValidateJSON(t, dir); code != 1 {
		t.Fatalf("validate: exit code = %d, want 1", code)
	} else if d := diag(t, res, "threads.unknown-decision"); d.Field != "entries[0].decision" {
		t.Fatalf("unknown-decision field = %q", d.Field)
	}
}

// overdue：计划回收场景 done、章整体 done（推导）或章后场景 done 时标注；人读输出含标注。
func TestThreadsOverdue(t *testing.T) {
	dir := threadsFixture(t)
	// 把 scene-2 标为 done：f-1 的计划回收场景（scene-2）已 done → 超过；f-2 的 chapter-two 还没走到 → 不超过。
	setWorkStage(t, dir, "scene-2", "done")
	if code, res := runWS(t, dir, "threads", "my-novel"); code != 0 {
		t.Fatalf("threads: exit code = %d", code)
	} else {
		entries := threadsOf(t, res)
		if !entries[0].Overdue || entries[1].Overdue {
			t.Fatalf("entries = %+v", entries)
		}
	}
	// 人读输出标注已超过计划回收位置。
	code, stdout, _ := run(t, "threads", "my-novel", "--dir", dir)
	if code != 0 || !strings.Contains(stdout, "已超过计划回收位置") {
		t.Fatalf("human output missing overdue note: %q", stdout)
	}
	// scene-3 done：先序在 scene-2 之后且不在其子树内 → f-1 也超过；chapter-two 只含 scene-3，
	// 推导为 done → f-2 超过。
	dir2 := threadsFixture(t)
	setWorkStage(t, dir2, "scene-3", "done")
	_, res := runWS(t, dir2, "threads", "my-novel")
	entries := threadsOf(t, res)
	if !entries[0].Overdue || !entries[1].Overdue {
		t.Fatalf("entries = %+v", entries)
	}
	// 章内先行场景 done 而章未推导为 done 时不误报：chapter-one 的 scene-1 done、scene-2 未 done，
	// 计划回收于 chapter-one 的条目不 overdue。
	dir3 := threadsFixture(t)
	runWS(t, dir3, "threads", "add", "my-novel", "--text", "章内收", "--plant-in", "my-novel/scene-1", "--recover-at", "my-novel/chapter-one", "--id", "f-ch")
	setWorkStage(t, dir3, "scene-1", "done")
	_, res = runWS(t, dir3, "threads", "my-novel")
	for _, e := range threadsOf(t, res) {
		if e.ID == "f-ch" && e.Overdue {
			t.Fatalf("chapter in progress should not be overdue: %+v", e)
		}
	}
}

// scene contract 的写前预检：--recover 引用未登记伏笔拒绝且不写文件；无台账作品按空台账同样拒绝。
func TestThreadsScenePrecheck(t *testing.T) {
	dir := threadsFixture(t)
	code, res := runWS(t, dir, "scene", "contract", "my-novel/scene-2", "beat-1", "--title", "收线", "--recover", "f-9")
	if code != 1 {
		t.Fatalf("recover unknown: exit code = %d, want 1", code)
	}
	d := diag(t, res, "threads.unknown-thread")
	if d.Field != "recovers[0]" || !hasNext(d, "wa-cli threads my-novel") {
		t.Fatalf("unknown-thread = %+v", d)
	}
	if slices.Contains(res.ChangedPaths, ".writing-anything/works/my-novel/units/scene-2/outline.yaml") {
		t.Fatalf("outline written on refusal: %v", res.ChangedPaths)
	}
	// 埋设引用埋在别处的伏笔。
	if code, res := runWS(t, dir, "scene", "contract", "my-novel/scene-1", "beat-1", "--title", "灯下", "--plant", "f-2"); code != 1 {
		t.Fatalf("plant elsewhere: exit code = %d, want 1", code)
	} else {
		d := diag(t, res, "threads.unknown-thread")
		if d.Field != "plants[0]" {
			t.Fatalf("plant field = %q", d.Field)
		}
	}
	// 合法引用写入成功。
	if code, _ := runWS(t, dir, "scene", "contract", "my-novel/scene-2", "beat-1", "--title", "收线", "--recover", "f-1"); code != 0 {
		t.Fatalf("valid recover: exit code = %d", code)
	}
	// 没有台账的作品：按空台账检查，--plant 引用的伏笔未登记，拒绝写入并指向 threads add。
	dir2 := newNovel(t)
	code, res = runWS(t, dir2, "scene", "contract", "my-novel/scene-1", "beat-1", "--title", "灯下", "--plant", "f-1")
	if code != 1 {
		t.Fatalf("no ledger plant: exit code = %d, want 1", code)
	}
	if d := diag(t, res, "threads.unknown-thread"); d.Field != "plants[0]" || d.Path != "" || !hasNext(d, "wa-cli help threads add --json") ||
		!hasNext(d, "wa-cli help scene contract --json") || !hasNext(d, "wa-cli threads my-novel") {
		t.Fatalf("no ledger plant = %+v", d)
	}
	if len(res.ChangedPaths) != 0 {
		t.Fatalf("outline written on refusal: %v", res.ChangedPaths)
	}
	if _, err := os.Stat(dir2 + "/.writing-anything/works/my-novel/units/scene-1/outline.yaml"); err == nil {
		t.Fatalf("outline created on refusal")
	}
	// 没有 --plant/--recover 的契约照常写入。
	if code, _ := runWS(t, dir2, "scene", "contract", "my-novel/scene-1", "beat-1", "--title", "灯下"); code != 0 {
		t.Fatalf("no ledger plain contract: exit code = %d", code)
	}
}

// 没有台账的作品：validate 整体校验与 continuity 按空台账报告 recovers 引用的未登记伏笔，不把 threads.yaml 计入 checked。
func TestThreadsValidateNoLedger(t *testing.T) {
	dir := newNovel(t)
	os.MkdirAll(dir+"/.writing-anything/works/my-novel/units/scene-2", 0o755)
	writeFile(t, dir+"/.writing-anything/works/my-novel/units/scene-2/outline.yaml",
		"schema_version: 1\nsections:\n  - {id: beat-1, title: 收线, reason: 收线, recovers: [f-9]}\n")
	code, out := runValidateJSON(t, dir)
	if code != 1 {
		t.Fatalf("no ledger recover: exit code = %d, want 1, diagnostics = %+v", code, out.Diagnostics)
	}
	d := diag(t, out, "threads.unknown-thread")
	if d.Field != "sections[0].recovers[0]" || d.Path != ".writing-anything/works/my-novel/units/scene-2/outline.yaml" {
		t.Fatalf("no ledger diag = %+v", d)
	}
	if slices.Contains(out.Checked, ".writing-anything/works/my-novel/threads.yaml") {
		t.Fatalf("checked = %v", out.Checked)
	}
	// continuity 把同一问题报告为 error 级发现项（continuity.dangling-reference，message 带原错误码）。
	_, res := runWS(t, dir, "continuity", "my-novel")
	var cont struct {
		Findings []struct{ Scene, Code, Severity, Message string } `json:"findings"`
	}
	if err := json.Unmarshal([]byte(res.stdout), &cont); err != nil {
		t.Fatalf("continuity output: %v", err)
	}
	if !slices.ContainsFunc(cont.Findings, func(f struct{ Scene, Code, Severity, Message string }) bool {
		return f.Scene == "my-novel/scene-2" && f.Code == "continuity.dangling-reference" && f.Severity == "error" &&
			strings.Contains(f.Message, "threads.unknown-thread")
	}) {
		t.Fatalf("continuity no ledger: %s", res.stdout)
	}
}

// validate 整体校验：threads.yaml 计入 checked；契约交叉问题报告；validate <path> 支持台账。
func TestThreadsValidate(t *testing.T) {
	dir := threadsFixture(t)
	os.MkdirAll(dir+"/.writing-anything/works/my-novel/units/scene-2", 0o755)
	writeFile(t, dir+"/.writing-anything/works/my-novel/units/scene-2/outline.yaml",
		"schema_version: 1\nsections:\n  - {id: beat-1, title: 收线, reason: 收线, recovers: [f-1]}\n")
	if code, out := runValidateJSON(t, dir); code != 0 {
		t.Fatalf("valid workspace: exit code = %d, diagnostics = %+v", code, out.Diagnostics)
	} else if !slices.Contains(out.Checked, ".writing-anything/works/my-novel/threads.yaml") {
		t.Fatalf("checked = %v", out.Checked)
	}
	// recovers 引用不存在的伏笔。
	writeFile(t, dir+"/.writing-anything/works/my-novel/units/scene-2/outline.yaml",
		"schema_version: 1\nsections:\n  - {id: beat-1, title: 收线, reason: 收线, recovers: [f-9]}\n")
	if code, out := runValidateJSON(t, dir); code != 1 {
		t.Fatalf("cross problem: exit code = %d, want 1", code)
	} else if d := diag(t, out, "threads.unknown-thread"); d.Field != "sections[0].recovers[0]" {
		t.Fatalf("cross field = %q", d.Field)
	}
	// recovers 引用埋设晚于本场景的伏笔（f-2 埋在 scene-2，被 scene-1 引用）。
	os.MkdirAll(dir+"/.writing-anything/works/my-novel/units/scene-1", 0o755)
	writeFile(t, dir+"/.writing-anything/works/my-novel/units/scene-1/outline.yaml",
		"schema_version: 1\nsections:\n  - {id: beat-0, title: 灯下, reason: 灯下, recovers: [f-2]}\n")
	if code, out := runValidateJSON(t, dir); code != 1 {
		t.Fatalf("recover-not-planted: exit code = %d, want 1", code)
	} else if d := diag(t, out, "threads.recover-not-planted"); d.Field != "sections[0].recovers[0]" {
		t.Fatalf("recover-not-planted field = %q", d.Field)
	}
	os.Remove(dir + "/.writing-anything/works/my-novel/units/scene-1/outline.yaml")

	// validate <path> 对单个台账。
	writeFile(t, dir+"/.writing-anything/works/my-novel/threads.yaml",
		"schema_version: 1\nentries:\n  - {id: f-1, text: 灯芯的来历, planted_in: my-novel/scene-1, planned_recover: nowhere/s9, status: open}\n")
	if code, out := runWS(t, dir, "validate", ".writing-anything/works/my-novel/threads.yaml"); code != 1 {
		t.Fatalf("artifact validate: exit code = %d, want 1", code)
	} else if d := diag(t, out, "threads.unknown-unit"); d.Field != "entries[0].planned_recover" {
		t.Fatalf("artifact field = %q", d.Field)
	}
}

// setWorkStage 把 work.yaml 中单元 id 的 stage 改为 stage（任意嵌套缩进）。
func setWorkStage(t *testing.T, dir, id, stage string) {
	t.Helper()
	rel := dir + "/.writing-anything/works/my-novel/work.yaml"
	data := []byte(readFile(t, rel))
	re := regexp.MustCompile(`(?m)^(\s*- id: ` + id + `\n\s*type: \w+\n\s*stage: )\w+$`)
	if !re.Match(data) {
		t.Fatalf("work.yaml has no stage block for %s", id)
	}
	writeFile(t, rel, string(re.ReplaceAll(data, []byte("${1}"+stage))))
}
