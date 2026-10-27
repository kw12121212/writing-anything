# writing-anything

面向 AI Agent（智能体）的规范化辅助写作工具。接入的 Agent 通过命令行工具（CLI）`wa-cli`、内置技能（Skill）与 MCP 服务（MCP Server）协作写作，以作者（Author）为意图、素材（Source）与最终决策的来源，按文体（Genre）与规范包（Pack）走完写作生命周期（Lifecycle）：写作简报（Brief）→ 大纲（Outline）→ 草稿（Draft）→ 审稿（Review）→ 修订（Revision）→ 导出（Export）。

**帮 Agent 规范地协助人写作，而不是替人写作。** `wa-cli` 不调用大语言模型（LLM）：写作与审稿由接入的 Agent 完成，工具负责结构化产物（Artifact）、素材出处（Provenance）、规则检查与作者确认。

## 组成

| 组件 | 说明 |
|---|---|
| `wa-cli` | Go 单文件二进制内核（入口 `cmd/wa-cli/`），支持 Linux、macOS、Windows 的 amd64 与 arm64 |
| 内置技能 | 13 个 `wa-*` 技能：`wa-init`、`wa-plan`、`wa-brief`、`wa-outline`、`wa-draft`、`wa-review`、`wa-revise`、`wa-illustrate`、`wa-assemble`、`wa-publish`、`wa-auto`、`wa-scene`、`wa-continuity`，随二进制 embed |
| MCP 服务 | `wa-cli mcp --dir <项目目录>` 以 stdio 提供 tools、resources 与 prompts |
| `dsh-writing-viewer` | DeepSeek Harness（DSH）Web GUI 的 DSH 插件（DSH Plugin）：写作工作区面板，只读总览、产物语义卡片与源码高亮、单元内审阅、复制图文 |
| `zcode-writing-viewer` | ZCode 的 ZCode 插件（ZCode Plugin）：写作工作区面板，功能与 DSH 插件对齐，并捆绑全部内置 `wa-*` 技能 |

工作区（Workspace）是项目目录下的 `.writing-anything/`：结构化产物为 JSON Schema 约束的 YAML，正文为 Markdown。

## 安装

### wa-cli

Linux 与 macOS：

```sh
curl -fsSL https://raw.githubusercontent.com/kw12121212/writing-anything/main/install.sh | sh
```

Windows（PowerShell）：

```powershell
irm https://raw.githubusercontent.com/kw12121212/writing-anything/main/install.ps1 | iex
```

安装脚本（Install Script）下载对应平台的发布产物（Release Asset），校验 `SHA256SUMS` 一致后才安装到用户级目录，不需要 root。安装指定版本用 `WA_VERSION`，自定义目录用 `WA_INSTALL_DIR`，详见 [docs/INSTALL.md](docs/INSTALL.md)。

### DeepSeek Harness（DSH）插件

从本仓库的 GitHub Release 安装插件包（Plugin Package）：

```sh
dsh plugin --profile web add https://github.com/kw12121212/writing-anything/releases/latest/download/dsh-writing-viewer-0.9.0.tgz
```

详见 [plugins/dsh-writing-viewer/README.md](plugins/dsh-writing-viewer/README.md)。

### ZCode 插件

从 Release 安装 `zcode-writing-viewer-0.9.0.tgz`，或在 ZCode 中以本地市场（`plugins/marketplace.json`）安装；详见 [plugins/zcode-writing-viewer/README.md](plugins/zcode-writing-viewer/README.md)。

## 快速开始

```sh
wa-cli init                      # 在项目目录创建工作区 .writing-anything/
wa-cli new tech-blog my-post     # 按文体创建作品（Work），作品是单元树（Unit Tree）的根单元
wa-cli next                      # 当前阶段（Stage）、阻塞项（Blocker）与下一步命令
wa-cli status                    # 工作区汇总：每个单元的阶段、阻塞项与校验结果
wa-cli validate                  # 按 JSON Schema 校验整个工作区
```

内置文体：`minimal`、`tech-blog`、`wechat-marketing`、`short-story`、`novel`。所有命令支持 `--json`，诊断（Diagnostic）包含错误码、产物路径与下一步可执行命令。

## 文档

- [docs/INSTALL.md](docs/INSTALL.md)：安装渠道、安装脚本与 Windows 验证记录
- [docs/GLOSSARY.md](docs/GLOSSARY.md)：术语表（Term Glossary），全部面向人的文字遵循
- [AGENTS.md](AGENTS.md)：项目结构、开发约定与分支

## 开发

Go 1.27；两个面板插件用 pnpm 构建（`pnpm --dir plugins/<插件> run build`）。完整回归：

```sh
go vet ./... && go build ./... && go test -coverprofile=coverage.out ./... && go run ./internal/tools/covercheck -min 80 -profile coverage.out
```

本仓库开发由 strict-spec-driven 管理（`.strict-spec-driven/`），正式发布（Release）走项目技能 `.agents/skills/release`：`main` 只接收从 `dev` 合并的稳定版本，版本号经 `v*` 标签（Tag）更新，不使用持续集成与持续交付（CI/CD）。

## 许可

[Apache-2.0](LICENSE)
