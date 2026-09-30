// Package source 管理工作区中的素材库（Source Library）：每个素材（Source）一个目录
// .writing-anything/sources/<id>/，内含逐字节存档的原文与元数据 source.yaml。本包登记、加载、列出素材并
// 校验存档未被改写，只返回结构化的问题，由命令层转换为诊断（Diagnostic）。
package source

import (
	"crypto/sha256"
	"encoding/hex"
	"errors"
	"fmt"
	"io/fs"
	"os"
	"path/filepath"
	"regexp"
	"strconv"
	"strings"

	"github.com/kw12121212/writing-anything/internal/catalog"
	"github.com/kw12121212/writing-anything/internal/schema"
	"github.com/kw12121212/writing-anything/internal/workspace"
	"go.yaml.in/yaml/v3"
)

// 素材的错误码（Error Code）。已发布的错误码不得改名或复用。
const (
	CodeExists     = "source.exists"
	CodeEmpty      = "source.empty"
	CodeIDMismatch = "source.id-mismatch"
	CodeModified   = "source.modified"
)

// FileName 是素材目录中元数据的文件名。
const FileName = "source.yaml"

// Types 是素材类型的全部取值，与 source.schema.json 一致。
var Types = []string{"author-note", "interview", "transcript", "reference", "past-article"}

// idPattern 与 common.schema.json 中的 id 一致；素材 id 在拼接路径前按它检查。
var idPattern = regexp.MustCompile(`^[a-z][a-z0-9]*(-[a-z0-9]+)*$`)

// extPattern 是存档文件名中允许保留的扩展名，与 source.schema.json 中 file 的格式一致。
var extPattern = regexp.MustCompile(`^\.[A-Za-z0-9]+$`)

// Dir 返回素材 id 的素材目录，相对项目目录。
func Dir(id string) string { return workspace.SourcesPath + "/" + id }

// Path 返回素材 id 的元数据路径，相对项目目录。
func Path(id string) string { return Dir(id) + "/" + FileName }

// Source 是一个已登记的素材。
type Source struct {
	ID           string `json:"id"`
	Type         string `json:"type"`
	Title        string `json:"title"`
	OriginalName string `json:"original_name"`
	File         string `json:"file"` // 存档文件名，位于素材目录中
	SHA256       string `json:"sha256"`
	Bytes        int    `json:"bytes"`
}

// FilePath 返回存档文件的路径，相对项目目录。
func (s *Source) FilePath() string { return Dir(s.ID) + "/" + s.File }

func errorAt(code, path, field, msg string) workspace.Problem {
	return workspace.Problem{Code: code, Severity: workspace.SeverityError, Path: path, Field: field, Message: msg}
}

func abs(root, rel string) string { return filepath.Join(root, filepath.FromSlash(rel)) }

// Hash 返回 data 的 SHA-256 十六进制。
func Hash(data []byte) string {
	sum := sha256.Sum256(data)
	return hex.EncodeToString(sum[:])
}

// Add 把 content 登记为类型为 typ 的素材。title 为空时不记录标题；id 为空时取 src- 加内容哈希的前 12 个字符；
// originalName 为输入文件的文件名，来自标准输入时为空。返回新素材与新建的路径；出错时不创建任何文件。
func Add(root string, content []byte, typ, title, id, originalName string) (*Source, []string, []workspace.Problem) {
	if len(content) == 0 {
		return nil, nil, []workspace.Problem{errorAt(CodeEmpty, "", "", "素材（Source）内容为空，没有可以存档的原文")}
	}
	sum := Hash(content)
	if id == "" {
		id = "src-" + sum[:12]
	}
	file := "original.txt"
	if originalName != "" {
		file = "original"
		if ext := filepath.Ext(originalName); ext != originalName && extPattern.MatchString(ext) {
			file += ext
		}
	}
	m := &yaml.Node{Kind: yaml.MappingNode}
	add := func(key string, v *yaml.Node) { m.Content = append(m.Content, str(key), v) }
	add("schema_version", integer(1))
	add("id", str(id))
	add("type", str(typ))
	if title != "" {
		add("title", str(title))
	}
	if originalName != "" {
		add("original_name", str(originalName))
	}
	add("file", str(file))
	add("sha256", str(sum))
	add("bytes", integer(len(content)))
	doc := &yaml.Node{Kind: yaml.DocumentNode, HeadComment: "素材（Source）元数据；原文逐字节存档在同目录的 file 中，请勿修改。", Content: []*yaml.Node{m}}
	data, err := workspace.Encode(doc)
	if err != nil {
		return nil, nil, []workspace.Problem{errorAt(workspace.CodeWriteFailed, "", "", "生成素材元数据失败："+err.Error())}
	}
	if ps := schema.Named("source").Validate(data); len(ps) > 0 {
		out := workspace.FromSchema("", ps)
		for i := range out {
			out[i].Line = 0 // 素材尚未创建：不填路径，行号也没有意义
		}
		return nil, nil, out
	}
	dir := Dir(id)
	if _, err := os.Lstat(abs(root, dir)); !errors.Is(err, fs.ErrNotExist) {
		return nil, nil, []workspace.Problem{errorAt(CodeExists, dir, "", "素材 "+id+" 已存在；同一内容只需登记一次")}
	}
	if err := os.Mkdir(abs(root, dir), 0o755); err != nil {
		return nil, nil, []workspace.Problem{errorAt(workspace.CodeWriteFailed, dir, "", "创建素材目录失败："+err.Error())}
	}
	s := &Source{ID: id, Type: typ, Title: title, OriginalName: originalName, File: file, SHA256: sum, Bytes: len(content)}
	for _, w := range []struct {
		rel  string
		data []byte
	}{{s.FilePath(), content}, {Path(id), data}} {
		if err := workspace.WriteFile(root, w.rel, w.data); err != nil {
			os.RemoveAll(abs(root, dir))
			return nil, nil, []workspace.Problem{errorAt(workspace.CodeWriteFailed, w.rel, "", "写入素材失败："+err.Error())}
		}
	}
	return s, []string{dir, s.FilePath(), Path(id)}, nil
}

func str(v string) *yaml.Node { return &yaml.Node{Kind: yaml.ScalarNode, Tag: "!!str", Value: v} }

func integer(v int) *yaml.Node {
	return &yaml.Node{Kind: yaml.ScalarNode, Tag: "!!int", Value: strconv.Itoa(v)}
}

// Load 读取并校验素材 id 的元数据：符合 Schema，且 id 与目录名一致。不检查存档文件。
func Load(root, id string) (*Source, []workspace.Problem) {
	rel := Path(id)
	if info, err := os.Lstat(abs(root, rel)); err == nil && !info.Mode().IsRegular() {
		// 不跟随符号链接读取项目目录之外的文件。
		return nil, []workspace.Problem{errorAt(schema.CodeNotFound, rel, "", "素材元数据不是普通文件")}
	}
	doc, ps := workspace.Read(root, rel)
	if doc == nil {
		return nil, ps
	}
	if ps := schema.Named("source").ValidateNode(doc); len(ps) > 0 {
		return nil, workspace.FromSchema(rel, ps)
	}
	var s Source
	catalog.Decode(doc, &s)
	if s.ID != id {
		return nil, []workspace.Problem{errorAt(CodeIDMismatch, rel, "id", fmt.Sprintf("素材 id %s 与素材目录名 %s 不一致", s.ID, id))}
	}
	return &s, nil
}

// Check 加载素材 id 并校验存档文件存在、是普通文件且内容哈希与记录一致。
func Check(root, id string) (*Source, []workspace.Problem) {
	s, ps := Load(root, id)
	if s == nil {
		return nil, ps
	}
	rel := s.FilePath()
	info, err := os.Lstat(abs(root, rel))
	if err != nil || !info.Mode().IsRegular() {
		return nil, []workspace.Problem{errorAt(schema.CodeNotFound, rel, "", "素材的存档文件不存在或不是普通文件")}
	}
	data, err := os.ReadFile(abs(root, rel))
	if err != nil {
		return nil, []workspace.Problem{errorAt(workspace.CodeReadFailed, rel, "", "读取失败："+err.Error())}
	}
	if Hash(data) != s.SHA256 {
		return nil, []workspace.Problem{errorAt(CodeModified, rel, "", "素材原文已被改动：内容哈希与登记时记录的 sha256 不一致；素材原文不得改写，需要新版本时请另行登记")}
	}
	return s, nil
}

// Registered 报告素材 id 是否已登记：id 合法、素材目录是真实目录且元数据可以加载。
func Registered(root, id string) bool {
	if !idPattern.MatchString(id) || !isDir(root, Dir(id)) {
		return false
	}
	s, _ := Load(root, id)
	return s != nil
}

// isDir 报告 rel 是否为真实目录；符号链接不算，避免跟随它读取项目目录之外的文件。
func isDir(root, rel string) bool {
	info, err := os.Lstat(abs(root, rel))
	return err == nil && info.IsDir()
}

// LoadAll 检查 sources/ 下每个不以 . 开头的子目录，按目录名排序。返回完整的素材、已校验的 source.yaml 路径与
// 全部问题；sources/ 下的普通文件不属于素材库，不检查；sources/ 不存在时什么也不返回，由工作区校验报告。
func LoadAll(root string) (sources []*Source, checked []string, problems []workspace.Problem) {
	entries, err := os.ReadDir(abs(root, workspace.SourcesPath))
	if err != nil {
		return nil, nil, nil
	}
	for _, e := range entries {
		if strings.HasPrefix(e.Name(), ".") || !isDir(root, Dir(e.Name())) {
			continue
		}
		s, ps := Check(root, e.Name())
		if len(ps) == 0 || ps[0].Code != schema.CodeNotFound || ps[0].Path != Path(e.Name()) {
			checked = append(checked, Path(e.Name()))
		}
		if s != nil {
			sources = append(sources, s)
		}
		problems = append(problems, ps...)
	}
	return sources, checked, problems
}

// CheckArtifact 对已通过 Schema 校验的 source.yaml（rel）做 id 与存档完整性检查。
func CheckArtifact(root, rel string) []workspace.Problem {
	_, ps := Check(root, strings.Split(rel, "/")[2])
	return ps
}
