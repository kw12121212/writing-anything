package cli

import (
	"encoding/json"
	"os"
	"path/filepath"
	"regexp"
	"strings"
	"testing"
	"time"
	"unicode"
	"unicode/utf8"

	"go.yaml.in/yaml/v3"
)

// 系列小说端到端（series-e2e）语料《雾港灯火》：系列 harbor 含两部 novel 作品——第一部《雾港》（fog-harbor）与
// 第二部《归帆》（homeward），各一部一章两个场景。重放、黄金比对与矩阵对账复用小说语料的助手
// （replayCorpus、compareCorpusGoldens、reconcileCorpusMatrix），设计与维护流程见 testdata/e2e/series/README.md。

var seriesCorpusDir = filepath.Join("testdata", "e2e", "series")

// seriesCorpusFrozen 是系列语料重放的冻结时间源（与小说语料相同的时刻）。
var seriesCorpusFrozen = time.Date(2026, 6, 20, 12, 0, 0, 0, time.UTC)

// seriesCorpusLastAccept 是第二部最后一个场景验收步骤的命令串前缀：负向注入重放到它之前停下。
const seriesCorpusLastAccept = "accept homeward/s2 "

// freezeSeriesClock 冻结包级时钟 now，测试结束时恢复。
func freezeSeriesClock(t *testing.T) {
	t.Helper()
	now = func() time.Time { return seriesCorpusFrozen }
	t.Cleanup(func() { now = time.Now })
}

// replaySeriesCorpus 在 dir 中重放系列语料全部步骤（见 replayCorpus），并断言系列语料的规模：四个场景都收口。
func replaySeriesCorpus(t *testing.T, dir string) {
	t.Helper()
	stats := replayCorpus(t, dir, seriesCorpusDir, "")
	if stats.closeouts != 4 {
		t.Fatalf("成功的写后收口步数 = %d，系列语料四个场景都应收口", stats.closeouts)
	}
	if stats.steps < 100 {
		t.Fatalf("重放步数 = %d，系列语料应不少于 100 步（与 TestSeriesCorpusScript 的下限互指）", stats.steps)
	}
}

// 系列语料命令脚本的结构校验：阶段齐备、步骤字段语义合法、input_ref 指向检入文件、规模符合设计（两部作品各
// 两个场景，每场正文 1000–1500 个非空白字符）、系列命令面与真实负向步骤在位。
func TestSeriesCorpusScript(t *testing.T) {
	raw, err := os.ReadFile(filepath.Join(seriesCorpusDir, "commands.yaml"))
	if err != nil {
		t.Fatal(err)
	}
	var script corpusScript
	if err := yaml.Unmarshal(raw, &script); err != nil {
		t.Fatalf("commands.yaml 解析失败: %v", err)
	}
	wantStages := []string{"init", "sources", "series", "fog-harbor", "homeward", "series-wrap"}
	var gotStages []string
	for _, st := range script.Stages {
		gotStages = append(gotStages, st.Stage)
	}
	if strings.Join(gotStages, ",") != strings.Join(wantStages, ",") {
		t.Fatalf("阶段序列 = %v，期望 %v", gotStages, wantStages)
	}

	negative := map[string]bool{"canon.override-unconfirmed": false}
	verbs := map[string]int{}
	steps, sceneUnits, members, lastAccept := 0, 0, 0, 0
	for _, st := range script.Stages {
		if strings.TrimSpace(st.Note) == "" {
			t.Errorf("阶段 %s 缺少说明", st.Stage)
		}
		for _, s := range st.Steps {
			steps++
			if s.Command == nil || len(s.Command) == 0 || s.WriteFile != nil || s.AuthorReview != nil {
				t.Errorf("阶段 %s 第 %d 步：系列语料只录 wa-cli 命令步骤", st.Stage, steps)
				continue
			}
			label := strings.Join(s.Command, " ")
			verbs[s.Command[0]]++
			if s.ExpectExit != 0 && (s.ExpectCode == nil || *s.ExpectCode == "") {
				t.Errorf("%s：失败步骤（exit=%d）必须声明 expect_code", label, s.ExpectExit)
			}
			if s.ExpectExit != 0 && s.ExpectExit != 1 && s.ExpectExit != 2 {
				t.Errorf("%s：expect_exit = %d，只允许 0/1/2", label, s.ExpectExit)
			}
			if s.ExpectCode != nil && *s.ExpectCode != "" {
				for _, code := range strings.Split(*s.ExpectCode, ",") {
					if _, ok := negative[code]; !ok {
						t.Errorf("%s：未登记的负向步骤码 %s", label, code)
					}
					negative[code] = true
				}
			}
			if s.InputRef != nil && *s.InputRef != "" {
				if _, err := os.Stat(filepath.Join(seriesCorpusDir, *s.InputRef)); err != nil {
					t.Errorf("input_ref %q 指向的文件不存在", *s.InputRef)
				}
			}
			if len(s.Command) == 5 && s.Command[0] == "unit" && s.Command[3] == "scene" {
				sceneUnits++
			}
			if len(s.Command) == 4 && s.Command[0] == "series" && s.Command[1] == "add" {
				members++
			}
			if strings.HasPrefix(label, seriesCorpusLastAccept) {
				lastAccept++
			}
		}
	}
	for code, seen := range negative {
		if !seen {
			t.Errorf("脚本缺少真实负向步骤 %s", code)
		}
	}
	if steps < 100 {
		t.Errorf("步骤总数 = %d，期望两部作品完整生命周期不少于 100 步", steps)
	}
	if sceneUnits != 4 || members != 2 {
		t.Errorf("场景单元 = %d、系列成员 = %d，期望两部作品各两个场景", sceneUnits, members)
	}
	if lastAccept != 1 {
		t.Errorf("以 %q 开头的验收步骤 = %d，负向注入的停止点必须唯一", seriesCorpusLastAccept, lastAccept)
	}
	for _, verb := range []string{"init", "new", "source", "series", "canon", "brief", "unit", "scene", "outline", "draft", "lint",
		"review", "accept", "advance", "state", "summary", "volume", "handoff", "timeline", "context", "continuity",
		"assemble", "export", "validate"} {
		if verbs[verb] == 0 {
			t.Errorf("脚本没有用到命令 %q", verb)
		}
	}
	for _, scene := range []string{"fog-s1", "fog-s2", "home-s1", "home-s2"} {
		data, err := os.ReadFile(filepath.Join(seriesCorpusDir, "inputs", scene+".md"))
		if err != nil {
			t.Fatal(err)
		}
		n := utf8.RuneCountInString(strings.Map(func(r rune) rune {
			if unicode.IsSpace(r) {
				return -1
			}
			return r
		}, string(data)))
		if n < 1000 || n > 1500 {
			t.Errorf("场景 %s 正文 %d 个非空白字符，超出设计区间 [1000, 1500]", scene, n)
		}
	}
}

// 系列语料重放：冻结时间源，连续重放两次，两棵工作区目录树逐文件 SHA-256 一致（含系列文件、系列设定集、
// 带覆盖记录的作品设定集与两部作品的组稿产物）；覆盖记录的 confirmed_at 取冻结时钟。
func TestSeriesCorpusReplay(t *testing.T) {
	freezeSeriesClock(t)
	dir1 := t.TempDir()
	replaySeriesCorpus(t, dir1)
	dir2 := t.TempDir()
	replaySeriesCorpus(t, dir2)
	assertSameTree(t, dir1, dir2)
	for _, out := range []string{"fog-harbor.md", "homeward.md", "homeward-export.md"} {
		if _, err := os.Stat(filepath.Join(dir1, out)); err != nil {
			t.Fatalf("组稿或导出产物 %s 缺失: %v", out, err)
		}
	}
	data, err := os.ReadFile(filepath.Join(dir1, ".writing-anything", "works", "homeward", "canon.yaml"))
	if err != nil || !strings.Contains(string(data), `confirmed_at: "2026-06-20T12:00:00Z"`) {
		t.Fatalf("覆盖记录应带冻结时钟的 confirmed_at: %s (%v)", data, err)
	}
}

// 系列语料黄金文件：重放后采集系列文件、系列与作品设定集、解析视图、系列视图、全局时间线、跨作品连续性、
// 第二部场景上下文包与两部作品的组稿与导出，与 testdata/e2e/series/golden/ 逐字节比对；UPDATE_GOLDEN=1 一键再生成。
func TestSeriesCorpusGolden(t *testing.T) {
	freezeSeriesClock(t)
	dir := t.TempDir()
	replaySeriesCorpus(t, dir)
	ws := filepath.Join(dir, ".writing-anything")
	goldens := []corpusGolden{
		{name: "harbor.yaml", path: filepath.Join(ws, "series", "harbor.yaml")},
		{name: "series-canon.yaml", path: filepath.Join(ws, "series", "harbor", "canon.yaml")},
		{name: "homeward-canon.yaml", path: filepath.Join(ws, "works", "homeward", "canon.yaml")},
		{name: "canon-list-homeward.json", cmd: []string{"canon", "list", "homeward"}},
		{name: "series-show.json", cmd: []string{"series", "show", "harbor"}},
		{name: "timeline.json", cmd: []string{"timeline", "harbor"}},
		{name: "continuity.json", cmd: []string{"continuity", "harbor"}},
		{name: "context-homeward-s2.json", cmd: []string{"context", "homeward/s2"}},
		{name: "fog-harbor.md", path: filepath.Join(dir, "fog-harbor.md")},
		{name: "homeward.md", path: filepath.Join(dir, "homeward.md")},
		{name: "homeward-export.md", path: filepath.Join(dir, "homeward-export.md")},
	}
	compareCorpusGoldens(t, dir, filepath.Join(seriesCorpusDir, "golden"), goldens, "TestSeriesCorpusGolden")
}

// 跨作品违例的负向注入：重放到第二部最后一个场景（homeward/s2，处于审稿阶段）验收之前，逐类注入——
// (1) 跨作品设定冲突：第一部已确认亡故的陆山出现在第二部场景 → continuity.dead-acting；
// (2) 跨作品设定冲突：第二部把陆山确认为另一终态 → continuity.conflicting-state；
// (3) 跨作品时间矛盾：第二部场景与第一部场景同一故事时间、阿宁两处出场 → timeline.same-time-presence；
// 三类都断言 wa-cli continuity harbor 报告 error 级发现、wa-cli accept homeward/s2 以退出码 1 拒绝并列出该错误码，
// 修回合规后全绿。(4) 全局时间线成环：第一部首场声明晚于第二部末场 → timeline harbor 报 timeline.cycle 与
// timeline.order-conflict，连续性审查携带 error 级发现，修回后全绿。最后验收成功。
func TestSeriesCorpusNegativeInjections(t *testing.T) {
	freezeSeriesClock(t)
	dir := t.TempDir()
	replayCorpus(t, dir, seriesCorpusDir, seriesCorpusLastAccept)

	// findings 返回系列范围连续性审查中未豁免发现项的错误码 → severity（同码多条时任一非 error 即记录该 severity）。
	findings := func() map[string]string {
		t.Helper()
		code, res := runWS(t, dir, "continuity", "harbor")
		if code != 0 {
			t.Fatalf("continuity harbor: exit = %d, diagnostics = %+v", code, res.Diagnostics)
		}
		var out struct {
			Findings []struct {
				Code     string `json:"code"`
				Severity string `json:"severity"`
				Waived   bool   `json:"waived"`
			} `json:"findings"`
		}
		if err := json.Unmarshal([]byte(res.stdout), &out); err != nil {
			t.Fatalf("continuity 输出解析失败: %v; %s", err, res.stdout)
		}
		codes := map[string]string{}
		for _, f := range out.Findings {
			if f.Waived {
				continue
			}
			if old, seen := codes[f.Code]; !seen || old == "error" {
				codes[f.Code] = f.Severity
			}
		}
		return codes
	}
	// accept 尝试验收 homeward/s2，返回退出码与结果（不断言成功）。
	accept := func() (int, result) {
		t.Helper()
		return runWS(t, dir, "accept", "homeward/s2", "--confirmed-by-author", "--confirmation", "作者：日志一场收。")
	}
	must := func(args ...string) {
		t.Helper()
		if code, res := runWS(t, dir, args...); code != 0 {
			t.Fatalf("%v: exit = %d, diagnostics = %+v", args, code, res.Diagnostics)
		}
	}
	green := func(after string) {
		t.Helper()
		if codes := findings(); len(codes) > 0 {
			t.Fatalf("%s后系列连续性应全绿，实际 %v", after, codes)
		}
	}
	green("基线")

	// (1) 跨作品已死人物行动。
	must("scene", "contract", "homeward/s2", "sc", "--characters", "阿宁,陆归帆,陆山")
	if codes := findings(); codes["continuity.dead-acting"] != "error" || len(codes) != 1 {
		t.Fatalf("跨作品已死人物出场应只报 error 级 continuity.dead-acting，实际 %v", codes)
	}
	if code, res := accept(); code != 1 || !diagError(res, "continuity.dead-acting") {
		t.Fatalf("跨作品已死人物出场应阻塞验收: exit = %d, diagnostics = %+v", code, res.Diagnostics)
	} else if d := diag(t, res, "continuity.dead-acting"); !strings.Contains(d.Message, "[系列]") || !strings.Contains(d.Message, "fog-harbor/s2") {
		t.Fatalf("已死人物发现应来自第一部的已确认终态: %+v", d)
	}
	must("scene", "contract", "homeward/s2", "sc", "--characters", "阿宁,陆归帆")
	green("移除已故人物")

	// (2) 跨作品状态矛盾。
	must("state", "propose", "homeward", "--scene", "homeward/s2", "--entry", "char-lu-shan", "--state", "失踪", "--id", "st-x")
	must("state", "confirm", "homeward", "st-x", "--confirmed-by-author", "--confirmation", "作者：确认陆山失踪。")
	if codes := findings(); codes["continuity.conflicting-state"] != "error" || len(codes) != 1 {
		t.Fatalf("跨作品状态矛盾应只报 error 级 continuity.conflicting-state，实际 %v", codes)
	}
	if code, res := accept(); code != 1 || !diagError(res, "continuity.conflicting-state") {
		t.Fatalf("跨作品状态矛盾应阻塞验收: exit = %d, diagnostics = %+v", code, res.Diagnostics)
	} else if d := diag(t, res, "continuity.conflicting-state"); !strings.Contains(d.Message, "[系列]") ||
		!strings.Contains(d.Message, "fog-harbor/s2") || !strings.Contains(d.Message, "homeward/s2") {
		t.Fatalf("状态矛盾发现应列出两部作品的场景: %+v", d)
	}
	must("state", "reject", "homeward", "st-x", "--confirmed-by-author", "--confirmation", "作者：撤销，陆山在第一部已亡故。")
	green("撤销矛盾状态")

	// (3) 跨作品同一时间两地。
	must("scene", "contract", "homeward/s2", "sc", "--time", "1998-10-04 黎明", "--after", "")
	if codes := findings(); codes["timeline.same-time-presence"] != "error" || len(codes) != 1 {
		t.Fatalf("跨作品同一时间两地应只报 error 级 timeline.same-time-presence，实际 %v", codes)
	}
	code, res := accept()
	if code != 1 || !diagError(res, "timeline.same-time-presence") {
		t.Fatalf("跨作品同一时间两地应阻塞验收: exit = %d, diagnostics = %+v", code, res.Diagnostics)
	}
	if d := diag(t, res, "timeline.same-time-presence"); !strings.Contains(d.Message, "[系列]") ||
		!strings.Contains(d.Message, "fog-harbor/s2") || !strings.Contains(d.Message, "homeward/s2") {
		t.Fatalf("跨作品发现应标明系列与两部作品的场景: %+v", d)
	}
	must("scene", "contract", "homeward/s2", "sc", "--time", "2008-10-04 黎明", "--after", "homeward/s1")
	green("恢复时间声明")

	// (4) 全局时间线成环。
	must("scene", "contract", "fog-harbor/s1", "sc", "--after", "homeward/s2")
	if code, res := runWS(t, dir, "timeline", "harbor"); code != 1 || !diagError(res, "timeline.cycle") || !diagError(res, "timeline.order-conflict") {
		t.Fatalf("全局时间线成环: exit = %d, diagnostics = %+v", code, res.Diagnostics)
	}
	if codes := findings(); codes["timeline.cycle"] != "error" || codes["timeline.order-conflict"] != "error" {
		t.Fatalf("系列连续性审查应携带时间线成环发现，实际 %v", codes)
	}
	must("scene", "contract", "fog-harbor/s1", "sc", "--after", "")
	green("删除成环的 after")

	// 修回合规后验收成功。
	if code, res := accept(); code != 0 {
		t.Fatalf("修回合规后验收应成功: exit = %d, diagnostics = %+v", code, res.Diagnostics)
	}
}

// 系列语料矩阵完备性对账（双向，同 TestNovelCorpusMatrix）。
func TestSeriesCorpusMatrix(t *testing.T) {
	reconcileCorpusMatrix(t, corpusMatrixSpec{
		corpusDir:       seriesCorpusDir,
		goldenSrc:       "series_corpus_test.go",
		goldenTest:      "TestSeriesCorpusGolden",
		negativeSrc:     "series_corpus_test.go",
		structureGlob:   "series_corpus_test.go",
		structurePrefix: "TestSeriesCorpus",
		self:            "TestSeriesCorpusMatrix",
		minEntries:      18,
		minNegative:     5,
		minStructure:    5,
	})
	// 矩阵编号格式与唯一性（技术规范 testdata-design-artifact-validation）。
	raw, err := os.ReadFile(filepath.Join(seriesCorpusDir, "matrix.yaml"))
	if err != nil {
		t.Fatal(err)
	}
	var matrix struct {
		Entries []struct {
			ID      string `yaml:"id"`
			Feature string `yaml:"feature"`
		} `yaml:"entries"`
	}
	if err := yaml.Unmarshal(raw, &matrix); err != nil {
		t.Fatal(err)
	}
	idRe := regexp.MustCompile(`^S-[A-Z0-9]+-[0-9]{2}$`)
	seen := map[string]bool{}
	features := map[string]bool{}
	for _, e := range matrix.Entries {
		if !idRe.MatchString(e.ID) || seen[e.ID] {
			t.Errorf("矩阵编号 %q 格式不符（S-<特性>-<序号>）或重复", e.ID)
		}
		seen[e.ID] = true
		features[e.Feature] = true
	}
	for _, f := range []string{"series-setup", "series-canon", "canon-override", "series-context", "global-timeline",
		"same-time-presence", "cross-work-dead-acting", "cross-work-state", "series-gate", "determinism"} {
		if !features[f] {
			t.Errorf("矩阵缺少特性面 %s", f)
		}
	}
}
