// Signal handling lets an interactive psql child receive Ctrl-C without canceling its parent.
package cli

import (
	"context"
	"os"
	"os/signal"
	"sync/atomic"
	"syscall"

	"github.com/spf13/cobra"
)

type interactiveProcessKey struct{}

// Execute cancels commands on interruption unless a foreground shell owns interactive Ctrl-C.
func Execute(command *cobra.Command) error {
	ctx, cancel := context.WithCancel(context.Background())
	defer cancel()
	var interactive atomic.Bool
	ctx = context.WithValue(ctx, interactiveProcessKey{}, &interactive)
	interrupts := make(chan os.Signal, 1)
	signal.Notify(interrupts, os.Interrupt, syscall.SIGTERM)
	defer signal.Stop(interrupts)
	go func() {
		for {
			select {
			case <-ctx.Done():
				return
			case interrupt := <-interrupts:
				if interrupt == os.Interrupt && interactive.Load() {
					// Terminal-generated SIGINT also reaches psql in the foreground process group.
					continue
				}
				cancel()
			}
		}
	}()
	return command.ExecuteContext(ctx)
}
