// Package raptorstate mirrors the raptor CLI's auth
// configuration (~/.facets/credentials + FACETS_* env vars), so `praxis
// status` can report which control plane raptor commands will hit and
// whether that matches the active praxis profile's URL.
//
// praxis and raptor keep deliberately independent credential stores. This
// package is the only place that touches raptor's file, and it only ever ADDS
// to it: SeedProfile adds a section that is absent, and nothing here ever
// edits or removes one that exists. Otherwise it reads: it mirrors
// raptor's own profile-resolution rules (raptor/pkg/config.Config.GetProfile)
// for status reporting, and PAT() hands `praxis login` the control-plane token
// of a resolved profile (see PAT for why that is not a State field):
//
//  1. env override — CONTROL_PLANE_URL set (FACETS_USERNAME/FACETS_TOKEN
//     optional with FACETS_HEADERS)
//  2. FACETS_PROFILE env var
//  3. the [default] section
//  4. the sole profile, when exactly one exists
//  5. none — raptor itself would error
//
// One praxis-side addition sits between 1 and 2: a praxis profile may pin a
// raptor profile (`raptor_profile` key, set via `praxis login
// --raptor-profile`). The pin does not change what a BARE raptor command
// does — it is a contract with the AI host, which is instructed to prefix
// every raptor command with `FACETS_PROFILE=<pin>`. That prefix beats a
// bare FACETS_PROFILE in the environment, so the pin is reported above it.
package raptorstate

import (
	"fmt"
	"net/url"
	"os"
	"os/exec"
	"path/filepath"
	"strings"

	"github.com/Facets-cloud/praxis-cli/internal/credentials"
)

// Source labels which rule resolved the raptor profile. Mirrors the order
// documented on the package.
type Source string

const (
	SourceEnv        Source = "env"         // CONTROL_PLANE_URL override
	SourcePin        Source = "pin"         // praxis profile's raptor_profile
	SourceEnvProfile Source = "env-profile" // FACETS_PROFILE
	SourceDefault    Source = "default"     // [default] section
	SourceSole       Source = "sole"        // the only profile in the file
)

// State is raptor's effective auth configuration as visible from praxis.
type State struct {
	// Installed reports whether the raptor binary is on PATH.
	Installed bool
	// Found reports whether a profile (or env override) resolved. When
	// false with Pinned true, the praxis-side pin names a profile raptor
	// doesn't have — Profile still carries the pinned name so callers can
	// say WHICH profile is missing.
	Found           bool
	Profile         string
	Source          Source
	ControlPlaneURL string
	Username        string
	// Pinned reports that resolution went through (or was attempted via)
	// the praxis profile's raptor_profile pairing.
	Pinned bool
}

// lookPath is a seam over exec.LookPath so tests control "is raptor
// installed" without depending on the machine's PATH.
var lookPath = exec.LookPath

// DefaultPath returns raptor's credentials file location,
// $HOME/.facets/credentials.
func DefaultPath() (string, error) {
	home, err := os.UserHomeDir()
	if err != nil {
		return "", err
	}
	return filepath.Join(home, ".facets", "credentials"), nil
}

// Resolve reports raptor's effective auth state. pinnedProfile is the active
// praxis profile's raptor_profile pairing ("" when unset). It never fails:
// an unreadable or missing credentials file simply yields Found=false (plus
// whatever the env override provides).
func Resolve(pinnedProfile string) State {
	path, err := DefaultPath()
	if err != nil {
		path = ""
	}
	return resolve(pinnedProfile, path)
}

// resolve is the testable core — the credentials path is explicit so tests
// never touch the real ~/.facets.
func resolve(pinnedProfile, credentialsPath string) State {
	st := State{}
	if _, err := lookPath("raptor"); err == nil {
		st.Installed = true
	}

	// 1. Full env override — raptor requires only CONTROL_PLANE_URL here.
	if cpURL := os.Getenv("CONTROL_PLANE_URL"); cpURL != "" {
		st.Found = true
		st.Profile = "env"
		st.Source = SourceEnv
		st.ControlPlaneURL = cpURL
		st.Username = os.Getenv("FACETS_USERNAME")
		return st
	}

	profiles := loadProfiles(credentialsPath)

	// 2. praxis-side pin. Reported even when the named profile is missing
	// from raptor's store (Found=false, Pinned=true) so status can warn.
	if pinnedProfile != "" {
		st.Pinned = true
		st.Profile = pinnedProfile
		st.Source = SourcePin
		if p, ok := profiles[pinnedProfile]; ok {
			st.Found = true
			st.ControlPlaneURL = p["control_plane_url"]
			st.Username = p["username"]
		}
		return st
	}

	// 3. FACETS_PROFILE. Like the pin, a name that doesn't exist is still
	// reported (raptor itself would error on it).
	if name := os.Getenv("FACETS_PROFILE"); name != "" {
		st.Profile = name
		st.Source = SourceEnvProfile
		if p, ok := profiles[name]; ok {
			st.Found = true
			st.ControlPlaneURL = p["control_plane_url"]
			st.Username = p["username"]
		}
		return st
	}

	// 4. [default] section.
	if p, ok := profiles["default"]; ok {
		st.Found = true
		st.Profile = "default"
		st.Source = SourceDefault
		st.ControlPlaneURL = p["control_plane_url"]
		st.Username = p["username"]
		return st
	}

	// 5. Sole profile.
	if len(profiles) == 1 {
		for name, p := range profiles {
			st.Found = true
			st.Profile = name
			st.Source = SourceSole
			st.ControlPlaneURL = p["control_plane_url"]
			st.Username = p["username"]
		}
		return st
	}

	// 6. Nothing resolved — zero or multiple profiles without a selector.
	return st
}

// PAT returns the (username, control-plane PAT) pair stored for the named
// profile; ok=false when the file, the section, or either value is missing.
// `praxis login` authenticates with it before falling back to minting a Praxis
// API key. Deliberately a separate call rather than a State field, so the token
// can't ride along into anything that prints State (e.g. `praxis status`).
func PAT(profile string) (username, token string, ok bool) {
	path, err := DefaultPath()
	if err != nil {
		return "", "", false
	}
	p, found := loadProfiles(path)[profile]
	if !found || p["username"] == "" || p["token"] == "" {
		return "", "", false
	}
	return p["username"], p["token"], true
}

// HasProfile reports whether raptor's credentials file contains the named
// profile. Used by `praxis login --raptor-profile` validation.
func HasProfile(name string) (bool, string) {
	path, err := DefaultPath()
	if err != nil {
		return false, ""
	}
	return hasProfile(name, path)
}

func hasProfile(name, credentialsPath string) (bool, string) {
	p, ok := loadProfiles(credentialsPath)[name]
	if !ok {
		return false, ""
	}
	return true, p["control_plane_url"]
}

// loadProfiles parses raptor's INI credentials file into raw sections.
// Missing or unreadable file yields an empty map — callers treat that the
// same as "no profiles".
func loadProfiles(path string) map[string]map[string]string {
	if path == "" {
		return map[string]map[string]string{}
	}
	data, err := os.ReadFile(path)
	if err != nil {
		return map[string]map[string]string{}
	}
	return credentials.ParseRawINI(data)
}

// MatchesHost reports whether two URLs point at the same host,
// case-insensitively. Used to compare the praxis profile URL with raptor's
// control_plane_url; praxis URLs are stored canonical (post-redirect), so
// host equality is the right granularity. Either side unparseable or
// hostless → false.
func MatchesHost(praxisURL, cpURL string) bool {
	a, err := url.Parse(strings.TrimSpace(praxisURL))
	if err != nil || a.Host == "" {
		return false
	}
	b, err := url.Parse(strings.TrimSpace(cpURL))
	if err != nil || b.Host == "" {
		return false
	}
	return strings.EqualFold(a.Host, b.Host)
}

// SeedProfile writes the control-plane PAT praxis just verified into raptor's
// credentials file, under the section raptor would READ (pin > FACETS_PROFILE >
// [default] — resolve()'s order, minus the env override that bypasses the file).
// Returns wrote=false when that section already exists: it may hold a
// credential for another control plane, which praxis has no standing to replace.
func SeedProfile(pinnedProfile, cpURL, username, token string) (bool, error) {
	path, err := DefaultPath()
	if err != nil {
		return false, err
	}
	return seedProfile(path, seedTarget(pinnedProfile), cpURL, username, token)
}

// seedTarget names the section to write. It must agree with resolve() above, or
// praxis writes [default] while a FACETS_PROFILE shell reads somewhere else.
func seedTarget(pinnedProfile string) string {
	if pinnedProfile != "" {
		return pinnedProfile
	}
	if name := os.Getenv("FACETS_PROFILE"); name != "" {
		return name
	}
	return "default"
}

// seedProfile is the testable core — the path is explicit so tests never touch
// the real ~/.facets.
func seedProfile(path, name, cpURL, username, token string) (bool, error) {
	if name == "" || cpURL == "" || username == "" || token == "" {
		return false, nil
	}
	data, err := os.ReadFile(path)
	if err != nil && !os.IsNotExist(err) {
		return false, err
	}
	if _, exists := credentials.ParseRawINI(data)[name]; exists {
		return false, nil
	}

	// Separator only when appending to existing content, and a newline first if
	// the file did not end with one.
	sep := ""
	if n := len(data); n > 0 {
		sep = "\n"
		if data[n-1] != '\n' {
			sep = "\n\n"
		}
	}
	section := fmt.Sprintf("%s[%s]\ncontrol_plane_url = %s\nusername = %s\ntoken = %s\n",
		sep, name, cpURL, username, token)
	return true, writeAtomic(path, append(data, section...))
}

// writeAtomic mirrors credentials.Save: a partial write here would truncate
// raptor's OTHER profiles, which is the loss the never-overwrite rule exists to
// prevent. 0600 because the file holds a control-plane token.
func writeAtomic(path string, data []byte) error {
	dir := filepath.Dir(path)
	if err := os.MkdirAll(dir, 0o700); err != nil {
		return err
	}
	tmp, err := os.CreateTemp(dir, ".credentials-*.tmp")
	if err != nil {
		return err
	}
	if _, err := tmp.Write(data); err != nil {
		tmp.Close()
		os.Remove(tmp.Name())
		return err
	}
	if err := tmp.Close(); err != nil {
		os.Remove(tmp.Name())
		return err
	}
	if err := os.Chmod(tmp.Name(), 0o600); err != nil {
		os.Remove(tmp.Name())
		return err
	}
	return os.Rename(tmp.Name(), path)
}
