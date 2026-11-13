package lint

import (
	"os"
	"path/filepath"
	"slices"
	"strings"
	"testing"

	"github.com/kw12121212/writing-anything/internal/resolve"
	"github.com/kw12121212/writing-anything/internal/workspace"
)

// builtinRules 返回内置文体 genre 在 packs 为空的工作区中合并后的规则（genres-and-packs/article-tone-rules）。
func builtinRules(t *testing.T, genre string) *resolve.Resolved {
	t.Helper()
	root := t.TempDir()
	if _, ps := workspace.Init(root, nil); len(ps) > 0 {
		t.Fatal(ps)
	}
	cfg := "schema_version: 1\nlanguage: zh-CN\npacks: []\ngate_level: standard\ndefault_genre: " + genre + "\n"
	if err := os.WriteFile(filepath.Join(root, ".writing-anything", "config.yaml"), []byte(cfg), 0o644); err != nil {
		t.Fatal(err)
	}
	r, ps := resolve.ForGenre(root, genre)
	if r == nil || len(ps) > 0 {
		t.Fatalf("ForGenre %s: %+v", genre, ps)
	}
	return r
}

// toneOutcome 返回 tone-patterns 命中的模式 id（按 message 反查）与 tone-density 超限的指标名。
func toneOutcome(t *testing.T, r *resolve.Resolved, content string) (patterns, density []string) {
	t.Helper()
	res, ps := Run(content, r, "draft.md", Input{})
	for _, p := range ps {
		if p.Code == "rule.invalid-params" {
			t.Fatalf("invalid params: %+v", p)
		}
	}
	pats, _ := parsePatterns(r.Rules["tone-patterns"].Params["patterns"].Value)
	for _, f := range res.Findings {
		switch f.Rule {
		case "tone-patterns":
			for _, p := range pats {
				if p.message == f.Message && !slices.Contains(patterns, p.id) {
					patterns = append(patterns, p.id)
				}
			}
		case "tone-density":
			for _, name := range []string{"连接词密度", "四字片段密度", "句长标准差", "列表行占比", "节长变异系数"} {
				if strings.Contains(f.Message, name) {
					density = append(density, name)
				}
			}
		}
	}
	return patterns, density
}

// TestToneFixtures 按 testdata/tone/README.md 逐个文件核对固定样本（genres-and-packs/article-tone-rules 场景 fixed-samples）：
// plain 目录没有发现项，cliche 目录至少有表中列出的发现项。样本与阈值不得为了让本测试通过而修改。
func TestToneFixtures(t *testing.T) {
	want := map[string][]string{
		"tb-01-not-but":           {"not-but"},
		"tb-02-first-second":      {"first-second"},
		"tb-03-grand-opening":     {"grand-opening", "lets"},
		"tb-04-closing-in-short":  {"closing-in-short"},
		"tb-05-formal-words":      {"word-empower", "word-lever", "word-closed-loop"},
		"tb-06-connectives":       {"连接词密度"},
		"tb-07-four-char":         {"四字片段密度"},
		"tb-08-uniform-sections":  {"句长标准差", "节长变异系数"},
		"wx-01-not-only":          {"not-only"},
		"wx-02-closing-shows":     {"closing-shows"},
		"wx-03-closing-visible":   {"closing-visible"},
		"wx-04-formal-words":      {"word-crucial", "word-all-round", "word-indispensable"},
		"wx-05-formal-words-2":    {"word-seamless", "word-boost", "word-deep-dive"},
		"wx-06-uniform-sentences": {"句长标准差"},
		"wx-07-list-heavy":        {"列表行占比", "四字片段密度"},
	}
	genres := map[string]*resolve.Resolved{"tb": builtinRules(t, "tech-blog"), "wx": builtinRules(t, "wechat-marketing")}
	for _, dir := range []string{"plain", "cliche"} {
		files, err := filepath.Glob(filepath.Join("testdata", "tone", dir, "*.md"))
		if err != nil || len(files) == 0 {
			t.Fatalf("%s: %v %v", dir, files, err)
		}
		for _, f := range files {
			name := strings.TrimSuffix(filepath.Base(f), ".md")
			r := genres[name[:2]]
			if r == nil {
				t.Fatalf("%s: 文件名应以 tb- 或 wx- 开头", f)
			}
			content, err := os.ReadFile(f)
			if err != nil {
				t.Fatal(err)
			}
			patterns, density := toneOutcome(t, r, string(content))
			got := append(patterns, density...)
			if dir == "plain" {
				if len(got) > 0 {
					t.Errorf("%s: 对照稿不应有发现项，得到 %v", name, got)
				}
				continue
			}
			w, ok := want[name]
			if !ok {
				t.Errorf("%s: README 中没有这个套路稿", name)
				continue
			}
			for _, x := range w {
				if !slices.Contains(got, x) {
					t.Errorf("%s: 缺少 %s，得到 %v", name, x, got)
				}
			}
		}
	}
	if len(want) != 15 {
		t.Fatalf("want = %d", len(want))
	}
}
