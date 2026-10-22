package preview

import (
	"encoding/base64"
	"fmt"
	"html/template"
	"net/url"
	"slices"
	"strings"

	"github.com/kw12121212/writing-anything/internal/illustration"
	"github.com/kw12121212/writing-anything/internal/workspace"
)

// EmbedBudget 是静态阅读页内嵌配图的总字节数上限（32 MiB），按图片原始字节累计（preview-static）。
const EmbedBudget = 32 << 20

// Image 是阅读页中的一张配图（Illustration）或封面图（Cover Image）：登记信息、是否可用（图片存在、普通文件、SHA-256 与
// 登记一致、格式为 PNG 或 JPEG），以及正文是否引用。Src 由 Render 按页面模式填写：serve 模式为相对页面的取图地址，静态页为
// 预算内的 data URI；Skipped 表示静态页超出内嵌预算未内嵌。
type Image struct {
	Unit       string
	ID         string
	Role       string
	Alt        string
	Title      *string
	Content    string
	Origin     string
	Width      int
	Height     int
	Claims     []string
	Sources    []string
	Path       string // 图片相对项目目录的路径
	Format     string
	Referenced bool
	Available  bool
	Reason     string // 不可用的原因：文件缺失、哈希不符或格式不符
	data       []byte

	Src     template.URL
	Skipped bool
}

// ContentName 返回内容类型的中文名。
func (im *Image) ContentName() string {
	if im.Content == illustration.ContentInfographic {
		return "信息图（Infographic）"
	}
	return "氛围图（Mood Image）"
}

// URL 返回 page.json 中的取图地址：相对页面、不以 / 开头、不含令牌；不可用时为空。
func (im *Image) URL() string {
	if !im.Available {
		return ""
	}
	return "illustration?unit=" + url.QueryEscape(im.Unit) + "&id=" + url.QueryEscape(im.ID)
}

// unavailableReason 判定登记项 it 的图片是否可用，返回图片字节与不可用的原因（可用时为空）。
func unavailableReason(root, dir string, it illustration.Item) ([]byte, string) {
	if it.File != illustration.Prefix+it.ID+"."+illustration.Ext(it.Format) {
		return nil, "格式不符"
	}
	if data, ok := illustration.ReadImage(root, dir, it); ok {
		return data, ""
	}
	if _, ok, _ := workspace.ReadRegular(root, illustration.ImagePath(dir, it)); !ok {
		return nil, "文件缺失"
	}
	return nil, "哈希不符"
}

// buildIllustrations 读取单元目录 dir 的配图登记，把配图挂到引用它们的段落、封面与未引用配图挂到单元，并在段落之后说明
// 未登记或不可用的引用。没有配图登记且正文没有本地配图引用时不做任何事，阅读页与引入配图前相同。
func (out *Unit) buildIllustrations(root, dir string, content []byte) {
	items, exists, ps := illustration.Load(root, dir)
	refs := illustration.Refs(content)
	if !exists && len(refs) == 0 {
		return
	}
	if len(ps) > 0 {
		out.Notes = append(out.Notes, messages("配图登记 illustrations.yaml 不通过校验，不显示配图：", ps)...)
		return
	}
	byFile := map[string]int{}
	for _, it := range items {
		im := Image{Unit: out.Ref, ID: it.ID, Role: it.Role, Alt: it.Alt, Title: it.Title, Content: it.Content, Origin: it.Origin,
			Width: it.Width, Height: it.Height, Claims: it.Claims, Sources: it.Sources, Path: illustration.ImagePath(dir, it), Format: it.Format}
		im.data, im.Reason = unavailableReason(root, dir, it)
		im.Available = im.Reason == ""
		byFile[it.File] = len(out.Illustrations)
		out.Illustrations = append(out.Illustrations, im)
	}
	for _, r := range refs {
		pi := out.paragraphAt(r.Line)
		i, ok := byFile[r.Src]
		var note string
		switch {
		case !ok:
			note = fmt.Sprintf("第 %d 行引用的 %s 不是已登记的配图（未登记）", r.Line, r.Src)
			out.ImageNotes = append(out.ImageNotes, note)
		case out.Illustrations[i].Role == illustration.RoleCover:
			note = fmt.Sprintf("第 %d 行引用了封面图（Cover Image）%s；封面显示在单元开头，不在正文中显示", r.Line, out.Illustrations[i].ID)
		default:
			im := &out.Illustrations[i]
			im.Referenced = true
			if pi >= 0 && !slices.Contains(out.Paragraphs[pi].Illustrations, im.ID) {
				out.Paragraphs[pi].Illustrations = append(out.Paragraphs[pi].Illustrations, im.ID)
				if !im.Available {
					note = fmt.Sprintf("配图 %s 不可用（%s）：%s", im.ID, im.Reason, im.Path)
				}
			}
		}
		if note != "" && pi >= 0 {
			out.Paragraphs[pi].ImageNotes = append(out.Paragraphs[pi].ImageNotes, note)
		}
	}
}

// paragraphAt 返回第 line 行所在段落的下标；不在任何段落中时为 -1。
func (u *Unit) paragraphAt(line int) int {
	for i, p := range u.Paragraphs {
		if line >= p.Line && line <= p.Line+strings.Count(p.Text, "\n") {
			return i
		}
	}
	return -1
}

// image 返回单元中 id 为 id 的配图；没有时为 nil。
func (u *Unit) image(id string) *Image {
	for i := range u.Illustrations {
		if u.Illustrations[i].ID == id {
			return &u.Illustrations[i]
		}
	}
	return nil
}

// Covers 返回单元登记的封面图。
func (u *Unit) Covers() []*Image {
	var out []*Image
	for i := range u.Illustrations {
		if u.Illustrations[i].Role == illustration.RoleCover {
			out = append(out, &u.Illustrations[i])
		}
	}
	return out
}

// Unreferenced 返回已登记但正文没有引用的正文配图。
func (u *Unit) Unreferenced() []*Image {
	var out []*Image
	for i := range u.Illustrations {
		if im := &u.Illustrations[i]; im.Role == illustration.RoleBody && !im.Referenced {
			out = append(out, im)
		}
	}
	return out
}

// Images 返回段落中按引用顺序显示的可用配图。
func (u *Unit) Images(p Paragraph) []*Image {
	var out []*Image
	for _, id := range p.Illustrations {
		if im := u.image(id); im != nil && im.Available {
			out = append(out, im)
		}
	}
	return out
}

// shown 按页面中出现的顺序返回会显示图像的配图：每个单元的封面、各段引用的可用配图与未引用的配图。
func (p *Page) shown() []*Image {
	var out []*Image
	for i := range p.Units {
		u := &p.Units[i]
		out = append(out, u.Covers()...)
		for _, para := range u.Paragraphs {
			out = append(out, u.Images(para)...)
		}
		out = append(out, u.Unreferenced()...)
	}
	return out
}

// prepareImages 按页面模式填写配图的图像地址：serve 模式为相对页面的取图地址（带令牌），静态页按出现顺序在预算内以 data URI
// 内嵌，超出预算的标为未内嵌。同一张图在页面中出现多次时只计一次预算。返回页面是否含图像。
func (p *Page) prepareImages() bool {
	seen := map[*Image]bool{}
	used := 0
	hasImg := false
	for _, im := range p.shown() {
		if !im.Available || seen[im] {
			continue
		}
		seen[im] = true
		switch {
		case p.Serve:
			im.Src = template.URL("illustration?unit=" + url.QueryEscape(im.Unit) + "&id=" + url.QueryEscape(im.ID) + "&token=" + url.QueryEscape(p.Token))
		case used+len(im.data) <= EmbedBudget:
			used += len(im.data)
			// 只对经 ReadImage 校验过（PNG 或 JPEG、哈希一致）的字节构造受信任的 data URI。
			im.Src = template.URL("data:" + illustration.MIME(im.Format) + ";base64," + base64.StdEncoding.EncodeToString(im.data))
		default:
			im.Skipped = true
		}
		hasImg = hasImg || im.Src != "" || im.Skipped
	}
	return hasImg
}
