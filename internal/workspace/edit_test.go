package workspace

import (
	"strings"
	"testing"

	"go.yaml.in/yaml/v3"
)

func mapping(t *testing.T, src string) (*yaml.Node, *yaml.Node) {
	t.Helper()
	var doc yaml.Node
	if err := yaml.Unmarshal([]byte(src), &doc); err != nil {
		t.Fatal(err)
	}
	return &doc, doc.Content[0]
}

func encoded(t *testing.T, doc *yaml.Node) string {
	t.Helper()
	data, err := Encode(doc)
	if err != nil {
		t.Fatal(err)
	}
	return string(data)
}

func TestSetKey(t *testing.T) {
	order := []string{"a", "b", "c", "d"}
	tests := []struct {
		name, src, key string
		n              *yaml.Node
		want           string
	}{
		{"insert in order", "a: 1\nd: 4\n", "b", Str("2"), "a: 1\nb: \"2\"\nd: 4\n"},
		{"insert first", "d: 4\n", "a", Str("x"), "a: x\nd: 4\n"},
		{"unknown key appended at front", "a: 1\n", "z", Str("x"), "z: x\na: 1\n"},
		{"replace keeps comment", "a: old # 注释\n", "a", Str("new"), "a: new # 注释\n"},
		{"same scalar keeps style", "a: 'x'\n", "a", Str("x"), "a: 'x'\n"},
		{"sequence keeps flow style", "a: [x]\n", "a", Seq([]string{"y", "z"}, false), "a: [y, z]\n"},
		{"sequence keeps block style", "a:\n  - x\n", "a", Seq([]string{"y"}, true), "a:\n  - y\n"},
		{"mapping updated per key", "a:\n  min: 1 # 下限\n  max: 2\n", "a", &yaml.Node{Kind: yaml.MappingNode, Content: []*yaml.Node{Str("min"), Int(5), Str("max"), Int(9)}}, "a:\n  min: 5 # 下限\n  max: 9\n"},
		{"delete", "a: 1\nb: 2\n", "a", nil, "b: 2\n"},
		{"delete missing", "a: 1\n", "b", nil, "a: 1\n"},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			doc, m := mapping(t, tt.src)
			if f, ok := SetKey(m, order, tt.key, tt.n); !ok {
				t.Fatalf("SetKey refused at %q", f)
			}
			if got := encoded(t, doc); got != tt.want {
				t.Fatalf("got\n%s\nwant\n%s", got, tt.want)
			}
		})
	}
	for _, tt := range []struct{ name, src, key, field string }{
		{"anchored value", "a: &x 1\nb: *x\n", "a", "a"},
		{"alias value", "a: &x 1\nb: *x\n", "b", "b"},
		{"anchor inside subtree", "a: [&x 1]\nb: *x\n", "a", "a"},
		{"anchor inside mapping key", "a:\n  min: &x 1\nb: *x\n", "a", "a.min"},
	} {
		t.Run(tt.name, func(t *testing.T) {
			doc, m := mapping(t, tt.src)
			before := encoded(t, doc)
			n := Str("new")
			if strings.HasSuffix(tt.field, "min") {
				n = &yaml.Node{Kind: yaml.MappingNode, Content: []*yaml.Node{Str("min"), Int(5)}}
			}
			if f, ok := SetKey(m, order, tt.key, n); ok || f != tt.field {
				t.Fatalf("SetKey = %q, %v; want refusal at %q", f, ok, tt.field)
			}
			if encoded(t, doc) != before {
				t.Fatal("document changed")
			}
		})
	}
}

func TestEditHelpers(t *testing.T) {
	if Seq(nil, true) != nil {
		t.Fatal("empty Seq must be nil")
	}
	_, m := mapping(t, "a: &x 1\nb: 2\nitems:\n  - {id: p}\n  - x\n  - {id: q}\n<<: {c: 3}\n")
	if v, ok := Lookup(m, "a"); v == nil || ok {
		t.Fatal("Lookup anchored")
	}
	if v, ok := Lookup(m, "b"); v == nil || !ok {
		t.Fatal("Lookup plain")
	}
	if v, ok := Lookup(m, "nope"); v != nil || !ok {
		t.Fatal("Lookup missing")
	}
	items, _ := Lookup(m, "items")
	if IndexByID(items, "q") != 2 || IndexByID(items, "z") != -1 {
		t.Fatal("IndexByID")
	}
	if !HasMerge(m) || !HasAnchor(m) || HasAnchor(items) || !Plain(items) {
		t.Fatal("anchor helpers")
	}
}

func TestSetContextBudget(t *testing.T) {
	root := t.TempDir()
	if _, ps := Init(root, nil); len(ps) > 0 {
		t.Fatal(ps)
	}
	cfg, changed, ps := SetConfig(root, "context_budget", "5000")
	if !changed || ps != nil || cfg.ContextBudget == nil || *cfg.ContextBudget != 5000 {
		t.Fatalf("set: %+v %v %+v", cfg, changed, ps)
	}
	if cfg, changed, ps := SetConfig(root, "context_budget", "abc"); changed || len(ps) != 1 || ps[0].Field != "context_budget" || *cfg.ContextBudget != 5000 {
		t.Fatalf("invalid: %+v %v %+v", cfg, changed, ps)
	}
	if cfg, changed, ps := SetConfig(root, "context_budget", ""); !changed || ps != nil || cfg.ContextBudget != nil {
		t.Fatalf("clear: %+v %v %+v", cfg, changed, ps)
	}
	for v, want := range map[any]int{7: 7, int64(8): 8, 9.0: 9} {
		if c := summarize(map[string]any{"context_budget": v}); c.ContextBudget == nil || *c.ContextBudget != want {
			t.Errorf("summarize(%v) = %+v", v, c.ContextBudget)
		}
	}
	if c := summarize(map[string]any{"context_budget": 1.5}); c.ContextBudget != nil {
		t.Errorf("summarize(1.5) = %v", *c.ContextBudget)
	}
}
