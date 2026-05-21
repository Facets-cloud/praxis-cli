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

// TestInstallOnlyClaudeCodeV1 — agents install to Claude Code only
// for v1 (see supportsAgentInstall doc-comment in installer.go).
// Gemini CLI's and Codex's loaders did not pick up the files when
// runtime-tested, despite documented contracts that match our
// renderer. Until those host runtimes catch up we skip them rather
// than writing files no loader consumes.
func TestInstallOnlyClaudeCodeV1(t *testing.T) {
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
	if len(results) != 1 {
		t.Fatalf("want 1 installation (claude-code only in v1), got %d: %#v", len(results), results)
	}
	got := results[0]
	if got.Harness != "claude-code" {
		t.Errorf("only claude-code should receive agent installs in v1, got %q", got.Harness)
	}
	wantPath := filepath.Join(home, "claude", "praxis-alpha.md")
	if got.Path != wantPath {
		t.Errorf("path = %q, want %q", got.Path, wantPath)
	}
	if _, err := os.Stat(got.Path); err != nil {
		t.Errorf("file should exist at %s: %v", got.Path, err)
	}
	// Negative assertions: gemini and codex agent dirs must be untouched.
	for _, denied := range []string{
		filepath.Join(home, "gemini", "praxis-alpha.md"),
		filepath.Join(home, "codex", "praxis-alpha.toml"),
	} {
		if _, err := os.Stat(denied); !os.IsNotExist(err) {
			t.Errorf("v1 should NOT have written to %s, but it exists", denied)
		}
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
	if len(removed) != 2 {
		t.Fatalf("want 2 removed (2 agents × 1 claude-code host in v1), got %d", len(removed))
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
	if len(entries) != 1 {
		t.Fatalf("want 1 entry (1 agent × 1 claude-code host in v1), got %d", len(entries))
	}
	for _, e := range entries {
		if e.Kind != "agent" {
			t.Errorf("Kind = %q, want \"agent\"", e.Kind)
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
	// 1 claude-code host × 1 agent = 1 entry; second install upserts, never duplicates.
	if len(entries) != 1 {
		t.Errorf("upsert should replace, got %d entries (want 1)", len(entries))
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
