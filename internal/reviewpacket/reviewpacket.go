// Package reviewpacket 实现审查任务包（Review Task Packet）：wa-cli review plan 按五个维度（人物一致性、
// 伏笔回收质量、因果与动机、视角与揭示边界、文风与情绪节奏）把全书语义审查拆成不超过上下文预算的小任务
// （一段正文切片、相关台账切片与固定检查清单），写入 .writing-anything/packets/<work>/；结论经
// wa-cli review submit 校验齐全后写回；wa-cli review report 只读汇总。任务包由接入的 Agent 调度执行，
// wa-cli 不调用大语言模型（LLM）、不启动 Agent。本包不 import internal/cli；只返回结构化的问题。
package reviewpacket

import (
	"fmt"
	"maps"
	"os"
	"path/filepath"
	"slices"
	"strings"
	"time"
	"unicode"
	"unicode/utf8"

	"github.com/kw12121212/writing-anything/internal/canon"
	"github.com/kw12121212/writing-anything/internal/catalog"
	"github.com/kw12121212/writing-anything/internal/draft"
	"github.com/kw12121212/writing-anything/internal/knowledge"
	"github.com/kw12121212/writing-anything/internal/metrics"
	"github.com/kw12121212/writing-anything/internal/outline"
	"github.com/kw12121212/writing-anything/internal/schema"
	"github.com/kw12121212/writing-anything/internal/series"
	"github.com/kw12121212/writing-anything/internal/state"
	"github.com/kw12121212/writing-anything/internal/threads"
	"github.com/kw12121212/writing-anything/internal/timeline"
	"github.com/kw12121212/writing-anything/internal/work"
	"github.com/kw12121212/writing-anything/internal/workspace"
	"go.yaml.in/yaml/v3"
)

// 审查任务包的错误码（Error Code）。已发布的错误码不得改名或复用。
const (
	CodeMissingVerdict = "packet.missing-verdict"
	CodeUnknownItem    = "packet.unknown-item"
	CodeNotFound       = "packet.not-found"
	CodeAlreadyDone    = "packet.already-done"
	CodeInvalid        = "packet.invalid"
	CodeOverBudget     = "packet.over-budget"
)

// Dir 是任务包根目录（相对项目目录）；PacketsPath 返回某作品的包目录。
const Dir = ".writing-anything/packets"

// 维度。
const (
	DimCharacter = "character"
	DimThread    = "thread"
	DimCausality = "causality"
	DimPovReveal = "pov-reveal"
	DimStyle     = "style"
)

// Dimensions 是全部维度（也是 id 前缀）。
var Dimensions = []string{DimCharacter, DimThread, DimCausality, DimPovReveal, DimStyle}

// ChecklistItem 是一项固定检查。
type ChecklistItem struct {
	ID       string `json:"id"`
	Question string `json:"question"`
}

// Slice 是一段台账或数据切片。rank 是装配预算时的相关度等级（越小越相关，不写入产物）。
type Slice struct {
	Kind string `json:"kind"`
	Text string `json:"text"`
	rank int
}

// 切片相关度等级（packet-plan）：契约类切片最高；台账切片按所指场景与审查范围的先序距离计算；无法定位的最低。
const (
	rankContract   = 0
	rankUnlocated  = 1 << 30
	minExcerptSize = 100 // 有正文时最小可审片段的字符数上限
)

// Result 是一项结论。
type Result struct {
	Item    string `json:"item"`
	Verdict string `json:"verdict"`
	Note    string `json:"note,omitempty"`
}

// File 是任务包产物（packet.yaml）的解码形态。
type File = fileRecord

type fileRecord struct {
	SchemaVersion int    `json:"schema_version"`
	ID            string `json:"id"`
	Work          string `json:"work"`
	Dimension     string `json:"dimension"`
	Scope         string `json:"scope"`
	Subject       string `json:"subject"`
	Excerpt       struct {
		Text      string `json:"text"`
		Truncated bool   `json:"truncated"`
	} `json:"excerpt"`
	Slices        []Slice         `json:"slices"`
	OmittedSlices int             `json:"omitted_slices"`
	Checklist     []ChecklistItem `json:"checklist"`
	Status        string          `json:"status"`
	Results       []Result        `json:"results"`
	RecordedAt    *string         `json:"recorded_at"`
}

// Packet 是一个任务包的对外输出形态（生成、提交与报告共用；数组永不为 null）。
type Packet struct {
	ID            string   `json:"id"`
	Dimension     string   `json:"dimension"`
	Scope         string   `json:"scope"`
	Status        string   `json:"status"`
	Results       []Result `json:"results"`
	Chars         int      `json:"chars"`
	Truncated     bool     `json:"truncated"`
	OmittedSlices int      `json:"omittedSlices"`
}

// ViewOf 把提交后的任务包转换为输出形态（命令层使用）。
func ViewOf(f *File) Packet { return viewOf(f, 0) }

// viewOf 把产物形态转换为输出形态。
func viewOf(f *fileRecord, chars int) Packet {
	return Packet{ID: f.ID, Dimension: f.Dimension, Scope: f.Scope, Status: f.Status,
		Results: orEmptyResults(f.Results), Chars: chars, Truncated: f.Excerpt.Truncated, OmittedSlices: f.OmittedSlices}
}

func orEmptyResults(rs []Result) []Result {
	if rs == nil {
		return []Result{}
	}
	return rs
}

// checklists 是各维度的固定检查清单（packet-plan）。
var checklists = map[string][]ChecklistItem{
	DimCharacter: {
		{"behavior-state", "人物的言行与最新已确认状态一致吗？"},
		{"naming", "人物称谓与设定集（name 与别名）一致吗？"},
		{"knowledge-boundary", "人物言行未超出其已知信息吗？"},
		{"state-change", "状态变化的过渡在正文中可信吗？"},
	},
	DimThread: {
		{"plant-natural", "伏笔的埋设自然吗？"},
		{"payoff-earned", "回收兑现了埋设的期待吗？"},
		{"continuity", "跨场景的伏笔提示连贯吗？"},
	},
	DimCausality: {
		{"cause-holds", "场景起因在正文成立吗？"},
		{"event-produced", "契约声明的事件在正文产生了吗？"},
		{"motivation", "人物行动的动机可信吗？"},
	},
	DimPovReveal: {
		{"pov-consistent", "视角与人称保持一致吗？"},
		{"no-early-reveal", "正文未提前透露揭示边界吗？"},
		{"reader-knowledge", "读者已知的信息运用得当吗？"},
	},
	DimStyle: {
		{"style-consistent", "文风与画像基线一致吗？"},
		{"pacing-curve", "情绪节奏符合曲线目标吗？"},
		{"drift-check", "文风漂移发现项核实了吗？"},
	},
}

func errorAt(code, path, field, msg string) workspace.Problem {
	return workspace.Problem{Code: code, Severity: workspace.SeverityError, Path: path, Field: field, Message: msg}
}

// packetChars 是任务包的 chars：excerpt.text、每个切片（kind：text）与每个清单项（id：question）的字符数之和。
func packetChars(f *fileRecord) int {
	return utf8.RuneCountInString(f.Excerpt.Text) + slicesChars(f.Slices) + checklistChars(f.Checklist)
}

func sliceChars(s Slice) int { return utf8.RuneCountInString(s.Kind + "：" + s.Text) }

func slicesChars(ss []Slice) int {
	n := 0
	for _, s := range ss {
		n += sliceChars(s)
	}
	return n
}

func itemChars(c ChecklistItem) int { return utf8.RuneCountInString(c.ID + "：" + c.Question) }

func checklistChars(cs []ChecklistItem) int {
	n := 0
	for _, c := range cs {
		n += itemChars(c)
	}
	return n
}

// sceneOrder 返回作品场景的先序位置（场景引用 → 下标），用于计算台账切片的相关度。
func sceneOrder(w *work.Work) map[string]int {
	idx := map[string]int{}
	for _, x := range w.Units() {
		if x.Type == outline.SceneType {
			idx[x.Ref] = len(idx)
		}
	}
	return idx
}

// ledgerRank 返回所指场景 scene 相对审查范围（先序下标 [lo, hi]）的相关度等级：范围内为 1，距离越远等级越大，
// 范围之后的低于范围之前的同距离切片；场景无法定位或范围没有场景时为最低。
func ledgerRank(order map[string]int, lo, hi int, scene string) int {
	i, ok := order[scene]
	if !ok || lo < 0 {
		return rankUnlocated
	}
	switch {
	case i < lo:
		return 1 + 2*(lo-i)
	case i > hi:
		return 2 + 2*(i-hi)
	}
	return 1
}

// sceneRange 返回场景集合在先序中的下标范围；没有场景时 lo 为 -1。
func sceneRange(order map[string]int, scenes []*work.Unit) (lo, hi int) {
	lo, hi = -1, -1
	for _, s := range scenes {
		i, ok := order[s.Ref]
		if !ok {
			continue
		}
		if lo < 0 || i < lo {
			lo = i
		}
		if i > hi {
			hi = i
		}
	}
	return lo, hi
}

// fitBudget 按上下文预算装配任务包（packet-plan）：(1) 正文取扣除切片与清单后的剩余预算，有正文时至少保留
// min(100, 正文字符数)；(2) 仍超预算时按相关度从低到高逐条省略切片（同等级先省略靠后的）；(3) 清单与最小正文
// 仍超预算时按清单顺序拆分为多个同 dimension、同 scope 的任务包，各包分别执行 (1)(2)；(4) 单个清单项与最小
// 正文仍超预算时返回 packet.over-budget。fixed 为 true 时 base.Excerpt.Text 是不可裁的固定说明文字。
//
// 放不下时返回 nil 与所需的最小预算（单个清单项与最小正文片段之和的最大值）。
func fitBudget(base fileRecord, body string, fixed bool, budget int) ([]fileRecord, int) {
	body = strings.TrimLeftFunc(body, unicode.IsSpace) // 截断后的片段不得只剩空白
	full := body
	switch {
	case fixed:
		full = base.Excerpt.Text
	case strings.TrimSpace(body) == "":
		full = "（该范围还没有正文；待正文写入后重新 wa-cli review plan 生成本包）"
		fixed = true
	}
	minEx := utf8.RuneCountInString(full)
	if !fixed {
		minEx = min(minEx, minExcerptSize)
	}
	var groups [][]ChecklistItem
	var cur []ChecklistItem
	curChars, need := 0, 0
	for _, c := range base.Checklist {
		n := itemChars(c)
		need = max(need, minEx+n)
		if len(cur) > 0 && minEx+curChars+n > budget {
			groups = append(groups, cur)
			cur, curChars = nil, 0
		}
		cur = append(cur, c)
		curChars += n
	}
	if need > budget {
		return nil, need
	}
	groups = append(groups, cur)
	out := make([]fileRecord, 0, len(groups))
	for _, g := range groups {
		f := base
		f.Checklist = g
		f.Slices = fitSlices(base.Slices, budget-minEx-checklistChars(g))
		f.OmittedSlices = len(base.Slices) - len(f.Slices)
		if fixed {
			f.Excerpt.Text, f.Excerpt.Truncated = full, false
		} else {
			f.Excerpt.Text, f.Excerpt.Truncated = headN(body, budget-checklistChars(g)-slicesChars(f.Slices))
		}
		out = append(out, f)
	}
	return out, 0
}

// fitSlices 按相关度从低到高（同等级先省略靠后的）逐条省略切片，直到总字符数不超过 room；保留项维持原顺序。
func fitSlices(ss []Slice, room int) []Slice {
	total := slicesChars(ss)
	if total <= room {
		return slices.Clone(ss)
	}
	order := make([]int, len(ss))
	for i := range order {
		order[i] = i
	}
	slices.SortStableFunc(order, func(a, b int) int {
		if ss[a].rank != ss[b].rank {
			return ss[b].rank - ss[a].rank
		}
		return b - a
	})
	drop := map[int]bool{}
	for _, i := range order {
		if total <= room {
			break
		}
		drop[i] = true
		total -= sliceChars(ss[i])
	}
	kept := []Slice{}
	for i, s := range ss {
		if !drop[i] {
			kept = append(kept, s)
		}
	}
	return kept
}

// assembler 收集按预算装配后的任务包；放不下的包汇总为一条 packet.over-budget（message 含首个放不下的范围、
// 放不下的包数与全部包所需的最小预算），调用方在任何写入之前返回该问题（先预检后副作用）。
type assembler struct {
	budget  int
	packets []fileRecord
	over    *workspace.Problem
	need    int
	failed  int
}

func (a *assembler) add(f fileRecord, body string, fixed bool) {
	fs, need := fitBudget(f, body, fixed, a.budget)
	if need == 0 {
		a.packets = append(a.packets, fs...)
		return
	}
	a.failed++
	if a.over == nil {
		a.over = &workspace.Problem{Code: CodeOverBudget, Severity: workspace.SeverityError, Field: "context_budget"}
		a.over.Message = fmt.Sprintf("%s 维度范围 %s", f.Dimension, f.Scope)
	}
	a.need = max(a.need, need)
}

// problem 返回汇总后的 packet.over-budget；全部放得下时为 nil。
func (a *assembler) problem() []workspace.Problem {
	if a.over == nil {
		return nil
	}
	p := *a.over
	p.Message = fmt.Sprintf("%s 等 %d 个任务包放不下：单个检查清单项与最小正文片段至少需要 %d 字符，上下文预算只有 %d；请提高 context_budget 到不少于 %d 后重新运行 wa-cli review plan",
		p.Message, a.failed, a.need, a.budget, a.need)
	return []workspace.Problem{p}
}

// packetKey 是重 plan 时的匹配键：（dimension, scope, subject, 清单项 id 集合）。subject 区分同一章的不同人物包。
func packetKey(f *fileRecord) string {
	ids := make([]string, 0, len(f.Checklist))
	for _, c := range f.Checklist {
		ids = append(ids, c.ID)
	}
	return f.Dimension + "\x00" + f.Scope + "\x00" + f.Subject + "\x00" + strings.Join(ids, ",")
}

// chaptersIn 返回范围内的章（unit 为 nil 时为全部章）。
func chaptersIn(w *work.Work, u *work.Unit) []*work.Unit {
	var out []*work.Unit
	for _, x := range w.Units() {
		if x.Type != "chapter" {
			continue
		}
		if u != nil {
			inside := false
			for p := x; p != nil; p = p.Parent {
				if p == u {
					inside = true
					break
				}
			}
			if !inside {
				continue
			}
		}
		out = append(out, x)
	}
	return out
}

// scenesIn 返回范围内的场景（unit 为 nil 时为全部）。
func scenesIn(w *work.Work, u *work.Unit) []*work.Unit {
	var out []*work.Unit
	for _, x := range w.Units() {
		if x.Type != outline.SceneType {
			continue
		}
		if u != nil {
			inside := false
			for p := x; p != nil; p = p.Parent {
				if p == u {
					inside = true
					break
				}
			}
			if !inside {
				continue
			}
		}
		out = append(out, x)
	}
	return out
}

// sceneOutline 读取场景大纲节（复用 outline Schema 校验）。
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

// causeSceneTail 返回声明事件 ev 的场景引用与正文结尾（含场景引用），找不到声明场景或没有正文时为空。
func causeSceneTail(root string, w *work.Work, ev string) (string, string) {
	for _, x := range w.Units() {
		if x.Type != outline.SceneType {
			continue
		}
		ol, _ := sceneOutline(root, w.UnitDir(x))
		if ol == nil {
			continue
		}
		declared := false
		for _, sec := range ol.Sections {
			if slices.Contains(sec.Events, ev) {
				declared = true
				break
			}
		}
		if !declared {
			continue
		}
		if text, ok := draftOf(root, w, x); ok {
			return x.Ref, fmt.Sprintf("%s（声明 %s）的结尾：%s", x.Ref, ev, tailN(text, 200))
		}
	}
	return "", ""
}

// povKnowledgeSlices 返回视角人物的已知条目与读者已知人物未知的条目切片。
func povKnowledgeSlices(root string, w *work.Work, u *work.Unit, pov string, entriesByID map[string]canon.Entry, order map[string]int) []Slice {
	lo, hi := sceneRange(order, []*work.Unit{u})
	if pov == "" {
		return nil
	}
	var povID string
	for _, e := range entriesByID {
		if e.Type == canon.TypeCharacter && e.Name != nil && *e.Name == pov {
			povID = e.ID
			break
		}
	}
	if povID == "" {
		return nil
	}
	rep, ps := knowledge.For(root, w, u, entriesByID)
	if rep == nil && len(ps) > 0 {
		return nil
	}
	var out []Slice
	known := map[string]bool{}
	for _, it := range rep.Known(povID) {
		known[it.Entry] = true
		entry := it.Entry
		if c, ok := entriesByID[entry]; ok && c.Name != nil {
			entry = entry + "（" + *c.Name + "）"
		}
		out = append(out, Slice{Kind: "pov-knowledge", Text: fmt.Sprintf("视角人物已知：%s（获知于 %s）", entry, it.Scene), rank: ledgerRank(order, lo, hi, it.Scene)})
	}
	for _, it := range rep.Known("reader") {
		if known[it.Entry] {
			continue
		}
		entry := it.Entry
		if c, ok := entriesByID[entry]; ok && c.Name != nil {
			entry = entry + "（" + *c.Name + "）"
		}
		out = append(out, Slice{Kind: "reader-only", Text: fmt.Sprintf("读者已知人物未知：%s（获知于 %s）", entry, it.Scene), rank: ledgerRank(order, lo, hi, it.Scene)})
	}
	return out
}

func draftOf(root string, w *work.Work, u *work.Unit) (string, bool) {
	content, ok, ps := draft.Read(root, w, u)
	if len(ps) > 0 || !ok {
		return "", false
	}
	return string(content), true
}

func tailN(text string, n int) string {
	r := []rune(strings.TrimSpace(text))
	if len(r) > n {
		r = r[len(r)-n:]
	}
	return string(r)
}

func headN(text string, n int) (string, bool) {
	if utf8.RuneCountInString(text) <= n {
		return text, false
	}
	return string([]rune(text)[:n]), true
}

// Plan 生成任务包并写入 packets/<work>/：unit 为 nil 时范围为整个作品；dimension 为空时生成全部维度。
// 既有包按（dimension, scope, subject, 清单项集合）匹配：done 包保留；范围内的 open 包原位重建（内容随台账与
// 正文更新），范围外的包不动。返回生成的包、写入与删除的路径与问题。
func Plan(root string, w *work.Work, u *work.Unit, dimension string, budget int, now time.Time) ([]Packet, []string, []workspace.Problem) {
	if dimension != "" && !slices.Contains(Dimensions, dimension) {
		return nil, nil, []workspace.Problem{{Code: "input.invalid-value", Severity: workspace.SeverityError, Field: "dimension",
			Message: "维度 " + dimension + " 不存在；可用维度：character、thread、causality、pov-reveal、style"}}
	}
	dims := Dimensions
	if dimension != "" {
		dims = []string{dimension}
	}
	// 台账每命令加载一次。
	canonList, _ := canon.List(root, w)
	entriesByID := map[string]canon.Entry{}
	for _, e := range canonList {
		entriesByID[e.ID] = e
	}
	stateEvents, _ := state.List(root, w, true)
	knowEvents, _ := knowledge.List(root, w, true)
	threadItems, _ := threads.List(root, w, true)
	chapters := chaptersIn(w, u)
	scenes := scenesIn(w, u)
	order := sceneOrder(w)

	asm := &assembler{budget: budget}
	add := asm.add
	for _, dim := range dims {
		switch dim {
		case DimCharacter:
			for _, ch := range chapters {
				// 章 × 出场人物：解析到唯一设定条目的称谓。
				perCharacter := map[string][]string{} // canon id → 称谓
				chScenes := scenesIn(w, ch)
				for _, s := range chScenes {
					ol, _ := sceneOutline(root, w.UnitDir(s))
					if ol == nil {
						continue
					}
					seen := map[string]bool{}
					for _, sec := range ol.Sections {
						for _, name := range sec.Characters {
							if seen[name] {
								continue
							}
							seen[name] = true
							var hits []canon.Entry
							for _, e := range canonList {
								if e.Type != canon.TypeCharacter {
									continue
								}
								for _, n := range e.Names() {
									if n == name {
										hits = append(hits, e)
										break
									}
								}
							}
							if len(hits) == 1 && !slices.Contains(perCharacter[hits[0].ID], name) {
								perCharacter[hits[0].ID] = append(perCharacter[hits[0].ID], name)
							}
						}
					}
				}
				lo, hi := sceneRange(order, chScenes)
				for _, id := range slices.Sorted(maps.Keys(perCharacter)) {
					var slicesOf []Slice
					for _, e := range stateEvents {
						if e.Entry == id {
							slicesOf = append(slicesOf, Slice{Kind: "state", Text: fmt.Sprintf("%s：%s（%s，%s）", e.ID, e.State, e.Scene, e.Status),
								rank: ledgerRank(order, lo, hi, e.Scene)})
						}
					}
					for _, e := range knowEvents {
						if e.Character == id {
							entry := e.Entry
							if c, ok := entriesByID[entry]; ok && c.Name != nil {
								entry = entry + "（" + *c.Name + "）"
							}
							slicesOf = append(slicesOf, Slice{Kind: "knowledge", Text: fmt.Sprintf("%s：获知 %s 于 %s（%s）", e.ID, entry, e.Scene, e.Status),
								rank: ledgerRank(order, lo, hi, e.Scene)})
						}
					}
					var body strings.Builder
					for _, s := range chScenes {
						if text, ok := draftOf(root, w, s); ok {
							fmt.Fprintf(&body, "【%s】\n%s\n\n", s.Ref, text)
						}
					}
					f := newPacket(w.ID, DimCharacter, ch.Ref, slicesOf)
					f.Subject = id
					add(f, body.String(), false)
				}
			}
		case DimThread:
			for _, it := range threadItems {
				var slicesOf []Slice
				slicesOf = append(slicesOf, Slice{Kind: "thread", Text: fmt.Sprintf("%s：%s（埋设于 %s，计划回收于 %s，状态 %s）", it.ID, it.Text, it.PlantedIn, it.PlannedRecover, it.Status)})
				var body strings.Builder
				for _, ref := range []string{it.PlantedIn, deref(it.RecoveredIn)} {
					if ref == "" {
						continue
					}
					for _, s := range w.Units() {
						if s.Ref == ref && s.Type == outline.SceneType {
							if text, ok := draftOf(root, w, s); ok {
								fmt.Fprintf(&body, "【%s 结尾】\n%s\n\n", s.Ref, tailN(text, 400))
							}
						}
					}
				}
				add(newPacket(w.ID, DimThread, it.ID, slicesOf), body.String(), false)
			}
		case DimCausality, DimPovReveal:
			for _, s := range scenes {
				ol, _ := sceneOutline(root, w.UnitDir(s))
				lo, hi := sceneRange(order, []*work.Unit{s})
				var slicesOf []Slice
				if ol != nil {
					if dim == DimCausality {
						for _, sec := range ol.Sections {
							for _, c := range sec.Causes {
								slicesOf = append(slicesOf, Slice{Kind: "cause", Text: "起因事件 " + c})
								// 起因事件声明场景的正文结尾（packet-plan）。
								if ref, tail := causeSceneTail(root, w, c); tail != "" {
									slicesOf = append(slicesOf, Slice{Kind: "cause-scene", Text: tail, rank: ledgerRank(order, lo, hi, ref)})
								}
							}
							for _, e := range sec.Events {
								slicesOf = append(slicesOf, Slice{Kind: "event", Text: "预期事件 " + e})
							}
						}
					} else {
						pov := ""
						for _, sec := range ol.Sections {
							if sec.Reveal != "" {
								slicesOf = append(slicesOf, Slice{Kind: "reveal", Text: sec.Reveal})
							}
							if pov == "" && sec.Pov != "" {
								pov = sec.Pov
							}
						}
						// 视角人物已知与读者已知人物未知的条目（解析与重放语义同 context 主规格 scene-context）。
						slicesOf = append(slicesOf, povKnowledgeSlices(root, w, s, pov, entriesByID, order)...)
					}
				}
				body, _ := draftOf(root, w, s)
				add(newPacket(w.ID, dim, s.Ref, slicesOf), body, false)
			}
		case DimStyle:
			for _, ch := range chapters {
				var slicesOf []Slice
				if sum, _ := series.LoadSummary(root, w, ch); sum != nil {
					stale := ""
					if sum.Stale {
						stale = "（已过期）"
					}
					slicesOf = append(slicesOf, Slice{Kind: "chapter-summary", Text: sum.Text + stale})
				}
				for _, d := range metricSlices(root, w, ch) {
					slicesOf = append(slicesOf, Slice{Kind: "metric", Text: d, rank: 1}) // 章内场景：先序距离为 0
				}
				f := newPacket(w.ID, DimStyle, ch.Ref, slicesOf)
				f.Excerpt.Text = "本任务为纵向审查：请阅读下方切片（章摘要、情绪曲线偏离点与文风漂移发现项），不读全文。"
				add(f, "", true)
			}
		}
	}
	if ps := asm.problem(); ps != nil {
		return nil, nil, ps
	}

	// 范围内的 scope：给出 unit 时只清理该子树的章与场景（thread 维度不随 unit 收窄，始终在范围内）。
	inScope := map[string]bool{}
	for _, x := range append(chaptersIn(w, u), scenes...) {
		inScope[x.Ref] = true
	}
	return persist(root, Dir+"/"+w.ID, asm.packets, dims, func(f *fileRecord) bool {
		return u == nil || f.Dimension == DimThread || inScope[f.Scope]
	})
}

// persist 落盘一次规划生成的任务包（packet-plan）：按 packetKey（dimension, scope, subject, 清单项集合）匹配
// 既有包——done 包保留（不重写，输出取自落盘文件），open 包沿用其 id 原位重建；其余新建，新 id 避开全部既有
// id。全部写入前统一预检（任一包不合法时不写任何文件）。之后删除本次维度与范围内、没有被重建的 open 包（键已
// 不再生成的过期包，以及同键的重复包），不触碰 done、其他维度与范围外的包。
func persist(root, packetDir string, packets []fileRecord, dims []string, inRange func(*fileRecord) bool) ([]Packet, []string, []workspace.Problem) {
	type existingFile struct {
		id  string
		rec *fileRecord
	}
	var existing []existingFile
	taken := map[string]bool{}
	if entries, err := os.ReadDir(filepath.Join(root, filepath.FromSlash(packetDir))); err == nil {
		for _, e := range entries {
			if e.IsDir() || !strings.HasSuffix(e.Name(), ".yaml") {
				continue
			}
			taken[strings.TrimSuffix(e.Name(), ".yaml")] = true // 不合法的包文件也不被新 id 覆盖
			f, ps := loadPacket(root, packetDir+"/"+e.Name())
			if f == nil || len(ps) > 0 {
				continue
			}
			existing = append(existing, existingFile{id: strings.TrimSuffix(e.Name(), ".yaml"), rec: f})
		}
	}
	done := map[string]existingFile{} // packetKey → 既有 done 包
	openKey := map[string][]string{}  // packetKey → 既有 open 包 id
	for _, x := range existing {
		taken[x.id] = true
		key := packetKey(x.rec)
		if x.rec.Status == "done" {
			done[key] = x
		} else {
			openKey[key] = append(openKey[key], x.id)
		}
	}
	type toWrite struct {
		rel string
		rec *fileRecord
	}
	var writes []toWrite
	out := []Packet{}
	for i := range packets {
		p := &packets[i]
		key := packetKey(p)
		if d, ok := done[key]; ok {
			stored := *d.rec
			stored.ID = d.id
			out = append(out, viewOf(&stored, packetChars(&stored)))
			continue
		}
		if ids := openKey[key]; len(ids) > 0 {
			p.ID, openKey[key] = ids[0], ids[1:]
		} else {
			for n := 1; ; n++ {
				id := fmt.Sprintf("%s-%d", p.Dimension, n)
				if !taken[id] {
					p.ID = id
					taken[id] = true
					break
				}
			}
		}
		writes = append(writes, toWrite{rel: packetDir + "/" + p.ID + ".yaml", rec: p})
		out = append(out, viewOf(p, packetChars(p)))
	}
	for _, wv := range writes {
		if err := precheckPacket(wv.rec); err != nil {
			return nil, nil, []workspace.Problem{errorAt(workspace.CodeWriteFailed, wv.rel, "", "生成的任务包不通过校验："+err.Error())}
		}
	}
	changed := []string{}
	written := map[string]bool{}
	for _, wv := range writes {
		if err := writePacket(root, wv.rel, wv.rec); err != nil {
			return nil, nil, []workspace.Problem{errorAt(workspace.CodeWriteFailed, wv.rel, "", "写入任务包失败："+err.Error())}
		}
		changed = append(changed, wv.rel)
		written[wv.rec.ID] = true
	}
	for _, x := range existing {
		if x.rec.Status != "open" || written[x.id] || !slices.Contains(dims, x.rec.Dimension) || !inRange(x.rec) {
			continue
		}
		rel := packetDir + "/" + x.id + ".yaml"
		if err := os.Remove(filepath.Join(root, filepath.FromSlash(rel))); err == nil {
			changed = append(changed, rel)
		}
	}
	return out, changed, nil
}

func deref(s *string) string {
	if s == nil {
		return ""
	}
	return *s
}

func newPacket(workID, dim, scope string, slicesOf []Slice) fileRecord {
	f := fileRecord{SchemaVersion: 1, Work: workID, Dimension: dim, Scope: scope, Status: "open"}
	if slicesOf != nil {
		f.Slices = slicesOf
	} else {
		f.Slices = []Slice{}
	}
	f.Checklist = checklists[dim]
	f.Results = nil
	return f
}

// metricSlices 返回章内各场景已记录的 AI 评分（会话值，含段落均值），没有 metrics.yaml 时为空。
func metricSlices(root string, w *work.Work, ch *work.Unit) []string {
	var out []string
	for _, s := range scenesIn(w, ch) {
		f, ps := metrics.Load(root, w.UnitDir(s))
		if f == nil && len(ps) > 0 {
			continue
		}
		if f == nil || len(f.Sessions) == 0 {
			continue
		}
		last := f.Sessions[len(f.Sessions)-1]
		parts := []string{}
		for _, sc := range last.Scores {
			parts = append(parts, fmt.Sprintf("%s#%d=%d", sc.Metric, sc.Paragraph, sc.Score))
		}
		if len(parts) > 0 {
			out = append(out, fmt.Sprintf("%s：%s", s.Ref, strings.Join(parts, "、")))
		}
	}
	return out
}

func loadPacket(root, rel string) (*fileRecord, []workspace.Problem) {
	if _, ok, _ := workspace.ReadRegular(root, rel); !ok {
		if info, err := os.Lstat(filepath.Join(root, filepath.FromSlash(rel))); err == nil && !info.Mode().IsRegular() {
			return nil, []workspace.Problem{errorAt(schema.CodeNotFound, rel, "", "任务包文件途经符号链接或不是普通文件，按不存在处理")}
		}
		return nil, nil
	}
	doc, ps := workspace.Read(root, rel)
	if doc == nil {
		return nil, ps
	}
	if sps := schema.Named("packet").ValidateNode(doc); len(sps) > 0 {
		return nil, workspace.FromSchema(rel, sps)
	}
	var f fileRecord
	catalog.Decode(doc, &f)
	return &f, nil
}

// precheckPacket 对生成内容做编码、重新解析与 Schema 校验，不写文件（写入前统一预检）。
func precheckPacket(f *fileRecord) error {
	data, err := workspace.Encode(recordDoc(f))
	if err != nil {
		return err
	}
	written, p := schema.Parse(data)
	if p != nil {
		return fmt.Errorf("生成的内容无法解析：%s", p.Message)
	}
	if ps := schema.Named("packet").ValidateNode(written); len(ps) > 0 {
		return fmt.Errorf("生成的内容不通过校验：%s", ps[0].Message)
	}
	return nil
}

func writePacket(root, rel string, f *fileRecord) error {
	if err := os.MkdirAll(filepath.Dir(filepath.Join(root, filepath.FromSlash(rel))), 0o755); err != nil {
		return err
	}
	data, err := workspace.Encode(recordDoc(f))
	if err != nil {
		return err
	}
	written, p := schema.Parse(data)
	if p != nil {
		return fmt.Errorf("生成的内容无法解析：%s", p.Message)
	}
	if ps := schema.Named("packet").ValidateNode(written); len(ps) > 0 {
		return fmt.Errorf("生成的内容不通过校验：%s", ps[0].Message)
	}
	return workspace.WriteFile(root, rel, data)
}

func recordDoc(f *fileRecord) *yaml.Node {
	m := &yaml.Node{Kind: yaml.MappingNode}
	m.Content = append(m.Content, workspace.Str("schema_version"), workspace.Int(1),
		workspace.Str("id"), workspace.Str(f.ID), workspace.Str("work"), workspace.Str(f.Work),
		workspace.Str("dimension"), workspace.Str(f.Dimension), workspace.Str("scope"), workspace.Str(f.Scope))
	if f.Subject != "" {
		m.Content = append(m.Content, workspace.Str("subject"), workspace.Str(f.Subject))
	}
	ex := &yaml.Node{Kind: yaml.MappingNode, Content: []*yaml.Node{
		workspace.Str("text"), workspace.Str(f.Excerpt.Text),
		workspace.Str("truncated"), &yaml.Node{Kind: yaml.ScalarNode, Tag: "!!bool", Value: fmt.Sprint(f.Excerpt.Truncated)}}}
	m.Content = append(m.Content, workspace.Str("excerpt"), ex)
	sn := &yaml.Node{Kind: yaml.SequenceNode, Tag: "!!seq"}
	for _, s := range f.Slices {
		sn.Content = append(sn.Content, &yaml.Node{Kind: yaml.MappingNode, Content: []*yaml.Node{
			workspace.Str("kind"), workspace.Str(s.Kind), workspace.Str("text"), workspace.Str(s.Text)}})
	}
	m.Content = append(m.Content, workspace.Str("slices"), sn)
	if f.OmittedSlices > 0 {
		m.Content = append(m.Content, workspace.Str("omitted_slices"), workspace.Int(f.OmittedSlices))
	}
	cn := &yaml.Node{Kind: yaml.SequenceNode, Tag: "!!seq"}
	for _, c := range f.Checklist {
		cn.Content = append(cn.Content, &yaml.Node{Kind: yaml.MappingNode, Content: []*yaml.Node{
			workspace.Str("id"), workspace.Str(c.ID), workspace.Str("question"), workspace.Str(c.Question)}})
	}
	m.Content = append(m.Content, workspace.Str("checklist"), cn,
		workspace.Str("status"), workspace.Str(f.Status))
	if f.Results != nil {
		rn := &yaml.Node{Kind: yaml.SequenceNode, Tag: "!!seq"}
		for _, r := range f.Results {
			item := &yaml.Node{Kind: yaml.MappingNode, Content: []*yaml.Node{
				workspace.Str("item"), workspace.Str(r.Item), workspace.Str("verdict"), workspace.Str(r.Verdict)}}
			if r.Note != "" {
				item.Content = append(item.Content, workspace.Str("note"), workspace.Str(r.Note))
			}
			rn.Content = append(rn.Content, item)
		}
		m.Content = append(m.Content, workspace.Str("results"), rn)
		if f.RecordedAt != nil {
			m.Content = append(m.Content, workspace.Str("recorded_at"), workspace.Str(*f.RecordedAt))
		}
	}
	return &yaml.Node{Kind: yaml.DocumentNode, HeadComment: "审查任务包（Review Task Packet）；由 wa-cli review plan 生成，结论经 wa-cli review submit 写回。", Content: []*yaml.Node{m}}
}

// FindPacket 在作品 workID 的包目录中按 id 定位任务包；id 与文件内 id 不一致时报告 packet.invalid。
func FindPacket(root, workID, id string) (*fileRecord, string, []workspace.Problem) {
	rel := Dir + "/" + workID + "/" + id + ".yaml"
	f, ps := loadPacket(root, rel)
	if ps != nil {
		return nil, rel, ps
	}
	if f != nil && f.ID != id {
		return nil, rel, []workspace.Problem{errorAt(CodeInvalid, rel, "id",
			"任务包文件内记录的 id "+f.ID+" 与文件名 "+id+" 不一致；请修正文件或用文件内的 id")}
	}
	return f, rel, nil
}

// SubmitInput 是结论输入。
type SubmitInput struct {
	Verdicts []Result `json:"verdicts"`
}

// ParseInput 解析并校验结论输入。
func ParseInput(data []byte) (SubmitInput, []workspace.Problem) {
	doc, p := schema.Parse(data)
	if p != nil {
		return SubmitInput{}, workspace.FromSchema("", []schema.Problem{*p})
	}
	if ps := schema.Named("packet-input").ValidateNode(doc); len(ps) > 0 {
		return SubmitInput{}, workspace.FromSchema("", ps)
	}
	var in struct {
		Verdicts []struct {
			Item    string `json:"item"`
			Verdict string `json:"verdict"`
			Note    string `json:"note"`
		} `json:"verdicts"`
	}
	catalog.Decode(doc, &in)
	out := SubmitInput{}
	for _, v := range in.Verdicts {
		out.Verdicts = append(out.Verdicts, Result{Item: v.Item, Verdict: v.Verdict, Note: v.Note})
	}
	return out, nil
}

// Submit 把结论写回任务包：校验清单齐全后置 status 为 done。
func Submit(root string, w *work.Work, id string, in SubmitInput, now time.Time) (*fileRecord, []string, []workspace.Problem) {
	f, rel, ps := FindPacket(root, w.ID, id)
	if ps != nil {
		return nil, nil, ps
	}
	if f == nil {
		return nil, nil, []workspace.Problem{errorAt(CodeNotFound, "", "",
			"作品 "+w.ID+" 没有任务包 "+id+"；先运行 wa-cli review plan 生成")}
	}
	if f.Status == "done" {
		return nil, nil, []workspace.Problem{errorAt(CodeAlreadyDone, rel, "", "任务包 "+id+" 已提交过结论；重新审查请先 wa-cli review plan 重建该包")}
	}
	// 预检：未知 item、重复结论、缺漏结论（失败不写文件）。
	known := map[string]bool{}
	for _, c := range f.Checklist {
		known[c.ID] = true
	}
	given := map[string]bool{}
	var problems []workspace.Problem
	for i, v := range in.Verdicts {
		if !known[v.Item] {
			problems = append(problems, errorAt(CodeUnknownItem, "", fmt.Sprintf("verdicts[%d].item", i),
				"清单项 "+v.Item+" 不在任务包 "+id+" 的检查清单中"))
			continue
		}
		if given[v.Item] {
			problems = append(problems, errorAt(CodeUnknownItem, "", fmt.Sprintf("verdicts[%d].item", i),
				"清单项 "+v.Item+" 有多个结论；每项只能有一个结论"))
		}
		given[v.Item] = true
	}
	for _, c := range f.Checklist {
		if !given[c.ID] {
			problems = append(problems, errorAt(CodeMissingVerdict, "", "verdicts",
				"清单项 "+c.ID+"（"+c.Question+"）没有结论；每个清单项都必须给出 pass、fail 或 na"))
		}
	}
	if len(problems) > 0 {
		return nil, nil, problems
	}
	f.Status = "done"
	f.Results = in.Verdicts
	ts := now.UTC().Format("2006-01-02T15:04:05Z")
	f.RecordedAt = &ts
	// 节点树上只改 status、results 与 recorded_at，保留其他内容与 YAML 注释（workspace-artifacts/
	// structured-edits）；写出前重新解析并校验。
	doc, ps := workspace.Read(root, rel)
	if doc == nil || len(ps) > 0 {
		return nil, nil, append([]workspace.Problem{errorAt(CodeInvalid, rel, "", "任务包无法重新读取")}, ps...)
	}
	m := doc.Content[0]
	for i := 0; i+1 < len(m.Content); i += 2 {
		switch m.Content[i].Value {
		case "status":
			m.Content[i+1] = workspace.Str("done")
		case "results", "recorded_at":
			m.Content = slices.Delete(m.Content, i, i+2)
			i -= 2
		}
	}
	rn := &yaml.Node{Kind: yaml.SequenceNode, Tag: "!!seq"}
	for _, r := range f.Results {
		item := &yaml.Node{Kind: yaml.MappingNode, Content: []*yaml.Node{
			workspace.Str("item"), workspace.Str(r.Item), workspace.Str("verdict"), workspace.Str(r.Verdict)}}
		if r.Note != "" {
			item.Content = append(item.Content, workspace.Str("note"), workspace.Str(r.Note))
		}
		rn.Content = append(rn.Content, item)
	}
	m.Content = append(m.Content, workspace.Str("results"), rn, workspace.Str("recorded_at"), workspace.Str(ts))
	data, err := workspace.Encode(doc)
	if err != nil {
		return nil, nil, []workspace.Problem{errorAt(workspace.CodeWriteFailed, rel, "", "生成任务包失败："+err.Error())}
	}
	written, p := schema.Parse(data)
	if p != nil {
		return nil, nil, workspace.FromSchema(rel, []schema.Problem{*p})
	}
	if sps := schema.Named("packet").ValidateNode(written); len(sps) > 0 {
		return nil, nil, workspace.FromSchema(rel, sps)
	}
	if err := workspace.WriteFile(root, rel, data); err != nil {
		return nil, nil, []workspace.Problem{errorAt(workspace.CodeWriteFailed, rel, "", "写入任务包失败："+err.Error())}
	}
	return f, []string{rel}, nil
}

// DimensionStat 是 report 的维度统计。
type DimensionStat struct {
	Dimension string `json:"dimension"`
	Open      int    `json:"open"`
	Done      int    `json:"done"`
}

// Failure 是一条 fail 结论。
type Failure struct {
	Packet  string `json:"packet"`
	Scope   string `json:"scope"`
	Item    string `json:"item"`
	Verdict string `json:"verdict"`
	Note    string `json:"note"`
}

// Report 汇总作品全部任务包：每命令只加载一次全部包；不通过校验的包以 packet.invalid 汇总报告并使 ready
// 为 false，不让汇总崩溃（workspace-artifacts/read-path-invariants）。
func Report(root string, w *work.Work) (stats []DimensionStat, failures []Failure, ready bool, ps []workspace.Problem) {
	stats = []DimensionStat{}
	failures = []Failure{}
	total := 0
	invalid := 0
	var firstInvalid string
	for _, dim := range Dimensions {
		st := DimensionStat{Dimension: dim}
		for _, l := range loadAll(root, w.ID) {
			if len(l.ps) > 0 {
				invalid++
				if firstInvalid == "" {
					firstInvalid = l.rel + "：" + l.ps[0].Message
				}
				continue
			}
			if l.p.Dimension != dim {
				continue
			}
			total++
			if l.p.Status == "done" {
				st.Done++
				for _, r := range l.p.Results {
					if r.Verdict == "fail" {
						failures = append(failures, Failure{Packet: l.p.ID, Scope: l.p.Scope, Item: r.Item, Verdict: r.Verdict, Note: r.Note})
					}
				}
			} else {
				st.Open++
			}
		}
		stats = append(stats, st)
	}
	if invalid > 0 {
		ps = append(ps, workspace.Problem{Code: CodeInvalid, Severity: workspace.SeverityError, Message: firstInvalid})
		return stats, failures, false, ps
	}
	if total == 0 {
		return stats, failures, false, nil
	}
	ready = len(failures) == 0
	for _, st := range stats {
		if st.Open > 0 {
			ready = false
		}
	}
	return stats, failures, ready, nil
}

type loaded struct {
	p   *fileRecord
	rel string
	ps  []workspace.Problem
}

func loadAll(root, workID string) []loaded {
	var out []loaded
	dir := filepath.Join(root, filepath.FromSlash(Dir+"/"+workID))
	entries, err := os.ReadDir(dir)
	if err != nil {
		return nil
	}
	for _, e := range entries {
		if e.IsDir() || !strings.HasSuffix(e.Name(), ".yaml") {
			continue
		}
		rel := Dir + "/" + workID + "/" + e.Name()
		f, ps := loadPacket(root, rel)
		if f == nil {
			if len(ps) > 0 {
				out = append(out, loaded{rel: rel, ps: ps})
			}
			continue
		}
		// 领域校验：id 与文件名一致、done 结论齐全。
		if f.ID != strings.TrimSuffix(e.Name(), ".yaml") {
			out = append(out, loaded{p: f, rel: rel, ps: []workspace.Problem{errorAt(CodeInvalid, rel, "id",
				"任务包 id "+f.ID+" 与文件名不一致")}})
			continue
		}
		if f.Status == "done" {
			known := map[string]bool{}
			for _, c := range f.Checklist {
				known[c.ID] = true
			}
			given := map[string]bool{}
			var probs []workspace.Problem
			for i, r := range f.Results {
				if !known[r.Item] {
					probs = append(probs, errorAt(CodeUnknownItem, rel, fmt.Sprintf("results[%d].item", i), "结论引用的清单项 "+r.Item+" 不在清单中"))
					continue
				}
				if given[r.Item] {
					probs = append(probs, errorAt(CodeUnknownItem, rel, fmt.Sprintf("results[%d].item", i), "清单项 "+r.Item+" 有多个结论"))
				}
				given[r.Item] = true
			}
			for _, c := range f.Checklist {
				if !given[c.ID] {
					probs = append(probs, errorAt(CodeMissingVerdict, rel, "results", "清单项 "+c.ID+" 没有结论"))
				}
			}
			if len(probs) > 0 {
				out = append(out, loaded{p: f, rel: rel, ps: probs})
				continue
			}
		}
		out = append(out, loaded{p: f, rel: rel})
	}
	return out
}

// CheckWorks 校验每个作品包目录下的全部任务包（整体校验）。
func CheckWorks(root string, works []*work.Work) (checked []string, problems []workspace.Problem) {
	for _, w := range works {
		for _, l := range loadAll(root, w.ID) {
			checked = append(checked, l.rel)
			problems = append(problems, l.ps...)
		}
	}
	return checked, problems
}

// CheckArtifact 对单个包文件做 Schema 与领域校验（wa-cli validate <path>）。
func CheckArtifact(root, rel string) []workspace.Problem {
	f, ps := loadPacket(root, rel)
	if f == nil {
		return ps
	}
	parts := strings.Split(rel, "/")
	if f.ID != strings.TrimSuffix(parts[len(parts)-1], ".yaml") {
		return []workspace.Problem{errorAt(CodeInvalid, rel, "id", "任务包 id 与文件名不一致")}
	}
	// done 结论齐全性与 loadAll 的判定相同，这里对单个文件直接检查。
	if f.Status == "done" {
		known := map[string]bool{}
		for _, c := range f.Checklist {
			known[c.ID] = true
		}
		given := map[string]bool{}
		for i, r := range f.Results {
			if !known[r.Item] {
				return []workspace.Problem{errorAt(CodeUnknownItem, rel, fmt.Sprintf("results[%d].item", i), "结论引用的清单项 "+r.Item+" 不在清单中")}
			}
			if given[r.Item] {
				return []workspace.Problem{errorAt(CodeUnknownItem, rel, fmt.Sprintf("results[%d].item", i), "清单项 "+r.Item+" 有多个结论")}
			}
			given[r.Item] = true
		}
		for _, c := range f.Checklist {
			if !given[c.ID] {
				return []workspace.Problem{errorAt(CodeMissingVerdict, rel, "results", "清单项 "+c.ID+" 没有结论")}
			}
		}
	}
	return nil
}

// DimSeriesCausality 是跨作品因果维度（series-continuity-review 主规格）。
const DimSeriesCausality = "series-causality"

// SeriesDimensions 是系列级规划的全部维度（含单作品维度在系列范围的重排）。
var SeriesDimensions = append([]string{DimSeriesCausality}, Dimensions...)

// checklists 的系列因果清单。
func init() {
	checklists[DimSeriesCausality] = []ChecklistItem{
		{"cross-cause-holds", "跨作品的起因在两部正文中都成立吗？"},
		{"event-chain", "前作事件到本作场景的因果链连贯吗？"},
		{"motivation", "跨作品的人物动机一致吗？"},
	}
}

// PlanSeries 为系列（多作品，按 works 顺序）生成任务包（series-continuity-review 主规格）：character 生成
// 跨作品包（人物 × 其出场的全部作品的章合并一包）、causality 的切片顺序按合并时间线、新增 series-causality
// 维度（causes 引用跨作品事件的场景）；scope 前缀 series:<series-id> 标明系列与涉及作品。单作品调用退化为
// Plan 的既有行为。
func PlanSeries(root string, seriesID string, ws []*work.Work, dimension string, budget int, now time.Time) ([]Packet, []string, []workspace.Problem) {
	if len(ws) == 1 {
		return Plan(root, ws[0], nil, dimension, budget, now)
	}
	if dimension != "" && !slices.Contains(SeriesDimensions, dimension) {
		return nil, nil, []workspace.Problem{{Code: "input.invalid-value", Severity: workspace.SeverityError, Field: "dimension",
			Message: "维度 " + dimension + " 不存在；可用维度：" + strings.Join(SeriesDimensions, "、")}}
	}
	dims := SeriesDimensions
	if dimension != "" {
		dims = []string{dimension}
	}
	tws := make([]*timelinePkgWork, 0, len(ws))
	for _, w := range ws {
		tws = append(tws, &timelinePkgWork{W: w})
	}
	_, _ = timelinePkgOrder(root, tws) // 合并排序可用于后续按时间线切片的维度扩展；当前维度自行排序
	asm := &assembler{budget: budget}
	for _, dim := range dims {
		switch dim {
		case DimSeriesCausality:
			// 场景的 causes 引用另一作品声明的事件：切片含声明场景的正文结尾。
			declaredBy := map[string]string{} // 事件 → 声明场景
			for _, w := range ws {
				for _, u := range w.Units() {
					if u.Type != outline.SceneType {
						continue
					}
					ol, _ := sceneOutline(root, w.UnitDir(u))
					if ol == nil {
						continue
					}
					for _, sec := range ol.Sections {
						for _, ev := range sec.Events {
							if _, ok := declaredBy[ev]; !ok {
								declaredBy[ev] = u.Ref
							}
						}
					}
				}
			}
			for _, w := range ws {
				for _, u := range w.Units() {
					if u.Type != outline.SceneType {
						continue
					}
					ol, _ := sceneOutline(root, w.UnitDir(u))
					if ol == nil {
						continue
					}
					var cross []string
					for _, sec := range ol.Sections {
						for _, c := range sec.Causes {
							ref := declaredBy[c]
							if ref == "" || ref == u.Ref {
								continue
							}
							if !strings.Contains(ref, "/") || strings.SplitN(ref, "/", 2)[0] == w.ID {
								continue
							}
							cross = append(cross, c)
						}
					}
					if len(cross) == 0 {
						continue
					}
					var slicesOf []Slice
					for _, c := range cross {
						slicesOf = append(slicesOf, Slice{Kind: "cross-cause", Text: "起因事件 " + c + " 声明于 " + declaredBy[c]})
					}
					body, _ := draftOf(root, w, u)
					asm.add(newPacket(seriesID, DimSeriesCausality, u.Ref, slicesOf), body, false)
				}
			}
		case DimCharacter:
			// 跨作品人物包：人物 × 其出场的全部作品的章合并。
			type personPkt struct {
				names  []string
				entry  string
				chRefs []string
				slices []Slice
				bodies []string
			}
			byEntry := map[string]*personPkt{}
			for _, w := range ws {
				scoped := canonPkgResolve(root, w)
				for _, ch := range chaptersIn(w, nil) {
					seen := map[string]bool{}
					for _, s := range scenesIn(w, ch) {
						ol, _ := sceneOutline(root, w.UnitDir(s))
						if ol == nil {
							continue
						}
						for _, sec := range ol.Sections {
							for _, name := range sec.Characters {
								if seen[name] {
									continue
								}
								seen[name] = true
								id := matchUnique(scoped, name)
								if id == "" {
									continue
								}
								p := byEntry[id]
								if p == nil {
									p = &personPkt{entry: id}
									byEntry[id] = p
								}
								if !slices.Contains(p.names, name) {
									p.names = append(p.names, name)
								}
								if !slices.Contains(p.chRefs, ch.Ref) {
									p.chRefs = append(p.chRefs, ch.Ref)
								}
							}
						}
					}
					// 只有该章确实出场该人物时才并入正文与切片。
					for id, p := range byEntry {
						if !slices.Contains(p.chRefs, ch.Ref) {
							continue
						}
						for _, s := range scenesIn(w, ch) {
							ol, _ := sceneOutline(root, w.UnitDir(s))
							if ol == nil {
								continue
							}
							hits := false
							for _, sec := range ol.Sections {
								for _, name := range sec.Characters {
									if matchUnique(scoped, name) == id {
										hits = true
									}
								}
							}
							if !hits {
								continue
							}
							if text, ok := draftOf(root, w, s); ok {
								p.bodies = append(p.bodies, fmt.Sprintf("【%s】\n%s\n\n", s.Ref, text))
							}
						}
						for _, e := range stateEventsOf(root, w, id) {
							// 跨作品状态切片无法按单一作品的先序定位，相关度最低。
							p.slices = append(p.slices, Slice{Kind: "state", Text: fmt.Sprintf("[作品 %s] %s：%s（%s）", w.ID, e.ID, e.State, e.Scene), rank: rankUnlocated})
						}
					}
				}
			}
			for _, p := range byEntry {
				if len(p.chRefs) < 2 {
					continue // 只在一部作品的章出场：单作品包已覆盖
				}
				f := newPacket(seriesID, DimCharacter, strings.Join(p.chRefs, "+"), p.slices)
				f.Subject = p.entry
				asm.add(f, strings.Join(p.bodies, ""), false)
			}
		}
	}
	if ps := asm.problem(); ps != nil {
		return nil, nil, ps
	}
	// 落盘：与单作品相同的匹配、原位重建与过期清理（系列规划总是覆盖全部成员作品）。
	return persist(root, Dir+"/"+seriesID, asm.packets, dims, func(*fileRecord) bool { return true })
}

// timelinePkgWork / timelinePkgOrder 是 internal/timeline 的薄别名（系列规划的合并排序复用）。
type timelinePkgWork = timeline.Work

func timelinePkgOrder(root string, tws []*timelinePkgWork) ([]timeline.Entry, []workspace.Problem) {
	return timeline.Order(root, tws)
}

// canonPkgResolve 返回作品的作用域解析视图（包内复用；失败时返回空）。
func canonPkgResolve(root string, w *work.Work) []canon.ScopedEntry {
	scoped, _ := canon.Resolve(root, w)
	if scoped == nil {
		return []canon.ScopedEntry{}
	}
	return scoped
}

// matchUnique 返回称谓唯一命中的设定条目 id（多义或未命中为空）。
func matchUnique(scoped []canon.ScopedEntry, name string) string {
	var hit string
	n := 0
	for _, se := range scoped {
		if se.Type != canon.TypeCharacter {
			continue
		}
		for _, x := range se.Names() {
			if x == name {
				hit = se.ID
				n++
				break
			}
		}
	}
	if n == 1 {
		return hit
	}
	return ""
}

// stateEventsOf 返回条目 entry 的全部状态台账事件（先序稳定）。
func stateEventsOf(root string, w *work.Work, entry string) []state.Event {
	events, ps := state.List(root, w, true)
	if events == nil && len(ps) > 0 {
		return nil
	}
	var out []state.Event
	for _, e := range events {
		if e.Entry == entry {
			out = append(out, e)
		}
	}
	return out
}
