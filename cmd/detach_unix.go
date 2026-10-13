//go:build !windows

package cmd

import (
	"os/exec"
	"syscall"
)

// detach runs c in its own session, so it outlives the command that starts it.
func detach(c *exec.Cmd) { c.SysProcAttr = &syscall.SysProcAttr{Setsid: true} }
