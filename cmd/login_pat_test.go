package cmd

import (
	"bytes"
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

// stubTTY claims (or denies) a terminal so the PAT eligibility gate can be
// driven without a real stdin.
func stubTTY(t *testing.T, on bool) {
	t.Helper()
	orig := stdinIsTTY
	stdinIsTTY = func() bool { return on }
	t.Cleanup(func() { stdinIsTTY = orig })
}

// stubReadLine drives the Enter-to-skip watcher (and the URL prompt) from a
// test: readLine returns line without touching a real stdin.
func stubReadLine(t *testing.T, line string) {
	t.Helper()
	orig := readLine
	readLine = func() (string, error) { return line, nil }
	t.Cleanup(func() { readLine = orig })
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

// patDepositServer stands in for the control plane's cli-session endpoint that
// the PAT flow polls: 200 with the given plaintext_key, or — when it is "" —
// 204 (nothing deposited yet) so the poll runs until the caller's timeout.
func patDepositServer(t *testing.T, plaintextKey string) *httptest.Server {
	t.Helper()
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		if r.Method != http.MethodGet || !strings.HasSuffix(r.URL.Path, "/key") {
			w.WriteHeader(http.StatusNotFound)
			return
		}
		if plaintextKey == "" {
			w.WriteHeader(http.StatusNoContent)
			return
		}
		w.Header().Set("Content-Type", "application/json")
		_ = json.NewEncoder(w).Encode(map[string]string{"plaintext_key": plaintextKey})
	}))
	t.Cleanup(srv.Close)
	return srv
}

// depositJSON is the {username, token} payload the control-plane UI deposits.
func depositJSON(t *testing.T, username, token string) string {
	t.Helper()
	b, err := json.Marshal(patDeposit{Username: username, Token: token})
	if err != nil {
		t.Fatalf("marshal deposit: %v", err)
	}
	return string(b)
}

// ─── patPageURL / buildPATLoginURL ───────────────────────────────────────

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

func TestBuildPATLoginURL(t *testing.T) {
	// The nonce must ride as a query param BEFORE the fragment, or the SPA's hash
	// routing swallows it and the deposit can't find the session.
	got := buildPATLoginURL("https://cp.test/", "abc123")
	want := "https://cp.test/v2/home?cli_session=abc123#personal-access-tokens"
	if got != want {
		t.Errorf("buildPATLoginURL = %q, want %q", got, want)
	}
	if strings.Index(got, "?cli_session=") > strings.Index(got, "#") {
		t.Errorf("nonce query must precede the fragment: %q", got)
	}
}

func TestDecodePATDeposit(t *testing.T) {
	tests := []struct {
		name, raw, wantUser, wantTok string
		wantErr                      bool
	}{
		{name: "valid", raw: `{"username":"u@corp","token":"pat"}`, wantUser: "u@corp", wantTok: "pat"},
		{name: "not json", raw: "just-a-token", wantErr: true},
		{name: "missing token", raw: `{"username":"u@corp"}`, wantErr: true},
		{name: "missing username", raw: `{"token":"pat"}`, wantErr: true},
	}
	for _, tc := range tests {
		t.Run(tc.name, func(t *testing.T) {
			user, tok, err := decodePATDeposit(tc.raw)
			if tc.wantErr {
				if err == nil {
					t.Fatalf("want error for %q, got user=%q tok=%q", tc.raw, user, tok)
				}
				return
			}
			if err != nil || user != tc.wantUser || tok != tc.wantTok {
				t.Errorf("decodePATDeposit(%q) = %q,%q,%v; want %q,%q,nil", tc.raw, user, tok, err, tc.wantUser, tc.wantTok)
			}
		})
	}
}

// ─── the skip gates ──────────────────────────────────────────────────────

func TestTryInteractivePAT_Skips(t *testing.T) {
	tests := []struct {
		name    string
		asJSON  bool
		tty     bool
		baseURL string
	}{
		{name: "json output is machine-invoked", asJSON: true, tty: true, baseURL: "https://cp.test"},
		{name: "no tty to interact on", tty: false, baseURL: "https://cp.test"},
		{name: "plaintext non-loopback url", tty: true, baseURL: "http://cp.test"},
	}
	for _, tc := range tests {
		t.Run(tc.name, func(t *testing.T) {
			isolateHome(t)
			resetLoginFlags(t)
			stubTTY(t, tc.tty)
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

// ─── fall-through paths ──────────────────────────────────────────────────

func TestTryInteractivePAT_TimeoutFallsThrough(t *testing.T) {
	// Nothing deposited before the timeout is the documented "no token created"
	// case — it must not fail login, it hands off to the Praxis API-key flow.
	isolateHome(t)
	resetLoginFlags(t)
	stubTTY(t, true)
	stubOpenBrowser(t)
	readStderr := captureStderr(t)
	stubAuthMe(t, func(string, map[string]string) (*authMeResponse, error) {
		t.Fatal("verified a PAT despite nothing being deposited")
		return nil, nil
	})
	srv := patDepositServer(t, "")
	loginTimeout = 60 * time.Millisecond

	handled, err := tryInteractivePAT(io.Discard, false, "default", srv.URL, false)
	stderr := readStderr()
	if handled || err != nil {
		t.Fatalf("handled=%v err=%v, want false/nil so the api-key flow runs", handled, err)
	}
	if !strings.Contains(stderr, "No token was created") {
		t.Errorf("stderr = %q, want it to mention the timeout", stderr)
	}
	if prof := mustLoadProfile(t, "default"); prof.Token != "" {
		t.Errorf("persisted a profile on timeout: %+v", prof)
	}
}

func TestTryInteractivePAT_EnterSkips(t *testing.T) {
	// Pressing Enter hands off to the API-key flow immediately — the escape the
	// paste flow had. Without it a user waits out the full timeout, twice.
	isolateHome(t)
	resetLoginFlags(t)
	stubTTY(t, true)
	stubOpenBrowser(t)
	readStderr := captureStderr(t)
	stubReadLine(t, "")
	stubAuthMe(t, func(string, map[string]string) (*authMeResponse, error) {
		t.Fatal("verified a PAT despite the user skipping")
		return nil, nil
	})
	srv := patDepositServer(t, "")
	loginTimeout = 10 * time.Second // must NOT be waited out

	start := time.Now()
	handled, err := tryInteractivePAT(io.Discard, false, "default", srv.URL, false)
	stderr := readStderr()
	if handled || err != nil {
		t.Fatalf("handled=%v err=%v, want false/nil", handled, err)
	}
	if elapsed := time.Since(start); elapsed > 5*time.Second {
		t.Errorf("skip took %s — Enter did not cut the wait short", elapsed)
	}
	if !strings.Contains(stderr, "Skipping the control-plane token") {
		t.Errorf("stderr = %q, want the skip notice", stderr)
	}
}

func TestTryInteractivePAT_BadPayloadFallsThrough(t *testing.T) {
	// A deposited value that isn't {username, token} JSON reads as "no usable
	// PAT" and hands off, rather than failing the login.
	isolateHome(t)
	resetLoginFlags(t)
	stubTTY(t, true)
	stubOpenBrowser(t)
	readStderr := captureStderr(t)
	stubAuthMe(t, func(string, map[string]string) (*authMeResponse, error) {
		t.Fatal("verified an undecodable payload")
		return nil, nil
	})
	srv := patDepositServer(t, "not-json-token")

	handled, err := tryInteractivePAT(io.Discard, false, "default", srv.URL, false)
	stderr := readStderr()
	if handled || err != nil {
		t.Fatalf("handled=%v err=%v, want false/nil", handled, err)
	}
	if !strings.Contains(stderr, "unexpected token payload") {
		t.Errorf("stderr = %q, want it to mention the bad payload", stderr)
	}
}

// ─── the happy path ──────────────────────────────────────────────────────

func TestTryInteractivePAT_PersistsDepositedPAT(t *testing.T) {
	isolateHome(t)
	resetLoginFlags(t)
	stubPostAuth(t)
	stubTTY(t, true)
	opened := stubOpenBrowser(t)
	restoreStderr := captureStderr(t)

	var gotAuth map[string]string
	stubAuthMe(t, func(_ string, auth map[string]string) (*authMeResponse, error) {
		gotAuth = auth
		return &authMeResponse{Email: "u@corp"}, nil
	})
	srv := patDepositServer(t, depositJSON(t, "u@corp", "pat-deposited"))

	handled, err := tryInteractivePAT(io.Discard, false, "default", srv.URL, false)
	restoreStderr()
	if !handled || err != nil {
		t.Fatalf("handled=%v err=%v, want true/nil", handled, err)
	}
	// The browser must open the PAT page carrying a cli_session nonce.
	if !strings.HasPrefix(*opened, srv.URL+"/v2/home?cli_session=") ||
		!strings.HasSuffix(*opened, "#personal-access-tokens") {
		t.Errorf("opened %q, want the PAT page with a cli_session nonce", *opened)
	}
	// A control-plane PAT is only valid alongside the identity header.
	if gotAuth["Authorization"] != "Bearer pat-deposited" || gotAuth["X-Facets-Username"] != "u@corp" {
		t.Errorf("auth headers = %v, want Bearer pat-deposited + X-Facets-Username u@corp", gotAuth)
	}
	prof := mustLoadProfile(t, "default")
	if prof.Token != "pat-deposited" || prof.Username != "u@corp" || prof.AuthMode != credentials.AuthModeBasic {
		t.Errorf("persisted profile = %+v, want the deposited PAT in %q mode", prof, credentials.AuthModeBasic)
	}
}

func TestTryInteractivePAT_RejectedPATFallsThrough(t *testing.T) {
	// The API key is the final fallback, so a PAT the server won't take keeps the
	// chain walking instead of failing the login — matching tryFacetsPAT.
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
			stubTTY(t, true)
			stubOpenBrowser(t)
			readStderr := captureStderr(t)
			stubAuthMe(t, func(string, map[string]string) (*authMeResponse, error) {
				return nil, tc.authErr
			})
			srv := patDepositServer(t, depositJSON(t, "u@corp", "bad-pat"))

			handled, err := tryInteractivePAT(io.Discard, false, "default", srv.URL, false)
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
// reports, so a test can place it in the chain without driving the browser flow.
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
	// Tier 2 sits between raptor's stored PAT and the Praxis API key: login must
	// try a control-plane PAT before minting an API key, and only reach the
	// API-key browser when the PAT step declines.
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
	// Tier 1 still wins: a PAT already on the machine means no browser at all.
	isolateHome(t)
	resetLoginFlags(t)
	clearFacetsEnv(t)
	seedRaptorCreds(t, "[default]\ncontrol_plane_url = https://cp.test\nusername = u@corp\ntoken = pat-from-raptor\n")
	stubPostAuth(t)
	browsed := stubBrowserLogin(t)
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

// ─── no server probe decides the PAT prompt ──────────────────────────────

func TestInteractivePATEligible_DoesNotProbeServer(t *testing.T) {
	// Every deployment validates control-plane PATs, so eligibility is decided
	// by the local gates alone. A probe here would make an unreachable or
	// slow server silently demote login to the API-key browser flow.
	srv := httptest.NewServer(http.HandlerFunc(func(_ http.ResponseWriter, r *http.Request) {
		t.Errorf("login probed %s before offering the PAT prompt", r.URL.Path)
	}))
	defer srv.Close()
	origTTY := stdinIsTTY
	stdinIsTTY = func() bool { return true }
	t.Cleanup(func() { stdinIsTTY = origTTY })

	if !interactivePATEligible(srv.URL, false) {
		t.Error("eligible = false on a tty with a loopback url, want true")
	}
}

func TestRunLoginDryRun_ReportsPATPrompt(t *testing.T) {
	// --dry-run exists to predict login. Once the PAT browser sits in front of
	// the api-key browser, a report that still says only "browser" is wrong.
	tests := []struct {
		name   string
		asJSON bool
		tty    bool
		want   string
	}{
		{name: "human at a tty", tty: true, want: "control-plane PAT (browser), else browser"},
		{name: "no tty means login would not open a PAT browser either", tty: false, want: "browser"},
		// JSON output means an AI host is calling, and login skips the PAT browser
		// there too — so the report must keep saying browser.
		{name: "json output", asJSON: true, tty: true, want: "browser"},
	}
	for _, tc := range tests {
		t.Run(tc.name, func(t *testing.T) {
			isolateHome(t)
			resetLoginFlags(t)
			t.Cleanup(func() { loginDryRun = false })
			stubPostAuth(t)
			stubTTY(t, tc.tty)
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
