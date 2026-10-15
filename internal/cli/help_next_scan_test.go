package cli

import (
	"fmt"
	"go/ast"
	"go/parser"
	"go/token"
	"io/fs"
	"path/filepath"
	"slices"
	"sort"
	"strconv"
	"strings"
	"testing"
)

// helpOnlyAllowed 是 next 只含 wa-cli help 的诊断构造位置白名单，键为「相对 internal 的文件路径:所在函数」，
// 值为该函数内允许的位置数与理由。新增只给 help 的位置、或改造后未从白名单删除时 TestHelpOnlyNextAllowlist 失败。
var helpOnlyAllowed = map[string]struct {
	count  int
	reason string
}{
	"cli/parse.go:usageDiagnostic":          {1, "用法错误（usage.*）发生在解析出单元与产物之前，没有可引用的单元或产物；cli 主规格要求 next 包含该命令的用法"},
	"cli/units.go:runNew":                   {1, "作品 id 不合法是本次参数的问题，作品尚未创建、没有可引用的单元；查看用法即修复依据"},
	"cli/units.go:rewriteParamNext":         {1, "unit add 与 unit move 的参数问题：units-and-lifecycle 主规格 unit-move 规定 next 为 wa-cli help unit move --json"},
	"cli/decisions.go:runDecisionAdd":       {1, "非生命周期主路径：decision add 的参数问题，决策尚未写入、没有可引用的产物（lesson-ccfda7 按参数名白名单分流）"},
	"cli/export.go:runExport":               {1, "非生命周期主路径：export 的输出路径与格式参数问题，与单元产物无关"},
	"cli/feedback.go:runFeedbackAdd":        {1, "非生命周期主路径：feedback add 的参数与引文锚点问题，修复依据是用法"},
	"cli/feedback.go:runFeedbackResolve":    {1, "非生命周期主路径：feedback resolve 的 status 参数与锚点值问题"},
	"cli/handoff.go:handoffNext":            {2, "非生命周期主路径：交接记录是工作区级产物，输入与参数问题没有单元可引用"},
	"cli/impact.go:impactNext":              {1, "非生命周期主路径：impact clear 缺少作者确认参数，需作者给出确认原话"},
	"cli/learn.go:finishProposal":           {1, "非生命周期主路径：pack learn/prefer/confirm/reject 的参数问题兜底，其余错误码已给出 source list 等具体命令"},
	"cli/marketing.go:runMarketingSet":      {1, "非生命周期主路径包之外的营销要素：marketing set 的参数问题，路线图把它排除在本次改造包之外"},
	"cli/marketing.go:runMarketingAsk":      {1, "非生命周期主路径包之外的营销要素：marketing.yaml 不存在时只能先用 marketing set 创建"},
	"cli/mcpinstall.go:runMCPInstall":       {1, "非生命周期主路径且不需要工作区：mcp 主规格规定参数与配置问题的 next 为 wa-cli help mcp install --json"},
	"cli/pack.go:runPackInstall":            {1, "非生命周期主路径：genres-and-packs 主规格规定来源与参数问题的 next 为 wa-cli help pack install --json"},
	"cli/pack.go:runPackRemove":             {1, "非生命周期主路径：pack remove 的 id 参数问题，与单元产物无关"},
	"cli/plan.go:planParamNext":             {1, "非生命周期主路径：plan step/confirm 的参数问题兜底，其余错误码已追加 method list、plan show 等具体命令"},
	"cli/preview.go:previewServer.feedback": {1, "非生命周期主路径：preview 主规格规定阅读页请求问题的 next 为 wa-cli help preview --json"},
	"cli/preview_confirm.go:pageNext":       {1, "非生命周期主路径：preview 主规格规定阅读页请求问题的 next 为 wa-cli help preview --json"},
	"cli/reviewpacket.go:packetsNext":       {2, "非生命周期主路径：review plan/submit 的提交内容与参数问题，任务包输入由 Agent 重新组织"},
	"cli/series.go:finishSeries":            {1, "非生命周期主路径：系列（series set/add/fact）的参数问题，系列是跨作品产物"},
	"cli/series.go:runSummarySet":           {2, "非生命周期主路径：summary set 的摘要输入与参数问题，修复依据是用法与文体字数范围"},
	"cli/canon.go:runCanonRemove":           {1, "非生命周期主路径：canon remove 遇到锚点值，需作者先去掉 canon.yaml 中的锚点"},
	"cli/feedback.go:runFeedbackList":       {1, "非生命周期主路径：feedback list 的参数取值问题"},
	"cli/feedback.go:runFeedbackWatch":      {1, "非生命周期主路径：feedback watch 的 --interval/--max 参数问题，cli-contract invalid-value-code 的示例即为此用法"},
	"cli/knowledge.go:runKnowledgeAt":       {1, "非生命周期主路径：knowledge at 的 --scene 参数不是场景，只读查询"},
	"cli/mcp.go:runMCP":                     {1, "非生命周期主路径：mcp 主规格规定缺少 --dir 等参数问题的 next 为 wa-cli help mcp --json"},
	"cli/metrics.go:runMetricsRecord":       {1, "非生命周期主路径：metrics record 无法读取打分输入文件"},
	"cli/preview.go:runPreview":             {1, "非生命周期主路径：preview 主规格规定参数与输出路径问题的 next 为 wa-cli help preview --json"},
	"cli/preview.go:servePreview":           {1, "非生命周期主路径：preview --serve 监听失败，preview 主规格规定 next 为 wa-cli help preview --json"},
	"cli/recall.go:runRecall":               {1, "非生命周期主路径：recall 的检索参数问题，只读查询"},
	"cli/sources.go:runClaimRemove":         {1, "非生命周期主路径：claim remove 遇到锚点值，需作者先去掉 claims.yaml 中的锚点"},
	"cli/sources.go:runSourceAdd":           {1, "非生命周期主路径：source add 的素材输入问题，素材尚未登记、没有可引用的产物"},
	"cli/state.go:runStateAt":               {1, "非生命周期主路径：state at 的 --scene 参数不是场景，只读查询"},
	"cli/skills.go:runSkillsInstall":        {1, "非生命周期主路径且不需要工作区：skills 主规格规定 next 为 wa-cli help skills install --json"},
}

// helpOnlySite 是一处 next 只含 wa-cli help 的 []string 字面量。
type helpOnlySite struct {
	key  string
	line int
}

// TestHelpOnlyNextAllowlist 用 go/ast 扫描 internal/** 非测试源码（不含 internal/tools），列出元素全部是
// wa-cli help 提示的 []string 字面量（即 next 只给查看用法的位置），并与白名单逐个函数比对。
func TestHelpOnlyNextAllowlist(t *testing.T) {
	sites := scanHelpOnlySites(t, filepath.Join("..", "..", "internal"))
	found := map[string][]int{}
	for _, s := range sites {
		found[s.key] = append(found[s.key], s.line)
	}
	keys := make([]string, 0, len(found))
	for k := range found {
		keys = append(keys, k)
	}
	sort.Strings(keys)
	for _, k := range keys {
		allowed, ok := helpOnlyAllowed[k]
		switch {
		case !ok:
			t.Errorf("%s 第 %v 行的 next 只含 wa-cli help：改为带单元引用或产物路径的具体修复命令，或在 helpOnlyAllowed 中写明理由", k, found[k])
		case len(found[k]) > allowed.count:
			t.Errorf("%s 有 %d 处 next 只含 wa-cli help（第 %v 行），白名单只允许 %d 处", k, len(found[k]), found[k], allowed.count)
		case len(found[k]) < allowed.count:
			t.Errorf("%s 只剩 %d 处 next 只含 wa-cli help（第 %v 行），把白名单的 count 从 %d 下调", k, len(found[k]), found[k], allowed.count)
		}
	}
	for k, allowed := range helpOnlyAllowed {
		if _, ok := found[k]; !ok {
			t.Errorf("白名单条目 %s 已无 next 只含 wa-cli help 的位置，删除该条目", k)
		}
		if strings.TrimSpace(allowed.reason) == "" {
			t.Errorf("白名单条目 %s 没有写明理由", k)
		}
	}
}

// scanHelpOnlySites 解析 root 下除 tools 外的非测试 Go 源码，返回所有 next 只含 wa-cli help 的 []string 字面量。
func scanHelpOnlySites(t *testing.T, root string) []helpOnlySite {
	t.Helper()
	var sites []helpOnlySite
	fset := token.NewFileSet()
	err := filepath.WalkDir(root, func(path string, d fs.DirEntry, err error) error {
		if err != nil {
			return err
		}
		if d.IsDir() {
			if path == filepath.Join(root, "tools") || d.Name() == "testdata" {
				return filepath.SkipDir
			}
			return nil
		}
		if !strings.HasSuffix(path, ".go") || strings.HasSuffix(path, "_test.go") {
			return nil
		}
		f, err := parser.ParseFile(fset, path, nil, 0)
		if err != nil {
			return err
		}
		rel, _ := filepath.Rel(root, path)
		for _, decl := range f.Decls {
			fn, ok := decl.(*ast.FuncDecl)
			if !ok || fn.Body == nil {
				continue
			}
			key := filepath.ToSlash(rel) + ":" + funcName(fn)
			for _, line := range helpOnlyLiterals(fset, fn.Body) {
				sites = append(sites, helpOnlySite{key: key, line: line})
			}
		}
		return nil
	})
	if err != nil {
		t.Fatal(err)
	}
	return sites
}

// funcName 返回函数名，方法写作 Recv.Name。
func funcName(fn *ast.FuncDecl) string {
	if fn.Recv == nil || len(fn.Recv.List) == 0 {
		return fn.Name.Name
	}
	recv := fn.Recv.List[0].Type
	if star, ok := recv.(*ast.StarExpr); ok {
		recv = star.X
	}
	if id, ok := recv.(*ast.Ident); ok {
		return id.Name + "." + fn.Name.Name
	}
	return fn.Name.Name
}

// helpOnlyLiterals 返回函数体内元素全部是 wa-cli help 提示的 []string 字面量所在行，以及不带 extra 调用
// helpNext（把 next 改为只含 wa-cli help 的辅助函数）的位置。作为 append 首个参数、且后面还追加了
// 不是 help 提示的元素的字面量不计入：追加的元素可能是具体命令；helpNext 自身以 extra... 追加，由调用处计数。
func helpOnlyLiterals(fset *token.FileSet, body *ast.BlockStmt) []int {
	helpVars := map[string]bool{}
	ast.Inspect(body, func(n ast.Node) bool {
		switch v := n.(type) {
		case *ast.AssignStmt:
			if len(v.Lhs) == len(v.Rhs) {
				for i, lhs := range v.Lhs {
					if id, ok := lhs.(*ast.Ident); ok && isHelpExpr(v.Rhs[i], nil) {
						helpVars[id.Name] = true
					}
				}
			}
		case *ast.ValueSpec:
			// 函数内的 const 与 var 声明。
			if len(v.Names) == len(v.Values) {
				for i, id := range v.Names {
					if isHelpExpr(v.Values[i], nil) {
						helpVars[id.Name] = true
					}
				}
			}
		}
		return true
	})
	skip := map[*ast.CompositeLit]bool{}
	var lines []int
	ast.Inspect(body, func(n ast.Node) bool {
		if call, ok := n.(*ast.CallExpr); ok {
			if id, ok := call.Fun.(*ast.Ident); ok {
				switch {
				case id.Name == "helpNext" && len(call.Args) <= 3:
					lines = append(lines, fset.Position(call.Pos()).Line)
				case id.Name == "append" && len(call.Args) > 1:
					if lit, ok := call.Args[0].(*ast.CompositeLit); ok && !allHelp(call.Args[1:], helpVars) {
						skip[lit] = true
					}
				}
			}
		}
		lit, ok := n.(*ast.CompositeLit)
		if !ok || skip[lit] || len(lit.Elts) == 0 || !isStringSlice(lit.Type) {
			return true
		}
		for _, e := range lit.Elts {
			if !isHelpExpr(e, helpVars) {
				return true
			}
		}
		lines = append(lines, fset.Position(lit.Pos()).Line)
		return true
	})
	return lines
}

// allHelp 报告 append 追加的元素是否全部是 wa-cli help 提示；以 extra... 展开的变量取值未知，不算。
func allHelp(args []ast.Expr, helpVars map[string]bool) bool {
	for _, a := range args {
		if !isHelpExpr(a, helpVars) {
			return false
		}
	}
	return true
}

func isStringSlice(e ast.Expr) bool {
	arr, ok := e.(*ast.ArrayType)
	if !ok || arr.Len != nil {
		return false
	}
	id, ok := arr.Elt.(*ast.Ident)
	return ok && id.Name == "string"
}

// isHelpExpr 判断表达式是否为 wa-cli help 提示：以 "wa-cli help" 开头的字符串字面量、
// 以这样的字面量开头的字符串拼接，或在同一函数内被赋值为这样的表达式的局部变量。
func isHelpExpr(e ast.Expr, helpVars map[string]bool) bool {
	switch v := e.(type) {
	case *ast.BasicLit:
		if v.Kind != token.STRING {
			return false
		}
		s, err := strconv.Unquote(v.Value)
		return err == nil && (s == "wa-cli help" || strings.HasPrefix(s, "wa-cli help "))
	case *ast.BinaryExpr:
		return v.Op == token.ADD && isHelpExpr(v.X, helpVars)
	case *ast.ParenExpr:
		return isHelpExpr(v.X, helpVars)
	case *ast.Ident:
		return helpVars[v.Name]
	}
	return false
}

// TestHelpOnlyScannerDetects 用内联源码确认扫描器识别字面量、拼接、局部变量、函数内 const/var、只追加 help 的 append
// 与不带 extra 的 helpNext 调用，并跳过含具体命令、追加了其他元素或以 extra... 展开的写法。
func TestHelpOnlyScannerDetects(t *testing.T) {
	src := `package p
func a(dir, name string) {
	_ = []string{"wa-cli help draft write --json"}
	_ = []string{"wa-cli help " + name + " --json", "wa-cli help unit add --json"}
	h := "wa-cli help --json"
	_ = []string{h}
	_ = []string{"wa-cli help x --json", shellCommand("wa-cli", "status")}
	_ = append([]string{"wa-cli help x --json"}, name)
	_ = []string{"wa-cli status --json"}
	const c = "wa-cli help c --json"
	var v = "wa-cli help v --json"
	_ = []string{c, v}
	_ = append([]string{"wa-cli help x --json"}, "wa-cli help y --json")
	helpNext(nil, "x", nil)
	helpNext(nil, "x", nil, "wa-cli status --json")
	_ = append([]string{"wa-cli help x --json"}, extra...)
}`
	fset := token.NewFileSet()
	f, err := parser.ParseFile(fset, "p.go", src, 0)
	if err != nil {
		t.Fatal(err)
	}
	got := helpOnlyLiterals(fset, f.Decls[0].(*ast.FuncDecl).Body)
	if fmt.Sprint(got) != "[3 4 6 12 13 14]" {
		t.Fatalf("扫描结果 %v，期望第 3、4、6、12、13、14 行", got)
	}
}

// concreteFirst 报告诊断的 next 是否以具体命令 first 开头，且仍以 wa-cli help 命令 help 作补充（cli/diagnostic-next-concrete）。
func concreteFirst(d Diagnostic, first, help string) bool {
	return len(d.Next) > 1 && d.Next[0] == first && slices.Contains(d.Next[1:], help)
}
