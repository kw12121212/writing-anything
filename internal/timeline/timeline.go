// Package timeline 实现时间线（Timeline）：从场景大纲的 time（故事时间标记）与 after（相对顺序声明）
// 推导场景全序（Kahn 拓扑排序 + 单元树先序/系列 works 顺序稳定选择），检查 after 成环、引用不存在、声明与
// 推导序矛盾，以及同一人物在相同 time 的两个场景出场（同时两地的确定性近似）。时间线与状态快照、因果链共用
// 场景与事件 id（场景为事件的宿主，场景有全序则事件有序），不引入独立的事件存储。本包不 import internal/cli。
package timeline

import (
	"fmt"
	"slices"
	"strings"

	"github.com/kw12121212/writing-anything/internal/canon"
	"github.com/kw12121212/writing-anything/internal/catalog"
	"github.com/kw12121212/writing-anything/internal/outline"
	"github.com/kw12121212/writing-anything/internal/schema"
	"github.com/kw12121212/writing-anything/internal/work"
	"github.com/kw12121212/writing-anything/internal/workspace"
)

// 时间线的错误码（Error Code）。已发布的错误码不得改名或复用。
const (
	CodeCycle         = "timeline.cycle"
	CodeUnknownScene  = "timeline.unknown-scene"
	CodeOrderConflict = "timeline.order-conflict"
	CodeSameTime      = "timeline.same-time-presence"
)

func errorAt(code, field, msg string) workspace.Problem {
	return workspace.Problem{Code: code, Severity: workspace.SeverityError, Field: field, Message: msg}
}

// Entry 是一个场景的时间线声明。
type Entry struct {
	Unit     string   `json:"unit"`
	Time     *string  `json:"time"`
	After    []string `json:"after"`
	Position int      `json:"position"`
}

// Work 是参与时间线的一个作品：其场景（默认序）与所属系列（决定跨作品默认序）。
type Work struct {
	W        *work.Work
	SeriesID string
}

// Declaration 是从大纲节收集的声明。
type Declaration struct {
	Unit       string
	Time       string
	After      []string
	Characters []string
}

// collect 从作品 w 的全部场景大纲收集 time/after 声明（合并各节：time 取首个非空、after 并集去重、
// characters 并集去重）。defaultOrder 给出场景的默认序（单元树先序）。
func collect(root string, w *work.Work) (map[string]*Declaration, []string) {
	decls := map[string]*Declaration{}
	var order []string
	for _, u := range w.Units() {
		if u.Type != outline.SceneType {
			continue
		}
		order = append(order, u.Ref)
		rel := outline.Path(w.UnitDir(u))
		if _, ok, _ := workspace.ReadRegular(root, rel); !ok {
			continue
		}
		doc, ps := workspace.Read(root, rel)
		if doc == nil || len(ps) > 0 {
			continue
		}
		if sps := schema.Named("outline").ValidateNode(doc); len(sps) > 0 {
			continue
		}
		var f struct {
			Sections []outline.Section `json:"sections"`
		}
		catalog.Decode(doc, &f)
		d := &Declaration{Unit: u.Ref, After: []string{}, Characters: []string{}}
		for _, sec := range f.Sections {
			if d.Time == "" && sec.Time != "" {
				d.Time = sec.Time
			}
			for _, a := range sec.After {
				if !slices.Contains(d.After, a) {
					d.After = append(d.After, a)
				}
			}
			for _, c := range sec.Characters {
				if !slices.Contains(d.Characters, c) {
					d.Characters = append(d.Characters, c)
				}
			}
		}
		decls[u.Ref] = d
	}
	return decls, order
}

// resolveRef 解析 after 引用：同作品的场景名补全为 <work>/<scene>，跨作品引用（已含作品段）原样。
func resolveRef(curWork, ref, scene string) string {
	if strings.Contains(ref, "/") {
		return ref
	}
	return curWork + "/" + ref
}

// Order 推导多个作品的合并全序：defaultOrder 为各作品场景按 works 顺序拼接的默认序；decls 键为
// <work>/<scene> 的场景引用。返回按序排列的 Entry 与问题（cycle / unknown-scene / order-conflict）。
func Order(root string, ws []*Work) ([]Entry, []workspace.Problem) {
	var problems []workspace.Problem
	decls := map[string]*Declaration{}
	var defaultOrder []string
	workOf := map[string]string{} // scene ref → work id
	for _, tw := range ws {
		d, order := collect(root, tw.W)
		for _, ref := range order {
			workOf[ref] = tw.W.ID
		}
		prefixLen := len(defaultOrder)
		defaultOrder = append(defaultOrder, order...)
		_ = prefixLen
		for ref, dd := range d {
			decls[ref] = dd
		}
	}
	// 解析 after 引用并校验存在性。
	edges := map[string][]string{} // 场景 → 它声明的 after（已解析）
	known := map[string]bool{}
	for _, ref := range defaultOrder {
		known[ref] = true
	}
	for _, ref := range defaultOrder {
		d := decls[ref]
		if d == nil {
			continue
		}
		for _, raw := range d.After {
			target := resolveRef(workOf[ref], raw, ref)
			if !known[target] {
				problems = append(problems, errorAt(CodeUnknownScene, "after",
					fmt.Sprintf("场景 %s 的 after 引用 %s 不存在（作品内场景或 <work>/<scene> 引用）", ref, raw)))
				continue
			}
			edges[ref] = append(edges[ref], target)
		}
	}
	// Kahn：入度为 0 的候选按 defaultOrder 稳定选择。
	rank := map[string]int{}
	for i, ref := range defaultOrder {
		rank[ref] = i
	}
	indeg := map[string]int{}
	dependents := map[string][]string{} // target → 依赖它的场景
	for ref, targets := range edges {
		for _, t := range targets {
			if t == ref {
				continue
			}
			indeg[ref]++
			dependents[t] = append(dependents[t], ref)
		}
	}
	var queue []string
	for _, ref := range defaultOrder {
		if indeg[ref] == 0 {
			queue = append(queue, ref)
		}
	}
	slices.SortStableFunc(queue, func(a, b string) int { return rank[a] - rank[b] })
	var ordered []string
	for len(queue) > 0 {
		next := queue[0]
		queue = queue[1:]
		ordered = append(ordered, next)
		fresh := []string{}
		for _, dep := range dependents[next] {
			indeg[dep]--
			if indeg[dep] == 0 {
				fresh = append(fresh, dep)
			}
		}
		queue = append(queue, fresh...)
		slices.SortStableFunc(queue, func(a, b string) int { return rank[a] - rank[b] })
	}
	if len(ordered) < len(defaultOrder) {
		var cycle []string
		for _, ref := range defaultOrder {
			if indeg[ref] > 0 {
				cycle = append(cycle, ref)
			}
		}
		shown := cycle
		if len(shown) > 5 {
			shown = append(append([]string{}, shown[:5]...), fmt.Sprintf("等 %d 项", len(cycle)))
		}
		problems = append(problems, errorAt(CodeCycle, "",
			"after 声明成环："+strings.Join(shown, "、")+"；请修正场景的 after 声明"))
		// 有环时退回默认序（检查降级，但命令仍可输出）。
		ordered = defaultOrder
	}
	// order-conflict：after 声明的目标在推导序中不早于本场景（除自环与未知引用）。
	pos := map[string]int{}
	for i, ref := range ordered {
		pos[ref] = i
	}
	for _, ref := range ordered {
		for _, t := range edges[ref] {
			if t == ref || !known[t] {
				continue
			}
			if pos[t] >= pos[ref] {
				problems = append(problems, errorAt(CodeOrderConflict, "after",
					fmt.Sprintf("场景 %s 声明晚于 %s，但推导序中 %s 不早于它（与其他 after 声明矛盾）", ref, t, t)))
			}
		}
	}
	out := make([]Entry, 0, len(ordered))
	for i, ref := range ordered {
		e := Entry{Unit: ref, After: []string{}, Time: nil, Position: i + 1}
		if d := decls[ref]; d != nil {
			e.After = d.After
			if d.Time != "" {
				t := d.Time
				e.Time = &t
			}
		}
		out = append(out, e)
	}
	return out, problems
}

// SameTimePresence 检查同一人物（解析到同一 canon 条目的称谓）在相同 time 的两个场景出场且两场景间
// 无 after 链分隔（BFS 可达性：任一方向可达即有序）。只对声明了 time 的场景参与；canonEntries 为 nil 时
// 按称谓原文比较。
func SameTimePresence(ordered []Entry, root string, ws []*Work, canonEntries map[string]canon.Entry) []workspace.Problem {
	nameToID := map[string]string{}
	for _, e := range canonEntries {
		for _, n := range e.Names() {
			nameToID[n] = e.ID
		}
	}
	decls := map[string]*Declaration{}
	for _, tw := range ws {
		d, _ := collect(root, tw.W)
		for ref, dd := range d {
			decls[ref] = dd
		}
	}
	// after 可达性（双向）。
	reach := map[string]map[string]bool{}
	var dfs func(from, cur string, seen map[string]bool)
	dfs = func(from, cur string, seen map[string]bool) {
		if seen[cur] {
			return
		}
		seen[cur] = true
		d := decls[cur]
		if d == nil {
			return
		}
		workID := workIDOf(ws, cur)
		for _, raw := range d.After {
			t := resolveRef(workID, raw, cur)
			if decls[t] != nil || slices.Contains(unitsOf(ws), t) {
				dfs(from, t, seen)
			}
		}
	}
	unitsAll := unitsOf(ws)
	for _, ref := range unitsAll {
		reach[ref] = map[string]bool{}
		dfs(ref, ref, reach[ref])
	}
	// 按 time 分组；同组内比较人物交集。
	byTime := map[string][]string{}
	for _, e := range ordered {
		if e.Time == nil {
			continue
		}
		d := decls[e.Unit]
		if d == nil || len(d.Characters) == 0 {
			continue
		}
		byTime[*e.Time] = append(byTime[*e.Time], e.Unit)
	}
	seen := map[string]bool{}
	var problems []workspace.Problem
	for _, refs := range byTime {
		for i := 0; i < len(refs); i++ {
			for j := i + 1; j < len(refs); j++ {
				a, b := refs[i], refs[j]
				if reach[a][b] || reach[b][a] {
					continue
				}
				da, db := decls[a], decls[b]
				for _, ca := range da.Characters {
					for _, cb := range db.Characters {
						same := ca == cb
						if !same && nameToID[ca] != "" && nameToID[ca] == nameToID[cb] {
							same = true
						}
						if !same {
							continue
						}
						key := fmt.Sprintf("%s\x00%s\x00%s", a, b, ca)
						if seen[key] {
							continue
						}
						seen[key] = true
						problems = append(problems, errorAt(CodeSameTime, "",
							fmt.Sprintf("人物「%s」在两个标记为相同故事时间（%s）的场景 %s 与 %s 同时出场，且两场景之间没有 after 顺序声明；请修正时间标记或补充 after", ca, declsOf(decls, a).Time, a, b)))
					}
				}
			}
		}
	}
	return problems
}

func declsOf(decls map[string]*Declaration, ref string) *Declaration { return decls[ref] }

func workIDOf(ws []*Work, ref string) string {
	for _, tw := range ws {
		for _, u := range tw.W.Units() {
			if u.Ref == ref {
				return tw.W.ID
			}
		}
	}
	return ""
}

func unitsOf(ws []*Work) []string {
	var out []string
	for _, tw := range ws {
		for _, u := range tw.W.Units() {
			if u.Type == outline.SceneType {
				out = append(out, u.Ref)
			}
		}
	}
	return out
}

// HasDeclaration 报告作品任一场景声明了 time 或 after（continuity 只对声明者启用时间线检查）。
func HasDeclaration(root string, w *work.Work) bool {
	decls, _ := collect(root, w)
	for _, d := range decls {
		if d.Time != "" || len(d.After) > 0 {
			return true
		}
	}
	return false
}
