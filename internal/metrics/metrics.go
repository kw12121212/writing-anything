// Package metrics 管理 AI 评分指标（Metric）的打分结构与统计：按段落生成打分任务，把 Agent 在独立会话中给出的分数记录到单元
// 目录的 metrics.yaml，计算中位数与分歧（Score Disagreement），把分歧超过阈值的片段登记为作者决策；把作者的校准（Calibration）
// 打分记录到工作区的 calibration.yaml，按声明指标的配置层统计一致率（Agreement Rate）。wa-cli 不调用模型；本包只返回结构化的
// 结果与问题，由命令层转换为诊断（Diagnostic）。
package metrics

import (
	"bytes"
	"crypto/sha256"
	"encoding/hex"
	"errors"
	"fmt"
	"io/fs"
	"math"
	"os"
	"path/filepath"
	"slices"
	"strconv"
	"strings"

	"github.com/kw12121212/writing-anything/internal/catalog"
	"github.com/kw12121212/writing-anything/internal/decision"
	"github.com/kw12121212/writing-anything/internal/draft"
	"github.com/kw12121212/writing-anything/internal/resolve"
	"github.com/kw12121212/writing-anything/internal/schema"
	"github.com/kw12121212/writing-anything/internal/workspace"
	"go.yaml.in/yaml/v3"
)

// AI 评分的错误码（Error Code）。已发布的错误码不得改名或复用。
const (
	CodeNoneDeclared      = "metrics.none-declared"
	CodeUnknownMetric     = "metrics.unknown-metric"
	CodeUnknownParagraph  = "metrics.unknown-paragraph"
	CodeScoreOutOfRange   = "metrics.score-out-of-range"
	CodeIncompleteSession = "metrics.incomplete-session"
	CodeSessionsDropped   = "metrics.sessions-dropped"
	CodeNotScored         = "metrics.not-scored"
	CodeAnchoredValue     = "metrics.anchored-value"
	CodeTooLarge          = "metrics.too-large"
)

// FileName 是单元目录中 AI 评分的文件名；CalibrationPath 是校准样本相对项目目录的路径。
const (
	FileName        = "metrics.yaml"
	CalibrationPath = workspace.Dir + "/calibration.yaml"
)

// 片段的状态。
const (
	StatusPending  = "pending"
	StatusAgreed   = "agreed"
	StatusDisputed = "disputed"
	StatusDecided  = "decided"
)

// Instructions 是打分任务的说明，随 metrics plan 输出给 Agent。
const Instructions = "每个打分会话（Scoring Session）必须是全新的独立会话（如新开的子 Agent），不要参考其他会话的分数；只依据每个指标的锚定说明与示例片段打分，" +
	"给某个指标打分时覆盖全部片段；每个会话用一个新的会话 id，经 wa-cli metrics record 写入。达到需要的会话数后，分歧超过阈值的片段会登记为作者决策，不要自行下结论。"

// Segment 是一个打分片段（Segment）：正文的一个段落，编号与 wa-cli provenance 相同。
type Segment struct {
	Paragraph int    `json:"paragraph"`
	Text      string `json:"text"`
	SHA256    string `json:"sha256"`
}

// Segments 按段落切分正文 content。
func Segments(content []byte) []Segment {
	out := []Segment{}
	for i, p := range draft.Split(content) {
		out = append(out, Segment{Paragraph: i + 1, Text: p.Text, SHA256: p.Hash})
	}
	return out
}

// Digest 返回 content 的 SHA-256 十六进制。
func Digest(content []byte) string {
	sum := sha256.Sum256(content)
	return hex.EncodeToString(sum[:])
}

// Score 是一次会话给一个片段一个指标的分数。
type Score struct {
	Metric    string `json:"metric" yaml:"metric"`
	Paragraph int    `json:"paragraph" yaml:"paragraph"`
	Score     int    `json:"score" yaml:"score"`
	Rationale string `json:"rationale,omitempty" yaml:"rationale,omitempty"`
}

// Session 是一次打分会话。
type Session struct {
	ID     string  `json:"id" yaml:"id"`
	Scores []Score `json:"scores" yaml:"scores"`
}

// DecisionRef 记录分歧片段对应的作者决策 id；作者的选择由决策队列推导。
type DecisionRef struct {
	Metric    string `json:"metric" yaml:"metric"`
	Paragraph int    `json:"paragraph" yaml:"paragraph"`
	Decision  string `json:"decision" yaml:"decision"`
}

// File 是一份已通过校验的 metrics.yaml。
type File struct {
	DraftSHA256 string        `json:"draft_sha256"`
	Sessions    []Session     `json:"sessions"`
	Decisions   []DecisionRef `json:"decisions"`
}

// Path 返回单元目录 dir 下 metrics.yaml 的路径。
func Path(dir string) string { return dir + "/" + FileName }

func errorAt(code, path, field, msg string) workspace.Problem {
	return workspace.Problem{Code: code, Severity: workspace.SeverityError, Path: path, Field: field, Message: msg}
}

// readDoc 读取 rel 处的产物：不存在时返回 nil、nil；途经符号链接或不是普通文件时报告 artifact.not-found。
func readDoc(root, rel string) (*yaml.Node, []byte, []workspace.Problem) {
	data, ok, err := workspace.ReadRegular(root, rel)
	switch {
	case err != nil:
		return nil, nil, []workspace.Problem{errorAt(workspace.CodeReadFailed, rel, "", "读取失败："+err.Error())}
	case !ok:
		if _, err := os.Lstat(filepath.Join(root, filepath.FromSlash(rel))); err == nil {
			return nil, nil, []workspace.Problem{errorAt(schema.CodeNotFound, rel, "", filepath.Base(rel)+" 途经符号链接或不是普通文件，按不存在处理；请把它换成普通文件")}
		}
		return nil, nil, nil
	}
	doc, p := schema.Parse(data)
	if p != nil {
		return nil, nil, workspace.FromSchema(rel, []schema.Problem{*p})
	}
	return doc, data, nil
}

// Load 读取并校验单元目录 dir 下的 metrics.yaml；不存在时返回 nil 与 nil。
func Load(root, dir string) (*File, []workspace.Problem) {
	_, f, _, ps := load(root, dir)
	return f, ps
}

func load(root, dir string) (*yaml.Node, *File, []byte, []workspace.Problem) {
	rel := Path(dir)
	doc, data, ps := readDoc(root, rel)
	if doc == nil {
		return nil, nil, nil, ps
	}
	f, ps := parse(rel, doc)
	if f == nil {
		return nil, nil, nil, ps
	}
	return doc, f, data, nil
}

// parse 按 Schema 与领域规则（会话 id、会话中的指标与段落、决策记录不重复）校验 metrics.yaml。
func parse(rel string, doc *yaml.Node) (*File, []workspace.Problem) {
	if ps := schema.Named("metrics").ValidateNode(doc); len(ps) > 0 {
		return nil, workspace.FromSchema(rel, ps)
	}
	var f File
	catalog.Decode(doc, &f)
	var out []workspace.Problem
	dup := func(field, msg string) { out = append(out, errorAt(catalog.CodeDuplicateID, rel, field, msg)) }
	sessions := map[string]bool{}
	for i, s := range f.Sessions {
		if sessions[s.ID] {
			dup(fmt.Sprintf("sessions[%d].id", i), "打分会话 "+s.ID+" 重复")
		}
		sessions[s.ID] = true
		seen := map[string]bool{}
		for j, sc := range s.Scores {
			if k := key(sc.Metric, sc.Paragraph); seen[k] {
				dup(fmt.Sprintf("sessions[%d].scores[%d]", i, j), fmt.Sprintf("会话 %s 中指标 %s 第 %d 段的分数重复", s.ID, sc.Metric, sc.Paragraph))
			} else {
				seen[k] = true
			}
		}
	}
	seen := map[string]bool{}
	for i, d := range f.Decisions {
		if k := key(d.Metric, d.Paragraph); seen[k] {
			dup(fmt.Sprintf("decisions[%d]", i), fmt.Sprintf("指标 %s 第 %d 段的分歧决策重复", d.Metric, d.Paragraph))
		} else {
			seen[k] = true
		}
	}
	if out != nil {
		return nil, out
	}
	return &f, nil
}

func key(metric string, paragraph int) string { return fmt.Sprintf("%s#%d", metric, paragraph) }

// CheckUnitArtifacts 校验 dirs 中每个单元目录下存在的 metrics.yaml（包括途经符号链接、按不存在处理的），返回已校验的路径与问题。
func CheckUnitArtifacts(root string, dirs []string) (checked []string, problems []workspace.Problem) {
	for _, dir := range dirs {
		rel := Path(dir)
		if _, err := os.Lstat(filepath.Join(root, filepath.FromSlash(rel))); err != nil {
			continue
		}
		checked = append(checked, rel)
		_, ps := Load(root, dir)
		problems = append(problems, ps...)
	}
	return checked, problems
}

// CheckArtifact 对已通过 Schema 校验的产物 rel（metrics.yaml 或 calibration.yaml）做领域检查。
func CheckArtifact(root, rel string) []workspace.Problem {
	if rel == CalibrationPath {
		_, ps := LoadCalibration(root)
		return ps
	}
	_, ps := Load(root, strings.TrimSuffix(rel, "/"+FileName))
	return ps
}

// Input 是一次打分会话的输入。
type Input struct {
	Session string  `json:"session"`
	Scores  []Score `json:"scores"`
}

// ParseInput 解析并校验 metrics record 的输入：Schema、指标、段落、分值范围、重复与覆盖。问题不填 path，field 为输入中的位置。
func ParseInput(data []byte, segs []Segment, ms []resolve.ScoredMetric) (*Input, []workspace.Problem) {
	doc, p := schema.Parse(data)
	if p != nil {
		return nil, workspace.FromSchema("", []schema.Problem{*p})
	}
	if ps := schema.Named("metrics-input").ValidateNode(doc); len(ps) > 0 {
		return nil, workspace.FromSchema("", ps)
	}
	var in Input
	catalog.Decode(doc, &in)
	byID := map[string]resolve.ScoredMetric{}
	for _, m := range ms {
		byID[m.ID] = m
	}
	var out []workspace.Problem
	covered := map[string]map[int]bool{}
	var order []string
	for i, sc := range in.Scores {
		at := func(f string) string { return fmt.Sprintf("scores[%d].%s", i, f) }
		m, ok := byID[sc.Metric]
		switch {
		case !ok:
			out = append(out, errorAt(CodeUnknownMetric, "", at("metric"), fmt.Sprintf("指标 %s 不是已声明的 AI 评分指标；已声明的指标为 %s", sc.Metric, ids(ms))))
			continue
		case sc.Paragraph > len(segs):
			out = append(out, errorAt(CodeUnknownParagraph, "", at("paragraph"), fmt.Sprintf("第 %d 段不存在；正文共 %d 段", sc.Paragraph, len(segs))))
			continue
		case sc.Score < m.Scale.Min || sc.Score > m.Scale.Max:
			out = append(out, errorAt(CodeScoreOutOfRange, "", at("score"), fmt.Sprintf("指标 %s 的分值为 %d 到 %d，%d 不在范围内", m.ID, m.Scale.Min, m.Scale.Max, sc.Score)))
			continue
		}
		if covered[sc.Metric] == nil {
			covered[sc.Metric] = map[int]bool{}
			order = append(order, sc.Metric)
		}
		if covered[sc.Metric][sc.Paragraph] {
			out = append(out, errorAt(catalog.CodeDuplicateID, "", fmt.Sprintf("scores[%d]", i), fmt.Sprintf("指标 %s 第 %d 段打了不止一次分", sc.Metric, sc.Paragraph)))
		}
		covered[sc.Metric][sc.Paragraph] = true
	}
	if out != nil {
		return nil, out
	}
	for _, id := range order {
		var missing []string
		for n := 1; n <= len(segs); n++ {
			if !covered[id][n] {
				missing = append(missing, fmt.Sprint(n))
			}
		}
		if missing != nil {
			out = append(out, errorAt(CodeIncompleteSession, "", "scores", fmt.Sprintf("会话给指标 %s 打分时必须覆盖全部 %d 个段落，缺少第 %s 段", id, len(segs), strings.Join(missing, "、"))))
		}
	}
	if out != nil {
		return nil, out
	}
	return &in, nil
}

func ids(ms []resolve.ScoredMetric) string {
	var out []string
	for _, m := range ms {
		out = append(out, m.ID)
	}
	if out == nil {
		return "无"
	}
	return strings.Join(out, "、")
}

// Recorded 是一次 metrics record 的结果。
type Recorded struct {
	Session   string
	Replaced  bool
	Dropped   bool     // 正文已改变，丢弃了旧会话
	Decisions []string // 本次新建的分歧决策 id
	Updated   []string // 本次按新分数更新的、尚未决定的分歧决策 id
	Changed   []string
}

// Record 把已校验的会话 in 写入单元目录 dir 的 metrics.yaml，并为达到会话数、不是仅供参考的指标的分歧片段新建决策。ref 为单元引用；
// draftSHA 为当前正文的 SHA-256；advisory 报告指标是否已降级为仅供参考。全部检查都在第一次写入之前完成：先追加决策，再写 metrics.yaml。
func Record(root, dir, ref string, in *Input, segs []Segment, draftSHA string, ms []resolve.ScoredMetric, advisory func(resolve.ScoredMetric) bool) (*Recorded, []workspace.Problem) {
	rel := Path(dir)
	doc, f, old, ps := load(root, dir)
	if ps != nil {
		return nil, ps
	}
	queue, _, ps := decision.Load(root)
	if ps != nil {
		return nil, ps
	}
	open := func(id string) bool {
		return slices.ContainsFunc(queue, func(d decision.Decision) bool { return d.ID == id && d.Status == decision.StatusOpen })
	}
	res := &Recorded{Session: in.Session}
	if f != nil && f.DraftSHA256 != draftSHA {
		// 旧分数针对旧正文：丢弃会话与已决定的决策记录；尚未决定的决策留着，同一片段再有分歧时原地更新，不另建（lesson：旧决策不能撤回）。
		var carry []DecisionRef
		for _, d := range f.Decisions {
			if open(d.Decision) {
				carry = append(carry, d)
			}
		}
		doc, f, res.Dropped = nil, &File{DraftSHA256: draftSHA, Decisions: carry}, true
	}
	if f == nil {
		f = &File{DraftSHA256: draftSHA}
	}
	session := Session{ID: in.Session, Scores: in.Scores}
	if i := slices.IndexFunc(f.Sessions, func(s Session) bool { return s.ID == in.Session }); i >= 0 {
		f.Sessions[i], res.Replaced = session, true
	} else {
		f.Sessions = append(f.Sessions, session)
	}

	type update struct {
		id, recommend, rationale string
		options                  []string
	}
	var inputs []decision.Input
	var updates []update
	var refs []DecisionRef
	for _, m := range ms {
		if advisory(m) || countSessions(f, m.ID) < m.Sessions {
			continue
		}
		var options []string
		for s := m.Scale.Min; s <= m.Scale.Max; s++ {
			options = append(options, fmt.Sprintf("%d：%s", s, m.Anchor(s).Description))
		}
		for _, seg := range segs {
			scores := scoresOf(f, m.ID, seg.Paragraph)
			if len(scores) == 0 || slices.Max(scores)-slices.Min(scores) <= m.MaxSpread {
				continue
			}
			med := median(scores)
			recommend := string(rune('a' + int(math.Round(med)) - m.Scale.Min))
			rationale := fmt.Sprintf("AI 各次打分：%s；中位数为 %s", join(scores), format(med))
			if i := slices.IndexFunc(f.Decisions, func(d DecisionRef) bool { return d.Metric == m.ID && d.Paragraph == seg.Paragraph }); i >= 0 {
				// 已有决策：作者已决定的保留，尚未决定的在分数、推荐或选项改变时原地更新（workspace-artifacts/author-choice-decisions）。
				id := f.Decisions[i].Decision
				if j := slices.IndexFunc(queue, func(d decision.Decision) bool { return d.ID == id }); j >= 0 && queue[j].Status == decision.StatusOpen &&
					!sameDecision(queue[j], options, recommend, rationale) {
					updates = append(updates, update{id, recommend, rationale, options})
				}
				continue
			}
			id := decision.NextID(queue)
			queue = append(queue, decision.Decision{ID: id})
			inputs = append(inputs, decision.Input{ID: id,
				Question: fmt.Sprintf("单元 %s 第 %d 段的「%s」打分有分歧：你认为应是几分？", ref, seg.Paragraph, m.Title),
				Options:  options, Recommend: recommend, Rationale: rationale, Units: []string{ref}})
			refs = append(refs, DecisionRef{Metric: m.ID, Paragraph: seg.Paragraph, Decision: id})
		}
	}
	f.Decisions = append(f.Decisions, refs...)
	data, ps := encodeFile(rel, doc, f, session, refs)
	if ps != nil {
		return nil, ps
	}
	if len(inputs) > 0 {
		if _, ps := decision.AddAll(root, inputs, func(string) []workspace.Problem { return nil }); ps != nil {
			return nil, ps
		}
		for _, in := range inputs {
			res.Decisions = append(res.Decisions, in.ID)
		}
	}
	for _, u := range updates {
		// 决策存在、尚未决定、推荐在选项内，都已在上面按同一份决策队列检查过。
		if _, ps := decision.Replace(root, u.id, u.options, u.recommend, u.rationale); ps != nil {
			return res, ps
		}
		res.Updated = append(res.Updated, u.id)
	}
	if len(inputs) > 0 || len(updates) > 0 {
		res.Changed = append(res.Changed, decision.Path)
	}
	if !bytes.Equal(old, data) {
		if err := workspace.WriteFile(root, rel, data); err != nil {
			msg := "写入失败：" + err.Error()
			if len(inputs) > 0 {
				msg += "；已在决策队列中追加分歧决策 " + strings.Join(res.Decisions, "、") + "，metrics.yaml 没有记下它们，请先请作者处理或忽略这些非阻塞决策"
			}
			return res, []workspace.Problem{errorAt(workspace.CodeWriteFailed, rel, "", msg)}
		}
		res.Changed = append(res.Changed, rel)
	}
	return res, nil
}

// encodeFile 返回写入后的 metrics.yaml：doc 为 nil 时整体生成；否则在节点树上替换或追加会话 s、追加决策记录 refs，保留其他内容与注释。
// 写出前重新解析并校验（workspace-artifacts/structured-edits）。
func encodeFile(rel string, doc *yaml.Node, f *File, s Session, refs []DecisionRef) ([]byte, []workspace.Problem) {
	anchored := func(field string) []workspace.Problem {
		return []workspace.Problem{errorAt(CodeAnchoredValue, rel, field, "该处经由 YAML 锚点、别名或合并键得到，wa-cli 无法只修改这一处；请先去掉锚点、别名与合并键")}
	}
	if doc == nil || len(doc.Content) == 0 {
		root := &yaml.Node{Kind: yaml.MappingNode}
		root.Content = append(root.Content, workspace.Str("schema_version"), workspace.Int(1), workspace.Str("draft_sha256"), workspace.Str(f.DraftSHA256),
			workspace.Str("sessions"), &yaml.Node{Kind: yaml.SequenceNode, Tag: "!!seq"})
		if kept := f.Decisions[:len(f.Decisions)-len(refs)]; len(kept) > 0 {
			// 正文改变后重新生成时，保留尚未决定的分歧决策记录。
			seq := &yaml.Node{Kind: yaml.SequenceNode, Tag: "!!seq"}
			for _, r := range kept {
				seq.Content = append(seq.Content, flow(r))
			}
			root.Content = append(root.Content, workspace.Str("decisions"), seq)
		}
		doc = &yaml.Node{Kind: yaml.DocumentNode, HeadComment: "AI 评分（Scored Metrics）；请用 wa-cli metrics record 修改。", Content: []*yaml.Node{root}}
	}
	root := doc.Content[0]
	if !workspace.Plain(root) || workspace.HasMerge(root) {
		return nil, anchored("")
	}
	sessions, ok := workspace.Lookup(root, "sessions")
	if !ok || sessions == nil {
		return nil, anchored("sessions")
	}
	n := sessionNode(s)
	if i := workspace.IndexByID(sessions, s.ID); i >= 0 {
		if workspace.HasAnchor(sessions.Content[i]) {
			return nil, anchored(fmt.Sprintf("sessions[%d]", i))
		}
		sessions.Content[i] = n
	} else {
		sessions.Content = append(sessions.Content, n)
	}
	workspace.BlockSequence(root, "sessions")
	if len(refs) > 0 {
		decisions, ok := workspace.Lookup(root, "decisions")
		switch {
		case !ok:
			return nil, anchored("decisions")
		case decisions == nil:
			decisions = &yaml.Node{Kind: yaml.SequenceNode, Tag: "!!seq"}
			root.Content = append(root.Content, workspace.Str("decisions"), decisions)
		}
		for _, r := range refs {
			decisions.Content = append(decisions.Content, flow(r))
		}
		workspace.BlockSequence(root, "decisions")
	}
	data, err := workspace.Encode(doc)
	if err != nil {
		return nil, []workspace.Problem{errorAt(workspace.CodeWriteFailed, rel, "", "生成 metrics.yaml 失败："+err.Error())}
	}
	written, p := schema.Parse(data)
	if p != nil {
		// 修改前的 metrics.yaml 是合法的：问题出在本次写入会让它超过可处理的规模，不填 path，validate 复查不到。
		return nil, []workspace.Problem{errorAt(CodeTooLarge, "", "", fmt.Sprintf("记录这次会话后 %s 将超过 wa-cli 可处理的规模（%s）；请减少一次打分的指标，或把单元拆分为多个子单元", rel, p.Message))}
	}
	if _, ps := parse(rel, written); ps != nil {
		return nil, ps
	}
	return data, nil
}

func sessionNode(s Session) *yaml.Node {
	scores := &yaml.Node{Kind: yaml.SequenceNode, Tag: "!!seq"}
	for _, sc := range s.Scores {
		scores.Content = append(scores.Content, flow(sc))
	}
	return &yaml.Node{Kind: yaml.MappingNode, Content: []*yaml.Node{workspace.Str("id"), workspace.Str(s.ID), workspace.Str("scores"), scores}}
}

// flow 把 v 编码为流式映射节点，让每个分数或决策记录占一行。
func flow(v any) *yaml.Node {
	var n yaml.Node
	if err := n.Encode(v); err != nil {
		panic(fmt.Sprintf("编码 %T 失败：%v", v, err)) // 本包自己的结构体一定能编码
	}
	n.Style = yaml.FlowStyle
	return &n
}

// sameDecision 报告决策 d 的选项、推荐与理由是否已经是给出的取值。
func sameDecision(d decision.Decision, options []string, recommend, rationale string) bool {
	var texts []string
	for _, o := range d.Options {
		texts = append(texts, o.Text)
	}
	return slices.Equal(texts, options) && d.Recommendation != nil && *d.Recommendation == recommend && d.Rationale != nil && *d.Rationale == rationale
}

func countSessions(f *File, metric string) int {
	n := 0
	for _, s := range f.Sessions {
		if slices.ContainsFunc(s.Scores, func(sc Score) bool { return sc.Metric == metric }) {
			n++
		}
	}
	return n
}

// scoresOf 返回各会话（按记录顺序）给指标 metric 第 paragraph 段的分数。
func scoresOf(f *File, metric string, paragraph int) []int {
	var out []int
	for _, s := range f.Sessions {
		for _, sc := range s.Scores {
			if sc.Metric == metric && sc.Paragraph == paragraph {
				out = append(out, sc.Score)
			}
		}
	}
	return out
}

// median 返回非空 scores 的中位数：偶数个时取中间两值的平均。
func median(scores []int) float64 {
	s := slices.Sorted(slices.Values(scores))
	n := len(s)
	if n%2 == 1 {
		return float64(s[n/2])
	}
	return float64(s[n/2-1]+s[n/2]) / 2
}

func format(v float64) string { return fmt.Sprint(v) }

func join(scores []int) string {
	var out []string
	for _, s := range scores {
		out = append(out, fmt.Sprint(s))
	}
	return strings.Join(out, "、")
}

// SegmentResult 是一个片段一个指标的汇总。
type SegmentResult struct {
	Paragraph int      `json:"paragraph"`
	Scores    []int    `json:"scores"`
	Median    *float64 `json:"median"`
	Spread    *int     `json:"spread"`
	Status    string   `json:"status"`
	Decision  *string  `json:"decision"`
	Value     *float64 `json:"value"`
}

// MetricResult 是一个指标的汇总。
type MetricResult struct {
	Metric    string          `json:"metric"`
	Title     string          `json:"title"`
	Source    string          `json:"source"`
	Advisory  bool            `json:"advisory"`
	Sessions  int             `json:"sessions"`
	Required  int             `json:"required"`
	Agreement Agreement       `json:"agreement"`
	Segments  []SegmentResult `json:"segments"`
}

// Summary 是一个单元的 AI 评分汇总。Unknown 为 metrics.yaml 中有分数、但不再声明的指标。
type Summary struct {
	Stale    bool
	Sessions []string
	Items    []MetricResult
	Unknown  []string
}

// Summarize 汇总 metrics.yaml f（nil 表示还没有打分）：按 ms 的顺序列出每个指标在每个片段上的分数、中位数、分歧与状态。queue 为
// 决策队列（推导分歧决策是否已决定），cal 为校准样本。
func Summarize(f *File, segs []Segment, draftSHA string, ms []resolve.ScoredMetric, queue []decision.Decision, cal *Calibration) Summary {
	sum := Summary{Sessions: []string{}, Items: []MetricResult{}}
	if f == nil {
		f = &File{}
	} else {
		sum.Stale = f.DraftSHA256 != draftSHA
	}
	for _, s := range f.Sessions {
		sum.Sessions = append(sum.Sessions, s.ID)
	}
	declared := map[string]bool{}
	for _, m := range ms {
		declared[m.ID] = true
		ag := cal.Agreement(m)
		r := MetricResult{Metric: m.ID, Title: m.Title, Source: m.Source, Advisory: ag.Advisory, Sessions: countSessions(f, m.ID), Required: m.Sessions,
			Agreement: ag, Segments: []SegmentResult{}}
		for _, seg := range segs {
			sr := SegmentResult{Paragraph: seg.Paragraph, Scores: scoresOf(f, m.ID, seg.Paragraph), Status: StatusPending}
			if sr.Scores == nil {
				sr.Scores = []int{}
			} else {
				med, spread := median(sr.Scores), slices.Max(sr.Scores)-slices.Min(sr.Scores)
				sr.Median, sr.Spread = &med, &spread
			}
			if i := slices.IndexFunc(f.Decisions, func(d DecisionRef) bool { return d.Metric == m.ID && d.Paragraph == seg.Paragraph }); i >= 0 {
				sr.Decision = &f.Decisions[i].Decision
			}
			switch {
			case sum.Stale || r.Sessions < m.Sessions || sr.Spread == nil:
				// 没有分数的片段（metrics.yaml 不是 wa-cli 写出的、会话没有覆盖它）不能假定已打完分。
			case *sr.Spread <= m.MaxSpread:
				sr.Status, sr.Value = StatusAgreed, sr.Median
			case sr.Decision != nil:
				sr.Status = StatusDisputed
				if v, ok := chosen(queue, *sr.Decision, m); ok {
					sr.Status, sr.Value = StatusDecided, &v
				}
			default:
				sr.Status = StatusDisputed
			}
			r.Segments = append(r.Segments, sr)
		}
		sum.Items = append(sum.Items, r)
	}
	for _, s := range f.Sessions {
		for _, sc := range s.Scores {
			if !declared[sc.Metric] && !slices.Contains(sum.Unknown, sc.Metric) {
				sum.Unknown = append(sum.Unknown, sc.Metric)
			}
		}
	}
	return sum
}

// chosen 返回分歧决策 id 已被作者决定时选择的分值：按决策自己记录的选项文本「<分值>：<说明>」解释，不按当前 scale 反推，
// 规范包改了 scale 也不会把作者的选择换成别的分值；选择的分值已不在当前 scale 内时视为未决定。
func chosen(queue []decision.Decision, id string, m resolve.ScoredMetric) (float64, bool) {
	i := slices.IndexFunc(queue, func(d decision.Decision) bool { return d.ID == id })
	if i < 0 || queue[i].Status != decision.StatusDecided || queue[i].Choice == nil {
		return 0, false
	}
	j := slices.IndexFunc(queue[i].Options, func(o decision.Option) bool { return o.ID == *queue[i].Choice })
	if j < 0 {
		return 0, false
	}
	head, _, _ := strings.Cut(queue[i].Options[j].Text, "：")
	v, err := strconv.Atoi(head)
	if err != nil || v < m.Scale.Min || v > m.Scale.Max {
		return 0, false
	}
	return float64(v), true
}

// Sample 是一个校准样本：作者对片段的打分与记录时的 AI 中位数。
type Sample struct {
	Metric          string  `json:"metric" yaml:"metric"`
	Source          string  `json:"source" yaml:"source"`
	Unit            string  `json:"unit" yaml:"unit"`
	Paragraph       int     `json:"paragraph" yaml:"paragraph"`
	ParagraphSHA256 string  `json:"paragraph_sha256" yaml:"paragraph_sha256"`
	AIMedian        float64 `json:"ai_median" yaml:"ai_median"`
	AuthorScore     int     `json:"author_score" yaml:"author_score"`
	Confirmation    string  `json:"confirmation" yaml:"confirmation"`
	RecordedAt      string  `json:"recorded_at" yaml:"recorded_at"`
}

// Calibration 是已读取的校准样本；文件不存在时 Samples 为空、doc 为 nil。
type Calibration struct {
	Samples []Sample
	doc     *yaml.Node
	data    []byte
}

// Agreement 是一个指标在其配置层中的校准统计。Rate 在没有样本时为 nil。
type Agreement struct {
	Samples  int      `json:"samples"`
	Agreed   int      `json:"agreed"`
	Rate     *float64 `json:"rate"`
	Advisory bool     `json:"-"`
}

// Agreement 按指标 m 的 (source, id) 统计一致率：|ai_median − author_score| 不大于 tolerance 的样本比例；样本数不少于 min_samples
// 且一致率低于 min_agreement 时为仅供参考（Advisory）。c 为 nil 时没有样本。
func (c *Calibration) Agreement(m resolve.ScoredMetric) Agreement {
	var a Agreement
	if c == nil {
		return a
	}
	for _, s := range c.Samples {
		if s.Metric != m.ID || s.Source != m.Source {
			continue
		}
		a.Samples++
		if math.Abs(s.AIMedian-float64(s.AuthorScore)) <= float64(m.Tolerance) {
			a.Agreed++
		}
	}
	if a.Samples > 0 {
		rate := float64(a.Agreed) / float64(a.Samples)
		a.Rate = &rate
		a.Advisory = a.Samples >= m.MinSamples && rate < m.MinAgreement
	}
	return a
}

// LoadCalibration 读取并校验工作区的 calibration.yaml；不存在时返回空的校准。
func LoadCalibration(root string) (*Calibration, []workspace.Problem) {
	doc, data, ps := readDoc(root, CalibrationPath)
	if ps != nil {
		return nil, ps
	}
	if doc == nil {
		return &Calibration{Samples: []Sample{}}, nil
	}
	if ps := schema.Named("calibration").ValidateNode(doc); len(ps) > 0 {
		return nil, workspace.FromSchema(CalibrationPath, ps)
	}
	var f struct {
		Samples []Sample `json:"samples"`
	}
	catalog.Decode(doc, &f)
	return &Calibration{Samples: f.Samples, doc: doc, data: data}, nil
}

// CheckCalibration 做整体校验中与校准样本有关的部分；没有 calibration.yaml 时不检查。
func CheckCalibration(root string) (checked []string, problems []workspace.Problem) {
	if _, err := os.Lstat(filepath.Join(root, filepath.FromSlash(CalibrationPath))); errors.Is(err, fs.ErrNotExist) {
		return nil, nil
	}
	_, ps := LoadCalibration(root)
	return []string{CalibrationPath}, ps
}

// Calibrate 把样本 s 写入 calibration.yaml：同一单元、指标、段落与 paragraph_sha256 已有样本时替换它，否则追加。返回变更的路径。
func (c *Calibration) Calibrate(root string, s Sample) ([]string, []workspace.Problem) {
	anchored := func(field string) []workspace.Problem {
		return []workspace.Problem{errorAt(CodeAnchoredValue, CalibrationPath, field, "该处经由 YAML 锚点、别名或合并键得到，wa-cli 无法只修改这一处；请先去掉锚点、别名与合并键")}
	}
	doc := c.doc
	if doc == nil || len(doc.Content) == 0 {
		root := &yaml.Node{Kind: yaml.MappingNode}
		root.Content = append(root.Content, workspace.Str("schema_version"), workspace.Int(1), workspace.Str("samples"), &yaml.Node{Kind: yaml.SequenceNode, Tag: "!!seq"})
		doc = &yaml.Node{Kind: yaml.DocumentNode, HeadComment: "校准（Calibration）样本：作者的打分与记录时的 AI 中位数；请用 wa-cli metrics calibrate 修改。", Content: []*yaml.Node{root}}
	}
	root0 := doc.Content[0]
	samples, ok := workspace.Lookup(root0, "samples")
	if !workspace.Plain(root0) || workspace.HasMerge(root0) || !ok || samples == nil {
		return nil, anchored("samples")
	}
	n := flow(s)
	if i := slices.IndexFunc(c.Samples, func(o Sample) bool {
		return o.Unit == s.Unit && o.Metric == s.Metric && o.Paragraph == s.Paragraph && o.ParagraphSHA256 == s.ParagraphSHA256
	}); i >= 0 {
		if workspace.HasAnchor(samples.Content[i]) {
			return nil, anchored(fmt.Sprintf("samples[%d]", i))
		}
		samples.Content[i] = n
		c.Samples[i] = s
	} else {
		samples.Content = append(samples.Content, n)
		c.Samples = append(c.Samples, s)
	}
	workspace.BlockSequence(root0, "samples")
	data, err := workspace.Encode(doc)
	if err != nil {
		return nil, []workspace.Problem{errorAt(workspace.CodeWriteFailed, CalibrationPath, "", "生成 calibration.yaml 失败："+err.Error())}
	}
	written, p := schema.Parse(data)
	if p != nil {
		return nil, workspace.FromSchema(CalibrationPath, []schema.Problem{*p})
	}
	if ps := schema.Named("calibration").ValidateNode(written); len(ps) > 0 {
		return nil, workspace.FromSchema(CalibrationPath, ps)
	}
	c.doc = written
	if bytes.Equal(c.data, data) {
		return nil, nil
	}
	if err := workspace.WriteFile(root, CalibrationPath, data); err != nil {
		return nil, []workspace.Problem{errorAt(workspace.CodeWriteFailed, CalibrationPath, "", "写入失败："+err.Error())}
	}
	c.data = data
	return []string{CalibrationPath}, nil
}
