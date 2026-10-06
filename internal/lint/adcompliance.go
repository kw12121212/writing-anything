package lint

import (
	"strings"

	"github.com/kw12121212/writing-anything/internal/resolve"
)

// 广告合规的规则类型（ad-compliance）：检查广告法限制的极限词（Superlative Claims）。只是辅助检查，不构成法律意见。

// adBasis 是内置基础极限词表的依据。
const adBasis = "《中华人民共和国广告法》第九条第（三）项"

// adDisclaimer 写在每项发现项的 message 末尾。
const adDisclaimer = "辅助检查，不构成法律意见"

// adTerms 是内置基础极限词表。只收短语、不收单字「最」或单独的「第一」，避免命中「最近」「第一步」等正常写法；
// 任何一个词都不是另一个词的子串，同一处只报告一次。
var adTerms = []string{
	"最佳", "最好", "最优", "最强", "最高级", "最先进", "最低价", "最便宜", "最受欢迎",
	"国家级", "世界级", "全球级", "顶级", "顶尖", "极致", "极品",
	"独一无二", "史无前例", "绝无仅有", "万能", "首选", "唯一", "遥遥领先", "全球领先",
	"销量第一", "排名第一", "全国第一", "全网第一", "行业第一", "世界第一", "第一品牌",
}

func checkAdCompliance(t *text, p params, _ *resolve.Resolved, _ Input) ([]Finding, bool) {
	var terms, basis []string
	seen := map[string]bool{}
	add := func(term, b string) {
		if !seen[term] {
			seen[term] = true
			terms = append(terms, term)
			basis = append(basis, b)
		}
	}
	if p.bool("builtin", true) {
		for _, term := range adTerms {
			add(term, adBasis+"（内置基础词表）")
		}
	}
	custom := "规则参数 terms（未写明依据）"
	if b, ok := p["basis"].(string); ok {
		custom = strings.TrimSpace(b)
	}
	for _, term := range p.strings("terms") {
		add(term, custom)
	}
	var out []Finding
	hits, truncated := newMatcher(terms).find(t)
	for _, h := range hits {
		term := terms[h.term]
		out = append(out, Finding{Line: h.line, Column: h.column, Text: term,
			Message: "极限词（Superlative Claims）：「" + term + "」属于绝对化用语，依据：" + basis[h.term] + "；" + adDisclaimer})
	}
	return out, truncated
}
