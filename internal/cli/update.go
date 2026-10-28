package cli

import (
	"context"
	"errors"
	"fmt"
	"os"
	"time"

	"github.com/sliplane/cli/internal/buildinfo"
	"github.com/sliplane/cli/internal/update"
	"github.com/spf13/cobra"
	"golang.org/x/term"
)

func (o *options) updateCommand() *cobra.Command {
	var background bool
	command := &cobra.Command{
		Use: "update", Short: "Update Sliplane to the latest release", Args: validateArguments,
		Long:              "Update Sliplane to the latest signed release. Releases also update themselves in the background once a day; set SLIPLANE_NO_AUTO_UPDATE=1 to turn that off.",
		PersistentPreRunE: o.checkOutput,
		RunE: func(command *cobra.Command, _ []string) error {
			if buildinfo.Release != "true" {
				return errors.New("this build was not installed from a Sliplane release; update it the way you installed it")
			}
			ctx := command.Context()
			if background {
				var cancel context.CancelFunc
				ctx, cancel = context.WithTimeout(ctx, 10*time.Minute)
				defer cancel()
			}
			executable, err := update.Executable()
			if err != nil {
				return err
			}
			updater := update.New(executable)
			latest, err := updater.Latest(ctx)
			if err != nil {
				return err
			}
			updated := update.Newer(latest, buildinfo.Version)
			if updated {
				if !background && o.output == "text" {
					fmt.Fprintf(command.ErrOrStderr(), "Downloading Sliplane %s…\n", latest)
				}
				if err := updater.Install(ctx, latest); err != nil {
					return err
				}
				if background {
					if path, err := update.StatePath(); err == nil {
						return update.Record(path, buildinfo.Version)
					}
					return nil
				}
			}
			if o.output == "json" {
				version := buildinfo.Version
				if updated {
					version = latest
				}
				return writeJSON(command.OutOrStdout(), map[string]any{"version": version, "previousVersion": buildinfo.Version, "updated": updated})
			}
			if !updated {
				_, err = fmt.Fprintf(command.OutOrStdout(), "Already up to date: Sliplane %s\n", terminalText(buildinfo.Version))
				return err
			}
			_, err = fmt.Fprintf(command.OutOrStdout(), "Updated Sliplane %s → %s\n", terminalText(buildinfo.Version), latest)
			return err
		},
	}
	command.Flags().BoolVar(&background, "background", false, "Update silently; used by automatic updates")
	_ = command.Flags().MarkHidden("background")
	return command
}

// autoUpdate announces the last background update and, at most once a day,
// starts the next one in a detached process so that no command waits for it.
// The new version takes effect from the next command.
func autoUpdate(root *cobra.Command, args []string) {
	if buildinfo.Release != "true" || update.Disabled() {
		return
	}
	if target, _, err := root.Find(args); err == nil && target.Parent() == root && target.Name() == "update" {
		return
	}
	path, err := update.StatePath()
	if err != nil {
		return
	}
	if term.IsTerminal(int(os.Stderr.Fd())) {
		if replaced, ok := update.Replaced(path, buildinfo.Version); ok {
			fmt.Fprintf(os.Stderr, "Updated Sliplane %s → %s\n", terminalText(replaced), terminalText(buildinfo.Version))
		}
	}
	if !update.Claim(path, time.Now()) {
		return
	}
	if executable, err := update.Executable(); err == nil {
		_ = update.Start(executable)
	}
}
