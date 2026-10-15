# writing-anything 产品要点

## 是什么

writing-anything 是给 AI 智能体（Agent）用的规范化辅助写作工具。Claude Code、Codex 等 Agent 通过命令行工具 wa-cli、技能（Skill）或 MCP 服务接入，按规范协助作者写作。

定位：帮 Agent 规范地协助人写作，而不是替人写作。作者提供意图和素材，并做最终决定；Agent 按规范组织、起草和打磨。wa-cli 本身不调用大模型，写作和审稿由接入的 Agent 完成，wa-cli 负责流程、记录和检查。

## 面向谁

- 用 AI 写稿的内容创作者：公众号作者、自媒体人。
- 用 Agent 写技术博客或文档的开发者。
- 用 AI 辅助长篇创作的小说作者。

## 重点能力

### 素材出处，防止编造

- 作者先把自己的笔记、访谈、过往文章登记为素材，原文逐字节存档。
- 文章中的亲历、观点、案例和数据要登记为论断，并关联到作者素材；没有关联素材的论断，在文本检查中报告为 error，过不了验收。
- 检查还会找出看起来像亲历（如「我当时」「我们遇到」）或像数据（如百分比、倍数）却没有登记的句子，提示补登记。
- 每个段落都记录出处：是 Agent 写的还是作者改的、依据了哪些素材。

### 规范化写作流程

- 每篇文章按「写作简报 → 大纲 → 草稿 → 审稿 → 导出」逐步推进，前一步没完成，后一步不能开始。
- 需要作者拿主意的地方，Agent 会停下来问：写作简报的内容、大纲的取舍、缺少的素材、标题选择、最终验收。
- 作者可以在本地阅读页上逐段查看正文、出处和检查结果，直接在页面上确认验收和决策。
- 作者可以直接改正文，作者的修改优先，Agent 不能覆盖。

### 规范包与文风

- 规范包（Pack）是纯数据的写作规范：文风要求、术语表、禁用词、评分标准，不执行任何代码。
- 内置默认规范包检查常见 AI 腔套话（如「值得注意的是」「综上所述」「在当今时代」）、禁用词、中文排版和术语一致性。
- 公众号营销文章额外检查广告法极限词（如「最佳」「顶级」「全网第一」），依据《广告法》第九条。
- 可以从作者的过往文章生成文风画像，作者逐项确认后写入自己的规范包。

## 支持的文体

技术博客（tech-blog）、公众号营销文章（wechat-marketing）、短篇小说（short-story）、长篇小说（novel，含系列小说）。导出格式有 Markdown、只含内联样式的公众号 HTML 和纯文本。

## 怎么获取

- 开源地址：https://github.com/kw12121212/writing-anything
- Linux、macOS 安装：`curl -fsSL https://raw.githubusercontent.com/kw12121212/writing-anything/main/install.sh | sh`
- Windows（PowerShell）安装：`irm https://raw.githubusercontent.com/kw12121212/writing-anything/main/install.ps1 | iex`
- 接入 Agent：在项目目录运行 `wa-cli init`，再运行 `wa-cli skills install --agent claude-code`（或 `codex`），以及 `wa-cli mcp install --client claude-code`。

## 行动号召

关注公众号【公众号名】获取后续更新；【加群方式】加入交流群，交流 AI 协助写作的经验。（占位，发布前填写）
