# AI 腔（AI Tone）规则的固定样本

这些样本是 article-tone-rules 的回归输入，随变更 ai-tone-guard 一起确定。测试以内置 tech-blog（`tb-*`）与 wechat-marketing（`wx-*`）合并后的规则逐个文件运行文本检查（Lint），只看 tone-patterns 与 tone-density 两条规则的发现项（Finding）。

- `plain/`：不含套路的平实对照稿，必须没有发现项。它们由 Agent 按「像人当面讲」写成，不是作者本人的文章，不代表人写样本的分布。
- `cliche/`：Agent 套路稿，每个文件至少有下表列出的发现项。

不要为了让测试通过而修改样本或内置阈值。实现的结果与下表不一致时，先核对切句、屏蔽区域（Masked Region）与各项定义是否符合 lint 规格的 tone-rule-types；仍不一致就停下来报告。

## 套路稿应有的发现项

| 文件 | 发现项 |
| --- | --- |
| tb-01-not-but | not-but |
| tb-02-first-second | first-second |
| tb-03-grand-opening | grand-opening、lets |
| tb-04-closing-in-short | closing-in-short |
| tb-05-formal-words | word-empower、word-lever、word-closed-loop |
| tb-06-connectives | tone-density：连接词密度（约 58.48，阈值 12） |
| tb-07-four-char | tone-density：四字片段密度（约 111.45，阈值 25） |
| tb-08-uniform-sections | tone-density：句长标准差（约 4.54，阈值 5）、节长变异系数（约 0.04，阈值 0.15） |
| wx-01-not-only | not-only |
| wx-02-closing-shows | closing-shows |
| wx-03-closing-visible | closing-visible |
| wx-04-formal-words | word-crucial、word-all-round、word-indispensable |
| wx-05-formal-words-2 | word-seamless、word-boost、word-deep-dive |
| wx-06-uniform-sentences | tone-density：句长标准差（约 0.71，阈值 5） |
| wx-07-list-heavy | tone-density：列表行占比（约 0.86，阈值 0.5）、四字片段密度（约 42.07，阈值 25） |

## 对照稿里故意放的边界情况

| 文件 | 不应命中的原因 |
| --- | --- |
| tb-01-tricky-shibushi | 「是不是」含「不是」，但同句没有「而是」 |
| tb-02-tricky-kejian | 「清晰可见」在段末句，但「可见」不在句首 |
| tb-03-tricky-zongzhi | 「总之」在句首，但不在段落最后一句 |
| tb-04-tricky-today | 同一句里有「在」和「的今天」，但「在」不在句首 |
| tb-05-tricky-shouxian | 只有「首先」，没有「其次」 |
| tb-06-code-masked | 「赋能」「抓手」「闭环」只在围栏代码块里 |
| wx-03-tricky-zai | 段首句以「在」开头，但「的今天」不在同一句 |

四篇长对照稿（`tb-07`、`tb-08`、`wx-04`、`wx-05`）超过 300 字，用来确认五项密度阈值都不触发；规划时的实测范围是：连接词 0 到 2.69，四字片段 2.69 到 11.99，句长标准差 7.21 到 11.10，列表行占比 0 到 0.30，节长变异系数 0.26 到 0.35。这些是规划时按规格定义用脚本算出的数值；Go 实现的字数不计标题行的 # 标记，`tb-07` 的四字片段密度为 12.17，其余项与上述范围一致，均以 `go test ./internal/lint -run TestToneFixtures` 的判定为准。
