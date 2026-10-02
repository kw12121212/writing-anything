package stylemetric

import (
	"math"
	"strings"
	"testing"
)

func TestMeasure(t *testing.T) {
	m, ps := Measure([]byte("他说：“走吧。”"), nil)
	if m.Chars != 8 || m.DialogueRatio != 0.375 {
		t.Fatalf("dialogue = %+v", m)
	}
	if len(ps) != 1 || ps[0].Paragraph != 1 || ps[0].Line != 1 {
		t.Fatalf("paragraphs = %+v", ps)
	}
	m, _ = Measure([]byte("一石二鸟，事半功倍。今天"), nil)
	if m.Chars != 12 || m.FourChar != round(2*1000.0/12, 2) {
		t.Fatalf("four char = %+v", m)
	}
	// 批注不计入；段落编号与行号按原文；虚词、AI 腔与标点按每千字计。
	body := "值得注意的是，我们的工具。<!-- @me: 太长了 -->\n\n<!-- @me: 整段批注 -->\n\n第二段很短。真的。\n"
	m, ps = Measure([]byte(body), []string{"值得注意的是"})
	if m.Chars != learnChars("值得注意的是，我们的工具。第二段很短。真的。") {
		t.Fatalf("chars = %d", m.Chars)
	}
	if len(ps) != 2 || ps[1].Paragraph != 3 || ps[1].Line != 5 || ps[1].Sentences != 2 || ps[1].SentenceMean != 4.5 {
		t.Fatalf("paragraphs = %+v", ps)
	}
	if m.AICliche != per1000(1, m.Chars) || m.FunctionWords != per1000(4, m.Chars) || m.Punctuation != per1000(4, m.Chars) {
		t.Fatalf("rates = %+v", m)
	}
	if m.SentenceMean != round((13+6+3)/3.0, 2) || m.VocabularyRichness <= 0 || m.VocabularyRichness > 1 {
		t.Fatalf("sentences = %+v", m)
	}
	if a, _ := Measure([]byte(body), []string{"值得注意的是"}); a != m {
		t.Fatal("not deterministic")
	}
	if e, ps := Measure(nil, nil); e != (Metrics{}) || ps == nil || len(ps) != 0 {
		t.Fatalf("empty = %+v %#v", e, ps)
	}
}

func learnChars(s string) int {
	n := 0
	for _, r := range s {
		if r != ' ' && r != '\n' {
			n++
		}
	}
	return n
}

func TestProfileDrift(t *testing.T) {
	profile := map[string]any{"sentences": map[string]any{"mean": 10, "stdev": 2.0}, "paragraphs": map[string]any{"mean": 30.0, "stdev": 0.0}}
	m := Metrics{SentenceMean: 20, ParagraphMean: 100}
	ps := []Paragraph{{Paragraph: 1, Line: 1, Sentences: 3, SentenceMean: 1}, {Paragraph: 2, Line: 3, Sentences: 1, SentenceMean: 1}, {Paragraph: 3, Line: 5, Sentences: 2, SentenceMean: 11}}
	fs := ProfileDrift(m, ps, profile, 2)
	if len(fs) != 2 {
		t.Fatalf("findings = %+v", fs)
	}
	if fs[0].Metric != "sentenceMean" || fs[0].Paragraph != nil || fs[0].Value != 20 || fs[0].Mean != 10 || fs[0].Stdev != 2 || fs[0].Threshold != 2 || !strings.Contains(fs[0].Message, "高于") || !strings.Contains(fs[0].Message, "不评判好坏") {
		t.Fatalf("unit finding = %+v", fs[0])
	}
	if *fs[1].Paragraph != 1 || *fs[1].Line != 1 || !strings.Contains(fs[1].Message, "低于") {
		t.Fatalf("paragraph finding = %+v", fs[1])
	}
	// 段长的标准差为 0：不判定；没有文风画像：没有发现项；阈值放宽后不报告。
	if got := ProfileDrift(m, ps, nil, 2); got == nil || len(got) != 0 {
		t.Fatalf("nil profile = %#v", got)
	}
	if got := ProfileDrift(m, ps, profile, 6); len(got) != 0 {
		t.Fatalf("wide threshold = %+v", got)
	}
}

func TestAcceptedDrift(t *testing.T) {
	base := []Metrics{{Chars: 100, DialogueRatio: 0}, {Chars: 110, DialogueRatio: 0.01}, {Chars: 90, DialogueRatio: 0.02}}
	m := Metrics{Chars: 100, DialogueRatio: 0.6}
	fs := AcceptedDrift(m, base, 2)
	if len(fs) != 1 || fs[0].Metric != "dialogueRatio" || fs[0].Baseline != "accepted" {
		t.Fatalf("findings = %+v", fs)
	}
	if got := AcceptedDrift(m, base[:2], 2); got == nil || len(got) != 0 {
		t.Fatalf("too few = %#v", got)
	}
}

func TestMeasureDetails(t *testing.T) {
	// 多段引语：只有最后一段闭合，旁白不算对话。
	m, _ := Measure([]byte("“第一段话。\n\n“第二段话。”\n\n旁白在这里。\n\n旁白也在这里。\n"), nil)
	if m.DialogueRatio > 0.5 {
		t.Fatalf("dialogue across paragraphs = %v", m.DialogueRatio)
	}
	// AI 腔与 lint 同一口径：去重、代码与注释中的不计。
	body := "总而言之，很好。\n\n`总而言之` 在代码里。<!-- 总而言之 -->\n"
	m, _ = Measure([]byte(body), []string{"总而言之", "总而言之"})
	if m.AICliche != per1000(1, m.Chars) {
		t.Fatalf("ai cliche = %v (chars %d)", m.AICliche, m.Chars)
	}
	// 句长与段长：两段，句长 3、3、6。
	m, _ = Measure([]byte("一二。三四。\n\n五六七八九。\n"), nil)
	if m.SentenceMean != 4 || m.SentenceStdev != round(math.Sqrt(2), 2) || m.ParagraphMean != 6 || m.VocabularyRichness != 1 {
		t.Fatalf("distribution = %+v", m)
	}
	if m, _ := Measure([]byte("的的的。\n"), nil); m.VocabularyRichness != 0.5 {
		t.Fatalf("richness = %v", m.VocabularyRichness)
	}
}

func TestIdenticalBaselineNotJudged(t *testing.T) {
	// 0.1 无法精确表示：三个相同取值的方差在浮点运算下不为 0，也不能判定。
	base := []Metrics{{DialogueRatio: 0.1}, {DialogueRatio: 0.1}, {DialogueRatio: 0.1}}
	if got := AcceptedDrift(Metrics{DialogueRatio: 0.1818}, base, 2); len(got) != 0 {
		t.Fatalf("identical baseline judged: %+v", got)
	}
	// 比例类指标的基准值保留四位小数。
	base = []Metrics{{DialogueRatio: 0.001}, {DialogueRatio: 0.002}, {DialogueRatio: 0.003}}
	got := AcceptedDrift(Metrics{DialogueRatio: 0.5}, base, 2)
	if len(got) != 1 || got[0].Mean != 0.002 || got[0].Stdev != 0.0008 {
		t.Fatalf("precision = %+v", got)
	}
}
