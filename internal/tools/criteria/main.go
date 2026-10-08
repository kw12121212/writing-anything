// criteria 对账里程碑完成标准（Done Criteria）与测试函数：读取对账表（Reconciliation Table）
// 与 .strict-spec-driven/roadmap/archive/milestones/*.yaml，逐条确认每个已归档里程碑的完成标准
// 都映射到至少一个真实存在、且函数体首层没有无条件 t.Skip 的测试函数。
//
// 用法：go run ./internal/tools/criteria [-root .] [-table internal/tools/criteria/testdata/criteria.yaml]
//
// 新里程碑归档（或修改已归档的完成标准）时，在同一变更中同步补表或重新确认映射。
// 产品二进制与 wa-cli 运行时不引用本工具。
package main

import (
	"bytes"
	"flag"
	"fmt"
	"go/ast"
	"go/build"
	"go/parser"
	"go/token"
	"io"
	"os"
	"path/filepath"
	"sort"
	"strings"
	"time"
	"unicode"
	"unicode/utf8"

	"go.yaml.in/yaml/v3"
)

// DefaultTable 是对账表相对仓库根目录的默认位置。
const DefaultTable = "internal/tools/criteria/testdata/criteria.yaml"

// archivedMilestones 是已归档里程碑文件所在目录（相对仓库根目录）。
const archivedMilestones = ".strict-spec-driven/roadmap/archive/milestones"

// MinHardened 是 2026-10-07 路线图里程碑审计列出的部分达标或未达标完成标准条数：
// 真实对账表中带 hardened_by 的表项不得少于此数。
const MinHardened = 14

// archivedChanges 是已归档变更所在目录（相对仓库根目录），目录名为 <日期>-<变更名>。
const archivedChanges = ".strict-spec-driven/changes/archive"

func main() {
	os.Exit(run(os.Args[1:], os.Stdout, os.Stderr))
}

// Table 是对账表的结构。
type Table struct {
	SchemaVersion int     `yaml:"schema_version"`
	Entries       []Entry `yaml:"entries"`
}

// Entry 把一条完成标准原文映射到测试函数。
type Entry struct {
	Milestone  string   `yaml:"milestone"`
	Criterion  string   `yaml:"criterion"`
	Tests      []string `yaml:"tests"`
	HardenedBy []string `yaml:"hardened_by"`
}

// Report 是一次对账的结果。
type Report struct {
	Criteria int      // 已归档完成标准条数
	Entries  int      // 对账表表项数
	Tests    int      // 引用的不同测试函数数
	Hardened int      // 带 hardened_by 的表项数
	Problems []string // 全部问题，按稳定顺序
}

// run 返回进程退出码：0 对账通过，1 存在问题或输入错误，2 用法错误。
func run(args []string, stdout, stderr io.Writer) int {
	fs := flag.NewFlagSet("criteria", flag.ContinueOnError)
	fs.SetOutput(stderr)
	root := fs.String("root", ".", "仓库根目录")
	table := fs.String("table", DefaultTable, "对账表路径（相对路径相对 -root 解析）")
	minHardened := fs.Int("min-hardened", MinHardened, "带 hardened_by 的审计加固表项最少条数")
	if err := fs.Parse(args); err != nil {
		return 2
	}
	if fs.NArg() > 0 {
		fmt.Fprintf(stderr, "criteria：未知参数：%s\n", fs.Arg(0))
		return 2
	}
	tablePath := *table
	if !filepath.IsAbs(tablePath) {
		tablePath = filepath.Join(*root, tablePath)
	}
	report, err := Check(*root, tablePath, *minHardened)
	if err != nil {
		fmt.Fprintf(stderr, "criteria：%v\n", err)
		return 1
	}
	if len(report.Problems) > 0 {
		fmt.Fprintf(stderr, "criteria：完成标准对账发现 %d 个问题（对账表 %s）：\n", len(report.Problems), *table)
		for _, p := range report.Problems {
			fmt.Fprintf(stderr, "  - %s\n", p)
		}
		fmt.Fprintln(stderr, "criteria：里程碑归档或完成标准修改后，请在同一变更中补表或重新确认映射。")
		return 1
	}
	fmt.Fprintf(stdout, "criteria：%d 条已归档完成标准全部映射到测试（表项 %d，测试函数 %d，审计加固表项 %d）\n",
		report.Criteria, report.Entries, report.Tests, report.Hardened)
	return 0
}

type criterionKey struct{ milestone, text string }

// Check 以 root 为仓库根目录，按 tablePath 的对账表对账，并要求带 hardened_by 的表项不少于 minHardened 条；
// 只有输入无法读取或解析时返回 error。
func Check(root, tablePath string, minHardened int) (Report, error) {
	var report Report
	tbl, err := loadTable(tablePath)
	if err != nil {
		return report, err
	}
	criteria, order, err := loadCriteria(filepath.Join(root, archivedMilestones))
	if err != nil {
		return report, err
	}
	report.Criteria = len(order)
	report.Entries = len(tbl.Entries)

	var problems []string
	add := func(format string, a ...any) { problems = append(problems, fmt.Sprintf(format, a...)) }

	if tbl.SchemaVersion != 1 {
		add("对账表 schema_version 应为 1，实际为 %d", tbl.SchemaVersion)
	}

	mapped := map[criterionKey]bool{}
	tests := map[string]bool{}
	for i, e := range tbl.Entries {
		at := fmt.Sprintf("entries[%d]（%s）", i, e.Milestone)
		key := criterionKey{e.Milestone, e.Criterion}
		switch {
		case strings.TrimSpace(e.Milestone) == "" || strings.TrimSpace(e.Criterion) == "":
			add("%s：milestone 与 criterion 都不能为空", at)
		case mapped[key]:
			add("%s：重复登记的完成标准「%s」", at, e.Criterion)
		case !criteria[key]:
			add("%s：完成标准「%s」不在已归档里程碑中（文本已变化或已删除），请按当前原文重新确认映射", at, e.Criterion)
		}
		mapped[key] = true
		if len(e.Tests) == 0 {
			add("%s：完成标准「%s」没有映射任何测试函数", at, e.Criterion)
		}
		for _, ref := range e.Tests {
			tests[ref] = true
		}
		if len(e.HardenedBy) > 0 {
			report.Hardened++
		}
		for _, change := range e.HardenedBy {
			if !archivedChange(root, change) {
				add("%s：hardened_by 中的变更 %s 不在 %s 中", at, change, archivedChanges)
			}
		}
	}
	if report.Hardened < minHardened {
		add("带 hardened_by 的审计加固表项 %d 条，少于审计列出的 %d 条", report.Hardened, minHardened)
	}
	for _, k := range order {
		if !mapped[k] {
			add("里程碑 %s 的完成标准缺少对账表项：「%s」", k.milestone, k.text)
		}
	}

	refs := make([]string, 0, len(tests))
	for ref := range tests {
		refs = append(refs, ref)
	}
	sort.Strings(refs)
	report.Tests = len(refs)
	type parsed struct {
		decls map[string]*ast.FuncDecl
		err   error
	}
	pkgs := map[string]parsed{}
	for _, ref := range refs {
		dir, name, ok := splitRef(ref)
		if !ok {
			add("测试引用 %s 格式应为「包目录.TestXxx」，如 internal/cli.TestNext", ref)
			continue
		}
		pkg, seen := pkgs[dir]
		if !seen {
			pkg.decls, pkg.err = testFuncs(root, dir)
			pkgs[dir] = pkg
		}
		if pkg.err != nil {
			add("测试引用 %s：%v", ref, pkg.err)
			continue
		}
		fn := pkg.decls[name]
		if fn == nil {
			add("测试引用 %s：%s 的 *_test.go 中没有测试函数 %s", ref, dir, name)
			continue
		}
		if skip := unconditionalSkip(fn); skip != "" {
			add("测试引用 %s：函数体首层无条件调用 %s，测试不会被执行", ref, skip)
		}
	}
	report.Problems = problems
	return report, nil
}

func loadTable(path string) (Table, error) {
	var tbl Table
	data, err := os.ReadFile(path)
	if err != nil {
		return tbl, fmt.Errorf("读取对账表失败：%w", err)
	}
	dec := yaml.NewDecoder(bytes.NewReader(data))
	dec.KnownFields(true)
	if err := dec.Decode(&tbl); err != nil {
		return tbl, fmt.Errorf("解析对账表 %s 失败：%w", path, err)
	}
	return tbl, nil
}

// loadCriteria 读取已归档里程碑的完成标准，返回集合与按文件名、原顺序排列的列表。
func loadCriteria(dir string) (map[criterionKey]bool, []criterionKey, error) {
	paths, err := filepath.Glob(filepath.Join(dir, "*.yaml"))
	if err != nil {
		return nil, nil, err
	}
	if len(paths) == 0 {
		return nil, nil, fmt.Errorf("%s 下没有已归档里程碑", dir)
	}
	sort.Strings(paths)
	set := map[criterionKey]bool{}
	var order []criterionKey
	for _, p := range paths {
		data, err := os.ReadFile(p)
		if err != nil {
			return nil, nil, fmt.Errorf("读取里程碑失败：%w", err)
		}
		var m struct {
			Milestone    map[string]any `yaml:"milestone"`
			DoneCriteria []string       `yaml:"done_criteria"`
		}
		if err := yaml.Unmarshal(data, &m); err != nil {
			return nil, nil, fmt.Errorf("解析里程碑 %s 失败：%w", p, err)
		}
		if len(m.Milestone) != 1 {
			return nil, nil, fmt.Errorf("里程碑 %s 的 milestone 应恰好含一个 id", p)
		}
		var id string
		for k := range m.Milestone {
			id = k
		}
		for _, c := range m.DoneCriteria {
			k := criterionKey{id, c}
			if !set[k] {
				set[k] = true
				order = append(order, k)
			}
		}
	}
	return set, order, nil
}

// archivedChange 判断 changes/archive 下是否有 <日期>-<name> 形式的目录。
func archivedChange(root, name string) bool {
	if strings.TrimSpace(name) == "" || strings.ContainsAny(name, `/\*?[`) {
		return false
	}
	matches, _ := filepath.Glob(filepath.Join(root, archivedChanges, "*-"+name))
	for _, m := range matches {
		base := filepath.Base(m)
		// 前缀必须恰好是 YYYY-MM-DD- 日期，排除 foo-<name> 这样的后缀误配。
		if _, err := time.Parse("2006-01-02-", strings.TrimSuffix(base, name)); err == nil {
			if info, err := os.Stat(m); err == nil && info.IsDir() {
				return true
			}
		}
	}
	return false
}

// splitRef 把「包目录.TestXxx」拆成目录与函数名。
func splitRef(ref string) (dir, name string, ok bool) {
	i := strings.LastIndex(ref, ".")
	if i <= 0 || i == len(ref)-1 {
		return "", "", false
	}
	dir, name = ref[:i], ref[i+1:]
	if !isTestName(name) || filepath.IsAbs(dir) || strings.Contains(dir, "..") {
		return "", "", false
	}
	return dir, name, true
}

// isTestName 按 go test 的规则判断函数名：Test 之后为空或不是小写字母（TestXxx，不是 Testxxx）。
func isTestName(name string) bool {
	if !strings.HasPrefix(name, "Test") {
		return false
	}
	r, _ := utf8.DecodeRuneInString(name[len("Test"):])
	return r == utf8.RuneError || !unicode.IsLower(r)
}

// testFuncs 解析 root 下包目录 dir 中当前平台会编译的 *_test.go（按构建约束与文件名后缀筛选），
// 返回顶层测试函数（func TestXxx(t *testing.T)）。
func testFuncs(root, dir string) (map[string]*ast.FuncDecl, error) {
	abs := filepath.Join(root, filepath.FromSlash(dir))
	paths, err := filepath.Glob(filepath.Join(abs, "*_test.go"))
	if err != nil {
		return nil, err
	}
	if len(paths) == 0 {
		return nil, fmt.Errorf("目录 %s 下没有 *_test.go", dir)
	}
	sort.Strings(paths)
	out := map[string]*ast.FuncDecl{}
	fset := token.NewFileSet()
	for _, p := range paths {
		if match, err := build.Default.MatchFile(abs, filepath.Base(p)); err != nil || !match {
			continue
		}
		f, err := parser.ParseFile(fset, p, nil, parser.SkipObjectResolution)
		if err != nil {
			return nil, fmt.Errorf("解析 %s/%s 失败：%w", dir, filepath.Base(p), err)
		}
		for _, d := range f.Decls {
			fn, ok := d.(*ast.FuncDecl)
			if !ok || fn.Recv != nil || fn.Body == nil || !isTestName(fn.Name.Name) || !isTestingT(fn) {
				continue
			}
			out[fn.Name.Name] = fn
		}
	}
	return out, nil
}

// isTestingT 判断函数签名是否为 (x *testing.T)。
func isTestingT(fn *ast.FuncDecl) bool {
	params := fn.Type.Params.List
	if len(params) != 1 || len(params[0].Names) > 1 || fn.Type.Results != nil {
		return false
	}
	star, ok := params[0].Type.(*ast.StarExpr)
	if !ok {
		return false
	}
	sel, ok := star.X.(*ast.SelectorExpr)
	if !ok {
		return false
	}
	pkg, ok := sel.X.(*ast.Ident)
	return ok && pkg.Name == "testing" && sel.Sel.Name == "T"
}

// unconditionalSkip 返回函数体首层语句中无条件调用的 Skip、Skipf 或 SkipNow（如 t.Skip），没有时返回空串。
func unconditionalSkip(fn *ast.FuncDecl) string {
	for _, stmt := range fn.Body.List {
		expr, ok := stmt.(*ast.ExprStmt)
		if !ok {
			continue
		}
		call, ok := expr.X.(*ast.CallExpr)
		if !ok {
			continue
		}
		sel, ok := call.Fun.(*ast.SelectorExpr)
		if !ok {
			continue
		}
		switch sel.Sel.Name {
		case "Skip", "Skipf", "SkipNow":
			if recv, ok := sel.X.(*ast.Ident); ok {
				return recv.Name + "." + sel.Sel.Name
			}
			return sel.Sel.Name
		}
	}
	return ""
}
