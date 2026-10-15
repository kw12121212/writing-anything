---
name: wa-revise
description: 处理审稿发现项与作者反馈形成的修订任务，修改正文后重新审稿。存在修订任务、作者留下批注或反馈时使用。
---

# wa-revise：修订

## 步骤

1. 运行 `wa-cli feedback list <单元> --json` 与 `wa-cli review show <单元> --json`，列出修订任务（Revision Task）：未处理的审稿发现项与作者反馈（Feedback，包括正文中的 `<!-- @me: … -->` 批注（Comment））。等待作者的新反馈时运行 `wa-cli feedback watch --max 1 --json`，收到一条后返回。作者想边读边提意见时运行 `wa-cli preview <单元> --serve --json`，把首行输出中的 preview.url 交给作者在浏览器中打开（只在本机可访问）。阅读页（Reading View）是在作者确认期间持续运行的本地服务：用客户端的后台任务运行该命令（不要放在会结束或被回收的前台命令里），作者确认完成前不要停止它；读取首行输出的 preview.url 后，先确认该地址可以访问（如请求它得到 200）再交给作者；作者说打不开时，重新运行该命令并交出新的地址（令牌会变化）。作者在阅读页中提交的意见会保存为作者反馈，同样出现在 feedback watch 中。
2. 逐项和作者确认怎么改，修改正文后用 `wa-cli draft write <单元> --file <文件> --json` 写入（作者改过正文时先 `wa-cli diff <单元> --json`）。
3. 处理完的反馈用 `wa-cli feedback resolve <单元> <反馈 id> --json`；审稿发现项用 `wa-cli review resolve <单元> <发现项 id> --json`，作者决定不改的用 `--status waived --note <作者的理由>`。
4. 正文改动后审稿过期：回到 wa-review 重新记录审稿；用 `wa-cli lint <单元> --json` 确认没有 error 级发现项。单元已经完成（done）时，改动正文会使验收（Acceptance）失效，导出不再包含它：先运行 `wa-cli reopen <单元> --json` 把它退回需要验收的阶段，再重新审稿并按 wa-publish 请作者重新验收；不要手工修改 work.yaml 的 stage。
5. 一轮修订结束后，运行 `wa-cli pack prefer <提议 id> --pack <作者规范包 id> --json`，从作者的修改与反馈中归纳作者偏好（Author Preference）；把每个条目连同 evidence 交给作者，作者同意的用 `wa-cli pack confirm <提议 id> --item <条目 id> --confirmation <作者原话摘要> --json` 写入规范包，不同意的用 `wa-cli pack reject <提议 id> --item <条目 id> --json` 记下，之后不会再提出。

## 必须停下来问作者

- 每个修订任务怎么处理、是否豁免，都由作者决定；豁免的理由写作者的原话。
- 归纳出的作者偏好是否采纳由作者逐项决定，不要代替作者确认。
