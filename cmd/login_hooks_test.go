package cmd

import (
	"io"
	"os"
	"path/filepath"
	"testing"

	"github.com/Facets-cloud/praxis-cli/internal/harness"
)

// Regression for the login/logout asymmetry: even when the login is project
// scoped (hosts rebased under a project dir), the hooks must be wired at the
// USER level, because logout only ever cleans the user-level settings.json.
func TestWirePraxisHooksAlwaysUserLevel(t *testing.T) {
	home := t.TempDir()
	t.Setenv("HOME", home)
	proj := t.TempDir()
	hosts := []harness.Harness{{Name: "claude-code", SkillDir: filepath.Join(proj, ".claude", "skills")}}

	got := wirePraxisHooks(io.Discard, true, hosts)
	want := filepath.Join(home, ".claude", "settings.json")
	if got != want {
		t.Errorf("wired to %q, want user-level %q", got, want)
	}
	if _, err := os.Stat(want); err != nil {
		t.Errorf("user-level settings.json not written: %v", err)
	}
	if _, err := os.Stat(filepath.Join(proj, ".claude", "settings.json")); err == nil {
		t.Error("must NOT wire into the project-scoped settings.json (logout can't clean it)")
	}
}

func TestWirePraxisHooksSkipsWithoutClaudeCode(t *testing.T) {
	t.Setenv("HOME", t.TempDir())
	if got := wirePraxisHooks(io.Discard, true, []harness.Harness{{Name: "codex"}}); got != "" {
		t.Errorf("no claude-code host → no wiring, got %q", got)
	}
}

// Regression for the swallowed-in-JSON logout failure: a corrupt settings.json
// makes Uninstall fail, and unwirePraxisHooks must report a warning so a JSON
// logout envelope doesn't claim success while hooks remain.
func TestUnwirePraxisHooksReportsFailure(t *testing.T) {
	home := t.TempDir()
	t.Setenv("HOME", home)
	if err := os.MkdirAll(filepath.Join(home, ".claude"), 0o755); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(filepath.Join(home, ".claude", "settings.json"), []byte("{ bad json"), 0o600); err != nil {
		t.Fatal(err)
	}
	removed, warn := unwirePraxisHooks()
	if removed {
		t.Error("must not report removed on failure")
	}
	if warn == "" {
		t.Error("must return a warning so JSON automation can surface it")
	}
}

func TestUnwirePraxisHooksCleanNoop(t *testing.T) {
	t.Setenv("HOME", t.TempDir())
	removed, warn := unwirePraxisHooks()
	if removed || warn != "" {
		t.Errorf("clean no-op expected, got removed=%v warn=%q", removed, warn)
	}
}
