package cli

import (
	"bytes"
	"encoding/json"
	"os"
	"slices"
	"strings"
	"sync"
	"testing"
	"time"
)

// fbView 是一条反馈的 --json 输出。
type fbView struct {
	Unit   string  `json:"unit"`
	ID     string  `json:"id"`
	Kind   string  `json:"kind"`
	Status string  `json:"status"`
	Text   string  `json:"text"`
	Note   *string `json:"note"`
	Source string  `json:"source"`
	Anchor struct {
		State     string  `json:"state"`
		Quote     *string `json:"quote"`
		Line      *int    `json:"line"`
		Paragraph *int    `json:"paragraph"`
	} `json:"anchor"`
}

type fbResult struct {
	result
	One  *fbView
	Many []fbView
}

// runFb 以 --json 运行反馈命令，复用 runWS 的断言；feedback 为对象时放入 One，为数组时放入 Many。
func runFb(t *testing.T, dir string, args ...string) (int, fbResult) {
	t.Helper()
	code, res := runWS(t, dir, args...)
	out := fbResult{result: res}
	var raw struct {
		Feedback json.RawMessage `json:"feedback"`
	}
	json.Unmarshal([]byte(res.stdout), &raw)
	if len(raw.Feedback) > 0 && raw.Feedback[0] == '[' {
		json.Unmarshal(raw.Feedback, &out.Many)
	} else if string(raw.Feedback) != "null" {
		json.Unmarshal(raw.Feedback, &out.One)
	}
	return code, out
}

func ids(vs []fbView) []string {
	var out []string
	for _, v := range vs {
		out = append(out, v.Unit+" "+v.ID)
	}
	return out
}

const draftPath = ".writing-anything/works/my-post/draft.md"

func TestFeedbackAdd(t *testing.T) {
	dir := newWork(t)
	writeFile(t, wsPath(dir, draftPath), "第一段讲背景。\n\n第二段讲方案。")
	code, res := runFb(t, dir, "feedback", "add", "my-post", "--kind", "must-fix", "--quote", "讲方案", "--text", "方案要给出数据")
	f := res.One
	if code != 0 || f == nil || f.ID != "fb-1" || f.Kind != "must-fix" || f.Status != "open" || f.Anchor.State != "located" ||
		*f.Anchor.Line != 3 || *f.Anchor.Paragraph != 2 || *f.Anchor.Quote != "讲方案" || f.Note != nil {
		t.Fatalf("code = %d, feedback = %+v, %v", code, f, res.Diagnostics)
	}
	if !slices.Equal(res.ChangedPaths, []string{".writing-anything/works/my-post/feedback.yaml"}) {
		t.Errorf("changedPaths = %v", res.ChangedPaths)
	}
	stored := readFile(t, wsPath(dir, ".writing-anything/works/my-post/feedback.yaml"))
	if !strings.Contains(stored, "after: 。") || !strings.Contains(stored, "第二段") {
		t.Errorf("feedback.yaml:\n%s", stored)
	}
	// 针对整个单元的反馈。
	code, res = runFb(t, dir, "feedback", "add", "my-post", "--text", "整体太长")
	if code != 0 || res.One.Kind != "suggestion" || res.One.Anchor.State != "none" || res.One.Anchor.Quote != nil || res.One.Anchor.Line != nil {
		t.Errorf("unit-level: %+v", res.One)
	}
	before := readFile(t, wsPath(dir, ".writing-anything/works/my-post/feedback.yaml"))
	writeFile(t, wsPath(dir, draftPath), "方案一。\n\n方案二。")
	for _, tc := range []struct {
		args []string
		code string
	}{
		{[]string{"--quote", "不存在的话", "--text", "x"}, "feedback.quote-not-found"},
		{[]string{"--quote", "方案", "--text", "x"}, "feedback.ambiguous-quote"},
		{[]string{"--kind", "rumor", "--text", "x"}, "artifact.schema-violation"},
		{[]string{"--text", "x", "--id", "fb-1"}, "artifact.duplicate-id"},
	} {
		code, res := runFb(t, dir, append([]string{"feedback", "add", "my-post"}, tc.args...)...)
		if d := diag(t, res.result, tc.code); code != 1 || d.Next[0] != "wa-cli help feedback add --json" || res.One != nil {
			t.Errorf("%v: code = %d, %+v", tc.args, code, d)
		}
	}
	if readFile(t, wsPath(dir, ".writing-anything/works/my-post/feedback.yaml")) != before {
		t.Error("rejected feedback changed feedback.yaml")
	}
	os.Remove(wsPath(dir, draftPath))
	if code, res := runFb(t, dir, "feedback", "add", "my-post", "--quote", "x", "--text", "x"); code != 1 {
		t.Errorf("no draft: code = %d", code)
	} else {
		diag(t, res.result, "draft.not-found")
	}
	// 已有 feedback.yaml 的问题保留能复查它的命令。
	writeFile(t, wsPath(dir, ".writing-anything/works/my-post/feedback.yaml"), "schema_version: 1\nfeedback: {}\n")
	_, res = runFb(t, dir, "feedback", "add", "my-post", "--text", "x")
	if d := diag(t, res.result, "artifact.schema-violation"); d.Next[0] == "wa-cli help feedback add --json" {
		t.Errorf("existing problem next = %v", d.Next)
	}
}

func TestFeedbackListAndRelocation(t *testing.T) {
	dir := newWork(t)
	runWS(t, dir, "unit", "add", "my-post", "section", "intro")
	writeFile(t, wsPath(dir, draftPath), "第一段讲背景。\n\n第二段讲方案。\n")
	runFb(t, dir, "feedback", "add", "my-post", "--quote", "讲方案", "--text", "x")
	runFb(t, dir, "feedback", "add", "my-post/intro", "--text", "子单元意见")
	code, res := runFb(t, dir, "feedback", "list")
	if code != 0 || !slices.Equal(ids(res.Many), []string{"my-post fb-1", "my-post/intro fb-1"}) || len(res.ChangedPaths) != 0 {
		t.Fatalf("list-all: code = %d, %v", code, ids(res.Many))
	}
	writeFile(t, wsPath(dir, draftPath), "新开头。\n\n第一段讲背景。\n\n第二段讲方案。\n")
	if _, res = runFb(t, dir, "feedback", "list", "my-post"); res.Many[0].Anchor.State != "located" || *res.Many[0].Anchor.Paragraph != 3 || *res.Many[0].Anchor.Line != 5 {
		t.Errorf("moved: %+v", res.Many[0].Anchor)
	}
	writeFile(t, wsPath(dir, draftPath), "第一段也讲方案。\n\n第二段讲方案。\n")
	if _, res = runFb(t, dir, "feedback", "list", "my-post"); *res.Many[0].Anchor.Paragraph != 2 {
		t.Errorf("duplicate after edit: %+v", res.Many[0].Anchor)
	}
	writeFile(t, wsPath(dir, draftPath), "第一段讲背景。\n\n第二段讲设计。\n")
	code, res = runFb(t, dir, "feedback", "list", "my-post")
	if a := res.Many[0].Anchor; code != 0 || a.State != "orphaned" || a.Line != nil || a.Paragraph != nil || *a.Quote != "讲方案" {
		t.Errorf("rewritten: code = %d, %+v", code, a)
	}
	// 按状态过滤。
	runFb(t, dir, "feedback", "add", "my-post", "--text", "第二条")
	runFb(t, dir, "feedback", "resolve", "my-post", "fb-2")
	if _, res = runFb(t, dir, "feedback", "list", "my-post", "--status", "open"); !slices.Equal(ids(res.Many), []string{"my-post fb-1"}) {
		t.Errorf("by status: %v", ids(res.Many))
	}
	code, res = runFb(t, dir, "feedback", "list", "--status", "done")
	if d := diag(t, res.result, "input.invalid-value"); code != 1 || d.Next[0] != "wa-cli help feedback list --json" || res.Many == nil {
		t.Errorf("bad status: code = %d, %+v", code, d)
	}
	// 不合法的 feedback.yaml：报告诊断，其他反馈照常列出。
	writeFile(t, wsPath(dir, ".writing-anything/works/my-post/units/intro/feedback.yaml"), "a: [\n")
	code, res = runFb(t, dir, "feedback", "list")
	if code != 1 || res.Valid || len(res.Many) != 2 {
		t.Errorf("invalid file: code = %d, %v", code, ids(res.Many))
	}
}

func TestFeedbackResolve(t *testing.T) {
	dir := newWork(t)
	runFb(t, dir, "feedback", "add", "my-post", "--text", "意见")
	rel := ".writing-anything/works/my-post/feedback.yaml"
	writeFile(t, wsPath(dir, rel), "# 作者的注释\n"+readFile(t, wsPath(dir, rel)))
	code, res := runFb(t, dir, "feedback", "resolve", "my-post", "fb-1", "--note", "已在第二段补充数据")
	if code != 0 || res.One.Status != "resolved" || *res.One.Note != "已在第二段补充数据" || !slices.Equal(res.ChangedPaths, []string{rel}) {
		t.Fatalf("resolve: code = %d, %+v, %v", code, res.One, res.ChangedPaths)
	}
	if !strings.Contains(readFile(t, wsPath(dir, rel)), "# 作者的注释") {
		t.Error("comment lost")
	}
	if code, res = runFb(t, dir, "feedback", "resolve", "my-post", "fb-1"); code != 0 || len(res.ChangedPaths) != 0 {
		t.Errorf("same: code = %d, %v", code, res.ChangedPaths)
	}
	code, res = runFb(t, dir, "feedback", "resolve", "my-post", "fb-9")
	if d := diag(t, res.result, "feedback.not-found"); code != 1 || d.Next[0] != shellCommand("wa-cli", "feedback", "list", "my-post", "--dir", dir, "--json") {
		t.Errorf("missing: code = %d, %+v", code, d)
	}
	code, res = runFb(t, dir, "feedback", "resolve", "my-post", "fb-1", "--status", "done")
	if d := diag(t, res.result, "artifact.schema-violation"); code != 1 || d.Field != "status" || d.Next[0] != "wa-cli help feedback resolve --json" {
		t.Errorf("bad status: code = %d, %+v", code, d)
	}
}

// syncBuffer 是可以在 watch 运行时并发读取的输出缓冲。
type syncBuffer struct {
	mu sync.Mutex
	b  bytes.Buffer
}

func (s *syncBuffer) Write(p []byte) (int, error) {
	s.mu.Lock()
	defer s.mu.Unlock()
	return s.b.Write(p)
}

func (s *syncBuffer) String() string {
	s.mu.Lock()
	defer s.mu.Unlock()
	return s.b.String()
}

// watch 在后台运行 feedback watch，返回等待其结束的函数（超时时测试失败）。
func watch(t *testing.T, args ...string) func() (int, string, string) {
	t.Helper()
	var out, errOut syncBuffer
	done := make(chan int, 1)
	go func() { done <- Run(args, strings.NewReader(""), &out, &errOut) }()
	return func() (int, string, string) {
		select {
		case code := <-done:
			return code, out.String(), errOut.String()
		case <-time.After(10 * time.Second):
			t.Fatalf("feedback watch did not finish; stdout so far: %q", out.String())
			return 0, "", ""
		}
	}
}

// waitFor 轮询 buf，直到其中出现 want；超时时测试失败。以输出作为同步点，不依赖固定的等待时间。
func waitFor(t *testing.T, buf *syncBuffer, want string) {
	t.Helper()
	deadline := time.Now().Add(10 * time.Second)
	for !strings.Contains(buf.String(), want) {
		if time.Now().After(deadline) {
			t.Fatalf("output never contained %q: %q", want, buf.String())
		}
		time.Sleep(5 * time.Millisecond)
	}
}

// atomicWrite 先写临时文件再重命名，避免 watch 扫描时读到写了一半的文件。
func atomicWrite(t *testing.T, path, content string) {
	t.Helper()
	tmp := path + ".tmp"
	writeFile(t, tmp, content)
	if err := os.Rename(tmp, path); err != nil {
		t.Fatal(err)
	}
}

// envelopeLines 断言每一行都是完整的输出信封，返回每行的反馈摘要或诊断错误码。
func envelopeLines(t *testing.T, stdout string) []string {
	t.Helper()
	var got []string
	for _, l := range strings.Split(strings.TrimSpace(stdout), "\n") {
		var m map[string]json.RawMessage
		if err := json.Unmarshal([]byte(l), &m); err != nil {
			t.Fatalf("line %q is not JSON: %v", l, err)
		}
		var keys []string
		for k := range m {
			keys = append(keys, k)
		}
		slices.Sort(keys)
		if !slices.Equal(keys, []string{"changedPaths", "diagnostics", "feedback", "valid"}) {
			t.Fatalf("line %q keys = %v, want a full envelope", l, keys)
		}
		var env struct {
			Diagnostics []Diagnostic `json:"diagnostics"`
			Feedback    *fbView      `json:"feedback"`
		}
		json.Unmarshal([]byte(l), &env)
		if env.Feedback != nil {
			got = append(got, env.Feedback.Unit+" "+env.Feedback.ID+" "+env.Feedback.Text)
		} else {
			got = append(got, env.Diagnostics[0].Code)
		}
	}
	return got
}

// watchBuf 与 watch 相同，但返回可并发读取的输出，供测试以输出为同步点。
func watchBuf(t *testing.T, args ...string) (*syncBuffer, func() (int, string)) {
	t.Helper()
	var out, errOut syncBuffer
	done := make(chan int, 1)
	go func() { done <- Run(args, strings.NewReader(""), &out, &errOut) }()
	return &out, func() (int, string) {
		select {
		case code := <-done:
			return code, errOut.String()
		case <-time.After(10 * time.Second):
			t.Fatalf("feedback watch did not finish; stdout so far: %q", out.String())
			return 0, ""
		}
	}
}

func TestFeedbackWatch(t *testing.T) {
	dir := newWork(t)
	runFb(t, dir, "feedback", "add", "my-post", "--text", "哨兵")
	runFb(t, dir, "feedback", "add", "my-post", "--text", "旧意见")
	runFb(t, dir, "feedback", "resolve", "my-post", "fb-2")
	out, wait := watchBuf(t, "feedback", "watch", "--all", "--max", "3", "--interval", "20", "--dir", dir, "--json")
	// --all 输出的哨兵表示启动扫描已完成，此后追加的反馈都是新增。
	waitFor(t, out, "哨兵")
	runFb(t, dir, "feedback", "add", "my-post", "--text", "新意见")
	waitFor(t, out, "新意见")
	// 扫描中遇到的问题输出一次。
	runWS(t, dir, "unit", "add", "my-post", "section", "intro")
	intro := wsPath(dir, ".writing-anything/works/my-post/units/intro")
	os.MkdirAll(intro, 0o755)
	atomicWrite(t, intro+"/feedback.yaml", "a: [\n")
	waitFor(t, out, "artifact.parse-error")
	os.Remove(intro + "/feedback.yaml")
	runFb(t, dir, "feedback", "add", "my-post/intro", "--text", "子单元新意见")
	code, stderr := wait()
	got := envelopeLines(t, out.String())
	want := []string{"my-post fb-1 哨兵", "my-post fb-3 新意见", "artifact.parse-error", "my-post/intro fb-1 子单元新意见"}
	if code != 0 || stderr != "" || !slices.Equal(got, want) {
		t.Fatalf("code = %d, lines = %v, want %v; stderr = %q", code, got, want, stderr)
	}
}

func TestFeedbackWatchAllAndErrors(t *testing.T) {
	dir := newWork(t)
	runFb(t, dir, "feedback", "add", "my-post", "--text", "已解决")
	runFb(t, dir, "feedback", "resolve", "my-post", "fb-1")
	runFb(t, dir, "feedback", "add", "my-post", "--text", "已采纳")
	runFb(t, dir, "feedback", "resolve", "my-post", "fb-2", "--status", "accepted")
	runFb(t, dir, "feedback", "add", "my-post", "--text", "待处理")
	out, waitAll := watchBuf(t, "feedback", "watch", "--all", "--max", "2", "--dir", dir, "--json")
	if code, _ := waitAll(); code != 0 || !slices.Equal(envelopeLines(t, out.String()), []string{"my-post fb-2 已采纳", "my-post fb-3 待处理"}) {
		t.Errorf("watch --all: code = %d, %q", code, out.String())
	}
	for _, args := range [][]string{{"--interval", "0"}, {"--max", "x"}, {"--interval", "9300000000000"}} {
		code, res := runFb(t, dir, append([]string{"feedback", "watch"}, args...)...)
		if d := diag(t, res.result, "input.invalid-value"); code != 1 || d.Next[0] != "wa-cli help feedback watch --json" {
			t.Errorf("%v: code = %d, %+v", args, code, d)
		}
	}
	if code, res := runFb(t, t.TempDir(), "feedback", "watch"); code != 1 {
		t.Errorf("no workspace: code = %d", code)
	} else {
		diag(t, res.result, "workspace.not-found")
	}
	// 人读文本模式。
	wait := watch(t, "feedback", "watch", "--all", "--max", "1", "--dir", dir)
	if code, stdout, _ := wait(); code != 0 || !strings.Contains(stdout, "新的作者反馈（Feedback）：my-post fb-2") {
		t.Errorf("text: code = %d, %q", code, stdout)
	}
}

func TestNextListsFeedback(t *testing.T) {
	dir := newWork(t)
	runFb(t, dir, "feedback", "add", "my-post", "--text", "一")
	runFb(t, dir, "feedback", "add", "my-post", "--text", "二")
	runFb(t, dir, "feedback", "add", "my-post", "--text", "三")
	runFb(t, dir, "feedback", "resolve", "my-post", "fb-2", "--status", "accepted")
	runFb(t, dir, "feedback", "resolve", "my-post", "fb-3")
	code, res := runUnits(t, dir, "next", "my-post")
	s := res.Steps[0]
	fb := s["feedback"].([]any)
	if code != 0 || len(fb) != 2 || fb[0].(map[string]any)["id"] != "fb-1" || fb[1].(map[string]any)["id"] != "fb-2" ||
		!strings.Contains(s["action"].(string), "作者反馈") || len(s["blockers"].([]any)) != 1 {
		t.Fatalf("next: code = %d, step = %v", code, s)
	}
	// 没有反馈的单元 feedback 为空数组。
	runWS(t, dir, "unit", "add", "my-post", "section", "intro")
	if _, res = runUnits(t, dir, "next", "my-post/intro"); len(res.Steps[0]["feedback"].([]any)) != 0 {
		t.Errorf("intro feedback = %v", res.Steps[0]["feedback"])
	}
	if code, stdout, _ := run(t, "next", "my-post", "--dir", dir); code != 0 || !strings.Contains(stdout, "反馈：my-post fb-1") {
		t.Errorf("text next: %q", stdout)
	}
	// 未处理的反馈不阻止推进。
	writeFile(t, wsPath(dir, ".writing-anything/works/my-post/brief.yaml"), completeBrief)
	if code, res := runUnits(t, dir, "advance", "my-post"); code != 0 || res.Unit["stage"] != "draft" {
		t.Errorf("advance with pending feedback: code = %d, %v", code, res.Diagnostics)
	}
}

func TestFeedbackValidateAndText(t *testing.T) {
	dir := newWork(t)
	runFb(t, dir, "feedback", "add", "my-post", "--text", "意见")
	rel := ".writing-anything/works/my-post/feedback.yaml"
	if code, res := runWS(t, dir, "validate"); code != 0 || !slices.Contains(res.Checked, rel) {
		t.Fatalf("checks-feedback-files: code = %d, %v", code, res.Checked)
	}
	writeFile(t, wsPath(dir, rel), readFile(t, wsPath(dir, rel))+"  - {id: fb-1, kind: praise, status: open, text: y}\n")
	code, res := runWS(t, dir, "validate")
	if d := diag(t, res, "artifact.duplicate-id"); code != 1 || d.Path != rel || d.Field != "feedback[1].id" {
		t.Errorf("duplicate: code = %d, %+v", code, d)
	}
	if code, res = runWS(t, dir, "validate", rel); code != 1 {
		t.Errorf("single feedback.yaml: code = %d", code)
	} else {
		diag(t, res, "artifact.duplicate-id")
	}
	fresh := newWork(t)
	writeFile(t, wsPath(fresh, draftPath), "正文里的一句话。")
	for _, tc := range []struct {
		args []string
		want string
	}{
		{[]string{"feedback", "list"}, "没有作者反馈"},
		{[]string{"feedback", "add", "my-post", "--quote", "一句话", "--text", "改短"}, "第 1 段（第 1 行）「一句话」"},
		{[]string{"feedback", "add", "my-post", "--text", "整体"}, "整个单元"},
		{[]string{"feedback", "resolve", "my-post", "fb-2"}, "已更新"},
		{[]string{"feedback", "list"}, "fb-2（suggestion，resolved）"},
	} {
		if code, stdout, stderr := run(t, append(tc.args, "--dir", fresh)...); code != 0 || !strings.Contains(stdout, tc.want) || stderr != "" {
			t.Errorf("%v: code = %d, %q, %q", tc.args, code, stdout, stderr)
		}
	}
	writeFile(t, wsPath(fresh, draftPath), "改写了。")
	if _, stdout, _ := run(t, "feedback", "list", "--dir", fresh); !strings.Contains(stdout, "失去锚点") {
		t.Errorf("orphaned text: %q", stdout)
	}
	for _, args := range [][]string{{"feedback", "add", "nope", "--text", "x"}, {"feedback", "resolve", "my-post", "fb-9"}} {
		if code, stdout, _ := run(t, append(args, "--dir", fresh)...); code != 1 || stdout != "" {
			t.Errorf("%v: code = %d, %q", args, code, stdout)
		}
	}
}

// 单元目录途经符号链接时，list、next 与 validate 都不读取其中的 feedback.yaml。
func TestFeedbackSymlinkedUnitDir(t *testing.T) {
	dir := newWork(t)
	runWS(t, dir, "unit", "add", "my-post", "section", "intro")
	outside := t.TempDir()
	writeFile(t, outside+"/feedback.yaml", "schema_version: 1\nfeedback:\n  - {id: fb-1, kind: praise, status: open, text: 外部}\n  - {id: fb-1, kind: praise, status: open, text: 外部}\n")
	os.MkdirAll(wsPath(dir, ".writing-anything/works/my-post/units"), 0o755)
	os.Symlink(outside, wsPath(dir, ".writing-anything/works/my-post/units/intro"))
	if code, res := runFb(t, dir, "feedback", "list"); code != 0 || len(res.Many) != 0 {
		t.Errorf("list: code = %d, %v", code, ids(res.Many))
	}
	if _, res := runUnits(t, dir, "next", "my-post/intro"); len(res.Steps[0]["feedback"].([]any)) != 0 {
		t.Errorf("next: %v", res.Steps[0]["feedback"])
	}
	rel := ".writing-anything/works/my-post/units/intro/feedback.yaml"
	if code, res := runWS(t, dir, "validate"); code != 0 || slices.Contains(res.Checked, rel) {
		t.Errorf("validate: code = %d, checked = %v", code, res.Checked)
	}
	code, res := runWS(t, dir, "validate", rel)
	if d := diag(t, res, "artifact.not-found"); code != 1 || d.Path != rel {
		t.Errorf("validate path: code = %d, %+v", code, d)
	}
}
