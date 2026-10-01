package catalog

import (
	"encoding/json"
	"os"
	"testing"
)

// pack.schema.json 的样式主题取值格式与 SafeCSS 使用同一组模式（workspace-artifacts/generated-html：Schema 一次、渲染时再一次）。
func TestThemePatternsMatchSchema(t *testing.T) {
	data, err := os.ReadFile("../schema/schemas/pack.schema.json")
	if err != nil {
		t.Fatal(err)
	}
	var doc struct {
		Defs struct {
			CSS struct {
				Pattern string `json:"pattern"`
				Not     struct {
					Pattern string `json:"pattern"`
				} `json:"not"`
			} `json:"css"`
		} `json:"$defs"`
		Properties map[string]struct {
			Properties map[string]any `json:"properties"`
		} `json:"properties"`
	}
	if err := json.Unmarshal(data, &doc); err != nil {
		t.Fatal(err)
	}
	if doc.Defs.CSS.Pattern != CSSPattern || doc.Defs.CSS.Not.Pattern != UnsafeCSSPattern {
		t.Fatalf("schema patterns %q / %q differ from catalog", doc.Defs.CSS.Pattern, doc.Defs.CSS.Not.Pattern)
	}
	keys := doc.Properties["html_theme"].Properties
	if len(keys) != len(HTMLThemeKeys) {
		t.Fatalf("schema keys %v, catalog keys %v", keys, HTMLThemeKeys)
	}
	for _, k := range HTMLThemeKeys {
		if _, ok := keys[k]; !ok || !SafeCSS(DefaultHTMLTheme[k]) {
			t.Errorf("key %s missing from schema or default unsafe", k)
		}
	}
}
