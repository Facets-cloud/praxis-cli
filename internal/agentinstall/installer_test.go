package agentinstall

import (
	"os"
	"path/filepath"
	"testing"

	"github.com/Facets-cloud/praxis-cli/internal/agentcatalog"
	"github.com/Facets-cloud/praxis-cli/internal/harness"
)

func fakeHarnesses(t *testing.T, root string) []harness.Harness {
	t.Helper()
	return []harness.Harness{
		{Name: "claude-code", DisplayName: "Claude Code", Detected: true, AgentDir: filepath.Join(root, "claude")},
		{Name: "gemini-cli", DisplayName: "Gemini CLI", Detected: true, AgentDir: filepath.Join(root, "gemini")},
		{Name: "codex", DisplayName: "Codex", Detected: true, AgentDir: filepath.Join(root, "codex")},
	}
}

func setupHome(t *testing.T) string {
	t.Helper()
	tmp := t.TempDir()
	t.Setenv("HOME", tmp)
	return tmp
}

// TestInstallSupportedHostsOnly — v1 installs agents to Claude Code
// and Gemini CLI (both runtime-verified). Codex is gated off (its
// loader did not surface installed files in smoke testing despite
// matching the documented TOML format). See supportsAgentInstall
// in installer.go for the gating rationale.
func TestInstallSupportedHostsOnly(t *testing.T) {
	home := setupHome(t)
	hosts := fakeHarnesses(t, home)
	a := agentcatalog.Agent{
		Name:         "alpha",
		Description:  "an agent",
		SystemPrompt: "body",
		IsActive:     true,
		Kind:         agentcatalog.KindAgent,
	}

	results, err := Install([]agentcatalog.Agent{a}, hosts)
	if err != nil {
		t.Fatalf("Install: %v", err)
	}
	if len(results) != 2 {
		t.Fatalf("want 2 installations (claude-code + gemini-cli; codex gated), got %d: %#v", len(results), results)
	}
	wantPaths := map[string]string{
		"claude-code": filepath.Join(home, "claude", "praxis-alpha.md"),
		"gemini-cli":  filepath.Join(home, "gemini", "praxis-alpha.md"),
	}
	for _, r := range results {
		want, ok := wantPaths[r.Harness]
		if !ok {
			t.Errorf("unexpected harness %q in results (codex should be gated)", r.Harness)
			continue
		}
		if r.Path != want {
			t.Errorf("%s path = %q, want %q", r.Harness, r.Path, want)
		}
		if _, err := os.Stat(r.Path); err != nil {
			t.Errorf("file should exist at %s: %v", r.Path, err)
		}
	}
	// Negative assertion: Codex agent dir must be untouched.
	codexPath := filepath.Join(home, "codex", "praxis-alpha.toml")
	if _, err := os.Stat(codexPath); !os.IsNotExist(err) {
		t.Errorf("v1 should NOT have written to %s, but it exists", codexPath)
	}
}

func TestUninstallByPrefixRemovesAllAgents(t *testing.T) {
	home := setupHome(t)
	hosts := fakeHarnesses(t, home)
	_, err := Install([]agentcatalog.Agent{
		{Name: "alpha", Description: "a", SystemPrompt: "b", IsActive: true, Kind: agentcatalog.KindAgent},
		{Name: "beta", Description: "b", SystemPrompt: "b", IsActive: true, Kind: agentcatalog.KindAgent},
	}, hosts)
	if err != nil {
		t.Fatalf("Install: %v", err)
	}

	removed, err := UninstallByPrefix("praxis-")
	if err != nil {
		t.Fatalf("UninstallByPrefix: %v", err)
	}
	if len(removed) != 4 {
		t.Fatalf("want 4 removed (2 agents × 2 supported hosts in v1), got %d", len(removed))
	}
	for _, r := range removed {
		if _, err := os.Stat(r.Path); !os.IsNotExist(err) {
			t.Errorf("file should be removed: %s", r.Path)
		}
	}
}

func TestListReturnsReceiptEntries(t *testing.T) {
	home := setupHome(t)
	hosts := fakeHarnesses(t, home)
	if _, err := Install([]agentcatalog.Agent{
		{Name: "alpha", Description: "a", SystemPrompt: "b", IsActive: true, Kind: agentcatalog.KindAgent},
	}, hosts); err != nil {
		t.Fatalf("seed Install failed: %v", err)
	}
	_ = home

	entries, err := List()
	if err != nil {
		t.Fatalf("List: %v", err)
	}
	if len(entries) != 2 {
		t.Fatalf("want 2 entries (1 agent × 2 supported hosts in v1), got %d", len(entries))
	}
	for _, e := range entries {
		if e.Kind != "agent" {
			t.Errorf("Kind = %q, want \"agent\"", e.Kind)
		}
	}
}

// TestRemoveOrphanedByPrefix verifies the orphan-cleanup helper:
// files matching the prefix in a host's AgentDir that aren't in the
// receipt and aren't in the keep set get removed. Files in the keep
// set are preserved; files in the receipt are preserved. The
// extension is stripped before the keep-set lookup.
func TestRemoveOrphanedByPrefix(t *testing.T) {
	home := setupHome(t)
	hosts := fakeHarnesses(t, home)

	// Seed: install one agent normally (ends up in claude + gemini).
	if _, err := Install([]agentcatalog.Agent{
		{Name: "alpha", Description: "a", SystemPrompt: "b", IsActive: true, Kind: agentcatalog.KindAgent},
	}, hosts); err != nil {
		t.Fatalf("seed Install: %v", err)
	}

	// Hand-plant orphans in each host's AgentDir — these are NOT in
	// the receipt and represent leftover files from older praxis-cli
	// installs (or gated hosts).
	orphans := map[string]string{
		filepath.Join(home, "claude", "praxis-orphan.md"):    "claude orphan body",
		filepath.Join(home, "gemini", "praxis-orphan.md"):    "gemini orphan body",
		filepath.Join(home, "codex", "praxis-stranded.toml"): "codex stranded body",
	}
	for path, body := range orphans {
		if err := os.MkdirAll(filepath.Dir(path), 0700); err != nil {
			t.Fatal(err)
		}
		if err := os.WriteFile(path, []byte(body), 0600); err != nil {
			t.Fatal(err)
		}
	}

	// keep set: only alpha (the agent we just installed, via PrefixedName).
	keep := map[string]bool{"praxis-alpha": true}

	removed, err := RemoveOrphanedByPrefix("praxis-", hosts, keep)
	if err != nil {
		t.Fatalf("RemoveOrphanedByPrefix: %v", err)
	}
	if len(removed) != 3 {
		t.Fatalf("want 3 orphans removed, got %d: %#v", len(removed), removed)
	}
	// All seeded orphans should be gone.
	for path := range orphans {
		if _, err := os.Stat(path); !os.IsNotExist(err) {
			t.Errorf("orphan %s should be removed, still exists", path)
		}
	}
	// The kept agent's files (alpha) should survive.
	for _, kept := range []string{
		filepath.Join(home, "claude", "praxis-alpha.md"),
		filepath.Join(home, "gemini", "praxis-alpha.md"),
	} {
		if _, err := os.Stat(kept); err != nil {
			t.Errorf("kept agent %s should still exist: %v", kept, err)
		}
	}
}

func TestUninstallByPrefixRejectsEmptyPrefix(t *testing.T) {
	setupHome(t)
	_, err := UninstallByPrefix("")
	if err == nil {
		t.Fatal("UninstallByPrefix(\"\") should error — empty prefix would wipe everything")
	}
}

func TestInstallUpsertsExistingEntry(t *testing.T) {
	home := setupHome(t)
	hosts := fakeHarnesses(t, home)
	a := agentcatalog.Agent{Name: "alpha", Description: "v1", SystemPrompt: "b", IsActive: true, Kind: agentcatalog.KindAgent}
	if _, err := Install([]agentcatalog.Agent{a}, hosts); err != nil {
		t.Fatalf("first Install: %v", err)
	}
	// Re-install (e.g. refresh): upsert should replace, not duplicate.
	a.Description = "v2"
	if _, err := Install([]agentcatalog.Agent{a}, hosts); err != nil {
		t.Fatalf("second Install: %v", err)
	}
	entries, err := List()
	if err != nil {
		t.Fatalf("List: %v", err)
	}
	// 2 supported hosts × 1 agent = 2 entries; second install upserts, never duplicates.
	if len(entries) != 2 {
		t.Errorf("upsert should replace, got %d entries (want 2)", len(entries))
	}
}

func TestListNormalizesEmptyKind(t *testing.T) {
	home := setupHome(t)
	// Hand-craft a receipt with one entry that has Kind == "" (simulates
	// an old in-flight receipt that pre-dates the Kind field — defense
	// in depth for future schema evolution).
	receiptPath := filepath.Join(home, ".praxis", "installed.json")
	if err := os.MkdirAll(filepath.Dir(receiptPath), 0700); err != nil {
		t.Fatal(err)
	}
	raw := `{"skills":[],"agents":[{"agent_name":"praxis-legacy","kind":"","harness":"claude-code","path":"/x.md","installed_at":"2026-01-01T00:00:00Z"}]}`
	if err := os.WriteFile(receiptPath, []byte(raw), 0600); err != nil {
		t.Fatal(err)
	}
	entries, err := List()
	if err != nil {
		t.Fatalf("List: %v", err)
	}
	if len(entries) != 1 {
		t.Fatalf("want 1 entry, got %d", len(entries))
	}
	if entries[0].Kind != "agent" {
		t.Errorf("empty kind should be normalized to \"agent\", got %q", entries[0].Kind)
	}
}

func TestListRejectsCorruptReceipt(t *testing.T) {
	home := setupHome(t)
	receiptPath := filepath.Join(home, ".praxis", "installed.json")
	if err := os.MkdirAll(filepath.Dir(receiptPath), 0700); err != nil {
		t.Fatal(err)
	}
	// Garbage JSON — List should return a parse error, not panic.
	if err := os.WriteFile(receiptPath, []byte(`{not json`), 0600); err != nil {
		t.Fatal(err)
	}
	if _, err := List(); err == nil {
		t.Fatal("List should return error for corrupt receipt JSON")
	}
}

func TestSaveReceiptFailsOnUnwritableDir(t *testing.T) {
	home := setupHome(t)
	dir := filepath.Join(home, ".praxis")
	if err := os.MkdirAll(dir, 0700); err != nil {
		t.Fatal(err)
	}
	// Drop write permission on the dir so CreateTemp inside it fails.
	if err := os.Chmod(dir, 0500); err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { _ = os.Chmod(dir, 0700) })

	hosts := fakeHarnesses(t, home)
	_, err := Install([]agentcatalog.Agent{
		{Name: "x", Description: "y", SystemPrompt: "z", IsActive: true, Kind: agentcatalog.KindAgent},
	}, hosts)
	if err == nil {
		t.Fatal("Install should fail to save receipt when ~/.praxis is read-only")
	}
}

func TestInstallSkipsUndetectedHarnesses(t *testing.T) {
	home := setupHome(t)
	hosts := []harness.Harness{
		{Name: "claude-code", Detected: true, AgentDir: filepath.Join(home, "claude")},
		{Name: "gemini-cli", Detected: false, AgentDir: filepath.Join(home, "gemini")},
	}
	results, err := Install([]agentcatalog.Agent{
		{Name: "x", Description: "y", SystemPrompt: "z", IsActive: true, Kind: agentcatalog.KindAgent},
	}, hosts)
	if err != nil {
		t.Fatalf("Install: %v", err)
	}
	if len(results) != 1 {
		t.Fatalf("want 1 install (skipping undetected), got %d", len(results))
	}
}
