package cli

import (
	"context"
	"fmt"
	"os/exec"
	"runtime"
	"time"
)

// openBrowser invokes the platform URL handler without passing the URL through a shell.
func openBrowser(ctx context.Context, address string) error {
	ctx, cancel := context.WithTimeout(ctx, 5*time.Second)
	defer cancel()
	var command *exec.Cmd
	switch runtime.GOOS {
	case "darwin":
		command = exec.CommandContext(ctx, "open", address)
	case "linux":
		command = exec.CommandContext(ctx, "xdg-open", address)
	case "windows":
		command = exec.CommandContext(ctx, "rundll32", "url.dll,FileProtocolHandler", address)
	default:
		return fmt.Errorf("browser login is unsupported on %s", runtime.GOOS)
	}
	return command.Run()
}
