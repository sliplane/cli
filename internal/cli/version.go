package cli

import (
	"errors"
	"fmt"

	"github.com/sliplane/cli/internal/buildinfo"
	"github.com/spf13/cobra"
)

func (o *options) versionCommand() *cobra.Command {
	return &cobra.Command{
		Use: "version", Short: "Show the compiled version and commit hash", Args: validateArguments,
		PersistentPreRunE: o.checkOutput,
		RunE: func(command *cobra.Command, _ []string) error {
			if o.output == "json" {
				return writeJSON(command.OutOrStdout(), map[string]string{"version": buildinfo.Version, "commit": buildinfo.Commit})
			}
			_, err := fmt.Fprintf(command.OutOrStdout(), "sliplane %s (commit %s)\n", terminalText(buildinfo.Version), terminalText(buildinfo.Commit))
			return err
		},
	}
}

// checkOutput replaces config loading for commands that work without config or credentials.
func (o *options) checkOutput(_ *cobra.Command, _ []string) error {
	if o.output != "text" && o.output != "json" {
		return errors.New("--output must be text or json")
	}
	return nil
}
