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

// FenceLines 返回每行是否属于围栏代码块（含开头与结束行）。行可以带或不带结尾的换行。
func FenceLines(lines []string) []bool {
	in := make([]bool, len(lines))
	open, char := 0, byte(0)
	for i, line := range lines {
		line = strings.TrimRight(line, "\r\n")
		if open > 0 {
			in[i] = true
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
			open, char, in[i] = len(m[1]), m[1][0], true
		}
	}
	return in
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
