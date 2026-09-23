// Build metadata is embedded by the linker and available without config or credentials.
package cli

import (
	"errors"
	"fmt"

	"github.com/spf13/cobra"
	"sliplane-cli/internal/buildinfo"
)

// Plain go builds use this default; make build replaces it through -ldflags -X.
var commit = "unknown"

// versionCommand skips config loading so build details remain available when setup is broken.
func (o *options) versionCommand() *cobra.Command {
	return &cobra.Command{
		Use: "version", Short: "Show the compiled version and commit hash", Args: validateArguments,
		PersistentPreRunE: func(_ *cobra.Command, _ []string) error {
			if o.output != "text" && o.output != "json" {
				return errors.New("--output must be text or json")
			}
			return nil
		},
		RunE: func(command *cobra.Command, _ []string) error {
			if o.output == "json" {
				return writeJSON(command.OutOrStdout(), map[string]string{"version": buildinfo.Version, "commit": commit})
			}
			_, err := fmt.Fprintf(command.OutOrStdout(), "sliplane %s (commit %s)\n", terminalText(buildinfo.Version), terminalText(commit))
			return err
		},
	}
}
