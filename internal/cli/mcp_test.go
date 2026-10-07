package cli

import (
	"bufio"
	"encoding/json"
	"fmt"
	"io"
	"maps"
	"os"
	"path/filepath"
	"slices"
	"strings"
	"testing"
	"time"
)

const mcpMeta = `"_meta":{"io.modelcontextprotocol/protocolVersion":"2026-07-28","io.modelcontextprotocol/clientCapabilities":{}}`

// mcpSession 是一个在后台运行的 wa-cli mcp。
type mcpSession struct {
	t     *testing.T
	w     io.WriteCloser
	lines chan map[string]any
	done  chan int
	id    int
}

func startMCP(t *testing.T, dir string, args ...string) *mcpSession {
	t.Helper()
	in, w := io.Pipe()
	out, ow := io.Pipe()
	s := &mcpSession{t: t, w: w, lines: make(chan map[string]any, 100), done: make(chan int, 1)}
	go func() {
		s.done <- Run(append([]string{"mcp", "--dir", dir}, args...), in, ow, io.Discard)
		ow.Close()
	}()
	go func() {
		sc := bufio.NewScanner(out)
		sc.Buffer(make([]byte, 1<<20), 16<<20)
		for sc.Scan() {
			var m map[string]any
			if err := json.Unmarshal(sc.Bytes(), &m); err != nil {
				m = map[string]any{"unparsed": sc.Text()}
			}
			s.lines <- m
		}
		close(s.lines)
	}()
	t.Cleanup(func() { w.Close() })
	return s
}

func (s *mcpSession) next() map[string]any {
	s.t.Helper()
	select {
	case m, ok := <-s.lines:
		if !ok {
			s.t.Fatal("mcp closed its output")
		}
		return m
	case <-time.After(10 * time.Second):
		s.t.Fatal("no message from mcp")
		return nil
	}
}

// request 发送一个请求，返回 id 相同的响应（其间的通知交给 notes）。
func (s *mcpSession) request(method, params string, notes ...*[]map[string]any) map[string]any {
	s.t.Helper()
	s.id++
	if params != "" {
		params += ","
	}
	fmt.Fprintf(s.w, `{"jsonrpc":"2.0","id":%d,"method":%q,"params":{%s%s}}`+"\n", s.id, method, params, mcpMeta)
	for {
		m := s.next()
		if m["id"] == float64(s.id) {
			return m
		}
		if len(notes) > 0 {
			*notes[0] = append(*notes[0], m)
		}
	}
}

func (s *mcpSession) call(tool string, args map[string]any) map[string]any {
	s.t.Helper()
	b, _ := json.Marshal(args)
	m := s.request("tools/call", fmt.Sprintf(`"name":%q,"arguments":%s`, tool, b))
	r, ok := m["result"].(map[string]any)
	if !ok {
		s.t.Fatalf("tools/call %s: %v", tool, m)
	}
	return r
}

func (s *mcpSession) close() int {
	s.t.Helper()
	s.w.Close()
	select {
	case code := <-s.done:
		return code
	case <-time.After(10 * time.Second):
		s.t.Fatal("mcp did not exit")
		return 0
	}
}

// tool-per-command：注册表中除 mcp 外的每条命令都有一个 tool。
func TestMCPToolPerCommand(t *testing.T) {
	s := startMCP(t, newWorkspace(t))
	tools := map[string]map[string]any{}
	for _, it := range s.request("tools/list", "")["result"].(map[string]any)["tools"].([]any) {
		tool := it.(map[string]any)
		tools[tool["name"].(string)] = tool
	}
	for _, c := range registry {
		name := strings.ReplaceAll(c.name, " ", "_")
		if (tools[name] != nil) != !slices.Contains(mcpExcluded, c.name) {
			t.Errorf("command %s: tool %v", c.name, tools[name])
		}
	}
	// pack install 的来源可以在项目目录之外，不作为 tool（cli-contract/mcp-thin-wrapper）。
	if tools["pack_install"] != nil || tools["pack_list"] == nil || tools["pack_remove"] == nil {
		t.Errorf("pack tools: install %v, list %v, remove %v", tools["pack_install"] != nil, tools["pack_list"] != nil, tools["pack_remove"] != nil)
	}
	if len(tools) != len(registry)-len(mcpExcluded) {
		t.Errorf("%d tools for %d commands", len(tools), len(registry))
	}
	props := func(name string) map[string]any {
		return tools[name]["inputSchema"].(map[string]any)["properties"].(map[string]any)
	}
	if props("draft_write")["input"] == nil || props("draft_write")["file"] != nil || props("draft_write")["dir"] != nil || props("preview")["serve"] != nil ||
		props("preview")["output"] == nil || props("skills_install")["target"] != nil || props("skills_install")["agent"] == nil {
		t.Errorf("schemas: %v %v %v", props("draft_write"), props("preview"), props("skills_install"))
	}
	if code := s.close(); code != 0 {
		t.Fatalf("exit %d", code)
	}
}

// same-as-cli：同一操作经 MCP 与 CLI 调用，输出信封与产物相同。
func TestMCPSameAsCLI(t *testing.T) {
	d1, d2 := newWork(t), newWork(t)
	s := startMCP(t, d1)
	r := s.call("feedback_add", map[string]any{"unit": "my-post", "text": "太长"})
	got, _ := json.Marshal(r["structuredContent"])
	_, res := runWS(t, d2, "feedback", "add", "my-post", "--text", "太长")
	var want map[string]any
	json.Unmarshal([]byte(res.stdout), &want)
	wantJSON, _ := json.Marshal(want)
	if r["isError"] != false || string(got) != string(wantJSON) {
		t.Fatalf("envelope: %s vs %s", got, wantJSON)
	}
	if a, b := readFile(t, wsPath(d1, ".writing-anything/works/my-post/feedback.yaml")), readFile(t, wsPath(d2, ".writing-anything/works/my-post/feedback.yaml")); a != b {
		t.Fatalf("feedback.yaml differs:\n%s\n%s", a, b)
	}
	// 读取标准输入的命令：input 作为标准输入；流式命令返回 events。
	if r := s.call("draft_write", map[string]any{"unit": "my-post", "input": "正文。\n"}); r["isError"] != false || readFile(t, wsPath(d1, draftRel)) != "正文。\n" {
		t.Fatalf("draft_write: %v", r)
	}
	// 不接受 --dir 的命令（version、help、skills list）同样可以调用。
	for _, name := range []string{"version", "help", "skills_list"} {
		if r := s.call(name, map[string]any{}); r["isError"] != false {
			t.Fatalf("%s: %v", name, r)
		}
	}
	// tool-errors：命令失败时 isError 为 true；不存在的 tool 与未声明的参数返回 -32602。
	if r := s.call("unit_add", map[string]any{"parent": "nope", "type": "section", "id": "x"}); r["isError"] != true {
		t.Fatalf("failing command: %v", r)
	}
	for _, params := range []string{`"name":"nope"`, `"name":"feedback_add","arguments":{"unit":"my-post","color":"red"}`} {
		if m := s.request("tools/call", params); m["error"].(map[string]any)["code"] != -32602.0 {
			t.Fatalf("%s: %v", params, m)
		}
	}
	s.close()
}

func TestMCPResourcesAndPrompts(t *testing.T) {
	dir := newWork(t)
	writeFile(t, wsPath(dir, draftRel), "作者的正文。\n")
	s := startMCP(t, dir)
	uris := map[string]bool{}
	for _, it := range s.request("resources/list", "")["result"].(map[string]any)["resources"].([]any) {
		uris[it.(map[string]any)["uri"].(string)] = true
	}
	for _, want := range []string{"wa://resolved", "wa://feedback", "wa://file/.writing-anything/config.yaml", "wa://file/.writing-anything/works/my-post/work.yaml",
		"wa://file/.writing-anything/works/my-post/draft.md"} {
		if !uris[want] {
			t.Errorf("resources lack %s: %v", want, uris)
		}
	}
	// read-draft
	c := s.request("resources/read", `"uri":"wa://file/.writing-anything/works/my-post/draft.md"`)["result"].(map[string]any)["contents"].([]any)[0].(map[string]any)
	if c["text"] != "作者的正文。\n" || c["mimeType"] != "text/markdown" {
		t.Fatalf("read draft: %v", c)
	}
	if c := s.request("resources/read", `"uri":"wa://resolved"`)["result"].(map[string]any)["contents"].([]any)[0].(map[string]any); !strings.Contains(c["text"].(string), `"resolved"`) {
		t.Fatalf("read resolved: %v", c)
	}
	for _, uri := range []string{"wa://file/../etc/passwd", "wa://file/.writing-anything/../x", "wa://file/.writing-anything/works/my-post/nope.yaml", "file:///etc/passwd"} {
		if m := s.request("resources/read", fmt.Sprintf(`"uri":%q`, uri)); m["error"].(map[string]any)["code"] != -32602.0 {
			t.Errorf("%s: %v", uri, m)
		}
	}
	// 途经符号链接的文件不列出也不能读取。
	os.Symlink("/etc/hostname", wsPath(dir, ".writing-anything/works/my-post/brief.yaml"))
	if m := s.request("resources/read", `"uri":"wa://file/.writing-anything/works/my-post/brief.yaml"`); m["error"] == nil {
		t.Errorf("symlink read: %v", m)
	}
	// get-prompt
	m := s.request("prompts/get", `"name":"wa-draft"`)["result"].(map[string]any)["messages"].([]any)[0].(map[string]any)
	if text := m["content"].(map[string]any)["text"].(string); !strings.HasPrefix(text, "# wa-draft") {
		t.Fatalf("prompt: %q", text)
	}
	s.close()
}

// feedback-notification：订阅 wa://feedback 后，作者提交新反馈时收到更新通知；取消后收到结束响应。
func TestMCPFeedbackNotification(t *testing.T) {
	dir := newWork(t)
	s := startMCP(t, dir, "--interval", "20")
	s.id++
	sub := s.id
	fmt.Fprintf(s.w, `{"jsonrpc":"2.0","id":%d,"method":"subscriptions/listen","params":{"notifications":{"resourceSubscriptions":["wa://feedback"]},%s}}`+"\n", sub, mcpMeta)
	if ack := s.next(); ack["method"] != "notifications/subscriptions/acknowledged" {
		t.Fatalf("ack: %v", ack)
	}
	mustRunWS(t, dir, "feedback", "add", "my-post", "--text", "这里读不下去了")
	up := s.next()
	if up["method"] != "notifications/resources/updated" || up["params"].(map[string]any)["uri"] != "wa://feedback" ||
		up["params"].(map[string]any)["_meta"].(map[string]any)["io.modelcontextprotocol/subscriptionId"] != float64(sub) {
		t.Fatalf("updated: %v", up)
	}
	// 取消后服务不再回复订阅请求；之后的请求照常处理。
	fmt.Fprintf(s.w, `{"jsonrpc":"2.0","method":"notifications/cancelled","params":{"requestId":%d}}`+"\n", sub)
	if m := s.request("prompts/list", ""); m["result"] == nil {
		t.Fatalf("after cancel: %v", m)
	}
	s.close()
}

// rawRequest 发送一个不带 _meta 的请求（旧版会话），返回 id 相同的响应（其间的通知交给 notes）。
func (s *mcpSession) rawRequest(method, params string, notes ...*[]map[string]any) map[string]any {
	s.t.Helper()
	s.id++
	fmt.Fprintf(s.w, `{"jsonrpc":"2.0","id":%d,"method":%q,"params":{%s}}`+"\n", s.id, method, params)
	for {
		m := s.next()
		if m["id"] == float64(s.id) {
			return m
		}
		if len(notes) > 0 {
			*notes[0] = append(*notes[0], m)
		}
	}
}

// mcp-legacy-session：只发送 initialize 的旧版客户端（2025-11-25 与 2025-06-18）协商到所请求的版本，initialized 后能列出并调用
// tool，订阅 wa://feedback 后作者提交反馈时收到更新通知；同一进程中带 _meta 的 2026-07-28 请求行为不变。
func TestMCPLegacyClients(t *testing.T) {
	for _, version := range []string{"2025-11-25", "2025-06-18"} {
		t.Run(version, func(t *testing.T) {
			dir := newWork(t)
			s := startMCP(t, dir, "--interval", "20")
			init := s.rawRequest("initialize", `"protocolVersion":"`+version+`","capabilities":{},"clientInfo":{"name":"old","version":"1"}`)
			r, _ := init["result"].(map[string]any)
			if r["protocolVersion"] != version || r["serverInfo"].(map[string]any)["name"] != "wa-cli" ||
				r["capabilities"].(map[string]any)["resources"].(map[string]any)["subscribe"] != true {
				t.Fatalf("initialize: %v", init)
			}
			fmt.Fprintln(s.w, `{"jsonrpc":"2.0","method":"notifications/initialized"}`)
			tools, _ := s.rawRequest("tools/list", "")["result"].(map[string]any)
			names := []string{}
			for _, tl := range tools["tools"].([]any) {
				names = append(names, tl.(map[string]any)["name"].(string))
			}
			if !slices.Contains(names, "feedback_add") || slices.Contains(names, "mcp") {
				t.Fatalf("tools/list: %v", names)
			}
			call, _ := s.rawRequest("tools/call", `"name":"feedback_list","arguments":{}`)["result"].(map[string]any)
			if call["isError"] != false || call["structuredContent"].(map[string]any)["valid"] != true {
				t.Fatalf("tools/call: %v", call)
			}
			read, _ := s.rawRequest("resources/read", `"uri":"wa://feedback"`)["result"].(map[string]any)
			if read["contents"].([]any)[0].(map[string]any)["mimeType"] != "application/json" {
				t.Fatalf("resources/read: %v", read)
			}
			if sub := s.rawRequest("resources/subscribe", `"uri":"wa://feedback"`); sub["result"] == nil {
				t.Fatalf("subscribe: %v", sub)
			}
			mustRunWS(t, dir, "feedback", "add", "my-post", "--text", "这里读不下去了")
			up := s.next()
			if up["method"] != "notifications/resources/updated" || up["params"].(map[string]any)["uri"] != "wa://feedback" {
				t.Fatalf("updated: %v", up)
			}
			if m := s.request("server/discover", ""); m["result"].(map[string]any)["resultType"] != "complete" {
				t.Fatalf("modern request in legacy session: %v", m)
			}
			s.close()
		})
	}
}

// missing-dir、不合法的 --interval 与 --json 的汇总信封。
func TestMCPCommand(t *testing.T) {
	code, stdout, _ := runIn(t, "", "mcp", "--json")
	var res result
	json.Unmarshal([]byte(stdout), &res)
	if code != 1 || !hasNext(diag(t, res, "mcp.missing-dir"), "wa-cli help mcp --json") {
		t.Fatalf("missing-dir: %d %s", code, stdout)
	}
	dir := newWorkspace(t)
	if code, res := runWS(t, dir, "mcp", "--interval", "0"); code != 1 || diag(t, res, "input.invalid-value").Code == "" {
		t.Fatalf("interval: %d %+v", code, res.Diagnostics)
	}
	if code, res := runWS(t, t.TempDir(), "mcp"); code != 1 || diag(t, res, "workspace.not-found").Code == "" {
		t.Fatalf("no workspace: %d %+v", code, res.Diagnostics)
	}
	input := `{"jsonrpc":"2.0","id":1,"method":"server/discover","params":{` + mcpMeta + `}}` + "\n"
	code, stdout, _ = runIn(t, input, "mcp", "--dir", dir, "--json")
	lines := strings.Split(strings.TrimSpace(stdout), "\n")
	if code != 0 || len(lines) != 2 || !strings.Contains(lines[0], `"resultType":"complete"`) || !strings.Contains(lines[1], `"mcp":{"requests":1}`) {
		t.Fatalf("json summary: %d %q", code, stdout)
	}
	if code, stdout, _ := runIn(t, input, "mcp", "--dir", dir); code != 0 || strings.Count(stdout, "\n") != 1 {
		t.Fatalf("plain: %d %q", code, stdout)
	}
}

// 每个 tool 都按命令登记的样例调用一次，必须成功（cli-contract/mcp-thin-wrapper：测试逐一校验）。
func TestMCPEveryTool(t *testing.T) {
	for _, c := range registry {
		if slices.Contains(mcpExcluded, c.name) {
			continue
		}
		t.Run(c.name, func(t *testing.T) {
			argv := sampleArgs(t, c.name)
			inv, d := parseArgs(c, argv[len(strings.Fields(c.name)):])
			if d != nil {
				t.Fatalf("sample %v: %s", argv, d.Message)
			}
			dir := inv.values["dir"]
			if dir == "" {
				dir = newWorkspace(t)
			}
			if _, err := os.Stat(filepath.Join(dir, ".writing-anything")); err != nil {
				run(t, "init", "--dir", dir) // MCP 服务需要工作区；样例在空目录中运行的命令（如 skills install）照常调用
			}
			args := mcpSampleArgs(t, c, inv, dir)
			s := startMCP(t, dir)
			if r := s.call(strings.ReplaceAll(c.name, " ", "_"), args); r["isError"] != false {
				t.Fatalf("%v: %v", args, r["content"])
			}
			s.close()
		})
	}
}

// mcpSampleArgs 把命令样例解析结果转换为 tool 参数：位置参数按名称，命名参数按类型；--dir、--json、--help 由服务给出或不适用，
// --file 的内容作为 input。
func mcpSampleArgs(t *testing.T, c *command, inv *invocation, dir string) map[string]any {
	t.Helper()
	args := map[string]any{}
	for i, a := range inv.args {
		args[c.args[i].Name] = a
	}
	for name, v := range inv.values {
		switch spec := findFlag(c.allFlags(), name); {
		case name == "dir" || name == "json" || name == "help":
		case name == "file":
			path := v
			if !filepath.IsAbs(path) {
				path = filepath.Join(dir, path)
			}
			args["input"] = readFile(t, path)
		case spec.Type == flagBool:
			args[name] = true
		case spec.Type == flagList:
			items := []any{}
			for _, x := range inv.lists[name] {
				items = append(items, x)
			}
			args[name] = items
		default:
			args[name] = v
		}
	}
	return args
}

// mcpReadOnlyKnown 是必须被识别为只读的命令：样例调用不改变工作区。防止只读判定因样例变化静默丢失覆盖。
var mcpReadOnlyKnown = []string{"help", "version", "validate", "status", "config resolved", "pack list", "source list", "next",
	"decisions", "feedback list", "continuity", "context", "threads", "timeline", "skills list", "brief show", "outline show",
	"plan show", "canon list", "claim list", "state list", "state at", "knowledge list", "knowledge at", "method list", "method show",
	"review show", "review report", "handoff show", "metrics show", "series show", "summary show", "marketing show", "impact list",
	"recall", "lint", "diff", "provenance", "curve"}

// mcp-same-as-cli（只读命令全集）：遍历命令注册表中有 tool 的命令，样例调用前后工作区逐字节不变者按只读处理（含只读命令、
// 不产生变更的幂等调用与被拒绝的调用）；在同一工作区分别以
// 新版请求（2026-07-28，带 _meta）与旧版会话（Legacy Session，2025-11-25 与 2025-06-18 以 initialize 握手后不带 _meta）调用对应 tool，content 文本必须逐字节
// 等于 CLI --json 的标准输出、isError 与退出码一致，且调用后工作区仍不变。
func TestMCPReadOnlySameAsCLI(t *testing.T) {
	frozen := time.Date(2026, 6, 20, 12, 0, 0, 0, time.UTC)
	now = func() time.Time { return frozen }
	defer func() { now = time.Now }()

	var readOnly []string
	for _, c := range registry {
		if slices.Contains(mcpExcluded, c.name) {
			continue
		}
		t.Run(c.name, func(t *testing.T) {
			argv := sampleArgs(t, c.name)
			if !slices.Contains(argv, "--json") {
				argv = append(argv, "--json")
			}
			inv, d := parseArgs(c, argv[len(strings.Fields(c.name)):])
			if d != nil {
				t.Fatalf("sample %v: %s", argv, d.Message)
			}
			dir := inv.values["dir"]
			if dir == "" {
				dir = newWorkspace(t)
			}
			if _, err := os.Stat(filepath.Join(dir, ".writing-anything")); err != nil {
				run(t, "init", "--dir", dir)
			}
			args := mcpSampleArgs(t, c, inv, dir)
			stdin := ""
			if in, ok := args["input"].(string); ok {
				stdin = in
				for i := 0; i+1 < len(argv); i++ {
					if argv[i] == "--file" {
						argv = slices.Delete(slices.Clone(argv), i, i+2) // --file 的内容改由标准输入提供，与 tool 的 input 一致
						break
					}
				}
			}
			before := snapshotTree(t, dir)
			code, want, _ := runIn(t, stdin, argv...)
			if !strings.HasPrefix(want, "{") {
				t.Fatalf("CLI %v 没有输出 JSON 信封: %q", argv, want)
			}
			if !maps.Equal(before, snapshotTree(t, dir)) {
				return // 样例调用改变了工作区：不是只读命令
			}
			readOnly = append(readOnly, c.name)
			tool := strings.ReplaceAll(c.name, " ", "_")
			raw, _ := json.Marshal(args)
			for _, era := range []string{"2026-07-28", "2025-11-25", "2025-06-18"} {
				s := startMCP(t, dir)
				var m map[string]any
				if era == "2026-07-28" {
					m = s.request("tools/call", fmt.Sprintf(`"name":%q,"arguments":%s`, tool, raw))
				} else {
					init := s.rawRequest("initialize", `"protocolVersion":"`+era+`","capabilities":{},"clientInfo":{"name":"old","version":"1"}`)
					if r, _ := init["result"].(map[string]any); r["protocolVersion"] != era {
						t.Fatalf("initialize: %v", init)
					}
					fmt.Fprintln(s.w, `{"jsonrpc":"2.0","method":"notifications/initialized"}`)
					m = s.rawRequest("tools/call", fmt.Sprintf(`"name":%q,"arguments":%s`, tool, raw))
				}
				r, ok := m["result"].(map[string]any)
				if !ok {
					t.Fatalf("%s tools/call: %v", era, m)
				}
				got := r["content"].([]any)[0].(map[string]any)["text"].(string)
				if got != want || r["isError"] != (code != 0) {
					t.Errorf("%s: tool 输出与 CLI 不一致（isError=%v，exit=%d）\n--- MCP ---\n%s\n--- CLI ---\n%s", era, r["isError"], code, got, want)
				}
				if c := s.close(); c != 0 {
					t.Fatalf("%s: mcp exit %d", era, c)
				}
				if !maps.Equal(before, snapshotTree(t, dir)) {
					t.Fatalf("%s: 只读 tool 调用改变了工作区", era)
				}
			}
		})
	}
	for _, name := range mcpReadOnlyKnown {
		if !slices.Contains(readOnly, name) {
			t.Errorf("已知只读命令 %s 没有被识别为只读（样例调用改变了工作区或被跳过）", name)
		}
	}
	if len(readOnly) < 30 {
		t.Errorf("只识别出 %d 条只读命令，期望不少于 30：%v", len(readOnly), readOnly)
	}
	t.Logf("只读命令 %d 条：%v", len(readOnly), readOnly)
}
