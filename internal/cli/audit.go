package cli

import (
	"encoding/json"
	"fmt"
	"io"
	"os"
	"path/filepath"
	"strings"
	"sync"
	"time"

	"github.com/kw12121212/writing-anything/internal/preview"
)

// 审计日志（Audit Log）：wa-cli 每次调用（含失败与用法错误）向 .writing-anything/audit.log 追加一行 JSONL 的
// 运维痕迹。它是回捞（next 的失败重试提示）与作者知情（阅读页的最近失败摘要）的数据源，不是产物：不进 JSON Schema
// 校验、不进 changedPaths（cli 主规格 audit-log）。追加用单次写入保证行完整性；超过 1 MiB 时轮转一份 audit.log.1。
const (
	auditFileName = "audit.log"
	auditMaxBytes = 1 << 20
	auditArgMax   = 200
)

// auditMu 保护 auditCodes（mcp 服务在进程内并发运行命令时也经此暂存诊断码）。
var auditMu sync.Mutex

// auditCodes 是本次调用经 finish / usageError 写出的诊断错误码；auditUnit 是关键命令已解析的首个位置参数
// （单元引用）。两者由顶层钩子随审计记录落盘。
var (
	auditCodes []string
	auditUnit  string
)

// auditSuppressed 为 true 时不写审计（MCP 服务为资源订阅轮询执行只读命令时经 runQuiet 置位）。
var auditSuppressed bool

// auditRememberCodes 记录本次调用写出的诊断错误码（先清后记，每命令一次）。
func auditRememberCodes(ds []Diagnostic) {
	codes := make([]string, 0, len(ds))
	for _, d := range ds {
		codes = append(codes, d.Code)
	}
	auditMu.Lock()
	auditCodes = codes
	auditMu.Unlock()
}

// auditRecord 是 audit.log 中的一行。
type auditRecord struct {
	At      string   `json:"at"`
	Command string   `json:"command"`
	Args    []string `json:"args"`
	Exit    int      `json:"exit"`
	Codes   []string `json:"codes"`
	Unit    string   `json:"unit,omitempty"`
}

// auditEntry 是 next 回捞与阅读页摘要读取的已解析记录。
type auditEntry struct {
	auditRecord
	AtInt int64
}

// criticalCommands 是回捞关注的命令：失败后无同对成功重试时 next 给出提示。
var criticalCommands = map[string]bool{"draft write": true, "review record": true, "accept": true, "export": true}

// appendAudit 在一次调用结束时追加审计记录。没有工作区不写；写入失败只向 stderr 写一行说明，
// 不改变退出码与输出契约（运维痕迹尽力而为）。
func appendAudit(stderr io.Writer, s commandSet, args []string, exit int, now time.Time) {
	auditMu.Lock()
	suppressed := auditSuppressed
	auditMu.Unlock()
	if suppressed {
		return
	}
	dir := auditProjectDir(args)
	if dir == "" {
		return
	}
	if info, err := os.Lstat(filepath.Join(dir, ".writing-anything")); err != nil || !info.IsDir() {
		return
	}
	name := ""
	if named := rootVersionAlias(args); len(named) > 0 && !strings.HasPrefix(named[0], "-") {
		name = named[0]
		if c, _ := s.find(named); c != nil {
			name = c.name
		}
	}
	rec := auditRecord{At: now.UTC().Format("2006-01-02T15:04:05Z"), Command: name, Exit: exit,
		Args: make([]string, 0, len(args)), Codes: []string{}}
	for _, a := range args {
		if r := []rune(a); len(r) > auditArgMax {
			a = string(r[:auditArgMax])
		}
		rec.Args = append(rec.Args, a)
	}
	auditMu.Lock()
	rec.Codes = append([]string{}, auditCodes...)
	rec.Unit = auditUnit
	auditMu.Unlock()
	line, err := json.Marshal(rec)
	if err != nil {
		return
	}
	path := filepath.Join(dir, ".writing-anything", auditFileName)
	if info, err := os.Stat(path); err == nil && info.Size() >= auditMaxBytes {
		_ = os.Rename(path, path+".1") // 轮转一份替换旧备份；失败则继续追加
	}
	f, err := os.OpenFile(path, os.O_APPEND|os.O_CREATE|os.O_WRONLY, 0o644)
	if err != nil {
		fmt.Fprintf(stderr, "审计日志（Audit Log）无法写入：%v\n", err)
		return
	}
	defer f.Close()
	if _, err := f.Write(append(line, '\n')); err != nil {
		fmt.Fprintf(stderr, "审计日志（Audit Log）无法写入：%v\n", err)
	}
}

// auditProjectDir 从原始参数解析项目目录（--dir <dir> 或 --dir=<dir>，缺省当前目录）；无法判定时返回空。
func auditProjectDir(args []string) string {
	dir := "."
	for i, a := range args {
		if a == "--dir" && i+1 < len(args) {
			dir = args[i+1]
			break
		}
		if v, ok := strings.CutPrefix(a, "--dir="); ok {
			dir = v
			break
		}
	}
	if dir == "" {
		return ""
	}
	return dir
}

// readAudit 解析 dir 的审计日志（含轮转的 .1），坏行跳过；按时间原序返回（旧文件在前）。
func readAudit(dir string) []auditEntry {
	var out []auditEntry
	for _, name := range []string{auditFileName + ".1", auditFileName} {
		data, err := os.ReadFile(filepath.Join(dir, ".writing-anything", name))
		if err != nil {
			continue
		}
		for _, line := range strings.Split(string(data), "\n") {
			if line == "" {
				continue
			}
			var e auditEntry
			if json.Unmarshal([]byte(line), &e.auditRecord) != nil {
				continue
			}
			if t, err := time.Parse("2006-01-02T15:04:05Z", e.At); err == nil {
				e.AtInt = t.Unix()
			}
			out = append(out, e)
		}
	}
	return out
}

// auditRecoveries 返回需要回捞的（命令、单元）对：最后一次调用失败且其后没有同对的成功调用。
func auditRecoveries(dir string) map[string]string {
	out := map[string]string{}
	for _, e := range readAudit(dir) {
		if !criticalCommands[e.Command] || e.Unit == "" {
			continue
		}
		key := e.Command + "\x00" + e.Unit
		if e.Exit == 0 {
			delete(out, key)
		} else {
			out[key] = e.Unit
		}
	}
	return out
}

// auditFailureSummary 返回最近 5 条失败调用（新的在后）供阅读页只读展示（preview-audit-summary）。
func auditFailureSummary(dir string) []preview.AuditFailure {
	var out []preview.AuditFailure
	for _, e := range readAudit(dir) {
		if e.Exit != 0 {
			out = append(out, preview.AuditFailure{At: e.At, Command: e.Command, Exit: e.Exit})
		}
	}
	if len(out) > 5 {
		out = out[len(out)-5:]
	}
	return out
}
