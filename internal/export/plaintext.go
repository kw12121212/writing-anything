package export

import (
	"regexp"
	"sort"
	"strings"

	"github.com/kw12121212/writing-anything/internal/mdscan"
)

// 纯文本渲染用到的 Markdown 标记。地址与标题里不含换行与括号的常见写法都能匹配；匹配不到的写法按文字输出。
var (
	plainHeading = regexp.MustCompile(`^ {0,3}#{1,6}[ \t]+`)
	plainQuote   = regexp.MustCompile(`^ {0,3}> ?`)
	plainImage   = regexp.MustCompile(`!\[([^\]]*)\]\([^)\n]*\)`)
	plainLink    = regexp.MustCompile(`\[([^\]]*)\]\([^)\n]*\)`)
	plainBold    = regexp.MustCompile(`\*\*(.+?)\*\*`)
	plainUnder2  = regexp.MustCompile(`__(.+?)__`)
	plainItalic  = regexp.MustCompile(`\*([^*\n]+)\*`)
	plainUnder   = regexp.MustCompile(`(^|[^\p{L}\p{N}_])_([^_\n]+)_($|[^\p{L}\p{N}_])`)
)

// RenderPlainText 把去掉过程注释后的 Markdown 正文转为纯文本（Plain Text）：ATX 标题去掉 # 前缀保留标题文字；
// 行内代码与强调（**、__、* 与 _，_ 在单词中间时不是强调）只保留文字；链接 [文字](地址) 只保留文字，图片
// ![替代文字](地址) 输出「【图：替代文字】」；引用块行去掉行首的 > 与其后的一个空格；围栏代码块去掉开头与结束的围栏行、
// 内容行原样保留；不跨越空行的已闭合 HTML 注释去掉（跨越空行的按文字输出）。去掉标记后只剩空白的行整行去掉，连续的空行
// 规整为一个；标记延伸到行尾时去掉它之前留下的行尾空白，其余空白（如行尾两个空格的硬换行）原样保留。
func RenderPlainText(src string) string {
	s := strings.ReplaceAll(strings.ReplaceAll(strings.TrimPrefix(src, "\uFEFF"), "\r\n", "\n"), "\r", "\n")
	s = dropComments(s)
	lines := strings.Split(s, "\n")
	if n := len(lines); n > 0 && lines[n-1] == "" {
		lines = lines[:n-1] // 末尾换行切出的空元素不是一行
	}
	fenced := mdscan.FenceLines(lines)
	markers := mdscan.FenceMarkers(lines)
	var out []string
	for i, line := range lines {
		if markers[i] {
			continue // 开头与结束的围栏行去掉
		}
		if !fenced[i] {
			line = plainLine(line)
		}
		if strings.TrimSpace(line) == "" {
			// 空行只在上一行不是空行时保留：去掉标记变空的行整行去掉，不留下连续空行。
			if n := len(out); n > 0 && strings.TrimSpace(out[n-1]) != "" {
				out = append(out, "")
			}
			continue
		}
		out = append(out, line)
	}
	return strings.Join(out, "\n")
}

// plainLine 去掉一行（围栏代码块之外）中的 Markdown 标记：标题的 # 前缀、引用块的 > 前缀、行内代码的反引号、图片、链接、
// 强调记号与反斜杠转义。行内代码的内容只去掉两侧反引号，不做标记处理（与公众号渲染器一致），避免 `a*b*c` 中的 * 被
// 当成强调剥掉。
func plainLine(line string) string {
	line = plainHeading.ReplaceAllString(line, "")
	line = plainQuote.ReplaceAllString(line, "")
	var b strings.Builder
	pos := 0
	for _, sp := range mdscan.CodeSpans(line) {
		n := 0
		for sp[0]+n < len(line) && line[sp[0]+n] == '`' {
			n++
		}
		b.WriteString(stripInline(line[pos:sp[0]]))
		b.WriteString(line[sp[0]+n : sp[1]-n])
		pos = sp[1]
	}
	b.WriteString(stripInline(line[pos:]))
	return b.String()
}

// stripInline 去掉一段不含行内代码的文字中的图片、链接、强调记号与反斜杠转义。
func stripInline(s string) string {
	s = plainImage.ReplaceAllString(s, "【图：$1】")
	s = plainLink.ReplaceAllString(s, "$1")
	s = plainBold.ReplaceAllString(s, "$1")
	s = plainUnder2.ReplaceAllString(s, "$1")
	s = plainItalic.ReplaceAllString(s, "$1")
	for range 2 { // 相邻的两个下划线斜体（_a_ 与 _b_）需要两遍
		s = plainUnder.ReplaceAllString(s, "${1}${2}${3}")
	}
	return unescapeMD(s)
}

// dropComments 去掉不跨越空行的已闭合 HTML 注释（跨越空行的按文字输出）；围栏代码块与行内代码中的写法不动。
// 注释去掉后只剩空白的行整行去掉，不因此留下连续空行；注释延伸到行尾时去掉它之前留下的行尾空白（同 StripComments 的重建逻辑）。
func dropComments(s string) string {
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
	var blanks []int
	for _, m := range blankLineRE.FindAllStringIndex(s, -1) {
		blanks = append(blanks, m[0])
	}
	var cuts [][2]int
	after := 0
	for _, start := range opens {
		if start < after || inCode(start) {
			continue
		}
		end := firstAtOrAfter(closes, start+len("<!--"))
		if end < 0 {
			break // 之后再没有 -->：都不是注释
		}
		stop := end + len("-->")
		if blank := firstAtOrAfter(blanks, start); blank >= 0 && blank < stop {
			continue // 跨越空行的按文字输出
		}
		cuts = append(cuts, [2]int{start, stop})
		after = stop
	}
	if len(cuts) == 0 {
		return s
	}
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
	return strings.Join(out, "")
}
