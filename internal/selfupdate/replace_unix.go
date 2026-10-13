//go:build !windows

package selfupdate

import (
	"os"
	"syscall"
)

func replaceFile(newPath, currentPath string) error { return os.Rename(newPath, currentPath) }

// CleanupOld does nothing outside Windows: no binary is renamed aside there.
func CleanupOld(string) {}

// Writable reports whether the user may write to the file or folder at path.
// It uses access(2) and never opens the file: on Apple silicon, opening a
// running binary for writing makes macOS kill its next runs.
func Writable(path string) bool {
	const wOK = 0x2
	return syscall.Access(path, wOK) == nil
}

// OwnedByRoot reports whether user id 0 owns the file at path.
func OwnedByRoot(path string) bool {
	info, err := os.Stat(path)
	if err != nil {
		return false
	}
	st, ok := info.Sys().(*syscall.Stat_t)
	return ok && st.Uid == 0
}
