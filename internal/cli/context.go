package cli

import (
	"io"
	"strings"

	"github.com/kw12121212/writing-anything/internal/brief"
	"github.com/kw12121212/writing-anything/internal/ctxpack"
	"github.com/kw12121212/writing-anything/internal/resolve"
	"github.com/kw12121212/writing-anything/internal/work"
	"github.com/kw12121212/writing-anything/internal/workspace"
)

var contextCommand = &command{
	name:       "context",
	summary:    "输出单元（Unit）的有界上下文包（Context Pack）：不加 --json 时为 Markdown；超出上下文预算（Context Budget）时按优先级裁剪",
	args:       []argSpec{{"unit", unitRefHelp, true}},
	flags:      []flagSpec{dirFlag},
	outputKeys: []outputKey{{"context", "上下文包：unit、type、stage、genre、budget（value、source）、used、brief、outline、section、characters（场景级：出场人物在设定集中的当前设定 id、name、text、protection；非 scene 单元为空数组）、knowledge（场景级：视角人物已知的设定条目 entry、name、text、event、scene；非 scene 单元为空数组）、readerOnly（场景级：读者已知、视角人物未知的设定条目，字段同 knowledge；非 scene 单元为空数组）、priorScenes（前序场景的已验收摘要 unit、text、stale）、reveal（揭示边界 section、text）、threads（场景级：与本场景契约相关的未回收伏笔 id、text、planted_in、planned_recover、overdue；非 scene 单元或作品没有伏笔台账时为空数组）、feedback、decisions、norms、sources、neighbors、series（所属系列的 id、title、facts 与前序作品的 summaries：work、text、stale；不属于系列时为 null）与 trimmed；失败时为 null；scene 单元另含 states（出场人物状态快照）、priorEndings（前序场景结尾）与 summaries（分层摘要）"}},
	exitCodes:  standardExitCodes,
	run:        runContext,
}

type ctxMessage struct {
	ID   string `json:"id"`
	Text string `json:"text"`
}

type ctxBrief struct {
	Unit        string        `json:"unit"`
	Audience    *string       `json:"audience"`
	Purpose     *string       `json:"purpose"`
	Length      *brief.Length `json:"length"`
	Avoid       []string      `json:"avoid"`
	KeyMessages []ctxMessage  `json:"keyMessages"`
}

type ctxOutlineSection struct {
	ID     string   `json:"id"`
	Title  string   `json:"title"`
	Covers []string `json:"covers"`
	Reason *string  `json:"reason"`
}

type ctxOutline struct {
	Sections []ctxOutlineSection `json:"sections"`
}

type ctxSection struct {
	ID          string       `json:"id"`
	Title       string       `json:"title"`
	Reason      *string      `json:"reason"`
	Uses        []string     `json:"uses"`
	KeyMessages []ctxMessage `json:"keyMessages"`
}

type ctxCharacter struct {
	ID         string `json:"id"`
	Name       string `json:"name"`
	Text       string `json:"text"`
	Protection string `json:"protection"`
}

type ctxKnowledge struct {
	Entry string  `json:"entry"`
	Name  *string `json:"name"`
	Text  string  `json:"text"`
	Event string  `json:"event"`
	Scene string  `json:"scene"`
}

type ctxSceneSummary struct {
	Unit  string `json:"unit"`
	Text  string `json:"text"`
	Stale bool   `json:"stale"`
}

type ctxReveal struct {
	Section string `json:"section"`
	Text    string `json:"text"`
}

type ctxThread struct {
	ID             string `json:"id"`
	Text           string `json:"text"`
	PlantedIn      string `json:"planted_in"`
	PlannedRecover string `json:"planned_recover"`
	Overdue        bool   `json:"overdue"`
}

type ctxOption struct {
	ID   string `json:"id"`
	Text string `json:"text"`
}

type ctxDecision struct {
	ID       string      `json:"id"`
	Question string      `json:"question"`
	Options  []ctxOption `json:"options"`
	Blocking bool        `json:"blocking"`
}

type ctxRule struct {
	ID       string         `json:"id"`
	Type     string         `json:"type"`
	Severity string         `json:"severity"`
	Params   map[string]any `json:"params"`
}

type ctxTerm struct {
	Term    string   `json:"term"`
	English *string  `json:"english"`
	Avoid   []string `json:"avoid"`
}

type ctxBannedTerm struct {
	Term        string  `json:"term"`
	Replacement *string `json:"replacement"`
}

type ctxNorms struct {
	Rules       []ctxRule       `json:"rules"`
	Glossary    []ctxTerm       `json:"glossary"`
	BannedTerms []ctxBannedTerm `json:"bannedTerms"`
	Style       []string        `json:"style"`
}

type ctxSource struct {
	ID        string  `json:"id"`
	Type      string  `json:"type"`
	Title     *string `json:"title"`
	Text      string  `json:"text"`
	Truncated bool    `json:"truncated"`
}

type ctxNeighbor struct {
	Unit     string  `json:"unit"`
	Position string  `json:"position"`
	Title    *string `json:"title"`
	Excerpt  *string `json:"excerpt"`
}

type ctxTrim struct {
	Part      string  `json:"part"`
	ID        *string `json:"id"`
	Chars     int     `json:"chars"`
	KeptChars int     `json:"keptChars"`
}

type ctxView struct {
	Unit         string              `json:"unit"`
	Type         string              `json:"type"`
	Stage        string              `json:"stage"`
	Genre        string              `json:"genre"`
	Budget       resolve.Value       `json:"budget"`
	Used         int                 `json:"used"`
	Brief        *ctxBrief           `json:"brief"`
	Outline      *ctxOutline         `json:"outline"`
	Section      *ctxSection         `json:"section"`
	Characters   []ctxCharacter      `json:"characters"`
	States       []ctxState          `json:"states"`
	PriorEndings []ctxPriorEnding    `json:"priorEndings"`
	Summaries    []ctxLayeredSummary `json:"summaries"`
	Knowledge    []ctxKnowledge      `json:"knowledge"`
	ReaderOnly   []ctxKnowledge      `json:"readerOnly"`
	PriorScenes  []ctxSceneSummary   `json:"priorScenes"`
	Reveal       []ctxReveal         `json:"reveal"`
	Threads      []ctxThread         `json:"threads"`
	Feedback     []feedbackView      `json:"feedback"`
	Decisions    []ctxDecision       `json:"decisions"`
	Norms        ctxNorms            `json:"norms"`
	Sources      []ctxSource         `json:"sources"`
	Neighbors    []ctxNeighbor       `json:"neighbors"`
	Series       *ctxSeries          `json:"series"`
	Trimmed      []ctxTrim           `json:"trimmed"`
}

type ctxSeries struct {
	ID        string          `json:"id"`
	Title     string          `json:"title"`
	Facts     []seriesFact    `json:"facts"`
	Summaries []ctxSummaryRef `json:"summaries"`
}

type ctxSummaryRef struct {
	Work  string `json:"work"`
	Text  string `json:"text"`
	Stale bool   `json:"stale"`
}

type ctxState struct {
	Entry    string  `json:"entry"`
	Name     *string `json:"name"`
	State    string  `json:"state"`
	Proposal string  `json:"proposal"`
	Scene    string  `json:"scene"`
}

type ctxPriorEnding struct {
	Unit    string `json:"unit"`
	Excerpt string `json:"excerpt"`
}

type ctxLayeredSummary struct {
	Unit  string `json:"unit"`
	Level string `json:"level"`
	Text  string `json:"text"`
	Stale bool   `json:"stale"`
}

type contextResult struct {
	Result
	Context *ctxView `json:"context"`
	pack    *ctxpack.Pack
}

func messagesView(ms []ctxpack.Message) []ctxMessage {
	out := []ctxMessage{}
	for _, m := range ms {
		out = append(out, ctxMessage{m.ID, m.Text})
	}
	return out
}

// viewContext 把上下文包转换为 --json 输出；数组永不为 null（cli-contract/shared-envelope）。
func viewContext(p *ctxpack.Pack) *ctxView {
	v := &ctxView{Unit: p.Unit, Type: p.Type, Stage: p.Stage, Genre: p.Genre, Budget: p.Budget, Used: p.Used,
		Characters: []ctxCharacter{}, States: []ctxState{}, PriorEndings: []ctxPriorEnding{}, Summaries: []ctxLayeredSummary{}, Knowledge: []ctxKnowledge{}, ReaderOnly: []ctxKnowledge{}, PriorScenes: []ctxSceneSummary{}, Reveal: []ctxReveal{}, Threads: []ctxThread{},
		Feedback: []feedbackView{}, Decisions: []ctxDecision{}, Sources: []ctxSource{}, Neighbors: []ctxNeighbor{}, Trimmed: []ctxTrim{},
		Norms: ctxNorms{Rules: []ctxRule{}, Glossary: []ctxTerm{}, BannedTerms: []ctxBannedTerm{}, Style: nonNilStrings(p.Norms.Style)}}
	if b := p.Brief; b != nil {
		v.Brief = &ctxBrief{Unit: b.Unit, Audience: optional(b.Audience), Purpose: optional(b.Purpose), Length: b.Length,
			Avoid: nonNilStrings(b.Avoid), KeyMessages: messagesView(b.KeyMessages)}
	}
	if o := p.Outline; o != nil {
		v.Outline = &ctxOutline{Sections: []ctxOutlineSection{}}
		for _, s := range o.Sections {
			v.Outline.Sections = append(v.Outline.Sections, ctxOutlineSection{ID: s.ID, Title: s.Title, Covers: nonNilStrings(s.Covers), Reason: optional(s.Reason)})
		}
	}
	if s := p.Section; s != nil {
		v.Section = &ctxSection{ID: s.ID, Title: s.Title, Reason: optional(s.Reason), Uses: nonNilStrings(s.Uses), KeyMessages: messagesView(s.KeyMessages)}
	}
	for _, c := range p.Characters {
		v.Characters = append(v.Characters, ctxCharacter{ID: c.ID, Name: c.Name, Text: c.Text, Protection: c.Protection})
	}
	for _, s := range p.States {
		v.States = append(v.States, ctxState{Entry: s.Entry, Name: s.Name, State: s.State, Proposal: s.Proposal, Scene: s.Scene})
	}
	for _, e := range p.PriorEndings {
		v.PriorEndings = append(v.PriorEndings, ctxPriorEnding{Unit: e.Unit, Excerpt: e.Excerpt})
	}
	for _, s := range p.Summaries {
		v.Summaries = append(v.Summaries, ctxLayeredSummary{Unit: s.Unit, Level: s.Level, Text: s.Text, Stale: s.Stale})
	}
	for _, k := range p.Knowledge {
		v.Knowledge = append(v.Knowledge, ctxKnowledge{Entry: k.Entry, Name: k.Name, Text: k.Text, Event: k.Event, Scene: k.Scene})
	}
	for _, k := range p.ReaderOnly {
		v.ReaderOnly = append(v.ReaderOnly, ctxKnowledge{Entry: k.Entry, Name: k.Name, Text: k.Text, Event: k.Event, Scene: k.Scene})
	}
	for _, s := range p.PriorScenes {
		v.PriorScenes = append(v.PriorScenes, ctxSceneSummary{Unit: s.Unit, Text: s.Text, Stale: s.Stale})
	}
	for _, r := range p.Reveal {
		v.Reveal = append(v.Reveal, ctxReveal{Section: r.Section, Text: r.Text})
	}
	for _, t := range p.Threads {
		v.Threads = append(v.Threads, ctxThread{ID: t.ID, Text: t.Text, PlantedIn: t.PlantedIn, PlannedRecover: t.PlannedRecover, Overdue: t.Overdue})
	}
	for _, f := range p.Feedback {
		v.Feedback = append(v.Feedback, feedbackOf(f))
	}
	for _, d := range p.Decisions {
		cd := ctxDecision{ID: d.ID, Question: d.Question, Options: []ctxOption{}, Blocking: d.Blocking}
		for _, o := range d.Options {
			cd.Options = append(cd.Options, ctxOption{o.ID, o.Text})
		}
		v.Decisions = append(v.Decisions, cd)
	}
	for _, r := range p.Norms.Rules {
		v.Norms.Rules = append(v.Norms.Rules, ctxRule{ID: r.ID, Type: r.Type, Severity: r.Severity, Params: r.Params})
	}
	for _, t := range p.Norms.Glossary {
		v.Norms.Glossary = append(v.Norms.Glossary, ctxTerm{Term: t.Term.Term, English: optional(t.English), Avoid: nonNilStrings(t.Avoid)})
	}
	for _, t := range p.Norms.BannedTerms {
		v.Norms.BannedTerms = append(v.Norms.BannedTerms, ctxBannedTerm{Term: t.BannedTerm.Term, Replacement: optional(t.Replacement)})
	}
	for _, s := range p.Sources {
		v.Sources = append(v.Sources, ctxSource{ID: s.ID, Type: s.Type, Title: optional(s.Title), Text: s.Text, Truncated: s.Truncated})
	}
	for _, n := range p.Neighbors {
		v.Neighbors = append(v.Neighbors, ctxNeighbor{Unit: n.Unit, Position: n.Position, Title: optional(n.Title), Excerpt: n.Excerpt})
	}
	if s := p.Series; s != nil {
		v.Series = &ctxSeries{ID: s.ID, Title: s.Title, Facts: factsView(s.Facts), Summaries: []ctxSummaryRef{}}
		for _, x := range s.Summaries {
			v.Series.Summaries = append(v.Series.Summaries, ctxSummaryRef(x))
		}
	}
	for _, t := range p.Trimmed {
		v.Trimmed = append(v.Trimmed, ctxTrim{Part: t.Part, ID: optional(t.ID), Chars: t.Chars, KeptChars: t.KeptChars})
	}
	return v
}

// canonWork 返回单元引用所属作品的 id：设定集（canon）是作品级产物。
func canonWork(ref string) string {
	if i := strings.Index(ref, "/"); i >= 0 {
		return ref[:i]
	}
	return ref
}

func runContext(inv *invocation) int {
	dir := projectDir(inv)
	res := &contextResult{}
	problems := workspace.Require(dir)
	if problems == nil {
		var w *work.Work
		var u *work.Unit
		if w, u, problems = work.Resolve(dir, inv.args[0]); u != nil {
			var p *ctxpack.Pack
			if p, problems = ctxpack.Build(dir, w, u); p != nil {
				res.Context, res.pack = viewContext(p), p
			}
		}
	}
	res.Diagnostics = diagnostics(problems, dir)
	for i, d := range res.Diagnostics {
		// 规则的合并问题取决于作品的文体；整体校验按 default_genre 合并，未必能重新检出，用本命令复查。
		switch {
		case strings.HasPrefix(d.Code, "rule."):
			res.Diagnostics[i].Next = []string{shellCommand("wa-cli", "context", inv.args[0], "--dir", dir, "--json")}
		case d.Code == ctxpack.CodeSummaryMissing:
			res.Diagnostics[i].Next = []string{"wa-cli help summary set --json", shellCommand("wa-cli", "context", inv.args[0], "--dir", dir, "--json")}
		case d.Code == ctxpack.CodeCharacterNotInCanon:
			res.Diagnostics[i].Next = []string{"wa-cli help canon add --json", shellCommand("wa-cli", "canon", "list", canonWork(inv.args[0]), "--dir", dir, "--json")}
		case d.Code == ctxpack.CodePOVNotInCanon:
			res.Diagnostics[i].Next = []string{shellCommand("wa-cli", "canon", "list", canonWork(inv.args[0]), "--dir", dir, "--json"), "wa-cli help scene contract --json"}
		case d.Code == ctxpack.CodeSourceSkipped:
			// 非 UTF-8 的素材整体校验检不出，用本命令复查；素材列表给出可用的素材。
			res.Diagnostics[i].Next = []string{shellCommand("wa-cli", "source", "list", "--dir", dir, "--json"), shellCommand("wa-cli", "context", inv.args[0], "--dir", dir, "--json")}
		}
	}
	return finish(inv, res, func(out io.Writer) error {
		if res.pack == nil {
			return nil
		}
		_, err := io.WriteString(out, res.pack.Markdown())
		return err
	})
}
