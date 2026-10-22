package preview

import (
	"bytes"
	_ "embed"
	"encoding/json"
	"fmt"
	"sync"

	"github.com/santhosh-tekuri/jsonschema/v6"
)

// 本文件是阅读页数据（GET /page.json）的 JSON 契约：字段名为 camelCase，任何层级的数组没有元素时为空数组，
// 段落出处无法推导时为 null；正文等文字按原文给出，不做 HTML 转义，由消费方负责安全显示（preview-page-json）。

//go:embed page.schema.json
var pageSchema []byte

// PageSchema 返回阅读页数据的 JSON Schema（draft 2020-12）。
func PageSchema() []byte { return bytes.Clone(pageSchema) }

type findingJSON struct {
	Kind       string `json:"kind"`
	Label      string `json:"label"`
	Severity   string `json:"severity"`
	Message    string `json:"message"`
	Suggestion string `json:"suggestion"`
}

type paragraphJSON struct {
	Index    int           `json:"index"`
	Line     int           `json:"line"`
	Text     string        `json:"text"`
	Origin   *string       `json:"origin"`
	Findings []findingJSON `json:"findings"`
}

type unitJSON struct {
	Ref        string          `json:"ref"`
	Stage      string          `json:"stage"`
	HasDraft   bool            `json:"hasDraft"`
	Notes      []string        `json:"notes"`
	Global     []findingJSON   `json:"global"`
	Paragraphs []paragraphJSON `json:"paragraphs"`
}

type optionJSON struct {
	ID   string `json:"id"`
	Text string `json:"text"`
}

type confirmJSON struct {
	Kind        string       `json:"kind"`
	Unit        string       `json:"unit"`
	ID          string       `json:"id"`
	Label       string       `json:"label"`
	Detail      string       `json:"detail"`
	Options     []optionJSON `json:"options"`
	Recommend   string       `json:"recommend"`
	NeedsAnswer bool         `json:"needsAnswer"`
	CanReject   bool         `json:"canReject"`
}

type reviewJSON struct {
	Key   string `json:"key"`
	Label string `json:"label"`
}

type failureJSON struct {
	At      string `json:"at"`
	Command string `json:"command"`
	Exit    int    `json:"exit"`
}

type pageJSON struct {
	Title    string        `json:"title"`
	Units    []unitJSON    `json:"units"`
	Confirms []confirmJSON `json:"confirms"`
	Strict   bool          `json:"strict"`
	Reviews  []reviewJSON  `json:"reviews"`
	Failures []failureJSON `json:"failures"`
}

func findingsJSON(fs []Finding) []findingJSON {
	out := []findingJSON{}
	for _, f := range fs {
		out = append(out, findingJSON{Kind: f.Kind, Label: f.Label, Severity: f.Severity, Message: f.Message, Suggestion: f.Suggestion})
	}
	return out
}

// JSON 把阅读页数据编码为 page.json 的正文。
func JSON(p *Page) ([]byte, error) {
	out := pageJSON{Title: p.Title, Units: []unitJSON{}, Confirms: []confirmJSON{}, Strict: p.Strict,
		Reviews: []reviewJSON{}, Failures: []failureJSON{}}
	for _, u := range p.Units {
		uj := unitJSON{Ref: u.Ref, Stage: u.Stage, HasDraft: u.HasDraft, Notes: append([]string{}, u.Notes...),
			Global: findingsJSON(u.Global), Paragraphs: []paragraphJSON{}}
		for _, para := range u.Paragraphs {
			pj := paragraphJSON{Index: para.Index, Line: para.Line, Text: para.Text, Findings: findingsJSON(para.Findings)}
			if para.Origin != "" {
				o := para.Origin
				pj.Origin = &o
			}
			uj.Paragraphs = append(uj.Paragraphs, pj)
		}
		out.Units = append(out.Units, uj)
	}
	for _, c := range p.Confirms {
		cj := confirmJSON{Kind: c.Kind, Unit: c.Unit, ID: c.ID, Label: c.Label, Detail: c.Detail, Options: []optionJSON{},
			Recommend: c.Recommend, NeedsAnswer: c.NeedsAnswer, CanReject: c.CanReject}
		for _, o := range c.Options {
			cj.Options = append(cj.Options, optionJSON{ID: o.ID, Text: o.Text})
		}
		out.Confirms = append(out.Confirms, cj)
	}
	if p.Strict {
		for _, r := range p.Reviews {
			out.Reviews = append(out.Reviews, reviewJSON{Key: r.Key, Label: r.Label})
		}
	}
	for _, f := range p.Failures {
		out.Failures = append(out.Failures, failureJSON{At: f.At, Command: f.Command, Exit: f.Exit})
	}
	var b bytes.Buffer
	enc := json.NewEncoder(&b)
	enc.SetEscapeHTML(false) // 正文按原文给出：< > & 不转成 \u003c 等形式
	if err := enc.Encode(out); err != nil {
		return nil, err
	}
	return bytes.TrimSuffix(b.Bytes(), []byte("\n")), nil
}

var compiledSchema = sync.OnceValues(func() (*jsonschema.Schema, error) {
	doc, err := jsonschema.UnmarshalJSON(bytes.NewReader(pageSchema))
	if err != nil {
		return nil, err
	}
	c := jsonschema.NewCompiler()
	if err := c.AddResource("https://writing-anything.invalid/schemas/preview-page.schema.json", doc); err != nil {
		return nil, err
	}
	return c.Compile("https://writing-anything.invalid/schemas/preview-page.schema.json")
})

// ValidateJSON 按阅读页数据的 JSON Schema 校验 data；不符合时返回说明原因的错误。
func ValidateJSON(data []byte) error {
	sch, err := compiledSchema()
	if err != nil {
		return fmt.Errorf("阅读页数据的 JSON Schema 无法编译：%w", err)
	}
	v, err := jsonschema.UnmarshalJSON(bytes.NewReader(data))
	if err != nil {
		return err
	}
	return sch.Validate(v)
}
