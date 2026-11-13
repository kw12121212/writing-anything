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

	"github.com/kw12121212/writing-anything/internal/mdscan"
	"github.com/kw12121212/writing-anything/internal/resolve"
)

// 检查 AI 腔（AI Tone）的两种规则类型（lint/tone-rule-types）：formulaic-patterns 按有序词组找句式套路（Formulaic
// Pattern），tone-density 按全文统计腔调密度（Tone Density）。

// minDensityChars 是 tone-density 判定所需的最少字数（屏蔽后的非空白字符数）。
const minDensityChars = 300

// minDensitySentences 与 minDensitySections 是句长标准差与节长变异系数判定所需的最少句数与节数。
const (
	minDensitySentences = 10
	minDensitySections  = 3
)

var (
	idRE        = regexp.MustCompile(`^[a-z][a-z0-9]*(-[a-z0-9]+)*$`)
	sectionRE   = regexp.MustCompile(`^##\s`)
	patternKeys = []string{"id", "terms", "scope", "position", "at_sentence_start", "message"}
)

// pattern 是 formulaic-patterns 的一个已校验的模式。
type pattern struct {
	id        string
	terms     []string
	paragraph bool   // scope 为 paragraph
	position  string // any、paragraph-start 或 paragraph-end
	startOnly bool   // at_sentence_start
	message   string
}

// fieldProblem 是参数中一个子字段的问题：field 接在参数名之后（如 [2].terms）。
type fieldProblem struct {
	field, msg string
}

// parsePatterns 校验 formulaic-patterns 的 patterns 参数（YAML 解码得到的 []any），返回模式或子字段的问题。
func parsePatterns(v any) ([]pattern, []fieldProblem) {
	items, ok := v.([]any)
	if !ok || len(items) == 0 {
		return nil, []fieldProblem{{"", "patterns 应为至少一项的模式数组"}}
	}
	var out []pattern
	var probs []fieldProblem
	seen := map[string]bool{}
	for i, it := range items {
		at := fmt.Sprintf("[%d]", i)
		bad := func(field, msg string) {
			probs = append(probs, fieldProblem{at + field, fmt.Sprintf("patterns%s%s：%s", at, field, msg)})
		}
		m, ok := it.(map[string]any)
		if !ok {
			bad("", "模式应为含 id 与 terms 的对象")
			continue
		}
		pt := pattern{position: "any"}
		n := len(probs)
		for _, k := range slices.Sorted(maps.Keys(m)) {
			if !slices.Contains(patternKeys, k) {
				bad("."+k, "模式没有字段 "+k+"（可用的字段为 "+strings.Join(patternKeys, "、")+"）")
			}
		}
		switch id, ok := m["id"].(string); {
		case m["id"] == nil:
			bad(".id", "缺少 id")
		case !ok || !idRE.MatchString(id):
			bad(".id", fmt.Sprintf("id %v 应为小写字母开头、以连字符分隔的 id", m["id"]))
		case seen[id]:
			bad(".id", "id "+id+" 在同一规则内重复")
		default:
			seen[id], pt.id = true, id
		}
		terms, ok := m["terms"].([]any)
		switch {
		case m["terms"] == nil:
			bad(".terms", "缺少 terms")
		case !ok || len(terms) == 0 || len(terms) > 3:
			bad(".terms", "terms 应为 1 到 3 个按顺序出现的词")
		default:
			for _, w := range terms {
				s, ok := w.(string)
				if !ok || strings.TrimSpace(s) == "" {
					bad(".terms", "terms 的每一项都应为非空的词")
					break
				}
				pt.terms = append(pt.terms, s)
			}
		}
		if s, has := m["scope"]; has {
			switch s {
			case "sentence":
			case "paragraph":
				pt.paragraph = true
			default:
				bad(".scope", fmt.Sprintf("scope %v 应为 sentence 或 paragraph", s))
			}
		}
		if s, has := m["position"]; has {
			switch s {
			case "any", "paragraph-start", "paragraph-end":
				pt.position = s.(string)
			default:
				bad(".position", fmt.Sprintf("position %v 应为 any、paragraph-start 或 paragraph-end", s))
			}
		}
		if s, has := m["at_sentence_start"]; has {
			b, ok := s.(bool)
			if !ok {
				bad(".at_sentence_start", fmt.Sprintf("at_sentence_start %v 应为布尔值", s))
			}
			pt.startOnly = b
		}
		if s, has := m["message"]; has {
			msg, ok := s.(string)
			if !ok || strings.TrimSpace(msg) == "" {
				bad(".message", "message 应为非空字符串")
			}
			pt.message = msg
		}
		if len(probs) == n {
			out = append(out, pt)
		}
	}
	return out, probs
}

// validateDensity 检查 tone-density 参数之间的约束；bad 的 field 接在 params 之后。
func validateDensity(p params, rule *resolve.Rule, bad func(field, source, msg string)) {
	thresholds := []string{"max_connectives_per_1000", "max_four_char_per_1000", "min_sentence_stdev", "max_list_line_ratio", "min_section_length_cv"}
	_, hasTerms := p["connectives"]
	if !hasTerms && !slices.ContainsFunc(thresholds, func(name string) bool { _, ok := p[name]; return ok }) {
		bad("", rule.Type.Source, "tone-density 至少要给出一个阈值（"+strings.Join(thresholds, "、")+"），否则这条规则什么也不检查")
	}
	_, hasLimit := p["max_connectives_per_1000"]
	switch {
	case hasTerms && !hasLimit:
		bad(".max_connectives_per_1000", rule.Params["connectives"].Source, "给出 connectives 时还要给出 max_connectives_per_1000")
	case hasLimit && !hasTerms:
		bad(".connectives", rule.Params["max_connectives_per_1000"].Source, "给出 max_connectives_per_1000 时还要给出 connectives")
	}
	if v, ok := p["max_list_line_ratio"].(float64); ok && v > 1 {
		bad(".max_list_line_ratio", rule.Params["max_list_line_ratio"].Source, fmt.Sprintf("max_list_line_ratio %g 应大于 0、不超过 1", v))
	}
}

// count 返回全部词在可见文字中的出现次数之和（不截断）。
func (m *matcher) count(t *text) int {
	n := 0
	inc := func(int, int) bool { n++; return true }
	for _, v := range t.vis {
		m.scanTokens(v, inc)
		m.scanAC(v, false, inc)
	}
	return n
}

// paraText 是一个散文段落的可见文字：各行以换行连接，start[k] 为第 k 行在 seq 中的起点。
type paraText struct {
	first int
	seq   []rune
	start []int
}

func (t *text) paraText(p paragraph) paraText {
	pt := paraText{first: p.first}
	for i := p.first; i <= p.last; i++ {
		if i > p.first {
			pt.seq = append(pt.seq, '\n')
		}
		pt.start = append(pt.start, len(pt.seq))
		pt.seq = append(pt.seq, t.vis[i]...)
	}
	return pt
}

// patternBlocks 返回 formulaic-patterns 判定段首、段末与句首所用的块：散文段落中的标题行单独成块，其余连续的行成块
// （CommonMark 中标题可以紧贴正文、不隔空行）。
func (t *text) patternBlocks() []paragraph {
	var out []paragraph
	for _, p := range t.paragraphs {
		start := p.first
		for i := p.first; i <= p.last; i++ {
			if isHeading(t.vis[i]) {
				if start < i {
					out = append(out, paragraph{start, i - 1})
				}
				out = append(out, paragraph{i, i})
				start = i + 1
			}
		}
		if start <= p.last {
			out = append(out, paragraph{start, p.last})
		}
	}
	return out
}

// at 返回段落中偏移 off 处的行与列（从 1 开始）。
func (pt paraText) at(off int) (line, column int) {
	k := sort.SearchInts(pt.start, off+1) - 1
	return pt.first + k + 1, off - pt.start[k] + 1
}

func checkPatterns(t *text, p params, _ *resolve.Resolved, _ Input) ([]Finding, bool) {
	pats, _ := p["patterns"].([]pattern)
	var terms []string
	index := map[string]int{}
	for _, pt := range pats {
		for _, w := range pt.terms {
			if _, ok := index[w]; !ok {
				index[w] = len(terms)
				terms = append(terms, w)
			}
		}
	}
	m := newMatcher(terms)
	// 每个模式的词下标、text 与 message 只算一次。
	type compiled struct {
		pattern
		ks        []int
		text, msg string
	}
	cs := make([]compiled, len(pats))
	for i, pat := range pats {
		c := compiled{pattern: pat, text: strings.Join(pat.terms, "…"), msg: pat.message}
		for _, w := range pat.terms {
			c.ks = append(c.ks, index[w])
		}
		if c.msg == "" {
			c.msg = fmt.Sprintf("句式套路（Formulaic Pattern）%s：「%s」是常见的套路句式，请换成直接的说法", pat.id, c.text)
		}
		cs[i] = c
	}
	var out []Finding
	for _, para := range t.patternBlocks() {
		pt := t.paraText(para)
		// 每个词在段落中的出现位置（升序）：一次扫描找出全部词。
		occ := make([][]int, len(terms))
		for k := range pt.start {
			base := pt.start[k]
			emit := func(start, term int) bool { occ[term] = append(occ[term], base+start); return true }
			m.scanTokens(t.vis[para.first+k], emit)
			m.scanAC(t.vis[para.first+k], false, emit)
		}
		spans := SentenceSpans(pt.seq)
		if len(spans) == 0 {
			continue
		}
		sentOf := make([]int, len(pt.seq)) // 偏移所在句子的下标，不在句子中时为 -1
		for i := range sentOf {
			sentOf[i] = -1
		}
		lead := make([]int, len(spans)) // 句子第一个非空白字符的偏移
		for s, sp := range spans {
			lead[s] = -1
			for o := sp[0]; o < sp[1]; o++ {
				sentOf[o] = s
				if lead[s] < 0 && !unicode.IsSpace(pt.seq[o]) {
					lead[s] = o
				}
			}
		}
		for _, c := range cs {
			pat := c.pattern
			found := 0 // 本块中该模式的命中数：块内命中按位置升序，多于 MaxHits 个之后的不会进入结果
			match := func(lo, hi int) {
				if found > MaxHits {
					return
				}
				for _, h := range matchPattern(pat, c.ks, terms, occ, sentOf, lead, spans, lo, hi, MaxHits+1-found) {
					line, col := pt.at(h)
					out = append(out, Finding{Line: line, Column: col, Text: c.text, Message: c.msg})
					found++
				}
			}
			switch {
			case pat.paragraph:
				match(0, len(pt.seq))
			case pat.position == "paragraph-start":
				match(spans[0][0], spans[0][1])
			case pat.position == "paragraph-end":
				match(spans[len(spans)-1][0], spans[len(spans)-1][1])
			default:
				for _, sp := range spans {
					match(sp[0], sp[1])
				}
			}
		}
		// 之后各块的命中都在本块之后：已经多于 MaxHits 个时截断，不再查找（workspace-artifacts/input-limits）。
		if len(out) > MaxHits {
			sortFindings(out)
			return out[:MaxHits], true
		}
	}
	sortFindings(out)
	return out, false
}

// matchPattern 返回模式 pat 在段落偏移区间 [lo, hi) 中每次命中的第一个词的偏移：各词依次出现、互不重叠，命中后从最后一个
// 词之后继续查找，至多返回 limit 个。ks 为各词在 terms 中的下标，occ 为各词出现位置（升序），sentOf、lead 与 spans 为句子信息。
func matchPattern(pat pattern, ks []int, terms []string, occ [][]int, sentOf, lead []int, spans [][2]int, lo, hi, limit int) []int {
	length := func(j int) int { return len([]rune(terms[ks[j]])) }
	// next 返回第 j 个词在 from 及其之后、整个落在 hi 之前的第一个满足 ok 的出现；没有时为 -1。
	next := func(j, from int, ok func(o int) bool) int {
		os := occ[ks[j]]
		for i := sort.SearchInts(os, from); i < len(os) && os[i]+length(j) <= hi; i++ {
			if ok == nil || ok(os[i]) {
				return os[i]
			}
		}
		return -1
	}
	first := func(o int) bool {
		if pat.startOnly && lead[sentOf[o]] != o {
			return false
		}
		return pat.position != "paragraph-start" || sentOf[o] == 0
	}
	lastFrom := lo
	if pat.position == "paragraph-end" {
		lastFrom = spans[len(spans)-1][0]
	}
	var out []int
	for pos := lo; len(out) < limit; {
		o0 := next(0, pos, first)
		if o0 < 0 {
			return out
		}
		end := o0 + length(0)
		for j := 1; j < len(ks) && end >= 0; j++ {
			from := end
			if j == len(ks)-1 {
				from = max(from, lastFrom)
			}
			if o := next(j, from, nil); o >= 0 {
				end = o + length(j)
			} else {
				end = -1
			}
		}
		if end < 0 {
			return out
		}
		if len(ks) == 1 && pat.position == "paragraph-end" && sentOf[o0] != len(spans)-1 {
			// 只有一个词时它既是第一个也是最后一个词：要落在最后一句。
			pos = max(end, lastFrom)
			continue
		}
		out = append(out, o0)
		pos = end
	}
	return out
}

// density 是 tone-density 统计出的一项指标。
type density struct {
	name, unit string
	value      float64 // 两位小数
	limit      float64
	over       bool // true 为上限，false 为下限
	detail     string
	advice     string
}

func checkDensity(t *text, p params, _ *resolve.Resolved, _ Input) ([]Finding, bool) {
	chars := t.chars()
	if chars < minDensityChars {
		return nil, false
	}
	round2 := func(x float64) float64 { return math.Round(x*100) / 100 }
	var ds []density
	if limit, ok := p["max_connectives_per_1000"].(float64); ok {
		n := newMatcher(dedupe(p.strings("connectives"))).count(t)
		ds = append(ds, density{name: "连接词密度", unit: "每千字", value: round2(float64(n) * 1000 / float64(chars)), limit: limit, over: true,
			detail: fmt.Sprintf("%d 个连接词，共 %d 字（非空白字符数，含标点）", n, chars), advice: "删掉多余的连接词，让前后句直接相接"})
	}
	if limit, ok := p["max_four_char_per_1000"].(float64); ok {
		n := 0
		for _, v := range t.vis {
			n += FourCharRuns(string(v))
		}
		ds = append(ds, density{name: "四字片段密度", unit: "每千字", value: round2(float64(n) * 1000 / float64(chars)), limit: limit, over: true,
			detail: fmt.Sprintf("%d 个四字片段，共 %d 字（非空白字符数，含标点）", n, chars), advice: "把四字词换成具体、口语的说法"})
	}
	if limit, ok := p["min_sentence_stdev"].(float64); ok {
		var ls []int
		for _, v := range t.vis {
			if !isHeading(v) && !mdscan.ListItem.MatchString(string(v)) {
				ls = append(ls, SentenceLengths(string(v))...)
			}
		}
		if len(ls) >= minDensitySentences {
			ds = append(ds, density{name: "句长标准差", value: round2(popStdev(ls)), limit: limit,
				detail: fmt.Sprintf("%d 句", len(ls)), advice: "句子长短太均匀，读起来像念稿；长句短句交替着写"})
		}
	}
	if limit, ok := p["max_list_line_ratio"].(float64); ok {
		lines, items := 0, 0
		for _, v := range t.vis {
			if strings.TrimSpace(string(v)) == "" {
				continue
			}
			lines++
			if mdscan.ListItem.MatchString(string(v)) {
				items++
			}
		}
		ds = append(ds, density{name: "列表行占比", value: round2(float64(items) / float64(lines)), limit: limit, over: true,
			detail: fmt.Sprintf("%d / %d 行", items, lines), advice: "能连成段落的要点改写成段落"})
	}
	if limit, ok := p["min_section_length_cv"].(float64); ok {
		var heads []int
		for i, v := range t.vis {
			if sectionRE.MatchString(string(v)) {
				heads = append(heads, i)
			}
		}
		if len(heads) >= minDensitySections {
			var sizes []int
			for k, h := range heads {
				end := len(t.vis) - 1
				if k+1 < len(heads) {
					end = heads[k+1] - 1
				}
				sizes = append(sizes, t.charsOf(h+1, end))
			}
			if mean := popMean(sizes); mean > 0 {
				ds = append(ds, density{name: "节长变异系数", value: round2(popStdev(sizes) / mean), limit: limit,
					detail: fmt.Sprintf("%d 节", len(sizes)), advice: "各节长度过于整齐；按内容多少安排篇幅"})
			}
		}
	}
	var out []Finding
	for _, d := range ds {
		if d.over && d.value > d.limit {
			out = append(out, Finding{Message: fmt.Sprintf("腔调密度（Tone Density）：%s为%s %.2f（%s），超过阈值 %g；%s", d.name, d.unit, d.value, d.detail, d.limit, d.advice)})
		}
		if !d.over && d.value < d.limit {
			out = append(out, Finding{Message: fmt.Sprintf("腔调密度（Tone Density）：%s为 %.2f（%s），低于阈值 %g；%s", d.name, d.value, d.detail, d.limit, d.advice)})
		}
	}
	return out, false
}

// isHeading 报告可见文字行 v 是否为 Markdown 标题行（mdscan.Heading）。
func isHeading(v []rune) bool { return mdscan.Heading.MatchString(string(v)) }

func popMean(xs []int) float64 {
	sum := 0
	for _, x := range xs {
		sum += x
	}
	return float64(sum) / float64(len(xs))
}

// popStdev 返回总体标准差；取值全部相同时为 0（不受浮点误差影响，go-project-structure/float-equality-in-stats）。
func popStdev(xs []int) float64 {
	if slices.Min(xs) == slices.Max(xs) {
		return 0
	}
	m, v := popMean(xs), 0.0
	for _, x := range xs {
		v += (float64(x) - m) * (float64(x) - m)
	}
	return math.Sqrt(v / float64(len(xs)))
}
