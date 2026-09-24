package cli

import (
	"bufio"
	"context"
	"errors"
	"fmt"
	"io"
	"os"
	"strings"

	"charm.land/huh/v2"
	"github.com/spf13/cobra"
	"golang.org/x/term"
)

// runPrompt binds a terminal field to the command and treats Ctrl+C as cancellation.
func runPrompt(command *cobra.Command, field huh.Field) error {
	form := huh.NewForm(huh.NewGroup(field)).WithInput(command.InOrStdin()).WithOutput(command.ErrOrStderr())
	err := form.RunWithContext(command.Context())
	if command.Context().Err() != nil {
		return command.Context().Err()
	}
	if errors.Is(err, huh.ErrUserAborted) {
		return context.Canceled
	}
	return err
}

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
		line, err := readPromptLine(ctx, reader)
		if errors.Is(err, io.EOF) {
			return false, nil
		}
		if err != nil {
			return false, fmt.Errorf("read confirmation: %w", err)
		}

		switch strings.ToLower(strings.TrimSpace(line)) {
		case "y", "yes":
			return true, ctx.Err()
		case "", "n", "no":
			return false, nil
		}
	}
}

// readPromptLine lets cancellation interrupt terminal input without blocking the command.
func readPromptLine(ctx context.Context, reader *bufio.Reader) (string, error) {
	if err := ctx.Err(); err != nil {
		return "", err
	}
	type response struct {
		line string
		err  error
	}
	answer := make(chan response, 1)
	go func() { line, err := reader.ReadString('\n'); answer <- response{line, err} }()
	select {
	case <-ctx.Done():
		return "", ctx.Err()
	case result := <-answer:
		return result.line, result.err
	}
}
