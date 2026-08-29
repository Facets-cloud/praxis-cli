package claudehooks

import (
	"encoding/json"
	"os"
	"path/filepath"
	"strings"
	"testing"
)

const praxis = "/usr/local/bin/praxis"

const igArgs = "ig hook session-start"

// claudeAt is the claude-code host rebased onto a temp file.
func claudeAt(file string) Host {
	h, _ := ByHarness("/nonexistent", "claude-code")
	h.File = file
	return h
}

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
	changed, err := Install(claudeAt(p), praxis)
	if err != nil {
		t.Fatalf("Install: %v", err)
	}
	if !changed {
		t.Error("first install must report changed")
	}
	s := readSettings(t, p)
	if !has(commandsFor(t, s, "SessionStart"), command(praxis, igArgs)) {
		t.Error("SessionStart hook not wired")
	}
	if !has(commandsFor(t, s, "CwdChanged"), command(praxis, "ig hook cwd-changed")) {
		t.Error("CwdChanged hook not wired")
	}
}

func TestInstallIsIdempotent(t *testing.T) {
	p := filepath.Join(t.TempDir(), "settings.json")
	if _, err := Install(claudeAt(p), praxis); err != nil {
		t.Fatal(err)
	}
	changed, err := Install(claudeAt(p), praxis)
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
			if _, err := Install(claudeAt(p), c.from); err != nil {
				t.Fatal(err)
			}
			changed, err := Install(claudeAt(p), c.to)
			if err != nil {
				t.Fatal(err)
			}
			if !changed {
				t.Error("a moved praxis binary must refresh the hook path")
			}
			got := commandsFor(t, readSettings(t, p), "SessionStart")
			if len(got) != 1 || !has(got, command(c.to, igArgs)) {
				t.Errorf("stale path not refreshed to one current entry: %v", got)
			}
			// The refreshed entry must still be recognized as ours (so a further
			// install is idempotent, not duplicating).
			if !isPraxisHookCommand(got[0], igArgs) {
				t.Errorf("refreshed command not recognized as praxis: %q", got[0])
			}
			again, err := Install(claudeAt(p), c.to)
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
	want := command(praxis, igArgs)
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
	if _, err := Install(claudeAt(p), praxis); err != nil {
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
	if _, err := Install(claudeAt(p), praxis); err != nil {
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
	if !has(cmds, command(praxis, igArgs)) {
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
	if _, err := Install(claudeAt(p), praxis); err == nil {
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
	if _, err := Install(claudeAt(p), praxis); err != nil {
		t.Fatal(err)
	}
	changed, err := Uninstall(claudeAt(p))
	if err != nil {
		t.Fatal(err)
	}
	if !changed {
		t.Error("uninstall must report change when praxis hooks were present")
	}
	cmds := commandsFor(t, readSettings(t, p), "SessionStart")
	if has(cmds, command(praxis, igArgs)) {
		t.Error("praxis hook not removed")
	}
	if !has(cmds, "flow hook session-start") {
		t.Error("foreign hook wrongly removed")
	}
	changed, err = Uninstall(claudeAt(p))
	if err != nil {
		t.Fatal(err)
	}
	if changed {
		t.Error("second uninstall must report no change")
	}
}

func TestUninstallMissingFileIsNoop(t *testing.T) {
	changed, err := Uninstall(claudeAt(filepath.Join(t.TempDir(), "nope.json")))
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
		command(praxis, igArgs):                                   true,
		"/usr/local/bin/praxis ig hook session-start":             true, // bare (older) form
		"'/Applications/Praxis CLI/praxis' ig hook session-start": true,
		"flow hook session-start":                                 false,
		"/opt/tool/ig ig hook session-start":                      false, // basename ig, not praxis
	}
	for cmd, want := range cases {
		if got := isPraxisHookCommand(cmd, igArgs); got != want {
			t.Errorf("isPraxisHookCommand(%q) = %v, want %v", cmd, got, want)
		}
	}
}

// Each host names the prompt event differently and Gemini's timeout is in
// milliseconds; getting either wrong writes a file that parses and never fires.
func TestPerHostPromptEventAndTimeout(t *testing.T) {
	tests := []struct {
		harness, key string
		timeout      float64
	}{
		{"claude-code", "UserPromptSubmit", 5},
		{"codex", "UserPromptSubmit", 5},
		{"gemini-cli", "BeforeAgent", 5000},
	}
	for _, tc := range tests {
		t.Run(tc.harness, func(t *testing.T) {
			h, _ := ByHarness("/nonexistent", tc.harness)
			h.File = filepath.Join(t.TempDir(), "hooks.json")
			if _, err := Install(h, praxis); err != nil {
				t.Fatal(err)
			}
			s := readSettings(t, h.File)
			if !has(commandsFor(t, s, tc.key), command(praxis, promptArgs)) {
				t.Fatalf("prompt hook not under %q: %+v", tc.key, s)
			}
			hooks, _ := s["hooks"].(map[string]any)
			list, _ := hooks[tc.key].([]any)
			inner, _ := list[0].(map[string]any)["hooks"].([]any)
			if got, _ := inner[0].(map[string]any)["timeout"].(float64); got != tc.timeout {
				t.Errorf("timeout = %v, want %v", got, tc.timeout)
			}
		})
	}
}

// Antigravity has no hook mechanism, so installing must write nothing.
func TestHooklessHostIsNoop(t *testing.T) {
	h, _ := ByHarness("/nonexistent", "antigravity")
	if changed, err := Install(h, praxis); err != nil || changed {
		t.Errorf("changed=%v err=%v, want no-op", changed, err)
	}
}

// Uninstall must clean every host, not just claude — otherwise logout leaves
// Codex and Gemini pointing at a binary the user signed out of.
func TestUninstallCleansEveryHost(t *testing.T) {
	home := t.TempDir()
	for _, h := range Hosts(home) {
		if len(h.Events) == 0 {
			continue
		}
		if err := os.MkdirAll(filepath.Dir(h.File), 0o755); err != nil {
			t.Fatal(err)
		}
		if err := os.WriteFile(h.File, []byte(`{"hooks":{"UserPromptSubmit":[{"hooks":[{"type":"command","command":"flow hook x"}]}]},"mine":"keep"}`), 0o600); err != nil {
			t.Fatal(err)
		}
		if _, err := Install(h, praxis); err != nil {
			t.Fatal(err)
		}
	}
	for _, h := range Hosts(home) {
		if len(h.Events) == 0 {
			continue
		}
		if _, err := Uninstall(h); err != nil {
			t.Fatal(err)
		}
		raw, err := os.ReadFile(h.File)
		if err != nil {
			t.Fatal(err)
		}
		if strings.Contains(string(raw), praxis) {
			t.Errorf("%s: praxis hook survived", h.Harness)
		}
		if !strings.Contains(string(raw), "flow hook x") || !strings.Contains(string(raw), `"mine": "keep"`) {
			t.Errorf("%s: foreign hook or key lost", h.Harness)
		}
	}
}

// A cask install stages the binary under its per-arch asset name, so an entry
// written from that path must still be recognized as ours — otherwise every
// login appends a duplicate and logout leaves the dead hook behind.
func TestReleaseNamedBinaryIsRecognizedAsOurs(t *testing.T) {
	staged := "/opt/homebrew/Caskroom/praxis/1.6.0/praxis_darwin_arm64"
	if !isPraxisHookCommand(command(staged, igArgs), igArgs) {
		t.Fatalf("staged release binary not recognized as a praxis hook")
	}
	if isPraxisHookCommand("'/usr/bin/praxisfoo' "+igArgs, igArgs) {
		t.Fatalf("foreign praxisfoo binary claimed as ours")
	}
}

func TestInstallReplacesStaleCaskroomEntry(t *testing.T) {
	file := filepath.Join(t.TempDir(), "settings.json")
	h := claudeAt(file)
	if _, err := Install(h, "/opt/homebrew/Caskroom/praxis/1.6.0/praxis_darwin_arm64"); err != nil {
		t.Fatalf("install: %v", err)
	}
	if _, err := Install(h, praxis); err != nil {
		t.Fatalf("reinstall: %v", err)
	}
	got := commandsFor(t, readSettings(t, file), "UserPromptSubmit")
	if len(got) != 1 || got[0] != command(praxis, promptArgs) {
		t.Fatalf("stale Caskroom entry not collapsed: %v", got)
	}
}

// BinaryPath must not resolve symlinks: the resolved target is the
// version-stamped path that the next upgrade deletes.
func TestBinaryPathPrefersTheStablePathEntry(t *testing.T) {
	dir := t.TempDir()
	staged := filepath.Join(dir, "praxis_darwin_arm64")
	if err := os.WriteFile(staged, []byte("#!/bin/sh\n"), 0o755); err != nil {
		t.Fatalf("write staged binary: %v", err)
	}
	binDir := filepath.Join(dir, "bin")
	if err := os.MkdirAll(binDir, 0o755); err != nil {
		t.Fatalf("mkdir bin: %v", err)
	}
	stable := filepath.Join(binDir, "praxis")
	if err := os.Symlink(staged, stable); err != nil {
		t.Fatalf("symlink: %v", err)
	}
	t.Setenv("PATH", binDir)
	fakeExec(t, staged)

	got, ok := StableBinaryPath()
	if !ok {
		t.Fatalf("StableBinaryPath found none, want the bin/praxis symlink")
	}
	if got != stable {
		t.Fatalf("StableBinaryPath = %q, want the stable PATH entry %q", got, stable)
	}
}

// fakeExec points the os.Executable seam at path for one test.
func fakeExec(t *testing.T, path string) {
	t.Helper()
	t.Cleanup(SetExecPathForTest(path))
}

// A praxis elsewhere on PATH is a DIFFERENT install; the running binary wins.
func TestBinaryPathKeepsSelfWhenPathEntryIsAnotherBinary(t *testing.T) {
	dir := t.TempDir()
	self := filepath.Join(dir, "praxis")
	other := filepath.Join(t.TempDir(), "praxis")
	for _, p := range []string{self, other} {
		if err := os.WriteFile(p, []byte("#!/bin/sh\n"), 0o755); err != nil {
			t.Fatalf("write %s: %v", p, err)
		}
	}
	t.Setenv("PATH", filepath.Dir(other))
	fakeExec(t, self)

	if got, ok := StableBinaryPath(); ok {
		t.Fatalf("StableBinaryPath = %q for a different install, want none", got)
	}
	got, err := BinaryPath()
	if err != nil {
		t.Fatalf("BinaryPath: %v", err)
	}
	if got != self {
		t.Fatalf("BinaryPath = %q, want the running binary %q", got, self)
	}
}

func TestRepairRepointsWiredHooksAndAddsNone(t *testing.T) {
	dir := t.TempDir()
	wired := filepath.Join(dir, "wired.json")
	h := claudeAt(wired)
	stale := "/opt/homebrew/Caskroom/praxis/1.6.0/praxis_darwin_arm64"
	if _, err := Install(h, stale); err != nil {
		t.Fatalf("install: %v", err)
	}
	changed, err := Repair(h, praxis)
	if err != nil || !changed {
		t.Fatalf("Repair changed=%v err=%v", changed, err)
	}
	for _, e := range h.Events {
		got := commandsFor(t, readSettings(t, wired), e.key)
		if len(got) != 1 || got[0] != command(praxis, e.args) {
			t.Fatalf("%s = %v, want exactly %q", e.key, got, command(praxis, e.args))
		}
	}

	empty := filepath.Join(dir, "empty.json")
	if changed, err := Repair(claudeAt(empty), praxis); err != nil || changed {
		t.Fatalf("Repair on an unwired host changed=%v err=%v", changed, err)
	}
	if _, err := os.Stat(empty); !os.IsNotExist(err) {
		t.Fatalf("Repair created a settings file for an unwired host")
	}
}
