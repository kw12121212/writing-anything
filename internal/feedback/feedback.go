// Package feedback 管理单元（Unit）中的作者反馈（Feedback）：单元目录下的 feedback.yaml 记录作者意见的类型、
// 状态、内容与锚点（Anchor）；锚点在每次读取时按当前正文重新定位，找不到时为失去锚点（Orphaned Anchor）。
// 本包只返回结构化的问题，由命令层转换为诊断（Diagnostic）。
package feedback

import (
	"fmt"
	"os"
	"path/filepath"
	"slices"
	"sort"
	"strings"
	"unicode/utf8"

	"github.com/kw12121212/writing-anything/internal/catalog"
	"github.com/kw12121212/writing-anything/internal/draft"
	"github.com/kw12121212/writing-anything/internal/schema"
	"github.com/kw12121212/writing-anything/internal/work"
	"github.com/kw12121212/writing-anything/internal/workspace"
	"go.yaml.in/yaml/v3"
)

// 反馈的错误码（Error Code）。已发布的错误码不得改名或复用。
const (
	CodeQuoteNotFound  = "feedback.quote-not-found"
	CodeAmbiguousQuote = "feedback.ambiguous-quote"
	CodeNotFound       = "feedback.not-found"
	CodeAnchoredValue  = "feedback.anchored-value"
)

// FileName 是单元目录中反馈产物的文件名。
const FileName = "feedback.yaml"

// 反馈类型、状态与锚点状态的取值，与 feedback.schema.json 一致。
var (
	Kinds    = []string{"must-fix", "suggestion", "question", "praise"}
	Statuses = []string{"open", "accepted", "resolved", "rejected"}
)

// 锚点状态。
const (
	StateLocated  = "located"
	StateOrphaned = "orphaned"
	StateNone     = "none"
)

// contextRunes 是锚点记录的前后文长度（字符数）。
const contextRunes = 30

// Anchor 是反馈指向的原文片段、前后文与所在段落的哈希。
type Anchor struct {
	Quote     string `json:"quote"`
	Before    string `json:"before"`
	After     string `json:"after"`
	Paragraph string `json:"paragraph"`
}

// Feedback 是 feedback.yaml 中的一条反馈。
type Feedback struct {
	ID     string  `json:"id"`
	Kind   string  `json:"kind"`
	Status string  `json:"status"`
	Text   string  `json:"text"`
	Note   *string `json:"note"`
	Source *string `json:"source"` // conversation 或 comment；省略时为 conversation
	Anchor *Anchor `json:"anchor"`
}

// SourceOf 返回反馈的来源，省略时为 conversation。
func (f Feedback) SourceOf() string {
	if f.Source == nil {
		return SourceConversation
	}
	return *f.Source
}

// Location 是锚点在当前正文中的定位结果；Line 与 Paragraph 从 1 开始，不适用时为 0。
type Location struct {
	State     string
	Line      int
	Paragraph int
}

// Item 是一条已定位的反馈。
type Item struct {
	Unit string // 单元引用
	Feedback
	Location
}

// Pending 报告状态为 status 的反馈是否尚未处理完（open 或 accepted）。
func Pending(status string) bool { return status == "open" || status == "accepted" }

func errorAt(code, path, field, msg string) workspace.Problem {
	return workspace.Problem{Code: code, Severity: workspace.SeverityError, Path: path, Field: field, Message: msg}
}

func normalize(s string) string { return strings.ReplaceAll(s, "\r\n", "\n") }

// occurrences 返回 quote 在 text 中每一处出现的字节偏移，允许重叠。
func occurrences(text, quote string) []int {
	var out []int
	for off := 0; off <= len(text); {
		i := strings.Index(text[off:], quote)
		if i < 0 {
			break
		}
		out = append(out, off+i)
		_, size := utf8.DecodeRuneInString(text[off+i:])
		off += i + max(size, 1)
	}
	return out
}

// position 返回偏移 off 所在的行号与段落序号。
func position(text string, paras []draft.Paragraph, off int) (line, para int) {
	return paragraphAt(paras, strings.Count(text[:off], "\n")+1)
}

// paragraphAt 返回行号 line 与它所在（或之前最近）的段落序号；line 在第一个段落之前时取第一个段落。
func paragraphAt(paras []draft.Paragraph, line int) (int, int) {
	para := sort.Search(len(paras), func(i int) bool { return paras[i].Line > line })
	return line, max(para, min(1, len(paras)))
}

func lastRunes(s string, n int) string {
	r := []rune(s)
	return string(r[max(0, len(r)-n):])
}

func firstRunes(s string, n int) string {
	r := []rune(s)
	return string(r[:min(n, len(r))])
}

// commonSuffix 返回 a 与 b 的公共后缀长度（字符数），只比较到较短者的长度，耗时与 b 的长度成正比。
func commonSuffix(a, b string) int {
	n := 0
	for len(a) > 0 && len(b) > 0 {
		ra, sa := utf8.DecodeLastRuneInString(a)
		rb, sb := utf8.DecodeLastRuneInString(b)
		if ra != rb {
			break
		}
		a, b, n = a[:len(a)-sa], b[:len(b)-sb], n+1
	}
	return n
}

// commonPrefix 返回 a 与 b 的公共前缀长度（字符数），耗时与 b 的长度成正比。
func commonPrefix(a, b string) int {
	n := 0
	for len(a) > 0 && len(b) > 0 {
		ra, sa := utf8.DecodeRuneInString(a)
		rb, sb := utf8.DecodeRuneInString(b)
		if ra != rb {
			break
		}
		a, b, n = a[sa:], b[sb:], n+1
	}
	return n
}

// Locate 在正文 content 中重新定位锚点 a：没有锚点为 none；原文不出现（content 为 nil 表示没有正文）为
// orphaned；出现多处时按段落哈希（1000 分）与前后文的公共部分长度打分，取最高分，同分取最靠前的一处。
func Locate(content []byte, a *Anchor) Location {
	if a == nil {
		return Location{State: StateNone}
	}
	text := normalize(string(content))
	offs := occurrences(text, normalize(a.Quote))
	if content == nil || len(offs) == 0 {
		return Location{State: StateOrphaned}
	}
	paras := draft.Split(content)
	quote := normalize(a.Quote)
	best, bestScore := Location{}, -1
	line, prev := 1, 0
	for _, off := range offs {
		// 行号按偏移递增累计，不在每处匹配重新扫描前文。
		line += strings.Count(text[prev:off], "\n")
		prev = off
		_, para := paragraphAt(paras, line)
		score := commonSuffix(text[:off], a.Before) + commonPrefix(text[off+len(quote):], a.After)
		if para > 0 && paras[para-1].Hash == a.Paragraph {
			score += 1000
		}
		if score > bestScore {
			best, bestScore = Location{State: StateLocated, Line: line, Paragraph: para}, score
		}
	}
	return best
}

// path 返回单元 u 的 feedback.yaml 路径，相对项目目录。
func path(w *work.Work, u *work.Unit) string { return w.UnitDir(u) + "/" + FileName }

// load 读取并按 Schema 校验 rel 处的反馈文档；文件不存在（或途经符号链接）时返回 nil 与 nil。
func load(root, rel string) (*yaml.Node, []Feedback, []workspace.Problem) {
	if _, ok, _ := draft.ReadUnitFile(root, rel); !ok {
		return nil, nil, nil
	}
	doc, ps := workspace.Read(root, rel)
	if doc == nil {
		return nil, nil, ps
	}
	if ps := schema.Named("feedback").ValidateNode(doc); len(ps) > 0 {
		return nil, nil, workspace.FromSchema(rel, ps)
	}
	var f struct {
		Feedback []Feedback `json:"feedback"`
	}
	catalog.Decode(doc, &f)
	return doc, f.Feedback, nil
}

// duplicates 报告反馈 id 的重复。
func duplicates(rel string, fs []Feedback) []workspace.Problem {
	var out []workspace.Problem
	seen := map[string]bool{}
	for i, f := range fs {
		if seen[f.ID] {
			out = append(out, errorAt(catalog.CodeDuplicateID, rel, fmt.Sprintf("feedback[%d].id", i), "反馈 id "+f.ID+" 重复"))
		}
		seen[f.ID] = true
	}
	return out
}

// Add 在单元 u 的 feedback.yaml 末尾追加一条反馈。kind 为空时为 suggestion；id 为空时取 fb-<n>；quote 非空时
// 必须在当前正文中恰好出现一次，并记录锚点。返回已定位的新反馈与变更的路径；出错时不修改文件。
func Add(root string, w *work.Work, u *work.Unit, kind, text, quote, id string) (*Item, []string, []workspace.Problem) {
	rel := path(w, u)
	if kind == "" {
		kind = "suggestion"
	}
	var anchor *Anchor
	var content []byte
	if quote != "" {
		data, ok, ps := draft.Read(root, w, u)
		if len(ps) > 0 {
			return nil, nil, ps
		}
		if !ok {
			return nil, nil, []workspace.Problem{errorAt(draft.CodeNotFound, w.UnitDir(u)+"/"+draft.FileName, "", "单元 "+u.Ref+" 还没有正文，无法定位原文；不带 --quote 可以记录针对整个单元的反馈")}
		}
		content = data
		body := normalize(string(data))
		q := normalize(quote)
		offs := occurrences(body, q)
		switch {
		case len(offs) == 0:
			return nil, nil, []workspace.Problem{errorAt(CodeQuoteNotFound, w.UnitDir(u)+"/"+draft.FileName, "", "原文「"+quote+"」不在单元 "+u.Ref+" 的正文中")}
		case len(offs) > 1:
			return nil, nil, []workspace.Problem{errorAt(CodeAmbiguousQuote, w.UnitDir(u)+"/"+draft.FileName, "", fmt.Sprintf("原文「%s」在正文中出现了 %d 次；请加长原文片段，使它只出现一次", quote, len(offs)))}
		}
		paras := draft.Split(data)
		_, para := position(body, paras, offs[0])
		anchor = &Anchor{Quote: q, Before: lastRunes(body[:offs[0]], contextRunes), After: firstRunes(body[offs[0]+len(q):], contextRunes)}
		if para > 0 {
			anchor.Paragraph = paras[para-1].Hash
		}
	}
	if info, err := os.Lstat(filepath.Join(root, filepath.FromSlash(rel))); err == nil && !info.Mode().IsRegular() {
		return nil, nil, []workspace.Problem{errorAt(workspace.CodeWriteFailed, rel, "", "不是普通文件（如符号链接），wa-cli 不会替换它")}
	}
	doc, existing, ps := load(root, rel)
	if len(ps) > 0 {
		return nil, nil, ps
	}
	if doc == nil {
		doc = &yaml.Node{Kind: yaml.DocumentNode, HeadComment: "作者反馈（Feedback）；请用 wa-cli feedback add 与 wa-cli feedback resolve 修改。", Content: []*yaml.Node{{Kind: yaml.MappingNode, Content: []*yaml.Node{
			str("schema_version"), {Kind: yaml.ScalarNode, Tag: "!!int", Value: "1"},
			str("feedback"), {Kind: yaml.SequenceNode, Tag: "!!seq"},
		}}}}
	}
	seen := map[string]bool{}
	for _, f := range existing {
		seen[f.ID] = true
	}
	if id == "" {
		for n := len(existing) + 1; ; n++ {
			if id = fmt.Sprintf("fb-%d", n); !seen[id] {
				break
			}
		}
	} else if seen[id] {
		return nil, nil, []workspace.Problem{errorAt(catalog.CodeDuplicateID, rel, "id", "单元 "+u.Ref+" 中已有反馈 "+id)}
	}
	seq := sequence(doc)
	if seq == nil {
		return nil, nil, []workspace.Problem{anchored(rel, "feedback")}
	}
	item := &yaml.Node{Kind: yaml.MappingNode, Content: []*yaml.Node{str("id"), str(id), str("kind"), str(kind), str("status"), str("open")}}
	if text != "" {
		item.Content = append(item.Content, str("text"), str(text))
	}
	if anchor != nil {
		item.Content = append(item.Content, str("anchor"), &yaml.Node{Kind: yaml.MappingNode, Content: []*yaml.Node{
			str("quote"), str(anchor.Quote), str("before"), str(anchor.Before), str("after"), str(anchor.After), str("paragraph"), str(anchor.Paragraph)}})
	}
	index := len(seq.Content)
	workspace.BlockSequence(doc.Content[0], "feedback")
	seq.Content = append(seq.Content, item)
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
	f := saved[index]
	return &Item{Unit: u.Ref, Feedback: f, Location: Locate(content, f.Anchor)}, append(changed, rel), nil
}

// encodeChecked 编码 doc 并重新解析，按 Schema 与 id 唯一性复查；index 为本次修改的反馈下标，其问题的字段去掉
// feedback[index]. 前缀，以参数名表示。
func encodeChecked(doc *yaml.Node, rel string, index int) ([]Feedback, []byte, []workspace.Problem) {
	data, err := workspace.Encode(doc)
	if err != nil {
		return nil, nil, []workspace.Problem{errorAt(workspace.CodeWriteFailed, rel, "", "生成反馈失败："+err.Error())}
	}
	written, p := schema.Parse(data)
	if p != nil {
		p.Line = 0
		return nil, nil, workspace.FromSchema(rel, []schema.Problem{*p})
	}
	if ps := schema.Named("feedback").ValidateNode(written); len(ps) > 0 {
		out := workspace.FromSchema(rel, ps)
		prefix := fmt.Sprintf("feedback[%d]", index)
		for i := range out {
			out[i].Line = 0
			if out[i].Field == prefix {
				out[i].Field = ""
			}
			out[i].Field = strings.TrimPrefix(out[i].Field, prefix+".")
		}
		return nil, nil, out
	}
	var f struct {
		Feedback []Feedback `json:"feedback"`
	}
	catalog.Decode(written, &f)
	if ps := duplicates(rel, f.Feedback); len(ps) > 0 {
		return nil, nil, ps
	}
	return f.Feedback, data, nil
}

func str(v string) *yaml.Node { return &yaml.Node{Kind: yaml.ScalarNode, Tag: "!!str", Value: v} }

func anchored(rel, field string) workspace.Problem {
	return errorAt(CodeAnchoredValue, rel, field, "该处经由 YAML 锚点、别名或合并键得到，wa-cli 无法只修改这一处；请先去掉锚点、别名与合并键")
}

func plain(n *yaml.Node) bool { return n.Anchor == "" && n.Kind != yaml.AliasNode }

// value 返回映射 m 中键 key 的值节点；映射含合并键或值不能直接修改（带锚点、是别名）时 ok 为 false。
func value(m *yaml.Node, key string) (v *yaml.Node, ok bool) {
	if !plain(m) {
		return nil, false
	}
	for i := 0; i+1 < len(m.Content); i += 2 {
		if m.Content[i].ShortTag() == "!!merge" {
			return nil, false
		}
		if m.Content[i].Value == key {
			v = m.Content[i+1]
		}
	}
	return v, v == nil || plain(v)
}

// sequence 返回文档中的 feedback 序列节点；途经锚点、别名或合并键时返回 nil（Schema 保证它存在）。
func sequence(doc *yaml.Node) *yaml.Node {
	v, ok := value(doc.Content[0], "feedback")
	if !ok || v == nil {
		return nil
	}
	return v
}

// Resolve 把单元 u 中反馈 id 的状态改为 status（为空时为 resolved），setNote 为 true 时把处理说明设为 note。id 为
// 尚未记入的批注反馈时先把它记入 feedback.yaml。source 为 comment 的反馈状态改为 resolved 或 rejected 时，若正文中仍有
// 该批注则把它移除，并在正文未被作者改过时重建写入记录。返回修改后的反馈与变更的路径；取值未变时不写文件。
func Resolve(root string, w *work.Work, u *work.Unit, id, status, note string, setNote bool) (*Item, []string, []workspace.Problem) {
	rel := path(w, u)
	if status == "" {
		status = "resolved"
	}
	if !slices.Contains(Statuses, status) {
		return nil, nil, []workspace.Problem{errorAt(schema.CodeSchemaViolation, rel, "status",
			fmt.Sprintf("取值 %q 不在允许范围内（%s）", status, strings.Join(Statuses, "、")))}
	}
	content, _, ps := draft.Read(root, w, u)
	if len(ps) > 0 {
		return nil, nil, ps
	}
	doc, fs, ps := load(root, rel)
	if len(ps) > 0 {
		return nil, nil, ps
	}
	var changed []string
	index := slices.IndexFunc(fs, func(f Feedback) bool { return f.ID == id })
	// 批注的编号跳过修改前已关闭的记录；移除批注时用同一编号找到它。
	closed := closedIDs(fs)
	if index < 0 {
		pending := pendingComments(content, fs)
		i := slices.IndexFunc(pending, func(it Item) bool { return it.ID == id })
		if i < 0 {
			return nil, nil, []workspace.Problem{errorAt(CodeNotFound, rel, "", fmt.Sprintf("单元 %s 中没有反馈 %s", u.Ref, id))}
		}
		// 批注反馈尚未记入：先记入，再修改其状态。
		if _, changed, ps = appendRecords(root, w, u, []Feedback{pending[i].Feedback}, -1); len(ps) > 0 {
			return nil, nil, ps
		}
		if doc, fs, ps = load(root, rel); len(ps) > 0 {
			return nil, nil, ps
		}
		index = len(fs) - 1
	}
	f := fs[index]
	if f.Status == status && (!setNote || f.Note != nil && *f.Note == note) {
		return &Item{Unit: u.Ref, Feedback: f, Location: Locate(content, f.Anchor)}, changed, nil
	}
	field := fmt.Sprintf("feedback[%d]", index)
	seq := sequence(doc)
	if seq == nil || index >= len(seq.Content) {
		return nil, nil, []workspace.Problem{anchored(rel, "feedback")}
	}
	item := seq.Content[index]
	st, ok := value(item, "status")
	if !ok || st == nil {
		return nil, nil, []workspace.Problem{anchored(rel, field+".status")}
	}
	st.Value, st.Tag, st.Style = status, "!!str", 0
	if setNote {
		n, ok := value(item, "note")
		switch {
		case !ok:
			return nil, nil, []workspace.Problem{anchored(rel, field+".note")}
		case n == nil:
			item.Content = append(item.Content, str("note"), str(note))
		default:
			n.Value, n.Tag, n.Style = note, "!!str", 0
		}
	}
	saved, data, ps := encodeChecked(doc, rel, index)
	if len(ps) > 0 {
		return nil, nil, ps
	}
	if err := workspace.WriteFile(root, rel, data); err != nil {
		return nil, nil, []workspace.Problem{errorAt(workspace.CodeWriteFailed, rel, "", "写入反馈失败："+err.Error())}
	}
	if !slices.Contains(changed, rel) {
		changed = append(changed, rel)
	}
	f = saved[index]
	if f.SourceOf() == SourceComment && (status == "resolved" || status == "rejected") {
		more, ps := removeFromDraft(root, w, u, content, f.ID, closed)
		if len(ps) > 0 {
			// feedback.yaml 已经写入：如实报告变更的路径。
			return nil, changed, ps
		}
		if len(more) > 0 {
			changed = append(changed, more...)
			content, _, _ = draft.Read(root, w, u)
		}
	}
	return &Item{Unit: u.Ref, Feedback: f, Location: Locate(content, f.Anchor)}, changed, nil
}

// removeFromDraft 从单元 u 的正文 content 中移除 id 为 id 的批注，并在正文未被作者改过时重建写入记录；正文中没有
// 该批注时什么也不做。返回写入的路径。
func removeFromDraft(root string, w *work.Work, u *work.Unit, content []byte, id string, closed func(string) bool) ([]string, []workspace.Problem) {
	for _, c := range Comments(content, closed) {
		if c.ID != id {
			continue
		}
		now := removeComment(content, c)
		rel := w.UnitDir(u) + "/" + draft.FileName
		if err := workspace.WriteFile(root, rel, now); err != nil {
			return nil, []workspace.Problem{errorAt(workspace.CodeWriteFailed, rel, "", "移除批注失败："+err.Error())}
		}
		more, ps := draft.Rebaseline(root, w, u, content, now)
		return append([]string{rel}, more...), ps
	}
	return nil, nil
}

// List 返回单元 u 的全部反馈，按文件内顺序，并按当前正文定位锚点。
// feedback.yaml 中的重复 id 作为问题一并返回，反馈照常列出。
// 尚未记入 feedback.yaml 的批注反馈按正文顺序排在已记入的反馈之后。
func List(root string, w *work.Work, u *work.Unit) ([]Item, []workspace.Problem) {
	rel := path(w, u)
	_, fs, ps := load(root, rel)
	if len(ps) > 0 {
		return nil, ps
	}
	content, _, ps := draft.Read(root, w, u)
	if len(ps) > 0 {
		return nil, ps
	}
	var out []Item
	for _, f := range fs {
		out = append(out, Item{Unit: u.Ref, Feedback: f, Location: Locate(content, f.Anchor)})
	}
	pending := pendingComments(content, fs)
	problems := duplicates(rel, fs)
	if len(pending) > MaxComments {
		pending = pending[:MaxComments]
		problems = append(problems, tooManyComments(w.UnitDir(u)+"/"+draft.FileName, MaxComments))
	}
	for _, it := range pending {
		it.Unit = u.Ref
		out = append(out, it)
	}
	return out, problems
}

// ListAll 按作品、单元树先序与文件内顺序返回 works 中全部单元的反馈。
func ListAll(root string, works []*work.Work) ([]Item, []workspace.Problem) {
	var items []Item
	var problems []workspace.Problem
	for _, w := range works {
		for _, u := range w.Units() {
			its, ps := List(root, w, u)
			items, problems = append(items, its...), append(problems, ps...)
		}
	}
	return items, problems
}

// CheckUnitArtifacts 校验 works 中每个单元目录里存在的 feedback.yaml：Schema 与反馈 id 唯一。返回已校验的路径与问题。
func CheckUnitArtifacts(root string, works []*work.Work) (checked []string, problems []workspace.Problem) {
	for _, w := range works {
		for _, u := range w.Units() {
			rel := path(w, u)
			if _, ok, _ := draft.ReadUnitFile(root, rel); ok {
				checked = append(checked, rel)
				problems = append(problems, CheckArtifact(root, rel)...)
			}
		}
	}
	return checked, problems
}

// CheckArtifact 校验 feedback.yaml（rel）：Schema 与反馈 id 唯一。
func CheckArtifact(root, rel string) []workspace.Problem {
	_, fs, ps := load(root, rel)
	if len(ps) > 0 {
		return ps
	}
	return duplicates(rel, fs)
}
