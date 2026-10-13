//go:build !windows

package cmd

import (
	"os"
	"syscall"
)

// execHelper replaces this process with the helper binary; it returns only on
// failure.
func execHelper(path string, argv []string) error { return syscall.Exec(path, argv, os.Environ()) }
