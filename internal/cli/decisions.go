package cli

import (
	"fmt"
	"io"
	"slices"
	"strings"

	"github.com/kw12121212/writing-anything/internal/catalog"
	"github.com/kw12121212/writing-anything/internal/decision"
	"github.com/kw12121212/writing-anything/internal/schema"
	"github.com/kw12121212/writing-anything/internal/work"
	"github.com/kw12121212/writing-anything/internal/workspace"
)

const decisionViewHelp = "id、question、options、recommendation、rationale、blocking、units、status、choice、answer、confirmation、channel（确认渠道（Confirmation Channel）：cli 或 preview，未决定为 null）与 decidedAt"

var decisionAddCommand = &command{
	name:    "decision add",
	summary: "在决策队列（Decision Queue）中记录一个等待作者决定的事项",
	flags: []flagSpec{
		dirFlag,
		{"question", flagString, "要请作者决定的问题"},
		{"option", flagList, "一个选项的文本，可重复；按出现顺序编号为 a、b、c…；省略时为自由答复"},
		{"recommend", flagString, "Agent 推荐的选项 id，如 b"},
		{"rationale", flagString, "推荐的理由"},
		{"blocking", flagBool, "阻塞（Blocking）：决定之前关联单元不能推进"},
		{"unit", flagList, "关联的" + unitRefHelp + "；可重复"},
		{"id", flagString, "决策 id；省略时为 dec-<n>"},
	},
	outputKeys: []outputKey{{"decision", "新决策：" + decisionViewHelp + "；失败时为 null"}},
	exitCodes:  standardExitCodes,
	run:        runDecisionAdd,
}

var decisionsCommand = &command{
	name:    "decisions",
	summary: "按阻塞与非阻塞分组列出决策队列（Decision Queue）中待决的事项，便于一次性询问作者",
	flags:   []flagSpec{dirFlag, {"all", flagBool, "同时列出已决定的事项"}},
	outputKeys: []outputKey{
		{"blocking", "阻塞的决策数组，每项含 " + decisionViewHelp},
		{"nonBlocking", "非阻塞的决策数组，字段同 blocking"},
	},
	exitCodes: standardExitCodes,
	run:       runDecisions,
}

var decideCommand = &command{
	name:    "decide",
	summary: "记录作者对一个决策的决定与确认原话摘要，形成确认记录（Confirmation Record）",
	args:    []argSpec{{"id", "决策 id", true}},
	flags: []flagSpec{
		dirFlag,
		{"option", flagString, "作者选择的选项 id；决策有选项时必填"},
		{"answer", flagString, "作者的答复；决策没有选项时必填，有选项时作为补充说明"},
		{"confirmation", flagString, "作者确认原话的摘要"},
	},
	outputKeys: []outputKey{{"decision", "决定后的决策：" + decisionViewHelp + "；失败时为 null"}},
	exitCodes:  standardExitCodes,
	run:        runDecide,
}

type optionView struct {
	ID   string `json:"id"`
	Text string `json:"text"`
}

type decisionView struct {
	ID             string       `json:"id"`
	Question       string       `json:"question"`
	Options        []optionView `json:"options"`
	Recommendation *string      `json:"recommendation"`
	Rationale      *string      `json:"rationale"`
	Blocking       bool         `json:"blocking"`
	Units          []string     `json:"units"`
	Status         string       `json:"status"`
	Choice         *string      `json:"choice"`
	Answer         *string      `json:"answer"`
	Confirmation   *string      `json:"confirmation"`
	Channel        *string      `json:"channel"` // 确认渠道（Confirmation Channel）；未决定为 null
	DecidedAt      *string      `json:"decidedAt"`
}

func decisionOf(d decision.Decision) decisionView {
	v := decisionView{ID: d.ID, Question: d.Question, Options: []optionView{}, Recommendation: d.Recommendation, Rationale: d.Rationale,
		Blocking: d.Blocking, Units: append([]string{}, d.Units...), Status: d.Status, Choice: d.Choice, Answer: d.Answer,
		Confirmation: d.Confirmation, Channel: channelView(d.Status == decision.StatusDecided, d.Channel), DecidedAt: d.DecidedAt}
	for _, o := range d.Options {
		v.Options = append(v.Options, optionView(o))
	}
	return v
}

type decisionResult struct {
	Result
	Decision *decisionView `json:"decision"`
}

type decisionsResult struct {
	Result
	Blocking    []decisionView `json:"blocking"`
	NonBlocking []decisionView `json:"nonBlocking"`
}

// decisionText 以人读文字描述一个决策：问题、选项与推荐，已决定的另列决定与确认记录。
func decisionText(v decisionView) string {
	var b strings.Builder
	fmt.Fprintf(&b, "%s：%s\n", v.ID, v.Question)
	for _, o := range v.Options {
		mark := ""
		if v.Recommendation != nil && *v.Recommendation == o.ID {
			mark = "（推荐）"
		}
		fmt.Fprintf(&b, "  %s. %s%s\n", strings.ToUpper(o.ID), o.Text, mark)
	}
	if v.Rationale != nil {
		fmt.Fprintf(&b, "  推荐理由：%s\n", *v.Rationale)
	}
	if len(v.Units) > 0 {
		fmt.Fprintf(&b, "  关联单元：%s\n", strings.Join(v.Units, "、"))
	}
	if v.Status == decision.StatusDecided {
		choice := ""
		if v.Choice != nil {
			choice = "选择 " + strings.ToUpper(*v.Choice)
		}
		if v.Answer != nil {
			choice = strings.TrimLeft(choice+"，答复："+*v.Answer, "，")
		}
		fmt.Fprintf(&b, "  已决定：%s；作者确认：%s（%s）\n", choice, *v.Confirmation, *v.DecidedAt)
	}
	return b.String()
}

func runDecisionAdd(inv *invocation) int {
	dir := projectDir(inv)
	res := &decisionResult{}
	problems := workspace.Require(dir)
	if problems == nil {
		in := decision.Input{ID: inv.values["id"], Question: inv.values["question"], Options: inv.lists["option"],
			Recommend: inv.values["recommend"], Rationale: inv.values["rationale"], Blocking: inv.flag("blocking"), Units: inv.lists["unit"]}
		var d *decision.Decision
		if d, problems = decision.Add(dir, in, func(ref string) []workspace.Problem {
			_, _, ps := work.Resolve(dir, ref)
			return ps
		}); d != nil {
			v := decisionOf(*d)
			res.Decision, res.ChangedPaths = &v, []string{decision.Path}
		}
	}
	res.Diagnostics = diagnostics(problems, dir)
	// 由本次参数引起的问题（字段为参数名）指向用法；已有决策队列中的问题保留默认的复查命令。
	for i, d := range res.Diagnostics {
		if d.Code == decision.CodeAnchoredValue || paramField(d.Field, "question", "options", "id", "recommendation") &&
			slices.Contains([]string{decision.CodeUnknownOption, schema.CodeSchemaViolation, catalog.CodeDuplicateID}, d.Code) {
			res.Diagnostics[i].Next = []string{"wa-cli help decision add --json"}
		}
	}
	return finish(inv, res, func(w io.Writer) error {
		if res.Decision == nil {
			return nil
		}
		_, err := fmt.Fprintf(w, "已记录待决事项（Decision Queue）%s", decisionText(*res.Decision))
		return err
	})
}

func runDecisions(inv *invocation) int {
	dir := projectDir(inv)
	res := &decisionsResult{Blocking: []decisionView{}, NonBlocking: []decisionView{}}
	problems := workspace.Require(dir)
	if problems == nil {
		var ds []decision.Decision
		ds, _, problems = decision.Load(dir)
		for _, d := range ds {
			if d.Status != decision.StatusOpen && !inv.flag("all") {
				continue
			}
			if d.Blocking {
				res.Blocking = append(res.Blocking, decisionOf(d))
			} else {
				res.NonBlocking = append(res.NonBlocking, decisionOf(d))
			}
		}
	}
	res.Diagnostics = diagnostics(problems, dir)
	return finish(inv, res, func(w io.Writer) error {
		var b strings.Builder
		if len(res.Blocking)+len(res.NonBlocking) == 0 && !hasError(res.Diagnostics) {
			b.WriteString("没有待决的事项\n")
		}
		for _, g := range []struct {
			title string
			ds    []decisionView
		}{{"阻塞（Blocking）的决策：决定之前关联单元不能推进", res.Blocking}, {"非阻塞的决策", res.NonBlocking}} {
			if len(g.ds) == 0 {
				continue
			}
			b.WriteString(g.title + "\n")
			for _, d := range g.ds {
				b.WriteString(decisionText(d))
			}
		}
		_, err := io.WriteString(w, b.String())
		return err
	})
}

func runDecide(inv *invocation) int {
	dir := projectDir(inv)
	id := inv.args[0]
	res := &decisionResult{}
	problems := workspace.Require(dir)
	if problems == nil {
		var d *decision.Decision
		if d, problems = decision.Decide(dir, id, inv.values["option"], inv.values["answer"], inv.values["confirmation"], now()); d != nil {
			v := decisionOf(*d)
			res.Decision, res.ChangedPaths = &v, []string{decision.Path}
		}
	}
	res.Diagnostics = diagnostics(problems, dir)
	for i, d := range res.Diagnostics {
		switch {
		case d.Code == decision.CodeNotFound || d.Code == decision.CodeAlreadyDecided:
			res.Diagnostics[i].Next = []string{shellCommand("wa-cli", "decisions", "--all", "--dir", dir, "--json")}
		case d.Code == decision.CodeAnchoredValue || d.Code == decision.CodeChoiceRequired ||
			paramField(d.Field, "choice", "confirmation") && slices.Contains([]string{decision.CodeUnknownOption, schema.CodeSchemaViolation}, d.Code):
			res.Diagnostics[i].Next = []string{"wa-cli help decide --json", shellCommand("wa-cli", "decisions", "--dir", dir, "--json")}
		}
	}
	return finish(inv, res, func(w io.Writer) error {
		if res.Decision == nil {
			return nil
		}
		_, err := fmt.Fprintf(w, "已记录作者的决定（Confirmation Record）%s", decisionText(*res.Decision))
		return err
	})
}

// paramField 报告诊断字段 field 是否为某个参数名（或其下标、子字段），即问题由本次参数引起；已有产物中的问题字段
// 带有产物内的路径（如 decisions[0].choice）或为根级字段，不算。
func paramField(field string, params ...string) bool {
	for _, p := range params {
		if field == p || strings.HasPrefix(field, p+"[") || strings.HasPrefix(field, p+".") {
			return true
		}
	}
	return false
}
