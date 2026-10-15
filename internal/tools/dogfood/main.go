// dogfood 校验真实接入记录（Dogfood Record）docs/dogfood/runs.yaml：两个客户端 × 两种文体的每个组合恰好
// 一条记录；已运行的走到导出，记录含客户端版本、wa-cli 版本与命令序列，每个问题都链接到已归档的缺陷变更或写明
// 不修理由；未运行的只写明原因；两个客户端与两种文体各至少有一次已运行。字段说明见 docs/dogfood/README.md。
//
// 用法：go run ./internal/tools/dogfood [-root .] [-records docs/dogfood/runs.yaml]
//
// 产品二进制与 wa-cli 运行时不引用本工具。
package main

import (
	"bytes"
	"errors"
	"flag"
	"fmt"
	"io"
	"os"
	"path/filepath"
	"slices"
	"strings"
	"time"

	"go.yaml.in/yaml/v3"
)

// DefaultRecords 是真实接入记录相对仓库根目录的默认位置。
const DefaultRecords = "docs/dogfood/runs.yaml"

// archivedChanges 是已归档变更所在目录（相对仓库根目录），目录名为 <日期>-<变更名>。
const archivedChanges = ".strict-spec-driven/changes/archive"

// clients 是需要覆盖的客户端，按输出顺序排列。
var clients = []string{"claude-code", "codex"}

// genres 是需要覆盖的文体，按输出顺序排列；exportFormats 是各文体应使用的导出格式。
var (
	genres        = []string{"tech-blog", "wechat-marketing"}
	exportFormats = map[string]string{"tech-blog": "markdown", "wechat-marketing": "wechat-html"}
)

// channels 是允许的接入方式。
var channels = map[string]bool{"skill": true, "mcp": true}

func main() {
	os.Exit(run(os.Args[1:], os.Stdout, os.Stderr))
}

// Records 是 runs.yaml 的结构。
type Records struct {
	SchemaVersion int   `yaml:"schema_version"`
	Runs          []Run `yaml:"runs"`
}

// Run 是一次真实接入验证运行的记录。
type Run struct {
	ID            string   `yaml:"id"`
	Client        string   `yaml:"client"`
	ClientVersion string   `yaml:"client_version"`
	WaCliVersion  string   `yaml:"wa_cli_version"`
	Genre         string   `yaml:"genre"`
	Channels      []string `yaml:"channels"`
	Date          string   `yaml:"date"`
	Commands      []string `yaml:"commands"`
	Export        Export   `yaml:"export"`
	Notes         string   `yaml:"notes"`
	Issues        []Issue  `yaml:"issues"`
	SkippedReason string   `yaml:"skipped_reason"` // 非空表示该组合未运行
}

// Export 是运行的导出结果。
type Export struct {
	Format string `yaml:"format"`
	Path   string `yaml:"path"`
}

// Issue 是运行中发现的一个问题：fix_change 与 wont_fix_reason 恰好其一。
type Issue struct {
	ID            string `yaml:"id"`
	Summary       string `yaml:"summary"`
	FixChange     string `yaml:"fix_change"`
	WontFixReason string `yaml:"wont_fix_reason"`
}

// Report 是一次校验的结果。
type Report struct {
	Runs     int      // 已运行的记录条数
	Skipped  int      // 写明原因的未运行记录条数
	Issues   int      // 问题总数
	Fixed    int      // 链接已归档缺陷变更的问题数
	WontFix  int      // 写明不修理由的问题数
	Problems []string // 全部问题，按稳定顺序
}

// run 返回进程退出码：0 记录齐备，1 存在问题或输入错误，2 用法错误。
func run(args []string, stdout, stderr io.Writer) int {
	fs := flag.NewFlagSet("dogfood", flag.ContinueOnError)
	fs.SetOutput(stderr)
	root := fs.String("root", ".", "仓库根目录")
	records := fs.String("records", DefaultRecords, "真实接入记录路径（相对路径相对 -root 解析）")
	if err := fs.Parse(args); err != nil {
		return 2
	}
	if fs.NArg() > 0 {
		fmt.Fprintf(stderr, "dogfood：未知参数：%s\n", fs.Arg(0))
		return 2
	}
	path := *records
	if !filepath.IsAbs(path) {
		path = filepath.Join(*root, path)
	}
	report, err := Check(*root, path)
	if err != nil {
		fmt.Fprintf(stderr, "dogfood：%v\n", err)
		return 1
	}
	if len(report.Problems) > 0 {
		fmt.Fprintf(stderr, "dogfood：真实接入记录发现 %d 个问题（%s）：\n", len(report.Problems), *records)
		for _, p := range report.Problems {
			fmt.Fprintf(stderr, "  - %s\n", p)
		}
		fmt.Fprintln(stderr, "dogfood：记录格式与问题分诊见 docs/dogfood/README.md。")
		return 1
	}
	fmt.Fprintf(stdout, "dogfood：%d 次运行记录齐备，%d 个组合写明未运行原因（问题 %d 个：已修复 %d，不修 %d）\n",
		report.Runs, report.Skipped, report.Issues, report.Fixed, report.WontFix)
	return 0
}

// Check 以 root 为仓库根目录校验 path 处的真实接入记录；只有记录无法读取或解析时返回 error。
func Check(root, path string) (Report, error) {
	var report Report
	recs, err := load(path)
	if err != nil {
		return report, err
	}

	var problems []string
	add := func(format string, a ...any) { problems = append(problems, fmt.Sprintf(format, a...)) }

	if recs.SchemaVersion != 1 {
		add("schema_version 应为 1，实际为 %d", recs.SchemaVersion)
	}

	seen := map[string]bool{}
	ran := map[string]bool{} // 有已运行记录的客户端与文体
	issueIDs := map[string]bool{}
	for i, r := range recs.Runs {
		at := fmt.Sprintf("runs[%d]（%s）", i, r.ID)
		validClient := slices.Contains(clients, r.Client)
		validGenre := slices.Contains(genres, r.Genre)
		if !validClient {
			add("%s：client 应为 %s，实际为 %q", at, strings.Join(clients, " 或 "), r.Client)
		}
		if !validGenre {
			add("%s：genre 应为 %s，实际为 %q", at, strings.Join(genres, " 或 "), r.Genre)
		}
		if validClient && validGenre {
			combo := r.Client + "-" + r.Genre
			if r.ID != combo {
				add("%s：id 应为 %s", at, combo)
			}
			if seen[combo] {
				add("%s：重复的 %s × %s 运行记录", at, r.Client, r.Genre)
			}
			seen[combo] = true
		}
		if strings.TrimSpace(r.SkippedReason) != "" {
			// 未运行的组合只记录原因，不应有运行才有的字段。
			report.Skipped++
			if extra := runFields(r); len(extra) > 0 {
				add("%s：未运行的记录只能含 id、client、genre 与 skipped_reason，多出 %s", at, strings.Join(extra, "、"))
			}
			continue
		}
		report.Runs++
		ran[r.Client], ran[r.Genre] = true, true
		if strings.TrimSpace(r.ClientVersion) == "" {
			add("%s：缺少 client_version", at)
		}
		if strings.TrimSpace(r.WaCliVersion) == "" {
			add("%s：缺少 wa_cli_version", at)
		}
		if _, err := time.Parse("2006-01-02", r.Date); err != nil {
			add("%s：date 应为 YYYY-MM-DD，实际为 %q", at, r.Date)
		}
		checkChannels(at, r.Channels, add)

		want := exportFormats[r.Genre]
		if validGenre && r.Export.Format != want {
			add("%s：%s 的 export.format 应为 %s，实际为 %q", at, r.Genre, want, r.Export.Format)
		}
		if strings.TrimSpace(r.Export.Path) == "" {
			add("%s：缺少 export.path", at)
		}
		if len(r.Commands) == 0 {
			add("%s：commands 为空", at)
		} else if validGenre && !hasExport(r.Commands, want) {
			add("%s：commands 中没有以 %s 格式导出的 wa-cli export 命令", at, want)
		}

		for j, is := range r.Issues {
			iat := fmt.Sprintf("%s.issues[%d]（%s）", at, j, is.ID)
			report.Issues++
			switch {
			case strings.TrimSpace(is.ID) == "":
				add("%s：缺少 id", iat)
			case issueIDs[is.ID]:
				add("%s：问题 id 重复", iat)
			}
			issueIDs[is.ID] = true
			if strings.TrimSpace(is.Summary) == "" {
				add("%s：缺少 summary", iat)
			}
			fix, reason := strings.TrimSpace(is.FixChange), strings.TrimSpace(is.WontFixReason)
			switch {
			case fix != "" && reason != "":
				add("%s：fix_change 与 wont_fix_reason 只能填一个", iat)
			case fix == "" && reason == "":
				add("%s：需要 fix_change（已归档的缺陷变更名）或 wont_fix_reason（不修理由）", iat)
			case fix != "":
				if archivedChange(root, fix) {
					report.Fixed++
				} else {
					add("%s：fix_change 中的变更 %s 不在 %s 中（尚未归档或变更名有误）", iat, fix, archivedChanges)
				}
			default:
				report.WontFix++
			}
		}
	}
	for _, c := range clients {
		for _, g := range genres {
			if !seen[c+"-"+g] {
				add("缺少 %s × %s 的运行记录", c, g)
			}
		}
	}
	for _, k := range append(slices.Clone(clients), genres...) {
		if !ran[k] {
			add("%s 没有任何已运行的记录：每个客户端与每种文体都至少要实际运行一次", k)
		}
	}
	report.Problems = problems
	return report, nil
}

func load(path string) (Records, error) {
	var recs Records
	data, err := os.ReadFile(path)
	if err != nil {
		return recs, fmt.Errorf("读取真实接入记录失败：%w", err)
	}
	dec := yaml.NewDecoder(bytes.NewReader(data))
	dec.KnownFields(true)
	if err := dec.Decode(&recs); err != nil {
		if errors.Is(err, io.EOF) {
			return recs, fmt.Errorf("真实接入记录 %s 为空", path)
		}
		return recs, fmt.Errorf("解析真实接入记录 %s 失败：%w", path, err)
	}
	return recs, nil
}

func checkChannels(at string, list []string, add func(string, ...any)) {
	if len(list) == 0 {
		add("%s：channels 为空，应至少含 skill 或 mcp", at)
		return
	}
	dup := map[string]bool{}
	for _, c := range list {
		switch {
		case !channels[c]:
			add("%s：未知的接入方式 %q，只能是 skill 或 mcp", at, c)
		case dup[c]:
			add("%s：接入方式 %s 重复", at, c)
		}
		dup[c] = true
	}
}

// runFields 返回未运行的记录中出现的、只属于已运行记录的字段名。
func runFields(r Run) []string {
	var out []string
	for _, f := range []struct {
		name string
		set  bool
	}{
		{"client_version", r.ClientVersion != ""}, {"wa_cli_version", r.WaCliVersion != ""}, {"channels", len(r.Channels) > 0},
		{"date", r.Date != ""}, {"commands", len(r.Commands) > 0}, {"export", r.Export != (Export{})}, {"notes", r.Notes != ""},
		{"issues", len(r.Issues) > 0},
	} {
		if f.set {
			out = append(out, f.name)
		}
	}
	return out
}

// hasExport 判断命令序列中是否有以 format 导出的 wa-cli export 命令；省略 --format 时为 markdown。
func hasExport(commands []string, format string) bool {
	for _, cmd := range commands {
		fields := strings.Fields(cmd)
		if len(fields) < 2 || fields[0] != "wa-cli" || fields[1] != "export" {
			continue
		}
		got := "markdown"
		for i, f := range fields {
			if f == "--format" && i+1 < len(fields) {
				got = fields[i+1]
			} else if v, ok := strings.CutPrefix(f, "--format="); ok {
				got = v
			}
		}
		if got == format {
			return true
		}
	}
	return false
}

// archivedChange 判断 changes/archive 下是否有 <YYYY-MM-DD>-<name> 形式的目录。
func archivedChange(root, name string) bool {
	if strings.ContainsAny(name, `/\`) {
		return false
	}
	entries, err := os.ReadDir(filepath.Join(root, archivedChanges))
	if err != nil {
		return false
	}
	for _, e := range entries {
		date, ok := strings.CutSuffix(e.Name(), "-"+name)
		if !ok || !e.IsDir() {
			continue
		}
		if _, err := time.Parse("2006-01-02", date); err == nil {
			return true
		}
	}
	return false
}
