// Package ctxpack 组装单元的上下文包（Context Pack）：只含当前单元写作所需的写作简报、大纲、作者反馈、决策、规范要点、
// 素材原文与相邻节，按固定优先级放入上下文预算（Context Budget），放不下的内容被裁剪。本包只读取产物，不修改任何文件；
// 只返回结构化的问题，由命令层转换为诊断（Diagnostic）。
package ctxpack

import (
	"fmt"
	"regexp"
	"slices"
	"strings"
	"unicode/utf8"

	"github.com/kw12121212/writing-anything/internal/brief"
	"github.com/kw12121212/writing-anything/internal/decision"
	"github.com/kw12121212/writing-anything/internal/draft"
	"github.com/kw12121212/writing-anything/internal/feedback"
	"github.com/kw12121212/writing-anything/internal/outline"
	"github.com/kw12121212/writing-anything/internal/resolve"
	"github.com/kw12121212/writing-anything/internal/work"
	"github.com/kw12121212/writing-anything/internal/workspace"
)

// 内容项的部分名，也是 trimmed 中的 part。
const (
	PartBrief       = "brief"
	PartOutline     = "outline"
	PartSection     = "section"
	PartFeedback    = "feedback"
	PartDecision    = "decision"
	PartRules       = "rules"
	PartGlossary    = "glossary"
	PartBannedTerms = "bannedTerms"
	PartStyle       = "style"
	PartSource      = "source"
	PartNeighbor    = "neighbor"
)

// ExcerptChars 是相邻节节选的最大字符数。
const ExcerptChars = 200

// Message 是一个核心观点（Key Message）。
type Message struct {
	ID   string
	Text string
}

// Brief 是写作简报（Brief）摘要；Unit 为写作简报所在的单元（本单元或祖先单元）。
type Brief struct {
	Unit        string
	Audience    string
	Purpose     string
	Length      *brief.Length
	Avoid       []string
	KeyMessages []Message
}

// Section 是本节在父单元大纲中的条目。
type Section struct {
	ID          string
	Title       string
	Reason      string
	Uses        []string
	KeyMessages []Message
}

// Decision 是关联本单元的一个未决定的决策。
type Decision struct {
	ID       string
	Question string
	Options  []decision.Option
	Blocking bool
}

// Rule 是一条生效的规则（Rule）。
type Rule struct {
	ID       string
	Type     string
	Severity string
	Params   map[string]any
}

// Norms 是生效的规范要点。
type Norms struct {
	Rules       []Rule
	Glossary    []resolve.Term
	BannedTerms []resolve.BannedTerm
	Style       []string
}

// Source 是一份素材（Source）的原文；Truncated 为 true 时 Text 被截断。
type Source struct {
	ID        string
	Type      string
	Title     string
	Text      string
	Truncated bool
}

// Neighbor 是一个相邻节；Position 为 previous 或 next。
type Neighbor struct {
	Unit     string
	Position string
	Title    string
	Excerpt  *string
}

// Trim 是一个被裁剪或截断的内容项。
type Trim struct {
	Part      string
	ID        string // 反馈、决策、素材与相邻节的 id 或单元引用；其他为空
	Chars     int
	KeptChars int
}

// Pack 是一个上下文包。被整项裁剪的内容不出现在对应字段中。
type Pack struct {
	Unit, Type, Stage, Genre string
	Budget                   resolve.Value
	Used                     int
	Brief                    *Brief
	Outline                  *outline.Outline
	Section                  *Section
	Feedback                 []feedback.Item
	Decisions                []Decision
	Norms                    Norms
	Sources                  []Source
	Neighbors                []Neighbor
	Trimmed                  []Trim
	items                    []item // 保留的内容项，按优先级顺序，供 Markdown 使用
}

// item 是一个内容项：body 是计入预算的 Markdown 文本。
type item struct {
	part, id string
	body     string
	prefix   string // 可截断项（素材原文）中不能截断的开头
	chars    int    // 全文的字符数；为 0 时按 body 计算（素材原文的 body 只含放得进预算的前缀）
	drop     func() // 整项裁剪时从 Pack 中移除该项
	truncate func(text string)
}

func chars(s string) int { return utf8.RuneCountInString(s) }

// Build 组装单元 u 的上下文包，并按预算裁剪。读取的写作简报、大纲、反馈文件或决策队列不通过校验，或合并配置有 error 级
// 问题时返回 nil 与这些问题；否则返回的问题都是 warning（大纲中的悬空引用、未能放入的素材、合并的 warning）。
func Build(root string, w *work.Work, u *work.Unit) (*Pack, []workspace.Problem) {
	ds, _, ps := decision.Load(root)
	if len(ps) > 0 {
		return nil, ps
	}
	q := brief.Queue{Decisions: ds}
	r, warnings := resolve.ForGenre(root, w.Genre.ID)
	if r == nil {
		return nil, warnings
	}
	p := &Pack{Unit: u.Ref, Type: u.Type, Stage: u.Stage, Genre: w.Genre.ID, Budget: r.ContextBudget}
	budget, _ := p.Budget.Value.(int)
	var items []item
	add := func(it item) { items = append(items, it) }
	briefs := briefCache{root: root, q: q, loaded: map[string]*brief.Brief{}}

	// 写作简报：本单元的，没有时取最近的祖先单元的。
	var b *brief.Brief
	for x := u; x != nil && b == nil; x = x.Parent {
		var ps []workspace.Problem
		if b, ps = briefs.load(w.UnitDir(x)); len(ps) > 0 {
			return nil, ps
		}
		if b != nil {
			p.Brief = briefOf(x.Ref, b)
		}
	}
	if p.Brief != nil {
		add(item{part: PartBrief, body: p.Brief.markdown(), drop: func() { p.Brief = nil }})
	}

	// 本单元的大纲与本节在父单元大纲中的条目；大纲中的悬空引用以 warning 报告（workspace-artifacts/cross-artifact-references）。
	own, refs, ps := loadOutline(root, w.UnitDir(u), &briefs)
	if ps != nil {
		return nil, ps
	}
	warnings = append(warnings, refs...)
	if own != nil {
		p.Outline = own
		add(item{part: PartOutline, body: outlineMarkdown(own), drop: func() { p.Outline = nil }})
	}
	var parent *outline.Outline
	var sectionSources []string
	if u.Parent != nil {
		pdir := w.UnitDir(u.Parent)
		if parent, refs, ps = loadOutline(root, pdir, &briefs); ps != nil {
			return nil, ps
		}
		warnings = append(warnings, refs...)
		if s := find(parent, u.ID); s != nil {
			pb, _ := briefs.load(pdir) // 已在 loadOutline 中加载并校验
			p.Section, sectionSources = sectionOf(s, pb)
			add(item{part: PartSection, body: p.Section.markdown(), drop: func() { p.Section = nil }})
		}
	}

	// 未处理的作者反馈与关联本单元的未决定决策。
	fb, ps := feedback.List(root, w, u)
	if len(ps) > 0 {
		return nil, ps
	}
	for _, f := range fb {
		if !feedback.Pending(f.Status) {
			continue
		}
		p.Feedback = append(p.Feedback, f)
		id := f.ID
		add(item{part: PartFeedback, id: id, body: feedbackMarkdown(f), drop: func() {
			p.Feedback = slices.DeleteFunc(p.Feedback, func(x feedback.Item) bool { return x.ID == id })
		}})
	}
	for _, d := range ds {
		if d.Status != decision.StatusOpen || !slices.Contains(d.Units, u.Ref) {
			continue
		}
		v := Decision{ID: d.ID, Question: d.Question, Options: d.Options, Blocking: d.Blocking}
		p.Decisions = append(p.Decisions, v)
		add(item{part: PartDecision, id: d.ID, body: v.markdown(), drop: func() {
			p.Decisions = slices.DeleteFunc(p.Decisions, func(x Decision) bool { return x.ID == v.ID })
		}})
	}

	// 规范要点。
	p.Norms = normsOf(r)
	for _, it := range p.Norms.items(&p.Norms) {
		add(it)
	}

	// 素材原文：只在内存中保留放得进预算的前缀。
	ids := sectionSources
	if p.Section == nil {
		ids = rootSources(b, own)
	}
	for _, id := range ids {
		s, t, warn := readSource(root, id, budget)
		if warn != nil {
			warnings = append(warnings, *warn)
		}
		if s.ID == "" {
			continue
		}
		s.Text = t.Prefix
		p.Sources = append(p.Sources, s)
		heading := fmt.Sprintf("### %s（%s）%s\n\n", s.ID, s.Type, oneLine(s.Title))
		add(item{part: PartSource, id: id, prefix: heading, body: heading + t.Prefix + "\n", chars: chars(heading) + t.Chars + 1,
			drop: func() { p.Sources = slices.DeleteFunc(p.Sources, func(x Source) bool { return x.ID == id }) },
			truncate: func(t string) {
				for i := range p.Sources {
					if p.Sources[i].ID == id {
						p.Sources[i].Text, p.Sources[i].Truncated = t, true
					}
				}
			}})
	}

	// 相邻节。
	for _, n := range neighbors(root, w, u, parent) {
		p.Neighbors = append(p.Neighbors, n)
		ref := n.Unit
		add(item{part: PartNeighbor, id: ref, body: n.markdown(), drop: func() {
			p.Neighbors = slices.DeleteFunc(p.Neighbors, func(x Neighbor) bool { return x.Unit == ref })
		}})
	}

	p.fit(items)
	return p, warnings
}

// briefCache 在一条命令中每个单元目录的写作简报只加载并校验一次。
type briefCache struct {
	root     string
	q        brief.Queue
	loaded   map[string]*brief.Brief
	problems map[string][]workspace.Problem
}

func (c *briefCache) load(dir string) (*brief.Brief, []workspace.Problem) {
	if b, ok := c.loaded[dir]; ok {
		return b, c.problems[dir]
	}
	b, ps := brief.Load(c.root, dir, c.q)
	c.loaded[dir] = b
	if c.problems == nil {
		c.problems = map[string][]workspace.Problem{}
	}
	c.problems[dir] = ps
	return b, ps
}

// fit 按优先级依次放入内容项；放不下的素材原文在剩余预算大于其标题所占字符数加 1 时截断，其他项整项裁剪。
func (p *Pack) fit(items []item) {
	budget, _ := p.Budget.Value.(int)
	left := budget
	for _, it := range items {
		n := it.chars
		if n == 0 {
			n = chars(it.body)
		}
		switch {
		case n <= left:
			left -= n
			p.items = append(p.items, it)
			continue
		case it.truncate != nil && left > chars(it.prefix)+1:
			it.body = prefixChars(it.body, left-1) + "\n" // 截断后仍以换行结尾
			it.truncate(strings.TrimPrefix(strings.TrimSuffix(it.body, "\n"), it.prefix))
			p.Trimmed = append(p.Trimmed, Trim{Part: it.part, ID: it.id, Chars: n, KeptChars: left})
			left = 0
			p.items = append(p.items, it)
			continue
		}
		it.drop()
		p.Trimmed = append(p.Trimmed, Trim{Part: it.part, ID: it.id, Chars: n})
	}
	p.Used = budget - left
}

// prefixChars 返回 s 的前 n 个字符，按字节偏移截取而不复制为 []rune。
func prefixChars(s string, n int) string {
	i := 0
	for ; n > 0 && i < len(s); n-- {
		_, size := utf8.DecodeRuneInString(s[i:])
		i += size
	}
	return s[:i]
}

// oneLine 把换行换成空格，避免打乱 Markdown 的列表与标题。
func oneLine(s string) string {
	return strings.Join(strings.Fields(strings.ReplaceAll(s, "\r\n", "\n")), " ")
}

func briefOf(ref string, b *brief.Brief) *Brief {
	v := &Brief{Unit: ref, Audience: b.Audience, Purpose: b.Purpose, Length: b.Length, Avoid: b.Avoid}
	for _, m := range b.KeyMessages {
		v.KeyMessages = append(v.KeyMessages, Message{m.ID, m.Text})
	}
	return v
}

// loadOutline 读取单元目录 dir 下的大纲，返回大纲、悬空引用（以 warning 报告）与阻止组装的问题。
func loadOutline(root, dir string, briefs *briefCache) (*outline.Outline, []workspace.Problem, []workspace.Problem) {
	b, ps := briefs.load(dir)
	if len(ps) > 0 {
		return nil, nil, ps
	}
	ms := outline.Messages{}
	if b != nil {
		ms.List = b.KeyMessages
	}
	o, fatal, refs := outline.LoadLenient(root, dir, ms)
	if len(fatal) > 0 {
		return nil, nil, fatal
	}
	for i := range refs {
		refs[i].Severity = workspace.SeverityWarning
	}
	return o, refs, nil
}

func find(o *outline.Outline, id string) *outline.Section {
	if o == nil {
		return nil
	}
	for i := range o.Sections {
		if o.Sections[i].ID == id {
			return &o.Sections[i]
		}
	}
	return nil
}

// sectionOf 返回本节条目与它引用的素材 id：该节的 uses，以及它覆盖的核心观点在父单元写作简报 pb 中的 sources。
func sectionOf(s *outline.Section, pb *brief.Brief) (*Section, []string) {
	v := &Section{ID: s.ID, Title: s.Title, Reason: s.Reason, Uses: s.Uses}
	ids := slices.Clone(s.Uses)
	if pb != nil {
		for _, id := range s.Covers {
			for _, m := range pb.KeyMessages {
				if m.ID == id {
					v.KeyMessages = append(v.KeyMessages, Message{m.ID, m.Text})
					ids = append(ids, m.Sources...)
				}
			}
		}
	}
	return v, dedupe(ids)
}

func normsOf(r *resolve.Resolved) Norms {
	n := Norms{Glossary: r.Glossary, BannedTerms: r.BannedTerms}
	ids := make([]string, 0, len(r.Rules))
	for id := range r.Rules {
		ids = append(ids, id)
	}
	slices.Sort(ids)
	for _, id := range ids {
		rule := r.Rules[id]
		if !rule.Enabled {
			continue
		}
		params := map[string]any{}
		for k, v := range rule.Params {
			params[k] = v.Value
		}
		n.Rules = append(n.Rules, Rule{ID: id, Type: fmt.Sprint(rule.Type.Value), Severity: fmt.Sprint(rule.Severity.Value), Params: params})
	}
	for _, s := range r.Style {
		n.Style = append(n.Style, s.Text)
	}
	return n
}

// rootSources 返回没有本节条目时引用的素材：写作简报的 sources、全部核心观点的 sources 与本单元大纲各节的 uses。
func rootSources(b *brief.Brief, own *outline.Outline) []string {
	var ids []string
	if b != nil {
		ids = append(ids, b.Sources...)
		for _, m := range b.KeyMessages {
			ids = append(ids, m.Sources...)
		}
	}
	if own != nil {
		for _, s := range own.Sections {
			ids = append(ids, s.Uses...)
		}
	}
	return dedupe(ids)
}

// dedupe 去掉重复的 id，保留首次出现的顺序。
func dedupe(ids []string) []string {
	var out []string
	for _, id := range ids {
		if !slices.Contains(out, id) {
			out = append(out, id)
		}
	}
	return out
}

// neighbors 返回子单元 u 在父单元 units 中的前一个与后一个兄弟单元；parent 为父单元的大纲（可为 nil）。
func neighbors(root string, w *work.Work, u *work.Unit, parent *outline.Outline) []Neighbor {
	if u.Parent == nil {
		return nil
	}
	sib := u.Parent.Units
	i := slices.Index(sib, u)
	var out []Neighbor
	for _, c := range []struct {
		at  int
		pos string
	}{{i - 1, "previous"}, {i + 1, "next"}} {
		if c.at < 0 || c.at >= len(sib) {
			continue
		}
		s := sib[c.at]
		n := Neighbor{Unit: s.Ref, Position: c.pos}
		if sec := find(parent, s.ID); sec != nil {
			n.Title = sec.Title
		}
		if data, ok, _ := draft.ReadUnitFile(root, w.UnitDir(s)+"/"+draft.FileName); ok {
			e := excerpt(string(data))
			n.Excerpt = &e
		}
		out = append(out, n)
	}
	return out
}

// comments 匹配 HTML 注释（包括作者批注），节选中去掉它们；heading 匹配 Markdown 的 ATX 标题行。
var (
	comments = regexp.MustCompile(`(?s)<!--.*?-->`)
	heading  = regexp.MustCompile(`^#{1,6}(\s|$)`)
)

// excerpt 返回正文第一个不是 Markdown 标题的段落（去掉 HTML 注释与首尾空白、换行换成空格）的前 ExcerptChars 个字符。
func excerpt(text string) string {
	text = comments.ReplaceAllString(strings.ReplaceAll(text, "\r\n", "\n"), "")
	for _, para := range strings.Split(text, "\n\n") {
		para = oneLine(para)
		if para == "" || heading.MatchString(para) {
			continue
		}
		return prefixChars(para, ExcerptChars)
	}
	return ""
}
