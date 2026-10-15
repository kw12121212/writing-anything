package cli

import (
	"fmt"
	"io"
	"slices"
	"strings"

	"github.com/kw12121212/writing-anything/internal/brief"
	"github.com/kw12121212/writing-anything/internal/catalog"
	"github.com/kw12121212/writing-anything/internal/draft"
	"github.com/kw12121212/writing-anything/internal/impact"
	"github.com/kw12121212/writing-anything/internal/outline"
	"github.com/kw12121212/writing-anything/internal/schema"
	"github.com/kw12121212/writing-anything/internal/threads"
	"github.com/kw12121212/writing-anything/internal/work"
	"github.com/kw12121212/writing-anything/internal/workspace"
)

var sceneContractCommand = &command{
	name:    "scene contract",
	summary: "以命名参数写入场景（Scene）大纲节的长篇场景契约（Scene Contract）：视角、地点、出场人物、节拍、必须发生的状态变化、伏笔、揭示边界、起因与预期事件；起因引用不存在或尚未发生的事件时拒绝写入",
	args: []argSpec{
		{"unit", "场景单元（Unit）引用；必须是 scene 类型", true},
		{"section", "场景大纲中的大纲节（Outline Section）id；不存在时新增，此时必须给出 --title", true},
	},
	flags: []flagSpec{
		dirFlag,
		{"title", flagString, "节的标题"},
		{"pov", flagString, "视角人物；空字符串时删除"},
		{"location", flagString, "地点；空字符串时删除"},
		{"characters", flagString, "出场人物，用逗号分隔；整体替换，空字符串时删除"},
		{"beats", flagString, "承担的情节节拍（Story Beat）id，用逗号分隔，指向故事根单元大纲的节；整体替换，空字符串时删除"},
		{"reveal", flagString, "揭示边界（Reveal Boundary），本场景不能提前透露的信息；空字符串时删除"},
		{"state-change", flagList, "必须发生的状态变化，形如 <id>=<文本>；按 id 新增或更新，可重复给出"},
		{"plant", flagString, "要埋设的伏笔（Foreshadowing）id，用逗号分隔；整体替换，空字符串时删除"},
		{"recover", flagString, "要回收的伏笔 id，用逗号分隔；整体替换，空字符串时删除"},
		{"cause", flagString, "起因（Cause）：引用前文已发生事件的 id，用逗号分隔；整体替换，空字符串时删除"},
		{"event", flagString, "预期产生的事件（Event）id，用逗号分隔；整体替换，空字符串时删除"},
		{"time", flagString, "故事时间（Story Time）标记（如 第三日夜）；相同文本视为同时；空字符串时删除"},
		{"after", flagString, "时间线：声明本场景晚于的前文场景引用，用逗号分隔；整体替换，空字符串时删除"},
	},
	outputKeys: append(append([]outputKey{}, outlineKeys...), impactOutputKey),
	exitCodes:  standardExitCodes,
	run:        runSceneContract,
}

// sceneContractEdit 把 scene contract 的参数转换为修改；参数取值不合法时返回 input.invalid-value 或
// artifact.schema-violation（均不填 path）。existing 为已有的大纲（可为 nil），用于判断是否新增。
func sceneContractEdit(inv *invocation, existing *outline.Outline) (outline.Edit, []workspace.Problem) {
	id := inv.args[1]
	e := outline.Edit{ID: id}
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
	e.Title, e.Reveal, e.Pov, e.Location, e.Time = str("title"), str("reveal"), str("pov"), str("location"), str("time")
	if e.Title != nil && *e.Title == "" {
		return e, []workspace.Problem{invalidValue("title", "", "大纲节必须有标题，--title 不能为空")}
	}
	for _, name := range []string{"title", "reveal", "pov", "location"} {
		if v, ok := inv.values[name]; ok && v != "" && strings.TrimSpace(v) == "" {
			return e, []workspace.Problem{invalidValue(name, v, "不能只含空白")}
		}
	}
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
	for _, name := range []string{"beats", "plant", "recover", "cause", "event", "after"} {
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
		p := &list
		switch name {
		case "beats":
			e.Beats = p
		case "plant":
			e.Plants = p
		case "recover":
			e.Recovers = p
		case "cause":
			e.Causes = p
		case "after":
			e.TimelineAfter = p
		default:
			e.Events = p
		}
	}
	// 必须发生的状态变化：形如 <id>=<文本>，按 id 与该节既有条目合并（新增或更新，保留未涉及的条目），可重复给出。
	if vals := inv.lists["state-change"]; len(vals) > 0 {
		var sc []outline.StateChange
		if existing != nil {
			if i := slices.IndexFunc(existing.Sections, func(s outline.Section) bool { return s.ID == id }); i >= 0 {
				sc = append(sc, existing.Sections[i].StateChanges...)
			}
		}
		for _, v := range vals {
			scID, text, found := strings.Cut(v, "=")
			scID = strings.TrimSpace(scID)
			if !found || scID == "" {
				return e, []workspace.Problem{invalidValue("state-change", v, "每一项应为 <id>=<文本>，如 sc-1=精灵交出灯芯")}
			}
			if !brief.ValidID(scID) {
				return e, []workspace.Problem{invalidValue("state-change", v, "状态变化 id "+scID+" 不合法：应以小写字母开头，由小写字母、数字与连字符组成")}
			}
			if strings.TrimSpace(text) == "" {
				return e, []workspace.Problem{invalidValue("state-change", v, "状态变化 "+scID+" 的描述不能为空或只含空白")}
			}
			if j := slices.IndexFunc(sc, func(x outline.StateChange) bool { return x.ID == scID }); j >= 0 {
				sc[j] = outline.StateChange{ID: scID, Text: text}
				continue
			}
			sc = append(sc, outline.StateChange{ID: scID, Text: text})
		}
		e.StateChanges = &sc
	}
	exists := existing != nil && slices.ContainsFunc(existing.Sections, func(s outline.Section) bool { return s.ID == id })
	if !exists && e.Title == nil {
		return e, []workspace.Problem{invalidValue("title", inv.values["title"], "新增大纲节时必须给出 --title")}
	}
	if exists && e.Title == nil && e.Pov == nil && e.Location == nil && e.Characters == nil && e.Beats == nil &&
		e.Reveal == nil && e.StateChanges == nil && e.Plants == nil && e.Recovers == nil && e.Causes == nil && e.Events == nil && e.Time == nil && e.TimelineAfter == nil {
		return e, []workspace.Problem{{Code: codeInvalidValue, Severity: workspace.SeverityError,
			Message: "没有给出要修改的内容；至少给出 --title、--pov、--location、--characters、--beats、--reveal、--state-change、--plant、--recover、--cause 或 --event 之一"}}
	}
	return e, nil
}

// causalityPrecheck 以写入后的状态（Candidate 覆盖本场景的大纲）检查因果链，只拦截本次写入的 causes/events 引起的
// 问题：报告在本场景、且位于被编辑的节上，由参数引起——不填 path，字段去掉 sections[i] 前缀。其他节或其他场景的
// 既有问题不阻止写入（cross-artifact-references），由整体校验报告。
func causalityPrecheck(dir string, w *work.Work, u *work.Unit, e outline.Edit, existing *outline.Outline) []workspace.Problem {
	scenes := w.Scenes()
	cand := e.Candidate(existing)
	if cand == nil {
		return nil
	}
	unitDir := w.UnitDir(u)
	for i := range scenes {
		if scenes[i].Dir == unitDir {
			scenes[i].Outline = cand
		}
	}
	at := slices.IndexFunc(cand.Sections, func(s outline.Section) bool { return s.ID == e.ID })
	var ps []workspace.Problem
	for _, p := range outline.CausalityOn(dir, scenes, unitDir) {
		if at < 0 || !strings.HasPrefix(p.Field, fmt.Sprintf("sections[%d].", at)) {
			continue
		}
		p.Path, p.Line = "", 0
		if _, rest, ok := strings.Cut(p.Field, "]."); ok {
			p.Field = rest
		}
		ps = append(ps, p)
	}
	return ps
}

// threadsPrecheck 以写入后的状态检查场景契约的 plants/recovers 与伏笔台账（Foreshadowing Ledger）的交叉一致
// （threads-contract-checks），只拦截被编辑的节上、由本次 --plant/--recover 引起的问题：不填 path，字段去掉
// sections[i] 前缀。作品没有 threads.yaml 时按空台账检查（被引用的伏笔都未登记）；其他节或其他场景的既有问题不阻止
// 写入，由整体校验报告。
func threadsPrecheck(dir string, w *work.Work, u *work.Unit, e outline.Edit, existing *outline.Outline) []workspace.Problem {
	scenes := w.Scenes()
	cand := e.Candidate(existing)
	if cand == nil {
		return nil
	}
	unitDir := w.UnitDir(u)
	for i := range scenes {
		if scenes[i].Dir == unitDir {
			scenes[i].Outline = cand
		}
	}
	at := slices.IndexFunc(cand.Sections, func(s outline.Section) bool { return s.ID == e.ID })
	var ps []workspace.Problem
	for _, p := range threads.ContractProblems(dir, w, scenes) {
		if p.Path != outline.Path(unitDir) || at < 0 || !strings.HasPrefix(p.Field, fmt.Sprintf("sections[%d].", at)) {
			continue
		}
		p.Path, p.Line = "", 0
		if _, rest, ok := strings.Cut(p.Field, "]."); ok {
			p.Field = rest
		}
		ps = append(ps, p)
	}
	return ps
}

type sceneContractResult struct {
	outlineResult
	Impact []impact.Ripple `json:"impact"`
}

// contractSemantic 返回大纲 o 中节 id 的语义比较键（场景契约的起因 causes 与必须发生的状态变化 state_changes）；
// 大纲或节不存在时为空串。
func contractSemantic(o *outline.Outline, id string) string {
	if o == nil {
		return ""
	}
	i := slices.IndexFunc(o.Sections, func(s outline.Section) bool { return s.ID == id })
	if i < 0 {
		return ""
	}
	sec := o.Sections[i]
	var changes []string
	for _, sc := range sec.StateChanges {
		changes = append(changes, sc.ID+"="+sc.Text)
	}
	return impact.SemanticKey(impact.SortedKey(sec.Causes), impact.SortedKey(changes))
}

func runSceneContract(inv *invocation) int {
	dir := projectDir(inv)
	res := &sceneContractResult{outlineResult: *newOutlineResult(), Impact: []impact.Ripple{}}
	var auto []Diagnostic
	problems := workspace.Require(dir)
	edit := false      // 已进入大纲修改路径：参数问题由 sceneContractParamNext 改写 next
	paramNext := false // 因本次参数被拒绝（含非场景单元）：同样改写 next
	if problems == nil {
		var w *work.Work
		var u *work.Unit
		if w, u, problems = work.Resolve(dir, inv.args[0]); u != nil {
			if u.Type != outline.SceneType {
				problems = []workspace.Problem{{Code: outline.CodeNotScene, Severity: workspace.SeverityError, Field: "unit",
					Message: fmt.Sprintf("单元 %s 的类型是 %s；scene contract 只能写入 scene 类型单元（场景）的场景契约（Scene Contract）", u.Ref, u.Type)}}
				paramNext = true
			} else {
				unitDir := w.UnitDir(u)
				ms := outline.LoadMessages(dir, unitDir, brief.LoadQueue(dir), outline.Picker{Methods: catalog.LoadMethods(dir), Default: unitDefaultMethod(w, u)})
				ms.Metrics = outlineMetrics(dir, w)
				var existing *outline.Outline
				// 已有大纲中的引用问题不妨碍修改（cross-artifact-references），与 outline set 一致。
				if existing, problems, _ = outline.LoadLenient(dir, unitDir, ms); problems == nil {
					var e outline.Edit
					if e, problems = sceneContractEdit(inv, existing); problems == nil {
						// 给出 --cause 或 --event 时按写入后的状态即时检查因果链（scene-causality），检查通过前不写文件。
						if e.Causes != nil || e.Events != nil {
							problems = causalityPrecheck(dir, w, u, e, existing)
						}
						// 给出 --plant 或 --recover 时按写入后的状态即时检查伏笔台账的交叉一致（threads-contract-checks）。
						if problems == nil && (e.Plants != nil || e.Recovers != nil) {
							problems = threadsPrecheck(dir, w, u, e, existing)
						}
						if problems == nil {
							var o *outline.Outline
							var changed []string
							ensure := func() ([]string, []workspace.Problem) { return draft.EnsureUnitDir(dir, w, u) }
							pre := preAffected(dir, w, u.Ref)
							if o, changed, problems = outline.Set(dir, unitDir, e, ms, ensure); o != nil {
								res.fill(u.Ref, o, ms)
								res.ChangedPaths = changed
								problems = remainingRefs(dir, unitDir, ms)
								if len(changed) > 0 && contractSemantic(existing, e.ID) != contractSemantic(o, e.ID) {
									var more []string
									res.Impact, more, auto = autoImpact(dir, w, []impactTarget{{target: u.Ref, pre: pre}})
									res.ChangedPaths = append(res.ChangedPaths, more...)
								}
							}
						}
					}
					edit = true
				}
			}
		}
	}
	res.Diagnostics = diagnostics(problems, dir)
	if edit || paramNext {
		sceneContractParamNext(&res.outlineResult, dir, inv.args[0])
		for i, d := range res.Diagnostics {
			if d.Code == outline.CodeAnchoredValue {
				res.Diagnostics[i].Next = []string{shellCommand("wa-cli", "outline", "show", inv.args[0], "--dir", dir, "--json"),
					"wa-cli help scene contract --json"}
			}
		}
	}
	res.Diagnostics = append(res.Diagnostics, auto...)
	return finish(inv, res, func(out io.Writer) error {
		if res.Outline == nil {
			return nil
		}
		verb := "已更新"
		if len(res.ChangedPaths) == 0 {
			verb = "取值未变，没有修改"
		}
		_, err := io.WriteString(out, fmt.Sprintf("场景契约（Scene Contract）%s：%s\n", verb, res.Outline.Path)+outlineText(&res.outlineResult)+impactText(res.Impact))
		return err
	})
}

// sceneContractParamNext 为参数引起的问题（不填 path）先给出该场景的大纲，再给出用法与相关的查看命令；已有产物中的问题保留默认的复查命令。
func sceneContractParamNext(res *outlineResult, dir, ref string) {
	show := shellCommand("wa-cli", "outline", "show", ref, "--dir", dir, "--json")
	work, _, _ := strings.Cut(ref, "/")
	for i, d := range res.Diagnostics {
		if d.Path != "" {
			continue
		}
		next := []string{show, "wa-cli help scene contract --json"}
		switch d.Code {
		case outline.CodeUnknownBeat:
			// 节拍由根单元的写作法声明：场景的问题再给出根单元的大纲。
			if work != ref {
				next = append(next, shellCommand("wa-cli", "outline", "show", work, "--dir", dir, "--json"))
			}
		case threads.CodeUnknownThread, threads.CodeRecoverNotPlanted:
			next = append(next, "wa-cli help threads add --json", shellCommand("wa-cli", "threads", work, "--dir", dir, "--json"))
		}
		res.Diagnostics[i].Next = next
	}
}
