package ctxpack

import (
	"fmt"
	"maps"
	"slices"
	"strings"

	"github.com/kw12121212/writing-anything/internal/canon"
	"github.com/kw12121212/writing-anything/internal/feedback"
	"github.com/kw12121212/writing-anything/internal/outline"
	"github.com/kw12121212/writing-anything/internal/series"
)

// partTitles 是各部分在 Markdown 中的二级标题；标题不计入预算。
var partTitles = map[string]string{
	PartBrief:        "## 写作简报（Brief）",
	PartOutline:      "## 本单元大纲（Outline）",
	PartSection:      "## 本节大纲条目",
	PartCharacter:    "## 出场人物（Characters）",
	PartReveal:       "## 揭示边界（Reveal Boundary）",
	PartThread:       "## 未回收伏笔（Open Foreshadowing）",
	PartFeedback:     "## 未处理的作者反馈（Feedback）",
	PartDecision:     "## 待决的决策（Decision Queue）",
	PartFact:         "## 系列（Series）",
	PartSummary:      "## 系列（Series）",
	PartSceneSummary: "## 前序场景（Prior Scenes）",
	PartRules:        "## 规范要点",
	PartGlossary:     "## 规范要点",
	PartBannedTerms:  "## 规范要点",
	PartStyle:        "## 规范要点",
	PartSource:       "## 素材原文",
	PartNeighbor:     "## 相邻节（Neighbor Section）",
}

func (b *Brief) markdown() string {
	var s strings.Builder
	fmt.Fprintf(&s, "来自单元 %s\n", b.Unit)
	line := func(name, v string) {
		if v != "" {
			fmt.Fprintf(&s, "- %s：%s\n", name, v)
		}
	}
	line("受众（Audience）", b.Audience)
	line("写作目的（Purpose）", b.Purpose)
	if b.Length != nil {
		line("字数范围", fmt.Sprintf("%d-%d", b.Length.Min, b.Length.Max))
	}
	line("禁区（Off-limits）", strings.Join(b.Avoid, "；"))
	if len(b.KeyMessages) > 0 {
		s.WriteString("- 核心观点（Key Message）：\n")
		for _, m := range b.KeyMessages {
			fmt.Fprintf(&s, "  - %s：%s\n", m.ID, m.Text)
		}
	}
	return s.String()
}

func outlineMarkdown(o *outline.Outline) string {
	var s strings.Builder
	for i, sec := range o.Sections {
		detail := "覆盖 " + strings.Join(sec.Covers, "、")
		if len(sec.Covers) == 0 {
			detail = "不覆盖核心观点：" + sec.Reason
		}
		fmt.Fprintf(&s, "%d. %s %s（%s）\n", i+1, sec.ID, sec.Title, detail)
	}
	if len(o.Sections) == 0 {
		s.WriteString("（没有大纲节）\n")
	}
	return s.String()
}

func (sec *Section) markdown() string {
	var s strings.Builder
	fmt.Fprintf(&s, "- 节：%s %s\n", sec.ID, sec.Title)
	if sec.Reason != "" {
		fmt.Fprintf(&s, "- 不覆盖核心观点的理由：%s\n", sec.Reason)
	}
	for _, m := range sec.KeyMessages {
		fmt.Fprintf(&s, "- 覆盖核心观点 %s：%s\n", m.ID, m.Text)
	}
	if len(sec.Uses) > 0 {
		fmt.Fprintf(&s, "- 使用素材：%s\n", strings.Join(sec.Uses, "、"))
	}
	return s.String()
}

func feedbackMarkdown(f feedback.Item) string {
	s := fmt.Sprintf("- %s [%s，%s]：%s\n", f.ID, f.Kind, f.Status, oneLine(f.Text))
	if f.Anchor != nil && f.Anchor.Quote != "" {
		s += "  原文：" + oneLine(f.Anchor.Quote) + "\n"
	}
	return s
}

func (d Decision) markdown() string {
	kind := "非阻塞"
	if d.Blocking {
		kind = "阻塞"
	}
	s := fmt.Sprintf("- %s（%s）：%s\n", d.ID, kind, oneLine(d.Question))
	for _, o := range d.Options {
		s += fmt.Sprintf("  - %s. %s\n", o.ID, oneLine(o.Text))
	}
	return s
}

// items 返回规范要点的四个内容项；某项被整项裁剪时清空 n 中对应的字段。
func (n Norms) items(p *Norms) []item {
	var out []item
	if len(n.Rules) > 0 {
		var s strings.Builder
		s.WriteString("### 规则（Rule）\n")
		for _, r := range n.Rules {
			var params []string
			for _, k := range slices.Sorted(maps.Keys(r.Params)) {
				params = append(params, fmt.Sprintf("%s=%v", k, r.Params[k]))
			}
			line := fmt.Sprintf("- %s：%s，%s", r.ID, r.Type, r.Severity)
			if len(params) > 0 {
				line += "，" + strings.Join(params, " ")
			}
			s.WriteString(line + "\n")
		}
		out = append(out, item{part: PartRules, body: s.String(), drop: func() { p.Rules = nil }})
	}
	if len(n.Glossary) > 0 {
		var s strings.Builder
		s.WriteString("### 术语表（Glossary）\n")
		for _, t := range n.Glossary {
			line := "- " + t.Term.Term
			if t.English != "" {
				line += "（" + t.English + "）"
			}
			if len(t.Avoid) > 0 {
				line += "；不要写作 " + strings.Join(t.Avoid, "、")
			}
			s.WriteString(line + "\n")
		}
		out = append(out, item{part: PartGlossary, body: s.String(), drop: func() { p.Glossary = nil }})
	}
	if len(n.BannedTerms) > 0 {
		var s strings.Builder
		s.WriteString("### 禁用词（Banned Term）\n")
		for _, t := range n.BannedTerms {
			line := "- " + t.BannedTerm.Term
			if t.Replacement != "" {
				line += " → " + t.Replacement
			}
			s.WriteString(line + "\n")
		}
		out = append(out, item{part: PartBannedTerms, body: s.String(), drop: func() { p.BannedTerms = nil }})
	}
	if len(n.Style) > 0 {
		var s strings.Builder
		s.WriteString("### 文风（Style）\n")
		for _, t := range n.Style {
			s.WriteString("- " + t + "\n")
		}
		out = append(out, item{part: PartStyle, body: s.String(), drop: func() { p.Style = nil }})
	}
	return out
}

func (n Neighbor) markdown() string {
	pos := "上一节"
	if n.Position == "next" {
		pos = "下一节"
	}
	s := fmt.Sprintf("- %s %s", pos, n.Unit)
	if n.Title != "" {
		s += "「" + n.Title + "」"
	}
	if n.Excerpt != nil {
		s += "：" + *n.Excerpt
	}
	return s + "\n"
}

func (c Character) markdown() string {
	pin := ""
	if c.Protection == canon.ProtectionPinned {
		pin = "［作者钦定］"
	}
	return fmt.Sprintf("- %s %s%s：%s\n", c.ID, c.Name, pin, oneLine(c.Text))
}

func (r RevealBoundary) markdown() string {
	return fmt.Sprintf("- 节 %s 不能提前透露：%s\n", r.Section, oneLine(r.Text))
}

func (t Thread) markdown() string {
	note := ""
	if t.Overdue {
		note = "（已超过计划回收位置）"
	}
	return fmt.Sprintf("- %s%s：埋设于 %s，计划回收于 %s：%s\n", t.ID, note, t.PlantedIn, t.PlannedRecover, oneLine(t.Text))
}

func (s SceneSummary) markdown() string {
	note := ""
	if s.Stale {
		note = "（已过期：正文在摘要之后被修改）"
	}
	// 摘要按引用块输出，其中以 # 开头的行不会变成上下文包的标题。
	var b strings.Builder
	for _, line := range strings.Split(strings.TrimSpace(s.Text), "\n") {
		b.WriteString("> " + line + "\n")
	}
	return fmt.Sprintf("- 前序场景 %s 的摘要（Summary）%s：\n\n%s\n", s.Unit, note, b.String())
}

// Markdown 返回上下文包的 Markdown 文本：单元信息头、按优先级顺序保留的内容项与裁剪报告。
func (p *Pack) Markdown() string {
	var s strings.Builder
	fmt.Fprintf(&s, "# 上下文包（Context Pack）：%s\n\n", p.Unit)
	fmt.Fprintf(&s, "单元（Unit）%s（类型 %s，阶段（Stage）%s，文体（Genre）%s）；上下文预算（Context Budget）%v 字符（%s），已用 %d 字符\n",
		p.Unit, p.Type, p.Stage, p.Genre, p.Budget.Value, p.Budget.Source, p.Used)
	title := ""
	for _, it := range p.items {
		if t := partTitles[it.part]; t != title {
			fmt.Fprintf(&s, "\n%s\n\n", t)
			title = t
			if (it.part == PartFact || it.part == PartSummary) && p.Series != nil {
				fmt.Fprintf(&s, "本作品属于系列 %s（%s）\n\n", p.Series.ID, oneLine(p.Series.Title))
			}
		}
		s.WriteString(it.body)
	}
	if len(p.Trimmed) > 0 {
		s.WriteString("\n## 被裁剪的内容\n\n")
		for _, t := range p.Trimmed {
			name := t.Part
			if t.ID != "" {
				name += " " + t.ID
			}
			if t.KeptChars > 0 {
				fmt.Fprintf(&s, "- %s：原 %d 字符，截断为 %d 字符\n", name, t.Chars, t.KeptChars)
			} else {
				fmt.Fprintf(&s, "- %s：%d 字符，已裁剪\n", name, t.Chars)
			}
		}
	}
	return s.String()
}

func factMarkdown(f series.Fact) string {
	src := ""
	if len(f.Sources) > 0 {
		src = "（素材 " + strings.Join(f.Sources, "、") + "）"
	}
	return fmt.Sprintf("- 事实 %s：%s%s\n", f.ID, oneLine(f.Text), src)
}

func (s Summary) markdown() string {
	note := ""
	if s.Stale {
		note = "（已过期：正文在摘要之后被修改）"
	}
	// 摘要按引用块输出，其中以 # 开头的行不会变成上下文包的标题。
	var b strings.Builder
	for _, line := range strings.Split(strings.TrimSpace(s.Text), "\n") {
		b.WriteString("> " + line + "\n")
	}
	return fmt.Sprintf("- 前序作品 %s 的摘要（Summary）%s：\n\n%s\n", s.Work, note, b.String())
}
