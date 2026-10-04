package cli

import (
	"os"
	"path/filepath"
	"testing"
)

// seriesFixture：系列 guide 两部 short-story 作品，book-1 场景 done，book-2 场景推进到审稿。
func seriesFixture(t *testing.T) string {
	t.Helper()
	dir := newWorkspace(t)
	seriesMkdir(t, filepath.Join(dir, ".writing-anything", "series"))
	writeFile(t, filepath.Join(dir, ".writing-anything", "series", "guide.yaml"),
		"schema_version: 1\nid: guide\ntitle: 指南\nworks: [book-1, book-2]\npacks: []\nsources: []\nfacts: []\n")
	for _, id := range []string{"book-1", "book-2"} {
		run(t, "new", "short-story", id, "--dir", dir)
		run(t, "unit", "add", id, "scene", "s1", "--dir", dir)
		s1 := filepath.Join(dir, ".writing-anything", "works", id, "units", "s1")
		seriesMkdir(t, s1)
		writeFile(t, filepath.Join(s1, "brief.yaml"), "schema_version: 1\n")
		run(t, "advance", id+"/s1", "--dir", dir)
	}
	b1 := filepath.Join(dir, ".writing-anything", "works", "book-1")
	b1s1 := filepath.Join(b1, "units", "s1")
	writeFile(t, filepath.Join(b1, "canon.yaml"),
		"schema_version: 1\nentries:\n  - {id: canon-1, type: character, name: 守灯人, text: 系列, sources: [], protection: normal, scope: work}\n")
	writeFile(t, filepath.Join(b1s1, "outline.yaml"),
		"schema_version: 1\nsections:\n  - {id: sc1, title: 场景, reason: 推进, pov: 守灯人, characters: [守灯人], time: 第一日, events: [ev-1]}\n")
	writeFile(t, filepath.Join(b1s1, "draft.md"), "正文。\n")
	run(t, "advance", "book-1/s1", "--dir", dir)
	run(t, "review", "record", "book-1/s1", "--file", fictionReviewInput(t, dir), "--dir", dir)
	run(t, "accept", "book-1/s1", "--confirmed-by-author", "--confirmation", "作者：可以", "--dir", dir)
	run(t, "advance", "book-1/s1", "--dir", dir)
	b2 := filepath.Join(dir, ".writing-anything", "works", "book-2", "units", "s1")
	writeFile(t, filepath.Join(b2, "outline.yaml"),
		"schema_version: 1\nsections:\n  - {id: sc1, title: 场景, reason: 推进, pov: 守灯人, characters: [守灯人], time: 第一日}\n")
	writeFile(t, filepath.Join(b2, "draft.md"), "正文。\n")
	run(t, "advance", "book-2/s1", "--dir", dir)
	return dir
}

func seriesMkdir(t *testing.T, path string) {
	t.Helper()
	if err := os.MkdirAll(path, 0o755); err != nil {
		t.Fatal(err)
	}
}

// 完成标准的 CLI 面：第二部冲突场景（同 time 双出场）被 continuity 阻塞，修正后通过。
func TestSeriesContinuityCLI(t *testing.T) {
	dir := seriesFixture(t)
	code, res := runWS(t, dir, "continuity", "guide")
	if code != 0 {
		t.Fatalf("continuity guide: exit code = %d, result = %+v", code, res)
	}
	if !contains(res.stdout, "timeline.same-time-presence") {
		t.Fatalf("findings = %s", res.stdout)
	}
	// 修正：book-2 声明晚于 book-1/s1。
	b2s1 := filepath.Join(dir, ".writing-anything", "works", "book-2", "units", "s1")
	writeFile(t, filepath.Join(b2s1, "outline.yaml"),
		"schema_version: 1\nsections:\n  - {id: sc1, title: 场景, reason: 推进, pov: 守灯人, characters: [守灯人], time: 第一日, after: [book-1/s1]}\n")
	if code, res = runWS(t, dir, "continuity", "guide"); code != 0 || contains(res.stdout, "timeline.") {
		t.Fatalf("after fix: exit code = %d, stdout = %s", code, res.stdout)
	}
}

// review plan 的系列级包：跨作品 character 包与 series-causality 维度。
func TestSeriesReviewPlan(t *testing.T) {
	dir := seriesFixture(t)
	code, res := runWS(t, dir, "review", "plan", "guide", "--dimension", "series-causality", "--dir", dir)
	if code != 0 {
		t.Fatalf("plan series-causality: exit code = %d, result = %+v", code, res)
	}
	// book-2/s1 引用 book-1 声明的 ev-1？夹具未声明 causes——先补一条再断言。
	b2s1 := filepath.Join(dir, ".writing-anything", "works", "book-2", "units", "s1")
	writeFile(t, filepath.Join(b2s1, "outline.yaml"),
		"schema_version: 1\nsections:\n  - {id: sc1, title: 场景, reason: 推进, pov: 守灯人, characters: [守灯人], causes: [ev-1], after: [book-1/s1]}\n")
	if code, res = runWS(t, dir, "review", "plan", "guide", "--dimension", "series-causality", "--dir", dir); code != 0 {
		t.Fatalf("plan: exit code = %d, result = %+v", code, res)
	}
	if !contains(res.stdout, "series-causality-1") || !contains(res.stdout, "book-2/s1") {
		t.Fatalf("packets = %s", res.stdout)
	}
	// 跨作品 character 包：guide 目录下 character 包（两章合并）。
	if code, res = runWS(t, dir, "review", "plan", "guide", "--dimension", "character", "--dir", dir); code != 0 {
		t.Fatalf("plan character: exit code = %d", code)
	}
}

// assemble 的系列关卡：另一作品的 open 复查标记阻塞本作品组稿。
func TestAssembleSeriesGate(t *testing.T) {
	dir := seriesFixture(t)
	// book-1 手写一条 open 复查标记。
	rechecks := filepath.Join(dir, ".writing-anything", "rechecks")
	seriesMkdir(t, rechecks)
	writeFile(t, filepath.Join(rechecks, "book-1.yaml"),
		"schema_version: 1\nscenes:\n  - {scene: book-1/s1, target: canon-1, via: character, reason: 设定被修改, marked_at: \"2026-10-04T08:00:00Z\", status: open}\n")
	// book-2 走完生命周期到 done（补作品级设定卡与晚于声明，清掉单作品连续性发现）。
	b2 := filepath.Join(dir, ".writing-anything", "works", "book-2")
	writeFile(t, filepath.Join(b2, "canon.yaml"),
		"schema_version: 1\nentries:\n  - {id: canon-1, type: character, name: 守灯人, text: 系列, sources: [], protection: normal, scope: work, override: {reason: 时间线分歧, confirmation: 作者确认, confirmed_at: \"2026-10-04T08:00:00Z\"}}\n")
	writeFile(t, filepath.Join(b2, "units", "s1", "outline.yaml"),
		"schema_version: 1\nsections:\n  - {id: sc1, title: 场景, reason: 推进, pov: 守灯人, characters: [守灯人], time: 第一日, after: [book-1/s1]}\n")
	run(t, "advance", "book-2/s1", "--dir", dir) // draft → review
	run(t, "review", "record", "book-2/s1", "--file", fictionReviewInput(t, dir), "--dir", dir)
	run(t, "accept", "book-2/s1", "--confirmed-by-author", "--confirmation", "作者：可以", "--dir", dir)
	run(t, "advance", "book-2/s1", "--dir", dir)
	code, res := runWS(t, dir, "assemble", "book-2")
	if code != 1 {
		t.Fatalf("assemble: exit code = %d, result = %+v", code, res)
	}
	d := diag(t, res, "assemble.rechecks")
	if d.Code == "" || !contains(d.Message, "book-1/s1") {
		t.Fatalf("diagnostic = %+v", d)
	}
}

// fictionReviewInput 返回带 short-story 全部评分结论的审稿输入文件路径。
func fictionReviewInput(t *testing.T, dir string) string {
	t.Helper()
	p := filepath.Join(dir, "fiction-ri.yaml")
	writeFile(t, p, "rubric:\n  - {item: conflict, verdict: pass, evidence: 有冲突, paragraphs: [1]}\n  - {item: pacing, verdict: pass, evidence: 节奏稳, paragraphs: [1]}\n  - {item: motivation, verdict: pass, evidence: 动机可信, paragraphs: [1]}\nfindings: []\n")
	return p
}
