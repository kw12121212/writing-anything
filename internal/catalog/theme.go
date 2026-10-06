package catalog

import "regexp"

// 样式主题（HTML Theme）：HTML 导出时各元素的内联样式（Inline Style）。规范包用 html_theme 按元素覆盖内置的默认主题；
// 元素名与取值的格式由 pack.schema.json 校验（只允许简单的 CSS 声明，不能加载外部资源或执行脚本）。

// HTMLThemeKeys 是样式主题的元素名，按输出顺序排列。
var HTMLThemeKeys = []string{"wrapper", "h1", "h2", "h3", "h4", "p", "strong", "em", "code", "pre", "blockquote", "ul", "ol", "li", "hr",
	"figure", "caption", "footnote", "references"}

// DefaultHTMLTheme 是内置的默认样式主题，适合公众号正文的阅读排版。
var DefaultHTMLTheme = map[string]string{
	"wrapper":    "font-size: 16px; line-height: 1.75; color: #333; letter-spacing: 0.5px; word-break: break-word",
	"h1":         "font-size: 22px; font-weight: bold; color: #222; margin: 24px 0 16px",
	"h2":         "font-size: 20px; font-weight: bold; color: #222; margin: 24px 0 12px; padding-left: 8px; border-left: 4px solid #07c160",
	"h3":         "font-size: 18px; font-weight: bold; color: #222; margin: 20px 0 12px",
	"h4":         "font-size: 16px; font-weight: bold; color: #222; margin: 16px 0 8px",
	"p":          "margin: 0 0 16px",
	"strong":     "font-weight: bold; color: #222",
	"em":         "font-style: italic",
	"code":       "font-family: Menlo, Consolas, monospace; font-size: 14px; background: #f3f3f3; padding: 2px 4px; border-radius: 3px",
	"pre":        "font-family: Menlo, Consolas, monospace; font-size: 13px; line-height: 1.6; background: #f6f8fa; padding: 12px; border-radius: 4px; margin: 0 0 16px; white-space: pre-wrap; word-break: break-all",
	"blockquote": "margin: 0 0 16px; padding: 8px 12px; border-left: 3px solid #ddd; color: #666; background: #f9f9f9",
	"ul":         "margin: 0 0 16px; padding-left: 24px",
	"ol":         "margin: 0 0 16px; padding-left: 24px",
	"li":         "margin: 4px 0",
	"hr":         "border: none; border-top: 1px solid #e5e5e5; margin: 24px 0",
	"figure":     "margin: 16px 0; padding: 24px 12px; border: 1px dashed #bbb; background: #fafafa; text-align: center; color: #888",
	"caption":    "font-size: 13px; color: #999; margin: 8px 0 0",
	"footnote":   "font-size: 12px; color: #07c160",
	"references": "font-size: 13px; color: #888; margin: 24px 0 0",
}

// CSSPattern 与 UnsafeCSSPattern 是样式主题取值的格式：一个或多个「属性: 值」，属性名为小写字母与连字符，值只能由英文字母、数字、
// 空格与 # % . , ( ) + - 组成（字符白名单），且不区分大小写地不含 url(、expression 与 javascript。pack.schema.json 使用同样的模式。
const (
	CSSPattern       = `^[ \t]*[a-z-]+[ \t]*:[ \t]*[A-Za-z0-9#%., ()+-]+([ \t]*;[ \t]*[a-z-]+[ \t]*:[ \t]*[A-Za-z0-9#%., ()+-]+)*[ \t]*;?[ \t]*$`
	UnsafeCSSPattern = `(?i)url\(|expression|javascript`
)

var (
	cssRE       = regexp.MustCompile(CSSPattern)
	unsafeCSSRE = regexp.MustCompile(UnsafeCSSPattern)
)

// SafeCSS 报告 v 是否是可以写进 style 属性的样式主题取值。
func SafeCSS(v string) bool { return cssRE.MatchString(v) && !unsafeCSSRE.MatchString(v) }

// DefaultDisclosureTemplate 是内置的 AI 参与说明（AI-generated Content Label）模板；规范包可以用 ai_disclosure_template 覆盖。
// 占位符：{ai_percent} 为 AI 参与段落的字数占比，{ai_paragraphs} 与 {paragraphs} 为 AI 参与段落数与全部段落数。
const DefaultDisclosureTemplate = "本文在 AI 辅助下完成：约 {ai_percent}% 的内容由 AI 起草，均经作者审阅确认。"
