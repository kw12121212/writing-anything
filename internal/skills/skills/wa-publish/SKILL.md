---
name: wa-publish
description: 请作者确认验收，推进单元并导出 Markdown 成稿。修订任务都已处理、作者准备发布时使用。
---

# wa-publish：验收（Acceptance）与导出（Export）

## 步骤

1. 运行 `wa-cli review show <单元> --json` 查看审稿（Review）与验收关卡，确认 `gate.ready` 为 true；不为 true 时按 `gate.blockers` 回到 wa-revise。
2. 把审稿结论、修订情况与成稿交给作者确认。作者同意后，用作者的原话作为确认记录（Confirmation Record）运行 `wa-cli accept <单元> --confirmed-by-author --confirmation <作者原话摘要> --json`。
3. 运行 `wa-cli advance <单元> --json` 把单元推进到 done。
4. 运行 `wa-cli export <作品> --format markdown --front-matter --json` 导出；需要别的位置时加 `--output <路径>`。导出只包含已验收的单元。
5. 运行 `wa-cli validate --json` 确认工作区没有问题。

## 必须停下来问作者

- 验收必须由作者确认：没有作者的明确同意，不要运行 accept，也不要编造确认原话。
