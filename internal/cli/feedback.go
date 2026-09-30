package cli

import (
	"fmt"
	"io"
	"slices"
	"strconv"
	"strings"
	"time"

	"github.com/kw12121212/writing-anything/internal/catalog"
	"github.com/kw12121212/writing-anything/internal/draft"
	"github.com/kw12121212/writing-anything/internal/feedback"
	"github.com/kw12121212/writing-anything/internal/work"
	"github.com/kw12121212/writing-anything/internal/workspace"
)

// codeInvalidValue 表示参数取值不合法（如 --interval 不是正整数）。已发布的错误码不得改名或复用。
const codeInvalidValue = "input.invalid-value"

const feedbackViewHelp = "unit、id、kind、status、text、note、source（conversation 或 comment）与 anchor（state、quote、line、paragraph）"

var feedbackAddCommand = &command{
	name:    "feedback add",
	summary: "在单元（Unit）中记录一条作者反馈（Feedback）；给出 --quote 时记录指向原文的锚点（Anchor）",
	args:    []argSpec{{"unit", unitRefHelp, true}},
	flags: []flagSpec{
		dirFlag,
		{"text", flagString, "作者的意见"},
		{"kind", flagString, "反馈类型：" + strings.Join(feedback.Kinds, "、") + "；默认 suggestion"},
		{"quote", flagString, "被评论的原文片段，必须在当前正文中恰好出现一次；省略时反馈针对整个单元"},
		{"id", flagString, "反馈 id；省略时为 fb-<n>"},
	},
	outputKeys: []outputKey{{"feedback", "新反馈：" + feedbackViewHelp + "；失败时为 null"}},
	exitCodes:  standardExitCodes,
	run:        runFeedbackAdd,
}

var feedbackListCommand = &command{
	name:       "feedback list",
	summary:    "列出作者反馈（Feedback），并按当前正文重新定位锚点（Anchor）",
	args:       []argSpec{{"unit", "只列出该单元的反馈；省略时列出所有单元。" + unitRefHelp, false}},
	flags:      []flagSpec{dirFlag, {"status", flagString, "只列出该状态的反馈：" + strings.Join(feedback.Statuses, "、")}},
	outputKeys: []outputKey{{"feedback", "反馈数组，每项含 " + feedbackViewHelp}},
	exitCodes:  standardExitCodes,
	run:        runFeedbackList,
}

var feedbackResolveCommand = &command{
	name:    "feedback resolve",
	summary: "修改作者反馈（Feedback）的状态，并可记录处理说明",
	args:    []argSpec{{"unit", unitRefHelp, true}, {"id", "反馈 id", true}},
	flags: []flagSpec{
		dirFlag,
		{"status", flagString, "新状态：" + strings.Join(feedback.Statuses, "、") + "；默认 resolved"},
		{"note", flagString, "处理说明，如关联的修订"},
	},
	outputKeys: []outputKey{{"feedback", "修改后的反馈：" + feedbackViewHelp + "；失败时为 null"}},
	exitCodes:  standardExitCodes,
	run:        runFeedbackResolve,
}

var feedbackWatchCommand = &command{
	name:    "feedback watch",
	summary: "持续检查工作区，每发现一条新增的作者反馈（Feedback）就输出一行；加 --json 时每行是一个输出信封（Output Envelope）",
	flags: []flagSpec{
		dirFlag,
		{"interval", flagString, "检查间隔（毫秒），正整数，默认 1000"},
		{"max", flagString, "输出指定条数的反馈后退出；省略时一直运行"},
		{"all", flagBool, "先输出启动时已有的未处理（open 或 accepted）反馈"},
	},
	outputKeys: []outputKey{{"feedback", "每行一条反馈：" + feedbackViewHelp + "；报告扫描问题的行为 null"}},
	exitCodes:  standardExitCodes,
	run:        runFeedbackWatch,
}

type anchorView struct {
	State     string  `json:"state"`
	Quote     *string `json:"quote"`
	Line      *int    `json:"line"`
	Paragraph *int    `json:"paragraph"`
}

type feedbackView struct {
	Unit   string     `json:"unit"`
	ID     string     `json:"id"`
	Kind   string     `json:"kind"`
	Status string     `json:"status"`
	Text   string     `json:"text"`
	Note   *string    `json:"note"`
	Source string     `json:"source"`
	Anchor anchorView `json:"anchor"`
}

func feedbackOf(it feedback.Item) feedbackView {
	v := feedbackView{Unit: it.Unit, ID: it.ID, Kind: it.Kind, Status: it.Status, Text: it.Text, Note: it.Note,
		Source: it.SourceOf(), Anchor: anchorView{State: it.State}}
	if it.Anchor != nil {
		v.Anchor.Quote = &it.Anchor.Quote
	}
	if it.State == feedback.StateLocated {
		line, para := it.Line, it.Paragraph
		v.Anchor.Line, v.Anchor.Paragraph = &line, &para
	}
	return v
}

// feedbackText 以一行人读文字描述反馈。
func feedbackText(v feedbackView) string {
	where := "整个单元"
	switch v.Anchor.State {
	case feedback.StateLocated:
		where = fmt.Sprintf("第 %d 段（第 %d 行）「%s」", *v.Anchor.Paragraph, *v.Anchor.Line, *v.Anchor.Quote)
	case feedback.StateOrphaned:
		where = fmt.Sprintf("失去锚点（原文「%s」已不在正文中）", *v.Anchor.Quote)
	}
	return fmt.Sprintf("%s %s（%s，%s）%s：%s", v.Unit, v.ID, v.Kind, v.Status, where, v.Text)
}

type feedbackResult struct {
	Result
	Feedback *feedbackView `json:"feedback"`
}

type feedbackListResult struct {
	Result
	Feedback []feedbackView `json:"feedback"`
}

func runFeedbackAdd(inv *invocation) int {
	dir := projectDir(inv)
	res := &feedbackResult{}
	problems := workspace.Require(dir)
	edit := false
	if problems == nil {
		var w *work.Work
		var u *work.Unit
		if w, u, problems = work.Resolve(dir, inv.args[0]); u != nil {
			var it *feedback.Item
			var changed []string
			if it, changed, problems = feedback.Add(dir, w, u, inv.values["kind"], inv.values["text"], inv.values["quote"], inv.values["id"]); it != nil {
				v := feedbackOf(*it)
				res.Feedback, res.ChangedPaths = &v, changed
			}
			edit = true
		}
	}
	res.Diagnostics = diagnostics(problems, dir)
	if edit {
		// 由本次参数引起的问题指向用法；已有 feedback.yaml 中的问题（字段以 feedback 开头）保留默认的复查命令。
		for i, d := range res.Diagnostics {
			if slices.Contains([]string{feedback.CodeQuoteNotFound, feedback.CodeAmbiguousQuote, draft.CodeNotFound, feedback.CodeAnchoredValue}, d.Code) ||
				slices.Contains([]string{"id", "kind", "text"}, d.Field) {
				res.Diagnostics[i].Next = []string{"wa-cli help feedback add --json"}
			}
		}
	}
	return finish(inv, res, func(w io.Writer) error {
		if res.Feedback == nil {
			return nil
		}
		_, err := fmt.Fprintf(w, "已记录作者反馈（Feedback）：%s\n", feedbackText(*res.Feedback))
		return err
	})
}

// invalidValue 生成参数取值不合法的问题。
func invalidValue(flag, value, want string) workspace.Problem {
	return workspace.Problem{Code: codeInvalidValue, Severity: workspace.SeverityError,
		Message: fmt.Sprintf("参数 --%s 的取值 %q 不合法：%s", flag, value, want)}
}

func runFeedbackList(inv *invocation) int {
	dir := projectDir(inv)
	res := &feedbackListResult{Feedback: []feedbackView{}}
	problems := workspace.Require(dir)
	status, filter := inv.values["status"]
	if problems == nil && filter && !slices.Contains(feedback.Statuses, status) {
		problems = []workspace.Problem{invalidValue("status", status, "应为 "+strings.Join(feedback.Statuses, "、")+" 之一")}
	}
	if problems == nil {
		var items []feedback.Item
		if len(inv.args) == 1 {
			var w *work.Work
			var u *work.Unit
			if w, u, problems = work.Resolve(dir, inv.args[0]); u != nil {
				items, problems = feedback.List(dir, w, u)
			}
		} else {
			works, _, _ := work.LoadAll(dir)
			items, problems = feedback.ListAll(dir, works)
		}
		for _, it := range items {
			if !filter || it.Status == status {
				res.Feedback = append(res.Feedback, feedbackOf(it))
			}
		}
	}
	res.Diagnostics = diagnostics(problems, dir)
	helpNext(res.Diagnostics, "feedback list", []string{codeInvalidValue})
	return finish(inv, res, func(w io.Writer) error {
		var b strings.Builder
		if len(res.Feedback) == 0 && !hasError(res.Diagnostics) {
			b.WriteString("没有作者反馈（Feedback）\n")
		}
		for _, v := range res.Feedback {
			b.WriteString(feedbackText(v) + "\n")
		}
		_, err := io.WriteString(w, b.String())
		return err
	})
}

func runFeedbackResolve(inv *invocation) int {
	dir := projectDir(inv)
	res := &feedbackResult{}
	problems := workspace.Require(dir)
	if problems == nil {
		var w *work.Work
		var u *work.Unit
		if w, u, problems = work.Resolve(dir, inv.args[0]); u != nil {
			note, setNote := inv.values["note"]
			var it *feedback.Item
			var changed []string
			if it, changed, problems = feedback.Resolve(dir, w, u, inv.args[1], inv.values["status"], note, setNote); it != nil {
				v := feedbackOf(*it)
				res.Feedback = &v
			}
			res.ChangedPaths = changed
		}
	}
	res.Diagnostics = diagnostics(problems, dir)
	for i, d := range res.Diagnostics {
		switch {
		case d.Code == feedback.CodeNotFound:
			res.Diagnostics[i].Next = []string{shellCommand("wa-cli", "feedback", "list", inv.args[0], "--dir", dir, "--json")}
		case d.Code == feedback.CodeAnchoredValue || d.Field == "status":
			res.Diagnostics[i].Next = []string{"wa-cli help feedback resolve --json"}
		}
	}
	return finish(inv, res, func(w io.Writer) error {
		if res.Feedback == nil {
			return nil
		}
		_, err := fmt.Fprintf(w, "作者反馈（Feedback）已更新：%s\n", feedbackText(*res.Feedback))
		return err
	})
}

// maxPositive 是正整数参数的上限：--interval 最长一天（毫秒），也避免换算为时长时溢出。
const maxPositive = 86400000

// positive 解析不超过 maxPositive 的正整数参数 name；未给出时返回 def。
func positive(inv *invocation, name string, def int) (int, *workspace.Problem) {
	v, ok := inv.values[name]
	if !ok {
		return def, nil
	}
	n, err := strconv.Atoi(v)
	if err != nil || n <= 0 || n > maxPositive {
		p := invalidValue(name, v, fmt.Sprintf("应为不超过 %d 的正整数", maxPositive))
		return 0, &p
	}
	return n, nil
}

func runFeedbackWatch(inv *invocation) int {
	dir := projectDir(inv)
	problems := workspace.Require(dir)
	interval, p := positive(inv, "interval", 1000)
	if p != nil && problems == nil {
		problems = []workspace.Problem{*p}
	}
	limit, p := positive(inv, "max", 0)
	if p != nil && problems == nil {
		problems = []workspace.Problem{*p}
	}
	if problems != nil {
		res := &feedbackResult{}
		res.Diagnostics = diagnostics(problems, dir)
		helpNext(res.Diagnostics, "feedback watch", []string{codeInvalidValue})
		return finish(inv, res, func(io.Writer) error { return nil })
	}
	emitted := 0
	emit := func(res *feedbackResult) error {
		if inv.json() {
			return writeJSON(inv.stdout, res)
		}
		if res.Feedback != nil {
			_, err := fmt.Fprintf(inv.stdout, "新的作者反馈（Feedback）：%s\n", feedbackText(*res.Feedback))
			return err
		}
		return writeDiagnostics(inv.stderr, res.Diagnostics)
	}
	seen, reported := map[string]bool{}, map[string]bool{}
	// scan 扫描工作区：输出尚未报告过的问题，对每条未见过的反馈调用 fresh；返回是否已达到 --max。
	scan := func(fresh func(feedback.Item) bool) (bool, error) {
		works, _, wps := work.LoadAll(dir)
		items, fps := feedback.ListAll(dir, works)
		for _, p := range slices.Concat(wps, fps) {
			key := p.Code + "\x00" + p.Path + "\x00" + p.Field
			if reported[key] {
				continue
			}
			reported[key] = true
			if err := emit(&feedbackResult{Result: Result{Diagnostics: diagnostics([]workspace.Problem{p}, dir)}}); err != nil {
				return false, err
			}
		}
		for _, it := range items {
			key := it.Unit + "\x00" + it.ID
			if seen[key] {
				continue
			}
			seen[key] = true
			if !fresh(it) {
				continue
			}
			v := feedbackOf(it)
			if err := emit(&feedbackResult{Feedback: &v}); err != nil {
				return false, err
			}
			if emitted++; limit > 0 && emitted >= limit {
				return true, nil
			}
		}
		return false, nil
	}
	all := inv.flag("all")
	done, err := scan(func(it feedback.Item) bool { return all && feedback.Pending(it.Status) })
	for err == nil && !done {
		time.Sleep(time.Duration(interval) * time.Millisecond)
		done, err = scan(func(feedback.Item) bool { return true })
	}
	if err != nil {
		return writeFailed(inv.stderr, err)
	}
	return exitOK
}

// pendingFeedback 返回单元 u 未处理完（open 或 accepted）的反馈；数组永不为 nil。feedback.yaml 的问题由
// validate 报告，这里不重复。
func pendingFeedback(dir string, w *work.Work, u *work.Unit) []feedbackView {
	out := []feedbackView{}
	items, _ := feedback.List(dir, w, u)
	for _, it := range items {
		if feedback.Pending(it.Status) {
			out = append(out, feedbackOf(it))
		}
	}
	return out
}

// feedbackNext 返回反馈产物中已有问题的下一步命令；不相关时返回 nil。由参数引起的问题由各命令改写为查看用法。
func feedbackNext(p workspace.Problem, dir string) []string {
	switch {
	case p.Code == catalog.CodeDuplicateID && strings.HasSuffix(p.Path, "/"+feedback.FileName):
		return []string{shellCommand("wa-cli", "validate", p.Path, "--dir", dir, "--json")}
	case p.Code == feedback.CodeTooManyComments:
		return []string{shellCommand("wa-cli", "feedback", "list", work.RefOf(strings.TrimSuffix(p.Path, "/draft.md")), "--dir", dir, "--json")}
	}
	return nil
}
