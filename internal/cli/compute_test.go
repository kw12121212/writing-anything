package cli

import (
	"encoding/json"
	"os"
	"path/filepath"
	"slices"
	"strings"
	"testing"

	"github.com/kw12121212/writing-anything/internal/metrics"
	"github.com/kw12121212/writing-anything/internal/stylemetric"
)

type computeOut struct {
	result
	Units []struct {
		Unit       string
		Metrics    stylemetric.Metrics
		Paragraphs []stylemetric.Paragraph
		Drift      []stylemetric.Finding
	}
	Baselines *struct {
		Profile   *struct{ Source string }
		Accepted  []string
		Threshold struct {
			Value  float64
			Source string
		}
	}
}

func runCompute(t *testing.T, dir, ref string) (int, computeOut) {
	t.Helper()
	code, stdout, _ := run(t, "metrics", "compute", ref, "--dir", dir, "--json")
	var out computeOut
	if err := json.Unmarshal([]byte(stdout), &out); err != nil {
		t.Fatalf("%v; %s", err, stdout)
	}
	return code, out
}

func writeDraft(t *testing.T, dir, ref, body string) {
	t.Helper()
	writeFile(t, filepath.Join(dir, "in.md"), body)
	if code, stdout, _ := run(t, "draft", "write", ref, "--file", "in.md", "--dir", dir); code != 0 {
		t.Fatal(stdout)
	}
}

func TestMetricsComputeTreeAndProfile(t *testing.T) {
	dir := newWork(t)
	run(t, "unit", "add", "my-post", "section", "intro", "--dir", dir)
	writeDraft(t, dir, "my-post", "这是根单元的第一句话，写得比较长一些。\n")
	writeDraft(t, dir, "my-post/intro", "第一段。\n\n短。短。短。\n")
	code, out := runCompute(t, dir, "my-post")
	if code != 0 || len(out.Units) != 2 || out.Units[0].Unit != "my-post" || out.Units[1].Unit != "my-post/intro" || len(out.ChangedPaths) != 0 ||
		out.Baselines.Profile != nil || out.Baselines.Threshold.Value != 2 || out.Baselines.Threshold.Source != "default" || len(out.Baselines.Accepted) != 0 {
		t.Fatalf("tree: %d %+v", code, out)
	}
	if out.Units[1].Paragraphs[1].Paragraph != 2 || out.Units[1].Paragraphs[1].Line != 3 || out.Units[1].Paragraphs[1].Sentences != 3 {
		t.Fatalf("paragraphs: %+v", out.Units[1].Paragraphs)
	}
	// 作者文风画像：句长均值 10、标准差 2。
	os.MkdirAll(filepath.Join(dir, ".writing-anything", "packs"), 0o755)
	writeFile(t, filepath.Join(dir, ".writing-anything", "packs", "mine.yaml"), "schema_version: 1\nid: mine\ntitle: 我的\nstyle_profile:\n  sources: [a1]\n  chars: 100\n"+
		"  sentences: {count: 10, mean: 10, median: 10, stdev: 2, p10: 8, p90: 12}\n  paragraphs: {count: 3, mean: 30, median: 30, stdev: 5, p10: 25, p90: 35}\n  punctuation: {}\n  terms: []\n")
	run(t, "config", "set", "packs", "mine", "--dir", dir)
	code, out = runCompute(t, dir, "my-post/intro")
	if code != 0 || out.Baselines.Profile == nil || out.Baselines.Profile.Source != "pack:mine" || len(out.Units) != 1 {
		t.Fatalf("profile: %d %+v", code, out)
	}
	var found []string
	for _, d := range out.Units[0].Drift {
		p := 0
		if d.Paragraph != nil {
			p = *d.Paragraph
		}
		found = append(found, d.Metric+":"+d.Baseline+":"+string(rune('0'+p)))
	}
	if !slices.Contains(found, "sentenceMean:profile:0") || !slices.Contains(found, "sentenceMean:profile:2") || !slices.Contains(found, "paragraphMean:profile:0") {
		t.Fatalf("drift = %v", found)
	}
	if code, stdout, _ := run(t, "metrics", "compute", "my-post/intro", "--dir", dir); code != 0 || !strings.Contains(stdout, "文风漂移（Style Drift）") || !strings.Contains(stdout, "不评判好坏") {
		t.Fatalf("text: %s", stdout)
	}
	// 阈值放宽到 10 个标准差：没有漂移。
	writeFile(t, filepath.Join(dir, ".writing-anything", "packs", "mine.yaml"), readFile(t, filepath.Join(dir, ".writing-anything", "packs", "mine.yaml"))+"drift_threshold: 10\n")
	if _, out := runCompute(t, dir, "my-post/intro"); len(out.Units[0].Drift) != 0 || out.Baselines.Threshold.Source != "pack:mine" {
		t.Fatalf("wide threshold: %+v", out.Units[0].Drift)
	}
	if code, out := runCompute(t, newWork(t), "my-post"); code != 1 || diag(t, out.result, "draft.not-found").Path != ".writing-anything/works/my-post/draft.md" {
		t.Fatalf("no draft: %d %+v", code, out)
	}
}

func TestMetricsComputeAcceptedBaseline(t *testing.T) {
	dir := newWork(t)
	for _, id := range []string{"a", "b", "c", "new"} {
		run(t, "unit", "add", "my-post", "section", id, "--dir", dir)
	}
	bodies := map[string]string{"a": "平静的叙述，没有对话。\n", "b": "依然平静地叙述下去，“嗯”。\n", "c": "叙述继续，没有人说话。\n"}
	for id, body := range bodies {
		writeDraft(t, dir, "my-post/"+id, body)
		writeFile(t, filepath.Join(dir, ".writing-anything", "works", "my-post", "units", id, "acceptance.yaml"),
			"schema_version: 1\ngate_level: fast\ndraft_sha256: "+metrics.Digest([]byte(body))+"\nconfirmed_by_author: false\naccepted_at: '2026-10-02T08:00:00Z'\n")
	}
	writeDraft(t, dir, "my-post/new", "他说：“我们走吧，现在就走。”她说：“好。”\n")
	code, out := runCompute(t, dir, "my-post/new")
	if code != 0 || len(out.Baselines.Accepted) != 3 {
		t.Fatalf("baseline: %d %+v", code, out.Baselines)
	}
	if !slices.ContainsFunc(out.Units[0].Drift, func(d stylemetric.Finding) bool { return d.Metric == "dialogueRatio" && d.Baseline == "accepted" }) {
		t.Fatalf("drift = %+v", out.Units[0].Drift)
	}
	// 漂移不阻塞：lint 的结果与发现项无关。
	lintCode, _, _ := run(t, "lint", "my-post/new", "--dir", dir)
	if lintCode != 0 {
		t.Fatalf("lint exit %d", lintCode)
	}
	// 不足 3 个已验收单元时不使用该基准。
	os.Remove(filepath.Join(dir, ".writing-anything", "works", "my-post", "units", "c", "acceptance.yaml"))
	if _, out := runCompute(t, dir, "my-post/new"); len(out.Baselines.Accepted) != 0 || len(out.Units[0].Drift) != 0 {
		t.Fatalf("too few: %+v %+v", out.Baselines, out.Units[0].Drift)
	}
}

func TestMetricsComputeBaselineFilters(t *testing.T) {
	dir := newWork(t)
	for _, id := range []string{"a", "b", "c", "d", "e", "new"} {
		run(t, "unit", "add", "my-post", "section", id, "--dir", dir)
	}
	accept := func(id, body, sha string) {
		writeDraft(t, dir, "my-post/"+id, body)
		writeFile(t, filepath.Join(dir, ".writing-anything", "works", "my-post", "units", id, "acceptance.yaml"),
			"schema_version: 1\ngate_level: fast\ndraft_sha256: "+sha+"\nconfirmed_by_author: false\naccepted_at: '2026-10-02T08:00:00Z'\n")
	}
	for _, id := range []string{"a", "b"} {
		accept(id, "已验收的"+id+"。\n", metrics.Digest([]byte("已验收的"+id+"。\n")))
	}
	// c 的验收记录与当前正文不一致，d 的验收记录不合法：都不作为基准。
	accept("c", "已验收的c。\n", metrics.Digest([]byte("别的正文")))
	accept("d", "已验收的d。\n", "")
	writeFile(t, filepath.Join(dir, ".writing-anything", "works", "my-post", "units", "d", "acceptance.yaml"), "gate_level: [")
	writeDraft(t, dir, "my-post/new", "新的一段。\n")
	if _, out := runCompute(t, dir, "my-post/new"); len(out.Baselines.Accepted) != 0 {
		t.Fatalf("invalid acceptances used: %v", out.Baselines.Accepted)
	}
	accept("e", "已验收的e。\n", metrics.Digest([]byte("已验收的e。\n")))
	if _, out := runCompute(t, dir, "my-post/new"); !slices.Equal(out.Baselines.Accepted, []string{"my-post/a", "my-post/b", "my-post/e"}) {
		t.Fatalf("baseline = %v", out.Baselines.Accepted)
	}
	// 计算范围内的已验收单元不作为自己的基准：从根单元计算时，子单元都在范围内。
	if _, out := runCompute(t, dir, "my-post"); len(out.Baselines.Accepted) != 0 || len(out.Units) != 6 {
		t.Fatalf("in-range accepted used: %v (%d units)", out.Baselines.Accepted, len(out.Units))
	}
}

func TestMetricsComputeProfileValuesAndNonBlocking(t *testing.T) {
	dir := reviewWork(t)
	os.MkdirAll(filepath.Join(dir, ".writing-anything", "packs"), 0o755)
	writeFile(t, filepath.Join(dir, ".writing-anything", "packs", "mine.yaml"), "schema_version: 1\nid: mine\ntitle: 我的\nstyle_profile:\n  sources: [a1]\n  chars: 100\n"+
		"  sentences: {count: 10, mean: 1, median: 1, stdev: 0.1, p10: 1, p90: 1}\n  paragraphs: {count: 3, mean: 1, median: 1, stdev: 0.1, p10: 1, p90: 1}\n  punctuation: {}\n  terms: []\n")
	run(t, "config", "set", "packs", "default,mine", "--dir", dir)
	run(t, "config", "set", "gate_level", "fast", "--dir", dir)
	code, out := runCompute(t, dir, "my-post")
	if code != 0 || len(out.Units[0].Drift) == 0 {
		t.Fatalf("drift: %d %+v", code, out)
	}
	d := out.Units[0].Drift[0]
	if d.Metric != "sentenceMean" || d.Baseline != "profile" || d.Value != out.Units[0].Metrics.SentenceMean || d.Mean != 1 || d.Stdev != 0.1 || d.Threshold != 2 || d.Paragraph != nil {
		t.Fatalf("finding = %+v", d)
	}
	// 漂移不阻塞：lint 与 accept 照常。
	lintCode, _, _ := run(t, "lint", "my-post", "--dir", dir)
	acceptCode, stdout, _ := run(t, "accept", "my-post", "--dir", dir, "--json")
	if lintCode != 0 || acceptCode != 0 {
		t.Fatalf("lint %d, accept %d: %s", lintCode, acceptCode, stdout)
	}
}
