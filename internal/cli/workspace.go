package cli

import (
	"fmt"
	"io"
	"maps"
	"slices"
	"strconv"
	"strings"

	"github.com/kw12121212/writing-anything/internal/brief"
	"github.com/kw12121212/writing-anything/internal/canon"
	"github.com/kw12121212/writing-anything/internal/catalog"
	"github.com/kw12121212/writing-anything/internal/decision"
	"github.com/kw12121212/writing-anything/internal/draft"
	"github.com/kw12121212/writing-anything/internal/feedback"
	"github.com/kw12121212/writing-anything/internal/learn"
	"github.com/kw12121212/writing-anything/internal/marketing"
	"github.com/kw12121212/writing-anything/internal/metrics"
	"github.com/kw12121212/writing-anything/internal/outline"
	"github.com/kw12121212/writing-anything/internal/packinstall"
	"github.com/kw12121212/writing-anything/internal/plan"
	"github.com/kw12121212/writing-anything/internal/resolve"
	"github.com/kw12121212/writing-anything/internal/review"
	"github.com/kw12121212/writing-anything/internal/schema"
	"github.com/kw12121212/writing-anything/internal/series"
	"github.com/kw12121212/writing-anything/internal/knowledge"
	"github.com/kw12121212/writing-anything/internal/state"
	"github.com/kw12121212/writing-anything/internal/source"
	"github.com/kw12121212/writing-anything/internal/threads"
	"github.com/kw12121212/writing-anything/internal/work"
	"github.com/kw12121212/writing-anything/internal/workspace"
)

var dirFlag = flagSpec{"dir", flagString, "项目目录，即包含 .writing-anything/ 的目录；默认为当前目录"}

var initCommand = &command{
	name:    "init",
	summary: "创建工作区（Workspace）.writing-anything/；重复运行只补齐缺失的文件与目录",
	flags: []flagSpec{
		dirFlag,
		{"language", flagString, "配置的语言标签，默认 zh-CN"},
		{"genre", flagString, "默认文体（Genre）id"},
		{"gate-level", flagString, "关卡强度（Gate Level）：strict、standard 或 fast，默认 standard"},
	},
	outputKeys: []outputKey{{"workspace", "相对项目目录的工作区路径"}},
	exitCodes:  standardExitCodes,
	run:        runInit,
}

var validateCommand = &command{
	name:       "validate",
	summary:    "按 JSON Schema 校验整个工作区或单个产物（Artifact）",
	args:       []argSpec{{"path", "要校验的产物路径，相对项目目录或为绝对路径；省略时校验整个工作区", false}},
	flags:      []flagSpec{dirFlag},
	outputKeys: []outputKey{{"checked", "已校验的产物路径数组，相对项目目录"}},
	exitCodes:  standardExitCodes,
	run:        runValidate,
}

var statusCommand = &command{
	name:    "status",
	summary: "汇总工作区的配置、素材与作品数量、每个单元（Unit）的阶段（Stage）与阻塞项（Blocker），以及校验结果",
	flags:   []flagSpec{dirFlag},
	outputKeys: []outputKey{
		{"workspace", "相对项目目录的工作区路径"},
		{"config", "配置摘要，含 language、defaultGenre、packs、gateLevel；配置无法解析时为 null"},
		{"sources", "sources/ 下的直接条目数，不含隐藏条目"},
		{"works", "works/ 下的直接条目数，不含隐藏条目"},
		{"units", "按作品与单元树先序列出的单元，每项含 unit、work、type、parent、stage、blockers、commands 与 progress（汇总单元（Aggregated Unit）的进度，其他单元为 null）；汇总单元的 stage 与 blockers 由子单元汇总"},
	},
	exitCodes: standardExitCodes,
	run:       runStatus,
}

var configSetCommand = &command{
	name:    "config set",
	summary: "修改工作区配置中的一项：language、default_genre、packs、gate_level、context_budget 或 ai_disclosure",
	args: []argSpec{
		{"key", "配置项：language、default_genre、packs、gate_level、context_budget（上下文预算（Context Budget），字符数）或 ai_disclosure（AI 参与说明（AI-generated Content Label）的放置位置：footer、metadata 或 both）", true},
		{"value", "新取值；packs 用逗号分隔，空字符串表示清空；default_genre、context_budget 与 ai_disclosure 为空字符串时删除该项", true},
	},
	flags:      []flagSpec{dirFlag},
	outputKeys: []outputKey{{"config", "修改后（未修改时为当前）的配置摘要；配置无法解析时为 null"}},
	exitCodes:  standardExitCodes,
	run:        runConfigSet,
}

var configResolvedCommand = &command{
	name:       "config resolved",
	summary:    "按 文体（Genre）→ 规范包（Pack）→ 项目配置 合并生效的设置，并列出每项的来源",
	flags:      []flagSpec{dirFlag},
	outputKeys: []outputKey{{"resolved", "解析后配置（Resolved Config），含 genre、packs、unitTypes、stages、rules、glossary、bannedTerms、style、rubric，每项带 source；有 error 级诊断时为 null"}},
	exitCodes:  standardExitCodes,
	run:        runConfigResolved,
}

type initResult struct {
	Result
	Workspace string `json:"workspace"`
}

type validateResult struct {
	Result
	Checked []string `json:"checked"`
}

type statusResult struct {
	Result
	Workspace string            `json:"workspace"`
	Config    *workspace.Config `json:"config"`
	Sources   int               `json:"sources"`
	Works     int               `json:"works"`
	Units     []unitView        `json:"units"`
}

type configResolvedResult struct {
	Result
	Resolved *resolve.Resolved `json:"resolved"`
}

type configSetResult struct {
	Result
	Config *workspace.Config `json:"config"`
}

func runInit(inv *invocation) int {
	dir := projectDir(inv)
	set := map[string]string{}
	for flag, key := range map[string]string{"language": "language", "genre": "default_genre", "gate-level": "gate_level"} {
		if v, ok := inv.values[flag]; ok {
			set[key] = v
		}
	}
	created, problems := workspace.Init(dir, set)
	res := &initResult{Result: Result{ChangedPaths: created, Diagnostics: diagnostics(problems, dir)}, Workspace: workspace.Dir}
	for i, p := range problems {
		switch p.Code {
		case workspace.CodeConfigKept:
			res.Diagnostics[i].Next = []string{shellCommand("wa-cli", "config", "set", p.Field, set[p.Field], "--dir", dir)}
		case schema.CodeSchemaViolation:
			// 工作区尚未创建，修复命令是换一个取值重新运行 init。
			var next []string
			if p.Field == "gate_level" {
				for _, level := range workspace.GateLevels {
					next = append(next, shellCommand("wa-cli", "init", "--dir", dir, "--gate-level", level, "--json"))
				}
			}
			res.Diagnostics[i].Next = append(next, "wa-cli help init --json")
		}
	}
	return finish(inv, res, func(w io.Writer) error {
		if hasError(res.Diagnostics) {
			return nil
		}
		if len(created) == 0 {
			_, err := fmt.Fprintf(w, "工作区（Workspace）%s 已存在，没有需要补齐的文件\n", workspace.Dir)
			return err
		}
		for _, p := range created {
			if _, err := fmt.Fprintf(w, "已创建 %s\n", p); err != nil {
				return err
			}
		}
		return nil
	})
}

func runValidate(inv *invocation) int {
	dir := projectDir(inv)
	res := &validateResult{Checked: []string{}}
	problems := workspace.Require(dir)
	if problems == nil {
		var checked []string
		if len(inv.args) == 1 {
			checked, problems = workspace.ValidateArtifact(dir, inv.args[0])
			if len(checked) == 1 && len(problems) == 0 {
				problems = resolve.CheckArtifact(dir, checked[0])
				switch schema.KindFor(checked[0]).Name {
				case "work":
					problems = append(problems, work.CheckArtifact(dir, checked[0])...)
				case "source":
					problems = append(problems, source.CheckArtifact(dir, checked[0])...)
				case "claims":
					problems = append(problems, draft.CheckArtifact(dir, checked[0])...)
				case "canon":
					problems = append(problems, canon.CheckArtifact(dir, checked[0])...)
				case "threads":
					problems = append(problems, threads.CheckArtifact(dir, checked[0])...)
				case "state":
					problems = append(problems, state.CheckArtifact(dir, checked[0])...)
				case "knowledge":
					problems = append(problems, knowledge.CheckArtifact(dir, checked[0])...)
				case "brief":
					problems = append(problems, brief.CheckArtifact(dir, checked[0])...)
				case "outline":
					problems = append(problems, outline.CheckArtifact(dir, checked[0], defaultMethodOf(dir, checked[0]), metricsOfArtifact(dir, checked[0]), scenesOfOutlineArtifact(dir, checked[0]))...)
				case "plan":
					problems = append(problems, plan.CheckArtifact(dir, checked[0], defaultMethodOf(dir, checked[0]))...)
				case "feedback":
					problems = append(problems, feedback.CheckArtifact(dir, checked[0])...)
				case "decisions":
					_, problems = decision.Check(dir, unitExists(dir))
				case "review", "acceptance":
					problems = append(problems, review.CheckArtifact(dir, checked[0])...)
				case "marketing":
					problems = append(problems, marketing.CheckArtifact(dir, checked[0])...)
				case "series", "summary":
					problems = append(problems, series.CheckArtifact(dir, checked[0])...)
				case "pack-proposal":
					problems = append(problems, learn.CheckArtifact(dir, checked[0])...)
				case "metrics", "calibration":
					problems = append(problems, metrics.CheckArtifact(dir, checked[0])...)
				case "pack-lock":
					problems = append(problems, packinstall.CheckLockArtifact(dir)...)
				}
			}
		} else {
			checked, _, problems = checkWorkspace(dir)
		}
		res.Checked = append(res.Checked, checked...)
	}
	res.Diagnostics = diagnostics(problems, dir)
	return finish(inv, res, func(w io.Writer) error {
		if hasError(res.Diagnostics) {
			return nil
		}
		_, err := fmt.Fprintf(w, "校验通过：%s\n", strings.Join(res.Checked, "、"))
		return err
	})
}

func runStatus(inv *invocation) int {
	dir := projectDir(inv)
	res := &statusResult{Workspace: workspace.Dir, Units: []unitView{}}
	problems := workspace.Require(dir)
	if problems == nil {
		res.Config, _ = workspace.Load(dir)
		var works []*work.Work
		_, works, problems = checkWorkspace(dir)
		res.Sources, res.Works = workspace.Summary(dir)
		res.Units = unitViews(dir, works)
	}
	res.Diagnostics = diagnostics(problems, dir)
	return finish(inv, res, func(w io.Writer) error {
		if res.Config == nil && hasError(res.Diagnostics) && problems[0].Code == workspace.CodeNotFound {
			return nil
		}
		var b strings.Builder
		fmt.Fprintf(&b, "工作区（Workspace）：%s\n", res.Workspace)
		if c := res.Config; c != nil {
			genre, packs := "未设置", "无"
			if c.DefaultGenre != nil {
				genre = *c.DefaultGenre
			}
			if len(c.Packs) > 0 {
				packs = strings.Join(c.Packs, "、")
			}
			fmt.Fprintf(&b, "语言：%s\n默认文体（Genre）：%s\n规范包（Pack）：%s\n关卡强度（Gate Level）：%s\n", c.Language, genre, packs, c.GateLevel)
			if c.ContextBudget != nil {
				fmt.Fprintf(&b, "上下文预算（Context Budget）：%d 字符\n", *c.ContextBudget)
			}
			if c.AIDisclosure != nil {
				fmt.Fprintf(&b, "AI 参与说明（AI-generated Content Label）：%s\n", *c.AIDisclosure)
			}
		}
		fmt.Fprintf(&b, "素材（Source）：%d\n作品（Work）：%d\n", res.Sources, res.Works)
		if len(res.Units) > 0 {
			b.WriteString("单元（Unit）：\n")
		}
		for _, u := range res.Units {
			indent := strings.Repeat("  ", u.depth+1)
			stage := u.Stage
			if u.Progress != nil {
				stage = fmt.Sprintf("%s，进度 %d/%d", stage, u.Progress.Done, u.Progress.Total)
			}
			fmt.Fprintf(&b, "%s%s（%s）：阶段 %s", indent, u.Unit, u.Type, stage)
			if len(u.Blockers) > 0 {
				fmt.Fprintf(&b, "，阻塞项 %d 项", len(u.Blockers))
			}
			b.WriteString("\n")
			for _, bl := range u.Blockers {
				fmt.Fprintf(&b, "%s  阻塞项：%s\n", indent, bl.Message)
			}
			for _, c := range u.Commands {
				fmt.Fprintf(&b, "%s  下一步：%s\n", indent, c)
			}
		}
		if !hasError(res.Diagnostics) {
			b.WriteString("校验通过\n")
		}
		_, err := io.WriteString(w, b.String())
		return err
	})
}

func runConfigSet(inv *invocation) int {
	dir := projectDir(inv)
	key, value := inv.args[0], inv.args[1]
	res := &configSetResult{}
	problems := workspace.Require(dir)
	var changed bool
	if problems == nil {
		res.Config, changed, problems = workspace.SetConfig(dir, key, value)
	}
	if changed {
		res.ChangedPaths = []string{workspace.ConfigPath}
	}
	res.Diagnostics = diagnostics(problems, dir)
	return finish(inv, res, func(w io.Writer) error {
		var err error
		switch {
		case changed:
			_, err = fmt.Fprintf(w, "已把 %s 设为 %q\n", key, value)
		case !hasError(res.Diagnostics):
			_, err = fmt.Fprintf(w, "%s 已是 %q，未修改\n", key, value)
		}
		return err
	})
}

func runConfigResolved(inv *invocation) int {
	dir := projectDir(inv)
	res := &configResolvedResult{}
	problems := workspace.Require(dir)
	if problems == nil {
		res.Resolved, problems = resolve.Resolve(dir)
	}
	res.Diagnostics = diagnostics(problems, dir)
	return finish(inv, res, func(w io.Writer) error {
		if res.Resolved == nil {
			return nil
		}
		_, err := io.WriteString(w, resolvedText(res.Resolved))
		return err
	})
}

// resolvedText 以人读文本列出解析后配置的每一项，并注明来源层。
func resolvedText(r *resolve.Resolved) string {
	var b strings.Builder
	from := func(source string) string { return "（来自 " + source + "）" }
	if r.Genre == nil {
		b.WriteString("文体（Genre）：未设置\n")
	} else {
		fmt.Fprintf(&b, "文体（Genre）：%s（%s）\n", r.Genre.ID, r.Genre.Origin)
	}
	b.WriteString("规范包（Pack），按应用顺序：\n")
	for _, p := range r.Packs {
		where := p.Origin
		if p.Path != "" {
			where += "，" + p.Path
		}
		fmt.Fprintf(&b, "  %s（%s）\n", p.ID, where)
	}
	fmt.Fprintf(&b, "上下文预算（Context Budget）：%v 字符%s\n", r.ContextBudget.Value, from(r.ContextBudget.Source))
	fmt.Fprintf(&b, "AI 参与说明（AI-generated Content Label）：放在 %v%s；模板：%v%s\n", r.AIDisclosure.Placement.Value, from(r.AIDisclosure.Placement.Source),
		r.AIDisclosure.Template.Value, from(r.AIDisclosure.Template.Source))
	b.WriteString("样式主题（HTML Theme）：\n")
	for _, k := range catalog.HTMLThemeKeys {
		if v, ok := r.HTMLTheme[k]; ok {
			fmt.Fprintf(&b, "  %s：%v%s\n", k, v.Value, from(v.Source))
		}
	}
	section := func(title string, n int) bool {
		if n == 0 {
			fmt.Fprintf(&b, "%s：无\n", title)
			return false
		}
		fmt.Fprintf(&b, "%s：\n", title)
		return true
	}
	if section("单元类型（Unit Type）", len(r.UnitTypes)) {
		for _, u := range r.UnitTypes {
			children := ""
			if len(u.Children) > 0 {
				children = "，子单元类型 " + strings.Join(u.Children, "、")
			}
			fmt.Fprintf(&b, "  %s %s%s%s\n", u.ID, u.Title, children, from(u.Source))
		}
	}
	if section("阶段（Stage）", len(r.Stages)) {
		for _, s := range r.Stages {
			required := ""
			if len(s.RequiredArtifacts) > 0 {
				required = "，必需产物 " + strings.Join(s.RequiredArtifacts, "、")
			}
			fmt.Fprintf(&b, "  %s %s%s%s\n", s.ID, s.Title, required, from(s.Source))
		}
	}
	if section("规则（Rule）", len(r.Rules)) {
		for _, id := range slices.Sorted(maps.Keys(r.Rules)) {
			rule := r.Rules[id]
			fmt.Fprintf(&b, "  %s：%v%s，%v%s", id, rule.Type.Value, from(rule.Type.Source), rule.Severity.Value, from(rule.Severity.Source))
			for _, k := range slices.Sorted(maps.Keys(rule.Params)) {
				fmt.Fprintf(&b, "，%s=%v%s", k, rule.Params[k].Value, from(rule.Params[k].Source))
			}
			if rule.Waiver != nil {
				fmt.Fprintf(&b, "；已豁免%s：%s", from(rule.Waiver.Source), rule.Waiver.Reason)
			}
			b.WriteString("\n")
		}
	}
	if section("术语表（Glossary）", len(r.Glossary)) {
		for _, t := range r.Glossary {
			english, avoid := "", ""
			if t.English != "" {
				english = "（" + t.English + "）"
			}
			if len(t.Avoid) > 0 {
				avoid = "，避免写作 " + strings.Join(t.Avoid, "、")
			}
			fmt.Fprintf(&b, "  %s%s%s%s\n", t.Term.Term, english, avoid, from(t.Source))
		}
	}
	if section("禁用词（Banned Term）", len(r.BannedTerms)) {
		for _, t := range r.BannedTerms {
			line := t.Term
			if t.Reason != "" {
				line += "：" + t.Reason
			}
			if t.Replacement != "" {
				line += "，改用 " + t.Replacement
			}
			fmt.Fprintf(&b, "  %s%s\n", line, from(t.Source))
		}
	}
	if section("文风（Writing Style）", len(r.Style)) {
		for _, s := range r.Style {
			fmt.Fprintf(&b, "  %s%s\n", s.Text, from(s.Source))
		}
	}
	if section("评分标准（Rubric）", len(r.Rubric)) {
		for _, it := range r.Rubric {
			desc := ""
			if it.Description != "" {
				desc = "：" + it.Description
			}
			fmt.Fprintf(&b, "  %s %s%s%s\n", it.ID, it.Title, desc, from(it.Source))
		}
	}
	return b.String()
}

// checkWorkspace 校验整个工作区：工作区结构与配置，规范包文件、文体与规范包引用和合并问题，每个作品，
// 素材库中的每个素材，以及单元目录中的写入记录、论断、写作简报、大纲与反馈。同时返回能加载的作品。
func checkWorkspace(dir string) (checked []string, works []*work.Work, problems []workspace.Problem) {
	checked, problems = workspace.Check(dir)
	more, ps := resolve.Check(dir)
	works, wchecked, wps := work.LoadAll(dir)
	_, schecked, sps := source.LoadAll(dir)
	uchecked, ups := draft.CheckUnitArtifacts(dir, works)
	var dirs []string
	for _, w := range works {
		for _, u := range w.Units() {
			dirs = append(dirs, w.UnitDir(u))
		}
	}
	q := brief.LoadQueue(dir) // 写作简报与大纲共用一次加载的决策队列
	bchecked, bps := brief.CheckUnitArtifacts(dir, dirs, q)
	defaults := map[string]string{}                     // 单元目录 → 所在作品的文体的默认写作法；只有根单元适用（method-selection）
	genreMetrics := map[string][]catalog.ScoredMetric{} // 单元目录 → 所在作品按文体合并的 AI 评分指标（targets 检查）
	for _, w := range works {
		var ms []catalog.ScoredMetric
		if r, _ := series.Resolve(dir, w); r != nil {
			ms = metricDefs(r.ScoredMetrics)
		}
		for _, u := range w.Units() {
			if u.Parent == nil {
				defaults[w.UnitDir(u)] = w.Genre.DefaultMethod
			}
			genreMetrics[w.UnitDir(u)] = ms
		}
	}
	ochecked, ops := outline.CheckUnitArtifacts(dir, dirs, q, func(d string) string { return defaults[d] }, func(d string) []catalog.ScoredMetric { return genreMetrics[d] })
	// 因果链（Causality Chain）跨一个作品的多个场景大纲：每个作品按先序计算一次（scene-causality）。
	for _, w := range works {
		ops = append(ops, outline.Causality(dir, w.Scenes())...)
	}
	fchecked, fps := feedback.CheckUnitArtifacts(dir, works)
	rchecked, rps := review.CheckUnitArtifacts(dir, dirs)
	mchecked, mps := marketing.CheckUnitArtifacts(dir, dirs)
	dchecked, dps := decision.Check(dir, unitExists(dir))
	lchecked, lps := packinstall.Check(dir)
	xchecked, xps := metrics.CheckUnitArtifacts(dir, dirs)
	cchecked, cps := metrics.CheckCalibration(dir)
	pchecked, pps := learn.CheckAll(dir)
	echecked, eps := draft.CheckEdits(dir, works)
	tchecked, tps := series.Check(dir)
	ychecked, yps := series.CheckSummaries(dir, works)
	kchecked, kps := canon.CheckWorks(dir, works)
	thchecked, thps := threads.CheckWorks(dir, works)
	stchecked, stps := state.CheckWorks(dir, works)
	knchecked, knps := knowledge.CheckWorks(dir, works)
	plchecked, plps := plan.CheckUnitArtifacts(dir, works, catalog.LoadMethods(dir), unitDefaultMethod)
	checked = slices.Concat(checked, more, wchecked, schecked, uchecked, bchecked, ochecked, fchecked, rchecked, mchecked, dchecked, lchecked, xchecked, cchecked, pchecked, echecked, tchecked, ychecked, kchecked, thchecked, stchecked, knchecked, plchecked)
	return checked, works, slices.Concat(problems, ps, wps, sps, ups, bps, ops, fps, rps, mps, dps, lps, xps, cps, pps, eps, tps, yps, kps, thps, stps, knps, plps)
}

// defaultMethodOf 返回单元产物 rel 适用的文体默认写作法：只有作品的根单元适用，子单元不继承；
// 作品无法加载时为空（其问题由作品校验报告）。
func defaultMethodOf(dir, rel string) string {
	if w, u, _ := work.Resolve(dir, work.RefOf(rel[:strings.LastIndex(rel, "/")])); w != nil && u != nil && u.Parent == nil {
		return w.Genre.DefaultMethod
	}
	return ""
}

// metricsOfArtifact 返回单元产物 rel 所在作品按文体合并的 AI 评分指标（Metric），供 outline.yaml 的 targets 检查；
// 作品或生效配置无法加载时为 nil（其问题由作品与配置校验报告），跳过 targets 检查。
func metricsOfArtifact(dir, rel string) []catalog.ScoredMetric {
	w, _, _ := work.Resolve(dir, work.RefOf(strings.TrimSuffix(rel, "/"+outline.FileName)))
	if w == nil {
		return nil
	}
	r, _ := series.Resolve(dir, w)
	if r == nil {
		return nil
	}
	return metricDefs(r.ScoredMetrics)
}

// scenesOfOutlineArtifact 返回 outline.yaml 单产物校验所需的因果链场景列表：该作品全部场景单元的先序序列；
// 作品无法加载时为 nil（其问题由作品校验报告），跳过因果链检查。
func scenesOfOutlineArtifact(dir, rel string) []outline.Scene {
	w, _, _ := work.Resolve(dir, work.RefOf(strings.TrimSuffix(rel, "/"+outline.FileName)))
	if w == nil {
		return nil
	}
	return w.Scenes()
}

// sceneContractTarget 从因果链诊断的 path 与 field 解析要修改的单元引用与大纲节（Outline Section）id，供 next 给出
// 可直接运行的 scene contract 命令；解析不出节 id 时返回空串，调用方改用查看大纲。
func sceneContractTarget(dir string, p workspace.Problem) (ref, section string) {
	unitDir := strings.TrimSuffix(p.Path, "/"+outline.FileName)
	ref = work.RefOf(unitDir)
	at, _, ok := strings.Cut(strings.TrimPrefix(p.Field, "sections["), "]")
	if !ok {
		return ref, ""
	}
	n, err := strconv.Atoi(at)
	if err != nil {
		return ref, ""
	}
	// 只读地取出该节的 id：因果链诊断只在大纲通过 Schema 与节 id 检查后产生，这里解析不出时按无节处理。
	w, u, _ := work.Resolve(dir, ref)
	if w == nil || u == nil {
		return ref, ""
	}
	if o := outline.Loadable(dir, w.UnitDir(u)); o != nil && n < len(o.Sections) {
		return ref, o.Sections[n].ID
	}
	return ref, ""
}

// unitExists 返回检查单元引用能否解析的函数，供决策队列检查关联单元；每个作品只解析一次。
func unitExists(dir string) func(ref string) bool {
	works := map[string]*work.Work{}
	return func(ref string) bool {
		workID, unitID, child := strings.Cut(ref, "/")
		w, seen := works[workID]
		if !seen {
			w, _, _ = work.Resolve(dir, workID)
			works[workID] = w
		}
		if w == nil {
			return false
		}
		return !child || slices.ContainsFunc(w.Units(), func(u *work.Unit) bool { return u.Parent != nil && u.ID == unitID })
	}
}

func projectDir(inv *invocation) string {
	if d := inv.values["dir"]; d != "" {
		return d
	}
	return "."
}

// diagnostics 把工作区问题转换为诊断，并按错误码填写下一步命令。
func diagnostics(problems []workspace.Problem, dir string) []Diagnostic {
	var out []Diagnostic
	for _, p := range problems {
		var loc []string
		if p.Line > 0 {
			loc = append(loc, fmt.Sprintf("第 %d 行", p.Line))
		}
		if p.Field != "" {
			loc = append(loc, p.Field)
		}
		msg := p.Path
		if len(loc) > 0 {
			msg = strings.TrimSpace(msg + " " + strings.Join(loc, " "))
		}
		if msg == "" {
			msg = p.Message // 没有位置可写时不加前导的「：」
		} else {
			msg += "：" + p.Message
		}
		if p.Origin != "" {
			// 内置产物没有工作区路径：以出处说明开头，位置放在末尾。
			msg = p.Origin + "：" + p.Message
			if len(loc) > 0 {
				msg += "（" + strings.Join(loc, " ") + "）"
			}
		}
		if key := configKey(p); key != "" && p.Code == schema.CodeSchemaViolation {
			msg += "；可用 wa-cli config set " + key + " <value> 修改"
		}
		out = append(out, Diagnostic{
			Code: p.Code, Severity: p.Severity, Message: msg,
			Path: p.Path, Field: p.Field, Next: next(p, dir),
		})
	}
	return out
}

// next 返回可以直接运行的下一步命令，不含需要替换的占位符。
func next(p workspace.Problem, dir string) []string {
	initCmd := shellCommand("wa-cli", "init", "--dir", dir, "--json")
	validate := shellCommand("wa-cli", "validate", workspace.ConfigPath, "--dir", dir, "--json")
	validateAll := shellCommand("wa-cli", "validate", "--dir", dir, "--json")
	if schema.KindFor(p.Path) != nil {
		validate = shellCommand("wa-cli", "validate", p.Path, "--dir", dir, "--json")
	}
	if p.Origin != "" {
		// 内置产物的问题只会在合并时出现，用整体校验复查。
		validate = validateAll
	}
	status := shellCommand("wa-cli", "status", "--dir", dir, "--json")
	if n := feedbackNext(p, dir); n != nil {
		return n
	}
	inSources := strings.HasPrefix(p.Path, workspace.SourcesPath+"/")
	if p.Path == decision.Path && (p.Code == schema.CodeNotFound || p.Code == workspace.CodeReadFailed) {
		// 决策队列不可读：init 不会修复它，用整体校验复查。
		return []string{validateAll, shellCommand("wa-cli", "decisions", "--dir", dir, "--json")}
	}
	switch p.Code {
	case schema.CodeNotFound:
		if k := schema.KindFor(p.Path); k != nil && k.Name == "work" {
			// 作品目录中缺少 work.yaml：init 不会补齐它，作品只能用 wa-cli new 创建。
			return []string{"wa-cli help new --json", validateAll}
		}
		if k := schema.KindFor(p.Path); k != nil && (k.Name == "brief" || k.Name == "outline" || k.Name == "review" || k.Name == "acceptance" || k.Name == "marketing") {
			// brief.yaml 途经符号链接或不是普通文件：需要作者把它换成普通文件，init 不会修复它。
			return []string{status, validateAll}
		}
		if inSources {
			// 素材目录不完整：init 不会补齐它，素材只能用 wa-cli source add 登记。
			return []string{"wa-cli help source add --json", validateAll}
		}
		return []string{initCmd}
	case source.CodeModified:
		return []string{validateAll}
	case decision.CodeBlocking, brief.CodeMissingField, brief.CodeUnsourcedMessage, brief.CodeUnconfirmedMessage,
		brief.CodeUnsourcedPremise, brief.CodeUnconfirmedPremise, brief.CodeInvalid,
		outline.CodeInvalid, outline.CodeUncoveredMessage, outline.CodeMissingReason, marketing.CodeInvalid, marketing.CodeMissingField, marketing.CodeTooManyHeadlines, marketing.CodeHeadlineUnasked, marketing.CodeHeadlineStale:
		return blockerNext(dir, p.Code, p.Path)
	case outline.CodeMissingPart, outline.CodeMethodOrder, outline.CodeUnknownRole:
		// 写作法缺口：查看大纲与写作法的章节或节拍，再用 outline set --role 修复。参数引起的（不填 path）由命令改写 next。
		if p.Path != "" {
			return append(blockerNext(dir, p.Code, p.Path), shellCommand("wa-cli", "method", "list", "--dir", dir, "--json"))
		}
	case outline.CodeBeatPosition:
		return []string{shellCommand("wa-cli", "lint", work.RefOf(strings.TrimSuffix(p.Path, "/"+draft.FileName)), "--dir", dir, "--json")}
	case brief.CodeUnknownMethod:
		if p.Path != "" {
			return []string{shellCommand("wa-cli", "method", "list", "--dir", dir, "--json"), validate}
		}
	case outline.CodeUnknownMessage:
		// 引用写作简报中的核心观点，涉及两个产物，用整体校验复查；参数引起的（不填 path）由命令改写 next。
		if i := strings.LastIndex(p.Path, "/"); i > 0 {
			return []string{shellCommand("wa-cli", "brief", "show", work.RefOf(p.Path[:i]), "--dir", dir, "--json"), validateAll}
		}
		return []string{validateAll}
	case outline.CodeUnknownTarget, outline.CodeTargetOutOfRange:
		// targets 引用按作品文体合并的指标，涉及配置层与产物；查看大纲与用法后修复（scene-contract-targets）。
		if p.Path != "" {
			return []string{shellCommand("wa-cli", "outline", "show", work.RefOf(strings.TrimSuffix(p.Path, "/"+outline.FileName)), "--dir", dir, "--json"),
				"wa-cli help outline set --json"}
		}
	case outline.CodeUnknownEvent, outline.CodeEventNotHappened, outline.CodeDuplicateEvent:
		// 因果链（Causality Chain）引用其他场景的大纲，涉及多个产物；用 scene contract 修复出错的场景契约（scene-causality）。
		if p.Path != "" {
			if ref, sec := sceneContractTarget(dir, p); sec != "" {
				return []string{shellCommand("wa-cli", "scene", "contract", ref, sec, "--dir", dir, "--json"), "wa-cli help scene contract --json"}
			}
			return []string{shellCommand("wa-cli", "outline", "show", work.RefOf(strings.TrimSuffix(p.Path, "/"+outline.FileName)), "--dir", dir, "--json"),
				"wa-cli help scene contract --json"}
		}
		return []string{validateAll}
	case outline.CodeUnknownSource:
		return []string{shellCommand("wa-cli", "source", "list", "--dir", dir, "--json"), validateAll}
	case brief.CodeUnknownSource:
		// 素材引用涉及多个产物，用整体校验复查。
		return []string{shellCommand("wa-cli", "source", "list", "--dir", dir, "--json"), validateAll}
	case brief.CodeUnknownDecision, brief.CodeUnrelatedDecision:
		return []string{shellCommand("wa-cli", "decisions", "--all", "--dir", dir, "--json"), validateAll}
	case decision.CodeInvalidQueue:
		// wa-cli decisions 会报出决策队列的具体问题，整体校验同样能复查。
		return []string{shellCommand("wa-cli", "decisions", "--dir", dir, "--json"), validateAll}
	case decision.CodeUnknownUnit:
		// 关联单元涉及多个产物，用整体校验复查。
		return []string{validateAll}
	case source.CodeExists:
		return []string{shellCommand("wa-cli", "source", "list", "--dir", dir, "--json")}
	case draft.CodeNotFound:
		return []string{"wa-cli help draft write --json"}
	case draft.CodeHumanEdited, draft.CodeBaseMismatch:
		return []string{shellCommand("wa-cli", "diff", work.RefOf(strings.TrimSuffix(p.Path, "/"+draft.FileName)), "--dir", dir, "--json")}
	case draft.CodeUnknownSource:
		// 素材引用涉及多个产物，用整体校验复查。
		return []string{shellCommand("wa-cli", "source", "list", "--dir", dir, "--json"), validateAll}
	case canon.CodeUnknownSource:
		// 素材引用涉及多个产物，用整体校验复查（canon-validation）。
		return []string{shellCommand("wa-cli", "source", "list", "--dir", dir, "--json"), validateAll}
	case canon.CodeUnknownCharacter:
		// 人物引用涉及同一设定集的多个条目，用整体校验复查（canon-validation）。
		if strings.HasSuffix(p.Path, "/"+canon.FileName) {
			return []string{shellCommand("wa-cli", "canon", "list", work.RefOf(strings.TrimSuffix(p.Path, "/"+canon.FileName)), "--dir", dir, "--json"), validateAll}
		}
		return []string{validateAll}
	case canon.CodeUnknownDecision:
		// 决策引用涉及多个产物，用整体校验复查（canon-validation）。
		return []string{shellCommand("wa-cli", "decisions", "--all", "--dir", dir, "--json"), validateAll}
	case plan.CodeNotPlanning, plan.CodeStepOrder, plan.CodeMethodChanged, plan.CodeUnknownLink, plan.CodeMissingLinks:
		// 规划的问题涉及写作法选用与产物链接：查看规划并整体校验复查（plan-artifact）。
		if strings.HasSuffix(p.Path, "/"+plan.FileName) {
			return []string{shellCommand("wa-cli", "plan", "show", work.RefOf(strings.TrimSuffix(p.Path, "/"+plan.FileName)), "--dir", dir, "--json"), validate}
		}
		return []string{validateAll}
	case plan.CodeUnknownSource:
		return []string{shellCommand("wa-cli", "source", "list", "--dir", dir, "--json"), validateAll}
	case catalog.CodeDuplicateID:
		if strings.HasSuffix(p.Path, "/"+draft.ClaimsName) || strings.HasSuffix(p.Path, "/"+brief.FileName) || strings.HasSuffix(p.Path, "/"+outline.FileName) {
			return []string{validateAll}
		}
	case workspace.CodeNotFound, workspace.CodeMissingDirectory:
		return []string{initCmd}
	case work.CodeNotFound, work.CodeUnitNotFound, work.CodeExists, work.CodeAlreadyDone, work.CodeDerivedStage:
		return []string{status}
	case work.CodeNotSibling, work.CodeMoveRoot, work.CodeMoveUnderSelf:
		// 由本次参数引起的问题：unit add 与 unit move 会改写为各自的用法；这里给出两个用法兜底。
		return []string{"wa-cli help unit add --json", "wa-cli help unit move --json"}
	case work.CodeMissingArtifact:
		return []string{shellCommand("wa-cli", "next", work.RefOf(p.Path), "--dir", dir, "--json")}
	case review.CodeAcceptanceStale, review.CodeAcceptanceInvalid, review.CodeAcceptanceWeakerGate:
		return blockerNext(dir, p.Code, p.Path)
	case review.CodeDuplicateVerdict:
		return []string{validate}
	case schema.CodeSchemaViolation:
		var out []string
		switch key := configKey(p); key {
		case "":
		case "gate_level":
			for _, level := range workspace.GateLevels {
				out = append(out, shellCommand("wa-cli", "config", "set", key, level, "--dir", dir, "--json"))
			}
		default:
			out = append(out, "wa-cli help config set --json")
		}
		return append(out, validate)
	case schema.CodeUnknownKind:
		return []string{"wa-cli help validate --json"}
	case resolve.CodeUnknownGenre:
		var out []string
		for _, g := range catalog.BuiltinGenres() {
			out = append(out, shellCommand("wa-cli", "config", "set", "default_genre", g, "--dir", dir, "--json"))
		}
		return append(out, shellCommand("wa-cli", "config", "set", "default_genre", "", "--dir", dir, "--json"))
	case resolve.CodeUnknownPack:
		if strings.HasPrefix(p.Path, series.Dir+"/") {
			// 系列文件中的规范包：用 series set 修改，pack list 查看可用的规范包。
			return []string{"wa-cli help series set --json", shellCommand("wa-cli", "pack", "list", "--dir", dir, "--json"), validateAll}
		}
		return []string{"wa-cli help config set --json", shellCommand("wa-cli", "pack", "list", "--dir", dir, "--json"), validateAll}
	case packinstall.CodeLockMismatch, packinstall.CodeLockMissingFile:
		return packNext(p, dir)
	case packinstall.CodeExists, packinstall.CodeNotInstalled:
		return []string{shellCommand("wa-cli", "pack", "list", "--dir", dir, "--json")}
	case resolve.CodeMissingRuleType, resolve.CodeRuleTypeConflict, resolve.CodeUnknownWaiverTarget:
		// 合并问题涉及多个配置层，只有整体校验能重新检出。
		return []string{validateAll}
	case workspace.CodeUnknownConfigKey, workspace.CodeConfigKept, workspace.CodeAnchoredValue:
		return []string{"wa-cli help config set --json"}
	case workspace.CodeWriteFailed, workspace.CodeReadFailed:
		if strings.HasPrefix(p.Path, workspace.WorksPath+"/") || inSources {
			return []string{status}
		}
		if p.Path != workspace.ConfigPath {
			return []string{initCmd}
		}
	}
	return []string{validate}
}

// configKey 返回配置问题所在的可设置配置项；不是配置项的问题返回空字符串。
func configKey(p workspace.Problem) string {
	if p.Path != workspace.ConfigPath {
		return ""
	}
	key, _, _ := strings.Cut(p.Field, ".")
	key, _, _ = strings.Cut(key, "[")
	for _, k := range workspace.SettableKeys {
		if k == key {
			return key
		}
	}
	return ""
}

// shellCommand 把参数拼成一条可以直接粘贴到 shell 中运行的命令，必要时加单引号。
func shellCommand(args ...string) string {
	quoted := make([]string, len(args))
	for i, a := range args {
		if a != "" && !strings.ContainsAny(a, " \t\n'\"\\$`!*?[]{}()<>|&;#~") {
			quoted[i] = a
		} else {
			quoted[i] = "'" + strings.ReplaceAll(a, "'", `'\''`) + "'"
		}
	}
	return strings.Join(quoted, " ")
}

func hasError(ds []Diagnostic) bool {
	for _, d := range ds {
		if d.Severity == severityError {
			return true
		}
	}
	return false
}

// finish 写出结果：JSON 模式写输出信封，文本模式用 text 写标准输出、把诊断写到标准错误。
// 存在 error 级诊断时返回退出码 1。
func finish(inv *invocation, v enveloped, text func(io.Writer) error) int {
	r := v.envelope()
	var err error
	if inv.json() {
		err = writeJSON(inv.stdout, v)
	} else if err = text(inv.stdout); err == nil {
		err = writeDiagnostics(inv.stderr, r.Diagnostics)
	}
	if err != nil {
		return writeFailed(inv.stderr, err)
	}
	if hasError(r.Diagnostics) {
		return exitError
	}
	return exitOK
}

// writeDiagnostics 以人读文本写出诊断及其下一步命令。
func writeDiagnostics(w io.Writer, ds []Diagnostic) error {
	var b strings.Builder
	for _, d := range ds {
		label := "错误"
		if d.Severity == severityWarning {
			label = "警告"
		}
		fmt.Fprintf(&b, "%s [%s] %s\n", label, d.Code, d.Message)
		for _, n := range d.Next {
			fmt.Fprintf(&b, "  下一步：%s\n", n)
		}
	}
	_, err := io.WriteString(w, b.String())
	return err
}
