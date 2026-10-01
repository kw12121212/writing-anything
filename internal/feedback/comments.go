package feedback

import (
	"crypto/sha256"
	"encoding/hex"
	"fmt"
	"os"
	"path/filepath"
	"regexp"
	"strings"

	"github.com/kw12121212/writing-anything/internal/draft"
	"github.com/kw12121212/writing-anything/internal/mdscan"
	"github.com/kw12121212/writing-anything/internal/work"
	"github.com/kw12121212/writing-anything/internal/workspace"
	"go.yaml.in/yaml/v3"
)

// 反馈的来源。
const (
	SourceConversation = "conversation"
	SourceComment      = "comment"
)

// MaxComments 是一个单元正文中允许的最多批注数：批注要记入 feedback.yaml（受 schema.MaxNodes 约束），逐条定位的
// 开销也随数量增长（workspace-artifacts/input-limits）。
const MaxComments = 1000

// CodeTooManyComments 表示正文中的批注超过 MaxComments。已发布的错误码不得改名或复用。
const CodeTooManyComments = "feedback.too-many-comments"

func tooManyComments(rel string, n int) workspace.Problem {
	return errorAt(CodeTooManyComments, rel, "", fmt.Sprintf("正文中有 %d 个以上的批注，超过上限 %d；请先处理并删除一部分批注", n, MaxComments))
}

// commentHeader 匹配批注开头 <!-- 之后的 @me: 或 @me(kind):。
var commentHeader = regexp.MustCompile(`^[ \t]*\n?[ \t]*@me(?:\((must-fix|suggestion|question|praise)\))?[ \t]*:`)

// Comment 是正文中的一个批注。
type Comment struct {
	ID         string
	Kind       string
	Text       string // 去掉首尾空白、换行规范为 LF 的批注文本
	Raw        string // 批注在正文中的原样文字
	Start, End int    // 批注在正文原始字节中的位置
}

// fenced 返回 content 中围栏代码块覆盖的字节区间，按升序；围栏的识别与文本检查一致（见 mdscan.FenceLines）。
func fenced(content string) [][2]int {
	lines := strings.SplitAfter(content, "\n")
	in := mdscan.FenceLines(lines)
	var out [][2]int
	off := 0
	for i, line := range lines {
		if in[i] {
			if n := len(out); n > 0 && out[n-1][1] == off {
				out[n-1][1] = off + len(line)
			} else {
				out = append(out, [2]int{off, off + len(line)})
			}
		}
		off += len(line)
	}
	return out
}

// Comments 解析正文中的批注（Comment）<!-- @me: 文本 --> 与 <!-- @me(kind): 文本 -->，按出现顺序。批注可以跨行，但不能
// 跨空行、不能包含另一个 <!--，也不能位于围栏代码块或行内代码中；不满足这些条件的（如漏写 --> 的）不是批注。id 为
// cm- 加 kind 与文本哈希的前 8 个十六进制字符，kind 与文本都相同的批注依次加 -2、-3，closed 报告为 true 的 id（已关闭的
// 记录）在编号时跳过，使它们不再占用正文中仍然存在的批注。
func Comments(content []byte, closed func(id string) bool) []Comment {
	s := string(content)
	blocks := fenced(s)
	var out []Comment
	seen := map[string]int{}
	for off := 0; ; {
		i := strings.Index(s[off:], "<!--")
		if i < 0 {
			break
		}
		start := off + i
		off = start + len("<!--")
		m := commentHeader.FindStringSubmatchIndex(s[off:])
		if m == nil || inside(blocks, start) || inCodeSpan(s, start) {
			continue
		}
		end := strings.Index(s[off:], "-->")
		if end < 0 {
			break
		}
		body := s[off+m[1] : off+end]
		if strings.Contains(body, "<!--") || blankLine.MatchString(s[start:off+end]) || inside(blocks, off+end) {
			continue
		}
		kind := "suggestion"
		if m[2] >= 0 {
			kind = s[off+m[2] : off+m[3]]
		}
		text := strings.TrimSpace(normalize(body))
		stop := off + end + len("-->")
		if text == "" {
			continue
		}
		off = stop
		sum := sha256.Sum256([]byte(kind + "\n" + text))
		base := "cm-" + hex.EncodeToString(sum[:])[:8]
		id := base
		for {
			seen[base]++
			if seen[base] > 1 {
				id = fmt.Sprintf("%s-%d", base, seen[base])
			}
			if closed == nil || !closed(id) {
				break
			}
		}
		out = append(out, Comment{ID: id, Kind: kind, Text: text, Raw: s[start:stop], Start: start, End: stop})
		if len(out) > MaxComments {
			break // 超过上限后不再解析，由调用方报告 feedback.too-many-comments
		}
	}
	return out
}

// blankLine 匹配空行：批注不能跨越它。
var blankLine = regexp.MustCompile(`\n[ \t\r]*\n`)

// inCodeSpan 报告偏移 off 是否位于其所在行的行内代码中（行内代码的识别与文本检查一致，见 mdscan.CodeSpans）。
func inCodeSpan(s string, off int) bool {
	lineStart := strings.LastIndex(s[:off], "\n") + 1
	lineEnd := len(s)
	if i := strings.Index(s[off:], "\n"); i >= 0 {
		lineEnd = off + i
	}
	return mdscan.InSpans(mdscan.CodeSpans(s[lineStart:lineEnd]), off-lineStart)
}

// inside 报告偏移 off 是否在某个围栏代码块的字节区间中。
func inside(blocks [][2]int, off int) bool { return mdscan.InSpans(blocks, off) }

// closedIDs 返回已关闭（resolved 或 rejected）的记录 id 的检查函数。
func closedIDs(recorded []Feedback) func(string) bool {
	closed := map[string]bool{}
	for _, f := range recorded {
		if f.Status == "resolved" || f.Status == "rejected" {
			closed[f.ID] = true
		}
	}
	return func(id string) bool { return closed[id] }
}

// pendingComments 返回正文 content 中尚未记入（id 不在 recorded 中）的批注反馈及其定位：status 为 open，source 为
// comment，锚点 quote 为整个批注，定位为批注开始的行与所在段落。正文只切分、规范化一次，行号按偏移递增计算，
// 耗时与正文长度成线性。
func pendingComments(content []byte, recorded []Feedback) []Item {
	ids := map[string]bool{}
	for _, f := range recorded {
		ids[f.ID] = true
	}
	var paras []draft.Paragraph
	var out []Item
	line, prev := 1, 0
	for _, c := range Comments(content, closedIDs(recorded)) {
		if ids[c.ID] {
			continue
		}
		if paras == nil {
			paras = draft.Split(content)
		}
		line += strings.Count(string(content[prev:c.Start]), "\n")
		prev = c.Start
		_, para := paragraphAt(paras, line)
		source := SourceComment
		a := &Anchor{Quote: normalize(c.Raw), Before: normalize(lastRunes(string(content[max(0, c.Start-4*contextRunes):c.Start]), contextRunes)),
			After: normalize(firstRunes(string(content[c.End:min(len(content), c.End+4*contextRunes)]), contextRunes))}
		if para > 0 {
			a.Paragraph = paras[para-1].Hash
		}
		f := Feedback{ID: c.ID, Kind: c.Kind, Status: "open", Text: c.Text, Source: &source, Anchor: a}
		out = append(out, Item{Feedback: f, Location: Location{State: StateLocated, Line: line, Paragraph: para}})
	}
	return out
}

// node 把反馈 f 编码为 feedback.yaml 中的一项。
func node(f Feedback) *yaml.Node {
	item := &yaml.Node{Kind: yaml.MappingNode, Content: []*yaml.Node{str("id"), str(f.ID), str("kind"), str(f.Kind), str("status"), str(f.Status)}}
	if f.Text != "" {
		item.Content = append(item.Content, str("text"), str(f.Text))
	}
	if f.Note != nil {
		item.Content = append(item.Content, str("note"), str(*f.Note))
	}
	if f.Source != nil {
		item.Content = append(item.Content, str("source"), str(*f.Source))
	}
	if a := f.Anchor; a != nil {
		item.Content = append(item.Content, str("anchor"), &yaml.Node{Kind: yaml.MappingNode, Content: []*yaml.Node{
			str("quote"), str(a.Quote), str("before"), str(a.Before), str("after"), str(a.After), str("paragraph"), str(a.Paragraph)}})
	}
	return item
}

// appendRecords 把 fs 追加到单元 u 的 feedback.yaml 末尾（不存在时创建），保留其他内容与注释。index 为参数问题的
// 下标（问题字段去掉 feedback[index]. 前缀），不需要时为 -1。返回写入后的全部反馈与变更的路径。
func appendRecords(root string, w *work.Work, u *work.Unit, fs []Feedback, index int) ([]Feedback, []string, []workspace.Problem) {
	rel := path(w, u)
	if info, err := os.Lstat(filepath.Join(root, filepath.FromSlash(rel))); err == nil && !info.Mode().IsRegular() {
		return nil, nil, []workspace.Problem{errorAt(workspace.CodeWriteFailed, rel, "", "不是普通文件（如符号链接），wa-cli 不会替换它")}
	}
	doc, _, ps := load(root, rel)
	if len(ps) > 0 {
		return nil, nil, ps
	}
	if doc == nil {
		doc = &yaml.Node{Kind: yaml.DocumentNode, HeadComment: "作者反馈（Feedback）；请用 wa-cli feedback add 与 wa-cli feedback resolve 修改。", Content: []*yaml.Node{{Kind: yaml.MappingNode, Content: []*yaml.Node{
			str("schema_version"), {Kind: yaml.ScalarNode, Tag: "!!int", Value: "1"},
			str("feedback"), {Kind: yaml.SequenceNode, Tag: "!!seq"},
		}}}}
	}
	seq := sequence(doc)
	if seq == nil {
		return nil, nil, []workspace.Problem{anchored(rel, "feedback")}
	}
	workspace.BlockSequence(doc.Content[0], "feedback")
	for _, f := range fs {
		seq.Content = append(seq.Content, node(f))
	}
	saved, data, ps := encodeChecked(doc, rel, index)
	if len(ps) > 0 {
		return nil, nil, ps
	}
	changed, ps := draft.EnsureUnitDir(root, w, u)
	if len(ps) > 0 {
		return nil, nil, ps
	}
	if err := workspace.WriteFile(root, rel, data); err != nil {
		return nil, nil, []workspace.Problem{errorAt(workspace.CodeWriteFailed, rel, "", "写入反馈失败："+err.Error())}
	}
	return saved, append(changed, rel), nil
}

// ImportComments 把单元 u 当前正文中尚未记入的批注反馈记入 feedback.yaml（source 为 comment），在 draft write
// 覆盖正文之前调用，使批注即使被新正文删除也保留在反馈中。没有需要记入的批注时不写文件。
func ImportComments(root string, w *work.Work, u *work.Unit) ([]string, []workspace.Problem) {
	content, ok, ps := draft.Read(root, w, u)
	if len(ps) > 0 || !ok {
		return nil, ps
	}
	switch n := len(Comments(content, nil)); {
	case n == 0:
		// 正文中没有批注时不读取 feedback.yaml，draft write 的行为与没有批注功能时相同。
		return nil, nil
	case n > MaxComments:
		return nil, []workspace.Problem{tooManyComments(w.UnitDir(u)+"/"+draft.FileName, MaxComments)}
	}
	_, recorded, ps := load(root, path(w, u))
	if len(ps) > 0 {
		return nil, ps
	}
	var pending []Feedback
	for _, it := range pendingComments(content, recorded) {
		pending = append(pending, it.Feedback)
	}
	if len(pending) == 0 {
		return nil, nil
	}
	_, changed, ps := appendRecords(root, w, u, pending, -1)
	return changed, ps
}

// removeComment 从正文中移除批注 c：批注独占的行（其余只有空白）整行删除，否则只删除批注本身。
func removeComment(content []byte, c Comment) []byte {
	s := string(content)
	lineStart := strings.LastIndex(s[:c.Start], "\n") + 1
	lineEnd := len(s)
	if i := strings.Index(s[c.End:], "\n"); i >= 0 {
		lineEnd = c.End + i + 1
	}
	if strings.TrimSpace(s[lineStart:c.Start]+s[c.End:lineEnd]) == "" {
		return []byte(s[:lineStart] + s[lineEnd:])
	}
	return []byte(s[:c.Start] + s[c.End:])
}
