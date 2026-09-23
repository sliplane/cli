package main

import (
	"context"
	"errors"
	"fmt"
	"os"
	"os/exec"

	"sliplane-cli/internal/cli"
)

func main() {
	if err := cli.Execute(cli.NewCommand()); err != nil {
		if errors.Is(err, context.Canceled) {
			os.Exit(130)
		}
		fmt.Fprintln(os.Stderr, "Error:", err)
		var processError *exec.ExitError
		if errors.As(err, &processError) && processError.ExitCode() > 0 {
			os.Exit(processError.ExitCode())
		}
		os.Exit(1)
	}
}
