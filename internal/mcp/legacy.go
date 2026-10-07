package mcp

import (
	"encoding/json"
	"fmt"
	"slices"
	"strings"
	"sync"
	"time"
)

// 旧版会话（Legacy Session）：只发送 initialize 的客户端（MCP 2025-11-25 与 2025-06-18）在该 stdio 进程内的会话。
// 握手、initialized 门禁与按 URI 的资源订阅在本文件中实现；tool、资源与 prompt 的业务映射与 2026-07-28 共用（server.common）。

// legacyVersions 是旧版会话可协商的协议版本，第一个为客户端请求其他版本时的回复。
var legacyVersions = []string{"2025-11-25", "2025-06-18"}

type legacySession struct {
	ready bool // 已收到 notifications/initialized；只在读取消息的 goroutine 中读写

	mu      sync.Mutex
	subs    map[string]string // 订阅的 URI → 上次读取的内容
	polling bool
}

// supportedText 是 initialize 错误中列出的支持版本。
func supportedText() string {
	return "支持的协议版本：" + strings.Join(SupportedVersions, "、")
}

// initialize 处理旧版握手：协商版本并在该进程内建立旧版会话。
func (s *server) initialize(raw json.RawMessage) (map[string]any, *rpcError) {
	data := map[string]any{"supported": SupportedVersions}
	if s.legacy != nil {
		return nil, &rpcError{Code: codeInvalidReq, Message: "该进程已完成 initialize，不能再次初始化；" + supportedText(), Data: data}
	}
	params, perr := parseParams(raw)
	requested, ok := params["protocolVersion"].(string)
	if perr != nil || !ok {
		return nil, &rpcError{Code: codeInvalidArgs, Message: "initialize 的 params.protocolVersion 必须是字符串；" + supportedText(), Data: data}
	}
	version := legacyVersions[0]
	if slices.Contains(legacyVersions, requested) {
		version = requested
	}
	s.legacy = &legacySession{subs: map[string]string{}}
	return map[string]any{
		"protocolVersion": version,
		"capabilities":    capabilities(),
		"serverInfo":      map[string]any{"name": "wa-cli", "version": s.cfg.Version},
		"instructions":    s.cfg.Instructions,
	}, nil
}

// awaitingInitialized 报告旧版会话已 initialize 而尚未收到 notifications/initialized 时，该请求是否必须被拒绝（ping 除外）。
func (s *server) awaitingInitialized(m message) bool {
	return s.legacy != nil && !s.legacy.ready && m.Method != "ping"
}

// handleLegacy 按旧版语义处理一个请求并回复（initialized 之前的请求已由 handle 拒绝）。
func (s *server) handleLegacy(m message, params map[string]any) {
	l := s.legacy
	switch m.Method {
	case "ping":
		s.respond(m.ID, map[string]any{}, nil)
		return
	case "resources/subscribe":
		result, rerr := s.subscribe(params)
		s.respond(m.ID, result, rerr)
		return
	case "resources/unsubscribe":
		uri, _ := params["uri"].(string)
		l.mu.Lock()
		delete(l.subs, uri)
		l.mu.Unlock()
		s.respond(m.ID, map[string]any{}, nil)
		return
	}
	if result, rerr, ok := s.common(m.Method, params); ok {
		s.respond(m.ID, result, rerr)
		return
	}
	s.respond(m.ID, nil, &rpcError{Code: codeNoMethod, Message: "Method not found: " + m.Method})
}

// subscribe 处理 resources/subscribe：记录 URI 与当前内容，第一次订阅时开始轮询。
func (s *server) subscribe(params map[string]any) (map[string]any, *rpcError) {
	l := s.legacy
	uri, _ := params["uri"].(string)
	text, _, ok := s.read(uri)
	if !ok {
		return nil, notFound(uri)
	}
	l.mu.Lock()
	defer l.mu.Unlock()
	if _, dup := l.subs[uri]; !dup {
		if len(l.subs) >= MaxSubscribedURI {
			return nil, &rpcError{Code: codeInvalidArgs, Message: fmt.Sprintf("旧版会话（Legacy Session）至多订阅 %d 个资源", MaxSubscribedURI)}
		}
		l.subs[uri] = text
	}
	if !l.polling {
		l.polling = true
		s.wg.Add(1)
		go s.pollLegacy(l)
	}
	return map[string]any{}, nil
}

// pollLegacy 按间隔读取订阅的资源，变化时发送 notifications/resources/updated，直到服务结束。
func (s *server) pollLegacy(l *legacySession) {
	defer s.wg.Done()
	ticker := time.NewTicker(s.cfg.Interval)
	defer ticker.Stop()
	for {
		select {
		case <-s.done:
			return
		case <-ticker.C:
			l.mu.Lock()
			uris := make([]string, 0, len(l.subs))
			for uri := range l.subs {
				uris = append(uris, uri)
			}
			l.mu.Unlock()
			slices.Sort(uris)
			for _, uri := range uris {
				text, _, ok := s.read(uri)
				l.mu.Lock()
				last, subscribed := l.subs[uri]
				if ok && subscribed && text != last {
					l.subs[uri] = text
					// 持有 l.mu 发送，使 resources/unsubscribe 的回复之后不再出现该 URI 的通知。
					s.notify("notifications/resources/updated", map[string]any{"uri": uri})
				}
				l.mu.Unlock()
			}
		}
	}
}
