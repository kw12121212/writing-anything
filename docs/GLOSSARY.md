# 术语表（Glossary）

本项目所有面向人的文字（文档、CLI 输出、诊断、Skill、路线图与规格）统一使用本表的译名。

## 使用约定

- 专有名词在每份文档或产物中**首次出现**时写作「中文（English）」，例如「规范包（Pack）」，此后使用「常用写法」一栏的形式。
- 「常用写法」为英文的词（如 Agent、CLI、MCP），说明行业内直接使用英文更通俗，正文中直接写英文即可。
- 命令名、参数名、字段名、文件名、文体 id 等**标识符**保持英文原样，不翻译，例如 `wa-cli draft write`、`tech-blog`、`covers`。
- 引入新的专有名词时，在同一变更中把它加入本表。

## 架构

| 中文 | English | 常用写法 | 说明 |
|---|---|---|---|
| 内核 | Kernel | 内核 | 与文体无关的通用能力：单元树、生命周期、事实库、规则引擎等 |
| 文体 | Genre | 文体 | 一类文章的写作规则与流程，如 `tech-blog`、`novel` |
| 规范包 | Pack | 规范包 | 第三方或作者提供的纯数据规范：文风、术语、禁用词、评分标准 |
| 工作区 | Workspace | 工作区 | 项目中的 `.writing-anything/` 目录 |
| 作品 | Work | 作品 | 一篇文章、一部小说等独立成品 |
| 单元 | Unit | 单元 | 作品中逐个推进的组成部分，如节、章、场景 |
| 单元类型 | Unit Type | 单元类型 | 文体声明的单元种类，如文章、节；决定单元树的层级 |
| 单元树 | Unit Tree | 单元树 | 单元的层级结构，如 部 → 章 → 场景 |
| 根单元 | Root Unit | 根单元 | 单元树的顶层单元，即作品本身；类型为文体的第一个单元类型 |
| 单元引用 | Unit Reference | 单元引用 | 命令中定位单元的写法：`<work>` 表示根单元，`<work>/<unit-id>` 表示任意深度的单元 |
| 汇总单元 | Aggregated Unit | 汇总单元 | 单元类型声明 `stage_from_children` 的单元（如 novel 的部与章）：阶段不由推进得出，而由子单元树中走生命周期的单元汇总；`work.yaml` 中的存储阶段仅为占位，生效阶段以推导为准，`wa-cli advance` 对其报告 `lifecycle.derived-stage` |
| 推导阶段 | Derived Stage | 推导阶段 | 汇总单元的生效阶段：取子单元树中最落后者（最短板）；没有子孙单元时为文体第一个阶段，全部 done 时为 done；`wa-cli status` 与 `next` 按此输出，另附 progress（done 计数与总数） |
| 系列 | Series | 系列 | 共享规范与设定的多部作品；文章领域也称"专栏"；定义在 `.writing-anything/series/<id>.yaml`，成员作品继承其规范包、素材与事实 |
| 作用域 | Scope | 作用域 | 设定或配置生效的范围，如系列级、作品级 |
| 内置 | Built-in | 内置 | 随 `wa-cli` 二进制提供的文体与规范包，如 `default` 规范包 |
| 配置层 | Config Layer | 配置层 | 参与合并的一层设置：文体默认、规范包、项目配置，后者覆盖前者；来源写作 `genre:<id>`、`pack:<id>`、`project` |
| 解析后配置 | Resolved Config | 解析后配置 | 各配置层合并后的生效设置，每项注明来源层，由 `wa-cli config resolved` 输出 |

## 写作流程

| 中文 | English | 常用写法 | 说明 |
|---|---|---|---|
| 生命周期 | Lifecycle | 生命周期 | 单元从简报到验收的完整过程 |
| 阶段 | Stage | 阶段 | 生命周期中的一步 |
| 关卡 | Gate | 关卡 | 进入下一阶段前必须满足的检查 |
| 必需产物 | Required Artifact | 必需产物 | 文体为每个阶段声明、推进前必须存在于单元目录中的产物，如 `brief.yaml` |
| 关卡强度 | Gate Level | 关卡强度 | `strict` 严格 / `standard` 标准 / `fast` 快速 |
| 写作简报 | Brief | 写作简报 | 受众、目的、核心观点、字数、禁区 |
| 核心观点 | Key Message | 核心观点 | 文章必须传达的观点，由作者提供 |
| 写作目的 | Purpose | 写作目的 | 读者读完后应该知道、相信或去做什么；写作简报的 `purpose` 字段 |
| 禁区 | Off-limits | 禁区 | 文章不能写或需要回避的内容；写作简报的 `avoid` 字段 |
| 简报必填项 | Required Brief Field | 简报必填项 | 文体为单元类型声明、推进前必须在写作简报中填写的字段（`brief_required`） |
| 大纲 | Outline | 大纲 | 章节结构及其覆盖的核心观点 |
| 大纲节 | Outline Section | 大纲节 | 大纲中的一节：`covers` 覆盖的核心观点、`uses` 使用的素材，或 `reason` 不覆盖的理由；`role` 为它承担的写作法章节或节拍 |
| 草稿 | Draft | 草稿 | 尚未验收的正文 |
| 审稿 | Review | 审稿 | 文本检查加按评分标准的评审 |
| 修订 | Revision | 修订 | 根据批注与审稿发现项修改正文 |
| 修订任务 | Revision Task | 修订任务 | 尚未处理的审稿发现项或作者反馈；standard 与 strict 关卡强度下全部解决或豁免后才能验收 |
| 验收 | Acceptance | 验收 | 作者确认单元达到要求 |
| 导出 | Export | 导出 | 生成 Markdown、HTML 等交付格式 |
| 纯文本 | Plain Text | 纯文本 | `--format text` 导出的交付格式：去掉 Markdown 标记、只保留文字内容的 `.txt` 文件 |
| 文件头元数据 | Front Matter | front matter | 导出文件开头的 YAML 元数据块（标题、描述、文体、单元与验收时间），供博客平台读取 |
| 组稿 | Assembly | 组稿 | 把已验收单元按顺序合成完整书稿 |

## 内容与出处

| 中文 | English | 常用写法 | 说明 |
|---|---|---|---|
| 素材 | Source | 素材 | 作者笔记、访谈、转写稿、参考资料、过往文章 |
| 素材库 | Source Library | 素材库 | 原文存档的素材集合 |
| 论断 | Claim | 论断 | 需要出处支撑的事实、数据、经历或观点 |
| 疑似论断 | Claim Candidate | 疑似论断 | 文本检查按启发式规则发现、尚未登记为论断的亲历或数据句子 |
| 出处 | Provenance | 出处 | 内容来自人、Agent，还是 Agent 起草后经人修改；段落出处取值为 `human`（作者）、`agent`（Agent）、`agent-draft-human-edited`（Agent 起草、作者修改） |
| 段落 | Paragraph | 段落 | 正文中由空行分隔的连续非空行，围栏代码块整体算一段；出处的最小粒度 |
| 写入记录 | Draft Record | 写入记录 | `wa-cli draft write` 在单元目录留下的 `provenance.yaml`：整篇正文与每个段落的哈希及出处，用于推导段落出处 |
| 事实库 | Fact Store | 事实库 | 带出处与保护级别的素材、论断与设定条目 |
| 设定集 | Canon | 设定集 | 小说的人物、地点、物品、世界规则等既定设定 |
| 设定条目 | Canon Entry | 设定条目 | 设定集中的一条：人物、地点、物品、世界规则或人物关系，带出处（素材 id）与保护级别；人物条目即人物卡 |
| 别名 | Alias | 别名 | 设定条目的其他称谓（绰号、称号等）；别名不得与任何名称或其他别名重复（名称之间可以重复），检索时映射到同一条目 |
| 保护级别 | Protection Level | 保护级别 | 设定条目的保护强度：`normal`（常规）或 `author-pinned`（作者钦定，最高级别，修改须经决策队列授权） |
| 作者钦定 | Author-pinned | 作者钦定 | 由作者确定、受最高级别保护的内容 |
| 状态变更提议 | State Change Proposal | 状态变更提议 | 场景造成的人物或世界状态变化，待作者确认 |
| 摘要 | Summary | 摘要 | 已验收单元的简要内容，由 Agent 撰写、`wa-cli summary set` 校验并记录正文哈希；正文之后被修改时过期；系列中后面作品的上下文包会带上前序作品的摘要，场景单元的上下文包会带上前序场景的摘要 |
| 事实 | Fact | 事实 | 系列中带素材出处的共享事实，成员作品的上下文包会带上它 |
| 分层摘要 | Layered Summary | 分层摘要 | 场景 → 章 → 部 逐层汇总的摘要 |
| 卷末检查点 | Volume Checkpoint | 卷末检查点 | 每部（卷）结束时汇总新增设定、状态与伏笔变化，供作者抽查确认 |
| 时间线 | Timeline | 时间线 | 事件的时间与先后顺序 |
| 故事圣经 | Story Bible | 故事圣经 | 长篇的外部记忆：设定集、状态快照、知情表、伏笔台账、因果链、分层摘要的总称 |
| 事件溯源 | Event Sourcing | 事件溯源 | 只记录每次变化（事件），状态由事件按顺序重放得出 |
| 状态快照 | State Snapshot | 状态快照 | 某个场景开始时人物、物品、地点的状态 |
| 状态台账 | State Ledger | 状态台账 | 作品根单元目录 `state.yaml` 中按场景登记的状态变更提议（State Change Proposal）总账：Agent 经 `wa-cli state propose` 提交、作者确认后生效，`wa-cli state at <场景>` 按单元树先序重放已确认条目得出状态快照（State Snapshot） |
| 知情表 | Knowledge State | 知情表 | 各人物与读者在某个场景时已知的信息；存于作品根单元目录 `knowledge.yaml`，Agent 经 `wa-cli knowledge propose` 登记、作者确认后生效，`wa-cli knowledge at <场景>` 按单元树先序重放已确认条目得出某获知者的已知列表 |
| 知情事件 | Knowledge Event | 知情事件 | 知情表中的一条登记：某获知者（设定集 character 条目或固定值 `reader` 表示读者（Reader））在某场景获知某设定条目；知情只积累，首次获知即已知，重复登记不改变已知列表 |
| 伏笔台账 | Foreshadowing Ledger | 伏笔台账 | 伏笔的埋设位置、计划回收位置与状态，存于作品根单元目录的 `threads.yaml`，由 `wa-cli threads` 列出并标出已超过计划回收位置的条目 |
| 伏笔条目 | Thread Entry | 伏笔条目 | 伏笔台账中的一条登记：伏笔内容、埋设场景、计划回收位置与状态（open 未回收 / paid 已回收 / dropped 放弃），放弃须经决策队列作者确认 |
| 因果链 | Causality Chain | 因果链 | 场景的起因引用前文事件、结果产生新事件的链条 |
| 起因 | Cause | 起因 | 场景契约中引用的前文已发生事件（Event）的 id，由 `wa-cli scene contract` 声明；引用不存在或尚未发生的事件时校验失败 |
| 事件 | Event | 事件 | 场景契约中预期产生的事件 id，在作品内唯一，是因果链与事件溯源（Event Sourcing）的引用对象 |
| 状态变化 | State Change | 状态变化 | 场景契约中声明的必须发生的变化，每条带 id 与描述，写后收口（Close-out）按 id 对照提交 |
| 场景契约 | Scene Contract | 场景契约 | 动笔前声明场景的出场人物、承担的情节节拍（Story Beat）与揭示边界（Reveal Boundary）；长篇在此基础上扩展视角（POV）、地点、状态变化、伏笔与因果链，由 `wa-cli scene contract` 写入 |
| 写后收口 | Close-out | 收口 | 场景写完后提交抽取结果，使台账与正文同步 |
| 别名 | Alias | 别名 | 同一人物、地点、物品的其他称呼 |

## 质量与规则

| 中文 | English | 常用写法 | 说明 |
|---|---|---|---|
| 规则引擎 | Rule Engine | 规则引擎 | 执行声明式规则的组件 |
| 规则 | Rule | 规则 | 一条可配置的检查，如禁用词、字数范围 |
| 文本检查 | Lint | 文本检查 | 确定性的自动检查，类似代码的 lint |
| 屏蔽区域 | Masked Region | 屏蔽区域 | 文本检查跳过的正文部分：代码块、行内代码、HTML 注释、URL 与链接地址 |
| 信息字符串 | Info String | 信息字符串 | 围栏代码块开头的 ``` 或 ~~~ 之后的文字，通常写明代码的语言 |
| 破折号密度 | Dash Density | 破折号密度 | 每千字中破折号（——）的个数，AI 腔的一种信号 |
| 散文段落 | Prose Paragraph | 散文段落 | 文本检查中由空行分隔、含屏蔽区域之外文字的连续行；单处豁免以它为作用范围 |
| 发现项 | Finding | 发现项 | 检查或审稿发现的一个问题 |
| 豁免 | Waiver | 豁免 | 对某条规则在某处不适用的显式声明，须写明理由 |
| 单处豁免 | Inline Waiver | 单处豁免 | 正文注释 `<!-- lint-ignore <规则 id>: 理由 -->`，豁免一个散文段落中该规则的发现项；须由作者写入 |
| 规则类型 | Rule Type | 规则类型 | 规则的检查方式，如 `length-range`、`banned-terms`；只能取已知类型 |
| 禁用词 | Banned Term | 禁用词 | 规范包中不允许出现的词语，可附理由与替换写法 |
| 评分标准 | Rubric | 评分标准 | 审稿时逐项评判的标准 |
| AI 腔 | AI Cliché | AI 腔 | 典型的 AI 套话与句式，如"值得注意的是" |
| 小说 AI 腔词表 | Fiction AI-cliché Terms | 小说 AI 腔词表 | 内置 short-story 与 novel 文体为 `ai-cliche` 规则声明的虚构写作套话词表，如"眼底闪过一丝" |
| 对话标点 | Dialogue Punctuation | 对话标点 | `dialogue-punctuation` 规则检查的小说对话标点：应使用全角引号，对话后紧跟说话人时引号内句号应为逗号 |
| 视角与人称一致 | POV Consistency | 视角一致 | `pov-consistency` 规则的启发式检查：按写作简报 `pov` 的人称检测叙述文字中的视角滑移 |
| 中文排版 | CJK Typography | 中文排版 | 中英文间空格、全角/半角标点等排版规范 |
| 术语表 | Glossary | 术语表 | 标准写法与错误写法的对照 |
| 覆盖追踪 | Coverage Tracing | 覆盖追踪 | 检查每个核心观点都被大纲的某一节覆盖 |
| 覆盖缺口 | Coverage Gap | 覆盖缺口 | 没有被任何大纲节覆盖的核心观点，或既不覆盖也没写理由的大纲节；选用写作法时，还包括没有大纲节承担的写作法章节或节拍、顺序颠倒的章节与不属于写作法的 `role` |
| 连续性审查 | Continuity Review | 连续性审查 | 检查新正文与设定集、前文是否矛盾 |
| 审查任务包 | Review Task Packet | 审查任务包 | 只含一段正文、相关台账切片与检查清单的小型审查任务 |
| 影响传播 | Ripple Analysis | 影响传播 | 修改早期内容后，找出受影响、需复查的后续内容 |
| 预审 | Pre-review | 预审 | 由 AI 完成的第一遍审稿，默认不阻塞，作者做最终判断 |
| 指标 | Metric | 指标 | 可计算或可打分的量化数据，如平均句长、紧张度 |
| 文风统计 | Style Stats | 文风统计 | 由作者过往文章确定性计算的句长、段长、标点频率与常用词，确认后写为规范包的 `style_profile` |
| 规范包提议 | Pack Proposal | 提议 | `.writing-anything/proposals/` 中待作者逐项确认的规范包条目；确认前不影响任何规则 |
| 文风漂移 | Style Drift | 文风漂移 | 指标明显偏离作者文风画像或本作品已验收单元的基准（默认超过 2 个标准差，规范包用 `drift_threshold` 设置）；`wa-cli metrics compute` 列出，只衡量偏离、不评判好坏，不阻塞验收 |
| 锚定评分 | Anchored Scale | 锚定评分 | 每个分值都附带示例片段的评分方式，减少打分波动 |
| 校准 | Calibration | 校准 | 用作者打分的样本检验 AI 评分是否可信 |
| 一致率 | Agreement Rate | 一致率 | AI 评分与作者评分相符的比例 |
| AI 评分指标 | Scored Metric | AI 评分指标 | 文体或规范包用 `scored_metrics` 声明、由 Agent 在独立会话中按锚定评分打分的指标 |
| 打分任务 | Scoring Task | 打分任务 | `wa-cli metrics plan` 给出的片段、指标与锚定说明，供 Agent 打分 |
| 打分会话 | Scoring Session | 打分会话 | 一次全新的、独立的 AI 打分；同一片段需要多个会话的分数才能取中位数 |
| 片段 | Segment | 片段 | 打分的单位，即正文的一个段落，编号与 `wa-cli provenance` 相同 |
| 分歧 | Score Disagreement | 分歧 | 同一片段各次打分的最高分减最低分；超过阈值时转为作者决策 |
| 仅供参考 | Advisory | 仅供参考 | 与作者一致率低于阈值的指标：照常显示分数，但不再产生分歧决策 |
| 情绪曲线 | Emotion Curve | 情绪曲线 | 按单元顺序排列的情绪评分走势 |
| 张力曲线 | Tension Curve | 张力曲线 | 按单元顺序排列的紧张度评分走势 |
| 偏离发现项 | Deviation Finding | 偏离发现项 | 曲线取值与目标分值之差超过指标 tolerance 的单元；`wa-cli curve` 列出，只衡量偏离、不评判好坏，不阻塞验收 |
| 节奏检查 | Pacing Check | 节奏检查 | 文体用 `pacing_checks` 声明的节奏约束（如两个节拍点片段之间的最大字数间隔），由 `wa-cli curve` 基于已记录的分数与确定性指标执行，发现项不阻塞验收 |
| 极限词 | Superlative Claims | 极限词 | 广告法限制的"最""第一"等绝对化用语 |

## 人机协作

| 中文 | English | 常用写法 | 说明 |
|---|---|---|---|
| 作者 | Author | 作者 | 内容来源与最终决策者 |
| 智能体 | Agent | Agent | 接入本工具、协助作者写作的 AI 程序 |
| 决策 | Decision | 决策 | 需要作者决定的一个问题；决定后的原话即为作者的确认记录 |
| 决策队列 | Decision Queue | 决策队列 | 等待作者决定的事项列表 |
| 阻塞 | Blocking | 阻塞 | 未解决前禁止继续推进 |
| 阻塞项 | Blocker | 阻塞项 | 阻止单元推进的具体原因，如缺失的必需产物、未决定的阻塞决策，由 `wa-cli next` 与 `wa-cli status` 列出 |
| 确认记录 | Confirmation Record | 确认记录 | 作者确认的时间与原话摘要 |
| 批注 | Comment | 批注 | 作者写在正文里的修改意见，如 `<!-- @me: ... -->` |
| 作者反馈 | Feedback | 反馈 | 作者对正文的结构化意见，来自对话、正文批注或阅读页 |
| 锚点 | Anchor | 锚点 | 反馈指向的原文片段及其前后文，正文修改后用于重新定位 |
| 失去锚点 | Orphaned Anchor | 失去锚点 | 正文修改后无法重新定位的反馈，需要作者处理 |
| 阅读卡点 | Sticking Point | 阅读卡点 | 作者阅读时标记的"读不下去"的位置 |
| 阅读页 | Reading View | 阅读页 | `wa-cli preview` 生成的逐段阅读页面：默认为可离线打开的静态 HTML；加 `--serve` 时为本地服务提供、可以选中文字提交反馈的页面 |
| 令牌 | Token | 令牌 | `wa-cli preview --serve` 每次运行生成的随机字符串，访问本地阅读页必须携带，防止本机其他网页或程序冒用 |
| 人工编辑保护 | Human Edit Protection | 人工编辑保护 | 人改过的正文不被 Agent 覆盖 |
| 上下文包 | Context Pack | 上下文包 | 只包含当前单元所需信息的输入集合 |
| 上下文预算 | Context Budget | 上下文预算 | 上下文包的大小上限，以字符数近似度量 |
| 相邻节 | Neighbor Section | 相邻节 | 单元在父单元中的前一个与后一个兄弟单元，上下文包给出其大纲标题与正文节选 |
| AI 参与说明 | AI-generated Content Label | AI 参与说明 | 导出时按段落出处说明 AI 参与情况的文字或元数据，也称「AI 生成内容标识」；放置位置由 `ai_disclosure` 决定 |
| 文风画像 | Style Profile | 文风画像 | 从作者样文归纳的写作风格特征 |
| 作者偏好 | Author Preference | 作者偏好 | 从作者修改与作者反馈中归纳的习惯，如"不用排比句"；以规范包提议的条目出现，作者确认后写入规范包 |
| 修改记录 | Edit Record | 修改记录 | 单元目录中的 `edits.yaml`：作者对 Agent 段落的修改对，由 `wa-cli draft write` 追加 |
| 修改对 | Edit Pair | 修改对 | 一段 Agent 原文与作者修改后的文字，是归纳作者偏好的输入 |
| 交接记录 | Handoff Note | 交接记录 | 会话结束时记录的进展与待办，供新会话接续 |
| 子 Agent | Subagent | 子 Agent | 由主 Agent 派生、拥有独立全新上下文的 Agent 会话 |

## 文体类型

| 中文 | English | 常用写法 | 说明 |
|---|---|---|---|
| 文章 | Article | 文章 | 非虚构写作的统称，包括博客、公众号文章、报告等 |
| 非虚构 | Non-fiction | 非虚构 | 以真实信息为基础的写作 |
| 虚构 | Fiction | 虚构 | 小说等想象性写作 |
| 短文 | Short-form Writing | 短文 | 篇幅短小的文章或随笔，如短评、小红书笔记、微头条 |
| 技术博客 | Tech Blog | 技术博客 | 分享技术经验、方案与教程的文章；文体 id 为 `tech-blog` |
| 公众号文章 | WeChat Official Account Article | 公众号文章 | 发布在微信公众号上的文章 |
| 营销文案 | Marketing Copy | 营销文案 | 以推广产品或品牌为目的的文字；公众号营销文体 id 为 `wechat-marketing` |
| 软文 | Advertorial | 软文 | 以内容形式包装的广告 |
| 随笔 | Essay | 随笔 | 以个人观点和感受为主的散文式文章 |
| 教程 | Tutorial | 教程 | 按步骤教读者完成某件事的文章 |
| 复盘 | Retrospective / Postmortem | 复盘 | 回顾项目或事故的经过、原因与改进 |
| 小说 | Novel / Fiction | 小说 | 虚构叙事作品的统称 |
| 微型小说 | Flash Fiction | 微型小说 / 小小说 | 通常在 2000 字以内的极短篇小说 |
| 短篇小说 | Short Story | 短篇小说 | 一般在数千到两三万字；文体 id 为 `short-story` |
| 中篇小说 | Novella | 中篇小说 | 介于短篇与长篇之间 |
| 长篇小说 | Novel | 长篇小说 | 通常在十万字以上；文体 id 为 `novel` |
| 系列小说 | Novel Series | 系列小说 | 共享世界观与人物的多部小说 |
| 网络小说 | Web Novel | 网文 | 在网络平台连载的小说 |

## 文章写作

| 中文 | English | 常用写法 | 说明 |
|---|---|---|---|
| 选题 | Topic Selection | 选题 | 决定写什么 |
| 受众 | Audience | 受众 | 文章写给谁看 |
| 读者画像 | Reader Persona | 读者画像 | 对目标读者的具体描述 |
| 论点 | Thesis / Argument | 论点 | 文章要论证的主张；本工具中作者的论点记录为核心观点 |
| 论据 | Evidence | 论据 | 支撑论点的事实、数据、案例 |
| 案例 | Case Study | 案例 | 用于说明观点的真实事例 |
| 引用 | Citation / Quote | 引用 | 注明来源的他人内容 |
| 标题 | Headline / Title | 标题 | |
| 小标题 | Subheading | 小标题 | 正文中分节的标题 |
| 导语 | Lede / Lead | 导语 | 文章开头概括要点、吸引阅读的段落 |
| 正文 | Body | 正文 | |
| 结语 | Conclusion | 结语 | |
| 金句 | Quotable Line / Punchline | 金句 | 便于传播、令人印象深刻的句子 |
| 语气 | Tone | 语气 | 如正式、轻松、犀利 |
| 文风 | Writing Style / Voice | 文风 | 作者稳定的表达特征 |
| 人设 | Persona | 人设 | 作者或账号对外呈现的形象 |
| 总分总 | General–Specific–General Structure | 总分总 | 先总述、再分述、最后总结的结构 |
| 金字塔原理 | Pyramid Principle | 金字塔原理 | 结论先行、以上统下的表达方法 |
| 代码示例 | Code Sample | 代码示例 | 技术文章中的示例代码 |
| 前置要求 | Prerequisites | 前置要求 | 读者需要事先具备的知识或环境 |
| 字数 | Word Count | 字数 | 中文按字符计 |

## 营销与公众号

| 中文 | English | 常用写法 | 说明 |
|---|---|---|---|
| 营销要素 | Marketing Elements | 营销要素 | 营销文章的标题候选、文章摘要、开头钩子与行动号召，记录在单元目录下的 `marketing.yaml` |
| 标题候选 | Headline Candidates | 标题候选 | 供作者挑选的多个标题；作者通过标题决策选定 |
| 文章摘要 | Digest | 文章摘要 | 公众号文章在分享卡片与消息列表中显示的简介；不同于供上下文使用的「摘要（Summary）」 |
| 标题决策 | Headline Decision | 标题决策 | 由 `wa-cli marketing ask` 登记、请作者从标题候选中选定标题的阻塞决策 |
| 标题党 | Clickbait | 标题党 | 标题夸大或与内容不符，应避免 |
| 开头钩子 | Hook | 钩子 | 吸引读者读下去的开头 |
| 行动号召 | Call to Action | CTA | 引导读者关注、购买、报名等的结尾 |
| 卖点 | Selling Point | 卖点 | 产品打动用户的关键特性 |
| 痛点 | Pain Point | 痛点 | 目标用户亟待解决的问题 |
| 种草 | Product Seeding | 种草 | 通过内容激发读者的购买兴趣 |
| 转化 | Conversion | 转化 | 读者完成关注、购买等目标行为 |
| 爆款 | Viral Hit | 爆款 | 传播量远超平均的内容 |
| 阅读量 | Page Views / Reads | 阅读量 | |
| 完读率 | Read-through Rate | 完读率 | 读完全文的读者比例 |
| 封面图 | Cover Image | 封面图 | |
| 配图 | Illustration | 配图 | 正文中的图片 |
| 内联样式 | Inline Style | 内联样式 | 直接写在 HTML 标签上的样式，公众号编辑器要求 |
| 样式主题 | HTML Theme | 样式主题 | HTML 导出时各元素的内联样式，规范包用 `html_theme` 覆盖内置默认主题 |
| 配图清单 | Image List | 配图清单 | 公众号 HTML 导出时与导出文件一起写出的配图列表，供作者按编号替换配图占位 |
| 原创声明 | Originality Declaration | 原创声明 | 公众号对原创内容的标注 |
| 敏感词 | Sensitive Words | 敏感词 | 平台限制或需谨慎使用的词语 |

## 小说创作

| 中文 | English | 常用写法 | 说明 |
|---|---|---|---|
| 前提 | Premise | 前提 | 用一两句话概括的故事核心设想 |
| 主题 | Theme | 主题 | 作品想表达的深层意义 |
| 世界观 | Worldbuilding | 世界观 | 故事世界的规则、历史与社会结构 |
| 设定 | Setting | 设定 | 人物、地点、规则等具体设计；集合称设定集 |
| 人物 | Character | 人物 / 角色 | |
| 主角 | Protagonist | 主角 | |
| 反派 | Antagonist | 反派 | |
| 配角 | Supporting Character | 配角 | |
| 人物卡 | Character Sheet | 人物卡 | 人物的设定条目 |
| 人物弧光 | Character Arc | 人物弧光 | 人物在故事中的成长或转变轨迹 |
| 动机 | Motivation | 动机 | 人物行动的原因 |
| 情节 | Plot | 情节 | |
| 主线 | Main Plot | 主线 | |
| 支线 | Subplot | 支线 | |
| 情节节拍 | Story Beat | 节拍 | 情节推进中的关键转折点；短篇中作为故事根单元大纲（Outline）的节 |
| 冲突 | Conflict | 冲突 | 推动故事的对立与阻碍 |
| 悬念 | Suspense | 悬念 | 让读者想知道后续的未解问题 |
| 伏笔 | Foreshadowing | 伏笔 | 提前埋下、后文呼应的线索 |
| 回收伏笔 | Payoff | 回收伏笔 | 后文对伏笔的呼应与揭晓 |
| 高潮 | Climax | 高潮 | 冲突最激烈的部分 |
| 结局 | Resolution / Ending | 结局 | |
| 开篇 | Opening | 开篇 | 小说的开头部分 |
| 断章 | Cliffhanger | 断章 | 章节在悬念处结束，网文常用；也是内置结构类写作法 volume-structure 的收束节拍 |
| 黄金三章 | Golden Three Chapters | 黄金三章 | 网文开篇模板：前三章完成钩子、主角亮相、核心冲突与金手指（Protagonist's Advantage）揭示，内置写作法 id 为 golden-three |
| 卷结构 | Volume Structure | 卷结构 | 网文的卷级模板：每卷声明目标、高潮与断章三个节拍，供部（卷）单元选用，内置写作法 id 为 volume-structure |
| 爽点 | Payoff Beat | 爽点 | 网文中让读者获得满足感的情节 |
| 金手指 | Protagonist's Advantage | 金手指 | 网文中主角独有的特殊能力或机缘 |
| 起承转合 | Introduction–Development–Turn–Conclusion | 起承转合 | 传统的四段式结构 |
| 三幕结构 | Three-Act Structure | 三幕结构 | 建置、对抗、解决三幕 |
| 英雄之旅 | Hero's Journey | 英雄之旅 | 坎贝尔总结的主角冒险模型 |
| 雪花写作法 | Snowflake Method | 雪花写作法 | 从一句话前提逐步扩展到场景列表的规划方法 |
| 大纲 | Outline | 大纲 | 情节的整体规划 |
| 细纲 | Detailed Outline | 细纲 | 细化到章或场景的大纲 |
| 场景 | Scene | 场景 | 连续时间、地点内的一段情节；也是小说类文体走完整生命周期的单元类型 |
| 章 | Chapter | 章 | novel 文体包含场景的单元类型；汇总单元 |
| 部 | Part | 部 / 卷 | novel 文体包含章的单元类型；汇总单元；网文中常称"卷" |
| 视角 | Point of View | 视角 / POV | 叙述者的观察位置 |
| 第一人称 | First Person | 第一人称 | 以"我"叙述 |
| 第三人称有限视角 | Third-person Limited | 有限视角 | 只呈现单个人物的所见所想 |
| 全知视角 | Omniscient Point of View | 全知视角 | 叙述者知道所有人物的内心与全部事件 |
| 叙述者 | Narrator | 叙述者 | |
| 对白 | Dialogue | 对白 / 对话 | |
| 内心独白 | Interior Monologue | 内心独白 | |
| 展示而非告知 | Show, Don't Tell | 展示而非告知 | 用动作与细节呈现，而不是直接陈述 |
| 节奏 | Pacing | 节奏 | 情节推进的快慢 |
| 揭示边界 | Reveal Boundary | 揭示边界 | 本单元不能提前透露的信息 |
| 连载 | Serialization | 连载 | 分章节持续发布 |
| 番外 | Side Story / Extra | 番外 | 正文之外的补充故事 |
| 前传 | Prequel | 前传 | |
| 续集 | Sequel | 续集 | |
| 同人 | Fan Fiction | 同人 | 基于已有作品的二次创作 |

## 写作法

写作法以写作法模板（Method Template）的形式提供，分为规划类、结构类、文章类三种。金字塔原理、总分总、起承转合、三幕结构、英雄之旅、雪花写作法见上文各节。

| 中文 | English | 常用写法 | 说明 |
|---|---|---|---|
| 写作法模板 | Method Template | 写作法模板 | 以纯数据描述的写作方法：规划步骤、结构节拍或章节模板 |
| 规划类写作法 | Planning Method | 规划类写作法 | 分步产出规划产物，每步经作者确认后进入下一步，如雪花写作法 |
| 规划 | Plan | 规划 | 单元目录下的 `plan.yaml`：按规划类写作法的步骤分步记录规划文本、素材与产物链接，由 `wa-cli plan show / step / confirm` 管理 |
| 规划步骤 | Planning Step | 规划步骤 | 规划类写作法 steps 中声明的一步（如雪花写作法的一句话概括）；每步起草后经作者确认才能进入下一步 |
| 规划产物 | Planning Product | 规划产物 | 规划步骤声明的产物链接类型：characters 为设定集人物卡、scenes 为场景单元、scene-contracts 为场景契约 |
| 产物链接 | Plan Link | 产物链接 | 规划步骤记录中指向已落地产物的引用（`--link`）：characters 为设定集条目 id，scenes 为场景单元引用，scene-contracts 形如 <场景单元引用>:<大纲节 id>，确认时校验可解析 |
| 结构类写作法 | Structure Method | 结构类写作法 | 规定节拍及其在全文中的位置范围，如三幕结构 |
| 目标张力 | Target Tension | 目标张力 | 结构类写作法节拍声明的 0 到 10 整数目标紧张度，作为情绪曲线与张力曲线的目标走势 |
| 文章类写作法 | Article Method | 文章类写作法 | 规定必需章节及其顺序，如金字塔原理 |
| SCQA | Situation–Complication–Question–Answer | SCQA | 情境 → 冲突 → 问题 → 答案的开篇结构，常与金字塔原理配合 |
| 倒金字塔 | Inverted Pyramid | 倒金字塔 | 最重要的信息放在最前面，常用于新闻与资讯 |
| Diátaxis | Diátaxis | Diátaxis | 技术文档四分类：教程、操作指南、参考、解释 |
| 操作指南 | How-to Guide | 操作指南 | Diátaxis 中解决具体问题的步骤说明 |
| 参考 | Reference | 参考 | Diátaxis 中准确、完整描述接口、命令或配置的查阅型文档 |
| 解释 | Explanation | 解释 | Diátaxis 中讨论背景、原理与取舍的理解型文档 |
| AIDA | Attention–Interest–Desire–Action | AIDA | 注意 → 兴趣 → 欲望 → 行动的营销文案结构 |
| PAS | Problem–Agitate–Solution | PAS | 问题 → 放大 → 解决的营销文案结构 |
| 4U 标题法 | 4U Headline Formula | 4U 标题法 | 有用（Useful）、紧迫（Urgent）、独特（Unique）、具体（Ultra-specific） |
| 救猫咪节拍表 | Save the Cat! Beat Sheet | 救猫咪 | 布莱克·斯奈德提出的 15 个故事节拍 |
| 七点结构 | Seven-Point Story Structure | 七点结构 | 钩子、第一情节点、第一压力点、中点、第二压力点、第二情节点、结局 |
| 催化剂 | Catalyst / Inciting Incident | 催化剂 / 激励事件 | 打破主角日常、引出故事的事件 |
| 中点 | Midpoint | 中点 | 故事中段的重大转折 |
| 黄金三章 | Golden First Three Chapters | 黄金三章 | 网文前三章须完成钩子、主角亮相、核心冲突与金手指揭示 |
| 卷结构 | Volume Structure | 卷结构 | 网文按卷规划目标、高潮与断章 |

## 工程

| 中文 | English | 常用写法 | 说明 |
|---|---|---|---|
| 命令行工具 | Command-Line Interface | CLI | 本项目为 `wa-cli` |
| 技能 | Skill | 技能 | Agent Skills 格式的操作说明包；wa-cli 内置的技能用 wa-cli skills install 安装 |
| 模型上下文协议 | Model Context Protocol | MCP | Agent 调用外部工具的开放协议 |
| 大语言模型 | Large Language Model | LLM | Agent 背后的 AI 模型；`wa-cli` 本身不调用 |
| MCP 服务 | MCP Server | MCP 服务 | 由 `wa-cli mcp --dir <项目目录>` 以 stdio 方式提供（MCP 2026-07-28 版协议）：tools 对应命令，resources 暴露工作区产物、生效配置与作者反馈，prompts 对应技能 |
| 工具 | Tool | tool | MCP 服务提供的可调用操作；wa-cli 的每个 tool 对应一条命令（命令名的空格换成下划线） |
| 资源 | Resource | 资源 | MCP 服务提供的只读内容，如工作区产物、生效配置与作者反馈，以 URI 标识 |
| 提示模板 | Prompt | prompt | MCP 服务提供的流程说明；wa-cli 的每个 prompt 对应一个技能 |
| 订阅 | Subscription | 订阅 | MCP 客户端用 subscriptions/listen 关注资源，资源变化时收到更新通知 |
| 标准输入输出 | stdio | stdio | MCP 客户端启动服务进程，经其标准输入与标准输出交换消息的传输方式 |
| 作用域 | Scope | 作用域 | `wa-cli mcp install` 注册 MCP 服务的范围：项目级（project）或用户级（user） |
| 诊断 | Diagnostic | 诊断 | 带错误码、产物路径与修复命令的问题报告 |
| 错误码 | Error Code | 错误码 | 稳定的机器可读问题标识 |
| 参数 | Flag | 参数 | 命令行中以 `--` 开头的选项，如 `--json`；命令名后按顺序给出的值称位置参数 |
| 退出码 | Exit Code | 退出码 | 命令结束时返回的数字：0 成功、1 校验失败或执行失败、2 用法错误 |
| 输出信封 | Output Envelope | 输出信封 | 所有命令 `--json` 输出共用的顶层结构：`valid`、`diagnostics`、`changedPaths` |
| 命令注册表 | Command Registry | 命令注册表 | 声明每条命令参数、输出键和退出码的清单；help 由它生成，它也是 MCP tools 与 Skill 命令校验的依据 |
| 产物 | Artifact | 产物 | 工作区中由 `wa-cli` 管理的文件：受 JSON Schema 约束的结构化 YAML 文件（如 `config.yaml`）与 Markdown 正文 |
| 数据结构约束 | JSON Schema | JSON Schema | 校验 YAML 产物结构的规范 |
| 可执行内容 | Executable Content | 可执行内容 | 产物中可能被当作代码执行的部分：非数据的 YAML 标签，或 `script`、`command` 等字段；规范包中一律拒绝 |
| 测试夹具 | Test Fixture | 测试夹具 | 测试用的示例数据，放在各包的 `testdata/` |
| 回归测试 | Regression Test | 回归测试 | 每次变更后运行的完整测试 |
| 覆盖率 | Coverage | 覆盖率 | 测试执行到的代码比例 |
| 交叉编译 | Cross-compilation | 交叉编译 | 在一个平台上编译出其他平台的二进制 |
| 发布产物 | Release Asset | 发布产物 | 随版本发布的文件：各平台的 `wa-cli-<os>-<arch>` 二进制与 `SHA256SUMS` |
| 校验文件 | Checksum File | 校验文件 | 记录每个发布产物 SHA-256 的 `SHA256SUMS`，格式与 `sha256sum` 兼容 |
| 安装脚本 | Install Script | 安装脚本 | `install.sh`（Linux、macOS）与 `install.ps1`（Windows）：下载、校验并把 `wa-cli` 安装到用户级目录 |
| 锁定文件 | Lock File | 锁定文件 | 记录已安装规范包来源、git 提交与内容 SHA-256 的文件 `.writing-anything/packs.lock.yaml`，由 `wa-cli pack install` 与 `wa-cli pack remove` 维护 |
| 规范包仓库 | Registry | 规范包仓库 | 中心化发布与查找规范包的服务；尚未支持，`<owner>/<name>` 写法为它保留 |
| 全文检索 | Full-text Search | 全文检索 | 按关键词查找正文中的出现位置 |
| 倒排索引 | Inverted Index | 倒排索引 | 从词到出现位置的索引结构，用于全文检索 |
| 向量检索 | Vector Search | 向量检索 | 按语义相似度查找内容，需要嵌入模型；本项目不采用 |
| 嵌入 | Embedding | 嵌入 / Embedding | 把文本转换成向量的模型输出 |

## 开发管理（strict-spec-driven）

| 中文 | English | 常用写法 | 说明 |
|---|---|---|---|
| 路线图 | Roadmap | 路线图 | |
| 里程碑 | Milestone | 里程碑 | |
| 计划变更 | Planned Change | 计划变更 | 路线图中尚未开始的工作项 |
| 变更 | Change | 变更 | 一次提案到归档的开发单位 |
| 规格 | Spec | 规格 | 可观察行为的要求 |
| 用户场景 | Scene | 用户场景 | strict-spec-driven 中描述用户操作流程的产物；为与小说"场景"区分，统一称"用户场景" |
