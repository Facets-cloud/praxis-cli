package cmd

import (
	"os/exec"
	"syscall"

	"golang.org/x/sys/windows"
)

// detach runs c without a console and in its own process group, so it outlives
// the command that starts it.
func detach(c *exec.Cmd) {
	c.SysProcAttr = &syscall.SysProcAttr{CreationFlags: windows.DETACHED_PROCESS | windows.CREATE_NEW_PROCESS_GROUP, HideWindow: true}
}
