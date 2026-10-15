// Package closeout 管理场景单元目录下的写后收口（Close-out）产物 closeout.yaml：场景草拟后 Agent 经
// wa-cli scene close-out 一次提交抽取结果——新增设定、状态变化、知情变化、伏笔埋设与回收、确认产生的事件——
// 把记忆台账与正文同步。提交按写入后状态对照场景契约（Scene Contract）：契约要求的状态变化、伏笔回收与事件
// 必须齐备才写入，任一预检失败时不写任何文件；同时以场景大纲声明的人物与地点及正文中的人名上下文为候选扫描
// 疑似专有名词（Suspected Proper Noun），未解析到设定集（Canon）条目 name 或别名的取值以 warning 提示补录或豁免。
// 收口记录各台账写入的条目 id 与正文指纹；收口未完成、产物失效或记录的提议仍未经作者处理时场景不能验收。
// 台账写入全部复用 canon、state、knowledge、threads 包的导出函数（各包自带领域校验与追加语义）；本包不
// import internal/cli，只返回结构化的问题，由命令层转换为诊断（Diagnostic）。
package closeout

import (
	"crypto/sha256"
	"encoding/hex"
	"fmt"
	"os"
	"path/filepath"
	"slices"
	"strings"

	"github.com/kw12121212/writing-anything/internal/canon"
	"github.com/kw12121212/writing-anything/internal/catalog"
	"github.com/kw12121212/writing-anything/internal/draft"
	"github.com/kw12121212/writing-anything/internal/knowledge"
	"github.com/kw12121212/writing-anything/internal/outline"
	"github.com/kw12121212/writing-anything/internal/schema"
	"github.com/kw12121212/writing-anything/internal/source"
	"github.com/kw12121212/writing-anything/internal/state"
	"github.com/kw12121212/writing-anything/internal/threads"
	"github.com/kw12121212/writing-anything/internal/work"
	"github.com/kw12121212/writing-anything/internal/workspace"
	"go.yaml.in/yaml/v3"
)

// 写后收口的错误码（Error Code）。已发布的错误码不得改名或复用。
const (
	CodeUnknownEntry       = "closeout.unknown-entry"
	CodeUnknownEvent       = "closeout.unknown-event"
	CodeMissingStateChange = "closeout.missing-state-change"
	CodeMissingRecovery    = "closeout.missing-recovery"
	CodeMissingPlant       = "closeout.missing-plant"
	CodeMissingEvent       = "closeout.missing-event"
	CodeSuspectedName      = "closeout.suspected-name"
	CodeAlreadyClosed      = "closeout.already-closed"
	CodeNotClosed          = "closeout.not-closed"
	CodeStale              = "closeout.stale"
	CodeMissing            = "closeout.missing"
	CodeInvalid            = "closeout.invalid"
	CodePendingProposals   = "closeout.pending-proposals"
)

// FileName 是收口产物文件名。
const FileName = "closeout.yaml"

// Path 返回单元目录 dir 下 closeout.yaml 的路径（相对项目目录）。
func Path(dir string) string { return dir + "/" + FileName }

// StateRef 是收口记录中的一条状态变化提交：id 为台账条目，contract 为对应的契约节内 state_changes 的 id
// （计划外提交省略）。验收复查按 contract 关联对照契约。
type StateRef struct {
	ID       string `json:"id"`
	Contract string `json:"contract,omitempty"`
}

type fileRecord struct {
	SchemaVersion int        `json:"schema_version"`
	DraftSHA256   string     `json:"draft_sha256"`
	Events        []string   `json:"events"`
	Canon         []string   `json:"canon"`
	State         []StateRef `json:"state"`
	Knowledge     []string   `json:"knowledge"`
	Plants        []string   `json:"plants"`
	Recovers      []string   `json:"recovers"`
	Waived        []string   `json:"waived"`
}

// Suspected 是一个疑似专有名词：取值与它在大纲中的来源字段。
type Suspected struct {
	Name   string `json:"name"`
	Source string `json:"source"`
}

// View 是 scene close-out 的输出：收口记录与扫描命中的疑似专有名词名单。
type View struct {
	Unit        string      `json:"unit"`
	Path        string      `json:"path"`
	DraftSHA256 string      `json:"draftSha256"`
	Events      []string    `json:"events"`
	Canon       []string    `json:"canon"`
	State       []StateRef  `json:"state"`
	Knowledge   []string    `json:"knowledge"`
	Plants      []string    `json:"plants"`
	Recovers    []string    `json:"recovers"`
	Waived      []string    `json:"waived"`
	Suspected   []Suspected `json:"suspected"`
}

// CanonInput 是收口输入 canon 节的一个新增设定条目；字段语义与 canon add 相同（Name 为空表示未给出）。
type CanonInput struct {
	ID      string
	Type    string
	Name    string
	Aliases []string
	Text    string
	Sources []string
}

// StateChangeInput 是收口输入 state_changes 节的一条提交；Contract 关联契约节内 state_changes 的 id。
type StateChangeInput struct {
	ID       string
	Contract string
	Entry    string
	State    string
}

// KnowledgeInput 是收口输入 knowledge 节的一条提交。
type KnowledgeInput struct {
	ID        string
	Entry     string
	Character string
}

// PlantInput 是收口输入 plants 节的一条计划外埋设。
type PlantInput struct {
	ID        string
	Text      string
	RecoverAt string
}

// Input 是收口输入（closeout-input Schema 的解码结果）；各节缺省视为空。
type Input struct {
	Canon        []CanonInput
	StateChanges []StateChangeInput
	Knowledge    []KnowledgeInput
	Plants       []PlantInput
	Recovers     []string
	Events       []string
	Waived       []string
}

func errorAt(code, path, field, msg string) workspace.Problem {
	return workspace.Problem{Code: code, Severity: workspace.SeverityError, Path: path, Field: field, Message: msg}
}

func sha256hex(data []byte) string {
	sum := sha256.Sum256(data)
	return hex.EncodeToString(sum[:])
}

// ParseInput 解析并校验收口输入（--file 或标准输入的 YAML）：Schema 问题以 artifact.schema-violation /
// artifact.parse-error 报告（不填 path，field 为输入中的位置）。
func ParseInput(data []byte) (Input, []workspace.Problem) {
	doc, p := schema.Parse(data)
	if p != nil {
		return Input{}, workspace.FromSchema("", []schema.Problem{*p})
	}
	if ps := schema.Named("closeout-input").ValidateNode(doc); len(ps) > 0 {
		return Input{}, workspace.FromSchema("", ps)
	}
	var in struct {
		Canon []struct {
			ID      string   `json:"id"`
			Type    string   `json:"type"`
			Name    string   `json:"name"`
			Aliases []string `json:"aliases"`
			Text    string   `json:"text"`
			Sources []string `json:"sources"`
		} `json:"canon"`
		StateChanges []struct {
			ID       string `json:"id"`
			Contract string `json:"contract"`
			Entry    string `json:"entry"`
			State    string `json:"state"`
		} `json:"state_changes"`
		Knowledge []struct {
			ID        string `json:"id"`
			Entry     string `json:"entry"`
			Character string `json:"character"`
		} `json:"knowledge"`
		Plants []struct {
			ID        string `json:"id"`
			Text      string `json:"text"`
			RecoverAt string `json:"recover_at"`
		} `json:"plants"`
		Recovers []string `json:"recovers"`
		Events   []string `json:"events"`
		Waived   []string `json:"waived"`
	}
	catalog.Decode(doc, &in)
	out := Input{Recovers: orEmpty(in.Recovers), Events: orEmpty(in.Events), Waived: orEmpty(in.Waived)}
	for _, c := range in.Canon {
		out.Canon = append(out.Canon, CanonInput{ID: c.ID, Type: c.Type, Name: c.Name, Aliases: orEmpty(c.Aliases), Text: c.Text, Sources: orEmpty(c.Sources)})
	}
	for _, s := range in.StateChanges {
		out.StateChanges = append(out.StateChanges, StateChangeInput{ID: s.ID, Contract: s.Contract, Entry: s.Entry, State: s.State})
	}
	for _, k := range in.Knowledge {
		out.Knowledge = append(out.Knowledge, KnowledgeInput{ID: k.ID, Entry: k.Entry, Character: k.Character})
	}
	for _, p := range in.Plants {
		out.Plants = append(out.Plants, PlantInput{ID: p.ID, Text: p.Text, RecoverAt: p.RecoverAt})
	}
	return out, nil
}

func orEmpty(vs []string) []string {
	if vs == nil {
		return []string{}
	}
	return vs
}

// sceneOutline 读取场景单元目录下的大纲并解码节（不需要写作简报与写作法上下文：收口只消费契约字段）。
// outline.yaml 不存在时返回 nil 与 nil（契约视为空）；存在但不通过 Schema 校验（含途经符号链接按 artifact.not-found
// 处理）时返回其问题。
func sceneOutline(root, unitDir string) (*outline.Outline, []workspace.Problem) {
	rel := outline.Path(unitDir)
	if _, ok, _ := workspace.ReadRegular(root, rel); !ok {
		if info, err := os.Lstat(filepath.Join(root, filepath.FromSlash(rel))); err == nil && !info.Mode().IsRegular() {
			return nil, []workspace.Problem{errorAt(schema.CodeNotFound, rel, "", "outline.yaml 途经符号链接或不是普通文件，按不存在处理")}
		}
		return nil, nil
	}
	doc, ps := workspace.Read(root, rel)
	if doc == nil {
		return nil, ps
	}
	if ps := schema.Named("outline").ValidateNode(doc); len(ps) > 0 {
		return nil, workspace.FromSchema(rel, ps)
	}
	var f struct {
		Sections []outline.Section `json:"sections"`
	}
	catalog.Decode(doc, &f)
	return &outline.Outline{Path: rel, Sections: f.Sections}, nil
}

// snapshot 是一次命令中加载的作品级台账现状（各 List 的 error 级问题阻塞收口，warning 级悬空引用忽略——
// 它们由整体校验报告，不阻止收口，见 workspace-artifacts/cross-artifact-references）。
type snapshot struct {
	canon   map[string]canon.Entry
	canonN  int
	states  map[string]state.Event
	knows   map[string]knowledge.Event
	threads map[string]threads.Entry
}

func loadLedgers(root string, w *work.Work) (*snapshot, []workspace.Problem) {
	s := &snapshot{canon: map[string]canon.Entry{}, states: map[string]state.Event{}, knows: map[string]knowledge.Event{}, threads: map[string]threads.Entry{}}
	entries, ps := canon.List(root, w)
	if entries == nil && hasError(ps) {
		return nil, ps
	}
	for _, e := range entries {
		s.canon[e.ID] = e
	}
	s.canonN = len(entries)
	events, ps := state.List(root, w, true)
	if events == nil && hasError(ps) {
		return nil, ps
	}
	for _, e := range events {
		s.states[e.ID] = e
	}
	knows, ps := knowledge.List(root, w, true)
	if knows == nil && hasError(ps) {
		return nil, ps
	}
	for _, e := range knows {
		s.knows[e.ID] = e
	}
	items, ps := threads.List(root, w, true)
	if items == nil && hasError(ps) {
		return nil, ps
	}
	for _, it := range items {
		s.threads[it.ID] = it.Entry
	}
	return s, nil
}

// appendable 报告 rel 处台账的追加键 key 是否可直接追加（追加序列不带锚点、不是别名，所在映射不含合并键）；
// 文件不存在时为 nil。文件存在但无法解析时其问题已由 loadLedgers 报告，此处返回 nil。
func appendable(root, rel, key string) *workspace.Problem {
	if _, ok, _ := workspace.ReadRegular(root, rel); !ok {
		return nil
	}
	doc, _ := workspace.Read(root, rel)
	if doc == nil || len(doc.Content) == 0 {
		return nil
	}
	m := doc.Content[0]
	for i := 0; i+1 < len(m.Content); i += 2 {
		if m.Content[i].ShortTag() == "!!merge" || (m.Content[i].Value == key && (m.Content[i+1].Anchor != "" || m.Content[i+1].Kind == yaml.AliasNode)) {
			return &workspace.Problem{Code: CodeInvalid, Severity: workspace.SeverityError, Path: rel, Field: key,
				Message: rel + " 的 " + key + " 经由 YAML 锚点、别名或合并键得到，wa-cli 无法只追加一项；请先去掉锚点、别名与合并键"}
		}
	}
	return nil
}

func hasError(ps []workspace.Problem) bool {
	return slices.ContainsFunc(ps, func(p workspace.Problem) bool { return p.Severity == workspace.SeverityError })
}

// load 读取并校验单元目录下的收口产物；文件不存在时返回 nil 与 nil；途经符号链接按 artifact.not-found 处理，
// Schema 问题原样返回。领域检查由 checkRecord 负责。
func load(root, unitDir string) (*yaml.Node, *fileRecord, []workspace.Problem) {
	rel := Path(unitDir)
	if _, ok, _ := workspace.ReadRegular(root, rel); !ok {
		if info, err := os.Lstat(filepath.Join(root, filepath.FromSlash(rel))); err == nil && !info.Mode().IsRegular() {
			return nil, nil, []workspace.Problem{errorAt(schema.CodeNotFound, rel, "", "closeout.yaml 途经符号链接或不是普通文件，按不存在处理")}
		}
		return nil, nil, nil
	}
	doc, ps := workspace.Read(root, rel)
	if doc == nil {
		return nil, nil, ps
	}
	if ps := schema.Named("closeout").ValidateNode(doc); len(ps) > 0 {
		return nil, nil, workspace.FromSchema(rel, ps)
	}
	var f fileRecord
	catalog.Decode(doc, &f)
	return doc, &f, nil
}

// contractStateChanges 返回大纲全部节的契约字段合并：必须发生的状态变化、要埋设与回收的伏笔和预期事件。
func contractStateChanges(ol *outline.Outline) (changes []outline.StateChange, plants, recovers, events []string) {
	if ol == nil {
		return nil, nil, nil, nil
	}
	for _, s := range ol.Sections {
		changes = append(changes, s.StateChanges...)
		plants = append(plants, s.Plants...)
		recovers = append(recovers, s.Recovers...)
		events = append(events, s.Events...)
	}
	return changes, plants, recovers, events
}

// declaredEvents 返回大纲全部节声明的事件 id 集合。
func declaredEvents(ol *outline.Outline) map[string]bool {
	_, _, _, events := contractStateChanges(ol)
	set := map[string]bool{}
	for _, e := range events {
		set[e] = true
	}
	return set
}

// requiresCloseout 报告场景大纲是否声明了要求收口的契约字段（任一节 state_changes、plants、recovers 或 events 非空）。
func requiresCloseout(ol *outline.Outline) bool {
	changes, plants, recovers, events := contractStateChanges(ol)
	return len(changes) > 0 || len(plants) > 0 || len(recovers) > 0 || len(events) > 0
}

// Record 是收口记录（closeout.yaml 的解码结果）。
type Record = fileRecord

// Load 读取并校验单元目录下的收口记录，供卷末检查点等消费方复用；不存在时返回 nil 与 nil。
func Load(root, unitDir string) (*Record, []workspace.Problem) {
	_, f, ps := load(root, unitDir)
	return f, ps
}

// checkRecord 对已通过 Schema 校验的收口产物做领域检查：各 id 引用的台账条目存在且绑定该场景，events 在该场景
// 大纲声明中。snap 为 nil 时跳过台账引用检查（台账问题由台账校验报告），ol 为 nil 时 events 对照空集合（无大纲
// 声明任何事件），u 为 nil 时跳过绑定场景检查（所在单元无法解析）。
func checkRecord(rel string, f *fileRecord, u *work.Unit, snap *snapshot, ol *outline.Outline) (problems []workspace.Problem) {
	unitRef := ""
	if u != nil {
		unitRef = u.Ref
	}
	declared := declaredEvents(ol)
	for i, id := range f.Events {
		if !declared[id] {
			problems = append(problems, errorAt(CodeUnknownEvent, rel, fmt.Sprintf("events[%d]", i),
				"事件 "+id+" 不在该场景大纲声明的 events 中；事件（Event）由场景大纲声明"))
		}
	}
	if snap == nil {
		return problems
	}
	for i, r := range f.State {
		e, ok := snap.states[r.ID]
		if !ok || (unitRef != "" && e.Scene != unitRef) {
			problems = append(problems, errorAt(CodeUnknownEntry, rel, fmt.Sprintf("state[%d].id", i),
				"状态台账中没有绑定场景 "+unitRef+" 的条目 "+r.ID))
		}
	}
	for i, id := range f.Knowledge {
		e, ok := snap.knows[id]
		if !ok || (unitRef != "" && e.Scene != unitRef) {
			problems = append(problems, errorAt(CodeUnknownEntry, rel, fmt.Sprintf("knowledge[%d]", i),
				"知情表中没有绑定场景 "+unitRef+" 的条目 "+id))
		}
	}
	for i, id := range f.Plants {
		t, ok := snap.threads[id]
		if !ok || (unitRef != "" && t.PlantedIn != unitRef) {
			problems = append(problems, errorAt(CodeUnknownEntry, rel, fmt.Sprintf("plants[%d]", i),
				"伏笔台账中没有 planted_in 为 "+unitRef+" 的条目 "+id))
		}
	}
	for i, id := range f.Recovers {
		t, ok := snap.threads[id]
		if !ok || t.Status != threads.StatusPaid || (unitRef != "" && (t.RecoveredIn == nil || *t.RecoveredIn != unitRef)) {
			problems = append(problems, errorAt(CodeUnknownEntry, rel, fmt.Sprintf("recovers[%d]", i),
				"伏笔台账中没有在该场景回收（status 为 paid 且 recovered_in 为 "+unitRef+"）的条目 "+id))
		}
	}
	for i, id := range f.Canon {
		if _, ok := snap.canon[id]; !ok {
			problems = append(problems, errorAt(CodeUnknownEntry, rel, fmt.Sprintf("canon[%d]", i),
				"设定集（canon.yaml）中没有条目 "+id))
		}
	}
	return problems
}

// nextID 按 <prefix>-<n> 从 count+1 起递增生成不与 used 重复的 id（与各台账的缺省生成规则一致）。
func nextID(prefix string, used map[string]bool, count int) string {
	for n := count + 1; ; n++ {
		if id := fmt.Sprintf("%s-%d", prefix, n); !used[id] {
			return id
		}
	}
}

// units 返回作品全部单元的先序位置与单元索引。
func units(w *work.Work) (pos map[string]int, byRef map[string]*work.Unit) {
	us := w.Units()
	pos = make(map[string]int, len(us))
	byRef = make(map[string]*work.Unit, len(us))
	for i, u := range us {
		pos[u.Ref], byRef[u.Ref] = i, u
	}
	return pos, byRef
}

// Submit 完成场景 u 的写后收口：先全部预检（closeout.yaml 已存在且未 --replace、正文存在、大纲与台账可加载、
// 载荷引用合法、按写入后状态对照契约），通过后依次以 canon add、state propose、knowledge propose、threads add、
// threads set 的语义写入各台账，再整体写出 closeout.yaml。预检或对照失败时不写任何文件。--replace 整体替换既有
// closeout.yaml，不清理已写入的台账条目。返回收口视图、变更的路径与 warning（疑似专有名词）；出错时返回问题。
func Submit(root string, w *work.Work, u *work.Unit, in Input, replace bool) (*View, []string, []workspace.Problem) {
	if u.Type != outline.SceneType {
		return nil, nil, []workspace.Problem{{Code: outline.CodeNotScene, Severity: workspace.SeverityError, Field: "unit",
			Message: "单元 " + u.Ref + " 的类型是 " + u.Type + "；scene close-out 只收口 scene 类型单元（场景）"}}
	}
	unitDir := w.UnitDir(u)
	rel := Path(unitDir)
	if doc, _, ps := load(root, unitDir); len(ps) > 0 {
		return nil, nil, ps
	} else if doc != nil && !replace {
		return nil, nil, []workspace.Problem{errorAt(CodeAlreadyClosed, rel, "",
			"场景 "+u.Ref+" 已收口（"+rel+" 存在）；改契约或正文后重收口请给出 --replace（整体替换收口记录，不撤销已写入的台账条目）")}
	}
	content, ok, ps := draft.Read(root, w, u)
	if len(ps) > 0 {
		return nil, nil, ps
	}
	if !ok {
		return nil, nil, []workspace.Problem{errorAt(draft.CodeNotFound, unitDir+"/"+draft.FileName, "",
			"单元 "+u.Ref+" 还没有正文（draft.md）；场景草拟（wa-cli draft write）之后才能收口")}
	}
	sha := sha256hex(content)
	ol, ps := sceneOutline(root, unitDir)
	if ps != nil {
		return nil, nil, ps
	}
	snap, ps := loadLedgers(root, w)
	if ps != nil {
		return nil, nil, ps
	}
	_, byRef := units(w)

	// 载荷 canon 节的 id 预演（缺省 canon-<n>），并构建写入后的设定集索引：state 与 knowledge 的 entry 引用
	// 可以指向本次新增的条目，疑似专有名词扫描也按写入后的 name 与别名匹配。
	canonAfter := make(map[string]canon.Entry, len(snap.canon)+len(in.Canon))
	for id, e := range snap.canon {
		canonAfter[id] = e
	}
	usedCanon := make(map[string]bool, len(snap.canon)+len(in.Canon))
	for id := range snap.canon {
		usedCanon[id] = true
	}
	canonIDs := make([]string, len(in.Canon))
	for i, c := range in.Canon {
		id := c.ID
		if id == "" {
			id = nextID("canon", usedCanon, snap.canonN+i)
		}
		if usedCanon[id] {
			return nil, nil, []workspace.Problem{errorAt(catalog.CodeDuplicateID, "", fmt.Sprintf("canon[%d].id", i),
				"设定集（canon.yaml）或载荷中已有条目 "+id)}
		}
		usedCanon[id] = true
		canonIDs[i] = id
		// canon add 会拒绝的参数与领域问题在预检中先行报告：多条 canon 时，后续条目失败不应留下前面已写入的
		// 条目（precheck-before-side-effects）。
		field := fmt.Sprintf("canon[%d]", i)
		switch c.Type {
		case canon.TypeWorldRule, canon.TypeRelationship:
			if c.Name != "" {
				return nil, nil, []workspace.Problem{errorAt(schema.CodeSchemaViolation, "", field+".name",
					c.Type+" 类型的设定条目不得有 name")}
			}
			if len(c.Aliases) > 0 {
				return nil, nil, []workspace.Problem{errorAt(schema.CodeSchemaViolation, "", field+".aliases",
					c.Type+" 类型的设定条目不得有别名（Alias）")}
			}
			if c.Type == canon.TypeRelationship {
				return nil, nil, []workspace.Problem{errorAt(schema.CodeSchemaViolation, "", field+".type",
					"收口载荷不支持 relationship 条目：人物关系须由 wa-cli canon add 给出 --members 登记")}
			}
		case canon.TypeCharacter, canon.TypeLocation, canon.TypeItem:
			if strings.TrimSpace(c.Name) == "" {
				return nil, nil, []workspace.Problem{errorAt(schema.CodeSchemaViolation, "", field+".name",
					c.Type+" 类型的设定条目必须有非空的 name")}
			}
		}
		for j, a := range c.Aliases {
			if strings.TrimSpace(a) == "" || slices.Contains(c.Aliases[:j], a) {
				return nil, nil, []workspace.Problem{errorAt(schema.CodeSchemaViolation, "", fmt.Sprintf("%s.aliases[%d]", field, j),
					"别名（Alias）不能为空、只含空白或彼此重复")}
			}
		}
		for j, s := range c.Sources {
			if !source.Registered(root, s) {
				return nil, nil, []workspace.Problem{errorAt(canon.CodeUnknownSource, "", fmt.Sprintf("%s.sources[%d]", field, j),
					"素材 "+s+" 未登记；收口载荷的 sources 只能引用已登记素材")}
			}
		}
		var name *string
		if c.Name != "" {
			name = &c.Name
		}
		canonAfter[id] = canon.Entry{ID: id, Type: c.Type, Name: name, Aliases: orEmpty(c.Aliases), Text: c.Text, Sources: orEmpty(c.Sources)}
	}
	// 别名冲突（canon.duplicate-alias）：载荷条目的别名不得与写入后任何条目的 name、其他条目的别名或本条目 name 重复。
	var aliasProblems []workspace.Problem
	for i, c := range in.Canon {
		names := map[string]string{}
		for id, e := range canonAfter {
			for _, n := range e.Names() {
				if _, ok := names[n]; !ok {
					names[n] = id
				}
			}
		}
		for j, a := range c.Aliases {
			if holder, ok := names[a]; ok {
				aliasProblems = append(aliasProblems, errorAt(canon.CodeDuplicateAlias, "", fmt.Sprintf("canon[%d].aliases[%d]", i, j),
					"别名 "+a+" 与条目 "+holder+" 的 name 或别名重复"))
			}
		}
	}
	if len(aliasProblems) > 0 {
		return nil, nil, aliasProblems
	}
	// 各台账的追加点途经锚点、别名或合并键时先报告：写入函数自身的 anchored 检查也会失败，但那会留下更早
	// 写入的文件（precheck-before-side-effects）。
	for _, t := range []struct{ rel, key string }{
		{canon.Path(w.ID), "entries"},
		{state.Path(w.ID), "events"},
		{knowledge.Path(w.ID), "events"},
		{threads.Path(w.ID), "entries"},
	} {
		if p := appendable(root, t.rel, t.key); p != nil {
			return nil, nil, []workspace.Problem{*p}
		}
	}

	// 载荷引用与重复 id 预检（写入函数自身也会校验，但 state、knowledge 与 threads 的写入排在 canon 之后，
	// 失败时 canon.yaml 已写入：全部预检过才开始写，precheck-before-side-effects）。
	usedState := make(map[string]bool, len(snap.states)+len(in.StateChanges))
	for id := range snap.states {
		usedState[id] = true
	}
	for i, sc := range in.StateChanges {
		if _, ok := canonAfter[sc.Entry]; !ok {
			return nil, nil, []workspace.Problem{errorAt(state.CodeUnknownEntry, "", fmt.Sprintf("state_changes[%d].entry", i),
				"设定条目 "+sc.Entry+" 不在设定集（canon.yaml）或本次 canon 节中；entry 只能引用已登记的设定条目")}
		}
		if sc.ID != "" && usedState[sc.ID] {
			return nil, nil, []workspace.Problem{errorAt(catalog.CodeDuplicateID, "", fmt.Sprintf("state_changes[%d].id", i),
				"状态台账或载荷中已有条目 "+sc.ID)}
		}
		usedState[sc.ID] = true
	}
	usedKnow := make(map[string]bool, len(snap.knows)+len(in.Knowledge))
	for id := range snap.knows {
		usedKnow[id] = true
	}
	for i, k := range in.Knowledge {
		if k.ID != "" && usedKnow[k.ID] {
			return nil, nil, []workspace.Problem{errorAt(catalog.CodeDuplicateID, "", fmt.Sprintf("knowledge[%d].id", i),
				"知情表或载荷中已有条目 "+k.ID)}
		}
		usedKnow[k.ID] = true
		if _, ok := canonAfter[k.Entry]; !ok {
			return nil, nil, []workspace.Problem{errorAt(knowledge.CodeUnknownEntry, "", fmt.Sprintf("knowledge[%d].entry", i),
				"设定条目 "+k.Entry+" 不在设定集（canon.yaml）或本次 canon 节中；entry 只能引用已登记的设定条目")}
		}
		if k.Character != knowledge.Reader {
			e, isChar := canonAfter[k.Character]
			if !isChar || e.Type != canon.TypeCharacter {
				return nil, nil, []workspace.Problem{errorAt(knowledge.CodeUnknownCharacter, "", fmt.Sprintf("knowledge[%d].character", i),
					"获知者 "+k.Character+" 不是设定集中的 character（人物）条目；character 只能取 character 条目 id 或 reader")}
			}
		}
	}
	usedThread := make(map[string]bool, len(snap.threads)+len(in.Plants))
	for id := range snap.threads {
		usedThread[id] = true
	}
	plantIDs := make([]string, len(in.Plants))
	for i, p := range in.Plants {
		at, exists := byRef[p.RecoverAt]
		if !exists || (at.Type != outline.SceneType && at.Type != "chapter") {
			return nil, nil, []workspace.Problem{errorAt(threads.CodeUnknownUnit, "", fmt.Sprintf("plants[%d].recover_at", i),
				"计划回收位置 "+p.RecoverAt+" 不是本作品的章（chapter）或场景（scene）单元")}
		}
		if threads.RecoverBeforePlant(w, u.Ref, p.RecoverAt) {
			return nil, nil, []workspace.Problem{errorAt(threads.CodeRecoverBeforePlant, "", fmt.Sprintf("plants[%d].recover_at", i),
				"计划回收位置 "+p.RecoverAt+" 按单元树先序早于埋设场景 "+u.Ref)}
		}
		id := p.ID
		if id == "" {
			id = nextID("thread", usedThread, len(snap.threads)+i)
		}
		if usedThread[id] {
			return nil, nil, []workspace.Problem{errorAt(catalog.CodeDuplicateID, "", fmt.Sprintf("plants[%d].id", i),
				"伏笔台账或载荷中已有条目 "+id)}
		}
		usedThread[id] = true
		plantIDs[i] = id
	}

	// 按写入后状态对照契约：要求的提交必须齐备，事件必须恰好覆盖。
	changes, contractPlants, contractRecovers, contractEvents := contractStateChanges(ol)
	covered := map[string]bool{}
	for _, sc := range in.StateChanges {
		if sc.Contract != "" {
			covered[sc.Contract] = true
		}
	}
	for _, c := range changes {
		if !covered[c.ID] {
			return nil, nil, []workspace.Problem{errorAt(CodeMissingStateChange, "", "state_changes",
				fmt.Sprintf("契约要求的状态变化 %s（%s）未提交；载荷 state_changes 须含 contract 为 %s 的提交", c.ID, c.Text, c.ID))}
		}
	}
	paid := map[string]threads.Entry{}
	for id, t := range snap.threads {
		paid[id] = t
	}
	for _, id := range in.Recovers {
		t, ok := paid[id]
		if !ok {
			return nil, nil, []workspace.Problem{errorAt(threads.CodeNotFound, "", "recovers",
				"伏笔台账中没有条目 "+id+"；--recovers 只能回收既有伏笔")}
		}
		t.Status = threads.StatusPaid
		ref := u.Ref
		t.RecoveredIn = &ref
		paid[id] = t
	}
	for i, p := range in.Plants {
		paid[plantIDs[i]] = threads.Entry{ID: plantIDs[i], Text: p.Text, PlantedIn: u.Ref, PlannedRecover: p.RecoverAt, Status: threads.StatusOpen}
	}
	for _, id := range contractRecovers {
		t, ok := paid[id]
		if !ok || t.Status != threads.StatusPaid || t.RecoveredIn == nil || *t.RecoveredIn != u.Ref {
			return nil, nil, []workspace.Problem{errorAt(CodeMissingRecovery, "", "recovers",
				"契约要求回收的伏笔 "+id+" 未回收；载荷 recovers 须含 "+id+"（写入后 status 为 paid 且 recovered_in 为本场景）")}
		}
	}
	for _, id := range contractPlants {
		t, ok := paid[id]
		if !ok || t.PlantedIn != u.Ref {
			return nil, nil, []workspace.Problem{errorAt(CodeMissingPlant, "", "plants",
				"契约要埋设的伏笔 "+id+" 的 planted_in 不是本场景；请先 wa-cli threads add 登记（--plant-in "+u.Ref+" --id "+id+"）")}
		}
	}
	declared := declaredEvents(ol)
	payloadEvents := map[string]bool{}
	for _, e := range in.Events {
		payloadEvents[e] = true
	}
	for _, e := range in.Events {
		if !declared[e] {
			return nil, nil, []workspace.Problem{errorAt(CodeUnknownEvent, "", "events",
				"事件 "+e+" 不在该场景大纲声明的 events 中；确认产生的事件只能是大纲声明的事件")}
		}
	}
	for _, e := range contractEvents {
		if !payloadEvents[e] {
			return nil, nil, []workspace.Problem{errorAt(CodeMissingEvent, "", "events",
				"契约声明的事件 "+e+" 未确认产生；载荷 events 须含 "+e)}
		}
	}

	// 全部预检通过：依次写入各台账（复用各包的领域校验与追加语义；canon 节的问题此前已由 canon add 的
	// 参数检查覆盖——它是第一个写入，失败时不留副作用）。
	changed := []string{}
	for i, c := range in.Canon {
		e, ch, ps := canon.Add(root, w, canon.AddInput{Type: c.Type, Name: c.Name, Aliases: c.Aliases, Text: c.Text, Sources: c.Sources, ID: canonIDs[i]})
		if e == nil {
			return nil, nil, ps
		}
		if !slices.Contains(changed, ch[0]) {
			changed = append(changed, ch...)
		}
	}
	stateRefs := make([]StateRef, len(in.StateChanges))
	for i, sc := range in.StateChanges {
		e, ch, ps := state.Propose(root, w, state.ProposeInput{Scene: u.Ref, Entry: sc.Entry, State: sc.State, ID: sc.ID})
		if e == nil {
			return nil, nil, ps
		}
		if !slices.Contains(changed, ch[0]) {
			changed = append(changed, ch...)
		}
		stateRefs[i] = StateRef{ID: e.ID, Contract: sc.Contract}
	}
	knowIDs := make([]string, len(in.Knowledge))
	for i, k := range in.Knowledge {
		e, ch, ps := knowledge.Propose(root, w, knowledge.ProposeInput{Scene: u.Ref, Entry: k.Entry, Character: k.Character, ID: k.ID})
		if e == nil {
			return nil, nil, ps
		}
		if !slices.Contains(changed, ch[0]) {
			changed = append(changed, ch...)
		}
		knowIDs[i] = e.ID
	}
	for i, p := range in.Plants {
		e, ch, ps := threads.Add(root, w, threads.AddInput{Text: p.Text, PlantIn: u.Ref, RecoverAt: p.RecoverAt, ID: plantIDs[i]})
		if e == nil {
			return nil, nil, ps
		}
		if !slices.Contains(changed, ch[0]) {
			changed = append(changed, ch...)
		}
	}
	for _, id := range in.Recovers {
		status, at := threads.StatusPaid, u.Ref
		e, ch, ps := threads.Set(root, w, id, threads.SetInput{Status: &status, RecoveredIn: &at})
		if e == nil {
			return nil, nil, ps
		}
		if len(ch) > 0 && !slices.Contains(changed, ch[0]) {
			changed = append(changed, ch...)
		}
	}

	// 疑似专有名词扫描（warning，不阻塞）：候选为大纲声明的人物与地点及正文扫描的候选，词典为写入后设定集的 name 与别名。
	rec := &fileRecord{SchemaVersion: 1, DraftSHA256: sha, Events: orEmpty(in.Events), Canon: canonIDs,
		State: stateRefs, Knowledge: knowIDs, Plants: plantIDs, Recovers: orEmpty(in.Recovers), Waived: orEmpty(in.Waived)}
	suspected := scan(ol, content, canonAfter, rec.Waived)
	if ps := writeRecord(root, rel, rec); ps != nil {
		return nil, nil, ps
	}
	if !slices.Contains(changed, rel) {
		changed = append(changed, rel)
	}
	var warns []workspace.Problem
	for _, s := range suspected {
		warns = append(warns, workspace.Problem{Code: CodeSuspectedName, Severity: workspace.SeverityWarning,
			Message: fmt.Sprintf("疑似专有名词（%s：%s）不在设定集中；请 wa-cli canon add 补录，或确认非专名后用 --waive %s 豁免", sourceText(s.Source), s.Name, s.Name)})
	}
	return view(u, rel, rec, suspected), changed, warns
}

// scan 返回大纲声明与正文中未解析到设定集 name 或别名、且未豁免的疑似专有名词（按取值去重，保留首个来源）：
// 先是大纲声明的人物、视角人物与地点（完全相等匹配），再是正文扫描的候选（closeout-body-scan，按首次出现顺序）。
func scan(ol *outline.Outline, body []byte, canonAfter map[string]canon.Entry, waived []string) []Suspected {
	names := map[string]bool{}
	var charNames []string
	for _, e := range canonAfter {
		for _, n := range e.Names() {
			names[n] = true
		}
		if e.Type == canon.TypeCharacter && e.Name != nil {
			charNames = append(charNames, *e.Name)
		}
	}
	waivedSet := map[string]bool{}
	for _, v := range waived {
		waivedSet[v] = true
	}
	seen := map[string]bool{}
	out := []Suspected{}
	add := func(v, source string) {
		if v == "" || names[v] || waivedSet[v] || seen[v] {
			return
		}
		seen[v] = true
		out = append(out, Suspected{Name: v, Source: source})
	}
	if ol != nil {
		for _, s := range ol.Sections {
			for _, c := range s.Characters {
				add(c, "characters")
			}
			add(s.Pov, "pov")
			add(s.Location, "location")
		}
	}
	initials := characterInitials(charNames)
	for _, c := range bodyCandidates(string(body)) {
		if !resolvedBody(c, names, initials) {
			add(c.name, SourceDraft)
		}
	}
	return out
}

func sourceText(source string) string {
	switch source {
	case "pov":
		return "视角人物"
	case "location":
		return "地点"
	case SourceDraft:
		return "正文"
	}
	return "出场人物"
}

// mapping 按交替的键值对构造映射节点。
func mapping(kv ...*yaml.Node) *yaml.Node {
	return &yaml.Node{Kind: yaml.MappingNode, Content: kv}
}

// seqOf 构造字符串序列节点（空序列也显式写出）。
func seqOf(vs []string) *yaml.Node {
	n := &yaml.Node{Kind: yaml.SequenceNode, Tag: "!!seq"}
	for _, v := range vs {
		n.Content = append(n.Content, workspace.Str(v))
	}
	return n
}

// writeRecord 整体写出收口产物：构造节点树、编码、重新解析、Schema 校验，通过后原子写入。
func writeRecord(root, rel string, rec *fileRecord) []workspace.Problem {
	stateSeq := &yaml.Node{Kind: yaml.SequenceNode, Tag: "!!seq"}
	for _, r := range rec.State {
		item := mapping(workspace.Str("id"), workspace.Str(r.ID))
		if r.Contract != "" {
			item.Content = append(item.Content, workspace.Str("contract"), workspace.Str(r.Contract))
		}
		stateSeq.Content = append(stateSeq.Content, item)
	}
	doc := &yaml.Node{Kind: yaml.DocumentNode, HeadComment: "写后收口（Close-out）记录；请用 wa-cli scene close-out 写入。", Content: []*yaml.Node{mapping(
		workspace.Str("schema_version"), workspace.Int(1),
		workspace.Str("draft_sha256"), workspace.Str(rec.DraftSHA256),
		workspace.Str("events"), seqOf(rec.Events),
		workspace.Str("canon"), seqOf(rec.Canon),
		workspace.Str("state"), stateSeq,
		workspace.Str("knowledge"), seqOf(rec.Knowledge),
		workspace.Str("plants"), seqOf(rec.Plants),
		workspace.Str("recovers"), seqOf(rec.Recovers),
		workspace.Str("waived"), seqOf(rec.Waived),
	)}}
	data, err := workspace.Encode(doc)
	if err != nil {
		return []workspace.Problem{errorAt(workspace.CodeWriteFailed, rel, "", "生成收口记录失败："+err.Error())}
	}
	written, p := schema.Parse(data)
	if p != nil {
		return workspace.FromSchema(rel, []schema.Problem{*p})
	}
	if ps := schema.Named("closeout").ValidateNode(written); len(ps) > 0 {
		return workspace.FromSchema(rel, ps)
	}
	if err := workspace.WriteFile(root, rel, data); err != nil {
		return []workspace.Problem{errorAt(workspace.CodeWriteFailed, rel, "", "写入收口记录失败："+err.Error())}
	}
	return nil
}

func view(u *work.Unit, rel string, rec *fileRecord, suspected []Suspected) *View {
	suspected = orEmptySuspected(suspected)
	return &View{Unit: u.Ref, Path: rel, DraftSHA256: rec.DraftSHA256, Events: orEmpty(rec.Events), Canon: orEmpty(rec.Canon),
		State: orEmptyRefs(rec.State), Knowledge: orEmpty(rec.Knowledge), Plants: orEmpty(rec.Plants), Recovers: orEmpty(rec.Recovers),
		Waived: orEmpty(rec.Waived), Suspected: suspected}
}

func orEmptyRefs(rs []StateRef) []StateRef {
	if rs == nil {
		return []StateRef{}
	}
	return rs
}

func orEmptySuspected(ss []Suspected) []Suspected {
	if ss == nil {
		return []Suspected{}
	}
	return ss
}

// Waive 把名字并入场景收口记录的 waived 名单（不读写任何台账）：closeout.yaml 不存在时报告 closeout.not-closed；
// 全部名字已在名单中时不写文件（changedPaths 为空数组）。返回更新后的收口视图与变更的路径。
func Waive(root string, w *work.Work, u *work.Unit, names []string) (*View, []string, []workspace.Problem) {
	if u.Type != outline.SceneType {
		return nil, nil, []workspace.Problem{{Code: outline.CodeNotScene, Severity: workspace.SeverityError, Field: "unit",
			Message: "单元 " + u.Ref + " 的类型是 " + u.Type + "；scene close-out 只作用于 scene 类型单元（场景）"}}
	}
	unitDir := w.UnitDir(u)
	rel := Path(unitDir)
	for _, n := range names {
		if strings.TrimSpace(n) == "" {
			return nil, nil, []workspace.Problem{{Code: "input.invalid-value", Severity: workspace.SeverityError, Field: "waive",
				Message: "--waive 的取值不能为空或只含空白"}}
		}
	}
	doc, f, ps := load(root, unitDir)
	if ps != nil {
		return nil, nil, ps
	}
	if doc == nil {
		return nil, nil, []workspace.Problem{errorAt(CodeNotClosed, "", "",
			"场景 "+u.Ref+" 还没有收口记录；先运行 wa-cli scene close-out 提交收口，再豁免疑似专有名词")}
	}
	// 正文扫描按当前正文重算；正文缺失或无法读取时只按大纲扫描。
	body, _, _ := draft.Read(root, w, u)
	// waived 名单在节点树上追加，保留其他内容与注释（workspace-artifacts/structured-edits）。
	m := doc.Content[0]
	var seq *yaml.Node
	for i := 0; i+1 < len(m.Content); i += 2 {
		if m.Content[i].Value == "waived" {
			seq = m.Content[i+1]
		}
	}
	existing := map[string]bool{}
	if seq != nil {
		for _, n := range seq.Content {
			existing[n.Value] = true
		}
	}
	fresh := []string{}
	for _, n := range names {
		if !existing[n] {
			existing[n] = true
			fresh = append(fresh, n)
		}
	}
	if len(fresh) == 0 {
		ol, _ := sceneOutline(root, unitDir)
		snap, _ := loadLedgers(root, w)
		canonAfter := map[string]canon.Entry{}
		if snap != nil {
			canonAfter = snap.canon
		}
		return view(u, rel, f, scan(ol, body, canonAfter, f.Waived)), []string{}, nil
	}
	if seq == nil {
		m.Content = append(m.Content, workspace.Str("waived"), seqOf(nil))
		seq = m.Content[len(m.Content)-1]
	}
	workspace.BlockSequence(m, "waived")
	for _, n := range fresh {
		seq.Content = append(seq.Content, workspace.Str(n))
	}
	f.Waived = append(f.Waived, fresh...)
	// 写前复查：编码、重新解析、Schema 与领域校验。
	data, err := workspace.Encode(doc)
	if err != nil {
		return nil, nil, []workspace.Problem{errorAt(workspace.CodeWriteFailed, rel, "", "生成收口记录失败："+err.Error())}
	}
	written, p := schema.Parse(data)
	if p != nil {
		return nil, nil, workspace.FromSchema(rel, []schema.Problem{*p})
	}
	if ps := schema.Named("closeout").ValidateNode(written); len(ps) > 0 {
		return nil, nil, workspace.FromSchema(rel, ps)
	}
	var checked fileRecord
	catalog.Decode(written, &checked)
	var u2 *work.Unit
	_, byRef := units(w)
	if v, ok := byRef[u.Ref]; ok {
		u2 = v
	}
	snap, _ := loadLedgers(root, w)
	ol, _ := sceneOutline(root, unitDir)
	if ps := checkRecord(rel, &checked, u2, snap, ol); len(ps) > 0 {
		return nil, nil, ps
	}
	if err := workspace.WriteFile(root, rel, data); err != nil {
		return nil, nil, []workspace.Problem{errorAt(workspace.CodeWriteFailed, rel, "", "写入收口记录失败："+err.Error())}
	}
	canonAfter := map[string]canon.Entry{}
	if snap != nil {
		canonAfter = snap.canon
	}
	return view(u, rel, &checked, scan(ol, body, canonAfter, checked.Waived)), []string{rel}, nil
}

// AcceptanceBlockers 计算场景 u 的收口阻塞项（accept 的所有关卡强度共用）：大纲声明了要求收口的契约字段
// （任一节 state_changes、plants、recovers 或 events 非空）或已有 closeout.yaml 时——没有 closeout.yaml 报
// closeout.missing；产物不通过校验报 closeout.invalid（含首个问题）；正文指纹不同报 closeout.stale；记录的
// state 与 knowledge 提议仍有 proposed 报 closeout.pending-proposals；按当前契约与台账复查对照失败复用
// closeout.missing-state-change、closeout.missing-recovery、closeout.missing-plant 与 closeout.missing-event。
// 台账无法加载时跳过依赖台账的复查（其问题由台账校验报告）。
func AcceptanceBlockers(root string, w *work.Work, u *work.Unit) []workspace.Problem {
	if u.Type != outline.SceneType {
		return nil
	}
	unitDir := w.UnitDir(u)
	rel := Path(unitDir)
	ol, _ := sceneOutline(root, unitDir)
	required := requiresCloseout(ol)
	if _, ok, _ := workspace.ReadRegular(root, rel); !ok {
		if info, err := os.Lstat(filepath.Join(root, filepath.FromSlash(rel))); err == nil && !info.Mode().IsRegular() {
			if required {
				return []workspace.Problem{errorAt(CodeInvalid, rel, "",
					"收口记录 "+rel+" 途经符号链接或不是普通文件，按不存在处理；请重新收口")}
			}
			return nil
		}
		if !required {
			return nil
		}
		return []workspace.Problem{errorAt(CodeMissing, unitDir, "",
			"场景 "+u.Ref+" 的契约要求收口（Close-out），但没有 closeout.yaml；场景草拟后请运行 wa-cli scene close-out 提交抽取结果")}
	}
	_, f, ps := load(root, unitDir)
	if ps != nil {
		return []workspace.Problem{errorAt(CodeInvalid, rel, "", "收口记录 "+rel+" 不通过校验（"+ps[0].Code+"："+ps[0].Message+"）；请 --replace 重新收口")}
	}
	var problems []workspace.Problem
	snap, _ := loadLedgers(root, w)
	// 领域校验（closeout-artifact 的引用检查）同样属于「不通过校验」：收口后台账被改动导致的悬空引用按
	// closeout.invalid 阻塞验收（保留其诊断信息）。
	if rps := checkRecord(rel, f, u, snap, ol); len(rps) > 0 {
		problems = append(problems, errorAt(CodeInvalid, rel, "", "收口记录 "+rel+" 不通过校验（"+rps[0].Code+"："+rps[0].Message+"）；请 --replace 重新收口"))
	}
	if content, ok, _ := draft.Read(root, w, u); ok && f.DraftSHA256 != sha256hex(content) {
		problems = append(problems, errorAt(CodeStale, rel, "",
			"收口之后正文已被修改，收口记录不再代表当前正文；请 --replace 重新收口"))
	}
	var pending []string
	if snap != nil {
		for _, r := range f.State {
			if e, ok := snap.states[r.ID]; ok && e.Status == state.StatusProposed {
				pending = append(pending, r.ID)
			}
		}
		for _, id := range f.Knowledge {
			if e, ok := snap.knows[id]; ok && e.Status == knowledge.StatusProposed {
				pending = append(pending, id)
			}
		}
	}
	if len(pending) > 0 {
		problems = append(problems, errorAt(CodePendingProposals, rel, "",
			"收口提交的提议尚未经作者处理："+strings.Join(pending, "、")+"；请逐条 wa-cli state / knowledge confirm 或 reject"))
	}
	changes, contractPlants, contractRecovers, contractEvents := contractStateChanges(ol)
	covered := map[string]bool{}
	for _, r := range f.State {
		if r.Contract != "" {
			covered[r.Contract] = true
		}
	}
	for _, c := range changes {
		if !covered[c.ID] {
			problems = append(problems, errorAt(CodeMissingStateChange, rel, "",
				fmt.Sprintf("契约要求的状态变化 %s（%s）未在收口中提交；请 --replace 重收口或先修正契约", c.ID, c.Text)))
		}
	}
	if snap != nil {
		for _, id := range contractRecovers {
			t, ok := snap.threads[id]
			if !ok || t.Status != threads.StatusPaid || t.RecoveredIn == nil || *t.RecoveredIn != u.Ref {
				problems = append(problems, errorAt(CodeMissingRecovery, rel, "",
					"契约要求回收的伏笔 "+id+" 未在本场景回收；请 --replace 重收口或先修正契约"))
			}
		}
		for _, id := range contractPlants {
			if t, ok := snap.threads[id]; !ok || t.PlantedIn != u.Ref {
				problems = append(problems, errorAt(CodeMissingPlant, rel, "",
					"契约要埋设的伏笔 "+id+" 的 planted_in 不是本场景；请先 wa-cli threads add 登记或修正契约"))
			}
		}
	}
	paidEvents := map[string]bool{}
	for _, e := range f.Events {
		paidEvents[e] = true
	}
	for _, e := range contractEvents {
		if !paidEvents[e] {
			problems = append(problems, errorAt(CodeMissingEvent, rel, "",
				"契约声明的事件 "+e+" 未在收口中确认产生；请 --replace 重收口或先修正契约"))
		}
	}
	return problems
}

// CheckWorks 校验每个可加载作品每个场景单元目录下存在的 closeout.yaml（整体校验）：Schema 与领域检查
// （checkRecord）。closeout.yaml 途经符号链接时报告 artifact.not-found。返回已校验的路径与全部问题。
func CheckWorks(root string, works []*work.Work) (checked []string, problems []workspace.Problem) {
	for _, w := range works {
		snap, _ := loadLedgers(root, w)
		for _, u := range w.Units() {
			if u.Type != outline.SceneType {
				continue
			}
			unitDir := w.UnitDir(u)
			rel := Path(unitDir)
			if _, ok, _ := workspace.ReadRegular(root, rel); !ok {
				if info, err := os.Lstat(filepath.Join(root, filepath.FromSlash(rel))); err == nil && !info.Mode().IsRegular() {
					checked = append(checked, rel)
					problems = append(problems, errorAt(schema.CodeNotFound, rel, "", "closeout.yaml 途经符号链接或不是普通文件，按不存在处理"))
				}
				continue
			}
			checked = append(checked, rel)
			_, f, ps := load(root, unitDir)
			if ps != nil {
				problems = append(problems, ps...)
				continue
			}
			ol, _ := sceneOutline(root, unitDir)
			problems = append(problems, checkRecord(rel, f, u, snap, ol)...)
		}
	}
	return checked, problems
}

// CheckArtifact 对 rel（场景单元目录下的 closeout.yaml）做 Schema 与领域检查，供 wa-cli validate <path>。
// 所在作品或单元无法加载时只做不依赖单元树的检查（其问题由作品校验报告）。
func CheckArtifact(root, rel string) []workspace.Problem {
	unitDir := strings.TrimSuffix(rel, "/"+FileName)
	var w *work.Work
	var u *work.Unit
	if parts := strings.Split(rel, "/"); len(parts) >= 4 {
		w, _ = work.Load(root, parts[2])
	}
	if w != nil {
		_, byRef := units(w)
		if v, ok := byRef[workspace.UnitRef(unitDir)]; ok {
			u = v
		}
	}
	_, f, ps := load(root, unitDir)
	if f == nil {
		return ps
	}
	snap, _ := loadLedgers(root, w)
	ol, _ := sceneOutline(root, unitDir)
	return checkRecord(rel, f, u, snap, ol)
}
