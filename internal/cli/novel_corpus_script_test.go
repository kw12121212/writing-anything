package cli

import (
	"crypto/sha256"
	"fmt"
	"os"
	"strings"
	"testing"
	"unicode/utf8"

	"go.yaml.in/yaml/v3"
)

type corpusStep struct {
	Command        []string `yaml:"command"`
	WriteFile      *string  `yaml:"write_file"`
	AuthorReview   *string  `yaml:"author_review"`
	InputRef       *string  `yaml:"input_ref"`
	ExpectExit     int      `yaml:"expect_exit"`
	ExpectCode     *string  `yaml:"expect_code"`
	StdoutContains *string  `yaml:"stdout_contains"`
}

type corpusStage struct {
	Stage string       `yaml:"stage"`
	Note  string       `yaml:"note"`
	Steps []corpusStep `yaml:"steps"`
}

type corpusScript struct {
	Stages []corpusStage `yaml:"stages"`
}

// 小说语料命令脚本（corpus-authoring 一次性真实创作录制）的结构校验：阶段齐备、
// 步骤字段语义合法、input_ref 全部指向检入文件、正文规模符合设计、
// 五类真实负向关卡步骤在位（人工编辑保护、卷末未确认、复查标记组稿阻塞、
// 连续性组稿阻塞、悬空伏笔放弃授权）。
func TestNovelCorpusScript(t *testing.T) {
	corpusDir := "testdata/e2e/novel"
	raw, err := os.ReadFile(corpusDir + "/commands.yaml")
	if err != nil {
		t.Fatal(err)
	}
	var script corpusScript
	if err := yaml.Unmarshal(raw, &script); err != nil {
		t.Fatalf("commands.yaml 解析失败: %v", err)
	}

	wantStages := []string{"init", "sources", "brief", "plan", "part-1", "part-2", "part-3", "assemble-export"}
	gotStages := make([]string, 0, len(script.Stages))
	for _, st := range script.Stages {
		gotStages = append(gotStages, st.Stage)
	}
	if strings.Join(gotStages, ",") != strings.Join(wantStages, ",") {
		t.Fatalf("阶段序列 = %v，期望 %v（完整生命周期顺序）", gotStages, wantStages)
	}

	negative := map[string]bool{
		"draft.human-edited":          false,
		"volume.unconfirmed":          false,
		"assemble.scenes-open":        false,
		"assemble.rechecks":           false,
		"assemble.continuity":         false,
		"threads.author-confirmation": false,
		"assemble.unreviewed":         false,
	}
	steps := 0
	writeSteps := 0
	reviewSteps := 0
	inputRefs := 0
	seenCommands := map[string]int{}
	for _, st := range script.Stages {
		if strings.TrimSpace(st.Note) == "" {
			t.Errorf("阶段 %s 缺少说明", st.Stage)
		}
		for _, s := range st.Steps {
			steps++
			kinds := 0
			for _, has := range []bool{s.Command != nil, s.WriteFile != nil, s.AuthorReview != nil} {
				if has {
					kinds++
				}
			}
			if kinds != 1 {
				t.Errorf("步骤必须三选一：wa-cli 命令（command）、文件写入（write_file）或阅读页复核（author_review），阶段 %s 第 %d 步", st.Stage, steps)
			}
			if s.AuthorReview != nil {
				reviewSteps++
			}
			if s.ExpectExit != 0 && (s.ExpectCode == nil || *s.ExpectCode == "") {
				t.Errorf("失败步骤（exit=%d）必须声明 expect_code", s.ExpectExit)
			}
			if s.ExpectExit != 0 && s.ExpectExit != 1 && s.ExpectExit != 2 {
				t.Errorf("expect_exit = %d，只允许 0/1/2", s.ExpectExit)
			}
			if s.ExpectCode != nil && *s.ExpectCode != "" {
				for _, code := range strings.Split(*s.ExpectCode, ",") {
					negative[code] = true
				}
			}
			if s.WriteFile != nil {
				writeSteps++
				if s.InputRef == nil || *s.InputRef == "" {
					t.Errorf("write_file 步骤必须以 input_ref 提供写入内容")
				}
			}
			if s.Command != nil && len(s.Command) == 0 {
				t.Errorf("command 为空数组；无命令步骤应使用 write_file")
			}
			if s.Command != nil {
				seenCommands[s.Command[0]]++
			}
			if s.InputRef != nil && *s.InputRef != "" {
				inputRefs++
				if _, err := os.Stat(corpusDir + "/" + *s.InputRef); err != nil {
					t.Errorf("input_ref %q 指向的文件不存在", *s.InputRef)
				}
			}
		}
	}
	for code, seen := range negative {
		if !seen {
			t.Errorf("脚本缺少真实负向步骤 %s", code)
		}
	}
	if steps < 200 {
		t.Errorf("步骤总数 = %d，期望完整创作流程不少于 200 步", steps)
	}
	if writeSteps != 1 {
		t.Errorf("作者直编步骤数 = %d，期望恰好 1 处", writeSteps)
	}
	if reviewSteps != 1 {
		t.Errorf("阅读页复核步骤数 = %d，期望恰好 1 处（strict 组稿前作者批量复核命令行渠道的确认）", reviewSteps)
	}
	if inputRefs < 30 {
		t.Errorf("带文本输入的步骤 = %d，期望素材、草稿、收口、审稿、交接等合计不少于 30", inputRefs)
	}
	// 生命周期各阶段的命令面在位。
	for _, verb := range []string{"init", "new", "source", "brief", "plan", "canon", "unit", "scene", "outline",
		"draft", "lint", "review", "accept", "advance", "summary", "volume", "recall", "impact", "handoff",
		"assemble", "export", "decide", "threads", "state", "knowledge", "timeline", "validate"} {
		if seenCommands[verb] == 0 {
			t.Errorf("脚本没有用到命令 %q", verb)
		}
	}

	// 九个场景草稿的规模：每场 1400–2000 字，总计约 1.5 万字。
	total := 0
	for ch := 1; ch <= 3; ch++ {
		for sc := 1; sc <= 3; sc++ {
			data, err := os.ReadFile(fmt.Sprintf("%s/inputs/ch%d-s%d.md", corpusDir, ch, sc))
			if err != nil {
				t.Fatal(err)
			}
			n := utf8.RuneCountInString(strings.TrimSpace(string(data)))
			if n < 1400 || n > 2000 {
				t.Errorf("场景 ch%d-s%d 草稿 %d 字，超出设计区间 [1400, 2000]", ch, sc, n)
			}
			total += n
		}
	}
	if total < 12000 || total > 18000 {
		t.Errorf("九场景正文合计 %d 字，偏离约 1.5 万字的设计规模", total)
	}

	// 哈希承诺：draft write --base 必须等于作者直编文件内容的 SHA-256。
	authorRaw, err := os.ReadFile(corpusDir + "/inputs/ch1-s3-author.md")
	if err != nil {
		t.Fatal(err)
	}
	wantBase := fmt.Sprintf("%x", sha256.Sum256(authorRaw))
	baseSeen := false
	for _, st := range script.Stages {
		for _, s := range st.Steps {
			c := s.Command
			for i := 0; i+1 < len(c); i++ {
				if c[i] == "--base" {
					baseSeen = true
					if c[i+1] != wantBase {
						t.Errorf("--base 哈希承诺 %s 与 ch1-s3-author.md 的 SHA-256 %s 不一致", c[i+1], wantBase)
					}
				}
			}
		}
	}
	if !baseSeen {
		t.Fatal("脚本中没有 --base 哈希承诺（作者直编合并步骤缺失）")
	}
	// 作者直编的合并终稿同样受规模约束。
	finalRaw, err := os.ReadFile(corpusDir + "/inputs/ch1-s3-final.md")
	if err != nil {
		t.Fatal(err)
	}
	if n := utf8.RuneCountInString(strings.TrimSpace(string(finalRaw))); n < 1400 || n > 2000 {
		t.Errorf("作者直编合并终稿 %d 字，超出设计区间 [1400, 2000]", n)
	}
}
