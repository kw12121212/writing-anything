package cli

import (
	"encoding/json"
	"os"
	"path/filepath"
	"slices"
	"strings"
	"testing"
)

// rippleOut 是修改命令 impact 输出的一项（impact 主规格 impact-auto-mark）。
type rippleOut struct {
	Target string `json:"target"`
	Marked []struct {
		Scene  string `json:"scene"`
		Target string `json:"target"`
		Via    string `json:"via"`
		Status string `json:"status"`
	} `json:"marked"`
	Existing   []struct{ Scene string } `json:"existing"`
	Unaccepted []struct{ Scene string } `json:"unaccepted"`
}

func ripplesOf(t *testing.T, r result) []rippleOut {
	t.Helper()
	var out struct {
		Impact []rippleOut `json:"impact"`
	}
	if err := json.Unmarshal([]byte(r.stdout), &out); err != nil {
		t.Fatal(err)
	}
	if out.Impact == nil {
		t.Fatalf("impact is null or missing: %s", r.stdout)
	}
	return out.Impact
}

func markedScenes(r rippleOut) []string {
	var out []string
	for _, m := range r.Marked {
		out = append(out, m.Scene)
	}
	return out
}

func sceneList(items []struct{ Scene string }) []string {
	var out []string
	for _, it := range items {
		out = append(out, it.Scene)
	}
	return out
}

func openMarks(t *testing.T, dir string) []string {
	t.Helper()
	code, res := runWS(t, dir, "impact", "list", "my-novel")
	if code != 0 {
		t.Fatalf("impact list: %+v", res)
	}
	var out struct {
		Marks []struct{ Scene, Target string } `json:"marks"`
	}
	if err := json.Unmarshal([]byte(res.stdout), &out); err != nil {
		t.Fatal(err)
	}
	var s []string
	for _, m := range out.Marks {
		s = append(s, m.Scene+"←"+m.Target)
	}
	return s
}

func packetCount(changed []string) int {
	n := 0
	for _, c := range changed {
		if strings.Contains(c, "/causality-") {
			n++
		}
	}
	return n
}

// impactAutoWork 构造 novel 作品：设定条目 canon-1（守灯人）出场于 scene-1、scene-2、scene-3；scene-1 与 scene-2
// 已验收（stage 为 done），scene-3 未验收。scene-1 声明事件 ev-1，scene-2 的起因引用 ev-1。
func impactAutoWork(t *testing.T) string {
	t.Helper()
	dir := newNovel(t)
	for _, args := range [][]string{
		{"canon", "add", "my-novel", "--type", "character", "--name", "守灯人", "--text", "五十岁"},
		{"scene", "contract", "my-novel/scene-1", "sc1", "--title", "开场", "--characters", "守灯人", "--event", "ev-1"},
		{"scene", "contract", "my-novel/scene-2", "sc1", "--title", "承接", "--characters", "守灯人", "--cause", "ev-1"},
		{"scene", "contract", "my-novel/scene-3", "sc1", "--title", "转折", "--characters", "守灯人"},
	} {
		if code, stdout, _ := run(t, append(args, "--dir", dir)...); code != 0 {
			t.Fatalf("%v: %s", args, stdout)
		}
	}
	for _, s := range []string{"scene-1", "scene-2", "scene-3"} {
		writeFile(t, filepath.Join(dir, ".writing-anything", "works", "my-novel", "units", s, "draft.md"), "正文。\n")
	}
	setStage(t, dir, "my-novel", "scene-1", "brief", "done")
	setStage(t, dir, "my-novel", "scene-2", "brief", "done")
	return dir
}

// canon set 修改被两个已验收场景与一个未验收场景引用的设定文本：已验收者自动落盘复查标记并生成任务包，未验收者只提示；
// 再次修改去重、不重复生成任务包；非语义字段与只改排版不触发；解除后可再次触发；next 给出复查与解除命令。
func TestCanonSetAutoMark(t *testing.T) {
	dir := impactAutoWork(t)
	code, res := runWS(t, dir, "canon", "set", "my-novel", "canon-1", "--text", "六十岁")
	if code != 0 {
		t.Fatalf("canon set: %+v", res)
	}
	rs := ripplesOf(t, res)
	if len(rs) != 1 || rs[0].Target != "canon-1" ||
		!slices.Equal(markedScenes(rs[0]), []string{"my-novel/scene-1", "my-novel/scene-2"}) ||
		!slices.Equal(sceneList(rs[0].Unaccepted), []string{"my-novel/scene-3"}) || len(rs[0].Existing) != 0 {
		t.Fatalf("impact = %+v", rs)
	}
	if rs[0].Marked[0].Status != "open" || rs[0].Marked[0].Via != "character" {
		t.Fatalf("marked = %+v", rs[0].Marked)
	}
	if !slices.Contains(res.ChangedPaths, ".writing-anything/rechecks/my-novel.yaml") || packetCount(res.ChangedPaths) != 2 {
		t.Fatalf("changedPaths = %v", res.ChangedPaths)
	}
	if got := openMarks(t, dir); !slices.Equal(got, []string{"my-novel/scene-1←canon-1", "my-novel/scene-2←canon-1"}) {
		t.Fatalf("open marks = %v", got)
	}

	// 再次修改：不新增标记与任务包，已标记场景列入 existing。
	rechecks := filepath.Join(dir, ".writing-anything", "rechecks", "my-novel.yaml")
	before := readFile(t, rechecks)
	code, res = runWS(t, dir, "canon", "set", "my-novel", "canon-1", "--text", "七十岁")
	rs = ripplesOf(t, res)
	if code != 0 || len(rs) != 1 || len(rs[0].Marked) != 0 ||
		!slices.Equal(sceneList(rs[0].Existing), []string{"my-novel/scene-1", "my-novel/scene-2"}) {
		t.Fatalf("second set: exit %d, impact = %+v", code, rs)
	}
	if len(res.ChangedPaths) != 1 || !strings.HasSuffix(res.ChangedPaths[0], "canon.yaml") || readFile(t, rechecks) != before {
		t.Fatalf("second set changedPaths = %v", res.ChangedPaths)
	}

	// 手工只改注释，再以只差首尾与连续空白的文本修改：不触发。
	canonFile := filepath.Join(dir, ".writing-anything", "works", "my-novel", "canon.yaml")
	writeFile(t, canonFile, "# 作者备注：守灯人的年龄待定\n"+readFile(t, canonFile))
	code, res = runWS(t, dir, "canon", "set", "my-novel", "canon-1", "--text", "  七十岁 ")
	if rs = ripplesOf(t, res); code != 0 || len(res.ChangedPaths) == 0 || len(rs) != 0 || readFile(t, rechecks) != before {
		t.Fatalf("whitespace-only set: exit %d, changed %v, impact = %+v", code, res.ChangedPaths, rs)
	}

	// next：done 场景因 open 复查标记列出，commands 含复查与解除命令，action 给出作者确认路径；不进入 blockers。
	code, res = runWS(t, dir, "next")
	var nx struct {
		Steps []struct {
			Unit     string        `json:"unit"`
			Action   string        `json:"action"`
			Blockers []blockerView `json:"blockers"`
			Commands []string      `json:"commands"`
		} `json:"steps"`
	}
	if err := json.Unmarshal([]byte(res.stdout), &nx); err != nil || code != 0 {
		t.Fatalf("next: %v %+v", err, res)
	}
	found := false
	for _, s := range nx.Steps {
		if s.Unit != "my-novel/scene-1" {
			continue
		}
		found = true
		if len(s.Blockers) != 0 ||
			!slices.Contains(s.Commands, "wa-cli review plan my-novel/scene-1 --dimension causality --dir "+dir+" --json") ||
			!slices.Contains(s.Commands, "wa-cli impact clear my-novel/scene-1 canon-1 --dir "+dir+" --json") ||
			!strings.Contains(s.Action, "wa-cli impact clear my-novel/scene-1 canon-1 --confirmed-by-author --confirmation") {
			t.Fatalf("next step = %+v", s)
		}
		for _, c := range s.Commands {
			if strings.ContainsAny(c, "<>") {
				t.Fatalf("command %q contains a placeholder", c)
			}
		}
	}
	if !found {
		t.Fatalf("next does not list done scene with open marks: %s", res.stdout)
	}
	if _, stdout, _ := run(t, "next", "my-novel/scene-2", "--dir", dir); !strings.Contains(stdout, "--confirmed-by-author") || !strings.Contains(stdout, "下一步：wa-cli impact clear my-novel/scene-2 canon-1") {
		t.Fatalf("next text = %s", stdout)
	}

	// 未验收场景经手动 impact mark 有 open 标记时：复查命令追加在原有命令之后，仍不进入 blockers。
	if code, res := runWS(t, dir, "impact", "mark", "my-novel/canon-1"); code != 0 {
		t.Fatalf("impact mark: %+v", res)
	}
	code, res = runWS(t, dir, "next", "my-novel/scene-3")
	var one struct {
		Steps []struct {
			Blockers []blockerView `json:"blockers"`
			Commands []string      `json:"commands"`
		} `json:"steps"`
	}
	if err := json.Unmarshal([]byte(res.stdout), &one); err != nil || code != 0 || len(one.Steps) != 1 {
		t.Fatalf("next scene-3: %v %+v", err, res)
	}
	cmds := one.Steps[0].Commands
	plan := slices.Index(cmds, "wa-cli review plan my-novel/scene-3 --dimension causality --dir "+dir+" --json")
	if plan <= 0 || plan+2 != len(cmds)-1 || cmds[plan+1] != "wa-cli impact clear my-novel/scene-3 canon-1 --dir "+dir+" --json" ||
		cmds[len(cmds)-1] != "wa-cli help impact clear --json" || len(one.Steps[0].Blockers) == 0 {
		t.Fatalf("next scene-3 commands = %v", cmds)
	}
	if code, res := runWS(t, dir, "impact", "clear", "my-novel/scene-3", "canon-1", "--confirmed-by-author", "--confirmation", "作者确认无影响"); code != 0 {
		t.Fatalf("impact clear scene-3: %+v", res)
	}

	// 解除 scene-1 后再次修改：scene-1 重新落盘，scene-2 仍为 existing；只为 scene-1 生成任务包。
	if code, res := runWS(t, dir, "impact", "clear", "my-novel/scene-1", "canon-1", "--confirmed-by-author", "--confirmation", "作者确认无影响"); code != 0 {
		t.Fatalf("impact clear: %+v", res)
	}
	code, res = runWS(t, dir, "canon", "set", "my-novel", "canon-1", "--text", "八十岁")
	rs = ripplesOf(t, res)
	if code != 0 || len(rs) != 1 || !slices.Equal(markedScenes(rs[0]), []string{"my-novel/scene-1"}) ||
		!slices.Equal(sceneList(rs[0].Existing), []string{"my-novel/scene-2"}) || packetCount(res.ChangedPaths) != 1 {
		t.Fatalf("after clear: exit %d, impact = %+v, changed = %v", code, rs, res.ChangedPaths)
	}

	// 非语义字段（protection）不触发。
	code, res = runWS(t, dir, "canon", "set", "my-novel", "canon-1", "--pin")
	if rs = ripplesOf(t, res); code != 0 || len(res.ChangedPaths) != 1 || len(rs) != 0 {
		t.Fatalf("pin: exit %d, changed %v, impact = %+v", code, res.ChangedPaths, rs)
	}

	// 文本输出逐行列出。
	dir2 := impactAutoWork(t)
	_, stdout, _ := run(t, "canon", "set", "my-novel", "canon-1", "--text", "六十岁", "--dir", dir2)
	if !strings.Contains(stdout, "已验收场景 my-novel/scene-1 受目标 canon-1 影响，已落盘复查标记") ||
		!strings.Contains(stdout, "未验收场景 my-novel/scene-3 受目标 canon-1 影响（只提示，未标记）") {
		t.Fatalf("text = %s", stdout)
	}
	_, stdout, _ = run(t, "canon", "set", "my-novel", "canon-1", "--text", "七十岁", "--dir", dir2)
	if !strings.Contains(stdout, "已有针对 canon-1 的 open 复查标记，未重复标记") {
		t.Fatalf("text = %s", stdout)
	}
}

// 已有任何 open 标记的场景不重复生成任务包：scene-2 已有针对 scene-1 的标记，canon-1 修改只追加标记不再生成包。
func TestAutoMarkSkipsPacketsForOpenScene(t *testing.T) {
	dir := impactAutoWork(t)
	if code, res := runWS(t, dir, "impact", "mark", "my-novel/scene-1"); code != 0 {
		t.Fatalf("impact mark: %+v", res)
	}
	code, res := runWS(t, dir, "canon", "set", "my-novel", "canon-1", "--text", "六十岁")
	rs := ripplesOf(t, res)
	if code != 0 || len(rs) != 1 || !slices.Equal(markedScenes(rs[0]), []string{"my-novel/scene-1", "my-novel/scene-2"}) {
		t.Fatalf("impact = %+v", rs)
	}
	// 只有 scene-1 生成任务包（scene-2 已有 open 标记）。
	var pk []string
	for _, c := range res.ChangedPaths {
		if strings.Contains(c, "/causality-") {
			pk = append(pk, c)
		}
	}
	if len(pk) != 1 || !strings.Contains(readFile(t, filepath.Join(dir, filepath.FromSlash(pk[0]))), "my-novel/scene-1") {
		t.Fatalf("packets = %v", pk)
	}
}

// threads set、state set、knowledge set 与 scene contract 的语义修改同样触发；非语义修改不触发。
func TestOtherSetCommandsAutoMark(t *testing.T) {
	dir := impactAutoWork(t)
	// scene contract：修改 scene-1 的状态变化（语义）→ 受影响的 scene-2（起因引用 ev-1）已验收，落盘。
	code, res := runWS(t, dir, "scene", "contract", "my-novel/scene-1", "sc1", "--state-change", "sc-1=守灯人熄灯")
	rs := ripplesOf(t, res)
	if code != 0 || len(rs) != 1 || rs[0].Target != "my-novel/scene-1" || !slices.Equal(markedScenes(rs[0]), []string{"my-novel/scene-2"}) {
		t.Fatalf("scene contract: exit %d, impact = %+v", code, rs)
	}
	// 只改标题（非语义）不触发。
	code, res = runWS(t, dir, "scene", "contract", "my-novel/scene-1", "sc1", "--title", "新标题")
	if rs = ripplesOf(t, res); code != 0 || len(res.ChangedPaths) == 0 || len(rs) != 0 {
		t.Fatalf("title only: exit %d, impact = %+v", code, rs)
	}

	// threads：埋设于 scene-1、计划回收于 scene-2（已验收，大纲未列该伏笔）的伏笔；修改内容 → scene-2 落盘
	// （planted 场景本身不计入）。
	if code, stdout, _ := run(t, "threads", "add", "my-novel", "--id", "th-1", "--text", "灯芯", "--plant-in", "my-novel/scene-1", "--recover-at", "my-novel/scene-2", "--dir", dir); code != 0 {
		t.Fatalf("threads add: %s", stdout)
	}
	code, res = runWS(t, dir, "threads", "set", "my-novel", "th-1", "--text", "熄灭的灯芯")
	rs = ripplesOf(t, res)
	if code != 0 || len(rs) != 1 || rs[0].Target != "th-1" || !slices.Equal(markedScenes(rs[0]), []string{"my-novel/scene-2"}) || len(rs[0].Unaccepted) != 0 {
		t.Fatalf("threads set: exit %d, impact = %+v", code, rs)
	}
	// 只改排版（首尾空白）不触发。
	code, res = runWS(t, dir, "threads", "set", "my-novel", "th-1", "--text", " 熄灭的灯芯 ")
	if rs = ripplesOf(t, res); code != 0 || len(res.ChangedPaths) == 0 || len(rs) != 0 {
		t.Fatalf("threads whitespace: exit %d, impact = %+v", code, rs)
	}
	// 把计划回收位置移到 scene-3：修改后的台账不再指向 scene-2，但修改前的引用图结果并入，scene-2 仍计入。
	code, res = runWS(t, dir, "threads", "set", "my-novel", "th-1", "--recover-at", "my-novel/scene-3")
	rs = ripplesOf(t, res)
	if code != 0 || len(rs) != 1 || !slices.Equal(sceneList(rs[0].Existing), []string{"my-novel/scene-2"}) ||
		!slices.Equal(sceneList(rs[0].Unaccepted), []string{"my-novel/scene-3"}) {
		t.Fatalf("threads recover-at move: exit %d, impact = %+v", code, rs)
	}

	// state：绑定 scene-2 的状态变更；修改状态描述 → 只计不早于 scene-2 的场景：scene-2（已验收）落盘、scene-3 提示，scene-1 不计。
	if code, stdout, _ := run(t, "state", "propose", "my-novel", "--scene", "my-novel/scene-2", "--entry", "canon-1", "--state", "受伤", "--id", "state-1", "--dir", dir); code != 0 {
		t.Fatalf("state propose: %s", stdout)
	}
	code, res = runWS(t, dir, "state", "set", "my-novel", "state-1", "--state", "重伤")
	rs = ripplesOf(t, res)
	if code != 0 || len(rs) != 1 || rs[0].Target != "canon-1" {
		t.Fatalf("state set: exit %d, impact = %+v; %s", code, rs, res.stdout)
	}
	all := append(append(markedScenes(rs[0]), sceneList(rs[0].Existing)...), sceneList(rs[0].Unaccepted)...)
	if slices.Contains(all, "my-novel/scene-1") || !slices.Contains(all, "my-novel/scene-2") || !slices.Contains(all, "my-novel/scene-3") {
		t.Fatalf("state set scenes = %v", all)
	}
	// 把绑定场景前移到 scene-1：场景下限取新旧绑定中先序较早者，scene-1 计入并落盘。
	code, res = runWS(t, dir, "state", "set", "my-novel", "state-1", "--scene", "my-novel/scene-1")
	rs = ripplesOf(t, res)
	if code != 0 || len(rs) != 1 || !slices.Contains(markedScenes(rs[0]), "my-novel/scene-1") {
		t.Fatalf("state scene move: exit %d, impact = %+v", code, rs)
	}

	// knowledge：读者于 scene-3 获知 canon-1；修改揭示词 → 只计不早于 scene-3：scene-3 只提示。
	if code, stdout, _ := run(t, "knowledge", "propose", "my-novel", "--scene", "my-novel/scene-3", "--entry", "canon-1", "--character", "reader", "--id", "know-1", "--dir", dir); code != 0 {
		t.Fatalf("knowledge propose: %s", stdout)
	}
	code, res = runWS(t, dir, "knowledge", "set", "my-novel", "know-1", "--terms", "守灯人")
	rs = ripplesOf(t, res)
	if code != 0 || len(rs) != 1 || len(rs[0].Marked) != 0 || !slices.Equal(sceneList(rs[0].Unaccepted), []string{"my-novel/scene-3"}) {
		t.Fatalf("knowledge set: exit %d, impact = %+v; %s", code, rs, res.stdout)
	}
	// 改绑定条目到 canon-2 并前移到 scene-2：新旧两个条目各一项，下限为 scene-2。
	if code, stdout, _ := run(t, "canon", "add", "my-novel", "--type", "character", "--name", "访客", "--text", "外乡人", "--dir", dir); code != 0 {
		t.Fatalf("canon add: %s", stdout)
	}
	code, res = runWS(t, dir, "knowledge", "set", "my-novel", "know-1", "--entry", "canon-2", "--scene", "my-novel/scene-2")
	rs = ripplesOf(t, res)
	if code != 0 || len(rs) != 2 || rs[0].Target != "canon-1" || rs[1].Target != "canon-2" ||
		!slices.Equal(markedScenes(rs[1]), []string{"my-novel/scene-2"}) {
		t.Fatalf("knowledge entry move: exit %d, impact = %+v", code, rs)
	}
	for _, r := range rs {
		all := append(append(markedScenes(r), sceneList(r.Existing)...), sceneList(r.Unaccepted)...)
		if slices.Contains(all, "my-novel/scene-1") {
			t.Fatalf("knowledge from filter: %+v", r)
		}
	}
}

// canon set 改名：修改后的称谓不再与场景契约的出场人物对上，但修改前引用该条目的已验收场景仍自动标记。
func TestCanonRenameAutoMark(t *testing.T) {
	dir := impactAutoWork(t)
	code, res := runWS(t, dir, "canon", "set", "my-novel", "canon-1", "--name", "老守灯人")
	rs := ripplesOf(t, res)
	if code != 0 || len(rs) != 1 || !slices.Equal(markedScenes(rs[0]), []string{"my-novel/scene-1", "my-novel/scene-2"}) ||
		!slices.Equal(sceneList(rs[0].Unaccepted), []string{"my-novel/scene-3"}) {
		t.Fatalf("rename: exit %d, impact = %+v", code, rs)
	}
}

// 自动传播失败（复查标记文件不通过校验）时修改照常写入，报告 impact.auto-mark-failed 的 warning，退出码为 0。
func TestAutoMarkFailureWarns(t *testing.T) {
	dir := impactAutoWork(t)
	rechecks := filepath.Join(dir, ".writing-anything", "rechecks")
	if err := os.MkdirAll(rechecks, 0o755); err != nil {
		t.Fatal(err)
	}
	writeFile(t, filepath.Join(rechecks, "my-novel.yaml"), "schema_version: 1\nscenes: nope\n")
	code, res := runWS(t, dir, "canon", "set", "my-novel", "canon-1", "--text", "六十岁")
	d := diag(t, res, "impact.auto-mark-failed")
	if code != 0 || d.Severity != "warning" || !hasNext(d, "wa-cli impact mark my-novel/canon-1") ||
		len(res.ChangedPaths) != 1 || len(ripplesOf(t, res)) != 0 {
		t.Fatalf("exit %d, result = %+v", code, res)
	}
}
