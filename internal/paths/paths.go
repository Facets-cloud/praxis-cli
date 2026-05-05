// Package paths centralizes ~/.praxis filesystem locations. Only the helpers
// actually called by Phase 1 commands live here; later phases should add
// their own paths (skill receipt, server config, etc.) as they need them.
package paths

import (
	"os"
	"path/filepath"
)

const dirName = ".praxis"

// Dir returns ~/.praxis (does not create it).
func Dir() (string, error) {
	home, err := os.UserHomeDir()
	if err != nil {
		return "", err
	}
	return filepath.Join(home, dirName), nil
}

// Credentials is the bearer-token store. Used by `praxis logout`; will
// also be used by `praxis login` once that lands.
func Credentials() (string, error) {
	d, err := Dir()
	return filepath.Join(d, "credentials"), err
}
