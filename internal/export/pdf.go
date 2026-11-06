package export

import (
	"bytes"
	"encoding/base64"
	"html/template"
	"os"
	"path/filepath"
	"strings"

	"github.com/kw12121212/writing-anything/internal/illustration"
	"github.com/kw12121212/writing-anything/internal/work"
	"github.com/kw12121212/writing-anything/internal/workspace"
)

// PDF（pdf）：把已验收单元导出为给读者的 A4 文档（见 export-pdf）。正文与配图复用 wechat-html 的节点树与配图内嵌，
// 外层套一份打印样式；浏览器打印由命令层注入的 render 完成（external-processes 允许的固定无头浏览器），本包只负责
// 生成待打印的 HTML 并把结果按导出输出路径规则落盘，不依赖任何外部程序。

// FormatPDF 是 PDF 导出格式。
const FormatPDF = "pdf"

// CodePDFRender 表示 PDF 生成失败（浏览器打印失败或读不出结果）。已发布的错误码不得改名或复用。
const CodePDFRender = "export.pdf-render"

// pdfPage 是 PDF 的整页模板：A4 页面、中文字体栈、分页控制。正文片段由我们自己的渲染器产出（已转义），以 template.HTML 传入。
var pdfPage = template.Must(template.New("pdf").Parse(`<!doctype html>
<html lang="zh-CN"><head><meta charset="utf-8"><title>{{.Title}}</title>
<style>
@page { size: A4; margin: 18mm 16mm 20mm; }
html { -webkit-print-color-adjust: exact; print-color-adjust: exact; }
body { font-family: "Noto Sans CJK SC","Source Han Sans SC","WenQuanYi Zen Hei",system-ui,sans-serif;
  font-size: 10.5pt; line-height: 1.85; color: #222; margin: 0; }
h1 { font-size: 20pt; line-height: 1.35; margin: 0 0 6mm; }
h2, h3, h4 { font-size: 13pt; margin: 7mm 0 3mm; break-after: avoid; }
p { margin: 0 0 3.2mm; text-align: justify; }
img { max-width: 100%; height: auto; break-inside: avoid; margin: 3mm 0; }
blockquote { margin: 3mm 0; padding-left: 4mm; border-left: 2px solid #ccc; color: #444; }
.cover { margin-bottom: 8mm; break-inside: avoid; }
.cover img { width: 100%; display: block; }
</style></head>
<body>{{if .Cover}}<div class="cover"><img src="{{.Cover}}" alt="封面"></div>{{end}}{{.Body}}</body></html>
`))

// PDF 把作品 w 中已验收的单元导出为 A4 PDF（见 export-pdf）。render 收到待打印的完整 HTML，返回 PDF 字节；命令层注入
// 真实的浏览器打印，测试注入假实现。输出路径按 export-output 检查，写入原子进行。没有可导出的单元时报告 export.nothing-accepted。
func PDF(root string, w *work.Work, opt Options, theme map[string]string, render func(html []byte) ([]byte, error)) (*Result, []string, []workspace.Problem) {
	out := opt.Output
	if out == "" {
		out = ExportsDir + "/" + w.ID + ".pdf"
	}
	rel, p := cleanOutput(out, CodeInvalidOutput)
	if p != nil {
		return nil, nil, []workspace.Problem{*p}
	}
	res, parts, units, _, problems := collect(root, w, opt.Level)
	if len(parts) == 0 {
		return nil, nil, problems
	}
	res.Path = rel
	var occ []occurrence
	_, res.Assets, occ = planAssets(root, w, units, parts, rel)
	d, ps := disclose(root, w, units, opt.Disclosure, countAI(res.Assets), len(res.Assets))
	problems, res.Disclosure = append(problems, ps...), d

	body, _ := renderTree(strings.Join(parts, "\n\n"), theme, "", embedder(res.Assets, occ))
	var frag bytes.Buffer
	if err := page.ExecuteTemplate(&frag, "block", body); err != nil {
		panic(err) // 模板与节点种类是固定的，执行不会失败
	}
	cover, ps := pdfCover(root, res.Assets)
	problems = append(problems, ps...)
	title := pdfTitle(parts, w.ID)
	var doc bytes.Buffer
	if err := pdfPage.Execute(&doc, struct {
		Title string
		Cover template.URL
		Body  template.HTML
	}{Title: title, Cover: cover, Body: template.HTML(frag.String())}); err != nil {
		panic(err) // 模板固定，执行不会失败
	}
	out2, err := render(doc.Bytes())
	if err != nil {
		return nil, nil, append(problems, workspace.Problem{Code: CodePDFRender, Severity: workspace.SeverityError,
			Message: "生成 PDF 失败：" + err.Error()})
	}
	changed, ps := write(root, rel, out2, CodeInvalidOutput)
	if len(ps) > 0 {
		return nil, nil, append(problems, ps...)
	}
	// PDF 内嵌全部图片，不复制资产目录（与 wechat-html 不同）。
	res.Assets = nil
	res.Chars = len([]rune(string(out2)))
	return res, changed, problems
}

// pdfCover 读出导出封面并以 data: URI 内嵌；没有封面或读取失败时返回空（失败作为 warning，不阻断导出）。
func pdfCover(root string, assets []Asset) (template.URL, []workspace.Problem) {
	for _, a := range assets {
		if a.Role != illustration.RoleCover {
			continue
		}
		b := a.data
		if len(b) == 0 {
			var err error
			if b, err = os.ReadFile(filepath.Join(root, filepath.FromSlash(a.Source))); err != nil {
				return "", []workspace.Problem{{Code: CodePDFRender, Severity: workspace.SeverityWarning,
					Path: a.Source, Message: "读取封面失败，PDF 不含封面：" + err.Error()}}
			}
		}
		return template.URL("data:image/png;base64," + base64.StdEncoding.EncodeToString(b)), nil
	}
	return "", nil
}

// pdfTitle 依次取正文围栏代码块之外的第一个一级标题与作品 id 作为文档标题。
func pdfTitle(parts []string, id string) string {
	for _, part := range parts {
		for _, line := range strings.Split(part, "\n") {
			if t, ok := strings.CutPrefix(strings.TrimSpace(line), "# "); ok {
				if t = strings.TrimSpace(strings.TrimSuffix(t, "#")); t != "" {
					return t
				}
			}
		}
	}
	return id
}
