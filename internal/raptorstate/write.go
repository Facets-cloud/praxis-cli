package raptorstate

import (
	"fmt"
	"net/url"
	"os"
	"path/filepath"
	"sort"
	"strings"
)

// gitignoreBody keeps a project-local credentials file out of the repo.
const gitignoreBody = "credentials\n"

// Written reports what Write did.
type Written struct {
	Path    string
	Section string
	// Changed is false when the section already held these exact values.
	Changed bool
	// Replaced is the control_plane_url the section pointed at before, when
	// that was a different host. Empty otherwise.
	Replaced string
}

// Write saves a control-plane PAT as raptor profile `name`, the way `raptor
// login` does. dir=="" writes ~/.facets/credentials; otherwise
// <dir>/.facets/credentials plus a .gitignore for it (raptor login --local).
// Every other section and key in the file is kept. The section's
// control_plane_url is scheme://host only, matching raptor.
func Write(name, cpURL, username, token, dir string) (Written, error) {
	if name == "" || cpURL == "" || username == "" || token == "" {
		return Written{}, fmt.Errorf("raptor profile needs a name, url, username and token")
	}
	base, err := baseHost(cpURL)
	if err != nil {
		return Written{}, err
	}
	path, err := DefaultPath()
	if dir != "" {
		path, err = filepath.Join(dir, ".facets", "credentials"), nil
	}
	if err != nil {
		return Written{}, err
	}

	sections := loadProfiles(path)
	w := Written{Path: path, Section: name}
	cur, exists := sections[name]
	if exists && cur["control_plane_url"] == base && cur["username"] == username && cur["token"] == token {
		return w, nil
	}
	if exists && cur["control_plane_url"] != "" && !MatchesHost(cur["control_plane_url"], base) {
		w.Replaced = cur["control_plane_url"]
	}
	if !exists {
		cur = map[string]string{}
		sections[name] = cur
	}
	cur["control_plane_url"] = base
	cur["username"] = username
	cur["token"] = token

	if err := os.MkdirAll(filepath.Dir(path), 0o700); err != nil {
		return Written{}, err
	}
	if err := writeAtomic(path, renderINI(sections)); err != nil {
		return Written{}, err
	}
	if dir != "" {
		gi := filepath.Join(filepath.Dir(path), ".gitignore")
		if _, statErr := os.Stat(gi); os.IsNotExist(statErr) {
			if err := os.WriteFile(gi, []byte(gitignoreBody), 0o644); err != nil {
				return Written{}, err
			}
		}
	}
	w.Changed = true
	return w, nil
}

// baseHost reduces a URL to scheme://host, which is what raptor stores.
func baseHost(raw string) (string, error) {
	u, err := url.Parse(strings.TrimSpace(raw))
	if err != nil || u.Scheme == "" || u.Host == "" {
		return "", fmt.Errorf("invalid control plane url %q", raw)
	}
	return u.Scheme + "://" + u.Host, nil
}

// renderINI writes sections in a stable order: raptor's three required keys
// first, every other key after them, sections sorted by name.
func renderINI(sections map[string]map[string]string) []byte {
	names := make([]string, 0, len(sections))
	for n := range sections {
		names = append(names, n)
	}
	sort.Strings(names)
	var sb strings.Builder
	for _, n := range names {
		kv := sections[n]
		fmt.Fprintf(&sb, "[%s]\n", n)
		for _, k := range []string{"control_plane_url", "username", "token"} {
			if v, ok := kv[k]; ok {
				fmt.Fprintf(&sb, "%s = %s\n", k, v)
			}
		}
		rest := make([]string, 0, len(kv))
		for k := range kv {
			if k != "control_plane_url" && k != "username" && k != "token" {
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
func writeAtomic(path string, data []byte) error {
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
	if err := os.Chmod(tmp.Name(), 0o600); err != nil {
		os.Remove(tmp.Name())
		return err
	}
	return os.Rename(tmp.Name(), path)
}
