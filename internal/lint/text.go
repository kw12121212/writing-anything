package lint

import (
	"regexp"
	"strings"
	"unicode"
)

// MaxBytes 是文本检查接受的正文字节数上限；超过时报告 lint.too-large（workspace-artifacts/input-limits）。
const MaxBytes = 2 << 20

// text 是规范化后的正文：按行切分，屏蔽区域（Masked Region）中的字符在 vis 中替换为空格，并切出散文段落。
// 全部结构在 prepare 中一遍算出，之后的检查只读取它们，扫描次数不随规则或词条数增长。
type text struct {
	lines      [][]rune
	vis        [][]rune // 可见文字：屏蔽区域中的字符替换为空格，列位置不变
	fence      []bool   // fence[i] 为 true 时第 i 行属于围栏代码块
	paragraphs []paragraph
	para       []int      // para[i] 为第 i 行所在散文段落的下标，不在散文段落中时为 -1
	comments   []comment  // 已闭合的 HTML 注释，用于单处豁免
	fences     []fence    // 围栏代码块的开头行
	commentAt  [][][2]int // commentAt[i] 为第 i 行中属于已闭合 HTML 注释的列区间 [起, 止)
	spanCache  [][][2]int // 每行行内代码的列区间，首次需要时计算（codeSpansOf）
}

// fence 是一个围栏代码块的开头行：line 为行下标（从 0 开始），info 为围栏之后的信息字符串（去掉首尾空白，通常是语言）。
type fence struct {
	line int
	info string
}

// paragraph 是一个散文段落：由空行分隔、含可见文字的连续行；first 与 last 为首行与末行的下标（从 0 开始）。
type paragraph struct {
	first, last int
}

// comment 是一个已闭合的 HTML 注释：body 为 <!-- 与 --> 之间的文字，first 与 last 为开始行与结束行的下标。
type comment struct {
	body        string
	first, last int
}

// urlStops 是结束 URL 的全角标点。
const urlStops = "，。；：、！？）」』】》“”‘’…—"

var (
	backtickFence = regexp.MustCompile("^ {0,3}(`{3,})[^`]*$")
	tildeFence    = regexp.MustCompile(`^ {0,3}(~{3,})`)
	headingRE     = regexp.MustCompile(`^#{1,6}(\s|$)`)
)

// prepare 规范正文（去掉 BOM，CRLF 与单独的 CR 换成 LF）并切成行，标出屏蔽区域：围栏代码块、行内代码、已闭合的 HTML
// 注释、URL 与 Markdown 链接地址，再切出散文段落。每一步都与正文长度成线性关系。
func prepare(content string) *text {
	content = strings.TrimPrefix(content, "\uFEFF")
	content = strings.ReplaceAll(strings.ReplaceAll(content, "\r\n", "\n"), "\r", "\n")
	t := &text{}
	for l := range strings.SplitSeq(content, "\n") {
		t.lines = append(t.lines, []rune(l))
	}
	t.fence = make([]bool, len(t.lines))
	t.vis = make([][]rune, len(t.lines))
	for i, rs := range t.lines {
		t.vis[i] = append([]rune(nil), rs...)
	}
	t.maskFences()
	t.maskInline()
	t.split()
	return t
}

// maskFences 标出围栏代码块：开头为至少三个 ` 或 ~ 的行（反引号围栏的信息字符串不含 `），到同一字符、长度不短于开头、
// 其后只有空白的结束行；未闭合时到正文末尾。
func (t *text) maskFences() {
	open, char := 0, byte(0)
	for i, rs := range t.lines {
		line := string(rs)
		if open > 0 {
			t.fence[i] = true
			blank(t.vis[i], 0, len(rs))
			trim := strings.TrimLeft(line, " ")
			n := 0
			for n < len(trim) && trim[n] == char {
				n++
			}
			if n >= open && strings.TrimSpace(trim[n:]) == "" {
				open = 0
			}
			continue
		}
		m := backtickFence.FindStringSubmatch(line)
		if m == nil {
			m = tildeFence.FindStringSubmatch(line)
		}
		if m != nil {
			open, char = len(m[1]), m[1][0]
			info := strings.TrimSpace(strings.TrimLeft(strings.TrimLeft(line, " "), m[1][:1]))
			t.fences = append(t.fences, fence{line: i, info: info})
			t.fence[i] = true
			blank(t.vis[i], 0, len(rs))
		}
	}
}

// maskInline 标出围栏之外的行内代码、HTML 注释、URL 与链接地址。行首的 <!--（HTML 块）可以跨越空行，到其后第一个 -->；
// 不在行首的 <!--（行内 HTML）不能跨越空行；没有对应 --> 的 <!-- 不是注释，按普通文字检查。
func (t *text) maskInline() {
	// 每行最后一个 --> 的列、每行及其之后第一个含 --> 的行、每行及其之后第一个空行，都一次预先算好。
	n := len(t.lines)
	lastClose := make([]int, n)
	nextCloseLine, nextBlank := make([]int, n+1), make([]int, n+1)
	nextCloseLine[n], nextBlank[n] = -1, -1
	for i := n - 1; i >= 0; i-- {
		lastClose[i] = -1
		if !t.fence[i] {
			for j := len(t.lines[i]) - 3; j >= 0; j-- {
				if hasAt(t.lines[i], j, "-->") {
					lastClose[i] = j
					break
				}
			}
		}
		nextCloseLine[i], nextBlank[i] = nextCloseLine[i+1], nextBlank[i+1]
		if lastClose[i] >= 0 {
			nextCloseLine[i] = i
		}
		if strings.TrimSpace(string(t.lines[i])) == "" {
			nextBlank[i] = i
		}
	}
	closes := func(i, j int, leading bool) bool {
		if lastClose[i] >= j+4 {
			return true
		}
		c := nextCloseLine[i+1]
		return c >= 0 && (leading || nextBlank[i+1] < 0 || nextBlank[i+1] > c)
	}
	inComment := false
	var body strings.Builder
	start, commentStart := 0, 0 // 注释的开始行与它在当前行中的开始列
	for i, rs := range t.lines {
		if t.fence[i] {
			continue
		}
		firstText := 0 // 行首第一个非空白字符的列：<!-- 就在这里时为 HTML 块
		for firstText < len(rs) && unicode.IsSpace(rs[firstText]) {
			firstText++
		}
		nextTick := nextIndex(rs, '`')
		nextParen := nextIndex(rs, ')')
		for j := 0; j < len(rs); j++ {
			switch {
			case inComment:
				if hasAt(rs, j, "-->") {
					blank(t.vis[i], j, j+3)
					t.markComment(i, commentStart, j+3)
					j += 2
					inComment = false
					t.comments = append(t.comments, comment{body: body.String(), first: start, last: i})
					body.Reset()
					continue
				}
				t.vis[i][j] = ' '
				body.WriteRune(rs[j])
			case hasAt(rs, j, "<!--") && closes(i, j, j <= firstText):
				inComment, start, commentStart = true, i, j
				blank(t.vis[i], j, j+4)
				j += 3
			case rs[j] == '`':
				// 行内代码：n 个反引号到下一个恰好 n 个反引号为止；没有对应的结束时按普通文字处理。
				n := runLen(rs, j, '`')
				end := closingRun(rs, j+n, n, nextTick)
				if end < 0 {
					j += n - 1
					continue
				}
				blank(t.vis[i], j, end+n)
				j = end + n - 1
			case hasAt(rs, j, "http://") || hasAt(rs, j, "https://"):
				// URL 到空白或全角标点（如紧随其后的「，」「。」）为止；路径中的汉字仍属于 URL。
				end := j
				for end < len(rs) && !unicode.IsSpace(rs[end]) && !strings.ContainsRune(urlStops, rs[end]) {
					end++
				}
				blank(t.vis[i], j, end)
				j = end - 1
			case hasAt(rs, j, "]("):
				end := nextParen[j+2]
				if end < 0 {
					continue
				}
				blank(t.vis[i], j+2, end)
				j = end
			}
		}
		if inComment {
			body.WriteRune('\n')
			t.markComment(i, commentStart, len(rs))
			commentStart = 0
		}
	}
}

// markComment 记录第 i 行中属于 HTML 注释的列区间 [from, to)。
func (t *text) markComment(i, from, to int) {
	if t.commentAt == nil {
		t.commentAt = make([][][2]int, len(t.lines))
	}
	t.commentAt[i] = append(t.commentAt[i], [2]int{from, to})
}

// inComment 报告第 i 行第 c 列是否在已闭合的 HTML 注释中。
func (t *text) inComment(i, c int) bool {
	return t.commentAt != nil && inSpans(t.commentAt[i], c)
}

// codeSpansOf 返回第 i 行行内代码的列区间；每行只计算一次，各规则共用。
func (t *text) codeSpansOf(i int) [][2]int {
	if t.spanCache == nil {
		t.spanCache = make([][][2]int, len(t.lines))
	}
	if t.spanCache[i] == nil {
		t.spanCache[i] = append([][2]int{}, codeSpans(t.lines[i])...)
	}
	return t.spanCache[i]
}

// nextIndex 返回每个位置及其之后第一个 r 的下标，没有时为 -1；一次从右向左算出，查找为常数时间。
func nextIndex(rs []rune, r rune) []int {
	out := make([]int, len(rs)+1)
	out[len(rs)] = -1
	for k := len(rs) - 1; k >= 0; k-- {
		if rs[k] == r {
			out[k] = k
		} else {
			out[k] = out[k+1]
		}
	}
	return out
}

// runLen 返回从 j 开始连续的 r 的个数。
func runLen(rs []rune, j int, r rune) int {
	n := 0
	for j+n < len(rs) && rs[j+n] == r {
		n++
	}
	return n
}

// closingRun 返回 from 及其之后第一个恰好 n 个反引号的连续段的起点；没有时为 -1。next 为 nextIndex(rs, '`')。
func closingRun(rs []rune, from, n int, next []int) int {
	for k := next[min(from, len(rs))]; k >= 0; {
		m := runLen(rs, k, '`')
		if m == n {
			return k
		}
		k = next[min(k+m, len(rs))]
	}
	return -1
}

// split 切出散文段落：由空行或围栏代码块分隔、含可见文字的连续行；块首尾只含屏蔽区域（如单独的注释）的行不属于散文段落，
// 整块只含屏蔽区域时不成段。
func (t *text) split() {
	t.para = make([]int, len(t.lines))
	for i := range t.para {
		t.para[i] = -1
	}
	visible := func(i int) bool { return strings.TrimSpace(string(t.vis[i])) != "" }
	start := -1
	flush := func(end int) {
		if start < 0 {
			return
		}
		first, last := start, end
		for first <= last && !visible(first) {
			first++
		}
		for last >= first && !visible(last) {
			last--
		}
		if first <= last {
			for i := first; i <= last; i++ {
				t.para[i] = len(t.paragraphs)
			}
			t.paragraphs = append(t.paragraphs, paragraph{first, last})
		}
		start = -1
	}
	for i := range t.lines {
		if strings.TrimSpace(string(t.lines[i])) == "" || t.fence[i] {
			flush(i - 1)
			continue
		}
		if start < 0 {
			start = i
		}
	}
	flush(len(t.lines) - 1)
}

// paragraphOf 返回第 i 行所在散文段落的下标；不在任何散文段落中时为 -1。
func (t *text) paragraphOf(i int) int {
	if i < 0 || i >= len(t.para) {
		return -1
	}
	return t.para[i]
}

// nextParagraph 返回首行在第 i 行之后的第一个散文段落的下标；没有时为 -1。段落按首行排序，用二分查找。
func (t *text) nextParagraph(i int) int {
	lo, hi := 0, len(t.paragraphs)
	for lo < hi {
		mid := (lo + hi) / 2
		if t.paragraphs[mid].first > i {
			hi = mid
		} else {
			lo = mid + 1
		}
	}
	if lo == len(t.paragraphs) {
		return -1
	}
	return lo
}

// charsOf 返回第 first 到 last 行中可见的、去掉 Markdown 标题标记后的非空白字符数（含标点）。
func (t *text) charsOf(first, last int) int {
	return t.countOf(first, last, func(r rune) bool { return !unicode.IsSpace(r) })
}

// cjkCharsOf 返回第 first 到 last 行中可见的、去掉 Markdown 标题标记后的汉字数（Unicode Han；
// 全角标点与假名等不计入）。与 charsOf 共用同一屏蔽与去标题处理，两个口径不会漂移。
func (t *text) cjkCharsOf(first, last int) int {
	return t.countOf(first, last, func(r rune) bool { return unicode.Is(unicode.Han, r) })
}

// countOf 返回第 first 到 last 行中可见的、去掉 Markdown 标题标记后满足 keep 的字符个数。
func (t *text) countOf(first, last int, keep func(rune) bool) int {
	n := 0
	for i := first; i <= last; i++ {
		v := t.vis[i]
		k := 0
		for k < len(v) && v[k] == ' ' {
			k++
		}
		if m := headingRE.FindString(string(v[k:])); m != "" {
			k += len([]rune(m))
		}
		for _, r := range v[k:] {
			if keep(r) {
				n++
			}
		}
	}
	return n
}

// chars 返回正文字数（非空白字符数，含标点）。
func (t *text) chars() int { return t.charsOf(0, len(t.lines)-1) }

// cjkChars 返回正文汉字数。
func (t *text) cjkChars() int { return t.cjkCharsOf(0, len(t.lines)-1) }

func blank(v []rune, from, to int) {
	for k := from; k < to && k < len(v); k++ {
		v[k] = ' '
	}
}

// hasAt 报告 rs 从下标 j 开始是否为 ASCII 标记 s。
func hasAt(rs []rune, j int, s string) bool {
	if j < 0 || j+len(s) > len(rs) {
		return false
	}
	for k := 0; k < len(s); k++ {
		if rs[j+k] != rune(s[k]) {
			return false
		}
	}
	return true
}

// runesAt 报告 rs 从下标 j 开始是否为 p。
func runesAt(rs []rune, j int, p []rune) bool {
	if j+len(p) > len(rs) {
		return false
	}
	for k, r := range p {
		if rs[j+k] != r {
			return false
		}
	}
	return true
}
