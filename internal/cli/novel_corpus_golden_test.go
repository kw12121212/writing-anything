package cli

import (
	"encoding/json"
	"os"
	"path/filepath"
	"slices"
	"strings"
	"testing"
	"time"
)

// corpusGolden 是一个黄金对象：名称、采集方式（文件路径或命令 stdout）。
type corpusGolden struct {
	name string // golden/ 下同名文件
	path string // 重放工作区内文件（二选一）
	cmd  []string
}

// 小说语料黄金文件：重放后采集九类对象与 testdata/e2e/novel/golden/ 逐字节比对。
// 冻结时间源下全部产物确定，逐字节即规范化策略；UPDATE_GOLDEN=1 一键再生成。
func TestNovelCorpusGolden(t *testing.T) {
	frozen := time.Date(2026, 6, 20, 12, 0, 0, 0, time.UTC)
	now = func() time.Time { return frozen }
	defer func() { now = time.Now }()

	dir := t.TempDir()
	replayNovelCorpus(t, dir)

	work := filepath.Join(dir, ".writing-anything", "works", "stopped-at-417")
	goldens := []corpusGolden{
		{name: "knowledge.yaml", path: filepath.Join(work, "knowledge.yaml")},
		{name: "threads.yaml", path: filepath.Join(work, "threads.yaml")},
		{name: "book.md", path: filepath.Join(dir, "book.md")},
		{name: "novel-export.md", path: filepath.Join(dir, "novel-export.md")},
		{name: "state-at-ch3-s3.json", cmd: []string{"state", "at", "stopped-at-417/ch3-s3"}},
		{name: "continuity.json", cmd: []string{"continuity", "stopped-at-417"}},
		{name: "context-ch3-s2.json", cmd: []string{"context", "stopped-at-417/ch3-s2"}},
		{name: "review-report.json", cmd: []string{"review", "report", "stopped-at-417"}},
	}
	// 纯文本整书导出在重放脚本之外补一次（语料脚本导出 Markdown；纯文本同链路、同先序）。
	if code, res := runWS(t, dir, "export", "stopped-at-417", "--format", "text", "--output", "novel-export.txt"); code != 0 {
		t.Fatalf("export text: exit = %d, diagnostics = %+v", code, res.Diagnostics)
	}
	goldens = append(goldens, corpusGolden{name: "novel-export.txt", path: filepath.Join(dir, "novel-export.txt")})

	update := os.Getenv("UPDATE_GOLDEN") == "1"
	goldenDir := filepath.Join("testdata", "e2e", "novel", "golden")
	if update {
		if err := os.MkdirAll(goldenDir, 0o755); err != nil {
			t.Fatal(err)
		}
	}
	for _, g := range goldens {
		var data []byte
		if g.path != "" {
			var err error
			if data, err = os.ReadFile(g.path); err != nil {
				t.Fatalf("%s: 采集失败: %v", g.name, err)
			}
		} else {
			code, res := runWS(t, dir, g.cmd...)
			if code != 0 {
				t.Fatalf("%v: exit = %d, diagnostics = %+v", g.cmd, code, res.Diagnostics)
			}
			// 命令输出的诊断 next 会内嵌临时目录绝对路径（--dir <tmp>），归一为占位符。
			data = []byte(strings.ReplaceAll(res.stdout, dir, "<dir>"))
		}
		gp := filepath.Join(goldenDir, g.name)
		if update {
			if err := os.WriteFile(gp, data, 0o644); err != nil {
				t.Fatal(err)
			}
			t.Logf("更新黄金文件 %s（%d 字节）", g.name, len(data))
			continue
		}
		want, err := os.ReadFile(gp)
		if err != nil {
			t.Fatalf("黄金文件 %s 缺失（先 UPDATE_GOLDEN=1 生成）: %v", g.name, err)
		}
		if string(want) != string(data) {
			at := 0
			for at < len(want) && at < len(data) && want[at] == data[at] {
				at++
			}
			lo, hi := at-120, at+120
			if lo < 0 {
				lo = 0
			}
			if hi > len(want) {
				hi = len(want)
			}
			lo2, hi2 := at-120, at+120
			if lo2 < 0 {
				lo2 = 0
			}
			if hi2 > len(data) {
				hi2 = len(data)
			}
			t.Fatalf("黄金文件 %s 不一致（期望 %d 字节，实际 %d 字节，首个差异在 %d）\n--- 期望 ---\n%s\n--- 实际 ---\n%s\n（UPDATE_GOLDEN=1 go test ./internal/cli/ -run TestNovelCorpusGolden 再生成）",
				g.name, len(want), len(data), at, string(want[lo:hi]), string(data[lo2:hi2]))
		}
	}
}

// 四类注入违例的负向断言：重放副本上逐类注入，断言对应审查以 error 级报告且诊断码正确，
// 修回合规后恢复全绿。知情违例有两条断言：台账注入（提前登记获知事件，knowledge at 暴露）与
// prose 注入（正文提前出现揭示词，continuity.knowledge-early，error 级——knowledge-reveal-terms 变更补齐）。
func TestNovelCorpusNegativeInjections(t *testing.T) {
	frozen := time.Date(2026, 6, 20, 12, 0, 0, 0, time.UTC)
	now = func() time.Time { return frozen }
	defer func() { now = time.Now }()

	dir := t.TempDir()
	replayNovelCorpus(t, dir)

	findings := func() map[string]bool {
		t.Helper()
		code, res := runWS(t, dir, "continuity", "stopped-at-417")
		if code != 0 {
			t.Fatalf("baseline continuity: exit = %d, diagnostics = %+v", code, res.Diagnostics)
		}
		var out struct {
			Findings []struct {
				Code    string `json:"code"`
				Waived  bool   `json:"waived"`
				Scene   string `json:"scene"`
			} `json:"findings"`
		}
		if err := json.Unmarshal([]byte(res.stdout), &out); err != nil {
			t.Fatalf("continuity 输出解析失败: %v; %s", err, res.stdout)
		}
		codes := map[string]bool{}
		for _, f := range out.Findings {
			if !f.Waived {
				codes[f.Code] = true
			}
		}
		return codes
	}

	// 基线：合规语料无未豁免发现项。
	if codes := findings(); len(codes) > 0 {
		t.Fatalf("基线连续性应全绿，实际 %v", codes)
	}

	// (1) 设定冲突：修改作者钦定条目被拒（canon.author-pinned，error 级）并生成阻塞决策
	//（唯一写入是决策队列；设定集不变，作者未 decide 前修改不生效）。
	before := snapshotTree(t, dir)
	code, res := runWS(t, dir, "canon", "set", "stopped-at-417", "rule-clock-freeze", "--text", "测试：停摆之约改为可修")
	if code != 1 || !diagCode(res, "canon.author-pinned") {
		t.Fatalf("修改钦定条目: exit = %d, diagnostics = %+v", code, res.Diagnostics)
	}
	for _, p := range res.ChangedPaths {
		if p != ".writing-anything/decisions.yaml" {
			t.Fatalf("被拒的钦定修改只应写决策队列，实际写 %v", res.ChangedPaths)
		}
	}
	if after := snapshotTree(t, dir); len(after) != len(before) {
		t.Fatalf("被拒的钦定修改不应新增/删除文件：%d → %d", len(before), len(after))
	}

	// (2) 知情违例：提前引用秘密——向倒叙场景注入林晚获知溺亡真相的知情事件（close-out --replace）。
	injected := "state_changes: []\nknowledge:\n  - {id: kn-x, entry: char-shen-wangshu, character: char-lin-wan}\nevents: [ev-letter-undelivered]\n"
	if code, res := runWSIn(t, dir, injected, "scene", "close-out", "stopped-at-417/ch2-s2", "--replace"); code != 0 {
		t.Fatalf("注入越界知情: exit = %d, diagnostics = %+v", code, res.Diagnostics)
	}
	if code, res := runWS(t, dir, "knowledge", "confirm", "stopped-at-417", "kn-x", "--confirmed-by-author", "--confirmation", "作者：确认这条越界获知。"); code != 0 {
		t.Fatalf("确认注入的越界知情: exit = %d, diagnostics = %+v", code, res.Diagnostics)
	}
	if code, res := runWS(t, dir, "knowledge", "at", "stopped-at-417/ch2-s3", "--character", "char-lin-wan"); code != 0 {
		t.Fatalf("注入后 knowledge at: exit = %d, diagnostics = %+v", code, res.Diagnostics)
	} else if !strings.Contains(res.stdout, "char-shen-wangshu") {
		t.Fatalf("注入后 knowledge at 应暴露越界获知（林晚提前知道溺亡真相）: %s", res.stdout)
	}
	// 修回合规：撤销越界知情（close-out --replace 不清理已写入的台账条目，需显式 reject），
	// 再用原始收口载荷恢复 closeout 记录，边界回到基线。
	if code, res := runWS(t, dir, "knowledge", "reject", "stopped-at-417", "kn-x", "--confirmed-by-author", "--confirmation", "作者：撤销这条越界获知。"); code != 0 {
		t.Fatalf("撤销注入的越界知情: exit = %d, diagnostics = %+v", code, res.Diagnostics)
	}
	// 原始知情事件 kn-03 仍在台账（close-out --replace 不清理台账），恢复载荷不再重复登记。
	restore := "state_changes: []\nknowledge: []\nevents: [ev-letter-undelivered]\n"
	if code, res := runWSIn(t, dir, restore, "scene", "close-out", "stopped-at-417/ch2-s2", "--replace"); code != 0 {
		t.Fatalf("恢复原始收口: exit = %d, diagnostics = %+v", code, res.Diagnostics)
	}
	if code, res := runWS(t, dir, "knowledge", "at", "stopped-at-417/ch2-s3", "--character", "char-lin-wan"); code != 0 {
		t.Fatalf("恢复后 knowledge at: exit = %d, diagnostics = %+v", code, res.Diagnostics)
	} else if strings.Contains(res.stdout, "char-shen-wangshu") {
		t.Fatalf("恢复后 knowledge at 不应再含溺亡真相: %s", res.stdout)
	}
	if codes := findings(); len(codes) > 0 {
		t.Fatalf("恢复知情边界后应全绿，实际 %v", codes)
	}

	// (2b) 知情违例（prose 级）：把含揭示词的句子注入 ch1-s3 正文 → continuity.knowledge-early（error）。
	// 揭示词挂在 kn-05/kn-07（溺亡真相，获知点 ch3-s2）与 kn-08/kn-09（信件内容，获知点 ch3-s3）上；
	// ch1-s3 早于两个获知点且出场人物含林晚（kn-05 获知者），reader 与林晚双通道都应报。
	original, err := os.ReadFile(filepath.Join("testdata", "e2e", "novel", "inputs", "ch1-s3-final.md"))
	if err != nil {
		t.Fatal(err)
	}
	injectedProse := string(original) + "\n他想起韩江说过的那句话：沈先生是为了救人才走的，这一点他从未怀疑。\n"
	if code, res := runWSIn(t, dir, injectedProse, "draft", "write", "stopped-at-417/ch1-s3"); code != 0 {
		t.Fatalf("注入揭示词正文: exit = %d, diagnostics = %+v", code, res.Diagnostics)
	}
	if codes := findings(); !codes["continuity.knowledge-early"] {
		t.Fatalf("正文提前揭示应报 continuity.knowledge-early，实际 %v", codes)
	}
	// 修回合规：重写原始正文后全绿。
	if code, res := runWSIn(t, dir, string(original), "draft", "write", "stopped-at-417/ch1-s3"); code != 0 {
		t.Fatalf("恢复原始正文: exit = %d, diagnostics = %+v", code, res.Diagnostics)
	}
	if codes := findings(); len(codes) > 0 {
		t.Fatalf("恢复正文后应全绿，实际 %v", codes)
	}

	// (3) 伏笔悬空：删除伏笔回收（th-a 回退 open）→ continuity.thread-anomaly（error）。
	if code, res := runWS(t, dir, "threads", "set", "stopped-at-417", "th-a", "--status", "open"); code != 0 {
		t.Fatalf("回退 th-a: exit = %d, diagnostics = %+v", code, res.Diagnostics)
	}
	if codes := findings(); !codes["continuity.thread-anomaly"] {
		t.Fatalf("删除伏笔回收后应报 continuity.thread-anomaly，实际 %v", codes)
	}
	if code, res := runWS(t, dir, "threads", "set", "stopped-at-417", "th-a", "--status", "paid", "--recovered-in", "stopped-at-417/ch3-s3"); code != 0 {
		t.Fatalf("恢复 th-a: exit = %d, diagnostics = %+v", code, res.Diagnostics)
	}
	if codes := findings(); len(codes) > 0 {
		t.Fatalf("恢复伏笔回收后应全绿，实际 %v", codes)
	}

	// (4) 时间线倒置：互指 after 成环 → timeline.cycle 与 timeline.order-conflict（error）。
	if code, res := runWS(t, dir, "scene", "contract", "stopped-at-417/ch2-s1", "sc", "--after", "stopped-at-417/ch2-s2"); code != 0 {
		t.Fatalf("注入互指 after: exit = %d, diagnostics = %+v", code, res.Diagnostics)
	}
	code, res = runWS(t, dir, "timeline", "stopped-at-417")
	if code != 1 || !diagCode(res, "timeline.cycle") || !diagCode(res, "timeline.order-conflict") {
		t.Fatalf("时间线倒置: exit = %d, diagnostics = %+v", code, res.Diagnostics)
	}
	if codes := findings(); !codes["timeline.cycle"] || !codes["timeline.order-conflict"] {
		t.Fatalf("连续性审查应携带时间线发现，实际 %v", codes)
	}
	if code, res := runWS(t, dir, "scene", "contract", "stopped-at-417/ch2-s1", "sc", "--after", ""); code != 0 {
		t.Fatalf("删除互指 after: exit = %d, diagnostics = %+v", code, res.Diagnostics)
	}
	if codes := findings(); len(codes) > 0 {
		t.Fatalf("删除互指 after 后应全绿，实际 %v", codes)
	}
}

func diagCode(res result, code string) bool {
	for _, d := range res.Diagnostics {
		if d.Code == code {
			return true
		}
	}
	return false
}

// 秘密遮蔽（Secret Masking）在语料上的完成标准：沈望舒溺水真相（设定条目 char-shen-wangshu，kn-05/kn-07 登记
// 揭示词）在视角人物获知之前的场景不出现在 wa-cli context 输出中，获知之后出现；补登揭示词后无需改写人物卡，
// 语料整体校验没有 knowledge.missing-terms。
func TestNovelCorpusSecretMasking(t *testing.T) {
	frozen := time.Date(2026, 6, 20, 12, 0, 0, 0, time.UTC)
	now = func() time.Time { return frozen }
	defer func() { now = time.Now }()

	dir := t.TempDir()
	replayNovelCorpus(t, dir)

	truths := []string{"潮生溺水被沈望舒救起", "为救溺水的潮生下水"}
	povs := map[string]string{
		"ch1-s1": "char-chen-shouzhuo", "ch1-s2": "char-lin-wan", "ch1-s3": "char-chen-shouzhuo",
		"ch2-s1": "char-lin-wan", "ch2-s2": "char-han-jiang", "ch2-s3": "char-chen-shouzhuo",
		"ch3-s1": "char-lin-wan", "ch3-s2": "char-lin-wan", "ch3-s3": "char-lin-wan",
	}
	contextOf := func(scene string) string {
		t.Helper()
		code, res := runWS(t, dir, "context", "stopped-at-417/"+scene)
		if code != 0 {
			t.Fatalf("context %s: exit = %d, diagnostics = %+v", scene, code, res.Diagnostics)
		}
		return res.stdout
	}
	for scene, pov := range povs {
		code, res := runWS(t, dir, "knowledge", "at", "stopped-at-417/"+scene, "--character", pov)
		if code != 0 {
			t.Fatalf("knowledge at %s: exit = %d", scene, code)
		}
		if strings.Contains(res.stdout, `"entry":"char-shen-wangshu"`) {
			continue
		}
		out := contextOf(scene)
		for _, truth := range truths {
			if strings.Contains(out, truth) {
				t.Errorf("%s（视角 %s 尚未获知）的上下文包含溺水真相「%s」", scene, pov, truth)
			}
		}
	}
	// 林晚在 ch3-s2 获知：ch3-s2 遮蔽陈守拙人物卡的真相句，ch3-s3 恢复。
	if out := contextOf("ch3-s2"); !strings.Contains(out, `"masked":[{"part":"character","id":"char-chen-shouzhuo","sentences":1}]`) {
		t.Errorf("ch3-s2 masked: %s", out)
	}
	if out := contextOf("ch3-s3"); !strings.Contains(out, "二十年前儿子潮生溺水被沈望舒救起，此后沉默守护恩人之女。") {
		t.Errorf("ch3-s3 应包含溺水真相: %s", out)
	}
	// 陈守拙在 ch1-s1 获知：以他为视角的 ch1-s3 不遮蔽。
	if out := contextOf("ch1-s3"); strings.Contains(out, "另有未揭示设定") {
		t.Errorf("ch1-s3 不应遮蔽: %s", out)
	}
	if _, res := runWS(t, dir, "validate"); slices.ContainsFunc(res.Diagnostics, func(d Diagnostic) bool { return d.Code == "knowledge.missing-terms" }) {
		t.Errorf("语料不应有 knowledge.missing-terms: %+v", res.Diagnostics)
	}
}

// 文风按文体隔离：novel-e2e 语料的 config resolved 与每个场景的上下文包都不含 default 规范包的文章类文风，
// config resolved 在 excludedStyle 中列出被排除的条目与原因；人读输出同样列出（genre-style-isolation）。
func TestNovelCorpusStyleIsolation(t *testing.T) {
	dir := t.TempDir()
	replayNovelCorpus(t, dir)

	articleStyle := []string{"结论先行", "用具体的事实、数据和例子代替空泛的形容词"}
	code, res := runWS(t, dir, "config", "resolved")
	if code != 0 {
		t.Fatalf("config resolved: exit = %d, diagnostics = %+v", code, res.Diagnostics)
	}
	var out struct {
		Resolved struct {
			Genre struct {
				ID       string `json:"id"`
				Category string `json:"category"`
			} `json:"genre"`
			Style []struct {
				Text string `json:"text"`
			} `json:"style"`
			ExcludedStyle []struct {
				Text      string   `json:"text"`
				Source    string   `json:"source"`
				AppliesTo []string `json:"appliesTo"`
				Reason    string   `json:"reason"`
			} `json:"excludedStyle"`
		} `json:"resolved"`
	}
	if err := json.Unmarshal([]byte(res.stdout), &out); err != nil {
		t.Fatal(err)
	}
	r := out.Resolved
	if r.Genre.ID != "novel" || r.Genre.Category != "fiction" || len(r.ExcludedStyle) != 2 {
		t.Fatalf("resolved = %+v", r)
	}
	for _, s := range r.Style {
		for _, a := range articleStyle {
			if strings.Contains(s.Text, a) {
				t.Errorf("resolved.style 含文章类文风 %q", s.Text)
			}
		}
	}
	for i, e := range r.ExcludedStyle {
		if !strings.Contains(e.Text, articleStyle[i]) || e.Source != "pack:default" || !slices.Equal(e.AppliesTo, []string{"article"}) ||
			!strings.Contains(e.Reason, "novel") || !strings.Contains(e.Reason, "fiction") {
			t.Errorf("excludedStyle[%d] = %+v", i, e)
		}
	}
	code, text, _ := run(t, "config", "resolved", "--dir", dir)
	if code != 0 || !strings.Contains(text, "文体类别（Genre Category）fiction") || !strings.Contains(text, "未生效的文风（按文体类别排除）") ||
		!strings.Contains(text, "结论先行，先说结果再说过程。（来自 pack:default）：只适用于文章类（article）文体，当前文体 novel 属于小说类（fiction）") {
		t.Fatalf("config resolved text: exit = %d\n%s", code, text)
	}
	for _, scene := range []string{"ch1-s1", "ch2-s2", "ch3-s3"} {
		code, res := runWS(t, dir, "context", "stopped-at-417/"+scene)
		if code != 0 {
			t.Fatalf("context %s: exit = %d", scene, code)
		}
		if !strings.Contains(res.stdout, `"style":[]`) {
			t.Errorf("context %s 的 norms.style 不为空", scene)
		}
		for _, a := range articleStyle {
			if strings.Contains(res.stdout, a) {
				t.Errorf("context %s 含文章类文风 %q", scene, a)
			}
		}
	}
}
