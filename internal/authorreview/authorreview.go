// Package authorreview 计算待作者复核（Pending Author Review）清单并维护复核记录（Author Review Record）
// .writing-anything/author-reviews.yaml。strict 关卡（Gate）下，经命令行渠道（cli）写入的验收（Acceptance）、
// 钦定设定修改（授权修改或删除作者钦定条目的决策）与卷末检查点（Volume Checkpoint）确认照常生效，但须由作者在本地
// 阅读页（Reading View）复核后才能组稿。复核记录按 kind、ref 与被复核那一次确认的时间对应，确认重写后旧复核自然失效。
// 这是可审计的流程约束而非安全隔离：同机同账户的 Agent 能读到阅读页令牌。本包只返回结构化的问题，由命令层转换为诊断。
package authorreview

import (
	"fmt"
	"os"
	"path/filepath"
	"slices"
	"strings"
	"time"

	"github.com/kw12121212/writing-anything/internal/canon"
	"github.com/kw12121212/writing-anything/internal/catalog"
	"github.com/kw12121212/writing-anything/internal/decision"
	"github.com/kw12121212/writing-anything/internal/review"
	"github.com/kw12121212/writing-anything/internal/schema"
	"github.com/kw12121212/writing-anything/internal/volume"
	"github.com/kw12121212/writing-anything/internal/work"
	"github.com/kw12121212/writing-anything/internal/workspace"
	"go.yaml.in/yaml/v3"
)

// Path 是复核记录相对项目目录的路径。
const Path = workspace.Dir + "/author-reviews.yaml"

// 待复核确认的类型。
const (
	KindAcceptance       = "acceptance"
	KindCanonDecision    = "canon-decision"
	KindVolumeCheckpoint = "volume-checkpoint"
)

// 复核的错误码（Error Code）。已发布的错误码不得改名或复用。
const (
	CodeUnknownKey    = "author-review.unknown-key"
	CodeAnchoredValue = "author-review.anchored-value"
)

// codeInvalidValue 是参数取值不合法的通用错误码（cli-contract/invalid-value-code）。
const codeInvalidValue = "input.invalid-value"

// Item 是一项待作者复核的确认。Key 唯一标识这一次确认：<kind>:<ref>@<确认时间>。
type Item struct {
	Key          string `json:"key"`
	Kind         string `json:"kind"`
	Work         string `json:"work"`
	Ref          string `json:"ref"`
	Path         string `json:"path"`
	Confirmation string `json:"confirmation"`
	ConfirmedAt  string `json:"confirmedAt"`
}

// Record 是复核记录中的一条。
type Record struct {
	Kind        string `json:"kind"`
	Work        string `json:"work"`
	Ref         string `json:"ref"`
	ConfirmedAt string `json:"confirmed_at"`
	Channel     string `json:"channel"`
	ReviewedAt  string `json:"reviewed_at"`
}

type fileRecord struct {
	Reviews []Record `json:"reviews"`
}

// key 返回一次确认的键。
func key(kind, ref, confirmedAt string) string { return kind + ":" + ref + "@" + confirmedAt }

func errorAt(code, path, field, msg string) workspace.Problem {
	return workspace.Problem{Code: code, Severity: workspace.SeverityError, Path: path, Field: field, Message: msg}
}

// Strict 报告工作区配置的关卡强度是否为 strict；配置无法加载时按缺省（standard）处理。
func Strict(root string) bool {
	cfg, _ := workspace.Load(root)
	return cfg != nil && cfg.GateLevel == "strict"
}

// load 读取并校验复核记录；不存在时返回 nil、nil 与 nil；途经符号链接或不是普通文件时按 artifact.not-found 报告。
func load(root string) (*yaml.Node, *fileRecord, []workspace.Problem) {
	if _, ok, _ := workspace.ReadRegular(root, Path); !ok {
		if info, err := os.Lstat(filepath.Join(root, filepath.FromSlash(Path))); err == nil && !info.Mode().IsRegular() {
			return nil, nil, []workspace.Problem{errorAt(schema.CodeNotFound, Path, "", "author-reviews.yaml 途经符号链接或不是普通文件，按不存在处理")}
		}
		return nil, nil, nil
	}
	doc, ps := workspace.Read(root, Path)
	if doc == nil {
		return nil, nil, ps
	}
	if sps := schema.Named("author-reviews").ValidateNode(doc); len(sps) > 0 {
		return nil, nil, workspace.FromSchema(Path, sps)
	}
	var f fileRecord
	catalog.Decode(doc, &f)
	return doc, &f, nil
}

// Check 校验复核记录（wa-cli validate 整体校验）；不存在时 checked 为空。
func Check(root string) (checked []string, problems []workspace.Problem) {
	doc, _, ps := load(root)
	if doc != nil || len(ps) > 0 && ps[0].Code != schema.CodeNotFound {
		checked = []string{Path}
	}
	return checked, ps
}

// Pending 返回作品 w 中经 cli 渠道（含缺失渠道的旧记录）写入、还没有对应复核记录的三类确认，按验收（单元树先序）、
// 卷末检查点（单元树先序）与钦定设定修改（决策队列顺序）排列。是否要求复核（strict 关卡）由调用方判断。
// 不通过校验的验收、检查点与决策队列不计入（其问题由各自的关卡报告）；复核记录无法加载时全部确认都算未复核，
// 并返回其问题。
func Pending(root string, w *work.Work) ([]Item, []workspace.Problem) {
	_, f, ps := load(root)
	reviewed := map[string]bool{}
	if f != nil {
		for _, r := range f.Reviews {
			reviewed[key(r.Kind, r.Ref, r.ConfirmedAt)] = true
		}
	}
	items := []Item{}
	add := func(kind, ref, path, confirmation, confirmedAt string, channel *string) {
		k := key(kind, ref, confirmedAt)
		if workspace.ChannelOr(channel) != workspace.ChannelCLI || reviewed[k] {
			return
		}
		items = append(items, Item{Key: k, Kind: kind, Work: w.ID, Ref: ref, Path: path, Confirmation: confirmation, ConfirmedAt: confirmedAt})
	}
	for _, u := range w.Units() {
		dir := w.UnitDir(u)
		if a, aps := review.LoadAcceptance(root, dir); a != nil && len(aps) == 0 && a.ConfirmedByAuthor && a.Confirmation != nil {
			add(KindAcceptance, u.Ref, review.AcceptancePath(dir), *a.Confirmation, a.AcceptedAt, a.Channel)
		}
	}
	for _, u := range w.Units() {
		if !volume.IsVolume(w, u) {
			continue
		}
		if v, vps := volume.Load(root, w, u); v != nil && len(vps) == 0 && v.ConfirmedByAuthor && v.Confirmation != nil && v.ConfirmedAt != nil {
			add(KindVolumeCheckpoint, u.Ref, v.Path, *v.Confirmation, *v.ConfirmedAt, v.Channel)
		}
	}
	ds, _, dps := decision.Load(root)
	if len(dps) == 0 {
		for _, d := range ds {
			if d.Status != decision.StatusDecided || d.Choice == nil || *d.Choice != canon.ChoiceAllow || !canon.PinnedChange(d) ||
				!slices.Contains(d.Units, w.ID) || d.Confirmation == nil || d.DecidedAt == nil {
				continue
			}
			add(KindCanonDecision, d.ID, decision.Path, *d.Confirmation, *d.DecidedAt, d.Channel)
		}
	}
	return items, ps
}

// Review 为作品 w 的待复核确认 keys 追加复核记录（channel 为 preview，reviewed_at 为 now）：keys 不能为空、不能重复，
// 每一项都必须是 Pending 当前给出的键，否则报告 author-review.unknown-key（field 为 keys[i]），不写文件。
// 返回被复核的确认与变更的路径。
func Review(root string, w *work.Work, keys []string, now time.Time) ([]Item, []string, []workspace.Problem) {
	if len(keys) == 0 {
		return nil, nil, []workspace.Problem{errorAt(codeInvalidValue, "", "keys", "keys 不能为空：请给出要复核的待作者复核项的 key")}
	}
	pending, ps := Pending(root, w)
	if len(ps) > 0 {
		return nil, nil, ps
	}
	var picked []Item
	for i, k := range keys {
		at := slices.IndexFunc(pending, func(it Item) bool { return it.Key == k })
		switch {
		case slices.Contains(keys[:i], k):
			return nil, nil, []workspace.Problem{errorAt(codeInvalidValue, "", fmt.Sprintf("keys[%d]", i), "key "+k+" 重复")}
		case at < 0:
			return nil, nil, []workspace.Problem{errorAt(CodeUnknownKey, "", fmt.Sprintf("keys[%d]", i),
				"作品 "+w.ID+" 没有待作者复核的确认 "+k+"：它可能已被复核、已重新确认或不需要复核；请刷新阅读页后重试")}
		}
		picked = append(picked, pending[at])
	}
	doc, _, ps := load(root)
	if len(ps) > 0 {
		return nil, nil, ps
	}
	if doc == nil {
		doc = &yaml.Node{Kind: yaml.DocumentNode, HeadComment: "复核记录（Author Review Record）；由作者在 wa-cli preview --serve 的阅读页批量复核时追加。", Content: []*yaml.Node{{Kind: yaml.MappingNode, Content: []*yaml.Node{
			workspace.Str("schema_version"), workspace.Int(1),
			workspace.Str("reviews"), {Kind: yaml.SequenceNode, Tag: "!!seq"}}}}}
	}
	m := doc.Content[0]
	seq, ok := workspace.Lookup(m, "reviews")
	if !ok || seq == nil || workspace.HasMerge(m) {
		return nil, nil, []workspace.Problem{errorAt(CodeAnchoredValue, Path, "reviews",
			"reviews 经由 YAML 锚点、别名或合并键得到，wa-cli 无法只追加记录；请先去掉锚点、别名与合并键")}
	}
	workspace.BlockSequence(m, "reviews")
	stamp := now.UTC().Format("2006-01-02T15:04:05Z")
	for _, it := range picked {
		seq.Content = append(seq.Content, &yaml.Node{Kind: yaml.MappingNode, Content: []*yaml.Node{
			workspace.Str("kind"), workspace.Str(it.Kind), workspace.Str("work"), workspace.Str(it.Work),
			workspace.Str("ref"), workspace.Str(it.Ref), workspace.Str("confirmed_at"), workspace.Str(it.ConfirmedAt),
			workspace.Str("channel"), workspace.Str(workspace.ChannelPreview), workspace.Str("reviewed_at"), workspace.Str(stamp)}})
	}
	data, err := workspace.Encode(doc)
	if err != nil {
		return nil, nil, []workspace.Problem{errorAt(workspace.CodeWriteFailed, Path, "", "生成复核记录失败："+err.Error())}
	}
	written, p := schema.Parse(data)
	if p != nil {
		return nil, nil, workspace.FromSchema(Path, []schema.Problem{*p})
	}
	if sps := schema.Named("author-reviews").ValidateNode(written); len(sps) > 0 {
		return nil, nil, workspace.FromSchema(Path, sps)
	}
	if err := workspace.WriteFile(root, Path, data); err != nil {
		return nil, nil, []workspace.Problem{errorAt(workspace.CodeWriteFailed, Path, "", "写入复核记录失败："+err.Error())}
	}
	return picked, []string{Path}, nil
}

// KindText 返回待复核确认类型的人读说法。
func KindText(kind string) string {
	switch kind {
	case KindAcceptance:
		return "验收（Acceptance）"
	case KindVolumeCheckpoint:
		return "卷末检查点（Volume Checkpoint）"
	}
	return "钦定设定修改"
}

// Describe 返回一项待复核确认的人读说明。
func Describe(it Item) string {
	return strings.Join([]string{KindText(it.Kind), it.Ref, "（cli 渠道，" + it.ConfirmedAt + "，作者：" + it.Confirmation + "）"}, " ")
}
