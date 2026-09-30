package draft

import (
	"fmt"
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
	ID      string   `json:"id"`
	Kind    string   `json:"kind"`
	Text    string   `json:"text"`
	Sources []string `json:"sources"`
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

// AddClaim 在单元 u 的 claims.yaml 末尾追加论断。id 为空时取 claim-<n>，n 从已有论断数加一开始递增到不重复；
// sources 中的素材必须已登记。claims.yaml 不存在时创建，已存在时只追加该项，保留其他内容与注释。
// 返回新论断与变更的路径（新建的单元目录与 claims.yaml）；出错时不修改文件。
func AddClaim(root string, w *work.Work, u *work.Unit, kind, text, id string, sources []string) (*Claim, []string, []workspace.Problem) {
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
