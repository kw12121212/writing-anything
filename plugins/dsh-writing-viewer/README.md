# dsh-writing-viewer（插件骨架）

DSH 插件（DSH Plugin）`dsh-writing-viewer` 是 writing-anything 仓库的 DSH 工作区查看插件的骨架面板：只读定位一个包含 `.writing-anything/` 工作区（Workspace）的项目目录，在 DeepSeek Harness（DSH）Web GUI 的全局面板中报告定位结果；定位不到时给出 `wa-cli init` 空态指引。它不展示工作区内容——总览、单元树与产物视图由本里程碑（`dsh-workspace-viewer`）的后续变更在同一包内扩展。

- 严格只读：host 侧唯一的文件系统操作是对 `<候选目录>/.writing-anything` 的 `stat`；不提供任何写入工作区的能力。
- 不属于产品运行时：wa-cli（Go 内核）、内置技能与 MCP 服务不依赖本包；本包也不进入 Go 模块与覆盖率范围。

## 结构

```
cordis.patch.yml   bundle patch：向 DSH profile 组合插入插件行
src/host.ts        host 入口（cordis 插件：导出 name 与 apply(ctx, config)）
src/client.ts      web client 入口（window.__ModuleLoader__.load 工厂）
src/root.ts        工作区定位的纯逻辑（候选顺序、去重、探测）
src/registry.ts    DSH 工作区注册表（workspaceRegistry.list()）的容错读取
dist/index.js      构建产物：host 入口（package.json 的 "." 导出）
dist/client.js     构建产物：client 入口（package.json 的 "./client" 导出）
```

package.json 声明：

- `dsh.bundle.patch` → `./cordis.patch.yml`（insert 一个插件行）；
- `dsh.client` → `{ inject: [], platform: "web", immediately: true }`；
- `engines.dsh` → `>=0.2.1-rc.2 <0.3.0`（实测验证用的宿主为 DSH 0.2.1-rc.2）。

## host 侧行为

`apply(ctx, config)` 通过 scoped `ctx.inject` 获取两个可选服务，宿主没有它们时插件保持安静：

- `workspaceRegistry`（DSH 工作区注册表，root 作用域）：每次请求同步 `list()` 全部已注册工作区的 `path`（0.2.1-rc.2 实测：`workspaceController.follow` 挂在按连接的 remotes API 组合上，静态插件行的 `ctx.inject` 闭包不触发，不可用）。
- `webServer`（web profile）：注册只读 GET 路由 `/dsh-writing-viewer/workspace`，返回 JSON：

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

## client 侧行为

以 lazy-CJS 协议（`window.__ModuleLoader__.load`，factory 返回 `{ apply, inject }`）注册两个插槽（slot）条目：

- root-scoped `main` keyed 面板，key 为 `dsh-writing-viewer`（`ctx.layout.selectPanel('dsh-writing-viewer')` 可选中）；
- `sidebar.panellist` 列表条目，id 同为 `dsh-writing-viewer`，侧栏按钮由宿主渲染并在点击时选中面板。

面板拉取上述路由并渲染：定位成功态（项目目录、来源、工作区目录、探测记录）、空态（`wa-cli init` 指引）、加载失败态（重试）。文案中英双语，按页面语言选择。

## 开发、构建与测试

需要 Node ≥ 22.13 与 pnpm。所有命令在包目录内运行：

```sh
pnpm install            # 安装开发依赖
pnpm run build          # 类型检查（tsc --noEmit）+ 构建（tsdown）+ 产物名归一
pnpm test               # vitest 单元测试（定位逻辑、注册表读取、路由、面板注册）
```

构建产物名归一（`scripts/normalize-outputs.mjs`）把 tsdown 的 `host.js`/`client.iife.js` 改名为 `dist/index.js`/`dist/client.js`，与 package.json 的 `exports` 对齐；缺少任一产物时构建失败。

## 装入本机 DSH web profile

以 link: 依赖安装（改动源码后重新 `pnpm run build`，无需重装）：

```sh
pnpm --dir plugins/dsh-writing-viewer run build
dsh plugin --profile web add link:/home/wx766/Code/writing-anything/plugins/dsh-writing-viewer
```

`dsh plugin add` 在 profile 的 package.json 中同时登记依赖与 `dsh.profile.bundles`。插件行在宿主启动时组合生效——已经运行的 Web GUI 不会自动加载新装的插件，需重启宿主或在另一端口启动一个新实例验证（避免重启正在使用的 GUI 进程）。

按 DSH 版本验证兼容性：`engines.dsh` 不满足运行中的 DSH 版本时，安装会要求版本豁免（`dsh plugin --profile web allow-version …`）；本骨架按 0.2.1-rc.2 实测，范围声明为 `>=0.2.1-rc.2 <0.3.0`。

如需固定展示某个项目目录，在该 profile 的 `cordis.patch.yml` 中为插件行加配置：

```yaml
- id: dsh-writing-viewer
  name: dsh-writing-viewer
  config:
    workspaceRoot: /home/user/Code/writing-anything
```

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
3. **client 半区协议**：`window.__ModuleLoader__.load({ id, factory })`，factory 返回 `{ apply, inject }`；`react` 经 factory 的 `require` 获得；`exports.inject` 声明 client 服务（本插件 `['slots']`）。package.json 的 `dsh.client.inject` 是 client 侧 bundle 依赖（包名），不是服务名；`immediately: true` 使页面加载即执行。
4. **全局面板注册**：root-scoped `main` keyed 插槽（`{ name: 'main', key: <面板 id> }` + 组件）+ `sidebar.panellist` 列表条目（同 id，宿主渲染按钮并 `ctx.layout.selectPanel(id)` 选中）；`selectPanel` 对未注册 key 抛错。
5. **profile 补丁行即插件配置**：与插件 insert 行同 id 的 `- id: …, name: …, config: …` 行提供 `apply(ctx, config)` 的 config；`dsh web --patch <overlay.yml>` 可不改 profile 文件叠加配置。
6. **host 路由即同源数据通道**：`ctx.inject(['webServer'], scope => scope.webServer.register({ name, kind: 'exact', path, handler }))` 注册的路由可被 client 以相对路径 `fetch` 访问（沿用 modsearch 模式，实测可用）。

