package cmd

import (
	"errors"
	"io"
	"net/http"
	"net/http/httptest"
	"testing"

	"github.com/Facets-cloud/praxis-cli/internal/credentials"
)

// ─── issue #19-A: canonicalize the host at login ─────────────────────────
//
// The apex https://askpraxis.ai 301-redirects to www. fetchAuthMe's GET
// follows the redirect, so login *works* against the apex — but if the
// CLI then stores the apex URL, every later MCP invoke pays (and used to
// fail on) that redirect. Login must store the scheme://host the
// /auth/me call actually landed on.

// canonicalPair spins up a "final" server serving /ai-api/auth/me and a
// "stale" server that 301-redirects everything to it, mimicking the
// apex → www hop.
func canonicalPair(t *testing.T) (stale, final *httptest.Server) {
	t.Helper()
	final = httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		if r.URL.Path != "/ai-api/auth/me" {
			http.NotFound(w, r)
			return
		}
		w.Header().Set("Content-Type", "application/json")
		_, _ = w.Write([]byte(`{"user_id":"u1","email":"qa@facets.cloud"}`))
	}))
	t.Cleanup(final.Close)
	stale = httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		http.Redirect(w, r, final.URL+r.URL.Path, http.StatusMovedPermanently)
	}))
	t.Cleanup(stale.Close)
	return stale, final
}

func TestFetchAuthMe_ReportsCanonicalHostAfterRedirect(t *testing.T) {
	stale, final := canonicalPair(t)

	me, err := fetchAuthMe(stale.URL, "sk_test_T")
	if err != nil {
		t.Fatalf("fetchAuthMe: %v", err)
	}
	if me.Email != "qa@facets.cloud" {
		t.Errorf("email = %q, want qa@facets.cloud", me.Email)
	}
	if me.canonicalBaseURL != final.URL {
		t.Errorf("canonicalBaseURL = %q, want %q (the post-redirect host)", me.canonicalBaseURL, final.URL)
	}
}

func TestFetchAuthMe_NoRedirectKeepsBaseURL(t *testing.T) {
	_, final := canonicalPair(t)

	me, err := fetchAuthMe(final.URL, "sk_test_T")
	if err != nil {
		t.Fatalf("fetchAuthMe: %v", err)
	}
	if me.canonicalBaseURL != final.URL {
		t.Errorf("canonicalBaseURL = %q, want %q (unchanged when no redirect)", me.canonicalBaseURL, final.URL)
	}
}

// ─── error classification: rejected vs. transient ───────────────────────
//
// fetchAuthMe must mark a 401/403 with the errTokenRejected sentinel (the
// server reached a verdict — fall back to the browser) while leaving any
// other non-200 (5xx, etc.) unwrapped so callers read it as transient and
// keep the stored token. This is the crux of not mislabeling a flaky
// server as an expired token.

func TestFetchAuthMe_ClassifiesStatus(t *testing.T) {
	tests := []struct {
		name         string
		status       int
		wantRejected bool // errors.Is(err, errTokenRejected)
	}{
		{"401 is a token rejection", http.StatusUnauthorized, true},
		{"403 is a token rejection", http.StatusForbidden, true},
		{"500 is transient, not a rejection", http.StatusInternalServerError, false},
		{"503 is transient, not a rejection", http.StatusServiceUnavailable, false},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
				http.Error(w, "nope", tt.status)
			}))
			t.Cleanup(srv.Close)

			_, err := fetchAuthMe(srv.URL, "sk_test_T")
			if err == nil {
				t.Fatalf("fetchAuthMe(HTTP %d) returned nil error", tt.status)
			}
			if got := errors.Is(err, errTokenRejected); got != tt.wantRejected {
				t.Errorf("errors.Is(err, errTokenRejected) = %v, want %v (err: %v)", got, tt.wantRejected, err)
			}
		})
	}
}

// End to end through the --token login path: the profile must be stored
// with the canonical (post-redirect) URL, not the stale one the user
// passed.
func TestSaveAndVerifyToken_StoresCanonicalURL(t *testing.T) {
	isolateHome(t)
	stubPostAuth(t)
	stale, final := canonicalPair(t)

	if err := saveAndVerifyToken(io.Discard, true, "default", stale.URL, "sk_test_T"); err != nil {
		t.Fatalf("saveAndVerifyToken: %v", err)
	}

	store, err := credentials.Load()
	if err != nil {
		t.Fatalf("load credentials: %v", err)
	}
	got := store["default"].URL
	if got != final.URL {
		t.Errorf("stored URL = %q, want canonical %q", got, final.URL)
	}
}

// Token reuse against a stored stale URL must self-heal the stored URL
// to the canonical host on the next login.
func TestTryReuseStoredToken_SelfHealsStaleURL(t *testing.T) {
	isolateHome(t)
	stubPostAuth(t)
	stale, final := canonicalPair(t)
	seedProfile(t, "default", stale.URL, "sk_test_T")

	reused, err := tryReuseStoredToken(io.Discard, true, "default", stale.URL)
	if err != nil {
		t.Fatalf("tryReuseStoredToken: %v", err)
	}
	if !reused {
		t.Fatal("expected the stored token to be reused")
	}

	store, err := credentials.Load()
	if err != nil {
		t.Fatalf("load credentials: %v", err)
	}
	if got := store["default"].URL; got != final.URL {
		t.Errorf("stored URL after reuse = %q, want self-healed canonical %q", got, final.URL)
	}
}
