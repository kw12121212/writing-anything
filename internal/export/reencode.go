package export

import (
	"bytes"
	"image"
	"image/jpeg"
	_ "image/png" // 解码 PNG 配图

	"github.com/kw12121212/writing-anything/internal/illustration"
)

// 为粘贴重新编码（paste-html-export）：公众号图文编辑器在一次粘贴中遇到单张超过约 1 MiB 的 base64 图片时会丢掉全部图片
// （2026-10-23 实测），可粘贴正文因此把超过 PasteImageMaxBytes 的配图缩小并编码为 JPEG。只对经 illustration.ReadImage 校验过的
// 字节解码；重新编码的结果由标准库编码器产生，MIME 固定为 image/jpeg（workspace-artifacts/trusted-data-uri）。

// PasteImageMaxBytes 是可粘贴正文中单张内嵌图片的字节数上限（1 MiB）。
const PasteImageMaxBytes = 1 << 20

// pasteMaxPixels 是重新编码前允许解码的最大像素数（宽×高），防止很小的文件解码出巨大的位图（workspace-artifacts/input-limits）。
const pasteMaxPixels = 40_000_000

// 重新编码依次尝试的最长边与 JPEG 质量。
var (
	pasteSides     = []int{1600, 1200, 900}
	pasteQualities = []int{85, 75, 65, 55}
)

// pasteImage 返回配图在可粘贴正文中内嵌的字节与格式：不超过 PasteImageMaxBytes 的 PNG/JPEG 原样返回；更大的按 pasteSides 与
// pasteQualities 缩小并在白底上编码为 JPEG，取第一个不超过上限的结果。不是 PNG/JPEG、像素过多、无法解码或压不到上限时返回 false。
func pasteImage(data []byte) ([]byte, string, bool) {
	format, _, _ := illustration.Detect(data)
	if format == "" {
		return nil, "", false
	}
	if len(data) <= PasteImageMaxBytes {
		return data, format, true
	}
	cfg, _, err := image.DecodeConfig(bytes.NewReader(data))
	if err != nil || cfg.Width <= 0 || cfg.Height <= 0 || int64(cfg.Width)*int64(cfg.Height) > pasteMaxPixels {
		return nil, "", false
	}
	src, _, err := image.Decode(bytes.NewReader(data))
	if err != nil {
		return nil, "", false
	}
	for _, side := range pasteSides {
		small := shrinkOnWhite(src, side)
		for _, q := range pasteQualities {
			var b bytes.Buffer
			if err := jpeg.Encode(&b, small, &jpeg.Options{Quality: q}); err == nil && b.Len() <= PasteImageMaxBytes {
				return b.Bytes(), illustration.FormatJPEG, true
			}
		}
	}
	return nil, "", false
}

// shrinkOnWhite 把 src 按面积平均缩到最长边不超过 side（不放大），并在白底上合成（去掉透明）。耗时与源像素数成正比。
func shrinkOnWhite(src image.Image, side int) *image.RGBA {
	b := src.Bounds()
	sw, sh := b.Dx(), b.Dy()
	dw, dh := sw, sh
	if sw > side || sh > side {
		if sw >= sh {
			dw, dh = side, max(1, sh*side/sw)
		} else {
			dw, dh = max(1, sw*side/sh), side
		}
	}
	// 按目标像素累加源像素的预乘分量（16 位），每个源像素恰好落在一个目标像素中。
	sum := make([]uint64, dw*dh*4)
	cnt := make([]uint32, dw*dh)
	for y := 0; y < sh; y++ {
		ty := y * dh / sh
		row := ty * dw
		for x := 0; x < sw; x++ {
			r, g, bl, a := src.At(b.Min.X+x, b.Min.Y+y).RGBA()
			i := row + x*dw/sw
			// 白底合成：c + (1-α)·白，分量已预乘。
			sum[i*4] += uint64(r + 0xffff - a)
			sum[i*4+1] += uint64(g + 0xffff - a)
			sum[i*4+2] += uint64(bl + 0xffff - a)
			cnt[i]++
		}
	}
	dst := image.NewRGBA(image.Rect(0, 0, dw, dh))
	for i, n := range cnt {
		if n == 0 {
			n = 1
		}
		dst.Pix[i*4] = uint8(sum[i*4] / uint64(n) >> 8)
		dst.Pix[i*4+1] = uint8(sum[i*4+1] / uint64(n) >> 8)
		dst.Pix[i*4+2] = uint8(sum[i*4+2] / uint64(n) >> 8)
		dst.Pix[i*4+3] = 0xff
	}
	return dst
}
