// Package continuity 实现确定性连续性审查（Continuity Review）：wa-cli continuity 在全书范围对场景执行
// 不依赖模型的检查——未建卡出场人物、保留词终态人物出场（死亡、失踪等）、同场景多名一卡、按场景过滤的悬空
// 引用与伏笔超期——发现即阻塞该场景的验收，直到修改正文或作者经决策队列（Decision Queue）豁免。豁免按
// （场景， 错误码）粒度幂等地生成阻塞决策（同 canon 作者钦定与伏笔放弃的模式）。本包不 import internal/cli；
// 只返回结构化的问题，由命令层转换为诊断（Diagnostic）。
package continuity

import (
	"fmt"
	"os"
	"path/filepath"
	"slices"
	"strings"

	"github.com/kw12121212/writing-anything/internal/canon"
	"github.com/kw12121212/writing-anything/internal/catalog"
	"github.com/kw12121212/writing-anything/internal/closeout"
	"github.com/kw12121212/writing-anything/internal/decision"
	"github.com/kw12121212/writing-anything/internal/knowledge"
	"github.com/kw12121212/writing-anything/internal/outline"
	"github.com/kw12121212/writing-anything/internal/schema"
	"github.com/kw12121212/writing-anything/internal/state"
	"github.com/kw12121212/writing-anything/internal/threads"
	"github.com/kw12121212/writing-anything/internal/timeline"
	"github.com/kw12121212/writing-anything/internal/work"
	"github.com/kw12121212/writing-anything/internal/workspace"
)

// 连续性审查的错误码（Error Code）。已发布的错误码不得改名或复用。
const (
	CodeUnknownCharacter  = "continuity.unknown-character"
	CodeDeadActing        = "continuity.dead-acting"
	CodeAliasMismatch     = "continuity.alias-mismatch"
	CodeDanglingReference = "continuity.dangling-reference"
	CodeThreadAnomaly     = "continuity.thread-anomaly"
)

// TerminalStates 是 dead-acting 检查的保留词：出场人物最新已确认状态的完整描述精确等于其中之一时，
// 该人物不应再出场。作者不用这些规范词时规则静默，语义写法由审查任务包（review-task-packets）负责。
var TerminalStates = []string{"死亡", "已死亡", "已亡故", "失踪", "已退场"}

// Finding 是一条连续性发现。
type Finding struct {
	Scene    string `json:"scene"`
	Code     string `json:"code"`
	Severity string `json:"severity"`
	Waived   bool   `json:"waived"`
	Message  string `json:"message"`
}

// Question 返回（场景， 错误码）豁免决策的固定问题文本：同场景同错误码的多条发现共用一个豁免决策。
func Question(scene, code, firstSentence string) string {
	return fmt.Sprintf("是否豁免场景 %s 的连续性检查 %s？", scene, code)
}

// sceneOutline 读取场景大纲（只需要节：复用 outline 的 Schema 校验，诊断原样返回）。
func sceneOutline(root, unitDir string) (*outline.Outline, []workspace.Problem) {
	rel := outline.Path(unitDir)
	if _, ok, _ := workspace.ReadRegular(root, rel); !ok {
		return nil, nil
	}
	doc, ps := workspace.Read(root, rel)
	if doc == nil {
		return nil, ps
	}
	if sps := schema.Named("outline").ValidateNode(doc); len(sps) > 0 {
		return nil, workspace.FromSchema(rel, sps)
	}
	var f struct {
		Sections []outline.Section `json:"sections"`
	}
	catalog.Decode(doc, &f)
	return &outline.Outline{Path: rel, Sections: f.Sections}, nil
}

// Run 对单元 u（scene 单元或任意范围的单元）执行确定性连续性检查，返回按场景先序排列的 findings。
// 台账或设定集无法加载（error 级）时，相关检查以原问题作为 findings 返回；不修改任何文件。
func Run(root string, w *work.Work, u *work.Unit) ([]Finding, []workspace.Problem) {
	scenes := scenesIn(w, u)
	// 设定集与各台账每命令加载一次（workspace-artifacts/load-once-per-command）。
	scoped, scps := canon.Resolve(root, w)
	canonFatal := scoped == nil && hasError(scps)
	cps := scps
	var canonEntries []canon.Entry
	if !canonFatal {
		canonEntries = make([]canon.Entry, len(scoped))
		for i, se := range scoped {
			canonEntries[i] = se.Entry
		}
	}
	stateEvents, sps := state.List(root, w, true)
	stateFatal := stateEvents == nil && hasError(sps)
	knowEvents, kps := knowledge.List(root, w, true)
	knowFatal := knowEvents == nil && hasError(kps)
	threadItems, tps := threads.List(root, w, true)
	threadFatal := threadItems == nil && hasError(tps)
	ds, _, dps := decision.Load(root)
	causality := outline.Causality(root, w.Scenes())
	// 时间线检查（只对声明了 time/after 的作品启用）：unknown-scene / cycle / order-conflict /
	// same-time-presence，与既有发现共用豁免通道。
	var allWorkIDs []string
	if entries, err := os.ReadDir(filepath.Join(root, filepath.FromSlash(workspace.WorksPath))); err == nil {
		for _, e := range entries {
			if e.IsDir() && !strings.HasPrefix(e.Name(), ".") {
				allWorkIDs = append(allWorkIDs, e.Name())
			}
		}
	}
	var timelineProblems []workspace.Problem
	if timeline.HasDeclaration(root, w) {
		tw := []*timeline.Work{{W: w}}
		if sid, _, ok := seriesOfForTimeline(root, w); ok {
			tw[0].SeriesID = sid
		}
		ordered, tops := timeline.Order(root, tw)
		timelineProblems = tops
		var canonFlat map[string]canon.Entry
		if scopedFlat, sps2 := canon.Resolve(root, w); sps2 == nil {
			canonFlat = map[string]canon.Entry{}
			for _, se := range scopedFlat {
				canonFlat[se.ID] = se.Entry
			}
		}
		timelineProblems = append(timelineProblems, timeline.SameTimePresence(ordered, root, tw, canonFlat)...)
	}
	// 伏笔交叉检查每命令计算一次，按场景大纲路径过滤（workspace-artifacts/load-once-per-command）。
	var contractProblems []workspace.Problem
	if !threadFatal {
		contractProblems = threads.ContractProblems(root, w, w.Scenes())
	}

	var out []Finding
	for _, s := range scenes {
		dir := w.UnitDir(s)
		ol, ops := sceneOutline(root, dir)
		// 触发条件先行：只有轻量契约（characters、beats、reveal）或没有大纲的场景跳过全部检查，
		// 不出现在输出中（既有文体验收行为不变）。
		if !ExtendedContract(ol) {
			continue
		}
		if len(ops) > 0 {
			out = append(out, Finding{Scene: s.Ref, Code: CodeDanglingReference, Severity: "error", Message: firstMessage(ops)})
			continue
		}
		if canonFatal {
			out = append(out, Finding{Scene: s.Ref, Code: CodeDanglingReference, Severity: "error", Message: firstMessage(cps)})
			continue
		}
		if stateFatal {
			out = append(out, Finding{Scene: s.Ref, Code: CodeDanglingReference, Severity: "error", Message: "状态台账无法加载，无法检查状态快照：" + firstMessage(sps)})
			continue
		}
		if knowFatal {
			out = append(out, Finding{Scene: s.Ref, Code: CodeDanglingReference, Severity: "error", Message: "知情表无法加载，无法检查知情矛盾：" + firstMessage(kps)})
			continue
		}
		// (1) 出场称谓解析与 (3) 多名一卡。
		names, pov := declaredNames(ol)
		resolved := map[string][]canon.Entry{} // 称谓 → 命中的 character 条目
		for _, n := range names {
			resolved[n] = matchCharacters(canonEntries, n)
		}
		seenEntry := map[string]string{} // 条目 id → 首个称谓
		for _, n := range names {
			hits := resolved[n]
			if len(hits) == 0 {
				out = append(out, Finding{Scene: s.Ref, Code: CodeUnknownCharacter, Severity: "error",
					Message: fmt.Sprintf("出场人物「%s」在设定集（canon.yaml）中没有同名 character 条目；用 wa-cli canon add 登记人物卡（Character Sheet）后再出场", n)})
				continue
			}
			if len(hits) > 1 {
				continue // 同名多条目：与 recall 的多义不解析一致，不判
			}
			id := hits[0].ID
			if first, ok := seenEntry[id]; ok && first != n {
				out = append(out, Finding{Scene: s.Ref, Code: CodeAliasMismatch, Severity: "error",
					Message: fmt.Sprintf("同一场景用两个称谓（%s 与 %s）指同一设定条目 %s；请统一称谓或在设定集中合并条目", first, n, id)})
			} else if !ok {
				seenEntry[id] = n
			}
		}
		_ = pov
		// (2) 保留词终态出场。
		if !stateFatal {
			for _, n := range names {
				hits := resolved[n]
				if len(hits) != 1 {
					continue
				}
				if st := latestConfirmedState(stateEvents, w, hits[0].ID, s); st != "" && slices.Contains(TerminalStates, st) {
					out = append(out, Finding{Scene: s.Ref, Code: CodeDeadActing, Severity: "error",
						Message: fmt.Sprintf("出场人物「%s」（%s）的最新已确认状态为「%s」，仍在该场景出场；请修正正文与状态，或经作者决策豁免", n, hits[0].ID, st)})
				}
			}
		}
		// (4) 悬空引用：绑定该场景的台账条目（warning 级，不阻塞验收，cross-artifact-references）、该场景
		// 收口记录的问题（error）与大纲交叉问题（error）。
		if canonEntries != nil {
			idx := canonIndexOnce(canonEntries)
			for _, e := range stateEvents {
				if e.Scene != s.Ref {
					continue
				}
				if _, ok := idx[e.Entry]; !ok {
					out = append(out, Finding{Scene: s.Ref, Code: CodeDanglingReference, Severity: "warning",
						Message: fmt.Sprintf("状态台账条目 %s（绑定本场景）引用的设定条目 %s 不在设定集中；请修正条目或补录设定", e.ID, e.Entry)})
				}
			}
		}
		if canonEntries != nil {
			idx := canonIndexOnce(canonEntries)
			for _, e := range knowEvents {
				if e.Scene != s.Ref {
					continue
				}
				if _, ok := idx[e.Entry]; !ok || (e.Character != "reader" && !isCharacterEntry(idx, e.Character)) {
					out = append(out, Finding{Scene: s.Ref, Code: CodeDanglingReference, Severity: "warning",
						Message: fmt.Sprintf("知情台账条目 %s（绑定本场景）的 entry 或 character 引用在设定集中失效；请修正条目", e.ID)})
				}
			}
		}
		if _, cps2 := closeout.Load(root, dir); cps2 != nil {
			for _, p := range cps2 {
				if p.Severity == workspace.SeverityError {
					out = append(out, toFinding(s.Ref, p))
				}
			}
		}
		for _, p := range contractProblems {
			if p.Severity == workspace.SeverityError && p.Path == outline.Path(dir) {
				out = append(out, toFinding(s.Ref, p))
			}
		}
		for _, p := range causality {
			if p.Severity == workspace.SeverityError && p.Path == outline.Path(dir) {
				out = append(out, toFinding(s.Ref, p))
			}
		}
		// (5) 伏笔超期。
		if !threadFatal {
			recovers := map[string]bool{}
			if ol != nil {
				for _, sec := range ol.Sections {
					for _, id := range sec.Recovers {
						recovers[id] = true
					}
				}
			}
			for _, it := range threadItems {
				related := it.PlantedIn == s.Ref || it.PlannedRecover == s.Ref || recovers[it.ID]
				if related && it.Status == threads.StatusOpen && it.Overdue {
					out = append(out, Finding{Scene: s.Ref, Code: CodeThreadAnomaly, Severity: "error",
						Message: fmt.Sprintf("伏笔 %s（%s）已超过计划回收位置 %s 仍未回收；请回收（wa-cli threads set）或调整计划", it.ID, it.Text, it.PlannedRecover)})
				}
			}
		}
	}
	// 时间线发现归入对应场景（message 中的场景引用），无场景可归属时归入首个声明场景。
	for _, p := range timelineProblems {
		// 单作品检查跳过跨作品 after 引用的 unknown-scene（引用属于系列内另一作品，由系列范围检查报告）。
		if p.Code == "timeline.unknown-scene" {
			cross := false
			for _, other := range allWorkIDs {
				if other != w.ID && strings.Contains(p.Message, other+"/") {
					cross = true
					break
				}
			}
			if cross {
				continue
			}
		}
		scene := ""
		for _, s := range scenes {
			if strings.Contains(p.Message, s.Ref) {
				scene = s.Ref
				break
			}
		}
		if scene == "" && len(scenes) > 0 {
			scene = scenes[0].Ref
		}
		if scene == "" {
			continue
		}
		out = append(out, Finding{Scene: scene, Code: p.Code, Severity: "error",
			Message: p.Message + "；请用 wa-cli scene contract --time/--after 修正时间声明，或经作者决策豁免"})
	}
	// 豁免标注。
	waived := map[string]bool{}
	for _, d := range ds {
		if d.Status == decision.StatusDecided && d.Choice != nil && *d.Choice == "a" && strings.HasPrefix(d.Question, "是否豁免场景 ") {
			waived[d.Question] = true
		}
	}
	for i := range out {
		if waived[Question(out[i].Scene, out[i].Code, firstSentence(out[i].Message))] {
			out[i].Waived = true
		}
	}
	var fatal []workspace.Problem
	for _, ps := range [][]workspace.Problem{cps, sps, kps, tps, dps} {
		for _, p := range ps {
			if p.Severity == workspace.SeverityError {
				fatal = append(fatal, p)
			}
		}
	}
	return out, fatal
}

// Blockers 只读地计算场景 u 的连续性阻塞项（不生成任何决策），供 review show 等只读命令复用；
// 台账无法加载时按原问题阻塞（偏安全）。
func Blockers(root string, w *work.Work, u *work.Unit) ([]workspace.Problem, []workspace.Problem) {
	if u.Type != outline.SceneType {
		return nil, nil
	}
	findings, fatal := Run(root, w, u)
	if len(fatal) > 0 {
		return fatal, nil
	}
	ds, _, dps := decision.Load(root)
	if dps != nil {
		return nil, dps
	}
	unitDir := w.UnitDir(u)
	var blockers []workspace.Problem
	for _, f := range findings {
		if f.Severity != "error" {
			continue
		}
		q := Question(f.Scene, f.Code, firstSentence(f.Message))
		state, existing := waiveState(ds, q, w.ID)
		switch state {
		case "waived":
			continue
		case "pending", "kept":
			blockers = append(blockers, workspace.Problem{Code: f.Code, Severity: workspace.SeverityError,
				Path: outline.Path(unitDir), Message: f.Message + "；" + waiveNote(state, existing)})
		default:
			blockers = append(blockers, workspace.Problem{Code: f.Code, Severity: workspace.SeverityError,
				Path: outline.Path(unitDir), Message: f.Message})
		}
	}
	return blockers, nil
}

// EnsureWaivers 为场景 u 未豁免的 error 级发现幂等地生成豁免决策：返回实际变更的路径（生成了决策时为
// 决策队列路径）；决策队列无法加载时不追加并返回其诊断。由 accept 的阻塞路径调用（只读命令不得调用）。
func EnsureWaivers(root string, w *work.Work, u *work.Unit) ([]string, []workspace.Problem) {
	if u.Type != outline.SceneType {
		return nil, nil
	}
	findings, fatal := Run(root, w, u)
	if len(fatal) > 0 {
		return nil, nil
	}
	ds, _, dps := decision.Load(root)
	if dps != nil {
		return nil, dps
	}
	changed := []string{}
	for _, f := range findings {
		if f.Severity != "error" {
			continue
		}
		q := Question(f.Scene, f.Code, firstSentence(f.Message))
		if state, _ := waiveState(ds, q, w.ID); state != "none" {
			continue
		}
		if _, ps := decision.Add(root, decision.Input{
			Question:  q,
			Options:   []string{"允许", "保持不变"},
			Recommend: "b",
			Rationale: "连续性检查发现冲突；默认保持阻塞，等待作者决定是否豁免这一次",
			Blocking:  true,
			Units:     []string{w.ID},
		}, func(ref string) []workspace.Problem { return nil }); len(ps) > 0 {
			return nil, ps
		}
		changed = append(changed, decision.Path)
		ds, _, _ = decision.Load(root)
	}
	dedupe(&changed)
	return changed, nil
}

// AcceptanceBlockers 计算场景 u 的连续性阻塞项，并为未豁免的（场景， 错误码）幂等地生成豁免决策：
// 返回阻塞项与实际变更的路径（生成了决策时为决策队列路径）。决策队列无法加载时不追加并返回其诊断。
func AcceptanceBlockers(root string, w *work.Work, u *work.Unit) ([]workspace.Problem, []string, []workspace.Problem) {
	if u.Type != outline.SceneType {
		return nil, nil, nil
	}
	findings, fatal := Run(root, w, u)
	if len(fatal) > 0 {
		// 台账无法加载：不生成决策，按原问题报告（调用方决定如何阻塞）。
		return nil, nil, fatal
	}
	ds, _, dps := decision.Load(root)
	if dps != nil {
		return nil, nil, dps
	}
	var blockers []workspace.Problem
	unitDir := w.UnitDir(u)
	changed := []string{}
	for _, f := range findings {
		if f.Severity != "error" {
			continue
		}
		q := Question(f.Scene, f.Code, firstSentence(f.Message))
		state, existing := waiveState(ds, q, w.ID)
		switch state {
		case "waived":
			continue
		case "pending", "kept":
			blockers = append(blockers, workspace.Problem{Code: f.Code, Severity: workspace.SeverityError,
				Path: outline.Path(unitDir), Message: f.Message + "；" + waiveNote(state, existing)})
			continue
		}
		if _, ps := decision.Add(root, decision.Input{
			Question:  q,
			Options:   []string{"允许", "保持不变"},
			Recommend: "b",
			Rationale: "连续性检查发现冲突；默认保持阻塞，等待作者决定是否豁免这一次",
			Blocking:  true,
			Units:     []string{w.ID},
		}, func(ref string) []workspace.Problem { return nil }); len(ps) > 0 {
			return nil, nil, ps
		}
		changed = append(changed, decision.Path)
		ds, _, _ = decision.Load(root)
		blockers = append(blockers, workspace.Problem{Code: f.Code, Severity: workspace.SeverityError,
			Path: outline.Path(unitDir), Message: f.Message + "；已生成豁免决策，等待作者决定"})
	}
	dedupe(&changed)
	return blockers, changed, nil
}

// waiveState 判定固定问题文本 q 的豁免状态：waived（作者已允许）、pending（已有 open 决策）、
// kept（作者已选择保持不变）或 none（尚无决策，返回该决策 id 供提示）。
func waiveState(ds []decision.Decision, q, workID string) (state string, existing string) {
	for _, d := range ds {
		if d.Question != q || !slices.Contains(d.Units, workID) {
			continue
		}
		switch {
		case d.Status == decision.StatusOpen:
			return "pending", d.ID
		case d.Status == decision.StatusDecided && d.Choice != nil && *d.Choice == "a":
			return "waived", d.ID
		default:
			return "kept", d.ID
		}
	}
	return "none", ""
}

func waiveNote(state, id string) string {
	switch state {
	case "pending":
		return "豁免决策 " + id + " 已在队列中，等待作者决定"
	case "kept":
		return "作者已对豁免决策 " + id + " 选择保持不变，请修改正文或状态"
	}
	return ""
}

func dedupe(paths *[]string) {
	out := (*paths)[:0]
	for _, p := range *paths {
		if !slices.Contains(out, p) {
			out = append(out, p)
		}
	}
	*paths = out
}

// scenesIn 返回 u 子树中的全部场景（u 为 scene 时只含自身），按先序。
func scenesIn(w *work.Work, u *work.Unit) []*work.Unit {
	var out []*work.Unit
	for _, x := range w.Units() {
		if x.Type != outline.SceneType {
			continue
		}
		for p := x; p != nil; p = p.Parent {
			if p == u {
				out = append(out, x)
				break
			}
		}
	}
	return out
}

// ExtendedContract 报告场景大纲是否声明了长篇场景契约扩展字段（pov、location、state_changes、plants、
// recovers、causes、events 任一）：连续性检查只对这类场景启用；只有轻量契约（characters、beats、reveal）
// 的场景与既有文体的行为不变。
func ExtendedContract(ol *outline.Outline) bool {
	if ol == nil {
		return false
	}
	for _, sec := range ol.Sections {
		if sec.Pov != "" || sec.Location != "" || len(sec.StateChanges) > 0 || len(sec.Plants) > 0 ||
			len(sec.Recovers) > 0 || len(sec.Causes) > 0 || len(sec.Events) > 0 {
			return true
		}
	}
	return false
}

// declaredNames 返回大纲各节 characters（按出现顺序去重）与首个非空 pov（并入末尾）。
func declaredNames(ol *outline.Outline) (names []string, pov string) {
	if ol == nil {
		return nil, ""
	}
	seen := map[string]bool{}
	add := func(n string) {
		n = strings.TrimSpace(n)
		if n != "" && !seen[n] {
			seen[n] = true
			names = append(names, n)
		}
	}
	for _, sec := range ol.Sections {
		for _, c := range sec.Characters {
			add(c)
		}
		if pov == "" && sec.Pov != "" {
			pov = sec.Pov
		}
	}
	if pov != "" {
		add(pov)
	}
	return names, pov
}

// matchCharacters 返回 name 或别名与称谓完全相等的 character 条目。
func matchCharacters(entries []canon.Entry, name string) []canon.Entry {
	var out []canon.Entry
	for _, e := range entries {
		if e.Type != canon.TypeCharacter {
			continue
		}
		for _, n := range e.Names() {
			if n == name {
				out = append(out, e)
				break
			}
		}
	}
	return out
}

// latestConfirmedState 返回条目 entry 在场景 s 开始时最后生效的已确认状态描述（无则空串）。
func latestConfirmedState(events []state.Event, w *work.Work, entry string, s *work.Unit) string {
	pos := map[string]int{}
	for i, x := range w.Units() {
		pos[x.Ref] = i
	}
	best := ""
	for _, e := range events {
		at, ok := pos[e.Scene]
		if !ok {
			continue // 绑定场景解析不了的事件不参与（其引用问题由整体校验报告）
		}
		if e.Entry != entry || e.Status != state.StatusConfirmed || at >= pos[s.Ref] {
			continue
		}
		best = e.State
	}
	return best
}

// canonIndexOnce 包装 canonIndex（调用方在同一场景内复用同一索引）。
func canonIndexOnce(entries []canon.Entry) map[string]canon.Entry { return canonIndex(entries) }

// canonIndex 返回设定条目按 id 的索引。
func canonIndex(entries []canon.Entry) map[string]canon.Entry {
	out := make(map[string]canon.Entry, len(entries))
	for _, e := range entries {
		out[e.ID] = e
	}
	return out
}

// isCharacterEntry 报告 id 是否为 character 条目。
func isCharacterEntry(idx map[string]canon.Entry, id string) bool {
	e, ok := idx[id]
	return ok && e.Type == canon.TypeCharacter
}

// toFinding 把各包的既有问题转为该场景的悬空引用发现：错误码统一为 continuity.dangling-reference
// （豁免键随之统一），message 保留原错误码与位置。
func toFinding(scene string, p workspace.Problem) Finding {
	return Finding{Scene: scene, Code: CodeDanglingReference, Severity: "error",
		Message: fmt.Sprintf("%s（%s：%s）", p.Message, p.Code, p.Field)}
}

func firstMessage(ps []workspace.Problem) string {
	for _, p := range ps {
		if p.Severity == workspace.SeverityError {
			return fmt.Sprintf("%s（%s）", p.Message, p.Code)
		}
	}
	if len(ps) > 0 {
		return ps[0].Message
	}
	return ""
}

func hasError(ps []workspace.Problem) bool {
	return slices.ContainsFunc(ps, func(p workspace.Problem) bool { return p.Severity == workspace.SeverityError })
}

func firstSentence(msg string) string {
	if i := strings.IndexAny(msg, "。；"); i >= 0 {
		return msg[:i]
	}
	return msg
}

// seriesOfForTimeline 返回作品所属系列 id（时间线跨作品默认序用）；continuity 不能 import series，
// 以目录扫描 series.yaml 的 works 判定成员关系。
func seriesOfForTimeline(root string, w *work.Work) (string, string, bool) {
	entries, err := os.ReadDir(filepath.Join(root, filepath.FromSlash(".writing-anything/series")))
	if err != nil {
		return "", "", false
	}
	for _, e := range entries {
		if !e.IsDir() || strings.HasPrefix(e.Name(), ".") {
			continue
		}
		rel := ".writing-anything/series/" + e.Name() + ".yaml"
		if doc, ps := workspace.Read(root, rel); doc != nil && len(ps) == 0 {
			var f struct {
				Works []string `json:"works"`
			}
			catalog.Decode(doc, &f)
			for _, id := range f.Works {
				if id == w.ID {
					return e.Name(), "", true
				}
			}
		}
	}
	return "", "", false
}

// RunSeries 对系列（或多作品集合）执行跨作品连续性检查（series-continuity-review 主规格）：合并成员作品的
// 场景集合（按 works 顺序），在合并集合上执行时间线检查、系列级设定解析与跨作品状态矛盾（conflicting-state）；
// 发现归属 message 中提及的场景（跨双作品的归先序更早作品的场景），message 标明涉及的作品。单作品调用
// （works 长度为 1）与 Run 的既有行为一致。
func RunSeries(root string, ws []*work.Work) ([]Finding, []workspace.Problem) {
	if len(ws) == 0 {
		return []Finding{}, nil
	}
	if len(ws) == 1 {
		return Run(root, ws[0], ws[0].Root)
	}
	tws := make([]*timeline.Work, len(ws))
	for i, w := range ws {
		tws[i] = &timeline.Work{W: w}
	}
	ordered, tops := timeline.Order(root, tws)
	var out []Finding
	for _, p := range tops {
		scene := firstMentionedScene(p.Message, ws)
		if scene == "" {
			continue
		}
		out = append(out, Finding{Scene: scene, Code: p.Code, Severity: "error",
			Message: "[系列] " + p.Message + "；请用 wa-cli scene contract --time/--after 修正时间声明，或经作者决策豁免"})
	}
	// 同 time 双出场（跨作品词典：各作品解析视图的并集，作品级优先）。
	flat := map[string]canon.Entry{}
	for _, w := range ws {
		if scoped, sps := canon.Resolve(root, w); sps == nil {
			for _, se := range scoped {
				if _, ok := flat[se.ID]; !ok {
					flat[se.ID] = se.Entry
				}
			}
		}
	}
	for _, p := range timeline.SameTimePresence(ordered, root, tws, flat) {
		scene := firstMentionedScene(p.Message, ws)
		if scene == "" {
			continue
		}
		out = append(out, Finding{Scene: scene, Code: p.Code, Severity: "error",
			Message: "[系列] " + p.Message + "；请修正时间标记或补充 after，或经作者决策豁免"})
	}
	// 跨作品状态矛盾：同一设定条目在多部作品的最后生效 confirmed 状态不同。
	perWork := map[string]map[string]string{} // work id → entry id → last confirmed state
	sceneOf := map[string]map[string]string{} // work id → entry id → 场景
	for _, w := range ws {
		events, ps := state.List(root, w, true)
		if events == nil && len(ps) > 0 {
			continue
		}
		perWork[w.ID] = map[string]string{}
		sceneOf[w.ID] = map[string]string{}
		for _, e := range events {
			if e.Status != state.StatusConfirmed {
				continue
			}
			perWork[w.ID][e.Entry] = e.State
			sceneOf[w.ID][e.Entry] = e.Scene
		}
	}
	seen := map[string]bool{}
	for i, wi := range ws {
		for j, wj := range ws {
			if i >= j {
				continue
			}
			for entry, si := range perWork[wi.ID] {
				sj, ok := perWork[wj.ID][entry]
				if !ok || sj == si || seen[entry+"\x00"+wi.ID+"\x00"+wj.ID] {
					continue
				}
				seen[entry+"\x00"+wi.ID+"\x00"+wj.ID] = true
				sc, ok := sceneOf[wi.ID][entry]
				if !ok || sc == "" {
					continue
				}
				out = append(out, Finding{Scene: sc, Code: CodeConflictingState, Severity: "error",
					Message: fmt.Sprintf("[系列] 设定条目 %s 在作品 %s（场景 %s）确认为「%s」，在作品 %s（场景 %s）确认为「%s」——两部作品对该条目的状态矛盾；请作者裁决正史或修正其中一部",
						entry, wi.ID, sc, si, wj.ID, sceneOf[wj.ID][entry], sj)})
			}
		}
	}
	return out, nil
}

// firstMentionedScene 返回 message 中第一个被提及的（属于给定作品的）场景引用；两作品都提及时归先序更早
// 作品的场景（按 ws 顺序扫描）。
func firstMentionedScene(message string, ws []*work.Work) string {
	for _, w := range ws {
		for _, u := range w.Units() {
			if u.Type != outline.SceneType {
				continue
			}
			if strings.Contains(message, u.Ref) {
				return u.Ref
			}
		}
	}
	return ""
}

// CodeConflictingState 是跨作品状态矛盾的错误码。
const CodeConflictingState = "continuity.conflicting-state"
