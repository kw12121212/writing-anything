// Package learn 从作者的过往文章生成文风画像（Style Profile）提议：计算确定性的文风统计，维护工作区中的规范包提议（Pack Proposal）
// .writing-anything/proposals/<id>.yaml，并在作者逐项确认后把条目写入作者自己的规范包。未确认的提议不影响任何规则；本包只返回
// 结构化的结果与问题，由命令层转换为诊断（Diagnostic）。
package learn

import (
	"bytes"
	"errors"
	"fmt"
	"io/fs"
	"os"
	"path/filepath"
	"reflect"
	"slices"
	"strings"
	"time"

	"github.com/kw12121212/writing-anything/internal/catalog"
	"github.com/kw12121212/writing-anything/internal/packinstall"
	"github.com/kw12121212/writing-anything/internal/schema"
	"github.com/kw12121212/writing-anything/internal/source"
	"github.com/kw12121212/writing-anything/internal/workspace"
	"go.yaml.in/yaml/v3"
)

// 规范包提议的错误码（Error Code）。已发布的错误码不得改名或复用。
const (
	CodeNotFound           = "proposal.not-found"
	CodeMissingPack        = "proposal.missing-pack"
	CodeNoSources          = "proposal.no-sources"
	CodePackMismatch       = "proposal.pack-mismatch"
	CodeUnknownSource      = "proposal.unknown-source"
	CodeNotPastArticle     = "proposal.not-past-article"
	CodeSourceNotText      = "proposal.source-not-text"
	CodeTooLarge           = "proposal.too-large"
	CodeInstalledPack      = "proposal.installed-pack"
	CodeUnknownItem        = "proposal.unknown-item"
	CodeAlreadyConfirmed   = "proposal.already-confirmed"
	CodePreviouslyRejected = "proposal.previously-rejected"
	CodeIDMismatch         = "proposal.id-mismatch"
	CodeAnchoredValue      = "proposal.anchored-value"
	CodeInvalidBaseline    = "proposal.invalid-baseline"
)

// Dir 是提议所在的目录，相对项目目录。
const Dir = workspace.Dir + "/proposals"

// 条目的种类与状态。
const (
	KindBaseline   = "baseline"
	KindStyle      = "style"
	KindBannedTerm = "banned-term"

	StatusProposed  = "proposed"
	StatusConfirmed = "confirmed"
	StatusRejected  = "rejected"

	// BaselineID 是统计基准条目的 id。
	BaselineID = "baseline"
)

// Item 是提议中的一个条目。
type Item struct {
	ID           string     `json:"id" yaml:"id"`
	Kind         string     `json:"kind" yaml:"kind"`
	Text         string     `json:"text,omitempty" yaml:"text,omitempty"`
	Term         string     `json:"term,omitempty" yaml:"term,omitempty"`
	Status       string     `json:"status" yaml:"status"`
	Confirmation string     `json:"confirmation,omitempty" yaml:"confirmation,omitempty"`
	DecidedAt    string     `json:"decided_at,omitempty" yaml:"decided_at,omitempty"`
	Key          string     `json:"key,omitempty" yaml:"key,omitempty"`
	Evidence     []Evidence `json:"evidence,omitempty" yaml:"evidence,omitempty"`
}

// Evidence 是偏好条目的一条证据：单元引用与至多 80 个字符的片段。
type Evidence struct {
	Unit string `json:"unit" yaml:"unit"`
	Text string `json:"text" yaml:"text"`
}

// Proposal 是一份已通过校验的规范包提议。
type Proposal struct {
	ID      string   `json:"id"`
	Pack    string   `json:"pack"`
	Sources []string `json:"sources"`
	Stats   *Stats   `json:"stats"` // 没有素材（只由 pack prefer 创建）时为 nil
	Items   []Item   `json:"items"`
	Path    string   `json:"-"`
	doc     *yaml.Node
	data    []byte
}

// Path 返回提议 id（调用方保证 id 格式）的路径。
func Path(id string) string { return Dir + "/" + id + ".yaml" }

func errorAt(code, path, field, msg string) workspace.Problem {
	return workspace.Problem{Code: code, Severity: workspace.SeverityError, Path: path, Field: field, Message: msg}
}

func abs(root, rel string) string { return filepath.Join(root, filepath.FromSlash(rel)) }

// Load 读取并校验提议 rel；不存在时返回 nil 与 nil；途经符号链接或不是普通文件时报告 artifact.not-found。
func Load(root, rel string) (*Proposal, []workspace.Problem) {
	data, ok, err := workspace.ReadRegular(root, rel)
	switch {
	case err != nil:
		return nil, []workspace.Problem{errorAt(workspace.CodeReadFailed, rel, "", "读取失败："+err.Error())}
	case !ok:
		if _, err := os.Lstat(abs(root, rel)); err == nil {
			return nil, []workspace.Problem{errorAt(schema.CodeNotFound, rel, "", "提议途经符号链接或不是普通文件，按不存在处理")}
		}
		return nil, nil
	}
	doc, p := schema.Parse(data)
	if p != nil {
		return nil, workspace.FromSchema(rel, []schema.Problem{*p})
	}
	pr, ps := parse(rel, doc)
	if pr == nil {
		return nil, ps
	}
	pr.data = data
	return pr, nil
}

func parse(rel string, doc *yaml.Node) (*Proposal, []workspace.Problem) {
	if ps := schema.Named("pack-proposal").ValidateNode(doc); len(ps) > 0 {
		return nil, workspace.FromSchema(rel, ps)
	}
	var pr Proposal
	catalog.Decode(doc, &pr)
	pr.Path, pr.doc = rel, doc
	var out []workspace.Problem
	if name := strings.TrimSuffix(filepath.Base(rel), ".yaml"); pr.ID != name {
		out = append(out, errorAt(CodeIDMismatch, rel, "id", fmt.Sprintf("id %s 与文件名 %s 不一致", pr.ID, name)))
	}
	seen := map[string]bool{}
	for i, it := range pr.Items {
		if seen[it.ID] {
			out = append(out, errorAt(catalog.CodeDuplicateID, rel, fmt.Sprintf("items[%d].id", i), "条目 "+it.ID+" 重复"))
		}
		seen[it.ID] = true
		if (it.ID == BaselineID) != (it.Kind == KindBaseline) {
			out = append(out, errorAt(CodeInvalidBaseline, rel, fmt.Sprintf("items[%d]", i), "统计基准条目的 id 必须为 baseline、kind 必须为 baseline，两者不能只有其一"))
		}
	}
	if out != nil {
		return nil, out
	}
	if pr.Stats != nil && pr.Stats.Punctuation == nil {
		pr.Stats.Punctuation = map[string]float64{}
	}
	if pr.Stats != nil && pr.Stats.Terms == nil {
		pr.Stats.Terms = []Term{}
	}
	return &pr, nil
}

// CheckAll 校验 proposals/ 下每个不以 . 开头的 .yaml 文件；目录不存在不是问题。返回已校验的路径。
func CheckAll(root string) (checked []string, problems []workspace.Problem) {
	for _, name := range workspace.YAMLFiles(root, Dir) {
		rel := Dir + "/" + name
		checked = append(checked, rel)
		_, ps := Load(root, rel)
		problems = append(problems, ps...)
	}
	return checked, problems
}

// CheckArtifact 对已通过 Schema 校验的提议 rel 做领域检查。
func CheckArtifact(root, rel string) []workspace.Problem {
	_, ps := Load(root, rel)
	return ps
}

// Find 返回 id 的条目；没有时为 nil。
func (pr *Proposal) Find(id string) *Item {
	for i := range pr.Items {
		if pr.Items[i].ID == id {
			return &pr.Items[i]
		}
	}
	return nil
}

// Request 是一次 pack learn 的参数：Pack 与 Sources 可为空（追加时），Styles 与 Avoids 为新的定性条目。
type Request struct {
	ID, Pack       string
	Sources        []string
	Styles, Avoids []string
}

// Learn 按 req 生成或更新提议：校验目标规范包与全部素材，重新计算统计，合并条目。返回提议、变更的路径与 warning 级问题；
// 有 error 级问题时不写文件。
func Learn(root string, req Request) (*Proposal, []string, []workspace.Problem) {
	rel := Path(req.ID)
	pr, ps := Load(root, rel)
	if ps != nil {
		return nil, nil, ps
	}
	switch {
	case pr == nil && req.Pack == "":
		return nil, nil, []workspace.Problem{errorAt(CodeMissingPack, "", "pack", "提议 "+req.ID+" 还不存在；首次运行必须用 --pack 给出要写入的作者规范包 id")}
	case pr == nil && len(req.Sources) == 0:
		return nil, nil, []workspace.Problem{errorAt(CodeNoSources, "", "source", "提议 "+req.ID+" 还不存在；首次运行必须用 --source 给出至少一个 past-article 素材")}
	case pr != nil && req.Pack != "" && req.Pack != pr.Pack:
		return nil, nil, []workspace.Problem{errorAt(CodePackMismatch, "", "pack", fmt.Sprintf("提议 %s 的目标规范包是 %s，不是 %s；请换一个提议 id", req.ID, pr.Pack, req.Pack))}
	}
	if pr == nil {
		pr = &Proposal{ID: req.ID, Pack: req.Pack, Path: rel, Sources: []string{}, Items: []Item{}}
	}
	if p := checkTarget(root, pr.Pack); p != nil {
		return nil, nil, []workspace.Problem{*p}
	}
	sources := slices.Clone(pr.Sources)
	for _, id := range req.Sources {
		if !slices.Contains(sources, id) {
			sources = append(sources, id)
		}
	}
	texts, ps := readSources(root, sources)
	if ps != nil {
		return nil, nil, ps
	}
	next := *pr
	next.Sources, next.Items = sources, slices.Clone(pr.Items)
	if len(sources) > 0 {
		// 没有素材（只由 pack prefer 创建）的提议不生成统计与统计基准条目。
		stats := Compute(texts)
		next.Stats = &stats
		if !slices.ContainsFunc(next.Items, func(it Item) bool { return it.Kind == KindBaseline }) {
			next.Items = append([]Item{{ID: BaselineID, Kind: KindBaseline, Status: StatusProposed}}, next.Items...)
		}
	}
	if !reflect.DeepEqual(pr.Stats, next.Stats) {
		for i := range next.Items {
			if b := &next.Items[i]; b.Kind == KindBaseline && b.Status != StatusProposed {
				*b = Item{ID: b.ID, Kind: KindBaseline, Status: StatusProposed}
			}
		}
	}
	var warnings []workspace.Problem
	add := func(kind, prefix, value string) {
		value = strings.TrimSpace(value)
		for _, it := range next.Items {
			if it.Kind == kind && (it.Text == value || it.Term == value) {
				if it.Status == StatusRejected {
					warnings = append(warnings, workspace.Problem{Code: CodePreviouslyRejected, Severity: workspace.SeverityWarning, Path: rel, Field: "items",
						Message: fmt.Sprintf("「%s」已被作者拒绝（条目 %s），不再重复提出", value, it.ID)})
				}
				return
			}
		}
		it := Item{ID: nextID(next.Items, prefix), Kind: kind, Status: StatusProposed}
		if kind == KindStyle {
			it.Text = value
		} else {
			it.Term = value
		}
		next.Items = append(next.Items, it)
	}
	for _, s := range req.Styles {
		add(KindStyle, "style", s)
	}
	for _, s := range req.Avoids {
		add(KindBannedTerm, "avoid", s)
	}
	changed, ps := next.save(root)
	if ps != nil {
		return nil, nil, ps
	}
	return &next, changed, warnings
}

func nextID(items []Item, prefix string) string {
	for n := 1; ; n++ {
		id := fmt.Sprintf("%s-%d", prefix, n)
		if !slices.ContainsFunc(items, func(it Item) bool { return it.ID == id }) {
			return id
		}
	}
}

// checkTarget 确认目标规范包可以由作者确认写入：不是内置规范包，也不是由 pack install 安装（有锁定条目）的规范包。
func checkTarget(root, pack string) *workspace.Problem {
	if slices.Contains(catalog.BuiltinPacks(), pack) {
		p := errorAt(catalog.CodePackIDConflict, "", "pack", fmt.Sprintf("%s 是内置规范包，文风画像只能写入作者自己的规范包；请换一个规范包 id", pack))
		return &p
	}
	lock, ps := packinstall.ReadLock(root)
	if ps != nil {
		return &ps[0]
	}
	if lock.Find(pack) != nil {
		p := errorAt(CodeInstalledPack, catalog.PackPath(pack), "", fmt.Sprintf("规范包 %s 由 wa-cli pack install 安装并锁定，改写会破坏锁定；文风画像只能写入作者自己的规范包", pack))
		return &p
	}
	return nil
}

// readSources 读取并校验素材：已登记、原文未被改动、type 为 past-article、内容为 UTF-8 文本，合计不超过 MaxChars。
func readSources(root string, ids []string) ([][]byte, []workspace.Problem) {
	var texts [][]byte
	total := 0
	for i, id := range ids {
		if !source.Registered(root, id) {
			return nil, []workspace.Problem{errorAt(CodeUnknownSource, "", fmt.Sprintf("sources[%d]", i), "素材 "+id+" 未登记；请先用 wa-cli source add --type past-article 登记过往文章")}
		}
		s, data, ps := source.Read(root, id)
		if ps != nil {
			return nil, ps
		}
		switch {
		case s.Type != "past-article":
			return nil, []workspace.Problem{errorAt(CodeNotPastArticle, source.Path(id), "type", fmt.Sprintf("素材 %s 的 type 为 %s；文风画像只从作者的过往文章（past-article）学习", id, s.Type))}
		case !validText(data):
			return nil, []workspace.Problem{errorAt(CodeSourceNotText, s.FilePath(), "", "素材 "+id+" 的内容不是 UTF-8 文本，无法统计")}
		}
		if total += Chars(string(data)); total > MaxChars {
			return nil, []workspace.Problem{errorAt(CodeTooLarge, "", "", fmt.Sprintf("素材合计超过 %d 个非空白字符；请减少素材，或分成多份提议", MaxChars))}
		}
		texts = append(texts, data)
	}
	return texts, nil
}

// proposalOrder 是提议字段的顺序，新字段按它插入。
var proposalOrder = []string{"schema_version", "id", "pack", "sources", "stats", "items"}

// render 生成 pr 写出后的提议内容：在已有文件节点树的副本上替换 sources、stats 与改动的条目，追加新条目，保留其他内容与注释；
// 写出前重新解析并校验（workspace-artifacts/structured-edits）。不修改 pr，不写文件。
func (pr *Proposal) render() ([]byte, *yaml.Node, []workspace.Problem) {
	anchored := func(field string) []workspace.Problem {
		return []workspace.Problem{errorAt(CodeAnchoredValue, pr.Path, field, "该处经由 YAML 锚点、别名或合并键得到，wa-cli 无法只修改这一处；请先去掉锚点、别名与合并键")}
	}
	doc := cloneNode(pr.doc)
	if doc == nil || len(doc.Content) == 0 {
		m := &yaml.Node{Kind: yaml.MappingNode}
		m.Content = append(m.Content, workspace.Str("schema_version"), workspace.Int(1), workspace.Str("id"), workspace.Str(pr.ID), workspace.Str("pack"), workspace.Str(pr.Pack),
			workspace.Str("items"), &yaml.Node{Kind: yaml.SequenceNode, Tag: "!!seq"})
		doc = &yaml.Node{Kind: yaml.DocumentNode, HeadComment: "规范包提议（Pack Proposal）；条目经作者确认后才写入规范包。请用 wa-cli pack learn、pack confirm 与 pack reject 修改。",
			Content: []*yaml.Node{m}}
	}
	m := doc.Content[0]
	if !workspace.Plain(m) || workspace.HasMerge(m) {
		return nil, nil, anchored("")
	}
	for _, kv := range []struct {
		key string
		v   any
	}{{"sources", pr.Sources}, {"stats", pr.Stats}} {
		if kv.key == "stats" && pr.Stats == nil {
			if f, ok := workspace.SetKey(m, proposalOrder, "stats", nil); !ok {
				return nil, nil, anchored(f)
			}
			continue
		}
		n := encode(kv.v)
		if kv.key == "sources" {
			n.Style = yaml.FlowStyle
		}
		if old, _ := workspace.Lookup(m, kv.key); old != nil && same(old, n) {
			continue // 取值未变：保留原节点的风格与注释（workspace-artifacts/structured-edits）
		}
		// 先删除再写入：stats 的 punctuation 等映射要整体替换，不能保留已不存在的键。
		if f, ok := workspace.SetKey(m, proposalOrder, kv.key, nil); !ok {
			return nil, nil, anchored(f)
		}
		workspace.SetKey(m, proposalOrder, kv.key, n)
	}
	items, ok := workspace.Lookup(m, "items")
	if !ok || items == nil {
		return nil, nil, anchored("items")
	}
	for _, it := range pr.Items {
		n := encode(it)
		n.Style = yaml.FlowStyle
		i := workspace.IndexByID(items, it.ID)
		switch {
		case i < 0:
			items.Content = append(items.Content, n)
		case same(items.Content[i], n):
			// 取值未变的条目保留原节点，其中的注释与锚点都不受影响。
		case workspace.HasAnchor(items.Content[i]):
			return nil, nil, anchored(fmt.Sprintf("items[%d]", i))
		default:
			old := items.Content[i]
			n.HeadComment, n.LineComment, n.FootComment = old.HeadComment, old.LineComment, old.FootComment
			items.Content[i] = n
		}
	}
	workspace.BlockSequence(m, "items")
	data, err := workspace.Encode(doc)
	if err != nil {
		return nil, nil, []workspace.Problem{errorAt(workspace.CodeWriteFailed, pr.Path, "", "生成提议失败："+err.Error())}
	}
	written, p := schema.Parse(data)
	if p != nil {
		return nil, nil, workspace.FromSchema(pr.Path, []schema.Problem{*p})
	}
	if _, ps := parse(pr.Path, written); ps != nil {
		return nil, nil, ps
	}
	return data, written, nil
}

// save 写出 pr；内容未变时不写文件。
func (pr *Proposal) save(root string) ([]string, []workspace.Problem) {
	data, written, ps := pr.render()
	if ps != nil {
		return nil, ps
	}
	if bytes.Equal(data, pr.data) {
		return nil, nil
	}
	if p := realDir(root, Dir); p != nil {
		return nil, []workspace.Problem{*p}
	}
	if err := os.MkdirAll(abs(root, Dir), 0o755); err != nil {
		return nil, []workspace.Problem{errorAt(workspace.CodeWriteFailed, Dir, "", "创建目录失败："+err.Error())}
	}
	if err := workspace.WriteFile(root, pr.Path, data); err != nil {
		return nil, []workspace.Problem{errorAt(workspace.CodeWriteFailed, pr.Path, "", "写入失败："+err.Error())}
	}
	pr.doc, pr.data = written, data
	return []string{pr.Path}, nil
}

// realDir 确认 rel（存在时）是真实目录，不经由符号链接写到项目目录之外（workspace-artifacts/validated-path-segments）。
func realDir(root, rel string) *workspace.Problem {
	info, err := os.Lstat(abs(root, rel))
	if errors.Is(err, fs.ErrNotExist) || err == nil && info.IsDir() {
		return nil
	}
	p := errorAt(workspace.CodeWriteFailed, rel, "", "不是真实目录（可能是符号链接），wa-cli 不经由它写入")
	return &p
}

// same 报告两个节点解码后的取值是否相同。
func same(a, b *yaml.Node) bool { return reflect.DeepEqual(schema.Decode(a), schema.Decode(b)) }

func encode(v any) *yaml.Node {
	var n yaml.Node
	if err := n.Encode(v); err != nil {
		panic(fmt.Sprintf("编码 %T 失败：%v", v, err)) // 本包自己的结构体一定能编码
	}
	return &n
}

// Reject 把条目 items 标为 rejected；已确认的条目报告 proposal.already-confirmed。
func Reject(root, id string, items []string, now time.Time) (*Proposal, []string, []workspace.Problem) {
	pr, ps := loadExisting(root, id)
	if ps != nil {
		return nil, nil, ps
	}
	next := *pr
	next.Items = slices.Clone(pr.Items)
	for i, iid := range items {
		it := next.Find(iid)
		switch {
		case it == nil:
			return nil, nil, []workspace.Problem{unknownItem(&next, iid, i)}
		case it.Status == StatusConfirmed:
			return nil, nil, []workspace.Problem{errorAt(CodeAlreadyConfirmed, pr.Path, "", fmt.Sprintf("条目 %s 已确认并写入规范包 %s；要撤回请直接编辑该规范包", iid, pr.Pack))}
		case it.Status == StatusProposed:
			*it = Item{ID: it.ID, Kind: it.Kind, Text: it.Text, Term: it.Term, Key: it.Key, Evidence: it.Evidence, Status: StatusRejected, DecidedAt: stamp(now)}
		}
	}
	changed, ps := next.save(root)
	if ps != nil {
		return nil, nil, ps
	}
	return &next, changed, nil
}

func loadExisting(root, id string) (*Proposal, []workspace.Problem) {
	pr, ps := Load(root, Path(id))
	if ps != nil {
		return nil, ps
	}
	if pr == nil {
		return nil, []workspace.Problem{errorAt(CodeNotFound, "", "", "没有提议 "+id+"；请先用 wa-cli pack learn 生成")}
	}
	return pr, nil
}

func unknownItem(pr *Proposal, id string, i int) workspace.Problem {
	var ids []string
	for _, it := range pr.Items {
		ids = append(ids, it.ID)
	}
	return errorAt(CodeUnknownItem, "", fmt.Sprintf("item[%d]", i), fmt.Sprintf("提议 %s 中没有条目 %s；已有的条目为 %s", pr.ID, id, strings.Join(ids, "、")))
}

func stamp(now time.Time) string { return now.UTC().Format("2006-01-02T15:04:05Z") }

// packOrder 是规范包字段的顺序，新字段按它插入。
var packOrder = []string{"schema_version", "id", "title", "description", "style", "glossary", "banned_terms", "rules", "waivers", "rubric", "methods",
	"scored_metrics", "style_profile", "context_budget", "html_theme", "ai_disclosure_template"}

// Confirm 把条目 items 写入目标规范包并标为 confirmed，confirmation 为作者原话摘要（调用方保证非空）。先完成全部检查（含写出后规范包的
// 校验），先写规范包、再写提议；已确认的条目不再写入。
func Confirm(root, id string, items []string, confirmation string, now time.Time) (*Proposal, []string, []workspace.Problem) {
	pr, ps := loadExisting(root, id)
	if ps != nil {
		return nil, nil, ps
	}
	next := *pr
	next.Items = slices.Clone(pr.Items)
	var apply []Item
	for i, iid := range items {
		it := next.Find(iid)
		if it == nil {
			return nil, nil, []workspace.Problem{unknownItem(&next, iid, i)}
		}
		if it.Status != StatusConfirmed && !slices.ContainsFunc(apply, func(a Item) bool { return a.ID == iid }) {
			apply = append(apply, *it)
			it.Status, it.Confirmation, it.DecidedAt = StatusConfirmed, confirmation, stamp(now)
		}
	}
	if p := checkTarget(root, pr.Pack); p != nil {
		return nil, nil, []workspace.Problem{*p}
	}
	if len(apply) == 0 {
		return &next, nil, nil
	}
	packRel := catalog.PackPath(pr.Pack)
	packData, ps := editPack(root, packRel, pr, apply)
	if ps != nil {
		return nil, nil, ps
	}
	// 提议的写前检查放在写规范包之前：它失败时什么都不写。
	if _, _, ps := next.render(); ps != nil {
		return nil, nil, ps
	}
	if p := realDir(root, Dir); p != nil {
		return nil, nil, []workspace.Problem{*p}
	}
	var changed []string
	if packData != nil {
		if p := realDir(root, workspace.PacksPath); p != nil {
			return nil, nil, []workspace.Problem{*p}
		}
		if err := os.MkdirAll(abs(root, workspace.PacksPath), 0o755); err != nil {
			return nil, nil, []workspace.Problem{errorAt(workspace.CodeWriteFailed, workspace.PacksPath, "", "创建目录失败："+err.Error())}
		}
		if err := workspace.WriteFile(root, packRel, packData); err != nil {
			return nil, nil, []workspace.Problem{errorAt(workspace.CodeWriteFailed, packRel, "", "写入失败："+err.Error())}
		}
		changed = append(changed, packRel)
	}
	more, ps := next.save(root)
	if ps != nil {
		return nil, changed, ps
	}
	return &next, append(changed, more...), nil
}

func cloneNode(n *yaml.Node) *yaml.Node {
	if n == nil {
		return nil
	}
	c := *n
	c.Content = make([]*yaml.Node, len(n.Content))
	for i, ch := range n.Content {
		c.Content[i] = cloneNode(ch)
	}
	return &c
}

// editPack 返回把条目 apply 写入规范包 rel 后的内容；内容未变时返回 nil。规范包不存在时新建；写出后的规范包必须通过工作区规范包的全部校验。
func editPack(root, rel string, pr *Proposal, apply []Item) ([]byte, []workspace.Problem) {
	data, ok, err := workspace.ReadRegular(root, rel)
	if err != nil {
		return nil, []workspace.Problem{errorAt(workspace.CodeReadFailed, rel, "", "读取失败："+err.Error())}
	}
	var doc *yaml.Node
	if ok {
		var p *schema.Problem
		if doc, p = schema.Parse(data); p != nil {
			return nil, workspace.FromSchema(rel, []schema.Problem{*p})
		}
		if _, ps := catalog.LoadPackFile(root, rel); len(ps) > 0 {
			return nil, ps
		}
	} else if _, err := os.Lstat(abs(root, rel)); err == nil {
		return nil, []workspace.Problem{errorAt(schema.CodeNotFound, rel, "", "规范包途经符号链接或不是普通文件，wa-cli 不写入它")}
	}
	if doc == nil || len(doc.Content) == 0 {
		m := &yaml.Node{Kind: yaml.MappingNode}
		m.Content = append(m.Content, workspace.Str("schema_version"), workspace.Int(1), workspace.Str("id"), workspace.Str(pr.Pack), workspace.Str("title"), workspace.Str(pr.Pack+" 的文风"))
		doc = &yaml.Node{Kind: yaml.DocumentNode, Content: []*yaml.Node{m}}
	}
	m := doc.Content[0]
	anchored := func(field string) []workspace.Problem {
		return []workspace.Problem{errorAt(CodeAnchoredValue, rel, field, "该处经由 YAML 锚点、别名或合并键得到，wa-cli 无法只修改这一处；请先去掉锚点、别名与合并键")}
	}
	if m.Kind != yaml.MappingNode || !workspace.Plain(m) || workspace.HasMerge(m) {
		return nil, anchored("")
	}
	seq := func(key string) (*yaml.Node, []workspace.Problem) {
		v, ok := workspace.Lookup(m, key)
		switch {
		case !ok || v != nil && v.Kind != yaml.SequenceNode:
			return nil, anchored(key)
		case v == nil:
			v = &yaml.Node{Kind: yaml.SequenceNode, Tag: "!!seq"}
			workspace.SetKey(m, packOrder, key, v)
		}
		return v, nil
	}
	for _, it := range apply {
		switch it.Kind {
		case KindStyle:
			s, ps := seq("style")
			if ps != nil {
				return nil, ps
			}
			if !slices.ContainsFunc(s.Content, func(n *yaml.Node) bool { return n.Value == it.Text }) {
				s.Content = append(s.Content, workspace.Str(it.Text))
				workspace.BlockSequence(m, "style")
			}
		case KindBannedTerm:
			s, ps := seq("banned_terms")
			if ps != nil {
				return nil, ps
			}
			if !slices.ContainsFunc(s.Content, func(n *yaml.Node) bool { v, _ := workspace.Lookup(n, "term"); return v != nil && v.Value == it.Term }) {
				s.Content = append(s.Content, &yaml.Node{Kind: yaml.MappingNode, Style: yaml.FlowStyle, Content: []*yaml.Node{workspace.Str("term"), workspace.Str(it.Term)}})
				workspace.BlockSequence(m, "banned_terms")
			}
		case KindBaseline:
			if pr.Stats == nil {
				continue
			}
			profile := encode(struct {
				Sources []string `yaml:"sources,flow"`
				Stats   `yaml:",inline"`
			}{pr.Sources, *pr.Stats})
			if f, ok := workspace.SetKey(m, packOrder, "style_profile", nil); !ok {
				return nil, anchored(f)
			}
			workspace.SetKey(m, packOrder, "style_profile", profile)
		}
	}
	out, err := workspace.Encode(doc)
	if err != nil {
		return nil, []workspace.Problem{errorAt(workspace.CodeWriteFailed, rel, "", "生成规范包失败："+err.Error())}
	}
	if _, ps := catalog.LoadPackData(out, "写入后的规范包 "+rel); len(ps) > 0 {
		return nil, ps
	}
	if bytes.Equal(out, data) {
		return nil, nil
	}
	return out, nil
}
