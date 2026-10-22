package export

import (
	"path"
	"slices"
	"strings"

	"github.com/kw12121212/writing-anything/internal/illustration"
	"github.com/kw12121212/writing-anything/internal/mdscan"
	"github.com/kw12121212/writing-anything/internal/work"
	"github.com/kw12121212/writing-anything/internal/workspace"
)

// Asset 是复制到资产目录（Asset Directory）的一张配图（Illustration）或封面图（Cover Image）：Unit 为单元引用，Source 为工作区中的
// 图片路径，Path 为资产文件路径（都相对项目目录）。
type Asset struct {
	Unit   string
	ID     string
	Role   string
	Origin string
	Source string
	Path   string
	Alt    string
	data   []byte
}

// occurrence 是导出正文中的一个 Markdown 图片（按出现顺序）：src 为正文中写的地址，asset 为它对应的资产下标（不是已复制的本地配图时为 -1）。
type occurrence struct {
	src   string
	asset int
}

// AssetDir 返回导出文件 rel 的资产目录：同目录下的「导出文件名去掉最后一个扩展名（没有扩展名时为整个文件名）加 .assets」。
func AssetDir(rel string) string {
	dir, base := path.Split(rel)
	if ext := path.Ext(base); ext != "" && ext != base {
		base = strings.TrimSuffix(base, ext)
	}
	return dir + base + ".assets"
}

// assetName 返回单元 u 中配图 it 的资产文件名：根单元为 <work>.<id>.<ext>，子单元为 <work>.<单元 id>.<id>.<ext>。id 不含点号，
// 不同单元的同名配图不会冲突。
func assetName(w *work.Work, u *work.Unit, it illustration.Item) string {
	name := w.ID + "."
	if u.Parent != nil {
		name += u.ID + "."
	}
	return name + it.ID + "." + illustration.Ext(it.Format)
}

// planAssets 为导出的单元 units（正文为 parts，已去掉过程注释）规划资产：根单元被导出时的封面在前，其余为正文中本地配图引用所指的
// 已登记配图，按首次被引用的顺序，同一配图只复制一次；图片不可读或与登记的哈希不一致时不复制、引用原样保留。返回把这些引用的地址
// 改写为资产路径（相对导出文件）后的正文、资产与按出现顺序的全部图片（供配图清单对应编号）。读取工作区中的配图时不跟随符号链接。
func planAssets(root string, w *work.Work, units []*work.Unit, parts []string, rel string) ([]string, []Asset, []occurrence) {
	dir := AssetDir(rel)
	dirName := path.Base(dir)
	var assets []Asset
	index := map[string]int{} // 资产路径 → 下标
	add := func(u *work.Unit, it illustration.Item) int {
		p := dir + "/" + assetName(w, u, it)
		if i, ok := index[p]; ok {
			return i
		}
		data, ok := illustration.ReadImage(root, w.UnitDir(u), it)
		if !ok {
			return -1
		}
		index[p] = len(assets)
		assets = append(assets, Asset{Unit: u.Ref, ID: it.ID, Role: it.Role, Origin: it.Origin, Source: illustration.ImagePath(w.UnitDir(u), it), Path: p, Alt: it.Alt, data: data})
		return len(assets) - 1
	}
	if len(units) > 0 && units[0].Parent == nil {
		items, _, _ := illustration.Load(root, w.UnitDir(units[0]))
		for _, it := range items {
			if it.Role == illustration.RoleCover {
				add(units[0], it)
				break
			}
		}
	}
	out := slices.Clone(parts)
	var occ []occurrence
	for k, u := range units {
		items, _, _ := illustration.Load(root, w.UnitDir(u))
		byFile := map[string]illustration.Item{}
		for _, it := range items {
			byFile[it.File] = it
		}
		body := parts[k]
		var b strings.Builder
		last := 0
		for _, im := range mdscan.Images(body) {
			o := occurrence{src: im.Src, asset: -1}
			if it, ok := byFile[im.Src]; ok && strings.HasPrefix(im.Src, illustration.Prefix) {
				o.asset = add(u, it)
			}
			occ = append(occ, o)
			if o.asset < 0 {
				continue
			}
			// 地址紧跟在「![替代文字](」之后（可以用尖括号包住）；只替换地址本身，其余字节不变。
			at := im.Start + 2 + len(im.Alt) + 2
			if at < len(body) && body[at] == '<' {
				at++
			}
			if at+len(im.Src) > len(body) || body[at:at+len(im.Src)] != im.Src {
				continue
			}
			b.WriteString(body[last:at])
			b.WriteString(dirName + "/" + path.Base(assets[o.asset].Path))
			last = at + len(im.Src)
		}
		if last > 0 {
			b.WriteString(body[last:])
			out[k] = b.String()
		}
	}
	return out, assets, occ
}

// cover 返回资产中的封面；没有时为 nil。
func cover(assets []Asset) *Asset {
	for i := range assets {
		if assets[i].Role == illustration.RoleCover {
			return &assets[i]
		}
	}
	return nil
}

// countAI 返回资产中出处为 agent 的张数。
func countAI(assets []Asset) int {
	n := 0
	for _, a := range assets {
		if a.Origin == illustration.OriginAgent {
			n++
		}
	}
	return n
}

// checkAssets 不带副作用地检查全部资产文件的路径（资产目录必须是真实目录或不存在，已存在的资产文件必须是普通文件，见 export-output）。
func checkAssets(root string, assets []Asset, code string) []workspace.Problem {
	for _, a := range assets {
		if ps := checkParents(root, a.Path, code); len(ps) > 0 {
			return ps
		}
	}
	return nil
}

// writeAssets 写出全部资产文件（原子写入，已存在时覆盖），返回新建的目录（资产目录）与写入的资产文件，按资产顺序；资产目录中已有的其他
// 文件不删除。
func writeAssets(root string, assets []Asset, code string) ([]string, []workspace.Problem) {
	var changed []string
	for _, a := range assets {
		more, ps := write(root, a.Path, a.data, code)
		if len(ps) > 0 {
			return changed, ps
		}
		for _, c := range more {
			if !slices.Contains(changed, c) {
				changed = append(changed, c)
			}
		}
	}
	return changed, nil
}

// relToExport 返回资产 a 相对导出文件（或同目录的配图清单）的路径。
func relToExport(a Asset) string {
	dir, file := path.Split(a.Path)
	return path.Base(strings.TrimSuffix(dir, "/")) + "/" + file
}
