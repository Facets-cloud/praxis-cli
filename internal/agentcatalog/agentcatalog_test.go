package agentcatalog

import (
	"encoding/json"
	"net/http"
	"net/http/httptest"
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

func TestFetchMergesAndFilters(t *testing.T) {
	customJSON := `[
		{"name":"alpha","system_prompt":"a","is_active":true},
		{"name":"beta","system_prompt":"b","is_active":false}
	]`
	subagentJSON := `[
		{"name":"helper","system_prompt":"h","is_active":true,"parent_agent_name":""},
		{"name":"bound","system_prompt":"bd","is_active":true,"parent_agent_name":"alpha"}
	]`

	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		switch r.URL.Path {
		case "/ai-api/custom-agents":
			_, _ = w.Write([]byte(customJSON))
		case "/ai-api/subagents":
			_, _ = w.Write([]byte(subagentJSON))
		default:
			http.NotFound(w, r)
		}
	}))
	defer srv.Close()

	got, err := Fetch(srv.URL, "test-token")
	if err != nil {
		t.Fatalf("Fetch: %v", err)
	}
	if len(got) != 2 {
		t.Fatalf("want 2 agents after filtering, got %d: %#v", len(got), got)
	}
	wantByName := map[string]string{"alpha": KindAgent, "helper": KindSubagent}
	for _, a := range got {
		want, ok := wantByName[a.Name]
		if !ok {
			t.Errorf("unexpected agent %q in result", a.Name)
			continue
		}
		if a.Kind != want {
			t.Errorf("agent %q: Kind = %q, want %q", a.Name, a.Kind, want)
		}
	}
}

func TestFetchPartialFailureFailsHard(t *testing.T) {
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		switch r.URL.Path {
		case "/ai-api/custom-agents":
			_, _ = w.Write([]byte(`[]`))
		case "/ai-api/subagents":
			http.Error(w, "boom", http.StatusInternalServerError)
		}
	}))
	defer srv.Close()
	_, err := Fetch(srv.URL, "tok")
	if err == nil {
		t.Fatal("expected error on partial failure, got nil")
	}
}

func TestFetchRequiresBaseURLAndToken(t *testing.T) {
	if _, err := Fetch("", "tok"); err == nil {
		t.Error("empty baseURL: expected error")
	}
	if _, err := Fetch("http://x", ""); err == nil {
		t.Error("empty token: expected error")
	}
}
