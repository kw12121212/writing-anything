---
name: wa-brief
description: 和作者一起填写写作简报（受众、目的、前提、主题、视角、核心观点、字数、禁区、写作法），并通过提问关卡。作品处于 brief 阶段，或 wa-cli next 提示写作简报缺项时使用。
---

# wa-brief：写作简报

写作简报（Brief）记录作者的意图。核心观点（Key Message）必须出自作者的素材（Source），或经作者在决策中确认。

## 步骤

1. 运行 `wa-cli brief show <单元> --json`，读取 `questions`：这些是必须向作者提问的问题。
2. 逐个向作者提问，把回答用 `wa-cli brief set` 写入：
   - `--audience`、`--purpose`、`--length <min>-<max>`、`--avoid`、`--sources`；
   - 虚构文体（如 short-story）的前提与视角：`--premise <前提> --premise-sources <素材 id>`、`--theme <主题>`、`--pov-person <first|second|third> --pov-character <视角人物>`；
   - 核心观点（短篇中承载关键转折）：`--message <id> --message-text <内容> --message-sources <素材 id>`；
   - 没有素材的观点：用决策（Decision）请作者确认。给出候选时运行 `wa-cli decision add --question <问题> --option <候选甲> --option <候选乙> --unit <单元> --blocking --json`，作者选定后运行 `wa-cli decide <决策 id> --option <a|b|…> --confirmation <作者原话> --json`；没有候选时 decision add 不加 --option，作者答复后运行 `wa-cli decide <决策 id> --answer <作者的答复> --confirmation <作者原话> --json`。再用 `--message-decision <决策 id>` 关联。
3. 写作法（Method Template）：没有设置 method 时，只有作品的根单元使用文体的默认写作法，子单元（如场景）不用写作法。运行 `wa-cli method list --json` 把可用的写作法告诉作者；作者要换时用 `wa-cli brief set <单元> --method <id>`，不用写作法时为 `none`。
4. `questions` 为空后运行 `wa-cli advance <单元> --json`。

## 会话协议（可执行模板）

把下面的模板复制进会话的 shell，之后按步骤逐条运行：失败即停，读诊断（Diagnostic）的 next 修复后重试；绝不 `|| true`、管道或输出重定向吞掉失败（shell 层吞错对状态机不可见，只能事后靠 stale 暴露）。

```bash
set -euo pipefail
wa() { local out; out=$(wa-cli "$@") || { printf '%s\n' "$out" >&2; return 1; }; printf '%s\n' "$out"; }
wa next --json   # 示例：每步先看下一步；把 wa 后面换成当前步骤的 wa-cli 命令与参数
```

经 harness 提问工具得到作者的决定后，同步回写决策队列（Decision Queue）保住审计链：先用 `wa-cli decision add --question <问题> --unit <单元> --json` 登记问题，作者答复后用 `wa-cli decide <决策 id> --option <选项> --answer <答复> --confirmation <作者原话摘要> --json` 记录决定；不要只在对话里问完就往下走。

## 必须停下来问作者

- 受众、目的、前提、视角、核心观点与禁区都只能来自作者；不要替作者决定。
- 观点或前提没有素材支撑时，必须登记决策等作者确认（`--premise-decision <决策 id>`），不能自己补出处。
