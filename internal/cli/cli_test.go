package cli

import (
	"bytes"
	"encoding/json"
	"errors"
	"fmt"
	"path/filepath"
	"slices"
	"sort"
	"strings"
	"testing"

	"github.com/kw12121212/writing-anything/internal/version"
)

func run(t *testing.T, args ...string) (code int, stdout, stderr string) {
	t.Helper()
	return runIn(t, "", args...)
}

// runIn 以 input 作为标准输入运行命令。
func runIn(t *testing.T, input string, args ...string) (code int, stdout, stderr string) {
	t.Helper()
	var out, errOut bytes.Buffer
	code = Run(args, strings.NewReader(input), &out, &errOut)
	return code, out.String(), errOut.String()
}

// runJSON 运行命令并把标准输出解析为 JSON 对象。
func runJSON(t *testing.T, args ...string) (int, map[string]any) {
	t.Helper()
	code, stdout, stderr := run(t, args...)
	if stderr != "" {
		t.Fatalf("%v: stderr = %q, want empty", args, stderr)
	}
	var got map[string]any
	if err := json.Unmarshal([]byte(stdout), &got); err != nil {
		t.Fatalf("%v: stdout is not a JSON object: %v; stdout: %q", args, err, stdout)
	}
	if strings.Count(stdout, "\n") != 1 || !strings.HasSuffix(stdout, "\n") {
		t.Fatalf("%v: stdout should be exactly one JSON line: %q", args, stdout)
	}
	return code, got
}

func keys(m map[string]any) []string {
	var ks []string
	for k := range m {
		ks = append(ks, k)
	}
	sort.Strings(ks)
	return ks
}

func TestVersionText(t *testing.T) {
	code, stdout, stderr := run(t, "version")
	if code != 0 {
		t.Fatalf("exit code = %d, want 0; stderr: %s", code, stderr)
	}
	v, c := version.Info()
	if want := "wa-cli " + v + " (commit " + c + ")\n"; stdout != want {
		t.Fatalf("stdout = %q, want %q", stdout, want)
	}
	if stderr != "" {
		t.Fatalf("stderr = %q, want empty", stderr)
	}
}

func TestVersionJSON(t *testing.T) {
	code, got := runJSON(t, "version", "--json")
	if code != 0 {
		t.Fatalf("exit code = %d, want 0", code)
	}
	if want := []string{"changedPaths", "commit", "diagnostics", "valid", "version"}; !slices.Equal(keys(got), want) {
		t.Fatalf("keys = %v, want %v", keys(got), want)
	}
	v, c := version.Info()
	if got["valid"] != true || got["version"] != v || got["commit"] != c {
		t.Fatalf("got %v, want valid true, version %q and commit %q", got, v, c)
	}
	for _, k := range []string{"diagnostics", "changedPaths"} {
		if arr, ok := got[k].([]any); !ok || len(arr) != 0 {
			t.Fatalf("%s = %#v, want empty array", k, got[k])
		}
	}
}

func TestUsageErrorsText(t *testing.T) {
	tests := []struct {
		name       string
		args       []string
		wantStderr []string
	}{
		{"no command", nil, []string{"缺少命令", "用法：wa-cli <command>"}},
		{"leading flag", []string{"-json"}, []string{"缺少命令", "用法：wa-cli <command>"}},
		{"unknown command", []string{"frobnicate"}, []string{"未知命令：frobnicate", "用法：wa-cli <command>"}},
		{"unknown flag", []string{"version", "--unknown"}, []string{"未知参数：--unknown", "用法：wa-cli version"}},
		{"single dash flag", []string{"version", "-json"}, []string{"未知参数：-json", "用法：wa-cli version"}},
		{"bare double dash", []string{"version", "--"}, []string{"未知参数：--", "用法：wa-cli version"}},
		{"bool flag with value", []string{"version", "--json=maybe"}, []string{"参数 --json 不接受取值：--json=maybe", "用法：wa-cli version"}},
		{"unexpected argument", []string{"version", "extra"}, []string{"多余的参数：extra", "用法：wa-cli version"}},
		{"help extra argument", []string{"help", "version", "extra"}, []string{"多余的参数：extra", "用法：wa-cli help"}},
		{"help unknown command", []string{"help", "nope"}, []string{"未知命令：nope", "用法：wa-cli <command>"}},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			code, stdout, stderr := run(t, tt.args...)
			if code != 2 {
				t.Fatalf("exit code = %d, want 2", code)
			}
			if stdout != "" {
				t.Fatalf("stdout = %q, want empty", stdout)
			}
			for _, want := range tt.wantStderr {
				if !strings.Contains(stderr, want) {
					t.Errorf("stderr %q does not contain %q", stderr, want)
				}
			}
		})
	}
}

func TestUsageErrorsJSON(t *testing.T) {
	tests := []struct {
		args        []string
		wantCode    string
		wantMessage string
		wantNext    string
	}{
		{[]string{"--json"}, "usage.missing-command", "缺少命令", "wa-cli help --json"},
		{[]string{"nope", "--json"}, "usage.unknown-command", "未知命令：nope", "wa-cli help --json"},
		{[]string{"version", "--unknown", "--json"}, "usage.unknown-flag", "未知参数：--unknown", "wa-cli help version --json"},
		{[]string{"version", "--help=yes", "--json"}, "usage.flag-takes-no-value", "参数 --help 不接受取值：--help=yes", "wa-cli help version --json"},
		{[]string{"version", "extra", "--json"}, "usage.unexpected-argument", "多余的参数：extra", "wa-cli help version --json"},
		{[]string{"help", "nope", "--json"}, "usage.unknown-command", "未知命令：nope", "wa-cli help --json"},
		{[]string{"help", "nope", "--help", "--json"}, "usage.unknown-command", "未知命令：nope", "wa-cli help --json"},
		{[]string{"--help", "--unknown", "--json"}, "usage.unknown-flag", "未知参数：--unknown", "wa-cli help help --json"},
	}
	for _, tt := range tests {
		t.Run(strings.Join(tt.args, " "), func(t *testing.T) {
			code, got := runJSON(t, tt.args...)
			if code != 2 {
				t.Fatalf("exit code = %d, want 2", code)
			}
			if got["valid"] != false {
				t.Fatalf("valid = %v, want false", got["valid"])
			}
			diags := got["diagnostics"].([]any)
			if len(diags) != 1 {
				t.Fatalf("diagnostics = %v, want exactly one", diags)
			}
			d := diags[0].(map[string]any)
			if d["code"] != tt.wantCode || d["severity"] != "error" || d["message"] != tt.wantMessage {
				t.Fatalf("diagnostic = %v, want code %q, severity error, message %q", d, tt.wantCode, tt.wantMessage)
			}
			if next := d["next"].([]any); len(next) != 1 || next[0] != tt.wantNext {
				t.Fatalf("next = %v, want [%q]", next, tt.wantNext)
			}
			if _, ok := d["path"]; ok {
				t.Fatalf("usage diagnostic should omit path: %v", d)
			}
		})
	}
}

func TestHelpText(t *testing.T) {
	code, stdout, stderr := run(t, "help")
	if code != 0 || stderr != "" {
		t.Fatalf("exit code = %d, stderr = %q; want 0 and empty", code, stderr)
	}
	for _, c := range registry {
		if !strings.Contains(stdout, c.name) || !strings.Contains(stdout, c.summary) {
			t.Errorf("help does not list %s with its summary:\n%s", c.name, stdout)
		}
	}
}

func TestCommandHelp(t *testing.T) {
	_, byHelp, _ := run(t, "help", "version")
	code, byFlag, stderr := run(t, "version", "--help")
	if code != 0 || stderr != "" {
		t.Fatalf("version --help: exit code = %d, stderr = %q", code, stderr)
	}
	if byFlag != byHelp {
		t.Fatalf("version --help = %q, help version = %q; want equal", byFlag, byHelp)
	}
	for _, want := range []string{"用法：wa-cli version [--json] [--help]", "--json", "输出键（--json）", "version", "退出码", "用法错误"} {
		if !strings.Contains(byFlag, want) {
			t.Errorf("version help does not contain %q:\n%s", want, byFlag)
		}
	}
	_, helpHelp, _ := run(t, "help", "help")
	if !strings.Contains(helpHelp, "位置参数") || !strings.Contains(helpHelp, "<command>") {
		t.Errorf("help help does not describe the positional argument:\n%s", helpHelp)
	}
}

// helpDocs 运行 help --json 类命令并解码 commands 数组。
func helpDocs(t *testing.T, args ...string) []commandDoc {
	t.Helper()
	var out, errOut bytes.Buffer
	if code := Run(args, strings.NewReader(""), &out, &errOut); code != 0 {
		t.Fatalf("%v: exit code = %d; stderr: %s", args, code, errOut.String())
	}
	var res struct {
		Valid    bool         `json:"valid"`
		Commands []commandDoc `json:"commands"`
	}
	if err := json.Unmarshal(out.Bytes(), &res); err != nil {
		t.Fatalf("%v: %v", args, err)
	}
	if !res.Valid {
		t.Fatalf("%v: valid = false", args)
	}
	return res.Commands
}

func TestHelpJSON(t *testing.T) {
	docs := helpDocs(t, "help", "--json")
	if len(docs) != len(registry) {
		t.Fatalf("commands = %d, want %d", len(docs), len(registry))
	}
	for i, d := range docs {
		if d.Name != registry[i].name || d.Summary == "" || d.Usage == "" {
			t.Errorf("command %d = %+v", i, d)
		}
		if d.Args == nil || d.Flags == nil || d.OutputKeys == nil || d.ExitCodes == nil {
			t.Errorf("%s: arrays must not be null: %+v", d.Name, d)
		}
		var flags []string
		for _, f := range d.Flags {
			flags = append(flags, f.Name)
		}
		if !slices.Contains(flags, "json") || !slices.Contains(flags, "help") {
			t.Errorf("%s: flags %v must include json and help", d.Name, flags)
		}
	}

	one := helpDocs(t, "help", "version", "--json")
	if len(one) != 1 || one[0].Name != "version" {
		t.Fatalf("help version --json = %+v, want only version", one)
	}
	if !slices.ContainsFunc(one[0].OutputKeys, func(k outputKey) bool { return k.Name == "version" }) {
		t.Fatalf("version outputKeys %v do not include version", one[0].OutputKeys)
	}
	if byFlag := helpDocs(t, "version", "--help", "--json"); !slices.EqualFunc(byFlag, one, func(a, b commandDoc) bool { return a.Name == b.Name && a.Usage == b.Usage }) {
		t.Fatalf("version --help --json = %+v, want %+v", byFlag, one)
	}
}

// nullableFields 是命令样例 --json 输出中允许为 null 的字段名清单（对象字段）；数组字段一律不得为 null，
// 没有元素时必须是空数组（cli-contract/shared-envelope）。新增合法可空的字段时在此登记。
var nullableFields = map[string]bool{
	"acceptance":       true,
	"aiDisclosure":     true,
	"answer":           true,
	"brief":            true,
	"choice":           true,
	"column":           true,
	"commit":           true,
	"confirmation":     true,
	"contextBudget":    true,
	"cta":              true,
	"decidedAt":        true,
	"decision":         true,
	"defaultGenre":     true,
	"digest":           true,
	"feedback":         true,
	"genre":            true,
	"headline":         true,
	"headlineDecision": true,
	"imageList":        true,
	"key":              true,
	"length":           true,
	"line":             true,
	"lock":             true,
	"median":           true,
	"method":           true,
	"note":             true,
	"opening":          true,
	"outline":          true,
	"paragraph":        true,
	"path":             true,
	"pov":              true,
	"premise":          true,
	"profile":          true,
	"progress":         true,
	"quote":            true,
	"rate":             true,
	"rationale":        true,
	"recommendation":   true,
	"recorded":         true,
	"ref":              true,
	"reveal":           true,
	"review":           true,
	"role":             true,
	"rubric":           true,
	"section":          true,
	"series":           true,
	"spread":           true,
	"stats":            true,
	"styleProfile":     true,
	"suggestion":       true,
	"summary":          true,
	"target":           true,
	"text":             true,
	"theme":            true,
	"title":            true,
	"url":              true,
	"value":            true,
	"waiver":           true,
}

// assertNoUnexpectedNulls 递归断言 v 中没有清单之外的 null：数组字段为 null 或未登记的可空字段都会被检出。
func assertNoUnexpectedNulls(t *testing.T, path string, v any) {
	t.Helper()
	switch x := v.(type) {
	case map[string]any:
		for k, item := range x {
			if item == nil {
				if !nullableFields[k] {
					t.Errorf("%s.%s = null：数组字段必须为空数组，未登记的可空字段不允许为 null（shared-envelope）", path, k)
				}
				continue
			}
			assertNoUnexpectedNulls(t, path+"."+k, item)
		}
	case []any:
		for i, item := range x {
			assertNoUnexpectedNulls(t, fmt.Sprintf("%s[%d]", path, i), item)
		}
	}
}

// TestRegistryConsistency 确认 help --json 的声明与命令的实际行为一致。
func TestRegistryConsistency(t *testing.T) {
	for _, d := range helpDocs(t, "help", "--json") {
		t.Run(d.Name, func(t *testing.T) {
			// 输出键与 --json 的实际顶层字段一致。
			code, got := runJSON(t, sampleArgs(t, d.Name)...)
			assertNoUnexpectedNulls(t, d.Name, got)
			if code != 0 {
				t.Fatalf("exit code = %d", code)
			}
			var declared []string
			for _, k := range d.OutputKeys {
				if k.Description == "" {
					t.Errorf("output key %s has no description", k.Name)
				}
				declared = append(declared, k.Name)
			}
			sort.Strings(declared)
			if !slices.Equal(keys(got), declared) {
				t.Errorf("JSON keys = %v, declared outputKeys = %v", keys(got), declared)
			}
			if got["valid"] != true {
				t.Errorf("valid = %v, want true", got["valid"])
			}
			if arr, ok := got["diagnostics"].([]any); !ok || len(arr) != 0 {
				t.Errorf("diagnostics = %#v, want empty array", got["diagnostics"])
			}
			// 只能通过变更产物才能成功的命令，其样例变更了临时工作区中的产物（cli-contract/sample-invocation）。
			if arr, ok := got["changedPaths"].([]any); !ok || (len(arr) != 0) != mutatingSamples[d.Name] {
				t.Errorf("changedPaths = %#v, want %s array", got["changedPaths"], map[bool]string{true: "a non-empty", false: "an empty"}[mutatingSamples[d.Name]])
			}
			// 声明的每个参数都被解析器接受。
			c := registry.lookup(d.Name)
			var required []string
			for i, a := range d.Args {
				if a.Description == "" {
					t.Errorf("argument <%s> has no description", a.Name)
				}
				if a.Required {
					// 必需参数只能排在可选参数之前。
					if i != len(required) {
						t.Errorf("required argument <%s> follows an optional argument", a.Name)
					}
					required = append(required, "x")
				}
			}
			for _, f := range d.Flags {
				if f.Description == "" {
					t.Errorf("flag --%s has no description", f.Name)
				}
				args := append(slices.Clip(required), "--"+f.Name)
				if f.Type == flagString || f.Type == flagList {
					args = append(args, "x")
				}
				if _, diag := parseArgs(c, args); diag != nil {
					t.Errorf("declared flag --%s rejected: %v", f.Name, diag.Message)
				}
			}
			// 退出码只取 0、1、2 且都有含义。
			if len(d.ExitCodes) == 0 {
				t.Errorf("no exit codes declared")
			}
			for _, e := range d.ExitCodes {
				if e.Code < 0 || e.Code > 2 || e.Meaning == "" {
					t.Errorf("exit code %+v outside the 0/1/2 convention", e)
				}
			}
		})
	}
}

// mutatingSamples 列出只能通过变更产物才能成功的命令；它们的样例在临时工作区中变更产物。
var mutatingSamples = map[string]bool{"new": true, "unit add": true, "unit move": true, "advance": true, "source add": true, "claim add": true, "claim remove": true, "review record": true, "accept": true, "export": true, "preview": true, "marketing ask": true, "mcp install": true, "skills install": true, "feedback add": true, "decision add": true, "decide": true, "outline remove": true, "pack install": true, "pack remove": true, "metrics record": true, "metrics calibrate": true, "pack learn": true, "series add": true, "series fact": true, "summary set": true, "pack confirm": true, "pack reject": true, "canon add": true, "canon remove": true}

// sampleArgs 返回命令的一次成功调用：除 mutatingSamples 中的命令外都不变更任何产物；工作区命令在已初始化的
// 临时工作区中运行。
func sampleArgs(t *testing.T, name string) []string {
	t.Helper()
	switch name {
	case "new":
		return []string{"new", "minimal", "my-post", "--dir", newWorkspace(t), "--json"}
	case "unit add":
		return []string{"unit", "add", "my-post", "section", "intro", "--dir", newWork(t), "--json"}
	case "unit move":
		dir := newWork(t)
		run(t, "unit", "add", "my-post", "section", "s1", "--dir", dir)
		run(t, "unit", "add", "my-post", "section", "s2", "--dir", dir)
		return []string{"unit", "move", "my-post/s1", "my-post", "--after", "s2", "--dir", dir, "--json"}
	case "advance":
		dir := newWork(t)
		writeFile(t, filepath.Join(dir, ".writing-anything", "works", "my-post", "brief.yaml"), completeBrief)
		return []string{"advance", "my-post", "--dir", dir, "--json"}
	case "next":
		return []string{"next", "--dir", newWork(t), "--json"}
	case "source add":
		dir := newWorkspace(t)
		writeFile(t, filepath.Join(dir, "idea.md"), "作者笔记")
		return []string{"source", "add", "--type", "author-note", "--file", "idea.md", "--dir", dir, "--json"}
	case "source list":
		return []string{"source", "list", "--dir", newWorkspace(t), "--json"}
	case "draft write":
		// 同样的内容再次写入不变更产物（cli-contract/sample-invocation）。
		dir := newWork(t)
		writeFile(t, filepath.Join(dir, "draft.md"), "正文")
		args := []string{"draft", "write", "my-post", "--file", "draft.md", "--dir", dir, "--json"}
		if code, stdout, _ := run(t, args...); code != 0 {
			t.Fatalf("draft write: exit code = %d; %s", code, stdout)
		}
		return args
	case "diff":
		dir := newWork(t)
		writeFile(t, filepath.Join(dir, ".writing-anything", "works", "my-post", "draft.md"), "作者写的")
		return []string{"diff", "my-post", "--dir", dir, "--json"}
	case "provenance":
		dir := newWork(t)
		writeFile(t, filepath.Join(dir, ".writing-anything", "works", "my-post", "draft.md"), "正文")
		return []string{"provenance", "my-post", "--dir", dir, "--json"}
	case "feedback add":
		return []string{"feedback", "add", "my-post", "--text", "整体太长", "--dir", newWork(t), "--json"}
	case "feedback list":
		return []string{"feedback", "list", "--dir", newWork(t), "--json"}
	case "feedback resolve":
		// 状态未变时不变更产物。
		dir := newWork(t)
		run(t, "feedback", "add", "my-post", "--text", "x", "--dir", dir)
		run(t, "feedback", "resolve", "my-post", "fb-1", "--dir", dir)
		return []string{"feedback", "resolve", "my-post", "fb-1", "--dir", dir, "--json"}
	case "marketing set":
		// 取值未变时不变更产物。
		dir := newWork(t)
		writeFile(t, filepath.Join(dir, ".writing-anything", "works", "my-post", "marketing.yaml"), "schema_version: 1\ndigest: 一篇短文\n")
		return []string{"marketing", "set", "my-post", "--digest", "一篇短文", "--dir", dir, "--json"}
	case "marketing show":
		dir := newWork(t)
		run(t, "marketing", "set", "my-post", "--digest", "一篇短文", "--dir", dir)
		return []string{"marketing", "show", "my-post", "--dir", dir, "--json"}
	case "marketing ask":
		dir := newWork(t)
		run(t, "marketing", "set", "my-post", "--headline", "h1", "--headline-text", "甲", "--dir", dir)
		run(t, "marketing", "set", "my-post", "--headline", "h2", "--headline-text", "乙", "--dir", dir)
		return []string{"marketing", "ask", "my-post", "--dir", dir, "--json"}
	case "mcp install":
		return []string{"mcp", "install", "--client", "json", "--config", "mcp.json", "--dir", t.TempDir(), "--json"}
	case "mcp":
		// 标准输入为空：没有消息，退出前写出汇总信封。
		return []string{"mcp", "--dir", newWorkspace(t), "--json"}
	case "preview":
		return []string{"preview", "my-post", "--dir", newWork(t), "--json"}
	case "feedback watch":
		dir := newWork(t)
		run(t, "feedback", "add", "my-post", "--text", "x", "--dir", dir)
		return []string{"feedback", "watch", "--all", "--max", "1", "--dir", dir, "--json"}
	case "decision add":
		return []string{"decision", "add", "--question", "标题用哪个", "--option", "甲", "--option", "乙", "--unit", "my-post", "--blocking", "--dir", newWork(t), "--json"}
	case "decisions":
		dir := newWork(t)
		run(t, "decision", "add", "--question", "q", "--dir", dir)
		return []string{"decisions", "--all", "--dir", dir, "--json"}
	case "decide":
		dir := newWork(t)
		run(t, "decision", "add", "--question", "q", "--option", "甲", "--dir", dir)
		return []string{"decide", "dec-1", "--option", "a", "--confirmation", "作者同意", "--dir", dir, "--json"}
	case "brief set":
		// 取值未变时不变更产物（cli-contract/sample-invocation）。
		dir := newWork(t)
		writeFile(t, filepath.Join(dir, ".writing-anything", "works", "my-post", "brief.yaml"), completeBrief)
		return []string{"brief", "set", "my-post", "--audience", "后端工程师", "--dir", dir, "--json"}
	case "brief show":
		return []string{"brief", "show", "my-post", "--dir", newWork(t), "--json"}
	case "outline set":
		// 取值未变时不变更产物。
		dir := newWork(t)
		writeFile(t, filepath.Join(dir, ".writing-anything", "works", "my-post", "outline.yaml"), "schema_version: 1\nsections:\n  - {id: why, title: 为什么, reason: 引出问题}\n")
		return []string{"outline", "set", "my-post", "why", "--title", "为什么", "--dir", dir, "--json"}
	case "outline remove":
		dir := newWork(t)
		writeFile(t, filepath.Join(dir, ".writing-anything", "works", "my-post", "outline.yaml"), "schema_version: 1\nsections:\n  - {id: why, title: 为什么, reason: 引出问题}\n")
		return []string{"outline", "remove", "my-post", "why", "--dir", dir, "--json"}
	case "outline show":
		return []string{"outline", "show", "my-post", "--dir", newWork(t), "--json"}
	case "lint":
		dir := newWork(t)
		writeFile(t, filepath.Join(dir, ".writing-anything", "works", "my-post", "draft.md"), "正文。")
		return []string{"lint", "my-post", "--dir", dir, "--json"}
	case "context":
		return []string{"context", "my-post", "--dir", newWork(t), "--json"}
	case "claim add":
		return []string{"claim", "add", "my-post", "--kind", "opinion", "--text", "观点", "--dir", newWork(t), "--json"}
	case "claim set":
		// 取值未变时不变更产物。
		dir := newWork(t)
		run(t, "claim", "add", "my-post", "--kind", "opinion", "--text", "观点", "--dir", dir)
		return []string{"claim", "set", "my-post", "claim-1", "--kind", "opinion", "--dir", dir, "--json"}
	case "claim list":
		return []string{"claim", "list", "my-post", "--dir", newWork(t), "--json"}
	case "claim remove":
		dir := newWork(t)
		run(t, "claim", "add", "my-post", "--kind", "opinion", "--text", "观点", "--dir", dir)
		return []string{"claim", "remove", "my-post", "claim-1", "--dir", dir, "--json"}
	case "canon add":
		return []string{"canon", "add", "my-post", "--type", "character", "--name", "守灯人", "--text", "五十岁，左手有旧伤", "--dir", newWork(t), "--json"}
	case "canon set":
		// 取值未变时不变更产物。
		dir := newWork(t)
		run(t, "canon", "add", "my-post", "--type", "character", "--name", "守灯人", "--text", "五十岁", "--dir", dir)
		return []string{"canon", "set", "my-post", "canon-1", "--text", "五十岁", "--dir", dir, "--json"}
	case "canon list":
		return []string{"canon", "list", "my-post", "--dir", newWork(t), "--json"}
	case "canon remove":
		dir := newWork(t)
		run(t, "canon", "add", "my-post", "--type", "world-rule", "--text", "灯油只能燃烧到黎明", "--dir", dir)
		return []string{"canon", "remove", "my-post", "canon-1", "--dir", dir, "--json"}
	case "method list":
		return []string{"method", "list", "--dir", newWorkspace(t), "--json"}
	case "method show":
		return []string{"method", "show", "pyramid-scqa", "--dir", newWorkspace(t), "--json"}
	case "review record":
		dir := reviewWork(t)
		writeFile(t, filepath.Join(dir, "review-input.yaml"), goodReview)
		return []string{"review", "record", "my-post", "--file", "review-input.yaml", "--dir", dir, "--json"}
	case "review show":
		return []string{"review", "show", "my-post", "--dir", reviewWork(t), "--json"}
	case "review resolve":
		// 取值未变时不变更产物。
		dir := reviewWork(t)
		runIn(t, goodReview, "review", "record", "my-post", "--dir", dir)
		run(t, "review", "resolve", "my-post", "rf-1", "--dir", dir)
		return []string{"review", "resolve", "my-post", "rf-1", "--dir", dir, "--json"}
	case "accept":
		dir := reviewWork(t)
		run(t, "config", "set", "gate_level", "fast", "--dir", dir)
		return []string{"accept", "my-post", "--confirmed-by-author", "--confirmation", "作者：可以", "--dir", dir, "--json"}
	case "export":
		dir := reviewWork(t)
		run(t, "config", "set", "gate_level", "fast", "--dir", dir)
		run(t, "accept", "my-post", "--dir", dir)
		return []string{"export", "my-post", "--dir", dir, "--json"}
	case "skills list":
		return []string{"skills", "list", "--json"}
	case "skills install":
		return []string{"skills", "install", "--agent", "claude-code", "--dir", t.TempDir(), "--json"}
	case "help", "version":
		return []string{name, "--json"}
	case "init", "validate", "status":
		return []string{name, "--dir", newWorkspace(t), "--json"}
	case "config set":
		return []string{"config", "set", "gate_level", "standard", "--dir", newWorkspace(t), "--json"}
	case "config resolved":
		return []string{"config", "resolved", "--dir", newWorkspace(t), "--json"}
	case "pack install":
		dir := newWorkspace(t)
		writeFile(t, filepath.Join(dir, "house.yaml"), "schema_version: 1\nid: house\ntitle: 本团队规范\n")
		return []string{"pack", "install", "house.yaml", "--dir", dir, "--json"}
	case "metrics compute":
		return []string{"metrics", "compute", "my-post", "--dir", metricsWork(t, ""), "--json"}
	case "curve":
		return []string{"curve", "my-post", "--dir", metricsWork(t, ""), "--json"}
	case "metrics plan", "metrics show":
		return []string{"metrics", strings.Fields(name)[1], "my-post", "--dir", metricsWork(t, ""), "--json"}
	case "metrics record":
		dir := metricsWork(t, "")
		writeFile(t, filepath.Join(dir, "session.yaml"), scoreSession("s1", 3, 3, 3))
		return []string{"metrics", "record", "my-post", "--file", "session.yaml", "--dir", dir, "--json"}
	case "metrics calibrate":
		dir := metricsWork(t, "    sessions: 1\n")
		runIn(t, scoreSession("s1", 3, 3, 3), "metrics", "record", "my-post", "--dir", dir)
		return []string{"metrics", "calibrate", "my-post", "--metric", "tension", "--paragraph", "1", "--score", "3", "--confirmation", "作者：3 分", "--dir", dir, "--json"}
	case "pack learn":
		return []string{"pack", "learn", "me", "--pack", "mine", "--source", "a1", "--style", "多用短句", "--dir", learnWork(t), "--json"}
	case "pack prefer":
		return []string{"pack", "prefer", "prefs", "--pack", "mine", "--dir", newWork(t), "--json"}
	case "pack confirm", "pack reject":
		dir := learnWork(t)
		run(t, "pack", "learn", "me", "--pack", "mine", "--source", "a1", "--dir", dir)
		if name == "pack reject" {
			return []string{"pack", "reject", "me", "--item", "baseline", "--dir", dir, "--json"}
		}
		return []string{"pack", "confirm", "me", "--item", "baseline", "--confirmation", "作者：可以", "--dir", dir, "--json"}
	case "series set":
		// 取值未变时不变更产物。
		dir := newWorkspace(t)
		run(t, "series", "set", "guide", "--title", "入门指南", "--dir", dir)
		return []string{"series", "set", "guide", "--title", "入门指南", "--dir", dir, "--json"}
	case "series add":
		dir := newWork(t)
		run(t, "series", "set", "guide", "--dir", dir)
		return []string{"series", "add", "guide", "my-post", "--dir", dir, "--json"}
	case "series fact":
		dir := newWorkspace(t)
		run(t, "series", "set", "guide", "--dir", dir)
		return []string{"series", "fact", "guide", "audience", "--text", "读者是后端工程师", "--dir", dir, "--json"}
	case "series show":
		dir := newWork(t)
		run(t, "series", "set", "guide", "--dir", dir)
		run(t, "series", "add", "guide", "my-post", "--dir", dir)
		return []string{"series", "show", "guide", "--dir", dir, "--json"}
	case "summary set":
		dir := reviewWork(t)
		run(t, "config", "set", "gate_level", "fast", "--dir", dir)
		run(t, "accept", "my-post", "--dir", dir)
		return []string{"summary", "set", "my-post", "--text", strings.Repeat("这是一篇关于规范包的文章。", 3), "--dir", dir, "--json"}
	case "summary show":
		return []string{"summary", "show", "my-post", "--dir", newWork(t), "--json"}
	case "pack list":
		return []string{"pack", "list", "--dir", newWorkspace(t), "--json"}
	case "pack remove":
		dir := newWorkspace(t)
		writeFile(t, filepath.Join(dir, "house.yaml"), "schema_version: 1\nid: house\ntitle: 本团队规范\n")
		run(t, "pack", "install", "house.yaml", "--dir", dir)
		return []string{"pack", "remove", "house", "--dir", dir, "--json"}
	}
	t.Fatalf("no sample invocation for command %q; add one to sampleArgs", name)
	return nil
}

func TestParseStringFlag(t *testing.T) {
	c := &command{name: "demo", flags: []flagSpec{{"dir", flagString, "目录"}}}
	for _, args := range [][]string{{"--dir", "a b"}, {"--dir=a b"}} {
		inv, d := parseArgs(c, args)
		if d != nil || inv.values["dir"] != "a b" {
			t.Fatalf("%v: values = %v, diag = %v", args, inv, d)
		}
	}
	if inv, d := parseArgs(c, []string{"--dir="}); d != nil || !inv.flag("dir") || inv.values["dir"] != "" {
		t.Fatalf("--dir= should set an empty value: %v %v", inv, d)
	}
	for _, args := range [][]string{{"--dir"}, {"--dir", "--json"}} {
		_, d := parseArgs(c, args)
		if d == nil || d.Code != "usage.missing-flag-value" || d.Message != "参数 --dir 缺少取值" || d.Next[0] != "wa-cli help demo --json" {
			t.Fatalf("%v: diag = %+v, want usage.missing-flag-value", args, d)
		}
	}
	if u := c.usage(); u != "wa-cli demo [--dir <value>] [--json] [--help]" {
		t.Fatalf("usage = %q", u)
	}
}

func TestWriteJSON(t *testing.T) {
	var out bytes.Buffer
	res := &Result{Diagnostics: []Diagnostic{
		{Code: "demo.warn", Severity: severityWarning, Message: "提示", Path: "a.yaml", Field: "$.x", Next: []string{"wa-cli help"}},
	}}
	if err := writeJSON(&out, res); err != nil {
		t.Fatal(err)
	}
	want := `{"valid":true,"diagnostics":[{"code":"demo.warn","severity":"warning","message":"提示","path":"a.yaml","field":"$.x","next":["wa-cli help"]}],"changedPaths":[]}` + "\n"
	if out.String() != want {
		t.Fatalf("warning only:\n got %s\nwant %s", out.String(), want)
	}

	out.Reset()
	res.Diagnostics = append(res.Diagnostics, Diagnostic{Code: "demo.err", Severity: severityError, Message: "错误", Next: []string{"wa-cli help"}})
	if err := writeJSON(&out, res); err != nil {
		t.Fatal(err)
	}
	if !strings.HasPrefix(out.String(), `{"valid":false,`) {
		t.Fatalf("an error diagnostic must make valid false: %s", out.String())
	}
}

type failingWriter struct{}

func (failingWriter) Write([]byte) (int, error) { return 0, errors.New("disk full") }

func TestWriteFailure(t *testing.T) {
	for _, args := range [][]string{{"version"}, {"version", "--json"}, {"help"}, {"help", "--json"}, {"help", "version"}} {
		var errOut bytes.Buffer
		if code := Run(args, strings.NewReader(""), failingWriter{}, &errOut); code != 1 {
			t.Fatalf("%v: exit code = %d, want 1", args, code)
		}
		if !strings.Contains(errOut.String(), "写入输出失败：disk full") {
			t.Fatalf("%v: stderr %q does not report the write error", args, errOut.String())
		}
	}
}

func TestRootHelpFlag(t *testing.T) {
	for _, tt := range [][2][]string{
		{{"--help"}, {"help"}},
		{{"--help", "--json"}, {"help", "--json"}},
		{{"--help", "version"}, {"help", "version"}},
		{{"--help", "version", "--json"}, {"help", "version", "--json"}},
	} {
		code, got, stderr := run(t, tt[0]...)
		_, want, _ := run(t, tt[1]...)
		if code != 0 || stderr != "" || got != want {
			t.Errorf("%v: exit code = %d, stderr = %q, stdout = %q; want 0, empty, output of %v", tt[0], code, stderr, got, tt[1])
		}
	}
}

func TestHelpCommandHelpFlag(t *testing.T) {
	_, want, _ := run(t, "help", "version")
	code, got, stderr := run(t, "help", "version", "--help")
	if code != 0 || stderr != "" || got != want {
		t.Fatalf("help version --help: exit code = %d, stderr = %q, stdout = %q; want output of help version", code, stderr, got)
	}
	_, own, _ := run(t, "help", "help")
	if _, got, _ := run(t, "help", "--help"); got != own {
		t.Fatalf("help --help = %q, want help's own usage %q", got, own)
	}
}

func TestUsageErrorWriteFailure(t *testing.T) {
	var errOut bytes.Buffer
	if code := Run([]string{"nope", "--json"}, strings.NewReader(""), failingWriter{}, &errOut); code != 1 {
		t.Fatalf("JSON usage error with failing stdout: exit code = %d, want 1", code)
	}
	if !strings.Contains(errOut.String(), "写入输出失败：disk full") {
		t.Fatalf("stderr %q does not report the write error", errOut.String())
	}
	if code := Run([]string{"nope"}, strings.NewReader(""), &bytes.Buffer{}, failingWriter{}); code != 1 {
		t.Fatalf("text usage error with failing stderr: exit code = %d, want 1", code)
	}
}

func TestPlaceholders(t *testing.T) {
	c := &command{name: "demo", flags: []flagSpec{{"dir", flagString, "目录"}}}
	texts := []string{registry.overview(), c.usage(), c.helpText()}
	for _, cmd := range registry {
		texts = append(texts, cmd.helpText())
	}
	for _, text := range texts {
		for _, bad := range []string{"<命令>", "<值>"} {
			if strings.Contains(text, bad) {
				t.Errorf("usage contains Chinese placeholder %s:\n%s", bad, text)
			}
		}
	}
	if !strings.Contains(registry.overview(), "用法：wa-cli <command> [<args>]") {
		t.Errorf("overview usage line = %q", registry.overview())
	}
	if !strings.Contains(c.usage(), "[--dir <value>]") {
		t.Errorf("string flag usage = %q", c.usage())
	}
}

func TestParseListFlag(t *testing.T) {
	c := &command{name: "demo", flags: []flagSpec{{"option", flagList, "选项"}}}
	inv, d := parseArgs(c, []string{"--option", "甲", "--option=乙", "--option", "丙"})
	if d != nil || !slices.Equal(inv.lists["option"], []string{"甲", "乙", "丙"}) || inv.values["option"] != "丙" {
		t.Fatalf("lists = %v, values = %v, %v", inv.lists, inv.values, d)
	}
	if !strings.Contains(c.usage(), "[--option <value>]...") {
		t.Errorf("usage = %q", c.usage())
	}
}
