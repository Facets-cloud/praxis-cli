// Package paths centralizes ~/.praxis filesystem locations.
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

// Ensure returns ~/.praxis, creating it (mode 0700) if missing.
func Ensure() (string, error) {
	d, err := Dir()
	if err != nil {
		return "", err
	}
	if err := os.MkdirAll(d, 0700); err != nil {
		return "", err
	}
	return d, nil
}

// Config is the per-user config file (PRAXIS_API_URL, defaults).
func Config() (string, error) {
	d, err := Dir()
	return filepath.Join(d, "config.json"), err
}

// Credentials is the bearer-token store.
func Credentials() (string, error) {
	d, err := Dir()
	return filepath.Join(d, "credentials"), err
}

// Installed is the receipt of skills installed across AI hosts.
func Installed() (string, error) {
	d, err := Dir()
	return filepath.Join(d, "installed.json"), err
}

// InstallReceipt records how the binary itself was installed (brew/curl/etc).
func InstallReceipt() (string, error) {
	d, err := Dir()
	return filepath.Join(d, "install.json"), err
}
