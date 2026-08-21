package cmd

import (
	"bytes"
	"errors"
	"io"
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"

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

// stubAuthMode swaps the /auth/status probe.
func stubAuthMode(t *testing.T, mode string) {
	t.Helper()
	orig := fetchAuthMode
	fetchAuthMode = func(string) string { return mode }
	t.Cleanup(func() { fetchAuthMode = orig })
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
	// Must stay byte-identical to what `raptor login` opens
	// (raptor/cmd/login.go: baseURL + "/v2/home#personal-access-tokens"), so a
	// token created for one CLI is the same credential the other asks for.
	tests := []struct {
		name, in, want string
	}{
		{"plain", "https://cp.test", "https://cp.test/v2/home#personal-access-tokens"},
		{"trailing slash", "https://cp.test/", "https://cp.test/v2/home#personal-access-tokens"},
		{"loopback", "http://127.0.0.1:8080", "http://127.0.0.1:8080/v2/home#personal-access-tokens"},
	}
	for _, tc := range tests {
		t.Run(tc.name, func(t *testing.T) {
			if got := patPageURL(tc.in); got != tc.want {
				t.Errorf("patPageURL(%q) = %q, want %q", tc.in, got, tc.want)
			}
		})
	}
}

// ─── the skip gates ──────────────────────────────────────────────────────

func TestTryInteractivePAT_Skips(t *testing.T) {
	tests := []struct {
		name     string
		asJSON   bool
		tty      bool
		authMode string
		baseURL  string
	}{
		{name: "json output is machine-invoked", asJSON: true, tty: true, authMode: facetsAuthMode, baseURL: "https://cp.test"},
		{name: "no tty to prompt on", tty: false, authMode: facetsAuthMode, baseURL: "https://cp.test"},
		{name: "not a facets deployment", tty: true, authMode: "general", baseURL: "https://cp.test"},
		{name: "probe unreachable or too old", tty: true, authMode: "", baseURL: "https://cp.test"},
		{name: "plaintext non-loopback url", tty: true, authMode: facetsAuthMode, baseURL: "http://cp.test"},
	}
	for _, tc := range tests {
		t.Run(tc.name, func(t *testing.T) {
			isolateHome(t)
			resetLoginFlags(t)
			stubPrompts(t, "u@corp", "pat-should-not-be-sent")
			stdinIsTTY = func() bool { return tc.tty }
			stubAuthMode(t, tc.authMode)
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

func TestTryInteractivePAT_EmptyAnswerFallsThrough(t *testing.T) {
	// Pressing Enter at either prompt is the documented way to say "skip this,
	// give me a Praxis API key" — it must not be an error.
	tests := []struct {
		name, username, token string
	}{
		{"blank username", "", "tok"},
		{"blank token", "u@corp", ""},
	}
	for _, tc := range tests {
		t.Run(tc.name, func(t *testing.T) {
			isolateHome(t)
			resetLoginFlags(t)
			stubPrompts(t, tc.username, tc.token)
			stubAuthMode(t, facetsAuthMode)
			stubOpenBrowser(t)
			stubAuthMe(t, func(string, map[string]string) (*authMeResponse, error) {
				t.Fatal("verified empty credentials")
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

func TestTryInteractivePAT_PersistsPastedPAT(t *testing.T) {
	isolateHome(t)
	resetLoginFlags(t)
	stubPostAuth(t)
	stubPrompts(t, "u@corp", "pat-pasted")
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
	if *opened != "https://cp.test/v2/home#personal-access-tokens" {
		t.Errorf("opened %q, want the control plane's PAT page", *opened)
	}
	// A control-plane PAT is only valid alongside the identity header.
	if gotAuth["Authorization"] != "Bearer pat-pasted" || gotAuth["X-Facets-Username"] != "u@corp" {
		t.Errorf("auth headers = %v, want Bearer pat-pasted + X-Facets-Username u@corp", gotAuth)
	}
	prof := mustLoadProfile(t, "default")
	if prof.Token != "pat-pasted" || prof.Username != "u@corp" || prof.AuthMode != credentials.AuthModeBasic {
		t.Errorf("persisted profile = %+v, want the pasted PAT in %q mode", prof, credentials.AuthModeBasic)
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
			stubPrompts(t, "u@corp", "bad-pat")
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

			if got := fetchAuthMode(srv.URL); got != tc.want {
				t.Errorf("fetchAuthMode = %q, want %q", got, tc.want)
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
	if got := fetchAuthMode(url); got != "" {
		t.Errorf("fetchAuthMode on a closed server = %q, want \"\"", got)
	}
}

func TestRunLoginDryRun_ReportsPATPrompt(t *testing.T) {
	// --dry-run exists to predict login. Once the PAT prompt sits in front of
	// the api-key browser, a report that still says "browser" is wrong.
	tests := []struct {
		name     string
		asJSON   bool
		tty      bool
		authMode string
		want     string
	}{
		{name: "human at a tty on a facets deployment", tty: true, authMode: facetsAuthMode,
			want: "control-plane PAT prompt, else browser"},
		{name: "not a facets deployment", tty: true, authMode: "general", want: "browser"},
		{name: "no tty means login would not prompt either", tty: false, authMode: facetsAuthMode, want: "browser"},
		// JSON output means an AI host is calling, and login skips the prompt
		// there too — so the report must keep saying browser.
		{name: "json output", asJSON: true, tty: true, authMode: facetsAuthMode, want: "browser"},
	}
	for _, tc := range tests {
		t.Run(tc.name, func(t *testing.T) {
			isolateHome(t)
			resetLoginFlags(t)
			t.Cleanup(func() { loginDryRun = false })
			stubPostAuth(t)
			stubAuthMode(t, tc.authMode)
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
			if !strings.Contains(buf.String(), tc.want) {
				t.Errorf("report = %q, want it to contain %q", buf.String(), tc.want)
			}
		})
	}
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
