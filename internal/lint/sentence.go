package lint

import (
	"slices"
	"unicode"
)

// 句子切分（style-profile/style-stats）用的句末标点与收尾标点。
var (
	styleSentenceEnd = []rune("。！？!?；;…")
	styleClosers     = []rune("”」』）】〕)]’\"'")
)

// SentenceSpans 返回 rs 中每个句子的区间 [起, 止)（下标按 rune 计）：在句末标点与换行处切分，句末标点后紧跟的句末标点、
// 右引号与右括号归入该句，只含空白的句子不计。句子的区间含句首的空白，不含切分处的换行。文风统计（Style Stats）、
// 文风指标与文本检查的句子都按这一规则切分。
func SentenceSpans(rs []rune) [][2]int {
	var out [][2]int
	start, nonSpace := 0, false
	flush := func(end int) {
		if nonSpace {
			out = append(out, [2]int{start, end})
		}
		nonSpace = false
	}
	for i := 0; i < len(rs); i++ {
		r := rs[i]
		if r == '\n' {
			flush(i)
			start = i + 1
			continue
		}
		if !unicode.IsSpace(r) {
			nonSpace = true
		}
		if slices.Contains(styleSentenceEnd, r) {
			for i+1 < len(rs) && (slices.Contains(styleSentenceEnd, rs[i+1]) || slices.Contains(styleClosers, rs[i+1])) {
				i++
			}
			flush(i + 1)
			start = i + 1
		}
	}
	flush(len(rs))
	return out
}

// SentenceLengths 返回 text 中每个句子的长度（非空白字符数），句子按 SentenceSpans 切分。
func SentenceLengths(text string) []int {
	rs := []rune(text)
	spans := SentenceSpans(rs)
	out := make([]int, 0, len(spans))
	for _, s := range spans {
		n := 0
		for _, r := range rs[s[0]:s[1]] {
			if !unicode.IsSpace(r) {
				n++
			}
		}
		out = append(out, n)
	}
	return out
}

// FourCharRuns 返回 text 中恰好由四个汉字组成的连续汉字串（四字片段）的个数：汉字（Unicode Han）以外的任何字符都打断
// 汉字串。文风指标的 fourChar 与 tone-density 规则共用这一口径。
func FourCharRuns(text string) int {
	n, run := 0, 0
	for _, r := range text {
		if unicode.Is(unicode.Han, r) {
			run++
			continue
		}
		if run == 4 {
			n++
		}
		run = 0
	}
	if run == 4 {
		n++
	}
	return n
}
