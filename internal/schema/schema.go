// Package schema 用嵌入二进制的 JSON Schema 校验工作区（Workspace）中的结构化产物（Artifact）。
// 产物类型由产物相对项目目录的路径决定；所有命令都经 Kind.Validate 或 Kind.ValidateNode 校验产物。
package schema

import (
	"bytes"
	"embed"
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"path"
	"regexp"
	"slices"
	"strconv"
	"strings"
	"sync"
	"time"

	"github.com/santhosh-tekuri/jsonschema/v6"
	"github.com/santhosh-tekuri/jsonschema/v6/kind"
	"go.yaml.in/yaml/v3"
)

// 产物的错误码（Error Code）。已发布的错误码不得改名或复用。
const (
	CodeNotFound        = "artifact.not-found"
	CodeUnknownKind     = "artifact.unknown-kind"
	CodeParseError      = "artifact.parse-error"
	CodeSchemaViolation = "artifact.schema-violation"
	CodeExecutable      = "artifact.executable-content"
)

//go:embed schemas/*.schema.json
var files embed.FS

// Kind 是一种产物类型：按相对项目目录、以 / 分隔的路径模式识别，用一份嵌入的 JSON Schema 校验。
type Kind struct {
	Name    string // 产物类型名，如 config
	Pattern string // path.Match 语法的路径模式；为空时不对应任何工作区路径，只作为内置数据校验
	File    string // schemas/ 下的 Schema 文件名
	compile func() (*jsonschema.Schema, error)
}

// 文体、规范包与工作区配置都只能包含数据，校验时先做可执行内容检查。
var kinds = []*Kind{
	newKind("config", ".writing-anything/config.yaml", "config.schema.json"),
	newKind("pack", ".writing-anything/packs/*.yaml", "pack.schema.json"),
	newKind("genre", "", "genre.schema.json"),
	newKind("work", ".writing-anything/works/*/work.yaml", "work.schema.json"),
}

func newKind(name, pattern, file string) *Kind {
	return &Kind{Name: name, Pattern: pattern, File: file, compile: sync.OnceValues(func() (*jsonschema.Schema, error) {
		return compileFile(file)
	})}
}

// schemaURL 是 Schema 文件的资源地址；所有资源都预先登记，编译时不会访问网络。
func schemaURL(file string) string { return "https://writing-anything.invalid/schemas/" + file }

// compileFile 编译 file；schemas/ 下的其他文件都登记为资源，供 $ref 引用共用的定义。
func compileFile(file string) (*jsonschema.Schema, error) {
	entries, err := files.ReadDir("schemas")
	if err != nil {
		return nil, err
	}
	c := jsonschema.NewCompiler()
	for _, e := range entries {
		data, err := files.ReadFile("schemas/" + e.Name())
		if err != nil {
			return nil, err
		}
		doc, err := jsonschema.UnmarshalJSON(bytes.NewReader(data))
		if err != nil {
			return nil, fmt.Errorf("%s: %w", e.Name(), err)
		}
		if err := c.AddResource(schemaURL(e.Name()), doc); err != nil {
			return nil, err
		}
	}
	return c.Compile(schemaURL(file))
}

// Kinds 返回全部已登记的产物类型。
func Kinds() []*Kind { return slices.Clone(kinds) }

// KindFor 返回 rel（相对项目目录、以 / 分隔的路径）对应的产物类型；未登记时返回 nil。
func KindFor(rel string) *Kind {
	for _, k := range kinds {
		if ok, _ := path.Match(k.Pattern, rel); ok && k.Pattern != "" {
			return k
		}
	}
	return nil
}

// Named 返回名为 name 的产物类型；未登记时返回 nil。
func Named(name string) *Kind {
	for _, k := range kinds {
		if k.Name == name {
			return k
		}
	}
	return nil
}

// Problem 是校验产物时发现的一个问题。
type Problem struct {
	Code    string // CodeParseError 或 CodeSchemaViolation
	Field   string // 点分字段路径，数组下标写作 [i]；指向产物根部时为空
	Line    int    // YAML 行号；未知时为 0
	Message string // 问题说明，不含行号与字段路径
}

// Validate 解析 data 并按 k 的 Schema 校验。
func (k *Kind) Validate(data []byte) []Problem {
	doc, p := Parse(data)
	if p != nil {
		return []Problem{*p}
	}
	return k.ValidateNode(doc)
}

// ValidateNode 按 k 的 Schema 校验已解析的 YAML 文档。文档含可执行内容时只报告这些内容，不再做 Schema 校验。
func (k *Kind) ValidateNode(doc *yaml.Node) []Problem {
	if ps := Executable(doc); len(ps) > 0 {
		return ps
	}
	sch, err := k.compile()
	if err != nil {
		// 嵌入的 Schema 由测试保证可编译；走到这里说明二进制本身有缺陷。
		panic(fmt.Sprintf("嵌入的 Schema %s 无法编译：%v", k.File, err))
	}
	d := &decoder{locs: map[string]location{}}
	v := d.value(doc, nil, "", 0)
	var verr *jsonschema.ValidationError
	if err := sch.Validate(v); !errors.As(err, &verr) {
		return nil
	}
	var out []Problem
	for _, e := range leaves(verr) {
		out = append(out, d.problems(e)...)
	}
	slices.SortStableFunc(out, func(a, b Problem) int {
		if a.Line != b.Line {
			return a.Line - b.Line
		}
		return strings.Compare(a.Field, b.Field)
	})
	return out
}

// Decode 把已解析的 YAML 文档转换为 JSON 兼容的值（map[string]any、[]any、标量）。
func Decode(doc *yaml.Node) any {
	if doc == nil {
		return nil
	}
	return (&decoder{locs: map[string]location{}}).value(doc, nil, "", 0)
}

var yamlLine = regexp.MustCompile(`^(?:yaml: )?(?:line (\d+): )?(.*)$`)

// parserProblems 是 YAML 解析器（而非扫描器）报告的问题。解析器报告的行号从 0 开始，且为 0 时省略，
// 需要换算成从 1 开始的行号；扫描器报告的行号已经从 1 开始。
var parserProblems = map[string]bool{
	"did not find expected ',' or ']'":       true,
	"did not find expected ',' or '}'":       true,
	"did not find expected '-' indicator":    true,
	"did not find expected <document start>": true,
	"did not find expected key":              true,
	"did not find expected node content":     true,
	"found duplicate %TAG directive":         true,
	"found duplicate %YAML directive":        true,
	"found incompatible YAML document":       true,
	"found undefined tag handle":             true,
}

// Parse 把 data 解析为 YAML 文档节点。语法错误、多个文档、重复字段或非标量字段名都报告为 CodeParseError。
// 空文件解析为空文档。
func Parse(data []byte) (*yaml.Node, *Problem) {
	dec := yaml.NewDecoder(bytes.NewReader(data))
	var doc yaml.Node
	if err := dec.Decode(&doc); err != nil && !errors.Is(err, io.EOF) {
		return nil, parseProblem(err)
	}
	var extra yaml.Node
	if err := dec.Decode(&extra); !errors.Is(err, io.EOF) {
		if err != nil {
			return nil, parseProblem(err)
		}
		return nil, &Problem{Code: CodeParseError, Line: extra.Line, Message: "出现了第二个 YAML 文档；每个产物只能包含一个文档"}
	}
	if p := checkKeys(&doc); p != nil {
		return nil, p
	}
	if expandedSize(&doc, map[*yaml.Node]int{}) > MaxNodes {
		return nil, &Problem{Code: CodeParseError, Message: fmt.Sprintf("展开别名后超过 %d 个节点，拒绝处理", MaxNodes)}
	}
	return &doc, nil
}

// MaxNodes 是产物展开别名后允许的最大节点数，防止少量别名层层引用耗尽内存。
const MaxNodes = 100000

// expandedSize 计算展开别名后的节点数；memo 记录已算过的节点，计算本身不随展开规模增长。
func expandedSize(n *yaml.Node, memo map[*yaml.Node]int) int {
	if n.Kind == yaml.AliasNode {
		n = n.Alias
	}
	if size, ok := memo[n]; ok {
		return size
	}
	memo[n] = 1 // 先占位，防止异常的自引用导致无限递归
	size := 1
	for _, c := range n.Content {
		if size += expandedSize(c, memo); size > MaxNodes {
			break
		}
	}
	memo[n] = size
	return size
}

func parseProblem(err error) *Problem {
	m := yamlLine.FindStringSubmatch(err.Error())
	line, _ := strconv.Atoi(m[1])
	if parserProblems[m[2]] {
		line++
	}
	return &Problem{Code: CodeParseError, Line: line, Message: "YAML 语法错误：" + m[2]}
}

// checkKeys 检查映射的字段名都是标量且不重复。不进入别名，避免重复检查同一节点。
func checkKeys(n *yaml.Node) *Problem {
	if n.Kind == yaml.MappingNode {
		seen := map[string]int{}
		for i := 0; i+1 < len(n.Content); i += 2 {
			k := n.Content[i]
			if k.Kind != yaml.ScalarNode {
				return &Problem{Code: CodeParseError, Line: k.Line, Message: "字段名必须是标量"}
			}
			if isMerge(k) {
				continue
			}
			if first, dup := seen[k.Value]; dup {
				return &Problem{Code: CodeParseError, Line: k.Line, Message: fmt.Sprintf("字段 %s 重复，首次出现在第 %d 行", k.Value, first)}
			}
			seen[k.Value] = k.Line
		}
	}
	for _, c := range n.Content {
		if p := checkKeys(c); p != nil {
			return p
		}
	}
	return nil
}

// dataTags 是 YAML 核心 Schema 的标签；其他标签可能被某些解析器解释为对象构造或代码，一律视为可执行内容。
var dataTags = []string{"!!str", "!!int", "!!float", "!!bool", "!!null", "!!map", "!!seq", "!!timestamp", "!!merge"}

// ExecutableKeys 是在任意深度出现即视为可执行内容的字段名，比较时不区分大小写。
var ExecutableKeys = []string{"script", "scripts", "command", "commands", "exec", "shell", "hook", "hooks", "plugin", "plugins", "run", "eval"}

// Executable 报告文档中的可执行内容：YAML 核心 Schema 以外的标签，以及名为 ExecutableKeys 的字段（不区分大小写）。
// 不进入别名，被引用的节点在其定义处检查。
func Executable(doc *yaml.Node) []Problem {
	var out []Problem
	var walk func(n *yaml.Node, field string)
	walk = func(n *yaml.Node, field string) {
		if n.Kind != yaml.DocumentNode && n.Kind != yaml.AliasNode && !slices.Contains(dataTags, n.ShortTag()) {
			out = append(out, Problem{Code: CodeExecutable, Field: field, Line: n.Line,
				Message: fmt.Sprintf("出现了非数据标签 %s；产物只能包含数据", n.Tag)})
		}
		switch n.Kind {
		case yaml.DocumentNode:
			for _, c := range n.Content {
				walk(c, field)
			}
		case yaml.SequenceNode:
			for i, c := range n.Content {
				walk(c, fmt.Sprintf("%s[%d]", field, i))
			}
		case yaml.MappingNode:
			for i := 0; i+1 < len(n.Content); i += 2 {
				k, v := n.Content[i], n.Content[i+1]
				child := joinField(field, k.Value)
				if isMerge(k) {
					child = field
				} else if slices.Contains(ExecutableKeys, strings.ToLower(k.Value)) {
					out = append(out, Problem{Code: CodeExecutable, Field: child, Line: k.Line,
						Message: fmt.Sprintf("出现了脚本或命令类字段 %s；产物只能包含数据", k.Value)})
				}
				walk(k, child)
				walk(v, child)
			}
		}
	}
	walk(doc, "")
	return out
}

func isMerge(k *yaml.Node) bool { return k.Kind == yaml.ScalarNode && k.ShortTag() == "!!merge" }

// location 记录一个值在 YAML 中的字段路径与行号。
type location struct {
	field string
	line  int
}

// decoder 把 YAML 节点转换为 JSON 兼容的值，并按实例位置记录字段路径与行号，供诊断定位。
type decoder struct {
	locs map[string]location // 键为以 \x00 连接的实例位置
}

func pointer(segs []string) string { return strings.Join(segs, "\x00") }

func joinField(parent, key string) string {
	if parent == "" {
		return key
	}
	return parent + "." + key
}

func (d *decoder) value(n *yaml.Node, ptr []string, field string, line int) any {
	switch n.Kind {
	case yaml.DocumentNode:
		if len(n.Content) == 0 {
			break
		}
		return d.value(n.Content[0], ptr, field, n.Content[0].Line)
	case yaml.AliasNode:
		return d.value(n.Alias, ptr, field, line)
	}
	d.locs[pointer(ptr)] = location{field, line}
	switch n.Kind {
	case yaml.MappingNode:
		m := map[string]any{}
		d.mapping(n, m, ptr, field)
		return m
	case yaml.SequenceNode:
		s := make([]any, len(n.Content))
		for i, c := range n.Content {
			s[i] = d.value(c, append(slices.Clip(ptr), strconv.Itoa(i)), fmt.Sprintf("%s[%d]", field, i), c.Line)
		}
		return s
	case yaml.ScalarNode:
		return scalar(n)
	}
	return nil
}

// mapping 把 n 的字段写入 m：先展开合并键（<<）引用的映射，再写入显式字段，使显式字段覆盖合并来的值。
func (d *decoder) mapping(n *yaml.Node, m map[string]any, ptr []string, field string) {
	for i := 0; i+1 < len(n.Content); i += 2 {
		if k, v := n.Content[i], n.Content[i+1]; isMerge(k) {
			d.merge(v, m, ptr, field)
		}
	}
	for i := 0; i+1 < len(n.Content); i += 2 {
		if k, v := n.Content[i], n.Content[i+1]; !isMerge(k) {
			m[k.Value] = d.value(v, append(slices.Clip(ptr), k.Value), joinField(field, k.Value), k.Line)
		}
	}
}

func (d *decoder) merge(v *yaml.Node, m map[string]any, ptr []string, field string) {
	if v.Kind == yaml.AliasNode {
		v = v.Alias
	}
	switch v.Kind {
	case yaml.MappingNode:
		d.mapping(v, m, ptr, field)
	case yaml.SequenceNode:
		for _, c := range v.Content {
			d.merge(c, m, ptr, field)
		}
	}
}

func scalar(n *yaml.Node) any {
	var v any
	if err := n.Decode(&v); err != nil {
		return n.Value
	}
	if _, ok := v.(time.Time); ok {
		// JSON 没有时间类型，时间戳按原文作为字符串校验。
		return n.Value
	}
	return v
}

// leaves 返回校验错误树的叶子，即具体违反的约束。
func leaves(e *jsonschema.ValidationError) []*jsonschema.ValidationError {
	if len(e.Causes) == 0 {
		return []*jsonschema.ValidationError{e}
	}
	var out []*jsonschema.ValidationError
	for _, c := range e.Causes {
		out = append(out, leaves(c)...)
	}
	return out
}

func (d *decoder) problems(e *jsonschema.ValidationError) []Problem {
	loc := d.locs[pointer(e.InstanceLocation)]
	violation := func(field string, line int, msg string) Problem {
		return Problem{Code: CodeSchemaViolation, Field: field, Line: line, Message: msg}
	}
	switch k := e.ErrorKind.(type) {
	case *kind.Required:
		var out []Problem
		for _, name := range k.Missing {
			out = append(out, violation(joinField(loc.field, name), loc.line, "缺少必需字段 "+name))
		}
		return out
	case *kind.AdditionalProperties:
		var out []Problem
		for _, name := range k.Properties {
			child := d.locs[pointer(append(slices.Clip(e.InstanceLocation), name))]
			out = append(out, violation(child.field, child.line, "未定义的字段 "+name))
		}
		return out
	}
	return []Problem{violation(loc.field, loc.line, describe(e.ErrorKind))}
}

// describe 用中文说明违反的约束；未单独翻译的约束只给出约束关键字。
func describe(ek jsonschema.ErrorKind) string {
	switch k := ek.(type) {
	case *kind.Type:
		return fmt.Sprintf("类型应为 %s，实际为 %s", strings.Join(k.Want, " 或 "), k.Got)
	case *kind.Enum:
		want := make([]string, len(k.Want))
		for i, w := range k.Want {
			want[i] = show(w)
		}
		return fmt.Sprintf("取值 %s 不在允许范围内（%s）", show(k.Got), strings.Join(want, "、"))
	case *kind.Const:
		return fmt.Sprintf("取值应为 %s，实际为 %s", show(k.Want), show(k.Got))
	case *kind.Pattern:
		return fmt.Sprintf("取值 %s 格式不正确，应匹配 %s", show(k.Got), k.Want)
	case *kind.UniqueItems:
		return fmt.Sprintf("第 %d 项与第 %d 项重复", k.Duplicates[0]+1, k.Duplicates[1]+1)
	case *kind.MinItems:
		return fmt.Sprintf("至少需要 %d 项，实际为 %d 项", k.Want, k.Got)
	case *kind.MaxItems:
		return fmt.Sprintf("最多允许 %d 项，实际为 %d 项", k.Want, k.Got)
	case *kind.MinLength:
		return fmt.Sprintf("长度至少为 %d 个字符，实际为 %d 个", k.Want, k.Got)
	case *kind.MaxLength:
		return fmt.Sprintf("长度最多为 %d 个字符，实际为 %d 个", k.Want, k.Got)
	}
	return "不符合 Schema 约束 " + strings.Join(ek.KeywordPath(), "/")
}

func show(v any) string {
	b, err := json.Marshal(v)
	if err != nil {
		return fmt.Sprint(v)
	}
	return string(b)
}
