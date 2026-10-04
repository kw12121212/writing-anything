package decision

import (
	"os"
	"path/filepath"
	"testing"
)

func TestDbgLoadBroken(t *testing.T) {
	root := t.TempDir()
	os.MkdirAll(filepath.Join(root, ".writing-anything"), 0o755)
	os.WriteFile(filepath.Join(root, ".writing-anything", "decisions.yaml"), []byte("schema_version: 1\nquestions: [\n"), 0o644)
	ds, _, ps := Load(root)
	t.Logf("ds=%d ps=%+v", len(ds), ps)
}
