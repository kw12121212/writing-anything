package outline

import (
	"fmt"
	"maps"
	"reflect"
	"slices"
	"strings"

	"github.com/kw12121212/writing-anything/internal/catalog"
	"github.com/kw12121212/writing-anything/internal/schema"
	"github.com/kw12121212/writing-anything/internal/source"
	"github.com/kw12121212/writing-anything/internal/workspace"
	"go.yaml.in/yaml/v3"
)

// sectionOrder 是大纲节中字段的固定顺序。
var sectionOrder = []string{"id", "title", "role", "covers", "uses", "reason", "characters", "beats", "reveal", "targets",
	"pov", "location", "state_changes", "plants", "recovers", "causes", "events"}

// Edit 是一次 outline set 或 scene contract 的修改：按 ID 新增或修改一节。指针为 nil 的字段不修改；取值为空（空字符串、
// 空数组）表示删除该字段。After 或 Before 非空时把该节放到指定节之后或之前。新增时调用方须给出 Title，否则写前复查
// 报告缺少 title。
type Edit struct {
	ID           string
	Title        *string
	Role         *string // 承担的写作法章节或节拍 id；空字符串表示删除
	Covers       *[]string
	Uses         *[]string
	Reason       *string
	Characters   *[]string       // 场景契约：出场人物；空数组表示删除
	Beats        *[]string       // 场景契约：承担的情节节拍 id；空数组表示删除
	Reveal       *string         // 场景契约：揭示边界；空字符串表示删除
	Targets      *map[string]int // 场景契约：AI 评分指标 id → 目标锚定分值；空映射表示删除
	Pov          *string         // 场景契约：视角人物；空字符串表示删除
	Location     *string         // 场景契约：地点；空字符串表示删除
	StateChanges *[]StateChange  // 场景契约：必须发生的状态变化，按 id 新增或更新；空数组表示删除
	Plants       *[]string       // 场景契约：要埋设的伏笔 id；空数组表示删除
	Recovers     *[]string       // 场景契约：要回收的伏笔 id；空数组表示删除
	Causes       *[]string       // 场景契约：起因，引用前文已发生事件的 id；空数组表示删除
	Events       *[]string       // 场景契约：预期产生的事件 id；空数组表示删除
	After, Before string
}

// Candidate 返回把 e 应用到 o（可为 nil）后的大纲取值，只填 Sections，不写文件；供 scene contract 的写前因果链检查
// 以写入后的状态运行。新增节缺少 --title 时返回 nil。
func (e Edit) Candidate(o *Outline) *Outline {
	out := Outline{}
	if o != nil {
		out.Sections = append(out.Sections, o.Sections...)
	}
	at := slices.IndexFunc(out.Sections, func(s Section) bool { return s.ID == e.ID })
	if at < 0 {
		if e.Title == nil || *e.Title == "" {
			return nil
		}
		out.Sections = append(out.Sections, Section{ID: e.ID, Title: *e.Title})
		at = len(out.Sections) - 1
	}
	s := &out.Sections[at]
	if e.Title != nil {
		s.Title = *e.Title
	}
	for _, kv := range []struct {
		v    *string
		dst  *string
	}{
		{e.Reveal, &s.Reveal}, {e.Pov, &s.Pov}, {e.Location, &s.Location},
	} {
		if kv.v != nil {
			*kv.dst = *kv.v
		}
	}
	for _, kv := range []struct {
		list *[]string
		dst  *[]string
	}{
		{e.Characters, &s.Characters}, {e.Beats, &s.Beats}, {e.Plants, &s.Plants},
		{e.Recovers, &s.Recovers}, {e.Causes, &s.Causes}, {e.Events, &s.Events},
	} {
		if kv.list != nil {
			*kv.dst = *kv.list
		}
	}
	if e.StateChanges != nil {
		sc := append([]StateChange(nil), s.StateChanges...)
		for _, item := range *e.StateChanges {
			if j := slices.IndexFunc(sc, func(x StateChange) bool { return x.ID == item.ID }); j >= 0 {
				sc[j] = item
			} else {
				sc = append(sc, item)
			}
		}
		s.StateChanges = sc
	}
	return &out
}

// Set 按 e 修改单元目录 dir 下的 outline.yaml：不存在时创建，已存在时只修改涉及的节，保留其他内容与注释。覆盖缺口不阻止
// 写入。ensure 在写出前确保单元目录存在，返回新建的单元目录。修改后的取值与原有取值相同时不写文件，changed 为空。由参数
// 引起的问题不填路径，字段为参数名（covers[i]、uses[i]、beats[i]、title、reason、reveal、after、before）；出错时不修改文件。
func Set(root, dir string, e Edit, ms Messages, ensure func() ([]string, []workspace.Problem)) (*Outline, []string, []workspace.Problem) {
	if ps := checkEdit(root, dir, e, ms); len(ps) > 0 {
		return nil, nil, ps
	}
	// 已有文件中的引用问题不阻止修改（写作简报删除核心观点后，要能用 wa-cli 修复大纲）；本次参数的引用已由 checkEdit 检查。
	doc, _, ps, _ := load(root, dir, ms)
	if len(ps) > 0 {
		return nil, nil, ps
	}
	rel := Path(dir)
	var before any
	if doc == nil {
		doc = &yaml.Node{Kind: yaml.DocumentNode, HeadComment: "大纲（Outline）；请用 wa-cli outline set、wa-cli outline remove 与 wa-cli scene contract 修改。", Content: []*yaml.Node{{Kind: yaml.MappingNode, Content: []*yaml.Node{
			workspace.Str("schema_version"), workspace.Int(1),
			workspace.Str("sections"), {Kind: yaml.SequenceNode, Tag: "!!seq"},
		}}}}
	} else {
		before = schema.Decode(doc)
	}
	secs, p := sectionsNode(doc, rel)
	if p != nil {
		return nil, nil, []workspace.Problem{*p}
	}
	if workspace.IndexByID(secs, e.ID) < 0 {
		workspace.BlockSequence(doc.Content[0], "sections") // 向流式序列追加节后改为块式，便于阅读
	}
	if p := apply(secs, rel, e); p != nil {
		return nil, nil, []workspace.Problem{*p}
	}
	return save(root, rel, doc, before, ms, ensure)
}

// Remove 删除单元目录 dir 下 outline.yaml 中 id 为 id 的节，保留其他内容与注释。
func Remove(root, dir, id string, ms Messages) (*Outline, []string, []workspace.Problem) {
	doc, _, ps, _ := load(root, dir, ms)
	if len(ps) > 0 {
		return nil, nil, ps
	}
	rel := Path(dir)
	if doc == nil {
		return nil, nil, []workspace.Problem{notFound("", "", id)}
	}
	before := schema.Decode(doc)
	secs, p := sectionsNode(doc, rel)
	if p != nil {
		return nil, nil, []workspace.Problem{*p}
	}
	at := workspace.IndexByID(secs, id)
	switch {
	case at < 0:
		return nil, nil, []workspace.Problem{notFound(rel, "", id)}
	case workspace.HasAnchor(secs.Content[at]):
		return nil, nil, []workspace.Problem{*anchored(rel, fmt.Sprintf("sections[%d]", at))}
	}
	secs.Content = slices.Delete(secs.Content, at, at+1)
	return save(root, rel, doc, before, ms, func() ([]string, []workspace.Problem) { return nil, nil })
}

// save 在写前复查修改后的文档，通过后原子写入；before 为修改前的取值（新建时为 nil），取值未变时不写文件。
func save(root, rel string, doc *yaml.Node, before any, ms Messages, ensure func() ([]string, []workspace.Problem)) (*Outline, []string, []workspace.Problem) {
	data, err := workspace.Encode(doc)
	if err != nil {
		return nil, nil, []workspace.Problem{errorAt(workspace.CodeWriteFailed, rel, "", "生成大纲失败："+err.Error())}
	}
	written, p := schema.Parse(data)
	if p != nil {
		p.Line = 0
		return nil, nil, workspace.FromSchema(rel, []schema.Problem{*p})
	}
	o, ps, _ := parse(root, rel, written, ms)
	if len(ps) > 0 {
		// 原有的 outline.yaml 已通过校验：修改后才出现的问题来自本次参数，不填路径，字段改为参数名。
		for i := range ps {
			ps[i].Path, ps[i].Line, ps[i].Field = "", 0, paramOf(ps[i].Field)
		}
		return nil, nil, ps
	}
	if before != nil && reflect.DeepEqual(before, schema.Decode(written)) {
		return o, nil, nil
	}
	changed, ps := ensure()
	if len(ps) > 0 {
		return nil, nil, ps
	}
	if err := workspace.WriteFile(root, rel, data); err != nil {
		return nil, nil, []workspace.Problem{errorAt(workspace.CodeWriteFailed, rel, "", "写入大纲失败："+err.Error())}
	}
	return o, append(changed, rel), nil
}

// paramOf 把写前复查问题的字段路径（如 sections[2].title）换成引起它的参数名；无法对应到参数时为空。
func paramOf(field string) string {
	_, rest, ok := strings.Cut(field, "].")
	if !ok || !strings.HasPrefix(field, "sections[") {
		return ""
	}
	if name, _, _ := strings.Cut(rest, "["); slices.Contains([]string{"title", "role", "covers", "uses", "reason", "characters", "beats", "reveal", "pov", "location", "plants", "recovers", "causes", "events"}, name) {
		return rest
	}
	if strings.HasPrefix(rest, "targets.") || strings.HasPrefix(rest, "state_changes") {
		return rest
	}
	return ""
}

// checkEdit 检查修改引用的核心观点在写作简报中、素材已登记、承担的节拍在根单元大纲中；给出 covers 而写作简报不通过校验时
// 返回写作简报的问题。根单元大纲缺失或不通过校验时不检查 beats（引用问题照常由 validate 报告）。
func checkEdit(root, dir string, e Edit, ms Messages) []workspace.Problem {
	var out []workspace.Problem
	if e.Role != nil && *e.Role != "" && ms.Method != nil && ms.Method.Parts() != nil && !slices.ContainsFunc(ms.Method.Parts(), func(p catalog.MethodPart) bool { return p.ID == *e.Role }) {
		out = append(out, errorAt(CodeUnknownRole, "", "role", fmt.Sprintf("role %s 不是写作法 %s（%s）的章节或节拍；可用的有 %s", *e.Role, ms.Method.ID, ms.Method.Title, partIDs(ms.Method.Parts()))))
	}
	if e.Covers != nil && len(*e.Covers) > 0 {
		if len(ms.Problems) > 0 {
			return ms.Problems
		}
		for i, id := range *e.Covers {
			if !ms.has(id) {
				out = append(out, errorAt(CodeUnknownMessage, "", fmt.Sprintf("covers[%d]", i), unknownMessage(id)))
			}
		}
	}
	if e.Uses != nil {
		for i, id := range *e.Uses {
			if !source.Registered(root, id) {
				out = append(out, errorAt(CodeUnknownSource, "", fmt.Sprintf("uses[%d]", i), unknownSource(id)))
			}
		}
	}
	if e.Beats != nil && len(*e.Beats) > 0 {
		if ids, ok := beatIDs(root, RootDir(dir)); ok {
			for i, id := range *e.Beats {
				if !slices.Contains(ids, id) {
					out = append(out, errorAt(CodeUnknownBeat, "", fmt.Sprintf("beats[%d]", i), unknownBeat(id)))
				}
			}
		}
	}
	if e.Targets != nil && len(*e.Targets) > 0 && ms.Metrics != nil {
		for _, id := range slices.Sorted(maps.Keys(*e.Targets)) {
			j := slices.IndexFunc(ms.Metrics, func(m catalog.ScoredMetric) bool { return m.ID == id })
			if j < 0 {
				out = append(out, errorAt(CodeUnknownTarget, "", "targets."+id, unknownTarget(id)))
				continue
			}
			if v := (*e.Targets)[id]; v < ms.Metrics[j].Scale.Min || v > ms.Metrics[j].Scale.Max {
				out = append(out, errorAt(CodeTargetOutOfRange, "", "targets."+id,
					fmt.Sprintf("目标分值 %d 不在 AI 评分指标（Metric）%s 的 scale（%d 到 %d）内", v, id, ms.Metrics[j].Scale.Min, ms.Metrics[j].Scale.Max)))
			}
		}
	}
	return out
}

// sectionsNode 返回文档中的 sections 序列节点；根映射或 sections 带锚点、是别名或含合并键时返回 outline.anchored-value。
func sectionsNode(doc *yaml.Node, rel string) (*yaml.Node, *workspace.Problem) {
	m := doc.Content[0]
	if !workspace.Plain(m) || workspace.HasMerge(m) {
		return nil, anchored(rel, "")
	}
	secs, ok := workspace.Lookup(m, "sections")
	if !ok || secs == nil {
		return nil, anchored(rel, "sections")
	}
	return secs, nil
}

// apply 在 sections 序列上新增或修改节 e.ID，并按 After/Before 移动它。
func apply(secs *yaml.Node, rel string, e Edit) *workspace.Problem {
	at := workspace.IndexByID(secs, e.ID)
	var item *yaml.Node
	if at < 0 {
		item = &yaml.Node{Kind: yaml.MappingNode, Tag: "!!map", Content: []*yaml.Node{workspace.Str("id"), workspace.Str(e.ID)}}
		secs.Content = append(secs.Content, item)
		at = len(secs.Content) - 1
	} else {
		item = secs.Content[at]
	}
	field := fmt.Sprintf("sections[%d]", at)
	if !workspace.Plain(item) || workspace.HasMerge(item) {
		return anchored(rel, field)
	}
	for _, kv := range []struct {
		key  string
		v    *string
		list *[]string
	}{
		{"title", e.Title, nil}, {"role", e.Role, nil}, {"covers", nil, e.Covers}, {"uses", nil, e.Uses}, {"reason", e.Reason, nil},
		{"characters", nil, e.Characters}, {"beats", nil, e.Beats}, {"reveal", e.Reveal, nil},
		{"pov", e.Pov, nil}, {"location", e.Location, nil},
		{"plants", nil, e.Plants}, {"recovers", nil, e.Recovers}, {"causes", nil, e.Causes}, {"events", nil, e.Events},
	} {
		var n *yaml.Node
		switch {
		case kv.v != nil:
			if *kv.v != "" {
				n = workspace.Str(*kv.v)
			}
		case kv.list != nil:
			n = workspace.Seq(*kv.list, true)
		default:
			continue
		}
		if f, ok := workspace.SetKey(item, sectionOrder, kv.key, n); !ok {
			return anchored(rel, field+"."+f)
		}
	}
	if e.StateChanges != nil {
		var n *yaml.Node
		if len(*e.StateChanges) > 0 {
			n = &yaml.Node{Kind: yaml.SequenceNode, Tag: "!!seq"}
			for _, sc := range *e.StateChanges {
				item := &yaml.Node{Kind: yaml.MappingNode, Tag: "!!map"}
				for _, kv := range []struct {
					key string
					v   string
				}{{"id", sc.ID}, {"text", sc.Text}} {
					if f, ok := workspace.SetKey(item, []string{"id", "text"}, kv.key, workspace.Str(kv.v)); !ok {
						return anchored(rel, field+".state_changes."+f)
					}
				}
				n.Content = append(n.Content, item)
			}
		}
		if f, ok := workspace.SetKey(item, sectionOrder, "state_changes", n); !ok {
			return anchored(rel, field+"."+f)
		}
	}
	if e.Targets != nil {
		var n *yaml.Node
		if len(*e.Targets) > 0 {
			n = &yaml.Node{Kind: yaml.MappingNode, Tag: "!!map"}
			for _, k := range slices.Sorted(maps.Keys(*e.Targets)) {
				n.Content = append(n.Content, workspace.Str(k), workspace.Int((*e.Targets)[k]))
			}
		}
		if f, ok := workspace.SetKey(item, sectionOrder, "targets", n); !ok {
			return anchored(rel, field+"."+f)
		}
	}
	target := e.After
	if target == "" {
		target = e.Before
	}
	if target == "" {
		return nil
	}
	if workspace.HasAnchor(item) {
		// 移动会改变锚点与别名的先后顺序，别名可能出现在锚点之前。
		return anchored(rel, field)
	}
	secs.Content = slices.Delete(secs.Content, at, at+1)
	to := workspace.IndexByID(secs, target)
	if to < 0 {
		name := "after"
		if e.After == "" {
			name = "before"
		}
		return &workspace.Problem{Code: CodeSectionNotFound, Severity: workspace.SeverityError, Field: name, Message: "大纲中没有节 " + target}
	}
	if e.After != "" {
		to++
	}
	secs.Content = slices.Insert(secs.Content, to, item)
	return nil
}

func notFound(rel, field, id string) workspace.Problem {
	return workspace.Problem{Code: CodeSectionNotFound, Severity: workspace.SeverityError, Path: rel, Field: field, Message: "大纲（Outline）中没有节 " + id}
}

func anchored(rel, field string) *workspace.Problem {
	return &workspace.Problem{Code: CodeAnchoredValue, Severity: workspace.SeverityError, Path: rel, Field: field,
		Message: "该处经由 YAML 锚点、别名或合并键得到，wa-cli 无法只修改这一处；请先去掉锚点、别名与合并键再修改"}
}
