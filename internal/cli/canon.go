package cli

import (
	"fmt"
	"io"
	"strings"

	"github.com/kw12121212/writing-anything/internal/canon"
	"github.com/kw12121212/writing-anything/internal/catalog"
	"github.com/kw12121212/writing-anything/internal/impact"
	"github.com/kw12121212/writing-anything/internal/schema"
	"github.com/kw12121212/writing-anything/internal/work"
	"github.com/kw12121212/writing-anything/internal/workspace"
)

var workArg = argSpec{"work", "作品（Work）引用；设定集（Canon）是作品级产物，使用作品 id", true}

var canonAddCommand = &command{
	name:    "canon add",
	summary: "在作品（Work）的设定集（Canon）中登记一个设定条目（Canon Entry）：人物、地点、物品、世界规则或人物关系",
	args:    []argSpec{workArg},
	flags: []flagSpec{
		dirFlag,
		{"type", flagString, "条目类型：" + strings.Join(canon.Types, "、")},
		{"name", flagString, "名称；character、location 与 item 必填，其他类型不得给出"},
		{"aliases", flagString, "别名（Alias），用逗号分隔；同一设定集内名称与别名不得重复"},
		{"text", flagString, "设定内容"},
		{"sources", flagString, "关联的素材（Source）id，用逗号分隔；省略时表示尚未关联素材"},
		{"members", flagString, "人物关系（relationship）引用的人物条目 id，用逗号分隔，至少 2 个"},
		{"pin", flagBool, "登记为作者钦定（Author-pinned）条目，受最高级别保护"},
		{"id", flagString, "条目 id；省略时为 canon-<n>"},
		{"scope", flagString, "条目作用域：work（缺省，写入作品设定集）或 series（写入所属系列的共享设定集）"},
		{"override-reason", flagString, "作品级条目覆盖系列级同 id 条目的原因（覆盖时必填）"},
		{"confirmed-by-author", flagBool, "作者确认本条作品级条目覆盖系列级同 id 条目"},
		{"confirmation", flagString, "作者确认覆盖的原话摘要（覆盖时必填）"},
	},
	outputKeys: []outputKey{{"canon", "新条目：work、id、type、name、aliases、text、sources、protection、members、decision、scope 与 override；失败时为 null"}},
	exitCodes:  standardExitCodes,
	run:        runCanonAdd,
}

var canonSetCommand = &command{
	name:    "canon set",
	summary: "修改设定集（Canon）中一个设定条目（Canon Entry），保留其他内容与注释；修改作者钦定条目需要 --decision",
	args:    []argSpec{workArg, {"entry", "设定条目 id", true}},
	flags: []flagSpec{
		dirFlag,
		{"type", flagString, "条目类型：" + strings.Join(canon.Types, "、") + "；省略时不修改"},
		{"name", flagString, "名称；给出空值时删除该字段"},
		{"aliases", flagString, "别名（Alias），用逗号分隔，整体替换；给出空值时删除该字段；同一设定集内名称与别名不得重复"},
		{"text", flagString, "设定内容；省略时不修改"},
		{"sources", flagString, "关联的素材（Source）id，用逗号分隔，整体替换原有关联；给出空值时清空"},
		{"members", flagString, "人物关系引用的人物条目 id，用逗号分隔，整体替换；给出空值时删除该字段"},
		{"pin", flagBool, "把 protection 升为 author-pinned；只加强保护，不需要决策"},
		{"unpin", flagBool, "把 protection 降为 normal；属于修改钦定条目，需要 --decision"},
		{"decision", flagString, "作者钦定条目的授权：指向 wa-cli decide 已决定且选择为允许的决策 id"},
	},
	outputKeys: []outputKey{{"canon", "修改后的条目：work、id、type、name、aliases、text、sources、protection、members 与 decision；失败时为 null"}, impactOutputKey},
	exitCodes:  standardExitCodes,
	run:        runCanonSet,
}

var canonListCommand = &command{
	name:       "canon list",
	summary:    "列出作品（Work）设定集（Canon）中的设定条目（Canon Entry）",
	args:       []argSpec{workArg},
	flags:      []flagSpec{dirFlag},
	outputKeys: []outputKey{{"entries", "按登记顺序排列的条目数组，每项含 work、id、type、name、aliases、text、sources、protection、members 与 decision"}},
	exitCodes:  standardExitCodes,
	run:        runCanonList,
}

var canonRemoveCommand = &command{
	name:       "canon remove",
	summary:    "删除设定集（Canon）中一个设定条目（Canon Entry），保留其他内容与注释；删除作者钦定条目需要 --decision",
	args:       []argSpec{workArg, {"entry", "设定条目 id", true}},
	flags:      []flagSpec{dirFlag, {"decision", flagString, "作者钦定条目的授权：指向 wa-cli decide 已决定且选择为允许的决策 id"}},
	outputKeys: []outputKey{{"removed", "被删除的条目 id；失败时为 null"}},
	exitCodes:  standardExitCodes,
	run:        runCanonRemove,
}

type canonEntryView struct {
	Work string `json:"work"`
	canon.Entry
	Scope string `json:"scope,omitempty"`
}

type canonAddResult struct {
	Result
	Canon *canonEntryView `json:"canon"`
}

type canonSetResult struct {
	Result
	Canon  *canonEntryView `json:"canon"`
	Impact []impact.Ripple `json:"impact"`
}

// canonSemantic 返回设定条目的语义比较键（type、name、aliases、text 与 members；sources、protection 与 decision
// 不是语义字段）。e 为 nil 时为空串。
func canonSemantic(e *canon.Entry) string {
	if e == nil {
		return ""
	}
	name := ""
	if e.Name != nil {
		name = *e.Name
	}
	return impact.SemanticKey(e.Type, name, impact.SortedKey(e.Aliases), e.Text, impact.SortedKey(e.Members))
}

type canonListResult struct {
	Result
	Entries []canonEntryView `json:"entries"`
}

type canonRemoveResult struct {
	Result
	Removed *string `json:"removed"`
}

func runCanonAdd(inv *invocation) int {
	dir := projectDir(inv)
	res := &canonAddResult{}
	problems := workspace.Require(dir)
	if problems == nil {
		var w *work.Work
		if w, problems = resolveWorkAt(dir, inv.args[0]); w != nil {
			aliases, ok := splitAliases(inv.values["aliases"])
			if !ok {
				problems = []workspace.Problem{{Code: schema.CodeSchemaViolation, Severity: workspace.SeverityError, Field: "aliases",
					Message: "别名（Alias）只含空白；请给出非空白的别名，用逗号分隔"}}
			} else {
				in := canon.AddInput{
					Type:            inv.values["type"],
					Name:            inv.values["name"],
					Aliases:         aliases,
					Text:            inv.values["text"],
					Sources:         splitSources(inv.values["sources"]),
					Members:         splitSources(inv.values["members"]),
					Pinned:          inv.flag("pin"),
					ID:              inv.values["id"],
					Series:          inv.values["scope"] == "series",
					OverrideReason:  inv.values["override-reason"],
					OverrideConfirm: inv.values["confirmation"],
				}
				if inv.values["scope"] != "" && inv.values["scope"] != "series" && inv.values["scope"] != "work" {
					problems = []workspace.Problem{{Code: "input.invalid-value", Severity: workspace.SeverityError, Field: "scope",
						Message: "--scope 只能取 work 或 series"}}
					res.Diagnostics = diagnostics(problems, dir)
					return finish(inv, res, nil)
				}
				if in.OverrideConfirm != "" && !inv.flag("confirmed-by-author") {
					problems = []workspace.Problem{{Code: canon.CodeOverrideUnconfirmed, Severity: workspace.SeverityError, Field: "confirmation",
						Message: "作者确认覆盖必须同时给出 --confirmed-by-author 与非空白的 --confirmation（作者原话摘要）"}}
				}
				var e *canon.Entry
				var changed []string
				if e, changed, problems = canon.Add(dir, w, in); e != nil {
					res.Canon, res.ChangedPaths = &canonEntryView{Work: w.ID, Entry: *e}, changed
				}
			}
		}
	}
	res.Diagnostics = diagnostics(problems, dir)
	canonParamNext(res.Diagnostics, "canon add", dir)
	return finish(inv, res, func(w io.Writer) error {
		if res.Canon == nil {
			return nil
		}
		_, err := fmt.Fprintf(w, "已在作品 %s 登记设定条目（Canon Entry）%s（%s%s）：%s\n",
			res.Canon.Work, res.Canon.ID, res.Canon.Type, canonNameText(res.Canon.Name), res.Canon.Text)
		return err
	})
}

func runCanonSet(inv *invocation) int {
	dir := projectDir(inv)
	res := &canonSetResult{Impact: []impact.Ripple{}}
	var auto []Diagnostic
	problems := workspace.Require(dir)
	if problems == nil {
		var w *work.Work
		if w, problems = resolveWorkAt(dir, inv.args[0]); w != nil {
			var in canon.SetInput
			if v, ok := inv.values["type"]; ok {
				in.Type = &v
			}
			if v, ok := inv.values["name"]; ok {
				in.Name = &v
			}
			if _, ok := inv.values["aliases"]; ok {
				a, ok := splitAliases(inv.values["aliases"])
				if !ok {
					problems = []workspace.Problem{{Code: schema.CodeSchemaViolation, Severity: workspace.SeverityError, Field: "aliases",
						Message: "别名（Alias）只含空白；请给出非空白的别名，用逗号分隔，或用空值删除全部别名"}}
				} else {
					in.Aliases = &a
				}
			}
			if v, ok := inv.values["text"]; ok {
				in.Text = &v
			}
			if _, ok := inv.values["sources"]; ok {
				s := splitSources(inv.values["sources"])
				in.Sources = &s
			}
			if _, ok := inv.values["members"]; ok {
				m := splitSources(inv.values["members"])
				in.Members = &m
			}
			if inv.flag("pin") && inv.flag("unpin") {
				problems = []workspace.Problem{{Code: "input.invalid-value", Severity: workspace.SeverityError, Field: "pin",
					Message: "不能同时给出 --pin 与 --unpin"}}
			} else {
				if inv.flag("pin") {
					v := true
					in.Pin = &v
				}
				if inv.flag("unpin") {
					v := false
					in.Pin = &v
				}
			}
			in.Decision = inv.values["decision"]
			var e *canon.Entry
			var changed []string
			if problems == nil {
				var before *canon.Entry
				if entries, _ := canon.List(dir, w); entries != nil {
					for i := range entries {
						if entries[i].ID == inv.args[1] {
							before = &entries[i]
						}
					}
				}
				pre := preAffected(dir, w, inv.args[1])
				if e, changed, problems = canon.Set(dir, w, inv.args[1], in); e != nil {
					res.Canon, res.ChangedPaths = &canonEntryView{Work: w.ID, Entry: *e}, changed
					if len(changed) > 0 && canonSemantic(before) != canonSemantic(e) {
						var more []string
						res.Impact, more, auto = autoImpact(dir, w, []impactTarget{{target: e.ID, pre: pre}})
						res.ChangedPaths = append(res.ChangedPaths, more...)
					}
				} else if changed != nil {
					// 拒绝修改钦定条目时 changed 为追加的决策队列路径，canon 为 null。
					res.ChangedPaths = changed
				}
			}
		}
	}
	res.Diagnostics = diagnostics(problems, dir)
	canonParamNext(res.Diagnostics, "canon set", dir)
	canonPinnedNext(res.Diagnostics, dir)
	canonNotFoundNext(res.Diagnostics, inv.args[0], dir)
	res.Diagnostics = append(res.Diagnostics, auto...)
	return finish(inv, res, func(w io.Writer) error {
		if res.Canon == nil {
			return nil
		}
		verb := "已修改"
		if len(res.ChangedPaths) == 0 {
			verb = "取值未变，未修改"
		}
		_, err := fmt.Fprintf(w, "%s作品 %s 的设定条目（Canon Entry）%s（%s%s）：%s\n%s",
			verb, res.Canon.Work, res.Canon.ID, res.Canon.Type, canonNameText(res.Canon.Name), res.Canon.Text, impactText(res.Impact))
		return err
	})
}

func runCanonList(inv *invocation) int {
	dir := projectDir(inv)
	res := &canonListResult{Entries: []canonEntryView{}}
	problems := workspace.Require(dir)
	if problems == nil {
		var w *work.Work
		if w, problems = resolveWorkAt(dir, inv.args[0]); w != nil {
			// 默认输出按作用域解析的视图（作品级优先 + 系列级并入，标明来源）；--all-scope 输出两级原始条目。
			scoped, sps := canon.Resolve(dir, w)
			if sps != nil {
				problems = sps
			} else {
				for _, se := range scoped {
					v := canonEntryView{Work: w.ID, Entry: se.Entry}
					v.Scope = se.Scope
					res.Entries = append(res.Entries, v)
				}
			}
		}
	}
	res.Diagnostics = diagnostics(problems, dir)
	return finish(inv, res, func(w io.Writer) error {
		var b strings.Builder
		if len(res.Entries) == 0 && !hasError(res.Diagnostics) {
			fmt.Fprintf(&b, "作品 %s 还没有设定条目（Canon Entry）\n", inv.args[0])
		}
		for _, e := range res.Entries {
			scope := ""
			if e.Scope != "" && e.Scope != "work" {
				scope = "［" + e.Scope + "］"
			}
			fmt.Fprintf(&b, "%s%s（%s%s%s）%s：%s\n", e.ID, scope, e.Type, canonNameText(e.Name), canonAliasesText(e.Aliases), protectionText(e.Protection), e.Text)
		}
		_, err := io.WriteString(w, b.String())
		return err
	})
}

func runCanonRemove(inv *invocation) int {
	dir := projectDir(inv)
	res := &canonRemoveResult{}
	problems := workspace.Require(dir)
	if problems == nil {
		var w *work.Work
		if w, problems = resolveWorkAt(dir, inv.args[0]); w != nil {
			var changed []string
			var ok bool
			if ok, changed, problems = canon.Remove(dir, w, inv.args[1], inv.values["decision"]); ok {
				id := inv.args[1]
				res.Removed, res.ChangedPaths = &id, changed
			} else if changed != nil {
				res.ChangedPaths = changed
			}
		}
	}
	res.Diagnostics = diagnostics(problems, dir)
	canonPinnedNext(res.Diagnostics, dir)
	canonNotFoundNext(res.Diagnostics, inv.args[0], dir)
	helpNext(res.Diagnostics, "canon remove", []string{canon.CodeAnchoredValue})
	return finish(inv, res, func(w io.Writer) error {
		if res.Removed == nil {
			return nil
		}
		_, err := fmt.Fprintf(w, "已删除作品 %s 的设定条目（Canon Entry）%s\n", inv.args[0], *res.Removed)
		return err
	})
}

func canonNameText(name *string) string {
	if name == nil {
		return ""
	}
	return "，" + *name
}

// canonAliasesText 返回人读文本中的别名列；没有别名时为空。
func canonAliasesText(aliases []string) string {
	if len(aliases) == 0 {
		return ""
	}
	return "（别名：" + strings.Join(aliases, "、") + "）"
}

// splitAliases 把逗号分隔的别名（Alias）切开并去掉首尾空白；空串表示没有别名。与 splitSources 不同，
// 只含空白的元素不是被丢弃而是报错：别名元素只含空白时按 Schema 违规报告（field 为 aliases）。
func splitAliases(v string) ([]string, bool) {
	out := []string{}
	if v == "" {
		return out, true
	}
	for _, s := range strings.Split(v, ",") {
		s = strings.TrimSpace(s)
		if s == "" {
			return nil, false
		}
		out = append(out, s)
	}
	return out, true
}

func protectionText(protection string) string {
	if protection == canon.ProtectionPinned {
		return "［作者钦定］"
	}
	return ""
}

// resolveWorkAt 按作品引用加载 dir 下的作品；设定集是作品级产物，子单元引用不可用。
func resolveWorkAt(dir, ref string) (*work.Work, []workspace.Problem) {
	w, u, ps := work.Resolve(dir, ref)
	if u != nil && u.Parent != nil {
		return nil, []workspace.Problem{{Code: work.CodeNotFound, Severity: workspace.SeverityError, Path: w.Path,
			Message: "设定集（Canon）是作品级产物，请使用作品 id " + w.ID + "，不要带子单元"}}
	}
	return w, ps
}

// canonParamNext 把参数引起的问题（参数尚未写入，整体校验检不出）的下一步改为查看 name 的用法与素材列表；
// 已有 canon.yaml 中的问题（字段以 entries 开头）保留默认的复查命令。
func canonParamNext(ds []Diagnostic, name, dir string) {
	var own []Diagnostic
	var idx []int
	for i, d := range ds {
		if d.Code == canon.CodeAnchoredValue || d.Code == "input.invalid-value" || paramField(d.Field, "id", "type", "name", "aliases", "text", "sources", "members") {
			own, idx = append(own, d), append(idx, i)
		}
	}
	helpNext(own, name, []string{schema.CodeSchemaViolation, canon.CodeUnknownSource, catalog.CodeDuplicateID, canon.CodeDuplicateAlias, canon.CodeAnchoredValue, "input.invalid-value"},
		shellCommand("wa-cli", "source", "list", "--dir", dir, "--json"))
	for k, i := range idx {
		ds[i] = own[k]
	}
}

// canonPinnedNext 把 canon.author-pinned 的下一步改为查看决策队列与 canon set 的用法。
func canonPinnedNext(ds []Diagnostic, dir string) {
	for i := range ds {
		if ds[i].Code == canon.CodeAuthorPinned {
			ds[i].Next = []string{
				shellCommand("wa-cli", "decisions", "--dir", dir, "--json"),
				"wa-cli help canon set --json",
			}
		}
	}
}

// canonNotFoundNext 把 canon.not-found 的下一步改为列出该作品已有的设定条目。
func canonNotFoundNext(ds []Diagnostic, ref, dir string) {
	for i := range ds {
		if ds[i].Code == canon.CodeNotFound {
			ds[i].Next = []string{shellCommand("wa-cli", "canon", "list", ref, "--dir", dir, "--json")}
		}
	}
}
