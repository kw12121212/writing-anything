package learn

import (
	"math"
	"regexp"
	"slices"
	"strings"
	"unicode"
	"unicode/utf8"

	"github.com/kw12121212/writing-anything/internal/draft"
)

// MaxChars 是一份提议全部素材的非空白字符数上限：常用词统计的内存随字数增长，不可信的输入不能不受限制。
const MaxChars = 500000

// maxTerms 是常用词的条数上限；minTermCount 是常用词的最少出现次数。
const (
	maxTerms     = 20
	minTermCount = 3
)

// Distribution 是长度分布：长度为非空白字符数，保留两位小数。
type Distribution struct {
	Count  int     `json:"count" yaml:"count"`
	Mean   float64 `json:"mean" yaml:"mean"`
	Median float64 `json:"median" yaml:"median"`
	Stdev  float64 `json:"stdev" yaml:"stdev"`
	P10    float64 `json:"p10" yaml:"p10"`
	P90    float64 `json:"p90" yaml:"p90"`
}

// Term 是一个常用词。
type Term struct {
	Term    string  `json:"term" yaml:"term"`
	Count   int     `json:"count" yaml:"count"`
	Per1000 float64 `json:"per_1000" yaml:"per_1000"`
}

// Stats 是文风统计（Style Stats）。
type Stats struct {
	Chars       int                `json:"chars" yaml:"chars"`
	Sentences   Distribution       `json:"sentences" yaml:"sentences"`
	Paragraphs  Distribution       `json:"paragraphs" yaml:"paragraphs"`
	Punctuation map[string]float64 `json:"punctuation" yaml:"punctuation"`
	Terms       []Term             `json:"terms" yaml:"terms"`
}

// Punctuation 是统计频率的标点集合。
var Punctuation = []rune("，。、；：？！…—「」『』“”‘’（）《》,.;:?!")

var (
	sentenceEnd = []rune("。！？!?；;…")
	closers     = []rune("”」』）】〕)]’\"'")
	latinWord   = regexp.MustCompile(`[A-Za-z]{3,}`)
)

// Chars 返回 text 中非空白字符的个数。
func Chars(text string) int {
	n := 0
	for _, r := range text {
		if !unicode.IsSpace(r) {
			n++
		}
	}
	return n
}

// Compute 由素材内容 texts 计算文风统计；同样的内容总是得到同样的结果。
func Compute(texts [][]byte) Stats {
	var sentences, paragraphs []int
	punct := map[rune]int{}
	chars := 0
	counts := map[string]int{}
	for _, text := range texts {
		for _, p := range draft.Split(text) {
			n := Chars(p.Text)
			chars += n
			paragraphs = append(paragraphs, n)
			sentences = append(sentences, splitSentences(p.Text)...)
			for _, r := range p.Text {
				if slices.Contains(Punctuation, r) {
					punct[r]++
				}
			}
			countTerms(p.Text, counts)
		}
	}
	s := Stats{Chars: chars, Sentences: distribution(sentences), Paragraphs: distribution(paragraphs), Punctuation: map[string]float64{}, Terms: []Term{}}
	for r, c := range punct {
		s.Punctuation[string(r)] = per1000(c, chars)
	}
	s.Terms = terms(counts, chars)
	return s
}

// splitSentences 返回段落中每个句子的长度：在句末标点与换行处切分，句末标点后紧跟的句末标点、右引号与右括号归入该句，空句不计。
func splitSentences(text string) []int {
	var out []int
	rs := []rune(text)
	n := 0
	flush := func() {
		if n > 0 {
			out = append(out, n)
		}
		n = 0
	}
	for i := 0; i < len(rs); i++ {
		r := rs[i]
		if r == '\n' {
			flush()
			continue
		}
		if !unicode.IsSpace(r) {
			n++
		}
		if slices.Contains(sentenceEnd, r) {
			for i+1 < len(rs) && (slices.Contains(sentenceEnd, rs[i+1]) || slices.Contains(closers, rs[i+1])) {
				i++
				n++
			}
			flush()
		}
	}
	flush()
	return out
}

func distribution(xs []int) Distribution {
	d := Distribution{Count: len(xs)}
	if len(xs) == 0 {
		return d
	}
	s := slices.Sorted(slices.Values(xs))
	sum := 0
	for _, x := range s {
		sum += x
	}
	mean := float64(sum) / float64(len(s))
	v := 0.0
	for _, x := range s {
		v += (float64(x) - mean) * (float64(x) - mean)
	}
	median := float64(s[len(s)/2])
	if len(s)%2 == 0 {
		median = float64(s[len(s)/2-1]+s[len(s)/2]) / 2
	}
	d.Mean, d.Median, d.Stdev = round2(mean), round2(median), round2(math.Sqrt(v/float64(len(s))))
	d.P10, d.P90 = float64(rank(s, 0.1)), float64(rank(s, 0.9))
	return d
}

// rank 返回最近秩分位数：第 ⌈p·n⌉ 个（至少第 1 个）。
func rank(sorted []int, p float64) int {
	k := int(math.Ceil(p * float64(len(sorted))))
	if k < 1 {
		k = 1
	}
	return sorted[k-1]
}

func round2(x float64) float64 { return math.Round(x*100) / 100 }

func per1000(count, chars int) float64 {
	if chars == 0 {
		return 0
	}
	return round2(float64(count) * 1000 / float64(chars))
}

// countTerms 统计 text 中汉字连续串里长度 2 到 4 的子串，与长度不小于 3 的拉丁字母词（转小写）。
func countTerms(text string, counts map[string]int) {
	var run []rune
	flush := func() {
		for l := 2; l <= 4; l++ {
			for i := 0; i+l <= len(run); i++ {
				counts[string(run[i:i+l])]++
			}
		}
		run = run[:0]
	}
	for _, r := range text {
		if unicode.Is(unicode.Han, r) {
			run = append(run, r)
			continue
		}
		flush()
	}
	flush()
	for _, w := range latinWord.FindAllString(text, -1) {
		counts["\x00"+strings.ToLower(w)]++ // 前缀区分拉丁词与汉字子串，拉丁词不参与子串去重
	}
}

// terms 取出现次数不少于 minTermCount 的常用词：去掉被出现次数相同的更长汉字子串包含的子串，按次数降序、再按字典序取前 maxTerms 项。
func terms(counts map[string]int, chars int) []Term {
	removed := map[string]bool{}
	for u, c := range counts {
		if c < minTermCount || strings.HasPrefix(u, "\x00") {
			continue
		}
		rs := []rune(u)
		for l := 2; l < len(rs); l++ {
			for i := 0; i+l <= len(rs); i++ {
				if t := string(rs[i : i+l]); counts[t] == c {
					removed[t] = true
				}
			}
		}
	}
	out := []Term{}
	for t, c := range counts {
		if c >= minTermCount && !removed[t] {
			out = append(out, Term{Term: strings.TrimPrefix(t, "\x00"), Count: c})
		}
	}
	slices.SortFunc(out, func(a, b Term) int {
		if a.Count != b.Count {
			return b.Count - a.Count
		}
		return strings.Compare(a.Term, b.Term)
	})
	if len(out) > maxTerms {
		out = out[:maxTerms]
	}
	for i := range out {
		out[i].Per1000 = per1000(out[i].Count, chars)
	}
	return out
}

// validText 报告 data 是否为 UTF-8 文本。
func validText(data []byte) bool { return utf8.Valid(data) }

// SentenceLengths 返回段落文字中每个句子的长度（非空白字符数），切分规则与文风统计相同。
func SentenceLengths(text string) []int { return splitSentences(text) }
