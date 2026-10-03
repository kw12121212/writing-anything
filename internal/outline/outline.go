// Package outline 管理单元目录下的大纲（Outline）outline.yaml：按顺序排列的大纲节（Outline Section），每节声明覆盖的
// 核心观点（Key Message）、使用的素材（Source）或不覆盖任何核心观点的理由。本包加载与校验大纲、按节修改它，并对照同一
// 单元目录下写作简报（Brief）中的核心观点做覆盖追踪（Coverage Tracing），给出覆盖缺口（Coverage Gap），供 internal/work
// 计算大纲关卡的阻塞项。本包不 import internal/work；只返回结构化的问题，由命令层转换为诊断（Diagnostic）。
package outline

import (
	"fmt"
	"maps"
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
	CodeUnknownBeat      = "outline.unknown-beat"
	CodeSectionNotFound  = "outline.section-not-found"
	CodeAnchoredValue    = "outline.anchored-value"
	CodeMissingPart      = "outline.missing-method-part"
	CodeMethodOrder      = "outline.method-order"
	CodeUnknownRole      = "outline.unknown-role"
	CodeUnknownTarget    = "outline.unknown-target-metric"
	CodeTargetOutOfRange = "outline.target-out-of-range"
)

// 场景契约与因果链（Causality Chain）的错误码。
const (
	CodeNotScene         = "scene.not-scene"
	CodeDuplicateEvent   = "scene.duplicate-event"
	CodeUnknownEvent     = "scene.unknown-event"
	CodeEventNotHappened = "scene.event-not-yet-happened"
)

// SceneType 是场景单元（Unit）的单元类型 id；short-story 与 novel 文体都以 scene 声明场景，
// 场景契约（Scene Contract）与因果链检查只作用于该类型的单元。
const SceneType = "scene"

// 单元目录中大纲的文件名：outline.yaml 是结构化的大纲，outline.md 是自由文本，不含可检查的节。
const (
	FileName = "outline.yaml"
	MDName   = "outline.md"
)

// Section 是一个大纲节；未设置的数组为 nil，未设置的 reason 为空字符串。Characters、Beats、Reveal 与 Targets 是轻量场景契约
// （Scene Contract）字段：出场人物、承担的情节节拍（Story Beat）、揭示边界（Reveal Boundary）与按 AI 评分指标（Metric）id
// 声明的目标锚定分值，用于虚构文体。Pov、Location、StateChanges、Plants、Recovers、Causes 与 Events 是长篇场景契约扩展字段：
// 视角人物、地点、必须发生的状态变化、要埋设与回收的伏笔（Foreshadowing）、起因（Cause，引用前文已发生事件的 id）与
// 预期产生的事件（Event）id，由 wa-cli scene contract 写入，供因果链检查与后续的写后收口、上下文包组装使用。
type Section struct {
	ID           string         `json:"id"`
	Title        string         `json:"title"`
	Role         string         `json:"role"` // 该节承担的写作法章节或节拍 id；空表示不承担
	Covers       []string       `json:"covers"`
	Uses         []string       `json:"uses"`
	Reason       string         `json:"reason"`
	Characters   []string       `json:"characters"`
	Beats         []string      `json:"beats"`
	Reveal       string         `json:"reveal"`
	Targets      map[string]int `json:"targets"`
	Pov          string         `json:"pov"`      // 视角人物；空表示未声明
	Location     string         `json:"location"` // 地点；空表示未声明
	StateChanges []StateChange  `json:"state_changes"`
	Plants       []string       `json:"plants"`   // 要埋设的伏笔 id
	Recovers     []string       `json:"recovers"` // 要回收的伏笔 id
	Causes       []string       `json:"causes"`   // 起因：引用前文已发生事件的 id
	Events       []string       `json:"events"`   // 预期产生的事件 id
}

// StateChange 是场景契约中必须发生的一条状态变化：id 供写后收口按稳定键对照提交，text 为描述。
type StateChange struct {
	ID   string `json:"id"`
	Text string `json:"text"`
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
	// Metrics 是按作品文体合并的 AI 评分指标（Metric），供场景契约的 targets 做引用与取值检查；为 nil 时（调用方没有
	// 解析生效配置，如配置本身不通过校验）不检查 targets，整体校验会另行报告配置的问题。
	Metrics []catalog.ScoredMetric
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

// checkIDs 检查节 id 在文件内唯一，以及场景契约 state_changes 的 id 在节内唯一（Schema 的 uniqueItems 只能拦截
// 完全相同的条目，id 相同而描述不同的条目在此检查）。
func checkIDs(rel string, o *Outline) []workspace.Problem {
	var out []workspace.Problem
	seen := map[string]bool{}
	for i, s := range o.Sections {
		if seen[s.ID] {
			out = append(out, errorAt(catalog.CodeDuplicateID, rel, fmt.Sprintf("sections[%d].id", i), "大纲节 id "+s.ID+" 重复"))
		}
		seen[s.ID] = true
		seenSC := map[string]bool{}
		for j, sc := range s.StateChanges {
			if seenSC[sc.ID] {
				out = append(out, errorAt(schema.CodeSchemaViolation, rel, fmt.Sprintf("sections[%d].state_changes[%d].id", i, j),
					"状态变化 id "+sc.ID+" 在该节内重复；state_changes 的 id 必须节内唯一，供写后收口对照"))
			}
			seenSC[sc.ID] = true
		}
	}
	return out
}

// checkRefs 检查 covers 引用写作简报中的核心观点（写作简报不通过校验时不检查）、uses 引用已登记的素材，
// 以及场景契约的 beats 引用同一作品根单元大纲的节（根单元没有大纲或不通过校验时不检查）。
func checkRefs(root, rel string, o *Outline, ms Messages) []workspace.Problem {
	var out []workspace.Problem
	var beats []string
	beatsKnown := false
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
		if len(s.Beats) > 0 {
			if !beatsKnown {
				beats, beatsKnown = beatIDs(root, RootDir(strings.TrimSuffix(rel, "/"+FileName)))
			}
			if beatsKnown {
				for j, id := range s.Beats {
					if !slices.Contains(beats, id) {
						out = append(out, errorAt(CodeUnknownBeat, rel, fmt.Sprintf("sections[%d].beats[%d]", i, j), unknownBeat(id)))
					}
				}
			}
		}
		// 调用方没有解析生效配置（如关卡路径）时 ms.Metrics 为 nil，跳过 targets 检查，由整体校验报告。
		if ms.Metrics != nil {
			for _, id := range slices.Sorted(maps.Keys(s.Targets)) {
				field := fmt.Sprintf("sections[%d].targets.%s", i, id)
				j := slices.IndexFunc(ms.Metrics, func(m catalog.ScoredMetric) bool { return m.ID == id })
				if j < 0 {
					out = append(out, errorAt(CodeUnknownTarget, rel, field, unknownTarget(id)))
					continue
				}
				if v := s.Targets[id]; v < ms.Metrics[j].Scale.Min || v > ms.Metrics[j].Scale.Max {
					out = append(out, errorAt(CodeTargetOutOfRange, rel, field,
						fmt.Sprintf("目标分值 %d 不在 AI 评分指标（Metric）%s 的 scale（%d 到 %d）内", v, id, ms.Metrics[j].Scale.Min, ms.Metrics[j].Scale.Max)))
				}
			}
		}
	}
	return out
}

// RootDir 返回单元目录 dir 所在作品根单元的目录：根单元的目录是它本身，子单元目录形如 <作品>/units/<id>。
// 作品 id 是去掉 works 前缀后的第一段，不能在完整路径中搜索 /units/——作品 id 本身可以叫 units（structural-path-parsing）。
func RootDir(dir string) string {
	if !strings.HasPrefix(dir, workspace.WorksPath+"/") {
		return dir
	}
	rest := strings.TrimPrefix(dir, workspace.WorksPath+"/")
	if i := strings.Index(rest, "/"); i >= 0 {
		return workspace.WorksPath + "/" + rest[:i]
	}
	return dir
}

// beatIDs 返回作品根单元目录 rootDir 下大纲（Outline）的节 id，即可引用的情节节拍（Story Beat）。根单元没有大纲、
// 或大纲不通过 Schema 与节 id 唯一性检查时不做引用检查，返回 false。
func beatIDs(root, rootDir string) ([]string, bool) {
	o := Loadable(root, rootDir)
	if o == nil {
		return nil, false
	}
	ids := make([]string, len(o.Sections))
	for i, s := range o.Sections {
		ids[i] = s.ID
	}
	return ids, true
}

// Loadable 读取单元目录 dir 下的 outline.yaml 并做 Schema 与节 id（含 state_changes id）唯一检查；没有文件、读取失败
// 或不通过检查时返回 nil。引用问题（covers、uses、beats、targets 与因果链）不妨碍返回，由各自的检查报告。
func Loadable(root, dir string) *Outline {
	rel := Path(dir)
	data, ok, err := workspace.ReadRegular(root, rel)
	if err != nil || !ok {
		return nil
	}
	doc, p := schema.Parse(data)
	if p != nil {
		return nil
	}
	if ps := schema.Named("outline").ValidateNode(doc); len(ps) > 0 {
		return nil
	}
	var o Outline
	catalog.Decode(doc, &o)
	if len(checkIDs(rel, &o)) > 0 {
		return nil
	}
	return &o
}

// Scene 是因果链（Causality Chain）检查中的一个场景单元：Ref 为单元引用（用于诊断 message），Dir 为单元目录
// （相对项目目录）。Outline 非 nil 时代替磁盘上的大纲，供 scene contract 的写前检查以写入后的状态运行。
type Scene struct {
	Ref     string
	Dir     string
	Outline *Outline
}

// causality 检查同一作品按单元树先序（Pre-order）给出的场景序列 scenes 的因果链：事件（Event）id 由场景大纲节的
// events 声明，在作品内跨场景唯一，同一 id 被之后的场景再次声明时对每个后声明的节报告 scene.duplicate-event；
// 每个场景大纲节的 causes 引用的事件 id 没有任何场景声明时报告 scene.unknown-event，声明它的场景不按先序排在
// 该场景之前（同一场景或更靠后）时报告 scene.event-not-yet-happened。大纲不通过 Schema 与节 id 检查的场景不参与
// 因果链（其问题由整体校验另行报告）；非场景单元大纲节上的 causes 与 events 不参与因果链。
func causality(root string, scenes []Scene) []workspace.Problem {
	type declared struct{ scene int }
	first := map[string]declared{}
	os := make([]*Outline, len(scenes))
	var out []workspace.Problem
	for i, sc := range scenes {
		o := sc.Outline
		if o == nil {
			o = Loadable(root, sc.Dir)
		}
		if o == nil {
			continue
		}
		os[i] = o
		for si, s := range o.Sections {
			for ei, id := range s.Events {
				if d, dup := first[id]; dup {
					out = append(out, errorAt(CodeDuplicateEvent, Path(sc.Dir), fmt.Sprintf("sections[%d].events[%d]", si, ei),
						fmt.Sprintf("事件（Event）%s 已由场景 %s 的大纲声明；预期产生的事件 id 在作品内唯一", id, scenes[d.scene].Ref)))
					continue
				}
				first[id] = declared{i}
			}
		}
	}
	for i, o := range os {
		if o == nil {
			continue
		}
		for si, s := range o.Sections {
			for ci, id := range s.Causes {
				d, ok := first[id]
				switch {
				case !ok:
					out = append(out, errorAt(CodeUnknownEvent, Path(scenes[i].Dir), fmt.Sprintf("sections[%d].causes[%d]", si, ci),
						fmt.Sprintf("起因（Cause）引用的事件（Event）%s 没有被任何场景的大纲声明；起因只能引用前文场景 events 声明的事件 id", id)))
				case d.scene >= i:
					out = append(out, errorAt(CodeEventNotHappened, Path(scenes[i].Dir), fmt.Sprintf("sections[%d].causes[%d]", si, ci),
						fmt.Sprintf("起因（Cause）引用的事件（Event）%s 由场景 %s 声明，按单元树先序不排在本场景之前，尚未发生；起因只能引用前文已发生的事件", id, scenes[d.scene].Ref)))
				}
			}
		}
	}
	return out
}

// Causality 返回作品全部场景的因果链问题，供整体校验（wa-cli validate）使用；scenes 必须是同一作品全部场景
// 单元的单元树先序序列。
func Causality(root string, scenes []Scene) []workspace.Problem {
	return causality(root, scenes)
}

// CausalityOn 返回报告在单元目录 dir 的大纲上的因果链问题（该场景的 causes，以及它作为后声明者的重复事件），
// 供单产物校验（wa-cli validate <path>）、大纲关卡（outline.invalid）与 scene contract 的写前检查使用。
func CausalityOn(root string, scenes []Scene, dir string) []workspace.Problem {
	var out []workspace.Problem
	for _, p := range causality(root, scenes) {
		if p.Path == Path(dir) {
			out = append(out, p)
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

func unknownBeat(id string) string {
	return "情节节拍（Story Beat）" + id + " 不在故事根单元的大纲（Outline）节中；场景契约（Scene Contract）的 beats 只能引用根单元大纲的节 id"
}

func unknownTarget(id string) string {
	return "AI 评分指标（Metric）" + id + " 不是按作品文体合并的已声明指标；场景契约（Scene Contract）的 targets 只能引用已声明的指标"
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
// 已加载的决策队列（load-once-per-command），defaultMethod 返回单元目录所在作品的文体的默认写作法，metricsOf 返回
// 该作品按文体合并的 AI 评分指标（无法解析生效配置时返回 nil，跳过 targets 检查）。
func CheckUnitArtifacts(root string, dirs []string, q brief.Queue, defaultMethod func(dir string) string, metricsOf func(dir string) []catalog.ScoredMetric) (checked []string, problems []workspace.Problem) {
	set := catalog.LoadMethods(root) // 写作法目录每条命令只加载一次
	for _, dir := range dirs {
		if _, err := os.Lstat(filepath.Join(root, filepath.FromSlash(Path(dir)))); err != nil {
			continue
		}
		checked = append(checked, Path(dir))
		problems = append(problems, checkDir(root, dir, q, Picker{set, defaultMethod(dir)}, metricsOf(dir))...)
	}
	return checked, problems
}

func checkDir(root, dir string, q brief.Queue, pick Picker, metrics []catalog.ScoredMetric) []workspace.Problem {
	ms := LoadMessages(root, dir, q, pick)
	ms.Metrics = metrics
	o, ps := Load(root, dir, ms)
	if o == nil || len(ms.Problems) > 0 {
		// 写作简报不通过校验时不报告覆盖缺口，写作简报的问题由它自己的校验报告。
		return ps
	}
	return Problems(o.Path, Gaps(o, ms))
}

// CheckArtifact 对已通过 Schema 校验的 outline.yaml（rel）做领域检查与覆盖追踪；defaultMethod 为所在作品的文体的默认写作法，
// metrics 为该作品按文体合并的 AI 评分指标（nil 时跳过 targets 检查）。scenes 为该作品全部场景单元的先序序列，
// 因果链问题只报告该文件自己的部分；没有场景（作品无法加载）时跳过因果链检查。
func CheckArtifact(root, rel, defaultMethod string, metrics []catalog.ScoredMetric, scenes []Scene) []workspace.Problem {
	dir := strings.TrimSuffix(rel, "/"+FileName)
	ps := checkDir(root, dir, brief.LoadQueue(root), Picker{catalog.LoadMethods(root), defaultMethod}, metrics)
	return append(ps, CausalityOn(root, scenes, dir)...)
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
