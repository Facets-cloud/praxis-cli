package credentials

import (
	"fmt"
	"net/url"
	"os"
	"path/filepath"
	"sort"
	"strings"
)

// The facets store is raptor's credentials file, read by both CLIs. A section
// there is a control-plane PAT: praxis sends it as Bearer + X-Facets-Username,
// raptor as HTTP Basic. Its location follows raptor's own rule — the first
// .facets/credentials walking up from the working directory, else the home
// file — so inside a `raptor login --local` tree both CLIs see the same file.
// The praxis file (~/.praxis/credentials) only holds what raptor cannot use:
// Praxis API keys and PATs for a loopback developer server.

// Store labels which file a Profile was loaded from.
const (
	StoreFacets = "facets"
	StorePraxis = "praxis"
	StoreEnv    = "env"
)

// getwd is a seam for the working directory the facets-file walk starts from.
var getwd = os.Getwd

// SetGetwdForTest overrides the walk's starting directory and returns a
// restore func. Test-only: the walk otherwise climbs through the developer's
// real home and reads their live ~/.facets/credentials.
func SetGetwdForTest(fn func() (string, error)) func() {
	prev := getwd
	getwd = fn
	return func() { getwd = prev }
}

// FacetsHome is ~/.facets/credentials — where a global PAT login writes.
func FacetsHome() (string, error) {
	home, err := os.UserHomeDir()
	if err != nil {
		return "", err
	}
	return filepath.Join(home, ".facets", "credentials"), nil
}

// FacetsPathIn is <dir>/.facets/credentials — where a --local PAT login writes.
func FacetsPathIn(dir string) string {
	return filepath.Join(dir, ".facets", "credentials")
}

// FacetsPath is the file raptor would read from the working directory
// (raptor/pkg/config.getCredentialsPath): the first .facets/credentials
// walking up to the filesystem root, else the home file.
func FacetsPath() (string, error) {
	if cwd, err := getwd(); err == nil {
		for dir := cwd; ; dir = filepath.Dir(dir) {
			p := FacetsPathIn(dir)
			if _, err := os.Stat(p); err == nil {
				return p, nil
			}
			if filepath.Dir(dir) == dir {
				break
			}
		}
	}
	return FacetsHome()
}

// LoadFacets returns the facets store raptor would read from here.
func LoadFacets() (map[string]Profile, error) {
	path, err := FacetsPath()
	if err != nil {
		return nil, err
	}
	return loadFacets(path), nil
}

// loadFacets parses a raptor credentials file. Missing or unreadable → empty.
func loadFacets(path string) map[string]Profile {
	out := map[string]Profile{}
	for name, kv := range rawFacets(path) {
		out[name] = Profile{
			URL:      kv["control_plane_url"],
			Username: kv["username"],
			Token:    kv["token"],
			AuthMode: AuthModeBasic,
			Store:    StoreFacets,
		}
	}
	return out
}

func rawFacets(path string) map[string]map[string]string {
	data, err := os.ReadFile(path)
	if err != nil {
		return map[string]map[string]string{}
	}
	return parseRawINI(data)
}

// isFacetsCredential reports whether p belongs in raptor's file: a PAT for an
// https control plane. A loopback developer server cannot be raptor's
// control_plane_url, so that PAT stays in the praxis file.
func isFacetsCredential(p Profile) bool {
	return p.AuthMode == AuthModeBasic && strings.HasPrefix(p.URL, "https://")
}

// putFacets writes section `name` the way `raptor login` does — the three
// keys raptor requires, control_plane_url as scheme://host — and keeps every
// other section and key in the file.
func putFacets(path, name string, p Profile) error {
	base, err := baseHost(p.URL)
	if err != nil {
		return err
	}
	if p.Username == "" || p.Token == "" {
		return fmt.Errorf("a raptor profile needs a username and a token")
	}
	sections := rawFacets(path)
	sec := sections[name]
	if sec != nil && sec["control_plane_url"] == base && sec["username"] == p.Username && sec["token"] == p.Token {
		return nil // already current; leave raptor's file as it is
	}
	if sec == nil {
		sec = map[string]string{}
		sections[name] = sec
	}
	sec["control_plane_url"] = base
	sec["username"] = p.Username
	sec["token"] = p.Token
	return saveFacets(path, sections)
}

// deleteFacets removes a section; reports whether it existed.
func deleteFacets(path, name string) (bool, error) {
	sections := rawFacets(path)
	if _, ok := sections[name]; !ok {
		return false, nil
	}
	delete(sections, name)
	if len(sections) == 0 {
		err := os.Remove(path)
		if os.IsNotExist(err) {
			err = nil
		}
		return true, err
	}
	return true, saveFacets(path, sections)
}

// renameFacets moves a section; reports whether it existed.
func renameFacets(path, oldName, newName string) (bool, error) {
	sections := rawFacets(path)
	sec, ok := sections[oldName]
	if !ok {
		return false, nil
	}
	sections[newName] = sec
	delete(sections, oldName)
	return true, saveFacets(path, sections)
}

func saveFacets(path string, sections map[string]map[string]string) error {
	if err := os.MkdirAll(filepath.Dir(path), 0o700); err != nil {
		return err
	}
	return writeAtomic(path, ".credentials-*.tmp", renderFacetsINI(sections))
}

// ensureGitignore keeps a project-local credentials file out of the repo.
func ensureGitignore(path string) error {
	gi := filepath.Join(filepath.Dir(path), ".gitignore")
	if _, err := os.Stat(gi); err == nil {
		return nil
	}
	return os.WriteFile(gi, []byte("credentials\n"), 0o644)
}

// baseHost reduces a URL to scheme://host, which is what raptor stores.
func baseHost(raw string) (string, error) {
	u, err := url.Parse(strings.TrimSpace(raw))
	if err != nil || u.Scheme == "" || u.Host == "" {
		return "", fmt.Errorf("invalid control plane url %q", raw)
	}
	return u.Scheme + "://" + u.Host, nil
}

// renderFacetsINI writes raptor's three keys first, other keys after, sections
// sorted by name.
func renderFacetsINI(sections map[string]map[string]string) []byte {
	names := make([]string, 0, len(sections))
	for n := range sections {
		names = append(names, n)
	}
	sort.Strings(names)
	known := []string{"control_plane_url", "username", "token"}
	var sb strings.Builder
	for _, n := range names {
		kv := sections[n]
		fmt.Fprintf(&sb, "[%s]\n", n)
		for _, k := range known {
			if v, ok := kv[k]; ok {
				fmt.Fprintf(&sb, "%s = %s\n", k, v)
			}
		}
		rest := make([]string, 0, len(kv))
		for k := range kv {
			if k != known[0] && k != known[1] && k != known[2] {
				rest = append(rest, k)
			}
		}
		sort.Strings(rest)
		for _, k := range rest {
			fmt.Fprintf(&sb, "%s = %s\n", k, kv[k])
		}
		sb.WriteString("\n")
	}
	return []byte(sb.String())
}

// writeAtomic replaces path via temp file + rename, mode 0600.
func writeAtomic(path, pattern string, data []byte) error {
	tmp, err := os.CreateTemp(filepath.Dir(path), pattern)
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

// MigrateLegacyPATs moves control-plane PATs that an older praxis stored in
// ~/.praxis/credentials into the facets home file, so raptor can use them and
// both CLIs read one store. Returns the profiles moved. Best-effort: a failure
// leaves the praxis file as it was.
func MigrateLegacyPATs() ([]string, error) {
	praxis, err := loadPraxis()
	if err != nil {
		return nil, err
	}
	home, err := FacetsHome()
	if err != nil {
		return nil, err
	}
	var moved []string
	for _, name := range sortedKeys(praxis) {
		p := praxis[name]
		if !isFacetsCredential(p) {
			continue
		}
		if err := putFacets(home, name, p); err != nil {
			return moved, err
		}
		delete(praxis, name)
		moved = append(moved, name)
	}
	if len(moved) == 0 {
		return nil, nil
	}
	return moved, savePraxis(praxis)
}
