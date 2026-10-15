package cli

import (
	"encoding/json"
	"os"
	"path/filepath"
	"strings"
	"testing"
)

// cc-tb-2：review show 列出审稿必须逐项给结论的全部评分项（合并结果 + 写作法），按它写审稿不会被 review.missing-verdict 拒绝；
// cc-wm-2：不针对段落的结论 paragraphs 可以为空或省略（review-show、review-record）。
func TestReviewShowRubric(t *testing.T) {
	dir := reviewWork(t)
	writeFile(t, filepath.Join(dir, ".writing-anything", "works", "my-post", "brief.yaml"), completeBrief+"method: pyramid-scqa\n")
	code, res := runWS(t, dir, "review", "show", "my-post")
	var out struct {
		Rubric []struct {
			ID          string `json:"id"`
			Title       string `json:"title"`
			Description string `json:"description"`
			Blocking    bool   `json:"blocking"`
		} `json:"rubric"`
	}
	if err := json.Unmarshal([]byte(res.stdout), &out); err != nil {
		t.Fatal(err)
	}
	if code != 0 || len(out.Rubric) < 2 || out.Rubric[0].ID != "clarity" || out.Rubric[0].Title != "清晰" || out.Rubric[0].Description != "读者读一遍就能说出核心观点。" {
		t.Fatalf("review show rubric: code %d, %+v", code, out.Rubric)
	}
	var ids []string
	for _, it := range out.Rubric {
		ids = append(ids, it.ID)
	}
	if !strings.Contains(strings.Join(ids, ","), "conclusion-first") {
		t.Errorf("rubric lacks the method's items: %v", ids)
	}

	recordEveryRubricItem(t, dir, ids)
}

// recordEveryRubricItem 按评分项清单逐项给结论：第一项没有 paragraphs，第二项 paragraphs 为空数组，其余引用第 1 段。
func recordEveryRubricItem(t *testing.T, dir string, ids []string) {
	t.Helper()
	var b strings.Builder
	b.WriteString("rubric:\n")
	for i, id := range ids {
		switch i {
		case 0:
			b.WriteString("  - {item: " + id + ", verdict: pass, evidence: 依据写在证据里}\n")
		case 1:
			b.WriteString("  - {item: " + id + ", verdict: pass, evidence: 依据写在证据里, paragraphs: []}\n")
		default:
			b.WriteString("  - {item: " + id + ", verdict: pass, evidence: 清楚, paragraphs: [1]}\n")
		}
	}
	code, rec := runWSIn(t, dir, b.String(), "review", "record", "my-post")
	if code != 0 {
		t.Fatalf("review record: %+v", rec.Diagnostics)
	}
	if !strings.Contains(rec.stdout, `"paragraphs":[]`) || strings.Contains(rec.stdout, `"paragraphs":null`) {
		t.Errorf("review record output = %s", rec.stdout)
	}
	stored, _ := os.ReadFile(filepath.Join(dir, ".writing-anything", "works", "my-post", "review.yaml"))
	if strings.Count(string(stored), "paragraphs: []") != 2 {
		t.Errorf("review.yaml = %s", stored)
	}
	if code, res := runWS(t, dir, "validate"); code != 0 {
		t.Errorf("validate: %+v", res.Diagnostics)
	}
}

// cc-wm-2：不使用写作法时（只有 clarity 一项）省略 paragraphs 也能记录。
func TestReviewRecordOptionalParagraphs(t *testing.T) {
	dir := reviewWork(t)
	code, rec := runWSIn(t, dir, "rubric:\n  - {item: clarity, verdict: pass, evidence: 依据写在证据里}\n", "review", "record", "my-post")
	if code != 0 || !strings.Contains(rec.stdout, `"paragraphs":[]`) {
		t.Fatalf("review record: code %d, %+v", code, rec.Diagnostics)
	}
}

// 手写的 review.yaml 省略 paragraphs 时，review show 输出空数组而不是 null（read-path-invariants、shared-envelope）。
func TestReviewShowHandwrittenWithoutParagraphs(t *testing.T) {
	dir := reviewWork(t)
	code, rec := runWSIn(t, dir, "rubric:\n  - {item: clarity, verdict: pass, evidence: 清楚, paragraphs: [1]}\n", "review", "record", "my-post")
	if code != 0 {
		t.Fatalf("review record: %+v", rec.Diagnostics)
	}
	path := filepath.Join(dir, ".writing-anything", "works", "my-post", "review.yaml")
	data, _ := os.ReadFile(path)
	handwritten := strings.Replace(string(data), "paragraphs:\n            - 1\n", "", 1)
	handwritten = strings.Replace(handwritten, "    paragraphs: [1]\n", "", 1)
	if handwritten == string(data) {
		t.Fatalf("fixture did not drop paragraphs:\n%s", data)
	}
	writeFile(t, path, handwritten)
	code, res := runWS(t, dir, "review", "show", "my-post")
	if code != 0 || !strings.Contains(res.stdout, `"paragraphs":[]`) || strings.Contains(res.stdout, `"paragraphs":null`) {
		t.Errorf("review show: code %d, %s", code, res.stdout)
	}
}

// 审稿输入有问题时，诊断的下一步紧随用法之后给出列出评分项清单的 review show。
func TestReviewRecordNextShowsRubric(t *testing.T) {
	dir := reviewWork(t)
	code, res := runWSIn(t, dir, "rubric: []\n", "review", "record", "my-post")
	d := diag(t, res, "review.missing-verdict")
	if code != 1 || len(d.Next) < 2 || d.Next[1] != "wa-cli review show my-post --dir "+dir+" --json" {
		t.Errorf("next = %v", d.Next)
	}
}
