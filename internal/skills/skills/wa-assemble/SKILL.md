---
name: wa-assemble
description: 长篇（novel）的部末与成书收尾：生成并请作者确认卷末检查点，核对时间线，组稿与导出整书，并在会话结束前写交接记录。一部的最后一个场景验收后、全书场景都已验收准备成书时，或会话即将结束时使用。
---

# wa-assemble：卷末检查点、组稿与交接

## 步骤

1. 卷末检查点（Volume Checkpoint）：一部（Part，即卷）的最后一个场景经验收（Acceptance）后，若本部其他场景都已完成，wa-cli 会自动在该部目录下生成 checkpoint.yaml（只写汇总，不代作者确认）；本部还有未完成的场景时验收照常成功，只给出 warning 级 volume.scenes-open，等其余场景完成后运行 `wa-cli volume checkpoint <部> --json` 生成，需要重新生成时同样运行它。把检查点汇总的本部新增设定、已确认的状态变化、伏笔变化、未处理决策与抽查条目（sampled）交给作者对照原文核对。
2. 作者核对无误后，优先请作者在本地阅读页（Reading View）上确认：运行 `wa-cli preview <作品> --serve --json`，把首行输出中的 preview.url 交给作者。作者在对话中同意时，运行 `wa-cli volume confirm <部> --confirmed-by-author --confirmation <作者原话摘要> --json`。关卡强度（Gate Level）为 strict 时，更早的部缺少检查点或检查点未经作者确认，下一部的场景不能验收（volume.missing、volume.unconfirmed；检查点文件损坏为 volume.invalid，重新生成即可）。
3. 部末收尾：用 `wa-cli summary set <单元> --text <摘要> --json` 为章与部写分层摘要（Layered Summary）；运行 `wa-cli handoff write --file <文件> --json` 写交接记录（见第 7 步）。
4. 时间线（Timeline）：成书前运行 `wa-cli timeline <作品> --json`（系列用系列 id）检查场景全序、after 成环与声明矛盾，把矛盾交给作者处理。
5. 组稿（Assembly）：运行 `wa-cli assemble <作品> --json`（需要别的位置或纯文本时加 `--output <路径>` 与 `--format text`）。被拒绝时按阻塞项处理：未完成的场景回到 wa-scene；连续性阻塞与 open 的复查标记转到 wa-continuity；strict 关卡下的待作者复核（Pending Author Review，见 `wa-cli review show <作品> --json` 的 pendingAuthorReviews）把阅读页地址交给作者，由作者在「待作者复核」区批量复核。书名是作品根单元大纲的首节：根大纲已有情节节拍时用 `wa-cli outline set <作品> <节 id> --title <书名> --reason <说明> --before <首节 id> --json` 放在首位；部、章标题用 `wa-cli outline set <单元> <节 id> --title <标题> --reason <说明> --json` 写在对应单元的大纲中。
6. 导出（Export）：组稿成功后，需要发布用的成稿时按 wa-publish 运行 `wa-cli export <作品> --format markdown --json`，导出只含已验收的单元并附 AI 参与说明。
7. 交接记录（Handoff Note）：会话结束前，把完成了什么、未完成的事项、作者的新指示与需要注意的风险写成 YAML（done 与 todo 必填，instructions 与 risks 可选，格式见 `wa-cli help handoff write --json`），运行 `wa-cli handoff write --file <文件> --json` 追加。新会话开始时，`wa-cli next --json` 每个步骤的 handoff 字段给出最近一条交接的 id、todo 与作者指示（instructions），不加 --json 时 next 在文本输出顶部显示同样内容；运行 `wa-cli handoff show --json` 读取完整的最近一条（含完成事项与风险），需要更早的记录时加 `--last <条数>`。

## 必须停下来问作者

- 卷末检查点只能由作者确认：没有作者的明确同意，不要运行 volume confirm，也不要编造确认原话。
- 阅读页上的确认与复核只能由作者本人操作，不要自己向阅读页提交；确认渠道（Confirmation Channel）是可审计的流程约束，不是安全隔离。
- 时间线矛盾、组稿阻塞中需要取舍的部分（改正文、改设定或豁免）由作者决定。
- 交接记录中的作者指示只写作者说过的话，不要替作者补充指示。
