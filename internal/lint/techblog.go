package lint

import (
	"fmt"
	"regexp"
	"slices"
	"sort"
	"strings"
	"unicode"

	"github.com/kw12121212/writing-anything/internal/resolve"
)

// 技术写作的规则类型：代码块标注语言（code-block-language）、链接格式（link-format）与版本号、API 说法的出处
// （reference-sources）。都只检查正文本身，不访问网络。

func checkCodeLanguage(t *text, _ params, _ *resolve.Resolved, _ Input) ([]Finding, bool) {
	var out []Finding
	for _, f := range t.fences {
		if f.info == "" {
			if len(out) == MaxHits {
				return out, true
			}
			out = append(out, Finding{Line: f.line + 1, Column: 1, Text: strings.TrimSpace(string(t.lines[f.line])),
				Message: "代码块没有标注语言：请在开头的围栏后写明语言，如 ```go、```bash；纯文本写作 ```text"})
		}
	}
	return out, false
}

// codeSpans 返回一行中行内代码的区间：[开头反引号, 结尾反引号之后)；没有对应结束的反引号不是行内代码。
func codeSpans(rs []rune) [][2]int {
	var out [][2]int
	next := nextIndex(rs, '`')
	for j := 0; j < len(rs); j++ {
		if rs[j] != '`' {
			continue
		}
		n := runLen(rs, j, '`')
		end := closingRun(rs, j+n, n, next)
		if end < 0 {
			j += n - 1
			continue
		}
		out = append(out, [2]int{j, end + n})
		j = end + n - 1
	}
	return out
}

// inSpans 报告列 c 是否落在某个区间 [起, 止) 中；spans 按起点升序且互不重叠，二分查找。
func inSpans(spans [][2]int, c int) bool {
	i := sort.Search(len(spans), func(k int) bool { return spans[k][0] > c }) - 1
	return i >= 0 && c < spans[i][1]
}

var (
	// linkRE 匹配 Markdown 行内链接与图片：[文字](地址) 与 ![替代文字](地址)。
	linkRE = regexp.MustCompile(`(!?)\[([^\[\]]*)\]\(([^()]*)\)`)
	// bareURLRE 匹配裸 URL 的协议与主机部分。
	bareURLRE = regexp.MustCompile(`https?://[^\s/?#）」』】》，。；：、！？“”‘’…—<>()\[\]]*`)
	schemeRE  = regexp.MustCompile(`^([A-Za-z][A-Za-z0-9+.-]*):`)
)

// runeCols 返回字符串 s 中每个字节下标（字符起点）对应的列下标。
func runeCols(s string) []int {
	col := make([]int, len(s)+1)
	c := 0
	for b := range s {
		col[b] = c
		c++
	}
	col[len(s)] = c
	return col
}

func checkLinks(t *text, p params, _ *resolve.Resolved, _ Input) ([]Finding, bool) {
	schemes := p.orDefault("schemes", []string{"http", "https", "mailto"})
	var out []Finding
	add := func(line, col int, text, msg string) bool {
		if len(out) == MaxHits {
			return false
		}
		out = append(out, Finding{Line: line + 1, Column: col + 1, Text: text, Message: "链接格式：" + msg})
		return true
	}
	for i, rs := range t.lines {
		if t.fence[i] {
			continue
		}
		line := string(rs)
		col := runeCols(line)
		spans := t.codeSpansOf(i)
		var targets [][2]int // 链接地址的列区间，其中的 URL 不再按裸 URL 检查
		for _, m := range linkRE.FindAllStringSubmatchIndex(line, -1) {
			start := col[m[0]]
			if inSpans(spans, start) || t.inComment(i, start) {
				continue // 行内代码与 HTML 注释（屏蔽区域）中的链接不检查
			}
			targets = append(targets, [2]int{col[m[6]], col[m[7]]})
			image, label, target := m[3] > m[2], line[m[4]:m[5]], strings.TrimSpace(line[m[6]:m[7]])
			whole := line[m[0]:m[1]]
			if msg := badLink(image, label, target, schemes); msg != "" && !add(i, start, whole, msg) {
				return out, true
			}
		}
		for _, m := range bareURLRE.FindAllStringIndex(line, -1) {
			start := col[m[0]]
			if inSpans(spans, start) || inSpans(targets, start) || t.inComment(i, start) {
				continue
			}
			// 匹配在主机名之后的 / ? # 处结束；以 // 结尾说明没有主机名。
			if u := line[m[0]:m[1]]; strings.HasSuffix(u, "//") && !add(i, start, u, "URL 缺少主机名") {
				return out, true
			}
		}
	}
	return out, false
}

// badLink 返回链接的格式问题；没有问题时为空。
func badLink(image bool, label, target string, schemes []string) string {
	if !image && strings.TrimSpace(label) == "" {
		return "链接文字为空"
	}
	if strings.HasPrefix(target, "<") && strings.HasSuffix(target, ">") {
		target = strings.TrimSpace(target[1 : len(target)-1])
	} else if i := strings.IndexFunc(target, unicode.IsSpace); i >= 0 {
		// 地址之后只允许带引号的标题：[文字](地址 "标题")。
		rest := strings.TrimSpace(target[i:])
		if !(len(rest) >= 2 && (rest[0] == '"' && rest[len(rest)-1] == '"' || rest[0] == '\'' && rest[len(rest)-1] == '\'')) {
			return "链接地址中含空白；请把空格写作 %20，或给地址加尖括号"
		}
		target = target[:i]
	}
	if target == "" {
		return "链接地址为空"
	}
	if m := schemeRE.FindStringSubmatch(target); m != nil {
		scheme := strings.ToLower(m[1])
		if !slices.Contains(schemes, scheme) {
			return fmt.Sprintf("不允许的协议 %s:；允许的协议为 %s", m[1], strings.Join(schemes, "、"))
		}
		if (scheme == "http" || scheme == "https") && !hasHost(target[len(m[0]):]) {
			return "链接地址缺少主机名"
		}
	}
	return ""
}

func hasHost(rest string) bool {
	if !strings.HasPrefix(rest, "//") {
		return false
	}
	host := rest[2:]
	if i := strings.IndexAny(host, "/?#"); i >= 0 {
		host = host[:i]
	}
	return host != ""
}

var (
	// versionRE 匹配版本号：v1.2、1.2.3，以及紧跟在英文名称之后的 1.22（如 Go 1.22、Python3.12）。
	versionRE = regexp.MustCompile(`\bv\d+(?:\.\d+){1,3}\b|\b\d+\.\d+\.\d+(?:\.\d+)?\b|\b[A-Za-z][A-Za-z+#]*\s?\d+\.\d+\b`)
	// apiRE 匹配函数调用形式的行内代码内容，如 http.Get()、os.Open(path)、Vec::new()。
	apiRE = regexp.MustCompile(`^[A-Za-z_][A-Za-z0-9_]*(?:(?:\.|::)[A-Za-z_][A-Za-z0-9_]*)*\(.*\)$`)
)

// mention 是一处需要出处的版本号或 API 说法：行与列区间（列下标从 0 开始）。
type mention struct {
	line, from, to int
	kind, text     string
}

func checkReferences(t *text, p params, _ *resolve.Resolved, in Input) ([]Finding, bool) {
	if in.ClaimsInvalid {
		return nil, false
	}
	types := p.orDefault("source_types", []string{"reference"})
	var claims []string
	for _, c := range in.Claims {
		if s := normalizeClaim(c.Text); s != "" && slices.ContainsFunc(c.Sources, func(s ClaimSource) bool { return slices.Contains(types, s.Type) }) {
			claims = append(claims, s)
		}
	}
	covered := newMatcher(dedupe(claims)).cover(t.prose())
	isCovered := func(m mention) bool {
		for c := m.from; c < m.to; c++ {
			if c >= len(covered[m.line]) || !covered[m.line][c] {
				return false
			}
		}
		return true
	}
	var out []Finding
	for _, m := range mentions(t, p.bool("versions", true), p.bool("apis", true)) {
		if isCovered(m) {
			continue
		}
		if len(out) == MaxHits {
			return out, true
		}
		unit := in.Unit
		if unit == "" {
			unit = "<单元>"
		}
		out = append(out, Finding{Line: m.line + 1, Column: m.from + 1, Text: m.text,
			Message: fmt.Sprintf("%s「%s」需要出处：请用 wa-cli source add --type %s 登记官方文档等资料，再用 wa-cli claim add %s --kind fact --text <含该%s的句子> --sources <素材 id> 登记论断（Claim）",
				m.kind, m.text, types[0], unit, m.kind)})
	}
	return out, false
}

// mentions 找出散文段落中的版本号（可见文字中）与 API 说法（函数调用形式的行内代码）。
func mentions(t *text, versions, apis bool) []mention {
	var out []mention
	for i := range t.lines {
		if t.paragraphOf(i) < 0 {
			continue
		}
		if versions {
			line := string(t.vis[i])
			col := runeCols(line)
			for _, m := range versionRE.FindAllStringIndex(line, -1) {
				out = append(out, mention{i, col[m[0]], col[m[1]], "版本号", line[m[0]:m[1]]})
			}
		}
		if apis {
			rs := t.lines[i]
			for _, s := range t.codeSpansOf(i) {
				if t.inComment(i, s[0]) {
					continue
				}
				n := runLen(rs, s[0], '`')
				body := strings.TrimSpace(string(rs[s[0]+n : s[1]-n]))
				if apiRE.MatchString(body) {
					out = append(out, mention{i, s[0] + n, s[1] - n, "API 说法", body})
				}
			}
		}
	}
	slices.SortStableFunc(out, func(a, b mention) int {
		if a.line != b.line {
			return a.line - b.line
		}
		return a.from - b.from
	})
	return out
}
