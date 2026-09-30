package cli

import (
	"fmt"
	"io"
	"strings"

	"github.com/kw12121212/writing-anything/internal/schema"
	"github.com/kw12121212/writing-anything/internal/workspace"
)

var dirFlag = flagSpec{"dir", flagString, "项目目录，即包含 .writing-anything/ 的目录；默认为当前目录"}

var initCommand = &command{
	name:    "init",
	summary: "创建工作区（Workspace）.writing-anything/；重复运行只补齐缺失的文件与目录",
	flags: []flagSpec{
		dirFlag,
		{"language", flagString, "配置的语言标签，默认 zh-CN"},
		{"genre", flagString, "默认文体（Genre）id"},
		{"gate-level", flagString, "关卡强度（Gate Level）：strict、standard 或 fast，默认 standard"},
	},
	outputKeys: []outputKey{{"workspace", "相对项目目录的工作区路径"}},
	exitCodes:  standardExitCodes,
	run:        runInit,
}

var validateCommand = &command{
	name:       "validate",
	summary:    "按 JSON Schema 校验整个工作区或单个产物（Artifact）",
	args:       []argSpec{{"path", "要校验的产物路径，相对项目目录或为绝对路径；省略时校验整个工作区", false}},
	flags:      []flagSpec{dirFlag},
	outputKeys: []outputKey{{"checked", "已校验的产物路径数组，相对项目目录"}},
	exitCodes:  standardExitCodes,
	run:        runValidate,
}

var statusCommand = &command{
	name:    "status",
	summary: "汇总工作区的配置、素材与作品数量以及校验结果",
	flags:   []flagSpec{dirFlag},
	outputKeys: []outputKey{
		{"workspace", "相对项目目录的工作区路径"},
		{"config", "配置摘要，含 language、defaultGenre、packs、gateLevel；配置无法解析时为 null"},
		{"sources", "sources/ 下的直接条目数，不含隐藏条目"},
		{"works", "works/ 下的直接条目数，不含隐藏条目"},
	},
	exitCodes: standardExitCodes,
	run:       runStatus,
}

var configSetCommand = &command{
	name:    "config set",
	summary: "修改工作区配置中的一项：language、default_genre、packs 或 gate_level",
	args: []argSpec{
		{"key", "配置项：language、default_genre、packs 或 gate_level", true},
		{"value", "新取值；packs 用逗号分隔，空字符串表示清空；default_genre 为空字符串时删除该项", true},
	},
	flags:      []flagSpec{dirFlag},
	outputKeys: []outputKey{{"config", "修改后（未修改时为当前）的配置摘要；配置无法解析时为 null"}},
	exitCodes:  standardExitCodes,
	run:        runConfigSet,
}

type initResult struct {
	Result
	Workspace string `json:"workspace"`
}

type validateResult struct {
	Result
	Checked []string `json:"checked"`
}

type statusResult struct {
	Result
	Workspace string            `json:"workspace"`
	Config    *workspace.Config `json:"config"`
	Sources   int               `json:"sources"`
	Works     int               `json:"works"`
}

type configSetResult struct {
	Result
	Config *workspace.Config `json:"config"`
}

func runInit(inv *invocation) int {
	dir := projectDir(inv)
	set := map[string]string{}
	for flag, key := range map[string]string{"language": "language", "genre": "default_genre", "gate-level": "gate_level"} {
		if v, ok := inv.values[flag]; ok {
			set[key] = v
		}
	}
	created, problems := workspace.Init(dir, set)
	res := &initResult{Result: Result{ChangedPaths: created, Diagnostics: diagnostics(problems, dir)}, Workspace: workspace.Dir}
	for i, p := range problems {
		switch p.Code {
		case workspace.CodeConfigKept:
			res.Diagnostics[i].Next = []string{shellCommand("wa-cli", "config", "set", p.Field, set[p.Field], "--dir", dir)}
		case schema.CodeSchemaViolation:
			// 工作区尚未创建，修复命令是换一个取值重新运行 init。
			var next []string
			if p.Field == "gate_level" {
				for _, level := range workspace.GateLevels {
					next = append(next, shellCommand("wa-cli", "init", "--dir", dir, "--gate-level", level, "--json"))
				}
			}
			res.Diagnostics[i].Next = append(next, "wa-cli help init --json")
		}
	}
	return finish(inv, res, func(w io.Writer) error {
		if hasError(res.Diagnostics) {
			return nil
		}
		if len(created) == 0 {
			_, err := fmt.Fprintf(w, "工作区（Workspace）%s 已存在，没有需要补齐的文件\n", workspace.Dir)
			return err
		}
		for _, p := range created {
			if _, err := fmt.Fprintf(w, "已创建 %s\n", p); err != nil {
				return err
			}
		}
		return nil
	})
}

func runValidate(inv *invocation) int {
	dir := projectDir(inv)
	res := &validateResult{Checked: []string{}}
	problems := workspace.Require(dir)
	if problems == nil {
		var checked []string
		if len(inv.args) == 1 {
			checked, problems = workspace.ValidateArtifact(dir, inv.args[0])
		} else {
			checked, problems = workspace.Check(dir)
		}
		res.Checked = append(res.Checked, checked...)
	}
	res.Diagnostics = diagnostics(problems, dir)
	return finish(inv, res, func(w io.Writer) error {
		if hasError(res.Diagnostics) {
			return nil
		}
		_, err := fmt.Fprintf(w, "校验通过：%s\n", strings.Join(res.Checked, "、"))
		return err
	})
}

func runStatus(inv *invocation) int {
	dir := projectDir(inv)
	res := &statusResult{Workspace: workspace.Dir}
	problems := workspace.Require(dir)
	if problems == nil {
		res.Config, _ = workspace.Load(dir)
		_, problems = workspace.Check(dir)
		res.Sources, res.Works = workspace.Summary(dir)
	}
	res.Diagnostics = diagnostics(problems, dir)
	return finish(inv, res, func(w io.Writer) error {
		if res.Config == nil && hasError(res.Diagnostics) && problems[0].Code == workspace.CodeNotFound {
			return nil
		}
		var b strings.Builder
		fmt.Fprintf(&b, "工作区（Workspace）：%s\n", res.Workspace)
		if c := res.Config; c != nil {
			genre, packs := "未设置", "无"
			if c.DefaultGenre != nil {
				genre = *c.DefaultGenre
			}
			if len(c.Packs) > 0 {
				packs = strings.Join(c.Packs, "、")
			}
			fmt.Fprintf(&b, "语言：%s\n默认文体（Genre）：%s\n规范包（Pack）：%s\n关卡强度（Gate Level）：%s\n", c.Language, genre, packs, c.GateLevel)
		}
		fmt.Fprintf(&b, "素材（Source）：%d\n作品（Work）：%d\n", res.Sources, res.Works)
		if !hasError(res.Diagnostics) {
			b.WriteString("校验通过\n")
		}
		_, err := io.WriteString(w, b.String())
		return err
	})
}

func runConfigSet(inv *invocation) int {
	dir := projectDir(inv)
	key, value := inv.args[0], inv.args[1]
	res := &configSetResult{}
	problems := workspace.Require(dir)
	var changed bool
	if problems == nil {
		res.Config, changed, problems = workspace.SetConfig(dir, key, value)
	}
	if changed {
		res.ChangedPaths = []string{workspace.ConfigPath}
	}
	res.Diagnostics = diagnostics(problems, dir)
	return finish(inv, res, func(w io.Writer) error {
		var err error
		switch {
		case changed:
			_, err = fmt.Fprintf(w, "已把 %s 设为 %q\n", key, value)
		case !hasError(res.Diagnostics):
			_, err = fmt.Fprintf(w, "%s 已是 %q，未修改\n", key, value)
		}
		return err
	})
}

func projectDir(inv *invocation) string {
	if d := inv.values["dir"]; d != "" {
		return d
	}
	return "."
}

// diagnostics 把工作区问题转换为诊断，并按错误码填写下一步命令。
func diagnostics(problems []workspace.Problem, dir string) []Diagnostic {
	var out []Diagnostic
	for _, p := range problems {
		var loc []string
		if p.Line > 0 {
			loc = append(loc, fmt.Sprintf("第 %d 行", p.Line))
		}
		if p.Field != "" {
			loc = append(loc, p.Field)
		}
		msg := p.Path
		if len(loc) > 0 {
			msg = strings.TrimSpace(msg + " " + strings.Join(loc, " "))
		}
		msg += "：" + p.Message
		if key := configKey(p); key != "" && p.Code == schema.CodeSchemaViolation {
			msg += "；可用 wa-cli config set " + key + " <value> 修改"
		}
		out = append(out, Diagnostic{
			Code: p.Code, Severity: p.Severity, Message: msg,
			Path: p.Path, Field: p.Field, Next: next(p, dir),
		})
	}
	return out
}

// next 返回可以直接运行的下一步命令，不含需要替换的占位符。
func next(p workspace.Problem, dir string) []string {
	initCmd := shellCommand("wa-cli", "init", "--dir", dir, "--json")
	validate := shellCommand("wa-cli", "validate", workspace.ConfigPath, "--dir", dir, "--json")
	if schema.KindFor(p.Path) != nil {
		validate = shellCommand("wa-cli", "validate", p.Path, "--dir", dir, "--json")
	}
	switch p.Code {
	case workspace.CodeNotFound, workspace.CodeMissingDirectory, schema.CodeNotFound:
		return []string{initCmd}
	case schema.CodeSchemaViolation:
		var out []string
		switch key := configKey(p); key {
		case "":
		case "gate_level":
			for _, level := range workspace.GateLevels {
				out = append(out, shellCommand("wa-cli", "config", "set", key, level, "--dir", dir, "--json"))
			}
		default:
			out = append(out, "wa-cli help config set --json")
		}
		return append(out, validate)
	case schema.CodeUnknownKind:
		return []string{"wa-cli help validate --json"}
	case workspace.CodeUnknownConfigKey, workspace.CodeConfigKept, workspace.CodeAnchoredValue:
		return []string{"wa-cli help config set --json"}
	case workspace.CodeWriteFailed, workspace.CodeReadFailed:
		if p.Path != workspace.ConfigPath {
			return []string{initCmd}
		}
	}
	return []string{validate}
}

// configKey 返回配置问题所在的可设置配置项；不是配置项的问题返回空字符串。
func configKey(p workspace.Problem) string {
	if p.Path != workspace.ConfigPath {
		return ""
	}
	key, _, _ := strings.Cut(p.Field, ".")
	key, _, _ = strings.Cut(key, "[")
	for _, k := range workspace.SettableKeys {
		if k == key {
			return key
		}
	}
	return ""
}

// shellCommand 把参数拼成一条可以直接粘贴到 shell 中运行的命令，必要时加单引号。
func shellCommand(args ...string) string {
	quoted := make([]string, len(args))
	for i, a := range args {
		if a != "" && !strings.ContainsAny(a, " \t\n'\"\\$`!*?[]{}()<>|&;#~") {
			quoted[i] = a
		} else {
			quoted[i] = "'" + strings.ReplaceAll(a, "'", `'\''`) + "'"
		}
	}
	return strings.Join(quoted, " ")
}

func hasError(ds []Diagnostic) bool {
	for _, d := range ds {
		if d.Severity == severityError {
			return true
		}
	}
	return false
}

// finish 写出结果：JSON 模式写输出信封，文本模式用 text 写标准输出、把诊断写到标准错误。
// 存在 error 级诊断时返回退出码 1。
func finish(inv *invocation, v enveloped, text func(io.Writer) error) int {
	r := v.envelope()
	var err error
	if inv.json() {
		err = writeJSON(inv.stdout, v)
	} else if err = text(inv.stdout); err == nil {
		err = writeDiagnostics(inv.stderr, r.Diagnostics)
	}
	if err != nil {
		return writeFailed(inv.stderr, err)
	}
	if hasError(r.Diagnostics) {
		return exitError
	}
	return exitOK
}

// writeDiagnostics 以人读文本写出诊断及其下一步命令。
func writeDiagnostics(w io.Writer, ds []Diagnostic) error {
	var b strings.Builder
	for _, d := range ds {
		label := "错误"
		if d.Severity == severityWarning {
			label = "警告"
		}
		fmt.Fprintf(&b, "%s [%s] %s\n", label, d.Code, d.Message)
		for _, n := range d.Next {
			fmt.Fprintf(&b, "  下一步：%s\n", n)
		}
	}
	_, err := io.WriteString(w, b.String())
	return err
}
