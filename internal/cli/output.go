// Output is deliberately plain: JSON values or tab-aligned human-readable content.
package cli

import (
	"encoding/json"
	"fmt"
	"io"
	"strings"
	"text/tabwriter"
	"unicode"

	"github.com/spf13/cobra"
)

// print emits one JSON document or a table, never progress or status decorations.
func (o *options) print(command *cobra.Command, value any, rows [][]string) error {
	if o.output == "json" {
		return writeJSON(command.OutOrStdout(), value)
	}
	writer := tabwriter.NewWriter(command.OutOrStdout(), 0, 4, 2, ' ', 0)
	for _, row := range rows {
		cells := make([]string, len(row))
		for index, cell := range row {
			cells[index] = terminalText(cell)
		}
		if _, err := fmt.Fprintln(writer, strings.Join(cells, "\t")); err != nil {
			return err
		}
	}
	return writer.Flush()
}

// writeJSON encodes a complete value on one line, also suitable for log streaming.
func writeJSON(writer io.Writer, value any) error {
	encoder := json.NewEncoder(writer)
	encoder.SetEscapeHTML(false)
	return encoder.Encode(value)
}

// terminalText prevents remote names or log messages from injecting terminal control sequences.
func terminalText(value string) string {
	return strings.Map(func(character rune) rune {
		if unicode.IsControl(character) {
			return ' '
		}
		return character
	}, value)
}
