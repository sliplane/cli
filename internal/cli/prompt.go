package cli

import (
	"bufio"
	"context"
	"errors"
	"fmt"
	"io"
	"os"
	"strings"

	tea "charm.land/bubbletea/v2"
	"charm.land/huh/v2"
	"github.com/spf13/cobra"
	"golang.org/x/term"
)

func runPrompt(command *cobra.Command, field huh.Field) error {
	if err := command.Context().Err(); err != nil {
		return err
	}
	form := huh.NewForm(huh.NewGroup(field)).WithInput(command.InOrStdin()).WithOutput(command.ErrOrStderr())
	var err error
	if os.Getenv("TERM") == "dumb" {
		err = form.RunWithContext(command.Context())
	} else {
		form.SubmitCmd = tea.Quit
		form.CancelCmd = tea.Quit
		program := tea.NewProgram(promptModel{form},
			tea.WithInput(command.InOrStdin()), tea.WithOutput(command.ErrOrStderr()), tea.WithoutSignalHandler())
		// Huh's RunWithContext force-kills on cancellation. Quit instead so the input reader is joined.
		stopCancellation := context.AfterFunc(command.Context(), program.Quit)
		defer stopCancellation()
		_, err = program.Run()
	}
	if command.Context().Err() != nil {
		return command.Context().Err()
	}
	if form.State == huh.StateAborted || errors.Is(err, huh.ErrUserAborted) {
		return context.Canceled
	}
	return err
}

// promptModel adapts Huh's string view to Bubble Tea while runPrompt owns cancellation.
type promptModel struct {
	form huh.Model
}

func (model promptModel) Init() tea.Cmd { return model.form.Init() }

func (model promptModel) Update(message tea.Msg) (tea.Model, tea.Cmd) {
	form, command := model.form.Update(message)
	return promptModel{form}, command
}

func (model promptModel) View() tea.View {
	var view tea.View
	view.ReportFocus = true
	view.SetContent(model.form.View())
	return view
}

func (o *options) canPrompt(command *cobra.Command) bool {
	if !o.canShowProgress(command) {
		return false
	}
	file, ok := command.InOrStdin().(*os.File)
	return ok && term.IsTerminal(int(file.Fd()))
}

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
