# wa 写作流程改进清单（2026-10-16 复盘）

来源：tesla-flying-car 全流程跑通后的复盘。分「我（Agent 操作）」「wa-cli」「技能与环境」三层，只列可执行项。

## Agent 操作纪律（自查项）

- [ ] 流程关键命令（draft/review/accept/export）绝不 `|| true` 或重定向吞错；每步检查 exit code 与 diagnostics
- [ ] claim 登记时先判来源类型：仅 reference（外部报道）→ 一律 `fact`；`data`/`case` 只留给作者亲历素材
- [ ] 作者经 harness 提问工具做的决定，同步回写 `wa-cli decision add/decide`，保住审计链
- [ ] 提问前先查工作区已有配置（如 defaultGenre），能推断的不问，能合并的合并成一轮
- [ ] 素材里出现互相矛盾的数字（如喷嘴数 10 vs 推进器 4）时，必须在交给作者时显式标记矛盾，不能只靠正文回避
- [ ] 引语若只拿到二手转述（x_search 降级、媒体报道转述），向作者明示信源等级

## wa-cli 待修（按优先级）

- [ ] **P0 catch-22**：~~数字类 claim-candidate 只认 data 类~~ → **上游 36c4585 已修**（fact+素材认领句级候选，HEAD 实测 warning 3→1）。遗留：标题行候选不可消；已安装 v0.2.0 未含修复，待发版更新
- [ ] P1 `brief set --message` 同命令多组时静默丢弃，应报错或支持重复组
- [ ] P1 `claim add` 写入时即校验 kind×source 类型，并建议正确类型
- [ ] P2 小改动的审稿增量重记通道（如 `review record --reaffirm`）
- [ ] P2 `handoff write` 允许 todo 为空（干净收尾）
- [ ] P2 `source add --url`：一手出处机器可校验，claims 可关联 URL 级
- [ ] P3 preview confirmation 事件的 id 为空串

## 技能与环境

- [ ] agent-reach：安装二进制 / mcporter 配 exa / twitter 登录，三选一起步；或技能补「降级到内置 web 工具」的说明
- [ ] PATH 根因：DSH 服务又被精简环境重启。兜底方案 = 在 `~/.nvm/versions/node/v24.12.0/bin/` 重建 wa-cli 符号链接（该目录在最小 PATH 第一位）；根治 = 服务从完整环境启动（systemd user 单元或包装脚本 source ~/.profile）
- [ ] wa 技能补一段「harness 提问工具 ↔ decision 记录」的映射指引

## 本次遗留（非缺陷）

- lint 3 条 warning（claim-candidate）对应论断已登记，作者知情，非阻塞
- 公众号 HTML 版未导出（作者选了 Markdown），需要时：`wa-cli export tesla-flying-car --format wechat-html --json`
