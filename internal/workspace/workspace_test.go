package workspace

import (
	"os"
	"path/filepath"
	"reflect"
	"strings"
	"testing"

	"github.com/kw12121212/writing-anything/internal/schema"
)

// initialized 返回一个刚初始化的项目目录。
func initialized(t *testing.T, set map[string]string) string {
	t.Helper()
	dir := t.TempDir()
	if _, ps := Init(dir, set); len(ps) > 0 {
		t.Fatalf("Init problems = %+v", ps)
	}
	return dir
}

func readConfig(t *testing.T, dir string) string {
	t.Helper()
	data, err := os.ReadFile(filepath.Join(dir, ".writing-anything", "config.yaml"))
	if err != nil {
		t.Fatal(err)
	}
	return string(data)
}

func writeConfig(t *testing.T, dir, content string) {
	t.Helper()
	if err := os.WriteFile(filepath.Join(dir, ".writing-anything", "config.yaml"), []byte(content), 0o644); err != nil {
		t.Fatal(err)
	}
}

func codes(ps []Problem) []string {
	var out []string
	for _, p := range ps {
		out = append(out, p.Code)
	}
	return out
}

func TestInitFresh(t *testing.T) {
	dir := t.TempDir()
	created, ps := Init(dir, nil)
	if len(ps) > 0 {
		t.Fatalf("problems = %+v", ps)
	}
	want := []string{".writing-anything", ".writing-anything/config.yaml", ".writing-anything/sources", ".writing-anything/works"}
	if !reflect.DeepEqual(created, want) {
		t.Fatalf("created = %v, want %v", created, want)
	}
	cfg, ps := Load(dir)
	if len(ps) > 0 {
		t.Fatalf("Load problems = %+v", ps)
	}
	if want := (&Config{Language: "zh-CN", Packs: []string{}, GateLevel: "standard"}); !reflect.DeepEqual(cfg, want) {
		t.Fatalf("config = %+v, want %+v", cfg, want)
	}
	if strings.Contains(readConfig(t, dir), "default_genre") {
		t.Fatal("default_genre must be absent when --genre is not given")
	}
	if checked, ps := Check(dir); len(ps) > 0 || !reflect.DeepEqual(checked, []string{".writing-anything/config.yaml"}) {
		t.Fatalf("Check = %v, %+v", checked, ps)
	}
}

func TestInitWithValues(t *testing.T) {
	dir := initialized(t, map[string]string{"language": "en", "default_genre": "tech-blog", "gate_level": "strict"})
	cfg, _ := Load(dir)
	if cfg.Language != "en" || cfg.DefaultGenre == nil || *cfg.DefaultGenre != "tech-blog" || cfg.GateLevel != "strict" {
		t.Fatalf("config = %+v", cfg)
	}
	// 参数给出空的默认文体时不写该字段。
	dir = initialized(t, map[string]string{"default_genre": ""})
	if strings.Contains(readConfig(t, dir), "default_genre") {
		t.Fatal("empty default_genre must be omitted")
	}
}

func TestInitRerunKeepsFiles(t *testing.T) {
	dir := initialized(t, nil)
	edited := strings.Replace(readConfig(t, dir), "zh-CN", "en", 1)
	writeConfig(t, dir, edited)
	created, ps := Init(dir, nil)
	if len(created) != 0 || len(ps) != 0 {
		t.Fatalf("created = %v, problems = %+v, want none", created, ps)
	}
	if readConfig(t, dir) != edited {
		t.Fatal("rerunning init modified config.yaml")
	}
}

func TestInitFillsMissing(t *testing.T) {
	dir := initialized(t, nil)
	before := readConfig(t, dir)
	if err := os.Remove(filepath.Join(dir, ".writing-anything", "works")); err != nil {
		t.Fatal(err)
	}
	created, ps := Init(dir, nil)
	if len(ps) > 0 || !reflect.DeepEqual(created, []string{".writing-anything/works"}) {
		t.Fatalf("created = %v, problems = %+v", created, ps)
	}
	if readConfig(t, dir) != before {
		t.Fatal("config.yaml changed")
	}
}

func TestInitConfigKept(t *testing.T) {
	dir := initialized(t, map[string]string{"default_genre": "tech-blog"})
	_, ps := Init(dir, map[string]string{"gate_level": "strict", "language": "zh-CN", "default_genre": ""})
	if len(ps) != 2 {
		t.Fatalf("problems = %+v, want two warnings", ps)
	}
	for i, field := range []string{"default_genre", "gate_level"} {
		p := ps[i]
		if p.Code != "workspace.config-kept" || p.Severity != "warning" || p.Field != field || p.Path != ".writing-anything/config.yaml" {
			t.Errorf("problem %d = %+v, want config-kept warning on %s", i, p, field)
		}
	}
	if !strings.Contains(ps[1].Message, "保留原值 standard") || !strings.Contains(ps[0].Message, "保留原值 tech-blog") {
		t.Errorf("messages = %q, %q", ps[0].Message, ps[1].Message)
	}
	// 已有配置缺少的字段也提示保留，已有配置无法解析时不比较。
	writeConfig(t, dir, "schema_version: 1\nlanguage: zh-CN\npacks: []\ngate_level: standard\n")
	if _, ps := Init(dir, map[string]string{"default_genre": "novel"}); len(ps) != 1 || !strings.Contains(ps[0].Message, "保留原值 未设置") {
		t.Fatalf("problems = %+v", ps)
	}
	writeConfig(t, dir, "a: [\n")
	if _, ps := Init(dir, map[string]string{"gate_level": "fast"}); len(ps) != 0 {
		t.Fatalf("problems = %+v, want none for an unparsable config", ps)
	}
}

func TestInitInvalidWritesNothing(t *testing.T) {
	dir := t.TempDir()
	created, ps := Init(dir, map[string]string{"gate_level": "bogus"})
	if created != nil || len(ps) != 1 || ps[0].Code != "artifact.schema-violation" || ps[0].Field != "gate_level" || ps[0].Line != 0 {
		t.Fatalf("created = %v, problems = %+v", created, ps)
	}
	if entries, _ := os.ReadDir(dir); len(entries) != 0 {
		t.Fatalf("init wrote %d entries", len(entries))
	}
}

func TestInitWriteFailures(t *testing.T) {
	dir := t.TempDir()
	// 工作区位置已有同名文件。
	if err := os.WriteFile(filepath.Join(dir, ".writing-anything"), nil, 0o644); err != nil {
		t.Fatal(err)
	}
	created, ps := Init(dir, nil)
	if len(created) != 0 || !reflect.DeepEqual(codes(ps), []string{"workspace.write-failed"}) || ps[0].Path != ".writing-anything" {
		t.Fatalf("created = %v, problems = %+v", created, ps)
	}
	// sources 位置已有同名文件：其余部分照常补齐。
	dir = t.TempDir()
	os.MkdirAll(filepath.Join(dir, ".writing-anything"), 0o755)
	os.WriteFile(filepath.Join(dir, ".writing-anything", "sources"), nil, 0o644)
	created, ps = Init(dir, nil)
	if !reflect.DeepEqual(created, []string{".writing-anything/config.yaml", ".writing-anything/works"}) || !reflect.DeepEqual(codes(ps), []string{"workspace.write-failed"}) {
		t.Fatalf("created = %v, problems = %+v", created, ps)
	}
	if exists := Exists(dir); !exists {
		t.Fatal("workspace should exist")
	}
}

func TestInitReadOnly(t *testing.T) {
	if os.Getuid() == 0 {
		t.Skip("root ignores directory permissions")
	}
	dir := t.TempDir()
	os.MkdirAll(filepath.Join(dir, ".writing-anything"), 0o755)
	os.Chmod(filepath.Join(dir, ".writing-anything"), 0o555)
	t.Cleanup(func() { os.Chmod(filepath.Join(dir, ".writing-anything"), 0o755) })
	_, ps := Init(dir, nil)
	if len(ps) != 3 {
		t.Fatalf("problems = %+v, want config, sources and works failures", ps)
	}
	for _, p := range ps {
		if p.Code != "workspace.write-failed" {
			t.Errorf("problem = %+v", p)
		}
	}
	// 不可读的目录：Stat 失败但不是不存在。
	parent := t.TempDir()
	os.Chmod(parent, 0o000)
	t.Cleanup(func() { os.Chmod(parent, 0o755) })
	if _, ps := Init(filepath.Join(parent, "p"), nil); len(ps) != 1 || ps[0].Code != "workspace.read-failed" {
		t.Fatalf("problems = %+v", ps)
	}
}

func TestRequire(t *testing.T) {
	dir := t.TempDir()
	ps := Require(dir)
	if len(ps) != 1 || ps[0].Code != "workspace.not-found" || ps[0].Path != ".writing-anything" || !strings.Contains(ps[0].Message, dir) {
		t.Fatalf("problems = %+v", ps)
	}
	if ps := Require(initialized(t, nil)); ps != nil {
		t.Fatalf("problems = %+v, want nil", ps)
	}
}

func TestCheck(t *testing.T) {
	dir := initialized(t, nil)
	os.Remove(filepath.Join(dir, ".writing-anything", "sources"))
	os.Remove(filepath.Join(dir, ".writing-anything", "config.yaml"))
	checked, ps := Check(dir)
	if len(checked) != 0 || !reflect.DeepEqual(codes(ps), []string{"artifact.not-found", "workspace.missing-directory"}) {
		t.Fatalf("checked = %v, problems = %+v", checked, ps)
	}
	if ps[0].Path != ".writing-anything/config.yaml" || ps[1].Path != ".writing-anything/sources" {
		t.Fatalf("paths = %s, %s", ps[0].Path, ps[1].Path)
	}
	writeConfig(t, dir, "schema_version: 1\nlanguage: zh-CN\npacks: []\ngate_level: bogus\n")
	checked, ps = Check(dir)
	if !reflect.DeepEqual(checked, []string{".writing-anything/config.yaml"}) || ps[0].Code != "artifact.schema-violation" || ps[0].Line != 4 {
		t.Fatalf("checked = %v, problems = %+v", checked, ps)
	}
}

func TestLoad(t *testing.T) {
	dir := initialized(t, nil)
	writeConfig(t, dir, "schema_version: 1\nlanguage: 1\ndefault_genre: novel\npacks: [a, 2]\ngate_level: bogus\n")
	cfg, ps := Load(dir)
	if len(ps) == 0 {
		t.Fatal("want problems")
	}
	if cfg == nil || cfg.Language != "" || *cfg.DefaultGenre != "novel" || !reflect.DeepEqual(cfg.Packs, []string{"a"}) || cfg.GateLevel != "bogus" {
		t.Fatalf("config = %+v", cfg)
	}
	writeConfig(t, dir, "[1, 2]\n")
	if cfg, ps := Load(dir); cfg != nil || len(ps) == 0 {
		t.Fatalf("config = %+v, problems = %+v; want nil config for a non-mapping", cfg, ps)
	}
	writeConfig(t, dir, "a: [\n")
	if cfg, ps := Load(dir); cfg != nil || !reflect.DeepEqual(codes(ps), []string{"artifact.parse-error"}) {
		t.Fatalf("config = %+v, problems = %+v", cfg, ps)
	}
}

func TestValidateArtifact(t *testing.T) {
	dir := initialized(t, nil)
	for _, p := range []string{".writing-anything/config.yaml", filepath.Join(dir, ".writing-anything", "config.yaml")} {
		checked, ps := ValidateArtifact(dir, p)
		if len(ps) > 0 || !reflect.DeepEqual(checked, []string{".writing-anything/config.yaml"}) {
			t.Fatalf("%s: checked = %v, problems = %+v", p, checked, ps)
		}
	}
	os.WriteFile(filepath.Join(dir, "notes.yaml"), []byte("a: 1\n"), 0o644)
	tests := []struct{ path, code, reported string }{
		{"notes.yaml", "artifact.unknown-kind", "notes.yaml"},
		{"../elsewhere.yaml", "artifact.unknown-kind", "../elsewhere.yaml"},
		{filepath.Join(filepath.Dir(dir), "outside.yaml"), "artifact.unknown-kind", "../outside.yaml"},
		{".writing-anything/config.yaml", "artifact.not-found", ".writing-anything/config.yaml"},
	}
	os.Remove(filepath.Join(dir, ".writing-anything", "config.yaml"))
	for _, tt := range tests {
		checked, ps := ValidateArtifact(dir, tt.path)
		if len(checked) != 0 || len(ps) != 1 || ps[0].Code != tt.code || ps[0].Path != tt.reported {
			t.Errorf("%s: checked = %v, problems = %+v", tt.path, checked, ps)
		}
	}
	// 读取失败（路径是目录）。
	os.Mkdir(filepath.Join(dir, ".writing-anything", "config.yaml"), 0o755)
	if _, ps := ValidateArtifact(dir, ".writing-anything/config.yaml"); len(ps) != 1 || ps[0].Code != "workspace.read-failed" {
		t.Fatalf("problems = %+v", ps)
	}
}

func TestSummary(t *testing.T) {
	dir := initialized(t, nil)
	ws := filepath.Join(dir, ".writing-anything")
	os.Mkdir(filepath.Join(ws, "works", "a"), 0o755)
	os.Mkdir(filepath.Join(ws, "works", "b"), 0o755)
	os.WriteFile(filepath.Join(ws, "sources", "note.md"), nil, 0o644)
	os.WriteFile(filepath.Join(ws, "sources", ".DS_Store"), nil, 0o644)
	if s, w := Summary(dir); s != 1 || w != 2 {
		t.Fatalf("Summary = %d, %d; want 1, 2", s, w)
	}
	if s, w := Summary(t.TempDir()); s != 0 || w != 0 {
		t.Fatalf("Summary without workspace = %d, %d", s, w)
	}
}

func TestSetConfigPreservesComments(t *testing.T) {
	dir := initialized(t, nil)
	fixture, err := os.ReadFile(filepath.Join("testdata", "commented-config.yaml"))
	if err != nil {
		t.Fatal(err)
	}
	writeConfig(t, dir, string(fixture))
	cfg, changed, ps := SetConfig(dir, "gate_level", "strict")
	if !changed || len(ps) > 0 || cfg.GateLevel != "strict" {
		t.Fatalf("cfg = %+v, changed = %v, problems = %+v", cfg, changed, ps)
	}
	got := readConfig(t, dir)
	if want := strings.Replace(string(fixture), "gate_level: standard", "gate_level: strict", 1); got != want {
		t.Fatalf("config.yaml =\n%s\nwant\n%s", got, want)
	}
}

func TestSetConfigValues(t *testing.T) {
	dir := initialized(t, map[string]string{"default_genre": "tech-blog"})
	cfg, changed, ps := SetConfig(dir, "packs", " house-style, glossary-zh ,")
	if !changed || len(ps) > 0 || !reflect.DeepEqual(cfg.Packs, []string{"house-style", "glossary-zh"}) {
		t.Fatalf("cfg = %+v, changed = %v, problems = %+v", cfg, changed, ps)
	}
	if !strings.Contains(readConfig(t, dir), "packs: [house-style, glossary-zh]") {
		t.Fatalf("config.yaml = %s", readConfig(t, dir))
	}
	if cfg, _, _ := SetConfig(dir, "packs", ""); !reflect.DeepEqual(cfg.Packs, []string{}) {
		t.Fatalf("packs = %v, want empty", cfg.Packs)
	}
	cfg, changed, _ = SetConfig(dir, "default_genre", "")
	if !changed || cfg.DefaultGenre != nil || strings.Contains(readConfig(t, dir), "default_genre") {
		t.Fatalf("default_genre not removed: %+v", cfg)
	}
	// 字段不存在时追加。
	cfg, changed, _ = SetConfig(dir, "default_genre", "novel")
	if !changed || *cfg.DefaultGenre != "novel" {
		t.Fatalf("cfg = %+v", cfg)
	}
	// 删除不存在的字段不算修改。
	SetConfig(dir, "default_genre", "")
	if _, changed, _ := SetConfig(dir, "default_genre", ""); changed {
		t.Fatal("removing an absent field must not write")
	}
}

func TestSetConfigSameValue(t *testing.T) {
	dir := initialized(t, nil)
	info, _ := os.Stat(filepath.Join(dir, ".writing-anything", "config.yaml"))
	cfg, changed, ps := SetConfig(dir, "gate_level", "standard")
	if changed || len(ps) > 0 || cfg.GateLevel != "standard" {
		t.Fatalf("cfg = %+v, changed = %v, problems = %+v", cfg, changed, ps)
	}
	if after, _ := os.Stat(filepath.Join(dir, ".writing-anything", "config.yaml")); !after.ModTime().Equal(info.ModTime()) {
		t.Fatal("config.yaml was rewritten")
	}
}

func TestSetConfigRejects(t *testing.T) {
	dir := initialized(t, nil)
	before := readConfig(t, dir)
	tests := []struct{ key, value, code, field string }{
		{"gate_level", "bogus", "artifact.schema-violation", "gate_level"},
		{"packs", "Bad_Id", "artifact.schema-violation", "packs[0]"},
		{"language", "", "artifact.schema-violation", "language"},
		{"schema_version", "2", "config.unknown-key", ""},
	}
	for _, tt := range tests {
		cfg, changed, ps := SetConfig(dir, tt.key, tt.value)
		if changed || len(ps) != 1 || ps[0].Code != tt.code || ps[0].Field != tt.field || ps[0].Line != 0 {
			t.Errorf("%s=%q: changed = %v, problems = %+v", tt.key, tt.value, changed, ps)
		}
		if tt.code == "artifact.schema-violation" && cfg.GateLevel != "standard" {
			t.Errorf("%s: cfg = %+v, want the unchanged config", tt.key, cfg)
		}
	}
	if readConfig(t, dir) != before {
		t.Fatal("rejected values modified config.yaml")
	}
}

func TestSetConfigRepairsBrokenField(t *testing.T) {
	dir := initialized(t, nil)
	writeConfig(t, dir, "schema_version: 1\nlanguage: zh-CN\npacks: []\ngate_level: bogus\ncolour: red\n")
	cfg, changed, ps := SetConfig(dir, "gate_level", "fast")
	if !changed || cfg.GateLevel != "fast" {
		t.Fatalf("cfg = %+v, changed = %v", cfg, changed)
	}
	// 其他字段已有的问题照常报告，行号指向写入后的文件。
	if len(ps) != 1 || ps[0].Field != "colour" || ps[0].Line != 5 {
		t.Fatalf("problems = %+v, want the remaining colour violation", ps)
	}
}

func TestSetConfigUnreadable(t *testing.T) {
	dir := initialized(t, nil)
	writeConfig(t, dir, "a: [\n")
	if _, changed, ps := SetConfig(dir, "gate_level", "fast"); changed || !reflect.DeepEqual(codes(ps), []string{"artifact.parse-error"}) {
		t.Fatalf("changed = %v, problems = %+v", changed, ps)
	}
	writeConfig(t, dir, "[1]\n")
	if cfg, changed, ps := SetConfig(dir, "gate_level", "fast"); cfg != nil || changed || len(ps) == 0 {
		t.Fatalf("cfg = %+v, changed = %v, problems = %+v", cfg, changed, ps)
	}
	// 空文件：从空映射开始写入，其余必需字段仍缺失。
	writeConfig(t, dir, "")
	cfg, changed, ps := SetConfig(dir, "gate_level", "fast")
	if !changed || cfg.GateLevel != "fast" || len(ps) == 0 {
		t.Fatalf("cfg = %+v, changed = %v, problems = %+v", cfg, changed, ps)
	}
	os.Remove(filepath.Join(dir, ".writing-anything", "config.yaml"))
	if _, _, ps := SetConfig(dir, "gate_level", "fast"); !reflect.DeepEqual(codes(ps), []string{"artifact.not-found"}) {
		t.Fatalf("problems = %+v", ps)
	}
}

func TestSetConfigWriteFailure(t *testing.T) {
	if os.Getuid() == 0 {
		t.Skip("root ignores directory permissions")
	}
	dir := initialized(t, nil)
	ws := filepath.Join(dir, ".writing-anything")
	os.Chmod(ws, 0o555)
	t.Cleanup(func() { os.Chmod(ws, 0o755) })
	if _, changed, ps := SetConfig(dir, "gate_level", "fast"); changed || !reflect.DeepEqual(codes(ps), []string{"workspace.write-failed"}) {
		t.Fatalf("changed = %v, problems = %+v", changed, ps)
	}
}

// 带锚点的值被其他字段引用：修改会让别名失效，必须拒绝且不改文件。
func TestSetConfigAnchors(t *testing.T) {
	dir := initialized(t, nil)
	fixture, err := os.ReadFile(filepath.Join("..", "schema", "testdata", "config", "valid", "anchors.yaml"))
	if err != nil {
		t.Fatal(err)
	}
	writeConfig(t, dir, string(fixture))
	for _, v := range []string{"", "novel"} {
		cfg, changed, ps := SetConfig(dir, "default_genre", v)
		if changed || len(ps) != 1 || ps[0].Code != "config.anchored-value" || ps[0].Field != "default_genre" || ps[0].Line != 3 || cfg == nil {
			t.Fatalf("default_genre=%q: cfg = %+v, changed = %v, problems = %+v", v, cfg, changed, ps)
		}
	}
	if readConfig(t, dir) != string(fixture) {
		t.Fatal("config.yaml changed")
	}
	// 值本身是别名时可以直接替换，锚点所在的字段不受影响。
	writeConfig(t, dir, "schema_version: 1\nlanguage: &l en\ndefault_genre: *l\npacks: []\ngate_level: standard\n")
	if cfg, changed, ps := SetConfig(dir, "default_genre", "novel"); !changed || len(ps) > 0 || *cfg.DefaultGenre != "novel" || cfg.Language != "en" {
		t.Fatalf("cfg = %+v, changed = %v, problems = %+v", cfg, changed, ps)
	}
	if _, ps := Load(dir); len(ps) > 0 {
		t.Fatalf("config.yaml broken after edit: %+v", ps)
	}
}

func TestWriteFileKeepsMode(t *testing.T) {
	dir := initialized(t, nil)
	path := filepath.Join(dir, ".writing-anything", "config.yaml")
	os.Chmod(path, 0o600)
	if _, changed, _ := SetConfig(dir, "gate_level", "fast"); !changed {
		t.Fatal("not changed")
	}
	if info, _ := os.Stat(path); info.Mode().Perm() != 0o600 {
		t.Fatalf("mode = %v, want 0600", info.Mode().Perm())
	}
}

// validate <path> 不跟随工作区目录之下的符号链接读取项目目录之外的文件。
func TestValidateArtifactSymlinks(t *testing.T) {
	dir := t.TempDir()
	if _, ps := Init(dir, nil); len(ps) > 0 {
		t.Fatal(ps)
	}
	outside := t.TempDir()
	os.WriteFile(filepath.Join(outside, "feedback.yaml"), []byte("schema_version: 1\nfeedback: []\n"), 0o644)
	work := filepath.Join(dir, ".writing-anything", "works", "my-post")
	os.MkdirAll(filepath.Join(work, "units"), 0o755)
	os.Symlink(outside, filepath.Join(work, "units", "intro"))
	os.Symlink(filepath.Join(outside, "feedback.yaml"), filepath.Join(work, "feedback.yaml"))
	for _, rel := range []string{".writing-anything/works/my-post/units/intro/feedback.yaml", ".writing-anything/works/my-post/feedback.yaml"} {
		if checked, ps := ValidateArtifact(dir, rel); len(checked) != 0 || len(ps) != 1 || ps[0].Code != "artifact.not-found" {
			t.Errorf("%s: checked = %v, problems = %+v", rel, checked, ps)
		}
	}
}

func TestBlockSequenceKeepsLineComment(t *testing.T) {
	doc, _ := schema.Parse([]byte("a: []  # 注释\nb: [x]\n"))
	m := doc.Content[0]
	BlockSequence(m, "a")
	BlockSequence(m, "b")
	data, _ := Encode(doc)
	if !strings.Contains(string(data), "a: # 注释") || strings.Contains(string(data), "[x]") {
		t.Fatalf("encoded:\n%s", data)
	}
}

// Blocked 与 EnsureDir 逐级检查工作区目录之下的路径段，不跟随符号链接（validated-path-segments）。
func TestBlockedAndEnsureDir(t *testing.T) {
	root := t.TempDir()
	outside := t.TempDir()
	if err := os.Mkdir(filepath.Join(root, ".writing-anything"), 0o755); err != nil {
		t.Fatal(err)
	}
	if err := EnsureDir(root, ".writing-anything/series/guide"); err != nil {
		t.Fatal(err)
	}
	if info, err := os.Lstat(filepath.Join(root, ".writing-anything", "series", "guide")); err != nil || !info.IsDir() {
		t.Fatalf("EnsureDir 未创建目录: %v", err)
	}
	if err := EnsureDir(root, ".writing-anything/series/guide"); err != nil {
		t.Fatalf("目录已存在时应成功: %v", err)
	}
	if Blocked(root, ".writing-anything/series/guide/canon.yaml") {
		t.Fatal("不存在的文件不应算受阻")
	}
	if err := os.Symlink(outside, filepath.Join(root, ".writing-anything", "series", "link")); err != nil {
		t.Fatal(err)
	}
	if err := EnsureDir(root, ".writing-anything/series/link/sub"); err != ErrNotRealDir {
		t.Fatalf("途经符号链接应返回 ErrNotRealDir，实际 %v", err)
	}
	if entries, _ := os.ReadDir(outside); len(entries) != 0 {
		t.Fatalf("项目目录之外被创建了 %v", entries)
	}
	if !Blocked(root, ".writing-anything/series/link/canon.yaml") {
		t.Fatal("途经符号链接的路径应算受阻")
	}
	if err := os.WriteFile(filepath.Join(root, ".writing-anything", "series", "file"), []byte("x"), 0o644); err != nil {
		t.Fatal(err)
	}
	if err := EnsureDir(root, ".writing-anything/series/file/sub"); err != ErrNotRealDir {
		t.Fatalf("途经普通文件应返回 ErrNotRealDir，实际 %v", err)
	}
	if !Blocked(root, ".writing-anything/series/link") {
		t.Fatal("本身是符号链接的路径应算受阻")
	}
}
