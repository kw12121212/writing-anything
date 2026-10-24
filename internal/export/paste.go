package export

import (
	"bytes"
	"crypto/sha256"
	"encoding/base64"
	"html/template"
	"strings"

	"github.com/kw12121212/writing-anything/internal/illustration"
	"github.com/kw12121212/writing-anything/internal/work"
	"github.com/kw12121212/writing-anything/internal/workspace"
)

// 可粘贴页（Paste-ready Page，paste-html）：正文按公众号 HTML 的规则渲染，可用的本地配图以受信 data URI 内嵌为图像，外包一个带「复制图文」
// 按钮的完整 HTML 文档。页面始终不含 AI 参与说明与封面图；资产目录与配图清单照常写出，作为目标编辑器不接受内嵌图片时的兜底。

// FormatPaste 是可粘贴页的导出格式。
const FormatPaste = "paste-html"

// PasteEmbedBudget 是可粘贴页内嵌配图的总字节数上限（32 MiB，与阅读页一致），按内嵌后的字节累计（重新编码的图按编码结果计）。
const PasteEmbedBudget = 32 << 20

// PasteSegmentImages 是可粘贴正文每段最多内嵌的图片数：公众号图文编辑器一次粘贴超过 7 张 base64 图片时会丢掉全部图片
// （2026-10-23 实测），超过时正文按段落边界分段，作者逐段复制。
const PasteSegmentImages = 7

// pasteScript 是可粘贴页唯一的脚本，内容固定、不内插数据，由 Content-Security-Policy 按哈希放行（workspace-artifacts/generated-html）：
// 每个带 data-copy 的按钮复制 id 为该属性值的容器（单段时为整个正文容器，多段时为各段或全文），以 text/html 与 text/plain 写入剪贴板，
// 剪贴板接口不可用或被拒绝时退回为选中该容器后复制。
const pasteScript = `(function () {
  var status = document.getElementById("wa-status");
  var copied = "已复制，可以到公众号编辑器或在线文档中粘贴";
  function fallback(target) {
    var range = document.createRange();
    range.selectNodeContents(target);
    var sel = window.getSelection();
    sel.removeAllRanges();
    sel.addRange(range);
    var ok = false;
    try { ok = document.execCommand("copy"); } catch (e) { ok = false; }
    sel.removeAllRanges();
    status.textContent = ok ? copied : "复制失败：请全选正文后按 Ctrl+C（macOS 为 Command+C）复制";
  }
  function copy(target) {
    if (navigator.clipboard && window.ClipboardItem) {
      var item = new ClipboardItem({
        "text/html": new Blob([target.innerHTML], { type: "text/html" }),
        "text/plain": new Blob([target.innerText], { type: "text/plain" })
      });
      navigator.clipboard.write([item]).then(function () {
        status.textContent = copied;
      }, function () { fallback(target); });
    } else {
      fallback(target);
    }
  }
  var buttons = document.querySelectorAll("[data-copy]");
  for (var i = 0; i < buttons.length; i++) {
    buttons[i].addEventListener("click", function (event) {
      var target = document.getElementById(event.currentTarget.getAttribute("data-copy"));
      if (target) copy(target);
    });
  }
})();`

// pasteCSP 是可粘贴页的 Content-Security-Policy：只放行内联样式、data: 图片与哈希一致的复制脚本。
var pasteCSP = func() string {
	sum := sha256.Sum256([]byte(pasteScript))
	return "default-src 'none'; style-src 'unsafe-inline'; img-src data:; script-src 'sha256-" + base64.StdEncoding.EncodeToString(sum[:]) +
		"'; base-uri 'none'; form-action 'none'"
}()

var pastePage = template.Must(template.Must(page.Clone()).Funcs(template.FuncMap{
	"script": func() template.JS { return template.JS(pasteScript) },
	"csp":    func() string { return pasteCSP },
}).Parse(`{{define "paste" -}}
<!DOCTYPE html>
<html lang="zh-CN">
<head>
<meta charset="utf-8">
<meta name="viewport" content="width=device-width, initial-scale=1">
<meta http-equiv="Content-Security-Policy" content="{{csp}}">
<title>{{.Title}}</title>
</head>
<body style="margin: 0; background: #f5f5f5">
<div style="max-width: 720px; margin: 0 auto; padding: 16px; background: #fff">
<div style="user-select: none; -webkit-user-select: none; padding: 8px 0 16px; border-bottom: 1px solid #eee; margin-bottom: 16px; font-size: 14px; color: #666">
{{if eq (len .Segments) 1 -}}
<button id="wa-copy" data-copy="wa-article" type="button" style="font-size: 14px; padding: 6px 16px; cursor: pointer">复制图文</button>
<span id="wa-status" style="margin-left: 12px">点「复制图文」后到公众号编辑器或在线文档中粘贴；也可以全选正文后复制。</span>
{{- else -}}
{{range .Segments}}<button data-copy="{{.ID}}" type="button" style="font-size: 14px; padding: 6px 12px; margin: 0 8px 8px 0; cursor: pointer">{{.Label}}</button>{{end}}<button data-copy="wa-article" type="button" style="font-size: 14px; padding: 6px 12px; margin: 0 8px 8px 0; cursor: pointer">复制全文</button>
<div id="wa-status">公众号每次粘贴最多 7 张图：请依次点「复制第 n 段」，逐段粘贴到公众号；粘贴到飞书等在线文档时可以用「复制全文」。</div>
{{- end}}
</div>
<div id="wa-article">{{if eq (len .Segments) 1}}{{template "block" (index .Segments 0).Root}}{{else}}{{range .Segments}}<div id="{{.ID}}">{{template "block" .Root}}</div>{{end}}{{end}}</div>
</div>
<script>{{script}}</script>
</body>
</html>
{{end}}`))

// PasteHTML 把作品 w 中已验收的单元导出为可粘贴页（paste-html-export），theme 为合并后的样式主题。opt.Print 为 true 时只读
// （paste-html-print）：不检查也不写输出路径、资产目录与配图清单，正文片段放在 Result.HTML 中。没有可导出的单元时报告
// export.nothing-accepted。
func PasteHTML(root string, w *work.Work, opt Options, theme map[string]string) (*Result, []string, []workspace.Problem) {
	out := opt.Output
	if out == "" {
		out = ExportsDir + "/" + w.ID + ".paste.html"
	}
	rel := out
	if !opt.Print {
		var p *workspace.Problem
		if rel, p = cleanOutput(out, CodeInvalidOutput); p != nil {
			return nil, nil, []workspace.Problem{*p}
		}
	}
	res, parts, units, _, problems := collect(root, w, opt.Level)
	if len(parts) == 0 {
		return nil, nil, problems
	}
	var occ []occurrence
	_, res.Assets, occ = planAssets(root, w, units, parts, rel)
	// AI 参与说明照常计算并报告，但不写进页面（paste-html-export）。
	d, ps := disclose(root, w, units, opt.Disclosure, countAI(res.Assets), len(res.Assets))
	problems, res.Disclosure = append(problems, ps...), d
	tree, images := renderTree(strings.Join(parts, "\n\n"), theme, "", embedder(res.Assets, occ))
	res.Images = images
	segs := segment(tree)
	var full strings.Builder
	for _, sg := range segs {
		var frag bytes.Buffer
		if err := page.ExecuteTemplate(&frag, "block", sg.Root); err != nil {
			panic(err) // 模板与节点种类是固定的，执行不会失败
		}
		res.Segments = append(res.Segments, frag.String())
		full.WriteString(frag.String())
	}
	if opt.Print {
		res.HTML, res.Assets = full.String(), nil
		res.Chars = len([]rune(res.HTML))
		return res, nil, problems
	}
	res.Segments = nil
	res.Path = rel
	title := w.ID
	if h := FirstHeading(parts[0]); h != "" {
		title = h
	}
	var doc bytes.Buffer
	if err := pastePage.ExecuteTemplate(&doc, "paste", struct {
		Title    string
		Segments []pasteSegment
	}{title, segs}); err != nil {
		panic(err)
	}
	changed, ps, ok := writeWithImages(root, rel, doc.Bytes(), res, occ)
	if problems = append(problems, ps...); !ok {
		return nil, changed, problems
	}
	res.Chars = len([]rune(doc.String()))
	return res, changed, problems
}

// pasteSegment 是可粘贴正文的一段：Root 为该段的根 section，ID 为页面中该段容器的 id，Label 为复制按钮的文字。
type pasteSegment struct {
	ID    string
	Label string
	Root  *node
}

// segment 把渲染树按内嵌图像切成每段至多 PasteSegmentImages 张图的分段：根 section 的顶层块依次归入当前段，加入某块会使当前段超过
// 上限时从该块开始新段，单个块超过上限时独占一段；没有图像的块总是归入当前段。每段是一个与根同样式的 section，参考链接等
// 没有图像的尾部块留在最后一段。
// 只有一段时该段与原渲染树的输出相同。
func segment(root *node) []pasteSegment {
	var segs []pasteSegment
	cur, n, first := &node{Kind: root.Kind, Style: root.Style}, 0, 1
	flush := func() {
		label := "复制第 " + itoa(len(segs)+1) + " 段（图 " + itoa(first) + "–" + itoa(first+n-1) + "）"
		segs = append(segs, pasteSegment{ID: "wa-seg-" + itoa(len(segs)+1), Label: label, Root: cur})
		first += n
	}
	for _, ch := range root.Children {
		k := countImages(ch)
		if k > 0 && n > 0 && n+k > PasteSegmentImages {
			flush()
			cur, n = &node{Kind: root.Kind, Style: root.Style}, 0
		}
		cur.Children = append(cur.Children, ch)
		n += k
	}
	flush()
	return segs
}

// countImages 返回节点子树中的内嵌图像数。
func countImages(n *node) int {
	k := 0
	if n.Kind == "image" {
		k++
	}
	for _, ch := range n.Children {
		k += countImages(ch)
	}
	return k
}

// embedder 返回可粘贴页的内嵌判定：按出现顺序把正文中的图片对应到 planAssets 的图片（occ），独占一段、对应已复制的正文配图（不是封面）、
// 能取得内嵌字节（见 pasteImage：PNG/JPEG 原样，超过 1 MiB 的重新编码为 JPEG）且内嵌总量不超过 PasteEmbedBudget 时，返回受信
// data URI。同一张图出现多次只计一次预算。只对经 illustration.ReadImage 校验（文件名与格式一致、SHA-256 与登记一致）的字节，或由它们
// 重新编码的字节构造 data URI（trusted-data-uri）。
func embedder(assets []Asset, occ []occurrence) func(string, bool) (template.URL, bool) {
	// 按地址索引 occ 的下标（升序）：每次取 cursor 之后第一个同地址的图片，每个下标至多被跳过一次，总耗时与图片数成线性。
	bySrc := map[string][]int{}
	for j, o := range occ {
		bySrc[o.src] = append(bySrc[o.src], j)
	}
	cursor, used := 0, 0
	uris := map[int]template.URL{}
	rejected := map[int]bool{}
	return func(src string, block bool) (template.URL, bool) {
		if strings.HasPrefix(src, "<") && strings.HasSuffix(src, ">") {
			src = src[1 : len(src)-1] // 公众号渲染保留尖括号包住的地址
		}
		a := -1
		idx := bySrc[src]
		for len(idx) > 0 && idx[0] < cursor {
			idx = idx[1:]
		}
		if len(idx) > 0 {
			a, cursor = occ[idx[0]].asset, idx[0]+1
			idx = idx[1:]
		}
		bySrc[src] = idx
		if a < 0 || !block || assets[a].Role == illustration.RoleCover {
			return "", false
		}
		if uri, ok := uris[a]; ok {
			return uri, true
		}
		if rejected[a] {
			return "", false
		}
		// 超过 PasteImageMaxBytes 的配图重新编码为 JPEG（同一张图只编码一次）；预算按内嵌后的字节累计。
		data, format, ok := pasteImage(assets[a].data)
		if !ok || used+len(data) > PasteEmbedBudget {
			rejected[a] = true
			return "", false
		}
		used += len(data)
		uris[a] = template.URL("data:" + illustration.MIME(format) + ";base64," + base64.StdEncoding.EncodeToString(data))
		return uris[a], true
	}
}
