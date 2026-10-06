# 可重放命令脚本格式（commands.yaml）

本文件是小说语料命令脚本的文件格式规范，单一来源：`corpus-authoring` 按此格式录制创作过程的命令序列，`replay-harness` 按此格式解析并在临时目录重放。设计依据见 `DESIGN.md`；每条命令的期望结果（发现项或全绿）逐条映射到 `matrix.yaml` 的稳定编号。

## 文件与位置

- 文件：`internal/cli/testdata/e2e/novel/commands.yaml`，UTF-8 编码的 YAML。
- 正文与长文本输入一律以 `inputs/` 目录下的文件引用（`input_ref`）提供，不在脚本中内联，保证脚本可读。

## 结构

```yaml
stages:
  - stage: init            # 阶段名（snake_case），仅用于分段与注释，不参与执行语义
    note: 初始化工作区与作品   # 阶段说明（可选，一句话）
    steps:
      - command: ["init", "--genre", "novel", "--gate-level", "strict"]
        input_ref: null
        expect_exit: 0
        expect_code: null
        stdout_contains: null
      - command: ["recall", "四点十七"]
        input_ref: null
        expect_exit: 0
        expect_code: null
        stdout_contains: "ch1-s1"
      - write_file: .writing-anything/works/stopped-at-417/units/ch1-s3/draft.md
        command: null
        input_ref: inputs/ch1-s3-author.md
        expect_exit: 0
        expect_code: null
        stdout_contains: null
```

## 字段定义

| 字段 | 必填 | 含义 |
| --- | --- | --- |
| stage | 是 | 生命周期阶段（如 `init`、`sources`、`brief`、`plan`、`outline`、`scene-contracts`、`draft`、`closeout`、`review`、`volume`、`recall`、`impact`、`handoff`、`assemble`、`export`）。分段注释用，重放器不依赖它改变行为。 |
| note | 否 | 阶段说明，一句话，解释本阶段在写作流程中的位置。 |
| command | 与 write_file 二选一 | wa-cli 参数序列（不含程序名、不含 `--dir`；原命令的 `--file` 已折算为 `input_ref`，文本输入一律经标准输入提供），数组元素均为字符串。 |
| write_file | 与 command 二选一 | 文件级参与点：把 `input_ref` 的内容写入工作区内该相对路径（作者直接编辑正文等），此时 `command` 为 null。 |
| input_ref | write_file 必填；stdin 步骤可选 | 文本来源：相对语料目录的路径（如 `inputs/ch1-s1.md`）；无文本输入时为 null。 |
| expect_exit | 是 | 期望退出码（Exit Code）：0 成功，1 校验或执行失败，2 用法错误。负向步骤（预期被拒绝的命令）以非 0 记录，同样真实发生在创作流程中。 |
| expect_code | 否 | 期望诊断（Diagnostic）错误码（如 `volume.unconfirmed`）：失败步骤必填，成功步骤为 null；逗号分隔多个码时全部都必须出现在诊断中（如 `assemble.scenes-open,assemble.rechecks`）。 |
| stdout_contains | 否 | 可选的 stdout 包含断言（子字符串），供 `recall`、`threads`、`timeline` 等只读命令断言关键输出；无断言时为 null。 |

## 约定

- 重放器（`replay-harness`）负责：在临时目录以 `--dir <tmp>` 执行；补 `--json`（解析输出信封时）；冻结时间源（`now` 固定，与 `TestTechBlogEndToEnd` 相同模式）；逐步断言 `expect_exit`、`expect_code` 与 `stdout_contains`，关键阶段校验 changedPaths；`write_file` 步骤按声明写入工作区文件。
- `expect_exit` 非 0 的步骤是创作流程真实发生过的拒绝（人工编辑保护 `draft.human-edited`、部未确认时验收被 `volume.unconfirmed` 阻塞、open 复查标记与未完成场景下组稿被拒、悬空伏笔阻塞组稿 `assemble.continuity`、未经授权放弃伏笔 `threads.author-confirmation`），不是虚构的失败注入；临时注入违例属于 `golden-assertions`，不进本脚本。
- 脚本一次录制、只追加不重排：修订语料时按阶段追加或替换步骤后，同步更新 `matrix.yaml` 的对应条目与 `golden/` 黄金文件。
- 命令中的中文文本（标题、确认原话等）允许内联；只有正文与长文本输入必须走 `input_ref`。
