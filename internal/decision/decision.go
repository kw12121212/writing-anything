// Package decision 管理工作区的决策队列（Decision Queue）.writing-anything/decisions.yaml：记录等待作者决定的
// 事项、作者的决定与确认记录（Confirmation Record），并给出阻塞决策供 internal/work 计算阻塞项。本包不 import
// internal/work，关联单元的存在性由调用方传入的检查函数判断；只返回结构化的问题，由命令层转换为诊断（Diagnostic）。
package decision

import (
	"errors"
	"fmt"
	"io/fs"
	"os"
	"path/filepath"
	"slices"
	"strings"
	"time"

	"github.com/kw12121212/writing-anything/internal/catalog"
	"github.com/kw12121212/writing-anything/internal/schema"
	"github.com/kw12121212/writing-anything/internal/workspace"
	"go.yaml.in/yaml/v3"
)

// 决策队列的错误码（Error Code）。已发布的错误码不得改名或复用。
const (
	CodeNotFound       = "decision.not-found"
	CodeAlreadyDecided = "decision.already-decided"
	CodeUnknownOption  = "decision.unknown-option"
	CodeUnknownSource  = "decision.unknown-source"
	CodeChoiceRequired = "decision.choice-required"
	CodeUnknownUnit    = "decision.unknown-unit"
	CodeBlocking       = "decision.blocking"
	CodeInvalidQueue   = "decision.invalid-queue"
	CodeAnchoredValue  = "decision.anchored-value"
)

// Path 是决策队列相对项目目录的路径。
const Path = workspace.Dir + "/decisions.yaml"

// MaxOptions 是一个决策最多的选项数，选项 id 依次为 a 到 z。
const MaxOptions = 26

// 决策状态。
const (
	StatusOpen    = "open"
	StatusDecided = "decided"
)

// Option 是决策的一个选项。
type Option struct {
	ID   string `json:"id"`
	Text string `json:"text"`
}

// Decision 是决策队列中的一个决策；可选字段未设置时为 nil。
type Decision struct {
	ID             string    `json:"id"`
	Question       string    `json:"question"`
	Options        []Option  `json:"options"`
	Recommendation *string   `json:"recommendation"`
	Rationale      *string   `json:"rationale"`
	Blocking       bool      `json:"blocking"`
	Units          []string  `json:"units"`
	Status         string    `json:"status"`
	Choice         *string   `json:"choice"`
	Answer         *string   `json:"answer"`
	Confirmation   *string   `json:"confirmation"`
	Channel        *string   `json:"channel"` // 确认渠道（Confirmation Channel）；缺失按 cli 解释
	DecidedAt      *string   `json:"decided_at"`
	Conflict       *Conflict `json:"conflict,omitempty"` // 冲突（Conflict）载荷；有它时本决策请作者在两说之间裁定
}

// ConflictSide 是冲突（Conflict）中的一方说法：出自哪份素材与该素材中的引文（用于 lint 的正文匹配）。
type ConflictSide struct {
	Source string `json:"source"`
	Quote  string `json:"quote"`
}

// Conflict 是素材对同一主题的两种矛盾说法的结构化载荷。
type Conflict struct {
	About string       `json:"about"`
	A     ConflictSide `json:"a"`
	B     ConflictSide `json:"b"`
}

func (d *Decision) hasOption(id string) bool {
	return slices.ContainsFunc(d.Options, func(o Option) bool { return o.ID == id })
}

func errorAt(code, field, msg string) workspace.Problem {
	return workspace.Problem{Code: code, Severity: workspace.SeverityError, Path: Path, Field: field, Message: msg}
}

func optionID(i int) string { return string(rune('a' + i)) }

// Load 读取并校验决策队列：Schema 与 check 中的领域检查（不含关联单元）。队列不存在时返回 nil、false 与 nil。
func Load(root string) ([]Decision, bool, []workspace.Problem) {
	doc, ds, ps := load(root)
	return ds, doc != nil || len(ps) > 0, ps
}

func load(root string) (*yaml.Node, []Decision, []workspace.Problem) {
	info, err := os.Lstat(filepath.Join(root, filepath.FromSlash(Path)))
	switch {
	case errors.Is(err, fs.ErrNotExist):
		return nil, nil, nil
	case err == nil && !info.Mode().IsRegular():
		// 与 validate <path> 一致：符号链接按不存在处理，不读取项目目录之外的文件。
		return nil, nil, []workspace.Problem{errorAt(schema.CodeNotFound, "", "决策队列途经符号链接或不是普通文件，按不存在处理")}
	}
	doc, ps := workspace.Read(root, Path)
	if doc == nil {
		return nil, nil, ps
	}
	if ps := schema.Named("decisions").ValidateNode(doc); len(ps) > 0 {
		return nil, nil, workspace.FromSchema(Path, ps)
	}
	var f struct {
		Decisions []Decision `json:"decisions"`
	}
	catalog.Decode(doc, &f)
	if ps := check(f.Decisions); len(ps) > 0 {
		return nil, nil, ps
	}
	return doc, f.Decisions, nil
}

// check 检查已通过 Schema 校验的决策：id 唯一，选项 id 依次为 a、b、c…，推荐与选择是已有选项。
func check(ds []Decision) []workspace.Problem {
	var out []workspace.Problem
	seen := map[string]bool{}
	for i, d := range ds {
		if seen[d.ID] {
			out = append(out, errorAt(catalog.CodeDuplicateID, fmt.Sprintf("decisions[%d].id", i), "决策 id "+d.ID+" 重复"))
		}
		seen[d.ID] = true
		for j, o := range d.Options {
			if o.ID != optionID(j) {
				out = append(out, errorAt(schema.CodeSchemaViolation, fmt.Sprintf("decisions[%d].options[%d].id", i, j),
					fmt.Sprintf("第 %d 个选项的 id 应为 %s，实际为 %s", j+1, optionID(j), o.ID)))
			}
		}
		for _, f := range []struct {
			name string
			v    *string
		}{{"recommendation", d.Recommendation}, {"choice", d.Choice}} {
			if f.v != nil && !d.hasOption(*f.v) {
				out = append(out, errorAt(CodeUnknownOption, fmt.Sprintf("decisions[%d].%s", i, f.name), unknownOption(d, *f.v)))
			}
		}
	}
	return out
}

func unknownOption(d Decision, id string) string {
	var ids []string
	for _, o := range d.Options {
		ids = append(ids, o.ID)
	}
	if len(ids) == 0 {
		return fmt.Sprintf("决策 %s 没有选项，不能选择 %s", d.ID, id)
	}
	return fmt.Sprintf("选项 %s 不存在；决策 %s 的选项为 %s", id, d.ID, strings.Join(ids, "、"))
}

// CheckUnits 检查决策关联的单元引用都能解析；exists 由调用方提供。
func CheckUnits(ds []Decision, exists func(ref string) bool) []workspace.Problem {
	var out []workspace.Problem
	for i, d := range ds {
		for j, ref := range d.Units {
			if !exists(ref) {
				out = append(out, errorAt(CodeUnknownUnit, fmt.Sprintf("decisions[%d].units[%d]", i, j), "决策 "+d.ID+" 关联的单元 "+ref+" 不存在"))
			}
		}
	}
	return out
}

func str(v string) *yaml.Node { return &yaml.Node{Kind: yaml.ScalarNode, Tag: "!!str", Value: v} }

func plain(n *yaml.Node) bool { return n.Anchor == "" && n.Kind != yaml.AliasNode }

// value 返回映射 m 中键 key 的值节点；映射含合并键或节点不能直接修改（带锚点、是别名）时 ok 为 false。
func value(m *yaml.Node, key string) (v *yaml.Node, ok bool) {
	if !plain(m) {
		return nil, false
	}
	for i := 0; i+1 < len(m.Content); i += 2 {
		if m.Content[i].ShortTag() == "!!merge" {
			return nil, false
		}
		if m.Content[i].Value == key {
			v = m.Content[i+1]
		}
	}
	return v, v == nil || plain(v)
}

func anchored(field string) workspace.Problem {
	return errorAt(CodeAnchoredValue, field, "该处经由 YAML 锚点、别名或合并键得到，wa-cli 无法只修改这一处；请先去掉锚点、别名与合并键")
}

// save 编码 doc，重新解析并做 Schema 与领域检查，通过后原子写入。index 为本次修改的决策下标，其 Schema 问题的
// 字段去掉 decisions[index]. 前缀，以参数名表示。
func save(root string, doc *yaml.Node, index int) ([]Decision, []workspace.Problem) {
	data, err := workspace.Encode(doc)
	if err != nil {
		return nil, []workspace.Problem{errorAt(workspace.CodeWriteFailed, "", "生成决策队列失败："+err.Error())}
	}
	written, p := schema.Parse(data)
	if p != nil {
		p.Line = 0
		return nil, workspace.FromSchema(Path, []schema.Problem{*p})
	}
	if ps := schema.Named("decisions").ValidateNode(written); len(ps) > 0 {
		out := workspace.FromSchema(Path, ps)
		prefix := fmt.Sprintf("decisions[%d]", index)
		for i := range out {
			out[i].Line = 0
			if out[i].Field == prefix {
				out[i].Field = ""
			}
			out[i].Field = strings.TrimPrefix(out[i].Field, prefix+".")
		}
		return nil, out
	}
	var f struct {
		Decisions []Decision `json:"decisions"`
	}
	catalog.Decode(written, &f)
	if ps := check(f.Decisions); len(ps) > 0 {
		return nil, ps
	}
	if err := workspace.WriteFile(root, Path, data); err != nil {
		return nil, []workspace.Problem{errorAt(workspace.CodeWriteFailed, "", "写入决策队列失败："+err.Error())}
	}
	return f.Decisions, nil
}

// Input 是新决策的参数。
type Input struct {
	ID        string
	Question  string
	Options   []string // 选项文本，按顺序编号为 a、b、c…
	Recommend string
	Rationale string
	Blocking  bool
	Units     []string
	Conflict  *Conflict // 冲突（Conflict）载荷；给出时两说必须齐备且引文非空白
}

// Add 在决策队列末尾追加一个待决事项。resolve 检查每个关联单元，返回其问题（如 work.not-found）。
func Add(root string, in Input, resolve func(ref string) []workspace.Problem) (*Decision, []workspace.Problem) {
	ds, ps := AddAll(root, []Input{in}, resolve)
	if len(ps) > 0 {
		return nil, ps
	}
	return &ds[0], nil
}

// AddAll 在决策队列末尾依次追加 ins，只写一次决策队列；任一项不合法时都不写。省略 ID 的决策按 NextID 依次编号。
// 返回新追加的决策。
func AddAll(root string, ins []Input, resolve func(ref string) []workspace.Problem) ([]Decision, []workspace.Problem) {
	for _, in := range ins {
		if len(in.Options) > MaxOptions {
			return nil, []workspace.Problem{errorAt(schema.CodeSchemaViolation, "options", fmt.Sprintf("最多允许 %d 个选项，实际为 %d 个", MaxOptions, len(in.Options)))}
		}
		if in.Recommend != "" && !slices.Contains(optionIDs(len(in.Options)), in.Recommend) {
			d := Decision{ID: "新决策"}
			for i := range in.Options {
				d.Options = append(d.Options, Option{ID: optionID(i)})
			}
			return nil, []workspace.Problem{errorAt(CodeUnknownOption, "recommendation", unknownOption(d, in.Recommend))}
		}
		for _, ref := range in.Units {
			if ps := resolve(ref); len(ps) > 0 {
				return nil, ps
			}
		}
		if c := in.Conflict; c != nil {
			if strings.TrimSpace(c.About) == "" || c.A.Source == "" || c.B.Source == "" {
				return nil, []workspace.Problem{errorAt(schema.CodeSchemaViolation, "conflict", "冲突（Conflict）载荷必须成组给出：about 与两说的素材 id、引文都非空")}
			}
			for _, q := range []struct {
				field, text string
			}{{"conflict-a-quote", c.A.Quote}, {"conflict-b-quote", c.B.Quote}} {
				if strings.TrimSpace(q.text) == "" {
					return nil, []workspace.Problem{errorAt(schema.CodeSchemaViolation, q.field, "冲突（Conflict）的引文不能为空：lint 按引文匹配正文，空引文无法检查")}
				}
			}
		}
	}
	doc, existing, ps := load(root)
	if len(ps) > 0 {
		return nil, ps
	}
	if doc == nil {
		doc = &yaml.Node{Kind: yaml.DocumentNode, HeadComment: "决策队列（Decision Queue）；请用 wa-cli decision add 与 wa-cli decide 修改。", Content: []*yaml.Node{{Kind: yaml.MappingNode, Content: []*yaml.Node{
			str("schema_version"), {Kind: yaml.ScalarNode, Tag: "!!int", Value: "1"},
			str("decisions"), {Kind: yaml.SequenceNode, Tag: "!!seq"},
		}}}}
	}
	seq, ok := value(doc.Content[0], "decisions")
	if !ok || seq == nil {
		return nil, []workspace.Problem{anchored("decisions")}
	}
	index := len(seq.Content)
	var items []*yaml.Node
	for _, in := range ins {
		id := in.ID
		if id == "" {
			id = NextID(existing)
		} else if slices.ContainsFunc(existing, func(d Decision) bool { return d.ID == id }) {
			return nil, []workspace.Problem{errorAt(catalog.CodeDuplicateID, "id", "决策队列中已有决策 "+id)}
		}
		existing = append(existing, Decision{ID: id})
		opts := &yaml.Node{Kind: yaml.SequenceNode, Tag: "!!seq"}
		if len(in.Options) == 0 {
			opts.Style = yaml.FlowStyle
		}
		for i, text := range in.Options {
			opts.Content = append(opts.Content, &yaml.Node{Kind: yaml.MappingNode, Style: yaml.FlowStyle, Content: []*yaml.Node{str("id"), str(optionID(i)), str("text"), str(text)}})
		}
		units := &yaml.Node{Kind: yaml.SequenceNode, Tag: "!!seq", Style: yaml.FlowStyle}
		for i, ref := range in.Units {
			if !slices.Contains(in.Units[:i], ref) {
				units.Content = append(units.Content, str(ref))
			}
		}
		item := &yaml.Node{Kind: yaml.MappingNode, Content: []*yaml.Node{str("id"), str(id), str("question"), str(in.Question), str("options"), opts}}
		if in.Recommend != "" {
			item.Content = append(item.Content, str("recommendation"), str(in.Recommend))
		}
		if in.Rationale != "" {
			item.Content = append(item.Content, str("rationale"), str(in.Rationale))
		}
		if in.Conflict != nil {
			side := func(s ConflictSide) *yaml.Node {
				return &yaml.Node{Kind: yaml.MappingNode, Style: yaml.FlowStyle, Content: []*yaml.Node{
					str("source"), str(s.Source), str("quote"), str(s.Quote)}}
			}
			item.Content = append(item.Content, str("conflict"), &yaml.Node{Kind: yaml.MappingNode, Content: []*yaml.Node{
				str("about"), str(in.Conflict.About), str("a"), side(in.Conflict.A), str("b"), side(in.Conflict.B)}})
		}
		item.Content = append(item.Content, str("blocking"), &yaml.Node{Kind: yaml.ScalarNode, Tag: "!!bool", Value: fmt.Sprint(in.Blocking)},
			str("units"), units, str("status"), str(StatusOpen))
		items = append(items, item)
	}
	workspace.BlockSequence(doc.Content[0], "decisions")
	seq.Content = append(seq.Content, items...)
	saved, ps := save(root, doc, index)
	if len(ps) > 0 {
		return nil, ps
	}
	return saved[index:], nil
}

// NextID 返回 decision add 省略 --id 时新决策的 id：dec-<n>，n 从已有决策数加一开始递增到不重复。
func NextID(existing []Decision) string {
	seen := map[string]bool{}
	for _, d := range existing {
		seen[d.ID] = true
	}
	for n := len(existing) + 1; ; n++ {
		if id := fmt.Sprintf("dec-%d", n); !seen[id] {
			return id
		}
	}
}

func optionIDs(n int) []string {
	var out []string
	for i := range n {
		out = append(out, optionID(i))
	}
	return out
}

// Decide 以命令行渠道（cli）记录作者的决定，见 DecideVia。
func Decide(root, id, option, answer, confirmation string, now time.Time) (*Decision, []workspace.Problem) {
	return DecideVia(root, id, option, answer, confirmation, workspace.ChannelCLI, now)
}

// DecideVia 记录作者对决策 id 的决定：option 为选择的选项 id（决策有选项时必填），answer 为答复（没有选项时必填），
// confirmation 为作者确认原话的摘要，channel 为确认渠道（Confirmation Channel），now 为确认时间。
func DecideVia(root, id, option, answer, confirmation, channel string, now time.Time) (*Decision, []workspace.Problem) {
	doc, ds, ps := load(root)
	if len(ps) > 0 {
		return nil, ps
	}
	index := slices.IndexFunc(ds, func(d Decision) bool { return d.ID == id })
	if index < 0 {
		return nil, []workspace.Problem{errorAt(CodeNotFound, "", "决策队列中没有决策 "+id)}
	}
	d := ds[index]
	switch {
	case d.Status == StatusDecided:
		return nil, []workspace.Problem{errorAt(CodeAlreadyDecided, fmt.Sprintf("decisions[%d].status", index), "决策 "+id+" 已经决定，不能重复决定")}
	case len(d.Options) > 0 && option == "":
		return nil, []workspace.Problem{errorAt(CodeChoiceRequired, "", fmt.Sprintf("决策 %s 有选项，请用 --option 给出作者选择的选项（%s）", id, strings.Join(optionIDs(len(d.Options)), "、")))}
	case len(d.Options) == 0 && strings.TrimSpace(answer) == "":
		return nil, []workspace.Problem{errorAt(CodeChoiceRequired, "", "决策 "+id+" 没有选项，请用 --answer 给出作者的答复")}
	case option != "" && !d.hasOption(option):
		return nil, []workspace.Problem{errorAt(CodeUnknownOption, "choice", unknownOption(d, option))}
	case strings.TrimSpace(confirmation) == "":
		return nil, []workspace.Problem{errorAt(schema.CodeSchemaViolation, "confirmation", "缺少必需字段 confirmation：请给出作者确认原话的摘要")}
	}
	seq, ok := value(doc.Content[0], "decisions")
	if !ok || seq == nil || index >= len(seq.Content) {
		return nil, []workspace.Problem{anchored("decisions")}
	}
	item := seq.Content[index]
	field := fmt.Sprintf("decisions[%d]", index)
	set := func(key string, v *yaml.Node) *workspace.Problem {
		cur, ok := value(item, key)
		switch {
		case !ok:
			p := anchored(field + "." + key)
			return &p
		case cur == nil:
			item.Content = append(item.Content, str(key), v)
		default:
			cur.Kind, cur.Tag, cur.Value, cur.Style, cur.Content = v.Kind, v.Tag, v.Value, v.Style, v.Content
		}
		return nil
	}
	updates := [][2]any{{"status", str(StatusDecided)}}
	if option != "" {
		updates = append(updates, [2]any{"choice", str(option)})
	}
	if strings.TrimSpace(answer) != "" {
		updates = append(updates, [2]any{"answer", str(answer)})
	}
	updates = append(updates, [2]any{"confirmation", str(confirmation)}, [2]any{"channel", str(channel)},
		[2]any{"decided_at", str(now.UTC().Format("2006-01-02T15:04:05Z"))})
	for _, u := range updates {
		if p := set(u[0].(string), u[1].(*yaml.Node)); p != nil {
			return nil, []workspace.Problem{*p}
		}
	}
	saved, ps := save(root, doc, index)
	if len(ps) > 0 {
		return nil, ps
	}
	return &saved[index], nil
}

// Replace 替换尚未决定的决策 id 的选项、推荐与理由（recommend 与 rationale 为空时删除），id、问题、关联单元与其他字段不变；
// 用于提问的内容在决定前改变的情况（如营销要素的标题候选改过）。决策不存在时报告 decision.not-found，已决定时报告
// decision.already-decided，目标节点经由锚点、别名或合并键得到时报告 decision.anchored-value；出错时不修改文件。
func Replace(root, id string, options []string, recommend, rationale string) (*Decision, []workspace.Problem) {
	if len(options) > MaxOptions {
		return nil, []workspace.Problem{errorAt(schema.CodeSchemaViolation, "options", fmt.Sprintf("最多允许 %d 个选项，实际为 %d 个", MaxOptions, len(options)))}
	}
	doc, ds, ps := load(root)
	if len(ps) > 0 {
		return nil, ps
	}
	index := slices.IndexFunc(ds, func(d Decision) bool { return d.ID == id })
	switch {
	case index < 0:
		return nil, []workspace.Problem{errorAt(CodeNotFound, "", "决策队列中没有决策 "+id)}
	case ds[index].Status == StatusDecided:
		return nil, []workspace.Problem{errorAt(CodeAlreadyDecided, fmt.Sprintf("decisions[%d].status", index), "决策 "+id+" 已经决定，不能再修改选项")}
	case recommend != "" && !slices.Contains(optionIDs(len(options)), recommend):
		d := Decision{ID: id}
		for i := range options {
			d.Options = append(d.Options, Option{ID: optionID(i)})
		}
		return nil, []workspace.Problem{errorAt(CodeUnknownOption, "recommendation", unknownOption(d, recommend))}
	}
	seq, ok := value(doc.Content[0], "decisions")
	if !ok || seq == nil || index >= len(seq.Content) {
		return nil, []workspace.Problem{anchored("decisions")}
	}
	item := seq.Content[index]
	field := fmt.Sprintf("decisions[%d]", index)
	opts := &yaml.Node{Kind: yaml.SequenceNode, Tag: "!!seq"}
	for i, text := range options {
		opts.Content = append(opts.Content, &yaml.Node{Kind: yaml.MappingNode, Style: yaml.FlowStyle, Content: []*yaml.Node{str("id"), str(optionID(i)), str("text"), str(text)}})
	}
	order := []string{"id", "question", "options", "recommendation", "rationale", "blocking", "units", "status"}
	for _, kv := range []struct {
		key string
		v   *yaml.Node
	}{{"options", opts}, {"recommendation", optionalStr(recommend)}, {"rationale", optionalStr(rationale)}} {
		if _, ok := value(item, kv.key); !ok {
			return nil, []workspace.Problem{anchored(field + "." + kv.key)}
		}
		if f, ok := workspace.SetKey(item, order, kv.key, kv.v); !ok {
			return nil, []workspace.Problem{anchored(field + "." + f)}
		}
	}
	saved, ps := save(root, doc, index)
	if len(ps) > 0 {
		return nil, ps
	}
	return &saved[index], nil
}

func optionalStr(v string) *yaml.Node {
	if v == "" {
		return nil
	}
	return str(v)
}

// Blocking 返回关联单元引用 ref 的未决定阻塞决策；决策队列存在但无法加载时 invalid 为 true。
func Blocking(root, ref string) (ds []Decision, invalid bool) {
	all, _, ps := Load(root)
	return BlockingIn(all, ref), len(ps) > 0
}

// BlockingIn 从已加载的决策 all 中返回关联单元引用 ref 的未决定阻塞决策；逐单元计算时只需加载一次决策队列。
func BlockingIn(all []Decision, ref string) (ds []Decision) {
	for _, d := range all {
		if d.Status == StatusOpen && d.Blocking && slices.Contains(d.Units, ref) {
			ds = append(ds, d)
		}
	}
	return ds
}

// Check 校验决策队列：Schema、领域检查与关联单元。队列不存在时什么也不返回。
func Check(root string, exists func(ref string) bool) (checked []string, problems []workspace.Problem) {
	ds, present, ps := Load(root)
	if !present {
		return nil, nil
	}
	if len(ps) > 0 {
		return []string{Path}, ps
	}
	return []string{Path}, CheckUnits(ds, exists)
}
