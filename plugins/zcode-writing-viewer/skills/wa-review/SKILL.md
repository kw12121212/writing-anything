---
name: wa-review
description: 按评分标准做 AI 预审并记录审稿结果，汇总修订任务与验收是否就绪。作品处于 review 阶段时使用。
---

# wa-review：审稿（Review）

Agent 的审稿是 AI 预审（Pre-review），默认不阻塞，由作者做最终判断。

## 步骤

1. 运行 `wa-cli review show <单元> --json`：其中的 rubric 是本次审稿必须逐项给出结论的全部评分项（已合并文体与规范包的评分标准（Rubric），以及选用的写作法附带的评分项），同时可以看到文本检查（Lint）结果与验收关卡；运行 `wa-cli provenance <单元> --json` 查看段落编号。文体的评分标准含标题类评分项（如 wechat-marketing 的 4U 标题法）时，运行 `wa-cli marketing show <单元> --json`，按其中作者选定的 headline 评分。
2. 按 rubric 逐项给出结论（pass 或 fail）、证据与依据的段落（不针对段落的评分项，如评作者选定的标题，可以省略 paragraphs，把依据写在证据里），列出发现项（Finding，每项引用具体段落，可附引文与建议），按 `wa-cli help review record --json` 说明的格式写成 YAML，用 `wa-cli review record <单元> --file <文件> --json` 记录；缺少评分项结论时诊断会列出缺少的评分项。
3. 文体或启用的规范包声明了 AI 评分指标（Metric，见 `wa-cli config resolved --json` 的 scoredMetrics）时，运行 `wa-cli metrics plan <单元> --json` 取得打分任务；按其中的 sessions，每次开一个全新的独立会话（如新的子 Agent，不带其他会话的分数），只依据锚定说明给全部片段打分，用 `wa-cli metrics record <单元> --file <文件> --json` 写入（每个会话一个新的 session id）。不要自己合并分数或替分歧下结论：分歧超过阈值的片段会成为作者决策，`wa-cli metrics show <单元> --json` 汇总结果。
4. 作者愿意时，请作者给几个片段打分，用 `wa-cli metrics calibrate <单元> --metric <指标> --paragraph <段落> --score <分值> --confirmation <作者原话摘要> --json` 记录为校准（Calibration）样本；与作者一致率低的指标会降级为仅供参考。
5. 运行 `wa-cli metrics compute <单元> --json` 查看文风与节奏的确定性指标与文风漂移（Style Drift）：drift 中的每一项说明偏离了作者文风画像还是本作品已验收单元、偏离多少与段落位置。漂移只说明偏离、不评判好坏，也不阻塞验收；把它作为参考交给作者，是否调整由作者决定。
6. 再次运行 `wa-cli review show <单元> --json`，把 `revisions`（即修订任务，Revision Task）与 `gate.blockers` 交给作者，转到 wa-revise 处理。

## 会话协议（可执行模板）

把下面的模板复制进会话的 shell，之后按步骤逐条运行：失败即停，读诊断（Diagnostic）的 next 修复后重试；绝不 `|| true`、管道或输出重定向吞掉失败（shell 层吞错对状态机不可见，只能事后靠 stale 暴露）。

```bash
set -euo pipefail
wa() { local out; out=$(wa-cli "$@") || { printf '%s\n' "$out" >&2; return 1; }; printf '%s\n' "$out"; }
wa next --json   # 示例：每步先看下一步；把 wa 后面换成当前步骤的 wa-cli 命令与参数
```

经 harness 提问工具得到作者的决定后，同步回写决策队列（Decision Queue）保住审计链：先用 `wa-cli decision add --question <问题> --unit <单元> --json` 登记问题，作者答复后用 `wa-cli decide <决策 id> --option <选项> --answer <答复> --confirmation <作者原话摘要> --json` 记录决定；不要只在对话里问完就往下走。

## 必须停下来问作者

- 审稿结论只是建议：哪些发现项要改、哪些保留，由作者决定。
- AI 评分的分歧由作者用 `wa-cli decide` 决定；校准打分必须是作者本人给出的分数，不能由 Agent 代填。
