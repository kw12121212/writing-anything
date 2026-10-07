---
name: wa-continuity
description: 长篇（novel）与系列的连续性把关：运行确定性连续性审查，按维度生成审查任务包并以全新子 Agent 会话并行执行、写回结论，修改早期内容后做影响传播并处理复查标记。场景验收前、一章或一部写完后、或修改了已验收内容时使用。
---

# wa-continuity：连续性审查与影响传播

连续性审查（Continuity Review）分两层：`wa-cli continuity` 做不依赖模型的确定性检查，发现即阻塞验收；审查任务包（Review Task Packet）把需要判断的语义检查切成有界的小包，由接入的 Agent 执行、wa-cli 只汇总。修改早期内容后，影响传播（Ripple Analysis）找出需要复查的后续场景。

## 步骤

1. 确定性审查：运行 `wa-cli continuity <单元> --json`（单元（Unit）为场景时只查该场景，章、部或作品查其子树全部场景）。作品属于系列（Series，见 `wa-cli series show <系列> --json`）时，再对系列运行 `wa-cli continuity <系列> --json` 做跨作品检查（跨作品时间线、系列级设定、前作已死亡或失踪的人物仍出场等）；跨作品的终态人物检查只采信前作已确认的状态，先用 wa-scene 请作者确认前作的状态变更提议。时间先后有疑问时运行 `wa-cli timeline <作品或系列> --json` 查看时间线（Timeline）推导出的场景全序与矛盾。
2. 处理发现项（Finding）：按 message 修改正文（经 wa-scene 重新写入并收口）、补登设定或修正场景契约。作者认定某条发现是有意为之时，验收（Acceptance）被阻塞后会生成豁免（Waiver）决策（Decision），运行 `wa-cli decisions --json` 找到它，作者选定后运行 `wa-cli decide <决策 id> --option <选项 id> --confirmation <作者原话摘要> --json`。
3. 语义审查任务包：运行 `wa-cli review plan <单元或作品> --json` 生成全部维度的任务包，或用 `--dimension <character|thread|causality|pov-reveal|style>` 只生成一个维度。每个任务包只含一段正文切片、相关台账切片与检查清单，不超过上下文预算。
4. 并行执行：为每个任务包开一个全新的子 Agent（Subagent）会话，只把该任务包的内容交给它，不带主会话的判断或其他任务包的结论；按维度可以并行派发。子 Agent 对清单的每一项给出 pass、fail 或 na 的结论与说明，写成 YAML（verdicts 数组，每项含 item、verdict 与可选 note，格式见 `wa-cli help review submit --json`），由主会话运行 `wa-cli review submit <作品>/<任务包 id> --file <文件> --json` 写回。
5. 汇总：运行 `wa-cli review report <作品> --json` 查看各维度的进度与 fail 结论；把 fail 结论交给作者，由作者决定改正文还是保留，改正文时转到 wa-revise 或 wa-scene。
6. 影响传播：作者要修改已验收的场景、设定条目、事件或伏笔时，先运行 `wa-cli impact <目标> --json` 预览受影响的后续场景与引用依据，交给作者确认修改范围；修改落地后运行 `wa-cli impact mark <目标> --json` 把范围落盘为复查标记（Recheck Mark），并为每个受影响场景生成 causality 维度的任务包。
7. 复查标记：运行 `wa-cli impact list <作品> --json` 列出 open 的标记。对每个受影响场景，先执行 impact mark 生成的 causality 任务包并用 review submit 写回，再按 wa-review 基于当前正文重新记录审稿；wa-cli 只核对审稿记录（基于当前正文且没有 fail 结论），不核对任务包，所以不要在没有重新审查的情况下省略确认解除。重新审查通过后运行 `wa-cli impact clear <场景> <目标> --json` 解除；作者核对后认定无影响的，运行 `wa-cli impact clear <场景> <目标> --confirmed-by-author --confirmation <作者原话摘要> --json`。open 的复查标记会阻塞组稿（Assembly）。

## 必须停下来问作者

- 连续性发现是改正文、改设定还是豁免，由作者决定；豁免只能经作者在决策中选定，不要替作者 decide。
- 任务包的 fail 结论只是建议，是否修改由作者决定；不要把 fail 改写成 pass 来通过汇总。
- 修改已验收内容之前，先把影响预览交给作者确认范围。
- 以「无影响」解除复查标记必须有作者的明确同意，`--confirmation` 写作者的原话摘要，不能由 Agent 代填。
