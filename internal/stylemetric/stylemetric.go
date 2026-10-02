// Package stylemetric 计算文风与节奏的确定性指标（Metric），并与作者文风画像（Style Profile）和同一作品已验收单元的统计对比，
// 判定文风漂移（Style Drift）。指标只衡量偏离，不评判好坏；本包不调用模型，只返回数据。
package stylemetric

import (
	"fmt"
	"math"
	"slices"
	"strings"
	"unicode"

	"github.com/kw12121212/writing-anything/internal/draft"
	"github.com/kw12121212/writing-anything/internal/feedback"
	"github.com/kw12121212/writing-anything/internal/learn"
	"github.com/kw12121212/writing-anything/internal/lint"
)

// MinAccepted 是使用同一作品已验收单元作为基准所需的最少单元数。
const MinAccepted = 3

// 基准的名称。
const (
	BaselineProfile  = "profile"
	BaselineAccepted = "accepted"
)

// functionWords 是统计频率的常用虚词。
var functionWords = []rune("的了是在和就都也而着")

// Metrics 是一个单元的标量指标。
type Metrics struct {
	Chars              int     `json:"chars"`
	SentenceMean       float64 `json:"sentenceMean"`
	SentenceStdev      float64 `json:"sentenceStdev"`
	ParagraphMean      float64 `json:"paragraphMean"`
	DialogueRatio      float64 `json:"dialogueRatio"`
	VocabularyRichness float64 `json:"vocabularyRichness"`
	FunctionWords      float64 `json:"functionWords"`
	FourChar           float64 `json:"fourChar"`
	AICliche           float64 `json:"aiCliche"`
	Punctuation        float64 `json:"punctuation"`
}

// Named 是一个有名字的指标取值。
type Named struct {
	Name  string
	Value float64
}

// Values 按固定顺序返回全部标量指标，供与已验收单元的统计逐项比较。
func (m Metrics) Values() []Named {
	return []Named{{"chars", float64(m.Chars)}, {"sentenceMean", m.SentenceMean}, {"sentenceStdev", m.SentenceStdev}, {"paragraphMean", m.ParagraphMean},
		{"dialogueRatio", m.DialogueRatio}, {"vocabularyRichness", m.VocabularyRichness}, {"functionWords", m.FunctionWords},
		{"fourChar", m.FourChar}, {"aiCliche", m.AICliche}, {"punctuation", m.Punctuation}}
}

// Paragraph 是一个段落的指标。
type Paragraph struct {
	Paragraph    int     `json:"paragraph"`
	Line         int     `json:"line"`
	Sentences    int     `json:"sentences"`
	SentenceMean float64 `json:"sentenceMean"`
}

func round(x float64, digits int) float64 {
	p := math.Pow(10, float64(digits))
	return math.Round(x*p) / p
}

func per1000(n, chars int) float64 {
	if chars == 0 {
		return 0
	}
	return round(float64(n)*1000/float64(chars), 2)
}

func meanStdev(xs []int) (float64, float64) {
	if len(xs) == 0 {
		return 0, 0
	}
	sum := 0.0
	for _, x := range xs {
		sum += float64(x)
	}
	m := sum / float64(len(xs))
	v := 0.0
	for _, x := range xs {
		v += (float64(x) - m) * (float64(x) - m)
	}
	return m, math.Sqrt(v / float64(len(xs)))
}

// Measure 计算正文 content 的指标：先按段落切分（编号与行号与 wa-cli provenance 相同），再去掉每段中的作者批注；aiTerms 为
// AI 腔词表。同样的正文与词表总是得到同样的结果。
func Measure(content []byte, aiTerms []string) (Metrics, []Paragraph) {
	var m Metrics
	paragraphs := []Paragraph{}
	var sentences, paraChars []int
	var bodies []string
	var text strings.Builder
	for i, p := range draft.Split(content) {
		body := strings.TrimSpace(feedback.StripComments(p.Text))
		if body == "" {
			continue // 只有批注的段落不计入，其余段落的编号仍与 wa-cli provenance 相同
		}
		ls := learn.SentenceLengths(body)
		mean, _ := meanStdev(ls)
		paragraphs = append(paragraphs, Paragraph{Paragraph: i + 1, Line: p.Line, Sentences: len(ls), SentenceMean: round(mean, 2)})
		sentences = append(sentences, ls...)
		paraChars = append(paraChars, learn.Chars(body))
		text.WriteString(body + "\n")
		bodies = append(bodies, body)
	}
	all := text.String()
	m.Chars = learn.Chars(all)
	mean, sd := meanStdev(sentences)
	m.SentenceMean, m.SentenceStdev = round(mean, 2), round(sd, 2)
	pm, _ := meanStdev(paraChars)
	m.ParagraphMean = round(pm, 2)

	inside, depth, fn, punct := 0, 0, 0, 0
	bigrams, types := 0, map[string]bool{}
	var run []rune
	four := 0
	flush := func() {
		if len(run) == 4 {
			four++
		}
		for i := 0; i+1 < len(run); i++ {
			bigrams++
			types[string(run[i:i+2])] = true
		}
		run = run[:0]
	}
	for _, body := range bodies {
		// 引号深度按段落计：多段引语每段开头都有前引号、只在最后一段闭合，不闭合的引号不能把后面所有段落都算作对话。
		depth = 0
		for _, r := range body {
			switch {
			case strings.ContainsRune("“「『", r):
				depth++
			case strings.ContainsRune("”」』", r):
				depth = max(0, depth-1)
			case depth > 0 && !unicode.IsSpace(r):
				inside++
			}
		}
	}
	for _, r := range all {
		if slices.Contains(functionWords, r) {
			fn++
		}
		if slices.Contains(learn.Punctuation, r) {
			punct++
		}
		if unicode.Is(unicode.Han, r) {
			run = append(run, r)
		} else {
			flush()
		}
	}
	flush()
	if m.Chars > 0 {
		m.DialogueRatio = round(float64(inside)/float64(m.Chars), 4)
	}
	if bigrams > 0 {
		m.VocabularyRichness = round(float64(len(types))/float64(bigrams), 4)
	}
	// AI 腔与 lint 的 ai-cliche 规则同一口径：一次扫描、去重、屏蔽代码与注释（workspace-artifacts/input-limits）。
	cliche := lint.CountTerms(string(content), aiTerms)
	m.FunctionWords, m.FourChar, m.AICliche, m.Punctuation = per1000(fn, m.Chars), per1000(four, m.Chars), per1000(cliche, m.Chars), per1000(punct, m.Chars)
	return m, paragraphs
}

// Finding 是一项文风漂移发现项。Paragraph 与 Line 对单元级发现项为 nil。
type Finding struct {
	Metric    string  `json:"metric"`
	Baseline  string  `json:"baseline"`
	Value     float64 `json:"value"`
	Mean      float64 `json:"mean"`
	Stdev     float64 `json:"stdev"`
	Threshold float64 `json:"threshold"`
	Paragraph *int    `json:"paragraph"`
	Line      *int    `json:"line"`
	Message   string  `json:"message"`
}

// drift 判定 value 是否偏离 mean 超过 k 倍 stdev；stdev 为 0 时不判定。
func drift(metric, baseline string, value, mean, stdev, k float64, p *Paragraph) *Finding {
	// 按输出精度比较：浮点运算会让完全相同的取值算出 1e-17 这样的标准差（go-project-structure/float-equality-in-stats）。
	if round(stdev, 4) <= 0 || math.Abs(value-mean) <= k*stdev {
		return nil
	}
	dir := "高于"
	if value < mean {
		dir = "低于"
	}
	where := ""
	f := &Finding{Metric: metric, Baseline: baseline, Value: value, Mean: round(mean, 4), Stdev: round(stdev, 4), Threshold: k}
	if p != nil {
		f.Paragraph, f.Line = &p.Paragraph, &p.Line
		where = fmt.Sprintf("第 %d 段（第 %d 行）", p.Paragraph, p.Line)
	}
	name := map[string]string{BaselineProfile: "作者文风画像", BaselineAccepted: "本作品已验收单元"}[baseline]
	f.Message = fmt.Sprintf("%s%s %v %s%s的均值 %v 超过 %v 个标准差（标准差 %v）；只说明偏离，不评判好坏", where, metric, value, dir, name, f.Mean, k, f.Stdev)
	return f
}

// num 把解码后的数值（float64 或 int）转换为 float64。
func num(v any) (float64, bool) {
	switch x := v.(type) {
	case float64:
		return x, true
	case int:
		return float64(x), true
	case int64:
		return float64(x), true
	}
	return 0, false
}

func dist(profile map[string]any, key string) (mean, stdev float64, ok bool) {
	d, _ := profile[key].(map[string]any)
	mean, ok1 := num(d["mean"])
	stdev, ok2 := num(d["stdev"])
	return mean, stdev, ok1 && ok2
}

// ProfileDrift 把单元的句长、段长与每个至少 2 句的段落的平均句长和文风画像 profile（style_profile 的结构）对比；profile 为 nil 时没有发现项。
func ProfileDrift(m Metrics, ps []Paragraph, profile map[string]any, k float64) []Finding {
	out := []Finding{}
	if profile == nil {
		return out
	}
	if mean, sd, ok := dist(profile, "sentences"); ok {
		if f := drift("sentenceMean", BaselineProfile, m.SentenceMean, mean, sd, k, nil); f != nil {
			out = append(out, *f)
		}
		for i := range ps {
			if ps[i].Sentences < 2 {
				continue
			}
			if f := drift("sentenceMean", BaselineProfile, ps[i].SentenceMean, mean, sd, k, &ps[i]); f != nil {
				out = append(out, *f)
			}
		}
	}
	if mean, sd, ok := dist(profile, "paragraphs"); ok {
		if f := drift("paragraphMean", BaselineProfile, m.ParagraphMean, mean, sd, k, nil); f != nil {
			out = append(out, *f)
		}
	}
	return out
}

// AcceptedDrift 把单元的每个标量指标与已验收单元 accepted 的均值与总体标准差对比；accepted 少于 MinAccepted 个时没有发现项。
func AcceptedDrift(m Metrics, accepted []Metrics, k float64) []Finding {
	out := []Finding{}
	if len(accepted) < MinAccepted {
		return out
	}
	for i, v := range m.Values() {
		var xs []float64
		for _, a := range accepted {
			xs = append(xs, a.Values()[i].Value)
		}
		if slices.Min(xs) == slices.Max(xs) {
			continue // 已验收单元的取值完全相同：标准差为 0，不判定
		}
		sum := 0.0
		for _, x := range xs {
			sum += x
		}
		mean := sum / float64(len(xs))
		vv := 0.0
		for _, x := range xs {
			vv += (x - mean) * (x - mean)
		}
		if f := drift(v.Name, BaselineAccepted, v.Value, mean, math.Sqrt(vv/float64(len(xs))), k, nil); f != nil {
			out = append(out, *f)
		}
	}
	return out
}
