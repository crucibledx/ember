// Package cli holds Ember's commands.
package cli

import (
	"github.com/spf13/cobra"
	"go.opentelemetry.io/collector/otelcol"
)

// NewRootCommand returns the `ember` command with its subcommands.
func NewRootCommand(info BuildInfo, collector otelcol.CollectorSettings) *cobra.Command {
	root := &cobra.Command{
		Use:               "ember",
		Short:             "OpenTelemetry for coding agents",
		SilenceUsage:      true,
		CompletionOptions: cobra.CompletionOptions{DisableDefaultCmd: true},
	}
	root.AddCommand(newVersionCommand(info), newRunCommand(collector))
	return root
}
