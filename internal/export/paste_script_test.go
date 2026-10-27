package export

import (
	"encoding/json"
	"os/exec"
	"strings"
	"testing"
)

// pasteScriptHarness 在 node 的 vm 沙箱中以假的 document、navigator 与 ClipboardItem 执行可粘贴页的复制脚本（pasteScript），
// 模拟点击按钮，按 mode 走异步剪贴板、剪贴板被拒后退回选区复制、没有剪贴板接口时退回选区复制（execCommand 成功或失败），
// 输出写入剪贴板的内容、选区与状态文字。
const pasteScriptHarness = `
const vm = require('node:vm');
const { script, mode, click } = JSON.parse(require('node:fs').readFileSync(0, 'utf8'));
const result = { written: null, selected: null, cleared: 0, execs: [], status: null };
const article = { id: 'wa-article', innerHTML: '<p style="x">正文</p><img src="data:image/png;base64,AA" alt="图">', innerText: '正文' };
const seg2 = { id: 'wa-seg-2', innerHTML: '<p style="x">第二段</p>', innerText: '第二段' };
const status = { textContent: '初始' };
const targets = { 'wa-article': article, 'wa-seg-2': seg2, 'wa-status': status };
const handlers = {};
const button = (copy) => ({ getAttribute: (n) => (n === 'data-copy' ? copy : null), addEventListener: (type, fn) => { if (type === 'click') handlers[copy] = fn; } });
const buttons = [button('wa-seg-2'), button('wa-article')];
const selection = { removeAllRanges: () => { result.cleared++; }, addRange: (r) => { result.selected = r.node.id; } };
const sandbox = {
  document: {
    getElementById: (id) => targets[id] || null,
    querySelectorAll: (sel) => (sel === '[data-copy]' ? buttons : []),
    createRange: () => ({ selectNodeContents(node) { this.node = node; } }),
    execCommand: (c) => { result.execs.push(c); return mode !== 'fallback-fails'; },
  },
  window: { getSelection: () => selection },
  Blob,
};
if (mode === 'rich' || mode === 'rich-denied') {
  sandbox.window.ClipboardItem = class { constructor(items) { this.items = items; } };
  sandbox.navigator = { clipboard: { write: async (items) => {
    if (mode === 'rich-denied') throw new Error('NotAllowedError');
    const it = items[0].items;
    result.written = { html: await it['text/html'].text(), htmlType: it['text/html'].type, text: await it['text/plain'].text(), textType: it['text/plain'].type };
  } } };
} else {
  sandbox.navigator = {};
}
sandbox.ClipboardItem = sandbox.window.ClipboardItem;
vm.runInNewContext(script, sandbox);
const target = buttons.find((b) => b.getAttribute('data-copy') === click);
handlers[click]({ currentTarget: target });
setTimeout(() => { result.status = status.textContent; process.stdout.write(JSON.stringify(result)); }, 20);
`

type scriptRun struct {
	Written  *struct{ HTML, HTMLType, Text, TextType string } `json:"written"`
	Selected *string                                          `json:"selected"`
	Cleared  int                                              `json:"cleared"`
	Execs    []string                                         `json:"execs"`
	Status   string                                           `json:"status"`
}

// 页面的复制按钮（data-copy 指向要复制的容器）：有异步剪贴板接口时以 text/html 与 text/plain 写入该容器；接口被拒或不可用时退回为选中正文容器后
// execCommand('copy')；页面显示复制结果（paste-html-export）。
func TestPasteScriptBehavior(t *testing.T) {
	node, err := exec.LookPath("node")
	if err != nil {
		t.Skip("node 不可用：复制脚本的行为测试需要 node")
	}
	runOn := func(mode, click string) scriptRun {
		t.Helper()
		in, _ := json.Marshal(map[string]string{"script": pasteScript, "mode": mode, "click": click})
		cmd := exec.Command(node, "-e", pasteScriptHarness)
		cmd.Stdin = strings.NewReader(string(in))
		out, err := cmd.Output()
		if err != nil {
			t.Fatalf("%s: %v\n%s", mode, err, out)
		}
		var r scriptRun
		if err := json.Unmarshal(out, &r); err != nil {
			t.Fatalf("%s: %v\n%s", mode, err, out)
		}
		return r
	}
	run := func(mode string) scriptRun { return runOn(mode, "wa-article") }
	const copied = "已复制，可以到公众号编辑器或在线文档中粘贴"
	r := run("rich")
	if r.Written == nil || r.Written.HTML != `<p style="x">正文</p><img src="data:image/png;base64,AA" alt="图">` || r.Written.HTMLType != "text/html" ||
		r.Written.Text != "正文" || r.Written.TextType != "text/plain" || len(r.Execs) != 0 || r.Status != copied {
		t.Errorf("rich: %+v", r)
	}
	for _, mode := range []string{"rich-denied", "no-clipboard"} {
		r := run(mode)
		if r.Written != nil || r.Selected == nil || *r.Selected != "wa-article" || strings.Join(r.Execs, ",") != "copy" || r.Cleared != 2 || r.Status != copied {
			t.Errorf("%s: %+v", mode, r)
		}
	}
	if r := run("fallback-fails"); !strings.HasPrefix(r.Status, "复制失败") || strings.Join(r.Execs, ",") != "copy" {
		t.Errorf("fallback-fails: %+v", r)
	}
	// 多段时每个按钮只复制自己的段（paste-segments）。
	if r := runOn("rich", "wa-seg-2"); r.Written == nil || r.Written.HTML != `<p style="x">第二段</p>` || r.Written.Text != "第二段" {
		t.Errorf("segment: %+v", r)
	}
	if r := runOn("no-clipboard", "wa-seg-2"); r.Selected == nil || *r.Selected != "wa-seg-2" {
		t.Errorf("segment fallback: %+v", r)
	}
}
