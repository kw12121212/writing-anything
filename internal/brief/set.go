package brief

import (
	"fmt"
	"reflect"
	"regexp"
	"slices"
	"strconv"
	"strings"

	"github.com/kw12121212/writing-anything/internal/catalog"
	"github.com/kw12121212/writing-anything/internal/decision"
	"github.com/kw12121212/writing-anything/internal/schema"
	"github.com/kw12121212/writing-anything/internal/source"
	"github.com/kw12121212/writing-anything/internal/workspace"
	"go.yaml.in/yaml/v3"
)

// Edit 是一次 brief set 的修改。指针或 Set* 为零值的字段不修改；取值为空（空字符串、nil 的 Length、空数组）表示删除该字段。
type Edit struct {
	Audience, Purpose *string
	SetLength         bool
	Length            *Length
	SetAvoid          bool
	Avoid             []string
	SetSources        bool
	Sources           []string
	Method            *string      // 写作法模板 id 或 none；空字符串表示删除（用文体的默认写作法）
	Message           *MessageEdit // 选中的核心观点；nil 表示不修改核心观点
	Remove            []string     // 要删除的核心观点 id，先于 Message 执行
}

// MessageEdit 修改一个核心观点：id 不存在时追加，存在时只修改非 nil 的字段。追加时调用方须给出 Text，
// 否则写前复查报告缺少 text。
type MessageEdit struct {
	ID       string
	Text     *string
	Sources  *[]string // 空数组表示删除 sources
	Decision *string   // 空字符串表示删除 decision
}

// Set 按 e 修改单元目录 dir 下的 brief.yaml：不存在时创建，已存在时只修改涉及的字段，保留其他内容与注释。
// ensure 在写出前确保单元目录存在，返回新建的单元目录。修改后的内容与原文件相同时不写文件，changed 为空。
// 参数引起的问题以参数名作为字段（sources、message、message-sources、message-decision、remove-message）；出错时不修改文件。
func Set(root, dir string, e Edit, q Queue, ensure func() ([]string, []workspace.Problem)) (*Brief, []string, []workspace.Problem) {
	rel := Path(dir)
	if ps := checkEdit(root, workspace.UnitRef(dir), e, q); len(ps) > 0 {
		return nil, nil, ps
	}
	doc, _, ps := load(root, dir, q)
	if len(ps) > 0 {
		return nil, nil, ps
	}
	var before any // 修改前的取值；取值未变时不写文件（workspace-artifacts/structured-edits）
	if doc == nil {
		doc = &yaml.Node{Kind: yaml.DocumentNode, HeadComment: "写作简报（Brief）；请用 wa-cli brief set 修改。", Content: []*yaml.Node{{Kind: yaml.MappingNode, Content: []*yaml.Node{
			workspace.Str("schema_version"), {Kind: yaml.ScalarNode, Tag: "!!int", Value: "1"},
		}}}}
	} else {
		before = schema.Decode(doc)
	}
	if p := apply(doc, rel, e); p != nil {
		return nil, nil, []workspace.Problem{*p}
	}
	data, err := workspace.Encode(doc)
	if err != nil {
		return nil, nil, []workspace.Problem{errorAt(workspace.CodeWriteFailed, rel, "", "生成写作简报失败："+err.Error())}
	}
	// 写前复查：重新解析编码结果，Schema 与领域检查都通过才写出。
	written, p := schema.Parse(data)
	if p != nil {
		p.Line = 0
		return nil, nil, workspace.FromSchema(rel, []schema.Problem{*p})
	}
	b, ps := parse(root, rel, written, q)
	if len(ps) > 0 {
		// 原有的 brief.yaml 已通过校验：修改后才出现的问题来自本次参数，不填路径，字段改为参数名。
		for i := range ps {
			ps[i].Path, ps[i].Line, ps[i].Field = "", 0, paramOf(ps[i].Field)
		}
		return nil, nil, ps
	}
	if before != nil && reflect.DeepEqual(before, schema.Decode(written)) {
		return b, nil, nil
	}
	changed, ps := ensure()
	if len(ps) > 0 {
		return nil, nil, ps
	}
	if err := workspace.WriteFile(root, rel, data); err != nil {
		return nil, nil, []workspace.Problem{errorAt(workspace.CodeWriteFailed, rel, "", "写入写作简报失败："+err.Error())}
	}
	return b, append(changed, rel), nil
}

// paramOf 把写前复查问题的字段路径换成引起它的参数名；无法对应到参数时为空。
func paramOf(field string) string {
	head, rest, _ := strings.Cut(field, ".")
	switch {
	case head == FieldAudience || head == FieldPurpose || head == FieldMethod || strings.HasPrefix(head, FieldAvoid) || strings.HasPrefix(head, FieldSources):
		return head
	case strings.HasPrefix(head, FieldLength):
		return FieldLength
	case strings.HasPrefix(head, FieldKeyMessages+"["):
		switch {
		case rest == "" || rest == "text":
			return "message-text"
		case rest == "id":
			return "message"
		case strings.HasPrefix(rest, "sources"):
			return "message-" + rest
		case rest == "decision":
			return "message-decision"
		}
	}
	return ""
}

// checkEdit 检查修改引用的素材已登记、决策存在且关联单元 ref；这些问题以参数名作为字段、不填路径。决策队列无法加载时
// 不能确认决策，给出 decision.invalid-queue。
func checkEdit(root, ref string, e Edit, q Queue) []workspace.Problem {
	var out []workspace.Problem
	if m := e.Method; m != nil && *m != "" && *m != catalog.MethodNone {
		set := catalog.LoadMethods(root)
		if _, ok := set.Find(*m); !ok {
			out = append(out, UnknownMethod("", FieldMethod, *m, set))
		}
	}
	for i, id := range e.Sources {
		if !source.Registered(root, id) {
			out = append(out, errorAt(CodeUnknownSource, "", fmt.Sprintf("sources[%d]", i), unknownSource(id)))
		}
	}
	if m := e.Message; m != nil {
		if m.Sources != nil {
			for i, id := range *m.Sources {
				if !source.Registered(root, id) {
					out = append(out, errorAt(CodeUnknownSource, "", fmt.Sprintf("message-sources[%d]", i), unknownSource(id)))
				}
			}
		}
		if m.Decision != nil && *m.Decision != "" {
			switch d := q.find(*m.Decision); {
			case q.Invalid:
				out = append(out, errorAt(decision.CodeInvalidQueue, decision.Path, "", "决策队列（Decision Queue）无法加载，无法确认决策 "+*m.Decision+" 是否存在；请先修复 "+decision.Path))
			case d == nil:
				out = append(out, errorAt(CodeUnknownDecision, "", "message-decision", unknownDecision(*m.Decision)))
			case !slices.Contains(d.Units, ref):
				out = append(out, errorAt(CodeUnrelatedDecision, "", "message-decision", unrelatedDecision(*m.Decision, ref)))
			}
		}
	}
	return out
}

// apply 在文档 doc 上执行修改；目标节点或途经的节点带锚点、是别名或所在映射含合并键时返回 brief.anchored-value。
func apply(doc *yaml.Node, rel string, e Edit) *workspace.Problem {
	m := doc.Content[0]
	if !workspace.Plain(m) || workspace.HasMerge(m) {
		return anchored(rel, "")
	}
	for _, kv := range []struct {
		key string
		v   *string
	}{{FieldAudience, e.Audience}, {FieldPurpose, e.Purpose}} {
		if kv.v == nil {
			continue
		}
		var n *yaml.Node
		if *kv.v != "" {
			n = workspace.Str(*kv.v)
		}
		if p := setField(m, rel, kv.key, n); p != nil {
			return p
		}
	}
	if e.Method != nil {
		var n *yaml.Node
		if *e.Method != "" {
			n = workspace.Str(*e.Method)
		}
		if p := setField(m, rel, FieldMethod, n); p != nil {
			return p
		}
	}
	if e.SetLength {
		var n *yaml.Node
		if e.Length != nil {
			n = &yaml.Node{Kind: yaml.MappingNode, Tag: "!!map", Style: yaml.FlowStyle, Content: []*yaml.Node{
				workspace.Str("min"), workspace.Int(e.Length.Min), workspace.Str("max"), workspace.Int(e.Length.Max)}}
		}
		if p := setField(m, rel, FieldLength, n); p != nil {
			return p
		}
	}
	for _, kv := range []struct {
		key string
		set bool
		v   []string
	}{{FieldAvoid, e.SetAvoid, e.Avoid}, {FieldSources, e.SetSources, e.Sources}} {
		if !kv.set {
			continue
		}
		if p := setField(m, rel, kv.key, workspace.Seq(kv.v, kv.key == FieldSources)); p != nil {
			return p
		}
	}
	if len(e.Remove) == 0 && e.Message == nil {
		return nil
	}
	msgs, ok := workspace.Lookup(m, FieldKeyMessages)
	if !ok {
		return anchored(rel, FieldKeyMessages)
	}
	for i, id := range e.Remove {
		at := -1
		if msgs != nil {
			at = workspace.IndexByID(msgs, id)
		}
		if at < 0 {
			return &workspace.Problem{Code: CodeMessageNotFound, Severity: workspace.SeverityError, Field: fmt.Sprintf("remove-message[%d]", i),
				Message: "写作简报中没有核心观点 " + id}
		}
		if workspace.HasAnchor(msgs.Content[at]) {
			return anchored(rel, fmt.Sprintf("key_messages[%d]", at))
		}
		msgs.Content = slices.Delete(msgs.Content, at, at+1)
	}
	if e.Message != nil {
		if msgs == nil {
			msgs = &yaml.Node{Kind: yaml.SequenceNode, Tag: "!!seq"}
			if p := setField(m, rel, FieldKeyMessages, msgs); p != nil {
				return p
			}
		}
		if workspace.IndexByID(msgs, e.Message.ID) < 0 {
			workspace.BlockSequence(m, FieldKeyMessages) // 向流式序列追加观点后改为块式，便于阅读
		}
		if p := editMessage(msgs, rel, *e.Message); p != nil {
			return p
		}
	}
	if msgs != nil && len(msgs.Content) == 0 {
		return setField(m, rel, FieldKeyMessages, nil)
	}
	return nil
}

// editMessage 追加或修改核心观点 me。
func editMessage(msgs *yaml.Node, rel string, me MessageEdit) *workspace.Problem {
	at := workspace.IndexByID(msgs, me.ID)
	var item *yaml.Node
	if at < 0 {
		item = &yaml.Node{Kind: yaml.MappingNode, Tag: "!!map", Content: []*yaml.Node{workspace.Str("id"), workspace.Str(me.ID)}}
		msgs.Content = append(msgs.Content, item)
		at = len(msgs.Content) - 1
	} else {
		item = msgs.Content[at]
	}
	field := fmt.Sprintf("key_messages[%d]", at)
	if !workspace.Plain(item) || workspace.HasMerge(item) {
		return anchored(rel, field)
	}
	order := []string{"id", "text", "sources", "decision"}
	if me.Text != nil {
		if p := setKey(item, rel, field, order, "text", workspace.Str(*me.Text)); p != nil {
			return p
		}
	}
	if me.Sources != nil {
		if p := setKey(item, rel, field, order, "sources", workspace.Seq(*me.Sources, true)); p != nil {
			return p
		}
	}
	if me.Decision != nil {
		var n *yaml.Node
		if *me.Decision != "" {
			n = workspace.Str(*me.Decision)
		}
		if p := setKey(item, rel, field, order, "decision", n); p != nil {
			return p
		}
	}
	return nil
}

// setField 把写作简报顶层字段 key 设为 n（nil 表示删除），新字段按 Fields 的顺序放置。
func setField(m *yaml.Node, rel, key string, n *yaml.Node) *workspace.Problem {
	return setKey(m, rel, "", append(append([]string{"schema_version"}, Fields...), FieldMethod), key, n)
}

// setKey 把映射 m 中的 key 设为 n（nil 表示删除），见 workspace.SetKey；base 是 m 在文档中的字段路径。
func setKey(m *yaml.Node, rel, base string, order []string, key string, n *yaml.Node) *workspace.Problem {
	if f, ok := workspace.SetKey(m, order, key, n); !ok {
		if base != "" {
			f = base + "." + f
		}
		return anchored(rel, f)
	}
	return nil
}

func anchored(rel, field string) *workspace.Problem {
	return &workspace.Problem{Code: CodeAnchoredValue, Severity: workspace.SeverityError, Path: rel, Field: field,
		Message: "该处经由 YAML 锚点、别名或合并键得到，wa-cli 无法只修改这一处；请先去掉锚点、别名与合并键再修改"}
}

// idPattern 与 common.schema.json 中的 id 一致；参数给出的核心观点 id 在修改前按它检查。
var idPattern = regexp.MustCompile(`^[a-z][a-z0-9]*(-[a-z0-9]+)*$`)

// ValidID 报告 id 是否符合 id 格式。
func ValidID(id string) bool { return idPattern.MatchString(id) }

// ParseLength 解析 --length 的取值 <min>-<max>；min 与 max 必须为正整数且 min 不大于 max。
func ParseLength(v string) (*Length, bool) {
	lo, hi, ok := strings.Cut(v, "-")
	if !ok {
		return nil, false
	}
	min, err1 := strconv.Atoi(lo)
	max, err2 := strconv.Atoi(hi)
	if err1 != nil || err2 != nil || min < 1 || max < 1 || min > max || strings.HasPrefix(lo, "+") || strings.HasPrefix(hi, "+") {
		return nil, false
	}
	return &Length{Min: min, Max: max}, true
}
