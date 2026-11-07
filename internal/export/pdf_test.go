package export

import (
	"bytes"
	"errors"
	"os"
	"path/filepath"
	"testing"

	"github.com/kw12121212/writing-anything/internal/illustration"
)

// PDF：渲染交给注入的函数，导出按输出路径规则落盘；文档内含标题、正文与内嵌配图。
func TestPDF(t *testing.T) {
	body := "# 标题\n\n开头。\n\n![店门口](illustrations/shop.png)\n"
	root, w := illustratedRoot(t, body,
		illustration.AddInput{ID: "shop", Data: testPNG(t, 1), Alt: "店门口"},
		illustration.AddInput{ID: "cover", Role: illustration.RoleCover, Data: testPNG(t, 2), Alt: "封面"})
	var doc []byte
	res, changed, ps := PDF(root, w, Options{Level: "fast"}, map[string]string{}, func(html []byte) ([]byte, error) {
		doc = html
		return []byte("%PDF-1.4 fake"), nil
	})
	if res == nil || len(ps) > 0 {
		t.Fatalf("PDF: %v", ps)
	}
	if res.Path != ".writing-anything/exports/post.pdf" {
		t.Errorf("path = %q", res.Path)
	}
	got, err := os.ReadFile(filepath.Join(root, res.Path))
	if err != nil || string(got) != "%PDF-1.4 fake" {
		t.Errorf("written = %q err = %v", got, err)
	}
	if !bytes.Contains(doc, []byte("<title>标题</title>")) || !bytes.Contains(doc, []byte("data:image/png;base64,")) {
		t.Errorf("document: %.200s", doc)
	}
	if res.Chars != len("%PDF-1.4 fake") {
		t.Errorf("chars = %d，应为文件字节数", res.Chars)
	}
	if len(changed) == 0 {
		t.Errorf("changedPaths empty")
	}
}

// PDF：浏览器打印失败时报 export.pdf-render，且不写文件。
func TestPDFRenderError(t *testing.T) {
	root, w := illustratedRoot(t, "# 标题\n\n正文。\n")
	res, _, ps := PDF(root, w, Options{Level: "fast"}, map[string]string{}, func([]byte) ([]byte, error) {
		return nil, errors.New("no browser")
	})
	if res != nil || len(ps) != 1 || ps[0].Code != CodePDFRender || ps[0].Severity != "error" {
		t.Fatalf("res = %+v ps = %+v", res, ps)
	}
	if _, err := os.Stat(filepath.Join(root, ".writing-anything/exports/post.pdf")); !os.IsNotExist(err) {
		t.Errorf("不应写出文件")
	}
}

// PDF：输出路径越界时报 export.invalid-output，且不写文件。
func TestPDFInvalidOutput(t *testing.T) {
	root, w := illustratedRoot(t, "# 标题\n\n正文。\n")
	if _, _, ps := PDF(root, w, Options{Level: "fast", Output: "../x.pdf"}, map[string]string{}, func([]byte) ([]byte, error) {
		return []byte("%PDF-1.4 fake"), nil
	}); len(ps) != 1 || ps[0].Code != CodeInvalidOutput {
		t.Fatalf("invalid output: %+v", ps)
	}
}
