package export

import (
	"bytes"
	"html/template"
	"regexp"
	"sort"
	"strings"
	"unicode"
	"unicode/utf8"

	"github.com/kw12121212/writing-anything/internal/catalog"
	"github.com/kw12121212/writing-anything/internal/mdscan"
)

// 公众号 HTML（wechat-html）：把 Markdown 正文转成只含内联样式（Inline Style）的 HTML 片段。先把正文解析成节点树，再用 html/template
// 按节点种类输出固定的标签（AllowedTags），文字一律由模板转义，style 只取通过 catalog.SafeCSS 的样式主题取值
// （workspace-artifacts/generated-html）。不对已生成的标记再做文字替换，也不在正文中插入标记字符。

// AllowedTags 是 wechat-html 输出允许的标签。
var AllowedTags = []string{"section", "h1", "h2", "h3", "h4", "p", "strong", "em", "code", "pre", "blockquote", "ul", "ol", "li", "hr", "br", "sup"}

// 输入规模的界限（workspace-artifacts/input-limits）：引用块与行内强调的嵌套层数；链接文字与地址的长度上限写在 linkAt 中。
const (
	maxQuoteDepth    = 8
	maxEmphasisDepth = 16
	linkWindow       = 12000 // linkAt 可能匹配的最大字节数：文字与标题各至多 1000 个字符（每个至多 4 字节或转义的 2 字节）、地址至多 1000 字节
)

// Image 是导出中的一张配图（Illustration）。
type Image struct {
	Index int
	Alt   string
	Src   string
	Title string
}

var (
	headingRE = regexp.MustCompile(`^ {0,3}(#{1,6})(?:[ \t]+(.*?))?(?:[ \t]+#+)?[ \t]*$`)
	hrRE      = regexp.MustCompile(`^ {0,3}(?:(?:-[ \t]*){3,}|(?:\*[ \t]*){3,}|(?:_[ \t]*){3,})$`)
	quoteRE   = regexp.MustCompile(`^ {0,3}> ?`)
	listRE    = regexp.MustCompile(`^ {0,3}([-*+]|\d{1,9}[.)])[ \t]+(.*)$`)
	fenceOpen = regexp.MustCompile("^ {0,3}(`{3,}|~{3,})")
	commentRE = regexp.MustCompile(`(?s)<!--.*?-->`)
	// linkAt 在 [ 或 ![ 处匹配链接或图片；文字、地址与标题的长度有上限（最长不超过 linkWindow 字节），每次只在这个窗口内尝试，耗时有界。
	linkAt = regexp.MustCompile(`^(!?)\[((?:\\.|[^\[\]\\]){0,1000})\]\(([^()\s]{0,1000})(?:[ \t]+"([^"]{0,1000})")?\)`)
)

// node 是渲染树中的一个节点。Kind 决定输出的标签：块为 section、h1–h4、p、pre、blockquote、ul、ol、li、hr、figure 与 references，
// 行内为 text、code、strong、em、br 与 sup。
type node struct {
	Kind     string
	Style    template.CSS
	Text     string   // text、code、pre、sup 的文字与 figure 的占位文字
	Caption  string   // figure 的说明
	Lines    []string // references 的各行
	Children []*node
}

type wechat struct {
	theme  map[string]string
	images []Image
	links  []string
	index  map[string]int // 链接地址 → 编号
}

// style 返回元素 key 的样式；不通过 catalog.SafeCSS 的取值不输出（合并结果已经过 Schema 校验，这里再挡一层）。
func (r *wechat) style(key string) template.CSS {
	if v := r.theme[key]; catalog.SafeCSS(v) {
		return template.CSS(v)
	}
	return ""
}

func (r *wechat) el(kind, key string, children ...*node) *node {
	return &node{Kind: kind, Style: r.style(key), Children: children}
}

// RenderWechat 把 Markdown 正文 md（换行为 LF）按样式主题 theme（元素名 → CSS 声明）渲染为公众号 HTML，返回 HTML 与配图。footer 非空时
// 作为 AI 参与说明追加在文末（样式同参考链接）。
func RenderWechat(md string, theme map[string]string, footer string) (string, []Image) {
	r := &wechat{theme: theme, index: map[string]int{}}
	root := r.el("section", "wrapper", r.blocks(strings.Split(md, "\n"), 0)...)
	if len(r.links) > 0 {
		ref := r.el("references", "references")
		for i, l := range r.links {
			ref.Lines = append(ref.Lines, "["+itoa(i+1)+"] "+l)
		}
		root.Children = append(root.Children, ref)
	}
	if footer != "" {
		root.Children = append(root.Children, r.el("p", "references", &node{Kind: "text", Text: footer}))
	}
	var b bytes.Buffer
	if err := page.ExecuteTemplate(&b, "block", root); err != nil {
		panic(err) // 模板与节点种类是固定的，执行不会失败
	}
	return b.String() + "\n", r.images
}

func itoa(n int) string {
	if n < 10 {
		return string(rune('0' + n))
	}
	return itoa(n/10) + string(rune('0'+n%10))
}

// blocks 解析一组行；depth 为所在引用块的嵌套层数，超过 maxQuoteDepth 时 > 按文字处理。
func (r *wechat) blocks(lines []string, depth int) []*node {
	fence := mdscan.FenceLines(lines)
	var out []*node
	for i := 0; i < len(lines); {
		line := lines[i]
		switch {
		case fence[i]:
			// 围栏代码块：到结束行为止（未闭合时到正文末尾），结束行不输出。
			j, closed := i+1, false
			for j < len(lines) && fence[j] {
				j++
				if closes(lines[i], lines[j-1]) {
					closed = true
					break
				}
			}
			body := lines[i+1 : j]
			if closed {
				body = body[:len(body)-1]
			}
			n := r.el("pre", "pre")
			n.Text = strings.Join(body, "\n")
			out = append(out, n)
			i = j
		case strings.TrimSpace(line) == "":
			i++
		case headingRE.MatchString(line):
			m := headingRE.FindStringSubmatch(line)
			tag := "h" + itoa(min(len(m[1]), 4))
			out = append(out, r.el(tag, tag, r.inline(m[2])...))
			i++
		case hrRE.MatchString(line):
			out = append(out, r.el("hr", "hr"))
			i++
		case depth < maxQuoteDepth && quoteRE.MatchString(line):
			var inner []string
			for i < len(lines) && !fence[i] && quoteRE.MatchString(lines[i]) {
				inner = append(inner, quoteRE.ReplaceAllString(lines[i], ""))
				i++
			}
			out = append(out, r.el("blockquote", "blockquote", r.blocks(inner, depth+1)...))
		case listRE.MatchString(line):
			var n *node
			n, i = r.list(lines, fence, i)
			out = append(out, n)
		default:
			var para []string
			for i < len(lines) && !fence[i] && strings.TrimSpace(lines[i]) != "" && (len(para) == 0 || !startsBlock(lines[i], depth)) {
				para = append(para, lines[i])
				i++
			}
			if n := r.paragraph(para); n != nil {
				out = append(out, n)
			}
		}
	}
	return out
}

// closes 报告 line 是否为开头行 open 的围栏的结束行：同一字符、长度不短于开头、其后只有空白。
func closes(open, line string) bool {
	m := fenceOpen.FindStringSubmatch(open)
	if m == nil {
		return false
	}
	t := strings.TrimLeft(line, " ")
	if len(line)-len(t) > 3 {
		return false
	}
	run := len(t) - len(strings.TrimLeft(t, m[1][:1]))
	return run >= len(m[1]) && strings.TrimSpace(t[run:]) == ""
}

// startsBlock 报告 line 是否开始一个新的块（标题、分隔线、引用、列表或围栏），从而结束当前段落。
func startsBlock(line string, depth int) bool {
	return headingRE.MatchString(line) || hrRE.MatchString(line) || depth < maxQuoteDepth && quoteRE.MatchString(line) ||
		listRE.MatchString(line) || fenceOpen.MatchString(line)
}

// list 解析从第 i 行开始的列表，返回列表节点与列表之后的行号。只支持一层：缩进的后续行与不开始新块的行并入上一项；同类列表项
// 之间的空行不结束列表。
func (r *wechat) list(lines []string, fence []bool, i int) (*node, int) {
	ordered := isOrdered(listRE.FindStringSubmatch(lines[i])[1])
	var items [][]string
	for i < len(lines) && !fence[i] {
		line := lines[i]
		if m := listRE.FindStringSubmatch(line); m != nil {
			if isOrdered(m[1]) != ordered {
				break
			}
			items = append(items, []string{m[2]})
			i++
			continue
		}
		if strings.TrimSpace(line) == "" {
			k := i
			for k < len(lines) && strings.TrimSpace(lines[k]) == "" {
				k++
			}
			if k < len(lines) && !fence[k] {
				if m := listRE.FindStringSubmatch(lines[k]); m != nil && isOrdered(m[1]) == ordered {
					i = k
					continue
				}
			}
			break
		}
		if startsBlock(line, 0) && !strings.HasPrefix(line, "  ") {
			break
		}
		items[len(items)-1] = append(items[len(items)-1], strings.TrimLeft(line, " \t"))
		i++
	}
	tag := "ul"
	if ordered {
		tag = "ol"
	}
	n := r.el(tag, tag)
	for _, it := range items {
		n.Children = append(n.Children, r.el("li", "li", r.inline(strings.Join(it, "\n"))...))
	}
	return n, i
}

func isOrdered(marker string) bool { return marker[0] >= '0' && marker[0] <= '9' }

// paragraph 解析一个段落；独占一段的图片输出为块级的配图占位；只有注释的段落不输出。
func (r *wechat) paragraph(lines []string) *node {
	text := strings.TrimSpace(removeComments(strings.Join(lines, "\n")))
	if text == "" {
		return nil
	}
	if m := linkAt.FindStringSubmatch(text); m != nil && m[1] == "!" && len(m[0]) == len(text) {
		img := r.image(m)
		caption := img.Title
		if caption == "" {
			caption = img.Src
		}
		f := r.el("figure", "figure", &node{Style: r.style("p")}, &node{Style: r.style("caption")})
		f.Text, f.Caption = "【配图 "+itoa(img.Index)+"】"+img.Alt, caption
		return f
	}
	return r.el("p", "p", r.inline(text)...)
}

func (r *wechat) image(m []string) Image {
	img := Image{Index: len(r.images) + 1, Alt: plain(m[2]), Src: unescapeMD(m[3]), Title: plain(m[4])}
	r.images = append(r.images, img)
	return img
}

// plain 把可能跨行的文字（图片的替代文字与标题）变成一行，并去掉反斜杠转义。
func plain(s string) string { return unescapeMD(strings.Join(strings.Fields(s), " ")) }

// removeComments 去掉行内代码之外的、已闭合的 HTML 注释（不跨越空行，因为段落之间已经分开）。
func removeComments(s string) string {
	spans := mdscan.CodeSpans(s)
	var b strings.Builder
	last := 0
	for _, m := range commentRE.FindAllStringIndex(s, -1) {
		if mdscan.InSpans(spans, m[0]) || m[0] < last {
			continue
		}
		b.WriteString(s[last:m[0]])
		last = m[1]
	}
	b.WriteString(s[last:])
	return b.String()
}

// inline 解析段落、标题或列表项的行内写法：行内代码、图片、链接、加粗与斜体；行与行之间为软换行或硬换行。
func (r *wechat) inline(s string) []*node {
	s = removeComments(s)
	var out []*node
	last := 0
	for _, sp := range mdscan.CodeSpans(s) {
		out = append(out, r.text(s[last:sp[0]])...)
		n := len(s[sp[0]:]) - len(strings.TrimLeft(s[sp[0]:], "`"))
		code := strings.ReplaceAll(s[sp[0]+n:sp[1]-n], "\n", " ")
		if len(code) >= 2 && code[0] == ' ' && code[len(code)-1] == ' ' && strings.TrimSpace(code) != "" {
			code = code[1 : len(code)-1]
		}
		c := r.el("code", "code")
		c.Text = code
		out = append(out, c)
		last = sp[1]
	}
	return breaks(append(out, r.text(s[last:])...))
}

// text 解析不含行内代码的文字：图片与链接，其余交给 emphasis。
func (r *wechat) text(s string) []*node {
	var out []*node
	start := 0
	for i := 0; i < len(s); i++ {
		switch s[i] {
		case '\\':
			i++ // 转义的字符不开始图片或链接
		case '[', '!':
			m := linkAt.FindStringSubmatch(s[i:min(len(s), i+linkWindow)])
			if m == nil || (s[i] == '!') != (m[1] == "!") {
				continue
			}
			out = append(out, r.emphasis(s[start:i], 0)...)
			if m[1] == "!" {
				img := r.image(m)
				out = append(out, &node{Kind: "text", Text: "【配图 " + itoa(img.Index) + "：" + img.Alt + "】"})
			} else {
				out = append(out, r.emphasis(m[2], 0)...)
				sup := r.el("sup", "footnote")
				sup.Text = "[" + itoa(r.link(unescapeMD(m[3]))) + "]"
				out = append(out, sup)
			}
			i += len(m[0]) - 1
			start = i + 1
		}
	}
	return append(out, r.emphasis(s[start:], 0)...)
}

// link 返回地址的脚注编号；同一个地址只编号一次。
func (r *wechat) link(url string) int {
	if k, ok := r.index[url]; ok {
		return k
	}
	r.links = append(r.links, url)
	r.index[url] = len(r.links)
	return len(r.links)
}

// delimiter 是强调的一种写法与其合法的结束位置（升序）。
type delimiter struct {
	mark   string
	kind   string // strong 或 em
	closer []int
}

// emphasis 解析加粗（**、__）与斜体（*、_）：从左到右找开头，在其后找最近的合法结束；找到时内部递归解析，找不到时开头按文字输出。
// 结束位置预先算好并二分查找，耗时与文字长度近似线性；嵌套达到 maxEmphasisDepth 层时不再识别强调。
func (r *wechat) emphasis(s string, depth int) []*node {
	if s == "" {
		return nil
	}
	if depth >= maxEmphasisDepth {
		return []*node{{Kind: "text", Text: unescapeMD(s)}}
	}
	ds := delimiters(s)
	var out []*node
	var buf strings.Builder
	flush := func() {
		if buf.Len() > 0 {
			out = append(out, &node{Kind: "text", Text: unescapeMD(buf.String())})
			buf.Reset()
		}
	}
	for i := 0; i < len(s); {
		if s[i] == '\\' && i+1 < len(s) {
			buf.WriteString(s[i : i+2])
			i += 2
			continue
		}
		matched := false
		for _, d := range ds {
			if !strings.HasPrefix(s[i:], d.mark) || !opens(s, i, d.mark) {
				continue
			}
			from := i + len(d.mark)
			k := sort.SearchInts(d.closer, from+1) // 内部至少一个字符
			if k == len(d.closer) {
				continue
			}
			end := d.closer[k]
			flush()
			out = append(out, r.el(d.kind, d.kind, r.emphasis(s[from:end], depth+1)...))
			i = end + len(d.mark)
			matched = true
			break
		}
		if !matched {
			_, size := utf8.DecodeRuneInString(s[i:])
			buf.WriteString(s[i : i+size])
			i += size
		}
	}
	flush()
	return out
}

// delimiters 返回 s 中各种强调写法与其合法的结束位置；两个字符的写法排在前面，先试。
func delimiters(s string) []delimiter {
	ds := []delimiter{{mark: "**", kind: "strong"}, {mark: "__", kind: "strong"}, {mark: "*", kind: "em"}, {mark: "_", kind: "em"}}
	for i := range ds {
		for j := 0; j+len(ds[i].mark) <= len(s); j++ {
			if strings.HasPrefix(s[j:], ds[i].mark) && closesAt(s, j, ds[i].mark) {
				ds[i].closer = append(ds[i].closer, j)
			}
		}
	}
	return ds
}

// opens 报告 s[i:] 处的 mark 能否开始强调：之前不是同一字符或反斜杠，之后不是空白或同一字符；_ 之前不能是字母、数字或 _。
func opens(s string, i int, mark string) bool {
	j := i + len(mark)
	if j >= len(s) || isSpace(s[j]) || s[j] == mark[0] || i > 0 && (s[i-1] == mark[0] || s[i-1] == '\\') {
		return false
	}
	if mark[0] != '_' || i == 0 {
		return true
	}
	prev, _ := utf8.DecodeLastRuneInString(s[:i])
	return !isWord(prev)
}

// closesAt 报告 s[j:] 处的 mark 能否结束强调：之前不是空白、同一字符或反斜杠，之后不是同一字符；_ 之后不能是字母、数字或 _。
func closesAt(s string, j int, mark string) bool {
	end := j + len(mark)
	if j == 0 || isSpace(s[j-1]) || s[j-1] == mark[0] || s[j-1] == '\\' || end < len(s) && s[end] == mark[0] {
		return false
	}
	if mark[0] != '_' || end == len(s) {
		return true
	}
	next, _ := utf8.DecodeRuneInString(s[end:])
	return !isWord(next)
}

func isSpace(c byte) bool { return c == ' ' || c == '\t' || c == '\n' }

// isWord 报告 c 是否属于单词（字母、数字或 _，包括汉字）：_ 在单词中间时不是强调。
func isWord(c rune) bool { return c == '_' || unicode.IsLetter(c) || unicode.IsDigit(c) }

// mdEscapable 是可以用反斜杠转义的字符。
const mdEscapable = "\\`*_{}[]()#+-.!>|~"

// unescapeMD 去掉反斜杠转义：\* 等为字符本身。
func unescapeMD(s string) string {
	if !strings.Contains(s, `\`) {
		return s
	}
	var b strings.Builder
	for i := 0; i < len(s); i++ {
		if s[i] == '\\' && i+1 < len(s) && strings.IndexByte(mdEscapable, s[i+1]) >= 0 {
			i++
		}
		b.WriteByte(s[i])
	}
	return b.String()
}

// breaks 把文字节点中的换行变成软换行或硬换行：行尾两个空格或反斜杠为 br，两个汉字之间直接相连，其他为一个空格。
func breaks(ns []*node) []*node {
	var out []*node
	for _, n := range ns {
		switch n.Kind {
		case "strong", "em":
			n.Children = breaks(n.Children)
			out = append(out, n)
		case "text":
			// 逐行写入，耗时与文字长度成正比。
			lines := strings.Split(n.Text, "\n")
			var b strings.Builder
			var last rune // 已写入文字的最后一个字符
			write := func(s string) {
				if s != "" {
					b.WriteString(s)
					last, _ = utf8.DecodeLastRuneInString(s)
				}
			}
			for k, line := range lines {
				if k > 0 {
					line = strings.TrimLeft(line, " \t")
				}
				if k == len(lines)-1 {
					write(line)
					break
				}
				if strings.HasSuffix(line, "  ") || strings.HasSuffix(line, `\`) {
					write(strings.TrimRight(strings.TrimSuffix(strings.TrimRight(line, " "), `\`), " "))
					out = append(out, &node{Kind: "text", Text: b.String()}, &node{Kind: "br"})
					b.Reset()
					last = 0
					continue
				}
				write(strings.TrimRight(line, " \t"))
				first, _ := utf8.DecodeRuneInString(strings.TrimLeft(lines[k+1], " \t"))
				if !(last >= 0x2e80 && first >= 0x2e80) {
					write(" ")
				}
			}
			out = append(out, &node{Kind: "text", Text: b.String()})
		default:
			out = append(out, n)
		}
	}
	return out
}

// page 按节点种类输出固定的标签；文字与属性由 html/template 转义，style 为已校验的 template.CSS。
var page = template.Must(template.New("wechat").Parse(`
{{- define "block" -}}
{{- if eq .Kind "section"}}<section style="{{.Style}}">{{range .Children}}{{template "block" .}}{{end}}</section>
{{- else if eq .Kind "h1"}}<h1 style="{{.Style}}">{{template "inline" .Children}}</h1>
{{- else if eq .Kind "h2"}}<h2 style="{{.Style}}">{{template "inline" .Children}}</h2>
{{- else if eq .Kind "h3"}}<h3 style="{{.Style}}">{{template "inline" .Children}}</h3>
{{- else if eq .Kind "h4"}}<h4 style="{{.Style}}">{{template "inline" .Children}}</h4>
{{- else if eq .Kind "p"}}<p style="{{.Style}}">{{template "inline" .Children}}</p>
{{- else if eq .Kind "pre"}}<pre style="{{.Style}}">{{.Text}}</pre>
{{- else if eq .Kind "blockquote"}}<blockquote style="{{.Style}}">{{range .Children}}{{template "block" .}}{{end}}</blockquote>
{{- else if eq .Kind "ul"}}<ul style="{{.Style}}">{{range .Children}}<li style="{{.Style}}">{{template "inline" .Children}}</li>{{end}}</ul>
{{- else if eq .Kind "ol"}}<ol style="{{.Style}}">{{range .Children}}<li style="{{.Style}}">{{template "inline" .Children}}</li>{{end}}</ol>
{{- else if eq .Kind "hr"}}<hr style="{{.Style}}">
{{- else if eq .Kind "figure"}}<section style="{{.Style}}"><p style="{{(index .Children 0).Style}}">{{.Text}}</p><p style="{{(index .Children 1).Style}}">{{.Caption}}</p></section>
{{- else if eq .Kind "references"}}<p style="{{.Style}}">参考链接{{range .Lines}}<br>{{.}}{{end}}</p>
{{- end -}}
{{- end -}}
{{- define "inline" -}}
{{- range . -}}
{{- if eq .Kind "text"}}{{.Text}}
{{- else if eq .Kind "code"}}<code style="{{.Style}}">{{.Text}}</code>
{{- else if eq .Kind "strong"}}<strong style="{{.Style}}">{{template "inline" .Children}}</strong>
{{- else if eq .Kind "em"}}<em style="{{.Style}}">{{template "inline" .Children}}</em>
{{- else if eq .Kind "br"}}<br>
{{- else if eq .Kind "sup"}}<sup style="{{.Style}}">{{.Text}}</sup>
{{- end -}}
{{- end -}}
{{- end -}}`))
