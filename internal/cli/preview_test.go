package cli

import (
	"crypto/sha256"
	"encoding/base64"
	"encoding/json"
	"io"
	"maps"
	"net"
	"net/http"
	"net/http/httptest"
	"net/url"
	"os"
	"path/filepath"
	"strings"
	"testing"
	"time"
)

type previewOut struct {
	result
	Preview  *previewView  `json:"preview"`
	Feedback *feedbackView `json:"feedback"`
}

func runPreviewJSON(t *testing.T, dir string, args ...string) (int, previewOut) {
	t.Helper()
	code, res := runWS(t, dir, append([]string{"preview"}, args...)...)
	var out previewOut
	if err := json.Unmarshal([]byte(res.stdout), &out); err != nil {
		t.Fatalf("%v: %v\n%s", args, err, res.stdout)
	}
	out.result = res
	return code, out
}

// previewWork 返回一个作品 my-post：根单元正文由 Agent 写入，有子单元 intro（没有正文）。
func previewWork(t *testing.T, body string) string {
	t.Helper()
	dir := newWork(t)
	if code, stdout, _ := runIn(t, body, "draft", "write", "my-post", "--dir", dir); code != 0 {
		t.Fatal(stdout)
	}
	mustRunWS(t, dir, "unit", "add", "my-post", "section", "intro")
	return dir
}

func TestPreviewStatic(t *testing.T) {
	dir := previewWork(t, "值得注意的是，连接池很重要。\n\n<b>粗体</b> & 引号\n")
	review := "rubric:\n  - {item: clarity, verdict: pass, evidence: 清楚, paragraphs: [1]}\nfindings:\n  - {id: r1, paragraph: 1, message: 开头太空, suggestion: 先写结论}\n"
	if code, _ := runReview(t, dir, review, "review", "record", "my-post"); code != 0 {
		t.Fatal("review record failed")
	}
	// static-page 与 content-findings；除阅读页文件外不创建或修改任何文件。
	snapshot := workspaceFiles(t, dir)
	code, res := runPreviewJSON(t, dir, "my-post")
	const rel = ".writing-anything/exports/my-post.preview.html"
	if code != 0 || res.Preview == nil || *res.Preview.Path != rel || res.Preview.URL != nil || res.Feedback != nil ||
		strings.Join(res.Preview.Units, ",") != "my-post,my-post/intro" || !strings.Contains(strings.Join(res.ChangedPaths, ","), rel) {
		t.Fatalf("static-page: exit code = %d, result = %+v", code, res)
	}
	after := workspaceFiles(t, dir)
	delete(after, ".writing-anything/exports/my-post.preview.html")
	delete(after, ".writing-anything/exports")
	if !maps.Equal(snapshot, after) {
		t.Fatalf("static preview changed other files")
	}
	page := readFile(t, wsPath(dir, rel))
	for _, want := range []string{"值得注意的是，连接池很重要。", `class="para o-agent"`, "· Agent</div>", "阶段：brief", `id="color"`, "#color:checked ~ main .o-agent", "ai-cliche", "审稿 r1（open）", "先写结论", "还没有正文", "my-post/intro",
		"&lt;b&gt;粗体&lt;/b&gt; &amp; 引号"} {
		if !strings.Contains(page, want) {
			t.Errorf("page lacks %q", want)
		}
	}
	// 发现项列在所在段落之后：ai-cliche 与 r1 在第 1 段之后、第 2 段之前。
	p2 := strings.Index(page, `data-index="2"`)
	if i := strings.Index(page, "ai-cliche"); i < 0 || i > p2 || strings.Index(page, "审稿 r1") > p2 {
		t.Errorf("findings not after paragraph 1")
	}
	for _, bad := range []string{"<script", "http://", "https://", "<b>粗体"} {
		if strings.Contains(page, bad) {
			t.Errorf("static page contains %q", bad)
		}
	}
	// 子单元的默认路径，--output 与人读输出。
	if _, res := runPreviewJSON(t, dir, "my-post/intro"); *res.Preview.Path != ".writing-anything/exports/my-post.intro.preview.html" {
		t.Fatalf("subunit path: %+v", res.Preview)
	}
	if code, stdout, _ := run(t, "preview", "my-post", "--output", "out/p.html", "--dir", dir); code != 0 || !strings.Contains(stdout, "out/p.html") {
		t.Fatalf("output: exit code = %d, %s", code, stdout)
	}
	// static-invalid-output
	config := readFile(t, configPath(dir))
	code, res = runPreviewJSON(t, dir, "my-post", "--output", ".writing-anything/config.yaml")
	if d := diag(t, res.result, "preview.invalid-output"); code != 1 || d.Field != "output" || !hasNext(d, "wa-cli help preview --json") || readFile(t, configPath(dir)) != config {
		t.Fatalf("static-invalid-output: exit code = %d, result = %+v", code, res)
	}
	// 审稿过期与正文过大。
	writeFile(t, wsPath(dir, draftRel), "改过的正文。\n")
	runPreviewJSON(t, dir, "my-post")
	if page := readFile(t, wsPath(dir, rel)); !strings.Contains(page, "审稿基于旧版正文") || !strings.Contains(page, "审稿 r1（open）") {
		t.Fatalf("stale review: %s", page)
	}
	writeFile(t, wsPath(dir, draftRel), strings.Repeat("长", 1<<20))
	runPreviewJSON(t, dir, "my-post")
	if page := readFile(t, wsPath(dir, rel)); !strings.Contains(page, "超过阅读页的上限") {
		t.Fatal("too large draft not reported")
	}
}

func TestPreviewFlags(t *testing.T) {
	dir := newWork(t)
	for _, args := range [][]string{{"--port", "70000", "--serve"}, {"--port", "x", "--serve"}, {"--max", "1"}, {"--port", "8080"},
		{"--serve", "--output", "a.html"}, {"--serve", "--max", "0"}} {
		code, res := runPreviewJSON(t, dir, append([]string{"my-post"}, args...)...)
		if d := diag(t, res.result, "input.invalid-value"); code != 1 || d.Next[0] != "wa-cli help preview --json" || res.Preview != nil {
			t.Errorf("%v: exit code = %d, result = %+v", args, code, res)
		}
	}
	if code, res := runPreviewJSON(t, dir, "nope"); code != 1 || res.Preview != nil {
		t.Fatalf("unknown unit: exit code = %d", code)
	}
	if code, res := runPreviewJSON(t, t.TempDir(), "my-post"); code != 1 || diag(t, res.result, "workspace.not-found").Code == "" {
		t.Fatalf("no workspace: exit code = %d", code)
	}
}

// serving 是一个在后台运行的 preview --serve。
type serving struct {
	first  previewOut // --json 时的首行信封
	url    string
	token  string
	host   string
	finish func() (int, string, string) // 等命令结束，返回退出码、首行之后的输出与标准错误
}

// servePreviewT 在后台运行 preview --serve，以输出为同步点读出首行中的地址。
func servePreviewT(t *testing.T, dir string, args ...string) *serving {
	t.Helper()
	var out, errOut syncBuffer
	done := make(chan int, 1)
	go func() {
		done <- Run(append([]string{"preview", "my-post", "--serve", "--dir", dir}, args...), strings.NewReader(""), &out, &errOut)
	}()
	waitFor(t, &out, "\n")
	first, _, _ := strings.Cut(out.String(), "\n")
	s := &serving{finish: func() (int, string, string) {
		select {
		case code := <-done:
			_, rest, _ := strings.Cut(out.String(), "\n")
			return code, strings.TrimSuffix(rest, "\n"), errOut.String()
		case <-time.After(10 * time.Second):
			t.Fatalf("preview --serve did not finish; stdout so far: %q", out.String())
			return 0, "", ""
		}
	}}
	if json.Unmarshal([]byte(first), &s.first) == nil && s.first.Preview != nil && s.first.Preview.URL != nil {
		s.url = *s.first.Preview.URL
	} else if i := strings.Index(first, "http://"); i >= 0 {
		s.url = strings.Fields(strings.ReplaceAll(first[i:], "（", " "))[0]
	}
	u, err := url.Parse(s.url)
	if err != nil || u.Hostname() != "127.0.0.1" {
		t.Fatalf("first line %q: url %q", first, s.url)
	}
	s.token, s.host = u.Query().Get("token"), u.Host
	return s
}

func (s *serving) post(t *testing.T, body string, mutate ...func(*http.Request)) (int, map[string]any) {
	t.Helper()
	req, _ := http.NewRequest(http.MethodPost, "http://"+s.host+"/feedback", strings.NewReader(body))
	req.Header.Set("X-WA-Token", s.token)
	for _, m := range mutate {
		m(req)
	}
	resp, err := http.DefaultClient.Do(req)
	if err != nil {
		t.Fatal(err)
	}
	defer resp.Body.Close()
	var out map[string]any
	json.NewDecoder(resp.Body).Decode(&out)
	return resp.StatusCode, out
}

func httpGet(t *testing.T, raw string, mutate ...func(*http.Request)) *http.Response {
	t.Helper()
	req, _ := http.NewRequest(http.MethodGet, raw, nil)
	for _, m := range mutate {
		m(req)
	}
	resp, err := http.DefaultClient.Do(req)
	if err != nil {
		t.Fatal(err)
	}
	resp.Body.Close()
	return resp
}

func TestPreviewServe(t *testing.T) {
	dir := previewWork(t, "连接池很重要。连接池要复用。\n")
	// 哨兵反馈让 feedback watch 的启动有输出可等（go-project-structure/output-sync-in-tests）。
	mustRunWS(t, dir, "feedback", "add", "my-post", "--text", "哨兵")
	feedbackFile := wsPath(dir, ".writing-anything/works/my-post/feedback.yaml")
	before := readFile(t, feedbackFile)
	s := servePreviewT(t, dir, "--max", "2", "--json")
	if u, _ := url.Parse(s.url); !net.ParseIP(u.Hostname()).IsLoopback() || u.Hostname() != "127.0.0.1" {
		t.Fatalf("not listening on loopback: %s", s.url)
	}
	if len(s.token) < 32 {
		t.Fatalf("token %q shorter than 128 bits", s.token)
	}
	if f := s.first; f.Preview.Path != nil || f.Feedback != nil || len(f.ChangedPaths) != 0 || !f.Valid || strings.Join(f.Preview.Units, ",") != "my-post,my-post/intro" {
		t.Fatalf("first line: %+v", f)
	}
	// serve-url
	resp, err := http.Get(s.url)
	if err != nil {
		t.Fatal(err)
	}
	body, _ := io.ReadAll(resp.Body)
	resp.Body.Close()
	h := resp.Header
	sum := sha256.Sum256([]byte(scriptOf(string(body))))
	if resp.StatusCode != 200 || !strings.Contains(string(body), "连接池很重要") || !strings.Contains(string(body), "<script") ||
		!strings.Contains(h.Get("Content-Security-Policy"), "'sha256-"+base64.StdEncoding.EncodeToString(sum[:])+"'") ||
		h.Get("Cache-Control") != "no-store" || h.Get("Referrer-Policy") != "no-referrer" || h.Get("X-Frame-Options") != "DENY" || h.Get("Content-Security-Policy") == "" {
		t.Fatalf("serve-url: status %d, headers %v", resp.StatusCode, h)
	}
	// preview-relative-submit：提交路径相对于页面地址，直接访问时落到 /<path>，挂在代理前缀下时落到前缀下的同名路径。
	script := scriptOf(string(body))
	direct, _ := url.Parse(s.url)
	prefixed, _ := url.Parse("http://gamepc.example:13080/dsh-writing-viewer/review/")
	for _, p := range []string{"feedback", "confirm", "author-review"} {
		if !strings.Contains(script, "('"+p+"'") || strings.Contains(script, "('/"+p+"'") {
			t.Errorf("script does not submit %s by a relative path", p)
		}
		ref, _ := url.Parse(p)
		if got := direct.ResolveReference(ref).Path; got != "/"+p {
			t.Errorf("direct %s resolves to %s", p, got)
		}
		if got := prefixed.ResolveReference(ref).Path; got != "/dsh-writing-viewer/review/"+p {
			t.Errorf("prefixed %s resolves to %s", p, got)
		}
	}
	// serve-reject-no-token：GET 与 POST 缺少令牌、令牌错误或 Host 不符时都返回 403，不写文件。
	base := "http://" + s.host + "/"
	evil := func(r *http.Request) { r.Host = "evil.example:" + strings.Split(s.host, ":")[1] }
	for name, code := range map[string]int{
		"get no token":  httpGet(t, base).StatusCode,
		"get bad token": httpGet(t, base+"?token=00").StatusCode,
		"get evil host": httpGet(t, s.url, evil).StatusCode,
	} {
		if code != 403 {
			t.Errorf("%s: status %d", name, code)
		}
	}
	quote := `{"unit":"my-post","quote":"连接池很重要","text":"补一个例子"}`
	for name, m := range map[string]func(*http.Request){
		"post no token":  func(r *http.Request) { r.Header.Del("X-WA-Token") },
		"post bad token": func(r *http.Request) { r.Header.Set("X-WA-Token", "00") },
		"post evil host": evil,
	} {
		if code, _ := s.post(t, quote, m); code != 403 {
			t.Errorf("%s: status %d", name, code)
		}
	}
	if readFile(t, feedbackFile) != before {
		t.Fatal("rejected requests changed feedback.yaml")
	}
	// 其他路径与方法；不合法的请求体。
	if c := httpGet(t, "http://"+s.host+"/x?token="+s.token).StatusCode; c != 404 {
		t.Errorf("unknown path: %d", c)
	}
	if c := httpGet(t, "http://"+s.host+"/feedback?token="+s.token).StatusCode; c != 405 {
		t.Errorf("GET /feedback: %d", c)
	}
	for _, body := range []string{`{`, `{"unit":"my-post","text":"a"} 多余`, `{"unit":"other","text":"x"}`, `{"unit":"my-post","mark":"wow"}`, `{"unit":"my-post","text":"x","extra":1}`,
		`{"unit":"my-post","text":"` + strings.Repeat("长", 30000) + `"}`} {
		if code, out := s.post(t, body); code != 400 || out["valid"] != false || !strings.Contains(asJSON(out), "input.invalid-value") {
			t.Errorf("%.40s: status %d, %v", body, code, out)
		}
	}
	// ambiguous-selection
	if code, out := s.post(t, `{"unit":"my-post","quote":"连接池","text":"x"}`); code != 400 || !strings.Contains(asJSON(out), "feedback.ambiguous-quote") || out["feedback"] != nil {
		t.Fatalf("ambiguous-selection: %d %v", code, out)
	}
	if readFile(t, feedbackFile) != before {
		t.Fatal("failed request changed feedback.yaml")
	}
	// select-and-comment 与 watch-sees-page-feedback
	watchOut, finishWatch := watchBuf(t, "feedback", "watch", "--all", "--max", "2", "--interval", "10", "--dir", dir, "--json")
	waitFor(t, watchOut, "哨兵")
	code, out := s.post(t, quote)
	fb, _ := out["feedback"].(map[string]any)
	if code != 200 || out["valid"] != true || fb["id"] != "fb-2" || fb["kind"] != "suggestion" || fb["anchor"].(map[string]any)["state"] != "located" {
		t.Fatalf("select-and-comment: %d %v", code, out)
	}
	if code, _ := finishWatch(); code != 0 || !strings.Contains(watchOut.String(), `"id":"fb-2"`) {
		t.Fatalf("watch did not see page feedback: %s", watchOut.String())
	}
	// quick-mark-stuck；第二条反馈达到 --max 2，命令结束。
	code, out = s.post(t, `{"unit":"my-post","quote":"连接池要复用","mark":"stuck"}`)
	fb, _ = out["feedback"].(map[string]any)
	if code != 200 || fb["kind"] != "must-fix" || fb["text"] != "阅读卡点：这里读不下去了" {
		t.Fatalf("quick-mark-stuck: %d %v", code, out)
	}
	exit, rest, stderr := s.finish()
	if exit != 0 || strings.Count(rest, "\n")+1 != 2 || !strings.Contains(rest, `"id":"fb-3"`) || !strings.Contains(rest, `"changedPaths":[".writing-anything/works/my-post/feedback.yaml"]`) {
		t.Fatalf("--max: exit %d, lines %q, stderr %s", exit, rest, stderr)
	}
	code, res := runWS(t, dir, "feedback", "list", "my-post")
	if code != 0 || !strings.Contains(res.stdout, "补一个例子") || !strings.Contains(res.stdout, "阅读卡点") {
		t.Fatalf("feedback list: %s", res.stdout)
	}
}

// 快捷标记附带文字、针对整个单元的意见与人读输出。
func TestPreviewServeMarks(t *testing.T) {
	dir := previewWork(t, "正文。\n")
	s := servePreviewT(t, dir, "--max", "4")
	if code, out := s.post(t, `{"unit":"my-post/intro","mark":"good","text":"节奏好"}`); code != 200 || out["feedback"].(map[string]any)["text"] != "好：节奏好" ||
		out["feedback"].(map[string]any)["kind"] != "praise" {
		t.Fatalf("good with text: %d %v", code, out)
	}
	// localhost:<port> 同样被接受；kind 原样传给 feedback add。
	localhost := func(r *http.Request) { r.Host = "localhost:" + strings.Split(s.host, ":")[1] }
	if code, out := s.post(t, `{"unit":"my-post","text":"必须改","kind":"must-fix"}`, localhost); code != 200 || out["feedback"].(map[string]any)["kind"] != "must-fix" {
		t.Fatalf("localhost / kind: %d %v", code, out)
	}
	// 服务启动后新增的单元出现在重新生成的页面中，也可以提交意见。
	mustRunWS(t, dir, "unit", "add", "my-post", "section", "later")
	if resp := httpGet(t, s.url); resp.StatusCode != 200 {
		t.Fatalf("GET: %d", resp.StatusCode)
	}
	if page := fetch(t, s.url); !strings.Contains(page, "my-post/later") {
		t.Fatal("page not rebuilt from the current unit tree")
	}
	if code, out := s.post(t, `{"unit":"my-post/later","text":"新单元"}`); code != 200 {
		t.Fatalf("new unit: %d %v", code, out)
	}
	if code, out := s.post(t, `{"unit":"my-post","mark":"question"}`); code != 200 || out["feedback"].(map[string]any)["kind"] != "question" || out["feedback"].(map[string]any)["text"] != "疑问" {
		t.Fatalf("question: %d %v", code, out)
	}
	exit, rest, _ := s.finish()
	if exit != 0 || !strings.Contains(rest, "新的作者反馈") || strings.Contains(rest, "{") {
		t.Fatalf("text output: exit %d, %q", exit, rest)
	}
}

// 端口已被占用时报告 preview.listen-failed。
func TestPreviewListenFailed(t *testing.T) {
	dir := newWork(t)
	s := servePreviewT(t, dir, "--max", "1", "--json")
	port := strings.Split(s.host, ":")[1]
	code, res := runPreviewJSON(t, dir, "my-post", "--serve", "--port", port)
	if d := diag(t, res.result, "preview.listen-failed"); code != 1 || !hasNext(d, "wa-cli help preview --json") {
		t.Fatalf("listen-failed: exit code = %d, result = %+v", code, res)
	}
	s.post(t, `{"unit":"my-post","text":"结束"}`)
	if exit, _, _ := s.finish(); exit != 0 {
		t.Fatalf("exit %d", exit)
	}
}

func asJSON(v any) string {
	b, _ := json.Marshal(v)
	return string(b)
}

func fetch(t *testing.T, raw string) string {
	t.Helper()
	resp, err := http.Get(raw)
	if err != nil {
		t.Fatal(err)
	}
	defer resp.Body.Close()
	b, _ := io.ReadAll(resp.Body)
	return string(b)
}

// scriptOf 返回页面中内联脚本的内容。
func scriptOf(page string) string {
	_, rest, _ := strings.Cut(page, "<script>")
	script, _, _ := strings.Cut(rest, "</script>")
	return script
}

// workspaceFiles 返回项目目录下每个文件与目录的内容（目录为空字符串），用于确认命令没有改动其他文件。
func workspaceFiles(t *testing.T, dir string) map[string]string {
	t.Helper()
	out := map[string]string{}
	err := filepath.WalkDir(dir, func(p string, d os.DirEntry, err error) error {
		if err != nil {
			return err
		}
		rel, _ := filepath.Rel(dir, p)
		rel = filepath.ToSlash(rel)
		if rel == ".writing-anything/audit.log" || rel == ".writing-anything/audit.log.1" {
			return nil
		}
		if d.IsDir() {
			out[rel] = ""
			return nil
		}
		b, err := os.ReadFile(p)
		out[rel] = string(b)
		return err
	})
	if err != nil {
		t.Fatal(err)
	}
	return out
}

// 达到 --max 后仍在途的提交得到 503 与输出信封，不保存反馈。
func TestPreviewClosing(t *testing.T) {
	dir := newWork(t)
	s := &previewServer{inv: &invocation{stdout: io.Discard}, dir: dir, ref: "my-post", limit: 1, saved: 1, done: make(chan struct{})}
	rec := httptest.NewRecorder()
	s.feedback(rec, httptest.NewRequest(http.MethodPost, "/feedback", strings.NewReader(`{"unit":"my-post","text":"晚到"}`)))
	var out previewOut
	if err := json.Unmarshal(rec.Body.Bytes(), &out); err != nil || rec.Code != 503 || out.Valid || out.Diagnostics[0].Code != "preview.closing" {
		t.Fatalf("closing: %d %s", rec.Code, rec.Body.String())
	}
	if _, err := os.Stat(wsPath(dir, ".writing-anything/works/my-post/feedback.yaml")); err == nil {
		t.Fatal("feedback saved after --max")
	}
}
