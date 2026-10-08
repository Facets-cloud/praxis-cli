package selfupdate

import (
	"os"
	"path/filepath"
	"strconv"
	"time"
)

// replaceFile puts newPath at currentPath. Windows cannot overwrite or delete a
// running .exe, but it can rename one: the running binary moves aside to
// <name>.old-<time>, and the new file takes its place. CleanupOld removes the
// old file at a later start. A failed move puts the old binary back.
func replaceFile(newPath, currentPath string) error {
	old := currentPath + ".old-" + strconv.FormatInt(time.Now().UnixNano(), 36)
	if err := os.Rename(currentPath, old); err != nil && !os.IsNotExist(err) {
		return err
	}
	if err := os.Rename(newPath, currentPath); err != nil {
		_ = os.Rename(old, currentPath)
		return err
	}
	return nil
}

// CleanupOld removes binaries that replaceFile renamed aside next to path.
// One that still runs cannot be removed and stays for the next start.
func CleanupOld(path string) {
	olds, _ := filepath.Glob(path + ".old-*")
	for _, o := range olds {
		_ = os.Remove(o)
	}
}

// Writable reports whether the user may write to the folder or file at path.
// Windows has no access(2): a folder is tested with a temporary file, a file by
// opening it for writing (a running .exe reports false).
func Writable(path string) bool {
	info, err := os.Stat(path)
	if err != nil {
		return false
	}
	if info.IsDir() {
		f, err := os.CreateTemp(path, ".praxis-write-test-*")
		if err != nil {
			return false
		}
		f.Close()
		_ = os.Remove(f.Name())
		return true
	}
	f, err := os.OpenFile(path, os.O_WRONLY, 0)
	if err != nil {
		return false
	}
	f.Close()
	return true
}

// OwnedByRoot is false on Windows, which has no user id 0.
func OwnedByRoot(string) bool { return false }
