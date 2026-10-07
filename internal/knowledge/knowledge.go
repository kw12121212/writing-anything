// Package knowledge 管理作品根单元目录下的知情表（Knowledge State）knowledge.yaml：每个条目是一条知情事件
// （Knowledge Event），记录绑定场景（scene 单元）、被获知的设定集（Canon）条目与获知者（设定集的 character 条目
// 或固定值 reader 表示读者（Reader）），status 为 proposed（待确认）/ confirmed（已确认）/ rejected（已拒绝）。
// 确认与拒绝记录作者确认原话摘要（确认记录），修改或撤销已确认条目须作者重新确认；未确认（proposed）与已拒绝
// （rejected）的条目不参与计算。wa-cli knowledge at <场景> 按单元树先序重放此前已确认的条目，得出该场景开始时
// 各获知者的已知列表：知情只积累，同一设定条目取最早获知的事件，同一输入总是得到同一结果。本包不 import
// internal/cli；只返回结构化的问题，由命令层转换为诊断（Diagnostic）。
package knowledge

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

// 知情表的错误码（Error Code）。已发布的错误码不得改名或复用。
const (
	CodeUnknownUnit            = "knowledge.unknown-unit"
	CodeUnknownEntry           = "knowledge.unknown-entry"
	CodeUnknownCharacter       = "knowledge.unknown-character"
	CodeNotFound               = "knowledge.not-found"
	CodeAlreadyDecided         = "knowledge.already-decided"
	CodeIncompleteConfirmation = "knowledge.incomplete-confirmation"
	CodeAnchoredValue          = "knowledge.anchored-value"
)

// FileName 是知情表文件名；Path 返回作品 id 的台账路径（只在作品根单元目录中识别）。
const FileName = "knowledge.yaml"

// Reader 是获知者的固定取值 reader：表示读者（Reader）；人物获知者取设定集 character 条目的 id。
const Reader = "reader"

func Path(workID string) string { return work.Dir(workID) + "/" + FileName }

// 知情事件的状态。
const (
	StatusProposed  = "proposed"  // 待确认：不参与已知列表计算
	StatusConfirmed = "confirmed" // 已确认：参与重放
	StatusRejected  = "rejected"  // 已拒绝（含撤销）：终态，不参与重放
)

// Statuses 是条目状态在 help 与诊断中的说法。
var Statuses = map[string]string{
	StatusProposed:  "待确认",
	StatusConfirmed: "已确认",
	StatusRejected:  "已拒绝",
}

// orCLI 返回渠道 c；为空时为 cli。
func orCLI(c string) string {
	if c == "" {
		return workspace.ChannelCLI
	}
	return c
}

// timestamp 返回决定时间的统一格式（UTC，RFC 3339）。
func timestamp(now time.Time) string { return now.UTC().Format(time.RFC3339) }

// Event 是知情表中的一条知情事件（Knowledge Event）；confirmation 与 decided_at 未设置时为 nil。
type Event struct {
	ID           string   `json:"id"`
	Scene        string   `json:"scene"`
	Entry        string   `json:"entry"`
	Character    string   `json:"character"`
	Terms        []string `json:"terms"`
	Status       string   `json:"status"`
	Confirmation *string  `json:"confirmation"`
	Channel      *string  `json:"channel"` // 确认渠道（Confirmation Channel）；已决定而缺失时按 cli 解释
	DecidedAt    *string  `json:"decided_at"`
}

// checkTerms 校验揭示词（Reveal Terms）：每项去除首尾空白后非空且不重复；空切片或缺省合法。
func checkTerms(terms []string) []workspace.Problem {
	seen := map[string]bool{}
	for i, term := range terms {
		trimmed := strings.TrimSpace(term)
		if trimmed == "" {
			return []workspace.Problem{errorAt(schema.CodeSchemaViolation, "", fmt.Sprintf("terms[%d]", i),
				"揭示词（terms）去除首尾空白后不能为空；--terms 用逗号分隔多个揭示词，空白项会被忽略")}
		}
		if seen[trimmed] {
			return []workspace.Problem{errorAt(schema.CodeSchemaViolation, "", fmt.Sprintf("terms[%d]", i),
				"揭示词（terms）不能重复："+trimmed)}
		}
		seen[trimmed] = true
	}
	return nil
}

// termsSeq 构造揭示词的 YAML 序列节点；空切片返回 nil（删除该字段，恢复缺省空数组）。
func termsSeq(terms []string) *yaml.Node {
	if len(terms) == 0 {
		return nil
	}
	seq := &yaml.Node{Kind: yaml.SequenceNode}
	for _, term := range terms {
		seq.Content = append(seq.Content, str(term))
	}
	return seq
}

// Item 是已知列表中一个设定条目的获知情况。
type Item struct {
	Entry string  `json:"entry"`
	Name  *string `json:"name"`
	Event string  `json:"event"`
	Scene string  `json:"scene"`
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

// load 读取并校验 rel 处的知情表（Schema）；文件不存在时返回 nil 与 nil；途经符号链接或不是普通文件时按
// 不存在处理并报告 artifact.not-found。领域检查由 checkEvents 负责。w 为 nil 时 scene 引用检查在 checkEvents 中跳过。
func load(root, rel string, w *work.Work) (*yaml.Node, *file, []workspace.Problem) {
	if _, ok, _ := workspace.ReadRegular(root, rel); !ok {
		if info, err := os.Lstat(filepath.Join(root, filepath.FromSlash(rel))); err == nil && !info.Mode().IsRegular() {
			return nil, nil, []workspace.Problem{errorAt(schema.CodeNotFound, rel, "", "knowledge.yaml 途经符号链接或不是普通文件，按不存在处理")}
		}
		return nil, nil, nil
	}
	doc, ps := workspace.Read(root, rel)
	if doc == nil {
		return nil, nil, ps
	}
	if ps := schema.Named("knowledge").ValidateNode(doc); len(ps) > 0 {
		return nil, nil, workspace.FromSchema(rel, ps)
	}
	var f file
	catalog.Decode(doc, &f)
	normalizeTerms(&f)
	return doc, &f, nil
}

// normalizeTerms 把 terms 为 nil 的条目归一为空数组：输出信封的数组字段不允许为 null（cli-contract/shared-envelope）。
func normalizeTerms(f *file) {
	for i := range f.Events {
		if f.Events[i].Terms == nil {
			f.Events[i].Terms = []string{}
		}
	}
}

// canonByID 返回作品设定集（Canon）条目按 id 的索引。canon.yaml 无法加载（不通过 Schema 校验或条目 id 重复）时
// 返回 nil 与其问题（由调用方决定是否阻塞）；不存在或只有引用失效的 warning 时返回索引（空索引意味着所有
// entry 引用都悬空）。
func canonByID(root string, w *work.Work) (map[string]canon.Entry, []workspace.Problem) {
	entries, ps := canon.List(root, w)
	// canon.yaml 不存在时 canon.List 返回 (nil, nil)：条目集合按空处理（所有 entry 与 character 引用都悬空），
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

// isCharacter 报告 id 是否为设定集中 type 为 character 的条目。
func isCharacter(entries map[string]canon.Entry, id string) bool {
	e, ok := entries[id]
	return ok && e.Type == canon.TypeCharacter
}

// checkCharacter 报告获知者取值的问题：不为 reader 且不是设定集 character 条目时 knowledge.unknown-character。
func checkCharacter(entries map[string]canon.Entry, character string) *workspace.Problem {
	if character == Reader || isCharacter(entries, character) {
		return nil
	}
	if _, ok := entries[character]; ok {
		return &workspace.Problem{Code: CodeUnknownCharacter, Severity: workspace.SeverityError, Field: "character",
			Message: "设定条目 " + character + " 不是 character（人物）类型的条目；获知者 --character 只能取 character 条目 id 或 reader"}
	}
	return &workspace.Problem{Code: CodeUnknownCharacter, Severity: workspace.SeverityError, Field: "character",
		Message: "获知者 " + character + " 不在设定集（canon.yaml）的 character 条目中；--character 只能取 character 条目 id 或 reader"}
}

// checkEvents 检查已通过 Schema 校验的知情表：条目 id 唯一；scene 是本作品的 scene 单元；entry 在设定集中；
// character 为 reader 或设定集 character 条目；非 proposed 的条目带非空 confirmation 与 decided_at（取值化字段，
// 键存在性条件不进 Schema，见 workspace-artifacts/value-based-optional-fields）。w 为 nil 时跳过 scene 检查，
// canon 为 nil 时跳过 entry 与 character 检查。悬空引用与产物自身问题都返回，级别由调用方决定。
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
		if ps := checkTerms(e.Terms); len(ps) > 0 {
			for j := range ps {
				ps[j].Path = rel
				ps[j].Field = fmt.Sprintf("events[%d].%s", i, ps[j].Field)
			}
			problems = append(problems, ps...)
		}
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
			if p := checkCharacter(canonEntries, e.Character); p != nil {
				p.Field = fmt.Sprintf("events[%d].character", i)
				problems = append(problems, *p)
			}
		}
		if e.Status != StatusProposed && (e.Confirmation == nil || strings.TrimSpace(*e.Confirmation) == "" || e.DecidedAt == nil || *e.DecidedAt == "") {
			problems = append(problems, errorAt(CodeIncompleteConfirmation, rel, fmt.Sprintf("events[%d]", i),
				"条目 "+e.ID+" 的 status 为 "+e.Status+"，必须带非空的 confirmation（作者确认原话摘要）与 decided_at"))
		}
	}
	return problems
}

// split 把领域检查的问题分为产物自身问题（条目 id 重复、确认记录不完整）与悬空引用（scene、entry 或 character
// 解析不了），不改变级别。
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
			ps[i].Message += "；请用 wa-cli knowledge set 修正该条目的绑定场景"
		case CodeUnknownEntry:
			ps[i].Message += "；请用 wa-cli canon list 核对设定条目 id（条目的 entry 引用需作者修正后重新登记）"
		case CodeUnknownCharacter:
			ps[i].Message += "；请用 wa-cli canon list 核对 character 条目 id（获知者需作者修正后重新登记）"
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

// eventOrder 是知情事件中字段的固定顺序。
var eventOrder = []string{"id", "scene", "entry", "character", "terms", "status", "confirmation", "channel", "decided_at"}

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
// 不完整）阻止写入，悬空引用作为 warnings 返回。canonEntries 为调用方在同一命令中已加载的设定集索引
// （workspace-artifacts/load-once-per-command）；为 nil 时自行加载。
func recheck(root, rel string, doc *yaml.Node, w *work.Work, canonEntries map[string]canon.Entry, paramIndex int) (*file, []byte, []workspace.Problem, []workspace.Problem) {
	data, err := workspace.Encode(doc)
	if err != nil {
		return nil, nil, []workspace.Problem{errorAt(workspace.CodeWriteFailed, rel, "", "生成知情表失败："+err.Error())}, nil
	}
	written, p := schema.Parse(data)
	if p != nil {
		p.Line = 0
		return nil, nil, workspace.FromSchema(rel, []schema.Problem{*p}), nil
	}
	if ps := schema.Named("knowledge").ValidateNode(written); len(ps) > 0 {
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
	normalizeTerms(&f)
	if canonEntries == nil {
		// 设定集无法加载时跳过 entry 与 character 引用检查（其问题由 canon 校验报告）。
		canonEntries, _ = canonByID(root, w)
	}
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

// ProposeInput 是 knowledge propose 的参数。
type ProposeInput struct {
	Scene     string
	Entry     string
	Character string
	ID        string
	Terms     []string
}

// Propose 在作品 w 的知情表末尾追加一条知情事件（初始 status 为 proposed）。scene 必须是本作品的 scene 单元，
// entry 必须在本作品设定集中，character 必须为 reader 或设定集的 character 条目（canon.yaml 无法加载时报告其
// 问题）。knowledge.yaml 不存在时创建，已存在时只追加该项，保留其他内容与 YAML 注释。id 为空时取 know-<n>，
// n 从已有条目数加一起递增到不重复。返回新条目、变更的路径与其他条目悬空引用的 warning；出错时不修改文件。
func Propose(root string, w *work.Work, in ProposeInput) (*Event, []string, []workspace.Problem) {
	for _, p := range slices.Concat(checkText("scene", in.Scene), checkText("entry", in.Entry), checkText("character", in.Character)) {
		return nil, nil, []workspace.Problem{p}
	}
	if in.ID != "" && !brief.ValidID(in.ID) {
		return nil, nil, []workspace.Problem{{Code: schema.CodeSchemaViolation, Severity: workspace.SeverityError, Field: "id",
			Message: "条目 id " + in.ID + " 不合法：应以小写字母开头，由小写字母、数字与连字符组成"}}
	}
	_, byRef := units(w)
	if u, ok := byRef[in.Scene]; !ok || u.Type != outline.SceneType {
		return nil, nil, []workspace.Problem{errorAt(CodeUnknownUnit, "", "scene",
			"绑定场景 " + in.Scene + " 不是作品 " + w.ID + " 的场景（scene）单元；--scene 只能指向 scene 单元")}
	}
	canonEntries, ps := canonByID(root, w)
	if canonEntries == nil {
		return nil, nil, ps
	}
	if _, ok := canonEntries[in.Entry]; !ok {
		return nil, nil, []workspace.Problem{errorAt(CodeUnknownEntry, "", "entry",
			"设定条目 " + in.Entry + " 不在作品 " + w.ID + " 的设定集（canon.yaml）中；--entry 只能引用已登记的设定条目")}
	}
	if p := checkCharacter(canonEntries, in.Character); p != nil {
		return nil, nil, []workspace.Problem{*p}
	}
	rel := Path(w.ID)
	doc, f, ps := load(root, rel, w)
	if len(ps) > 0 {
		return nil, nil, ps
	}
	if doc == nil {
		f = &file{}
		doc = &yaml.Node{Kind: yaml.DocumentNode, HeadComment: "知情表（Knowledge State）；请用 wa-cli knowledge propose 登记知情事件（Knowledge Event），作者经 wa-cli knowledge confirm 确认后生效。", Content: []*yaml.Node{{Kind: yaml.MappingNode, Content: []*yaml.Node{
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
			if id = fmt.Sprintf("know-%d", n); !seen[id] {
				break
			}
		}
	} else if seen[id] {
		// --id 与既有条目重复由本次参数引起：不填 path，next 指向用法而不是复查未写入的文件。
		return nil, nil, []workspace.Problem{errorAt(catalog.CodeDuplicateID, "", "id", "作品 " + w.ID + " 的知情表中已有条目 "+id)}
	}
	events := eventsNode(doc)
	if events == nil {
		return nil, nil, []workspace.Problem{errorAt(CodeAnchoredValue, rel, "events",
			"events 经由 YAML 锚点、别名或合并键得到，wa-cli 无法只追加一项；请先去掉锚点、别名与合并键")}
	}
	if ps := checkTerms(in.Terms); len(ps) > 0 {
		return nil, nil, ps
	}
	itemContent := []*yaml.Node{
		str("id"), str(id), str("scene"), str(in.Scene), str("entry"), str(in.Entry),
		str("character"), str(in.Character)}
	if seq := termsSeq(in.Terms); seq != nil {
		itemContent = append(itemContent, str("terms"), seq)
	}
	itemContent = append(itemContent, str("status"), str(StatusProposed))
	item := &yaml.Node{Kind: yaml.MappingNode, Content: itemContent}
	index := len(events.Content)
	workspace.BlockSequence(doc.Content[0], "events")
	events.Content = append(events.Content, item)
	// 其他条目已有的悬空引用不阻止登记新条目（workspace-artifacts/cross-artifact-references）：本条目的参数在
	// 写入前已逐一校验，recheck 只会再确认一遍；其余条目的问题以 warning 返回。
	saved, data, ps, warns := recheck(root, rel, doc, w, canonEntries, index)
	if len(ps) > 0 {
		return nil, nil, ps
	}
	if err := workspace.WriteFile(root, rel, data); err != nil {
		return nil, nil, []workspace.Problem{errorAt(workspace.CodeWriteFailed, rel, "", "写入知情表失败："+err.Error())}
	}
	e := saved.Events[index]
	return &e, []string{rel}, warns
}

// SetInput 是 knowledge set 的修改参数；指针为 nil 表示不修改。修改已确认条目时 ConfirmedByAuthor 与
// Confirmation 须同时给出（作者重新确认）。
type SetInput struct {
	Scene             *string
	Entry             *string
	Character         *string
	Terms             *[]string
	ConfirmedByAuthor bool
	Confirmation      string
	Channel           string // 重新确认的确认渠道（Confirmation Channel）；空为 cli
}

// Set 修改作品 w 的知情表中 id 的条目，只改动给出的字段，保留其他条目、其他内容与 YAML 注释。proposed 条目
// 照常修改；confirmed 条目必须同时给出 --confirmed-by-author 与非空白的 --confirmation（改动会改变已生效的
// 知情），写入后 confirmation 与 decided_at 更新为本次取值；rejected 为终态，报告 knowledge.already-decided。
// 取值未变时不写文件。返回修改后的条目、变更的路径与其他条目悬空引用的 warning；出错时不修改文件。
func Set(root string, w *work.Work, id string, in SetInput, now time.Time) (*Event, []string, []workspace.Problem) {
	for _, f := range []struct {
		field string
		v     *string
	}{{"scene", in.Scene}, {"entry", in.Entry}, {"character", in.Character}} {
		if f.v != nil {
			if ps := checkText(f.field, *f.v); len(ps) > 0 {
				return nil, nil, ps
			}
		}
	}
	if in.Terms != nil {
		if ps := checkTerms(*in.Terms); len(ps) > 0 {
			return nil, nil, ps
		}
	}
	_, byRef := units(w)
	if in.Scene != nil {
		if u, ok := byRef[*in.Scene]; !ok || u.Type != outline.SceneType {
			return nil, nil, []workspace.Problem{errorAt(CodeUnknownUnit, "", "scene",
				"绑定场景 " + *in.Scene + " 不是作品 " + w.ID + " 的场景（scene）单元；--scene 只能指向 scene 单元")}
		}
	}
	var canonEntries map[string]canon.Entry
	if in.Entry != nil || in.Character != nil {
		var ps []workspace.Problem
		if canonEntries, ps = canonByID(root, w); canonEntries == nil {
			return nil, nil, ps
		}
	}
	if in.Entry != nil {
		if _, ok := canonEntries[*in.Entry]; !ok {
			return nil, nil, []workspace.Problem{errorAt(CodeUnknownEntry, "", "entry",
				"设定条目 " + *in.Entry + " 不在作品 " + w.ID + " 的设定集（canon.yaml）中；--entry 只能引用已登记的设定条目")}
		}
	}
	if in.Character != nil {
		if p := checkCharacter(canonEntries, *in.Character); p != nil {
			return nil, nil, []workspace.Problem{*p}
		}
	}
	rel := Path(w.ID)
	doc, f, ps := load(root, rel, w)
	if len(ps) > 0 {
		return nil, nil, ps
	}
	notFound := []workspace.Problem{errorAt(CodeNotFound, "", "", "作品 "+w.ID+" 的知情表中没有知情事件（Knowledge Event）"+id)}
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
		return nil, nil, []workspace.Problem{errorAt(CodeAlreadyDecided, "", "", "条目 "+id+" 已被作者拒绝（rejected），是终态；重新登记请用 wa-cli knowledge propose 提交新条目")}
	case entry.Status == StatusConfirmed && (!in.ConfirmedByAuthor || strings.TrimSpace(in.Confirmation) == ""):
		return nil, nil, []workspace.Problem{{Code: CodeIncompleteConfirmation, Severity: workspace.SeverityError, Field: "confirmation",
			Message: "修改已确认（confirmed）的条目会改变已生效的知情，必须同时给出 --confirmed-by-author 与非空白的 --confirmation（作者重新确认）"}}
	}

	// 组装写入后的条目取值。
	next := entry
	if in.Scene != nil {
		next.Scene = *in.Scene
	}
	if in.Entry != nil {
		next.Entry = *in.Entry
	}
	if in.Character != nil {
		next.Character = *in.Character
	}
	if in.Terms != nil {
		next.Terms = *in.Terms
	}
	if entry.Status == StatusConfirmed && in.ConfirmedByAuthor {
		next.Confirmation = &in.Confirmation
		channel := orCLI(in.Channel)
		next.Channel = &channel
		stamp := timestamp(now)
		next.DecidedAt = &stamp
	}
	before := schema.Decode(doc)
	if p := editEvent(events, at, rel, next); p != nil {
		return nil, nil, []workspace.Problem{*p}
	}
	// 修改 --entry / --character 时已加载设定集（canonEntries），否则在此加载一次供 recheck 复用。
	if canonEntries == nil {
		canonEntries, _ = canonByID(root, w)
	}
	saved, data, rps, warns := recheck(root, rel, doc, w, canonEntries, at)
	if len(rps) > 0 {
		return nil, nil, rps
	}
	if reflect.DeepEqual(before, schema.Decode(doc)) {
		e := saved.Events[at]
		return &e, nil, warns
	}
	if err := workspace.WriteFile(root, rel, data); err != nil {
		return nil, nil, []workspace.Problem{errorAt(workspace.CodeWriteFailed, rel, "", "写入知情表失败："+err.Error())}
	}
	e := saved.Events[at]
	return &e, []string{rel}, warns
}

// Confirm 以命令行渠道（cli）确认条目，见 ConfirmVia。
func Confirm(root string, w *work.Work, id, confirmation string, now time.Time) (*Event, []string, []workspace.Problem) {
	return ConfirmVia(root, w, id, confirmation, workspace.ChannelCLI, now)
}

// ConfirmVia 把作品 w 知情表中 id 的条目确认为 confirmed 并写入确认记录（confirmation、channel 与 decided_at）。
// 条目必须是 proposed；摘要为空白时报告 knowledge.incomplete-confirmation。返回修改后的条目、变更的路径与其他
// 条目悬空引用的 warning；出错时不修改文件。
func ConfirmVia(root string, w *work.Work, id, confirmation, channel string, now time.Time) (*Event, []string, []workspace.Problem) {
	return decide(root, w, id, confirmation, channel, now, StatusConfirmed,
		"条目 "+id+" 已是 "+StatusConfirmed+"，不能重复确认",
		"作者确认必须给出 --confirmed-by-author 与非空白的 --confirmation（作者原话摘要）")
}

// Reject 以命令行渠道（cli）拒绝或撤销条目，见 RejectVia。
func Reject(root string, w *work.Work, id, confirmation string, now time.Time) (*Event, []string, []workspace.Problem) {
	return RejectVia(root, w, id, confirmation, workspace.ChannelCLI, now)
}

// RejectVia 把作品 w 知情表中 id 的条目改为 rejected 并写入确认记录：条目为 proposed 时即作者拒绝该提议，为
// confirmed 时即作者撤销已生效的知情事件（此后该获知者的已知列表不再含该条目带来的信息）。已是 rejected 时
// 报告 knowledge.already-decided。其余同 ConfirmVia。
func RejectVia(root string, w *work.Work, id, confirmation, channel string, now time.Time) (*Event, []string, []workspace.Problem) {
	return decide(root, w, id, confirmation, channel, now, StatusRejected,
		"条目 "+id+" 已被作者拒绝（rejected），是终态；重新登记请用 wa-cli knowledge propose 提交新条目",
		"作者拒绝或撤销必须给出 --confirmed-by-author 与非空白的 --confirmation（作者原话摘要）")
}

// decide 是 confirm 与 reject 的共同实现：status 为 to（confirmed 或 rejected），拒绝重复决定与终态操作。
func decide(root string, w *work.Work, id, confirmation, channel string, now time.Time, to, already, incomplete string) (*Event, []string, []workspace.Problem) {
	if strings.TrimSpace(confirmation) == "" {
		return nil, nil, []workspace.Problem{{Code: CodeIncompleteConfirmation, Severity: workspace.SeverityError, Field: "confirmation", Message: incomplete}}
	}
	rel := Path(w.ID)
	doc, f, ps := load(root, rel, w)
	if len(ps) > 0 {
		return nil, nil, ps
	}
	notFound := []workspace.Problem{errorAt(CodeNotFound, "", "", "作品 "+w.ID+" 的知情表中没有知情事件（Knowledge Event）"+id)}
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
			"条目 "+id+" 已被作者拒绝（rejected），是终态；重新登记请用 wa-cli knowledge propose 提交新条目")}
	}
	entry.Status = to
	entry.Confirmation = &confirmation
	channel = orCLI(channel)
	entry.Channel = &channel
	stamp := timestamp(now)
	entry.DecidedAt = &stamp
	if p := editEvent(events, at, rel, entry); p != nil {
		return nil, nil, []workspace.Problem{*p}
	}
	saved, data, rps, warns := recheck(root, rel, doc, w, nil, at)
	if len(rps) > 0 {
		return nil, nil, rps
	}
	if err := workspace.WriteFile(root, rel, data); err != nil {
		return nil, nil, []workspace.Problem{errorAt(workspace.CodeWriteFailed, rel, "", "写入知情表失败："+err.Error())}
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
	kvs := []kv{{"scene", str(next.Scene)}, {"entry", str(next.Entry)}, {"character", str(next.Character)},
		{"terms", termsSeq(next.Terms)}, {"status", str(next.Status)}, {"confirmation", node(next.Confirmation)}, {"channel", node(next.Channel)}, {"decided_at", node(next.DecidedAt)}}
	for _, pair := range kvs {
		if f, ok := workspace.SetKey(item, eventOrder, pair.key, pair.n); !ok {
			return &workspace.Problem{Code: CodeAnchoredValue, Severity: workspace.SeverityError, Path: rel, Field: field + "." + f,
				Message: "该处经由 YAML 锚点、别名或合并键得到，wa-cli 无法只修改这一处；请先去掉锚点、别名与合并键"}
		}
	}
	return nil
}

// List 读取作品 w 的知情表并返回条目列表：all 为 false 时只含 proposed（待作者处理）条目；按 scene 的单元树
// 先序排序（先序相同或解析不了时按文件顺序，解析不了的排在最后）。Schema 问题、条目 id 重复或确认记录不完整时
// 不返回条目并报告其问题；条目悬空引用时照常返回，这些问题以 warning 报告。knowledge.yaml 不存在时返回空。
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

// Replay 是单元开始时的知情重放结果：按获知者查询其已知条目（knowledge-at 主规格的语义）。同一
// knowledge.yaml、canon.yaml 与单元树输入总是得到同一结果。
type Replay struct {
	first map[string]map[string]Event // 获知者 → 设定条目 → 最早适用事件
	canon map[string]canon.Entry      // 设定条目 id → 条目（取 name）
}

// Known 返回获知者 character 的已知条目，按 entry 升序，每项含 entry、name、event（最早适用事件的 id）与
// scene（该事件绑定的场景）。character 由调用方校验（reader 或设定集 character 条目）。
func (r *Replay) Known(character string) []Item {
	items := []Item{}
	for entry, e := range r.first[character] {
		var name *string
		if c, ok := r.canon[entry]; ok {
			name = c.Name
		}
		items = append(items, Item{Entry: entry, Name: name, Event: e.ID, Scene: e.Scene})
	}
	slices.SortStableFunc(items, func(a, b Item) int { return strings.Compare(a.Entry, b.Entry) })
	return items
}

// For 计算单元 u（scene 类型）开始时的知情重放：取 knowledge.yaml 中 status 为 confirmed 且 scene 按单元树先序
// 严格早于 u 的条目（同一场景内按文件顺序），每个（获知者， 设定条目）取最早一条——知情只积累，首次获知即已知。
// canonEntries 为调用方在同一命令中已加载的设定集索引（workspace-artifacts/load-once-per-command）；为 nil 时
// 自行加载。knowledge.yaml 不存在时返回空重放；knowledge.yaml 或 canon.yaml 不通过校验（含悬空引用与确认记录
// 不完整）时返回 nil 与问题。
func For(root string, w *work.Work, u *work.Unit, canonEntries map[string]canon.Entry) (*Replay, []workspace.Problem) {
	rel := Path(w.ID)
	_, f, ps := load(root, rel, w)
	if f == nil {
		if len(ps) > 0 {
			return nil, ps
		}
		return &Replay{first: map[string]map[string]Event{}, canon: map[string]canon.Entry{}}, nil
	}
	if canonEntries == nil {
		var cps []workspace.Problem
		if canonEntries, cps = canonByID(root, w); canonEntries == nil {
			return nil, cps
		}
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
	first := map[string]map[string]Event{}
	for _, e := range confirmed {
		if first[e.Character] == nil {
			first[e.Character] = map[string]Event{}
		}
		if _, ok := first[e.Character][e.Entry]; !ok {
			first[e.Character][e.Entry] = e
		}
	}
	return &Replay{first: first, canon: canonEntries}, nil
}

// KnownAt 输出单元 u（scene 类型）开始时获知者 character 的已知条目（knowledge-at 主规格）：character 必须为
// reader 或本作品设定集中的 character 条目，否则报告 knowledge.unknown-character。没有任何适用条目（含没有
// knowledge.yaml）时返回空列表；knowledge.yaml 或 canon.yaml 不通过校验时返回 nil 与问题。
func KnownAt(root string, w *work.Work, u *work.Unit, character string) ([]Item, []workspace.Problem) {
	canonEntries, ps := canonByID(root, w)
	if canonEntries == nil {
		return nil, ps
	}
	if p := checkCharacter(canonEntries, character); p != nil {
		return nil, []workspace.Problem{*p}
	}
	rep, ps := For(root, w, u, canonEntries)
	if ps != nil {
		return nil, ps
	}
	return rep.Known(character), nil
}

// CheckWorks 校验每个可加载作品的知情表（整体校验）：Schema 与领域检查（条目 id 唯一、scene 是 scene 单元、
// entry 在设定集中、character 为 reader 或 character 条目、确认记录完整）。设定集无法加载时跳过 entry 与
// character 引用检查（其问题由设定集校验报告）。knowledge.yaml 途经符号链接时报告 artifact.not-found。返回已
// 校验的路径与全部问题；没有台账的作品不做任何知情检查。
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

// CheckArtifact 对 rel（作品根单元目录下的 knowledge.yaml）做 Schema 与领域检查，供 wa-cli validate <path>。
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
