package cli

import (
	"encoding/json"
	"os"
	"path/filepath"
	"strings"
	"testing"
)

// learnWork 返回登记了 past-article 素材 a1 的工作区。
func learnWork(t *testing.T) string {
	t.Helper()
	dir := newWorkspace(t)
	writeFile(t, filepath.Join(dir, "a1.md"), "今天下雨。我们出门了吗？没有！")
	if code, stdout, _ := run(t, "source", "add", "--type", "past-article", "--id", "a1", "--file", "a1.md", "--dir", dir); code != 0 {
		t.Fatalf("source add: %s", stdout)
	}
	return dir
}

type proposalOut struct {
	result
	Proposal *proposalView `json:"proposal"`
}

func runProposal(t *testing.T, args ...string) (int, proposalOut) {
	t.Helper()
	code, stdout, _ := run(t, append([]string{"pack"}, append(args, "--json")...)...)
	var out proposalOut
	if err := json.Unmarshal([]byte(stdout), &out); err != nil {
		t.Fatalf("%v: %v; %s", args, err, stdout)
	}
	return code, out
}

func TestPackLearnConfirmReject(t *testing.T) {
	dir := learnWork(t)
	code, out := runProposal(t, "learn", "me", "--pack", "mine", "--source", "a1", "--style", "多用短句", "--avoid", "赋能", "--dir", dir)
	p := out.Proposal
	if code != 0 || p == nil || p.Path != ".writing-anything/proposals/me.yaml" || p.Stats.Chars != 15 || p.Stats.Sentences.Mean != 5 || len(p.Items) != 3 ||
		p.Items[1].Text != "多用短句" || p.Items[2].Term != "赋能" || p.Items[0].Confirmation != nil || len(out.ChangedPaths) != 1 {
		t.Fatalf("learn: %d %+v", code, out)
	}
	if code, stdout, _ := run(t, "pack", "learn", "me", "--dir", dir); code != 0 || !strings.Contains(stdout, "平均句长 5") || !strings.Contains(stdout, "- style-1（style）多用短句：proposed") {
		t.Fatalf("text: %q", stdout)
	}
	// 未确认的提议不影响规则。
	if code, res := runJSON(t, "config", "resolved", "--dir", dir, "--json"); code != 0 || strings.Contains(mustJSON(t, res), "多用短句") {
		t.Fatalf("resolved before confirm: %v", res)
	}
	code, out = runProposal(t, "confirm", "me", "--item", "baseline", "--item", "style-1", "--confirmation", "作者：就按这个来", "--dir", dir)
	if code != 0 || out.Proposal.Items[1].Status != "confirmed" || *out.Proposal.Items[1].Confirmation != "作者：就按这个来" ||
		strings.Join(out.ChangedPaths, ",") != ".writing-anything/packs/mine.yaml,.writing-anything/proposals/me.yaml" {
		t.Fatalf("confirm: %d %+v", code, out)
	}
	run(t, "config", "set", "packs", "mine", "--dir", dir)
	code, res := runJSON(t, "config", "resolved", "--dir", dir, "--json")
	resolved := mustJSON(t, res["resolved"])
	if code != 0 || !strings.Contains(resolved, `"styleProfile":{"source":"pack:mine","value":{"chars":15`) || !strings.Contains(resolved, "多用短句") {
		t.Fatalf("resolved after confirm: %s", resolved)
	}
	if code, out := runProposal(t, "reject", "me", "--item", "avoid-1", "--dir", dir); code != 0 || out.Proposal.Items[2].Status != "rejected" || out.Proposal.Items[2].DecidedAt == nil {
		t.Fatalf("reject: %d %+v", code, out)
	}
	code, out = runProposal(t, "learn", "me", "--avoid", "赋能", "--dir", dir)
	if d := diag(t, out.result, "proposal.previously-rejected"); code != 0 || d.Severity != "warning" || len(out.Proposal.Items) != 3 {
		t.Fatalf("rejected again: %d %+v", code, out)
	}
	if code, out := runProposal(t, "reject", "me", "--item", "style-1", "--dir", dir); code != 1 || !hasNext(diag(t, out.result, "proposal.already-confirmed"), "wa-cli pack learn me") {
		t.Fatalf("reject confirmed: %d %+v", code, out)
	}
	if code, _ := runJSON(t, "validate", "--dir", dir, "--json"); code != 0 {
		t.Fatalf("validate: %d", code)
	}
	if code, _ := runJSON(t, "validate", ".writing-anything/proposals/me.yaml", "--dir", dir, "--json"); code != 0 {
		t.Fatalf("validate proposal: %d", code)
	}
}

func TestPackLearnRejects(t *testing.T) {
	dir := learnWork(t)
	for _, tt := range []struct {
		args []string
		code string
	}{
		{[]string{"learn", "Me", "--pack", "mine", "--source", "a1"}, "input.invalid-value"},
		{[]string{"learn", "me", "--pack", "../x", "--source", "a1"}, "input.invalid-value"},
		{[]string{"learn", "me", "--pack", "mine", "--source", "a1", "--style", " "}, "input.invalid-value"},
		{[]string{"learn", "me", "--source", "a1"}, "proposal.missing-pack"},
		{[]string{"learn", "me", "--pack", "mine", "--source", "nope"}, "proposal.unknown-source"},
		{[]string{"confirm", "me", "--item", "baseline", "--confirmation", "c"}, "proposal.not-found"},
		{[]string{"confirm", "me", "--confirmation", "c"}, "input.invalid-value"},
		{[]string{"confirm", "me", "--item", "baseline"}, "input.invalid-value"},
		{[]string{"reject", "me"}, "input.invalid-value"},
	} {
		code, out := runProposal(t, append(tt.args, "--dir", dir)...)
		if d := diag(t, out.result, tt.code); code != 1 || !hasNext(d, "wa-cli help pack "+tt.args[0]) {
			t.Errorf("%v: %d %+v", tt.args, code, out)
		}
	}
	// 目标为已安装的规范包。
	writeFile(t, filepath.Join(dir, "theirs.yaml"), "schema_version: 1\nid: theirs\ntitle: 第三方\n")
	run(t, "pack", "install", "theirs.yaml", "--dir", dir)
	if code, out := runProposal(t, "learn", "t", "--pack", "theirs", "--source", "a1", "--dir", dir); code != 1 || !hasNext(diag(t, out.result, "proposal.installed-pack"), "wa-cli pack list") {
		t.Fatalf("installed: %d %+v", code, out)
	}
}

// 项目目录名含 [ ] 等通配字符时，整体校验仍然检查 packs/ 与 proposals/ 中的每个文件。
func TestValidateDirWithGlobCharacters(t *testing.T) {
	dir := filepath.Join(t.TempDir(), "Project [2026]")
	if code, stdout, _ := run(t, "init", "--dir", dir); code != 0 {
		t.Fatalf("init: %s", stdout)
	}
	for _, rel := range []string{".writing-anything/proposals/me.yaml", ".writing-anything/packs/house.yaml"} {
		os.MkdirAll(filepath.Dir(filepath.Join(dir, rel)), 0o755)
		writeFile(t, filepath.Join(dir, rel), "id: [")
		code, out := runValidateJSON(t, dir)
		if code != 1 || len(diag(t, out, "artifact.parse-error").Path) == 0 {
			t.Fatalf("%s: %d %+v", rel, code, out)
		}
		os.Remove(filepath.Join(dir, rel))
	}
	if code, out := runProposal(t, "learn", "me", "--pack", "mine", "--source", "nope", "--dir", dir); code != 1 || !hasNext(diag(t, out.result, "proposal.unknown-source"), "wa-cli source list") {
		t.Fatalf("next: %d %+v", code, out)
	}
}

func TestPackPrefer(t *testing.T) {
	dir := newWork(t)
	draftFile := filepath.Join(dir, ".writing-anything", "works", "my-post", "draft.md")
	writeFile(t, filepath.Join(dir, "a.md"), "首先，我们需要赋能团队。\n\n其次，赋能业务。\n\n最后，赋能用户。\n")
	if code, stdout, _ := run(t, "draft", "write", "my-post", "--file", "a.md", "--dir", dir); code != 0 {
		t.Fatal(stdout)
	}
	writeFile(t, draftFile, "先让团队上手。\n\n再理业务。\n\n最后服务用户。\n")
	// Agent 再次写入：修改对进入 edits.yaml。
	code, res := runJSON(t, "diff", "my-post", "--dir", dir, "--json")
	base := res["diff"].(map[string]any)["base"].(string)
	writeFile(t, filepath.Join(dir, "b.md"), "先让团队上手。\n\n再理业务。\n\n最后服务用户。\n\n新的一段。\n")
	code, stdout, _ := run(t, "draft", "write", "my-post", "--file", "b.md", "--base", base, "--dir", dir, "--json")
	if code != 0 || !strings.Contains(stdout, ".writing-anything/works/my-post/edits.yaml") {
		t.Fatalf("draft write: %d %s", code, stdout)
	}
	code, stdout, _ = run(t, "pack", "prefer", "prefs", "--pack", "mine", "--dir", dir, "--json")
	var out struct {
		proposalOut
		Observed *struct{ Edits, Feedback int } `json:"observed"`
	}
	if err := json.Unmarshal([]byte(stdout), &out); err != nil {
		t.Fatal(err)
	}
	if code != 0 || out.Observed == nil || out.Observed.Edits != 3 || out.Proposal.Stats != nil || len(out.Proposal.Items) == 0 ||
		out.Proposal.Items[0].Key == nil || *out.Proposal.Items[0].Key != "removed:赋能" || len(out.Proposal.Items[0].Evidence) != 3 {
		t.Fatalf("prefer: %d %s", code, stdout)
	}
	if !strings.Contains(stdout, `"stats":null`) {
		t.Fatalf("stats not null: %s", stdout)
	}
	if code, stdout, _ := run(t, "pack", "prefer", "prefs", "--dir", dir); code != 0 || !strings.Contains(stdout, "已归纳规范包提议（Pack Proposal）prefs（写入规范包 mine，0 个素材）") {
		t.Fatalf("text: %q", stdout)
	}
	if code, _ := runJSON(t, "validate", "--dir", dir, "--json"); code != 0 {
		t.Fatalf("validate: %d", code)
	}
	if code, _ := runJSON(t, "validate", ".writing-anything/works/my-post/edits.yaml", "--dir", dir, "--json"); code != 0 {
		t.Fatalf("validate edits: %d", code)
	}
	if code, out := runProposal(t, "prefer", "other", "--dir", dir); code != 1 || !hasNext(diag(t, out.result, "proposal.missing-pack"), "wa-cli help pack prefer") {
		t.Fatalf("missing pack: %d %+v", code, out)
	}
	if code, out := runProposal(t, "prefer", "Bad", "--dir", dir); code != 1 || len(diag(t, out.result, "input.invalid-value").Next) == 0 {
		t.Fatalf("bad id: %d %+v", code, out)
	}
}

func TestDraftWriteEditsPrecheckAndMarkup(t *testing.T) {
	dir := newWork(t)
	unitDir := filepath.Join(dir, ".writing-anything", "works", "my-post")
	write := func(content, base string) (int, string) {
		writeFile(t, filepath.Join(dir, "in.md"), content)
		args := []string{"draft", "write", "my-post", "--file", "in.md", "--dir", dir, "--json"}
		if base != "" {
			args = append(args, "--base", base)
		}
		code, stdout, _ := run(t, args...)
		return code, stdout
	}
	baseOf := func() string {
		_, res := runJSON(t, "diff", "my-post", "--dir", dir, "--json")
		return res["diff"].(map[string]any)["base"].(string)
	}
	write("第一段。\n\n第二段。\n", "")
	// 只加了批注、没改文字：不算修改对。
	writeFile(t, filepath.Join(unitDir, "draft.md"), "第一段。<!-- @me: 太短 -->\n\n第二段。\n")
	if code, stdout := write("第一段。\n\n第二段换了。\n", baseOf()); code != 0 || strings.Contains(stdout, "edits.yaml") {
		t.Fatalf("comment-only edit logged: %d %s", code, stdout)
	}
	// edits.yaml 不合法：draft write 在记入批注之前就失败，所有文件逐字节不变。
	writeFile(t, filepath.Join(unitDir, "edits.yaml"), "edits: [")
	writeFile(t, filepath.Join(unitDir, "draft.md"), "第一段改了。<!-- @me: 再短点 -->\n\n第二段换了。\n")
	before := map[string]string{}
	for _, f := range []string{"draft.md", "provenance.yaml", "edits.yaml", "feedback.yaml"} {
		data, _ := os.ReadFile(filepath.Join(unitDir, f))
		before[f] = string(data)
	}
	code, stdout := write("第一段改了。\n\n第三版。\n", baseOf())
	var out result
	json.Unmarshal([]byte(stdout), &out)
	if code != 1 || len(out.ChangedPaths) != 0 || len(diag(t, out, "artifact.parse-error").Next) == 0 {
		t.Fatalf("invalid edits.yaml: %d %s", code, stdout)
	}
	for f, want := range before {
		data, _ := os.ReadFile(filepath.Join(unitDir, f))
		if string(data) != want {
			t.Fatalf("%s changed", f)
		}
	}
	writeFile(t, filepath.Join(unitDir, "edits.yaml"), "schema_version: 1\nedits: &e []\n")
	code, stdout = write("第一段改了。\n\n第三版。\n", baseOf())
	json.Unmarshal([]byte(stdout), &out)
	if code != 1 || !hasNext(diag(t, out, "draft.edits-anchored"), "wa-cli help draft write") {
		t.Fatalf("anchored edits: %d %s", code, stdout)
	}
	// 正文含非 UTF-8 字节：照常逐字节写入，不记原文。
	other := newWork(t)
	writeFile(t, filepath.Join(other, "bin.md"), "abc\xff\xfe\n\n正常。\n")
	if code, stdout, _ := run(t, "draft", "write", "my-post", "--file", "bin.md", "--dir", other, "--json"); code != 0 {
		t.Fatalf("non-UTF-8: %d %s", code, stdout)
	}
	if got := readFile(t, filepath.Join(other, ".writing-anything", "works", "my-post", "draft.md")); got != "abc\xff\xfe\n\n正常。\n" {
		t.Fatalf("draft = %q", got)
	}
}
