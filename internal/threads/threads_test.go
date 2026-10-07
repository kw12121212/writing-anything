package threads

import (
	"os"
	"path/filepath"
	"regexp"
	"slices"
	"strings"
	"testing"
	"time"

	"github.com/kw12121212/writing-anything/internal/decision"
	"github.com/kw12121212/writing-anything/internal/outline"
	"github.com/kw12121212/writing-anything/internal/work"
	"github.com/kw12121212/writing-anything/internal/workspace"
)

// newNovel 初始化 novel 工作区：my-novel/chapter-one/{scene-1,scene-2} 与
// my-novel/scene-3（先序 scene-1、scene-2、scene-3）。
func newNovel(t *testing.T) (string, *work.Work) {
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
		{"my-novel/part-one", "chapter", "chapter-two"},
		{"my-novel/chapter-two", "scene", "scene-3"},
	} {
		if _, _, ps = work.AddUnit(root, add.parent, add.typ, add.id, work.Anchor{}); len(ps) > 0 {
			t.Fatal(ps)
		}
	}
	if w, ps = work.Load(root, "my-novel"); len(ps) > 0 {
		t.Fatal(ps)
	}
	return root, w
}

func write(t *testing.T, root, rel, content string) {
	t.Helper()
	if err := os.MkdirAll(filepath.Dir(filepath.Join(root, filepath.FromSlash(rel))), 0o755); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(filepath.Join(root, filepath.FromSlash(rel)), []byte(content), 0o644); err != nil {
		t.Fatal(err)
	}
}

// setStage 把单元 id 的阶段改为 stage（直接改 work.yaml，按任意嵌套缩进匹配该单元的 stage 行）。
func setStage(t *testing.T, root, id, stage string) {
	t.Helper()
	rel := work.Path("my-novel")
	data, err := os.ReadFile(filepath.Join(root, filepath.FromSlash(rel)))
	if err != nil {
		t.Fatal(err)
	}
	re := regexp.MustCompile(`(?m)^(\s*- id: ` + id + `\n\s*type: \w+\n\s*stage: )brief$`)
	if !re.Match(data) {
		t.Fatalf("work.yaml has no stage block for %s:\n%s", id, data)
	}
	write(t, root, rel, string(re.ReplaceAll(data, []byte("${1}"+stage))))
}

func has(ps []workspace.Problem, code, field string) bool {
	for _, p := range ps {
		if p.Code == code && (field == "" || p.Field == field) {
			return true
		}
	}
	return false
}

func TestAdd(t *testing.T) {
	root, w := newNovel(t)
	rel := Path("my-novel")
	e, changed, ps := Add(root, w, AddInput{Text: "灯芯的来历后文揭晓", PlantIn: "my-novel/scene-1", RecoverAt: "my-novel/chapter-two"})
	if len(ps) > 0 {
		t.Fatal(ps)
	}
	if e.ID != "thread-1" || e.Status != StatusOpen || !slices.Equal(changed, []string{rel}) {
		t.Fatalf("entry = %+v, changed = %v", e, changed)
	}
	// 第二条默认 id 递增；章或场景都可作为计划回收位置。
	e, _, ps = Add(root, w, AddInput{Text: "老灯匠的怀表", PlantIn: "my-novel/scene-1", RecoverAt: "my-novel/scene-2"})
	if len(ps) > 0 || e.ID != "thread-2" {
		t.Fatalf("second add: entry = %+v, ps = %+v", e, ps)
	}
	// 指定 id 重复时拒绝。
	if _, _, ps = Add(root, w, AddInput{Text: "重复", PlantIn: "my-novel/scene-1", RecoverAt: "my-novel/chapter-two", ID: "thread-1"}); !has(ps, "artifact.duplicate-id", "id") {
		t.Fatalf("duplicate id: %+v", ps)
	}
	// 埋设位置不是 scene 单元。
	if _, _, ps = Add(root, w, AddInput{Text: "章里埋", PlantIn: "my-novel/chapter-one", RecoverAt: "my-novel/chapter-two"}); !has(ps, CodeUnknownUnit, "plant-in") {
		t.Fatalf("plant-in chapter: %+v", ps)
	}
	// 计划回收位置不是本作品的单元。
	if _, _, ps = Add(root, w, AddInput{Text: "别处收", PlantIn: "my-novel/scene-1", RecoverAt: "my-novel/part-nine"}); !has(ps, CodeUnknownUnit, "recover-at") {
		t.Fatalf("recover-at unknown: %+v", ps)
	}
	// 计划回收位置按先序早于埋设场景。
	if _, _, ps = Add(root, w, AddInput{Text: "倒收", PlantIn: "my-novel/scene-2", RecoverAt: "my-novel/scene-1"}); !has(ps, CodeRecoverBeforePlant, "recover-at") {
		t.Fatalf("recover before plant: %+v", ps)
	}
	// 内容只含空白。
	if _, _, ps = Add(root, w, AddInput{Text: "  ", PlantIn: "my-novel/scene-1", RecoverAt: "my-novel/chapter-two"}); !has(ps, "input.invalid-value", "text") {
		t.Fatalf("blank text: %+v", ps)
	}
	// 被拒绝的写入不落盘：只有两条。
	_, f, _ := load(root, rel, w)
	if len(f.Entries) != 2 {
		t.Fatalf("entries = %d, want 2", len(f.Entries))
	}
}

func TestSet(t *testing.T) {
	root, w := newNovel(t)
	Add(root, w, AddInput{Text: "灯芯", PlantIn: "my-novel/scene-1", RecoverAt: "my-novel/scene-2", ID: "f-1"})
	// paid 必须同时给出回收场景。
	if _, _, ps := Set(root, w, "f-1", SetInput{Status: strPtr(StatusPaid)}); !has(ps, "input.invalid-value", "status") {
		t.Fatalf("paid without recovered-in: %+v", ps)
	}
	// 回收场景不是 scene 单元。
	if _, _, ps := Set(root, w, "f-1", SetInput{Status: strPtr(StatusPaid), RecoveredIn: strPtr("my-novel/part-one")}); !has(ps, CodeUnknownUnit, "recovered-in") {
		t.Fatalf("recovered-in part: %+v", ps)
	}
	e, changed, ps := Set(root, w, "f-1", SetInput{Status: strPtr(StatusPaid), RecoveredIn: strPtr("my-novel/scene-2")})
	if len(ps) > 0 || e.Status != StatusPaid || e.RecoveredIn == nil || *e.RecoveredIn != "my-novel/scene-2" {
		t.Fatalf("paid: entry = %+v, ps = %+v", e, ps)
	}
	if !slices.Equal(changed, []string{Path("my-novel")}) {
		t.Fatalf("changed = %v", changed)
	}
	// 改回 open 清除 recovered_in 与 decision。
	e, _, ps = Set(root, w, "f-1", SetInput{Status: strPtr(StatusOpen)})
	if len(ps) > 0 || e.RecoveredIn != nil || e.Decision != nil {
		t.Fatalf("back to open: entry = %+v, ps = %+v", e, ps)
	}
	// 计划回收位置改到埋设场景之前拒绝。
	if _, _, ps := Set(root, w, "f-1", SetInput{RecoverAt: strPtr("my-novel/part-one")}); !has(ps, CodeUnknownUnit, "recover-at") {
		t.Fatalf("recover-at part: %+v", ps)
	}
	// 条目不存在。
	if _, _, ps := Set(root, w, "f-9", SetInput{Text: strPtr("没有")}); !has(ps, CodeNotFound, "") {
		t.Fatalf("not-found: %+v", ps)
	}
	// 没有台账的作品。
	root2 := t.TempDir()
	workspace.Init(root2, nil)
	w2, _, _ := work.Create(root2, "novel", "other")
	if _, _, ps := Set(root2, w2, "f-1", SetInput{Text: strPtr("x")}); !has(ps, CodeNotFound, "") {
		t.Fatalf("no ledger: %+v", ps)
	}
}

func strPtr(v string) *string { return &v }

func TestDropGate(t *testing.T) {
	root, w := newNovel(t)
	Add(root, w, AddInput{Text: "灯芯", PlantIn: "my-novel/scene-1", RecoverAt: "my-novel/scene-2", ID: "f-1"})
	before, _ := os.ReadFile(filepath.Join(root, filepath.FromSlash(Path("my-novel"))))

	// 未授权的放弃：拒绝并生成阻塞决策，threads.yaml 不变。
	e, changed, ps := Set(root, w, "f-1", SetInput{Status: strPtr(StatusDropped)})
	if e != nil || !has(ps, CodeAuthorConfirmation, "f-1") {
		t.Fatalf("drop without decision: entry = %+v, ps = %+v", e, ps)
	}
	if !slices.Equal(changed, []string{decision.Path}) {
		t.Fatalf("changed = %v", changed)
	}
	if after, _ := os.ReadFile(filepath.Join(root, filepath.FromSlash(Path("my-novel")))); string(after) != string(before) {
		t.Fatalf("threads.yaml changed on refusal")
	}
	// 幂等：再次未授权尝试不重复追加决策。
	Set(root, w, "f-1", SetInput{Status: strPtr(StatusDropped)})
	ds, _, _ := decision.Load(root)
	n := 0
	for _, d := range ds {
		if d.Question == Question("f-1") {
			n++
		}
	}
	if n != 1 {
		t.Fatalf("blocking decisions = %d, want 1", n)
	}
	// 作者保持不变后仍拒绝，且不再追加。
	if _, ps := decision.Decide(root, "dec-1", "b", "作者：留着", "作者：留着这条伏笔", time.Time{}); len(ps) > 0 {
		t.Fatal(ps)
	}
	if _, _, ps := Set(root, w, "f-1", SetInput{Status: strPtr(StatusDropped)}); !has(ps, CodeAuthorConfirmation, "f-1") {
		t.Fatalf("drop after keep: %+v", ps)
	}
	if ds, _, _ = decision.Load(root); len(ds) != 1 {
		t.Fatalf("decisions = %d, want 1", len(ds))
	}
	// 新的允许决策授权写入，条目记录 decision。
	if _, ps := decision.Add(root, decision.Input{Question: Question("f-1"), Options: []string{"允许", "保持不变"}, Blocking: true, Units: []string{"my-novel"}}, func(ref string) []workspace.Problem { return nil }); len(ps) > 0 {
		t.Fatal(ps)
	}
	if _, ps := decision.Decide(root, "dec-2", "a", "作者：放弃", "作者：放弃这条伏笔", time.Time{}); len(ps) > 0 {
		t.Fatal(ps)
	}
	e, changed, ps = Set(root, w, "f-1", SetInput{Status: strPtr(StatusDropped), Decision: "dec-2"})
	if len(ps) > 0 || e.Status != StatusDropped || e.Decision == nil || *e.Decision != "dec-2" {
		t.Fatalf("drop with decision: entry = %+v, ps = %+v", e, ps)
	}
	if !slices.Equal(changed, []string{Path("my-novel")}) {
		t.Fatalf("changed = %v", changed)
	}
	// --decision 用在非放弃的修改上拒绝。
	if _, _, ps := Set(root, w, "f-1", SetInput{Text: strPtr("改"), Decision: "dec-2"}); !has(ps, "input.invalid-value", "decision") {
		t.Fatalf("decision on non-drop: %+v", ps)
	}
	// 各类不能授权的 --decision：不存在、不是为该条目生成、尚未决定。
	Add(root, w, AddInput{Text: "第二条", PlantIn: "my-novel/scene-1", RecoverAt: "my-novel/scene-2", ID: "f-2"})
	for _, tc := range []struct {
		decision, want string
	}{
		{"dec-99", "不存在"},
		{"dec-1", "不是为"},
	} {
		if _, _, ps := Set(root, w, "f-2", SetInput{Status: strPtr(StatusDropped), Decision: tc.decision}); !has(ps, CodeAuthorConfirmation, "f-2") || !strings.Contains(ps[0].Message, tc.want) {
			t.Fatalf("reject %s: %+v", tc.decision, ps)
		}
	}
	// 尚未决定的决策不能授权。
	if _, ps := decision.Add(root, decision.Input{Question: Question("f-2"), Options: []string{"允许", "保持不变"}, Blocking: true, Units: []string{"my-novel"}}, func(ref string) []workspace.Problem { return nil }); len(ps) > 0 {
		t.Fatal(ps)
	}
	if _, _, ps := Set(root, w, "f-2", SetInput{Status: strPtr(StatusDropped), Decision: "dec-3"}); !has(ps, CodeAuthorConfirmation, "f-2") || !strings.Contains(ps[0].Message, "尚未由作者决定") {
		t.Fatalf("undecided: %+v", ps)
	}
	// 决策不追加（给出 --decision 时不追加新的决策）。
	if ds, _, _ := decision.Load(root); len(ds) != 3 {
		t.Fatalf("decisions = %d, want 3", len(ds))
	}
}

// 状态互斥：dropped→paid 清除残留的 decision；paid→dropped 不保留 recovered_in（放弃重新授权后写入）。
func TestSetExclusiveFields(t *testing.T) {
	root, w := newNovel(t)
	Add(root, w, AddInput{Text: "灯芯", PlantIn: "my-novel/scene-1", RecoverAt: "my-novel/scene-2", ID: "f-1"})
	if _, ps := decision.Add(root, decision.Input{Question: Question("f-1"), Options: []string{"允许", "保持不变"}, Blocking: true, Units: []string{"my-novel"}}, func(ref string) []workspace.Problem { return nil }); len(ps) > 0 {
		t.Fatal(ps)
	}
	decision.Decide(root, "dec-1", "a", "作者：放弃", "作者确认", time.Time{})
	if e, _, ps := Set(root, w, "f-1", SetInput{Status: strPtr(StatusDropped), Decision: "dec-1"}); len(ps) > 0 || e.Decision == nil {
		t.Fatalf("dropped: %+v %+v", e, ps)
	}
	// dropped → paid：清掉 decision，记入 recovered_in。
	if e, _, ps := Set(root, w, "f-1", SetInput{Status: strPtr(StatusPaid), RecoveredIn: strPtr("my-novel/scene-2")}); len(ps) > 0 || e.Decision != nil || e.RecoveredIn == nil {
		t.Fatalf("dropped to paid: %+v %+v", e, ps)
	}
	// paid → dropped：清掉 recovered_in（须重新授权）。
	if _, _, ps := Set(root, w, "f-1", SetInput{Status: strPtr(StatusDropped)}); !has(ps, CodeAuthorConfirmation, "f-1") {
		t.Fatalf("paid to drop without decision: %+v", ps)
	}
	if _, ps := decision.Add(root, decision.Input{Question: Question("f-1"), Options: []string{"允许", "保持不变"}, Blocking: true, Units: []string{"my-novel"}}, func(ref string) []workspace.Problem { return nil }); len(ps) > 0 {
		t.Fatal(ps)
	}
	if _, ps := decision.Decide(root, "dec-2", "a", "作者：再放弃", "作者确认", time.Time{}); len(ps) > 0 {
		t.Fatal(ps)
	}
	if e, _, ps := Set(root, w, "f-1", SetInput{Status: strPtr(StatusDropped), Decision: "dec-2"}); len(ps) > 0 || e.RecoveredIn != nil {
		t.Fatalf("paid to dropped: %+v %+v", e, ps)
	}
	// 台账重读后状态互斥成立（Schema 保证）。
	_, f, ps := load(root, Path("my-novel"), w)
	if len(ps) > 0 || f.Entries[0].RecoveredIn != nil || f.Entries[0].Decision == nil {
		t.Fatalf("reloaded entry = %+v, ps = %+v", f.Entries[0], ps)
	}
}

// 还没有场景的空章可作为计划回收位置（自上而下建章后补场景）；早于埋设场景的空章仍拒绝。
func TestAddEmptyChapter(t *testing.T) {
	root, w := newNovel(t)
	work.AddUnit(root, "my-novel/part-one", "chapter", "chapter-late", work.Anchor{})
	w, ps := work.Load(root, "my-novel")
	if len(ps) > 0 {
		t.Fatal(ps)
	}
	if _, _, ps := Add(root, w, AddInput{Text: "后章空收", PlantIn: "my-novel/scene-1", RecoverAt: "my-novel/chapter-late"}); len(ps) > 0 {
		t.Fatalf("empty later chapter: %+v", ps)
	}
	work.AddUnit(root, "my-novel", "part", "part-zero", work.Anchor{Sibling: "part-one"})
	work.AddUnit(root, "my-novel/part-zero", "chapter", "chapter-early", work.Anchor{})
	w, ps = work.Load(root, "my-novel")
	if len(ps) > 0 {
		t.Fatal(ps)
	}
	if _, _, ps := Add(root, w, AddInput{Text: "前章空收", PlantIn: "my-novel/scene-1", RecoverAt: "my-novel/chapter-early"}); !has(ps, CodeRecoverBeforePlant, "recover-at") {
		t.Fatalf("empty earlier chapter: %+v", ps)
	}
}

// 其他条目已有的悬空引用不阻止登记新条目：Add 成功并以 warning 报告既有问题。
func TestAddLenientToDangling(t *testing.T) {
	root, w := newNovel(t)
	write(t, root, Path("my-novel"), "schema_version: 1\nentries:\n  - {id: f-x, text: 悬空, planted_in: nowhere/s9, planned_recover: my-novel/scene-2, status: open}\n")
	e, changed, ps := Add(root, w, AddInput{Text: "灯芯", PlantIn: "my-novel/scene-1", RecoverAt: "my-novel/scene-2", ID: "f-1"})
	if e == nil || !slices.Equal(changed, []string{Path("my-novel")}) {
		t.Fatalf("add = %+v, changed = %v", e, changed)
	}
	if !has(ps, CodeUnknownUnit, "entries[0].planted_in") || ps[0].Severity != workspace.SeverityWarning {
		t.Fatalf("warnings = %+v", ps)
	}
}

func TestListOverdue(t *testing.T) {
	root, w := newNovel(t)
	// a：计划回收于 scene-2；b：计划回收于 chapter-one；c：计划回收于 scene-3。
	Add(root, w, AddInput{Text: "a", PlantIn: "my-novel/scene-1", RecoverAt: "my-novel/scene-2", ID: "f-a"})
	Add(root, w, AddInput{Text: "b", PlantIn: "my-novel/scene-1", RecoverAt: "my-novel/chapter-one", ID: "f-b"})
	Add(root, w, AddInput{Text: "c", PlantIn: "my-novel/scene-2", RecoverAt: "my-novel/scene-3", ID: "f-c"})

	// 没有任何 done：都不超过。
	items, ps := List(root, w, false)
	if len(ps) > 0 || len(items) != 3 {
		t.Fatalf("items = %+v, ps = %+v", items, ps)
	}
	// 按 planted_in 先序排序：f-a、f-b（scene-1）先于 f-c（scene-2）。
	if items[0].ID != "f-a" || items[1].ID != "f-b" || items[2].ID != "f-c" {
		t.Fatalf("order = %+v", items)
	}

	// scene-1 done：计划回收位置为 scene-1 之后… f-b 的计划位置 chapter-one 在 scene-1 之前（先序），
	// 但 chapter-one 自身推导阶段不是 done（scene-2 未 done），子树外的 scene-3 也未 done → 不超过；
	// f-a、f-c 的计划位置都还没走到 → 不超过。
	setStage(t, root, "scene-1", "done")
	w, _ = work.Load(root, "my-novel")
	items, _ = List(root, w, false)
	for _, it := range items {
		if it.Overdue {
			t.Fatalf("after scene-1 done: %+v overdue", it)
		}
	}

	// scene-2 也 done：f-a 的计划回收场景（scene-2）已 done → 超过；chapter-one 推导为 done → f-b 也超过。
	setStage(t, root, "scene-2", "done")
	w, _ = work.Load(root, "my-novel")
	items, _ = List(root, w, false)
	byID := map[string]Item{}
	for _, it := range items {
		byID[it.ID] = it
	}
	if !byID["f-a"].Overdue || !byID["f-b"].Overdue || byID["f-c"].Overdue {
		t.Fatalf("after scene-2 done: %+v", items)
	}

	// paid 与 dropped 不在默认列表；--all 时都在且不标 overdue。
	Set(root, w, "f-a", SetInput{Status: strPtr(StatusPaid), RecoveredIn: strPtr("my-novel/scene-2")})
	items, _ = List(root, w, false)
	if len(items) != 2 {
		t.Fatalf("open items = %+v", items)
	}
	all, _ := List(root, w, true)
	if len(all) != 3 {
		t.Fatalf("all items = %+v", all)
	}
	for _, it := range all {
		if it.Status != StatusOpen && it.Overdue {
			t.Fatalf("non-open overdue: %+v", it)
		}
	}
}

// ContractProblems：recovers 引用未登记或晚埋、plants 引用异场景的伏笔时报告对应错误码；无台账按空台账检查。
func TestContractProblems(t *testing.T) {
	root, w := newNovel(t)
	s1 := ".writing-anything/works/my-novel/units/scene-1"
	s2 := ".writing-anything/works/my-novel/units/scene-2"
	write(t, root, outline.Path(s1), "schema_version: 1\nsections:\n  - {id: sc1, title: 灯下, plants: [f-1]}\n")
	write(t, root, outline.Path(s2), "schema_version: 1\nsections:\n  - {id: sc2, title: 收线, recovers: [f-9, f-2]}\n")

	// 没有台账：按空台账检查，plants 与 recovers 的每个引用都未登记。
	scenes := []outline.Scene{{Ref: "my-novel/scene-1", Dir: s1}, {Ref: "my-novel/scene-2", Dir: s2}}
	ps := ContractProblems(root, w, scenes)
	if len(ps) != 3 || !has(ps, CodeUnknownThread, "sections[0].plants[0]") ||
		!has(ps, CodeUnknownThread, "sections[0].recovers[0]") || !has(ps, CodeUnknownThread, "sections[0].recovers[1]") {
		t.Fatalf("no ledger: %+v", ps)
	}

	// 台账存在但不通过 Schema 校验：不做交叉检查（其问题由台账校验报告）。
	write(t, root, Path("my-novel"), "schema_version: 1\nentries:\n  - {id: f-1, status: closed}\n")
	if ps := ContractProblems(root, w, scenes); ps != nil {
		t.Fatalf("broken ledger: %+v", ps)
	}
	os.Remove(filepath.Join(root, filepath.FromSlash(Path("my-novel"))))

	// threads.yaml 途经符号链接：不按空台账检查；CheckWorks 计入 checked 并报告 artifact.not-found。
	target := filepath.Join(t.TempDir(), "threads.yaml")
	os.WriteFile(target, []byte("schema_version: 1\nentries: []\n"), 0o644)
	ledger := filepath.Join(root, filepath.FromSlash(Path("my-novel")))
	if err := os.Symlink(target, ledger); err == nil { // 不支持符号链接的平台跳过本段
		if ps := ContractProblems(root, w, scenes); ps != nil {
			t.Fatalf("symlinked ledger: %+v", ps)
		}
		if checked, ps := CheckWorks(root, []*work.Work{w}); !slices.Equal(checked, []string{Path("my-novel")}) ||
			len(ps) != 1 || ps[0].Code != "artifact.not-found" {
			t.Fatalf("symlinked ledger CheckWorks: checked = %v, ps = %+v", checked, ps)
		}
		os.Remove(ledger)
	}

	// 台账：f-1 埋在 scene-1，f-2 埋在 scene-2（比 recovers 它的 scene-2 晚不存在——同场景可以）；
	// f-9 未登记；f-3 埋在 scene-2，被 scene-2 之前没有问题；另埋 f-late 在 scene-2，被…（不存在此用例）。
	Add(root, w, AddInput{Text: "f-1", PlantIn: "my-novel/scene-1", RecoverAt: "my-novel/scene-2", ID: "f-1"})
	Add(root, w, AddInput{Text: "f-2", PlantIn: "my-novel/scene-3", RecoverAt: "my-novel/scene-3", ID: "f-2"})
	ps = ContractProblems(root, w, scenes)
	// f-9 未登记（threads.unknown-thread）；f-2 埋在 scene-3，按先序晚于 recovers 它的 scene-2（threads.recover-not-planted）。
	if len(ps) != 2 || !has(ps, CodeUnknownThread, "sections[0].recovers[0]") || !has(ps, CodeRecoverNotPlanted, "sections[0].recovers[1]") {
		t.Fatalf("contract problems = %+v", ps)
	}
	for _, p := range ps {
		if p.Path != outline.Path(s2) {
			t.Fatalf("path = %s, want %s", p.Path, outline.Path(s2))
		}
	}

	// plants 引用埋在别的场景的伏笔。
	write(t, root, outline.Path(s1), "schema_version: 1\nsections:\n  - {id: sc1, title: 灯下, plants: [f-2]}\n")
	write(t, root, outline.Path(s2), "schema_version: 1\nsections:\n  - {id: sc2, title: 收线, recovers: [f-1]}\n")
	ps = ContractProblems(root, w, scenes)
	if len(ps) != 1 || !has(ps, CodeUnknownThread, "sections[0].plants[0]") {
		t.Fatalf("plants wrong scene = %+v", ps)
	}

	// 契约合法时没有问题。
	write(t, root, outline.Path(s1), "schema_version: 1\nsections:\n  - {id: sc1, title: 灯下, plants: [f-1]}\n")
	write(t, root, outline.Path(s2), "schema_version: 1\nsections:\n  - {id: sc2, title: 收线, recovers: [f-1]}\n")
	if ps = ContractProblems(root, w, scenes); ps != nil {
		t.Fatalf("valid contracts: %+v", ps)
	}
}

// CheckWorks：台账的领域检查计入 checked；dropped 条目引用不存在的决策报告 threads.unknown-decision；
// 交叉检查与台账检查一起报告。没有台账的作品不做任何伏笔检查。
func TestCheckWorks(t *testing.T) {
	root, w := newNovel(t)
	Add(root, w, AddInput{Text: "灯芯", PlantIn: "my-novel/scene-1", RecoverAt: "my-novel/scene-2", ID: "f-1"})
	checked, ps := CheckWorks(root, []*work.Work{w})
	if len(ps) != 0 || !slices.Equal(checked, []string{Path("my-novel")}) {
		t.Fatalf("checked = %v, ps = %+v", checked, ps)
	}

	// 手写台账：dropped 条目引用不存在的决策。
	write(t, root, Path("my-novel"), "schema_version: 1\nentries:\n  - {id: f-1, text: 灯芯, planted_in: my-novel/scene-1, planned_recover: my-novel/scene-2, status: dropped, decision: dec-9}\n")
	_, ps = CheckWorks(root, []*work.Work{w})
	if !has(ps, CodeUnknownDecision, "entries[0].decision") {
		t.Fatalf("unknown decision: %+v", ps)
	}

	// 手写台账：planned_recover 引用别的作品/不存在单元与 recovers 交叉检查。
	s2 := ".writing-anything/works/my-novel/units/scene-2"
	write(t, root, Path("my-novel"), "schema_version: 1\nentries:\n  - {id: f-1, text: 灯芯, planted_in: my-novel/scene-1, planned_recover: other/scene-x, status: open}\n")
	write(t, root, outline.Path(s2), "schema_version: 1\nsections:\n  - {id: sc2, title: 收线, recovers: [f-9]}\n")
	_, ps = CheckWorks(root, []*work.Work{w})
	if !has(ps, CodeUnknownUnit, "entries[0].planned_recover") || !has(ps, CodeUnknownThread, "sections[0].recovers[0]") {
		t.Fatalf("mixed problems: %+v", ps)
	}

	// 没有台账的作品：不计入 checked；大纲没有 plants 与 recovers 时没有问题。
	root2 := t.TempDir()
	workspace.Init(root2, nil)
	w2, _, _ := work.Create(root2, "minimal", "my-post")
	if checked, ps = CheckWorks(root2, []*work.Work{w2}); len(checked) != 0 || len(ps) != 0 {
		t.Fatalf("no ledger: checked = %v, ps = %+v", checked, ps)
	}

	// 没有台账的长篇：不计入 checked，但 recovers 引用的伏笔按空台账报告 threads.unknown-thread。
	root3, w3 := newNovel(t)
	write(t, root3, outline.Path(s2), "schema_version: 1\nsections:\n  - {id: sc2, title: 收线, recovers: [f-1]}\n")
	checked, ps = CheckWorks(root3, []*work.Work{w3})
	if len(checked) != 0 || len(ps) != 1 || !has(ps, CodeUnknownThread, "sections[0].recovers[0]") || ps[0].Path != outline.Path(s2) {
		t.Fatalf("no ledger novel: checked = %v, ps = %+v", checked, ps)
	}
}

// CheckArtifact：单产物校验做同样的检查；所在作品加载不了时跳过单元检查。
func TestCheckArtifact(t *testing.T) {
	root, w := newNovel(t)
	_ = w
	rel := Path("my-novel")
	write(t, root, rel, "schema_version: 1\nentries:\n  - {id: f-1, text: 灯芯, planted_in: my-novel/scene-1, planned_recover: my-novel/scene-2, status: open}\n")
	if ps := CheckArtifact(root, rel); len(ps) != 0 {
		t.Fatalf("valid artifact: %+v", ps)
	}
	write(t, root, rel, "schema_version: 1\nentries:\n  - {id: f-1, text: 灯芯, planted_in: nowhere/s1, planned_recover: nowhere/s2, status: open}\n")
	if ps := CheckArtifact(root, rel); !has(ps, CodeUnknownUnit, "entries[0].planted_in") {
		t.Fatalf("unknown unit: %+v", ps)
	}
}
