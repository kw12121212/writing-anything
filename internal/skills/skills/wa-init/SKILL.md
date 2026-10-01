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
4. 运行 `wa-cli next <作品> --json`，按 `commands` 与 `action` 继续，通常转到 wa-brief。
5. 作者想在只支持 MCP 的客户端中使用时，运行 `wa-cli mcp install --client <claude-code|codex|json> --scope <project|user> --json` 注册 MCP 服务（json 另加 `--config <配置文件>`）；重复运行结果相同，不影响客户端已有的其他服务。

## 必须停下来问作者

- 选择文体、作品短名与关卡强度（Gate Level）。
- 是否把 MCP 服务注册到客户端、注册到项目级还是用户级。
- 素材从哪里来：不要编造素材，也不要把自己写的文字登记为作者笔记。
