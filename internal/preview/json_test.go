package preview

import (
	"encoding/json"
	"strings"
	"testing"
)

// 空阅读页的数组都编码为空数组，段落出处无法推导时为 null，并通过阅读页数据的 JSON Schema（preview-page-json）。
func TestJSONEmptyAndNullOrigin(t *testing.T) {
	p := &Page{Title: "my-post", Units: []Unit{
		{Ref: "my-post", Stage: "draft", HasDraft: true, Paragraphs: []Paragraph{{Index: 1, Line: 1, Text: "<b>原文</b>"}}},
		{Ref: "my-post/intro", Stage: "brief"},
	}}
	data, err := JSON(p)
	if err != nil {
		t.Fatal(err)
	}
	if err := ValidateJSON(data); err != nil {
		t.Fatalf("schema: %v\n%s", err, data)
	}
	got := string(data)
	for _, want := range []string{`"origin":null`, `"text":"<b>原文</b>"`, `"confirms":[]`, `"reviews":[]`, `"failures":[]`,
		`"notes":[]`, `"global":[]`, `"paragraphs":[]`, `"findings":[]`, `"strict":false`} {
		if !strings.Contains(got, want) {
			t.Errorf("missing %s in %s", want, got)
		}
	}
	var back struct {
		Units []struct {
			Paragraphs []struct{ Text string } `json:"paragraphs"`
		} `json:"units"`
	}
	if json.Unmarshal(data, &back) != nil || back.Units[0].Paragraphs[0].Text != "<b>原文</b>" {
		t.Fatalf("text not kept verbatim: %s", data)
	}
}

// 全部区块都有内容时字段齐全；非 strict 的复核项不输出。
func TestJSONFullPage(t *testing.T) {
	f := Finding{Kind: "lint", Label: "warning length", Severity: "warning", Message: "太长", Suggestion: "拆开"}
	p := &Page{
		Title: "my-post",
		Units: []Unit{{Ref: "my-post", Stage: "review", HasDraft: true, Notes: []string{"说明"}, Global: []Finding{f},
			Paragraphs: []Paragraph{{Index: 1, Line: 3, Text: "正文", Origin: "agent-draft-human-edited", Findings: []Finding{{Kind: "review", Label: "审稿 r1（open）", Message: "空"}}}}}},
		Confirms: []Confirm{{Kind: "decision", ID: "dec-1", Label: "决策", Options: []Option{{ID: "a", Text: "甲"}}, Recommend: "a", Units: []string{"my-post"}},
			{Kind: "state", ID: "state-1", CanReject: true}, {Kind: "acceptance", Unit: "my-post"}},
		Strict:   true,
		Reviews:  []Review{{Key: "acceptance:my-post", Label: "验收"}},
		Failures: []AuditFailure{{At: "2026-10-22T06:47:38Z", Command: "review record", Exit: 1}},
	}
	data, err := JSON(p)
	if err != nil {
		t.Fatal(err)
	}
	if err := ValidateJSON(data); err != nil {
		t.Fatalf("schema: %v\n%s", err, data)
	}
	got := string(data)
	for _, want := range []string{`"origin":"agent-draft-human-edited"`, `"recommend":"a"`, `"canReject":true`, `"key":"acceptance:my-post"`,
		`"command":"review record","exit":1`, `"strict":true`} {
		if !strings.Contains(got, want) {
			t.Errorf("missing %s in %s", want, got)
		}
	}
	// Units 只供 wa-cli next 使用，不进入阅读页数据。
	if strings.Contains(got, `"units":["my-post"]`) {
		t.Errorf("internal Units leaked: %s", got)
	}
	p.Strict = false
	data, _ = JSON(p)
	if !strings.Contains(string(data), `"reviews":[]`) {
		t.Errorf("non-strict page lists reviews: %s", data)
	}
}

func TestValidateJSONRejects(t *testing.T) {
	for _, bad := range []string{`{"title":"x"}`, `{"title":"x","units":null,"confirms":[],"strict":false,"reviews":[],"failures":[]}`, `not json`} {
		if ValidateJSON([]byte(bad)) == nil {
			t.Errorf("accepted %s", bad)
		}
	}
	if s := PageSchema(); !json.Valid(s) || !strings.Contains(string(s), "page.json") {
		t.Errorf("schema bytes: %s", s)
	}
}
