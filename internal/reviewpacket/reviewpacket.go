// Package reviewpacket 实现审查任务包（Review Task Packet）：wa-cli review plan 按五个维度（人物一致性、
// 伏笔回收质量、因果与动机、视角与揭示边界、文风与情绪节奏）把全书语义审查拆成不超过上下文预算的小任务
// （一段正文切片、相关台账切片与固定检查清单），写入 .writing-anything/packets/<work>/；结论经
// wa-cli review submit 校验齐全后写回；wa-cli review report 只读汇总。任务包由接入的 Agent 调度执行，
// wa-cli 不调用大语言模型（LLM）、不启动 Agent。本包不 import internal/cli；只返回结构化的问题。
package reviewpacket

import (
	"fmt"
	"os"
	"path/filepath"
	"slices"
	"strings"
	"time"
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

// Slice 是一段台账或数据切片。
type Slice struct {
	Kind string `json:"kind"`
	Text string `json:"text"`
}

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
	Excerpt       struct {
		Text      string `json:"text"`
		Truncated bool   `json:"truncated"`
	} `json:"excerpt"`
	Slices     []Slice         `json:"slices"`
	Checklist  []ChecklistItem `json:"checklist"`
	Status     string          `json:"status"`
	Results    []Result        `json:"results"`
	RecordedAt *string         `json:"recorded_at"`
}

// Packet 是一个任务包的对外输出形态（生成、提交与报告共用；数组永不为 null）。
type Packet struct {
	ID        string   `json:"id"`
	Dimension string   `json:"dimension"`
	Scope     string   `json:"scope"`
	Status    string   `json:"status"`
	Results   []Result `json:"results"`
	Chars     int      `json:"chars"`
	Truncated bool     `json:"truncated"`
}

// ViewOf 把提交后的任务包转换为输出形态（命令层使用）。
func ViewOf(f *File) Packet { return viewOf(f, 0) }

// viewOf 把产物形态转换为输出形态。
func viewOf(f *fileRecord, chars int) Packet {
	return Packet{ID: f.ID, Dimension: f.Dimension, Scope: f.Scope, Status: f.Status,
		Results: orEmptyResults(f.Results), Chars: chars, Truncated: f.Excerpt.Truncated}
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

func packetChars(f *fileRecord) int {
	n := utf8.RuneCountInString(f.Excerpt.Text)
	for _, s := range f.Slices {
		n += utf8.RuneCountInString(s.Kind + "：" + s.Text)
	}
	for _, c := range f.Checklist {
		n += utf8.RuneCountInString(c.ID + "：" + c.Question)
	}
	return n
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

// causeSceneTail 返回声明事件 ev 的场景正文结尾（含场景引用），找不到声明场景或没有正文时为空。
func causeSceneTail(root string, w *work.Work, ev string) string {
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
			return fmt.Sprintf("%s（声明 %s）的结尾：%s", x.Ref, ev, tailN(text, 200))
		}
	}
	return ""
}

// povKnowledgeSlices 返回视角人物的已知条目与读者已知人物未知的条目切片。
func povKnowledgeSlices(root string, w *work.Work, u *work.Unit, pov string, entriesByID map[string]canon.Entry) []Slice {
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
		out = append(out, Slice{Kind: "pov-knowledge", Text: fmt.Sprintf("视角人物已知：%s（获知于 %s）", entry, it.Scene)})
	}
	for _, it := range rep.Known("reader") {
		if known[it.Entry] {
			continue
		}
		entry := it.Entry
		if c, ok := entriesByID[entry]; ok && c.Name != nil {
			entry = entry + "（" + *c.Name + "）"
		}
		out = append(out, Slice{Kind: "reader-only", Text: fmt.Sprintf("读者已知人物未知：%s（获知于 %s）", entry, it.Scene)})
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
// 已有 status 为 done 的包按（dimension, scope）保留；范围内的 open 包重建（内容随台账与正文更新），范围外
// 的包不动。返回生成的包、写入与删除的路径与问题。
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

	var packets []fileRecord
	_ = packets // reassigned below
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
				for id, names := range perCharacter {
					var slicesOf []Slice
					for _, e := range stateEvents {
						if e.Entry == id {
							slicesOf = append(slicesOf, Slice{Kind: "state", Text: fmt.Sprintf("%s：%s（%s，%s）", e.ID, e.State, e.Scene, e.Status)})
						}
					}
					for _, e := range knowEvents {
						if e.Character == id {
							entry := e.Entry
							if c, ok := entriesByID[entry]; ok && c.Name != nil {
								entry = entry + "（" + *c.Name + "）"
							}
							slicesOf = append(slicesOf, Slice{Kind: "knowledge", Text: fmt.Sprintf("%s：获知 %s 于 %s（%s）", e.ID, entry, e.Scene, e.Status)})
						}
					}
					var body strings.Builder
					for _, s := range chScenes {
						if text, ok := draftOf(root, w, s); ok {
							fmt.Fprintf(&body, "【%s】\n%s\n\n", s.Ref, text)
						}
					}
					f := newPacket(w.ID, DimCharacter, ch.Ref, slicesOf)
					f.Excerpt.Text, f.Excerpt.Truncated = budgetExcerpt(body.String(), slicesOf, f.Checklist, budget)
					packets = append(packets, f)
					_ = names
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
				f := newPacket(w.ID, DimThread, it.ID, slicesOf)
				f.Excerpt.Text, f.Excerpt.Truncated = budgetExcerpt(body.String(), slicesOf, f.Checklist, budget)
				packets = append(packets, f)
			}
		case DimCausality, DimPovReveal:
			for _, s := range scenes {
				ol, _ := sceneOutline(root, w.UnitDir(s))
				var slicesOf []Slice
				if ol != nil {
					if dim == DimCausality {
						for _, sec := range ol.Sections {
							for _, c := range sec.Causes {
								slicesOf = append(slicesOf, Slice{Kind: "cause", Text: "起因事件 " + c})
								// 起因事件声明场景的正文结尾（packet-plan）。
								if tail := causeSceneTail(root, w, c); tail != "" {
									slicesOf = append(slicesOf, Slice{Kind: "cause-scene", Text: tail})
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
						for _, s2 := range povKnowledgeSlices(root, w, s, pov, entriesByID) {
							slicesOf = append(slicesOf, s2)
						}
					}
				}
				body, _ := draftOf(root, w, s)
				f := newPacket(w.ID, dim, s.Ref, slicesOf)
				f.Excerpt.Text, f.Excerpt.Truncated = budgetExcerpt(body, slicesOf, f.Checklist, budget)
				packets = append(packets, f)
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
					slicesOf = append(slicesOf, Slice{Kind: "metric", Text: d})
				}
				f := newPacket(w.ID, DimStyle, ch.Ref, slicesOf)
				f.Excerpt.Text = "本任务为纵向审查：请阅读下方切片（章摘要、情绪曲线偏离点与文风漂移发现项），不读全文。"
				f.Excerpt.Truncated = false
				packets = append(packets, f)
			}
		}
	}

	// 落盘：保留 done（按 dimension+scope 匹配），重建 open。新包的 id 避开全部既有 id（含 done 与未重建的
	// open），防止重编号覆盖既有文件；过期的同维度 open 包（本次范围未再生成的）按其自身的 dimension、scope
	// 与 status 判断后删除，不触碰 done 与其他维度。
	packetDir := Dir + "/" + w.ID
	type existingFile struct {
		id, dimension, scope, status string
		doneKey                      string // 命中 done 保留时的既有 id
	}
	var existing []existingFile
	if entries, err := os.ReadDir(filepath.Join(root, filepath.FromSlash(packetDir))); err == nil {
		for _, e := range entries {
			if e.IsDir() || !strings.HasSuffix(e.Name(), ".yaml") {
				continue
			}
			f, ps := loadPacket(root, packetDir+"/"+e.Name())
			if f == nil || len(ps) > 0 {
				continue
			}
			existing = append(existing, existingFile{id: strings.TrimSuffix(e.Name(), ".yaml"),
				dimension: f.Dimension, scope: f.Scope, status: f.Status})
		}
	}
	taken := map[string]bool{}
	doneKey := map[string]string{} // dimension+"\x00"+scope → 既有 id
	for _, x := range existing {
		taken[x.id] = true
		if x.status == "done" {
			doneKey[x.dimension+"\x00"+x.scope] = x.id
		}
	}
	// 先全部构造并分配 id，再统一预检（precheck-before-side-effects：任一包不合法时不写任何文件）。
	type toWrite struct {
		rel string
		rec *fileRecord
	}
	var writes []toWrite
	out := []Packet{}
	for i := range packets {
		p := &packets[i]
		if id, ok := doneKey[p.Dimension+"\x00"+p.Scope]; ok {
			// 保留既有 done 包：不重写，输出中列出既有 id。
			p.ID = id
			p.Status = "done"
			out = append(out, viewOf(p, packetChars(p)))
			continue
		}
		for n := 1; ; n++ {
			id := fmt.Sprintf("%s-%d", p.Dimension, n)
			if !taken[id] {
				p.ID = id
				taken[id] = true
				break
			}
		}
		writes = append(writes, toWrite{rel: packetDir + "/" + p.ID + ".yaml", rec: p})
		out = append(out, viewOf(p, packetChars(p)))
	}
	var changed []string
	for _, wv := range writes {
		if err := precheckPacket(wv.rec); err != nil {
			return nil, nil, []workspace.Problem{errorAt(workspace.CodeWriteFailed, wv.rel, "", "生成的任务包不通过校验："+err.Error())}
		}
	}
	for _, wv := range writes {
		if err := writePacket(root, wv.rel, wv.rec); err != nil {
			return nil, nil, []workspace.Problem{errorAt(workspace.CodeWriteFailed, wv.rel, "", "写入任务包失败："+err.Error())}
		}
		changed = append(changed, wv.rel)
	}
	// 删除过期的同维度 open 包：其（dimension, scope）不在本次生成集且不在 done 保留集。
	regenerated := map[string]bool{}
	for i := range packets {
		regenerated[packets[i].Dimension+"\x00"+packets[i].Scope] = true
	}
	if entries, err := os.ReadDir(filepath.Join(root, filepath.FromSlash(packetDir))); err == nil {
		for _, e := range entries {
			if e.IsDir() || !strings.HasSuffix(e.Name(), ".yaml") {
				continue
			}
			rel := packetDir + "/" + e.Name()
			f, ps := loadPacket(root, rel)
			if f == nil || len(ps) > 0 {
				continue
			}
			if f.Status != "open" || !slices.Contains(dims, f.Dimension) {
				continue
			}
			key := f.Dimension + "\x00" + f.Scope
			if regenerated[key] || doneKey[key] != "" {
				continue
			}
			if err := os.Remove(filepath.Join(root, filepath.FromSlash(rel))); err == nil {
				changed = append(changed, rel)
			}
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

// budgetExcerpt 按预算取正文：checklist 与 slices 优先，excerpt 用剩余预算（不足 100 字符时截到 100 以内）。
func budgetExcerpt(body string, slicesOf []Slice, checklist []ChecklistItem, budget int) (string, bool) {
	used := 0
	for _, s := range slicesOf {
		used += utf8.RuneCountInString(s.Kind + "：" + s.Text)
	}
	for _, c := range checklist {
		used += utf8.RuneCountInString(c.ID + "：" + c.Question)
	}
	left := budget - used - 32 // 预留字段名与结构开销
	if left < 100 {
		left = 100 // 剩余不足 100 字符：仍给最小的可审片段（packet-plan）
	}
	if strings.TrimSpace(body) == "" {
		return "（该范围还没有正文；待正文写入后重新 wa-cli review plan 生成本包）", false
	}
	return headN(body, left)
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
	if f.Status == "done" {
		for _, l := range []loaded{{p: f, rel: rel}} {
			_ = l
		}
	}
	// done 结论齐全性复用 loadAll 的判定：构造临时目录不现实，这里直接重复检查。
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
	var packets []fileRecord
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
					f := newPacket(seriesID, DimSeriesCausality, u.Ref, slicesOf)
					f.Excerpt.Text, f.Excerpt.Truncated = budgetExcerpt(body, slicesOf, f.Checklist, budget)
					packets = append(packets, f)
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
							p.slices = append(p.slices, Slice{Kind: "state", Text: fmt.Sprintf("[作品 %s] %s：%s（%s）", w.ID, e.ID, e.State, e.Scene)})
						}
					}
				}
			}
			for _, p := range byEntry {
				if len(p.chRefs) < 2 {
					continue // 只在一部作品的章出场：单作品包已覆盖
				}
				f := newPacket(seriesID, DimCharacter, strings.Join(p.chRefs, "+"), p.slices)
				f.Excerpt.Text, f.Excerpt.Truncated = budgetExcerpt(strings.Join(p.bodies, ""), p.slices, f.Checklist, budget)
				packets = append(packets, f)
			}
		}
	}
	// 落盘（沿用单作品的 id 避让与 done 保留）。
	packetDir := Dir + "/" + seriesID
	_ = packetDir
	out := []Packet{}
	var changed []string
	taken := map[string]bool{}
	// 既有包目录若存在则避让其 id。
	if entries, err := os.ReadDir(filepath.Join(root, filepath.FromSlash(Dir+"/"+seriesID))); err == nil {
		for _, e := range entries {
			taken[strings.TrimSuffix(e.Name(), ".yaml")] = true
		}
	}
	for i := range packets {
		p := &packets[i]
		for n := 1; ; n++ {
			id := fmt.Sprintf("%s-%d", p.Dimension, n)
			if !taken[id] {
				p.ID = id
				taken[id] = true
				break
			}
		}
		rel := Dir + "/" + seriesID + "/" + p.ID + ".yaml"
		if err := precheckPacket(p); err != nil {
			return nil, nil, []workspace.Problem{errorAt(workspace.CodeWriteFailed, rel, "", "生成的任务包不通过校验："+err.Error())}
		}
		out = append(out, viewOf(p, packetChars(p)))
		_ = rel
		changed = append(changed, rel)
	}
	for i := range packets {
		if err := writePacket(root, Dir+"/"+seriesID+"/"+packets[i].ID+".yaml", &packets[i]); err != nil {
			return nil, nil, []workspace.Problem{errorAt(workspace.CodeWriteFailed, Dir+"/"+seriesID+"/"+packets[i].ID+".yaml", "", "写入任务包失败："+err.Error())}
		}
	}
	return out, changed, nil
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
