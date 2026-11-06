package cli

import (
	"context"
	"errors"
	"os"
	"os/exec"
	"path/filepath"
	"time"
)

// pdfRenderer 用本机的无头浏览器把页面打印成 PDF（external-processes 允许的固定外部程序）。
// 参数列表固定：只有固定的开关、本次生成的 file:// 地址与输出路径，不接受正文里的任何地址，也不加载网络资源。
func pdfRenderer(html []byte) ([]byte, error) {
	exe := chromePath()
	if exe == "" {
		return nil, errors.New("没有找到可用的浏览器：请设置 CHROME_PATH，或安装 Chrome/Chromium 后再导出 PDF")
	}
	dir, err := os.MkdirTemp("", "wa-pdf-")
	if err != nil {
		return nil, err
	}
	defer os.RemoveAll(dir)
	in, out := filepath.Join(dir, "doc.html"), filepath.Join(dir, "doc.pdf")
	if err := os.WriteFile(in, html, 0o600); err != nil {
		return nil, err
	}
	ctx, cancel := context.WithTimeout(context.Background(), 120*time.Second)
	defer cancel()
	cmd := exec.CommandContext(ctx, exe,
		"--headless", "--disable-gpu", "--no-sandbox", "--no-pdf-header-footer",
		"--virtual-time-budget=10000", "--print-to-pdf="+out, "file://"+in)
	cmd.WaitDelay = 5 * time.Second
	cmd.Stdin, cmd.Stdout, cmd.Stderr = nil, nil, nil
	if err := cmd.Run(); err != nil {
		return nil, err
	}
	return os.ReadFile(out)
}

// chromePath 依次取 CHROME_PATH 与 PATH 上常见的浏览器。
func chromePath() string {
	if p := os.Getenv("CHROME_PATH"); p != "" {
		return p
	}
	for _, name := range []string{"google-chrome", "google-chrome-stable", "chromium", "chromium-browser", "microsoft-edge"} {
		if p, err := exec.LookPath(name); err == nil {
			return p
		}
	}
	return ""
}
