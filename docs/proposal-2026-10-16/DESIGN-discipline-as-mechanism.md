# wa 工具链改进提案：把 agent 操作纪律落进机制（交上游版）

> 由一次端到端真实写作流程（tech-blog 文章《九年画饼，一夜起飞》tesla-flying-car）的复盘产出。
> 方法论：提示词是劝告，机制是约束。目标不是让模型变乖，而是让 P0 级纪律不再依赖模型自觉。

## 0. 元信息

- **版本**：wa-cli v0.2.0（commit `9b9e69cba58cdb8e557138f2ac2ef9ec63da1b45`）
- **环境**：Linux x86_64，二进制安装于 `~/.local/bin/wa-cli`，workspace gate level=standard
- **完整用例**：见附件 `wa-fixture-tesla-flying-car.tar.gz`（已验收作品的 `.writing-anything/` 全量产物：brief / outline / claims / lint / review / acceptance / handoff，可直接作回归样例）
- **置信度标注**：【已验证】= 本次会话有实际观察输出；【待复现】= 由行为推断，需上游确认（推断内容已注明）

## 1. 背景与动机

纯网调文章（全部素材来自公开报道的 reference）大概率是这套流程的最高频场景。本次跑通暴露的问题集中在两类：**边界语义**（fact vs data、二手引语、矛盾素材）与**静默失败**（brief 多消息丢弃、warning 不可消、失败被 agent 吞错掩盖）。共同根因：这些约束只存在于技能 prose 里，没有对应的 schema 校验或状态机 blocker。

三层落法：**L1 Schema**（错误输入直接拒绝，不可表示）、**L2 状态机**（纪律变成 next 的 step / gate 的 blocker，不可跳过）、**L3 脚本**（技能带可执行脚本，不可偷懒）。

## 2. 问题与提案明细

### P0-1 audit log + next 回捞【L2】
- **现象**：agent 在 shell 层用 `|| true` / 输出重定向吞掉命令失败，状态机完全不可见（本次实际发生：`review record` 失败被吞，靠事后 `stale: true` 才暴露）。【已验证】
- **提案**：wa-cli 每次调用（含失败）追加写 `.writing-anything/audit.log`（命令、args、exit code、诊断摘要、时间戳）；`next` 增加 step 类型「关键命令（draft write / review record / accept / export）失败后无成功重试」；preview 页展示 audit 摘要。
- **验收**：GIVEN review record 失败且未重试，WHEN next，THEN 出现对应 step；audit.log 含该次失败记录。
- **注意**：并发写与轮转策略；validate 需纳入该文件。

### P0-2 claim kind×source 写入校验 + 认领规则修正【L1】
- **现象 A**：`claim add --kind data --sources <纯 reference>` 写入成功无警告，直到 `lint` 才报 error（本次 7 条返工为 fact）。【已验证】上游 36c4585 已在 wa-draft 技能补论断类型选择指引（prose 层，感谢），但 L1 写入时校验与 `--override` 例外通道仍缺失——prose 指引属概率层，本提案维持 Schema 层诉求。
- **现象 B（catch-22）——上游已修复，实测确认**：36c4585「关联了素材的 fact 论断覆盖数据候选」。用仓库 HEAD 构建对 fixture 实测：line 31 两句的 warning 被逐字 fact 论断成功认领，warning 3→1。
- **遗留新发现（随 36c4585 实测发现）**：标题行（如 draft line 29「## 0.81 秒、33.2 万、2028」）仍被标为数据候选，但标题不是句子、论断文本无法与之逐字匹配 → 标题级候选目前不可消。建议：检测跳过标题行，或提供论断显式认领标题的机制。
- **版本提醒**：已安装二进制为 v0.2.0（`9b9e69c`），修复需发版或更新安装后才对终端用户生效。
- **复现**：fixture + 两版对比——v0.2.0：`wa-cli lint tesla-flying-car` → 3 条 warning；HEAD（36c4585）：同命令 → 1 条（仅标题行）。
- **验收**：①GIVEN `claim add --kind data` 且来源全部为 reference，WHEN add，THEN 拒绝并提示改用 fact（或 `--override` 留痕通过）；②GIVEN 标题行含数字，WHEN lint，THEN 不产生不可认领的候选（或可被显式认领）。

### P1-1 brief 字段出处强制【L2】
- **现象**：brief 的 audience/purpose/length 等必填项不携带出处，作者在 harness 提问工具里给的答案无法回溯为 decision，审计链断裂。【已验证（流程观察，非代码缺陷，但机制缺口真实）】
- **提案**：必填字段须携带 decision id 或 source id（与 key message 已有的 `--message-decision` 同构），缺出处不能 advance。
- **迁移**：存量进行中的作品会被打断——建议仅对新建作品启用，或提供给存量 brief 补 decision 的迁移命令。

### P1-2 source conflict 登记【L2】
- **现象**：两份素材对同一参数说法矛盾（InsideEVs「10 喷嘴、250kg 压缩气体」vs 腾讯/文学城「4 个推进器、8 万牛推力」），工具无登记处，只能靠 agent 在素材文件里并列两说并在正文回避——作者对此并不知情。【已验证】
- **提案**：`wa-cli conflict add --about <主题> --a "<说法+素材id>" --b "<说法+素材id>"`；未决 conflict 阻塞 advance；正文引用冲突任一方时校验其状态为 decided/waived；preview 页列出冲突与作者决定。
- **验收**：GIVEN 存在 open conflict，WHEN advance，THEN 被阻塞并列出该 conflict。

### P1-3 技能脚本化（wa-* 技能随 wa-cli 分发）【L3】
- **提案**：wa-* 技能的「会话协议」段从 prose 改为可执行模板（`set -euo pipefail`、禁 `|| true`、逐条检查 exit code 的 wrapper 样例）；参照 java-* 技能 `scripts/*.cjs --json` 的模式。

### P2-1 source tier 与 url【L1】
- **现象**：source 只有文件没有一手 URL；引语出自二手转述（x_search 降级时经 BI/Yahoo 转述）无等级标记，作者无从知情。【已验证】
- **提案**：`source add --url <地址> --tier <primary|secondary>`（可选字段，向后兼容）；claim 继承 tier；正文引用 secondary 引语时 review rubric 自动追加 blocking 项「二手信源已向作者明示」。

### P2-2 CLI 输入严格化【L1】
- **现象**：`brief set` 同命令两组 `--message ...` 仅保留一组，无诊断（本次实际丢失 msg-not-ppt，补第二条命令才恢复）。【已验证】
- **提案**：重复 flag 组报 usage error。顺带：`handoff write` 允许 todo 省略（干净收尾不必硬编待办；本次因 schema 要求 todo≥1 被迫补了一条）。

### P2-3 review 增量重记【易用性】
- **现象**：正文小改（本次 15 字措辞修正）后需整份重写 review YAML；且旧 YAML 中发现项引文已不在正文时 record 失败——该失败的具体诊断未能捕获（输出被 agent 吞掉，正好是 P0-1 的案例）。【现象已验证；失败诊断待复现】
- **提案**：`review record --reaffirm [--resolve <finding>...]`：rubric 沿用、指定发现项标记 resolved 的一键重申。设计原则：合规必须是阻力最小的路径。

### P3 preview confirmation 输出【小 bug】
- **现象**：`--serve` 模式 confirmation 事件 `"id": ""`。【已验证，观察输出：`{"kind":"acceptance","unit":"tesla-flying-car","id":"","action":"confirm","channel":"preview"}`】

## 3. 范围划分

- **上游 wa-cli**：§2 全部 + wa-* 技能文本（技能随 wa-cli 分发，`wa-cli skills install`）。
- **本地/用户侧（不属本提案，附述背景）**：运行环境 PATH 根治（DSH 服务需从完整环境启动）、agent-reach 后端安装与登录。上游若采纳 P1-3，技能侧的降级说明归上游。

## 4. 给上游的测试建议

每条验收标准建议直接转单元/集成测试；fixture 内的 tesla-flying-car 工作区可作 P0-2 的现成回归样例（claims 与 lint warning 的对应关系已在上文钉死）。
