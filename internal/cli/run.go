package cli

import (
	"github.com/spf13/cobra"
	"go.opentelemetry.io/collector/otelcol"
)

// newRunCommand is the upstream collector command (`--config`, `validate`, `components`) under `ember run`.
func newRunCommand(collector otelcol.CollectorSettings) *cobra.Command {
	cmd := otelcol.NewCommand(collector)
	cmd.Use = "run"
	cmd.Short = "Run the collector in the foreground"
	return cmd
}
