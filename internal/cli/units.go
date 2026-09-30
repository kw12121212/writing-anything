package cli

import (
	"fmt"
	"io"
	"strings"

	"github.com/kw12121212/writing-anything/internal/catalog"
	"github.com/kw12121212/writing-anything/internal/schema"
	"github.com/kw12121212/writing-anything/internal/work"
	"github.com/kw12121212/writing-anything/internal/workspace"
)

const unitRefHelp = "单元引用（Unit Reference）：<work> 表示作品的根单元，<work>/<unit-id> 表示作品中任意深度的单元"

var newCommand = &command{
	name:    "new",
	summary: "按文体（Genre）创建作品（Work）；作品本身是单元树（Unit Tree）的根单元（Root Unit）",
	args: []argSpec{
		{"genre", "文体 id，如 minimal", true},
		{"slug", "作品 id：小写字母开头，由小写字母、数字与连字符组成", true},
	},
	flags:      []flagSpec{dirFlag},
	outputKeys: []outputKey{{"work", "新建的作品：id、genre、type（根单元类型）与 stage；失败时为 null"}},
	exitCodes:  standardExitCodes,
	run:        runNew,
}

var unitAddCommand = &command{
	name:    "unit add",
	summary: "在父单元的子单元末尾追加一个单元（Unit），从文体的第一个阶段开始",
	args: []argSpec{
		{"parent", "父单元的" + unitRefHelp, true},
		{"type", "新单元的单元类型，必须是父单元类型允许的子单元类型", true},
		{"id", "新单元的 id，在作品内唯一", true},
	},
	flags:      []flagSpec{dirFlag},
	outputKeys: []outputKey{{"unit", "新单元：unit（单元引用）、type、parent（父单元引用）与 stage；失败时为 null"}},
	exitCodes:  standardExitCodes,
	run:        runUnitAdd,
}

var advanceCommand = &command{
	name:       "advance",
	summary:    "把单元（Unit）推进到文体声明的下一个阶段（Stage）；缺少必需产物（Required Artifact）时拒绝推进",
	args:       []argSpec{{"unit", unitRefHelp, true}},
	flags:      []flagSpec{dirFlag},
	outputKeys: []outputKey{{"unit", "推进后的单元：unit、type、previousStage 与 stage；失败时为 null"}},
	exitCodes:  standardExitCodes,
	run:        runAdvance,
}

var nextCommand = &command{
	name:       "next",
	summary:    "列出单元（Unit）的当前阶段（Stage）、阻塞项（Blocker）与下一步命令",
	args:       []argSpec{{"unit", "只列出该单元；省略时列出所有未完成的单元。" + unitRefHelp, false}},
	flags:      []flagSpec{dirFlag},
	outputKeys: []outputKey{{"steps", "下一步数组，每项含 unit、stage、blockers、action 与 commands"}},
	exitCodes:  standardExitCodes,
	run:        runNext,
}

type workView struct {
	ID    string `json:"id"`
	Genre string `json:"genre"`
	Type  string `json:"type"`
	Stage string `json:"stage"`
}

type newResult struct {
	Result
	Work *workView `json:"work"`
}

type addedUnit struct {
	Unit   string `json:"unit"`
	Type   string `json:"type"`
	Parent string `json:"parent"`
	Stage  string `json:"stage"`
}

type unitAddResult struct {
	Result
	Unit *addedUnit `json:"unit"`
}

type advancedUnit struct {
	Unit          string `json:"unit"`
	Type          string `json:"type"`
	PreviousStage string `json:"previousStage"`
	Stage         string `json:"stage"`
}

type advanceResult struct {
	Result
	Unit *advancedUnit `json:"unit"`
}

type blockerView struct {
	Code     string `json:"code"`
	Artifact string `json:"artifact"`
	Path     string `json:"path"`
	Message  string `json:"message"`
}

type stepView struct {
	Unit     string        `json:"unit"`
	Stage    string        `json:"stage"`
	Blockers []blockerView `json:"blockers"`
	Action   string        `json:"action"`
	Commands []string      `json:"commands"`
}

type nextResult struct {
	Result
	Steps []stepView `json:"steps"`
}

// unitView 是 status 输出 units 中的一项。
type unitView struct {
	Unit     string        `json:"unit"`
	Work     string        `json:"work"`
	Type     string        `json:"type"`
	Parent   *string       `json:"parent"`
	Stage    string        `json:"stage"`
	Blockers []blockerView `json:"blockers"`
	Commands []string      `json:"commands"`
	depth    int
}

func runNew(inv *invocation) int {
	dir := projectDir(inv)
	genre, slug := inv.args[0], inv.args[1]
	res := &newResult{}
	problems := workspace.Require(dir)
	if problems == nil {
		var w *work.Work
		var created []string
		if w, created, problems = work.Create(dir, genre, slug); w != nil {
			res.Work = &workView{ID: w.ID, Genre: w.Genre.ID, Type: w.Root.Type, Stage: w.Root.Stage}
			res.ChangedPaths = created
		}
	}
	res.Diagnostics = diagnostics(problems, dir)
	for i, p := range problems {
		switch p.Code {
		case work.CodeUnknownGenre:
			var next []string
			for _, g := range catalog.BuiltinGenres() {
				next = append(next, shellCommand("wa-cli", "new", g, slug, "--dir", dir, "--json"))
			}
			res.Diagnostics[i].Next = next
		case schema.CodeSchemaViolation:
			res.Diagnostics[i].Next = []string{"wa-cli help new --json"}
		}
	}
	return finish(inv, res, func(w io.Writer) error {
		if res.Work == nil {
			return nil
		}
		_, err := fmt.Fprintf(w, "已创建作品（Work）%s：文体 %s，根单元类型 %s，阶段 %s\n", res.Work.ID, res.Work.Genre, res.Work.Type, res.Work.Stage)
		return err
	})
}

func runUnitAdd(inv *invocation) int {
	dir := projectDir(inv)
	parent, typ, id := inv.args[0], inv.args[1], inv.args[2]
	res := &unitAddResult{}
	problems := workspace.Require(dir)
	edit := false
	if problems == nil {
		// 先单独解析父单元：作品本身的问题由 validate 复查，追加单元引起的问题另给下一步。
		if _, _, problems = work.Resolve(dir, parent); problems == nil {
			var w *work.Work
			var u *work.Unit
			if w, u, problems = work.AddUnit(dir, parent, typ, id); u != nil {
				res.Unit = &addedUnit{Unit: u.Ref, Type: u.Type, Parent: u.Parent.Ref, Stage: u.Stage}
				res.ChangedPaths = []string{w.Path}
			}
			edit = true
		}
	}
	res.Diagnostics = diagnostics(problems, dir)
	if edit {
		for i := range res.Diagnostics {
			res.Diagnostics[i].Next = []string{"wa-cli help unit add --json", shellCommand("wa-cli", "status", "--dir", dir, "--json")}
		}
	}
	return finish(inv, res, func(w io.Writer) error {
		if res.Unit == nil {
			return nil
		}
		_, err := fmt.Fprintf(w, "已在 %s 下追加单元（Unit）%s：类型 %s，阶段 %s\n", res.Unit.Parent, res.Unit.Unit, res.Unit.Type, res.Unit.Stage)
		return err
	})
}

func runAdvance(inv *invocation) int {
	dir := projectDir(inv)
	res := &advanceResult{}
	problems := workspace.Require(dir)
	if problems == nil {
		var w *work.Work
		var u *work.Unit
		var previous string
		if w, u, previous, problems = work.Advance(dir, inv.args[0]); u != nil {
			res.Unit = &advancedUnit{Unit: u.Ref, Type: u.Type, PreviousStage: previous, Stage: u.Stage}
			res.ChangedPaths = []string{w.Path}
		}
	}
	res.Diagnostics = diagnostics(problems, dir)
	for i, p := range problems {
		if p.Code == work.CodeAnchoredValue {
			res.Diagnostics[i].Next = []string{"wa-cli help advance --json"}
		}
	}
	return finish(inv, res, func(w io.Writer) error {
		if res.Unit == nil {
			return nil
		}
		_, err := fmt.Fprintf(w, "单元（Unit）%s 已从 %s 推进到 %s\n", res.Unit.Unit, res.Unit.PreviousStage, res.Unit.Stage)
		return err
	})
}

func runNext(inv *invocation) int {
	dir := projectDir(inv)
	res := &nextResult{Steps: []stepView{}}
	problems := workspace.Require(dir)
	if problems == nil {
		if len(inv.args) == 1 {
			var w *work.Work
			var u *work.Unit
			if w, u, problems = work.Resolve(dir, inv.args[0]); u != nil {
				res.Steps = append(res.Steps, step(dir, w, u))
			}
		} else {
			var works []*work.Work
			works, _, problems = work.LoadAll(dir)
			for _, w := range works {
				for _, u := range w.Units() {
					if u.Stage != catalog.StageDone {
						res.Steps = append(res.Steps, step(dir, w, u))
					}
				}
			}
		}
	}
	res.Diagnostics = diagnostics(problems, dir)
	return finish(inv, res, func(w io.Writer) error {
		var b strings.Builder
		if len(res.Steps) == 0 && !hasError(res.Diagnostics) {
			b.WriteString("没有需要推进的单元（Unit）\n")
		}
		for _, s := range res.Steps {
			fmt.Fprintf(&b, "%s（阶段 %s）：%s\n", s.Unit, s.Stage, s.Action)
			for _, bl := range s.Blockers {
				fmt.Fprintf(&b, "  阻塞项：%s\n", bl.Message)
			}
			for _, c := range s.Commands {
				fmt.Fprintf(&b, "  下一步：%s\n", c)
			}
		}
		_, err := io.WriteString(w, b.String())
		return err
	})
}

func step(dir string, w *work.Work, u *work.Unit) stepView {
	blockers, commands := progress(dir, w, u)
	return stepView{Unit: u.Ref, Stage: u.Stage, Blockers: blockers, Action: action(w, u, blockers), Commands: commands}
}

// progress 返回单元的阻塞项与可以直接运行的下一步命令，next 与 status 共用；数组永不为 nil。
func progress(dir string, w *work.Work, u *work.Unit) ([]blockerView, []string) {
	blockers, commands := []blockerView{}, []string{}
	for _, b := range w.Blockers(dir, u) {
		blockers = append(blockers, blockerView{Code: b.Code, Artifact: b.Artifact, Path: b.Path, Message: b.Message})
	}
	// 被缺失产物阻塞时只能列出为该产物登记的生成命令；目前还没有产物登记生成命令，commands 为空数组。
	if u.Stage != catalog.StageDone && len(blockers) == 0 {
		commands = append(commands, shellCommand("wa-cli", "advance", u.Ref, "--dir", dir, "--json"))
	}
	return blockers, commands
}

// action 以人读文字说明单元的下一步。
func action(w *work.Work, u *work.Unit, blockers []blockerView) string {
	switch {
	case u.Stage == catalog.StageDone:
		return "已走完全部阶段"
	case len(blockers) > 0:
		var missing []string
		for _, b := range blockers {
			missing = append(missing, fmt.Sprintf("%s（在 %s/ 下写入 %s.yaml 或 %s.md）", b.Artifact, b.Path, b.Artifact, b.Artifact))
		}
		return "先产出必需产物（Required Artifact）" + strings.Join(missing, "、") + "，再推进"
	}
	return fmt.Sprintf("阶段 %s 的必需产物已齐备，可以推进到 %s", u.Stage, w.NextStage(u))
}

// unitViews 按作品与单元树先序列出每个单元，供 status 输出。
func unitViews(dir string, works []*work.Work) []unitView {
	out := []unitView{}
	for _, w := range works {
		for _, u := range w.Units() {
			v := unitView{Unit: u.Ref, Work: w.ID, Type: u.Type, Stage: u.Stage}
			if u.Parent != nil {
				v.Parent = &u.Parent.Ref
			}
			for p := u.Parent; p != nil; p = p.Parent {
				v.depth++
			}
			v.Blockers, v.Commands = progress(dir, w, u)
			out = append(out, v)
		}
	}
	return out
}
