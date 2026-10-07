---
name: wa-scene
description: 逐场景推进长篇（novel）：写场景契约、按契约起草正文、写后收口，并把状态变更提议与知情事件交作者确认，再进入审稿与验收。novel 作品规划完成后，或 wa-cli next 指向某个场景时使用。
---

# wa-scene：长篇场景

长篇的每个场景（Scene）按「场景契约（Scene Contract）→ 草稿（Draft）→ 写后收口（Close-out）→ 作者确认台账 → 审稿（Review）与验收（Acceptance）」推进。台账即设定集（Canon）、状态台账（State Ledger）、知情表（Knowledge State）与伏笔台账（Foreshadowing Ledger），只经 wa-cli 写入，只有作者确认过的条目才参与后续的状态快照、上下文包（Context Pack）与连续性审查。

## 步骤

1. 运行 `wa-cli next <场景> --json` 查看场景所处的阶段与阻塞项（Blocker）。条目的 `handoff` 不为 null 时说明有上一次会话的交接记录（Handoff Note），它只含 id 与 todo；运行 `wa-cli handoff show --json` 读取完整记录（含作者指示与风险），按其接续。
2. 场景契约：动笔前用 `wa-cli scene contract <场景> <节 id> --title <标题> --pov <视角人物> --location <地点> --characters <出场人物> --beats <节拍 id> --reveal <揭示边界> --json` 声明本场景（规划阶段已写过时只补缺项）。`--beats` 指向故事根单元大纲中的情节节拍（Story Beat），`--reveal` 是揭示边界（Reveal Boundary），即本场景不能提前透露的信息。必须发生的变化用 `--state-change <id>=<文本>`，伏笔用 `--plant <伏笔 id>` 与 `--recover <伏笔 id>`，起因（Cause）与预期事件（Event）用 `--cause <前文事件 id>` 与 `--event <事件 id>`，故事时间（Story Time）与先后用 `--time <时间标记>` 与 `--after <前文场景>`。起因只能引用前文已经发生的事件：上一场收口确认了事件后，再为下一场补 `--cause`。新伏笔先用 `wa-cli threads add <作品> --text <伏笔> --plant-in <场景> --recover-at <场景> --json` 登记。
3. 写作简报（Brief）与大纲（Outline）：用 `wa-cli brief set <场景> --purpose <本场目的> --json` 写场景简报，用 `wa-cli outline set <场景> <节 id> --reason <本场职责> --json` 补节的理由，按 `wa-cli next <场景> --json` 的 commands 推进阶段（`wa-cli advance <场景> --json`）。
4. 起草：运行 `wa-cli context <场景> --json`，只依据上下文包中的契约、前情摘要、出场人物的状态与知情（谁在此刻知道什么）和揭示边界写作；人物卡与已知条目中写着视角人物（POV）尚未获知的秘密的句子会被替换为「（另有未揭示设定）」（秘密遮蔽（Secret Masking），输出的 masked 列出被遮蔽的条目与句数），不要写出、暗示或自行补全这些信息；本场景契约正是要揭示被遮蔽的内容时（视角人物在本场景才获知），先向作者确认揭示的内容与程度，不要自行补全；用 `wa-cli draft write <场景> --file <文件> --json` 写入。作者改过正文时（`draft.human-edited`），先运行 `wa-cli diff <场景> --json` 看作者的修改，基于作者的版本修改，再用 `wa-cli draft write <场景> --file <文件> --base <diff 输出的 base> --json` 写入。运行 `wa-cli lint <场景> --json` 直到没有 error 级发现项。
5. 写后收口：把本场新增的设定、状态变化、知情变化、伏笔埋设与回收、确认产生的事件写成收口输入 YAML，七节都可省略：
   - `canon`：新增设定条目，每项含 type、text，可选 name、aliases、sources 与 id；
   - `state_changes`：状态变化，每项含 entry（设定集条目 id）与 state，可选 contract（对应契约中 state_changes 的 id）与 id；
   - `knowledge`：知情变化，每项含 entry 与 character（character 条目 id 或 reader），可选 id；
   - `plants`：契约外新埋的伏笔，每项含 text 与 recover_at，可选 id；
   - `recovers`：本场回收的既有伏笔 id 数组；`events`：本场确认产生的事件 id 数组；`waived`：豁免的疑似专有名词数组。
   运行 `wa-cli scene close-out <场景> --file <文件> --json`。契约声明的每个 state_changes、plants、recovers 与 events 都必须在收口中兑现，缺漏时诊断会指出，此时不写入任何文件。正文改动后需要重新收口（收口过期）时加 `--replace`：它把输入中的条目再次写入台账并整体替换收口记录，但不清理也不撤销此前写入的条目，重复提交同一条会在台账中留下重复条目；重收口前先和作者核对哪些条目需要重新提交、作废的旧提议如何处理（如经作者同意用 state reject 或 knowledge reject 否定）。输出的 suspected 列出疑似专有名词（Suspected Proper Noun），source 标明来自大纲的出场人物、视角人物、地点，或来自正文（draft，正文里出现在介绍（叫某某）、反复的对话前导（某某说）或称呼（某师傅、老某）中却没登记进设定集的人名）：真是新设定的补进收口，作者认定不是的用 `wa-cli scene close-out <场景> --waive <名字> --json` 豁免。正文扫描只覆盖这几类明显的人名位置，收口前仍要自查正文中新出现的人物、地点与物品是否已登记。
6. 作者确认台账：收口写入的状态变更提议（State Change Proposal）与知情事件（Knowledge Event）初始为待确认。用 `wa-cli state list <作品> --json` 与 `wa-cli knowledge list <作品> --json` 列出，逐条交给作者核对：作者同意的运行 `wa-cli state confirm <作品> <id> --confirmed-by-author --confirmation <作者原话摘要> --json` 或 `wa-cli knowledge confirm <作品> <id> --confirmed-by-author --confirmation <作者原话摘要> --json`；作者否定的用 `wa-cli state reject <作品> <id> --confirmed-by-author --confirmation <作者原话摘要> --json` 或 `wa-cli knowledge reject <作品> <id> --confirmed-by-author --confirmation <作者原话摘要> --json`；需要补揭示词（Reveal Terms）时用 `wa-cli knowledge set <作品> <id> --terms <揭示词> --confirmed-by-author --confirmation <作者原话摘要> --json`。揭示词既用于连续性审查发现正文提前揭示，也决定上下文包能否在获知之前遮蔽人物卡里写出该秘密的句子：确认秘密类知情事件时，请作者给出人物卡与设定条目中实际写出该秘密的词句作为揭示词；`wa-cli validate --json` 的 knowledge.missing-terms 警告列出还没有任何揭示词的已确认知情事件。收口之外的零星补登用 `wa-cli state propose <作品> --scene <场景> --entry <条目> --state <状态> --json` 与 `wa-cli knowledge propose <作品> --scene <场景> --entry <条目> --character <获知者> --json`。收口记录的提议仍待确认时，验收被 closeout.pending-proposals 阻塞；待确认的条目也不参与状态快照与连续性判断，后续场景会看不到这些变化。
7. 审稿与验收：转到 wa-review 记录审稿，必要时用 wa-continuity 做连续性审查与审查任务包；验收前运行 `wa-cli review show <场景> --json` 看 `gate.blockers`（连续性发现、未收口、收口过期、收口的提议待确认、更早部的卷末检查点等都会阻塞），再按 wa-publish 请作者验收。验收后用 `wa-cli summary set <场景> --text <摘要> --json` 写场景摘要（Summary），供后续场景的上下文包使用。
8. 一部的最后一个场景验收后转到 wa-assemble 处理卷末检查点（Volume Checkpoint）。

## 必须停下来问作者

- 场景契约的取舍（谁出场、承担哪个节拍、揭示到哪里、必须发生什么变化）由作者决定；契约与规划冲突时先问作者。
- 状态变更提议与知情事件只能由作者确认或否定：`--confirmed-by-author` 与 `--confirmation` 只记录作者的原话摘要，不要替作者确认，也不要为了通过关卡编造确认。
- 收口发现正文与契约不符（缺少必发的变化、伏笔或事件）时，问作者是改正文还是改契约，不要自行删改契约来过关。
- 疑似专有名词是否豁免、新设定是否入设定集，由作者决定。
- 场景验收必须由作者确认：没有作者的明确同意，不要运行 accept，也不要编造确认原话。
