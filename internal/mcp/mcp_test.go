package mcp

import (
	"bufio"
	"encoding/json"
	"fmt"
	"io"
	"slices"
	"strings"
	"sync"
	"testing"
	"time"
)

const meta = `"_meta":{"io.modelcontextprotocol/protocolVersion":"2026-07-28","io.modelcontextprotocol/clientCapabilities":{}}`

var allVersions = []string{"2026-07-28", "2025-11-25", "2025-06-18"}

// fake 记录命令调用，并提供可修改的资源。
type fake struct {
	mu    sync.Mutex
	calls [][]string
	stdin []string
	docs  map[string]string
}

func (f *fake) config() Config {
	return Config{
		Version: "test", Dir: "/proj", Interval: 5 * time.Millisecond,
		Tools: []Tool{
			{Name: "feedback_add", Command: []string{"feedback", "add"}, Dir: true, Params: []Param{
				{Name: "unit", Kind: "positional", Required: true}, {Name: "text", Kind: "string"}, {Name: "avoid", Kind: "list"},
				{Name: "force", Kind: "bool"}, {Name: "input", Kind: "input"}}},
			{Name: "version", Command: []string{"version"}},
		},
		Prompts: []Prompt{{Name: "wa-draft", Description: "草稿", Text: "写草稿"}},
		Run: func(args []string, stdin io.Reader, stdout, stderr io.Writer) int {
			data, _ := io.ReadAll(stdin)
			f.mu.Lock()
			f.calls = append(f.calls, args)
			f.stdin = append(f.stdin, string(data))
			f.mu.Unlock()
			if args[0] == "version" {
				fmt.Fprint(stdout, `{"valid":true,"version":"dev"}`)
				return 0
			}
			fmt.Fprint(stdout, `{"valid":false,"diagnostics":[]}`)
			return 1
		},
		List: func() []Resource {
			return []Resource{{URI: "wa://feedback", Name: "反馈", MimeType: "application/json"}}
		},
		Read: func(uri string) (string, string, bool) {
			f.mu.Lock()
			defer f.mu.Unlock()
			text, ok := f.docs[uri]
			return text, "application/json", ok
		},
	}
}

// exchange 把每行输入交给服务，返回按行解析的输出。
func exchange(t *testing.T, cfg Config, input string) ([]map[string]any, int) {
	t.Helper()
	var out strings.Builder
	n, err := Serve(cfg, strings.NewReader(input), &out)
	if err != nil {
		t.Fatal(err)
	}
	var msgs []map[string]any
	for _, l := range strings.Split(strings.TrimSpace(out.String()), "\n") {
		if l == "" {
			continue
		}
		var m map[string]any
		if err := json.Unmarshal([]byte(l), &m); err != nil {
			t.Fatalf("output line %q: %v", l, err)
		}
		msgs = append(msgs, m)
	}
	return msgs, n
}

func errCode(m map[string]any) float64 {
	e, _ := m["error"].(map[string]any)
	c, _ := e["code"].(float64)
	return c
}

func TestProtocol(t *testing.T) {
	f := &fake{}
	input := strings.Join([]string{
		`{"jsonrpc":"2.0","id":1,"method":"server/discover","params":{` + meta + `}}`,
		`{"jsonrpc":"2.0","id":2,"method":"tools/list","params":{"_meta":{"io.modelcontextprotocol/protocolVersion":"2025-11-25","io.modelcontextprotocol/clientCapabilities":{}}}}`,
		`{"jsonrpc":"2.0","id":3,"method":"tools/list"}`,
		`{not json`,
		`[{"jsonrpc":"2.0","id":4,"method":"ping"}]`,
		`{"jsonrpc":"1.0","id":5,"method":"ping"}`,
		`{"jsonrpc":"2.0","id":6,"method":"nope","params":{` + meta + `}}`,
		`{"jsonrpc":"2.0","method":"notifications/initialized"}`,
		`{"jsonrpc":"2.0","id":99,"result":{}}`,
		`{"jsonrpc":"2.0","id":7,"method":"ping","params":{` + meta + `}}`,
		`{"jsonrpc":"2.0","id":8,"method":"tools/list","params":"x"}`,
		`{}`,
		`42`,
		`{"jsonrpc":"2.0"}`,
		`{"jsonrpc":"2.0","id":{"a":1},"method":"tools/list","params":{` + meta + `}}`,
		"",
	}, "\n")
	msgs, n := exchange(t, f.config(), input)
	if n != 6 || len(msgs) != 13 {
		t.Fatalf("requests %d, messages %d: %v", n, len(msgs), msgs)
	}
	d := msgs[0]["result"].(map[string]any)
	if d["resultType"] != "complete" || !slices.Equal(toStrings(d["supportedVersions"]), allVersions) ||
		d["capabilities"].(map[string]any)["resources"].(map[string]any)["subscribe"] != true ||
		d["_meta"].(map[string]any)["io.modelcontextprotocol/serverInfo"].(map[string]any)["name"] != "wa-cli" {
		t.Fatalf("discover: %v", d)
	}
	e := msgs[1]["error"].(map[string]any)
	if e["code"] != -32022.0 || e["data"].(map[string]any)["requested"] != "2025-11-25" || !slices.Equal(toStrings(e["data"].(map[string]any)["supported"]), allVersions) {
		t.Fatalf("version: %v", e)
	}
	for i, want := range []float64{-32602, -32700, -32600, -32600, -32601} {
		if c := errCode(msgs[i+2]); c != want {
			t.Errorf("message %d: code %v, want %v (%v)", i+2, c, want, msgs[i+2])
		}
	}
	if msgs[3]["id"] != nil || msgs[8]["id"] != 8.0 || errCode(msgs[8]) != -32602 {
		t.Errorf("ids: %v %v", msgs[3], msgs[8])
	}
	// ping 在 2026-07-28 中已移除，按未知方法处理。
	if errCode(msgs[7]) != -32601 || msgs[7]["id"] != 7.0 {
		t.Errorf("ping: %v", msgs[7])
	}
	for i := 9; i < 13; i++ {
		if errCode(msgs[i]) != -32600 {
			t.Errorf("message %d: %v", i, msgs[i])
		}
	}
}

func toStrings(v any) []string {
	var out []string
	for _, x := range v.([]any) {
		out = append(out, x.(string))
	}
	return out
}

func TestTools(t *testing.T) {
	f := &fake{}
	call := func(id int, args string) string {
		return fmt.Sprintf(`{"jsonrpc":"2.0","id":%d,"method":"tools/call","params":{"name":"feedback_add","arguments":%s,`+meta+`}}`, id, args)
	}
	input := strings.Join([]string{
		`{"jsonrpc":"2.0","id":1,"method":"tools/list","params":{` + meta + `}}`,
		call(2, `{"unit":"my-post","text":"--json 太长","avoid":["甲","乙"],"force":true,"input":"正文"}`),
		call(3, `{"text":"x"}`),
		call(4, `{"unit":"my-post","color":"red"}`),
		call(5, `{"unit":"-x"}`),
		call(6, `{"unit":"my-post","force":"yes"}`),
		call(7, `{"unit":"my-post","avoid":[1]}`),
		call(8, `[1]`),
		`{"jsonrpc":"2.0","id":9,"method":"tools/call","params":{"name":"nope",` + meta + `}}`,
		`{"jsonrpc":"2.0","id":10,"method":"tools/call","params":{"name":"version",` + meta + `}}`,
		call(11, `{"unit":"my-post","force":false,"text":null}`),
	}, "\n")
	msgs, _ := exchange(t, f.config(), input)
	tools := msgs[0]["result"].(map[string]any)["tools"].([]any)
	schema := tools[0].(map[string]any)["inputSchema"].(map[string]any)
	props := schema["properties"].(map[string]any)
	if len(tools) != 2 || schema["additionalProperties"] != false || !slices.Equal(toStrings(schema["required"]), []string{"unit"}) ||
		props["force"].(map[string]any)["type"] != "boolean" || props["avoid"].(map[string]any)["type"] != "array" || props["input"].(map[string]any)["type"] != "string" {
		t.Fatalf("tools/list: %v", tools)
	}
	r := msgs[1]["result"].(map[string]any)
	if r["isError"] != true || r["structuredContent"].(map[string]any)["valid"] != false || r["content"].([]any)[0].(map[string]any)["type"] != "text" {
		t.Fatalf("call: %v", r)
	}
	want := []string{"feedback", "add", "my-post", "--text=--json 太长", "--avoid=甲", "--avoid=乙", "--force", "--dir", "/proj", "--json"}
	if !slices.Equal(f.calls[0], want) || f.stdin[0] != "正文" {
		t.Fatalf("argv %q stdin %q", f.calls[0], f.stdin[0])
	}
	for i := 2; i <= 8; i++ {
		if errCode(msgs[i]) != -32602 {
			t.Errorf("message %d: %v", i, msgs[i])
		}
	}
	// 不接受 --dir 的命令只加 --json。
	if r := msgs[9]["result"].(map[string]any); r["isError"] != false || !slices.Equal(f.calls[1], []string{"version", "--json"}) {
		t.Fatalf("version: %v %q", msgs[9], f.calls[1])
	}
	if !slices.Equal(f.calls[2], []string{"feedback", "add", "my-post", "--dir", "/proj", "--json"}) || len(f.calls) != 3 {
		t.Fatalf("false and null arguments: %q (%d calls)", f.calls[2], len(f.calls))
	}
}

func TestResourcesAndPrompts(t *testing.T) {
	f := &fake{docs: map[string]string{"wa://feedback": "[]"}}
	input := strings.Join([]string{
		`{"jsonrpc":"2.0","id":1,"method":"resources/list","params":{` + meta + `}}`,
		`{"jsonrpc":"2.0","id":2,"method":"resources/read","params":{"uri":"wa://feedback",` + meta + `}}`,
		`{"jsonrpc":"2.0","id":3,"method":"resources/read","params":{"uri":"wa://nope",` + meta + `}}`,
		`{"jsonrpc":"2.0","id":4,"method":"prompts/list","params":{` + meta + `}}`,
		`{"jsonrpc":"2.0","id":5,"method":"prompts/get","params":{"name":"wa-draft",` + meta + `}}`,
		`{"jsonrpc":"2.0","id":6,"method":"prompts/get","params":{"name":"nope",` + meta + `}}`,
		`{"jsonrpc":"2.0","id":7,"method":"subscriptions/listen","params":{` + meta + `}}`,
	}, "\n")
	msgs, _ := exchange(t, f.config(), input)
	if msgs[0]["result"].(map[string]any)["resources"].([]any)[0].(map[string]any)["uri"] != "wa://feedback" {
		t.Fatalf("list: %v", msgs[0])
	}
	if c := msgs[1]["result"].(map[string]any)["contents"].([]any)[0].(map[string]any); c["text"] != "[]" || c["uri"] != "wa://feedback" {
		t.Fatalf("read: %v", msgs[1])
	}
	if errCode(msgs[2]) != -32602 || msgs[2]["error"].(map[string]any)["data"].(map[string]any)["uri"] != "wa://nope" {
		t.Fatalf("not found: %v", msgs[2])
	}
	if msgs[3]["result"].(map[string]any)["prompts"].([]any)[0].(map[string]any)["name"] != "wa-draft" {
		t.Fatalf("prompts: %v", msgs[3])
	}
	m := msgs[4]["result"].(map[string]any)["messages"].([]any)[0].(map[string]any)
	if m["role"] != "user" || m["content"].(map[string]any)["text"] != "写草稿" || errCode(msgs[5]) != -32602 || errCode(msgs[6]) != -32602 {
		t.Fatalf("get: %v %v %v", m, msgs[5], msgs[6])
	}
}

// 订阅：确认、资源变化时通知、取消后结束；订阅期间照常处理其他请求。
func TestSubscriptions(t *testing.T) {
	f := &fake{docs: map[string]string{"wa://feedback": "[]"}}
	in, w := io.Pipe()
	out, ow := io.Pipe()
	done := make(chan int, 1)
	go func() {
		n, _ := Serve(f.config(), in, ow)
		ow.Close()
		done <- n
	}()
	lines := bufio.NewScanner(out)
	next := func() map[string]any {
		t.Helper()
		if !lines.Scan() {
			t.Fatal("server closed the stream")
		}
		var m map[string]any
		json.Unmarshal(lines.Bytes(), &m)
		return m
	}
	send := func(s string) { fmt.Fprintln(w, s) }
	send(`{"jsonrpc":"2.0","id":"sub-1","method":"subscriptions/listen","params":{"notifications":{"resourceSubscriptions":["wa://feedback","wa://nope"]},` + meta + `}}`)
	ack := next()
	p := ack["params"].(map[string]any)
	if ack["method"] != "notifications/subscriptions/acknowledged" || p["_meta"].(map[string]any)["io.modelcontextprotocol/subscriptionId"] != "sub-1" ||
		!slices.Equal(toStrings(p["notifications"].(map[string]any)["resourceSubscriptions"]), []string{"wa://feedback"}) {
		t.Fatalf("ack: %v", ack)
	}
	send(`{"jsonrpc":"2.0","id":2,"method":"prompts/list","params":{` + meta + `}}`)
	if m := next(); m["id"] != 2.0 {
		t.Fatalf("request during subscription: %v", m)
	}
	// 重复的订阅 id 被拒绝。
	send(`{"jsonrpc":"2.0","id":"sub-1","method":"subscriptions/listen","params":{"notifications":{},` + meta + `}}`)
	if m := next(); errCode(m) != -32602 {
		t.Fatalf("duplicate subscription: %v", m)
	}
	f.mu.Lock()
	f.docs["wa://feedback"] = `[{"id":"fb-1"}]`
	f.mu.Unlock()
	up := next()
	if up["method"] != "notifications/resources/updated" || up["params"].(map[string]any)["uri"] != "wa://feedback" ||
		up["params"].(map[string]any)["_meta"].(map[string]any)["io.modelcontextprotocol/subscriptionId"] != "sub-1" {
		t.Fatalf("updated: %v", up)
	}
	send(`{"jsonrpc":"2.0","method":"notifications/cancelled","params":{"requestId":"other"}}`)
	send(`{"jsonrpc":"2.0","method":"notifications/cancelled","params":{"requestId": "sub-1" ,"reason":"done"}}`)
	// 客户端取消订阅后服务不再回复该请求；之后的请求照常处理。
	send(`{"jsonrpc":"2.0","id":3,"method":"prompts/list","params":{` + meta + `}}`)
	if m := next(); m["id"] != 3.0 {
		t.Fatalf("after cancel: %v", m)
	}
	w.Close()
	select {
	case n := <-done:
		if n != 4 {
			t.Fatalf("requests: %d", n)
		}
	case <-time.After(5 * time.Second):
		t.Fatal("server did not stop")
	}
}

// 超过上限的消息报告 -32600 并丢弃，之后的消息照常处理。
func TestMessageLimit(t *testing.T) {
	f := &fake{}
	input := `{"jsonrpc":"2.0","id":1,"method":"prompts/list","params":{"pad":"` + strings.Repeat("x", MaxMessage) + `",` + meta + `}}` + "\n" +
		`{"jsonrpc":"2.0","id":2,"method":"prompts/list","params":{` + meta + `}}`
	msgs, n := exchange(t, f.config(), input)
	if len(msgs) != 2 || errCode(msgs[0]) != -32600 || msgs[1]["id"] != 2.0 || n != 1 {
		t.Fatalf("limit: %d %v", n, msgs)
	}
}

// 同时进行的订阅与每个订阅的资源数有上限。
func TestSubscriptionLimits(t *testing.T) {
	f := &fake{docs: map[string]string{}}
	var lines []string
	for i := 0; i <= MaxSubscriptions; i++ {
		lines = append(lines, fmt.Sprintf(`{"jsonrpc":"2.0","id":%d,"method":"subscriptions/listen","params":{"notifications":{},`+meta+`}}`, i))
	}
	uris := make([]string, MaxSubscribedURI+1)
	for i := range uris {
		uris[i] = fmt.Sprintf("%q", fmt.Sprint("wa://x", i))
	}
	lines = append(lines, `{"jsonrpc":"2.0","id":"many","method":"subscriptions/listen","params":{"notifications":{"resourceSubscriptions":[`+strings.Join(uris, ",")+`]},`+meta+`}}`)
	msgs, _ := exchange(t, f.config(), strings.Join(lines, "\n"))
	errs := 0
	for _, m := range msgs {
		if errCode(m) == -32602 {
			errs++
		}
	}
	if errs != 2 {
		t.Fatalf("limit errors %d: %v", errs, msgs[len(msgs)-2:])
	}
}
