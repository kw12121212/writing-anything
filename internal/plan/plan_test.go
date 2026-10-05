package plan

import (
	"time"
	"os"
	"path/filepath"
	"strings"
	"testing"

	"github.com/kw12121212/writing-anything/internal/canon"
	"github.com/kw12121212/writing-anything/internal/catalog"
	"github.com/kw12121212/writing-anything/internal/work"
	"github.com/kw12121212/writing-anything/internal/workspace"
)

// newNovel 初始化 novel 工作区并返回 snowflake 写作法。
func newNovel(t *testing.T) (string, *work.Work, *catalog.Method) {
	t.Helper()
	root := t.TempDir()
	if _, ps := workspace.Init(root, nil); len(ps) > 0 {
		t.Fatal(ps)
	}
	w, _, ps := work.Create(root, "novel", "my-novel")
	if len(ps) > 0 {
		t.Fatal(ps)
	}
	m, problems, ok := catalog.LoadMethod("snowflake")
	if !ok || m == nil || len(problems) > 0 {
		t.Fatalf("snowflake: %v %v", m, problems)
	}
	return root, w, m
}

func ensureNone() ([]string, []workspace.Problem) { return nil, nil }

func write(t *testing.T, root, rel, content string) {
	t.Helper()
	if err := os.MkdirAll(filepath.Dir(filepath.Join(root, filepath.FromSlash(rel))), 0o755); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(filepath.Join(root, filepath.FromSlash(rel)), []byte(content), 0o644); err != nil {
		t.Fatal(err)
	}
}

func read(t *testing.T, root, rel string) string {
	t.Helper()
	data, err := os.ReadFile(filepath.Join(root, filepath.FromSlash(rel)))
	if err != nil {
		t.Fatal(err)
	}
	return string(data)
}

func hasCode(ps []workspace.Problem, code string) *workspace.Problem {
	for i := range ps {
		if ps[i].Code == code {
			return &ps[i]
		}
	}
	return nil
}

func noPlanningDefault(w *work.Work, u *work.Unit) string {
	if u.Parent == nil {
		return w.Genre.DefaultMethod
	}
	return ""
}

// 越序写、写已确认步骤都报告 plan.step-order；只有当前步的草稿可以重写。
func TestWriteStepOrder(t *testing.T) {
	root, w, m := newNovel(t)
	unitDir := work.Dir("my-novel")
	f, changed, ps := WriteStep(root, w, unitDir, m, StepInput{Step: "one-sentence", Text: "一句话"}, ensureNone)
	if f == nil || len(ps) > 0 {
		t.Fatalf("first step: %v %v", f, ps)
	}
	if len(changed) != 1 {
		t.Fatalf("changed = %v", changed)
	}
	// 越过未确认的 one-sentence 写后面的步骤。
	_, _, ps = WriteStep(root, w, unitDir, m, StepInput{Step: "character-sketch", Text: "人物"}, ensureNone)
	if p := hasCode(ps, "plan.step-order"); p == nil || !strings.Contains(p.Message, "one-sentence") {
		t.Fatalf("skip: %v", ps)
	}
	// 步骤不在写作法中。
	_, _, ps = WriteStep(root, w, unitDir, m, StepInput{Step: "nope", Text: "x"}, ensureNone)
	if hasCode(ps, "plan.unknown-step") == nil {
		t.Fatalf("unknown step: %v", ps)
	}
	// 确认后：改写已确认步骤被拒绝，重确认被拒绝，当前步可以继续写。
	if _, _, ps = Confirm(root, w, unitDir, m, "one-sentence", "作者：可以", ensureNone, time.Date(2026, 6, 20, 12, 0, 0, 0, time.UTC)); len(ps) > 0 {
		t.Fatalf("confirm: %v", ps)
	}
	_, _, ps = WriteStep(root, w, unitDir, m, StepInput{Step: "one-sentence", Text: "重写"}, ensureNone)
	if hasCode(ps, "plan.step-order") == nil {
		t.Fatalf("confirmed immutable: %v", ps)
	}
	if _, _, ps = Confirm(root, w, unitDir, m, "one-sentence", "作者：再确认", ensureNone, time.Date(2026, 6, 20, 12, 0, 0, 0, time.UTC)); hasCode(ps, "plan.step-order") == nil {
		t.Fatalf("re-confirm: %v", ps)
	}
	if _, _, ps = WriteStep(root, w, unitDir, m, StepInput{Step: "one-paragraph", Text: "一段话"}, ensureNone); len(ps) > 0 {
		t.Fatalf("current write: %v", ps)
	}
	if _, _, ps = WriteStep(root, w, unitDir, m, StepInput{Step: "one-paragraph", Text: "重写的一段话"}, ensureNone); len(ps) > 0 {
		t.Fatalf("current rewrite: %v", ps)
	}
}

// 确认记录的强制与当前步限制：--confirmation 为空白报告 plan.incomplete-confirmation，当前步没有草稿报告
// plan.not-drafted（含 plan.yaml 还不存在的情况）。
func TestConfirmPreconditions(t *testing.T) {
	root, w, m := newNovel(t)
	unitDir := work.Dir("my-novel")
	if _, _, ps := Confirm(root, w, unitDir, m, "one-sentence", "   ", ensureNone, time.Date(2026, 6, 20, 12, 0, 0, 0, time.UTC)); hasCode(ps, "plan.incomplete-confirmation") == nil {
		t.Fatalf("blank confirmation: %v", ps)
	}
	_, _, ps := Confirm(root, w, unitDir, m, "one-sentence", "作者：好", ensureNone, time.Date(2026, 6, 20, 12, 0, 0, 0, time.UTC))
	if hasCode(ps, "plan.not-drafted") == nil {
		t.Fatalf("no plan yet: %v", ps)
	}
	if _, _, ps = WriteStep(root, w, unitDir, m, StepInput{Step: "one-sentence", Text: "一句话"}, ensureNone); len(ps) > 0 {
		t.Fatal(ps)
	}
	f, _, ps := Confirm(root, w, unitDir, m, "one-sentence", "作者：好", ensureNone, time.Date(2026, 6, 20, 12, 0, 0, 0, time.UTC))
	if len(ps) > 0 {
		t.Fatalf("confirm: %v", ps)
	}
	step := f.Steps[0]
	if !step.ConfirmedByAuthor || step.Confirmation == nil || *step.Confirmation != "作者：好" || step.ConfirmedAt == nil || !strings.HasSuffix(*step.ConfirmedAt, "Z") {
		t.Fatalf("confirmed step = %+v", step)
	}
	// 下一步没有草稿时确认报告 plan.not-drafted。
	if _, _, ps = Confirm(root, w, unitDir, m, "one-paragraph", "作者：好", ensureNone, time.Date(2026, 6, 20, 12, 0, 0, 0, time.UTC)); hasCode(ps, "plan.not-drafted") == nil {
		t.Fatalf("next not drafted: %v", ps)
	}
}

// 人物步骤的链接校验：--link 必须指向设定集中的 character 条目，确认时要求至少一个链接；
// 没有声明规划产物的步骤不接受 --link；--source 必须已登记。
func TestCharacterProductAndSources(t *testing.T) {
	root, w, m := newNovel(t)
	unitDir := work.Dir("my-novel")
	// 没有声明规划产物的当前步骤不接受 --link。
	_, _, ps := WriteStep(root, w, unitDir, m, StepInput{Step: "one-sentence", Text: "一句话", Links: []string{"canon-1"}}, ensureNone)
	if hasCode(ps, "input.invalid-value") == nil {
		t.Fatalf("link without product: %v", ps)
	}
	// 未登记素材。
	_, _, ps = WriteStep(root, w, unitDir, m, StepInput{Step: "one-sentence", Text: "一句话", Sources: []string{"src-x"}}, ensureNone)
	if p := hasCode(ps, "plan.unknown-source"); p == nil || p.Field != "sources[0]" {
		t.Fatalf("unknown source: %v", ps)
	}
	if _, _, ps = WriteStep(root, w, unitDir, m, StepInput{Step: "one-sentence", Text: "一句话"}, ensureNone); len(ps) > 0 {
		t.Fatal(ps)
	}
	if _, _, ps = Confirm(root, w, unitDir, m, "one-sentence", "作者：好", ensureNone, time.Date(2026, 6, 20, 12, 0, 0, 0, time.UTC)); len(ps) > 0 {
		t.Fatal(ps)
	}
	if _, _, ps = WriteStep(root, w, unitDir, m, StepInput{Step: "one-paragraph", Text: "一段话"}, ensureNone); len(ps) > 0 {
		t.Fatal(ps)
	}
	if _, _, ps = Confirm(root, w, unitDir, m, "one-paragraph", "作者：好", ensureNone, time.Date(2026, 6, 20, 12, 0, 0, 0, time.UTC)); len(ps) > 0 {
		t.Fatal(ps)
	}
	// 链接指向不存在的条目。
	_, _, ps = WriteStep(root, w, unitDir, m, StepInput{Step: "character-sketch", Text: "人物概要", Links: []string{"canon-9"}}, ensureNone)
	if p := hasCode(ps, "plan.unknown-link"); p == nil || p.Field != "links[0]" {
		t.Fatalf("unknown link: %v", ps)
	}
	// 人物卡经 canon add 落地后链接可用；没有链接时确认被拒（plan.missing-links）。
	if _, _, cps := canon.Add(root, w, canon.AddInput{Type: canon.TypeCharacter, Name: "守灯人", Text: "外冷内热"}); len(cps) > 0 {
		t.Fatal(cps)
	}
	if _, _, ps = WriteStep(root, w, unitDir, m, StepInput{Step: "character-sketch", Text: "人物概要"}, ensureNone); len(ps) > 0 {
		t.Fatal(ps)
	}
	_, _, ps = Confirm(root, w, unitDir, m, "character-sketch", "作者：好", ensureNone, time.Date(2026, 6, 20, 12, 0, 0, 0, time.UTC))
	if p := hasCode(ps, "plan.missing-links"); p == nil || p.Field != "links" {
		t.Fatalf("missing links: %v", ps)
	}
	if _, _, ps = WriteStep(root, w, unitDir, m, StepInput{Step: "character-sketch", Text: "人物概要", Links: []string{"canon-1"}}, ensureNone); len(ps) > 0 {
		t.Fatal(ps)
	}
	if _, _, ps = Confirm(root, w, unitDir, m, "character-sketch", "作者：好", ensureNone, time.Date(2026, 6, 20, 12, 0, 0, 0, time.UTC)); len(ps) > 0 {
		t.Fatalf("confirm with link: %v", ps)
	}
}

// 场景列表链接指向单元树中的场景单元，场景详述链接指向场景单元的大纲节且该节声明了契约字段。
func TestSceneProducts(t *testing.T) {
	root, w, m := newNovel(t)
	unitDir := work.Dir("my-novel")
	for _, s := range []string{"one-sentence", "one-paragraph", "character-sketch", "one-page-synopsis", "character-details", "four-page-synopsis", "character-bible"} {
		in := StepInput{Step: s, Text: s}
		switch s {
		case "character-sketch", "character-details", "character-bible":
			if _, _, ps := canon.Add(root, w, canon.AddInput{Type: canon.TypeCharacter, Name: "守灯人", Text: "外冷内热"}); len(ps) > 0 {
				t.Fatal(ps)
			}
			in.Links = []string{"canon-1"}
		}
		if _, _, ps := WriteStep(root, w, unitDir, m, in, ensureNone); len(ps) > 0 {
			t.Fatalf("step %s: %v", s, ps)
		}
		if _, _, ps := Confirm(root, w, unitDir, m, s, "作者：好", ensureNone, time.Date(2026, 6, 20, 12, 0, 0, 0, time.UTC)); len(ps) > 0 {
			t.Fatalf("confirm %s: %v", s, ps)
		}
	}
	for _, add := range []struct{ parent, typ, id string }{
		{"my-novel", "part", "part-one"},
		{"my-novel/part-one", "chapter", "chapter-one"},
		{"my-novel/chapter-one", "scene", "scene-1"},
	} {
		if _, _, ps := work.AddUnit(root, add.parent, add.typ, add.id, work.Anchor{}); len(ps) > 0 {
			t.Fatal(ps)
		}
	}
	// work.AddUnit 写入 work.yaml；重新加载后场景单元才在单元树中。
	if reloaded, ps := work.Load(root, "my-novel"); reloaded == nil || len(ps) > 0 {
		t.Fatal(ps)
	} else {
		w = reloaded
	}
	// 场景列表：链接必须是场景单元引用。
	_, _, ps := WriteStep(root, w, unitDir, m, StepInput{Step: "scene-list", Text: "场景表", Links: []string{"my-novel/part-one"}}, ensureNone)
	if hasCode(ps, "plan.unknown-link") == nil {
		t.Fatalf("non-scene link: %v", ps)
	}
	if _, _, ps = WriteStep(root, w, unitDir, m, StepInput{Step: "scene-list", Text: "场景表", Links: []string{"my-novel/scene-1"}}, ensureNone); len(ps) > 0 {
		t.Fatal(ps)
	}
	if _, _, ps = Confirm(root, w, unitDir, m, "scene-list", "作者：好", ensureNone, time.Date(2026, 6, 20, 12, 0, 0, 0, time.UTC)); len(ps) > 0 {
		t.Fatal(ps)
	}
	// 场景详述：链接形如 <场景单元引用>:<大纲节 id>，该节要声明契约字段。
	write(t, root, ".writing-anything/works/my-novel/units/scene-1/outline.yaml", "schema_version: 1\nsections:\n  - id: sc1\n    title: 灯下对峙\n    pov: 守灯人\n")
	for _, link := range []string{"my-novel/scene-1", "my-novel/scene-1:nope", "my-novel/part-one:sc1"} {
		if _, _, ps = WriteStep(root, w, unitDir, m, StepInput{Step: "scene-details", Text: "场景详述", Links: []string{link}}, ensureNone); hasCode(ps, "plan.unknown-link") == nil {
			t.Fatalf("link %s should not resolve: %v", link, ps)
		}
	}
	if _, _, ps = WriteStep(root, w, unitDir, m, StepInput{Step: "scene-details", Text: "场景详述", Links: []string{"my-novel/scene-1:sc1"}}, ensureNone); len(ps) > 0 {
		t.Fatal(ps)
	}
	if _, _, ps = Confirm(root, w, unitDir, m, "scene-details", "作者：好", ensureNone, time.Date(2026, 6, 20, 12, 0, 0, 0, time.UTC)); len(ps) > 0 {
		t.Fatal(ps)
	}
}

// --redo 重置已确认的步骤并删除其后全部记录；对未确认的步骤报告 plan.not-confirmed，缺确认参数报告
// plan.incomplete-confirmation。
func TestRedo(t *testing.T) {
	root, w, m := newNovel(t)
	unitDir := work.Dir("my-novel")
	for _, s := range []string{"one-sentence", "one-paragraph"} {
		if _, _, ps := WriteStep(root, w, unitDir, m, StepInput{Step: s, Text: s}, ensureNone); len(ps) > 0 {
			t.Fatal(ps)
		}
		if _, _, ps := Confirm(root, w, unitDir, m, s, "作者：好", ensureNone, time.Date(2026, 6, 20, 12, 0, 0, 0, time.UTC)); len(ps) > 0 {
			t.Fatal(ps)
		}
	}
	if _, _, ps := WriteStep(root, w, unitDir, m, StepInput{Step: "character-sketch", Text: "人物概要"}, ensureNone); len(ps) > 0 {
		t.Fatal(ps)
	}
	// 未确认的步骤给出 --redo。
	_, _, ps := WriteStep(root, w, unitDir, m, StepInput{Step: "character-sketch", Text: "重写", Redo: true, AuthorConfirmed: true, Confirmation: "作者：改"}, ensureNone)
	if hasCode(ps, "plan.not-confirmed") == nil {
		t.Fatalf("redo unconfirmed: %v", ps)
	}
	_, _, ps = WriteStep(root, w, unitDir, m, StepInput{Step: "one-paragraph", Text: "重写", Redo: true, AuthorConfirmed: true}, ensureNone)
	if hasCode(ps, "plan.incomplete-confirmation") == nil {
		t.Fatalf("redo without confirmation: %v", ps)
	}
	// 重置 one-sentence：它变为未确认草稿，其后全部记录被删除。
	f, changed, ps := WriteStep(root, w, unitDir, m, StepInput{Step: "one-sentence", Text: "新的一句话", Redo: true, AuthorConfirmed: true, Confirmation: "作者：前提要改"}, ensureNone)
	if len(ps) > 0 {
		t.Fatal(ps)
	}
	if len(changed) != 1 || len(f.Steps) != 1 || f.Steps[0].ID != "one-sentence" || f.Steps[0].ConfirmedByAuthor {
		t.Fatalf("redo result = %+v, changed = %v", f, changed)
	}
	if cur := CurrentStep(f, m); cur != 0 {
		t.Fatalf("current = %d", cur)
	}
	data := read(t, root, ".writing-anything/works/my-novel/plan.yaml")
	if strings.Count(data, "- id:") != 1 || strings.Contains(data, "one-paragraph") {
		t.Fatalf("plan.yaml after redo:\n%s", data)
	}
}

// 手写 plan.yaml：步骤记录不是前缀、已确认步骤排在未确认之后、悬空素材与链接时 CheckUnitArtifacts 报告
// 对应错误码；悬空引用不阻止修改其他步骤（cross-artifact-references）。
func TestCheckOrderHandwritten(t *testing.T) {
	root, w, m := newNovel(t)
	unitDir := work.Dir("my-novel")
	rel := ".writing-anything/works/my-novel/plan.yaml"
	methods := catalog.LoadMethods(root)
	write(t, root, rel, "schema_version: 1\nmethod: snowflake\nsteps:\n  - id: one-paragraph\n    title: 一段话概括\n    text: x\n    confirmed_by_author: false\n")
	if _, ps := CheckUnitArtifacts(root, []*work.Work{w}, methods, noPlanningDefault); hasCode(ps, "plan.step-order") == nil {
		t.Fatalf("skip first: %v", ps)
	}
	write(t, root, rel, "schema_version: 1\nmethod: snowflake\nsteps:\n  - id: one-sentence\n    title: 一句话概括\n    text: x\n    confirmed_by_author: false\n  - id: one-paragraph\n    title: 一段话概括\n    text: x\n    confirmed_by_author: true\n    confirmation: 作者：好\n    confirmed_at: 2026-10-03T08:00:00Z\n")
	_, ps := CheckUnitArtifacts(root, []*work.Work{w}, methods, noPlanningDefault)
	if p := hasCode(ps, "plan.step-order"); p == nil || p.Field != "steps[1]" {
		t.Fatalf("confirmed after drafted: %v", ps)
	}
	write(t, root, rel, "schema_version: 1\nmethod: snowflake\nsteps:\n  - id: one-sentence\n    title: 一句话概括\n    text: x\n    sources: [src-x]\n    confirmed_by_author: false\n")
	_, ps = CheckUnitArtifacts(root, []*work.Work{w}, methods, noPlanningDefault)
	if p := hasCode(ps, "plan.unknown-source"); p == nil || p.Field != "steps[0].sources[0]" {
		t.Fatalf("unknown source: %v", ps)
	}
	// 悬空引用不阻止重写该步骤（成功时以 warning 报告）。
	if _, _, ps := WriteStep(root, w, unitDir, m, StepInput{Step: "one-sentence", Text: "重写"}, ensureNone); len(ps) != 1 || ps[0].Severity != workspace.SeverityWarning {
		t.Fatalf("rewrite with dangling: %v", ps)
	}
}

// 写入保留 plan.yaml 中的其他内容与 YAML 注释（workspace-artifacts/structured-edits）。
func TestWriteKeepsComments(t *testing.T) {
	root, w, m := newNovel(t)
	unitDir := work.Dir("my-novel")
	write(t, root, ".writing-anything/works/my-novel/plan.yaml", "# 作者的话：前提再想想\nschema_version: 1\nmethod: snowflake\nsteps:\n  - id: one-sentence # 第一步\n    title: 一句话概括\n    text: 旧的一句话\n    confirmed_by_author: false\n")
	if _, _, ps := WriteStep(root, w, unitDir, m, StepInput{Step: "one-sentence", Text: "新的一句话"}, ensureNone); len(ps) > 0 {
		t.Fatal(ps)
	}
	data := read(t, root, ".writing-anything/works/my-novel/plan.yaml")
	for _, want := range []string{"# 作者的话：前提再想想", "one-sentence # 第一步", "新的一句话"} {
		if !strings.Contains(data, want) {
			t.Fatalf("plan.yaml missing %q:\n%s", want, data)
		}
	}
}

// steps 序列经由锚点得到时拒绝写入（plan.anchored-value）。
func TestAnchoredValue(t *testing.T) {
	root, w, m := newNovel(t)
	write(t, root, ".writing-anything/works/my-novel/plan.yaml", "schema_version: 1\nmethod: snowflake\nsteps: &s\n  - id: one-sentence\n    title: 一句话概括\n    text: 旧的一句话\n    confirmed_by_author: false\n")
	if _, _, ps := WriteStep(root, w, work.Dir("my-novel"), m, StepInput{Step: "one-sentence", Text: "新的一句话"}, ensureNone); hasCode(ps, "plan.anchored-value") == nil {
		t.Fatalf("anchored: %v", ps)
	}
}

// 已确认的步骤缺非空 confirmation 或 confirmed_at 时整体校验报告 plan.incomplete-confirmation；
// 未确认的步骤写 null 的确认字段是合法产物（checkConfirmations）。
func TestCheckConfirmations(t *testing.T) {
	root, w, m := newNovel(t)
	rel := ".writing-anything/works/my-novel/plan.yaml"
	methods := catalog.LoadMethods(root)
	// 缺 confirmation 与 confirmed_at。
	write(t, root, rel, "schema_version: 1\nmethod: snowflake\nsteps:\n  - id: one-sentence\n    title: 一句话概括\n    text: x\n    confirmed_by_author: true\n")
	_, ps := CheckUnitArtifacts(root, []*work.Work{w}, methods, noPlanningDefault)
	if p := hasCode(ps, "plan.incomplete-confirmation"); p == nil || p.Field != "steps[0]" {
		t.Fatalf("missing both: %v", ps)
	}
	// 只缺 confirmed_at。
	write(t, root, rel, "schema_version: 1\nmethod: snowflake\nsteps:\n  - id: one-sentence\n    title: 一句话概括\n    text: x\n    confirmed_by_author: true\n    confirmation: 作者：好\n")
	_, ps = CheckUnitArtifacts(root, []*work.Work{w}, methods, noPlanningDefault)
	if p := hasCode(ps, "plan.incomplete-confirmation"); p == nil || p.Field != "steps[0]" {
		t.Fatalf("missing confirmed_at: %v", ps)
	}
	// 未确认的步骤写 null 的确认字段：合法。
	write(t, root, rel, "schema_version: 1\nmethod: snowflake\nsteps:\n  - id: one-sentence\n    title: 一句话概括\n    text: x\n    confirmed_by_author: false\n    confirmation: null\n    confirmed_at: null\n")
	if _, ps = CheckUnitArtifacts(root, []*work.Work{w}, methods, noPlanningDefault); len(ps) != 0 {
		t.Fatalf("null confirmation on drafted step: %v", ps)
	}
	_ = m
}

// 写作法变更后写入报告 plan.method-changed；单元选用的写作法不是规划类时 CheckUnitArtifacts 报告
// plan.not-planning。
func TestMethodChangedAndNotPlanning(t *testing.T) {
	root, w, m := newNovel(t)
	unitDir := work.Dir("my-novel")
	rel := ".writing-anything/works/my-novel/plan.yaml"
	if _, _, ps := WriteStep(root, w, unitDir, m, StepInput{Step: "one-sentence", Text: "一句话"}, ensureNone); len(ps) > 0 {
		t.Fatal(ps)
	}
	write(t, root, rel, strings.Replace(read(t, root, rel), "method: snowflake", "method: other-plan", 1))
	if _, _, ps := WriteStep(root, w, unitDir, m, StepInput{Step: "one-sentence", Text: "再写"}, ensureNone); hasCode(ps, "plan.method-changed") == nil {
		t.Fatalf("method changed: %v", ps)
	}
	// 写作简报把 method 设为 none：根单元不再选用规划类写作法，已存在的 plan.yaml 报告 plan.not-planning。
	write(t, root, ".writing-anything/works/my-novel/brief.yaml", "schema_version: 1\nmethod: none\nmessages: []\n")
	if _, ps := CheckUnitArtifacts(root, []*work.Work{w}, catalog.LoadMethods(root), noPlanningDefault); hasCode(ps, "plan.not-planning") == nil {
		t.Fatalf("not planning: %v", ps)
	}
}
