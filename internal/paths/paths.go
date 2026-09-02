// Package paths centralizes Praxis filesystem locations.
//
// Two roots exist:
//
//   - The HOME root (~/.praxis) holds the user-global praxis state: the
//     API-key credentials file and the update-check cache.
//   - A PROJECT root (<repo>/.praxis) exists wherever a <repo>/.facets/credentials
//     file does — raptor's local-mode marker, written by `raptor login --local`
//     and `praxis login --local`. It is discovered by walking up from the
//     working directory and becomes the ActiveRoot for everything that should
//     follow a working directory: the skill receipt and the MCP snapshot. This
//     lets a developer pin a profile (and its skills) to one repo while other
//     repos use other profiles — without the profiles clobbering each other's
//     skills.
package paths

import (
	"fmt"
	"os"
	"path/filepath"
	"strings"
)

const dirName = ".praxis"

// getwd is a seam for the working directory so project-root discovery is
// testable without chdir'ing the test process. Override via SetGetwdForTest.
var getwd = os.Getwd

// SetGetwdForTest overrides the working-directory resolver and returns a
// restore func. Test-only — lets tests (including cmd-layer tests in other
// packages) drive project-root discovery deterministically.
func SetGetwdForTest(fn func() (string, error)) func() {
	prev := getwd
	getwd = fn
	return func() { getwd = prev }
}

// activeRootOverride, when non-empty, pins ActiveRoot to a fixed dir
// regardless of the working directory. `praxis login` uses it (pinned to the
// home root) so its setup stays strictly global even when run from inside a
// project tree. Empty in the normal case.
var activeRootOverride string

// OverrideActiveRoot pins ActiveRoot to dir until the returned restore func is
// called. Not safe for concurrent use; intended for a single command's
// lifetime. While pinned, project-scope resolution treats everything as
// user-level (see cmd.resolveProjectScope) so the install location and the
// receipt location stay consistent.
func OverrideActiveRoot(dir string) func() {
	prev := activeRootOverride
	activeRootOverride = dir
	return func() { activeRootOverride = prev }
}

// RootIsPinned reports whether ActiveRoot is currently pinned via
// OverrideActiveRoot.
func RootIsPinned() bool {
	return activeRootOverride != ""
}

// Dir returns the HOME root ~/.praxis (does not create it).
func Dir() (string, error) {
	home, err := os.UserHomeDir()
	if err != nil {
		return "", err
	}
	return filepath.Join(home, dirName), nil
}

// ProjectRoot discovers a local-mode tree by walking up from the working
// directory to — but NOT including — the home directory, looking for raptor's
// marker, <dir>/.facets/credentials. It returns <dir>/.praxis (the receipt and
// snapshot live beside the marker), or ("", false, nil) when none is found,
// when the working directory is not under the home directory, or when the
// home directory can't be resolved. A stray <dir>/.praxis on its own means
// nothing: the credentials file IS the opt-in, and it is never committed.
//
// The walk stops below home, so the global ~/.praxis is never returned and
// can't be mistaken for a project root. Restricting discovery to the home
// subtree also keeps it deterministic under tests (which fake $HOME): a test
// whose working directory is outside the faked home sees no project root.
//
// Home and the working directory are aligned into one namespace first (see
// alignUnder) so a symlinked home — macOS $HOME=/tmp/x, where /tmp is a link
// to /private/tmp — doesn't read as "outside home".
func ProjectRoot() (string, bool, error) {
	home, err := os.UserHomeDir()
	if err != nil {
		return "", false, nil
	}
	cwd, err := getwd()
	if err != nil {
		return "", false, nil
	}
	home, dir, ok := alignUnder(home, cwd)
	if !ok {
		return "", false, nil
	}
	// Both are in the same namespace now, so this bound really does stop at
	// home instead of walking past it to / and finding a stray .praxis.
	for dir != home {
		marker := filepath.Join(dir, ".facets", "credentials")
		if fi, statErr := os.Stat(marker); statErr == nil && !fi.IsDir() {
			return filepath.Join(dir, dirName), true, nil
		}
		parent := filepath.Dir(dir)
		if parent == dir {
			break // filesystem root — defensive, shouldn't happen under home
		}
		dir = parent
	}
	return "", false, nil
}

// ActiveRoot returns the project root if one is discovered (or pinned via
// OverrideActiveRoot), else the home root. The skill receipt and MCP snapshot
// follow this root so a project-local session keeps its skills, receipt, and
// snapshot together. The project .praxis directory is created on demand: the
// marker that defines the tree is the credentials file beside it.
func ActiveRoot() (string, error) {
	if activeRootOverride != "" {
		return activeRootOverride, nil
	}
	root, ok, err := ProjectRoot()
	if err != nil {
		return "", err
	}
	if ok {
		return root, os.MkdirAll(root, 0o700)
	}
	return Dir()
}

// EnsureProjectRoot creates <cwd>/.praxis (if absent) and returns it. It
// requires the working directory to be under the home directory (and not the
// home directory itself): local mode is discovered by walking up to home, so
// a marker outside that subtree could never be found again. Returns an error
// otherwise.
//
// It aligns home and the working directory the same way ProjectRoot does, so
// whatever this creates is discoverable afterwards.
func EnsureProjectRoot() (string, error) {
	home, err := os.UserHomeDir()
	if err != nil {
		return "", err
	}
	cwd, err := getwd()
	if err != nil {
		return "", err
	}
	home, cwd, ok := alignUnder(home, cwd)
	if !ok {
		return "", fmt.Errorf("local mode requires a directory under your home directory (%s); %s is outside it", home, cwd)
	}
	if cwd == home {
		return "", fmt.Errorf("local mode requires a directory under your home directory (%s), not the home directory itself", home)
	}
	root := filepath.Join(cwd, dirName)
	if err := os.MkdirAll(root, 0o700); err != nil {
		return "", err
	}
	return root, nil
}

// Credentials is the praxis file (Praxis API keys, loopback PATs). ALWAYS
// under the home root — never project-local. Control-plane PATs live in
// raptor's file instead (see credentials.FacetsPath).
func Credentials() (string, error) {
	d, err := Dir()
	return filepath.Join(d, "credentials"), err
}

// LegacyConfig is where praxis before v1.11 kept its active-profile pointer.
// It is read once to migrate and then removed; nothing writes it.
func LegacyConfig() (string, error) {
	d, err := Dir()
	return filepath.Join(d, "config.json"), err
}

// LegacyProjectPointer reports a pre-v1.11 per-directory pointer
// (<dir>/.praxis/config.json) in the tree with no <dir>/.facets/credentials
// beside it. Such a tree is no longer local mode; callers print a hint.
func LegacyProjectPointer() (path, profile string, ok bool) {
	home, err := os.UserHomeDir()
	if err != nil {
		return "", "", false
	}
	cwd, err := getwd()
	if err != nil {
		return "", "", false
	}
	home, dir, aligned := alignUnder(home, cwd)
	if !aligned {
		return "", "", false
	}
	for dir != home {
		if _, err := os.Stat(filepath.Join(dir, ".facets", "credentials")); err == nil {
			return "", "", false // a real local tree; nothing legacy about it
		}
		p := filepath.Join(dir, dirName, "config.json")
		if data, err := os.ReadFile(p); err == nil {
			for _, line := range strings.Split(string(data), "\n") {
				if k, v, found := strings.Cut(line, "="); found && strings.TrimSpace(k) == "profile" {
					return p, strings.TrimSpace(v), true
				}
			}
			return p, "", true
		}
		parent := filepath.Dir(dir)
		if parent == dir {
			break
		}
		dir = parent
	}
	return "", "", false
}

// UpdateCheckCache is the throttle cache for the background version check.
// ALWAYS under the home root (never project-local): the installed binary's
// version is per-machine, not per-repo, so the "last checked" state must not
// divert into a project tree.
func UpdateCheckCache() (string, error) {
	d, err := Dir()
	return filepath.Join(d, "last-update-check.json"), err
}

// Installed is the JSON receipt of skills installed across AI hosts. Follows
// ActiveRoot — project-local when inside a project tree, else home.
func Installed() (string, error) {
	d, err := ActiveRoot()
	return filepath.Join(d, "installed.json"), err
}

// MCPTools is the snapshot of the gateway's /v1/mcp/manifest. Follows
// ActiveRoot — project-local when inside a project tree, else home.
func MCPTools() (string, error) {
	d, err := ActiveRoot()
	return filepath.Join(d, "mcp-tools.json"), err
}

// alignUnder expresses base and path in a single namespace and reports whether
// path is base or a descendant of it. Both returned values come from whichever
// namespace matched, so a caller can walk from path up to base knowing the two
// will actually meet.
//
// The paths as given are tried first: that costs no syscalls, keeps the
// spelling the user typed, and is what nearly every call hits. Only when that
// fails are the symlink-resolved forms compared — which is the case a plain
// prefix test gets wrong. $HOME may be logical while os.Getwd() reports the
// physical path (macOS: HOME=/tmp/x, cwd=/private/tmp/x/repo, since /tmp is a
// symlink to /private/tmp), or the reverse when $PWD carries a logical path
// into a physical home. Either way the two strings name the same directory and
// must compare equal.
//
// Resolution only ever ADDS matches. A directory reached through a symlink that
// points out of home still matches on the first attempt and keeps working, so
// this can't newly reject a layout that works today.
func alignUnder(base, path string) (string, string, bool) {
	base, path = filepath.Clean(base), filepath.Clean(path)
	if isUnder(base, path) {
		return base, path, true
	}
	if rBase, rPath := resolved(base), resolved(path); isUnder(rBase, rPath) {
		return rBase, rPath, true
	}
	// Not under base in either namespace. Report the paths as given — they're
	// what the user typed, so they're what an error message should name.
	return base, path, false
}

// resolved returns path with symlinks resolved, or path unchanged when it
// can't be: EvalSymlinks fails on a path that doesn't exist, and callers that
// check containment before creating a directory still need an answer. Every
// caller resolves the working directory (which exists) and $HOME, so the
// fallback only fires for a misconfigured or deleted one — where leaving the
// path as-is yields the conservative "not under home" answer.
func resolved(path string) string {
	if r, err := filepath.EvalSymlinks(path); err == nil {
		return r
	}
	return path
}

// isUnder reports whether path is base or a descendant of base.
func isUnder(base, path string) bool {
	rel, err := filepath.Rel(base, path)
	if err != nil {
		return false
	}
	return rel == "." || (rel != ".." && !strings.HasPrefix(rel, ".."+string(filepath.Separator)))
}
