package cli

import (
	"os"
	"path/filepath"
	"strings"
	"testing"
	"time"
)

// 时钟注入：state 与 knowledge 的确认时间戳必须取 internal/cli 的可注入时钟，
// 冻结后两次执行的 decided_at 一致（重放可重复性的前提）。
func TestStateKnowledgeConfirmClockInjection(t *testing.T) {
	frozen := time.Date(2026, 6, 20, 12, 0, 0, 0, time.UTC)
	now = func() time.Time { return frozen }
	defer func() { now = time.Now }()

	build := func() (string, string) {
		t.Helper()
		dir := t.TempDir()
		for _, args := range [][]string{
			{"init", "--genre", "novel", "--gate-level", "strict"},
			{"new", "novel", "my-novel"},
			{"canon", "add", "my-novel", "--type", "item", "--id", "item-1", "--name", "怀表", "--text", "一块停摆的怀表"},
			{"unit", "add", "my-novel", "part", "part-1"},
			{"unit", "add", "my-novel/part-1", "chapter", "ch-1"},
			{"unit", "add", "my-novel/ch-1", "scene", "sc-1"},
			{"state", "propose", "my-novel", "--scene", "my-novel/sc-1", "--entry", "item-1", "--state", "stopped 变为 running", "--id", "st-1"},
			{"state", "confirm", "my-novel", "st-1", "--confirmed-by-author", "--confirmation", "作者：确认。"},
			{"knowledge", "propose", "my-novel", "--scene", "my-novel/sc-1", "--entry", "item-1", "--character", "reader", "--id", "kn-1"},
			{"knowledge", "confirm", "my-novel", "kn-1", "--confirmed-by-author", "--confirmation", "作者：确认。"},
		} {
			if code, res := runWS(t, dir, args...); code != 0 {
				t.Fatalf("%v: exit code = %d, result = %+v", args, code, res)
			}
		}
		rel := func(name string) string {
			data, err := os.ReadFile(filepath.Join(dir, ".writing-anything", "works", "my-novel", name))
			if err != nil {
				t.Fatal(err)
			}
			for _, line := range strings.Split(string(data), "\n") {
				if strings.Contains(line, "decided_at:") {
					return strings.TrimSpace(strings.TrimPrefix(strings.TrimSpace(line), "decided_at:"))
				}
			}
			t.Fatalf("%s 中没有 decided_at", name)
			return ""
		}
		return rel("state.yaml"), rel("knowledge.yaml")
	}

	want := `"` + frozen.UTC().Format(time.RFC3339) + `"`
	st1, kn1 := build()
	st2, kn2 := build()
	if st1 != want || kn1 != want {
		t.Fatalf("decided_at 未取冻结时钟: state = %s, knowledge = %s, 期望 %s", st1, kn1, want)
	}
	if st1 != st2 || kn1 != kn2 {
		t.Fatalf("冻结时钟下两次执行不一致: state %s vs %s, knowledge %s vs %s", st1, st2, kn1, kn2)
	}
}
