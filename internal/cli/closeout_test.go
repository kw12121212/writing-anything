package cli

import (
	"encoding/json"
	"os"
	"path/filepath"
	"strings"
	"testing"
)

type closeoutViewOut struct {
	Result
	Closeout *struct {
		Unit        string `json:"unit"`
		Path        string `json:"path"`
		DraftSHA256 string `json:"draftSha256"`
		Events      []string
		Canon       []string
		State       []struct {
			ID       string `json:"id"`
			Contract string `json:"contract"`
		} `json:"state"`
		Knowledge []string `json:"knowledge"`
		Plants    []string `json:"plants"`
		Recovers  []string `json:"recovers"`
		Waived    []string `json:"waived"`
		Suspected []struct {
			Name   string `json:"name"`
			Source string `json:"source"`
		} `json:"suspected"`
	} `json:"closeout"`
}

// closeoutFixture 建 novel 工作区：canon-1（守灯人）、伏笔 fs-1 与 f-1（均埋设于 scene-1），scene-1 的契约声明
// state_changes sc-1、plants fs-1、recovers f-1、events ev-2 与出场人物守灯人、精灵、地点灯室，并写入正文。
func closeoutFixture(t *testing.T) string {
	t.Helper()
	dir := newNovel(t)
	for _, args := range [][]string{
		{"canon", "add", "my-novel", "--type", "character", "--name", "守灯人", "--text", "五十岁，左手有旧伤"},
		{"threads", "add", "my-novel", "--text", "灯塔的旧账", "--plant-in", "my-novel/scene-1", "--recover-at", "my-novel/chapter-two", "--id", "fs-1"},
		{"threads", "add", "my-novel", "--text", "灯芯的来历", "--plant-in", "my-novel/scene-1", "--recover-at", "my-novel/scene-2", "--id", "f-1"},
	} {
		if code, res := runWS(t, dir, args...); code != 0 || !res.Valid {
			t.Fatalf("%v: exit code = %d, result = %+v", args, code, res)
		}
	}
	code, res := runWS(t, dir, "scene", "contract", "my-novel/scene-1", "sc1",
		"--title", "灯下对峙", "--pov", "守灯人", "--characters", "守灯人,精灵", "--location", "灯室",
		"--state-change", "sc-1=精灵交出灯芯", "--plant", "fs-1", "--recover", "f-1", "--event", "ev-2")
	if code != 0 || !res.Valid {
		t.Fatalf("scene contract: exit code = %d, result = %+v", code, res)
	}
	writeFile(t, filepath.Join(dir, ".writing-anything", "works", "my-novel", "units", "scene-1", "draft.md"), "正文第一段。\n正文第二段。\n")
	closeoutPayload(t, dir, true)
	return dir
}

// closeoutPayload 写入并返回满足契约的收口输入文件；waived 为 true 时携带豁免名单。
func closeoutPayload(t *testing.T, dir string, waived bool) string {
	t.Helper()
	text := `state_changes:
  - {entry: canon-1, state: 灯芯易主给精灵, contract: sc-1}
knowledge:
  - {entry: canon-1, character: reader}
recovers: [f-1]
events: [ev-2]
`
	if waived {
		text += "waived: [精灵, 灯室]\n"
	}
	p := filepath.Join(dir, "closeout-input.yaml")
	writeFile(t, p, text)
	return p
}

func closeoutOf(t *testing.T, r result) *closeoutViewOut {
	t.Helper()
	var out closeoutViewOut
	if err := json.Unmarshal([]byte(r.stdout), &out); err != nil {
		t.Fatal(err)
	}
	return &out
}

// scene close-out 从 --file 与标准输入提交：信封、closeout 字段、changedPaths 与退出码。
func TestSceneCloseOutSubmit(t *testing.T) {
	dir := closeoutFixture(t)
	code, res := runWS(t, dir, "scene", "close-out", "my-novel/scene-1", "--file", "closeout-input.yaml")
	if code != 0 {
		t.Fatalf("exit code = %d, result = %+v", code, res)
	}
	// 文件参数相对 --dir 解析。
	if _, err := os.Stat(filepath.Join(dir, "closeout-input.yaml")); err != nil {
		t.Fatal(err)
	}
	code, res = runWS(t, dir, "scene", "close-out", "my-novel/scene-1", "--file", "closeout-input.yaml")
	if code != 1 {
		t.Fatalf("second submit exit code = %d, want 1（already-closed）", code)
	}
	// 标准输入入口：另一场景 scene-2 无契约，补正文后以空载荷收口。
	if err := os.MkdirAll(filepath.Join(dir, ".writing-anything", "works", "my-novel", "units", "scene-2"), 0o755); err != nil {
		t.Fatal(err)
	}
	writeFile(t, filepath.Join(dir, ".writing-anything", "works", "my-novel", "units", "scene-2", "draft.md"), "正文。\n")
	code, res = runWSIn(t, dir, "{}\n", "scene", "close-out", "my-novel/scene-2")
	if code != 0 || res.ChangedPaths == nil {
		t.Fatalf("stdin submit: exit code = %d, result = %+v", code, res)
	}
}

func TestSceneCloseOutFullPayload(t *testing.T) {
	dir := closeoutFixture(t)
	code, res := runWS(t, dir, "scene", "close-out", "my-novel/scene-1", "--file", "closeout-input.yaml")
	if code != 0 {
		t.Fatalf("fixture state: %+v", res)
	}
	out := closeoutOf(t, res)
	if out.Closeout == nil || out.Closeout.Unit != "my-novel/scene-1" {
		t.Fatalf("closeout = %+v", out.Closeout)
	}
	if len(out.Closeout.State) != 1 || !strings.HasPrefix(out.Closeout.State[0].ID, "state-") || out.Closeout.State[0].Contract != "sc-1" {
		t.Fatalf("state = %+v", out.Closeout.State)
	}
	if len(out.Closeout.Recovers) != 1 || out.Closeout.Recovers[0] != "f-1" || len(out.Closeout.Events) != 1 || out.Closeout.Events[0] != "ev-2" {
		t.Fatalf("closeout = %+v", out.Closeout)
	}
	found := map[string]bool{}
	for _, c := range res.ChangedPaths {
		found[c] = true
	}
	for _, rel := range []string{
		".writing-anything/works/my-novel/state.yaml",
		".writing-anything/works/my-novel/knowledge.yaml",
		".writing-anything/works/my-novel/threads.yaml",
		".writing-anything/works/my-novel/units/scene-1/closeout.yaml",
	} {
		if !found[rel] {
			t.Fatalf("changedPaths %v missing %s", res.ChangedPaths, rel)
		}
	}
	// 重放语义：收口后 state at 反映新提议（未确认不生效，确认后生效——这里只验证命令可用）。
	if code, res := runWS(t, dir, "state", "list", "my-novel"); code != 0 {
		t.Fatalf("state list: exit code = %d, result = %+v", code, res)
	}
}

// 契约缺口拒绝写入：outline、state、knowledge、threads 与 closeout.yaml 都未创建或未变。
func TestSceneCloseOutContractGap(t *testing.T) {
	dir := closeoutFixture(t)
	writeFile(t, filepath.Join(dir, "gap-input.yaml"), "recovers: [f-1]\nevents: [ev-2]\n")
	before, _ := os.ReadFile(filepath.Join(dir, ".writing-anything", "works", "my-novel", "threads.yaml"))
	code, res := runWS(t, dir, "scene", "close-out", "my-novel/scene-1", "--file", "gap-input.yaml")
	if code != 1 {
		t.Fatalf("exit code = %d, want 1", code)
	}
	d := diag(t, res, "closeout.missing-state-change")
	if d.Code == "" {
		t.Fatalf("diagnostics = %+v", res.Diagnostics)
	}
	if !strings.Contains(strings.Join(d.Next, "\n"), "wa-cli help scene close-out") ||
		!strings.Contains(strings.Join(d.Next, "\n"), "wa-cli help scene contract") {
		t.Fatalf("next = %v", d.Next)
	}
	if _, err := os.Stat(filepath.Join(dir, ".writing-anything", "works", "my-novel", "state.yaml")); err == nil {
		t.Fatal("state.yaml must not be created on rejected submit")
	}
	if _, err := os.Stat(filepath.Join(dir, ".writing-anything", "works", "my-novel", "units", "scene-1", "closeout.yaml")); err == nil {
		t.Fatal("closeout.yaml must not be created on rejected submit")
	}
	after, _ := os.ReadFile(filepath.Join(dir, ".writing-anything", "works", "my-novel", "threads.yaml"))
	if string(before) != string(after) {
		t.Fatal("threads.yaml changed on rejected submit")
	}
}

func TestSceneCloseOutNotSceneNoDraft(t *testing.T) {
	dir := closeoutFixture(t)
	if code, res := runWS(t, dir, "scene", "close-out", "my-novel/chapter-one"); code != 1 || diag(t, res, "scene.not-scene").Code == "" {
		t.Fatalf("not-scene: exit code = %d, diagnostics = %+v", code, res.Diagnostics)
	}
	if code, res := runWSIn(t, dir, "{}\n", "scene", "close-out", "my-novel/scene-2"); code != 1 || diag(t, res, "draft.not-found").Code == "" {
		t.Fatalf("no-draft: exit code = %d, diagnostics = %+v", code, res.Diagnostics)
	}
}

// 疑似专有名词提示为 warning：不改变退出码，人读输出列出候选。
func TestSceneCloseOutSuspectedWarning(t *testing.T) {
	dir := closeoutFixture(t)
	writeFile(t, filepath.Join(dir, "bare-input.yaml"), "state_changes:\n  - {entry: canon-1, state: 灯芯易主给精灵, contract: sc-1}\nrecovers: [f-1]\nevents: [ev-2]\n")
	code, res := runWS(t, dir, "scene", "close-out", "my-novel/scene-1", "--file", "bare-input.yaml")
	if code != 0 {
		t.Fatalf("exit code = %d, result = %+v", code, res)
	}
	var suspected []Diagnostic
	for _, d := range res.Diagnostics {
		if d.Code == "closeout.suspected-name" {
			suspected = append(suspected, d)
			if d.Severity != "warning" {
				t.Fatalf("severity = %s, want warning", d.Severity)
			}
		}
	}
	if len(suspected) != 2 {
		t.Fatalf("suspected-name diagnostics = %+v", res.Diagnostics)
	}
	// 人读输出列出疑似名单。
	code, stdout, stderr := run(t, "scene", "close-out", "my-novel/scene-1", "--replace", "--file", "bare-input.yaml", "--dir", dir)
	if code != 0 {
		t.Fatalf("exit code = %d, stderr = %q", code, stderr)
	}
	if !strings.Contains(stdout, "疑似专有名词") || !strings.Contains(stdout, "精灵") {
		t.Fatalf("human output missing suspected names: %q", stdout)
	}
}

func TestSceneCloseOutWaive(t *testing.T) {
	dir := closeoutFixture(t)
	writeFile(t, filepath.Join(dir, "bare-input.yaml"), "state_changes:\n  - {entry: canon-1, state: 灯芯易主给精灵, contract: sc-1}\nrecovers: [f-1]\nevents: [ev-2]\n")
	if code, res := runWS(t, dir, "scene", "close-out", "my-novel/scene-1", "--file", "bare-input.yaml"); code != 0 {
		t.Fatalf("submit: exit code = %d, result = %+v", code, res)
	}
	code, res := runWS(t, dir, "scene", "close-out", "my-novel/scene-1", "--waive", "精灵", "--waive", "灯室")
	if code != 0 {
		t.Fatalf("waive: exit code = %d, result = %+v", code, res)
	}
	out := closeoutOf(t, res)
	if len(out.Closeout.Waived) != 2 || len(out.Closeout.Suspected) != 0 {
		t.Fatalf("closeout = %+v", out.Closeout)
	}
	if len(res.ChangedPaths) != 1 || res.ChangedPaths[0] != ".writing-anything/works/my-novel/units/scene-1/closeout.yaml" {
		t.Fatalf("changedPaths = %v", res.ChangedPaths)
	}
	// 未收口的场景报告 closeout.not-closed。
	if err := os.MkdirAll(filepath.Join(dir, ".writing-anything", "works", "my-novel", "units", "scene-2"), 0o755); err != nil {
		t.Fatal(err)
	}
	writeFile(t, filepath.Join(dir, ".writing-anything", "works", "my-novel", "units", "scene-2", "draft.md"), "正文。\n")
	if code, res := runWS(t, dir, "scene", "close-out", "my-novel/scene-2", "--waive", "精灵"); code != 1 || diag(t, res, "closeout.not-closed").Code == "" {
		t.Fatalf("not-closed: exit code = %d, diagnostics = %+v", code, res.Diagnostics)
	}
}

func TestSceneCloseOutReplaceReclose(t *testing.T) {
	dir := closeoutFixture(t)
	if code, res := runWS(t, dir, "scene", "close-out", "my-novel/scene-1", "--file", "closeout-input.yaml"); code != 0 {
		t.Fatalf("first submit: %+v", res)
	}
	// 作者改契约新增 sc-2 后重收口：--replace 整体替换收口记录，不清理既有台账。
	writeFile(t, filepath.Join(dir, "second-input.yaml"),
		"state_changes:\n  - {entry: canon-1, state: 灯芯易主给精灵, contract: sc-1}\n  - {entry: canon-1, state: 守灯人受伤, contract: sc-2}\nrecovers: [f-1]\nevents: [ev-2]\n")
	if code, res := runWS(t, dir, "scene", "close-out", "my-novel/scene-1", "--file", "second-input.yaml"); code != 1 || diag(t, res, "closeout.already-closed").Code == "" {
		t.Fatalf("resubmit without --replace: exit code = %d, diagnostics = %+v", code, res.Diagnostics)
	}
	runWS(t, dir, "scene", "contract", "my-novel/scene-1", "sc1", "--state-change", "sc-2=守灯人受伤")
	code, res := runWS(t, dir, "scene", "close-out", "my-novel/scene-1", "--replace", "--file", "second-input.yaml")
	if code != 0 {
		t.Fatalf("replace submit: exit code = %d, result = %+v", code, res)
	}
	out := closeoutOf(t, res)
	if len(out.Closeout.State) != 2 {
		t.Fatalf("state refs = %+v, want 2", out.Closeout.State)
	}
}

// help scene 命令组列出 close-out。
func TestSceneCloseOutHelp(t *testing.T) {
	code, stdout, stderr := run(t, "help", "scene", "close-out", "--json")
	if code != 0 || stderr != "" {
		t.Fatalf("exit code = %d, stderr = %q; stdout: %s", code, stderr, stdout)
	}
	if !strings.Contains(stdout, "写后收口") {
		t.Fatalf("help scene close-out missing summary: %q", stdout)
	}
}

// closeoutAcceptFixture 把 novel 场景推进到审稿阶段（gate fast），契约要求收口。
func closeoutAcceptFixture(t *testing.T) string {
	t.Helper()
	dir := newWorkspace(t, "--genre", "novel", "--gate-level", "fast")
	for _, args := range [][]string{
		{"new", "novel", "my-novel"},
		{"unit", "add", "my-novel", "part", "part-one"},
		{"unit", "add", "my-novel/part-one", "chapter", "chapter-one"},
		{"unit", "add", "my-novel/chapter-one", "scene", "scene-1"},
	} {
		if code, stdout, stderr := run(t, append(args, "--dir", dir)...); code != 0 {
			t.Fatalf("%v: exit code = %d; stdout: %s; stderr: %s", args, code, stdout, stderr)
		}
	}
	sceneDir := filepath.Join(dir, ".writing-anything", "works", "my-novel", "units", "scene-1")
	if err := os.MkdirAll(sceneDir, 0o755); err != nil {
		t.Fatal(err)
	}
	writeFile(t, filepath.Join(sceneDir, "brief.yaml"), "schema_version: 1\n")
	run(t, "advance", "my-novel/scene-1", "--dir", dir)
	for _, args := range [][]string{
		{"canon", "add", "my-novel", "--type", "character", "--name", "守灯人", "--text", "五十岁"},
		{"threads", "add", "my-novel", "--text", "灯芯的来历", "--plant-in", "my-novel/scene-1", "--recover-at", "my-novel/scene-1", "--id", "f-1"},
	} {
		if code, res := runWS(t, dir, args...); code != 0 {
			t.Fatalf("%v: exit code = %d, result = %+v", args, code, res)
		}
	}
	runWS(t, dir, "outline", "set", "my-novel/scene-1", "sc1", "--title", "灯下对峙", "--reason", "推进情节")
	if code, res := runWS(t, dir, "scene", "contract", "my-novel/scene-1", "sc1",
		"--state-change", "sc-1=精灵交出灯芯", "--plant", "f-1", "--recover", "f-1", "--event", "ev-2"); code != 0 {
		t.Fatalf("scene contract: exit code = %d, result = %+v", code, res)
	}
	writeFile(t, filepath.Join(sceneDir, "draft.md"), "正文。\n")
	run(t, "advance", "my-novel/scene-1", "--dir", dir)
	run(t, "advance", "my-novel/scene-1", "--dir", dir)
	return dir
}

func acceptPayload(t *testing.T, dir string) string {
	t.Helper()
	text := "state_changes:\n  - {entry: canon-1, state: 灯芯易主给精灵, contract: sc-1}\nknowledge:\n  - {entry: canon-1, character: reader}\nrecovers: [f-1]\nevents: [ev-2]\n"
	p := filepath.Join(dir, "closeout-input.yaml")
	writeFile(t, p, text)
	return p
}

// 里程碑完成标准：契约要求收口的场景未收口时，accept 在所有关卡强度（这里 fast）下都被拒绝。
func TestAcceptCloseoutMissing(t *testing.T) {
	dir := closeoutAcceptFixture(t)
	code, res := runWS(t, dir, "accept", "my-novel/scene-1")
	if code != 1 {
		t.Fatalf("exit code = %d, want 1", code)
	}
	d := diag(t, res, "closeout.missing")
	if d.Code == "" {
		t.Fatalf("diagnostics = %+v", res.Diagnostics)
	}
	if !strings.Contains(strings.Join(d.Next, "\n"), "wa-cli scene close-out my-novel/scene-1") ||
		!strings.Contains(strings.Join(d.Next, "\n"), "wa-cli review show my-novel/scene-1") {
		t.Fatalf("next = %v", d.Next)
	}
	if _, err := os.Stat(filepath.Join(dir, ".writing-anything", "works", "my-novel", "units", "scene-1", "acceptance.yaml")); err == nil {
		t.Fatal("acceptance.yaml must not be written while blocked")
	}
	// review show 的 gate.blockers 同样呈现收口阻塞项。
	code, res = runWS(t, dir, "review", "show", "my-novel/scene-1")
	if code != 0 {
		t.Fatalf("review show: exit code = %d", code)
	}
	if !strings.Contains(res.stdout, "closeout.missing") {
		t.Fatalf("review show gate blockers missing closeout.missing: %s", res.stdout)
	}
}

// 收口后提议未经作者处理时 accept 被拒绝；全部确认处理后通过并推进到 done。
func TestAcceptCloseoutPendingThenPass(t *testing.T) {
	dir := closeoutAcceptFixture(t)
	acceptPayload(t, dir)
	if code, res := runWS(t, dir, "scene", "close-out", "my-novel/scene-1", "--file", "closeout-input.yaml"); code != 0 {
		t.Fatalf("close-out: exit code = %d, result = %+v", code, res)
	}
	code, res := runWS(t, dir, "accept", "my-novel/scene-1")
	if code != 1 || diag(t, res, "closeout.pending-proposals").Code == "" {
		t.Fatalf("pending: exit code = %d, diagnostics = %+v", code, res.Diagnostics)
	}
	for _, args := range [][]string{
		{"state", "confirm", "my-novel", "state-1", "--confirmed-by-author", "--confirmation", "作者确认灯芯易主"},
		{"knowledge", "confirm", "my-novel", "know-1", "--confirmed-by-author", "--confirmation", "作者确认读者知情"},
	} {
		if code, res := runWS(t, dir, args...); code != 0 {
			t.Fatalf("%v: exit code = %d, result = %+v", args, code, res)
		}
	}
	if code, res := runWS(t, dir, "accept", "my-novel/scene-1", "--confirmed-by-author", "--confirmation", "作者：可以"); code != 0 {
		t.Fatalf("accept: exit code = %d, result = %+v", code, res)
	}
	run(t, "review", "record", "my-novel/scene-1", "--file", reviewInput(t, dir), "--dir", dir)
	if code, res := runWS(t, dir, "advance", "my-novel/scene-1"); code != 0 {
		t.Fatalf("advance: exit code = %d, result = %+v", code, res)
	}
	// 已确认的状态变化进入台账（绑定本场景；state at 在该场景自身开始时不含它，先序严格早于才参与重放）。
	code, res = runWS(t, dir, "state", "list", "my-novel", "--all")
	if code != 0 || !strings.Contains(res.stdout, "\"confirmed\"") {
		t.Fatalf("state list: exit code = %d, stdout = %s", code, res.stdout)
	}
}

// 收口后正文被修改：accept 报 closeout.stale；--replace 重收口后通过。
func TestAcceptCloseoutStale(t *testing.T) {
	dir := closeoutAcceptFixture(t)
	acceptPayload(t, dir)
	runWS(t, dir, "scene", "close-out", "my-novel/scene-1", "--file", "closeout-input.yaml")
	runWS(t, dir, "state", "confirm", "my-novel", "state-1", "--confirmed-by-author", "--confirmation", "作者确认")
	runWS(t, dir, "knowledge", "confirm", "my-novel", "know-1", "--confirmed-by-author", "--confirmation", "作者确认")
	writeFile(t, filepath.Join(dir, ".writing-anything", "works", "my-novel", "units", "scene-1", "draft.md"), "修改后的正文。\n")
	if code, res := runWS(t, dir, "accept", "my-novel/scene-1"); code != 1 || diag(t, res, "closeout.stale").Code == "" {
		t.Fatalf("stale: exit code = %d, diagnostics = %+v", code, res.Diagnostics)
	}
	if code, res := runWS(t, dir, "scene", "close-out", "my-novel/scene-1", "--replace", "--file", "closeout-input.yaml"); code != 0 {
		t.Fatalf("re-close: exit code = %d, result = %+v", code, res)
	}
	// 重收口产生新的待确认提议（重放语义容忍重复条目），作者确认后再验收。
	for _, args := range [][]string{
		{"state", "confirm", "my-novel", "state-2", "--confirmed-by-author", "--confirmation", "作者再次确认"},
		{"knowledge", "confirm", "my-novel", "know-2", "--confirmed-by-author", "--confirmation", "作者再次确认"},
	} {
		if code, res := runWS(t, dir, args...); code != 0 {
			t.Fatalf("%v: exit code = %d, result = %+v", args, code, res)
		}
	}
	if code, res := runWS(t, dir, "accept", "my-novel/scene-1", "--confirmed-by-author", "--confirmation", "作者：可以"); code != 0 {
		t.Fatalf("accept after re-close: exit code = %d, result = %+v", code, res)
	}
}

// 收口后契约扩展：accept 报 closeout.missing-state-change。
func TestAcceptCloseoutContractExtended(t *testing.T) {
	dir := closeoutAcceptFixture(t)
	acceptPayload(t, dir)
	runWS(t, dir, "scene", "close-out", "my-novel/scene-1", "--file", "closeout-input.yaml")
	runWS(t, dir, "state", "confirm", "my-novel", "state-1", "--confirmed-by-author", "--confirmation", "作者确认")
	runWS(t, dir, "knowledge", "confirm", "my-novel", "know-1", "--confirmed-by-author", "--confirmation", "作者确认")
	runWS(t, dir, "scene", "contract", "my-novel/scene-1", "sc1", "--state-change", "sc-2=守灯人受伤")
	if code, res := runWS(t, dir, "accept", "my-novel/scene-1"); code != 1 || diag(t, res, "closeout.missing-state-change").Code == "" {
		t.Fatalf("contract extended: exit code = %d, diagnostics = %+v", code, res.Diagnostics)
	}
}

// 只有轻量契约（characters、reveal）的 short-story 场景：验收行为与引入写后收口前一致。
func TestAcceptLightContractUnchanged(t *testing.T) {
	dir := newWorkspace(t, "--gate-level", "fast")
	for _, args := range [][]string{
		{"new", "short-story", "my-story"},
		{"unit", "add", "my-story", "scene", "s1"},
	} {
		if code, stdout, stderr := run(t, append(args, "--dir", dir)...); code != 0 {
			t.Fatalf("%v: exit code = %d; stdout: %s; stderr: %s", args, code, stdout, stderr)
		}
	}
	sceneDir := filepath.Join(dir, ".writing-anything", "works", "my-story", "units", "s1")
	if err := os.MkdirAll(sceneDir, 0o755); err != nil {
		t.Fatal(err)
	}
	writeFile(t, filepath.Join(sceneDir, "brief.yaml"), "schema_version: 1\n")
	run(t, "advance", "my-story/s1", "--dir", dir)
	run(t, "outline", "set", "my-story/s1", "sec1", "--title", "灯下对峙", "--reason", "推进情节", "--dir", dir)
	run(t, "scene", "contract", "my-story/s1", "sec1", "--characters", "守灯人", "--reveal", "精灵的身份", "--dir", dir)
	writeFile(t, filepath.Join(sceneDir, "draft.md"), "正文。\n")
	run(t, "advance", "my-story/s1", "--dir", dir)
	run(t, "advance", "my-story/s1", "--dir", dir)
	code, res := runWS(t, dir, "accept", "my-story/s1", "--confirmed-by-author", "--confirmation", "作者：可以")
	if code != 0 {
		t.Fatalf("accept: exit code = %d, result = %+v", code, res)
	}
	for _, d := range res.Diagnostics {
		if strings.HasPrefix(d.Code, "closeout.") {
			t.Fatalf("unexpected closeout diagnostic: %+v", d)
		}
	}
}

// 长篇端到端：契约 → 正文 → 收口（提交台账）→ 作者确认 → validate 计入 checked → 验收推进到 done。
func TestCloseoutE2E(t *testing.T) {
	dir := closeoutAcceptFixture(t)
	acceptPayload(t, dir)
	if code, res := runWS(t, dir, "scene", "close-out", "my-novel/scene-1", "--file", "closeout-input.yaml"); code != 0 {
		t.Fatalf("close-out: exit code = %d, result = %+v", code, res)
	}
	// 整体校验：closeout.yaml 计入 checked 且零诊断。
	code, res := runWS(t, dir, "validate")
	if code != 0 {
		t.Fatalf("validate: exit code = %d, result = %+v", code, res)
	}
	if !strings.Contains(strings.Join(res.Checked, "\n"), "units/scene-1/closeout.yaml") {
		t.Fatalf("checked = %v", res.Checked)
	}
	// validate <path> 单产物校验。
	if code, res := runWS(t, dir, "validate", ".writing-anything/works/my-novel/units/scene-1/closeout.yaml"); code != 0 {
		t.Fatalf("validate path: exit code = %d, result = %+v", code, res)
	}
	// 无 closeout.yaml 的作品整体校验输出不含 closeout 检查（scene-2 没有 closeout）。
	runWS(t, dir, "state", "confirm", "my-novel", "state-1", "--confirmed-by-author", "--confirmation", "作者确认")
	runWS(t, dir, "knowledge", "confirm", "my-novel", "know-1", "--confirmed-by-author", "--confirmation", "作者确认")
	if code, res := runWS(t, dir, "accept", "my-novel/scene-1", "--confirmed-by-author", "--confirmation", "作者：可以"); code != 0 {
		t.Fatalf("accept: exit code = %d, result = %+v", code, res)
	}
	run(t, "review", "record", "my-novel/scene-1", "--file", reviewInput(t, dir), "--dir", dir)
	if code, res := runWS(t, dir, "advance", "my-novel/scene-1"); code != 0 {
		t.Fatalf("advance: exit code = %d, result = %+v", code, res)
	}
	if code, res := runWS(t, dir, "validate"); code != 0 {
		t.Fatalf("final validate: exit code = %d, result = %+v", code, res)
	}
}
