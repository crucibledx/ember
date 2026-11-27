// Package buildinfo identifies an ember build: release version, commit, build date and the linked collector version.
package buildinfo

import (
	"fmt"
	"runtime"
	"runtime/debug"
	"strings"
)

const (
	unknown       = "unknown"
	otelcolModule = "go.opentelemetry.io/collector/otelcol"
)

// Info identifies an ember build. Its JSON form is the output of `ember version --json`.
type Info struct {
	Version string `json:"version"`
	Commit  string `json:"commit"`
	Date    string `json:"date"`
	OtelCol string `json:"otelcol"`
	OS      string `json:"os"`
	Arch    string `json:"arch"`
}

// New combines the values set by linker flags with the Go build information: an empty commit or date comes from
// the VCS stamp, and the collector version from the linked otelcol module.
func New(version, commit, date string, build *debug.BuildInfo) Info {
	info := Info{Version: version, Commit: commit, Date: date, OtelCol: unknown, OS: runtime.GOOS, Arch: runtime.GOARCH}
	if build != nil {
		vcs := map[string]string{}
		for _, setting := range build.Settings {
			vcs[setting.Key] = setting.Value
		}
		if info.Commit == "" && vcs["vcs.revision"] != "" {
			info.Commit = vcs["vcs.revision"]
			if vcs["vcs.modified"] == "true" {
				info.Commit += "-dirty"
			}
		}
		if info.Date == "" {
			info.Date = vcs["vcs.time"]
		}
		for _, dep := range build.Deps {
			if dep.Path == otelcolModule {
				info.OtelCol = strings.TrimPrefix(dep.Version, "v")
			}
		}
	}
	if info.Commit == "" {
		info.Commit = unknown
	}
	if info.Date == "" {
		info.Date = unknown
	}
	return info
}

func (i Info) String() string {
	return fmt.Sprintf("ember %s (%s, %s) otelcol %s %s/%s", i.Version, i.Commit, i.Date, i.OtelCol, i.OS, i.Arch)
}
