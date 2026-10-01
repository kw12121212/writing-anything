---
name: wa-auto
description: 用 wa-cli next 驱动整篇文章从写作简报走到导出，在每个需要作者的节点停下来。用户想让 Agent 按规范完成整个写作流程时使用。
---

# wa-auto：按下一步推进

## 步骤

1. 运行 `wa-cli next --json`。没有工作区时先用 wa-init。
2. 对每个单元（Unit），按阶段（Stage）字段 `stage` 选择技能：brief 即写作简报（Brief）→ wa-brief，outline → wa-outline，draft → wa-draft，review → wa-review、wa-revise 与 wa-publish。按 `commands` 运行可以直接运行的命令，按 `action` 与 `blockers` 判断还缺什么。
3. 单元有未处理的作者反馈（Feedback，见输出的 `feedback`）时先用 wa-revise 处理。
4. 每推进一步都重新运行 `wa-cli next --json`，直到 `steps` 为空（所有单元都已走完全部阶段），再用 wa-publish 导出。
5. 遇到看不懂的诊断时运行 `wa-cli help <命令> --json` 查看用法。

## 必须停下来问作者

- 各技能列出的作者节点：写作简报的内容、大纲的取舍、缺少的素材、修订的取舍与最终验收。不要为了推进而替作者作答。
