package cli

import (
	"bufio"
	"context"
	"errors"
	"fmt"
	"io"
	"os"
	"strings"

	"github.com/spf13/cobra"
	"golang.org/x/term"
)

// canPrompt keeps redirected commands and JSON output noninteractive.
func (o *options) canPrompt(command *cobra.Command) bool {
	if !o.canShowProgress(command) {
		return false
	}
	file, ok := command.InOrStdin().(*os.File)
	return ok && term.IsTerminal(int(file.Fd()))
}

// canShowProgress keeps terminal updates out of JSON and redirected output, independent of stdin.
func (o *options) canShowProgress(command *cobra.Command) bool {
	if o.output != "text" {
		return false
	}
	for _, stream := range []io.Writer{command.OutOrStdout(), command.ErrOrStderr()} {
		file, ok := stream.(*os.File)
		if !ok || !term.IsTerminal(int(file.Fd())) {
			return false
		}
	}
	return true
}

// confirm requires an explicit yes and lets cancellation interrupt a blocked input read.
func confirm(ctx context.Context, input io.Reader, output io.Writer, question string) (bool, error) {
	reader := bufio.NewReader(input)
	for {
		if err := ctx.Err(); err != nil {
			return false, err
		}
		if _, err := fmt.Fprintf(output, "%s [y/N] ", question); err != nil {
			return false, err
		}
		type response struct {
			line string
			err  error
		}
		answer := make(chan response, 1)
		go func() {
			line, err := reader.ReadString('\n')
			answer <- response{line, err}
		}()
		select {
		case <-ctx.Done():
			return false, ctx.Err()
		case result := <-answer:
			if errors.Is(result.err, io.EOF) {
				return false, nil
			}
			if result.err != nil {
				return false, fmt.Errorf("read confirmation: %w", result.err)
			}
			switch strings.ToLower(strings.TrimSpace(result.line)) {
			case "y", "yes":
				return true, ctx.Err()
			case "", "n", "no":
				return false, nil
			}
		}
	}
}
