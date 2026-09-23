// Argument validation uses the same required <values> and optional [values] shown in help.
package cli

import (
	"errors"
	"fmt"
	"strings"

	"github.com/spf13/cobra"
)

// validateArguments checks fixed positional arguments before config, credentials, or network access.
// Command Use strings must list required <arguments> followed by optional [arguments].
func validateArguments(command *cobra.Command, args []string) error {
	parameters := strings.Fields(command.Use)[1:]
	var missing []string
	for index, parameter := range parameters {
		if index >= len(args) && strings.HasPrefix(parameter, "<") {
			missing = append(missing, strings.ReplaceAll(strings.Trim(parameter, "<>"), "-", " "))
		}
	}
	message := ""
	if len(missing) == 1 {
		message = "missing required argument: " + missing[0]
	}
	if len(missing) > 1 {
		message = "missing required arguments: " + strings.Join(missing, ", ")
	}
	if len(args) > len(parameters) {
		message = "too many arguments; if a value contains spaces, wrap it in quotes"
		if len(parameters) == 0 {
			message = "this command does not take positional arguments"
		}
	}
	if message == "" {
		return nil
	}
	message += "\n\nUsage:\n  " + command.UseLine()
	if command.Example != "" {
		message += "\n\nExamples:\n" + command.Example
	}
	message += fmt.Sprintf("\n\nRun '%s --help' for details.", command.CommandPath())
	return errors.New(message)
}
