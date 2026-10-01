// Package brief 管理单元目录下的写作简报（Brief）brief.yaml：受众、写作目的、核心观点（Key Message）、字数范围、
// 禁区与关联素材。本包加载与校验写作简报、按字段修改它，并按单元类型声明的简报必填项（Required Brief Field）与
// 核心观点的出处给出仍需向作者提问的问题，供 internal/work 计算提问关卡的阻塞项。本包不 import internal/work
// 与 internal/draft，单元目录与必填项由调用方传入；只返回结构化的问题，由命令层转换为诊断（Diagnostic）。
package brief

import (
	"fmt"
	"os"
	"path/filepath"
	"slices"
	"strings"

	"github.com/kw12121212/writing-anything/internal/catalog"
	"github.com/kw12121212/writing-anything/internal/decision"
	"github.com/kw12121212/writing-anything/internal/schema"
	"github.com/kw12121212/writing-anything/internal/source"
	"github.com/kw12121212/writing-anything/internal/workspace"
	"go.yaml.in/yaml/v3"
)

// 写作简报的错误码（Error Code）。已发布的错误码不得改名或复用。
const (
	CodeMissingField       = "brief.missing-field"
	CodeUnsourcedMessage   = "brief.unsourced-message"
	CodeUnconfirmedMessage = "brief.unconfirmed-message"
	CodeInvalid            = "brief.invalid"
	CodeUnknownSource      = "brief.unknown-source"
	CodeUnknownDecision    = "brief.unknown-decision"
	CodeUnrelatedDecision  = "brief.unrelated-decision"
	CodeInvalidLength      = "brief.invalid-length"
	CodeMessageNotFound    = "brief.message-not-found"
	CodeAnchoredValue      = "brief.anchored-value"
	CodeUnknownMethod      = "brief.unknown-method"
)

// 单元目录中写作简报的文件名：brief.yaml 是结构化的写作简报，brief.md 是自由文本，不含可检查的字段。
const (
	FileName = "brief.yaml"
	MDName   = "brief.md"
)

// 写作简报的字段名，按 brief.yaml 中的固定顺序排列。
const (
	FieldAudience    = "audience"
	FieldPurpose     = "purpose"
	FieldKeyMessages = "key_messages"
	FieldLength      = "length"
	FieldAvoid       = "avoid"
	FieldSources     = "sources"
	// FieldMethod 是选用的写作法模板（Method Template）；不是可声明为必填项的字段，排在最后。
	FieldMethod = "method"
)

// Fields 是可以声明为简报必填项的字段，也是 brief.yaml 中字段的固定顺序（schema_version 之后）。
var Fields = []string{FieldAudience, FieldPurpose, FieldKeyMessages, FieldLength, FieldAvoid, FieldSources}

// fieldTitles 是字段的中文名。
var fieldTitles = map[string]string{
	FieldAudience:    "受众",
	FieldPurpose:     "写作目的",
	FieldKeyMessages: "核心观点",
	FieldLength:      "字数范围",
	FieldAvoid:       "禁区",
	FieldSources:     "关联素材",
}

// fieldQuestions 是字段未填写时向作者提问的内置文字。
var fieldQuestions = map[string]string{
	FieldAudience:    "这篇文章写给谁看？他们是什么背景、已经知道什么、最关心什么？",
	FieldPurpose:     "你希望读者读完后知道什么、相信什么或去做什么？",
	FieldKeyMessages: "这篇文章必须传达的核心观点有哪些？每条请尽量用你自己的原话说，并说明出自哪份素材。",
	FieldLength:      "全文大约写多少字？请给出下限和上限。",
	FieldAvoid:       "有没有不能写、不想写或需要回避的内容？",
	FieldSources:     "这篇文章要用到哪些素材（笔记、访谈、过往文章或参考资料）？",
}

// Message 是一个核心观点。Decision 为空表示未关联决策。
type Message struct {
	ID       string   `json:"id"`
	Text     string   `json:"text"`
	Sources  []string `json:"sources"`
	Decision string   `json:"decision"`
}

// Length 是字数范围。
type Length struct {
	Min int `json:"min"`
	Max int `json:"max"`
}

// Brief 是一份已通过校验的写作简报；未设置的文本字段为空字符串，未设置的数组为 nil。
type Brief struct {
	Path        string    `json:"-"` // brief.yaml 相对项目目录的路径
	Audience    string    `json:"audience"`
	Purpose     string    `json:"purpose"`
	KeyMessages []Message `json:"key_messages"`
	Length      *Length   `json:"length"`
	Avoid       []string  `json:"avoid"`
	Sources     []string  `json:"sources"`
	Method      string    `json:"method"` // 写作法模板 id；none 表示不用写作法；空表示用文体的默认写作法
}

// Queue 是计算出处时使用的决策队列；Invalid 为 true 时队列无法加载，不检查决策是否存在，也不把任何决策视为已决定。
type Queue struct {
	Decisions []decision.Decision
	Invalid   bool
}

// LoadQueue 加载 root 下的决策队列。
func LoadQueue(root string) Queue {
	ds, _, ps := decision.Load(root)
	return Queue{Decisions: ds, Invalid: len(ps) > 0}
}

func (q Queue) find(id string) *decision.Decision {
	for i := range q.Decisions {
		if q.Decisions[i].ID == id {
			return &q.Decisions[i]
		}
	}
	return nil
}

func errorAt(code, path, field, msg string) workspace.Problem {
	return workspace.Problem{Code: code, Severity: workspace.SeverityError, Path: path, Field: field, Message: msg}
}

// Path 返回单元目录 dir 下 brief.yaml 的路径。
func Path(dir string) string { return dir + "/" + FileName }

// Load 读取并校验单元目录 dir 下的 brief.yaml：Schema 与 check 中的领域检查。brief.yaml 不是普通文件
// （不存在或途经符号链接）时返回 nil 与 nil。
func Load(root, dir string, q Queue) (*Brief, []workspace.Problem) {
	_, b, ps := load(root, dir, q)
	return b, ps
}

func load(root, dir string, q Queue) (*yaml.Node, *Brief, []workspace.Problem) {
	rel := Path(dir)
	data, ok, err := workspace.ReadRegular(root, rel)
	switch {
	case err != nil:
		return nil, nil, []workspace.Problem{errorAt(workspace.CodeReadFailed, rel, "", "读取失败："+err.Error())}
	case !ok:
		if _, err := os.Lstat(filepath.Join(root, filepath.FromSlash(rel))); err == nil {
			// 存在但途经符号链接或不是普通文件：所有入口都报告同一问题，不读取项目目录之外的内容，也不覆盖它。
			return nil, nil, []workspace.Problem{errorAt(schema.CodeNotFound, rel, "", "brief.yaml 途经符号链接或不是普通文件，按不存在处理；请把它换成单元目录中的普通文件")}
		}
		return nil, nil, nil
	}
	doc, p := schema.Parse(data)
	if p != nil {
		return nil, nil, workspace.FromSchema(rel, []schema.Problem{*p})
	}
	b, ps := parse(root, rel, doc, q)
	if b == nil {
		return nil, nil, ps
	}
	return doc, b, nil
}

// parse 按 Schema 与领域规则校验已解析的文档 doc。
func parse(root, rel string, doc *yaml.Node, q Queue) (*Brief, []workspace.Problem) {
	if ps := schema.Named("brief").ValidateNode(doc); len(ps) > 0 {
		return nil, workspace.FromSchema(rel, ps)
	}
	var b Brief
	catalog.Decode(doc, &b)
	b.Path = rel
	if ps := check(root, rel, &b, q); len(ps) > 0 {
		return nil, ps
	}
	return &b, nil
}

// check 检查已通过 Schema 校验的写作简报：核心观点 id 唯一，引用的素材都已登记，决策都存在且关联本单元，length 的
// min 不大于 max。决策队列无法加载时不检查决策（由决策队列自己的诊断报告）。
func check(root, rel string, b *Brief, q Queue) []workspace.Problem {
	ref := workspace.UnitRef(strings.TrimSuffix(rel, "/"+FileName))
	var out []workspace.Problem
	for i, id := range b.Sources {
		if !source.Registered(root, id) {
			out = append(out, errorAt(CodeUnknownSource, rel, fmt.Sprintf("sources[%d]", i), unknownSource(id)))
		}
	}
	seen := map[string]bool{}
	for i, m := range b.KeyMessages {
		if seen[m.ID] {
			out = append(out, errorAt(catalog.CodeDuplicateID, rel, fmt.Sprintf("key_messages[%d].id", i), "核心观点 id "+m.ID+" 重复"))
		}
		seen[m.ID] = true
		for j, id := range m.Sources {
			if !source.Registered(root, id) {
				out = append(out, errorAt(CodeUnknownSource, rel, fmt.Sprintf("key_messages[%d].sources[%d]", i, j), unknownSource(id)))
			}
		}
		if m.Decision != "" && !q.Invalid {
			field := fmt.Sprintf("key_messages[%d].decision", i)
			switch d := q.find(m.Decision); {
			case d == nil:
				out = append(out, errorAt(CodeUnknownDecision, rel, field, unknownDecision(m.Decision)))
			case !slices.Contains(d.Units, ref):
				out = append(out, errorAt(CodeUnrelatedDecision, rel, field, unrelatedDecision(m.Decision, ref)))
			}
		}
	}
	if b.Length != nil && b.Length.Min > b.Length.Max {
		out = append(out, errorAt(CodeInvalidLength, rel, FieldLength, fmt.Sprintf("字数范围的下限 %d 大于上限 %d", b.Length.Min, b.Length.Max)))
	}
	return out
}

// UnknownMethod 返回写作法 id 不存在的问题（brief.unknown-method），message 列出可用的写作法；rel 为 brief.yaml 的路径
// （参数引起时为空），field 为字段名。
func UnknownMethod(rel, field, id string, set *catalog.MethodSet) workspace.Problem {
	return errorAt(CodeUnknownMethod, rel, field, fmt.Sprintf("写作法（Method Template）%s 不存在；可用的写作法为 %s，不用写作法时取 none", id, strings.Join(set.IDs(), "、")))
}

// checkMethodRef 检查写作简报 b 选用的写作法存在（空与 none 不检查）。写作法是对规范包或内置写作法的引用，不属于写作简报
// 自身的合法性：查看与修改写作简报的命令不检查它，validate 与大纲关卡报告它（workspace-artifacts/cross-artifact-references）。
func checkMethodRef(b *Brief, set *catalog.MethodSet) []workspace.Problem {
	if b == nil || b.Method == "" || b.Method == catalog.MethodNone {
		return nil
	}
	if _, ok := set.Find(b.Method); ok {
		return nil
	}
	return []workspace.Problem{UnknownMethod(b.Path, FieldMethod, b.Method, set)}
}

// MethodWarnings 把写作简报 b 选用的写作法不存在的问题作为 warning 返回：查看与修改写作简报的命令照常执行并报告它。
func MethodWarnings(b *Brief, set *catalog.MethodSet) []workspace.Problem {
	ps := checkMethodRef(b, set)
	for i := range ps {
		ps[i].Severity = workspace.SeverityWarning
	}
	return ps
}

// MethodOf 返回单元目录 dir 下 brief.yaml 中 method 字段的原始取值：文件不存在、无法解析或没有该字段时为空。写作简报的
// 其他问题不影响它，审稿与大纲关卡用它选出同一个写作法。
func MethodOf(root, dir string) string {
	data, ok, _ := workspace.ReadRegular(root, Path(dir))
	if !ok {
		return ""
	}
	doc, p := schema.Parse(data)
	if p != nil {
		return ""
	}
	m, _ := schema.Decode(doc).(map[string]any)
	id, _ := m[FieldMethod].(string)
	return id
}

func unknownSource(id string) string {
	return "素材（Source）" + id + " 未登记；写作简报只能关联已用 wa-cli source add 登记的素材"
}

func unrelatedDecision(id, ref string) string {
	return fmt.Sprintf("决策 %s 没有关联单元 %s，不能作为该单元核心观点的确认记录；请用 wa-cli decision add --unit %s 登记向作者确认该观点的决策", id, ref, ref)
}

func unknownDecision(id string) string {
	return "决策队列（Decision Queue）中没有决策 " + id + "；请先用 wa-cli decision add 登记"
}

// CheckUnitArtifacts 校验 dirs 中每个单元目录下存在的 brief.yaml（包括途经符号链接、按不存在处理的），返回已校验的路径与全部问题。
// q 为调用方已加载的决策队列（load-once-per-command）。
func CheckUnitArtifacts(root string, dirs []string, q Queue) (checked []string, problems []workspace.Problem) {
	var set *catalog.MethodSet
	for _, dir := range dirs {
		if _, err := os.Lstat(filepath.Join(root, filepath.FromSlash(Path(dir)))); err != nil {
			continue
		}
		checked = append(checked, Path(dir))
		b, ps := Load(root, dir, q)
		if set == nil {
			set = catalog.LoadMethods(root) // 写作法目录每条命令只加载一次
		}
		problems = append(append(problems, ps...), checkMethodRef(b, set)...)
	}
	return checked, problems
}

// CheckArtifact 对已通过 Schema 校验的 brief.yaml（rel）做领域检查，并检查选用的写作法存在。
func CheckArtifact(root, rel string) []workspace.Problem {
	b, ps := Load(root, strings.TrimSuffix(rel, "/"+FileName), LoadQueue(root))
	return append(ps, checkMethodRef(b, catalog.LoadMethods(root))...)
}

// Question 是一个仍需向作者提问的问题。Field 为字段名，或核心观点的 key_messages[i]。
type Question struct {
	Code     string
	Field    string
	Message  string
	Question string
}

// Questions 返回写作简报 b（nil 表示没有可检查的字段）仍需向作者提问的问题：required 中每个未填写的必填项，
// 然后是每个没有出处或只关联未决定决策的核心观点。
func Questions(b *Brief, required []string, q Queue) []Question {
	if b == nil {
		b = &Brief{}
	}
	var out []Question
	for _, f := range Fields {
		if !slices.Contains(required, f) || filled(b, f) {
			continue
		}
		out = append(out, Question{Code: CodeMissingField, Field: f, Question: fieldQuestions[f],
			Message: fmt.Sprintf("写作简报（Brief）缺少必填项 %s（%s）；请向作者提问：%s", f, fieldTitles[f], fieldQuestions[f])})
	}
	for i, m := range b.KeyMessages {
		if len(m.Sources) > 0 {
			continue
		}
		field := fmt.Sprintf("key_messages[%d]", i)
		if m.Decision == "" {
			question := fmt.Sprintf("核心观点 %s「%s」出自哪里？请提供包含你原话的素材，或确认这是你的观点。", m.ID, m.Text)
			out = append(out, Question{Code: CodeUnsourcedMessage, Field: field, Question: question,
				Message: fmt.Sprintf("核心观点（Key Message）%s 没有出处：既没有关联素材，也没有作者的确认记录（Confirmation Record）；请向作者提问：%s", m.ID, question)})
			continue
		}
		if d := q.find(m.Decision); q.Invalid || d == nil || d.Status != decision.StatusDecided {
			question := fmt.Sprintf("核心观点 %s「%s」是否是你的观点？请确认决策 %s。", m.ID, m.Text, m.Decision)
			out = append(out, Question{Code: CodeUnconfirmedMessage, Field: field, Question: question,
				Message: fmt.Sprintf("核心观点（Key Message）%s 只关联了尚未决定的决策 %s，还没有作者的确认记录；请向作者提问：%s", m.ID, m.Decision, question)})
		}
	}
	return out
}

// filled 报告字段 f 是否已填写；数组字段为空视为未填写。
func filled(b *Brief, f string) bool {
	switch f {
	case FieldAudience:
		return strings.TrimSpace(b.Audience) != "" // 与 brief set 一致：只含空白（包括全角空格）视为未填写
	case FieldPurpose:
		return strings.TrimSpace(b.Purpose) != ""
	case FieldKeyMessages:
		return len(b.KeyMessages) > 0
	case FieldLength:
		return b.Length != nil
	case FieldAvoid:
		return len(b.Avoid) > 0
	default:
		return len(b.Sources) > 0
	}
}

// Gate 是单元目录 dir 下写作简报的关卡结果。
type Gate struct {
	Path      string              // 阻塞项的路径：brief.yaml，只有 brief.md 时为 brief.md
	Invalid   []workspace.Problem // brief.yaml 不通过校验时的问题；非空时 Questions 为空
	Questions []Question
}

// Check 计算单元目录 dir 下写作简报的关卡：yamlPresent 与 mdPresent 报告 brief.yaml 与 brief.md 是否按必需产物的规则
// 存在，两者都不存在时返回 nil（由缺失产物报告）。brief.yaml 途经符号链接、不是普通文件或不通过校验时给出 Invalid；
// 只有 brief.md 时视为没有填写任何字段。
func Check(root, dir string, yamlPresent, mdPresent bool, required []string, q Queue) *Gate {
	if yamlPresent {
		b, ps := Load(root, dir, q)
		if len(ps) > 0 {
			return &Gate{Path: Path(dir), Invalid: ps}
		}
		if b != nil {
			return &Gate{Path: Path(dir), Questions: Questions(b, required, q)}
		}
		// brief.yaml 在两次检查之间被删除：按只有 brief.md 或都不存在处理。
	}
	if mdPresent {
		return &Gate{Path: dir + "/" + MDName, Questions: Questions(nil, required, q)}
	}
	return nil
}
