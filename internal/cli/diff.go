package cli

import (
	"fmt"
	"io"
	"strings"

	"github.com/kw12121212/writing-anything/internal/draft"
	"github.com/kw12121212/writing-anything/internal/work"
	"github.com/kw12121212/writing-anything/internal/workspace"
)

var diffCommand = &command{
	name:       "diff",
	summary:    "逐段列出作者自最近一次 draft write 以来对单元（Unit）正文的改动，并给出可用作 --base 的当前正文哈希",
	args:       []argSpec{{"unit", unitRefHelp, true}},
	flags:      []flagSpec{dirFlag},
	outputKeys: []outputKey{{"diff", "改动：unit、path、edited、base、recorded 与 changes（每项含 kind、at、line、text）；失败时为 null"}},
	exitCodes:  standardExitCodes,
	run:        runDiff,
}

type changeView struct {
	Kind string  `json:"kind"`
	At   int     `json:"at"`
	Line *int    `json:"line"`
	Text *string `json:"text"`
}

type diffView struct {
	Unit     string       `json:"unit"`
	Path     string       `json:"path"`
	Edited   bool         `json:"edited"`
	Base     string       `json:"base"`
	Recorded *string      `json:"recorded"`
	Changes  []changeView `json:"changes"`
	total    int          // 当前正文的段落数，用于人读文本
}

type diffResult struct {
	Result
	Diff *diffView `json:"diff"`
}

// changeNames 是改动类型的人读名称。
var changeNames = map[string]string{"added": "新增", "edited": "修改", "removed": "删除"}

func runDiff(inv *invocation) int {
	dir := projectDir(inv)
	res := &diffResult{}
	problems := workspace.Require(dir)
	if problems == nil {
		var w *work.Work
		var u *work.Unit
		if w, u, problems = work.Resolve(dir, inv.args[0]); u != nil {
			var c *draft.Changes
			if c, problems = draft.Diff(dir, w, u); c != nil {
				v := &diffView{Unit: u.Ref, Path: c.Path, Edited: c.Edited, Base: c.Base, Changes: []changeView{}, total: c.Total}
				if c.Recorded != "" {
					v.Recorded = &c.Recorded
				}
				for _, ch := range c.Changes {
					cv := changeView{Kind: ch.Kind, At: ch.At}
					if ch.Kind != "removed" {
						line, text := ch.Line, ch.Text
						cv.Line, cv.Text = &line, &text
					}
					v.Changes = append(v.Changes, cv)
				}
				res.Diff = v
			}
		}
	}
	res.Diagnostics = diagnostics(problems, dir)
	return finish(inv, res, func(w io.Writer) error {
		v := res.Diff
		if v == nil {
			return nil
		}
		var b strings.Builder
		if !v.Edited {
			fmt.Fprintf(&b, "作者没有修改单元（Unit）%s 的正文 %s\n", v.Unit, v.Path)
		} else {
			fmt.Fprintf(&b, "作者修改过单元（Unit）%s 的正文 %s：\n", v.Unit, v.Path)
		}
		for _, c := range v.Changes {
			if c.Kind == "removed" {
				where := fmt.Sprintf("第 %d 段之前", c.At)
				if c.At > v.total {
					where = "正文末尾"
				}
				fmt.Fprintf(&b, "  删除：%s原有的一段\n", where)
				continue
			}
			fmt.Fprintf(&b, "  %s：第 %d 段（第 %d 行）\n", changeNames[c.Kind], c.At, *c.Line)
			for _, l := range strings.Split(*c.Text, "\n") {
				fmt.Fprintf(&b, "    %s\n", l)
			}
		}
		fmt.Fprintf(&b, "在作者的版本上修改后写入：%s\n", shellCommand("wa-cli", "draft", "write", v.Unit, "--base", v.Base, "--dir", dir))
		_, err := io.WriteString(w, b.String())
		return err
	})
}
