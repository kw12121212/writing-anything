package cli

import (
	"encoding/json"
	"os"
	"slices"
	"strings"
	"testing"

	"github.com/kw12121212/writing-anything/internal/catalog"
	"github.com/kw12121212/writing-anything/internal/work"
)

type outlineOut struct {
	result
	Outline  map[string]any   `json:"outline"`
	Coverage []map[string]any `json:"coverage"`
	Gaps     []map[string]any `json:"gaps"`
}

func runOutline(t *testing.T, dir string, args ...string) (int, outlineOut) {
	t.Helper()
	code, res := runUnits(t, dir, args...)
	var out outlineOut
	if err := json.Unmarshal([]byte(res.stdout), &out); err != nil {
		t.Fatal(err)
	}
	out.result = res.result
	return code, out
}

const outlineRel = ".writing-anything/works/my-post/outline.yaml"

// outlineWork 返回写作简报含核心观点 km-1 与 km-2（出自已登记素材 src-a）的作品 my-post。
func outlineWork(t *testing.T) string {
	t.Helper()
	dir := briefWork(t)
	writeFile(t, wsPath(dir, briefRel), completeBrief+"key_messages:\n  - {id: km-1, text: 连接池降低延迟, sources: [src-a]}\n  - {id: km-2, text: 连接数不是越多越好, sources: [src-a]}\n")
	return dir
}

func sectionIDs(o map[string]any) []string {
	var out []string
	for _, s := range o["sections"].([]any) {
		out = append(out, s.(map[string]any)["id"].(string))
	}
	return out
}

func gapSummary(gs []map[string]any) []string {
	var out []string
	for _, g := range gs {
		s := g["code"].(string)
		for _, k := range []string{"keyMessage", "section"} {
			if v, ok := g[k].(string); ok {
				s += " " + v
			}
		}
		out = append(out, s)
	}
	return out
}

func TestOutlineSet(t *testing.T) {
	dir := outlineWork(t)
	// set-first-section
	code, res := runOutline(t, dir, "outline", "set", "my-post", "why", "--title", "为什么需要连接池", "--covers", "km-1")
	if code != 0 || !res.Valid || !slices.Equal(res.ChangedPaths, []string{outlineRel}) || !slices.Equal(gapSummary(res.Gaps), []string{"outline.uncovered-message km-2"}) {
		t.Fatalf("set-first-section: exit code = %d, result = %+v", code, res)
	}
	if res.Outline["unit"] != "my-post" || res.Outline["path"] != outlineRel || len(res.Coverage) != 2 || res.Coverage[0]["sections"].([]any)[0] != "why" {
		t.Fatalf("set-first-section: outline = %v, coverage = %v", res.Outline, res.Coverage)
	}
	runOutline(t, dir, "outline", "set", "my-post", "how", "--title", "怎么做", "--covers", "km-2")
	// set-before
	code, res = runOutline(t, dir, "outline", "set", "my-post", "intro", "--title", "引子", "--reason", "引出问题", "--before", "why")
	if code != 0 || !slices.Equal(sectionIDs(res.Outline), []string{"intro", "why", "how"}) || len(res.Gaps) != 0 {
		t.Fatalf("set-before: exit code = %d, outline = %v, gaps = %v", code, res.Outline, res.Gaps)
	}
	// set-modify：保留注释与未涉及的字段。
	writeFile(t, wsPath(dir, outlineRel), strings.Replace(readFile(t, wsPath(dir, outlineRel)), "  - id: how\n", "  # 作者的注释\n  - id: how\n", 1))
	code, res = runOutline(t, dir, "outline", "set", "my-post", "how", "--uses", "src-a")
	how := res.Outline["sections"].([]any)[2].(map[string]any)
	if code != 0 || how["title"] != "怎么做" || how["covers"].([]any)[0] != "km-2" || how["uses"].([]any)[0] != "src-a" || !strings.Contains(readFile(t, wsPath(dir, outlineRel)), "# 作者的注释") {
		t.Fatalf("set-modify: exit code = %d, how = %v", code, how)
	}
	// set-move
	if code, res = runOutline(t, dir, "outline", "set", "my-post", "intro", "--after", "how"); code != 0 || !slices.Equal(sectionIDs(res.Outline), []string{"why", "how", "intro"}) {
		t.Fatalf("set-move: exit code = %d, outline = %v", code, res.Outline)
	}
	// set-clear
	runOutline(t, dir, "outline", "set", "my-post", "how", "--reason", "细节")
	code, res = runOutline(t, dir, "outline", "set", "my-post", "how", "--reason=")
	how = res.Outline["sections"].([]any)[1].(map[string]any)
	if code != 0 || how["reason"] != nil || len(how["covers"].([]any)) != 1 {
		t.Fatalf("set-clear: exit code = %d, how = %v", code, how)
	}
	// set-unchanged
	before := readFile(t, wsPath(dir, outlineRel))
	if code, res := runOutline(t, dir, "outline", "set", "my-post", "why", "--title", "为什么需要连接池"); code != 0 || len(res.ChangedPaths) != 0 || readFile(t, wsPath(dir, outlineRel)) != before {
		t.Fatalf("set-unchanged: exit code = %d, result = %+v", code, res)
	}
	// 子单元的单元目录不存在时先创建。
	runWS(t, dir, "unit", "add", "my-post", "section", "intro")
	if code, res := runOutline(t, dir, "outline", "set", "my-post/intro", "a", "--title", "甲", "--reason", "r"); code != 0 || !slices.Equal(res.ChangedPaths, []string{".writing-anything/works/my-post/units/intro", ".writing-anything/works/my-post/units/intro/outline.yaml"}) {
		t.Fatalf("child unit: exit code = %d, result = %+v", code, res)
	}
	code, stdout, _ := run(t, "outline", "set", "my-post", "why", "--title", "为什么", "--dir", dir)
	if code != 0 || !strings.Contains(stdout, "大纲（Outline）已更新") || !strings.Contains(stdout, "1. why 为什么（覆盖 km-1）") || !strings.Contains(stdout, "由 why 覆盖") {
		t.Fatalf("text: exit code = %d, stdout = %q", code, stdout)
	}
	if _, stdout, _ := run(t, "outline", "set", "my-post", "why", "--title", "为什么", "--dir", dir); !strings.Contains(stdout, "取值未变") {
		t.Fatalf("text unchanged: stdout = %q", stdout)
	}
}

func TestOutlineSetRejects(t *testing.T) {
	dir := outlineWork(t)
	runOutline(t, dir, "outline", "set", "my-post", "why", "--title", "为什么", "--covers", "km-1")
	before := readFile(t, wsPath(dir, outlineRel))
	tests := []struct {
		name  string
		args  []string
		code  string
		field string
		next  string
	}{
		{"set-new-without-title", []string{"end", "--reason", "总结"}, "input.invalid-value", "", "wa-cli help outline set --json"},
		{"set-unknown-message", []string{"why", "--covers", "km-9"}, "outline.unknown-message", "covers[0]", "wa-cli brief show my-post"},
		{"unknown source", []string{"why", "--uses", "src-nope"}, "outline.unknown-source", "uses[0]", "wa-cli source list"},
		{"set-missing-anchor", []string{"why", "--after", "nope"}, "outline.section-not-found", "after", "wa-cli outline show my-post"},
		{"invalid section id", []string{"Why", "--title", "t"}, "artifact.schema-violation", "section", "wa-cli help outline set --json"},
		{"blank title", []string{"why", "--title", " "}, "input.invalid-value", "", "wa-cli help outline set --json"},
		{"duplicate covers", []string{"why", "--covers", "km-1,km-1"}, "input.invalid-value", "", "wa-cli help outline set --json"},
		{"after and before", []string{"why", "--after", "a", "--before", "b"}, "input.invalid-value", "", "wa-cli help outline set --json"},
		{"relative to itself", []string{"why", "--after", "why"}, "input.invalid-value", "", "wa-cli help outline set --json"},
		{"nothing to change", []string{"why"}, "input.invalid-value", "", "wa-cli help outline set --json"},
		{"empty title", []string{"why", "--title="}, "input.invalid-value", "", "wa-cli help outline set --json"},
		{"empty after", []string{"why", "--after="}, "input.invalid-value", "", "wa-cli help outline set --json"},
		{"before itself", []string{"why", "--before", "why"}, "input.invalid-value", "", "wa-cli help outline set --json"},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			code, res := runOutline(t, dir, append([]string{"outline", "set", "my-post"}, tt.args...)...)
			d := diag(t, res.result, tt.code)
			if code != 1 || res.Valid || d.Field != tt.field || d.Path != "" || !hasNext(d, tt.next) || res.Outline != nil || len(res.ChangedPaths) != 0 {
				t.Fatalf("exit code = %d, diagnostic = %+v", code, d)
			}
			if readFile(t, wsPath(dir, outlineRel)) != before {
				t.Fatal("outline.yaml changed")
			}
		})
	}
	if _, res := runOutline(t, dir, "outline", "set", "my-post", "why", "--before", "why"); !strings.Contains(diag(t, res.result, "input.invalid-value").Message, "--before") {
		t.Fatal("before itself: message does not name --before")
	}
	if _, res := runOutline(t, dir, "outline", "set", "my-post", "end", "--reason", "总结"); !strings.Contains(diag(t, res.result, "input.invalid-value").Message, "--title") {
		t.Fatal("set-new-without-title: message does not mention --title")
	}
	// 已有 outline.yaml 不通过校验时报告其诊断，下一步是复查该文件；锚点。
	writeFile(t, wsPath(dir, outlineRel), "schema_version: 1\nsections: {}\n")
	if code, res := runOutline(t, dir, "outline", "set", "my-post", "why", "--title", "t"); code != 1 || !hasNext(diag(t, res.result, "artifact.schema-violation"), "wa-cli validate "+outlineRel) {
		t.Fatalf("invalid existing: exit code = %d", code)
	}
	writeFile(t, wsPath(dir, outlineRel), "schema_version: 1\nsections:\n  - &s {id: why, title: t, reason: r}\n")
	if code, res := runOutline(t, dir, "outline", "set", "my-post", "why", "--title", "u"); code != 1 || !hasNext(diag(t, res.result, "outline.anchored-value"), "wa-cli help outline set --json") {
		t.Fatalf("anchored: exit code = %d", code)
	}
	if code, res := runOutline(t, dir, "outline", "remove", "my-post", "why"); code != 1 || !hasNext(diag(t, res.result, "outline.anchored-value"), "wa-cli help outline remove --json") {
		t.Fatalf("remove anchored: exit code = %d", code)
	}
	// 写作简报不通过校验时，给出 --covers 报告写作简报的诊断。
	os.Remove(wsPath(dir, outlineRel))
	writeFile(t, wsPath(dir, briefRel), "k: v\n")
	if code, res := runOutline(t, dir, "outline", "set", "my-post", "why", "--title", "t", "--covers", "km-1"); code != 1 || diag(t, res.result, "artifact.schema-violation").Path != briefRel {
		t.Fatalf("invalid brief: exit code = %d", code)
	}
}

func TestOutlineRemoveAndShow(t *testing.T) {
	dir := outlineWork(t)
	// show-empty
	code, res := runOutline(t, dir, "outline", "show", "my-post")
	if code != 0 || res.Outline != nil || !slices.Equal(gapSummary(res.Gaps), []string{"outline.uncovered-message km-1", "outline.uncovered-message km-2"}) || len(res.ChangedPaths) != 0 {
		t.Fatalf("show-empty: exit code = %d, result = %+v", code, res)
	}
	runOutline(t, dir, "outline", "set", "my-post", "intro", "--title", "引子", "--reason", "引出问题")
	runOutline(t, dir, "outline", "set", "my-post", "why", "--title", "为什么", "--covers", "km-1")
	runOutline(t, dir, "outline", "set", "my-post", "how", "--title", "怎么做", "--reason", "r")
	runOutline(t, dir, "outline", "set", "my-post", "how", "--reason=")
	// show-coverage
	code, res = runOutline(t, dir, "outline", "show", "my-post")
	if code != 0 || !res.Valid || res.Coverage[0]["keyMessage"] != "km-1" || len(res.Coverage[1]["sections"].([]any)) != 0 ||
		!slices.Equal(gapSummary(res.Gaps), []string{"outline.uncovered-message km-2", "outline.missing-reason how"}) || res.Gaps[0]["section"] != nil || res.Gaps[1]["keyMessage"] != nil {
		t.Fatalf("show-coverage: exit code = %d, result = %+v", code, res)
	}
	code, stdout, _ := run(t, "outline", "show", "my-post", "--dir", dir)
	if code != 0 || !strings.Contains(stdout, "不覆盖核心观点：引出问题") || !strings.Contains(stdout, "核心观点（Key Message）km-2：连接数不是越多越好（未覆盖）") || !strings.Contains(stdout, "覆盖缺口：") {
		t.Fatalf("show text: stdout = %q", stdout)
	}
	// remove-section
	code, res = runOutline(t, dir, "outline", "remove", "my-post", "intro")
	if code != 0 || !slices.Equal(sectionIDs(res.Outline), []string{"why", "how"}) || !slices.Equal(res.ChangedPaths, []string{outlineRel}) {
		t.Fatalf("remove-section: exit code = %d, result = %+v", code, res)
	}
	if _, stdout, _ := run(t, "outline", "remove", "my-post", "how", "--dir", dir); !strings.Contains(stdout, "已从大纲（Outline）删除节 how") {
		t.Fatalf("remove text: stdout = %q", stdout)
	}
	// remove-missing
	before := readFile(t, wsPath(dir, outlineRel))
	code, res = runOutline(t, dir, "outline", "remove", "my-post", "nope")
	if d := diag(t, res.result, "outline.section-not-found"); code != 1 || !hasNext(d, "wa-cli outline show my-post") || readFile(t, wsPath(dir, outlineRel)) != before {
		t.Fatalf("remove-missing: exit code = %d, diagnostic = %+v", code, d)
	}
	// 不通过校验的大纲或写作简报：show 报告其诊断，退出码为 1。
	writeFile(t, wsPath(dir, outlineRel), "k: v\n")
	if code, res := runOutline(t, dir, "outline", "show", "my-post"); code != 1 || res.Valid {
		t.Fatalf("invalid outline show: exit code = %d", code)
	}
	if code, _, _ := run(t, "outline", "show", "my-post", "--dir", dir); code != 1 {
		t.Fatalf("invalid outline show text: exit code = %d", code)
	}
	writeFile(t, wsPath(dir, briefRel), "k: v\n")
	if code, res := runOutline(t, dir, "outline", "show", "my-post"); code != 1 || diag(t, res.result, "artifact.schema-violation").Path != briefRel {
		t.Fatalf("invalid brief show: exit code = %d", code)
	}
	empty := newWork(t)
	if _, stdout, _ := run(t, "outline", "show", "my-post", "--dir", empty); !strings.Contains(stdout, "还没有大纲") {
		t.Fatalf("text empty: stdout = %q", stdout)
	}
	if code, res := runOutline(t, empty, "outline", "remove", "my-post", "why"); code != 1 || diag(t, res.result, "outline.section-not-found").Path != "" {
		t.Fatalf("remove without outline: exit code = %d", code)
	}
}

func TestOutlineValidate(t *testing.T) {
	dir := outlineWork(t)
	// validate-covered
	writeFile(t, wsPath(dir, outlineRel), "schema_version: 1\nsections:\n  - {id: why, title: 为什么, covers: [km-1]}\n  - {id: how, title: 怎么做, covers: [km-2], uses: [src-a]}\n")
	if code, res := runWS(t, dir, "validate"); code != 0 || !slices.Contains(res.Checked, outlineRel) {
		t.Fatalf("validate-covered: exit code = %d, result = %+v", code, res)
	}
	tests := []struct {
		name, content, code, field string
		next                       []string
	}{
		{"validate-uncovered", "schema_version: 1\nsections:\n  - {id: why, title: 为什么, covers: [km-1]}\n  - {id: how, title: 怎么做, reason: 交代背景}\n", "outline.uncovered-message", "", []string{"wa-cli outline show my-post", "wa-cli help outline set --json"}},
		{"validate-missing-reason", "schema_version: 1\nsections:\n  - {id: why, title: 为什么, covers: [km-1, km-2]}\n  - {id: how, title: 怎么做}\n", "outline.missing-reason", "sections[1]", []string{"wa-cli outline show my-post", "wa-cli help outline set --json"}},
		{"validate-unknown-message", "schema_version: 1\nsections:\n  - {id: why, title: 为什么, covers: [km-9]}\n", "outline.unknown-message", "sections[0].covers[0]", []string{"wa-cli brief show my-post", "wa-cli validate --dir"}},
		{"validate-unknown-source", "schema_version: 1\nsections:\n  - {id: why, title: 为什么, covers: [km-1, km-2], uses: [src-nope]}\n", "outline.unknown-source", "sections[0].uses[0]", []string{"wa-cli source list", "wa-cli validate --dir"}},
		{"validate-duplicate-section", "schema_version: 1\nsections:\n  - {id: why, title: t, covers: [km-1, km-2]}\n  - {id: why, title: t, reason: r}\n", "artifact.duplicate-id", "sections[1].id", []string{"wa-cli validate --dir"}},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			writeFile(t, wsPath(dir, outlineRel), tt.content)
			for _, args := range [][]string{{"validate"}, {"validate", outlineRel}} {
				code, res := runWS(t, dir, args...)
				d := diag(t, res, tt.code)
				if code != 1 || d.Path != outlineRel || d.Field != tt.field {
					t.Fatalf("%v: exit code = %d, diagnostic = %+v", args, code, d)
				}
				for _, n := range tt.next {
					if !hasNext(d, n) {
						t.Fatalf("%v: next = %v, want %q", args, d.Next, n)
					}
				}
			}
		})
	}
	if _, res := runWS(t, dir, "validate"); !strings.Contains(diag(t, res, "artifact.duplicate-id").Message, "why") {
		t.Fatal("duplicate message")
	}
	writeFile(t, wsPath(dir, outlineRel), "schema_version: 1\nsections:\n  - {id: why, title: 为什么, covers: [km-1]}\n  - {id: how, title: 怎么做, reason: 交代背景}\n")
	if _, res := runWS(t, dir, "validate"); !strings.Contains(diag(t, res, "outline.uncovered-message").Message, "km-2") {
		t.Fatal("validate-uncovered: message lacks km-2")
	}
}

// minimal 文体没有要求 outline 的阶段：有覆盖缺口的大纲不阻塞推进（gate-other-stage）。
func TestOutlineGateOtherStage(t *testing.T) {
	dir := newWork(t)
	writeFile(t, wsPath(dir, briefRel), completeBrief)
	writeFile(t, wsPath(dir, outlineRel), "schema_version: 1\nsections:\n  - {id: how, title: 怎么做}\n")
	if code, res := runUnits(t, dir, "advance", "my-post"); code != 0 || res.Unit["stage"] != "draft" {
		t.Fatalf("exit code = %d, result = %+v", code, res)
	}
}

func TestOutlineSymlink(t *testing.T) {
	dir := outlineWork(t)
	outside := wsPath(t.TempDir(), "outline.yaml")
	writeFile(t, outside, "schema_version: 1\nsections: []\n")
	if err := os.Symlink(outside, wsPath(dir, outlineRel)); err != nil {
		t.Skip("symlinks unavailable:", err)
	}
	for _, args := range [][]string{{"outline", "show", "my-post"}, {"outline", "set", "my-post", "why", "--title", "t"}, {"outline", "remove", "my-post", "why"}, {"validate"}, {"validate", outlineRel}} {
		code, res := runWS(t, dir, args...)
		if d := diag(t, res, "artifact.not-found"); code != 1 || d.Path != outlineRel || !hasNext(d, "wa-cli status") {
			t.Fatalf("%v: exit code = %d, diagnostic = %+v", args, code, d)
		}
	}
	if info, err := os.Lstat(wsPath(dir, outlineRel)); err != nil || info.Mode()&os.ModeSymlink == 0 {
		t.Fatal("symlink was replaced")
	}
	// 单元目录本身是指向外部的符号链接：不读取外部的 outline.yaml，也不向其中写入。
	runWS(t, dir, "unit", "add", "my-post", "section", "intro")
	outDir := t.TempDir()
	writeFile(t, wsPath(outDir, "outline.yaml"), "schema_version: 1\nsections: []\n")
	os.MkdirAll(wsPath(dir, ".writing-anything/works/my-post/units"), 0o755)
	if err := os.Symlink(outDir, wsPath(dir, ".writing-anything/works/my-post/units/intro")); err != nil {
		t.Fatal(err)
	}
	if code, res := runWS(t, dir, "outline", "show", "my-post/intro"); code != 1 || diag(t, res, "artifact.not-found").Path != ".writing-anything/works/my-post/units/intro/outline.yaml" {
		t.Fatalf("symlinked unit dir show: exit code = %d", code)
	}
	os.Remove(wsPath(outDir, "outline.yaml"))
	if code, _ := runWS(t, dir, "outline", "set", "my-post/intro", "a", "--title", "t", "--reason", "r"); code != 1 {
		t.Fatalf("symlinked unit dir set: exit code = %d", code)
	}
	if entries, _ := os.ReadDir(outDir); len(entries) != 0 {
		t.Fatalf("wrote into the symlinked unit dir: %v", entries)
	}
}

// 写作简报删除核心观点后，仍能用 outline show、set 与 remove 查看与修复大纲；show 报告引用问题但照常输出大纲。
func TestOutlineDanglingMessage(t *testing.T) {
	dir := outlineWork(t)
	runOutline(t, dir, "outline", "set", "my-post", "why", "--title", "为什么", "--covers", "km-1")
	runOutline(t, dir, "outline", "set", "my-post", "how", "--title", "怎么做", "--covers", "km-2")
	runBrief(t, dir, "brief", "set", "my-post", "--remove-message", "km-2")
	code, res := runOutline(t, dir, "outline", "show", "my-post")
	if d := diag(t, res.result, "outline.unknown-message"); code != 1 || res.Outline == nil || d.Field != "sections[1].covers[0]" || len(res.Coverage) != 1 {
		t.Fatalf("show: exit code = %d, result = %+v", code, res)
	}
	if code, stdout, _ := run(t, "outline", "show", "my-post", "--dir", dir); code != 1 || !strings.Contains(stdout, "2. how 怎么做") {
		t.Fatalf("show text: exit code = %d, stdout = %q", code, stdout)
	}
	// 修复一节后，其余节中仍然失效的引用以 warning 报告。
	writeFile(t, wsPath(dir, outlineRel), strings.Replace(readFile(t, wsPath(dir, outlineRel)), "covers: [km-1]", "covers: [km-1, km-2]", 1))
	code, res = runOutline(t, dir, "outline", "set", "my-post", "how", "--covers=", "--reason", "背景")
	if d := diag(t, res.result, "outline.unknown-message"); code != 0 || !res.Valid || len(res.ChangedPaths) != 1 || d.Severity != "warning" || d.Field != "sections[0].covers[1]" {
		t.Fatalf("set: exit code = %d, result = %+v", code, res)
	}
	if code, res := runOutline(t, dir, "outline", "set", "my-post", "why", "--covers", "km-1"); code != 0 || len(res.Diagnostics) != 0 {
		t.Fatalf("set repaired: exit code = %d, result = %+v", code, res)
	}
	if code, _ := runWS(t, dir, "validate"); code != 0 {
		t.Fatalf("validate after repair: exit code = %d", code)
	}
	runOutline(t, dir, "outline", "set", "my-post", "how", "--covers", "km-1")
	runBrief(t, dir, "brief", "set", "my-post", "--remove-message", "km-1")
	if code, res := runOutline(t, dir, "outline", "remove", "my-post", "how"); code != 0 || len(res.Outline["sections"].([]any)) != 1 {
		t.Fatalf("remove: exit code = %d, result = %+v", code, res)
	}
}

// 大纲阻塞项的下一步命令与说明（outline-gate、next-steps）；内置文体没有 outline 阶段，直接检查阻塞项的转换。
func TestOutlineBlockerNext(t *testing.T) {
	dir := "/d"
	for _, tt := range []struct {
		code, path string
		want       []string
	}{
		{"outline.uncovered-message", outlineRel, []string{"wa-cli outline show my-post --dir /d --json", "wa-cli help outline set --json"}},
		{"outline.missing-reason", ".writing-anything/works/my-post/units/intro/outline.md", []string{"wa-cli outline show my-post/intro --dir /d --json", "wa-cli help outline set --json"}},
		{"outline.invalid", briefRel, []string{"wa-cli validate " + briefRel + " --dir /d --json", "wa-cli validate --dir /d --json"}},
	} {
		if got := blockerNext(dir, tt.code, tt.path); !slices.Equal(got, tt.want) {
			t.Errorf("blockerNext(%s) = %v, want %v", tt.code, got, tt.want)
		}
	}
	w := &work.Work{Genre: &catalog.Genre{Stages: []catalog.Stage{{ID: "outline"}, {ID: "draft"}}}}
	u := &work.Unit{Stage: "outline"}
	got := action(w, u, []blockerView{
		{Code: "outline.uncovered-message", Message: "核心观点 km-2 没有被覆盖。"},
		{Code: "outline.invalid", Path: briefRel, Message: "x"},
	})
	if !strings.Contains(got, "补齐大纲（Outline）的覆盖：核心观点 km-2 没有被覆盖；修复不通过校验的大纲") {
		t.Fatalf("action = %q", got)
	}
}
