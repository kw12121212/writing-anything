// Package outline 管理单元目录下的大纲（Outline）outline.yaml：按顺序排列的大纲节（Outline Section），每节声明覆盖的
// 核心观点（Key Message）、使用的素材（Source）或不覆盖任何核心观点的理由。本包加载与校验大纲、按节修改它，并对照同一
// 单元目录下写作简报（Brief）中的核心观点做覆盖追踪（Coverage Tracing），给出覆盖缺口（Coverage Gap），供 internal/work
// 计算大纲关卡的阻塞项。本包不 import internal/work；只返回结构化的问题，由命令层转换为诊断（Diagnostic）。
package outline

import (
	"fmt"
	"os"
	"path/filepath"
	"regexp"
	"slices"
	"strings"
	"unicode/utf8"

	"github.com/kw12121212/writing-anything/internal/brief"
	"github.com/kw12121212/writing-anything/internal/catalog"
	"github.com/kw12121212/writing-anything/internal/schema"
	"github.com/kw12121212/writing-anything/internal/source"
	"github.com/kw12121212/writing-anything/internal/workspace"
	"go.yaml.in/yaml/v3"
)

// 大纲的错误码（Error Code）。已发布的错误码不得改名或复用。
const (
	CodeInvalid          = "outline.invalid"
	CodeUncoveredMessage = "outline.uncovered-message"
	CodeMissingReason    = "outline.missing-reason"
	CodeUnknownMessage   = "outline.unknown-message"
	CodeUnknownSource    = "outline.unknown-source"
	CodeSectionNotFound  = "outline.section-not-found"
	CodeAnchoredValue    = "outline.anchored-value"
	CodeMissingPart      = "outline.missing-method-part"
	CodeMethodOrder      = "outline.method-order"
	CodeUnknownRole      = "outline.unknown-role"
)

// 单元目录中大纲的文件名：outline.yaml 是结构化的大纲，outline.md 是自由文本，不含可检查的节。
const (
	FileName = "outline.yaml"
	MDName   = "outline.md"
)

// Section 是一个大纲节；未设置的数组为 nil，未设置的 reason 为空字符串。
type Section struct {
	ID     string   `json:"id"`
	Title  string   `json:"title"`
	Role   string   `json:"role"` // 该节承担的写作法章节或节拍 id；空表示不承担
	Covers []string `json:"covers"`
	Uses   []string `json:"uses"`
	Reason string   `json:"reason"`
}

// Outline 是一份已通过校验的大纲。
type Outline struct {
	Path     string    `json:"-"` // outline.yaml 相对项目目录的路径
	Sections []Section `json:"sections"`
}

// Messages 是覆盖追踪的对象：同一单元目录下写作简报中的核心观点，以及单元选用的写作法模板（Method Template）。
// Problems 非空时写作简报不通过校验，不检查 covers 引用与覆盖缺口。Method 为 nil 时不检查写作法。
type Messages struct {
	List     []brief.Message
	Method   *catalog.Method
	Problems []workspace.Problem
	// MethodProblem 非 nil 时写作简报选用的写作法不存在（brief.unknown-method）：大纲关卡视为不通过校验，查看与修改大纲的
	// 命令照常执行并以 warning 报告它（workspace-artifacts/cross-artifact-references），validate 由写作简报的检查报告它。
	MethodProblem *workspace.Problem
}

// Picker 选出单元使用的写作法：Methods 为本条命令加载一次的写作法目录，Default 为作品文体的默认写作法。
type Picker struct {
	Methods *catalog.MethodSet
	Default string
}

// LoadMessages 读取单元目录 dir 下写作简报中的核心观点与选用的写作法；没有 brief.yaml 时没有核心观点。写作简报的 method
// 为空（或没有写作简报）时用文体的默认写作法，为 none 时不用写作法；选用的写作法不存在时不检查写作法，并记下该问题
// （MethodProblem）。
func LoadMessages(root, dir string, q brief.Queue, pick Picker) Messages {
	b, ps := brief.Load(root, dir, q)
	if len(ps) > 0 {
		return Messages{Problems: ps}
	}
	var ms Messages
	raw := ""
	if b != nil {
		ms.List, raw = b.KeyMessages, b.Method
	}
	if pick.Methods == nil {
		return ms
	}
	m, missing := pick.Methods.Pick(raw, pick.Default)
	if missing != "" {
		p := brief.UnknownMethod(brief.Path(dir), brief.FieldMethod, missing, pick.Methods)
		ms.MethodProblem = &p
	}
	ms.Method = m
	return ms
}

func (ms Messages) has(id string) bool {
	return slices.ContainsFunc(ms.List, func(m brief.Message) bool { return m.ID == id })
}

func errorAt(code, path, field, msg string) workspace.Problem {
	return workspace.Problem{Code: code, Severity: workspace.SeverityError, Path: path, Field: field, Message: msg}
}

// Path 返回单元目录 dir 下 outline.yaml 的路径。
func Path(dir string) string { return dir + "/" + FileName }

// Load 读取并校验单元目录 dir 下的 outline.yaml（Schema、节 id 唯一与引用检查，不含覆盖缺口）；没有 outline.yaml 时返回
// nil 与 nil。任一检查失败时返回 nil 与全部问题。
func Load(root, dir string, ms Messages) (*Outline, []workspace.Problem) {
	_, o, fatal, refs := load(root, dir, ms)
	if ps := append(fatal, refs...); len(ps) > 0 {
		return nil, ps
	}
	return o, nil
}

// LoadLenient 与 Load 相同，但引用问题（covers 指向写作简报中已不存在的核心观点、uses 指向未登记的素材）不妨碍返回大纲：
// 写作简报删除核心观点后，大纲仍能通过 wa-cli 查看与修改。fatal 为解析、Schema 与重复 id 问题，非空时大纲为 nil。
func LoadLenient(root, dir string, ms Messages) (o *Outline, fatal, refs []workspace.Problem) {
	_, o, fatal, refs = load(root, dir, ms)
	return o, fatal, refs
}

func load(root, dir string, ms Messages) (*yaml.Node, *Outline, []workspace.Problem, []workspace.Problem) {
	rel := Path(dir)
	data, ok, err := workspace.ReadRegular(root, rel)
	switch {
	case err != nil:
		return nil, nil, []workspace.Problem{errorAt(workspace.CodeReadFailed, rel, "", "读取失败："+err.Error())}, nil
	case !ok:
		if _, err := os.Lstat(filepath.Join(root, filepath.FromSlash(rel))); err == nil {
			// 存在但途经符号链接或不是普通文件：所有入口都报告同一问题，不读取项目目录之外的内容，也不覆盖它。
			return nil, nil, []workspace.Problem{errorAt(schema.CodeNotFound, rel, "", "outline.yaml 途经符号链接或不是普通文件，按不存在处理；请把它换成单元目录中的普通文件")}, nil
		}
		return nil, nil, nil, nil
	}
	doc, p := schema.Parse(data)
	if p != nil {
		return nil, nil, workspace.FromSchema(rel, []schema.Problem{*p}), nil
	}
	o, fatal, refs := parse(root, rel, doc, ms)
	if o == nil {
		return nil, nil, fatal, nil
	}
	return doc, o, nil, refs
}

// parse 按 Schema 校验已解析的文档并检查节 id 唯一（fatal）与引用（refs）；fatal 非空时返回的大纲为 nil。
func parse(root, rel string, doc *yaml.Node, ms Messages) (*Outline, []workspace.Problem, []workspace.Problem) {
	if ps := schema.Named("outline").ValidateNode(doc); len(ps) > 0 {
		return nil, workspace.FromSchema(rel, ps), nil
	}
	var o Outline
	catalog.Decode(doc, &o)
	o.Path = rel
	if ps := checkIDs(rel, &o); len(ps) > 0 {
		return nil, ps, nil
	}
	return &o, nil, checkRefs(root, rel, &o, ms)
}

// checkIDs 检查节 id 在文件内唯一。
func checkIDs(rel string, o *Outline) []workspace.Problem {
	var out []workspace.Problem
	seen := map[string]bool{}
	for i, s := range o.Sections {
		if seen[s.ID] {
			out = append(out, errorAt(catalog.CodeDuplicateID, rel, fmt.Sprintf("sections[%d].id", i), "大纲节 id "+s.ID+" 重复"))
		}
		seen[s.ID] = true
	}
	return out
}

// checkRefs 检查 covers 引用写作简报中的核心观点（写作简报不通过校验时不检查）、uses 引用已登记的素材。
func checkRefs(root, rel string, o *Outline, ms Messages) []workspace.Problem {
	var out []workspace.Problem
	for i, s := range o.Sections {
		if len(ms.Problems) == 0 {
			for j, id := range s.Covers {
				if !ms.has(id) {
					out = append(out, errorAt(CodeUnknownMessage, rel, fmt.Sprintf("sections[%d].covers[%d]", i, j), unknownMessage(id)))
				}
			}
		}
		for j, id := range s.Uses {
			if !source.Registered(root, id) {
				out = append(out, errorAt(CodeUnknownSource, rel, fmt.Sprintf("sections[%d].uses[%d]", i, j), unknownSource(id)))
			}
		}
	}
	return out
}

func unknownMessage(id string) string {
	return "写作简报（Brief）中没有核心观点（Key Message）" + id + "；大纲节只能覆盖写作简报中的核心观点"
}

func unknownSource(id string) string {
	return "素材（Source）" + id + " 未登记；大纲节只能使用已用 wa-cli source add 登记的素材"
}

// Cover 是一个核心观点的覆盖情况：Sections 为 covers 含它的节 id，按大纲顺序。
type Cover struct {
	KeyMessage string
	Text       string
	Sections   []string
}

// Coverage 按写作简报中的顺序返回每个核心观点被哪些节覆盖；o 为 nil 表示没有任何节。
func Coverage(o *Outline, ms Messages) []Cover {
	var out []Cover
	for _, m := range ms.List {
		c := Cover{KeyMessage: m.ID, Text: m.Text}
		if o != nil {
			for _, s := range o.Sections {
				if slices.Contains(s.Covers, m.ID) {
					c.Sections = append(c.Sections, s.ID)
				}
			}
		}
		out = append(out, c)
	}
	return out
}

// Gap 是一个覆盖缺口（Coverage Gap）：未被任何一节覆盖的核心观点（KeyMessage 非空），或既没有 covers 也没有 reason 的节
// （Section 非空，Index 为其下标）。
type Gap struct {
	Code       string
	KeyMessage string
	Section    string
	Index      int
	Message    string
}

// Gaps 返回大纲 o（nil 表示没有任何节）的覆盖缺口：先按写作简报顺序列出未被覆盖的核心观点，再按大纲顺序列出缺少理由的节。
func Gaps(o *Outline, ms Messages) []Gap {
	var out []Gap
	for _, c := range Coverage(o, ms) {
		if len(c.Sections) == 0 {
			out = append(out, Gap{Code: CodeUncoveredMessage, KeyMessage: c.KeyMessage,
				Message: fmt.Sprintf("核心观点（Key Message）%s「%s」没有被大纲（Outline）的任何一节覆盖；请在覆盖它的节的 covers 中加入 %s，或新增一节", c.KeyMessage, c.Text, c.KeyMessage)})
		}
	}
	if o != nil {
		for i, s := range o.Sections {
			if len(s.Covers) == 0 && strings.TrimSpace(s.Reason) == "" {
				out = append(out, Gap{Code: CodeMissingReason, Section: s.ID, Index: i,
					Message: fmt.Sprintf("大纲节（Outline Section）%s「%s」既没有覆盖核心观点，也没有写明不覆盖的理由；请给出 covers 或 reason", s.ID, s.Title)})
			}
		}
	}
	return append(out, methodGaps(o, ms.Method)...)
}

// methodGaps 返回大纲相对写作法 m 的缺口（m 为 nil 时没有）：role 不是写作法的章节或节拍的节（outline.unknown-role），
// 没有任何节承担的章节或节拍（outline.missing-method-part），以及文章类写作法中顺序颠倒的章节（outline.method-order：
// 按每个章节第一次出现的位置比较，排在前一章节之前的节）。
func methodGaps(o *Outline, m *catalog.Method) []Gap {
	if m == nil || m.Parts() == nil {
		return nil // 规划类写作法的步骤不由大纲承担，不检查 role
	}
	parts := m.Parts()
	rank := map[string]int{}
	for i, p := range parts {
		rank[p.ID] = i
	}
	var out []Gap
	first := map[string]int{} // 章节或节拍 id → 第一次承担它的节的下标
	if o != nil {
		for i, s := range o.Sections {
			if s.Role == "" {
				continue
			}
			if _, ok := rank[s.Role]; !ok {
				out = append(out, Gap{Code: CodeUnknownRole, Section: s.ID, Index: i,
					Message: fmt.Sprintf("大纲节 %s 的 role %s 不是写作法 %s（%s）的章节或节拍；可用的有 %s", s.ID, s.Role, m.ID, m.Title, partIDs(parts))})
				continue
			}
			if _, ok := first[s.Role]; !ok {
				first[s.Role] = i
			}
		}
	}
	for _, p := range parts {
		if _, ok := first[p.ID]; !ok {
			out = append(out, Gap{Code: CodeMissingPart,
				Message: fmt.Sprintf("写作法 %s（%s）要求的%s %s「%s」没有任何大纲节承担；请新增一节并设置 role 为 %s，或在写作简报中换用其他写作法", m.ID, m.Title, partName(m), p.ID, p.Title, p.ID)})
		}
	}
	if m.Kind == catalog.MethodArticle && o != nil {
		last, lastPart := -1, ""
		for _, p := range parts {
			i, ok := first[p.ID]
			if !ok {
				continue
			}
			if i < last {
				s := o.Sections[i]
				out = append(out, Gap{Code: CodeMethodOrder, Section: s.ID, Index: i,
					Message: fmt.Sprintf("写作法 %s 要求章节 %s 排在 %s 之后，但承担它的大纲节 %s 排在承担 %s 的节之前；请调整大纲节的顺序", m.ID, p.ID, lastPart, s.ID, lastPart)})
				continue
			}
			last, lastPart = i, p.ID
		}
	}
	return out
}

func partIDs(parts []catalog.MethodPart) string {
	var ids []string
	for _, p := range parts {
		ids = append(ids, p.ID)
	}
	return strings.Join(ids, "、")
}

func partName(m *catalog.Method) string {
	if m.Kind == catalog.MethodStructure {
		return "节拍"
	}
	return "章节"
}

// Problems 把覆盖缺口转换为 path 为 rel 的问题，供 validate 报告；缺少理由的节的 field 为 sections[i]。
func Problems(rel string, gaps []Gap) []workspace.Problem {
	var out []workspace.Problem
	for _, g := range gaps {
		field := ""
		switch g.Code {
		case CodeMissingReason:
			field = fmt.Sprintf("sections[%d]", g.Index)
		case CodeUnknownRole, CodeMethodOrder:
			field = fmt.Sprintf("sections[%d].role", g.Index)
		}
		out = append(out, errorAt(g.Code, rel, field, g.Message))
	}
	return out
}

// CheckUnitArtifacts 校验 dirs 中每个单元目录下存在的 outline.yaml：合法性问题，以及合法时的覆盖缺口。q 为调用方
// 已加载的决策队列（load-once-per-command），defaultMethod 返回单元目录所在作品的文体的默认写作法。
func CheckUnitArtifacts(root string, dirs []string, q brief.Queue, defaultMethod func(dir string) string) (checked []string, problems []workspace.Problem) {
	set := catalog.LoadMethods(root) // 写作法目录每条命令只加载一次
	for _, dir := range dirs {
		if _, err := os.Lstat(filepath.Join(root, filepath.FromSlash(Path(dir)))); err != nil {
			continue
		}
		checked = append(checked, Path(dir))
		problems = append(problems, checkDir(root, dir, q, Picker{set, defaultMethod(dir)})...)
	}
	return checked, problems
}

func checkDir(root, dir string, q brief.Queue, pick Picker) []workspace.Problem {
	ms := LoadMessages(root, dir, q, pick)
	o, ps := Load(root, dir, ms)
	if o == nil || len(ms.Problems) > 0 {
		// 写作简报不通过校验时不报告覆盖缺口，写作简报的问题由它自己的校验报告。
		return ps
	}
	return Problems(o.Path, Gaps(o, ms))
}

// CheckArtifact 对已通过 Schema 校验的 outline.yaml（rel）做领域检查与覆盖追踪；defaultMethod 为所在作品的文体的默认写作法。
func CheckArtifact(root, rel, defaultMethod string) []workspace.Problem {
	return checkDir(root, strings.TrimSuffix(rel, "/"+FileName), brief.LoadQueue(root), Picker{catalog.LoadMethods(root), defaultMethod})
}

// Gate 是单元目录 dir 下大纲的关卡结果。
type Gate struct {
	Path    string              // 覆盖缺口的路径：outline.yaml，只有 outline.md 时为 outline.md
	Invalid []workspace.Problem // 大纲或写作简报不通过校验时的问题（path 为不通过校验的产物）；非空时 Gaps 为空
	Gaps    []Gap
}

// Check 计算单元目录 dir 下大纲的关卡：yamlPresent 与 mdPresent 报告 outline.yaml 与 outline.md 是否按必需产物的规则
// 存在，两者都不存在时返回 nil（由缺失产物报告）。只有 outline.md 时视为没有任何节。
func Check(root, dir string, yamlPresent, mdPresent bool, q brief.Queue, pick Picker) *Gate {
	path := Path(dir)
	if !yamlPresent {
		if !mdPresent {
			return nil
		}
		path = dir + "/" + MDName
	}
	ms := LoadMessages(root, dir, q, pick)
	if len(ms.Problems) > 0 {
		return &Gate{Path: path, Invalid: ms.Problems}
	}
	if ms.MethodProblem != nil {
		return &Gate{Path: path, Invalid: []workspace.Problem{*ms.MethodProblem}}
	}
	var o *Outline
	if yamlPresent {
		var ps []workspace.Problem
		if o, ps = Load(root, dir, ms); len(ps) > 0 {
			return &Gate{Path: path, Invalid: ps}
		}
		if o == nil && !mdPresent {
			return nil // outline.yaml 在两次检查之间被删除
		}
	}
	return &Gate{Path: path, Gaps: Gaps(o, ms)}
}

// CodeBeatPosition 表示节拍在成稿中的位置超出写作法规定的范围；只提示，不阻塞。
const CodeBeatPosition = "outline.beat-position"

// BeatPositions 按字数比例核对成稿 content（rel 为其路径）中各节拍的位置：结构类写作法 m 的每个节拍，取承担它的第一个大纲节，
// 在正文中找标题文字与该节标题相同的第一个 Markdown 标题行，以该行之前的字符数占全文字符数的比例作为位置；超出节拍的位置范围时
// 给出一条 warning。找不到标题行的节拍不核对。m 不是结构类写作法或 o 为 nil 时没有。
func BeatPositions(content []byte, rel string, o *Outline, m *catalog.Method) []workspace.Problem {
	if o == nil || m == nil || m.Kind != catalog.MethodStructure {
		return nil
	}
	text := strings.ReplaceAll(string(content), "\r\n", "\n")
	lines := strings.Split(text, "\n")
	total := utf8.RuneCountInString(text)
	if total == 0 {
		return nil
	}
	headings := map[string]int{} // 标题文字 → 第一次出现的行下标；围栏代码块中的行不算标题
	before := make([]int, len(lines))
	n, fence := 0, ""
	for i, l := range lines {
		before[i] = n
		n += utf8.RuneCountInString(l) + 1
		if f := fenceRE.FindString(l); f != "" {
			switch {
			case fence == "":
				fence = f[:3]
			case strings.HasPrefix(f, fence):
				fence = ""
			}
			continue
		}
		if fence != "" {
			continue
		}
		if h := headingRE.FindStringSubmatch(l); h != nil {
			if _, ok := headings[strings.TrimSpace(h[1])]; !ok {
				headings[strings.TrimSpace(h[1])] = i
			}
		}
	}
	var out []workspace.Problem
	for _, b := range m.Beats {
		i := slices.IndexFunc(o.Sections, func(s Section) bool { return s.Role == b.ID })
		if i < 0 || b.Position == nil {
			continue
		}
		line, ok := headings[strings.TrimSpace(o.Sections[i].Title)]
		if !ok {
			continue
		}
		at := float64(before[line]) / float64(total)
		if at < b.Position.Min || at > b.Position.Max {
			out = append(out, workspace.Problem{Code: CodeBeatPosition, Severity: workspace.SeverityWarning, Path: rel, Line: line + 1,
				Message: fmt.Sprintf("节拍 %s「%s」出现在全文约 %.0f%% 处，写作法 %s 建议的位置为 %.0f%%–%.0f%%；只是提示，不阻塞推进",
					b.ID, b.Title, at*100, m.ID, b.Position.Min*100, b.Position.Max*100)})
		}
	}
	return out
}

// fenceRE 匹配围栏代码块的开始或结束行（去掉行首至多三个空格后以 ``` 或 ~~~ 开头）。
var fenceRE = regexp.MustCompile("^ {0,3}(`{3,}|~{3,})")

// headingRE 匹配 Markdown ATX 标题行，子匹配为标题文字。
var headingRE = regexp.MustCompile(`^ {0,3}#{1,6}\s+(.*?)\s*#*\s*$`)
