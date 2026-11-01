package cli

import (
	"encoding/json"
	"os"
	"slices"
	"strings"
	"testing"
)

// briefOut 是 brief 命令 --json 输出中测试关心的部分。
type briefOut struct {
	result
	Brief     map[string]any   `json:"brief"`
	Questions []map[string]any `json:"questions"`
}

func runBrief(t *testing.T, dir string, args ...string) (int, briefOut) {
	t.Helper()
	code, res := runUnits(t, dir, args...)
	var out briefOut
	if err := json.Unmarshal([]byte(res.stdout), &out); err != nil {
		t.Fatal(err)
	}
	out.result = res.result
	return code, out
}

const briefRel = ".writing-anything/works/my-post/brief.yaml"

// briefWork 返回含作品 my-post、已登记素材 src-a 的工作区。
func briefWork(t *testing.T) string {
	t.Helper()
	dir := newWork(t)
	if code, stdout, _ := runIn(t, "作者的原话", "source", "add", "--type", "author-note", "--id", "src-a", "--dir", dir); code != 0 {
		t.Fatalf("source add: %s", stdout)
	}
	return dir
}

func questionSummary(qs []map[string]any) []string {
	var out []string
	for _, q := range qs {
		out = append(out, q["code"].(string)+" "+q["field"].(string))
	}
	return out
}

func TestBriefSet(t *testing.T) {
	dir := briefWork(t)
	// set-creates
	code, res := runBrief(t, dir, "brief", "set", "my-post", "--audience", "后端工程师", "--audience-sources", "src-a",
		"--purpose", "讲清楚为什么要做连接池", "--purpose-sources", "src-a",
		"--message", "km-1", "--message-text", "连接池能显著降低延迟", "--message-sources", "src-a")
	if code != 0 || !res.Valid || !slices.Equal(res.ChangedPaths, []string{briefRel}) || len(res.Questions) != 0 {
		t.Fatalf("set-creates: exit code = %d, result = %+v", code, res)
	}
	km := res.Brief["keyMessages"].([]any)[0].(map[string]any)
	if km["id"] != "km-1" || km["sources"].([]any)[0] != "src-a" || km["decision"] != nil || res.Brief["unit"] != "my-post" || res.Brief["path"] != briefRel {
		t.Fatalf("set-creates: brief = %v", res.Brief)
	}
	if got := readFile(t, wsPath(dir, briefRel)); !strings.Contains(got, "schema_version: 1") || !strings.Contains(got, "audience: 后端工程师") {
		t.Fatalf("brief.yaml =\n%s", got)
	}

	// set-merges：保留注释与未涉及的字段。
	writeFile(t, wsPath(dir, briefRel), "# 作者的注释\n"+readFile(t, wsPath(dir, briefRel)))
	runWS(t, dir, "decision", "add", "--question", "这是你的观点吗", "--unit", "my-post")
	code, res = runBrief(t, dir, "brief", "set", "my-post", "--message", "km-1", "--message-decision", "dec-1", "--length", "1500-3000")
	km = res.Brief["keyMessages"].([]any)[0].(map[string]any)
	if code != 0 || km["text"] != "连接池能显著降低延迟" || km["decision"] != "dec-1" || len(km["sources"].([]any)) != 1 ||
		get(res.Brief, "length.min") != 1500.0 || get(res.Brief, "length.max") != 3000.0 || res.Brief["audience"] != "后端工程师" {
		t.Fatalf("set-merges: exit code = %d, brief = %v", code, res.Brief)
	}
	if got := readFile(t, wsPath(dir, briefRel)); !strings.Contains(got, "# 作者的注释") {
		t.Fatalf("comment lost:\n%s", got)
	}

	// 取值未变时不写文件。
	if code, res := runBrief(t, dir, "brief", "set", "my-post", "--audience", "后端工程师"); code != 0 || len(res.ChangedPaths) != 0 {
		t.Fatalf("unchanged: exit code = %d, result = %+v", code, res)
	}

	// set-clears
	runBrief(t, dir, "brief", "set", "my-post", "--avoid", "不点名", "--avoid", "不贴代码")
	code, res = runBrief(t, dir, "brief", "set", "my-post", "--purpose=", "--avoid=")
	if got := readFile(t, wsPath(dir, briefRel)); code != 0 || res.Brief["purpose"] != nil || len(res.Brief["avoid"].([]any)) != 0 || strings.Contains(got, "purpose") || strings.Contains(got, "avoid") {
		t.Fatalf("set-clears: exit code = %d, brief = %v\n%s", code, res.Brief, got)
	}
	if !slices.Equal(questionSummary(res.Questions), []string{"brief.missing-field purpose"}) {
		t.Fatalf("set-clears: questions = %v", res.Questions)
	}

	// set-remove-message
	runBrief(t, dir, "brief", "set", "my-post", "--message", "km-2", "--message-text", "连接数不是越多越好")
	code, res = runBrief(t, dir, "brief", "set", "my-post", "--remove-message", "km-1")
	if msgs := res.Brief["keyMessages"].([]any); code != 0 || len(msgs) != 1 || msgs[0].(map[string]any)["id"] != "km-2" {
		t.Fatalf("set-remove-message: exit code = %d, brief = %v", code, res.Brief)
	}
	// 删除后以同一 id 重新追加需要 --message-text。
	if code, res := runBrief(t, dir, "brief", "set", "my-post", "--remove-message", "km-2", "--message", "km-2", "--message-sources", "src-a"); code != 1 || diag(t, res.result, "input.invalid-value").Field != "" {
		t.Fatalf("re-add without text: exit code = %d", code)
	}

	// set-child-unit
	runWS(t, dir, "unit", "add", "my-post", "section", "intro")
	code, res = runBrief(t, dir, "brief", "set", "my-post/intro", "--purpose", "引出问题")
	if want := []string{".writing-anything/works/my-post/units/intro", ".writing-anything/works/my-post/units/intro/brief.yaml"}; code != 0 || !slices.Equal(res.ChangedPaths, want) || len(res.Questions) != 0 {
		t.Fatalf("set-child-unit: exit code = %d, result = %+v", code, res)
	}

	code, stdout, _ := run(t, "brief", "set", "my-post", "--audience", "工程师", "--dir", dir)
	if code != 0 || !strings.Contains(stdout, "写作简报（Brief）已更新") || !strings.Contains(stdout, "受众：工程师") || !strings.Contains(stdout, "待提问：") {
		t.Fatalf("text: exit code = %d, stdout = %q", code, stdout)
	}
	if _, stdout, _ := run(t, "brief", "set", "my-post", "--audience", "工程师", "--dir", dir); !strings.Contains(stdout, "取值未变") {
		t.Fatalf("text unchanged: stdout = %q", stdout)
	}
}

// 前提（Premise）、主题（Theme）与视角（POV）：写入、关联素材、整体删除与参数拒绝（brief-set、brief-artifact）。
func TestBriefSetFictionFields(t *testing.T) {
	dir := briefWork(t)
	code, res := runBrief(t, dir, "brief", "set", "my-post", "--premise", "灯里的精灵每撒一次谎，灯就暗一分",
		"--premise-sources", "src-a", "--theme", "善意的谎言", "--pov-person", "third", "--pov-character", "守灯人", "--length", "2000-8000")
	if code != 0 || !res.Valid {
		t.Fatalf("set premise: exit code = %d, diagnostics = %+v", code, res.Diagnostics)
	}
	p := res.Brief["premise"].(map[string]any)
	if p["text"] != "灯里的精灵每撒一次谎，灯就暗一分" || p["sources"].([]any)[0] != "src-a" || p["decision"] != nil {
		t.Fatalf("premise = %v", p)
	}
	if res.Brief["theme"] != "善意的谎言" || res.Brief["pov"].(map[string]any)["person"] != "third" ||
		res.Brief["pov"].(map[string]any)["character"] != "守灯人" {
		t.Fatalf("brief = %v", res.Brief)
	}
	if code, stdout, _ := run(t, "brief", "show", "my-post", "--dir", dir); code != 0 ||
		!strings.Contains(stdout, "前提：灯里的精灵") || !strings.Contains(stdout, "视角：第三人称（视角人物：守灯人）") {
		t.Fatalf("show text: %q", stdout)
	}
	// 删除视角人物后剩 person；删除人称时整体删除 pov；删除前提整体。
	runBrief(t, dir, "brief", "set", "my-post", "--pov-character=")
	if code, res = runBrief(t, dir, "brief", "set", "my-post", "--pov-person=", "--premise="); code != 0 ||
		res.Brief["pov"] != nil || res.Brief["premise"] != nil {
		t.Fatalf("delete pov/premise: exit code = %d, brief = %v", code, res.Brief)
	}
	// 参数拒绝：人称不在取值范围内；素材未登记；没有前提时只给子字段。
	for _, tt := range []struct {
		name  string
		args  []string
		code  string
		field string
	}{
		{"bad person", []string{"--pov-person", "omniscient"}, "input.invalid-value", ""},
		{"character without person", []string{"--pov-character", "守灯人"}, "input.invalid-value", ""},
		{"unknown premise source", []string{"--premise", "前提", "--premise-sources", "src-nope"}, "brief.unknown-source", "premise-sources[0]"},
		{"subfield without premise", []string{"--premise-sources", "src-a"}, "input.invalid-value", ""},
	} {
		code, res := runBrief(t, dir, append([]string{"brief", "set", "my-post"}, tt.args...)...)
		if d := diag(t, res.result, tt.code); code != 1 || d.Field != tt.field || !hasNext(d, "wa-cli help brief set --json") {
			t.Fatalf("%s: exit code = %d, diag = %+v", tt.name, code, d)
		}
	}
	// 前提没有出处时出现在提问中（brief.unsourced-premise）。
	runBrief(t, dir, "brief", "set", "my-post", "--audience", "读者", "--audience-sources", "src-a", "--purpose", "读一个故事", "--purpose-sources", "src-a")
	runBrief(t, dir, "brief", "set", "my-post", "--premise", "没有出处的前提")
	if code, res = runBrief(t, dir, "brief", "show", "my-post"); code != 0 ||
		!slices.Equal(questionSummary(res.Questions), []string{"brief.unsourced-premise premise"}) {
		t.Fatalf("unsourced premise: exit code = %d, questions = %v", code, res.Questions)
	}
}

func TestBriefSetRejects(t *testing.T) {
	dir := briefWork(t)
	runBrief(t, dir, "brief", "set", "my-post", "--audience", "读者", "--message", "km-1", "--message-text", "观点")
	runWS(t, dir, "decision", "add", "--question", "与本单元无关的决策")
	before := readFile(t, wsPath(dir, briefRel))
	tests := []struct {
		name  string
		args  []string
		code  string
		field string
		next  string
	}{
		{"set-no-flags", nil, "input.invalid-value", "", "wa-cli help brief set --json"},
		{"set-new-message-without-text", []string{"--message", "km-2", "--message-sources", "src-a"}, "input.invalid-value", "", "wa-cli help brief set --json"},
		{"message flag without message", []string{"--message-text", "x"}, "input.invalid-value", "", "wa-cli help brief set --json"},
		{"invalid message id", []string{"--message", "KM", "--message-text", "x"}, "artifact.schema-violation", "message", "wa-cli help brief set --json"},
		{"set-unknown-source", []string{"--sources", "src-nope"}, "brief.unknown-source", "sources[0]", "wa-cli source list"},
		{"unknown decision", []string{"--message", "km-1", "--message-decision", "dec-9"}, "brief.unknown-decision", "message-decision", "wa-cli decisions --all"},
		{"set-bad-length", []string{"--length", "3000-1500"}, "input.invalid-value", "", "wa-cli help brief set --json"},
		{"set-remove-missing", []string{"--remove-message", "km-9"}, "brief.message-not-found", "remove-message[0]", "wa-cli brief show my-post"},
		// 参数问题在修改前报告，诊断不指向 brief.yaml（否则按 next 复查会得到「通过」）。
		{"blank message text", []string{"--message", "km-1", "--message-text", " "}, "input.invalid-value", "", "wa-cli help brief set --json"},
		{"blank audience", []string{"--audience", "  "}, "input.invalid-value", "", "wa-cli help brief set --json"},
		{"blank avoid", []string{"--avoid", "a", "--avoid", ""}, "input.invalid-value", "", "wa-cli help brief set --json"},
		{"duplicate message sources", []string{"--message", "km-1", "--message-sources", "src-a,src-a"}, "input.invalid-value", "", "wa-cli help brief set --json"},
		{"message without changes", []string{"--message", "km-1"}, "input.invalid-value", "", "wa-cli help brief set --json"},
		{"unrelated decision", []string{"--message", "km-1", "--message-decision", "dec-1"}, "brief.unrelated-decision", "message-decision", "wa-cli decisions --all"},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			code, res := runBrief(t, dir, append([]string{"brief", "set", "my-post"}, tt.args...)...)
			d := diag(t, res.result, tt.code)
			if code != 1 || res.Valid || d.Field != tt.field || d.Path != "" || !hasNext(d, tt.next) ||
				d.Next[0] != shellCommand("wa-cli", "brief", "show", "my-post", "--dir", dir, "--json") || res.Brief != nil || len(res.ChangedPaths) != 0 {
				t.Fatalf("exit code = %d, diagnostic = %+v", code, d)
			}
			if readFile(t, wsPath(dir, briefRel)) != before {
				t.Fatal("brief.yaml changed")
			}
		})
	}
	if _, res := runBrief(t, dir, "brief", "set", "my-post", "--message", "km-2", "--message-sources", "src-a"); !strings.Contains(diag(t, res.result, "input.invalid-value").Message, "--message-text") {
		t.Fatal("set-new-message-without-text: message does not mention --message-text")
	}
	// 决策队列无法加载时不能关联决策。
	queue := wsPath(dir, ".writing-anything/decisions.yaml")
	good := readFile(t, queue)
	writeFile(t, queue, "decisions: [\n")
	if code, res := runBrief(t, dir, "brief", "set", "my-post", "--message", "km-1", "--message-decision", "dec-9"); code != 1 || !hasNext(diag(t, res.result, "decision.invalid-queue"), "wa-cli decisions") || readFile(t, wsPath(dir, briefRel)) != before {
		t.Fatalf("invalid queue: exit code = %d", code)
	}
	writeFile(t, queue, good)
	// 已有 brief.yaml 不通过校验时报告其诊断，下一步是复查该文件。
	writeFile(t, wsPath(dir, briefRel), "schema_version: 1\ntone: x\n")
	code, res := runBrief(t, dir, "brief", "set", "my-post", "--audience", "a")
	if d := diag(t, res.result, "artifact.schema-violation"); code != 1 || d.Path != briefRel || !hasNext(d, "wa-cli validate "+briefRel) {
		t.Fatalf("invalid existing: exit code = %d, diagnostic = %+v", code, d)
	}
	writeFile(t, wsPath(dir, briefRel), "schema_version: 1\naudience: &a 读者\npurpose: *a\n")
	code, res = runBrief(t, dir, "brief", "set", "my-post", "--purpose", "p")
	if d := diag(t, res.result, "brief.anchored-value"); code != 1 || d.Field != "purpose" ||
		!concreteFirst(d, shellCommand("wa-cli", "brief", "show", "my-post", "--dir", dir, "--json"), "wa-cli help brief set --json") {
		t.Fatalf("anchored: exit code = %d, diagnostic = %+v", code, d)
	}
	if code, res := runBrief(t, dir, "brief", "set", "nope", "--audience", "a"); code != 1 || diag(t, res.result, "work.not-found").Code == "" {
		t.Fatalf("unknown work: exit code = %d", code)
	}
}

// 同一命令重复给出同一非列表参数是 usage 错误（usage.duplicate-flag，退出码 2），产物逐字节不变；
// 下游复盘实测两组 --message 静默丢失过一组核心观点。
func TestBriefSetDuplicateFlagRejected(t *testing.T) {
	dir := briefWork(t)
	runBrief(t, dir, "brief", "set", "my-post", "--audience", "读者", "--message", "km-1", "--message-text", "观点")
	before := readFile(t, wsPath(dir, briefRel))
	code, stdout, stderr := run(t, "brief", "set", "my-post",
		"--message", "km-1", "--message-text", "甲", "--message", "km-2", "--message-text", "乙",
		"--dir", dir, "--json")
	if code != 2 {
		t.Fatalf("exit code = %d, stdout = %s, stderr = %s", code, stdout, stderr)
	}
	if !strings.Contains(stdout, "usage.duplicate-flag") || !strings.Contains(stdout, "--message") || !strings.Contains(stdout, "km-1 与 km-2") {
		t.Fatalf("diagnostic missing duplicate-flag detail: %s", stdout)
	}
	if readFile(t, wsPath(dir, briefRel)) != before {
		t.Fatal("brief.yaml changed on duplicate flag")
	}
}

func TestBriefShow(t *testing.T) {
	dir := briefWork(t)
	// show-empty
	code, res := runBrief(t, dir, "brief", "show", "my-post")
	if code != 0 || res.Brief != nil || len(res.ChangedPaths) != 0 || !slices.Equal(questionSummary(res.Questions), []string{"brief.missing-field audience", "brief.missing-field purpose"}) {
		t.Fatalf("show-empty: exit code = %d, result = %+v", code, res)
	}
	for _, q := range res.Questions {
		if q["question"] == "" || !strings.Contains(q["message"].(string), q["question"].(string)) {
			t.Errorf("question = %v", q)
		}
	}
	// show-unsourced
	runBrief(t, dir, "brief", "set", "my-post", "--audience", "读者", "--audience-sources", "src-a", "--purpose", "目的", "--purpose-sources", "src-a")
	runBrief(t, dir, "brief", "set", "my-post", "--message", "km-1", "--message-text", "观点")
	_, res = runBrief(t, dir, "brief", "show", "my-post")
	if !slices.Equal(questionSummary(res.Questions), []string{"brief.unsourced-message key_messages[0]"}) || !strings.Contains(res.Questions[0]["message"].(string), "km-1") {
		t.Fatalf("show-unsourced: questions = %v", res.Questions)
	}
	// show-unconfirmed：决定前后各一次。
	runWS(t, dir, "decision", "add", "--question", "这是你的观点吗", "--unit", "my-post")
	runBrief(t, dir, "brief", "set", "my-post", "--message", "km-1", "--message-decision", "dec-1")
	if _, res = runBrief(t, dir, "brief", "show", "my-post"); !slices.Equal(questionSummary(res.Questions), []string{"brief.unconfirmed-message key_messages[0]"}) {
		t.Fatalf("show-unconfirmed before: questions = %v", res.Questions)
	}
	mustRunWS(t, dir, "decide", "dec-1", "--answer", "是", "--confirmation", "我确认这个观点")
	if _, res = runBrief(t, dir, "brief", "show", "my-post"); len(res.Questions) != 0 {
		t.Fatalf("show-unconfirmed after: questions = %v", res.Questions)
	}
	code, stdout, _ := run(t, "brief", "show", "my-post", "--dir", dir)
	if code != 0 || !strings.Contains(stdout, "核心观点 km-1：观点（决策 dec-1）") {
		t.Fatalf("text: stdout = %q", stdout)
	}
	runBrief(t, dir, "brief", "set", "my-post", "--message", "km-1", "--message-sources", "src-a", "--length", "10-20", "--avoid", "x", "--sources", "src-a")
	if _, stdout, _ := run(t, "brief", "show", "my-post", "--dir", dir); !strings.Contains(stdout, "（素材 src-a）") || !strings.Contains(stdout, "字数范围：10-20") || !strings.Contains(stdout, "禁区：x") || !strings.Contains(stdout, "关联素材：src-a") {
		t.Fatalf("text full: stdout = %q", stdout)
	}
	writeFile(t, wsPath(dir, briefRel), "k: v\n")
	if code, res := runBrief(t, dir, "brief", "show", "my-post"); code != 1 || res.Valid || diag(t, res.result, "artifact.schema-violation").Path != briefRel {
		t.Fatalf("invalid: exit code = %d", code)
	}
	if code, _, _ := run(t, "brief", "show", "my-post", "--dir", dir); code != 1 {
		t.Fatalf("invalid text: exit code = %d", code)
	}
	if _, stdout, _ := run(t, "brief", "show", "my-post/nope", "--dir", dir); stdout != "" {
		t.Fatalf("unknown unit text: stdout = %q", stdout)
	}
	empty := newWork(t)
	if _, stdout, _ := run(t, "brief", "show", "my-post", "--dir", empty); !strings.Contains(stdout, "还没有写作简报") {
		t.Fatalf("text empty: stdout = %q", stdout)
	}
}

func TestBriefGate(t *testing.T) {
	dir := briefWork(t)
	before := readFile(t, workFile(dir, "my-post"))
	// gate-missing-field
	writeFile(t, wsPath(dir, briefRel), "schema_version: 1\naudience: 读者\nprovenance:\n  audience: {sources: [src-a]}\n")
	code, res := runUnits(t, dir, "advance", "my-post")
	d := diag(t, res.result, "brief.missing-field")
	if code != 1 || res.Valid || d.Path != briefRel || !strings.Contains(d.Message, "purpose") || !strings.Contains(d.Message, "请向作者提问") ||
		!hasNext(d, "wa-cli brief show my-post") || !hasNext(d, "wa-cli help brief set --json") || readFile(t, workFile(dir, "my-post")) != before {
		t.Fatalf("gate-missing-field: exit code = %d, diagnostic = %+v", code, d)
	}
	// next-blocked-by-brief
	_, res = runUnits(t, dir, "next", "my-post")
	s := res.Steps[0]
	b := s["blockers"].([]any)[0].(map[string]any)
	if len(s["blockers"].([]any)) != 1 || b["code"] != "brief.missing-field" || b["artifact"] != "brief" || b["path"] != briefRel || b["decision"] != nil ||
		!slices.Equal(commands(s), []string{"wa-cli brief show my-post --dir " + dir + " --json", "wa-cli help brief set --json"}) || !strings.Contains(s["action"].(string), "请向作者提问") {
		t.Fatalf("next-blocked-by-brief: step = %v", s)
	}
	// gate-unsourced
	writeFile(t, wsPath(dir, briefRel), completeBrief+"key_messages:\n  - {id: km-1, text: 观点}\n")
	code, res = runUnits(t, dir, "advance", "my-post")
	if d := diag(t, res.result, "brief.unsourced-message"); code != 1 || !strings.Contains(d.Message, "km-1") {
		t.Fatalf("gate-unsourced: exit code = %d, diagnostic = %+v", code, d)
	}
	// 只关联 open 决策：下一步是决策队列；与阻塞决策的命令去重。
	runWS(t, dir, "decision", "add", "--question", "是你的观点吗", "--unit", "my-post", "--blocking")
	writeFile(t, wsPath(dir, briefRel), completeBrief+"key_messages:\n  - {id: km-1, text: 观点, decision: dec-1}\n")
	code, res = runUnits(t, dir, "advance", "my-post")
	if d := diag(t, res.result, "brief.unconfirmed-message"); code != 1 || !hasNext(d, "wa-cli decisions --dir") {
		t.Fatalf("unconfirmed: exit code = %d, diagnostic = %+v", code, d)
	}
	_, res = runUnits(t, dir, "next", "my-post")
	if got := blockerCodes(res.Steps[0]); !slices.Equal(got, []string{"brief.unconfirmed-message", "decision.blocking"}) || !slices.Equal(commands(res.Steps[0]), []string{"wa-cli decisions --dir " + dir + " --json"}) {
		t.Fatalf("unconfirmed next: blockers = %v, commands = %v", got, commands(res.Steps[0]))
	}
	mustRunWS(t, dir, "decide", "dec-1", "--answer", "是", "--confirmation", "是我的观点")
	// gate-invalid
	writeFile(t, wsPath(dir, briefRel), "k: v\n")
	code, res = runUnits(t, dir, "advance", "my-post")
	if d := diag(t, res.result, "brief.invalid"); code != 1 || !hasNext(d, "wa-cli validate "+briefRel) || readFile(t, workFile(dir, "my-post")) != before {
		t.Fatalf("gate-invalid: exit code = %d, diagnostic = %+v", code, d)
	}
	if _, res = runUnits(t, dir, "next", "my-post"); !slices.Equal(commands(res.Steps[0]), []string{"wa-cli validate " + briefRel + " --dir " + dir + " --json"}) || !strings.Contains(res.Steps[0]["action"].(string), "修复不通过校验的写作简报") {
		t.Fatalf("gate-invalid next: step = %v", res.Steps[0])
	}
	// gate-brief-md
	os.Remove(wsPath(dir, briefRel))
	writeFile(t, wsPath(dir, ".writing-anything/works/my-post/brief.md"), "自由文本\n")
	code, res = runUnits(t, dir, "advance", "my-post")
	if code != 1 || len(res.Diagnostics) != 2 || res.Diagnostics[0].Code != "brief.missing-field" || res.Diagnostics[1].Path != ".writing-anything/works/my-post/brief.md" {
		t.Fatalf("gate-brief-md: exit code = %d, diagnostics = %+v", code, res.Diagnostics)
	}
	// gate-passes
	os.Remove(wsPath(dir, ".writing-anything/works/my-post/brief.md"))
	writeFile(t, wsPath(dir, briefRel), completeBrief+"key_messages:\n  - {id: km-1, text: 观点, sources: [src-a]}\n")
	if code, res := runUnits(t, dir, "advance", "my-post"); code != 0 || res.Unit["stage"] != "draft" {
		t.Fatalf("gate-passes: exit code = %d, result = %+v", code, res)
	}
	// gate-later-stage
	writeFile(t, wsPath(dir, briefRel), "schema_version: 1\naudience: 读者\nprovenance:\n  audience: {sources: [src-a]}\n")
	artifact(t, dir, "my-post", "", "draft.md")
	if code, res := runUnits(t, dir, "advance", "my-post"); code != 0 || res.Unit["stage"] != "review" {
		t.Fatalf("gate-later-stage: exit code = %d, result = %+v", code, res)
	}
	// gate-section
	runWS(t, dir, "unit", "add", "my-post", "section", "intro")
	os.MkdirAll(wsPath(dir, ".writing-anything/works/my-post/units/intro"), 0o755)
	writeFile(t, wsPath(dir, ".writing-anything/works/my-post/units/intro/brief.yaml"), "schema_version: 1\n")
	if code, res := runUnits(t, dir, "advance", "my-post/intro"); code != 0 || res.Unit["stage"] != "draft" {
		t.Fatalf("gate-section: exit code = %d, result = %+v", code, res)
	}
}

func TestBriefValidate(t *testing.T) {
	dir := briefWork(t)
	// validate-brief
	writeFile(t, wsPath(dir, briefRel), completeBrief+"key_messages:\n  - {id: km-1, text: 观点, sources: [src-a]}\n")
	if code, res := runWS(t, dir, "validate"); code != 0 || !slices.Contains(res.Checked, briefRel) {
		t.Fatalf("validate-brief: exit code = %d, result = %+v", code, res)
	}
	tests := []struct {
		name, content, code, field, next string
	}{
		{"duplicate-message", "schema_version: 1\nkey_messages:\n  - {id: km-1, text: a}\n  - {id: km-1, text: b}\n", "artifact.duplicate-id", "key_messages[1].id", "wa-cli validate --dir"},
		{"unknown-source", "schema_version: 1\nsources: [src-nope]\n", "brief.unknown-source", "sources[0]", "wa-cli source list"},
		{"unknown-decision", "schema_version: 1\nkey_messages:\n  - {id: km-1, text: a, decision: dec-9}\n", "brief.unknown-decision", "key_messages[0].decision", "wa-cli decisions"},
		{"invalid-length", "schema_version: 1\nlength: {min: 3000, max: 1500}\n", "brief.invalid-length", "length", "wa-cli validate " + briefRel},
		{"schema-violation", "schema_version: 1\ntone: 轻松\n", "artifact.schema-violation", "tone", "wa-cli validate " + briefRel},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			writeFile(t, wsPath(dir, briefRel), tt.content)
			for _, args := range [][]string{{"validate"}, {"validate", briefRel}} {
				code, res := runWS(t, dir, args...)
				d := diag(t, res, tt.code)
				if code != 1 || d.Path != briefRel || d.Field != tt.field || !hasNext(d, tt.next) {
					t.Fatalf("%v: exit code = %d, diagnostic = %+v", args, code, d)
				}
			}
		})
	}
}

// 途经符号链接的 brief.yaml 在所有入口都按同一问题处理：不读取外部内容，也不覆盖链接（validated-path-segments）。
func TestBriefSymlink(t *testing.T) {
	dir := briefWork(t)
	outside := wsPath(t.TempDir(), "brief.yaml")
	writeFile(t, outside, completeBrief)
	if err := os.Symlink(outside, wsPath(dir, briefRel)); err != nil {
		t.Skip("symlinks unavailable:", err)
	}
	for _, args := range [][]string{{"brief", "show", "my-post"}, {"brief", "set", "my-post", "--audience", "x"}, {"validate"}} {
		code, res := runWS(t, dir, args...)
		if d := diag(t, res, "artifact.not-found"); code != 1 || d.Path != briefRel || !hasNext(d, "wa-cli status") {
			t.Fatalf("%v: exit code = %d, diagnostic = %+v", args, code, d)
		}
	}
	if code, res := runWS(t, dir, "advance", "my-post"); code != 1 || diag(t, res, "brief.invalid").Path != briefRel {
		t.Fatalf("advance: exit code = %d", code)
	}
	if info, err := os.Lstat(wsPath(dir, briefRel)); err != nil || info.Mode()&os.ModeSymlink == 0 || readFile(t, outside) != completeBrief {
		t.Fatal("symlink or outside file changed")
	}

	// 单元目录本身是指向外部的符号链接：不读取外部的 brief.yaml，也不向其中写入。
	runWS(t, dir, "unit", "add", "my-post", "section", "intro")
	outDir := t.TempDir()
	writeFile(t, wsPath(outDir, "brief.yaml"), completeBrief)
	os.MkdirAll(wsPath(dir, ".writing-anything/works/my-post/units"), 0o755)
	if err := os.Symlink(outDir, wsPath(dir, ".writing-anything/works/my-post/units/intro")); err != nil {
		t.Fatal(err)
	}
	if code, res := runWS(t, dir, "brief", "show", "my-post/intro"); code != 1 || diag(t, res, "artifact.not-found").Path != ".writing-anything/works/my-post/units/intro/brief.yaml" {
		t.Fatalf("symlinked unit dir show: exit code = %d", code)
	}
	os.Remove(wsPath(outDir, "brief.yaml"))
	if code, res := runWS(t, dir, "brief", "set", "my-post/intro", "--purpose", "p"); code != 1 || len(res.Diagnostics) == 0 {
		t.Fatalf("symlinked unit dir set: exit code = %d", code)
	}
	if entries, _ := os.ReadDir(outDir); len(entries) != 0 {
		t.Fatalf("wrote into the symlinked unit dir: %v", entries)
	}
}

// 字段出处（Field Provenance）：--<字段>-sources/--<字段>-decision 写入与校验、关卡阻塞（brief.unsourced-field
// 与 brief.unconfirmed-field）与解除、视图与 show 问题。
func TestBriefFieldProvenance(t *testing.T) {
	dir := briefWork(t)
	// 写入：视图与 brief.yaml 都含 provenance。
	code, res := runBrief(t, dir, "brief", "set", "my-post", "--audience", "后端工程师", "--audience-sources", "src-a",
		"--purpose", "讲清楚为什么要做连接池", "--purpose-decision", "dec-x")
	if d := diag(t, res.result, "brief.unknown-decision"); code != 1 || d.Field != "purpose-decision" {
		t.Fatalf("unknown decision: code = %d, diag = %+v", code, d)
	}
	runWS(t, dir, "decision", "add", "--question", "受众与目的是你定的吗", "--unit", "my-post")
	code, res = runBrief(t, dir, "brief", "set", "my-post", "--audience", "后端工程师", "--audience-sources", "src-a",
		"--purpose", "讲清楚为什么要做连接池", "--purpose-decision", "dec-1")
	if code != 0 || res.Brief["provenance"].(map[string]any)["audience"].(map[string]any)["sources"].([]any)[0] != "src-a" {
		t.Fatalf("set with provenance: code = %d, brief = %v, %v", code, res.Brief, res.Diagnostics)
	}
	if got := readFile(t, wsPath(dir, briefRel)); !strings.Contains(got, "provenance:") || !strings.Contains(got, "audience:") || !strings.Contains(got, "decision: dec-1") {
		t.Fatalf("brief.yaml:\n%s", got)
	}
	// 关卡：audience 有关联素材、purpose 只关联 open 决策 → 一条 unconfirmed，decide 后推进通过。
	if code, res := runUnits(t, dir, "advance", "my-post"); code != 1 {
		t.Fatalf("advance should block: %d %v", code, res.Diagnostics)
	} else if d := diag(t, res.result, "brief.unconfirmed-field"); !strings.Contains(d.Message, "purpose") || !strings.Contains(d.Message, "dec-1") || !hasNext(d, "wa-cli decisions") {
		t.Fatalf("unconfirmed-field: %+v", d)
	}
	runWS(t, dir, "decide", "dec-1", "--answer", "是", "--confirmation", "作者：是我定的")
	if code, res := runUnits(t, dir, "advance", "my-post"); code != 0 {
		t.Fatalf("advance after decide: %d %v", code, res.Diagnostics)
	}
	// show：无 provenance 的已填必填字段报 unsourced-field。
	dir2 := briefWork(t)
	runBrief(t, dir2, "brief", "set", "my-post", "--audience", "读者", "--purpose", "目的")
	if code, res := runBrief(t, dir2, "brief", "show", "my-post"); code != 0 ||
		!slices.Equal(questionSummary(res.Questions), []string{"brief.unsourced-field audience", "brief.unsourced-field purpose"}) {
		t.Fatalf("show-unsourced-field: code = %d, questions = %v", code, res.Questions)
	}
}

// provenanceDecision 返回 brief 视图里字段出处的决策 id；没有该字段或决策时为 nil。
func provenanceDecision(brief map[string]any, field string) any {
	p, ok := brief["provenance"].(map[string]any)[field].(map[string]any)
	if !ok {
		return nil
	}
	return p["decision"]
}

// 共用决策（--decision）：补位本次写入的字段、前提与核心观点，显式字段级优先，删除与未写入的字段不补位。
func TestBriefSharedDecision(t *testing.T) {
	t.Run("fills written scalar fields and key message", func(t *testing.T) {
		dir := briefWork(t)
		runWS(t, dir, "decision", "add", "--question", "本轮回答都是你定的吗", "--unit", "my-post")
		code, res := runBrief(t, dir, "brief", "set", "my-post", "--decision", "dec-1",
			"--audience", "后端工程师", "--purpose", "讲清楚为什么要做连接池", "--theme", "连接池的取舍",
			"--message", "km-1", "--message-text", "连接池能显著降低延迟")
		if code != 0 || !slices.Equal(res.ChangedPaths, []string{briefRel}) {
			t.Fatalf("exit code = %d, result = %+v", code, res)
		}
		for _, f := range []string{"audience", "purpose", "theme"} {
			if got := provenanceDecision(res.Brief, f); got != "dec-1" {
				t.Fatalf("provenance.%s.decision = %v, brief = %v", f, got, res.Brief)
			}
		}
		if km := res.Brief["keyMessages"].([]any)[0].(map[string]any); km["decision"] != "dec-1" {
			t.Fatalf("key message decision = %v", km["decision"])
		}
	})

	t.Run("fills premise pov and length", func(t *testing.T) {
		dir := newWorkspace(t)
		if code, stdout, stderr := run(t, "new", "short-story", "my-story", "--dir", dir); code != 0 {
			t.Fatalf("new: exit code = %d; stdout: %s; stderr: %s", code, stdout, stderr)
		}
		runWS(t, dir, "decision", "add", "--question", "这些设定都是你定的吗", "--unit", "my-story")
		code, res := runBrief(t, dir, "brief", "set", "my-story", "--decision", "dec-1",
			"--premise", "守灯人发现灯里住着说谎的精灵", "--pov-person", "third", "--length", "2000-8000")
		if code != 0 || res.Brief["premise"].(map[string]any)["decision"] != "dec-1" ||
			provenanceDecision(res.Brief, "pov") != "dec-1" || provenanceDecision(res.Brief, "length") != "dec-1" {
			t.Fatalf("exit code = %d, brief = %v, %v", code, res.Brief, res.Diagnostics)
		}
	})

	t.Run("explicit field decision wins", func(t *testing.T) {
		dir := briefWork(t)
		runWS(t, dir, "decision", "add", "--question", "第一个决策", "--unit", "my-post")
		runWS(t, dir, "decision", "add", "--question", "第二个决策", "--unit", "my-post")
		code, res := runBrief(t, dir, "brief", "set", "my-post", "--decision", "dec-1",
			"--audience", "后端工程师", "--purpose", "讲清楚", "--purpose-decision", "dec-2")
		if code != 0 || provenanceDecision(res.Brief, "audience") != "dec-1" || provenanceDecision(res.Brief, "purpose") != "dec-2" {
			t.Fatalf("exit code = %d, brief = %v", code, res.Brief)
		}
	})

	t.Run("empty explicit field decision is not filled", func(t *testing.T) {
		dir := briefWork(t)
		runWS(t, dir, "decision", "add", "--question", "受众是你定的吗", "--unit", "my-post")
		if code, res := runBrief(t, dir, "brief", "set", "my-post", "--audience", "后端工程师", "--audience-decision", "dec-1"); code != 0 {
			t.Fatalf("seed: exit code = %d, %v", code, res.Diagnostics)
		}
		code, res := runBrief(t, dir, "brief", "set", "my-post", "--decision", "dec-1", "--audience", "前端工程师", "--audience-decision=")
		if code != 0 || provenanceDecision(res.Brief, "audience") != nil {
			t.Fatalf("empty explicit decision: exit code = %d, provenance = %v", code, res.Brief["provenance"])
		}
	})

	t.Run("deleted and unwritten targets are not filled", func(t *testing.T) {
		dir := briefWork(t)
		runWS(t, dir, "decision", "add", "--question", "受众是你定的吗", "--unit", "my-post")
		runWS(t, dir, "decision", "add", "--question", "本轮回答", "--unit", "my-post")
		if code, res := runBrief(t, dir, "brief", "set", "my-post", "--audience", "后端工程师", "--audience-decision", "dec-1",
			"--message", "km-1", "--message-text", "观点", "--message-decision", "dec-1"); code != 0 {
			t.Fatalf("seed: exit code = %d, %v", code, res.Diagnostics)
		}
		code, res := runBrief(t, dir, "brief", "set", "my-post", "--decision", "dec-2", "--purpose", "讲清楚")
		if code != 0 || provenanceDecision(res.Brief, "purpose") != "dec-2" ||
			provenanceDecision(res.Brief, "audience") != "dec-1" ||
			res.Brief["keyMessages"].([]any)[0].(map[string]any)["decision"] != "dec-1" {
			t.Fatalf("unwritten targets changed: exit code = %d, brief = %v", code, res.Brief)
		}
		if _, ok := res.Brief["provenance"].(map[string]any)["theme"]; ok {
			t.Fatalf("unwritten theme gained provenance: %v", res.Brief["provenance"])
		}
		// 删除字段不补位：没有可补位的写入，报参数错误且文件不变。
		before := readFile(t, wsPath(dir, briefRel))
		if code, res := runBrief(t, dir, "brief", "set", "my-post", "--decision", "dec-2", "--audience="); code != 1 ||
			diag(t, res.result, "input.invalid-value").Field != "decision" || readFile(t, wsPath(dir, briefRel)) != before {
			t.Fatalf("delete: exit code = %d, %v", code, res.Diagnostics)
		}
	})

	t.Run("no fill target is an error", func(t *testing.T) {
		dir := briefWork(t)
		runWS(t, dir, "decision", "add", "--question", "本轮回答", "--unit", "my-post")
		if code, res := runBrief(t, dir, "brief", "set", "my-post", "--audience", "后端工程师"); code != 0 {
			t.Fatalf("seed: exit code = %d, %v", code, res.Diagnostics)
		}
		before := readFile(t, wsPath(dir, briefRel))
		for _, args := range [][]string{
			{"--decision", "dec-1"},
			{"--decision", "dec-1", "--sources", "src-a"},
			{"--decision", "dec-1", "--audience="},
		} {
			code, res := runBrief(t, dir, append([]string{"brief", "set", "my-post"}, args...)...)
			if code != 1 || res.Brief != nil || len(res.ChangedPaths) != 0 || diag(t, res.result, "input.invalid-value").Field != "decision" {
				t.Fatalf("%v: exit code = %d, result = %+v", args, code, res)
			}
			if readFile(t, wsPath(dir, briefRel)) != before {
				t.Fatalf("%v: brief.yaml changed", args)
			}
		}
	})

	t.Run("validation matches field-level parameters", func(t *testing.T) {
		dir := briefWork(t)
		runWS(t, dir, "decision", "add", "--question", "与本单元无关的决策")
		runWS(t, dir, "decision", "add", "--question", "关联本单元的决策", "--unit", "my-post")
		if code, res := runBrief(t, dir, "brief", "set", "my-post", "--audience", "后端工程师"); code != 0 {
			t.Fatalf("seed: exit code = %d, %v", code, res.Diagnostics)
		}
		before := readFile(t, wsPath(dir, briefRel))
		for _, tc := range []struct {
			args  []string
			code  string
			field string
		}{
			{[]string{"--decision", "dec-9", "--audience", "读者"}, "brief.unknown-decision", "audience-decision"},
			{[]string{"--decision", "dec-9", "--premise", "前提"}, "brief.unknown-decision", "premise-decision"},
			{[]string{"--decision", "dec-9", "--message", "km-1", "--message-text", "观点"}, "brief.unknown-decision", "message-decision"},
			{[]string{"--decision", "dec-1", "--audience", "读者"}, "brief.unrelated-decision", "audience-decision"},
		} {
			code, res := runBrief(t, dir, append([]string{"brief", "set", "my-post"}, tc.args...)...)
			d := diag(t, res.result, tc.code)
			if code != 1 || d.Field != tc.field || d.Path != "" || res.Brief != nil || len(res.ChangedPaths) != 0 || readFile(t, wsPath(dir, briefRel)) != before {
				t.Fatalf("%v: exit code = %d, diagnostic = %+v", tc.args, code, d)
			}
		}
		// 决策队列无法加载时沿用字段级参数的 decision.invalid-queue（队列级诊断，不填 field）。
		queue := wsPath(dir, ".writing-anything/decisions.yaml")
		good := readFile(t, queue)
		writeFile(t, queue, "decisions: [\n")
		code, res := runBrief(t, dir, "brief", "set", "my-post", "--decision", "dec-2", "--audience", "读者")
		d := diag(t, res.result, "decision.invalid-queue")
		if code != 1 || d.Path != ".writing-anything/decisions.yaml" || d.Field != "" || readFile(t, wsPath(dir, briefRel)) != before {
			t.Fatalf("invalid queue: exit code = %d, diagnostic = %+v", code, d)
		}
		writeFile(t, queue, good)
	})

	t.Run("empty decision is not given", func(t *testing.T) {
		dir := briefWork(t)
		runWS(t, dir, "decision", "add", "--question", "受众是你定的吗", "--unit", "my-post")
		if code, res := runBrief(t, dir, "brief", "set", "my-post", "--audience", "后端工程师", "--audience-decision", "dec-1"); code != 0 {
			t.Fatalf("seed: exit code = %d, %v", code, res.Diagnostics)
		}
		code, res := runBrief(t, dir, "brief", "set", "my-post", "--decision=", "--audience", "前端工程师")
		if code != 0 || provenanceDecision(res.Brief, "audience") != "dec-1" {
			t.Fatalf("empty decision: exit code = %d, provenance = %v", code, res.Brief["provenance"])
		}
	})
}
