package draft

import (
	"bytes"
	"os"
	"path/filepath"
	"slices"
	"strings"
	"time"
	"unicode/utf8"

	"github.com/kw12121212/writing-anything/internal/catalog"
	"github.com/kw12121212/writing-anything/internal/schema"
	"github.com/kw12121212/writing-anything/internal/work"
	"github.com/kw12121212/writing-anything/internal/workspace"
	"go.yaml.in/yaml/v3"
)

// EditsName 是单元目录中修改记录（Edit Record）的文件名；MaxEdits 是每个单元保留的修改对上限，超过时丢弃最早的。
const (
	EditsName = "edits.yaml"
	MaxEdits  = 200
)

// CodeEditsAnchored 是修改记录的 edits 经由锚点、别名或合并键得到、无法追加时的错误码。已发布的错误码不得改名或复用。
const CodeEditsAnchored = "draft.edits-anchored"

// now 是记录修改对的时间，测试可以替换。
var now = time.Now

// Edit 是作者对一个 Agent 段落的修改：Before 为 Agent 写入的原文，After 为作者修改后的文字。
type Edit struct {
	Before     string `json:"before" yaml:"before"`
	After      string `json:"after" yaml:"after"`
	RecordedAt string `json:"recorded_at" yaml:"recorded_at"`
}

// StripMarkup 去掉段落中 wa-cli 自己的标记（作者批注），由 internal/feedback 在初始化时设置；比较修改前后时，只差批注的段落
// 不算作者修改。为 nil 时不去掉任何内容。
var StripMarkup func(text string) string

func strip(text string) string {
	if StripMarkup == nil {
		return text
	}
	return StripMarkup(text)
}

// pairs 返回当前段落 cur 与写入记录 rec 之间的修改对：按与 diff 相同的对齐，记录中 origin 为 agent 且有原文、与当前段落配对上、
// 去掉批注后文字仍不同的段落；修改后的文字记录去掉批注后的结果。不是 UTF-8 的段落不记录。
func pairs(cur []Paragraph, rec *record) []Edit {
	if rec == nil {
		return nil
	}
	var out []Edit
	_, gaps := align(hashes(cur), recordHashes(rec.Paragraphs))
	for _, g := range gaps {
		for k, i := range g.cur {
			if k >= len(g.rec) {
				break
			}
			r := rec.Paragraphs[g.rec[k]]
			after := strings.TrimSpace(strip(cur[i].Text))
			if r.Origin == OriginAgent && r.Text != "" && after != "" && after != strings.TrimSpace(strip(r.Text)) && utf8.ValidString(r.Text) && utf8.ValidString(after) {
				out = append(out, Edit{Before: r.Text, After: after})
			}
		}
	}
	return out
}

// Pairs 返回单元 u 当前尚未记入修改记录的修改对（写入记录与正文之间）；没有正文或写入记录时为空。
func Pairs(root string, w *work.Work, u *work.Unit) ([]Edit, []workspace.Problem) {
	dir := w.UnitDir(u)
	_, cur, ok, ps := current(root, dir)
	if !ok || len(ps) > 0 {
		return nil, ps
	}
	rec, ps := loadRecord(root, dir)
	if len(ps) > 0 {
		return nil, ps
	}
	return pairs(cur, rec), nil
}

// LoadEdits 读取并校验单元 u 的修改记录；不存在时为空。途经符号链接或不是普通文件时报告 artifact.not-found。
func LoadEdits(root string, w *work.Work, u *work.Unit) ([]Edit, []workspace.Problem) {
	_, es, _, ps := loadEdits(root, w.UnitDir(u))
	return es, ps
}

func loadEdits(root, dir string) (*yaml.Node, []Edit, []byte, []workspace.Problem) {
	rel := dir + "/" + EditsName
	data, ok, err := readRegular(root, rel)
	switch {
	case err != nil:
		return nil, nil, nil, []workspace.Problem{errorAt(workspace.CodeReadFailed, rel, "", "读取失败："+err.Error())}
	case !ok:
		if _, err := os.Lstat(filepath.Join(root, filepath.FromSlash(rel))); err == nil {
			return nil, nil, nil, []workspace.Problem{errorAt(schema.CodeNotFound, rel, "", "edits.yaml 途经符号链接或不是普通文件，按不存在处理")}
		}
		return nil, nil, nil, nil
	}
	doc, p := schema.Parse(data)
	if p != nil {
		return nil, nil, nil, workspace.FromSchema(rel, []schema.Problem{*p})
	}
	if ps := schema.Named("edits").ValidateNode(doc); len(ps) > 0 {
		return nil, nil, nil, workspace.FromSchema(rel, ps)
	}
	var f struct {
		Edits []Edit `json:"edits"`
	}
	catalog.Decode(doc, &f)
	return doc, f.Edits, data, nil
}

// appendEdits 返回把 add 中尚未记入的修改对追加到单元目录 dir 的 edits.yaml 后的内容（超过 MaxEdits 时丢弃最早的）；没有要追加的时返回 nil。
// 已有文件时在节点树上追加，保留其他内容与注释；写出前重新解析并校验。
func appendEdits(root, dir string, add []Edit) ([]byte, []workspace.Problem) {
	if len(add) == 0 {
		return nil, nil
	}
	rel := dir + "/" + EditsName
	doc, existing, old, ps := loadEdits(root, dir)
	if len(ps) > 0 {
		return nil, ps
	}
	var fresh []Edit
	for _, e := range add {
		dup := func(o Edit) bool { return o.Before == e.Before && o.After == e.After }
		if !slices.ContainsFunc(existing, dup) && !slices.ContainsFunc(fresh, dup) {
			e.RecordedAt = now().UTC().Format("2006-01-02T15:04:05Z")
			fresh = append(fresh, e)
		}
	}
	if len(fresh) == 0 {
		return nil, nil
	}
	if doc == nil || len(doc.Content) == 0 {
		doc = &yaml.Node{Kind: yaml.DocumentNode, HeadComment: "修改记录（Edit Record）：作者对 Agent 段落的修改，由 wa-cli draft write 追加，用于归纳作者偏好；请勿修改。",
			Content: []*yaml.Node{{Kind: yaml.MappingNode, Content: []*yaml.Node{str("schema_version"), {Kind: yaml.ScalarNode, Tag: "!!int", Value: "1"},
				str("edits"), {Kind: yaml.SequenceNode, Tag: "!!seq"}}}}}
	}
	m := doc.Content[0]
	seq, ok := workspace.Lookup(m, "edits")
	if !workspace.Plain(m) || workspace.HasMerge(m) || !ok || seq == nil {
		return nil, []workspace.Problem{errorAt(CodeEditsAnchored, rel, "edits", "edits 经由 YAML 锚点、别名或合并键得到，wa-cli 无法追加修改对；请先去掉锚点、别名与合并键")}
	}
	for _, e := range fresh {
		var n yaml.Node
		if err := n.Encode(e); err != nil {
			return nil, []workspace.Problem{errorAt(workspace.CodeWriteFailed, rel, "", "生成修改记录失败："+err.Error())}
		}
		seq.Content = append(seq.Content, &n)
	}
	if over := len(seq.Content) - MaxEdits; over > 0 {
		if slices.ContainsFunc(seq.Content[:over], workspace.HasAnchor) {
			return nil, []workspace.Problem{errorAt(CodeEditsAnchored, rel, "edits", "要丢弃的最早的修改对带有 YAML 锚点或别名；请先去掉它们")}
		}
		seq.Content = seq.Content[over:]
	}
	workspace.BlockSequence(m, "edits")
	data, err := workspace.Encode(doc)
	if err != nil {
		return nil, []workspace.Problem{errorAt(workspace.CodeWriteFailed, rel, "", "生成修改记录失败："+err.Error())}
	}
	if ps := schema.Named("edits").Validate(data); len(ps) > 0 {
		return nil, workspace.FromSchema(rel, ps)
	}
	if bytes.Equal(data, old) {
		return nil, nil
	}
	return data, nil
}

// CheckEdits 校验 works 中每个单元目录下存在的 edits.yaml，返回已校验的路径与问题。
func CheckEdits(root string, works []*work.Work) (checked []string, problems []workspace.Problem) {
	for _, w := range works {
		for _, u := range w.Units() {
			rel := w.UnitDir(u) + "/" + EditsName
			if _, err := os.Lstat(filepath.Join(root, filepath.FromSlash(rel))); err != nil {
				continue
			}
			checked = append(checked, rel)
			_, ps := LoadEdits(root, w, u)
			problems = append(problems, ps...)
		}
	}
	return checked, problems
}
