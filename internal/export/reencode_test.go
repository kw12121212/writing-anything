package export

import (
	"bytes"
	"encoding/binary"
	"hash/crc32"
	"image"
	"image/color"
	"image/jpeg"
	"image/png"
	"math/rand"
	"testing"

	"github.com/kw12121212/writing-anything/internal/illustration"
)

// encodePNG 编码 img，末尾补到至少 size 字节（PNG 解码器忽略 IEND 之后的字节）。
func encodePNG(t *testing.T, img image.Image, size int) []byte {
	t.Helper()
	var b bytes.Buffer
	if err := png.Encode(&b, img); err != nil {
		t.Fatal(err)
	}
	if b.Len() < size {
		b.Write(make([]byte, size-b.Len()))
	}
	return b.Bytes()
}

// 超过 1 MiB 的 PNG 缩到最长边 1600、白底合成后编码为不超过 1 MiB 的 JPEG；小图原样返回（paste-reencode-large）。
func TestPasteImageReencode(t *testing.T) {
	src := image.NewNRGBA(image.Rect(0, 0, 2000, 1500))
	for y := 0; y < 1500; y++ {
		for x := 0; x < 2000; x++ {
			a := uint8(255)
			if x < 400 {
				a = 0 // 左侧完全透明，应当变成白色
			}
			src.SetNRGBA(x, y, color.NRGBA{uint8(x * 255 / 2000), uint8(y * 255 / 1500), 40, a})
		}
	}
	data := encodePNG(t, src, 3<<20)
	out, format, ok := pasteImage(data)
	if !ok || format != illustration.FormatJPEG || len(out) > PasteImageMaxBytes {
		t.Fatalf("reencode: ok=%v format=%s size=%d", ok, format, len(out))
	}
	img, err := jpeg.Decode(bytes.NewReader(out))
	if err != nil {
		t.Fatal(err)
	}
	if b := img.Bounds(); b.Dx() != 1600 || b.Dy() != 1200 {
		t.Errorf("size = %v", b)
	}
	if r, g, bl, _ := img.At(50, 600).RGBA(); r>>8 < 245 || g>>8 < 245 || bl>>8 < 245 {
		t.Errorf("transparent area not white: %d %d %d", r>>8, g>>8, bl>>8)
	}
	// 同一输入的结果确定。
	if again, _, _ := pasteImage(data); !bytes.Equal(again, out) {
		t.Error("reencode is not deterministic")
	}
	small := encodePNG(t, image.NewNRGBA(image.Rect(0, 0, 8, 4)), 0)
	if out, format, ok := pasteImage(small); !ok || format != illustration.FormatPNG || !bytes.Equal(out, small) {
		t.Errorf("small image changed: %v %s", ok, format)
	}
	if _, _, ok := pasteImage([]byte("not an image")); ok {
		t.Error("non-image accepted")
	}
}

// 难压缩的噪声图在 1600 像素下超过 1 MiB 时退到更小的最长边，结果仍不超过 1 MiB。
func TestPasteImageNoiseFallsBack(t *testing.T) {
	rng := rand.New(rand.NewSource(1))
	src := image.NewRGBA(image.Rect(0, 0, 2400, 2400))
	rng.Read(src.Pix)
	for i := 3; i < len(src.Pix); i += 4 {
		src.Pix[i] = 0xff
	}
	out, _, ok := pasteImage(encodePNG(t, src, 0))
	if !ok || len(out) > PasteImageMaxBytes {
		t.Fatalf("noise: ok=%v size=%d", ok, len(out))
	}
	cfg, err := jpeg.DecodeConfig(bytes.NewReader(out))
	if err != nil || cfg.Width > 1600 {
		t.Errorf("noise size = %+v %v", cfg, err)
	}
}

// 宽高之积超过 4000 万像素的图片不解码、不内嵌（paste-pixel-limit）：只构造文件头即可，解码前就被拒绝。
func TestPasteImagePixelLimit(t *testing.T) {
	ihdr := make([]byte, 13)
	binary.BigEndian.PutUint32(ihdr[0:], 8000)
	binary.BigEndian.PutUint32(ihdr[4:], 6000)
	ihdr[8], ihdr[9] = 8, 2 // 8 位 RGB
	var b bytes.Buffer
	b.WriteString("\x89PNG\r\n\x1a\n")
	binary.Write(&b, binary.BigEndian, uint32(len(ihdr)))
	b.WriteString("IHDR")
	b.Write(ihdr)
	binary.Write(&b, binary.BigEndian, crc32.ChecksumIEEE(append([]byte("IHDR"), ihdr...)))
	b.Write(make([]byte, 2<<20))
	if format, w, h := illustration.Detect(b.Bytes()); format != illustration.FormatPNG || w != 8000 || h != 6000 {
		t.Fatalf("header not recognised: %s %d %d", format, w, h)
	}
	if _, _, ok := pasteImage(b.Bytes()); ok {
		t.Error("48 MP image was embedded")
	}
}
