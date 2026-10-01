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
	outputKeys: []outputKey{{"context", "上下文包：unit、type、stage、genre、budget（value、source）、used、brief、outline、section、feedback、decisions、norms、sources、neighbors 与 trimmed；失败时为 null"}},
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
	Unit      string         `json:"unit"`
	Type      string         `json:"type"`
	Stage     string         `json:"stage"`
	Genre     string         `json:"genre"`
	Budget    resolve.Value  `json:"budget"`
	Used      int            `json:"used"`
	Brief     *ctxBrief      `json:"brief"`
	Outline   *ctxOutline    `json:"outline"`
	Section   *ctxSection    `json:"section"`
	Feedback  []feedbackView `json:"feedback"`
	Decisions []ctxDecision  `json:"decisions"`
	Norms     ctxNorms       `json:"norms"`
	Sources   []ctxSource    `json:"sources"`
	Neighbors []ctxNeighbor  `json:"neighbors"`
	Trimmed   []ctxTrim      `json:"trimmed"`
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
	for _, t := range p.Trimmed {
		v.Trimmed = append(v.Trimmed, ctxTrim{Part: t.Part, ID: optional(t.ID), Chars: t.Chars, KeptChars: t.KeptChars})
	}
	return v
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
