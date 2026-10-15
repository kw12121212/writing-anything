// Package volume 管理部单元目录下的卷末检查点（Volume Checkpoint）checkpoint.yaml：本部最后一个场景验收后
// 汇总本部的新增设定、已确认的状态变化、伏笔变化与未处理决策，并从各场景的写后收口（Close-out）提交中确定性
// 抽取至多 3 条供作者对照原文核对。部的最后一个场景验收成功后由 accept 自动生成（OnAccept），也可用
// wa-cli volume checkpoint 重新生成。strict 关卡强度下更早的部缺少检查点或未经作者确认时，下一部的场景不能验收
// （其他关卡强度只提示）。抽样以部单元引用与子树场景正文串联指纹为种子，同一工作区状态总是得到同一抽取结果。本包不 import
// internal/cli；只返回结构化的问题，由命令层转换为诊断（Diagnostic）。
package volume

import (
	"crypto/sha256"
	"encoding/binary"
	"fmt"
	"math/rand"
	"os"
	"path/filepath"
	"slices"
	"strings"
	"time"

	"github.com/kw12121212/writing-anything/internal/canon"
	"github.com/kw12121212/writing-anything/internal/catalog"
	"github.com/kw12121212/writing-anything/internal/closeout"
	"github.com/kw12121212/writing-anything/internal/decision"
	"github.com/kw12121212/writing-anything/internal/knowledge"
	"github.com/kw12121212/writing-anything/internal/outline"
	"github.com/kw12121212/writing-anything/internal/schema"
	"github.com/kw12121212/writing-anything/internal/series"
	"github.com/kw12121212/writing-anything/internal/state"
	"github.com/kw12121212/writing-anything/internal/threads"
	"github.com/kw12121212/writing-anything/internal/work"
	"github.com/kw12121212/writing-anything/internal/workspace"
	"go.yaml.in/yaml/v3"
)

// 卷末检查点的错误码（Error Code）。已发布的错误码不得改名或复用。
const (
	CodeMissing                = "volume.missing"
	CodeNotVolume              = "volume.not-volume"
	CodeScenesOpen             = "volume.scenes-open"
	CodeNotFound               = "volume.not-found"
	CodeIncompleteConfirmation = "volume.incomplete-confirmation"
	CodeAlreadyConfirmed       = "volume.already-confirmed"
	CodeUnknownEntry           = "volume.unknown-entry"
	CodeInvalid                = "volume.invalid"
	CodeUnconfirmed            = "volume.unconfirmed"
)

// FileName 是检查点文件名。
const FileName = "checkpoint.yaml"

// Path 返回部单元目录 dir 下 checkpoint.yaml 的路径。
func Path(dir string) string { return dir + "/" + FileName }

// Sampled 是一条抽取的收口核对样本。
type Sampled struct {
	Scene string `json:"scene"`
	Kind  string `json:"kind"` // state 或 knowledge
	ID    string `json:"id"`
	Text  string `json:"text"`
}

type fileRecord struct {
	SchemaVersion     int       `json:"schema_version"`
	Part              string    `json:"part"`
	Scene             string    `json:"scene"`
	Fingerprint       string    `json:"fingerprint"`
	Canon             []string  `json:"canon"`
	States            []string  `json:"states"`
	Threads           []string  `json:"threads"`
	Decisions         []string  `json:"decisions"`
	Sampled           []Sampled `json:"sampled"`
	ConfirmedByAuthor bool      `json:"confirmed_by_author"`
	Confirmation      *string   `json:"confirmation"`
	Channel           *string   `json:"channel"`
	ConfirmedAt       *string   `json:"confirmed_at"`
}

// View 是 volume 命令的输出形态。
type View struct {
	Part              string    `json:"part"`
	Path              string    `json:"path"`
	Scene             string    `json:"scene"`
	Canon             []string  `json:"canon"`
	States            []string  `json:"states"`
	Threads           []string  `json:"threads"`
	Decisions         []string  `json:"decisions"`
	Sampled           []Sampled `json:"sampled"`
	ConfirmedByAuthor bool      `json:"confirmedByAuthor"`
	Confirmation      *string   `json:"confirmation"`
	Channel           *string   `json:"channel"` // 确认渠道（Confirmation Channel）；未确认为 null，已确认而缺失时为 cli
	ConfirmedAt       *string   `json:"confirmedAt"`
}

func errorAt(code, path, field, msg string) workspace.Problem {
	return workspace.Problem{Code: code, Severity: workspace.SeverityError, Path: path, Field: field, Message: msg}
}

// IsVolume 报告单元 u 是否为部（卷）：声明 stage_from_children 且其类型允许的子层含章（chapter）的汇总单元
// （novel 文体为 part）。只汇总场景的章（chapter）不是部，不生成卷末检查点。
func IsVolume(w *work.Work, u *work.Unit) bool {
	if u.Parent == nil {
		return false
	}
	for _, t := range w.Genre.UnitTypes {
		if t.ID == u.Type && t.StageFromChildren && slices.Contains(t.Children, "chapter") {
			return true
		}
	}
	return false
}

// subtreeScenes 返回部 u 子树中全部场景，按单元树先序。
func subtreeScenes(w *work.Work, u *work.Unit) []*work.Unit {
	var out []*work.Unit
	for _, x := range w.Units() {
		if x.Type != outline.SceneType {
			continue
		}
		for p := x; p != nil; p = p.Parent {
			if p.Ref == u.Ref {
				out = append(out, x)
				break
			}
		}
	}
	return out
}

// load 读取并校验部单元目录下的检查点；不存在时返回 nil 与 nil；途经符号链接按 artifact.not-found 处理。
func load(root, dir string) (*yaml.Node, *fileRecord, []workspace.Problem) {
	rel := Path(dir)
	if _, ok, _ := workspace.ReadRegular(root, rel); !ok {
		if info, err := os.Lstat(filepath.Join(root, filepath.FromSlash(rel))); err == nil && !info.Mode().IsRegular() {
			return nil, nil, []workspace.Problem{errorAt(schema.CodeNotFound, rel, "", "checkpoint.yaml 途经符号链接或不是普通文件，按不存在处理")}
		}
		return nil, nil, nil
	}
	doc, ps := workspace.Read(root, rel)
	if doc == nil {
		return nil, nil, ps
	}
	if ps := schema.Named("checkpoint").ValidateNode(doc); len(ps) > 0 {
		return nil, nil, workspace.FromSchema(rel, ps)
	}
	var f fileRecord
	catalog.Decode(doc, &f)
	return doc, &f, nil
}

// ledgerIndexes 每命令加载一次状态与知情台账：状态条目 id → 状态描述，知情条目 id → 设定条目 id。台账无法
// 加载时返回空索引（其问题由整体校验报告）。
func ledgerIndexes(root string, w *work.Work) (stateText map[string]string, knows map[string]string) {
	stateText, knows = map[string]string{}, map[string]string{}
	if events, ps := state.List(root, w, true); !(events == nil && len(ps) > 0) {
		for _, e := range events {
			stateText[e.ID] = e.State
		}
	}
	if events, ps := knowledge.List(root, w, true); !(events == nil && len(ps) > 0) {
		for _, e := range events {
			knows[e.ID] = e.Entry
		}
	}
	return stateText, knows
}

// sampleCandidates 返回部 u 各场景收口提交的核对候选（kind、条目 id 与文本），按场景先序。state 取状态描述，
// knowledge 取被获知设定条目的 text（经知情台账解析 entry）。
func sampleCandidates(root string, w *work.Work, u *work.Unit, canonEntries map[string]canon.Entry) []Sampled {
	stateText, knows := ledgerIndexes(root, w)
	var out []Sampled
	for _, s := range subtreeScenes(w, u) {
		rec, _ := closeout.Load(root, w.UnitDir(s))
		if rec == nil {
			continue
		}
		for _, r := range rec.State {
			if text, ok := stateText[r.ID]; ok {
				out = append(out, Sampled{Scene: s.Ref, Kind: "state", ID: r.ID, Text: text})
			}
		}
		for _, id := range rec.Knowledge {
			if entry, ok := knows[id]; ok {
				if c, ok := canonEntries[entry]; ok {
					out = append(out, Sampled{Scene: s.Ref, Kind: "knowledge", ID: id, Text: c.Text})
				}
			}
		}
	}
	return out
}

// sample 以部引用与子树正文串联指纹为种子做确定性洗牌，取至多 3 条候选。
func sample(part string, fingerprint string, candidates []Sampled) []Sampled {
	sum := sha256.Sum256([]byte(part + "\n" + fingerprint))
	rng := rand.New(rand.NewSource(int64(binary.BigEndian.Uint64(sum[:8]))))
	shuffled := slices.Clone(candidates)
	rng.Shuffle(len(shuffled), func(i, j int) { shuffled[i], shuffled[j] = shuffled[j], shuffled[i] })
	if len(shuffled) > 3 {
		shuffled = shuffled[:3]
	}
	out := []Sampled{}
	return append(out, shuffled...)
}

// Checkpoint 生成部 u 的卷末检查点：子树全部场景 stage 为 done 否则 volume.scenes-open；汇总 canon、states、
// threads、decisions 与确定性抽样的 sampled；已有检查点且最后场景与指纹一致时不写文件（changedPaths 为空）。
func Checkpoint(root string, w *work.Work, u *work.Unit, now time.Time) (*View, []string, []workspace.Problem) {
	return generate(root, w, u, nil)
}

// PartOf 返回单元 u 最近的部（卷）祖先；没有时返回 nil。
func PartOf(w *work.Work, u *work.Unit) *work.Unit {
	for p := u.Parent; p != nil; p = p.Parent {
		if IsVolume(w, p) {
			return p
		}
	}
	return nil
}

// OnAccept 在场景 u 验收成功后调用：u 是其所属部子树按先序最后一个场景时，按 Checkpoint 的同一逻辑生成该部的
// 卷末检查点（把刚验收的 u 视为已完成，其余场景仍须 stage 为 done），confirmed_by_author 为 false。不是部的
// 最后一个场景或没有部祖先时返回 nil。生成遇到的问题（本部其他场景未完成为 volume.scenes-open，path 为该部应有的
// checkpoint.yaml）一律降为 warning：验收已经写入，不因检查点而撤销；下一部验收的 volume.missing 门禁兜底。
func OnAccept(root string, w *work.Work, u *work.Unit) (*View, []string, []workspace.Problem) {
	if u.Type != outline.SceneType {
		return nil, nil, nil
	}
	part := PartOf(w, u)
	if part == nil {
		return nil, nil, nil
	}
	if scenes := subtreeScenes(w, part); len(scenes) == 0 || scenes[len(scenes)-1].Ref != u.Ref {
		return nil, nil, nil
	}
	v, changed, ps := generate(root, w, part, u)
	for i := range ps {
		ps[i].Severity = workspace.SeverityWarning
		if ps[i].Code == CodeScenesOpen {
			ps[i].Path = Path(w.UnitDir(part))
			ps[i].Message += "（本次验收已写入，卷末检查点（Volume Checkpoint）未生成）"
		}
	}
	return v, changed, ps
}

// generate 生成部 u 的卷末检查点；accepted 非 nil 时把该场景视为已完成（验收刚写入、阶段尚未推进）。
func generate(root string, w *work.Work, u *work.Unit, accepted *work.Unit) (*View, []string, []workspace.Problem) {
	if !IsVolume(w, u) {
		return nil, nil, []workspace.Problem{errorAt(CodeNotVolume, "", "part",
			"单元 "+u.Ref+" 的类型是 "+u.Type+"；卷末检查点（Volume Checkpoint）属于汇总各章的部单元（如 part）")}
	}
	scenes := subtreeScenes(w, u)
	if len(scenes) == 0 {
		return nil, nil, []workspace.Problem{errorAt(CodeScenesOpen, w.Path, "",
			"部 "+u.Ref+" 子树内还没有场景；先建场景并完成验收，再生成卷末检查点")}
	}
	var open []string
	for _, s := range scenes {
		if s.Stage != "done" && (accepted == nil || s.Ref != accepted.Ref) {
			open = append(open, s.Ref)
		}
	}
	if len(open) > 0 {
		shown := open
		if len(shown) > 5 {
			shown = append(append([]string{}, shown[:5]...), fmt.Sprintf("等 %d 项", len(open)))
		}
		return nil, nil, []workspace.Problem{errorAt(CodeScenesOpen, w.Path, "",
			"部 "+u.Ref+" 还有未完成的场景："+strings.Join(shown, "、")+"；全部场景验收并推进到 done 后才能生成卷末检查点")}
	}
	last := scenes[len(scenes)-1]
	fp, ok := series.Fingerprint(root, w, u)
	if !ok {
		return nil, nil, []workspace.Problem{errorAt(CodeScenesOpen, w.Path, "",
			"部 "+u.Ref+" 的子树有场景没有正文（draft.md），无法生成卷末检查点")}
	}
	dir := w.UnitDir(u)
	rel := Path(dir)
	if doc, old, ps := load(root, dir); len(ps) > 0 {
		return nil, nil, ps
	} else if doc != nil && old.Scene == last.Ref && old.Fingerprint == fp {
		return view(u, rel, old), nil, nil
	}
	// 汇总各台账与决策。
	canonList, cps := canon.List(root, w)
	if canonList == nil && len(cps) > 0 && slices.ContainsFunc(cps, func(p workspace.Problem) bool { return p.Severity == workspace.SeverityError }) {
		return nil, nil, cps
	}
	entriesByID := make(map[string]canon.Entry, len(canonList))
	canonIDs := []string{}
	events, sps := state.List(root, w, true)
	if events == nil && len(sps) > 0 {
		return nil, nil, sps
	}
	confirmed := map[string]bool{}
	for _, e := range events {
		if e.Status == state.StatusConfirmed {
			confirmed[e.ID] = true
		}
	}
	stateIDs := []string{}
	threadIDs := []string{}
	items, tps := threads.List(root, w, true)
	if items == nil && len(tps) > 0 {
		return nil, nil, tps
	}
	all, _, dps := decision.Load(root)
	if dps != nil {
		return nil, nil, dps
	}
	decisionIDs := []string{}
	for _, d := range all {
		if d.Status == decision.StatusOpen {
			decisionIDs = append(decisionIDs, d.ID)
		}
	}
	for _, e := range canonList {
		entriesByID[e.ID] = e
	}
	seenState := map[string]bool{}
	seenCanon := map[string]bool{}
	seenThread := map[string]bool{}
	for _, s := range scenes {
		rec, _ := closeout.Load(root, w.UnitDir(s))
		if rec == nil {
			continue
		}
		for _, id := range rec.Canon {
			if _, ok := entriesByID[id]; ok && !seenCanon[id] {
				seenCanon[id] = true
				canonIDs = append(canonIDs, id)
			}
		}
		for _, r := range rec.State {
			if confirmed[r.ID] && !seenState[r.ID] {
				seenState[r.ID] = true
				stateIDs = append(stateIDs, r.ID)
			}
		}
	}
	for _, it := range items {
		touches := false
		for _, s := range scenes {
			if it.PlantedIn == s.Ref || (it.RecoveredIn != nil && *it.RecoveredIn == s.Ref) {
				touches = true
				break
			}
		}
		if touches && !seenThread[it.ID] {
			seenThread[it.ID] = true
			threadIDs = append(threadIDs, it.ID)
		}
	}
	rec := &fileRecord{SchemaVersion: 1, Part: u.Ref, Scene: last.Ref, Fingerprint: fp,
		Canon: canonIDs, States: stateIDs, Threads: threadIDs, Decisions: decisionIDs,
		Sampled: sample(u.Ref, fp, sampleCandidates(root, w, u, entriesByID))}
	if err := os.MkdirAll(filepath.Join(root, filepath.FromSlash(dir)), 0o755); err != nil {
		return nil, nil, []workspace.Problem{errorAt(workspace.CodeWriteFailed, rel, "", "创建部目录失败："+err.Error())}
	}
	if err := writeRecord(root, rel, rec); err != nil {
		return nil, nil, []workspace.Problem{errorAt(workspace.CodeWriteFailed, rel, "", "写入卷末检查点失败："+err.Error())}
	}
	return view(u, rel, rec), []string{rel}, nil
}

// writeRecord 整体写出检查点：构造节点树、编码、重新解析、Schema 校验，通过后原子写入。
func writeRecord(root, rel string, rec *fileRecord) error {
	m := &yaml.Node{Kind: yaml.MappingNode}
	m.Content = append(m.Content, workspace.Str("schema_version"), workspace.Int(1),
		workspace.Str("part"), workspace.Str(rec.Part), workspace.Str("scene"), workspace.Str(rec.Scene),
		workspace.Str("fingerprint"), workspace.Str(rec.Fingerprint))
	seq := func(key string, vs []string) {
		n := &yaml.Node{Kind: yaml.SequenceNode, Tag: "!!seq"}
		for _, v := range vs {
			n.Content = append(n.Content, workspace.Str(v))
		}
		m.Content = append(m.Content, workspace.Str(key), n)
	}
	seq("canon", orEmpty(rec.Canon))
	seq("states", orEmpty(rec.States))
	seq("threads", orEmpty(rec.Threads))
	seq("decisions", orEmpty(rec.Decisions))
	sn := &yaml.Node{Kind: yaml.SequenceNode, Tag: "!!seq"}
	for _, s := range rec.Sampled {
		sn.Content = append(sn.Content, &yaml.Node{Kind: yaml.MappingNode, Content: []*yaml.Node{
			workspace.Str("scene"), workspace.Str(s.Scene), workspace.Str("kind"), workspace.Str(s.Kind),
			workspace.Str("id"), workspace.Str(s.ID), workspace.Str("text"), workspace.Str(s.Text)}})
	}
	m.Content = append(m.Content, workspace.Str("sampled"), sn,
		workspace.Str("confirmed_by_author"), &yaml.Node{Kind: yaml.ScalarNode, Tag: "!!bool", Value: "false"})
	doc := &yaml.Node{Kind: yaml.DocumentNode, HeadComment: "卷末检查点（Volume Checkpoint）；由 wa-cli accept 在部的最后一个场景验收后生成（wa-cli volume checkpoint 可重新生成），经 wa-cli volume confirm 确认。", Content: []*yaml.Node{m}}
	data, err := workspace.Encode(doc)
	if err != nil {
		return err
	}
	written, p := schema.Parse(data)
	if p != nil {
		return fmt.Errorf("生成的内容无法解析：%s", p.Message)
	}
	if ps := schema.Named("checkpoint").ValidateNode(written); len(ps) > 0 {
		return fmt.Errorf("生成的内容不通过校验：%s", ps[0].Message)
	}
	return workspace.WriteFile(root, rel, data)
}

func orEmpty(vs []string) []string {
	if vs == nil {
		return []string{}
	}
	return vs
}

func view(u *work.Unit, rel string, f *fileRecord) *View {
	return &View{Part: f.Part, Path: rel, Scene: f.Scene, Canon: orEmpty(f.Canon), States: orEmpty(f.States),
		Threads: orEmpty(f.Threads), Decisions: orEmpty(f.Decisions), Sampled: orEmptySampled(f.Sampled),
		ConfirmedByAuthor: f.ConfirmedByAuthor, Confirmation: f.Confirmation, Channel: channelOf(f), ConfirmedAt: f.ConfirmedAt}
}

// channelOf 返回已确认检查点的确认渠道（缺失为 cli）；未确认时为 nil。
func channelOf(f *fileRecord) *string {
	if !f.ConfirmedByAuthor {
		return nil
	}
	c := workspace.ChannelOr(f.Channel)
	return &c
}

func orEmptySampled(ss []Sampled) []Sampled {
	if ss == nil {
		return []Sampled{}
	}
	return ss
}

// Load 读取部 u 的检查点（Schema 校验）；不存在时返回 nil 与 nil。
func Load(root string, w *work.Work, u *work.Unit) (*View, []workspace.Problem) {
	dir := w.UnitDir(u)
	doc, f, ps := load(root, dir)
	if ps != nil || doc == nil {
		return nil, ps
	}
	return view(u, Path(dir), f), nil
}

// Confirm 以命令行渠道（cli）确认检查点，见 ConfirmVia。
func Confirm(root string, w *work.Work, u *work.Unit, confirmation string, now time.Time) (*View, []string, []workspace.Problem) {
	return ConfirmVia(root, w, u, confirmation, workspace.ChannelCLI, now)
}

// ConfirmVia 写入部 u 检查点的作者确认记录与确认渠道 channel；已有确认时报告 volume.already-confirmed。
func ConfirmVia(root string, w *work.Work, u *work.Unit, confirmation, channel string, now time.Time) (*View, []string, []workspace.Problem) {
	if strings.TrimSpace(confirmation) == "" {
		return nil, nil, []workspace.Problem{{Code: CodeIncompleteConfirmation, Severity: workspace.SeverityError, Field: "confirmation",
			Message: "作者确认必须给出 --confirmed-by-author 与非空白的 --confirmation（作者原话摘要）"}}
	}
	if !IsVolume(w, u) {
		return nil, nil, []workspace.Problem{errorAt(CodeNotVolume, "", "part",
			"单元 "+u.Ref+" 的类型是 "+u.Type+"；卷末检查点（Volume Checkpoint）属于汇总各章的部单元（如 part）")}
	}
	dir := w.UnitDir(u)
	rel := Path(dir)
	doc, f, ps := load(root, dir)
	if ps != nil {
		return nil, nil, ps
	}
	if doc == nil {
		return nil, nil, []workspace.Problem{errorAt(CodeNotFound, "", "",
			"部 "+u.Ref+" 还没有卷末检查点；先运行 wa-cli volume checkpoint 生成，再确认")}
	}
	if f.ConfirmedByAuthor {
		return nil, nil, []workspace.Problem{errorAt(CodeAlreadyConfirmed, rel, "", "部 "+u.Ref+" 的卷末检查点已经作者确认过")}
	}
	// 节点树上只改确认四字段（含确认渠道），保留其他内容与注释；目标节点或所在映射带锚点、别名或合并键时拒绝修改
	// （workspace-artifacts/structured-edits）。
	m := doc.Content[0]
	if workspace.HasAnchor(m) {
		return nil, nil, []workspace.Problem{errorAt(CodeInvalid, rel, "",
			"checkpoint.yaml 经由 YAML 锚点、别名或合并键得到，wa-cli 无法只修改确认字段；请先去掉锚点、别名与合并键")}
	}
	for i := 0; i+1 < len(m.Content); i += 2 {
		if m.Content[i].Value == "confirmed_by_author" {
			m.Content[i+1] = &yaml.Node{Kind: yaml.ScalarNode, Tag: "!!bool", Value: "true"}
		}
	}
	nowStr := now.UTC().Format("2006-01-02T15:04:05Z")
	keys := []struct {
		key string
		val string
	}{{"confirmation", confirmation}, {"channel", channel}, {"confirmed_at", nowStr}}
	for _, k := range keys {
		found := false
		for i := 0; i+1 < len(m.Content); i += 2 {
			if m.Content[i].Value == k.key {
				m.Content[i+1] = workspace.Str(k.val)
				found = true
				break
			}
		}
		if !found {
			m.Content = append(m.Content, workspace.Str(k.key), workspace.Str(k.val))
		}
	}
	data, err := workspace.Encode(doc)
	if err != nil {
		return nil, nil, []workspace.Problem{errorAt(workspace.CodeWriteFailed, rel, "", "生成卷末检查点失败："+err.Error())}
	}
	written, p := schema.Parse(data)
	if p != nil {
		return nil, nil, workspace.FromSchema(rel, []schema.Problem{*p})
	}
	if ps := schema.Named("checkpoint").ValidateNode(written); len(ps) > 0 {
		return nil, nil, workspace.FromSchema(rel, ps)
	}
	var f2 fileRecord
	catalog.Decode(written, &f2)
	if f2.ConfirmedByAuthor && (f2.Confirmation == nil || *f2.Confirmation == "" || f2.ConfirmedAt == nil || *f2.ConfirmedAt == "") {
		return nil, nil, []workspace.Problem{errorAt(CodeIncompleteConfirmation, rel, "",
			"确认记录不完整：confirmed_by_author 为 true 时必须带非空的 confirmation 与 confirmed_at")}
	}
	if err := workspace.WriteFile(root, rel, data); err != nil {
		return nil, nil, []workspace.Problem{errorAt(workspace.CodeWriteFailed, rel, "", "写入卷末检查点失败："+err.Error())}
	}
	return view(u, rel, &f2), []string{rel}, nil
}

// AcceptanceBlockers 计算场景 u 验收时的卷末检查点阻塞项：按先序早于 u、子树含场景的每个部——没有
// checkpoint.yaml 报 volume.missing，存在而 confirmed_by_author 不为 true 报 volume.unconfirmed，不通过校验报
// volume.invalid（保留其诊断信息）；level 为 strict 报 error（阻塞验收），其他关卡强度报 warning（提示）。
// 本部检查点、更晚的部与没有场景的部不参与。
func AcceptanceBlockers(root string, w *work.Work, u *work.Unit, level string) []workspace.Problem {
	if u.Type != outline.SceneType {
		return nil
	}
	severity := workspace.SeverityWarning
	if level == "strict" {
		severity = workspace.SeverityError
	}
	hint := func(strict string) string {
		if severity == workspace.SeverityWarning {
			return "（关卡强度 " + level + "，仅提示）"
		}
		return strict
	}
	var problems []workspace.Problem
	for _, x := range w.Units() {
		if x.Ref == u.Ref {
			break
		}
		if !IsVolume(w, x) {
			continue
		}
		// 本单元所属的部不参与：它收的是本部的尾，不阻塞本部内的场景。
		own := false
		for p := u; p != nil; p = p.Parent {
			if p.Ref == x.Ref {
				own = true
				break
			}
		}
		// 没有场景的部无法生成检查点，也没有可核对的内容。
		if own || len(subtreeScenes(w, x)) == 0 {
			continue
		}
		rel := Path(w.UnitDir(x))
		doc, f, ps := load(root, w.UnitDir(x))
		switch {
		case ps != nil:
			problems = append(problems, workspace.Problem{Code: CodeInvalid, Severity: severity, Path: rel,
				Message: "部 " + x.Ref + " 的卷末检查点 " + rel + " 不通过校验（" + ps[0].Code + "：" + ps[0].Message + "）；请修复或删除该文件后用 wa-cli volume checkpoint 重新生成"})
		case doc == nil:
			problems = append(problems, workspace.Problem{Code: CodeMissing, Severity: severity, Path: rel,
				Message: "部 " + x.Ref + " 还没有卷末检查点（Volume Checkpoint）；部内全部场景验收并推进到 done 后用 wa-cli volume checkpoint 生成，再请作者确认" +
					hint("；strict 关卡下生成并经作者确认前，下一部的场景不能验收")})
		case !f.ConfirmedByAuthor:
			problems = append(problems, workspace.Problem{Code: CodeUnconfirmed, Severity: severity, Path: rel,
				Message: "部 " + x.Ref + " 的卷末检查点（Volume Checkpoint）未经作者确认" + hint("；strict 关卡下确认前下一部的场景不能验收")})
		}
	}
	return problems
}

// CheckWorks 校验每个可加载作品每个部单元目录下存在的 checkpoint.yaml（整体校验）：Schema 与领域检查。
func CheckWorks(root string, works []*work.Work) (checked []string, problems []workspace.Problem) {
	for _, w := range works {
		for _, u := range w.Units() {
			if !IsVolume(w, u) {
				continue
			}
			rel := Path(w.UnitDir(u))
			if _, ok, _ := workspace.ReadRegular(root, rel); !ok {
				if info, err := os.Lstat(filepath.Join(root, filepath.FromSlash(rel))); err == nil && !info.Mode().IsRegular() {
					checked = append(checked, rel)
					problems = append(problems, errorAt(schema.CodeNotFound, rel, "", "checkpoint.yaml 途经符号链接或不是普通文件，按不存在处理"))
				}
				continue
			}
			checked = append(checked, rel)
			_, f, ps := load(root, w.UnitDir(u))
			if ps != nil {
				problems = append(problems, ps...)
				continue
			}
			problems = append(problems, checkRecord(root, rel, w, u, f)...)
		}
	}
	return checked, problems
}

// CheckArtifact 对 rel（部单元目录下的 checkpoint.yaml）做 Schema 与领域检查，供 wa-cli validate <path>。
func CheckArtifact(root, rel string) []workspace.Problem {
	dir := strings.TrimSuffix(rel, "/"+FileName)
	parts := strings.Split(rel, "/")
	var w *work.Work
	var u *work.Unit
	if len(parts) >= 4 {
		w, _ = work.Load(root, parts[2])
	}
	if w != nil {
		for _, x := range w.Units() {
			if x.Ref == workspace.UnitRef(dir) {
				u = x
				break
			}
		}
	}
	_, f, ps := load(root, dir)
	if f == nil {
		return ps
	}
	return checkRecord(root, rel, w, u, f)
}

// checkRecord 对已通过 Schema 校验的检查点做领域检查：part 与所在部一致、scene 为该部先序最后一个场景、各 id
// 引用存在且 sampled 每条绑定本部场景。w 或 u 无法解析时只做 id 格式与确认记录检查。
func checkRecord(root, rel string, w *work.Work, u *work.Unit, f *fileRecord) (problems []workspace.Problem) {
	if f.ConfirmedByAuthor && (f.Confirmation == nil || strings.TrimSpace(*f.Confirmation) == "" || f.ConfirmedAt == nil || *f.ConfirmedAt == "") {
		problems = append(problems, errorAt(CodeIncompleteConfirmation, rel, "",
			"确认记录不完整：confirmed_by_author 为 true 时必须带非空的 confirmation 与 confirmed_at"))
	}
	if w == nil || u == nil {
		return problems
	}
	if f.Part != u.Ref {
		problems = append(problems, errorAt(CodeInvalid, rel, "part",
			"检查点记录的部为 "+f.Part+"，与所在单元 "+u.Ref+" 不一致"))
	}
	scenes := subtreeScenes(w, u)
	if len(scenes) == 0 || scenes[len(scenes)-1].Ref != f.Scene {
		problems = append(problems, errorAt(CodeInvalid, rel, "scene",
			"检查点记录的最后场景 "+f.Scene+" 不是部 "+u.Ref+" 子树先序最后一个场景"))
	}
	refs := func(kind, code string, ids []string, exists func(id string) bool) {
		for i, id := range ids {
			if !exists(id) {
				problems = append(problems, errorAt(code, rel, fmt.Sprintf("%s[%d]", kind, i), "引用的条目 "+id+" 不存在"))
			}
		}
	}
	canonList, _ := canon.List(root, w)
	canonEntries := make(map[string]canon.Entry, len(canonList))
	for _, e := range canonList {
		canonEntries[e.ID] = e
	}
	events, _ := state.List(root, w, true)
	items, _ := threads.List(root, w, true)
	all, _, _ := decision.Load(root)
	refs("canon", CodeUnknownEntry, f.Canon, func(id string) bool { _, ok := canonEntries[id]; return ok })
	refs("states", CodeUnknownEntry, f.States, func(id string) bool {
		for _, e := range events {
			if e.ID == id {
				return true
			}
		}
		return false
	})
	refs("threads", CodeUnknownEntry, f.Threads, func(id string) bool {
		for _, it := range items {
			if it.ID == id {
				return true
			}
		}
		return false
	})
	refs("decisions", CodeUnknownEntry, f.Decisions, func(id string) bool {
		for _, d := range all {
			if d.ID == id {
				return true
			}
		}
		return false
	})
	stateText, knows := ledgerIndexes(root, w)
	for i, s := range f.Sampled {
		field := fmt.Sprintf("sampled[%d]", i)
		if !slices.ContainsFunc(scenes, func(x *work.Unit) bool { return x.Ref == s.Scene }) {
			problems = append(problems, errorAt(CodeUnknownEntry, rel, field+".scene", "抽取样本的场景 "+s.Scene+" 不在该部子树内"))
		}
		if s.Kind == "state" {
			if _, ok := stateText[s.ID]; !ok {
				problems = append(problems, errorAt(CodeUnknownEntry, rel, field+".id", "抽取样本引用的状态条目 "+s.ID+" 不存在"))
			}
		} else if _, ok := knows[s.ID]; !ok {
			problems = append(problems, errorAt(CodeUnknownEntry, rel, field+".id", "抽取样本引用的知情条目 "+s.ID+" 不存在"))
		}
	}
	return problems
}
