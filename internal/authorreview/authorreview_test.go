package authorreview

import (
	"os"
	"path/filepath"
	"slices"
	"strings"
	"testing"
	"time"

	"github.com/kw12121212/writing-anything/internal/work"
	"github.com/kw12121212/writing-anything/internal/workspace"
)

const sha = "aaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaa"

func writeAt(t *testing.T, root, rel, content string) {
	t.Helper()
	p := filepath.Join(root, filepath.FromSlash(rel))
	if err := os.MkdirAll(filepath.Dir(p), 0o755); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(p, []byte(content), 0o644); err != nil {
		t.Fatal(err)
	}
}

func acceptance(channel, at string) string {
	s := "schema_version: 1\ngate_level: strict\ndraft_sha256: " + sha + "\nconfirmed_by_author: true\nconfirmation: 作者：可以\n"
	if channel != "" {
		s += "channel: " + channel + "\n"
	}
	return s + "accepted_at: \"" + at + "\"\n"
}

// newNovel 初始化 strict 工作区与 novel 作品 my-novel：part-one/chapter-one/{scene-1,scene-2}；scene-1 的验收没有
// channel（旧记录，按 cli），scene-2 的验收经阅读页；part-one 的检查点经命令行确认；决策队列有本作品允许修改钦定
// 条目的决策 dec-1（cli）、选择保持不变的 dec-2、其他作品的 dec-3 与普通决策 dec-4。
func newNovel(t *testing.T) (string, *work.Work) {
	t.Helper()
	root := t.TempDir()
	if _, ps := workspace.Init(root, map[string]string{"gate_level": "strict"}); len(ps) > 0 {
		t.Fatal(ps)
	}
	if _, _, ps := work.Create(root, "novel", "my-novel"); len(ps) > 0 {
		t.Fatal(ps)
	}
	for _, add := range []struct{ parent, typ, id string }{
		{"my-novel", "part", "part-one"},
		{"my-novel/part-one", "chapter", "chapter-one"},
		{"my-novel/chapter-one", "scene", "scene-1"},
		{"my-novel/chapter-one", "scene", "scene-2"},
	} {
		if _, _, ps := work.AddUnit(root, add.parent, add.typ, add.id, work.Anchor{}); len(ps) > 0 {
			t.Fatal(ps)
		}
	}
	units := ".writing-anything/works/my-novel/units/"
	writeAt(t, root, units+"scene-1/acceptance.yaml", acceptance("", "2026-10-07T08:00:00Z"))
	writeAt(t, root, units+"scene-2/acceptance.yaml", acceptance("preview", "2026-10-07T08:00:00Z"))
	writeAt(t, root, units+"part-one/checkpoint.yaml", "schema_version: 1\npart: my-novel/part-one\nscene: my-novel/scene-2\n"+
		"confirmed_by_author: true\nconfirmation: 作者：本部无误\nchannel: cli\nconfirmed_at: \"2026-10-07T09:00:00Z\"\n")
	writeAt(t, root, ".writing-anything/decisions.yaml", `schema_version: 1
decisions:
  - {id: dec-1, question: 是否允许修改作者钦定的设定条目 canon-1（钟）？, options: [{id: a, text: 允许}, {id: b, text: 保持不变}], blocking: true, units: [my-novel], status: decided, choice: a, confirmation: 作者：改吧, decided_at: "2026-10-07T10:00:00Z"}
  - {id: dec-2, question: 是否允许删除作者钦定的设定条目 canon-2？, options: [{id: a, text: 允许}, {id: b, text: 保持不变}], blocking: true, units: [my-novel], status: decided, choice: b, confirmation: 作者：不删, decided_at: "2026-10-07T10:00:00Z"}
  - {id: dec-3, question: 是否允许修改作者钦定的设定条目 canon-1（钟）？, options: [{id: a, text: 允许}, {id: b, text: 保持不变}], blocking: true, units: [other], status: decided, choice: a, confirmation: 作者：改吧, decided_at: "2026-10-07T10:00:00Z"}
  - {id: dec-4, question: 书名用哪个, options: [{id: a, text: 甲}], blocking: false, units: [my-novel], status: decided, choice: a, confirmation: 作者：甲, decided_at: "2026-10-07T10:00:00Z"}
`)
	w, ps := work.Load(root, "my-novel")
	if len(ps) > 0 {
		t.Fatal(ps)
	}
	return root, w
}

func keysOf(items []Item) []string {
	var out []string
	for _, it := range items {
		out = append(out, it.Key)
	}
	return out
}

func TestPendingAndReview(t *testing.T) {
	root, w := newNovel(t)
	if !Strict(root) {
		t.Fatal("workspace should be strict")
	}
	items, ps := Pending(root, w)
	want := []string{
		"acceptance:my-novel/scene-1@2026-10-07T08:00:00Z",
		"volume-checkpoint:my-novel/part-one@2026-10-07T09:00:00Z",
		"canon-decision:dec-1@2026-10-07T10:00:00Z",
	}
	if len(ps) > 0 || !slices.Equal(keysOf(items), want) {
		t.Fatalf("pending = %v, problems = %v", keysOf(items), ps)
	}
	if items[0].Path != ".writing-anything/works/my-novel/units/scene-1/acceptance.yaml" || items[2].Path != ".writing-anything/decisions.yaml" ||
		items[0].Confirmation != "作者：可以" || items[0].Work != "my-novel" {
		t.Fatalf("items = %+v", items)
	}
	if d := Describe(items[1]); !strings.Contains(d, "卷末检查点") || !strings.Contains(d, "作者：本部无误") {
		t.Fatalf("describe = %q", d)
	}
	if KindText(KindCanonDecision) != "钦定设定修改" || KindText(KindAcceptance) != "验收（Acceptance）" {
		t.Fatal("kind text")
	}
	// 不合法的键：空、未知、重复；都不写文件。
	for _, c := range []struct {
		keys        []string
		code, field string
	}{{nil, "input.invalid-value", "keys"}, {[]string{"acceptance:my-novel/scene-2@2026-10-07T08:00:00Z"}, "author-review.unknown-key", "keys[0]"},
		{[]string{want[0], want[0]}, "input.invalid-value", "keys[1]"}} {
		if got, _, ps := Review(root, w, c.keys, time.Now()); got != nil || len(ps) != 1 || ps[0].Code != c.code || ps[0].Field != c.field {
			t.Fatalf("review %v: got %v, problems %v", c.keys, got, ps)
		}
	}
	if _, err := os.Stat(filepath.Join(root, filepath.FromSlash(Path))); err == nil {
		t.Fatal("ledger written by rejected review")
	}
	now := time.Date(2026, 10, 7, 12, 0, 0, 0, time.UTC)
	got, changed, ps := Review(root, w, want[:2], now)
	if len(ps) > 0 || len(got) != 2 || !slices.Equal(changed, []string{Path}) {
		t.Fatalf("review: %v %v %v", got, changed, ps)
	}
	if items, _ := Pending(root, w); !slices.Equal(keysOf(items), want[2:]) {
		t.Fatalf("pending after review = %v", keysOf(items))
	}
	if got, _, ps := Review(root, w, want[2:], now); len(ps) > 0 || len(got) != 1 {
		t.Fatalf("second review: %v %v", got, ps)
	}
	if checked, ps := Check(root); len(ps) > 0 || !slices.Equal(checked, []string{Path}) {
		t.Fatalf("check: %v %v", checked, ps)
	}
	data, _ := os.ReadFile(filepath.Join(root, filepath.FromSlash(Path)))
	if strings.Count(string(data), "channel: preview") != 3 || !strings.Contains(string(data), `reviewed_at: "2026-10-07T12:00:00Z"`) {
		t.Fatalf("ledger:\n%s", data)
	}
	// 重新确认（确认时间变化）后旧复核不再适用。
	writeAt(t, root, ".writing-anything/works/my-novel/units/scene-1/acceptance.yaml", acceptance("cli", "2026-10-08T08:00:00Z"))
	if items, _ := Pending(root, w); !slices.Equal(keysOf(items), []string{"acceptance:my-novel/scene-1@2026-10-08T08:00:00Z"}) {
		t.Fatalf("pending after re-accept = %v", keysOf(items))
	}
}

func TestLedgerProblems(t *testing.T) {
	root, w := newNovel(t)
	if checked, ps := Check(root); checked != nil || ps != nil {
		t.Fatalf("missing ledger: %v %v", checked, ps)
	}
	writeAt(t, root, Path, "schema_version: 1\nreviews:\n  - {kind: acceptance, work: my-novel, ref: x, confirmed_at: bad, channel: cli, reviewed_at: bad}\n")
	items, ps := Pending(root, w)
	if len(items) != 3 || len(ps) == 0 || ps[0].Code != "artifact.schema-violation" {
		t.Fatalf("invalid ledger: %v %v", keysOf(items), ps)
	}
	if checked, ps := Check(root); len(checked) != 1 || len(ps) == 0 {
		t.Fatalf("check invalid: %v %v", checked, ps)
	}
	if got, _, ps := Review(root, w, []string{items[0].Key}, time.Now()); got != nil || len(ps) == 0 || ps[0].Code != "artifact.schema-violation" {
		t.Fatalf("review over invalid ledger: %v %v", got, ps)
	}
	// 经由锚点的 reviews 不能追加。
	writeAt(t, root, Path, "schema_version: 1\nreviews: &r []\n")
	if got, _, ps := Review(root, w, []string{items[0].Key}, time.Now()); got != nil || len(ps) == 0 || ps[0].Code != CodeAnchoredValue {
		t.Fatalf("anchored: %v %v", got, ps)
	}
	// 途经符号链接按不存在处理并报告。
	os.Remove(filepath.Join(root, filepath.FromSlash(Path)))
	if err := os.Symlink(filepath.Join(root, "elsewhere.yaml"), filepath.Join(root, filepath.FromSlash(Path))); err != nil {
		t.Skip(err)
	}
	if _, ps := Check(root); len(ps) == 0 || ps[0].Code != "artifact.not-found" {
		t.Fatal("symlink not reported")
	}
}
