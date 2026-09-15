package cmd

import (
	"strings"
	"testing"

	"github.com/Facets-cloud/praxis-cli/internal/credentials"
	"github.com/Facets-cloud/praxis-cli/internal/exitcode"
)

// The heart of the "no new Praxis API keys" change: with no stored token, no
// raptor PAT, and the browser PAT pickup declining, login must END — pointing at
// the token page — and must not mint or write anything. Uses the real noPATFn.
func TestLoginRunE_NoPAT_FailsWithGuidanceAndMintsNothing(t *testing.T) {
	isolateHome(t)
	resetLoginFlags(t)
	clearFacetsEnv(t)
	stubPostAuth(t)
	stubInteractivePAT(t, false) // browser PAT pickup declines
	code := stubOsExit(t)
	loginURL = "https://cp.test"

	out, err := runLoginRunE(t)
	if err == nil || *code != exitcode.Auth {
		t.Fatalf("err=%v code=%d; want an auth failure", err, *code)
	}
	if !strings.Contains(out, "personal access token") || !strings.Contains(out, "--token") {
		t.Errorf("failure does not point at the token page and --token: %q", out)
	}
	if strings.Contains(out, "create a Praxis API key") || strings.Contains(out, "api-keys") {
		t.Errorf("output still offers to create a Praxis API key: %q", out)
	}
	if store, _ := credentials.Load(); len(store) != 0 {
		t.Errorf("credentials written despite the failure: %v", store)
	}
}

// Backward compatibility: an existing Praxis API key (no AuthMode → praxis store,
// Bearer) that the server still accepts logs in by reuse, without reaching the
// no-PAT terminal and without any browser. Removing the mint must not break
// keys already in hand.
func TestLoginRunE_ExistingPraxisKeyStillReused(t *testing.T) {
	isolateHome(t)
	resetLoginFlags(t)
	clearFacetsEnv(t)
	seedProfile(t, "default", "https://cp.test", "existing-praxis-key") // no AuthMode = API key
	stubPostAuth(t)
	terminal := stubNoPAT(t)
	stubAuthMe(t, func(_ string, auth map[string]string) (*authMeResponse, error) {
		if auth["Authorization"] != "Bearer existing-praxis-key" {
			t.Errorf("an existing Praxis API key must authenticate as Bearer, got %v", auth)
		}
		return &authMeResponse{Email: "u@x"}, nil
	})

	if _, err := runLoginRunE(t); err != nil {
		t.Fatalf("reuse of an existing Praxis API key failed: %v", err)
	}
	if *terminal {
		t.Error("login reached the no-PAT terminal despite a valid stored API key")
	}
	if p := mustLoadProfile(t, "default"); p.Store != credentials.StorePraxis || p.AuthMode != "" {
		t.Errorf("the existing API key was not preserved as a praxis-store key: %+v", p)
	}
}
