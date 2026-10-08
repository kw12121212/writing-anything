# 小说语料《停在四点十七分》（novel E2E corpus）

真实小说端到端（End-to-End，E2E）测试语料：一部按 wa-cli 完整生命周期一次性真实创作的中文中篇
（3 部各 1 章、每章 3 场景，九场景正文合计 1.34 万字（rune 计数）），配合固定命令序列在临时目录重放长篇写作生命周期（Lifecycle），以黄金文件与负向断言保证可验证性，以冻结时间源与固定语料保证可重复性。

系列小说语料《雾港灯火》见 `../series/README.md`：两部作品覆盖系列级设定、全局时间线与跨作品连续性关卡，与本语料共用重放（`replayCorpus`）、黄金比对（`compareCorpusGoldens`）与矩阵对账（`reconcileCorpusMatrix`）助手，维护流程同下。

## 目录结构

| 路径 | 职责 | 主要消费方 |
| --- | --- | --- |
| `DESIGN.md` | 小说设定设计：人物卡、场景表（时间线（Timeline）与跨章倒叙）、秘密与知情点（Knowledge Point）、伏笔链、状态变更、雪花写作法（Snowflake Method）十步映射、负向断言说明 | 人（维护者）；`TestNovelCorpusDesign` 校验结构锚点 |
| `matrix.yaml` | 测试矩阵：49 条带稳定编号（T-XXX-NN）的特性映射与负向条目，每条带 evidence 对账锚点 | `TestNovelCorpusDesign`（结构）、`TestNovelCorpusMatrix`（对账） |
| `script-format.md` | 命令脚本格式规范：stage/command/write_file/author_review/input_ref/expect_exit/expect_code/stdout_contains 字段定义 | 人；`TestNovelCorpusDesign` 校验字段定义、录制（corpus-authoring 已完成）与重放器共用契约 |
| `commands.yaml` | 287 步分阶段命令脚本（一次性真实创作录制，含一次作者直编 write_file 步骤、一次阅读页批量复核 author_review 步骤与六类七码真实负向步骤） | `TestNovelCorpusScript`（结构与哈希承诺）、`TestNovelCorpusReplay`（行为重放）、`TestNovelCorpusMatrix`（断言对账） |
| `inputs/` | 全部文本输入：三份作者素材（Source）、九个场景草稿（每场 1400–2000 字）、作者直编与合并终稿、九份写后收口（Close-out）、审稿（Review）记录与审查任务包（Review Task Packet）结论、三份部末交接记录（Handoff Note） | 重放器按 `input_ref` 引用 |
| `golden/` | 九个黄金文件：knowledge.yaml、threads.yaml、book.md、novel-export.md、novel-export.txt（文件字节）与 state-at-ch3-s3.json、continuity.json、context-ch3-s2.json、review-report.json（命令输出） | `TestNovelCorpusGolden`（逐字节比对）、`TestNovelCorpusMatrix`（反向引用对账） |

## 运行方式

```bash
# 六个语料测试（internal/cli 包内，约 8 秒）
go test ./internal/cli/ -run "TestNovelCorpus" -v
#   TestNovelCorpusDesign             设计文档结构锚点（人物/场景/伏笔/知情/倒叙/十步）
#   TestNovelCorpusScript             命令脚本结构与 --base 哈希承诺
#   TestNovelCorpusReplay             280 步重放 + 两次重放目录树逐字节一致
#   TestNovelCorpusGolden             九个黄金对象逐字节比对
#   TestNovelCorpusNegativeInjections 四类注入违例与恢复全绿
#   TestNovelCorpusMatrix             矩阵 evidence 双向对账

# 完整回归（语料测试包含在内；含覆盖率阈值 80%）
go vet ./... && go build ./... && go test -coverprofile=coverage.out ./... \
  && go run ./internal/tools/covercheck -min 80 -profile coverage.out
```

## UPDATE_GOLDEN：黄金文件再生成

修订语料后黄金文件必然失配，一键再生成：

```bash
UPDATE_GOLDEN=1 go test ./internal/cli/ -run TestNovelCorpusGolden
```

比对前唯一归一化：命令输出中的临时目录绝对路径替换为 `<dir>` 占位符（实际只出现在诊断的 next 命令里）。
时间戳在测试冻结时间源下恒定（见技术标准 injectable-clock），文件字节黄金不做任何规范化。

## 修订语料的完整步骤

1. 改设定或正文：`inputs/` 下的草稿、素材、收口载荷，或 `DESIGN.md` 的设计（场景表、伏笔链、知情点）。
2. 若改了命令序列：同步 `commands.yaml`（正文输入一律走 `input_ref`；负向步骤只录真实发生过的拒绝）。
3. `go test ./internal/cli/ -run TestNovelCorpusScript`——结构、字段、input_ref 与 `--base` 哈希承诺先过。
4. `go test ./internal/cli/ -run TestNovelCorpusReplay`——重放与双跑一致性必须全绿。
5. `UPDATE_GOLDEN=1 go test ./internal/cli/ -run TestNovelCorpusGolden` 再生成九个黄金文件并检入。
6. `go test ./internal/cli/ -run TestNovelCorpusMatrix`——矩阵对账必须全绿（见下）。
7. 跑完整回归命令。

## 矩阵维护约定

矩阵是断言意图的唯一登记处，`TestNovelCorpusMatrix` 做双向对账：

- 正向：每条矩阵条目的 `evidence` 必须解析到真实存在的断言——`script`（commands.yaml 步骤 token，
  可选 `stdout_contains`/`expect_code` 断言值）、`golden`（golden/ 文件）、`negative`（负向注入
  断言码）、`structure`（结构校验测试名）。
- 反向：全部黄金文件、负向测试断言的全部诊断码、脚本中全部带断言的步骤，都必须被至少一条
  矩阵条目引用；无断言过滤的宽 token 只作存在性锚点，不登记反向引用。

新增长篇特性或新增断言时：先加矩阵条目（含 evidence），再落断言；对账失败会给出缺失项清单。
负向断言必须对应真实存在的确定性检查，产品没有的检查如实记录缺口而不是伪造 error 断言——
知情违例的 prose 级检查缺口即按此原则先记录、后由 knowledge-reveal-terms 变更补齐
（知情事件 --terms 声明揭示词，continuity 报 continuity.knowledge-early）。
