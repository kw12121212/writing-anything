package cli

import (
	"fmt"
	"os"
	"regexp"
	"strings"
	"testing"

	"go.yaml.in/yaml/v3"
)

// novel 语料设计产物（corpus-design 变更）的结构校验：测试矩阵编号唯一、字段齐备、
// 特性面全覆盖、scene 引用设计文档中的单元 id，且设计满足规划文件规定的最低数量约束。
func TestNovelCorpusDesign(t *testing.T) {
	corpusDir := "testdata/e2e/novel"

	// 里程碑 scope 列出的长篇特性面：feature 枚举的唯一来源。
	features := []string{
		"units", "scene-contract", "causality", "state", "knowledge", "threads",
		"closeout", "summary", "context", "method", "recall", "continuity",
		"reviewpacket", "impact", "handoff", "volume", "assembly", "export",
	}
	featureSet := make(map[string]bool, len(features))
	for _, f := range features {
		featureSet[f] = true
	}

	raw, err := os.ReadFile(corpusDir + "/matrix.yaml")
	if err != nil {
		t.Fatal(err)
	}
	var matrix struct {
		Entries []struct {
			ID        string `yaml:"id"`
			Feature   string `yaml:"feature"`
			Scene     string `yaml:"scene"`
			Command   string `yaml:"command"`
			Expected  string `yaml:"expected"`
			Assertion string `yaml:"assertion"`
		} `yaml:"entries"`
	}
	if err := yaml.Unmarshal(raw, &matrix); err != nil {
		t.Fatalf("matrix.yaml 解析失败: %v", err)
	}
	if len(matrix.Entries) < 30 {
		t.Fatalf("矩阵条目数 = %d，期望至少 30 条覆盖全部特性面", len(matrix.Entries))
	}

	idRe := regexp.MustCompile(`^T-[A-Z0-9]+-[0-9]{2}$`)
	seenIDs := make(map[string]bool)
	covered := make(map[string]bool)
	for _, e := range matrix.Entries {
		if !idRe.MatchString(e.ID) {
			t.Errorf("矩阵编号格式不符: %q", e.ID)
		}
		if seenIDs[e.ID] {
			t.Errorf("矩阵编号重复: %q", e.ID)
		}
		seenIDs[e.ID] = true
		for field, v := range map[string]string{"feature": e.Feature, "scene": e.Scene, "command": e.Command, "expected": e.Expected, "assertion": e.Assertion} {
			if strings.TrimSpace(v) == "" {
				t.Errorf("%s: 字段 %s 为空", e.ID, field)
			}
		}
		if !featureSet[e.Feature] {
			t.Errorf("%s: feature %q 不在特性面枚举中", e.ID, e.Feature)
		}
		covered[e.Feature] = true
	}
	for _, f := range features {
		if !covered[f] {
			t.Errorf("特性面 %q 没有任何矩阵条目覆盖", f)
		}
	}

	design, err := os.ReadFile(corpusDir + "/DESIGN.md")
	if err != nil {
		t.Fatal(err)
	}
	designText := string(design)

	// DESIGN.md 的单元 id 与矩阵 scene 字段对账。
	unitIDs := map[string]bool{"book": true}
	for _, m := range regexp.MustCompile(`(?m)^\| (part-[0-9]) \|`).FindAllStringSubmatch(designText, -1) {
		unitIDs[m[1]] = true
	}
	for _, m := range regexp.MustCompile(`(?m)^\| (ch-[0-9]) \|`).FindAllStringSubmatch(designText, -1) {
		unitIDs[m[1]] = true
	}
	sceneRows := regexp.MustCompile(`(?m)^\| (ch[0-9]-s[0-9]) \|`).FindAllStringSubmatch(designText, -1)
	if len(sceneRows) != 9 {
		t.Fatalf("场景表行数 = %d，期望 3 章 9 场景", len(sceneRows))
	}
	sceneSet := make(map[string]bool, 9)
	for _, m := range sceneRows {
		sceneSet[m[1]] = true
		unitIDs[m[1]] = true
	}
	for ch := 1; ch <= 3; ch++ {
		for s := 1; s <= 3; s++ {
			id := fmt.Sprintf("ch%d-s%d", ch, s)
			if !sceneSet[id] {
				t.Errorf("场景表缺少 %s（3 章 × 3 场景网格不完整）", id)
			}
		}
	}
	for _, e := range matrix.Entries {
		if !unitIDs[e.Scene] {
			t.Errorf("%s: scene %q 不在 DESIGN.md 的单元结构中", e.ID, e.Scene)
		}
	}

	// 规划文件规定的最低数量约束。
	countMatches := func(re *regexp.Regexp) int {
		return len(re.FindAllStringSubmatch(designText, -1))
	}
	if n := countMatches(regexp.MustCompile(`(?m)^### char-[a-z0-9-]+ `)); n != 5 {
		t.Errorf("人物卡数 = %d，期望 5（不超过 5 个）", n)
	}
	if n := countMatches(regexp.MustCompile(`(?m)^- \*\*SECT-[0-9]+\*\*`)); n < 2 {
		t.Errorf("秘密数 = %d，期望至少 2 个知情边界", n)
	}
	if n := countMatches(regexp.MustCompile(`(?m)^- \*\*KN-[0-9]+\*\*`)); n < 8 {
		t.Errorf("知情点数 = %d，期望至少 8（两个秘密的完整边界）", n)
	}
	if n := countMatches(regexp.MustCompile(`(?m)^- \*\*ST-[0-9]+\*\*`)); n < 2 {
		t.Errorf("状态快照变更数 = %d，期望至少 2 条可断言变更", n)
	}
	// 跨章伏笔链：行内依次有埋设、强化、回收，且不是悬空链（不回收）。
	chainRe := regexp.MustCompile(`(?m)^- \*\*TH-[A-Z0-9]+\*\*[^\n]*埋设[^\n]*强化[^\n]*回收`)
	chains := chainRe.FindAllString(designText, -1)
	completed := 0
	for _, chain := range chains {
		if !strings.Contains(chain, "不回收") {
			completed++
		}
	}
	if completed < 2 {
		t.Errorf("完整跨章伏笔链数 = %d（%v），期望至少 2 条（埋设、强化、回收，不含悬空链）", completed, chains)
	}
	// 链的章位分布：第一章埋设、第二章出现强化、第三章回收。
	for _, m := range regexp.MustCompile(`(?m)^- \*\*(TH-[A-Z0-9]+)\*\*[^\n]*埋设[^\n]*强化[^\n]*回收`).FindAllStringSubmatch(designText, -1) {
		if strings.Contains(m[0], "不回收") {
			continue
		}
		line := m[0]
		if !regexp.MustCompile(`埋设 ch1-`).MatchString(line) {
			t.Errorf("%s: 跨章伏笔链未在第一章埋设", m[1])
		}
		if !regexp.MustCompile(`强化 ch1-[^\n]*、?ch2-|强化 ch2-`).MatchString(line) {
			t.Errorf("%s: 跨章伏笔链缺少第二章强化", m[1])
		}
		if !regexp.MustCompile(`回收 ch3-`).MatchString(line) {
			t.Errorf("%s: 跨章伏笔链未在第三章回收", m[1])
		}
	}
	if !strings.Contains(designText, "- **TH-C**") || !regexp.MustCompile(`- \*\*TH-C\*\*.*不回收`).MatchString(designText) {
		t.Errorf("缺少悬空伏笔 TH-C（埋设后不回收，供负向断言）")
	}
	// 作者钦定世界规则。
	rule := regexp.MustCompile(`(?s)### rule-[a-z0-9-]+.*author-mandated`)
	if countMatches(rule) < 1 {
		t.Errorf("缺少至少 1 条作者钦定（author-mandated）世界规则")
	}
	// 跨章倒叙：场景表 ch2-s2 行带 2006 年故事时间与倒叙标记。
	flashback := false
	for _, line := range strings.Split(designText, "\n") {
		if strings.HasPrefix(line, "| ch2-s2 |") && strings.Contains(line, "2006-") && strings.Contains(line, "倒叙") {
			flashback = true
		}
	}
	if !flashback {
		t.Errorf("场景表缺少跨章倒叙：ch2-s2 行应带 2006 年故事时间与倒叙标记")
	}
	// 雪花写作法十步映射齐全。
	for _, step := range []string{"one-sentence", "one-paragraph", "character-sketch", "one-page-synopsis",
		"character-details", "four-page-synopsis", "character-bible", "scene-list", "scene-details", "start-writing"} {
		if !strings.Contains(designText, step) {
			t.Errorf("雪花写作法十步映射缺少步骤 %q", step)
		}
	}
	// 矩阵断言引用的设计编号（KN/ST/TH/SECT/RQ）必须在 DESIGN.md 中存在。
	for _, m := range regexp.MustCompile(`\b((?:KN|ST|SECT|RQ)-[0-9]+|TH-[A-Z])\b`).FindAllStringSubmatch(string(raw), -1) {
		if !regexp.MustCompile(`(?m)^- \*\*` + regexp.QuoteMeta(m[1]) + `\*\*`).MatchString(designText) {
			t.Errorf("矩阵引用的设计编号 %q 在 DESIGN.md 中不存在", m[1])
		}
	}

	// 脚本格式规范：六个字段定义齐全，供录制与重放共用。
	format, err := os.ReadFile(corpusDir + "/script-format.md")
	if err != nil {
		t.Fatal(err)
	}
	for _, field := range []string{"stage", "command", "input_ref", "expect_exit", "expect_code", "stdout_contains"} {
		if !strings.Contains(string(format), field) {
			t.Errorf("script-format.md 缺少字段定义 %q", field)
		}
	}
}
