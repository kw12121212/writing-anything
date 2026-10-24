---
name: wa-publish
description: 请作者确认验收（验收前询问是否配图），推进单元并导出 Markdown、公众号 HTML 或可连图带文复制的可粘贴页成稿与配图。修订任务都已处理、作者准备发布时使用。
---

# wa-publish：验收（Acceptance）与导出（Export）

## 步骤

1. 运行 `wa-cli review show <单元> --json` 查看审稿（Review）与验收关卡，确认 `gate.ready` 为 true；不为 true 时按 `gate.blockers` 回到 wa-revise。长篇（novel）场景的连续性发现转到 wa-continuity，未收口、收口过期或收口的状态与知情提议待确认（closeout.*）转到 wa-scene，更早部的卷末检查点（volume.*）转到 wa-assemble。
2. 配图（Illustration）是可选环节，在请作者验收之前处理：作品文体为 tech-blog 或 wechat-marketing、且 `wa-cli illustration list <单元> --json` 与 `wa-cli illustration list <作品> --json`（封面只登记在作品根单元）都还没有配图时，询问作者是否需要正文配图与封面图（Cover Image）；其他文体只在作者提出时进入配图环节。作者需要时转到 wa-illustrate，完成后回到这里；作者不需要时跳过，按下面的步骤继续。已有配图时请作者在审看成稿时一并确认图文：验收会锁定配图与封面，验收后再改动配图需要作者重新确认。
3. 把审稿结论、修订情况与成稿交给作者确认。优先请作者自己在阅读页（Reading View）上确认：先运行 `wa-cli status --json` 查看 config.reviewSurface（审阅位置（Review Surface），即作者在哪里审阅阅读页）。未设置 review_surface 时 wa-cli 按运行环境自动给出：在 DSH 中运行即为 dsh、在 ZCode 中运行即为 zcode，不要自己判断。为 dsh 时：不要启动 `wa-cli preview --serve`，也不要交出 127.0.0.1 地址，而是请作者打开 DSH 插件（DSH Plugin）面板，在标为「待审阅」的作品或单元上点「审阅」，在面板里完成确认（记录的确认渠道（Confirmation Channel）同样为 preview）；作者说完成后，用 `wa-cli review show <单元> --json` 或 `wa-cli next --json`（其 pendingConfirmations 列出仍待作者确认的事项）复查结果再继续。作者说面板里没有「待审阅」入口（DSH 未安装 dsh-writing-viewer 插件）时，改按下面 browser 的做法。为 zcode 时：不要启动 `wa-cli preview --serve`，也不要交出 127.0.0.1 地址，而是请作者打开 ZCode 插件（zcode-writing-viewer）面板，在标为「待审阅」的作品或单元上点「审阅」，在面板里完成确认（记录的确认渠道（Confirmation Channel）同样为 preview）；作者说完成后，用 `wa-cli review show <单元> --json` 或 `wa-cli next --json` 复查结果再继续。作者说 ZCode 面板里没有「待审阅」入口（ZCode 未安装 zcode-writing-viewer 插件）时，改按下面 browser 的做法。为 browser 时：运行 `wa-cli preview <作品> --serve --json`，把首行输出中的 preview.url 交给作者在浏览器中打开，作者在「待作者确认」区填写原话摘要并确认，记录的确认渠道（Confirmation Channel）为 preview。阅读页是在作者确认期间持续运行的本地服务：用客户端的后台任务运行该命令（不要放在会结束或被回收的前台命令里），作者确认完成前不要停止它；读取首行输出的 preview.url 后，先确认该地址可以访问（如请求它得到 200）再交给作者；作者说打不开时，重新运行该命令并交出新的地址（令牌会变化）。作者在对话中同意时，也可以用作者的原话作为确认记录（Confirmation Record）运行 `wa-cli accept <单元> --confirmed-by-author --confirmation <作者原话摘要> --json`，此时渠道记为 cli。
4. 运行 `wa-cli advance <单元> --json` 把单元推进到 done。
   作品属于系列（Series，见 `wa-cli series show <系列> --json`）时，验收后为作品撰写摘要（Summary），用 `wa-cli summary set <作品 id> --text <摘要> --json` 为作品的根单元写入；字数范围见诊断，后面的作品写作时会在上下文包中看到它。摘要只写已经验收的内容，正文之后被修改时摘要会过期，需要重新撰写。
5. 运行 `wa-cli export <作品> --format markdown --front-matter --json` 导出；需要别的位置时加 `--output <路径>`。导出只包含已验收的单元；已完成（done）的单元在验收后又改过正文时，export.unaccepted 的 next 会给出 `wa-cli reopen <单元> --json`，退回后回到第 1 步重新审稿与验收。公众号文章用 `wa-cli export <作品> --format wechat-html --json` 导出只含内联样式（Inline Style）的 HTML，把输出的 imageList（配图清单）交给作者，按清单在公众号编辑器中替换配图占位。有登记的配图或封面时，导出会把图片复制到导出文件旁的资产目录（`<导出名>.assets/`），输出的 export.assets 逐张列出图片路径：把资产目录与 export.assets 交给作者，公众号文章按配图清单上传封面与各张配图（清单中标注「AI 生成」的是 Agent 渲染的图）。作品有正文配图、作者要把文章连图带文粘贴到公众号编辑器或飞书、语雀、知乎等在线文档时，运行 `wa-cli export <作品> --format paste-html --json` 导出可粘贴页（Paste-ready Page），把 export.path 交给作者：在浏览器打开后点「复制图文」（或全选正文复制），粘贴即可连同配图一起带过去；作者在 DSH 插件面板中也可以在作品根单元详情或审阅视图头部点「复制图文」，复制内容与可粘贴页相同。告诉作者：可粘贴页只含已验收的单元，不含 AI 参与说明与封面图——需要声明 AI 参与时在发布平台自行声明（可引用 export.disclosure 的说明文字与比例），封面在平台单独上传；公众号图文编辑器的粘贴限制（2026-10-23 实测）：单张超过约 1 MiB 或一次超过 7 张图时，整次粘贴的图片全部丢失，文字保留。wa-cli 已为此处理：超过 1 MiB 的配图在可粘贴正文中自动缩小并重新编码为 JPEG（资产目录仍为原图）；内嵌图超过 7 张时可粘贴页与面板改为「复制第 n 段」按钮，请作者逐段复制、逐段粘贴到公众号，「复制全文」用于飞书等不限张数的目标。目标编辑器不接受内嵌图片、或正文中出现「【配图 n】」占位（超出内嵌上限、像素过多或压不下来的图、外链或行内图片）时，按同一次导出写出的配图清单（export.imageList）与资产目录上传对应配图。正文有 Agent 起草的段落时，导出会按出处自动附上 AI 参与说明（AI-generated Content Label），输出的 export.disclosure 给出说明文字与 AI 参与比例；有 Agent 渲染的配图时，说明另含配图说明（如「文中 2 张配图由 AI 生成。」）。放置位置用 `wa-cli config set ai_disclosure <footer|metadata|both> --json` 设置，不要自己删改说明（含配图说明）。
6. 关卡强度（Gate Level）为 strict 时，经命令行渠道（cli）写入的验收、钦定设定修改与卷末检查点确认会列为待作者复核（Pending Author Review）：`wa-cli review show <单元> --json` 与 `wa-cli next --json` 的 pendingAuthorReviews 列出它们，存在时 `wa-cli assemble` 以 assemble.unreviewed 拒绝组稿。把 pendingAuthorReviews 中的 command（`wa-cli preview <作品> --serve --json`）给出的阅读页地址交给作者，由作者在「待作者复核」区批量复核后再组稿；config.reviewSurface 为 dsh 时不运行该命令，改为请作者在 DSH 插件面板的「待审阅」入口复核，完成后用 `wa-cli review show <单元> --json` 复查；config.reviewSurface 为 zcode 时同样不运行该命令，改为请作者在 ZCode 插件面板的「待审阅」入口复核，完成后用 `wa-cli review show <单元> --json` 复查；作者说面板里没有「待审阅」入口时改按 browser 的做法（运行该命令交出 preview.url）。
7. 运行 `wa-cli validate --json` 确认工作区没有问题。

## 会话协议（可执行模板）

把下面的模板复制进会话的 shell，之后按步骤逐条运行：失败即停，读诊断（Diagnostic）的 next 修复后重试；绝不 `|| true`、管道或输出重定向吞掉失败（shell 层吞错对状态机不可见，只能事后靠 stale 暴露）。

```bash
set -euo pipefail
wa() { local out; out=$(wa-cli "$@") || { printf '%s\n' "$out" >&2; return 1; }; printf '%s\n' "$out"; }
wa next --json   # 示例：每步先看下一步；把 wa 后面换成当前步骤的 wa-cli 命令与参数
```

经 harness 提问工具得到作者的决定后，同步回写决策队列（Decision Queue）保住审计链：先用 `wa-cli decision add --question <问题> --unit <单元> --json` 登记问题，作者答复后用 `wa-cli decide <决策 id> --option <选项> --answer <答复> --confirmation <作者原话摘要> --json` 记录决定；不要只在对话里问完就往下走。

## 必须停下来问作者

- 验收必须由作者确认：没有作者的明确同意，不要运行 accept，也不要编造确认原话。
- 阅读页上的确认与复核只能由作者本人操作：不要自己向阅读页提交确认或复核。确认渠道是可审计的流程约束，不是安全隔离——Agent 能读到阅读页令牌，但代填会以渠道记录下来，供作者与审计者核对。
