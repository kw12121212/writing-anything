package main

import (
	"bytes"
	"os"
	"path/filepath"
	"strings"
	"testing"
)

// TestCriteriaReconciliation 以真实仓库运行完成标准对账：已归档里程碑的每条完成标准都映射到
// 存在且首层没有无条件 t.Skip 的测试函数，审计加固表项不少于 14 条。
func TestCriteriaReconciliation(t *testing.T) {
	root := filepath.Join("..", "..", "..")
	report, err := Check(root, filepath.Join(root, DefaultTable), MinHardened)
	if err != nil {
		t.Fatalf("对账失败：%v", err)
	}
	for _, p := range report.Problems {
		t.Error(p)
	}
	if report.Criteria == 0 || report.Entries != report.Criteria {
		t.Errorf("已归档完成标准 %d 条，对账表项 %d 条", report.Criteria, report.Entries)
	}
	if report.Hardened < MinHardened {
		t.Errorf("带 hardened_by 的审计加固表项 %d 条，少于 %d 条", report.Hardened, MinHardened)
	}
}

// fixture 在临时目录中搭一个最小仓库：一个已归档里程碑（两条完成标准）、一个已归档变更与一个测试包。
type fixture struct {
	root  string
	table string
}

const fixtureMilestone = `milestone:
  m1:
    title: 夹具
done_criteria:
  - 第一条标准
  - "第二条: 标准"
`

const fixtureTests = `package pkg

import "testing"

func TestOne(t *testing.T) { t.Log("ok") }

func TestConditional(t *testing.T) {
	if testing.Short() {
		t.Skip("short")
	}
}

func TestSkipped(t *testing.T) {
	t.Skip("never runs")
}

func TestSkipNow(tt *testing.T) {
	tt.SkipNow()
}

func TestNotATest(x int) {}

func Testlower(t *testing.T) {}

func helper(t *testing.T) {}
`

const fixtureTable = `schema_version: 1
entries:
  - milestone: m1
    criterion: 第一条标准
    tests: [pkg.TestOne]
    hardened_by: [fix-x]
  - milestone: m1
    criterion: "第二条: 标准"
    tests: [pkg.TestOne, pkg.TestConditional]
`

func newFixture(t *testing.T) fixture {
	t.Helper()
	root := t.TempDir()
	write(t, filepath.Join(root, archivedMilestones, "m1.yaml"), fixtureMilestone)
	write(t, filepath.Join(root, archivedChanges, "2026-01-01-fix-x", "proposal.yaml"), "change: fix-x\n")
	write(t, filepath.Join(root, "pkg", "pkg_test.go"), fixtureTests)
	// 当前平台不编译的测试文件（构建约束 ignore）中的测试不算存在。
	write(t, filepath.Join(root, "pkg", "ignored_test.go"), "//go:build ignore\n\npackage pkg\n\nimport \"testing\"\n\nfunc TestIgnored(t *testing.T) {}\n")
	f := fixture{root: root, table: filepath.Join(root, "table.yaml")}
	write(t, f.table, fixtureTable)
	return f
}

func write(t *testing.T, path, content string) {
	t.Helper()
	if err := os.MkdirAll(filepath.Dir(path), 0o755); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(path, []byte(content), 0o644); err != nil {
		t.Fatal(err)
	}
}

func (f fixture) check(t *testing.T) Report {
	t.Helper()
	report, err := Check(f.root, f.table, 1)
	if err != nil {
		t.Fatalf("Check：%v", err)
	}
	return report
}

// wantProblems 断言问题恰好一条且含 want 的每个片段。
func wantProblems(t *testing.T, report Report, want ...string) {
	t.Helper()
	if len(report.Problems) != 1 {
		t.Fatalf("问题应恰好 1 条，实际 %d 条：%q", len(report.Problems), report.Problems)
	}
	for _, w := range want {
		if !strings.Contains(report.Problems[0], w) {
			t.Errorf("问题 %q 不含 %q", report.Problems[0], w)
		}
	}
}

func TestCheckPasses(t *testing.T) {
	f := newFixture(t)
	report := f.check(t)
	if len(report.Problems) != 0 {
		t.Fatalf("problems = %q", report.Problems)
	}
	if report.Criteria != 2 || report.Entries != 2 || report.Tests != 2 || report.Hardened != 1 {
		t.Errorf("report = %+v", report)
	}
}

func TestCheckMissingEntry(t *testing.T) {
	f := newFixture(t)
	write(t, f.table, `schema_version: 1
entries:
  - milestone: m1
    criterion: 第一条标准
    tests: [pkg.TestOne]
    hardened_by: [fix-x]
`)
	wantProblems(t, f.check(t), "m1", "缺少对账表项", "第二条: 标准")
}

// 完成标准原文变化：旧表项过期、新原文缺表项，两条都报告。
func TestCheckCriterionTextChanged(t *testing.T) {
	f := newFixture(t)
	write(t, filepath.Join(f.root, archivedMilestones, "m1.yaml"), strings.Replace(fixtureMilestone, "第一条标准", "第一条标准（已改写）", 1))
	report := f.check(t)
	if len(report.Problems) != 2 ||
		!strings.Contains(report.Problems[0], "不在已归档里程碑中") || !strings.Contains(report.Problems[0], "重新确认映射") ||
		!strings.Contains(report.Problems[1], "缺少对账表项") || !strings.Contains(report.Problems[1], "第一条标准（已改写）") {
		t.Fatalf("problems = %q", report.Problems)
	}
}

func TestCheckMissingTestFunction(t *testing.T) {
	f := newFixture(t)
	write(t, f.table, strings.Replace(fixtureTable, "pkg.TestConditional", "pkg.TestGone", 1))
	wantProblems(t, f.check(t), "pkg.TestGone", "没有测试函数 TestGone")
}

// 签名不是 (t *testing.T) 的 Test 前缀函数、当前平台不编译的文件中的函数都不算测试函数。
func TestCheckNotATestFunction(t *testing.T) {
	for _, name := range []string{"TestNotATest", "TestIgnored"} {
		t.Run(name, func(t *testing.T) {
			f := newFixture(t)
			write(t, f.table, strings.Replace(fixtureTable, "pkg.TestConditional", "pkg."+name, 1))
			wantProblems(t, f.check(t), "没有测试函数 "+name)
		})
	}
}

// 审计加固表项少于要求的条数时失败。
func TestCheckMinHardened(t *testing.T) {
	f := newFixture(t)
	report, err := Check(f.root, f.table, 2)
	if err != nil {
		t.Fatal(err)
	}
	wantProblems(t, report, "审计加固表项 1 条", "少于审计列出的 2 条")
}

func TestCheckUnconditionalSkip(t *testing.T) {
	for _, tc := range []struct{ name, call string }{{"TestSkipped", "t.Skip"}, {"TestSkipNow", "tt.SkipNow"}} {
		t.Run(tc.name, func(t *testing.T) {
			f := newFixture(t)
			write(t, f.table, strings.Replace(fixtureTable, "pkg.TestConditional", "pkg."+tc.name, 1))
			wantProblems(t, f.check(t), "pkg."+tc.name, "无条件调用 "+tc.call)
		})
	}
}

func TestCheckEntryProblems(t *testing.T) {
	cases := []struct {
		name  string
		table string
		want  []string
	}{
		{"no-tests", strings.Replace(fixtureTable, "[pkg.TestOne, pkg.TestConditional]", "[]", 1), []string{"没有映射任何测试函数"}},
		{"bad-ref", strings.Replace(fixtureTable, "pkg.TestConditional", "TestConditional", 1), []string{"格式应为"}},
		{"bad-ref-name", strings.Replace(fixtureTable, "pkg.TestConditional", "pkg.helper", 1), []string{"格式应为"}},
		{"lowercase-test-name", strings.Replace(fixtureTable, "pkg.TestConditional", "pkg.Testlower", 1), []string{"格式应为"}},
		{"no-package", strings.Replace(fixtureTable, "pkg.TestConditional", "nopkg.TestOne", 1), []string{"nopkg.TestOne", "没有 *_test.go"}},
		{"unknown-change", strings.Replace(fixtureTable, "[fix-x]", "[fix-y]", 1), []string{"hardened_by", "fix-y"}},
		{"suffix-change", strings.Replace(fixtureTable, "[fix-x]", "[x]", 1), []string{"hardened_by", "变更 x "}},
		{"date-suffix-change", strings.Replace(fixtureTable, "[fix-x]", "[01-fix-x]", 1), []string{"hardened_by", "变更 01-fix-x "}},
		{"schema-version", strings.Replace(fixtureTable, "schema_version: 1", "schema_version: 2", 1), []string{"schema_version 应为 1"}},
		{"duplicate", fixtureTable + `  - milestone: m1
    criterion: 第一条标准
    tests: [pkg.TestOne]
`, []string{"重复登记", "第一条标准"}},
		{"empty-criterion", fixtureTable + `  - milestone: m1
    criterion: " "
    tests: [pkg.TestOne]
`, []string{"不能为空"}},
	}
	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			f := newFixture(t)
			write(t, f.table, tc.table)
			wantProblems(t, f.check(t), tc.want...)
		})
	}
}

func TestCheckInputErrors(t *testing.T) {
	f := newFixture(t)
	if _, err := Check(f.root, filepath.Join(f.root, "missing.yaml"), 1); err == nil || !strings.Contains(err.Error(), "读取对账表失败") {
		t.Errorf("missing table: %v", err)
	}
	write(t, f.table, "schema_version: 1\nentries: []\nextra: 1\n")
	if _, err := Check(f.root, f.table, 1); err == nil || !strings.Contains(err.Error(), "解析对账表") {
		t.Errorf("unknown field: %v", err)
	}
	write(t, f.table, fixtureTable)
	write(t, filepath.Join(f.root, archivedMilestones, "m1.yaml"), "milestone: [\n")
	if _, err := Check(f.root, f.table, 1); err == nil || !strings.Contains(err.Error(), "解析里程碑") {
		t.Errorf("bad milestone: %v", err)
	}
	write(t, filepath.Join(f.root, archivedMilestones, "m1.yaml"), "milestone: {}\n")
	if _, err := Check(f.root, f.table, 1); err == nil || !strings.Contains(err.Error(), "恰好含一个 id") {
		t.Errorf("no milestone id: %v", err)
	}
	empty := t.TempDir()
	if _, err := Check(empty, f.table, 1); err == nil || !strings.Contains(err.Error(), "没有已归档里程碑") {
		t.Errorf("no milestones: %v", err)
	}
	write(t, filepath.Join(f.root, archivedMilestones, "m1.yaml"), fixtureMilestone)
	write(t, filepath.Join(f.root, "pkg", "broken_test.go"), "package pkg\nfunc (\n")
	report := f.check(t)
	if len(report.Problems) == 0 || !strings.Contains(strings.Join(report.Problems, "\n"), "解析 pkg/broken_test.go 失败") {
		t.Errorf("broken test file: %q", report.Problems)
	}
}

func TestRun(t *testing.T) {
	f := newFixture(t)
	var out, errOut bytes.Buffer
	if code := run([]string{"-root", f.root, "-table", f.table, "-min-hardened", "1"}, &out, &errOut); code != 0 ||
		!strings.Contains(out.String(), "2 条已归档完成标准全部映射到测试") {
		t.Fatalf("pass: code = %d, stdout = %q, stderr = %q", code, out.String(), errOut.String())
	}

	out.Reset()
	errOut.Reset()
	write(t, f.table, strings.Replace(fixtureTable, "pkg.TestConditional", "pkg.TestSkipped", 1))
	if code := run([]string{"-root", f.root, "-table", "table.yaml", "-min-hardened", "1"}, &out, &errOut); code != 1 ||
		!strings.Contains(errOut.String(), "发现 1 个问题") || !strings.Contains(errOut.String(), "同一变更中补表") {
		t.Fatalf("problems: code = %d, stderr = %q", code, errOut.String())
	}

	errOut.Reset()
	if code := run([]string{"-root", f.root, "-table", "missing.yaml"}, &out, &errOut); code != 1 || !strings.Contains(errOut.String(), "读取对账表失败") {
		t.Fatalf("input error: code = %d, stderr = %q", code, errOut.String())
	}
	if code := run([]string{"extra"}, &out, &errOut); code != 2 || !strings.Contains(errOut.String(), "未知参数") {
		t.Fatalf("extra arg: code = %d", code)
	}
	if code := run([]string{"-nope"}, &out, &errOut); code != 2 {
		t.Fatalf("bad flag: code = %d", code)
	}
}
