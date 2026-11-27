package buildinfo

import (
	"runtime"
	"runtime/debug"
	"testing"
)

func TestNew(t *testing.T) {
	stamped := &debug.BuildInfo{
		Deps: []*debug.Module{
			{Path: "github.com/spf13/cobra", Version: "v1.10.2"},
			{Path: otelcolModule, Version: "v0.166.0"},
		},
		Settings: []debug.BuildSetting{
			{Key: "vcs.revision", Value: "abc123"},
			{Key: "vcs.time", Value: "2026-11-25T09:00:00Z"},
			{Key: "vcs.modified", Value: "false"},
		},
	}
	dirty := &debug.BuildInfo{Settings: []debug.BuildSetting{
		{Key: "vcs.revision", Value: "abc123"},
		{Key: "vcs.modified", Value: "true"},
	}}
	tests := []struct {
		name                 string
		version, commit, day string
		build                *debug.BuildInfo
		want                 Info
	}{
		{
			name: "linker flags win over the VCS stamp", version: "0.1.0", commit: "fff000", day: "2026-11-26", build: stamped,
			want: Info{Version: "0.1.0", Commit: "fff000", Date: "2026-11-26", OtelCol: "0.166.0"},
		},
		{
			name: "VCS stamp fills commit and date", version: "0.1.0-dev", build: stamped,
			want: Info{Version: "0.1.0-dev", Commit: "abc123", Date: "2026-11-25T09:00:00Z", OtelCol: "0.166.0"},
		},
		{
			name: "modified checkout is marked dirty", version: "0.1.0-dev", build: dirty,
			want: Info{Version: "0.1.0-dev", Commit: "abc123-dirty", Date: unknown, OtelCol: unknown},
		},
		{
			name: "no build information", version: "0.1.0-dev",
			want: Info{Version: "0.1.0-dev", Commit: unknown, Date: unknown, OtelCol: unknown},
		},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			tt.want.OS, tt.want.Arch = runtime.GOOS, runtime.GOARCH
			if got := New(tt.version, tt.commit, tt.day, tt.build); got != tt.want {
				t.Errorf("got  %+v\nwant %+v", got, tt.want)
			}
		})
	}
}
