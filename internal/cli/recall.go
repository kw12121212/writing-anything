package cli

import (
	"fmt"
	"io"
	"strings"

	"github.com/kw12121212/writing-anything/internal/recall"
	"github.com/kw12121212/writing-anything/internal/workspace"
)

var recallCommand = &command{
	name:    "recall",
	summary: "以全文检索（Full-text Search）在已验收正文中查找关键词或设定条目（Canon Entry）称谓的全部出现位置",
	args:    []argSpec{{"query", "关键词，或设定条目的 id、名称、别名（Alias）", true}},
	flags: []flagSpec{
		dirFlag,
		{"work", flagString, "限定检索的作品（Work）id；省略时检索全部作品"},
	},
	outputKeys: []outputKey{
		{"results", "命中数组，每项含 work、unit、paragraph、snippet、matched 与 entry（未解析到设定条目时为 null），按作品、单元先序与段落号排序"},
		{"indexed", "本次刷新重建索引的单元引用数组；没有重建时为空数组"},
	},
	exitCodes: standardExitCodes,
	run:       runRecall,
}

type recallResult struct {
	Result
	Results []recall.Hit `json:"results"`
	Indexed []string     `json:"indexed"`
}

func runRecall(inv *invocation) int {
	dir := projectDir(inv)
	res := &recallResult{Results: []recall.Hit{}, Indexed: []string{}}
	problems := workspace.Require(dir)
	if problems == nil {
		var out *recall.Output
		var changed []string
		out, changed, problems = recall.Run(dir, recall.Input{Query: inv.args[0], Work: inv.values["work"]})
		if out != nil {
			res.Results, res.Indexed = out.Results, out.Indexed
		}
		res.ChangedPaths = changed
	}
	res.Diagnostics = diagnostics(problems, dir)
	helpNext(res.Diagnostics, "recall", []string{"input.invalid-value"})
	return finish(inv, res, func(w io.Writer) error {
		var b strings.Builder
		if len(res.Results) == 0 {
			if !hasError(res.Diagnostics) {
				fmt.Fprintf(&b, "没有在已验收正文中找到「%s」\n", inv.args[0])
			}
		} else {
			fmt.Fprintf(&b, "在已验收正文中找到「%s」%d 处\n", inv.args[0], len(res.Results))
			for _, h := range res.Results {
				entry := ""
				if h.Entry != nil {
					entry = "（条目 " + *h.Entry + "）"
				}
				fmt.Fprintf(&b, "%s 第 %d 段：%s%s\n", h.Unit, h.Paragraph, h.Snippet, entry)
			}
		}
		_, err := io.WriteString(w, b.String())
		return err
	})
}
