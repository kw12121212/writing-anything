package series

import (
	"bytes"
	"fmt"
	"os"
	"time"
	"unicode"

	"github.com/kw12121212/writing-anything/internal/catalog"
	"github.com/kw12121212/writing-anything/internal/draft"
	"github.com/kw12121212/writing-anything/internal/review"
	"github.com/kw12121212/writing-anything/internal/schema"
	"github.com/kw12121212/writing-anything/internal/work"
	"github.com/kw12121212/writing-anything/internal/workspace"
	"go.yaml.in/yaml/v3"
)

// 摘要的错误码（Error Code）。已发布的错误码不得改名或复用。
const (
	CodeNotAccepted = "summary.not-accepted"
	CodeLength      = "summary.length"
)

// SummaryName 是单元目录中摘要的文件名。
const SummaryName = "summary.yaml"

// Summary 是一个单元的摘要（Summary）。Stale 为当前正文与写入摘要时的正文不同（或没有正文）。
type Summary struct {
	Text        string `json:"text"`
	DraftSHA256 string `json:"draft_sha256"`
	RecordedAt  string `json:"recorded_at"`
	Path        string `json:"-"`
	Chars       int    `json:"-"`
	Stale       bool   `json:"-"`
}

// SummaryPath 返回单元目录 dir 下 summary.yaml 的路径。
func SummaryPath(dir string) string { return dir + "/" + SummaryName }

// chars 返回 s 中非空白字符（unicode.IsSpace 之外）的个数。
func chars(s string) int {
	n := 0
	for _, r := range s {
		if !unicode.IsSpace(r) {
			n++
		}
	}
	return n
}

func loadSummaryFile(root, rel string) (*Summary, []workspace.Problem) {
	data, ok, err := workspace.ReadRegular(root, rel)
	switch {
	case err != nil:
		return nil, []workspace.Problem{errorAt(workspace.CodeReadFailed, rel, "", "读取失败："+err.Error())}
	case !ok:
		if _, err := os.Lstat(abs(root, rel)); err == nil {
			return nil, []workspace.Problem{errorAt(schema.CodeNotFound, rel, "", "summary.yaml 途经符号链接或不是普通文件，按不存在处理")}
		}
		return nil, nil
	}
	doc, p := schema.Parse(data)
	if p != nil {
		return nil, workspace.FromSchema(rel, []schema.Problem{*p})
	}
	if ps := schema.Named("summary").ValidateNode(doc); len(ps) > 0 {
		return nil, workspace.FromSchema(rel, ps)
	}
	var s Summary
	catalog.Decode(doc, &s)
	s.Path, s.Chars = rel, chars(s.Text)
	return &s, nil
}

// LoadSummary 读取单元 u 的摘要并判断是否过期；没有摘要时返回 nil 与 nil。
func LoadSummary(root string, w *work.Work, u *work.Unit) (*Summary, []workspace.Problem) {
	s, ps := loadSummaryFile(root, SummaryPath(w.UnitDir(u)))
	if s == nil {
		return nil, ps
	}
	content, ok, ps := draft.Read(root, w, u)
	if len(ps) > 0 {
		return nil, ps
	}
	s.Stale = !ok || review.SHA256(content) != s.DraftSHA256
	return s, nil
}

// SetSummary 写入单元 u 的摘要：单元必须已验收（acceptance.yaml 合法且与当前正文一致），text 的非空白字符数在 length 内。
// 整体替换已有的摘要；内容未变时不写文件。
func SetSummary(root string, w *work.Work, u *work.Unit, text string, length catalog.Range, now time.Time) (*Summary, []string, []workspace.Problem) {
	dir := w.UnitDir(u)
	rel := SummaryPath(dir)
	content, ok, ps := draft.Read(root, w, u)
	if len(ps) > 0 {
		return nil, nil, ps
	}
	a, ps := review.LoadAcceptance(root, dir)
	sha := review.SHA256(content)
	if len(ps) > 0 || a == nil || !ok || a.DraftSHA256 != sha {
		return nil, nil, []workspace.Problem{errorAt(CodeNotAccepted, review.AcceptancePath(dir), "",
			"单元 "+u.Ref+" 还没有通过验收（没有合法的 acceptance.yaml，或验收之后正文又被修改）；摘要只能为已验收的内容撰写")}
	}
	if n := chars(text); n < length.Min || n > length.Max {
		return nil, nil, []workspace.Problem{errorAt(CodeLength, "", "text", fmt.Sprintf("摘要有 %d 个字；作品文体要求 %d 到 %d 个字", n, length.Min, length.Max))}
	}
	s := &Summary{Text: text, DraftSHA256: sha, RecordedAt: now.UTC().Format("2006-01-02T15:04:05Z"), Path: rel, Chars: chars(text)}
	if old, _ := loadSummaryFile(root, rel); old != nil && old.Text == text && old.DraftSHA256 == sha {
		old.Chars = chars(old.Text)
		return old, nil, nil
	}
	m := &yaml.Node{Kind: yaml.MappingNode}
	m.Content = append(m.Content, workspace.Str("schema_version"), workspace.Int(1), workspace.Str("text"), workspace.Str(text),
		workspace.Str("draft_sha256"), workspace.Str(sha), workspace.Str("recorded_at"), workspace.Str(s.RecordedAt))
	data, err := workspace.Encode(&yaml.Node{Kind: yaml.DocumentNode, HeadComment: "已验收单元的摘要（Summary）；请用 wa-cli summary set 修改。", Content: []*yaml.Node{m}})
	if err != nil {
		return nil, nil, []workspace.Problem{errorAt(workspace.CodeWriteFailed, rel, "", "生成摘要失败："+err.Error())}
	}
	if ps := schema.Named("summary").Validate(data); len(ps) > 0 {
		return nil, nil, workspace.FromSchema(rel, ps)
	}
	if old, _, _ := workspace.ReadRegular(root, rel); bytes.Equal(old, data) {
		return s, nil, nil
	}
	if err := workspace.WriteFile(root, rel, data); err != nil {
		return nil, nil, []workspace.Problem{errorAt(workspace.CodeWriteFailed, rel, "", "写入失败："+err.Error())}
	}
	return s, []string{rel}, nil
}

// CheckSummaries 校验 works 中每个单元目录下存在的 summary.yaml，返回已校验的路径与问题。
func CheckSummaries(root string, works []*work.Work) (checked []string, problems []workspace.Problem) {
	for _, w := range works {
		for _, u := range w.Units() {
			rel := SummaryPath(w.UnitDir(u))
			if _, err := os.Lstat(abs(root, rel)); err != nil {
				continue
			}
			checked = append(checked, rel)
			_, ps := loadSummaryFile(root, rel)
			problems = append(problems, ps...)
		}
	}
	return checked, problems
}

// Preceding 是系列中一个前序作品的摘要。Summary 为 nil 表示该作品的根单元还没有摘要，或摘要无法读取（Problems 非空）。
type Preceding struct {
	Work     string
	Summary  *Summary
	Problems []workspace.Problem
}

// PrecedingSummaries 返回系列 s 中排在作品 workID 之前的每个作品的根单元摘要，按 works 的顺序；无法加载的作品跳过。
// 别的作品的摘要读不出来不应让当前作品的命令失败，问题记在该项的 Problems 中，由调用方降为 warning。
func PrecedingSummaries(root string, s *Series, workID string) []Preceding {
	var out []Preceding
	for _, id := range s.Works {
		if id == workID {
			break
		}
		w, u, ps := work.Resolve(root, id)
		if len(ps) > 0 {
			continue
		}
		sum, ps := LoadSummary(root, w, u)
		out = append(out, Preceding{Work: id, Summary: sum, Problems: ps})
	}
	return out
}
