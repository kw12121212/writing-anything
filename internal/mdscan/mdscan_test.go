package mdscan

import (
	"slices"
	"strings"
	"testing"
)

func TestFenceLines(t *testing.T) {
	lines := strings.SplitAfter("a\n````md\n```go\nx\n```\n````\n  ~~~\ny\n~~~~ \nb\n    ```\n```\nunclosed\n", "\n")
	got := FenceLines(lines)
	want := []bool{false, true, true, true, true, true, true, true, true, false, false, true, true, true}
	if !slices.Equal(got, want) {
		t.Fatalf("FenceLines = %v, want %v", got, want)
	}
}

func TestCodeSpans(t *testing.T) {
	line := "a `b` ``c`d`` ```x` e"
	spans := CodeSpans(line)
	if len(spans) != 2 || line[spans[0][0]:spans[0][1]] != "`b`" || line[spans[1][0]:spans[1][1]] != "``c`d``" {
		t.Fatalf("CodeSpans = %v", spans)
	}
	if !InSpans(spans, spans[1][0]+2) || InSpans(spans, 0) || InSpans(spans, spans[1][1]) || InSpans(nil, 3) {
		t.Fatal("InSpans")
	}
}

func TestImages(t *testing.T) {
	content := "开头 ![店内](illustrations/fig-1.jpg \"周六\") 与 ![图标](<https://example.com/a b.png>)\n" +
		"```\n![代码中](illustrations/x.png)\n```\n" +
		"`![行内代码](illustrations/y.png)` <!-- ![注释中](illustrations/z.png) --> ![末尾](illustrations/fig-2.png)\n" +
		"![未闭合](illustrations/w.png\n"
	got := Images(content)
	if len(got) != 3 {
		t.Fatalf("Images = %+v, want 3", got)
	}
	if g := got[0]; g.Line != 1 || g.Alt != "店内" || g.Src != "illustrations/fig-1.jpg" || g.Title != "周六" || content[g.Start:g.End] != "![店内](illustrations/fig-1.jpg \"周六\")" {
		t.Errorf("first = %+v", g)
	}
	if g := got[1]; g.Src != "https://example.com/a b.png" || g.Title != "" {
		t.Errorf("second = %+v", g)
	}
	if g := got[2]; g.Line != 5 || g.Src != "illustrations/fig-2.png" {
		t.Errorf("third = %+v", g)
	}
}

func TestImagesUnclosedCommentAndLongInput(t *testing.T) {
	content := "<!-- 没有结束 ![图](illustrations/a.png)\n" + strings.Repeat("![x](", 20000)
	if got := Images(content); len(got) != 1 || got[0].Src != "illustrations/a.png" {
		t.Fatalf("Images = %+v", got)
	}
}
