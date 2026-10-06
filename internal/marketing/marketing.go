// Package marketing 管理单元目录下的营销要素（Marketing Elements）marketing.yaml：标题候选（Headline Candidates）、
// 文章摘要（Digest）、开头钩子（Hook）与行动号召（CTA），以及作者选定标题的决策。选定的标题不另存，由 headline_decision
// 指向的决策推导。本包加载与校验营销要素、按字段修改它、把标题候选登记为决策，并给出仍缺的要素，供 internal/work 计算
// 关卡的阻塞项。本包不 import internal/work，单元目录由调用方传入；只返回结构化的问题，由命令层转换为诊断（Diagnostic）。
package marketing

import (
	"fmt"
	"os"
	"path/filepath"
	"slices"
	"strings"

	"github.com/kw12121212/writing-anything/internal/catalog"
	"github.com/kw12121212/writing-anything/internal/decision"
	"github.com/kw12121212/writing-anything/internal/schema"
	"github.com/kw12121212/writing-anything/internal/workspace"
	"go.yaml.in/yaml/v3"
)

// 营销要素的错误码（Error Code）。已发布的错误码不得改名或复用。
const (
	CodeInvalid          = "marketing.invalid"
	CodeMissingField     = "marketing.missing-field"
	CodeHeadlineUnasked  = "marketing.headline-unasked"
	CodeHeadlineStale    = "marketing.headline-stale"
	CodeHeadlineNotFound = "marketing.headline-not-found"
	CodeAlreadyAsked     = "marketing.already-asked"
	CodeAnchoredValue    = "marketing.anchored-value"
	CodeTooManyHeadlines = "marketing.too-many-headlines"
)

// 单元目录中营销要素的文件名：marketing.yaml 是结构化的营销要素，marketing.md 是自由文本，不含可检查的字段。
const (
	FileName = "marketing.yaml"
	MDName   = "marketing.md"
)

// 营销要素的字段名，按 marketing.yaml 中的固定顺序排列。开头钩子的字段名是 opening：hook 是可执行内容检查保留的字段名。
const (
	FieldHeadlines        = "headlines"
	FieldDigest           = "digest"
	FieldOpening          = "opening"
	FieldCTA              = "cta"
	FieldHeadlineDecision = "headline_decision"
)

var order = []string{"schema_version", FieldHeadlines, FieldDigest, FieldOpening, FieldCTA, FieldHeadlineDecision}

// MinHeadlines 是关卡要求的标题候选数：至少两个，供作者选定。
const MinHeadlines = 2

// 标题决策的状态。
const (
	StateNone    = "none"    // 还没有标题决策
	StateOpen    = "open"    // 决策尚未决定
	StateDecided = "decided" // 已决定，选项与当前候选一致
	StateStale   = "stale"   // 决策不存在，或选项文字与当前候选不一致
	StateUnknown = "unknown" // 决策队列无法加载
)

// Headline 是一个标题候选。
type Headline struct {
	ID   string `json:"id"`
	Text string `json:"text"`
}

// Marketing 是一份已通过校验的营销要素；未设置的文本字段为空字符串。
type Marketing struct {
	Path             string     `json:"-"` // marketing.yaml 相对项目目录的路径
	Headlines        []Headline `json:"headlines"`
	Digest           string     `json:"digest"`
	Opening          string     `json:"opening"`
	CTA              string     `json:"cta"`
	HeadlineDecision string     `json:"headline_decision"`
}

// Queue 是推导标题决策状态时使用的决策队列；Invalid 为 true 时队列无法加载，不判断标题决策。
type Queue struct {
	Decisions []decision.Decision
	Invalid   bool
}

// LoadQueue 加载 root 下的决策队列。
func LoadQueue(root string) Queue {
	ds, _, ps := decision.Load(root)
	return Queue{Decisions: ds, Invalid: len(ps) > 0}
}

func errorAt(code, path, field, msg string) workspace.Problem {
	return workspace.Problem{Code: code, Severity: workspace.SeverityError, Path: path, Field: field, Message: msg}
}

// Path 返回单元目录 dir 下 marketing.yaml 的路径。
func Path(dir string) string { return dir + "/" + FileName }

// Load 读取并校验单元目录 dir 下的 marketing.yaml：Schema 与标题候选 id 唯一。不存在时返回 nil 与 nil；途经符号链接或不是
// 普通文件时报告 artifact.not-found。
func Load(root, dir string) (*Marketing, []workspace.Problem) {
	_, m, ps := load(root, dir)
	return m, ps
}

func load(root, dir string) (*yaml.Node, *Marketing, []workspace.Problem) {
	rel := Path(dir)
	data, ok, err := workspace.ReadRegular(root, rel)
	switch {
	case err != nil:
		return nil, nil, []workspace.Problem{errorAt(workspace.CodeReadFailed, rel, "", "读取失败："+err.Error())}
	case !ok:
		if _, err := os.Lstat(filepath.Join(root, filepath.FromSlash(rel))); err == nil {
			return nil, nil, []workspace.Problem{errorAt(schema.CodeNotFound, rel, "", "marketing.yaml 途经符号链接或不是普通文件，按不存在处理；请把它换成单元目录中的普通文件")}
		}
		return nil, nil, nil
	}
	doc, p := schema.Parse(data)
	if p != nil {
		return nil, nil, workspace.FromSchema(rel, []schema.Problem{*p})
	}
	m, ps := parse(rel, doc)
	if m == nil {
		return nil, nil, ps
	}
	return doc, m, nil
}

// parse 按 Schema 与领域规则（标题候选 id 唯一）校验已解析的文档 doc。
func parse(rel string, doc *yaml.Node) (*Marketing, []workspace.Problem) {
	if ps := schema.Named("marketing").ValidateNode(doc); len(ps) > 0 {
		return nil, workspace.FromSchema(rel, ps)
	}
	var m Marketing
	catalog.Decode(doc, &m)
	m.Path = rel
	var out []workspace.Problem
	seen := map[string]bool{}
	for i, h := range m.Headlines {
		if seen[h.ID] {
			out = append(out, errorAt(catalog.CodeDuplicateID, rel, fmt.Sprintf("headlines[%d].id", i), "标题候选 id "+h.ID+" 重复"))
		}
		seen[h.ID] = true
	}
	if len(out) > 0 {
		return nil, out
	}
	return &m, nil
}

// HeadlineState 推导标题决策的状态与选定的标题（只在 StateDecided 时非 nil）。
func (m *Marketing) HeadlineState(q Queue) (string, *Headline) {
	if m.HeadlineDecision == "" {
		return StateNone, nil
	}
	if q.Invalid {
		return StateUnknown, nil
	}
	i := slices.IndexFunc(q.Decisions, func(d decision.Decision) bool { return d.ID == m.HeadlineDecision })
	if i < 0 {
		return StateStale, nil
	}
	d := q.Decisions[i]
	if len(d.Options) != len(m.Headlines) {
		return StateStale, nil
	}
	for k, o := range d.Options {
		if o.Text != m.Headlines[k].Text {
			return StateStale, nil
		}
	}
	if d.Status != decision.StatusDecided || d.Choice == nil {
		return StateOpen, nil
	}
	k := slices.IndexFunc(d.Options, func(o decision.Option) bool { return o.ID == *d.Choice })
	if k < 0 {
		return StateStale, nil
	}
	return StateDecided, &m.Headlines[k]
}

// Question 是一个仍需补齐的营销要素。
type Question struct {
	Code    string
	Field   string
	Message string
}

// Questions 返回营销要素 m（nil 表示没有可检查的字段）仍需补齐的要素：标题候选少于两个，摘要、钩子与行动号召未填写，
// 标题还没有交给作者选定，或标题决策已过期。决策队列无法加载时不判断标题决策。
func Questions(m *Marketing, q Queue) []Question {
	if m == nil {
		m = &Marketing{}
	}
	var out []Question
	if n := len(m.Headlines); n < MinHeadlines {
		out = append(out, Question{Code: CodeMissingField, Field: FieldHeadlines,
			Message: fmt.Sprintf("营销要素（Marketing Elements）缺少标题候选（Headline Candidates）：至少需要 %d 个供作者选定，现在有 %d 个；请用 wa-cli marketing set --headline 写入", MinHeadlines, n)})
	}
	for _, f := range []struct{ field, value, title string }{
		{FieldDigest, m.Digest, "文章摘要（Digest），即分享卡片与消息列表中显示的简介"},
		{FieldOpening, m.Opening, "开头钩子（Hook），即让读者想读下去的开头"},
		{FieldCTA, m.CTA, "行动号召（CTA），即引导读者关注、购买或报名的结尾"},
	} {
		if strings.TrimSpace(f.value) == "" {
			out = append(out, Question{Code: CodeMissingField, Field: f.field,
				Message: fmt.Sprintf("营销要素（Marketing Elements）缺少 %s：%s；请用 wa-cli marketing set --%s 写入", f.field, f.title, f.field)})
		}
	}
	if len(m.Headlines) < MinHeadlines {
		return out
	}
	if n := len(m.Headlines); n > decision.MaxOptions {
		return append(out, Question{Code: CodeTooManyHeadlines, Field: FieldHeadlines,
			Message: fmt.Sprintf("标题候选有 %d 个，超过标题决策的选项上限 %d 个，无法交给作者选定；请用 wa-cli marketing set --remove-headline 删掉一些", n, decision.MaxOptions)})
	}
	switch state, _ := m.HeadlineState(q); state {
	case StateNone:
		out = append(out, Question{Code: CodeHeadlineUnasked, Field: FieldHeadlineDecision,
			Message: "标题候选还没有交给作者选定；请用 wa-cli marketing ask 把候选登记为标题决策，再请作者用 wa-cli decide 选定"})
	case StateStale:
		out = append(out, Question{Code: CodeHeadlineStale, Field: FieldHeadlineDecision,
			Message: fmt.Sprintf("标题决策 %s 已过期：决策不存在，或标题候选在提问之后改过；请用 wa-cli marketing ask 重新登记，请作者重新选定", m.HeadlineDecision)})
	}
	return out
}

// Gate 是单元目录 dir 下营销要素的关卡结果。
type Gate struct {
	Path      string              // 阻塞项的路径：marketing.yaml，只有 marketing.md 时为 marketing.md
	Invalid   []workspace.Problem // marketing.yaml 不通过校验时的问题；非空时 Questions 为空
	Questions []Question
}

// Check 计算单元目录 dir 下营销要素的关卡：yamlPresent 与 mdPresent 报告 marketing.yaml 与 marketing.md 是否按必需产物的
// 规则存在，两者都不存在时返回 nil（由缺失产物报告）。只有 marketing.md 时视为没有填写任何要素。
func Check(root, dir string, yamlPresent, mdPresent bool, q Queue) *Gate {
	if yamlPresent {
		m, ps := Load(root, dir)
		if len(ps) > 0 {
			return &Gate{Path: Path(dir), Invalid: ps}
		}
		if m != nil {
			return &Gate{Path: Path(dir), Questions: Questions(m, q)}
		}
	}
	if mdPresent {
		return &Gate{Path: dir + "/" + MDName, Questions: Questions(nil, q)}
	}
	return nil
}

// CheckUnitArtifacts 校验 dirs 中每个单元目录下存在的 marketing.yaml（包括途经符号链接、按不存在处理的），返回已校验的路径
// 与全部问题。
func CheckUnitArtifacts(root string, dirs []string) (checked []string, problems []workspace.Problem) {
	for _, dir := range dirs {
		if _, err := os.Lstat(filepath.Join(root, filepath.FromSlash(Path(dir)))); err != nil {
			continue
		}
		checked = append(checked, Path(dir))
		_, ps := Load(root, dir)
		problems = append(problems, ps...)
	}
	return checked, problems
}

// CheckArtifact 对已通过 Schema 校验的 marketing.yaml（rel）做领域检查。
func CheckArtifact(root, rel string) []workspace.Problem {
	_, ps := Load(root, strings.TrimSuffix(rel, "/"+FileName))
	return ps
}
