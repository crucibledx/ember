package cli

import (
	"bytes"
	"regexp"
	"runtime"
	"runtime/debug"
	"testing"

	"go.opentelemetry.io/collector/otelcol"
)

func TestVersion(t *testing.T) {
	info := BuildInfo{
		Version: "0.1.0",
		Commit:  "734252e",
		Date:    "2026-11-26T10:00:00Z",
		OtelCol: "0.166.0",
		OS:      "darwin",
		Arch:    "arm64",
	}
	tests := []struct {
		name string
		args []string
		want string
	}{
		{
			name: "text",
			args: []string{"version"},
			want: "ember 0.1.0 (734252e, 2026-11-26T10:00:00Z) otelcol 0.166.0 darwin/arm64\n",
		},
		{
			name: "json",
			args: []string{"version", "--json"},
			want: `{"version":"0.1.0","commit":"734252e","date":"2026-11-26T10:00:00Z","otelcol":"0.166.0","os":"darwin","arch":"arm64"}` + "\n",
		},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			root := NewRootCommand(info, otelcol.CollectorSettings{})
			var out bytes.Buffer
			root.SetOut(&out)
			root.SetArgs(tt.args)
			if err := root.Execute(); err != nil {
				t.Fatalf("execute: %v", err)
			}
			if got := out.String(); got != tt.want {
				t.Errorf("output\n got: %q\nwant: %q", got, tt.want)
			}
		})
	}
}

func TestNewBuildInfo(t *testing.T) {
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
		want                 BuildInfo
	}{
		{
			name: "linker flags win over the VCS stamp", version: "0.1.0", commit: "fff000", day: "2026-11-26", build: stamped,
			want: BuildInfo{Version: "0.1.0", Commit: "fff000", Date: "2026-11-26", OtelCol: "0.166.0"},
		},
		{
			name: "VCS stamp fills commit and date", version: "0.1.0-dev", build: stamped,
			want: BuildInfo{Version: "0.1.0-dev", Commit: "abc123", Date: "2026-11-25T09:00:00Z", OtelCol: "0.166.0"},
		},
		{
			name: "modified checkout is marked dirty", version: "0.1.0-dev", build: dirty,
			want: BuildInfo{Version: "0.1.0-dev", Commit: "abc123-dirty", Date: unknown, OtelCol: unknown},
		},
		{
			name: "no build information", version: "0.1.0-dev",
			want: BuildInfo{Version: "0.1.0-dev", Commit: unknown, Date: unknown, OtelCol: unknown},
		},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			tt.want.OS, tt.want.Arch = runtime.GOOS, runtime.GOARCH
			if got := NewBuildInfo(tt.version, tt.commit, tt.day, tt.build); got != tt.want {
				t.Errorf("got  %+v\nwant %+v", got, tt.want)
			}
		})
	}
}

func TestNewBuildInfoReadsLinkedCollector(t *testing.T) {
	build, ok := debug.ReadBuildInfo()
	if !ok {
		t.Fatal("no build information in the test binary")
	}
	got := NewBuildInfo("0.1.0", "", "", build).OtelCol
	if !regexp.MustCompile(`^0\.\d+\.\d+$`).MatchString(got) {
		t.Errorf("otelcol version = %q, want the linked otelcol module version", got)
	}
}
