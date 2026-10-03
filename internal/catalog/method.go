package catalog

import (
	"fmt"
	"math"
	"slices"
	"strings"
	"sync"

	"github.com/kw12121212/writing-anything/internal/schema"
	"github.com/kw12121212/writing-anything/internal/workspace"
)

// 写作法模板的错误码。已发布的错误码不得改名或复用。
const (
	CodeInvalidPosition = "method.invalid-position"
	CodeInvalidTension  = "method.invalid-tension"
	CodeUnknownMethod   = "genre.unknown-method"
	CodeReservedMethod  = "method.reserved-id"
)

// 写作法模板（Method Template）的种类。
const (
	MethodArticle   = "article"   // 文章类：必需章节及其顺序
	MethodStructure = "structure" // 结构类：节拍及其在全文中的位置范围
	MethodPlanning  = "planning"  // 规划类：分步产出规划产物
)

// MethodNone 是写作简报中表示「不用写作法」的取值。
const MethodNone = "none"

// MethodPart 是写作法的一个章节、节拍或规划步骤。Position 只用于节拍：按字数比例的位置范围，0 为开头，1 为结尾。
// Tension 只用于节拍：目标张力（Target Tension），0 到 10 的整数，作为情绪曲线与张力曲线的目标走势；未声明为 nil。
// Product 只用于规划步骤：规划产物（Planning Product），声明该步骤的产物链接（Plan Link）类型，供 plan 命令校验。
type MethodPart struct {
	ID          string    `json:"id"`
	Title       string    `json:"title"`
	Description string    `json:"description,omitempty"`
	Position    *Position `json:"position,omitempty"`
	Tension     *float64  `json:"tension,omitempty"`
	Product     string    `json:"product,omitempty"`
}

// Position 是节拍在全文中的位置范围。
type Position struct {
	Min float64 `json:"min"`
	Max float64 `json:"max"`
}

// Method 是一个写作法模板：纯数据，规划类、结构类或文章类。
type Method struct {
	ID          string       `json:"id"`
	Title       string       `json:"title"`
	Description string       `json:"description,omitempty"`
	Kind        string       `json:"kind"`
	Sections    []MethodPart `json:"sections,omitempty"`
	Beats       []MethodPart `json:"beats,omitempty"`
	Steps       []MethodPart `json:"steps,omitempty"`
	Rubric      []RubricItem `json:"rubric,omitempty"`
}

// Parts 返回大纲需要承担的写作法部分：文章类为章节，结构类为节拍；规划类没有（规划步骤不由大纲承担）。
func (m *Method) Parts() []MethodPart {
	switch m.Kind {
	case MethodArticle:
		return m.Sections
	case MethodStructure:
		return m.Beats
	}
	return nil
}

// checkMethod 检查已通过 Schema 校验的写作法：章节、节拍、步骤与评分项的 id 不重复，节拍的位置范围 min 不大于 max。
// prefix 为字段路径前缀（如 methods[0].），problem 构造问题。
func checkMethod(m Method, prefix string, problem func(code, field, msg string)) {
	if m.ID == MethodNone {
		problem(CodeReservedMethod, prefix+"id", "写作法 id "+MethodNone+" 保留为写作简报中「不用写作法」的取值，不能作为写作法 id")
	}
	for _, list := range []struct {
		key   string
		parts []MethodPart
	}{{"sections", m.Sections}, {"beats", m.Beats}, {"steps", m.Steps}} {
		for _, d := range duplicates(len(list.parts), func(i int) string { return list.parts[i].ID }) {
			problem(CodeDuplicateID, fmt.Sprintf("%s%s[%d].id", prefix, list.key, d), "写作法 "+m.ID+" 的 "+list.parts[d].ID+" 重复")
		}
	}
	for i, b := range m.Beats {
		if b.Position != nil && b.Position.Min > b.Position.Max {
			problem(CodeInvalidPosition, fmt.Sprintf("%sbeats[%d].position", prefix, i),
				fmt.Sprintf("节拍 %s 的位置范围 min %g 大于 max %g", b.ID, b.Position.Min, b.Position.Max))
		}
		if b.Tension != nil && (*b.Tension != math.Trunc(*b.Tension) || *b.Tension < 0 || *b.Tension > 10) {
			problem(CodeInvalidTension, fmt.Sprintf("%sbeats[%d].tension", prefix, i),
				fmt.Sprintf("节拍 %s 的目标张力 %g 必须是 0 到 10 的整数", b.ID, *b.Tension))
		}
	}
	for _, d := range duplicates(len(m.Rubric), func(i int) string { return m.Rubric[i].ID }) {
		problem(CodeDuplicateID, fmt.Sprintf("%srubric[%d].id", prefix, d), "评分项 "+m.Rubric[d].ID+" 重复")
	}
}

// BuiltinMethods 返回全部内置写作法的 id，按字母顺序排列。
func BuiltinMethods() []string { return builtinIDs("methods") }

// LoadMethod 加载内置写作法 id；不存在时 ok 为 false。
func LoadMethod(id string) (m *Method, problems []workspace.Problem, ok bool) {
	doc, problems, ok := readBuiltin("methods", id)
	if doc == nil {
		return nil, problems, ok
	}
	origin := builtinOrigin("methods", id)
	if ps := workspace.FromSchema("", schema.Named("method").ValidateNode(doc)); len(ps) > 0 {
		return nil, withOrigin(ps, origin), true
	}
	m = &Method{}
	Decode(doc, m)
	checkMethod(*m, "", func(code, field, msg string) {
		problems = append(problems, workspace.Problem{Code: code, Severity: workspace.SeverityError, Field: field, Origin: origin, Message: msg})
	})
	return m, problems, true
}

// builtinMethods 是内置写作法（嵌入二进制、不会改变），只解析一次。
var builtinMethods = sync.OnceValue(func() []Method {
	var out []Method
	for _, id := range BuiltinMethods() {
		if m, _, _ := LoadMethod(id); m != nil {
			out = append(out, *m)
		}
	}
	return out
})

// MethodRef 是一个可用的写作法与它的来源：builtin 或 pack:<id>。
type MethodRef struct {
	Method
	Source string `json:"source"`
}

// MethodSet 是工作区可用的全部写作法，按 id 排序。每条命令加载一次，传给需要选用写作法的检查（load-once-per-command）。
type MethodSet struct {
	List []MethodRef
}

// LoadMethods 汇总 root 下工作区可用的写作法：内置写作法，以及 config.yaml 的 packs（为空时为内置默认规范包）按顺序
// 定义的写作法；同一 id 由后出现的来源覆盖。无法加载的规范包跳过，其问题由整体校验报告。
func LoadMethods(root string) *MethodSet {
	byID := map[string]MethodRef{}
	for _, m := range builtinMethods() {
		byID[m.ID] = MethodRef{m, OriginBuiltin}
	}
	packs := []string{DefaultPack}
	if cfg, _ := workspace.Load(root); cfg != nil && len(cfg.Packs) > 0 {
		packs = cfg.Packs
	}
	for _, id := range packs {
		p, _, _ := FindPack(root, id)
		if p == nil {
			continue
		}
		for _, m := range p.Methods {
			if m.ID != MethodNone { // 保留 id，validate 报告 method.reserved-id；不能被选用，也不列出
				byID[m.ID] = MethodRef{m, "pack:" + p.ID}
			}
		}
	}
	set := &MethodSet{}
	for _, m := range byID {
		set.List = append(set.List, m)
	}
	slices.SortFunc(set.List, func(a, b MethodRef) int { return strings.Compare(a.ID, b.ID) })
	return set
}

// Find 返回写作法 id；不存在时 ok 为 false。
func (s *MethodSet) Find(id string) (*MethodRef, bool) {
	for i := range s.List {
		if s.List[i].ID == id {
			return &s.List[i], true
		}
	}
	return nil, false
}

// IDs 返回全部写作法的 id。
func (s *MethodSet) IDs() []string {
	var ids []string
	for _, m := range s.List {
		ids = append(ids, m.ID)
	}
	return ids
}

// Pick 选出单元使用的写作法：写作简报的 method（raw）非空时用它，否则用文体的默认写作法 def；为 none 或两者都为空时
// 返回 nil 与空字符串。写作法不存在时返回 nil 与它的 id（missing），由调用方报告 brief.unknown-method。
func (s *MethodSet) Pick(raw, def string) (m *Method, missing string) {
	id := raw
	if id == "" {
		id = def
	}
	if id == "" || id == MethodNone {
		return nil, ""
	}
	if r, ok := s.Find(id); ok {
		return &r.Method, ""
	}
	return nil, id
}
