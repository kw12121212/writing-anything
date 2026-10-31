// Package handoff 管理工作区（Workspace）的交接记录（Handoff Note）handoffs.yaml：Agent 会话结束前经
// wa-cli handoff write 追加完成了什么、未完成事项、作者的新指示与风险；条目可选关联作品（Work，work 字段），
// wa-cli next 附上所属作品可见的最近一条（关联该作品的与未关联的记录中较新者），未关联的记录对所有作品可见，
// 新会话不依赖对话历史即可接续。交接记录只能追加（没有编辑或删除命令），条目数上限 200。本包不 import
// internal/cli，作品存在性由命令层校验。
package handoff

import (
	"fmt"
	"os"
	"path/filepath"
	"strings"
	"time"

	"github.com/kw12121212/writing-anything/internal/catalog"
	"github.com/kw12121212/writing-anything/internal/schema"
	"github.com/kw12121212/writing-anything/internal/workspace"
	"go.yaml.in/yaml/v3"
)

// 交接记录的错误码（Error Code）。已发布的错误码不得改名或复用。
const (
	CodeTooMany = "handoff.too-many"
)

// FileName 是交接记录文件名；Path 返回其工作区相对路径。
const FileName = "handoffs.yaml"

// MaxNotes 是条目数上限（只能追加，防无界增长）。
const MaxNotes = 200

func Path() string { return workspace.Dir + "/" + FileName }

// Note 是一条交接记录。
type Note struct {
	ID           string   `json:"id"`
	WrittenAt    string   `json:"written_at"`
	Work         string   `json:"work"`
	Done         []string `json:"done"`
	Todo         []string `json:"todo"`
	Instructions []string `json:"instructions"`
	Risks        []string `json:"risks"`
}

// View 是输出形态（数组永不为 null）；Work 是关联作品（Work）的 id，未关联时为空、JSON 输出缺省。
type View struct {
	ID           string   `json:"id"`
	WrittenAt    string   `json:"writtenAt"`
	Work         string   `json:"work,omitempty"`
	Done         []string `json:"done"`
	Todo         []string `json:"todo"`
	Instructions []string `json:"instructions"`
	Risks        []string `json:"risks"`
}

type fileRecord struct {
	SchemaVersion int        `json:"schema_version"`
	Notes         []fileNote `json:"notes"`
}

type fileNote struct {
	ID           string   `json:"id"`
	WrittenAt    string   `json:"written_at"`
	Work         string   `json:"work"`
	Done         []string `json:"done"`
	Todo         []string `json:"todo"`
	Instructions []string `json:"instructions"`
	Risks        []string `json:"risks"`
}

func errorAt(code, path, field, msg string) workspace.Problem {
	return workspace.Problem{Code: code, Severity: workspace.SeverityError, Path: path, Field: field, Message: msg}
}

// load 读取并校验交接记录文件；不存在时返回 nil 与 nil；途经符号链接按 artifact.not-found。
func load(root string) (*yaml.Node, *fileRecord, []workspace.Problem) {
	rel := Path()
	if _, ok, _ := workspace.ReadRegular(root, rel); !ok {
		if info, err := os.Lstat(filepath.Join(root, filepath.FromSlash(rel))); err == nil && !info.Mode().IsRegular() {
			return nil, nil, []workspace.Problem{errorAt(schema.CodeNotFound, rel, "", "handoffs.yaml 途经符号链接或不是普通文件，按不存在处理")}
		}
		return nil, nil, nil
	}
	doc, ps := workspace.Read(root, rel)
	if doc == nil {
		return nil, nil, ps
	}
	if sps := schema.Named("handoffs").ValidateNode(doc); len(sps) > 0 {
		return nil, nil, workspace.FromSchema(rel, sps)
	}
	var f fileRecord
	catalog.Decode(doc, &f)
	return doc, &f, checkNotes(rel, f)
}

// checkNotes 领域校验：id 递增形态且不重复。
func checkNotes(rel string, f fileRecord) (problems []workspace.Problem) {
	seen := map[string]bool{}
	for i, n := range f.Notes {
		var seq int
		if n.ID != fmt.Sprintf("handoff-%d", i+1) || seen[n.ID] {
			problems = append(problems, errorAt(catalog.CodeDuplicateID, rel, fmt.Sprintf("notes[%d].id", i),
				"交接记录的 id 必须从 handoff-1 起按顺序递增且不重复（got "+n.ID+"）"))
		}
		_ = seq
		seen[n.ID] = true
	}
	return problems
}

// ParseInput 解析并校验交接记录输入。
func ParseInput(data []byte) (View, []workspace.Problem) {
	doc, p := schema.Parse(data)
	if p != nil {
		return View{}, workspace.FromSchema("", []schema.Problem{*p})
	}
	if ps := schema.Named("handoff-input").ValidateNode(doc); len(ps) > 0 {
		return View{}, workspace.FromSchema("", ps)
	}
	var in struct {
		Done         []string `json:"done"`
		Todo         []string `json:"todo"`
		Instructions []string `json:"instructions"`
		Risks        []string `json:"risks"`
		Work         string   `json:"work"`
	}
	catalog.Decode(doc, &in)
	v := View{Work: in.Work, Done: nonNil(in.Done), Todo: nonNil(in.Todo), Instructions: nonNil(in.Instructions), Risks: nonNil(in.Risks)}
	return v, nil
}

func nonNil(vs []string) []string {
	if vs == nil {
		return []string{}
	}
	return vs
}

func nowUTC(now time.Time) string { return now.UTC().Format("2006-01-02T15:04:05Z") }

// Write 追加一条交接记录：上限 200（超出报告 handoff.too-many 且文件未变），节点树追加保留注释；
// 返回新记录视图与变更路径。
func Write(root string, in View, now time.Time) (*View, []string, []workspace.Problem) {
	rel := Path()
	doc, f, ps := load(root)
	if ps != nil {
		return nil, nil, ps
	}
	if f != nil && len(f.Notes) >= MaxNotes {
		return nil, nil, []workspace.Problem{errorAt(CodeTooMany, rel, "",
			fmt.Sprintf("交接记录已有 %d 条（上限 %d）；请归档清理旧记录后再写入", len(f.Notes), MaxNotes))}
	}
	if doc == nil {
		f = &fileRecord{SchemaVersion: 1, Notes: []fileNote{}}
		doc = &yaml.Node{Kind: yaml.DocumentNode, HeadComment: "交接记录（Handoff Note）；由 wa-cli handoff write 追加，新会话经 wa-cli next 接续。", Content: []*yaml.Node{{Kind: yaml.MappingNode, Content: []*yaml.Node{
			workspace.Str("schema_version"), workspace.Int(1),
			workspace.Str("notes"), {Kind: yaml.SequenceNode, Tag: "!!seq"}}}}}
	}
	n := len(f.Notes) + 1
	id := fmt.Sprintf("handoff-%d", n)
	item := &yaml.Node{Kind: yaml.MappingNode, Content: []*yaml.Node{}}
	addKV := func(k string, v *yaml.Node) { item.Content = append(item.Content, workspace.Str(k), v) }
	addKV("id", workspace.Str(id))
	addKV("written_at", workspace.Str(nowUTC(now)))
	if in.Work != "" {
		addKV("work", workspace.Str(in.Work))
	}
	seq := func(vs []string) *yaml.Node {
		s := &yaml.Node{Kind: yaml.SequenceNode, Tag: "!!seq"}
		for _, x := range vs {
			s.Content = append(s.Content, workspace.Str(x))
		}
		return s
	}
	addKV("done", seq(in.Done))
	addKV("todo", seq(in.Todo))
	addKV("instructions", seq(in.Instructions))
	addKV("risks", seq(in.Risks))
	m := doc.Content[0]
	var notes *yaml.Node
	for i := 0; i+1 < len(m.Content); i += 2 {
		if m.Content[i].Value == "notes" {
			notes = m.Content[i+1]
		}
	}
	if notes == nil {
		m.Content = append(m.Content, workspace.Str("notes"), &yaml.Node{Kind: yaml.SequenceNode, Tag: "!!seq"})
		notes = m.Content[len(m.Content)-1]
	}
	if notes.Anchor != "" || notes.Kind == yaml.AliasNode || workspace.HasMerge(m) {
		return nil, nil, []workspace.Problem{errorAt("handoff.anchored-value", rel, "notes",
			"notes 经由 YAML 锚点、别名或合并键得到，wa-cli 无法只追加一条；请先去掉锚点、别名与合并键")}
	}
	workspace.BlockSequence(m, "notes")
	notes.Content = append(notes.Content, item)
	data, err := workspace.Encode(doc)
	if err != nil {
		return nil, nil, []workspace.Problem{errorAt(workspace.CodeWriteFailed, rel, "", "生成交接记录失败："+err.Error())}
	}
	written, p := schema.Parse(data)
	if p != nil {
		return nil, nil, workspace.FromSchema(rel, []schema.Problem{*p})
	}
	if sps := schema.Named("handoffs").ValidateNode(written); len(sps) > 0 {
		return nil, nil, workspace.FromSchema(rel, sps)
	}
	var f2 fileRecord
	catalog.Decode(written, &f2)
	if rps := checkNotes(rel, f2); len(rps) > 0 {
		return nil, nil, rps
	}
	if err := workspace.WriteFile(root, rel, data); err != nil {
		return nil, nil, []workspace.Problem{errorAt(workspace.CodeWriteFailed, rel, "", "写交接记录失败："+err.Error())}
	}
	out := in
	out.ID, out.WrittenAt = id, nowUTC(now)
	return &out, []string{rel}, nil
}

// Show 返回最近 n 条交接记录（倒序，最近在前）；没有记录时空数组。
func Show(root string, n int) ([]View, []workspace.Problem) {
	return show(root, nil, n)
}

// ShowFor 返回作品 workID 可见的最近 n 条交接记录（关联该作品的与未关联的，倒序）。
func ShowFor(root, workID string, n int) ([]View, []workspace.Problem) {
	return show(root, &workID, n)
}

// show 按可选的作品过滤输出记录；workID 为 nil 时不过滤（全部记录）。
func show(root string, workID *string, n int) ([]View, []workspace.Problem) {
	_, f, ps := load(root)
	if ps != nil {
		return nil, ps
	}
	out := []View{}
	if f == nil {
		return out, nil
	}
	for i := len(f.Notes) - 1; i >= 0 && len(out) < n; i-- {
		note := f.Notes[i]
		if workID != nil && note.Work != "" && note.Work != *workID {
			continue
		}
		out = append(out, View{ID: note.ID, WrittenAt: note.WrittenAt, Work: note.Work, Done: nonNil(note.Done), Todo: nonNil(note.Todo),
			Instructions: nonNil(note.Instructions), Risks: nonNil(note.Risks)})
	}
	return out, nil
}

// VisibleLatest 返回已加载记录（倒序，最近在前）中作品 workID 可见的最近一条：关联该作品的
// 记录与未关联的记录中较新的一条；没有可见记录时为 nil。供 next 每条命令只加载一次后逐作品过滤。
func VisibleLatest(notes []View, workID string) *View {
	for i := range notes {
		if notes[i].Work == "" || notes[i].Work == workID {
			return &notes[i]
		}
	}
	return nil
}

// CheckArtifact 对 handoffs.yaml 做 Schema 与领域校验（wa-cli validate <path>）。
func CheckArtifact(root, rel string) []workspace.Problem {
	if rel != Path() {
		return nil
	}
	_, f, ps := load(root)
	if f == nil {
		return ps
	}
	return checkNotes(rel, *f)
}

var _ = strings.TrimSpace
