package cli

import (
	"fmt"
	"io"
	"slices"
	"strings"

	"github.com/kw12121212/writing-anything/internal/brief"
	"github.com/kw12121212/writing-anything/internal/catalog"
	"github.com/kw12121212/writing-anything/internal/draft"
	"github.com/kw12121212/writing-anything/internal/plan"
	"github.com/kw12121212/writing-anything/internal/work"
	"github.com/kw12121212/writing-anything/internal/workspace"
)

const planHelp = "method（id 与 source）、path（没有 plan.yaml 时为 null）与 steps（每个步骤含 id、title、product、" +
	"status（pending、drafted 或 confirmed）、text、sources、links、confirmedByAuthor、confirmation、channel（确认渠道（Confirmation Channel），未确认为 null）与 confirmedAt）"

var planShowCommand = &command{
	name:    "plan show",
	summary: "只读地查看单元的规划（Plan）：规划类写作法的每个步骤、各步的状态与当前待作者确认的步骤",
	args:    []argSpec{{"unit", unitRefHelp, true}},
	flags:   []flagSpec{dirFlag},
	outputKeys: []outputKey{
		{"plan", "规划：" + planHelp + "；单元选用的写作法不是规划类时为 null"},
		{"currentStep", "第一个未经作者确认的步骤 id；全部确认或没有规划类写作法时为 null"},
	},
	exitCodes: standardExitCodes,
	run:       runPlanShow,
}

var planStepCommand = &command{
	name:    "plan step",
	summary: "写入规划（Plan）当前步骤的草稿：规划文本、关联素材与产物链接（人物卡经 canon add、场景经 unit add、场景契约经 scene contract 落地后用 --link 关联）；上一步未经作者确认时拒绝，修订已确认步骤须用 --redo",
	args: []argSpec{
		{"unit", unitRefHelp + "；选用的写作法须为规划类", true},
		{"step", "规划步骤（Planning Step）id，见 wa-cli method show <写作法 id>", true},
	},
	flags: []flagSpec{
		dirFlag,
		{"text", flagString, "该步骤的规划文本"},
		{"source", flagString, "关联的素材（Source）id，用逗号分隔；整体替换，空白取值被忽略"},
		{"link", flagString, "产物链接（Plan Link），用逗号分隔；整体替换：characters 为设定集条目 id，scenes 为场景单元引用，scene-contracts 形如 <场景单元引用>:<大纲节 id>"},
		{"redo", flagBool, "修订已确认的步骤：重置该步为新的未确认草稿并删除其后全部步骤的记录（产物不动）；须与 --confirmed-by-author、--confirmation 同给"},
		{"confirmed-by-author", flagBool, "作者确认修订（只用于 --redo）"},
		{"confirmation", flagString, "作者原话摘要（只用于 --redo）"},
	},
	outputKeys: []outputKey{
		{"plan", "写入后的规划：" + planHelp},
		{"currentStep", "写入后的当前步骤 id（--redo 后为被重置的步骤）；全部确认时为 null"},
	},
	exitCodes: standardExitCodes,
	run:       runPlanStep,
}

var planConfirmCommand = &command{
	name:    "plan confirm",
	summary: "记录当前规划步骤的作者确认（确认记录）：--confirmed-by-author 与 --confirmation 必须同给；声明规划产物的步骤须已有可解析的 --link 产物",
	args: []argSpec{
		{"unit", unitRefHelp + "；选用的写作法须为规划类", true},
		{"step", "要确认的规划步骤（Planning Step）id，必须是当前第一个未确认的步骤", true},
	},
	flags: []flagSpec{
		dirFlag,
		{"confirmed-by-author", flagBool, "作者确认本步骤"},
		{"confirmation", flagString, "作者原话摘要"},
	},
	outputKeys: []outputKey{
		{"plan", "确认后的规划：" + planHelp},
		{"currentStep", "确认后的当前步骤 id（下一步）；全部确认时为 null"},
	},
	exitCodes: standardExitCodes,
	run:       runPlanConfirm,
}

// planStepView 是 plan show / plan step / plan confirm 输出的一个步骤。
type planStepView struct {
	ID                string   `json:"id"`
	Title             string   `json:"title"`
	Product           *string  `json:"product"`
	Status            string   `json:"status"` // pending、drafted 或 confirmed
	Text              string   `json:"text"`
	Sources           []string `json:"sources"`
	Links             []string `json:"links"`
	ConfirmedByAuthor bool     `json:"confirmedByAuthor"`
	Confirmation      *string  `json:"confirmation"`
	Channel           *string  `json:"channel"` // 确认渠道（Confirmation Channel）；未确认为 null
	ConfirmedAt       *string  `json:"confirmedAt"`
}

// planningView 是 plan show 等命令输出的规划。
type planningView struct {
	Method string         `json:"method"`
	Source string         `json:"source"`
	Path   *string        `json:"path"`
	Steps  []planStepView `json:"steps"`
}

type planResult struct {
	Result
	Plan        *planningView `json:"plan"`
	CurrentStep *string       `json:"currentStep"`
}

// planStatusText 是步骤状态的人读名称。
func planStatusText(status string) string {
	switch status {
	case "confirmed":
		return "已确认"
	case "drafted":
		return "待作者确认"
	}
	return "未起草"
}

// buildPlanView 把写作法步骤与规划记录合并为输出视图；f 为 nil 时全部步骤未起草。
func buildPlanView(m *catalog.Method, source, rel string, f *plan.File) *planningView {
	v := &planningView{Method: m.ID, Source: source, Steps: []planStepView{}}
	if rel != "" {
		v.Path = &rel
	}
	for i, s := range m.Steps {
		sv := planStepView{ID: s.ID, Title: s.Title, Status: "pending", Sources: []string{}, Links: []string{}}
		if s.Product != "" {
			p := s.Product
			sv.Product = &p
		}
		if f != nil && i < len(f.Steps) && f.Steps[i].ID == s.ID {
			r := f.Steps[i]
			sv.Status, sv.Text, sv.ConfirmedByAuthor = "drafted", r.Text, r.ConfirmedByAuthor
			sv.Sources, sv.Links, sv.Confirmation, sv.ConfirmedAt = r.Sources, r.Links, r.Confirmation, r.ConfirmedAt
			sv.Channel = channelView(r.ConfirmedByAuthor, r.Channel)
			if r.ConfirmedByAuthor {
				sv.Status = "confirmed"
			}
		}
		v.Steps = append(v.Steps, sv)
	}
	return v
}

// currentStepOf 返回视图的当前步骤 id；全部确认时为 nil。
func currentStepOf(v *planningView) *string {
	for i := range v.Steps {
		if v.Steps[i].Status != "confirmed" {
			id := v.Steps[i].ID
			return &id
		}
	}
	return nil
}

func planText(res *planResult, ref string) string {
	if res.Plan == nil {
		return ""
	}
	var b strings.Builder
	fmt.Fprintf(&b, "规划（Plan）：%s（写作法 %s，%s）\n", ref, res.Plan.Method, res.Plan.Source)
	for i, s := range res.Plan.Steps {
		mark := ""
		if res.CurrentStep != nil && s.ID == *res.CurrentStep {
			mark = "，当前步骤"
		}
		fmt.Fprintf(&b, "  %d. %s %s：%s%s\n", i+1, s.ID, s.Title, planStatusText(s.Status), mark)
		if s.Text != "" {
			fmt.Fprintf(&b, "    %s\n", s.Text)
		}
		if len(s.Sources) > 0 {
			fmt.Fprintf(&b, "    素材（Source）：%s\n", strings.Join(s.Sources, "、"))
		}
		if len(s.Links) > 0 {
			fmt.Fprintf(&b, "    产物链接（Plan Link）：%s\n", strings.Join(s.Links, "、"))
		}
		if s.Confirmation != nil && *s.Confirmation != "" {
			fmt.Fprintf(&b, "    作者确认：%s\n", *s.Confirmation)
		}
	}
	return b.String()
}

// planMethod 选出单元的规划类写作法；不是规划类时返回 plan.not-planning 的问题，选用的写作法不存在时另附
// brief.unknown-method 的 warning（method-selection 的悬空引用口径）。
func planMethod(dir string, w *work.Work, u *work.Unit) (m *catalog.Method, source string, problems []workspace.Problem) {
	set := catalog.LoadMethods(dir)
	ref, missing := set.Pick(brief.MethodOf(dir, w.UnitDir(u)), unitDefaultMethod(w, u))
	if missing != "" {
		p := brief.UnknownMethod(brief.Path(w.UnitDir(u)), brief.FieldMethod, missing, set)
		p.Severity = workspace.SeverityWarning
		problems = append(problems, p)
	}
	if ref == nil || ref.Kind != catalog.MethodPlanning {
		problems = append(problems, workspace.Problem{Code: plan.CodeNotPlanning, Severity: workspace.SeverityError, Field: "unit",
			Message: "单元 " + u.Ref + " 选用的写作法不是规划类（Planning），没有分步的规划（Plan）；可用的写作法用 wa-cli method list --dir " + dir + " --json 查看"})
		return nil, "", problems
	}
	source = "builtin"
	if r, ok := set.Find(ref.ID); ok {
		source = r.Source
	}
	return ref, source, problems
}

// planShow 读出并检查规划后构造输出：自身问题（解析、Schema、顺序）不输出规划；悬空引用照常输出并报告。
func planShow(dir string, w *work.Work, u *work.Unit, m *catalog.Method, source string) (*planResult, []workspace.Problem) {
	res := &planResult{}
	unitDir := w.UnitDir(u)
	f, ps := plan.Load(dir, unitDir)
	if len(ps) > 0 {
		return res, ps
	}
	own, refs := plan.Check(dir, unitDir, w, m)
	if len(own) > 0 {
		return res, own
	}
	rel := ""
	if f != nil {
		rel = plan.Path(unitDir)
	}
	res.Plan = buildPlanView(m, source, rel, f)
	res.CurrentStep = currentStepOf(res.Plan)
	return res, refs
}

func runPlanShow(inv *invocation) int {
	dir := projectDir(inv)
	res := &planResult{}
	var problems []workspace.Problem
	if problems = workspace.Require(dir); problems == nil {
		if w, u, ps := work.Resolve(dir, inv.args[0]); u != nil {
			problems = ps
			if m, source, ps := planMethod(dir, w, u); m != nil {
				res, problems = planShow(dir, w, u, m, source)
			} else {
				// 只读的查看不因未选用规划写作法而失败（plan-show）：以 warning 报告。
				for i := range ps {
					if ps[i].Code == plan.CodeNotPlanning {
						ps[i].Severity = workspace.SeverityWarning
					}
				}
				problems = ps
			}
		}
	}
	res.Diagnostics = diagnostics(problems, dir)
	return finish(inv, res, func(out io.Writer) error {
		_, err := io.WriteString(out, planText(res, inv.args[0]))
		return err
	})
}

// splitList 拆开 --source / --link 的逗号分隔取值；空白取值被忽略。重复取值时返回 input.invalid-value。
func splitList(name, v string) ([]string, []workspace.Problem, bool) {
	if v == "" {
		return []string{}, nil, true
	}
	list := splitIDs(v)
	for i, x := range list {
		if slices.Contains(list[:i], x) {
			p := invalidValue(name, v, "取值 "+x+" 重复")
			p.Field = name
			return nil, []workspace.Problem{p}, false
		}
	}
	return list, nil, true
}

// planParamNext 为参数引起的问题（不填 path）给出用法与相关的查看命令；已有产物中的问题保留默认的复查命令
// （next-rechecks-problem）。
func planParamNext(res *planResult, dir, ref, methodID, sub string) {
	for i, d := range res.Diagnostics {
		if d.Path != "" {
			continue
		}
		next := []string{"wa-cli help plan " + sub + " --json"}
		switch d.Code {
		case plan.CodeNotPlanning:
			next = append(next, shellCommand("wa-cli", "method", "list", "--dir", dir, "--json"))
		case plan.CodeUnknownStep:
			next = append(next, shellCommand("wa-cli", "method", "show", methodID, "--dir", dir, "--json"))
		case plan.CodeStepOrder, plan.CodeNotDrafted, plan.CodeNotConfirmed, plan.CodeMissingLinks, plan.CodeMethodChanged:
			next = append(next, shellCommand("wa-cli", "plan", "show", ref, "--dir", dir, "--json"))
		case plan.CodeUnknownSource:
			next = append(next, shellCommand("wa-cli", "source", "list", "--dir", dir, "--json"))
		case plan.CodeUnknownLink:
			next = append(next, shellCommand("wa-cli", "plan", "show", ref, "--dir", dir, "--json"))
		}
		res.Diagnostics[i].Next = next
	}
}

func runPlanStep(inv *invocation) int {
	dir := projectDir(inv)
	res := &planResult{}
	ref, stepID := inv.args[0], inv.args[1]
	var problems []workspace.Problem
	methodID := ""
	if problems = workspace.Require(dir); problems == nil {
		if w, u, ps := work.Resolve(dir, ref); u != nil {
			problems = ps
			if m, source, ps := planMethod(dir, w, u); m != nil {
				methodID = m.ID
				unitDir := w.UnitDir(u)
				ensure := func() ([]string, []workspace.Problem) { return draft.EnsureUnitDir(dir, w, u) }
				in := plan.StepInput{
					Step:            stepID,
					Text:            inv.values["text"],
					Redo:            inv.flag("redo"),
					AuthorConfirmed: inv.flag("confirmed-by-author"),
					Confirmation:    inv.values["confirmation"],
				}
				if _, ok := inv.values["source"]; ok && len(problems) == 0 {
					if in.Sources, problems, ok = splitList("source", inv.values["source"]); !ok {
						in.Sources = nil
					}
				}
				if _, ok := inv.values["link"]; ok && len(problems) == 0 {
					if in.Links, problems, ok = splitList("link", inv.values["link"]); !ok {
						in.Links = nil
					}
				}
				if len(problems) == 0 {
					var f *plan.File
					var changed []string
					if f, changed, problems = plan.WriteStep(dir, w, unitDir, m, in, ensure); f != nil {
						res.Plan = buildPlanView(m, source, plan.Path(unitDir), f)
						res.CurrentStep = currentStepOf(res.Plan)
						res.ChangedPaths = changed
					}
				}
			} else {
				problems = ps
			}
		}
	}
	res.Diagnostics = diagnostics(problems, dir)
	planParamNext(res, dir, ref, methodID, "step")
	return finish(inv, res, func(out io.Writer) error {
		_, err := io.WriteString(out, planText(res, ref))
		return err
	})
}

func runPlanConfirm(inv *invocation) int {
	dir := projectDir(inv)
	res := &planResult{}
	ref, stepID := inv.args[0], inv.args[1]
	var problems []workspace.Problem
	methodID := ""
	if problems = workspace.Require(dir); problems == nil {
		if w, u, ps := work.Resolve(dir, ref); u != nil {
			problems = ps
			if m, source, ps := planMethod(dir, w, u); m != nil {
				methodID = m.ID
				unitDir := w.UnitDir(u)
				ensure := func() ([]string, []workspace.Problem) { return draft.EnsureUnitDir(dir, w, u) }
				if !inv.flag("confirmed-by-author") || strings.TrimSpace(inv.values["confirmation"]) == "" {
					problems = []workspace.Problem{{Code: plan.CodeIncompleteConfirmation, Severity: workspace.SeverityError, Field: "confirmation",
						Message: "作者确认必须给出 --confirmed-by-author 与非空白的 --confirmation（作者原话摘要）"}}
				} else {
					var f *plan.File
					var changed []string
					if f, changed, problems = plan.Confirm(dir, w, unitDir, m, stepID, inv.values["confirmation"], ensure, now()); f != nil {
						res.Plan = buildPlanView(m, source, plan.Path(unitDir), f)
						res.CurrentStep = currentStepOf(res.Plan)
						res.ChangedPaths = changed
					}
				}
			} else {
				problems = ps
			}
		}
	}
	res.Diagnostics = diagnostics(problems, dir)
	planParamNext(res, dir, ref, methodID, "confirm")
	return finish(inv, res, func(out io.Writer) error {
		_, err := io.WriteString(out, planText(res, ref))
		return err
	})
}
