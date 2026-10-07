package ctxpack

import (
	"slices"
	"strings"
	"unicode"

	"github.com/kw12121212/writing-anything/internal/lint"
)

// Placeholder 是被遮蔽句子的占位文本：此处有视角人物（POV）尚未获知、不能写出的设定（秘密遮蔽，Secret Masking）。
const Placeholder = "（另有未揭示设定）"

// Masked 是一个文本被遮蔽的内容项：Part 为 character 或 knowledge，ID 为设定条目 id，Sentences 为被遮蔽的句数。
type Masked struct {
	Part      string
	ID        string
	Sentences int
}

// sentenceEnds 是句末标点；closers 是紧跟在句末标点之后、仍属于本句的收尾引号与括号。
const (
	sentenceEnds = "。！？!?…\n"
	closers      = "”’」』）)】》"
)

// splitSentences 按中文句末标点（。！？!?…与换行）把文本切成句子，每句包含其句末标点、紧随的收尾引号与括号
// 以及之后的空白；拼接全部句子等于原文。ASCII 双引号不分开合，不作为收尾引号。
func splitSentences(text string) []string {
	var out []string
	rs := []rune(text)
	start := 0
	for i := 0; i < len(rs); i++ {
		if !strings.ContainsRune(sentenceEnds, rs[i]) {
			continue
		}
		j := i + 1
		for j < len(rs) && (strings.ContainsRune(sentenceEnds, rs[j]) || strings.ContainsRune(closers, rs[j])) {
			j++
		}
		for j < len(rs) && unicode.IsSpace(rs[j]) {
			j++
		}
		out = append(out, string(rs[start:j]))
		start, i = j, j-1
	}
	if start < len(rs) {
		out = append(out, string(rs[start:]))
	}
	return out
}

// maskText 把 text 中含任一揭示词（Reveal Terms，匹配方式同连续性审查）的句子替换为占位文本，返回遮蔽后的文本与
// 被遮蔽的句数。句子末尾的空白（含换行）保留；相邻的被遮蔽句（之间没有空白）合并为一个占位。terms 为空时原样返回。
func maskText(text string, terms []string) (string, int) {
	if len(terms) == 0 || text == "" {
		return text, 0
	}
	// 先对全文匹配一次：没有命中时原样返回，有命中时逐句只匹配命中的揭示词，避免每句都对全部揭示词建匹配器。
	if terms = lint.MatchedTerms(text, terms); len(terms) == 0 {
		return text, 0
	}
	var b strings.Builder
	n := 0
	merge := false // 上一句被遮蔽且没有尾随空白：本句被遮蔽时并入同一占位
	for _, sent := range splitSentences(text) {
		body := strings.TrimRightFunc(sent, unicode.IsSpace)
		tail := sent[len(body):]
		if strings.TrimSpace(body) == "" || len(lint.MatchedTerms(body, terms)) == 0 {
			b.WriteString(sent)
			merge = false
			continue
		}
		n++
		if !merge {
			b.WriteString(Placeholder)
		}
		b.WriteString(tail)
		merge = tail == ""
	}
	return b.String(), n
}

// hiddenTerms 返回未获知条目的全部揭示词（去重升序）。
func hiddenTerms(hidden map[string][]string) []string {
	var out []string
	for _, ts := range hidden {
		out = append(out, ts...)
	}
	return slices.Compact(slices.Sorted(slices.Values(out)))
}
