package cli

import (
	"bufio"
	"context"
	"fmt"
	"io"
	"os"
	"os/user"
	"strings"
	"sync/atomic"

	"github.com/go-git/go-billy/v5/memfs"
	"github.com/gohxs/readline"
	"github.com/spf13/cobra"
	"github.com/xo/usql/drivers"
	"github.com/xo/usql/env"
	"github.com/xo/usql/handler"
	"github.com/xo/usql/metacmd"
	"github.com/xo/usql/rline"
	"golang.org/x/term"
)

// runPostgres uses the same usql engine for a shell or a single SQL argument.
func (o *options) runPostgres(command *cobra.Command, dsn, sql string) error {
	currentUser, err := user.Current()
	if err != nil {
		return fmt.Errorf("find SQL shell user: %w", err)
	}
	directory, err := os.Getwd()
	if err != nil {
		return err
	}
	ctx, cancel := context.WithCancel(command.Context())
	defer cancel()
	interactive := sql == "" && o.canPrompt(command)
	lines, err := postgresInput(command, interactive, sql)
	if err != nil {
		return err
	}
	inputEnded := false
	if sql != "" {
		next := lines.N
		lines.N = func() ([]rune, error) {
			line, err := next()
			inputEnded = err == io.EOF
			return line, err
		}
	}
	defer lines.Close()
	if lines.Inst != nil {
		defer lines.Inst.Close()
	}
	stopInput := context.AfterFunc(ctx, func() { _ = lines.Close() })
	defer stopInput()
	// usql settings and its driver registry are process-wide; a CLI runs one session at a time.
	previousStop := env.Get("ON_ERROR_STOP")
	stopOnError := "on"
	if interactive {
		stopOnError = "off"
	}
	_ = env.Set("ON_ERROR_STOP", stopOnError)
	defer env.Set("ON_ERROR_STOP", previousStop)
	previousPager, _ := env.Pget("pager")
	_, _ = env.Pset("pager", "off")
	defer env.Pset("pager", previousPager)
	previousDriver := installPostgresDriver(ctx)
	defer func() { drivers.Available()["postgres"] = previousDriver }()
	shell := handler.New(lines, currentUser, directory, memfs.New(), true)
	defer func() {
		_ = shell.Rollback()
		_ = shell.Close()
	}()
	if err := shell.Open(ctx, dsn); err != nil {
		return err
	}
	if active, ok := ctx.Value(interactiveProcessKey{}).(*atomic.Bool); ok && interactive {
		active.Store(true)
		defer active.Store(false)
	}
	err = shell.Run()
	// At EOF, execute a final statement without a semicolon. An explicit \q discards it.
	if err == nil && inputEnded && shell.Buf().Len > 0 && ctx.Err() == nil {
		err = shell.Execute(ctx, lines.Out, metacmd.Option{}, shell.Buf().Prefix, shell.Buf().String(), false)
	}
	if ctx.Err() != nil {
		return ctx.Err()
	}
	return err
}

func postgresInput(command *cobra.Command, interactive bool, sql string) (*rline.Rline, error) {
	lines := &rline.Rline{Out: command.OutOrStdout(), Err: command.ErrOrStderr(), Int: interactive}
	source := command.InOrStdin()
	if sql != "" {
		source = strings.NewReader(sql)
	}
	input := readline.NewCancelableStdin(source)
	lines.C = input.Close
	if !interactive {
		reader := bufio.NewReader(input)
		lines.N = func() ([]rune, error) {
			line, err := reader.ReadString('\n')
			if err == io.EOF && line != "" {
				err = nil
			}
			return []rune(strings.TrimSuffix(strings.TrimSuffix(line, "\n"), "\r")), err
		}
		return lines, nil
	}
	// canPrompt has already established that stdin is a terminal file.
	file := command.InOrStdin().(*os.File)
	var state *term.State
	editor, err := readline.NewEx(&readline.Config{
		Stdin: input, Stdout: lines.Out, Stderr: lines.Err,
		DisableAutoSaveHistory: true, HistorySearchFold: true, InterruptPrompt: "^C",
		FuncIsTerminal: func() bool { return true },
		FuncMakeRaw: func() error {
			var err error
			state, err = term.MakeRaw(int(file.Fd()))
			return err
		},
		FuncExitRaw: func() error {
			if state != nil {
				return term.Restore(int(file.Fd()), state)
			}
			return nil
		},
		FuncFilterInputRune: func(r rune) (rune, bool) { return r, r != readline.CharCtrlZ },
	})
	if err != nil {
		_ = input.Close()
		return nil, err
	}
	lines.Inst, lines.N, lines.P, lines.S = editor, editor.Operation.Runes, editor.SetPrompt, editor.SaveHistory
	// Closing input wakes the editor; only its owner closes the editor and restores the terminal.
	lines.A = func(completer readline.AutoCompleter) {
		config := editor.Config.Clone()
		config.AutoComplete = completer
		editor.SetConfig(config)
	}
	lines.Pw = func(prompt string) (string, error) {
		password, err := editor.ReadPassword(prompt)
		return string(password), err
	}
	return lines, nil
}
