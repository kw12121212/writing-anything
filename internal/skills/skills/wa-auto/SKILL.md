---
name: wa-auto
description: 用 wa-cli next 驱动整篇文章或整部长篇从写作简报走到导出，按文体分流文章路径与小说路径，在每个需要作者的节点停下来，并在会话之间用交接记录接续。用户想让 Agent 按规范完成整个写作流程时使用。
---

# wa-auto：按下一步推进

## 步骤

1. 运行 `wa-cli next --json`。没有工作区时先用 wa-init。`steps` 中条目的 `handoff` 不为 null 时，说明有上一次会话的交接记录（Handoff Note），该字段只含 id 与 todo；运行 `wa-cli handoff show --json` 读取完整的最近一条，按其中的 done、todo、作者指示（instructions）与风险（risks）接续，不要重做已完成的事项。`steps` 为空时也运行一次 handoff show，确认没有遗留的待办。
2. 按作品的文体（Genre）选择路径：在 `wa-cli status --json` 的 units 中，作品根单元的 type 为 novel、或作品下有 part、chapter 类型的单元时按小说路径处理，其余按文章路径处理（config.defaultGenre 只是新建作品时的默认文体）：
   - 文章路径（如 tech-blog、wechat-marketing、short-story）：对每个单元（Unit），按阶段（Stage）字段 `stage` 选择技能：brief 即写作简报（Brief）→ wa-brief，outline → wa-outline，draft → wa-draft，review → wa-review、wa-revise 与 wa-publish。
   - 小说路径（novel）：动笔前先用 wa-plan 逐步规划并经作者确认（`wa-cli plan show <作品> --json` 的 `currentStep` 为 null 表示规划完成）；之后按单元树顺序逐场景用 wa-scene 推进（场景契约、起草、写后收口、作者确认状态与知情），review 阶段用 wa-review 记录审稿、用 wa-continuity 做连续性审查与审查任务包，再按 wa-publish 请作者验收；一部的最后一个场景验收后用 wa-assemble 请作者确认卷末检查点；全书场景都验收后用 wa-assemble 组稿，再用 wa-publish 导出。修改已验收内容时先用 wa-continuity 做影响传播。
   按 `commands` 运行可以直接运行的命令，按 `action` 与 `blockers` 判断还缺什么。
3. 单元有未处理的作者反馈（Feedback，见输出的 `feedback`）时先用 wa-revise 处理。
4. 每推进一步都重新运行 `wa-cli next --json`，直到 `steps` 为空（所有单元都已走完全部阶段），再用 wa-publish 导出（长篇先用 wa-assemble 组稿）。`pendingAuthorReviews` 不为空时，运行其中的 command 启动阅读页，把输出的 preview.url 交给作者复核。
5. 会话结束前（作者要离开、上下文快用完或一部写完时），按 wa-assemble 的格式运行 `wa-cli handoff write --file <文件> --json` 写交接记录；新会话从第 1 步的 handoff 接续，需要更早的记录时运行 `wa-cli handoff show --last <条数> --json`。
6. 遇到看不懂的诊断时运行 `wa-cli help <命令> --json` 查看用法。
7. 只能使用 MCP 的 Agent 可以通过 `wa-cli mcp --dir <项目目录>` 接入：每条命令对应一个 tool（命令名的空格换成下划线，如 `feedback_add`），结果与加 `--json` 运行命令相同；订阅资源 wa://feedback 可以即时收到作者的新反馈。

## 必须停下来问作者

- 各技能列出的作者节点：写作简报的内容、大纲的取舍、缺少的素材、修订的取舍与最终验收；长篇另有每个规划步骤、场景契约、状态与知情的确认、连续性发现的豁免与卷末检查点。不要为了推进而替作者作答。
- 交接记录只记录事实与作者说过的指示，不要把自己的推测写成作者的指示。
