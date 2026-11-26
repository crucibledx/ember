package cli

import (
	"encoding/json"
	"fmt"
	"runtime"
	"runtime/debug"
	"strings"

	"github.com/spf13/cobra"
)

const (
	unknown       = "unknown"
	otelcolModule = "go.opentelemetry.io/collector/otelcol"
)

// BuildInfo identifies an ember build. Its JSON form is the output of `ember version --json`.
type BuildInfo struct {
	Version string `json:"version"`
	Commit  string `json:"commit"`
	Date    string `json:"date"`
	OtelCol string `json:"otelcol"`
	OS      string `json:"os"`
	Arch    string `json:"arch"`
}

// NewBuildInfo combines the values set by linker flags with the Go build information: an empty commit or date
// comes from the VCS stamp, and the collector version from the linked otelcol module.
func NewBuildInfo(version, commit, date string, build *debug.BuildInfo) BuildInfo {
	info := BuildInfo{Version: version, Commit: commit, Date: date, OtelCol: unknown, OS: runtime.GOOS, Arch: runtime.GOARCH}
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

func (b BuildInfo) String() string {
	return fmt.Sprintf("ember %s (%s, %s) otelcol %s %s/%s", b.Version, b.Commit, b.Date, b.OtelCol, b.OS, b.Arch)
}

func newVersionCommand(info BuildInfo) *cobra.Command {
	var asJSON bool
	cmd := &cobra.Command{
		Use:   "version",
		Short: "Print the ember and collector versions",
		Args:  cobra.NoArgs,
		RunE: func(cmd *cobra.Command, _ []string) error {
			out := cmd.OutOrStdout()
			if asJSON {
				if err := json.NewEncoder(out).Encode(info); err != nil {
					return fmt.Errorf("write version: %w", err)
				}
				return nil
			}
			if _, err := fmt.Fprintln(out, info); err != nil {
				return fmt.Errorf("write version: %w", err)
			}
			return nil
		},
	}
	cmd.Flags().BoolVar(&asJSON, "json", false, "print the version as JSON")
	return cmd
}
