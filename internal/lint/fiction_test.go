package lint

import (
	"slices"
	"testing"

	"github.com/kw12121212/writing-anything/internal/workspace"
)

func runPOV(t *testing.T, content, person string) (*Result, []workspace.Problem) {
	t.Helper()
	return Run(content, rules(map[string]string{"p": "pov-consistency"}, nil), "draft.md", Input{POVPerson: person})
}

// 对话标点（Dialogue Punctuation）：半角引号对话、对话后置说话人时的句号、？与！保持不变与合规文本。
func TestDialoguePunctuation(t *testing.T) {
	r := rules(map[string]string{"d": "dialogue-punctuation"}, nil)
	res, ps := run(t, "\"我不去。\"她说。\n\n“我明天走。”他说。\n\n“你走吗？”他问。\n\n“我明天走，”他说。\n\n他说：“我在这里等了很久。”\n\n“走吧。”他慢慢地说。\n", r)
	if ps != nil {
		t.Fatal(ps)
	}
	// 第一行：半角引号对话（列 1）与句号应为逗号（句号在第 5 列）；第二行：全角引号只有句号问题（第 6 列）；
	// 问句与逗号结尾不报告；对话后没有说话人、说话人超过 4 个字（他慢慢地说）时不报告。
	want := []string{
		"d|1|1|\"我不去。\"|改用全角引号“…”",
		"d|1|5|。|，",
		"d|3|6|。|，",
	}
	if got := summary(res.Findings); !slices.Equal(got, want) {
		t.Fatalf("findings = %v", got)
	}
	// 混合引号：全角对话内部的半角引号按奇数序配对，仍报告半角发现项（fiction-rule-types 的配对定义）。
	res, ps = run(t, "“他说\"不去\"了。”\n", r)
	if ps != nil || len(res.Findings) != 1 || res.Findings[0].Column != 4 || res.Findings[0].Text != "\"不去\"" {
		t.Fatalf("mixed quotes: findings = %v, problems = %v", summary(res.Findings), ps)
	}
	// 屏蔽区域中的写法不检查：围栏代码块与行内代码里的半角引号对话。
	res, ps = run(t, "```\n\"我不去。\"她说。\n```\n\n行内 `\"我不去。\"她说。` 结束。\n", r)
	if ps != nil || len(res.Findings) != 0 {
		t.Fatalf("masked: findings = %v, problems = %v", summary(res.Findings), ps)
	}
	// 违例与合规夹具互为对照：合规文本零发现项。
	res, ps = run(t, "“我不去，”她说。\n\n“你走吗？”他问。\n", r)
	if ps != nil || len(res.Findings) != 0 {
		t.Fatalf("compliant: findings = %v, problems = %v", summary(res.Findings), ps)
	}
}

// 视角与人称一致（POV Consistency）：期望第三人称时叙述中的「我」报告，对话引号内不报告；第一人称不报告。
func TestPOVConsistency(t *testing.T) {
	content := "他推开门。\n\n“我在这里等了很久。”他说。\n\n我拿起灯。我们在夜里赶路。\n"
	res, ps := runPOV(t, content, "third")
	if ps != nil {
		t.Fatal(ps)
	}
	// 对话中的「我」不报告；叙述中的「我」与「我们」的「我」各报告一项。
	want := []string{"p|5|1|我|", "p|5|6|我|"}
	if got := summary(res.Findings); !slices.Equal(got, want) {
		t.Fatalf("findings = %v", got)
	}
	// 期望第一人称时不报告。
	if res, ps = runPOV(t, content, "first"); ps != nil || len(res.Findings) != 0 {
		t.Fatalf("first: findings = %v, problems = %v", summary(res.Findings), ps)
	}
	// pov 人称无法识别（second、未设置或写作简报不可用）时规则不执行，以 warning 提示且不改变退出码语义。
	for _, person := range []string{"second", ""} {
		res, ps = runPOV(t, content, person)
		if len(res.Findings) != 0 || len(ps) != 1 || ps[0].Code != CodePOVUnrecognized || ps[0].Severity != workspace.SeverityWarning || ps[0].Path != "draft.md" {
			t.Fatalf("person %q: findings = %v, problems = %+v", person, summary(res.Findings), ps)
		}
	}
	// 未闭合的全角引号不构成对话：其后的叙述「我」照常报告（成对引号之外的文字都是叙述）。
	if res, ps = runPOV(t, "“他说：我走了。我想回家。\n", "third"); ps != nil || len(res.Findings) != 2 {
		t.Fatalf("unclosed quote: findings = %v, problems = %v", summary(res.Findings), ps)
	}
	// 屏蔽区域中的「我」不报告。
	if res, ps = runPOV(t, "```\n我拿起灯。\n```\n\n行内 `我` 结束。\n", "third"); ps != nil || len(res.Findings) != 0 {
		t.Fatalf("masked: findings = %v, problems = %v", summary(res.Findings), ps)
	}
}

// 两种新规则类型都没有参数：给出任何参数时按 rule-params 报告，规则不执行。
func TestFictionRuleParams(t *testing.T) {
	r := rules(map[string]string{"d": "dialogue-punctuation", "p": "pov-consistency"},
		map[string]map[string]any{"d": {"max": 5}, "p": {"pronouns": []any{"我"}}})
	res, ps := Run("\"我不去。\"她说。\n我拿起灯。\n", r, "draft.md", Input{POVPerson: "third"})
	if len(res.Findings) != 0 || !slices.Contains(codes(ps), "rule.invalid-params") {
		t.Fatalf("findings = %v, problems = %+v", summary(res.Findings), ps)
	}
	for _, p := range ps {
		if p.Code == "rule.invalid-params" && p.Severity != workspace.SeverityError {
			t.Fatalf("severity = %v", p)
		}
	}
}
