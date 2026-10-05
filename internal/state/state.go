// Package state 管理作品根单元目录下的状态台账（State Ledger）state.yaml：每个条目是一条状态变更提议
// （State Change Proposal），记录绑定场景（scene 单元）与设定集（Canon）条目的状态变化，status 为 proposed
// （待确认）/ confirmed（已确认）/ rejected（已拒绝）。确认与拒绝记录作者确认原话摘要（确认记录），修改或撤销
// 已确认条目须作者重新确认；未确认（proposed）与已拒绝（rejected）的条目不参与计算。wa-cli state at <场景> 按
// 单元树先序重放此前已确认的条目，得出该场景开始时的状态快照（State Snapshot）：事件溯源（Event Sourcing），
// 同一输入总是得到同一结果。本包不 import internal/cli；只返回结构化的问题，由命令层转换为诊断（Diagnostic）。
package state

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
	"github.com/kw12121212/writing-anything/internal/work"
	"github.com/kw12121212/writing-anything/internal/workspace"
	"go.yaml.in/yaml/v3"
)

// 状态台账的错误码（Error Code）。已发布的错误码不得改名或复用。
const (
	CodeUnknownUnit            = "state.unknown-unit"
	CodeUnknownEntry           = "state.unknown-entry"
	CodeNotFound               = "state.not-found"
	CodeAlreadyDecided         = "state.already-decided"
	CodeIncompleteConfirmation = "state.incomplete-confirmation"
	CodeAnchoredValue          = "state.anchored-value"
)

// FileName 是状态台账文件名；Path 返回作品 id 的台账路径（只在作品根单元目录中识别）。
const FileName = "state.yaml"

func Path(workID string) string { return work.Dir(workID) + "/" + FileName }

// 状态变更提议的状态。
const (
	StatusProposed  = "proposed"  // 待确认：不参与状态快照计算
	StatusConfirmed = "confirmed" // 已确认：参与重放
	StatusRejected  = "rejected"  // 已拒绝（含撤销）：终态，不参与重放
)

// Statuses 是条目状态在 help 与诊断中的说法。
var Statuses = map[string]string{
	StatusProposed:  "待确认",
	StatusConfirmed: "已确认",
	StatusRejected:  "已拒绝",
}

// timestamp 返回决定时间的统一格式（UTC，RFC 3339）；时钟由调用方注入（与 decision、handoff、volume 一致）。
func timestamp(now time.Time) string { return now.UTC().Format(time.RFC3339) }

// Event 是状态台账中的一条状态变更提议（State Change Proposal）；confirmation 与 decided_at 未设置时为 nil。
type Event struct {
	ID           string  `json:"id"`
	Scene        string  `json:"scene"`
	Entry        string  `json:"entry"`
	State        string  `json:"state"`
	Status       string  `json:"status"`
	Confirmation *string `json:"confirmation"`
	DecidedAt    *string `json:"decided_at"`
}

// Item 是状态快照（State Snapshot）中一个设定条目的当前状态。
type Item struct {
	Entry    string  `json:"entry"`
	Name     *string `json:"name"`
	State    string  `json:"state"`
	Proposal string  `json:"proposal"`
	Scene    string  `json:"scene"`
}

type file struct {
	Events []Event `json:"events"`
}

func errorAt(code, path, field, msg string) workspace.Problem {
	return workspace.Problem{Code: code, Severity: workspace.SeverityError, Path: path, Field: field, Message: msg}
}

func str(v string) *yaml.Node { return &yaml.Node{Kind: yaml.ScalarNode, Tag: "!!str", Value: v} }

func plain(n *yaml.Node) bool { return n.Anchor == "" && n.Kind != yaml.AliasNode }

// units 返回作品全部单元的先序位置（单元引用 → 先序下标）与单元引用 → 单元的索引。
func units(w *work.Work) (pos map[string]int, byRef map[string]*work.Unit) {
	us := w.Units()
	pos = make(map[string]int, len(us))
	byRef = make(map[string]*work.Unit, len(us))
	for i, u := range us {
		pos[u.Ref], byRef[u.Ref] = i, u
	}
	return pos, byRef
}

// load 读取并校验 rel 处的状态台账（Schema）；文件不存在时返回 nil 与 nil；途经符号链接或不是普通文件时按
// 不存在处理并报告 artifact.not-found。领域检查由 checkEvents 负责。w 为 nil 时 scene 引用检查在 checkEvents 中跳过。
func load(root, rel string, w *work.Work) (*yaml.Node, *file, []workspace.Problem) {
	if _, ok, _ := workspace.ReadRegular(root, rel); !ok {
		if info, err := os.Lstat(filepath.Join(root, filepath.FromSlash(rel))); err == nil && !info.Mode().IsRegular() {
			return nil, nil, []workspace.Problem{errorAt(schema.CodeNotFound, rel, "", "state.yaml 途经符号链接或不是普通文件，按不存在处理")}
		}
		return nil, nil, nil
	}
	doc, ps := workspace.Read(root, rel)
	if doc == nil {
		return nil, nil, ps
	}
	if ps := schema.Named("state").ValidateNode(doc); len(ps) > 0 {
		return nil, nil, workspace.FromSchema(rel, ps)
	}
	var f file
	catalog.Decode(doc, &f)
	return doc, &f, nil
}

// canonByID 返回作品设定集（Canon）条目按 id 的索引。canon.yaml 无法加载（不通过 Schema 校验或条目 id 重复）时
// 返回 nil 与其问题（由调用方决定是否阻塞）；不存在或只有引用失效的 warning 时返回索引（空索引意味着所有
// entry 引用都悬空）。
func canonByID(root string, w *work.Work) (map[string]canon.Entry, []workspace.Problem) {
	entries, ps := canon.List(root, w)
	// canon.yaml 不存在时 canon.List 返回 (nil, nil)：条目集合按空处理（所有 entry 引用都悬空），
	// 只有确实带问题时才视为无法加载。
	if entries == nil && len(ps) > 0 {
		return nil, ps
	}
	out := make(map[string]canon.Entry, len(entries))
	for _, e := range entries {
		out[e.ID] = e
	}
	return out, nil
}

// checkEvents 检查已通过 Schema 校验的状态台账：条目 id 唯一；scene 是本作品的 scene 单元；entry 在设定集中；
// 非 proposed 的条目带非空 confirmation 与 decided_at（取值化字段，键存在性条件不进 Schema，见
// workspace-artifacts/value-based-optional-fields）。w 为 nil 时跳过 scene 检查，canon 为 nil 时跳过 entry 检查。
// 悬空引用与产物自身问题都返回，级别由调用方决定。
func checkEvents(rel string, f file, w *work.Work, canonEntries map[string]canon.Entry) (problems []workspace.Problem) {
	var byRef map[string]*work.Unit
	if w != nil {
		_, byRef = units(w)
	}
	seen := map[string]bool{}
	for i, e := range f.Events {
		if seen[e.ID] {
			problems = append(problems, errorAt(catalog.CodeDuplicateID, rel, fmt.Sprintf("events[%d].id", i), "条目 id "+e.ID+" 重复"))
		}
		seen[e.ID] = true
		if byRef != nil {
			if u, ok := byRef[e.Scene]; !ok || u.Type != outline.SceneType {
				problems = append(problems, errorAt(CodeUnknownUnit, rel, fmt.Sprintf("events[%d].scene", i),
					"绑定场景 "+e.Scene+" 不是本作品的场景（scene）单元；scene 只能指向 scene 单元"))
			}
		}
		if canonEntries != nil {
			if _, ok := canonEntries[e.Entry]; !ok {
				problems = append(problems, errorAt(CodeUnknownEntry, rel, fmt.Sprintf("events[%d].entry", i),
					"设定条目 "+e.Entry+" 不在本作品设定集（canon.yaml）中；entry 只能引用 wa-cli canon add 登记的条目"))
			}
		}
		if e.Status != StatusProposed && (e.Confirmation == nil || strings.TrimSpace(*e.Confirmation) == "" || e.DecidedAt == nil || *e.DecidedAt == "") {
			problems = append(problems, errorAt(CodeIncompleteConfirmation, rel, fmt.Sprintf("events[%d]", i),
				"条目 "+e.ID+" 的 status 为 "+e.Status+"，必须带非空的 confirmation（作者确认原话摘要）与 decided_at"))
		}
	}
	return problems
}

// split 把领域检查的问题分为产物自身问题（条目 id 重复、确认记录不完整）与悬空引用（scene 或 entry 解析不了），
// 不改变级别。
func split(ps []workspace.Problem) (own, dangling []workspace.Problem) {
	for _, p := range ps {
		if p.Code == catalog.CodeDuplicateID || p.Code == CodeIncompleteConfirmation {
			own = append(own, p)
			continue
		}
		dangling = append(dangling, p)
	}
	return own, dangling
}

// downgrade 把悬空引用降为 warning 并附修复提示（workspace-artifacts/cross-artifact-references）：
// 修改条目时，已有条目的悬空引用不阻止处理其他条目。
func downgrade(ps []workspace.Problem) []workspace.Problem {
	for i := range ps {
		ps[i].Severity = workspace.SeverityWarning
		switch ps[i].Code {
		case CodeUnknownUnit:
			ps[i].Message += "；请用 wa-cli state set 修正该条目的绑定场景"
		case CodeUnknownEntry:
			ps[i].Message += "；请用 wa-cli canon list 核对设定条目 id（条目的 entry 引用需作者修正后重新登记）"
		}
	}
	return ps
}

// eventsNode 返回文档中 events 序列节点；途经锚点、别名或合并键时返回 nil（Schema 保证 events 存在）。
func eventsNode(doc *yaml.Node) *yaml.Node {
	m := doc.Content[0]
	if !plain(m) {
		return nil
	}
	var v *yaml.Node
	for i := 0; i+1 < len(m.Content); i += 2 {
		if m.Content[i].ShortTag() == "!!merge" {
			return nil
		}
		if m.Content[i].Value == "events" {
			v = m.Content[i+1]
		}
	}
	if v == nil || !plain(v) {
		return nil
	}
	return v
}

// eventOrder 是状态变更提议中字段的固定顺序。
var eventOrder = []string{"id", "scene", "entry", "state", "status", "confirmation", "decided_at"}

// checkText 检查写入产物的文本参数是合法的 UTF-8（workspace-artifacts/yaml-text-utf8）。
func checkText(field, v string) []workspace.Problem {
	if !utf8.ValidString(v) {
		return []workspace.Problem{{Code: "input.invalid-value", Severity: workspace.SeverityError, Field: field,
			Message: "参数 " + field + " 不是 UTF-8 文本"}}
	}
	return nil
}

// recheck 对编码前的文档做写前复查：编码、重新解析、Schema 校验与领域检查。paramIndex 不为负时，该条目的
// Schema 与领域问题以参数名表示、不填路径（新增或被本次参数修改）。只有产物自身问题（条目 id 重复、确认记录
// 不完整）阻止写入，悬空引用作为 warnings 返回。
func recheck(root, rel string, doc *yaml.Node, w *work.Work, paramIndex int) (*file, []byte, []workspace.Problem, []workspace.Problem) {
	data, err := workspace.Encode(doc)
	if err != nil {
		return nil, nil, []workspace.Problem{errorAt(workspace.CodeWriteFailed, rel, "", "生成状态台账失败："+err.Error())}, nil
	}
	written, p := schema.Parse(data)
	if p != nil {
		p.Line = 0
		return nil, nil, workspace.FromSchema(rel, []schema.Problem{*p}), nil
	}
	if ps := schema.Named("state").ValidateNode(written); len(ps) > 0 {
		out := workspace.FromSchema(rel, ps)
		if paramIndex >= 0 {
			prefix := fmt.Sprintf("events[%d]", paramIndex)
			for i := range out {
				out[i].Line = 0
				if out[i].Field == prefix || strings.HasPrefix(out[i].Field, prefix+".") {
					out[i].Path = ""
				}
				out[i].Field = strings.TrimPrefix(out[i].Field, prefix+".")
			}
		}
		return nil, nil, out, nil
	}
	var f file
	catalog.Decode(written, &f)
	// 设定集无法加载时跳过 entry 引用检查（其问题由 canon 校验报告）。
	canonEntries, _ := canonByID(root, w)
	own, dangling := split(checkEvents(rel, f, w, canonEntries))
	// 新增或被修改条目的问题以参数名表示、不填路径；其他条目的问题保留产物内路径。
	if paramIndex >= 0 {
		prefix := fmt.Sprintf("events[%d]", paramIndex)
		for i, p := range own {
			if p.Field == prefix || strings.HasPrefix(p.Field, prefix+".") {
				own[i].Path, own[i].Field = "", strings.TrimPrefix(p.Field, prefix+".")
			}
		}
		for i, p := range dangling {
			if p.Field == prefix || strings.HasPrefix(p.Field, prefix+".") {
				dangling[i].Path, dangling[i].Field = "", strings.TrimPrefix(dangling[i].Field, prefix+".")
			}
		}
	}
	if len(own) > 0 {
		return nil, nil, own, nil
	}
	return &f, data, nil, downgrade(dangling)
}

// ProposeInput 是 state propose 的参数。
type ProposeInput struct {
	Scene string
	Entry string
	State string
	ID    string
}

// Propose 在作品 w 的状态台账末尾追加一条状态变更提议（初始 status 为 proposed）。scene 必须是本作品的
// scene 单元，entry 必须在本作品设定集中（canon.yaml 无法加载时报告其问题）。state.yaml 不存在时创建，已存在时
// 只追加该项，保留其他内容与 YAML 注释。id 为空时取 state-<n>，n 从已有条目数加一起递增到不重复。返回新条目、
// 变更的路径与其他条目悬空引用的 warning；出错时不修改文件。
func Propose(root string, w *work.Work, in ProposeInput) (*Event, []string, []workspace.Problem) {
	for _, p := range slices.Concat(checkText("state", in.State), checkText("scene", in.Scene), checkText("entry", in.Entry)) {
		return nil, nil, []workspace.Problem{p}
	}
	if strings.TrimSpace(in.State) == "" {
		return nil, nil, []workspace.Problem{{Code: schema.CodeSchemaViolation, Severity: workspace.SeverityError, Field: "state",
			Message: "状态描述不能为空或只含空白"}}
	}
	if in.ID != "" && !brief.ValidID(in.ID) {
		return nil, nil, []workspace.Problem{{Code: schema.CodeSchemaViolation, Severity: workspace.SeverityError, Field: "id",
			Message: "条目 id " + in.ID + " 不合法：应以小写字母开头，由小写字母、数字与连字符组成"}}
	}
	_, byRef := units(w)
	if u, ok := byRef[in.Scene]; !ok || u.Type != outline.SceneType {
		return nil, nil, []workspace.Problem{errorAt(CodeUnknownUnit, "", "scene",
			"绑定场景 "+in.Scene+" 不是作品 "+w.ID+" 的场景（scene）单元；--scene 只能指向 scene 单元")}
	}
	canonEntries, ps := canonByID(root, w)
	if canonEntries == nil {
		return nil, nil, ps
	}
	if _, ok := canonEntries[in.Entry]; !ok {
		return nil, nil, []workspace.Problem{errorAt(CodeUnknownEntry, "", "entry",
			"设定条目 "+in.Entry+" 不在作品 "+w.ID+" 的设定集（canon.yaml）中；--entry 只能引用已登记的设定条目")}
	}
	rel := Path(w.ID)
	doc, f, ps := load(root, rel, w)
	if len(ps) > 0 {
		return nil, nil, ps
	}
	if doc == nil {
		f = &file{}
		doc = &yaml.Node{Kind: yaml.DocumentNode, HeadComment: "状态台账（State Ledger）；请用 wa-cli state propose 登记状态变更提议，作者经 wa-cli state confirm 确认后生效。", Content: []*yaml.Node{{Kind: yaml.MappingNode, Content: []*yaml.Node{
			str("schema_version"), {Kind: yaml.ScalarNode, Tag: "!!int", Value: "1"},
			str("events"), {Kind: yaml.SequenceNode, Tag: "!!seq"},
		}}}}
	}
	if own, _ := split(checkEvents(rel, *f, w, canonEntries)); len(own) > 0 {
		return nil, nil, own
	}
	seen := map[string]bool{}
	for _, e := range f.Events {
		seen[e.ID] = true
	}
	id := in.ID
	if id == "" {
		for n := len(f.Events) + 1; ; n++ {
			if id = fmt.Sprintf("state-%d", n); !seen[id] {
				break
			}
		}
	} else if seen[id] {
		// --id 与既有条目重复由本次参数引起：不填 path，next 指向用法而不是复查未写入的文件。
		return nil, nil, []workspace.Problem{errorAt(catalog.CodeDuplicateID, "", "id", "作品 "+w.ID+" 的状态台账中已有条目 "+id)}
	}
	events := eventsNode(doc)
	if events == nil {
		return nil, nil, []workspace.Problem{errorAt(CodeAnchoredValue, rel, "events",
			"events 经由 YAML 锚点、别名或合并键得到，wa-cli 无法只追加一项；请先去掉锚点、别名与合并键")}
	}
	item := &yaml.Node{Kind: yaml.MappingNode, Content: []*yaml.Node{
		str("id"), str(id), str("scene"), str(in.Scene), str("entry"), str(in.Entry),
		str("state"), str(in.State), str("status"), str(StatusProposed)}}
	index := len(events.Content)
	workspace.BlockSequence(doc.Content[0], "events")
	events.Content = append(events.Content, item)
	// 其他条目已有的悬空引用不阻止登记新条目（workspace-artifacts/cross-artifact-references）：本条目的参数在
	// 写入前已逐一校验，recheck 只会再确认一遍；其余条目的问题以 warning 返回。
	saved, data, ps, warns := recheck(root, rel, doc, w, index)
	if len(ps) > 0 {
		return nil, nil, ps
	}
	if err := workspace.WriteFile(root, rel, data); err != nil {
		return nil, nil, []workspace.Problem{errorAt(workspace.CodeWriteFailed, rel, "", "写入状态台账失败："+err.Error())}
	}
	e := saved.Events[index]
	return &e, []string{rel}, warns
}

// SetInput 是 state set 的修改参数；指针为 nil 表示不修改。修改已确认条目时 ConfirmedByAuthor 与 Confirmation
// 须同时给出（作者重新确认）。
type SetInput struct {
	Scene             *string
	State             *string
	ConfirmedByAuthor bool
	Confirmation      string
}

// Set 修改作品 w 的状态台账中 id 的条目，只改动给出的字段，保留其他条目、其他内容与 YAML 注释。proposed 条目
// 照常修改；confirmed 条目必须同时给出 --confirmed-by-author 与非空白的 --confirmation（改动会改变已生效的
// 状态），写入后 confirmation 与 decided_at 更新为本次取值；rejected 为终态，报告 state.already-decided。
// 取值未变时不写文件。返回修改后的条目、变更的路径与其他条目悬空引用的 warning；出错时不修改文件。
func Set(root string, w *work.Work, id string, in SetInput, now time.Time) (*Event, []string, []workspace.Problem) {
	if in.State != nil {
		if ps := checkText("state", *in.State); len(ps) > 0 {
			return nil, nil, ps
		}
	}
	if in.Scene != nil {
		if ps := checkText("scene", *in.Scene); len(ps) > 0 {
			return nil, nil, ps
		}
	}
	_, byRef := units(w)
	if in.Scene != nil {
		if u, ok := byRef[*in.Scene]; !ok || u.Type != outline.SceneType {
			return nil, nil, []workspace.Problem{errorAt(CodeUnknownUnit, "", "scene",
				"绑定场景 "+*in.Scene+" 不是作品 "+w.ID+" 的场景（scene）单元；--scene 只能指向 scene 单元")}
		}
	}
	rel := Path(w.ID)
	doc, f, ps := load(root, rel, w)
	if len(ps) > 0 {
		return nil, nil, ps
	}
	notFound := []workspace.Problem{errorAt(CodeNotFound, "", "", "作品 "+w.ID+" 的状态台账中没有状态变更提议（State Change Proposal）"+id)}
	if doc == nil {
		return nil, nil, notFound
	}
	events := eventsNode(doc)
	if events == nil {
		return nil, nil, []workspace.Problem{errorAt(CodeAnchoredValue, rel, "events",
			"events 经由 YAML 锚点、别名或合并键得到，wa-cli 无法只修改一项；请先去掉锚点、别名与合并键")}
	}
	at := workspace.IndexByID(events, id)
	if at < 0 {
		return nil, nil, notFound
	}
	entry := f.Events[at]
	switch {
	case entry.Status == StatusRejected:
		return nil, nil, []workspace.Problem{errorAt(CodeAlreadyDecided, "", "", "条目 "+id+" 已被作者拒绝（rejected），是终态；重新提议请用 wa-cli state propose 登记新条目")}
	case entry.Status == StatusConfirmed && (!in.ConfirmedByAuthor || strings.TrimSpace(in.Confirmation) == ""):
		return nil, nil, []workspace.Problem{{Code: CodeIncompleteConfirmation, Severity: workspace.SeverityError, Field: "confirmation",
			Message: "修改已确认（confirmed）的条目会改变已生效的状态，必须同时给出 --confirmed-by-author 与非空白的 --confirmation（作者重新确认）"}}
	}
	if in.State != nil && strings.TrimSpace(*in.State) == "" {
		return nil, nil, []workspace.Problem{{Code: schema.CodeSchemaViolation, Severity: workspace.SeverityError, Field: "state",
			Message: "状态描述不能为空或只含空白"}}
	}

	// 组装写入后的条目取值。
	next := entry
	if in.Scene != nil {
		next.Scene = *in.Scene
	}
	if in.State != nil {
		next.State = *in.State
	}
	if entry.Status == StatusConfirmed && in.ConfirmedByAuthor {
		next.Confirmation = &in.Confirmation
		stamp := timestamp(now)
		next.DecidedAt = &stamp
	}
	before := schema.Decode(doc)
	if p := editEvent(events, at, rel, next); p != nil {
		return nil, nil, []workspace.Problem{*p}
	}
	saved, data, rps, warns := recheck(root, rel, doc, w, at)
	if len(rps) > 0 {
		return nil, nil, rps
	}
	if reflect.DeepEqual(before, schema.Decode(doc)) {
		e := saved.Events[at]
		return &e, nil, warns
	}
	if err := workspace.WriteFile(root, rel, data); err != nil {
		return nil, nil, []workspace.Problem{errorAt(workspace.CodeWriteFailed, rel, "", "写入状态台账失败："+err.Error())}
	}
	e := saved.Events[at]
	return &e, []string{rel}, warns
}

// Confirm 把作品 w 状态台账中 id 的条目确认为 confirmed 并写入确认记录（confirmation 与 decided_at）。
// 条目必须是 proposed；摘要为空白时报告 state.incomplete-confirmation。返回修改后的条目、变更的路径与其他
// 条目悬空引用的 warning；出错时不修改文件。
func Confirm(root string, w *work.Work, id, confirmation string, now time.Time) (*Event, []string, []workspace.Problem) {
	return decide(root, w, id, confirmation, now, StatusConfirmed,
		"条目 "+id+" 已是 "+StatusConfirmed+"，不能重复确认",
		"作者确认必须给出 --confirmed-by-author 与非空白的 --confirmation（作者原话摘要）")
}

// Reject 把作品 w 状态台账中 id 的条目改为 rejected 并写入确认记录：条目为 proposed 时即作者拒绝该提议，为
// confirmed 时即作者撤销已生效的状态变化。已是 rejected 时报告 state.already-decided。其余同 Confirm。
func Reject(root string, w *work.Work, id, confirmation string, now time.Time) (*Event, []string, []workspace.Problem) {
	return decide(root, w, id, confirmation, now, StatusRejected,
		"条目 "+id+" 已被作者拒绝（rejected），是终态；重新提议请用 wa-cli state propose 登记新条目",
		"作者拒绝或撤销必须给出 --confirmed-by-author 与非空白的 --confirmation（作者原话摘要）")
}

// decide 是 confirm 与 reject 的共同实现：status 为 to（confirmed 或 rejected），拒绝重复决定与终态操作。
func decide(root string, w *work.Work, id, confirmation string, now time.Time, to, already, incomplete string) (*Event, []string, []workspace.Problem) {
	if strings.TrimSpace(confirmation) == "" {
		return nil, nil, []workspace.Problem{{Code: CodeIncompleteConfirmation, Severity: workspace.SeverityError, Field: "confirmation", Message: incomplete}}
	}
	rel := Path(w.ID)
	doc, f, ps := load(root, rel, w)
	if len(ps) > 0 {
		return nil, nil, ps
	}
	notFound := []workspace.Problem{errorAt(CodeNotFound, "", "", "作品 "+w.ID+" 的状态台账中没有状态变更提议（State Change Proposal）"+id)}
	if doc == nil {
		return nil, nil, notFound
	}
	events := eventsNode(doc)
	if events == nil {
		return nil, nil, []workspace.Problem{errorAt(CodeAnchoredValue, rel, "events",
			"events 经由 YAML 锚点、别名或合并键得到，wa-cli 无法只修改一项；请先去掉锚点、别名与合并键")}
	}
	at := workspace.IndexByID(events, id)
	if at < 0 {
		return nil, nil, notFound
	}
	entry := f.Events[at]
	if entry.Status == to {
		return nil, nil, []workspace.Problem{errorAt(CodeAlreadyDecided, "", "", already)}
	}
	if entry.Status == StatusRejected {
		return nil, nil, []workspace.Problem{errorAt(CodeAlreadyDecided, "", "",
			"条目 "+id+" 已被作者拒绝（rejected），是终态；重新提议请用 wa-cli state propose 登记新条目")}
	}
	entry.Status = to
	entry.Confirmation = &confirmation
	stamp := timestamp(now)
	entry.DecidedAt = &stamp
	if p := editEvent(events, at, rel, entry); p != nil {
		return nil, nil, []workspace.Problem{*p}
	}
	saved, data, rps, warns := recheck(root, rel, doc, w, at)
	if len(rps) > 0 {
		return nil, nil, rps
	}
	if err := workspace.WriteFile(root, rel, data); err != nil {
		return nil, nil, []workspace.Problem{errorAt(workspace.CodeWriteFailed, rel, "", "写入状态台账失败："+err.Error())}
	}
	e := saved.Events[at]
	return &e, []string{rel}, warns
}

// editEvent 在节点树上把条目 at 写成 next 的取值（confirmation 与 decided_at 为 nil 时删除该字段）；途经锚点、
// 别名或合并键时返回问题。
func editEvent(events *yaml.Node, at int, rel string, next Event) *workspace.Problem {
	item := events.Content[at]
	field := fmt.Sprintf("events[%d]", at)
	if !plain(item) || workspace.HasMerge(item) {
		return &workspace.Problem{Code: CodeAnchoredValue, Severity: workspace.SeverityError, Path: rel, Field: field,
			Message: "该条目经由 YAML 锚点、别名或合并键得到，wa-cli 无法只修改它；请先去掉锚点、别名与合并键"}
	}
	node := func(v *string) *yaml.Node {
		if v == nil {
			return nil
		}
		return str(*v)
	}
	type kv struct {
		key string
		n   *yaml.Node // nil 表示删除该字段
	}
	kvs := []kv{{"scene", str(next.Scene)}, {"state", str(next.State)}, {"status", str(next.Status)},
		{"confirmation", node(next.Confirmation)}, {"decided_at", node(next.DecidedAt)}}
	for _, pair := range kvs {
		if f, ok := workspace.SetKey(item, eventOrder, pair.key, pair.n); !ok {
			return &workspace.Problem{Code: CodeAnchoredValue, Severity: workspace.SeverityError, Path: rel, Field: field + "." + f,
				Message: "该处经由 YAML 锚点、别名或合并键得到，wa-cli 无法只修改这一处；请先去掉锚点、别名与合并键"}
		}
	}
	return nil
}

// List 读取作品 w 的状态台账并返回条目列表：all 为 false 时只含 proposed（待作者处理）条目；按 scene 的单元树
// 先序排序（先序相同或解析不了时按文件顺序，解析不了的排在最后）。Schema 问题、条目 id 重复或确认记录不完整时
// 不返回条目并报告其问题；条目悬空引用时照常返回，这些问题以 warning 报告。state.yaml 不存在时返回空。
func List(root string, w *work.Work, all bool) ([]Event, []workspace.Problem) {
	rel := Path(w.ID)
	_, f, ps := load(root, rel, w)
	if f == nil {
		return nil, ps
	}
	canonEntries, _ := canonByID(root, w)
	own, dangling := split(checkEvents(rel, *f, w, canonEntries))
	if len(own) > 0 {
		return nil, own
	}
	pos, _ := units(w)
	out := []Event{}
	for _, e := range f.Events {
		if !all && e.Status != StatusProposed {
			continue
		}
		out = append(out, e)
	}
	slices.SortStableFunc(out, func(a, b Event) int {
		pa, oka := pos[a.Scene]
		pb, okb := pos[b.Scene]
		switch {
		case oka && okb:
			return pa - pb
		case oka:
			return -1
		case okb:
			return 1
		}
		return 0
	})
	return out, downgrade(dangling)
}

// Snapshot 计算单元 u（scene 类型）开始时的状态快照（State Snapshot）：重放 state.yaml 中 status 为 confirmed
// 且 scene 按单元树先序严格早于 u 的条目（同一场景内按文件顺序），每个设定条目以最后应用的条目为准，输出按
// entry 升序。proposed 与 rejected 条目、u 自身及其后场景上的条目不参与；同一输入总是得到同一输出。state.yaml
// 不存在或没有任何可应用条目时返回空快照；state.yaml 或 canon.yaml 不通过校验（含悬空引用）时返回 nil 与问题。
func Snapshot(root string, w *work.Work, u *work.Unit) ([]Item, []workspace.Problem) {
	rel := Path(w.ID)
	_, f, ps := load(root, rel, w)
	if f == nil {
		if len(ps) > 0 {
			return nil, ps
		}
		return []Item{}, nil
	}
	canonEntries, cps := canonByID(root, w)
	if canonEntries == nil {
		return nil, cps
	}
	if ps := checkEvents(rel, *f, w, canonEntries); len(ps) > 0 {
		return nil, ps
	}
	pos, _ := units(w)
	confirmed := make([]Event, 0, len(f.Events))
	for _, e := range f.Events {
		if e.Status != StatusConfirmed || pos[e.Scene] >= pos[u.Ref] {
			continue
		}
		confirmed = append(confirmed, e)
	}
	slices.SortStableFunc(confirmed, func(a, b Event) int { return pos[a.Scene] - pos[b.Scene] })
	latest := map[string]Event{}
	for _, e := range confirmed {
		latest[e.Entry] = e
	}
	items := []Item{}
	for entry, e := range latest {
		var name *string
		if c, ok := canonEntries[entry]; ok {
			name = c.Name
		}
		items = append(items, Item{Entry: entry, Name: name, State: e.State, Proposal: e.ID, Scene: e.Scene})
	}
	slices.SortStableFunc(items, func(a, b Item) int { return strings.Compare(a.Entry, b.Entry) })
	return items, nil
}

// CheckWorks 校验每个可加载作品的状态台账（整体校验）：Schema 与领域检查（条目 id 唯一、scene 是 scene 单元、
// entry 在设定集中、确认记录完整）。设定集无法加载时跳过 entry 引用检查（其问题由设定集校验报告）。
// state.yaml 途经符号链接时报告 artifact.not-found。返回已校验的路径与全部问题；没有台账的作品不做任何状态检查。
func CheckWorks(root string, works []*work.Work) (checked []string, problems []workspace.Problem) {
	for _, w := range works {
		rel := Path(w.ID)
		_, f, ps := load(root, rel, w)
		if f == nil {
			if len(ps) > 0 {
				checked = append(checked, rel)
				problems = append(problems, ps...)
			}
			continue
		}
		checked = append(checked, rel)
		canonEntries, _ := canonByID(root, w)
		problems = append(problems, checkEvents(rel, *f, w, canonEntries)...)
	}
	return checked, problems
}

// CheckArtifact 对 rel（作品根单元目录下的 state.yaml）做 Schema 与领域检查，供 wa-cli validate <path>。
// 所在作品无法加载时只做不依赖单元树与设定集的检查（其问题由作品校验报告）。
func CheckArtifact(root, rel string) []workspace.Problem {
	var w *work.Work
	if parts := strings.Split(rel, "/"); len(parts) >= 4 {
		w, _ = work.Load(root, parts[2])
	}
	_, f, ps := load(root, rel, w)
	if f == nil {
		return ps
	}
	var canonEntries map[string]canon.Entry
	if w != nil {
		canonEntries, _ = canonByID(root, w)
	}
	return checkEvents(rel, *f, w, canonEntries)
}
