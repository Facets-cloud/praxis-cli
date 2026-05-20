package agentinstall

import (
	"os"
	"path/filepath"
	"strings"
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

func TestInstallFansOutToAllHarnesses(t *testing.T) {
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
	if len(results) != 3 {
		t.Fatalf("want 3 installations, got %d", len(results))
	}
	wantPaths := map[string]string{
		"claude-code": filepath.Join(home, "claude", "praxis-alpha.md"),
		"gemini-cli":  filepath.Join(home, "gemini", "praxis-alpha.md"),
		"codex":       filepath.Join(home, "codex", "praxis-alpha.toml"),
	}
	for _, r := range results {
		want, ok := wantPaths[r.Harness]
		if !ok {
			t.Errorf("unexpected harness %q in results", r.Harness)
			continue
		}
		if r.Path != want {
			t.Errorf("%s path = %q, want %q", r.Harness, r.Path, want)
		}
		if _, err := os.Stat(r.Path); err != nil {
			t.Errorf("file should exist at %s: %v", r.Path, err)
		}
	}
}

func TestInstallSubagentUsesSubPrefix(t *testing.T) {
	home := setupHome(t)
	hosts := fakeHarnesses(t, home)
	sub := agentcatalog.Agent{
		Name: "helper", Description: "h", SystemPrompt: "b",
		IsActive: true, Kind: agentcatalog.KindSubagent,
	}
	results, err := Install([]agentcatalog.Agent{sub}, hosts)
	if err != nil {
		t.Fatalf("Install: %v", err)
	}
	for _, r := range results {
		if !strings.Contains(r.Path, "praxis-sub-helper.") {
			t.Errorf("subagent should use praxis-sub- prefix; got %s", r.Path)
		}
	}
}

func TestUninstallByPrefixRemovesBothAgentsAndSubagents(t *testing.T) {
	home := setupHome(t)
	hosts := fakeHarnesses(t, home)
	_, err := Install([]agentcatalog.Agent{
		{Name: "alpha", Description: "a", SystemPrompt: "b", IsActive: true, Kind: agentcatalog.KindAgent},
		{Name: "helper", Description: "h", SystemPrompt: "b", IsActive: true, Kind: agentcatalog.KindSubagent},
	}, hosts)
	if err != nil {
		t.Fatalf("Install: %v", err)
	}

	removed, err := UninstallByPrefix("praxis-")
	if err != nil {
		t.Fatalf("UninstallByPrefix: %v", err)
	}
	if len(removed) != 6 {
		t.Fatalf("want 6 removed (2 agents × 3 hosts), got %d", len(removed))
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
	_, _ = Install([]agentcatalog.Agent{
		{Name: "alpha", Description: "a", SystemPrompt: "b", IsActive: true, Kind: agentcatalog.KindAgent},
	}, hosts)
	_ = home

	entries, err := List()
	if err != nil {
		t.Fatalf("List: %v", err)
	}
	if len(entries) != 3 {
		t.Fatalf("want 3 entries (1 agent × 3 hosts), got %d", len(entries))
	}
	for _, e := range entries {
		if e.Kind != "agent" {
			t.Errorf("Kind = %q, want \"agent\"", e.Kind)
		}
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
