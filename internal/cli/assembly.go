package cli

import (
	"fmt"
	"io"
	"strings"

	"github.com/kw12121212/writing-anything/internal/assembly"
	"github.com/kw12121212/writing-anything/internal/series"
	"github.com/kw12121212/writing-anything/internal/work"
	"github.com/kw12121212/writing-anything/internal/workspace"
)

var assembleCommand = &command{
	name:    "assemble",
	summary: "组稿（Assembly）：按单元树先序把已验收场景组成整书（书名、目录、部/章标题、去注释正文与 AI 参与说明），导出 Markdown 或纯文本；存在未完成场景、连续性阻塞、open 复查标记，或 strict 关卡下有待作者复核（Pending Author Review）的命令行渠道确认时拒绝组稿并列出阻塞项",
	args: []argSpec{
		{"work", "作品 id", true},
	},
	flags: []flagSpec{
		dirFlag,
		{"output", flagString, "输出文件路径（相对 --dir 或绝对）；省略时为 .writing-anything/exports/<work>-manuscript.md（text 为 .txt）"},
		{"format", flagString, "输出格式：markdown（缺省）或 text"},
	},
	outputKeys: []outputKey{{"manuscript", "组稿结果：work、path、format、scenes（场景数）、parts（部数）与 chars（书稿字符数）；失败时为 null"}},
	exitCodes:  standardExitCodes,
	run:        runAssemble,
}

type assembleResult struct {
	Result
	Manuscript *assembly.View `json:"manuscript"`
}

func runAssemble(inv *invocation) int {
	dir := projectDir(inv)
	res := &assembleResult{}
	problems := workspace.Require(dir)
	if problems == nil {
		var w *work.Work
		if w, _, problems = work.Resolve(dir, inv.args[0]); w != nil {
			// AI 参与说明按出处的既有机制生成（同 export 的合并配置；无模板时为空）。
			footer := ""
			if resolved, ps2 := series.Resolve(dir, w); resolved != nil {
				footer = assembly.DisclosureText(dir, w, disclosureConfig(resolved))
			} else {
				_ = ps2
			}
			var v *assembly.View
			var changed []string
			if v, changed, problems = assembly.Assemble(dir, w, inv.values["format"], inv.values["output"], footer); v != nil {
				res.Manuscript, res.ChangedPaths = v, changed
			}
		}
	}
	res.Diagnostics = diagnostics(problems, dir)
	assembleNext(res.Diagnostics, inv.args[0], dir)
	return finish(inv, res, func(w io.Writer) error {
		if res.Manuscript == nil {
			return nil
		}
		_, err := fmt.Fprintf(w, "已组稿（Assembly）%s：%s（%d 个场景、%d 部，%d 字符）\n",
			res.Manuscript.Work, res.Manuscript.Path, res.Manuscript.Scenes, res.Manuscript.Parts, res.Manuscript.Chars)
		return err
	})
}

// assembleNext 为组稿诊断给出下一步命令。
func assembleNext(ds []Diagnostic, ref, dir string) {
	for i := range ds {
		switch {
		case strings.HasPrefix(ds[i].Code, "assemble.scenes-open"):
			ds[i].Next = []string{shellCommand("wa-cli", "status", "--dir", dir, "--json")}
		case strings.HasPrefix(ds[i].Code, "assemble.continuity"):
			ds[i].Next = []string{shellCommand("wa-cli", "decisions", "--dir", dir, "--json"),
				"wa-cli help continuity --json", shellCommand("wa-cli", "review", "show", ref, "--dir", dir, "--json")}
		case ds[i].Code == assembly.CodeUnreviewed:
			ds[i].Next = []string{previewServeCommand(dir, ref), shellCommand("wa-cli", "review", "show", ref, "--dir", dir, "--json")}
		case strings.HasPrefix(ds[i].Code, "assemble.rechecks"):
			ds[i].Next = []string{shellCommand("wa-cli", "impact", "list", ref, "--dir", dir, "--json"),
				"wa-cli help impact clear --json"}
		}
	}
}
