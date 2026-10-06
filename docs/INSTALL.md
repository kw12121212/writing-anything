# 安装 wa-cli

`wa-cli` 是单文件二进制，支持 Linux、macOS、Windows 的 amd64 与 arm64。每个版本在 [GitHub Release](https://github.com/kw12121212/writing-anything/releases) 提供各平台的发布产物（Release Asset）`wa-cli-<os>-<arch>`（Windows 为 `.exe`），以及记录它们 SHA-256 的校验文件（Checksum File）`SHA256SUMS`。

## 用安装脚本安装

安装脚本（Install Script）识别当前平台，下载对应的二进制与 `SHA256SUMS`，校验 SHA-256 一致后才安装；安装到用户级目录，不需要 root 或管理员权限。校验失败时不会改动已安装的 `wa-cli`。

Linux 与 macOS：

```sh
curl -fsSL https://raw.githubusercontent.com/kw12121212/writing-anything/main/install.sh | sh
```

默认安装到 `$HOME/.local/bin/wa-cli`；该目录不在 `PATH` 中时，脚本会打印需要加入 shell 配置文件的一行。

Windows（PowerShell）：

```powershell
irm https://raw.githubusercontent.com/kw12121212/writing-anything/main/install.ps1 | iex
```

默认安装到 `%LOCALAPPDATA%\Programs\wa-cli\wa-cli.exe`，并把该目录加入用户级 `PATH`，新开的终端生效。Windows PowerShell 5.1 与 PowerShell 7 都支持这种方式；如果先下载 `install.ps1` 再以 `-File` 运行，请使用 PowerShell 7（`pwsh -File install.ps1`），Windows PowerShell 5.1 会按系统代码页误读脚本中的中文。

两个脚本都支持以下环境变量：

| 环境变量 | 含义 | 默认值 |
|---|---|---|
| `WA_VERSION` | 要安装的版本，如 `v1.2.3` | 最新版本 |
| `WA_INSTALL_DIR` | 安装目录；Windows 上指定后不修改 `PATH` | 见上文 |
| `WA_BASE_URL` | 发布地址，用于镜像或内网 | `https://github.com/kw12121212/writing-anything/releases` |

例如安装指定版本到自定义目录：

```sh
curl -fsSL https://raw.githubusercontent.com/kw12121212/writing-anything/main/install.sh | WA_VERSION=v1.2.3 WA_INSTALL_DIR="$HOME/bin" sh
```

## 用 go install 安装

已安装 Go 1.27 或更高版本时：

```sh
go install github.com/kw12121212/writing-anything/cmd/wa-cli@latest
```

这种方式不经过发布构建，`wa-cli version` 报告模块版本；提交信息未知时显示为 `unknown`。

## 手动下载与校验

从 Release 页面下载对应平台的二进制与 `SHA256SUMS`，在同一目录中运行（以 linux/amd64 为例；macOS 把 `sha256sum` 换成 `shasum -a 256`）：

```sh
grep ' wa-cli-linux-amd64$' SHA256SUMS | sha256sum -c -
```

## 确认安装

```sh
wa-cli version
```

输出形如 `wa-cli v1.2.3 (commit 0123abc…)`；加 `--json` 时输出含 `version` 与 `commit` 字段。

## 安装技能（Skill）

wa-cli 内置一组技能，告诉 Agent 何时调用哪些 wa-cli 命令、何时停下来询问作者：wa-init、wa-brief、wa-outline、wa-draft、wa-review、wa-revise、wa-publish 与 wa-auto。在项目目录中运行：

```sh
wa-cli skills install --agent claude-code   # 写入 .claude/skills/<技能>/SKILL.md
wa-cli skills install --agent codex         # 写入 .agents/skills/<技能>/SKILL.md（Codex 扫描的位置）
wa-cli skills install --target ~/.claude/skills   # 安装到其他目录（如用户级技能目录）
```

已有的技能文件与内置版本相同时不改动；有技能文件被改过时不写入任何文件，确认后加 `--force` 覆盖。技能目录的格式与通用的 Agent Skills 兼容，也可以用第三方 skills 工具安装；`wa-cli skills list --json` 列出全部技能及其引用的命令。

## 发布新版本（维护者）

正式发布（Release）通过项目技能（Project Skill）`.agents/skills/release` 执行：完整回归通过后把 `dev` 合并到 `main`，打新的 `v*` 标签（Tag），在本地用发布工具构建发布产物并创建 GitHub Release；本项目不使用持续集成与持续交付（CI/CD）。单独在本地生成产物可以用同一发布工具：

```sh
go run ./internal/tools/release -version v1.2.3 -out dist
```

`-commit` 默认取 `git rev-parse HEAD`，`-targets linux/amd64,windows/arm64` 可限定目标。
