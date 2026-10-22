package cli

import (
	"context"
	"crypto/rand"
	"crypto/subtle"
	"encoding/hex"
	"encoding/json"
	"fmt"
	"io"
	"log"
	"net"
	"net/http"
	"slices"
	"strconv"
	"strings"
	"sync"
	"time"

	"github.com/kw12121212/writing-anything/internal/draft"
	"github.com/kw12121212/writing-anything/internal/export"
	"github.com/kw12121212/writing-anything/internal/feedback"
	"github.com/kw12121212/writing-anything/internal/illustration"
	"github.com/kw12121212/writing-anything/internal/lint"
	"github.com/kw12121212/writing-anything/internal/preview"
	"github.com/kw12121212/writing-anything/internal/work"
	"github.com/kw12121212/writing-anything/internal/workspace"
)

var previewCommand = &command{
	name: "preview",
	summary: "生成单元（Unit）及其子单元的阅读页（Reading View）：逐段展示正文与出处（Provenance）、文本检查（Lint）与审稿（Review）发现项；" +
		"默认写出可离线打开的静态 HTML，加 --serve 时在 127.0.0.1 上启动需要随机令牌（Token）才能访问的本地阅读页，作者选中文字提交的意见即时保存为作者反馈（Feedback），GET /page.json 以 JSON 给出同一时刻的阅读页数据，供其他界面经同源代理呈现；" +
		"本地阅读页另列出该作品待作者确认的验收、决策、规划步骤、状态与知情提议和卷末检查点，作者在页面上确认即写入确认渠道（Confirmation Channel）为 preview 的确认记录；" +
		"strict 关卡下还列出待作者复核（Pending Author Review）的命令行渠道确认，供作者组稿前批量复核。这是可审计的流程约束而非安全隔离：同机同账户的 Agent 能读到令牌",
	args: []argSpec{{"unit", unitRefHelp, true}},
	flags: []flagSpec{dirFlag,
		{"output", flagString, "静态阅读页的输出文件，相对 --dir；省略时为 .writing-anything/exports/<work>.preview.html（子单元为 <work>.<unit-id>.preview.html）；不能与 --serve 同时使用"},
		{"serve", flagBool, "启动本地阅读页（只监听 127.0.0.1），一直运行到命令被中断或保存 --max 条反馈"},
		{"port", flagString, "--serve 监听的端口，1 到 65535；省略时由系统分配"},
		{"max", flagString, "--serve 保存指定条数的反馈后结束；省略时一直运行"}},
	outputKeys: []outputKey{
		{"preview", "阅读页：unit、units（按单元树先序列出的单元引用）、path（静态阅读页的路径，--serve 时为 null）与 url（--serve 时的页面地址，含令牌；否则为 null）；失败时为 null"},
		{"feedback", "--serve 时每保存一条反馈输出一行，含 " + feedbackViewHelp + "；其他行为 null"},
		{"confirmation", "--serve 时作者在阅读页每记录一次确认输出一行，含 kind（acceptance、decision、plan、state、knowledge 或 volume）、unit、action（confirm 或 reject）与 channel（preview）；id 只在 decision、plan、state 与 knowledge 出现（各自的条目 id），acceptance 与 volume 没有条目 id、不含 id 字段；其他行为 null"},
		{"authorReview", "--serve 时作者在阅读页每批量复核一次输出一行，含 reviewed（被复核的待作者复核项，每项含 key、kind、work、ref、path、confirmation 与 confirmedAt）与 channel（preview）；其他行为 null"},
	},
	exitCodes: standardExitCodes,
	run:       runPreview,
}

type previewView struct {
	Unit  string   `json:"unit"`
	Units []string `json:"units"`
	Path  *string  `json:"path"`
	URL   *string  `json:"url"`
}

type previewResult struct {
	Result
	Preview      *previewView      `json:"preview"`
	Feedback     *feedbackView     `json:"feedback"`
	Confirmation *confirmedView    `json:"confirmation"`
	AuthorReview *authorReviewView `json:"authorReview"`
}

// previewMaxBody 是 POST /feedback 请求体的字节数上限。
const previewMaxBody = 64 << 10

// previewPath 返回单元 u 默认的静态阅读页路径。
func previewPath(w *work.Work, u *work.Unit) string {
	name := w.ID
	if u.Parent != nil {
		name += "." + u.ID
	}
	return export.ExportsDir + "/" + name + ".preview.html"
}

// previewFlags 检查参数组合与取值；返回端口（0 为由系统分配）与 --max（0 为不限）。
func previewFlags(inv *invocation) (port, limit int, p *workspace.Problem) {
	serve := inv.flag("serve")
	for _, name := range []string{"port", "max"} {
		if v, ok := inv.values[name]; ok && !serve {
			q := invalidValue(name, v, "--"+name+" 只用于 --serve")
			return 0, 0, &q
		}
	}
	if v, ok := inv.values["output"]; ok && serve {
		q := invalidValue("output", v, "--output 只用于静态阅读页，不能与 --serve 同时使用")
		return 0, 0, &q
	}
	if v, ok := inv.values["port"]; ok {
		n, err := strconv.Atoi(v)
		if err != nil || n < 1 || n > 65535 {
			q := invalidValue("port", v, "应为 1 到 65535 的整数")
			return 0, 0, &q
		}
		port = n
	}
	limit, p = positive(inv, "max", 0)
	return port, limit, p
}

func runPreview(inv *invocation) int {
	dir := projectDir(inv)
	res := &previewResult{}
	problems := workspace.Require(dir)
	port, limit, p := previewFlags(inv)
	if problems == nil && p != nil {
		problems = []workspace.Problem{*p}
	}
	var w *work.Work
	var u *work.Unit
	if problems == nil {
		w, u, problems = work.Resolve(dir, inv.args[0])
	}
	if u != nil {
		if inv.flag("serve") {
			return servePreview(inv, dir, w, u, port, limit)
		}
		units := preview.Subtree(u)
		sp := preview.Build(dir, w, units, unitLint(dir, w))
		sp.Failures = auditFailureSummary(dir)
		data, err := preview.Render(sp)
		if err != nil {
			return writeFailed(inv.stderr, err)
		}
		out := inv.values["output"]
		if out == "" {
			out = previewPath(w, u)
		}
		var rel string
		var changed []string
		if rel, changed, problems = export.WriteOutput(dir, out, data, preview.CodeInvalidOutput); len(problems) == 0 {
			res.Preview, res.ChangedPaths = &previewView{Unit: u.Ref, Units: refs(units), Path: &rel}, changed
		}
	}
	res.Diagnostics = diagnostics(problems, dir)
	helpNext(res.Diagnostics, "preview", []string{codeInvalidValue, preview.CodeInvalidOutput})
	return finish(inv, res, func(out io.Writer) error {
		if res.Preview == nil {
			return nil
		}
		_, err := fmt.Fprintf(out, "已生成单元 %s 的阅读页（Reading View）：%s\n", res.Preview.Unit, *res.Preview.Path)
		return err
	})
}

func refs(units []*work.Unit) []string {
	out := []string{}
	for _, u := range units {
		out = append(out, u.Ref)
	}
	return out
}

// unitLint 返回对作品 w 的单元运行文本检查的函数，结果与 wa-cli lint 相同。
func unitLint(dir string, w *work.Work) preview.LintFunc {
	deps := workLintDeps(dir, w)
	return func(u *work.Unit) (*lint.Result, []workspace.Problem) {
		out, _, _, ps := runUnitLintWith(dir, w, u, deps)
		return out, ps
	}
}

// previewServer 是本地阅读页服务：只监听 127.0.0.1，每个请求都校验 Host 头与令牌（cli-contract/loopback-only-server）。
type previewServer struct {
	inv   *invocation
	dir   string
	ref   string // 阅读页的单元引用
	token string
	hosts []string // 接受的 Host 头
	view  *previewView
	limit int

	mu      sync.Mutex // 串行化保存反馈与写标准输出
	saved   int
	err     error
	done    chan struct{}
	closing sync.Once
}

func servePreview(inv *invocation, dir string, w *work.Work, u *work.Unit, port, limit int) int {
	ln, err := net.Listen("tcp", net.JoinHostPort("127.0.0.1", strconv.Itoa(port)))
	if err != nil {
		res := &previewResult{}
		res.Diagnostics = diagnostics([]workspace.Problem{{Code: preview.CodeListenFailed, Severity: workspace.SeverityError,
			Message: fmt.Sprintf("无法在 127.0.0.1:%d 上启动阅读页：%v；请换一个 --port 或省略它由系统分配", port, err)}}, dir)
		helpNext(res.Diagnostics, "preview", []string{preview.CodeListenFailed})
		return finish(inv, res, func(io.Writer) error { return nil })
	}
	key := make([]byte, 32)
	if _, err := rand.Read(key); err != nil {
		ln.Close()
		return writeFailed(inv.stderr, err)
	}
	// 地址取自实际监听的套接字，测试据此确认只监听回环地址（cli-contract/loopback-only-server）。
	addr := ln.Addr().(*net.TCPAddr)
	hostport := net.JoinHostPort(addr.IP.String(), strconv.Itoa(addr.Port))
	s := &previewServer{inv: inv, dir: dir, ref: u.Ref, token: hex.EncodeToString(key),
		hosts: []string{hostport, "localhost:" + strconv.Itoa(addr.Port)}, limit: limit, done: make(chan struct{})}
	if addr.Port == 80 {
		// 浏览器对默认端口不写端口号。
		s.hosts = append(s.hosts, addr.IP.String(), "localhost")
	}
	url := "http://" + hostport + "/?token=" + s.token
	s.view = &previewView{Unit: u.Ref, Units: refs(preview.Subtree(u)), URL: &url}
	if err := s.emit(&previewResult{Preview: s.view}, "阅读页（Reading View）："+url+"（按 Ctrl+C 结束）\n"); err != nil {
		ln.Close()
		return writeFailed(inv.stderr, err)
	}
	srv := &http.Server{Handler: s, ReadHeaderTimeout: 10 * time.Second, ErrorLog: log.New(inv.stderr, "", 0)}
	served := make(chan error, 1)
	go func() { served <- srv.Serve(ln) }()
	select {
	case <-s.done:
		ctx, cancel := context.WithTimeout(context.Background(), 5*time.Second)
		defer cancel()
		srv.Shutdown(ctx) // 等最后一个响应发出后关闭
	case err = <-served:
		return writeFailed(inv.stderr, err)
	}
	if s.err != nil {
		return writeFailed(inv.stderr, s.err)
	}
	return exitOK
}

// emit 输出一行：--json 时为输出信封，否则为 text。调用方持有 s.mu 或尚未启动服务。
func (s *previewServer) emit(res *previewResult, text string) error {
	if s.inv.json() {
		return writeJSON(s.inv.stdout, res)
	}
	_, err := io.WriteString(s.inv.stdout, text)
	return err
}

// authorized 校验 Host 头与令牌：GET 用查询参数 token，其他方法用请求头 X-WA-Token；按常量时间比较。
func (s *previewServer) authorized(r *http.Request) bool {
	if !slices.Contains(s.hosts, r.Host) {
		return false
	}
	given := r.Header.Get("X-WA-Token")
	if r.Method == http.MethodGet || r.Method == http.MethodHead {
		given = r.URL.Query().Get("token")
	}
	return subtle.ConstantTimeCompare([]byte(given), []byte(s.token)) == 1
}

func (s *previewServer) ServeHTTP(rw http.ResponseWriter, r *http.Request) {
	h := rw.Header()
	h.Set("Cache-Control", "no-store")
	h.Set("Referrer-Policy", "no-referrer")
	h.Set("X-Frame-Options", "DENY")
	h.Set("X-Content-Type-Options", "nosniff")
	h.Set("Content-Security-Policy", preview.ScriptCSP)
	if !s.authorized(r) {
		http.Error(rw, "forbidden", http.StatusForbidden)
		return
	}
	switch r.URL.Path {
	case "/", "/page.json":
		if r.Method != http.MethodGet && r.Method != http.MethodHead {
			http.Error(rw, "method not allowed", http.StatusMethodNotAllowed)
			return
		}
		s.page(rw, r.URL.Path == "/page.json")
	case "/illustration":
		if r.Method != http.MethodGet && r.Method != http.MethodHead {
			http.Error(rw, "method not allowed", http.StatusMethodNotAllowed)
			return
		}
		s.illustration(rw, r)
	case "/feedback", "/confirm", "/author-review":
		if r.Method != http.MethodPost {
			http.Error(rw, "method not allowed", http.StatusMethodNotAllowed)
			return
		}
		switch r.URL.Path {
		case "/feedback":
			s.feedback(rw, r)
		case "/confirm":
			s.confirm(rw, r)
		default:
			s.authorReview(rw, r)
		}
	default:
		http.NotFound(rw, r)
	}
}

// page 按当前工作区状态重新生成阅读页；asJSON 为 true 时输出阅读页数据（GET /page.json，preview-page-json），否则输出 HTML。
func (s *previewServer) page(rw http.ResponseWriter, asJSON bool) {
	w, u, ps := work.Resolve(s.dir, s.ref)
	if u == nil {
		http.Error(rw, "单元 "+s.ref+" 无法加载："+ps[0].Message, http.StatusInternalServerError)
		return
	}
	p := preview.Build(s.dir, w, preview.Subtree(u), unitLint(s.dir, w))
	p.Serve = true
	p.Token = s.token
	p.Confirms = confirmQueue(s.dir, w)
	p.Strict, p.Reviews = reviewQueue(s.dir, w)
	p.Failures = auditFailureSummary(s.dir)
	render, ctype := preview.Render, "text/html; charset=utf-8"
	if asJSON {
		render, ctype = preview.JSON, "application/json; charset=utf-8"
	}
	data, err := render(p)
	if err != nil {
		http.Error(rw, err.Error(), http.StatusInternalServerError)
		return
	}
	rw.Header().Set("Content-Type", ctype)
	rw.Write(data)
}

// illustration 返回阅读页所列单元中一张可用配图的图片字节（GET /illustration，preview-illustration）：按当前工作区状态重新
// 解析单元与配图登记，unit 不在阅读页所列单元中、id 未登记或图片不可用时返回 404，不返回任何图片数据。
func (s *previewServer) illustration(rw http.ResponseWriter, r *http.Request) {
	q := r.URL.Query()
	ref, id := q.Get("unit"), q.Get("id")
	notFound := func(msg string) {
		rw.Header().Set("Content-Type", "text/plain; charset=utf-8")
		rw.WriteHeader(http.StatusNotFound)
		io.WriteString(rw, msg+"\n")
	}
	w, u, _ := work.Resolve(s.dir, s.ref)
	if u == nil || ref == "" || id == "" || !slices.Contains(refs(preview.Subtree(u)), ref) {
		notFound("配图不存在：单元不在阅读页中或缺少 unit、id")
		return
	}
	_, target, _ := work.Resolve(s.dir, ref)
	if target == nil {
		notFound("配图不存在：单元无法解析")
		return
	}
	unitDir := w.UnitDir(target)
	items, _, ps := illustration.Load(s.dir, unitDir)
	if len(ps) > 0 {
		notFound("配图不存在：配图登记不通过校验")
		return
	}
	for _, it := range items {
		if it.ID != id {
			continue
		}
		data, ok := illustration.ReadImage(s.dir, unitDir, it)
		if !ok {
			break
		}
		rw.Header().Set("Content-Type", illustration.MIME(it.Format))
		rw.Header().Set("Content-Length", strconv.Itoa(len(data)))
		if r.Method == http.MethodHead {
			return
		}
		rw.Write(data)
		return
	}
	notFound("配图不存在或不可用：" + id)
}

// previewRequest 是 POST /feedback 的请求体。
type previewRequest struct {
	Unit  string `json:"unit"`
	Quote string `json:"quote"`
	Text  string `json:"text"`
	Kind  string `json:"kind"`
	Mark  string `json:"mark"`
}

// quickMarks 是快捷标记：反馈类型与默认文字；作者另写了文字时为「默认文字：」加文字。
var quickMarks = map[string]struct{ kind, label, text string }{
	"stuck":    {"must-fix", "阅读卡点", "阅读卡点：这里读不下去了"},
	"good":     {"praise", "好", "好"},
	"question": {"question", "疑问", "疑问"},
}

func (s *previewServer) feedback(rw http.ResponseWriter, r *http.Request) {
	res := &feedbackResult{}
	var problems []workspace.Problem
	var req previewRequest
	dec := json.NewDecoder(http.MaxBytesReader(rw, r.Body, previewMaxBody))
	dec.DisallowUnknownFields()
	bad := func(msg string) []workspace.Problem {
		return []workspace.Problem{{Code: codeInvalidValue, Severity: workspace.SeverityError, Message: msg}}
	}
	err := dec.Decode(&req)
	if err == nil && dec.Decode(&struct{}{}) != io.EOF {
		err = fmt.Errorf("JSON 对象之后还有其他内容")
	}
	// 阅读页按当前工作区重新生成，可以提交意见的单元同样按当前的单元树计算。
	var units []string
	if _, u, _ := work.Resolve(s.dir, s.ref); u != nil {
		units = refs(preview.Subtree(u))
	}
	if err != nil {
		problems = bad(fmt.Sprintf("请求体应为不超过 %d 字节的 JSON 对象，含 unit、quote、text、kind 与 mark：%v", previewMaxBody, err))
	} else if !slices.Contains(units, req.Unit) {
		problems = bad("单元 " + req.Unit + " 不在这个阅读页中；可以提交意见的单元为 " + strings.Join(units, "、"))
	} else if m, ok := quickMarks[req.Mark]; req.Mark != "" && !ok {
		problems = bad("快捷标记 " + req.Mark + " 不合法：应为 stuck、good 或 question")
	} else if ok {
		req.Kind = m.kind
		if strings.TrimSpace(req.Text) == "" {
			req.Text = m.text
		} else {
			req.Text = m.label + "：" + req.Text
		}
	}
	s.mu.Lock()
	defer s.mu.Unlock()
	if s.limit > 0 && s.saved >= s.limit {
		problems = []workspace.Problem{{Code: preview.CodeClosing, Severity: workspace.SeverityError,
			Message: fmt.Sprintf("阅读页已保存 --max 指定的 %d 条反馈，正在关闭，不再接受新的意见", s.limit)}}
	}
	if problems == nil {
		w, u, ps := work.Resolve(s.dir, req.Unit)
		problems = ps
		if u != nil {
			var it *feedback.Item
			var changed []string
			if it, changed, problems = feedback.Add(s.dir, w, u, req.Kind, req.Text, req.Quote, ""); it != nil {
				v := feedbackOf(*it)
				res.Feedback, res.ChangedPaths = &v, changed
			}
		}
	}
	res.Diagnostics = diagnostics(problems, s.dir)
	for i, d := range res.Diagnostics {
		if slices.Contains([]string{feedback.CodeQuoteNotFound, feedback.CodeAmbiguousQuote, draft.CodeNotFound, codeInvalidValue, preview.CodeClosing}, d.Code) ||
			slices.Contains([]string{"kind", "text"}, d.Field) {
			res.Diagnostics[i].Next = []string{"wa-cli help preview --json"}
		}
	}
	status := http.StatusOK
	switch {
	case res.Feedback != nil:
	case slices.ContainsFunc(problems, func(p workspace.Problem) bool {
		return p.Code == workspace.CodeWriteFailed || p.Code == workspace.CodeReadFailed
	}):
		status = http.StatusInternalServerError // 服务端读写失败，不是请求的问题
	case slices.ContainsFunc(problems, func(p workspace.Problem) bool { return p.Code == preview.CodeClosing }):
		status = http.StatusServiceUnavailable
	default:
		status = http.StatusBadRequest
	}
	rw.Header().Set("Content-Type", "application/json; charset=utf-8")
	rw.WriteHeader(status)
	writeJSON(rw, res)
	if res.Feedback == nil {
		return
	}
	line := &previewResult{Result: Result{ChangedPaths: res.ChangedPaths}, Preview: s.view, Feedback: res.Feedback}
	if err := s.emit(line, "新的作者反馈（Feedback）："+feedbackText(*res.Feedback)+"\n"); err != nil {
		s.err = err
		s.closing.Do(func() { close(s.done) })
		return
	}
	if s.saved++; s.limit > 0 && s.saved >= s.limit {
		s.closing.Do(func() { close(s.done) })
	}
}
