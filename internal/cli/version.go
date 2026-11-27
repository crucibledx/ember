package cli

import (
	"encoding/json"
	"fmt"

	"github.com/spf13/cobra"

	"github.com/crucibledx/ember/internal/buildinfo"
)

func newVersionCommand(info buildinfo.Info) *cobra.Command {
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
