package lint

import (
	"slices"
	"strings"
	"testing"
)

func TestAdCompliance(t *testing.T) {
	r := rules(map[string]string{"ads": "ad-compliance"}, nil)
	res, ps := run(t, "这是全网最好的咖啡机，国家级工艺。\n\n最近第一步先看说明。`最佳`", r)
	if got := summary(res.Findings); ps != nil || !slices.Equal(got, []string{"ads|1|5|最好|", "ads|1|12|国家级|"}) {
		t.Fatalf("builtin: %v %+v", got, ps)
	}
	if m := res.Findings[0].Message; !strings.Contains(m, "《中华人民共和国广告法》第九条第（三）项") || !strings.Contains(m, "辅助检查，不构成法律意见") || !strings.Contains(m, "极限词") {
		t.Fatalf("message: %s", m)
	}
	// terms 追加词表并写明依据；与内置词表重复的词只报告一次，按内置词表的依据。
	r = rules(map[string]string{"ads": "ad-compliance"}, map[string]map[string]any{"ads": {"terms": []any{"根治", "最佳"}, "basis": " 《医疗广告管理办法》 "}})
	res, _ = run(t, "三天根治，效果最佳。", r)
	if got := summary(res.Findings); !slices.Equal(got, []string{"ads|1|3|根治|", "ads|1|8|最佳|"}) ||
		!strings.Contains(res.Findings[0].Message, "依据：《医疗广告管理办法》；") || !strings.Contains(res.Findings[1].Message, "广告法") {
		t.Fatalf("terms: %v", res.Findings)
	}
	// builtin 为 false 时只检查 terms；没有 basis 时说明词来自规则参数。
	r = rules(map[string]string{"ads": "ad-compliance"}, map[string]map[string]any{"ads": {"terms": []any{"根治"}, "builtin": false}})
	res, _ = run(t, "三天根治，效果最佳。", r)
	if got := summary(res.Findings); !slices.Equal(got, []string{"ads|1|3|根治|"}) || !strings.Contains(res.Findings[0].Message, "规则参数 terms") {
		t.Fatalf("builtin false: %v", res.Findings)
	}
	// 单处豁免照常生效。
	r = rules(map[string]string{"ads": "ad-compliance"}, nil)
	res, _ = run(t, "本店是国家级非遗老字号。<!-- lint-ignore ads: 已取得国家级非遗认定 -->", r)
	if len(res.Findings) != 0 || len(res.Waived) != 1 || res.Waived[0].Reason != "已取得国家级非遗认定" {
		t.Fatalf("waiver: %+v", res)
	}
}

// 内置词表中任何一个词都不是另一个词的子串，同一处不会报告两次。
func TestAdTermsDisjoint(t *testing.T) {
	for i, a := range adTerms {
		for j, b := range adTerms {
			if i != j && strings.Contains(a, b) {
				t.Errorf("%q contains %q", a, b)
			}
		}
	}
}

func TestAdComplianceParams(t *testing.T) {
	for _, tt := range []struct {
		params map[string]any
		field  string
	}{
		{map[string]any{"basis": 3.0}, "rules.ads.params.basis"},
		{map[string]any{"basis": "  "}, "rules.ads.params.basis"},
		{map[string]any{"builtin": "no"}, "rules.ads.params.builtin"},
		{map[string]any{"terms": "最"}, "rules.ads.params.terms"},
		{map[string]any{"builtin": false}, "rules.ads.params.terms"},
		{map[string]any{"builtin": false, "terms": []any{}, "basis": "无词"}, "rules.ads.params.terms"},
	} {
		r := rules(map[string]string{"ads": "ad-compliance"}, map[string]map[string]any{"ads": tt.params})
		res, ps := run(t, "最佳", r)
		if len(ps) != 1 || ps[0].Code != "rule.invalid-params" || ps[0].Field != tt.field || len(res.Findings) != 0 {
			t.Errorf("%v: %+v %v", tt.params, ps, res.Findings)
		}
	}
	if v, ok := convert(kindString, "依据"); !ok || v != "依据" {
		t.Fatal("string")
	}
}
