package cli

import (
	"bytes"
	"encoding/json"
	"maps"
	"net/http"
	"net/http/httptest"
	"slices"
	"strings"
	"testing"

	"github.com/kw12121212/writing-anything/internal/preview"
)

// 本文件是阅读页数据（GET /page.json，preview-page-json）与 next 的待作者确认事项（next-pending-confirmations）的测试。

// pageData 是测试读取的 page.json 字段。
type pageData struct {
	Title string `json:"title"`
	Units []struct {
		Ref        string            `json:"ref"`
		HasDraft   bool              `json:"hasDraft"`
		Global     []json.RawMessage `json:"global"`
		Notes      []string          `json:"notes"`
		Paragraphs []struct {
			Index    int               `json:"index"`
			Text     string            `json:"text"`
			Origin   *string           `json:"origin"`
			Findings []json.RawMessage `json:"findings"`
		} `json:"paragraphs"`
	} `json:"units"`
	Confirms []struct {
		Kind    string `json:"kind"`
		ID      string `json:"id"`
		Options []struct {
			ID string `json:"id"`
		} `json:"options"`
	} `json:"confirms"`
	Strict   bool              `json:"strict"`
	Reviews  []json.RawMessage `json:"reviews"`
	Failures []json.RawMessage `json:"failures"`
}

// pageRequest 以 method 请求阅读页的 path（含查询参数），返回响应记录。
func pageRequest(s *previewServer, method, path string, mutate ...func(*http.Request)) *httptest.ResponseRecorder {
	req := httptest.NewRequest(method, "http://"+pageHost+path, nil)
	for _, m := range mutate {
		m(req)
	}
	rec := httptest.NewRecorder()
	s.ServeHTTP(rec, req)
	return rec
}

// pageWork 返回作品 my-post：第一段由作者直接写入正文、第二段由 Agent 经 draft write 写入，子单元 intro 没有正文；
// 决策队列中有关联 my-post 的 open 决策 dec-1（选项 a、b）。
func pageWork(t *testing.T) string {
	t.Helper()
	dir := previewWork(t, "连接池要复用。\n")
	draft := wsPath(dir, ".writing-anything/works/my-post/draft.md")
	writeFile(t, draft, "作者亲手写的开头。\n\n"+readFile(t, draft))
	mustRunWS(t, dir, "decision", "add", "--question", "标题用哪个？", "--option", "甲", "--option", "乙", "--unit", "my-post", "--id", "dec-1")
	return dir
}

func TestPreviewPageJSON(t *testing.T) {
	dir := pageWork(t)
	before := workspaceFiles(t, dir)
	s := newPage(dir, "my-post", &bytes.Buffer{})

	// page-json-content：内容、出处、待作者确认事项与 JSON Schema。
	rec := pageRequest(s, http.MethodGet, "/page.json?token=page-token")
	if rec.Code != 200 || rec.Header().Get("Content-Type") != "application/json; charset=utf-8" || rec.Header().Get("Cache-Control") != "no-store" {
		t.Fatalf("GET /page.json: %d %v %s", rec.Code, rec.Header(), rec.Body.String())
	}
	body := rec.Body.Bytes()
	if err := preview.ValidateJSON(body); err != nil {
		t.Fatalf("page.json does not match its schema: %v\n%s", err, body)
	}
	var p pageData
	if err := json.Unmarshal(body, &p); err != nil {
		t.Fatal(err)
	}
	if p.Title != "my-post" || len(p.Units) != 2 || p.Units[0].Ref != "my-post" || p.Units[1].Ref != "my-post/intro" {
		t.Fatalf("units: %s", body)
	}
	var origins []string
	for _, para := range p.Units[0].Paragraphs {
		if para.Origin == nil {
			origins = append(origins, "null")
		} else {
			origins = append(origins, *para.Origin)
		}
	}
	if !slices.Equal(origins, []string{"human", "agent"}) || p.Units[0].Paragraphs[0].Text != "作者亲手写的开头。" {
		t.Fatalf("paragraph origins = %v: %s", origins, body)
	}
	if len(p.Confirms) != 1 || p.Confirms[0].Kind != "decision" || p.Confirms[0].ID != "dec-1" || len(p.Confirms[0].Options) != 2 ||
		p.Confirms[0].Options[0].ID != "a" || p.Confirms[0].Options[1].ID != "b" {
		t.Fatalf("confirms: %s", body)
	}

	// page-json-empty-arrays：没有正文的单元与 standard 关卡下的空区块都是空数组，不是 null。
	intro := p.Units[1]
	if intro.HasDraft || intro.Paragraphs == nil || len(intro.Paragraphs) != 0 || intro.Global == nil || intro.Notes == nil ||
		p.Strict || p.Reviews == nil || len(p.Reviews) != 0 || p.Failures == nil {
		t.Fatalf("empty areas: %s", body)
	}
	if strings.Contains(string(body), "null") && !strings.Contains(string(body), `"origin":null`) {
		t.Fatalf("an array encoded as null: %s", body)
	}

	// page-json-matches-page：同一时刻的 HTML 阅读页列出同样的单元、段落与待作者确认事项（待作者复核项见 TestPreviewPageJSONStrict）。
	html := pageGet(t, s)
	for _, u := range p.Units {
		if !strings.Contains(html, `<section class="unit" data-unit="`+u.Ref+`">`) {
			t.Fatalf("HTML has no unit %s", u.Ref)
		}
	}
	if n := strings.Count(html, `<section class="unit"`); n != len(p.Units) {
		t.Fatalf("HTML units = %d, page.json = %d", n, len(p.Units))
	}
	if n := strings.Count(html, `<div class="text">`); n != len(p.Units[0].Paragraphs) {
		t.Fatalf("HTML paragraphs = %d, page.json = %d", n, len(p.Units[0].Paragraphs))
	}
	if n := strings.Count(html, `<div class="confirm"`); n != len(p.Confirms) || !strings.Contains(html, `data-id="dec-1"`) {
		t.Fatalf("HTML confirms = %d, page.json = %d", n, len(p.Confirms))
	}

	// page-json-rejected：缺令牌、错令牌与非法 Host 都得到 403，不返回任何阅读页数据。
	for name, rec := range map[string]*httptest.ResponseRecorder{
		"no token":    pageRequest(s, http.MethodGet, "/page.json"),
		"bad token":   pageRequest(s, http.MethodGet, "/page.json?token=forged"),
		"header only": pageRequest(s, http.MethodGet, "/page.json", func(r *http.Request) { r.Header.Set("X-WA-Token", "page-token") }),
		"evil host":   pageRequest(s, http.MethodGet, "/page.json?token=page-token", func(r *http.Request) { r.Host = "evil.example:4170" }),
	} {
		if rec.Code != 403 || strings.Contains(rec.Body.String(), "连接池") || strings.Contains(rec.Body.String(), "dec-1") {
			t.Errorf("%s: %d %s", name, rec.Code, rec.Body.String())
		}
	}
	// 其他方法返回 405；HEAD 与 GET 相同地放行。
	if rec := pageRequest(s, http.MethodPost, "/page.json", func(r *http.Request) { r.Header.Set("X-WA-Token", "page-token") }); rec.Code != 405 {
		t.Errorf("POST /page.json: %d", rec.Code)
	}
	if rec := pageRequest(s, http.MethodHead, "/page.json?token=page-token"); rec.Code != 200 {
		t.Errorf("HEAD /page.json: %d", rec.Code)
	}

	// 只读：读取阅读页数据不创建或修改任何产物（audit.log 除外）。
	if after := workspaceFiles(t, dir); !maps.Equal(before, after) {
		t.Fatalf("page.json changed the workspace")
	}
}

// 正文中的 HTML 按原文给出，由消费方负责安全显示（page.json 不做 HTML 转义）。
func TestPreviewPageJSONRawText(t *testing.T) {
	dir := previewWork(t, "<b>粗体</b> & 引号\n")
	rec := pageRequest(newPage(dir, "my-post", &bytes.Buffer{}), http.MethodGet, "/page.json?token=page-token")
	var p pageData
	if err := json.Unmarshal(rec.Body.Bytes(), &p); err != nil || len(p.Units) == 0 || len(p.Units[0].Paragraphs) != 1 ||
		p.Units[0].Paragraphs[0].Text != "<b>粗体</b> & 引号" {
		t.Fatalf("raw text: %v %s", err, rec.Body.String())
	}
}

// 无法加载的单元引用（作品在服务启动后被删掉）时返回 500，不输出半截数据。
func TestPreviewPageJSONUnitGone(t *testing.T) {
	dir := previewWork(t, "连接池要复用。\n")
	rec := pageRequest(newPage(dir, "no-such-work", &bytes.Buffer{}), http.MethodGet, "/page.json?token=page-token")
	if rec.Code != 500 || strings.Contains(rec.Body.String(), `"units"`) {
		t.Fatalf("unit gone: %d %s", rec.Code, rec.Body.String())
	}
}

type pendingConfirmation struct {
	Kind    string   `json:"kind"`
	Work    string   `json:"work"`
	Units   []string `json:"units"`
	ID      *string  `json:"id"`
	Label   string   `json:"label"`
	Command string   `json:"command"`
}

func nextPending(t *testing.T, dir string, args ...string) []pendingConfirmation {
	t.Helper()
	code, res := runWS(t, dir, append([]string{"next"}, args...)...)
	var out struct {
		Pending []pendingConfirmation `json:"pendingConfirmations"`
	}
	var raw struct {
		Pending []map[string]json.RawMessage `json:"pendingConfirmations"`
	}
	if err := json.Unmarshal([]byte(res.stdout), &out); err != nil || code != 0 || out.Pending == nil || json.Unmarshal([]byte(res.stdout), &raw) != nil {
		t.Fatalf("next: exit code = %d, err = %v, stdout = %s", code, err, res.stdout)
	}
	// enum-scoped-output-fields：id 只在有条目 id 的 kind 出现，acceptance 与 volume 不含该键。
	for i, item := range raw.Pending {
		_, has := item["id"]
		if want := out.Pending[i].Kind != "acceptance" && out.Pending[i].Kind != "volume"; has != want {
			t.Errorf("pendingConfirmations[%d] (%s): has id key = %v, want %v", i, out.Pending[i].Kind, has, want)
		}
	}
	return out.Pending
}

func TestNextPendingConfirmations(t *testing.T) {
	// pending-none：刚建的作品还没有任何待作者确认事项，输出空数组。
	dir := previewWork(t, "连接池要复用。\n")
	if got := nextPending(t, dir); len(got) != 0 {
		t.Fatalf("pending-none: %+v", got)
	}

	// pending-acceptance：当前阶段必需产物含 acceptance 且没有验收记录。
	setStage(t, dir, "my-post", "my-post", "brief", "review")
	got := nextPending(t, dir)
	want := shellCommand("wa-cli", "preview", "my-post", "--serve", "--dir", dir, "--json")
	if len(got) != 1 || got[0].Kind != "acceptance" || got[0].Work != "my-post" || !slices.Equal(got[0].Units, []string{"my-post"}) ||
		got[0].ID != nil || got[0].Command != want || !strings.Contains(got[0].Label, "my-post") {
		t.Fatalf("pending-acceptance: %+v (want command %q)", got, want)
	}

	// pending-decision：关联 my-post 的 open 决策；不关联任何单元的决策归到作品根单元。
	mustRunWS(t, dir, "decision", "add", "--question", "标题用哪个？", "--option", "甲", "--option", "乙", "--unit", "my-post/intro", "--id", "dec-1")
	mustRunWS(t, dir, "decision", "add", "--question", "署名用哪个？", "--option", "甲", "--option", "乙", "--id", "dec-2")
	got = nextPending(t, dir)
	kinds := map[string]pendingConfirmation{}
	for _, c := range got {
		key := c.Kind
		if c.ID != nil {
			key += ":" + *c.ID
		}
		kinds[key] = c
	}
	if d := kinds["decision:dec-1"]; len(got) != 3 || !slices.Equal(d.Units, []string{"my-post/intro"}) || d.Work != "my-post" ||
		!slices.Equal(kinds["decision:dec-2"].Units, []string{"my-post"}) {
		t.Fatalf("pending-decision: %+v", got)
	}

	// pending-matches-page：与同一时刻 page.json 的待作者确认区在 kind 与 id 上一一对应。
	rec := pageRequest(newPage(dir, "my-post", &bytes.Buffer{}), http.MethodGet, "/page.json?token=page-token")
	var p pageData
	if err := json.Unmarshal(rec.Body.Bytes(), &p); err != nil {
		t.Fatal(err)
	}
	var fromPage, fromNext []string
	for _, c := range p.Confirms {
		fromPage = append(fromPage, c.Kind+":"+c.ID)
	}
	for _, c := range got {
		id := ""
		if c.ID != nil {
			id = *c.ID
		}
		fromNext = append(fromNext, c.Kind+":"+id)
	}
	if !slices.Equal(fromPage, fromNext) {
		t.Fatalf("page.json confirms %v != next pendingConfirmations %v", fromPage, fromNext)
	}

	// 给出 unit 时只列该单元所在作品的事项；另一个作品的事项不出现。
	mustRunWS(t, dir, "new", "minimal", "other-post")
	if got := nextPending(t, dir, "other-post"); len(got) != 1 || got[0].Work != "other-post" || got[0].Kind != "decision" {
		t.Fatalf("scoped to other-post: %+v", got)
	}

	// 人读输出在末尾列出事项与打开阅读页的命令。
	code, res, _ := runIn(t, "", "next", "--dir", dir)
	if code != 0 || !strings.Contains(res, "待作者确认：") || !strings.Contains(res, "请作者打开阅读页确认：") {
		t.Fatalf("text: exit code = %d, stdout = %q", code, res)
	}
}

// strict 关卡下 page.json 的待作者复核项与同一时刻 HTML 阅读页的待作者复核区一致。
func TestPreviewPageJSONStrict(t *testing.T) {
	dir := previewWork(t, "连接池要复用。\n")
	mustRunWS(t, dir, "config", "set", "gate_level", "strict")
	setStage(t, dir, "my-post", "my-post", "brief", "review")
	review := "rubric:\n  - {item: clarity, verdict: pass, evidence: 清楚, paragraphs: [1]}\nfindings: []\n"
	if code, _ := runReview(t, dir, review, "review", "record", "my-post"); code != 0 {
		t.Fatal("review record failed")
	}
	if code, res := runWS(t, dir, "accept", "my-post", "--confirmed-by-author", "--confirmation", "作者：可以"); code != 0 {
		t.Fatalf("accept: %s", res.stdout)
	}
	s := newPage(dir, "my-post", &bytes.Buffer{})
	rec := pageRequest(s, http.MethodGet, "/page.json?token=page-token")
	var p struct {
		Strict  bool `json:"strict"`
		Reviews []struct {
			Key string `json:"key"`
		} `json:"reviews"`
	}
	if err := json.Unmarshal(rec.Body.Bytes(), &p); err != nil || !p.Strict || len(p.Reviews) == 0 {
		t.Fatalf("strict reviews: %v %s", err, rec.Body.String())
	}
	if err := preview.ValidateJSON(rec.Body.Bytes()); err != nil {
		t.Fatal(err)
	}
	html := pageGet(t, s)
	for _, r := range p.Reviews {
		if !strings.Contains(html, `<input type="checkbox" value="`+r.Key+`" checked>`) {
			t.Fatalf("HTML has no review item %s", r.Key)
		}
	}
	if n := strings.Count(html, `<label><input type="checkbox" value=`); n != len(p.Reviews) {
		t.Fatalf("HTML review items = %d, page.json = %d", n, len(p.Reviews))
	}
}
