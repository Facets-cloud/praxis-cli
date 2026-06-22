// Package paths centralizes Praxis filesystem locations.
//
// Two roots exist:
//
//   - The HOME root (~/.praxis) holds the shared, user-global state:
//     credentials and the global active-profile pointer. Credentials ALWAYS
//     live here regardless of the working directory.
//   - A PROJECT root (<repo>/.praxis) is discovered by walking up from the
//     working directory, git-style. When present it becomes the ActiveRoot
//     for everything that should follow a working directory: the skill
//     receipt, the MCP snapshot, and a project-local active-profile pointer.
//     This lets a developer pin a profile (and its skills) to one repo while
//     other repos use other profiles — without the profiles clobbering each
//     other's skills.
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

// LocalModeActive, when set, decides whether a discovered project root should
// actually be treated as the ActiveRoot — i.e. whether local mode is GENUINELY
// active there (its pointer names a profile this machine has). It is injected
// by the credentials package (which paths can't import without a cycle) so a
// bare .praxis (no pointer) or a foreign one (pointer to a profile you don't
// have, e.g. a teammate's committed marker) does NOT silently divert the
// receipt/snapshot/skill location for a user who never opted in. When nil
// (low-level tests with no credentials wired up), discovery falls back to
// presence-only.
var LocalModeActive func(projectRoot string) bool

// Dir returns the HOME root ~/.praxis (does not create it).
func Dir() (string, error) {
	home, err := os.UserHomeDir()
	if err != nil {
		return "", err
	}
	return filepath.Join(home, dirName), nil
}

// ProjectRoot discovers a project-local .praxis directory by walking up from
// the working directory to — but NOT including — the home directory. It
// returns ("", false, nil) when none is found, when the working directory is
// not under the home directory, or when the home directory can't be resolved.
//
// The walk stops below home, so the global ~/.praxis is never returned and
// can't be mistaken for a project root. Restricting discovery to the home
// subtree also keeps it deterministic under tests (which fake $HOME): a test
// whose working directory is outside the faked home sees no project root.
func ProjectRoot() (string, bool, error) {
	home, err := os.UserHomeDir()
	if err != nil {
		return "", false, nil
	}
	cwd, err := getwd()
	if err != nil {
		return "", false, nil
	}
	home = filepath.Clean(home)
	dir := filepath.Clean(cwd)
	if !isUnder(home, dir) {
		return "", false, nil
	}
	for dir != home {
		candidate := filepath.Join(dir, dirName)
		if fi, statErr := os.Stat(candidate); statErr == nil && fi.IsDir() {
			return candidate, true, nil
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
// snapshot together while credentials stay global.
func ActiveRoot() (string, error) {
	if activeRootOverride != "" {
		return activeRootOverride, nil
	}
	root, ok, err := ProjectRoot()
	if err != nil {
		return "", err
	}
	// A discovered .praxis only becomes the active root when local mode is
	// genuinely active there (see LocalModeActive). Otherwise — a bare or
	// foreign marker — fall back to home so a stray directory can't divert a
	// normal user's receipt/snapshot/skills.
	if ok && (LocalModeActive == nil || LocalModeActive(root)) {
		return root, nil
	}
	return Dir()
}

// EnsureProjectRoot creates <cwd>/.praxis (if absent) and returns it. It
// requires the working directory to be under the home directory (and not the
// home directory itself): local mode is discovered by walking up to home, so
// a marker outside that subtree could never be found again. Returns an error
// otherwise.
func EnsureProjectRoot() (string, error) {
	home, err := os.UserHomeDir()
	if err != nil {
		return "", err
	}
	cwd, err := getwd()
	if err != nil {
		return "", err
	}
	home = filepath.Clean(home)
	cwd = filepath.Clean(cwd)
	if cwd == home || !isUnder(home, cwd) {
		return "", fmt.Errorf("local mode requires a directory under your home directory (%s); %s is outside it", home, cwd)
	}
	root := filepath.Join(cwd, dirName)
	if err := os.MkdirAll(root, 0o700); err != nil {
		return "", err
	}
	return root, nil
}

// Credentials is the bearer-token store. ALWAYS under the home root — never
// project-local — so a single login is shared across every working directory.
func Credentials() (string, error) {
	d, err := Dir()
	return filepath.Join(d, "credentials"), err
}

// Config is the GLOBAL active-profile pointer (set by `praxis use`). Always
// under the home root. The project-local pointer is ProjectConfig.
func Config() (string, error) {
	d, err := Dir()
	return filepath.Join(d, "config.json"), err
}

// ProjectConfig returns the project-local active-profile pointer
// (<projectRoot>/config.json) and whether a project root exists. Used to
// resolve the active profile for a working directory ahead of the global
// pointer.
func ProjectConfig() (string, bool, error) {
	root, ok, err := ProjectRoot()
	if err != nil || !ok {
		return "", false, err
	}
	return filepath.Join(root, "config.json"), true, nil
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

// isUnder reports whether path is base or a descendant of base.
func isUnder(base, path string) bool {
	rel, err := filepath.Rel(base, path)
	if err != nil {
		return false
	}
	return rel == "." || (rel != ".." && !strings.HasPrefix(rel, ".."+string(filepath.Separator)))
}
