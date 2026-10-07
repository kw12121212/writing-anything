package closeout

import (
	"regexp"
	"strings"
	"unicode"
)

// 正文疑似专有名词扫描（closeout/closeout-body-scan）：wa-cli 不调用模型，中文没有大小写与分词边界，按出现频次统计
// 未登记词会把大量普通词语当作专名，所以只在人名高度可能出现的三类上下文中取词——命名引入（叫、名叫、叫做之后）、
// 对话前导（边界之后、紧跟说问喊答或其后为标点的道，且全文出现至少两次）与称谓（边界之后的「姓氏 + 称谓词」或
// 「老、小 + 姓氏」）；候选必须以常见姓氏开头（老、小 + 姓氏除外）。
// 精度优先：novel E2E 语料全部场景收口时该扫描零提示（见 TestNovelCorpusReplay）。

// SourceDraft 是正文扫描得到的疑似专有名词的来源字段取值。
const SourceDraft = "draft"

// htmlComments 匹配 HTML 注释（含作者批注与单处豁免），扫描前从正文中去掉。
var htmlComments = regexp.MustCompile(`(?s)<!--.*?-->`)

// singleSurnames 是常见单字姓氏表；compoundSurnames 是少量常见复姓。
var (
	singleSurnames = runeSet("王李张刘陈杨黄赵周吴徐孙马朱胡郭何林高罗郑梁谢宋唐许韩冯邓曹彭曾萧肖田董潘袁蔡蒋余于杜叶程魏苏吕丁任卢姚沈钟姜崔谭陆范汪廖石金韦贾夏付傅方邹熊白孟秦邱侯江尹薛闫段雷龙黎史陶贺毛郝顾龚邵万钱戴严欧莫孔向常汤康易乔赖文施洪辛柳祝岳柯齐庞左")
	compoundSurnames = [][]rune{[]rune("欧阳"), []rune("司马"), []rune("诸葛"), []rune("上官"), []rune("东方"), []rune("慕容"), []rune("令狐"), []rune("皇甫"), []rune("尉迟"), []rune("公孙")}
	// nameStops 是名字部分不会出现的常见虚词、副词、代词、动作与方位字：名字部分含其一时不取，用以排除「高声说」「方才说」一类短语。
	nameStops = runeSet("声也都就又才还再只却便正刚已曾将要会能可该总终常时先后前里上下是在着了过不没很一的地得和与跟对把被给从他她它我你您们这那谁什么怎说问道喊答叫笑轻低慢冷淡忽连回接补喃继自")
	speechVerbs = runeSet("说问道喊答")
	// prepositionSurnames 是也常作介词的姓氏字，称谓模式不取（「向老师请教」）。
	prepositionSurnames = runeSet("向于")
	// shoutBeforeCall 是「叫」前表示喊叫而非命名的字（「大叫」「尖叫」）。
	shoutBeforeCall = runeSet("大喊惊尖呼哭嚎狂怪乱")
	// honorifics 是称谓词，长的在前以便优先匹配。
	honorifics = [][]rune{[]rune("师傅"), []rune("老板"), []rune("先生"), []rune("小姐"), []rune("太太"), []rune("老师"), []rune("大爷"),
		[]rune("大妈"), []rune("阿姨"), []rune("大哥"), []rune("大姐"), []rune("叔叔"), []rune("婶子"), []rune("叔"), []rune("婶"), []rune("伯")}
)

func runeSet(s string) map[rune]bool {
	m := map[rune]bool{}
	for _, r := range s {
		m[r] = true
	}
	return m
}

// bodyCandidate 是正文中的一个候选：取值与（称谓类候选的）姓氏。
type bodyCandidate struct {
	name    string
	surname string // 称谓类候选的姓氏；其他候选为空
	repeat  bool   // 对话前导候选：正文中至少出现两次才保留
}

// isSep 报告 r 是否为边界字符：空白、标点或符号。
func isSep(r rune) bool { return unicode.IsSpace(r) || unicode.IsPunct(r) || unicode.IsSymbol(r) }

func isHan(r rune) bool { return unicode.Is(unicode.Han, r) }

// surnameAt 返回 rs[i:] 开头的姓氏长度（复姓优先），不是姓氏时为 0。
func surnameAt(rs []rune, i int) int {
	for _, c := range compoundSurnames {
		if hasRunes(rs, i, c) {
			return len(c)
		}
	}
	if i < len(rs) && singleSurnames[rs[i]] {
		return 1
	}
	return 0
}

func hasRunes(rs []rune, i int, p []rune) bool {
	if i < 0 || i+len(p) > len(rs) {
		return false
	}
	for k, r := range p {
		if rs[i+k] != r {
			return false
		}
	}
	return true
}

// givenOK 报告名字部分 rs[from:to] 是否全为汉字且不含 nameStops 中的字。
func givenOK(rs []rune, from, to int) bool {
	if from >= to || to > len(rs) {
		return false
	}
	for _, r := range rs[from:to] {
		if !isHan(r) || nameStops[r] {
			return false
		}
	}
	return true
}

// bodyCandidates 按出现顺序返回去掉 HTML 注释后的正文中三类上下文模式提取的候选（未去重）。
func bodyCandidates(body string) []bodyCandidate {
	rs := []rune(htmlComments.ReplaceAllString(body, ""))
	n := len(rs)
	dialogue := map[string]bool{} // 对话前导候选：至少出现两次才保留
	boundary := func(i int) bool { return i == 0 || isSep(rs[i-1]) }
	endOK := func(i int) bool { return i == n || isSep(rs[i]) }
	var out []bodyCandidate
	for i := 0; i < n; i++ {
		// 命名引入：叫、名叫（以叫结尾）、叫做 + 姓氏 + 1 至 2 个汉字 + 标点、空白或文末。
		if rs[i] == '叫' && (i == 0 || !shoutBeforeCall[rs[i-1]]) {
			s := i + 1
			if s < n && rs[s] == '做' {
				s++
			}
			if sl := surnameAt(rs, s); sl > 0 {
				for g := 1; g <= 2; g++ {
					end := s + sl + g
					if end <= n && givenOK(rs, s+sl, end) && endOK(end) {
						out = append(out, bodyCandidate{name: string(rs[s:end])})
						break
					}
				}
			}
		}
		if !boundary(i) {
			continue
		}
		if sl := surnameAt(rs, i); sl > 0 {
			// 称谓：边界 + 姓氏 + 称谓词（也作介词的姓氏字除外）。称谓命中时不再按对话前导取词，姓氏解析规则才能生效。
			honorific := false
			for _, h := range honorifics {
				if hasRunes(rs, i+sl, h) {
					honorific = true
					if sl > 1 || !prepositionSurnames[rs[i]] {
						out = append(out, bodyCandidate{name: string(rs[i : i+sl+len(h)]), surname: string(rs[i : i+sl])})
					}
					break
				}
			}
			// 对话前导：边界 + 姓氏开头、姓氏后至少 1 字、总长不超过 4 字的词 + 说问喊答或其后为边界的道（取最短的匹配），
			// 且该词在正文中至少出现两次。
			for l := sl + 1; !honorific && l <= 4; l++ {
				if i+l < n && speechVerbs[rs[i+l]] && (rs[i+l] != '道' || endOK(i+l+1)) && givenOK(rs, i+sl, i+l) {
					name := string(rs[i : i+l])
					dialogue[name] = true
					out = append(out, bodyCandidate{name: name, repeat: true})
					break
				}
			}
		}
		// 称谓：边界 + 老、小 + 姓氏 + 标点、空白、文末或说问道。
		if rs[i] == '老' || rs[i] == '小' {
			if sl := surnameAt(rs, i+1); sl > 0 {
				end := i + 1 + sl
				if endOK(end) || rs[end] == '说' || rs[end] == '问' || rs[end] == '道' {
					out = append(out, bodyCandidate{name: string(rs[i:end]), surname: string(rs[i+1 : end])})
				}
			}
		}
	}
	if len(dialogue) == 0 {
		return out
	}
	// 一遍滑窗统计对话前导候选（2 至 4 字）在正文中的出现次数，耗时与正文长度成线性。
	occurs := map[string]int{}
	for i := 0; i < n; i++ {
		for l := 2; l <= 4 && i+l <= n; l++ {
			if w := string(rs[i : i+l]); dialogue[w] {
				occurs[w]++
			}
		}
	}
	kept := out[:0]
	for _, c := range out {
		if !c.repeat || occurs[c.name] >= 2 {
			kept = append(kept, c)
		}
	}
	return kept
}

// resolvedBody 报告正文候选是否已解析：与设定集 name 或别名完全相等、以至少 2 字的 name 或别名开头，或（称谓类）
// 姓氏是某个人物条目 name 的开头（charInitials 含人物 name 的首字与前两字，前两字用于复姓）。
func resolvedBody(c bodyCandidate, names, charInitials map[string]bool) bool {
	if names[c.name] {
		return true
	}
	rs := []rune(c.name)
	for l := 2; l < len(rs); l++ {
		if names[string(rs[:l])] {
			return true
		}
	}
	return c.surname != "" && charInitials[c.surname]
}

// characterInitials 返回人物条目 name 的首字与前两字（用于复姓）集合。
func characterInitials(names []string) map[string]bool {
	out := map[string]bool{}
	for _, n := range names {
		rs := []rune(strings.TrimSpace(n))
		if len(rs) >= 1 {
			out[string(rs[:1])] = true
		}
		if len(rs) >= 2 {
			out[string(rs[:2])] = true
		}
	}
	return out
}
