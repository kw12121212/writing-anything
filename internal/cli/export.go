package cli

import (
	"fmt"
	"io"
	"slices"
	"strings"

	"github.com/kw12121212/writing-anything/internal/export"
	"github.com/kw12121212/writing-anything/internal/resolve"
	"github.com/kw12121212/writing-anything/internal/work"
	"github.com/kw12121212/writing-anything/internal/workspace"
)

var exportCommand = &command{
	name: "export",
	summary: "导出（Export）作品中已验收（Acceptance）的单元：按单元树先序拼接正文，去掉批注与单处豁免注释；markdown 可选附带文件头元数据（Front Matter），" +
		"wechat-html 输出只含内联样式（Inline Style）的公众号 HTML，配图导出为占位并附配图清单",
	args: []argSpec{{"work", "作品 id", true}},
	flags: []flagSpec{dirFlag,
		{"format", flagString, "导出格式：" + strings.Join(export.Formats, "、") + "；省略时为 markdown"},
		{"output", flagString, "输出文件，相对 --dir；省略时为 .writing-anything/exports/<work>.md（wechat-html 为 <work>.html）"},
		{"front-matter", flagBool, "在开头附带 YAML front matter：title、description、genre、units 与 accepted_at；只用于 markdown"}},
	outputKeys: []outputKey{{"export", "导出结果：work、format、path、units（导出的单元，按顺序）、skipped（没有导出的单元，每项含 unit 与 reason）、chars、" +
		"images（wechat-html 的配图（Illustration），每项含 index、alt、src 与 title；markdown 为空数组）、imageList（配图清单的路径，没有时为 null）与 " +
		"disclosure（AI 参与说明（AI-generated Content Label）：text、placement、aiParagraphs、paragraphs 与 aiPercent；没有 AI 参与的段落时为 null）；失败时为 null"}},
	exitCodes: standardExitCodes,
	run:       runExport,
}

type skippedView struct {
	Unit   string `json:"unit"`
	Reason string `json:"reason"`
}

type imageView struct {
	Index int     `json:"index"`
	Alt   string  `json:"alt"`
	Src   string  `json:"src"`
	Title *string `json:"title"`
}

type exportView struct {
	Work       string          `json:"work"`
	Format     string          `json:"format"`
	Path       string          `json:"path"`
	Units      []string        `json:"units"`
	Skipped    []skippedView   `json:"skipped"`
	Chars      int             `json:"chars"`
	Images     []imageView     `json:"images"`
	ImageList  *string         `json:"imageList"`
	Disclosure *disclosureView `json:"disclosure"`
}

type disclosureView struct {
	Text         string `json:"text"`
	Placement    string `json:"placement"`
	AIParagraphs int    `json:"aiParagraphs"`
	Paragraphs   int    `json:"paragraphs"`
	AIPercent    int    `json:"aiPercent"`
}

func disclosureConfig(r *resolve.Resolved) *export.DisclosureConfig {
	return &export.DisclosureConfig{Placement: fmt.Sprint(r.AIDisclosure.Placement.Value), Template: fmt.Sprint(r.AIDisclosure.Template.Value)}
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
	_, frontMatter := inv.values["front-matter"]
	switch {
	case problems != nil:
	case !slices.Contains(export.Formats, format):
		problems = []workspace.Problem{invalidValue("format", format, "支持的导出格式为 "+strings.Join(export.Formats, "、"))}
	case format == export.FormatWechat && frontMatter:
		p := invalidValue("front-matter", "true", "--front-matter 只用于 markdown 格式；公众号 HTML 没有文件头元数据")
		p.Field = "front-matter"
		problems = []workspace.Problem{p}
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
				var r *export.Result
				var changed []string
				opt := export.Options{FrontMatter: frontMatter, Output: inv.values["output"], Level: level}
				// 样式主题与 AI 参与说明的配置取自按作品文体合并的结果（workspace-artifacts/resolved-sources）。
				resolved, ps := resolve.ForGenre(dir, w.Genre.ID)
				switch {
				case resolved == nil:
					problems = ps
				case format == export.FormatWechat:
					opt.Disclosure = disclosureConfig(resolved)
					theme := map[string]string{}
					for k, v := range resolved.HTMLTheme {
						theme[k] = fmt.Sprint(v.Value)
					}
					r, changed, problems = export.WechatHTML(dir, w, opt, theme)
				default:
					opt.Disclosure = disclosureConfig(resolved)
					r, changed, problems = export.Markdown(dir, w, opt)
				}
				if r != nil {
					v := &exportView{Work: w.ID, Format: format, Path: r.Path, Units: r.Units, Skipped: []skippedView{}, Chars: r.Chars, Images: []imageView{}, ImageList: optional(r.ImageList)}
					for _, s := range r.Skipped {
						v.Skipped = append(v.Skipped, skippedView(s))
					}
					if d := r.Disclosure; d != nil {
						v.Disclosure = &disclosureView{Text: d.Text, Placement: d.Placement, AIParagraphs: d.AIParagraphs, Paragraphs: d.Paragraphs, AIPercent: d.AIPercent}
					}
					for _, img := range r.Images {
						v.Images = append(v.Images, imageView{Index: img.Index, Alt: img.Alt, Src: img.Src, Title: optional(img.Title)})
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
		case export.CodeUnknownProvenance:
			res.Diagnostics[i].Next = []string{shellCommand("wa-cli", "provenance", work.RefOf(strings.TrimSuffix(d.Path, "/provenance.yaml")), "--dir", dir, "--json")}
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
		text := fmt.Sprintf("已导出作品 %s 的 %d 个已验收单元到 %s（%d 字符）；%d 个单元没有导出\n",
			res.Export.Work, len(res.Export.Units), res.Export.Path, res.Export.Chars, len(res.Export.Skipped))
		if d := res.Export.Disclosure; d != nil {
			text += fmt.Sprintf("AI 参与说明（%s）：%s\n", d.Placement, d.Text)
		}
		if res.Export.ImageList != nil {
			text += fmt.Sprintf("配图清单（%d 张）：%s\n", len(res.Export.Images), *res.Export.ImageList)
		}
		_, err := io.WriteString(w, text)
		return err
	})
}
