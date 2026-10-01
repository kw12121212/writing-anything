package workspace

import (
	"slices"
	"strconv"

	"go.yaml.in/yaml/v3"
)

// 本文件是修改结构化产物时共用的 YAML 节点操作（workspace-artifacts/structured-edits）：只改目标节点，保留其他字段与
// 注释；目标节点、途经的节点或被替换、删除的子树带锚点、是别名或含合并键时不修改。

// Str 返回字符串标量节点。
func Str(v string) *yaml.Node { return &yaml.Node{Kind: yaml.ScalarNode, Tag: "!!str", Value: v} }

// Int 返回整数标量节点。
func Int(v int) *yaml.Node {
	return &yaml.Node{Kind: yaml.ScalarNode, Tag: "!!int", Value: strconv.Itoa(v)}
}

// Seq 返回取值 vs 组成的序列节点；vs 为空时返回 nil（表示删除字段）。flow 为 true 时用流式（适合 id 列表）。
func Seq(vs []string, flow bool) *yaml.Node {
	if len(vs) == 0 {
		return nil
	}
	n := &yaml.Node{Kind: yaml.SequenceNode, Tag: "!!seq"}
	if flow {
		n.Style = yaml.FlowStyle
	}
	for _, v := range vs {
		n.Content = append(n.Content, Str(v))
	}
	return n
}

// Plain 报告节点本身既没有锚点也不是别名。
func Plain(n *yaml.Node) bool { return n.Anchor == "" && n.Kind != yaml.AliasNode }

// HasMerge 报告映射 m 是否含合并键。
func HasMerge(m *yaml.Node) bool {
	for i := 0; i+1 < len(m.Content); i += 2 {
		if m.Content[i].ShortTag() == "!!merge" {
			return true
		}
	}
	return false
}

// HasAnchor 报告节点 n 或其子树中是否有锚点、别名或合并键。
func HasAnchor(n *yaml.Node) bool {
	if !Plain(n) || n.Kind == yaml.MappingNode && HasMerge(n) {
		return true
	}
	return slices.ContainsFunc(n.Content, HasAnchor)
}

// Lookup 返回映射 m 中键 key 的值节点（不存在时为 nil）与能否直接修改它（值本身不带锚点、不是别名）。
func Lookup(m *yaml.Node, key string) (*yaml.Node, bool) {
	for i := 0; i+1 < len(m.Content); i += 2 {
		if m.Content[i].Value == key {
			v := m.Content[i+1]
			return v, Plain(v)
		}
	}
	return nil, true
}

// IndexByID 返回序列 seq 中 id 键为 id 的映射项的下标；没有时为 -1。
func IndexByID(seq *yaml.Node, id string) int {
	for i, item := range seq.Content {
		if item.Kind != yaml.MappingNode {
			continue
		}
		for j := 0; j+1 < len(item.Content); j += 2 {
			if item.Content[j].Value == "id" && item.Content[j+1].Value == id {
				return i
			}
		}
	}
	return -1
}

// SetKey 把映射 m 中的 key 设为 n（nil 表示删除）：已有的键原地替换值，保留值上的注释与作者选择的风格，取值相同的标量
// 保持原样；两者都是映射时逐个修改其中的键。新键插在 order 中排在它之前的最后一个已有键之后。被替换或删除的子树带锚点、
// 别名或合并键时不修改，返回 false 与该处相对 m 的字段路径。
func SetKey(m *yaml.Node, order []string, key string, n *yaml.Node) (string, bool) {
	for i := 0; i+1 < len(m.Content); i += 2 {
		if m.Content[i].Value != key {
			continue
		}
		v := m.Content[i+1]
		if n != nil && v.Kind == yaml.MappingNode && n.Kind == yaml.MappingNode && Plain(v) && !HasMerge(v) {
			for j := 0; j+1 < len(n.Content); j += 2 {
				if f, ok := SetKey(v, nil, n.Content[j].Value, n.Content[j+1]); !ok {
					return key + "." + f, false
				}
			}
			return "", true
		}
		// 删除锚点会使别处的别名失效，替换别名会改变别处的取值。
		if HasAnchor(v) {
			return key, false
		}
		if n == nil {
			m.Content = slices.Delete(m.Content, i, i+2)
			return "", true
		}
		if v.Kind == yaml.ScalarNode && n.Kind == yaml.ScalarNode && v.Value == n.Value && v.ShortTag() == n.ShortTag() {
			return "", true
		}
		n.HeadComment, n.LineComment, n.FootComment = v.HeadComment, v.LineComment, v.FootComment
		if n.Kind == v.Kind {
			n.Style = v.Style
		}
		m.Content[i+1] = n
		return "", true
	}
	if n == nil {
		return "", true
	}
	rank := slices.Index(order, key)
	at := 0
	for i := 0; i+1 < len(m.Content); i += 2 {
		if r := slices.Index(order, m.Content[i].Value); r >= 0 && r < rank {
			at = i + 2
		}
	}
	m.Content = slices.Insert(m.Content, at, Str(key), n)
	return "", true
}
