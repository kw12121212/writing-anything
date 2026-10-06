// Package work 管理工作区中的作品（Work）。作品本身是单元树（Unit Tree）的根单元（Root Unit），
// 每个单元按作品文体（Genre）声明的阶段（Stage）序列独立推进。本包加载与校验
// .writing-anything/works/<work>/work.yaml、解析单元引用（Unit Reference）、计算阻塞项（Blocker）、
// 推进阶段与追加子单元，只返回结构化的问题，由命令层转换为诊断（Diagnostic）。
package work

import (
	"errors"
	"fmt"
	"io/fs"
	"os"
	"path/filepath"
	"regexp"
	"slices"
	"strings"

	"github.com/kw12121212/writing-anything/internal/brief"
	"github.com/kw12121212/writing-anything/internal/catalog"
	"github.com/kw12121212/writing-anything/internal/decision"
	"github.com/kw12121212/writing-anything/internal/marketing"
	"github.com/kw12121212/writing-anything/internal/outline"
	"github.com/kw12121212/writing-anything/internal/review"
	"github.com/kw12121212/writing-anything/internal/schema"
	"github.com/kw12121212/writing-anything/internal/workspace"
	"go.yaml.in/yaml/v3"
)

// 作品、单元与生命周期（Lifecycle）的错误码（Error Code）。已发布的错误码不得改名或复用。
const (
	CodeExists              = "work.exists"
	CodeNotFound            = "work.not-found"
	CodeIDMismatch          = "work.id-mismatch"
	CodeUnknownGenre        = "work.unknown-genre"
	CodeRootTypeMismatch    = "work.root-type-mismatch"
	CodeUnknownUnitType     = "work.unknown-unit-type"
	CodeChildTypeNotAllowed = "work.child-type-not-allowed"
	CodeUnknownStage        = "work.unknown-stage"
	CodeAnchoredValue       = "work.anchored-value"
	CodeUnitNotFound        = "unit.not-found"
	CodeNotSibling          = "unit.not-sibling"
	CodeMoveRoot            = "unit.move-root"
	CodeMoveUnderSelf       = "unit.move-under-self"
	CodeMissingArtifact     = "lifecycle.missing-artifact"
	CodeAlreadyDone         = "lifecycle.already-done"
	CodeDerivedStage        = "lifecycle.derived-stage"
)

// FileName 是作品目录中作品产物的文件名。
const FileName = "work.yaml"

// codeInvalidValue 是参数取值不合法的错误码（Error Code）；与 internal/cli 的同名约定一致。
const codeInvalidValue = "input.invalid-value"

// idPattern 与 common.schema.json 中的 id 一致；单元引用中的 id 在拼接路径前按它检查。
var idPattern = regexp.MustCompile(`^[a-z][a-z0-9]*(-[a-z0-9]+)*$`)

// Dir 返回作品 id 的作品目录，相对项目目录。
func Dir(id string) string { return workspace.WorksPath + "/" + id }

// Path 返回作品 id 的作品产物路径，相对项目目录。
func Path(id string) string { return Dir(id) + "/" + FileName }

// Unit 是单元树中的一个单元。
type Unit struct {
	Ref    string // 单元引用：根单元为 <work>，其他单元为 <work>/<unit-id>
	ID     string
	Type   string
	Stage  string
	Parent *Unit // 根单元为 nil
	Units  []*Unit
	field  string // 单元在 work.yaml 中的字段路径，根单元为空
	index  []int  // 从根单元到本单元经过的 units 下标
}

// Work 是一个已通过校验的作品。
type Work struct {
	ID        string
	Path      string // work.yaml 相对项目目录的路径
	Genre     *catalog.Genre
	Root      *Unit
	doc       *yaml.Node
	decisions *loadedQueue         // 决策队列，首次计算阻塞项时加载
	gateLevel string               // 工作区配置的关卡强度（Gate Level），首次需要时读取
	methods   *catalog.MethodSet   // 写作法目录，首次计算大纲阻塞项时加载
	causality *[]workspace.Problem // 场景因果链（Causality Chain）的全部问题，首次需要时计算（load-once-per-command）
}

// loadedQueue 是一次加载的决策队列。
type loadedQueue struct {
	ds      []decision.Decision
	invalid bool
}

// 文件格式：字段名与 YAML 一致。
type (
	unitFile struct {
		ID    string     `json:"id"`
		Type  string     `json:"type"`
		Stage string     `json:"stage"`
		Units []unitFile `json:"units"`
	}
	workFile struct {
		unitFile
		Genre string `json:"genre"`
	}
)

// genreLookup 按 id 取文体，签名与 catalog.LoadGenre 一致；测试用它传入测试文体。
type genreLookup func(id string) (*catalog.Genre, []workspace.Problem, bool)

func errorAt(code, path, field, msg string) workspace.Problem {
	return workspace.Problem{Code: code, Severity: workspace.SeverityError, Path: path, Field: field, Message: msg}
}

// Load 读取并校验 root 下的作品 id。
func Load(root, id string) (*Work, []workspace.Problem) {
	return load(root, id, catalog.LoadGenre)
}

func load(root, id string, genres genreLookup) (*Work, []workspace.Problem) {
	rel := Path(id)
	doc, ps := workspace.Read(root, rel)
	if doc == nil {
		return nil, ps
	}
	return parse(doc, rel, id, genres)
}

// parse 按作品的 Schema 与文体校验已解析的文档 doc 并构建单元树；dirName 是作品目录名。
func parse(doc *yaml.Node, rel, dirName string, genres genreLookup) (*Work, []workspace.Problem) {
	if ps := schema.Named("work").ValidateNode(doc); len(ps) > 0 {
		return nil, workspace.FromSchema(rel, ps)
	}
	var f workFile
	catalog.Decode(doc, &f)
	w := &Work{ID: f.ID, Path: rel, doc: doc}
	w.Root = w.build(f.unitFile, nil, "", nil)
	var problems []workspace.Problem
	if f.ID != dirName {
		problems = append(problems, errorAt(CodeIDMismatch, rel, "id", fmt.Sprintf("作品 id %s 与作品目录名 %s 不一致", f.ID, dirName)))
	}
	g, ps, ok := genres(f.Genre)
	switch {
	case !ok:
		return nil, append(problems, errorAt(CodeUnknownGenre, rel, "genre", UnknownGenreMessage(f.Genre)))
	case g == nil || len(ps) > 0:
		return nil, append(problems, ps...)
	}
	w.Genre = g
	if problems = append(problems, w.check()...); len(problems) > 0 {
		return nil, problems
	}
	return w, nil
}

// UnknownGenreMessage 说明文体 id 不存在，并列出可用的文体。
func UnknownGenreMessage(id string) string {
	return fmt.Sprintf("文体（Genre）%s 不存在；可用的文体为 %s", id, strings.Join(catalog.BuiltinGenres(), "、"))
}

func (w *Work) build(f unitFile, parent *Unit, field string, index []int) *Unit {
	u := &Unit{Ref: w.ID, ID: f.ID, Type: f.Type, Stage: f.Stage, Parent: parent, field: field, index: index}
	if parent != nil {
		u.Ref = w.ID + "/" + f.ID
	}
	for i, c := range f.Units {
		sub := fmt.Sprintf("units[%d]", i)
		if field != "" {
			sub = field + "." + sub
		}
		u.Units = append(u.Units, w.build(c, u, sub, append(slices.Clip(index), i)))
	}
	return u
}

// fieldOf 返回单元 u 的字段 key 在 work.yaml 中的点分路径。
func (u *Unit) fieldOf(key string) string {
	if u.field == "" {
		return key
	}
	return u.field + "." + key
}

// Units 按单元树先序返回全部单元，根单元在最前。
func (w *Work) Units() []*Unit {
	var out []*Unit
	var walk func(u *Unit)
	walk = func(u *Unit) {
		out = append(out, u)
		for _, c := range u.Units {
			walk(c)
		}
	}
	walk(w.Root)
	return out
}

// check 按文体检查单元类型、父子关系、阶段与 id 唯一性。
func (w *Work) check() []workspace.Problem {
	g := w.Genre
	types := map[string]catalog.UnitType{}
	var typeIDs []string
	for _, t := range g.UnitTypes {
		types[t.ID] = t
		typeIDs = append(typeIDs, t.ID)
	}
	var stageIDs []string
	for _, s := range g.Stages {
		stageIDs = append(stageIDs, s.ID)
	}
	var out []workspace.Problem
	add := func(code, field, msg string) { out = append(out, errorAt(code, w.Path, field, msg)) }
	seen := map[string]bool{w.ID: true}
	for _, u := range w.Units() {
		if u.Parent != nil {
			switch {
			case u.ID == w.ID:
				add(catalog.CodeDuplicateID, u.fieldOf("id"), "单元 id "+u.ID+" 与作品 id 相同")
			case seen[u.ID]:
				add(catalog.CodeDuplicateID, u.fieldOf("id"), "单元 id "+u.ID+" 在作品中重复")
			}
			seen[u.ID] = true
		}
		_, declared := types[u.Type]
		switch {
		case !declared:
			add(CodeUnknownUnitType, u.fieldOf("type"), fmt.Sprintf("单元类型（Unit Type）%s 未在文体 %s 中声明；可用的单元类型为 %s", u.Type, g.ID, strings.Join(typeIDs, "、")))
		case u.Parent == nil && u.Type != typeIDs[0]:
			add(CodeRootTypeMismatch, u.fieldOf("type"), fmt.Sprintf("根单元（Root Unit）的类型必须是文体 %s 的第一个单元类型 %s，不能是 %s", g.ID, typeIDs[0], u.Type))
		case u.Parent != nil:
			if pt, ok := types[u.Parent.Type]; ok && !slices.Contains(pt.Children, u.Type) {
				add(CodeChildTypeNotAllowed, u.fieldOf("type"), fmt.Sprintf("单元类型 %s 不能作为 %s 的子单元；%s", u.Type, pt.ID, allowedChildren(pt)))
			}
		}
		if u.Stage != catalog.StageDone && !slices.Contains(stageIDs, u.Stage) {
			add(CodeUnknownStage, u.fieldOf("stage"), fmt.Sprintf("阶段（Stage）%s 未在文体 %s 中声明；可用的阶段为 %s 与终点 %s", u.Stage, g.ID, strings.Join(stageIDs, "、"), catalog.StageDone))
		}
	}
	return out
}

func allowedChildren(t catalog.UnitType) string {
	if len(t.Children) == 0 {
		return "单元类型 " + t.ID + " 不能包含子单元"
	}
	return "允许的子单元类型为 " + strings.Join(t.Children, "、")
}

// Resolve 按单元引用 ref（<work> 或 <work>/<unit-id>）加载作品并找到单元。
func Resolve(root, ref string) (*Work, *Unit, []workspace.Problem) {
	return resolve(root, ref, catalog.LoadGenre)
}

func resolve(root, ref string, genres genreLookup) (*Work, *Unit, []workspace.Problem) {
	workID, unitID, child := strings.Cut(ref, "/")
	if !idPattern.MatchString(workID) || !isDir(root, Dir(workID)) {
		path := ""
		if idPattern.MatchString(workID) {
			path = Dir(workID)
		}
		return nil, nil, []workspace.Problem{errorAt(CodeNotFound, path, "", "没有作品（Work）"+workID)}
	}
	w, ps := load(root, workID, genres)
	if w == nil {
		return nil, nil, ps
	}
	if !child {
		return w, w.Root, nil
	}
	if u := w.find(unitID); u != nil && u.Parent != nil {
		return w, u, nil
	}
	return nil, nil, []workspace.Problem{errorAt(CodeUnitNotFound, w.Path, "", fmt.Sprintf("作品 %s 中没有单元（Unit）%s", w.ID, unitID))}
}

func (w *Work) find(id string) *Unit {
	for _, u := range w.Units() {
		if u.ID == id {
			return u
		}
	}
	return nil
}

func abs(root, rel string) string { return filepath.Join(root, filepath.FromSlash(rel)) }

// isDir 报告 rel 是否为真实目录；符号链接不算，避免跟随它读写项目目录之外的文件。
func isDir(root, rel string) bool {
	info, err := os.Lstat(abs(root, rel))
	return err == nil && info.IsDir()
}

func isFile(root, rel string) bool {
	info, err := os.Stat(abs(root, rel))
	return err == nil && !info.IsDir()
}

// UnitDir 返回单元 u 的单元目录，相对项目目录：根单元为作品目录，其他单元为作品目录下的 units/<unit-id>。
func (w *Work) UnitDir(u *Unit) string {
	if u.Parent == nil {
		return Dir(w.ID)
	}
	return Dir(w.ID) + "/units/" + u.ID
}

// RefOf 返回单元目录 dir（UnitDir 的结果）对应的单元引用。
func RefOf(dir string) string { return workspace.UnitRef(dir) }

// Blocker 是阻止单元推进的一个阻塞项（Blocker）。
type Blocker struct {
	Code     string
	Artifact string // 缺失的必需产物 id；决策阻塞项为 decisions
	Path     string // 缺失产物所在的单元目录或决策队列，相对项目目录
	Decision string // 决策阻塞项的决策 id；其他阻塞项为空
	Message  string
}

// Blockers 返回单元 u 在当前阶段的全部阻塞项；advance、next 与 status 都只通过它得到阻塞项。
// 阻塞来源依次为：缺失的必需产物（Required Artifact），即单元目录下既没有 <a>.yaml 也没有 <a>.md；当前阶段要求 brief
// 时的写作简报问题（brief-gate）；当前阶段要求 outline 时的覆盖缺口（outline-gate）；当前阶段要求 marketing 时仍缺的营销要素（marketing-gate）；当前阶段要求 acceptance 时失效的验收记录；决策队列无法加载时，偏安全地给出一条 decision.invalid-queue；关联该单元的未决定阻塞决策。
func (w *Work) Blockers(root string, u *Unit) []Blocker {
	s := w.stage(u.Stage)
	if s == nil {
		return nil
	}
	dir := w.UnitDir(u)
	var out []Blocker
	for _, a := range s.RequiredArtifacts {
		if isFile(root, dir+"/"+a+".yaml") || isFile(root, dir+"/"+a+".md") {
			continue
		}
		out = append(out, Blocker{Code: CodeMissingArtifact, Artifact: a, Path: dir,
			Message: fmt.Sprintf("阶段 %s 的必需产物（Required Artifact）%s 缺失：%s/ 下没有 %s.yaml 或 %s.md", u.Stage, a, dir, a, a)})
	}
	all, invalid := w.queue(root)
	q := brief.Queue{Decisions: all, Invalid: invalid}
	if slices.Contains(s.RequiredArtifacts, "brief") {
		out = append(out, w.briefBlockers(root, u, q)...)
	}
	if slices.Contains(s.RequiredArtifacts, "outline") {
		out = append(out, w.outlineBlockers(root, u, q)...)
	}
	if slices.Contains(s.RequiredArtifacts, "marketing") {
		out = append(out, w.marketingBlockers(root, u, marketing.Queue{Decisions: all, Invalid: invalid})...)
	}
	if slices.Contains(s.RequiredArtifacts, "acceptance") {
		// 验收后正文又被修改，或验收记录不通过校验：原来的验收不再有效。
		for _, b := range review.AcceptanceBlockers(root, dir, w.level(root)) {
			out = append(out, Blocker{Code: b.Code, Artifact: "acceptance", Path: b.Path, Message: b.Message})
		}
	}
	ds := decision.BlockingIn(all, u.Ref)
	if invalid {
		out = append(out, Blocker{Code: decision.CodeInvalidQueue, Artifact: "decisions", Path: decision.Path,
			Message: "决策队列（Decision Queue）无法加载，无法确认是否存在阻塞决策；请先修复 " + decision.Path})
	}
	for _, d := range ds {
		out = append(out, Blocker{Code: decision.CodeBlocking, Artifact: "decisions", Path: decision.Path, Decision: d.ID,
			Message: fmt.Sprintf("阻塞决策（Blocking）%s 尚未决定：%s", d.ID, d.Question)})
	}
	return out
}

// briefBlockers 返回单元 u 的写作简报（Brief）阻塞项：brief.yaml 不通过校验时一条 brief.invalid，否则每个
// 仍需向作者提问的问题一条；单元目录下没有 brief.yaml 与 brief.md 时没有（由缺失产物报告）。
func (w *Work) briefBlockers(root string, u *Unit, q brief.Queue) []Blocker {
	dir := w.UnitDir(u)
	g := brief.Check(root, dir, isFile(root, brief.Path(dir)), isFile(root, dir+"/"+brief.MDName), w.briefRequired(u), q)
	if g == nil {
		return nil
	}
	if len(g.Invalid) > 0 {
		return []Blocker{{Code: brief.CodeInvalid, Artifact: "brief", Path: g.Path,
			Message: fmt.Sprintf("写作简报（Brief）%s 不通过校验（%s），无法确认必填项与核心观点的出处；请先修复", g.Path, g.Invalid[0].Message)}}
	}
	var out []Blocker
	for _, q := range g.Questions {
		out = append(out, Blocker{Code: q.Code, Artifact: "brief", Path: g.Path, Message: q.Message})
	}
	return out
}

// outlineBlockers 返回单元 u 的大纲（Outline）阻塞项：大纲或写作简报不通过校验时一条 outline.invalid，否则每个覆盖缺口
// （Coverage Gap）一条；单元目录下没有 outline.yaml 与 outline.md 时没有（由缺失产物报告）。报告在该单元大纲上的
// 因果链（Causality Chain）问题同样计入 outline.invalid（scene-causality）。
func (w *Work) outlineBlockers(root string, u *Unit, q brief.Queue) []Blocker {
	dir := w.UnitDir(u)
	g := outline.Check(root, dir, isFile(root, outline.Path(dir)), isFile(root, dir+"/"+outline.MDName), q, w.picker(root, u))
	if g == nil {
		return nil
	}
	if isFile(root, outline.Path(dir)) {
		if ps := w.outlineCausality(root, dir); len(ps) > 0 {
			if len(g.Invalid) == 0 {
				g = &outline.Gate{Path: outline.Path(dir), Invalid: ps}
			} else {
				g.Invalid = append(g.Invalid, ps...)
			}
		}
	}
	if len(g.Invalid) > 0 {
		// path 为不通过校验的产物（outline.yaml 或 brief.yaml），复查命令才能重新检出问题。
		return []Blocker{{Code: outline.CodeInvalid, Artifact: "outline", Path: g.Invalid[0].Path,
			Message: fmt.Sprintf("%s 不通过校验（%s），无法检查大纲（Outline）的覆盖；请先修复", g.Invalid[0].Path, g.Invalid[0].Message)}}
	}
	var out []Blocker
	for _, gap := range g.Gaps {
		out = append(out, Blocker{Code: gap.Code, Artifact: "outline", Path: g.Path, Message: gap.Message})
	}
	return out
}

// marketingBlockers 返回单元 u 的营销要素（Marketing Elements）阻塞项：marketing.yaml 不通过校验时一条 marketing.invalid，否则
// 每个仍缺的要素一条；单元目录下没有 marketing.yaml 与 marketing.md 时没有（由缺失产物报告）。标题决策尚未决定时由决策阻塞项报告。
func (w *Work) marketingBlockers(root string, u *Unit, q marketing.Queue) []Blocker {
	dir := w.UnitDir(u)
	g := marketing.Check(root, dir, isFile(root, marketing.Path(dir)), isFile(root, dir+"/"+marketing.MDName), q)
	if g == nil {
		return nil
	}
	if len(g.Invalid) > 0 {
		return []Blocker{{Code: marketing.CodeInvalid, Artifact: "marketing", Path: g.Path,
			Message: fmt.Sprintf("营销要素（Marketing Elements）%s 不通过校验（%s）；请先修复", g.Path, g.Invalid[0].Message)}}
	}
	var out []Blocker
	for _, q := range g.Questions {
		out = append(out, Blocker{Code: q.Code, Artifact: "marketing", Path: g.Path, Message: q.Message})
	}
	return out
}

// Scenes 返回作品全部场景单元（type 为 scene）的因果链场景列表，按单元树先序排列。
func (w *Work) Scenes() []outline.Scene {
	var out []outline.Scene
	for _, u := range w.Units() {
		if u.Type == outline.SceneType {
			out = append(out, outline.Scene{Ref: u.Ref, Dir: w.UnitDir(u)})
		}
	}
	return out
}

// outlineCausality 返回报告在单元目录 dir 的大纲上的因果链（Causality Chain）问题；全部场景的因果链每条命令只
// 计算一次，再按目录过滤（load-once-per-command）。
func (w *Work) outlineCausality(root, dir string) []workspace.Problem {
	if w.causality == nil {
		ps := outline.Causality(root, w.Scenes())
		w.causality = &ps
	}
	var out []workspace.Problem
	for _, p := range *w.causality {
		if p.Path == outline.Path(dir) {
			out = append(out, p)
		}
	}
	return out
}

// briefRequired 返回单元 u 的类型声明的简报必填项（Required Brief Field）。
func (w *Work) briefRequired(u *Unit) []string {
	for _, t := range w.Genre.UnitTypes {
		if t.ID == u.Type {
			return t.BriefRequired
		}
	}
	return nil
}

// queue 返回决策队列与它是否无法加载；同一个作品只加载一次，逐单元计算阻塞项时不重复读取与校验。
// level 返回工作区配置的关卡强度；每个作品只读取一次配置，读取失败时按默认的 standard 处理。
func (w *Work) level(root string) string {
	if w.gateLevel == "" {
		w.gateLevel = workspace.DefaultGateLevel
		if cfg, _ := workspace.Load(root); cfg != nil && cfg.GateLevel != "" {
			w.gateLevel = cfg.GateLevel
		}
	}
	return w.gateLevel
}

// picker 返回为单元 u 的大纲关卡选出写作法的 Picker；每个作品只加载一次写作法目录。
// 只有作品的根单元用文体的 default_method，子单元不继承（method-selection）。
func (w *Work) picker(root string, u *Unit) outline.Picker {
	if w.methods == nil {
		w.methods = catalog.LoadMethods(root)
	}
	def := ""
	if u.Parent == nil {
		def = w.Genre.DefaultMethod
	}
	return outline.Picker{Methods: w.methods, Default: def}
}

func (w *Work) queue(root string) ([]decision.Decision, bool) {
	if w.decisions == nil {
		ds, _, ps := decision.Load(root)
		w.decisions = &loadedQueue{ds: ds, invalid: len(ps) > 0}
	}
	return w.decisions.ds, w.decisions.invalid
}

// stage 返回阶段 id 的声明；id 为 done 时返回 nil。
func (w *Work) stage(id string) *catalog.Stage {
	for i := range w.Genre.Stages {
		if w.Genre.Stages[i].ID == id {
			return &w.Genre.Stages[i]
		}
	}
	return nil
}

// NextStage 返回单元 u 推进后的阶段：下一阶段，当前为最后一个阶段时为 done。
func (w *Work) NextStage(u *Unit) string {
	stages := w.Genre.Stages
	for i, s := range stages {
		if s.ID == u.Stage && i+1 < len(stages) {
			return stages[i+1].ID
		}
	}
	return catalog.StageDone
}

// Aggregated 报告单元 u 是否为汇总单元（Aggregated Unit）：其类型声明 stage_from_children，
// 阶段不由推进得出，而由子单元汇总（Derived Stage）。文体没有声明该字段的类型各自独立推进。
func (w *Work) Aggregated(u *Unit) bool {
	for _, t := range w.Genre.UnitTypes {
		if t.ID == u.Type {
			return t.StageFromChildren
		}
	}
	return false
}

// EffectiveStage 返回单元 u 的生效阶段：汇总单元为推导阶段（Derived Stage），其他单元为存储阶段。
func (w *Work) EffectiveStage(u *Unit) string {
	if w.Aggregated(u) {
		return w.DerivedStage(u)
	}
	return u.Stage
}

// DerivedStage 返回汇总单元 u 的推导阶段：取其子单元树中所有非汇总子孙单元（走生命周期的单元）里
// 最落后者的阶段——按文体阶段序列排序最靠前的阶段；没有非汇总子孙单元时为文体的第一个阶段；
// 全部为 done 时为 done。
func (w *Work) DerivedStage(u *Unit) string {
	stage, found := w.weakest(u)
	if !found {
		return w.Genre.Stages[0].ID
	}
	return stage
}

// weakest 返回子单元树中最落后的非汇总子孙单元的阶段；没有非汇总子孙单元时 found 为 false。
// 汇总单元自身不参与（其阶段就是推导结果），但继续向其子孙递归；非汇总单元既参与也向其子孙递归。
func (w *Work) weakest(u *Unit) (stage string, found bool) {
	order := map[string]int{catalog.StageDone: len(w.Genre.Stages)}
	for i, s := range w.Genre.Stages {
		order[s.ID] = i
	}
	var walk func(u *Unit)
	walk = func(u *Unit) {
		for _, c := range u.Units {
			if !w.Aggregated(c) && (!found || order[c.Stage] < order[stage]) {
				stage, found = c.Stage, true
			}
			walk(c)
		}
	}
	walk(u)
	return stage, found
}

// Progress 返回单元 u 的子单元树中非汇总子孙单元的总数与其中阶段为 done 的数量。
func (w *Work) Progress(u *Unit) (done, total int) {
	for _, c := range w.Units() {
		if w.Aggregated(c) || !w.under(c, u) {
			continue
		}
		total++
		if c.Stage == catalog.StageDone {
			done++
		}
	}
	return done, total
}

// under 报告单元 c 是否在单元 u 的子单元树中（不含 u 自身）。
func (w *Work) under(c, u *Unit) bool {
	for p := c.Parent; p != nil; p = p.Parent {
		if p == u {
			return true
		}
	}
	return false
}

// AggregatedBlockers 返回汇总单元 u 的阻塞项：其未完成非汇总子孙单元的阻塞项按出现顺序去重后的并集
// （同一 code、artifact、path 与 decision 只保留一条）。status 与 next 对汇总单元用它代替单元自身的阻塞项。
func (w *Work) AggregatedBlockers(root string, u *Unit) []Blocker {
	var out []Blocker
	seen := map[string]bool{}
	for _, c := range w.Units() {
		if w.Aggregated(c) || !w.under(c, u) || c.Stage == catalog.StageDone {
			continue
		}
		for _, b := range w.Blockers(root, c) {
			key := fmt.Sprintf("%s\x00%s\x00%s\x00%s", b.Code, b.Artifact, b.Path, b.Decision)
			if !seen[key] {
				seen[key] = true
				out = append(out, b)
			}
		}
	}
	return out
}

// LoadAll 加载 root 下 works/ 中每个不以 . 开头的子目录的作品，按目录名排序。返回能加载的作品、
// 已校验的 work.yaml 路径与全部问题；works/ 不存在时什么也不返回，由工作区校验报告。
func LoadAll(root string) (works []*Work, checked []string, problems []workspace.Problem) {
	entries, err := os.ReadDir(abs(root, workspace.WorksPath))
	if err != nil {
		return nil, nil, nil
	}
	for _, e := range entries {
		if strings.HasPrefix(e.Name(), ".") || !isDir(root, Dir(e.Name())) {
			continue
		}
		w, ps := Load(root, e.Name())
		if len(ps) == 0 || ps[0].Code != schema.CodeNotFound {
			checked = append(checked, Path(e.Name()))
		}
		if w != nil {
			works = append(works, w)
		}
		problems = append(problems, ps...)
	}
	return works, checked, problems
}

// CheckArtifact 对已通过 Schema 校验的 work.yaml（rel）做与文体有关的检查。
func CheckArtifact(root, rel string) []workspace.Problem {
	_, ps := Load(root, strings.Split(rel, "/")[2])
	return ps
}

// Create 在 root 下按文体 genreID 创建作品 slug：根单元类型为文体的第一个单元类型，阶段为第一个阶段。
// 返回新建的作品与新建的路径；出错时不创建任何文件。
func Create(root, genreID, slug string) (*Work, []string, []workspace.Problem) {
	return create(root, genreID, slug, catalog.LoadGenre)
}

func create(root, genreID, slug string, genres genreLookup) (*Work, []string, []workspace.Problem) {
	g, ps, ok := genres(genreID)
	switch {
	case !ok:
		return nil, nil, []workspace.Problem{errorAt(CodeUnknownGenre, "", "", UnknownGenreMessage(genreID))}
	case g == nil || len(ps) > 0:
		return nil, nil, ps
	}
	m := &yaml.Node{Kind: yaml.MappingNode}
	for _, kv := range [][2]*yaml.Node{
		{str("schema_version"), {Kind: yaml.ScalarNode, Tag: "!!int", Value: "1"}},
		{str("id"), str(slug)},
		{str("genre"), str(g.ID)},
		{str("type"), str(g.UnitTypes[0].ID)},
		{str("stage"), str(g.Stages[0].ID)},
		{str("units"), {Kind: yaml.SequenceNode, Tag: "!!seq", Style: yaml.FlowStyle}},
	} {
		m.Content = append(m.Content, kv[0], kv[1])
	}
	doc := &yaml.Node{Kind: yaml.DocumentNode, HeadComment: "作品（Work）；请用 wa-cli new、wa-cli unit add 与 wa-cli advance 修改。", Content: []*yaml.Node{m}}
	data, w, ps := encodeChecked(doc, Path(slug), slug, fixed(g))
	if len(ps) > 0 {
		for i := range ps {
			// 作品尚未创建：不填路径，行号也没有意义。
			ps[i].Path, ps[i].Line = "", 0
		}
		return nil, nil, ps
	}
	dir := Dir(slug)
	if _, err := os.Lstat(abs(root, dir)); !errors.Is(err, fs.ErrNotExist) {
		return nil, nil, []workspace.Problem{errorAt(CodeExists, dir, "", "作品 "+slug+" 已存在")}
	}
	if err := os.Mkdir(abs(root, dir), 0o755); err != nil {
		return nil, nil, []workspace.Problem{errorAt(workspace.CodeWriteFailed, dir, "", "创建作品目录失败："+err.Error())}
	}
	if err := workspace.WriteFile(root, w.Path, data); err != nil {
		os.Remove(abs(root, dir))
		return nil, nil, []workspace.Problem{errorAt(workspace.CodeWriteFailed, w.Path, "", "写入作品失败："+err.Error())}
	}
	return w, []string{dir, w.Path}, nil
}

func str(v string) *yaml.Node { return &yaml.Node{Kind: yaml.ScalarNode, Tag: "!!str", Value: v} }

// fixed 返回总是给出文体 g 的查找函数：修改作品不会改变其文体，写前复查无需重新加载。
func fixed(g *catalog.Genre) genreLookup {
	return func(string) (*catalog.Genre, []workspace.Problem, bool) { return g, nil, true }
}

// encodeChecked 编码 doc，并把编码结果重新解析、按 Schema 与文体校验，保证写出的文件可用。
func encodeChecked(doc *yaml.Node, rel, dirName string, genres genreLookup) ([]byte, *Work, []workspace.Problem) {
	data, err := workspace.Encode(doc)
	if err != nil {
		return nil, nil, []workspace.Problem{errorAt(workspace.CodeWriteFailed, rel, "", "生成作品失败："+err.Error())}
	}
	written, p := schema.Parse(data)
	if p != nil {
		p.Line = 0
		return nil, nil, workspace.FromSchema(rel, []schema.Problem{*p})
	}
	w, ps := parse(written, rel, dirName, genres)
	for i := range ps {
		ps[i].Line = 0 // 未写出的文件，行号没有意义
	}
	return data, w, ps
}

// save 在写前复查修改后的文档，通过后原子写入；返回写入后的作品。
func (w *Work) save(root string) (*Work, []workspace.Problem) {
	data, saved, ps := encodeChecked(w.doc, w.Path, w.ID, fixed(w.Genre))
	if len(ps) > 0 {
		return nil, ps
	}
	if err := workspace.WriteFile(root, w.Path, data); err != nil {
		return nil, []workspace.Problem{errorAt(workspace.CodeWriteFailed, w.Path, "", "写入作品失败："+err.Error())}
	}
	return saved, nil
}

// Advance 把 ref 所指单元推进到下一阶段。返回推进后的作品、单元与推进前的阶段。
func Advance(root, ref string) (*Work, *Unit, string, []workspace.Problem) {
	return advance(root, ref, catalog.LoadGenre)
}

func advance(root, ref string, genres genreLookup) (*Work, *Unit, string, []workspace.Problem) {
	w, u, ps := resolve(root, ref, genres)
	if u == nil {
		return nil, nil, "", ps
	}
	if w.Aggregated(u) {
		return nil, nil, "", []workspace.Problem{errorAt(CodeDerivedStage, w.Path, u.fieldOf("stage"),
			fmt.Sprintf("单元 %s 是汇总单元（Aggregated Unit），阶段由子单元汇总得出，不能推进；当前推导阶段为 %s", u.Ref, w.DerivedStage(u)))}
	}
	if u.Stage == catalog.StageDone {
		return nil, nil, "", []workspace.Problem{errorAt(CodeAlreadyDone, w.Path, u.fieldOf("stage"), "单元 "+u.Ref+" 已走完全部阶段")}
	}
	for _, b := range w.Blockers(root, u) {
		ps = append(ps, errorAt(b.Code, b.Path, "", b.Message))
	}
	if len(ps) > 0 {
		return nil, nil, "", ps
	}
	node := w.node(u)
	var stage *yaml.Node
	if node != nil {
		stage, _ = field(node, "stage")
	}
	if stage == nil {
		return nil, nil, "", []workspace.Problem{anchored(w, u.fieldOf("stage"))}
	}
	previous := u.Stage
	stage.Value, stage.Tag, stage.Style = w.NextStage(u), "!!str", 0
	saved, ps := w.save(root)
	if saved == nil {
		return nil, nil, "", ps
	}
	return saved, saved.find(u.ID), previous, nil
}

// Anchor 描述在父单元子单元序列中的插入位置：Sibling 为锚点单元的 id，After 表示插在锚点之后；
// Sibling 为空时插入到末尾。unit add 与 unit move 共用。
type Anchor struct {
	Sibling string
	After   bool
}

// AddUnit 在 parentRef 所指单元的子单元中追加（a.Sibling 为空）或按锚点插入一个类型为 typ、id 为 id 的
// 单元，阶段为文体的第一个阶段（汇总单元把它作为存储值，行为以推导阶段为准）。
func AddUnit(root, parentRef, typ, id string, a Anchor) (*Work, *Unit, []workspace.Problem) {
	return addUnit(root, parentRef, typ, id, a, catalog.LoadGenre)
}

func addUnit(root, parentRef, typ, id string, a Anchor, genres genreLookup) (*Work, *Unit, []workspace.Problem) {
	w, parent, ps := resolve(root, parentRef, genres)
	if parent == nil {
		return nil, nil, ps
	}
	for _, t := range w.Genre.UnitTypes {
		if t.ID == parent.Type && !slices.Contains(t.Children, typ) {
			return nil, nil, []workspace.Problem{errorAt(CodeChildTypeNotAllowed, w.Path, parent.fieldOf("units"),
				fmt.Sprintf("单元类型 %s 不能作为 %s 的子单元；%s", typ, t.ID, allowedChildren(t)))}
		}
	}
	node := w.node(parent)
	var units *yaml.Node
	safe := false
	if node != nil {
		units, safe = field(node, "units")
	}
	if !safe {
		return nil, nil, []workspace.Problem{anchored(w, parent.fieldOf("units"))}
	}
	if units == nil {
		units = &yaml.Node{Kind: yaml.SequenceNode, Tag: "!!seq"}
		node.Content = append(node.Content, str("units"), units)
	}
	at, ps := w.insertIndex(parent, units, a)
	if ps != nil {
		return nil, nil, ps
	}
	unit := &yaml.Node{Kind: yaml.MappingNode}
	unit.Content = []*yaml.Node{str("id"), str(id), str("type"), str(typ), str("stage"), str(w.Genre.Stages[0].ID),
		str("units"), {Kind: yaml.SequenceNode, Tag: "!!seq", Style: yaml.FlowStyle}}
	workspace.BlockSequence(node, "units") // 空的流式序列 [] 追加单元后改为块式，便于阅读
	units.Content = slices.Insert(units.Content, at, unit)
	saved, ps := w.save(root)
	if saved == nil {
		return nil, nil, ps
	}
	u := saved.find(id)
	return saved, u, nil
}

// insertIndex 返回锚点 a 在父单元 units 序列中的插入位置。锚点不存在时报告 unit.not-found；
// 锚点不是 parent 的直接子单元时报告 unit.not-sibling。
func (w *Work) insertIndex(parent *Unit, units *yaml.Node, a Anchor) (int, []workspace.Problem) {
	if a.Sibling == "" {
		return len(units.Content), nil
	}
	sibling := w.find(a.Sibling)
	if sibling == nil || sibling.Parent == nil {
		return 0, []workspace.Problem{errorAt(CodeUnitNotFound, w.Path, "",
			fmt.Sprintf("作品 %s 中没有单元（Unit）%s", w.ID, a.Sibling))}
	}
	if sibling.Parent != parent {
		return 0, []workspace.Problem{errorAt(CodeNotSibling, "", "",
			fmt.Sprintf("单元 %s/%s 不是 %s 的直接子单元，不能作为插入锚点", w.ID, a.Sibling, parent.Ref))}
	}
	for i, c := range units.Content {
		if id, _ := mappingField(c, "id"); id != nil && id.Value == a.Sibling {
			if a.After {
				return i + 1, nil
			}
			return i, nil
		}
	}
	// 树中的锚点子单元不在该序列节点里：该序列不是 parent 的直接子单元序列（如经由 YAML 锚点得到）。
	return 0, []workspace.Problem{anchored(w, parent.fieldOf("units"))}
}

// mappingField 返回映射节点 m 中键 key 的值节点；m 不是映射或没有该键时为 nil。
func mappingField(m *yaml.Node, key string) (*yaml.Node, bool) {
	if m == nil || m.Kind != yaml.MappingNode {
		return nil, false
	}
	for i := 0; i+1 < len(m.Content); i += 2 {
		if m.Content[i].Value == key {
			return m.Content[i+1], true
		}
	}
	return nil, false
}

// MoveUnit 把 unitRef 所指单元连同其子树移动为 parentRef 所指单元的子单元（含同父内重排序），
// 插入位置由锚点 a 指定。只修改 work.yaml 中的树位置；单元 id、单元引用与单元目录不变。
func MoveUnit(root, unitRef, parentRef string, a Anchor) (*Work, *Unit, []workspace.Problem) {
	return moveUnit(root, unitRef, parentRef, a, catalog.LoadGenre)
}

func moveUnit(root, unitRef, parentRef string, a Anchor, genres genreLookup) (*Work, *Unit, []workspace.Problem) {
	w, u, ps := resolve(root, unitRef, genres)
	if u == nil {
		return nil, nil, ps
	}
	if u.Parent == nil {
		return nil, nil, []workspace.Problem{errorAt(CodeMoveRoot, "", "",
			fmt.Sprintf("单元 %s 是作品的根单元，没有可以移动到的父单元", u.Ref))}
	}
	workID, unitID, hasUnit := strings.Cut(parentRef, "/")
	if workID != w.ID {
		return nil, nil, []workspace.Problem{errorAt(codeInvalidValue, "", "parent",
			fmt.Sprintf("父单元 %s 属于另一个作品；unit move 只能在作品 %s 内移动", parentRef, w.ID))}
	}
	parent := w.Root
	if hasUnit {
		p := w.find(unitID)
		if p == nil || p.Parent == nil {
			return nil, nil, []workspace.Problem{errorAt(CodeUnitNotFound, w.Path, "",
				fmt.Sprintf("作品 %s 中没有单元（Unit）%s", w.ID, unitID))}
		}
		parent = p
	}
	return w.move(root, u, parent, a)
}

func (w *Work) move(root string, u, parent *Unit, a Anchor) (*Work, *Unit, []workspace.Problem) {
	if parent == u || w.under(parent, u) {
		return nil, nil, []workspace.Problem{errorAt(CodeMoveUnderSelf, "", "",
			fmt.Sprintf("不能把单元 %s 移动到它自身或其子孙单元 %s 之下", u.Ref, parent.Ref))}
	}
	if parent != u.Parent {
		t := w.unitType(parent)
		if t == nil {
			t = &catalog.UnitType{ID: parent.Type}
		}
		if !slices.Contains(t.Children, u.Type) {
			return nil, nil, []workspace.Problem{errorAt(CodeChildTypeNotAllowed, w.Path, parent.fieldOf("units"),
				fmt.Sprintf("单元类型 %s 不能作为 %s 的子单元；%s", u.Type, parent.Type, allowedChildren(*t)))}
		}
	}
	if a.Sibling == u.ID {
		return nil, nil, []workspace.Problem{errorAt(CodeNotSibling, "", "",
			fmt.Sprintf("锚点不能是被移动单元 %s 自身", u.Ref))}
	}
	if a.Sibling != "" && (w.find(a.Sibling) == nil || w.find(a.Sibling).Parent != parent) {
		return nil, nil, w.anchorProblem(parent, a)
	}
	// 被移动的子树本身以及两处 units 序列途经的节点都必须不带锚点、别名或合并键。
	moved := w.node(u)
	if moved == nil || !subtreePlain(moved) {
		return nil, nil, []workspace.Problem{anchored(w, u.fieldOf("units"))}
	}
	from, ps := w.containing(u)
	if ps != nil {
		return nil, nil, ps
	}
	// 先按解析时的 index 取到目标序列并转换为块式，再从原序列摘除子树：摘除会使同级后面的单元左移，
	// 之后再按（解析时的）index 走树就会找错节点。插入位置在摘除后按锚点重新计算。
	to, ps := w.childSeq(parent)
	if ps != nil {
		return nil, nil, ps
	}
	workspace.BlockSequence(w.node(parent), "units")
	last := u.index[len(u.index)-1]
	from.Content = slices.Delete(from.Content, last, last+1)
	at, ps := w.insertIndex(parent, to, a)
	if ps != nil {
		return nil, nil, ps
	}
	to.Content = slices.Insert(to.Content, at, moved)
	saved, ps := w.save(root)
	if saved == nil {
		return nil, nil, ps
	}
	return saved, saved.find(u.ID), nil
}

// anchorProblem 返回锚点 a 相对父单元 parent 的问题：锚点单元不存在或不是 parent 的直接子单元。
func (w *Work) anchorProblem(parent *Unit, a Anchor) []workspace.Problem {
	if w.find(a.Sibling) == nil {
		return []workspace.Problem{errorAt(CodeUnitNotFound, w.Path, "",
			fmt.Sprintf("作品 %s 中没有单元（Unit）%s", w.ID, a.Sibling))}
	}
	return []workspace.Problem{errorAt(CodeNotSibling, "", "",
		fmt.Sprintf("单元 %s/%s 不是 %s 的直接子单元，不能作为插入锚点", w.ID, a.Sibling, parent.Ref))}
}

// containing 返回包含单元 u 的 units 序列节点；途经锚点、别名或合并键时报告 work.anchored-value。
func (w *Work) containing(u *Unit) (*yaml.Node, []workspace.Problem) {
	m := w.doc.Content[0]
	if !plain(m) {
		return nil, []workspace.Problem{anchored(w, u.fieldOf("units"))}
	}
	for _, i := range u.index[:len(u.index)-1] {
		seq, safe := field(m, "units")
		if seq == nil || !safe || i >= len(seq.Content) || !plain(seq.Content[i]) {
			return nil, []workspace.Problem{anchored(w, u.fieldOf("units"))}
		}
		m = seq.Content[i]
	}
	seq, safe := field(m, "units")
	if seq == nil || !safe {
		return nil, []workspace.Problem{anchored(w, u.fieldOf("units"))}
	}
	return seq, nil
}

// childSeq 返回单元 parent 的 units 序列节点（没有时创建）；途经锚点、别名或合并键时报告 work.anchored-value。
func (w *Work) childSeq(parent *Unit) (*yaml.Node, []workspace.Problem) {
	node := w.node(parent)
	if node == nil {
		return nil, []workspace.Problem{anchored(w, parent.fieldOf("units"))}
	}
	units, safe := field(node, "units")
	if !safe {
		return nil, []workspace.Problem{anchored(w, parent.fieldOf("units"))}
	}
	if units == nil {
		units = &yaml.Node{Kind: yaml.SequenceNode, Tag: "!!seq"}
		node.Content = append(node.Content, str("units"), units)
	}
	return units, nil
}

// subtreePlain 报告节点 n 及其全部子孙是否都不带锚点、不是别名（所在映射含合并键由 field 检查）。
func subtreePlain(n *yaml.Node) bool {
	if n == nil {
		return false
	}
	if !plain(n) {
		return false
	}
	for _, c := range n.Content {
		if !subtreePlain(c) {
			return false
		}
	}
	return true
}

// unitType 返回单元 u 的类型声明。
func (w *Work) unitType(u *Unit) *catalog.UnitType {
	for i := range w.Genre.UnitTypes {
		if w.Genre.UnitTypes[i].ID == u.Type {
			return &w.Genre.UnitTypes[i]
		}
	}
	return nil
}

func anchored(w *Work, f string) workspace.Problem {
	return errorAt(CodeAnchoredValue, w.Path, f, "该处经由 YAML 锚点、别名或合并键得到，wa-cli 无法只修改这一处；请先去掉锚点、别名与合并键再修改")
}

// node 返回单元 u 在文档中的映射节点；途经锚点、别名或合并键时返回 nil。
func (w *Work) node(u *Unit) *yaml.Node {
	m := w.doc.Content[0]
	if !plain(m) {
		return nil
	}
	for _, i := range u.index {
		seq, _ := field(m, "units")
		if seq == nil || i >= len(seq.Content) || !plain(seq.Content[i]) {
			return nil
		}
		m = seq.Content[i]
	}
	return m
}

// field 返回映射 m 中键 key 的值节点（不存在时为 nil），以及能否安全地直接修改它：映射含合并键，
// 或值带锚点、是别名时不能。
func field(m *yaml.Node, key string) (*yaml.Node, bool) {
	var v *yaml.Node
	for i := 0; i+1 < len(m.Content); i += 2 {
		if m.Content[i].ShortTag() == "!!merge" {
			return nil, false
		}
		if m.Content[i].Value == key {
			v = m.Content[i+1]
		}
	}
	if v != nil && !plain(v) {
		return nil, false
	}
	return v, true
}

func plain(n *yaml.Node) bool { return n.Anchor == "" && n.Kind != yaml.AliasNode }
