---
name: wa-plan
description: 按规划类写作法（如雪花写作法十步）分步规划长篇：每步产出规划产物、关联素材与产物链接，经作者确认后进入下一步。novel 作品动笔前，或 wa-cli plan show 显示有待确认步骤时使用。
---

# wa-plan：规划（Plan）

## 步骤

1. 运行 `wa-cli plan show <单元> --json` 查看规划：`plan.method` 是选用的规划类写作法，`steps` 列出全部步骤与状态（pending 未起草、drafted 待作者确认、confirmed 已确认），`currentStep` 是当前要处理的步骤。步骤的说明用 `wa-cli method show <写作法 id> --json` 查看。
2. 起草当前步骤：与作者讨论出该步的内容后，运行 `wa-cli plan step <单元> <步骤 id> --text <规划文本> --source <素材 id> --json` 关联已登记的素材（`wa-cli source list --json` 查看）；核心内容没有对应素材时，交给作者确认后由确认记录承载。
   - 声明了规划产物（Planning Product）的步骤，先经既有命令落地产物再用 `--link` 关联：characters 用 `wa-cli canon add <作品> --type character --name <名称> --text <设定> --json` 写人物卡，`--link <条目 id>`；scenes 用 `wa-cli unit add <章> scene <场景 id> --json` 登记场景，`--link <场景单元引用>`；scene-contracts 用 `wa-cli scene contract <场景> <节 id> --title <标题> ... --json` 写场景契约，`--link <场景单元引用>:<大纲节 id>`。
3. 把该步的规划文本（与产物）交给作者看，作者同意后运行 `wa-cli plan confirm <单元> <步骤 id> --confirmed-by-author --confirmation <作者原话摘要> --json` 记录确认；确认成功后 `currentStep` 前进到下一步。
4. 上一步未经作者确认时不能写或确认后面的步骤（plan.step-order）；后续步骤推翻了已确认的前一步时，经作者同意用 `wa-cli plan step <单元> <步骤 id> --redo --confirmed-by-author --confirmation <作者原话摘要> --text <新文本> --json` 重置该步（其后步骤的记录一并删除，产物不动），再重新逐级确认。
5. 全部步骤确认（`currentStep` 为 null）后进入写作：用 wa-scene 逐场景推进（场景契约、起草、写后收口与作者确认台账），审稿与验收后由 wa-assemble 处理卷末检查点与组稿。

## 必须停下来问作者

- 每一步的规划文本必须经作者确认才能继续；`--confirmed-by-author` 与 `--confirmation` 只记录作者的原话摘要，不能替作者决定。
- `--redo` 会删除被重置步骤之后的全部确认记录，必须先征得作者同意。
