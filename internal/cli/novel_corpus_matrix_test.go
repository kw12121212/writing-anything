package cli

import (
	"fmt"
	"os"
	"path/filepath"
	"regexp"
	"strings"
	"testing"

	"go.yaml.in/yaml/v3"
)

// matrixEvidence 是矩阵条目的对账锚点：script=commands.yaml 步骤（token 为命令子串，可选
// stdout_contains/expect_code 断言取值）、golden=golden/ 文件、negative=负向注入断言码、
// structure=结构校验测试名。
type matrixEvidence struct {
	Kind           string  `yaml:"kind"`
	Token          *string `yaml:"token"`
	StdoutContains *string `yaml:"stdout_contains"`
	ExpectCode     *string `yaml:"expect_code"`
	File           *string `yaml:"file"`
	Code           *string `yaml:"code"`
	Test           *string `yaml:"test"`
}

type matrixEntryFull struct {
	ID       string           `yaml:"id"`
	Feature  string           `yaml:"feature"`
	Evidence []matrixEvidence `yaml:"evidence"`
}

// negativeAssertionsOf 从 TestNovelCorpusNegativeInjections 的源码机械推导其断言的全部
// 负向码（diagCode/codes 索引字面量）与标记，防止清单与断言漂移；
// knowledge-at-leak 是知情违例的台账暴露标记（产品暂无对应诊断码，见测试注释）。
func negativeAssertionsOf(t *testing.T) map[string]bool {
	t.Helper()
	src, err := os.ReadFile("novel_corpus_golden_test.go")
	if err != nil {
		t.Fatal(err)
	}
	out := map[string]bool{"knowledge-at-leak": true}
	re := regexp.MustCompile(`(?:diagCode\(res, "|codes\[")([a-z]+\.[a-z-]+)"`)
	for _, m := range re.FindAllStringSubmatch(string(src), -1) {
		out[m[1]] = true
	}
	if len(out) < 5 {
		t.Fatalf("从负向测试源码推导断言码失败：仅 %v", out)
	}
	return out
}

// structureAssertionTests 是可作为 structure 锚点的测试名。
var structureAssertionTests = map[string]bool{
	"TestNovelCorpusScript": true,
	"TestNovelCorpusDesign": true,
	"TestNovelCorpusReplay": true,
}

// 矩阵完备性对账（双向）：每条矩阵条目的 evidence 必须解析到真实存在的断言；反向地，
// 全部黄金文件、全部负向断言码、脚本中全部带 stdout_contains/expect_code 断言的步骤，
// 都必须被至少一条矩阵条目引用。缺失项清单化输出。
func TestNovelCorpusMatrix(t *testing.T) {
	corpusDir := filepath.Join("testdata", "e2e", "novel")
	raw, err := os.ReadFile(filepath.Join(corpusDir, "matrix.yaml"))
	if err != nil {
		t.Fatal(err)
	}
	var matrix struct {
		Entries []matrixEntryFull `yaml:"entries"`
	}
	if err := yaml.Unmarshal(raw, &matrix); err != nil {
		t.Fatalf("matrix.yaml 解析失败: %v", err)
	}
	if len(matrix.Entries) < 45 {
		t.Fatalf("矩阵条目数 = %d，期望不少于 45（特性面 + 负向条目）", len(matrix.Entries))
	}

	scriptRaw, err := os.ReadFile(filepath.Join(corpusDir, "commands.yaml"))
	if err != nil {
		t.Fatal(err)
	}
	var script corpusScript
	if err := yaml.Unmarshal(scriptRaw, &script); err != nil {
		t.Fatal(err)
	}
	// 脚本步骤的三元定位：命令串、stdout_contains、expect_code。
	type step struct{ label, stdout, code string }
	var steps []step
	for _, st := range script.Stages {
		for _, s := range st.Steps {
			stdout, code := "", ""
			if s.StdoutContains != nil {
				stdout = *s.StdoutContains
			}
			if s.ExpectCode != nil {
				code = *s.ExpectCode
			}
			steps = append(steps, step{strings.Join(s.Command, " "), stdout, code})
		}
	}
	negativeAssertions := negativeAssertionsOf(t)
	goldens := map[string]bool{}
	gs, err := os.ReadDir(filepath.Join(corpusDir, "golden"))
	if err != nil {
		t.Fatal(err)
	}
	for _, g := range gs {
		goldens[g.Name()] = true
	}
	// 黄金目录与 TestNovelCorpusGolden 的采集清单一一对应：目录里不该有没有被比对过的文件。
	goldenSrc, err := os.ReadFile("novel_corpus_golden_test.go")
	if err != nil {
		t.Fatal(err)
	}
	goldenNames := map[string]bool{}
	for _, m := range regexp.MustCompile(`name: "([a-z0-9./-]+)"`).FindAllStringSubmatch(string(goldenSrc), -1) {
		goldenNames[m[1]] = true
	}
	for name := range goldens {
		if !goldenNames[name] {
			t.Errorf("golden/ 中的 %s 不在 TestNovelCorpusGolden 的采集清单里", name)
		}
	}
	for name := range goldenNames {
		if !goldens[name] {
			t.Errorf("TestNovelCorpusGolden 的采集项 %s 在 golden/ 目录中不存在", name)
		}
	}

	usedGolden := map[string]string{}
	usedNegative := map[string]string{}
	usedAssert := map[string]string{} // "label\x00type\x00value" → matrix id
	missing := []string{}
	for _, e := range matrix.Entries {
		if len(e.Evidence) == 0 {
			missing = append(missing, fmt.Sprintf("%s: 没有 evidence 锚点", e.ID))
			continue
		}
		for _, ev := range e.Evidence {
			switch ev.Kind {
			case "script":
				if ev.Token == nil {
					missing = append(missing, fmt.Sprintf("%s: script 锚点缺少 token", e.ID))
					continue
				}
				hit := false
				// 只有自带断言过滤（stdout_contains/expect_code）的锚点才登记反向引用：
				// 无过滤的宽 token 只作存在性锚点，防止顺带命中把反向检查变成虚绿。
				filtered := ev.StdoutContains != nil || ev.ExpectCode != nil
				for _, s := range steps {
					if !strings.Contains(s.label, *ev.Token) {
						continue
					}
					if ev.StdoutContains != nil && s.stdout != *ev.StdoutContains {
						continue
					}
					if ev.ExpectCode != nil {
						exact := false
						for _, c := range strings.Split(s.code, ",") {
							if strings.TrimSpace(c) == strings.TrimSpace(*ev.ExpectCode) {
								exact = true
								break
							}
						}
						if !exact {
							continue
						}
					}
					hit = true
					if !filtered {
						continue
					}
					if s.stdout != "" {
						usedAssert[s.label+"\x00stdout\x00"+s.stdout] = e.ID
					}
					for _, c := range strings.Split(s.code, ",") {
						usedAssert[s.label+"\x00code\x00"+strings.TrimSpace(c)] = e.ID
					}
				}
				if !hit {
					missing = append(missing, fmt.Sprintf("%s: 脚本中没有匹配 token %q%s%s 的步骤", e.ID, *ev.Token,
						stdSuffix(ev.StdoutContains), codeSuffix(ev.ExpectCode)))
				}
			case "golden":
				if ev.File == nil || !goldens[*ev.File] {
					missing = append(missing, fmt.Sprintf("%s: 黄金文件 %v 不存在", e.ID, ev.File))
					continue
				}
				usedGolden[*ev.File] = e.ID // 多条目共享同一黄金文件是允许的语义
			case "negative":
				if ev.Code == nil || !negativeAssertions[*ev.Code] {
					missing = append(missing, fmt.Sprintf("%s: 负向断言码 %v 不在负向测试的断言清单中", e.ID, ev.Code))
					continue
				}
				usedNegative[*ev.Code] = e.ID
			case "structure":
				if ev.Test == nil || !structureAssertionTests[*ev.Test] {
					missing = append(missing, fmt.Sprintf("%s: 结构测试 %v 不存在", e.ID, ev.Test))
				}
			default:
				missing = append(missing, fmt.Sprintf("%s: 未知 evidence 类型 %q", e.ID, ev.Kind))
			}
		}
	}
	if len(missing) > 0 {
		t.Errorf("矩阵→断言缺失项（%d 项）：\n%s", len(missing), strings.Join(missing, "\n"))
	}
	// 反向：黄金文件、负向码、脚本断言都被引用。
	for name := range goldens {
		if _, ok := usedGolden[name]; !ok {
			t.Errorf("黄金文件 %s 没有任何矩阵条目引用", name)
		}
	}
	for code := range negativeAssertions {
		if _, ok := usedNegative[code]; !ok {
			t.Errorf("负向断言码 %s 没有任何矩阵条目引用", code)
		}
	}
	for _, s := range steps {
		if s.stdout != "" {
			if _, ok := usedAssert[s.label+"\x00stdout\x00"+s.stdout]; !ok {
				t.Errorf("脚本断言（stdout_contains %q @ %s）没有被矩阵条目引用", s.stdout, s.label)
			}
		}
		for _, c := range strings.Split(s.code, ",") {
			c = strings.TrimSpace(c)
			if c == "" {
				continue
			}
			if _, ok := usedAssert[s.label+"\x00code\x00"+c]; !ok {
				t.Errorf("脚本断言（expect_code %s @ %s）没有被矩阵条目引用", c, s.label)
			}
		}
	}
}

func stdSuffix(p *string) string {
	if p == nil {
		return ""
	}
	return "（stdout_contains=" + *p + "）"
}

func codeSuffix(p *string) string {
	if p == nil {
		return ""
	}
	return "（expect_code=" + *p + "）"
}
