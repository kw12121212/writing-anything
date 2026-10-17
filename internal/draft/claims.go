package draft

import (
	"fmt"
	"reflect"
	"slices"
	"strings"

	"github.com/kw12121212/writing-anything/internal/catalog"
	"github.com/kw12121212/writing-anything/internal/schema"
	"github.com/kw12121212/writing-anything/internal/source"
	"github.com/kw12121212/writing-anything/internal/work"
	"github.com/kw12121212/writing-anything/internal/workspace"
	"go.yaml.in/yaml/v3"
)

// Claim 是单元中的一个论断（Claim）。
type Claim struct {
	ID       string   `json:"id"`
	Kind     string   `json:"kind"`
	Text     string   `json:"text"`
	Sources  []string `json:"sources"`
	Decision string   `json:"decision,omitempty"` // 授权以该类型登记的决策 id（写入校验的留痕例外通道）
}

type claimsFile struct {
	Claims []Claim `json:"claims"`
}

// checkClaims 检查已通过 Schema 校验的论断文档：id 在文件内唯一，引用的素材都已登记。
func checkClaims(root, rel string, f claimsFile) []workspace.Problem {
	var out []workspace.Problem
	seen := map[string]bool{}
	for i, c := range f.Claims {
		if seen[c.ID] {
			out = append(out, errorAt(catalog.CodeDuplicateID, rel, fmt.Sprintf("claims[%d].id", i), "论断 id "+c.ID+" 重复"))
		}
		seen[c.ID] = true
		for j, id := range c.Sources {
			if !source.Registered(root, id) {
				out = append(out, errorAt(CodeUnknownSource, rel, fmt.Sprintf("claims[%d].sources[%d]", i, j), unknownSource(id)))
			}
		}
	}
	return out
}

func unknownSource(id string) string {
	return "素材（Source）" + id + " 未登记；论断只能关联已用 wa-cli source add 登记的素材"
}

// loadClaims 读取并校验 rel 处的论断文档；文件不存在时返回 nil 与 nil。
func loadClaims(root, rel string) (*yaml.Node, *claimsFile, []workspace.Problem) {
	if _, ok, _ := readRegular(root, rel); !ok {
		return nil, nil, nil
	}
	doc, ps := workspace.Read(root, rel)
	if doc == nil {
		return nil, nil, ps
	}
	if ps := schema.Named("claims").ValidateNode(doc); len(ps) > 0 {
		return nil, nil, workspace.FromSchema(rel, ps)
	}
	var f claimsFile
	catalog.Decode(doc, &f)
	return doc, &f, nil
}

// LoadClaims 读取并校验单元 u 的 claims.yaml（Schema、论断 id 唯一、引用的素材已登记）；不存在时返回 nil 与 nil。
func LoadClaims(root string, w *work.Work, u *work.Unit) ([]Claim, []workspace.Problem) {
	rel := w.UnitDir(u) + "/" + ClaimsName
	_, f, ps := loadClaims(root, rel)
	if f == nil {
		return nil, ps
	}
	if ps := checkClaims(root, rel, *f); len(ps) > 0 {
		return nil, ps
	}
	return f.Claims, nil
}

// ListClaims 读取单元 u 的 claims.yaml 并返回全部论断：Schema 或 id 唯一性不通过时返回 nil 与其问题；引用未登记素材的
// 论断照常返回，其问题降为 warning（workspace-artifacts/cross-artifact-references）。不存在时返回 nil 与 nil。
func ListClaims(root string, w *work.Work, u *work.Unit) ([]Claim, []workspace.Problem) {
	rel := w.UnitDir(u) + "/" + ClaimsName
	_, f, ps := loadClaims(root, rel)
	if f == nil {
		return nil, ps
	}
	errs, warns := danglingAsWarnings(checkClaims(root, rel, *f))
	if len(errs) > 0 {
		return nil, errs
	}
	return f.Claims, warns
}

// danglingAsWarnings 把已有论断引用未登记素材的问题（claim.unknown-source，字段以 claims 开头）降为 warning，
// 其余问题原样作为 error 返回。修改或列出论断时，一个论断的悬空引用不应阻止处理其他论断。
func danglingAsWarnings(ps []workspace.Problem) (errs, warns []workspace.Problem) {
	for _, p := range ps {
		if p.Code == CodeUnknownSource && strings.HasPrefix(p.Field, "claims[") {
			p.Severity = workspace.SeverityWarning
			p.Message += "；请用 wa-cli claim set 把该论断改为关联已登记的素材"
			warns = append(warns, p)
			continue
		}
		errs = append(errs, p)
	}
	return errs, warns
}

// AddClaim 在单元 u 的 claims.yaml 末尾追加论断。id 为空时取 claim-<n>，n 从已有论断数加一开始递增到不重复；
// sources 中的素材必须已登记。claims.yaml 不存在时创建，已存在时只追加该项，保留其他内容与注释。
// 返回新论断与变更的路径（新建的单元目录与 claims.yaml）；出错时不修改文件。
func AddClaim(root string, w *work.Work, u *work.Unit, kind, text, id string, sources []string, decisionID string) (*Claim, []string, []workspace.Problem) {
	rel := w.UnitDir(u) + "/" + ClaimsName
	for i, s := range sources {
		if !source.Registered(root, s) {
			return nil, nil, []workspace.Problem{errorAt(CodeUnknownSource, rel, fmt.Sprintf("sources[%d]", i), unknownSource(s))}
		}
	}
	doc, f, ps := loadClaims(root, rel)
	if len(ps) > 0 {
		return nil, nil, ps
	}
	if doc == nil {
		f = &claimsFile{}
		doc = &yaml.Node{Kind: yaml.DocumentNode, HeadComment: "论断（Claim）；请用 wa-cli claim add 追加。", Content: []*yaml.Node{{Kind: yaml.MappingNode, Content: []*yaml.Node{
			str("schema_version"), {Kind: yaml.ScalarNode, Tag: "!!int", Value: "1"},
			str("claims"), {Kind: yaml.SequenceNode, Tag: "!!seq"},
		}}}}
	}
	seen := map[string]bool{}
	for _, c := range f.Claims {
		seen[c.ID] = true
	}
	if id == "" {
		for n := len(f.Claims) + 1; ; n++ {
			if id = fmt.Sprintf("claim-%d", n); !seen[id] {
				break
			}
		}
	} else if seen[id] {
		return nil, nil, []workspace.Problem{errorAt(catalog.CodeDuplicateID, rel, "id", "单元 "+u.Ref+" 中已有论断 "+id)}
	}
	claims := claimsNode(doc)
	if claims == nil {
		return nil, nil, []workspace.Problem{errorAt(CodeAnchoredValue, rel, "claims", "claims 经由 YAML 锚点、别名或合并键得到，wa-cli 无法只追加一项；请先去掉锚点、别名与合并键")}
	}
	item := &yaml.Node{Kind: yaml.MappingNode, Content: []*yaml.Node{str("id"), str(id), str("kind"), str(kind)}}
	if text != "" {
		item.Content = append(item.Content, str("text"), str(text))
	}
	srcs := &yaml.Node{Kind: yaml.SequenceNode, Tag: "!!seq", Style: yaml.FlowStyle}
	for _, s := range sources {
		srcs.Content = append(srcs.Content, str(s))
	}
	item.Content = append(item.Content, str("sources"), srcs)
	if decisionID != "" {
		item.Content = append(item.Content, str("decision"), str(decisionID))
	}
	index := len(claims.Content)
	workspace.BlockSequence(doc.Content[0], "claims")
	claims.Content = append(claims.Content, item)
	data, err := workspace.Encode(doc)
	if err != nil {
		return nil, nil, []workspace.Problem{errorAt(workspace.CodeWriteFailed, rel, "", "生成论断失败："+err.Error())}
	}
	// 写前复查：重新解析编码结果，Schema 与领域检查都通过才写出。新论断的问题以参数名作为字段。
	written, p := schema.Parse(data)
	if p != nil {
		p.Line = 0
		return nil, nil, workspace.FromSchema(rel, []schema.Problem{*p})
	}
	if ps := schema.Named("claims").ValidateNode(written); len(ps) > 0 {
		out := workspace.FromSchema(rel, ps)
		prefix := fmt.Sprintf("claims[%d].", index)
		for i := range out {
			out[i].Line = 0
			out[i].Field = strings.TrimPrefix(out[i].Field, prefix)
			if out[i].Field == fmt.Sprintf("claims[%d]", index) {
				out[i].Field = ""
			}
		}
		return nil, nil, out
	}
	var saved claimsFile
	catalog.Decode(written, &saved)
	if ps := checkClaims(root, rel, saved); len(ps) > 0 {
		return nil, nil, ps
	}
	changed, ps := ensureDir(root, w, u)
	if len(ps) > 0 {
		return nil, nil, ps
	}
	if err := workspace.WriteFile(root, rel, data); err != nil {
		return nil, nil, []workspace.Problem{errorAt(workspace.CodeWriteFailed, rel, "", "写入论断失败："+err.Error())}
	}
	c := saved.Claims[index]
	if c.Sources == nil {
		c.Sources = []string{}
	}
	return &c, append(changed, rel), nil
}

// claimsNode 返回文档中 claims 序列节点；途经锚点、别名或合并键时返回 nil（Schema 保证 claims 存在）。
func claimsNode(doc *yaml.Node) *yaml.Node {
	m := doc.Content[0]
	if !plain(m) {
		return nil
	}
	var v *yaml.Node
	for i := 0; i+1 < len(m.Content); i += 2 {
		if m.Content[i].ShortTag() == "!!merge" {
			return nil
		}
		if m.Content[i].Value == "claims" {
			v = m.Content[i+1]
		}
	}
	if v == nil || !plain(v) {
		return nil
	}
	return v
}

func plain(n *yaml.Node) bool { return n.Anchor == "" && n.Kind != yaml.AliasNode }

// CheckUnitArtifacts 校验每个作品的每个单元目录中存在的 provenance.yaml 与 claims.yaml：Schema，以及论断 id
// 唯一、引用的素材已登记。返回已校验的路径与全部问题。
func CheckUnitArtifacts(root string, works []*work.Work) (checked []string, problems []workspace.Problem) {
	for _, w := range works {
		for _, u := range w.Units() {
			dir := w.UnitDir(u)
			if _, ok, _ := readRegular(root, dir+"/"+RecordName); ok {
				checked = append(checked, dir+"/"+RecordName)
				_, ps := loadRecord(root, dir)
				problems = append(problems, ps...)
			}
			rel := dir + "/" + ClaimsName
			if _, ok, _ := readRegular(root, rel); ok {
				checked = append(checked, rel)
				problems = append(problems, CheckArtifact(root, rel)...)
			}
		}
	}
	return checked, problems
}

// CheckArtifact 对单元目录中的产物 rel 做领域检查：claims.yaml 检查论断 id 唯一与素材引用；provenance.yaml
// 只需 Schema 校验，没有领域检查。
func CheckArtifact(root, rel string) []workspace.Problem {
	if !strings.HasSuffix(rel, "/"+ClaimsName) {
		return nil
	}
	_, f, ps := loadClaims(root, rel)
	if f == nil {
		return ps
	}
	return checkClaims(root, rel, *f)
}

// CodeClaimNotFound 表示单元中没有给定 id 的论断。已发布的错误码不得改名或复用。
const CodeClaimNotFound = "claim.not-found"

// claimOrder 是论断中字段的固定顺序。
var claimOrder = []string{"id", "kind", "text", "sources", "decision"}

// editClaims 读取单元 u 的 claims.yaml，找到论断 id 并交给 edit 修改，写前复查后写出；修改后的取值与原有取值相同时
// 不写文件，changed 为空。edit 返回的问题原样返回。参数引起的问题（写前复查中新出现的）不填路径，字段为参数名。
// 修改后仍引用未登记素材的论断以 warning 报告，不阻止写入（workspace-artifacts/cross-artifact-references）；
// 成功时返回的问题只含这些 warning。
func editClaims(root string, w *work.Work, u *work.Unit, id string, edit func(claims *yaml.Node, at int, rel string) *workspace.Problem) (*claimsFile, int, []string, []workspace.Problem) {
	rel := w.UnitDir(u) + "/" + ClaimsName
	doc, _, ps := loadClaims(root, rel)
	if len(ps) > 0 {
		return nil, 0, nil, ps
	}
	notFound := []workspace.Problem{errorAt(CodeClaimNotFound, "", "", "单元 "+u.Ref+" 中没有论断（Claim）"+id)}
	if doc == nil {
		return nil, 0, nil, notFound
	}
	claims := claimsNode(doc)
	if claims == nil {
		return nil, 0, nil, []workspace.Problem{errorAt(CodeAnchoredValue, rel, "claims", "claims 经由 YAML 锚点、别名或合并键得到，wa-cli 无法只修改一项；请先去掉锚点、别名与合并键")}
	}
	at := workspace.IndexByID(claims, id)
	if at < 0 {
		return nil, 0, nil, notFound
	}
	before := schema.Decode(doc)
	if p := edit(claims, at, rel); p != nil {
		return nil, 0, nil, []workspace.Problem{*p}
	}
	data, err := workspace.Encode(doc)
	if err != nil {
		return nil, 0, nil, []workspace.Problem{errorAt(workspace.CodeWriteFailed, rel, "", "生成论断失败："+err.Error())}
	}
	written, p := schema.Parse(data)
	if p != nil {
		p.Line = 0
		return nil, 0, nil, workspace.FromSchema(rel, []schema.Problem{*p})
	}
	if ps := schema.Named("claims").ValidateNode(written); len(ps) > 0 {
		out := workspace.FromSchema(rel, ps)
		prefix := fmt.Sprintf("claims[%d].", at)
		for i := range out {
			out[i].Path, out[i].Line, out[i].Field = "", 0, strings.TrimPrefix(out[i].Field, prefix)
		}
		return nil, 0, nil, out
	}
	var saved claimsFile
	catalog.Decode(written, &saved)
	errs, warns := danglingAsWarnings(checkClaims(root, rel, saved))
	if len(errs) > 0 {
		return nil, 0, nil, errs
	}
	if reflect.DeepEqual(before, schema.Decode(written)) {
		return &saved, at, nil, warns
	}
	if err := workspace.WriteFile(root, rel, data); err != nil {
		return nil, 0, nil, []workspace.Problem{errorAt(workspace.CodeWriteFailed, rel, "", "写入论断失败："+err.Error())}
	}
	return &saved, at, []string{rel}, warns
}

// SetClaim 修改单元 u 中论断 id 的 kind、text 与 sources（nil 表示不修改；sources 整体替换），保留其他内容与注释。
// sources 中的素材必须已登记。返回修改后的论断与变更的路径（取值未变时为空）；出错时不修改文件。
func SetClaim(root string, w *work.Work, u *work.Unit, id string, kind, text *string, sources *[]string, decisionID string) (*Claim, []string, []workspace.Problem) {
	if sources != nil {
		for i, s := range *sources {
			if !source.Registered(root, s) {
				return nil, nil, []workspace.Problem{errorAt(CodeUnknownSource, "", fmt.Sprintf("sources[%d]", i), unknownSource(s))}
			}
		}
	}
	f, at, changed, ps := editClaims(root, w, u, id, func(claims *yaml.Node, at int, rel string) *workspace.Problem {
		item := claims.Content[at]
		field := fmt.Sprintf("claims[%d]", at)
		if !plain(item) || workspace.HasMerge(item) {
			return &workspace.Problem{Code: CodeAnchoredValue, Severity: workspace.SeverityError, Path: rel, Field: field,
				Message: "该论断经由 YAML 锚点、别名或合并键得到，wa-cli 无法只修改它；请先去掉锚点、别名与合并键"}
		}
		decisionNode := (*yaml.Node)(nil)
		if decisionID != "" {
			decisionNode = str(decisionID)
		}
		for _, kv := range []struct {
			key string
			n   *yaml.Node
		}{{"kind", strNode(kind)}, {"text", strNode(text)}, {"sources", seqNode(sources)}, {"decision", decisionNode}} {
			if kv.n == nil {
				continue
			}
			if f, ok := workspace.SetKey(item, claimOrder, kv.key, kv.n); !ok {
				return &workspace.Problem{Code: CodeAnchoredValue, Severity: workspace.SeverityError, Path: rel, Field: field + "." + f,
					Message: "该处经由 YAML 锚点、别名或合并键得到，wa-cli 无法只修改这一处；请先去掉锚点、别名与合并键"}
			}
		}
		return nil
	})
	if f == nil {
		return nil, nil, ps
	}
	c := f.Claims[at]
	if c.Sources == nil {
		c.Sources = []string{}
	}
	return &c, changed, ps
}

// RemoveClaim 删除单元 u 中的论断 id，保留其他内容与注释。返回是否已删除、变更的路径与问题（成功时只含其他论断悬空
// 引用的 warning）；出错时不修改文件。
func RemoveClaim(root string, w *work.Work, u *work.Unit, id string) (bool, []string, []workspace.Problem) {
	f, _, changed, ps := editClaims(root, w, u, id, func(claims *yaml.Node, at int, rel string) *workspace.Problem {
		if workspace.HasAnchor(claims.Content[at]) {
			return &workspace.Problem{Code: CodeAnchoredValue, Severity: workspace.SeverityError, Path: rel, Field: fmt.Sprintf("claims[%d]", at),
				Message: "该论断中有 YAML 锚点、别名或合并键，删除会改变其他取值；请先去掉锚点、别名与合并键"}
		}
		claims.Content = slices.Delete(claims.Content, at, at+1)
		return nil
	})
	if f == nil {
		return false, nil, ps
	}
	return true, changed, ps
}

func strNode(v *string) *yaml.Node {
	if v == nil {
		return nil
	}
	return str(*v)
}

// seqNode 返回 sources 的流式序列节点；空数组也写出为 []（sources 是必需字段）。
func seqNode(v *[]string) *yaml.Node {
	if v == nil {
		return nil
	}
	n := &yaml.Node{Kind: yaml.SequenceNode, Tag: "!!seq", Style: yaml.FlowStyle}
	for _, s := range *v {
		n.Content = append(n.Content, str(s))
	}
	return n
}
