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
//  1. --profile flag passed to a command (where it exists)
//  2. <cwd>/.praxis/config.json project pointer (set by `praxis use --local`),
//     discovered by walking up from the working directory to home
//  3. ~/.praxis/config.json "default profile" pointer (set by `praxis use`)
//  4. PRAXIS_PROFILE environment variable
//  5. literal "default" section
//
// Rationale: a project pointer is the most specific, explicit choice — being
// inside that directory tree IS the intent — so it wins. `praxis use X` is an
// explicit, persistent global choice; it's next. PRAXIS_PROFILE is the
// fallback when no `use` has been called (and the rare per-shell override) —
// it does NOT silently override an explicit `use` decision in the next shell.
//
// Single-profile users never see steps 1–3 — everything resolves to
// "default" automatically.
package credentials

import (
	"fmt"
	"os"
	"path/filepath"
	"regexp"
	"sort"
	"strings"

	"github.com/Facets-cloud/praxis-cli/internal/paths"
)

// profileNameRE bounds profile names to chars that round-trip cleanly through
// the INI section header `[name]` — no `[`, `]`, `=`, `\n`, or whitespace.
// Matches credentials-style identifiers (kubectl context, AWS profile, etc).
var profileNameRE = regexp.MustCompile(`^[a-zA-Z0-9][a-zA-Z0-9_.-]*$`)

// validateProfileName rejects names that would corrupt the credentials INI.
// Empty, whitespace, control chars, `[`, `]`, `=`, `\n` are all blocked.
// A leading `.` is rejected so names can't shadow hidden-file conventions.
func validateProfileName(name string) error {
	if name == "" {
		return fmt.Errorf("profile name cannot be empty")
	}
	if !profileNameRE.MatchString(name) {
		return fmt.Errorf("invalid profile name %q: must match [a-zA-Z0-9][a-zA-Z0-9_.-]*", name)
	}
	return nil
}

// DefaultURL is the built-in fallback when a profile has no URL set.
// Use the canonical www host: the apex https://askpraxis.ai
// 301-redirects to www, and a stored apex URL forces every MCP invoke
// to pay (and, before the callMCP redirect fix, fail on) that redirect.
const DefaultURL = "https://www.askpraxis.ai"

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
	SourceProject Source = "project"
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
	store, err := Load()
	if err != nil {
		return Active{}, err
	}
	name, src := resolveName(flagProfile)
	if src == SourceProject {
		if _, ok := store[name]; !ok {
			// The project pointer names a profile this machine doesn't have
			// — e.g. a <repo>/.praxis committed by a teammate, or a stale
			// pointer left after `logout`. Don't hijack the user into a
			// profile they never created (which would just hard-fail every
			// command); fall back to the global resolution.
			name, src = resolveGlobalName(flagProfile)
		}
	}
	p, ok := store[name]
	return Active{
		Name:    name,
		Source:  src,
		Profile: p,
		Loaded:  ok,
	}, nil
}

// ResolveActiveGlobal resolves the active profile IGNORING any project-local
// pointer — flag → global config → PRAXIS_PROFILE → "default". Lifecycle
// commands that are global by definition (e.g. `praxis logout`, mirroring
// `praxis login`) use this so a stray/leftover <cwd>/.praxis can't redirect a
// destructive operation at a profile the user didn't mean.
func ResolveActiveGlobal() (Active, error) {
	store, err := Load()
	if err != nil {
		return Active{}, err
	}
	name, src := resolveGlobalName("")
	p, ok := store[name]
	return Active{Name: name, Source: src, Profile: p, Loaded: ok}, nil
}

func resolveName(flagProfile string) (string, Source) {
	if flagProfile != "" {
		return flagProfile, SourceFlag
	}
	if name := projectProfile(); name != "" {
		return name, SourceProject
	}
	return resolveGlobalName(flagProfile)
}

// resolveGlobalName is resolveName without the project-pointer step.
func resolveGlobalName(flagProfile string) (string, Source) {
	if flagProfile != "" {
		return flagProfile, SourceFlag
	}
	if cfg, _ := loadConfig(); cfg.Profile != "" {
		return cfg.Profile, SourceConfig
	}
	if env := os.Getenv("PRAXIS_PROFILE"); env != "" {
		return env, SourceEnv
	}
	return DefaultProfileName, SourceDefault
}

// init wires the paths package's local-mode gate to the credentials store:
// a discovered <repo>/.praxis is only the active root when its pointer names
// a profile that actually exists here. This is what makes a bare or
// teammate-committed .praxis inert for a user who never opted in, while
// keeping paths free of a credentials import (which would be a cycle).
func init() {
	paths.LocalModeActive = func(projectRoot string) bool {
		cfg, err := readConfigFile(filepath.Join(projectRoot, "config.json"))
		if err != nil || cfg.Profile == "" {
			return false
		}
		store, err := Load()
		if err != nil {
			return false
		}
		_, ok := store[cfg.Profile]
		return ok
	}
}

// projectProfile returns the profile named in the project-local pointer
// (<projectRoot>/.praxis/config.json), or "" when there's no project root or
// no profile recorded there.
func projectProfile() string {
	path, ok, err := paths.ProjectConfig()
	if err != nil || !ok {
		return ""
	}
	cfg, err := readConfigFile(path)
	if err != nil {
		return ""
	}
	return cfg.Profile
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
	if err := validateProfileName(name); err != nil {
		return err
	}
	store, err := Load()
	if err != nil {
		return err
	}
	store[name] = p
	return Save(store)
}

// Delete removes one profile. No-op if it didn't exist.
func Delete(name string) error {
	if err := validateProfileName(name); err != nil {
		return err
	}
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

// ─── Active-profile pointer (~/.praxis/config.json) ──────────────────────

// configFile is the on-disk shape of ~/.praxis/config.json (INI-formatted
// despite the filename — the .json suffix predates the format choice).
type configFile struct {
	Profile string
}

// SetActive writes the GLOBAL active-profile pointer (kubectl-style "use").
func SetActive(name string) error {
	if err := validateProfileName(name); err != nil {
		return err
	}
	path, err := paths.Config()
	if err != nil {
		return err
	}
	return writeConfigPointer(path, name)
}

// SetActiveLocal pins the active profile to the current working-directory
// tree by writing a project-local pointer. If a project root (a .praxis dir)
// already exists at or above the working directory it is reused; otherwise
// <cwd>/.praxis is created. Returns the project root written to. Credentials
// are NOT touched — they stay global in ~/.praxis/credentials.
func SetActiveLocal(name string) (string, error) {
	if err := validateProfileName(name); err != nil {
		return "", err
	}
	root, ok, err := paths.ProjectRoot()
	if err != nil {
		return "", err
	}
	if !ok {
		root, err = paths.EnsureProjectRoot()
		if err != nil {
			return "", err
		}
	}
	if err := writeConfigPointer(filepath.Join(root, "config.json"), name); err != nil {
		return "", err
	}
	return root, nil
}

// writeConfigPointer atomically writes a "[default]\nprofile = <name>"
// pointer file at path (temp + rename, chmod 0600).
func writeConfigPointer(path, name string) error {
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
	return readConfigFile(path)
}

// readConfigFile parses a pointer file (the [default] profile = <name>
// shape). A missing file is not an error — it yields a zero configFile.
func readConfigFile(path string) (configFile, error) {
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
