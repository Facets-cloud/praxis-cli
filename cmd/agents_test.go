package cmd

import (
	"bytes"
	"encoding/json"
	"strings"
	"testing"

	"github.com/Facets-cloud/praxis-cli/internal/skillinstall"
)

func TestAgentsCommandEmpty(t *testing.T) {
	orig := listInstalledAgents
	defer func() { listInstalledAgents = orig }()
	listInstalledAgents = func() ([]skillinstall.AgentInstallation, error) { return nil, nil }

	var buf bytes.Buffer
	agentsCmd.SetOut(&buf)
	agentsCmd.SetErr(&buf)
	agentsJSON = false
	if err := agentsCmd.RunE(agentsCmd, nil); err != nil {
		t.Fatalf("RunE: %v", err)
	}
	if !strings.Contains(buf.String(), "No agents installed") {
		t.Errorf("empty pretty output should hint at login:\n%s", buf.String())
	}
}

func TestAgentsCommandPretty(t *testing.T) {
	orig := listInstalledAgents
	defer func() { listInstalledAgents = orig }()
	listInstalledAgents = func() ([]skillinstall.AgentInstallation, error) {
		return []skillinstall.AgentInstallation{
			{AgentName: "praxis-alpha", Kind: "agent", Harness: "claude-code", Path: "/a.md"},
			{AgentName: "praxis-sub-helper", Kind: "subagent", Harness: "claude-code", Path: "/h.md"},
		}, nil
	}

	var buf bytes.Buffer
	agentsCmd.SetOut(&buf)
	agentsCmd.SetErr(&buf)
	agentsJSON = false
	if err := agentsCmd.RunE(agentsCmd, nil); err != nil {
		t.Fatalf("RunE: %v", err)
	}
	out := buf.String()
	if !strings.Contains(out, "praxis-alpha") || !strings.Contains(out, "praxis-sub-helper") {
		t.Errorf("missing rows:\n%s", out)
	}
	if !strings.Contains(out, "agent") || !strings.Contains(out, "subagent") {
		t.Errorf("missing kind column:\n%s", out)
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
