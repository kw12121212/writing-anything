// Package mdscan 识别 Markdown 正文中的围栏代码块与行内代码，规则与文本检查（Lint）一致；导出与作者反馈的批注识别共用它，
// 使各个命令看到的「代码中的写法」是同一组。
package mdscan

import (
	"regexp"
	"strings"
)

// 围栏代码块：开头行缩进不超过 3 个空格、以至少三个 ` 或 ~ 开头（反引号围栏的信息字符串不含 `）；结束行为同一字符、
// 长度不短于开头、其后只有空白；未闭合的围栏延续到正文末尾。
var (
	backtickFence = regexp.MustCompile("^ {0,3}(`{3,})[^`]*$")
	tildeFence    = regexp.MustCompile(`^ {0,3}(~{3,})`)
)

// FenceLines 返回每行是否属于围栏代码块（含开头与结束行）。行可以带或不带结尾的换行。识别复用 FenceMarkers 的
// 状态机：标记行本身属于围栏，两个标记行之间的内容行也属于（未闭合时到末尾）。
func FenceLines(lines []string) []bool {
	mark := FenceMarkers(lines)
	in := make([]bool, len(lines))
	open := false
	for i, m := range mark {
		if m {
			open = !open // 标记行交替出现：开头行开启，结束行关闭
		}
		in[i] = m || open
	}
	return in
}

// FenceMarkers 返回每行是否为围栏代码块的开头或结束行（识别同 FenceLines；未闭合的围栏没有结束行）。行可以带或不带结尾的换行。
func FenceMarkers(lines []string) []bool {
	mark := make([]bool, len(lines))
	open, char := 0, byte(0)
	for i, line := range lines {
		line = strings.TrimRight(line, "\r\n")
		if open > 0 {
			trim := strings.TrimLeft(line, " ")
			n := 0
			for n < len(trim) && trim[n] == char {
				n++
			}
			if n >= open && strings.TrimSpace(trim[n:]) == "" {
				open, mark[i] = 0, true
			}
			continue
		}
		m := backtickFence.FindStringSubmatch(line)
		if m == nil {
			m = tildeFence.FindStringSubmatch(line)
		}
		if m != nil {
			open, char, mark[i] = len(m[1]), m[1][0], true
		}
	}
	return mark
}

// CodeSpans 返回一行中行内代码的字节区间 [开头反引号, 结尾反引号之后)：n 个反引号到之后恰好 n 个反引号为止；没有对应结束的
// 反引号不是行内代码。区间按起点升序、互不重叠。
func CodeSpans(line string) [][2]int {
	var out [][2]int
	for j := 0; j < len(line); {
		if line[j] != '`' {
			j++
			continue
		}
		n := 0
		for j+n < len(line) && line[j+n] == '`' {
			n++
		}
		end := -1
		for k := j + n; k < len(line); {
			if line[k] != '`' {
				k++
				continue
			}
			m := 0
			for k+m < len(line) && line[k+m] == '`' {
				m++
			}
			if m == n {
				end = k
				break
			}
			k += m
		}
		if end < 0 {
			j += n
			continue
		}
		out = append(out, [2]int{j, end + n})
		j = end + n
	}
	return out
}

// InSpans 报告偏移 c 是否落在某个区间 [起, 止) 中；spans 按起点升序且互不重叠。
func InSpans(spans [][2]int, c int) bool {
	lo, hi := 0, len(spans)
	for lo < hi {
		mid := (lo + hi) / 2
		if spans[mid][0] > c {
			hi = mid
		} else {
			lo = mid + 1
		}
	}
	return lo > 0 && c < spans[lo-1][1]
}

// Image 是正文中的一个 Markdown 图片 ![替代文字](地址) 或 ![替代文字](地址 "标题")：Line 从 1 开始，Start 与 End 为它在
// 整个正文中的字节区间，Src 为去掉尖括号后的地址。
type Image struct {
	Line       int
	Start, End int
	Alt, Src   string
	Title      string
}

// imageAt 在 ![ 处匹配图片；替代文字、标题各至多 1000 个字符，地址至多 1000 字节，可以用尖括号包住。每次只在
// imageWindow 字节的窗口内尝试，耗时有界（workspace-artifacts/input-limits）。
var imageAt = regexp.MustCompile(`^!\[((?:\\.|[^\[\]\\\n]){0,1000})\]\((?:<([^<>\n]{0,1000})>|([^()\s<>]{0,1000}))(?:[ \t]+"([^"\n]{0,1000})")?\)`)

const imageWindow = 12100

// Images 按出现顺序返回正文中的图片，跳过围栏代码块、行内代码与已闭合的 HTML 注释中的写法（与文本检查、导出一致）。
// 耗时与正文长度成线性：每个 ![ 处只在有界窗口内匹配，注释区间一次扫描得到。
func Images(content string) []Image {
	comments := closedComments(content)
	lines := strings.SplitAfter(content, "\n")
	fence := FenceLines(lines)
	var out []Image
	off := 0
	for i, line := range lines {
		start := off
		off += len(line)
		if fence[i] || !strings.Contains(line, "![") {
			continue
		}
		spans := CodeSpans(line)
		for j := 0; j < len(line); {
			k := strings.Index(line[j:], "![")
			if k < 0 {
				break
			}
			j += k
			if InSpans(spans, j) || InSpans(comments, start+j) {
				j += 2
				continue
			}
			m := imageAt.FindStringSubmatch(line[j:min(len(line), j+imageWindow)])
			if m == nil {
				j += 2
				continue
			}
			src := m[2]
			if src == "" {
				src = m[3]
			}
			out = append(out, Image{Line: i + 1, Start: start + j, End: start + j + len(m[0]), Alt: m[1], Src: src, Title: m[4]})
			j += len(m[0])
		}
	}
	return out
}

// closedComments 返回已闭合的 HTML 注释 <!-- … --> 的字节区间，按起点升序、互不重叠；没有结束标记的 <!-- 及其之后
// 不再有已闭合的注释。只扫描一遍，耗时与正文长度成线性。
func closedComments(s string) [][2]int {
	var out [][2]int
	for i := 0; ; {
		k := strings.Index(s[i:], "<!--")
		if k < 0 {
			return out
		}
		open := i + k
		e := strings.Index(s[open+4:], "-->")
		if e < 0 {
			return out
		}
		end := open + 4 + e + 3
		out = append(out, [2]int{open, end})
		i = end
	}
}
