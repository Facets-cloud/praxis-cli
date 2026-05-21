package cmd

import (
	"bytes"
	"encoding/json"
	"strings"
	"testing"

	"github.com/Facets-cloud/praxis-cli/internal/skillinstall"
)

// TestAgentsCommandEmptyJSON covers the AI-host path: stdout is non-TTY
// (bytes.Buffer) so UseJSON auto-resolves to true, and an empty receipt
// produces `[]` not English text — preserving the parseable-JSON
// contract every AI-callable command holds.
func TestAgentsCommandEmptyJSON(t *testing.T) {
	orig := listInstalledAgents
	defer func() { listInstalledAgents = orig }()
	listInstalledAgents = func() ([]skillinstall.AgentInstallation, error) { return nil, nil }

	var buf bytes.Buffer
	agentsCmd.SetOut(&buf)
	agentsCmd.SetErr(&buf)
	agentsJSON = false
	defer func() { agentsJSON = false }()
	if err := agentsCmd.RunE(agentsCmd, nil); err != nil {
		t.Fatalf("RunE: %v", err)
	}
	var got []map[string]string
	if err := json.Unmarshal(buf.Bytes(), &got); err != nil {
		t.Fatalf("empty result should be parseable JSON `[]`, got %q: %v", buf.String(), err)
	}
	if len(got) != 0 {
		t.Errorf("expected empty array, got %d entries: %v", len(got), got)
	}
}

// TestPrintAgentsPretty exercises the pretty formatter directly,
// bypassing the cobra RunE + UseJSON TTY-detection path (which would
// auto-resolve to JSON when stdout is a bytes.Buffer). This is the
// right shape to test the pretty branch — it pins format invariants
// (table header, no JSON-shape prefix, data rows present) without
// needing a TTY emulator in tests.
func TestPrintAgentsPretty(t *testing.T) {
	entries := []agentEntryForOutput{
		{AgentName: "praxis-alpha", Kind: "agent", Harness: "claude-code", Path: "/a.md"},
		{AgentName: "praxis-beta", Kind: "agent", Harness: "gemini-cli", Path: "/b.md"},
	}

	var buf bytes.Buffer
	printAgentsPretty(&buf, entries)
	out := buf.String()

	// Pretty-specific assertions a regression to JSON-fallback would fail:
	trimmed := strings.TrimSpace(out)
	if strings.HasPrefix(trimmed, "{") || strings.HasPrefix(trimmed, "[") {
		t.Errorf("pretty output should not begin with a JSON-shape character; got:\n%s", out[:min(200, len(out))])
	}
	for _, header := range []string{"NAME", "KIND", "HARNESS", "PATH"} {
		if !strings.Contains(out, header) {
			t.Errorf("pretty output should include column header %q; got:\n%s", header, out)
		}
	}
	if !strings.Contains(out, "praxis-alpha") || !strings.Contains(out, "praxis-beta") {
		t.Errorf("pretty output missing data rows; got:\n%s", out)
	}
}

func TestAgentsCommandJSON(t *testing.T) {
	orig := listInstalledAgents
	defer func() { listInstalledAgents = orig }()
	listInstalledAgents = func() ([]skillinstall.AgentInstallation, error) {
		return []skillinstall.AgentInstallation{
			{AgentName: "praxis-alpha", Kind: "agent", Harness: "claude-code", Path: "/a.md"},
		}, nil
	}

	var buf bytes.Buffer
	agentsCmd.SetOut(&buf)
	agentsCmd.SetErr(&buf)
	agentsJSON = true
	defer func() { agentsJSON = false }()
	if err := agentsCmd.RunE(agentsCmd, nil); err != nil {
		t.Fatalf("RunE: %v", err)
	}
	var got []map[string]string
	if err := json.Unmarshal(buf.Bytes(), &got); err != nil {
		t.Fatalf("invalid JSON: %v\n%s", err, buf.String())
	}
	if len(got) != 1 || got[0]["agent_name"] != "praxis-alpha" {
		t.Errorf("unexpected JSON shape: %#v", got)
	}
}
