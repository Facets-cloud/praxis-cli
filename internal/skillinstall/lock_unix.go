//go:build !windows

package skillinstall

import (
	"os"
	"syscall"
)

// lockFile takes an exclusive lock on f and returns its release.
func lockFile(f *os.File) (func(), error) {
	if err := syscall.Flock(int(f.Fd()), syscall.LOCK_EX); err != nil {
		return nil, err
	}
	return func() { _ = syscall.Flock(int(f.Fd()), syscall.LOCK_UN) }, nil
}
