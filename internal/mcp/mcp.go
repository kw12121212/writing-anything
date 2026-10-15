// Package mcp 实现 MCP 服务（MCP Server）的协议层：以 stdio 方式收发 JSON-RPC 2.0 消息。服务是双代服务端（Dual-era Server）：
// 在 _meta 中带协议版本的请求遵循 MCP 2026-07-28 版协议（无状态，每个请求带协议版本与客户端能力）；发送 initialize 的旧版客户端
// 在该进程内得到一个旧版会话（Legacy Session，2025-11-25 或 2025-06-18，见 legacy.go）。本包不知道 wa-cli 有哪些命令：tool 的规格、执行命令的函数、资源与 prompt 都由
// 命令层传入（cli-contract/mcp-thin-wrapper），本包只负责把 MCP 请求映射为一次命令调用并原样返回结果。
package mcp

import (
	"bufio"
	"bytes"
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"sort"
	"strings"
	"sync"
	"time"
)

// ProtocolVersion 是无状态（2026-07-28）请求的协议版本。
const ProtocolVersion = "2026-07-28"

// SupportedVersions 是服务支持的全部协议版本：2026-07-28 与旧版会话可协商的版本（legacyVersions）。
var SupportedVersions = append([]string{ProtocolVersion}, legacyVersions...)

// MaxMessage 是一条消息（一行）的字节数上限（workspace-artifacts/input-limits）。
const MaxMessage = 8 << 20

// JSON-RPC 与 MCP 的错误码。
const (
	codeParse       = -32700
	codeInvalidReq  = -32600
	codeNoMethod    = -32601
	codeInvalidArgs = -32602
	codeVersion     = -32022
)

const metaPrefix = "io.modelcontextprotocol/"

// Param 是 tool 的一个参数。Kind 为 positional（位置参数，字符串）、string、bool、list（字符串数组）或 input（标准输入的内容）。
type Param struct {
	Name        string
	Kind        string
	Required    bool
	Description string
}

// Tool 是一条命令对应的 tool：Command 为命令名的各个词，Params 按位置参数在前的顺序排列；Dir 为 true 时命令接受 --dir，
// 调用时加上服务的项目根目录。
type Tool struct {
	Name        string
	Description string
	Command     []string
	Params      []Param
	Dir         bool
}

// 订阅的数量上限（workspace-artifacts/input-limits）。
const (
	MaxSubscriptions = 32
	MaxSubscribedURI = 64
)

// Resource 是一个资源的描述。
type Resource struct {
	URI      string `json:"uri"`
	Name     string `json:"name"`
	MimeType string `json:"mimeType"`
}

// Prompt 是一个 prompt：Text 为发给模型的说明。
type Prompt struct {
	Name        string
	Description string
	Text        string
}

// Config 是服务的配置。Run 运行一条命令（等同 cli.Run）；List 与 Read 列出与读取资源（Read 的 ok 为 false 表示资源不存在）。
// 对 Run 与 Read 的调用由服务串行化。
type Config struct {
	Version      string
	Instructions string
	Dir          string
	Tools        []Tool
	Prompts      []Prompt
	Run          func(args []string, stdin io.Reader, stdout, stderr io.Writer) int
	List         func() []Resource
	Read         func(uri string) (text, mimeType string, ok bool)
	Interval     time.Duration
}

type server struct {
	cfg     Config
	out     io.Writer
	writeMu sync.Mutex
	runMu   sync.Mutex // 串行化命令执行与资源读取
	subsMu  sync.Mutex
	subs    map[string]*subscription // 规范化的请求 id → 订阅
	wg      sync.WaitGroup
	done    chan struct{}
	legacy  *legacySession // initialize 之后的旧版会话；只在读取消息的 goroutine 中赋值
}

type subscription struct {
	id   json.RawMessage
	stop chan struct{}
}

// message 是收到的一条 JSON-RPC 消息。
type message struct {
	JSONRPC string          `json:"jsonrpc"`
	ID      json.RawMessage `json:"id"`
	Method  string          `json:"method"`
	Params  json.RawMessage `json:"params"`
	Result  json.RawMessage `json:"result"`
	Error   json.RawMessage `json:"error"`
}

type rpcError struct {
	Code    int    `json:"code"`
	Message string `json:"message"`
	Data    any    `json:"data,omitempty"`
}

// Serve 从 in 逐行读取消息并处理，直到 in 结束；返回处理的请求数。订阅在 in 结束时停止。
func Serve(cfg Config, in io.Reader, out io.Writer) (int, error) {
	if cfg.Interval <= 0 {
		cfg.Interval = time.Second
	}
	s := &server{cfg: cfg, out: out, subs: map[string]*subscription{}, done: make(chan struct{})}
	defer func() {
		close(s.done)
		s.wg.Wait()
	}()
	r := bufio.NewReaderSize(in, 64<<10)
	requests := 0
	for {
		line, tooLong, err := readLine(r)
		if tooLong {
			s.reply(nil, nil, &rpcError{Code: codeInvalidReq, Message: fmt.Sprintf("消息超过 %d 字节的上限", MaxMessage)})
		} else if len(bytes.TrimSpace(line)) > 0 {
			if s.handle(line) {
				requests++
			}
		}
		if err == io.EOF {
			return requests, nil
		}
		if err != nil {
			return requests, err
		}
	}
}

// readLine 读取一行（不含换行）；超过 MaxMessage 时丢弃这一行的其余部分并报告 tooLong。
func readLine(r *bufio.Reader) (line []byte, tooLong bool, err error) {
	for {
		chunk, e := r.ReadSlice('\n')
		if !tooLong {
			if len(line)+len(chunk) > MaxMessage+1 {
				tooLong, line = true, nil
			} else {
				line = append(line, chunk...)
			}
		}
		if !errors.Is(e, bufio.ErrBufferFull) {
			return bytes.TrimRight(line, "\r\n"), tooLong, e
		}
	}
}

// handle 处理一条消息，返回它是否为请求。
func (s *server) handle(line []byte) bool {
	trimmed := bytes.TrimSpace(line)
	if trimmed[0] == '[' {
		s.reply(nil, nil, &rpcError{Code: codeInvalidReq, Message: "不支持批量消息"})
		return false
	}
	if !json.Valid(trimmed) {
		s.reply(nil, nil, &rpcError{Code: codeParse, Message: "无法解析的 JSON"})
		return false
	}
	var m message
	if trimmed[0] != '{' || json.Unmarshal(trimmed, &m) != nil {
		s.reply(nil, nil, &rpcError{Code: codeInvalidReq, Message: "消息必须是 JSON-RPC 2.0 对象"})
		return false
	}
	isRequest := len(m.ID) > 0 && string(m.ID) != "null"
	if isRequest && m.ID[0] != '"' && (m.ID[0] < '0' || m.ID[0] > '9') && m.ID[0] != '-' {
		s.reply(nil, nil, &rpcError{Code: codeInvalidReq, Message: "请求的 id 必须是字符串或数字"})
		return false
	}
	switch {
	case m.JSONRPC == "2.0" && m.Method == "" && (len(m.Result) > 0 || len(m.Error) > 0):
		return false // 客户端发来的响应：服务不发请求，忽略
	case m.JSONRPC != "2.0" || m.Method == "" && isRequest:
		s.reply(m.ID, nil, &rpcError{Code: codeInvalidReq, Message: "不是 JSON-RPC 2.0 请求"})
		return false
	case m.Method == "":
		s.reply(nil, nil, &rpcError{Code: codeInvalidReq, Message: "不是 JSON-RPC 2.0 请求或通知：缺少 method"})
		return false
	case !isRequest:
		switch m.Method {
		case "notifications/cancelled":
			s.cancel(m.Params)
		case "notifications/initialized":
			if s.legacy != nil {
				s.legacy.ready = true
			}
		}
		return false // 其他通知忽略
	case m.Method == "initialize":
		result, rerr := s.initialize(m.Params)
		s.respond(m.ID, result, rerr)
		return true
	}
	params, perr := parseParams(m.Params)
	meta, _ := params["_meta"].(map[string]any)
	_, modern := meta[metaPrefix+"protocolVersion"]
	switch {
	case !modern && s.awaitingInitialized(m):
		s.respond(m.ID, nil, &rpcError{Code: codeInvalidReq, Message: "旧版会话（Legacy Session）尚未完成初始化：请先发送 notifications/initialized"})
		return true
	case perr != nil:
		s.reply(m.ID, nil, perr)
		return true
	case !modern && s.legacy != nil:
		s.handleLegacy(m, params)
		return true
	}
	if perr := check(meta); perr != nil {
		s.reply(m.ID, nil, perr)
		return true
	}
	result, rerr := s.dispatch(m, params)
	if result != nil || rerr != nil {
		s.reply(m.ID, result, rerr)
	}
	return true
}

// parseParams 把请求的 params 解析为对象；缺少或为 null 时为空对象。
func parseParams(raw json.RawMessage) (map[string]any, *rpcError) {
	params := map[string]any{}
	if len(raw) > 0 && string(raw) != "null" {
		if err := json.Unmarshal(raw, &params); err != nil || params == nil {
			return nil, &rpcError{Code: codeInvalidArgs, Message: "params 应为对象"}
		}
	}
	return params, nil
}

// check 校验 2026-07-28 请求 _meta 中的协议版本与客户端能力。
func check(meta map[string]any) *rpcError {
	version, ok := meta[metaPrefix+"protocolVersion"].(string)
	_, caps := meta[metaPrefix+"clientCapabilities"].(map[string]any)
	if !ok || !caps {
		return &rpcError{Code: codeInvalidArgs, Message: "请求的 params._meta 必须含 " + metaPrefix + "protocolVersion 与 " + metaPrefix +
			"clientCapabilities；只支持旧版协议的客户端请先发送 initialize（支持的协议版本：" + strings.Join(SupportedVersions, "、") + "）"}
	}
	if version != ProtocolVersion {
		return &rpcError{Code: codeVersion, Message: "Unsupported protocol version", Data: map[string]any{"supported": SupportedVersions, "requested": version}}
	}
	return nil
}

// dispatch 处理一个 2026-07-28 请求；返回 nil 结果与 nil 错误表示稍后回复（订阅）。
func (s *server) dispatch(m message, params map[string]any) (map[string]any, *rpcError) {
	switch m.Method {
	case "server/discover":
		return map[string]any{
			"supportedVersions": SupportedVersions,
			"capabilities":      capabilities(),
			"instructions":      s.cfg.Instructions,
		}, nil
	case "subscriptions/listen":
		return s.listen(m.ID, params)
	}
	if result, rerr, ok := s.common(m.Method, params); ok {
		return result, rerr
	}
	return nil, &rpcError{Code: codeNoMethod, Message: "Method not found: " + m.Method}
}

// capabilities 是服务声明的能力，两代相同。
func capabilities() map[string]any {
	return map[string]any{"tools": map[string]any{}, "resources": map[string]any{"subscribe": true}, "prompts": map[string]any{}}
}

// common 处理两代共用的 tool、资源与 prompt 方法；ok 为 false 表示不是这些方法。
func (s *server) common(method string, params map[string]any) (result map[string]any, rerr *rpcError, ok bool) {
	switch method {
	case "tools/list":
		return map[string]any{"tools": s.toolList()}, nil, true
	case "tools/call":
		result, rerr = s.call(params)
		return result, rerr, true
	case "resources/list":
		s.runMu.Lock()
		rs := s.cfg.List()
		s.runMu.Unlock()
		if rs == nil {
			rs = []Resource{}
		}
		return map[string]any{"resources": rs}, nil, true
	case "resources/read":
		uri, _ := params["uri"].(string)
		text, mime, found := s.read(uri)
		if !found {
			return nil, notFound(uri), true
		}
		return map[string]any{"contents": []map[string]any{{"uri": uri, "mimeType": mime, "text": text}}}, nil, true
	case "prompts/list":
		out := []map[string]any{}
		for _, p := range s.cfg.Prompts {
			out = append(out, map[string]any{"name": p.Name, "description": p.Description})
		}
		return map[string]any{"prompts": out}, nil, true
	case "prompts/get":
		name, _ := params["name"].(string)
		for _, p := range s.cfg.Prompts {
			if p.Name == name {
				return map[string]any{"description": p.Description, "messages": []map[string]any{
					{"role": "user", "content": map[string]any{"type": "text", "text": p.Text}}}}, nil, true
			}
		}
		return nil, &rpcError{Code: codeInvalidArgs, Message: "prompt " + name + " 不存在"}, true
	}
	return nil, nil, false
}

func notFound(uri string) *rpcError {
	return &rpcError{Code: codeInvalidArgs, Message: "Resource not found", Data: map[string]any{"uri": uri}}
}

func (s *server) read(uri string) (string, string, bool) {
	s.runMu.Lock()
	defer s.runMu.Unlock()
	return s.cfg.Read(uri)
}

// toolList 返回 tools/list 的 tool 定义：inputSchema 由参数生成。
func (s *server) toolList() []map[string]any {
	out := []map[string]any{}
	for _, t := range s.cfg.Tools {
		props := map[string]any{}
		required := []string{}
		for _, p := range t.Params {
			var schema map[string]any
			switch p.Kind {
			case "bool":
				schema = map[string]any{"type": "boolean"}
			case "list":
				schema = map[string]any{"type": "array", "items": map[string]any{"type": "string"}}
			default:
				schema = map[string]any{"type": "string"}
			}
			schema["description"] = p.Description
			props[p.Name] = schema
			if p.Required {
				required = append(required, p.Name)
			}
		}
		out = append(out, map[string]any{"name": t.Name, "description": t.Description,
			"inputSchema": map[string]any{"type": "object", "properties": props, "required": required, "additionalProperties": false}})
	}
	return out
}

// call 处理 tools/call：校验参数、转换为命令行、在进程内运行命令并返回其输出。
func (s *server) call(params map[string]any) (map[string]any, *rpcError) {
	name, _ := params["name"].(string)
	var tool *Tool
	for i := range s.cfg.Tools {
		if s.cfg.Tools[i].Name == name {
			tool = &s.cfg.Tools[i]
		}
	}
	if tool == nil {
		return nil, &rpcError{Code: codeInvalidArgs, Message: "tool " + name + " 不存在"}
	}
	args := map[string]any{}
	if raw, ok := params["arguments"]; ok && raw != nil {
		if args, ok = raw.(map[string]any); !ok {
			return nil, &rpcError{Code: codeInvalidArgs, Message: "arguments 应为对象"}
		}
	}
	argv, stdin, err := buildArgs(tool, args)
	if err != nil {
		return nil, &rpcError{Code: codeInvalidArgs, Message: err.Error()}
	}
	if tool.Dir {
		argv = append(argv, "--dir", s.cfg.Dir)
	}
	argv = append(argv, "--json")
	var stdout, stderr bytes.Buffer
	s.runMu.Lock()
	code := s.cfg.Run(argv, strings.NewReader(stdin), &stdout, &stderr)
	s.runMu.Unlock()
	result := map[string]any{"content": []map[string]any{{"type": "text", "text": stdout.String()}}, "isError": code != 0}
	var v map[string]any
	if json.Unmarshal(stdout.Bytes(), &v) == nil {
		result["structuredContent"] = v
	}
	return result, nil
}

// buildArgs 按 tool 的参数把 arguments 转换为命令行与标准输入；出现未声明的参数、类型不符、缺少必需参数或位置参数以 - 开头时报错。
func buildArgs(t *Tool, args map[string]any) ([]string, string, error) {
	for _, k := range sortedKeys(args) {
		if !hasParam(t, k) {
			return nil, "", fmt.Errorf("tool %s 没有参数 %s", t.Name, k)
		}
	}
	argv := append([]string{}, t.Command...)
	var flags []string
	stdin := ""
	for _, p := range t.Params {
		v, ok := args[p.Name]
		if !ok || v == nil {
			if p.Required {
				return nil, "", fmt.Errorf("tool %s 缺少必需的参数 %s", t.Name, p.Name)
			}
			continue
		}
		switch p.Kind {
		case "bool":
			b, ok := v.(bool)
			if !ok {
				return nil, "", fmt.Errorf("参数 %s 应为布尔值", p.Name)
			}
			if b {
				flags = append(flags, "--"+p.Name)
			}
		case "list":
			items, ok := v.([]any)
			if !ok {
				return nil, "", fmt.Errorf("参数 %s 应为字符串数组", p.Name)
			}
			for _, it := range items {
				str, ok := it.(string)
				if !ok {
					return nil, "", fmt.Errorf("参数 %s 应为字符串数组", p.Name)
				}
				flags = append(flags, "--"+p.Name+"="+str)
			}
		default:
			str, ok := v.(string)
			if !ok {
				return nil, "", fmt.Errorf("参数 %s 应为字符串", p.Name)
			}
			switch p.Kind {
			case "positional":
				if strings.HasPrefix(str, "-") {
					return nil, "", fmt.Errorf("位置参数 %s 不能以 - 开头", p.Name)
				}
				argv = append(argv, str)
			case "input":
				stdin = str
			default:
				flags = append(flags, "--"+p.Name+"="+str)
			}
		}
	}
	return append(argv, flags...), stdin, nil
}

func hasParam(t *Tool, name string) bool {
	for _, p := range t.Params {
		if p.Name == name {
			return true
		}
	}
	return false
}

func sortedKeys(m map[string]any) []string {
	out := make([]string, 0, len(m))
	for k := range m {
		out = append(out, k)
	}
	sort.Strings(out)
	return out
}

// listen 处理 subscriptions/listen：确认订阅，之后按间隔轮询订阅的资源，变化时发送更新通知；请求在被取消前不回复。
func (s *server) listen(id json.RawMessage, params map[string]any) (map[string]any, *rpcError) {
	filter, _ := params["notifications"].(map[string]any)
	if filter == nil {
		return nil, &rpcError{Code: codeInvalidArgs, Message: "subscriptions/listen 的 params.notifications 必须是对象"}
	}
	var uris []string
	last := map[string]string{}
	key := canonical(id)
	s.subsMu.Lock()
	_, dup := s.subs[key]
	full := len(s.subs) >= MaxSubscriptions
	s.subsMu.Unlock()
	switch list, _ := filter["resourceSubscriptions"].([]any); {
	case dup:
		return nil, &rpcError{Code: codeInvalidArgs, Message: "订阅的请求 id 已在使用"}
	case full:
		return nil, &rpcError{Code: codeInvalidArgs, Message: fmt.Sprintf("同时进行的订阅至多 %d 个", MaxSubscriptions)}
	case len(list) > MaxSubscribedURI:
		return nil, &rpcError{Code: codeInvalidArgs, Message: fmt.Sprintf("一个订阅至多 %d 个资源", MaxSubscribedURI)}
	}
	if list, ok := filter["resourceSubscriptions"].([]any); ok {
		for _, it := range list {
			uri, _ := it.(string)
			if text, _, ok := s.read(uri); ok {
				uris = append(uris, uri)
				last[uri] = text
			}
		}
	}
	sub := &subscription{id: id, stop: make(chan struct{})}
	s.subsMu.Lock()
	s.subs[key] = sub
	s.subsMu.Unlock()
	s.notify("notifications/subscriptions/acknowledged", map[string]any{
		"_meta":         map[string]any{metaPrefix + "subscriptionId": id},
		"notifications": map[string]any{"resourceSubscriptions": nonNil(uris)},
	})
	s.wg.Add(1)
	go func() {
		defer s.wg.Done()
		ticker := time.NewTicker(s.cfg.Interval)
		defer ticker.Stop()
		for {
			select {
			case <-s.done:
				return
			case <-sub.stop:
				return // 客户端取消：按取消的规则不再回复该请求
			case <-ticker.C:
				for _, uri := range uris {
					text, _, ok := s.read(uri)
					if ok && text != last[uri] {
						last[uri] = text
						s.notify("notifications/resources/updated", map[string]any{
							"_meta": map[string]any{metaPrefix + "subscriptionId": id}, "uri": uri})
					}
				}
			}
		}
	}()
	return nil, nil
}

// canonical 返回请求 id 的规范形式（数字按数值，如 1.0 与 1 相同），使订阅与取消通知中的 id 可以比较。
func canonical(id json.RawMessage) string {
	var v any
	if json.Unmarshal(id, &v) != nil {
		return string(id)
	}
	b, _ := json.Marshal(v)
	return string(b)
}

func nonNil(s []string) []string {
	if s == nil {
		return []string{}
	}
	return s
}

// cancel 处理 notifications/cancelled：requestId 指向订阅时结束该订阅。
func (s *server) cancel(raw json.RawMessage) {
	var p struct {
		RequestID json.RawMessage `json:"requestId"`
	}
	if json.Unmarshal(raw, &p) != nil || len(p.RequestID) == 0 {
		return
	}
	s.subsMu.Lock()
	sub := s.subs[canonical(p.RequestID)]
	delete(s.subs, canonical(p.RequestID))
	s.subsMu.Unlock()
	if sub != nil {
		close(sub.stop)
	}
}

// reply 写出一个 2026-07-28 响应：成功结果另含 resultType 与服务身份。
func (s *server) reply(id json.RawMessage, result map[string]any, e *rpcError) {
	if e == nil {
		result["resultType"] = "complete"
		meta, _ := result["_meta"].(map[string]any)
		if meta == nil {
			meta = map[string]any{}
		}
		meta[metaPrefix+"serverInfo"] = map[string]any{"name": "wa-cli", "version": s.cfg.Version}
		result["_meta"] = meta
	}
	s.respond(id, result, e)
}

// respond 原样写出一个响应（旧版会话的结果不加 2026-07-28 的字段）。
func (s *server) respond(id json.RawMessage, result map[string]any, e *rpcError) {
	if len(id) == 0 {
		id = json.RawMessage("null")
	}
	msg := map[string]any{"jsonrpc": "2.0", "id": id}
	if e != nil {
		msg["error"] = e
	} else {
		msg["result"] = result
	}
	s.write(msg)
}

func (s *server) notify(method string, params map[string]any) {
	s.write(map[string]any{"jsonrpc": "2.0", "method": method, "params": params})
}

// write 把一条消息编码为一行写到标准输出。
func (s *server) write(msg map[string]any) {
	var b bytes.Buffer
	enc := json.NewEncoder(&b)
	enc.SetEscapeHTML(false)
	if enc.Encode(msg) != nil {
		return
	}
	s.writeMu.Lock()
	defer s.writeMu.Unlock()
	s.out.Write(b.Bytes())
}
