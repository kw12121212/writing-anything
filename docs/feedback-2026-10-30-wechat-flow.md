# 公众号写作流程复盘（2026-10-30）

来源：`writing-desk` 项目里两轮真实使用后的复盘。第一轮由 Agent 按假设一路走完（作品 `moments-ladder`），第二轮同一选题走真流程、每道作者关卡都停下来等作者拍板（作品 `moments-ladder-v2`）。环境：wa-cli v0.8.0（commit 8e8d091c），文体 `wechat-marketing`，规范包 default + plain-talk，关卡 standard，导出 `markdown` 与 `wechat-html`。

结论先行：**四道作者关卡是这个流程的核心价值，一道都不该减**。要改的是两处顺序（标题候选前置、验收绑死正文哈希）、一处审计口径（假设确认与真确认不可区分），以及导出与审稿两个每天都在浪费时间的实现缺口。下面按「流程 / 导出 / 审稿与质检 / 审计与确认 / 不建议改」分层，每条给现象、证据、影响与建议。

## 一、流程

### F1 标题候选被迫前置（P1）

**现象**：`marketing` 是 draft 阶段的必需产物，正文一个字没写就得先交至少两个标题候选；`marketing ask` 立刻生成阻塞决策，作者在空白稿前面选标题。

**证据**：`wa-cli next moments-ladder --json` 在 draft 阶段同时报 `draft` 与 `marketing` 两个缺失产物；实际执行是「先 `marketing set` 四个候选 → 写正文 → `marketing ask`」才绕过去。

**影响**：标题是全文最后才定得准的东西。强制前置会让作者凭标题党直觉选标题，而定稿后标题往往要改，改了还要重走 `decide`。真流程第二轮里，Agent 只能先攒着候选、写完正文再补 `marketing set`，这是流程外的绕行。

**建议**：把 headline 从 draft 阶段的必需产物里拆出来，只要求 digest / opening / cta；`marketing ask` 挪到 review 之后、accept 之前。若担心旧工作区不兼容，可保留字段、只改「必需」判定。

### F2 验收绑死正文哈希（P1）

**现象**：`acceptance.yaml` 记录 `draft_sha256`，正文任何变化都让验收失效，必须 `reopen` → 重审 → 重新确认。

**证据**：本轮真实发生：导出后想把作者选定的标题以 H1 写进正文首行，会直接推翻已完成的验收。`two-rulers` 的正文首行有 `# 标题`，而 `moments-ladder-v2` 没有，导致 md 导出的 title 退化成作品 id（见 E2）。

**影响**：公众号稿子在验收后还要排版、改标题、加导语，这类改动每次都要全量重走。绑定本身合理，但粒度过细。

**建议**：区分「内容变更」与「排版变更」。前者仍要重审；后者（新增首个 H1、分隔符、空行、标点）允许 `review record --reaffirm` 加一次作者确认后直接更新哈希，不再要求重新 `decide` 标题、重申 rubric。

### F3 简报字段与决策的两步走（P2）

**现象**：`brief` 的必填项是 audience / purpose / key_messages 三项，作者一轮答完是常态，但落地要 「`decision add` → `decide` → `brief set --audience-decision ... --purpose-decision ... --message-decision ...`」两轮命令；多字段共用一个决策的写法只在 `brief set --help` 里一句带过。

**影响**：这是每篇必走的第一道关卡，多出来的动作纯属摩擦。

**建议**：`brief set` 支持 `--decision <id>` 一次挂到本轮回答的全部字段；或在 `brief show` 的问题清单里直接给出建议命令。

### F4 `next` 串入别的作品的 handoff todo（P3）

**现象**：新作品 `moments-ladder` 的 `wa-cli next` 输出里带着上一个作品 `two-rulers` 的待办（配图占位框）。

**影响**：新手会以为新作品有遗留问题。

**建议**：`next` 只带与当前单元相关的 handoff 条目；全局待办单独一条命令（如 `handoff todo`）。

## 二、导出

### E1 wechat-html 不带标题（P0，公众号线）

**现象**：`export --format wechat-html` 产出从正文第一段开始的内联样式 HTML，标题、摘要、作者都没有；标题只存在 `marketing.yaml` 里。

**影响**：公众号编辑器里每次都要手贴标题，且容易贴错版本（本轮正文里就有两版 CTA 文案不一致的情况）。这是整条线上唯一必须人肉补的一步。

**建议**：HTML 顶部输出标题块（H1 + digest），或提供 `--with-title` / `--title-block` 开关；默认关闭也行，但要能一条命令拿到可粘贴的完整稿。

### E2 md front matter 的 title 退化成作品 id（P1）

**现象**：正文没有 H1 时，`export --front-matter` 的 `title` 取作品 id（`moments-ladder-v2`），`description` 取 brief 的 purpose。

**证据**：`two-rulers` 首行有 `# 换城市换的不是收入，是尺子`，导出 title 正常；`moments-ladder-v2` 没有 H1，导出 title 为作品 id。

**建议**：title 优先取作者选定的 headline（`marketing.headline`），其次正文首个 H1，最后才是作品 id；description 优先取 digest 而不是 purpose。

## 三、审稿与质检

### R1 `review record` 要手写整个 YAML（P0，使用成本）

**现象**：一次审稿要写 8 个 rubric 项（每项 verdict + evidence + paragraphs）与若干 finding，约 2KB YAML，全部手写；段落号还得自己数，写错段落或引文对不上就整份被拒。

**证据**：本轮真实报错两次：`review.quote-not-found`（引文与段落不一致）、`review.unknown-paragraph`（引了第 71 段，正文只有 65 段）。

**影响**：占整轮四分之一以上的时间，是纯机械劳动。

**建议**：`review record --init` 生成带 rubric 骨架（item、verdict 占位、paragraphs 提示）与段落索引的文件；或提供 `review draft` 输出当前正文的段落编号表，减少数错。

### R2 lint 不检查示意数字与编造细节（P2）

**现象**：正文里「3 个月前」「共同好友 12 人」「214 个点赞」这类示意数字，lint 全过（`claim-candidates` 只认已登记的论断与数据候选）。

**影响**：本轮两条审稿发现里有一条就是这个（读者会把示意数字当作作者的真实数据）。这类问题只能靠人肉读。

**建议**：加一条可选规则（默认 warning）：段落内出现多组具体数字但无 fact 论断关联时提示「疑似示意数字，确认是否需要限定或标注」。

### R3 字数口径（P3，待确认）

**现象**：`lint` 的 `chars` 是含标点的字符数（本轮 2755），汉字数 2353；简要里的 2500–3200 按同一口径判定。

**影响**：公众号作者通常按汉字数感觉篇幅，两个口径差 15% 左右，容易在边界上反复。

**建议**：输出同时给 `chars` 与 `cjkChars`，或在文档里写清口径。

## 四、审计与确认

### A1 假设确认与真确认不可区分（P1）

**现象**：`accept --confirmed-by-author --confirmation "<文本>"` 任何文本都写 `channel: cli`、`confirmedByAuthor: true`。第一轮为了不冒用作者名义，把「【流程试用·假设，非作者本人拍板】」写进 confirmation 文本，记录形式上仍是作者确认。

**影响**：仓库历史里混进了假验收，事后只能靠读文本判断。

**建议**：加 `--dry-run` 或显式渠道 `channel: assumption`；作者在阅读页确认的走 `preview`，命令行代作者转述的另记一种渠道。

### A2 preview 只监听 127.0.0.1（P2，已用脚本绕过）

**现象**：`wa-cli preview --serve` 只绑 127.0.0.1，且校验请求的 Host 头（纯 TCP 转发返回 403）；作者在另一台机器（同一 tailnet）时打不开。

**现状**：已在 `writing-desk/scripts/preview-tailnet.mjs`（命令名 `wa-preview`）做了 Host 重写代理，绑 Tailscale IP，支持 `--dir/--port/--host/--json`。

**建议**：`preview --serve` 增加 `--bind <address>` 与可选的 `--base-url`，把 Host 校验改成白名单，省掉外挂代理。

## 五、不建议改的

- **不要为了顺滑削掉关卡**。第一轮把四道关卡都用一句「按假设」越过，产出读起来像 Agent 自证；第二轮每道都真停，稿子的判断才属于作者。
- **不要自动替作者选标题**。`marketing ask` 的阻塞决策是对的，连推荐理由都该保留在决策里。
- **不要放宽 lint 的论断关联**。本轮 8 条 fact 论断全部关联素材，是稿子可信的底。

## 六、优先级汇总

| 编号 | 问题 | 优先级 | 影响面 |
|---|---|---|---|
| E1 | wechat-html 无标题块 | P0 | 每篇公众号稿都要手贴 |
| R1 | review record 手写 YAML | P0 | 每篇审稿 |
| F1 | 标题候选前置到 draft | P1 | 每篇的决策顺序 |
| F2 | 验收绑死正文哈希 | P1 | 验收后的排版改动 |
| E2 | md title 退化成作品 id | P1 | 导出元数据 |
| A1 | 假设确认不可区分 | P1 | 审计链 |
| F3 | 简报字段两步走 | P2 | 每篇第一道关卡 |
| A2 | preview 只绑 127.0.0.1 | P2 | 远程作者确认 |
| R2 | lint 不查示意数字 | P2 | 事实严谨性 |
| F4 | next 串入别的作品 todo | P3 | 日常噪音 |
| R3 | 字数口径 | P3 | 篇幅判定 |

如果只做三件：E1（导出带标题，每篇省一次手贴）、R1（审稿骨架，每篇省四分之一时间）、F1（标题后置，决策顺序顺过来）。

## 七、对产出的判断

两轮稿子都在 `.writing-anything/exports/`：`moments-ladder`（假设轮）与 `moments-ladder-v2`（真流程轮）。工具层面能保证的是结构、事实关联与流程留痕；保证不了的是「这是不是作者的文章」。

真流程那一篇的事实全部来自已有素材（2026 年线上上行比较元分析、费斯廷格 1954、斯托弗 1949、拉特默 2005、博伊斯 2010、廖福挺 2021、局部支配效应），核心观点是 Agent 起草、作者确认的。它缺一个只有作者能给的细节——他自己那条被刺到的朋友圈。有它，文章才站得住；没有它，就是一篇合格的解释文。

另一个可测但未测的指标是信息密度：2700 字装八个研究，读者到六成容易掉。`metrics compute` 只有句长、段长、词汇丰富度，没有信息密度，建议后续补一个「每千字引证数」之类的确定性指标。
