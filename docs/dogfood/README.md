# 真实接入验证（Dogfooding）

本目录记录里程碑（Milestone）「真实使用验证与发布（Real-use Validation & Release）」中的真实接入验证：由作者（Author）在本机操作真实的 Agent 客户端（Claude Code 与 Codex），经技能（Skill）与 MCP 服务（MCP Server）接入 `wa-cli`，按 客户端 × 文体（Genre：技术博客 `tech-blog`、公众号营销文章 `wechat-marketing`）的组合运行并走到导出（Export）。原计划四个组合各运行一次；Codex × 公众号营销文章因 Codex 额度不足由作者取消，记为未运行，实际运行三次。

- `runs.yaml`：真实接入记录（Dogfood Record），每个 客户端 × 文体 组合一条的结构化记录与问题清单，是唯一的记录来源。
- `sources/`：公众号营销文章运行使用的作者素材（Source）。
- 校验：`go run ./internal/tools/dogfood` 检查 `runs.yaml` 是否齐备（字段说明见下文「记录格式」）。

## 运行前准备

1. 在本仓库的 `dev` 分支构建 `wa-cli`，放进 `PATH`，记下版本与提交：

   ```sh
   go build -o "$HOME/.local/bin/wa-cli" ./cmd/wa-cli
   wa-cli version
   ```

   `wa-cli version` 的完整输出写进记录的 `wa_cli_version`。各次运行之间如果重新构建过（例如合入了缺陷修复），每次运行记录各自的版本。

2. 记下客户端版本，写进记录的 `client_version`：

   ```sh
   claude --version
   codex --version
   ```

3. 每次运行在**仓库之外**新建一个独立的项目目录，目录名与运行 id 相同（仓库根目录不得创建 `.writing-anything/`）：

   ```sh
   mkdir -p "$HOME/wa-dogfood/claude-code-tech-blog" && cd "$HOME/wa-dogfood/claude-code-tech-blog"
   wa-cli init
   ```

4. 在项目目录中安装技能并注册 MCP 服务：

   | 客户端 | 安装技能 | 注册 MCP 服务 |
   |---|---|---|
   | Claude Code | `wa-cli skills install --agent claude-code`（写入 `.claude/skills/`） | `wa-cli mcp install --client claude-code`（项目级） |
   | Codex | `wa-cli skills install --agent codex`（写入 `.agents/skills/`） | `wa-cli mcp install --client codex --scope user`（Codex 只支持用户级：注册写入用户配置并指向当前项目目录，换到下一个项目目录时重新运行即更新；全部运行结束后可用 `codex mcp remove writing-anything` 移除） |

5. 准备素材：
   - 技术博客：复制本仓库 `internal/cli/testdata/e2e/` 下的 `go-docs.md`、`interview.md`、`author-note.md` 到项目目录。
   - 公众号营销文章：复制本目录 `sources/` 下的 `product-points.md`（产品要点，登记为 `reference` 类型）与 `author-note.md`（作者笔记，登记为 `author-note` 类型）到项目目录。文中的亲历、观点与数据只能关联 `author-note.md`：`claim-sources` 规则默认只认 author-note、interview、transcript 类型的素材，所以项目数据也写在作者笔记中。

## 运行

1. 先进入项目目录本身，再启动**全新的**客户端会话，如 `cd ~/wa-dogfood/claude-code-wechat-marketing && claude`（不要在开发本仓库的会话中运行；客户端只能通过安装的技能与 MCP 服务了解 `wa-cli`）。在上级目录启动时，项目级的技能不会加载；Claude Code 的项目级 MCP 配置（`.mcp.json`）同样不会加载，Codex 的 MCP 服务是用户级注册，不受启动目录影响。
2. 用一句话说明意图即可，例如：

   > 用 writing-anything 帮我写一篇技术博客，素材是当前目录下的 go-docs.md、interview.md 和 author-note.md。按规范推进到导出 Markdown。

   公众号营销文章把文体换成 `wechat-marketing`、素材换成 `sources/` 中的文件，导出格式为 `wechat-html`。
3. Agent 停下来问作者时如实回答；验收（Acceptance）、决策（Decision）与作者复核一律在阅读页（Reading View）上确认：让 Agent 运行 `wa-cli preview <unit> --serve`，在浏览器中打开输出的地址操作。
4. 走到导出为止：技术博客 `wa-cli export <work> --format markdown`，公众号营销文章 `wa-cli export <work> --format wechat-html`。
5. 运行中随手记下：Agent 实际运行的命令（客户端的工具调用记录或 MCP tool 调用，按顺序）、卡住的位置、看不懂的诊断（Diagnostic）、需要作者绕过的地方。

## 记录格式

每次运行在 `runs.yaml` 的 `runs` 中追加一项：

| 字段 | 说明 |
|---|---|
| `id` | `<client>-<genre>`，如 `codex-wechat-marketing` |
| `client` | `claude-code` 或 `codex` |
| `client_version` | 客户端版本输出，如 `2.1.280 (Claude Code)` |
| `wa_cli_version` | `wa-cli version` 的输出，含提交 |
| `genre` | `tech-blog` 或 `wechat-marketing` |
| `channels` | 本次实际用到的接入方式：`skill`、`mcp` 中的一项或两项 |
| `date` | 开始运行的日期，`YYYY-MM-DD`（跨天续接的运行仍取第一天） |
| `commands` | 按顺序的 `wa-cli` 命令序列；经 MCP 调用的 tool 写成对应的命令形式，并在 `notes` 中说明；必须包含导出命令 |
| `export` | `format`（技术博客为 `markdown`，公众号营销文章为 `wechat-html`）与 `path`（导出文件相对项目目录的路径） |
| `notes` | 卡点与观察，自由文字 |
| `issues` | 问题清单；没有问题时为 `[]` 或省略 |

没有运行的组合仍要有一条记录，只含 `id`、`client`、`genre` 与 `skipped_reason`（未运行的原因），不得填写其他字段（写成空值视同未填）。两个客户端与两种文体都必须各至少有一次已运行的记录。

每个问题含 `id`（全部运行中唯一，如 `cc-tb-1`）、`summary`，以及以下二者**恰好其一**：

- `fix_change`：修复它的缺陷变更名（如 `fix-export-path`），该变更须已归档（`.strict-spec-driven/changes/archive/` 下有 `<日期>-<变更名>` 目录）；
- `wont_fix_reason`：不修的理由；属于新需求的，注明转入后续里程碑。

## 问题分诊

1. 全部运行结束后，逐个处理 `issues`：需要修复的，用 `strict-spec-bugfix` 单独开缺陷变更（Change），修复并归档后把变更名写进 `fix_change`；不修的写明 `wont_fix_reason`。
2. 本变更活动期间，项目同时最多两个活动变更，缺陷变更一个一个处理。
3. 全部处理完后运行 `go run ./internal/tools/dogfood`，无问题时把汇总写进下文。

## 汇总

| 组合 | 结果 | wa-cli 提交 | 问题 |
|---|---|---|---|
| Claude Code × 技术博客 | 2026-10-14 导出 Markdown；10-15 两次续接时导出 wechat-html | 5ca203b（含未提交文档） | cc-tb-1、cc-tb-2、cc-tb-3 |
| Claude Code × 公众号营销文章 | 2026-10-15 导出 wechat-html | 5ca203b（含未提交文档） | cc-wm-1、cc-wm-2 |
| Codex × 技术博客 | 2026-10-15 导出 Markdown | 2d88c77 | cx-tb-1、cx-tb-2、cx-tb-3 |
| Codex × 公众号营销文章 | 未运行：Codex 额度不足，作者取消 | — | — |

问题分诊结果（2026-10-15，作者确认）：

| 问题 | 处理 |
|---|---|
| cc-tb-1 在项目目录外启动客户端，安装输出没有提示 | `fix-cli-install-hints`：skills install 输出 launchDir 与启动提示，mcp install 说明启动目录与用户级注册的切换 |
| cc-tb-2 审稿前拿不到完整评分项 | `fix-review-rubric-discovery`：review show 输出 rubric |
| cc-tb-3 done 作品改正文后无法重新验收 | `fix-reopen-stale-acceptance`：新增 wa-cli reopen |
| cc-wm-1 有技能时 MCP 闲置 | 不修：符合预期，MCP 面向不能运行 shell 的 Agent；wa-auto 技能已说明 |
| cc-wm-2 标题类评分项被迫引用段落 | `fix-review-rubric-discovery`：paragraphs 可为空 |
| cx-tb-1 升级后重装技能被报为改过 | `fix-skills-install-upgrade`：按历史版本哈希识别旧版内置技能 |
| cx-tb-2 阅读页服务提前退出 | `fix-preview-serve-guidance`：技能说明后台运行、确认可访问与打不开时重新运行 |
| cx-tb-3 decision show 没有指向 decisions | `fix-cli-install-hints`：未知子命令提示相关命令 |

与原计划的偏差：Claude Code × 技术博客的 6 个审稿决策由作者选择在对话中接受建议、经命令行记录（channel 为 cli），没有全部在阅读页确认，验收仍在阅读页确认；记录在该运行的 notes 中，不作为缺陷。

`go run ./internal/tools/dogfood` 通过：3 次运行记录齐备，1 个组合写明未运行原因（问题 8 个：已修复 7，不修 1）。
