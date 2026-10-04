// Package threads 管理作品根单元目录下的伏笔台账（Foreshadowing Ledger）threads.yaml：每个伏笔条目（Thread Entry）
// 记录伏笔内容、埋设场景、计划回收位置与状态（open 未回收 / paid 已回收 / dropped 放弃）。放弃（dropped）是作者的
// 叙事决策，须经决策队列（Decision Queue）授权：拒绝时自动生成阻塞决策，作者决定后携带 --decision 重试才写入
// （与设定集（Canon）作者钦定条目同一阻塞模式）。场景契约（Scene Contract）的 plants/recovers 与台账交叉校验也
// 在本包实现。本包不 import internal/cli；只返回结构化的问题，由命令层转换为诊断（Diagnostic）。
package threads

import (
	"fmt"
	"os"
	"path/filepath"
	"reflect"
	"slices"
	"strings"
	"unicode/utf8"

	"github.com/kw12121212/writing-anything/internal/catalog"
	"github.com/kw12121212/writing-anything/internal/decision"
	"github.com/kw12121212/writing-anything/internal/outline"
	"github.com/kw12121212/writing-anything/internal/schema"
	"github.com/kw12121212/writing-anything/internal/work"
	"github.com/kw12121212/writing-anything/internal/workspace"
	"go.yaml.in/yaml/v3"
)

// 伏笔台账的错误码（Error Code）。已发布的错误码不得改名或复用。
const (
	CodeUnknownUnit        = "threads.unknown-unit"
	CodeRecoverBeforePlant = "threads.recover-before-plant"
	CodeUnknownDecision    = "threads.unknown-decision"
	CodeNotFound           = "threads.not-found"
	CodeAuthorConfirmation = "threads.author-confirmation"
	CodeUnknownThread      = "threads.unknown-thread"
	CodeRecoverNotPlanted  = "threads.recover-not-planted"
	CodeAnchoredValue      = "threads.anchored-value"
)

// FileName 是伏笔台账文件名；Path 返回作品 id 的台账路径（只在作品根单元目录中识别）。
const FileName = "threads.yaml"

func Path(workID string) string { return work.Dir(workID) + "/" + FileName }

// 伏笔条目的状态。
const (
	StatusOpen    = "open"    // 未回收
	StatusPaid    = "paid"    // 已回收
	StatusDropped = "dropped" // 放弃（须经作者确认）
)

// typeChapter 是计划回收位置允许的章（chapter）单元类型；场景（scene）用 outline.SceneType。
const typeChapter = "chapter"

// OpDrop 是放弃伏笔的操作词，用于阻塞决策的问题文本。
const OpDrop = "放弃"

// ChoiceAllow 是自动生成决策中「允许」选项的 id。
const ChoiceAllow = "a"

// Entry 是伏笔台账中的一个伏笔条目（Thread Entry）；可选字段未设置时为 nil。
type Entry struct {
	ID             string  `json:"id"`
	Text           string  `json:"text"`
	PlantedIn      string  `json:"planted_in"`
	PlannedRecover string  `json:"planned_recover"`
	Status         string  `json:"status"`
	RecoveredIn    *string `json:"recovered_in"`
	Decision       *string `json:"decision"`
}

// Item 是列表输出中的一条伏笔：条目与其是否已超过计划回收位置。
type Item struct {
	Entry
	Overdue bool `json:"overdue"`
}

type file struct {
	Entries []Entry `json:"entries"`
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

// Question 返回为放弃条目 id 生成的阻塞决策的固定问题文本；--decision 的授权按这段文本识别条目与操作。
func Question(id string) string {
	return fmt.Sprintf("是否允许%s伏笔台账（Foreshadowing Ledger）中的伏笔条目（Thread Entry）%s？", OpDrop, id)
}

// load 读取并校验 rel 处的伏笔台账（Schema）；文件不存在时返回 nil 与 nil；途经符号链接或不是普通文件时按
// 不存在处理并报告 artifact.not-found。领域检查由 checkEntries 负责。w 为 nil 时单元引用检查在 checkEntries 中跳过。
func load(root, rel string, w *work.Work) (*yaml.Node, *file, []workspace.Problem) {
	if _, ok, _ := workspace.ReadRegular(root, rel); !ok {
		if info, err := os.Lstat(filepath.Join(root, filepath.FromSlash(rel))); err == nil && !info.Mode().IsRegular() {
			return nil, nil, []workspace.Problem{errorAt(schema.CodeNotFound, rel, "", "threads.yaml 途经符号链接或不是普通文件，按不存在处理")}
		}
		return nil, nil, nil
	}
	doc, ps := workspace.Read(root, rel)
	if doc == nil {
		return nil, nil, ps
	}
	if ps := schema.Named("threads").ValidateNode(doc); len(ps) > 0 {
		return nil, nil, workspace.FromSchema(rel, ps)
	}
	var f file
	catalog.Decode(doc, &f)
	return doc, &f, nil
}

// checkEntries 检查已通过 Schema 校验的伏笔台账：条目 id 唯一；planted_in 与 recovered_in 是本作品的 scene 单元、
// planned_recover 是本作品的 chapter 或 scene 单元且按单元树先序不早于 planted_in；dropped 条目的 decision 引用的
// 决策存在（know 为 true 时检查）。w 为 nil 时跳过单元引用与先序检查。悬空引用与 id 重复都返回，级别由调用方决定。
func checkEntries(rel string, f file, w *work.Work, ds []decision.Decision, know bool) (problems []workspace.Problem) {
	var pos map[string]int
	var byRef map[string]*work.Unit
	if w != nil {
		pos, byRef = units(w)
	}
	seen := map[string]bool{}
	for i, e := range f.Entries {
		if seen[e.ID] {
			problems = append(problems, errorAt(catalog.CodeDuplicateID, rel, fmt.Sprintf("entries[%d].id", i), "伏笔条目 id "+e.ID+" 重复"))
		}
		seen[e.ID] = true
		if w == nil {
			continue
		}
		if u, ok := byRef[e.PlantedIn]; !ok || u.Type != outline.SceneType {
			problems = append(problems, errorAt(CodeUnknownUnit, rel, fmt.Sprintf("entries[%d].planted_in", i),
				"埋设场景 "+e.PlantedIn+" 不是本作品的场景（scene）单元；planted_in 只能指向 scene 单元"))
		}
		if u, ok := byRef[e.PlannedRecover]; !ok || (u.Type != outline.SceneType && u.Type != typeChapter) {
			problems = append(problems, errorAt(CodeUnknownUnit, rel, fmt.Sprintf("entries[%d].planned_recover", i),
				"计划回收位置 "+e.PlannedRecover+" 不是本作品的章（chapter）或场景（scene）单元"))
		} else if recoverBeforePlant(w, pos, byRef, e.PlantedIn, e.PlannedRecover) {
			problems = append(problems, errorAt(CodeRecoverBeforePlant, rel, fmt.Sprintf("entries[%d].planned_recover", i),
				"计划回收位置 "+e.PlannedRecover+" 早于埋设场景 "+e.PlantedIn+"；其单元下没有任何不早于埋设场景的场景"))
		}
		if e.RecoveredIn != nil {
			if u, ok := byRef[*e.RecoveredIn]; !ok || u.Type != outline.SceneType {
				problems = append(problems, errorAt(CodeUnknownUnit, rel, fmt.Sprintf("entries[%d].recovered_in", i),
					"回收场景 "+*e.RecoveredIn+" 不是本作品的场景（scene）单元"))
			}
		}
		if e.Decision != nil && know && !slices.ContainsFunc(ds, func(d decision.Decision) bool { return d.ID == *e.Decision }) {
			problems = append(problems, errorAt(CodeUnknownDecision, rel, fmt.Sprintf("entries[%d].decision", i),
				"条目 "+e.ID+" 记录的授权决策 "+*e.Decision+" 不在决策队列中"))
		}
	}
	return problems
}

// split 把领域检查的问题分为产物自身问题（条目 id 重复）与悬空引用，不改变级别。
func split(ps []workspace.Problem) (own, dangling []workspace.Problem) {
	for _, p := range ps {
		if p.Code == catalog.CodeDuplicateID {
			own = append(own, p)
			continue
		}
		dangling = append(dangling, p)
	}
	return own, dangling
}

// downgrade 把悬空引用降为 warning 并附修复提示（workspace-artifacts/cross-artifact-references）：
// 列出条目时，已有条目的悬空引用不阻止处理其他条目。
func downgrade(ps []workspace.Problem) []workspace.Problem {
	for i := range ps {
		ps[i].Severity = workspace.SeverityWarning
		switch ps[i].Code {
		case CodeUnknownUnit:
			ps[i].Message += "；请修正该条目的单元引用（planned_recover 可用 wa-cli threads set 调整）"
		case CodeRecoverBeforePlant:
			ps[i].Message += "；请用 wa-cli threads set 调整计划回收位置"
		case CodeUnknownDecision:
			ps[i].Message += "；请用 wa-cli threads set 重写该条目或补回决策记录"
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

// entryOrder 是伏笔条目中字段的固定顺序。
var entryOrder = []string{"id", "text", "planted_in", "planned_recover", "status", "recovered_in", "decision"}

// checkText 检查写入产物的文本参数是合法的 UTF-8（workspace-artifacts/yaml-text-utf8）。
func checkText(field, v string) []workspace.Problem {
	if !utf8.ValidString(v) {
		return []workspace.Problem{{Code: "input.invalid-value", Severity: workspace.SeverityError, Field: field,
			Message: "参数 " + field + " 不是 UTF-8 文本"}}
	}
	return nil
}

// recheck 对编码前的文档做写前复查：编码、重新解析、Schema 校验与领域检查。paramIndex 不为负时，该条目的
// Schema 与领域问题以参数名表示、不填路径（新增或被本次参数修改）。strict 为 true 时领域检查的全部问题都阻止
// 写入（threads add）；否则只有条目 id 重复阻止写入，悬空引用作为 warnings 返回。
func recheck(root, rel string, doc *yaml.Node, w *work.Work, paramIndex int, strict bool) (*file, []byte, []workspace.Problem, []workspace.Problem) {
	data, err := workspace.Encode(doc)
	if err != nil {
		return nil, nil, []workspace.Problem{errorAt(workspace.CodeWriteFailed, rel, "", "生成伏笔台账失败："+err.Error())}, nil
	}
	written, p := schema.Parse(data)
	if p != nil {
		p.Line = 0
		return nil, nil, workspace.FromSchema(rel, []schema.Problem{*p}), nil
	}
	if ps := schema.Named("threads").ValidateNode(written); len(ps) > 0 {
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
	var f file
	catalog.Decode(written, &f)
	// 决策队列无法加载时跳过 decision 引用检查（其问题由 decisions 校验报告）。
	ds, know, _ := decisions(root)
	own, dangling := split(checkEntries(rel, f, w, ds, know))
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

// AddInput 是 threads add 的参数。
type AddInput struct {
	Text      string
	PlantIn   string
	RecoverAt string
	ID        string
}

// Add 在作品 w 的伏笔台账末尾追加一个伏笔条目（初始状态 open）。id 为空时取 thread-<n>，n 从已有条目数加一起
// 递增到不重复；planted_in 必须是本作品的 scene 单元，recover_at 必须是 chapter 或 scene 单元且按先序不早于
// planted_in。threads.yaml 不存在时创建，已存在时只追加该项，保留其他内容与 YAML 注释。返回新条目与变更的路径；
// 出错时不修改文件。
func Add(root string, w *work.Work, in AddInput) (*Entry, []string, []workspace.Problem) {
	for _, p := range slices.Concat(checkText("text", in.Text), checkText("plant-in", in.PlantIn), checkText("recover-at", in.RecoverAt)) {
		return nil, nil, []workspace.Problem{p}
	}
	if strings.TrimSpace(in.Text) == "" {
		return nil, nil, []workspace.Problem{{Code: "input.invalid-value", Severity: workspace.SeverityError, Field: "text",
			Message: "伏笔内容不能为空或只含空白"}}
	}
	pos, byRef := units(w)
	if u, ok := byRef[in.PlantIn]; !ok || u.Type != outline.SceneType {
		return nil, nil, []workspace.Problem{errorAt(CodeUnknownUnit, "", "plant-in",
			"埋设场景 "+in.PlantIn+" 不是作品 "+w.ID+" 的场景（scene）单元；--plant-in 只能指向 scene 单元")}
	}
	if u, ok := byRef[in.RecoverAt]; !ok || (u.Type != outline.SceneType && u.Type != typeChapter) {
		return nil, nil, []workspace.Problem{errorAt(CodeUnknownUnit, "", "recover-at",
			"计划回收位置 "+in.RecoverAt+" 不是作品 "+w.ID+" 的章（chapter）或场景（scene）单元")}
	}
	if recoverBeforePlant(w, pos, byRef, in.PlantIn, in.RecoverAt) {
		return nil, nil, []workspace.Problem{errorAt(CodeRecoverBeforePlant, "", "recover-at",
			"计划回收位置 "+in.RecoverAt+" 早于埋设场景 "+in.PlantIn+"；其单元下没有任何不早于埋设场景的场景")}
	}
	rel := Path(w.ID)
	doc, f, ps := load(root, rel, w)
	if len(ps) > 0 {
		return nil, nil, ps
	}
	if doc == nil {
		f = &file{}
		doc = &yaml.Node{Kind: yaml.DocumentNode, HeadComment: "伏笔台账（Foreshadowing Ledger）；请用 wa-cli threads add 登记伏笔条目。", Content: []*yaml.Node{{Kind: yaml.MappingNode, Content: []*yaml.Node{
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
			if id = fmt.Sprintf("thread-%d", n); !seen[id] {
				break
			}
		}
	} else if seen[id] {
		return nil, nil, []workspace.Problem{errorAt(catalog.CodeDuplicateID, rel, "id", "作品 "+w.ID+" 的伏笔台账中已有条目 "+id)}
	}
	entries := entriesNode(doc)
	if entries == nil {
		return nil, nil, []workspace.Problem{errorAt(CodeAnchoredValue, rel, "entries",
			"entries 经由 YAML 锚点、别名或合并键得到，wa-cli 无法只追加一项；请先去掉锚点、别名与合并键")}
	}
	item := &yaml.Node{Kind: yaml.MappingNode, Content: []*yaml.Node{
		str("id"), str(id), str("text"), str(in.Text), str("planted_in"), str(in.PlantIn),
		str("planned_recover"), str(in.RecoverAt), str("status"), str(StatusOpen)}}
	index := len(entries.Content)
	workspace.BlockSequence(doc.Content[0], "entries")
	entries.Content = append(entries.Content, item)
	// 其他条目已有的悬空引用不阻止登记新条目（workspace-artifacts/cross-artifact-references）：本条目的参数在
	// 写入前已逐一校验，recheck 只会再确认一遍；其余条目的问题以 warning 返回。
	saved, data, ps, warns := recheck(root, rel, doc, w, index, false)
	if len(ps) > 0 {
		return nil, nil, ps
	}
	if err := workspace.WriteFile(root, rel, data); err != nil {
		return nil, nil, []workspace.Problem{errorAt(workspace.CodeWriteFailed, rel, "", "写入伏笔台账失败："+err.Error())}
	}
	e := saved.Entries[index]
	return &e, []string{rel}, warns
}

// SetInput 是 threads set 的修改参数；指针为 nil 表示不修改。
type SetInput struct {
	Text        *string
	RecoverAt   *string
	Status      *string
	RecoveredIn *string // 指向空字符串时清除该字段
	Decision    string  // 放弃条目的授权决策 id
}

// Set 修改作品 w 的伏笔台账中 id 的条目，只改动给出的字段，保留其他条目、其他内容与 YAML 注释。--status 为 paid
// 时必须同时给出 --recovered-in（或条目已有回收场景）；--status 为 open 时清除 recovered_in 与 decision；放弃
// （--status dropped）需要作者确认：未授权时拒绝（threads.author-confirmation）并幂等地生成阻塞决策，作者允许后
// 携带 --decision 重试才写入。取值未变时不写文件、不需要决策。返回修改后的条目、变更的路径（拒绝且追加了决策时为
// 决策队列路径）与问题（成功时只含其他条目悬空引用的 warning）。
func Set(root string, w *work.Work, id string, in SetInput) (*Entry, []string, []workspace.Problem) {
	if in.Text != nil {
		if ps := checkText("text", *in.Text); len(ps) > 0 {
			return nil, nil, ps
		}
	}
	if in.RecoverAt != nil {
		if ps := checkText("recover-at", *in.RecoverAt); len(ps) > 0 {
			return nil, nil, ps
		}
	}
	pos, byRef := units(w)
	if in.RecoverAt != nil && *in.RecoverAt != "" {
		if u, ok := byRef[*in.RecoverAt]; !ok || (u.Type != outline.SceneType && u.Type != typeChapter) {
			return nil, nil, []workspace.Problem{errorAt(CodeUnknownUnit, "", "recover-at",
				"计划回收位置 "+*in.RecoverAt+" 不是作品 "+w.ID+" 的章（chapter）或场景（scene）单元")}
		}
	}
	if in.RecoveredIn != nil && *in.RecoveredIn != "" {
		if ps := checkText("recovered-in", *in.RecoveredIn); len(ps) > 0 {
			return nil, nil, ps
		}
		if u, ok := byRef[*in.RecoveredIn]; !ok || u.Type != outline.SceneType {
			return nil, nil, []workspace.Problem{errorAt(CodeUnknownUnit, "", "recovered-in",
				"回收场景 "+*in.RecoveredIn+" 不是作品 "+w.ID+" 的场景（scene）单元")}
		}
	}
	if in.Status != nil && *in.Status != StatusOpen && *in.Status != StatusPaid && *in.Status != StatusDropped {
		return nil, nil, []workspace.Problem{{Code: "input.invalid-value", Severity: workspace.SeverityError, Field: "status",
			Message: "状态（status）只能是 open（未回收）、paid（已回收）或 dropped（放弃）"}}
	}
	rel := Path(w.ID)
	doc, f, ps := load(root, rel, w)
	if len(ps) > 0 {
		return nil, nil, ps
	}
	notFound := []workspace.Problem{errorAt(CodeNotFound, "", "", "作品 "+w.ID+" 的伏笔台账中没有伏笔条目（Thread Entry）"+id)}
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
	wantsDrop := in.Status != nil && *in.Status == StatusDropped
	dropping := wantsDrop && entry.Status != StatusDropped
	if in.Decision != "" && !wantsDrop {
		return nil, nil, []workspace.Problem{{Code: "input.invalid-value", Severity: workspace.SeverityError, Field: "decision",
			Message: "--decision 只用于放弃（--status dropped）条目的授权；条目 " + entry.ID + " 本次不在放弃"}}
	}

	// 组装写入后的条目取值，检查参数组合。
	next := entry
	if in.Text != nil {
		next.Text = *in.Text
	}
	if in.RecoverAt != nil {
		next.PlannedRecover = *in.RecoverAt
	}
	if in.Status != nil {
		next.Status = *in.Status
	}
	if in.RecoveredIn != nil {
		if *in.RecoveredIn == "" {
			next.RecoveredIn = nil
		} else {
			next.RecoveredIn = in.RecoveredIn
		}
	}
	if in.Status != nil && *in.Status == StatusOpen {
		next.RecoveredIn, next.Decision = nil, nil
	}
	// 状态互斥：paid 只带 recovered_in，dropped 只带 decision（threads-artifact）。
	if next.Status == StatusPaid {
		next.Decision = nil
	}
	if next.Status == StatusDropped && in.Status != nil {
		next.RecoveredIn = nil
	}
	if strings.TrimSpace(next.Text) == "" {
		return nil, nil, []workspace.Problem{{Code: "input.invalid-value", Severity: workspace.SeverityError, Field: "text",
			Message: "伏笔内容不能为空或只含空白"}}
	}
	if recoverBeforePlant(w, pos, byRef, next.PlantedIn, next.PlannedRecover) {
		return nil, nil, []workspace.Problem{errorAt(CodeRecoverBeforePlant, "", "recover-at",
			"计划回收位置 "+next.PlannedRecover+" 早于埋设场景 "+next.PlantedIn+"；其单元下没有任何不早于埋设场景的场景")}
	}
	if next.Status == StatusPaid && next.RecoveredIn == nil {
		return nil, nil, []workspace.Problem{{Code: "input.invalid-value", Severity: workspace.SeverityError, Field: "status",
			Message: "已回收（paid）的条目必须同时用 --recovered-in 记录回收发生的场景"}}
	}
	if next.Status == StatusOpen && next.RecoveredIn != nil {
		return nil, nil, []workspace.Problem{{Code: "input.invalid-value", Severity: workspace.SeverityError, Field: "recovered-in",
			Message: "未回收（open）的条目不能记录回收场景；先 --status paid 再给 --recovered-in"}}
	}

	// 授权预检（纯检查，不写入）：--decision 必须指向为放弃该条目生成的、已决定且 choice 为 a 的决策。
	// 未授权的放弃在改写文件前直接走 gate（拒绝并生成阻塞决策），不会写出没有 decision 的 dropped 条目。
	var queueProblems []workspace.Problem
	authorized := true
	if dropping {
		var ds []decision.Decision
		ds, _, queueProblems = decision.Load(root)
		authorized = false
		if len(queueProblems) == 0 {
			authorized = authorize(ds, entry.ID, in.Decision)
		}
		if !authorized {
			changed, gps := gate(root, w, entry.ID, in.Decision, queueProblems)
			return nil, changed, gps
		}
		next.Decision = &in.Decision
	}

	before := schema.Decode(doc)
	if p := editEntry(entries, at, rel, next); p != nil {
		return nil, nil, []workspace.Problem{*p}
	}
	saved, data, rps, warns := recheck(root, rel, doc, w, at, false)
	if len(rps) > 0 {
		return nil, nil, rps
	}
	if reflect.DeepEqual(before, schema.Decode(doc)) {
		e := saved.Entries[at]
		return &e, nil, warns
	}
	if err := workspace.WriteFile(root, rel, data); err != nil {
		return nil, nil, []workspace.Problem{errorAt(workspace.CodeWriteFailed, rel, "", "写入伏笔台账失败："+err.Error())}
	}
	e := saved.Entries[at]
	return &e, []string{rel}, warns
}

// editEntry 在节点树上把条目 at 写成 next 的取值（decision 与 recovered_in 为 nil 时删除该字段）；途经锚点、
// 别名或合并键时返回问题。
func editEntry(entries *yaml.Node, at int, rel string, next Entry) *workspace.Problem {
	item := entries.Content[at]
	field := fmt.Sprintf("entries[%d]", at)
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
	kvs := []kv{{"text", str(next.Text)}, {"planned_recover", str(next.PlannedRecover)}, {"status", str(next.Status)},
		{"recovered_in", node(next.RecoveredIn)}, {"decision", node(next.Decision)}}
	for _, pair := range kvs {
		if f, ok := workspace.SetKey(item, entryOrder, pair.key, pair.n); !ok {
			return &workspace.Problem{Code: CodeAnchoredValue, Severity: workspace.SeverityError, Path: rel, Field: field + "." + f,
				Message: "该处经由 YAML 锚点、别名或合并键得到，wa-cli 无法只修改这一处；请先去掉锚点、别名与合并键"}
		}
	}
	return nil
}

// gate 拒绝放弃伏笔条目（threads.author-confirmation），并幂等地向决策队列追加阻塞决策；决策队列无法加载时不追加。
// 同一作品、同一条目已有 open 的此类决策，或作者已选择保持不变（选项 b）时不重复追加；作者选择允许后再次未授权
// 尝试需要新的授权，会追加新的决策。返回 changed（追加了决策时为决策队列路径）与问题（首条为 threads.author-confirmation）。
func gate(root string, w *work.Work, id, decisionID string, queueProblems []workspace.Problem) ([]string, []workspace.Problem) {
	refuse := func(msg string) []workspace.Problem {
		return []workspace.Problem{{Code: CodeAuthorConfirmation, Severity: workspace.SeverityError, Path: Path(w.ID), Field: id, Message: msg}}
	}
	if len(queueProblems) > 0 {
		if decisionID != "" {
			return nil, refuse("决策 " + decisionID + " 不能授权这次" + OpDrop + "：决策队列无法加载，未能核对")
		}
		return nil, append(refuse("伏笔条目 "+id+" 的"+OpDrop+"需要作者确认，wa-cli 不能代为决定；决策队列无法加载，未生成阻塞决策"), queueProblems...)
	}
	ds, _, ps := decision.Load(root)
	if len(ps) > 0 {
		return nil, append(refuse("伏笔条目 "+id+" 的"+OpDrop+"需要作者确认，wa-cli 不能代为决定；决策队列无法加载，未生成阻塞决策"), ps...)
	}
	if decisionID != "" {
		return nil, refuse(rejectDecision(ds, id, decisionID))
	}
	q := Question(id)
	for _, d := range ds {
		if d.Question != q || !slices.Contains(d.Units, w.ID) {
			continue
		}
		if d.Status == decision.StatusOpen {
			return nil, refuse("伏笔条目 " + id + " 的" + OpDrop + "需要作者确认；阻塞决策已在队列中，等待作者决定")
		}
		if d.Status == decision.StatusDecided && d.Choice != nil && *d.Choice != ChoiceAllow {
			return nil, refuse("伏笔条目 " + id + " 的" + OpDrop + "需要作者确认，作者已决定保持不变，wa-cli 不能放弃它")
		}
	}
	if _, ps := decision.Add(root, decision.Input{
		Question:  q,
		Options:   []string{"允许", "保持不变"},
		Recommend: "b",
		Rationale: "放弃（dropped）伏笔会改变后文走向，默认保持，等待作者决定是否允许这次" + OpDrop,
		Blocking:  true,
		Units:     []string{w.ID},
	}, func(ref string) []workspace.Problem { return nil }); len(ps) > 0 {
		return nil, append(refuse("伏笔条目 "+id+" 的"+OpDrop+"需要作者确认，wa-cli 不能代为决定"), ps...)
	}
	return []string{decision.Path}, refuse("伏笔条目 " + id + " 的" + OpDrop + "需要作者确认；已生成阻塞决策，等待作者决定")
}

// rejectDecision 说明 --decision 不能授权的原因；给出 --decision 时不追加新的决策。
func rejectDecision(ds []decision.Decision, id, decisionID string) string {
	for _, d := range ds {
		if d.ID != decisionID {
			continue
		}
		switch {
		case d.Question != Question(id):
			return "决策 " + decisionID + " 不是为" + OpDrop + "伏笔条目 " + id + " 生成的，不能授权"
		case d.Status != decision.StatusDecided:
			return "决策 " + decisionID + " 尚未由作者决定，不能授权这次" + OpDrop
		default:
			return "决策 " + decisionID + " 中作者选择保持不变，wa-cli 不能放弃该条目"
		}
	}
	return "决策 " + decisionID + " 不存在，不能授权这次" + OpDrop + "：它必须是为该条目生成、已由作者决定且选择为允许的决策"
}

// authorize 报告 decisionID 是否授权放弃条目 id：决策必须存在、为该条目生成（按固定问题文本识别）、status 为
// decided 且 choice 为「允许」（选项 a）。
func authorize(ds []decision.Decision, id, decisionID string) bool {
	if decisionID == "" {
		return false
	}
	for _, d := range ds {
		if d.ID != decisionID {
			continue
		}
		return d.Question == Question(id) && d.Status == decision.StatusDecided && d.Choice != nil && *d.Choice == ChoiceAllow
	}
	return false
}

// under 报告 u 是否在 c 的子树内（u 是 c 或其子孙）；work.under 未导出，这里按公开的 Parent 链判断。
func under(u, c *work.Unit) bool {
	for x := u; x != nil; x = x.Parent {
		if x == c {
			return true
		}
	}
	return false
}

// recoverBeforePlant 报告计划回收位置 recover 是否早于埋设场景 plant：回收单元（章或场景）下存在按单元树先序
// 不早于 plant 的场景时为 false。章节点在先序中先于其子场景，不能直接比较章与场景的位置，否则「埋设在章内场景、
// 计划在该章回收」会被误判；还没有任何场景的空章按单元位置直接比较（自上而下建章后补场景）。
// 单元解析不了时为 false（其问题由 threads.unknown-unit 报告）。
// RecoverBeforePlant 报告回收位置 recover 按单元树先序是否早于埋设场景 plant（同一语义供收口等调用方预检复用）：
// 计划回收单元子树内存在先序不早于埋设场景的场景时按不早于处理，还没有场景的空章按单元位置直接比较。
func RecoverBeforePlant(w *work.Work, plant, recover string) bool {
	pos, byRef := units(w)
	return recoverBeforePlant(w, pos, byRef, plant, recover)
}

func recoverBeforePlant(w *work.Work, pos map[string]int, byRef map[string]*work.Unit, plant, recover string) bool {
	pu, ok := byRef[plant]
	if !ok || pu.Type != outline.SceneType {
		return false
	}
	ru, ok := byRef[recover]
	if !ok {
		return false
	}
	hasScene := false
	for _, u := range w.Units() {
		if u.Type != outline.SceneType {
			continue
		}
		if u == ru || under(u, ru) {
			if pos[u.Ref] >= pos[plant] {
				return false
			}
			hasScene = true
		}
	}
	// 还没有任何场景的空章（自上而下建章后补场景）：按单元位置直接比较。
	if !hasScene {
		return pos[recover] < pos[plant]
	}
	return true
}

// overdue 报告条目 e 是否已超过计划回收位置：status 为 open 且（计划回收单元的阶段（Stage）为 done，或存在既不在
// 计划回收单元子树内、又按单元树先序位于其后的 done 场景）。单元引用解析不了时为 false（其问题由校验报告）。
func overdue(w *work.Work, pos map[string]int, byRef map[string]*work.Unit, e Entry) bool {
	if e.Status != StatusOpen {
		return false
	}
	pu, ok := byRef[e.PlannedRecover]
	if !ok {
		return false
	}
	if w.EffectiveStage(pu) == catalog.StageDone {
		return true
	}
	pp := pos[e.PlannedRecover]
	for _, u := range w.Units() {
		if u.Type != outline.SceneType || w.EffectiveStage(u) != catalog.StageDone || under(u, pu) {
			continue
		}
		if pos[u.Ref] > pp {
			return true
		}
	}
	return false
}

// List 读取作品 w 的伏笔台账并返回条目列表：all 为 false 时只含 open 条目；按 planted_in 的单元树先序排序
// （先序相同或解析不了时按文件顺序，解析不了的排在最后）。Schema 问题或条目 id 重复时不返回条目并报告其问题；
// 条目引用失效时照常返回，这些问题以 warning 报告。threads.yaml 不存在时返回空。
func List(root string, w *work.Work, all bool) ([]Item, []workspace.Problem) {
	rel := Path(w.ID)
	_, f, ps := load(root, rel, w)
	if f == nil {
		return nil, ps
	}
	ds, know, _ := decisions(root)
	own, dangling := split(checkEntries(rel, *f, w, ds, know))
	if len(own) > 0 {
		return nil, own
	}
	pos, byRef := units(w)
	out := []Item{}
	for _, e := range f.Entries {
		if !all && e.Status != StatusOpen {
			continue
		}
		out = append(out, Item{Entry: e, Overdue: overdue(w, pos, byRef, e)})
	}
	slices.SortStableFunc(out, func(a, b Item) int {
		pa, oka := pos[a.PlantedIn]
		pb, okb := pos[b.PlantedIn]
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

// ContractProblems 检查场景大纲与伏笔台账的交叉一致（threads 主规格 threads-contract-checks）：大纲节 recovers 引用
// 的伏笔必须在台账中且埋设不晚于本场景，plants 引用的伏笔必须埋设恰在本场景。scenes 必须是同一作品全部场景单元的
// 单元树先序序列；场景的 Outline 非 nil 时代替磁盘上的大纲（供写前检查）。台账不存在或不通过 Schema 校验时不检查
// （其问题由台账校验报告）；大纲读不出来的场景跳过。
func ContractProblems(root string, w *work.Work, scenes []outline.Scene) []workspace.Problem {
	_, f, ps := load(root, Path(w.ID), w)
	if f == nil || len(ps) > 0 {
		return nil
	}
	byID := make(map[string]Entry, len(f.Entries))
	for _, e := range f.Entries {
		byID[e.ID] = e
	}
	pos, _ := units(w)
	var out []workspace.Problem
	for _, sc := range scenes {
		o := sc.Outline
		if o == nil {
			o = outline.Loadable(root, sc.Dir)
		}
		if o == nil {
			continue
		}
		here := pos[sc.Ref]
		for si, sec := range o.Sections {
			for ri, id := range sec.Recovers {
				e, ok := byID[id]
				if !ok {
					out = append(out, errorAt(CodeUnknownThread, outline.Path(sc.Dir), fmt.Sprintf("sections[%d].recovers[%d]", si, ri),
						"伏笔 "+id+" 不在作品 "+w.ID+" 的伏笔台账（threads.yaml）中；recovers 只能引用 wa-cli threads add 登记的伏笔"))
					continue
				}
				if p, resolved := pos[e.PlantedIn]; resolved && here >= 0 && p > here {
					out = append(out, errorAt(CodeRecoverNotPlanted, outline.Path(sc.Dir), fmt.Sprintf("sections[%d].recovers[%d]", si, ri),
						"伏笔 "+id+" 的埋设场景是 "+e.PlantedIn+"，按单元树先序不排在本场景之前；recovers 只能引用前文已埋设的伏笔"))
				}
			}
			for pi, id := range sec.Plants {
				e, ok := byID[id]
				if !ok {
					out = append(out, errorAt(CodeUnknownThread, outline.Path(sc.Dir), fmt.Sprintf("sections[%d].plants[%d]", si, pi),
						"伏笔 "+id+" 不在作品 "+w.ID+" 的伏笔台账（threads.yaml）中；plants 只能引用 wa-cli threads add 登记的伏笔"))
					continue
				}
				if e.PlantedIn != sc.Ref {
					out = append(out, errorAt(CodeUnknownThread, outline.Path(sc.Dir), fmt.Sprintf("sections[%d].plants[%d]", si, pi),
						"伏笔 "+id+" 的埋设场景是 "+e.PlantedIn+"；本场景的 plants 只能引用埋设在本场景（"+sc.Ref+"）的伏笔"))
				}
			}
		}
	}
	return out
}

// CheckWorks 校验每个可加载作品的伏笔台账并做契约交叉检查（整体校验）：Schema 与领域检查（条目 id 唯一、单元引用
// 存在与类型、计划回收不早于埋设、decision 引用的决策存在）。threads.yaml 途经符号链接时报告 artifact.not-found。
// 返回已校验的路径与全部问题；没有台账的作品不做任何伏笔检查。
func CheckWorks(root string, works []*work.Work) (checked []string, problems []workspace.Problem) {
	ds, know, _ := decisions(root)
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
		problems = append(problems, checkEntries(rel, *f, w, ds, know)...)
		problems = append(problems, ContractProblems(root, w, w.Scenes())...)
	}
	return checked, problems
}

// CheckArtifact 对 rel（作品根单元目录下的 threads.yaml）做 Schema 与领域检查，供 wa-cli validate <path>。
// 所在作品无法加载时只做不依赖单元树的检查（其问题由作品校验报告）。
func CheckArtifact(root, rel string) []workspace.Problem {
	var w *work.Work
	if parts := strings.Split(rel, "/"); len(parts) >= 4 {
		w, _ = work.Load(root, parts[2])
	}
	_, f, ps := load(root, rel, w)
	if f == nil {
		return ps
	}
	ds, know, _ := decisions(root)
	out := checkEntries(rel, *f, w, ds, know)
	if w != nil {
		out = append(out, ContractProblems(root, w, w.Scenes())...)
	}
	return out
}
