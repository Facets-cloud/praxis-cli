package cmd

import (
	"encoding/json"
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
	pat := stubInteractivePAT(t, false) // must not be reached when reuse succeeds
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
	if *pat {
		t.Error("the browser PAT tier ran even though a valid API key was reused")
	}
	if p := mustLoadProfile(t, "default"); p.Store != credentials.StorePraxis ||
		p.AuthMode != "" || p.Token != "existing-praxis-key" {
		t.Errorf("the existing API key was not preserved intact: %+v", p)
	}
}

// The machine-readable contract of the new terminal: an AI host running with
// --json must get a parseable envelope (error + hint + auth exit), not a
// half-broken stream — and still nothing written.
func TestLoginRunE_NoPAT_JSONShape(t *testing.T) {
	isolateHome(t)
	resetLoginFlags(t)
	clearFacetsEnv(t)
	stubPostAuth(t)
	stubInteractivePAT(t, false)
	code := stubOsExit(t)
	loginJSON = true
	loginURL = "https://cp.test"

	out, _ := runLoginRunE(t)
	var d map[string]any
	if err := json.Unmarshal([]byte(out), &d); err != nil {
		t.Fatalf("no-PAT --json is not parseable: %v\n%s", err, out)
	}
	if *code != exitcode.Auth {
		t.Errorf("exit=%d, want Auth(%d)", *code, exitcode.Auth)
	}
	hint, _ := d["hint"].(string)
	if !strings.Contains(hint, "personal-access-tokens") || !strings.Contains(hint, "--token") {
		t.Errorf("hint lacks token-page/--token guidance: %v", d)
	}
	if store, _ := credentials.Load(); len(store) != 0 {
		t.Errorf("credentials written despite the failure: %v", store)
	}
}

// patFallThrough carries the PR's renamed message: it must say no token was
// obtained and must NOT offer to create a Praxis API key. This is the exact
// string the change is named for, asserted directly.
func TestPatFallThrough_NoTokenMessage(t *testing.T) {
	read := captureStderr(t)
	handled, err := patFallThrough("some reason")
	s := read()
	if handled || err != nil {
		t.Fatalf("patFallThrough = %v,%v; want false,nil", handled, err)
	}
	if !strings.Contains(s, "no control-plane token was obtained") {
		t.Errorf("stderr = %q, want the no-token suffix", s)
	}
	if strings.Contains(s, "Praxis API key") || strings.Contains(s, "api key") {
		t.Errorf("fall-through still mentions creating an API key: %q", s)
	}
}

// A dead stored key (a lapsed Praxis API key holder) must NOT be replaced by a
// freshly minted key: login falls through to the PAT tiers, fails with guidance,
// and leaves the old key untouched.
func TestLoginRunE_RejectedStoredKey_FailsWithoutMinting(t *testing.T) {
	isolateHome(t)
	resetLoginFlags(t)
	clearFacetsEnv(t)
	seedProfile(t, "default", "https://cp.test", "dead-key")
	stubPostAuth(t)
	stubInteractivePAT(t, false) // PAT pickup declines too
	stubAuthMe(t, func(_ string, _ map[string]string) (*authMeResponse, error) {
		return nil, errTokenRejected
	})
	code := stubOsExit(t)
	loginURL = "https://cp.test"

	out, _ := runLoginRunE(t)
	if *code != exitcode.Auth {
		t.Errorf("exit=%d, want Auth", *code)
	}
	if !strings.Contains(out, "personal access token") {
		t.Errorf("no token-page guidance: %q", out)
	}
	if got := readPraxisFile(t)["default"]["token"]; got != "dead-key" {
		t.Errorf("stored key changed by a failed login (mint leaked?): %q", got)
	}
}

// A --token that is only whitespace is a paste slip, not a credential: it must
// fall through the chain exactly like an omitted flag, never reaching
// saveAndVerifyToken.
func TestLoginRunE_WhitespaceTokenTreatedAsAbsent(t *testing.T) {
	isolateHome(t)
	resetLoginFlags(t)
	clearFacetsEnv(t)
	stubPostAuth(t)
	terminal := stubNoPAT(t) // reached only if the chain ran to the end
	stubInteractivePAT(t, false)
	stubAuthMe(t, func(_ string, _ map[string]string) (*authMeResponse, error) {
		t.Fatal("whitespace --token was verified as a real token")
		return nil, nil
	})
	loginToken = "   "
	loginURL = "https://cp.test"

	if _, err := runLoginRunE(t); err != nil {
		t.Fatalf("login err: %v", err)
	}
	if !*terminal {
		t.Error("whitespace --token did not fall through like an absent token")
	}
}

// --dry-run must agree with the chain: a --local login that supplies an API key
// is refused by the real command (refuseLocalAPIKey), so the report must say
// refused — not predict a save the command rejects.
func TestLoginDryRun_LocalRefusesSuppliedKey(t *testing.T) {
	isolateHome(t)
	resetLoginFlags(t)
	clearFacetsEnv(t)
	stubPostAuth(t)
	stubOsExit(t)
	stubAuthMe(t, func(_ string, _ map[string]string) (*authMeResponse, error) {
		return &authMeResponse{Email: "u@x"}, nil // the supplied key is valid
	})
	loginLocal = true
	loginToken = "sk_key"
	loginURL = "https://cp.test"

	report := runDryRunJSON(t)
	if se, _ := report["store_effect"].(string); !strings.Contains(se, "refused") {
		t.Errorf("--local --token dry-run must report refused, got %q", se)
	}
}
