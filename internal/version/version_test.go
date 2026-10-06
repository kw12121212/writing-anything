package version

import (
	"runtime/debug"
	"testing"
)

func TestDefaults(t *testing.T) {
	if Version != "dev" || Commit != "unknown" {
		t.Fatalf("Version, Commit = %q, %q; want dev, unknown", Version, Commit)
	}
}

func buildInfo(version string, settings ...string) *debug.BuildInfo {
	info := &debug.BuildInfo{Main: debug.Module{Path: "github.com/kw12121212/writing-anything", Version: version}}
	for i := 0; i+1 < len(settings); i += 2 {
		info.Settings = append(info.Settings, debug.BuildSetting{Key: settings[i], Value: settings[i+1]})
	}
	return info
}

func TestResolve(t *testing.T) {
	tests := []struct {
		name                      string
		injectedVersion, injected string
		info                      *debug.BuildInfo
		wantVersion, wantCommit   string
	}{
		{"injected wins", "v1.2.3", "abc1234", buildInfo("v9.9.9", "vcs.revision", "fff"), "v1.2.3", "abc1234"},
		{"go install module version", "dev", "unknown", buildInfo("v1.2.3"), "v1.2.3", "unknown"},
		{"source build revision", "dev", "unknown", buildInfo("(devel)", "vcs.revision", "abc1234", "vcs.modified", "false"), "dev", "abc1234"},
		{"dirty source build", "dev", "unknown", buildInfo("v0.0.0-20260930-abc1234+dirty", "vcs.revision", "abc1234", "vcs.modified", "true"), "v0.0.0-20260930-abc1234+dirty", "abc1234-dirty"},
		{"empty build info", "dev", "unknown", buildInfo(""), "dev", "unknown"},
		{"no build info", "dev", "unknown", nil, "dev", "unknown"},
		{"injected version with build revision", "v1.2.3", "unknown", buildInfo("(devel)", "vcs.revision", "abc1234"), "v1.2.3", "abc1234"},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			v, c := Resolve(tt.injectedVersion, tt.injected, tt.info)
			if v != tt.wantVersion || c != tt.wantCommit {
				t.Fatalf("Resolve = %q, %q; want %q, %q", v, c, tt.wantVersion, tt.wantCommit)
			}
		})
	}
}

func TestInfo(t *testing.T) {
	info, _ := debug.ReadBuildInfo()
	wantV, wantC := Resolve(Version, Commit, info)
	if v, c := Info(); v != wantV || c != wantC {
		t.Fatalf("Info = %q, %q; want %q, %q", v, c, wantV, wantC)
	}
}
