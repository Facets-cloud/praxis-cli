package cmd

import (
	"bytes"
	"context"
	"errors"
	"fmt"
	"io"
	"strings"
	"testing"
	"time"

	"github.com/Facets-cloud/praxis-cli/internal/credentials"
	"github.com/Facets-cloud/praxis-cli/internal/exitcode"
)

// ─── test helpers ────────────────────────────────────────────────────────

// isolateHome points ~/.praxis at a temp dir and clears the env profile
// override so credential reads/writes never touch the real home.
func isolateHome(t *testing.T) {
	t.Helper()
	t.Setenv("HOME", t.TempDir())
	t.Setenv("PRAXIS_PROFILE", "")
}

// seedProfile writes a profile into the isolated credentials store.
func seedProfile(t *testing.T, name, url, token string) {
	t.Helper()
	if err := credentials.Put(name, credentials.Profile{URL: url, Username: "u@x", Token: token}); err != nil {
		t.Fatalf("seed profile %q: %v", name, err)
	}
}

// stubAuthMe swaps fetchAuthMe and restores it at test end.
func stubAuthMe(t *testing.T, fn func(baseURL, token string) (*authMeResponse, error)) {
	t.Helper()
	orig := fetchAuthMe
	fetchAuthMe = fn
	t.Cleanup(func() { fetchAuthMe = orig })
}

// stubPostAuth swaps the post-auth setup seam with a recorder.
func stubPostAuth(t *testing.T) *bool {
	t.Helper()
	called := false
	orig := postAuthSetup
	postAuthSetup = func(out io.Writer, asJSON bool, baseURL, token string, projectScoped bool) postAuthState {
		// The login path is never project-scoped — only `refresh-skills
		// --project` is. Guard the compatibility contract so a future
		// signature change can't silently start passing true here.
		if projectScoped {
			t.Errorf("login should call postAuthSetup with projectScoped=false, got true")
		}
		called = true
		return postAuthState{}
	}
	t.Cleanup(func() { postAuthSetup = orig })
	return &called
}

// stubBrowserLogin swaps the browser-flow seam with a recorder.
func stubBrowserLogin(t *testing.T) *bool {
	t.Helper()
	called := false
	orig := browserLoginFn
	browserLoginFn = func(out io.Writer, asJSON bool, profileName, baseURL string, timeout time.Duration) error {
		called = true
		return nil
	}
	t.Cleanup(func() { browserLoginFn = orig })
	return &called
}

// stubOsExit swaps osExit with a recorder so a fatal-exit path can be
// asserted without terminating the test binary. The returned *int is -1
// until osExit is called, then holds the exit code it was called with.
func stubOsExit(t *testing.T) *int {
	t.Helper()
	code := -1
	orig := osExit
	osExit = func(c int) { code = c }
	t.Cleanup(func() { osExit = orig })
	return &code
}

func resetLoginFlags(t *testing.T) {
	t.Helper()
	loginProfile, loginURL, loginToken = "", "", ""
	loginForce, loginJSON = false, false
	loginTimeout = 90 * time.Second
	t.Cleanup(func() {
		loginProfile, loginURL, loginToken = "", "", ""
		loginForce, loginJSON = false, false
		loginTimeout = 90 * time.Second
	})
}

func runLoginRunE(t *testing.T) (string, error) {
	t.Helper()
	var buf bytes.Buffer
	loginCmd.SetOut(&buf)
	err := loginCmd.RunE(loginCmd, nil)
	return buf.String(), err
}

// ─── resolveLoginURL ─────────────────────────────────────────────────────

func TestResolveLoginURL(t *testing.T) {
	tests := []struct {
		name        string
		seedName    string // "" → seed nothing
		seedURL     string
		profile     string
		flagURL     string
		wantURL     string
		wantErr     bool
		errContains string
	}{
		{
			name:     "explicit --url wins over stored profile URL",
			seedName: "default", seedURL: "https://stored.test",
			profile: "default", flagURL: "https://flag.test",
			wantURL: "https://flag.test",
		},
		{
			name:     "existing profile reuses its stored URL",
			seedName: "acme", seedURL: "https://acme.test",
			profile: "acme", flagURL: "",
			wantURL: "https://acme.test",
		},
		{
			name:    "default profile falls back to the built-in URL",
			profile: "default", flagURL: "",
			wantURL: credentials.DefaultURL,
		},
		{
			name:    "new named profile without --url errors",
			profile: "acme", flagURL: "",
			wantErr: true, errContains: "--url",
		},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			isolateHome(t)
			if tt.seedName != "" {
				seedProfile(t, tt.seedName, tt.seedURL, "tok")
			}
			got, err := resolveLoginURL(tt.profile, tt.flagURL)
			if tt.wantErr {
				if err == nil {
					t.Fatalf("expected error containing %q, got nil", tt.errContains)
				}
				if !strings.Contains(err.Error(), tt.errContains) {
					t.Errorf("error = %v, want substring %q", err, tt.errContains)
				}
				return
			}
			if err != nil {
				t.Fatalf("unexpected error: %v", err)
			}
			if got != tt.wantURL {
				t.Errorf("got %q, want %q", got, tt.wantURL)
			}
		})
	}
}

// ─── tryReuseStoredToken ─────────────────────────────────────────────────

func TestTryReuseStoredToken(t *testing.T) {
	tests := []struct {
		name             string
		seed             bool
		storedURL        string
		storedToken      string
		targetURL        string
		authMeErr        error
		wantReused       bool
		wantErr          bool // tryReuseStoredToken returns a non-nil error
		wantExit         int  // exit code osExit must be called with (0 → not called)
		wantTokenIntact  bool // stored token must survive untouched
		wantPostAuth     bool
		wantAuthMeCalled bool
		wantActive       string // "" → don't assert active pointer
	}{
		{
			name:      "no stored token → no reuse, browser fallback",
			seed:      false,
			targetURL: credentials.DefaultURL,
		},
		{
			name:      "URL re-target skips reuse without verifying",
			seed:      true,
			storedURL: "https://stored.test", storedToken: "tok",
			targetURL: "https://other.test",
		},
		{
			name:      "valid token reuses, persists, sets active",
			seed:      true,
			storedURL: "https://stored.test", storedToken: "tok",
			targetURL:        "https://stored.test",
			wantReused:       true,
			wantPostAuth:     true,
			wantAuthMeCalled: true,
			wantActive:       "default",
		},
		{
			name:      "rejected token (401/403) falls back to browser gracefully",
			seed:      true,
			storedURL: "https://stored.test", storedToken: "expired",
			targetURL:        "https://stored.test",
			authMeErr:        fmt.Errorf("%w (HTTP 401)", errTokenRejected),
			wantReused:       false, // browser fallback, not owned
			wantAuthMeCalled: true,
		},
		{
			name:      "transient error aborts without browser and keeps the token",
			seed:      true,
			storedURL: "https://stored.test", storedToken: "still-good",
			targetURL:        "https://stored.test",
			authMeErr:        context.DeadlineExceeded,
			wantReused:       true, // owned: caller must NOT open the browser
			wantErr:          true,
			wantExit:         exitcode.Network,
			wantTokenIntact:  true,
			wantAuthMeCalled: true,
		},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			isolateHome(t)
			if tt.seed {
				seedProfile(t, "default", tt.storedURL, tt.storedToken)
			}
			post := stubPostAuth(t)
			exitCode := stubOsExit(t)
			authMeCalled := false
			stubAuthMe(t, func(baseURL, token string) (*authMeResponse, error) {
				authMeCalled = true
				if baseURL != tt.targetURL || token != tt.storedToken {
					t.Errorf("fetchAuthMe(%q,%q), want (%q,%q)", baseURL, token, tt.targetURL, tt.storedToken)
				}
				if tt.authMeErr != nil {
					return nil, tt.authMeErr
				}
				return &authMeResponse{Email: "u@x"}, nil
			})

			reused, err := tryReuseStoredToken(io.Discard, true, "default", tt.targetURL)
			if tt.wantErr {
				// The transient failure must surface the underlying cause
				// verbatim, not a generic wrapper — that's what lets the
				// caller distinguish it from errTokenRejected.
				if !errors.Is(err, tt.authMeErr) {
					t.Fatalf("err = %v, want it to wrap %v", err, tt.authMeErr)
				}
			} else if err != nil {
				t.Fatalf("tryReuseStoredToken returned err = %v, want nil (fallback is non-fatal)", err)
			}
			if reused != tt.wantReused {
				t.Errorf("reused = %v, want %v", reused, tt.wantReused)
			}
			if tt.wantExit != 0 {
				if *exitCode != tt.wantExit {
					t.Errorf("osExit code = %d, want %d", *exitCode, tt.wantExit)
				}
			} else if *exitCode != -1 {
				t.Errorf("osExit called with %d, want never called", *exitCode)
			}
			if tt.wantTokenIntact {
				store, lerr := credentials.Load()
				if lerr != nil {
					t.Fatalf("credentials.Load() after reuse: %v", lerr)
				}
				if got := store["default"].Token; got != tt.storedToken {
					t.Errorf("stored token = %q, want it left intact as %q", got, tt.storedToken)
				}
			}
			if *post != tt.wantPostAuth {
				t.Errorf("post-auth setup ran = %v, want %v", *post, tt.wantPostAuth)
			}
			if authMeCalled != tt.wantAuthMeCalled {
				t.Errorf("fetchAuthMe called = %v, want %v", authMeCalled, tt.wantAuthMeCalled)
			}
			if tt.wantActive != "" {
				active, _ := credentials.ResolveActive("")
				if active.Name != tt.wantActive || !active.Loaded {
					t.Errorf("active profile = %+v, want %q loaded", active, tt.wantActive)
				}
			}
		})
	}
}

// ─── RunE precedence ─────────────────────────────────────────────────────
//
// These stay as separate functions rather than a table: each exercises a
// distinct branch of the precedence chain with its own seam stubbing and
// assertions (browser called vs. not, error returned), so a shared table
// would obscure more than it consolidates.

func TestLoginRunE_ValidStoredTokenSkipsBrowser(t *testing.T) {
	isolateHome(t)
	resetLoginFlags(t)
	seedProfile(t, "default", "https://stored.test", "tok")
	browser := stubBrowserLogin(t)
	stubPostAuth(t)
	stubAuthMe(t, func(_, _ string) (*authMeResponse, error) {
		return &authMeResponse{Email: "u@x"}, nil
	})
	if _, err := runLoginRunE(t); err != nil {
		t.Fatalf("login err: %v", err)
	}
	if *browser {
		t.Error("browser flow ran even though a valid token was stored")
	}
}

func TestLoginRunE_TransientErrorDoesNotOpenBrowser(t *testing.T) {
	isolateHome(t)
	resetLoginFlags(t)
	seedProfile(t, "default", "https://stored.test", "still-good")
	browser := stubBrowserLogin(t)
	stubPostAuth(t)
	exitCode := stubOsExit(t) // production exits here; stub keeps the test alive
	stubAuthMe(t, func(_, _ string) (*authMeResponse, error) {
		return nil, context.DeadlineExceeded // server unreachable, not a rejection
	})
	_, err := runLoginRunE(t)
	if !errors.Is(err, context.DeadlineExceeded) {
		t.Fatalf("err = %v, want it to wrap context.DeadlineExceeded", err)
	}
	if *exitCode != exitcode.Network {
		t.Errorf("osExit code = %d, want %d (Network)", *exitCode, exitcode.Network)
	}
	if *browser {
		t.Error("browser flow ran on a transient failure — a flaky network must not force re-login")
	}
	// The possibly-valid token must survive the failed verification attempt.
	store, lerr := credentials.Load()
	if lerr != nil {
		t.Fatalf("credentials.Load(): %v", lerr)
	}
	if got := store["default"].Token; got != "still-good" {
		t.Errorf("stored token = %q, want it left intact", got)
	}
}

func TestLoginRunE_RejectedTokenOpensBrowser(t *testing.T) {
	isolateHome(t)
	resetLoginFlags(t)
	seedProfile(t, "default", "https://stored.test", "expired")
	browser := stubBrowserLogin(t)
	stubPostAuth(t)
	stubAuthMe(t, func(_, _ string) (*authMeResponse, error) {
		return nil, fmt.Errorf("%w (HTTP 401)", errTokenRejected)
	})
	if _, err := runLoginRunE(t); err != nil {
		t.Fatalf("login err: %v", err)
	}
	if !*browser {
		t.Error("a server-rejected token should fall back to the browser")
	}
}

func TestLoginRunE_ForceOpensBrowser(t *testing.T) {
	isolateHome(t)
	resetLoginFlags(t)
	seedProfile(t, "default", "https://stored.test", "tok")
	loginForce = true
	browser := stubBrowserLogin(t)
	stubPostAuth(t)
	stubAuthMe(t, func(_, _ string) (*authMeResponse, error) {
		t.Fatal("--force must not verify/reuse the stored token")
		return nil, nil
	})
	if _, err := runLoginRunE(t); err != nil {
		t.Fatalf("login err: %v", err)
	}
	if !*browser {
		t.Error("--force did not open the browser")
	}
}

func TestLoginRunE_NoStoredTokenOpensBrowser(t *testing.T) {
	isolateHome(t)
	resetLoginFlags(t)
	browser := stubBrowserLogin(t)
	stubPostAuth(t)
	if _, err := runLoginRunE(t); err != nil {
		t.Fatalf("login err: %v", err)
	}
	if !*browser {
		t.Error("browser flow did not run when no token was stored")
	}
}

func TestLoginRunE_URLRetargetOpensBrowser(t *testing.T) {
	isolateHome(t)
	resetLoginFlags(t)
	seedProfile(t, "default", "https://stored.test", "tok")
	loginURL = "https://other.test" // re-target away from the stored URL
	browser := stubBrowserLogin(t)
	stubPostAuth(t)
	stubAuthMe(t, func(_, _ string) (*authMeResponse, error) {
		t.Fatal("token must not be reused when --url re-targets the profile")
		return nil, nil
	})
	if _, err := runLoginRunE(t); err != nil {
		t.Fatalf("login err: %v", err)
	}
	if !*browser {
		t.Error("re-targeting --url did not fall back to the browser")
	}
}

func TestLoginRunE_NewNamedProfileWithoutURLErrors(t *testing.T) {
	isolateHome(t)
	resetLoginFlags(t)
	loginProfile = "acme" // does not exist, no --url
	browser := stubBrowserLogin(t)
	stubPostAuth(t)
	_, err := runLoginRunE(t)
	if err == nil {
		t.Fatal("expected error for new named profile without --url")
	}
	if !strings.Contains(err.Error(), "--url") {
		t.Errorf("error = %v, want message containing '--url'", err)
	}
	if *browser {
		t.Error("browser flow ran despite a usage error")
	}
}
