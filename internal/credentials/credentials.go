// Package credentials manages the multi-profile credentials store praxis
// shares with raptor. Two INI files make one store (see facets.go):
//
//   - ~/.facets/credentials — raptor's file. A section is a control-plane
//     PAT; praxis sends it as Bearer + X-Facets-Username, raptor as Basic.
//     Located by raptor's rule: the first .facets/credentials walking up
//     from the working directory, else the home file.
//
//   - ~/.praxis/credentials — what raptor cannot use: Praxis API keys, and
//     PATs for a loopback developer server. Always global.
//
//     [default]                                  # ~/.facets/credentials
//     control_plane_url = https://acme.console.facets.cloud
//     username          = anshul@facets.cloud
//     token             = <PAT>
//
//     [ci]                                       # ~/.praxis/credentials
//     url      = https://acme.console.facets.cloud
//     username = support@acme.com
//     token    = sk_live_…
//
// Load merges both; a name in both resolves to the facets section. Put routes
// by credential type so a profile lives in exactly one file.
//
// Active-profile resolution (highest priority first):
//
//  1. --profile/-p, the persistent root flag (scoped to one invocation)
//  2. CONTROL_PLANE_URL + FACETS_USERNAME + FACETS_TOKEN — raptor's env
//     credential, a complete profile named "env"
//  3. $PRAXIS_PROFILE (scoped to one shell or agent session)
//  4. $FACETS_PROFILE — raptor's selector, so one variable drives both CLIs
//  5. <cwd>/.praxis/config.json project pointer (set by
//     `praxis profiles use X --local` or `praxis login --profile X --local`),
//     discovered by walking up from the working directory to home
//  6. ~/.praxis/config.json "default profile" pointer (set by
//     `praxis profiles use X` or `praxis login --profile X`)
//  7. literal "default" section
//
// Rationale, and note the env var OUTRANKS the project pointer: the two
// pointers are machine-global state, so moving one repoints every other shell
// and agent session on the box — and rewrites the installed praxis-* skill
// files a concurrent session may already have read. The environment writes
// nothing and is invisible to other sessions, which makes it the only
// concurrency-safe way to scope one session, and the reason a pinned repo stays
// per-session overridable. The flag is the same argument narrowed to a single
// command. Between the two pointers the project one still wins over the global
// one: being inside that directory tree IS the intent.
//
// Single-profile users never see steps 1–6 — everything resolves to
// "default" automatically.
//
// Two deliberate exceptions, for callers asking a question about STATE rather
// than about this invocation — "which profile owns the skills on disk?", or
// "what would this command act on if the invocation named nothing?":
// PersistedActiveName reads only the global pointer, and PointerActiveName only
// the applicable one (project, else global). Both ignore the flag and the
// environment. Resolving those questions through the full chain is what made
// `login --dry-run` mispredict, and what makes a divergence check compare a
// selection with itself.
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

// ValidateProfileName reports whether name can be an INI section header.
func ValidateProfileName(name string) error { return validateProfileName(name) }

// DefaultProfileName is the literal section name used when no other
// signal selects a profile.
const DefaultProfileName = "default"

// Profile is one section of the credentials file.
type Profile struct {
	URL      string
	Username string
	Token    string
	// Store says which file the profile was loaded from (StoreFacets,
	// StorePraxis, StoreEnv). Not persisted; Put decides the file from
	// AuthMode and URL (see isFacetsCredential).
	Store string
	// AuthMode selects how Token is presented on outbound requests.
	// "basic" → facets mode: control-plane PAT sent as Bearer plus an
	// X-Facets-Username identity header; anything else (including "") →
	// plain Bearer (Praxis API key). Persisted as auth_mode in the INI,
	// omitted when empty. (The name is historical — the wire shape is
	// Bearer, never HTTP Basic.)
	AuthMode string
}

// AuthModeBasic is the AuthMode value for control-plane PAT (facets) profiles.
// Shared so the writer (login) and reader (Auth) can't drift on the spelling.
const AuthModeBasic = "basic"

// FacetsProfile builds the profile for a control-plane PAT. Every login path
// that obtains one (raptor's credentials file, the interactive paste) goes
// through here, so the Username/AuthMode pair Auth() keys off is asserted once.
func FacetsProfile(url, username, token string) Profile {
	return Profile{URL: url, Username: username, Token: token, AuthMode: AuthModeBasic}
}

// Auth returns the headers that authenticate a request for this profile:
// always Authorization (Bearer <token>), plus X-Facets-Username for facets
// (AuthModeBasic) profiles — a control-plane PAT the server validates against
// that username. Returns nil when there is no token. Sent as Bearer (never
// HTTP Basic, which browsers cache/replay per origin).
func (p Profile) Auth() map[string]string {
	if p.Token == "" {
		return nil
	}
	h := map[string]string{"Authorization": "Bearer " + p.Token}
	if p.AuthMode == AuthModeBasic {
		h["X-Facets-Username"] = p.Username
	}
	return h
}

// Source describes which level produced the active-profile name. Surfaced
// in `praxis status` so users (and AI hosts) can understand WHERE the
// active profile came from.
type Source string

const (
	SourceFlag        Source = "flag"
	SourceEnvOverride Source = "env-override" // CONTROL_PLANE_URL + FACETS_USERNAME + FACETS_TOKEN
	SourceEnv         Source = "env"          // PRAXIS_PROFILE
	SourceFacetsEnv   Source = "facets-env"   // FACETS_PROFILE
	SourceProject     Source = "project"
	SourceConfig      Source = "config"
	SourceDefault     Source = "default"
)

// EnvProfile selects the active profile for one PROCESS TREE — i.e. one shell
// or one agent session.
//
// This is the concurrency-safe way to work in a profile. The active-profile
// pointer (~/.praxis/config.json) and the installed org skills are BOTH
// machine-global, so `praxis profiles use X` changes what every other session
// on the machine resolves to, and rewrites skill files those sessions have
// already read. Exporting PRAXIS_PROFILE instead writes nothing: it can't be
// observed by another session, and another session's switch can't be observed
// by you.
const EnvProfile = "PRAXIS_PROFILE"

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
	if a, ok := envOverride(flagProfile); ok {
		return a, nil
	}
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
// pointer — flag → global config → "default". Lifecycle
// commands that are global by definition (e.g. `praxis logout`, mirroring
// `praxis login`) use this so a stray/leftover <cwd>/.praxis can't redirect a
// destructive operation at a profile the user didn't mean.
func ResolveActiveGlobal() (Active, error) {
	if a, ok := envOverride(""); ok {
		return a, nil
	}
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
	// Env outranks both pointers: it is this session's explicit choice, and a
	// repo pinned via .praxis must still be overridable per session.
	if name, src := envProfile(); name != "" {
		return name, src
	}
	if name := projectProfile(); name != "" {
		return name, SourceProject
	}
	return resolveGlobalName(flagProfile)
}

// EnvProfileName returns the profile named by $PRAXIS_PROFILE, else by
// raptor's $FACETS_PROFILE (one store, one selector for both CLIs), or "" when
// neither is set. Read live on every resolution so a session can change it.
func EnvProfileName() string {
	name, _ := envProfile()
	return name
}

// FacetsEnvProfile is raptor's profile selector, honored by praxis after
// PRAXIS_PROFILE so one exported variable can drive both CLIs.
const FacetsEnvProfile = "FACETS_PROFILE"

func envProfile() (string, Source) {
	if name := strings.TrimSpace(os.Getenv(EnvProfile)); name != "" {
		return name, SourceEnv
	}
	if name := strings.TrimSpace(os.Getenv(FacetsEnvProfile)); name != "" {
		return name, SourceFacetsEnv
	}
	return "", ""
}

// envOverride mirrors raptor's environment credential: CONTROL_PLANE_URL with
// FACETS_USERNAME and FACETS_TOKEN all set is a complete PAT profile named
// "env", ahead of every file. An explicit --profile still wins.
func envOverride(flagProfile string) (Active, bool) {
	if flagProfile != "" {
		return Active{}, false
	}
	cpURL, user, token := strings.TrimSpace(os.Getenv("CONTROL_PLANE_URL")),
		strings.TrimSpace(os.Getenv("FACETS_USERNAME")), strings.TrimSpace(os.Getenv("FACETS_TOKEN"))
	if cpURL == "" || user == "" || token == "" {
		return Active{}, false
	}
	p := FacetsProfile(strings.TrimRight(cpURL, "/"), user, token)
	p.Store = StoreEnv
	return Active{Name: "env", Source: SourceEnvOverride, Profile: p, Loaded: true}, true
}

// PersistedActiveName returns the profile named by the persisted global
// pointer (~/.praxis/config.json), ignoring --profile and $PRAXIS_PROFILE.
//
// Destructive credentials operations MUST use this rather than
// ResolveActiveGlobal. An override picks which deployment a SESSION talks to;
// the pointer is what owns the org skills installed on disk. Resolving through
// an override would let `PRAXIS_PROFILE=B praxis profiles rm A` delete the very
// profile the pointer and those skills still belong to, leaving both dangling.
func PersistedActiveName() string {
	if cfg, _ := loadConfig(); cfg.Profile != "" {
		return cfg.Profile
	}
	return DefaultProfileName
}

// PointerActiveName returns the profile named by whichever on-disk pointer
// applies here — the project pointer when this tree is pinned to a profile that
// exists, else the persisted global one, else "default". Like
// PersistedActiveName it ignores --profile and $PRAXIS_PROFILE, so it answers
// "what would this command act on if the invocation named nothing?".
//
// That is what an explicit selection has to be measured against. A command that
// refuses a redirect is protecting against DIVERGENCE between the named profile
// and the one whose skills are on disk; `-p X` where X is already the answer is
// a no-op, not a conflict, and refusing it turns the single-profile user's only
// profile name into an error.
func PointerActiveName() (string, error) {
	if name := projectProfile(); name != "" {
		store, err := Load()
		if err != nil {
			return "", err
		}
		// Same guard as ResolveActive: a pointer naming a profile this machine
		// doesn't have (a teammate-committed .praxis, a stale post-logout
		// pointer) is inert and falls through to the global resolution.
		if _, ok := store[name]; ok {
			return name, nil
		}
	}
	return PersistedActiveName(), nil
}

// resolveGlobalName is resolveName without the project-pointer step.
func resolveGlobalName(flagProfile string) (string, Source) {
	if flagProfile != "" {
		return flagProfile, SourceFlag
	}
	if name, src := envProfile(); name != "" {
		return name, src
	}
	if cfg, _ := loadConfig(); cfg.Profile != "" {
		return cfg.Profile, SourceConfig
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

// Load returns every profile praxis can use: the praxis file's API keys plus
// the facets store raptor would read from here (see FacetsPath). A name in
// both files resolves to the facets section, so praxis and raptor never
// disagree about a shared name. Missing files yield an empty store.
func Load() (map[string]Profile, error) {
	store, err := loadPraxis()
	if err != nil {
		return nil, err
	}
	path, err := FacetsPath()
	if err != nil {
		return nil, err
	}
	for name, p := range loadFacets(path) {
		store[name] = p
	}
	return store, nil
}

// loadPraxis reads ~/.praxis/credentials only.
func loadPraxis() (map[string]Profile, error) {
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

// Save replaces the praxis file (API keys and loopback PATs) atomically.
// Facets-store profiles are never written here; use Put.
func Save(store map[string]Profile) error {
	return savePraxis(store)
}

func savePraxis(store map[string]Profile) error {
	path, err := paths.Credentials()
	if err != nil {
		return err
	}
	if err := os.MkdirAll(filepath.Dir(path), 0700); err != nil {
		return err
	}
	return writeAtomic(path, ".credentials-*.tmp", writeINI(store))
}

// Put saves one profile after a login. A control-plane PAT goes to the facets
// home file, where raptor reads it too; anything else (a Praxis API key, a
// PAT for a loopback developer server) goes to the praxis file. A profile
// lives in exactly one file, so the same name is dropped from the other.
func Put(name string, p Profile) error {
	return put(name, p, "")
}

// PutLocal is Put for `login --local`: a PAT goes to <dir>/.facets/credentials
// (raptor's local mode) with a .gitignore; the praxis file stays global.
func PutLocal(name string, p Profile, dir string) error {
	return put(name, p, dir)
}

func put(name string, p Profile, dir string) error {
	if err := validateProfileName(name); err != nil {
		return err
	}
	if isFacetsCredential(p) {
		path, err := FacetsHome()
		if dir != "" {
			path, err = FacetsPathIn(dir), nil
		}
		if err != nil {
			return err
		}
		if err := putFacets(path, name, p); err != nil {
			return err
		}
		if dir != "" {
			if err := ensureGitignore(path); err != nil {
				return err
			}
		}
		praxis, err := loadPraxis()
		if err != nil {
			return err
		}
		if _, dup := praxis[name]; dup {
			delete(praxis, name)
			return savePraxis(praxis)
		}
		return nil
	}
	praxis, err := loadPraxis()
	if err != nil {
		return err
	}
	p.Store = ""
	praxis[name] = p
	if err := savePraxis(praxis); err != nil {
		return err
	}
	fpath, err := FacetsPath()
	if err != nil {
		return err
	}
	_, err = deleteFacets(fpath, name)
	return err
}

// Rename moves a profile to a new section name in whichever file holds it,
// keeping every field. If the GLOBAL active-profile
// pointer named the old profile it is updated to follow; project-local
// pointers can live in any directory tree and are NOT rewritten — a stale
// one is inert by design (LocalModeActive requires the pointer to name an
// existing profile, so it falls back to the global resolution).
// Returns whether the global pointer was updated.
func Rename(oldName, newName string) (pointerUpdated bool, err error) {
	if err := validateProfileName(oldName); err != nil {
		return false, err
	}
	if err := validateProfileName(newName); err != nil {
		return false, err
	}
	if oldName == newName {
		return false, fmt.Errorf("old and new profile names are both %q", oldName)
	}
	store, err := Load()
	if err != nil {
		return false, err
	}
	p, ok := store[oldName]
	if !ok {
		return false, fmt.Errorf("profile %q does not exist", oldName)
	}
	if _, exists := store[newName]; exists {
		return false, fmt.Errorf("profile %q already exists", newName)
	}
	if p.Store == StoreFacets {
		fpath, err := FacetsPath()
		if err != nil {
			return false, err
		}
		if _, err := renameFacets(fpath, oldName, newName); err != nil {
			return false, err
		}
	} else {
		praxis, err := loadPraxis()
		if err != nil {
			return false, err
		}
		praxis[newName] = praxis[oldName]
		delete(praxis, oldName)
		if err := savePraxis(praxis); err != nil {
			return false, err
		}
	}
	if cfg, _ := loadConfig(); cfg.Profile == oldName {
		if err := SetActive(newName); err != nil {
			return false, fmt.Errorf("profile renamed, but updating the active-profile pointer failed: %w", err)
		}
		return true, nil
	}
	return false, nil
}

// Deleted reports which files Delete removed a profile from. Facets means
// raptor lost that profile too.
type Deleted struct {
	Praxis bool
	Facets bool
	// FacetsPath is the facets file the section was removed from.
	FacetsPath string
}

// Delete removes one profile from both files. No-op if it didn't exist.
func Delete(name string) (Deleted, error) {
	var d Deleted
	if err := validateProfileName(name); err != nil {
		return d, err
	}
	praxis, err := loadPraxis()
	if err != nil {
		return d, err
	}
	if _, ok := praxis[name]; ok {
		delete(praxis, name)
		if err := savePraxis(praxis); err != nil {
			return d, err
		}
		d.Praxis = true
	}
	fpath, err := FacetsPath()
	if err != nil {
		return d, err
	}
	d.Facets, err = deleteFacets(fpath, name)
	if d.Facets {
		d.FacetsPath = fpath
	}
	return d, err
}

// DeleteAll wipes both home credentials files — the praxis file and raptor's —
// and the active-profile pointer. Used by `praxis logout --all`.
func DeleteAll() error {
	path, err := paths.Credentials()
	if err != nil {
		return err
	}
	if err := os.Remove(path); err != nil && !os.IsNotExist(err) {
		return err
	}
	home, err := FacetsHome()
	if err != nil {
		return err
	}
	if err := os.Remove(home); err != nil && !os.IsNotExist(err) {
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

// ParseRawINI exposes the flat-INI parser for other packages that handle
// credentials-shaped files (internal/raptorstate, for raptor's
// ~/.facets/credentials).
func ParseRawINI(data []byte) map[string]map[string]string {
	return parseRawINI(data)
}

func parseINI(data []byte) map[string]Profile {
	out := map[string]Profile{}
	for name, kv := range parseRawINI(data) {
		out[name] = Profile{
			URL:      kv["url"],
			Username: kv["username"],
			Token:    kv["token"],
			AuthMode: kv["auth_mode"],
			Store:    StorePraxis,
		}
	}
	return out
}

func writeINI(store map[string]Profile) []byte {
	var sb strings.Builder
	sb.WriteString("# Praxis CLI credentials. Managed by `praxis login` / `praxis logout`.\n")
	sb.WriteString("# Praxis API keys only; control-plane PATs live in ~/.facets/credentials.\n\n")
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
		if p.AuthMode != "" {
			fmt.Fprintf(&sb, "auth_mode = %s\n", p.AuthMode)
		}
		sb.WriteString("\n")
	}
	return []byte(sb.String())
}
