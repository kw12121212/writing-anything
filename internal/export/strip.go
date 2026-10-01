package export

import (
	"regexp"
	"sort"
	"strings"

	"github.com/kw12121212/writing-anything/internal/mdscan"
)

// 写作过程的注释：作者批注的开头与 feedback 的批注一致（<!-- 之后的 @me: 或 @me(kind):），单处豁免的内容与文本检查一致
// （lint-ignore <规则 id>: 理由）。围栏代码块与行内代码的识别见 mdscan。
var (
	annotationHead = regexp.MustCompile(`^[ \t]*\n?[ \t]*@me(?:\((must-fix|suggestion|question|praise)\))?[ \t]*:`)
	waiverBody     = regexp.MustCompile(`^\s*lint-ignore\s+[a-z][a-z0-9]*(?:-[a-z0-9]+)*\s*[:：]\s*\S`)
	blankLineRE    = regexp.MustCompile(`\n[ \t\r]*\n`)
	h1RE           = regexp.MustCompile(`^ {0,3}# +(.*?)(?:\s+#+)?\s*$`)
)

// positions 返回 sub 在 s 中每一处（不重叠）出现的字节偏移，按升序。
func positions(s, sub string) []int {
	var out []int
	for off := 0; ; {
		i := strings.Index(s[off:], sub)
		if i < 0 {
			return out
		}
		out = append(out, off+i)
		off += i + len(sub)
	}
}

// firstAtOrAfter 返回升序数组 ps 中第一个不小于 x 的元素；没有时为 -1。
func firstAtOrAfter(ps []int, x int) int {
	if i := sort.SearchInts(ps, x); i < len(ps) {
		return ps[i]
	}
	return -1
}

// StripComments 去掉一个单元正文中写作过程的注释：作者批注 <!-- @me…: … -->（可以跨行，但不能跨空行）与单处豁免
// <!-- lint-ignore <规则 id>: 理由 -->。围栏代码块、行内代码与其他 HTML 注释原样保留；没有对应 -->、包含另一个 <!-- 或跨空行的
// 不是注释，原样保留。去掉注释后只剩空白的行整行去掉，不因此留下连续的空行。开头的 BOM 去掉，CRLF 换成 LF。
// 各种位置都一次预先算好，每个 <!-- 只做对数时间的查找，耗时与正文长度成线性（workspace-artifacts/input-limits）。
func StripComments(content []byte) []byte {
	s := strings.ReplaceAll(strings.TrimPrefix(string(content), "\uFEFF"), "\r\n", "\n")
	lines := strings.SplitAfter(s, "\n")
	fenced := mdscan.FenceLines(lines)
	starts := make([]int, len(lines)+1)
	for i, l := range lines {
		starts[i+1] = starts[i] + len(l)
	}
	lineOf := func(off int) int { return sort.SearchInts(starts, off+1) - 1 }
	spans := make([][][2]int, len(lines))
	spanDone := make([]bool, len(lines))
	inCode := func(off int) bool {
		i := lineOf(off)
		if fenced[i] {
			return true
		}
		if !spanDone[i] {
			spans[i], spanDone[i] = mdscan.CodeSpans(lines[i]), true
		}
		return mdscan.InSpans(spans[i], off-starts[i])
	}
	opens, closes := positions(s, "<!--"), positions(s, "-->")
	var blanks []int // 每个空行之前那个换行的偏移
	for _, m := range blankLineRE.FindAllStringIndex(s, -1) {
		blanks = append(blanks, m[0])
	}
	var cuts [][2]int // 要去掉的注释的字节区间，按升序、互不重叠
	after := 0        // 已去掉的最后一个注释的结束位置
	for _, start := range opens {
		if start < after || inCode(start) {
			continue
		}
		end := firstAtOrAfter(closes, start+len("<!--"))
		if end < 0 {
			break // 之后再没有 -->：都不是注释
		}
		stop := end + len("-->")
		endInCode := inCode(end) // 文本检查进入注释后不再识别反引号：只有作者批注要求结尾不在代码中
		next := firstAtOrAfter(opens, start+1)
		blank := firstAtOrAfter(blanks, start)
		nested, crosses := next >= 0 && next < end, blank >= 0 && blank < end
		body := s[start+len("<!--") : end]
		// 作者批注与作者反馈的批注一致：不能包含另一个 <!--、不能跨空行。单处豁免与文本检查一致：注释到其后第一个 -->，
		// 行首的注释（HTML 块）可以跨空行，不在行首的不能。
		line := lineOf(start)
		leading := strings.TrimSpace(s[starts[line]:start]) == ""
		annotation := !nested && !crosses && !endInCode && func() bool {
			m := annotationHead.FindStringIndex(body)
			return m != nil && strings.TrimSpace(body[m[1]:]) != ""
		}()
		waiver := (leading || !crosses) && waiverBody.MatchString(body)
		if annotation || waiver {
			cuts = append(cuts, [2]int{start, stop})
			after = stop
		}
	}
	if len(cuts) == 0 {
		return []byte(s)
	}
	// 按行重建：含被去掉注释的行去掉注释后只剩空白时整行去掉；去掉的行前后都是空行时同时去掉后一个空行。
	var out []string
	k, skipBlank := 0, false
	for i, line := range lines {
		if skipBlank {
			skipBlank = false
			if strings.TrimSpace(line) == "" {
				continue
			}
		}
		for k < len(cuts) && cuts[k][1] <= starts[i] {
			k++
		}
		if k == len(cuts) || cuts[k][0] >= starts[i+1] {
			out = append(out, line)
			continue
		}
		var b strings.Builder
		pos := starts[i]
		for j := k; j < len(cuts) && cuts[j][0] < starts[i+1]; j++ {
			if from := max(cuts[j][0], starts[i]); from > pos {
				b.WriteString(s[pos:from])
			}
			pos = max(pos, min(cuts[j][1], starts[i+1]))
		}
		tail := s[pos:starts[i+1]]
		rest := b.String()
		if strings.TrimSpace(rest+tail) != "" {
			// 注释一直延伸到行尾（之后只剩空白与换行）时，去掉注释之前留下的行尾空白；否则之后的文字与空白（如 Markdown 的
			// 行尾两个空格硬换行）原样保留。
			if strings.TrimSpace(tail) == "" {
				rest = strings.TrimRight(rest, " \t")
				if strings.HasSuffix(line, "\n") {
					rest += "\n"
				}
			} else {
				rest += tail
			}
			out = append(out, rest)
			continue
		}
		if n := len(out); n == 0 || strings.TrimSpace(out[n-1]) == "" {
			skipBlank = true
		}
	}
	return []byte(strings.Join(out, ""))
}

// FirstHeading 返回正文中围栏代码块之外的第一个一级标题的文字；没有时为空。
func FirstHeading(content string) string {
	lines := strings.Split(strings.ReplaceAll(content, "\r\n", "\n"), "\n")
	fenced := mdscan.FenceLines(lines)
	for i, l := range lines {
		if fenced[i] {
			continue
		}
		if m := h1RE.FindStringSubmatch(l); m != nil && strings.TrimSpace(m[1]) != "" {
			return strings.TrimSpace(m[1])
		}
	}
	return ""
}
