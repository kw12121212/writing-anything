package ctxpack

import (
	"crypto/sha256"
	"encoding/hex"
	"io"
	"strings"
	"unicode/utf8"

	"github.com/kw12121212/writing-anything/internal/source"
	"github.com/kw12121212/writing-anything/internal/workspace"
)

// 上下文包的错误码（Error Code）。已发布的错误码不得改名或复用。
const (
	// CodeSourceSkipped 表示引用的素材无法放入上下文包（存档文件缺失、途经符号链接或不是 UTF-8 文本），以 warning 报告。
	CodeSourceSkipped = "context.source-skipped"
)

// chunkSize 是流式读取素材原文时每次读取的字节数。
const chunkSize = 64 << 10

// text 是流式读取的素材原文：Prefix 为前至多 keep 个字符（CRLF 换成 LF、去掉末尾换行前的内容），Chars 为全文字符数。
type text struct {
	Prefix string
	Chars  int
	Whole  bool // Prefix 是否为全文
}

// readSource 读取已登记素材 id 的原文，只在内存中保留前 keep 个字符，同时计算全文的字符数与 SHA-256。
// 原文与登记的哈希不一致时返回 source.modified，无法作为文本读取时返回 context.source-skipped，两者都是 warning。
func readSource(root, id string, keep int) (Source, text, *workspace.Problem) {
	if !source.Registered(root, id) {
		// 未登记或无法加载的素材已由写作简报、大纲的引用检查或整体校验报告，这里不重复报告。
		return Source{}, text{}, nil
	}
	s, _ := source.Load(root, id)
	rel := s.FilePath()
	f, ok, _ := workspace.OpenRegular(root, rel)
	if !ok {
		return Source{}, text{}, skipped(rel, "素材（Source）"+id+" 的存档文件缺失、途经符号链接或不是普通文件，未放入上下文包")
	}
	defer f.Close()
	t, sum, valid, err := scan(f, keep)
	switch {
	case err != nil:
		return Source{}, text{}, skipped(rel, "读取素材（Source）"+id+" 失败，未放入上下文包："+err.Error())
	case sum != s.SHA256:
		p := workspace.Problem{Code: source.CodeModified, Severity: workspace.SeverityWarning, Path: rel,
			Message: "素材（Source）" + id + " 的原文与登记时的 SHA-256 不一致，可能已被改写；未放入上下文包"}
		return Source{}, text{}, &p
	case !valid:
		return Source{}, text{}, skipped(rel, "素材（Source）"+id+" 不是 UTF-8 文本，未放入上下文包")
	}
	return Source{ID: s.ID, Type: s.Type, Title: s.Title}, t, nil
}

func skipped(path, msg string) *workspace.Problem {
	return &workspace.Problem{Code: CodeSourceSkipped, Severity: workspace.SeverityWarning, Path: path, Message: msg}
}

// scan 流式读取 r：返回前 keep 个字符（CRLF 换成 LF，去掉末尾的换行）、全文的字符数与 SHA-256，以及全文是否为 UTF-8。
// 内存只随 keep 增长，与文件大小无关（workspace-artifacts/input-limits）。
func scan(r io.Reader, keep int) (text, string, bool, error) {
	h := sha256.New()
	var prefix strings.Builder
	var carry []byte
	kept, total, trailing, valid, pendingCR := 0, 0, 0, true, false
	emit := func(c rune) {
		total++
		if c == '\n' {
			trailing++
		} else {
			trailing = 0
		}
		if kept < keep {
			prefix.WriteRune(c)
			kept++
		}
	}
	buf := make([]byte, chunkSize)
	for {
		n, err := r.Read(buf)
		h.Write(buf[:n])
		data := append(carry, buf[:n]...)
		// 块末尾不完整的字符留到下一块。
		cut := len(data)
		if err == nil {
			for i := len(data) - 1; i >= 0 && i >= len(data)-utf8.UTFMax; i-- {
				if utf8.RuneStart(data[i]) {
					if !utf8.FullRune(data[i:]) {
						cut = i
					}
					break
				}
			}
		}
		carry = append([]byte(nil), data[cut:]...)
		for chunk := data[:cut]; len(chunk) > 0; {
			c, size := utf8.DecodeRune(chunk)
			chunk = chunk[size:]
			if c == utf8.RuneError && size <= 1 {
				valid = false
			}
			if pendingCR {
				pendingCR = false
				if c == '\n' {
					emit('\n') // CRLF 换成 LF
					continue
				}
				emit('\r')
			}
			if c == '\r' {
				pendingCR = true
				continue
			}
			emit(c)
		}
		if err == io.EOF {
			break
		}
		if err != nil {
			return text{}, "", false, err
		}
	}
	if pendingCR {
		emit('\r')
	}
	// 末尾的换行不计入字符数，也不放入原文。
	p := prefix.String()
	whole := kept == total
	if whole {
		p = strings.TrimRight(p, "\n")
	}
	return text{Prefix: p, Chars: total - trailing, Whole: whole}, hex.EncodeToString(h.Sum(nil)), valid, nil
}
