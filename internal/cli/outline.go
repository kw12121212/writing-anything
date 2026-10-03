package cli

import (
	"fmt"
	"io"
	"maps"
	"slices"
	"strconv"
	"strings"

	"github.com/kw12121212/writing-anything/internal/brief"
	"github.com/kw12121212/writing-anything/internal/catalog"
	"github.com/kw12121212/writing-anything/internal/draft"
	"github.com/kw12121212/writing-anything/internal/outline"
	"github.com/kw12121212/writing-anything/internal/schema"
	"github.com/kw12121212/writing-anything/internal/series"
	"github.com/kw12121212/writing-anything/internal/work"
	"github.com/kw12121212/writing-anything/internal/workspace"
)

var outlineKeys = []outputKey{
	{"outline", "大纲（Outline）：unit、path 与 sections（id、title、role、covers、uses、reason、characters、beats、reveal、targets，以及场景契约扩展字段 pov、location、state_changes、plants、recovers、causes、events）；没有 outline.yaml 或失败时为 null"},
	{"coverage", "写作简报中每个核心观点（Key Message）的覆盖情况：keyMessage、text 与 sections（覆盖它的节）"},
	{"gaps", "覆盖缺口（Coverage Gap）数组，每项含 code、keyMessage、section 与 message"},
}

var outlineSetCommand = &command{
	name:    "outline set",
	summary: "按 id 新增或修改大纲（Outline）中的一节；覆盖缺口（Coverage Gap）不阻止写入，在 gaps 中列出",
	args: []argSpec{
		{"unit", unitRefHelp, true},
		{"section", "大纲节（Outline Section）id；不存在时新增，此时必须给出 --title", true},
	},
	flags: []flagSpec{
		dirFlag,
		{"title", flagString, "节的标题"},
		{"role", flagString, "该节承担的写作法模板（Method Template）章节或节拍 id（见 wa-cli method show）；空字符串时删除"},
		{"covers", flagString, "该节覆盖的核心观点（Key Message）id，用逗号分隔；整体替换，空字符串时删除"},
		{"uses", flagString, "该节使用的素材（Source）id，用逗号分隔；整体替换，空字符串时删除"},
		{"reason", flagString, "该节不覆盖任何核心观点的理由；空字符串时删除"},
		{"characters", flagString, "场景契约（Scene Contract）：本场景出场的人物，用逗号分隔；整体替换，空字符串时删除"},
		{"beats", flagString, "场景契约：本场景承担的情节节拍（Story Beat）id，用逗号分隔，指向故事根单元大纲的节；整体替换，空字符串时删除"},
		{"reveal", flagString, "场景契约：揭示边界（Reveal Boundary），本场景不能提前透露的信息；空字符串时删除"},
		{"target", flagString, "场景契约：目标锚定分值，形如 <指标 id>=<分值>，用逗号分隔（如 tension=4）；整体替换，空字符串时删除"},
		{"after", flagString, "把该节放到这一节之后"},
		{"before", flagString, "把该节放到这一节之前"},
	},
	outputKeys: outlineKeys,
	exitCodes:  standardExitCodes,
	run:        runOutlineSet,
}

var outlineRemoveCommand = &command{
	name:    "outline remove",
	summary: "删除大纲（Outline）中的一节",
	args: []argSpec{
		{"unit", unitRefHelp, true},
		{"section", "要删除的大纲节（Outline Section）id", true},
	},
	flags:      []flagSpec{dirFlag},
	outputKeys: outlineKeys,
	exitCodes:  standardExitCodes,
	run:        runOutlineRemove,
}

var outlineShowCommand = &command{
	name:       "outline show",
	summary:    "列出单元（Unit）的大纲（Outline）、每个核心观点的覆盖情况与覆盖缺口（Coverage Gap）",
	args:       []argSpec{{"unit", unitRefHelp, true}},
	flags:      []flagSpec{dirFlag},
	outputKeys: outlineKeys,
	exitCodes:  standardExitCodes,
	run:        runOutlineShow,
}

type sectionView struct {
	ID           string              `json:"id"`
	Title        string              `json:"title"`
	Role         *string             `json:"role"`
	Covers       []string            `json:"covers"`
	Uses         []string            `json:"uses"`
	Reason       *string             `json:"reason"`
	Characters   []string            `json:"characters"`
	Beats        []string            `json:"beats"`
	Reveal       *string             `json:"reveal"`
	Targets      map[string]int      `json:"targets"`
	Pov          *string             `json:"pov"`
	Location     *string             `json:"location"`
	StateChanges []stateChangeView   `json:"state_changes"`
	Plants       []string            `json:"plants"`
	Recovers     []string            `json:"recovers"`
	Causes       []string            `json:"causes"`
	Events       []string            `json:"events"`
}

// stateChangeView 是场景契约中一条必须发生的状态变化：id 供写后收口对照，text 为描述。
type stateChangeView struct {
	ID   string `json:"id"`
	Text string `json:"text"`
}

type outlineView struct {
	Unit     string        `json:"unit"`
	Path     string        `json:"path"`
	Sections []sectionView `json:"sections"`
}

type coverView struct {
	KeyMessage string   `json:"keyMessage"`
	Text       string   `json:"text"`
	Sections   []string `json:"sections"`
}

type gapView struct {
	Code       string  `json:"code"`
	KeyMessage *string `json:"keyMessage"`
	Section    *string `json:"section"`
	Message    string  `json:"message"`
}

type outlineResult struct {
	Result
	Outline  *outlineView `json:"outline"`
	Coverage []coverView  `json:"coverage"`
	Gaps     []gapView    `json:"gaps"`
}

// fill 以大纲 o（可为 nil）与核心观点 ms 填写输出中的 outline、coverage 与 gaps。
func (res *outlineResult) fill(ref string, o *outline.Outline, ms outline.Messages) {
	if o != nil {
		v := &outlineView{Unit: ref, Path: o.Path, Sections: []sectionView{}}
		for _, s := range o.Sections {
			sv := sectionView{ID: s.ID, Title: s.Title, Role: optional(s.Role), Covers: nonNilStrings(s.Covers), Uses: nonNilStrings(s.Uses), Reason: optional(s.Reason),
				Characters: nonNilStrings(s.Characters), Beats: nonNilStrings(s.Beats), Reveal: optional(s.Reveal), Targets: nonNilInts(s.Targets),
				Pov: optional(s.Pov), Location: optional(s.Location), Plants: nonNilStrings(s.Plants), Recovers: nonNilStrings(s.Recovers),
				Causes: nonNilStrings(s.Causes), Events: nonNilStrings(s.Events), StateChanges: []stateChangeView{}}
			for _, sc := range s.StateChanges {
				sv.StateChanges = append(sv.StateChanges, stateChangeView{ID: sc.ID, Text: sc.Text})
			}
			v.Sections = append(v.Sections, sv)
		}
		res.Outline = v
	}
	for _, c := range outline.Coverage(o, ms) {
		res.Coverage = append(res.Coverage, coverView{KeyMessage: c.KeyMessage, Text: c.Text, Sections: nonNilStrings(c.Sections)})
	}
	for _, g := range outline.Gaps(o, ms) {
		res.Gaps = append(res.Gaps, gapView{Code: g.Code, KeyMessage: optional(g.KeyMessage), Section: optional(g.Section), Message: g.Message})
	}
}

func newOutlineResult() *outlineResult {
	return &outlineResult{Coverage: []coverView{}, Gaps: []gapView{}}
}

// outlineEdit 把 outline set 的参数转换为修改；参数取值不合法时返回 input.invalid-value 或 artifact.schema-violation。
// existing 为已有的大纲（可为 nil），用于判断是否新增。
func outlineEdit(inv *invocation, existing *outline.Outline) (outline.Edit, []workspace.Problem) {
	id := inv.args[1]
	e := outline.Edit{ID: id, After: inv.values["after"], Before: inv.values["before"]}
	if !brief.ValidID(id) {
		return e, []workspace.Problem{{Code: schema.CodeSchemaViolation, Severity: workspace.SeverityError, Field: "section",
			Message: fmt.Sprintf("大纲节 id %q 不合法：应以小写字母开头，由小写字母、数字与连字符组成", id)}}
	}
	str := func(name string) *string {
		if v, ok := inv.values[name]; ok {
			return &v
		}
		return nil
	}
	e.Title, e.Reason, e.Role, e.Reveal = str("title"), str("reason"), str("role"), str("reveal")
	if e.Role != nil && *e.Role != "" && !brief.ValidID(*e.Role) {
		return e, []workspace.Problem{{Code: schema.CodeSchemaViolation, Severity: workspace.SeverityError, Field: "role",
			Message: fmt.Sprintf("role %q 不合法：应为写作法的章节或节拍 id，以小写字母开头，由小写字母、数字与连字符组成", *e.Role)}}
	}
	if e.Title != nil && *e.Title == "" {
		return e, []workspace.Problem{invalidValue("title", "", "大纲节必须有标题，--title 不能为空")}
	}
	for _, name := range []string{"after", "before"} {
		if v, ok := inv.values[name]; ok && v == "" {
			return e, []workspace.Problem{invalidValue(name, v, "应为大纲中已有节的 id")}
		}
	}
	for _, name := range []string{"title", "reason", "reveal"} {
		if v, ok := inv.values[name]; ok && v != "" && strings.TrimSpace(v) == "" {
			return e, []workspace.Problem{invalidValue(name, v, "不能只含空白")}
		}
	}
	// 场景契约（Scene Contract）：出场人物与承担的节拍；重复或空白取值在修改前报告，空字符串删除字段。
	if v, ok := inv.values["characters"]; ok {
		var list []string
		if v != "" {
			for _, c := range strings.Split(v, ",") {
				c = strings.TrimSpace(c)
				if c == "" {
					return e, []workspace.Problem{invalidValue("characters", v, "出场人物不能为空；只给出一个空值表示删除全部出场人物")}
				}
				if slices.Contains(list, c) {
					return e, []workspace.Problem{invalidValue("characters", v, "出场人物 "+c+" 重复")}
				}
				list = append(list, c)
			}
		}
		e.Characters = &list
	}
	for _, name := range []string{"covers", "uses", "beats"} {
		v, ok := inv.values[name]
		if !ok {
			continue
		}
		list := splitIDs(v)
		for i, x := range list {
			if slices.Contains(list[:i], x) {
				return e, []workspace.Problem{invalidValue(name, v, "id "+x+" 重复")}
			}
		}
		switch name {
		case "covers":
			e.Covers = &list
		case "uses":
			e.Uses = &list
		default:
			e.Beats = &list
		}
	}
	// 场景契约：按 AI 评分指标（Metric）id 声明的目标锚定分值（如 tension=4）；格式不合法在修改前报告，空字符串删除字段。
	if v, ok := inv.values["target"]; ok {
		targets := map[string]int{}
		if v != "" {
			for _, item := range strings.Split(v, ",") {
				id, score, found := strings.Cut(strings.TrimSpace(item), "=")
				if !found {
					return e, []workspace.Problem{invalidValue("target", v, "每一项应为 <指标 id>=<分值>，如 tension=4")}
				}
				id = strings.TrimSpace(id)
				if !brief.ValidID(id) {
					return e, []workspace.Problem{invalidValue("target", v, "指标 id "+id+" 不合法：应以小写字母开头，由小写字母、数字与连字符组成")}
				}
				if _, dup := targets[id]; dup {
					return e, []workspace.Problem{invalidValue("target", v, "指标 "+id+" 重复")}
				}
				n, err := strconv.Atoi(strings.TrimSpace(score))
				if err != nil {
					return e, []workspace.Problem{invalidValue("target", v, "指标 "+id+" 的分值应为整数")}
				}
				targets[id] = n
			}
		}
		e.Targets = &targets
	}
	switch {
	case e.After != "" && e.Before != "":
		return e, []workspace.Problem{invalidValue("before", e.Before, "--after 与 --before 只能给出一个")}
	case e.After == id:
		return e, []workspace.Problem{invalidValue("after", id, "不能相对该节本身放置")}
	case e.Before == id:
		return e, []workspace.Problem{invalidValue("before", id, "不能相对该节本身放置")}
	}
	exists := existing != nil && slices.ContainsFunc(existing.Sections, func(s outline.Section) bool { return s.ID == id })
	if !exists && e.Title == nil {
		return e, []workspace.Problem{invalidValue("title", inv.values["title"], "新增大纲节时必须给出 --title")}
	}
	if exists && e.Title == nil && e.Role == nil && e.Covers == nil && e.Uses == nil && e.Reason == nil && e.Characters == nil && e.Beats == nil && e.Reveal == nil && e.Targets == nil && e.After == "" && e.Before == "" {
		return e, []workspace.Problem{{Code: codeInvalidValue, Severity: workspace.SeverityError,
			Message: "没有给出要修改的内容；至少给出 --title、--role、--covers、--uses、--reason、--characters、--beats、--reveal、--target、--after 或 --before 之一"}}
	}
	return e, nil
}

// remainingRefs 返回修改成功后大纲中仍然存在的引用问题，作为 warning 报告，提示 Agent 大纲还没有修复完。
func remainingRefs(dir, unitDir string, ms outline.Messages) []workspace.Problem {
	_, _, refs := outline.LoadLenient(dir, unitDir, ms)
	for i := range refs {
		refs[i].Severity = workspace.SeverityWarning
	}
	return append(refs, methodWarning(ms)...)
}

// outlineMetrics 返回作品 w 按文体合并的 AI 评分指标（Metric），供场景契约（Scene Contract）的 targets 检查；
// 生效配置无法解析时返回 nil（其问题由 validate 报告），修改与查看大纲的命令不因此拒绝。
func outlineMetrics(root string, w *work.Work) []catalog.ScoredMetric {
	if r, _ := series.Resolve(root, w); r != nil {
		return metricDefs(r.ScoredMetrics)
	}
	return nil
}

// methodWarning 把写作简报选用的写作法不存在的问题作为 warning 返回：查看与修改大纲照常执行，写作法缺口暂不检查。
func methodWarning(ms outline.Messages) []workspace.Problem {
	if ms.MethodProblem == nil {
		return nil
	}
	p := *ms.MethodProblem
	p.Severity = workspace.SeverityWarning
	p.Message += "；在写作简报改选写作法之前，大纲不按写作法检查"
	return []workspace.Problem{p}
}

// outlineParamNext 为参数引起的问题（不填 path）给出用法与相关的查看命令；已有产物中的问题保留默认的复查命令。
func outlineParamNext(res *outlineResult, dir, ref string) {
	for i, d := range res.Diagnostics {
		if d.Path != "" {
			continue
		}
		next := []string{"wa-cli help outline set --json"}
		switch d.Code {
		case outline.CodeUnknownMessage:
			next = append(next, shellCommand("wa-cli", "brief", "show", ref, "--dir", dir, "--json"))
		case outline.CodeUnknownSource:
			next = append(next, shellCommand("wa-cli", "source", "list", "--dir", dir, "--json"))
		case outline.CodeUnknownBeat:
			if work, _, found := strings.Cut(ref, "/"); found {
				ref = work
			}
			next = append(next, shellCommand("wa-cli", "outline", "show", ref, "--dir", dir, "--json"))
		case outline.CodeUnknownTarget, outline.CodeTargetOutOfRange:
			next = append(next, shellCommand("wa-cli", "config", "resolved", "--dir", dir, "--json"))
		case outline.CodeSectionNotFound:
			next = append(next, shellCommand("wa-cli", "outline", "show", ref, "--dir", dir, "--json"))
		}
		res.Diagnostics[i].Next = next
	}
}

func runOutlineSet(inv *invocation) int {
	dir := projectDir(inv)
	res := newOutlineResult()
	problems := workspace.Require(dir)
	edit := false
	if problems == nil {
		var w *work.Work
		var u *work.Unit
		if w, u, problems = work.Resolve(dir, inv.args[0]); u != nil {
			unitDir := w.UnitDir(u)
			ms := outline.LoadMessages(dir, unitDir, brief.LoadQueue(dir), outline.Picker{Methods: catalog.LoadMethods(dir), Default: unitDefaultMethod(w, u)})
			ms.Metrics = outlineMetrics(dir, w)
			var existing *outline.Outline
			// 已有大纲中的引用问题不妨碍修改：写作简报删除核心观点后，要能用 outline set 修复大纲。
			if existing, problems, _ = outline.LoadLenient(dir, unitDir, ms); problems == nil {
				var e outline.Edit
				if e, problems = outlineEdit(inv, existing); problems == nil {
					var o *outline.Outline
					var changed []string
					ensure := func() ([]string, []workspace.Problem) { return draft.EnsureUnitDir(dir, w, u) }
					if o, changed, problems = outline.Set(dir, unitDir, e, ms, ensure); o != nil {
						res.fill(u.Ref, o, ms)
						res.ChangedPaths = changed
						problems = remainingRefs(dir, unitDir, ms)
					}
				}
				edit = true
			}
		}
	}
	res.Diagnostics = diagnostics(problems, dir)
	if edit {
		outlineParamNext(res, dir, inv.args[0])
		for i, d := range res.Diagnostics {
			if d.Code == outline.CodeAnchoredValue {
				res.Diagnostics[i].Next = []string{"wa-cli help outline set --json"}
			}
		}
	}
	return finish(inv, res, func(out io.Writer) error {
		if res.Outline == nil {
			return nil
		}
		verb := "已更新"
		if len(res.ChangedPaths) == 0 {
			verb = "取值未变，没有修改"
		}
		_, err := io.WriteString(out, fmt.Sprintf("大纲（Outline）%s：%s\n", verb, res.Outline.Path)+outlineText(res))
		return err
	})
}

func runOutlineRemove(inv *invocation) int {
	dir := projectDir(inv)
	res := newOutlineResult()
	problems := workspace.Require(dir)
	if problems == nil {
		var w *work.Work
		var u *work.Unit
		if w, u, problems = work.Resolve(dir, inv.args[0]); u != nil {
			unitDir := w.UnitDir(u)
			ms := outline.LoadMessages(dir, unitDir, brief.LoadQueue(dir), outline.Picker{Methods: catalog.LoadMethods(dir), Default: unitDefaultMethod(w, u)})
			ms.Metrics = outlineMetrics(dir, w)
			var o *outline.Outline
			var changed []string
			if o, changed, problems = outline.Remove(dir, unitDir, inv.args[1], ms); o != nil {
				res.fill(u.Ref, o, ms)
				res.ChangedPaths = changed
				problems = remainingRefs(dir, unitDir, ms)
			}
		}
	}
	res.Diagnostics = diagnostics(problems, dir)
	for i, d := range res.Diagnostics {
		switch d.Code {
		case outline.CodeSectionNotFound:
			res.Diagnostics[i].Next = []string{shellCommand("wa-cli", "outline", "show", inv.args[0], "--dir", dir, "--json")}
		case outline.CodeAnchoredValue:
			res.Diagnostics[i].Next = []string{"wa-cli help outline remove --json"}
		}
	}
	return finish(inv, res, func(out io.Writer) error {
		if res.Outline == nil {
			return nil
		}
		_, err := io.WriteString(out, fmt.Sprintf("已从大纲（Outline）删除节 %s：%s\n", inv.args[1], res.Outline.Path)+outlineText(res))
		return err
	})
}

func runOutlineShow(inv *invocation) int {
	dir := projectDir(inv)
	res := newOutlineResult()
	problems := workspace.Require(dir)
	if problems == nil {
		var w *work.Work
		var u *work.Unit
		if w, u, problems = work.Resolve(dir, inv.args[0]); u != nil {
			unitDir := w.UnitDir(u)
			ms := outline.LoadMessages(dir, unitDir, brief.LoadQueue(dir), outline.Picker{Methods: catalog.LoadMethods(dir), Default: unitDefaultMethod(w, u)})
			ms.Metrics = outlineMetrics(dir, w)
			if problems = ms.Problems; problems == nil {
				// 引用问题照常报告，但仍输出大纲与覆盖情况，便于据此修复。
				o, fatal, refs := outline.LoadLenient(dir, unitDir, ms)
				if problems = append(append(fatal, refs...), methodWarning(ms)...); len(fatal) == 0 {
					res.fill(u.Ref, o, ms)
				}
			}
		}
	}
	res.Diagnostics = diagnostics(problems, dir)
	return finish(inv, res, func(out io.Writer) error {
		if hasError(res.Diagnostics) && res.Outline == nil {
			return nil
		}
		text := "单元（Unit）" + inv.args[0] + " 还没有大纲（Outline）\n"
		if res.Outline != nil {
			text = "大纲（Outline）" + res.Outline.Path + "\n"
		}
		_, err := io.WriteString(out, text+outlineText(res))
		return err
	})
}

// outlineText 以人读文字列出大纲的节、核心观点的覆盖情况与覆盖缺口。
func outlineText(res *outlineResult) string {
	var s strings.Builder
	if res.Outline != nil {
		for i, sec := range res.Outline.Sections {
			detail := "覆盖 " + strings.Join(sec.Covers, "、")
			if len(sec.Covers) == 0 {
				detail = "不覆盖核心观点"
				if sec.Reason != nil {
					detail += "：" + *sec.Reason
				}
			}
			if len(sec.Uses) > 0 {
				detail += "；素材 " + strings.Join(sec.Uses, "、")
			}
			if sec.Role != nil {
				detail += "；承担写作法的 " + *sec.Role
			}
			if len(sec.Characters) > 0 {
				detail += "；出场人物 " + strings.Join(sec.Characters, "、")
			}
			if len(sec.Beats) > 0 {
				detail += "；承担节拍 " + strings.Join(sec.Beats, "、")
			}
			if sec.Reveal != nil {
				detail += "；不提前透露：" + *sec.Reveal
			}
			if sec.Pov != nil {
				detail += "；视角 " + *sec.Pov
			}
			if sec.Location != nil {
				detail += "；地点 " + *sec.Location
			}
			for _, sc := range sec.StateChanges {
				detail += "；必须发生：" + sc.Text + "（" + sc.ID + "）"
			}
			if len(sec.Plants) > 0 {
				detail += "；埋设伏笔 " + strings.Join(sec.Plants, "、")
			}
			if len(sec.Recovers) > 0 {
				detail += "；回收伏笔 " + strings.Join(sec.Recovers, "、")
			}
			if len(sec.Causes) > 0 {
				detail += "；起因 " + strings.Join(sec.Causes, "、")
			}
			if len(sec.Events) > 0 {
				detail += "；产生事件 " + strings.Join(sec.Events, "、")
			}
			if len(sec.Targets) > 0 {
				var items []string
				for _, k := range slices.Sorted(maps.Keys(sec.Targets)) {
					items = append(items, fmt.Sprintf("%s=%d", k, sec.Targets[k]))
				}
				detail += "；目标 " + strings.Join(items, "、")
			}
			fmt.Fprintf(&s, "  %d. %s %s（%s）\n", i+1, sec.ID, sec.Title, detail)
		}
	}
	for _, c := range res.Coverage {
		where := "未覆盖"
		if len(c.Sections) > 0 {
			where = "由 " + strings.Join(c.Sections, "、") + " 覆盖"
		}
		fmt.Fprintf(&s, "  核心观点（Key Message）%s：%s（%s）\n", c.KeyMessage, c.Text, where)
	}
	for _, g := range res.Gaps {
		fmt.Fprintf(&s, "  覆盖缺口：%s\n", g.Message)
	}
	return s.String()
}
