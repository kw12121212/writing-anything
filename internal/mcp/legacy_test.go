package mcp

import (
	"bufio"
	"encoding/json"
	"fmt"
	"io"
	"slices"
	"strings"
	"testing"
	"time"
)

func legacyInit(version string) string {
	return `{"jsonrpc":"2.0","id":"init","method":"initialize","params":{"protocolVersion":"` + version +
		`","capabilities":{},"clientInfo":{"name":"old-client","version":"1.0"}}}`
}

const initialized = `{"jsonrpc":"2.0","method":"notifications/initialized"}`

func result(t *testing.T, m map[string]any) map[string]any {
	t.Helper()
	r, ok := m["result"].(map[string]any)
	if !ok {
		t.Fatalf("no result: %v", m)
	}
	return r
}

// legacy-handshake：每个旧版本协商到所请求的版本，initialized 之后不带 _meta 即可列出并调用 tool、读取资源与 prompt。
func TestLegacyHandshake(t *testing.T) {
	for _, version := range []string{"2025-11-25", "2025-06-18"} {
		t.Run(version, func(t *testing.T) {
			f := &fake{docs: map[string]string{"wa://feedback": "[]"}}
			input := strings.Join([]string{
				legacyInit(version),
				`{"jsonrpc":"2.0","id":1,"method":"tools/list"}`,
				`{"jsonrpc":"2.0","id":2,"method":"ping"}`,
				initialized,
				`{"jsonrpc":"2.0","id":3,"method":"tools/list","params":{"_meta":{"progressToken":"p"}}}`,
				`{"jsonrpc":"2.0","id":4,"method":"tools/call","params":{"name":"version"}}`,
				`{"jsonrpc":"2.0","id":5,"method":"resources/list"}`,
				`{"jsonrpc":"2.0","id":6,"method":"resources/read","params":{"uri":"wa://feedback"}}`,
				`{"jsonrpc":"2.0","id":7,"method":"prompts/get","params":{"name":"wa-draft"}}`,
				`{"jsonrpc":"2.0","id":8,"method":"tools/call","params":{"name":"nope"}}`,
				`{"jsonrpc":"2.0","id":9,"method":"server/discover"}`,
				`{"jsonrpc":"2.0","id":10,"method":"ping"}`,
				`{"jsonrpc":"2.0","id":11,"method":"prompts/list"}`,
			}, "\n")
			msgs, n := exchange(t, f.config(), input)
			if n != 12 || len(msgs) != 12 {
				t.Fatalf("requests %d, messages %d: %v", n, len(msgs), msgs)
			}
			init := result(t, msgs[0])
			info := init["serverInfo"].(map[string]any)
			caps := init["capabilities"].(map[string]any)
			if init["protocolVersion"] != version || info["name"] != "wa-cli" || info["version"] != "test" ||
				caps["resources"].(map[string]any)["subscribe"] != true || caps["tools"] == nil || caps["prompts"] == nil {
				t.Fatalf("initialize: %v", init)
			}
			if _, has := init["resultType"]; has {
				t.Errorf("legacy initialize has resultType: %v", init)
			}
			// initialized 之前只接受 ping。
			if errCode(msgs[1]) != -32600 || len(result(t, msgs[2])) != 0 {
				t.Fatalf("before initialized: %v %v", msgs[1], msgs[2])
			}
			tools := result(t, msgs[3])
			if len(tools["tools"].([]any)) != 2 {
				t.Fatalf("tools/list: %v", tools)
			}
			for i := 3; i <= 7; i++ {
				r := result(t, msgs[i])
				if _, has := r["resultType"]; has {
					t.Errorf("message %d has resultType: %v", i, r)
				}
				if _, has := r["_meta"]; has {
					t.Errorf("message %d has _meta: %v", i, r)
				}
			}
			call := result(t, msgs[4])
			if call["isError"] != false || call["structuredContent"].(map[string]any)["version"] != "dev" ||
				!slices.Equal(f.calls[0], []string{"version", "--json"}) {
				t.Fatalf("tools/call: %v %q", call, f.calls)
			}
			if result(t, msgs[5])["resources"].([]any)[0].(map[string]any)["uri"] != "wa://feedback" ||
				result(t, msgs[6])["contents"].([]any)[0].(map[string]any)["text"] != "[]" {
				t.Fatalf("resources: %v %v", msgs[5], msgs[6])
			}
			if result(t, msgs[7])["messages"].([]any)[0].(map[string]any)["content"].(map[string]any)["text"] != "写草稿" {
				t.Fatalf("prompts/get: %v", msgs[7])
			}
			if errCode(msgs[8]) != -32602 || errCode(msgs[9]) != -32601 || len(result(t, msgs[10])) != 0 ||
				result(t, msgs[11])["prompts"].([]any)[0].(map[string]any)["name"] != "wa-draft" {
				t.Fatalf("errors and ping: %v %v %v %v", msgs[8], msgs[9], msgs[10], msgs[11])
			}
		})
	}
}

// legacy-negotiate-latest 与 legacy-gate：其他版本协商到 2025-11-25；initialize 的错误列出支持版本；未 initialize 的请求被拒。
func TestLegacyNegotiation(t *testing.T) {
	for _, requested := range []string{"2024-11-05", "2025-03-26", "2026-07-28", "1.0.0"} {
		msgs, _ := exchange(t, (&fake{}).config(), legacyInit(requested))
		if v := result(t, msgs[0])["protocolVersion"]; v != "2025-11-25" {
			t.Errorf("requested %s: negotiated %v", requested, v)
		}
	}
	supported := func(m map[string]any) []string {
		e := m["error"].(map[string]any)
		if !strings.Contains(e["message"].(string), "2026-07-28、2025-11-25、2025-06-18") {
			t.Errorf("message does not list versions: %v", e)
		}
		return toStrings(e["data"].(map[string]any)["supported"])
	}
	input := strings.Join([]string{
		initialized, // initialize 之前的 initialized 通知被忽略
		`{"jsonrpc":"2.0","id":1,"method":"tools/list"}`,
		`{"jsonrpc":"2.0","id":2,"method":"ping"}`,
		`{"jsonrpc":"2.0","id":3,"method":"initialize","params":{"capabilities":{}}}`,
		`{"jsonrpc":"2.0","id":4,"method":"initialize","params":"x"}`,
		`{"jsonrpc":"2.0","id":5,"method":"initialize","params":{"protocolVersion":20250618}}`,
		`{"jsonrpc":"2.0","id":6,"method":"initialize","params":null}`,
		legacyInit("2025-06-18"),
		`{"jsonrpc":"2.0","id":7,"method":"tools/list","params":"x"}`,
		legacyInit("2025-06-18"),
		initialized,
		`{"jsonrpc":"2.0","id":8,"method":"tools/list"}`,
	}, "\n")
	msgs, n := exchange(t, (&fake{}).config(), input)
	if n != 10 || len(msgs) != 10 {
		t.Fatalf("requests %d, messages %d: %v", n, len(msgs), msgs)
	}
	// 未 initialize 且不带 _meta：沿用 -32602，提示可以先 initialize。
	for _, m := range msgs[:2] {
		if errCode(m) != -32602 || !strings.Contains(m["error"].(map[string]any)["message"].(string), "initialize") {
			t.Errorf("before initialize: %v", m)
		}
	}
	for _, m := range msgs[2:6] {
		if errCode(m) != -32602 || !slices.Equal(supported(m), allVersions) {
			t.Errorf("invalid initialize: %v", m)
		}
	}
	if result(t, msgs[6])["protocolVersion"] != "2025-06-18" {
		t.Fatalf("initialize after errors: %v", msgs[6])
	}
	// initialized 之前，即使 params 不合法也按未完成初始化拒绝。
	if errCode(msgs[7]) != -32600 {
		t.Fatalf("before initialized: %v", msgs[7])
	}
	if errCode(msgs[8]) != -32600 || !slices.Equal(supported(msgs[8]), allVersions) {
		t.Fatalf("second initialize: %v", msgs[8])
	}
	if _, ok := msgs[9]["result"]; !ok {
		t.Fatalf("session after second initialize: %v", msgs[9])
	}
}

// legacy-keeps-modern：旧版会话中带 _meta 协议版本的请求仍按 2026-07-28 处理。
func TestLegacyKeepsModern(t *testing.T) {
	input := strings.Join([]string{
		legacyInit("2025-11-25"),
		initialized,
		`{"jsonrpc":"2.0","id":1,"method":"tools/list","params":{` + meta + `}}`,
		`{"jsonrpc":"2.0","id":2,"method":"ping","params":{` + meta + `}}`,
		`{"jsonrpc":"2.0","id":3,"method":"tools/list","params":{"_meta":{"io.modelcontextprotocol/protocolVersion":"2025-11-25","io.modelcontextprotocol/clientCapabilities":{}}}}`,
		`{"jsonrpc":"2.0","id":4,"method":"server/discover","params":{` + meta + `}}`,
	}, "\n")
	msgs, _ := exchange(t, (&fake{}).config(), input)
	r := result(t, msgs[1])
	if r["resultType"] != "complete" || r["_meta"].(map[string]any)["io.modelcontextprotocol/serverInfo"].(map[string]any)["name"] != "wa-cli" {
		t.Fatalf("modern tools/list: %v", r)
	}
	if errCode(msgs[2]) != -32601 || errCode(msgs[3]) != -32022 {
		t.Fatalf("modern ping and version: %v %v", msgs[2], msgs[3])
	}
	if !slices.Equal(toStrings(result(t, msgs[4])["supportedVersions"]), allVersions) {
		t.Fatalf("discover: %v", msgs[4])
	}
}

// legacy-subscribe：resources/subscribe 后资源变化时收到只含 uri 的更新通知；取消订阅后不再通知。
func TestLegacySubscribe(t *testing.T) {
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
	setDoc := func(text string) {
		f.mu.Lock()
		f.docs["wa://feedback"] = text
		f.mu.Unlock()
	}
	send(legacyInit("2025-06-18"))
	next()
	send(`{"jsonrpc":"2.0","id":1,"method":"resources/subscribe","params":{"uri":"wa://feedback"}}`)
	if m := next(); errCode(m) != -32600 {
		t.Fatalf("subscribe before initialized: %v", m)
	}
	send(initialized)
	send(`{"jsonrpc":"2.0","id":2,"method":"resources/subscribe","params":{"uri":"wa://feedback"}}`)
	if m := next(); m["id"] != 2.0 || len(result(t, m)) != 0 {
		t.Fatalf("subscribe: %v", m)
	}
	send(`{"jsonrpc":"2.0","id":3,"method":"resources/subscribe","params":{"uri":"wa://feedback"}}`)
	if m := next(); m["id"] != 3.0 || m["result"] == nil {
		t.Fatalf("duplicate subscribe: %v", m)
	}
	send(`{"jsonrpc":"2.0","id":4,"method":"resources/subscribe","params":{"uri":"wa://nope"}}`)
	if m := next(); errCode(m) != -32602 || m["error"].(map[string]any)["message"] != "Resource not found" {
		t.Fatalf("missing resource: %v", m)
	}
	setDoc(`[{"id":"fb-1"}]`)
	up := next()
	if up["method"] != "notifications/resources/updated" || up["params"].(map[string]any)["uri"] != "wa://feedback" {
		t.Fatalf("updated: %v", up)
	}
	if _, has := up["params"].(map[string]any)["_meta"]; has {
		t.Fatalf("legacy notification has _meta: %v", up)
	}
	// 订阅期间其他请求照常处理。
	send(`{"jsonrpc":"2.0","id":5,"method":"prompts/list"}`)
	if m := next(); m["id"] != 5.0 {
		t.Fatalf("request during subscription: %v", m)
	}
	send(`{"jsonrpc":"2.0","id":6,"method":"resources/unsubscribe","params":{"uri":"wa://feedback"}}`)
	if m := next(); m["id"] != 6.0 || m["result"] == nil {
		t.Fatalf("unsubscribe: %v", m)
	}
	// 取消未订阅的 URI 也返回空结果。
	send(`{"jsonrpc":"2.0","id":"u2","method":"resources/unsubscribe","params":{"uri":"wa://nope"}}`)
	if m := next(); m["id"] != "u2" || m["result"] == nil {
		t.Fatalf("unsubscribe unknown: %v", m)
	}
	setDoc(`[{"id":"fb-1"},{"id":"fb-2"}]`)
	time.Sleep(50 * time.Millisecond)
	send(`{"jsonrpc":"2.0","id":7,"method":"ping"}`)
	if m := next(); m["id"] != 7.0 {
		t.Fatalf("notification after unsubscribe: %v", m)
	}
	w.Close()
	select {
	case n := <-done:
		if n != 9 {
			t.Fatalf("requests: %d", n)
		}
	case <-time.After(5 * time.Second):
		t.Fatal("server did not stop")
	}
}

// 旧版会话至多订阅 MaxSubscribedURI 个资源；重复订阅不占名额。
func TestLegacySubscribeLimit(t *testing.T) {
	f := &fake{docs: map[string]string{}}
	lines := []string{legacyInit("2025-11-25"), initialized}
	for i := 0; i <= MaxSubscribedURI; i++ {
		uri := fmt.Sprint("wa://x", i)
		f.docs[uri] = ""
		lines = append(lines, fmt.Sprintf(`{"jsonrpc":"2.0","id":%d,"method":"resources/subscribe","params":{"uri":%q}}`, i, uri))
	}
	lines = append(lines, `{"jsonrpc":"2.0","id":"dup","method":"resources/subscribe","params":{"uri":"wa://x0"}}`)
	msgs, _ := exchange(t, f.config(), strings.Join(lines, "\n"))
	errs := 0
	for _, m := range msgs[1:] {
		if errCode(m) == -32602 {
			errs++
		}
	}
	if errs != 1 || errCode(msgs[len(msgs)-2]) != -32602 || msgs[len(msgs)-1]["result"] == nil {
		t.Fatalf("limit errors %d: %v", errs, msgs[len(msgs)-2:])
	}
}
