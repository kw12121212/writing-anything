package cli

import (
	"fmt"
	"io"
	"strings"

	"github.com/kw12121212/writing-anything/internal/brief"
	"github.com/kw12121212/writing-anything/internal/catalog"
	"github.com/kw12121212/writing-anything/internal/work"
	"github.com/kw12121212/writing-anything/internal/workspace"
)

// codeMethodNotFound 表示没有给定 id 的写作法模板。已发布的错误码不得改名或复用。
const codeMethodNotFound = "method.not-found"

const methodHelp = "id、title、description、kind（article 文章类、structure 结构类或 planning 规划类）、source（builtin 或 pack:<id>）、" +
	"sections、beats（另含 position 的 min 与 max，以及声明了目标张力时的 tension）、steps 与 rubric"

var methodListCommand = &command{
	name:       "method list",
	summary:    "列出工作区可用的写作法模板（Method Template）：内置写作法与已启用规范包（Pack）定义的写作法",
	flags:      []flagSpec{dirFlag},
	outputKeys: []outputKey{{"methods", "按 id 排序的写作法数组，每项含 " + methodHelp}},
	exitCodes:  standardExitCodes,
	run:        runMethodList,
}

var methodShowCommand = &command{
	name:       "method show",
	summary:    "查看一个写作法模板（Method Template）的章节、节拍或规划步骤与评分标准（Rubric）条目",
	args:       []argSpec{{"method", "写作法 id（见 wa-cli method list）", true}},
	flags:      []flagSpec{dirFlag},
	outputKeys: []outputKey{{"method", "写作法：" + methodHelp + "；不存在时为 null"}},
	exitCodes:  standardExitCodes,
	run:        runMethodShow,
}

type methodView struct {
	catalog.Method
	Description string               `json:"description"`
	Sections    []catalog.MethodPart `json:"sections"`
	Beats       []catalog.MethodPart `json:"beats"`
	Steps       []catalog.MethodPart `json:"steps"`
	Rubric      []catalog.RubricItem `json:"rubric"`
	Source      string               `json:"source"`
}

func viewMethod(m catalog.MethodRef) methodView {
	v := methodView{Method: m.Method, Description: m.Description, Sections: m.Sections, Beats: m.Beats, Steps: m.Steps, Rubric: m.Rubric, Source: m.Source}
	for _, p := range []*[]catalog.MethodPart{&v.Sections, &v.Beats, &v.Steps} {
		if *p == nil {
			*p = []catalog.MethodPart{}
		}
	}
	if v.Rubric == nil {
		v.Rubric = []catalog.RubricItem{}
	}
	return v
}

type methodListResult struct {
	Result
	Methods []methodView `json:"methods"`
}

type methodShowResult struct {
	Result
	Method *methodView `json:"method"`
}

var methodKinds = map[string]string{catalog.MethodArticle: "文章类", catalog.MethodStructure: "结构类", catalog.MethodPlanning: "规划类"}

func runMethodList(inv *invocation) int {
	dir := projectDir(inv)
	res := &methodListResult{Methods: []methodView{}}
	problems := workspace.Require(dir)
	if problems == nil {
		for _, m := range catalog.LoadMethods(dir).List {
			res.Methods = append(res.Methods, viewMethod(m))
		}
	}
	res.Diagnostics = diagnostics(problems, dir)
	return finish(inv, res, func(w io.Writer) error {
		var b strings.Builder
		for _, m := range res.Methods {
			fmt.Fprintf(&b, "%s（%s，%s）：%s\n", m.ID, methodKinds[m.Kind], m.Source, m.Title)
		}
		_, err := io.WriteString(w, b.String())
		return err
	})
}

func runMethodShow(inv *invocation) int {
	dir := projectDir(inv)
	res := &methodShowResult{}
	problems := workspace.Require(dir)
	if problems == nil {
		if m, ok := catalog.LoadMethods(dir).Find(inv.args[0]); ok {
			v := viewMethod(*m)
			res.Method = &v
		} else {
			problems = []workspace.Problem{{Code: codeMethodNotFound, Severity: workspace.SeverityError, Field: "method",
				Message: "写作法（Method Template）" + inv.args[0] + " 不存在"}}
		}
	}
	res.Diagnostics = diagnostics(problems, dir)
	for i, d := range res.Diagnostics {
		if d.Code == codeMethodNotFound {
			res.Diagnostics[i].Next = []string{shellCommand("wa-cli", "method", "list", "--dir", dir, "--json")}
		}
	}
	return finish(inv, res, func(w io.Writer) error {
		m := res.Method
		if m == nil {
			return nil
		}
		var b strings.Builder
		fmt.Fprintf(&b, "%s（%s，%s）：%s\n", m.ID, methodKinds[m.Kind], m.Source, m.Title)
		if m.Description != "" {
			fmt.Fprintf(&b, "  %s\n", m.Description)
		}
		for _, list := range []struct {
			name  string
			parts []catalog.MethodPart
		}{{"章节", m.Sections}, {"节拍", m.Beats}, {"步骤", m.Steps}} {
			for i, p := range list.parts {
				fmt.Fprintf(&b, "  %s %d %s：%s", list.name, i+1, p.ID, p.Title)
				if p.Position != nil {
					fmt.Fprintf(&b, "（位置 %.0f%%–%.0f%%）", p.Position.Min*100, p.Position.Max*100)
				}
				b.WriteString("\n")
			}
		}
		for _, r := range m.Rubric {
			fmt.Fprintf(&b, "  评分项 %s：%s\n", r.ID, r.Title)
		}
		_, err := io.WriteString(w, b.String())
		return err
	})
}

// unitDefaultMethod 返回单元适用的文体默认写作法：只有作品的根单元用文体的 default_method，
// 子单元不继承（子单元的写作法只来自它自己的写作简报），见 methods 主规格的 method-selection。
func unitDefaultMethod(w *work.Work, u *work.Unit) string {
	if u.Parent == nil {
		return w.Genre.DefaultMethod
	}
	return ""
}

// unitMethod 返回单元选用的写作法：与大纲关卡相同，取写作简报 method 字段的原始取值（写作简报的其他问题不影响它），为空时
// 根单元用文体的默认写作法；为 none 或写作法不存在时为 nil（不存在的问题由 validate 与大纲关卡报告）。
func unitMethod(dir string, w *work.Work, u *work.Unit) *catalog.Method {
	m, _ := catalog.LoadMethods(dir).Pick(brief.MethodOf(dir, w.UnitDir(u)), unitDefaultMethod(w, u))
	return m
}

// danglingMethod 在单元选用的写作法不存在时返回一条 warning：审稿与验收照常执行，但不包含该写作法附带的评分项。
func danglingMethod(dir string, w *work.Work, u *work.Unit) []workspace.Problem {
	set := catalog.LoadMethods(dir)
	if _, missing := set.Pick(brief.MethodOf(dir, w.UnitDir(u)), unitDefaultMethod(w, u)); missing != "" {
		p := brief.UnknownMethod(brief.Path(w.UnitDir(u)), brief.FieldMethod, missing, set)
		p.Severity = workspace.SeverityWarning
		p.Message += "；审稿的评分标准暂不包含写作法附带的评分项"
		return []workspace.Problem{p}
	}
	return nil
}
