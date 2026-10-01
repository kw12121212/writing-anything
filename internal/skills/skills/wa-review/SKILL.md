---
name: wa-review
description: 按评分标准做 AI 预审并记录审稿结果，汇总修订任务与验收是否就绪。作品处于 review 阶段时使用。
---

# wa-review：审稿（Review）

Agent 的审稿是 AI 预审（Pre-review），默认不阻塞，由作者做最终判断。

## 步骤

1. 评分标准（Rubric）见 `wa-cli config resolved --json` 的 rubric，选用的写作法附带的评分项见 `wa-cli method show <写作法 id> --json`；运行 `wa-cli provenance <单元> --json` 查看段落编号，`wa-cli review show <单元> --json` 查看文本检查（Lint）结果与验收关卡。文体的评分标准含标题类评分项（如 wechat-marketing 的 4U 标题法）时，运行 `wa-cli marketing show <单元> --json`，按其中作者选定的 headline 评分。
2. 按评分标准逐项给出结论（pass 或 fail）、证据与段落，列出发现项（Finding，每项引用具体段落，可附引文与建议），按 `wa-cli help review record --json` 说明的格式写成 YAML，用 `wa-cli review record <单元> --file <文件> --json` 记录；缺少评分项结论时诊断会列出缺少的评分项。
3. 再次运行 `wa-cli review show <单元> --json`，把 `revisions`（即修订任务，Revision Task）与 `gate.blockers` 交给作者，转到 wa-revise 处理。

## 必须停下来问作者

- 审稿结论只是建议：哪些发现项要改、哪些保留，由作者决定。
