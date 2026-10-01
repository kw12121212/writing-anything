package cli

import (
	"encoding/json"
	"path/filepath"
	"slices"
	"strings"
	"testing"
)

type methodResult struct {
	result
	Methods []map[string]any `json:"methods"`
	Method  map[string]any   `json:"method"`
}

func runMethod(t *testing.T, dir string, args ...string) (int, methodResult) {
	t.Helper()
	code, res := runWS(t, dir, args...)
	var out methodResult
	if err := json.Unmarshal([]byte(res.stdout), &out); err != nil {
		t.Fatal(err)
	}
	out.result = res
	return code, out
}

const methodPack = `schema_version: 1
id: house
title: 团队规范
methods:
  - id: three-act
    title: 三幕结构
    kind: structure
    beats:
      - {id: setup, title: 建置, position: {min: 0, max: 0.3}}
      - {id: climax, title: 高潮, position: {min: 0.7, max: 1}}
  - id: general-specific-general
    title: 团队版总分总
    kind: article
    sections:
      - {id: overview, title: 总述}
      - {id: summary, title: 总结}
`

// 内置写作法与规范包定义的写作法（method-list、method-show）。
func TestMethodListShow(t *testing.T) {
	dir := newWorkspace(t)
	code, res := runMethod(t, dir, "method", "list")
	var ids []string
	for _, m := range res.Methods {
		ids = append(ids, m["id"].(string))
		if m["kind"] != "article" || m["source"] != "builtin" || len(m["sections"].([]any)) == 0 {
			t.Errorf("builtin method %v", m)
		}
	}
	want := []string{"aida", "diataxis-explanation", "diataxis-how-to", "diataxis-reference", "diataxis-tutorial", "general-specific-general", "inverted-pyramid", "pas", "pyramid-scqa"}
	if code != 0 || !slices.Equal(ids, want) {
		t.Fatalf("method list: code = %d, ids = %v", code, ids)
	}
	code, res = runMethod(t, dir, "method", "show", "pyramid-scqa")
	if code != 0 || res.Method["title"] != "金字塔原理与 SCQA" || len(res.Method["sections"].([]any)) != 5 || len(res.Method["beats"].([]any)) != 0 || len(res.Method["rubric"].([]any)) != 1 {
		t.Fatalf("method show: %+v", res.Method)
	}
	code, res = runMethod(t, dir, "method", "show", "nope")
	if d := diag(t, res.result, "method.not-found"); code != 1 || res.Method != nil || !hasNext(d, "wa-cli method list") {
		t.Fatalf("not found: %+v", res.Diagnostics)
	}
	// 规范包的写作法：同 id 覆盖内置写作法，结构类带位置范围。
	writePack(t, dir, "house", methodPack)
	mustRunWS(t, dir, "config", "set", "packs", "house")
	_, res = runMethod(t, dir, "method", "list")
	got := map[string]map[string]any{}
	for _, m := range res.Methods {
		got[m["id"].(string)] = m
	}
	if got["general-specific-general"]["source"] != "pack:house" || got["three-act"]["kind"] != "structure" || len(res.Methods) != 10 {
		t.Fatalf("pack methods: %v", got)
	}
	if _, res = runMethod(t, dir, "method", "show", "three-act"); res.Method["beats"].([]any)[1].(map[string]any)["position"].(map[string]any)["min"] != 0.7 {
		t.Fatalf("beats: %+v", res.Method)
	}
	// 规范包中写作法的问题由整体校验报告。
	writePack(t, dir, "house", strings.Replace(methodPack, "min: 0.7, max: 1", "min: 0.9, max: 0.1", 1))
	if code, res := runWS(t, dir, "validate"); code != 1 || diag(t, res, "method.invalid-position").Field != "methods[0].beats[1].position" {
		t.Fatalf("invalid position: %+v", res.Diagnostics)
	}
	for _, args := range [][]string{{"method", "list"}, {"method", "show", "three-act"}} {
		if code, stdout, _ := run(t, append(args, "--dir", dir)...); code != 0 || !strings.Contains(stdout, "三幕结构") {
			t.Errorf("%v text: %q", args, stdout)
		}
	}
}

// 写作简报选用写作法，大纲按写作法检查章节、顺序与 role（method-outline-check）。
func TestMethodOutlineCheck(t *testing.T) {
	dir := newWork(t)
	ws := func(rel string) string { return filepath.Join(dir, ".writing-anything", "works", "my-post", rel) }
	writeFile(t, ws("brief.yaml"), completeBrief)
	if code, res := runWS(t, dir, "brief", "set", "my-post", "--method", "nope"); code != 1 || diag(t, res, "brief.unknown-method").Field != "method" {
		t.Fatalf("unknown method: %+v", res.Diagnostics)
	}
	mustRunWS(t, dir, "brief", "set", "my-post", "--method", "general-specific-general")
	if _, res := runWS(t, dir, "brief", "show", "my-post"); !strings.Contains(res.stdout, `"method":"general-specific-general"`) {
		t.Fatalf("brief show: %s", res.stdout)
	}
	mustRunWS(t, dir, "outline", "set", "my-post", "a", "--title", "分述一", "--role", "details", "--reason", "展开")
	if code, res := runWS(t, dir, "outline", "set", "my-post", "b", "--title", "x", "--role", "nope", "--reason", "x"); code != 1 || diag(t, res, "outline.unknown-role").Field != "role" {
		t.Fatalf("unknown role param: %+v", res.Diagnostics)
	}
	// 缺少总述与总结：validate 以非零退出。
	code, res := runWS(t, dir, "validate")
	var missing int
	for _, d := range res.Diagnostics {
		if d.Code == "outline.missing-method-part" {
			missing++
		}
	}
	if code != 1 || missing != 2 {
		t.Fatalf("missing parts: code = %d, %+v", code, res.Diagnostics)
	}
	// 总结排在总述之前：顺序缺口；补齐并调整顺序后通过。
	mustRunWS(t, dir, "outline", "set", "my-post", "z", "--title", "总结", "--role", "summary", "--reason", "收尾", "--before", "a")
	mustRunWS(t, dir, "outline", "set", "my-post", "o", "--title", "总述", "--role", "overview", "--reason", "开头")
	code, res = runWS(t, dir, "outline", "show", "my-post")
	if code != 0 || !strings.Contains(res.stdout, "outline.method-order") || !strings.Contains(res.stdout, `"role":"overview"`) {
		t.Fatalf("order gap: %s", res.stdout)
	}
	if code, res := runWS(t, dir, "validate", ".writing-anything/works/my-post/outline.yaml"); code != 1 || diag(t, res, "outline.method-order").Field != "sections[1].role" || len(res.Diagnostics) != 2 {
		t.Fatalf("validate order: %+v", res.Diagnostics)
	}
	mustRunWS(t, dir, "outline", "set", "my-post", "o", "--before", "z")
	mustRunWS(t, dir, "outline", "set", "my-post", "z", "--after", "a")
	if code, res := runWS(t, dir, "validate"); code != 0 {
		t.Fatalf("complete outline: %+v", res.Diagnostics)
	}
	// 不用写作法时不检查；写作法被移除后，写作简报报告 brief.unknown-method，仍可用 brief set 改选。
	writeFile(t, ws("outline.yaml"), "schema_version: 1\nsections:\n  - {id: a, title: 一, reason: x}\n")
	mustRunWS(t, dir, "brief", "set", "my-post", "--method", "none")
	if code, res := runWS(t, dir, "validate"); code != 0 {
		t.Fatalf("method none: %+v", res.Diagnostics)
	}
	writeFile(t, ws("brief.yaml"), completeBrief+"method: gone\n")
	if code, res := runWS(t, dir, "validate"); code != 1 || diag(t, res, "brief.unknown-method").Path != ".writing-anything/works/my-post/brief.yaml" {
		t.Fatalf("dangling method: %+v", res.Diagnostics)
	}
	mustRunWS(t, dir, "brief", "set", "my-post", "--method", "")
	if code, res := runWS(t, dir, "validate"); code != 0 || strings.Contains(readFile(t, ws("brief.yaml")), "method") {
		t.Fatalf("cleared method: %+v", res.Diagnostics)
	}
}

// 结构类写作法：成稿中节拍的位置超出范围时只给出 warning；写作法附带的评分项进入审稿的评分标准。
func TestMethodBeatsAndRubric(t *testing.T) {
	dir := reviewWork(t)
	writePack(t, dir, "house", methodPack)
	mustRunWS(t, dir, "config", "set", "packs", "house")
	mustRunWS(t, dir, "brief", "set", "my-post", "--method", "three-act")
	mustRunWS(t, dir, "outline", "set", "my-post", "s", "--title", "开端", "--role", "setup", "--reason", "x")
	mustRunWS(t, dir, "outline", "set", "my-post", "c", "--title", "高潮", "--role", "climax", "--reason", "x")
	draft := "## 高潮\n\n" + strings.Repeat("内容很长。", 40) + "\n\n## 开端\n\n结尾。\n"
	if code, res := runWSIn(t, dir, draft, "draft", "write", "my-post"); code != 0 {
		t.Fatalf("draft: %+v", res.Diagnostics)
	}
	code, res := runWS(t, dir, "lint", "my-post")
	var beats []Diagnostic
	for _, d := range res.Diagnostics {
		if d.Code == "outline.beat-position" {
			beats = append(beats, d)
		}
	}
	if code != 0 || len(beats) != 2 || beats[0].Severity != "warning" || !strings.Contains(beats[0].Message, "第 5 行") || !strings.Contains(beats[1].Message, "第 1 行") ||
		!hasNext(beats[0], "wa-cli lint my-post") {
		t.Fatalf("beat positions: code = %d, %+v", code, res.Diagnostics)
	}
	// review show 同样展示节拍位置的 warning，它们不是验收的阻塞项。
	if _, rs := runReview(t, dir, "", "review", "show", "my-post"); !hasNext(diag(t, rs.result, "outline.beat-position"), "wa-cli lint my-post") ||
		slices.Contains(gateCodes(rs), "outline.beat-position") {
		t.Fatalf("review show beats: %+v", rs.Diagnostics)
	}
	// 写作法附带的评分项：pyramid-scqa 的 conclusion-first 需要审稿结论。
	mustRunWS(t, dir, "brief", "set", "my-post", "--method", "pyramid-scqa")
	in := "rubric:\n  - {item: clarity, verdict: pass, evidence: x, paragraphs: [1]}\n"
	if code, rr := runReview(t, dir, in, "review", "record", "my-post"); code != 1 || !strings.Contains(diag(t, rr.result, "review.missing-verdict").Message, "conclusion-first") {
		t.Fatalf("method rubric: %+v", rr.Diagnostics)
	}
}

// 写作法被移除后：查看与修改写作简报和大纲的命令照常执行，大纲命令以 warning 报告；validate 与 next 报告问题并给出可执行的
// 下一步（cross-artifact-references、next-rechecks-problem）。
func TestMethodDanglingAndNext(t *testing.T) {
	dir := newWork(t)
	writeFile(t, filepath.Join(dir, ".writing-anything", "works", "my-post", "brief.yaml"), completeBrief)
	writePack(t, dir, "house", methodPack)
	mustRunWS(t, dir, "config", "set", "packs", "house")
	mustRunWS(t, dir, "brief", "set", "my-post", "--method", "three-act")
	mustRunWS(t, dir, "outline", "set", "my-post", "s", "--title", "开端", "--role", "setup", "--reason", "x")
	// 缺少节拍 climax：outline show 的缺口与 validate 的诊断都给出 outline show 与 method list。
	code, res := runWS(t, dir, "validate")
	if d := diag(t, res, "outline.missing-method-part"); code != 1 || !hasNext(d, "wa-cli outline show my-post") || !hasNext(d, "wa-cli method list") {
		t.Fatalf("missing part next: %+v", res.Diagnostics)
	}
	mustRunWS(t, dir, "config", "set", "packs", "default")
	if code, res := runWS(t, dir, "context", "my-post"); code != 0 {
		t.Errorf("context after removing the pack: code = %d, %+v", code, res.Diagnostics)
	}
	for _, args := range [][]string{{"brief", "show", "my-post"}, {"brief", "set", "my-post", "--audience", "运维"}} {
		code, res := runWS(t, dir, args...)
		if d := diag(t, res, "brief.unknown-method"); code != 0 || d.Severity != "warning" || !hasNext(d, "wa-cli method list") {
			t.Errorf("%v after removing the pack: code = %d, %+v", args, code, res.Diagnostics)
		}
	}
	for _, args := range [][]string{{"outline", "show", "my-post"}, {"outline", "set", "my-post", "s", "--title", "开头"}} {
		code, res := runWS(t, dir, args...)
		if d := diag(t, res, "brief.unknown-method"); code != 0 || d.Severity != "warning" {
			t.Errorf("%v after removing the pack: code = %d, %+v", args, code, res.Diagnostics)
		}
	}
	code, res = runWS(t, dir, "validate")
	if d := diag(t, res, "brief.unknown-method"); code != 1 || !hasNext(d, "wa-cli method list") {
		t.Fatalf("validate dangling: %+v", res.Diagnostics)
	}
	// 写作法的 id 不能是保留的 none；规范包写作法省略 description 时输出空字符串。
	writePack(t, dir, "house", strings.Replace(methodPack, "id: three-act", "id: none", 1))
	mustRunWS(t, dir, "config", "set", "packs", "house")
	if code, res := runWS(t, dir, "validate"); code != 1 || diag(t, res, "method.reserved-id").Field != "methods[0].id" {
		t.Fatalf("reserved none: %+v", res.Diagnostics)
	}
	if _, res := runMethod(t, dir, "method", "show", "general-specific-general"); res.Method["description"] != "" {
		t.Fatalf("description: %+v", res.Method)
	}
	// --role 格式不合法时报告 artifact.schema-violation；--method 不可用时 brief.yaml 不变。
	if code, res := runWS(t, dir, "outline", "set", "my-post", "s", "--role", "Bad_Role"); code != 1 || diag(t, res, "artifact.schema-violation").Field != "role" {
		t.Fatalf("bad role: %+v", res.Diagnostics)
	}
	before := readFile(t, filepath.Join(dir, ".writing-anything", "works", "my-post", "brief.yaml"))
	runWS(t, dir, "brief", "set", "my-post", "--method", "nope")
	if readFile(t, filepath.Join(dir, ".writing-anything", "works", "my-post", "brief.yaml")) != before {
		t.Fatal("rejected --method changed brief.yaml")
	}
}
