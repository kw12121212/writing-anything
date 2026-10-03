// Package plan 管理单元目录下的规划（Plan）plan.yaml：按规划类写作法（Method Template）的步骤分步产出，
// 每步记录规划文本、关联的素材（Source）与产物链接（Plan Link），经作者确认（plan confirm）后才能进入下一步。
// 人物、场景与场景契约等产物由既有命令（canon add、unit add、scene contract）落地，plan step 用 --link 记录引用，
// 确认时按步骤的规划产物（Planning Product）校验链接可解析且类型正确。本包不 import internal/cli；只返回结构化
// 的问题，由命令层转换为诊断（Diagnostic）。
package plan

import (
	"fmt"
	"os"
	"path/filepath"
	"reflect"
	"slices"
	"strings"
	"time"
	"unicode/utf8"

	"github.com/kw12121212/writing-anything/internal/brief"
	"github.com/kw12121212/writing-anything/internal/canon"
	"github.com/kw12121212/writing-anything/internal/catalog"
	"github.com/kw12121212/writing-anything/internal/outline"
	"github.com/kw12121212/writing-anything/internal/schema"
	"github.com/kw12121212/writing-anything/internal/source"
	"github.com/kw12121212/writing-anything/internal/work"
	"github.com/kw12121212/writing-anything/internal/workspace"
	"go.yaml.in/yaml/v3"
)

// 规划的错误码（Error Code）。已发布的错误码不得改名或复用。
const (
	CodeNotPlanning            = "plan.not-planning"
	CodeUnknownStep            = "plan.unknown-step"
	CodeStepOrder              = "plan.step-order"
	CodeUnknownSource          = "plan.unknown-source"
	CodeUnknownLink            = "plan.unknown-link"
	CodeMissingLinks           = "plan.missing-links"
	CodeIncompleteConfirmation = "plan.incomplete-confirmation"
	CodeNotDrafted             = "plan.not-drafted"
	CodeNotConfirmed           = "plan.not-confirmed"
	CodeMethodChanged          = "plan.method-changed"
	CodeAnchoredValue          = "plan.anchored-value"
)

// FileName 是规划文件名；Path 返回单元目录 unitDir 的规划路径。
const FileName = "plan.yaml"

func Path(unitDir string) string { return unitDir + "/" + FileName }

// 规划产物（Planning Product）：规划步骤的产物链接类型，声明在写作法模板的 steps 上。
const (
	ProductCharacters     = "characters"      // 设定集（Canon）的人物条目（人物卡）
	ProductScenes         = "scenes"          // 单元树（Unit Tree）中的场景单元
	ProductSceneContracts = "scene-contracts" // 场景契约（Scene Contract）
)

// TimeFormat 是确认记录（Confirmation Record）中确认时间的格式（UTC）。
const TimeFormat = "2006-01-02T15:04:05Z"

// Step 是规划（Plan）中一个已起草的规划步骤（Planning Step）记录；未设置的可选字段为 nil。
type Step struct {
	ID                string   `json:"id"`
	Title             string   `json:"title"`
	Text              string   `json:"text"`
	Sources           []string `json:"sources"`
	Links             []string `json:"links"`
	ConfirmedByAuthor bool     `json:"confirmed_by_author"`
	Confirmation      *string  `json:"confirmation"`
	ConfirmedAt       *string  `json:"confirmed_at"`
}

// File 是一份已通过 Schema 校验的规划。
type File struct {
	Method string `json:"method"`
	Steps  []Step `json:"steps"`
}

func errorAt(code, path, field, msg string) workspace.Problem {
	return workspace.Problem{Code: code, Severity: workspace.SeverityError, Path: path, Field: field, Message: msg}
}

func str(v string) *yaml.Node { return &yaml.Node{Kind: yaml.ScalarNode, Tag: "!!str", Value: v} }

func plain(n *yaml.Node) bool { return n.Anchor == "" && n.Kind != yaml.AliasNode }

func seqNode(v []string) *yaml.Node {
	n := &yaml.Node{Kind: yaml.SequenceNode, Tag: "!!seq", Style: yaml.FlowStyle}
	for _, s := range v {
		n.Content = append(n.Content, str(s))
	}
	return n
}

func unknownSource(id string) string {
	return "素材（Source）" + id + " 未登记；规划步骤只能关联已用 wa-cli source add 登记的素材"
}

// load 读取并校验 rel 处的规划（Schema）；文件不存在时返回 nil 与 nil；途经符号链接或不是普通文件时按
// 不存在处理并报告 artifact.not-found（与 canon.yaml 一致）。
func load(root, rel string) (*yaml.Node, *File, []workspace.Problem) {
	if _, ok, _ := workspace.ReadRegular(root, rel); !ok {
		if notRegular(root, rel) {
			return nil, nil, []workspace.Problem{errorAt(schema.CodeNotFound, rel, "", "plan.yaml 途经符号链接或不是普通文件，按不存在处理")}
		}
		return nil, nil, nil
	}
	doc, ps := workspace.Read(root, rel)
	if doc == nil {
		return nil, nil, ps
	}
	if ps := schema.Named("plan").ValidateNode(doc); len(ps) > 0 {
		return nil, nil, workspace.FromSchema(rel, ps)
	}
	var f File
	catalog.Decode(doc, &f)
	return doc, &f, nil
}

// notRegular 报告 rel 是否存在但不是普通文件（如符号链接）。单元目录是作品能加载的真实目录
//（validated-path-segments），只检查最后一级文件。
func notRegular(root, rel string) bool {
	info, err := os.Lstat(filepath.Join(root, filepath.FromSlash(rel)))
	return err == nil && !info.Mode().IsRegular()
}

// stepsNode 返回文档中 steps 序列节点；途经锚点、别名或合并键时返回 nil（Schema 保证 steps 存在）。
func stepsNode(doc *yaml.Node) *yaml.Node {
	m := doc.Content[0]
	if !plain(m) {
		return nil
	}
	var v *yaml.Node
	for i := 0; i+1 < len(m.Content); i += 2 {
		if m.Content[i].ShortTag() == "!!merge" {
			return nil
		}
		if m.Content[i].Value == "steps" {
			v = m.Content[i+1]
		}
	}
	if v == nil || !plain(v) {
		return nil
	}
	return v
}

// methodIndex 返回步骤 id 在写作法 steps 中的位置；不存在时为 -1。
func methodIndex(m *catalog.Method, id string) int {
	return slices.IndexFunc(m.Steps, func(s catalog.MethodPart) bool { return s.ID == id })
}

// CurrentStep 返回第一个未经作者确认的步骤在写作法 steps 中的位置；全部确认时为 -1，没有记录时为 0。
// 要求步骤记录构成写作法 steps 的前缀（checkOrder 通过）。
func CurrentStep(f *File, m *catalog.Method) int {
	if f == nil {
		return 0
	}
	for i := range m.Steps {
		if i >= len(f.Steps) || !f.Steps[i].ConfirmedByAuthor {
			return i
		}
	}
	return -1
}

// checkOrder 检查已通过 Schema 校验的规划：步骤记录必须是所选写作法 steps 的前缀（每个 id 都对应该位置的方法
// 步骤），且已确认步骤都排在未确认步骤之前。违反时对每个出错位置报告 plan.step-order。
func checkOrder(rel string, f *File, m *catalog.Method) (problems []workspace.Problem) {
	if f == nil {
		return nil
	}
	for i, r := range f.Steps {
		if i >= len(m.Steps) || m.Steps[i].ID != r.ID {
			want := "（写作法步骤已尽）"
			if i < len(m.Steps) {
				want = m.Steps[i].ID
			}
			problems = append(problems, errorAt(CodeStepOrder, rel, fmt.Sprintf("steps[%d]", i),
				fmt.Sprintf("步骤记录必须是写作法 %s 的步骤按顺序组成的前缀；steps[%d] 为 %s，该位置应为 %s", m.ID, i, r.ID, want)))
		}
	}
	for i := 1; i < len(f.Steps); i++ {
		if !f.Steps[i-1].ConfirmedByAuthor && f.Steps[i].ConfirmedByAuthor {
			problems = append(problems, errorAt(CodeStepOrder, rel, fmt.Sprintf("steps[%d]", i),
				"已确认的步骤必须排在未确认的步骤之前；上一步未经作者确认时，后面的步骤不能已确认"))
		}
	}
	return problems
}

// resolver 解析产物链接（Plan Link）：characters 为设定集条目 id，scenes 为场景单元引用，
// scene-contracts 形如 <场景单元引用>:<大纲节 id>。每个作品构造一次（load-once-per-command）。
type resolver struct {
	root        string
	w           *work.Work
	characters  map[string]bool
	canonUsable bool
	unitsByRef  map[string]*work.Unit
	scenes      map[string]bool
}

func newResolver(root string, w *work.Work) *resolver {
	r := &resolver{root: root, w: w, unitsByRef: map[string]*work.Unit{}, scenes: map[string]bool{}}
	r.characters, r.canonUsable = canon.CharacterIDs(root, w)
	for _, u := range w.Units() {
		r.unitsByRef[u.Ref] = u
		if u.Type == outline.SceneType {
			r.scenes[u.Ref] = true
		}
	}
	return r
}

// hasContract 报告大纲节是否声明了至少一个场景契约（Scene Contract）字段。
func hasContract(s outline.Section) bool {
	return len(s.Characters) > 0 || len(s.Beats) > 0 || len(s.StateChanges) > 0 || len(s.Plants) > 0 ||
		len(s.Recovers) > 0 || len(s.Causes) > 0 || len(s.Events) > 0 ||
		s.Reveal != "" || s.Pov != "" || s.Location != ""
}

// check 报告链接在产物 product 下是否可解析且类型正确。
func (r *resolver) check(product, link string) bool {
	switch product {
	case ProductCharacters:
		return r.canonUsable && r.characters[link]
	case ProductScenes:
		return r.scenes[link]
	case ProductSceneContracts:
		ref, section, found := strings.Cut(link, ":")
		if !found || !r.scenes[ref] {
			return false
		}
		o := outline.Loadable(r.root, r.w.UnitDir(r.unitsByRef[ref]))
		if o == nil {
			return false
		}
		return slices.ContainsFunc(o.Sections, func(s outline.Section) bool { return s.ID == section && hasContract(s) })
	}
	return false
}

func unknownLink(product, link string) string {
	switch product {
	case ProductCharacters:
		return "产物链接 " + link + " 不是本作品设定集（Canon）中类型为 character 的条目；人物步骤的产物经 wa-cli canon add 写入人物卡后用 --link 关联"
	case ProductScenes:
		return "产物链接 " + link + " 不是本作品单元树（Unit Tree）中的场景单元；场景列表的产物经 wa-cli unit add 登记后用 --link 关联"
	case ProductSceneContracts:
		return "产物链接 " + link + " 不是本作品场景单元的大纲节（形如 <场景单元引用>:<大纲节 id>，且该节声明了场景契约字段）；场景详述的产物经 wa-cli scene contract 写入后用 --link 关联"
	}
	return "产物链接 " + link + " 无法解析"
}

func productHint(product string) string {
	switch product {
	case ProductCharacters:
		return "先用 wa-cli canon add 写入人物卡，再在 wa-cli plan step 用 --link 关联条目 id"
	case ProductScenes:
		return "先用 wa-cli unit add 登记场景单元，再在 wa-cli plan step 用 --link 关联单元引用"
	case ProductSceneContracts:
		return "先用 wa-cli scene contract 写入场景契约，再在 wa-cli plan step 用 --link 关联 <场景单元引用>:<大纲节 id>"
	}
	return ""
}

// checkRefsUsing 检查步骤记录的引用：sources 已登记；声明了规划产物（Planning Product）的步骤每个 link 都可解析
// 且类型正确。要求步骤记录与写作法步骤对位（checkOrder 通过）；对位不上的记录只检查 sources。
func checkRefsUsing(root, rel string, f *File, w *work.Work, m *catalog.Method, r *resolver) (problems []workspace.Problem) {
	if f == nil {
		return nil
	}
	for i, step := range f.Steps {
		for j, id := range step.Sources {
			if !source.Registered(root, id) {
				problems = append(problems, errorAt(CodeUnknownSource, rel, fmt.Sprintf("steps[%d].sources[%d]", i, j), unknownSource(id)))
			}
		}
		if i >= len(m.Steps) || m.Steps[i].ID != step.ID {
			continue
		}
		for j, link := range step.Links {
			if !r.check(m.Steps[i].Product, link) {
				problems = append(problems, errorAt(CodeUnknownLink, rel, fmt.Sprintf("steps[%d].links[%d]", i, j), unknownLink(m.Steps[i].Product, link)))
			}
		}
	}
	return problems
}

// checkText 检查写入产物的文本参数是合法的 UTF-8（workspace-artifacts/yaml-text-utf8）。
func checkText(field, v string) []workspace.Problem {
	if !utf8.ValidString(v) {
		return []workspace.Problem{{Code: "input.invalid-value", Severity: workspace.SeverityError, Field: field,
			Message: "参数 " + field + " 不是 UTF-8 文本"}}
	}
	return nil
}

// split 把规划的问题分为自身问题（顺序违反）与悬空引用（sources、links），不改变级别。
func split(ps []workspace.Problem) (own, dangling []workspace.Problem) {
	for _, p := range ps {
		if p.Code == CodeStepOrder {
			own = append(own, p)
			continue
		}
		dangling = append(dangling, p)
	}
	return own, dangling
}

// downgrade 把悬空引用降为 warning（workspace-artifacts/cross-artifact-references）：修改规划时，
// 已有步骤的悬空引用不阻止写入其他步骤。
func downgrade(ps []workspace.Problem) []workspace.Problem {
	for i := range ps {
		ps[i].Severity = workspace.SeverityWarning
	}
	return ps
}

// orderProblem 是 plan step / plan confirm 的顺序诊断：message 指出当前应处理的步骤 id。
func orderProblem(m *catalog.Method, cur int) workspace.Problem {
	p := workspace.Problem{Code: CodeStepOrder, Severity: workspace.SeverityError, Field: "step",
		Message: "步骤顺序不对；全部步骤都已确认"}
	if cur >= 0 && cur < len(m.Steps) {
		p.Message = fmt.Sprintf("上一步骤未经作者确认时不能写或确认后面的步骤；当前步骤是 %s", m.Steps[cur].ID)
	}
	return p
}

// recheck 对编码前的文档做写前复查：编码、重新解析、Schema 校验、顺序与引用检查。paramAt 不为负时，该步骤的
// 问题以参数名表示、不填路径（next-rechecks-problem）。返回复查后的规划、编码结果、阻止写入的问题，以及只在
// 成功时关心的其他步骤悬空引用（warning）。
func recheck(root, rel string, doc *yaml.Node, w *work.Work, m *catalog.Method, paramAt int, r *resolver) (*File, []byte, []workspace.Problem, []workspace.Problem) {
	data, err := workspace.Encode(doc)
	if err != nil {
		return nil, nil, []workspace.Problem{errorAt(workspace.CodeWriteFailed, rel, "", "生成规划失败："+err.Error())}, nil
	}
	written, p := schema.Parse(data)
	if p != nil {
		p.Line = 0
		return nil, nil, workspace.FromSchema(rel, []schema.Problem{*p}), nil
	}
	if ps := schema.Named("plan").ValidateNode(written); len(ps) > 0 {
		out := workspace.FromSchema(rel, ps)
		mapParam(out, paramAt)
		return nil, nil, out, nil
	}
	var f File
	catalog.Decode(written, &f)
	own, dangling := split(slices.Concat(checkOrder(rel, &f, m), checkRefsUsing(root, rel, &f, w, m, r)))
	// 本次写入或确认的步骤的问题以参数名表示、不填路径；其他步骤的问题保留产物内路径。
	own = mapParam(own, paramAt)
	if len(own) > 0 {
		return nil, nil, own, nil
	}
	return &f, data, nil, downgrade(dangling)
}

// mapParam 把第 paramAt 个步骤的问题改为参数问题：不填 path，字段去掉 steps[paramAt]. 前缀；paramAt 为 -1 时原样返回。
func mapParam(ps []workspace.Problem, paramAt int) []workspace.Problem {
	if paramAt < 0 {
		return ps
	}
	prefix := fmt.Sprintf("steps[%d]", paramAt)
	for i := range ps {
		if ps[i].Field == prefix || strings.HasPrefix(ps[i].Field, prefix+".") {
			ps[i].Path, ps[i].Line = "", 0
			ps[i].Field = strings.TrimPrefix(ps[i].Field, prefix+".")
		}
	}
	return ps
}

// StepInput 是 plan step 的参数。
type StepInput struct {
	Step            string
	Text            string
	Sources         []string // nil 表示保留既有取值；给出时（含空数组）整体替换
	Links           []string // 同上
	Redo            bool     // 修订已确认的步骤：重置该步并删除其后全部记录
	AuthorConfirmed bool     // --confirmed-by-author；Redo 时必须给出
	Confirmation    string   // --confirmation；Redo 时必须给出
}

// WriteStep 写入规划步骤的草稿：按 plan-step 的规则校验顺序与参数，只改该步骤的记录并保留其他内容与 YAML
// 注释。ensure 在全部检查通过后、写入前调用（创建缺失的单元目录，其路径并入 changedPaths）。返回复查后的
// 规划、变更的路径与问题（成功时只含其他步骤悬空引用的 warning）。
func WriteStep(root string, w *work.Work, unitDir string, m *catalog.Method, in StepInput, ensure func() ([]string, []workspace.Problem)) (*File, []string, []workspace.Problem) {
	for _, ps := range [][]workspace.Problem{checkText("text", in.Text), checkText("confirmation", in.Confirmation)} {
		if len(ps) > 0 {
			return nil, nil, ps
		}
	}
	at := methodIndex(m, in.Step)
	if at < 0 {
		return nil, nil, []workspace.Problem{errorAt(CodeUnknownStep, "", "step",
			"步骤 " + in.Step + " 不在写作法 " + m.ID + " 的规划步骤中")}
	}
	if strings.TrimSpace(in.Text) == "" {
		return nil, nil, []workspace.Problem{{Code: "input.invalid-value", Severity: workspace.SeverityError, Field: "text",
			Message: "规划文本 --text 不能为空或只含空白"}}
	}
	rel := Path(unitDir)
	doc, f, ps := load(root, rel)
	if len(ps) > 0 {
		return nil, nil, ps
	}
	if f != nil && f.Method != m.ID {
		return nil, nil, []workspace.Problem{errorAt(CodeMethodChanged, rel, "method",
			fmt.Sprintf("规划记录的写作法是 %s，与当前选用的 %s 不一致；请先统一写作法再继续", f.Method, m.ID))}
	}
	// 已有记录的顺序问题阻止写入（自身问题，cross-artifact-references 的例外）。
	if own, _ := split(checkOrder(rel, f, m)); len(own) > 0 {
		return nil, nil, own
	}
	if in.Redo {
		if f == nil || at >= len(f.Steps) || !f.Steps[at].ConfirmedByAuthor {
			return nil, nil, []workspace.Problem{{Code: CodeNotConfirmed, Severity: workspace.SeverityError, Field: "redo",
				Message: "步骤 " + in.Step + " 还未经作者确认，不需要 --redo；直接改写当前步骤即可"}}
		}
		if !in.AuthorConfirmed || strings.TrimSpace(in.Confirmation) == "" {
			return nil, nil, []workspace.Problem{{Code: CodeIncompleteConfirmation, Severity: workspace.SeverityError, Field: "confirmation",
				Message: "修订已确认的步骤必须由作者确认：--redo 要与 --confirmed-by-author 与非空白的 --confirmation 同时给出"}}
		}
	} else if cur := CurrentStep(f, m); at != cur {
		return nil, nil, []workspace.Problem{orderProblem(m, cur)}
	}
	for i, id := range in.Sources {
		if !source.Registered(root, id) {
			return nil, nil, []workspace.Problem{errorAt(CodeUnknownSource, "", fmt.Sprintf("sources[%d]", i), unknownSource(id))}
		}
	}
	product := m.Steps[at].Product
	if product == "" && in.Links != nil {
		return nil, nil, []workspace.Problem{{Code: "input.invalid-value", Severity: workspace.SeverityError, Field: "link",
			Message: "步骤 " + in.Step + " 没有声明规划产物（Planning Product），不接受 --link"}}
	}
	r := newResolver(root, w)
	for i, link := range in.Links {
		if !r.check(product, link) {
			return nil, nil, []workspace.Problem{errorAt(CodeUnknownLink, "", fmt.Sprintf("links[%d]", i), unknownLink(product, link))}
		}
	}
	if doc == nil {
		doc = &yaml.Node{Kind: yaml.DocumentNode, HeadComment: "规划（Plan）；请用 wa-cli plan step 写入步骤、wa-cli plan confirm 记录作者确认。", Content: []*yaml.Node{{Kind: yaml.MappingNode, Content: []*yaml.Node{
			str("schema_version"), {Kind: yaml.ScalarNode, Tag: "!!int", Value: "1"},
			str("method"), str(m.ID),
			str("steps"), {Kind: yaml.SequenceNode, Tag: "!!seq"},
		}}}}
	}
	steps := stepsNode(doc)
	if steps == nil {
		return nil, nil, []workspace.Problem{errorAt(CodeAnchoredValue, rel, "steps",
			"steps 经由 YAML 锚点、别名或合并键得到，wa-cli 无法只修改一项；请先去掉锚点、别名与合并键")}
	}
	before := schema.Decode(doc)
	// 合并出该步骤的新记录：未给出的列表保留既有取值。
	record := Step{ID: m.Steps[at].ID, Title: m.Steps[at].Title, Text: in.Text, Sources: in.Sources, Links: in.Links}
	if f != nil && at < len(f.Steps) {
		if in.Sources == nil {
			record.Sources = f.Steps[at].Sources
		}
		if in.Links == nil {
			record.Links = f.Steps[at].Links
		}
	}
	if record.Sources == nil {
		record.Sources = []string{}
	}
	if record.Links == nil {
		record.Links = []string{}
	}
	// --redo：删除被重置步骤之后的全部记录（产物不动），后续步骤须重新逐级起草与确认；写入前 checkOrder 已
	// 通过，记录与写作法步骤对位，截断到该步骤即可。
	if in.Redo && at+1 < len(steps.Content) {
		for _, item := range steps.Content[at+1:] {
			if workspace.HasAnchor(item) {
				return nil, nil, []workspace.Problem{errorAt(CodeAnchoredValue, rel, "steps",
					"要删除的步骤记录中有 YAML 锚点、别名或合并键，删除会改变其他取值；请先去掉锚点、别名与合并键")}
			}
		}
		steps.Content = slices.Clone(steps.Content[:at+1])
	}
	// 该步骤的记录：存在时只改涉及的字段并保留其他字段与注释（structured-edits），不存在时追加在末尾。
	if idx := workspace.IndexByID(steps, record.ID); idx >= 0 {
		if !plain(steps.Content[idx]) || workspace.HasMerge(steps.Content[idx]) {
			return nil, nil, []workspace.Problem{errorAt(CodeAnchoredValue, rel, fmt.Sprintf("steps[%d]", idx),
				"该步骤记录经由 YAML 锚点、别名或合并键得到，wa-cli 无法只修改它；请先去掉锚点、别名与合并键")}
		}
		if p := editStep(steps.Content[idx], rel, record); p != nil {
			return nil, nil, []workspace.Problem{*p}
		}
	} else {
		workspace.BlockSequence(doc.Content[0], "steps")
		steps.Content = append(steps.Content, stepNode(record))
	}
	saved, data, own, warns := recheck(root, rel, doc, w, m, at, r)
	if len(own) > 0 {
		return nil, nil, own
	}
	if !in.Redo && reflect.DeepEqual(before, schema.Decode(doc)) {
		return normalize(saved), nil, warns
	}
	created, ps := ensure()
	if len(ps) > 0 {
		return nil, nil, ps
	}
	if err := workspace.WriteFile(root, rel, data); err != nil {
		return nil, nil, []workspace.Problem{errorAt(workspace.CodeWriteFailed, rel, "", "写入规划失败："+err.Error())}
	}
	return normalize(saved), append(created, rel), warns
}

// stepFieldOrder 是步骤记录中字段的固定顺序。
var stepFieldOrder = []string{"id", "title", "text", "sources", "links", "confirmed_by_author", "confirmation", "confirmed_at"}

// editStep 在已有的步骤记录节点上按固定字段顺序只改涉及的字段，保留其他字段与注释（structured-edits）：
// 空数组删除对应键，未确认的记录删除确认字段。途经锚点、别名或合并键时返回问题。
func editStep(item *yaml.Node, rel string, s Step) *workspace.Problem {
	type kv struct {
		key string
		n   *yaml.Node // nil 表示删除该字段
	}
	kvs := []kv{{"id", str(s.ID)}, {"title", str(s.Title)}, {"text", str(s.Text)}}
	if len(s.Sources) > 0 {
		kvs = append(kvs, kv{"sources", seqNode(s.Sources)})
	} else {
		kvs = append(kvs, kv{"sources", nil})
	}
	if len(s.Links) > 0 {
		kvs = append(kvs, kv{"links", seqNode(s.Links)})
	} else {
		kvs = append(kvs, kv{"links", nil})
	}
	if s.ConfirmedByAuthor {
		kvs = append(kvs, kv{"confirmed_by_author", &yaml.Node{Kind: yaml.ScalarNode, Tag: "!!bool", Value: "true"}})
		if s.Confirmation != nil {
			kvs = append(kvs, kv{"confirmation", str(*s.Confirmation)})
		}
		if s.ConfirmedAt != nil {
			kvs = append(kvs, kv{"confirmed_at", str(*s.ConfirmedAt)})
		}
	} else {
		kvs = append(kvs,
			kv{"confirmed_by_author", &yaml.Node{Kind: yaml.ScalarNode, Tag: "!!bool", Value: "false"}},
			kv{"confirmation", nil},
			kv{"confirmed_at", nil})
	}
	for _, pair := range kvs {
		if f, ok := workspace.SetKey(item, stepFieldOrder, pair.key, pair.n); !ok {
			return &workspace.Problem{Code: CodeAnchoredValue, Severity: workspace.SeverityError, Path: rel, Field: f,
				Message: "该步骤记录的字段经由 YAML 锚点、别名或合并键得到，wa-cli 无法只修改它；请先去掉锚点、别名与合并键"}
		}
	}
	return nil
}

// stepNode 按固定字段顺序构造一个步骤记录节点；未确认的记录不含确认字段。
func stepNode(s Step) *yaml.Node {
	content := []*yaml.Node{str("id"), str(s.ID), str("title"), str(s.Title), str("text"), str(s.Text)}
	if len(s.Sources) > 0 {
		content = append(content, str("sources"), seqNode(s.Sources))
	}
	if len(s.Links) > 0 {
		content = append(content, str("links"), seqNode(s.Links))
	}
	content = append(content, str("confirmed_by_author"))
	if s.ConfirmedByAuthor {
		content = append(content, &yaml.Node{Kind: yaml.ScalarNode, Tag: "!!bool", Value: "true"})
		if s.Confirmation != nil {
			content = append(content, str("confirmation"), str(*s.Confirmation))
		}
		if s.ConfirmedAt != nil {
			content = append(content, str("confirmed_at"), str(*s.ConfirmedAt))
		}
	} else {
		content = append(content, &yaml.Node{Kind: yaml.ScalarNode, Tag: "!!bool", Value: "false"})
	}
	return &yaml.Node{Kind: yaml.MappingNode, Tag: "!!map", Content: content}
}

// Confirm 记录当前步骤的作者确认（确认记录（Confirmation Record））：只确认第一个未确认的步骤，要求它已有草稿；
// 声明了规划产物（Planning Product）的步骤必须已有至少一个链接且全部可解析。返回复查后的规划、变更的路径与问题。
func Confirm(root string, w *work.Work, unitDir string, m *catalog.Method, step, confirmation string, ensure func() ([]string, []workspace.Problem)) (*File, []string, []workspace.Problem) {
	if ps := checkText("confirmation", confirmation); len(ps) > 0 {
		return nil, nil, ps
	}
	at := methodIndex(m, step)
	if at < 0 {
		return nil, nil, []workspace.Problem{errorAt(CodeUnknownStep, "", "step",
			"步骤 " + step + " 不在写作法 " + m.ID + " 的规划步骤中")}
	}
	if strings.TrimSpace(confirmation) == "" {
		return nil, nil, []workspace.Problem{{Code: CodeIncompleteConfirmation, Severity: workspace.SeverityError, Field: "confirmation",
			Message: "作者确认必须给出 --confirmed-by-author 与非空白的 --confirmation（作者原话摘要）"}}
	}
	rel := Path(unitDir)
	doc, f, ps := load(root, rel)
	if len(ps) > 0 {
		return nil, nil, ps
	}
	if f == nil {
		return nil, nil, []workspace.Problem{errorAt(CodeNotDrafted, "", "",
			"还没有规划（Plan）；当前步骤 " + m.Steps[at].ID + " 须先用 wa-cli plan step 起草")}
	}
	if f.Method != m.ID {
		return nil, nil, []workspace.Problem{errorAt(CodeMethodChanged, rel, "method",
			fmt.Sprintf("规划记录的写作法是 %s，与当前选用的 %s 不一致；请先统一写作法再继续", f.Method, m.ID))}
	}
	if own, _ := split(checkOrder(rel, f, m)); len(own) > 0 {
		return nil, nil, own
	}
	if cur := CurrentStep(f, m); at != cur {
		return nil, nil, []workspace.Problem{orderProblem(m, cur)}
	}
	if at >= len(f.Steps) {
		return nil, nil, []workspace.Problem{errorAt(CodeNotDrafted, "", "",
			"步骤 " + step + " 还没有草稿；先用 wa-cli plan step 起草再确认")}
	}
	record := f.Steps[at]
	r := newResolver(root, w)
	if product := m.Steps[at].Product; product != "" {
		if len(record.Links) == 0 {
			return nil, nil, []workspace.Problem{errorAt(CodeMissingLinks, rel, "links",
				"步骤 " + step + " 声明了规划产物（Planning Product）" + product + "，确认时必须至少有一个 --link 关联的产物：" + productHint(product))}
		}
		for i, link := range record.Links {
			if !r.check(product, link) {
				return nil, nil, []workspace.Problem{errorAt(CodeUnknownLink, rel, fmt.Sprintf("links[%d]", i), unknownLink(product, link))}
			}
		}
	}
	confirmedAt := time.Now().UTC().Format(TimeFormat)
	record.ConfirmedByAuthor = true
	record.Confirmation = &confirmation
	record.ConfirmedAt = &confirmedAt
	steps := stepsNode(doc)
	if steps == nil {
		return nil, nil, []workspace.Problem{errorAt(CodeAnchoredValue, rel, "steps",
			"steps 经由 YAML 锚点、别名或合并键得到，wa-cli 无法只修改一项；请先去掉锚点、别名与合并键")}
	}
	idx := workspace.IndexByID(steps, record.ID)
	if idx < 0 {
		return nil, nil, []workspace.Problem{errorAt(CodeNotDrafted, "", "",
			"步骤 " + step + " 还没有草稿；先用 wa-cli plan step 起草再确认")}
	}
	if !plain(steps.Content[idx]) || workspace.HasMerge(steps.Content[idx]) {
		return nil, nil, []workspace.Problem{errorAt(CodeAnchoredValue, rel, fmt.Sprintf("steps[%d]", idx),
			"该步骤记录经由 YAML 锚点、别名或合并键得到，wa-cli 无法只修改它；请先去掉锚点、别名与合并键")}
	}
	if p := editStep(steps.Content[idx], rel, record); p != nil {
		return nil, nil, []workspace.Problem{*p}
	}
	saved, data, own, warns := recheck(root, rel, doc, w, m, at, r)
	if len(own) > 0 {
		return nil, nil, own
	}
	created, ps := ensure()
	if len(ps) > 0 {
		return nil, nil, ps
	}
	if err := workspace.WriteFile(root, rel, data); err != nil {
		return nil, nil, []workspace.Problem{errorAt(workspace.CodeWriteFailed, rel, "", "写入规划失败："+err.Error())}
	}
	return normalize(saved), append(created, rel), warns
}

// normalize 把步骤的数组字段补成空数组（输出信封中不得为 null）。
func normalize(f *File) *File {
	for i := range f.Steps {
		if f.Steps[i].Sources == nil {
			f.Steps[i].Sources = []string{}
		}
		if f.Steps[i].Links == nil {
			f.Steps[i].Links = []string{}
		}
	}
	return f
}

// Load 读出规划供查看：文件不存在时返回 nil；Schema 问题照常返回（顺序与引用问题由调用方按需检查）。
func Load(root, unitDir string) (*File, []workspace.Problem) {
	_, f, ps := load(root, Path(unitDir))
	if f == nil {
		return nil, ps
	}
	return normalize(f), ps
}

// Check 检查单元的规划（plan show 与命令的只读检查）：把问题分为自身问题 own（顺序不变式违反）与悬空引用 refs
//（sources、links）；plan.yaml 不存在或未通过 Schema 校验时都为空（其问题由 Load 返回）。
func Check(root, unitDir string, w *work.Work, m *catalog.Method) (own, refs []workspace.Problem) {
	rel := Path(unitDir)
	_, f, ps := load(root, rel)
	if len(ps) > 0 || f == nil {
		return nil, nil
	}
	return split(slices.Concat(checkOrder(rel, f, m), checkRefsUsing(root, rel, f, w, m, newResolver(root, w))))
}

// CheckUnitArtifacts 校验每个单元目录中存在的 plan.yaml（整体校验）：Schema、顺序不变式、素材登记与产物链接。
// methods 为工作区可用的写作法，defaultOf 返回单元适用的文体默认写作法（只有根单元适用，method-selection）。
// 单元选用的写作法不是规划类而存在 plan.yaml 时报告 plan.not-planning。返回已校验的路径与全部问题。
func CheckUnitArtifacts(root string, works []*work.Work, methods *catalog.MethodSet, defaultOf func(w *work.Work, u *work.Unit) string) (checked []string, problems []workspace.Problem) {
	for _, w := range works {
		r := newResolver(root, w) // 每个作品构造一次（load-once-per-command）
		for _, u := range w.Units() {
			dir := w.UnitDir(u)
			rel := Path(dir)
			doc, f, ps := load(root, rel)
			if doc == nil {
				if len(ps) > 0 {
					checked = append(checked, rel)
					problems = append(problems, ps...)
				}
				continue
			}
			checked = append(checked, rel)
			problems = append(problems, ps...)
			m, _ := methods.Pick(brief.MethodOf(root, dir), defaultOf(w, u))
			if m == nil || m.Kind != catalog.MethodPlanning {
				problems = append(problems, errorAt(CodeNotPlanning, rel, "",
					"单元 "+u.Ref+" 选用的写作法不是规划类，不能有规划（Plan）plan.yaml"))
				continue
			}
			problems = append(problems, checkPlan(root, rel, f, w, m, r)...)
		}
	}
	return checked, problems
}

// checkPlan 汇总一份已通过 Schema 校验的规划的领域问题：写作法一致、顺序不变式、确认记录完整与引用。
func checkPlan(root, rel string, f *File, w *work.Work, m *catalog.Method, r *resolver) []workspace.Problem {
	if f == nil {
		return nil
	}
	var ps []workspace.Problem
	if f.Method != m.ID {
		ps = append(ps, errorAt(CodeMethodChanged, rel, "method",
			fmt.Sprintf("规划记录的写作法是 %s，与当前选用的 %s 不一致", f.Method, m.ID)))
	}
	ps = append(ps, checkOrder(rel, f, m)...)
	ps = append(ps, checkConfirmations(rel, f)...)
	return append(ps, checkRefsUsing(root, rel, f, w, m, r)...)
}

// checkConfirmations 检查确认记录（Confirmation Record）完整：confirmed_by_author 为 true 的步骤缺非空
// confirmation 或 confirmed_at 时报告 plan.incomplete-confirmation（field 为该步骤）。Schema 只约束取值形态，
// 缺省与空值由这里报告。
func checkConfirmations(rel string, f *File) (problems []workspace.Problem) {
	for i, step := range f.Steps {
		if !step.ConfirmedByAuthor {
			continue
		}
		if step.Confirmation == nil || strings.TrimSpace(*step.Confirmation) == "" || step.ConfirmedAt == nil {
			missing := "confirmation 与 confirmed_at"
			if step.ConfirmedAt != nil {
				missing = "confirmation"
			} else if step.Confirmation != nil && strings.TrimSpace(*step.Confirmation) != "" {
				missing = "confirmed_at"
			}
			problems = append(problems, errorAt(CodeIncompleteConfirmation, rel, fmt.Sprintf("steps[%d]", i),
				"已确认的步骤 "+step.ID+" 缺非空的确认记录（"+missing+"）；请补齐作者确认的原话摘要与确认时间"))
		}
	}
	return problems
}

// CheckArtifact 对 rel（单元目录下的 plan.yaml）做 Schema 与领域检查，供 wa-cli validate <path>。
// defaultMethod 为该单元适用的文体默认写作法（只有根单元适用，由命令层经 unit-aware 的入口给出，
// 见 cli-contract/unit-aware-defaults）。
func CheckArtifact(root, rel, defaultMethod string) []workspace.Problem {
	unitDir := strings.TrimSuffix(rel, "/"+FileName)
	w, u, ps := work.Resolve(root, work.RefOf(unitDir))
	if w == nil || u == nil {
		return ps
	}
	doc, f, ps := load(root, rel)
	if doc == nil || len(ps) > 0 {
		return ps
	}
	m, _ := catalog.LoadMethods(root).Pick(brief.MethodOf(root, unitDir), defaultMethod)
	if m == nil || m.Kind != catalog.MethodPlanning {
		return []workspace.Problem{errorAt(CodeNotPlanning, rel, "",
			"单元 "+u.Ref+" 选用的写作法不是规划类，不能有规划（Plan）plan.yaml")}
	}
	return checkPlan(root, rel, f, w, m, newResolver(root, w))
}
