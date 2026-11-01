// Package review 管理单元（Unit）的审稿（Review）与验收（Acceptance）：review.yaml 记录 Agent 的 AI 预审（Pre-review）——
// 按评分标准（Rubric）逐项的结论与发现项（Finding），acceptance.yaml 记录验收时的关卡强度（Gate Level）与确认记录
// （Confirmation Record）。本包按单元目录工作，不依赖作品与正文包：正文的段落与 SHA-256 由调用方传入，
// 只返回结构化的问题，由命令层转换为诊断（Diagnostic）。
package review

import (
	"crypto/sha256"
	"encoding/hex"
	"fmt"
	"os"
	"path/filepath"
	"reflect"
	"slices"
	"strings"
	"time"

	"github.com/kw12121212/writing-anything/internal/catalog"
	"github.com/kw12121212/writing-anything/internal/illustration"
	"github.com/kw12121212/writing-anything/internal/schema"
	"github.com/kw12121212/writing-anything/internal/workspace"
	"go.yaml.in/yaml/v3"
)

// 审稿与验收的错误码（Error Code）。已发布的错误码不得改名或复用。
const (
	CodeMissingVerdict    = "review.missing-verdict"
	CodeMissingEvidence   = "review.missing-evidence"
	CodeUnknownRubricItem = "review.unknown-rubric-item"
	CodeDuplicateVerdict  = "review.duplicate-verdict"
	CodeUnknownParagraph  = "review.unknown-paragraph"
	CodeQuoteNotFound     = "review.quote-not-found"
	CodeFindingNotFound   = "review.finding-not-found"
	CodeMissingNote       = "review.missing-note"
	CodeAnchoredValue     = "review.anchored-value"
	CodeReaffirmNoReview  = "review.reaffirm-no-review"

	CodeLintErrors             = "accept.lint-errors"
	CodeReviewMissing          = "accept.review-missing"
	CodeReviewStale            = "accept.review-stale"
	CodeOpenFinding            = "accept.open-finding"
	CodeOpenFeedback           = "accept.open-feedback"
	CodeBlockingRubric         = "accept.blocking-rubric"
	CodeUnconfirmed            = "accept.unconfirmed"
	CodeIncompleteConfirmation = "accept.incomplete-confirmation"
	CodeWrongStage             = "accept.wrong-stage"

	CodeAcceptanceInvalid = "acceptance.invalid"
	CodeAcceptanceStale   = "acceptance.stale"
	// CodeAcceptanceWeakerGate 表示验收时的关卡强度比工作区当前的关卡强度宽松。
	CodeAcceptanceWeakerGate = "acceptance.weaker-gate"
)

// 单元目录中的文件名。draftName 与 draft 包的正文文件名一致（本包不依赖 draft 包）。
const (
	FileName       = "review.yaml"
	AcceptanceName = "acceptance.yaml"
	draftName      = "draft.md"
)

// 发现项状态与评分结论的取值，与 review.schema.json 一致。
var (
	Statuses = []string{"open", "resolved", "waived"}
	Verdicts = []string{"pass", "fail"}
)

// Verdict 是一个评分项的审稿结论。
type Verdict struct {
	Item       string `json:"item"`
	Verdict    string `json:"verdict"`
	Evidence   string `json:"evidence"`
	Paragraphs []int  `json:"paragraphs"`
}

// Finding 是一个审稿发现项；Paragraph 为段落编号，从 1 开始，与 wa-cli provenance 一致。
type Finding struct {
	ID         string  `json:"id"`
	Rubric     *string `json:"rubric"`
	Paragraph  int     `json:"paragraph"`
	Quote      *string `json:"quote"`
	Message    string  `json:"message"`
	Suggestion *string `json:"suggestion"`
	Status     string  `json:"status"`
	Note       *string `json:"note"`
}

// Review 是 review.yaml 的内容。
type Review struct {
	DraftSHA256 string     `json:"draft_sha256"`
	Rubric      []Verdict  `json:"rubric"`
	Findings    []Finding  `json:"findings"`
	Reaffirms   []Reaffirm `json:"reaffirms,omitempty"`
}

// Reaffirm 是一次重申（Reaffirm）：小改动后沿用既有结论，记录变化摘要与本次标记的发现项。
type Reaffirm struct {
	At          string   `json:"at"`
	Summary     string   `json:"summary"`
	DraftSHA256 string   `json:"draft_sha256"`
	Resolved    []string `json:"resolved"`
	Waived      []string `json:"waived"`
}

// Acceptance 是 acceptance.yaml 的内容。
type Acceptance struct {
	GateLevel         string  `json:"gate_level"`
	DraftSHA256       string  `json:"draft_sha256"`
	IllustrationsSHA  *string `json:"illustrations_sha256"` // 验收时的配图摘要；单元没有配图时缺省（nil）
	ConfirmedByAuthor bool    `json:"confirmed_by_author"`
	Confirmation      *string `json:"confirmation"`
	Channel           *string `json:"channel"` // 确认渠道（Confirmation Channel）；只在经作者确认时记录，缺失按 cli 解释
	AcceptedAt        string  `json:"accepted_at"`
}

func errorAt(code, path, field, msg string) workspace.Problem {
	return workspace.Problem{Code: code, Severity: workspace.SeverityError, Path: path, Field: field, Message: msg}
}

// Path 返回单元目录 dir 下 review.yaml 的路径。
func Path(dir string) string { return dir + "/" + FileName }

// AcceptancePath 返回单元目录 dir 下 acceptance.yaml 的路径。
func AcceptancePath(dir string) string { return dir + "/" + AcceptanceName }

// SHA256 返回正文的 SHA-256 十六进制，与 wa-cli diff 的 base 一致。
func SHA256(content []byte) string {
	sum := sha256.Sum256(content)
	return hex.EncodeToString(sum[:])
}

// DraftSHA256 返回单元目录 dir 下当前正文的 SHA-256；没有可读取的正文时为空。
func DraftSHA256(root, dir string) string {
	data, ok, _ := workspace.ReadRegular(root, dir+"/"+draftName)
	if !ok {
		return ""
	}
	return SHA256(data)
}

// loadDoc 读取 rel 处的产物并按 Schema kind 校验；文件不存在（或途经符号链接、不是普通文件）时返回 nil 与 nil。
// 存在但途经符号链接或不是普通文件时，所有入口（advance、next、validate、review show 与 accept）都报告同一问题
// （workspace-artifacts/validated-path-segments），不读取项目目录之外的内容，也不把它当作不存在。
func loadDoc(root, rel, kind string, out any) (*yaml.Node, []workspace.Problem) {
	if _, ok, _ := workspace.ReadRegular(root, rel); !ok {
		if exists(root, rel) {
			return nil, []workspace.Problem{errorAt(schema.CodeNotFound, rel, "", rel[strings.LastIndex(rel, "/")+1:]+" 途经符号链接或不是普通文件，按不存在处理；请把它换成单元目录中的普通文件")}
		}
		return nil, nil
	}
	doc, ps := workspace.Read(root, rel)
	if doc == nil {
		return nil, ps
	}
	if ps := schema.Named(kind).ValidateNode(doc); len(ps) > 0 {
		return nil, workspace.FromSchema(rel, ps)
	}
	catalog.Decode(doc, out)
	return doc, nil
}

// exists 报告 rel 处是否有文件系统条目（不跟随最后一级的符号链接）。
func exists(root, rel string) bool {
	_, err := os.Lstat(filepath.Join(root, filepath.FromSlash(rel)))
	return err == nil
}

// checkReview 检查已通过 Schema 校验的审稿：评分项结论与发现项 id 都不重复。
func checkReview(rel string, r *Review) []workspace.Problem {
	var out []workspace.Problem
	items := map[string]bool{}
	for i, v := range r.Rubric {
		if items[v.Item] {
			out = append(out, errorAt(CodeDuplicateVerdict, rel, fmt.Sprintf("rubric[%d].item", i), "评分项 "+v.Item+" 有多个结论"))
		}
		items[v.Item] = true
	}
	ids := map[string]bool{}
	for i, f := range r.Findings {
		if ids[f.ID] {
			out = append(out, errorAt(catalog.CodeDuplicateID, rel, fmt.Sprintf("findings[%d].id", i), "发现项 id "+f.ID+" 重复"))
		}
		ids[f.ID] = true
	}
	for i, a := range r.Reaffirms {
		for j, id := range a.Resolved {
			if !ids[id] {
				out = append(out, errorAt(CodeFindingNotFound, rel, fmt.Sprintf("reaffirms[%d].resolved[%d]", i, j), "重申标记的发现项 "+id+" 不在审稿的发现项中"))
			}
		}
		for j, id := range a.Waived {
			if !ids[id] {
				out = append(out, errorAt(CodeFindingNotFound, rel, fmt.Sprintf("reaffirms[%d].waived[%d]", i, j), "重申标记的发现项 "+id+" 不在审稿的发现项中"))
			}
		}
	}
	return out
}

// Load 读取并校验单元目录 dir 下的 review.yaml；不存在时返回 nil 与 nil。
func Load(root, dir string) (*Review, []workspace.Problem) {
	rel := Path(dir)
	var r Review
	doc, ps := loadDoc(root, rel, "review", &r)
	if doc == nil {
		return nil, ps
	}
	if ps := checkReview(rel, &r); len(ps) > 0 {
		return nil, ps
	}
	normalizeParagraphs(r.Rubric)
	return &r, nil
}

// normalizeParagraphs 把省略的 paragraphs 规范化为空数组：不针对段落的评分项（如评作者选定的标题）可以不给段落，
// 写出与输出中仍是数组而不是 null（review-record）。
func normalizeParagraphs(vs []Verdict) {
	for i := range vs {
		if vs[i].Paragraphs == nil {
			vs[i].Paragraphs = []int{}
		}
	}
}

// LoadAcceptance 读取并校验单元目录 dir 下的 acceptance.yaml；不存在时返回 nil 与 nil。
func LoadAcceptance(root, dir string) (*Acceptance, []workspace.Problem) {
	var a Acceptance
	doc, ps := loadDoc(root, AcceptancePath(dir), "acceptance", &a)
	if doc == nil {
		return nil, ps
	}
	return &a, nil
}

// CheckArtifact 校验单元目录中的 review.yaml 或 acceptance.yaml（rel）；其他路径返回 nil。
func CheckArtifact(root, rel string) []workspace.Problem {
	dir, name, _ := cutLast(rel)
	switch name {
	case FileName:
		_, ps := Load(root, dir)
		return ps
	case AcceptanceName:
		_, ps := LoadAcceptance(root, dir)
		return ps
	}
	return nil
}

// CheckUnitArtifacts 校验每个单元目录中存在的 review.yaml 与 acceptance.yaml，返回已校验的路径与问题。
func CheckUnitArtifacts(root string, dirs []string) (checked []string, problems []workspace.Problem) {
	for _, dir := range dirs {
		for _, rel := range []string{Path(dir), AcceptancePath(dir)} {
			if exists(root, rel) {
				checked = append(checked, rel)
				problems = append(problems, CheckArtifact(root, rel)...)
			}
		}
	}
	return checked, problems
}

func cutLast(rel string) (dir, name string, ok bool) {
	i := strings.LastIndex(rel, "/")
	if i < 0 {
		return "", rel, false
	}
	return rel[:i], rel[i+1:], true
}

// input 是 wa-cli review record 的输入。
type input struct {
	Rubric   []Verdict `json:"rubric"`
	Findings []struct {
		ID         string  `json:"id"`
		Rubric     *string `json:"rubric"`
		Paragraph  int     `json:"paragraph"`
		Quote      *string `json:"quote"`
		Message    string  `json:"message"`
		Suggestion *string `json:"suggestion"`
	} `json:"findings"`
}

// Record 校验审稿输入 data 并整体替换单元目录 dir 下的 review.yaml。paragraphs 为当前正文各段落的原文（按顺序），
// draftSHA 为当前正文的 SHA-256，rubric 为合并后的评分标准。输入中的问题不填路径，字段为输入中的位置。
// 每个评分项必须恰有一个结论；段落编号必须在 1 与段落数之间；引文必须出现在所引段落中；发现项 id 省略时取 rf-<n>。
// 返回写入的审稿与变更的路径；出错时不修改文件。
func Record(root, dir string, data []byte, paragraphs []string, draftSHA string, rubric []catalog.RubricItem) (*Review, []string, []workspace.Problem) {
	doc, p := schema.Parse(data)
	if p != nil {
		return nil, nil, workspace.FromSchema("", []schema.Problem{*p})
	}
	if ps := schema.Named("review-input").ValidateNode(doc); len(ps) > 0 {
		return nil, nil, workspace.FromSchema("", ps)
	}
	var in input
	catalog.Decode(doc, &in)
	var out []workspace.Problem
	known := map[string]bool{}
	for _, it := range rubric {
		known[it.ID] = true
	}
	paragraph := func(field string, n int) bool {
		if n > len(paragraphs) {
			out = append(out, errorAt(CodeUnknownParagraph, "", field, fmt.Sprintf("正文只有 %d 个段落，没有第 %d 段；段落编号见 wa-cli provenance", len(paragraphs), n)))
			return false
		}
		return true
	}
	seen := map[string]bool{}
	for i, v := range in.Rubric {
		field := fmt.Sprintf("rubric[%d]", i)
		switch {
		case !known[v.Item]:
			out = append(out, errorAt(CodeUnknownRubricItem, "", field+".item", unknownItem(v.Item, rubric)))
		case seen[v.Item]:
			out = append(out, errorAt(CodeDuplicateVerdict, "", field+".item", "评分项 "+v.Item+" 有多个结论；每个评分项只能有一个结论"))
		}
		seen[v.Item] = true
		// 骨架未填完：verdict 与 evidence 为空时逐项指出（而不是笼统的 Schema 错误），field 到具体位置；
		// item 本身未知的项已由 unknown-rubric-item 指出，不再叠加空值提示。
		if known[v.Item] && v.Verdict != "pass" && v.Verdict != "fail" {
			out = append(out, errorAt(CodeMissingVerdict, "", field+".verdict",
				fmt.Sprintf("评分项 %s 的结论（verdict）未填写：骨架未填完；请填为 pass 或 fail", v.Item)))
		}
		if known[v.Item] && strings.TrimSpace(v.Evidence) == "" {
			out = append(out, errorAt(CodeMissingEvidence, "", field+".evidence",
				fmt.Sprintf("评分项 %s 的证据（evidence）未填写：骨架未填完；请写明结论的依据（依据的段落写在 paragraphs）", v.Item)))
		}
		for j, n := range v.Paragraphs {
			paragraph(fmt.Sprintf("%s.paragraphs[%d]", field, j), n)
		}
	}
	for _, it := range rubric {
		if !seen[it.ID] {
			out = append(out, errorAt(CodeMissingVerdict, "", "rubric", fmt.Sprintf("评分项 %s（%s）没有结论；每个评分项都要给出 pass 或 fail 与证据（依据的段落写在 paragraphs，不针对段落时可以省略）；评分项清单见 wa-cli review show 的 rubric", it.ID, it.Title)))
		}
	}
	ids := map[string]bool{}
	for _, f := range in.Findings {
		if f.ID != "" {
			ids[f.ID] = true
		}
	}
	normalized := make([]string, len(paragraphs)) // 每个段落只规范化一次，引文检查的耗时不随发现项数重复扫描
	for i, p := range paragraphs {
		normalized[i] = normalize(p)
	}
	used := map[string]bool{}
	next := 1
	normalizeParagraphs(in.Rubric)
	r := &Review{DraftSHA256: draftSHA, Rubric: in.Rubric, Findings: []Finding{}}
	for i, f := range in.Findings {
		field := fmt.Sprintf("findings[%d]", i)
		if f.Rubric != nil && !known[*f.Rubric] {
			out = append(out, errorAt(CodeUnknownRubricItem, "", field+".rubric", unknownItem(*f.Rubric, rubric)))
		}
		if paragraph(field+".paragraph", f.Paragraph) && f.Quote != nil && !strings.Contains(normalized[f.Paragraph-1], normalize(*f.Quote)) {
			out = append(out, errorAt(CodeQuoteNotFound, "", field+".quote", fmt.Sprintf("引文「%s」不在第 %d 段中", *f.Quote, f.Paragraph)))
		}
		id := f.ID
		if id == "" {
			for ; ; next++ {
				if id = fmt.Sprintf("rf-%d", next); !ids[id] && !used[id] {
					break
				}
			}
		} else if used[id] {
			out = append(out, errorAt(catalog.CodeDuplicateID, "", field+".id", "发现项 id "+id+" 重复"))
		}
		used[id] = true
		r.Findings = append(r.Findings, Finding{ID: id, Rubric: f.Rubric, Paragraph: f.Paragraph, Quote: f.Quote, Message: f.Message, Suggestion: f.Suggestion, Status: "open"})
	}
	if r.Rubric == nil {
		r.Rubric = []Verdict{}
	}
	if len(out) > 0 {
		return nil, nil, out
	}
	rel := Path(dir)
	data, ps := encodeChecked("review", rel, reviewDoc(r))
	if len(ps) > 0 {
		return nil, nil, ps
	}
	if err := workspace.WriteFile(root, rel, data); err != nil {
		return nil, nil, []workspace.Problem{errorAt(workspace.CodeWriteFailed, rel, "", "写入审稿失败："+err.Error())}
	}
	return r, []string{rel}, nil
}

func normalize(s string) string { return strings.ReplaceAll(s, "\r\n", "\n") }

func unknownItem(id string, rubric []catalog.RubricItem) string {
	var ids []string
	for _, it := range rubric {
		ids = append(ids, it.ID)
	}
	if len(ids) == 0 {
		return "评分项 " + id + " 不存在；合并后的评分标准（Rubric）为空"
	}
	return "评分项 " + id + " 不存在；合并后的评分标准（Rubric）有 " + strings.Join(ids, "、")
}

// reviewDoc 生成 review.yaml 的文档节点。
func reviewDoc(r *Review) *yaml.Node {
	verdicts := &yaml.Node{Kind: yaml.SequenceNode, Tag: "!!seq"}
	for _, v := range r.Rubric {
		ps := &yaml.Node{Kind: yaml.SequenceNode, Tag: "!!seq", Style: yaml.FlowStyle}
		for _, n := range v.Paragraphs {
			ps.Content = append(ps.Content, workspace.Int(n))
		}
		verdicts.Content = append(verdicts.Content, mapping("item", workspace.Str(v.Item), "verdict", workspace.Str(v.Verdict),
			"evidence", workspace.Str(v.Evidence), "paragraphs", ps))
	}
	findings := &yaml.Node{Kind: yaml.SequenceNode, Tag: "!!seq"}
	for _, f := range r.Findings {
		kv := []any{"id", workspace.Str(f.ID)}
		if f.Rubric != nil {
			kv = append(kv, "rubric", workspace.Str(*f.Rubric))
		}
		kv = append(kv, "paragraph", workspace.Int(f.Paragraph))
		if f.Quote != nil {
			kv = append(kv, "quote", workspace.Str(*f.Quote))
		}
		kv = append(kv, "message", workspace.Str(f.Message))
		if f.Suggestion != nil {
			kv = append(kv, "suggestion", workspace.Str(*f.Suggestion))
		}
		kv = append(kv, "status", workspace.Str(f.Status))
		findings.Content = append(findings.Content, mapping(kv...))
	}
	if len(r.Rubric) == 0 {
		verdicts.Style = yaml.FlowStyle
	}
	if len(r.Findings) == 0 {
		findings.Style = yaml.FlowStyle
	}
	return &yaml.Node{Kind: yaml.DocumentNode, HeadComment: "审稿（Review）：Agent 的 AI 预审（Pre-review）；请用 wa-cli review record 与 wa-cli review resolve 修改。",
		Content: []*yaml.Node{mapping("schema_version", workspace.Int(1), "draft_sha256", workspace.Str(r.DraftSHA256), "rubric", verdicts, "findings", findings)}}
}

// mapping 由交替的键（string）与值（*yaml.Node）生成映射节点。
func mapping(kv ...any) *yaml.Node {
	m := &yaml.Node{Kind: yaml.MappingNode, Tag: "!!map"}
	for i := 0; i+1 < len(kv); i += 2 {
		m.Content = append(m.Content, workspace.Str(kv[i].(string)), kv[i+1].(*yaml.Node))
	}
	return m
}

// encodeChecked 编码 doc 并重新解析，按 Schema kind 复查（写前复查），通过时返回编码结果。
func encodeChecked(kind, rel string, doc *yaml.Node) ([]byte, []workspace.Problem) {
	data, err := workspace.Encode(doc)
	if err != nil {
		return nil, []workspace.Problem{errorAt(workspace.CodeWriteFailed, rel, "", "生成产物失败："+err.Error())}
	}
	written, p := schema.Parse(data)
	if p != nil {
		p.Line = 0
		return nil, workspace.FromSchema(rel, []schema.Problem{*p})
	}
	if ps := schema.Named(kind).ValidateNode(written); len(ps) > 0 {
		return nil, workspace.FromSchema(rel, ps)
	}
	return data, nil
}

// findingOrder 是发现项中字段的固定顺序。
var findingOrder = []string{"id", "rubric", "paragraph", "quote", "message", "suggestion", "status", "note"}

// Resolve 把单元目录 dir 下 review.yaml 中发现项 id 的状态改为 status（open、resolved 或 waived），note 非 nil 时
// 把处理说明设为 *note；waived 必须有处理说明（本次给出或已有）。保留其他内容与注释；取值未变时不写文件。
// 返回修改后的发现项与变更的路径；出错时不修改文件。
func Resolve(root, dir, id, status string, note *string) (*Finding, []string, []workspace.Problem) {
	rel := Path(dir)
	if !slices.Contains(Statuses, status) {
		return nil, nil, []workspace.Problem{errorAt(schema.CodeSchemaViolation, "", "status", fmt.Sprintf("取值 %q 不在允许范围内（%s）", status, strings.Join(Statuses, "、")))}
	}
	if note != nil && strings.TrimSpace(*note) == "" {
		return nil, nil, []workspace.Problem{errorAt(schema.CodeSchemaViolation, "", "note", "处理说明不能为空")}
	}
	var r Review
	doc, ps := loadDoc(root, rel, "review", &r)
	if len(ps) > 0 {
		return nil, nil, ps
	}
	if doc == nil {
		return nil, nil, []workspace.Problem{errorAt(CodeFindingNotFound, rel, "", "单元还没有审稿（Review）；请先用 wa-cli review record 记录")}
	}
	if ps := checkReview(rel, &r); len(ps) > 0 {
		return nil, nil, ps
	}
	at := slices.IndexFunc(r.Findings, func(f Finding) bool { return f.ID == id })
	if at < 0 {
		return nil, nil, []workspace.Problem{errorAt(CodeFindingNotFound, rel, "", "审稿中没有发现项 "+id)}
	}
	f := r.Findings[at]
	if status == "waived" && note == nil && f.Note == nil {
		return nil, nil, []workspace.Problem{errorAt(CodeMissingNote, "", "note", "豁免（waived）发现项 "+id+" 时必须用 --note 写明理由")}
	}
	m := doc.Content[0]
	var seq *yaml.Node
	for i := 0; i+1 < len(m.Content); i += 2 {
		if m.Content[i].Value == "findings" {
			seq = m.Content[i+1]
		}
	}
	field := fmt.Sprintf("findings[%d]", at)
	if seq == nil || !workspace.Plain(m) || workspace.HasMerge(m) || !workspace.Plain(seq) || workspace.IndexByID(seq, id) != at || !workspace.Plain(seq.Content[at]) || workspace.HasMerge(seq.Content[at]) {
		return nil, nil, []workspace.Problem{anchored(rel, field)}
	}
	item := seq.Content[at]
	before := schema.Decode(doc)
	if k, ok := workspace.SetKey(item, findingOrder, "status", workspace.Str(status)); !ok {
		return nil, nil, []workspace.Problem{anchored(rel, field+"."+k)}
	}
	if note != nil {
		if k, ok := workspace.SetKey(item, findingOrder, "note", workspace.Str(*note)); !ok {
			return nil, nil, []workspace.Problem{anchored(rel, field+"."+k)}
		}
	}
	data, ps := encodeChecked("review", rel, doc)
	if len(ps) > 0 {
		return nil, nil, ps
	}
	written, _ := schema.Parse(data)
	var saved Review
	catalog.Decode(written, &saved)
	out := saved.Findings[at]
	if reflect.DeepEqual(before, schema.Decode(written)) {
		return &out, nil, nil
	}
	if err := workspace.WriteFile(root, rel, data); err != nil {
		return nil, nil, []workspace.Problem{errorAt(workspace.CodeWriteFailed, rel, "", "写入审稿失败："+err.Error())}
	}
	return &out, []string{rel}, nil
}

func anchored(rel, field string) workspace.Problem {
	return errorAt(CodeAnchoredValue, rel, field, "该处经由 YAML 锚点、别名或合并键得到，wa-cli 无法只修改这一处；请先去掉锚点、别名与合并键")
}

// reviewOrder 是 review.yaml 顶层字段的固定顺序。
var reviewOrder = []string{"schema_version", "draft_sha256", "rubric", "findings", "reaffirms"}

// RecordReaffirm 记录一次重申（Reaffirm）：正文小改后沿用既有评分结论与发现项（不重校验 quote——措辞修改后引文移位是
// 重申的常态），把 draft_sha256 更新为当前正文哈希（验收关卡据此不再报过期），resolve 与 waive 指定的发现项必须存在
// 且为 open，summary 为非空白的变化摘要（「这次为什么不用重新审」的留痕）。没有 review.yaml 时报告
// review.reaffirm-no-review，引导整份录入。
func RecordReaffirm(root, dir, summary string, resolve, waive []string, draftSHA string, now time.Time) (*Review, []string, []workspace.Problem) {
	rel := Path(dir)
	if strings.TrimSpace(summary) == "" {
		return nil, nil, []workspace.Problem{errorAt(schema.CodeSchemaViolation, "", "summary", "重申必须用 --summary 写明自上次审稿以来的正文变化摘要（非空白）")}
	}
	var r Review
	doc, ps := loadDoc(root, rel, "review", &r)
	if len(ps) > 0 {
		return nil, nil, ps
	}
	if doc == nil {
		return nil, nil, []workspace.Problem{errorAt(CodeReaffirmNoReview, rel, "", "单元还没有审稿（Review）；重申（Reaffirm）沿用既有结论，请先用 wa-cli review record 整份录入")}
	}
	if ps := checkReview(rel, &r); len(ps) > 0 {
		return nil, nil, ps
	}
	m := doc.Content[0]
	if !workspace.Plain(m) || workspace.HasMerge(m) {
		return nil, nil, []workspace.Problem{anchored(rel, "")}
	}
	mark := func(ids []string, status, field string) []workspace.Problem {
		var seq *yaml.Node
		for i := 0; i+1 < len(m.Content); i += 2 {
			if m.Content[i].Value == "findings" {
				seq = m.Content[i+1]
			}
		}
		for _, id := range ids {
			at := slices.IndexFunc(r.Findings, func(f Finding) bool { return f.ID == id })
			if at < 0 {
				return []workspace.Problem{errorAt(CodeFindingNotFound, rel, field, "审稿中没有发现项 "+id)}
			}
			if r.Findings[at].Status != "open" {
				return []workspace.Problem{errorAt(CodeFindingNotFound, rel, field, "发现项 "+id+" 不是 open 状态（当前 "+r.Findings[at].Status+"），重申只能标记 open 的发现项")}
			}
			if seq == nil || !workspace.Plain(seq) || workspace.IndexByID(seq, id) != at || !workspace.Plain(seq.Content[at]) {
				return []workspace.Problem{anchored(rel, fmt.Sprintf("findings[%d]", at))}
			}
			if k, ok := workspace.SetKey(seq.Content[at], findingOrder, "status", workspace.Str(status)); !ok {
				return []workspace.Problem{anchored(rel, fmt.Sprintf("findings[%d].%s", at, k))}
			}
			r.Findings[at].Status = status
		}
		return nil
	}
	if ps := mark(resolve, "resolved", "resolve[0]"); len(ps) > 0 {
		return nil, nil, ps
	}
	if ps := mark(waive, "waived", "waive[0]"); len(ps) > 0 {
		return nil, nil, ps
	}
	if k, ok := workspace.SetKey(m, reviewOrder, "draft_sha256", workspace.Str(draftSHA)); !ok {
		return nil, nil, []workspace.Problem{anchored(rel, k)}
	}
	r.DraftSHA256 = draftSHA
	// 追加 reaffirms 序列（不存在时创建）与一条重申记录。
	var re *yaml.Node
	for i := 0; i+1 < len(m.Content); i += 2 {
		if m.Content[i].Value == "reaffirms" {
			re = m.Content[i+1]
		}
	}
	if re == nil {
		if k, ok := workspace.SetKey(m, reviewOrder, "reaffirms", &yaml.Node{Kind: yaml.SequenceNode, Tag: "!!seq"}); !ok {
			return nil, nil, []workspace.Problem{anchored(rel, k)}
		}
		for i := 0; i+1 < len(m.Content); i += 2 {
			if m.Content[i].Value == "reaffirms" {
				re = m.Content[i+1]
			}
		}
	} else if !workspace.Plain(re) {
		return nil, nil, []workspace.Problem{anchored(rel, "reaffirms")}
	}
	item := &yaml.Node{Kind: yaml.MappingNode, Content: []*yaml.Node{}}
	seqOf := func(vs []string) *yaml.Node {
		s := &yaml.Node{Kind: yaml.SequenceNode, Tag: "!!seq"}
		for _, v := range vs {
			s.Content = append(s.Content, workspace.Str(v))
		}
		return s
	}
	add := func(k string, v *yaml.Node) { item.Content = append(item.Content, workspace.Str(k), v) }
	add("at", workspace.Str(now.UTC().Format("2006-01-02T15:04:05Z")))
	add("summary", workspace.Str(summary))
	add("draft_sha256", workspace.Str(draftSHA))
	add("resolved", seqOf(nonNilIDs(resolve)))
	add("waived", seqOf(nonNilIDs(waive)))
	re.Content = append(re.Content, item)
	r.Reaffirms = append(r.Reaffirms, Reaffirm{At: now.UTC().Format("2006-01-02T15:04:05Z"), Summary: summary, DraftSHA256: draftSHA,
		Resolved: nonNilIDs(resolve), Waived: nonNilIDs(waive)})
	data, ps := encodeChecked("review", rel, doc)
	if len(ps) > 0 {
		return nil, nil, ps
	}
	if err := workspace.WriteFile(root, rel, data); err != nil {
		return nil, nil, []workspace.Problem{errorAt(workspace.CodeWriteFailed, rel, "", "写入审稿失败："+err.Error())}
	}
	return &r, []string{rel}, nil
}

// nonNilIDs 返回去空白后的 id 列表；结果不为 nil。
func nonNilIDs(ids []string) []string {
	out := []string{}
	for _, id := range ids {
		if id = strings.TrimSpace(id); id != "" {
			out = append(out, id)
		}
	}
	return out
}

// Accept 以命令行渠道（cli）验收，见 AcceptVia。
func Accept(root, dir, level, draftSHA string, confirmed bool, confirmation string, now time.Time) (*Acceptance, []string, []workspace.Problem) {
	return AcceptVia(root, dir, level, draftSHA, confirmed, confirmation, workspace.ChannelCLI, now)
}

// AcceptVia 整体替换单元目录 dir 下的 acceptance.yaml，记录关卡强度、正文 SHA-256、是否经作者确认、确认原话摘要与
// 确认渠道 channel（confirmed 为 false 时都不记录）与验收时间 now（UTC）；单元有配图时另记录当前配图摘要。返回写入的验收与变更的路径。
func AcceptVia(root, dir, level, draftSHA string, confirmed bool, confirmation, channel string, now time.Time) (*Acceptance, []string, []workspace.Problem) {
	rel := AcceptancePath(dir)
	a := &Acceptance{GateLevel: level, DraftSHA256: draftSHA, IllustrationsSHA: illustration.Digest(root, dir), ConfirmedByAuthor: confirmed, AcceptedAt: now.UTC().Format("2006-01-02T15:04:05Z")}
	kv := []any{"schema_version", workspace.Int(1), "gate_level", workspace.Str(level), "draft_sha256", workspace.Str(draftSHA)}
	if a.IllustrationsSHA != nil {
		// 单元有配图时一并锁定配图摘要；没有配图时不写该字段，记录与引入配图前相同。
		kv = append(kv, "illustrations_sha256", workspace.Str(*a.IllustrationsSHA))
	}
	kv = append(kv, "confirmed_by_author", &yaml.Node{Kind: yaml.ScalarNode, Tag: "!!bool", Value: fmt.Sprint(confirmed)})
	if confirmed {
		a.Confirmation, a.Channel = &confirmation, &channel
		kv = append(kv, "confirmation", workspace.Str(confirmation), "channel", workspace.Str(channel))
	}
	kv = append(kv, "accepted_at", workspace.Str(a.AcceptedAt))
	doc := &yaml.Node{Kind: yaml.DocumentNode, HeadComment: "验收（Acceptance）记录；请用 wa-cli accept 写入。", Content: []*yaml.Node{mapping(kv...)}}
	data, ps := encodeChecked("acceptance", rel, doc)
	if len(ps) > 0 {
		return nil, nil, ps
	}
	if err := workspace.WriteFile(root, rel, data); err != nil {
		return nil, nil, []workspace.Problem{errorAt(workspace.CodeWriteFailed, rel, "", "写入验收失败："+err.Error())}
	}
	return a, []string{rel}, nil
}

// Blocker 是一个阻塞项：Code 为错误码，Path 为相关产物，Ref 为相关的发现项、反馈或决策 id（没有时为空）。
type Blocker struct {
	Code    string
	Path    string
	Ref     string
	Message string
}

// AcceptanceBlockers 返回单元目录 dir 下已有验收的阻塞项：acceptance.yaml 不通过校验（含途经符号链接）、或只有
// acceptance.md 时一条 acceptance.invalid；验收时的正文与当前正文不同时一条 acceptance.stale；工作区当前的关卡强度 level
// 比验收时更严时一条 acceptance.weaker-gate。两者都不存在时没有（由缺失产物报告）。
func AcceptanceBlockers(root, dir, level string) []Blocker {
	rel := AcceptancePath(dir)
	a, ps := LoadAcceptance(root, dir)
	switch {
	case !exists(root, rel) && exists(root, dir+"/acceptance.md"):
		// 验收只能由 wa-cli accept 写入 acceptance.yaml；手写的 acceptance.md 不算验收。
		return []Blocker{{Code: CodeAcceptanceInvalid, Path: dir + "/acceptance.md", Message: "验收（Acceptance）只能用 wa-cli accept 记录为 acceptance.yaml；" + dir + "/acceptance.md 不算验收"}}
	case len(ps) > 0:
		return []Blocker{{Code: CodeAcceptanceInvalid, Path: rel, Message: fmt.Sprintf("验收（Acceptance）记录 %s 不通过校验（%s）；请重新用 wa-cli accept 验收", rel, ps[0].Message)}}
	case a == nil:
		return nil
	case a.DraftSHA256 != DraftSHA256(root, dir):
		return []Blocker{{Code: CodeAcceptanceStale, Path: rel, Message: "验收（Acceptance）之后正文已被修改，原来的验收不再代表当前正文；请重新审稿（Review）并用 wa-cli accept 验收"}}
	case !sameDigest(a.IllustrationsSHA, illustration.Digest(root, dir)):
		return []Blocker{{Code: CodeAcceptanceStale, Path: rel, Message: "验收（Acceptance）之后配图（Illustration）或封面图（Cover Image）已被增删、替换或修改，原来的验收不再代表当前图文；请作者重新确认图文后用 wa-cli accept 验收"}}
	case strictness(level) > strictness(a.GateLevel):
		return []Blocker{{Code: CodeAcceptanceWeakerGate, Path: rel, Message: fmt.Sprintf("验收（Acceptance）时的关卡强度为 %s，比工作区当前的 %s 宽松；请按当前关卡强度重新用 wa-cli accept 验收", a.GateLevel, level)}}
	}
	return nil
}

// sameDigest 报告验收记录的配图摘要与当前配图摘要是否一致（都没有配图时一致）。
func sameDigest(recorded, current *string) bool {
	if recorded == nil || current == nil {
		return recorded == nil && current == nil
	}
	return *recorded == *current
}

// Stale 报告验收记录 a 是否已过期：正文的 SHA-256 或单元目录 dir 当前的配图摘要与验收时不同。
func Stale(root, dir string, a *Acceptance, draftSHA string) bool {
	return a.DraftSHA256 != draftSHA || !sameDigest(a.IllustrationsSHA, illustration.Digest(root, dir))
}

// strictness 返回关卡强度的严格程度：fast < standard < strict；未知取值按 standard 处理。
func strictness(level string) int {
	switch level {
	case "fast":
		return 0
	case "strict":
		return 2
	}
	return 1
}

// Pending 是一条未处理完的作者反馈或阻塞决策。
type Pending struct {
	ID   string
	Text string
}

// GateInput 是验收关卡（Gate）的输入。
type GateInput struct {
	Level       string               // 关卡强度：strict、standard 或 fast
	Dir         string               // 单元目录
	LintErrors  int                  // 文本检查中未被豁免的 error 级发现项数
	LintFailed  []workspace.Problem  // 使文本检查无法完成的 error 级问题（如正文过大、论断不通过校验）
	Review      *Review              // 审稿；没有或不通过校验时为 nil
	ReviewBad   []workspace.Problem  // review.yaml 不通过校验的问题
	DraftSHA    string               // 当前正文的 SHA-256
	Rubric      []catalog.RubricItem // 合并后的评分标准
	Feedback    []Pending            // 未处理完（open 或 accepted）的作者反馈
	FeedbackRel string               // feedback.yaml 的路径
	Decisions   []Pending            // 关联该单元的 open 阻塞决策
	DecisionRel string               // 决策队列的路径
	QueueBad    []workspace.Problem  // 决策队列无法加载的问题：无法确认是否有阻塞决策，偏安全地阻塞验收
	Closeout    []workspace.Problem  // 写后收口（Close-out）阻塞项（场景契约要求收口而未收口、收口过期、提议未处理等，
	//                                // 由调用方按 closeout 主规格计算）；原样作为所有关卡强度下的阻塞项输出
	Volume []workspace.Problem // 卷末检查点（Volume Checkpoint）阻塞项（更早各部的检查点未确认或失效）：error 级
	//   // 进入阻塞项、warning 级进入提示，由调用方按关卡强度填充严重级别
	Continuity []workspace.Problem // 连续性审查（Continuity Review）阻塞项：error 级发现项原样进入阻塞项
	// Illustrations 是配图（Illustration）阻塞项：illustrations 主规格 illustration-validation 的 error 级问题，所有关卡强度下
	// 原样进入阻塞项。
	Illustrations []workspace.Problem
	Confirmed     bool // 是否给出了 --confirmed-by-author 与确认原话摘要
}

// Gate 按关卡强度计算验收的阻塞项与 warning（review show 与 accept 共用，只读）：
// 所有关卡强度下，文本检查的 error 级发现项、使文本检查无法完成的问题与阻塞决策阻塞验收；standard 与 strict 另要求审稿存在、
// 通过校验、基于当前正文且每个评分项都有结论，每个 open 的发现项与每条未处理的作者反馈都是阻塞项；strict 另要求
// blocking 的评分项结论不为 fail，并要求作者确认。standard 与 fast 下未确认时给出一条 warning。
func Gate(in GateInput) (blockers, warnings []Blocker) {
	draftRel, reviewRel := in.Dir+"/"+draftName, Path(in.Dir)
	add := func(code, path, ref, msg string) { blockers = append(blockers, Blocker{code, path, ref, msg}) }
	if in.LintErrors > 0 {
		add(CodeLintErrors, draftRel, "", fmt.Sprintf("文本检查（Lint）有 %d 条 error 级发现项（Finding）；确定性硬规则必须先通过", in.LintErrors))
	}
	for _, p := range in.LintFailed {
		add(p.Code, p.Path, "", "文本检查（Lint）无法完成："+p.Message)
	}
	for _, p := range in.QueueBad {
		add("decision.invalid-queue", in.DecisionRel, "", "决策队列（Decision Queue）无法加载，无法确认是否存在阻塞决策："+p.Message)
	}
	for _, d := range in.Decisions {
		add("decision.blocking", in.DecisionRel, d.ID, fmt.Sprintf("阻塞决策（Blocking）%s 尚未决定：%s", d.ID, d.Text))
	}
	for _, p := range in.Closeout {
		add(p.Code, p.Path, "", p.Message)
	}
	for _, p := range in.Volume {
		if p.Severity == workspace.SeverityError {
			add(p.Code, p.Path, "", p.Message)
		}
	}
	for _, p := range in.Continuity {
		add(p.Code, p.Path, "", p.Message)
	}
	for _, p := range in.Illustrations {
		add(p.Code, p.Path, "", p.Message)
	}
	strict := in.Level == "strict"
	if strict || in.Level == "standard" {
		switch {
		case len(in.ReviewBad) > 0:
			for _, p := range in.ReviewBad {
				add(p.Code, p.Path, "", p.Message)
			}
		case in.Review == nil:
			add(CodeReviewMissing, reviewRel, "", "还没有审稿（Review）；请按评分标准（Rubric）审稿后用 wa-cli review record 记录")
		case in.Review.DraftSHA256 != in.DraftSHA:
			add(CodeReviewStale, reviewRel, "", "审稿（Review）之后正文已被修改，审稿不再代表当前正文；请重新审稿并用 wa-cli review record 记录")
		default:
			has := map[string]string{}
			for _, v := range in.Review.Rubric {
				has[v.Item] = v.Verdict
			}
			for _, it := range in.Rubric {
				verdict, ok := has[it.ID]
				switch {
				case !ok:
					add(CodeMissingVerdict, reviewRel, it.ID, fmt.Sprintf("审稿（Review）缺少评分项 %s（%s）的结论；请重新审稿并用 wa-cli review record 记录", it.ID, it.Title))
				case strict && it.Blocking && verdict == "fail":
					add(CodeBlockingRubric, reviewRel, it.ID, fmt.Sprintf("评分项 %s（%s）声明为阻塞，审稿结论为 fail；请修订正文后重新审稿", it.ID, it.Title))
				}
			}
			for _, f := range in.Review.Findings {
				if f.Status == "open" {
					add(CodeOpenFinding, reviewRel, f.ID, fmt.Sprintf("修订任务（Revision Task）：审稿发现项 %s（第 %d 段）尚未处理：%s", f.ID, f.Paragraph, f.Message))
				}
			}
		}
		for _, f := range in.Feedback {
			add(CodeOpenFeedback, in.FeedbackRel, f.ID, fmt.Sprintf("修订任务（Revision Task）：作者反馈（Feedback）%s 尚未处理：%s", f.ID, f.Text))
		}
	}
	for _, p := range in.Volume {
		if p.Severity != workspace.SeverityError {
			warnings = append(warnings, Blocker{Code: p.Code, Path: p.Path, Message: p.Message})
		}
	}
	if !in.Confirmed {
		b := Blocker{CodeUnconfirmed, AcceptancePath(in.Dir), "", "没有作者确认记录（Confirmation Record）：请把审稿与修订情况交给作者确认，再用 --confirmed-by-author 与 --confirmation <作者原话摘要> 验收"}
		if strict {
			blockers = append(blockers, b)
		} else {
			b.Message = "没有作者确认记录（Confirmation Record）：验收将记为未经作者确认（关卡强度为 " + in.Level + "）"
			warnings = append(warnings, b)
		}
	}
	return blockers, warnings
}
