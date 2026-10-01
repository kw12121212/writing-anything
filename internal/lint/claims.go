package lint

import (
	"fmt"
	"regexp"
	"slices"
	"strings"
	"unicode"

	"github.com/kw12121212/writing-anything/internal/resolve"
)

// Input 是规则引擎在正文与合并结果之外需要的输入。
type Input struct {
	Origin Origin  // 段落出处，用于判断单处豁免是否出自作者；nil 表示按作者写入处理
	Unit   string  // 单元引用，用于发现项中的修复命令
	Claims []Claim // 单元 claims.yaml 中已登记的论断
	// ClaimsInvalid 为 true 时 claims.yaml 不通过校验：其问题由调用方报告，论断相关的规则不执行。
	ClaimsInvalid bool
}

// Claim 是一个已登记的论断（Claim）；Sources 为所关联素材的 id 与类型。
type Claim struct {
	ID      string
	Kind    string
	Text    string
	Sources []ClaimSource
}

// ClaimSource 是论断关联的一份素材。
type ClaimSource struct {
	ID   string
	Type string
}

var (
	defaultClaimKinds  = []string{"experience", "opinion", "case", "data"}
	defaultSourceTypes = []string{"author-note", "interview", "transcript"}
	defaultPronouns    = []string{"我", "我们"}
	defaultMarkers     = []string{"曾", "曾经", "当时", "那时", "那次", "去年", "前年", "上个月", "上周", "第一次", "亲自", "亲眼", "遇到", "经历过", "踩过", "做过", "参与", "负责"}
	// dataRE 匹配需要出处的具体数据：数字（含全角数字）后接百分号、倍数或大数单位，以及「百分之」与中文数字加「倍」。
	dataRE = regexp.MustCompile(`[0-9０-９]+(?:[.．][0-9０-９]+)?\s*(?:%|％|倍|个百分点|万|亿)|百分之[零〇一二三四五六七八九十百千两0-9０-９]+|[零〇一二三四五六七八九十百千两]+倍`)
	// quoteRE 匹配引用块（Blockquote）行，其中的文字多为引述他人。
	quoteRE = regexp.MustCompile(`^ {0,3}>`)
)

// orDefault 返回参数 name 的取值；没有给出时返回 def。
func (p params) orDefault(name string, def []string) []string {
	if v, ok := p[name].([]string); ok {
		return v
	}
	return def
}

// sentenceEnd 是结束句子的标点；closers 是论断文字末尾可以与句末标点一起去掉的右引号与右括号。
const (
	sentenceEnd = "。！？；…"
	closers     = "”’」』）)\""
	clauseEnd   = "，、：；,;:" + sentenceEnd
)

// normalizeClaim 去掉论断文字首尾的空白、句末标点与右引号，用于在正文中查找论断。
func normalizeClaim(s string) string {
	return strings.TrimRightFunc(strings.TrimSpace(s), func(r rune) bool {
		return strings.ContainsRune(sentenceEnd+closers, r) || unicode.IsSpace(r)
	})
}

// prose 返回围栏代码块之外的原文行（围栏中的行为空），用于查找论断：论断文字可以含行内代码或链接。
func (t *text) prose() [][]rune {
	out := make([][]rune, len(t.lines))
	for i, rs := range t.lines {
		if !t.fence[i] {
			out[i] = rs
		}
	}
	return out
}

// firstOccurrences 一遍扫描 lines，返回每个词第一次出现的行与列下标（从 0 开始）；没有出现时为 -1, -1。
// 每个结束位置只取最长的词：一个词作为另一个词的后缀出现时不计，定位到它下一处单独的出现。耗时与正文长度成线性，与词数无关。
func firstOccurrences(terms []string, lines [][]rune) [][2]int {
	out := make([][2]int, len(terms))
	for k := range out {
		out[k] = [2]int{-1, -1}
	}
	m := newMatcher(terms)
	for i, v := range lines {
		for _, h := range m.spans(v) {
			if o := out[h.term]; o[0] < 0 || o[0] == i && h.column < o[1] {
				out[h.term] = [2]int{i, h.column}
			}
		}
	}
	return out
}

func checkClaimSources(t *text, p params, _ *resolve.Resolved, in Input) ([]Finding, bool) {
	if in.ClaimsInvalid {
		return nil, false
	}
	kinds, types := p.orDefault("kinds", defaultClaimKinds), p.orDefault("source_types", defaultSourceTypes)
	unit := in.Unit
	if unit == "" {
		unit = "<单元>"
	}
	var out []Finding
	var terms []string
	termOf := map[string]int{}
	var at []int // at[i] 为 out[i] 对应的词下标，论断文字为空时为 -1
	for _, c := range in.Claims {
		if !slices.Contains(kinds, c.Kind) || slices.ContainsFunc(c.Sources, func(s ClaimSource) bool { return slices.Contains(types, s.Type) }) {
			continue
		}
		fix := fmt.Sprintf("请用 wa-cli source add --type <%s> 登记作者的原话，再用 wa-cli claim set %s %s --sources <素材 id> 关联它",
			strings.Join(types, "|"), unit, c.ID)
		msg := fmt.Sprintf("论断（Claim）%s（%s）没有关联作者素材（%s 类型的素材（Source））；%s", c.ID, c.Kind, strings.Join(types, "、"), fix)
		if len(c.Sources) > 0 {
			var got []string
			for _, s := range c.Sources {
				got = append(got, s.ID+"（"+s.Type+"）")
			}
			msg = fmt.Sprintf("论断（Claim）%s（%s）只关联了 %s，不是作者素材（%s 类型的素材（Source））；%s", c.ID, c.Kind, strings.Join(got, "、"), strings.Join(types, "、"), fix)
		}
		out = append(out, Finding{Message: msg})
		k := -1
		if s := normalizeClaim(c.Text); s != "" {
			var ok bool
			if k, ok = termOf[s]; !ok {
				k = len(terms)
				termOf[s] = k
				terms = append(terms, s)
			}
		}
		at = append(at, k)
	}
	if len(terms) > 0 {
		first := firstOccurrences(terms, t.prose())
		for i, k := range at {
			if k >= 0 && first[k][0] >= 0 {
				out[i].Line, out[i].Column, out[i].Text = first[k][0]+1, first[k][1]+1, terms[k]
			}
		}
	}
	return out, false
}

// span 是一行中的一段列区间 [from, to)（列下标从 0 开始）。
type span struct{ from, to int }

func checkCandidates(t *text, p params, _ *resolve.Resolved, in Input) ([]Finding, bool) {
	if in.ClaimsInvalid {
		return nil, false
	}
	pronouns := newMatcher(p.orDefault("pronouns", defaultPronouns))
	markers := newMatcher(p.orDefault("experience_markers", defaultMarkers))
	data := p.bool("data", true)
	coverKinds := p.orDefault("cover_kinds", defaultClaimKinds)
	var claims []string
	for _, c := range in.Claims {
		if s := normalizeClaim(c.Text); s != "" && slices.Contains(coverKinds, c.Kind) {
			claims = append(claims, s)
		}
	}
	covered := newMatcher(dedupe(claims)).cover(t.prose())
	var out []Finding
	for i := range t.lines {
		if t.paragraphOf(i) < 0 {
			continue
		}
		// 标题与引用块（多为引述他人）不是亲历叙述，只检测数据。
		narrative := !headingRE.MatchString(strings.TrimLeft(string(t.lines[i]), " ")) && !quoteRE.MatchString(string(t.lines[i]))
		// done[c] 为第 i 行前 c 列中被论断覆盖的列数，用于常数时间判断一段是否整段被覆盖。
		done := make([]int, len(covered[i])+1)
		for c, ok := range covered[i] {
			done[c+1] = done[c]
			if ok {
				done[c+1]++
			}
		}
		isCovered := func(s span) bool { return done[s.to]-done[s.from] == s.to-s.from }
		for _, s := range candidates(t.vis[i], pronouns, markers, narrative, data) {
			var kinds []string
			if uncoveredClause(s.experience, isCovered) {
				kinds = append(kinds, "亲历")
			}
			if slices.ContainsFunc(s.data, func(d span) bool { return !isCovered(d) }) {
				kinds = append(kinds, "数据")
			}
			if len(kinds) == 0 {
				continue
			}
			if len(out) == MaxHits {
				return out, true
			}
			out = append(out, Finding{Line: i + 1, Column: s.from + 1, Text: strings.TrimRightFunc(string(t.lines[i][s.from:s.to]), unicode.IsSpace),
				Message: "疑似论断（Claim Candidate，" + strings.Join(kinds, "、") + "）尚未登记为论断（Claim）：请用 wa-cli claim add " + unitOr(in.Unit) +
					" --kind <类型> --text <论断> --sources <作者素材 id> 登记该论断并关联作者素材；确属泛指时可由作者单处豁免"})
		}
	}
	return out, false
}

// uncoveredClause 报告是否有某个分句的亲历区间（按结束位置分组）都没有被论断覆盖。
func uncoveredClause(exp []span, isCovered func(span) bool) bool {
	covered := map[int]bool{}
	for _, e := range exp {
		covered[e.to] = covered[e.to] || isCovered(e)
	}
	for _, ok := range covered {
		if !ok {
			return true
		}
	}
	return false
}

func unitOr(u string) string {
	if u == "" {
		return "<单元>"
	}
	return u
}

// candidate 是一个句子：[from, to) 为它在行中的列区间（从首个非空白字符到句末标点），experience 为每个经历标记词与它
// 之前最近的第一人称代词组成的区间（到标记词所在分句的末尾；同一分句的区间结束位置相同，其中任一被论断覆盖即视为该分句的
// 亲历已登记），data 为其中的数据。
type candidate struct {
	span
	experience []span
	data       []span
}

// spansOf 返回一行中 m 的词的出现区间。
func spansOf(m *matcher, v []rune) []span {
	var out []span
	for _, h := range m.spans(v) {
		out = append(out, span{h.column, h.column + len(m.words[h.term])})
	}
	return out
}

// candidates 把一行可见文字 v 在句末标点处切分为句子，返回其中的亲历候选（experience 为 false 时不检测）与数据候选。
// 代词、标记词与数据各扫描一遍，耗时与行长成线性（排序除外）。
func candidates(v []rune, pronouns, markers *matcher, experience, data bool) []candidate {
	var sents []candidate
	of := make([]int, len(v)) // of[c] 为第 c 列所在句子的下标，不在句子中时为 -1
	start := -1
	for c, r := range v {
		of[c] = -1
		if start < 0 && !unicode.IsSpace(r) {
			start = c
		}
		if start >= 0 && (strings.ContainsRune(sentenceEnd, r) || c == len(v)-1) {
			sents = append(sents, candidate{span: span{start, c + 1}})
			for k := start; k <= c; k++ {
				of[k] = len(sents) - 1
			}
			start = -1
		}
	}
	// in 返回区间 [from, to) 所在的句子下标；跨句或不在句子中时为 -1。
	in := func(from, to int) int {
		if from < 0 || to > len(v) || from >= to || of[from] != of[to-1] {
			return -1
		}
		return of[from]
	}
	// 每个标记词与它之前最近的、在同一句中结束于它之前的代词组成一个亲历区间：从代词到标记词所在分句的末尾（下一个
	// 分句或句末标点之前）。代词按结束位置排序、标记词按起点排序后一遍扫过：已扫过的代词中起点最大者就是最近的代词。
	stop := make([]int, len(v)+1) // stop[c] 为第 c 列及其之后第一个分句或句末标点的列下标，没有时为行长
	stop[len(v)] = len(v)
	for c := len(v) - 1; c >= 0; c-- {
		if stop[c] = stop[c+1]; strings.ContainsRune(clauseEnd, v[c]) {
			stop[c] = c
		}
	}
	prons := spansOf(pronouns, v)
	slices.SortFunc(prons, func(a, b span) int { return a.to - b.to })
	marks := spansOf(markers, v)
	slices.SortFunc(marks, func(a, b span) int { return a.from - b.from })
	nearest, j := -1, 0
	for _, m := range marks {
		for ; j < len(prons) && prons[j].to <= m.from; j++ {
			if nearest < 0 || prons[j].from > prons[nearest].from {
				nearest = j
			}
		}
		if nearest < 0 {
			continue
		}
		end := stop[m.to]
		for end > m.to && unicode.IsSpace(v[end-1]) {
			end--
		}
		if k := in(prons[nearest].from, end); k >= 0 && experience {
			sents[k].experience = append(sents[k].experience, span{prons[nearest].from, end})
		}
	}
	if data {
		line := string(v)
		col := make([]int, len(line)+1) // col[b] 为字符起点 b（字节下标）处的列下标；匹配的起止都是字符起点
		c := 0
		for b := range line {
			col[b] = c
			c++
		}
		col[len(line)] = c
		for _, m := range dataRE.FindAllStringIndex(line, -1) {
			s := span{col[m[0]], col[m[1]]}
			if k := in(s.from, s.to); k >= 0 {
				sents[k].data = append(sents[k].data, s)
			}
		}
	}
	var out []candidate
	for _, s := range sents {
		if len(s.experience) > 0 || len(s.data) > 0 {
			out = append(out, s)
		}
	}
	return out
}
