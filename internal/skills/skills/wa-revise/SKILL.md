---
name: wa-revise
description: 处理审稿发现项与作者反馈形成的修订任务，修改正文后重新审稿。存在修订任务、作者留下批注或反馈时使用。
---

# wa-revise：修订

## 步骤

1. 运行 `wa-cli feedback list <单元> --json` 与 `wa-cli review show <单元> --json`，列出修订任务（Revision Task）：未处理的审稿发现项与作者反馈（Feedback，包括正文中的 `<!-- @me: … -->` 批注（Comment））。等待作者的新反馈时运行 `wa-cli feedback watch --max 1 --json`，收到一条后返回。作者想边读边提意见时，先运行 `wa-cli status --json` 查看 config.reviewSurface（审阅位置（Review Surface），即作者在哪里审阅阅读页）。未设置 review_surface 时 wa-cli 按运行环境自动给出：在 DSH 中运行即为 dsh，不要自己判断。为 dsh 时：不要启动 `wa-cli preview --serve`，也不要交出 127.0.0.1 地址，而是请作者打开 DSH 插件（DSH Plugin）面板，在标为「待审阅」的作品或单元上点「审阅」，在面板里完成选中文字提交意见；作者说完成后，用 `wa-cli feedback list <单元> --json` 与 `wa-cli review show <单元> --json` 复查结果再继续。作者说面板里没有「待审阅」入口（DSH 未安装 dsh-writing-viewer 插件）时，改按下面 browser 的做法。为 browser 时：运行 `wa-cli preview <单元> --serve --json`，把首行输出中的 preview.url 交给作者在浏览器中打开（只在本机可访问）。阅读页（Reading View）是在作者确认期间持续运行的本地服务：用客户端的后台任务运行该命令（不要放在会结束或被回收的前台命令里），作者确认完成前不要停止它；读取首行输出的 preview.url 后，先确认该地址可以访问（如请求它得到 200）再交给作者；作者说打不开时，重新运行该命令并交出新的地址（令牌会变化）。作者在阅读页中提交的意见会保存为作者反馈，同样出现在 feedback watch 中。
2. 逐项和作者确认怎么改，修改正文后用 `wa-cli draft write <单元> --file <文件> --json` 写入（作者改过正文时先 `wa-cli diff <单元> --json`）。
3. 处理完的反馈用 `wa-cli feedback resolve <单元> <反馈 id> --json`；审稿发现项用 `wa-cli review resolve <单元> <发现项 id> --json`，作者决定不改的用 `--status waived --note <作者的理由>`。
4. 正文改动后审稿过期：回到 wa-review 重新记录审稿；用 `wa-cli lint <单元> --json` 确认没有 error 级发现项。单元已经完成（done）时，改动正文会使验收（Acceptance）失效，导出不再包含它：先运行 `wa-cli reopen <单元> --json` 把它退回需要验收的阶段，再重新审稿并按 wa-publish 请作者重新验收；不要手工修改 work.yaml 的 stage。
5. 一轮修订结束后，运行 `wa-cli pack prefer <提议 id> --pack <作者规范包 id> --json`，从作者的修改与反馈中归纳作者偏好（Author Preference）；把每个条目连同 evidence 交给作者，作者同意的用 `wa-cli pack confirm <提议 id> --item <条目 id> --confirmation <作者原话摘要> --json` 写入规范包，不同意的用 `wa-cli pack reject <提议 id> --item <条目 id> --json` 记下，之后不会再提出。

## 会话协议（可执行模板）

把下面的模板复制进会话的 shell，之后按步骤逐条运行：失败即停，读诊断（Diagnostic）的 next 修复后重试；绝不 `|| true`、管道或输出重定向吞掉失败（shell 层吞错对状态机不可见，只能事后靠 stale 暴露）。

```bash
set -euo pipefail
wa() { local out; out=$(wa-cli "$@") || { printf '%s\n' "$out" >&2; return 1; }; printf '%s\n' "$out"; }
wa next --json   # 示例：每步先看下一步；把 wa 后面换成当前步骤的 wa-cli 命令与参数
```

经 harness 提问工具得到作者的决定后，同步回写决策队列（Decision Queue）保住审计链：先用 `wa-cli decision add --question <问题> --unit <单元> --json` 登记问题，作者答复后用 `wa-cli decide <决策 id> --option <选项> --answer <答复> --confirmation <作者原话摘要> --json` 记录决定；不要只在对话里问完就往下走。

## 必须停下来问作者

- 每个修订任务怎么处理、是否豁免，都由作者决定；豁免的理由写作者的原话。
- 归纳出的作者偏好是否采纳由作者逐项决定，不要代替作者确认。
