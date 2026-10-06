# AGENTS.md

## 项目

writing-anything 是面向 AI 智能体（Agent）的规范化辅助写作工具。第三方 Agent 通过命令行工具（CLI）`wa-cli`、技能（Skill）与 MCP 服务（MCP Server）接入，以作者（Author）为意图、素材（Source）与最终决策的来源，按规范包（Pack）与文体（Genre）完成写作生命周期（Lifecycle）：写作简报（Brief）→ 大纲（Outline）→ 草稿（Draft）→ 审稿（Review）→ 修订（Revision）→ 导出（Export）。

定位：帮 Agent 规范地协助人写作，而不是替人写作。

## 架构

- **内核（Kernel）**：通用单元树（Unit Tree）、按单元推进的生命周期、带出处（Provenance）的事实库（Fact Store）、规则引擎（Rule Engine）、有界上下文包（Context Pack）、决策队列（Decision Queue）、人工编辑保护（Human Edit Protection）、导出。
- **文体**：首批 `tech-blog`（技术博客）、`wechat-marketing`（公众号营销文章）；后续 `short-story`（短篇小说）、`novel`（长篇小说）、系列小说。
- **规范包**：第三方或作者提供的纯数据规范（文风、术语、禁用词、评分标准），不执行代码。

产品工作区（Workspace）：`.writing-anything/`。结构化产物为 JSON Schema 约束的 YAML，正文为 Markdown。

## 技术栈与命令

- Go 1.27，编译为跨平台单文件 `wa-cli` 二进制（入口 `cmd/wa-cli/`，实现位于 `internal/`，MCP 服务以 `wa-cli mcp` 子命令提供）。
- JSON Schema、内置文体、规范包与 Skill 通过 `embed` 打包进二进制。
- 测试夹具（Test Fixture）放在各包的 `testdata/` 下。
- 完整回归测试：`go vet ./... && go build ./... && go test -coverprofile=coverage.out ./... && go run ./internal/tools/covercheck -min 80 -profile coverage.out`（覆盖率阈值 80%，范围 `internal/**`，不含 `internal/tools/**`）。

## 约定

- `wa-cli` 是唯一真相来源；MCP 服务与 Skill 只做薄封装。
- 所有命令支持 `--json`，诊断（Diagnostic）包含错误码、产物路径与下一步可执行命令。
- 结构化产物只通过 `wa-cli` 变更。Agent 通过 `wa-cli draft write` 写入正文；作者可以直接编辑正文，作者的编辑优先于 Agent。
- 亲历内容、观点、数据必须链接到作者提供的素材；验收需记录作者确认。
- 规范包只能是数据，不得执行第三方代码。
- `wa-cli` 不调用大语言模型（LLM）；写作与审稿由接入的 Agent 完成。

## 术语

所有面向人的文字（文档、CLI 输出、诊断、Skill、路线图与规格）遵循 [docs/GLOSSARY.md](docs/GLOSSARY.md)：

- 专有名词在每份文档或产物中首次出现时写作「中文（English）」，此后使用术语表的常用写法。
- 命令名、参数名、字段名、文件名等标识符保持英文。
- 引入新术语时，在同一变更中加入术语表。

## 开发流程

本仓库开发由 strict-spec-driven 管理（`.strict-spec-driven/`），使用 `strict-spec-*` / `strict-roadmap-*` 技能。产品运行时不得依赖或调用 strict-spec-driven。

## 分支

- `main`：主分支，只接收从 `dev` 合并的稳定版本，不直接在上面开发。
- `dev`：主力开发分支，日常提交与变更都在 `dev` 上进行；需要隔离的工作从 `dev` 拉出功能分支，完成后合并回 `dev`。
- 正式发布（Release）通过项目技能（Project Skill）`.agents/skills/release` 执行：完整回归通过后把 `dev` 合并到 `main`，版本号（Version）经 `v*` 标签（Tag）更新，最后提交推送。本项目不使用持续集成与持续交付（CI/CD），仓库中不放置任何工作流文件。
- `CLAUDE.md` 是指向本文件的软链接，只修改 `AGENTS.md`。
