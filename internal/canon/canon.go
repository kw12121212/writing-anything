// Package canon 管理作品根单元目录下的设定集（Canon）canon.yaml：人物、地点、物品、世界规则与人物关系的设定条目
// （Canon Entry），每条带出处（素材 id）、别名（Alias）与保护级别（Protection Level）。作者钦定（Author-pinned）
// 的条目受最高级别保护，修改或删除须经决策队列（Decision Queue）授权：拒绝时自动生成阻塞决策，作者决定后携带
// --decision 重试才写入。本包不 import internal/cli；只返回结构化的问题，由命令层转换为诊断（Diagnostic）。
package canon

import (
	"fmt"
	"os"
	"path/filepath"
	"reflect"
	"slices"
	"strings"
	"time"
	"unicode/utf8"

	"github.com/kw12121212/writing-anything/internal/catalog"
	"github.com/kw12121212/writing-anything/internal/decision"
	"github.com/kw12121212/writing-anything/internal/schema"
	"github.com/kw12121212/writing-anything/internal/source"
	"github.com/kw12121212/writing-anything/internal/work"
	"github.com/kw12121212/writing-anything/internal/workspace"
	"go.yaml.in/yaml/v3"
)

// 设定集的错误码（Error Code）。已发布的错误码不得改名或复用。
const (
	CodeUnknownSource    = "canon.unknown-source"
	CodeUnknownCharacter = "canon.unknown-character"
	CodeNotFound         = "canon.not-found"
	CodeAuthorPinned     = "canon.author-pinned"
	CodeAnchoredValue    = "canon.anchored-value"
	CodeUnknownDecision  = "canon.unknown-decision"
	CodeDuplicateAlias   = "canon.duplicate-alias"
)

// FileName 是设定集文件名；Path 返回作品 id 的设定集路径（只在作品根单元目录中识别）。
const FileName = "canon.yaml"

func Path(workID string) string { return work.Dir(workID) + "/" + FileName }

// 设定条目的类型。
const (
	TypeCharacter    = "character" // 人物；该类条目即人物卡（Character Sheet）
	TypeLocation     = "location"
	TypeItem         = "item" // 物品
	TypeWorldRule    = "world-rule"
	TypeRelationship = "relationship"
)

// Types 是设定条目的全部类型，按 help 与诊断中的顺序排列。
var Types = []string{TypeCharacter, TypeLocation, TypeItem, TypeWorldRule, TypeRelationship}

// 保护级别（Protection Level）。
const (
	ProtectionNormal = "normal"
	ProtectionPinned = "author-pinned" // 作者钦定（Author-pinned）：最高级别保护
)

// 钦定条目阻塞决策的操作词。
const (
	OpModify = "修改"
	OpRemove = "删除"
)

// ChoiceAllow 是自动生成决策中「允许」选项的 id。
const ChoiceAllow = "a"

// Entry 是设定集中的一个设定条目（Canon Entry）；可选字段未设置时为 nil。
type Entry struct {
	ID         string    `json:"id"`
	Type       string    `json:"type"`
	Name       *string   `json:"name"`
	Aliases    []string  `json:"aliases"`
	Text       string    `json:"text"`
	Sources    []string  `json:"sources"`
	Protection string    `json:"protection"`
	Members    []string  `json:"members"`
	Decision   *string   `json:"decision"`
	Scope      string    `json:"scope,omitempty"`    // work（缺省）或 series；所在文件决定层级
	Override   *Override `json:"override,omitempty"` // 作品级覆盖系列级同 id 条目的确认记录
}

// Override 是作品级条目覆盖系列级条目的确认记录（作者确认覆盖原因）。
type Override struct {
	Reason       string `json:"reason"`
	Confirmation string `json:"confirmation"`
	ConfirmedAt  string `json:"confirmed_at"`
}

// ScopedEntry 是解析视图中的一个条目：条目与来源层级（work 或 series:<id>）。
type ScopedEntry struct {
	Entry
	Scope string `json:"scope"`
}

// Names 返回条目的全部称谓：name 与别名（Alias），没有 name 时只有别名。
func (e Entry) Names() []string {
	out := []string{}
	if e.Name != nil {
		out = append(out, *e.Name)
	}
	return append(out, e.Aliases...)
}

type canonFile struct {
	Entries []Entry `json:"entries"`
}

func errorAt(code, path, field, msg string) workspace.Problem {
	return workspace.Problem{Code: code, Severity: workspace.SeverityError, Path: path, Field: field, Message: msg}
}

func str(v string) *yaml.Node { return &yaml.Node{Kind: yaml.ScalarNode, Tag: "!!str", Value: v} }

func plain(n *yaml.Node) bool { return n.Anchor == "" && n.Kind != yaml.AliasNode }

func unknownSource(id string) string {
	return "素材（Source）" + id + " 未登记；设定条目只能关联已用 wa-cli source add 登记的素材"
}

func unknownCharacter(id string) string {
	return "人物条目 " + id + " 不在设定集中；人物关系（relationship）的 members 只能引用同一设定集里的 character 条目"
}

// Question 返回为条目 e 与操作 op（OpModify 或 OpRemove）生成的阻塞决策的固定问题文本；
// --decision 的授权按这段文本识别条目与操作。
func Question(op string, e Entry) string {
	if e.Name != nil {
		return fmt.Sprintf("是否允许%s作者钦定的设定条目 %s（%s）？", op, e.ID, *e.Name)
	}
	return fmt.Sprintf("是否允许%s作者钦定的设定条目 %s？", op, e.ID)
}

// PinnedChange 报告决策 d 是否为修改或删除作者钦定（Author-pinned）条目生成的授权决策（按 Question 的固定问题
// 文本识别），供待作者复核（Pending Author Review）计算钦定设定修改。
func PinnedChange(d decision.Decision) bool {
	for _, op := range []string{OpModify, OpRemove} {
		if strings.HasPrefix(d.Question, "是否允许"+op+"作者钦定的设定条目 ") {
			return true
		}
	}
	return false
}

// loadCanon 读取并校验 rel 处的设定集（Schema）；文件不存在时返回 nil 与 nil；途经符号链接或不是普通文件时按
// 不存在处理并报告 artifact.not-found（canon-validation）。领域检查由 checkEntries 负责。
func loadCanon(root, rel string) (*yaml.Node, *canonFile, []workspace.Problem) {
	if _, ok, _ := workspace.ReadRegular(root, rel); !ok {
		if notRegular(root, rel) {
			return nil, nil, []workspace.Problem{errorAt(schema.CodeNotFound, rel, "", "canon.yaml 途经符号链接或不是普通文件，按不存在处理")}
		}
		return nil, nil, nil
	}
	doc, ps := workspace.Read(root, rel)
	if doc == nil {
		return nil, nil, ps
	}
	if ps := schema.Named("canon").ValidateNode(doc); len(ps) > 0 {
		return nil, nil, workspace.FromSchema(rel, ps)
	}
	var f canonFile
	catalog.Decode(doc, &f)
	return doc, &f, nil
}

// notRegular 报告 rel 是否存在但不是普通文件（如符号链接）。只检查最后一级：canon.yaml 的父目录是作品目录，
// 作品能加载（work.yaml 可读）说明途经的目录都是真实目录（validated-path-segments）；validate <path> 的逐级
// 检查由 workspace.ValidateArtifact 负责。
func notRegular(root, rel string) bool {
	info, err := os.Lstat(filepath.Join(root, filepath.FromSlash(rel)))
	return err == nil && !info.Mode().IsRegular()
}

// checkEntries 检查已通过 Schema 校验的设定集：条目 id 唯一；别名（Alias）不得与任何条目的 name、其他条目的
// 别名或本条目 name 重复（name 之间可以重复：场景级上下文包按 name 匹配同名条目）；sources 已登记，members 是
// 同文件的 character 条目，decision 引用的决策存在（knowDecisions 为 true 时检查）。悬空引用与 id、别名重复都
// 返回，级别由调用方决定；别名重复归到该别名自己的字段上。
func checkEntries(root, rel string, f canonFile, ds []decision.Decision, knowDecisions bool) (problems []workspace.Problem) {
	seen := map[string]bool{}
	characters := map[string]bool{}
	for _, e := range f.Entries {
		if e.Type == TypeCharacter {
			characters[e.ID] = true
		}
	}
	names := map[string]bool{} // 全部条目的 name；别名不得与之重复
	for _, e := range f.Entries {
		if e.Name != nil {
			names[*e.Name] = true
		}
	}
	seenAliases := map[string]string{} // 别名 → 首次使用它的条目 id
	for i, e := range f.Entries {
		if seen[e.ID] {
			problems = append(problems, errorAt(catalog.CodeDuplicateID, rel, fmt.Sprintf("entries[%d].id", i), "设定条目 id "+e.ID+" 重复"))
		}
		seen[e.ID] = true
		for j, a := range e.Aliases {
			if first, ok := seenAliases[a]; ok {
				problems = append(problems, errorAt(CodeDuplicateAlias, rel, fmt.Sprintf("entries[%d].aliases[%d]", i, j),
					"别名 "+a+" 已是条目 "+first+" 的别名；同一设定集中别名不得重复"))
				continue
			}
			if names[a] {
				problems = append(problems, errorAt(CodeDuplicateAlias, rel, fmt.Sprintf("entries[%d].aliases[%d]", i, j),
					"别名 "+a+" 与某个条目的名称相同；同一设定集中别名不得与名称重复"))
				continue
			}
			if e.Name != nil && a == *e.Name {
				problems = append(problems, errorAt(CodeDuplicateAlias, rel, fmt.Sprintf("entries[%d].aliases[%d]", i, j),
					"别名 "+a+" 与本条目的名称相同；别名不得与名称重复"))
				continue
			}
			seenAliases[a] = e.ID
		}
		for j, id := range e.Sources {
			if !source.Registered(root, id) {
				problems = append(problems, errorAt(CodeUnknownSource, rel, fmt.Sprintf("entries[%d].sources[%d]", i, j), unknownSource(id)))
			}
		}
		for j, id := range e.Members {
			if !characters[id] {
				problems = append(problems, errorAt(CodeUnknownCharacter, rel, fmt.Sprintf("entries[%d].members[%d]", i, j), unknownCharacter(id)))
			}
		}
		if e.Decision != nil && knowDecisions && !slices.ContainsFunc(ds, func(d decision.Decision) bool { return d.ID == *e.Decision }) {
			problems = append(problems, errorAt(CodeUnknownDecision, rel, fmt.Sprintf("entries[%d].decision", i),
				"条目 "+e.ID+" 记录的授权决策 "+*e.Decision+" 不在决策队列中"))
		}
	}
	return problems
}

// split 把领域检查的问题分为产物自身问题（条目 id 重复、名称或别名重复）与悬空引用，不改变级别。
func split(ps []workspace.Problem) (own, dangling []workspace.Problem) {
	for _, p := range ps {
		if p.Code == catalog.CodeDuplicateID || p.Code == CodeDuplicateAlias {
			own = append(own, p)
			continue
		}
		dangling = append(dangling, p)
	}
	return own, dangling
}

// downgrade 把悬空引用降为 warning 并附修复提示（workspace-artifacts/cross-artifact-references）：
// 修改或列出条目时，已有条目的悬空引用不阻止处理其他条目。
func downgrade(ps []workspace.Problem) []workspace.Problem {
	for i := range ps {
		ps[i].Severity = workspace.SeverityWarning
		switch ps[i].Code {
		case CodeUnknownSource:
			ps[i].Message += "；请用 wa-cli canon set 修复该条目的素材关联"
		case CodeUnknownCharacter:
			ps[i].Message += "；请用 wa-cli canon set 修复该条目的 members"
		case CodeUnknownDecision:
			ps[i].Message += "；请用 wa-cli canon set 重写该条目或补回决策记录"
		}
	}
	return ps
}

// decisions 一次性加载决策队列；队列不存在时视为已知为空（know 为 true），无法加载时 know 为 false 并返回问题。
func decisions(root string) (ds []decision.Decision, know bool, ps []workspace.Problem) {
	ds, _, ps = decision.Load(root)
	if len(ps) > 0 {
		return nil, false, ps
	}
	return ds, true, nil
}

// entriesNode 返回文档中 entries 序列节点；途经锚点、别名或合并键时返回 nil（Schema 保证 entries 存在）。
func entriesNode(doc *yaml.Node) *yaml.Node {
	m := doc.Content[0]
	if !plain(m) {
		return nil
	}
	var v *yaml.Node
	for i := 0; i+1 < len(m.Content); i += 2 {
		if m.Content[i].ShortTag() == "!!merge" {
			return nil
		}
		if m.Content[i].Value == "entries" {
			v = m.Content[i+1]
		}
	}
	if v == nil || !plain(v) {
		return nil
	}
	return v
}

// entryOrder 是设定条目中字段的固定顺序。
var entryOrder = []string{"id", "type", "name", "aliases", "text", "sources", "protection", "members", "decision"}

// checkText 检查写入产物的文本参数是合法的 UTF-8（workspace-artifacts/yaml-text-utf8）。
func checkText(field, v string) []workspace.Problem {
	if !utf8.ValidString(v) {
		return []workspace.Problem{{Code: "input.invalid-value", Severity: workspace.SeverityError, Field: field,
			Message: "参数 " + field + " 不是 UTF-8 文本"}}
	}
	return nil
}

// normalize 把条目的数组字段补成空数组（输出信封中不得为 null）。
func normalize(e Entry) Entry {
	if e.Sources == nil {
		e.Sources = []string{}
	}
	if e.Members == nil {
		e.Members = []string{}
	}
	if e.Aliases == nil {
		e.Aliases = []string{}
	}
	return e
}

// recheck 对编码前的文档做写前复查：编码、重新解析、Schema 校验与领域检查。paramIndex 不为负时，该条目的
// Schema 问题以参数名表示、不填路径（新增或被本次参数修改）。strict 为 true 时领域检查的全部问题都阻止写入
// （canon add：已有条目的悬空引用也阻塞）；否则只有条目 id 重复阻止写入，悬空引用作为 warnings 返回。
func recheck(root, rel string, doc *yaml.Node, paramIndex int, strict bool) (*canonFile, []byte, []workspace.Problem, []workspace.Problem) {
	data, err := workspace.Encode(doc)
	if err != nil {
		return nil, nil, []workspace.Problem{errorAt(workspace.CodeWriteFailed, rel, "", "生成设定集失败："+err.Error())}, nil
	}
	written, p := schema.Parse(data)
	if p != nil {
		p.Line = 0
		return nil, nil, workspace.FromSchema(rel, []schema.Problem{*p}), nil
	}
	if ps := schema.Named("canon").ValidateNode(written); len(ps) > 0 {
		out := workspace.FromSchema(rel, ps)
		if paramIndex >= 0 {
			prefix := fmt.Sprintf("entries[%d]", paramIndex)
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
	var f canonFile
	catalog.Decode(written, &f)
	// 决策队列无法加载时跳过 decision 引用检查（其问题由 decisions 校验报告）。
	ds, know, _ := decisions(root)
	own, dangling := split(checkEntries(root, rel, f, ds, know))
	// 新增或被修改条目的问题以参数名表示、不填路径；其他条目的问题保留产物内路径。
	if paramIndex >= 0 {
		prefix := fmt.Sprintf("entries[%d]", paramIndex)
		for i, p := range own {
			if p.Field == prefix || strings.HasPrefix(p.Field, prefix+".") {
				own[i].Path, own[i].Field = "", strings.TrimPrefix(p.Field, prefix+".")
			}
		}
		for i, p := range dangling {
			if p.Field == prefix || strings.HasPrefix(p.Field, prefix+".") {
				dangling[i].Path, dangling[i].Field = "", strings.TrimPrefix(p.Field, prefix+".")
			}
		}
	}
	if strict {
		if ps := append(slices.Clone(own), dangling...); len(ps) > 0 {
			return nil, nil, ps, nil
		}
		return &f, data, nil, nil
	}
	if len(own) > 0 {
		return nil, nil, own, nil
	}
	return &f, data, nil, downgrade(dangling)
}

// AddInput 是 canon add 的参数。
type AddInput struct {
	Type            string
	Name            string // character、location 与 item 必填；其他类型不得给出
	Aliases         []string
	Text            string
	Sources         []string
	Members         []string // relationship 必填
	Pinned          bool
	ID              string
	Series          bool   // --scope series：写入系列级设定集（作品须属于该系列）
	OverrideReason  string // 覆盖系列级同 id 条目的原因（覆盖时必填）
	OverrideConfirm string // 作者确认原话摘要（覆盖时必填）
}

// Add 在作品 w 的设定集末尾追加一个设定条目。id 为空时取 canon-<n>，n 从已有条目数加一开始递增到不重复；
// sources 中的素材必须已登记，members 必须是同文件的 character 条目。canon.yaml 不存在时创建，已存在时只追加
// 该项，保留其他内容与 YAML 注释。返回新条目与变更的路径；出错时不修改文件。
func Add(root string, w *work.Work, in AddInput) (*Entry, []string, []workspace.Problem) {
	for _, p := range slices.Concat(checkText("name", in.Name), checkText("text", in.Text)) {
		return nil, nil, []workspace.Problem{p}
	}
	// 作用域：series 写入系列级文件；work 覆盖系列级同 id 条目须记录原因并经作者确认。
	target := Path(w.ID)
	scope := "work"
	var override *Override
	if in.Series {
		sid := seriesOf(root, w)
		if sid == "" {
			return nil, nil, []workspace.Problem{errorAt(CodeNotInSeries, "", "scope",
				"作品 "+w.ID+" 不属于任何系列，不能用 --scope series；先 wa-cli series add 把作品加入系列")}
		}
		target = SeriesPath(sid)
		scope = "series"
		if strings.TrimSpace(in.OverrideReason) != "" || strings.TrimSpace(in.OverrideConfirm) != "" {
			return nil, nil, []workspace.Problem{{Code: "input.invalid-value", Severity: workspace.SeverityError, Field: "override-reason",
				Message: "--override-reason 与 --confirmation 只用于作品级覆盖系列级条目；系列级条目本身不存在覆盖"}}
		}
	} else {
		if sid := seriesOf(root, w); sid != "" {
			if seriesEntries, sps := SeriesList(root, sid); sps == nil {
				for _, e := range seriesEntries {
					if e.ID == in.ID || in.ID == "" {
						// 只在最终 id 相同时构成覆盖；id 缺省时无法预判，落到写入后的复查。
						if e.ID == in.ID && in.ID != "" {
							if strings.TrimSpace(in.OverrideReason) == "" || strings.TrimSpace(in.OverrideConfirm) == "" {
								return nil, nil, []workspace.Problem{errorAt(CodeOverrideUnconfirmed, "", "override-reason",
									"条目 "+in.ID+" 覆盖系列级同 id 条目，必须同时给出 --override-reason（覆盖原因）与 --confirmed-by-author、--confirmation（作者确认原话摘要）")}
							}
							override = &Override{Reason: strings.TrimSpace(in.OverrideReason), Confirmation: strings.TrimSpace(in.OverrideConfirm), ConfirmedAt: time.Now().UTC().Format("2006-01-02T15:04:05Z")}
							break
						}
					}
				}
			}
		}
		if strings.TrimSpace(in.OverrideReason) != "" || strings.TrimSpace(in.OverrideConfirm) != "" {
			if override == nil {
				return nil, nil, []workspace.Problem{{Code: "input.invalid-value", Severity: workspace.SeverityError, Field: "override-reason",
					Message: "--override-reason 只在作品级条目覆盖系列级同 id 条目时使用；该 id 不构成覆盖"}}
			}
		}
	}
	_ = target
	_ = scope
	_ = override
	if len(in.Aliases) > 0 && (in.Type == TypeWorldRule || in.Type == TypeRelationship) {
		return nil, nil, []workspace.Problem{{Code: schema.CodeSchemaViolation, Severity: workspace.SeverityError, Field: "aliases",
			Message: "world-rule 与 relationship 条目不得有别名（Alias）；别名只用于 character、location 与 item 条目"}}
	}
	for i, s := range in.Sources {
		if !source.Registered(root, s) {
			return nil, nil, []workspace.Problem{errorAt(CodeUnknownSource, "", fmt.Sprintf("sources[%d]", i), unknownSource(s))}
		}
	}
	rel := target
	doc, f, ps := loadCanon(root, rel)
	if len(ps) > 0 {
		return nil, nil, ps
	}
	if doc == nil {
		f = &canonFile{}
		doc = &yaml.Node{Kind: yaml.DocumentNode, HeadComment: "设定集（Canon）；请用 wa-cli canon add 追加设定条目。", Content: []*yaml.Node{{Kind: yaml.MappingNode, Content: []*yaml.Node{
			str("schema_version"), {Kind: yaml.ScalarNode, Tag: "!!int", Value: "1"},
			str("entries"), {Kind: yaml.SequenceNode, Tag: "!!seq"},
		}}}}
	}
	seen := map[string]bool{}
	for _, e := range f.Entries {
		seen[e.ID] = true
	}
	id := in.ID
	if id == "" {
		for n := len(f.Entries) + 1; ; n++ {
			if id = fmt.Sprintf("canon-%d", n); !seen[id] {
				break
			}
		}
	} else if seen[id] {
		return nil, nil, []workspace.Problem{errorAt(catalog.CodeDuplicateID, rel, "id", "作品 "+w.ID+" 的设定集中已有条目 "+id)}
	}
	entries := entriesNode(doc)
	if entries == nil {
		return nil, nil, []workspace.Problem{errorAt(CodeAnchoredValue, rel, "entries",
			"entries 经由 YAML 锚点、别名或合并键得到，wa-cli 无法只追加一项；请先去掉锚点、别名与合并键")}
	}
	protection := ProtectionNormal
	if in.Pinned {
		protection = ProtectionPinned
	}
	item := &yaml.Node{Kind: yaml.MappingNode, Content: []*yaml.Node{str("id"), str(id), str("type"), str(in.Type), str("scope"), str(scope)}}
	if in.Name != "" {
		item.Content = append(item.Content, str("name"), str(in.Name))
	}
	if len(in.Aliases) > 0 {
		item.Content = append(item.Content, str("aliases"), seqNode(in.Aliases))
	}
	item.Content = append(item.Content, str("text"), str(in.Text), str("sources"), seqNode(in.Sources), str("protection"), str(protection))
	if in.Type == TypeRelationship {
		item.Content = append(item.Content, str("members"), seqNode(in.Members))
	}
	index := len(entries.Content)
	workspace.BlockSequence(doc.Content[0], "entries")
	entries.Content = append(entries.Content, item)
	saved, data, ps, _ := recheck(root, rel, doc, index, true)
	if len(ps) > 0 {
		return nil, nil, ps
	}
	if err := workspace.WriteFile(root, rel, data); err != nil {
		return nil, nil, []workspace.Problem{errorAt(workspace.CodeWriteFailed, rel, "", "写入设定集失败："+err.Error())}
	}
	e := normalize(saved.Entries[index])
	e.Scope = scope
	e.Override = override
	return &e, []string{rel}, nil
}

func seqNode(v []string) *yaml.Node {
	n := &yaml.Node{Kind: yaml.SequenceNode, Tag: "!!seq", Style: yaml.FlowStyle}
	for _, s := range v {
		n.Content = append(n.Content, str(s))
	}
	return n
}

// SetInput 是 canon set 的修改参数；指针为 nil 表示不修改。
type SetInput struct {
	Type     *string
	Name     *string // 指向空字符串时删除 name 字段
	Aliases  *[]string
	Text     *string
	Sources  *[]string // 整体替换
	Members  *[]string // 整体替换；指向空切片时删除 members 字段
	Pin      *bool     // true 把 protection 升为 author-pinned；false 降为 normal（须经决策授权）
	Decision string    // 修改作者钦定条目的授权决策 id
}

// Set 修改作品 w 的设定集中 id 的条目，只改动给出的字段，保留其他条目、其他内容与 YAML 注释。修改作者钦定的
// 条目（包括把 protection 改为 normal）需要指向已决定且选择为允许的决策（见 canon-protection）；未授权时拒绝
// （canon.author-pinned）并幂等地生成阻塞决策。取值未变时不写文件、不需要决策。返回修改后的条目、变更的路径
// （拒绝且追加了决策时为决策队列路径）与问题（成功时只含其他条目悬空引用的 warning）。
func Set(root string, w *work.Work, id string, in SetInput) (*Entry, []string, []workspace.Problem) {
	if in.Text != nil {
		if ps := checkText("text", *in.Text); len(ps) > 0 {
			return nil, nil, ps
		}
	}
	if in.Name != nil {
		if ps := checkText("name", *in.Name); len(ps) > 0 {
			return nil, nil, ps
		}
	}
	if in.Sources != nil {
		for i, s := range *in.Sources {
			if !source.Registered(root, s) {
				return nil, nil, []workspace.Problem{errorAt(CodeUnknownSource, "", fmt.Sprintf("sources[%d]", i), unknownSource(s))}
			}
		}
	}
	rel := Path(w.ID)
	doc, f, ps := loadCanon(root, rel)
	if len(ps) > 0 {
		return nil, nil, ps
	}
	notFound := []workspace.Problem{errorAt(CodeNotFound, "", "", "作品 "+w.ID+" 的设定集中没有条目（Canon Entry）"+id)}
	if doc == nil {
		return nil, nil, notFound
	}
	entries := entriesNode(doc)
	if entries == nil {
		return nil, nil, []workspace.Problem{errorAt(CodeAnchoredValue, rel, "entries",
			"entries 经由 YAML 锚点、别名或合并键得到，wa-cli 无法只修改一项；请先去掉锚点、别名与合并键")}
	}
	at := workspace.IndexByID(entries, id)
	if at < 0 {
		return nil, nil, notFound
	}
	entry := f.Entries[at]
	pinned := entry.Protection == ProtectionPinned
	if !pinned && in.Decision != "" {
		return nil, nil, []workspace.Problem{{Code: "input.invalid-value", Severity: workspace.SeverityError, Field: "decision",
			Message: "条目 " + entry.ID + " 不是作者钦定（Author-pinned）条目，不需要 --decision；该参数只用于修改或删除钦定条目的授权"}}
	}

	// 授权预检（纯检查，不写入）：--decision 必须指向为该条目与该操作生成的、已决定且 choice 为 a 的决策。
	var queueProblems []workspace.Problem
	authorized := true
	if pinned {
		var ds []decision.Decision
		ds, _, queueProblems = decision.Load(root)
		authorized = false
		if len(queueProblems) == 0 {
			authorized = authorize(ds, OpModify, entry, in.Decision)
		}
	}

	before := schema.Decode(doc)
	if p := editEntry(entries, at, rel, in, authorized); p != nil {
		return nil, nil, []workspace.Problem{*p}
	}
	saved, data, rps, warns := recheck(root, rel, doc, at, false)
	if len(rps) > 0 {
		return nil, nil, rps
	}
	if reflect.DeepEqual(before, schema.Decode(doc)) {
		e := normalize(saved.Entries[at])
		return &e, nil, warns
	}
	if pinned && !authorized {
		changed, gps := gate(root, w, OpModify, entry, in.Decision, queueProblems)
		return nil, changed, gps
	}
	if err := workspace.WriteFile(root, rel, data); err != nil {
		return nil, nil, []workspace.Problem{errorAt(workspace.CodeWriteFailed, rel, "", "写入设定集失败："+err.Error())}
	}
	e := normalize(saved.Entries[at])
	return &e, []string{rel}, warns
}

// editEntry 在节点树上只修改目标条目的给出字段；途经锚点、别名或合并键时返回问题。授权成立且给出 --decision
// 时，把决策 id 记录到条目的 decision 字段。
func editEntry(entries *yaml.Node, at int, rel string, in SetInput, authorized bool) *workspace.Problem {
	item := entries.Content[at]
	field := fmt.Sprintf("entries[%d]", at)
	if !plain(item) || workspace.HasMerge(item) {
		return &workspace.Problem{Code: CodeAnchoredValue, Severity: workspace.SeverityError, Path: rel, Field: field,
			Message: "该条目经由 YAML 锚点、别名或合并键得到，wa-cli 无法只修改它；请先去掉锚点、别名与合并键"}
	}
	type kv struct {
		key string
		n   *yaml.Node // nil 表示删除该字段
	}
	var kvs []kv
	if in.Type != nil {
		kvs = append(kvs, kv{"type", str(*in.Type)})
	}
	if in.Name != nil {
		var n *yaml.Node
		if *in.Name != "" {
			n = str(*in.Name)
		}
		kvs = append(kvs, kv{"name", n})
	}
	if in.Aliases != nil {
		var n *yaml.Node
		if len(*in.Aliases) > 0 {
			n = seqNode(*in.Aliases)
		}
		kvs = append(kvs, kv{"aliases", n})
	}
	if in.Text != nil {
		kvs = append(kvs, kv{"text", str(*in.Text)})
	}
	if in.Sources != nil {
		kvs = append(kvs, kv{"sources", seqNode(*in.Sources)})
	}
	if in.Pin != nil {
		p := ProtectionNormal
		if *in.Pin {
			p = ProtectionPinned
		}
		kvs = append(kvs, kv{"protection", str(p)})
	}
	if in.Members != nil {
		var n *yaml.Node
		if len(*in.Members) > 0 {
			n = seqNode(*in.Members)
		}
		kvs = append(kvs, kv{"members", n})
	}
	if authorized && in.Decision != "" {
		kvs = append(kvs, kv{"decision", str(in.Decision)})
	}
	for _, pair := range kvs {
		if f, ok := workspace.SetKey(item, entryOrder, pair.key, pair.n); !ok {
			return &workspace.Problem{Code: CodeAnchoredValue, Severity: workspace.SeverityError, Path: rel, Field: field + "." + f,
				Message: "该处经由 YAML 锚点、别名或合并键得到，wa-cli 无法只修改这一处；请先去掉锚点、别名与合并键"}
		}
	}
	return nil
}

// Remove 删除作品 w 的设定集中 id 的条目，保留其他内容与注释；删除 character 条目后仍有 relationship 引用它时
// 照常删除，以 warning 报告。删除作者钦定的条目需要决策授权（见 canon-protection）。返回是否已删除、变更的路径
// （拒绝且追加了决策时为决策队列路径）与问题。
func Remove(root string, w *work.Work, id, decisionID string) (bool, []string, []workspace.Problem) {
	rel := Path(w.ID)
	doc, f, ps := loadCanon(root, rel)
	if len(ps) > 0 {
		return false, nil, ps
	}
	notFound := []workspace.Problem{errorAt(CodeNotFound, "", "", "作品 "+w.ID+" 的设定集中没有条目（Canon Entry）"+id)}
	if doc == nil {
		return false, nil, notFound
	}
	entries := entriesNode(doc)
	if entries == nil {
		return false, nil, []workspace.Problem{errorAt(CodeAnchoredValue, rel, "entries",
			"entries 经由 YAML 锚点、别名或合并键得到，wa-cli 无法只删除一项；请先去掉锚点、别名与合并键")}
	}
	at := workspace.IndexByID(entries, id)
	if at < 0 {
		return false, nil, notFound
	}
	entry := f.Entries[at]
	pinned := entry.Protection == ProtectionPinned
	if !pinned && decisionID != "" {
		return false, nil, []workspace.Problem{{Code: "input.invalid-value", Severity: workspace.SeverityError, Field: "decision",
			Message: "条目 " + entry.ID + " 不是作者钦定（Author-pinned）条目，不需要 --decision；该参数只用于修改或删除钦定条目的授权"}}
	}
	var queueProblems []workspace.Problem
	authorized := true
	if pinned {
		var ds []decision.Decision
		ds, _, queueProblems = decision.Load(root)
		authorized = false
		if len(queueProblems) == 0 {
			authorized = authorize(ds, OpRemove, entry, decisionID)
		}
	}
	if workspace.HasAnchor(entries.Content[at]) {
		return false, nil, []workspace.Problem{{Code: CodeAnchoredValue, Severity: workspace.SeverityError, Path: rel,
			Field:   fmt.Sprintf("entries[%d]", at),
			Message: "该条目中有 YAML 锚点、别名或合并键，删除会改变其他取值；请先去掉锚点、别名与合并键"}}
	}
	entries.Content = slices.Delete(entries.Content, at, at+1)
	_, data, rps, warns := recheck(root, rel, doc, -1, false)
	if len(rps) > 0 {
		return false, nil, rps
	}
	if pinned && !authorized {
		changed, gps := gate(root, w, OpRemove, entry, decisionID, queueProblems)
		return false, changed, gps
	}
	if err := workspace.WriteFile(root, rel, data); err != nil {
		return false, nil, []workspace.Problem{errorAt(workspace.CodeWriteFailed, rel, "", "写入设定集失败："+err.Error())}
	}
	return true, []string{rel}, warns
}

// gate 拒绝修改或删除作者钦定的条目（canon.author-pinned），并幂等地向决策队列追加阻塞决策；决策队列无法加载时
// 不追加（canon-protection）。同一作品、同一条目与同一操作已有 open 的此类决策，或作者已选择保持不变（选项 b）
// 时不重复追加；作者选择允许后再次未授权尝试需要新的授权，会追加新的决策。返回 changed（追加了决策时为决策队列
// 路径）与问题（首条为 canon.author-pinned）。
func gate(root string, w *work.Work, op string, e Entry, decisionID string, queueProblems []workspace.Problem) ([]string, []workspace.Problem) {
	pin := func(msg string) []workspace.Problem {
		return []workspace.Problem{{Code: CodeAuthorPinned, Severity: workspace.SeverityError, Path: Path(w.ID), Field: e.ID, Message: msg}}
	}
	if len(queueProblems) > 0 {
		if decisionID != "" {
			return nil, pin("决策 " + decisionID + " 不能授权这次" + op + "：决策队列无法加载，未能核对")
		}
		return nil, append(pin("条目 "+e.ID+" 由作者钦定（Author-pinned），wa-cli 不能"+op+"它；决策队列无法加载，未生成阻塞决策"), queueProblems...)
	}
	ds, _, ps := decision.Load(root)
	if len(ps) > 0 {
		return nil, append(pin("条目 "+e.ID+" 由作者钦定（Author-pinned），wa-cli 不能"+op+"它；决策队列无法加载，未生成阻塞决策"), ps...)
	}
	if decisionID != "" {
		return nil, pin(rejectDecision(ds, op, e, decisionID))
	}
	q := Question(op, e)
	for _, d := range ds {
		if d.Question != q || !slices.Contains(d.Units, w.ID) {
			continue
		}
		if d.Status == decision.StatusOpen {
			return nil, pin("条目 " + e.ID + " 由作者钦定（Author-pinned），wa-cli 不能" + op + "它；阻塞决策已在队列中，等待作者决定")
		}
		if d.Status == decision.StatusDecided && d.Choice != nil && *d.Choice != ChoiceAllow {
			return nil, pin("条目 " + e.ID + " 由作者钦定（Author-pinned），作者已决定保持不变，wa-cli 不能" + op + "它")
		}
	}
	if _, ps := decision.Add(root, decision.Input{
		Question:  q,
		Options:   []string{"允许", "保持不变"},
		Recommend: "b",
		Rationale: "作者钦定（Author-pinned）的设定条目默认保持不变，等待作者决定是否允许这次" + op,
		Blocking:  true,
		Units:     []string{w.ID},
	}, func(ref string) []workspace.Problem { return nil }); len(ps) > 0 {
		return nil, append(pin("条目 "+e.ID+" 由作者钦定（Author-pinned），wa-cli 不能"+op+"它"), ps...)
	}
	return []string{decision.Path}, pin("条目 " + e.ID + " 由作者钦定（Author-pinned），wa-cli 不能" + op + "它；已生成阻塞决策，等待作者决定")
}

// rejectDecision 说明 --decision 不能授权的原因；给出 --decision 时不追加新的决策（canon-protection）。
func rejectDecision(ds []decision.Decision, op string, e Entry, decisionID string) string {
	for _, d := range ds {
		if d.ID != decisionID {
			continue
		}
		switch {
		case d.Question != Question(op, e):
			return "决策 " + decisionID + " 不是为该条目与这次" + op + "生成的，不能授权"
		case d.Status != decision.StatusDecided:
			return "决策 " + decisionID + " 尚未由作者决定，不能授权这次" + op
		default:
			return "决策 " + decisionID + " 中作者选择保持不变，wa-cli 不能" + op + "该条目"
		}
	}
	return "决策 " + decisionID + " 不存在，不能授权这次" + op + "：它必须是为该条目与该操作生成的、已由作者决定且选择为允许的决策"
}

// authorize 报告 decisionID 是否授权对条目 e 执行操作 op：决策必须存在、为该条目与该操作生成（按固定问题文本
// 识别）、status 为 decided 且 choice 为「允许」（选项 a）。
func authorize(ds []decision.Decision, op string, e Entry, decisionID string) bool {
	if decisionID == "" {
		return false
	}
	for _, d := range ds {
		if d.ID != decisionID {
			continue
		}
		return d.Question == Question(op, e) && d.Status == decision.StatusDecided && d.Choice != nil && *d.Choice == ChoiceAllow
	}
	return false
}

// List 读取作品 w 的设定集并返回全部条目（按文件顺序）：Schema 问题或条目 id 重复时不返回条目并报告其问题；
// 条目引用失效时照常返回，这些问题以 warning 报告。canon.yaml 不存在时返回空。
func List(root string, w *work.Work) ([]Entry, []workspace.Problem) {
	rel := Path(w.ID)
	_, f, ps := loadCanon(root, rel)
	if f == nil {
		return nil, ps
	}
	ds, know, _ := decisions(root)
	own, dangling := split(checkEntries(root, rel, *f, ds, know))
	if len(own) > 0 {
		return nil, own
	}
	out := []Entry{}
	for _, e := range f.Entries {
		out = append(out, normalize(e))
	}
	return out, downgrade(dangling)
}

// CheckWorks 校验每个可加载作品的设定集（整体校验）：Schema 与领域检查（条目 id 唯一、sources 已登记、
// members 为同文件 character 条目、decision 引用的决策存在）。canon.yaml 途经符号链接时报告 artifact.not-found。
// 返回已校验的路径与全部问题。
func CheckWorks(root string, works []*work.Work) (checked []string, problems []workspace.Problem) {
	ds, know, _ := decisions(root)
	for _, w := range works {
		rel := Path(w.ID)
		_, f, ps := loadCanon(root, rel)
		if f == nil {
			if len(ps) > 0 {
				checked = append(checked, rel)
				problems = append(problems, ps...)
			}
			continue
		}
		checked = append(checked, rel)
		problems = append(problems, checkEntries(root, rel, *f, ds, know)...)
	}
	return checked, problems
}

// CheckArtifact 对 rel（作品根单元目录下的 canon.yaml）做 Schema 与领域检查，供 wa-cli validate <path>。
func CheckArtifact(root, rel string) []workspace.Problem {
	_, f, ps := loadCanon(root, rel)
	if f == nil {
		return ps
	}
	ds, know, _ := decisions(root)
	return checkEntries(root, rel, *f, ds, know)
}

// CharacterIDs 返回作品 w 设定集中全部 character 条目（人物卡）的 id 集合：canon.yaml 不存在时为空集合；
// 无法读取或未通过 Schema 校验时 ok 为 false（其问题由 canon 自身的校验报告），调用方应跳过对人物链接的检查。
func CharacterIDs(root string, w *work.Work) (ids map[string]bool, ok bool) {
	rel := Path(w.ID)
	if _, regular, _ := workspace.ReadRegular(root, rel); !regular {
		return map[string]bool{}, true
	}
	doc, ps := workspace.Read(root, rel)
	if doc == nil || len(ps) > 0 {
		return nil, false
	}
	if ps := schema.Named("canon").ValidateNode(doc); len(ps) > 0 {
		return nil, false
	}
	var f canonFile
	catalog.Decode(doc, &f)
	ids = map[string]bool{}
	for _, e := range f.Entries {
		if e.Type == TypeCharacter {
			ids[e.ID] = true
		}
	}
	return ids, true
}

// 作用域的错误码（Error Code）。已发布的错误码不得改名或复用。
const (
	CodeOverrideUnconfirmed = "canon.override-unconfirmed"
	CodeNotInSeries         = "canon.not-in-series"
)

// SeriesPath 返回系列级设定集的路径（.writing-anything/series/<series>/canon.yaml）。
func SeriesPath(seriesID string) string {
	return ".writing-anything/series/" + seriesID + "/" + FileName
}

// seriesOf 返回作品所属系列 id（空表示不属于任何系列）；作品级缓存每命令一次。
func seriesOf(root string, w *work.Work) string {
	if w == nil {
		return ""
	}
	if seriesMemberCache == nil {
		seriesMemberCache = map[string]string{}
	}
	if id, ok := seriesMemberCache[w.ID]; ok {
		return id
	}
	// 系列文件为平铺的 .writing-anything/series/<id>.yaml（series 主规格）；逐个读取 works 判定成员。
	for _, name := range workspace.YAMLFiles(root, ".writing-anything/series") {
		rel := ".writing-anything/series/" + name
		doc, ps := workspace.Read(root, rel)
		if doc == nil || len(ps) > 0 {
			continue
		}
		var f struct {
			ID    string   `json:"id"`
			Works []string `json:"works"`
		}
		catalog.Decode(doc, &f)
		if f.ID == "" {
			f.ID = strings.TrimSuffix(name, ".yaml")
		}
		if slices.Contains(f.Works, w.ID) {
			seriesMemberCache[w.ID] = f.ID
			return f.ID
		}
	}
	seriesMemberCache[w.ID] = ""
	return ""
}

var seriesMemberCache map[string]string

// seriesFileName 是系列文件的文件名（series 主规格）。

// SeriesList 读取系列级设定集（不存在时返回空与 nil；不通过 Schema 或领域校验时返回 nil 与其问题）。
func SeriesList(root, seriesID string) ([]Entry, []workspace.Problem) {
	rel := SeriesPath(seriesID)
	if _, ok, _ := workspace.ReadRegular(root, rel); !ok {
		if info, err := os.Lstat(filepath.Join(root, filepath.FromSlash(rel))); err == nil && !info.Mode().IsRegular() {
			return nil, []workspace.Problem{errorAt(schema.CodeNotFound, rel, "", "系列设定集途经符号链接或不是普通文件，按不存在处理")}
		}
		return nil, nil
	}
	doc, ps := workspace.Read(root, rel)
	if doc == nil {
		return nil, ps
	}
	if sps := schema.Named("canon").ValidateNode(doc); len(sps) > 0 {
		return nil, workspace.FromSchema(rel, sps)
	}
	var f canonFile
	catalog.Decode(doc, &f)
	return f.Entries, nil
}

// Resolve 返回作品 w 的按作用域解析视图：作品级条目全部保留（scope 为 work），所属系列的系列级条目并入
// （scope 为 series:<id>），系列级条目与作品级同 id 时被覆盖不出现；每项含来源层级。系列文件不通过校验时
// 返回 nil 与其问题（调用方降级为作品级）。
func Resolve(root string, w *work.Work) ([]ScopedEntry, []workspace.Problem) {
	out := []ScopedEntry{}
	workEntries, ps := List(root, w)
	if workEntries == nil && len(ps) > 0 {
		// 作品级文件不通过校验：解析视图失败（同 List 的 fatal 语义）。
		return nil, ps
	}
	byID := map[string]bool{}
	for _, e := range workEntries {
		byID[e.ID] = true
		out = append(out, ScopedEntry{Entry: e, Scope: "work"})
	}
	if sid := seriesOf(root, w); sid != "" {
		seriesEntries, sps := SeriesList(root, sid)
		if sps != nil {
			return nil, sps
		}
		for _, e := range seriesEntries {
			if byID[e.ID] {
				continue // 被作品级覆盖
			}
			out = append(out, ScopedEntry{Entry: e, Scope: "series:" + sid})
		}
	}
	return out, nil
}
