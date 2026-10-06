package cli

import (
	"fmt"
	"io"
	"slices"
	"strings"

	"github.com/kw12121212/writing-anything/internal/brief"
	"github.com/kw12121212/writing-anything/internal/catalog"
	"github.com/kw12121212/writing-anything/internal/draft"
	"github.com/kw12121212/writing-anything/internal/lint"
	"github.com/kw12121212/writing-anything/internal/outline"
	"github.com/kw12121212/writing-anything/internal/resolve"
	"github.com/kw12121212/writing-anything/internal/series"
	"github.com/kw12121212/writing-anything/internal/source"
	"github.com/kw12121212/writing-anything/internal/work"
	"github.com/kw12121212/writing-anything/internal/workspace"
)

const findingHelp = "每项含 rule、type、severity、source、line、column、text、message 与 suggestion"

var lintCommand = &command{
	name:    "lint",
	summary: "对单元（Unit）正文执行文本检查（Lint）：按生效规则（Rule）输出发现项（Finding），包括论断（Claim）是否关联作者素材与疑似未登记的论断，有 error 级发现项时以退出码 1 退出；正文注释 <!-- lint-ignore <规则 id>: 理由 --> 豁免所在段落（单独成段时为下一段）中该规则的发现项，须由作者写入",
	args:    []argSpec{{"unit", unitRefHelp, true}},
	flags:   []flagSpec{dirFlag},
	outputKeys: []outputKey{
		{"findings", "未被豁免的发现项，按行、列、规则排序；" + findingHelp},
		{"waived", "被单处豁免（Waiver）的发现项，另含 reason"},
		{"chars", "正文字数（Word Count）：屏蔽区域之外的非空白字符数"},
	},
	exitCodes: standardExitCodes,
	run:       runLint,
}

type findingView struct {
	Rule       string  `json:"rule"`
	Type       string  `json:"type"`
	Severity   string  `json:"severity"`
	Source     string  `json:"source"`
	Line       *int    `json:"line"`
	Column     *int    `json:"column"`
	Text       *string `json:"text"`
	Message    string  `json:"message"`
	Suggestion *string `json:"suggestion"`
	Reason     *string `json:"reason,omitempty"`
}

type lintResult struct {
	Result
	Findings []findingView `json:"findings"`
	Waived   []findingView `json:"waived"`
	Chars    int           `json:"chars"`
}

func viewFinding(f lint.Finding, waived bool) findingView {
	v := findingView{Rule: f.Rule, Type: f.Type, Severity: f.Severity, Source: f.Source, Message: f.Message,
		Text: optional(f.Text), Suggestion: optional(f.Suggestion)}
	if f.Line > 0 {
		v.Line, v.Column = &f.Line, &f.Column
	}
	if waived {
		v.Reason = &f.Reason
	}
	return v
}

// paragraphOrigin 返回按行查询段落出处（Provenance）的函数，供单处豁免判断注释是否出自作者；每行的出处一次算好。
// 写入记录存在但无法推导出处时，所有豁免都不生效（按 Agent 写入处理），并以 warning 报告写入记录的问题。
func paragraphOrigin(dir string, w *work.Work, u *work.Unit) (lint.Origin, []workspace.Problem) {
	rep, ps := draft.Provenance(dir, w, u)
	if len(ps) > 0 {
		for i := range ps {
			ps[i].Severity = workspace.SeverityWarning
			ps[i].Message = "无法推导段落出处，单处豁免（Waiver）都不生效：" + ps[i].Message
		}
		return func(int) string { return draft.OriginAgent }, ps
	}
	if rep == nil {
		return nil, nil
	}
	var byLine []string // byLine[i] 为第 i+1 行所在段落的出处
	for _, p := range rep.Paragraphs {
		last := p.Line + strings.Count(p.Text, "\n")
		for len(byLine) < last {
			byLine = append(byLine, draft.OriginHuman)
		}
		for l := p.Line; l <= last; l++ {
			byLine[l-1] = p.Origin
		}
	}
	return func(line int) string {
		if line >= 1 && line <= len(byLine) {
			return byLine[line-1]
		}
		return draft.OriginHuman
	}, nil
}

// claimRules 报告合并结果中是否有生效的、读取论断的规则（claim-sources、claim-candidates 或 reference-sources）；没有时
// lint 不读取 claims.yaml。
func claimRules(r *resolve.Resolved) bool {
	for _, rule := range r.Rules {
		if t := fmt.Sprint(rule.Type.Value); rule.Enabled && (t == "claim-sources" || t == "claim-candidates" || t == "reference-sources") {
			return true
		}
	}
	return false
}

// lintClaims 读取单元的论断及其所关联素材的类型；每份素材只读取一次。claims.yaml 不通过校验时 invalid 为 true。
func lintClaims(dir string, w *work.Work, u *work.Unit) (claims []lint.Claim, invalid bool, problems []workspace.Problem) {
	cs, ps := draft.LoadClaims(dir, w, u)
	types := map[string]string{}
	for _, c := range cs {
		lc := lint.Claim{ID: c.ID, Kind: c.Kind, Text: c.Text}
		for _, id := range c.Sources {
			typ, ok := types[id]
			if !ok {
				if src, _ := source.Load(dir, id); src != nil {
					typ = src.Type
				}
				types[id] = typ
			}
			if typ != "" {
				lc.Sources = append(lc.Sources, lint.ClaimSource{ID: id, Type: typ})
			}
		}
		claims = append(claims, lc)
	}
	return claims, len(ps) > 0, ps
}

// povRules 报告合并结果中是否有生效的 pov-consistency 规则；没有时 lint 不读取写作简报。
func povRules(r *resolve.Resolved) bool {
	for _, rule := range r.Rules {
		if rule.Enabled && fmt.Sprint(rule.Type.Value) == "pov-consistency" {
			return true
		}
	}
	return false
}

// lintPOV 读取 pov 的人称：从本单元到根单元自下而上第一个声明了 pov 的写作简报（场景常有自己的简报，而 pov 声明在
// 故事根单元的简报里）；写作简报不可用或没有任何简报声明 pov 时返回空字符串，规则不执行（由规则引擎以
// lint.pov-unrecognized 提示）。写作简报的问题不在这里报告：lint 不因简报问题失败。
func lintPOV(dir string, w *work.Work, u *work.Unit) string {
	q := brief.LoadQueue(dir)
	for x := u; x != nil; x = x.Parent {
		b, ps := brief.Load(dir, w.UnitDir(x), q)
		if len(ps) > 0 {
			return ""
		}
		if b != nil && b.Pov != nil {
			return b.Pov.Person
		}
	}
	return ""
}

// runUnitLint 读取单元 u 的正文、合并生效规则与论断并运行规则引擎；lint、review show 与 accept 共用，合并结果 r 一并返回，
// 调用方不必再合并一次。out 为 nil 时文本检查没有运行（没有正文、合并失败等），problems 为其原因；否则 problems 含段落出处、
// 论断与规则引擎的问题。
func runUnitLint(dir string, w *work.Work, u *work.Unit) (out *lint.Result, r *resolve.Resolved, content []byte, problems []workspace.Problem) {
	return runUnitLintWith(dir, w, u, workResolver(dir, w))
}

// workResolver 返回按作品 w 合并生效设置的函数，结果只计算一次：对同一作品的多个单元运行文本检查（如阅读页）时，系列文件与
// 规范包只加载一次（workspace-artifacts/load-once-per-command）。
func workResolver(dir string, w *work.Work) func() (*resolve.Resolved, []workspace.Problem) {
	var r *resolve.Resolved
	var ps []workspace.Problem
	done := false
	return func() (*resolve.Resolved, []workspace.Problem) {
		if !done {
			r, ps = series.Resolve(dir, w)
			done = true
		}
		return r, slices.Clone(ps)
	}
}

// runUnitLintWith 与 runUnitLint 相同，合并结果由 resolved 给出。
func runUnitLintWith(dir string, w *work.Work, u *work.Unit, resolved func() (*resolve.Resolved, []workspace.Problem)) (out *lint.Result, r *resolve.Resolved, content []byte, problems []workspace.Problem) {
	rel := w.UnitDir(u) + "/" + draft.FileName
	content, ok, ps := draft.Read(dir, w, u)
	switch {
	case len(ps) > 0:
		return nil, nil, nil, ps
	case !ok:
		return nil, nil, nil, []workspace.Problem{{Code: draft.CodeNotFound, Severity: workspace.SeverityError, Path: rel,
			Message: "单元 " + u.Ref + " 没有可读取的正文 draft.md（不存在、途经符号链接或不是普通文件）；请用 wa-cli draft write 写入"}}
	}
	r, problems = resolved()
	if r == nil {
		return nil, nil, content, problems
	}
	origin, warnings := paragraphOrigin(dir, w, u)
	in := lint.Input{Origin: origin, Unit: u.Ref}
	var cps []workspace.Problem
	if claimRules(r) {
		in.Claims, in.ClaimsInvalid, cps = lintClaims(dir, w, u)
	}
	if povRules(r) {
		in.POVPerson = lintPOV(dir, w, u)
	}
	out, ps = lint.Run(string(content), r, rel, in)
	return out, r, content, slices.Concat(problems, warnings, cps, ps, beatPositions(dir, w, u, content, rel))
}

// beatPositions 按单元选用的结构类写作法核对成稿中节拍的位置，超出范围时给出 warning（只提示，不阻塞）。
func beatPositions(dir string, w *work.Work, u *work.Unit, content []byte, rel string) []workspace.Problem {
	unitDir := w.UnitDir(u)
	ms := outline.LoadMessages(dir, unitDir, brief.LoadQueue(dir), outline.Picker{Methods: catalog.LoadMethods(dir), Default: unitDefaultMethod(w, u)})
	if ms.Method == nil || len(ms.Problems) > 0 {
		return nil
	}
	o, _, _ := outline.LoadLenient(dir, unitDir, ms)
	return outline.BeatPositions(content, rel, o, ms.Method)
}

func runLint(inv *invocation) int {
	dir := projectDir(inv)
	res := &lintResult{Findings: []findingView{}, Waived: []findingView{}}
	problems := workspace.Require(dir)
	var rel string
	checked := false
	if problems == nil {
		var w *work.Work
		var u *work.Unit
		if w, u, problems = work.Resolve(dir, inv.args[0]); u != nil {
			rel = w.UnitDir(u) + "/" + draft.FileName
			var out *lint.Result
			if out, _, _, problems = runUnitLint(dir, w, u); out != nil {
				errors := 0
				for _, f := range out.Findings {
					res.Findings = append(res.Findings, viewFinding(f, false))
					if f.Severity == workspace.SeverityError {
						errors++
					}
				}
				for _, f := range out.Waived {
					res.Waived = append(res.Waived, viewFinding(f, true))
				}
				res.Chars, checked = out.Chars, true
				if errors > 0 {
					problems = append(problems, workspace.Problem{Code: lint.CodeFindings, Severity: workspace.SeverityError, Path: rel,
						Message: fmt.Sprintf("文本检查（Lint）有 %d 条 error 级发现项（Finding）；请按 findings 中的 message 与建议改写正文或为论断（Claim）关联作者素材后重新检查", errors)})
				}
			}
		}
	}
	res.Diagnostics = diagnostics(problems, dir)
	for i, d := range res.Diagnostics {
		switch d.Code {
		case lint.CodeFindings:
			res.Diagnostics[i].Next = []string{"wa-cli help draft write --json", shellCommand("wa-cli", "lint", inv.args[0], "--dir", dir, "--json")}
			if slices.ContainsFunc(res.Findings, func(f findingView) bool { return f.Type == "claim-sources" && f.Severity == workspace.SeverityError }) {
				// 缺少作者素材的论断：登记素材后改写论断的关联，而不是改写正文。
				res.Diagnostics[i].Next = append([]string{"wa-cli help source add --json", "wa-cli help claim set --json",
					shellCommand("wa-cli", "claim", "list", inv.args[0], "--dir", dir, "--json")}, res.Diagnostics[i].Next...)
			}
		case lint.CodeInvalidParams:
			// 参数只在 lint 执行规则时校验，validate 检不出，用本命令复查。
			res.Diagnostics[i].Next = []string{shellCommand("wa-cli", "lint", inv.args[0], "--dir", dir, "--json"), shellCommand("wa-cli", "config", "resolved", "--dir", dir, "--json")}
		case lint.CodeInvalidWaiver, lint.CodeUnknownWaiverRule, lint.CodeAgentWaiver:
			res.Diagnostics[i].Next = []string{"wa-cli help lint --json", shellCommand("wa-cli", "provenance", inv.args[0], "--dir", dir, "--json")}
		case lint.CodeInactiveTerms, lint.CodeTruncated:
			res.Diagnostics[i].Next = []string{shellCommand("wa-cli", "lint", inv.args[0], "--dir", dir, "--json"), shellCommand("wa-cli", "config", "resolved", "--dir", dir, "--json")}
		case lint.CodePOVUnrecognized:
			res.Diagnostics[i].Next = []string{"wa-cli help brief set --json", shellCommand("wa-cli", "lint", inv.args[0], "--dir", dir, "--json")}
		case lint.CodeTooLarge:
			res.Diagnostics[i].Next = []string{"wa-cli help unit add --json", shellCommand("wa-cli", "lint", inv.args[0], "--dir", dir, "--json")}
		}
	}
	return finish(inv, res, func(out io.Writer) error {
		var b strings.Builder
		if !checked {
			return nil
		}
		fmt.Fprintf(&b, "文本检查（Lint）%s：%d 条发现项（Finding），%d 条已豁免，字数 %d\n", rel, len(res.Findings), len(res.Waived), res.Chars)
		for _, f := range res.Findings {
			where := "全文"
			if f.Line != nil {
				where = fmt.Sprintf("第 %d 行第 %d 列", *f.Line, *f.Column)
			}
			fmt.Fprintf(&b, "  %s [%s %s]：%s", where, f.Severity, f.Rule, f.Message)
			if f.Suggestion != nil {
				fmt.Fprintf(&b, "；建议：%s", *f.Suggestion)
			}
			b.WriteString("\n")
		}
		_, err := io.WriteString(out, b.String())
		return err
	})
}
