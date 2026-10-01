#!/bin/sh
# wa-cli 安装脚本（Install Script），适用于 Linux 与 macOS：
# 识别操作系统与 CPU 架构，下载对应的发布产物（Release Asset）与 SHA256SUMS，
# 校验 SHA-256 后安装到用户级目录，不需要 root 权限。
#
#   curl -fsSL https://raw.githubusercontent.com/kw12121212/writing-anything/main/install.sh | sh
#
# 环境变量：
#   WA_VERSION      要安装的版本，如 v1.2.3；默认最新版本
#   WA_INSTALL_DIR  安装目录；默认 $HOME/.local/bin
#   WA_BASE_URL     发布地址；默认 https://github.com/kw12121212/writing-anything/releases
set -eu

fail() {
	printf 'wa-cli 安装失败：%s\n' "$1" >&2
	exit 1
}

# 全部逻辑放在 main 中、最后一行才调用：通过 curl | sh 运行时，下载中断不会执行半截脚本。
main() {
	base="${WA_BASE_URL:-https://github.com/kw12121212/writing-anything/releases}"
	version="${WA_VERSION:-latest}"
	install_dir="${WA_INSTALL_DIR:-}"
	if [ -z "$install_dir" ]; then
		[ -n "${HOME:-}" ] || fail "未设置 HOME，请用 WA_INSTALL_DIR 指定安装目录"
		install_dir="$HOME/.local/bin"
	fi

	os=$(uname -s)
	case "$os" in
	Linux) os=linux ;;
	Darwin) os=darwin ;;
	*) fail "不支持的操作系统：$os（支持 Linux 与 macOS；Windows 请使用 install.ps1）" ;;
	esac

	arch=$(uname -m)
	case "$arch" in
	x86_64 | amd64) arch=amd64 ;;
	aarch64 | arm64) arch=arm64 ;;
	*) fail "不支持的 CPU 架构：$arch（支持 amd64 与 arm64）" ;;
	esac

	asset="wa-cli-$os-$arch"
	if [ "$version" = latest ]; then
		url="$base/latest/download"
	else
		url="$base/download/$version"
	fi

	if command -v curl >/dev/null 2>&1; then
		download() { curl -fsSL -o "$2" "$1"; }
	elif command -v wget >/dev/null 2>&1; then
		download() { wget -q -O "$2" "$1"; }
	else
		fail "需要 curl 或 wget 才能下载"
	fi

	if command -v sha256sum >/dev/null 2>&1; then
		sha256() { sha256sum "$1" | cut -d ' ' -f 1; }
	elif command -v shasum >/dev/null 2>&1; then
		sha256() { shasum -a 256 "$1" | cut -d ' ' -f 1; }
	else
		fail "需要 sha256sum 或 shasum 才能校验"
	fi

	staged=""
	tmp=$(mktemp -d)
	trap 'rm -rf "$tmp"; if [ -n "$staged" ]; then rm -f "$staged"; fi' EXIT
	trap 'exit 1' HUP INT TERM

	download "$url/$asset" "$tmp/$asset" || fail "无法下载 $url/$asset"
	download "$url/SHA256SUMS" "$tmp/SHA256SUMS" || fail "无法下载 $url/SHA256SUMS"

	expected=$(awk -v f="$asset" '{ sub(/\r$/, "") } $2 == f || $2 == "*" f { print $1; exit }' "$tmp/SHA256SUMS")
	[ -n "$expected" ] || fail "SHA256SUMS 中没有 $asset 的记录"
	actual=$(sha256 "$tmp/$asset")
	[ "$actual" = "$expected" ] || fail "校验失败：$asset 的 SHA-256 为 $actual，SHA256SUMS 记录为 $expected"

	target="$install_dir/wa-cli"
	[ ! -d "$target" ] || fail "$target 是目录，无法安装"
	mkdir -p "$install_dir" || fail "无法创建安装目录 $install_dir"
	# 先复制到安装目录中的临时文件再重命名，失败时不改动已有的 wa-cli；临时文件由 EXIT trap 清理。
	staged="$install_dir/.wa-cli.$$"
	cp "$tmp/$asset" "$staged" || fail "无法写入安装目录 $install_dir"
	chmod 755 "$staged" || fail "无法设置 $staged 的执行权限"
	mv -f "$staged" "$target" || fail "无法安装到 $target"
	staged=""

	printf '已安装 wa-cli 到 %s\n' "$target"
	case ":${PATH:-}:" in
	*":$install_dir:"*) ;;
	*)
		printf '提示：%s 不在 PATH 中，请把下面一行加入 shell 配置文件（如 ~/.profile）：\n' "$install_dir"
		# shellcheck disable=SC2016 # $PATH 应原样写进 shell 配置文件，由 shell 启动时展开。
		printf '  export PATH="%s:$PATH"\n' "$install_dir"
		;;
	esac
}

main "$@"
