package cmd

import (
	"bytes"
	"io"
	"testing"
	"time"

	"github.com/Facets-cloud/praxis-cli/internal/credentials"
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
	postAuthSetup = func(out io.Writer, asJSON bool, baseURL, token string) postAuthState {
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

func TestResolveLoginURL_FlagWins(t *testing.T) {
	isolateHome(t)
	seedProfile(t, "default", "https://stored.test", "tok")
	got, err := resolveLoginURL("default", "https://flag.test")
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
	if got != "https://flag.test" {
		t.Errorf("got %q, want explicit flag URL", got)
	}
}

func TestResolveLoginURL_ExistingProfileReusesStoredURL(t *testing.T) {
	isolateHome(t)
	seedProfile(t, "acme", "https://acme.test", "tok")
	got, err := resolveLoginURL("acme", "")
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
	if got != "https://acme.test" {
		t.Errorf("got %q, want stored profile URL", got)
	}
}

func TestResolveLoginURL_DefaultFallsBackToBuiltin(t *testing.T) {
	isolateHome(t) // no profiles seeded
	got, err := resolveLoginURL("default", "")
	if err != nil {
		t.Fatalf("default profile must not require --url: %v", err)
	}
	if got != credentials.DefaultURL {
		t.Errorf("got %q, want built-in %q", got, credentials.DefaultURL)
	}
}

func TestResolveLoginURL_NewNamedProfileWithoutURLErrors(t *testing.T) {
	isolateHome(t) // "acme" does not exist
	_, err := resolveLoginURL("acme", "")
	if err == nil {
		t.Fatal("expected error for new named profile without --url, got nil")
	}
}

// ─── tryReuseStoredToken ─────────────────────────────────────────────────

func TestTryReuse_NoStoredToken(t *testing.T) {
	isolateHome(t)
	post := stubPostAuth(t)
	reused, err := tryReuseStoredToken(io.Discard, true, "default", credentials.DefaultURL)
	if reused || err != nil {
		t.Fatalf("got (reused=%v, err=%v), want (false, nil)", reused, err)
	}
	if *post {
		t.Error("post-auth setup ran without a stored token")
	}
}

func TestTryReuse_URLMismatchSkips(t *testing.T) {
	isolateHome(t)
	seedProfile(t, "default", "https://stored.test", "tok")
	post := stubPostAuth(t)
	stubAuthMe(t, func(_, _ string) (*authMeResponse, error) {
		t.Fatal("fetchAuthMe must not be called on URL mismatch")
		return nil, nil
	})
	reused, err := tryReuseStoredToken(io.Discard, true, "default", "https://other.test")
	if reused || err != nil {
		t.Fatalf("got (reused=%v, err=%v), want (false, nil)", reused, err)
	}
	if *post {
		t.Error("post-auth setup ran on URL mismatch")
	}
}

func TestTryReuse_ValidTokenReuses(t *testing.T) {
	isolateHome(t)
	seedProfile(t, "default", "https://stored.test", "tok")
	post := stubPostAuth(t)
	stubAuthMe(t, func(baseURL, token string) (*authMeResponse, error) {
		if token != "tok" || baseURL != "https://stored.test" {
			t.Errorf("fetchAuthMe called with (%q,%q)", baseURL, token)
		}
		return &authMeResponse{Email: "u@x"}, nil
	})
	reused, err := tryReuseStoredToken(io.Discard, true, "default", "https://stored.test")
	if !reused || err != nil {
		t.Fatalf("got (reused=%v, err=%v), want (true, nil)", reused, err)
	}
	if !*post {
		t.Error("post-auth setup did not run for a valid reused token")
	}
	// Active pointer must now be the reused profile.
	active, _ := credentials.ResolveActive("")
	if active.Name != "default" || !active.Loaded {
		t.Errorf("active profile = %+v, want default loaded", active)
	}
}

func TestTryReuse_InvalidTokenFallsBack(t *testing.T) {
	isolateHome(t)
	seedProfile(t, "default", "https://stored.test", "expired")
	post := stubPostAuth(t)
	stubAuthMe(t, func(_, _ string) (*authMeResponse, error) {
		return nil, io.ErrUnexpectedEOF // any verification failure
	})
	reused, err := tryReuseStoredToken(io.Discard, true, "default", "https://stored.test")
	if reused || err != nil {
		t.Fatalf("got (reused=%v, err=%v), want (false, nil) graceful fallback", reused, err)
	}
	if *post {
		t.Error("post-auth setup ran despite failed verification")
	}
}

// ─── RunE precedence ─────────────────────────────────────────────────────

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
	if *browser {
		t.Error("browser flow ran despite a usage error")
	}
}
