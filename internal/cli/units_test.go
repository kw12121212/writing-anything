package cli

import (
	"encoding/json"
	"os"
	"path/filepath"
	"slices"
	"strings"
	"testing"
	"time"
)

// newWork 返回一个已初始化的临时工作区，其中用 wa-cli new minimal my-post 创建了作品。
func newWork(t *testing.T) string {
	t.Helper()
	dir := newWorkspace(t)
	if code, stdout, stderr := run(t, "new", "minimal", "my-post", "--dir", dir); code != 0 {
		t.Fatalf("new: exit code = %d; stdout: %s; stderr: %s", code, stdout, stderr)
	}
	return dir
}

func workFile(dir, work string) string {
	return filepath.Join(dir, ".writing-anything", "works", work, "work.yaml")
}

// artifact 在单元目录中写入必需产物；unit 为空时写到根单元的作品目录。
func artifact(t *testing.T, dir, work, unit, name string) {
	t.Helper()
	d := filepath.Join(dir, ".writing-anything", "works", work)
	if unit != "" {
		d = filepath.Join(d, "units", unit)
	}
	if err := os.MkdirAll(d, 0o755); err != nil {
		t.Fatal(err)
	}
	writeFile(t, filepath.Join(d, name), "k: v\n")
}

// unitResult 是单元命令 --json 输出中测试关心的部分。
type unitResult struct {
	result
	Work  map[string]any   `json:"work"`
	Unit  map[string]any   `json:"unit"`
	Steps []map[string]any `json:"steps"`
	Units []map[string]any `json:"units"`
}

// runUnits 以 --json 运行命令，复用 runWS 的路径断言，并解析单元命令专属字段；同时断言输出中没有 null
// 数组（cli-contract/shared-envelope）。
func runUnits(t *testing.T, dir string, args ...string) (int, unitResult) {
	t.Helper()
	code, res := runWS(t, dir, args...)
	var out unitResult
	if err := json.Unmarshal([]byte(res.stdout), &out); err != nil {
		t.Fatal(err)
	}
	var v any
	json.Unmarshal([]byte(res.stdout), &v)
	noNulls(t, v, "")
	out.result = res
	return code, out
}

// noNulls 断言 JSON 值中只有可为 null 的字段（parent、work、unit、config、defaultGenre）为 null，数组永不为 null。
func noNulls(t *testing.T, v any, path string) {
	t.Helper()
	switch v := v.(type) {
	case map[string]any:
		for k, c := range v {
			if c == nil && !slices.Contains([]string{"parent", "work", "unit", "config", "defaultGenre"}, k) {
				t.Errorf("%s.%s is null", path, k)
			}
			noNulls(t, c, path+"."+k)
		}
	case []any:
		for _, c := range v {
			noNulls(t, c, path+"[]")
		}
	}
}

func blockerCodes(step map[string]any) []string {
	var out []string
	for _, b := range step["blockers"].([]any) {
		out = append(out, b.(map[string]any)["code"].(string))
	}
	return out
}

func commands(step map[string]any) []string {
	var out []string
	for _, c := range step["commands"].([]any) {
		out = append(out, c.(string))
	}
	return out
}

func TestNew(t *testing.T) {
	dir := newWorkspace(t)
	code, res := runUnits(t, dir, "new", "minimal", "my-post")
	if code != 0 || !res.Valid || !slices.Equal(res.ChangedPaths, []string{".writing-anything/works/my-post", ".writing-anything/works/my-post/work.yaml"}) {
		t.Fatalf("new-article: exit code = %d, result = %+v", code, res)
	}
	if res.Work["id"] != "my-post" || res.Work["genre"] != "minimal" || res.Work["type"] != "article" || res.Work["stage"] != "brief" {
		t.Fatalf("work = %v", res.Work)
	}
	src := readFile(t, workFile(dir, "my-post"))
	for _, want := range []string{"id: my-post", "genre: minimal", "type: article", "stage: brief", "units: []"} {
		if !strings.Contains(src, want) {
			t.Errorf("work.yaml lacks %q:\n%s", want, src)
		}
	}
	if code, res := runWS(t, dir, "validate"); code != 0 || !res.Valid || !slices.Contains(res.Checked, ".writing-anything/works/my-post/work.yaml") {
		t.Fatalf("validate after new (checks-work-files): exit code = %d, result = %+v", code, res)
	}

	code, res = runUnits(t, dir, "new", "minimal", "my-post")
	if d := diag(t, res.result, "work.exists"); code != 1 || d.Path != ".writing-anything/works/my-post" || readFile(t, workFile(dir, "my-post")) != src {
		t.Fatalf("new-existing-work: exit code = %d, diagnostic = %+v", code, d)
	}
	code, res = runUnits(t, dir, "new", "no-such-genre", "other")
	if d := diag(t, res.result, "work.unknown-genre"); code != 1 || res.Valid || !strings.Contains(d.Message, "minimal") || !slices.Contains(d.Next, "wa-cli new minimal other --dir "+shellCommand(dir)+" --json") {
		t.Fatalf("new-unknown-genre: exit code = %d, diagnostic = %+v", code, d)
	}
	code, res = runUnits(t, dir, "new", "minimal", "My_Post")
	if d := diag(t, res.result, "artifact.schema-violation"); code != 1 || d.Field != "id" {
		t.Fatalf("new-invalid-slug: exit code = %d, diagnostic = %+v", code, d)
	}
	if entries, _ := os.ReadDir(filepath.Join(dir, ".writing-anything", "works")); len(entries) != 1 {
		t.Fatalf("works/ has %d entries, want only my-post", len(entries))
	}
	bare := t.TempDir()
	code, res = runUnits(t, bare, "new", "minimal", "my-post")
	if diag(t, res.result, "workspace.not-found"); code != 1 {
		t.Fatalf("new-no-workspace: exit code = %d", code)
	}
	if entries, _ := os.ReadDir(bare); len(entries) != 0 {
		t.Fatalf("new-no-workspace created %d entries in D", len(entries))
	}
	// 没有路径的诊断，message 不以「：」开头。
	if d := diag(t, res.result, "workspace.not-found"); strings.HasPrefix(d.Message, "：") {
		t.Errorf("message %q starts with a colon", d.Message)
	}
	if _, res := runUnits(t, dir, "new", "no-such-genre", "x"); strings.HasPrefix(diag(t, res.result, "work.unknown-genre").Message, "：") {
		t.Errorf("unknown-genre message starts with a colon")
	}
	// 文本模式输出人读摘要。
	if code, stdout, _ := run(t, "new", "minimal", "second", "--dir", dir); code != 0 || !strings.Contains(stdout, "已创建作品（Work）second") {
		t.Fatalf("text: exit code = %d, stdout = %q", code, stdout)
	}
}

func TestWorkValidation(t *testing.T) {
	dir := newWork(t)
	code, res := runWS(t, dir, "validate", ".writing-anything/works/my-post/work.yaml")
	if code != 0 || !res.Valid || !slices.Equal(res.Checked, []string{".writing-anything/works/my-post/work.yaml"}) {
		t.Fatalf("single-work: exit code = %d, result = %+v", code, res)
	}
	orig := readFile(t, workFile(dir, "my-post"))
	for _, tc := range []struct {
		name, from, to, code, field string
	}{
		{"unknown-unit-type", "units: []", "units:\n  - {id: a, type: chapter, stage: brief}", "work.unknown-unit-type", "units[0].type"},
		{"child-type-not-allowed", "units: []", "units:\n  - id: a\n    type: section\n    stage: brief\n    units:\n      - {id: b, type: section, stage: brief}", "work.child-type-not-allowed", "units[0].units[0].type"},
		{"unknown-stage", "stage: brief", "stage: bogus", "work.unknown-stage", "stage"},
		{"duplicate-unit-id", "units: []", "units:\n  - {id: intro, type: section, stage: brief}\n  - {id: intro, type: section, stage: brief}", "artifact.duplicate-id", "units[1].id"},
		{"id-mismatch", "id: my-post", "id: other", "work.id-mismatch", "id"},
		{"root-type-mismatch", "type: article", "type: section", "work.root-type-mismatch", "type"},
	} {
		t.Run(tc.name, func(t *testing.T) {
			writeFile(t, workFile(dir, "my-post"), strings.Replace(orig, tc.from, tc.to, 1))
			for _, args := range [][]string{{"validate"}, {"validate", ".writing-anything/works/my-post/work.yaml"}} {
				code, res := runWS(t, dir, args...)
				d := diag(t, res, tc.code)
				if code != 1 || d.Path != ".writing-anything/works/my-post/work.yaml" || d.Field != tc.field {
					t.Fatalf("%v: exit code = %d, diagnostic = %+v", args, code, d)
				}
				// next 的复查命令能重新检出该问题（cli-contract/next-rechecks-problem）。
				if !slices.Contains(d.Next, "wa-cli validate .writing-anything/works/my-post/work.yaml --dir "+shellCommand(dir)+" --json") {
					t.Errorf("next = %v", d.Next)
				}
			}
		})
	}
	writeFile(t, workFile(dir, "my-post"), orig)
	os.Mkdir(filepath.Join(dir, ".writing-anything", "works", "draft-notes"), 0o755)
	code, res = runWS(t, dir, "validate")
	if d := diag(t, res, "artifact.not-found"); code != 1 || res.Valid || d.Path != ".writing-anything/works/draft-notes/work.yaml" || !slices.Contains(d.Next, "wa-cli help new --json") {
		t.Fatalf("missing-work-file: exit code = %d, diagnostic = %+v", code, d)
	}
}

func TestUnitReference(t *testing.T) {
	dir := newWork(t)
	code, res := runUnits(t, dir, "advance", "my-post/nope")
	if d := diag(t, res.result, "unit.not-found"); code != 1 || res.Valid || !slices.Contains(d.Next, "wa-cli status --dir "+shellCommand(dir)+" --json") {
		t.Fatalf("unit-not-found: exit code = %d, diagnostic = %+v", code, d)
	}
	before := readFile(t, workFile(dir, "my-post"))
	code, res = runUnits(t, dir, "advance", "nope")
	if d := diag(t, res.result, "work.not-found"); code != 1 || !slices.Contains(d.Next, "wa-cli status --dir "+shellCommand(dir)+" --json") || len(res.ChangedPaths) != 0 {
		t.Fatalf("work-not-found: exit code = %d, diagnostic = %+v", code, d)
	}
	if readFile(t, workFile(dir, "my-post")) != before {
		t.Fatal("work-not-found modified work.yaml")
	}
	if entries, _ := os.ReadDir(filepath.Join(dir, ".writing-anything", "works")); len(entries) != 1 {
		t.Fatal("work-not-found created files")
	}
	runUnits(t, dir, "unit", "add", "my-post", "section", "intro")
	code, res = runUnits(t, dir, "next", "my-post/intro")
	if code != 0 || len(res.Steps) != 1 || res.Steps[0]["unit"] != "my-post/intro" || res.Steps[0]["stage"] != "brief" {
		t.Fatalf("child-reference: exit code = %d, steps = %v", code, res.Steps)
	}
}

func TestUnitAdd(t *testing.T) {
	dir := newWork(t)
	writeFile(t, workFile(dir, "my-post"), "# 作者的注释\n"+readFile(t, workFile(dir, "my-post")))
	code, res := runUnits(t, dir, "unit", "add", "my-post", "section", "intro")
	if code != 0 || !slices.Equal(res.ChangedPaths, []string{".writing-anything/works/my-post/work.yaml"}) ||
		res.Unit["unit"] != "my-post/intro" || res.Unit["parent"] != "my-post" || res.Unit["type"] != "section" || res.Unit["stage"] != "brief" {
		t.Fatalf("add-section: exit code = %d, result = %+v", code, res)
	}
	src := readFile(t, workFile(dir, "my-post"))
	if !strings.Contains(src, "# 作者的注释") || !strings.Contains(src, "  - id: intro\n    type: section\n    stage: brief") {
		t.Fatalf("work.yaml =\n%s", src)
	}
	for _, tc := range []struct {
		args []string
		code string
	}{
		{[]string{"my-post", "article", "nested"}, "work.child-type-not-allowed"},
		{[]string{"my-post", "chapter", "c1"}, "work.child-type-not-allowed"},
		{[]string{"my-post/intro", "section", "deep"}, "work.child-type-not-allowed"},
		{[]string{"my-post", "section", "intro"}, "artifact.duplicate-id"},
	} {
		code, res := runUnits(t, dir, append([]string{"unit", "add"}, tc.args...)...)
		d := diag(t, res.result, tc.code)
		if code != 1 || readFile(t, workFile(dir, "my-post")) != src || !slices.Contains(d.Next, "wa-cli help unit add --json") {
			t.Errorf("unit add %v: exit code = %d, diagnostic = %+v", tc.args, code, d)
		}
		if tc.args[0] == "my-post" && tc.code == "work.child-type-not-allowed" && (!strings.Contains(d.Message, "section") || strings.Contains(d.Message, "article、")) {
			t.Errorf("message %q must list the allowed child types", d.Message)
		}
	}
	// 父单元所在的作品本身不合法时，按作品校验问题给出复查命令。
	writeFile(t, workFile(dir, "my-post"), strings.Replace(src, "stage: brief", "stage: bogus", 1))
	if code, res := runUnits(t, dir, "unit", "add", "my-post", "section", "x"); code != 1 || !slices.Contains(diag(t, res.result, "work.unknown-stage").Next, "wa-cli validate .writing-anything/works/my-post/work.yaml --dir "+shellCommand(dir)+" --json") {
		t.Fatalf("invalid work: exit code = %d, result = %+v", code, res)
	}
	writeFile(t, workFile(dir, "my-post"), src)
	if code, stdout, _ := run(t, "unit", "add", "my-post", "section", "outro", "--dir", dir); code != 0 || !strings.Contains(stdout, "my-post/outro") {
		t.Fatalf("text: exit code = %d, stdout = %q", code, stdout)
	}
}

func TestAdvance(t *testing.T) {
	dir := newWork(t)
	writeFile(t, workFile(dir, "my-post"), strings.Replace(readFile(t, workFile(dir, "my-post")), "stage: brief", "stage: brief # 作者的注释", 1))
	before := readFile(t, workFile(dir, "my-post"))
	code, res := runUnits(t, dir, "advance", "my-post")
	d := diag(t, res.result, "lifecycle.missing-artifact")
	if code != 1 || res.Valid || d.Path != ".writing-anything/works/my-post" || !strings.Contains(d.Message, "brief.yaml") || !strings.Contains(d.Message, "brief.md") ||
		!slices.Contains(d.Next, "wa-cli next my-post --dir "+shellCommand(dir)+" --json") || readFile(t, workFile(dir, "my-post")) != before {
		t.Fatalf("missing-artifact: exit code = %d, diagnostic = %+v", code, d)
	}

	artifact(t, dir, "my-post", "", "brief.yaml")
	code, res = runUnits(t, dir, "advance", "my-post")
	if code != 0 || res.Unit["previousStage"] != "brief" || res.Unit["stage"] != "draft" || res.Unit["unit"] != "my-post" || res.Unit["type"] != "article" ||
		!slices.Equal(res.ChangedPaths, []string{".writing-anything/works/my-post/work.yaml"}) {
		t.Fatalf("advance-to-next: exit code = %d, result = %+v", code, res)
	}
	if after := readFile(t, workFile(dir, "my-post")); after != strings.Replace(before, "stage: brief", "stage: draft", 1) {
		t.Fatalf("preserves-comments: work.yaml =\n%s", after)
	}

	artifact(t, dir, "my-post", "", "draft.md")
	if code, res := runUnits(t, dir, "advance", "my-post"); code != 0 || res.Unit["stage"] != "review" {
		t.Fatalf("markdown-artifact: exit code = %d, result = %+v", code, res)
	}
	artifact(t, dir, "my-post", "", "review.yaml")
	if code, res := runUnits(t, dir, "advance", "my-post"); code != 0 || res.Unit["stage"] != "done" {
		t.Fatalf("advance-to-done: exit code = %d, result = %+v", code, res)
	}
	done := readFile(t, workFile(dir, "my-post"))
	if code, res := runUnits(t, dir, "advance", "my-post"); code != 1 || diag(t, res.result, "lifecycle.already-done").Severity != "error" || readFile(t, workFile(dir, "my-post")) != done {
		t.Fatalf("already-done: exit code = %d, result = %+v", code, res)
	}

	// 子单元的必需产物在 units/<unit-id>/ 下，根单元的 brief.yaml 不算（child-unit-directory）。
	runUnits(t, dir, "unit", "add", "my-post", "section", "intro")
	code, res = runUnits(t, dir, "advance", "my-post/intro")
	if d := diag(t, res.result, "lifecycle.missing-artifact"); code != 1 || d.Path != ".writing-anything/works/my-post/units/intro" ||
		!slices.Contains(d.Next, "wa-cli next my-post/intro --dir "+shellCommand(dir)+" --json") {
		t.Fatalf("child-unit-directory: exit code = %d, diagnostic = %+v", code, d)
	}

	writeFile(t, workFile(dir, "my-post"), strings.Replace(done, "stage: done", "stage: bogus", 1))
	if code, res := runUnits(t, dir, "advance", "my-post"); code != 1 || diag(t, res.result, "work.unknown-stage").Path != ".writing-anything/works/my-post/work.yaml" {
		t.Fatalf("invalid-work: exit code = %d, result = %+v", code, res)
	}
	writeFile(t, workFile(dir, "my-post"), strings.Replace(before, "stage: brief # 作者的注释", "stage: &s brief", 1))
	if code, res := runUnits(t, dir, "advance", "my-post"); code != 1 || !slices.Equal(diag(t, res.result, "work.anchored-value").Next, []string{"wa-cli help advance --json"}) {
		t.Fatalf("anchored stage: exit code = %d, result = %+v", code, res)
	}
	writeFile(t, workFile(dir, "my-post"), before)
	if code, stdout, _ := run(t, "advance", "my-post", "--dir", dir); code != 0 || !strings.Contains(stdout, "从 brief 推进到 draft") {
		t.Fatalf("text: exit code = %d, stdout = %q", code, stdout)
	}
}

// 每个单元独立推进，父子互不检查（independent-units）。
func TestIndependentUnits(t *testing.T) {
	dir := newWork(t)
	runUnits(t, dir, "unit", "add", "my-post", "section", "intro")
	artifact(t, dir, "my-post", "intro", "brief.yaml")
	if code, res := runUnits(t, dir, "advance", "my-post/intro"); code != 0 || res.Unit["stage"] != "draft" {
		t.Fatalf("child-advance-isolated: exit code = %d, result = %+v", code, res)
	}
	_, res := runUnits(t, dir, "status")
	if res.Units[0]["stage"] != "brief" || res.Units[1]["stage"] != "draft" {
		t.Fatalf("after child advance: units = %v", res.Units)
	}
	artifact(t, dir, "my-post", "", "brief.yaml")
	if code, res := runUnits(t, dir, "advance", "my-post"); code != 0 || res.Unit["stage"] != "draft" {
		t.Fatalf("parent-ignores-children: exit code = %d, result = %+v", code, res)
	}
	if _, res := runUnits(t, dir, "next", "my-post/intro"); res.Steps[0]["stage"] != "draft" {
		t.Fatalf("intro stage changed: %v", res.Steps)
	}
}

func TestNext(t *testing.T) {
	dir := newWork(t)
	code, res := runUnits(t, dir, "next")
	if code != 0 || !res.Valid || len(res.ChangedPaths) != 0 || len(res.Steps) != 1 {
		t.Fatalf("next-blocked: exit code = %d, result = %+v", code, res)
	}
	s := res.Steps[0]
	b := s["blockers"].([]any)[0].(map[string]any)
	if s["unit"] != "my-post" || s["stage"] != "brief" || !slices.Equal(blockerCodes(s), []string{"lifecycle.missing-artifact"}) || b["artifact"] != "brief" ||
		b["path"] != ".writing-anything/works/my-post" || len(commands(s)) != 0 || !strings.Contains(s["action"].(string), "brief.yaml") {
		t.Fatalf("next-blocked: step = %v", s)
	}

	artifact(t, dir, "my-post", "", "brief.yaml")
	code, res = runUnits(t, dir, "next", "my-post")
	if s := res.Steps[0]; code != 0 || len(res.Steps) != 1 || len(blockerCodes(s)) != 0 || !slices.Equal(commands(s), []string{"wa-cli advance my-post --dir " + shellCommand(dir) + " --json"}) {
		t.Fatalf("next-unblocked: exit code = %d, steps = %v", code, res.Steps)
	}

	// 已完成的单元不出现在 next 中；给出已完成的单元时只含该单元，blockers 与 commands 为空。
	src := readFile(t, workFile(dir, "my-post"))
	writeFile(t, workFile(dir, "my-post"), strings.Replace(src, "stage: brief\nunits: []", "stage: done\nunits:\n  - {id: intro, type: section, stage: draft}", 1))
	if _, res := runUnits(t, dir, "next"); len(res.Steps) != 1 || res.Steps[0]["unit"] != "my-post/intro" {
		t.Fatalf("next-skips-done: steps = %v", res.Steps)
	}
	if _, res := runUnits(t, dir, "next", "my-post"); len(res.Steps) != 1 || len(commands(res.Steps[0])) != 0 || len(blockerCodes(res.Steps[0])) != 0 || res.Steps[0]["stage"] != "done" {
		t.Fatalf("done unit: steps = %v", res.Steps)
	}

	// next-read-only：内容与修改时间都不变。
	stat := func() map[string]time.Time {
		m := map[string]time.Time{}
		filepath.Walk(filepath.Join(dir, ".writing-anything"), func(p string, info os.FileInfo, err error) error {
			m[p] = info.ModTime()
			return nil
		})
		return m
	}
	before := stat()
	runUnits(t, dir, "next")
	after := stat()
	for p, mt := range before {
		if !after[p].Equal(mt) {
			t.Errorf("%s modified by next", p)
		}
	}
	if len(after) != len(before) {
		t.Errorf("next created files")
	}

	// 有作品不合法时报告诊断，其他作品照常列出。
	runUnits(t, dir, "new", "minimal", "other")
	writeFile(t, workFile(dir, "my-post"), "schema_version: 1\nid: my-post\ngenre: minimal\ntype: article\nstage: bogus\n")
	code, res = runUnits(t, dir, "next")
	if diag(t, res.result, "work.unknown-stage"); code != 1 || res.Valid || len(res.Steps) != 1 || res.Steps[0]["unit"] != "other" {
		t.Fatalf("invalid work: exit code = %d, result = %+v", code, res)
	}
	if code, res := runUnits(t, t.TempDir(), "next"); code != 1 || diag(t, res.result, "workspace.not-found").Severity != "error" {
		t.Fatalf("no workspace: exit code = %d", code)
	}

	code, stdout, _ := run(t, "next", "other", "--dir", dir)
	if code != 0 || !strings.Contains(stdout, "other（阶段 brief）") || !strings.Contains(stdout, "阻塞项：") {
		t.Fatalf("text: exit code = %d, stdout = %q", code, stdout)
	}
	artifact(t, dir, "other", "", "brief.md")
	if _, stdout, _ := run(t, "next", "other", "--dir", dir); !strings.Contains(stdout, "下一步：wa-cli advance other") {
		t.Fatalf("text unblocked: stdout = %q", stdout)
	}
	empty := newWorkspace(t)
	if _, stdout, _ := run(t, "next", "--dir", empty); !strings.Contains(stdout, "没有需要推进的单元") {
		t.Fatalf("text empty: stdout = %q", stdout)
	}
}

func TestStatusUnits(t *testing.T) {
	dir := newWork(t)
	runUnits(t, dir, "unit", "add", "my-post", "section", "intro")
	code, res := runUnits(t, dir, "status")
	if code != 0 || !res.Valid || len(res.Units) != 2 {
		t.Fatalf("status-units: exit code = %d, result = %+v", code, res)
	}
	root, intro := res.Units[0], res.Units[1]
	if root["unit"] != "my-post" || root["work"] != "my-post" || root["type"] != "article" || root["parent"] != nil || root["stage"] != "brief" ||
		intro["unit"] != "my-post/intro" || intro["type"] != "section" || intro["parent"] != "my-post" || intro["stage"] != "brief" {
		t.Fatalf("units = %v", res.Units)
	}
	for _, u := range res.Units {
		if !slices.Equal(blockerCodes(u), []string{"lifecycle.missing-artifact"}) || len(commands(u)) != 0 {
			t.Errorf("unit %v: want one missing-artifact blocker and no commands", u)
		}
	}
	artifact(t, dir, "my-post", "", "brief.yaml")
	if _, res := runUnits(t, dir, "status"); len(res.Units[0]["blockers"].([]any)) != 0 || len(commands(res.Units[0])) != 1 {
		t.Fatalf("unblocked root: %v", res.Units[0])
	}
	// stage 为 done 的单元，blockers 与 commands 为空数组。
	writeFile(t, workFile(dir, "my-post"), strings.Replace(readFile(t, workFile(dir, "my-post")), "stage: brief", "stage: done", 1))
	if _, res := runUnits(t, dir, "status"); res.Units[0]["stage"] != "done" || len(res.Units[0]["blockers"].([]any)) != 0 || len(commands(res.Units[0])) != 0 {
		t.Fatalf("done unit in status: %v", res.Units[0])
	}
	writeFile(t, workFile(dir, "my-post"), strings.Replace(readFile(t, workFile(dir, "my-post")), "stage: done", "stage: brief", 1))
	if code, res := runUnits(t, newWorkspace(t, "--genre", "minimal"), "status"); code != 0 || res.Units == nil || len(res.Units) != 0 {
		t.Fatalf("status-fresh: units = %v", res.Units)
	}
	code, stdout, _ := run(t, "status", "--dir", dir)
	if want := "单元（Unit）：\n  my-post（article）：阶段 brief\n    下一步：wa-cli advance my-post --dir " + shellCommand(dir) + " --json\n" +
		"    my-post/intro（section）：阶段 brief，阻塞项 1 项\n      阻塞项：阶段 brief 的必需产物（Required Artifact）brief 缺失："; code != 0 || !strings.Contains(stdout, want) {
		t.Fatalf("text: exit code = %d, stdout = %q", code, stdout)
	}
}
