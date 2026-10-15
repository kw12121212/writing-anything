package cli

import (
	"bytes"
	"encoding/json"
	"maps"
	"net/http"
	"net/http/httptest"
	"os"
	"path/filepath"
	"slices"
	"strings"
	"testing"
)

const pageHost = "127.0.0.1:4170"

// newPage 返回作品 ref 的本地阅读页服务（不监听端口，经 ServeHTTP 直接处理请求），标准输出写入 out。
func newPage(dir, ref string, out *bytes.Buffer) *previewServer {
	return &previewServer{inv: &invocation{stdout: out}, dir: dir, ref: ref, token: "page-token", hosts: []string{pageHost},
		view: &previewView{Unit: ref, Units: []string{ref}}, done: make(chan struct{})}
}

// pagePost 以令牌 token 向阅读页 POST path，返回状态码与解析后的 JSON 输出信封。
func pagePost(t *testing.T, s *previewServer, path, token string, body any) (int, map[string]any) {
	t.Helper()
	data, _ := json.Marshal(body)
	req := httptest.NewRequest(http.MethodPost, "http://"+pageHost+path, bytes.NewReader(data))
	if token != "" {
		req.Header.Set("X-WA-Token", token)
	}
	rec := httptest.NewRecorder()
	s.ServeHTTP(rec, req)
	var out map[string]any
	json.Unmarshal(rec.Body.Bytes(), &out)
	return rec.Code, out
}

// pageGet 返回阅读页的 HTML。
func pageGet(t *testing.T, s *previewServer) string {
	t.Helper()
	rec := httptest.NewRecorder()
	s.ServeHTTP(rec, httptest.NewRequest(http.MethodGet, "http://"+pageHost+"/?token="+s.token, nil))
	if rec.Code != 200 {
		t.Fatalf("GET /: %d %s", rec.Code, rec.Body.String())
	}
	return rec.Body.String()
}

// diagCodes 返回 JSON 输出信封中诊断的错误码。
func diagCodes(out map[string]any) []string {
	var codes []string
	ds, _ := out["diagnostics"].([]any)
	for _, d := range ds {
		codes = append(codes, d.(map[string]any)["code"].(string))
	}
	return codes
}

// pendingKeys 返回 review show 或 next 输出的待作者复核项的 key。
func pendingKeys(t *testing.T, stdout string) []string {
	t.Helper()
	var out struct {
		Pending []struct {
			Key     string `json:"key"`
			Kind    string `json:"kind"`
			Command string `json:"command"`
		} `json:"pendingAuthorReviews"`
	}
	if err := json.Unmarshal([]byte(stdout), &out); err != nil {
		t.Fatal(err)
	}
	keys := []string{}
	for _, p := range out.Pending {
		if !strings.Contains(p.Command, "wa-cli preview my-novel --serve") {
			t.Errorf("pending %s: command = %q", p.Key, p.Command)
		}
		keys = append(keys, p.Key)
	}
	return keys
}

func unitFile(dir, unit, name string) string {
	return filepath.Join(dir, ".writing-anything", "works", "my-novel", "units", unit, name)
}

// strict 关卡：命令行渠道的验收列为待作者复核，组稿被拒；作者在阅读页确认与批量复核后组稿通过；无令牌或错令牌
// 不能写入确认或复核。
func TestConfirmationChannelStrict(t *testing.T) {
	dir := volumeAcceptFixture(t, "strict")
	// 命令行验收记录渠道 cli。
	if a := readFile(t, unitFile(dir, "scene-1", "acceptance.yaml")); !strings.Contains(a, "channel: cli") {
		t.Fatalf("acceptance.yaml lacks channel: %s", a)
	}
	_, res := runWS(t, dir, "review", "show", "my-novel/scene-1")
	if keys := pendingKeys(t, res.stdout); len(keys) != 1 || !strings.HasPrefix(keys[0], "acceptance:my-novel/scene-1@") {
		t.Fatalf("review show pending = %v", keys)
	}
	if !strings.Contains(res.stdout, `"channel":"cli"`) {
		t.Fatalf("review show acceptance lacks channel: %s", res.stdout)
	}
	// 引入渠道前写入的旧记录（没有 channel）按 cli 解释。
	legacy := strings.Replace(readFile(t, unitFile(dir, "scene-1", "acceptance.yaml")), "channel: cli\n", "", 1)
	writeFile(t, unitFile(dir, "scene-1", "acceptance.yaml"), legacy)
	if _, res = runWS(t, dir, "review", "show", "my-novel/scene-1"); !strings.Contains(res.stdout, `"channel":"cli"`) || len(pendingKeys(t, res.stdout)) != 1 {
		t.Fatalf("legacy acceptance: %s", res.stdout)
	}
	_, res = runWS(t, dir, "next", "my-novel")
	if keys := pendingKeys(t, res.stdout); len(keys) != 1 {
		t.Fatalf("next pending = %v", keys)
	}
	if _, stdout, _ := run(t, "next", "--dir", dir); !strings.Contains(stdout, "待作者复核") {
		t.Fatalf("next text lacks pending reviews: %s", stdout)
	}

	var out bytes.Buffer
	s := newPage(dir, "my-novel", &out)
	page := pageGet(t, s)
	for _, want := range []string{"待作者确认", "验收（Acceptance）my-novel/scene-9", "待作者复核", "acceptance:my-novel/scene-1@", "review-submit"} {
		if !strings.Contains(page, want) {
			t.Errorf("page lacks %q", want)
		}
	}
	if !strings.Contains(scriptOf(page), "/author-review") {
		t.Error("page script lacks /author-review")
	}

	// 无令牌、错令牌与错误的 Host 都返回 403，不写任何文件。
	snapshot := workspaceFiles(t, dir)
	acceptBody := map[string]string{"kind": "acceptance", "unit": "my-novel/scene-9", "confirmation": "作者：可以"}
	for _, token := range []string{"", "wrong"} {
		if code, _ := pagePost(t, s, "/confirm", token, acceptBody); code != 403 {
			t.Errorf("confirm with token %q: %d", token, code)
		}
		if code, _ := pagePost(t, s, "/author-review", token, map[string][]string{"keys": {"x"}}); code != 403 {
			t.Errorf("author-review with token %q: %d", token, code)
		}
	}
	evil := httptest.NewRequest(http.MethodPost, "http://evil.example/confirm", strings.NewReader(`{}`))
	evil.Header.Set("X-WA-Token", s.token)
	rec := httptest.NewRecorder()
	if s.ServeHTTP(rec, evil); rec.Code != 403 {
		t.Errorf("evil host: %d", rec.Code)
	}
	if !maps.Equal(snapshot, workspaceFiles(t, dir)) {
		t.Fatal("unauthorized requests changed files")
	}

	// 请求不合法：原话摘要为空、不在队列中、动作不合法、请求体不是 JSON。
	for _, body := range []any{
		map[string]string{"kind": "acceptance", "unit": "my-novel/scene-9", "confirmation": " "},
		map[string]string{"kind": "acceptance", "unit": "my-novel/scene-1", "confirmation": "作者：可以"},
		map[string]string{"kind": "acceptance", "unit": "my-novel/scene-9", "action": "reject", "confirmation": "作者：可以"},
		"not an object",
	} {
		code, o := pagePost(t, s, "/confirm", s.token, body)
		ds, _ := o["diagnostics"].([]any)
		if code != 400 || o["valid"] != false || o["confirmation"] != nil || len(ds) != 1 || !slices.Equal(diagCodes(o), []string{"input.invalid-value"}) ||
			!slices.Equal(ds[0].(map[string]any)["next"].([]any), []any{"wa-cli help preview --json"}) {
			t.Errorf("%v: %d %v", body, code, o)
		}
	}
	// 已保存 --max 条反馈、正在关闭时返回 503（preview.closing），不写入。
	closing := newPage(dir, "my-novel", &out)
	closing.limit, closing.saved = 1, 1
	for _, path := range []string{"/confirm", "/author-review"} {
		if code, o := pagePost(t, closing, path, closing.token, map[string]any{}); code != 503 || !slices.Equal(diagCodes(o), []string{"preview.closing"}) {
			t.Errorf("closing %s: %d %v", path, code, o)
		}
	}
	// part-one 的最后场景 scene-1 验收时已自动生成卷末检查点（未确认），strict 下阻塞 part-two 的验收：作者在阅读页
	// 确认它（渠道 preview，不需要复核）。
	if !strings.Contains(pageGet(t, s), "卷末检查点（Volume Checkpoint）my-novel/part-one") {
		t.Fatal("page lacks part-one checkpoint")
	}
	if code, o := pagePost(t, s, "/confirm", s.token, map[string]string{"kind": "volume", "unit": "my-novel/part-one", "confirmation": "作者：上部无误"}); code != 200 {
		t.Fatalf("page volume confirm: %d %v", code, o)
	}
	if c := readFile(t, unitFile(dir, "part-one", "checkpoint.yaml")); !strings.Contains(c, "channel: preview") {
		t.Fatalf("checkpoint: %s", c)
	}
	// 阅读页验收：关卡阻塞（没有审稿）时 400 并列出阻塞项；记录审稿后验收成功，渠道 preview，不需要复核。
	if code, o := pagePost(t, s, "/confirm", s.token, acceptBody); code != 400 || !slices.Contains(diagCodes(o), "accept.review-missing") {
		t.Fatalf("blocked acceptance: %d %v", code, o)
	}
	run(t, "review", "record", "my-novel/scene-9", "--file", reviewInput(t, dir), "--dir", dir)
	code, o := pagePost(t, s, "/confirm", s.token, acceptBody)
	if c, _ := o["confirmation"].(map[string]any); code != 200 || c["channel"] != "preview" || c["kind"] != "acceptance" {
		t.Fatalf("page acceptance: %d %v", code, o)
	}
	// 阅读页验收与命令行一样在部的最后一个场景验收后自动生成卷末检查点。
	if cp, _ := o["changedPaths"].([]any); !slices.Contains(cp, any(".writing-anything/works/my-novel/units/part-two/checkpoint.yaml")) {
		t.Fatalf("page acceptance changedPaths = %v", o["changedPaths"])
	}
	if a := readFile(t, unitFile(dir, "scene-9", "acceptance.yaml")); !strings.Contains(a, "channel: preview") {
		t.Fatalf("scene-9 acceptance: %s", a)
	}
	if !strings.Contains(out.String(), "作者在阅读页确认（渠道 preview）：acceptance my-novel/scene-9") {
		t.Fatalf("stdout = %q", out.String())
	}
	run(t, "advance", "my-novel/scene-9", "--dir", dir)

	// 组稿被 scene-1 的命令行验收阻塞，next 给出打开阅读页的命令。
	code, res = runWS(t, dir, "assemble", "my-novel")
	d := diag(t, res, "assemble.unreviewed")
	if code != 1 || len(res.Diagnostics) != 1 || !strings.Contains(d.Message, "my-novel/scene-1") ||
		!hasNext(d, "wa-cli preview my-novel --serve") || !hasNext(d, "wa-cli review show my-novel") {
		t.Fatalf("assemble: %d %+v", code, res.Diagnostics)
	}
	// 复核：未知 key 与重复 key 被拒、不写文件；勾选的待复核项写入复核记录。
	_, res = runWS(t, dir, "review", "show", "my-novel")
	keys := pendingKeys(t, res.stdout)
	for _, bad := range []struct {
		keys []string
		code string
	}{{[]string{}, "input.invalid-value"}, {[]string{"acceptance:my-novel/scene-9@x"}, "author-review.unknown-key"}, {[]string{keys[0], keys[0]}, "input.invalid-value"}} {
		if code, o := pagePost(t, s, "/author-review", s.token, map[string][]string{"keys": bad.keys}); code != 400 || o["authorReview"] != nil || !slices.Contains(diagCodes(o), bad.code) {
			t.Errorf("review %v: %d %v", bad.keys, code, o)
		}
	}
	if _, err := os.Stat(filepath.Join(dir, ".writing-anything", "author-reviews.yaml")); err == nil {
		t.Fatal("rejected review wrote the ledger")
	}
	code, o = pagePost(t, s, "/author-review", s.token, map[string][]string{"keys": keys})
	if r, _ := o["authorReview"].(map[string]any); code != 200 || r["channel"] != "preview" || len(r["reviewed"].([]any)) != 1 {
		t.Fatalf("review: %d %v", code, o)
	}
	if l := readFile(t, filepath.Join(dir, ".writing-anything", "author-reviews.yaml")); !strings.Contains(l, "channel: preview") || !strings.Contains(l, "ref: my-novel/scene-1") {
		t.Fatalf("ledger: %s", l)
	}
	if code, res := runWS(t, dir, "assemble", "my-novel"); code != 0 {
		t.Fatalf("assemble after review: %d %+v", code, res.Diagnostics)
	}
	if code, res := runWS(t, dir, "validate", ".writing-anything/author-reviews.yaml"); code != 0 {
		t.Fatalf("validate ledger: %d %+v", code, res.Diagnostics)
	}
	if code, res := runWS(t, dir, "validate"); code != 0 || !slices.Contains(res.Checked, ".writing-anything/author-reviews.yaml") {
		t.Fatalf("validate: %d %v %+v", code, res.Checked, res.Diagnostics)
	}

	// 卷末检查点：命令行确认列为待复核；阅读页确认的（part-one）不需要复核。
	mustRunWS(t, dir, "volume", "confirm", "my-novel/part-two", "--confirmed-by-author", "--confirmation", "作者：下部无误")
	_, res = runWS(t, dir, "next")
	if keys := pendingKeys(t, res.stdout); len(keys) != 1 || !strings.HasPrefix(keys[0], "volume-checkpoint:my-novel/part-two@") {
		t.Fatalf("pending after volume confirms = %v", keys)
	}
	if code, res := runWS(t, dir, "assemble", "my-novel"); code != 1 || diag(t, res, "assemble.unreviewed").Path != ".writing-anything/works/my-novel/units/part-two/checkpoint.yaml" {
		t.Fatalf("assemble with cli checkpoint: %d %+v", code, res.Diagnostics)
	}

	// 改为 standard：只标出渠道，不列出也不阻塞；阅读页复核被拒。
	mustRunWS(t, dir, "config", "set", "gate_level", "standard")
	_, res = runWS(t, dir, "review", "show", "my-novel")
	if keys := pendingKeys(t, res.stdout); len(keys) != 0 {
		t.Fatalf("standard pending = %v", keys)
	}
	if code, res := runWS(t, dir, "assemble", "my-novel"); code != 0 {
		t.Fatalf("standard assemble: %d %+v", code, res.Diagnostics)
	}
	if code, _ := pagePost(t, s, "/author-review", s.token, map[string][]string{"keys": {"x"}}); code != 400 {
		t.Fatalf("standard review: %d", code)
	}
	if strings.Contains(pageGet(t, s), `id="author-review"`) {
		t.Fatal("standard page shows author review section")
	}
}

// 阅读页确认决策、规划步骤、状态变更提议与知情事件，记录渠道 preview；命令行确认记录渠道 cli。
func TestConfirmationChannelPageKinds(t *testing.T) {
	dir := volumeAcceptFixture(t, "standard")
	writeFile(t, filepath.Join(dir, ".writing-anything", "works", "my-novel", "canon.yaml"), `schema_version: 1
entries:
  - {id: canon-1, type: character, name: 守灯人, text: 五十岁, sources: [], protection: normal}
`)
	writeFile(t, filepath.Join(dir, ".writing-anything", "works", "my-novel", "state.yaml"), `schema_version: 1
events:
  - {id: state-1, scene: my-novel/scene-1, entry: canon-1, state: 受伤, status: proposed}
  - {id: state-2, scene: my-novel/scene-1, entry: canon-1, state: 痊愈, status: proposed}
`)
	writeFile(t, filepath.Join(dir, ".writing-anything", "works", "my-novel", "knowledge.yaml"), `schema_version: 1
events:
  - {id: know-1, scene: my-novel/scene-1, entry: canon-1, character: reader, status: proposed}
`)
	mustRunWS(t, dir, "decision", "add", "--question", "书名用哪个", "--option", "灯塔", "--option", "守灯人", "--unit", "my-novel")
	mustRunWS(t, dir, "decision", "add", "--question", "结尾写多长", "--unit", "my-novel")
	mustRunWS(t, dir, "new", "novel", "other-work")
	mustRunWS(t, dir, "decision", "add", "--question", "别的作品的事", "--option", "是", "--unit", "other-work")
	mustRunWS(t, dir, "plan", "step", "my-novel", "one-sentence", "--text", "一句话")

	var out bytes.Buffer
	s := newPage(dir, "my-novel", &out)
	page := pageGet(t, s)
	for _, want := range []string{"决策（Decision）dec-1：书名用哪个", "规划步骤 my-novel / one-sentence", "状态变更提议 state-1", "知情事件 know-1", `data-action="reject"`} {
		if !strings.Contains(page, want) {
			t.Errorf("page lacks %q", want)
		}
	}
	if strings.Contains(page, "别的作品的事") {
		t.Error("page lists another work's decision")
	}
	for _, c := range []struct {
		body map[string]string
		file string
		want string
	}{
		{map[string]string{"kind": "decision", "id": "dec-1", "option": "b", "confirmation": "作者：用守灯人"}, "decisions.yaml", "channel: preview"},
		{map[string]string{"kind": "decision", "id": "dec-2", "answer": "两千字", "confirmation": "作者：两千字"}, "decisions.yaml", "answer: 两千字"},
		{map[string]string{"kind": "plan", "unit": "my-novel", "id": "one-sentence", "confirmation": "作者：就这句"}, "works/my-novel/plan.yaml", "channel: preview"},
		{map[string]string{"kind": "state", "id": "state-1", "confirmation": "作者：受伤属实"}, "works/my-novel/state.yaml", "channel: preview"},
		{map[string]string{"kind": "knowledge", "id": "know-1", "action": "reject", "confirmation": "作者：读者此时不知道"}, "works/my-novel/knowledge.yaml", "status: rejected"},
	} {
		code, o := pagePost(t, s, "/confirm", s.token, c.body)
		if conf, _ := o["confirmation"].(map[string]any); code != 200 || conf["channel"] != "preview" {
			t.Fatalf("%v: %d %v", c.body, code, o)
		}
		if f := readFile(t, filepath.Join(dir, ".writing-anything", c.file)); !strings.Contains(f, c.want) {
			t.Fatalf("%v: %s lacks %q:\n%s", c.body, c.file, c.want, f)
		}
		// 已确认的事项离开队列：重复提交被拒。
		if code, _ := pagePost(t, s, "/confirm", s.token, c.body); code != 400 {
			t.Fatalf("%v repeated: %d", c.body, code)
		}
	}
	// 决策缺选项时报告决策队列的错误码。
	mustRunWS(t, dir, "decision", "add", "--question", "再选一次", "--option", "甲", "--option", "乙", "--unit", "my-novel")
	if code, o := pagePost(t, s, "/confirm", s.token, map[string]string{"kind": "decision", "id": "dec-4", "confirmation": "作者"}); code != 400 || !slices.Contains(diagCodes(o), "decision.choice-required") {
		t.Fatalf("choice required: %d %v", code, o)
	}
	// 命令行渠道写 cli，--json 输出带 channel；待确认条目的 channel 为 null。
	_, res := runWS(t, dir, "state", "confirm", "my-novel", "state-2", "--confirmed-by-author", "--confirmation", "作者：痊愈")
	if !strings.Contains(res.stdout, `"channel":"cli"`) {
		t.Fatalf("state confirm: %s", res.stdout)
	}
	_, res = runWS(t, dir, "decide", "dec-4", "--option", "a", "--confirmation", "作者：甲")
	if !strings.Contains(res.stdout, `"channel":"cli"`) {
		t.Fatalf("decide: %s", res.stdout)
	}
	_, res = runWS(t, dir, "knowledge", "list", "my-novel", "--all")
	if !strings.Contains(res.stdout, `"channel":"preview"`) {
		t.Fatalf("knowledge list: %s", res.stdout)
	}
	_, res = runWS(t, dir, "plan", "show", "my-novel")
	if !strings.Contains(res.stdout, `"channel":"preview"`) || !strings.Contains(res.stdout, `"channel":null`) {
		t.Fatalf("plan show: %s", res.stdout)
	}
	if !strings.Contains(out.String(), "作者在阅读页确认（渠道 preview）：decision dec-1") {
		t.Fatalf("stdout = %q", out.String())
	}
}
