package cli

import (
	"encoding/json"
	"fmt"
	"os"
	"path/filepath"
	"slices"
	"strings"
	"testing"
	"time"

	"github.com/kw12121212/writing-anything/internal/review"
)

// reviewResult 是审稿与验收命令 --json 输出中测试关心的部分。
type reviewResult struct {
	result
	Review *struct {
		Path      string `json:"path"`
		Stale     bool   `json:"stale"`
		DraftSHA  string `json:"draftSha256"`
		Rubric    []map[string]any
		Findings  []map[string]any
		Reaffirms []map[string]any `json:"reaffirms"`
	} `json:"review"`
	Lint struct {
		Ran    bool `json:"ran"`
		Errors int  `json:"errors"`
	} `json:"lint"`
	Revisions []map[string]any `json:"revisions"`
	Decisions []map[string]any `json:"decisions"`
	Gate      struct {
		Level                string `json:"level"`
		Ready                bool   `json:"ready"`
		ConfirmationRequired bool   `json:"confirmationRequired"`
		Blockers             []struct {
			Code string  `json:"code"`
			Ref  *string `json:"ref"`
		} `json:"blockers"`
	} `json:"gate"`
	Acceptance *struct {
		GateLevel         string  `json:"gateLevel"`
		ConfirmedByAuthor bool    `json:"confirmedByAuthor"`
		Confirmation      *string `json:"confirmation"`
		AcceptedAt        string  `json:"acceptedAt"`
		Stale             *bool   `json:"stale"`
	} `json:"acceptance"`
	Finding map[string]any `json:"finding"`
}

func runReview(t *testing.T, dir, input string, args ...string) (int, reviewResult) {
	t.Helper()
	code, res := runWSIn(t, dir, input, args...)
	var out reviewResult
	if err := json.Unmarshal([]byte(res.stdout), &out); err != nil {
		t.Fatal(err)
	}
	out.result = res
	return code, out
}

// acceptanceFor 返回与作品 work 当前正文一致的 acceptance.yaml 内容（standard，未经作者确认）。
func acceptanceFor(t *testing.T, dir, work string) string {
	t.Helper()
	data, err := os.ReadFile(filepath.Join(dir, ".writing-anything", "works", work, "draft.md"))
	if err != nil {
		t.Fatal(err)
	}
	return fmt.Sprintf("schema_version: 1\ngate_level: standard\ndraft_sha256: %s\nconfirmed_by_author: false\naccepted_at: \"2026-10-01T00:00:00Z\"\n", review.SHA256(data))
}

func gateCodes(r reviewResult) []string {
	var out []string
	for _, b := range r.Gate.Blockers {
		out = append(out, b.Code)
	}
	return out
}

const reviewDraft = "连接池让请求复用连接。\n\n第二段可能还需要例子。\n"

// reviewWork 创建推进到审稿阶段、写入了正文的作品 my-post。
func reviewWork(t *testing.T) string {
	t.Helper()
	dir := newWork(t)
	writeFile(t, filepath.Join(dir, ".writing-anything", "works", "my-post", "brief.yaml"), completeBrief)
	mustRunWS(t, dir, "advance", "my-post")
	if code, res := runWSIn(t, dir, reviewDraft, "draft", "write", "my-post"); code != 0 {
		t.Fatalf("draft write: %+v", res)
	}
	// 草稿阶段不能验收（accept.wrong-stage）。
	if code, res := runReview(t, dir, "", "accept", "my-post"); code != 1 || !hasNext(diag(t, res.result, "accept.wrong-stage"), "wa-cli next my-post") {
		t.Fatalf("wrong stage: code = %d, %+v", code, res.Diagnostics)
	}
	mustRunWS(t, dir, "advance", "my-post")
	return dir
}

const goodReview = `rubric:
  - {item: clarity, verdict: fail, evidence: 第二段没有给出结论, paragraphs: [2]}
findings:
  - {id: rf-1, paragraph: 1, message: 标题可以更具体}
  - {rubric: clarity, paragraph: 2, quote: 可能, message: 结论不明确, suggestion: 先写结论}
`

func TestReviewRecord(t *testing.T) {
	dir := reviewWork(t)
	rel := ".writing-anything/works/my-post/review.yaml"
	// 没有审稿时 next 给出记录审稿的命令（next-steps）。
	code, res := runWS(t, dir, "next", "my-post")
	if code != 0 || !strings.Contains(res.stdout, "wa-cli help review record --json") || !strings.Contains(res.stdout, "wa-cli review show my-post") ||
		!strings.Contains(res.stdout, `"artifact":"acceptance"`) {
		t.Fatalf("next without review: %s", res.stdout)
	}
	for _, tc := range []struct {
		input, code, field string
	}{
		{"rubric: []\n", "review.missing-verdict", "rubric"},
		{"rubric:\n  - {item: clarity, verdict: pass, evidence: x, paragraphs: [1]}\n  - {item: nope, verdict: pass, evidence: x, paragraphs: [1]}\n", "review.unknown-rubric-item", "rubric[1].item"},
		{"rubric:\n  - {item: clarity, verdict: pass, evidence: x, paragraphs: [1]}\n  - {item: clarity, verdict: fail, evidence: x, paragraphs: [1]}\n", "review.duplicate-verdict", "rubric[1].item"},
		{"rubric:\n  - {item: clarity, verdict: pass, evidence: x, paragraphs: [3]}\n", "review.unknown-paragraph", "rubric[0].paragraphs[0]"},
		{"rubric:\n  - {item: clarity, verdict: pass, evidence: x, paragraphs: [1]}\nfindings:\n  - {paragraph: 1, quote: 例子, message: x}\n", "review.quote-not-found", "findings[0].quote"},
		{"rubric:\n  - {item: clarity, verdict: pass, evidence: x, paragraphs: [1]}\nfindings:\n  - {paragraph: 1, message: x, rubric: nope}\n", "review.unknown-rubric-item", "findings[0].rubric"},
		{"rubric:\n  - {item: clarity, verdict: pass, evidence: x, paragraphs: [1]}\nfindings:\n  - {id: a, paragraph: 1, message: x}\n  - {id: a, paragraph: 2, message: y}\n", "artifact.duplicate-id", "findings[1].id"},
		{"rubric:\n  - {item: clarity, verdict: maybe, evidence: x, paragraphs: [1]}\n", "artifact.schema-violation", "rubric[0].verdict"},
		{"rubric: [\n", "artifact.parse-error", ""},
	} {
		code, res := runReview(t, dir, tc.input, "review", "record", "my-post")
		d := diag(t, res.result, tc.code)
		if code != 1 || res.Review != nil || d.Field != tc.field || d.Path != "" || d.Next[0] != "wa-cli help review record --json" {
			t.Errorf("%s: code = %d, %+v", tc.code, code, d)
		}
	}
	if _, err := os.Stat(wsPath(dir, rel)); err == nil {
		t.Fatal("rejected input wrote review.yaml")
	}
	code, rr := runReview(t, dir, goodReview, "review", "record", "my-post")
	if code != 0 || !slices.Equal(rr.ChangedPaths, []string{rel}) || rr.Review == nil || rr.Review.Stale || len(rr.Review.Findings) != 2 ||
		rr.Review.Findings[1]["id"] != "rf-2" || rr.Review.Findings[1]["status"] != "open" || rr.Review.Rubric[0]["title"] != "清晰" {
		t.Fatalf("record: code = %d, %+v", code, rr)
	}
	if got := readFile(t, wsPath(dir, rel)); !strings.Contains(got, "draft_sha256: "+review.SHA256([]byte(reviewDraft))) {
		t.Errorf("review.yaml:\n%s", got)
	}
	// 单个产物与整体校验都检查 review.yaml。
	if code, res := runWS(t, dir, "validate", rel); code != 0 || !slices.Equal(res.Checked, []string{rel}) {
		t.Errorf("validate review.yaml: code = %d, %+v", code, res)
	}
	writeFile(t, wsPath(dir, rel), strings.Replace(readFile(t, wsPath(dir, rel)), "id: rf-2", "id: rf-1", 1))
	if code, res := runWS(t, dir, "validate"); code != 1 || diag(t, res, "artifact.duplicate-id").Path != rel {
		t.Errorf("validate duplicate finding: code = %d, %+v", code, res.Diagnostics)
	}
	// 再次记录整体替换审稿；文件读取失败给出用法。
	if code, _ := runReview(t, dir, goodReview, "review", "record", "my-post"); code != 0 {
		t.Fatal("re-record failed")
	}
	if code, res := runReview(t, dir, "", "review", "record", "my-post", "--file", "nope.yaml"); code != 1 ||
		!concreteFirst(diag(t, res.result, "input.read-failed"), shellCommand("wa-cli", "review", "show", "my-post", "--dir", dir, "--json"), "wa-cli help review record --json") {
		t.Errorf("missing file: %+v", res.Diagnostics)
	}
	// 没有正文时不能审稿。
	os.Remove(wsPath(dir, ".writing-anything/works/my-post/draft.md"))
	if code, res := runReview(t, dir, goodReview, "review", "record", "my-post"); code != 1 || diag(t, res.result, "draft.not-found").Path == "" {
		t.Errorf("no draft: %+v", res.Diagnostics)
	}
}

func TestReviewShowResolveAccept(t *testing.T) {
	dir := reviewWork(t)
	show := func() reviewResult {
		t.Helper()
		code, res := runReview(t, dir, "", "review", "show", "my-post")
		if code != 0 || !res.Lint.Ran {
			t.Fatalf("review show: code = %d, %+v", code, res)
		}
		return res
	}
	// 没有审稿：standard 下阻塞验收。
	if s := show(); s.Review != nil || s.Gate.Level != "standard" || s.Gate.Ready || !slices.Equal(gateCodes(s), []string{"accept.review-missing"}) || s.Gate.ConfirmationRequired {
		t.Fatalf("no review: %+v", s)
	}
	runReview(t, dir, goodReview, "review", "record", "my-post")
	mustRunWS(t, dir, "feedback", "add", "my-post", "--text", "多举一个例子")
	mustRunWS(t, dir, "decision", "add", "--question", "标题用哪个", "--option", "甲", "--unit", "my-post")
	s := show()
	if len(s.Revisions) != 3 || s.Revisions[0]["source"] != "review" || s.Revisions[2]["source"] != "feedback" || s.Revisions[2]["id"] != "fb-1" ||
		len(s.Decisions) != 1 || s.Decisions[0]["blocking"] != false ||
		!slices.Equal(gateCodes(s), []string{"accept.open-finding", "accept.open-finding", "accept.open-feedback"}) || *s.Gate.Blockers[2].Ref != "fb-1" {
		t.Fatalf("revisions: %+v", s)
	}
	// 修订任务未处理时 standard 下不能验收，诊断给出能复查它的 review show。
	code, res := runReview(t, dir, "", "accept", "my-post")
	if d := diag(t, res.result, "accept.open-finding"); code != 1 || res.Acceptance != nil || !hasNext(d, "wa-cli review show my-post") || !hasNext(d, "wa-cli help review resolve") {
		t.Fatalf("accept with revisions: code = %d, %+v", code, res.Diagnostics)
	}
	if !hasNext(diag(t, res.result, "accept.open-feedback"), "wa-cli feedback list my-post") {
		t.Fatalf("feedback next: %+v", res.Diagnostics)
	}
	// review resolve：waived 须写明理由；取值未变时不写文件；找不到发现项。
	rel := ".writing-anything/works/my-post/review.yaml"
	writeFile(t, wsPath(dir, rel), strings.Replace(readFile(t, wsPath(dir, rel)), "findings:", "# 作者的注释\nfindings:", 1))
	if code, res := runReview(t, dir, "", "review", "resolve", "my-post", "rf-2", "--status", "waived"); code != 1 || !slices.Equal(diag(t, res.result, "review.missing-note").Next,
		[]string{"wa-cli review show my-post --dir " + dir + " --json", "wa-cli help review resolve --json"}) {
		t.Fatalf("waive without note: %+v", res.Diagnostics)
	}
	code, rs := runReview(t, dir, "", "review", "resolve", "my-post", "rf-2", "--status", "waived", "--note", "作者说保留")
	if code != 0 || rs.Finding["status"] != "waived" || rs.Finding["note"] != "作者说保留" || !slices.Equal(rs.ChangedPaths, []string{rel}) {
		t.Fatalf("waive: %+v", rs)
	}
	if !strings.Contains(readFile(t, wsPath(dir, rel)), "# 作者的注释") {
		t.Error("resolve dropped the author's comment")
	}
	mustRunWS(t, dir, "review", "resolve", "my-post", "rf-1")
	if code, rs := runReview(t, dir, "", "review", "resolve", "my-post", "rf-1"); code != 0 || len(rs.ChangedPaths) != 0 {
		t.Errorf("unchanged resolve: %+v", rs)
	}
	for _, args := range [][]string{{"rf-9"}, {"rf-1", "--status", "done"}, {"rf-1", "--note", " "}} {
		code, res := runReview(t, dir, "", append([]string{"review", "resolve", "my-post"}, args...)...)
		if code != 1 || res.Finding != nil {
			t.Errorf("%v: code = %d, %+v", args, code, res.Diagnostics)
		}
	}
	if _, res := runReview(t, dir, "", "review", "resolve", "my-post", "rf-9"); !hasNext(diag(t, res.result, "review.finding-not-found"), "wa-cli review show my-post") {
		t.Errorf("not found next: %+v", res.Diagnostics)
	}
	// 锚点值无法只改一处：先给出审稿汇总，再给出用法（cli/diagnostic-next-concrete）。
	reviewed := readFile(t, wsPath(dir, rel))
	writeFile(t, wsPath(dir, rel), strings.Replace(reviewed, "status: resolved", "status: &st resolved", 1))
	if code, res := runReview(t, dir, "", "review", "resolve", "my-post", "rf-1", "--status", "open"); code != 1 ||
		!concreteFirst(diag(t, res.result, "review.anchored-value"), shellCommand("wa-cli", "review", "show", "my-post", "--dir", dir, "--json"), "wa-cli help review resolve --json") {
		t.Errorf("anchored: code = %d, %+v\n%s", code, res.Diagnostics, readFile(t, wsPath(dir, rel)))
	}
	writeFile(t, wsPath(dir, rel), reviewed)
	mustRunWS(t, dir, "feedback", "resolve", "my-post", "fb-1")
	if s := show(); !s.Gate.Ready || len(s.Revisions) != 0 {
		t.Fatalf("ready: %+v", s)
	}
	// --confirmed-by-author 与 --confirmation 必须同时给出。
	for _, args := range [][]string{{"--confirmed-by-author"}, {"--confirmation", "可以"}, {"--confirmed-by-author", "--confirmation", " "}} {
		code, res := runReview(t, dir, "", append([]string{"accept", "my-post"}, args...)...)
		if code != 1 || !slices.Equal(diag(t, res.result, "accept.incomplete-confirmation").Next,
			[]string{"wa-cli review show my-post --dir " + dir + " --json", "wa-cli help accept --json"}) {
			t.Errorf("%v: code = %d, %+v", args, code, res.Diagnostics)
		}
		if _, err := os.Stat(wsPath(dir, ".writing-anything/works/my-post/acceptance.yaml")); err == nil {
			t.Errorf("%v: acceptance.yaml written without complete confirmation", args)
		}
	}
	// standard 下未经作者确认也可以验收，以 warning 报告。
	now = func() time.Time { return time.Date(2026, 10, 1, 8, 0, 0, 0, time.FixedZone("CST", 8*3600)) }
	defer func() { now = time.Now }()
	arel := ".writing-anything/works/my-post/acceptance.yaml"
	code, res = runReview(t, dir, "", "accept", "my-post")
	if d := diag(t, res.result, "accept.unconfirmed"); code != 0 || d.Severity != "warning" || res.Acceptance == nil || res.Acceptance.ConfirmedByAuthor ||
		res.Acceptance.AcceptedAt != "2026-10-01T00:00:00Z" || !slices.Equal(res.ChangedPaths, []string{arel}) {
		t.Fatalf("accept standard: code = %d, %+v", code, res)
	}
	if s := show(); s.Acceptance == nil || *s.Acceptance.Stale || s.Acceptance.GateLevel != "standard" {
		t.Fatalf("show acceptance: %+v", s.Acceptance)
	}
	// 验收后正文被修改：审稿与验收都过期，advance 与 next 报告 acceptance.stale。
	if code, _ := runWSIn(t, dir, reviewDraft+"\n补一段。\n", "draft", "write", "my-post"); code != 0 {
		t.Fatal("rewrite draft")
	}
	code, ar := runWS(t, dir, "advance", "my-post")
	if d := diag(t, ar, "acceptance.stale"); code != 1 || d.Path != arel || !hasNext(d, "wa-cli review show my-post") {
		t.Fatalf("stale acceptance: code = %d, %+v", code, ar.Diagnostics)
	}
	if _, nr := runWS(t, dir, "next", "my-post"); !strings.Contains(nr.stdout, "acceptance.stale") || !strings.Contains(nr.stdout, "wa-cli review show my-post") {
		t.Errorf("next stale: %s", nr.stdout)
	}
	if s := show(); s.Review == nil || !s.Review.Stale || !*s.Acceptance.Stale || !slices.Equal(gateCodes(s), []string{"accept.review-stale"}) {
		t.Fatalf("stale review: %+v", s)
	}
	// strict：阻塞评分项与作者确认。
	mustRunWS(t, dir, "config", "set", "gate_level", "strict")
	runReview(t, dir, strings.Replace(goodReview, "paragraphs: [2]", "paragraphs: [3]", 1), "review", "record", "my-post")
	mustRunWS(t, dir, "review", "resolve", "my-post", "rf-1")
	mustRunWS(t, dir, "review", "resolve", "my-post", "rf-2")
	if s := show(); !s.Gate.Ready || !s.Gate.ConfirmationRequired {
		t.Fatalf("strict show: %+v", s.Gate)
	}
	code, res = runReview(t, dir, "", "accept", "my-post")
	if d := diag(t, res.result, "accept.unconfirmed"); code != 1 || d.Severity != "error" || !hasNext(d, "wa-cli help accept") {
		t.Fatalf("strict unconfirmed: code = %d, %+v", code, res.Diagnostics)
	}
	code, res = runReview(t, dir, "", "accept", "my-post", "--confirmed-by-author", "--confirmation", "作者：可以发了")
	if code != 0 || len(res.Diagnostics) != 0 || !res.Acceptance.ConfirmedByAuthor || *res.Acceptance.Confirmation != "作者：可以发了" || res.Acceptance.GateLevel != "strict" {
		t.Fatalf("strict accept: code = %d, %+v", code, res)
	}
	if code, res := runWS(t, dir, "validate"); code != 0 || !slices.Contains(res.Checked, arel) {
		t.Errorf("validate acceptance: code = %d, %+v", code, res.Checked)
	}
	if code, ur := runUnits(t, dir, "advance", "my-post"); code != 0 || ur.Unit["stage"] != "done" {
		t.Fatalf("advance after accept: code = %d, %+v", code, ur)
	}
}

// 所有关卡强度下，文本检查的 error 级发现项与阻塞决策都阻塞验收；fast 下不要求审稿。
func TestAcceptHardRules(t *testing.T) {
	dir := reviewWork(t)
	mustRunWS(t, dir, "config", "set", "gate_level", "fast")
	if code, res := runWSIn(t, dir, "值得注意的是，连接池很重要。\n", "draft", "write", "my-post"); code != 0 {
		t.Fatalf("draft write: %+v", res)
	}
	mustRunWS(t, dir, "decision", "add", "--question", "要不要发", "--option", "发", "--unit", "my-post", "--blocking")
	code, res := runReview(t, dir, "", "accept", "my-post")
	if code != 1 || !hasNext(diag(t, res.result, "accept.lint-errors"), "wa-cli lint my-post") || !hasNext(diag(t, res.result, "decision.blocking"), "wa-cli decisions") {
		t.Fatalf("hard rules: code = %d, %+v", code, res.Diagnostics)
	}
	if _, s := runReview(t, dir, "", "review", "show", "my-post"); s.Lint.Errors != 1 || !slices.Equal(gateCodes(s), []string{"accept.lint-errors", "decision.blocking"}) {
		t.Fatalf("show hard rules: %+v", s.Gate)
	}
	if code, _ := runWSIn(t, dir, reviewDraft, "draft", "write", "my-post"); code != 0 {
		t.Fatal("rewrite draft")
	}
	mustRunWS(t, dir, "decide", "dec-1", "--option", "a", "--confirmation", "作者：发")
	if code, res := runReview(t, dir, "", "accept", "my-post"); code != 0 || res.Acceptance.GateLevel != "fast" {
		t.Fatalf("fast accept without review: code = %d, %+v", code, res.Diagnostics)
	}
	// 验收记录不通过校验、或只有手写的 acceptance.md 时，advance 报告 acceptance.invalid。
	arel := ".writing-anything/works/my-post/acceptance.yaml"
	writeFile(t, wsPath(dir, arel), "schema_version: 1\n")
	if code, res := runWS(t, dir, "advance", "my-post"); code != 1 || !hasNext(diag(t, res, "acceptance.invalid"), "wa-cli review show my-post") {
		t.Fatalf("invalid acceptance: %+v", res.Diagnostics)
	}
	if code, res := runWS(t, dir, "validate", arel); code != 1 || diag(t, res, "artifact.schema-violation").Path != arel {
		t.Errorf("validate invalid acceptance: %+v", res.Diagnostics)
	}
	if _, s := runReview(t, dir, "", "review", "show", "my-post"); s.Acceptance != nil || diag(t, s.result, "artifact.schema-violation").Path != arel {
		t.Errorf("show invalid acceptance: %+v", s.Diagnostics)
	}
	os.Remove(wsPath(dir, arel))
	writeFile(t, wsPath(dir, ".writing-anything/works/my-post/acceptance.md"), "作者说可以\n")
	if code, res := runWS(t, dir, "advance", "my-post"); code != 1 || diag(t, res, "acceptance.invalid").Path != ".writing-anything/works/my-post/acceptance.md" {
		t.Fatalf("acceptance.md: %+v", res.Diagnostics)
	}
}

// review show 与 accept 的人读输出。
func TestReviewText(t *testing.T) {
	dir := reviewWork(t)
	for _, tc := range []struct {
		input string
		args  []string
		want  string
	}{
		{"", []string{"review", "show", "my-post"}, "还没有可用的审稿"},
		{goodReview, []string{"review", "record", "my-post"}, "2 个发现项"},
		{"", []string{"review", "show", "my-post"}, "有 2 个阻塞项"},
		{"", []string{"review", "resolve", "my-post", "rf-1"}, "标为 resolved"},
		{"", []string{"review", "resolve", "my-post", "rf-1"}, "取值未变"},
		{"", []string{"review", "resolve", "my-post", "rf-2"}, "标为 resolved"},
		{"", []string{"review", "show", "my-post"}, "可以验收"},
		{"", []string{"accept", "my-post", "--confirmed-by-author", "--confirmation", "好"}, "经作者确认：好"},
	} {
		code, stdout, stderr := runIn(t, tc.input, append(tc.args, "--dir", dir)...)
		if code != 0 || !strings.Contains(stdout, tc.want) || stderr != "" {
			t.Errorf("%v: code = %d, stdout = %q, stderr = %q", tc.args, code, stdout, stderr)
		}
	}
	for _, args := range [][]string{{"review", "show", "nope"}, {"review", "record", "nope"}, {"review", "resolve", "nope", "x"}, {"accept", "nope"}} {
		if code, stdout, _ := runIn(t, "", append(args, "--dir", dir)...); code != 1 || stdout != "" {
			t.Errorf("%v: code = %d, stdout = %q", args, code, stdout)
		}
	}
}

// strict 下规范包声明 blocking 的评分项结论为 fail 时阻塞验收；standard 下不阻塞。
func TestAcceptBlockingRubric(t *testing.T) {
	dir := reviewWork(t)
	writePack(t, dir, "facts", "schema_version: 1\nid: facts\ntitle: 事实规范\nrubric:\n  - {id: facts, title: 事实, blocking: true}\n")
	mustRunWS(t, dir, "config", "set", "packs", "facts")
	mustRunWS(t, dir, "config", "set", "gate_level", "strict")
	in := "rubric:\n  - {item: clarity, verdict: pass, evidence: 清楚, paragraphs: [1]}\n  - {item: facts, verdict: fail, evidence: 第二段的数据没有出处, paragraphs: [2]}\n"
	if code, rr := runReview(t, dir, in, "review", "record", "my-post"); code != 0 || rr.Review.Rubric[1]["blocking"] != true {
		t.Fatalf("record: code = %d, %+v", code, rr)
	}
	code, res := runReview(t, dir, "", "accept", "my-post", "--confirmed-by-author", "--confirmation", "作者：好")
	if d := diag(t, res.result, "accept.blocking-rubric"); code != 1 || !hasNext(d, "wa-cli review show my-post") {
		t.Fatalf("blocking rubric: code = %d, %+v", code, res.Diagnostics)
	}
	mustRunWS(t, dir, "config", "set", "gate_level", "standard")
	if code, res := runReview(t, dir, "", "accept", "my-post", "--confirmed-by-author", "--confirmation", "作者：好"); code != 0 {
		t.Fatalf("standard ignores blocking rubric: %+v", res.Diagnostics)
	}
}

// 换成符号链接的 review.yaml 与 acceptance.yaml：advance、next、整体 validate、validate <path> 与 review show 都报告同一问题，
// 不当作不存在（workspace-artifacts/validated-path-segments）。
func TestReviewArtifactSymlinks(t *testing.T) {
	dir := reviewWork(t)
	outside := filepath.Join(t.TempDir(), "outside.yaml")
	writeFile(t, outside, "garbage: [\n")
	base := ".writing-anything/works/my-post/"
	for _, name := range []string{"review.yaml", "acceptance.yaml"} {
		if err := os.Symlink(outside, wsPath(dir, base+name)); err != nil {
			t.Skip("symlink not supported:", err)
		}
	}
	if code, res := runWS(t, dir, "advance", "my-post"); code != 1 || diag(t, res, "acceptance.invalid").Path != base+"acceptance.yaml" {
		t.Fatalf("advance: code = %d, %+v", code, res.Diagnostics)
	}
	if _, res := runWS(t, dir, "next", "my-post"); !strings.Contains(res.stdout, "acceptance.invalid") || strings.Contains(res.stdout, "acceptance.md") {
		t.Errorf("next: %s", res.stdout)
	}
	code, res := runWS(t, dir, "validate")
	if code != 1 || !slices.Contains(res.Checked, base+"review.yaml") || !slices.Contains(res.Checked, base+"acceptance.yaml") {
		t.Errorf("validate: code = %d, checked = %v, %+v", code, res.Checked, res.Diagnostics)
	}
	for _, name := range []string{"review.yaml", "acceptance.yaml"} {
		found := false
		for _, d := range res.Diagnostics {
			found = found || d.Code == "artifact.not-found" && d.Path == base+name
		}
		if !found {
			t.Errorf("validate %s: %+v", name, res.Diagnostics)
		}
		if code, res := runWS(t, dir, "validate", base+name); code != 1 || diag(t, res, "artifact.not-found").Path != base+name ||
			!hasNext(diag(t, res, "artifact.not-found"), "wa-cli validate --dir") || hasNext(diag(t, res, "artifact.not-found"), "wa-cli init") {
			t.Errorf("validate %s: code = %d, %+v", name, code, res.Diagnostics)
		}
	}
	code, s := runReview(t, dir, "", "review", "show", "my-post")
	if code != 1 || s.Review != nil || diag(t, s.result, "artifact.not-found").Path == "" || slices.Contains(gateCodes(s), "accept.review-missing") {
		t.Errorf("review show: code = %d, %+v %v", code, s.Diagnostics, gateCodes(s))
	}
	if code, res := runReview(t, dir, "", "review", "resolve", "my-post", "rf-1"); code != 1 || diag(t, res.result, "artifact.not-found").Path != base+"review.yaml" {
		t.Errorf("review resolve: code = %d, %+v", code, res.Diagnostics)
	}
}

// 验收后提高关卡强度：原来较宽松的验收不再有效（acceptance.weaker-gate）；accept 的每条阻塞项诊断都能用 review show 复查；
// 决策队列无法加载时阻塞验收，消息说明是决策队列。
func TestAcceptGateRechecks(t *testing.T) {
	dir := reviewWork(t)
	mustRunWS(t, dir, "config", "set", "gate_level", "fast")
	if code, res := runReview(t, dir, "", "accept", "my-post"); code != 0 || res.Acceptance == nil {
		t.Fatalf("fast accept: %+v", res.Diagnostics)
	}
	mustRunWS(t, dir, "config", "set", "gate_level", "strict")
	code, res := runWS(t, dir, "advance", "my-post")
	if d := diag(t, res, "acceptance.weaker-gate"); code != 1 || !hasNext(d, "wa-cli review show my-post") || !strings.Contains(d.Message, "fast") {
		t.Fatalf("weaker gate: code = %d, %+v", code, res.Diagnostics)
	}
	if _, nr := runWS(t, dir, "next", "my-post"); !strings.Contains(nr.stdout, "wa-cli accept 验收") || strings.Contains(nr.stdout, "acceptance.md") {
		t.Errorf("next action: %s", nr.stdout)
	}
	// strict 下的全部阻塞项（含决策与不通过校验的审稿）都给出 review show。
	mustRunWS(t, dir, "decision", "add", "--question", "要不要发", "--option", "发", "--unit", "my-post", "--blocking")
	writeFile(t, wsPath(dir, ".writing-anything/works/my-post/review.yaml"), "schema_version: 1\n")
	code, ar := runReview(t, dir, "", "accept", "my-post")
	if code != 1 || len(ar.Diagnostics) < 3 {
		t.Fatalf("strict blockers: code = %d, %+v", code, ar.Diagnostics)
	}
	for _, d := range ar.Diagnostics {
		if d.Severity == "error" && !hasNext(d, "wa-cli review show my-post") {
			t.Errorf("%s next = %v, want review show", d.Code, d.Next)
		}
	}
	writeFile(t, wsPath(dir, ".writing-anything/decisions.yaml"), "garbage: [\n")
	_, ar = runReview(t, dir, "", "accept", "my-post", "--confirmed-by-author", "--confirmation", "好")
	// 队列损坏时除 error 级阻塞诊断外，冲突（Conflict）检查未运行的 warning（同码 decision.invalid-queue）也会出现。
	var queueDiag *Diagnostic
	for i, d := range ar.Diagnostics {
		if d.Code == "decision.invalid-queue" && d.Severity == "error" {
			queueDiag = &ar.Diagnostics[i]
			break
		}
	}
	if queueDiag == nil || strings.Contains(queueDiag.Message, "文本检查") || !strings.Contains(queueDiag.Message, "决策队列") || !hasNext(*queueDiag, "wa-cli review show my-post") {
		t.Errorf("invalid queue: %+v", queueDiag)
	}
	warned := false
	for _, d := range ar.Diagnostics {
		if d.Code == "decision.invalid-queue" && d.Severity == "warning" && strings.Contains(d.Message, "冲突") {
			warned = true
		}
	}
	if !warned {
		t.Errorf("conflict-check warning missing: %+v", ar.Diagnostics)
	}
}

// review record --reaffirm：小改动后沿用既有结论的一键重申（Reaffirm）——更新 draft_sha256 解除 stale、
// 标记指定 open 发现项、记录变化摘要；不重校验引文（措辞改后引文移位是重申的常态）。
func TestReviewReaffirm(t *testing.T) {
	dir := reviewWork(t)
	rel := ".writing-anything/works/my-post/review.yaml"
	// 还没有审稿时不能重申。
	code, res := runReview(t, dir, "", "review", "record", "my-post", "--reaffirm", "--summary", "首次")
	if d := diag(t, res.result, "review.reaffirm-no-review"); code != 1 || d.Path != rel ||
		!hasNext(d, "wa-cli help review record --json") || !hasNext(d, shellCommand("wa-cli", "review", "show", "my-post", "--dir", dir, "--json")) {
		t.Fatalf("reaffirm without review: code = %d, diag = %+v", code, d)
	}
	runReview(t, dir, goodReview, "review", "record", "my-post")
	// 正文小改（作者直接编辑），引文「可能」被改掉：整份重录会因 quote-not-found 失败，重申不受影响。
	draftPath := wsPath(dir, ".writing-anything/works/my-post/draft.md")
	edited := strings.Replace(readFile(t, draftPath), "可能", "大概", 1)
	writeFile(t, draftPath, edited)
	if code, rr := runReview(t, dir, "", "review", "show", "my-post"); code != 0 || !rr.Review.Stale {
		t.Fatalf("show stale: code = %d, stale = %v", code, rr.Review.Stale)
	}
	// 变化摘要必填非空白。
	if code, res = runReview(t, dir, "", "review", "record", "my-post", "--reaffirm", "--summary", " "); code != 1 ||
		diag(t, res.result, "artifact.schema-violation").Field != "summary" || !hasNext(diag(t, res.result, "artifact.schema-violation"), "wa-cli help review record --json") {
		t.Fatalf("blank summary: code = %d, %+v", code, res.Diagnostics)
	}
	// 发现项必须存在且为 open。
	if code, res = runReview(t, dir, "", "review", "record", "my-post", "--reaffirm", "--summary", "s", "--resolve", "nope"); code != 1 ||
		!strings.Contains(diag(t, res.result, "review.finding-not-found").Message, "审稿中没有发现项 nope") ||
		!hasNext(diag(t, res.result, "review.finding-not-found"), shellCommand("wa-cli", "review", "show", "my-post", "--dir", dir, "--json")) {
		t.Fatalf("unknown finding: code = %d, %+v", code, res.Diagnostics)
	}
	// 重申成功：结论沿用、rf-1 变 resolved、draft_sha256 更新、reaffirms 追加、stale 解除。
	code, rr := runReview(t, dir, "", "review", "record", "my-post", "--reaffirm", "--summary", "措辞小改：可能改大概，结论不受影响", "--resolve", "rf-1")
	if code != 0 || rr.Review == nil || !slices.Equal(rr.ChangedPaths, []string{rel}) {
		t.Fatalf("reaffirm: code = %d, %+v", code, rr)
	}
	if rr.Review.Stale || rr.Review.DraftSHA != review.SHA256([]byte(edited)) {
		t.Fatalf("reaffirm stale/sha: %+v", rr.Review)
	}
	if len(rr.Review.Rubric) != 1 || rr.Review.Rubric[0]["verdict"] != "fail" || len(rr.Review.Findings) != 2 {
		t.Fatalf("reaffirm carried: %+v", rr.Review)
	}
	if rr.Review.Findings[0]["status"] != "resolved" || rr.Review.Findings[1]["status"] != "open" {
		t.Fatalf("reaffirm findings: %+v", rr.Review.Findings)
	}
	if len(rr.Review.Reaffirms) != 1 || rr.Review.Reaffirms[0]["summary"] != "措辞小改：可能改大概，结论不受影响" ||
		!slices.Equal(rr.Review.Reaffirms[0]["resolved"].([]any), []any{"rf-1"}) {
		t.Fatalf("reaffirm record: %+v", rr.Review.Reaffirms)
	}
	// review show 对重申后的审稿不再报 stale（accept 的过期阻塞随之解除）。
	if code, show := runReview(t, dir, "", "review", "show", "my-post"); code != 0 || show.Review.Stale || strings.Contains(show.stdout, "accept.review-stale") {
		t.Fatalf("show after reaffirm: code = %d, stale = %v, stdout = %s", code, show.Review.Stale, show.stdout)
	}
	saved := readFile(t, wsPath(dir, rel))
	if !strings.Contains(saved, "summary: 措辞小改：可能改大概，结论不受影响") || !strings.Contains(saved, "draft_sha256: "+review.SHA256([]byte(edited))) {
		t.Fatalf("review.yaml:\n%s", saved)
	}
	if code, res := runWS(t, dir, "validate", rel); code != 0 {
		t.Fatalf("validate reaffirmed: code = %d, %+v", code, res.Diagnostics)
	}
	// 再次重申标记已 resolved 的发现项被拒；再次小改后可再重申（记录追加）。
	if code, res = runReview(t, dir, "", "review", "record", "my-post", "--reaffirm", "--summary", "s", "--resolve", "rf-1"); code != 1 ||
		!strings.Contains(diag(t, res.result, "review.finding-not-found").Message, "不是 open") {
		t.Fatalf("re-resolve: code = %d, %+v", code, res.Diagnostics)
	}
	writeFile(t, draftPath, strings.Replace(readFile(t, draftPath), "例子", "示例", 1))
	if code, rr = runReview(t, dir, "", "review", "record", "my-post", "--reaffirm", "--summary", "又改一词", "--waive", "rf-2"); code != 0 ||
		len(rr.Review.Reaffirms) != 2 || rr.Review.Findings[1]["status"] != "waived" {
		t.Fatalf("second reaffirm: code = %d, reaffirms = %+v", code, rr.Review.Reaffirms)
	}
	// 不带 --reaffirm 的整份录入不受影响：引文已移位时按 quote-not-found 拒绝（重申避开的正是这一点），
	// 换成新引文的整份录入成功且清空重申记录（整体替换）。
	if code, res = runReview(t, dir, goodReview, "review", "record", "my-post"); code != 1 ||
		diag(t, res.result, "review.quote-not-found").Field != "findings[1].quote" {
		t.Fatalf("re-record with moved quote: code = %d, %+v", code, res.Diagnostics)
	}
	fixedReview := strings.Replace(goodReview, "quote: 可能", "quote: 大概", 1)
	if code, rr = runReview(t, dir, fixedReview, "review", "record", "my-post"); code != 0 || len(rr.Review.Reaffirms) != 0 {
		t.Fatalf("re-record: code = %d, reaffirms = %+v", code, rr.Review.Reaffirms)
	}
}

// review-record-skeleton：--init 输出与 review show 一致的评分项骨架（verdict 与 evidence 留空、注释含 title 与
// blocking、末尾段落表），空骨架录入逐项报 review.missing-verdict 与 review.missing-evidence，填写后录入成功。
func TestReviewRecordSkeleton(t *testing.T) {
	dir := newWork(t)
	// --init 需要正文。
	writeFile(t, wsPath(dir, draftRel), "连接池很重要。连接池要复用。\n\n复用能降低延迟。\n")
	code, res := runWS(t, dir, "review", "record", "my-post", "--init")
	if code != 0 || res.ChangedPaths != nil && len(res.ChangedPaths) != 0 {
		t.Fatalf("init: exit code = %d, result = %+v", code, res)
	}
	var out struct {
		Skeleton string `json:"skeleton"`
	}
	if err := json.Unmarshal([]byte(res.stdout), &out); err != nil || out.Skeleton == "" {
		t.Fatalf("skeleton field missing: %s", res.stdout)
	}
	skel := out.Skeleton
	// 评分项与 review show 的 rubric 一致（item、title、blocking 注释），verdict 与 evidence 留空。
	_, show := runWS(t, dir, "review", "show", "my-post")
	var showOut struct {
		Rubric []struct {
			ID       string `json:"id"`
			Title    string `json:"title"`
			Blocking bool   `json:"blocking"`
		} `json:"rubric"`
	}
	if err := json.Unmarshal([]byte(show.stdout), &showOut); err != nil || len(showOut.Rubric) == 0 {
		t.Fatalf("review show rubric: %s", show.stdout)
	}
	for _, it := range showOut.Rubric {
		line := "  - item: " + it.ID + " # " + it.Title
		if it.Blocking {
			line += "（blocking：fail 时阻塞验收）"
		}
		if !strings.Contains(skel, line+"\n") {
			t.Errorf("skeleton lacks rubric item %s: %s", it.ID, skel)
		}
	}
	if !strings.Contains(skel, "    verdict: \"\"\n    evidence: \"\"\n    paragraphs: []\n") || !strings.Contains(skel, "findings: []") {
		t.Errorf("skeleton must leave verdict/evidence empty and findings empty: %s", skel)
	}
	// 段落表：两段、编号与首句一致。
	if !strings.Contains(skel, "# 1: 连接池很重要。") || !strings.Contains(skel, "# 2: 复用能降低延迟。") {
		t.Errorf("skeleton paragraph table: %s", skel)
	}
	// 文本模式输出与 skeleton 字段等价；不写文件。
	if code, out2, _ := run(t, "review", "record", "my-post", "--init", "--dir", dir); code != 0 || !strings.HasPrefix(strings.TrimSpace(out2), "# 审稿（Review）骨架") {
		t.Fatalf("init text mode: exit code = %d, stdout = %s", code, out2)
	}
	if _, err := os.Stat(wsPath(dir, ".writing-anything/works/my-post/review.yaml")); err == nil {
		t.Fatal("--init must not write review.yaml")
	}
	// --init 与 --file / --reaffirm 互斥。
	for _, args := range [][]string{{"review", "record", "my-post", "--init", "--file", "r.yaml"}, {"review", "record", "my-post", "--init", "--reaffirm", "--summary", "x"}} {
		if code, res := runWS(t, dir, args...); code != 1 || !strings.Contains(res.stdout, `"code":"input.invalid-value"`) || !strings.Contains(res.stdout, `"field":"init"`) {
			t.Fatalf("%v: exit code = %d, stdout = %s", args, code, res.stdout)
		}
	}
	// 空骨架整份录入：逐项报错、不写文件。
	writeFile(t, filepath.Join(dir, "empty.yaml"), skel)
	if code, res = runWS(t, dir, "review", "record", "my-post", "--file", "empty.yaml"); code != 1 {
		t.Fatalf("empty skeleton: exit code = %d", code)
	}
	missVerdict, missEvidence := 0, 0
	for _, d := range res.Diagnostics {
		switch d.Code {
		case "review.missing-verdict":
			missVerdict++
			if !strings.HasPrefix(d.Field, "rubric[") || !strings.HasSuffix(d.Field, ".verdict") {
				t.Errorf("missing-verdict field = %q", d.Field)
			}
		case "review.missing-evidence":
			missEvidence++
			if !strings.HasPrefix(d.Field, "rubric[") || !strings.HasSuffix(d.Field, ".evidence") {
				t.Errorf("missing-evidence field = %q", d.Field)
			}
		}
	}
	if missVerdict != len(showOut.Rubric) || missEvidence != len(showOut.Rubric) {
		t.Fatalf("per-item diagnostics: verdict %d, evidence %d, rubric %d", missVerdict, missEvidence, len(showOut.Rubric))
	}
	if _, err := os.Stat(wsPath(dir, ".writing-anything/works/my-post/review.yaml")); err == nil {
		t.Fatal("empty skeleton must not write review.yaml")
	}
	// 填写后录入成功。
	filled := skel
	for _, it := range showOut.Rubric {
		filled = strings.Replace(filled, "  - item: "+it.ID+" # ", "  - item: "+it.ID+" # FILLED ", 1)
	}
	// 逐项填值：verdict 与 evidence 都在固定行上。
	lines := strings.Split(filled, "\n")
	inRubric := false
	item := ""
	var bld []string
	for _, ln := range lines {
		if strings.HasPrefix(ln, "rubric:") {
			inRubric = true
			bld = append(bld, ln)
			continue
		}
		if strings.HasPrefix(ln, "findings:") {
			inRubric = false
		}
		if inRubric {
			if strings.HasPrefix(ln, "  - item: ") {
				item = strings.TrimSpace(strings.TrimPrefix(ln, "  - item: "))
				item = strings.TrimSpace(strings.SplitN(item, " ", 2)[0])
			}
			if ln == "    verdict: \"\"" {
				ln = "    verdict: pass"
			}
			if ln == "    evidence: \"\"" {
				ln = "    evidence: 依据充分（" + item + "）"
			}
		}
		bld = append(bld, ln)
	}
	writeFile(t, filepath.Join(dir, "filled.yaml"), strings.Join(bld, "\n"))
	if code, res = runWS(t, dir, "review", "record", "my-post", "--file", "filled.yaml"); code != 0 {
		t.Fatalf("filled skeleton: exit code = %d, diagnostics = %+v", code, res.Diagnostics)
	}
	if _, err := os.Stat(wsPath(dir, ".writing-anything/works/my-post/review.yaml")); err != nil {
		t.Fatal("filled skeleton should write review.yaml")
	}
}
