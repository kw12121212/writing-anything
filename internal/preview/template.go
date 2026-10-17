package preview

// pageScript 是 serve 模式内联的脚本：读取选区并向本地服务提交意见，提交作者在待确认队列中的确认与对待复核确认的
// 批量复核；令牌取自页面地址。内容固定，Content-Security-Policy 按其 SHA-256 放行（ScriptCSP）。
const pageScript = `
(function () {
  var token = new URLSearchParams(location.search).get('token');
  var quote = '';
  var $ = function (id) { return document.getElementById(id); };
  function show() { $('quote').textContent = quote ? '「' + quote + '」' : '（未选中文字，意见针对整个单元）'; }
  document.addEventListener('mouseup', function () {
    var sel = window.getSelection();
    if (!sel.anchorNode) { return; }
    var el = sel.anchorNode.nodeType === 1 ? sel.anchorNode : sel.anchorNode.parentElement;
    var text = el && el.closest('.text');
    if (!text) { return; }
    var focus = sel.focusNode && (sel.focusNode.nodeType === 1 ? sel.focusNode : sel.focusNode.parentElement);
    if (!focus || focus.closest('.text') !== text) { $('status').textContent = '请在一段之内选择文字'; return; }
    quote = sel.toString();
    $('unit').value = text.closest('section').dataset.unit;
    show();
  });
  document.querySelectorAll('#panel button').forEach(function (b) {
    b.addEventListener('click', function () {
      var body = { unit: $('unit').value, quote: quote, text: $('text').value, kind: $('kind').value, mark: b.dataset.mark };
      fetch('/feedback', { method: 'POST', headers: { 'Content-Type': 'application/json', 'X-WA-Token': token }, body: JSON.stringify(body) })
        .then(function (r) { return r.json(); })
        .then(function (res) {
          if (res.valid) {
            $('status').textContent = '已保存作者反馈 ' + res.feedback.id;
            $('text').value = ''; quote = ''; show();
          } else {
            $('status').textContent = res.diagnostics.map(function (d) { return d.message; }).join('；');
          }
        })
        .catch(function (e) { $('status').textContent = '提交失败：' + e; });
    });
  });
  function post(path, body, out, ok) {
    fetch(path, { method: 'POST', headers: { 'Content-Type': 'application/json', 'X-WA-Token': token }, body: JSON.stringify(body) })
      .then(function (r) { return r.json(); })
      .then(function (res) {
        out.textContent = res.valid ? ok(res) : res.diagnostics.map(function (d) { return d.message; }).join('；');
      })
      .catch(function (e) { out.textContent = '提交失败：' + e; });
  }
  document.querySelectorAll('.confirm button').forEach(function (b) {
    b.addEventListener('click', function () {
      var item = b.closest('.confirm');
      var field = function (c) { var el = item.querySelector(c); return el ? el.value : ''; };
      var body = { kind: item.dataset.kind, unit: item.dataset.unit, id: item.dataset.id, action: b.dataset.action,
        option: field('.option'), answer: field('.answer'), confirmation: field('.summary') };
      post('/confirm', body, item.querySelector('.result'), function () { b.disabled = true; return '已记录作者确认（渠道 preview）'; });
    });
  });
  var review = $('review-submit');
  if (review) {
    review.addEventListener('click', function () {
      var keys = [];
      document.querySelectorAll('#author-review input[type=checkbox]:checked').forEach(function (c) { keys.push(c.value); });
      post('/author-review', { keys: keys }, $('review-result'), function (res) {
        review.disabled = true;
        return '已复核 ' + res.authorReview.reviewed.length + ' 项（渠道 preview）';
      });
    });
  }
})();
`

// pageHTML 是阅读页的模板：样式内联，不引用外部资源；静态页不含脚本，出处着色由复选框与 CSS 兄弟选择器切换。serve 模式
// 内联的脚本只读取选区并向本地服务提交意见，令牌取自页面地址。
const pageHTML = `<!DOCTYPE html>
<html lang="zh-CN">
<head>
<meta charset="utf-8">
<meta name="viewport" content="width=device-width, initial-scale=1">
{{- if not .Serve}}
<meta http-equiv="Content-Security-Policy" content="default-src 'none'; style-src 'unsafe-inline'">
{{- end}}
<meta name="referrer" content="no-referrer">
<title>阅读页：{{.Title}}</title>
<style>
body { margin: 0; font: 17px/1.8 -apple-system, "PingFang SC", "Noto Sans CJK SC", "Microsoft YaHei", sans-serif; color: #222; background: #fafafa; }
main { max-width: 760px; margin: 0 auto; padding: 1em 1.2em 16em; }
.bar { position: sticky; top: 0; background: #fff; border-bottom: 1px solid #ddd; padding: .4em 1.2em; font-size: 14px; z-index: 1; }
.legend span { display: inline-block; padding: 0 .5em; margin-left: .5em; border-radius: 3px; }
h2 { font-size: 18px; border-bottom: 1px solid #ddd; margin-top: 2em; }
h2 small { font-weight: normal; color: #888; margin-left: .5em; }
.para { margin: .8em 0; padding: .2em .6em; border-left: 3px solid transparent; }
.meta { font-size: 12px; color: #999; user-select: none; -webkit-user-select: none; }
.text { white-space: pre-wrap; word-break: break-word; }
.note, .empty { color: #8a5a00; background: #fff8e1; padding: .3em .6em; font-size: 14px; }
.finding { font-size: 14px; margin: .3em 0 .3em 1em; padding: .2em .6em; border-left: 3px solid #bbb; background: #f3f3f3; user-select: none; -webkit-user-select: none; }
.finding.error { border-color: #d32f2f; }
.finding.warning { border-color: #f9a825; }
.finding.review { border-color: #1565c0; }
.finding .label { font-weight: bold; margin-right: .5em; }
.finding .suggestion { display: block; color: #2e7d32; }
#color:checked ~ main .o-human { border-color: #43a047; }
#color:checked ~ main .o-agent { border-color: #fb8c00; background: #fff3e0; }
#color:checked ~ main .o-agent-draft-human-edited { border-color: #8e24aa; background: #f8eefa; }
.legend .o-human { border-left: 3px solid #43a047; }
.legend .o-agent { border-left: 3px solid #fb8c00; }
.legend .o-agent-draft-human-edited { border-left: 3px solid #8e24aa; }
#panel { position: fixed; bottom: 0; left: 0; right: 0; background: #fff; border-top: 1px solid #ccc; padding: .6em 1.2em; font-size: 14px; }
#panel textarea { width: 100%; box-sizing: border-box; height: 3.5em; }
#panel #quote { color: #555; }
#panel button { margin-right: .4em; }
.queue { font-size: 14px; background: #fff; border: 1px solid #ddd; padding: .4em 1em; margin: 1em 0; }
.queue h2 { margin-top: .4em; }
.confirm, .queue label { display: block; border-top: 1px solid #eee; padding: .4em 0; }
.confirm .detail { color: #555; white-space: pre-wrap; }
.confirm input.summary, .confirm input.answer { width: 60%; }
.result, #review-result { color: #1565c0; margin-left: .5em; }
</style>
</head>
<body>
<input type="checkbox" id="color" checked>
<div class="bar"><label for="color">按出处着色</label><span class="legend"><span class="o-human">作者</span><span class="o-agent">Agent</span><span class="o-agent-draft-human-edited">Agent 起草、作者修改</span></span></div>
<main>
<h1>阅读页：{{.Title}}</h1>
{{if .Failures}}<h2>最近失败的命令（Audit Log）<small>来自审计日志的最近失败，只读展示；请读当时的诊断修复后重跑</small></h2>
<ul class="findings">
{{range .Failures}}<li>{{.At}}：{{.Command}}（exit {{.Exit}}）</li>
{{end}}</ul>
{{end}}
{{- if .Serve}}
<section class="queue" id="confirm-queue">
<h2>待作者确认（Pending Author Confirmation）<small>确认由作者本人在此填写原话摘要后提交，确认渠道（Confirmation Channel）记为 preview</small></h2>
{{- range .Confirms}}
<div class="confirm" data-kind="{{.Kind}}" data-unit="{{.Unit}}" data-id="{{.ID}}">
<div class="label">{{.Label}}</div>
{{- if .Detail}}<div class="detail">{{.Detail}}</div>{{end}}
{{- if .Options}}<div>选项 <select class="option">{{$rec := .Recommend}}{{range .Options}}<option value="{{.ID}}"{{if eq .ID $rec}} selected{{end}}>{{.ID}}. {{.Text}}</option>{{end}}</select>{{if .Recommend}}（推荐 {{.Recommend}}）{{end}}</div>{{end}}
{{- if .NeedsAnswer}}<div><input class="answer" placeholder="作者的答复"></div>{{end}}
<div><input class="summary" placeholder="作者原话摘要（必填）"> <button type="button" data-action="confirm">确认</button>{{if .CanReject}}<button type="button" data-action="reject">拒绝</button>{{end}}<span class="result" role="status"></span></div>
</div>
{{- else}}
<p>没有待作者确认的事项</p>
{{- end}}
</section>
{{- if .Strict}}
<section class="queue" id="author-review">
<h2>待作者复核（Pending Author Review）<small>strict 关卡（Gate）下经命令行渠道（cli）写入的确认，组稿前须由作者在此复核</small></h2>
{{- range .Reviews}}
<label><input type="checkbox" value="{{.Key}}" checked> {{.Label}}</label>
{{- else}}
<p>没有待作者复核的确认</p>
{{- end}}
{{- if .Reviews}}<div><button type="button" id="review-submit">复核所选</button><span id="review-result" role="status"></span></div>{{end}}
</section>
{{- end}}
{{- end}}
{{- range .Units}}
<section class="unit" data-unit="{{.Ref}}">
<h2>{{.Ref}}<small>阶段：{{.Stage}}</small></h2>
{{- range .Notes}}
<p class="note">{{.}}</p>
{{- end}}
{{- if and (not .HasDraft) (not .Notes)}}
<p class="empty">还没有正文</p>
{{- end}}
{{- range .Global}}{{template "finding" .}}{{end}}
{{- range .Paragraphs}}
<div class="para o-{{.Origin}}" data-index="{{.Index}}">
<div class="meta">第 {{.Index}} 段 · 第 {{.Line}} 行 · {{originName .Origin}}</div>
<div class="text">{{.Text}}</div>
{{- range .Findings}}{{template "finding" .}}{{end}}
</div>
{{- end}}
</section>
{{- end}}
</main>
{{- if .Serve}}
<aside id="panel">
<div>单元 <select id="unit">{{range .Units}}<option>{{.Ref}}</option>{{end}}</select> 引用：<span id="quote">（未选中文字，意见针对整个单元）</span></div>
<textarea id="text" placeholder="选中正文中的文字后写下意见"></textarea>
<div>类型 <select id="kind"><option value="suggestion">建议</option><option value="must-fix">必须修改</option><option value="question">疑问</option><option value="praise">肯定</option></select>
<button type="button" data-mark="">提交意见</button><button type="button" data-mark="stuck">阅读卡点</button><button type="button" data-mark="good">好</button><button type="button" data-mark="question">疑问</button>
<span id="status" role="status"></span></div>
</aside>
<script>{{script}}</script>
{{- end}}
</body>
</html>
{{define "finding"}}
<div class="finding {{.Kind}} {{.Severity}}"><span class="label">{{.Label}}</span>{{.Message}}{{if .Suggestion}}<span class="suggestion">建议：{{.Suggestion}}</span>{{end}}</div>
{{- end}}
`
