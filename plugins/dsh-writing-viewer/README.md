# dsh-writing-viewer（工作区查看插件）

DSH 插件（DSH Plugin）`dsh-writing-viewer` 是 writing-anything 仓库的 DSH 工作区查看插件：只读定位一个包含 `.writing-anything/` 工作区（Workspace）的项目目录，在 DeepSeek Harness（DSH）Web GUI 的全局面板中以总览仪表盘（Overview Dashboard）呈现：作品（Work）卡片、工作区级计数、校验诊断与单元（Unit）树导航；定位不到时给出 `wa-cli init` 空态指引，面板内可切换到显式输入的项目路径。host 侧提供只读的工作区快照（Workspace Snapshot）路由，把工作区的配置、作品与单元树、各单元产物（Artifact）全文、素材、决策、规范包（Pack）与校验诊断聚合为结构化 JSON 供 client 拉取——产物语义视图与语法高亮由本里程碑（`dsh-workspace-viewer`）的后续变更在同一包内扩展。

- 默认严格只读：host 侧的全部文件系统操作都是读取（对 `.writing-anything` 的 `stat`、目录列举 `readdir` 与文件读取 `readFile`，均不跟随符号链接）；不提供任何写入工作区的能力。
- 一个例外要如实说明：快照经子进程调用 wa-cli 只读命令（`status`/`validate`/`next`/`timeline`）取结构状态，而 wa-cli 的审计日志（Audit Log）契约规定每次调用（含只读命令）都向 `.writing-anything/audit.log` 追加一行运维痕迹——该追加出自 wa-cli 而非插件代码，且 audit.log 不是产物（Artifact）；插件在每次快照请求内有界调用这些命令，不做轮询式反复调用。
- 一个由作者显式触发的工作区写入面：单元审阅（见下文「单元审阅」专节，默认开启）以审阅视图（Review View）原生呈现 wa-cli 阅读页（Reading View）的数据，作者在其中提交的反馈、确认与复核经同源代理交给 wa-cli 子进程写入工作区；插件代码只做同源代理，自身不写任何文件，确认渠道（Confirmation Channel）如实记为 `preview`。
- 一个可选的写入面：默认关闭的「内置技能与 CLI」设置能力（`enableSetup`，见下文专节）在作者显式点击启用后，向配置的 CLI 安装目录与操作系统临时目录写入——绝不写入 `.writing-anything/`、用户级或项目级技能目录；静态只读测试把该写入面钉在 setup 三个模块内，viewer 路径保持零写入调用。
- 不属于产品运行时：wa-cli（Go 内核）、内置技能与 MCP 服务不依赖本包；本包也不进入 Go 模块与覆盖率范围。

## 结构

```
cordis.patch.yml   bundle patch：向 DSH profile 组合插入插件行
src/host.ts        host 入口（cordis 插件：导出 name 与 apply(ctx, config)，含只读 fs/spawn 适配器与两个路由注册）
src/client.ts      web client 入口（window.__ModuleLoader__.load 工厂）：总览、单元树、路径切换、加载态与键盘导航的渲染与交互
src/overview.ts    总览视图模型的纯函数：作品卡片、工作区计数、单元树与单元详情（client 打包内联）
src/treekeys.ts    单元树键盘导航的纯逻辑：可见节点过滤与方向键决策（选中/收起/展开），无 React 与 DOM 依赖
src/root.ts        工作区定位的纯逻辑（候选顺序、去重、探测）
src/registry.ts    DSH 工作区注册表（workspaceRegistry.list()）的容错读取
src/snapshot.ts    工作区快照聚合：类型定义与注入式聚合入口（每次请求重读，无缓存）
src/wacli.ts       wa-cli 只读命令桥：--json 输出映射与按区域容错降级
src/routeguard.ts  路由请求来源校验（Request Origin Guard）：与宿主 /api 信任栅栏同构的 Host/cross-site/Origin 判定，纯函数、永不抛错
src/artifacts.ts   产物清单与全文读取（按 JSON Schema 约束的路径，跳过符号链接）
src/queueview.ts   决策队列（Decision Queue）与作者复核记录的纯函数视图模型（消费快照携带的 decisions.yaml / author-reviews.yaml 全文）
src/setup.ts       「内置技能与 CLI」设置核心（纯逻辑）：配置解析、发布产物下载与 SHA256SUMS 校验、原子安装、技能物化与解析、技能提供者注册、PATH 追加（内置后备）
src/setuproutes.ts setup 路由与启用动作管线（状态聚合、单飞行、宿主启动时的免下载注册）
src/setupfs.ts     setup 的真实文件系统适配器——包内唯一持有写入型 node:fs 调用的模块之一（与上两个 setup 模块同为静态只读测试声明的写入面）
src/setupview.ts   client 侧设置区的纯视图模型：状态解析、双语文案、idle/busy/enabled/error 状态机
src/reviewsession.ts 审阅会话（Review Session）：注入式 spawn 管理唯一的 wa-cli preview --serve 子进程（首行信封取端口与令牌、空闲与替换时终止）
src/reviewproxy.ts 阅读页服务同源代理的纯逻辑：只放行 page.json、配图图片（illustration，只转发 unit 与 id）与三个提交接口、令牌注入、只去掉传输头、JSON 形式的无会话提示
src/reviewroutes.ts 审阅会话路由与代理 prefix 路由的注册（先过请求来源校验）
src/reviewview.ts  client 侧审阅控件的纯视图模型：启动/进行中/失败/来源拒绝/已结束状态与双语文案
src/reviewmodel.ts client 侧审阅视图（Review View）的纯视图模型：page.json 解析（含配图（Illustration））、配图布局与固定代理取图地址、提交请求体、单段选区规则与「待审阅」标记索引
src/copyroutes.ts  「复制图文」只读路由：来源校验、作品 id 校验，调用一次 wa-cli export --format paste-html --print --json，只回传正文片段、配图数与诊断（含 severity）
src/copyview.ts    client 侧「复制图文」：路由响应解析、text/plain 生成、剪贴板写入（异步剪贴板接口，非安全上下文退回 copy 事件写入，片段不进 DOM）与双语文案
scripts/gen-large-workspace.mjs  经 wa-cli 生成多卷多场景长篇合成工作区（性能检查夹具）
scripts/perf-check.mjs           端到端大工作区性能检查并把耗时记录进 docs/evidence/perf-check.txt
testdata/          测试夹具：真实命令生成的多作品工作区 + 捕获的 --json 输出
dist/index.js      构建产物：host 入口（package.json 的 "." 导出）
dist/client.js     构建产物：client 入口（package.json 的 "./client" 导出）
```

package.json 声明：

- `dsh.bundle.patch` → `./cordis.patch.yml`（insert 一个插件行）；
- `dsh.client` → `{ inject: ["@deepseek-ai/dsh-client-ui-sidebar-right"], platform: "web", immediately: true }`（先载入宿主右侧栏模块，其 client 提供 `sidebarRightTabs` 服务）；
- `engines.dsh` → `>=0.2.1-rc.2 <0.3.0`（实测验证用的宿主为 DSH 0.2.1-rc.2）。

## host 侧行为

`apply(ctx, config)` 通过 scoped `ctx.inject` 获取两个可选服务，宿主没有它们时插件保持安静：

- `workspaceRegistry`（DSH 工作区注册表，root 作用域）：每次请求同步 `list()` 全部已注册工作区的 `path`（0.2.1-rc.2 实测：`workspaceController.follow` 挂在按连接的 remotes API 组合上，静态插件行的 `ctx.inject` 闭包不触发，不可用）。
- `webServer`（web profile）：注册两个只读 GET 路由（`enableSetup` 开启时另注册一条 setup 路由，见下方专节）：
  - `/dsh-writing-viewer/workspace`：轻量定位状态，返回 JSON：

```json
{
  "ok": true,
  "root": "/home/user/Code/writing-anything",
  "source": "workspace-registry",
  "workspaceDir": "/home/user/Code/writing-anything/.writing-anything",
  "workspaceExists": true,
  "candidates": [{ "source": "workspace-registry", "path": "…", "workspaceExists": true }],
  "emptyState": null
}
```

项目目录解析规则：

- 配置了 `workspaceRoot`（在 profile 的 `cordis.patch.yml` 中为 `dsh-writing-viewer` 行设置 `config.workspaceRoot`，或经 `--patch` 覆盖层注入）时为排他固定：只探测该目录，目录下没有 `.writing-anything/` 就进入空态，不再回退到其他来源；
- 未配置时按顺序探测：DSH 工作区注册表的全部路径（注册表顺序）→ 宿主进程工作目录 `process.cwd()`，第一个探测到 `.writing-anything/` 的候选胜出；

`candidates` 按实际探测顺序记录轨迹；全部落空时 `root` 为 `null`、`emptyState.command` 为 `wa-cli init`。

  - `/dsh-writing-viewer/snapshot`：工作区快照（Workspace Snapshot），聚合结构如下（字段只增不改）：

```json
{
  "ok": true,
  "root": "…", "source": "workspace-registry", "workspaceDir": "…/.writing-anything",
  "workspaceExists": true, "candidates": [], "emptyState": null,
  "degraded": [],
  "snapshot": {
    "config": { "path": "config.yaml", "format": "yaml", "text": "…" },
    "decisions": { "…": "…" }, "authorReviews": null, "handoffs": { "…": "…" },
    "packLock": { "…": "…" }, "packs": [ { "path": "packs/lean-style.yaml", "…": "…" } ],
    "sources": [ { "id": "src-note", "sourceYaml": { "…": "…" }, "original": { "…": "…" } } ],
    "works": [
      { "id": "my-novel", "dir": "works/my-novel", "workYaml": { "…": "…" },
        "units": [ { "ref": "my-novel/scene-1", "type": "scene", "stage": "brief", "blockers": [], "progress": null, "commands": [], "dir": "works/my-novel/units/scene-1", "artifacts": { "outline": { "path": "…", "format": "yaml", "text": "…" } }, "draft": null } ],
        "ledgers": { "canon": { "…": "…" }, "threads": { "…": "…" }, "state": { "…": "…" }, "knowledge": { "…": "…" } },
        "timeline": [ { "unit": "my-novel/scene-1", "time": "第 1 夜", "after": [], "position": 1 } ] }
    ],
    "cli": { "status": { "…": "wa-cli status --json 的映射" }, "validate": { "…": "诊断与 checked" }, "next": { "…": "待复核摘要" }, "timelines": { "my-novel": { "…": "…" } } }
  }
}
```

快照数据来源（里程碑定案的混合方案）：结构状态（单元树、阶段、阻塞项、进度、诊断、待复核摘要、长篇时间线）复用 wa-cli 只读命令的 `--json` 输出——每次请求有界调用 `status`、`validate`、`next` 与每个长篇作品一次 `timeline`；产物全文按 JSON Schema 约束的路径直接读取文件（YAML 与 Markdown 全文，含 `draft.md` 正文）。wa-cli 不可用、执行失败、超时或输出缺字段时按区域降级：`degraded` 数组记录原因，文件可解析的数据照常返回，不整体失败。每次请求都重新读取（无缓存、无文件监听）；client 侧的手动刷新即重新拉取该路由。

快照路由接受可选查询参数 `root`（项目目录的绝对路径，供面板内路径切换）：

- 合法时该路径是本次请求的唯一候选（`source: "panel-input"`），优先于插件配置的 `workspaceRoot` 固定，只作用于这一次请求，host 不保存状态；
- 不是绝对路径（或含 NUL、超长、以两个斜杠或反斜杠开头的网络与设备路径）时不做任何读取，返回空态并附 `rootError` 说明原因；路径下没有 `.writing-anything/` 时同样返回空态与 `rootError`——两种情形都不回退到默认定位，避免面板静默显示另一个项目；
- 不带参数时行为与响应形状不变（不出现 `rootError` 字段）。读取范围始终限于所选目录的 `.writing-anything/`，wa-cli 仍以 `--dir <所选路径>` 有界调用。

插件配置除 `workspaceRoot` 外还接受 `waCliPath`：覆盖 wa-cli 可执行文件路径（默认 `PATH` 上的 `wa-cli`），用于宿主未把 wa-cli 装进 PATH 的环境。

## 单元审阅（Review，默认开启）

审稿环节作者要用的 wa-cli 阅读页（Reading View，`wa-cli preview <unit> --serve`）只监听宿主机 `127.0.0.1`；经 Tailscale 等网络远程打开 DSH 的浏览器访问不到它。插件因此在 DSH 同源路由下代理阅读页服务的数据与提交接口，由面板以原生组件呈现审阅视图（Review View），不使用 iframe：

1. **发现待审阅。** 快照从 `wa-cli next --json` 读取待作者确认事项（`pendingConfirmations`，与阅读页「待作者确认」区同一口径：验收、决策、规划步骤、状态与知情提议、卷末检查点）与待作者复核项（`pendingAuthorReviews`，strict 关卡）。有事项的作品卡片显示「待审阅 · N」，单元树中事项关联的单元带「● N」标记；点它们即选中该单元（作品卡片为作品根单元）并直接开始审阅。next 区域降级时不显示标记，降级原因照常列出。
2. **启动会话。** 单元详情的「审阅」按钮（或上述标记）向 `POST /dsh-writing-viewer/review-session`（`{ unit, root }`，root 为面板当前显示的项目目录）发起启动；host 先结束任何既有会话，再执行一次 `wa-cli preview <unit> --serve --json --dir <root>`，10 秒内读到首行输出信封即成功，从中取得端口与令牌——令牌只留在 host 进程内存，不进入任何响应；之后持续读空子进程输出。同一宿主同一时间至多一个会话（审阅会话，Review Session）。`GET` 同一路由返回当前会话摘要（unit、root、开始时间），`POST /dsh-writing-viewer/review-session/end` 显式结束。
3. **只代理 JSON 接口。** 代理是一条 prefix 路由 `/dsh-writing-viewer/review`，只放行 `GET …/review/page.json`（对应阅读页服务的 `GET /page.json`，阅读页数据）、`GET`/`HEAD …/review/illustration`（对应阅读页服务的 `GET /illustration`，只转发查询参数 `unit` 与 `id`，取已登记的配图（Illustration）与封面图（Cover Image）图片）与 `POST …/review/feedback`、`…/review/confirm`、`…/review/author-review`；阅读页 HTML（`GET …/review/`）与其他子路径一律 404，方法不符 405。代理丢弃浏览器带来的 `token` 与 `X-WA-Token`、注入会话令牌，上游 Host 为 `127.0.0.1:<port>`，wa-cli 自身的 Host 与令牌校验原样生效；请求体超过 64 KiB 不转发。上游正文（含图片字节）逐字节回传，响应头只去掉由宿主重新派生的传输头（content-length、content-encoding 等），安全头原样保留。浏览器始终只请求 DSH 同源地址、看不到 `127.0.0.1`。
4. **原生审阅视图。** 视图按单元树先序逐段显示正文与段落序号，按出处（作者、Agent、Agent 起草作者修改）标色（可关闭），段落下列出文本检查与审稿发现项；单元有配图时，封面图显示在作品根单元开头并标「封面」，正文配图以 img 显示在引用它的段落之后（地址由面板以固定代理路由加 `unit` 与 `id` 拼出，不采信正文或 page.json 中的地址，旁边以纯文本标出氛围图或信息图、出处与信息图关联的论断和素材），未被正文引用的配图在单元末尾标「未引用」，wa-cli 判为不可用（缺失、被改动或未通过校验）的配图只显示说明、不请求图片；另列最近失败的命令、「待作者确认」区（说明、决策选项与推荐、原话摘要输入、确认与可拒绝事项的拒绝）与 strict 关卡下的「待作者复核」区（默认全选、「复核所选」）。在某一段内选中文字即成为反馈的引用，也可改为针对整个单元；选区跨段落时不提交并提示。每次提交成功后视图重新拉取 page.json；wa-cli 拒绝时显示其诊断并保留已填内容。来自工作区的文字一律作为纯文本显示。
5. **结束。** 会话在作者点「结束审阅」、30 分钟内没有代理请求、启动另一个单元的审阅、阅读页子进程退出、插件卸载或宿主停止时结束并终止子进程。卸载与停止的清理挂在 cordis 的 `ctx.effect` disposer 上（DSH 0.2.x 的 cordis 4 不发出 `dispose` 事件，`ctx.on('dispose')` 永不触发）；DSH 收到 SIGTERM/SIGINT 时会 dispose 应用 fiber，因此常规停止也会清理，宿主进程退出另有 `exit` 钩子兜底——只有宿主被 SIGKILL 强杀时子进程会残留（`wa-cli preview --serve` 本身不感知父进程退出），需手动结束。会话结束后代理对数据与提交都返回 JSON 形式的「没有进行中的审阅」；面板在每次拉取数据、窗口重新获得焦点或页面重新可见时查询一次会话状态（只访问插件路由，不调用 wa-cli），发现已结束或已被另一单元的审阅替换即显示「审阅已结束」与「重新审阅」入口。结束审阅后面板自动刷新快照，处理完的「待审阅」标记随之消失。
6. 写入全部出自 wa-cli 子进程：经代理提交的反馈、确认与复核与直接使用阅读页完全相同，确认渠道（Confirmation Channel）记为 `preview`；只浏览不提交时工作区（除 wa-cli 追加的 audit.log 外）不变。

**让 Agent 指引作者到 DSH 审阅（无需配置）。** DSH Web 会向模型的 shell 注入环境变量 `DSH_WEB_URL`；工作区没有设置 `review_surface` 时，wa-cli 据此判断 Agent 运行在 DSH 中，`wa-cli status --json` 的 `config.reviewSurface` 即为 `dsh`（`reviewSurfaceSource` 为 `env`）。内置技能 wa-revise、wa-publish、wa-assemble 与 wa-auto 读到 `dsh` 就不再启动 `wa-cli preview --serve` 或交出 `127.0.0.1` 链接，而是请作者打开本面板、在「待审阅」入口完成审阅，作者说完成后再以 `wa-cli review show` 或 `wa-cli next` 复查；作者说面板里没有「待审阅」入口时改给浏览器阅读页。同一工作区在终端中的 Agent（没有 `DSH_WEB_URL`）仍得到 `browser`。需要固定某一种时用 `wa-cli config set review_surface browser|dsh --json` 显式设置，设为空字符串即恢复自动判断。

实测证据（2026-10-21，iframe 版本，经 Tailscale MagicDNS 名访问隔离的 DSH 0.2.1-rc.2 实例）：`docs/evidence/review-live-check.txt`、`docs/evidence/review-*.png`。原生审阅视图的实测证据见 `docs/evidence/native-review-*`。

Tailscale 远程访问：以 `http://<machine>.<tailnet>.ts.net:13080`（`tailscale serve --tcp` 直通）或 `https://<machine>.<tailnet>.ts.net:13080`（`tailscale serve --https`，Tailscale 终止 TLS 并自动签发、续期证书）这类 MagicDNS 地址访问时，Host 不是回环地址也不是宿主监听地址字面量，所以全部插件路由（含审阅）都需要在 `allowedHosts` 中放行该权威，例如：

```yaml
- id: dsh-writing-viewer
  name: dsh-writing-viewer
  config:
    allowedHosts:
      - gamepc.tail1234.ts.net:13080   # 浏览器地址栏里的主机名与端口
```

`tailscale serve --https=<port> http://127.0.0.1:<port>` 会把浏览器地址栏里的 Host（`<machine>.<tailnet>.ts.net:<port>`）原样转给 DSH（2026-10-21 实测：同源请求通过，伪造为 `http://127.0.0.1:<port>` 的 Origin 被判为与 Host 不同权威而 403），所以从 http 换成 https 时 `allowedHosts` 与 DSH 的 `--trusted-host` 写法都不变——同一个主机名与端口即可。审阅视图的请求都是不带协议的同源路径，随页面走 https，不产生混合内容；代理到 `http://127.0.0.1:<port>` 的那一段只发生在宿主进程内部。证书只签给 MagicDNS 名，按 `https://<tailnet IP>:<port>` 访问会证书不匹配。

未放行时全部插件路由都被来源校验拒绝：面板在加载快照时即显示被拒原因（指出被拒的 Host）与重试按钮，不崩溃也不空白；若只有审阅路由被拒，单元详情显示在 `allowedHosts` 中放行该权威的提示，不会出现空白的审阅视图。纯 http 的 tailnet 地址不是安全上下文（Secure Context），https 地址是；面板只有「复制图文」会优先使用仅安全上下文可用的异步剪贴板接口，在 http 地址上自动退回选区复制，两种访问方式都可用。

安全边界：审阅默认开启、没有配置开关，凡通过请求来源校验的请求（本机进程、`allowedHosts` 放行的远程权威）都能经代理提交作者确认。wa-cli 生成的 HTML 与脚本不再进入 DSH 源：代理只转发 JSON 与已登记配图的图片，面板以纯文本节点显示其中的工作区文字。安全前提是 tailnet 本身只对作者的设备开放——**不要用 Tailscale Funnel 或其他公网反向代理暴露 DSH**；需要更宽的网络暴露时请置于带认证的反向代理之后。

## 复制图文（Copy with images）

在面板里选中文字复制时，审阅视图中的配图地址是带会话令牌的同源代理路由，粘贴到公众号编辑器或在线文档后图片取不到。「复制图文」按钮因此不复制审阅视图，而是复制 wa-cli 渲染的可粘贴正文（Paste-ready Page 的正文）：

1. **入口。** 作品根单元的单元详情（「审阅」旁）与审阅视图头部（含「放大」后的覆盖层）各有一个「复制图文」按钮，复制对象都是该单元所属的整篇作品。
2. **只读取用。** 点击后面板请求 `GET /dsh-writing-viewer/copy?work=<作品>&root=<项目目录>`；host 先做请求来源校验，作品 id 经与审阅相同的参数校验（另不得含 `/`），再以参数列表调用一次 `wa-cli export <作品> --format paste-html --print --json --dir <root>`。`--print` 是 wa-cli 的只读模式，不写导出文件、资产目录或配图清单，插件仍不向项目目录写任何文件。该调用的输出上限为 128 MiB、时限 120 秒：wa-cli 最多内嵌 32 MiB 图片字节，base64 后约 42.7 MiB，--print 的信封里全文（export.html）与分段（export.segments）各带一份，再加 JSON 转义，最坏约 98 MiB；超过上限时面板显示可读原因，可改用 `wa-cli export --format paste-html` 导出的可粘贴页复制。
3. **复制内容与导出一致。** 只含验收记录有效的单元；可用配图以 data URI 内嵌，图说为标题（没有时为替代文字）；不含封面图与 AI 参与说明。按公众号图文编辑器的粘贴限制（2026-10-23 实测：单张超过约 1 MiB 或一次超过 7 张时整次粘贴丢掉全部图片），超过 1 MiB 的配图由 wa-cli 缩到最长边 1600 像素以内并重新编码为不超过 1 MiB 的 JPEG（资产目录仍为原图）；超出 32 MiB 内嵌上限、像素过多或压不下来的配图，以及外链与行内图片，保留「【配图 n】」占位，面板提示按导出的配图清单补传。
4. **分段。** 内嵌图超过 7 张时 wa-cli 按段落边界把正文切成每段至多 7 张图的分段（`export.segments`）。面板第一次点「复制图文」时不写剪贴板，而是列出「复制第 n 段（图 a–b）」与「复制全文」按钮；作者逐段点击、逐段粘贴到公众号，每次点击都在该次点击中复制已取到的内容，不再调用 wa-cli。「复制全文」用于飞书等不限张数的目标。
5. **写剪贴板。** 安全上下文（本机或 https）下用异步剪贴板接口同时写入 `text/html` 与 `text/plain`；剪贴板项在点击时同步创建，取正文的请求在其后完成，因此不丢失点击授权。http 远程访问等非安全上下文退回为 `execCommand('copy')`：选区是只放纯文本的离屏 textarea，copy 事件监听器把 `text/html` 与 `text/plain` 写进 `clipboardData`，正文片段从不插入页面 DOM；浏览器拒绝时面板保留取到的正文并提示再点一次，再次点击即在新的点击中同步复制。
6. **失败提示。** 作品没有可导出的单元时显示 wa-cli 的 `export.nothing-accepted` 诊断与下一步命令（失败原因取第一条 error 级诊断，wa-cli 在它之前列出的 `export.unaccepted` 警告不作为原因），不写剪贴板；wa-cli 不可用、超时或输出超出上限时同样只显示原因，不影响面板其余部分与进行中的审阅会话。

公众号编辑器对粘贴进来的 base64 图片是否全部保留取决于编辑器本身；不保留时按 `wa-cli export <作品> --format paste-html` 写出的配图清单与资产目录手动上传。

实测证据（2026-10-23，本机与局域网 IP 两个隔离 DSH 实例，headless Chrome）：`docs/evidence/copy-live-check.txt`、`docs/evidence/copy-review-insecure.png`、`docs/evidence/copy-nothing-accepted.png`。

## 内置技能与 CLI 的启用（Setup，默认关闭）

配置开关 `enableSetup`（默认 `false`）之后是插件的全部「安装类」能力。未开启时插件不注册 setup 路由、不注入宿主 `skills` 服务、不修改宿主环境、不发起任何网络请求，行为与本节不存在时完全一致。

开启后面板出现「内置技能与 CLI」设置区（client 打开面板时探测一次 `GET /dsh-writing-viewer/setup`，探测不到即整区隐藏），其中唯一动作是「启用内置技能与 CLI」按钮，一次点击完成全链路，同一时间至多一个进行中的动作：

1. **确保内置 CLI**：配置目录（`cliInstallDir`，默认 `~/.local/bin`）没有 `wa-cli` 时，从本项目 GitHub Releases 下载对应平台发布产物（Release Asset，`wa-cli-<os>-<arch>`，linux/darwin × amd64/arm64），按同目录 `SHA256SUMS` 记录校验 SHA-256 后原子安装（同目录临时文件 + rename、`chmod 755`）；校验或下载失败可读报错且不留部分文件。已配置 `waCliPath` 时直接使用该二进制、不下载。`waVersion` 为具体版本号且与现存二进制不符时重新下载；为默认 `latest` 时现存二进制视为就绪、不重复下载（要升级就填具体版本号或删掉二进制）。host 启动时不做任何下载——下载只由这次显式点击触发。
2. **注册内置技能**：以临时目录为 cwd 与 `--target` 执行 `wa-cli skills install --json` 物化全部内置 wa-\* 技能（该目录下没有 `.writing-anything`，实测不产生任何审计行），解析 SKILL.md 后经宿主技能注册表（`@deepseek-ai/dsh-skill` 的 `ctx.skills.registerProvider`）注册为一个技能提供者（Skill Provider），rank 650——高于 DSH 全部技能层级（project `.dsh`=100、project `.agents`=200、custom=300、user=400/500、运行时注册=250、bundled=600）的 rank 升序竞争，因此存在同名外部技能时外部版本优先，内置版本仅在无同名外部技能时提供（后备）。不向 `~/.agents/skills`、`.claude/skills` 或项目 `.agents/skills` 写任何文件；物化目录用后即删。
3. **外部 CLI 优先、内置后备**：把内置二进制所在目录幂等追加到宿主 `process.env.PATH` 末尾（目录已在 PATH 任意位置则不动）。DSH 子进程环境以宿主 `process.env` 为基底（过滤敏感键与 `DSH_*` 前缀，见 dsh-subprocess），因此之后的 Agent shell 工具与插件自身的快照桥按既有 PATH 顺序优先解析外部 `wa-cli`，仅在无任何外部 `wa-cli` 时落到内置二进制；设置区如实显示当前解析路径与生效的外部版本（如存在，内置为后备）。该追加是宿主进程级副作用：只影响本宿主进程后续派生的子进程，不修改用户 shell 配置。

设置区同时回答「内置还是外置」：**技能来源对照表**逐个列出内置名单技能的当前胜出来源——外部显示来源层级标签（project-dsh、project-agents、custom、user-dsh、user-agents、bundled）与 SKILL.md 路径，内置显示 dsh-writing-viewer 提供者，并标明内置后备就绪（按文件系统技能源的优先级顺序做目录存在性比对：项目 `.dsh/skills`、项目 `.agents/skills`、用户 `~/.dsh/skills`、`~/.agents/skills`，项目目录随面板定位经 `root` 参数传入；自定义技能目录（custom）与其他提供者不在比对之列，加载时的技能包裹层仍是权威）；**CLI 溯源标记**——插件下载安装 wa-cli 时在同目录写 `wa-cli.dsh-writing-viewer.json`（managedBy、sha256、installedAt），状态按「有标记且 sha256 与磁盘二进制一致 → 插件安装」如实分类，操作者经 `waCliPath` 指定的路径、标记缺失或与磁盘不符一律如实显示非插件安装（install.sh 与本插件默认同目录，只有标记能区分谁装的）。

host 启动且开关开启、内置二进制已存在时，自动重做第 2、3 步（免下载、无网络），宿主重启后技能无需再点。设置区状态（GET）与启用动作（POST）共用一条路由 `/dsh-writing-viewer/setup`，与既有路由同构地做请求来源校验（Request Origin Guard）：未通过校验的请求返回 403 且不触发任何下载或注册；其他方法返回 405。状态查询与启用动作全过程不读写任何 `.writing-anything/`（含 audit.log）。

```yaml
- id: dsh-writing-viewer
  name: dsh-writing-viewer
  config:
    enableSetup: true                # 打开「内置技能与 CLI」能力（默认 false）
    waVersion: latest                # 可选：发布版本号；填具体 tag（如 v0.3.0）时版本不符会重新下载
    cliInstallDir: ~/.local/bin      # 可选：内置 wa-cli 安装目录（默认 ~/.local/bin，支持 ~ 前缀）
    waBaseUrl: https://github.com/kw12121212/writing-anything/releases   # 可选：镜像或测试用发布地址
    workspaceRoot: /home/user/Code/writing-anything
    waCliPath: /usr/local/bin/wa-cli     # 可选：已有 wa-cli 时直接使用，跳过下载
    allowedHosts:                        # 可选：显式放行的远程权威（bare host[:port]）
      - gamepc.example.net:13080
```

安全边界（沿用本 README「发现的 DSH API 约束」的登记原则）：setup 路由不在宿主认证栅栏内，靠来源校验拒绝陌生 Host、`cross-site` 与异权威 Origin 的浏览器载体请求，但本机任何进程都能 POST 触发下载与安装——与直接运行 `install.sh` 等价，不是该防线针对的威胁；下载目标只限本插件配置的 `waBaseUrl` 下固定命名的产物且必须通过 SHA256SUMS 校验。远程暴露宿主时请配合 `allowedHosts` 与带认证的反向代理，并考虑不开 `enableSetup`。

## client 侧行为

以 lazy-CJS 协议（`window.__ModuleLoader__.load`，factory 返回 `{ apply, inject }`）注册两个插槽（slot）条目：

- root-scoped `main` keyed 面板，key 为 `dsh-writing-viewer`（`ctx.layout.selectPanel('dsh-writing-viewer')` 可选中）；
- `sidebar.panellist` 列表条目，id 同为 `dsh-writing-viewer`，侧栏按钮由宿主渲染并在点击时选中面板；
- 每个会话右侧边栏的「写作工作区」页（与 dsh-plugin-strict-spec 同款交互）：经宿主 `sidebarRightTabs` 服务注册静态页面类型（kind `writing-workspace`，guide 入口盒），并在 `sidebar.right.pane.tab` 插槽以同 id 注册正文（复用同一面板组件，窄容器自动上下堆叠）。依赖 `package.json` 的 `dsh.client.inject` 声明 `@deepseek-ai/dsh-client-ui-sidebar-right`（宿主模块先载入、提供该服务与插槽）——注意 `sidebarRightTabs` 是 client 激活硬依赖（声明于 exports.inject）：宿主 client 组合若不提供该服务，本插件的 client 整体（含左侧入口）不会激活；声明引擎范围内的宿主均随带该模块。该宿主模块不属于对第三方承诺的契约（见「发现的 DSH API 约束」第 7 条），升级宿主后需实测。

面板拉取快照路由，按写作概念（而非目录树）组织：

- **工具栏**：当前项目目录与来源、刷新按钮；路径输入框与「切换」「恢复默认」——输入项目目录的绝对路径即以 `?root=` 重新拉取快照。所选路径只保存在面板组件状态里。刷新或切换进行中，旧数据保留在屏幕上并显示「读取中…」提示；只有最新一次请求的响应会落地，较慢的旧响应被丢弃，不会覆盖新的选择。
- **总览仪表盘（Overview Dashboard）**：工作区级计数——素材数、待决决策数（`decisions.yaml` 中 `status: open` 的条目）、待复核数（`wa-cli next` 的 `pendingAuthorReviews`）、validate 诊断数；对应区域降级（含 `next` 缺少 `pendingAuthorReviews`、`validate` 缺少 `diagnostics` 这类部分降级）时显示「—」而不是 0。诊断按严重级别汇总并可展开为列表，降级原因同样可展开。「素材」「待决决策」「待复核」三个磁贴可点开对应清单视图（素材清单、决策队列、复核记录）。
- **作品卡片**：作品 id、文体（`work.yaml` 的 `genre`）、单元数、当前阶段、进度（根单元为汇总单元时取其 progress，否则为最外层各汇总单元之和）、去重后的阻塞项数量与前两条摘要（汇总单元会重复列出子孙的阻塞项，按 code、路径与消息去重）。
- **单元树与单元详情**：选中作品后，单元树按 `wa-cli status` 的 parent 关系缩进展示，汇总单元带 done/total 进度标记、有阻塞项的单元带计数；status 降级时退回 `work.yaml` 的层级（汇总单元不显示其占位阶段）。选中单元后详情区显示类型、阶段、进度、阻塞项、下一步命令与产物清单（名称与路径）；产物内容视图由后续变更挂载到同一详情区。
- **空态与错误态**：定位不到工作区显示 `wa-cli init` 指引（路径切换失败时附原因）；工作区没有作品显示 `wa-cli new <genre> <id>` 指引；取数失败显示原因与重试。
- **加载反馈**：首次打开面板、快照尚未返回且没有数据可显示时，面板显示独立的加载提示（`role="status"` + `aria-busy`），不空白、也不复用空态或错误态文案；刷新与路径切换进行中旧数据保留并显示进行中提示，较慢的旧响应被丢弃。
- **键盘导航**：面板内全程可不碰鼠标——焦点在单元树上时 ↑/↓ 在可见单元间移动选中（详情区同步更新，DOM 焦点随选中移动）、→ 展开已收起的汇总单元或进入第一个子单元、← 收起已展开的汇总单元或回到父单元（roving tabindex：选中节点是树内唯一 Tab 停留点，带 `aria-current` 与 `aria-expanded`，Tab 离开树到下一控件）；单元详情里的「打开」产物条目、视图切换按钮与素材跳转都是原生按钮，Enter/空格即激活。
- **主题与窄宽度**：样式只用 DSH 主题令牌（`--dsw-alias-*` CSS 自定义属性，带回退值），明暗主题随宿主切换；视口低于 1024px（DSH 侧栏收为 56px 导轨）或面板容器低于 640px 时，单元树与详情改为上下堆叠。

面板自身只有刷新、路径切换与选择三类控件，没有任何编辑、写入或确认控件；作者确认与复核走 wa-cli 阅读页（Reading View）——单元详情的「审阅」按钮把它经同源代理内嵌到面板里（见上文「单元审阅」），其中的反馈、确认与复核控件由 wa-cli 提供并写入。另一个例外是默认关闭的 setup 设置区（`enableSetup` 开启且路由可达时出现）：它的「启用内置技能与 CLI」按钮是显式的安装动作，不作用于任何工作区产物。文案中英双语，按页面语言选择。

client bundle 内联了 `yaml` 解析器（用于读取 `work.yaml` 的文体与 `decisions.yaml` 的待决条目），`dist/client.js` 约 306 kB（gzip 约 73 kB；2026-10-20 实测：`wc -c dist/client.js` 为 305,683 B、`gzip -c dist/client.js | wc -c` 为 73,461 B，体积随依赖与视图演进会变，以这两条命令复验），仍是单一 `__ModuleLoader__.load` 脚本、唯一外部依赖为宿主的 `react`，不访问网络。

## 开发、构建与测试

需要 Node ≥ 22.13 与 pnpm。所有命令在包目录内运行：

```sh
pnpm install            # 安装开发依赖
pnpm run build          # 类型检查（tsc --noEmit）+ 构建（tsdown）+ 产物名归一
pnpm test               # vitest 单元测试（定位、路径切换、注册表、路由、快照聚合、CLI 桥降级、只读约束、总览视图模型、面板渲染、产物视图、高亮器、键盘导航、setup 全链路）
pnpm run check          # 一次执行类型检查、构建与全部测试（统一检查入口）
pnpm run perf:check     # 大工作区性能检查：构建 wa-cli → 生成 3 卷×8 章×8 场景（220 单元）合成夹具 → 计时并记录 docs/evidence/perf-check.txt
```

构建产物名归一（`scripts/normalize-outputs.mjs`）把 tsdown 的 `host.js`/`client.iife.js` 改名为 `dist/index.js`/`dist/client.js`，与 package.json 的 `exports` 对齐；缺少任一产物时构建失败。性能检查的夹具由 `pnpm run perf:workspace`（`scripts/gen-large-workspace.mjs`）经 wa-cli 生成，结构与真实长篇一致；`perf:check` 是记录证据而非数值门槛，实测数字见 `docs/evidence/perf-check.txt`。

## 界面导览

按写作概念（而不是目录树）组织，全部只读；截图在 `docs/evidence/`：

1. **总览仪表盘（Overview Dashboard）**（`overview-default.png`）：每个作品一张卡片（文体、单元数、当前阶段、汇总进度、去重后的阻塞项数量与前两条摘要），工作区级计数（素材、待决决策、待复核、validate 诊断；对应区域降级时显示「—」而不是 0），诊断与降级原因可展开。
2. **单元树与单元详情**（`overview-switched.png`、`overview-narrow-tree.png`）：选中作品后按卷、章、场景层级缩进展示单元树，汇总单元带 done/total 进度、有阻塞项的单元带计数；选中单元后详情区显示类型、阶段、进度、阻塞项、下一步命令与产物清单。
3. **产物视图**：单元详情的产物清单里「打开」任一产物——YAML 产物默认语义卡片视图（按 Schema 字段组织成卡片），一键切换源码高亮视图（滚动位置保持；大 YAML 按顶层键折叠）；draft 正文提供渲染阅读视图与源码高亮切换；claims 语义卡片中的素材引用可点击跳转到素材条目。
4. **素材清单**：总览的「素材」计数即可进入素材清单，逐条查看 source.yaml 语义卡片与原文 Markdown（阅读视图/源码高亮切换）。
5. **决策队列与复核记录**：「待决决策」「待复核」磁贴分别点开决策队列（Decision Queue）清单（逐条展示 id、status、question、options 与推荐标记、rationale、阻塞标记、关联单元）与作者复核记录（Author Review Record）清单（kind、ref、作品、渠道、确认与复核时间）；文件缺失显示空态提示、解析失败显示错误提示；全部只读，作者决定与确认仍走 wa-cli。
6. **空态与路径切换**（`panel-empty.png`、`panel-switch-invalid.png`）：定位不到工作区显示 `wa-cli init` 指引；工具栏可输入另一个项目目录的绝对路径切换查看，或恢复默认定位。
7. **加载与键盘**（`polish-first-load.png`、`polish-keyboard-tree.png`、`polish-large-workspace.png`）：首次打开显示加载提示（无数据、非空态、非错误态）；↑/↓/←/→ 在单元树内导航（含收起/展开，截图摄于键盘把选中移到 ch-1-1 并收起其 8 个场景后）；`polish-large-workspace.png` 为 3 卷×8 章×8 场景（220 单元）合成长篇的实测面板。

## 安装

公开渠道（推荐）：从本仓库的 GitHub Release 安装插件包（Plugin Package）tarball——它与同版本的 wa-cli 发布产物（Release Asset）一同发布，一条命令完成依赖与 bundle 登记：

```sh
dsh plugin --profile web add https://github.com/kw12121212/writing-anything/releases/latest/download/dsh-writing-viewer-0.9.1.tgz
```

URL 固定用 `latest/download`，升级时先 `dsh plugin --profile web remove dsh-writing-viewer` 再以新版本文件名重新 add。

开发模式：以 link: 依赖安装本仓库内的插件（改动源码后重新 `pnpm run build`，无需重装）：

```sh
pnpm --dir plugins/dsh-writing-viewer run build
dsh plugin --profile web add link:/home/wx766/Code/writing-anything/plugins/dsh-writing-viewer
```

`dsh plugin add` 在 profile 的 package.json 中同时登记依赖与 `dsh.profile.bundles`。插件行在宿主启动时组合生效——已经运行的 Web GUI 不会自动加载新装的插件，需重启宿主或在另一端口启动一个新实例验证（避免重启正在使用的 GUI 进程）。

按 DSH 版本验证兼容性：`engines.dsh` 不满足运行中的 DSH 版本时，安装会要求版本豁免（`dsh plugin --profile web allow-version …`）；本插件按 0.2.1-rc.2 实测，范围声明为 `>=0.2.1-rc.2 <0.3.0`。

安全提示：插件的全部路由（两条只读路由、审阅会话与代理路由、setup 路由）自带请求来源校验（Request Origin Guard，见下方「发现的 DSH API 约束」第 8 条）——不在 Web GUI 浏览器认证（`?token=`）栅栏之内的请求，只有 Host 为回环地址、宿主监听地址字面量、或 `allowedHosts` 显式放行的权威，且不带 `cross-site` 标记、Origin 与 Host 同权威时才被处理，否则返回 403（`cache-control: no-store`）。经反向代理或 tailscale 等远程权威访问面板时，需在该 profile 的插件配置中显式放行（见下方示例）；本机其他进程仍可直接访问路由——这与直接读取工作区文件等价，不是该防线针对的威胁。

如需固定展示某个项目目录，在该 profile 的 `cordis.patch.yml` 中为插件行加配置：

```yaml
- id: dsh-writing-viewer
  name: dsh-writing-viewer
  config:
    workspaceRoot: /home/user/Code/writing-anything
    waCliPath: /usr/local/bin/wa-cli     # 可选：覆盖 wa-cli 可执行文件路径
    allowedHosts:                        # 可选：显式放行的远程权威（bare host[:port]）
      - gamepc.example.net:13080         # 带端口：精确匹配该权威
      - proxy.internal                   # 不带端口：任意端口匹配该主机名
```

### 与 wa-cli 版本的兼容说明

- 快照的结构状态（单元树、阶段、阻塞项、诊断、待复核摘要）来自 wa-cli 只读命令的 `--json` 输出，每次完整快照的有界命令集合是 `status`、`validate`、`next` 各一次、每个长篇（novel）作品 `timeline` 一次；产物与素材全文按 JSON Schema 约束的路径直接读文件，不依赖命令输出。
- 0.x 的 wa-cli `--json` 字段仍可能演进。映射层（`src/wacli.ts`）按区域独立降级：二进制不可用、执行失败、超时或输出缺少所需字段时，该区域在面板上标注降级原因（总览计数显示「—」而不是 0），文件可直接解析的产物全文照常返回，整体不失败。
- 面板按「渲染并核对」验证而不是按版本号：仓库测试夹具 `testdata/cli/*.json` 捕获自 2026-10-20 的仓库 wa-cli（v0.2.x 世代）真实输出；升级 wa-cli 后运行 `pnpm run check` 与面板实测即可确认映射仍成立，缺字段会以降级形式显式暴露而不是静默错误。
- `wa-cli` 不在 `PATH` 上时可用上面的 `waCliPath` 配置指向二进制；找不到可执行文件时插件按降级路径工作（产物全文仍可查看）。

## 实测加载证据与 API 约束

以下证据来自 2026-10-19 在本机对 DSH 0.2.1-rc.2（web profile）的实测；截图在 `docs/evidence/`。

### 安装与加载

- `dsh plugin --profile web add link:/home/wx766/Code/writing-anything/plugins/dsh-writing-viewer` 一次完成依赖安装与 bundle 登记：profile 的 package.json 中新增 `"dsh-writing-viewer": "link:…"` 依赖与 `dsh.profile.bundles` 的 `dsh-writing-viewer` 行；`engines.dsh >=0.2.1-rc.2 <0.3.0` 通过检查，无需版本豁免。
- 静态插件行在宿主启动时组合：已运行的 Web GUI 不会自动加载新装或重建后的插件，需重启宿主验证（为不影响正在使用的 GUI，验证用 `dsh web --host 127.0.0.1 --port 13099 --no-open` 另起实例；重建后的插件同样要重启实例才生效）。
- 定位成功态（`docs/evidence/panel-located.png`）：侧栏「Global panels」出现 Writing Workspace 入口，面板定位到注册表中的 `/home/wx766/Code/writing-desk`（`source: workspace-registry`），探测轨迹依次列出无工作区与有工作区的候选；路由原始输出与面板一致。
- 空态（`docs/evidence/panel-empty.png`，经 `dsh web --patch` 覆盖层把 `workspaceRoot` 固定到无工作区目录）：面板显示「未找到」指引与 `wa-cli init` 命令及探测记录，不报错不崩溃。
- 只读验证（`docs/evidence/readonly-check.txt`）：面板两次完整加载（含路由读取）前后，`writing-desk/.writing-anything/` 下全部 41 个条目的 `find -printf` 输出（路径、修改时间、大小）逐字节一致，SHA-256 相同。

### 发现的 DSH API 约束（0.2.1-rc.2）

1. **项目目录来源**：宿主进程 `process.cwd()` 是启动目录（本机为用户主目录），不是工作区根；DSH 工作区注册表（`ctx.workspaceRegistry.list()`，@deepseek-ai/dsh-workspace）才是 GUI 语义下的项目目录清单，root 作用域可注入，`list()` 同步返回带 `path` 的实体。
2. **`workspaceController` 不可静态注入**：`follow()` 基线流挂在按连接的 remotes API 组合上，静态插件行的 `ctx.inject(['workspaceController'])` 闭包不触发（实测路由只回退 `process-cwd`）；读注册表用 `workspaceRegistry`。
3. **client 半区协议**：`window.__ModuleLoader__.load({ id, factory })`，factory 返回 `{ apply, inject }`；`react` 经 factory 的 `require` 获得；`exports.inject` 声明 client 服务（本插件 `['slots', 'sidebarRightTabs']`）——注意这是硬激活门：任一声明服务未提供时整个 client 插件不激活，`engines.dsh` 范围内的宿主均随带 sidebar-right 模块，老宿主装本插件需版本豁免并自担风险。package.json 的 `dsh.client.inject` 是 client 侧 bundle 依赖（包名），不是服务名；`immediately: true` 使页面加载即执行。
4. **全局面板注册**：root-scoped `main` keyed 插槽（`{ name: 'main', key: <面板 id> }` + 组件）+ `sidebar.panellist` 列表条目（同 id，宿主渲染按钮并 `ctx.layout.selectPanel(id)` 选中）；`selectPanel` 对未注册 key 抛错。
5. **profile 补丁行即插件配置**：与插件 insert 行同 id 的 `- id: …, name: …, config: …` 行提供 `apply(ctx, config)` 的 config；`dsh web --patch <overlay.yml>` 可不改 profile 文件叠加配置。
6. **host 路由即同源数据通道**：`ctx.inject(['webServer'], scope => scope.webServer.register({ name, kind: 'exact', path, handler }))` 注册的路由可被 client 以相对路径 `fetch` 访问（沿用 modsearch 模式，实测可用）。
7. **UI 基础组件不作为依赖**：DSH 自带 client 包（如 dsh-client-ui-workspace、dsh-client-ui-trajectory）都经同一加载器 `require('@deepseek-ai/dsh-client-ui-primitives')`，说明该模块在加载器注册表中存在；但它不是面向第三方插件声明的契约，且加载器按需执行注册（`mode: live` 下临时注册的探测 factory 不会被执行），无法在页面上直接验证其导出。本插件只用主题令牌与原生元素，不依赖该模块。
8. **插件路由不在认证栅栏内 → 自带来源校验（Request Origin Guard）**：`webServer.register` 对具名路由直接调用 handler、无认证中间件，宿主的浏览器认证是 `/` 与 `/api` 各自 handler 的私有实现——无 token `GET /` 返回 `401 dsh web authentication required`，而插件路由不经宿主认证。自 plugin-route-origin-guard 起，插件在两条路由的 handler 内实现与宿主 `/api` 信任栅栏（`@deepseek-ai/dsh-client-connection` 的 api-request-trust）同构的请求来源校验：Host 头绑定每个请求（回环字面量 localhost/127.0.0.0/8/::1、宿主监听地址字面量（忽略端口与接口 zone）、或插件配置 `allowedHosts` 的权威——带端口精确匹配、不带端口匹配任意端口，经 WHATWG URL 规范化）；`sec-fetch-site: cross-site` 拒绝；带 Origin 头时必须与 Host 同权威。不满足返回 403 JSON（`cache-control: no-store`），不触达任何读取逻辑。该防线针对浏览器载体攻击（跨站读取、DNS rebinding）；本机进程仍可直接访问，与直接读工作区文件等价——远程暴露请配合 `allowedHosts` 显式放行并置于带认证的反向代理之后。
9. **宿主技能注册表可注入（skills 服务）**：`ctx.inject(['skills'], …)` 拿到 `@deepseek-ai/dsh-skill`（0.2.1-rc.2）的 SkillRegistry；`registerProvider(create)` 注册一个技能提供者（Skill Provider），`create({ signal, invalidate })` 返回 `{ name, list(options), get(candidate, options) }`。契约要点（初版实现曾因忽略而让全部技能被注册表静默跳过，见 docs/evidence/setup-check.txt 的真实注册表取证）：**`list`/`get` 必须返回 Promise**——每个真实消费方都带 AbortSignal 调用，注册表的 waitWithAbort 会对返回值直接调 `.then`，同步返回数组/对象是 TypeError，整个 provider 被捕获并跳过（日志 `skill provider "…" skipped`）；候选须含 `name/description/source/provider/rank/locator`（`provider` 必须等于提供者名，`rank` 为有限数字，`locator` 是回传给 `get` 的不透明句柄），`get` 返回的定义另须 `content`、查不到时 resolve `undefined`。同名候选按 rank 升序取胜，且技能层级为 project `.dsh`=100、project `.agents`=200、custom=300、user `.dsh`=400、user `.agents`=500、运行时注册=250、bundled=600——插件取 650（高于全部层级）即让任何同名外部技能优先、内置作为后备。静态插件行的注册落入全局层，与文件系统源同层按 rank 竞争。注册表 `list()` 的合并清单携带来源标签（project-dsh、project-agents、custom、user-dsh、user-agents、bundled 或提供者名）与 SKILL.md 路径——但静态插件行查询时只见全局层（见第 11 条），插件的技能来源对照表因此不消费该清单，改做标准技能目录存在性比对（见上方 setup 专节）。
10. **宿主子进程环境以宿主 `process.env` 为基底**：`@deepseek-ai/dsh-subprocess` 组装子进程环境时逐键复制宿主 `process.env`（过滤敏感键与 `DSH_*` 前缀）再叠加覆盖项；bash 工具的 PATH 即来自宿主进程。插件在 setup 启用后把内置二进制目录幂等追加到宿主 `process.env.PATH` 末尾，既有顺序的外部 `wa-cli` 保持优先、无外部版本时内置兜底——这是宿主级的进程内副作用（不落盘、不改动用户 shell 配置、只影响本宿主进程之后的子进程），已在上方 setup 专节与规格中如实登记。
11. **web profile 的 dsh-skill-filesystem 挂在 agent preset 层，宿主层同名行被禁用**：`dsh --profile web --dump-config` 显示 dsh-base 顶层的 `skill-filesystem` 行 `disabled: true`，真正启用的一份在 agent preset 的常驻组合里。SkillRegistry 的层合并按「查询带 scope 才含 preset 层」——静态插件行 `ctx.inject(['skills'])` 拿到的注册表 `list({})`（无 scope）只能看到全局层（仅本插件），看不到会话实际加载的 user/project 技能；会话侧经 `agentPresets.serviceFor(live, 'skills')` + `scope` 查询才含全部层（dsh-api-session-controller 实证）。因此插件的技能来源对照表不能用注册表清单，改为标准技能目录存在性比对（见上方 setup 专节）；独立探针（真实 dsh-skill + 真实 dsh-skill-filesystem 同层挂载）证实 rank 竞争与来源标签行为符合预期。
12. **webServer 路由的 handler 拿原生 req/res，支持 prefix 路由**（dsh-host-webserver 0.2.1-rc.2 的类型声明与分发源码）：`register({ kind, path, handler })` 的 handler 收到 node:http 的 `IncomingMessage` 与 `ServerResponse`（可读请求体、可设任意响应头、自持响应生命周期）；`kind: 'exact'` 逐字匹配且路径不得带结尾斜杠，`kind: 'prefix'` 匹配 `p` 与 `p/<任意>`、exact 未命中时最长前缀胜出；宿主可配置 gzip 中间件包裹 res，因此代理不透传上游的 content-length 与 content-encoding。宿主首页不设 CSP，同源 iframe 可内嵌插件路由。审阅代理据此用一条 prefix 路由加子路径白名单实现。

### 总览与导航实测（overview-navigation，2026-10-20）

同一宿主（DSH 0.2.1-rc.2 web profile，`dsh web --host 127.0.0.1 --port 13099 --no-open` 测试实例）重建插件后实测：

- 默认定位（`docs/evidence/overview-default.png`）：面板定位到注册表中的 `writing-desk`，显示 2 个作品卡片、工作区计数与选中作品的单元树和详情（含 7 个产物清单）。
- 路径切换（`docs/evidence/overview-switched.png`）：输入 `testdata/workspace` 副本的绝对路径后，来源显示「panel input」，novel 作品的单元树按 part-1 → ch-1 → scene-1/scene-2 缩进，part 与 ch 带 0/2 进度；作品卡片阻塞项 6（去重后）、待决决策 1、validate 诊断 5（error 5），与 `wa-cli status`/`decisions.yaml`/`wa-cli validate` 一致。
- 无效路径（`docs/evidence/panel-switch-invalid.png`）：切换到没有工作区的目录时显示空态、原因（`该目录下没有 .writing-anything/ 工作区`）与 `wa-cli init` 指引，不显示其他项目数据；「恢复默认」回到注册表定位的 `writing-desk`。
- 窄宽度与主题（`docs/evidence/overview-narrow.png`、`overview-narrow-dark.png`、`overview-narrow-tree.png`、`overview-dark.png`）：900px 视口下 DSH 侧栏收为导轨，面板 `data-layout` 为 `stacked`，单元树与详情上下堆叠；暗色主题下文字、卡片与状态色随 DSH 令牌切换。
- 只读核对（`docs/evidence/readonly-check-overview.txt`）：默认项目 `.writing-anything/` 前后逐字节一致；切换项目的全部文件一致，唯一差异是 wa-cli 新建 `audit.log` 导致的目录修改时间变化，audit.log 共 12 行 = 3 次快照请求 × status/validate/next/timeline 各 1 次；无工作区目录未被写入。
