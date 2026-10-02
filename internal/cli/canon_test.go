package cli

import (
	"encoding/json"
	"os"
	"path/filepath"
	"slices"
	"strings"
	"testing"
)

// canonResult 是 canon 命令 --json 输出中测试关心的部分。
type canonResult struct {
	result
	Canon *struct {
		Work       string   `json:"work"`
		ID         string   `json:"id"`
		Type       string   `json:"type"`
		Name       *string  `json:"name"`
		Text       string   `json:"text"`
		Sources    []string `json:"sources"`
		Protection string   `json:"protection"`
		Members    []string `json:"members"`
		Decision   *string  `json:"decision"`
	} `json:"canon"`
	Entries []struct {
		ID         string  `json:"id"`
		Type       string  `json:"type"`
		Name       *string `json:"name"`
		Protection string  `json:"protection"`
	} `json:"entries"`
	Removed *string `json:"removed"`
}

func runCanon(t *testing.T, dir string, args ...string) (int, canonResult) {
	t.Helper()
	code, res := runWS(t, dir, args...)
	var out canonResult
	if err := json.Unmarshal([]byte(res.stdout), &out); err != nil {
		t.Fatal(err)
	}
	out.result = res
	var v any
	json.Unmarshal([]byte(res.stdout), &v)
	if m, ok := v.(map[string]any); ok {
		if arr, ok := m["entries"].([]any); ok {
			out.Entries = nil
			for _, e := range arr {
				b, _ := json.Marshal(e)
				var item struct {
					ID         string  `json:"id"`
					Type       string  `json:"type"`
					Name       *string `json:"name"`
					Protection string  `json:"protection"`
				}
				json.Unmarshal(b, &item)
				out.Entries = append(out.Entries, item)
			}
		}
	}
	return code, out
}

func canonPath(dir string) string {
	return filepath.Join(dir, ".writing-anything", "works", "my-post", "canon.yaml")
}

func TestCanonAddAndList(t *testing.T) {
	dir := newWork(t)
	writeFile(t, filepath.Join(dir, "idea.md"), "作者笔记")
	run(t, "source", "add", "--type", "author-note", "--file", "idea.md", "--id", "note", "--dir", dir)

	// 人物条目（canon-add/add-character）。
	code, res := runCanon(t, dir, "canon", "add", "my-post", "--type", "character", "--name", "守灯人", "--text", "五十岁，左手有旧伤", "--sources", "note")
	if code != 0 || res.Canon == nil {
		t.Fatalf("code = %d, res = %+v", code, res)
	}
	c := res.Canon
	if c.Work != "my-post" || c.ID != "canon-1" || c.Type != "character" || c.Name == nil || *c.Name != "守灯人" ||
		!slices.Equal(c.Sources, []string{"note"}) || c.Protection != "normal" {
		t.Fatalf("canon = %+v", c)
	}
	if c.Members == nil || len(c.Members) != 0 {
		t.Fatalf("members = %#v, want empty array (not null)", c.Members)
	}
	if !slices.Equal(res.ChangedPaths, []string{".writing-anything/works/my-post/canon.yaml"}) {
		t.Fatalf("changedPaths = %v", res.ChangedPaths)
	}
	// 人物关系引用同文件的人物条目（canon-add/add-relationship）。
	if code, res = runCanon(t, dir, "canon", "add", "my-post", "--type", "character", "--name", "精灵", "--text", "夜行"); code != 0 {
		t.Fatal(code)
	}
	if code, res = runCanon(t, dir, "canon", "add", "my-post", "--type", "relationship", "--text", "旧识", "--members", "canon-1,canon-2"); code != 0 || res.Canon.Members == nil {
		t.Fatalf("code = %d, canon = %+v", code, res.Canon)
	}
	// 列出（canon-list/list-entries）。
	code, res = runCanon(t, dir, "canon", "list", "my-post")
	if code != 0 || len(res.Entries) != 3 || res.Entries[0].ID != "canon-1" {
		t.Fatalf("entries = %+v", res.Entries)
	}
	// 没有设定集的作品列出为空（canon-list/list-empty）。
	empty := newWork(t)
	if code, res = runCanon(t, empty, "canon", "list", "my-post"); code != 0 || len(res.Entries) != 0 {
		t.Fatalf("entries = %+v, code = %d", res.Entries, code)
	}
}

func TestCanonAddRejects(t *testing.T) {
	dir := newWork(t)
	// 素材未登记（canon-add/unknown-source）：next 含 help 与 source list。
	code, res := runCanon(t, dir, "canon", "add", "my-post", "--type", "character", "--name", "主角", "--text", "描述", "--sources", "nope")
	if code != 1 {
		t.Fatalf("code = %d", code)
	}
	d := diag(t, res.result, "canon.unknown-source")
	if d.Field != "sources[0]" || d.Path != "" {
		t.Fatalf("diagnostic = %+v", d)
	}
	if !slices.Contains(d.Next, "wa-cli help canon add --json") || !slices.Contains(d.Next, shellCommand("wa-cli", "source", "list", "--dir", dir, "--json")) {
		t.Fatalf("next = %v", d.Next)
	}
	if _, err := os.Stat(canonPath(dir)); !os.IsNotExist(err) {
		t.Error("canon.yaml created on rejected add")
	}
	// members 引用不存在的人物条目（canon-add/unknown-member）。
	if code, res = runCanon(t, dir, "canon", "add", "my-post", "--type", "character", "--name", "甲", "--text", "x"); code != 0 {
		t.Fatal(code)
	}
	if code, res = runCanon(t, dir, "canon", "add", "my-post", "--type", "relationship", "--text", "对立", "--members", "canon-1,canon-9"); code != 1 {
		t.Fatalf("code = %d", code)
	}
	if d = diag(t, res.result, "canon.unknown-character"); d.Field != "members[1]" {
		t.Fatalf("diagnostic = %+v", d)
	}
	// id 重复（canon-add/duplicate-id）。
	if code, res = runCanon(t, dir, "canon", "add", "my-post", "--type", "world-rule", "--text", "规则", "--id", "canon-1"); code != 1 {
		t.Fatalf("code = %d", code)
	}
	if d = diag(t, res.result, "artifact.duplicate-id"); d.Field != "id" {
		t.Fatalf("diagnostic = %+v", d)
	}
	// 子单元引用不可用：canon 是作品级产物。
	run(t, "unit", "add", "my-post", "section", "intro", "--dir", dir)
	if code, res = runCanon(t, dir, "canon", "add", "my-post/intro", "--type", "world-rule", "--text", "x"); code != 1 {
		t.Fatalf("code = %d", code)
	}
	if d := diag(t, res.result, "work.not-found"); !strings.Contains(d.Message, "作品级产物") {
		t.Fatalf("diagnostic = %+v", d)
	}
}

func TestCanonSetRemove(t *testing.T) {
	dir := newWork(t)
	run(t, "canon", "add", "my-post", "--type", "character", "--name", "守灯人", "--text", "五十岁", "--dir", dir)
	// 修改文本并保留注释（canon-edit/set-text）。
	rel := canonPath(dir)
	data, _ := os.ReadFile(rel)
	os.WriteFile(rel, append([]byte("# 作者的注释\n"), data...), 0o644)
	code, res := runCanon(t, dir, "canon", "set", "my-post", "canon-1", "--text", "六十岁")
	if code != 0 || res.Canon == nil || res.Canon.Text != "六十岁" {
		t.Fatalf("code = %d, canon = %+v", code, res.Canon)
	}
	if b, _ := os.ReadFile(rel); !strings.Contains(string(b), "# 作者的注释") {
		t.Errorf("canon.yaml lost comments")
	}
	// 取值未变（canon-edit/set-unchanged）。
	if code, res = runCanon(t, dir, "canon", "set", "my-post", "canon-1", "--text", "六十岁"); code != 0 || len(res.ChangedPaths) != 0 {
		t.Fatalf("code = %d, changed = %v", code, res.ChangedPaths)
	}
	// 不存在的条目（canon-edit/entry-not-found）：next 指向 canon list。
	if code, res = runCanon(t, dir, "canon", "set", "my-post", "canon-9", "--text", "x"); code != 1 {
		t.Fatalf("code = %d", code)
	}
	if d := diag(t, res.result, "canon.not-found"); !slices.Contains(d.Next, shellCommand("wa-cli", "canon", "list", "my-post", "--dir", dir, "--json")) {
		t.Fatalf("next = %v", d.Next)
	}
	// 删除（canon-edit/remove-entry）。
	if code, res = runCanon(t, dir, "canon", "remove", "my-post", "canon-1"); code != 0 || res.Removed == nil || *res.Removed != "canon-1" {
		t.Fatalf("code = %d, removed = %+v", code, res.Removed)
	}
	// --pin 与 --unpin 同时给出。
	if code, res = runCanon(t, dir, "canon", "add", "my-post", "--type", "world-rule", "--text", "规则"); code != 0 {
		t.Fatal(code)
	}
	if code, res = runCanon(t, dir, "canon", "set", "my-post", "canon-1", "--pin", "--unpin"); code != 1 {
		t.Fatalf("code = %d", code)
	}
	if d := diag(t, res.result, "input.invalid-value"); d.Field != "pin" {
		t.Fatalf("diagnostic = %+v", d)
	}
	// 普通条目不接受 --decision。
	if code, res = runCanon(t, dir, "canon", "set", "my-post", "canon-1", "--text", "新", "--decision", "dec-1"); code != 1 {
		t.Fatalf("code = %d", code)
	}
	if d := diag(t, res.result, "input.invalid-value"); d.Field != "decision" || !strings.Contains(d.Message, "不是作者钦定") {
		t.Fatalf("diagnostic = %+v", d)
	}
	if code, res = runCanon(t, dir, "canon", "remove", "my-post", "canon-1", "--decision", "dec-1"); code != 1 {
		t.Fatalf("code = %d", code)
	}
	diag(t, res.result, "input.invalid-value")
}

func TestCanonListHumanOutput(t *testing.T) {
	dir := newWork(t)
	run(t, "canon", "add", "my-post", "--type", "world-rule", "--text", "灯油只能燃烧到黎明", "--pin", "--dir", dir)
	code, stdout, _ := run(t, "canon", "list", "my-post", "--dir", dir)
	if code != 0 {
		t.Fatal(code)
	}
	if !strings.Contains(stdout, "canon-1（world-rule）［作者钦定］：灯油只能燃烧到黎明") {
		t.Fatalf("human output = %q", stdout)
	}
}

func TestCanonPinnedFlow(t *testing.T) {
	dir := newWork(t)
	writeFile(t, filepath.Join(dir, "idea.md"), "作者设定")
	run(t, "source", "add", "--type", "author-note", "--file", "idea.md", "--id", "note", "--dir", dir)
	run(t, "canon", "add", "my-post", "--type", "character", "--name", "守灯人", "--text", "五十岁", "--sources", "note", "--pin", "--dir", dir)

	// 拒绝修改并自动生成阻塞决策（canon-protection/refuse-modify-pinned）。
	code, res := runCanon(t, dir, "canon", "set", "my-post", "canon-1", "--text", "新描述")
	if code != 1 {
		t.Fatalf("code = %d", code)
	}
	d := diag(t, res.result, "canon.author-pinned")
	if d.Path != ".writing-anything/works/my-post/canon.yaml" || !strings.Contains(d.Message, "作者钦定") {
		t.Fatalf("diagnostic = %+v", d)
	}
	if !slices.Contains(d.Next, shellCommand("wa-cli", "decisions", "--dir", dir, "--json")) {
		t.Fatalf("next = %v", d.Next)
	}
	if !slices.Equal(res.ChangedPaths, []string{".writing-anything/decisions.yaml"}) {
		t.Fatalf("changedPaths = %v", res.ChangedPaths)
	}
	// 决策内容与阻塞推进（canon-protection/blocks-advance）。
	code, ds := runJSON(t, "decisions", "--dir", dir, "--json")
	if code != 0 {
		t.Fatal(code)
	}
	blocking, _ := ds["blocking"].([]any)
	if len(blocking) != 1 {
		t.Fatalf("blocking = %v", ds["blocking"])
	}
	dec := blocking[0].(map[string]any)
	if dec["id"] != "dec-1" || dec["question"] != "是否允许修改作者钦定的设定条目 canon-1（守灯人）？" ||
		dec["blocking"] != true || dec["status"] != "open" {
		t.Fatalf("decision = %v", dec)
	}
	options := dec["options"].([]any)
	if len(options) != 2 || options[0].(map[string]any)["id"] != "a" || options[0].(map[string]any)["text"] != "允许" ||
		options[1].(map[string]any)["id"] != "b" || options[1].(map[string]any)["text"] != "保持不变" {
		t.Fatalf("options = %v", options)
	}
	if dec["recommendation"] != "b" {
		t.Fatalf("recommendation = %v", dec["recommendation"])
	}
	// 重复拒绝幂等（canon-protection/idempotent-decision）。
	if code, res = runCanon(t, dir, "canon", "set", "my-post", "canon-1", "--text", "新描述"); code != 1 || len(res.ChangedPaths) != 0 {
		t.Fatalf("code = %d, changed = %v", code, res.ChangedPaths)
	}
	// 作者决定允许后携带 --decision 重试（canon-protection/retry-after-decide）。
	if code, _, _ = run(t, "decide", "dec-1", "--option", "a", "--confirmation", "作者同意这次修改", "--dir", dir); code != 0 {
		t.Fatal(code)
	}
	if code, res = runCanon(t, dir, "canon", "set", "my-post", "canon-1", "--text", "新描述", "--decision", "dec-1"); code != 0 || res.Canon == nil {
		t.Fatalf("code = %d, canon = %+v, diags = %+v", code, res.Canon, res.Diagnostics)
	}
	if res.Canon.Text != "新描述" || res.Canon.Decision == nil || *res.Canon.Decision != "dec-1" || res.Canon.Protection != "author-pinned" {
		t.Fatalf("canon = %+v", res.Canon)
	}
	// 决定之后未携带 --decision 的再次修改需要新的授权：生成新的阻塞决策。
	if code, res = runCanon(t, dir, "canon", "set", "my-post", "canon-1", "--text", "再改"); code != 1 ||
		!slices.Equal(res.ChangedPaths, []string{".writing-anything/decisions.yaml"}) {
		t.Fatalf("code = %d, changed = %v", code, res.ChangedPaths)
	}
}

func TestCanonPinnedKeepChoice(t *testing.T) {
	dir := newWork(t)
	run(t, "canon", "add", "my-post", "--type", "world-rule", "--text", "灯油只能燃烧到黎明", "--pin", "--dir", dir)
	// 无 name 的删除问题文本省略括号（canon-protection/refuse-remove-pinned）。
	code, res := runCanon(t, dir, "canon", "remove", "my-post", "canon-1")
	if code != 1 {
		t.Fatalf("code = %d", code)
	}
	diag(t, res.result, "canon.author-pinned")
	if b, _ := os.ReadFile(filepath.Join(dir, ".writing-anything", "decisions.yaml")); !strings.Contains(string(b), "是否允许删除作者钦定的设定条目 canon-1？") {
		t.Errorf("decisions.yaml:\n%s", b)
	}
	run(t, "decide", "dec-1", "--option", "b", "--confirmation", "作者决定不改", "--dir", dir)
	// 作者选择保持不变：携带该决策重试仍拒绝，且不重复追加决策（canon-protection/keep-choice-refuses）。
	if code, res = runCanon(t, dir, "canon", "remove", "my-post", "canon-1", "--decision", "dec-1"); code != 1 || len(res.ChangedPaths) != 0 {
		t.Fatalf("code = %d, changed = %v", code, res.ChangedPaths)
	}
	d := diag(t, res.result, "canon.author-pinned")
	if !strings.Contains(d.Message, "保持不变") {
		t.Fatalf("diagnostic = %+v", d)
	}
	if _, err := os.Stat(canonPath(dir)); err != nil {
		t.Error("canon.yaml removed after keep choice")
	}
}

func TestCanonValidate(t *testing.T) {
	dir := newWork(t)
	run(t, "canon", "add", "my-post", "--type", "character", "--name", "守灯人", "--text", "x", "--dir", dir)
	// 单文件校验（canon-validation/single-file-validate）。
	code, res := runWS(t, dir, "validate", ".writing-anything/works/my-post/canon.yaml")
	if code != 0 || !slices.Equal(res.Checked, []string{".writing-anything/works/my-post/canon.yaml"}) {
		t.Fatalf("code = %d, checked = %v, diags = %+v", code, res.Checked, res.Diagnostics)
	}
	// 整体校验计入 checked。
	if code, res = runWS(t, dir, "validate"); code != 0 || !slices.Contains(res.Checked, ".writing-anything/works/my-post/canon.yaml") {
		t.Fatalf("code = %d, checked = %v", code, res.Checked)
	}
	// 子单元目录中的 canon.yaml 不是已知产物类型（canon-validation/child-dir-not-recognized）。
	child := filepath.Join(dir, ".writing-anything", "works", "my-post", "units", "s1")
	os.MkdirAll(child, 0o755)
	writeFile(t, filepath.Join(child, "canon.yaml"), "schema_version: 1\nentries: []\n")
	if code, res = runWS(t, dir, "validate", ".writing-anything/works/my-post/units/s1/canon.yaml"); code != 1 {
		t.Fatalf("code = %d", code)
	}
	diag(t, res, "artifact.unknown-kind")
	// 条目 id 重复：整体校验报告（canon-validation/duplicate-entry）。
	rel := canonPath(dir)
	b, _ := os.ReadFile(rel)
	os.WriteFile(rel, append(b, []byte("  - {id: canon-1, type: location, name: 灯塔, text: y, sources: [], protection: normal}\n")...), 0o644)
	if code, res = runWS(t, dir, "validate"); code != 1 {
		t.Fatalf("code = %d", code)
	}
	if d := diag(t, res, "artifact.duplicate-id"); d.Field != "entries[1].id" || d.Path != ".writing-anything/works/my-post/canon.yaml" {
		t.Fatalf("diagnostic = %+v", d)
	}
}
