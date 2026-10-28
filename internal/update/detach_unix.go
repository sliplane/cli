//go:build unix

package update

import (
	"os/exec"
	"syscall"
)

// detach starts command in a new session, so Ctrl+C in the terminal cannot stop an update halfway.
func detach(command *exec.Cmd) {
	command.SysProcAttr = &syscall.SysProcAttr{Setsid: true}
}
