// Package workspace 管理项目目录下的工作区（Workspace）.writing-anything/：创建与补齐、读取与修改配置、
// 检查结构与汇总状态。本包只返回结构化的问题，由命令层转换为诊断（Diagnostic）。
package workspace

import (
	"bytes"
	"errors"
	"fmt"
	"io/fs"
	"os"
	"path/filepath"
	"reflect"
	"slices"
	"strconv"
	"strings"

	"github.com/kw12121212/writing-anything/internal/schema"
	"go.yaml.in/yaml/v3"
)

// 工作区与配置的错误码（Error Code）。已发布的错误码不得改名或复用。
const (
	CodeNotFound         = "workspace.not-found"
	CodeMissingDirectory = "workspace.missing-directory"
	CodeReadFailed       = "workspace.read-failed"
	CodeWriteFailed      = "workspace.write-failed"
	CodeConfigKept       = "workspace.config-kept"
	CodeUnknownConfigKey = "config.unknown-key"
	CodeAnchoredValue    = "config.anchored-value"
)

// 问题的严重程度。
const (
	SeverityError   = "error"
	SeverityWarning = "warning"
)

// 工作区内的路径，均相对项目目录、以 / 分隔。
const (
	Dir         = ".writing-anything"
	ConfigPath  = Dir + "/config.yaml"
	SourcesPath = Dir + "/sources"
	WorksPath   = Dir + "/works"
	PacksPath   = Dir + "/packs"
)

// 配置的默认值。
const (
	DefaultLanguage  = "zh-CN"
	DefaultGateLevel = "standard"
)

// GateLevels 是关卡强度（Gate Level）的全部取值，与 config.schema.json 一致。
var GateLevels = []string{"strict", "standard", "fast"}

// SettableKeys 是 config set 可以设置的配置项，按 config.yaml 中的顺序排列。
var SettableKeys = []string{"language", "default_genre", "packs", "gate_level", "context_budget", "ai_disclosure"}

// Problem 是工作区中的一个问题。Path 相对项目目录、以 / 分隔。
type Problem struct {
	Code     string
	Severity string
	Path     string
	Field    string
	Line     int    // YAML 行号；未知时为 0
	Origin   string // 内置产物的出处，如「内置规范包 default」；工作区产物为空
	Message  string
}

// Config 是工作区配置的摘要。
type Config struct {
	Language     string   `json:"language"`
	DefaultGenre *string  `json:"defaultGenre"`
	Packs        []string `json:"packs"`
	GateLevel    string   `json:"gateLevel"`
	// ContextBudget 是项目配置层的上下文预算（Context Budget）；未设置时为 nil。
	ContextBudget *int `json:"contextBudget"`
	// AIDisclosure 是 AI 参与说明的放置位置（footer、metadata 或 both）；未设置时为 nil，即 both。
	AIDisclosure *string `json:"aiDisclosure"`
}

func errorAt(code, path, msg string) Problem {
	return Problem{Code: code, Severity: SeverityError, Path: path, Message: msg}
}

// FromSchema 把 path 处产物的 Schema 校验问题转换为工作区问题。
func FromSchema(path string, ps []schema.Problem) []Problem { return fromSchema(path, ps) }

func fromSchema(path string, ps []schema.Problem) []Problem {
	var out []Problem
	for _, p := range ps {
		out = append(out, Problem{Code: p.Code, Severity: SeverityError, Path: path, Field: p.Field, Line: p.Line, Message: p.Message})
	}
	return out
}

func configKind() *schema.Kind { return schema.KindFor(ConfigPath) }

func abs(root, rel string) string { return filepath.Join(root, filepath.FromSlash(rel)) }

// Exists 报告 root 下是否存在工作区目录。
func Exists(root string) bool {
	info, err := os.Stat(abs(root, Dir))
	return err == nil && info.IsDir()
}

// Require 在 root 下没有工作区时返回 workspace.not-found 问题。
func Require(root string) []Problem {
	if Exists(root) {
		return nil
	}
	return []Problem{errorAt(CodeNotFound, Dir, fmt.Sprintf("项目目录 %s 下没有工作区 %s/", root, Dir))}
}

// Init 在 root 下创建工作区，只补齐缺失的目录与文件，不修改已有文件。set 为参数给出的配置项（键为
// config.yaml 字段名）。返回新建的路径；生成的配置不合法时不创建任何东西。
func Init(root string, set map[string]string) (created []string, problems []Problem) {
	doc := newConfig(set)
	data, err := encode(doc)
	if err != nil {
		return nil, []Problem{errorAt(CodeWriteFailed, ConfigPath, "生成配置失败："+err.Error())}
	}
	if ps := configKind().Validate(data); len(ps) > 0 {
		problems = fromSchema(ConfigPath, ps)
		for i := range problems {
			problems[i].Line = 0 // 文件尚未写出，行号没有意义
		}
		return nil, problems
	}
	ensureDir := func(rel string) bool {
		info, err := os.Stat(abs(root, rel))
		switch {
		case err == nil && info.IsDir():
			return true
		case err == nil:
			problems = append(problems, errorAt(CodeWriteFailed, rel, "已存在同名文件，无法创建目录"))
			return false
		case !errors.Is(err, fs.ErrNotExist):
			problems = append(problems, errorAt(CodeReadFailed, rel, "读取失败："+err.Error()))
			return false
		}
		if err := os.MkdirAll(abs(root, rel), 0o755); err != nil {
			problems = append(problems, errorAt(CodeWriteFailed, rel, "创建目录失败："+err.Error()))
			return false
		}
		created = append(created, rel)
		return true
	}
	if !ensureDir(Dir) {
		return created, problems
	}
	switch _, err := os.Stat(abs(root, ConfigPath)); {
	case err == nil:
		problems = append(problems, kept(root, set)...)
	case errors.Is(err, fs.ErrNotExist):
		if err := writeFile(abs(root, ConfigPath), data); err != nil {
			problems = append(problems, errorAt(CodeWriteFailed, ConfigPath, "写入配置失败："+err.Error()))
		} else {
			created = append(created, ConfigPath)
		}
	default:
		problems = append(problems, errorAt(CodeReadFailed, ConfigPath, "读取失败："+err.Error()))
	}
	ensureDir(SourcesPath)
	ensureDir(WorksPath)
	return created, problems
}

// kept 为已有配置中与 set 取值不同的字段生成 warning；已有配置无法解析时不比较，由 validate 报告。
func kept(root string, set map[string]string) []Problem {
	data, err := os.ReadFile(abs(root, ConfigPath))
	if err != nil {
		return nil
	}
	doc, p := schema.Parse(data)
	if p != nil {
		return nil
	}
	m, _ := schema.Decode(doc).(map[string]any)
	var out []Problem
	for _, key := range SettableKeys {
		want, ok := set[key]
		if !ok {
			continue
		}
		cur, has := m[key]
		if !has && want == "" || has && cur == want {
			continue
		}
		old := "未设置"
		if has {
			old = fmt.Sprint(cur)
		}
		out = append(out, Problem{Code: CodeConfigKept, Severity: SeverityWarning, Path: ConfigPath, Field: key,
			Message: fmt.Sprintf("config.yaml 已存在，保留原值 %s，未采用参数给出的 %q", old, want)})
	}
	return out
}

// newConfig 生成新工作区的配置文档。
func newConfig(set map[string]string) *yaml.Node {
	m := &yaml.Node{Kind: yaml.MappingNode}
	add := func(key string, v *yaml.Node) {
		m.Content = append(m.Content, str(key), v)
	}
	add("schema_version", &yaml.Node{Kind: yaml.ScalarNode, Tag: "!!int", Value: "1"})
	add("language", str(value(set, "language", DefaultLanguage)))
	if g := set["default_genre"]; g != "" {
		add("default_genre", str(g))
	}
	add("packs", packs(""))
	add("gate_level", str(value(set, "gate_level", DefaultGateLevel)))
	return &yaml.Node{Kind: yaml.DocumentNode, HeadComment: "writing-anything 工作区配置；请用 wa-cli config set <key> <value> 修改。", Content: []*yaml.Node{m}}
}

func value(set map[string]string, key, def string) string {
	if v, ok := set[key]; ok {
		return v
	}
	return def
}

func str(v string) *yaml.Node { return &yaml.Node{Kind: yaml.ScalarNode, Tag: "!!str", Value: v} }

// packs 把逗号分隔的规范包 id 转换为流式序列；空字符串表示空数组。
func packs(v string) *yaml.Node {
	seq := &yaml.Node{Kind: yaml.SequenceNode, Tag: "!!seq", Style: yaml.FlowStyle}
	for _, p := range strings.Split(v, ",") {
		if p = strings.TrimSpace(p); p != "" {
			seq.Content = append(seq.Content, str(p))
		}
	}
	return seq
}

// Encode 把 YAML 文档编码为缩进两格的文本，保留注释。
func Encode(doc *yaml.Node) ([]byte, error) { return encode(doc) }

func encode(doc *yaml.Node) ([]byte, error) {
	var b bytes.Buffer
	enc := yaml.NewEncoder(&b)
	enc.SetIndent(2)
	if err := enc.Encode(doc); err != nil {
		return nil, err
	}
	if err := enc.Close(); err != nil {
		return nil, err
	}
	return b.Bytes(), nil
}

// WriteFile 原子写入 root 下的产物 rel，所有产物写入都经过它（workspace-artifacts/atomic-writes）。
func WriteFile(root, rel string, data []byte) error { return writeFile(abs(root, rel), data) }

// writeFile 先写同目录的临时文件再重命名，避免留下写了一半的文件；覆盖已有文件时保留其权限。
func writeFile(path string, data []byte) error {
	mode := fs.FileMode(0o644)
	if info, err := os.Stat(path); err == nil {
		mode = info.Mode().Perm()
	}
	f, err := os.CreateTemp(filepath.Dir(path), ".wa-*.tmp")
	if err != nil {
		return err
	}
	tmp := f.Name()
	_, err = f.Write(data)
	if cerr := f.Close(); err == nil {
		err = cerr
	}
	if err == nil {
		err = os.Chmod(tmp, mode)
	}
	if err == nil {
		err = os.Rename(tmp, path)
	}
	if err != nil {
		os.Remove(tmp)
	}
	return err
}

// Load 读取并校验 root 下的工作区配置。配置能解析为映射时返回其摘要，即使存在 Schema 违规。
func Load(root string) (*Config, []Problem) {
	doc, ps := read(root, ConfigPath)
	if doc == nil {
		return nil, ps
	}
	return summarize(schema.Decode(doc)), fromSchema(ConfigPath, configKind().ValidateNode(doc))
}

// UnitRef 返回单元目录 dir（works/<work> 或 works/<work>/units/<unit-id>，相对项目目录）对应的单元引用
// （<work> 或 <work>/<unit-id>）。
func UnitRef(dir string) string {
	return strings.Replace(strings.TrimPrefix(dir, WorksPath+"/"), "/units/", "/", 1)
}

// ReadRegular 读取工作区中的普通文件 rel；工作区目录之下途经的每一级都必须是真实目录、文件本身必须是普通文件，
// 否则（包括不存在、途经符号链接）返回 nil 与 false，避免跟随符号链接读取项目目录之外的文件
// （workspace-artifacts/validated-path-segments）。
func ReadRegular(root, rel string) ([]byte, bool, error) {
	if !regular(root, rel) {
		return nil, false, nil
	}
	data, err := os.ReadFile(abs(root, rel))
	return data, err == nil, err
}

// OpenRegular 与 ReadRegular 做同样的检查，但打开文件供调用方流式读取；不是普通文件时返回 nil 与 false。
func OpenRegular(root, rel string) (*os.File, bool, error) {
	if !regular(root, rel) {
		return nil, false, nil
	}
	f, err := os.Open(abs(root, rel))
	return f, err == nil, err
}

// regular 报告 rel 是否为工作区中途经真实目录的普通文件。
func regular(root, rel string) bool {
	segs := strings.Split(rel, "/")
	for i := 2; i <= len(segs); i++ {
		info, err := os.Lstat(abs(root, strings.Join(segs[:i], "/")))
		if err != nil || i < len(segs) && !info.IsDir() || i == len(segs) && !info.Mode().IsRegular() {
			return false
		}
	}
	return true
}

// Read 读取并解析 root 下的产物 rel；失败时返回 nil 与问题。
func Read(root, rel string) (*yaml.Node, []Problem) { return read(root, rel) }

// read 读取并解析 rel；失败时返回 nil 与问题。
func read(root, rel string) (*yaml.Node, []Problem) {
	data, err := os.ReadFile(abs(root, rel))
	switch {
	case errors.Is(err, fs.ErrNotExist):
		return nil, []Problem{errorAt(schema.CodeNotFound, rel, "产物不存在")}
	case err != nil:
		return nil, []Problem{errorAt(CodeReadFailed, rel, "读取失败："+err.Error())}
	}
	doc, p := schema.Parse(data)
	if p != nil {
		return nil, fromSchema(rel, []schema.Problem{*p})
	}
	return doc, nil
}

func summarize(v any) *Config {
	m, ok := v.(map[string]any)
	if !ok {
		return nil
	}
	c := &Config{Packs: []string{}}
	c.Language, _ = m["language"].(string)
	c.GateLevel, _ = m["gate_level"].(string)
	if g, ok := m["default_genre"].(string); ok {
		c.DefaultGenre = &g
	}
	if a, ok := m["ai_disclosure"].(string); ok {
		c.AIDisclosure = &a
	}
	switch n := m["context_budget"].(type) {
	case int:
		c.ContextBudget = &n
	case int64:
		v := int(n)
		c.ContextBudget = &v
	case float64:
		if v := int(n); float64(v) == n {
			c.ContextBudget = &v
		}
	}
	ps, _ := m["packs"].([]any)
	for _, p := range ps {
		if s, ok := p.(string); ok {
			c.Packs = append(c.Packs, s)
		}
	}
	return c
}

// Check 校验 root 下的整个工作区：配置符合 Schema，且必需目录存在。返回已校验的产物路径。
func Check(root string) (checked []string, problems []Problem) {
	_, problems = Load(root)
	if len(problems) == 0 || problems[0].Code != schema.CodeNotFound {
		checked = append(checked, ConfigPath)
	}
	for _, rel := range []string{SourcesPath, WorksPath} {
		if info, err := os.Stat(abs(root, rel)); err != nil || !info.IsDir() {
			problems = append(problems, errorAt(CodeMissingDirectory, rel, "目录不存在"))
		}
	}
	return checked, problems
}

// ValidateArtifact 校验单个产物。p 相对 root 解析，也可以是绝对路径。
func ValidateArtifact(root, p string) (checked []string, problems []Problem) {
	rootAbs, err := filepath.Abs(root)
	if err != nil {
		return nil, []Problem{errorAt(CodeReadFailed, filepath.ToSlash(p), err.Error())}
	}
	target := p
	if !filepath.IsAbs(target) {
		target = filepath.Join(rootAbs, target)
	}
	rel, err := filepath.Rel(rootAbs, target)
	if err != nil {
		// 无法表示为相对路径（如 Windows 上不同的盘符）：不填 path，把原始输入写进说明。
		return nil, []Problem{errorAt(schema.CodeUnknownKind, "", p+" 不在项目目录中，不是工作区产物")}
	}
	rel = filepath.ToSlash(rel)
	if rel == ".." || strings.HasPrefix(rel, "../") {
		return nil, []Problem{errorAt(schema.CodeUnknownKind, rel, "不在项目目录中，不是工作区产物")}
	}
	k := schema.KindFor(rel)
	if k == nil {
		return nil, []Problem{errorAt(schema.CodeUnknownKind, rel, "不对应任何已知的产物类型")}
	}
	// 工作区目录之下途经符号链接（或途经的不是目录）时视为不存在，不读取项目目录之外的文件。
	segs := strings.Split(rel, "/")
	for i := 2; i <= len(segs); i++ {
		info, err := os.Lstat(abs(root, strings.Join(segs[:i], "/")))
		if err != nil {
			break
		}
		if i < len(segs) && !info.IsDir() || info.Mode()&fs.ModeSymlink != 0 {
			return nil, []Problem{errorAt(schema.CodeNotFound, rel, "产物途经符号链接，按不存在处理")}
		}
	}
	doc, ps := read(root, rel)
	if doc == nil {
		return nil, ps
	}
	return []string{rel}, fromSchema(rel, k.ValidateNode(doc))
}

// Summary 统计 sources/ 与 works/ 下的直接条目数，不含以 . 开头的隐藏条目；目录不存在时计为 0。
func Summary(root string) (sources, works int) {
	count := func(rel string) int {
		entries, _ := os.ReadDir(abs(root, rel))
		n := 0
		for _, e := range entries {
			if !strings.HasPrefix(e.Name(), ".") {
				n++
			}
		}
		return n
	}
	return count(SourcesPath), count(WorksPath)
}

// SetConfig 把配置项 key 设为 v，保留其他字段与注释。packs 的 v 按逗号分隔；default_genre 的 v 为空时删除该字段。
// 被设置字段的新值不合法时不写文件；其他字段已有的问题不阻止写入，随结果一并返回。
// 返回写入后（未写入时为当前）的配置摘要与是否改动了文件。
func SetConfig(root, key, v string) (cfg *Config, changed bool, problems []Problem) {
	if !slices.Contains(SettableKeys, key) {
		return nil, false, []Problem{errorAt(CodeUnknownConfigKey, ConfigPath,
			fmt.Sprintf("不能设置配置项 %s；可设置的配置项为 %s", key, strings.Join(SettableKeys, "、")))}
	}
	doc, ps := read(root, ConfigPath)
	if doc == nil {
		return nil, false, ps
	}
	if len(doc.Content) == 0 {
		// 空文件：从空映射开始写入。
		doc.Kind, doc.Content = yaml.DocumentNode, []*yaml.Node{{Kind: yaml.MappingNode}}
	}
	m := doc.Content[0]
	cfg = summarize(schema.Decode(doc))
	if m.Kind != yaml.MappingNode {
		return cfg, false, fromSchema(ConfigPath, configKind().ValidateNode(doc))
	}
	next := configValue(key, v)
	i := -1
	var cur *yaml.Node
	for j := 0; j+1 < len(m.Content); j += 2 {
		if m.Content[j].Value == key && !isMergeKey(m.Content[j]) {
			i, cur = j, m.Content[j+1]
		}
	}
	if equal(cur, next) {
		return cfg, false, fromSchema(ConfigPath, configKind().ValidateNode(doc))
	}
	if cur != nil && cur.Anchor != "" {
		// 替换或删除带锚点的值会让引用它的别名失效。
		return cfg, false, []Problem{{Code: CodeAnchoredValue, Severity: SeverityError, Path: ConfigPath, Field: key, Line: cur.Line,
			Message: fmt.Sprintf("取值带有 YAML 锚点 &%s，其他字段可能引用它；请先去掉锚点及其引用再修改", cur.Anchor)}}
	}
	switch {
	case next == nil:
		m.Content = slices.Delete(m.Content, i, i+2)
	case cur != nil:
		next.HeadComment, next.LineComment, next.FootComment = cur.HeadComment, cur.LineComment, cur.FootComment
		m.Content[i+1] = next
	default:
		m.Content = append(m.Content, str(key), next)
	}
	data, err := encode(doc)
	if err != nil {
		return cfg, false, []Problem{errorAt(CodeWriteFailed, ConfigPath, "生成配置失败："+err.Error())}
	}
	written, p := schema.Parse(data)
	if p != nil {
		// 修改后的文档无法解析时绝不写出，保持原文件可用。
		p.Line = 0
		return cfg, false, fromSchema(ConfigPath, []schema.Problem{*p})
	}
	var own, rest []schema.Problem
	for _, p := range configKind().ValidateNode(written) {
		if p.Field == key || strings.HasPrefix(p.Field, key+"[") || strings.HasPrefix(p.Field, key+".") {
			p.Line = 0 // 未写出的文件，行号没有意义
			own = append(own, p)
		} else {
			rest = append(rest, p)
		}
	}
	if len(own) > 0 {
		return cfg, false, fromSchema(ConfigPath, own)
	}
	if err := writeFile(abs(root, ConfigPath), data); err != nil {
		return cfg, false, []Problem{errorAt(CodeWriteFailed, ConfigPath, "写入配置失败："+err.Error())}
	}
	return summarize(schema.Decode(written)), true, fromSchema(ConfigPath, rest)
}

// configValue 返回配置项 key 取值 v 时的 YAML 节点；nil 表示删除该字段。
func configValue(key, v string) *yaml.Node {
	switch {
	case key == "packs":
		return packs(v)
	case (key == "default_genre" || key == "context_budget" || key == "ai_disclosure") && v == "":
		return nil
	case key == "context_budget":
		// 十进制整数写为整数，交给 Schema 检查是否为正整数；其他取值写为字符串，由 Schema 报告类型错误。
		if n, err := strconv.Atoi(v); err == nil {
			return Int(n)
		}
	}
	return str(v)
}

func isMergeKey(k *yaml.Node) bool { return k.ShortTag() == "!!merge" }

func equal(cur, next *yaml.Node) bool {
	if cur == nil || next == nil {
		return cur == next
	}
	return reflect.DeepEqual(schema.Decode(cur), schema.Decode(next))
}

// BlockSequence 把映射 m 中键 key 的序列值改为块式，便于追加元素后阅读；流式序列上的行尾注释移到键上，
// 避免编码时丢失（workspace-artifacts/structured-edits）。
func BlockSequence(m *yaml.Node, key string) {
	for i := 0; i+1 < len(m.Content); i += 2 {
		k, v := m.Content[i], m.Content[i+1]
		if k.Value != key || v.Kind != yaml.SequenceNode || v.Style&yaml.FlowStyle == 0 {
			continue
		}
		if v.LineComment != "" && k.LineComment == "" {
			k.LineComment, v.LineComment = v.LineComment, ""
		}
		v.Style &^= yaml.FlowStyle
	}
}
