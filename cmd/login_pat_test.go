package cmd

import (
	"bytes"
	"context"
	"encoding/json"
	"errors"
	"io"
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"
	"time"

	"github.com/Facets-cloud/praxis-cli/internal/credentials"
)

// ─── test helpers ────────────────────────────────────────────────────────

// stubPrompts makes the interactive PAT path answerable from a test: a TTY is
// claimed, the line prompt reads from `username`, and the masked prompt returns
// `token`.
func stubPrompts(t *testing.T, username, token string) {
	t.Helper()
	origTTY, origSecret, origLine := stdinIsTTY, readSecret, readLine
	stdinIsTTY = func() bool { return true }
	readLine = func() (string, error) { return username, nil }
	readSecret = func() (string, error) { return token, nil }
	t.Cleanup(func() { stdinIsTTY, readSecret, readLine = origTTY, origSecret, origLine })
}

// stubAuthMode swaps the /auth/status probe, reporting a deployment that
// serves the zero-copy handshake. stubAuthStatus covers the other combinations.
func stubAuthMode(t *testing.T, mode string) {
	t.Helper()
	stubAuthStatus(t, authStatus{Mode: mode, PATHandshake: true})
}

func stubAuthStatus(t *testing.T, st authStatus) {
	t.Helper()
	orig := fetchAuthStatus
	fetchAuthStatus = func(string) authStatus { return st }
	t.Cleanup(func() { fetchAuthStatus = orig })
}

// stubHandshake makes the browser deposit land immediately. Without it the
// poll runs for the full --timeout against a host that does not exist.
func stubHandshake(t *testing.T, cred sessionCredential, err error) *string {
	t.Helper()
	var gotNonce string
	orig := pollSessionFn
	pollSessionFn = func(_ context.Context, _, nonce string, _ time.Duration) (sessionCredential, error) {
		gotNonce = nonce
		return cred, err
	}
	t.Cleanup(func() { pollSessionFn = orig })
	return &gotNonce
}

// stubOpenBrowser records the URL login would open without opening it.
func stubOpenBrowser(t *testing.T) *string {
	t.Helper()
	var got string
	orig := openBrowser
	openBrowser = func(u string) error { got = u; return nil }
	t.Cleanup(func() { openBrowser = orig })
	return &got
}

// ─── patPageURL ──────────────────────────────────────────────────────────

func TestPatPageURL(t *testing.T) {
	// The nonce must survive into the query verbatim: it is the whole
	// credential tying this browser tab to the waiting CLI process.
	const nonce = "abc123"
	tests := []struct {
		name, in, want string
	}{
		{"plain", "https://cp.test", "https://cp.test/ui/ai/cli-login?cli_session=abc123"},
		{"trailing slash", "https://cp.test/", "https://cp.test/ui/ai/cli-login?cli_session=abc123"},
		{"loopback", "http://127.0.0.1:8080", "http://127.0.0.1:8080/ui/ai/cli-login?cli_session=abc123"},
	}
	for _, tc := range tests {
		t.Run(tc.name, func(t *testing.T) {
			if got := patPageURL(tc.in, nonce); got != tc.want {
				t.Errorf("patPageURL(%q) = %q, want %q", tc.in, got, tc.want)
			}
		})
	}
}

// ─── the skip gates ──────────────────────────────────────────────────────

func TestTryInteractivePAT_Skips(t *testing.T) {
	tests := []struct {
		name      string
		asJSON    bool
		authMode  string
		handshake bool
		baseURL   string
	}{
		// --json and a missing TTY no longer skip: the user types nothing, so
		// an AI host gets the same flow (and the raptor credentials with it).
		{name: "not a facets deployment", authMode: "general", handshake: true, baseURL: "https://cp.test"},
		{name: "probe unreachable or too old", authMode: "", handshake: true, baseURL: "https://cp.test"},
		{name: "deployment predates the handshake", authMode: facetsAuthMode, handshake: false, baseURL: "https://cp.test"},
		{name: "plaintext non-loopback url", authMode: facetsAuthMode, handshake: true, baseURL: "http://cp.test"},
	}
	for _, tc := range tests {
		t.Run(tc.name, func(t *testing.T) {
			isolateHome(t)
			resetLoginFlags(t)
			stubAuthStatus(t, authStatus{Mode: tc.authMode, PATHandshake: tc.handshake})
			stubHandshake(t, sessionCredential{}, errors.New("must not poll"))
			stubAuthMe(t, func(string, map[string]string) (*authMeResponse, error) {
				t.Fatal("verified a PAT on a path that should have been skipped")
				return nil, nil
			})

			handled, err := tryInteractivePAT(io.Discard, tc.asJSON, "default", tc.baseURL, false)
			if handled || err != nil {
				t.Errorf("handled=%v err=%v, want false/nil so the API-key flow runs", handled, err)
			}
		})
	}
}

func TestTryInteractivePAT_IncompleteHandshakeFallsThrough(t *testing.T) {
	// Abandoning the browser, or a deposit carrying no username, must hand the
	// login on to the API-key flow rather than fail it. A PAT with no username
	// could never authenticate — it has no identity header to travel with.
	tests := []struct {
		name string
		cred sessionCredential
		err  error
	}{
		{name: "browser never completed", err: context.DeadlineExceeded},
		{name: "deposit carries no username", cred: sessionCredential{Token: "pat-1"}},
	}
	for _, tc := range tests {
		t.Run(tc.name, func(t *testing.T) {
			isolateHome(t)
			resetLoginFlags(t)
			stubAuthMode(t, facetsAuthMode)
			stubOpenBrowser(t)
			stubHandshake(t, tc.cred, tc.err)
			stubAuthMe(t, func(string, map[string]string) (*authMeResponse, error) {
				t.Fatal("verified an incomplete credential")
				return nil, nil
			})

			handled, err := tryInteractivePAT(io.Discard, false, "default", "https://cp.test", false)
			if handled || err != nil {
				t.Errorf("handled=%v err=%v, want false/nil", handled, err)
			}
		})
	}
}

// ─── the happy path ──────────────────────────────────────────────────────

func TestTryInteractivePAT_PersistsHandshakePAT(t *testing.T) {
	isolateHome(t)
	resetLoginFlags(t)
	stubPostAuth(t)
	gotNonce := stubHandshake(t, sessionCredential{Token: "pat-pasted", Username: "u@corp"}, nil)
	stubAuthMode(t, facetsAuthMode)
	opened := stubOpenBrowser(t)
	restoreStderr := captureStderr(t)

	var gotAuth map[string]string
	stubAuthMe(t, func(_ string, auth map[string]string) (*authMeResponse, error) {
		gotAuth = auth
		return &authMeResponse{Email: "u@corp"}, nil
	})

	handled, err := tryInteractivePAT(io.Discard, false, "default", "https://cp.test", false)
	restoreStderr()
	if !handled || err != nil {
		t.Fatalf("handled=%v err=%v, want true/nil", handled, err)
	}
	// The page must carry the SAME nonce the CLI then polls — that pairing is
	// the whole credential linking this browser tab to this process.
	if want := patPageURL("https://cp.test", *gotNonce); *opened != want {
		t.Errorf("opened %q, want %q", *opened, want)
	}
	if len(*gotNonce) != 48 {
		t.Errorf("nonce %q is %d chars, want the 48 the server validates", *gotNonce, len(*gotNonce))
	}
	// A control-plane PAT is only valid alongside the identity header.
	if gotAuth["Authorization"] != "Bearer pat-pasted" || gotAuth["X-Facets-Username"] != "u@corp" {
		t.Errorf("auth headers = %v, want Bearer pat-pasted + X-Facets-Username u@corp", gotAuth)
	}
	prof := mustLoadProfile(t, "default")
	if prof.Token != "pat-pasted" || prof.Username != "u@corp" || prof.AuthMode != credentials.AuthModeBasic {
		t.Errorf("persisted profile = %+v, want the handshake PAT in %q mode", prof, credentials.AuthModeBasic)
	}
}

func TestTryInteractivePAT_RejectedPATFallsThrough(t *testing.T) {
	// The API key is the final fallback, so a PAT the server won't take keeps
	// the chain walking instead of failing the login — matching tryFacetsPAT.
	tests := []struct {
		name     string
		authErr  error
		wantNote string
	}{
		{name: "server rejects it", authErr: errTokenRejected, wantNote: "was not accepted"},
		{name: "server never answers", authErr: errors.New("dial timeout"), wantNote: "could not be verified"},
	}
	for _, tc := range tests {
		t.Run(tc.name, func(t *testing.T) {
			isolateHome(t)
			resetLoginFlags(t)
			stubHandshake(t, sessionCredential{Token: "bad-pat", Username: "u@corp"}, nil)
			stubAuthMode(t, facetsAuthMode)
			stubOpenBrowser(t)
			readStderr := captureStderr(t)
			stubAuthMe(t, func(string, map[string]string) (*authMeResponse, error) {
				return nil, tc.authErr
			})

			handled, err := tryInteractivePAT(io.Discard, false, "default", "https://cp.test", false)
			stderr := readStderr()
			if handled || err != nil {
				t.Fatalf("handled=%v err=%v, want false/nil so the api-key flow runs", handled, err)
			}
			// A silent fallback is the one thing an AI host can't diagnose.
			if !strings.Contains(stderr, tc.wantNote) {
				t.Errorf("stderr = %q, want it to mention %q", stderr, tc.wantNote)
			}
			if prof := mustLoadProfile(t, "default"); prof.Token != "" {
				t.Errorf("persisted a rejected PAT: %+v", prof)
			}
		})
	}
}

// ─── chain order, through login's RunE ───────────────────────────────────

// stubInteractivePAT swaps the interactive-PAT seam. `handled` is what it
// reports, so a test can place it in the chain without driving a prompt.
func stubInteractivePAT(t *testing.T, handled bool) *bool {
	t.Helper()
	called := false
	orig := interactivePATFn
	interactivePATFn = func(_ io.Writer, _ bool, _, _ string, _ bool) (bool, error) {
		called = true
		return handled, nil
	}
	t.Cleanup(func() { interactivePATFn = orig })
	return &called
}

func TestLoginRunE_ChainOrder(t *testing.T) {
	// Tier 2 sits between raptor's stored PAT and the Praxis API key: login
	// must ask for a control-plane PAT before minting an API key, and only
	// reach the API-key browser when the PAT step declines.
	tests := []struct {
		name        string
		patHandled  bool
		wantBrowser bool
	}{
		{name: "pat step handles the login", patHandled: true, wantBrowser: false},
		{name: "pat step declines, api key browser runs", patHandled: false, wantBrowser: true},
	}
	for _, tc := range tests {
		t.Run(tc.name, func(t *testing.T) {
			isolateHome(t)
			resetLoginFlags(t)
			clearFacetsEnv(t)
			stubPostAuth(t)
			browsed := stubBrowserLogin(t)
			patCalled := stubInteractivePAT(t, tc.patHandled)

			loginURL = "https://cp.test"
			if _, err := runLoginRunE(t); err != nil {
				t.Fatalf("login err: %v", err)
			}
			if !*patCalled {
				t.Error("the control-plane PAT step was never reached")
			}
			if *browsed != tc.wantBrowser {
				t.Errorf("api-key browser called = %v, want %v", *browsed, tc.wantBrowser)
			}
		})
	}
}

func TestLoginRunE_ForceStillTriesPAT(t *testing.T) {
	// --force skips the STORED token, not the whole chain: re-authenticating
	// should still prefer a control-plane PAT over minting an API key.
	isolateHome(t)
	resetLoginFlags(t)
	clearFacetsEnv(t)
	seedProfile(t, "default", "https://cp.test", "stale-token")
	stubPostAuth(t)
	browsed := stubBrowserLogin(t)
	patCalled := stubInteractivePAT(t, true)
	stubAuthMe(t, func(string, map[string]string) (*authMeResponse, error) {
		t.Fatal("verified the stored token despite --force")
		return nil, nil
	})

	loginURL, loginForce = "https://cp.test", true
	if _, err := runLoginRunE(t); err != nil {
		t.Fatalf("login err: %v", err)
	}
	if !*patCalled {
		t.Error("--force skipped the control-plane PAT step")
	}
	if *browsed {
		t.Error("--force went straight to the api-key browser")
	}
}

func TestLoginRunE_RaptorPATBeatsInteractivePrompt(t *testing.T) {
	// Tier 1 still wins: a PAT already on the machine means no prompt at all.
	isolateHome(t)
	resetLoginFlags(t)
	clearFacetsEnv(t)
	seedRaptorCreds(t, "[default]\ncontrol_plane_url = https://cp.test\nusername = u@corp\ntoken = pat-from-raptor\n")
	stubPostAuth(t)
	browsed := stubBrowserLogin(t)
	stubAuthMode(t, facetsAuthMode)
	stdinIsTTY = func() bool { t.Fatal("checked for a TTY despite a usable raptor PAT"); return false }
	t.Cleanup(func() { stdinIsTTY = func() bool { return false } })
	stubAuthMe(t, func(string, map[string]string) (*authMeResponse, error) {
		return &authMeResponse{Email: "u@corp"}, nil
	})

	loginURL = "https://cp.test"
	if _, err := runLoginRunE(t); err != nil {
		t.Fatalf("login err: %v", err)
	}
	if *browsed {
		t.Error("browser opened despite a usable raptor PAT")
	}
	if prof := mustLoadProfile(t, "default"); prof.Token != "pat-from-raptor" {
		t.Errorf("persisted token = %q, want the raptor PAT", prof.Token)
	}
}

// ─── the auth-mode probe against a real server ───────────────────────────

func TestFetchAuthMode(t *testing.T) {
	tests := []struct {
		name    string
		status  int
		body    string
		want    string
		wantHit bool
	}{
		{name: "facets mode", status: 200, body: `{"auth_mode":"facets"}`, want: "facets", wantHit: true},
		{name: "case and space tolerant", status: 200, body: `{"auth_mode":" FACETS "}`, want: "facets", wantHit: true},
		{name: "general mode", status: 200, body: `{"auth_mode":"general"}`, want: "general", wantHit: true},
		{name: "endpoint missing on an old deployment", status: 404, body: "", want: "", wantHit: true},
		{name: "unparseable body", status: 200, body: "not json", want: "", wantHit: true},
	}
	for _, tc := range tests {
		t.Run(tc.name, func(t *testing.T) {
			var path string
			srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
				path = r.URL.Path
				w.WriteHeader(tc.status)
				_, _ = w.Write([]byte(tc.body))
			}))
			defer srv.Close()

			if got := fetchAuthStatus(srv.URL).Mode; got != tc.want {
				t.Errorf("fetchAuthStatus().Mode = %q, want %q", got, tc.want)
			}
			if tc.wantHit && path != "/ai-api/auth/status" {
				t.Errorf("probed %q, want /ai-api/auth/status", path)
			}
		})
	}
}

func TestFetchAuthMode_UnreachableIsEmpty(t *testing.T) {
	// A closed port must not be reported as any mode — "" keeps the API-key
	// flow, which is the pre-probe behavior.
	srv := httptest.NewServer(http.HandlerFunc(func(http.ResponseWriter, *http.Request) {}))
	url := srv.URL
	srv.Close()
	if got := fetchAuthStatus(url).Mode; got != "" {
		t.Errorf("fetchAuthStatus on a closed server = %q, want \"\"", got)
	}
}

func TestRunLoginDryRun_ReportsControlPlaneSignIn(t *testing.T) {
	// --dry-run exists to predict login. It shares interactivePATEligible with
	// the real chain, so this pins that they cannot drift.
	//
	// The assertion is on the whole `action:` line, not a substring: "browser"
	// is a substring of "control-plane sign-in (browser), else browser", so a
	// Contains check passes for either answer and proves nothing.
	tests := []struct {
		name      string
		asJSON    bool
		tty       bool
		authMode  string
		handshake bool
		want      string
	}{
		{name: "facets deployment serving the handshake", tty: true, authMode: facetsAuthMode, handshake: true,
			want: "control-plane sign-in (browser), else browser"},
		// Neither of these gates the tier any more: the user types nothing, so
		// an AI host takes the same path and gets raptor configured with it.
		{name: "no tty is no longer a skip", tty: false, authMode: facetsAuthMode, handshake: true,
			want: "control-plane sign-in (browser), else browser"},
		{name: "json output is no longer a skip", asJSON: true, tty: true, authMode: facetsAuthMode, handshake: true,
			want: "control-plane sign-in (browser), else browser"},
		{name: "not a facets deployment", tty: true, authMode: "general", handshake: true, want: "browser"},
		{name: "deployment predates the handshake", tty: true, authMode: facetsAuthMode, handshake: false, want: "browser"},
	}
	for _, tc := range tests {
		t.Run(tc.name, func(t *testing.T) {
			isolateHome(t)
			resetLoginFlags(t)
			t.Cleanup(func() { loginDryRun = false })
			stubPostAuth(t)
			stubAuthStatus(t, authStatus{Mode: tc.authMode, PATHandshake: tc.handshake})
			origTTY := stdinIsTTY
			stdinIsTTY = func() bool { return tc.tty }
			t.Cleanup(func() { stdinIsTTY = origTTY })
			stubOsExit(t)
			stubAuthMe(t, func(string, map[string]string) (*authMeResponse, error) {
				return nil, errTokenRejected // reachable, no credentials
			})

			var buf bytes.Buffer
			if err := runLoginDryRun(&buf, tc.asJSON, "default", "https://cp.test", false); err != nil {
				t.Fatalf("dry run err: %v", err)
			}
			if got := dryRunAction(t, buf.String(), tc.asJSON); got != tc.want {
				t.Errorf("action = %q, want %q", got, tc.want)
			}
		})
	}
}

// dryRunAction pulls the single action value out of either report format, so
// the assertion is an equality check rather than a substring search.
func dryRunAction(t *testing.T, report string, asJSON bool) string {
	t.Helper()
	if asJSON {
		var payload struct {
			Action string `json:"action"`
		}
		if err := json.Unmarshal([]byte(report), &payload); err != nil {
			t.Fatalf("decode dry-run JSON: %v (report %q)", err, report)
		}
		return payload.Action
	}
	for _, line := range strings.Split(report, "\n") {
		if rest, ok := strings.CutPrefix(strings.TrimSpace(line), "action:"); ok {
			return strings.TrimSpace(rest)
		}
	}
	t.Fatalf("no action line in report %q", report)
	return ""
}

func TestPatTransportOK(t *testing.T) {
	// A trust-boundary rule shared by every PAT path, so it gets its own table.
	tests := []struct {
		name, url string
		want      bool
	}{
		{name: "https", url: "https://cp.test", want: true},
		{name: "plaintext remote", url: "http://cp.test", want: false},
		{name: "loopback ip", url: "http://127.0.0.1:8080", want: true},
		{name: "localhost", url: "http://localhost:8080", want: true},
		{name: "ipv6 loopback", url: "http://[::1]:8080", want: true},
		{name: "hostname that merely starts with local", url: "http://localhost.evil.test", want: false},
	}
	for _, tc := range tests {
		t.Run(tc.name, func(t *testing.T) {
			if got := patTransportOK(tc.url); got != tc.want {
				t.Errorf("patTransportOK(%q) = %v, want %v", tc.url, got, tc.want)
			}
		})
	}
}
