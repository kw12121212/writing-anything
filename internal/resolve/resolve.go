// Package resolve 按 文体（Genre）默认 → 规范包（Pack）→ 项目配置 的顺序合并工作区生效的设置，
// 并为每项结果记录来源的配置层（Config Layer）。合并结果只能由本包产生，消费方不得自行重新合并。
package resolve

import (
	"fmt"
	"maps"
	"slices"
	"strings"

	"github.com/kw12121212/writing-anything/internal/catalog"
	"github.com/kw12121212/writing-anything/internal/schema"
	"github.com/kw12121212/writing-anything/internal/workspace"
	"go.yaml.in/yaml/v3"
)

// 合并的错误码（Error Code）。已发布的错误码不得改名或复用。
const (
	CodeUnknownGenre        = "config.unknown-genre"
	CodeUnknownPack         = "config.unknown-pack"
	CodeMissingRuleType     = "rule.missing-type"
	CodeRuleTypeConflict    = "rule.type-conflict"
	CodeUnknownWaiverTarget = "rule.unknown-waiver-target"
)

// SourceProject 是项目配置层的来源标识；文体与规范包层分别为 genre:<id> 与 pack:<id>。
const SourceProject = "project"

// defaultSeverity 是没有任何配置层给出 severity 时规则的严重程度。
const defaultSeverity = "warning"

// Value 是一项设置的最终值及其来源层。
type Value struct {
	Value  any    `json:"value"`
	Source string `json:"source"`
}

// Rule 是合并后的一条规则（Rule）。
type Rule struct {
	Type     Value            `json:"type"`
	Severity Value            `json:"severity"`
	Params   map[string]Value `json:"params"`
	Enabled  bool             `json:"enabled"`
	Waiver   *Waiver          `json:"waiver"`
}

// Waiver 是关闭一条规则的豁免（Waiver）及其来源层。
type Waiver struct {
	Reason string `json:"reason"`
	Source string `json:"source"`
}

// Ref 是一个生效的文体或规范包。
type Ref struct {
	ID     string `json:"id"`
	Origin string `json:"origin"`
	Path   string `json:"path,omitempty"`
	// Category 是文体类别（Genre Category），只在文体声明了类别时出现。
	Category string `json:"category,omitempty"`
}

// 以下是带来源层的合并结果条目。
type (
	UnitType struct {
		catalog.UnitType
		Source string `json:"source"`
	}
	Stage struct {
		catalog.Stage
		Source string `json:"source"`
	}
	Term struct {
		catalog.Term
		Source string `json:"source"`
	}
	BannedTerm struct {
		catalog.BannedTerm
		Source string `json:"source"`
	}
	RubricItem struct {
		catalog.RubricItem
		Source string `json:"source"`
	}
	ScoredMetric struct {
		catalog.ScoredMetric
		Source string `json:"source"`
	}
	PacingCheck struct {
		catalog.PacingCheck
		Source string `json:"source"`
	}
	Style struct {
		Text   string `json:"text"`
		Source string `json:"source"`
	}
	// ExcludedStyle 是因文体类别不符而未生效的文风条目。
	ExcludedStyle struct {
		Text      string   `json:"text"`
		Source    string   `json:"source"`
		AppliesTo []string `json:"appliesTo"`
		Reason    string   `json:"reason"`
	}
)

// categoryTitles 是文体类别的中文写法。
var categoryTitles = map[string]string{catalog.CategoryArticle: "文章类", catalog.CategoryFiction: "小说类"}

func categoryNames(cs []string) string {
	names := make([]string, len(cs))
	for i, c := range cs {
		names[i] = fmt.Sprintf("%s（%s）", categoryTitles[c], c)
	}
	return strings.Join(names, "、")
}

// Resolved 是解析后配置（Resolved Config）。
type Resolved struct {
	Genre       *Ref             `json:"genre"`
	Packs       []Ref            `json:"packs"`
	UnitTypes   []UnitType       `json:"unitTypes"`
	Stages      []Stage          `json:"stages"`
	Rules       map[string]*Rule `json:"rules"`
	Glossary    []Term           `json:"glossary"`
	BannedTerms []BannedTerm     `json:"bannedTerms"`
	Style       []Style          `json:"style"`
	// ExcludedStyle 是按作品文体类别排除的文风条目及原因，按配置层顺序排列；没有时为空数组。
	ExcludedStyle []ExcludedStyle `json:"excludedStyle"`
	Rubric      []RubricItem     `json:"rubric"`
	// ContextBudget 是上下文预算（Context Budget）的最终取值与来源；没有配置层声明时为 DefaultContextBudget，来源为 default。
	ContextBudget Value `json:"contextBudget"`
	// HTMLTheme 是 HTML 导出的样式主题（HTML Theme）：元素名 → CSS 声明与来源；没有规范包覆盖的元素来自内置默认主题，来源为 default。
	HTMLTheme map[string]Value `json:"htmlTheme"`
	// DriftThreshold 是文风漂移（Style Drift）的阈值：规范包按 packs 的顺序覆盖；都没有声明时为 DefaultDriftThreshold，来源为 default。
	DriftThreshold Value `json:"driftThreshold"`
	// StyleProfile 是文风画像（Style Profile）：packs 中最后一个声明 style_profile 的规范包的取值与来源；没有时为 nil。
	StyleProfile *Value `json:"styleProfile"`
	// ScoredMetrics 是 AI 评分指标（Metric）：按 id 合并，后面的配置层整项替换前面的同名指标并保留其位置。
	ScoredMetrics []ScoredMetric `json:"scoredMetrics"`
	// PacingChecks 是节奏检查（Pacing Check）：只有文体能声明，按声明顺序输出；没有声明时为空数组。
	PacingChecks []PacingCheck `json:"pacingChecks"`
	// AIDisclosure 是 AI 参与说明的放置位置（项目配置的 ai_disclosure，默认 both）与模板（规范包的 ai_disclosure_template，默认为内置模板）。
	AIDisclosure Disclosure `json:"aiDisclosure"`
}

// Disclosure 是 AI 参与说明（AI-generated Content Label）的合并结果。
type Disclosure struct {
	Placement Value `json:"placement"`
	Template  Value `json:"template"`
}

// DefaultDisclosurePlacement 是项目配置没有 ai_disclosure 时的放置位置。
const DefaultDisclosurePlacement = "both"

// DefaultDriftThreshold 是没有任何配置层声明 drift_threshold 时的文风漂移阈值（标准差的倍数）。
const DefaultDriftThreshold = 2.0

// DefaultContextBudget 是没有任何配置层声明 context_budget 时的上下文预算（字符数）。
const DefaultContextBudget = 12000

// SourceDefault 是没有任何配置层给出取值时的来源标识。
const SourceDefault = "default"

// layer 是参与合并的一个配置层。
type layer struct {
	source  string
	path    string // 相对项目目录的产物路径；内置产物为空
	origin  string // 内置产物的出处说明；工作区产物为空
	rules   map[string]catalog.Rule
	waivers []catalog.Waiver
}

// projectFile 是 config.yaml 中参与合并的字段。
type projectFile struct {
	DefaultGenre string                  `json:"default_genre"`
	Packs        []string                `json:"packs"`
	Rules        map[string]catalog.Rule `json:"rules"`
	Waivers      []catalog.Waiver        `json:"waivers"`
	Budget       int                     `json:"context_budget"`
	Disclosure   string                  `json:"ai_disclosure"`
}

// Resolve 读取 root 下的工作区配置并合并。存在 error 级问题时返回 nil。
func Resolve(root string) (*Resolved, []workspace.Problem) {
	doc, problems := workspace.Read(root, workspace.ConfigPath)
	if doc == nil {
		return nil, problems
	}
	if ps := schema.KindFor(workspace.ConfigPath).ValidateNode(doc); len(ps) > 0 {
		return nil, workspace.FromSchema(workspace.ConfigPath, ps)
	}
	return merge(root, doc, "", nil)
}

// ForGenre 与 Resolve 相同，但以文体 genre 作为文体层，而不是 config.yaml 的 default_genre：作品按其自己的文体写作。
func ForGenre(root, genre string) (*Resolved, []workspace.Problem) {
	doc, problems := workspace.Read(root, workspace.ConfigPath)
	if doc == nil {
		return nil, problems
	}
	if ps := schema.KindFor(workspace.ConfigPath).ValidateNode(doc); len(ps) > 0 {
		return nil, workspace.FromSchema(workspace.ConfigPath, ps)
	}
	return merge(root, doc, genre, nil)
}

// Layer 是作品额外的规范包层（如所属系列的 packs）：Path 为声明它的产物路径，诊断指向它。
type Layer struct {
	Path  string
	Packs []string
}

// ForWork 与 ForGenre 相同，并在 config.yaml 的 packs 之后、项目配置之前依次应用 extra 中的规范包（已应用过的不重复）：
// 系列成员作品继承系列的规范包。extra 为 nil 时与 ForGenre 相同。
func ForWork(root, genre string, extra *Layer) (*Resolved, []workspace.Problem) {
	doc, problems := workspace.Read(root, workspace.ConfigPath)
	if doc == nil {
		return nil, problems
	}
	if ps := schema.KindFor(workspace.ConfigPath).ValidateNode(doc); len(ps) > 0 {
		return nil, workspace.FromSchema(workspace.ConfigPath, ps)
	}
	return merge(root, doc, genre, extra)
}

// Check 做工作区整体校验中与文体、规范包有关的部分：校验 packs/ 下的每个规范包；config.yaml 本身
// 合法时，再检查它引用的文体与规范包以及合并问题。config.yaml 自身的问题由 workspace.Check 报告。
func Check(root string) (checked []string, problems []workspace.Problem) {
	checked, problems = catalog.CheckWorkspacePacks(root)
	doc, ps := workspace.Read(root, workspace.ConfigPath)
	if doc == nil || len(ps) > 0 || len(schema.KindFor(workspace.ConfigPath).ValidateNode(doc)) > 0 {
		return checked, problems
	}
	_, ps = merge(root, doc, "", nil)
	for _, p := range ps {
		// 被引用的工作区规范包已在上面校验过，不重复报告。
		if !slices.Contains(problems, p) {
			problems = append(problems, p)
		}
	}
	return checked, problems
}

// CheckArtifact 对已通过 Schema 校验的单个产物 rel 做与文体、规范包有关的检查：规范包的 id、重复项与
// 规则类型，config.yaml 中规则的类型。引用与合并问题涉及多个产物，只由 Check 与 Resolve 报告。
func CheckArtifact(root, rel string) []workspace.Problem {
	switch schema.KindFor(rel).Name {
	case "pack":
		_, ps := catalog.LoadPackFile(root, rel)
		return ps
	case "config":
		doc, ps := workspace.Read(root, rel)
		if doc == nil {
			return ps
		}
		var cfg projectFile
		catalog.Decode(doc, &cfg)
		ps = catalog.CheckRuleTypes(cfg.Rules)
		for i := range ps {
			ps[i].Path = rel
		}
		return ps
	}
	return nil
}

// merge 合并各配置层；genre 非空时以它作为文体层，否则用 config.yaml 的 default_genre。
func merge(root string, doc *yaml.Node, genre string, extra *Layer) (*Resolved, []workspace.Problem) {
	var cfg projectFile
	catalog.Decode(doc, &cfg)
	if genre != "" {
		cfg.DefaultGenre = genre
	}
	var problems []workspace.Problem
	errorAt := func(code, path, field, msg string) {
		problems = append(problems, workspace.Problem{Code: code, Severity: workspace.SeverityError, Path: path, Field: field, Message: msg})
	}
	layerError := func(l layer, code, field, msg string) {
		problems = append(problems, workspace.Problem{Code: code, Severity: workspace.SeverityError, Path: l.path, Field: field, Origin: l.origin, Message: msg})
	}
	r := &Resolved{Packs: []Ref{}, UnitTypes: []UnitType{}, Stages: []Stage{}, Rules: map[string]*Rule{},
		Glossary: []Term{}, BannedTerms: []BannedTerm{}, Style: []Style{}, ExcludedStyle: []ExcludedStyle{}, Rubric: []RubricItem{}, ScoredMetrics: []ScoredMetric{},
		PacingChecks: []PacingCheck{},
		ContextBudget: Value{DefaultContextBudget, SourceDefault}, HTMLTheme: map[string]Value{}, DriftThreshold: Value{DefaultDriftThreshold, SourceDefault},
		AIDisclosure: Disclosure{Placement: Value{DefaultDisclosurePlacement, SourceDefault}, Template: Value{catalog.DefaultDisclosureTemplate, SourceDefault}}}
	for _, k := range catalog.HTMLThemeKeys {
		r.HTMLTheme[k] = Value{catalog.DefaultHTMLTheme[k], SourceDefault}
	}
	budget := func(v int, source string) {
		if v > 0 {
			r.ContextBudget = Value{v, source}
		}
	}
	var layers []layer
	category := "" // 生效文体的类别；空表示没有文体或文体未声明类别，此时所有文风条目都生效
	var pacing []catalog.PacingCheck
	var pacingLayer *layer
	rubric := map[string]int{}
	addRubric := func(items []catalog.RubricItem, source string) {
		for _, it := range items {
			if i, ok := rubric[it.ID]; ok {
				r.Rubric[i] = RubricItem{it, source}
				continue
			}
			rubric[it.ID] = len(r.Rubric)
			r.Rubric = append(r.Rubric, RubricItem{it, source})
		}
	}
	metrics := map[string]int{}
	addMetrics := func(ms []catalog.ScoredMetric, source string) {
		for _, m := range ms {
			if i, ok := metrics[m.ID]; ok {
				r.ScoredMetrics[i] = ScoredMetric{m, source}
				continue
			}
			metrics[m.ID] = len(r.ScoredMetrics)
			r.ScoredMetrics = append(r.ScoredMetrics, ScoredMetric{m, source})
		}
	}

	if cfg.DefaultGenre != "" {
		g, ps, ok := catalog.LoadGenre(cfg.DefaultGenre)
		problems = append(problems, ps...)
		switch {
		case !ok:
			errorAt(CodeUnknownGenre, workspace.ConfigPath, "default_genre",
				fmt.Sprintf("文体 %s 不存在；可用的文体为 %s", cfg.DefaultGenre, list(catalog.BuiltinGenres())))
		case g != nil:
			source := "genre:" + g.ID
			r.Genre = &Ref{ID: g.ID, Origin: catalog.OriginBuiltin, Category: g.Category}
			category = g.Category
			for _, u := range g.UnitTypes {
				r.UnitTypes = append(r.UnitTypes, UnitType{u, source})
			}
			for _, s := range g.Stages {
				r.Stages = append(r.Stages, Stage{s, source})
			}
			addRubric(g.Rubric, source)
			addMetrics(g.ScoredMetrics, source)
			for _, c := range g.PacingChecks {
				r.PacingChecks = append(r.PacingChecks, PacingCheck{c, source})
			}
			budget(g.ContextBudget, source)
			layers = append(layers, layer{source: source, origin: "内置文体 " + g.ID, rules: g.Rules})
			// 节奏检查引用的指标可以来自规范包：合并完全部指标层之后再校验，问题记在声明它的文体层。
			pacingLayer = &layer{source: source, path: "", origin: "内置文体 " + g.ID}
			pacing = g.PacingChecks
		}
	}

	packs := cfg.Packs
	if len(packs) == 0 {
		packs = []string{catalog.DefaultPack}
	}
	type use struct {
		id, path, field string
	}
	var uses []use
	for i, id := range packs {
		uses = append(uses, use{id, workspace.ConfigPath, fmt.Sprintf("packs[%d]", i)})
	}
	if extra != nil {
		for i, id := range extra.Packs {
			if !slices.Contains(packs, id) {
				uses = append(uses, use{id, extra.Path, fmt.Sprintf("packs[%d]", i)})
			}
		}
	}
	glossary, banned := map[string]int{}, map[string]int{}
	for _, us := range uses {
		id := us.id
		p, ps, ok := catalog.FindPack(root, id)
		problems = append(problems, ps...)
		if !ok {
			errorAt(CodeUnknownPack, us.path, us.field,
				fmt.Sprintf("规范包 %s 不存在：既不是内置规范包（%s），也没有 %s", id, list(catalog.BuiltinPacks()), catalog.PackPath(id)))
		}
		if p == nil {
			continue
		}
		source := "pack:" + p.ID
		r.Packs = append(r.Packs, Ref{ID: p.ID, Origin: p.Origin, Path: p.Path})
		for _, s := range p.Style {
			if s.AppliesToCategory(category) {
				r.Style = append(r.Style, Style{s.Text, source})
				continue
			}
			r.ExcludedStyle = append(r.ExcludedStyle, ExcludedStyle{Text: s.Text, Source: source, AppliesTo: s.AppliesTo,
				Reason: fmt.Sprintf("只适用于%s文体，当前文体 %s 属于%s", categoryNames(s.AppliesTo), r.Genre.ID, categoryNames([]string{category}))})
		}
		for _, t := range p.Glossary {
			if j, ok := glossary[t.Term]; ok {
				r.Glossary[j] = Term{t, source}
				continue
			}
			glossary[t.Term] = len(r.Glossary)
			r.Glossary = append(r.Glossary, Term{t, source})
		}
		for _, t := range p.BannedTerms {
			if j, ok := banned[t.Term]; ok {
				r.BannedTerms[j] = BannedTerm{t, source}
				continue
			}
			banned[t.Term] = len(r.BannedTerms)
			r.BannedTerms = append(r.BannedTerms, BannedTerm{t, source})
		}
		addRubric(p.Rubric, source)
		addMetrics(p.ScoredMetrics, source)
		if p.StyleProfile != nil {
			r.StyleProfile = &Value{p.StyleProfile, source}
		}
		budget(p.ContextBudget, source)
		if p.DriftThreshold > 0 {
			r.DriftThreshold = Value{p.DriftThreshold, source}
		}
		for k, v := range p.HTMLTheme {
			r.HTMLTheme[k] = Value{v, source}
		}
		if p.DisclosureTemplate != "" {
			r.AIDisclosure.Template = Value{p.DisclosureTemplate, source}
		}
		layers = append(layers, layer{source: source, path: p.Path, origin: catalog.ProblemOrigin(p), rules: p.Rules, waivers: p.Waivers})
	}

	for _, q := range catalog.CheckRuleTypes(cfg.Rules) {
		q.Path = workspace.ConfigPath
		problems = append(problems, q)
	}
	budget(cfg.Budget, SourceProject)
	if cfg.Disclosure != "" {
		r.AIDisclosure.Placement = Value{cfg.Disclosure, SourceProject}
	}
	layers = append(layers, layer{source: SourceProject, path: workspace.ConfigPath, rules: cfg.Rules, waivers: cfg.Waivers})

	for _, l := range layers {
		for _, id := range catalog.SortedRuleIDs(l.rules) {
			in, cur := l.rules[id], r.Rules[id]
			switch {
			case cur == nil && in.Type == "":
				layerError(l, CodeMissingRuleType, "rules."+id, fmt.Sprintf("规则 %s 是首次定义，必须给出 type", id))
				continue
			case cur == nil:
				cur = &Rule{Type: Value{in.Type, l.source}, Severity: Value{defaultSeverity, l.source}, Params: map[string]Value{}, Enabled: true}
				r.Rules[id] = cur
			case in.Type != "" && in.Type != cur.Type.Value:
				layerError(l, CodeRuleTypeConflict, "rules."+id+".type",
					fmt.Sprintf("规则 %s 的 type 已由 %s 定为 %v，不能改为 %s", id, cur.Type.Source, cur.Type.Value, in.Type))
				continue
			}
			if in.Severity != "" {
				cur.Severity = Value{in.Severity, l.source}
			}
			for _, k := range slices.Sorted(maps.Keys(in.Params)) {
				cur.Params[k] = Value{in.Params[k], l.source}
			}
		}
	}
	for _, l := range layers {
		for i, w := range l.waivers {
			rule := r.Rules[w.Rule]
			if rule == nil {
				layerError(l, CodeUnknownWaiverTarget, fmt.Sprintf("waivers[%d].rule", i), fmt.Sprintf("豁免的规则 %s 不存在", w.Rule))
				continue
			}
			rule.Enabled, rule.Waiver = false, &Waiver{w.Reason, l.source}
		}
	}
	if pacingLayer != nil {
		catalog.CheckPacingRefs(pacing, metricDefs(r.ScoredMetrics), func(code, field, msg string) {
			layerError(*pacingLayer, code, field, msg)
		})
	}
	for _, p := range problems {
		if p.Severity == workspace.SeverityError {
			return nil, problems
		}
	}
	return r, problems
}

func list(ids []string) string {
	if len(ids) == 0 {
		return "无"
	}
	return strings.Join(ids, "、")
}

func metricDefs(ms []ScoredMetric) []catalog.ScoredMetric {
	out := make([]catalog.ScoredMetric, len(ms))
	for i, m := range ms {
		out[i] = m.ScoredMetric
	}
	return out
}
