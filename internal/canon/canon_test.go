package canon

import (
	"os"
	"path/filepath"
	"slices"
	"strings"
	"testing"
	"time"

	"github.com/kw12121212/writing-anything/internal/decision"
	"github.com/kw12121212/writing-anything/internal/source"
	"github.com/kw12121212/writing-anything/internal/work"
	"github.com/kw12121212/writing-anything/internal/workspace"
)

// newWork 初始化工作区并创建作品 my-post（canon-add/add-character）。
func newWork(t *testing.T) (string, *work.Work) {
	t.Helper()
	root := t.TempDir()
	if _, ps := workspace.Init(root, nil); len(ps) > 0 {
		t.Fatal(ps)
	}
	if _, _, ps := work.Create(root, "minimal", "my-post"); len(ps) > 0 {
		t.Fatal(ps)
	}
	w, ps := work.Load(root, "my-post")
	if len(ps) > 0 {
		t.Fatal(ps)
	}
	return root, w
}

func addSource(t *testing.T, root, id string) {
	t.Helper()
	if _, _, ps := source.Add(root, []byte("原文 "+id), "interview", "", id, ""); len(ps) > 0 {
		t.Fatal(ps)
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

func write(t *testing.T, root, rel, content string) {
	t.Helper()
	if err := os.WriteFile(filepath.Join(root, filepath.FromSlash(rel)), []byte(content), 0o644); err != nil {
		t.Fatal(err)
	}
}

func problemsContain(ps []workspace.Problem, code, field string) bool {
	for _, p := range ps {
		if p.Code == code && (field == "" || p.Field == field) {
			return true
		}
	}
	return false
}

func TestAdd(t *testing.T) {
	root, w := newWork(t)
	addSource(t, root, "author-notes")
	rel := ".writing-anything/works/my-post/canon.yaml"
	e, changed, ps := Add(root, w, AddInput{Type: TypeCharacter, Name: "守灯人", Text: "五十岁，左手有旧伤", Sources: []string{"author-notes"}})
	if len(ps) > 0 {
		t.Fatal(ps)
	}
	if e.ID != "canon-1" || e.Type != TypeCharacter || e.Name == nil || *e.Name != "守灯人" || !slices.Equal(e.Sources, []string{"author-notes"}) || e.Protection != ProtectionNormal {
		t.Fatalf("entry = %+v", e)
	}
	if !slices.Equal(changed, []string{rel}) {
		t.Fatalf("changed = %v", changed)
	}
	// 作者的注释保留；第二条默认 id 递增。
	write(t, root, rel, "# 作者的注释\n"+read(t, root, rel))
	if e, _, ps = Add(root, w, AddInput{Type: TypeCharacter, Name: "精灵", Text: "只在夜里出现"}); len(ps) > 0 || e.ID != "canon-2" {
		t.Fatalf("second = %+v, %v", e, ps)
	}
	// 人物关系引用同文件的人物条目（canon-add/add-relationship）。
	e, _, ps = Add(root, w, AddInput{Type: TypeRelationship, Text: "守灯人是精灵的旧识", Members: []string{"canon-1", "canon-2"}})
	if len(ps) > 0 || e.ID != "canon-3" || !slices.Equal(e.Members, []string{"canon-1", "canon-2"}) || e.Name != nil {
		t.Fatalf("relationship = %+v, %v", e, ps)
	}
	// 钦定条目（canon-add/add-pinned）：世界规则没有 name。
	e, _, ps = Add(root, w, AddInput{Type: TypeWorldRule, Text: "灯油只能燃烧到黎明", Pinned: true})
	if len(ps) > 0 || e.Protection != ProtectionPinned || e.Name != nil {
		t.Fatalf("pinned = %+v, %v", e, ps)
	}
	if text := read(t, root, rel); !strings.Contains(text, "# 作者的注释") || !strings.Contains(text, "protection: author-pinned") {
		t.Errorf("canon.yaml:\n%s", text)
	}
	if ps := CheckArtifact(root, rel); len(ps) > 0 {
		t.Fatalf("CheckArtifact = %v", ps)
	}
}

func TestAddRejects(t *testing.T) {
	root, w := newWork(t)
	addSource(t, root, "author-notes")
	rel := ".writing-anything/works/my-post/canon.yaml"
	if _, _, ps := Add(root, w, AddInput{Type: TypeCharacter, Name: "守灯人", Text: "描述"}); len(ps) > 0 {
		t.Fatal(ps)
	}
	before := read(t, root, rel)
	for _, tc := range []struct {
		name string
		in   AddInput
		code string
	}{
		{"unknown source", AddInput{Type: TypeCharacter, Name: "甲", Text: "x", Sources: []string{"nope"}}, CodeUnknownSource},
		{"bad type", AddInput{Type: "person", Name: "甲", Text: "x"}, "artifact.schema-violation"},
		{"missing name", AddInput{Type: TypeCharacter, Text: "x"}, "artifact.schema-violation"},
		{"name on world-rule", AddInput{Type: TypeWorldRule, Name: "规则", Text: "x"}, "artifact.schema-violation"},
		{"empty text", AddInput{Type: TypeWorldRule, Text: " "}, "artifact.schema-violation"},
		{"short members", AddInput{Type: TypeRelationship, Text: "x", Members: []string{"canon-1"}}, "artifact.schema-violation"},
		{"duplicate id", AddInput{Type: TypeWorldRule, Text: "x", ID: "canon-1"}, "artifact.duplicate-id"},
		{"bad id", AddInput{Type: TypeWorldRule, Text: "x", ID: "Can_on"}, "artifact.schema-violation"},
	} {
		if _, _, ps := Add(root, w, tc.in); !problemsContain(ps, tc.code, "") {
			t.Errorf("%s: problems = %+v, want %s", tc.name, ps, tc.code)
		}
	}
	if read(t, root, rel) != before {
		t.Error("canon.yaml changed on rejected add")
	}
}

func TestAddRelationshipUnknownMember(t *testing.T) {
	root, w := newWork(t)
	if _, _, ps := Add(root, w, AddInput{Type: TypeCharacter, Name: "守灯人", Text: "x"}); len(ps) > 0 {
		t.Fatal(ps)
	}
	_, _, ps := Add(root, w, AddInput{Type: TypeRelationship, Text: "对立", Members: []string{"canon-1", "canon-9"}})
	if !problemsContain(ps, CodeUnknownCharacter, "members[1]") {
		t.Fatalf("problems = %+v", ps)
	}
}

func TestSetAndRemove(t *testing.T) {
	root, w := newWork(t)
	addSource(t, root, "note")
	rel := ".writing-anything/works/my-post/canon.yaml"
	mk := func(t *testing.T) {
		t.Helper()
		write(t, root, rel, "# 作者的注释\nschema_version: 1\nentries:\n  - {id: canon-1, type: character, name: 守灯人, text: 五十岁, sources: [], protection: normal}\n"+
			"  - {id: canon-2, type: character, name: 精灵, text: 夜行, sources: [note], protection: normal}\n"+
			"  - {id: canon-3, type: relationship, text: 旧识, sources: [], protection: normal, members: [canon-1, canon-2]}\n")
	}
	text := "六十岁，左手有旧伤"
	t.Run("set-text", func(t *testing.T) {
		mk(t)
		e, changed, ps := Set(root, w, "canon-1", SetInput{Text: &text})
		if len(ps) > 0 || e.Text != text || !slices.Equal(changed, []string{rel}) {
			t.Fatalf("entry = %+v, changed = %v, %v", e, changed, ps)
		}
		if s := read(t, root, rel); !strings.Contains(s, "# 作者的注释") || !strings.Contains(s, "六十岁") {
			t.Errorf("canon.yaml:\n%s", s)
		}
	})
	t.Run("set-unchanged", func(t *testing.T) {
		mk(t)
		before := read(t, root, rel)
		e, changed, ps := Set(root, w, "canon-1", SetInput{Text: ptr("五十岁")})
		if len(ps) > 0 || changed != nil || e.Text != "五十岁" {
			t.Fatalf("entry = %+v, changed = %v, %v", e, changed, ps)
		}
		if read(t, root, rel) != before {
			t.Error("unchanged set rewrote canon.yaml")
		}
	})
	t.Run("set-sources-and-members", func(t *testing.T) {
		mk(t)
		srcs := []string{"note"}
		e, _, ps := Set(root, w, "canon-3", SetInput{Sources: &srcs})
		if len(ps) > 0 || !slices.Equal(e.Sources, []string{"note"}) {
			t.Fatalf("sources = %+v, %v", e, ps)
		}
		// members 整体替换；少于 2 个成员由 Schema 拒绝；空切片删除该字段。
		if _, _, ps = Set(root, w, "canon-3", SetInput{Members: ptr([]string{"canon-2"})}); !problemsContain(ps, "artifact.schema-violation", "members") {
			t.Fatalf("short members = %+v", ps)
		}
		if e, _, ps = Set(root, w, "canon-3", SetInput{Members: ptr([]string{"canon-2", "canon-1"})}); len(ps) > 0 || !slices.Equal(e.Members, []string{"canon-2", "canon-1"}) {
			t.Fatalf("members = %+v, %v", e, ps)
		}
		// 类型改为 world-rule 后 --members 空值删除该字段。
		if e, _, ps = Set(root, w, "canon-3", SetInput{Type: ptr(TypeWorldRule), Members: &[]string{}}); len(ps) > 0 || e.Type != TypeWorldRule || len(e.Members) != 0 {
			t.Fatalf("retype = %+v, %v", e, ps)
		}
		if ps := CheckArtifact(root, rel); len(ps) > 0 {
			t.Fatalf("CheckArtifact = %v", ps)
		}
	})
	t.Run("not-found", func(t *testing.T) {
		mk(t)
		if _, _, ps := Set(root, w, "canon-9", SetInput{Text: &text}); !problemsContain(ps, CodeNotFound, "") {
			t.Fatalf("set = %+v", ps)
		}
		if _, _, ps := Remove(root, w, "canon-9", ""); !problemsContain(ps, CodeNotFound, "") {
			t.Fatalf("remove = %+v", ps)
		}
	})
	t.Run("remove-entry", func(t *testing.T) {
		mk(t)
		ok, changed, ps := Remove(root, w, "canon-2", "")
		// 删除 canon-2 后 canon-3 的 members 悬空：照常删除，以 warning 报告（canon-edit/remove-referenced-character）。
		if !ok || !slices.Equal(changed, []string{rel}) || !problemsContain(ps, CodeUnknownCharacter, "entries[1].members[1]") || ps[0].Severity != workspace.SeverityWarning {
			t.Fatalf("ok = %v, changed = %v, ps = %+v", ok, changed, ps)
		}
		if s := read(t, root, rel); !strings.Contains(s, "# 作者的注释") || strings.Contains(s, "精灵") {
			t.Errorf("canon.yaml:\n%s", s)
		}
	})
	t.Run("remove-referenced-character", func(t *testing.T) {
		mk(t)
		ok, _, ps := Remove(root, w, "canon-1", "")
		if !ok || !problemsContain(ps, CodeUnknownCharacter, "entries[1].members[0]") {
			t.Fatalf("ok = %v, problems = %+v", ok, ps)
		}
		if ps[0].Severity != workspace.SeverityWarning {
			t.Errorf("severity = %s", ps[0].Severity)
		}
	})
	t.Run("anchored", func(t *testing.T) {
		write(t, root, rel, "schema_version: 1\nentries: &anchor\n  - {id: canon-1, type: world-rule, text: 规则, sources: [], protection: normal}\n")
		if _, _, ps := Set(root, w, "canon-1", SetInput{Text: &text}); !problemsContain(ps, CodeAnchoredValue, "") {
			t.Fatalf("set anchored = %+v", ps)
		}
		if _, _, ps := Remove(root, w, "canon-1", ""); !problemsContain(ps, CodeAnchoredValue, "") {
			t.Fatalf("remove anchored = %+v", ps)
		}
		if _, _, ps := Add(root, w, AddInput{Type: TypeWorldRule, Text: "x"}); !problemsContain(ps, CodeAnchoredValue, "") {
			t.Fatalf("add anchored = %+v", ps)
		}
	})
}

func ptr[T any](v T) *T { return &v }

func addPinned(t *testing.T, root string, w *work.Work) Entry {
	t.Helper()
	e, _, ps := Add(root, w, AddInput{Type: TypeCharacter, Name: "守灯人", Text: "五十岁", Pinned: true})
	if len(ps) > 0 {
		t.Fatal(ps)
	}
	return *e
}

func TestPinnedProtection(t *testing.T) {
	root, w := newWork(t)
	e := addPinned(t, root, w)
	rel := ".writing-anything/works/my-post/canon.yaml"
	before := read(t, root, rel)
	text := "新描述"

	// 拒绝修改并自动生成阻塞决策（canon-protection/refuse-modify-pinned）。
	_, changed, ps := Set(root, w, e.ID, SetInput{Text: &text})
	if !problemsContain(ps, CodeAuthorPinned, e.ID) {
		t.Fatalf("problems = %+v", ps)
	}
	if p := ps[0]; p.Path != rel || !strings.Contains(p.Message, "作者钦定") {
		t.Fatalf("problem = %+v", p)
	}
	if !slices.Equal(changed, []string{decision.Path}) {
		t.Fatalf("changed = %v", changed)
	}
	if read(t, root, rel) != before {
		t.Error("canon.yaml changed on refused set")
	}
	ds, _, err := decision.Load(root)
	if len(err) > 0 || len(ds) != 1 {
		t.Fatalf("decisions = %+v, %v", ds, err)
	}
	d := ds[0]
	if d.ID != "dec-1" || d.Question != Question(OpModify, e) || d.Question != "是否允许修改作者钦定的设定条目 canon-1（守灯人）？" {
		t.Fatalf("question = %q", d.Question)
	}
	if len(d.Options) != 2 || d.Options[0].ID != "a" || d.Options[0].Text != "允许" || d.Options[1].ID != "b" || d.Options[1].Text != "保持不变" {
		t.Fatalf("options = %+v", d.Options)
	}
	if d.Recommendation == nil || *d.Recommendation != "b" || !d.Blocking || !slices.Equal(d.Units, []string{"my-post"}) || d.Status != decision.StatusOpen {
		t.Fatalf("decision = %+v", d)
	}

	// 重复拒绝幂等：不重复追加决策（canon-protection/idempotent-decision）。
	_, changed, ps = Set(root, w, e.ID, SetInput{Text: &text})
	if !problemsContain(ps, CodeAuthorPinned, "") || changed != nil {
		t.Fatalf("problems = %+v, changed = %v", ps, changed)
	}
	if ds, _, _ = decision.Load(root); len(ds) != 1 {
		t.Fatalf("decisions = %d, want 1", len(ds))
	}

	// 阻塞决策阻塞作品根单元的推进（canon-protection/blocks-advance）。
	if blockers := decision.BlockingIn(mustLoad(t, root), "my-post"); len(blockers) != 1 || blockers[0].ID != "dec-1" {
		t.Fatalf("blockers = %+v", blockers)
	}

	// 作者选择保持不变后，携带该决策重试仍被拒绝（canon-protection/keep-choice-refuses）。
	if _, ps := decision.Decide(root, "dec-1", "b", "", "作者决定不改", time.Time{}); len(ps) > 0 {
		t.Fatal(ps)
	}
	_, changed, ps = Set(root, w, e.ID, SetInput{Text: &text, Decision: "dec-1"})
	if !problemsContain(ps, CodeAuthorPinned, "") || changed != nil {
		t.Fatalf("keep-choice = %+v", ps)
	}
	if read(t, root, rel) != before {
		t.Error("canon.yaml changed after keep choice")
	}
}

func mustLoad(t *testing.T, root string) []decision.Decision {
	t.Helper()
	ds, _, ps := decision.Load(root)
	if len(ps) > 0 {
		t.Fatal(ps)
	}
	return ds
}

func TestPinnedAuthorize(t *testing.T) {
	root, w := newWork(t)
	e := addPinned(t, root, w)
	rel := ".writing-anything/works/my-post/canon.yaml"
	if _, _, ps := Set(root, w, e.ID, SetInput{Text: ptr("x")}); len(ps) == 0 {
		t.Fatal("expected refusal to create decision")
	}
	if _, ps := decision.Decide(root, "dec-1", "a", "", "作者同意这次修改", time.Time{}); len(ps) > 0 {
		t.Fatal(ps)
	}
	// 决定允许后，携带 --decision 重试成功并记录决策 id（canon-protection/retry-after-decide）。
	got, changed, ps := Set(root, w, e.ID, SetInput{Text: ptr("新描述"), Decision: "dec-1"})
	if len(ps) > 0 {
		t.Fatal(ps)
	}
	if got.Text != "新描述" || got.Protection != ProtectionPinned || got.Decision == nil || *got.Decision != "dec-1" {
		t.Fatalf("entry = %+v", got)
	}
	if !slices.Equal(changed, []string{rel}) {
		t.Fatalf("changed = %v", changed)
	}
	if s := read(t, root, rel); !strings.Contains(s, "decision: dec-1") {
		t.Errorf("canon.yaml:\n%s", s)
	}
	// --decision 指向不存在的决策：仍拒绝。
	_, changed, ps = Set(root, w, e.ID, SetInput{Text: ptr("再改"), Decision: "dec-9"})
	if !problemsContain(ps, CodeAuthorPinned, "") || changed != nil {
		t.Fatalf("unknown decision = %+v", ps)
	}
}

func TestPinnedRemove(t *testing.T) {
	root, w := newWork(t)
	e, _, ps := Add(root, w, AddInput{Type: TypeWorldRule, Text: "灯油只能燃烧到黎明", Pinned: true})
	if len(ps) > 0 {
		t.Fatal(ps)
	}
	// 拒绝删除并生成决策（canon-protection/refuse-remove-pinned；无 name 的问题文本省略括号）。
	ok, changed, ps := Remove(root, w, e.ID, "")
	if ok || !problemsContain(ps, CodeAuthorPinned, "") || !slices.Equal(changed, []string{decision.Path}) {
		t.Fatalf("ok = %v, changed = %v, ps = %+v", ok, changed, ps)
	}
	if q := Question(OpRemove, *e); q != "是否允许删除作者钦定的设定条目 canon-1？" {
		t.Fatalf("question = %q", q)
	}
	if _, ps := decision.Decide(root, "dec-1", "a", "", "作者同意删除", time.Time{}); len(ps) > 0 {
		t.Fatal(ps)
	}
	if ok, changed, ps = Remove(root, w, e.ID, "dec-1"); !ok || len(ps) > 0 || !slices.Equal(changed, []string{Path("my-post")}) {
		t.Fatalf("ok = %v, changed = %v, ps = %+v", ok, changed, ps)
	}
}

func TestPinUpgrade(t *testing.T) {
	root, w := newWork(t)
	if _, _, ps := Add(root, w, AddInput{Type: TypeWorldRule, Text: "规则"}); len(ps) > 0 {
		t.Fatal(ps)
	}
	// normal 升为 author-pinned 只加强保护，不需要决策。
	e, changed, ps := Set(root, w, "canon-1", SetInput{Pin: ptr(true)})
	if len(ps) > 0 || e.Protection != ProtectionPinned || !slices.Equal(changed, []string{Path("my-post")}) {
		t.Fatalf("entry = %+v, %v", e, ps)
	}
	// 降为 normal 属于修改钦定条目：未授权时拒绝并生成决策。
	_, changed, ps = Set(root, w, "canon-1", SetInput{Pin: ptr(false)})
	if !problemsContain(ps, CodeAuthorPinned, "") || !slices.Equal(changed, []string{decision.Path}) {
		t.Fatalf("unpin = %+v", ps)
	}
	if _, ps := decision.Decide(root, "dec-1", "a", "", "作者同意降级", time.Time{}); len(ps) > 0 {
		t.Fatal(ps)
	}
	if e, _, ps = Set(root, w, "canon-1", SetInput{Pin: ptr(false), Decision: "dec-1"}); len(ps) > 0 || e.Protection != ProtectionNormal {
		t.Fatalf("unpin = %+v, %v", e, ps)
	}
}

func TestList(t *testing.T) {
	root, w := newWork(t)
	if es, ps := List(root, w); len(ps) > 0 || len(es) != 0 {
		t.Fatalf("empty list = %+v, %v", es, ps)
	}
	addSource(t, root, "note")
	if _, _, ps := Add(root, w, AddInput{Type: TypeCharacter, Name: "守灯人", Text: "x", Sources: []string{"note"}}); len(ps) > 0 {
		t.Fatal(ps)
	}
	if _, _, ps := Add(root, w, AddInput{Type: TypeRelationship, Text: "旧识", Members: []string{"canon-1", "canon-1"}}); len(ps) == 0 {
		t.Fatal("expected short-members rejection")
	}
	es, ps := List(root, w)
	if len(ps) > 0 || len(es) != 1 {
		t.Fatalf("entries = %+v, %v", es, ps)
	}
	// 删除素材后：悬空引用照常列出，降为 warning（canon-list/dangling-warning）。
	os.RemoveAll(filepath.Join(root, ".writing-anything", "sources", "note"))
	es, ps = List(root, w)
	if len(es) != 1 || !problemsContain(ps, CodeUnknownSource, "entries[0].sources[0]") || ps[0].Severity != workspace.SeverityWarning {
		t.Fatalf("entries = %+v, ps = %+v", es, ps)
	}
	// 条目 id 重复：不返回条目。
	rel := Path("my-post")
	body := strings.TrimSuffix(read(t, root, rel), "\n")
	write(t, root, rel, body+"\n  - {id: canon-2, type: location, name: 灯塔, text: 悬崖, sources: [], protection: normal}\n")
	write(t, root, rel, strings.Replace(read(t, root, rel), "id: canon-2, type: location", "id: canon-1, type: location", 1))
	if es, ps = List(root, w); es != nil || !problemsContain(ps, "artifact.duplicate-id", "") {
		t.Fatalf("entries = %+v, ps = %+v", es, ps)
	}
}

func TestCheckWorks(t *testing.T) {
	root, w := newWork(t)
	if _, _, ps := Add(root, w, AddInput{Type: TypeCharacter, Name: "守灯人", Text: "x"}); len(ps) > 0 {
		t.Fatal(ps)
	}
	works := []*work.Work{w}
	checked, ps := CheckWorks(root, works)
	if len(ps) > 0 || !slices.Equal(checked, []string{Path("my-post")}) {
		t.Fatalf("checked = %v, ps = %+v", checked, ps)
	}
	// 无 canon.yaml 的作品跳过；有问题的作品报告（canon-validation/duplicate-entry、unknown-member-validate）。
	rel := Path("my-post")
	write(t, root, rel, "schema_version: 1\nentries:\n  - {id: canon-1, type: character, name: 甲, text: x, sources: [], protection: normal}\n"+
		"  - {id: canon-1, type: location, name: 乙, text: y, sources: [], protection: normal}\n")
	if _, ps = CheckWorks(root, works); !problemsContain(ps, "artifact.duplicate-id", "entries[1].id") {
		t.Fatalf("ps = %+v", ps)
	}
	write(t, root, rel, "schema_version: 1\nentries:\n  - {id: canon-1, type: character, name: 甲, text: x, sources: [], protection: normal}\n"+
		"  - {id: canon-2, type: location, name: 乙, text: y, sources: [], protection: normal}\n"+
		"  - {id: canon-3, type: relationship, text: 关系, sources: [], protection: normal, members: [canon-1, canon-2]}\n")
	if _, ps = CheckWorks(root, works); !problemsContain(ps, CodeUnknownCharacter, "entries[2].members[1]") {
		t.Fatalf("ps = %+v", ps)
	}
	// decision 引用不存在的决策（canon-validation）。
	write(t, root, rel, "schema_version: 1\nentries:\n  - {id: canon-1, type: character, name: 甲, text: x, sources: [], protection: author-pinned, decision: dec-9}\n")
	if _, ps = CheckWorks(root, works); !problemsContain(ps, CodeUnknownDecision, "entries[0].decision") {
		t.Fatalf("ps = %+v", ps)
	}
	// 符号链接：按不存在处理并报告 artifact.not-found。
	os.Remove(filepath.Join(root, filepath.FromSlash(rel)))
	target := filepath.Join(root, "outside.yaml")
	os.WriteFile(target, []byte("schema_version: 1\nentries: []\n"), 0o644)
	if err := os.Symlink(target, filepath.Join(root, filepath.FromSlash(rel))); err != nil {
		t.Fatal(err)
	}
	if checked, ps = CheckWorks(root, works); !problemsContain(ps, "artifact.not-found", "") || !slices.Equal(checked, []string{rel}) {
		t.Fatalf("checked = %v, ps = %+v", checked, ps)
	}
	// 命令入口同此处理：canon add 拒绝写入符号链接。
	if _, _, ps = Add(root, w, AddInput{Type: TypeWorldRule, Text: "x"}); !problemsContain(ps, "artifact.not-found", "") {
		t.Fatalf("add on symlink = %+v", ps)
	}
}

func TestAddNormalizesMembers(t *testing.T) {
	// 非 relationship 条目的 members 与 sources 在输出中为空数组，不是 null（cli-contract/shared-envelope）。
	root, w := newWork(t)
	e, _, ps := Add(root, w, AddInput{Type: TypeWorldRule, Text: "规则"})
	if len(ps) > 0 || e.Members == nil || e.Sources == nil || len(e.Members) != 0 || len(e.Sources) != 0 {
		t.Fatalf("entry = %+v, %v", e, ps)
	}
}

func TestDecisionFlagOnlyForPinned(t *testing.T) {
	root, w := newWork(t)
	if _, _, ps := Add(root, w, AddInput{Type: TypeWorldRule, Text: "规则"}); len(ps) > 0 {
		t.Fatal(ps)
	}
	for _, decide := range []func() []workspace.Problem{
		func() []workspace.Problem {
			_, _, ps := Set(root, w, "canon-1", SetInput{Text: ptr("新"), Decision: "dec-1"})
			return ps
		},
		func() []workspace.Problem {
			_, _, ps := Remove(root, w, "canon-1", "dec-1")
			return ps
		},
	} {
		ps := decide()
		if len(ps) != 1 || ps[0].Code != "input.invalid-value" || ps[0].Field != "decision" {
			t.Fatalf("problems = %+v", ps)
		}
	}
	if s := read(t, root, Path("my-post")); strings.Contains(s, "decision:") {
		t.Errorf("canon.yaml records a decision on a normal entry:\n%s", s)
	}
}

func TestPinnedQueueUnloadable(t *testing.T) {
	// decisions.yaml 无法加载：报告 canon.author-pinned 与队列诊断，不追加，两文件都不变（canon-protection）。
	root, w := newWork(t)
	addPinned(t, root, w)
	rel := Path("my-post")
	before := read(t, root, rel)
	write(t, root, decision.Path, "decisions: [broken\n")
	_, changed, ps := Set(root, w, "canon-1", SetInput{Text: ptr("新")})
	if !problemsContain(ps, CodeAuthorPinned, "") || !problemsContain(ps, "artifact.parse-error", "") {
		t.Fatalf("problems = %+v", ps)
	}
	if changed != nil {
		t.Fatalf("changed = %v, want nil", changed)
	}
	if read(t, root, rel) != before || read(t, root, decision.Path) != "decisions: [broken\n" {
		t.Error("files changed while the queue was unloadable")
	}
}

func TestAddRejectsNonUTF8(t *testing.T) {
	root, w := newWork(t)
	if _, _, ps := Add(root, w, AddInput{Type: TypeWorldRule, Text: string([]byte{0xff})}); len(ps) != 1 ||
		ps[0].Code != "input.invalid-value" || ps[0].Field != "text" {
		t.Fatalf("problems = %+v", ps)
	}
	bad := string([]byte{0xff})
	if _, _, ps := Set(root, w, "canon-1", SetInput{Name: &bad}); problemsContain(ps, CodeNotFound, "") {
		// canon.yaml 尚未创建：先报告参数问题。
		t.Fatalf("problems = %+v", ps)
	}
}

func TestRejectDecisionReasons(t *testing.T) {
	root, w := newWork(t)
	addPinned(t, root, w)
	// 决策尚未决定：--decision 不能授权。
	if _, ps := decision.Add(root, decision.Input{Question: Question(OpModify, Entry{ID: "canon-1", Name: ptr("守灯人")}),
		Options: []string{"允许", "保持不变"}, Recommend: "b", Blocking: true, Units: []string{"my-post"}},
		func(ref string) []workspace.Problem { return nil }); len(ps) > 0 {
		t.Fatal(ps)
	}
	_, changed, ps := Set(root, w, "canon-1", SetInput{Text: ptr("新"), Decision: "dec-1"})
	if !problemsContain(ps, CodeAuthorPinned, "") || changed != nil || !strings.Contains(ps[0].Message, "尚未由作者决定") {
		t.Fatalf("problems = %+v, changed = %v", ps, changed)
	}
	// 决策属于另一个条目或操作：不能授权。
	decision.Decide(root, "dec-1", "a", "", "作者同意", time.Time{})
	other := Entry{ID: "canon-9", Name: ptr("别人")}
	if _, ps := decision.Add(root, decision.Input{Question: Question(OpModify, other),
		Options: []string{"允许", "保持不变"}, Recommend: "b", Blocking: true, Units: []string{"my-post"}},
		func(ref string) []workspace.Problem { return nil }); len(ps) > 0 {
		t.Fatal(ps)
	}
	if _, _, ps = Set(root, w, "canon-1", SetInput{Text: ptr("新"), Decision: "dec-2"}); !problemsContain(ps, CodeAuthorPinned, "") ||
		!strings.Contains(ps[0].Message, "不是为该条目与这次") {
		t.Fatalf("problems = %+v", ps)
	}
}

func TestSetNameDelete(t *testing.T) {
	// --name 空值删除字段：character 改型为 world-rule 时去掉 name。
	root, w := newWork(t)
	if _, _, ps := Add(root, w, AddInput{Type: TypeCharacter, Name: "守灯人", Text: "x"}); len(ps) > 0 {
		t.Fatal(ps)
	}
	e, _, ps := Set(root, w, "canon-1", SetInput{Type: ptr(TypeWorldRule), Name: ptr("")})
	if len(ps) > 0 || e.Type != TypeWorldRule || e.Name != nil {
		t.Fatalf("entry = %+v, %v", e, ps)
	}
	if s := read(t, root, Path("my-post")); strings.Contains(s, "name:") {
		t.Errorf("canon.yaml still has name:\n%s", s)
	}
}
