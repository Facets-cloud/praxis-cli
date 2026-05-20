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
	a := Agent{Name: "foo", Kind: KindAgent}
	if got := a.PrefixedName(); got != "praxis-foo" {
		t.Errorf("PrefixedName() = %q, want praxis-foo", got)
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

// TestFetchFiltersInactive: only active rows survive the client-side
// filter; the rest are dropped before render+install.
func TestFetchFiltersInactive(t *testing.T) {
	customJSON := `[
		{"name":"alpha","system_prompt":"a","is_active":true},
		{"name":"beta","system_prompt":"b","is_active":false}
	]`

	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		if r.URL.Path == "/ai-api/custom-agents" {
			_, _ = w.Write([]byte(customJSON))
			return
		}
		http.NotFound(w, r)
	}))
	defer srv.Close()

	got, err := Fetch(srv.URL, "test-token")
	if err != nil {
		t.Fatalf("Fetch: %v", err)
	}
	if len(got) != 1 {
		t.Fatalf("want 1 agent after filtering inactive, got %d: %#v", len(got), got)
	}
	if got[0].Name != "alpha" {
		t.Errorf("want alpha, got %q", got[0].Name)
	}
	if got[0].Kind != KindAgent {
		t.Errorf("Kind = %q, want %q", got[0].Kind, KindAgent)
	}
}

// TestFetchServerErrorFails pins the routing + status-preservation
// contract: a 500 on /custom-agents must surface as an error that
// names the endpoint AND preserves the inner HTTP status. A regression
// that swallowed either would silently degrade login.
func TestFetchServerErrorFails(t *testing.T) {
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		http.Error(w, "boom", http.StatusInternalServerError)
	}))
	defer srv.Close()
	_, err := Fetch(srv.URL, "tok")
	if err == nil {
		t.Fatal("expected error on 500, got nil")
	}
	if !strings.Contains(err.Error(), "fetch custom-agents") {
		t.Errorf("error should name the failing endpoint (custom-agents), got: %v", err)
	}
	if !strings.Contains(err.Error(), "HTTP 500") {
		t.Errorf("error should preserve the inner HTTP status, got: %v", err)
	}
}

// TestFetchTolerates404: deployments that don't expose /custom-agents
// (older server versions) install nothing instead of failing login.
// fetchOne returns (nil, nil) on 404; Fetch propagates that as an
// empty result with no error.
func TestFetchTolerates404(t *testing.T) {
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		http.NotFound(w, r)
	}))
	defer srv.Close()
	got, err := Fetch(srv.URL, "tok")
	if err != nil {
		t.Fatalf("Fetch should tolerate 404 on /custom-agents, got: %v", err)
	}
	if len(got) != 0 {
		t.Fatalf("want empty result on 404, got %d: %#v", len(got), got)
	}
}

func TestFetchRequiresBaseURLAndToken(t *testing.T) {
	if _, err := Fetch("", "tok"); err == nil {
		t.Error("empty baseURL: expected error")
	} else if !strings.Contains(err.Error(), "baseURL is required") {
		t.Errorf("empty baseURL: error should name the missing field, got: %v", err)
	}
	if _, err := Fetch("http://x", ""); err == nil {
		t.Error("empty token: expected error")
	} else if !strings.Contains(err.Error(), "token is required") {
		t.Errorf("empty token: error should name the missing field, got: %v", err)
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
	// Pin the rejection reason — a regression that returned a different
	// error (e.g. "unsupported harness") for this input would silently
	// pass the err != nil check.
	if !strings.Contains(err.Error(), "triple-quote sentinel") {
		t.Errorf("error should name the triple-quote sentinel collision, got: %v", err)
	}
}

func TestRenderUnknownHarness(t *testing.T) {
	a := Agent{Name: "x", Description: "y", SystemPrompt: "z", IsActive: true, Kind: KindAgent}
	_, err := a.Render("nonsense")
	if err == nil {
		t.Fatal("unknown harness should error")
	}
	if !strings.Contains(err.Error(), "unsupported harness") {
		t.Errorf("error should name the unsupported harness reason, got: %v", err)
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
