package cmd

import (
	"bytes"
	"strings"
	"testing"
)

func TestPromptNudge_MixedGatewayEnvironmentKeepsPraxisRoute(t *testing.T) {
	for _, prompt := range []string{"debug kubernetes pods in production environment", "query New Relic for this Facets environment", "inspect cloud integrations and blueprint overrides"} {
		if got := promptNudge(prompt); !strings.Contains(got, praxisNudge) {
			t.Errorf("gateway route lost for %q: %s", prompt, got)
		}
	}
}

func TestMatches(t *testing.T) {
	tests := []struct {
		prompt string
		want   bool
	}{
		{"add an override to the staging blueprint", true},
		{"how do I log into the control plane", true},
		{"what goes in facets.yaml", true},
		{"debug the failed raptor release", true},
		{"deploy to the prod environment", true},
		{"rename this variable and run the tests", false},
		{"fix the unused import in this file", false},
		// facets/praxis appear in every checkout under facets-repos.
		{"cd into ~/facets-repos/raptor and run go test", false},
		{"open /Users/me/facets-repos/praxis-cli/main.go", false},
		{"the method is overridden in the subclass", false},
		{"", false},
	}
	for _, tc := range tests {
		if got := matches(tc.prompt); got != tc.want {
			t.Errorf("matches(%q) = %v, want %v", tc.prompt, got, tc.want)
		}
	}
}

func runHook(t *testing.T, stdin string) string {
	t.Helper()
	var out bytes.Buffer
	hookCmd.SetOut(&out)
	hookCmd.SetIn(strings.NewReader(stdin))
	t.Cleanup(func() { hookCmd.SetIn(nil) })
	if err := hookCmd.RunE(hookCmd, []string{"user-prompt-submit"}); err != nil {
		t.Fatalf("hook errored: %v", err)
	}
	return out.String()
}

// Gemini calls the event BeforeAgent, so the reply must echo the payload's own
// name rather than a constant.
func TestHookEchoesEventName(t *testing.T) {
	if out := runHook(t, `{"prompt":"check the blueprint","hook_event_name":"BeforeAgent"}`); !strings.Contains(out, `"hookEventName":"BeforeAgent"`) {
		t.Errorf("event name not echoed: %q", out)
	}
	if out := runHook(t, `{"prompt":"check the blueprint"}`); !strings.Contains(out, `"hookEventName":"UserPromptSubmit"`) {
		t.Errorf("missing event name should fall back: %q", out)
	}
}

// Silent paths must print nothing: Gemini fails parsing on stray stdout, and an
// error would cost the user their prompt.
func TestHookSilentPaths(t *testing.T) {
	for _, stdin := range []string{`{"prompt":"unrelated work"}`, `{"prompt":""}`, `{ bad json`, ``} {
		if got := runHook(t, stdin); got != "" {
			t.Errorf("stdin %q → %q, want silence", stdin, got)
		}
	}
}

func TestHookRejectsUnknownArg(t *testing.T) {
	hookCmd.SetIn(strings.NewReader(`{}`))
	t.Cleanup(func() { hookCmd.SetIn(nil) })
	if err := hookCmd.RunE(hookCmd, []string{"cwd-changed"}); err == nil {
		t.Error("an unknown hook arg is a wiring bug and must error")
	}
}

// The exact nudge the agent receives. Asserted whole so that re-adding a
// report-back clause fails here: an earlier "say so if none fit" made every
// trigger-word prompt open with "no praxis skill applies".
func TestNudgeText(t *testing.T) {
	for _, tc := range []struct{ prompt, want string }{{"check the blueprint", "`raptor`"}, {"Praxis memory", "`praxis`"}, {"Kubernetes pod failure", "`praxis`"}, {"read the ig catalog", "`praxis`"}} {
		if got := nudgeContext(t, runHook(t, `{"prompt":"`+tc.prompt+`"}`)); !strings.Contains(got, tc.want) || strings.Contains(got, "praxis-*") {
			t.Errorf("prompt %q routed to %q, want canonical %s", tc.prompt, got, tc.want)
		}
	}
}
