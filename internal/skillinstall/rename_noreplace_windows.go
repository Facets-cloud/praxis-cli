package skillinstall

import "golang.org/x/sys/windows"

// renameNoReplace moves from to to and fails when to exists: MoveFileEx
// without MOVEFILE_REPLACE_EXISTING.
func renameNoReplace(from, to string) error {
	f, err := windows.UTF16PtrFromString(from)
	if err != nil {
		return err
	}
	t, err := windows.UTF16PtrFromString(to)
	if err != nil {
		return err
	}
	return windows.MoveFileEx(f, t, 0)
}
