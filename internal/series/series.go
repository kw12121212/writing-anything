// Package series 管理系列（Series）.writing-anything/series/<id>.yaml：有序的成员作品，以及被成员作品继承的规范包、素材与事实；
// 并管理单元目录的已验收摘要（Summary）summary.yaml。本包只返回结构化的结果与问题，由命令层转换为诊断（Diagnostic）。
package series

import (
	"bytes"
	"errors"
	"fmt"
	"io/fs"
	"os"
	"path/filepath"
	"slices"
	"strings"

	"github.com/kw12121212/writing-anything/internal/catalog"
	"github.com/kw12121212/writing-anything/internal/resolve"
	"github.com/kw12121212/writing-anything/internal/schema"
	"github.com/kw12121212/writing-anything/internal/source"
	"github.com/kw12121212/writing-anything/internal/work"
	"github.com/kw12121212/writing-anything/internal/workspace"
	"go.yaml.in/yaml/v3"
)

// 系列的错误码（Error Code）。已发布的错误码不得改名或复用。
const (
	CodeNotFound      = "series.not-found"
	CodeUnknownSource = "series.unknown-source"
	CodeAlreadyMember = "series.already-member"
	CodeIDMismatch    = "series.id-mismatch"
	CodeAnchoredValue = "series.anchored-value"
)

// Dir 是系列所在的目录，相对项目目录。
const Dir = workspace.Dir + "/series"

// Fact 是系列的一条事实（Fact）及其素材出处。
type Fact struct {
	ID      string   `json:"id" yaml:"id"`
	Text    string   `json:"text" yaml:"text"`
	Sources []string `json:"sources" yaml:"sources,flow"`
}

// Series 是一个已通过 Schema 与文件内检查的系列。
type Series struct {
	ID      string   `json:"id"`
	Title   string   `json:"title"`
	Works   []string `json:"works"`
	Packs   []string `json:"packs"`
	Sources []string `json:"sources"`
	Facts   []Fact   `json:"facts"`
	Path    string   `json:"-"`
	doc     *yaml.Node
	data    []byte
}

// Path 返回系列 id（调用方保证 id 格式）的路径。
func Path(id string) string { return Dir + "/" + id + ".yaml" }

func errorAt(code, path, field, msg string) workspace.Problem {
	return workspace.Problem{Code: code, Severity: workspace.SeverityError, Path: path, Field: field, Message: msg}
}

func abs(root, rel string) string { return filepath.Join(root, filepath.FromSlash(rel)) }

// Load 读取并校验系列文件 rel（Schema、id 与文件名一致、事实 id 不重复）；不存在时返回 nil 与 nil；途经符号链接或不是普通文件
// 时报告 artifact.not-found。
func Load(root, rel string) (*Series, []workspace.Problem) {
	data, ok, err := workspace.ReadRegular(root, rel)
	switch {
	case err != nil:
		return nil, []workspace.Problem{errorAt(workspace.CodeReadFailed, rel, "", "读取失败："+err.Error())}
	case !ok:
		if _, err := os.Lstat(abs(root, rel)); err == nil {
			return nil, []workspace.Problem{errorAt(schema.CodeNotFound, rel, "", "系列文件途经符号链接或不是普通文件，按不存在处理")}
		}
		return nil, nil
	}
	doc, p := schema.Parse(data)
	if p != nil {
		return nil, workspace.FromSchema(rel, []schema.Problem{*p})
	}
	s, ps := parse(rel, doc)
	if s == nil {
		return nil, ps
	}
	s.data = data
	return s, nil
}

func parse(rel string, doc *yaml.Node) (*Series, []workspace.Problem) {
	if ps := schema.Named("series").ValidateNode(doc); len(ps) > 0 {
		return nil, workspace.FromSchema(rel, ps)
	}
	var s Series
	catalog.Decode(doc, &s)
	s.Path, s.doc = rel, doc
	var out []workspace.Problem
	if name := strings.TrimSuffix(filepath.Base(rel), ".yaml"); s.ID != name {
		out = append(out, errorAt(CodeIDMismatch, rel, "id", fmt.Sprintf("id %s 与文件名 %s 不一致", s.ID, name)))
	}
	seen := map[string]bool{}
	for i, f := range s.Facts {
		if seen[f.ID] {
			out = append(out, errorAt(catalog.CodeDuplicateID, rel, fmt.Sprintf("facts[%d].id", i), "事实 "+f.ID+" 重复"))
		}
		seen[f.ID] = true
	}
	if out != nil {
		return nil, out
	}
	return &s, nil
}

// LoadAll 读取 series/ 下全部系列，按文件名排序；不能加载的系列以问题报告，不出现在结果中。返回已检查的路径。
func LoadAll(root string) (all []*Series, checked []string, problems []workspace.Problem) {
	for _, name := range workspace.YAMLFiles(root, Dir) {
		rel := Dir + "/" + name
		checked = append(checked, rel)
		s, ps := Load(root, rel)
		problems = append(problems, ps...)
		if s != nil {
			all = append(all, s)
		}
	}
	return all, checked, problems
}

// Of 返回作品 workID 所属的系列；不属于任何系列时为 nil。无法加载的系列文件不阻塞与它无关的作品：它们的问题降为 warning 返回
// （整体校验仍按 error 报告），作品按不属于这些系列处理（workspace-artifacts/subject-scoped-lookup）。
func Of(root, workID string) (*Series, []workspace.Problem) {
	all, _, ps := LoadAll(root)
	for i := range ps {
		ps[i].Severity = workspace.SeverityWarning
	}
	for _, s := range all {
		if slices.Contains(s.Works, workID) {
			return s, ps
		}
	}
	return nil, ps
}

// ForWork 按作品 w 合并生效的设置，并返回它所属的系列（不属于时为 nil）：属于系列时在 config.yaml 的 packs 之后应用系列的
// packs（resolve.ForWork）。系列文件只加载一次，调用方应在一条命令中复用结果（workspace-artifacts/load-once-per-command）。
func ForWork(root string, w *work.Work) (*resolve.Resolved, *Series, []workspace.Problem) {
	s, warnings := Of(root, w.ID)
	var layer *resolve.Layer
	if s != nil {
		layer = &resolve.Layer{Path: s.Path, Packs: s.Packs}
	}
	r, ps := resolve.ForWork(root, w.Genre.ID, layer)
	return r, s, append(warnings, ps...)
}

// Resolve 与 ForWork 相同，只返回合并结果。
func Resolve(root string, w *work.Work) (*resolve.Resolved, []workspace.Problem) {
	r, _, ps := ForWork(root, w)
	return r, ps
}

// Check 做整体校验中与系列有关的部分：每个系列文件合法，成员作品存在且只属于一个系列，规范包存在，素材已登记。
func Check(root string) (checked []string, problems []workspace.Problem) {
	all, checked, problems := LoadAll(root)
	owner := map[string]string{}
	for _, s := range all {
		for i, w := range s.Works {
			if _, _, ps := work.Resolve(root, w); len(ps) > 0 {
				problems = append(problems, errorAt(work.CodeNotFound, s.Path, fmt.Sprintf("works[%d]", i), "作品 "+w+" 不存在"))
			}
			if o, ok := owner[w]; ok {
				problems = append(problems, errorAt(CodeAlreadyMember, s.Path, fmt.Sprintf("works[%d]", i), fmt.Sprintf("作品 %s 已属于系列 %s；一个作品至多属于一个系列", w, o)))
			} else {
				owner[w] = s.ID
			}
		}
		problems = append(problems, refs(root, s)...)
		// 成员作品按系列层合并时的问题（如系列规范包与文体规则冲突）只有按作品合并才能检出，validate 也要报告
		// （workspace-artifacts/per-work-merge-validated）。
		for _, id := range s.Works {
			w, _, ps := work.Resolve(root, id)
			if len(ps) > 0 {
				continue
			}
			_, ps = resolve.ForWork(root, w.Genre.ID, &resolve.Layer{Path: s.Path, Packs: s.Packs})
			for _, p := range ps {
				// 系列中不存在的规范包已由 refs 报告。
				if p.Severity == workspace.SeverityError && p.Code != resolve.CodeUnknownPack && !slices.Contains(problems, p) {
					problems = append(problems, p)
				}
			}
		}
	}
	return checked, problems
}

// refs 检查系列引用的规范包与素材存在。
func refs(root string, s *Series) []workspace.Problem {
	var out []workspace.Problem
	for i, id := range s.Packs {
		if _, _, ok := catalog.FindPack(root, id); !ok {
			out = append(out, errorAt(resolve.CodeUnknownPack, s.Path, fmt.Sprintf("packs[%d]", i), "规范包 "+id+" 不存在：既不是内置规范包，也没有 "+catalog.PackPath(id)))
		}
	}
	for i, id := range s.Sources {
		if !source.Registered(root, id) {
			out = append(out, errorAt(CodeUnknownSource, s.Path, fmt.Sprintf("sources[%d]", i), "素材 "+id+" 未登记"))
		}
	}
	for i, f := range s.Facts {
		for j, id := range f.Sources {
			if !source.Registered(root, id) {
				out = append(out, errorAt(CodeUnknownSource, s.Path, fmt.Sprintf("facts[%d].sources[%d]", i, j), "素材 "+id+" 未登记"))
			}
		}
	}
	return out
}

// CheckArtifact 对已通过 Schema 校验的系列文件或摘要 rel 做文件内检查。
func CheckArtifact(root, rel string) []workspace.Problem {
	if strings.HasPrefix(rel, Dir+"/") {
		_, ps := Load(root, rel)
		return ps
	}
	_, ps := loadSummaryFile(root, rel)
	return ps
}

// order 是系列字段的顺序，新字段按它插入。
var order = []string{"schema_version", "id", "title", "works", "packs", "sources", "facts"}

// Edit 是一次 series set 的修改；nil 的字段不修改。
type Edit struct {
	Title          *string
	Packs, Sources *[]string
}

// Set 创建或修改系列 id。
func Set(root, id string, e Edit) (*Series, []string, []workspace.Problem) {
	s, ps := loadOrNew(root, id)
	if ps != nil {
		return nil, nil, ps
	}
	if e.Title != nil {
		s.Title = *e.Title
	}
	if e.Packs != nil {
		s.Packs = *e.Packs
		for i, p := range s.Packs {
			if _, _, ok := catalog.FindPack(root, p); !ok {
				return nil, nil, []workspace.Problem{errorAt(resolve.CodeUnknownPack, "", fmt.Sprintf("packs[%d]", i), "规范包 "+p+" 不存在：既不是内置规范包，也没有 "+catalog.PackPath(p))}
			}
		}
	}
	if e.Sources != nil {
		s.Sources = *e.Sources
		if p := unknownSource(root, s.Sources, "sources"); p != nil {
			return nil, nil, []workspace.Problem{*p}
		}
	}
	return s.save(root, func(m *yaml.Node) *workspace.Problem {
		for _, kv := range []struct {
			key string
			set bool
			v   any
		}{{"title", e.Title != nil, s.Title}, {"packs", e.Packs != nil, s.Packs}, {"sources", e.Sources != nil, s.Sources}} {
			if !kv.set {
				continue
			}
			n := encode(kv.v)
			if n.Kind == yaml.SequenceNode {
				n.Style = yaml.FlowStyle
			}
			if f, ok := workspace.SetKey(m, order, kv.key, n); !ok {
				return anchored(s.Path, f)
			}
		}
		return nil
	})
}

func unknownSource(root string, ids []string, field string) *workspace.Problem {
	for i, id := range ids {
		if !source.Registered(root, id) {
			p := errorAt(CodeUnknownSource, "", fmt.Sprintf("%s[%d]", field, i), "素材 "+id+" 未登记；请先用 wa-cli source add 登记")
			return &p
		}
	}
	return nil
}

// Add 把作品追加到系列 works 的末尾；已在其中时不写文件。
func Add(root, id, workID string) (*Series, []string, []workspace.Problem) {
	s, ps := loadExisting(root, id)
	if ps != nil {
		return nil, nil, ps
	}
	if _, _, ps := work.Resolve(root, workID); len(ps) > 0 {
		return nil, nil, ps
	}
	if slices.Contains(s.Works, workID) {
		return s, nil, nil
	}
	all, _, ps := LoadAll(root)
	if ps != nil {
		return nil, nil, ps
	}
	for _, o := range all {
		if o.ID != s.ID && slices.Contains(o.Works, workID) {
			return nil, nil, []workspace.Problem{errorAt(CodeAlreadyMember, "", "work", fmt.Sprintf("作品 %s 已属于系列 %s；一个作品至多属于一个系列", workID, o.ID))}
		}
	}
	s.Works = append(s.Works, workID)
	return s.save(root, func(m *yaml.Node) *workspace.Problem {
		works, ok := workspace.Lookup(m, "works")
		if !ok || works == nil {
			return anchored(s.Path, "works")
		}
		works.Content = append(works.Content, workspace.Str(workID))
		return nil
	})
}

// SetFact 新增或替换系列中与 f 同 id 的事实。
func SetFact(root, id string, f Fact) (*Series, []string, []workspace.Problem) {
	s, ps := loadExisting(root, id)
	if ps != nil {
		return nil, nil, ps
	}
	if f.Sources == nil {
		f.Sources = []string{}
	}
	if p := unknownSource(root, f.Sources, "sources"); p != nil {
		return nil, nil, []workspace.Problem{*p}
	}
	i := slices.IndexFunc(s.Facts, func(o Fact) bool { return o.ID == f.ID })
	if i >= 0 && s.Facts[i].Text == f.Text && slices.Equal(s.Facts[i].Sources, f.Sources) {
		return s, nil, nil // 取值未变：不写文件，事实中的注释原样保留
	}
	if i >= 0 {
		s.Facts[i] = f
	} else {
		s.Facts = append(s.Facts, f)
	}
	return s.save(root, func(m *yaml.Node) *workspace.Problem {
		facts, ok := workspace.Lookup(m, "facts")
		if !ok || facts == nil {
			return anchored(s.Path, "facts")
		}
		switch j := workspace.IndexByID(facts, f.ID); {
		case j < 0:
			facts.Content = append(facts.Content, encode(f))
		case !workspace.Plain(facts.Content[j]) || workspace.HasMerge(facts.Content[j]):
			return anchored(s.Path, fmt.Sprintf("facts[%d]", j))
		default:
			// 只改变化的字段，事实中其他字段与注释保留（workspace-artifacts/structured-edits）。
			sources := encode(f.Sources)
			sources.Style = yaml.FlowStyle
			for _, kv := range []struct {
				key string
				n   *yaml.Node
			}{{"text", workspace.Str(f.Text)}, {"sources", sources}} {
				if fld, ok := workspace.SetKey(facts.Content[j], []string{"id", "text", "sources"}, kv.key, kv.n); !ok {
					return anchored(s.Path, fmt.Sprintf("facts[%d].%s", j, fld))
				}
			}
		}
		workspace.BlockSequence(m, "facts")
		return nil
	})
}

func loadExisting(root, id string) (*Series, []workspace.Problem) {
	s, ps := Load(root, Path(id))
	if ps != nil {
		return nil, ps
	}
	if s == nil {
		return nil, []workspace.Problem{errorAt(CodeNotFound, "", "", "没有系列 "+id+"；请先用 wa-cli series set "+id+" 创建")}
	}
	return s, nil
}

func loadOrNew(root, id string) (*Series, []workspace.Problem) {
	s, ps := Load(root, Path(id))
	if ps != nil || s != nil {
		return s, ps
	}
	return &Series{ID: id, Title: id, Works: []string{}, Packs: []string{}, Sources: []string{}, Facts: []Fact{}, Path: Path(id)}, nil
}

func anchored(rel, field string) *workspace.Problem {
	p := errorAt(CodeAnchoredValue, rel, field, "该处经由 YAML 锚点、别名或合并键得到，wa-cli 无法只修改这一处；请先去掉锚点、别名与合并键")
	return &p
}

func encode(v any) *yaml.Node {
	var n yaml.Node
	if err := n.Encode(v); err != nil {
		panic(fmt.Sprintf("编码 %T 失败：%v", v, err)) // 本包自己的结构体一定能编码
	}
	return &n
}

// save 在系列文件的节点树上执行 edit（文件不存在时先生成完整的新文档），写出前重新解析并校验；内容未变时不写文件。
func (s *Series) save(root string, edit func(m *yaml.Node) *workspace.Problem) (*Series, []string, []workspace.Problem) {
	doc := s.doc
	if doc == nil || len(doc.Content) == 0 {
		m := &yaml.Node{Kind: yaml.MappingNode}
		for _, kv := range []struct {
			key string
			v   any
		}{{"schema_version", 1}, {"id", s.ID}, {"title", s.Title}, {"works", s.Works}, {"packs", s.Packs}, {"sources", s.Sources}, {"facts", s.Facts}} {
			n := encode(kv.v)
			if n.Kind == yaml.SequenceNode && kv.key != "facts" {
				n.Style = yaml.FlowStyle
			}
			m.Content = append(m.Content, workspace.Str(kv.key), n)
		}
		workspace.BlockSequence(m, "facts")
		doc = &yaml.Node{Kind: yaml.DocumentNode, HeadComment: "系列（Series）；请用 wa-cli series set、series add 与 series fact 修改。", Content: []*yaml.Node{m}}
	} else {
		m := doc.Content[0]
		if !workspace.Plain(m) || workspace.HasMerge(m) {
			return nil, nil, []workspace.Problem{*anchored(s.Path, "")}
		}
		if p := edit(m); p != nil {
			return nil, nil, []workspace.Problem{*p}
		}
	}
	data, err := workspace.Encode(doc)
	if err != nil {
		return nil, nil, []workspace.Problem{errorAt(workspace.CodeWriteFailed, s.Path, "", "生成系列文件失败："+err.Error())}
	}
	written, p := schema.Parse(data)
	if p != nil {
		return nil, nil, workspace.FromSchema(s.Path, []schema.Problem{*p})
	}
	saved, ps := parse(s.Path, written)
	if ps != nil {
		return nil, nil, ps
	}
	saved.data = data
	if bytes.Equal(data, s.data) {
		return saved, nil, nil
	}
	if info, err := os.Lstat(abs(root, Dir)); err == nil && !info.IsDir() || err != nil && !errors.Is(err, fs.ErrNotExist) {
		return nil, nil, []workspace.Problem{errorAt(workspace.CodeWriteFailed, Dir, "", "不是真实目录（可能是符号链接），wa-cli 不经由它写入")}
	}
	if err := os.MkdirAll(abs(root, Dir), 0o755); err != nil {
		return nil, nil, []workspace.Problem{errorAt(workspace.CodeWriteFailed, Dir, "", "创建目录失败："+err.Error())}
	}
	if err := workspace.WriteFile(root, s.Path, data); err != nil {
		return nil, nil, []workspace.Problem{errorAt(workspace.CodeWriteFailed, s.Path, "", "写入失败："+err.Error())}
	}
	return saved, []string{s.Path}, nil
}
