# wa-cli 安装脚本（Install Script），适用于 Windows：
# 识别 CPU 架构，下载对应的发布产物（Release Asset）与 SHA256SUMS，
# 校验 SHA-256 后安装到用户级目录，不需要管理员权限。
#
#   irm https://raw.githubusercontent.com/kw12121212/writing-anything/main/install.ps1 | iex
#
# 环境变量：
#   WA_VERSION      要安装的版本，如 v1.2.3；默认最新版本
#   WA_INSTALL_DIR  安装目录；默认 %LOCALAPPDATA%\Programs\wa-cli，并加入用户级 PATH
#   WA_BASE_URL     发布地址；默认 https://github.com/kw12121212/writing-anything/releases
#
# 本文件不带 BOM：irm 不会去掉 BOM，带 BOM 时 iex 会把它当作命令报错。

# 全部逻辑放在脚本块中执行：通过 iex 运行时，设置、变量与函数不会留在作者的会话里。
& {
    $ErrorActionPreference = 'Stop'
    $ProgressPreference = 'SilentlyContinue'
    # Windows PowerShell 5.1 默认可能未启用 TLS 1.2，GitHub 要求 TLS 1.2 及以上。
    [Net.ServicePointManager]::SecurityProtocol = [Net.ServicePointManager]::SecurityProtocol -bor [Net.SecurityProtocolType]::Tls12

    # 用 throw 而不是 exit 报告失败：通过 iex 运行时 exit 会关闭作者的 PowerShell 窗口；
    # 以 -File 运行时未捕获的 throw 使进程以非零退出。
    function Fail([string]$Message) {
        throw "wa-cli 安装失败：$Message"
    }

    if ($env:OS -ne 'Windows_NT') {
        Fail '不支持的操作系统（install.ps1 只支持 Windows；Linux 与 macOS 请使用 install.sh）'
    }

    $base = if ($env:WA_BASE_URL) { $env:WA_BASE_URL } else { 'https://github.com/kw12121212/writing-anything/releases' }
    $version = if ($env:WA_VERSION) { $env:WA_VERSION } else { 'latest' }
    $defaultDir = -not $env:WA_INSTALL_DIR
    $installDir = if ($defaultDir) { Join-Path $env:LOCALAPPDATA 'Programs\wa-cli' } else { $env:WA_INSTALL_DIR }

    # 32 位 PowerShell 运行在 64 位 Windows 上时，真实架构在 PROCESSOR_ARCHITEW6432 中。
    $rawArch = if ($env:PROCESSOR_ARCHITEW6432) { $env:PROCESSOR_ARCHITEW6432 } else { $env:PROCESSOR_ARCHITECTURE }
    $arch = switch ($rawArch) {
        'AMD64' { 'amd64' }
        'ARM64' { 'arm64' }
        default { Fail "不支持的 CPU 架构：$rawArch（支持 amd64 与 arm64）" }
    }

    $asset = "wa-cli-windows-$arch.exe"
    $url = if ($version -eq 'latest') { "$base/latest/download" } else { "$base/download/$version" }

    $tmp = Join-Path ([IO.Path]::GetTempPath()) ("wa-cli-" + [Guid]::NewGuid())
    New-Item -ItemType Directory -Path $tmp | Out-Null
    try {
        $assetPath = Join-Path $tmp $asset
        $sumsPath = Join-Path $tmp 'SHA256SUMS'
        foreach ($pair in @(@("$url/$asset", $assetPath), @("$url/SHA256SUMS", $sumsPath))) {
            try {
                Invoke-WebRequest -Uri $pair[0] -OutFile $pair[1] -UseBasicParsing
            } catch {
                Fail "无法下载 $($pair[0])：$($_.Exception.Message)"
            }
        }

        $expected = $null
        foreach ($line in Get-Content -LiteralPath $sumsPath) {
            $fields = $line.Trim() -split '\s+'
            if ($fields.Count -eq 2 -and ($fields[1] -eq $asset -or $fields[1] -eq "*$asset")) {
                $expected = $fields[0].ToLowerInvariant()
                break
            }
        }
        if (-not $expected) {
            Fail "SHA256SUMS 中没有 $asset 的记录"
        }
        $actual = (Get-FileHash -Algorithm SHA256 -LiteralPath $assetPath).Hash.ToLowerInvariant()
        if ($actual -ne $expected) {
            Fail "校验失败：$asset 的 SHA-256 为 $actual，SHA256SUMS 记录为 $expected"
        }

        $target = Join-Path $installDir 'wa-cli.exe'
        if (Test-Path -LiteralPath $target -PathType Container) {
            Fail "$target 是目录，无法安装"
        }
        New-Item -ItemType Directory -Force -Path $installDir | Out-Null
        # 先复制到安装目录中的临时文件再重命名，失败时不改动已有的 wa-cli.exe。
        $staged = Join-Path $installDir ".wa-cli.$PID.exe"
        try {
            Copy-Item -LiteralPath $assetPath -Destination $staged -Force
            Move-Item -LiteralPath $staged -Destination $target -Force
        } catch {
            Remove-Item -LiteralPath $staged -Force -ErrorAction SilentlyContinue
            Fail "无法安装到 ${target}：$($_.Exception.Message)"
        }
        Write-Output "已安装 wa-cli 到 $target"

        if ($defaultDir) {
            # 直接读写注册表中未展开的值并保持 REG_EXPAND_SZ：
            # [Environment]::GetEnvironmentVariable 会展开 %USERPROFILE% 等引用，写回后原有条目会被固化。
            $key = Get-Item -LiteralPath 'HKCU:\Environment'
            $userPath = $key.GetValue('Path', '', 'DoNotExpandEnvironmentNames')
            $entries = @($userPath -split ';' | ForEach-Object { $_.TrimEnd('\') })
            if ($entries -notcontains $installDir.TrimEnd('\')) {
                $newPath = if ($userPath) { "$($userPath.TrimEnd(';'));$installDir" } else { $installDir }
                Set-ItemProperty -LiteralPath 'HKCU:\Environment' -Name 'Path' -Value $newPath -Type ExpandString
                # 设置并删除一个临时变量，借 SetEnvironmentVariable 广播设置变更，新开的终端即可读到新的 PATH。
                [Environment]::SetEnvironmentVariable('WA_CLI_INSTALL', '1', 'User')
                [Environment]::SetEnvironmentVariable('WA_CLI_INSTALL', $null, 'User')
                Write-Output "已把 $installDir 加入用户级 PATH，新开的终端生效"
            }
        } elseif (@($env:Path -split ';' | ForEach-Object { $_.TrimEnd('\') }) -notcontains $installDir.TrimEnd('\')) {
            Write-Output "提示：$installDir 不在 PATH 中，请把它加入 PATH"
        }
    } finally {
        Remove-Item -LiteralPath $tmp -Recurse -Force -ErrorAction SilentlyContinue
    }
}
