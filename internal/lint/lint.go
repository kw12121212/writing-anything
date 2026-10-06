// Package lint 是规则引擎（Rule Engine）：按合并后的生效规则（Rule）对单元正文做确定性的文本检查（Lint），给出发现项
// （Finding），并处理正文中的单处豁免（Inline Waiver）。本包只读取正文与合并结果，不修改任何文件，不发起网络请求；只返回结构化的
// 发现项与问题，由命令层转换为输出与诊断（Diagnostic）。
package lint

import (
	"fmt"
	"maps"
	"math"
	"regexp"
	"slices"
	"sort"
	"strings"
	"unicode"

	"github.com/kw12121212/writing-anything/internal/resolve"
	"github.com/kw12121212/writing-anything/internal/workspace"
)

// 文本检查的错误码（Error Code）。已发布的错误码不得改名或复用。
const (
	CodeFindings          = "lint.findings"
	CodeInvalidWaiver     = "lint.invalid-waiver"
	CodeUnknownWaiverRule = "lint.unknown-waiver-rule"
	CodeAgentWaiver       = "lint.agent-waiver"
	CodeInactiveTerms     = "lint.inactive-terms"
	CodeTooLarge          = "lint.too-large"
	CodeTruncated         = "lint.truncated"
	CodePOVUnrecognized   = "lint.pov-unrecognized"
	CodeInvalidParams     = "rule.invalid-params"
)

// Finding 是一个发现项；Line 为 0 表示全文级发现项。
type Finding struct {
	Rule       string
	Type       string
	Severity   string
	Source     string
	Line       int // 从 1 开始
	Column     int // 从 1 开始，按 Unicode 字符计
	Text       string
	Message    string
	Suggestion string
	Reason     string // 被单处豁免时的理由
}

// Result 是一次文本检查的结果。
type Result struct {
	Findings []Finding
	Waived   []Finding
	Chars    int
}

// kind 是参数的取值类型。
type kind int

const (
	kindBool kind = iota
	kindPositiveInt
	kindPositiveNumber
	kindString // 非空字符串
	kindStrings
	kindNonEmptyStrings // 至少一项的 kindStrings
)

// param 是规则类型声明的一个参数。
type param struct {
	name     string
	kind     kind
	required bool
}

// ruleType 是一种内置规则类型：参数声明与检查函数。
type ruleType struct {
	params []param
	check  func(t *text, p params, r *resolve.Resolved, in Input) ([]Finding, bool) // bool 为出现次数超过 MaxHits、结果被截断
}

// params 是已校验的参数取值。
type params map[string]any

func (p params) bool(name string, def bool) bool {
	if v, ok := p[name].(bool); ok {
		return v
	}
	return def
}

func (p params) int(name string) (int, bool) {
	v, ok := p[name].(int)
	return v, ok
}

func (p params) strings(name string) []string {
	v, _ := p[name].([]string)
	return v
}

var ruleTypes = map[string]ruleType{
	"cjk-typography":       {params: []param{{"spacing", kindBool, false}, {"punctuation", kindBool, false}}, check: checkCJK},
	"ai-cliche":            {params: []param{{"terms", kindStrings, false}, {"max_dashes_per_1000", kindPositiveNumber, false}}, check: checkCliche},
	"banned-terms":         {params: []param{{"terms", kindStrings, false}}, check: checkBanned},
	"term-consistency":     {check: checkTerms},
	"length-range":         {params: []param{{"min", kindPositiveInt, false}, {"max", kindPositiveInt, false}}, check: checkLength},
	"paragraph-length":     {params: []param{{"max", kindPositiveInt, true}}, check: checkParagraphs},
	"required-sections":    {params: []param{{"sections", kindStrings, true}}, check: checkSections},
	"claim-sources":        {params: []param{{"kinds", kindStrings, false}, {"source_types", kindStrings, false}}, check: checkClaimSources},
	"code-block-language":  {check: checkCodeLanguage},
	"link-format":          {params: []param{{"schemes", kindStrings, false}}, check: checkLinks},
	"reference-sources":    {params: []param{{"source_types", kindNonEmptyStrings, false}, {"versions", kindBool, false}, {"apis", kindBool, false}}, check: checkReferences},
	"claim-candidates":     {params: []param{{"pronouns", kindStrings, false}, {"experience_markers", kindStrings, false}, {"data", kindBool, false}, {"cover_kinds", kindStrings, false}}, check: checkCandidates},
	"ad-compliance":        {params: []param{{"terms", kindStrings, false}, {"builtin", kindBool, false}, {"basis", kindString, false}}, check: checkAdCompliance},
	"dialogue-punctuation": {check: checkDialogue},
	"pov-consistency":      {check: checkPOV},
}

// Run 对正文 content 执行 r 中的全部生效规则，按行、列、规则 id 排序发现项，并应用单处豁免。problems 为规则参数问题
// （error）与豁免注释、未生效词表的问题（warning）；rel 为 draft.md 的路径，in 给出段落出处与已登记的论断。
// 正文超过 MaxBytes 时只返回一条 lint.too-large。
func Run(content string, r *resolve.Resolved, rel string, in Input) (*Result, []workspace.Problem) {
	if len(content) > MaxBytes {
		return &Result{}, []workspace.Problem{{Code: CodeTooLarge, Severity: workspace.SeverityError, Path: rel,
			Message: fmt.Sprintf("正文有 %d 字节，超过文本检查（Lint）的上限 %d 字节；请拆分为多个单元", len(content), MaxBytes)}}
	}
	t := prepare(content)
	var problems []workspace.Problem
	var all []Finding
	ids := make([]string, 0, len(r.Rules))
	active := map[string]bool{}
	for id, rule := range r.Rules {
		if rule.Enabled {
			ids = append(ids, id)
		}
	}
	sort.Strings(ids)
	for _, id := range ids {
		rule := r.Rules[id]
		typ := fmt.Sprint(rule.Type.Value)
		rt, ok := ruleTypes[typ]
		if !ok {
			continue // 未知的规则类型已由合并校验报告
		}
		p, ps := validate(id, rt, rule, r)
		if len(ps) > 0 {
			problems = append(problems, ps...)
			continue
		}
		if typ == "pov-consistency" && in.POVPerson != povFirst && in.POVPerson != povThird {
			// pov 人称无法识别（second、未设置或写作简报不可用）时规则不执行，以 warning 提示（fiction-rule-types）。
			problems = append(problems, workspace.Problem{Code: CodePOVUnrecognized, Severity: workspace.SeverityWarning, Path: rel,
				Message: "视角与人称一致（POV Consistency）：写作简报的 pov 人称无法识别（应为 first 或 third）或写作简报不可用，规则未执行；请用 wa-cli brief set 设置 pov"})
			continue
		}
		active[typ] = true
		fs, truncated := rt.check(t, p, r, in)
		if truncated {
			problems = append(problems, workspace.Problem{Code: CodeTruncated, Severity: workspace.SeverityWarning, Path: rel,
				Message: fmt.Sprintf("规则（Rule）%s 的命中超过 %d 处，只列出前 %d 处；请先处理这些发现项后重新检查", id, MaxHits, MaxHits)})
		}
		for _, f := range fs {
			f.Rule, f.Type, f.Severity, f.Source = id, typ, fmt.Sprint(rule.Severity.Value), rule.Type.Source
			all = append(all, f)
		}
	}
	problems = append(problems, inactive(r, active, rel)...)
	res := &Result{Chars: t.chars()}
	waivers, ps := t.waivers(ids, rel, in.Origin)
	problems = append(problems, ps...)
	for _, f := range all {
		if reason, ok := waivers.waive(t, f); ok {
			f.Reason = reason
			res.Waived = append(res.Waived, f)
			continue
		}
		res.Findings = append(res.Findings, f)
	}
	sortFindings(res.Findings)
	sortFindings(res.Waived)
	return res, problems
}

// inactive 在合并后有禁用词或术语表的 avoid 写法、却没有生效的对应规则时给出 warning，避免它们静默不生效。
func inactive(r *resolve.Resolved, active map[string]bool, rel string) []workspace.Problem {
	var out []workspace.Problem
	warn := func(what, typ string) {
		out = append(out, workspace.Problem{Code: CodeInactiveTerms, Severity: workspace.SeverityWarning, Path: rel,
			Message: fmt.Sprintf("合并后的配置有%s，但没有生效的 %s 规则，文本检查不会检查它们；请在规范包或 config.yaml 中定义该类型的规则", what, typ)})
	}
	if len(r.BannedTerms) > 0 && !active["banned-terms"] {
		warn("禁用词（Banned Term）", "banned-terms")
	}
	avoid := false
	for _, g := range r.Glossary {
		avoid = avoid || len(g.Avoid) > 0
	}
	if avoid && !active["term-consistency"] {
		warn("术语表（Glossary）中要避免的写法", "term-consistency")
	}
	return out
}

func sortFindings(fs []Finding) {
	sort.SliceStable(fs, func(i, j int) bool {
		a, b := fs[i], fs[j]
		if a.Line != b.Line {
			return a.Line < b.Line
		}
		if a.Column != b.Column {
			return a.Column < b.Column
		}
		return a.Rule < b.Rule
	})
}

// validate 按规则类型校验合并后的参数，返回已转换的取值；有问题时规则不执行。问题的 path 为给出该参数的配置层的产物
// （工作区规范包或 config.yaml），内置配置层在 Origin 中写明出处（workspace-artifacts/builtin-problem-origin）。
func validate(id string, rt ruleType, rule *resolve.Rule, r *resolve.Resolved) (params, []workspace.Problem) {
	var out []workspace.Problem
	bad := func(field, source, msg string) {
		path, origin := layerOf(r, source)
		out = append(out, workspace.Problem{Code: CodeInvalidParams, Severity: workspace.SeverityError, Path: path, Origin: origin,
			Field: "rules." + id + ".params" + field, Message: fmt.Sprintf("规则（Rule）%s：%s（来自 %s）", id, msg, source)})
	}
	p := params{}
	for _, name := range slices.Sorted(maps.Keys(rule.Params)) {
		v := rule.Params[name]
		i := slices.IndexFunc(rt.params, func(q param) bool { return q.name == name })
		if i < 0 {
			bad("."+name, v.Source, fmt.Sprintf("规则类型 %v 没有参数 %s", rule.Type.Value, name))
			continue
		}
		x, ok := convert(rt.params[i].kind, v.Value)
		if !ok {
			bad("."+name, v.Source, fmt.Sprintf("参数 %s 的取值 %v 应为%s", name, v.Value, kindNames[rt.params[i].kind]))
			continue
		}
		p[name] = x
	}
	for _, q := range rt.params {
		if _, ok := rule.Params[q.name]; q.required && !ok {
			bad("."+q.name, rule.Type.Source, "缺少必需的参数 "+q.name)
		}
	}
	if len(out) == 0 && fmt.Sprint(rule.Type.Value) == "length-range" {
		lo, hasLo := p.int("min")
		hi, hasHi := p.int("max")
		switch {
		case !hasLo && !hasHi:
			bad("", rule.Type.Source, "length-range 至少要给出 min 或 max")
		case hasLo && hasHi && lo > hi:
			bad("", rule.Params["max"].Source, fmt.Sprintf("min %d 大于 max %d", lo, hi))
		}
	}
	if len(out) == 0 && fmt.Sprint(rule.Type.Value) == "ad-compliance" && !p.bool("builtin", true) && len(p.strings("terms")) == 0 {
		// 不用内置词表又没有追加词的合规规则什么也不检查，不能让它静默通过。
		source := rule.Type.Source
		if v, ok := rule.Params["builtin"]; ok {
			source = v.Source
		}
		bad(".terms", source, "builtin 为 false 时 terms 至少要有一项，否则这条规则什么也不检查")
	}
	return p, out
}

// layerOf 返回配置层 source（genre:<id>、pack:<id> 或 project）的产物路径，内置配置层返回出处说明。
func layerOf(r *resolve.Resolved, source string) (path, origin string) {
	kind, id, _ := strings.Cut(source, ":")
	switch kind {
	case resolve.SourceProject:
		return workspace.ConfigPath, ""
	case "pack":
		for _, p := range r.Packs {
			if p.ID == id && p.Path != "" {
				return p.Path, ""
			}
		}
		return "", "内置规范包 " + id
	case "genre":
		return "", "内置文体 " + id
	}
	return "", ""
}

var kindNames = map[kind]string{kindBool: "布尔值", kindPositiveInt: "正整数", kindPositiveNumber: "正数", kindString: "非空字符串", kindStrings: "非空字符串的数组", kindNonEmptyStrings: "至少一项、每项都是非空字符串的数组"}

// convert 把合并结果中的参数取值（YAML 解码得到的 bool、float64、[]any 等）转换为参数类型。
func convert(k kind, v any) (any, bool) {
	switch k {
	case kindBool:
		b, ok := v.(bool)
		return b, ok
	case kindPositiveInt, kindPositiveNumber:
		var f float64
		switch n := v.(type) {
		case float64:
			f = n
		case int:
			f = float64(n)
		case int64:
			f = float64(n)
		default:
			return nil, false
		}
		if f <= 0 || math.IsInf(f, 0) || math.IsNaN(f) {
			return nil, false
		}
		if k == kindPositiveNumber {
			return f, true
		}
		if f != math.Trunc(f) || f > math.MaxInt32 {
			return nil, false
		}
		return int(f), true
	case kindString:
		s, ok := v.(string)
		return s, ok && strings.TrimSpace(s) != ""
	default:
		items, ok := v.([]any)
		if !ok || k == kindNonEmptyStrings && len(items) == 0 {
			return nil, false
		}
		out := []string{}
		for _, it := range items {
			s, ok := it.(string)
			if !ok || strings.TrimSpace(s) == "" {
				return nil, false
			}
			out = append(out, s)
		}
		return out, true
	}
}

func isHan(r rune) bool { return unicode.Is(unicode.Han, r) }

func isASCIIAlnum(r rune) bool { return r < 128 && (unicode.IsLetter(r) || unicode.IsDigit(r)) }

var fullwidth = map[rune]string{',': "，", '.': "。", ':': "：", ';': "；", '!': "！", '?': "？"}

func checkCJK(t *text, p params, _ *resolve.Resolved, _ Input) ([]Finding, bool) {
	spacing, punct := p.bool("spacing", true), p.bool("punctuation", true)
	var out []Finding
	for i, v := range t.vis {
		for j := 0; j+1 < len(v); j++ {
			a, b := v[j], v[j+1]
			switch {
			case spacing && (isHan(a) && isASCIIAlnum(b) || isASCIIAlnum(a) && isHan(b)):
				out = append(out, Finding{Line: i + 1, Column: j + 1, Text: string([]rune{a, b}), Suggestion: string(a) + " " + string(b),
					Message: "中文排版（CJK Typography）：汉字与英文字母或数字之间应有一个空格"})
			case punct && isHan(a) && fullwidth[b] != "" && !(j+2 < len(v) && (isASCIIAlnum(v[j+2]) || v[j+2] == b)):
				// 其后紧跟英文字母、数字或同一标点（如 .env、……写作 ...）时不是句读，不报告。
				out = append(out, Finding{Line: i + 1, Column: j + 2, Text: string(b), Suggestion: fullwidth[b],
					Message: "中文排版（CJK Typography）：汉字之后应使用全角标点"})
			}
		}
	}
	return out, false
}

// wordChar 报告 r 是否属于 ASCII 词：字母、数字或连字符（如 go-kit、k8s-x 是一个词）。
func wordChar(r rune) bool { return isASCIIAlnum(r) || r == '-' }

func checkCliche(t *text, p params, _ *resolve.Resolved, _ Input) ([]Finding, bool) {
	terms := dedupe(p.strings("terms"))
	var out []Finding
	hits, truncated := newMatcher(terms).find(t)
	for _, h := range hits {
		term := terms[h.term]
		out = append(out, Finding{Line: h.line, Column: h.column, Text: term, Message: "AI 腔（AI Cliché）：「" + term + "」是常见的套话，请改为具体的表述"})
	}
	if limit, ok := p["max_dashes_per_1000"].(float64); ok {
		dashes := 0
		for _, v := range t.vis {
			prev := false
			for _, r := range v {
				if r == '—' && !prev {
					dashes++
				}
				prev = r == '—'
			}
		}
		if n := t.chars(); n > 0 {
			if density := float64(dashes) * 1000 / float64(n); density > limit {
				out = append(out, Finding{Message: fmt.Sprintf("AI 腔（AI Cliché）：破折号密度（Dash Density）为每千字 %.1f 个（%d 个破折号，%d 字），超过上限 %g", density, dashes, n, limit)})
			}
		}
	}
	return out, truncated
}

func checkBanned(t *text, p params, r *resolve.Resolved, _ Input) ([]Finding, bool) {
	type entry struct{ term, reason, replacement string }
	var entries []entry
	var terms []string
	seen := map[string]bool{}
	add := func(e entry) {
		if !seen[e.term] {
			seen[e.term] = true
			terms = append(terms, e.term)
			entries = append(entries, e)
		}
	}
	for _, b := range r.BannedTerms {
		add(entry{b.BannedTerm.Term, b.Reason, b.Replacement})
	}
	for _, term := range p.strings("terms") {
		add(entry{term: term})
	}
	var out []Finding
	hits, truncated := newMatcher(terms).find(t)
	for _, h := range hits {
		e := entries[h.term]
		msg := "禁用词（Banned Term）：不要使用「" + e.term + "」"
		if e.reason != "" {
			msg += "：" + e.reason
		}
		out = append(out, Finding{Line: h.line, Column: h.column, Text: e.term, Suggestion: e.replacement, Message: msg})
	}
	return out, truncated
}

func checkTerms(t *text, _ params, r *resolve.Resolved, _ Input) ([]Finding, bool) {
	type entry struct{ avoid, term string }
	var entries []entry
	var words, standard []string
	seen := map[string]bool{}
	for _, g := range r.Glossary {
		standard = append(standard, g.Term.Term)
		for _, a := range g.Avoid {
			if !seen[a] {
				seen[a] = true
				entries = append(entries, entry{a, g.Term.Term})
				words = append(words, a)
			}
		}
	}
	// 落在标准写法某处出现范围内的命中（如「大语言模型」中的「语言模型」）不是不一致的写法；覆盖范围不截断。
	covered := newMatcher(standard).cover(t.vis)
	var out []Finding
	hits, truncated := newMatcher(words).find(t)
	for _, h := range hits {
		e := entries[h.term]
		if covered[h.line-1][h.column-1] {
			continue
		}
		out = append(out, Finding{Line: h.line, Column: h.column, Text: e.avoid, Suggestion: e.term,
			Message: "术语一致：术语表（Glossary）中「" + e.term + "」不写作「" + e.avoid + "」"})
	}
	return out, truncated
}

func checkLength(t *text, p params, _ *resolve.Resolved, _ Input) ([]Finding, bool) {
	n := t.chars()
	if lo, ok := p.int("min"); ok && n < lo {
		return []Finding{{Message: fmt.Sprintf("字数（Word Count）为 %d，少于下限 %d", n, lo)}}, false
	}
	if hi, ok := p.int("max"); ok && n > hi {
		return []Finding{{Message: fmt.Sprintf("字数（Word Count）为 %d，超过上限 %d", n, hi)}}, false
	}
	return nil, false
}

func checkParagraphs(t *text, p params, _ *resolve.Resolved, _ Input) ([]Finding, bool) {
	hi, _ := p.int("max")
	var out []Finding
	for _, para := range t.paragraphs {
		if n := t.charsOf(para.first, para.last); n > hi {
			out = append(out, Finding{Line: para.first + 1, Column: 1, Message: fmt.Sprintf("段落有 %d 字，超过上限 %d，请拆分", n, hi)})
		}
	}
	return out, false
}

func checkSections(t *text, p params, _ *resolve.Resolved, _ Input) ([]Finding, bool) {
	have := map[string]bool{}
	for _, v := range t.vis {
		line := strings.TrimSpace(string(v))
		if headingRE.MatchString(line) {
			have[strings.TrimSpace(strings.TrimLeft(line, "#"))] = true
		}
	}
	var out []Finding
	for _, s := range p.strings("sections") {
		if !have[s] {
			out = append(out, Finding{Message: "缺少必需章节「" + s + "」：正文中没有这一标题"})
		}
	}
	return out, false
}

// waiverRE 匹配单处豁免注释的内容：lint-ignore <rule-id>: 理由（冒号可为全角）。
var waiverRE = regexp.MustCompile(`^\s*lint-ignore\s+([a-z][a-z0-9]*(?:-[a-z0-9]+)*)\s*[:：]\s*([\s\S]*?)\s*$`)

// waiverSet 按散文段落下标记录各规则的豁免理由。
type waiverSet map[int]map[string]string

// Origin 返回正文第 line 行（从 1 开始）所在段落的出处（Provenance）：human、agent 或 agent-draft-human-edited；
// 没有写入记录时为 human。
type Origin func(line int) string

// waivers 解析正文中的单处豁免注释；ids 为生效规则的 id，origin 给出注释所在段落的出处。
func (t *text) waivers(ids []string, rel string, origin Origin) (waiverSet, []workspace.Problem) {
	set := waiverSet{}
	var out []workspace.Problem
	for _, c := range t.comments {
		body := strings.TrimSpace(c.body)
		if !strings.HasPrefix(body, "lint-ignore") {
			continue
		}
		warn := func(code, msg string) {
			out = append(out, workspace.Problem{Code: code, Severity: workspace.SeverityWarning, Path: rel,
				Message: fmt.Sprintf("第 %d 行的单处豁免（Inline Waiver）%s", c.first+1, msg)})
		}
		m := waiverRE.FindStringSubmatch(body)
		switch {
		case m == nil || strings.TrimSpace(m[2]) == "":
			warn(CodeInvalidWaiver, "格式不对或没有写明理由，不生效；请写作 <!-- lint-ignore <规则 id>: 理由 -->")
			continue
		case !slices.Contains(ids, m[1]):
			warn(CodeUnknownWaiverRule, "指向的规则 "+m[1]+" 不是生效的规则，不生效")
			continue
		case origin != nil && origin(c.first+1) == "agent":
			// Agent 不能替作者豁免：注释所在段落出自 Agent 时不生效，作者改写或确认该段后才生效。
			warn(CodeAgentWaiver, "写在 Agent 写入、作者未改动的段落中，需要作者确认，不生效；请作者自己写入或修改这条注释")
			continue
		}
		// 注释在散文段落中时作用于该段落；单独成段（或跨空行）时作用于注释之后的第一个散文段落。
		k := t.paragraphOf(c.first)
		if k < 0 {
			k = t.nextParagraph(c.last)
		}
		if k < 0 {
			continue
		}
		if set[k] == nil {
			set[k] = map[string]string{}
		}
		set[k][m[1]] = strings.TrimSpace(m[2])
	}
	return set, out
}

// waive 报告发现项 f 是否被单处豁免；全文级发现项不能单处豁免。
func (w waiverSet) waive(t *text, f Finding) (string, bool) {
	if f.Line == 0 {
		return "", false
	}
	reason, ok := w[t.paragraphOf(f.Line-1)][f.Rule]
	return reason, ok
}
