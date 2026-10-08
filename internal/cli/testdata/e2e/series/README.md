# 系列语料《雾港灯火》（series E2E corpus）

系列小说（Novel Series）端到端（End-to-End，E2E）测试语料：一个系列（Series）`harbor` 含两部 novel 作品，按 wa-cli 完整生命周期一次性真实创作，配合固定命令序列在临时目录重放，以黄金文件与负向注入验证系列级设定、全局时间线与跨作品连续性关卡，以冻结时间源与固定语料保证可重复性。重放、黄金比对与矩阵对账与小说语料（`../novel/`）共用同一套测试助手（`replayCorpus`、`compareCorpusGoldens`、`reconcileCorpusMatrix`），命令脚本格式见 `../novel/script-format.md`。

## 语料设计

| 作品 | 单元 | 故事时间（time） | after | 视角 / 出场人物 | 要点 |
| --- | --- | --- | --- | --- | --- |
| 第一部《雾港》（fog-harbor） | part-1/ch-1/s1 点灯 | 1998-10-03 黄昏 | — | 阿宁 / 阿宁、陆山 | 陆山第一次让阿宁点灯，立下三十秒雾笛的规矩 |
| | part-1/ch-1/s2 雾散 | 1998-10-04 黎明 | fog-harbor/s1 | 阿宁 / 阿宁、陆山、周海生 | 船循笛声归港；陆山辞世，状态「已亡故」经作者确认 |
| 第二部《归帆》（homeward） | part-1/ch-1/s1 归帆 | 2008-10-03 黄昏 | fog-harbor/s2（跨作品） | 阿宁 / 阿宁、陆归帆 | 十年后陆山之子回到灯塔 |
| | part-1/ch-1/s2 日志 | 2008-10-04 黎明 | homeward/s1 | 阿宁 / 阿宁、陆归帆 | 雾笛故障，两人用怀表数秒；陆归帆「留在雾港」经作者确认 |

- 系列级：素材 `series-note`；两条系列事实（`fact-lighthouse`、`fact-ten-years`）；系列级共享设定集 `.writing-anything/series/harbor/canon.yaml` 含世界规则 `rule-fog-horn`（三十秒雾笛）与物品 `item-logbook`（值守日志）。
- 作品级覆盖：第二部以同 id 的 `rule-fog-horn`（电动雾笛二十秒）覆盖系列级规程。脚本先录制缺少覆盖原因与作者确认时被 `canon.override-unconfirmed` 拒绝的真实负向步骤，再带 `--override-reason`、`--confirmed-by-author` 与 `--confirmation` 覆盖；覆盖记录（reason、confirmation、confirmed_at）写入第二部 canon.yaml。
- 人物与地点在两部作品各自的设定集登记：状态台账（State Ledger）、知情台账与写后收口（Close-out）只读取作品级设定集，系列级人物无法登记终态、也会在收口时被提示为疑似专有名词。跨作品已死人物检查按称谓在各作品设定集中解析，作品级人物即可覆盖。
- 第二部的上下文包（Context Pack）读取系列事实与第一部根摘要（`context.series`）。
- 每场正文 1000–1500 个非空白字符，对话使用全角引号“”，视角为第三人称。

## 目录结构

| 路径 | 职责 | 主要消费方 |
| --- | --- | --- |
| `commands.yaml` | 分阶段命令脚本（init → sources → series → fog-harbor → homeward → series-wrap），含一条真实负向步骤 | `TestSeriesCorpusScript`、`TestSeriesCorpusReplay`、`TestSeriesCorpusMatrix` |
| `matrix.yaml` | 测试矩阵：带稳定编号（S-<特性>-<序号>）的系列特性面与负向条目，每条带 evidence 对账锚点 | `TestSeriesCorpusMatrix` |
| `inputs/` | 三份素材、四个场景草稿、收口与审稿载荷、两份交接记录（Handoff Note） | 重放器按 `input_ref` 引用 |
| `golden/` | 十一个黄金文件：系列文件、系列与第二部设定集、canon list 解析视图、series show、系列时间线、系列连续性、第二部场景上下文包、两部组稿与第二部导出 | `TestSeriesCorpusGolden`、`TestSeriesCorpusMatrix` |

## 运行方式

```bash
# 五个系列语料测试（internal/cli 包内，约 2 秒）
go test ./internal/cli/ -run "TestSeriesCorpus" -v
#   TestSeriesCorpusScript              脚本结构、规模、负向步骤与命令面
#   TestSeriesCorpusReplay              全量重放两次，目录树逐字节一致；覆盖记录带冻结时钟
#   TestSeriesCorpusGolden              十一个黄金对象逐字节比对
#   TestSeriesCorpusNegativeInjections  重放到 homeward/s2 验收之前，注入跨作品违例
#   TestSeriesCorpusMatrix              矩阵 evidence 双向对账、编号格式与特性面

# 完整回归（语料测试包含在内；含覆盖率阈值 80%）
go vet ./... && go build ./... && go test -coverprofile=coverage.out ./... \
  && go run ./internal/tools/covercheck -min 80 -profile coverage.out
```

负向注入在第二部最后一个场景处于审稿阶段、验收之前进行（`replayCorpus` 的 stopBefore 参数），对同一个真实场景逐类注入并修回：

| 注入 | 期望 |
| --- | --- |
| 第二部场景出场人物加入第一部已确认亡故的陆山 | `continuity.dead-acting`（error，带 [系列] 标记并指向 fog-harbor/s2），`accept homeward/s2` 退出码 1 |
| 第二部把陆山确认为另一状态「失踪」 | `continuity.conflicting-state`（error），验收退出码 1 |
| 第二部末场改为与第一部末场相同的故事时间并去掉 after | `timeline.same-time-presence`（error，带 [系列] 标记并列出两部作品的场景），验收退出码 1 |
| 第一部首场声明晚于第二部末场（跨作品成环） | `wa-cli timeline harbor` 报 `timeline.cycle` 与 `timeline.order-conflict`（退出码 1），系列连续性审查携带 error 级发现 |

每类修回后系列连续性审查全绿，最后验收成功。跨作品成环与声明矛盾不在场景验收关卡的系列错误码之列（见 continuity 主规格 continuity-series-accept-gate），本语料只断言其 error 级发现。

## UPDATE_GOLDEN：黄金文件再生成

```bash
UPDATE_GOLDEN=1 go test ./internal/cli/ -run TestSeriesCorpusGolden
```

比对前唯一归一化：命令输出中的临时目录绝对路径替换为 `<dir>` 占位符。时间戳在冻结时间源下恒定（技术规范 injectable-clock），文件字节黄金不做任何规范化。

## 修订语料的完整步骤

1. 改设定或正文：`inputs/` 下的素材、草稿、收口与审稿载荷、交接记录，或本文件「语料设计」一节。
2. 若改了命令序列：同步 `commands.yaml`（正文输入一律走 `input_ref`；负向步骤只录真实发生过的拒绝；第二部末场验收的命令串须仍以 `accept homeward/s2 ` 开头且唯一，它是负向注入的停止点）。
3. `go test ./internal/cli/ -run TestSeriesCorpusScript`——结构、规模与负向步骤先过。
4. `go test ./internal/cli/ -run 'TestSeriesCorpus(Replay|NegativeInjections)'`——重放、双跑一致性与注入必须全绿。
5. `UPDATE_GOLDEN=1 go test ./internal/cli/ -run TestSeriesCorpusGolden` 再生成黄金文件，逐个审阅差异后检入。
6. `go test ./internal/cli/ -run TestSeriesCorpusMatrix`——矩阵对账必须全绿：新增断言时先加矩阵条目（含 evidence），再落断言。
7. 跑完整回归命令。

## 矩阵维护约定

同小说语料（`../novel/README.md`「矩阵维护约定」）：`TestSeriesCorpusMatrix` 做双向对账——每条矩阵条目的 evidence 必须解析到真实存在的断言；全部黄金文件、`TestSeriesCorpusNegativeInjections` 源码中 `diagError(res, "…")` 与 `codes["…"]` 断言的全部诊断码、脚本中全部带断言的步骤，都必须被至少一条矩阵条目引用。负向断言只对应产品真实存在的确定性检查。
