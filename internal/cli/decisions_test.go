package cli

import (
	"encoding/json"
	"os"
	"regexp"
	"slices"
	"strings"
	"testing"
)

type decView struct {
	ID             string              `json:"id"`
	Question       string              `json:"question"`
	Options        []map[string]string `json:"options"`
	Recommendation *string             `json:"recommendation"`
	Blocking       bool                `json:"blocking"`
	Units          []string            `json:"units"`
	Status         string              `json:"status"`
	Choice         *string             `json:"choice"`
	Answer         *string             `json:"answer"`
	Confirmation   *string             `json:"confirmation"`
	DecidedAt      *string             `json:"decidedAt"`
	Conflict       map[string]any      `json:"conflict"`
}

type decResult struct {
	result
	Decision    *decView  `json:"decision"`
	Blocking    []decView `json:"blocking"`
	NonBlocking []decView `json:"nonBlocking"`
}

// runDec 以 --json 运行决策命令，复用 runUnits 的路径、next 与 null 断言。
func runDec(t *testing.T, dir string, args ...string) (int, decResult) {
	t.Helper()
	code, res := runUnits(t, dir, args...)
	var out decResult
	if err := json.Unmarshal([]byte(res.stdout), &out); err != nil {
		t.Fatal(err)
	}
	out.result = res.result
	return code, out
}

func decIDs(ds []decView) []string {
	var out []string
	for _, d := range ds {
		out = append(out, d.ID)
	}
	return out
}

const decisionsPath = ".writing-anything/decisions.yaml"

func TestDecisionAdd(t *testing.T) {
	dir := newWork(t)
	code, res := runDec(t, dir, "decision", "add", "--question", "标题用哪个", "--option", "迁移实录", "--option", "踩坑复盘",
		"--recommend", "b", "--rationale", "更贴近读者", "--blocking", "--unit", "my-post")
	d := res.Decision
	if code != 0 || d == nil || d.ID != "dec-1" || len(d.Options) != 2 || d.Options[1]["id"] != "b" || d.Options[1]["text"] != "踩坑复盘" ||
		*d.Recommendation != "b" || !d.Blocking || !slices.Equal(d.Units, []string{"my-post"}) || d.Status != "open" || d.DecidedAt != nil {
		t.Fatalf("code = %d, decision = %+v, %v", code, d, res.Diagnostics)
	}
	if !slices.Equal(res.ChangedPaths, []string{decisionsPath}) {
		t.Errorf("changedPaths = %v", res.ChangedPaths)
	}
	before := readFile(t, wsPath(dir, decisionsPath))
	for _, tc := range []struct {
		args        []string
		code, field string
	}{
		{[]string{"--question", "q", "--option", "x", "--recommend", "c"}, "decision.unknown-option", "recommendation"},
		{[]string{"--question", "q", "--unit", "nope"}, "work.not-found", ""},
		{[]string{"--question", "q", "--unit", "my-post/nope"}, "unit.not-found", ""},
		{[]string{"--option", "x"}, "artifact.schema-violation", "question"},
		{[]string{"--question", "q", "--id", "dec-1"}, "artifact.duplicate-id", "id"},
	} {
		code, res := runDec(t, dir, append([]string{"decision", "add"}, tc.args...)...)
		if d := diag(t, res.result, tc.code); code != 1 || d.Field != tc.field || res.Decision != nil {
			t.Errorf("%v: code = %d, %+v", tc.args, code, d)
		} else if tc.field != "" && d.Next[0] != "wa-cli help decision add --json" {
			t.Errorf("%v: next = %v", tc.args, d.Next)
		}
	}
	if readFile(t, wsPath(dir, decisionsPath)) != before {
		t.Error("rejected decisions changed decisions.yaml")
	}
	// 已有决策队列中的问题（含根级字段）保留能复查它的命令。
	for _, content := range []string{"schema_version: 1\ndecisions: {}\n", "schema_version: 2\ndecisions: []\n", "schema_version: 1\ndecisions: []\nfoo: 1\n"} {
		writeFile(t, wsPath(dir, decisionsPath), content)
		_, res = runDec(t, dir, "decision", "add", "--question", "q")
		if d := diag(t, res.result, "artifact.schema-violation"); d.Next[0] == "wa-cli help decision add --json" {
			t.Errorf("%q: existing problem next = %v", content, d.Next)
		}
	}
	// 参数错误不创建决策队列；重复的 --unit 只记录一次。
	fresh := newWork(t)
	runDec(t, fresh, "decision", "add", "--question", "q", "--option", "x", "--recommend", "c")
	if _, err := os.Stat(wsPath(fresh, decisionsPath)); err == nil {
		t.Error("unknown recommendation created decisions.yaml")
	}
	if _, res := runDec(t, fresh, "decision", "add", "--question", "q", "--unit", "my-post", "--unit", "my-post"); !slices.Equal(res.Decision.Units, []string{"my-post"}) {
		t.Errorf("units = %v", res.Decision.Units)
	}
	// 决策队列是符号链接：各入口都按不存在处理，next 为能复查的整体校验。
	outside := t.TempDir() + "/d.yaml"
	writeFile(t, outside, "schema_version: 1\ndecisions: []\n")
	os.Remove(wsPath(fresh, decisionsPath))
	os.Symlink(outside, wsPath(fresh, decisionsPath))
	for _, args := range [][]string{{"decisions"}, {"validate"}, {"validate", decisionsPath}, {"decision", "add", "--question", "q"}} {
		_, res := runDec(t, fresh, args...)
		if d := diag(t, res.result, "artifact.not-found"); d.Path != decisionsPath || d.Next[0] != shellCommand("wa-cli", "validate", "--dir", fresh, "--json") {
			t.Errorf("%v: %+v", args, d)
		}
	}
}

func TestDecisionsGrouped(t *testing.T) {
	dir := newWork(t)
	if code, res := runDec(t, dir, "decisions"); code != 0 || res.Blocking == nil || len(res.Blocking)+len(res.NonBlocking) != 0 {
		t.Fatalf("empty: code = %d, %+v", code, res)
	}
	runDec(t, dir, "decision", "add", "--question", "阻塞", "--blocking", "--unit", "my-post")
	runDec(t, dir, "decision", "add", "--question", "非阻塞")
	runDec(t, dir, "decision", "add", "--question", "已决定", "--blocking")
	runDec(t, dir, "decide", "dec-3", "--answer", "x", "--confirmation", "作者确认")
	code, res := runDec(t, dir, "decisions")
	if code != 0 || !slices.Equal(decIDs(res.Blocking), []string{"dec-1"}) || !slices.Equal(decIDs(res.NonBlocking), []string{"dec-2"}) || len(res.ChangedPaths) != 0 {
		t.Fatalf("grouped: code = %d, %v %v", code, decIDs(res.Blocking), decIDs(res.NonBlocking))
	}
	code, res = runDec(t, dir, "decisions", "--all")
	if code != 0 || !slices.Equal(decIDs(res.Blocking), []string{"dec-1", "dec-3"}) || res.Blocking[1].Status != "decided" || res.Blocking[1].Confirmation == nil {
		t.Fatalf("all: code = %d, %+v", code, res.Blocking)
	}
	writeFile(t, wsPath(dir, decisionsPath), "a: [\n")
	if code, res = runDec(t, dir, "decisions"); code != 1 || len(res.Blocking) != 0 {
		t.Errorf("invalid: code = %d", code)
	}
}

func TestDecide(t *testing.T) {
	dir := newWork(t)
	runDec(t, dir, "decision", "add", "--question", "标题", "--option", "迁移实录", "--option", "踩坑复盘", "--blocking", "--unit", "my-post")
	runDec(t, dir, "decision", "add", "--question", "字数")
	writeFile(t, wsPath(dir, decisionsPath), "# 作者的注释\n"+readFile(t, wsPath(dir, decisionsPath)))
	code, res := runDec(t, dir, "decide", "dec-1", "--option", "b", "--confirmation", "作者说就用踩坑复盘")
	d := res.Decision
	if code != 0 || d.Status != "decided" || *d.Choice != "b" || *d.Confirmation != "作者说就用踩坑复盘" ||
		!regexp.MustCompile(`^\d{4}-\d{2}-\d{2}T\d{2}:\d{2}:\d{2}Z$`).MatchString(*d.DecidedAt) || !slices.Equal(res.ChangedPaths, []string{decisionsPath}) {
		t.Fatalf("decide: code = %d, %+v", code, d)
	}
	stored := readFile(t, wsPath(dir, decisionsPath))
	if !strings.Contains(stored, "# 作者的注释") || !strings.Contains(stored, "decided_at: \""+*d.DecidedAt+"\"") && !strings.Contains(stored, "decided_at: "+*d.DecidedAt) {
		t.Errorf("decisions.yaml:\n%s", stored)
	}
	if code, res = runDec(t, dir, "decide", "dec-2", "--answer", "控制在 3000 字", "--confirmation", "作者确认 3000 字"); code != 0 || *res.Decision.Answer != "控制在 3000 字" || res.Decision.Choice != nil {
		t.Errorf("free answer: code = %d, %+v", code, res.Decision)
	}
	runDec(t, dir, "decision", "add", "--question", "开头", "--option", "甲")
	before := readFile(t, wsPath(dir, decisionsPath))
	for _, tc := range []struct {
		args        []string
		code, field string
		next        string
	}{
		{[]string{"dec-3", "--option", "a"}, "artifact.schema-violation", "confirmation", "wa-cli help decide --json"},
		{[]string{"dec-1", "--option", "a", "--confirmation", "x"}, "decision.already-decided", "decisions[0].status", "decisions --all"},
		{[]string{"dec-9", "--option", "a", "--confirmation", "x"}, "decision.not-found", "", "decisions --all"},
		{[]string{"dec-3", "--confirmation", "x"}, "decision.choice-required", "", "wa-cli help decide --json"},
		{[]string{"dec-3", "--option", "z", "--confirmation", "x"}, "decision.unknown-option", "choice", "wa-cli help decide --json"},
	} {
		code, res := runDec(t, dir, append([]string{"decide"}, tc.args...)...)
		if d := diag(t, res.result, tc.code); code != 1 || d.Field != tc.field || !strings.Contains(d.Next[0], tc.next) {
			t.Errorf("%v: code = %d, %+v", tc.args, code, d)
		}
	}
	if readFile(t, wsPath(dir, decisionsPath)) != before {
		t.Error("rejected decide changed decisions.yaml")
	}
}

func TestDecisionBlocksAdvance(t *testing.T) {
	dir := newWork(t)
	writeFile(t, wsPath(dir, ".writing-anything/works/my-post/brief.yaml"), completeBrief)
	runDec(t, dir, "decision", "add", "--question", "非阻塞", "--unit", "my-post")
	runDec(t, dir, "decision", "add", "--question", "标题", "--option", "甲", "--blocking", "--unit", "my-post")
	before := readFile(t, workFile(dir, "my-post"))
	code, res := runUnits(t, dir, "advance", "my-post")
	d := diag(t, res.result, "decision.blocking")
	if code != 1 || d.Path != decisionsPath || d.Next[0] != shellCommand("wa-cli", "decisions", "--dir", dir, "--json") || readFile(t, workFile(dir, "my-post")) != before {
		t.Fatalf("advance: code = %d, %+v", code, d)
	}
	// next 与 status 给出同一阻塞项与查看决策队列的命令。
	_, res = runUnits(t, dir, "next", "my-post")
	s := res.Steps[0]
	b := s["blockers"].([]any)[0].(map[string]any)
	if b["code"] != "decision.blocking" || b["artifact"] != "decisions" || b["decision"] != "dec-2" ||
		!slices.Equal(toStrings(s["commands"]), []string{shellCommand("wa-cli", "decisions", "--dir", dir, "--json")}) || !strings.Contains(s["action"].(string), "dec-2") {
		t.Fatalf("next step = %v", s)
	}
	if _, res = runUnits(t, dir, "status"); res.Units[0]["blockers"].([]any)[0].(map[string]any)["decision"] != "dec-2" {
		t.Errorf("status units = %v", res.Units)
	}
	if code, stdout, _ := run(t, "next", "my-post", "--dir", dir); code != 0 || !strings.Contains(stdout, "请作者决定阻塞决策（Blocking）dec-2") {
		t.Errorf("text next = %q", stdout)
	}
	// 缺失产物的阻塞项 decision 为 null。
	runWS(t, dir, "unit", "add", "my-post", "section", "intro")
	if _, res = runUnits(t, dir, "next", "my-post/intro"); res.Steps[0]["blockers"].([]any)[0].(map[string]any)["decision"] != nil {
		t.Errorf("missing-artifact blocker = %v", res.Steps[0]["blockers"])
	}
	runDec(t, dir, "decide", "dec-2", "--option", "a", "--confirmation", "作者同意")
	if code, res = runUnits(t, dir, "advance", "my-post"); code != 0 || res.Unit["stage"] != "draft" {
		t.Fatalf("after decide: code = %d, %v", code, res.Diagnostics)
	}
	// 决策队列损坏时偏安全地阻塞。
	writeFile(t, wsPath(dir, decisionsPath), "a: [\n")
	writeFile(t, wsPath(dir, ".writing-anything/works/my-post/draft.md"), "正文")
	code, res = runUnits(t, dir, "advance", "my-post")
	if d := diag(t, res.result, "decision.invalid-queue"); code != 1 || d.Next[0] != shellCommand("wa-cli", "decisions", "--dir", dir, "--json") || !slices.Contains(d.Next, shellCommand("wa-cli", "validate", "--dir", dir, "--json")) {
		t.Errorf("invalid queue: code = %d, %+v", code, d)
	}
	if _, stdout, _ := run(t, "next", "my-post", "--dir", dir); !strings.Contains(stdout, "先修复无法加载的决策队列（Decision Queue）") {
		t.Errorf("text next invalid = %q", stdout)
	}
}

func toStrings(v any) []string {
	var out []string
	for _, s := range v.([]any) {
		out = append(out, s.(string))
	}
	return out
}

func TestDecisionsValidateAndText(t *testing.T) {
	dir := newWork(t)
	runDec(t, dir, "decision", "add", "--question", "q", "--unit", "my-post")
	if code, res := runWS(t, dir, "validate"); code != 0 || !slices.Contains(res.Checked, decisionsPath) {
		t.Fatalf("checks-decisions: code = %d, %v", code, res.Checked)
	}
	writeFile(t, wsPath(dir, decisionsPath), strings.Replace(readFile(t, wsPath(dir, decisionsPath)), "[my-post]", "[my-post/nope]", 1))
	code, res := runWS(t, dir, "validate")
	d := diag(t, res, "decision.unknown-unit")
	if code != 1 || d.Path != decisionsPath || d.Field != "decisions[0].units[0]" || d.Next[0] != shellCommand("wa-cli", "validate", "--dir", dir, "--json") {
		t.Errorf("unknown unit: code = %d, %+v", code, d)
	}
	if code, res = runWS(t, dir, "validate", decisionsPath); code != 1 {
		t.Errorf("single: code = %d", code)
	} else {
		diag(t, res, "decision.unknown-unit")
	}
	writeFile(t, wsPath(dir, decisionsPath), "schema_version: 1\ndecisions:\n  - {id: dec-1, question: q, options: [], blocking: true, units: [], status: decided, decided_at: \"2026-09-30T12:00:00Z\"}\n")
	if _, res = runWS(t, dir, "validate"); diag(t, res, "artifact.schema-violation").Field != "decisions[0].confirmation" {
		t.Errorf("decided without confirmation: %v", res.Diagnostics)
	}
	fresh := newWork(t)
	for _, tc := range []struct {
		args []string
		want string
	}{
		{[]string{"decisions"}, "没有待决的事项"},
		{[]string{"decision", "add", "--question", "标题", "--option", "甲", "--option", "乙", "--recommend", "b", "--rationale", "更好", "--blocking", "--unit", "my-post"}, "B. 乙（推荐）"},
		{[]string{"decision", "add", "--question", "字数"}, "dec-2：字数"},
		{[]string{"decisions"}, "阻塞（Blocking）的决策"},
		{[]string{"decide", "dec-1", "--option", "b", "--answer", "就这个", "--confirmation", "作者同意"}, "已决定：选择 B，答复：就这个；作者确认：作者同意"},
		{[]string{"decide", "dec-2", "--answer", "3000 字", "--confirmation", "作者确认"}, "已决定：答复：3000 字"},
		{[]string{"decisions", "--all"}, "关联单元：my-post"},
	} {
		if code, stdout, stderr := run(t, append(tc.args, "--dir", fresh)...); code != 0 || !strings.Contains(stdout, tc.want) || stderr != "" {
			t.Errorf("%v: code = %d, %q, %q", tc.args, code, stdout, stderr)
		}
	}
	for _, args := range [][]string{{"decision", "add"}, {"decide", "dec-9", "--confirmation", "x"}} {
		if code, stdout, _ := run(t, append(args, "--dir", fresh)...); code != 1 || stdout != "" {
			t.Errorf("%v: code = %d, %q", args, code, stdout)
		}
	}
}

// 冲突（Conflict）决策：结构化载荷登记进既有决策队列，未决时阻塞 advance、lint 对引用任一方引文报 error，
// 作者 decide 后发现项消失；素材不存在与半组参数被拒。
func TestDecisionConflict(t *testing.T) {
	dir := newWork(t)
	runWSIn(t, dir, "作者笔记", "source", "add", "--type", "author-note", "--id", "src-note")
	runWSIn(t, dir, "报道", "source", "add", "--type", "reference", "--id", "src-news")
	// 半组参数被拒（载荷成组校验）。
	code, res := runDec(t, dir, "decision", "add", "--question", "q", "--conflict-about", "喷嘴数量",
		"--conflict-a", "src-note", "--conflict-a-quote", "10 个喷嘴")
	if d := diag(t, res.result, "artifact.schema-violation"); code != 1 || d.Field != "conflict" {
		t.Fatalf("half conflict: code = %d, diag = %+v", code, d)
	}
	// 素材不存在。
	code, res = runDec(t, dir, "decision", "add", "--question", "q", "--conflict-about", "喷嘴数量",
		"--conflict-a", "nope", "--conflict-a-quote", "甲", "--conflict-b", "src-news", "--conflict-b-quote", "乙")
	if d := diag(t, res.result, "decision.unknown-source"); code != 1 || d.Field != "conflict-a" {
		t.Fatalf("unknown source: code = %d, diag = %+v", code, d)
	}
	if _, err := os.Stat(wsPath(dir, decisionsPath)); err == nil {
		t.Fatal("rejected conflict wrote decisions.yaml")
	}
	// 先推进到草稿阶段并写入引用一方引文的正文（冲突登记后再推进会被阻塞，先写稿后登记）。
	writeFile(t, wsPath(dir, ".writing-anything/works/my-post/brief.yaml"), completeBrief)
	runDec(t, dir, "advance", "my-post")
	if code, _, stderr := runIn(t, "官方说法是 10 个喷嘴。", "draft", "write", "my-post", "--dir", dir); code != 0 {
		t.Fatalf("draft write failed: %s", stderr)
	}
	// 登记成功：载荷写入 decisions.yaml 并出现在视图。
	code, res = runDec(t, dir, "decision", "add", "--question", "喷嘴数量两说矛盾，请作者裁定",
		"--conflict-about", "喷嘴数量", "--conflict-a", "src-note", "--conflict-a-quote", "10 个喷嘴",
		"--conflict-b", "src-news", "--conflict-b-quote", "4 个推进器", "--unit", "my-post", "--blocking")
	d := res.Decision
	if code != 0 || d == nil || d.Conflict == nil || d.Conflict["about"] != "喷嘴数量" {
		t.Fatalf("add conflict: code = %d, decision = %+v, %v", code, d, res.Diagnostics)
	}
	saved := readFile(t, wsPath(dir, decisionsPath))
	if !strings.Contains(saved, "about: 喷嘴数量") || !strings.Contains(saved, "quote: 10 个喷嘴") {
		t.Fatalf("decisions.yaml:\n%s", saved)
	}
	// 未决冲突阻塞 advance（既有 blocking 决策机制）。
	if code, res = runDec(t, dir, "advance", "my-post"); code != 1 || !hasNext(diag(t, res.result, "decision.blocking"), "wa-cli decisions") {
		t.Fatalf("advance blocked: code = %d, %+v", code, res.Diagnostics)
	}
	// lint：正文引用了 open 冲突一方引文 → conflict-sources error。
	if code, res = runDec(t, dir, "lint", "my-post"); code != 1 || !strings.Contains(res.stdout, "conflict-sources") ||
		!strings.Contains(res.stdout, "喷嘴数量") || !strings.Contains(res.stdout, "dec-1") ||
		!strings.Contains(res.stdout, "src-note") || !strings.Contains(res.stdout, "src-news") {
		t.Fatalf("lint with open conflict: code = %d, stdout = %s", code, res.stdout)
	}
	// 作者 decide 后发现项消失。
	runDec(t, dir, "decide", "dec-1", "--answer", "采信笔记", "--confirmation", "作者：以笔记为准")
	if code, res = runDec(t, dir, "lint", "my-post"); code != 0 || !res.Valid || len(res.Diagnostics) != 0 {
		t.Fatalf("lint after decide: code = %d, diagnostics = %+v", code, res.Diagnostics)
	}
	// decisions.yaml 通过整体校验。
	if code, res = runDec(t, dir, "validate"); code != 0 {
		t.Fatalf("validate: code = %d, %v", code, res.Diagnostics)
	}
}
