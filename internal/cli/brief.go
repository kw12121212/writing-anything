package cli

import (
	"fmt"
	"io"
	"slices"
	"strings"

	"github.com/kw12121212/writing-anything/internal/brief"
	"github.com/kw12121212/writing-anything/internal/catalog"
	"github.com/kw12121212/writing-anything/internal/draft"
	"github.com/kw12121212/writing-anything/internal/schema"
	"github.com/kw12121212/writing-anything/internal/work"
	"github.com/kw12121212/writing-anything/internal/workspace"
)

const briefViewHelp = "写作简报：unit、path、audience、purpose、keyMessages（id、text、sources、decision）、length（min、max）、avoid、sources 与 method（选用的写作法模板（Method Template），null 表示用文体的默认写作法）"

const questionsHelp = "仍需向作者提问的问题数组，每项含 code、field、message 与 question"

var briefSetCommand = &command{
	name:    "brief set",
	summary: "按命名参数写入或修改单元（Unit）的写作简报（Brief）；取值为空字符串时删除该字段",
	args:    []argSpec{{"unit", unitRefHelp, true}},
	flags: []flagSpec{
		dirFlag,
		{"audience", flagString, "受众（Audience）：文章写给谁看"},
		{"purpose", flagString, "写作目的（Purpose）：读者读完后应该知道、相信或去做什么"},
		{"length", flagString, "字数范围，写作 <min>-<max>，如 1500-3000"},
		{"avoid", flagList, "禁区（Off-limits）：不能写或需要回避的内容；可重复，整体替换已有的禁区"},
		{"sources", flagString, "关联素材（Source）id，用逗号分隔；整体替换已有的关联素材"},
		{"message", flagString, "要新增或修改的核心观点（Key Message）id；不存在时新增，此时必须给出 --message-text"},
		{"message-text", flagString, "核心观点的内容，与 --message 一起使用"},
		{"message-sources", flagString, "核心观点出自的素材 id，用逗号分隔，与 --message 一起使用；整体替换"},
		{"message-decision", flagString, "记录作者确认该观点的决策 id，与 --message 一起使用；决策决定后即为作者的确认记录（Confirmation Record）"},
		{"remove-message", flagList, "要删除的核心观点 id；可重复，先于 --message 执行"},
		{"method", flagString, "选用的写作法模板（Method Template）id（见 wa-cli method list）；none 表示不用写作法；空字符串时删除，改用文体的默认写作法"},
	},
	outputKeys: []outputKey{
		{"brief", briefViewHelp + "；失败时为 null"},
		{"questions", questionsHelp + "：未填写的简报必填项（Required Brief Field）与没有出处或未经确认的核心观点"},
	},
	exitCodes: standardExitCodes,
	run:       runBriefSet,
}

var briefShowCommand = &command{
	name:    "brief show",
	summary: "列出单元（Unit）的写作简报（Brief）与仍需向作者提问的问题",
	args:    []argSpec{{"unit", unitRefHelp, true}},
	flags:   []flagSpec{dirFlag},
	outputKeys: []outputKey{
		{"brief", briefViewHelp + "；没有 brief.yaml 时为 null"},
		{"questions", questionsHelp},
	},
	exitCodes: standardExitCodes,
	run:       runBriefShow,
}

type messageView struct {
	ID       string   `json:"id"`
	Text     string   `json:"text"`
	Sources  []string `json:"sources"`
	Decision *string  `json:"decision"`
}

type briefView struct {
	Unit        string        `json:"unit"`
	Path        string        `json:"path"`
	Audience    *string       `json:"audience"`
	Purpose     *string       `json:"purpose"`
	KeyMessages []messageView `json:"keyMessages"`
	Length      *brief.Length `json:"length"`
	Avoid       []string      `json:"avoid"`
	Sources     []string      `json:"sources"`
	Method      *string       `json:"method"`
}

type questionView struct {
	Code     string `json:"code"`
	Field    string `json:"field"`
	Message  string `json:"message"`
	Question string `json:"question"`
}

type briefResult struct {
	Result
	Brief     *briefView     `json:"brief"`
	Questions []questionView `json:"questions"`
}

func optional(s string) *string {
	if s == "" {
		return nil
	}
	return &s
}

func nonNilStrings(s []string) []string {
	if s == nil {
		return []string{}
	}
	return s
}

func viewBrief(ref string, b *brief.Brief) *briefView {
	v := &briefView{Unit: ref, Path: b.Path, Audience: optional(b.Audience), Purpose: optional(b.Purpose), KeyMessages: []messageView{},
		Length: b.Length, Avoid: nonNilStrings(b.Avoid), Sources: nonNilStrings(b.Sources), Method: optional(b.Method)}
	for _, m := range b.KeyMessages {
		v.KeyMessages = append(v.KeyMessages, messageView{ID: m.ID, Text: m.Text, Sources: nonNilStrings(m.Sources), Decision: optional(m.Decision)})
	}
	return v
}

// briefQuestions 返回单元 u 的写作简报 b（可为 nil）仍需向作者提问的问题。
func briefQuestions(w *work.Work, u *work.Unit, b *brief.Brief, q brief.Queue) []questionView {
	out := []questionView{}
	for _, t := range w.Genre.UnitTypes {
		if t.ID != u.Type {
			continue
		}
		for _, qu := range brief.Questions(b, t.BriefRequired, q) {
			out = append(out, questionView{Code: qu.Code, Field: qu.Field, Message: qu.Message, Question: qu.Question})
		}
	}
	return out
}

// splitIDs 把逗号分隔的 id 列表拆开，去掉空白与空项。
func splitIDs(v string) []string {
	var out []string
	for _, s := range strings.Split(v, ",") {
		if s = strings.TrimSpace(s); s != "" {
			out = append(out, s)
		}
	}
	return out
}

// briefEdit 把 brief set 的参数转换为修改；参数取值不合法时返回 input.invalid-value 或 artifact.schema-violation。
// existing 为已有的写作简报（可为 nil），用于判断 --message 是否新增。
func briefEdit(inv *invocation, existing *brief.Brief) (brief.Edit, []workspace.Problem) {
	var e brief.Edit
	str := func(name string) *string {
		if v, ok := inv.values[name]; ok {
			return &v
		}
		return nil
	}
	e.Audience, e.Purpose = str("audience"), str("purpose")
	// 取值只有空白的文本与重复的 id 在修改前报告，诊断指向参数（brief-set）。
	for _, name := range []string{"audience", "purpose", "message-text"} {
		if v, ok := inv.values[name]; ok && v != "" && strings.TrimSpace(v) == "" {
			hint := "不能只含空白；要删除该字段请给出空字符串"
			if name == "message-text" {
				hint = "不能只含空白；核心观点必须有内容，要删除观点请用 --remove-message"
			}
			return e, []workspace.Problem{invalidValue(name, v, hint)}
		}
	}
	if vs := inv.lists["avoid"]; len(vs) > 1 {
		for _, v := range vs {
			if strings.TrimSpace(v) == "" {
				return e, []workspace.Problem{invalidValue("avoid", v, "禁区不能为空；只给出一个空值表示删除全部禁区")}
			}
		}
	}
	for _, name := range []string{"sources", "message-sources"} {
		if v, ok := inv.values[name]; ok {
			list := splitIDs(v)
			for i, id := range list {
				if slices.Contains(list[:i], id) {
					return e, []workspace.Problem{invalidValue(name, v, "素材 id "+id+" 重复")}
				}
			}
		}
	}
	if v, ok := inv.values["length"]; ok {
		e.SetLength = true
		if v != "" {
			l, ok := brief.ParseLength(v)
			if !ok {
				return e, []workspace.Problem{invalidValue("length", v, "应写作 <min>-<max>，min 与 max 为正整数且 min 不大于 max，如 1500-3000")}
			}
			e.Length = l
		}
	}
	if vs, ok := inv.lists["avoid"]; ok {
		e.SetAvoid = true
		if !(len(vs) == 1 && strings.TrimSpace(vs[0]) == "") {
			e.Avoid = vs
		}
	}
	if v, ok := inv.values["sources"]; ok {
		e.SetSources, e.Sources = true, splitIDs(v)
	}
	if v, ok := inv.values["method"]; ok {
		e.Method = &v
	}
	e.Remove = inv.lists["remove-message"]
	id, selected := inv.values["message"]
	text, sources, decision := str("message-text"), str("message-sources"), str("message-decision")
	if !selected {
		for i, v := range []*string{text, sources, decision} {
			if v != nil {
				name := []string{"message-text", "message-sources", "message-decision"}[i]
				return e, []workspace.Problem{invalidValue(name, *v, "需要与 --message <id> 一起使用，指明要修改的核心观点")}
			}
		}
	} else {
		if !brief.ValidID(id) {
			return e, []workspace.Problem{{Code: schema.CodeSchemaViolation, Severity: workspace.SeverityError, Field: "message",
				Message: fmt.Sprintf("核心观点 id %q 不合法：应以小写字母开头，由小写字母、数字与连字符组成", id)}}
		}
		exists := existing != nil && slices.ContainsFunc(existing.KeyMessages, func(m brief.Message) bool { return m.ID == id }) && !slices.Contains(e.Remove, id)
		if !exists && text == nil {
			return e, []workspace.Problem{invalidValue("message", id, "新增核心观点时必须同时给出 --message-text")}
		}
		if exists && text == nil && sources == nil && decision == nil {
			return e, []workspace.Problem{invalidValue("message", id, "没有给出要修改的内容；请给出 --message-text、--message-sources 或 --message-decision")}
		}
		me := &brief.MessageEdit{ID: id, Text: text, Decision: decision}
		if sources != nil {
			s := splitIDs(*sources)
			me.Sources = &s
		}
		e.Message = me
	}
	if e.Audience == nil && e.Purpose == nil && !e.SetLength && !e.SetAvoid && !e.SetSources && e.Message == nil && len(e.Remove) == 0 && e.Method == nil {
		return e, []workspace.Problem{{Code: codeInvalidValue, Severity: workspace.SeverityError,
			Message: "没有给出要修改的字段；至少给出 --audience、--purpose、--length、--avoid、--sources、--message、--remove-message 或 --method 之一"}}
	}
	return e, nil
}

func runBriefSet(inv *invocation) int {
	dir := projectDir(inv)
	res := &briefResult{Questions: []questionView{}}
	problems := workspace.Require(dir)
	edit := false
	if problems == nil {
		var w *work.Work
		var u *work.Unit
		if w, u, problems = work.Resolve(dir, inv.args[0]); u != nil {
			q := brief.LoadQueue(dir)
			unitDir := w.UnitDir(u)
			var existing *brief.Brief
			if existing, problems = brief.Load(dir, unitDir, q); problems == nil {
				var e brief.Edit
				if e, problems = briefEdit(inv, existing); problems == nil {
					var b *brief.Brief
					var changed []string
					ensure := func() ([]string, []workspace.Problem) { return draft.EnsureUnitDir(dir, w, u) }
					if b, changed, problems = brief.Set(dir, unitDir, e, q, ensure); b != nil {
						res.Brief, res.ChangedPaths = viewBrief(u.Ref, b), changed
						res.Questions = briefQuestions(w, u, b, q)
						problems = brief.MethodWarnings(b, catalog.LoadMethods(dir))
					}
				}
				edit = true
			}
		}
	}
	res.Diagnostics = diagnostics(problems, dir)
	if edit {
		// 参数引起的问题（字段为参数名或为空）给出用法；已有 brief.yaml 中的问题保留默认的复查命令。
		for i, d := range res.Diagnostics {
			if d.Path == "" {
				res.Diagnostics[i].Next = []string{"wa-cli help brief set --json"}
				switch d.Code {
				case brief.CodeUnknownSource:
					res.Diagnostics[i].Next = append(res.Diagnostics[i].Next, shellCommand("wa-cli", "source", "list", "--dir", dir, "--json"))
				case brief.CodeUnknownDecision, brief.CodeUnrelatedDecision:
					res.Diagnostics[i].Next = append(res.Diagnostics[i].Next, shellCommand("wa-cli", "decisions", "--all", "--dir", dir, "--json"))
				case brief.CodeMessageNotFound:
					res.Diagnostics[i].Next = append(res.Diagnostics[i].Next, shellCommand("wa-cli", "brief", "show", inv.args[0], "--dir", dir, "--json"))
				}
			}
			if d.Code == brief.CodeAnchoredValue {
				res.Diagnostics[i].Next = []string{"wa-cli help brief set --json"}
			}
		}
	}
	return finish(inv, res, func(out io.Writer) error {
		if res.Brief == nil {
			return nil
		}
		verb := "已更新"
		if len(res.ChangedPaths) == 0 {
			verb = "取值未变，没有修改"
		}
		_, err := io.WriteString(out, fmt.Sprintf("写作简报（Brief）%s：%s\n", verb, res.Brief.Path)+briefText(res.Brief, res.Questions))
		return err
	})
}

func runBriefShow(inv *invocation) int {
	dir := projectDir(inv)
	res := &briefResult{Questions: []questionView{}}
	problems := workspace.Require(dir)
	if problems == nil {
		var w *work.Work
		var u *work.Unit
		if w, u, problems = work.Resolve(dir, inv.args[0]); u != nil {
			q := brief.LoadQueue(dir)
			var b *brief.Brief
			if b, problems = brief.Load(dir, w.UnitDir(u), q); problems == nil {
				if b != nil {
					res.Brief = viewBrief(u.Ref, b)
					problems = brief.MethodWarnings(b, catalog.LoadMethods(dir))
				}
				res.Questions = briefQuestions(w, u, b, q)
			}
		}
	}
	res.Diagnostics = diagnostics(problems, dir)
	return finish(inv, res, func(out io.Writer) error {
		if hasError(res.Diagnostics) {
			return nil
		}
		text := "单元（Unit）" + inv.args[0] + " 还没有写作简报（Brief）\n"
		if res.Brief != nil {
			text = "写作简报（Brief）" + res.Brief.Path + "\n"
		}
		_, err := io.WriteString(out, text+briefText(res.Brief, res.Questions))
		return err
	})
}

// briefText 以人读文字列出写作简报的字段与仍需向作者提问的问题。
func briefText(b *briefView, questions []questionView) string {
	var s strings.Builder
	if b != nil {
		line := func(name string, v *string) {
			if v != nil {
				fmt.Fprintf(&s, "  %s：%s\n", name, *v)
			}
		}
		line("受众", b.Audience)
		line("写作目的", b.Purpose)
		for _, m := range b.KeyMessages {
			from := "没有出处"
			switch {
			case len(m.Sources) > 0:
				from = "素材 " + strings.Join(m.Sources, "、")
			case m.Decision != nil:
				from = "决策 " + *m.Decision
			}
			fmt.Fprintf(&s, "  核心观点 %s：%s（%s）\n", m.ID, m.Text, from)
		}
		if b.Length != nil {
			fmt.Fprintf(&s, "  字数范围：%d-%d\n", b.Length.Min, b.Length.Max)
		}
		if len(b.Avoid) > 0 {
			fmt.Fprintf(&s, "  禁区：%s\n", strings.Join(b.Avoid, "；"))
		}
		if len(b.Sources) > 0 {
			fmt.Fprintf(&s, "  关联素材：%s\n", strings.Join(b.Sources, "、"))
		}
		if b.Method != nil {
			fmt.Fprintf(&s, "  写作法：%s\n", *b.Method)
		}
	}
	for _, q := range questions {
		fmt.Fprintf(&s, "  待提问：%s\n", q.Message)
	}
	return s.String()
}
