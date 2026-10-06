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
	"github.com/kw12121212/writing-anything/internal/canon"
	"github.com/kw12121212/writing-anything/internal/decision"
	"github.com/kw12121212/writing-anything/internal/draft"
	"github.com/kw12121212/writing-anything/internal/feedback"
	"github.com/kw12121212/writing-anything/internal/knowledge"
	"github.com/kw12121212/writing-anything/internal/outline"
	"github.com/kw12121212/writing-anything/internal/resolve"
	"github.com/kw12121212/writing-anything/internal/series"
	"github.com/kw12121212/writing-anything/internal/state"
	"github.com/kw12121212/writing-anything/internal/threads"
	"github.com/kw12121212/writing-anything/internal/work"
	"github.com/kw12121212/writing-anything/internal/workspace"
)

// 内容项的部分名，也是 trimmed 中的 part。
const (
	PartBrief        = "brief"
	PartOutline      = "outline"
	PartSection      = "section"
	PartCharacter    = "character"
	PartState        = "state"
	PartPriorEnding  = "prior-ending"
	PartChapSummary  = "chapter-summary"
	PartPartSummary  = "part-summary"
	PartBookSummary  = "book-summary"
	PartKnowledge    = "knowledge"
	PartReaderOnly   = "reader-only"
	PartReveal       = "reveal"
	PartThread       = "thread"
	PartFeedback     = "feedback"
	PartDecision     = "decision"
	PartFact         = "fact"
	PartSummary      = "summary"
	PartSceneSummary = "scene-summary"
	PartRules        = "rules"
	PartGlossary     = "glossary"
	PartBannedTerms  = "bannedTerms"
	PartStyle        = "style"
	PartSource       = "source"
	PartNeighbor     = "neighbor"
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

// Series 是成员作品所属系列的事实与前序作品的已验收摘要。
type Series struct {
	ID, Title string
	Facts     []series.Fact
	Summaries []Summary
}

// Summary 是一个前序作品根单元的摘要；Stale 为摘要之后正文又被修改。
type Summary struct {
	Work  string
	Text  string
	Stale bool
}

// Character 是场景大纲声明的出场人物在设定集（Canon）中的当前设定。
type Character struct {
	ID         string
	Name       string
	Text       string
	Protection string
}

// SceneSummary 是一个前序场景的已验收摘要（复用 summary.yaml）；Stale 为摘要之后正文又被修改。
type SceneSummary struct {
	Unit  string
	Text  string
	Stale bool
}

// RevealBoundary 是场景大纲一节的揭示边界（Reveal Boundary）：本场景不能提前透露的信息。
type RevealBoundary struct {
	Section string
	Text    string
}

// Thread 是与本场景契约相关的一条未回收伏笔（Open Foreshadowing）：埋设在本场景、计划回收于本场景，或本场景
// 大纲节 recovers 引用的伏笔台账（Foreshadowing Ledger）条目。
type Thread struct {
	ID             string
	Text           string
	PlantedIn      string
	PlannedRecover string
	Overdue        bool
}

// StateItem 是出场人物状态快照（Character States）中的一条：契约出场人物按 state at 本场景语义重放的当前状态。
type StateItem struct {
	Entry    string  `json:"entry"`
	Name     *string `json:"name"`
	State    string  `json:"state"`
	Proposal string  `json:"proposal"`
	Scene    string  `json:"scene"`
}

// PriorEnding 是一个前序场景的原文结尾（Prior Ending）：至多 400 个字符，最近的在前。
type PriorEnding struct {
	Unit    string `json:"unit"`
	Excerpt string `json:"excerpt"`
}

// LayeredSummary 是一层分层摘要（Layered Summary）：所属章（chapter）、部（part）或全书（book）的摘要。
type LayeredSummary struct {
	Unit  string `json:"unit"`
	Level string `json:"level"`
	Text  string `json:"text"`
	Stale bool   `json:"stale"`
}

// PriorEndingChars 是前序场景结尾的最大字符数。
const PriorEndingChars = 400

// KnowledgeItem 是视角人物（POV）或读者已知的一个设定条目（scene-context 的 knowledge 与 readerOnly 内容项）：
// Event 与 Scene 指向该获知者最早获知它的知情事件（knowledge 主规格的 knowledge-at 语义）。
type KnowledgeItem struct {
	Entry string
	Name  *string
	Text  string
	Event string
	Scene string
}

// CodePOVNotInCanon 是场景契约的视角人物在设定集中没有同名 character 条目时的 warning 错误码。
const CodePOVNotInCanon = "knowledge.pov-not-in-canon"

// CodeSummaryMissing 是前序作品或前序场景还没有摘要时的 warning 错误码。已发布的错误码不得改名或复用。
const CodeSummaryMissing = "context.summary-missing"

// CodeCharacterNotInCanon 是场景大纲声明的出场人物在设定集中没有同名 character 条目时的 warning 错误码。
const CodeCharacterNotInCanon = "context.character-not-in-canon"

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
	Characters               []Character      // 场景级：出场人物的当前设定；非 scene 单元为 nil
	States                   []StateItem      // 场景级：出场人物的状态快照；非 scene 单元为 nil
	PriorEndings             []PriorEnding    // 场景级：前序场景的原文结尾；非 scene 单元为 nil
	Summaries                []LayeredSummary // 场景级：章、部与全书的分层摘要；非 scene 单元为 nil
	Knowledge                []KnowledgeItem  // 场景级：视角人物已知的条目；非 scene 单元为 nil
	ReaderOnly               []KnowledgeItem  // 场景级：读者已知、视角人物未知的条目；非 scene 单元为 nil
	PriorScenes              []SceneSummary   // 场景级：前序场景的已验收摘要；非 scene 单元为 nil
	Reveal                   []RevealBoundary
	Threads                  []Thread // 场景级：与本场景契约相关的未回收伏笔；非 scene 单元为 nil
	Feedback                 []feedback.Item
	Decisions                []Decision
	Norms                    Norms
	Sources                  []Source
	Neighbors                []Neighbor
	Series                   *Series // 作品不属于系列时为 nil
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
	r, s, warnings := series.ForWork(root, w)
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

	// 场景级上下文（scene-context）：出场人物的当前设定、揭示边界与未回收伏笔，只对单元类型为 scene 的单元组装。
	if u.Type == "scene" {
		its, fatal := sceneItems(root, w, u, own, p, &warnings)
		if fatal != nil {
			return nil, fatal
		}
		items = append(items, its...)
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

	// 系列的事实与前序作品的已验收摘要（series 主规格的 context-series）。
	if s != nil {
		p.Series = &Series{ID: s.ID, Title: s.Title, Facts: []series.Fact{}, Summaries: []Summary{}}
		for _, f := range s.Facts {
			p.Series.Facts = append(p.Series.Facts, f)
			id := f.ID
			add(item{part: PartFact, id: id, body: factMarkdown(f), drop: func() {
				p.Series.Facts = slices.DeleteFunc(p.Series.Facts, func(x series.Fact) bool { return x.ID == id })
			}})
		}
		for _, pr := range series.PrecedingSummaries(root, s, w.ID) {
			if pr.Problems != nil {
				// 别的作品的摘要读不出来不让本单元的上下文包失败：降为 warning 并跳过（series 主规格的 context-series）。
				for _, q := range pr.Problems {
					q.Severity = workspace.SeverityWarning
					warnings = append(warnings, q)
				}
				continue
			}
			if pr.Summary == nil {
				warnings = append(warnings, workspace.Problem{Code: CodeSummaryMissing, Severity: workspace.SeverityWarning,
					Message: fmt.Sprintf("系列 %s 中排在前面的作品 %s 还没有已验收摘要；请在它验收后用 wa-cli summary set %s 写入", s.ID, pr.Work, pr.Work)})
				continue
			}
			v := Summary{Work: pr.Work, Text: pr.Summary.Text, Stale: pr.Summary.Stale}
			p.Series.Summaries = append(p.Series.Summaries, v)
			add(item{part: PartSummary, id: v.Work, body: v.markdown(), drop: func() {
				p.Series.Summaries = slices.DeleteFunc(p.Series.Summaries, func(x Summary) bool { return x.Work == v.Work })
			}})
		}
	}

	// 场景级：前序场景结尾（Prior Ending）——同作品先序最近两个场景的原文末尾（scene-context 的 priorEndings）。
	if u.Type == "scene" {
		// 先序中有正文的场景，取最近两个（不足取全部），最近的在前。
		var withDraft []*work.Unit
		for _, x := range w.Units() {
			if x.Ref == u.Ref {
				break
			}
			if x.Type != "scene" {
				continue
			}
			if _, ok, ps := draft.Read(root, w, x); len(ps) == 0 && ok {
				withDraft = append(withDraft, x)
			}
		}
		count := 0
		for i := len(withDraft) - 1; i >= 0 && count < 2; i-- {
			c := withDraft[i]
			excerpt := tailCharsOf(root, w, c)
			if strings.TrimSpace(excerpt) == "" {
				continue
			}
			count++
			v := PriorEnding{Unit: c.Ref, Excerpt: excerpt}
			p.PriorEndings = append(p.PriorEndings, v)
			ref := v.Unit
			heading := fmt.Sprintf("### %s 的结尾\n\n", ref)
			add(item{part: PartPriorEnding, id: ref, prefix: heading, body: heading + v.Excerpt + "\n",
				drop: func() {
					p.PriorEndings = slices.DeleteFunc(p.PriorEndings, func(x PriorEnding) bool { return x.Unit == ref })
				},
				truncate: func(t string) {
					for j := range p.PriorEndings {
						if p.PriorEndings[j].Unit == ref {
							p.PriorEndings[j].Excerpt = t
						}
					}
				}})
		}
		// 分层摘要（Layered Summary）：所属章、部与全书根单元的摘要，按章、部、书的顺序。
		levels := []struct {
			level string
			part  string
			unit  *work.Unit
		}{}
		for a := u.Parent; a != nil; a = a.Parent {
			switch a.Type {
			case "chapter":
				levels = append(levels, struct {
					level string
					part  string
					unit  *work.Unit
				}{"chapter", PartChapSummary, a})
			case "part":
				levels = append(levels, struct {
					level string
					part  string
					unit  *work.Unit
				}{"part", PartPartSummary, a})
			}
		}
		if w.Root != nil && w.Root.Type != "scene" {
			levels = append(levels, struct {
				level string
				part  string
				unit  *work.Unit
			}{"book", PartBookSummary, w.Root})
		}
		for _, l := range levels {
			sum, ps := series.LoadSummary(root, w, l.unit)
			if len(ps) > 0 {
				for _, q := range ps {
					q.Severity = workspace.SeverityWarning
					warnings = append(warnings, q)
				}
				continue
			}
			if sum == nil {
				warnings = append(warnings, workspace.Problem{Code: CodeSummaryMissing, Severity: workspace.SeverityWarning,
					Message: fmt.Sprintf("%s 层还没有摘要（Summary）；用 wa-cli summary set %s 写入后，后续场景的上下文才会带出该层摘要", l.unit.Ref, l.unit.Ref)})
				continue
			}
			v := LayeredSummary{Unit: l.unit.Ref, Level: l.level, Text: sum.Text, Stale: sum.Stale}
			p.Summaries = append(p.Summaries, v)
			ref := v.Unit
			add(item{part: l.part, id: ref, body: v.markdown(), drop: func() {
				p.Summaries = slices.DeleteFunc(p.Summaries, func(x LayeredSummary) bool { return x.Unit == ref })
			}})
		}
	}

	// 场景级：前序场景的已验收摘要（复用 summary.yaml），按父单元 units 顺序；排在前序作品摘要之后。
	if u.Type == "scene" && u.Parent != nil {
		sib := u.Parent.Units
		for _, c := range sib[:slices.Index(sib, u)] {
			if c.Type != "scene" {
				continue
			}
			sum, ps := series.LoadSummary(root, w, c)
			if len(ps) > 0 {
				// 前序场景的摘要读不出来不让本场景的上下文包失败：降为 warning 并跳过（同系列摘要的处理）。
				for _, q := range ps {
					q.Severity = workspace.SeverityWarning
					warnings = append(warnings, q)
				}
				continue
			}
			if sum == nil {
				warnings = append(warnings, workspace.Problem{Code: CodeSummaryMissing, Severity: workspace.SeverityWarning,
					Message: fmt.Sprintf("前序场景 %s 还没有摘要（Summary）；在它验收后用 wa-cli summary set %s 写入，本场景的上下文才会带出前情", c.Ref, c.Ref)})
				continue
			}
			v := SceneSummary{Unit: c.Ref, Text: sum.Text, Stale: sum.Stale}
			p.PriorScenes = append(p.PriorScenes, v)
			ref := c.Ref
			add(item{part: PartSceneSummary, id: ref, body: v.markdown(), drop: func() {
				p.PriorScenes = slices.DeleteFunc(p.PriorScenes, func(x SceneSummary) bool { return x.Unit == ref })
			}})
		}
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
	if s != nil {
		// 系列素材排在作品自己引用的素材之后：预算不够时先裁剪系列的。
		for _, id := range s.Sources {
			if !slices.Contains(ids, id) {
				ids = append(ids, id)
			}
		}
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

	if u.Type == "scene" {
		// scene 单元按 context-trim 的场景优先级整体重排（稳定排序：同 part 保持构造顺序）；非 scene 单元保持
		// 既有顺序（本变更的 unchanged behavior）。
		rank := map[string]int{PartOutline: 0, PartBrief: 1, PartSection: 2, PartState: 3, PartKnowledge: 4,
			PartReaderOnly: 5, PartReveal: 6, PartThread: 7, PartPriorEnding: 8, PartChapSummary: 9, PartPartSummary: 10,
			PartBookSummary: 11, PartFeedback: 12, PartDecision: 13, PartFact: 14, PartSummary: 15, PartSceneSummary: 16,
			PartRules: 17, PartGlossary: 18, PartBannedTerms: 19, PartStyle: 20, PartSource: 21, PartNeighbor: 22}
		slices.SortStableFunc(items, func(a, b item) int { return rank[a.part] - rank[b.part] })
	}
	p.fit(items)
	return p, warnings
}

// sceneItems 组装场景单元的出场人物、视角人物知情、揭示边界与未回收伏笔内容项，按 context-trim 的优先级（人物
// 设定在前、视角人物已知与读者已知人物未知其次、揭示边界再次、未回收伏笔在后）：出场人物按场景大纲 characters
// 声明顺序去重后，取 canon.yaml 中 name 相同的 character 条目（同名的按条目顺序都保留）；知情内容项按场景大纲
// 第一个声明 pov 的大纲节解析视角人物，重放 knowledge.yaml（knowledge 主规格的 knowledge-at 语义）；揭示边界为
// 本单元大纲各节的 reveal；未回收伏笔为伏笔台账（Foreshadowing Ledger）中 open 且与本场景契约相关的条目
// （threads 主规格 scene-context）。canon.yaml、knowledge.yaml 或 threads.yaml 存在但不通过校验时返回阻止组装的
// 问题；声明的人物或视角人物没有同名条目时追加 warning（作品没有 canon.yaml 时不提示）。
func sceneItems(root string, w *work.Work, u *work.Unit, own *outline.Outline, p *Pack, warnings *[]workspace.Problem) ([]item, []workspace.Problem) {
	var names []string
	pov := ""
	if own != nil {
		for _, sec := range own.Sections {
			names = append(names, sec.Characters...)
			if pov == "" && sec.Pov != "" {
				pov = sec.Pov
			}
		}
	}
	names = dedupe(names)
	// 设定集只在需要解析（声明了出场人物或视角人物）时加载一次（workspace-artifacts/load-once-per-command），
	// 出场人物匹配与视角人物解析、知情条目的 text 复用同一份；两者都未声明时留给 knowledge.For 按需加载。
	var canonEntries map[string]canon.Entry
	var entries []canon.Entry
	if len(names) > 0 || pov != "" {
		var ps []workspace.Problem
		scoped, sps := canon.Resolve(root, w)
		if sps != nil {
			entries, ps = nil, sps
		} else if len(scoped) == 0 {
			entries, ps = nil, nil // 没有任何设定条目：与没有 canon.yaml 一致，不提示
		} else {
			entries = make([]canon.Entry, len(scoped))
			for i, se := range scoped {
				entries[i] = se.Entry
			}
			ps = nil
		}
		var fatal []workspace.Problem
		for _, q := range ps {
			if q.Severity == workspace.SeverityError {
				fatal = append(fatal, q)
			} else {
				*warnings = append(*warnings, q)
			}
		}
		if fatal != nil {
			return nil, fatal
		}
		canonEntries = make(map[string]canon.Entry, len(entries))
		for _, e := range entries {
			canonEntries[e.ID] = e
		}
	}
	var out []item
	// 出场人物与视角人物解析到的设定条目（按声明顺序去重），供状态快照重放过滤。
	var memberIDs []string
	if len(names) > 0 {
		// canon.yaml 不存在时 List 返回空：出场人物照常缺失，但不提示（scene-context）。
		if entries != nil {
			for _, name := range names {
				matched := false
				for _, e := range entries {
					if e.Type != canon.TypeCharacter || e.Name == nil || *e.Name != name {
						continue
					}
					matched = true
					if !slices.Contains(memberIDs, e.ID) {
						memberIDs = append(memberIDs, e.ID)
					}
					v := Character{ID: e.ID, Name: name, Text: e.Text, Protection: e.Protection}
					p.Characters = append(p.Characters, v)
					id := e.ID
					out = append(out, item{part: PartCharacter, id: id, body: v.markdown(), drop: func() {
						p.Characters = slices.DeleteFunc(p.Characters, func(x Character) bool { return x.ID == id })
					}})
				}
				if !matched {
					*warnings = append(*warnings, workspace.Problem{Code: CodeCharacterNotInCanon, Severity: workspace.SeverityWarning,
						Message: fmt.Sprintf("场景大纲声明的出场人物「%s」在设定集（canon.yaml）中没有同名 character 条目；用 wa-cli canon add 登记人物卡（Character Sheet）后，wa-cli context 才会带出其当前设定", name)})
				}
			}
		}
	}
	// 视角人物（POV）的知情与读者已知：knowledge.yaml 不存在时 For 返回空重放；存在但不通过校验时返回阻止组装
	// 的问题（同 threads.yaml 的处理）。
	rep, fatal := knowledge.For(root, w, u, canonEntries)
	if fatal != nil {
		return nil, fatal
	}
	povID := ""
	if pov != "" {
		for _, e := range entries {
			if e.Type == canon.TypeCharacter && e.Name != nil && *e.Name == pov {
				povID = e.ID
				break
			}
		}
		if povID == "" {
			*warnings = append(*warnings, workspace.Problem{Code: CodePOVNotInCanon, Severity: workspace.SeverityWarning,
				Message: fmt.Sprintf("场景契约的视角人物「%s」在设定集（canon.yaml）中没有同名 character 条目；wa-cli canon add 登记后，wa-cli context 才会带出视角人物已知与读者已知的条目", pov)})
		} else if !slices.Contains(memberIDs, povID) {
			memberIDs = append(memberIDs, povID)
		}
	}
	// 出场人物状态快照（scene-context 的 states）：state.yaml 不存在时 Snapshot 返回空；存在但不通过校验时
	// 返回阻止组装的问题（同 threads.yaml 的处理）；未声明 pov 时仍含 characters 解析的条目。
	snap, fatal := state.Snapshot(root, w, u)
	if fatal != nil {
		return nil, fatal
	}
	for _, it := range snap {
		if !slices.Contains(memberIDs, it.Entry) {
			continue
		}
		v := StateItem{Entry: it.Entry, Name: it.Name, State: it.State, Proposal: it.Proposal, Scene: it.Scene}
		p.States = append(p.States, v)
		entry := v.Entry
		out = append(out, item{part: PartState, id: entry, body: v.markdown(), drop: func() {
			p.States = slices.DeleteFunc(p.States, func(x StateItem) bool { return x.Entry == entry })
		}})
	}
	if povID != "" {
		known := map[string]bool{}
		for _, it := range rep.Known(povID) {
			known[it.Entry] = true
			v := KnowledgeItem{Entry: it.Entry, Name: it.Name, Event: it.Event, Scene: it.Scene}
			if c, ok := canonEntries[it.Entry]; ok {
				v.Text = c.Text
			}
			p.Knowledge = append(p.Knowledge, v)
			out = append(out, item{part: PartKnowledge, id: v.Entry, body: v.markdown(), drop: func() {
				p.Knowledge = slices.DeleteFunc(p.Knowledge, func(x KnowledgeItem) bool { return x.Entry == v.Entry })
			}})
		}
		// 读者已知、视角人物未知的条目（悬念素材，与揭示边界配合）。
		for _, it := range rep.Known(knowledge.Reader) {
			if known[it.Entry] {
				continue
			}
			v := KnowledgeItem{Entry: it.Entry, Name: it.Name, Event: it.Event, Scene: it.Scene}
			if c, ok := canonEntries[it.Entry]; ok {
				v.Text = c.Text
			}
			p.ReaderOnly = append(p.ReaderOnly, v)
			out = append(out, item{part: PartReaderOnly, id: v.Entry, body: v.markdown(), drop: func() {
				p.ReaderOnly = slices.DeleteFunc(p.ReaderOnly, func(x KnowledgeItem) bool { return x.Entry == v.Entry })
			}})
		}
	}
	if own != nil {
		for _, sec := range own.Sections {
			if sec.Reveal == "" {
				continue
			}
			v := RevealBoundary{Section: sec.ID, Text: sec.Reveal}
			p.Reveal = append(p.Reveal, v)
			id := sec.ID
			out = append(out, item{part: PartReveal, body: v.markdown(), drop: func() {
				p.Reveal = slices.DeleteFunc(p.Reveal, func(x RevealBoundary) bool { return x.Section == id })
			}})
		}
	}
	if its, fatal := threadItems(root, w, u, own, p, warnings); fatal != nil {
		return nil, fatal
	} else {
		out = append(out, its...)
	}
	return out, nil
}

// threadItems 组装未回收伏笔（Open Foreshadowing）内容项：伏笔台账中 open 且与本场景契约相关的条目——埋设在本
// 场景、计划回收于本场景，或本场景大纲节 recovers 引用。threads.yaml 不存在时 List 返回空，不提示；台账存在但不
// 通过校验时返回阻止组装的问题（同 canon.yaml 的处理）。
func threadItems(root string, w *work.Work, u *work.Unit, own *outline.Outline, p *Pack, warnings *[]workspace.Problem) ([]item, []workspace.Problem) {
	items, ps := threads.List(root, w, false)
	var fatal []workspace.Problem
	for _, q := range ps {
		if q.Severity == workspace.SeverityError {
			fatal = append(fatal, q)
		} else {
			*warnings = append(*warnings, q)
		}
	}
	if fatal != nil {
		return nil, fatal
	}
	recovers := map[string]bool{}
	if own != nil {
		for _, sec := range own.Sections {
			for _, id := range sec.Recovers {
				recovers[id] = true
			}
		}
	}
	var out []item
	for _, it := range items {
		if it.PlantedIn != u.Ref && it.PlannedRecover != u.Ref && !recovers[it.ID] {
			continue
		}
		v := Thread{ID: it.ID, Text: it.Text, PlantedIn: it.PlantedIn, PlannedRecover: it.PlannedRecover, Overdue: it.Overdue}
		p.Threads = append(p.Threads, v)
		id := v.ID
		out = append(out, item{part: PartThread, id: id, body: v.markdown(), drop: func() {
			p.Threads = slices.DeleteFunc(p.Threads, func(x Thread) bool { return x.ID == id })
		}})
	}
	return out, nil
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

// tailCharsOf 返回场景正文（去掉 HTML 注释、换行换成空格）末尾至多 PriorEndingChars 个字符（Prior Ending）。
func tailCharsOf(root string, w *work.Work, u *work.Unit) string {
	content, ok, ps := draft.Read(root, w, u)
	if len(ps) > 0 || !ok {
		return ""
	}
	text := oneLine(comments.ReplaceAllString(strings.ReplaceAll(string(content), "\r\n", "\n"), ""))
	r := []rune(strings.TrimSpace(text))
	if len(r) > PriorEndingChars {
		r = r[len(r)-PriorEndingChars:]
	}
	return string(r)
}

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
