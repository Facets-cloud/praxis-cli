package cmd

import "errors"

// execHelper is never reached on Windows: selfupdate.WriteInPlaceSafe is false
// there, and replaceFile renames the running binary aside instead.
func execHelper(string, []string) error {
	return errors.New("praxis does not hand off an update on Windows")
}
