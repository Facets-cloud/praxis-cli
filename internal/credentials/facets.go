package credentials

import (
	"fmt"
	"os"
	"path/filepath"
)

// ReadFacetsProfile reads ~/.facets/credentials and returns the (url, username, token)
// for the named profile (default "default"). Returns an error if the file or profile
// is missing or the username/token are empty.
//
// The facets/raptor credentials file is the same INI shape as ~/.praxis/credentials,
// but its keys are control_plane_url / username / token.
func ReadFacetsProfile(profile string) (url, username, token string, err error) {
	if profile == "" {
		profile = DefaultProfileName
	}
	home, err := os.UserHomeDir()
	if err != nil {
		return "", "", "", err
	}
	path := filepath.Join(home, ".facets", "credentials")
	data, err := os.ReadFile(path)
	if err != nil {
		return "", "", "", fmt.Errorf("read %s: %w", path, err)
	}
	sections := parseRawINI(data)
	kv, ok := sections[profile]
	if !ok {
		return "", "", "", fmt.Errorf("profile %q not found in %s", profile, path)
	}
	url, username, token = kv["control_plane_url"], kv["username"], kv["token"]
	if username == "" || token == "" {
		return "", "", "", fmt.Errorf("profile %q in %s is missing username or token", profile, path)
	}
	return url, username, token, nil
}
