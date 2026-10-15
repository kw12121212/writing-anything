---
name: wa-draft
description: 按大纲撰写草稿，登记论断并关联作者素材，通过文本检查。作品处于 draft 阶段时使用。
---

# wa-draft：草稿（Draft）

## 步骤

1. 运行 `wa-cli context <单元> --json`，只依据上下文包（Context Pack）中的写作简报（Brief）、大纲（Outline）、素材（Source）摘录与规范写作。
2. 写好正文后用 `wa-cli draft write <单元> --file <文件> --json` 写入。作者改过正文时（`draft.human-edited`），先运行 `wa-cli diff <单元> --json` 看作者的修改，基于作者的版本修改，再用 `--base <diff 输出的 base>` 写入；作者的编辑优先。
3. 亲历、观点、案例与数据都要登记为论断（Claim）并关联作者素材：`wa-cli claim add <单元> --kind <experience|opinion|case|data|fact> --text <句子> --sources <素材 id> --json`；用 `wa-cli claim list <单元> --json` 查看，`wa-cli claim set` 修正关联。版本号与 API 说法关联 reference 类型的素材。选类型时：亲历、观点、案例与作者自己的数据用 experience、opinion、case、data 并关联作者素材，外部事实数据用 fact 并关联 reference 素材；关联了素材的 fact 论断可覆盖文本检查的数据候选，但不覆盖亲历候选。
4. 运行 `wa-cli lint <单元> --json` 做文本检查（Lint），按发现项（Finding）的 message 修改正文后重新写入，直到没有 error 级发现项。不要自己写 lint-ignore 单处豁免（Inline Waiver）：只有作者写入的豁免生效。
5. 草稿阶段的必需产物含 marketing 时（如 wechat-marketing 文体），写齐营销要素（Marketing Elements）：用 `wa-cli marketing set <单元> --headline <id> --headline-text <标题> --json` 写入至少两个标题候选（Headline Candidates），用 `--digest`、`--opening` 与 `--cta` 写入文章摘要（Digest）、开头钩子（Hook）与行动号召（CTA）；`wa-cli marketing show <单元> --json` 列出仍缺的要素。然后运行 `wa-cli marketing ask <单元> --recommend <候选 id> --rationale <理由> --json` 把候选登记为标题决策，请作者选定。
6. 运行 `wa-cli advance <单元> --json` 进入审稿。

## 必须停下来问作者

- 正文需要作者的亲历、数据或观点而素材里没有时，向作者要素材，不要编造。
- 文本检查的发现项确属作者有意为之时，请作者在正文中写豁免并说明理由。
- 标题由作者选定：把标题决策交给作者，按作者的选择运行 `wa-cli decide <决策 id> --option <选项 id> --confirmation <作者原话摘要> --json`，不要替作者选。
