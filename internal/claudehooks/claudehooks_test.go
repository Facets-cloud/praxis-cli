package claudehooks

import (
	"encoding/json"
	"os"
	"path/filepath"
	"testing"
)

const praxis = "/usr/local/bin/praxis"

func readSettings(t *testing.T, p string) map[string]any {
	t.Helper()
	raw, err := os.ReadFile(p)
	if err != nil {
		t.Fatalf("read settings: %v", err)
	}
	var m map[string]any
	if err := json.Unmarshal(raw, &m); err != nil {
		t.Fatalf("settings is not valid JSON: %v\n%s", err, raw)
	}
	return m
}

// commandsFor returns every hook command string registered under event key.
func commandsFor(t *testing.T, settings map[string]any, key string) []string {
	t.Helper()
	hooks, _ := settings["hooks"].(map[string]any)
	list, _ := hooks[key].([]any)
	var out []string
	for _, item := range list {
		entry, _ := item.(map[string]any)
		inner, _ := entry["hooks"].([]any)
		for _, hv := range inner {
			h, _ := hv.(map[string]any)
			if c, ok := h["command"].(string); ok {
				out = append(out, c)
			}
		}
	}
	return out
}

func has(ss []string, want string) bool {
	for _, s := range ss {
		if s == want {
			return true
		}
	}
	return false
}

func TestInstallCreatesBothHooks(t *testing.T) {
	p := filepath.Join(t.TempDir(), ".claude", "settings.json")
	changed, err := Install(p, praxis)
	if err != nil {
		t.Fatalf("Install: %v", err)
	}
	if !changed {
		t.Error("first install must report changed")
	}
	s := readSettings(t, p)
	if !has(commandsFor(t, s, "SessionStart"), praxis+" ig hook session-start") {
		t.Error("SessionStart hook not wired")
	}
	if !has(commandsFor(t, s, "CwdChanged"), praxis+" ig hook cwd-changed") {
		t.Error("CwdChanged hook not wired")
	}
}

func TestInstallIsIdempotent(t *testing.T) {
	p := filepath.Join(t.TempDir(), "settings.json")
	if _, err := Install(p, praxis); err != nil {
		t.Fatal(err)
	}
	changed, err := Install(p, praxis)
	if err != nil {
		t.Fatal(err)
	}
	if changed {
		t.Error("second identical install must report no change")
	}
	// Exactly one SessionStart command, not duplicated.
	if got := commandsFor(t, readSettings(t, p), "SessionStart"); len(got) != 1 {
		t.Errorf("expected 1 SessionStart command, got %d: %v", len(got), got)
	}
}

func TestInstallRefreshesStalePath(t *testing.T) {
	p := filepath.Join(t.TempDir(), "settings.json")
	if _, err := Install(p, "/old/path/praxis"); err != nil {
		t.Fatal(err)
	}
	changed, err := Install(p, praxis)
	if err != nil {
		t.Fatal(err)
	}
	if !changed {
		t.Error("a moved praxis binary must refresh the hook path")
	}
	got := commandsFor(t, readSettings(t, p), "SessionStart")
	if len(got) != 1 || !has(got, praxis+" ig hook session-start") {
		t.Errorf("stale path not refreshed in place: %v", got)
	}
}

func TestInstallPreservesForeignHooksAndKeys(t *testing.T) {
	p := filepath.Join(t.TempDir(), "settings.json")
	seed := map[string]any{
		"model": "opus",
		"hooks": map[string]any{
			"SessionStart": []any{
				map[string]any{"hooks": []any{
					map[string]any{"type": "command", "command": "flow hook session-start"},
				}},
			},
		},
	}
	b, _ := json.MarshalIndent(seed, "", "  ")
	if err := os.WriteFile(p, b, 0o644); err != nil {
		t.Fatal(err)
	}
	if _, err := Install(p, praxis); err != nil {
		t.Fatal(err)
	}
	s := readSettings(t, p)
	if s["model"] != "opus" {
		t.Error("top-level key clobbered")
	}
	cmds := commandsFor(t, s, "SessionStart")
	if !has(cmds, "flow hook session-start") {
		t.Error("foreign flow hook removed")
	}
	if !has(cmds, praxis+" ig hook session-start") {
		t.Error("praxis hook not added alongside foreign hook")
	}
	// A .bak of the prior file is kept.
	if _, err := os.Stat(p + ".bak"); err != nil {
		t.Errorf("expected settings.json.bak, got %v", err)
	}
}

func TestInstallRefusesInvalidJSON(t *testing.T) {
	p := filepath.Join(t.TempDir(), "settings.json")
	if err := os.WriteFile(p, []byte("{ not json"), 0o644); err != nil {
		t.Fatal(err)
	}
	if _, err := Install(p, praxis); err == nil {
		t.Error("Install must refuse to overwrite invalid JSON")
	}
	// Untouched.
	if b, _ := os.ReadFile(p); string(b) != "{ not json" {
		t.Error("invalid settings.json was modified")
	}
}

func TestUninstallRemovesOnlyPraxisHooks(t *testing.T) {
	p := filepath.Join(t.TempDir(), "settings.json")
	// Seed a foreign hook, then add praxis's.
	seed := map[string]any{
		"hooks": map[string]any{
			"SessionStart": []any{
				map[string]any{"hooks": []any{
					map[string]any{"type": "command", "command": "flow hook session-start"},
				}},
			},
		},
	}
	b, _ := json.MarshalIndent(seed, "", "  ")
	if err := os.WriteFile(p, b, 0o644); err != nil {
		t.Fatal(err)
	}
	if _, err := Install(p, praxis); err != nil {
		t.Fatal(err)
	}
	changed, err := Uninstall(p, praxis)
	if err != nil {
		t.Fatal(err)
	}
	if !changed {
		t.Error("uninstall must report change when praxis hooks were present")
	}
	cmds := commandsFor(t, readSettings(t, p), "SessionStart")
	if has(cmds, praxis+" ig hook session-start") {
		t.Error("praxis hook not removed")
	}
	if !has(cmds, "flow hook session-start") {
		t.Error("foreign hook wrongly removed")
	}
	// Second uninstall is a no-op.
	changed, err = Uninstall(p, praxis)
	if err != nil {
		t.Fatal(err)
	}
	if changed {
		t.Error("second uninstall must report no change")
	}
}

func TestUninstallMissingFileIsNoop(t *testing.T) {
	changed, err := Uninstall(filepath.Join(t.TempDir(), "nope.json"), praxis)
	if err != nil {
		t.Fatalf("uninstall on missing file must not error: %v", err)
	}
	if changed {
		t.Error("uninstall on missing file must report no change")
	}
}
