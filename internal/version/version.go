// Package version 保存 wa-cli 的版本号与提交信息。
package version

import "runtime/debug"

// Version 与 Commit 是 wa-cli 的版本号与提交。发布构建通过
// -ldflags "-X github.com/kw12121212/writing-anything/internal/version.Version=<v> -X github.com/kw12121212/writing-anything/internal/version.Commit=<c>"
// 注入；未注入时由 Info 回退到 Go 构建信息。
var (
	Version = "dev"
	Commit  = "unknown"
)

// Info 返回当前 wa-cli 的版本与提交，规则见 Resolve。
func Info() (version, commit string) {
	info, _ := debug.ReadBuildInfo()
	return Resolve(Version, Commit, info)
}

// Resolve 按「构建时注入的值 → Go 构建信息 → dev/unknown」的顺序得出版本与提交：
// injectedVersion 不为 dev 时直接使用，否则取 info 的主模块版本（为空或为 (devel) 时视为缺失）；
// injectedCommit 不为 unknown 时直接使用，否则取 info 中的 vcs.revision，vcs.modified 为 true 时加 -dirty 后缀。
func Resolve(injectedVersion, injectedCommit string, info *debug.BuildInfo) (version, commit string) {
	version, commit = injectedVersion, injectedCommit
	if info == nil {
		return version, commit
	}
	if version == "dev" && info.Main.Version != "" && info.Main.Version != "(devel)" {
		version = info.Main.Version
	}
	if commit == "unknown" {
		var revision string
		var modified bool
		for _, s := range info.Settings {
			switch s.Key {
			case "vcs.revision":
				revision = s.Value
			case "vcs.modified":
				modified = s.Value == "true"
			}
		}
		if revision != "" {
			commit = revision
			if modified {
				commit += "-dirty"
			}
		}
	}
	return version, commit
}
