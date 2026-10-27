---
name: release
description: 对 writing-anything 仓库执行一次正式发布（Release）：完整回归、把 dev 合并到 main、确定版本号、本地构建发布产物、用 gh CLI 创建 GitHub Release，最后提交推送。用户要求「正式发布」「发版」「发布新版本」时使用。构建与 GitHub Release 在本地完成；npm 包在 Release 发布后由仓库唯一的 GitHub Actions 工作流自动发布。
---

# release：正式发布

对 writing-anything 仓库执行一次正式发布（Release）。本项目除 npm 发布外不使用持续集成与持续交付（CI/CD）：仓库中只有 `.github/workflows/publish-npm.yml` 一个工作流，它在 GitHub Release 发布后经 npm Trusted Publishing（OIDC）把 `plugins/dsh-writing-viewer` 发布到 npm；构建与 GitHub Release 全部在本地完成。发布产物（Release Asset）由 `internal/tools/release` 交叉编译（Cross-compilation），GitHub Release 用 gh CLI 创建。本技能只执行发布，不在发布过程中修改规格或代码；发现缺陷时停止发布，先走正常开发流程修复。

## 步骤

1. 前置检查，任何一项不满足时停止，先处理再发布：
   - 工作树干净：`git status --short` 没有输出。
   - 当前在 `dev` 且与远端同步：`git fetch origin && git status --short --branch` 没有 ahead 或 behind。
   - `main` 与远端同步：`git fetch origin main && git diff --stat main origin/main` 没有输出。
   - `main` 只接收过从 `dev` 的合并：`git log --no-merges --oneline origin/dev..origin/main` 没有输出。有输出说明有人直接在 `main` 上提交，按「必须停下来问发布者」处理。
   - gh 已认证：`gh auth status`，失败时按其输出运行 `gh auth login`。
2. 回归测试（Regression Test）：在 `dev` 上运行完整回归命令 `go vet ./... && go build ./... && go test -coverprofile=coverage.out ./... && go run ./internal/tools/covercheck -min 80 -profile coverage.out`。失败时先修复再发布，不要带着失败的回归合并。完整回归包含完成标准对账（Criteria Reconciliation）：`internal/tools/criteria` 的 `TestCriteriaReconciliation` 核对 `.strict-spec-driven/roadmap/archive/milestones` 下每个已归档里程碑的每条完成标准（Done Criteria）都在对账表（Reconciliation Table）`internal/tools/criteria/testdata/criteria.yaml` 中映射到存在、且首层没有无条件 `t.Skip` 的测试函数；可用 `go run ./internal/tools/criteria` 单独运行并列出全部问题。新里程碑归档（或修改已归档的完成标准）时，必须在同一变更中同步补表或重新确认映射；对账失败说明有里程碑归档后没有补表、已归档的完成标准被改写，或被引用的测试函数被改名、删除或改成无条件跳过，此时停止发布，回 `dev` 按正常开发流程补表后再发布，不要在发布过程中改表。
3. Windows 安装校验：完整回归在没有 PowerShell（pwsh）的机器上会跳过 `install.ps1` 的测试，这一步确保它真正运行过。先用 `command -v pwsh`（Windows 上为 `Get-Command pwsh`）检测 pwsh，再运行 `WA_REQUIRE_PWSH=1 go test ./internal/tools/release -run TestInstallPs1 -count=1 -v`（在 PowerShell 中写作 `$env:WA_REQUIRE_PWSH='1'; go test ./internal/tools/release -run TestInstallPs1 -count=1 -v`），退出码为 0 且输出含 `--- PASS: TestInstallPs1` 才算通过；该测试以 `-File`、iex 与 `irm <url> | iex` 三种方式运行安装脚本（Install Script），覆盖 amd64、arm64 与各类失败用例。缺少 pwsh 时设置了 `WA_REQUIRE_PWSH=1` 的测试会失败而不是跳过：此时停止发布，不要跳过本步继续。请发布者安装 PowerShell 7 后重跑本步（Linux 可把官方 `powershell-<版本>-linux-x64.tar.gz` 解压到用户目录，不需要 sudo；在 Windows 本机执行同样需要 PowerShell 7，Windows 自带的 Windows PowerShell 5.1 不够）。安装 pwsh 不改动工作树，重跑本步即可继续。本步不修改仓库文件；在新的环境（如 Windows 本机）首次通过时，发布结束后在 `dev` 上按正常开发流程把 pwsh 版本、架构与结果补进 docs/INSTALL.md 的「Windows 验证记录」。
4. 确定版本号：用 `git tag --list 'v*' --sort=-v:refname` 找最新标签（Tag）。没有任何标签时建议 `v0.1.0`；已有标签 `vX.Y.Z` 时按语义化版本（Semantic Versioning）建议下一个：破坏性变更升 X，新功能升 Y，缺陷修复升 Z。建议值必须经发布者确认，不要代替发布者选号。版本号确认后校验插件版本对齐：`plugins/dsh-writing-viewer/package.json` 与 `plugins/zcode-writing-viewer/package.json` 的 `version` 都必须等于版本号去掉前导 `v`（如 v0.4.0 对应 0.4.0，可用 `python3 -c "import json;print(json.load(open('plugins/dsh-writing-viewer/package.json'))['version'], json.load(open('plugins/zcode-writing-viewer/package.json'))['version'])"` 读取；zcode 插件还需与 `.zcode-plugin/plugin.json` 的 `version` 一致）。同时校验两个插件 README 的安装地址：`grep -n 'dsh-writing-viewer-[0-9]' plugins/dsh-writing-viewer/README.md` 与 `grep -n 'zcode-writing-viewer-[0-9]' plugins/zcode-writing-viewer/README.md` 命中的安装命令必须使用各自 `插件名-<插件版本>.tgz`，否则发布后 `latest/download` 下旧文件名会 404。任一不一致时停止发布：回 `dev` 把插件版本号与 README 对齐作为常规变更提交推送后，再从头发布；不要在发布过程中代改版本号或文档。
5. 合并：`git switch main && git merge --no-ff dev -m "chore: release <版本号>"`。出现冲突时停止，把冲突交给发布者处理，不要自行取舍。合并后校验 `main` 与 `dev` 内容完全一致：`git diff --quiet dev main` 退出码为 0，保证打标签的正是第 2、3 步回归过的代码；不一致时用 `git reset --hard origin/main` 撤销合并，`git switch dev` 后停止并说明情况。
6. 打标签：`git tag -a <版本号> -m "wa-cli <版本号>"`。
7. 本地构建发布产物：`go run ./internal/tools/release -version <版本号> -out dist`，生成六个平台（linux、darwin、windows 各 amd64 与 arm64）的 `wa-cli-<os>-<arch>` 二进制与校验文件（Checksum File）`SHA256SUMS`；随后构建并打包 dsh-writing-viewer 插件包（Plugin Package）：`pnpm --dir plugins/dsh-writing-viewer run build && pnpm --dir plugins/dsh-writing-viewer pack --pack-destination ../../dist`，在仓库根的 `dist/` 生成 `dsh-writing-viewer-<插件版本>.tgz`（插件版本与发布 tag 对齐，第 4 步已校验）；再构建并打包 zcode-writing-viewer 插件包：`pnpm --dir plugins/zcode-writing-viewer run build && pnpm --dir plugins/zcode-writing-viewer pack --pack-destination ../../dist`，其构建会从 `internal/skills/skills` 原样捆绑全部内置 wa-* 技能并核对名单，在 `dist/` 生成 `zcode-writing-viewer-<插件版本>.tgz`。注意 `--pack-destination` 的相对路径以 `--dir` 指定的插件目录为基准解析，必须写 `../../dist` 才会落进仓库根的 `dist/`（写成 `dist` 会落进插件自身被 git 忽略的构建目录，第 10 步核对会失败）。缺少 pnpm 或任一构建失败时不要推送：仍在 `main` 上依次执行 `git tag -d <版本号>`（删除本地标签）、`git reset --hard origin/main`（撤销本地合并），再 `git switch dev` 回 `dev` 修复，然后从头发布。注意必须先复位再切换分支：切到 `dev` 后再执行 `git reset --hard` 复位的是 `dev`，会丢失 `dev` 上的提交。
8. 推送：`git push --atomic origin main <版本号>`，`main` 与标签要么一起推送成功，要么都不推送；`dev` 在发布过程中没有变化，不需要推送。推送前向发布者做最后一次对外发布确认（见下节）。
9. 创建 GitHub Release：`gh release create <版本号> dist/* --title <版本号> --generate-notes`；版本号带连字符（预发布版（Prerelease），如 `v0.1.0-rc1`）时加 `--prerelease`。这一步同时触发仓库唯一的 GitHub Actions 工作流 `.github/workflows/publish-npm.yml`：它在 GitHub 托管 runner 上校验插件版本与发布标签一致、安装插件依赖、跑插件回归，然后经 npm Trusted Publishing（OIDC）把 `plugins/dsh-writing-viewer` 发布到 npm（稳定版发到 `latest` 标签，预发布版发到 `next`，不覆盖 `latest`；溯源（Provenance）声明由 npm 自动生成）。仓库里没有任何 npm token，也不需要提供；认证依据是 npm 侧一次性配置的 Trusted Publisher（GitHub Actions、用户 `kw12121212`、仓库 `writing-anything`、工作流文件名 `publish-npm.yml`，并允许直接 `npm publish` 而不只是 `npm stage publish`）。
10. 核对：`gh release view <版本号>` 确认产物齐全（六个平台二进制 + `SHA256SUMS` + `dsh-writing-viewer-<插件版本>.tgz` + `zcode-writing-viewer-<插件版本>.tgz`，tarball 以 dist/ 内实际文件名为准）；再核对 npm 发布：`gh run list --workflow publish-npm.yml --limit 1` 确认这次 Release 触发的运行成功，`npm view dsh-writing-viewer version` 与 `npm view dsh-writing-viewer dist-tags --json` 确认版本与标签（稳定版为 `latest`，预发布版为 `next`），`npm view dsh-writing-viewer repository.url` 应指回 `github.com/kw12121212/writing-anything`（awesome-dsh-plugin 的 npm 映射校验依赖这一条）。发布者愿意时，可用安装脚本冒烟验证：`curl -fsSL https://raw.githubusercontent.com/kw12121212/writing-anything/main/install.sh | sh`，再运行安装脚本输出的路径（默认 `"$HOME/.local/bin/wa-cli" --version`，避免 PATH 中更靠前的旧 wa-cli 干扰），输出应为 `wa-cli <版本号> (commit <提交>)`；Windows 上可用 `irm https://raw.githubusercontent.com/kw12121212/writing-anything/main/install.ps1 | iex` 做同样的验证。`dist/` 已被 git 忽略，核对后可删除。

## 已发布版本不可改写

GitHub Release 的产物必须由对应标签在 `main` 上的源码构建。发布后发现缺陷（包括产物命名、版本号、文档错误）时，不要在已发布的 Release 上补传、替换或删除产物，也不要移动或重建已推送的标签：先在 `dev` 按正常开发流程修复，再用本技能发布一个修订版本（如 v0.4.0 → v0.4.1）。唯一例外是上传中断导致产物缺失：可以从同一标签重新构建（`git switch --detach <版本号>` 后按第 7 步构建）并补传缺失的产物，其余产物保持不变。npm 上已发布的版本同理不可改写：同一版本号不能再发一次，发布出问题时发修订版本。

## 必须停下来问发布者

- 最终版本号：建议值只是起点，发布者确认后才能打标签。
- 第 8 步推送前的对外发布确认：推送标签并创建 GitHub Release 后撤回困难（会影响已下载的用户），必须得到发布者明确同意。
- 是否按预发布版发布：版本号带连字符时默认加 `--prerelease`，是否如此由发布者决定。
- 仓库状态与预期不符（如 `main` 上有不是来自 `dev` 的提交、`dev` 落后于预期）时停止并说明情况，不要强行合并。

## 参考

- 术语与中英文写法遵循 docs/GLOSSARY.md。
- 安装渠道与安装脚本的说明见 docs/INSTALL.md。
- main 与 dev 的分支约定见 AGENTS.md 的「分支」一节。
