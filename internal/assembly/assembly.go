// Package assembly 实现长篇组稿（Assembly）：按单元树先序把作品中 stage 为 done 的场景正文组成整书
// （书名、目录、部/章标题、去注释正文、验收摘要与按出处的 AI 参与说明），组稿前三类前置（未完成场景、未豁免
// 连续性发现、open 复查标记）任一存在即拒绝。输出路径检查与原子写入复用 internal/export。本包不调用大语言
// 模型（LLM）、不 import internal/cli。
package assembly

import (
	"bytes"
	"fmt"
	"os"
	"path/filepath"
	"strings"

	"github.com/kw12121212/writing-anything/internal/continuity"
	"github.com/kw12121212/writing-anything/internal/draft"
	"github.com/kw12121212/writing-anything/internal/export"
	"github.com/kw12121212/writing-anything/internal/impact"
	"github.com/kw12121212/writing-anything/internal/outline"
	"github.com/kw12121212/writing-anything/internal/schema"
	"github.com/kw12121212/writing-anything/internal/work"
	"github.com/kw12121212/writing-anything/internal/workspace"
	"go.yaml.in/yaml/v3"
)

// 组稿的错误码（Error Code）。已发布的错误码不得改名或复用。
const (
	CodeScenesOpen = "assemble.scenes-open"
	CodeContinuity = "assemble.continuity"
	CodeRechecks   = "assemble.rechecks"
)

// Formats。
const (
	FormatMarkdown = "markdown"
	FormatText     = "text"
)

// Chapter 是一章：标题与其场景正文（按先序）。
type Chapter struct {
	Title  string
	Scenes []Scene
}

// Scene 是一个已验收场景的正文。
type Scene struct {
	Ref  string
	Text string
}

// Part 是一部：标题与其章（按先序）。
type Part struct {
	Title    string
	Chapters []Chapter
}

// Book 是组装结果：书名、部列表与统计。
type Book struct {
	Title  string
	Parts  []Part
	Scenes int
}

// View 是 assemble 命令的输出形态。
type View struct {
	Work   string `json:"work"`
	Path   string `json:"path"`
	Format string `json:"format"`
	Scenes int    `json:"scenes"`
	Parts  int    `json:"parts"`
	Chars  int    `json:"chars"`
}

func errorAt(code, path, msg string) workspace.Problem {
	return workspace.Problem{Code: code, Severity: workspace.SeverityError, Path: path, Message: msg}
}

// outlineTitle 返回单元大纲首个节的 title；无大纲、无节或大纲不通过 Schema 时回退到单元 id。
func outlineTitle(root string, w *work.Work, u *work.Unit) string {
	rel := outline.Path(w.UnitDir(u))
	if _, ok, _ := workspace.ReadRegular(root, rel); ok {
		if doc, ps := workspace.Read(root, rel); doc != nil && len(ps) == 0 {
			if sps := schema.Named("outline").ValidateNode(doc); len(sps) == 0 {
				var g struct {
					Sections []outline.Section `json:"sections"`
				}
				catalogDecode(doc, &g)
				if len(g.Sections) > 0 && g.Sections[0].Title != "" {
					return g.Sections[0].Title
				}
			}
		}
	}
	return u.ID
}

// catalogDecode 经重新编码解码（assembly 内部小工具）。
func catalogDecode(doc *yaml.Node, out any) {
	b, err := workspace.Encode(doc)
	if err != nil {
		return
	}
	_ = yaml.NewDecoder(bytes.NewReader(b)).Decode(out)
}

// Collect 按单元树先序组装书稿：只收 stage 为 done 的场景正文（去 HTML 注释与作者批注）。
func Collect(root string, w *work.Work) *Book {
	book := &Book{Title: outlineTitle(root, w, w.Root)}
	// 遍历先序：部 → 章 → 场景。
	var walkPart func(p *work.Unit)
	walkPart = func(p *work.Unit) {
		part := Part{Title: outlineTitle(root, w, p)}
		for _, c := range p.Units {
			if c.Type == "chapter" {
				ch := Chapter{Title: outlineTitle(root, w, c)}
				for _, s := range c.Units {
					if s.Type == outline.SceneType && s.Stage == "done" {
						if content, ok, ps := draft.Read(root, w, s); len(ps) == 0 && ok {
							ch.Scenes = append(ch.Scenes, Scene{Ref: s.Ref, Text: string(export.StripComments(content))})
							book.Scenes++
						}
					}
				}
				part.Chapters = append(part.Chapters, ch)
			}
		}
		book.Parts = append(book.Parts, part)
	}
	for _, u := range w.Root.Units {
		if u.Type == "part" {
			walkPart(u)
		}
	}
	// 混合层级：根下直接挂章（无部）时归入一个匿名部，保证正文不丢。
	if len(book.Parts) == 0 {
		fallback := Part{Title: outlineTitle(root, w, w.Root)}
		for _, c := range w.Root.Units {
			if c.Type == "chapter" {
				ch := Chapter{Title: outlineTitle(root, w, c)}
				for _, s := range c.Units {
					if s.Type == outline.SceneType && s.Stage == "done" {
						if content, ok, ps := draft.Read(root, w, s); len(ps) == 0 && ok {
							ch.Scenes = append(ch.Scenes, Scene{Ref: s.Ref, Text: string(export.StripComments(content))})
							book.Scenes++
						}
					}
				}
				fallback.Chapters = append(fallback.Chapters, ch)
			}
		}
		if len(fallback.Chapters) > 0 {
			book.Parts = append(book.Parts, fallback)
		}
	}
	return book
}

// Gate 检查组稿前置：未完成场景、未豁免连续性发现与 open 复查标记，逐项返回诊断。
func Gate(root string, w *work.Work) []workspace.Problem {
	var problems []workspace.Problem
	var open []string
	for _, u := range w.Units() {
		if u.Type == outline.SceneType && u.Stage != "done" {
			open = append(open, u.Ref)
		}
	}
	if len(open) > 0 {
		shown := open
		if len(shown) > 5 {
			shown = append(append([]string{}, shown[:5]...), fmt.Sprintf("等 %d 项", len(open)))
		}
		problems = append(problems, errorAt(CodeScenesOpen, w.Path,
			"还有未完成的场景："+strings.Join(shown, "、")+"；全部场景验收并推进到 done 后才能组稿"))
	}
	// 连续性：只读阻塞计算（不生成决策），按场景过滤 error 级发现；系列成员作品再算跨作品发现
	//（series-continuity-review 主规格）。
	for _, u := range w.Units() {
		if u.Type != outline.SceneType {
			continue
		}
		blockers, fatal := continuity.Blockers(root, w, u)
		if len(fatal) > 0 {
			problems = append(problems, errorAt(CodeContinuity, "",
				"连续性台账无法加载："+fatal[0].Message))
			continue
		}
		for _, b := range blockers {
			problems = append(problems, errorAt(CodeContinuity, b.Path,
				fmt.Sprintf("场景 %s 的连续性发现 %s：%s", u.Ref, b.Code, b.Message)))
		}
	}
	if seriesWorks := seriesMembers(root, w); len(seriesWorks) > 1 {
		if findings, _ := continuity.RunSeries(root, seriesWorks); len(findings) > 0 {
			for _, f := range findings {
				problems = append(problems, errorAt(CodeContinuity, "",
					fmt.Sprintf("[系列] 场景 %s 的跨作品连续性发现 %s：%s", f.Scene, f.Code, f.Message)))
			}
		}
		// 组稿关卡的复查标记扩展到系列内全部作品（message 标明作品）。
		var items []string
		for _, sw := range seriesWorks {
			for _, m := range impact.OpenMarks(root, sw.ID) {
				items = append(items, fmt.Sprintf("%s ← %s", m.Scene, m.Target))
			}
		}
		if len(items) > 0 {
			shown := items
			if len(shown) > 5 {
				shown = append(append([]string{}, shown[:5]...), fmt.Sprintf("等 %d 项", len(items)))
			}
			problems = append(problems, errorAt(CodeRechecks, "",
				"系列内存在 open 复查标记（Recheck Mark）："+strings.Join(shown, "、")))
		}
	}
	if marks := impact.OpenMarks(root, w.ID); len(marks) > 0 {
		var items []string
		for _, m := range marks {
			items = append(items, fmt.Sprintf("%s ← %s", m.Scene, m.Target))
		}
		shown := items
		if len(shown) > 5 {
			shown = append(append([]string{}, shown[:5]...), fmt.Sprintf("等 %d 项", len(items)))
		}
		problems = append(problems, errorAt(CodeRechecks, impact.Dir+"/"+w.ID+".yaml",
			"存在 open 复查标记（Recheck Mark）："+strings.Join(shown, "、")+"；复查通过或作者确认后 wa-cli impact clear 解除"))
	}
	return problems
}

// RenderMarkdown 渲染 Markdown 书稿：书名、验收摘要、目录（锚点）、部/章标题与正文。
func RenderMarkdown(b *Book) string {
	var s strings.Builder
	fmt.Fprintf(&s, "# %s\n\n", b.Title)
	fmt.Fprintf(&s, "> 已验收场景：%d 个\n\n", b.Scenes)
	// 目录。
	s.WriteString("## 目录\n\n")
	for _, p := range b.Parts {
		if len(p.Chapters) == 0 {
			continue
		}
		anchor := anchorOf("part-" + p.Title)
		fmt.Fprintf(&s, "- [%s](#%s)\n", p.Title, anchor)
		for _, c := range p.Chapters {
			fmt.Fprintf(&s, "  - [%s](#%s)\n", c.Title, anchorOf("chapter-"+p.Title+"-"+c.Title))
		}
	}
	s.WriteString("\n")
	for _, p := range b.Parts {
		if len(p.Chapters) == 0 {
			continue
		}
		fmt.Fprintf(&s, "# %s\n\n", p.Title)
		for _, c := range p.Chapters {
			fmt.Fprintf(&s, "## %s\n\n", c.Title)
			for i, sc := range c.Scenes {
				if i > 0 {
					s.WriteString("\n---\n\n")
				}
				s.WriteString(sc.Text)
				if !strings.HasSuffix(sc.Text, "\n") {
					s.WriteString("\n")
				}
			}
			s.WriteString("\n")
		}
	}
	return s.String()
}

// RenderText 渲染纯文本书稿（标题为文本行、目录无链接）。
func RenderText(b *Book) string {
	var s strings.Builder
	fmt.Fprintf(&s, "《%s》\n\n", b.Title)
	fmt.Fprintf(&s, "已验收场景：%d 个\n\n", b.Scenes)
	s.WriteString("目录\n\n")
	for _, p := range b.Parts {
		if len(p.Chapters) == 0 {
			continue
		}
		fmt.Fprintf(&s, "%s\n", p.Title)
		for _, c := range p.Chapters {
			fmt.Fprintf(&s, "  %s\n", c.Title)
		}
	}
	s.WriteString("\n")
	for _, p := range b.Parts {
		if len(p.Chapters) == 0 {
			continue
		}
		fmt.Fprintf(&s, "【%s】\n\n", p.Title)
		for _, c := range p.Chapters {
			fmt.Fprintf(&s, "—— %s ——\n\n", c.Title)
			for i, sc := range c.Scenes {
				if i > 0 {
					s.WriteString("\n* * *\n\n")
				}
				s.WriteString(sc.Text)
				if !strings.HasSuffix(sc.Text, "\n") {
					s.WriteString("\n")
				}
			}
			s.WriteString("\n")
		}
	}
	return s.String()
}

// anchorOf 生成 GitHub 风格的标题锚点。
func anchorOf(title string) string {
	var b strings.Builder
	for _, r := range strings.ToLower(title) {
		switch {
		case r >= 'a' && r <= 'z', r >= '0' && r <= '9':
			b.WriteRune(r)
		case r == ' ' || r == '-':
			b.WriteRune('-')
		}
	}
	return b.String()
}

// DisclosureText 按整书场景生成 AI 参与说明文本（没有配置层模板时为空）；供命令层把说明追加到书稿末尾。
func DisclosureText(root string, w *work.Work, cfg *export.DisclosureConfig) string {
	if cfg == nil {
		return ""
	}
	var units []*work.Unit
	for _, u := range w.Units() {
		if u.Type == outline.SceneType && u.Stage == "done" {
			units = append(units, u)
		}
	}
	// export 的 disclose 未导出：此处按同一来源（写入记录的出处）复用 export.DisclosureOf（若导出）或
	// 交由命令层生成。当前以 export 导出的 Disclosure 函数为准。
	if d := export.DisclosureOf(root, w, units, cfg); d != nil {
		return d.Text
	}
	return ""
}

// Assemble 组稿并写出交付文件：关卡在前，输出路径检查与原子写入复用 internal/export；footer 为按出处生成的
// AI 参与说明（调用方经 export 的说明机制生成，可为空）。
func Assemble(root string, w *work.Work, format, output, footer string) (*View, []string, []workspace.Problem) {
	if format == "" {
		format = FormatMarkdown
	}
	if format != FormatMarkdown && format != FormatText {
		return nil, nil, []workspace.Problem{{Code: "input.invalid-value", Severity: workspace.SeverityError, Field: "format",
			Message: "格式 " + format + " 不支持；可用格式：markdown、text"}}
	}
	if problems := Gate(root, w); len(problems) > 0 {
		return nil, nil, problems
	}
	book := Collect(root, w)
	var body string
	if format == FormatText {
		body = RenderText(book)
	} else {
		body = RenderMarkdown(book)
	}
	if footer != "" {
		body += "\n---\n\n" + footer + "\n"
	}
	if output == "" {
		ext := ".md"
		if format == FormatText {
			ext = ".txt"
		}
		output = export.ExportsDir + "/" + w.ID + "-manuscript" + ext
	}
	rel, changed, ps := export.WriteOutput(root, output, []byte(body), "export.invalid-output")
	if len(ps) > 0 {
		return nil, nil, ps
	}
	parts := 0
	for _, p := range book.Parts {
		if len(p.Chapters) > 0 {
			parts++
		}
	}
	return &View{Work: w.ID, Path: rel, Format: format, Scenes: book.Scenes, Parts: parts, Chars: len([]rune(body))}, changed, nil
}

// seriesMembers 返回作品所属系列的成员作品（按 works 顺序；不属于系列时只含自身）；系列文件坏时降级为自身。
func seriesMembers(root string, w *work.Work) []*work.Work {
	entries, err := os.ReadDir(filepath.Join(root, filepath.FromSlash(".writing-anything/series")))
	if err != nil {
		return []*work.Work{w}
	}
	for _, e := range entries {
		if e.IsDir() || strings.HasPrefix(e.Name(), ".") || !strings.HasSuffix(e.Name(), ".yaml") {
			continue
		}
		rel := ".writing-anything/series/" + e.Name()
		doc, ps := workspace.Read(root, rel)
		if doc == nil || len(ps) > 0 {
			continue
		}
		var f struct {
			Works []string `json:"works"`
		}
		catalogDecode(doc, &f)
		for _, id := range f.Works {
			if id != w.ID {
				continue
			}
			var out []*work.Work
			for _, wid := range f.Works {
				if ww, wps := work.Load(root, wid); ww != nil && len(wps) == 0 {
					out = append(out, ww)
				}
			}
			if len(out) == 0 {
				return []*work.Work{w}
			}
			return out
		}
	}
	return []*work.Work{w}
}
