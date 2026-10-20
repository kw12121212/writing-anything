# 里程碑 dsh-workspace-viewer 收尾对账（polish-release，2026-10-20）

对照 `.strict-spec-driven/roadmap/milestones/dsh-workspace-viewer.yaml` 的 done_criteria 逐条核对。
实测环境：DSH 0.2.1-rc.2 web profile（link: 安装本插件），另起 `dsh web --host 127.0.0.1 --port 13099 --no-open` 测试实例；键盘与 DOM 断言经 CDP 执行。

## 逐条对账

1. **「插件经 link: 依赖装入本机 web profile 后，Web GUI 出现工作区查看面板，能浏览一个真实写作项目」**
   - 达成。plugin-scaffold 实测见 README「安装与加载」与 `panel-located.png`（profile 登记 `dsh-writing-viewer@link:…`，侧栏出现 Writing Workspace 入口）。
   - polish-release 实测：同 profile、新实例下打开面板浏览了默认项目 `writing-desk`（2 个 tech-blog 作品）与合成长篇 `big-novel`（`polish-large-workspace.png`）。

2. **「总览、单元树导航、产物语义视图可用；YAML 可在结构化视图与源码高亮视图间切换；Markdown 正文既有渲染阅读视图也有源码高亮」**
   - 达成。overview-navigation 实测（`overview-default.png`、`overview-switched.png`、`overview-narrow-tree.png`）与 artifact-views 的 vitest 覆盖（`client.test.ts` 产物视图组、`artifactview.test.ts`、`yamlhighlight.test.ts`、`markdownview.test.ts`）。
   - polish-release 增补键盘导航实测：面板内仅用键盘 ↑/↓ 两次把选中从根单元移到 `big-novel/ch-1-1`（DOM 焦点与 `aria-current` 同步为 `big-novel/ch-1-1`，详情区跟随），← 收起该章（可见节点 220 → 212，`aria-expanded="false"`，焦点保持），→ 重新展开（220）；截图 `polish-keyboard-tree.png`。

3. **「快照服务不含任何写路径，测试覆盖读取逻辑与只读约束；Go 完整回归不受影响仍通过」**
   - 写路径：`readonly.test.ts` 断言快照聚合只用只读 fs；本变更加做大工作区只读核对——面板多次完整快照（含路径切换与刷新）后 `/tmp/dsh-large-ws/.writing-anything` 内除 wa-cli 自身追加的 `audit.log`（有界命令集合：status、validate、next、timeline）外无任何文件新增或变化，与既有 `readonly-check.txt`、`readonly-check-overview.txt` 结论一致。
   - 读取逻辑测试：`snapshot.test.ts`、`host.test.ts`、`wacli` 桥降级与定位测试全部通过（`pnpm run check`）。
   - Go 完整回归：在 polish-release 的 `go-regression` 任务执行，结果见下「回归记录」。

4. **「安装与使用文档就绪，新增术语已按首次出现规则写入 docs/GLOSSARY.md」**
   - 文档：README 已含安装（link:）、启用与重启说明、与 wa-cli 版本的兼容说明、界面导览（含截图索引）与开发检查命令（`check`、`perf:check`）。
   - 术语：前置变更已把 DSH 插件（DSH Plugin）、工作区快照（Workspace Snapshot）、总览仪表盘（Overview Dashboard）、语义卡片视图（Semantic Card View）、源码高亮视图（Source Highlight View）写入 `docs/GLOSSARY.md`；polish-release 未引入新的专有名词（「加载提示」「键盘导航」为通用界面用语，非项目术语），无需新增词条——本行即核对结论。

## 大工作区性能检查记录

`docs/evidence/perf-check.txt`：220 单元（3 卷×8 章×8 场景）合成长篇，host 聚合 244ms（4 次 wa-cli 调用），总览模型 35ms，首屏渲染 2ms（1188 渲染节点），单元切换约 0.4ms/次，1242 行 YAML 折叠源码视图 20ms——无不可交互级卡顿，未触发渲染层优化（按设计决策 perf-render-layer-only）。

## 回归记录

- 插件统一检查：`pnpm --dir plugins/dsh-writing-viewer run check` 通过（类型检查 + 构建 + 154 项测试；性能探针在无 `LARGE_WS_DIR` 时按设计跳过）。
- Go 完整回归：2026-10-20 通过——`go vet ./...`、`go build ./...`、`go test -coverprofile=coverage.out ./...` 全部 ok，covercheck 覆盖率 87.71%（阈值 80%）。

## 结论

四条 done criteria 全部达成，无遗留缺口；里程碑可随 polish-release 归档收口。
