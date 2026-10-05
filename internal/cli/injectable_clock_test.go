package cli

import (
	"os"
	"path/filepath"
	"strings"
	"testing"
)

// injectable-clock：internal/ 非测试源码不得直取 time.Now() 或硬编码 time.Date 时刻，
// 时钟一律经 time.Time 参数注入并由 internal/cli 传包级 now()；排除清单登记在
// go-project-structure 技术标准的 injectable-clock 规则中。
func TestInjectableClock(t *testing.T) {
	allowed := map[string]string{
		"cli/review.go":  "包级可注入时钟 now 的定义与测试恢复点",
		"canon/canon.go": "已登记排除：系列级同 id 覆盖的 ConfirmedAt，待系列覆盖确定性变更处理",
	}
	var files []string
	err := filepath.Walk("..", func(p string, info os.FileInfo, err error) error {
		if err != nil {
			return err
		}
		if info.IsDir() {
			return nil
		}
		name := info.Name()
		if !strings.HasSuffix(name, ".go") || strings.HasSuffix(name, "_test.go") {
			return nil
		}
		files = append(files, p)
		return nil
	})
	if err != nil {
		t.Fatal(err)
	}
	if len(files) < 100 {
		t.Fatalf("扫描到的源码文件数 = %d，异常", len(files))
	}
	for _, p := range files {
		rel, err := filepath.Rel("..", p)
		if err != nil {
			t.Fatal(err)
		}
		data, err := os.ReadFile(p)
		if err != nil {
			t.Fatal(err)
		}
		for _, line := range strings.Split(string(data), "\n") {
			switch {
			case strings.Contains(line, "time.Now()"), strings.Contains(line, "time.Date(202"):
				trimmed := strings.TrimSpace(line)
				if strings.HasPrefix(trimmed, "//") {
					continue
				}
				if reason, ok := allowed[rel]; ok {
					t.Logf("排除 %s：%s", rel, reason)
					continue
				}
				t.Errorf("%s 直取时钟：%s", rel, trimmed)
			}
		}
	}
}
