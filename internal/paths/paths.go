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

// Config is the per-user CLI configuration (currently just the Praxis
// deployment URL override).
func Config() (string, error) {
	d, err := Dir()
	return filepath.Join(d, "config.json"), err
}

// Installed is the JSON receipt of skills installed across AI hosts.
// Used by `praxis skill install/uninstall/list-installed`.
func Installed() (string, error) {
	d, err := Dir()
	return filepath.Join(d, "installed.json"), err
}

// MCPTools is the snapshot of the gateway's /v1/mcp/manifest, written by
// install-skill / refresh-skills so AI hosts can grep the file for tool
// names instead of fetching live on every invocation.
func MCPTools() (string, error) {
	d, err := Dir()
	return filepath.Join(d, "mcp-tools.json"), err
}
