package agentcatalog

import (
	"encoding/json"
	"testing"
)

func TestAgentPrefixedName(t *testing.T) {
	tests := []struct {
		name string
		kind string
		want string
	}{
		{"agent kind gets praxis- prefix", "agent", "praxis-foo"},
		{"subagent kind gets praxis-sub- prefix", "subagent", "praxis-sub-foo"},
		{"empty kind defaults to agent", "", "praxis-foo"},
	}
	for _, tc := range tests {
		t.Run(tc.name, func(t *testing.T) {
			a := Agent{Name: "foo", Kind: tc.kind}
			if got := a.PrefixedName(); got != tc.want {
				t.Errorf("PrefixedName() = %q, want %q", got, tc.want)
			}
		})
	}
}

func TestAgentUnmarshal(t *testing.T) {
	raw := `{
		"name": "terraform-planner",
		"display_name": "Terraform Planner",
		"description": "Reviews terraform plans",
		"icon": "/assets/tf.svg",
		"model": "intelligent",
		"scope": "organization",
		"system_prompt": "You are a terraform reviewer...",
		"is_active": true,
		"enabled_system_mcps": ["cloud_cli"],
		"can_edit": true
	}`
	var a Agent
	if err := json.Unmarshal([]byte(raw), &a); err != nil {
		t.Fatalf("unmarshal: %v", err)
	}
	if a.Name != "terraform-planner" {
		t.Errorf("Name = %q", a.Name)
	}
	if a.SystemPrompt != "You are a terraform reviewer..." {
		t.Errorf("SystemPrompt = %q", a.SystemPrompt)
	}
	if !a.IsActive {
		t.Error("IsActive should be true")
	}
}
