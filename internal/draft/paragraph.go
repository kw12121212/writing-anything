package draft

import (
	"crypto/sha256"
	"encoding/hex"
	"math"
	"strings"
	"unicode"
)

// 段落出处（Provenance）的取值，与 provenance.schema.json 一致。
const (
	OriginHuman  = "human"
	OriginAgent  = "agent"
	OriginEdited = "agent-draft-human-edited"
)

// Origins 按汇总输出的固定顺序列出全部出处取值。
var Origins = []string{OriginHuman, OriginAgent, OriginEdited}

// OriginNames 是出处取值的人读名称。
var OriginNames = map[string]string{OriginHuman: "作者", OriginAgent: "Agent", OriginEdited: "Agent 起草、作者修改"}

// Paragraph 是正文中的一个段落（Paragraph）。
type Paragraph struct {
	Line   int    // 段落首行的行号，从 1 开始
	Text   string // 段落原文，行之间以 LF 连接
	Hash   string // 规范化文本的 SHA-256 十六进制
	Chars  int    // 非空白 Unicode 字符的个数
	Origin string // 推导出的出处；Split 不填写
}

// Split 把正文切分为段落：段落是由空行（只含空白字符的行）分隔的连续非空行；去掉行首空白后以 ``` 或 ~~~ 开头的行开启围栏
// 代码块，直到以同样字符开头的行结束，代码块中的空行不分段，未闭合的围栏延续到正文末尾。
func Split(content []byte) []Paragraph {
	lines := strings.Split(strings.ReplaceAll(string(content), "\r\n", "\n"), "\n")
	var out []Paragraph
	var cur []string
	start, fence := 0, ""
	flush := func() {
		if len(cur) > 0 {
			out = append(out, paragraph(start, cur))
			cur = nil
		}
	}
	for i, line := range lines {
		lead := strings.TrimLeft(line, " \t")
		switch {
		case fence != "":
			if strings.HasPrefix(lead, fence) {
				fence = ""
			}
		case strings.TrimSpace(line) == "":
			flush()
			continue
		case strings.HasPrefix(lead, "```") || strings.HasPrefix(lead, "~~~"):
			fence = lead[:3]
		}
		if len(cur) == 0 {
			start = i + 1
		}
		cur = append(cur, line)
	}
	flush()
	return out
}

func paragraph(line int, lines []string) Paragraph {
	norm := make([]string, len(lines))
	chars := 0
	for i, l := range lines {
		norm[i] = strings.TrimRightFunc(l, unicode.IsSpace)
		for _, r := range l {
			if !unicode.IsSpace(r) {
				chars++
			}
		}
	}
	sum := sha256.Sum256([]byte(strings.Join(norm, "\n")))
	return Paragraph{Line: line, Text: strings.Join(lines, "\n"), Hash: hex.EncodeToString(sum[:]), Chars: chars}
}

// recorded 是写入记录中的一个段落。
type recorded struct {
	Hash   string `json:"hash"`
	Origin string `json:"origin"`
}

// gap 是两个段落序列对齐后，相邻对齐点之间（含开头与结尾）未对齐的一段：cur 与 rec 为各自的下标，next 为其后
// 第一个当前段落的下标（位于末尾时为当前段落数）。
type gap struct {
	cur, rec []int
	next     int
}

// align 按最长公共子序列对齐当前段落的哈希 cur 与记录段落的哈希 rec，返回每个当前段落对齐上的记录下标（没有
// 对齐时为 -1）与各个非空的未对齐区间。方向按位记录，长度只保留相邻两行，内存为 n·m/8 字节。
func align(cur, rec []string) ([]int, []gap) {
	n, m := len(cur), len(rec)
	down := make([]uint64, (n*m+63)/64)
	next, row := make([]int, m+1), make([]int, m+1)
	for i := n - 1; i >= 0; i-- {
		for j := m - 1; j >= 0; j-- {
			switch {
			case cur[i] == rec[j]:
				row[j] = next[j+1] + 1
			case next[j] >= row[j+1]:
				row[j] = next[j]
				down[(i*m+j)/64] |= 1 << ((i*m + j) % 64)
			default:
				row[j] = row[j+1]
			}
		}
		next, row = row, next
	}
	matches := make([]int, n)
	var gaps []gap
	g := gap{}
	flush := func(at int) {
		if len(g.cur)+len(g.rec) > 0 {
			g.next = at
			gaps = append(gaps, g)
		}
		g = gap{}
	}
	i, j := 0, 0
	for i < n && j < m {
		switch {
		case cur[i] == rec[j]:
			flush(i)
			matches[i] = j
			i, j = i+1, j+1
		case down[(i*m+j)/64]&(1<<((i*m+j)%64)) != 0:
			matches[i] = -1
			g.cur = append(g.cur, i)
			i++
		default:
			g.rec = append(g.rec, j)
			j++
		}
	}
	for ; i < n; i++ {
		matches[i] = -1
		g.cur = append(g.cur, i)
	}
	for ; j < m; j++ {
		g.rec = append(g.rec, j)
	}
	flush(n)
	return matches, gaps
}

func hashes(ps []Paragraph) []string {
	out := make([]string, len(ps))
	for i, p := range ps {
		out[i] = p.Hash
	}
	return out
}

func recordHashes(rec []recorded) []string {
	out := make([]string, len(rec))
	for i, r := range rec {
		out[i] = r.Hash
	}
	return out
}

// derive 按写入记录 rec 推导 ps 中每个段落的出处：hasRecord 为 false（没有写入记录）时全部为 human。否则按哈希
// 序列的最长公共子序列对齐，对齐上的段落沿用记录中的出处；相邻对齐点之间未对齐的当前段落按顺序与同一区间内未对齐
// 的记录段落配对，配对上的段落在记录出处为 human 时为 human、否则为 agent-draft-human-edited，其余为 human。
// inherit 为 true 时配对上的段落直接继承记录出处，用于 wa-cli 自己修改正文（如移除批注）后重建写入记录。
func derive(ps []Paragraph, rec []recorded, hasRecord bool, inherit ...bool) {
	for i := range ps {
		ps[i].Origin = OriginHuman
	}
	if !hasRecord {
		return
	}
	matches, gaps := align(hashes(ps), recordHashes(rec))
	for i, j := range matches {
		if j >= 0 {
			ps[i].Origin = rec[j].Origin
		}
	}
	for _, g := range gaps {
		for k, i := range g.cur {
			switch {
			case k >= len(g.rec):
			case len(inherit) > 0 && inherit[0]:
				ps[i].Origin = rec[g.rec[k]].Origin
			case rec[g.rec[k]].Origin != OriginHuman:
				ps[i].Origin = OriginEdited
			}
		}
	}
}

// Share 是一类出处的汇总。
type Share struct {
	Origin     string  `json:"origin"`
	Paragraphs int     `json:"paragraphs"`
	Chars      int     `json:"chars"`
	Ratio      float64 `json:"ratio"`
}

// summarize 按 Origins 的固定顺序汇总段落数、字符数与字符占比（保留 4 位小数）。
func summarize(ps []Paragraph) (shares []Share, totalChars int) {
	for _, p := range ps {
		totalChars += p.Chars
	}
	for _, o := range Origins {
		s := Share{Origin: o}
		for _, p := range ps {
			if p.Origin == o {
				s.Paragraphs++
				s.Chars += p.Chars
			}
		}
		if totalChars > 0 {
			s.Ratio = math.Round(float64(s.Chars)/float64(totalChars)*10000) / 10000
		}
		shares = append(shares, s)
	}
	return shares, totalChars
}
