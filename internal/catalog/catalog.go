// Package catalog 加载文体（Genre）与规范包（Pack）：内置于二进制的文体与规范包，以及工作区
// .writing-anything/packs/<id>.yaml 中作者自有的规范包。两者都只能是数据；本包在 Schema 校验之外检查
// 规则类型、id 重复与引用，只返回结构化的问题。
package catalog

import (
	"embed"
	"encoding/json"
	"errors"
	"fmt"
	"io/fs"
	"os"
	"path"
	"path/filepath"
	"slices"
	"strings"

	"github.com/kw12121212/writing-anything/internal/schema"
	"github.com/kw12121212/writing-anything/internal/workspace"
	"go.yaml.in/yaml/v3"
)

// 文体与规范包的错误码（Error Code）。已发布的错误码不得改名或复用。
const (
	CodeUnknownRuleType = "rule.unknown-type"
	CodeDuplicateID     = "artifact.duplicate-id"
	CodeUnknownUnitType = "genre.unknown-unit-type"
	CodePackIDMismatch  = "pack.id-mismatch"
	CodePackIDConflict  = "pack.id-conflict"
	CodeReservedStageID = "genre.reserved-stage-id"
)

// StageDone 是保留的阶段 id，表示单元走完了全部阶段；文体不得声明同名阶段。
const StageDone = "done"

// 规范包的出处。
const (
	OriginBuiltin   = "builtin"
	OriginWorkspace = "workspace"
)

// DefaultPack 是 config.yaml 的 packs 为空时隐式启用的内置规范包。
const DefaultPack = "default"

// RuleTypes 是已知的规则类型（Rule Type）。规则的 params 结构由规则引擎定义，这里不校验。
var RuleTypes = []string{"cjk-typography", "ai-cliche", "banned-terms", "term-consistency", "length-range", "paragraph-length", "required-sections", "claim-sources", "claim-candidates", "code-block-language", "link-format", "reference-sources", "ad-compliance"}

//go:embed builtin
var builtin embed.FS

// Rule 是一条规则（Rule）的配置。覆盖已有规则时 Type 可以为空。
type Rule struct {
	Type     string         `json:"type,omitempty"`
	Severity string         `json:"severity,omitempty"`
	Params   map[string]any `json:"params,omitempty"`
}

// Waiver 是对一条规则的豁免（Waiver）。
type Waiver struct {
	Rule   string `json:"rule"`
	Reason string `json:"reason"`
}

// RubricItem 是评分标准（Rubric）中的一项。
type RubricItem struct {
	ID          string `json:"id"`
	Title       string `json:"title"`
	Description string `json:"description,omitempty"`
	Blocking    bool   `json:"blocking,omitempty"` // strict 关卡强度下结论为 fail 时阻塞验收
}

// UnitType 是文体声明的单元类型。BriefRequired 是该类型单元的简报必填项（Required Brief Field）。
type UnitType struct {
	ID            string   `json:"id"`
	Title         string   `json:"title"`
	Children      []string `json:"children"`
	BriefRequired []string `json:"briefRequired"`
}

// Stage 是文体声明的生命周期阶段（Stage）。
type Stage struct {
	ID                string   `json:"id"`
	Title             string   `json:"title"`
	RequiredArtifacts []string `json:"requiredArtifacts"`
}

// Term 是术语表（Glossary）中的一个词条。
type Term struct {
	Term    string   `json:"term"`
	English string   `json:"english,omitempty"`
	Avoid   []string `json:"avoid,omitempty"`
}

// BannedTerm 是一个禁用词（Banned Term）。
type BannedTerm struct {
	Term        string `json:"term"`
	Reason      string `json:"reason,omitempty"`
	Replacement string `json:"replacement,omitempty"`
}

// Genre 是一个文体。
type Genre struct {
	ID          string
	Title       string
	Description string
	UnitTypes   []UnitType
	Stages      []Stage
	Rules       map[string]Rule
	Rubric      []RubricItem
	// ContextBudget 是文体声明的上下文预算（Context Budget）；0 表示未声明。
	ContextBudget int
	// DefaultMethod 是文体的默认写作法模板（Method Template）id；写作简报没有选择写作法时使用。空表示没有默认。
	DefaultMethod string
}

// Pack 是一个规范包。Path 是工作区规范包相对项目目录的路径，内置规范包为空。
type Pack struct {
	ID          string
	Title       string
	Description string
	Style       []string
	Glossary    []Term
	BannedTerms []BannedTerm
	Rules       map[string]Rule
	Waivers     []Waiver
	Rubric      []RubricItem
	Methods     []Method // 规范包自有的写作法模板（如企业内容 SOP）
	Origin      string
	Path        string
	// ContextBudget 是规范包声明的上下文预算；0 表示未声明。
	ContextBudget int
	// HTMLTheme 是规范包声明的样式主题（HTML Theme）：元素名 → CSS 声明；nil 表示未声明。
	HTMLTheme map[string]string
	// DisclosureTemplate 是规范包声明的 AI 参与说明模板；空表示未声明。
	DisclosureTemplate string
}

// 文件格式：字段名与 YAML 一致。与公开类型只差结构体标签，可以直接转换。
type (
	unitTypeFile struct {
		ID            string   `json:"id"`
		Title         string   `json:"title"`
		Children      []string `json:"children"`
		BriefRequired []string `json:"brief_required"`
	}
	stageFile struct {
		ID                string   `json:"id"`
		Title             string   `json:"title"`
		RequiredArtifacts []string `json:"required_artifacts"`
	}
	genreFile struct {
		ID          string          `json:"id"`
		Title       string          `json:"title"`
		Description string          `json:"description"`
		UnitTypes   []unitTypeFile  `json:"unit_types"`
		Stages      []stageFile     `json:"stages"`
		Rules       map[string]Rule `json:"rules"`
		Rubric      []RubricItem    `json:"rubric"`
		Budget      int             `json:"context_budget"`
		Method      string          `json:"default_method"`
	}
	packFile struct {
		ID          string            `json:"id"`
		Title       string            `json:"title"`
		Description string            `json:"description"`
		Style       []string          `json:"style"`
		Glossary    []Term            `json:"glossary"`
		BannedTerms []BannedTerm      `json:"banned_terms"`
		Rules       map[string]Rule   `json:"rules"`
		Waivers     []Waiver          `json:"waivers"`
		Rubric      []RubricItem      `json:"rubric"`
		Methods     []Method          `json:"methods"`
		Budget      int               `json:"context_budget"`
		Theme       map[string]string `json:"html_theme"`
		Disclosure  string            `json:"ai_disclosure_template"`
	}
)

// Decode 把已通过 Schema 校验的文档转换为 out 指向的、字段名与 YAML 一致的结构体。
func Decode(doc *yaml.Node, out any) {
	data, err := json.Marshal(schema.Decode(doc))
	if err == nil {
		err = json.Unmarshal(data, out)
	}
	if err != nil {
		// 通过 Schema 校验的文档一定能转换；走到这里说明 Schema 与文件格式不一致。
		panic(fmt.Sprintf("产物无法转换：%v", err))
	}
}

// BuiltinGenres 返回全部内置文体的 id，按字母顺序排列。
func BuiltinGenres() []string { return builtinIDs("genres") }

// BuiltinPacks 返回全部内置规范包的 id，按字母顺序排列。
func BuiltinPacks() []string { return builtinIDs("packs") }

func builtinIDs(dir string) []string {
	entries, _ := builtin.ReadDir("builtin/" + dir)
	var ids []string
	for _, e := range entries {
		ids = append(ids, strings.TrimSuffix(e.Name(), ".yaml"))
	}
	return ids
}

func readBuiltin(dir, id string) (*yaml.Node, []workspace.Problem, bool) {
	data, err := builtin.ReadFile("builtin/" + dir + "/" + id + ".yaml")
	if err != nil {
		return nil, nil, false
	}
	doc, p := schema.Parse(data)
	if p != nil {
		return nil, withOrigin(workspace.FromSchema("", []schema.Problem{*p}), builtinOrigin(dir, id)), true
	}
	return doc, nil, true
}

// builtinOrigin 是内置产物的出处说明；内置产物没有工作区路径，诊断用它指明问题出在哪里。
func builtinOrigin(dir, id string) string {
	switch dir {
	case "genres":
		return "内置文体 " + id
	case "methods":
		return "内置写作法 " + id
	}
	return "内置规范包 " + id
}

func withOrigin(ps []workspace.Problem, origin string) []workspace.Problem {
	for i := range ps {
		ps[i].Origin = origin
	}
	return ps
}

// LoadGenre 加载内置文体 id；不存在时 ok 为 false。
func LoadGenre(id string) (g *Genre, problems []workspace.Problem, ok bool) {
	doc, problems, ok := readBuiltin("genres", id)
	if doc == nil {
		return nil, problems, ok
	}
	if ps := workspace.FromSchema("", schema.Named("genre").ValidateNode(doc)); len(ps) > 0 {
		return nil, withOrigin(ps, builtinOrigin("genres", id)), true
	}
	var f genreFile
	Decode(doc, &f)
	g = &Genre{ID: f.ID, Title: f.Title, Description: f.Description, Rules: f.Rules, Rubric: f.Rubric, ContextBudget: f.Budget, DefaultMethod: f.Method}
	// 输出中的数组不得为 null：省略的 children、brief_required 与 required_artifacts 记为空数组。
	for _, u := range f.UnitTypes {
		u.Children, u.BriefRequired = nonNil(u.Children), nonNil(u.BriefRequired)
		g.UnitTypes = append(g.UnitTypes, UnitType(u))
	}
	for _, s := range f.Stages {
		s.RequiredArtifacts = nonNil(s.RequiredArtifacts)
		g.Stages = append(g.Stages, Stage(s))
	}
	return g, checkGenre(g), true
}

func nonNil(s []string) []string {
	if s == nil {
		return []string{}
	}
	return s
}

func checkGenre(g *Genre) []workspace.Problem {
	var out []workspace.Problem
	add := func(code, field, msg string) {
		out = append(out, workspace.Problem{Code: code, Severity: workspace.SeverityError, Field: field, Message: msg})
	}
	declared := map[string]bool{}
	for i, u := range g.UnitTypes {
		if declared[u.ID] {
			add(CodeDuplicateID, fmt.Sprintf("unit_types[%d].id", i), "单元类型 "+u.ID+" 重复")
		}
		declared[u.ID] = true
	}
	for i, u := range g.UnitTypes {
		for j, c := range u.Children {
			if !declared[c] {
				add(CodeUnknownUnitType, fmt.Sprintf("unit_types[%d].children[%d]", i, j), "子单元类型 "+c+" 未声明")
			}
		}
	}
	for i, s := range g.Stages {
		if s.ID == StageDone {
			add(CodeReservedStageID, fmt.Sprintf("stages[%d].id", i), "阶段 id "+StageDone+" 保留为生命周期终点，文体不得声明")
		}
	}
	for _, d := range duplicates(len(g.Stages), func(i int) string { return g.Stages[i].ID }) {
		add(CodeDuplicateID, fmt.Sprintf("stages[%d].id", d), "阶段 "+g.Stages[d].ID+" 重复")
	}
	for _, d := range duplicates(len(g.Rubric), func(i int) string { return g.Rubric[i].ID }) {
		add(CodeDuplicateID, fmt.Sprintf("rubric[%d].id", d), "评分项 "+g.Rubric[d].ID+" 重复")
	}
	if g.DefaultMethod != "" && !slices.Contains(BuiltinMethods(), g.DefaultMethod) {
		add(CodeUnknownMethod, "default_method", fmt.Sprintf("默认写作法 %s 不是内置写作法（%s）", g.DefaultMethod, strings.Join(BuiltinMethods(), "、")))
	}
	out = append(out, CheckRuleTypes(g.Rules)...)
	return withOrigin(out, builtinOrigin("genres", g.ID))
}

// FindPack 按 id 查找规范包：先找内置规范包，再找 root 下的工作区规范包。不存在时 ok 为 false。
func FindPack(root, id string) (p *Pack, problems []workspace.Problem, ok bool) {
	if doc, problems, ok := readBuiltin("packs", id); ok {
		if doc == nil {
			return nil, problems, true
		}
		p, problems = loadPack(doc, OriginBuiltin, "")
		return p, problems, true
	}
	rel := PackPath(id)
	if _, err := os.Stat(filepath.Join(root, filepath.FromSlash(rel))); errors.Is(err, fs.ErrNotExist) {
		return nil, nil, false
	}
	p, problems = LoadPackFile(root, rel)
	return p, problems, true
}

// PackPath 返回工作区规范包 id 的路径。
func PackPath(id string) string { return workspace.PacksPath + "/" + id + ".yaml" }

// LoadPackFile 读取并校验工作区规范包 rel（相对项目目录）。
func LoadPackFile(root, rel string) (*Pack, []workspace.Problem) {
	doc, problems := workspace.Read(root, rel)
	if doc == nil {
		return nil, problems
	}
	return loadPack(doc, OriginWorkspace, rel)
}

func loadPack(doc *yaml.Node, origin, rel string) (*Pack, []workspace.Problem) {
	if ps := workspace.FromSchema(rel, schema.Named("pack").ValidateNode(doc)); len(ps) > 0 {
		if origin == OriginBuiltin {
			withOrigin(ps, builtinOrigin("packs", schemaID(doc)))
		}
		return nil, ps
	}
	var f packFile
	Decode(doc, &f)
	p := &Pack{ID: f.ID, Title: f.Title, Description: f.Description, Style: f.Style, Glossary: f.Glossary, BannedTerms: f.BannedTerms,
		Rules: f.Rules, Waivers: f.Waivers, Rubric: f.Rubric, Methods: f.Methods, Origin: origin, Path: rel, ContextBudget: f.Budget, HTMLTheme: f.Theme, DisclosureTemplate: f.Disclosure}
	return p, checkPack(p)
}

func checkPack(p *Pack) []workspace.Problem {
	var out []workspace.Problem
	add := func(code, field, msg string) {
		out = append(out, workspace.Problem{Code: code, Severity: workspace.SeverityError, Path: p.Path, Field: field, Origin: ProblemOrigin(p), Message: msg})
	}
	if p.Origin == OriginWorkspace {
		if name := strings.TrimSuffix(path.Base(p.Path), ".yaml"); p.ID != name {
			add(CodePackIDMismatch, "id", fmt.Sprintf("id %s 与文件名 %s 不一致", p.ID, name))
		}
		if slices.Contains(BuiltinPacks(), p.ID) {
			add(CodePackIDConflict, "id", fmt.Sprintf("id %s 与内置规范包同名；请换一个 id 并改名文件", p.ID))
		}
	}
	for _, d := range duplicates(len(p.Glossary), func(i int) string { return p.Glossary[i].Term }) {
		add(CodeDuplicateID, fmt.Sprintf("glossary[%d].term", d), "词条 "+p.Glossary[d].Term+" 重复")
	}
	for _, d := range duplicates(len(p.BannedTerms), func(i int) string { return p.BannedTerms[i].Term }) {
		add(CodeDuplicateID, fmt.Sprintf("banned_terms[%d].term", d), "禁用词 "+p.BannedTerms[d].Term+" 重复")
	}
	for _, d := range duplicates(len(p.Rubric), func(i int) string { return p.Rubric[i].ID }) {
		add(CodeDuplicateID, fmt.Sprintf("rubric[%d].id", d), "评分项 "+p.Rubric[d].ID+" 重复")
	}
	for _, d := range duplicates(len(p.Methods), func(i int) string { return p.Methods[i].ID }) {
		add(CodeDuplicateID, fmt.Sprintf("methods[%d].id", d), "写作法 "+p.Methods[d].ID+" 重复")
	}
	for i, m := range p.Methods {
		checkMethod(m, fmt.Sprintf("methods[%d].", i), add)
	}
	for _, q := range CheckRuleTypes(p.Rules) {
		q.Path, q.Origin = p.Path, ProblemOrigin(p)
		out = append(out, q)
	}
	return out
}

// ProblemOrigin 返回规范包问题的出处说明：内置规范包没有工作区路径，用 id 指明出处；工作区规范包为空。
func ProblemOrigin(p *Pack) string {
	if p.Origin == OriginBuiltin {
		return builtinOrigin("packs", p.ID)
	}
	return ""
}

// schemaID 返回文档顶层的 id 字段，取不到时为空字符串。
func schemaID(doc *yaml.Node) string {
	m, _ := schema.Decode(doc).(map[string]any)
	id, _ := m["id"].(string)
	return id
}

// duplicates 返回与前面某一项重复的项的下标。
func duplicates(n int, key func(int) string) []int {
	seen := map[string]bool{}
	var out []int
	for i := range n {
		if seen[key(i)] {
			out = append(out, i)
		}
		seen[key(i)] = true
	}
	return out
}

// CheckRuleTypes 报告 rules 中未知的规则类型，按规则 id 排序；返回的问题不含 Path。
func CheckRuleTypes(rules map[string]Rule) []workspace.Problem {
	var out []workspace.Problem
	for _, id := range SortedRuleIDs(rules) {
		if t := rules[id].Type; t != "" && !slices.Contains(RuleTypes, t) {
			out = append(out, workspace.Problem{Code: CodeUnknownRuleType, Severity: workspace.SeverityError, Field: "rules." + id + ".type",
				Message: fmt.Sprintf("未知的规则类型 %s；已知的规则类型为 %s", t, strings.Join(RuleTypes, "、"))})
		}
	}
	return out
}

// SortedRuleIDs 返回 rules 的规则 id，按字母顺序排列。
func SortedRuleIDs(rules map[string]Rule) []string {
	ids := make([]string, 0, len(rules))
	for id := range rules {
		ids = append(ids, id)
	}
	slices.Sort(ids)
	return ids
}

// CheckWorkspacePacks 校验 root 下 packs/ 中的每个 .yaml 文件；packs/ 不存在不是问题。返回已校验的路径。
func CheckWorkspacePacks(root string) (checked []string, problems []workspace.Problem) {
	files, _ := filepath.Glob(filepath.Join(root, filepath.FromSlash(workspace.PacksPath), "*.yaml"))
	for _, f := range files {
		if strings.HasPrefix(filepath.Base(f), ".") {
			continue // 隐藏文件（如编辑器的临时文件）不是规范包
		}
		rel := workspace.PacksPath + "/" + filepath.Base(f)
		_, ps := LoadPackFile(root, rel)
		checked = append(checked, rel)
		problems = append(problems, ps...)
	}
	return checked, problems
}
