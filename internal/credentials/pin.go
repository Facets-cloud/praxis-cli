package credentials

import (
	"errors"
	"fmt"
	"os"
	"path/filepath"
	"slices"
	"strings"

	"github.com/Facets-cloud/praxis-cli/internal/paths"
)

// A pin is a name, never a token: <dir>/.facets/profile holds the profile a
// bare command uses inside that tree, for both CLIs. It resolves in the store
// the facets-file walk finds from the same directory. $HOME/.facets/profile is
// never a pin — the machine default is [default].

// PinPathIn is <dir>/.facets/profile.
func PinPathIn(dir string) string {
	return filepath.Join(dir, ".facets", "profile")
}

// FindPin returns the name in the nearest pin file walking up from the working
// directory to the filesystem root, and that file's path. An empty file is no
// pin.
func FindPin() (name, path string, ok bool) {
	cwd, err := getwd()
	if err != nil {
		return "", "", false
	}
	// Home in both spellings, so the skip holds when one side is a symlink
	// (macOS /tmp is /private/tmp) without a syscall per level.
	var homes []string
	if h, herr := paths.Home(); herr == nil {
		homes = []string{filepath.Clean(h), resolvedDir(h)}
	}
	for dir := filepath.Clean(cwd); ; dir = filepath.Dir(dir) {
		if !slices.Contains(homes, dir) {
			if n := readPin(PinPathIn(dir)); n != "" {
				return n, PinPathIn(dir), true
			}
		}
		if filepath.Dir(dir) == dir {
			return "", "", false
		}
	}
}

// readPin is the first non-empty line of a pin file, or "".
func readPin(path string) string {
	data, err := os.ReadFile(path)
	if err != nil {
		return ""
	}
	for _, line := range strings.Split(string(data), "\n") {
		if line = strings.TrimSpace(line); line != "" {
			return line
		}
	}
	return ""
}

// WritePin pins name to dir. The name must be a control-plane PAT in the store
// a read from dir would use, and dir must not be the home directory. The
// folder's .gitignore is made to ignore the pin (see ignorePin).
func WritePin(dir, name string) error {
	if err := validateProfileName(name); err != nil {
		return err
	}
	if home, err := paths.Home(); err == nil && sameDir(dir, home) {
		return errors.New("the home directory cannot be pinned; [default] is the machine default (praxis profiles set-default)")
	}
	store, err := loadWith(facetsPathFrom(dir))
	if err != nil {
		return err
	}
	p, ok := store[name]
	if !ok {
		return fmt.Errorf("profile %q does not exist", name)
	}
	if !isFacetsCredential(p) {
		// raptor reads only control-plane PATs, so it could not follow the pin.
		return fmt.Errorf("profile %q is a Praxis API key; a pin needs a control-plane PAT", name)
	}
	folder := filepath.Join(dir, ".facets")
	if err := os.MkdirAll(folder, 0o700); err != nil {
		return err
	}
	if err := os.WriteFile(PinPathIn(dir), []byte(name+"\n"), 0o644); err != nil {
		return err
	}
	return ignorePin(filepath.Join(folder, ".gitignore"))
}

// ignorePin keeps the pin out of git: a committed pin would make every clone
// use a profile its owner may not have. A new .gitignore ignores the whole
// folder; an existing one gets a "profile" line unless a line already ignores
// it. No line is ever removed.
func ignorePin(gi string) error {
	data, err := os.ReadFile(gi)
	if os.IsNotExist(err) {
		return os.WriteFile(gi, []byte("*\n"), 0o644)
	}
	if err != nil {
		return err
	}
	for _, line := range strings.Split(string(data), "\n") {
		switch strings.TrimSpace(line) {
		case "*", "profile", "/profile":
			return nil
		}
	}
	if len(data) > 0 && !strings.HasSuffix(string(data), "\n") {
		data = append(data, '\n')
	}
	return os.WriteFile(gi, append(data, []byte("profile\n")...), 0o644)
}

// RemovePin removes the nearest pin at or above the working directory and
// returns its path, or "" when there is none.
func RemovePin() (string, error) {
	_, path, ok := FindPin()
	if !ok {
		return "", nil
	}
	return path, os.Remove(path)
}

// facetsPathFrom is FacetsPath with the walk starting at dir.
func facetsPathFrom(dir string) string {
	for d := dir; ; d = filepath.Dir(d) {
		p := FacetsPathIn(d)
		if _, err := os.Stat(p); err == nil {
			return p
		}
		if filepath.Dir(d) == d {
			break
		}
	}
	home, _ := FacetsHome()
	return home
}

// resolvedDir is dir with symlinks resolved (macOS /tmp is /private/tmp), or
// dir cleaned when it cannot be resolved.
func resolvedDir(dir string) string {
	if r, err := filepath.EvalSymlinks(dir); err == nil {
		return r
	}
	return filepath.Clean(dir)
}

// sameDir reports whether two paths name one directory.
func sameDir(a, b string) bool {
	return filepath.Clean(a) == filepath.Clean(b) || resolvedDir(a) == resolvedDir(b)
}
