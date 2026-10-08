package skills

import (
	"os"
	"path/filepath"
	"strings"
	"testing"
)

// projectSkillReferences 登记每个仓库级项目技能正文必须引用、且必须真实存在的仓库路径：
// 技能描述的发布流程随仓库演进漂移（路径改名、文件删除）时，在此处失败，而不是在执行发布时才发现。
var projectSkillReferences = map[string][]string{
	"release": {
		"AGENTS.md",
		"docs/GLOSSARY.md",
		"docs/INSTALL.md",
		"install.ps1",
		"install.sh",
		"internal/tools/release",
	},
}

// projectSkillPhrases 登记项目技能正文必须保留的关键命令片段：缺少它们时技能描述的校验步骤会失效。
// release 的 Windows 安装校验必须以 WA_REQUIRE_PWSH=1 运行 TestInstallPs1，缺少 pwsh 时测试失败而不是静默跳过。
var projectSkillPhrases = map[string][]string{
	"release": {
		"WA_REQUIRE_PWSH=1 go test ./internal/tools/release -run TestInstallPs1",
		"$env:WA_REQUIRE_PWSH='1'; go test ./internal/tools/release -run TestInstallPs1",
	},
}

// TestProjectSkills 校验仓库级项目技能（.agents/skills/）：文件头格式与内置技能一致（Parse），
// release 技能存在，其正文引用的仓库路径都真实存在、关键命令片段都在（go-project-structure 的 project-skill-location 规则）。
func TestProjectSkills(t *testing.T) {
	root := filepath.Join("..", "..")
	dir := filepath.Join(root, ".agents", "skills")
	entries, err := os.ReadDir(dir)
	if err != nil {
		t.Fatalf("读取项目技能目录 %s：%v", dir, err)
	}
	names := map[string]bool{}
	for _, entry := range entries {
		if !entry.IsDir() {
			continue
		}
		name := entry.Name()
		names[name] = true
		content, err := os.ReadFile(filepath.Join(dir, name, FileName))
		if err != nil {
			t.Fatalf("读取项目技能 %s：%v", name, err)
		}
		if _, problem := Parse(name, content); problem != "" {
			t.Errorf("项目技能 %s：%s", name, problem)
		}
		for _, path := range projectSkillReferences[name] {
			if !strings.Contains(string(content), path) {
				t.Errorf("项目技能 %s 的正文没有引用 %s", name, path)
			}
			if _, err := os.Stat(filepath.Join(root, path)); err != nil {
				t.Errorf("项目技能 %s 引用的仓库路径不存在：%s", name, path)
			}
		}
		for _, phrase := range projectSkillPhrases[name] {
			if !strings.Contains(string(content), phrase) {
				t.Errorf("项目技能 %s 的正文缺少命令片段 %s", name, phrase)
			}
		}
	}
	if !names["release"] {
		t.Error("缺少项目技能 release（正式发布入口）")
	}
}
