package cli

import (
	"fmt"
	"io"
	"strings"

	"github.com/kw12121212/writing-anything/internal/export"
	"github.com/kw12121212/writing-anything/internal/work"
	"github.com/kw12121212/writing-anything/internal/workspace"
)

var exportCommand = &command{
	name: "export",
	summary: "导出（Export）作品中已验收（Acceptance）的单元：按单元树先序拼接正文，去掉批注与单处豁免注释，" +
		"可选附带文件头元数据（Front Matter）",
	args: []argSpec{{"work", "作品 id", true}},
	flags: []flagSpec{dirFlag,
		{"format", flagString, "导出格式：" + strings.Join(export.Formats, "、") + "；省略时为 markdown"},
		{"output", flagString, "输出文件，相对 --dir；省略时为 .writing-anything/exports/<work>.md"},
		{"front-matter", flagBool, "在开头附带 YAML front matter：title、description、genre、units 与 accepted_at"}},
	outputKeys: []outputKey{{"export", "导出结果：work、format、path、units（导出的单元，按顺序）、skipped（没有导出的单元，每项含 unit 与 reason）与 chars；失败时为 null"}},
	exitCodes:  standardExitCodes,
	run:        runExport,
}

type skippedView struct {
	Unit   string `json:"unit"`
	Reason string `json:"reason"`
}

type exportView struct {
	Work    string        `json:"work"`
	Format  string        `json:"format"`
	Path    string        `json:"path"`
	Units   []string      `json:"units"`
	Skipped []skippedView `json:"skipped"`
	Chars   int           `json:"chars"`
}

type exportResult struct {
	Result
	Export *exportView `json:"export"`
}

func runExport(inv *invocation) int {
	dir := projectDir(inv)
	res := &exportResult{}
	problems := workspace.Require(dir)
	format := inv.values["format"]
	if format == "" {
		format = "markdown"
	}
	if problems == nil && format != "markdown" {
		problems = []workspace.Problem{invalidValue("format", format, "支持的导出格式为 "+strings.Join(export.Formats, "、"))}
	}
	if problems == nil {
		var w *work.Work
		var u *work.Unit
		if w, u, problems = work.Resolve(dir, inv.args[0]); u != nil {
			if u.Parent != nil {
				problems = []workspace.Problem{{Code: work.CodeNotFound, Severity: workspace.SeverityError, Field: "work",
					Message: "export 导出整个作品；请给出作品 id（单元引用中 / 之前的部分），而不是子单元 " + u.Ref}}
			} else {
				level := workspace.DefaultGateLevel
				if cfg, _ := workspace.Load(dir); cfg != nil && cfg.GateLevel != "" {
					level = cfg.GateLevel
				}
				_, frontMatter := inv.values["front-matter"]
				var r *export.Result
				var changed []string
				r, changed, problems = export.Markdown(dir, w, export.Options{FrontMatter: frontMatter, Output: inv.values["output"], Level: level})
				if r != nil {
					v := &exportView{Work: w.ID, Format: format, Path: r.Path, Units: r.Units, Skipped: []skippedView{}, Chars: r.Chars}
					for _, s := range r.Skipped {
						v.Skipped = append(v.Skipped, skippedView(s))
					}
					res.Export, res.ChangedPaths = v, changed
				}
			}
		}
	}
	res.Diagnostics = diagnostics(problems, dir)
	for i, d := range res.Diagnostics {
		switch d.Code {
		case export.CodeUnaccepted:
			res.Diagnostics[i].Next = []string{shellCommand("wa-cli", "review", "show", work.RefOf(d.Path), "--dir", dir, "--json")}
		case export.CodeNothing:
			res.Diagnostics[i].Next = []string{shellCommand("wa-cli", "next", "--dir", dir, "--json"), shellCommand("wa-cli", "status", "--dir", dir, "--json")}
		case export.CodeInvalidOutput, codeInvalidValue:
			res.Diagnostics[i].Next = []string{"wa-cli help export --json"}
		case work.CodeNotFound:
			if d.Field == "work" {
				res.Diagnostics[i].Next = []string{shellCommand("wa-cli", "status", "--dir", dir, "--json")}
			}
		}
	}
	return finish(inv, res, func(w io.Writer) error {
		if res.Export == nil {
			return nil
		}
		_, err := fmt.Fprintf(w, "已导出作品 %s 的 %d 个已验收单元到 %s（%d 字符）；%d 个单元没有导出\n",
			res.Export.Work, len(res.Export.Units), res.Export.Path, res.Export.Chars, len(res.Export.Skipped))
		return err
	})
}
