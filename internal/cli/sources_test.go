package cli

import (
	"crypto/sha256"
	"encoding/hex"
	"encoding/json"
	"os"
	"path/filepath"
	"slices"
	"strings"
	"testing"
	"time"
)

// sourceResult 是素材、正文与论断命令 --json 输出中测试关心的部分。
type sourceResult struct {
	result
	Source     map[string]any   `json:"source"`
	Sources    []map[string]any `json:"-"`
	Draft      map[string]any   `json:"draft"`
	Provenance *struct {
		Unit       string `json:"unit"`
		Path       string `json:"path"`
		Paragraphs []struct {
			Index  int    `json:"index"`
			Line   int    `json:"line"`
			Origin string `json:"origin"`
			Chars  int    `json:"chars"`
		} `json:"paragraphs"`
		Summary []struct {
			Origin     string  `json:"origin"`
			Paragraphs int     `json:"paragraphs"`
			Chars      int     `json:"chars"`
			Ratio      float64 `json:"ratio"`
		} `json:"summary"`
		TotalParagraphs int `json:"totalParagraphs"`
		TotalChars      int `json:"totalChars"`
	} `json:"provenance"`
	Claim   map[string]any   `json:"claim"`
	Claims  []map[string]any `json:"claims"`
	Removed *string          `json:"removed"`
}

// runSrc 以 input 作为标准输入、以 --json 运行命令，复用 runWSIn 的路径与 next 断言，并断言输出中只有允许为
// null 的字段为 null。
func runSrc(t *testing.T, dir, input string, args ...string) (int, sourceResult) {
	t.Helper()
	code, res := runWSIn(t, dir, input, args...)
	var out sourceResult
	if err := json.Unmarshal([]byte(res.stdout), &out); err != nil {
		t.Fatal(err)
	}
	var v any
	json.Unmarshal([]byte(res.stdout), &v)
	if m, ok := v.(map[string]any); ok {
		if arr, ok := m["sources"].([]any); ok {
			out.Sources = []map[string]any{}
			for _, s := range arr {
				out.Sources = append(out.Sources, s.(map[string]any))
			}
		}
	}
	var walk func(v any, path string)
	walk = func(v any, path string) {
		switch v := v.(type) {
		case map[string]any:
			for k, c := range v {
				if c == nil && !slices.Contains([]string{"source", "draft", "provenance", "claim", "removed", "title", "defaultGenre", "contextBudget", "aiDisclosure"}, k) {
					t.Errorf("%s.%s is null", path, k)
				}
				walk(c, path+"."+k)
			}
		case []any:
			for _, c := range v {
				walk(c, path+"[]")
			}
		}
	}
	walk(v, "")
	out.result = res
	return code, out
}

func sha(s string) string {
	sum := sha256.Sum256([]byte(s))
	return hex.EncodeToString(sum[:])
}

func wsPath(dir, rel string) string { return filepath.Join(dir, filepath.FromSlash(rel)) }

func origins(r sourceResult) []string {
	var out []string
	for _, p := range r.Provenance.Paragraphs {
		out = append(out, p.Origin)
	}
	return out
}

func TestSourceAdd(t *testing.T) {
	dir := newWorkspace(t)
	os.MkdirAll(filepath.Join(dir, "notes"), 0o755)
	content := "我在 2023 年迁移过一次数据库"
	writeFile(t, filepath.Join(dir, "notes", "idea.md"), content)
	code, res := runSrc(t, dir, "", "source", "add", "--type", "author-note", "--file", "notes/idea.md", "--title", "迁移笔记")
	id := "src-" + sha(content)[:12]
	srcDir := ".writing-anything/sources/" + id
	if code != 0 || res.Source["id"] != id || res.Source["type"] != "author-note" || res.Source["title"] != "迁移笔记" ||
		res.Source["path"] != srcDir+"/original.md" || res.Source["sha256"] != sha(content) {
		t.Fatalf("code = %d, source = %v, diagnostics = %v", code, res.Source, res.Diagnostics)
	}
	if !slices.Equal(res.ChangedPaths, []string{srcDir, srcDir + "/original.md", srcDir + "/source.yaml"}) {
		t.Errorf("changedPaths = %v", res.ChangedPaths)
	}
	if readFile(t, wsPath(dir, srcDir+"/original.md")) != content {
		t.Error("archive differs from the input")
	}
	if !strings.Contains(readFile(t, wsPath(dir, srcDir+"/source.yaml")), "original_name: idea.md") {
		t.Error("source.yaml lacks original_name")
	}
	// 同一内容再次登记。
	before := readFile(t, wsPath(dir, srcDir+"/source.yaml"))
	code, res = runSrc(t, dir, "", "source", "add", "--type", "author-note", "--file", "notes/idea.md")
	if d := diag(t, res.result, "source.exists"); code != 1 || d.Path != srcDir || !strings.Contains(d.Next[0], "source list") {
		t.Errorf("duplicate: code = %d, diagnostic = %+v", code, d)
	}
	if readFile(t, wsPath(dir, srcDir+"/source.yaml")) != before {
		t.Error("duplicate add changed source.yaml")
	}
	// 绝对路径也可以。
	abs := filepath.Join(t.TempDir(), "talk")
	writeFile(t, abs, "访谈")
	if code, res = runSrc(t, dir, "", "source", "add", "--type", "transcript", "--file", abs); code != 0 || !strings.HasSuffix(res.Source["path"].(string), "/original") || res.Source["title"] != nil {
		t.Errorf("absolute path: code = %d, source = %v", code, res.Source)
	}
}

func TestSourceAddStdin(t *testing.T) {
	dir := newWorkspace(t)
	code, res := runSrc(t, dir, "第一段", "source", "add", "--type", "interview", "--id", "interview-ceo")
	if code != 0 || res.Source["id"] != "interview-ceo" || res.Source["path"] != ".writing-anything/sources/interview-ceo/original.txt" {
		t.Fatalf("code = %d, source = %v, %v", code, res.Source, res.Diagnostics)
	}
	if readFile(t, wsPath(dir, ".writing-anything/sources/interview-ceo/original.txt")) != "第一段" {
		t.Error("stdin archive differs")
	}
}

func TestSourceAddRejects(t *testing.T) {
	dir := newWorkspace(t)
	writeFile(t, filepath.Join(dir, "idea.md"), "x")
	writeFile(t, filepath.Join(dir, "empty.md"), "")
	for _, tc := range []struct {
		name  string
		args  []string
		code  string
		field string
	}{
		{"unknown type", []string{"--type", "diary", "--file", "idea.md"}, "artifact.schema-violation", "type"},
		{"bad id", []string{"--type", "reference", "--id", "Bad_ID", "--file", "idea.md"}, "artifact.schema-violation", "id"},
		{"empty", []string{"--type", "author-note", "--file", "empty.md"}, "source.empty", ""},
		{"missing file", []string{"--type", "author-note", "--file", "nope.md"}, "input.read-failed", ""},
	} {
		code, res := runSrc(t, dir, "", append([]string{"source", "add"}, tc.args...)...)
		d := diag(t, res.result, tc.code)
		if code != 1 || d.Field != tc.field || d.Next[0] != "wa-cli help source add --json" || res.Source != nil {
			t.Errorf("%s: code = %d, diagnostic = %+v", tc.name, code, d)
		}
	}
	if entries, _ := os.ReadDir(filepath.Join(dir, ".writing-anything", "sources")); len(entries) != 0 {
		t.Errorf("sources/ has %d entries, want none", len(entries))
	}
	if code, res := runSrc(t, t.TempDir(), "x", "source", "add", "--type", "author-note"); code != 1 || diag(t, res.result, "workspace.not-found").Code == "" {
		t.Errorf("no workspace: code = %d", code)
	}
}

func TestSourceList(t *testing.T) {
	dir := newWorkspace(t)
	if code, res := runSrc(t, dir, "", "source", "list"); code != 0 || res.Sources == nil || len(res.Sources) != 0 || !res.Valid {
		t.Fatalf("empty list: code = %d, %+v", code, res)
	}
	runSrc(t, dir, "b", "source", "add", "--type", "reference", "--id", "zeta")
	runSrc(t, dir, "a", "source", "add", "--type", "interview", "--id", "interview-ceo", "--title", "访谈")
	runSrc(t, dir, "c", "source", "add", "--type", "reference", "--id", "tampered")
	writeFile(t, wsPath(dir, ".writing-anything/sources/tampered/original.txt"), "改过")
	code, res := runSrc(t, dir, "", "source", "list")
	var ids []string
	for _, s := range res.Sources {
		ids = append(ids, s["id"].(string))
	}
	if code != 1 || res.Valid || !slices.Equal(ids, []string{"interview-ceo", "zeta"}) || res.Sources[0]["title"] != "访谈" {
		t.Fatalf("code = %d, sources = %v", code, res.Sources)
	}
	if len(res.ChangedPaths) != 0 {
		t.Errorf("changedPaths = %v", res.ChangedPaths)
	}
	diag(t, res.result, "source.modified")
}

// --url 与 --tier：合法值写入 source.yaml 并在 add/list 输出透出；非法值报 Schema 校验错误（field 为 url 或 tier）；
// 不带新参数的行为与产物不变（既有用例覆盖）。
func TestSourceURLTier(t *testing.T) {
	dir := newWorkspace(t)
	code, res := runSrc(t, dir, "报道原文", "source", "add", "--type", "reference", "--id", "news",
		"--url", "https://example.com/post/1", "--tier", "secondary")
	if code != 0 || res.Source["url"] != "https://example.com/post/1" || res.Source["tier"] != "secondary" {
		t.Fatalf("add url/tier: code = %d, source = %v, %v", code, res.Source, res.Diagnostics)
	}
	meta := readFile(t, wsPath(dir, ".writing-anything/sources/news/source.yaml"))
	if !strings.Contains(meta, "url: https://example.com/post/1") || !strings.Contains(meta, "tier: secondary") {
		t.Fatalf("source.yaml lacks url/tier: %s", meta)
	}
	if code, res := runSrc(t, dir, "", "source", "list"); code != 0 {
		t.Fatalf("list: code = %d, %v", code, res.Diagnostics)
	} else if res.Sources[0]["url"] != "https://example.com/post/1" || res.Sources[0]["tier"] != "secondary" {
		t.Fatalf("list url/tier: %v", res.Sources[0])
	}
	if code, res := runSrc(t, dir, "", "validate"); code != 0 {
		t.Fatalf("validate: code = %d, %v", code, res.Diagnostics)
	}
	// 不带新参数：产物不含 url/tier 键。
	runSrc(t, dir, "笔记", "source", "add", "--type", "author-note", "--id", "note")
	plain := readFile(t, wsPath(dir, ".writing-anything/sources/note/source.yaml"))
	if strings.Contains(plain, "url:") || strings.Contains(plain, "tier:") {
		t.Fatalf("plain source.yaml has url/tier: %s", plain)
	}
	_, lres := runSrc(t, dir, "", "source", "list")
	for _, sv := range lres.Sources {
		if sv["id"] != "note" {
			continue
		}
		if _, has := sv["url"]; has {
			t.Errorf("plain note view has url key: %v", sv)
		}
		if _, has := sv["tier"]; has {
			t.Errorf("plain note view has tier key: %v", sv)
		}
	}
	// 非法取值：Schema 校验拒绝，field 指向参数，不创建素材目录。
	for _, tc := range []struct {
		flag, value, field string
	}{{"url", "ftp://example.com/x", "url"}, {"tier", "rumour", "tier"}, {"url", "https://example.com/" + strings.Repeat("a", 2048), "url"}} {
		code, res := runSrc(t, dir, "更多原文", "source", "add", "--type", "reference", "--id", "bad-"+tc.field, "--"+tc.flag, tc.value)
		d := diag(t, res.result, "artifact.schema-violation")
		if code != 1 || d.Field != tc.field {
			t.Errorf("%s=%s: code = %d, diagnostic = %+v", tc.flag, tc.value[:12], code, d)
		}
		if _, err := os.Stat(wsPath(dir, ".writing-anything/sources/bad-"+tc.field)); !os.IsNotExist(err) {
			t.Errorf("%s=%s: directory created", tc.flag, tc.field)
		}
	}
}

func TestArchiveIntegrity(t *testing.T) {
	dir := newWorkspace(t)
	runSrc(t, dir, "访谈原文", "source", "add", "--type", "interview", "--id", "interview-ceo")
	code, res := runSrc(t, dir, "", "validate")
	if code != 0 || !slices.Contains(res.Checked, ".writing-anything/sources/interview-ceo/source.yaml") {
		t.Fatalf("intact: code = %d, checked = %v, %v", code, res.Checked, res.Diagnostics)
	}
	writeFile(t, wsPath(dir, ".writing-anything/sources/interview-ceo/original.txt"), "改过的访谈")
	code, res = runSrc(t, dir, "", "validate")
	d := diag(t, res.result, "source.modified")
	if code != 1 || res.Valid || d.Path != ".writing-anything/sources/interview-ceo/original.txt" {
		t.Fatalf("tampered: code = %d, diagnostic = %+v", code, d)
	}
	// next 中的整体校验能重新检出问题（cli-contract/next-rechecks-problem）。
	if !slices.Contains(d.Next, shellCommand("wa-cli", "validate", "--dir", dir, "--json")) {
		t.Errorf("next = %v", d.Next)
	}
	// 单个 source.yaml 的校验同样检查存档。
	if code, res = runSrc(t, dir, "", "validate", ".writing-anything/sources/interview-ceo/source.yaml"); code != 1 {
		t.Errorf("single source.yaml: code = %d", code)
	} else {
		diag(t, res.result, "source.modified")
	}
	os.Mkdir(wsPath(dir, ".writing-anything/sources/loose"), 0o755)
	_, res = runSrc(t, dir, "", "validate")
	if d := diag(t, res.result, "artifact.not-found"); d.Path != ".writing-anything/sources/loose/source.yaml" || d.Next[0] != "wa-cli help source add --json" {
		t.Errorf("missing metadata: %+v", d)
	}
	// sources/ 下的普通文件不属于素材库；status 的计数含义不变。
	os.RemoveAll(wsPath(dir, ".writing-anything/sources"))
	os.Mkdir(wsPath(dir, ".writing-anything/sources"), 0o755)
	writeFile(t, wsPath(dir, ".writing-anything/sources/notes.md"), "x")
	if code, res = runSrc(t, dir, "", "status"); code != 0 || res.result.Sources != 1 {
		t.Errorf("status with a loose file: code = %d, sources = %d", code, res.result.Sources)
	}
}

func TestDraftWrite(t *testing.T) {
	dir := newWork(t)
	body := "第一段。\n\n第二段。\n\n第三段。\n"
	code, res := runSrc(t, dir, body, "draft", "write", "my-post")
	draftRel, recordRel := ".writing-anything/works/my-post/draft.md", ".writing-anything/works/my-post/provenance.yaml"
	if code != 0 || res.Draft["unit"] != "my-post" || res.Draft["path"] != draftRel || res.Draft["paragraphs"] != 3.0 || res.Draft["sha256"] != sha(body) {
		t.Fatalf("code = %d, draft = %v, %v", code, res.Draft, res.Diagnostics)
	}
	if !slices.Equal(res.ChangedPaths, []string{draftRel, recordRel}) {
		t.Errorf("changedPaths = %v", res.ChangedPaths)
	}
	if readFile(t, wsPath(dir, draftRel)) != body || strings.Count(readFile(t, wsPath(dir, recordRel)), "origin: agent") != 3 {
		t.Error("draft.md or provenance.yaml is wrong")
	}
	// draft.md 满足 draft 阶段的必需产物。
	writeFile(t, wsPath(dir, ".writing-anything/works/my-post/brief.yaml"), completeBrief)
	runWS(t, dir, "advance", "my-post")
	if _, res := runUnits(t, dir, "next", "my-post"); len(res.Steps) != 1 || len(res.Steps[0]["blockers"].([]any)) != 0 {
		t.Errorf("next after draft write = %v", res.Steps)
	}
	// 同样的内容再次写入：不写文件。
	info, _ := os.Stat(wsPath(dir, draftRel))
	time.Sleep(10 * time.Millisecond)
	if code, res = runSrc(t, dir, body, "draft", "write", "my-post"); code != 0 || len(res.ChangedPaths) != 0 {
		t.Errorf("same content: code = %d, changedPaths = %v", code, res.ChangedPaths)
	}
	if after, _ := os.Stat(wsPath(dir, draftRel)); !after.ModTime().Equal(info.ModTime()) {
		t.Error("same content touched draft.md")
	}
	// 验证写入记录也被整体校验检查。
	if _, res = runSrc(t, dir, "", "validate"); !slices.Contains(res.Checked, recordRel) {
		t.Errorf("checked = %v lacks provenance.yaml", res.Checked)
	}
}

func TestDraftWriteChildAndRejects(t *testing.T) {
	dir := newWork(t)
	runWS(t, dir, "unit", "add", "my-post", "section", "intro")
	writeFile(t, filepath.Join(dir, "intro.md"), "引言")
	code, res := runSrc(t, dir, "", "draft", "write", "my-post/intro", "--file", "intro.md")
	unitDir := ".writing-anything/works/my-post/units/intro"
	if code != 0 || !slices.Equal(res.ChangedPaths, []string{unitDir, unitDir + "/draft.md", unitDir + "/provenance.yaml"}) {
		t.Fatalf("child: code = %d, changedPaths = %v", code, res.ChangedPaths)
	}
	code, res = runSrc(t, dir, " \n", "draft", "write", "my-post")
	if d := diag(t, res.result, "draft.empty"); code != 1 ||
		!concreteFirst(d, shellCommand("wa-cli", "context", "my-post", "--dir", dir, "--json"), "wa-cli help draft write --json") {
		t.Errorf("empty: code = %d, %+v", code, d)
	}
	if _, err := os.Stat(wsPath(dir, ".writing-anything/works/my-post/draft.md")); err == nil {
		t.Error("empty write created draft.md")
	}
	code, res = runSrc(t, dir, strings.Repeat("段\n\n", 10001), "draft", "write", "my-post")
	if d := diag(t, res.result, "draft.too-large"); code != 1 ||
		!concreteFirst(d, shellCommand("wa-cli", "context", "my-post", "--dir", dir, "--json"), "wa-cli help draft write --json") {
		t.Errorf("too large: code = %d, %+v", code, d)
	}
	if code, res = runSrc(t, dir, "x", "draft", "write", "my-post/nope"); code != 1 {
		t.Errorf("unknown unit: code = %d", code)
	} else {
		diag(t, res.result, "unit.not-found")
	}
	// 输入文件不存在时先报告，不解析单元。
	fresh := newWorkspace(t)
	code, res = runSrc(t, fresh, "", "draft", "write", "my-post", "--file", "nope.md")
	if d := diag(t, res.result, "input.read-failed"); code != 1 || !strings.Contains(d.Message, "nope.md") || d.Path != "" {
		t.Errorf("missing file: code = %d, %+v", code, d)
	}
	if entries, _ := os.ReadDir(wsPath(fresh, ".writing-anything/works")); len(entries) != 0 {
		t.Error("missing input created files")
	}
}

func TestProvenance(t *testing.T) {
	dir := newWork(t)
	draftRel := wsPath(dir, ".writing-anything/works/my-post/draft.md")
	code, res := runSrc(t, dir, "", "provenance", "my-post")
	if d := diag(t, res.result, "draft.not-found"); code != 1 || d.Path != ".writing-anything/works/my-post/draft.md" || !slices.Equal(d.Next, []string{"wa-cli context my-post --dir " + dir + " --json", "wa-cli help draft write --json"}) || res.Provenance != nil {
		t.Fatalf("no draft: code = %d, %+v", code, d)
	}
	// 没有写入记录：全部为 human。
	writeFile(t, draftRel, "作者写的。\n\n也是作者写的。\n")
	if _, res = runSrc(t, dir, "", "provenance", "my-post"); !slices.Equal(origins(res), []string{"human", "human"}) {
		t.Errorf("no record: %v", origins(res))
	}
	a, b := strings.Repeat("甲", 30), strings.Repeat("乙", 30)
	runSrc(t, dir, a+"\n\n"+b+"\n\n"+strings.Repeat("丙", 30)+"\n", "draft", "write", "my-post", "--base", currentBase(t, dir))
	for _, tc := range []struct {
		name, body string
		want       []string
	}{
		{"edit one", a + "\n\n改过\n\n" + strings.Repeat("丙", 30) + "\n", []string{"agent", "agent-draft-human-edited", "agent"}},
		{"insert one", a + "\n\n新增\n\n" + b + "\n\n" + strings.Repeat("丙", 30) + "\n", []string{"agent", "human", "agent", "agent"}},
		{"delete one", a + "\n\n" + strings.Repeat("丙", 30) + "\n", []string{"agent", "agent"}},
		{"trailing whitespace", a + "   \n\n" + b + "\n\n" + strings.Repeat("丙", 30) + "\r\n", []string{"agent", "agent", "agent"}},
	} {
		writeFile(t, draftRel, tc.body)
		if code, res = runSrc(t, dir, "", "provenance", "my-post"); code != 0 || !slices.Equal(origins(res), tc.want) {
			t.Errorf("%s: code = %d, origins = %v, want %v", tc.name, code, origins(res), tc.want)
		}
	}
	// 汇总：两段 Agent 各 30 字符，作者追加 40 字符。
	runSrc(t, dir, a+"\n\n"+b+"\n", "draft", "write", "my-post", "--base", currentBase(t, dir))
	writeFile(t, draftRel, a+"\n\n"+b+"\n\n"+strings.Repeat("丁", 40)+"\n")
	code, res = runSrc(t, dir, "", "provenance", "my-post")
	p := res.Provenance
	if code != 0 || p.TotalParagraphs != 3 || p.TotalChars != 100 || len(res.ChangedPaths) != 0 || p.Unit != "my-post" {
		t.Fatalf("summary: code = %d, %+v", code, p)
	}
	type share struct {
		o    string
		n, c int
		r    float64
	}
	want := []share{{"human", 1, 40, 0.4}, {"agent", 2, 60, 0.6}, {"agent-draft-human-edited", 0, 0, 0}}
	for i, s := range p.Summary {
		if got := (share{s.Origin, s.Paragraphs, s.Chars, s.Ratio}); got != want[i] {
			t.Errorf("summary[%d] = %+v, want %+v", i, got, want[i])
		}
	}
	if p.Paragraphs[2].Index != 3 || p.Paragraphs[2].Line != 5 || p.Paragraphs[2].Chars != 40 {
		t.Errorf("paragraphs[2] = %+v", p.Paragraphs[2])
	}
	// 围栏代码块整体算一段。
	runSrc(t, dir, "说明\n\n```go\nfunc a() {\n\n}\n```\n\n再说明\n", "draft", "write", "my-post", "--base", currentBase(t, dir))
	if _, res = runSrc(t, dir, "", "provenance", "my-post"); res.Provenance.TotalParagraphs != 3 {
		t.Errorf("fenced code: %d paragraphs, want 3", res.Provenance.TotalParagraphs)
	}
}

func TestDraftRewriteKeepsHuman(t *testing.T) {
	dir := newWork(t)
	runSrc(t, dir, "第一段。\n\n第二段。\n", "draft", "write", "my-post")
	draftRel := wsPath(dir, ".writing-anything/works/my-post/draft.md")
	writeFile(t, draftRel, readFile(t, draftRel)+"\n作者加的第三段。\n")
	if code, res := runSrc(t, dir, "改写的第一段。\n\n第二段。\n\n作者加的第三段。\n", "draft", "write", "my-post", "--base", currentBase(t, dir)); code != 0 || !slices.Contains(res.ChangedPaths, draftPath) {
		t.Fatalf("write with base: code = %d, %v", code, res.Diagnostics)
	}
	if !strings.HasPrefix(readFile(t, draftRel), "改写的第一段。") {
		t.Fatal("draft not rewritten")
	}
	if _, res := runSrc(t, dir, "", "provenance", "my-post"); !slices.Equal(origins(res), []string{"agent", "agent", "human"}) {
		t.Errorf("origins = %v", origins(res))
	}
}

func TestClaimAdd(t *testing.T) {
	dir := newWork(t)
	runSrc(t, dir, "访谈", "source", "add", "--type", "interview", "--id", "interview-ceo")
	runSrc(t, dir, "报道", "source", "add", "--type", "reference", "--id", "news")
	rel := ".writing-anything/works/my-post/claims.yaml"
	code, res := runSrc(t, dir, "", "claim", "add", "my-post", "--kind", "experience", "--text", "我们在一周内完成了迁移", "--sources", "interview-ceo")
	if code != 0 || res.Claim["id"] != "claim-1" || res.Claim["unit"] != "my-post" || res.Claim["kind"] != "experience" ||
		!slices.Equal(res.ChangedPaths, []string{rel}) {
		t.Fatalf("code = %d, claim = %v, %v", code, res.Claim, res.Diagnostics)
	}
	if srcs := res.Claim["sources"].([]any); len(srcs) != 1 || srcs[0] != "interview-ceo" {
		t.Errorf("sources = %v", srcs)
	}
	// 写入时校验 kind×source：需要亲历素材的类型只关联 reference 时拒绝并建议 fact；fact 关联 reference 合法。
	before := readFile(t, wsPath(dir, rel))
	code, res = runSrc(t, dir, "", "claim", "add", "my-post", "--kind", "data", "--text", "性能提升了 30%", "--sources", "news")
	d := diag(t, res.result, "claim.needs-author-source")
	if code != 1 || d.Field != "kind" || !strings.Contains(d.Message, "news（reference）") || !strings.Contains(d.Message, "--kind fact") ||
		!strings.Contains(d.Message, "已生成阻塞决策") || !slices.Contains(d.Next, "wa-cli help claim add --json") {
		t.Errorf("reference-only data: code = %d, %+v", code, d)
	}
	if readFile(t, wsPath(dir, rel)) != before {
		t.Error("rejected claim changed claims.yaml")
	}
	code, res = runSrc(t, dir, "", "claim", "add", "my-post", "--kind", "fact", "--text", "报道称性能提升了 30%", "--sources", "news")
	if code != 0 || res.Claim["kind"] != "fact" {
		t.Errorf("fact with reference: code = %d, claim = %v", code, res.Claim)
	}
	// 亲历与 reference 混合：有亲历素材即可。
	code, res = runSrc(t, dir, "", "claim", "add", "my-post", "--kind", "data", "--text", "我们实测性能提升了 30%", "--sources", "interview-ceo,news")
	if code != 0 {
		t.Errorf("mixed sources: code = %d, %v", code, res.Diagnostics)
	}
	code, res = runSrc(t, dir, "", "claim", "add", "my-post", "--kind", "opinion", "--text", "微服务不适合小团队")
	if code != 0 || len(res.Claim["sources"].([]any)) != 0 {
		t.Errorf("without source: code = %d, claim = %v", code, res.Claim)
	}
	// claim set --kind 把只关联 reference 的论断改入需要亲历素材的类型时同样拒绝（claim-2 为 fact+news）。
	beforeSet := readFile(t, wsPath(dir, rel))
	code, res = runSrc(t, dir, "", "claim", "set", "my-post", "claim-2", "--kind", "data")
	if d := diag(t, res.result, "claim.needs-author-source"); code != 1 || d.Field != "kind" ||
		!strings.Contains(d.Message, "news（reference）") || !strings.Contains(d.Message, "--kind fact") ||
		!strings.Contains(d.Message, "已生成阻塞决策") || readFile(t, wsPath(dir, rel)) != beforeSet {
		t.Errorf("set kind to data: code = %d, diag = %+v", code, d)
	}
	// 项目层覆盖 claim-sources 参数后写入校验随之放行（与 lint 同判）：source_types 含 reference 时不拒；
	// kinds 置空表示没有类型需要亲历素材，也不拒。
	writeFile(t, configPath(dir), readFile(t, configPath(dir))+"rules:\n  claims: {params: {source_types: [reference]}}\n")
	code, res = runSrc(t, dir, "", "claim", "add", "my-post", "--kind", "data", "--text", "覆盖后的外部数据", "--sources", "news")
	if code != 0 {
		t.Errorf("overridden source_types: code = %d, %v", code, res.Diagnostics)
	}
	writeFile(t, configPath(dir), strings.Replace(readFile(t, configPath(dir)),
		"  claims: {params: {source_types: [reference]}}", "  claims: {params: {kinds: []}}", 1))
	code, res = runSrc(t, dir, "", "claim", "add", "my-post", "--kind", "case", "--text", "空 kinds 后的案例", "--sources", "news")
	if code != 0 {
		t.Errorf("empty kinds: code = %d, %v", code, res.Diagnostics)
	}
	writeFile(t, configPath(dir), strings.Replace(readFile(t, configPath(dir)), "rules:\n  claims: {params: {kinds: []}}\n", "", 1))
	before = readFile(t, wsPath(dir, rel))
	code, res = runSrc(t, dir, "", "claim", "add", "my-post", "--kind", "data", "--text", "性能提升了 30%", "--sources", "nope")
	d = diag(t, res.result, "claim.unknown-source")
	if code != 1 || d.Field != "sources[0]" || !slices.Contains(d.Next, shellCommand("wa-cli", "source", "list", "--dir", dir, "--json")) {
		t.Errorf("unknown source: code = %d, %+v", code, d)
	}
	for _, args := range [][]string{{"--kind", "rumor", "--text", "x"}, {"--kind", "data"}, {"--kind", "data", "--text", "x", "--id", "claim-1"}} {
		code, res = runSrc(t, dir, "", append([]string{"claim", "add", "my-post"}, args...)...)
		if code != 1 || res.Diagnostics[0].Next[0] != "wa-cli help claim add --json" {
			t.Errorf("%v: code = %d, %v", args, code, res.Diagnostics)
		}
	}
	if readFile(t, wsPath(dir, rel)) != before {
		t.Error("rejected claims changed claims.yaml")
	}
	// 被引用的素材删除后，validate 报告 claim.unknown-source；单个 claims.yaml 同样检查。
	if code, res = runSrc(t, dir, "", "validate", rel); code != 0 || !slices.Equal(res.Checked, []string{rel}) {
		t.Fatalf("single claims.yaml: code = %d, %v", code, res.Diagnostics)
	}
	if code, res = runSrc(t, dir, "", "validate"); code != 0 || !res.Valid || !slices.Contains(res.Checked, rel) || !slices.Contains(res.Checked, ".writing-anything/sources/interview-ceo/source.yaml") {
		t.Errorf("checks-source-and-unit-files: code = %d, checked = %v, %v", code, res.Checked, res.Diagnostics)
	}
	os.RemoveAll(wsPath(dir, ".writing-anything/sources/interview-ceo"))
	// 追加无关论断时，已有论断的素材引用问题保留能复查它的整体校验。
	code, res = runSrc(t, dir, "", "claim", "add", "my-post", "--kind", "fact", "--text", "无关")
	if d := diag(t, res.result, "claim.unknown-source"); code != 1 || d.Field != "claims[0].sources[0]" || !slices.Contains(d.Next, shellCommand("wa-cli", "validate", "--dir", dir, "--json")) {
		t.Errorf("existing problem during claim add: code = %d, %+v", code, d)
	}
	code, res = runSrc(t, dir, "", "validate")
	d = diag(t, res.result, "claim.unknown-source")
	if code != 1 || d.Path != rel || d.Field != "claims[0].sources[0]" || !slices.Contains(d.Next, shellCommand("wa-cli", "validate", "--dir", dir, "--json")) {
		t.Errorf("removed source: code = %d, %+v", code, d)
	}
	// 已有 claims.yaml 的根级问题保留能复查它的命令。
	saved := readFile(t, wsPath(dir, rel))
	writeFile(t, wsPath(dir, rel), saved+"foo: 1\n")
	_, res = runSrc(t, dir, "", "claim", "add", "my-post", "--kind", "fact", "--text", "x")
	if d := diag(t, res.result, "artifact.schema-violation"); d.Next[0] == "wa-cli help claim add --json" {
		t.Errorf("root-level problem next = %v", d.Next)
	}
	writeFile(t, wsPath(dir, rel), strings.Replace(saved, "claim-2", "claim-1", 1))
	_, res = runSrc(t, dir, "", "validate")
	if d := diag(t, res.result, "artifact.duplicate-id"); d.Field != "claims[1].id" || d.Next[0] != shellCommand("wa-cli", "validate", "--dir", dir, "--json") {
		t.Errorf("duplicate id: %+v", d)
	}
}

// claim set、claim list 与 claim remove（claim-edit）。
func TestClaimSetListRemove(t *testing.T) {
	dir := newWork(t)
	runSrc(t, dir, "访谈", "source", "add", "--type", "interview", "--id", "talk")
	runSrc(t, dir, "资料", "source", "add", "--type", "reference", "--id", "doc")
	rel := ".writing-anything/works/my-post/claims.yaml"
	if code, res := runSrc(t, dir, "", "claim", "list", "my-post"); code != 0 || res.Claims == nil || len(res.Claims) != 0 {
		t.Fatalf("empty list: code = %d, %+v", code, res)
	}
	mustRunWS(t, dir, "claim", "add", "my-post", "--kind", "experience", "--text", "我负责迁移", "--sources", "talk")
	mustRunWS(t, dir, "claim", "add", "my-post", "--kind", "opinion", "--text", "观点")
	// 作者的注释在修改与删除后保留。
	writeFile(t, wsPath(dir, rel), strings.Replace(readFile(t, wsPath(dir, rel)), "claims:", "# 作者的注释\nclaims:", 1))
	code, res := runSrc(t, dir, "", "claim", "set", "my-post", "claim-1", "--sources", "talk, doc")
	if code != 0 || !slices.Equal(res.ChangedPaths, []string{rel}) || res.Claim["text"] != "我负责迁移" || res.Claim["kind"] != "experience" ||
		len(res.Claim["sources"].([]any)) != 2 || res.Claim["sources"].([]any)[0] != "talk" {
		t.Fatalf("set sources: code = %d, %+v", code, res)
	}
	code, res = runSrc(t, dir, "", "claim", "set", "my-post", "claim-1", "--kind", "case", "--text", "我主导了迁移")
	if code != 0 || res.Claim["kind"] != "case" || res.Claim["text"] != "我主导了迁移" || strings.Contains(readFile(t, wsPath(dir, rel)), "我负责迁移") {
		t.Fatalf("set kind and text: code = %d, %+v\n%s", code, res, readFile(t, wsPath(dir, rel)))
	}
	// 取值未变时不写文件。
	if code, res = runSrc(t, dir, "", "claim", "set", "my-post", "claim-1", "--kind", "case"); code != 0 || len(res.ChangedPaths) != 0 {
		t.Errorf("unchanged: code = %d, changed = %v", code, res.ChangedPaths)
	}
	code, res = runSrc(t, dir, "", "claim", "set", "my-post", "claim-2", "--sources", "")
	if code != 0 || len(res.Claim["sources"].([]any)) != 0 {
		t.Errorf("clear sources: code = %d, %+v", code, res)
	}
	before := readFile(t, wsPath(dir, rel))
	// 参数问题给出用法与素材列表；找不到论断时给出论断列表。
	for _, args := range [][]string{{"claim-1", "--kind", "rumor"}, {"claim-1", "--sources", "nope"}, {"claim-1", "--kind", ""}, {"claim-1", "--text", ""}} {
		code, res = runSrc(t, dir, "", append([]string{"claim", "set", "my-post"}, args...)...)
		if code != 1 || res.Claim != nil || res.Diagnostics[0].Next[0] != "wa-cli help claim set --json" {
			t.Errorf("%v: code = %d, %+v", args, code, res.Diagnostics)
		}
	}
	listNext := shellCommand("wa-cli", "claim", "list", "my-post", "--dir", dir, "--json")
	for _, args := range [][]string{{"set", "my-post", "claim-9", "--kind", "data"}, {"remove", "my-post", "claim-9"}} {
		code, res = runSrc(t, dir, "", append([]string{"claim"}, args...)...)
		if d := diag(t, res.result, "claim.not-found"); code != 1 || !slices.Equal(d.Next, []string{listNext}) {
			t.Errorf("%v: code = %d, %+v", args, code, d)
		}
	}
	if readFile(t, wsPath(dir, rel)) != before {
		t.Error("rejected edits changed claims.yaml")
	}
	code, res = runSrc(t, dir, "", "claim", "list", "my-post")
	if code != 0 || len(res.Claims) != 2 || res.Claims[0]["id"] != "claim-1" || res.Claims[0]["unit"] != "my-post" || res.Claims[1]["kind"] != "opinion" {
		t.Fatalf("list: code = %d, %+v", code, res.Claims)
	}
	code, res = runSrc(t, dir, "", "claim", "remove", "my-post", "claim-1")
	if code != 0 || res.Removed == nil || *res.Removed != "claim-1" || !slices.Equal(res.ChangedPaths, []string{rel}) {
		t.Fatalf("remove: code = %d, %+v", code, res)
	}
	if got := readFile(t, wsPath(dir, rel)); !strings.Contains(got, "# 作者的注释") || strings.Contains(got, "claim-1") || !strings.Contains(got, "claim-2") {
		t.Errorf("after remove:\n%s", got)
	}
	// 途经锚点的论断不修改；claims.yaml 不存在时找不到论断。
	writeFile(t, wsPath(dir, rel), "schema_version: 1\nclaims:\n  - &c {id: claim-1, kind: fact, text: 事实, sources: []}\n")
	for _, args := range [][]string{{"set", "my-post", "claim-1", "--kind", "data"}, {"remove", "my-post", "claim-1"}} {
		code, res = runSrc(t, dir, "", append([]string{"claim"}, args...)...)
		if d := diag(t, res.result, "claim.anchored-value"); code != 1 || d.Next[0] != "wa-cli help claim "+args[0]+" --json" {
			t.Errorf("anchored %v: code = %d, %+v", args, code, d)
		}
	}
	writeFile(t, wsPath(dir, rel), "claims: [\n")
	if code, res = runSrc(t, dir, "", "claim", "list", "my-post"); code != 1 || len(res.Claims) != 0 {
		t.Errorf("invalid claims.yaml: code = %d, %+v", code, res)
	}
	os.Remove(wsPath(dir, rel))
	if code, res = runSrc(t, dir, "", "claim", "remove", "my-post", "claim-1"); code != 1 || diag(t, res.result, "claim.not-found").Path != "" {
		t.Errorf("missing claims.yaml: code = %d, %+v", code, res.Diagnostics)
	}
}

// 其他论断引用的素材被删除时，claim set、claim list 与 claim remove 照常处理，悬空引用以 warning 报告
// （workspace-artifacts/cross-artifact-references）。
func TestClaimEditDanglingSource(t *testing.T) {
	dir := newWork(t)
	runSrc(t, dir, "访谈", "source", "add", "--type", "interview", "--id", "src-a")
	runSrc(t, dir, "资料", "source", "add", "--type", "interview", "--id", "src-b")
	mustRunWS(t, dir, "claim", "add", "my-post", "--kind", "opinion", "--text", "观点")
	mustRunWS(t, dir, "claim", "add", "my-post", "--kind", "data", "--text", "数据", "--sources", "src-b")
	mustRunWS(t, dir, "claim", "add", "my-post", "--kind", "case", "--text", "案例")
	os.RemoveAll(wsPath(dir, ".writing-anything/sources/src-b"))
	dangling := func(res sourceResult) bool {
		for _, d := range res.Diagnostics {
			if d.Code == "claim.unknown-source" && d.Severity == "warning" && d.Field == "claims[1].sources[0]" && strings.Contains(d.Message, "wa-cli claim set") {
				return true
			}
		}
		return false
	}
	if code, res := runSrc(t, dir, "", "claim", "set", "my-post", "claim-1", "--sources", "src-a"); code != 0 || !res.Valid || len(res.ChangedPaths) != 1 || !dangling(res) {
		t.Fatalf("set: code = %d, %+v", code, res.Diagnostics)
	}
	if code, res := runSrc(t, dir, "", "claim", "list", "my-post"); code != 0 || len(res.Claims) != 3 || !dangling(res) {
		t.Fatalf("list: code = %d, %d claims, %+v", code, len(res.Claims), res.Diagnostics)
	}
	if code, res := runSrc(t, dir, "", "claim", "remove", "my-post", "claim-1"); code != 0 || res.Removed == nil || len(res.Diagnostics) != 1 {
		t.Fatalf("remove: code = %d, %+v", code, res.Diagnostics)
	}
	// 修复悬空引用本身：改为已登记的素材后不再有 warning；validate 对悬空引用仍报告 error。
	if code, res := runSrc(t, dir, "", "validate"); code != 1 || diag(t, res.result, "claim.unknown-source").Severity != "error" {
		t.Fatalf("validate: code = %d, %+v", code, res.Diagnostics)
	}
	if code, res := runSrc(t, dir, "", "claim", "set", "my-post", "claim-2", "--sources", "src-a"); code != 0 || len(res.Diagnostics) != 0 {
		t.Fatalf("repair: code = %d, %+v", code, res.Diagnostics)
	}
}

func TestSourceCommandsText(t *testing.T) {
	dir := newWorkspace(t)
	runIn(t, "", "new", "minimal", "my-post", "--dir", dir)
	for _, tc := range []struct {
		input string
		args  []string
		want  string
	}{
		{"", []string{"source", "list"}, "还没有登记素材"},
		{"访谈", []string{"source", "add", "--type", "interview", "--id", "talk", "--title", "访谈"}, "已登记素材（Source）talk"},
		{"", []string{"source", "list"}, "talk（interview） 访谈"},
		{"正文", []string{"draft", "write", "my-post"}, "已写入单元（Unit）my-post"},
		{"正文", []string{"draft", "write", "my-post"}, "正文未变化"},
		{"", []string{"provenance", "my-post"}, "Agent（agent）：1 段"},
		{"", []string{"claim", "add", "my-post", "--kind", "case", "--text", "某客户", "--sources", "talk"}, "关联素材 talk"},
		{"", []string{"claim", "add", "my-post", "--kind", "case", "--text", "另一客户"}, "尚未关联素材"},
		{"", []string{"claim", "set", "my-post", "claim-2", "--sources", "talk"}, "已修改单元（Unit）my-post 的论断（Claim）claim-2"},
		{"", []string{"claim", "set", "my-post", "claim-2", "--sources", "talk"}, "取值未变，未修改"},
		{"", []string{"claim", "list", "my-post"}, "claim-1（case）某客户：关联素材 talk"},
		{"", []string{"claim", "remove", "my-post", "claim-2"}, "已删除单元（Unit）my-post 的论断（Claim）claim-2"},
		{"", []string{"new", "minimal", "other"}, ""},
		{"", []string{"claim", "list", "other"}, "单元（Unit）other 还没有登记论断（Claim）"},
	} {
		code, stdout, stderr := runIn(t, tc.input, append(tc.args, "--dir", dir)...)
		if code != 0 || !strings.Contains(stdout, tc.want) || stderr != "" {
			t.Errorf("%v: code = %d, stdout = %q, stderr = %q", tc.args, code, stdout, stderr)
		}
	}
	for _, args := range [][]string{{"source", "add", "--type", "bad"}, {"draft", "write", "my-post"}, {"provenance", "nope"}, {"claim", "add", "nope"}} {
		if code, stdout, _ := runIn(t, "", append(args, "--dir", dir)...); code != 1 || stdout != "" {
			t.Errorf("%v: code = %d, stdout = %q", args, code, stdout)
		}
	}
}

// currentBase 返回作品 my-post 当前 draft.md 的 SHA-256，供作者改过正文后用 --base 写入。
func currentBase(t *testing.T, dir string) string {
	t.Helper()
	return sha(readFile(t, wsPath(dir, ".writing-anything/works/my-post/draft.md")))
}

// 留痕例外通道：写入校验拒绝时幂等生成阻塞决策；作者允许后 --decision 授权并把决策 id 记在论断上；
// 作者保持不变后不再追加；--decision 指向不匹配/未决定的决策时拒绝并说明原因。
func TestClaimKindOverrideDecision(t *testing.T) {
	dir := briefWork(t)
	runSrc(t, dir, "报道", "source", "add", "--type", "reference", "--id", "news")
	rel := ".writing-anything/works/my-post/claims.yaml"
	// 第一次拒绝：生成阻塞决策；第二次同输入：不重复追加。
	code, res := runSrc(t, dir, "", "claim", "add", "my-post", "--kind", "data", "--text", "外部数据", "--sources", "news")
	d := diag(t, res.result, "claim.needs-author-source")
	if code != 1 || !strings.Contains(d.Message, "已生成阻塞决策 dec-1") || !strings.Contains(d.Message, "--decision dec-1") {
		t.Fatalf("first reject: code = %d, diag = %+v", code, d)
	}
	if !slices.Contains(res.ChangedPaths, ".writing-anything/decisions.yaml") {
		t.Fatalf("changedPaths = %v", res.ChangedPaths)
	}
	if _, err := os.Stat(wsPath(dir, rel)); err == nil {
		t.Fatal("rejected claim wrote claims.yaml")
	}
	code, res = runSrc(t, dir, "", "claim", "add", "my-post", "--kind", "data", "--text", "外部数据", "--sources", "news")
	if d := diag(t, res.result, "claim.needs-author-source"); code != 1 || !strings.Contains(d.Message, "等待作者决定") ||
		len(res.ChangedPaths) != 0 {
		t.Fatalf("second reject: code = %d, changed = %v, diag = %+v", code, res.ChangedPaths, d)
	}
	// 队列中的决策是阻塞的且选项为 允许/保持不变；问题为规格固定的文本（逐字断言）。
	_, res2 := runDec(t, dir, "decisions", "--all")
	wantQ := "论断（Claim）my-post 以 data 登记「外部数据」只关联了非作者亲历素材，允许吗？"
	if len(res2.Blocking) != 1 || res2.Blocking[0].ID != "dec-1" || res2.Blocking[0].Question != wantQ {
		t.Fatalf("blocking decision: %+v, want question %q", res2.Blocking, wantQ)
	}
	// --decision 指向未决定的决策：拒绝。
	code, res = runSrc(t, dir, "", "claim", "add", "my-post", "--kind", "data", "--text", "外部数据", "--sources", "news", "--decision", "dec-1")
	if d := diag(t, res.result, "claim.needs-author-source"); code != 1 || !strings.Contains(d.Message, "尚未由作者决定") {
		t.Fatalf("undecided decision: code = %d, diag = %+v", code, d)
	}
	// 作者允许后 --decision 成功：决策 id 记在论断上。
	runDec(t, dir, "decide", "dec-1", "--option", "a", "--confirmation", "作者：允许这条外部数据")
	code, res = runSrc(t, dir, "", "claim", "add", "my-post", "--kind", "data", "--text", "外部数据", "--sources", "news", "--decision", "dec-1")
	if code != 0 || res.Claim["decision"] != "dec-1" {
		t.Fatalf("authorized: code = %d, claim = %v, %v", code, res.Claim, res.Diagnostics)
	}
	if got := readFile(t, wsPath(dir, rel)); !strings.Contains(got, "decision: dec-1") {
		t.Fatalf("claims.yaml:\n%s", got)
	}
	// 已授权的论断 lint 仍按 claim-sources 报告（例外只解除写入门槛，不静音文本检查）。
	writeFile(t, wsPath(dir, ".writing-anything/works/my-post/brief.yaml"), completeBrief)
	runWS(t, dir, "advance", "my-post")
	if code, _, _ := runIn(t, "外部数据提升 30%。", "draft", "write", "my-post", "--dir", dir); code != 0 {
		t.Fatal("draft write failed")
	}
	if code, lres := runWS(t, dir, "lint", "my-post"); code != 1 || !strings.Contains(lres.stdout, "claim-1") {
		t.Fatalf("lint after authorize: code = %d, stdout = %s", code, lres.stdout)
	}
	// 已授权后再来一条不同论断：问题文本不同，生成新的阻塞决策。
	code, res = runSrc(t, dir, "", "claim", "add", "my-post", "--kind", "data", "--text", "另一条外部数据", "--sources", "news")
	if d := diag(t, res.result, "claim.needs-author-source"); code != 1 || !strings.Contains(d.Message, "dec-2") {
		t.Fatalf("new question: code = %d, diag = %+v", code, d)
	}
	// 作者保持不变后：不带 --decision 再试不追加新决策；带该 id 的 --decision 被拒。
	runDec(t, dir, "decide", "dec-2", "--option", "b", "--confirmation", "作者：这条改用 fact")
	code, res = runSrc(t, dir, "", "claim", "add", "my-post", "--kind", "data", "--text", "另一条外部数据", "--sources", "news")
	if d := diag(t, res.result, "claim.needs-author-source"); code != 1 || !strings.Contains(d.Message, "保持不变") || len(res.ChangedPaths) != 0 {
		t.Fatalf("kept: code = %d, changed = %v, diag = %+v", code, res.ChangedPaths, d)
	}
	code, res = runSrc(t, dir, "", "claim", "add", "my-post", "--kind", "data", "--text", "另一条外部数据", "--sources", "news", "--decision", "dec-2")
	if d := diag(t, res.result, "claim.needs-author-source"); code != 1 || !strings.Contains(d.Message, "保持不变") {
		t.Fatalf("kept with decision: code = %d, diag = %+v", code, d)
	}
	// claim set --kind 的同构通道：fact 论断改为 data 被拒并生成决策（changedPaths 如实列出决策队列）；
	// 问题文本以「<单元>/<论断 id> 改为 <kind>」标识。允许后 --decision 成功。
	code, res = runSrc(t, dir, "", "claim", "set", "my-post", "claim-1", "--kind", "case")
	if d := diag(t, res.result, "claim.needs-author-source"); code != 1 || !strings.Contains(d.Message, "dec-3") {
		t.Fatalf("set kind reject: code = %d, diag = %+v", code, d)
	}
	if !slices.Contains(res.ChangedPaths, ".writing-anything/decisions.yaml") {
		t.Fatalf("set reject changedPaths = %v（应含决策队列）", res.ChangedPaths)
	}
	_, res2b := runDec(t, dir, "decisions", "--all")
	wantSetQ := "论断（Claim）my-post/claim-1 改为 case 且只关联了非作者亲历素材，允许吗？"
	found := false
	for _, b := range res2b.Blocking {
		if b.ID == "dec-3" && b.Question == wantSetQ {
			found = true
		}
	}
	if !found {
		t.Fatalf("set question text: want %q, blocking = %+v", wantSetQ, res2b.Blocking)
	}
	// --decision 用在未触发拒绝的输入上是参数错误，不写入。
	code, res = runSrc(t, dir, "", "claim", "set", "my-post", "claim-1", "--text", "外部数据（改写）", "--decision", "dec-1")
	if d := diag(t, res.result, "input.invalid-value"); code != 1 || d.Field != "decision" {
		t.Fatalf("decision without reject: code = %d, diag = %+v", code, d)
	}
	runDec(t, dir, "decide", "dec-3", "--option", "a", "--confirmation", "作者：允许改为 case")
	code, res = runSrc(t, dir, "", "claim", "set", "my-post", "claim-1", "--kind", "case", "--decision", "dec-3")
	if code != 0 || res.Claim["kind"] != "case" || res.Claim["decision"] != "dec-3" {
		t.Fatalf("set authorized: code = %d, claim = %v, %v", code, res.Claim, res.Diagnostics)
	}
}
