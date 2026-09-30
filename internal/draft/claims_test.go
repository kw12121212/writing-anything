package draft

import (
	"os"
	"path/filepath"
	"slices"
	"strings"
	"testing"

	"github.com/kw12121212/writing-anything/internal/source"
	"github.com/kw12121212/writing-anything/internal/work"
	"github.com/kw12121212/writing-anything/internal/workspace"
)

func addSource(t *testing.T, root, id string) {
	t.Helper()
	if _, _, ps := source.Add(root, []byte("原文 "+id), "interview", "", id, ""); len(ps) > 0 {
		t.Fatal(ps)
	}
}

func TestAddClaim(t *testing.T) {
	root, _ := newWork(t)
	addSource(t, root, "interview-ceo")
	w, u := unit(t, root, "my-post")
	rel := ".writing-anything/works/my-post/claims.yaml"
	c, changed, ps := AddClaim(root, w, u, "experience", "我们在一周内完成了迁移", "", []string{"interview-ceo"})
	if len(ps) > 0 {
		t.Fatal(ps)
	}
	if c.ID != "claim-1" || c.Kind != "experience" || !slices.Equal(c.Sources, []string{"interview-ceo"}) || !slices.Equal(changed, []string{rel}) {
		t.Fatalf("claim = %+v, changed = %v", c, changed)
	}
	// 作者的注释保留；没有素材的论断 sources 为空数组。
	write(t, root, rel, "# 作者的注释\n"+read(t, root, rel))
	c, _, ps = AddClaim(root, w, u, "opinion", "微服务不适合小团队", "", nil)
	if len(ps) > 0 || c.ID != "claim-2" || c.Sources == nil || len(c.Sources) != 0 {
		t.Fatalf("second claim = %+v, %v", c, ps)
	}
	text := read(t, root, rel)
	if !strings.Contains(text, "# 作者的注释") || !strings.Contains(text, "sources: []") {
		t.Errorf("claims.yaml:\n%s", text)
	}
	// 显式 id；默认 id 跳过已被占用的编号。
	if c, _, ps = AddClaim(root, w, u, "data", "性能提升了 30%", "claim-4", nil); len(ps) > 0 || c.ID != "claim-4" {
		t.Fatalf("explicit id = %+v, %v", c, ps)
	}
	if c, _, _ = AddClaim(root, w, u, "fact", "x", "", nil); c.ID != "claim-5" {
		t.Fatalf("next default id = %s, want claim-5", c.ID)
	}
	if ps := CheckArtifact(root, rel); len(ps) > 0 {
		t.Fatalf("CheckArtifact = %v", ps)
	}
}

func TestAddClaimChildCreatesUnitDir(t *testing.T) {
	root, _ := newWork(t)
	w, u := unit(t, root, "my-post/intro")
	dir := ".writing-anything/works/my-post/units/intro"
	if _, changed, ps := AddClaim(root, w, u, "case", "某客户", "", nil); len(ps) > 0 || !slices.Equal(changed, []string{dir, dir + "/claims.yaml"}) {
		t.Fatalf("changed = %v, %v", changed, ps)
	}
}

func TestAddClaimRejects(t *testing.T) {
	root, _ := newWork(t)
	addSource(t, root, "interview-ceo")
	w, u := unit(t, root, "my-post")
	if _, _, ps := AddClaim(root, w, u, "fact", "x", "dup", nil); len(ps) > 0 {
		t.Fatal(ps)
	}
	rel := ".writing-anything/works/my-post/claims.yaml"
	before := read(t, root, rel)
	for _, tc := range []struct {
		name, kind, text, id string
		sources              []string
		code, field          string
	}{
		{"unknown source", "data", "x", "", []string{"interview-ceo", "nope"}, "claim.unknown-source", "sources[1]"},
		{"bad kind", "rumor", "x", "", nil, "artifact.schema-violation", "kind"},
		{"blank text", "data", "  ", "", nil, "artifact.schema-violation", "text"},
		{"missing text", "data", "", "", nil, "artifact.schema-violation", "text"},
		{"bad id", "data", "x", "Bad_ID", nil, "artifact.schema-violation", "id"},
		{"duplicate sources", "data", "x", "", []string{"interview-ceo", "interview-ceo"}, "artifact.schema-violation", "sources"},
		{"duplicate id", "data", "x", "dup", nil, "artifact.duplicate-id", "id"},
	} {
		_, changed, ps := AddClaim(root, w, u, tc.kind, tc.text, tc.id, tc.sources)
		if len(ps) != 1 || ps[0].Code != tc.code || ps[0].Field != tc.field || changed != nil {
			t.Errorf("%s: problems = %+v", tc.name, ps)
		}
	}
	if read(t, root, rel) != before {
		t.Fatal("claims.yaml changed")
	}
	// 已有的 claims.yaml 不合法或带锚点时不修改。
	write(t, root, rel, "schema_version: 1\nclaims: {}\n")
	if _, _, ps := AddClaim(root, w, u, "data", "x", "", nil); len(ps) == 0 || ps[0].Code != "artifact.schema-violation" {
		t.Fatalf("invalid claims.yaml: %v", ps)
	}
	write(t, root, rel, "schema_version: 1\nclaims: &c []\n")
	if _, _, ps := AddClaim(root, w, u, "data", "x", "", nil); len(ps) != 1 || ps[0].Code != "claim.anchored-value" {
		t.Fatalf("anchored claims: %v", ps)
	}
}

func TestCheckUnitArtifacts(t *testing.T) {
	root, _ := newWork(t)
	addSource(t, root, "interview-ceo")
	w, u := unit(t, root, "my-post")
	_, child := unit(t, root, "my-post/intro")
	if _, _, ps := Write(root, w, u, []byte("正文"), ""); len(ps) > 0 {
		t.Fatal(ps)
	}
	if _, _, ps := AddClaim(root, w, child, "experience", "x", "", []string{"interview-ceo"}); len(ps) > 0 {
		t.Fatal(ps)
	}
	w, _ = unit(t, root, "my-post")
	checked, ps := CheckUnitArtifacts(root, []*work.Work{w})
	want := []string{".writing-anything/works/my-post/provenance.yaml", ".writing-anything/works/my-post/units/intro/claims.yaml"}
	if len(ps) > 0 || !slices.Equal(checked, want) {
		t.Fatalf("checked = %v, problems = %v", checked, ps)
	}
	// 被引用的素材被删除、论断 id 重复、写入记录不合法。
	os.RemoveAll(filepath.Join(root, ".writing-anything", "sources", "interview-ceo"))
	claims := want[1]
	write(t, root, claims, read(t, root, claims)+"  - {id: claim-1, kind: fact, text: y, sources: []}\n")
	write(t, root, want[0], "schema_version: 2\n")
	_, ps = CheckUnitArtifacts(root, []*work.Work{w})
	var got []string
	for _, p := range ps {
		got = append(got, p.Code+" "+p.Path+" "+p.Field)
	}
	for _, s := range []string{
		"claim.unknown-source " + claims + " claims[0].sources[0]",
		"artifact.duplicate-id " + claims + " claims[1].id",
	} {
		if !slices.Contains(got, s) {
			t.Errorf("problems %v lack %q", got, s)
		}
	}
	if !slices.ContainsFunc(ps, func(p workspace.Problem) bool { return p.Code == "artifact.schema-violation" && p.Path == want[0] }) {
		t.Errorf("problems %v lack the invalid record", got)
	}
	if ps := CheckArtifact(root, want[0]); ps != nil {
		t.Errorf("CheckArtifact(provenance.yaml) = %v, want no domain problems", ps)
	}
}

func TestAddClaimKeepsFlowComment(t *testing.T) {
	root, _ := newWork(t)
	w, u := unit(t, root, "my-post")
	rel := ".writing-anything/works/my-post/claims.yaml"
	write(t, root, rel, "schema_version: 1\nclaims: []  # 行尾注释\n")
	if _, _, ps := AddClaim(root, w, u, "fact", "x", "", nil); len(ps) > 0 || !strings.Contains(read(t, root, rel), "# 行尾注释") {
		t.Fatalf("%v\n%s", ps, read(t, root, rel))
	}
}
