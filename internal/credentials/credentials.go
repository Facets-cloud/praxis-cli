// Package credentials manages the per-user, multi-profile credentials store
// at ~/.praxis/credentials. INI format matching ~/.facets/credentials so
// users coming from facets-cli have zero learning curve.
//
//	[default]
//	url      = https://askpraxis.ai
//	username = anshul@facets.cloud
//	token    = sk_live_…
//
//	[acme]
//	url      = https://acme.console.facets.cloud
//	username = support@acme.com
//	token    = sk_live_…
//
// Active-profile resolution (highest priority first):
//
//  1. --profile flag passed to a command
//  2. PRAXIS_PROFILE environment variable
//  3. ~/.praxis/config "default profile" pointer (set by `praxis use`)
//  4. literal "default" section
//
// Single-profile users never see steps 1–3 — everything resolves to
// "default" automatically.
package credentials

import (
	"fmt"
	"os"
	"path/filepath"
	"sort"
	"strings"

	"github.com/Facets-cloud/praxis-cli/internal/paths"
)

// DefaultURL is the built-in fallback when a profile has no URL set.
const DefaultURL = "https://askpraxis.ai"

// DefaultProfileName is the literal section name used when no other
// signal selects a profile.
const DefaultProfileName = "default"

// Profile is one section of the credentials file.
type Profile struct {
	URL      string
	Username string
	Token    string
}

// Source describes which level produced the active-profile name. Surfaced
// in `praxis status` so users (and AI hosts) can understand WHERE the
// active profile came from.
type Source string

const (
	SourceFlag    Source = "flag"
	SourceEnv     Source = "env"
	SourceConfig  Source = "config"
	SourceDefault Source = "default"
)

// Active is the resolved active profile + provenance.
type Active struct {
	Name    string
	Source  Source
	Profile Profile
	// Loaded is false when the active profile name doesn't exist yet
	// in the credentials file (e.g., user hasn't logged in).
	Loaded bool
}

// ResolveActive walks the priority chain and returns the active profile.
// The Profile field is zeroed if the named section doesn't exist; callers
// should check Loaded before using URL/Token.
func ResolveActive(flagProfile string) (Active, error) {
	name, src := resolveName(flagProfile)
	store, err := Load()
	if err != nil {
		return Active{}, err
	}
	p, ok := store[name]
	return Active{
		Name:    name,
		Source:  src,
		Profile: p,
		Loaded:  ok,
	}, nil
}

func resolveName(flagProfile string) (string, Source) {
	if flagProfile != "" {
		return flagProfile, SourceFlag
	}
	if env := os.Getenv("PRAXIS_PROFILE"); env != "" {
		return env, SourceEnv
	}
	if cfg, _ := loadConfig(); cfg.Profile != "" {
		return cfg.Profile, SourceConfig
	}
	return DefaultProfileName, SourceDefault
}

// Load reads ~/.praxis/credentials. Missing file returns an empty store
// (not an error) so first-run code can call Load freely.
func Load() (map[string]Profile, error) {
	path, err := paths.Credentials()
	if err != nil {
		return nil, err
	}
	data, err := os.ReadFile(path)
	if err != nil {
		if os.IsNotExist(err) {
			return map[string]Profile{}, nil
		}
		return nil, fmt.Errorf("read %s: %w", path, err)
	}
	return parseINI(data), nil
}

// Save writes the entire store atomically (temp + rename, chmod 0600).
func Save(store map[string]Profile) error {
	path, err := paths.Credentials()
	if err != nil {
		return err
	}
	if err := os.MkdirAll(filepath.Dir(path), 0700); err != nil {
		return err
	}
	data := writeINI(store)
	tmp, err := os.CreateTemp(filepath.Dir(path), ".credentials-*.tmp")
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
	if err := os.Chmod(tmp.Name(), 0600); err != nil {
		os.Remove(tmp.Name())
		return err
	}
	return os.Rename(tmp.Name(), path)
}

// Put updates (or adds) one profile. Use case: a successful login.
func Put(name string, p Profile) error {
	store, err := Load()
	if err != nil {
		return err
	}
	store[name] = p
	return Save(store)
}

// Delete removes one profile. No-op if it didn't exist.
func Delete(name string) error {
	store, err := Load()
	if err != nil {
		return err
	}
	if _, ok := store[name]; !ok {
		return nil
	}
	delete(store, name)
	return Save(store)
}

// DeleteAll wipes the credentials file entirely. Used by `praxis logout --all`.
func DeleteAll() error {
	path, err := paths.Credentials()
	if err != nil {
		return err
	}
	if err := os.Remove(path); err != nil && !os.IsNotExist(err) {
		return err
	}
	// Also clear the active-profile pointer so a fresh login can re-bootstrap.
	return ClearActive()
}

// List returns sorted profile names ("default" first if present, then
// alphabetical) so output is deterministic.
func List() ([]string, error) {
	store, err := Load()
	if err != nil {
		return nil, err
	}
	return sortedKeys(store), nil
}

func sortedKeys(m map[string]Profile) []string {
	out := make([]string, 0, len(m))
	for k := range m {
		out = append(out, k)
	}
	sort.Slice(out, func(i, j int) bool {
		if out[i] == DefaultProfileName {
			return true
		}
		if out[j] == DefaultProfileName {
			return false
		}
		return out[i] < out[j]
	})
	return out
}

// ─── Active-profile pointer (~/.praxis/config) ──────────────────────────

// configFile is the on-disk shape of ~/.praxis/config.
type configFile struct {
	Profile string
}

// SetActive writes the active-profile pointer (kubectl-style "use").
func SetActive(name string) error {
	path, err := paths.Config()
	if err != nil {
		return err
	}
	if err := os.MkdirAll(filepath.Dir(path), 0700); err != nil {
		return err
	}
	body := fmt.Sprintf("[default]\nprofile = %s\n", name)
	tmp, err := os.CreateTemp(filepath.Dir(path), ".config-*.tmp")
	if err != nil {
		return err
	}
	if _, err := tmp.Write([]byte(body)); err != nil {
		tmp.Close()
		os.Remove(tmp.Name())
		return err
	}
	if err := tmp.Close(); err != nil {
		os.Remove(tmp.Name())
		return err
	}
	_ = os.Chmod(tmp.Name(), 0600)
	return os.Rename(tmp.Name(), path)
}

// ClearActive removes the active-profile pointer file. After this, the
// fallback "default" applies.
func ClearActive() error {
	path, err := paths.Config()
	if err != nil {
		return err
	}
	if err := os.Remove(path); err != nil && !os.IsNotExist(err) {
		return err
	}
	return nil
}

func loadConfig() (configFile, error) {
	path, err := paths.Config()
	if err != nil {
		return configFile{}, err
	}
	data, err := os.ReadFile(path)
	if err != nil {
		if os.IsNotExist(err) {
			return configFile{}, nil
		}
		return configFile{}, err
	}
	raw := parseRawINI(data)
	if def, ok := raw[DefaultProfileName]; ok {
		return configFile{Profile: def["profile"]}, nil
	}
	return configFile{}, nil
}

// ─── Hand-rolled INI parser (flat sections, key=value, # or ; comments) ──

// parseRawINI is the pure parser. Callers cast the inner maps into typed
// shapes (Profile, configFile) themselves — keeps the parser dumb and
// reusable across both files.
func parseRawINI(data []byte) map[string]map[string]string {
	out := map[string]map[string]string{}
	var current string
	for _, raw := range strings.Split(string(data), "\n") {
		line := strings.TrimSpace(raw)
		if line == "" || strings.HasPrefix(line, "#") || strings.HasPrefix(line, ";") {
			continue
		}
		if strings.HasPrefix(line, "[") && strings.HasSuffix(line, "]") {
			current = strings.TrimSpace(line[1 : len(line)-1])
			if _, ok := out[current]; !ok {
				out[current] = map[string]string{}
			}
			continue
		}
		eq := strings.Index(line, "=")
		if eq <= 0 || current == "" {
			continue
		}
		key := strings.TrimSpace(line[:eq])
		val := strings.TrimSpace(line[eq+1:])
		out[current][key] = val
	}
	return out
}

func parseINI(data []byte) map[string]Profile {
	out := map[string]Profile{}
	for name, kv := range parseRawINI(data) {
		out[name] = Profile{
			URL:      kv["url"],
			Username: kv["username"],
			Token:    kv["token"],
		}
	}
	return out
}

func writeINI(store map[string]Profile) []byte {
	var sb strings.Builder
	sb.WriteString("# Praxis CLI credentials. Managed by `praxis login` / `praxis logout`.\n")
	sb.WriteString("# Format matches facets-cli (~/.facets/credentials).\n\n")
	for _, name := range sortedKeys(store) {
		p := store[name]
		fmt.Fprintf(&sb, "[%s]\n", name)
		if p.URL != "" {
			fmt.Fprintf(&sb, "url      = %s\n", p.URL)
		}
		if p.Username != "" {
			fmt.Fprintf(&sb, "username = %s\n", p.Username)
		}
		if p.Token != "" {
			fmt.Fprintf(&sb, "token    = %s\n", p.Token)
		}
		sb.WriteString("\n")
	}
	return []byte(sb.String())
}
