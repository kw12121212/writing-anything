---
name: wa-publish
description: 请作者确认验收，推进单元并导出 Markdown 或公众号 HTML 成稿。修订任务都已处理、作者准备发布时使用。
---

# wa-publish：验收（Acceptance）与导出（Export）

## 步骤

1. 运行 `wa-cli review show <单元> --json` 查看审稿（Review）与验收关卡，确认 `gate.ready` 为 true；不为 true 时按 `gate.blockers` 回到 wa-revise。长篇（novel）场景的连续性发现转到 wa-continuity，未收口、收口过期或收口的状态与知情提议待确认（closeout.*）转到 wa-scene，更早部的卷末检查点（volume.*）转到 wa-assemble。
2. 把审稿结论、修订情况与成稿交给作者确认。优先请作者自己在本地阅读页（Reading View）上确认：运行 `wa-cli preview <作品> --serve --json`，把首行输出中的 preview.url 交给作者在浏览器中打开，作者在「待作者确认」区填写原话摘要并确认，记录的确认渠道（Confirmation Channel）为 preview。阅读页是在作者确认期间持续运行的本地服务：用客户端的后台任务运行该命令（不要放在会结束或被回收的前台命令里），作者确认完成前不要停止它；读取首行输出的 preview.url 后，先确认该地址可以访问（如请求它得到 200）再交给作者；作者说打不开时，重新运行该命令并交出新的地址（令牌会变化）。作者在对话中同意时，也可以用作者的原话作为确认记录（Confirmation Record）运行 `wa-cli accept <单元> --confirmed-by-author --confirmation <作者原话摘要> --json`，此时渠道记为 cli。
3. 运行 `wa-cli advance <单元> --json` 把单元推进到 done。
   作品属于系列（Series，见 `wa-cli series show <系列> --json`）时，验收后为作品撰写摘要（Summary），用 `wa-cli summary set <作品 id> --text <摘要> --json` 为作品的根单元写入；字数范围见诊断，后面的作品写作时会在上下文包中看到它。摘要只写已经验收的内容，正文之后被修改时摘要会过期，需要重新撰写。
4. 运行 `wa-cli export <作品> --format markdown --front-matter --json` 导出；需要别的位置时加 `--output <路径>`。导出只包含已验收的单元；已完成（done）的单元在验收后又改过正文时，export.unaccepted 的 next 会给出 `wa-cli reopen <单元> --json`，退回后回到第 1 步重新审稿与验收。公众号文章用 `wa-cli export <作品> --format wechat-html --json` 导出只含内联样式（Inline Style）的 HTML，把输出的 imageList（配图清单）交给作者，按清单在公众号编辑器中替换配图占位。正文有 Agent 起草的段落时，导出会按出处自动附上 AI 参与说明（AI-generated Content Label），输出的 export.disclosure 给出说明文字与 AI 参与比例；放置位置用 `wa-cli config set ai_disclosure <footer|metadata|both> --json` 设置，不要自己删改说明。
5. 关卡强度（Gate Level）为 strict 时，经命令行渠道（cli）写入的验收、钦定设定修改与卷末检查点确认会列为待作者复核（Pending Author Review）：`wa-cli review show <单元> --json` 与 `wa-cli next --json` 的 pendingAuthorReviews 列出它们，存在时 `wa-cli assemble` 以 assemble.unreviewed 拒绝组稿。把 pendingAuthorReviews 中的 command（`wa-cli preview <作品> --serve --json`）给出的阅读页地址交给作者，由作者在「待作者复核」区批量复核后再组稿。
6. 运行 `wa-cli validate --json` 确认工作区没有问题。

## 必须停下来问作者

- 验收必须由作者确认：没有作者的明确同意，不要运行 accept，也不要编造确认原话。
- 阅读页上的确认与复核只能由作者本人操作：不要自己向阅读页提交确认或复核。确认渠道是可审计的流程约束，不是安全隔离——Agent 能读到阅读页令牌，但代填会以渠道记录下来，供作者与审计者核对。
