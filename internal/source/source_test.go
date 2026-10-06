package source

import (
	"os"
	"path/filepath"
	"slices"
	"strings"
	"testing"

	"github.com/kw12121212/writing-anything/internal/workspace"
)

func newRoot(t *testing.T) string {
	t.Helper()
	root := t.TempDir()
	if _, ps := workspace.Init(root, nil); len(ps) > 0 {
		t.Fatal(ps)
	}
	return root
}

func codes(ps []workspace.Problem) []string {
	var out []string
	for _, p := range ps {
		out = append(out, p.Code)
	}
	return out
}

func TestAdd(t *testing.T) {
	root := newRoot(t)
	content := []byte("我在 2023 年迁移过一次数据库\r\n")
	s, created, ps := Add(root, content, "author-note", "迁移笔记", "", "idea.md")
	if len(ps) > 0 {
		t.Fatal(ps)
	}
	id := "src-" + Hash(content)[:12]
	if s.ID != id || s.File != "original.md" || s.Bytes != len(content) {
		t.Fatalf("source = %+v", s)
	}
	want := []string{Dir(id), Dir(id) + "/original.md", Dir(id) + "/source.yaml"}
	if !slices.Equal(created, want) {
		t.Fatalf("created = %v, want %v", created, want)
	}
	if got, _ := os.ReadFile(filepath.Join(root, filepath.FromSlash(s.FilePath()))); string(got) != string(content) {
		t.Fatalf("archive = %q, want the original bytes", got)
	}
	meta, _ := os.ReadFile(filepath.Join(root, filepath.FromSlash(Path(id))))
	for _, want := range []string{"original_name: idea.md", "title: 迁移笔记", "type: author-note"} {
		if !strings.Contains(string(meta), want) {
			t.Errorf("source.yaml lacks %q:\n%s", want, meta)
		}
	}
	loaded, ps := Check(root, id)
	if len(ps) > 0 || loaded.Title != "迁移笔记" || loaded.SHA256 != s.SHA256 {
		t.Fatalf("Check = %+v, %v", loaded, ps)
	}
	if !Registered(root, id) || Registered(root, "nope") || Registered(root, "../x") {
		t.Fatal("Registered disagrees with the archive")
	}
}

func TestAddFileNames(t *testing.T) {
	root := newRoot(t)
	for _, tc := range []struct{ id, name, file string }{
		{"stdin", "", "original.txt"},
		{"plain", "NOTES", "original"},
		{"weird", "a.tar gz", "original"},
		{"upper", "Talk.TXT", "original.TXT"},
		{"dotfile", ".bashrc", "original"},
	} {
		s, _, ps := Add(root, []byte("x"), "reference", "", tc.id, tc.name)
		if len(ps) > 0 || s.File != tc.file {
			t.Errorf("Add(%q) file = %v, %v; want %s", tc.name, s, ps, tc.file)
		}
	}
}

func TestAddRejects(t *testing.T) {
	root := newRoot(t)
	if _, _, ps := Add(root, []byte("x"), "author-note", "", "dup", ""); len(ps) > 0 {
		t.Fatal(ps)
	}
	before, _ := os.ReadFile(filepath.Join(root, filepath.FromSlash(Path("dup"))))
	for _, tc := range []struct {
		name, typ, id string
		content       string
		code, field   string
	}{
		{"empty", "author-note", "", "", "source.empty", ""},
		{"unknown type", "diary", "", "x", "artifact.schema-violation", "type"},
		{"missing type", "", "", "x", "artifact.schema-violation", "type"},
		{"bad id", "author-note", "Bad_ID", "x", "artifact.schema-violation", "id"},
		{"exists", "author-note", "dup", "y", "source.exists", ""},
	} {
		_, created, ps := Add(root, []byte(tc.content), tc.typ, "", tc.id, "")
		if len(ps) != 1 || ps[0].Code != tc.code || ps[0].Field != tc.field || created != nil {
			t.Errorf("%s: problems = %+v, created = %v", tc.name, ps, created)
		}
		if tc.code == "source.exists" && ps[0].Path != ".writing-anything/sources/dup" {
			t.Errorf("exists path = %s", ps[0].Path)
		}
	}
	after, _ := os.ReadFile(filepath.Join(root, filepath.FromSlash(Path("dup"))))
	if string(before) != string(after) {
		t.Fatal("source.yaml changed")
	}
	entries, _ := os.ReadDir(filepath.Join(root, ".writing-anything", "sources"))
	if len(entries) != 1 {
		t.Fatalf("sources/ has %d entries, want only dup", len(entries))
	}
}

func TestLoadAll(t *testing.T) {
	root := newRoot(t)
	sources := filepath.Join(root, ".writing-anything", "sources")
	for _, id := range []string{"b-ok", "a-ok", "tampered", "renamed", "no-archive"} {
		if _, _, ps := Add(root, []byte("原文 "+id), "interview", "", id, ""); len(ps) > 0 {
			t.Fatal(ps)
		}
	}
	os.WriteFile(filepath.Join(sources, "tampered", "original.txt"), []byte("改过"), 0o644)
	meta, _ := os.ReadFile(filepath.Join(sources, "renamed", "source.yaml"))
	os.WriteFile(filepath.Join(sources, "renamed", "source.yaml"), []byte(strings.Replace(string(meta), "id: renamed", "id: other", 1)), 0o644)
	os.Remove(filepath.Join(sources, "no-archive", "original.txt"))
	os.Mkdir(filepath.Join(sources, "loose"), 0o755)
	os.Mkdir(filepath.Join(sources, ".hidden"), 0o755)
	os.WriteFile(filepath.Join(sources, "stray.md"), []byte("x"), 0o644)
	os.Symlink(filepath.Join(sources, "a-ok"), filepath.Join(sources, "linked"))

	got, checked, ps := LoadAll(root)
	var ids []string
	for _, s := range got {
		ids = append(ids, s.ID)
	}
	if !slices.Equal(ids, []string{"a-ok", "b-ok"}) {
		t.Errorf("sources = %v", ids)
	}
	wantChecked := []string{Path("a-ok"), Path("b-ok"), Path("no-archive"), Path("renamed"), Path("tampered")}
	if !slices.Equal(checked, wantChecked) {
		t.Errorf("checked = %v, want %v", checked, wantChecked)
	}
	want := map[string]workspace.Problem{
		"artifact.not-found": {Path: ".writing-anything/sources/loose/source.yaml"},
		"source.modified":    {Path: ".writing-anything/sources/tampered/original.txt"},
		"source.id-mismatch": {Path: ".writing-anything/sources/renamed/source.yaml", Field: "id"},
	}
	found := map[string]bool{}
	for _, p := range ps {
		if p.Code == "artifact.not-found" && p.Path == ".writing-anything/sources/no-archive/original.txt" {
			found["archive"] = true
			continue
		}
		w, ok := want[p.Code]
		if !ok || w.Path != p.Path || w.Field != p.Field {
			t.Errorf("unexpected problem %+v", p)
		}
		found[p.Code] = true
	}
	if len(found) != 4 {
		t.Errorf("problems = %v, want all four kinds", codes(ps))
	}
	if ps := CheckArtifact(root, Path("tampered")); len(ps) != 1 || ps[0].Code != "source.modified" {
		t.Errorf("CheckArtifact(tampered) = %v", ps)
	}
	if s, _, _ := LoadAll(t.TempDir()); s != nil {
		t.Error("LoadAll without sources/ must return nothing")
	}
}

func TestLoadSchemaViolation(t *testing.T) {
	root := newRoot(t)
	dir := filepath.Join(root, ".writing-anything", "sources", "bad")
	os.Mkdir(dir, 0o755)
	os.WriteFile(filepath.Join(dir, "source.yaml"), []byte("schema_version: 1\nid: bad\n"), 0o644)
	if _, ps := Check(root, "bad"); len(ps) == 0 || ps[0].Code != "artifact.schema-violation" {
		t.Fatalf("Check(bad) = %v", ps)
	}
	if Registered(root, "bad") {
		t.Fatal("an invalid source must not count as registered")
	}
}

func TestSymlinkedMetadata(t *testing.T) {
	root := newRoot(t)
	dir := filepath.Join(root, ".writing-anything", "sources", "linked")
	os.Mkdir(dir, 0o755)
	outside := filepath.Join(t.TempDir(), "source.yaml")
	os.WriteFile(outside, []byte("schema_version: 1\n"), 0o644)
	os.Symlink(outside, filepath.Join(dir, "source.yaml"))
	if _, ps := Check(root, "linked"); len(ps) != 1 || ps[0].Code != "artifact.not-found" {
		t.Fatalf("Check(symlinked source.yaml) = %v", ps)
	}
}
