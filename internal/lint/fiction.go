package lint

import (
	"strings"

	"github.com/kw12121212/writing-anything/internal/resolve"
)

// 写作简报 pov 的人称取值（brief.schema.json 的枚举）。
const (
	povFirst = "first"
	povThird = "third"
)

// dialogueVerbs 是说话人分句的收尾动词：对话后紧跟「他说。」这类分句时，引号内的句号应为逗号（？与！保持不变）。
const dialogueVerbs = "说道问答喊叫嚷吼笑哭"

// quotePairs 返回一行可见文字中成对引号的区间 [左引号, 右引号]：half 为半角双引号按出现序两两配对（奇数序的双引号到
// 其后第一个双引号），full 为全角引号按每个 “ 到其后第一个 ” 配对；两类引号各自独立配对，未配对的引号不产生区间。
func quotePairs(v []rune) (half, full [][2]int) {
	first := -1
	for j, r := range v {
		if r != '"' {
			continue
		}
		if first < 0 {
			first = j
			continue
		}
		half = append(half, [2]int{first, j})
		first = -1
	}
	open := -1
	for j, r := range v {
		switch r {
		case '“':
			if open < 0 {
				open = j
			}
		case '”':
			if open >= 0 {
				full = append(full, [2]int{open, j})
				open = -1
			}
		}
	}
	return half, full
}

// capped 按 MaxHits 截断发现项：超过上限时只保留前 MaxHits 项并报告截断（同词表类规则）。
func capped(out []Finding) ([]Finding, bool) {
	if len(out) <= MaxHits {
		return out, false
	}
	return out[:MaxHits], true
}

// checkDialogue 是对话标点（Dialogue Punctuation）规则：成对的半角双引号对话提示改用全角引号；对话引号内以句号「。」结尾、
// 闭引号后紧跟不超过 4 个汉字且以说话人动词收尾的分句时，提示把句号改为逗号。每行一遍扫描，配对由 quotePairs 一次算出，
// 扫描次数不随对话数增长（workspace-artifacts/input-limits）。
func checkDialogue(t *text, _ params, _ *resolve.Resolved, _ Input) ([]Finding, bool) {
	var out []Finding
	for i, v := range t.vis {
		half, full := quotePairs(v)
		for _, q := range half {
			out = append(out, Finding{Line: i + 1, Column: q[0] + 1, Text: string(v[q[0] : q[1]+1]), Suggestion: "改用全角引号“…”",
				Message: "对话标点（Dialogue Punctuation）：对话应使用全角引号“”"})
			if v[q[1]-1] == '。' && attribution(v, q[1]) {
				out = append(out, periodFinding(i, q[1]))
			}
		}
		for _, q := range full {
			if v[q[1]-1] == '。' && attribution(v, q[1]) {
				out = append(out, periodFinding(i, q[1]))
			}
		}
	}
	return capped(out)
}

// periodFinding 返回「对话后紧跟说话人时句号应为逗号」的发现项：句号在闭引号 v[close] 的前一列。
func periodFinding(line, close int) Finding {
	return Finding{Line: line + 1, Column: close, Text: "。", Suggestion: "，",
		Message: "对话标点（Dialogue Punctuation）：对话后紧跟说话人（如「他说」）时，引号内的句号应改为逗号"}
}

// attribution 报告闭引号 v[close] 之后是否紧跟不超过 4 个汉字、以说话人动词收尾的分句。
func attribution(v []rune, close int) bool {
	n := 0
	for j := close + 1; j < len(v) && isHan(v[j]); j++ {
		n++
		if n > 4 {
			return false
		}
	}
	return n > 0 && strings.ContainsRune(dialogueVerbs, v[close+n])
}

// checkPOV 是视角与人称一致（POV Consistency，启发式检测）规则：期望第三人称时，对话（成对的全角引号与成对的半角双引号
// 包住的文字）之外的叙述文字中每个「我」字报告一项疑似视角滑移或对话漏加引号；期望第一人称时不报告。未配对的引号不构成
// 对话，不掩盖其后的叙述文字。pov 人称无法识别（second、未设置或写作简报不可用）时 Run 不执行本规则，
// 以 lint.pov-unrecognized 提示。
func checkPOV(t *text, _ params, _ *resolve.Resolved, in Input) ([]Finding, bool) {
	if in.POVPerson != povThird {
		return nil, false
	}
	var out []Finding
	for i, v := range t.vis {
		half, full := quotePairs(v)
		inDialogue := func(j int) bool {
			for _, q := range half {
				if j > q[0] && j < q[1] {
					return true
				}
			}
			for _, q := range full {
				if j > q[0] && j < q[1] {
					return true
				}
			}
			return false
		}
		for j, r := range v {
			if r == '我' && !inDialogue(j) {
				out = append(out, Finding{Line: i + 1, Column: j + 1, Text: "我",
					Message: "视角与人称一致（POV Consistency，启发式检测）：叙述文字中出现「我」，可能是视角滑移或对话漏加引号；写作简报声明的 pov 人称为第三人称"})
			}
		}
	}
	return capped(out)
}
