---
name: wa-outline
description: 按写作简报与写作法拟定大纲，确保每个核心观点都被覆盖。作品处于 outline 阶段，或 wa-cli next 提示覆盖缺口时使用。
---

# wa-outline：大纲（Outline）

## 步骤

1. 运行 `wa-cli context <单元> --json` 读取有界上下文包（Context Pack）。运行 `wa-cli outline show <单元> --json`：选用了写作法时，`gaps` 列出写作法要求而大纲还缺少的章节或节拍，message 中有写作法 id，用 `wa-cli method show <写作法 id> --json` 查看各章节的说明。
2. 逐节用 `wa-cli outline set <单元> <节 id> --title <标题> --role <写作法章节> --covers <核心观点 id> --json` 写入；不覆盖核心观点的节写明 `--reason`；需要素材的节用 `--uses <素材 id>`。
   - 虚构文体（如 short-story）：故事根单元的大纲节是情节节拍（Story Beat）；场景单元的大纲节用场景契约（Scene Contract）声明 `--characters <出场人物>`（逗号分隔）、`--beats <节拍 id>`（指向根单元大纲的节）与 `--reveal <揭示边界>`。
3. 再运行 `wa-cli outline show <单元> --json`，按 `gaps` 补齐覆盖缺口（Coverage Gap）：未覆盖的核心观点、缺少理由的节、缺少或顺序颠倒的写作法章节。删除多余的节用 `wa-cli outline remove <单元> <节 id> --json`。
4. 把大纲交给作者看，作者同意后运行 `wa-cli advance <单元> --json`。

## 必须停下来问作者

- 大纲的取舍与顺序需要作者同意；作者想调整写作法时回到 wa-brief。
