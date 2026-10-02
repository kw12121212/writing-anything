package brief

import (
	"os"
	"path/filepath"
	"slices"
	"strings"
	"testing"

	"github.com/kw12121212/writing-anything/internal/decision"
	"github.com/kw12121212/writing-anything/internal/schema"
	"github.com/kw12121212/writing-anything/internal/workspace"
)

const unitDir = ".writing-anything/works/my-post"

func abs(root, rel string) string { return filepath.Join(root, filepath.FromSlash(rel)) }

func put(t *testing.T, root, rel, content string) {
	t.Helper()
	if err := os.MkdirAll(filepath.Dir(abs(root, rel)), 0o755); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(abs(root, rel), []byte(content), 0o644); err != nil {
		t.Fatal(err)
	}
}

func read(t *testing.T, root, rel string) string {
	t.Helper()
	data, err := os.ReadFile(abs(root, rel))
	if err != nil {
		t.Fatal(err)
	}
	return string(data)
}

// project 返回含作品目录 my-post 与已登记素材 src-a 的项目目录。
func project(t *testing.T) string {
	t.Helper()
	root := t.TempDir()
	put(t, root, ".writing-anything/sources/src-a/original.txt", "笔记")
	put(t, root, ".writing-anything/sources/src-a/source.yaml",
		"schema_version: 1\nid: src-a\ntype: author-note\nfile: original.txt\nsha256: b778e307964fb978f7deb01a76dcfb63f3c97b8ee695d9804e278dfc1dd4f016\nbytes: 6\n")
	put(t, root, unitDir+"/.keep", "")
	return root
}

func codes(ps []workspace.Problem) []string {
	var out []string
	for _, p := range ps {
		out = append(out, p.Code)
	}
	return out
}

func noEnsure() ([]string, []workspace.Problem) { return nil, nil }

func ptr[T any](v T) *T { return &v }

func TestLoadAndCheck(t *testing.T) {
	root := project(t)
	if b, ps := Load(root, unitDir, Queue{}); b != nil || ps != nil {
		t.Fatalf("missing brief: %+v %+v", b, ps)
	}
	q := Queue{Decisions: []decision.Decision{{ID: "dec-1", Status: decision.StatusOpen, Units: []string{"my-post"}}, {ID: "dec-2", Status: decision.StatusDecided, Units: []string{"my-post/intro"}}}}
	tests := []struct {
		name, content, code, field string
	}{
		{"duplicate message", "schema_version: 1\nkey_messages:\n  - {id: km-1, text: a}\n  - {id: km-1, text: b}\n", "artifact.duplicate-id", "key_messages[1].id"},
		{"unknown source", "schema_version: 1\nsources: [src-nope]\n", "brief.unknown-source", "sources[0]"},
		{"unknown message source", "schema_version: 1\nkey_messages:\n  - {id: km-1, text: a, sources: [src-a, src-nope]}\n", "brief.unknown-source", "key_messages[0].sources[1]"},
		{"premise unknown source", "schema_version: 1\npremise: {text: 前提, sources: [src-nope]}\n", "brief.unknown-source", "premise.sources[0]"},
		{"premise unknown decision", "schema_version: 1\npremise: {text: 前提, decision: dec-9}\n", "brief.unknown-decision", "premise.decision"},
		{"premise unrelated decision", "schema_version: 1\npremise: {text: 前提, decision: dec-2}\n", "brief.unrelated-decision", "premise.decision"},
		{"unknown decision", "schema_version: 1\nkey_messages:\n  - {id: km-1, text: a, decision: dec-9}\n", "brief.unknown-decision", "key_messages[0].decision"},
		{"unrelated decision", "schema_version: 1\nkey_messages:\n  - {id: km-1, text: a, decision: dec-2}\n", "brief.unrelated-decision", "key_messages[0].decision"},
		{"invalid length", "schema_version: 1\nlength: {min: 3000, max: 1500}\n", "brief.invalid-length", "length"},
		{"schema violation", "schema_version: 1\ntone: 轻松\n", "artifact.schema-violation", "tone"},
		{"parse error", "schema_version: [\n", "artifact.parse-error", ""},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			put(t, root, Path(unitDir), tt.content)
			b, ps := Load(root, unitDir, q)
			if b != nil || len(ps) != 1 || ps[0].Code != tt.code || ps[0].Field != tt.field || ps[0].Path != Path(unitDir) {
				t.Fatalf("Load = %+v, %+v; want one %s at %s", b, ps, tt.code, tt.field)
			}
			if ps := CheckArtifact(root, Path(unitDir)); !slices.Equal(codes(ps), []string{tt.code}) && tt.code != "artifact.parse-error" && !strings.Contains(tt.code, "decision") {
				t.Fatalf("CheckArtifact = %+v", ps)
			}
		})
	}
	// 决策队列无法加载时不检查决策是否存在。
	put(t, root, Path(unitDir), "schema_version: 1\nkey_messages:\n  - {id: km-1, text: a, decision: dec-9}\n")
	if b, ps := Load(root, unitDir, Queue{Invalid: true}); b == nil || ps != nil {
		t.Fatalf("invalid queue: %+v %+v", b, ps)
	}

	put(t, root, Path(unitDir), "schema_version: 1\naudience: 读者\nkey_messages:\n  - {id: km-1, text: a, sources: [src-a], decision: dec-1}\nlength: {min: 10, max: 10}\n")
	b, ps := Load(root, unitDir, q)
	if ps != nil || b.Audience != "读者" || b.Path != Path(unitDir) || len(b.KeyMessages) != 1 || b.KeyMessages[0].Decision != "dec-1" || *b.Length != (Length{10, 10}) {
		t.Fatalf("Load = %+v, %+v", b, ps)
	}
	// 整体校验读取工作区中的决策队列：没有 dec-1 时报告 brief.unknown-decision。
	checked, ps := CheckUnitArtifacts(root, []string{unitDir, unitDir + "/units/intro"}, LoadQueue(root))
	if !slices.Equal(checked, []string{Path(unitDir)}) || !slices.Equal(codes(ps), []string{"brief.unknown-decision"}) {
		t.Fatalf("CheckUnitArtifacts = %v, %+v", checked, ps)
	}
	put(t, root, decision.Path, "schema_version: 1\ndecisions:\n  - {id: dec-1, question: q, options: [], blocking: false, units: [my-post], status: open}\n")
	checked, ps = CheckUnitArtifacts(root, []string{unitDir, unitDir + "/units/intro"}, LoadQueue(root))
	if !slices.Equal(checked, []string{Path(unitDir)}) || ps != nil {
		t.Fatalf("CheckUnitArtifacts = %v, %+v", checked, ps)
	}
}

func TestSymlinkedBrief(t *testing.T) {
	root := project(t)
	outside := filepath.Join(t.TempDir(), "brief.yaml")
	if err := os.WriteFile(outside, []byte("schema_version: 1\naudience: 外部\n"), 0o644); err != nil {
		t.Fatal(err)
	}
	if err := os.Symlink(outside, abs(root, Path(unitDir))); err != nil {
		t.Skip("symlinks unavailable:", err)
	}
	// 所有入口都报告同一问题：不读取外部内容，也不覆盖链接。
	b, ps := Load(root, unitDir, Queue{})
	if b != nil || len(ps) != 1 || ps[0].Code != "artifact.not-found" || ps[0].Path != Path(unitDir) {
		t.Fatalf("Load = %+v %+v", b, ps)
	}
	g := Check(root, unitDir, true, false, []string{FieldAudience}, Queue{})
	if g == nil || len(g.Invalid) != 1 || g.Invalid[0].Code != "artifact.not-found" {
		t.Fatalf("Check = %+v", g)
	}
	if _, _, ps := Set(root, unitDir, Edit{Audience: ptr("x")}, Queue{}, noEnsure); !slices.Equal(codes(ps), []string{"artifact.not-found"}) {
		t.Fatalf("Set = %+v", ps)
	}
	if checked, ps := CheckUnitArtifacts(root, []string{unitDir}, Queue{}); !slices.Equal(checked, []string{Path(unitDir)}) || !slices.Equal(codes(ps), []string{"artifact.not-found"}) {
		t.Fatalf("CheckUnitArtifacts = %v %+v", checked, ps)
	}
	if info, err := os.Lstat(abs(root, Path(unitDir))); err != nil || info.Mode()&os.ModeSymlink == 0 {
		t.Fatal("symlink was replaced")
	}
	if data, _ := os.ReadFile(outside); !strings.Contains(string(data), "外部") {
		t.Fatalf("outside file changed: %s", data)
	}
}

func TestQuestions(t *testing.T) {
	q := Queue{Decisions: []decision.Decision{{ID: "dec-1", Status: decision.StatusOpen}, {ID: "dec-2", Status: decision.StatusDecided}}}
	all := Questions(nil, Fields, q)
	var fields []string
	for _, qu := range all {
		fields = append(fields, qu.Field)
		if qu.Code != "brief.missing-field" || qu.Question == "" || !strings.Contains(qu.Message, qu.Question) || !strings.Contains(qu.Message, qu.Field) {
			t.Errorf("question = %+v", qu)
		}
	}
	if !slices.Equal(fields, Fields) {
		t.Fatalf("fields = %v, want %v", fields, Fields)
	}
	b := &Brief{Audience: "读者", KeyMessages: []Message{
		{ID: "km-1", Text: "有素材", Sources: []string{"src-a"}, Decision: "dec-1"},
		{ID: "km-2", Text: "没有出处"},
		{ID: "km-3", Text: "未决定", Decision: "dec-1"},
		{ID: "km-4", Text: "已决定", Decision: "dec-2"},
		{ID: "km-5", Text: "决策不存在", Decision: "dec-9"},
	}, Avoid: []string{}}
	got := Questions(b, []string{FieldAudience, FieldPurpose, FieldAvoid}, q)
	var summary []string
	for _, qu := range got {
		summary = append(summary, qu.Code+" "+qu.Field)
	}
	want := []string{"brief.missing-field purpose", "brief.missing-field avoid", "brief.unsourced-message key_messages[1]",
		"brief.unconfirmed-message key_messages[2]", "brief.unconfirmed-message key_messages[4]"}
	if !slices.Equal(summary, want) {
		t.Fatalf("questions = %v, want %v", summary, want)
	}
	if !strings.Contains(got[2].Message, "km-2") || !strings.Contains(got[3].Message, "dec-1") {
		t.Fatalf("messages = %q, %q", got[2].Message, got[3].Message)
	}
	// 只含全角空格的文本视为未填写。
	if got := Questions(&Brief{Audience: "\u3000", Purpose: "p"}, []string{FieldAudience, FieldPurpose}, q); len(got) != 1 || got[0].Field != "audience" {
		t.Fatalf("full-width blank: %+v", got)
	}
	// 决策队列无法加载时，已决定的决策也不算确认记录。
	if got := Questions(&Brief{KeyMessages: []Message{{ID: "km-1", Text: "a", Decision: "dec-2"}}}, nil, Queue{Decisions: q.Decisions, Invalid: true}); len(got) != 1 || got[0].Code != "brief.unconfirmed-message" {
		t.Fatalf("invalid queue: %+v", got)
	}
	// 前提（Premise）与核心观点同样要求出处或作者的确认记录。
	for _, tc := range []struct {
		premise *Premise
		code    string
	}{
		{&Premise{Text: "守灯人的前提"}, "brief.unsourced-premise"},
		{&Premise{Text: "守灯人的前提", Decision: "dec-1"}, "brief.unconfirmed-premise"},
		{&Premise{Text: "守灯人的前提", Sources: []string{"src-a"}}, ""},
		{&Premise{Text: "守灯人的前提", Decision: "dec-2"}, ""},
		{&Premise{Text: "守灯人的前提", Sources: []string{"src-a"}, Decision: "dec-1"}, ""},
	} {
		got := Questions(&Brief{Premise: tc.premise}, nil, q)
		if tc.code == "" {
			if len(got) != 0 {
				t.Errorf("premise %+v: questions = %+v", tc.premise, got)
			}
			continue
		}
		if len(got) != 1 || got[0].Code != tc.code || got[0].Field != FieldPremise {
			t.Errorf("premise %+v: questions = %+v, want %s %s", tc.premise, got, tc.code, FieldPremise)
		}
	}
	for _, f := range Fields {
		full := &Brief{Audience: "a", Purpose: "p", Premise: &Premise{Text: "前提", Sources: []string{"s"}}, Theme: "主题",
			Pov: &Pov{Person: "third", Character: "守灯人"}, KeyMessages: []Message{{ID: "k", Text: "t", Sources: []string{"s"}}}, Length: &Length{1, 2}, Avoid: []string{"x"}, Sources: []string{"s"}}
		if got := Questions(full, []string{f}, q); len(got) != 0 {
			t.Errorf("filled %s: %+v", f, got)
		}
	}
}

func TestCheckGate(t *testing.T) {
	root := project(t)
	req := []string{FieldAudience, FieldPurpose}
	if g := Check(root, unitDir, false, false, req, Queue{}); g != nil {
		t.Fatalf("no brief: %+v", g)
	}
	g := Check(root, unitDir, false, true, req, Queue{})
	if g == nil || g.Path != unitDir+"/brief.md" || len(g.Questions) != 2 || g.Invalid != nil {
		t.Fatalf("brief.md: %+v", g)
	}
	put(t, root, Path(unitDir), "k: v\n")
	if g := Check(root, unitDir, true, true, req, Queue{}); g == nil || g.Path != Path(unitDir) || len(g.Invalid) == 0 || g.Questions != nil {
		t.Fatalf("invalid: %+v", g)
	}
	put(t, root, Path(unitDir), "schema_version: 1\naudience: 读者\n")
	if g := Check(root, unitDir, true, false, req, Queue{}); g == nil || len(g.Questions) != 1 || g.Questions[0].Field != "purpose" {
		t.Fatalf("partial: %+v", g)
	}
}

func TestSetCreatesAndMerges(t *testing.T) {
	root := project(t)
	q := Queue{Decisions: []decision.Decision{{ID: "dec-1", Status: decision.StatusOpen, Units: []string{"my-post"}}}}
	ensured := false
	ensure := func() ([]string, []workspace.Problem) { ensured = true; return []string{"new-dir"}, nil }
	b, changed, ps := Set(root, unitDir, Edit{
		Purpose: ptr("讲清楚连接池"), Audience: ptr("后端工程师"),
		Message: &MessageEdit{ID: "km-1", Text: ptr("连接池降低延迟"), Sources: &[]string{"src-a"}},
	}, q, ensure)
	if ps != nil || !ensured || !slices.Equal(changed, []string{"new-dir", Path(unitDir)}) || b.Audience != "后端工程师" || b.KeyMessages[0].Sources[0] != "src-a" {
		t.Fatalf("create: %+v %v %+v", b, changed, ps)
	}
	want := "# 写作简报（Brief）；请用 wa-cli brief set 修改。\n\nschema_version: 1\naudience: 后端工程师\npurpose: 讲清楚连接池\nkey_messages:\n  - id: km-1\n    text: 连接池降低延迟\n    sources: [src-a]\n"
	if got := read(t, root, Path(unitDir)); got != want {
		t.Fatalf("brief.yaml =\n%s\nwant\n%s", got, want)
	}

	// 作者的注释与未涉及的字段保留；新字段按固定顺序插入。
	put(t, root, Path(unitDir), "# 作者的注释\nschema_version: 1\nsources: [src-a] # 素材\naudience: 后端工程师 # 读者\nkey_messages:\n  - id: km-1\n    text: 连接池降低延迟\n    sources: [src-a]\n")
	b, changed, ps = Set(root, unitDir, Edit{
		SetLength: true, Length: &Length{1500, 3000}, SetAvoid: true, Avoid: []string{"不点名"},
		Message: &MessageEdit{ID: "km-1", Decision: ptr("dec-1")},
	}, q, noEnsure)
	if ps != nil || !slices.Equal(changed, []string{Path(unitDir)}) || b.KeyMessages[0].Text != "连接池降低延迟" || b.KeyMessages[0].Decision != "dec-1" {
		t.Fatalf("merge: %+v %v %+v", b, changed, ps)
	}
	got := read(t, root, Path(unitDir))
	for _, s := range []string{"# 作者的注释", "# 素材", "# 读者", "length: {min: 1500, max: 3000}", "decision: dec-1", "avoid:\n  - 不点名"} {
		if !strings.Contains(got, s) {
			t.Errorf("brief.yaml lacks %q:\n%s", s, got)
		}
	}
	if strings.Index(got, "sources: [src-a] # 素材") > strings.Index(got, "length:") {
		t.Errorf("length should be placed after existing earlier fields and not reorder them:\n%s", got)
	}

	// 取值未变时不写文件。
	before := read(t, root, Path(unitDir))
	if b, changed, ps := Set(root, unitDir, Edit{Audience: ptr("后端工程师")}, q, noEnsure); ps != nil || changed != nil || b == nil {
		t.Fatalf("unchanged: %v %+v", changed, ps)
	}
	if read(t, root, Path(unitDir)) != before {
		t.Fatal("unchanged edit rewrote brief.yaml")
	}

	// 清空字段、删除与重新追加核心观点。
	b, _, ps = Set(root, unitDir, Edit{
		Audience: ptr(""), SetAvoid: true, SetLength: true, SetSources: true, Sources: nil,
		Remove:  []string{"km-1"},
		Message: &MessageEdit{ID: "km-2", Text: ptr("连接数不是越多越好")},
	}, q, noEnsure)
	if ps != nil || b.Audience != "" || b.Avoid != nil || b.Length != nil || b.Sources != nil || len(b.KeyMessages) != 1 || b.KeyMessages[0].ID != "km-2" {
		t.Fatalf("clear: %+v %+v", b, ps)
	}
	b, _, ps = Set(root, unitDir, Edit{Message: &MessageEdit{ID: "km-2", Sources: &[]string{}, Decision: ptr("")}, Purpose: ptr("新目的")}, q, noEnsure)
	if ps != nil || b.KeyMessages[0].Sources != nil || b.KeyMessages[0].Decision != "" || b.Purpose != "新目的" {
		t.Fatalf("clear message fields: %+v %+v", b, ps)
	}
	b, _, ps = Set(root, unitDir, Edit{Remove: []string{"km-2"}}, q, noEnsure)
	if ps != nil || b.KeyMessages != nil || strings.Contains(read(t, root, Path(unitDir)), "key_messages") {
		t.Fatalf("remove last message: %+v %+v\n%s", b, ps, read(t, root, Path(unitDir)))
	}

	// 修改已有观点保持流式序列；length 逐键修改，保留键上的注释。
	put(t, root, Path(unitDir), "schema_version: 1\nkey_messages: [{id: km-1, text: a}]\nlength:\n  min: 1 # 下限\n  max: 2\n")
	if _, _, ps := Set(root, unitDir, Edit{Message: &MessageEdit{ID: "km-1", Text: ptr("b")}, SetLength: true, Length: &Length{5, 9}}, q, noEnsure); ps != nil {
		t.Fatal(ps)
	}
	got = read(t, root, Path(unitDir))
	for _, want := range []string{"key_messages: [{id: km-1, text: b}]", "min: 5 # 下限", "max: 9"} {
		if !strings.Contains(got, want) {
			t.Errorf("brief.yaml lacks %q:\n%s", want, got)
		}
	}
	// 向流式序列追加观点后改为块式。
	if _, _, ps := Set(root, unitDir, Edit{Message: &MessageEdit{ID: "km-2", Text: ptr("c")}}, q, noEnsure); ps != nil || !strings.Contains(read(t, root, Path(unitDir)), "key_messages:\n  - {id: km-1, text: b}\n  - id: km-2") {
		t.Fatalf("append: %+v\n%s", ps, read(t, root, Path(unitDir)))
	}
}

// 前提（Premise）、主题（Theme）与视角（POV）的写入、子字段修改与删除（brief-set）。
func TestSetFictionFields(t *testing.T) {
	root := project(t)
	q := Queue{Decisions: []decision.Decision{{ID: "dec-1", Status: decision.StatusOpen, Units: []string{"my-post"}}}}
	b, changed, ps := Set(root, unitDir, Edit{
		Premise:     &PremiseEdit{Text: ptr("守灯人的前提"), Sources: &[]string{"src-a"}},
		Theme:       ptr("善意的谎言"),
		PovPerson:   ptr("third"),
		PovCharacter: ptr("守灯人"),
	}, q, noEnsure)
	if ps != nil || !slices.Equal(changed, []string{Path(unitDir)}) || b.Premise == nil || b.Premise.Text != "守灯人的前提" ||
		b.Theme != "善意的谎言" || b.Pov == nil || b.Pov.Person != "third" || b.Pov.Character != "守灯人" {
		t.Fatalf("create: %+v %v %+v", b, changed, ps)
	}
	want := "# 写作简报（Brief）；请用 wa-cli brief set 修改。\n\nschema_version: 1\npremise:\n  text: 守灯人的前提\n  sources: [src-a]\ntheme: 善意的谎言\npov:\n  person: third\n  character: 守灯人\n"
	if got := read(t, root, Path(unitDir)); got != want {
		t.Fatalf("brief.yaml =\n%s\nwant\n%s", got, want)
	}

	// 只改子字段：前提关联决策；删除视角人物后 pov 仍有 person。
	b, changed, ps = Set(root, unitDir, Edit{Premise: &PremiseEdit{Decision: ptr("dec-1")}, PovCharacter: ptr("")}, q, noEnsure)
	if ps != nil || changed == nil || b.Premise.Decision != "dec-1" || b.Premise.Text != "守灯人的前提" || b.Pov.Character != "" || b.Pov.Person != "third" {
		t.Fatalf("subfields: %+v %v %+v", b, changed, ps)
	}
	// 前提子字段的删除：空 sources 与空 decision 分别删除对应子字段，text 保留。
	b, _, ps = Set(root, unitDir, Edit{Premise: &PremiseEdit{Sources: &[]string{}, Decision: ptr("")}}, q, noEnsure)
	if ps != nil || b.Premise.Sources != nil || b.Premise.Decision != "" || b.Premise.Text != "守灯人的前提" {
		t.Fatalf("clear premise subfields: %+v %+v", b, ps)
	}

	// 删除前提与视角整体；主题保留。
	b, changed, ps = Set(root, unitDir, Edit{Premise: &PremiseEdit{Text: ptr("")}, PovPerson: ptr("")}, q, noEnsure)
	if ps != nil || b.Premise != nil || b.Pov != nil || b.Theme != "善意的谎言" {
		t.Fatalf("delete: %+v %+v", b, ps)
	}
	if got := read(t, root, Path(unitDir)); strings.Contains(got, "premise") || strings.Contains(got, "pov") || !strings.Contains(got, "theme: 善意的谎言") {
		t.Fatalf("after delete:\n%s", got)
	}

	// 删除人称而保留视角人物时，写前复查按 Schema 拒绝，诊断指向参数 pov-person。
	Set(root, unitDir, Edit{PovPerson: ptr("third"), PovCharacter: ptr("守灯人")}, q, noEnsure)
	_, changed, ps = Set(root, unitDir, Edit{PovPerson: ptr("")}, q, noEnsure)
	if changed != nil || len(ps) != 1 || ps[0].Code != schema.CodeSchemaViolation || ps[0].Path != "" || ps[0].Field != "pov-person" {
		t.Fatalf("delete person: %v %+v", changed, ps)
	}

	// 人称不在取值范围内时，同样由写前复查拒绝。
	_, changed, ps = Set(root, unitDir, Edit{PovPerson: ptr("omniscient")}, q, noEnsure)
	if changed != nil || len(ps) != 1 || ps[0].Code != schema.CodeSchemaViolation || ps[0].Field != "pov-person" {
		t.Fatalf("bad person: %v %+v", changed, ps)
	}

	// 只给子字段而没有前提时：先建出前提，写前复查报告缺少 text。
	put(t, root, Path(unitDir), "schema_version: 1\n")
	_, changed, ps = Set(root, unitDir, Edit{Premise: &PremiseEdit{Sources: &[]string{"src-a"}}}, q, noEnsure)
	if changed != nil || len(ps) != 1 || ps[0].Code != schema.CodeSchemaViolation || ps[0].Field != "premise" {
		t.Fatalf("sources without text: %v %+v", changed, ps)
	}

	// 引用未登记的素材或无关决策时，以参数名为字段拒绝，不写文件。
	for _, e := range []Edit{
		{Premise: &PremiseEdit{Text: ptr("前提"), Sources: &[]string{"src-nope"}}},
		{Premise: &PremiseEdit{Text: ptr("前提"), Decision: ptr("dec-9")}},
	} {
		_, changed, ps = Set(root, unitDir, e, q, noEnsure)
		if changed != nil || len(ps) != 1 || ps[0].Path != "" || (ps[0].Field != "premise-sources[0]" && ps[0].Field != "premise-decision") {
			t.Fatalf("reject: %v %+v", changed, ps)
		}
	}
}

func TestSetRejects(t *testing.T) {
	root := project(t)
	q := Queue{Decisions: []decision.Decision{{ID: "dec-1", Status: decision.StatusOpen, Units: []string{"my-post"}}, {ID: "dec-2", Status: decision.StatusDecided, Units: []string{"other"}}}}
	put(t, root, Path(unitDir), "schema_version: 1\nkey_messages:\n  - {id: km-1, text: a}\n")
	tests := []struct {
		name  string
		e     Edit
		code  string
		field string
	}{
		{"unknown source", Edit{SetSources: true, Sources: []string{"src-nope"}}, "brief.unknown-source", "sources[0]"},
		{"unknown message source", Edit{Message: &MessageEdit{ID: "km-1", Sources: &[]string{"src-a", "src-nope"}}}, "brief.unknown-source", "message-sources[1]"},
		{"unknown decision", Edit{Message: &MessageEdit{ID: "km-1", Decision: ptr("dec-9")}}, "brief.unknown-decision", "message-decision"},
		{"remove missing", Edit{Remove: []string{"km-9"}}, "brief.message-not-found", "remove-message[0]"},
		{"unrelated decision", Edit{Message: &MessageEdit{ID: "km-1", Decision: ptr("dec-2")}}, "brief.unrelated-decision", "message-decision"},
		// 写前复查才发现的参数问题不填路径，字段为参数名。
		{"new message without text", Edit{Message: &MessageEdit{ID: "km-2"}}, "artifact.schema-violation", "message-text"},
		{"blank text", Edit{Message: &MessageEdit{ID: "km-1", Text: ptr(" ")}}, "artifact.schema-violation", "message-text"},
		{"blank audience", Edit{Audience: ptr(" ")}, "artifact.schema-violation", "audience"},
		{"blank avoid", Edit{SetAvoid: true, Avoid: []string{"a", " "}}, "artifact.schema-violation", "avoid[1]"},
		{"duplicate message sources", Edit{Message: &MessageEdit{ID: "km-1", Sources: &[]string{"src-a", "src-a"}}}, "artifact.schema-violation", "message-sources"},
	}
	before := read(t, root, Path(unitDir))
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			b, changed, ps := Set(root, unitDir, tt.e, q, noEnsure)
			if b != nil || changed != nil || len(ps) != 1 || ps[0].Code != tt.code || ps[0].Field != tt.field {
				t.Fatalf("Set = %+v, %v, %+v; want %s at %s", b, changed, ps, tt.code, tt.field)
			}
			if read(t, root, Path(unitDir)) != before {
				t.Fatal("brief.yaml changed")
			}
		})
	}
	// 决策队列无法加载时不能关联决策。
	if _, _, ps := Set(root, unitDir, Edit{Message: &MessageEdit{ID: "km-1", Decision: ptr("dec-1")}}, Queue{Invalid: true}, noEnsure); len(ps) != 1 || ps[0].Code != "decision.invalid-queue" || ps[0].Path != decision.Path {
		t.Fatalf("invalid queue: %+v", ps)
	}
	// 已有的 brief.yaml 不通过校验时报告其问题。
	put(t, root, Path(unitDir), "schema_version: 1\ntone: x\n")
	if _, _, ps := Set(root, unitDir, Edit{Audience: ptr("a")}, q, noEnsure); !slices.Equal(codes(ps), []string{"artifact.schema-violation"}) {
		t.Fatalf("invalid existing: %+v", ps)
	}
	// 写出前确保单元目录的失败原样返回。
	put(t, root, Path(unitDir), "schema_version: 1\n")
	fail := func() ([]string, []workspace.Problem) {
		return nil, []workspace.Problem{{Code: "artifact.write-failed", Severity: workspace.SeverityError}}
	}
	if _, _, ps := Set(root, unitDir, Edit{Audience: ptr("a")}, q, fail); !slices.Equal(codes(ps), []string{"artifact.write-failed"}) {
		t.Fatalf("ensure failure: %+v", ps)
	}
}

func TestSetAnchored(t *testing.T) {
	root := project(t)
	tests := []struct {
		name, content string
		e             Edit
		field         string
	}{
		{"anchored root", "&root\nschema_version: 1\n", Edit{Audience: ptr("a")}, ""},
		{"merge key", "schema_version: 1\n<<: {audience: x}\n", Edit{Audience: ptr("a")}, ""},
		{"anchor inside replaced value", "schema_version: 1\navoid: [&a 不点名]\nkey_messages:\n  - {id: km-1, text: *a}\n", Edit{SetAvoid: true, Avoid: []string{"新"}}, "avoid"},
		{"anchor inside removed message", "schema_version: 1\nkey_messages:\n  - {id: km-1, text: &t 共用}\n  - {id: km-2, text: *t}\n", Edit{Remove: []string{"km-1"}}, "key_messages[0]"},
		{"anchored value", "schema_version: 1\naudience: &a 读者\npurpose: *a\n", Edit{Purpose: ptr("p")}, "purpose"},
		{"anchored messages", "schema_version: 1\nkey_messages: &m\n  - {id: km-1, text: a}\n", Edit{Remove: []string{"km-1"}}, "key_messages"},
		{"anchored message", "schema_version: 1\nkey_messages:\n  - &k {id: km-1, text: a}\n", Edit{Message: &MessageEdit{ID: "km-1", Text: ptr("b")}}, "key_messages[0]"},
		{"anchored removed message", "schema_version: 1\nkey_messages:\n  - &k {id: km-1, text: a}\n", Edit{Remove: []string{"km-1"}}, "key_messages[0]"},
		{"anchored message field", "schema_version: 1\naudience: &t 文本\nkey_messages:\n  - {id: km-1, text: *t}\n", Edit{Message: &MessageEdit{ID: "km-1", Text: ptr("b")}}, "key_messages[0].text"},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			put(t, root, Path(unitDir), tt.content)
			_, _, ps := Set(root, unitDir, tt.e, Queue{}, noEnsure)
			if tt.name == "merge key" {
				// 合并键使文档不通过 Schema：先报告已有文件的问题，不修改。
				if len(ps) == 0 || read(t, root, Path(unitDir)) != tt.content {
					t.Fatalf("want problems, got %+v", ps)
				}
				return
			}
			if len(ps) != 1 || ps[0].Code != "brief.anchored-value" || ps[0].Field != tt.field {
				t.Fatalf("Set = %+v; want brief.anchored-value at %q", ps, tt.field)
			}
			if read(t, root, Path(unitDir)) != tt.content {
				t.Fatal("brief.yaml changed")
			}
		})
	}
}

func TestParseLength(t *testing.T) {
	for v, want := range map[string]*Length{"1500-3000": {1500, 3000}, "10-10": {10, 10}} {
		if got, ok := ParseLength(v); !ok || *got != *want {
			t.Errorf("ParseLength(%q) = %v, %v", v, got, ok)
		}
	}
	for _, v := range []string{"3000-1500", "0-10", "abc", "10", "-5-10", "+1-2", "1-+2", "1-2-3"} {
		if got, ok := ParseLength(v); ok {
			t.Errorf("ParseLength(%q) = %v, want invalid", v, got)
		}
	}
	if !ValidID("km-1") || ValidID("KM") || ValidID("") {
		t.Fatal("ValidID")
	}
}

func TestLoadQueue(t *testing.T) {
	root := project(t)
	if q := LoadQueue(root); q.Invalid || q.Decisions != nil {
		t.Fatalf("no queue: %+v", q)
	}
	put(t, root, decision.Path, "decisions: [\n")
	if q := LoadQueue(root); !q.Invalid {
		t.Fatalf("broken queue: %+v", q)
	}
}
