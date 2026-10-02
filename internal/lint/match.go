package lint

import "sort"

// MaxHits 是一次多词匹配最多返回的出现次数；超过时截断，由调用方报告 lint.truncated（workspace-artifacts/input-limits）。
const MaxHits = 1000

// hit 是一个词在正文中的一处出现：line 与 column 从 1 开始，term 为词在列表中的下标。
type hit struct {
	line, column, term int
}

// acNode 是 Aho-Corasick 自动机的一个状态。
type acNode struct {
	next map[rune]int32
	fail int32
	out  []int32 // 在这里结束的词
	dict int32   // 沿失败链最近的、有词结束的状态；没有时为 -1
}

// matcher 一次查找多个词：只由 ASCII 字母、数字与连字符组成的词按整个 ASCII 词精确查表（它们必须前后都不是 ASCII 字母、
// 数字或连字符，所以每个位置至多一种长度可能命中）；其他词用 Aho-Corasick 自动机。每行各扫描一遍，耗时与正文长度加上
// 命中次数成正比，与词条数无关。
type matcher struct {
	words  [][]rune
	tokens map[string][]int // ASCII 词 → 词的下标
	nodes  []acNode
}

func newMatcher(terms []string) *matcher {
	m := &matcher{tokens: map[string][]int{}, nodes: []acNode{{next: map[rune]int32{}, dict: -1}}}
	for k, term := range terms {
		w := []rune(term)
		m.words = append(m.words, w)
		if len(w) == 0 {
			continue
		}
		if asciiWord(w) {
			m.tokens[term] = append(m.tokens[term], k)
			continue
		}
		s := int32(0)
		for _, r := range w {
			n, ok := m.nodes[s].next[r]
			if !ok {
				n = int32(len(m.nodes))
				m.nodes = append(m.nodes, acNode{next: map[rune]int32{}, dict: -1})
				m.nodes[s].next[r] = n
			}
			s = n
		}
		m.nodes[s].out = append(m.nodes[s].out, int32(k))
	}
	// 按广度优先计算失败链与输出链。
	queue := []int32{}
	for _, n := range m.nodes[0].next {
		queue = append(queue, n)
	}
	for len(queue) > 0 {
		s := queue[0]
		queue = queue[1:]
		for r, n := range m.nodes[s].next {
			f := m.nodes[s].fail
			for f > 0 {
				if _, ok := m.nodes[f].next[r]; ok {
					break
				}
				f = m.nodes[f].fail
			}
			if g, ok := m.nodes[f].next[r]; ok && g != n {
				m.nodes[n].fail = g
			}
			fl := m.nodes[n].fail
			if len(m.nodes[fl].out) > 0 {
				m.nodes[n].dict = fl
			} else {
				m.nodes[n].dict = m.nodes[fl].dict
			}
			queue = append(queue, n)
		}
	}
	return m
}

// asciiWord 报告 w 是否只由 ASCII 字母、数字与连字符组成。
func asciiWord(w []rune) bool {
	for _, r := range w {
		if !wordChar(r) {
			return false
		}
	}
	return true
}

// scanTokens 对一行中每个等于某个 ASCII 词的整个 ASCII 词调用 emit(起点列下标, 词下标)；emit 返回 false 时停止。
func (m *matcher) scanTokens(v []rune, emit func(start, term int) bool) {
	if len(m.tokens) == 0 {
		return
	}
	for j := 0; j < len(v); {
		if !wordChar(v[j]) {
			j++
			continue
		}
		end := j
		for end < len(v) && wordChar(v[end]) {
			end++
		}
		for _, k := range m.tokens[string(v[j:end])] {
			if !emit(j, k) {
				return
			}
		}
		j = end
	}
}

// scanAC 用自动机对一行中每个非 ASCII 词的出现调用 emit；longest 为 true 时每个结束位置只给出最长的词（输出链上的第一个
// 输出），耗时与行长成线性。emit 返回 false 时停止。
func (m *matcher) scanAC(v []rune, longest bool, emit func(start, term int) bool) {
	if len(m.nodes) == 1 {
		return
	}
	s := int32(0)
	for j, r := range v {
		for s > 0 {
			if _, ok := m.nodes[s].next[r]; ok {
				break
			}
			s = m.nodes[s].fail
		}
		if n, ok := m.nodes[s].next[r]; ok {
			s = n
		}
		o := s
		if len(m.nodes[o].out) == 0 {
			o = m.nodes[o].dict
		}
		for ; o > 0; o = m.nodes[o].dict {
			for _, k := range m.nodes[o].out {
				if !emit(j-len(m.words[k])+1, int(k)) {
					return
				}
				if longest {
					break
				}
			}
			if longest {
				break
			}
		}
	}
}

// find 返回全部词在可见文字中的出现（按行、列排序，至多 MaxHits 个，即按位置最前的 MaxHits 个）与是否被截断。
func (m *matcher) find(t *text) ([]hit, bool) {
	var out []hit
	for i, v := range t.vis {
		rem := MaxHits - len(out)
		var line []hit
		full := false
		collect := func(start, k int) bool {
			line = append(line, hit{i + 1, start + 1, k})
			if len(line) > rem {
				full = true
				return false
			}
			return true
		}
		// 两种来源各自至多取 rem+1 个，排序后取按位置最前的 rem 个；有多余的即为截断。
		m.scanTokens(v, collect)
		n := len(line)
		m.scanAC(v, false, func(start, k int) bool {
			line = append(line, hit{i + 1, start + 1, k})
			if len(line)-n > rem {
				full = true
				return false
			}
			return true
		})
		line = sortHits(line)
		if full || len(line) > rem {
			return append(out, line[:rem]...), true
		}
		out = append(out, line...)
	}
	return out, false
}

// cover 返回 lines 中每行被任一词覆盖的列（不截断）：每个结束位置只取最长的词，用差分数组一遍标出，耗时与正文长度成线性。
func (m *matcher) cover(lines [][]rune) [][]bool {
	out := make([][]bool, len(lines))
	for i, v := range lines {
		diff := make([]int, len(v)+1)
		mark := func(start, k int) bool {
			diff[start]++
			diff[start+len(m.words[k])]--
			return true
		}
		m.scanTokens(v, mark)
		m.scanAC(v, true, mark)
		out[i] = make([]bool, len(v))
		for c, acc := 0, 0; c < len(v); c++ {
			acc += diff[c]
			out[i][c] = acc > 0
		}
	}
	return out
}

// spans 返回一行中每个结束位置最长的词的出现（起点列下标与词下标），耗时与行长成线性。
func (m *matcher) spans(v []rune) []hit {
	var out []hit
	add := func(start, k int) bool {
		out = append(out, hit{column: start, term: k})
		return true
	}
	m.scanTokens(v, add)
	m.scanAC(v, true, add)
	return out
}

// sortHits 按列排序同一行中的出现（两种来源分别按位置产生出现）。
func sortHits(hs []hit) []hit {
	sort.SliceStable(hs, func(a, b int) bool {
		return hs[a].column < hs[b].column || hs[a].column == hs[b].column && hs[a].term < hs[b].term
	})
	return hs
}

// dedupe 去掉重复的词，保留首次出现的顺序。
func dedupe(terms []string) []string {
	seen := make(map[string]bool, len(terms))
	var out []string
	for _, t := range terms {
		if !seen[t] {
			seen[t] = true
			out = append(out, t)
		}
	}
	return out
}

// CountTerms 返回正文 content 中 terms 的出现次数，口径与 ai-cliche 规则相同：同样的屏蔽区域（代码、HTML 注释与 URL）、去重后的
// 词表与一次扫描的多词匹配器；超过 MaxHits 时按 MaxHits 计。供不产生发现项的统计（如文风指标）复用，不随词条数多次扫描正文。
func CountTerms(content string, terms []string) int {
	terms = dedupe(terms)
	if len(terms) == 0 {
		return 0
	}
	hits, _ := newMatcher(terms).find(prepare(content))
	return len(hits)
}
