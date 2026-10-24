# zcode-writing-viewer（写作工作区面板插件）

ZCode 插件（ZCode Plugin）`zcode-writing-viewer` 是 writing-anything 仓库的 ZCode 工作区面板插件，功能与 DSH 插件（DSH Plugin）`dsh-writing-viewer` 对齐：只读定位一个包含 `.writing-anything/` 工作区（Workspace）的项目目录，在 ZCode 的「写作工作区」面板中以总览仪表盘（Overview Dashboard）呈现作品（Work）卡片、工作区级计数、校验诊断与单元（Unit）树导航；单元产物（Artifact）提供语义卡片视图（Semantic Card View）与源码高亮视图（Source Highlight View），草稿（Draft）正文提供渲染阅读视图；素材（Source）清单、决策队列（Decision Queue）与作者复核记录清单只读呈现；作者在面板的审阅视图（Review View）中完成单元审阅——待审阅发现、按出处着色的段落正文、配图（Illustration）、待作者确认与待作者复核、选中文字提交反馈，全部经 MCP 服务（MCP Server）交给 wa-cli 阅读页（Reading View）写入，确认渠道（Confirmation Channel）记为 `preview`。插件同时随包捆绑全部 13 个内置 wa-* 技能（Skill）。

- 默认严格只读：server 侧的全部文件系统操作都是读取（对 `.writing-anything` 的 `stat`、目录列举与文件读取，均不跟随符号链接）；不提供任何直接写入工作区的能力。
- 一个例外要如实说明：快照（Workspace Snapshot）经子进程调用 wa-cli 只读命令（`status`/`validate`/`next`/`timeline`）取结构状态，而 wa-cli 的审计日志（Audit Log）契约规定每次调用（含只读命令）都向 `.writing-anything/audit.log` 追加一行运维痕迹——该追加出自 wa-cli 而非插件代码；插件在每次快照请求内有界调用这些命令（status、validate、next 各一次，每个长篇（novel）作品 timeline 一次），不做轮询式反复调用。
- 一个由作者显式触发的工作区写入面：单元审阅以原生视图呈现 wa-cli 阅读页的数据，作者在其中提交的反馈、确认与复核经白名单转发交给 wa-cli preview 子进程写入工作区；插件代码自身不写任何工作区文件。
- 一个可选的写入面：默认关闭的「内置技能与 CLI」设置能力（userConfig `enableSetup`）在作者显式点击启用后，从本项目 GitHub Releases 下载 wa-cli 发布产物（Release Asset）、按 SHA256SUMS 校验后原子安装到配置目录——绝不写入 `.writing-anything/`；技能不动态安装（见下文「内置技能捆绑」）。
- 不属于产品运行时：wa-cli（Go 内核）、内置技能源与 MCP 服务不依赖本包；本包也不进入 Go 模块与覆盖率范围。

## 结构

```
.zcode-plugin/plugin.json  插件清单（Plugin Manifest）：MCP server（node dist/server.js，cwd 为 ZCODE_PROJECT_DIR）、
                           ui.surfaces 注册「写作工作区」面板（ui://zcode-writing-viewer/panel.html，availability session）、
                           skills 指向捆绑技能目录、userConfig 声明 workspaceRoot/waCliPath/enableSetup/waVersion/cliInstallDir/waBaseUrl
src/server.ts              MCP server 入口：低层 JSON-RPC 注册工具与 ui:// 资源（官方 @modelcontextprotocol/sdk，stdio；
                           工具清单与结果携带 _meta.ui：surface 打开面板、model/app 可见性）
src/panel.ts               面板入口（官方 @modelcontextprotocol/ext-apps 的 App）：connect 前注册全部 handler，经
                           callServerTool/readServerResource 与本插件 server 通信；vanilla 渲染，createElement 纯文本子节点
src/root.ts                工作区定位的纯逻辑（候选顺序、面板路径校验：绝对路径、拒 NUL/超长/UNC 与设备路径）
src/snapshot.ts            快照聚合：类型定义与注入式聚合入口（每次请求重读，无缓存）
src/wacli.ts               wa-cli 只读命令桥：--json 输出映射与按区域容错降级
src/artifacts.ts           产物清单与全文读取（按 JSON Schema 约束的路径，跳过符号链接）
src/overview.ts            总览视图模型的纯函数：作品卡片、工作区计数、单元树与单元详情
src/treekeys.ts            单元树键盘导航的纯逻辑：可见节点过滤与方向键决策
src/artifactview.ts        产物语义卡片视图（brief/outline/claims 定制布局，其余通用卡片化）
src/yamlhighlight.ts       YAML 源码高亮与顶层键折叠（零依赖纯模块）
src/markdownview.ts        Markdown 渲染阅读视图与源码高亮（零依赖纯模块，HTML 全部转义）
src/queueview.ts           决策队列与作者复核记录的纯函数视图模型
src/reviewmodel.ts         审阅视图的纯视图模型：page.json 解析（含配图）、配图布局与固定 ui:// 取图地址、提交请求体、单段选区规则、待审阅索引
src/reviewsession.ts       审阅会话（Review Session）：注入式 spawn 管理唯一的 wa-cli preview --serve 子进程
                           （首行信封取端口与令牌、令牌只留 server 内存、空闲与替换时终止）
src/reviewproxy.ts         阅读页服务白名单转发：page.json、配图（只 unit 与 id）、feedback/confirm/author-review 三个提交接口
src/reviewview.ts          审阅控件的纯视图模型：启动/进行中/失败/已结束状态、放大覆盖层判定、双语文案
src/setup.ts               「内置技能与 CLI」设置核心（纯逻辑）：配置解析、发布产物下载与 SHA256SUMS 校验、原子安装与溯源标记、
                           技能来源对照表（捆绑 vs 外部存在性比对）、PATH 解析
src/setupfs.ts             setup 的真实文件系统适配器——包内唯一持有写入型 node:fs 调用的模块（与 setup.ts 同为静态只读测试声明的写入面）
src/setupview.ts           面板设置区的纯视图模型：状态机 idle/busy/enabled/error、双语文案
scripts/normalize-outputs.mjs  产物组装：tsdown 输出归一并把面板脚本内联进 dist/panel.html（自包含 ui:// 资源）
scripts/stage-skills.mjs       构建时从 internal/skills/skills 原样捆绑 13 个 wa-* 技能并核对名单
scripts/server-smoke.mjs       stdio JSON-RPC 驱动构建产物的冒烟检查，证据记入 docs/evidence/server-smoke.txt
testdata/                  测试夹具：真实命令生成的多作品工作区 + 捕获的 --json 输出（与 dsh-writing-viewer 同源）
skills/                    捆绑技能：13 个内置 wa-* 技能（构建时复制、逐字一致）+ 本插件的 writing-workspace 技能
dist/server.js             构建产物：MCP server（自包含打包）
dist/panel.html            构建产物：面板文档（脚本内联，经 ui:// 资源提供）
```

## server 侧行为（MCP 工具与资源）

工具面（`tools/list` 全部声明 `_meta.ui.visibility`；模型与面板共用的工具为 `["model","app"]`，仅面板使用的报告工具为 `["app"]`）：

- `workspace_status`：轻量定位状态（root、来源、探测轨迹、空态指引）。
- `workspace_snapshot`：工作区快照，聚合结构与 dsh 版一致（配置、作品与单元树、产物全文、素材、决策（含待决）与待复核摘要、validate 诊断、降级原因数组）。接受可选 `root`（面板路径切换，绝对路径；非法或无工作区返回空态与 `rootError`，不回退默认定位）。
- `open_workspace`：打开「写作工作区」面板（可带 `unit` 聚焦某单元或 `view` 打开 sources/decisions/reviews 清单视图）；结果携带 `_meta.ui.surface`。
- `show_in_viewer`：把面板定位到某单元或清单视图。
- `get_view` / `report_view` / `release_view`：面板报告作者正在看什么（项目目录、选中单元、当前视图），模型经 `get_view` 读取——作者说「这个」「选中的」时先查它。
- `review_start` / `review_status` / `review_end`：审阅会话管理。`review_start` 载荷 `{unit, root?}`（unit 经纯函数校验：非空、不以 `-` 开头、不含控制字符；root 校验同快照）。启动先结束既有会话，再以 `--dir <root>` 调用一次 `wa-cli preview <unit> --serve --json`，10 秒内读到首行信封即成功；响应只含会话摘要（unit、root、开始时间），令牌与端口永不出 server 进程。
- `review_page`：当前会话的阅读页数据（page.json 白名单转发）。
- `review_submit`：`{action: feedback|confirm|author-review, body}` 白名单转发；载荷超 64 KiB 拒绝；上游不可达返回可读提示，不回显原始错误。
- `setup_status` / `setup_enable`：仅在 userConfig `enableSetup` 开启时注册（见下文专节）。

资源面：`ui://zcode-writing-viewer/panel.html`（面板文档，`text/html;profile=mcp-app`）与 `ui://zcode-writing-viewer/review-illustration/<unit>/<id>` 模板（已登记配图与封面图的字节，base64 回传；无会话时读取失败可读）。

项目目录解析规则（`workspace_status` 与快照共用）：

- 配置了 `workspaceRoot`（userConfig）时为排他固定：只探测该目录，目录下没有 `.writing-anything/` 就进入空态，不再回退到其他来源；
- 未配置时按顺序探测：server 启动工作目录（清单声明为 `ZCODE_PROJECT_DIR`，即 ZCode 打开的项目目录）→ `process.cwd()`，第一个探测到 `.writing-anything/` 的候选胜出；
- 面板的 `root` 参数（显式输入的绝对路径）只作用于单次工具调用，优先于配置固定；非法路径（相对、NUL、超长、`//` 或 `\\` 开头的网络与设备路径）或其下没有工作区时返回空态与 `rootError`，不静默改显示其他项目。

插件配置（userConfig，经 `ZCODE_WV_*` 环境变量注入 server）：`workspaceRoot`、`waCliPath`（覆盖 wa-cli 可执行文件路径，默认解析 PATH）、`enableSetup`（默认 false）、`waVersion`（默认 latest）、`cliInstallDir`（默认 ~/.local/bin）、`waBaseUrl`（默认本项目 GitHub Releases）。

## 单元审阅（Review，默认开启）

与 dsh-writing-viewer 的审阅能力对齐，入口从 HTTP 代理路由换为 MCP 工具与 `ui://` 资源（ZCode 面板没有到插件进程的同源 HTTP 通道；工具调用本身就在宿主信任通道内）：

1. **发现待审阅。** 快照从 `wa-cli next --json` 读取待作者确认事项（`pendingConfirmations`）与待作者复核项（`pendingAuthorReviews`）。有事项的作品卡片显示「待审阅 · N」，单元树中关联单元带「● N」标记；点它们即选中该单元（作品卡片为作品根单元）并直接开始审阅。next 区域降级时不显示标记，降级原因照常列出。
2. **启动会话。** 单元详情的「审阅」按钮（或上述标记）调用 `review_start`；同一 server 同一时间至多一个会话，启动另一会话先终止既有子进程。
3. **只代理 JSON 接口。** `review_page` 对应阅读页服务的 `GET /page.json`；配图经 `ui://…/review-illustration/<unit>/<id>` 资源（只接受 unit 与 id）对应 `GET /illustration`；`review_submit` 的 action 只接受 feedback、confirm、author-review 三个提交接口。令牌由 server 注入上游请求，不接受调用方提供的任何令牌；响应不出现阅读页令牌或 127.0.0.1 地址；请求体超过 64 KiB 不转发。
4. **原生审阅视图。** 视图按单元树先序逐段显示正文与段落序号，按出处（作者、Agent、Agent 起草作者修改）标色，段落下列出文本检查与审稿发现项；配图显示在引用它的段落之后（地址由面板以固定 `ui://` 资源构造，经 `readServerResource` 读取并以 data URL 渲染，不采信正文或 page.json 中的地址），封面图在作品根单元开头标「封面」，未引用的正文配图在单元末尾标「未引用」，wa-cli 判为不可用的配图只显示说明；另列最近失败的命令、「待作者确认」区（说明、决策选项与推荐、原话摘要输入、确认与可拒绝事项的拒绝）与 strict 关卡下的「待作者复核」区（默认全选、「复核所选」）。某一段内选中文字即成为反馈引用，也可针对整个单元；选区跨段落时不提交并提示。每次提交成功后重新拉取 page.json；wa-cli 拒绝时显示诊断并保留已填内容。来自工作区的文字一律作为纯文本显示。头部提供「放大」按钮：同一视图铺满面板区域的覆盖层，Esc 或「还原」退出，已填内容保留、会话不结束。
5. **结束。** 会话在作者点「结束审阅」、30 分钟无请求、启动另一会话、子进程退出或 server 进程退出（ZCode 关闭面板/停止插件）时结束并终止子进程。面板在拉取数据、窗口重新获得焦点或页面重新可见时查询会话状态，发现已结束或被替换即显示「审阅已结束」与「重新审阅」入口。结束后面板自动刷新快照。

**让 Agent 指引作者到 ZCode 审阅（无需配置）。** ZCode 在模型 shell 注入环境变量 `ZCODE_APP_VERSION`；工作区没有设置 `review_surface` 时，wa-cli 据此判断 Agent 运行在 ZCode 中，`wa-cli status --json` 的 `config.reviewSurface` 即为 `zcode`（`reviewSurfaceSource` 为 `env`；DSH 环境注入的 `DSH_WEB_URL` 判断优先）。内置技能 wa-revise、wa-publish、wa-assemble 与 wa-auto 读到 `zcode` 就不再启动 `wa-cli preview --serve` 或交出 `127.0.0.1` 链接，而是请作者打开本面板、在「待审阅」入口完成审阅，作者说完成后再以 `wa-cli review show` 或 `wa-cli next` 复查；作者说面板里没有「待审阅」入口（ZCode 未安装本插件）时改给浏览器阅读页。需要固定某一种时用 `wa-cli config set review_surface browser|dsh|zcode --json` 显式设置，设为空字符串即恢复自动判断。

## 内置技能捆绑与「内置技能与 CLI」设置（Setup，默认关闭）

技能捆绑：本插件随包分发全部 13 个内置 wa-* 技能——构建时从仓库 `internal/skills/skills/` 原样复制进 `skills/`（构建脚本核对名单完整、内容逐字一致），不写任何用户级或项目级技能目录。捆绑技能与外部同名技能（`~/.agents/skills`、`~/.claude/skills`、项目 `.agents/skills`、`.claude/skills`）的解析优先由 ZCode 决定，插件不宣称「外部优先」；设置区的技能来源对照表按标准技能目录存在性比对，如实显示每个技能是捆绑还是存在外部副本。需要把技能装进用户目录时用 `wa-cli skills install`（ZCode 可读取其安装目录）。

设置开关 `enableSetup`（userConfig，默认 `false`）之后是插件的全部「安装类」能力。未开启时不注册 setup 工具、不发起任何网络请求，行为与本节不存在时完全一致。开启后面板出现「内置技能与 CLI」设置区，其中唯一动作是「启用内置技能与 CLI」按钮（同一时间至多一个进行中的动作）：

1. **确保内置 CLI**：配置目录（`cliInstallDir`，默认 `~/.local/bin`）没有 wa-cli 时，从本项目 GitHub Releases 下载对应平台发布产物（linux/darwin/windows × amd64/arm64），按同目录 `SHA256SUMS` 记录校验 SHA-256 后原子安装（同目录临时文件 + rename、可执行权限），并在同目录写溯源标记 `wa-cli.zcode-writing-viewer.json`（managedBy、sha256、installedAt）；校验或下载失败可读报错且不留部分文件。已配置 `waCliPath` 时直接使用该二进制、不下载。`waVersion` 为具体版本号且与现存二进制不符时重新下载；为默认 `latest` 时现存二进制视为就绪、不重复下载。下载只由这次显式点击触发。
2. **状态如实回答「内置还是外置」**：设置区显示当前解析到的 wa-cli 路径（waCliPath → PATH 顺序 → 安装目录后备）、其版本、以及按溯源标记的分类（标记存在且 SHA-256 与磁盘二进制一致 → 插件安装；其余含 waCliPath 指定路径 → 如实显示非插件安装）。插件进程不修改宿主环境（不做 PATH 追加——ZCode 插件进程无法回写宿主 env）。
3. **全程不触碰工作区**：下载、安装与状态查询不创建、修改或删除 `.writing-anything/` 内的任何文件（含 audit.log，setup 不运行任何 wa-cli 命令于工作区目录内）。

## 面板行为

面板经官方 MCP Apps 协议（`@modelcontextprotocol/ext-apps` 的 `App`）连接宿主：主题与语言取宿主下发的上下文并随变更切换（样式只用主题变量并带回退值），数据全部经 `callServerTool` 拉取本插件 server 的工具，配图经 `readServerResource` 读取；面板除本插件 server 外不发起任何网络请求，不使用 `window.zcode` 兼容桥。

- **工具栏**：当前项目目录与来源、刷新按钮；路径输入框与「切换」「恢复默认」。刷新或切换进行中旧数据保留并显示「读取中…」提示；只有最新一次请求的响应会落地。
- **总览仪表盘**：工作区级计数——素材数、待决决策数、待复核数、validate 诊断数；对应区域降级时显示「—」而不是 0。「素材」「待决决策」「待复核」磁贴可点开对应清单视图。
- **作品卡片**：作品 id、文体、单元数、当前阶段、进度、去重后的阻塞项数量与前两条摘要；有待审阅事项时显示徽标。
- **单元树与单元详情**：按 `wa-cli status` 的 parent 关系缩进展示，汇总单元带 done/total 进度标记、有阻塞项的单元带计数；选中单元后详情区显示类型、阶段、进度、阻塞项、下一步命令与产物清单。
- **产物视图**：YAML 产物默认语义卡片视图，一键切换源码高亮视图（滚动位置保持；大 YAML 按顶层键折叠；解析失败降级源码视图并提示）；draft 正文提供渲染阅读视图与源码高亮切换；claims 语义卡片中的素材引用可点击跳转到素材条目。
- **清单视图**：素材清单（source.yaml 语义卡片 + 原文 Markdown）、决策队列（id、status、question、options 与推荐标记、rationale、阻塞标记、关联单元）、作者复核记录（kind、work/ref、渠道、确认与复核时间）；文件缺失显示空态、解析失败显示错误提示。
- **空态与错误态**：定位不到工作区显示 `wa-cli init` 指引（含探测轨迹与 rootError 原因）；没有作品显示 `wa-cli new <genre> <id>` 指引；取数失败显示原因与重试。
- **键盘导航**：焦点在单元树上时 ↑/↓ 在可见单元间移动选中（详情区同步更新），→ 展开或进入第一个子单元，← 收起或回到父单元；产物「打开」按钮、视图切换控件与全部交互控件可经 Tab 与 Enter/空格激活。
- **窄宽度**：面板容器低于 640px 时单元树与详情上下堆叠。

## 开发、构建与测试

需要 Node ≥ 22.13 与 pnpm。所有命令在包目录内运行：

```sh
pnpm install            # 安装开发依赖
pnpm run build          # 类型检查（tsc --noEmit）+ 构建（tsdown）+ 面板文档组装（normalize-outputs）+ 技能捆绑（stage-skills）
pnpm test               # vitest 单元测试（定位、路径校验、快照聚合与降级、CLI 桥、产物视图、高亮、键盘、队列、审阅会话与转发、setup 全链路、只读约束静态扫描、技能捆绑同步、覆盖层 DOM 实测）
pnpm run check          # 一次执行类型检查、构建与全部测试（统一检查入口）
pnpm run smoke          # 构建产物冒烟：stdio JSON-RPC 驱动 dist/server.js，证据记入 docs/evidence/server-smoke.txt
```

交互页只在 ZCode Desktop 的本地工作区可用（MCP Apps 交互页的宿主限制）；面板实测需在 ZCode 中安装本插件后打开「写作工作区」面板。

## 安装

开发模式（本地市场，推荐先用这个验证）：

1. 仓库根已有 `plugins/marketplace.json`（本地开发市场）。在 ZCode 中打开 **Plugin Marketplace → Add → Add Plugin Marketplace**，粘贴本仓库的 `plugins` 目录（绝对路径）并添加。
2. 在市场列表中找到 **Writing Workspace (writing-anything)**，安装。
3. 安装后在会话中打开右侧「写作工作区」面板；技能清单应出现全部 wa-* 技能与 writing-workspace。

正式渠道：从本仓库的 GitHub Release 安装插件包（Plugin Package）tarball——它与同版本的 wa-cli 发布产物、dsh-writing-viewer 插件包一同发布：

```
zcode-writing-viewer-0.1.0.tgz
```

以 Release tarball 安装/升级时，用该版本的实际文件名（`latest/download` 直链为 `https://github.com/kw12121212/writing-anything/releases/latest/download/zcode-writing-viewer-<版本>.tgz`）。升级前先移除旧版本再安装新版本。

固定展示某个项目目录或覆盖 wa-cli 路径时，在 ZCode 的插件设置（userConfig）里配置 `workspaceRoot` / `waCliPath`；远程或镜像下载用 `waBaseUrl`。

### 与 wa-cli 版本的兼容说明

- 快照的结构状态来自 wa-cli 只读命令的 `--json` 输出，每次完整快照的有界命令集合是 `status`、`validate`、`next` 各一次、每个长篇（novel）作品 `timeline` 一次；产物与素材全文按 JSON Schema 约束的路径直接读文件，不依赖命令输出。
- 0.x 的 wa-cli `--json` 字段仍可能演进。映射层（`src/wacli.ts`）按区域独立降级：二进制不可用、执行失败、超时或输出缺少所需字段时，该区域在面板上标注降级原因（总览计数显示「—」而不是 0），文件可直接解析的产物全文照常返回，整体不失败。
- `wa-cli` 不在 `PATH` 上时可用 `waCliPath` 配置指向二进制，或开启 `enableSetup` 后在设置区一键安装；找不到可执行文件时插件按降级路径工作（产物全文仍可查看）。
- 审阅位置的自动判断依赖 `ZCODE_APP_VERSION`（ZCode 在模型 shell 注入的环境变量）：它不是 ZCode 对外承诺的公开契约，未来更名时该判断失效、行为回退 browser，可用 `wa-cli config set review_surface zcode` 显式固定。

## 实测证据

- `docs/evidence/server-smoke.txt`：构建产物 dist/server.js 的 stdio JSON-RPC 冒烟（initialize、工具清单与可见性、定位、快照聚合、rootError、surface meta、面板与配图资源），2026-10-23。
- 面板 UI（主题、树、产物视图、审阅视图、放大覆盖层）的实测需在 ZCode 中安装本插件后进行；本变更交付时该项为待办，安装后按上文章节逐项核对并补录证据。
