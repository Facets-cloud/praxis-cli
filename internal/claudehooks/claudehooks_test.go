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
	if !has(commandsFor(t, s, "SessionStart"), command(praxis, "session-start")) {
		t.Error("SessionStart hook not wired")
	}
	if !has(commandsFor(t, s, "CwdChanged"), command(praxis, "cwd-changed")) {
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
	if got := commandsFor(t, readSettings(t, p), "SessionStart"); len(got) != 1 {
		t.Errorf("expected 1 SessionStart command, got %d: %v", len(got), got)
	}
}

// TestInstallRefreshesStalePath covers a moved binary, including an executable
// path containing spaces (which must round-trip through shell-quoting and still
// be recognized as ours on the next install).
func TestInstallRefreshesStalePath(t *testing.T) {
	cases := []struct{ name, from, to string }{
		{"plain", "/old/path/praxis", praxis},
		{"to-spaced", praxis, "/Applications/Praxis CLI/praxis"},
		{"from-spaced", "/Applications/Praxis CLI/praxis", praxis},
	}
	for _, c := range cases {
		t.Run(c.name, func(t *testing.T) {
			p := filepath.Join(t.TempDir(), "settings.json")
			if _, err := Install(p, c.from); err != nil {
				t.Fatal(err)
			}
			changed, err := Install(p, c.to)
			if err != nil {
				t.Fatal(err)
			}
			if !changed {
				t.Error("a moved praxis binary must refresh the hook path")
			}
			got := commandsFor(t, readSettings(t, p), "SessionStart")
			if len(got) != 1 || !has(got, command(c.to, "session-start")) {
				t.Errorf("stale path not refreshed to one current entry: %v", got)
			}
			// The refreshed entry must still be recognized as ours (so a further
			// install is idempotent, not duplicating).
			if !isPraxisHookCommand(got[0], "session-start") {
				t.Errorf("refreshed command not recognized as praxis: %q", got[0])
			}
			again, err := Install(p, c.to)
			if err != nil {
				t.Fatal(err)
			}
			if again {
				t.Errorf("re-installing the same (possibly spaced) path must be idempotent")
			}
		})
	}
}

func TestInstallCollapsesDuplicateEntries(t *testing.T) {
	// A prior bug could leave two praxis entries for one event; install must
	// normalize to exactly one.
	p := filepath.Join(t.TempDir(), "settings.json")
	want := command(praxis, "session-start")
	seed := map[string]any{
		"hooks": map[string]any{
			"SessionStart": []any{
				map[string]any{"hooks": []any{map[string]any{"type": "command", "command": want}}},
				map[string]any{"hooks": []any{map[string]any{"type": "command", "command": want}}},
			},
		},
	}
	b, _ := json.MarshalIndent(seed, "", "  ")
	if err := os.WriteFile(p, b, 0o600); err != nil {
		t.Fatal(err)
	}
	if _, err := Install(p, praxis); err != nil {
		t.Fatal(err)
	}
	if got := commandsFor(t, readSettings(t, p), "SessionStart"); len(got) != 1 {
		t.Errorf("duplicate praxis hooks must collapse to one, got %d: %v", len(got), got)
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
	if err := os.WriteFile(p, b, 0o600); err != nil {
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
	if !has(cmds, command(praxis, "session-start")) {
		t.Error("praxis hook not added alongside foreign hook")
	}
	// A .bak of the prior file is kept, and NOT world/group readable (it may
	// contain credentials).
	fi, err := os.Stat(p + ".bak")
	if err != nil {
		t.Fatalf("expected settings.json.bak, got %v", err)
	}
	if perm := fi.Mode().Perm(); perm&0o077 != 0 {
		t.Errorf("settings backup must not be group/world readable, got %o", perm)
	}
}

func TestInstallRefusesInvalidJSON(t *testing.T) {
	p := filepath.Join(t.TempDir(), "settings.json")
	if err := os.WriteFile(p, []byte("{ not json"), 0o600); err != nil {
		t.Fatal(err)
	}
	if _, err := Install(p, praxis); err == nil {
		t.Error("Install must refuse to overwrite invalid JSON")
	}
	if b, _ := os.ReadFile(p); string(b) != "{ not json" {
		t.Error("invalid settings.json was modified")
	}
}

func TestUninstallRemovesOnlyPraxisHooks(t *testing.T) {
	p := filepath.Join(t.TempDir(), "settings.json")
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
	if err := os.WriteFile(p, b, 0o600); err != nil {
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
	if has(cmds, command(praxis, "session-start")) {
		t.Error("praxis hook not removed")
	}
	if !has(cmds, "flow hook session-start") {
		t.Error("foreign hook wrongly removed")
	}
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

func TestIsPraxisHookCommandRejectsForeignAndQuotedForeign(t *testing.T) {
	// Ownership is by argv[0] basename, quote-aware.
	cases := map[string]bool{
		command(praxis, "session-start"):                          true,
		"/usr/local/bin/praxis ig hook session-start":             true, // bare (older) form
		"'/Applications/Praxis CLI/praxis' ig hook session-start": true,
		"flow hook session-start":                                 false,
		"/opt/tool/ig ig hook session-start":                      false, // basename ig, not praxis
	}
	for cmd, want := range cases {
		if got := isPraxisHookCommand(cmd, "session-start"); got != want {
			t.Errorf("isPraxisHookCommand(%q) = %v, want %v", cmd, got, want)
		}
	}
}
