package marketing

import (
	"fmt"
	"reflect"
	"slices"
	"strings"

	"github.com/kw12121212/writing-anything/internal/decision"
	"github.com/kw12121212/writing-anything/internal/schema"
	"github.com/kw12121212/writing-anything/internal/workspace"
	"go.yaml.in/yaml/v3"
)

// Edit 是一次 marketing set 的修改。指针为 nil 的字段不修改；取值为空字符串表示删除该字段。
type Edit struct {
	Digest, Opening, CTA *string
	Headline             *HeadlineEdit // 选中的标题候选；nil 表示不修改候选
	Remove               []string      // 要删除的标题候选 id，先于 Headline 执行
	headlineDecision     *string       // marketing ask 写入的标题决策 id
}

// HeadlineEdit 修改一个标题候选：id 不存在时追加（须给出 Text，否则写前复查报告缺少 text），存在时修改非 nil 的 Text。
type HeadlineEdit struct {
	ID   string
	Text *string
}

// prepared 是一次修改准备好的结果：data 为要写出的内容，unchanged 为修改后的取值与原有取值相同。
type prepared struct {
	rel       string
	data      []byte
	m         *Marketing
	unchanged bool
}

// prepare 按 e 修改单元目录 dir 下的 marketing.yaml（不存在时从空文档开始），并对编码结果做写前复查；不写文件。
// 参数引起的问题以参数名作为字段、不填路径。
func prepare(root, dir string, e Edit) (*prepared, []workspace.Problem) {
	rel := Path(dir)
	doc, _, ps := load(root, dir)
	if len(ps) > 0 {
		return nil, ps
	}
	var before any // 修改前的取值；取值未变时不写文件（workspace-artifacts/structured-edits）
	if doc == nil {
		doc = &yaml.Node{Kind: yaml.DocumentNode, HeadComment: "营销要素（Marketing Elements）；请用 wa-cli marketing set 与 wa-cli marketing ask 修改。", Content: []*yaml.Node{{Kind: yaml.MappingNode, Content: []*yaml.Node{
			workspace.Str("schema_version"), {Kind: yaml.ScalarNode, Tag: "!!int", Value: "1"},
		}}}}
	} else {
		before = schema.Decode(doc)
	}
	if p := apply(doc, rel, e); p != nil {
		return nil, []workspace.Problem{*p}
	}
	data, err := workspace.Encode(doc)
	if err != nil {
		return nil, []workspace.Problem{errorAt(workspace.CodeWriteFailed, rel, "", "生成营销要素失败："+err.Error())}
	}
	written, p := schema.Parse(data)
	if p != nil {
		p.Line = 0
		return nil, workspace.FromSchema(rel, []schema.Problem{*p})
	}
	m, ps := parse(rel, written)
	if len(ps) > 0 {
		// 原有的 marketing.yaml 已通过校验：修改后才出现的问题来自本次参数，不填路径，字段改为参数名。
		for i := range ps {
			ps[i].Path, ps[i].Line, ps[i].Field = "", 0, paramOf(ps[i].Field)
		}
		return nil, ps
	}
	return &prepared{rel: rel, data: data, m: m, unchanged: before != nil && reflect.DeepEqual(before, schema.Decode(written))}, nil
}

// Set 按 e 修改单元目录 dir 下的 marketing.yaml：不存在时创建，已存在时只修改涉及的字段，保留其他内容与注释。ensure 在写出前
// 确保单元目录存在，返回新建的单元目录。修改后的取值与原有取值相同时不写文件，changed 为空。出错时不修改文件。
func Set(root, dir string, e Edit, ensure func() ([]string, []workspace.Problem)) (*Marketing, []string, []workspace.Problem) {
	pr, ps := prepare(root, dir, e)
	if len(ps) > 0 {
		return nil, nil, ps
	}
	if pr.unchanged {
		return pr.m, nil, nil
	}
	changed, ps := ensure()
	if len(ps) > 0 {
		return nil, nil, ps
	}
	if err := workspace.WriteFile(root, pr.rel, pr.data); err != nil {
		return nil, nil, []workspace.Problem{errorAt(workspace.CodeWriteFailed, pr.rel, "", "写入营销要素失败："+err.Error())}
	}
	return pr.m, append(changed, pr.rel), nil
}

// Ask 把单元目录 dir 下的标题候选登记为决策队列中关联单元 ref 的阻塞决策，并把决策 id 写入 marketing.yaml。recommend 为推荐的
// 候选 id（可为空）。所有检查都在写入之前完成：营销要素合法且至少有两个候选、决策队列可加载、没有仍在等待且未过期的标题决策、
// recommend 是候选 id；先追加决策，再写 marketing.yaml（cli-contract/precheck-before-side-effects）。已有的标题决策尚未决定
// 但已过期（候选改过）时，原地替换它的选项、推荐与理由，不另建决策，使旧的决策不会继续阻塞单元。changed 在 marketing.yaml
// 写入失败时仍列出已追加决策的决策队列。
func Ask(root, dir, ref, recommend, rationale string) (*decision.Decision, *Marketing, []string, []workspace.Problem) {
	rel := Path(dir)
	m, ps := Load(root, dir)
	switch {
	case len(ps) > 0:
		return nil, nil, nil, ps
	case m == nil:
		return nil, nil, nil, []workspace.Problem{errorAt(schema.CodeNotFound, rel, "", "单元 "+ref+" 还没有营销要素 marketing.yaml；请先用 wa-cli marketing set --headline 写入标题候选")}
	case len(m.Headlines) < MinHeadlines:
		return nil, nil, nil, []workspace.Problem{errorAt(CodeMissingField, rel, FieldHeadlines,
			fmt.Sprintf("标题决策至少需要 %d 个标题候选（Headline Candidates），现在有 %d 个；请先用 wa-cli marketing set --headline 写入", MinHeadlines, len(m.Headlines)))}
	case len(m.Headlines) > decision.MaxOptions:
		return nil, nil, nil, []workspace.Problem{errorAt(schema.CodeSchemaViolation, rel, FieldHeadlines,
			fmt.Sprintf("标题决策最多 %d 个选项，现在有 %d 个标题候选；请先删掉一些", decision.MaxOptions, len(m.Headlines)))}
	}
	ds, _, ps := decision.Load(root)
	if len(ps) > 0 {
		return nil, nil, nil, ps
	}
	q := Queue{Decisions: ds}
	state, _ := m.HeadlineState(q)
	if state == StateOpen {
		return nil, nil, nil, []workspace.Problem{errorAt(CodeAlreadyAsked, rel, FieldHeadlineDecision,
			"标题决策 "+m.HeadlineDecision+" 还在等待作者决定，候选也没有改过；请作者用 wa-cli decide 选定，不需要重新提问")}
	}
	open := state == StateStale && slices.ContainsFunc(ds, func(d decision.Decision) bool {
		return d.ID == m.HeadlineDecision && d.Status == decision.StatusOpen
	})
	var options []string
	letter := ""
	for i, h := range m.Headlines {
		options = append(options, h.Text)
		if h.ID == recommend {
			letter = string(rune('a' + i))
		}
	}
	if recommend != "" && letter == "" {
		return nil, nil, nil, []workspace.Problem{errorAt(CodeHeadlineNotFound, "", "recommend", "推荐的标题候选 "+recommend+" 不存在；可选的候选为 "+ids(m.Headlines))}
	}
	if open {
		d, ps := decision.Replace(root, m.HeadlineDecision, options, letter, rationale)
		if len(ps) > 0 {
			return nil, nil, nil, ps
		}
		return d, m, []string{decision.Path}, nil
	}
	id := decision.NextID(ds)
	pr, ps := prepare(root, dir, Edit{headlineDecision: &id})
	if len(ps) > 0 {
		return nil, nil, nil, ps
	}
	d, ps := decision.Add(root, decision.Input{ID: id, Question: "单元 " + ref + " 用哪个标题？", Options: options, Recommend: letter,
		Rationale: rationale, Blocking: true, Units: []string{ref}}, func(string) []workspace.Problem { return nil })
	if len(ps) > 0 {
		return nil, nil, nil, ps
	}
	if err := workspace.WriteFile(root, rel, pr.data); err != nil {
		// 决策已经追加：如实报告。marketing.yaml 没有记下它，重新提问会另建决策，所以要先处理这个决策。
		return nil, nil, []string{decision.Path}, []workspace.Problem{errorAt(workspace.CodeWriteFailed, rel, "",
			"已在决策队列中追加阻塞决策 "+id+"，但写入营销要素失败："+err.Error()+"；marketing.yaml 没有记下这个决策，修复写入问题后重新运行 wa-cli marketing ask 会另建决策，"+
				"请先请作者用 wa-cli decide "+id+" 处理它（它会一直阻塞单元 "+ref+"）")}
	}
	return d, pr.m, []string{decision.Path, rel}, nil
}

func ids(hs []Headline) string {
	var out []string
	for _, h := range hs {
		out = append(out, h.ID)
	}
	return strings.Join(out, "、")
}

// paramOf 把写前复查问题的字段路径换成引起它的参数名；无法对应到参数时为空。
func paramOf(field string) string {
	head, rest, _ := strings.Cut(field, ".")
	switch {
	case head == FieldDigest || head == FieldOpening || head == FieldCTA:
		return head
	case strings.HasPrefix(head, FieldHeadlines+"["):
		if rest == "id" {
			return "headline"
		}
		return "headline-text"
	}
	return ""
}

// apply 在文档 doc 上执行修改；目标节点或途经的节点带锚点、是别名或所在映射含合并键时返回 marketing.anchored-value。
func apply(doc *yaml.Node, rel string, e Edit) *workspace.Problem {
	m := doc.Content[0]
	if !workspace.Plain(m) || workspace.HasMerge(m) {
		return anchored(rel, "")
	}
	for _, kv := range []struct {
		key string
		v   *string
	}{{FieldDigest, e.Digest}, {FieldOpening, e.Opening}, {FieldCTA, e.CTA}, {FieldHeadlineDecision, e.headlineDecision}} {
		if kv.v == nil {
			continue
		}
		var n *yaml.Node
		if *kv.v != "" {
			n = workspace.Str(*kv.v)
		}
		if p := setKey(m, rel, "", order, kv.key, n); p != nil {
			return p
		}
	}
	if len(e.Remove) == 0 && e.Headline == nil {
		return nil
	}
	hs, ok := workspace.Lookup(m, FieldHeadlines)
	if !ok {
		return anchored(rel, FieldHeadlines)
	}
	if hs != nil {
		// 按 id 查找候选前确认 id 是字面写出的：合并键或别名得到的 id 解码后存在，按字面查找却找不到。
		for i, item := range hs.Content {
			if id, _ := workspace.Lookup(item, "id"); item.Kind != yaml.MappingNode || workspace.HasMerge(item) || id != nil && !workspace.Plain(id) {
				return anchored(rel, fmt.Sprintf("headlines[%d]", i))
			}
		}
	}
	for i, id := range e.Remove {
		at := -1
		if hs != nil {
			at = workspace.IndexByID(hs, id)
		}
		if at < 0 {
			return &workspace.Problem{Code: CodeHeadlineNotFound, Severity: workspace.SeverityError, Field: fmt.Sprintf("remove-headline[%d]", i),
				Message: "营销要素中没有标题候选 " + id}
		}
		if workspace.HasAnchor(hs.Content[at]) {
			return anchored(rel, fmt.Sprintf("headlines[%d]", at))
		}
		hs.Content = slices.Delete(hs.Content, at, at+1)
	}
	if h := e.Headline; h != nil {
		if hs == nil {
			hs = &yaml.Node{Kind: yaml.SequenceNode, Tag: "!!seq"}
			if p := setKey(m, rel, "", order, FieldHeadlines, hs); p != nil {
				return p
			}
		}
		at := workspace.IndexByID(hs, h.ID)
		if at < 0 {
			workspace.BlockSequence(m, FieldHeadlines) // 向流式序列追加候选后改为块式，便于阅读
			hs.Content = append(hs.Content, &yaml.Node{Kind: yaml.MappingNode, Tag: "!!map", Content: []*yaml.Node{workspace.Str("id"), workspace.Str(h.ID)}})
			at = len(hs.Content) - 1
		}
		item := hs.Content[at]
		field := fmt.Sprintf("headlines[%d]", at)
		if !workspace.Plain(item) || workspace.HasMerge(item) {
			return anchored(rel, field)
		}
		if h.Text != nil {
			if p := setKey(item, rel, field, []string{"id", "text"}, "text", workspace.Str(*h.Text)); p != nil {
				return p
			}
		}
	}
	if hs != nil && len(hs.Content) == 0 {
		return setKey(m, rel, "", order, FieldHeadlines, nil)
	}
	return nil
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
