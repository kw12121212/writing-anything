package cli

import (
	"os"
	"path/filepath"
	"slices"
	"strings"
	"testing"
)

// seriesFixture：系列 guide 两部 short-story 作品，book-1 场景验收并推进到 done，book-2 场景推进到 draft（大纲与
// book-1/s1 声明相同 time 且都出场守灯人）。每一步都断言成功，夹具状态即注释所述状态。
func seriesFixture(t *testing.T) string {
	t.Helper()
	dir := newWorkspace(t)
	for _, id := range []string{"book-1", "book-2"} {
		mustRunWS(t, dir, "new", "short-story", id)
		mustRunWS(t, dir, "unit", "add", id, "scene", "s1")
	}
	mustRunWS(t, dir, "series", "set", "guide", "--title", "指南")
	mustRunWS(t, dir, "series", "add", "guide", "book-1")
	mustRunWS(t, dir, "series", "add", "guide", "book-2")
	for _, id := range []string{"book-1", "book-2"} {
		s1 := filepath.Join(dir, ".writing-anything", "works", id, "units", "s1")
		seriesMkdir(t, s1)
		writeFile(t, filepath.Join(s1, "brief.yaml"), "schema_version: 1\n")
		mustRunWS(t, dir, "advance", id+"/s1")
	}
	b1 := filepath.Join(dir, ".writing-anything", "works", "book-1")
	b1s1 := filepath.Join(b1, "units", "s1")
	writeFile(t, filepath.Join(b1, "canon.yaml"),
		"schema_version: 1\nentries:\n  - {id: canon-1, type: character, name: 守灯人, text: 系列, sources: [], protection: normal, scope: work}\n")
	writeFile(t, filepath.Join(b1s1, "outline.yaml"),
		"schema_version: 1\nsections:\n  - {id: sc1, title: 场景, reason: 推进, pov: 守灯人, characters: [守灯人], time: 第一日, events: [ev-1]}\n")
	writeFile(t, filepath.Join(b1s1, "draft.md"), "正文。\n")
	mustRunWS(t, dir, "advance", "book-1/s1") // outline → draft
	seriesCloseOut(t, dir, "book-1/s1", "events: [ev-1]\n")
	mustRunWS(t, dir, "advance", "book-1/s1") // draft → review
	mustRunWS(t, dir, "review", "record", "book-1/s1", "--file", fictionReviewInput(t, dir))
	mustRunWS(t, dir, "accept", "book-1/s1", "--confirmed-by-author", "--confirmation", "作者：可以")
	mustRunWS(t, dir, "advance", "book-1/s1") // review → done
	b2 := filepath.Join(dir, ".writing-anything", "works", "book-2")
	writeFile(t, filepath.Join(b2, "canon.yaml"),
		"schema_version: 1\nentries:\n  - {id: canon-1, type: character, name: 守灯人, text: 系列, sources: [], protection: normal, scope: work}\n")
	writeFile(t, filepath.Join(b2, "units", "s1", "outline.yaml"),
		"schema_version: 1\nsections:\n  - {id: sc1, title: 场景, reason: 推进, pov: 守灯人, characters: [守灯人], time: 第一日}\n")
	writeFile(t, filepath.Join(b2, "units", "s1", "draft.md"), "正文。\n")
	mustRunWS(t, dir, "advance", "book-2/s1") // outline → draft
	seriesCloseOut(t, dir, "book-2/s1", "{}\n")
	if code, res := runWS(t, dir, "next", "book-1/s1"); code != 0 || !contains(res.stdout, `"stage":"done"`) {
		t.Fatalf("next book-1/s1: exit code = %d, stdout = %s", code, res.stdout)
	}
	return dir
}

// seriesCloseOut 以抽取结果 payload 为场景提交写后收口（Close-out）并断言成功。
func seriesCloseOut(t *testing.T, dir, ref, payload string) {
	t.Helper()
	if code, res := runWSIn(t, dir, payload, "scene", "close-out", ref); code != 0 {
		t.Fatalf("close-out %s: exit code = %d, stdout = %s", ref, code, res.stdout)
	}
}

// seriesReclose 在修改契约后以空抽取结果整体替换场景的收口记录。
func seriesReclose(t *testing.T, dir, ref string) {
	t.Helper()
	if code, res := runWSIn(t, dir, "{}\n", "scene", "close-out", ref, "--replace"); code != 0 {
		t.Fatalf("close-out --replace %s: exit code = %d, stdout = %s", ref, code, res.stdout)
	}
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
	seriesReclose(t, dir, "book-2/s1")
	mustRunWS(t, dir, "advance", "book-2/s1") // draft → review
	mustRunWS(t, dir, "review", "record", "book-2/s1", "--file", fictionReviewInput(t, dir))
	mustRunWS(t, dir, "accept", "book-2/s1", "--confirmed-by-author", "--confirmation", "作者：可以")
	mustRunWS(t, dir, "advance", "book-2/s1")
	code, res := runWS(t, dir, "assemble", "book-2")
	if code != 1 {
		t.Fatalf("assemble: exit code = %d, result = %+v", code, res)
	}
	d := diag(t, res, "assemble.rechecks")
	if d.Code == "" || !contains(d.Message, "book-1/s1") {
		t.Fatalf("diagnostic = %+v", d)
	}
}

// seriesReviewBook2 把 book-2/s1 推进到审稿并记录审稿结论。
func seriesReviewBook2(t *testing.T, dir string) {
	t.Helper()
	mustRunWS(t, dir, "advance", "book-2/s1") // draft → review
	mustRunWS(t, dir, "review", "record", "book-2/s1", "--file", fictionReviewInput(t, dir))
}

// 完成标准：book-2 场景与 book-1 同一时间两地时 accept 以非零退出并列出发现（review show 只读呈现同一阻塞项）；
// 修正时间声明后验收成功。
func TestSeriesAcceptGateSameTime(t *testing.T) {
	dir := seriesFixture(t)
	seriesReviewBook2(t, dir)
	decisions := filepath.Join(dir, ".writing-anything", "decisions.yaml")
	code, res := runWS(t, dir, "review", "show", "book-2/s1")
	if code != 0 || !contains(res.stdout, "timeline.same-time-presence") || len(res.ChangedPaths) != 0 {
		t.Fatalf("review show: exit code = %d, stdout = %s", code, res.stdout)
	}
	if _, err := os.Stat(decisions); err == nil {
		t.Fatal("review show created decisions.yaml")
	}
	code, res = runWS(t, dir, "accept", "book-2/s1", "--confirmed-by-author", "--confirmation", "作者：可以")
	if code != 1 {
		t.Fatalf("accept: exit code = %d, stdout = %s", code, res.stdout)
	}
	d := diag(t, res, "timeline.same-time-presence")
	if !contains(d.Message, "book-1/s1") || !contains(d.Message, "book-2/s1") || !contains(d.Message, "[系列]") {
		t.Fatalf("diagnostic = %+v", d)
	}
	if strings.Count(d.Message, "补充 after") != 1 {
		t.Fatalf("duplicated hint: %s", d.Message)
	}
	if !slices.Contains(res.ChangedPaths, ".writing-anything/decisions.yaml") {
		t.Fatalf("changedPaths = %v; %s", res.ChangedPaths, res.stdout)
	}
	// 修正：book-2 声明晚于 book-1/s1，重新收口与审稿后验收成功。
	writeFile(t, filepath.Join(dir, ".writing-anything", "works", "book-2", "units", "s1", "outline.yaml"),
		"schema_version: 1\nsections:\n  - {id: sc1, title: 场景, reason: 推进, pov: 守灯人, characters: [守灯人], time: 第一日, after: [book-1/s1]}\n")
	seriesReclose(t, dir, "book-2/s1")
	mustRunWS(t, dir, "review", "record", "book-2/s1", "--file", fictionReviewInput(t, dir))
	mustRunWS(t, dir, "accept", "book-2/s1", "--confirmed-by-author", "--confirmation", "作者：可以")
}

// 完成标准：book-1 已确认守灯人死亡、book-2 场景守灯人仍出场时，continuity <series> 报告 error 级 dead-acting，
// accept 该场景以非零退出；作者经决策队列允许豁免后验收成功。
func TestSeriesAcceptGateDeadActing(t *testing.T) {
	dir := seriesFixture(t)
	writeFile(t, filepath.Join(dir, ".writing-anything", "works", "book-1", "state.yaml"),
		"schema_version: 1\nevents:\n  - {id: state-1, scene: book-1/s1, entry: canon-1, state: 死亡, status: confirmed, confirmation: 作者确认, decided_at: \"2026-10-04T08:00:00Z\"}\n")
	writeFile(t, filepath.Join(dir, ".writing-anything", "works", "book-2", "units", "s1", "outline.yaml"),
		"schema_version: 1\nsections:\n  - {id: sc1, title: 场景, reason: 推进, pov: 守灯人, characters: [守灯人], time: 第一日, after: [book-1/s1]}\n")
	seriesReclose(t, dir, "book-2/s1")
	code, res := runWS(t, dir, "continuity", "guide")
	if code != 0 || !contains(res.stdout, "continuity.dead-acting") || !contains(res.stdout, `"scene":"book-2/s1"`) {
		t.Fatalf("continuity guide: exit code = %d, stdout = %s", code, res.stdout)
	}
	// 单作品检查看不到前作的状态：book-2 自身的 continuity 不报。
	if code, res = runWS(t, dir, "continuity", "book-2"); code != 0 || contains(res.stdout, "dead-acting") {
		t.Fatalf("continuity book-2: exit code = %d, stdout = %s", code, res.stdout)
	}
	// 更早作品的组稿仍列出该跨作品发现（不因组稿去重而丢失；它不阻塞 book-1 场景的验收）。
	if code, res = runWS(t, dir, "assemble", "book-1"); code != 1 || !contains(res.stdout, "continuity.dead-acting") {
		t.Fatalf("assemble book-1: exit code = %d, stdout = %s", code, res.stdout)
	}
	seriesReviewBook2(t, dir)
	code, res = runWS(t, dir, "accept", "book-2/s1", "--confirmed-by-author", "--confirmation", "作者：可以")
	if code != 1 {
		t.Fatalf("accept: exit code = %d, stdout = %s", code, res.stdout)
	}
	if d := diag(t, res, "continuity.dead-acting"); !contains(d.Message, "book-1") || !contains(d.Message, "死亡") {
		t.Fatalf("diagnostic = %+v", d)
	}
	// 幂等：再次被拒不追加决策。
	if code, res = runWS(t, dir, "accept", "book-2/s1", "--confirmed-by-author", "--confirmation", "作者：可以"); code != 1 || len(res.ChangedPaths) != 0 {
		t.Fatalf("second accept: exit code = %d, changedPaths = %v", code, res.ChangedPaths)
	}
	mustRunWS(t, dir, "decide", "dec-1", "--option", "a", "--confirmation", "作者允许这次豁免")
	mustRunWS(t, dir, "accept", "book-2/s1", "--confirmed-by-author", "--confirmation", "作者：可以")
	// 组稿关卡尊重同一豁免：已豁免的跨作品发现不再以系列范围发现重复阻塞。
	mustRunWS(t, dir, "advance", "book-2/s1")
	if code, res = runWS(t, dir, "assemble", "book-2"); code != 0 {
		t.Fatalf("assemble: exit code = %d, stdout = %s", code, res.stdout)
	}
}

// fictionReviewInput 返回带 short-story 全部评分结论的审稿输入文件路径。
func fictionReviewInput(t *testing.T, dir string) string {
	t.Helper()
	p := filepath.Join(dir, "fiction-ri.yaml")
	writeFile(t, p, "rubric:\n  - {item: conflict, verdict: pass, evidence: 有冲突, paragraphs: [1]}\n  - {item: pacing, verdict: pass, evidence: 节奏稳, paragraphs: [1]}\n  - {item: motivation, verdict: pass, evidence: 动机可信, paragraphs: [1]}\nfindings: []\n")
	return p
}
