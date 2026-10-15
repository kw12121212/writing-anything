package main

import (
	"bytes"
	"os"
	"path/filepath"
	"strings"
	"testing"
)

// fixtureRoot 在临时目录中搭一个最小仓库：已归档变更 fix-sample，以及不应被当作已归档变更的目录与文件。
func fixtureRoot(t *testing.T) string {
	t.Helper()
	root := t.TempDir()
	for _, dir := range []string{"2026-10-12-fix-sample", "2026-10-12-other-fix-missing", "notadate-fix-other"} {
		if err := os.MkdirAll(filepath.Join(root, archivedChanges, dir), 0o755); err != nil {
			t.Fatal(err)
		}
	}
	// 与变更同名的普通文件不算已归档。
	if err := os.WriteFile(filepath.Join(root, archivedChanges, "2026-10-12-fix-file"), nil, 0o644); err != nil {
		t.Fatal(err)
	}
	return root
}

func validFixture(t *testing.T) string {
	t.Helper()
	data, err := os.ReadFile(filepath.Join("testdata", "valid.yaml"))
	if err != nil {
		t.Fatal(err)
	}
	return string(data)
}

// writeRecords 把 content 写成记录文件并返回路径。
func writeRecords(t *testing.T, root, content string) string {
	t.Helper()
	path := filepath.Join(root, "runs.yaml")
	if err := os.WriteFile(path, []byte(content), 0o644); err != nil {
		t.Fatal(err)
	}
	return path
}

func TestCheckValid(t *testing.T) {
	root := fixtureRoot(t)
	report, err := Check(root, writeRecords(t, root, validFixture(t)))
	if err != nil {
		t.Fatal(err)
	}
	if len(report.Problems) != 0 {
		t.Fatalf("合法夹具不应有问题：%v", report.Problems)
	}
	if report.Runs != 4 || report.Skipped != 0 || report.Issues != 2 || report.Fixed != 1 || report.WontFix != 1 {
		t.Errorf("统计错误：%+v", report)
	}
}

// TestCheckProblems 每个用例对合法夹具做一处修改，断言恰好报告预期的问题。
func TestCheckProblems(t *testing.T) {
	const codexWechat = "  - id: codex-wechat-marketing\n"
	cases := []struct {
		name     string
		old, new string
		want     []string
	}{
		{"schema 版本", "schema_version: 1", "schema_version: 2",
			[]string{"schema_version 应为 1，实际为 2"}},
		{"未知客户端", "    client: codex\n    client_version: codex-cli 0.157.1\n    wa_cli_version: wa-cli dev (commit 0000000)\n    genre: tech-blog",
			"    client: cursor\n    client_version: codex-cli 0.157.1\n    wa_cli_version: wa-cli dev (commit 0000000)\n    genre: tech-blog",
			[]string{`runs[2]（codex-tech-blog）：client 应为 claude-code 或 codex，实际为 "cursor"`, "缺少 codex × tech-blog 的运行记录"}},
		{"未知文体", "genre: wechat-marketing\n    channels: [skill]", "genre: novel\n    channels: [skill]",
			[]string{`runs[1]（claude-code-wechat-marketing）：genre 应为 tech-blog 或 wechat-marketing，实际为 "novel"`, "缺少 claude-code × wechat-marketing 的运行记录"}},
		{"id 与组合不符", "id: codex-tech-blog", "id: codex-blog",
			[]string{"runs[2]（codex-blog）：id 应为 codex-tech-blog"}},
		{"缺少客户端版本", "client_version: codex-cli 0.157.1\n    wa_cli_version: wa-cli dev (commit 0000000)\n    genre: tech-blog",
			"client_version: \"\"\n    wa_cli_version: wa-cli dev (commit 0000000)\n    genre: tech-blog",
			[]string{"runs[2]（codex-tech-blog）：缺少 client_version"}},
		{"缺少 wa-cli 版本", "wa_cli_version: wa-cli dev (commit 0000000)\n    genre: tech-blog\n    channels: [mcp]",
			"wa_cli_version: \" \"\n    genre: tech-blog\n    channels: [mcp]",
			[]string{"runs[2]（codex-tech-blog）：缺少 wa_cli_version"}},
		{"日期格式", "channels: [mcp]\n    date: \"2026-10-13\"", "channels: [mcp]\n    date: \"2026/10/13\"",
			[]string{`runs[2]（codex-tech-blog）：date 应为 YYYY-MM-DD，实际为 "2026/10/13"`}},
		{"接入方式为空", "channels: [mcp]", "channels: []",
			[]string{"runs[2]（codex-tech-blog）：channels 为空，应至少含 skill 或 mcp"}},
		{"未知接入方式", "channels: [skill]", "channels: [cli]",
			[]string{`runs[1]（claude-code-wechat-marketing）：未知的接入方式 "cli"，只能是 skill 或 mcp`}},
		{"重复接入方式", "channels: [mcp, skill]", "channels: [mcp, mcp]",
			[]string{"runs[3]（codex-wechat-marketing）：接入方式 mcp 重复"}},
		{"导出格式与文体不符", "format: wechat-html\n      path: .writing-anything/exports/promo.html",
			"format: markdown\n      path: .writing-anything/exports/promo.html",
			[]string{`runs[1]（claude-code-wechat-marketing）：wechat-marketing 的 export.format 应为 wechat-html，实际为 "markdown"`}},
		{"缺少导出路径", "path: out/promo.html", `path: ""`,
			[]string{"runs[3]（codex-wechat-marketing）：缺少 export.path"}},
		{"命令为空", "commands:\n      - wa-cli export promo --format wechat-html\n", "commands: []\n",
			[]string{"runs[1]（claude-code-wechat-marketing）：commands 为空"}},
		{"没有导出命令", "      - wa-cli export promo --format wechat-html\n", "      - wa-cli preview promo --serve\n",
			[]string{"runs[1]（claude-code-wechat-marketing）：commands 中没有以 wechat-html 格式导出的 wa-cli export 命令"}},
		{"导出命令格式不符", "wa-cli export http-reuse --format=markdown --json", "wa-cli export http-reuse --format=text --json",
			[]string{"runs[2]（codex-tech-blog）：commands 中没有以 markdown 格式导出的 wa-cli export 命令"}},
		{"问题缺少 id", "id: cc-wm-1", `id: ""`,
			[]string{"runs[1]（claude-code-wechat-marketing）.issues[0]（）：缺少 id"}},
		{"问题 id 重复", "id: cc-wm-1", "id: cc-tb-1",
			[]string{"runs[1]（claude-code-wechat-marketing）.issues[0]（cc-tb-1）：问题 id 重复"}},
		{"问题缺少摘要", "summary: 不修的问题", `summary: ""`,
			[]string{"runs[1]（claude-code-wechat-marketing）.issues[0]（cc-wm-1）：缺少 summary"}},
		{"既无修复也无理由", "        wont_fix_reason: 属于新需求，转入后续里程碑\n", "",
			[]string{"runs[1]（claude-code-wechat-marketing）.issues[0]（cc-wm-1）：需要 fix_change（已归档的缺陷变更名）或 wont_fix_reason（不修理由）"}},
		{"修复与理由同时出现", "        wont_fix_reason: 属于新需求，转入后续里程碑\n", "        wont_fix_reason: 属于新需求，转入后续里程碑\n        fix_change: fix-sample\n",
			[]string{"runs[1]（claude-code-wechat-marketing）.issues[0]（cc-wm-1）：fix_change 与 wont_fix_reason 只能填一个"}},
		{"修复变更未归档", "fix_change: fix-sample", "fix_change: fix-missing",
			[]string{"runs[0]（claude-code-tech-blog）.issues[0]（cc-tb-1）：fix_change 中的变更 fix-missing 不在 .strict-spec-driven/changes/archive 中（尚未归档或变更名有误）"}},
		{"日期前缀不合法", "fix_change: fix-sample", "fix_change: fix-other",
			[]string{"runs[0]（claude-code-tech-blog）.issues[0]（cc-tb-1）：fix_change 中的变更 fix-other 不在 .strict-spec-driven/changes/archive 中（尚未归档或变更名有误）"}},
		{"归档项不是目录", "fix_change: fix-sample", "fix_change: fix-file",
			[]string{"runs[0]（claude-code-tech-blog）.issues[0]（cc-tb-1）：fix_change 中的变更 fix-file 不在 .strict-spec-driven/changes/archive 中（尚未归档或变更名有误）"}},
		{"变更名含路径分隔符", "fix_change: fix-sample", "fix_change: 2026-10-12-fix-sample/../fix-sample",
			[]string{"runs[0]（claude-code-tech-blog）.issues[0]（cc-tb-1）：fix_change 中的变更 2026-10-12-fix-sample/../fix-sample 不在 .strict-spec-driven/changes/archive 中（尚未归档或变更名有误）"}},
	}
	valid := validFixture(t)
	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			if strings.Count(valid, tc.old) != 1 {
				t.Fatalf("夹具中应恰好出现一次：%q", tc.old)
			}
			root := fixtureRoot(t)
			report, err := Check(root, writeRecords(t, root, strings.Replace(valid, tc.old, tc.new, 1)))
			if err != nil {
				t.Fatal(err)
			}
			if strings.Join(report.Problems, "\n") != strings.Join(tc.want, "\n") {
				t.Errorf("问题不符：\n得到：%q\n期望：%q", report.Problems, tc.want)
			}
		})
	}

	t.Run("缺少组合", func(t *testing.T) {
		root := fixtureRoot(t)
		content := valid[:strings.Index(valid, codexWechat)]
		report, err := Check(root, writeRecords(t, root, content))
		if err != nil {
			t.Fatal(err)
		}
		want := []string{"缺少 codex × wechat-marketing 的运行记录"}
		if strings.Join(report.Problems, "\n") != strings.Join(want, "\n") {
			t.Errorf("问题不符：%q", report.Problems)
		}
	})

	t.Run("重复组合", func(t *testing.T) {
		root := fixtureRoot(t)
		dup := valid[strings.Index(valid, codexWechat):]
		report, err := Check(root, writeRecords(t, root, valid+dup))
		if err != nil {
			t.Fatal(err)
		}
		want := []string{"runs[4]（codex-wechat-marketing）：重复的 codex × wechat-marketing 运行记录"}
		if strings.Join(report.Problems, "\n") != strings.Join(want, "\n") {
			t.Errorf("问题不符：%q", report.Problems)
		}
	})
}

func TestCheckLoadErrors(t *testing.T) {
	root := fixtureRoot(t)
	cases := []struct {
		name, content, want string
	}{
		{"空文件", "", "为空"},
		{"未知字段", strings.Replace(validFixture(t), "notes: 夹具", "note: 夹具", 1), "field note not found"},
		{"不是 YAML 映射", "- a\n", "解析真实接入记录"},
	}
	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			_, err := Check(root, writeRecords(t, root, tc.content))
			if err == nil || !strings.Contains(err.Error(), tc.want) {
				t.Errorf("错误应含 %q，实际为 %v", tc.want, err)
			}
		})
	}
	if _, err := Check(root, filepath.Join(root, "missing.yaml")); err == nil || !strings.Contains(err.Error(), "读取真实接入记录失败") {
		t.Errorf("缺少文件应报告读取失败，实际为 %v", err)
	}
}

func TestRun(t *testing.T) {
	root := fixtureRoot(t)
	valid := writeRecords(t, root, validFixture(t))
	bad := filepath.Join(root, "bad.yaml")
	if err := os.WriteFile(bad, []byte("schema_version: 1\nruns: []\n"), 0o644); err != nil {
		t.Fatal(err)
	}
	cases := []struct {
		name       string
		args       []string
		code       int
		stdout     string
		stderr     string
		emptyOther bool
	}{
		{"通过", []string{"-root", root, "-records", valid}, 0, "dogfood：4 次运行记录齐备，0 个组合写明未运行原因（问题 2 个：已修复 1，不修 1）", "", true},
		{"相对路径相对 root", []string{"-root", root, "-records", "runs.yaml"}, 0, "4 次运行记录齐备", "", true},
		{"记录不全", []string{"-root", root, "-records", bad}, 1, "", "真实接入记录发现 8 个问题", true},
		{"读取失败", []string{"-root", root, "-records", "missing.yaml"}, 1, "", "读取真实接入记录失败", true},
		{"未知参数", []string{"extra"}, 2, "", "dogfood：未知参数：extra", true},
		{"未知标志", []string{"-nope"}, 2, "", "nope", false},
	}
	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			var stdout, stderr bytes.Buffer
			if code := run(tc.args, &stdout, &stderr); code != tc.code {
				t.Fatalf("退出码 %d，期望 %d；stderr：%s", code, tc.code, stderr.String())
			}
			if tc.stdout != "" && !strings.Contains(stdout.String(), tc.stdout) {
				t.Errorf("stdout 应含 %q：%s", tc.stdout, stdout.String())
			}
			if tc.stderr != "" && !strings.Contains(stderr.String(), tc.stderr) {
				t.Errorf("stderr 应含 %q：%s", tc.stderr, stderr.String())
			}
			if tc.emptyOther && tc.code == 0 && stderr.Len() != 0 {
				t.Errorf("成功时 stderr 应为空：%s", stderr.String())
			}
			if tc.emptyOther && tc.code != 0 && stdout.Len() != 0 {
				t.Errorf("失败时 stdout 应为空：%s", stdout.String())
			}
		})
	}
}

// skippedFixture 把合法夹具中的 codex-wechat-marketing 改为写明原因的未运行记录。
func skippedFixture(t *testing.T) string {
	t.Helper()
	valid := validFixture(t)
	return valid[:strings.Index(valid, "  - id: codex-wechat-marketing\n")] +
		"  - id: codex-wechat-marketing\n    client: codex\n    genre: wechat-marketing\n    skipped_reason: 作者因 Codex 额度不足取消\n"
}

func TestCheckSkipped(t *testing.T) {
	root := fixtureRoot(t)
	report, err := Check(root, writeRecords(t, root, skippedFixture(t)))
	if err != nil {
		t.Fatal(err)
	}
	if len(report.Problems) != 0 || report.Runs != 3 || report.Skipped != 1 {
		t.Fatalf("report = %+v", report)
	}
	var stdout, stderr bytes.Buffer
	if code := run([]string{"-root", root, "-records", "runs.yaml"}, &stdout, &stderr); code != 0 || !strings.Contains(stdout.String(), "3 次运行记录齐备，1 个组合写明未运行原因") {
		t.Errorf("run: %d %s %s", code, stdout.String(), stderr.String())
	}

	cases := []struct {
		name, old, new string
		want           []string
	}{
		{"未运行带运行字段", "    skipped_reason: 作者因 Codex 额度不足取消\n", "    skipped_reason: 作者因 Codex 额度不足取消\n    client_version: codex-cli 0.157.1\n    commands: [wa-cli init]\n    issues: [{id: x, summary: y, wont_fix_reason: z}]\n",
			[]string{"runs[3]（codex-wechat-marketing）：未运行的记录只能含 id、client、genre 与 skipped_reason，多出 client_version、commands、issues"}},
		{"原因为空白", "    skipped_reason: 作者因 Codex 额度不足取消\n", "    skipped_reason: \" \"\n",
			[]string{"runs[3]（codex-wechat-marketing）：缺少 client_version", "runs[3]（codex-wechat-marketing）：缺少 wa_cli_version",
				`runs[3]（codex-wechat-marketing）：date 应为 YYYY-MM-DD，实际为 ""`, "runs[3]（codex-wechat-marketing）：channels 为空，应至少含 skill 或 mcp",
				"runs[3]（codex-wechat-marketing）：wechat-marketing 的 export.format 应为 wechat-html，实际为 \"\"", "runs[3]（codex-wechat-marketing）：缺少 export.path",
				"runs[3]（codex-wechat-marketing）：commands 为空"}},
	}
	base := skippedFixture(t)
	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			report, err := Check(root, writeRecords(t, root, strings.Replace(base, tc.old, tc.new, 1)))
			if err != nil {
				t.Fatal(err)
			}
			if strings.Join(report.Problems, "\n") != strings.Join(tc.want, "\n") {
				t.Errorf("问题不符：\n得到：%q\n期望：%q", report.Problems, tc.want)
			}
		})
	}

	// 某个客户端或某种文体没有任何已运行的记录。
	t.Run("客户端全部未运行", func(t *testing.T) {
		content := strings.Replace(base, "  - id: codex-tech-blog\n", "  - id: codex-tech-blog\n    skipped_reason: 额度不足\n", 1)
		// 去掉 codex-tech-blog 的运行字段：截到下一条记录之前。
		start := strings.Index(content, "    skipped_reason: 额度不足\n") + len("    skipped_reason: 额度不足\n")
		end := strings.Index(content, "  - id: codex-wechat-marketing\n")
		content = content[:start] + "    client: codex\n    genre: tech-blog\n" + content[end:]
		report, err := Check(root, writeRecords(t, root, content))
		if err != nil {
			t.Fatal(err)
		}
		want := []string{"codex 没有任何已运行的记录：每个客户端与每种文体都至少要实际运行一次"}
		if strings.Join(report.Problems, "\n") != strings.Join(want, "\n") || report.Skipped != 2 {
			t.Errorf("problems = %q, report = %+v", report.Problems, report)
		}
	})
}

// TestDogfoodRecords 校验仓库中的真实接入记录 docs/dogfood/runs.yaml：每个 客户端 × 文体 组合恰好一条，已运行的走到导出、
// 记录齐全，未运行的写明原因，每个问题都链接到已归档的缺陷变更或写明不修理由（里程碑 real-use-and-release 的完成标准）。
func TestDogfoodRecords(t *testing.T) {
	root := filepath.Join("..", "..", "..")
	report, err := Check(root, filepath.Join(root, DefaultRecords))
	if err != nil {
		t.Fatalf("读取真实接入记录失败：%v", err)
	}
	for _, p := range report.Problems {
		t.Error(p)
	}
	// 完成标准点名了三次运行；唯一未运行的组合是作者因 Codex 额度不足取消的 Codex × 公众号营销文章。
	recs, err := load(filepath.Join(root, DefaultRecords))
	if err != nil {
		t.Fatal(err)
	}
	var skipped []string
	for _, r := range recs.Runs {
		if strings.TrimSpace(r.SkippedReason) != "" {
			skipped = append(skipped, r.ID)
		}
	}
	if report.Runs != 3 || strings.Join(skipped, ",") != "codex-wechat-marketing" {
		t.Errorf("运行 %d 次、未运行的组合 %v；完成标准要求 Claude Code × 两种文体与 Codex × 技术博客三次运行，只有 codex-wechat-marketing 未运行", report.Runs, skipped)
	}
}
