---
name: wa-illustrate
description: 可选的配图环节：作者需要时，用外部风格库 lemo-opuscar 按定稿正文渲染封面图与正文配图，经配图计划确认后登记到单元并插入正文，形成图文成稿。修订完成、请作者验收之前，作者要配图时使用。
---

# wa-illustrate：配图（Illustration）

配图是可选环节：作者不要配图时直接回到 wa-publish 验收。wa-cli 不生成、不渲染图片，只登记、校验、锁定与导出；渲染由你在会话中用外部风格库 lemo-opuscar 完成（它用代码把画面画出来，不调用图像生成模型）。

## 步骤

1. 确认单元已完成修订：`wa-cli review show <单元> --json` 的 revisions 为空。运行 `wa-cli illustration list <单元> --json` 查看已登记的配图（Illustration）与封面图（Cover Image）；封面只登记在作品根单元，用 `wa-cli illustration list <作品> --json` 查看。
2. 定位 lemo-opuscar：依次查看环境变量 LEMO_OPUSCAR_HOME 与默认位置 ~/lemo-opuscar，库根目录下应有 `core/render/still.mjs` 与 `styles/README.md`；还需要 Node 20 以上版本（`node --version`）。找不到时告诉作者安装方式——克隆其仓库到 ~/lemo-opuscar，或安装它的 Claude Code 插件（`claude plugin marketplace add lemomo-ai/lemo-opuscar`、`claude plugin install lemo-opuscar@lemolab`）——之后在库根目录运行 `sh plugin/skills/lemo-opuscar/scripts/setup.sh deps` 安装渲染依赖。作者不同意安装时跳过整个配图环节，回到 wa-publish。
3. 请作者选风格：把 lemo-opuscar 风格索引 `styles/README.md` 中的风格（中文或英文名）与作者自己保存的风格（`~/lemo-my-styles/README.md`，存在时列在最前）交给作者，推荐两三个与文章气质相符的，由作者选定。之后读所选风格的 `styles/<风格>/STYLE.md`，渲染时遵守其中的风格不变量（画面、配色、字体）；故事与构图来自文章本身，不照搬样片。
4. 提出配图计划（Illustration Plan）并请作者确认：
   - 封面：表现什么、主体放在哪里；公众号封面按 2.35:1（如 1800×766）构图，主体居中，以便编辑器裁出方形分享图。
   - 每张正文配图：放在哪一段之后（引用段落原文的开头）、表现什么、是氛围图（Mood Image，content 为 mood）还是信息图（Infographic，content 为 infographic）。
   - 信息图只能呈现已登记论断（Claim）或素材（Source）中的数据与事实：用 `wa-cli claim list <单元> --json` 与 `wa-cli source list --json` 找到出处，在计划中写明关联的论断或素材 id；没有出处的数据不画进图里，先按 wa-draft 登记论断。
   用 harness 提问工具请作者确认计划，并按会话协议用 `wa-cli decision add --question <问题> --unit <单元> --json` 与 `wa-cli decide <决策 id> --option <选项> --answer <答复> --confirmation <作者原话摘要> --json` 回写决策队列。作者要求修改时改好计划再确认。
5. 渲染：在工作区 `.writing-anything/` 之外（如项目目录下的 `illustrations-work/` 或系统临时目录）为每张图写一个按所选风格绘制的页面（遵守 lemo-opuscar 的页面约定：`window.READY`、`window.render(t)`），在库根目录运行 `node core/render/still.mjs <页面目录> <时刻> --size <宽x高> --out <输出目录>` 渲染静帧（尺寸为偶数；正文配图如 1600x900，封面如 1800x766）。自查每张图与计划、风格一致，图中文字不超出画面。
6. 登记：对每张正文配图运行 `wa-cli illustration add <单元> --id <配图 id> --file <静帧文件> --alt <替代文字> --origin agent --tool lemo-opuscar --style <风格> --content <mood|infographic> --json`，信息图另加 `--claims <论断 id>` 或 `--sources <素材 id>`；封面在作品根单元登记：`wa-cli illustration add <作品> --id cover --role cover --file <静帧文件> --alt <替代文字> --origin agent --tool lemo-opuscar --style <风格> --json`。只有作者亲自提供的图片（如店内实拍）才用 `--origin author`。图片必须是不超过 10 MiB 的 PNG 或 JPEG。
7. 插入正文：add 输出的 `illustration.markdown` 是可直接插入正文的引用（如 `![店内一角](illustrations/fig-1.jpg)`），把它作为独立的一段放在计划的段落之后，用 `wa-cli draft write <单元> --file <文件> --json` 写入；作者改过正文时（`draft.human-edited`）先运行 `wa-cli diff <单元> --json`，基于作者的版本插入，再加 `--base <diff 输出的 base>` 写入。封面不进正文。插入引用会改变正文，原来的审稿（Review）随之过期：只插入了配图引用、没有改动文字时，运行 `wa-cli review record <单元> --reaffirm --summary <变化摘要，如「插入 3 张配图引用」> --json` 沿用原来的审稿结论；改动了文字时按 wa-review 重新审稿。只含配图引用的段落不计入 AI 参与说明的段落与字数，配图由配图说明单独标注。
8. 运行 `wa-cli validate --json`，确认没有 illustration.* 诊断（未登记的引用、图片被改动、信息图出处悬空都会阻塞验收）；`wa-cli illustration list <单元> --json` 中每张配图的 problems 都为空、正文配图的 referenced 为 true。
9. 交回 wa-publish 请作者在阅读页审看图文并验收。作者对某张图不满意时，按意见重新渲染，用 `wa-cli illustration add <单元> --id <原配图 id> --file <新静帧> --alt <替代文字> --origin agent --replace --json` 原位替换：格式不变时路径不变，正文不用改；格式变化时按输出的 previousPath 用 draft write 更新引用。不再需要的配图用 `wa-cli illustration remove <单元> <配图 id> --json` 删除，并去掉正文中的引用。验收会锁定配图：验收后再增删或替换配图、封面，验收即失效，需要作者重新确认。

## 会话协议（可执行模板）

把下面的模板复制进会话的 shell，之后按步骤逐条运行：失败即停，读诊断（Diagnostic）的 next 修复后重试；绝不 `|| true`、管道或输出重定向吞掉失败（shell 层吞错对状态机不可见，只能事后靠 stale 暴露）。

```bash
set -euo pipefail
wa() { local out; out=$(wa-cli "$@") || { printf '%s\n' "$out" >&2; return 1; }; printf '%s\n' "$out"; }
wa next --json   # 示例：每步先看下一步；把 wa 后面换成当前步骤的 wa-cli 命令与参数
```

经 harness 提问工具得到作者的决定后，同步回写决策队列（Decision Queue）保住审计链：先用 `wa-cli decision add --question <问题> --unit <单元> --json` 登记问题，作者答复后用 `wa-cli decide <决策 id> --option <选项> --answer <答复> --confirmation <作者原话摘要> --json` 记录决定；不要只在对话里问完就往下走。

## 必须停下来问作者

- 要不要配图、选哪个风格、配图计划（每张图放在哪里、画什么），以及是否安装 lemo-opuscar：都只能在作者明确同意后进行，不要替作者决定。
- 以作者名义登记图片（`--origin author`）只限作者亲自提供的图片；Agent 渲染的图一律 `--origin agent`，不要为了少一句 AI 参与说明而改写出处。
- 信息图中的数字与事实必须来自已登记的论断或素材；作者没有提供出处的数据不要画进图里。
- 验收由作者确认图文整体：不要替作者运行 accept，交回 wa-publish 处理。
