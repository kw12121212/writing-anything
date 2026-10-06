package cli

import (
	"fmt"
	"io"
	"strings"

	"github.com/kw12121212/writing-anything/internal/brief"
	"github.com/kw12121212/writing-anything/internal/learn"
	"github.com/kw12121212/writing-anything/internal/workspace"
)

const proposalHelp = "规范包提议（Pack Proposal）：id、path、pack、sources、stats（chars、sentences、paragraphs、punctuation、terms；没有素材时为 null）与 items（id、kind、text 或 term、status、confirmation、decidedAt、key、evidence）；失败时为 null"

var packLearnCommand = &command{
	name: "pack learn",
	summary: "从作者的过往文章（past-article 素材）生成或更新文风画像（Style Profile）提议：计算确定性的文风统计，并记录 Agent 补充的定性条目；" +
		"提议经作者用 pack confirm 逐项确认前不影响任何规则",
	args: []argSpec{{"proposal", "提议 id；提议保存在 " + learn.Dir + "/<proposal>.yaml", true}},
	flags: []flagSpec{dirFlag,
		{"pack", flagString, "确认后写入的作者规范包 id；首次运行必需"},
		{"source", flagList, "past-article 素材 id；可重复，首次运行至少一个，之后可以追加"},
		{"style", flagList, "文风描述（如「多用短句」）；可重复"},
		{"avoid", flagList, "要避免的写法，确认后写入 banned_terms；可重复"}},
	outputKeys: []outputKey{{"proposal", proposalHelp}},
	exitCodes:  standardExitCodes,
	run:        runPackLearn,
}

var packPreferCommand = &command{
	name: "pack prefer",
	summary: "从作者对 Agent 段落的修改（修改记录（Edit Record）与当前尚未记入的修改）和作者反馈中确定性地归纳作者偏好（Author Preference），" +
		"把带证据的条目合并进规范包提议；作者用 pack confirm 确认前不影响任何规则",
	args:  []argSpec{{"proposal", "提议 id；提议保存在 " + learn.Dir + "/<proposal>.yaml", true}},
	flags: []flagSpec{dirFlag, {"pack", flagString, "确认后写入的作者规范包 id；提议不存在时必需"}},
	outputKeys: []outputKey{
		{"proposal", proposalHelp},
		{"observed", "参与归纳的输入规模：edits（修改对数）与 feedback（反馈数）；失败时为 null"},
	},
	exitCodes: standardExitCodes,
	run:       runPackPrefer,
}

var packConfirmCommand = &command{
	name:    "pack confirm",
	summary: "按作者的确认把提议中的条目写入作者自己的规范包（Pack）：baseline 写为 style_profile，文风描述追加到 style，要避免的写法追加到 banned_terms",
	args:    []argSpec{{"proposal", "提议 id", true}},
	flags: []flagSpec{dirFlag,
		{"item", flagList, "要确认的条目 id；可重复，至少一个"},
		{"confirmation", flagString, "作者原话的摘要，作为作者确认"}},
	outputKeys: []outputKey{{"proposal", proposalHelp}},
	exitCodes:  standardExitCodes,
	run:        runPackConfirm,
}

var packRejectCommand = &command{
	name:       "pack reject",
	summary:    "记录作者拒绝的提议条目：被拒绝的内容之后不再由 pack learn 重复提出；不修改任何规范包",
	args:       []argSpec{{"proposal", "提议 id", true}},
	flags:      []flagSpec{dirFlag, {"item", flagList, "要拒绝的条目 id；可重复，至少一个"}},
	outputKeys: []outputKey{{"proposal", proposalHelp}},
	exitCodes:  standardExitCodes,
	run:        runPackReject,
}

type proposalItemView struct {
	ID           string           `json:"id"`
	Kind         string           `json:"kind"`
	Text         string           `json:"text,omitempty"`
	Term         string           `json:"term,omitempty"`
	Status       string           `json:"status"`
	Confirmation *string          `json:"confirmation"`
	DecidedAt    *string          `json:"decidedAt"`
	Key          *string          `json:"key"`
	Evidence     []learn.Evidence `json:"evidence"`
}

type proposalView struct {
	ID      string             `json:"id"`
	Path    string             `json:"path"`
	Pack    string             `json:"pack"`
	Sources []string           `json:"sources"`
	Stats   *learn.Stats       `json:"stats"`
	Items   []proposalItemView `json:"items"`
}

type proposalResult struct {
	Result
	Proposal *proposalView `json:"proposal"`
}

func viewProposal(pr *learn.Proposal) *proposalView {
	v := &proposalView{ID: pr.ID, Path: pr.Path, Pack: pr.Pack, Sources: pr.Sources, Stats: pr.Stats, Items: []proposalItemView{}}
	for _, it := range pr.Items {
		evidence := it.Evidence
		if evidence == nil {
			evidence = []learn.Evidence{}
		}
		v.Items = append(v.Items, proposalItemView{ID: it.ID, Kind: it.Kind, Text: it.Text, Term: it.Term, Status: it.Status,
			Confirmation: optional(it.Confirmation), DecidedAt: optional(it.DecidedAt), Key: optional(it.Key), Evidence: evidence})
	}
	return v
}

// checkIDs 检查参数给出的 id（提议、规范包、素材、条目）符合 id 格式，避免拼接出工作区之外的路径。
func checkIDs(flag string, ids ...string) []workspace.Problem {
	var out []workspace.Problem
	for _, id := range ids {
		if !brief.ValidID(id) {
			out = append(out, invalidValue(flag, id, "应为 id 格式（小写字母开头，由小写字母、数字与连字符组成）"))
		}
	}
	return out
}

// finishProposal 写出提议命令的结果：参数与来源问题（不填 path）的下一步为查看用法与已登记的素材。
// out 给出时作为要写出的输出信封（含 res 之外的输出键，如 pack prefer 的 observed）。
func finishProposal(inv *invocation, name, dir string, res *proposalResult, problems []workspace.Problem, verb string, out ...enveloped) int {
	res.Diagnostics = diagnostics(problems, dir)
	help := "wa-cli help " + name + " --json"
	sources := shellCommand("wa-cli", "source", "list", "--dir", dir, "--json")
	show := shellCommand("wa-cli", "pack", "learn", inv.args[0], "--dir", dir, "--json")
	// 按错误码选择能重新检出问题的命令（cli-contract/next-rechecks-problem）：这些问题由参数、素材或条目状态引起，
	// 单文件 validate 检不出来。
	for i, d := range res.Diagnostics {
		switch d.Code {
		case learn.CodeUnknownSource, learn.CodeNotPastArticle, learn.CodeSourceNotText, learn.CodeTooLarge:
			res.Diagnostics[i].Next = []string{sources, help}
		case learn.CodeInstalledPack:
			res.Diagnostics[i].Next = []string{shellCommand("wa-cli", "pack", "list", "--dir", dir, "--json"), help}
		case learn.CodePreviouslyRejected, learn.CodeAlreadyConfirmed, learn.CodeUnknownItem:
			res.Diagnostics[i].Next = []string{show, help}
		case learn.CodeAnchoredValue, learn.CodeMissingPack, learn.CodeNoSources, learn.CodePackMismatch, learn.CodeNotFound, codeInvalidValue:
			res.Diagnostics[i].Next = []string{help}
		default:
			if d.Path == "" && d.Code != workspace.CodeNotFound {
				res.Diagnostics[i].Next = []string{help, sources}
			}
		}
	}
	var target enveloped = res
	if len(out) > 0 {
		target = out[0]
	}
	return finish(inv, target, func(w io.Writer) error {
		p := res.Proposal
		if p == nil {
			return nil
		}
		var b strings.Builder
		fmt.Fprintf(&b, "%s规范包提议（Pack Proposal）%s（写入规范包 %s，%d 个素材", verb, p.ID, p.Pack, len(p.Sources))
		if p.Stats != nil {
			fmt.Fprintf(&b, "，%d 字，平均句长 %v", p.Stats.Chars, p.Stats.Sentences.Mean)
		}
		b.WriteString("）\n")
		for _, it := range p.Items {
			fmt.Fprintf(&b, "- %s（%s）%s%s：%s\n", it.ID, it.Kind, it.Text, it.Term, it.Status)
		}
		_, err := io.WriteString(w, b.String())
		return err
	})
}

func runPackLearn(inv *invocation) int {
	dir, id := projectDir(inv), inv.args[0]
	res := &proposalResult{}
	problems := workspace.Require(dir)
	if problems == nil {
		problems = checkIDs("proposal", id)
		if pack, ok := inv.values["pack"]; ok {
			problems = append(problems, checkIDs("pack", pack)...)
		}
		problems = append(problems, checkIDs("source", inv.lists["source"]...)...)
		for _, f := range []string{"style", "avoid"} {
			for _, v := range inv.lists[f] {
				if strings.TrimSpace(v) == "" {
					problems = append(problems, invalidValue(f, v, "不能为空白"))
				}
			}
		}
	}
	if problems == nil {
		var pr *learn.Proposal
		pr, res.ChangedPaths, problems = learn.Learn(dir, learn.Request{ID: id, Pack: inv.values["pack"], Sources: inv.lists["source"], Styles: inv.lists["style"], Avoids: inv.lists["avoid"]})
		if pr != nil {
			res.Proposal = viewProposal(pr)
		}
	}
	return finishProposal(inv, "pack learn", dir, res, problems, "已更新")
}

func runPackConfirm(inv *invocation) int {
	dir, id := projectDir(inv), inv.args[0]
	res := &proposalResult{}
	problems := workspace.Require(dir)
	if problems == nil {
		problems = append(checkIDs("proposal", id), checkIDs("item", inv.lists["item"]...)...)
		if len(inv.lists["item"]) == 0 {
			problems = append(problems, invalidValue("item", "", "至少给出一个要确认的条目 id"))
		}
		if strings.TrimSpace(inv.values["confirmation"]) == "" {
			problems = append(problems, invalidValue("confirmation", inv.values["confirmation"], "必须给出作者原话的摘要"))
		}
	}
	if problems == nil {
		var pr *learn.Proposal
		pr, res.ChangedPaths, problems = learn.Confirm(dir, id, inv.lists["item"], inv.values["confirmation"], now())
		if pr != nil && problems == nil {
			res.Proposal = viewProposal(pr)
		}
	}
	return finishProposal(inv, "pack confirm", dir, res, problems, "已确认")
}

func runPackReject(inv *invocation) int {
	dir, id := projectDir(inv), inv.args[0]
	res := &proposalResult{}
	problems := workspace.Require(dir)
	if problems == nil {
		problems = append(checkIDs("proposal", id), checkIDs("item", inv.lists["item"]...)...)
		if len(inv.lists["item"]) == 0 {
			problems = append(problems, invalidValue("item", "", "至少给出一个要拒绝的条目 id"))
		}
	}
	if problems == nil {
		var pr *learn.Proposal
		pr, res.ChangedPaths, problems = learn.Reject(dir, id, inv.lists["item"], now())
		if pr != nil {
			res.Proposal = viewProposal(pr)
		}
	}
	return finishProposal(inv, "pack reject", dir, res, problems, "已记录拒绝：")
}

type preferResult struct {
	proposalResult
	Observed *learn.Observed `json:"observed"`
}

func runPackPrefer(inv *invocation) int {
	dir, id := projectDir(inv), inv.args[0]
	res := &preferResult{}
	problems := workspace.Require(dir)
	if problems == nil {
		problems = checkIDs("proposal", id)
		if pack, ok := inv.values["pack"]; ok {
			problems = append(problems, checkIDs("pack", pack)...)
		}
	}
	if problems == nil {
		var pr *learn.Proposal
		var obs learn.Observed
		if pr, obs, res.ChangedPaths, problems = learn.Prefer(dir, id, inv.values["pack"]); pr != nil {
			res.Proposal, res.Observed = viewProposal(pr), &obs
		}
	}
	return finishProposal(inv, "pack prefer", dir, &res.proposalResult, problems, "已归纳", res)
}
