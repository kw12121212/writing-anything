---
name: release
description: 对 writing-anything 仓库执行一次正式发布（Release）：完整回归、把 dev 合并到 main、确定版本号、本地构建发布产物、用 gh CLI 创建 GitHub Release，最后提交推送。用户要求「正式发布」「发版」「发布新版本」时使用。本项目不使用 CI/CD，发布全部在本地完成。
---

# release：正式发布

对 writing-anything 仓库执行一次正式发布（Release）。本项目不使用持续集成与持续交付（CI/CD）：仓库中没有任何工作流文件，发布全部在本地完成。发布产物（Release Asset）由 `internal/tools/release` 交叉编译（Cross-compilation），GitHub Release 用 gh CLI 创建。本技能只执行发布，不在发布过程中修改规格或代码；发现缺陷时停止发布，先走正常开发流程修复。

## 步骤

1. 前置检查，任何一项不满足时停止，先处理再发布：
   - 工作树干净：`git status --short` 没有输出。
   - 当前在 `dev` 且与远端同步：`git fetch origin && git status --short --branch` 没有 ahead 或 behind。
   - `main` 与远端同步：`git fetch origin main && git diff --stat main origin/main` 没有输出。
   - gh 已认证：`gh auth status`，失败时按其输出运行 `gh auth login`。
2. 回归测试（Regression Test）：在 `dev` 上运行完整回归命令 `go vet ./... && go build ./... && go test -coverprofile=coverage.out ./... && go run ./internal/tools/covercheck -min 80 -profile coverage.out`。失败时先修复再发布，不要带着失败的回归合并。完整回归包含完成标准对账（Criteria Reconciliation）：`internal/tools/criteria` 的 `TestCriteriaReconciliation` 核对 `.strict-spec-driven/roadmap/archive/milestones` 下每个已归档里程碑的每条完成标准（Done Criteria）都在对账表（Reconciliation Table）`internal/tools/criteria/testdata/criteria.yaml` 中映射到存在、且首层没有无条件 `t.Skip` 的测试函数；可用 `go run ./internal/tools/criteria` 单独运行并列出全部问题。新里程碑归档（或修改已归档的完成标准）时，必须在同一变更中同步补表或重新确认映射；对账失败说明有里程碑归档后没有补表、已归档的完成标准被改写，或被引用的测试函数被改名、删除或改成无条件跳过，此时停止发布，回 `dev` 按正常开发流程补表后再发布，不要在发布过程中改表。
3. Windows 安装校验：完整回归在没有 PowerShell（pwsh）的机器上会跳过 `install.ps1` 的测试，这一步确保它真正运行过。先用 `command -v pwsh`（Windows 上为 `Get-Command pwsh`）检测 pwsh，再运行 `WA_REQUIRE_PWSH=1 go test ./internal/tools/release -run TestInstallPs1 -count=1 -v`（在 PowerShell 中写作 `$env:WA_REQUIRE_PWSH='1'; go test ./internal/tools/release -run TestInstallPs1 -count=1 -v`），退出码为 0 且输出含 `--- PASS: TestInstallPs1` 才算通过；该测试以 `-File`、iex 与 `irm <url> | iex` 三种方式运行安装脚本（Install Script），覆盖 amd64、arm64 与各类失败用例。缺少 pwsh 时设置了 `WA_REQUIRE_PWSH=1` 的测试会失败而不是跳过：此时停止发布，不要跳过本步继续。请发布者安装 PowerShell 7 后重跑本步（Linux 可把官方 `powershell-<版本>-linux-x64.tar.gz` 解压到用户目录，不需要 sudo；在 Windows 本机执行同样需要 PowerShell 7，Windows 自带的 Windows PowerShell 5.1 不够）。安装 pwsh 不改动工作树，重跑本步即可继续。本步不修改仓库文件；在新的环境（如 Windows 本机）首次通过时，发布结束后在 `dev` 上按正常开发流程把 pwsh 版本、架构与结果补进 docs/INSTALL.md 的「Windows 验证记录」。
4. 确定版本号：用 `git tag --list 'v*' --sort=-v:refname` 找最新标签（Tag）。没有任何标签时建议 `v0.1.0`；已有标签 `vX.Y.Z` 时按语义化版本（Semantic Versioning）建议下一个：破坏性变更升 X，新功能升 Y，缺陷修复升 Z。建议值必须经发布者确认，不要代替发布者选号。
5. 合并：`git switch main && git merge --no-ff dev -m "chore: release <版本号>"`。出现冲突时停止，把冲突交给发布者处理，不要自行取舍。
6. 打标签：`git tag -a <版本号> -m "wa-cli <版本号>"`。
7. 本地构建发布产物：`go run ./internal/tools/release -version <版本号> -out dist`，生成六个平台（linux、darwin、windows 各 amd64 与 arm64）的 `wa-cli-<os>-<arch>` 二进制与校验文件（Checksum File）`SHA256SUMS`；随后构建并打包 dsh-writing-viewer 插件包（Plugin Package）：`pnpm --dir plugins/dsh-writing-viewer run build && pnpm --dir plugins/dsh-writing-viewer pack --pack-destination ../../dist`，在仓库根的 `dist/` 生成 `dsh-writing-viewer-<插件版本>.tgz`（插件版本取自其 package.json，独立于发布 tag）。注意 `--pack-destination` 的相对路径以 `--dir` 指定的插件目录为基准解析，必须写 `../../dist` 才会落进仓库根的 `dist/`（写成 `dist` 会落进插件自身被 git 忽略的构建目录，第 10 步核对会失败）。缺少 pnpm 或任一构建失败时不要推送：用 `git tag -d <版本号>` 删除本地标签、`git switch dev`，在 `main` 上执行 `git reset --hard origin/main` 复位后回 `dev` 修复，再从头发布。
8. 推送：`git push origin main dev && git push origin <版本号>`。推送前向发布者做最后一次对外发布确认（见下节）。
9. 创建 GitHub Release：`gh release create <版本号> dist/* --title <版本号> --generate-notes`；版本号带连字符（预发布版（Prerelease），如 `v0.1.0-rc1`）时加 `--prerelease`。
10. 核对：`gh release view <版本号>` 确认产物齐全（六个平台二进制 + `SHA256SUMS` + `dsh-writing-viewer-<插件版本>.tgz`，tarball 以 dist/ 内实际文件名为准）；发布者愿意时，可用安装脚本冒烟验证：`curl -fsSL https://raw.githubusercontent.com/kw12121212/writing-anything/main/install.sh | sh`，安装的应为新版本；Windows 上可用 `irm https://raw.githubusercontent.com/kw12121212/writing-anything/main/install.ps1 | iex` 做同样的验证。`dist/` 已被 git 忽略，核对后可删除。

## 必须停下来问发布者

- 最终版本号：建议值只是起点，发布者确认后才能打标签。
- 第 8 步推送前的对外发布确认：推送标签并创建 GitHub Release 后撤回困难（会影响已下载的用户），必须得到发布者明确同意。
- 是否按预发布版发布：版本号带连字符时默认加 `--prerelease`，是否如此由发布者决定。
- 仓库状态与预期不符（如 `main` 上有不是来自 `dev` 的提交、`dev` 落后于预期）时停止并说明情况，不要强行合并。

## 参考

- 术语与中英文写法遵循 docs/GLOSSARY.md。
- 安装渠道与安装脚本的说明见 docs/INSTALL.md。
- main 与 dev 的分支约定见 AGENTS.md 的「分支」一节。
