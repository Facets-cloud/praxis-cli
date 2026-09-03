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
//  5. the [default] section — raptor's own rule
//  6. the sole section, when exactly one exists — raptor's own rule
//
// There is no pointer file. `praxis profiles use X` copies X's section over
// [default], so both CLIs move together; `--local` writes the tree's
// .facets/credentials, which both CLIs read first from inside that tree.
// The environment is the only per-session scope: it writes nothing and is
// invisible to other sessions, which is what makes it concurrency-safe.
//
// Single-profile users never see steps 1–4 — everything resolves to their one
// section automatically.
//
// OnDiskActiveName answers "what would a bare command act on?" for callers
// that must compare an explicit selection against the profile whose org skills
// are installed (logout, refresh-skills, profiles rm).
package credentials

import (
	"fmt"
	"net/url"
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
	SourceDefault     Source = "default"      // the [default] section
	SourceSole        Source = "sole"         // the only section in the store
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
//
// With nothing explicit, the active profile is raptor's: the [default]
// section, else the sole section when exactly one exists. There is no pointer
// file — `praxis profiles use X` copies X into [default], so both CLIs move.
func ResolveActive(flagProfile string) (Active, error) {
	if a, ok := envOverride(flagProfile); ok {
		return a, nil
	}
	store, err := Load()
	if err != nil {
		return Active{}, err
	}
	name, src := resolveName(flagProfile)
	if src == SourceDefault {
		name, src = onDiskActive(store)
	}
	p, ok := store[name]
	return Active{
		Name:    name,
		Source:  src,
		Profile: p,
		Loaded:  ok,
	}, nil
}

// resolveName is the explicit part of the chain: flag, then environment.
// SourceDefault means "nothing explicit" — the caller applies raptor's rule.
func resolveName(flagProfile string) (string, Source) {
	if flagProfile != "" {
		return flagProfile, SourceFlag
	}
	if name, src := envProfile(); name != "" {
		return name, src
	}
	return DefaultProfileName, SourceDefault
}

// onDiskActive is raptor's rule for a bare command: [default] if it exists,
// else the sole section, else the literal "default" (which then fails to load).
func onDiskActive(store map[string]Profile) (string, Source) {
	if _, ok := store[DefaultProfileName]; ok || len(store) != 1 {
		return DefaultProfileName, SourceDefault
	}
	for name := range store {
		return name, SourceSole
	}
	return DefaultProfileName, SourceDefault
}

// OnDiskActiveName is the profile a bare command acts on, ignoring --profile
// and the environment — the one whose org skills are installed. Destructive
// commands compare an explicit selection against this.
func OnDiskActiveName() string {
	store, err := Load()
	if err != nil {
		return DefaultProfileName
	}
	name, _ := onDiskActive(store)
	return name
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

// SameAs returns the other sections whose URL, username and token equal
// profile `name` — after `profiles use X`, [default] is a copy of X, and
// listings say so.
func SameAs(store map[string]Profile, name string) []string {
	p, ok := store[name]
	if !ok {
		return nil
	}
	var out []string
	for _, other := range sortedKeys(store) {
		q := store[other]
		if other != name && q.URL == p.URL && q.Username == p.Username && q.Token == p.Token {
			out = append(out, other)
		}
	}
	return out
}

// SameCreds reports whether two profile names hold identical credentials —
// true after `profiles use X` for X and "default". A guard that refuses a
// selection diverging from the active profile must not refuse its own copy.
func SameCreds(a, b string) bool {
	if a == b {
		return true
	}
	store, err := Load()
	if err != nil {
		return false
	}
	for _, n := range SameAs(store, a) {
		if n == b {
			return true
		}
	}
	return false
}

// SetDefault makes `name` the active profile for both CLIs by copying its
// section over [default], in whichever file holds it (Put routes). A no-op
// for "default" itself.
//
// A [default] that no other section duplicates — a bare first login writes
// only [default] — would be lost by the copy, so it is kept first under a
// name derived from its host (see keepName); the returned string names it.
func SetDefault(name string) (kept string, err error) {
	if name == DefaultProfileName {
		return "", nil
	}
	// The home store, not Load(): a global switch run from inside a local
	// tree must still read and write the home [default].
	store, err := loadHome()
	if err != nil {
		return "", err
	}
	p, ok := store[name]
	if !ok {
		return "", fmt.Errorf("profile %q does not exist", name)
	}
	if def, has := store[DefaultProfileName]; has && len(SameAs(store, DefaultProfileName)) == 0 &&
		(def.URL != p.URL || def.Username != p.Username || def.Token != p.Token) {
		kept = keepName(store, def.URL)
		if err := Put(kept, def); err != nil {
			return "", err
		}
	}
	return kept, Put(DefaultProfileName, p)
}

// keepName picks a free section name for a displaced [default]: the first
// DNS label of its control plane ("facetsdemo" for facetsdemo.console…),
// with a numeric suffix on collision, and "previous" when the URL is unusable.
func keepName(store map[string]Profile, rawURL string) string {
	base := "previous"
	if u, err := url.Parse(rawURL); err == nil && u.Hostname() != "" {
		if label := strings.Split(u.Hostname(), ".")[0]; ValidateProfileName(label) == nil {
			base = label
		}
	}
	name := base
	for i := 2; ; i++ {
		if _, taken := store[name]; !taken && name != DefaultProfileName {
			return name
		}
		name = fmt.Sprintf("%s-%d", base, i)
	}
}

// SetDefaultLocal pins `name` to a directory tree the way `raptor login
// --local` does: <dir>/.facets/credentials gets [name] and a [default] copy,
// so both CLIs resolve it there with no env var. Only a control-plane PAT can
// live in that file.
func SetDefaultLocal(name, dir string) error {
	store, err := Load()
	if err != nil {
		return err
	}
	p, ok := store[name]
	if !ok {
		// A --local login wrote the section into the tree's file, which the
		// walk from the caller's directory may not have reached yet.
		p, ok = loadFacets(FacetsPathIn(dir))[name]
	}
	if !ok {
		return fmt.Errorf("profile %q does not exist", name)
	}
	if !isFacetsCredential(p) {
		return fmt.Errorf("profile %q is a Praxis API key; local mode needs a control-plane PAT", name)
	}
	if err := PutLocal(name, p, dir); err != nil {
		return err
	}
	if name == DefaultProfileName {
		return nil
	}
	return PutLocal(DefaultProfileName, p, dir)
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

// loadHome is Load without the directory walk: the praxis file plus the HOME
// facets file. Global operations (SetDefault, the pointer migration) use it so
// running them from inside a local tree cannot redirect them to the tree.
func loadHome() (map[string]Profile, error) {
	store, err := loadPraxis()
	if err != nil {
		return nil, err
	}
	home, err := FacetsHome()
	if err != nil {
		return nil, err
	}
	for name, p := range loadFacets(home) {
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
			// A tree's file displaces nothing global.
			return ensureGitignore(path)
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
	// The praxis file is global, so the section it displaces is the HOME
	// facets one — never a tree's file the command merely ran inside.
	fpath, err := FacetsHome()
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
func Rename(oldName, newName string) error {
	if err := validateProfileName(oldName); err != nil {
		return err
	}
	if err := validateProfileName(newName); err != nil {
		return err
	}
	if oldName == newName {
		return fmt.Errorf("old and new profile names are both %q", oldName)
	}
	store, err := Load()
	if err != nil {
		return err
	}
	p, ok := store[oldName]
	if !ok {
		return fmt.Errorf("profile %q does not exist", oldName)
	}
	if _, exists := store[newName]; exists {
		return fmt.Errorf("profile %q already exists", newName)
	}
	if p.Store == StoreFacets {
		fpath, err := FacetsPath()
		if err != nil {
			return err
		}
		if _, err := renameFacets(fpath, oldName, newName); err != nil {
			return err
		}
	} else {
		praxis, err := loadPraxis()
		if err != nil {
			return err
		}
		praxis[newName] = praxis[oldName]
		delete(praxis, oldName)
		if err := savePraxis(praxis); err != nil {
			return err
		}
	}
	return nil
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
	// Also drop a legacy pointer file, if one is still around.
	if legacy, err := paths.LegacyConfig(); err == nil {
		if err := os.Remove(legacy); err != nil && !os.IsNotExist(err) {
			return err
		}
	}
	return nil
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

// ─── Legacy pointer (~/.praxis/config.json) ──────────────────────────────

// MigrateLegacyPointer retires the active-profile pointer an older praxis
// kept at ~/.praxis/config.json. The profile it named becomes [default] (a
// copy, so raptor follows too), then the file is removed. Returns the profile
// promoted — "" when the pointer named default, a missing profile, or was
// absent — and the name a displaced, unduplicated [default] was kept under.
func MigrateLegacyPointer() (promoted, kept string, err error) {
	path, err := paths.LegacyConfig()
	if err != nil {
		return "", "", err
	}
	data, err := os.ReadFile(path)
	if err != nil {
		if os.IsNotExist(err) {
			return "", "", nil
		}
		return "", "", err
	}
	name := ""
	if def, ok := parseRawINI(data)[DefaultProfileName]; ok {
		name = def["profile"]
	}
	if name != "" && name != DefaultProfileName {
		store, err := loadHome()
		if err != nil {
			return "", "", err
		}
		if _, ok := store[name]; ok {
			if kept, err = SetDefault(name); err != nil {
				return "", "", err
			}
			promoted = name
		}
	}
	return promoted, kept, os.Remove(path)
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
