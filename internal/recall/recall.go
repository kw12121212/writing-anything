// Package recall 提供已验收正文的全文检索（Full-text Search）：wa-cli recall 以内置倒排索引（Inverted Index）
// 在 stage 为 done 的单元正文（draft.md）中查找关键词或设定条目（Canon Entry）的称谓，返回出现位置与片段。
// 切词为确定性的：中文按字符二元组（bigram），非中文连续字母数字段按词；命中以对当前正文的子串复核为准
// （ASCII 字母不区分大小写），不使用向量检索（Vector Search），不调用任何模型。
//
// 索引持久化为工作区级缓存 .writing-anything/.recall-index.yaml（点前缀，不属于任何产物类型）：每次检索先按
// 单元 draft.md 与作品 canon.yaml 的 SHA-256 增量刷新——只重建内容发生变化的部分、移除已删除单元与作品的条目；
// 索引缺失、读取失败或结构不符时全量重建。索引是纯派生缓存，可随时删除。
package recall

import (
	"crypto/sha256"
	"encoding/hex"
	"fmt"
	"slices"
	"sort"
	"strings"
	"unicode"
	"unicode/utf8"

	"github.com/kw12121212/writing-anything/internal/canon"
	"github.com/kw12121212/writing-anything/internal/catalog"
	"github.com/kw12121212/writing-anything/internal/draft"
	"github.com/kw12121212/writing-anything/internal/work"
	"github.com/kw12121212/writing-anything/internal/workspace"
	"go.yaml.in/yaml/v3"
)

// IndexPath 是倒排索引缓存的工作区相对路径；点前缀使其不属于任何产物类型。
const IndexPath = workspace.Dir + "/.recall-index.yaml"

// indexSchemaVersion 是索引缓存的结构版本；不符时全量重建。
const indexSchemaVersion = 1

// snippetWidth 是片段的最大字符数（Unicode 码点），以段落内首次命中位置为中心。
const snippetWidth = 120

// Input 是 wa-cli recall 的参数。
type Input struct {
	Query string // 检索词：关键词，或设定条目的 id、name、别名（Alias）
	Work  string // 可选；限定检索的作品 id
}

// Hit 是一次出现。
type Hit struct {
	Work      string  `json:"work"`
	Unit      string  `json:"unit"`
	Paragraph int     `json:"paragraph"`
	Snippet   string  `json:"snippet"`
	Matched   string  `json:"matched"`
	Entry     *string `json:"entry"`
}

// Output 是检索结果；数组字段不为 nil（输出信封中不得为 null）。
type Output struct {
	Results []Hit    `json:"results"`
	Indexed []string `json:"indexed"`
}

// unitIndex 是一个已索引单元的倒排条目：正文哈希与该单元正文的全部词条（去重、排序）。
type unitIndex struct {
	Ref      string   `yaml:"ref" json:"ref"`
	DraftSHA string   `yaml:"draft_sha256" json:"draft_sha256"`
	Terms    []string `yaml:"terms,flow" json:"terms"`
}

// workIndex 是一个作品的索引条目：canon.yaml 的内容哈希（不存在时为空字符串）与其已索引单元。
type workIndex struct {
	Work     string      `yaml:"work" json:"work"`
	CanonSHA string      `yaml:"canon_sha256" json:"canon_sha256"`
	Units    []*unitIndex `yaml:"units" json:"units"`
}

// indexFile 是索引缓存文件的结构；Works 按作品 id 排序，Units 按单元树先序。
type indexFile struct {
	SchemaVersion int          `yaml:"schema_version" json:"schema_version"`
	Works         []*workIndex `yaml:"works" json:"works"`
}

func errorAt(code, path, field, msg string) workspace.Problem {
	return workspace.Problem{Code: code, Severity: workspace.SeverityError, Path: path, Field: field, Message: msg}
}

// Run 在 root 下检索 in.Query：增量刷新索引缓存后查询，返回结果、变更的路径（只在索引写盘时非空）与问题。
// 作品的 canon.yaml 不通过校验时报告其诊断（error 级），该作品跳过别名解析、仍按原词检索其正文；无法加载的
// 作品以 warning 报告并跳过（wa-cli validate 仍按 error 报告它们）。查询词为空或只含空白时不检索。
func Run(root string, in Input) (*Output, []string, []workspace.Problem) {
	if strings.TrimSpace(in.Query) == "" {
		return nil, nil, []workspace.Problem{{Code: "input.invalid-value", Severity: workspace.SeverityError, Field: "query",
			Message: "查询词为空或只含空白；请给出关键词或设定条目的 id、名称、别名"}}
	}
	var problems []workspace.Problem
	works, ps := scope(root, in.Work)
	problems = append(problems, ps...)
	if len(works) == 0 && hasError(problems) {
		return nil, nil, problems
	}
	sort.Slice(works, func(i, j int) bool { return works[i].ID < works[j].ID })

	idx, indexed, wrote, warns := refresh(root, works)
	if indexed == nil {
		indexed = []string{}
	}
	problems = append(problems, warns...)
	changed := []string{}
	if wrote {
		changed = []string{IndexPath}
	}
	terms := termSets(idx)

	// 逐作品解析查询词：恰好等于一个条目的 id、name 或别名（Alias）时扩展为该条目全部称谓并标注条目 id；
	// 没有命中、或同一个 name 命中多个条目（场景级上下文包允许同名条目）时按原词检索。
	type plan struct {
		w       *work.Work
		needles []string
		entry   *string
	}
	var plans []plan
	for _, w := range works {
		p := plan{w: w, needles: []string{in.Query}}
		entries, cps := canon.List(root, w)
		for _, cp := range cps {
			if cp.Severity == workspace.SeverityError {
				problems = append(problems, cp)
			}
		}
		var matched *canon.Entry
		hits := 0
		for i := range entries {
			e := &entries[i]
			if e.ID == in.Query || slices.Contains(e.Names(), in.Query) {
				hits++
				matched = e
			}
		}
		if hits == 1 && len(matched.Names()) > 0 {
			p.needles = matched.Names()
			id := matched.ID
			p.entry = &id
		}
		plans = append(plans, p)
	}

	out := &Output{Results: []Hit{}, Indexed: indexed}
	for _, p := range plans {
		search(root, p.w, p.needles, p.entry, terms, &out.Results)
	}
	return out, changed, problems
}

// scope 返回检索范围内的作品：给出 workID 时只加载该作品（子单元引用不可用），否则加载全部可加载作品；
// 无法加载的作品的问题降为 warning（与当前作品无关的坏文件不阻塞检索，subject-scoped-lookup）。
func scope(root, workID string) ([]*work.Work, []workspace.Problem) {
	if workID != "" {
		w, u, ps := work.Resolve(root, workID)
		if u != nil && u.Parent != nil {
			return nil, []workspace.Problem{errorAt(work.CodeNotFound, "", "",
				"作品 id " + w.ID + " 不带子单元；--work 只接受作品根单元 id，不要带 /" + u.ID)}
		}
		if len(ps) > 0 {
			return nil, ps
		}
		return []*work.Work{w}, nil
	}
	works, _, ps := work.LoadAll(root)
	for i := range ps {
		ps[i].Severity = workspace.SeverityWarning
	}
	return works, ps
}

func hasError(ps []workspace.Problem) bool {
	for _, p := range ps {
		if p.Severity == workspace.SeverityError {
			return true
		}
	}
	return false
}

// canonSHA 返回作品 canon.yaml 内容的 SHA-256 十六进制；不存在（含途经符号链接或读取失败）时为空字符串，
// 读取失败只影响新鲜度判断（触发重建），不影响检索结果：别名在查询时直接从设定集解析。
func canonSHA(root string, w *work.Work) string {
	data, ok, err := workspace.ReadRegular(root, canon.Path(w.ID))
	if err != nil || !ok {
		return ""
	}
	sum := sha256.Sum256(data)
	return hex.EncodeToString(sum[:])
}

// refresh 增量刷新索引缓存并返回内存索引、本次重建的单元引用、是否写盘与 warning 级问题：对检索范围内每个
// stage 为 done 的单元按其 draft.md 的 SHA-256、每个作品按其 canon.yaml 的 SHA-256 判断新鲜度，只重建变化部分、
// 移除已删除单元与作品；缓存缺失、读取失败或结构不符时全量重建。内容与磁盘一致时不写盘；磁盘上没有缓存、也
// 没有可索引内容时不写空文件。段落超过 paragraph-model 上限的单元以 draft.too-large（warning）跳过；缓存写盘
// 失败以 workspace.write-failed（warning）报告，检索结果不受影响。
func refresh(root string, works []*work.Work) (idx *indexFile, indexed []string, wrote bool, warns []workspace.Problem) {
	idx = loadIndex(root)
	keep := map[string]*workIndex{}
	for _, wi := range idx.Works {
		keep[wi.Work] = wi
	}
	next := &indexFile{SchemaVersion: indexSchemaVersion, Works: []*workIndex{}}
	for _, w := range works {
		csha := canonSHA(root, w)
		wi := &workIndex{Work: w.ID, CanonSHA: csha, Units: []*unitIndex{}}
		stored := keep[w.ID]
		stale := stored == nil || stored.CanonSHA != csha
		old := map[string]*unitIndex{}
		if stored != nil {
			for _, ui := range stored.Units {
				old[ui.Ref] = ui
			}
		}
		for _, u := range w.Units() {
			if w.EffectiveStage(u) != catalog.StageDone {
				continue
			}
			rel := w.UnitDir(u) + "/" + draft.FileName
			data, ok, err := workspace.ReadRegular(root, rel)
			if err != nil || !ok {
				continue // 正文缺失（含途经符号链接）：没有可索引的内容；已有条目随重建移除
			}
			if n := len(draft.Split(data)); n > draft.MaxParagraphs {
				warns = append(warns, workspace.Problem{Code: draft.CodeTooLarge, Severity: workspace.SeverityWarning, Path: rel,
					Message: fmt.Sprintf("正文有 %d 个段落，超过上限 %d，已跳过该单元的索引与检索", n, draft.MaxParagraphs)})
				continue
			}
			sum := sha256.Sum256(data)
			sha := hex.EncodeToString(sum[:])
			if ui := old[u.Ref]; !stale && ui != nil && ui.DraftSHA == sha {
				wi.Units = append(wi.Units, ui)
				continue
			}
			wi.Units = append(wi.Units, &unitIndex{Ref: u.Ref, DraftSHA: sha, Terms: tokenizeUnit(string(data))})
			indexed = append(indexed, u.Ref)
		}
		next.Works = append(next.Works, wi)
		delete(keep, w.ID)
	}
	// 范围外的作品（--work 限定时）保留缓存中的既有条目不动，避免反复切换范围重建整份缓存。
	for _, wi := range keep {
		next.Works = append(next.Works, wi)
	}
	sort.Slice(next.Works, func(i, j int) bool { return next.Works[i].Work < next.Works[j].Work })
	data, err := yaml.Marshal(next)
	if err != nil {
		return next, indexed, false, warns
	}
	cur, existed, _ := workspace.ReadRegular(root, IndexPath)
	if existed && string(cur) == string(data) {
		return next, indexed, false, warns
	}
	total := 0
	for _, wi := range next.Works {
		total += len(wi.Units)
	}
	if !existed && total == 0 {
		return next, indexed, false, warns
	}
	if err := workspace.WriteFile(root, IndexPath, data); err != nil {
		warns = append(warns, errorAt(workspace.CodeWriteFailed, IndexPath, "", "写入检索索引缓存失败："+err.Error()))
		return next, indexed, false, warns
	}
	return next, indexed, true, warns
}

// loadIndex 读取索引缓存；缺失、无法解析或结构版本不符时返回空索引（随后全量重建）。
func loadIndex(root string) *indexFile {
	data, ok, err := workspace.ReadRegular(root, IndexPath)
	if err != nil || !ok {
		return &indexFile{}
	}
	var idx indexFile
	if yaml.Unmarshal(data, &idx) != nil || idx.SchemaVersion != indexSchemaVersion {
		return &indexFile{}
	}
	return &idx
}

// termSets 返回单元引用到词条集合的映射。
func termSets(idx *indexFile) map[string]map[string]bool {
	out := map[string]map[string]bool{}
	for _, wi := range idx.Works {
		for _, ui := range wi.Units {
			set := map[string]bool{}
			for _, t := range ui.Terms {
				set[t] = true
			}
			out[ui.Ref] = set
		}
	}
	return out
}

// search 在作品 w 的已索引单元中检索 needles（一个或多个称谓），命中按单元树先序与段落号追加到 out；
// entry 非-nil 时命中项标注该设定条目 id。
func search(root string, w *work.Work, needles []string, entry *string, terms map[string]map[string]bool, out *[]Hit) {
	for _, u := range w.Units() {
		if w.EffectiveStage(u) != catalog.StageDone {
			continue
		}
		set := terms[u.Ref]
		if set == nil {
			continue
		}
		candidates := false
		for _, n := range needles {
			if beyondTerms(n) {
				// 含非 CJK 字母数字的称谓：词条包含不是子串命中的超集（如查询 son 而正文有 json），
				// 该单元仍需读正文复核。
				candidates = true
				break
			}
			if hasAllTokens(set, n) {
				candidates = true
				break
			}
		}
		if !candidates {
			continue
		}
		data, ok, err := workspace.ReadRegular(root, w.UnitDir(u)+"/"+draft.FileName)
		if err != nil || !ok {
			continue
		}
		for i, p := range draft.Split(data) {
			hay := foldASCII(p.Text)
			best := -1
			matched := ""
			for _, n := range needles {
				if pos := strings.Index(hay, foldASCII(n)); pos >= 0 {
					if r := utf8.RuneCountInString(hay[:pos]); best < 0 || r < best {
						best, matched = r, n
					}
				}
			}
			if best < 0 {
				continue
			}
			*out = append(*out, Hit{Work: w.ID, Unit: u.Ref, Paragraph: i + 1, Snippet: snippet(p.Text, best), Matched: matched, Entry: entry})
		}
	}
}

// beyondTerms 报告 needle 是否含有非 CJK 的字母或数字：这样的称谓可能是正文里更长单词的一段
//（如查询 son 而正文有 json），词条倒排表判不出，必须读正文做子串复核。
func beyondTerms(needle string) bool {
	for _, r := range needle {
		if (unicode.IsLetter(r) || unicode.IsDigit(r)) && !isCJK(r) {
			return true
		}
	}
	return false
}

// hasAllTokens 报告 needle 的全部词条是否都在集合 set 中（无词条的 needle 恒为候选，由调用方线性复核）。
func hasAllTokens(set map[string]bool, needle string) bool {
	tokens := tokenizeUnit(needle)
	if len(tokens) == 0 {
		return true
	}
	for _, t := range tokens {
		if !set[t] {
			return false
		}
	}
	return true
}

// tokenizeUnit 把文本切为去重且排序的词条：中文（CJK）连续段按字符二元组（bigram），非 CJK 的连续字母数字段
// 整词（ASCII 字母小写）；无效 UTF-8 序列与标点、空白都是分隔符，不产生词条。
func tokenizeUnit(s string) []string {
	seen := map[string]bool{}
	var out []string
	add := func(t string) {
		if !seen[t] {
			seen[t] = true
			out = append(out, t)
		}
	}
	var run []rune
	var word []rune
	flushRun := func() {
		for i := 0; i+1 < len(run); i++ {
			add(string(run[i : i+2]))
		}
		run = run[:0]
	}
	flushWord := func() {
		if len(word) > 0 {
			add(string(word))
			word = word[:0]
		}
	}
	for _, r := range s {
		switch {
		case r == utf8.RuneError:
			flushRun()
			flushWord()
		case isCJK(r):
			flushWord()
			run = append(run, r)
		case unicode.IsLetter(r) || unicode.IsDigit(r):
			flushRun()
			word = append(word, foldRune(r))
		default:
			flushRun()
			flushWord()
		}
	}
	flushRun()
	flushWord()
	sort.Strings(out)
	return out
}

// isCJK 报告 r 是否属于按二元组切分的中文（CJK）字符集：汉字、平假名、片假名与谚文。
func isCJK(r rune) bool {
	return unicode.Is(unicode.Han, r) || unicode.Is(unicode.Hiragana, r) ||
		unicode.Is(unicode.Katakana, r) || unicode.Is(unicode.Hangul, r)
}

// foldRune 只把 ASCII 大写字母折叠为小写：命中判定对 ASCII 字母不区分大小写，其他文字保持原样。
func foldRune(r rune) rune {
	if r >= 'A' && r <= 'Z' {
		return r + ('a' - 'A')
	}
	return r
}

// foldASCII 返回 s 的副本并把 ASCII 大写字母折叠为小写；命中判定对 ASCII 字母不区分大小写，
// 其他文字保持原样。
func foldASCII(s string) string {
	var b strings.Builder
	b.Grow(len(s))
	for _, r := range s {
		b.WriteRune(foldRune(r))
	}
	return b.String()
}

// snippet 以 text 中码点下标 pos 的命中位置为中心截取最长 snippetWidth 个字符，截断时两端以 … 标示。
func snippet(text string, pos int) string {
	rs := []rune(text)
	n := len(rs)
	lo := max(0, pos-snippetWidth/2)
	hi := min(n, lo+snippetWidth)
	lo = max(0, hi-snippetWidth)
	var b strings.Builder
	if lo > 0 {
		b.WriteString("…")
	}
	b.WriteString(string(rs[lo:hi]))
	if hi < n {
		b.WriteString("…")
	}
	return b.String()
}
