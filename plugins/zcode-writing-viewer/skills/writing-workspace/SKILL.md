---
name: writing-workspace
description: 在 ZCode 中查看与审阅 writing-anything 工作区（.writing-anything/）：用本插件的 MCP 工具读取工作区状态、快照与作者正在看的位置，把作者指引到「写作工作区」面板完成单元审阅。用户在 ZCode 中提到写作工作区、面板、待审阅，或需要总览作品进度、产物与素材时使用。
---

# writing-workspace：ZCode 面板的工具与审阅指引

本插件为 writing-anything 工作区提供 ZCode 面板（「写作工作区」）与一组 MCP 工具。面板是只读的查看面；作者的确认与复核在面板的审阅视图（Review View）中完成，写入全部经 wa-cli。

## 工具

- `workspace_status`：定位项目目录与工作区存在状态（含候选探测轨迹）；未配置时按 server 工作目录（ZCode 打开的项目）定位。
- `workspace_snapshot`：工作区快照（Workspace Snapshot）——配置、作品（Work）与单元（Unit）树（阶段、进度、阻塞项）、产物（Artifact）全文、素材（Source）、决策（Decision）与复核摘要、validate 诊断与 wa-cli 区域降级原因。回答“现在写到哪、卡在哪”以它为准，不要自己去拼目录树。
- `open_workspace`：在 ZCode 中打开「写作工作区」面板（可带 unit 聚焦某单元）。把作者引到面板查看时先调用它。
- `show_in_viewer`：把面板定位到某单元或清单视图（素材、决策队列（Decision Queue）、复核记录），并在 Agent 面板标注说明。回答涉及具体单元/素材时用它指路。
- `get_view`：作者当前在面板看什么、选中了什么。作者说“这个”“这里”“选中的”时先调用它。
- `review_start` / `review_status` / `review_end`：审阅会话（Review Session）。面板自己会调用它们；Agent 通常只需经 `open_workspace` 请作者打开面板，在标「待审阅」的作品或单元上点「审阅」。

## 审阅指引（reviewSurface 为 zcode 时）

`wa-cli status --json` 的 `config.reviewSurface` 为 `zcode`（Agent 运行在 ZCode 中且未显式配置时自动生效）时：不要启动 `wa-cli preview --serve`、不交出 127.0.0.1 地址，而是请作者打开「写作工作区」面板，在标为「待审阅」的作品或单元上点「审阅」，在面板里完成反馈、确认与复核；作者说完成后，用 `wa-cli review show <作品> --json` 或 `wa-cli next --json` 复查结果再继续。作者说面板里没有「待审阅」入口（ZCode 未安装 zcode-writing-viewer 插件）时，改按 browser 的做法运行 `wa-cli preview <作品> --serve --json` 并交出 preview.url。

## 约束

- 面板与工具对工作区只读；写入只能经 wa-cli（作者的审阅提交由面板经 wa-cli 阅读页服务完成，确认渠道记为 preview）。
- 快照每次调用都重读工作区；不要轮询式反复调用（wa-cli 只读命令会向 .writing-anything/audit.log 追加审计行，每次快照的有界集合是 status、validate、next 各一次、每个长篇（novel）作品 timeline 一次）。
- 定位不到工作区时面板显示 `wa-cli init` 指引；需要新建工作区时按 wa-init 技能操作。
