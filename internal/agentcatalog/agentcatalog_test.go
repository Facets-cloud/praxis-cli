package agentcatalog

import (
	"encoding/json"
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"

	"github.com/Facets-cloud/praxis-cli/internal/render"
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

// 404 on one endpoint is not a partial failure — it's "this Praxis
// deployment doesn't expose this resource type." Fetch should return
// whatever the OTHER endpoint provided rather than aborting the
// install. Real-world trigger: deployments that ship custom-agents
// but not yet subagents.
func TestFetchTolerates404FromOneEndpoint(t *testing.T) {
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		switch r.URL.Path {
		case "/ai-api/custom-agents":
			_, _ = w.Write([]byte(`[{"name":"alpha","system_prompt":"a","is_active":true}]`))
		case "/ai-api/subagents":
			http.NotFound(w, r)
		}
	}))
	defer srv.Close()
	got, err := Fetch(srv.URL, "tok")
	if err != nil {
		t.Fatalf("Fetch should tolerate 404 on one endpoint, got: %v", err)
	}
	if len(got) != 1 || got[0].Name != "alpha" {
		t.Fatalf("want 1 alpha agent (subagents 404'd, custom-agents returned 1), got %#v", got)
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

func TestRenderYAMLForClaudeCode(t *testing.T) {
	a := Agent{
		Name:         "tf-planner",
		Description:  "Reviews terraform plans",
		SystemPrompt: "You are a terraform reviewer.\nLook for risks.",
		IsActive:     true,
		Kind:         KindAgent,
	}
	out, err := a.Render("claude-code")
	if err != nil {
		t.Fatalf("Render: %v", err)
	}
	wantPrefix := "---\nname: \"praxis-tf-planner\"\ndescription: \"Reviews terraform plans\"\n---\n"
	if !strings.HasPrefix(out, wantPrefix) {
		t.Errorf("frontmatter wrong:\n%s", out[:200])
	}
	if !strings.Contains(out, render.ExecutionPreamble) {
		t.Error("rendered body must include the execution preamble")
	}
	if !strings.Contains(out, "You are a terraform reviewer.\nLook for risks.") {
		t.Error("rendered body must include the system_prompt verbatim")
	}
}

func TestRenderYAMLForGeminiCLI(t *testing.T) {
	a := Agent{Name: "x", Description: "y", SystemPrompt: "z", IsActive: true, Kind: KindAgent}
	out, err := a.Render("gemini-cli")
	if err != nil {
		t.Fatalf("Render: %v", err)
	}
	if !strings.HasPrefix(out, "---\nname: \"praxis-x\"") {
		t.Errorf("gemini render should use same YAML format as claude:\n%s", out)
	}
}

func TestRenderTOMLForCodex(t *testing.T) {
	a := Agent{
		Name:         "tf-planner",
		Description:  "Reviews \"terraform\" plans",
		SystemPrompt: "body content",
		IsActive:     true,
		Kind:         KindAgent,
	}
	out, err := a.Render("codex")
	if err != nil {
		t.Fatalf("Render: %v", err)
	}
	wantHeader := "name = \"praxis-tf-planner\"\ndescription = \"Reviews \\\"terraform\\\" plans\"\ndeveloper_instructions = \"\"\"\n"
	if !strings.HasPrefix(out, wantHeader) {
		t.Errorf("TOML header wrong:\n%s", out[:200])
	}
	if !strings.HasSuffix(out, "\"\"\"\n") {
		t.Errorf("TOML body must close on triple-quote sentinel:\n...%s", out[len(out)-50:])
	}
	if !strings.Contains(out, render.ExecutionPreamble) {
		t.Error("TOML body must include the execution preamble")
	}
}

func TestRenderTOMLRejectsTripleQuoteInPrompt(t *testing.T) {
	a := Agent{
		Name:         "x",
		Description:  "y",
		SystemPrompt: `here is """ inside`,
		IsActive:     true,
		Kind:         KindAgent,
	}
	_, err := a.Render("codex")
	if err == nil {
		t.Fatal("Render should reject system_prompt containing triple-quote — Codex TOML sentinel collision")
	}
}

func TestRenderSubagentUsesSubPrefix(t *testing.T) {
	a := Agent{Name: "log-analyzer", Description: "d", SystemPrompt: "p", IsActive: true, Kind: KindSubagent}
	out, _ := a.Render("claude-code")
	if !strings.Contains(out, "name: \"praxis-sub-log-analyzer\"") {
		t.Errorf("subagent should render with praxis-sub- prefix in frontmatter:\n%s", out[:200])
	}
}

func TestRenderUnknownHarness(t *testing.T) {
	a := Agent{Name: "x", Description: "y", SystemPrompt: "z", IsActive: true, Kind: KindAgent}
	if _, err := a.Render("nonsense"); err == nil {
		t.Fatal("unknown harness should error")
	}
}
