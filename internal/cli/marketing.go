package cli

import (
	"fmt"
	"io"
	"os"
	"path/filepath"
	"slices"
	"strings"

	"github.com/kw12121212/writing-anything/internal/brief"
	"github.com/kw12121212/writing-anything/internal/decision"
	"github.com/kw12121212/writing-anything/internal/draft"
	"github.com/kw12121212/writing-anything/internal/marketing"
	"github.com/kw12121212/writing-anything/internal/schema"
	"github.com/kw12121212/writing-anything/internal/work"
	"github.com/kw12121212/writing-anything/internal/workspace"
)

const marketingViewHelp = "营销要素（Marketing Elements）：unit、path、headlines（标题候选（Headline Candidates），每项含 id 与 text）、digest（文章摘要（Digest））、" +
	"opening（开头钩子（Hook））、cta（行动号召（CTA））、headlineDecision（标题决策 id）、headlineState（none、open、decided、stale 或 unknown）与 headline（作者选定的候选，未选定时为 null）"

const marketingQuestionsHelp = "仍需补齐的营销要素数组，每项含 code、field 与 message"

var marketingSetCommand = &command{
	name:    "marketing set",
	summary: "按命名参数写入或修改单元（Unit）的营销要素（Marketing Elements）：标题候选（Headline Candidates）、文章摘要（Digest）、开头钩子（Hook）与行动号召（CTA）；取值为空字符串时删除该字段",
	args:    []argSpec{{"unit", unitRefHelp, true}},
	flags: []flagSpec{
		dirFlag,
		{"headline", flagString, "要新增或修改的标题候选 id；不存在时新增，此时必须给出 --headline-text"},
		{"headline-text", flagString, "标题候选的文字，与 --headline 一起使用"},
		{"remove-headline", flagList, "要删除的标题候选 id；可重复，先于 --headline 执行"},
		{"digest", flagString, "文章摘要（Digest）：分享卡片与消息列表中显示的简介"},
		{"opening", flagString, "开头钩子（Hook）：让读者想读下去的开头"},
		{"cta", flagString, "行动号召（CTA）：引导读者关注、购买或报名的结尾"},
	},
	outputKeys: []outputKey{
		{"marketing", marketingViewHelp + "；失败时为 null"},
		{"questions", marketingQuestionsHelp},
	},
	exitCodes: standardExitCodes,
	run:       runMarketingSet,
}

var marketingShowCommand = &command{
	name:    "marketing show",
	summary: "列出单元（Unit）的营销要素（Marketing Elements）、作者选定的标题与仍需补齐的要素",
	args:    []argSpec{{"unit", unitRefHelp, true}},
	flags:   []flagSpec{dirFlag},
	outputKeys: []outputKey{
		{"marketing", marketingViewHelp + "；没有 marketing.yaml 时为 null"},
		{"questions", marketingQuestionsHelp},
	},
	exitCodes: standardExitCodes,
	run:       runMarketingShow,
}

var marketingAskCommand = &command{
	name: "marketing ask",
	summary: "把单元（Unit）的标题候选（Headline Candidates）登记为决策队列（Decision Queue）中的阻塞决策，请作者用 wa-cli decide 选定标题；" +
		"选择即为作者的确认记录（Confirmation Record）",
	args: []argSpec{{"unit", unitRefHelp, true}},
	flags: []flagSpec{
		dirFlag,
		{"recommend", flagString, "Agent 推荐的标题候选 id"},
		{"rationale", flagString, "推荐的理由"},
	},
	outputKeys: []outputKey{
		{"decision", "新登记的标题决策（字段同 wa-cli decisions）；失败时为 null"},
		{"marketing", marketingViewHelp + "；失败时为 null"},
	},
	exitCodes: standardExitCodes,
	run:       runMarketingAsk,
}

type headlineView struct {
	ID   string `json:"id"`
	Text string `json:"text"`
}

type marketingView struct {
	Unit             string         `json:"unit"`
	Path             string         `json:"path"`
	Headlines        []headlineView `json:"headlines"`
	Digest           *string        `json:"digest"`
	Opening          *string        `json:"opening"`
	CTA              *string        `json:"cta"`
	HeadlineDecision *string        `json:"headlineDecision"`
	HeadlineState    string         `json:"headlineState"`
	Headline         *headlineView  `json:"headline"`
}

type marketingQuestionView struct {
	Code    string `json:"code"`
	Field   string `json:"field"`
	Message string `json:"message"`
}

type marketingResult struct {
	Result
	Marketing *marketingView          `json:"marketing"`
	Questions []marketingQuestionView `json:"questions"`
}

type marketingAskResult struct {
	Result
	Decision  *decisionView  `json:"decision"`
	Marketing *marketingView `json:"marketing"`
}

func viewMarketing(ref string, m *marketing.Marketing, q marketing.Queue) *marketingView {
	state, chosen := m.HeadlineState(q)
	v := &marketingView{Unit: ref, Path: m.Path, Headlines: []headlineView{}, Digest: optional(m.Digest), Opening: optional(m.Opening),
		CTA: optional(m.CTA), HeadlineDecision: optional(m.HeadlineDecision), HeadlineState: state}
	for _, h := range m.Headlines {
		v.Headlines = append(v.Headlines, headlineView(h))
	}
	if chosen != nil {
		h := headlineView(*chosen)
		v.Headline = &h
	}
	return v
}

func marketingQuestions(m *marketing.Marketing, q marketing.Queue, draftPresent bool) []marketingQuestionView {
	out := []marketingQuestionView{}
	for _, qu := range marketing.Questions(m, q, draftPresent) {
		out = append(out, marketingQuestionView(qu))
	}
	return out
}

// draftPresent 报告单元目录下的正文（draft.md）是否已写入：标题按正文选定，正文未写入时 questions
// 不提示标题决策提问。与 work 的阻塞项判定共用 workspace.IsRegular（同一产物在所有入口得出同一结论）。
func draftPresent(root, unitDir string) bool {
	return workspace.IsRegular(root, unitDir+"/"+draft.FileName)
}

// marketingEdit 把 marketing set 的参数转换为修改；参数取值不合法时返回 input.invalid-value 或 artifact.schema-violation。
// existing 为已有的营销要素（可为 nil），用于判断 --headline 是否新增。
func marketingEdit(inv *invocation, existing *marketing.Marketing) (marketing.Edit, []workspace.Problem) {
	var e marketing.Edit
	str := func(name string) *string {
		if v, ok := inv.values[name]; ok {
			return &v
		}
		return nil
	}
	e.Digest, e.Opening, e.CTA = str("digest"), str("opening"), str("cta")
	// 取值只有空白的文本在修改前报告，诊断指向参数（marketing-set）。
	for _, name := range []string{"digest", "opening", "cta", "headline-text"} {
		if v, ok := inv.values[name]; ok && v != "" && strings.TrimSpace(v) == "" {
			hint := "不能只含空白；要删除该字段请给出空字符串"
			if name == "headline-text" {
				hint = "不能只含空白；标题候选必须有文字，要删除候选请用 --remove-headline"
			}
			return e, []workspace.Problem{invalidValue(name, v, hint)}
		}
	}
	e.Remove = inv.lists["remove-headline"]
	id, selected := inv.values["headline"]
	text := str("headline-text")
	switch {
	case !selected && text != nil:
		return e, []workspace.Problem{invalidValue("headline-text", *text, "需要与 --headline <id> 一起使用，指明要修改的标题候选")}
	case selected:
		if !brief.ValidID(id) {
			return e, []workspace.Problem{{Code: schema.CodeSchemaViolation, Severity: workspace.SeverityError, Field: "headline",
				Message: fmt.Sprintf("标题候选 id %q 不合法：应以小写字母开头，由小写字母、数字与连字符组成", id)}}
		}
		exists := existing != nil && slices.ContainsFunc(existing.Headlines, func(h marketing.Headline) bool { return h.ID == id }) && !slices.Contains(e.Remove, id)
		if !exists && text == nil {
			return e, []workspace.Problem{invalidValue("headline", id, "新增标题候选时必须同时给出 --headline-text")}
		}
		if exists && text == nil {
			return e, []workspace.Problem{invalidValue("headline", id, "没有给出要修改的内容；请给出 --headline-text")}
		}
		e.Headline = &marketing.HeadlineEdit{ID: id, Text: text}
	}
	if e.Digest == nil && e.Opening == nil && e.CTA == nil && e.Headline == nil && len(e.Remove) == 0 {
		return e, []workspace.Problem{{Code: codeInvalidValue, Severity: workspace.SeverityError,
			Message: "没有给出要修改的字段；至少给出 --headline、--remove-headline、--digest、--opening 或 --cta 之一"}}
	}
	return e, nil
}

func runMarketingSet(inv *invocation) int {
	dir := projectDir(inv)
	res := &marketingResult{Questions: []marketingQuestionView{}}
	problems := workspace.Require(dir)
	edit := false
	if problems == nil {
		var w *work.Work
		var u *work.Unit
		if w, u, problems = work.Resolve(dir, inv.args[0]); u != nil {
			unitDir := w.UnitDir(u)
			var existing *marketing.Marketing
			if existing, problems = marketing.Load(dir, unitDir); problems == nil {
				var e marketing.Edit
				if e, problems = marketingEdit(inv, existing); problems == nil {
					var m *marketing.Marketing
					var changed []string
					ensure := func() ([]string, []workspace.Problem) { return draft.EnsureUnitDir(dir, w, u) }
					if m, changed, problems = marketing.Set(dir, unitDir, e, ensure); m != nil {
						q := marketing.LoadQueue(dir)
						res.Marketing, res.ChangedPaths = viewMarketing(u.Ref, m, q), changed
						res.Questions = marketingQuestions(m, q, draftPresent(dir, unitDir))
					}
				}
				edit = true
			}
		}
	}
	res.Diagnostics = diagnostics(problems, dir)
	if edit {
		// 参数引起的问题（不填路径）给出用法；已有 marketing.yaml 中的问题保留默认的复查命令。
		for i, d := range res.Diagnostics {
			if d.Path == "" || d.Code == marketing.CodeAnchoredValue {
				res.Diagnostics[i].Next = []string{"wa-cli help marketing set --json"}
				if d.Code == marketing.CodeHeadlineNotFound {
					res.Diagnostics[i].Next = append(res.Diagnostics[i].Next, shellCommand("wa-cli", "marketing", "show", inv.args[0], "--dir", dir, "--json"))
				}
			}
		}
	}
	return finish(inv, res, func(out io.Writer) error {
		if res.Marketing == nil {
			return nil
		}
		verb := "已更新"
		if len(res.ChangedPaths) == 0 {
			verb = "取值未变，没有修改"
		}
		_, err := io.WriteString(out, fmt.Sprintf("营销要素（Marketing Elements）%s：%s\n", verb, res.Marketing.Path)+marketingText(res.Marketing, res.Questions))
		return err
	})
}

func runMarketingShow(inv *invocation) int {
	dir := projectDir(inv)
	res := &marketingResult{Questions: []marketingQuestionView{}}
	problems := workspace.Require(dir)
	if problems == nil {
		var w *work.Work
		var u *work.Unit
		if w, u, problems = work.Resolve(dir, inv.args[0]); u != nil {
			var m *marketing.Marketing
			if m, problems = marketing.Load(dir, w.UnitDir(u)); problems == nil {
				q := marketing.LoadQueue(dir)
				if m != nil {
					res.Marketing = viewMarketing(u.Ref, m, q)
				}
				res.Questions = marketingQuestions(m, q, draftPresent(dir, w.UnitDir(u)))
			}
		}
	}
	res.Diagnostics = diagnostics(problems, dir)
	return finish(inv, res, func(out io.Writer) error {
		if hasError(res.Diagnostics) {
			return nil
		}
		text := "单元（Unit）" + inv.args[0] + " 还没有营销要素（Marketing Elements）\n"
		if res.Marketing != nil {
			text = "营销要素（Marketing Elements）" + res.Marketing.Path + "\n"
		}
		_, err := io.WriteString(out, text+marketingText(res.Marketing, res.Questions))
		return err
	})
}

func runMarketingAsk(inv *invocation) int {
	dir := projectDir(inv)
	res := &marketingAskResult{}
	problems := workspace.Require(dir)
	if problems == nil {
		var w *work.Work
		var u *work.Unit
		if w, u, problems = work.Resolve(dir, inv.args[0]); u != nil {
			var d *decision.Decision
			var m *marketing.Marketing
			var changed []string
			d, m, changed, problems = marketing.Ask(dir, w.UnitDir(u), u.Ref, inv.values["recommend"], inv.values["rationale"])
			res.ChangedPaths = changed // 写入营销要素失败时，已追加的决策队列仍然如实列出
			if d != nil {
				v := decisionOf(*d)
				res.Decision = &v
				res.Marketing = viewMarketing(u.Ref, m, marketing.LoadQueue(dir))
			}
		}
	}
	res.Diagnostics = diagnostics(problems, dir)
	for i, d := range res.Diagnostics {
		show := shellCommand("wa-cli", "marketing", "show", inv.args[0], "--dir", dir, "--json")
		switch d.Code {
		case marketing.CodeAlreadyAsked:
			res.Diagnostics[i].Next = []string{shellCommand("wa-cli", "decisions", "--dir", dir, "--json"), show}
		case marketing.CodeMissingField, schema.CodeSchemaViolation:
			res.Diagnostics[i].Next = []string{show, "wa-cli help marketing set --json"}
		case marketing.CodeHeadlineNotFound:
			res.Diagnostics[i].Next = []string{show, "wa-cli help marketing ask --json"}
		case schema.CodeNotFound:
			// 只有 marketing.yaml 确实不存在时才指向 marketing set；途经符号链接时 set 同样失败，保留默认的复查命令。
			if _, err := os.Lstat(filepath.Join(dir, filepath.FromSlash(d.Path))); strings.HasSuffix(d.Path, "/"+marketing.FileName) && err != nil {
				res.Diagnostics[i].Next = []string{"wa-cli help marketing set --json"}
			}
		}
	}
	return finish(inv, res, func(out io.Writer) error {
		if res.Decision == nil {
			return nil
		}
		_, err := fmt.Fprintf(out, "已登记标题决策 %s：%s；请作者用 wa-cli decide %s --option <选项 id> --confirmation <原话摘要> 选定\n",
			res.Decision.ID, res.Decision.Question, res.Decision.ID)
		return err
	})
}

// marketingText 以人读文字列出营销要素与仍需补齐的要素。
func marketingText(m *marketingView, questions []marketingQuestionView) string {
	var s strings.Builder
	if m != nil {
		for _, h := range m.Headlines {
			mark := ""
			if m.Headline != nil && m.Headline.ID == h.ID {
				mark = "（作者选定）"
			}
			fmt.Fprintf(&s, "  标题候选 %s：%s%s\n", h.ID, h.Text, mark)
		}
		for _, f := range []struct {
			name string
			v    *string
		}{{"文章摘要", m.Digest}, {"开头钩子", m.Opening}, {"行动号召", m.CTA}} {
			if f.v != nil {
				fmt.Fprintf(&s, "  %s：%s\n", f.name, *f.v)
			}
		}
		if m.HeadlineDecision != nil {
			fmt.Fprintf(&s, "  标题决策：%s（%s）\n", *m.HeadlineDecision, m.HeadlineState)
		}
	}
	for _, q := range questions {
		fmt.Fprintf(&s, "  待补齐：%s\n", q.Message)
	}
	return s.String()
}
