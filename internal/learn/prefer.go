package learn

import (
	"fmt"
	"math"
	"slices"
	"strings"

	"github.com/kw12121212/writing-anything/internal/draft"
	"github.com/kw12121212/writing-anything/internal/feedback"
	"github.com/kw12121212/writing-anything/internal/work"
	"github.com/kw12121212/writing-anything/internal/workspace"
)

// 偏好归纳的阈值。
const (
	minPairs          = 3    // 至少在几个修改对中被删除，才算反复删除；长度变化也至少需要几个修改对
	maxRemovedTerms   = 10   // 删除词条目的上限
	shorterRatio      = 0.8  // 修改后与修改前的字数比不大于它时为写得更短
	longerRatio       = 1.25 // 不小于它时为写得更长
	minFeedback       = 3    // 反馈主题词至少出现在几条反馈中；长段落反馈至少几条
	maxFeedbackTerms  = 5    // 反馈主题词条目的上限
	longParagraph     = 1.5  // 长于所在正文平均段长的倍数
	maxEvidence       = 3    // 每个条目的证据数
	maxEvidenceLength = 80   // 每条证据片段的字符数
)

// Observed 是一次归纳用到的输入规模。
type Observed struct {
	Edits    int `json:"edits"`
	Feedback int `json:"feedback"`
}

// pair 是一个修改对及其所在单元。
type pair struct {
	unit string
	draft.Edit
}

// signal 是参与归纳的一条反馈：所在段落的字数与所在正文的平均段长（锚点未定位时为 0）。
type signal struct {
	unit, id, text string
	paraChars      int
	meanChars      float64
}

// Prefer 对工作区中全部修改对与作者反馈做确定性归纳，把作者偏好（Author Preference）条目合并进提议 id。pack 为目标规范包（提议
// 不存在时必需）。返回提议、输入规模、变更的路径与 warning 级问题；有 error 级问题时不写文件。
func Prefer(root, id, pack string) (*Proposal, Observed, []string, []workspace.Problem) {
	var obs Observed
	rel := Path(id)
	pr, ps := Load(root, rel)
	switch {
	case ps != nil:
		return nil, obs, nil, ps
	case pr == nil && pack == "":
		return nil, obs, nil, []workspace.Problem{errorAt(CodeMissingPack, "", "pack", "提议 "+id+" 还不存在；首次运行必须用 --pack 给出要写入的作者规范包 id")}
	case pr != nil && pack != "" && pack != pr.Pack:
		return nil, obs, nil, []workspace.Problem{errorAt(CodePackMismatch, "", "pack", fmt.Sprintf("提议 %s 的目标规范包是 %s，不是 %s；请换一个提议 id", id, pr.Pack, pack))}
	case pr == nil:
		pr = &Proposal{ID: id, Pack: pack, Path: rel, Sources: []string{}, Items: []Item{}}
	}
	if p := checkTarget(root, pr.Pack); p != nil {
		return nil, obs, nil, []workspace.Problem{*p}
	}
	pairs, signals, ps := gather(root)
	if ps != nil {
		return nil, obs, nil, ps
	}
	obs = Observed{Edits: len(pairs), Feedback: len(signals)}
	found := slices.Concat(removedTerms(pairs), lengthChange(pairs), feedbackTerms(signals), longParagraphs(signals))
	next := *pr
	next.Items = slices.Clone(pr.Items)
	var warnings []workspace.Problem
	for _, it := range found {
		// 同一偏好按 key 与 kind 识别：手写提议中 key 相同但种类不同的条目不被改写（workspace-artifacts/read-path-invariants）。
		i := slices.IndexFunc(next.Items, func(o Item) bool { return o.Key == it.Key && o.Kind == it.Kind })
		switch {
		case i < 0:
			prefix := "style"
			if it.Kind == KindBannedTerm {
				prefix = "avoid"
			}
			it.ID, it.Status = nextID(next.Items, prefix), StatusProposed
			next.Items = append(next.Items, it)
		case next.Items[i].Status == StatusProposed:
			next.Items[i].Text, next.Items[i].Term, next.Items[i].Evidence = it.Text, it.Term, it.Evidence
		case next.Items[i].Status == StatusRejected:
			warnings = append(warnings, workspace.Problem{Code: CodePreviouslyRejected, Severity: workspace.SeverityWarning, Path: rel, Field: "items",
				Message: fmt.Sprintf("偏好 %s 已被作者拒绝（条目 %s），不再重复提出", it.Key, next.Items[i].ID)})
		}
	}
	if pr.doc == nil && len(next.Items) == 0 {
		return &next, obs, nil, warnings // 没有归纳出任何偏好时不新建空的提议文件
	}
	changed, ps := next.save(root)
	if ps != nil {
		return nil, obs, nil, ps
	}
	return &next, obs, changed, warnings
}

// gather 读取每个可加载作品的每个单元的修改对（修改记录与当前尚未记入的，去重）与参与归纳的反馈。
func gather(root string) ([]pair, []signal, []workspace.Problem) {
	works, _, _ := work.LoadAll(root)
	var pairs []pair
	for _, w := range works {
		for _, u := range w.Units() {
			logged, ps := draft.LoadEdits(root, w, u)
			if ps != nil {
				return nil, nil, ps
			}
			pending, ps := draft.Pairs(root, w, u)
			if ps != nil {
				return nil, nil, ps
			}
			seen := map[[2]string]bool{}
			for _, e := range slices.Concat(logged, pending) {
				if k := [2]string{e.Before, e.After}; !seen[k] {
					seen[k] = true
					pairs = append(pairs, pair{u.Ref, e})
				}
			}
		}
	}
	items, ps := feedback.ListAll(root, works)
	if ps != nil {
		return nil, nil, ps
	}
	lengths := map[string][]int{} // 单元引用 → 各段字数
	byRef := map[string]unitOf{}
	for _, w := range works {
		for _, u := range w.Units() {
			byRef[u.Ref] = unitOf{w, u}
		}
	}
	var signals []signal
	for _, it := range items {
		if it.Kind == "praise" || it.Status == "rejected" {
			continue
		}
		s := signal{unit: it.Unit, id: it.ID, text: it.Text}
		if it.Paragraph > 0 {
			if _, ok := lengths[it.Unit]; !ok {
				lengths[it.Unit] = paragraphChars(root, byRef[it.Unit])
			}
			if ls := lengths[it.Unit]; it.Paragraph <= len(ls) {
				s.paraChars, s.meanChars = ls[it.Paragraph-1], mean(ls)
			}
		}
		signals = append(signals, s)
	}
	return pairs, signals, nil
}

// unitOf 是一个单元及其所在作品。
type unitOf struct {
	w *work.Work
	u *work.Unit
}

// paragraphChars 返回单元正文各段的字数；单元不存在或没有正文时为空。
func paragraphChars(root string, uo unitOf) []int {
	if uo.u == nil {
		return nil
	}
	content, ok, _ := draft.Read(root, uo.w, uo.u)
	if !ok {
		return nil
	}
	var out []int
	for _, p := range draft.Split(content) {
		out = append(out, Chars(p.Text))
	}
	return out
}

func mean(xs []int) float64 {
	if len(xs) == 0 {
		return 0
	}
	sum := 0
	for _, x := range xs {
		sum += x
	}
	return float64(sum) / float64(len(xs))
}

// excerpt 返回证据片段：prefix 加上 s 中的一段，合计至多 maxEvidenceLength 个字符（省略号也计入），空白合并为一个空格；
// 给出 term 时片段围绕 term 第一次出现的位置截取，保证片段里能看到它。
func excerpt(prefix, s, term string) string {
	rs := []rune(strings.Join(strings.Fields(s), " "))
	limit := maxEvidenceLength - len([]rune(prefix))
	if len(rs) <= limit {
		return prefix + string(rs)
	}
	avail := limit - 2 // 两端可能各有一个省略号
	start := 0
	if term != "" {
		if i := strings.Index(strings.ToLower(string(rs)), term); i >= 0 {
			at := len([]rune(strings.ToLower(string(rs))[:i]))
			start = max(0, min(at-(avail-len([]rune(term)))/2, len(rs)-avail))
		}
	}
	out := string(rs[start : start+avail])
	if start > 0 {
		out = "…" + out
	}
	if start+avail < len(rs) {
		out += "…"
	}
	return prefix + out
}

// display 返回计数用的词在条目中的写法：拉丁字母词去掉内部前缀。
func display(t string) string { return strings.TrimPrefix(t, "\x00") }

// dedupe 去掉被计数相同的更长汉字子串包含的子串。
func dedupe(counts map[string]int) {
	removed := map[string]bool{}
	for u, c := range counts {
		if strings.HasPrefix(u, "\x00") {
			continue
		}
		rs := []rune(u)
		for l := 2; l < len(rs); l++ {
			for i := 0; i+l <= len(rs); i++ {
				if t := string(rs[i : i+l]); counts[t] == c {
					removed[t] = true
				}
			}
		}
	}
	for t := range removed {
		delete(counts, t)
	}
}

// top 返回计数不少于 min 的词，按计数降序、再按字典序取前 n 个。
func top(counts map[string]int, min, n int) []string {
	var out []string
	for t, c := range counts {
		if c >= min {
			out = append(out, t)
		}
	}
	slices.SortFunc(out, func(a, b string) int {
		if counts[a] != counts[b] {
			return counts[b] - counts[a]
		}
		return strings.Compare(display(a), display(b))
	})
	if len(out) > n {
		out = out[:n]
	}
	return out
}

// removedTerms 归纳反复被删除的词：在不少于 minPairs 个修改对中被删除、且没有在任何修改对中被加入。
func removedTerms(pairs []pair) []Item {
	removed, added := map[string]int{}, map[string]bool{}
	removedIn := make([]map[string]bool, len(pairs)) // 每个修改对删除了哪些词，用于挑选证据
	for i, p := range pairs {
		before, after := map[string]int{}, map[string]int{}
		countTerms(p.Before, before)
		countTerms(p.After, after)
		removedIn[i] = map[string]bool{}
		for t, c := range before {
			if c > after[t] {
				removed[t]++
				removedIn[i][t] = true
			}
		}
		for t, c := range after {
			if c > before[t] {
				added[t] = true
			}
		}
	}
	for t := range added {
		delete(removed, t)
	}
	for t, c := range removed {
		if c < minPairs {
			delete(removed, t)
		}
	}
	dedupe(removed)
	var out []Item
	for _, t := range top(removed, minPairs, maxRemovedTerms) {
		term := display(t)
		it := Item{Kind: KindBannedTerm, Term: term, Key: "removed:" + term}
		for i, p := range pairs {
			if len(it.Evidence) < maxEvidence && removedIn[i][t] {
				it.Evidence = append(it.Evidence, Evidence{Unit: p.unit, Text: excerpt("", p.Before, term)})
			}
		}
		out = append(out, it)
	}
	return out
}

// lengthChange 归纳修改前后的长度变化：修改对不少于 minPairs 个，且修改后与修改前的总字数比超出 [shorterRatio, longerRatio]。
func lengthChange(pairs []pair) []Item {
	if len(pairs) < minPairs {
		return nil
	}
	before, after := 0, 0
	for _, p := range pairs {
		before, after = before+Chars(p.Before), after+Chars(p.After)
	}
	if before == 0 {
		return nil
	}
	ratio := float64(after) / float64(before)
	var it Item
	switch {
	case ratio <= shorterRatio:
		it = Item{Kind: KindStyle, Key: "length:shorter", Text: fmt.Sprintf("作者修改后的段落比 Agent 初稿短约 %d%%：写得更精简", int(math.Round((1-ratio)*100)))}
	case ratio >= longerRatio:
		it = Item{Kind: KindStyle, Key: "length:longer", Text: fmt.Sprintf("作者修改后的段落比 Agent 初稿长约 %d%%：展开得更充分", int(math.Round((ratio-1)*100)))}
	default:
		return nil
	}
	sorted := slices.Clone(pairs)
	slices.SortStableFunc(sorted, func(a, b pair) int {
		return absInt(Chars(b.After)-Chars(b.Before)) - absInt(Chars(a.After)-Chars(a.Before))
	})
	for _, p := range sorted[:min(maxEvidence, len(sorted))] {
		it.Evidence = append(it.Evidence, Evidence{Unit: p.unit, Text: excerpt(fmt.Sprintf("%d 字 → %d 字：", Chars(p.Before), Chars(p.After)), p.After, "")})
	}
	return []Item{it}
}

func absInt(x int) int {
	if x < 0 {
		return -x
	}
	return x
}

// feedbackTerms 归纳反馈中反复出现的词：出现在不少于 minFeedback 条反馈的文字中。
func feedbackTerms(signals []signal) []Item {
	counts := map[string]int{}
	mentions := make([]map[string]int, len(signals))
	for i, s := range signals {
		mentions[i] = map[string]int{}
		countTerms(s.text, mentions[i])
		for t := range mentions[i] {
			counts[t]++
		}
	}
	for t, c := range counts {
		if c < minFeedback {
			delete(counts, t)
		}
	}
	dedupe(counts)
	var out []Item
	for _, t := range top(counts, minFeedback, maxFeedbackTerms) {
		term := display(t)
		it := Item{Kind: KindStyle, Key: "feedback:" + term, Text: fmt.Sprintf("作者在 %d 条反馈中提到「%s」", counts[t], term)}
		for i, s := range signals {
			if len(it.Evidence) < maxEvidence && mentions[i][t] > 0 {
				it.Evidence = append(it.Evidence, Evidence{Unit: s.unit, Text: excerpt(s.id+"：", s.text, term)})
			}
		}
		out = append(out, it)
	}
	return out
}

// longParagraphs 归纳集中在长段落的反馈：锚点已定位的反馈不少于 minFeedback 条，且不少于三分之二位于长于所在正文平均段长 1.5 倍的段落。
func longParagraphs(signals []signal) []Item {
	var located, long []signal
	for _, s := range signals {
		if s.paraChars > 0 {
			located = append(located, s)
			if float64(s.paraChars) > longParagraph*s.meanChars {
				long = append(long, s)
			}
		}
	}
	if len(located) < minFeedback || 3*len(long) < 2*len(located) {
		return nil
	}
	it := Item{Kind: KindStyle, Key: "feedback:long-paragraphs",
		Text: fmt.Sprintf("作者的反馈集中出现在长段落（%d 条已定位的反馈中有 %d 条位于超过平均段长 1.5 倍的段落）：段落宜短", len(located), len(long))}
	for _, s := range long[:min(maxEvidence, len(long))] {
		it.Evidence = append(it.Evidence, Evidence{Unit: s.unit, Text: excerpt(fmt.Sprintf("%s（所在段落 %d 字）：", s.id, s.paraChars), s.text, "")})
	}
	return []Item{it}
}
