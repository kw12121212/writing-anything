package schema

import (
	"fmt"
	"math/big"
	"os"
	"path/filepath"
	"reflect"
	"strings"
	"testing"

	"github.com/santhosh-tekuri/jsonschema/v6"
	"github.com/santhosh-tekuri/jsonschema/v6/kind"
	"go.yaml.in/yaml/v3"
)

func readFixture(t *testing.T, path string) []byte {
	t.Helper()
	data, err := os.ReadFile(path)
	if err != nil {
		t.Fatal(err)
	}
	return data
}

// 每个已登记的产物类型：Schema 能编译，testdata/<类型名>/ 下至少各有一个合法与不合法的夹具，且结果与预期相符。
func TestKindsCompileAndFixtures(t *testing.T) {
	if len(Kinds()) == 0 {
		t.Fatal("no artifact kinds registered")
	}
	for _, k := range Kinds() {
		t.Run(k.Name, func(t *testing.T) {
			if _, err := k.compile(); err != nil {
				t.Fatalf("schema %s does not compile: %v", k.File, err)
			}
			for _, group := range []string{"valid", "invalid"} {
				files, _ := filepath.Glob(filepath.Join("testdata", k.Name, group, "*.yaml"))
				if len(files) == 0 {
					t.Fatalf("no %s fixtures under testdata/%s/%s", group, k.Name, group)
				}
				for _, f := range files {
					ps := k.Validate(readFixture(t, f))
					if group == "valid" && len(ps) > 0 {
						t.Errorf("%s: problems = %+v, want none", f, ps)
					}
					if group == "invalid" && len(ps) == 0 {
						t.Errorf("%s: no problems, want at least one", f)
					}
				}
			}
		})
	}
}

func TestConfigProblems(t *testing.T) {
	tests := []struct {
		file    string
		code    string
		field   string
		line    int
		message string
	}{
		{"parse-error.yaml", "artifact.parse-error", "", 3, "YAML 语法错误：did not find expected ',' or ']'"},
		{"unknown-field.yaml", "artifact.schema-violation", "colour", 5, "未定义的字段 colour"},
		{"bad-gate-level.yaml", "artifact.schema-violation", "gate_level", 4, `取值 "bogus" 不在允许范围内（"strict"、"standard"、"fast"）`},
		{"duplicate-packs.yaml", "artifact.schema-violation", "packs", 3, "第 1 项与第 3 项重复"},
		{"bad-pack-id.yaml", "artifact.schema-violation", "packs[0]", 4, `取值 "Not_A_Id" 格式不正确`},
		{"wrong-type.yaml", "artifact.schema-violation", "language", 2, "类型应为 string，实际为 number"},
		{"missing-required.yaml", "artifact.schema-violation", "gate_level", 1, "缺少必需字段 gate_level"},
		{"duplicate-key.yaml", "artifact.parse-error", "", 4, "字段 language 重复，首次出现在第 2 行"},
		{"two-documents.yaml", "artifact.parse-error", "", 5, "第二个 YAML 文档"},
		{"schema-version.yaml", "artifact.schema-violation", "schema_version", 1, "取值应为 1，实际为 2"},
		{"alias-duplicate.yaml", "artifact.schema-violation", "packs", 4, "第 1 项与第 2 项重复"},
		{"empty.yaml", "artifact.schema-violation", "", 0, "类型应为 object，实际为 null"},
	}
	k := KindFor(".writing-anything/config.yaml")
	for _, tt := range tests {
		t.Run(tt.file, func(t *testing.T) {
			ps := k.Validate(readFixture(t, filepath.Join("testdata", "config", "invalid", tt.file)))
			if len(ps) != 1 {
				t.Fatalf("problems = %+v, want exactly one", ps)
			}
			p := ps[0]
			if p.Code != tt.code || p.Field != tt.field || p.Line != tt.line || !strings.Contains(p.Message, tt.message) {
				t.Fatalf("problem = %+v, want code %s field %q line %d message containing %q", p, tt.code, tt.field, tt.line, tt.message)
			}
		})
	}
}

func TestProblemsSortedByLine(t *testing.T) {
	data := []byte("schema_version: 1\nlanguage: 1\npacks: [Bad]\ngate_level: bogus\n")
	ps := KindFor(".writing-anything/config.yaml").Validate(data)
	var fields []string
	for _, p := range ps {
		fields = append(fields, p.Field)
	}
	if want := []string{"language", "packs[0]", "gate_level"}; !reflect.DeepEqual(fields, want) {
		t.Fatalf("fields = %v, want %v", fields, want)
	}
}

func TestKindFor(t *testing.T) {
	if k := KindFor(".writing-anything/config.yaml"); k == nil || k.Name != "config" {
		t.Fatalf("KindFor(config.yaml) = %+v", k)
	}
	for _, rel := range []string{"notes.yaml", ".writing-anything/other.yaml", "config.yaml"} {
		if k := KindFor(rel); k != nil {
			t.Errorf("KindFor(%q) = %s, want nil", rel, k.Name)
		}
	}
	ks := Kinds()
	ks[0] = nil
	if Kinds()[0] == nil {
		t.Fatal("Kinds must return a copy")
	}
}

// 嵌套字段与数组元素的字段路径为点分路径加 [i]，行号指向字段所在行。
func TestFieldPaths(t *testing.T) {
	doc, p := Parse([]byte("a:\n  b:\n    - x\n    - c: 1\n"))
	if p != nil {
		t.Fatal(p)
	}
	d := &decoder{locs: map[string]location{}}
	d.value(doc, nil, "", 0)
	for ptr, want := range map[string]location{
		pointer([]string{"a"}):                {"a", 1},
		pointer([]string{"a", "b"}):           {"a.b", 2},
		pointer([]string{"a", "b", "0"}):      {"a.b[0]", 3},
		pointer([]string{"a", "b", "1", "c"}): {"a.b[1].c", 4},
	} {
		if got := d.locs[ptr]; got != want {
			t.Errorf("locs[%q] = %+v, want %+v", ptr, got, want)
		}
	}
}

func TestDecode(t *testing.T) {
	src := "base: &b {a: 1, c: 0}\nother: &o {d: 4}\nm:\n  <<: *b\n  c: 2\nn:\n  <<: [*b, *o]\nwhen: 2001-12-14\nflag: yes\nnothing: ~\n"
	doc, p := Parse([]byte(src))
	if p != nil {
		t.Fatal(p)
	}
	got := Decode(doc).(map[string]any)
	if want := map[string]any{"a": 1, "c": 2}; !reflect.DeepEqual(got["m"], want) {
		t.Errorf("m = %#v, want %#v", got["m"], want)
	}
	if want := map[string]any{"a": 1, "c": 0, "d": 4}; !reflect.DeepEqual(got["n"], want) {
		t.Errorf("n = %#v, want %#v", got["n"], want)
	}
	if got["when"] != "2001-12-14" {
		t.Errorf("timestamp = %#v, want the original string", got["when"])
	}
	if got["flag"] != "yes" || got["nothing"] != nil {
		t.Errorf("flag = %#v, nothing = %#v", got["flag"], got["nothing"])
	}
}

func TestParseErrors(t *testing.T) {
	tests := []struct {
		src     string
		line    int
		message string
	}{
		{"? [a, b]\n: 1\n", 1, "字段名必须是标量"},
		{"a: 1\n\tb: 2\n", 2, "YAML 语法错误"},
		{"a: *missing\n", 0, "YAML 语法错误：unknown anchor"},
		{"[a, b\n", 2, "YAML 语法错误：did not find expected ',' or ']'"},
		{"a: 1\nb: 2\nc: d: e\n", 3, "YAML 语法错误：mapping values are not allowed"},
		{"a: 1\nb: [\nc: 2\n", 2, "YAML 语法错误"},
	}
	for _, tt := range tests {
		_, p := Parse([]byte(tt.src))
		if p == nil || p.Code != "artifact.parse-error" || p.Line != tt.line || !strings.HasPrefix(p.Message, tt.message) {
			t.Errorf("Parse(%q) = %+v, want line %d message %q", tt.src, p, tt.line, tt.message)
		}
	}
}

func TestDescribe(t *testing.T) {
	tests := []struct {
		kind jsonschema.ErrorKind
		want string
	}{
		{&kind.MinItems{Got: 0, Want: 1}, "至少需要 1 项，实际为 0 项"},
		{&kind.MaxItems{Got: 3, Want: 2}, "最多允许 2 项，实际为 3 项"},
		{&kind.MinLength{Got: 1, Want: 2}, "长度至少为 2 个字符，实际为 1 个"},
		{&kind.MaxLength{Got: 9, Want: 8}, "长度最多为 8 个字符，实际为 9 个"},
		{&kind.Minimum{Got: big.NewRat(1, 1), Want: big.NewRat(2, 1)}, "不符合 Schema 约束 minimum"},
	}
	for _, tt := range tests {
		if got := describe(tt.kind); !strings.HasPrefix(got, tt.want) {
			t.Errorf("describe(%v) = %q, want prefix %q", tt.kind.KeywordPath(), got, tt.want)
		}
	}
	if got := describe(&kind.Minimum{Got: big.NewRat(1, 1), Want: big.NewRat(2, 1)}); got != "不符合 Schema 约束 minimum" {
		t.Errorf("fallback = %q, want only the keyword without the validator's English text", got)
	}
	if show(func() {}) == "" {
		t.Error("show must fall back for values JSON cannot encode")
	}
}

// 少量别名层层引用会展开成海量节点，解析时拒绝，避免耗尽内存。
func TestAliasBomb(t *testing.T) {
	var b strings.Builder
	b.WriteString("a0: &a0 [x, x, x, x, x, x, x, x, x, x]\n")
	for i := 1; i <= 8; i++ {
		fmt.Fprintf(&b, "a%d: &a%d [", i, i)
		for j := 0; j < 10; j++ {
			if j > 0 {
				b.WriteString(", ")
			}
			fmt.Fprintf(&b, "*a%d", i-1)
		}
		b.WriteString("]\n")
	}
	_, p := Parse([]byte(b.String()))
	if p == nil || p.Code != "artifact.parse-error" || !strings.Contains(p.Message, "展开别名后超过") {
		t.Fatalf("Parse(alias bomb) = %+v", p)
	}
	ps := KindFor(".writing-anything/config.yaml").Validate([]byte(b.String()))
	if len(ps) != 1 || ps[0].Code != "artifact.parse-error" {
		t.Fatalf("Validate(alias bomb) = %+v", ps)
	}
	if Decode(nil) != nil {
		t.Fatal("Decode(nil) must be nil")
	}
}

func TestKindsForPacksAndGenres(t *testing.T) {
	if k := KindFor(".writing-anything/packs/house.yaml"); k == nil || k.Name != "pack" {
		t.Fatalf("KindFor(packs/house.yaml) = %+v", k)
	}
	for _, rel := range []string{"", ".writing-anything/packs/sub/house.yaml", ".writing-anything/genres/minimal.yaml"} {
		if k := KindFor(rel); k != nil {
			t.Errorf("KindFor(%q) = %s, want nil", rel, k.Name)
		}
	}
	if k := Named("genre"); k == nil || k.Pattern != "" {
		t.Fatalf("Named(genre) = %+v, want a built-in-only kind", k)
	}
	if Named("nope") != nil {
		t.Fatal("Named(nope) must be nil")
	}
}

func TestKindForWorks(t *testing.T) {
	if k := KindFor(".writing-anything/works/my-post/work.yaml"); k == nil || k.Name != "work" {
		t.Fatalf("KindFor(works/my-post/work.yaml) = %+v", k)
	}
	for _, rel := range []string{".writing-anything/works/work.yaml", ".writing-anything/works/a/b/work.yaml", ".writing-anything/works/my-post/brief.yaml"} {
		if k := KindFor(rel); k != nil {
			t.Errorf("KindFor(%q) = %s, want nil", rel, k.Name)
		}
	}
	// 嵌套单元的字段路径逐层写出下标。
	ps := Named("work").Validate([]byte("schema_version: 1\nid: w\ngenre: minimal\ntype: article\nstage: brief\nunits:\n  - id: a\n    type: section\n    stage: brief\n    units:\n      - {id: b, type: section}\n"))
	if len(ps) != 1 || ps[0].Code != "artifact.schema-violation" || ps[0].Field != "units[0].units[0].stage" {
		t.Fatalf("problems = %+v, want one missing units[0].units[0].stage", ps)
	}
}

// 可执行内容先于 Schema 校验报告，给出字段路径与行号；核心标签、合并键与别名不算可执行内容。
func TestExecutable(t *testing.T) {
	pack := Named("pack")
	tests := []struct {
		src   string
		field string
		line  int
	}{
		{"schema_version: 1\nid: a\ntitle: t\ndescription: !exec rm\n", "description", 4},
		{"schema_version: 1\nid: a\ntitle: t\nrules:\n  r:\n    type: length-range\n    params:\n      run: x\n", "rules.r.params.run", 8},
		{"schema_version: 1\nid: a\ntitle: t\nstyle:\n  - ok\n  - !!python/object:os.system x\n", "style[1]", 6},
		{"schema_version: 1\nid: a\ntitle: t\nhooks: [a]\n", "hooks", 4},
		{"schema_version: 1\nid: a\ntitle: t\nrules:\n  r:\n    type: length-range\n    params: {Script: x}\n", "rules.r.params.Script", 7},
	}
	for _, tt := range tests {
		ps := pack.Validate([]byte(tt.src))
		if len(ps) != 1 || ps[0].Code != "artifact.executable-content" || ps[0].Field != tt.field || ps[0].Line != tt.line {
			t.Errorf("%q: problems = %+v, want one artifact.executable-content at %s line %d", tt.src, ps, tt.field, tt.line)
		}
	}
	ok := "schema_version: 1\nid: a\ntitle: !!str t\nbase: &b {x: 1}\n"
	if ps := Executable(mustParse(t, ok)); len(ps) != 0 {
		t.Errorf("core tags and anchors: problems = %+v, want none", ps)
	}
	merged := "a: &a {k: 1}\nb:\n  <<: *a\n  n: 1.5\n  t: 2001-12-14\n  z: null\n  y: true\n"
	if ps := Executable(mustParse(t, merged)); len(ps) != 0 {
		t.Errorf("merge keys: problems = %+v, want none", ps)
	}
	// 工作区配置同样只能包含数据。
	if ps := KindFor(".writing-anything/config.yaml").Validate(readFixture(t, filepath.Join("testdata", "config", "invalid", "python-tag.yaml"))); len(ps) != 1 || ps[0].Code != "artifact.executable-content" || ps[0].Field != "language" {
		t.Errorf("config: problems = %+v", ps)
	}
}

func mustParse(t *testing.T, src string) *yaml.Node {
	t.Helper()
	doc, p := Parse([]byte(src))
	if p != nil {
		t.Fatal(p)
	}
	return doc
}

// 文体与规范包共用 common.schema.json 中的定义。
func TestSharedDefinitions(t *testing.T) {
	ps := Named("pack").Validate(readFixture(t, filepath.Join("testdata", "pack", "invalid", "waiver-without-reason.yaml")))
	if len(ps) != 1 || ps[0].Code != "artifact.schema-violation" || ps[0].Field != "waivers[0].reason" {
		t.Fatalf("problems = %+v, want waivers[0].reason missing", ps)
	}
	ps = Named("genre").Validate(readFixture(t, filepath.Join("testdata", "genre", "invalid", "bad-severity.yaml")))
	if len(ps) != 1 || ps[0].Field != "rules.length.severity" {
		t.Fatalf("problems = %+v, want rules.length.severity", ps)
	}
	ps = Named("pack").Validate([]byte("schema_version: 1\nid: a\ntitle: t\nwaivers:\n  - rule: r\n    reason: '  '\n"))
	if len(ps) != 1 || ps[0].Field != "waivers[0].reason" {
		t.Fatalf("blank reason: problems = %+v", ps)
	}
}
