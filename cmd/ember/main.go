// Command ember is Ember's binary: a custom OpenTelemetry Collector distribution for coding agents.
package main

import (
	"os"
	"runtime/debug"

	"go.opentelemetry.io/collector/component"
	"go.opentelemetry.io/collector/confmap"
	"go.opentelemetry.io/collector/confmap/provider/envprovider"
	"go.opentelemetry.io/collector/confmap/provider/fileprovider"
	"go.opentelemetry.io/collector/confmap/provider/yamlprovider"
	"go.opentelemetry.io/collector/otelcol"

	"github.com/crucibledx/ember/internal/buildinfo"
	"github.com/crucibledx/ember/internal/cli"
)

// Set by release builds with -ldflags "-X main.version=... -X main.commit=... -X main.date=...". Empty commit and
// date fall back to the VCS stamp Go embeds when building from a git checkout.
var (
	version = "0.1.0-dev"
	commit  string
	date    string
)

func main() {
	goBuild, _ := debug.ReadBuildInfo()
	info := buildinfo.New(version, commit, date, goBuild)
	if err := cli.NewRootCommand(info, collectorSettings(info)).Execute(); err != nil {
		os.Exit(1)
	}
}

func collectorSettings(info buildinfo.Info) otelcol.CollectorSettings {
	return otelcol.CollectorSettings{
		BuildInfo: component.BuildInfo{
			Command:     "ember",
			Description: "Ember, OpenTelemetry for coding agents",
			Version:     info.Version,
		},
		Factories: components,
		ConfigProviderSettings: otelcol.ConfigProviderSettings{
			ResolverSettings: confmap.ResolverSettings{
				ProviderFactories: []confmap.ProviderFactory{
					envprovider.NewFactory(),
					fileprovider.NewFactory(),
					yamlprovider.NewFactory(),
				},
				DefaultScheme: "env",
			},
		},
	}
}
