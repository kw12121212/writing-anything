package cli

import (
	"fmt"
	"io"
	"slices"
	"strings"
	"unicode/utf8"

	"github.com/kw12121212/writing-anything/internal/series"
	"github.com/kw12121212/writing-anything/internal/work"
	"github.com/kw12121212/writing-anything/internal/workspace"
)

const seriesHelp = "系列（Series）：id、path、title、works、packs、sources 与 facts（id、text、sources）；失败时为 null"

var seriesSetCommand = &command{
	name:    "series set",
	summary: "创建或修改系列（Series）：成员作品继承系列的规范包（Pack）、素材（Source）与事实（Fact）",
	args:    []argSpec{{"series", "系列 id；系列保存在 " + series.Dir + "/<series>.yaml", true}},
	flags: []flagSpec{dirFlag,
		{"title", flagString, "系列标题；创建时省略则为系列 id"},
		{"packs", flagString, "系列的规范包 id，逗号分隔，整体替换；空字符串表示清空"},
		{"sources", flagString, "系列的素材 id，逗号分隔，整体替换；空字符串表示清空"}},
	outputKeys: []outputKey{{"series", seriesHelp}},
	exitCodes:  standardExitCodes,
	run:        runSeriesSet,
}

var seriesAddCommand = &command{
	name:       "series add",
	summary:    "把作品（Work）追加到系列的成员作品末尾；一个作品至多属于一个系列",
	args:       []argSpec{{"series", "系列 id", true}, {"work", "作品 id", true}},
	flags:      []flagSpec{dirFlag},
	outputKeys: []outputKey{{"series", seriesHelp}},
	exitCodes:  standardExitCodes,
	run:        runSeriesAdd,
}

var seriesFactCommand = &command{
	name:    "series fact",
	summary: "新增或替换系列的一条事实（Fact）：成员作品的上下文包会带上它",
	args:    []argSpec{{"series", "系列 id", true}, {"fact", "事实 id", true}},
	flags: []flagSpec{dirFlag,
		{"text", flagString, "事实的内容"},
		{"sources", flagString, "支撑这条事实的素材 id，逗号分隔"}},
	outputKeys: []outputKey{{"series", seriesHelp}},
	exitCodes:  standardExitCodes,
	run:        runSeriesFact,
}

var seriesShowCommand = &command{
	name:    "series show",
	summary: "查看系列的内容与每个成员作品的已验收摘要（Summary）",
	args:    []argSpec{{"series", "系列 id", true}},
	flags:   []flagSpec{dirFlag},
	outputKeys: []outputKey{
		{"series", seriesHelp},
		{"summaries", "按成员作品顺序的摘要：work、text（根单元没有摘要时为 null）与 stale"},
	},
	exitCodes: standardExitCodes,
	run:       runSeriesShow,
}

var summarySetCommand = &command{
	name:    "summary set",
	summary: "为已验收的单元（Unit）写入摘要（Summary）：校验非空、字数在文体规定的范围内，并记录正文的 SHA-256；正文之后被修改时摘要过期",
	args:    []argSpec{{"unit", unitRefHelp, true}},
	flags: []flagSpec{dirFlag,
		{"text", flagString, "摘要内容（由 Agent 撰写）"},
		{"file", flagString, "从文件读取摘要，相对 --dir 或为绝对路径；省略 --text 与 --file 时读取标准输入"}},
	outputKeys: []outputKey{{"summary", summaryHelp}},
	exitCodes:  standardExitCodes,
	run:        runSummarySet,
}

var summaryShowCommand = &command{
	name:       "summary show",
	summary:    "查看单元的摘要（Summary）与是否过期",
	args:       []argSpec{{"unit", unitRefHelp, true}},
	flags:      []flagSpec{dirFlag},
	outputKeys: []outputKey{{"summary", summaryHelp + "；单元没有摘要时为 null"}},
	exitCodes:  standardExitCodes,
	run:        runSummaryShow,
}

const summaryHelp = "摘要：unit、path、text、chars、draftSha256 与 stale"

type seriesFact struct {
	ID      string   `json:"id"`
	Text    string   `json:"text"`
	Sources []string `json:"sources"`
}

type seriesView struct {
	ID      string       `json:"id"`
	Path    string       `json:"path"`
	Title   string       `json:"title"`
	Works   []string     `json:"works"`
	Packs   []string     `json:"packs"`
	Sources []string     `json:"sources"`
	Facts   []seriesFact `json:"facts"`
}

type seriesResult struct {
	Result
	Series *seriesView `json:"series"`
}

type summaryRef struct {
	Work  string  `json:"work"`
	Text  *string `json:"text"`
	Stale bool    `json:"stale"`
}

type seriesShowResult struct {
	seriesResult
	Summaries []summaryRef `json:"summaries"`
}

type summaryView struct {
	Unit        string `json:"unit"`
	Path        string `json:"path"`
	Text        string `json:"text"`
	Chars       int    `json:"chars"`
	DraftSHA256 string `json:"draftSha256"`
	Stale       bool   `json:"stale"`
}

type summaryResult struct {
	Result
	Summary *summaryView `json:"summary"`
}

func factsView(fs []series.Fact) []seriesFact {
	out := []seriesFact{}
	for _, f := range fs {
		out = append(out, seriesFact{ID: f.ID, Text: f.Text, Sources: nonNilStrings(f.Sources)})
	}
	return out
}

func viewSeries(s *series.Series) *seriesView {
	return &seriesView{ID: s.ID, Path: s.Path, Title: s.Title, Works: nonNilStrings(s.Works), Packs: nonNilStrings(s.Packs),
		Sources: nonNilStrings(s.Sources), Facts: factsView(s.Facts)}
}

// finishSeries 写出系列命令的结果：参数与引用问题（不填 path）的下一步为查看用法与系列。
func finishSeries(inv *invocation, name, dir string, res *seriesResult, problems []workspace.Problem, out enveloped, text func(io.Writer) error) int {
	res.Diagnostics = diagnostics(problems, dir)
	for i, d := range res.Diagnostics {
		switch d.Code {
		case series.CodeUnknownSource:
			res.Diagnostics[i].Next = []string{shellCommand("wa-cli", "source", "list", "--dir", dir, "--json"), "wa-cli help " + name + " --json"}
		case series.CodeAlreadyMember, series.CodeNotFound, codeInvalidValue:
			res.Diagnostics[i].Next = []string{"wa-cli help " + name + " --json"}
		default:
			if d.Path == "" && d.Code == "config.unknown-pack" {
				res.Diagnostics[i].Next = []string{shellCommand("wa-cli", "pack", "list", "--dir", dir, "--json"), "wa-cli help " + name + " --json"}
			}
		}
	}
	return finish(inv, out, text)
}

func seriesText(res *seriesResult) func(io.Writer) error {
	return func(w io.Writer) error {
		s := res.Series
		if s == nil {
			return nil
		}
		_, err := fmt.Fprintf(w, "系列（Series）%s（%s）：作品 %s；规范包 %s；素材 %s；%d 条事实\n", s.ID, s.Title,
			listOrNone(s.Works), listOrNone(s.Packs), listOrNone(s.Sources), len(s.Facts))
		return err
	}
}

func listOrNone(xs []string) string {
	if len(xs) == 0 {
		return "无"
	}
	return strings.Join(xs, "、")
}

func runSeriesSet(inv *invocation) int {
	dir, id := projectDir(inv), inv.args[0]
	res := &seriesResult{}
	problems := workspace.Require(dir)
	var e series.Edit
	if problems == nil {
		problems = checkIDs("series", id)
		if t, ok := inv.values["title"]; ok {
			if strings.TrimSpace(t) == "" || !utf8.ValidString(t) {
				problems = append(problems, invalidValue("title", t, "不能为空白，且必须是 UTF-8 文本"))
			}
			e.Title = &t
		}
		for _, f := range []struct {
			flag string
			dst  **[]string
		}{{"packs", &e.Packs}, {"sources", &e.Sources}} {
			if v, ok := inv.values[f.flag]; ok {
				ids := nonNilStrings(splitIDs(v)) // 空字符串表示清空
				problems = append(problems, checkIDs(f.flag, ids...)...)
				problems = append(problems, checkUnique(f.flag, ids)...)
				*f.dst = &ids
			}
		}
	}
	if problems == nil {
		var s *series.Series
		if s, res.ChangedPaths, problems = series.Set(dir, id, e); s != nil {
			res.Series = viewSeries(s)
		}
	}
	return finishSeries(inv, "series set", dir, res, problems, res, seriesText(res))
}

func runSeriesAdd(inv *invocation) int {
	dir, id, workID := projectDir(inv), inv.args[0], inv.args[1]
	res := &seriesResult{}
	problems := workspace.Require(dir)
	if problems == nil {
		problems = append(checkIDs("series", id), checkIDs("work", workID)...)
	}
	if problems == nil {
		var s *series.Series
		if s, res.ChangedPaths, problems = series.Add(dir, id, workID); s != nil {
			res.Series = viewSeries(s)
		}
	}
	return finishSeries(inv, "series add", dir, res, problems, res, seriesText(res))
}

func runSeriesFact(inv *invocation) int {
	dir, id, fact := projectDir(inv), inv.args[0], inv.args[1]
	res := &seriesResult{}
	problems := workspace.Require(dir)
	sources := splitIDs(inv.values["sources"])
	if problems == nil {
		problems = append(checkIDs("series", id), checkIDs("fact", fact)...)
		problems = append(problems, checkIDs("sources", sources...)...)
		problems = append(problems, checkUnique("sources", sources)...)
		if t := inv.values["text"]; strings.TrimSpace(t) == "" || !utf8.ValidString(t) {
			problems = append(problems, invalidValue("text", t, "必须给出事实的内容，且必须是 UTF-8 文本"))
		}
	}
	if problems == nil {
		var s *series.Series
		if s, res.ChangedPaths, problems = series.SetFact(dir, id, series.Fact{ID: fact, Text: strings.TrimSpace(inv.values["text"]), Sources: sources}); s != nil {
			res.Series = viewSeries(s)
		}
	}
	return finishSeries(inv, "series fact", dir, res, problems, res, seriesText(res))
}

func runSeriesShow(inv *invocation) int {
	dir, id := projectDir(inv), inv.args[0]
	res := &seriesShowResult{Summaries: []summaryRef{}}
	problems := workspace.Require(dir)
	if problems == nil {
		problems = checkIDs("series", id)
	}
	if problems == nil {
		var s *series.Series
		if s, problems = series.Load(dir, series.Path(id)); s == nil && problems == nil {
			problems = []workspace.Problem{{Code: series.CodeNotFound, Severity: workspace.SeverityError, Message: "没有系列 " + id}}
		}
		if s != nil {
			res.Series = viewSeries(s)
			for _, wid := range s.Works {
				ref := summaryRef{Work: wid}
				if w, u, ps := work.Resolve(dir, wid); len(ps) == 0 {
					if sum, ps := series.LoadSummary(dir, w, u); sum != nil {
						ref.Text, ref.Stale = &sum.Text, sum.Stale
					} else {
						problems = append(problems, ps...)
					}
				}
				res.Summaries = append(res.Summaries, ref)
			}
		}
	}
	return finishSeries(inv, "series show", dir, &res.seriesResult, problems, res, func(w io.Writer) error {
		if err := seriesText(&res.seriesResult)(w); err != nil || res.Series == nil {
			return err
		}
		var b strings.Builder
		for _, s := range res.Summaries {
			switch {
			case s.Text == nil:
				fmt.Fprintf(&b, "- %s：还没有摘要\n", s.Work)
			case s.Stale:
				fmt.Fprintf(&b, "- %s（已过期）：%s\n", s.Work, *s.Text)
			default:
				fmt.Fprintf(&b, "- %s：%s\n", s.Work, *s.Text)
			}
		}
		_, err := io.WriteString(w, b.String())
		return err
	})
}

func viewSummary(ref string, s *series.Summary) *summaryView {
	return &summaryView{Unit: ref, Path: s.Path, Text: s.Text, Chars: s.Chars, DraftSHA256: s.DraftSHA256, Stale: s.Stale}
}

func summaryText(res *summaryResult) func(io.Writer) error {
	return func(w io.Writer) error {
		s := res.Summary
		if s == nil {
			return nil
		}
		note := ""
		if s.Stale {
			note = "（已过期：正文在摘要之后被修改）"
		}
		_, err := fmt.Fprintf(w, "单元（Unit）%s 的摘要（Summary）%s，%d 字：\n%s\n", s.Unit, note, s.Chars, s.Text)
		return err
	}
}

func runSummarySet(inv *invocation) int {
	dir, ref := projectDir(inv), inv.args[0]
	res := &summaryResult{}
	problems := workspace.Require(dir)
	var text string
	_, hasFile := inv.values["file"]
	if problems == nil {
		if t, ok := inv.values["text"]; ok && hasFile {
			problems = []workspace.Problem{invalidValue("text", t, "--text 与 --file 只能给出一个")}
		} else if ok {
			text = t
		} else {
			var data []byte
			data, _, problems = readInput(inv, dir)
			text = string(data)
		}
	}
	if problems == nil && (strings.TrimSpace(text) == "" || !utf8.ValidString(text)) {
		problems = []workspace.Problem{invalidValue("text", text, "摘要不能为空白，且必须是 UTF-8 文本")}
	}
	if problems == nil {
		var w *work.Work
		var u *work.Unit
		if w, u, problems = work.Resolve(dir, ref); u != nil {
			var s *series.Summary
			if s, res.ChangedPaths, problems = series.SetSummary(dir, w, u, strings.TrimSpace(text), w.Genre.SummaryLength, now()); s != nil {
				res.Summary = viewSummary(u.Ref, s)
			}
		}
	}
	res.Diagnostics = diagnostics(problems, dir)
	for i, d := range res.Diagnostics {
		switch d.Code {
		case series.CodeNotAccepted:
			res.Diagnostics[i].Next = []string{shellCommand("wa-cli", "review", "show", ref, "--dir", dir, "--json")}
		case series.CodeLength, codeInvalidValue, codeInputReadFailed:
			res.Diagnostics[i].Next = []string{"wa-cli help summary set --json"}
		}
	}
	return finish(inv, res, summaryText(res))
}

func runSummaryShow(inv *invocation) int {
	dir, ref := projectDir(inv), inv.args[0]
	res := &summaryResult{}
	problems := workspace.Require(dir)
	if problems == nil {
		var w *work.Work
		var u *work.Unit
		if w, u, problems = work.Resolve(dir, ref); u != nil {
			var s *series.Summary
			if s, problems = series.LoadSummary(dir, w, u); s != nil {
				res.Summary = viewSummary(u.Ref, s)
			}
		}
	}
	res.Diagnostics = diagnostics(problems, dir)
	return finish(inv, res, summaryText(res))
}

// checkUnique 报告参数给出的 id 中重复的项；这类问题在修改产物之前就能检查（cli-contract/invalid-value-code）。
func checkUnique(flag string, ids []string) []workspace.Problem {
	for i, id := range ids {
		if slices.Contains(ids[:i], id) {
			return []workspace.Problem{invalidValue(flag, strings.Join(ids, ","), "id "+id+" 重复")}
		}
	}
	return nil
}
