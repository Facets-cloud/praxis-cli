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
	if len(got) != 1 || got[0] != want {
		t.Errorf("wired to %q, want user-level [%q]", got, want)
	}
	if _, err := os.Stat(want); err != nil {
		t.Errorf("user-level settings.json not written: %v", err)
	}
	if _, err := os.Stat(filepath.Join(proj, ".claude", "settings.json")); err == nil {
		t.Error("must NOT wire into the project-scoped settings.json (logout can't clean it)")
	}
}

func TestWirePraxisHooksSkipsWithoutClaudeCode(t *testing.T) {
	home := t.TempDir()
	t.Setenv("HOME", home)
	// Codex has its own file, so a codex-only login must not touch Claude's.
	wirePraxisHooks(io.Discard, true, []harness.Harness{{Name: "codex"}})
	if _, err := os.Stat(filepath.Join(home, ".claude", "settings.json")); err == nil {
		t.Error("codex-only login must not create a claude settings.json")
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

// Every hook-capable detected host gets wired, each into its own file.
func TestWirePraxisHooksCoversEveryHost(t *testing.T) {
	home := t.TempDir()
	t.Setenv("HOME", home)
	got := wirePraxisHooks(io.Discard, true, []harness.Harness{
		{Name: "claude-code"}, {Name: "codex"}, {Name: "gemini-cli"}, {Name: "antigravity"},
	})
	want := []string{
		filepath.Join(home, ".claude", "settings.json"),
		filepath.Join(home, ".codex", "hooks.json"),
		filepath.Join(home, ".gemini", "settings.json"),
	}
	if len(got) != len(want) {
		t.Fatalf("wired %v, want %v (antigravity has no hooks)", got, want)
	}
	for i := range want {
		if got[i] != want[i] {
			t.Errorf("wired[%d] = %q, want %q", i, got[i], want[i])
		}
	}
}
