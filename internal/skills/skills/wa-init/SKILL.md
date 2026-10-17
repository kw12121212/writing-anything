---
name: wa-init
description: 在项目中初始化 writing-anything 工作区并创建作品。用户想开始写一篇文章、建立写作项目，或者其他 wa-* 技能提示工作区不存在时使用。
---

# wa-init：初始化工作区与作品

writing-anything 帮 Agent 规范地协助作者写作，而不是替作者写作。所有结构化产物只通过 `wa-cli` 修改，每个命令都加 `--json`，按输出中诊断（Diagnostic）的 `next` 执行下一步。

## 步骤

1. 运行 `wa-cli status --json`。没有工作区（Workspace，`workspace.not-found`）时，先问作者要用哪种文体（Genre），技术博客为 `tech-blog`，然后运行 `wa-cli init --genre <文体> --json`。需要严格的作者确认时加 `--gate-level strict`。
2. 问作者作品（Work）的短名（小写字母、数字与连字符），运行 `wa-cli new <文体> <作品> --json`。
3. 请作者提供素材（Source）：亲历的笔记、访谈记录、参考资料。每份素材用 `wa-cli source add --type <author-note|interview|transcript|reference|past-article> --file <文件> --json` 登记；用 `wa-cli source list --json` 查看已登记的素材。
4. 作者或团队有自己的规范包（Pack，文风、术语、禁用词等）时，用 `wa-cli pack install <git 地址或本地路径> --json` 安装（git 来源可加 `--ref <分支、标签或提交>`），再用 `wa-cli config set packs <规范包 id，逗号分隔> --json` 启用；`wa-cli pack list --json` 查看已安装的规范包与锁定状态。安装不会自动启用。
5. 作者提供了过往文章时，先用 `wa-cli source add --type past-article --file <文件> --json` 登记，再运行 `wa-cli pack learn <提议 id> --pack <作者规范包 id> --source <素材 id> --json` 取得文风统计（Style Stats）；阅读样文后，把定性观察用 `--style <文风描述>` 与 `--avoid <要避免的写法>` 补充到同一提议。把每个条目逐项交给作者，作者同意的用 `wa-cli pack confirm <提议 id> --item <条目 id> --confirmation <作者原话摘要> --json` 写入作者的规范包，不同意的用 `wa-cli pack reject <提议 id> --item <条目 id> --json` 记录；之后用 `wa-cli config set packs` 启用该规范包。
6. 运行 `wa-cli next <作品> --json`，按 `commands` 与 `action` 继续，通常转到 wa-brief。
7. 作者想在只支持 MCP 的客户端中使用时，运行 `wa-cli mcp install --client <claude-code|codex|json> --scope <project|user> --json` 注册 MCP 服务（json 另加 `--config <配置文件>`）；重复运行结果相同，不影响客户端已有的其他服务。把输出中 installs 的 message 转告作者：它说明要在哪个项目目录启动客户端，用户级注册换到其他项目时还要重新运行。安装技能（`wa-cli skills install --agent <客户端> --json`）后同样把 launchDir 告诉作者：在这个目录中启动客户端，技能才会被加载。

## 会话协议（可执行模板）

把下面的模板复制进会话的 shell，之后按步骤逐条运行：失败即停，读诊断（Diagnostic）的 next 修复后重试；绝不 `|| true`、管道或输出重定向吞掉失败（shell 层吞错对状态机不可见，只能事后靠 stale 暴露）。

```bash
set -euo pipefail
wa() { local out; out=$(wa-cli "$@") || { printf '%s\n' "$out" >&2; return 1; }; printf '%s\n' "$out"; }
wa next --json   # 示例：每步先看下一步；把 wa 后面换成当前步骤的 wa-cli 命令与参数
```

经 harness 提问工具得到作者的决定后，同步回写决策队列（Decision Queue）保住审计链：先用 `wa-cli decision add --question <问题> --unit <单元> --json` 登记问题，作者答复后用 `wa-cli decide <决策 id> --option <选项> --answer <答复> --confirmation <作者原话摘要> --json` 记录决定；不要只在对话里问完就往下走。

## 必须停下来问作者

- 选择文体、作品短名与关卡强度（Gate Level）。
- 用哪些规范包、从哪里安装、按什么顺序启用：启用会改变生效的规则。
- 文风画像的每个条目是否采纳：只有作者确认的条目才能用 pack confirm 写入，不要代替作者确认。
- 是否把 MCP 服务注册到客户端、注册到项目级还是用户级。
- 素材从哪里来：不要编造素材，也不要把自己写的文字登记为作者笔记。
