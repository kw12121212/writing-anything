// Package version 保存 wa-cli 的版本号。
package version

// Version 是 wa-cli 的版本号。发布构建通过
// -ldflags "-X github.com/kw12121212/writing-anything/internal/version.Version=<v>" 注入，
// 未注入时为 dev。
var Version = "dev"
