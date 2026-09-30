// Package draft 管理单元（Unit）的正文与出处（Provenance）：wa-cli draft write 把正文写入单元目录下的
// draft.md 并在 provenance.yaml 中留下写入记录（Draft Record），出处由写入记录与当前正文逐段对比推导；
// 同目录的 claims.yaml 登记论断（Claim）及其关联的素材（Source）。本包只返回结构化的问题，由命令层转换为
// 诊断（Diagnostic）。
package draft

import (
	"bytes"
	"errors"
	"fmt"
	"io/fs"
	"os"
	"path/filepath"
	"strings"

	"github.com/kw12121212/writing-anything/internal/catalog"
	"github.com/kw12121212/writing-anything/internal/schema"
	"github.com/kw12121212/writing-anything/internal/source"
	"github.com/kw12121212/writing-anything/internal/work"
	"github.com/kw12121212/writing-anything/internal/workspace"
	"go.yaml.in/yaml/v3"
)

// 正文与论断的错误码（Error Code）。已发布的错误码不得改名或复用。
const (
	CodeEmpty         = "draft.empty"
	CodeNotFound      = "draft.not-found"
	CodeTooLarge      = "draft.too-large"
	CodeHumanEdited   = "draft.human-edited"
	CodeBaseMismatch  = "draft.base-mismatch"
	CodeUnknownSource = "claim.unknown-source"
	CodeAnchoredValue = "claim.anchored-value"
)

// MaxParagraphs 是正文允许的最大段落数；出处推导的内存随段落数平方增长，不可信的正文不能不受限制。
const MaxParagraphs = 10000

func tooLarge(path string, n int) workspace.Problem {
	return errorAt(CodeTooLarge, path, "", fmt.Sprintf("正文有 %d 个段落，超过上限 %d；请把单元拆分为多个子单元", n, MaxParagraphs))
}

// 单元目录中的文件名。
const (
	FileName   = "draft.md"
	RecordName = "provenance.yaml"
	ClaimsName = "claims.yaml"
)

func errorAt(code, path, field, msg string) workspace.Problem {
	return workspace.Problem{Code: code, Severity: workspace.SeverityError, Path: path, Field: field, Message: msg}
}

func abs(root, rel string) string { return filepath.Join(root, filepath.FromSlash(rel)) }

// readRegular 读取工作区中的普通文件 rel；工作区目录之下途经的每一级都必须是真实目录、文件本身必须是普通文件，
// 否则（包括不存在、途经符号链接）返回 nil 与 false，避免跟随符号链接读取项目目录之外的文件。
func readRegular(root, rel string) ([]byte, bool, error) {
	segs := strings.Split(rel, "/")
	for i := 2; i <= len(segs); i++ {
		info, err := os.Lstat(abs(root, strings.Join(segs[:i], "/")))
		if err != nil || i < len(segs) && !info.IsDir() || i == len(segs) && !info.Mode().IsRegular() {
			return nil, false, nil
		}
	}
	data, err := os.ReadFile(abs(root, rel))
	return data, err == nil, err
}

// record 是一份已加载的写入记录。
type record struct {
	SHA256     string     `json:"sha256"`
	Paragraphs []recorded `json:"paragraphs"`
}

// loadRecord 读取单元目录 dir 下的写入记录；没有写入记录时返回 nil 与 nil。
func loadRecord(root, dir string) (*record, []workspace.Problem) {
	rel := dir + "/" + RecordName
	if _, ok, _ := readRegular(root, rel); !ok {
		return nil, nil
	}
	doc, ps := workspace.Read(root, rel)
	if doc == nil {
		return nil, ps
	}
	if ps := schema.Named("provenance").ValidateNode(doc); len(ps) > 0 {
		return nil, workspace.FromSchema(rel, ps)
	}
	var f record
	catalog.Decode(doc, &f)
	return &f, nil
}

func (r *record) paragraphs() []recorded {
	if r == nil {
		return nil
	}
	return r.Paragraphs
}

// current 读取单元目录 dir 下的正文并推导每个段落的出处；没有正文时返回 nil 与 false。
func current(root, dir string) ([]byte, []Paragraph, bool, []workspace.Problem) {
	data, ok, err := readRegular(root, dir+"/"+FileName)
	if err != nil {
		return nil, nil, false, []workspace.Problem{errorAt(workspace.CodeReadFailed, dir+"/"+FileName, "", "读取失败："+err.Error())}
	}
	if !ok {
		return nil, nil, false, nil
	}
	rec, ps := loadRecord(root, dir)
	if len(ps) > 0 {
		return nil, nil, false, ps
	}
	paras := Split(data)
	if len(paras) > MaxParagraphs {
		return nil, nil, false, []workspace.Problem{tooLarge(dir+"/"+FileName, len(paras))}
	}
	derive(paras, rec.paragraphs(), rec != nil)
	return data, paras, true, nil
}

// Written 是一次正文写入的结果。
type Written struct {
	Path       string // draft.md 相对项目目录的路径
	Paragraphs int
	SHA256     string
}

// Precheck 执行 Write 在修改任何文件之前的全部检查：内容非空、段落数不超过上限、Guard。命令层在记入批注等写入
// 之前调用它，保证检查失败时不修改任何文件。
func Precheck(root string, w *work.Work, u *work.Unit, content []byte, base string) []workspace.Problem {
	if strings.TrimSpace(string(content)) == "" {
		return []workspace.Problem{errorAt(CodeEmpty, "", "", "正文为空或只含空白，没有可以写入的内容")}
	}
	if n := len(Split(content)); n > MaxParagraphs {
		return []workspace.Problem{tooLarge("", n)}
	}
	if ps := Guard(root, w, u, content, base); len(ps) > 0 {
		return ps
	}
	// 写入时还要推导现有正文的出处：它超过段落上限或写入记录无法加载都会失败，须在任何写入之前报告。
	_, _, _, ps := current(root, w.UnitDir(u))
	return ps
}

// Guard 检查向单元 u 写入 content 是否会覆盖作者的改动（人工编辑保护（Human Edit Protection））：draft.md 存在且
// 没有写入记录、或其 SHA-256 与记录不同，即视为作者改过；此时 content 与当前正文逐字节相同照常允许，否则 base 必须
// 等于当前正文的 SHA-256。base 非空时无论作者是否改过都必须等于当前 SHA-256。
func Guard(root string, w *work.Work, u *work.Unit, content []byte, base string) []workspace.Problem {
	dir := w.UnitDir(u)
	rel := dir + "/" + FileName
	cur, ok, err := readRegular(root, rel)
	if err != nil {
		return []workspace.Problem{errorAt(workspace.CodeReadFailed, rel, "", "读取失败："+err.Error())}
	}
	if !ok {
		return nil
	}
	rec, ps := loadRecord(root, dir)
	if len(ps) > 0 {
		return ps
	}
	sum := source.Hash(cur)
	edited := rec == nil || rec.SHA256 != sum
	switch {
	case base != "" && base != sum:
		return []workspace.Problem{errorAt(CodeBaseMismatch, rel, "", "--base 与当前正文的 SHA-256 不一致：作者在你取得 base 之后又修改了正文；请重新用 wa-cli diff 查看改动并取得新的 base")}
	case edited && base == "" && !bytes.Equal(cur, content):
		return []workspace.Problem{errorAt(CodeHumanEdited, rel, "", "作者在最近一次 draft write 之后修改过正文，拒绝覆盖；请先用 wa-cli diff 查看作者的改动，在作者的版本上修改后用 --base 声明基于当前版本写入")}
	}
	return nil
}

// Write 把 content 逐字节写入单元 u 的 draft.md，并写出写入记录；写入前执行 Guard。新正文中与写入前正文里出处为
// human 或 agent-draft-human-edited 的段落哈希相同的段落保留该出处，其余为 agent。返回写入结果与变更的路径（新建的
// 单元目录与内容变化的文件）；正文与记录都不变时不写文件。
func Write(root string, w *work.Work, u *work.Unit, content []byte, base string) (*Written, []string, []workspace.Problem) {
	if ps := Precheck(root, w, u, content, base); len(ps) > 0 {
		return nil, nil, ps
	}
	dir := w.UnitDir(u)
	draftRel, recordRel := dir+"/"+FileName, dir+"/"+RecordName
	oldDraft, oldParas, _, ps := current(root, dir)
	if len(ps) > 0 {
		return nil, nil, ps
	}
	kept := map[string]string{}
	for _, p := range oldParas {
		if p.Origin != OriginAgent {
			kept[p.Hash] = p.Origin
		}
	}
	paras := Split(content)
	for i := range paras {
		paras[i].Origin = OriginAgent
		if o, ok := kept[paras[i].Hash]; ok {
			paras[i].Origin = o
		}
	}
	sum := source.Hash(content)
	record, ps := encodeRecord(recordRel, sum, paras)
	if len(ps) > 0 {
		return nil, nil, ps
	}
	oldRecord, _, _ := readRegular(root, recordRel)
	var changed []string
	created, ps := ensureDir(root, w, u)
	if len(ps) > 0 {
		return nil, nil, ps
	}
	changed = append(changed, created...)
	for _, f := range []struct {
		rel       string
		old, data []byte
	}{{draftRel, oldDraft, content}, {recordRel, oldRecord, record}} {
		if f.old != nil && bytes.Equal(f.old, f.data) {
			continue
		}
		if err := workspace.WriteFile(root, f.rel, f.data); err != nil {
			return nil, nil, []workspace.Problem{errorAt(workspace.CodeWriteFailed, f.rel, "", "写入失败："+err.Error())}
		}
		changed = append(changed, f.rel)
	}
	return &Written{Path: draftRel, Paragraphs: len(paras), SHA256: sum}, changed, nil
}

// encodeRecord 生成整篇哈希为 sum、段落出处为 paras 的写入记录，并按 Schema 复查。
func encodeRecord(rel, sum string, paras []Paragraph) ([]byte, []workspace.Problem) {
	seq := &yaml.Node{Kind: yaml.SequenceNode, Tag: "!!seq"}
	for _, p := range paras {
		seq.Content = append(seq.Content, &yaml.Node{Kind: yaml.MappingNode, Style: yaml.FlowStyle,
			Content: []*yaml.Node{str("hash"), str(p.Hash), str("origin"), str(p.Origin)}})
	}
	if len(seq.Content) == 0 {
		seq.Style = yaml.FlowStyle
	}
	m := &yaml.Node{Kind: yaml.MappingNode, Content: []*yaml.Node{
		str("schema_version"), {Kind: yaml.ScalarNode, Tag: "!!int", Value: "1"},
		str("sha256"), str(sum),
		str("paragraphs"), seq,
	}}
	doc := &yaml.Node{Kind: yaml.DocumentNode, HeadComment: "写入记录（Draft Record）：由 wa-cli draft write 生成，用于推导段落出处（Provenance），请勿修改。", Content: []*yaml.Node{m}}
	data, err := workspace.Encode(doc)
	if err != nil {
		return nil, []workspace.Problem{errorAt(workspace.CodeWriteFailed, rel, "", "生成写入记录失败："+err.Error())}
	}
	if ps := schema.Named("provenance").Validate(data); len(ps) > 0 {
		return nil, workspace.FromSchema(rel, ps)
	}
	return data, nil
}

// Rebaseline 在 wa-cli 自己把单元 u 的正文从 old 改为 now（如移除批注）之后调用：old 的 SHA-256 等于写入记录时，
// 按 old 各段的出处重建写入记录（对齐上的与配对上的段落继承原出处，多出的为 human），使这次修改不被当作人工编辑；
// 否则（作者已改过正文或没有写入记录）不改动。返回写入的路径。
func Rebaseline(root string, w *work.Work, u *work.Unit, old, now []byte) ([]string, []workspace.Problem) {
	dir := w.UnitDir(u)
	rec, ps := loadRecord(root, dir)
	if len(ps) > 0 || rec == nil || rec.SHA256 != source.Hash(old) {
		return nil, ps
	}
	before := Split(old)
	derive(before, rec.Paragraphs, true)
	var base []recorded
	for _, p := range before {
		base = append(base, recorded{Hash: p.Hash, Origin: p.Origin})
	}
	after := Split(now)
	derive(after, base, true, true)
	rel := dir + "/" + RecordName
	data, ps := encodeRecord(rel, source.Hash(now), after)
	if len(ps) > 0 {
		return nil, ps
	}
	if err := workspace.WriteFile(root, rel, data); err != nil {
		return nil, []workspace.Problem{errorAt(workspace.CodeWriteFailed, rel, "", "写入失败："+err.Error())}
	}
	return []string{rel}, nil
}

// Change 是作者自最近一次 draft write 以来的一处段落改动。
type Change struct {
	Kind string // added、edited 或 removed
	At   int    // added 与 edited 为段落序号；removed 为其后第一个当前段落的序号，位于末尾时为段落数加一
	Line int    // 段落首行的行号；removed 为 0
	Text string // 段落当前的文字；removed 为空
}

// Changes 是单元正文相对写入记录的改动。
type Changes struct {
	Path     string // draft.md 相对项目目录的路径
	Edited   bool   // 作者是否改过正文
	Base     string // 当前正文的 SHA-256
	Recorded string // 写入记录的 sha256；没有写入记录时为空
	Changes  []Change
	Total    int // 当前正文的段落数
}

// Diff 按写入记录逐段列出作者对单元 u 正文的改动：对齐后未对齐区间内配对上的当前段落为 edited，多出的当前段落为
// added，多出的记录段落为 removed；没有写入记录时每个段落都是 added。
func Diff(root string, w *work.Work, u *work.Unit) (*Changes, []workspace.Problem) {
	dir := w.UnitDir(u)
	rel := dir + "/" + FileName
	data, ok, err := readRegular(root, rel)
	switch {
	case err != nil:
		return nil, []workspace.Problem{errorAt(workspace.CodeReadFailed, rel, "", "读取失败："+err.Error())}
	case !ok:
		return nil, []workspace.Problem{errorAt(CodeNotFound, rel, "", "单元 "+u.Ref+" 还没有正文；请用 wa-cli draft write 写入")}
	}
	rec, ps := loadRecord(root, dir)
	if len(ps) > 0 {
		return nil, ps
	}
	paras := Split(data)
	if len(paras) > MaxParagraphs {
		return nil, []workspace.Problem{tooLarge(rel, len(paras))}
	}
	c := &Changes{Path: rel, Base: source.Hash(data), Changes: []Change{}, Total: len(paras)}
	c.Edited = rec == nil || rec.SHA256 != c.Base
	if rec != nil {
		c.Recorded = rec.SHA256
	}
	_, gaps := align(hashes(paras), recordHashes(rec.paragraphs()))
	for _, g := range gaps {
		for k, i := range g.cur {
			kind := "added"
			if k < len(g.rec) && rec != nil {
				kind = "edited"
			}
			c.Changes = append(c.Changes, Change{Kind: kind, At: i + 1, Line: paras[i].Line, Text: paras[i].Text})
		}
		for k := len(g.cur); k < len(g.rec); k++ {
			c.Changes = append(c.Changes, Change{Kind: "removed", At: g.next + 1})
		}
	}
	return c, nil
}

func str(v string) *yaml.Node { return &yaml.Node{Kind: yaml.ScalarNode, Tag: "!!str", Value: v} }

// ensureDir 确保单元 u 的单元目录存在，返回新建的单元目录（已存在时为空）。子单元的 units/ 与单元目录必须是
// 真实目录，不跟随符号链接写出项目目录。
func ensureDir(root string, w *work.Work, u *work.Unit) ([]string, []workspace.Problem) {
	dir := w.UnitDir(u)
	if u.Parent == nil {
		return nil, nil
	}
	parent := work.Dir(w.ID) + "/units"
	for _, rel := range []string{parent, dir} {
		info, err := os.Lstat(abs(root, rel))
		switch {
		case err == nil && info.IsDir():
			continue
		case err == nil:
			return nil, []workspace.Problem{errorAt(workspace.CodeWriteFailed, rel, "", "不是真实目录，无法在其中写入单元产物")}
		case !errors.Is(err, fs.ErrNotExist):
			return nil, []workspace.Problem{errorAt(workspace.CodeReadFailed, rel, "", "读取失败："+err.Error())}
		}
		if err := os.Mkdir(abs(root, rel), 0o755); err != nil {
			return nil, []workspace.Problem{errorAt(workspace.CodeWriteFailed, rel, "", "创建目录失败："+err.Error())}
		}
		if rel == dir {
			return []string{dir}, nil
		}
	}
	return nil, nil
}

// Report 是单元正文的出处报告。
type Report struct {
	Path       string // draft.md 相对项目目录的路径
	Paragraphs []Paragraph
	Summary    []Share
	TotalChars int
}

// Provenance 推导单元 u 当前正文的逐段出处与单元级汇总；单元目录下没有 draft.md 时报告 draft.not-found。
func Provenance(root string, w *work.Work, u *work.Unit) (*Report, []workspace.Problem) {
	dir := w.UnitDir(u)
	_, paras, ok, ps := current(root, dir)
	if len(ps) > 0 {
		return nil, ps
	}
	if !ok {
		return nil, []workspace.Problem{errorAt(CodeNotFound, dir+"/"+FileName, "", "单元 "+u.Ref+" 还没有正文；请用 wa-cli draft write 写入")}
	}
	if paras == nil {
		paras = []Paragraph{}
	}
	summary, total := summarize(paras)
	return &Report{Path: dir + "/" + FileName, Paragraphs: paras, Summary: summary, TotalChars: total}, nil
}

// Read 读取单元 u 的正文 draft.md；单元目录途经符号链接或 draft.md 不是普通文件时视为没有正文，返回 false。
func Read(root string, w *work.Work, u *work.Unit) ([]byte, bool, []workspace.Problem) {
	rel := w.UnitDir(u) + "/" + FileName
	data, ok, err := readRegular(root, rel)
	if err != nil {
		return nil, false, []workspace.Problem{errorAt(workspace.CodeReadFailed, rel, "", "读取失败："+err.Error())}
	}
	return data, ok, nil
}

// ReadUnitFile 读取工作区中的普通文件 rel，规则同 Read：途经符号链接或不是普通文件时返回 false。
func ReadUnitFile(root, rel string) ([]byte, bool, error) { return readRegular(root, rel) }

// EnsureUnitDir 确保单元 u 的单元目录存在，返回新建的单元目录（已存在时为空）；不跟随符号链接。
func EnsureUnitDir(root string, w *work.Work, u *work.Unit) ([]string, []workspace.Problem) {
	return ensureDir(root, w, u)
}
