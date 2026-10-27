# 测试夹具（Test Fixture）

`workspace/` 是一个真实的多作品工作区（Workspace）：用仓库根的 `wa-cli`
（`go run ./cmd/wa-cli`，版本随仓库）以下列命令序列生成后拷贝而来，不含
`audit.log`（运维痕迹，非产物（Artifact））。夹具保持真实命令产物的原因：
手写 YAML 夹具会在第一次面对真实命令序列时暴露缺陷。

- `wa-cli init --genre tech-blog --gate-level strict`
- `wa-cli new novel my-novel` + `unit add`（part/chapter/scene 层级）
- `wa-cli canon add`（character、location）、`threads add`、
  `state propose`、`knowledge propose`、`scene contract`（两个场景，含
  time/after 与伏笔埋设/回收）
- `wa-cli source add`（past-article 与 author-note 各一份）
- `wa-cli new tech-blog my-post` + `brief set`（含素材关联）、
  `outline set`（含 role）、`claim add`（data，关联亲历素材）、
  `draft write`、`feedback add`
- 被拒绝的 `claim add`（data 关联非亲历素材）在决策队列（Decision Queue）
  留下待决的阻塞决策 `dec-1`
- `wa-cli pack install`（本地规范包（Pack）lean-style）+ `config set packs`
- `wa-cli handoff write`

唯一手写的产物：`author-reviews.yaml`（复核记录（Author Review Record））——
它只能由阅读页（Reading View）的批量复核写入，无法用命令行生成；按其 Schema
手写一条最小合法记录（acceptance、channel 为 preview），覆盖快照读取它的
正路径。

注意：真实 wa-cli 每次调用（含只读命令）都会向 `.writing-anything/audit.log`
追加运维痕迹（见仓库 cli 契约的审计日志（Audit Log）例外）；本夹具已剔除
audit.log，快照经子进程调用 wa-cli 时该文件会重新出现，属预期行为而非插件
写入。

两个作品（Work）的大纲（Outline）留有校验诊断（outline.missing-reason、
outline.missing-method-part），用于覆盖快照内嵌 `wa-cli validate --json`
诊断的错误态数据。

`cli/` 是对同一工作区用真实命令捕获的 `--json` 输出（绝对路径替换为
`<root>`），供测试注入假 CLI 执行器（Fake Runner），不需要测试机安装
wa-cli。
