---
name: wa-auto
description: 用 wa-cli next 驱动整篇文章或整部长篇从写作简报走到导出，按文体分流文章路径与小说路径，在每个需要作者的节点停下来，并在会话之间用交接记录接续。用户想让 Agent 按规范完成整个写作流程时使用。
---

# wa-auto：按下一步推进

## 步骤

1. 运行 `wa-cli next --json`。没有工作区时先用 wa-init。`steps` 中条目的 `handoff` 不为 null 时，说明有上一次会话的交接记录（Handoff Note），该字段只含 id、todo 与作者指示（instructions），不加 --json 时同样内容显示在 next 文本输出顶部；运行 `wa-cli handoff show --json` 读取完整的最近一条，按其中的 done、todo、instructions 与风险（risks）接续，不要重做已完成的事项。`steps` 为空时也运行一次 handoff show，确认没有遗留的待办。
2. 按作品的文体（Genre）选择路径：在 `wa-cli status --json` 的 units 中，作品根单元的 type 为 novel、或作品下有 part、chapter 类型的单元时按小说路径处理，其余按文章路径处理（config.defaultGenre 只是新建作品时的默认文体）：
   - 文章路径（如 tech-blog、wechat-marketing、short-story）：对每个单元（Unit），按阶段（Stage）字段 `stage` 选择技能：brief 即写作简报（Brief）→ wa-brief，outline → wa-outline，draft → wa-draft，review → wa-review、wa-revise 与 wa-publish。
   - 小说路径（novel）：动笔前先用 wa-plan 逐步规划并经作者确认（`wa-cli plan show <作品> --json` 的 `currentStep` 为 null 表示规划完成）；之后按单元树顺序逐场景用 wa-scene 推进（场景契约、起草、写后收口、作者确认状态与知情），review 阶段用 wa-review 记录审稿、用 wa-continuity 做连续性审查与审查任务包，再按 wa-publish 请作者验收；一部的最后一个场景验收后用 wa-assemble 请作者确认卷末检查点；全书场景都验收后用 wa-assemble 组稿，再用 wa-publish 导出。修改已验收内容时先用 wa-continuity 做影响传播。
   按 `commands` 运行可以直接运行的命令，按 `action` 与 `blockers` 判断还缺什么。stage 为 done 的条目表示已完成单元的验收（Acceptance）已失效（正文在验收后被修改、验收记录无效或关卡强度调高），其 commands 为 `wa-cli reopen <单元> --json`：先告诉作者哪些单元要退回并征得同意，再运行 reopen，之后按 wa-review、wa-publish 重新审稿并请作者重新验收。
3. 单元有未处理的作者反馈（Feedback，见输出的 `feedback`）时先用 wa-revise 处理。
4. 每推进一步都重新运行 `wa-cli next --json`，直到 `steps` 为空（所有单元都已走完全部阶段且验收有效），再用 wa-publish 导出（长篇先用 wa-assemble 组稿）。`pendingAuthorReviews` 不为空时，先运行 `wa-cli status --json` 查看 config.reviewSurface（审阅位置（Review Surface），即作者在哪里审阅阅读页（Reading View））。为 dsh 时：不要启动 `wa-cli preview --serve`，也不要交出 127.0.0.1 地址，而是请作者打开 DSH 插件（DSH Plugin）面板，在标为「待审阅」的作品上点「审阅」完成复核；作者说完成后，用 `wa-cli next --json` 复查再继续。为 browser 时：运行其中的 command 启动阅读页，把输出的 preview.url 交给作者复核；阅读页是作者复核期间持续运行的本地服务，按 wa-publish 的说明用后台任务运行，先确认该地址可以访问再交给作者，作者说打不开时重新运行该命令并交出新的地址。
5. 会话结束前（作者要离开、上下文快用完或一部写完时），按 wa-assemble 的格式运行 `wa-cli handoff write --file <文件> --json` 写交接记录；新会话从第 1 步的 handoff 接续，需要更早的记录时运行 `wa-cli handoff show --last <条数> --json`。
6. 遇到看不懂的诊断时运行 `wa-cli help <命令> --json` 查看用法。
7. 只能使用 MCP 的 Agent 可以通过 `wa-cli mcp --dir <项目目录>` 接入：每条命令对应一个 tool（命令名的空格换成下划线，如 `feedback_add`），结果与加 `--json` 运行命令相同；订阅资源 wa://feedback 可以即时收到作者的新反馈。能运行 shell 的 Agent 直接运行 wa-cli 即可，结果与经 MCP 调用相同；MCP 服务面向只能调用 MCP tool 的 Agent。

## 会话协议（可执行模板）

把下面的模板复制进会话的 shell，之后按步骤逐条运行：失败即停，读诊断（Diagnostic）的 next 修复后重试；绝不 `|| true`、管道或输出重定向吞掉失败（shell 层吞错对状态机不可见，只能事后靠 stale 暴露）。

```bash
set -euo pipefail
wa() { local out; out=$(wa-cli "$@") || { printf '%s\n' "$out" >&2; return 1; }; printf '%s\n' "$out"; }
wa next --json   # 示例：每步先看下一步；把 wa 后面换成当前步骤的 wa-cli 命令与参数
```

经 harness 提问工具得到作者的决定后，同步回写决策队列（Decision Queue）保住审计链：先用 `wa-cli decision add --question <问题> --unit <单元> --json` 登记问题，作者答复后用 `wa-cli decide <决策 id> --option <选项> --answer <答复> --confirmation <作者原话摘要> --json` 记录决定；不要只在对话里问完就往下走。

## 必须停下来问作者

- 各技能列出的作者节点：写作简报的内容、大纲的取舍、缺少的素材、修订的取舍与最终验收；长篇另有每个规划步骤、场景契约、状态与知情的确认、连续性发现的豁免与卷末检查点。不要为了推进而替作者作答。
- 交接记录只记录事实与作者说过的指示，不要把自己的推测写成作者的指示。
