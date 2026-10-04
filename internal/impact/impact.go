// Package impact 实现影响传播（Ripple Analysis）：修改已验收的场景、设定条目、事件或伏笔后，按磁盘上的
// 引用图（场景契约的 causes、events、characters、pov 与 state_changes，伏笔台账的埋设与回收，recall 倒排
// 索引的正文命中）确定性计算受影响的后续场景——先序更晚且存在引用边，再做一层事件传递。受影响场景经
// wa-cli impact mark 落盘为复查标记（Recheck Mark）并联动生成 causality 审查任务包；复查通过（重新审查
// 或作者确认无影响）经 wa-cli impact clear 解除。本包不调用大语言模型（LLM）、不 import internal/cli。
package impact

import (
	"fmt"
	"os"
	"path/filepath"
	"slices"
	"strings"
	"time"

	"github.com/kw12121212/writing-anything/internal/canon"
	"github.com/kw12121212/writing-anything/internal/catalog"
	"github.com/kw12121212/writing-anything/internal/draft"
	"github.com/kw12121212/writing-anything/internal/knowledge"
	"github.com/kw12121212/writing-anything/internal/outline"
	"github.com/kw12121212/writing-anything/internal/review"
	"github.com/kw12121212/writing-anything/internal/reviewpacket"
	"github.com/kw12121212/writing-anything/internal/schema"
	"github.com/kw12121212/writing-anything/internal/state"
	"github.com/kw12121212/writing-anything/internal/threads"
	"github.com/kw12121212/writing-anything/internal/work"
	"github.com/kw12121212/writing-anything/internal/workspace"
	"go.yaml.in/yaml/v3"
)

// 影响传播的错误码（Error Code）。已发布的错误码不得改名或复用。
const (
	CodeUnknownTarget          = "impact.unknown-target"
	CodeIncompleteConfirmation = "impact.incomplete-confirmation"
	CodeNotFound               = "impact.not-found"
	CodeInvalid                = "impact.invalid"
)

// Via 是引用路径。
const (
	ViaCauseEvent = "cause-event"
	ViaCharacter  = "character"
	ViaState      = "state"
	ViaKnowledge  = "knowledge"
	ViaThread     = "thread"
)

// Dir 是复查标记根目录。
const Dir = ".writing-anything/rechecks"

// FileName 是单作品的复查标记文件名（与 schema 的路径模式一致：.writing-anything/rechecks/<work>.yaml）。
const FileName = "rechecks.yaml"

// Affected 是一个受影响场景。
type Affected struct {
	Scene  string `json:"scene"`
	Via    string `json:"via"`
	Reason string `json:"reason"`
}

// Mark 是一条复查标记。
type Mark struct {
	Scene    string `json:"scene"`
	Target   string `json:"target"`
	Via      string `json:"via"`
	Reason   string `json:"reason"`
	MarkedAt string `json:"marked_at"`
	Status   string `json:"status"`
	// ClearedAt、Confirmation 与 ReviewBased 是产物字段（取值化可空）；命令输出经 View 呈现，不直接
	// 序列化本结构，避免 --json 信封出现 null（cli-contract/shared-envelope）。
	ClearedAt    *string `json:"-"`
	Confirmation *string `json:"-"`
	ReviewBased  *bool   `json:"-"`
}

// MarkView 是复查标记的输出形态（数组永不为 null）。
type MarkView struct {
	Scene        string `json:"scene"`
	Target       string `json:"target"`
	Via          string `json:"via"`
	Reason       string `json:"reason"`
	MarkedAt     string `json:"marked_at"`
	Status       string `json:"status"`
	ClearedAt    string `json:"clearedAt,omitempty"`
	Confirmation string `json:"confirmation,omitempty"`
	ReviewBased  bool   `json:"reviewBased,omitempty"`
}

// ViewOf 把标记转为输出形态。
func ViewOf(m FileMark) MarkView {
	v := MarkView{Scene: m.Scene, Target: m.Target, Via: m.Via, Reason: m.Reason, MarkedAt: m.MarkedAt, Status: m.Status}
	if m.ClearedAt != nil {
		v.ClearedAt = *m.ClearedAt
	}
	if m.Confirmation != nil {
		v.Confirmation = *m.Confirmation
	}
	if m.ReviewBased != nil {
		v.ReviewBased = *m.ReviewBased
	}
	return v
}

// ViewAll 批量转换。
func ViewAll(ms []FileMark) []MarkView {
	out := []MarkView{}
	for _, m := range ms {
		out = append(out, ViewOf(m))
	}
	return out
}

type fileRecord struct {
	SchemaVersion int        `json:"schema_version"`
	Scenes        []FileMark `json:"scenes"`
}

// FileMark 是产物形态（cleared_at 等带 json 键；输出经 ViewOf 转换）。
type FileMark struct {
	Scene        string  `json:"scene"`
	Target       string  `json:"target"`
	Via          string  `json:"via"`
	Reason       string  `json:"reason"`
	MarkedAt     string  `json:"marked_at"`
	Status       string  `json:"status"`
	ClearedAt    *string `json:"cleared_at"`
	Confirmation *string `json:"confirmation"`
	ReviewBased  *bool   `json:"review_based"`
}

func errorAt(code, path, field, msg string) workspace.Problem {
	return workspace.Problem{Code: code, Severity: workspace.SeverityError, Path: path, Field: field, Message: msg}
}

func path(workID string) string { return Dir + "/" + workID + ".yaml" }

func nowUTC(now time.Time) string { return now.UTC().Format("2006-01-02T15:04:05Z") }

// sceneOutline 读取场景大纲节。
func sceneOutline(root, unitDir string) *outline.Outline {
	rel := outline.Path(unitDir)
	if _, ok, _ := workspace.ReadRegular(root, rel); !ok {
		return nil
	}
	doc, ps := workspace.Read(root, rel)
	if doc == nil || len(ps) > 0 {
		return nil
	}
	if sps := schema.Named("outline").ValidateNode(doc); len(sps) > 0 {
		return nil
	}
	var f struct {
		Sections []outline.Section `json:"sections"`
	}
	catalog.Decode(doc, &f)
	return &outline.Outline{Path: rel, Sections: f.Sections}
}

// Affected 计算目标 target（场景单元引用、设定条目 id、事件 id 或伏笔 id）被修改时受影响的后续场景：
// 先序晚于目标（或目标声明场景）且存在引用边，再做一层事件传递（受影响场景声明的事件所及的更晚场景）。
// 按先序排列，via 与 reason 给出引用依据。
func Compute(root string, w *work.Work, target string) ([]Affected, []workspace.Problem) {
	pos := map[string]int{}
	scenes := []*work.Unit{}
	for i, u := range w.Units() {
		pos[u.Ref] = i
		if u.Type == outline.SceneType {
			scenes = append(scenes, u)
		}
	}
	// 目标解析：场景单元 / canon 条目 / 事件 / 伏笔。
	base := -1 // 目标（或其声明场景）的先序位置；-1 表示作用于全部场景（如无声明场景的事件）。
	kind := ""
	for _, u := range w.Units() {
		if u.Ref == target {
			base = pos[target]
			kind = "scene"
			break
		}
	}
	if kind == "" {
		if entries, _ := canon.List(root, w); entries != nil {
			for _, e := range entries {
				if e.ID == target {
					kind = "canon"
					break
				}
			}
		}
	}
	if kind == "" {
		if items, _ := threads.List(root, w, true); items != nil {
			for _, it := range items {
				if it.ID == target {
					kind = "thread"
					if p, ok := pos[it.PlantedIn]; ok && (base < 0 || p < base) {
						base = p
					}
					break
				}
			}
		}
	}
	if kind == "" {
		// 事件：找声明场景（任一场景大纲 events 含该 id；多个声明取先序最早）。
		for _, s := range scenes {
			ol := sceneOutline(root, w.UnitDir(s))
			if ol == nil {
				continue
			}
			for _, sec := range ol.Sections {
				if slices.Contains(sec.Events, target) {
					if base < 0 || pos[s.Ref] < base {
						base = pos[s.Ref]
					}
					kind = "event"
				}
			}
		}
	}
	if kind == "" {
		return nil, []workspace.Problem{errorAt(CodeUnknownTarget, "", "target",
			"目标 "+target+" 不是本作品的场景单元、设定条目、事件或伏笔；impact 接受这四类引用")}
	}

	// 引用边收集：引用目标（canon/thread/event）或引用目标场景产出内容的边。
	var out []Affected
	seen := map[string]bool{}
	add := func(scene, via, reason string) {
		if seen[scene] {
			return
		}
		seen[scene] = true
		out = append(out, Affected{Scene: scene, Via: via, Reason: reason})
	}
	threadItems, _ := threads.List(root, w, true)
	for _, s := range scenes {
		if base >= 0 && pos[s.Ref] <= base {
			continue
		}
		ol := sceneOutline(root, w.UnitDir(s))
		var causes []string
		if ol != nil {
			for _, sec := range ol.Sections {
				causes = append(causes, sec.Causes...)
				if kind == "canon" {
					for _, name := range sec.Characters {
						if entryID, ok := canonIDByName(root, w, name); ok && entryID == target {
							add(s.Ref, ViaCharacter, fmt.Sprintf("出场人物「%s」（%s）的设定被修改", name, target))
						}
					}
					if sec.Pov != "" {
						if entryID, ok := canonIDByName(root, w, sec.Pov); ok && entryID == target {
							add(s.Ref, ViaCharacter, fmt.Sprintf("视角人物「%s」（%s）的设定被修改", sec.Pov, target))
						}
					}

				}
				if kind == "thread" {
					if slices.Contains(sec.Plants, target) || slices.Contains(sec.Recovers, target) {
						add(s.Ref, ViaThread, "场景契约引用伏笔 "+target)
					}
				}
			}
		}
		if kind == "event" && slices.Contains(causes, target) {
			add(s.Ref, ViaCauseEvent, "场景起因引用事件 "+target)
		}
		if kind == "scene" {
			// 目标是场景：后续场景受影响的依据是引用了该场景产出的事件。
			targetOL := sceneOutline(root, w.UnitDir(byRefUnit(w, target)))
			if targetOL != nil {
				for _, sec := range targetOL.Sections {
					for _, ev := range sec.Events {
						if slices.Contains(causes, ev) {
							add(s.Ref, ViaCauseEvent, fmt.Sprintf("起因事件 %s 由 %s 声明", ev, target))
						}
					}
				}
			}
		}
		if kind == "thread" {
			for _, it := range threadItems {
				if it.ID == target && (it.PlantedIn == s.Ref || it.PlannedRecover == s.Ref || (it.RecoveredIn != nil && *it.RecoveredIn == s.Ref)) {
					add(s.Ref, ViaThread, fmt.Sprintf("伏笔 %s 埋设或回收于本场景", target))
				}
			}
		}
		if kind == "canon" {
			// 状态与知情台账的绑定引用（经收口提交的条目变更也影响绑定场景）。
			if hits := stateKnowledgeScenes(root, w, target); slices.Contains(hits, s.Ref) {
				add(s.Ref, ViaState, "状态或知情台账中绑定本场景的条目 "+target+" 被修改")
			}
			// recall 倒排索引：设定条目 name 与别名在该场景正文的命中（只覆盖 stage 为 done 的场景）。
			if hit := recallHit(root, w, target, s.Ref); hit {
				add(s.Ref, ViaCharacter, fmt.Sprintf("正文中出现该条目的称谓（%s）", target))
			}
		}
	}
	// 一层事件传递：受影响场景声明的事件，其下游 causes 引用场景。
	if len(out) > 0 {
		declared := map[string]string{} // 事件 → 声明它的受影响场景
		for _, a := range out {
			if s := byRefUnit(w, a.Scene); s != nil {
				if ol := sceneOutline(root, w.UnitDir(s)); ol != nil {
					for _, sec := range ol.Sections {
						for _, ev := range sec.Events {
							if _, ok := declared[ev]; !ok {
								declared[ev] = a.Scene
							}
						}
					}
				}
			}
		}
		for _, s := range scenes {
			if seen[s.Ref] || (base >= 0 && pos[s.Ref] <= base) {
				continue
			}
			ol := sceneOutline(root, w.UnitDir(s))
			if ol == nil {
				continue
			}
			for _, sec := range ol.Sections {
				for _, c := range sec.Causes {
					if src, ok := declared[c]; ok {
						add(s.Ref, ViaCauseEvent, fmt.Sprintf("起因事件 %s 由受影响场景 %s 声明", c, src))
					}
				}
			}
		}
	}
	// 按先序排序。
	slices.SortStableFunc(out, func(a, b Affected) int { return pos[a.Scene] - pos[b.Scene] })
	return out, nil
}

// stateKnowledgeScenes 返回状态与知情台账中绑定条目 entry 的场景引用。
func stateKnowledgeScenes(root string, w *work.Work, entry string) []string {
	var out []string
	if events, ps := state.List(root, w, true); !(events == nil && len(ps) > 0) {
		for _, e := range events {
			if e.Entry == entry {
				out = append(out, e.Scene)
			}
		}
	}
	if events, ps := knowledge.List(root, w, true); !(events == nil && len(ps) > 0) {
		for _, e := range events {
			if e.Entry == entry {
				out = append(out, e.Scene)
			}
		}
	}
	return out
}

// recallHit 报告设定条目 entry 的称谓是否出现在场景 s 的正文中（经 recall 的倒排索引；只覆盖已验收场景）。
func recallHit(root string, w *work.Work, entry, scene string) bool {
	entries, _ := canon.List(root, w)
	names := map[string]bool{}
	for _, e := range entries {
		if e.ID == entry {
			for _, n := range e.Names() {
				names[n] = true
			}
		}
	}
	if len(names) == 0 {
		return false
	}
	u := byRefUnit(w, scene)
	if u == nil || u.Stage != catalogStageDone {
		return false
	}
	content, ok, ps := draft.Read(root, w, u)
	if len(ps) > 0 || !ok {
		return false
	}
	text := string(content)
	for n := range names {
		if strings.Contains(text, n) {
			return true
		}
	}
	return false
}

// catalogStageDone 是保留的终点阶段 id（与 units-and-lifecycle 主规格一致）。
const catalogStageDone = "done"

func byRefUnit(w *work.Work, ref string) *work.Unit {
	for _, u := range w.Units() {
		if u.Ref == ref {
			return u
		}
	}
	return nil
}

var canonNameCache map[string]map[string]string // root+work → name → id

func canonIDByName(root string, w *work.Work, name string) (string, bool) {
	key := root + "|" + w.ID
	if canonNameCache == nil {
		canonNameCache = map[string]map[string]string{}
	}
	if _, ok := canonNameCache[key]; !ok {
		m := map[string]string{}
		if entries, _ := canon.List(root, w); entries != nil {
			for _, e := range entries {
				for _, n := range e.Names() {
					if _, dup := m[n]; !dup {
						m[n] = e.ID
					} else {
						m[n] = "" // 同名多条目：多义不解析（与 recall 一致）
					}
				}
			}
		}
		canonNameCache[key] = m
	}
	id, ok := canonNameCache[key][name]
	return id, ok && id != ""
}

// loadRechecks 读取作品的复查标记文件；不存在时返回 nil 与 nil。
func loadRechecks(root, workID string) (*yaml.Node, *fileRecord, []workspace.Problem) {
	rel := path(workID)
	if _, ok, _ := workspace.ReadRegular(root, rel); !ok {
		if info, err := os.Lstat(filepath.Join(root, filepath.FromSlash(rel))); err == nil && !info.Mode().IsRegular() {
			return nil, nil, []workspace.Problem{errorAt(schema.CodeNotFound, rel, "", "复查标记文件途经符号链接或不是普通文件，按不存在处理")}
		}
		return nil, nil, nil
	}
	doc, ps := workspace.Read(root, rel)
	if doc == nil {
		return nil, nil, ps
	}
	if sps := schema.Named("rechecks").ValidateNode(doc); len(sps) > 0 {
		return nil, nil, workspace.FromSchema(rel, sps)
	}
	var f fileRecord
	catalog.Decode(doc, &f)
	return doc, &f, nil
}

// MarkTarget 计算影响并把复查标记追加落盘；同时为每个受影响场景生成 causality 审查任务包（复用
// reviewpacket.Plan）。返回新增标记与变更路径。
func MarkTarget(root string, w *work.Work, target string, budget int, now time.Time) ([]FileMark, []string, []workspace.Problem) {
	affected, ps := Compute(root, w, target)
	if ps != nil {
		return nil, nil, ps
	}
	if len(affected) == 0 {
		return nil, nil, nil
	}
	rel := path(w.ID)
	doc, f, ps := loadRechecks(root, w.ID)
	if ps != nil {
		return nil, nil, ps
	}
	if doc == nil {
		f = &fileRecord{SchemaVersion: 1, Scenes: []FileMark{}}
		doc = &yaml.Node{Kind: yaml.DocumentNode, HeadComment: "复查标记（Recheck Mark）；由 wa-cli impact mark 追加，经 wa-cli impact clear 解除。", Content: []*yaml.Node{{Kind: yaml.MappingNode, Content: []*yaml.Node{
			workspace.Str("schema_version"), workspace.Int(1),
			workspace.Str("scenes"), {Kind: yaml.SequenceNode, Tag: "!!seq"}}}}}
	}
	// 幂等：同（scene, target）的 open 标记不重复。
	open := map[string]bool{}
	for _, m := range f.Scenes {
		if m.Status == "open" {
			open[m.Scene+"\x00"+m.Target] = true
		}
	}
	var fresh []FileMark
	for _, a := range affected {
		if open[a.Scene+"\x00"+target] {
			continue
		}
		fresh = append(fresh, FileMark{Scene: a.Scene, Target: target, Via: a.Via, Reason: a.Reason,
			MarkedAt: nowUTC(now), Status: "open"})
	}
	if len(fresh) == 0 {
		return nil, nil, nil
	}
	// 节点树上追加标记，保留其他内容与注释。
	m := doc.Content[0]
	var seq *yaml.Node
	for i := 0; i+1 < len(m.Content); i += 2 {
		if m.Content[i].Value == "scenes" {
			seq = m.Content[i+1]
		}
	}
	if seq == nil {
		m.Content = append(m.Content, workspace.Str("scenes"), &yaml.Node{Kind: yaml.SequenceNode, Tag: "!!seq"})
		seq = m.Content[len(m.Content)-1]
	}
	workspace.BlockSequence(m, "scenes")
	for _, mk := range fresh {
		item := &yaml.Node{Kind: yaml.MappingNode, Content: []*yaml.Node{
			workspace.Str("scene"), workspace.Str(mk.Scene), workspace.Str("target"), workspace.Str(mk.Target),
			workspace.Str("via"), workspace.Str(mk.Via), workspace.Str("reason"), workspace.Str(mk.Reason),
			workspace.Str("marked_at"), workspace.Str(mk.MarkedAt), workspace.Str("status"), workspace.Str("open")}}
		seq.Content = append(seq.Content, item)
	}
	data, err := workspace.Encode(doc)
	if err != nil {
		return nil, nil, []workspace.Problem{errorAt(workspace.CodeWriteFailed, rel, "", "生成复查标记失败："+err.Error())}
	}
	written, p := schema.Parse(data)
	if p != nil {
		return nil, nil, workspace.FromSchema(rel, []schema.Problem{*p})
	}
	if sps := schema.Named("rechecks").ValidateNode(written); len(sps) > 0 {
		return nil, nil, workspace.FromSchema(rel, sps)
	}
	if err := os.MkdirAll(filepath.Dir(filepath.Join(root, filepath.FromSlash(rel))), 0o755); err != nil {
		return nil, nil, []workspace.Problem{errorAt(workspace.CodeWriteFailed, rel, "", "创建目录失败："+err.Error())}
	}
	if err := workspace.WriteFile(root, rel, data); err != nil {
		return nil, nil, []workspace.Problem{errorAt(workspace.CodeWriteFailed, rel, "", "写入复查标记失败："+err.Error())}
	}
	// 先生成审查任务包，全部成功后再写复查标记（precheck-before-side-effects：两个副作用一个事务，
	// 包生成失败时 rechecks 逐字节不变）。
	changed := []string{}
	for _, a := range affected {
		u := byRefUnit(w, a.Scene)
		if u == nil {
			continue
		}
		_, pk, ps := reviewpacket.Plan(root, w, u, reviewpacket.DimCausality, budget, now)
		if ps != nil {
			return nil, nil, ps
		}
		changed = append(changed, pk...)
	}
	if err := os.MkdirAll(filepath.Dir(filepath.Join(root, filepath.FromSlash(rel))), 0o755); err != nil {
		return nil, nil, []workspace.Problem{errorAt(workspace.CodeWriteFailed, rel, "", "创建目录失败："+err.Error())}
	}
	if err := workspace.WriteFile(root, rel, data); err != nil {
		return nil, nil, []workspace.Problem{errorAt(workspace.CodeWriteFailed, rel, "", "写入复查标记失败："+err.Error())}
	}
	changed = append(changed, rel)
	return fresh, changed, nil
}

// Clear 解除场景 scene 对目标 target 的 open 复查标记：作者确认（--confirmed-by-author 与 --confirmation）
// 或重新审查通过（该场景 review.yaml 的 draft_sha256 与当前正文一致且 rubric 无 fail 结论，此时可省略作者确认）。
func Clear(root string, w *work.Work, scene, target, confirmation string, now time.Time) (*FileMark, []string, []workspace.Problem) {
	rel := path(w.ID)
	doc, f, ps := loadRechecks(root, w.ID)
	if ps != nil {
		return nil, nil, ps
	}
	if doc == nil || f == nil {
		return nil, nil, []workspace.Problem{errorAt(CodeNotFound, "", "", "作品 "+w.ID+" 没有复查标记文件")}
	}
	idx := -1
	for i, m := range f.Scenes {
		if m.Scene == scene && m.Target == target && m.Status == "open" {
			idx = i
			break
		}
	}
	if idx < 0 {
		return nil, nil, []workspace.Problem{errorAt(CodeNotFound, "", "",
			"场景 "+scene+" 没有针对 "+target+" 的 open 复查标记")}
	}
	if strings.TrimSpace(confirmation) == "" && !reviewBased(root, w, scene) {
		return nil, nil, []workspace.Problem{errorAt(CodeIncompleteConfirmation, "", "confirmation",
			"解除复查标记需要 --confirmed-by-author 与 --confirmation（作者确认无影响），或该场景已重新审查通过（审稿需基于当前正文且无 fail 结论）")}
	}
	// 节点树上把该条目置为 cleared。
	m := doc.Content[0]
	var seq *yaml.Node
	for i := 0; i+1 < len(m.Content); i += 2 {
		if m.Content[i].Value == "scenes" {
			seq = m.Content[i+1]
		}
	}
	if seq == nil || idx >= len(seq.Content) {
		return nil, nil, []workspace.Problem{errorAt(CodeInvalid, rel, "", "复查标记文件结构异常")}
	}
	item := seq.Content[idx]
	if f, ok := workspace.SetKey(item, nil, "status", workspace.Str("cleared")); !ok {
		return nil, nil, []workspace.Problem{errorAt(CodeInvalid, rel, "", "标记条目经由 YAML 锚点、别名或合并键得到，wa-cli 无法只修改它（"+f+"）")}
	}
	ts := nowUTC(now)
	setOrAppend := func(key string, n *yaml.Node) *workspace.Problem {
		if f, ok := workspace.SetKey(item, nil, key, n); !ok {
			return &workspace.Problem{Code: CodeInvalid, Severity: workspace.SeverityError, Path: rel, Field: f,
				Message: "标记条目经由 YAML 锚点、别名或合并键得到，wa-cli 无法只修改它"}
		}
		return nil
	}
	if p := setOrAppend("cleared_at", workspace.Str(ts)); p != nil {
		return nil, nil, []workspace.Problem{*p}
	}
	if strings.TrimSpace(confirmation) != "" {
		if p := setOrAppend("confirmation", workspace.Str(confirmation)); p != nil {
			return nil, nil, []workspace.Problem{*p}
		}
	} else if p := setOrAppend("review_based", &yaml.Node{Kind: yaml.ScalarNode, Tag: "!!bool", Value: "true"}); p != nil {
		return nil, nil, []workspace.Problem{*p}
	}
	data, err := workspace.Encode(doc)
	if err != nil {
		return nil, nil, []workspace.Problem{errorAt(workspace.CodeWriteFailed, rel, "", "生成复查标记失败："+err.Error())}
	}
	written, p := schema.Parse(data)
	if p != nil {
		return nil, nil, workspace.FromSchema(rel, []schema.Problem{*p})
	}
	if sps := schema.Named("rechecks").ValidateNode(written); len(sps) > 0 {
		return nil, nil, workspace.FromSchema(rel, sps)
	}
	if err := workspace.WriteFile(root, rel, data); err != nil {
		return nil, nil, []workspace.Problem{errorAt(workspace.CodeWriteFailed, rel, "", "写入复查标记失败："+err.Error())}
	}
	var f2 fileRecord
	catalog.Decode(written, &f2)
	mk := f2.Scenes[idx]
	return &mk, []string{rel}, nil
}

// reviewBased 校验场景是否「重新审查通过」：review.yaml 基于当前正文且 rubric 无 fail 结论。
func reviewBased(root string, w *work.Work, scene string) bool {
	u := byRefUnit(w, scene)
	if u == nil {
		return false
	}
	rv, ps := review.Load(root, w.UnitDir(u))
	if rv == nil || len(ps) > 0 {
		return false
	}
	content, ok, dps := draft.Read(root, w, u)
	if len(dps) > 0 || !ok {
		return false
	}
	if rv.DraftSHA256 != review.SHA256(content) {
		return false
	}
	for _, v := range rv.Rubric {
		if v.Verdict == "fail" {
			return false
		}
	}
	return true
}

// List 列出作品的复查标记：all 为 false 时只含 open。文件不通过校验时返回 nil 与其问题。
func List(root, workID string, all bool) ([]FileMark, []workspace.Problem) {
	_, f, ps := loadRechecks(root, workID)
	if ps != nil {
		return nil, ps
	}
	if f == nil {
		return []FileMark{}, nil
	}
	out := []FileMark{}
	for _, m := range f.Scenes {
		if all || m.Status == "open" {
			out = append(out, m)
		}
	}
	return out, nil
}

// OpenMarks 返回作品的 open 复查标记（next 的 action 提示与组稿关卡用）。
func OpenMarks(root, workID string) []FileMark {
	_, f, ps := loadRechecks(root, workID)
	if f == nil || len(ps) > 0 {
		return nil
	}
	var out []FileMark
	for _, m := range f.Scenes {
		if m.Status == "open" {
			out = append(out, m)
		}
	}
	return out
}

// CheckWorks 校验每个作品的复查标记文件（整体校验）。
func CheckWorks(root string, works []*work.Work) (checked []string, problems []workspace.Problem) {
	for _, w := range works {
		rel := path(w.ID)
		if _, ok, _ := workspace.ReadRegular(root, rel); !ok {
			if info, err := os.Lstat(filepath.Join(root, filepath.FromSlash(rel))); err == nil && !info.Mode().IsRegular() {
				checked = append(checked, rel)
				problems = append(problems, errorAt(schema.CodeNotFound, rel, "", "复查标记文件途经符号链接或不是普通文件，按不存在处理"))
			}
			continue
		}
		checked = append(checked, rel)
		_, f, ps := loadRechecks(root, w.ID)
		if ps != nil {
			problems = append(problems, ps...)
			continue
		}
		problems = append(problems, checkMarks(rel, w, f)...)
	}
	return checked, problems
}

// CheckArtifact 对单个复查标记文件做 Schema 与领域校验。
func CheckArtifact(root, rel string) []workspace.Problem {
	parts := strings.Split(rel, "/")
	var w *work.Work
	if len(parts) >= 3 {
		w, _ = work.Load(root, parts[2])
	}
	if w == nil {
		return nil
	}
	_, f, ps := loadRechecks(root, w.ID)
	if f == nil {
		return ps
	}
	return checkMarks(rel, w, f)
}

// checkMarks 领域校验：场景存在且为 scene、同（scene, target）至多一条 open、cleared 带清除依据、
// open 不得含 cleared_at。
func checkMarks(rel string, w *work.Work, f *fileRecord) (problems []workspace.Problem) {
	open := map[string]bool{}
	for i, m := range f.Scenes {
		field := fmt.Sprintf("scenes[%d]", i)
		if u := byRefUnit(w, m.Scene); u == nil || u.Type != outline.SceneType {
			problems = append(problems, errorAt(CodeUnknownTarget, rel, field+".scene",
				"复查标记的场景 "+m.Scene+" 不是本作品的场景单元"))
		}
		if m.Status == "open" {
			if m.ClearedAt != nil {
				problems = append(problems, errorAt(CodeInvalid, rel, field+".cleared_at",
					"open 标记不得含 cleared_at"))
			}
			if open[m.Scene+"\x00"+m.Target] {
				problems = append(problems, errorAt(CodeInvalid, rel, field,
					"场景 "+m.Scene+" 对 "+m.Target+" 有多条 open 标记"))
			}
			open[m.Scene+"\x00"+m.Target] = true
		} else if m.ClearedAt == nil {
			problems = append(problems, errorAt(CodeInvalid, rel, field+".cleared_at",
				"cleared 标记必须含 cleared_at"))
		} else if m.Confirmation == nil && m.ReviewBased == nil {
			problems = append(problems, errorAt(CodeIncompleteConfirmation, rel, field,
				"cleared 标记必须带作者确认（confirmation）或重新审查依据（review_based）"))
		}
	}
	return problems
}
