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
