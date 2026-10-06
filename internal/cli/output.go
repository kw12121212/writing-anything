package cli

import (
	"encoding/json"
	"io"
)

// 诊断的严重程度。
const (
	severityError   = "error"
	severityWarning = "warning"
)

// Diagnostic 是一条诊断（Diagnostic）：稳定的错误码、说明、相关产物与字段，以及可执行的下一步命令。
type Diagnostic struct {
	Code     string   `json:"code"`
	Severity string   `json:"severity"`
	Message  string   `json:"message"`
	Path     string   `json:"path,omitempty"`
	Field    string   `json:"field,omitempty"`
	Next     []string `json:"next"`
}

// Result 是所有命令 --json 输出共用的输出信封（Output Envelope）。
// 命令专属字段通过嵌入 Result 的结构体与之并列放在顶层。
type Result struct {
	Valid        bool         `json:"valid"`
	Diagnostics  []Diagnostic `json:"diagnostics"`
	ChangedPaths []string     `json:"changedPaths"`
}

func (r *Result) envelope() *Result { return r }

type enveloped interface{ envelope() *Result }

// writeJSON 补齐信封（数组永不为 null，valid 当且仅当没有 error 级诊断），并把 v 编码为一行 JSON 写到 w。
// 每条诊断的 Next 由产生它的代码负责填写，至少一条。
func writeJSON(w io.Writer, v enveloped) error {
	r := v.envelope()
	if r.Diagnostics == nil {
		r.Diagnostics = []Diagnostic{}
	}
	if r.ChangedPaths == nil {
		r.ChangedPaths = []string{}
	}
	r.Valid = true
	for i := range r.Diagnostics {
		if r.Diagnostics[i].Severity == severityError {
			r.Valid = false
		}
	}
	enc := json.NewEncoder(w)
	enc.SetEscapeHTML(false)
	return enc.Encode(v)
}
