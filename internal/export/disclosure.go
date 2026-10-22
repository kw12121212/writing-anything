package export

import (
	"math"
	"os"
	"path/filepath"
	"strconv"
	"strings"

	"github.com/kw12121212/writing-anything/internal/catalog"
	"github.com/kw12121212/writing-anything/internal/draft"
	"github.com/kw12121212/writing-anything/internal/illustration"
	"github.com/kw12121212/writing-anything/internal/work"
	"github.com/kw12121212/writing-anything/internal/workspace"
)

// AI 参与说明（AI-generated Content Label）：按导出单元的段落出处（Provenance）汇总 AI 参与情况，有 AI 参与的段落时按模板生成说明。

// CodeUnknownProvenance 表示导出单元有写入记录但无法推导出处。已发布的错误码不得改名或复用。
const CodeUnknownProvenance = "export.unknown-provenance"

// 放置位置：文末、导出元数据或两者。
const (
	PlacementFooter   = "footer"
	PlacementMetadata = "metadata"
	PlacementBoth     = "both"
)

// DisclosureConfig 是合并后的说明配置：放置位置与模板。
type DisclosureConfig struct {
	Placement string
	Template  string
	// ImageTemplate 是配图说明的模板；空时用内置模板。
	ImageTemplate string
}

// Disclosure 是一次导出生成的 AI 参与说明。
type Disclosure struct {
	Text         string
	Placement    string
	AIParagraphs int
	Paragraphs   int
	AIPercent    int
	AIImages     int // 导出配图中出处为 agent 的张数
	Images       int // 导出配图的张数
}

func (d *Disclosure) footer() bool   { return d != nil && d.Placement != PlacementMetadata }
func (d *Disclosure) metadata() bool { return d != nil && d.Placement != PlacementFooter }

// disclose 汇总导出单元 units 的段落出处：出处为 agent 与 agent-draft-human-edited 的段落算作 AI 参与；无法推导出处的单元给出
// warning 并全部算作 AI 参与。没有 AI 参与段落或 cfg 为 nil 时返回 nil。
// DisclosureOf 是 disclose 的导出包装：供组稿（assembly）等整书交付复用按出处的 AI 参与说明。
func DisclosureOf(root string, w *work.Work, units []*work.Unit, cfg *DisclosureConfig) *Disclosure {
	d, _ := disclose(root, w, units, cfg, 0, 0)
	return d
}

// disclose 另按导出配图计入 AI 配图：aiImages 为出处为 agent 的张数，images 为导出配图的张数；aiImages 大于 0 时在正文说明之后
// 直接追加配图说明（没有 AI 参与段落时只有配图说明）。
func disclose(root string, w *work.Work, units []*work.Unit, cfg *DisclosureConfig, aiImages, images int) (*Disclosure, []workspace.Problem) {
	if cfg == nil {
		return nil, nil
	}
	var problems []workspace.Problem
	var ai, total, aiChars, chars int
	for _, u := range units {
		rep, ps := draft.Provenance(root, w, u)
		if len(ps) > 0 && !hasRecord(root, w.UnitDir(u)) {
			// 没有写入记录的单元全部出自作者；出处推导的问题（如段落数超过上限）与 AI 参与无关。
			content, _, _ := draft.Read(root, w, u) // collect 刚读过同一个文件
			for _, p := range draft.Split(content) {
				if !imageOnly(p.Text) {
					total, chars = total+1, chars+p.Chars
				}
			}
			continue
		}
		if len(ps) > 0 {
			problems = append(problems, workspace.Problem{Code: CodeUnknownProvenance, Severity: workspace.SeverityWarning,
				Path:    w.UnitDir(u) + "/provenance.yaml",
				Message: "单元 " + u.Ref + " 的段落出处无法推导（" + ps[0].Message + "），AI 参与说明把它的全部段落算作 AI 参与；请修复写入记录"})
			content, _, _ := draft.Read(root, w, u) // collect 刚读过同一个文件
			for _, p := range draft.Split(content) {
				if !imageOnly(p.Text) {
					ai, total, aiChars, chars = ai+1, total+1, aiChars+p.Chars, chars+p.Chars
				}
			}
			continue
		}
		for _, p := range rep.Paragraphs {
			if imageOnly(p.Text) {
				continue
			}
			total, chars = total+1, chars+p.Chars
			if p.Origin == draft.OriginAgent || p.Origin == draft.OriginEdited {
				ai, aiChars = ai+1, aiChars+p.Chars
			}
		}
	}
	if ai == 0 && aiImages == 0 {
		return nil, problems
	}
	percent, text := 0, ""
	if ai > 0 {
		percent = 100 // 有 AI 参与段落时字数为 0 只可能是段落全为空白，不会出现；按全部为 AI 参与处理
		if chars > 0 {
			percent = int(math.Round(float64(aiChars) * 100 / float64(chars)))
		}
		text = strings.TrimSpace(strings.NewReplacer("{ai_percent}", strconv.Itoa(percent), "{ai_paragraphs}", strconv.Itoa(ai), "{paragraphs}", strconv.Itoa(total)).Replace(cfg.Template))
	}
	if aiImages > 0 {
		tpl := cfg.ImageTemplate
		if tpl == "" {
			tpl = catalog.DefaultDisclosureImageTemplate
		}
		text += strings.TrimSpace(strings.NewReplacer("{ai_images}", strconv.Itoa(aiImages), "{images}", strconv.Itoa(images)).Replace(tpl))
	}
	return &Disclosure{Text: text, Placement: cfg.Placement, AIParagraphs: ai, Paragraphs: total, AIPercent: percent, AIImages: aiImages, Images: images}, problems
}

// imageOnly 报告段落是否只由本地配图引用（地址以 illustrations/ 开头的图片）与空白组成：这类段落是插入的配图位置，
// 不算正文段落，不计入段落数与字数；配图本身按导出配图计入配图说明。
func imageOnly(text string) bool {
	refs := illustration.Refs([]byte(text))
	if len(refs) == 0 {
		return false
	}
	var b strings.Builder
	last := 0
	for _, r := range refs {
		b.WriteString(text[last:r.Start])
		last = r.End
	}
	b.WriteString(text[last:])
	return strings.TrimSpace(b.String()) == ""
}

// hasRecord 报告单元目录 dir 下是否有写入记录 provenance.yaml（任何类型的文件都算，途经符号链接等问题由出处推导报告）。
func hasRecord(root, dir string) bool {
	_, err := os.Lstat(filepath.Join(root, filepath.FromSlash(dir+"/provenance.yaml")))
	return err == nil
}
