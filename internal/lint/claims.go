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
	// Conflicts 是关联本作品且仍 open 的冲突（Conflict）决策：任一方引文出现在正文时 conflict-sources 规则报告 error。
	Conflicts []Conflict
	// POVPerson 是写作简报（本单元或继承的祖先单元）pov 的人称（first、second 或 third）；空表示写作简报不可用
	// 或没有 pov，pov-consistency 规则不执行。
	POVPerson string
}

// Claim 是一个已登记的论断（Claim）；Sources 为所关联素材的 id 与类型。
type Claim struct {
	ID      string
	Kind    string
	Text    string
	Sources []ClaimSource
}

// Conflict 是一个仍待作者裁定的素材矛盾（冲突（Conflict）决策的结构化载荷）。
type Conflict struct {
	ID         string // 决策 id，用于发现项 message 提示 decide
	About      string
	A, B       string // 两说的引文
	SrcA, SrcB string // 两说出自的素材 id
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

// ClaimSourceRequirement 返回一条生效的 claim-sources 规则对论断与素材类型的要求：kinds 为需要作者亲历素材的论断
// 类型，sourceTypes 为算作亲历的素材类型。参数缺省时用默认值；显式空数组生效（与 lint 的取参一致：空 kinds 表示
// 没有论断类型需要亲历素材，空 source_types 表示任何素材都不算亲历）；参数取值不是字符串数组时 ok 为 false——
// lint 对参数非法的规则报告 rule.invalid-params 且不执行，写入校验同样跳过。命令层的写入校验与 checkClaimSources
// 经它共用同一张规则表，规范包按规则参数覆盖后两层同判。
func ClaimSourceRequirement(rule *resolve.Rule) (kinds, sourceTypes []string, ok bool) {
	kinds, sourceTypes = defaultClaimKinds, defaultSourceTypes
	if rule == nil {
		return kinds, sourceTypes, true
	}
	if v, has := rule.Params["kinds"]; has {
		list, valid := stringListParam(v.Value)
		if !valid {
			return nil, nil, false
		}
		kinds = list
	}
	if v, has := rule.Params["source_types"]; has {
		list, valid := stringListParam(v.Value)
		if !valid {
			return nil, nil, false
		}
		sourceTypes = list
	}
	return kinds, sourceTypes, true
}

// stringListParam 把规则参数取值转为字符串数组；类型不符时报告不可用。
func stringListParam(v any) ([]string, bool) {
	switch t := v.(type) {
	case []string:
		return t, true
	case []any:
		out := make([]string, 0, len(t))
		for _, x := range t {
			s, ok := x.(string)
			if !ok {
				return nil, false
			}
			out = append(out, s)
		}
		return out, true
	}
	return nil, false
}

// LacksAuthorSource 报告 kind 是否在 kinds 中且 srcTypes 里没有一个在 sourceTypes 中（含无素材）——claim-sources
// 规则的判定。命令层写入校验用它时须另要求素材非空：未关联素材是显式未完成（由 claim set 补齐），只关联非亲历
// 素材才是看起来完整的误分类。
func LacksAuthorSource(kind string, srcTypes []string, kinds, sourceTypes []string) bool {
	return slices.Contains(kinds, kind) && !slices.ContainsFunc(srcTypes, func(t string) bool { return slices.Contains(sourceTypes, t) })
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
		srcTypes := make([]string, 0, len(c.Sources))
		for _, s := range c.Sources {
			srcTypes = append(srcTypes, s.Type)
		}
		if !LacksAuthorSource(c.Kind, srcTypes, kinds, types) {
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

// claimNote 是一条候选覆盖说明的素材：论断文字与未覆盖候选区间重叠时，按未参与覆盖的原因在发现项 message 末尾说明。
type claimNote struct {
	id, kind, text string
	kindExcluded   bool // 类型不在 cover_kinds 中
	factNoSource   bool // kind 为 fact 且未关联素材
}

func checkCandidates(t *text, p params, _ *resolve.Resolved, in Input) ([]Finding, bool) {
	if in.ClaimsInvalid {
		return nil, false
	}
	pronouns := newMatcher(p.orDefault("pronouns", defaultPronouns))
	markers := newMatcher(p.orDefault("experience_markers", defaultMarkers))
	data := p.bool("data", true)
	coverKinds := p.orDefault("cover_kinds", defaultClaimKinds)
	_, coverGiven := p["cover_kinds"].([]string)
	coverList := strings.Join(coverKinds, "、")
	if !coverGiven {
		coverList = "默认 " + coverList
	}
	var claims, facts, noteTexts []string
	var notes []claimNote
	for _, c := range in.Claims {
		s := normalizeClaim(c.Text)
		if s == "" {
			continue
		}
		n := claimNote{id: c.ID, kind: c.Kind, text: s,
			kindExcluded: !slices.Contains(coverKinds, c.Kind), factNoSource: c.Kind == "fact" && len(c.Sources) == 0}
		if !n.kindExcluded {
			claims = append(claims, s)
		}
		// 关联了素材的 fact 论断是外部数据的正当登记，额外参与数据候选的覆盖；亲历候选仍只认 cover_kinds：fact 不在
		// claim-sources 的 kinds 中、没有作者素材的强制，未关联素材时不能以此消除数据提醒。
		if c.Kind == "fact" && len(c.Sources) > 0 {
			facts = append(facts, s)
		}
		if n.kindExcluded || n.factNoSource {
			notes = append(notes, n)
			noteTexts = append(noteTexts, s)
		}
	}
	proseLines := t.prose()
	covered := newMatcher(dedupe(claims)).cover(proseLines)
	var factCovered [][]bool
	if len(facts) > 0 {
		factCovered = newMatcher(dedupe(facts)).cover(proseLines)
	}
	// 未参与覆盖的论断的定位：一遍多词匹配，出现区间按行惰性计算，只在产生发现项的行上取（workspace-artifacts/input-limits）。
	var noteMatcher *matcher
	noteSpans := map[int]map[string][]span{}
	if len(notes) > 0 {
		noteTexts = dedupe(noteTexts)
		noteMatcher = newMatcher(noteTexts)
	}
	lineNotes := func(i int) map[string][]span {
		if noteMatcher == nil {
			return nil
		}
		if v, ok := noteSpans[i]; ok {
			return v
		}
		v := map[string][]span{}
		for _, h := range noteMatcher.spans(t.vis[i]) {
			text := noteTexts[h.term]
			v[text] = append(v[text], span{h.column, h.column + len(noteMatcher.words[h.term])})
		}
		noteSpans[i] = v
		return v
	}
	var out []Finding
	for i := range t.lines {
		if t.paragraphOf(i) < 0 {
			continue
		}
		// 标题行不产生任何候选：标题是电报式标签而非散文论断，其中的数字应出现在正文句子中登记（引用块行只检测数据）。
		if headingRE.MatchString(strings.TrimLeft(string(t.lines[i]), " ")) {
			continue
		}
		narrative := !quoteRE.MatchString(string(t.lines[i]))
		// done[c] 与 doneData[c] 为第 i 行前 c 列中被覆盖的列数，用于常数时间判断一段是否整段被覆盖：亲历只看 cover_kinds
		// 论断的覆盖，数据候选另被关联了素材的 fact 论断覆盖。
		done := make([]int, len(covered[i])+1)
		doneData := done
		if factCovered != nil {
			doneData = make([]int, len(covered[i])+1)
		}
		for c := 0; c < len(covered[i]); c++ {
			done[c+1] = done[c]
			if covered[i][c] {
				done[c+1]++
			}
			if factCovered != nil {
				doneData[c+1] = doneData[c]
				if covered[i][c] || factCovered[i][c] {
					doneData[c+1]++
				}
			}
		}
		isCovered := func(s span) bool { return done[s.to]-done[s.from] == s.to-s.from }
		isCoveredData := func(s span) bool { return doneData[s.to]-doneData[s.from] == s.to-s.from }
		for _, s := range candidates(t.vis[i], pronouns, markers, narrative, data) {
			// 亲历按分句分组（同一分句的亲历区间中任一被覆盖即视为该分句已登记），未登记的分句取从最早代词到分句末尾的并集。
			fromOf, registered := map[int]int{}, map[int]bool{}
			for _, e := range s.experience {
				if f, ok := fromOf[e.to]; !ok || e.from < f {
					fromOf[e.to] = e.from
				}
				registered[e.to] = registered[e.to] || isCovered(e)
			}
			var expOpen, dataOpen []span
			for end, from := range fromOf {
				if !registered[end] {
					expOpen = append(expOpen, span{from, end})
				}
			}
			for _, d := range s.data {
				if !isCoveredData(d) {
					dataOpen = append(dataOpen, d)
				}
			}
			var kinds []string
			if len(expOpen) > 0 {
				kinds = append(kinds, "亲历")
			}
			if len(dataOpen) > 0 {
				kinds = append(kinds, "数据")
			}
			if len(kinds) == 0 {
				continue
			}
			if len(out) == MaxHits {
				return out, true
			}
			msg := "疑似论断（Claim Candidate，" + strings.Join(kinds, "、") + "）尚未登记为论断（Claim）：请用 wa-cli claim add " + unitOr(in.Unit) +
				" --kind <类型> --text <论断> --sources <作者素材 id> 登记该论断并关联作者素材；确属泛指时可由作者单处豁免"
			if ns := exclusionNotes(notes, lineNotes(i), expOpen, dataOpen, coverList); len(ns) > 0 {
				msg += "；" + strings.Join(ns, "；")
			}
			out = append(out, Finding{Line: i + 1, Column: s.from + 1, Text: strings.TrimRightFunc(string(t.lines[i][s.from:s.to]), unicode.IsSpace),
				Message: msg})
		}
	}
	return out, false
}

// checkConflicts 是 conflict-sources 规则：open 冲突（Conflict）决策的任一方引文（规范化后）出现在正文时报告
// error 发现项；decided 或豁免的冲突由调用方不传入。匹配复用论断文字的规范化与一遍多词匹配。
func checkConflicts(t *text, _ params, _ *resolve.Resolved, in Input) ([]Finding, bool) {
	type side struct {
		decision, about, quote, source, other, otherSource string
	}
	var sides []side
	for _, c := range in.Conflicts {
		sides = append(sides,
			side{decision: c.ID, about: c.About, quote: normalizeClaim(c.A), source: c.SrcA, other: c.B, otherSource: c.SrcB},
			side{decision: c.ID, about: c.About, quote: normalizeClaim(c.B), source: c.SrcB, other: c.A, otherSource: c.SrcA})
	}
	var out []Finding
	for _, s := range sides {
		if s.quote == "" {
			continue
		}
		m := newMatcher([]string{s.quote})
		found, at := false, [2]int{0, 0}
		for i, v := range t.prose() {
			for _, h := range m.spans(v) {
				found, at = true, [2]int{i, h.column}
				break
			}
			if found {
				break
			}
		}
		if !found {
			continue
		}
		out = append(out, Finding{Line: at[0] + 1, Column: at[1] + 1, Text: s.quote,
			Message: fmt.Sprintf("正文引用了未决冲突（Conflict）「%s」中 %s 的说法（%s），另一说为 %s（%s）；请作者用 wa-cli decide %s 裁定后重跑检查",
				s.about, s.source, s.quote, s.other, s.otherSource, s.decision)})
	}
	return out, false
}

// exclusionNotes 返回要追加到发现项 message 末尾的论断说明：文字与未覆盖候选区间重叠、却未参与该候选覆盖的论断，按
// claims.yaml 中的顺序各说明原因（coverList 为 cover_kinds 的实际取值，为默认时带「默认」前缀）。
func exclusionNotes(notes []claimNote, spans map[string][]span, exp, dat []span, coverList string) []string {
	var out []string
	for _, n := range notes {
		var hitExp, hitData bool
		for _, occ := range spans[n.text] {
			overlaps := func(v span) bool { return occ.from < v.to && v.from < occ.to }
			for _, v := range exp {
				hitExp = hitExp || overlaps(v)
			}
			for _, v := range dat {
				hitData = hitData || overlaps(v)
			}
		}
		// fact 的数据区间未覆盖不由类型解释：关联了素材的 fact 本就参与数据候选的覆盖，未覆盖的原因是没有素材。
		if n.kindExcluded && (hitExp || hitData && n.kind != "fact") {
			out = append(out, fmt.Sprintf("论断 %s（%s）的类型不在 claim-candidates 的 cover_kinds（%s）内，未参与覆盖", n.id, n.kind, coverList))
		}
		if n.factNoSource && hitData {
			out = append(out, fmt.Sprintf("论断 %s（fact）未关联素材，不能覆盖数据候选", n.id))
		}
	}
	return out
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
