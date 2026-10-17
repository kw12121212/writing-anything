package cli

import (
	"crypto/sha256"
	"encoding/json"
	"fmt"
	"io"
	"net/http"
	"net/http/httptest"
	"os"
	"path/filepath"
	"slices"
	"strings"
	"testing"
	"time"

	"go.yaml.in/yaml/v3"
)

// snapshotTree 返回目录下全部普通文件的相对路径 → 内容 SHA-256 映射。
func snapshotTree(t *testing.T, dir string) map[string]string {
	t.Helper()
	out := map[string]string{}
	err := filepath.Walk(dir, func(p string, info os.FileInfo, err error) error {
		if err != nil {
			return err
		}
		if info.IsDir() {
			return nil
		}
		rel, err := filepath.Rel(dir, p)
		if err != nil {
			return err
		}
		rel = filepath.ToSlash(rel)
		if rel == ".writing-anything/audit.log" || rel == ".writing-anything/audit.log.1" {
			return nil // 运维痕迹：两次重放的时间戳必然不同
		}
		data, err := os.ReadFile(p)
		if err != nil {
			return err
		}
		out[rel] = fmt.Sprintf("%x", sha256.Sum256(data))
		return nil
	})
	if err != nil {
		t.Fatal(err)
	}
	return out
}

// replayNovelCorpus 在 dir 中按序重放小说语料全部步骤并做逐步断言（见 replayCorpus），另加小说语料的规模下限。
// 调用方负责冻结 now 时间源。黄金比对（TestNovelCorpusGolden）与负向注入（TestNovelCorpusNegativeInjections）复用本助手。
func replayNovelCorpus(t *testing.T, dir string) {
	t.Helper()
	stats := replayCorpus(t, dir, filepath.Join("testdata", "e2e", "novel"), "")
	if stats.closeouts < 9 {
		t.Fatalf("成功的写后收口步数 = %d，语料九个场景都应收口", stats.closeouts)
	}
	if stats.steps < 200 {
		t.Fatalf("重放步数 = %d，语料应不少于 200 步（与 TestNovelCorpusScript 的下限互指）", stats.steps)
	}
}

// corpusReplayStats 是一次语料重放的计数：执行的步数与成功的写后收口步数。
type corpusReplayStats struct{ steps, closeouts int }

// replayCorpus 在 dir 中按序重放 corpusDir 下 commands.yaml 的步骤并做逐步断言（退出码、诊断码、stdout 片段、
// 变异命令 changedPaths、写后收口正文零疑似专有名词、死键自检）。stopBefore 非空时在命令串以它开头的第一步之前
// 停下（不执行该步，供负向注入在真实场景验收前介入；此时跳过死键自检）。调用方负责冻结 now 时间源。
// 小说语料（novel-e2e）与系列语料（series-e2e）共用本助手。
func replayCorpus(t *testing.T, dir, corpusDir, stopBefore string) corpusReplayStats {
	t.Helper()
	var script corpusScript
	raw, err := os.ReadFile(filepath.Join(corpusDir, "commands.yaml"))
	if err != nil {
		t.Fatal(err)
	}
	if err := yaml.Unmarshal(raw, &script); err != nil {
		t.Fatalf("commands.yaml 解析失败: %v", err)
	}
	if len(script.Stages) == 0 {
		t.Fatal("commands.yaml 没有阶段")
	}

	// 变异类命令：执行成功时必须报告 changedPaths（按重放实测校准；show/list/at 等只读子命令不在内）。
	mutating := map[[2]string]bool{
		{"init", ""}: true, {"new", ""}: true,
		{"source", "add"}: true, {"brief", "set"}: true,
		{"plan", "step"}: true, {"plan", "confirm"}: true,
		{"canon", "add"}: true, {"canon", "set"}: true, {"canon", "remove"}: true,
		{"unit", "add"}: true, {"unit", "move"}: true,
		{"scene", "contract"}: true, {"scene", "close-out"}: true,
		{"outline", "set"}: true, {"outline", "remove"}: true,
		{"draft", "write"}:   true,
		{"review", "record"}: true, {"review", "submit"}: true, // review plan 生成任务包，但维度无产出时合法地不写文件

		{"accept", ""}: true, {"advance", ""}: true,
		{"summary", "set"}:       true,
		{"volume", "checkpoint"}: true, {"volume", "confirm"}: true,
		{"decide", ""}:     true,
		{"threads", "add"}: true, {"threads", "set"}: true,
		{"state", "propose"}: true, {"state", "confirm"}: true, {"state", "set"}: true,
		{"knowledge", "propose"}: true, {"knowledge", "confirm"}: true, {"knowledge", "set"}: true,
		{"impact", "mark"}: true, {"impact", "clear"}: true,
		{"handoff", "write"}: true,
		{"assemble", ""}:     true, {"export", ""}: true,
		{"series", "set"}: true, {"series", "add"}: true, {"series", "fact"}: true,
	}
	// 键控：先按 (verb, subverb) 查，未命中再回退 (verb, "")——顶层动词（第二个 token 是位置
	// 参数或旗标，如 init/new/accept/advance/decide/assemble/export）只有 verb 形态的键。
	isMutating := func(c []string) bool {
		if len(c) == 0 {
			return false
		}
		if len(c) > 1 && mutating[[2]string{c[0], c[1]}] {
			return true
		}
		return mutating[[2]string{c[0], ""}]
	}

	keyHits := map[[2]string]int{}
	total, closeouts := 0, 0
	stopped := false
	for _, st := range script.Stages {
		if stopped {
			break
		}
		for _, step := range st.Steps {
			label := strings.Join(step.Command, " ")
			if stopBefore != "" && strings.HasPrefix(label, stopBefore) {
				stopped = true
				break
			}
			total++
			if wk := step.AuthorReview; wk != nil {
				replayAuthorReview(t, dir, *wk, st.Stage)
				continue
			}
			if wf := step.WriteFile; wf != nil {
				if step.InputRef == nil {
					t.Fatalf("[%s] write_file 步骤缺少 input_ref", st.Stage)
				}
				data, err := os.ReadFile(filepath.Join(corpusDir, *step.InputRef))
				if err != nil {
					t.Fatalf("[%s] 读取 %s: %v", st.Stage, *step.InputRef, err)
				}
				dst := filepath.Join(dir, filepath.FromSlash(*wf))
				if err := os.MkdirAll(filepath.Dir(dst), 0o755); err != nil {
					t.Fatal(err)
				}
				if err := os.WriteFile(dst, data, 0o644); err != nil {
					t.Fatal(err)
				}
				continue
			}
			input := ""
			if step.InputRef != nil {
				data, err := os.ReadFile(filepath.Join(corpusDir, *step.InputRef))
				if err != nil {
					t.Fatalf("[%s] 读取 %s: %v", st.Stage, *step.InputRef, err)
				}
				input = string(data)
			}
			code, res := runWSIn(t, dir, input, step.Command...)
			if code != step.ExpectExit {
				t.Fatalf("[%s] %s: exit = %d, 期望 %d; diagnostics = %+v", st.Stage, label, code, step.ExpectExit, res.Diagnostics)
			}
			if step.ExpectCode != nil && *step.ExpectCode != "" {
				for _, want := range strings.Split(*step.ExpectCode, ",") {
					want = strings.TrimSpace(want)
					found := false
					for _, d := range res.Diagnostics {
						if d.Code == want {
							found = true
							break
						}
					}
					if !found {
						t.Fatalf("[%s] %s: 期望诊断码 %s 不在 %+v", st.Stage, label, want, res.Diagnostics)
					}
				}
			}
			if step.StdoutContains != nil && !strings.Contains(res.stdout, *step.StdoutContains) {
				t.Fatalf("[%s] %s: stdout 缺少 %q", st.Stage, label, *step.StdoutContains)
			}
			// 写后收口的正文疑似专有名词扫描在语料全部场景上零误报（closeout/closeout-body-scan）。大纲 location
			// 取值（如守拙钟表行）未登记进设定集产生的大纲来源提示是语料既有状态，不在此断言。
			if len(step.Command) > 1 && step.Command[0] == "scene" && step.Command[1] == "close-out" && code == 0 {
				closeouts++
				var out struct {
					Closeout struct {
						Suspected []struct{ Name, Source string } `json:"suspected"`
					} `json:"closeout"`
				}
				if err := json.Unmarshal([]byte(res.stdout), &out); err != nil {
					t.Fatalf("[%s] %s: 解析收口输出: %v", st.Stage, label, err)
				}
				for _, sp := range out.Closeout.Suspected {
					if sp.Source == "draft" {
						t.Fatalf("[%s] %s: 语料正文不应有疑似专有名词提示: %+v", st.Stage, label, sp)
					}
				}
			}
			if code == 0 && isMutating(step.Command) {
				key := [2]string{step.Command[0], ""}
				if len(step.Command) > 1 && mutating[[2]string{step.Command[0], step.Command[1]}] {
					key = [2]string{step.Command[0], step.Command[1]}
				}
				keyHits[key]++
				if len(res.ChangedPaths) == 0 {
					t.Fatalf("[%s] %s: 变异命令成功执行但 changedPaths 为空", st.Stage, label)
				}
			}
		}
	}
	if stopBefore != "" {
		if !stopped {
			t.Fatalf("脚本中没有命令串以 %q 开头的步骤，无法在它之前停下", stopBefore)
		}
		return corpusReplayStats{total, closeouts}
	}

	// 死键自检：清单中登记的键若被语料的命令形态覆盖（verb 相同且 subverb 相同或为回退形态），
	// 必须至少命中一次——否则登记不可达，断言静默失效而测试仍然全绿。
	for key := range mutating {
		verbSeen, shapeSeen := false, false
		for _, st := range script.Stages {
			for _, step := range st.Steps {
				c := step.Command
				if len(c) == 0 || c[0] != key[0] {
					continue
				}
				verbSeen = true
				if key[1] == "" || (len(c) > 1 && c[1] == key[1]) {
					shapeSeen = true
				}
			}
		}
		if verbSeen && shapeSeen && keyHits[key] == 0 {
			t.Errorf("变异清单键 %v 被语料命令形态覆盖但从未命中（键控或语料漂移）", key)
		}
	}
	return corpusReplayStats{total, closeouts}
}

// replayAuthorReview 重放作者在本地阅读页（preview --serve）上的批量复核：取 wa-cli next 列出的待作者复核
// （Pending Author Review）项，带令牌与回环 Host 经 POST /author-review 全部提交，断言写入复核记录、next 不再列出。
func replayAuthorReview(t *testing.T, dir, workID, stage string) {
	t.Helper()
	pending := func() []string {
		_, res := runWSIn(t, dir, "", "next", workID)
		var out struct {
			Pending []struct {
				Key string `json:"key"`
			} `json:"pendingAuthorReviews"`
		}
		if err := json.Unmarshal([]byte(res.stdout), &out); err != nil {
			t.Fatal(err)
		}
		var keys []string
		for _, p := range out.Pending {
			keys = append(keys, p.Key)
		}
		return keys
	}
	keys := pending()
	if len(keys) == 0 {
		t.Fatalf("[%s] author_review %s: next 没有列出待作者复核项", stage, workID)
	}
	const host = "127.0.0.1:4170"
	s := &previewServer{inv: &invocation{stdout: io.Discard}, dir: dir, ref: workID, token: "corpus-token", hosts: []string{host},
		view: &previewView{Unit: workID, Units: []string{}}, done: make(chan struct{})}
	body, _ := json.Marshal(map[string][]string{"keys": keys})
	req := httptest.NewRequest(http.MethodPost, "http://"+host+"/author-review", strings.NewReader(string(body)))
	req.Header.Set("X-WA-Token", "corpus-token")
	rec := httptest.NewRecorder()
	s.ServeHTTP(rec, req)
	var res result
	if err := json.Unmarshal(rec.Body.Bytes(), &res); err != nil || rec.Code != http.StatusOK || !slices.Contains(res.ChangedPaths, ".writing-anything/author-reviews.yaml") {
		t.Fatalf("[%s] author_review %s: %d %s", stage, workID, rec.Code, rec.Body.String())
	}
	if left := pending(); len(left) != 0 {
		t.Fatalf("[%s] author_review %s: 复核后仍有待复核项 %v", stage, workID, left)
	}
}

// 小说语料重放：冻结时间源，连续重放两次，两棵工作区目录树逐文件 SHA-256 比对完全一致
// （含导出的 book.md 与 novel-export.md），作为可重复性的显式验证。
func TestNovelCorpusReplay(t *testing.T) {
	frozen := time.Date(2026, 6, 20, 12, 0, 0, 0, time.UTC)
	now = func() time.Time { return frozen }
	defer func() { now = time.Now }()

	dir1 := t.TempDir()
	replayNovelCorpus(t, dir1)
	dir2 := t.TempDir()
	replayNovelCorpus(t, dir2)

	assertSameTree(t, dir1, dir2)
	if _, err := os.Stat(filepath.Join(dir1, "book.md")); err != nil {
		t.Fatalf("组稿产物缺失: %v", err)
	}
	if _, err := os.Stat(filepath.Join(dir1, "novel-export.md")); err != nil {
		t.Fatalf("整书导出产物缺失: %v", err)
	}
}

// assertSameTree 断言两棵重放工作区目录树的文件集合相同、逐文件 SHA-256 一致，失配时给出首个差异文件的两份开头。
func assertSameTree(t *testing.T, dir1, dir2 string) {
	t.Helper()
	tree1 := snapshotTree(t, dir1)
	tree2 := snapshotTree(t, dir2)
	if len(tree1) != len(tree2) {
		t.Fatalf("两次重放文件数不一致: %d vs %d", len(tree1), len(tree2))
	}
	for rel, sum1 := range tree1 {
		sum2, ok := tree2[rel]
		if !ok {
			t.Fatalf("第二次重放缺少文件 %s", rel)
		}
		if sum1 != sum2 {
			a, _ := os.ReadFile(filepath.Join(dir1, filepath.FromSlash(rel)))
			b, _ := os.ReadFile(filepath.Join(dir2, filepath.FromSlash(rel)))
			head := func(b []byte) string {
				if len(b) > 400 {
					return string(b[:400]) + "…（截断）"
				}
				return string(b)
			}
			t.Fatalf("两次重放产物不一致: %s\n--- 第一次 ---\n%s\n--- 第二次 ---\n%s", rel, head(a), head(b))
		}
	}
}
