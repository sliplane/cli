package cli

import (
	"encoding/json"
	"fmt"
	"io"
	"strings"
	"text/tabwriter"
	"time"
	"unicode"

	"github.com/spf13/cobra"
)

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

func writeJSON(writer io.Writer, value any) error {
	encoder := json.NewEncoder(writer)
	encoder.SetEscapeHTML(false)
	return encoder.Encode(value)
}

// formatTimestamp formats API timestamps for display, preserving missing or unrecognized values.
func formatTimestamp(value string) string {
	timestamp, err := time.Parse(time.RFC3339Nano, value)
	if err != nil {
		return value
	}
	return formatLocalTime(timestamp)
}

// formatLocalTime displays a timestamp in the machine's local timezone, including its zone name.
func formatLocalTime(value time.Time) string {
	if value.IsZero() {
		return ""
	}
	return value.Local().Format("2006-01-02 15:04:05 MST")
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
