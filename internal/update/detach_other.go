//go:build !unix

package update

import "os/exec"

func detach(*exec.Cmd) {}
