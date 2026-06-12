package cmd

import (
	"bytes"
	"context"
	"net/http"
	"net/http/httptest"
	"strings"
	"sync/atomic"
	"testing"
	"time"

	"github.com/Facets-cloud/praxis-cli/internal/credentials"
	"github.com/Facets-cloud/praxis-cli/internal/render"
)

func TestBuildLoginURL_Valid(t *testing.T) {
	got, err := buildLoginURL("https://app.example.com", "nonce-abc", "praxis-cli-abc12")
	if err != nil {
		t.Fatalf("buildLoginURL err = %v", err)
	}
	for _, want := range []string{
		"https://app.example.com/ui/ai/settings/api-keys",
		"cli_session=nonce-abc",
		"suggested_name=praxis-cli-abc12",
	} {
		if !strings.Contains(got, want) {
			t.Errorf("buildLoginURL output missing %q\nfull: %s", want, got)
		}
	}
	// cli_callback must NOT be present — the server-mediated handshake
	// supersedes the legacy localhost listener and we don't want the
	// modal trying both paths.
	if strings.Contains(got, "cli_callback") {
		t.Errorf("buildLoginURL output unexpectedly contains cli_callback: %s", got)
	}
}

func TestBuildLoginURL_Malformed(t *testing.T) {
	_, err := buildLoginURL("http://[::1", "nonce", "praxis-cli-x")
	if err == nil {
		t.Fatal("expected error for malformed URL, got nil")
	}
	if !strings.Contains(err.Error(), "invalid login URL") {
		t.Errorf("error message missing context: %v", err)
	}
}

func TestSuggestedKeyName_FormatAndUniqueness(t *testing.T) {
	first := suggestedKeyName()

	// Format: "praxis-cli-" followed by 5 hex chars.
	if !strings.HasPrefix(first, "praxis-cli-") {
		t.Errorf("expected prefix 'praxis-cli-', got %q", first)
	}
	suffix := strings.TrimPrefix(first, "praxis-cli-")
	if len(suffix) != 5 {
		t.Errorf("expected 5-char suffix, got %d chars in %q", len(suffix), suffix)
	}
	// Modal accepts only [a-z0-9_-]+
	for _, r := range suffix {
		if !((r >= 'a' && r <= 'f') || (r >= '0' && r <= '9')) {
			t.Errorf("suffix has non-hex char %q in %q", r, suffix)
		}
	}

	// A handful of regenerations should not collide. 20 bits of
	// randomness → birthday-paradox collision odds ~1 in 1024 across
	// 16 draws; we only check 5, so a real collision means rand.Read
	// is broken (e.g. always returns zeros).
	seen := map[string]bool{first: true}
	for i := 0; i < 5; i++ {
		name := suggestedKeyName()
		if seen[name] {
			t.Errorf("collision after %d draws: %q already seen", i+1, name)
		}
		seen[name] = true
	}
}

// ---- pollSessionKey ----------------------------------------------------

// fastPoll is short enough that an entire test completes in well under
// a second yet long enough that a "successful" run still exercises the
// retry path at least once before returning.
const fastPoll = 20 * time.Millisecond

func TestPollSessionKey_ReturnsKeyOn200(t *testing.T) {
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		w.Header().Set("Content-Type", "application/json")
		w.WriteHeader(http.StatusOK)
		_, _ = w.Write([]byte(`{"plaintext_key":"sk_live_FAKE_TEST_KEY"}`))
	}))
	defer srv.Close()

	ctx, cancel := context.WithTimeout(context.Background(), 500*time.Millisecond)
	defer cancel()
	got, err := pollSessionKey(ctx, srv.URL, "a"+strings.Repeat("0", 47), fastPoll)
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
	if got != "sk_live_FAKE_TEST_KEY" {
		t.Errorf("got %q, want sk_live_FAKE_TEST_KEY", got)
	}
}

func TestPollSessionKey_KeepsPollingOn204ThenSucceeds(t *testing.T) {
	var calls atomic.Int32
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		n := calls.Add(1)
		if n < 3 {
			w.WriteHeader(http.StatusNoContent)
			return
		}
		w.WriteHeader(http.StatusOK)
		_, _ = w.Write([]byte(`{"plaintext_key":"sk_live_AFTER_RETRY"}`))
	}))
	defer srv.Close()

	ctx, cancel := context.WithTimeout(context.Background(), 1*time.Second)
	defer cancel()
	got, err := pollSessionKey(ctx, srv.URL, "n", fastPoll)
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
	if got != "sk_live_AFTER_RETRY" {
		t.Errorf("got %q, want sk_live_AFTER_RETRY", got)
	}
	if calls.Load() < 3 {
		t.Errorf("expected at least 3 polls before success, got %d", calls.Load())
	}
}

func TestPollSessionKey_KeepsPollingOn5xx(t *testing.T) {
	var calls atomic.Int32
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		n := calls.Add(1)
		if n == 1 {
			w.WriteHeader(http.StatusInternalServerError)
			return
		}
		w.WriteHeader(http.StatusOK)
		_, _ = w.Write([]byte(`{"plaintext_key":"k"}`))
	}))
	defer srv.Close()

	ctx, cancel := context.WithTimeout(context.Background(), 1*time.Second)
	defer cancel()
	got, err := pollSessionKey(ctx, srv.URL, "n", fastPoll)
	if err != nil {
		t.Fatalf("transient 5xx should not be fatal, got err = %v", err)
	}
	if got != "k" {
		t.Errorf("got %q, want k", got)
	}
}

func TestPollSessionKey_FatalOn400(t *testing.T) {
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		w.WriteHeader(http.StatusBadRequest)
	}))
	defer srv.Close()

	ctx, cancel := context.WithTimeout(context.Background(), 500*time.Millisecond)
	defer cancel()
	_, err := pollSessionKey(ctx, srv.URL, "bad", fastPoll)
	if err == nil {
		t.Fatal("expected fatal error on 400, got nil")
	}
	if !strings.Contains(err.Error(), "400") {
		t.Errorf("error should mention 400 status, got: %v", err)
	}
}

func TestPollSessionKey_SlowRequestIsTransientNotTimeout(t *testing.T) {
	// Regression: a single poll request that outlived the per-request
	// http.Client timeout used to be reported as context.DeadlineExceeded
	// (the client wraps its timeout error to match that sentinel since
	// Go 1.16), which the login flow renders as "login timed out" — long
	// before the overall 90s deadline. A stalled request must instead be
	// treated as transient and the loop must keep polling.
	prev := pollRequestTimeout
	pollRequestTimeout = 50 * time.Millisecond
	defer func() { pollRequestTimeout = prev }()

	var calls atomic.Int32
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		if calls.Add(1) == 1 {
			time.Sleep(250 * time.Millisecond) // outlive the client timeout
			return
		}
		w.WriteHeader(http.StatusOK)
		_, _ = w.Write([]byte(`{"plaintext_key":"sk_live_AFTER_STALL"}`))
	}))
	defer srv.Close()

	ctx, cancel := context.WithTimeout(context.Background(), 2*time.Second)
	defer cancel()
	got, err := pollSessionKey(ctx, srv.URL, "n", fastPoll)
	if err != nil {
		t.Fatalf("a single stalled request should be transient, got err = %v", err)
	}
	if got != "sk_live_AFTER_STALL" {
		t.Errorf("got %q, want sk_live_AFTER_STALL", got)
	}
	if calls.Load() < 2 {
		t.Errorf("expected a retry after the stalled request, got %d calls", calls.Load())
	}
}

func TestPollSessionKey_TimeoutReturnsDeadlineExceeded(t *testing.T) {
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		w.WriteHeader(http.StatusNoContent) // always pending
	}))
	defer srv.Close()

	ctx, cancel := context.WithTimeout(context.Background(), 80*time.Millisecond)
	defer cancel()
	_, err := pollSessionKey(ctx, srv.URL, "n", fastPoll)
	if err != context.DeadlineExceeded {
		t.Errorf("expected context.DeadlineExceeded, got: %v", err)
	}
}

func TestPollSessionKey_EmptyKeyIsError(t *testing.T) {
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		w.WriteHeader(http.StatusOK)
		_, _ = w.Write([]byte(`{"plaintext_key":""}`))
	}))
	defer srv.Close()

	ctx, cancel := context.WithTimeout(context.Background(), 200*time.Millisecond)
	defer cancel()
	_, err := pollSessionKey(ctx, srv.URL, "n", fastPoll)
	if err == nil {
		t.Fatal("expected error for empty plaintext_key, got nil")
	}
	if !strings.Contains(err.Error(), "empty") {
		t.Errorf("error should mention empty key, got: %v", err)
	}
}

func TestPollSessionKey_TrimsTrailingSlashFromBaseURL(t *testing.T) {
	var gotPath atomic.Value
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		gotPath.Store(r.URL.Path)
		w.WriteHeader(http.StatusOK)
		_, _ = w.Write([]byte(`{"plaintext_key":"k"}`))
	}))
	defer srv.Close()

	ctx, cancel := context.WithTimeout(context.Background(), 500*time.Millisecond)
	defer cancel()
	// Pass baseURL WITH a trailing slash — a user-provided --url quirk.
	// pollSessionKey must not produce //ai-api/... (double slash) on the
	// wire; that's been a real-world source of 404s in similar CLIs.
	_, err := pollSessionKey(ctx, srv.URL+"/", "n", fastPoll)
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
	path, _ := gotPath.Load().(string)
	if strings.HasPrefix(path, "//") {
		t.Errorf("path had double-slash from trailing-slash baseURL: %s", path)
	}
}

func TestLoginJSONEnvelopeIncludesAgentKeys(t *testing.T) {
	state := postAuthState{
		agents: []agentInstallationLite{
			{AgentName: "praxis-alpha", Kind: "agent", Harness: "claude-code", Path: "/x.md"},
		},
		removedAgents: []agentInstallationLite{
			{AgentName: "praxis-old", Kind: "agent", Harness: "claude-code", Path: "/old.md"},
		},
	}

	var buf bytes.Buffer
	if err := render.JSON(&buf, map[string]any{
		"ok":             true,
		"agents":         state.agents,
		"removed_agents": state.removedAgents,
	}); err != nil {
		t.Fatalf("render JSON: %v", err)
	}
	got := buf.String()
	if !strings.Contains(got, `"agents":`) {
		t.Errorf("envelope missing 'agents' key:\n%s", got)
	}
	if !strings.Contains(got, `"removed_agents":`) {
		t.Errorf("envelope missing 'removed_agents' key:\n%s", got)
	}
}

// Regression: `praxis login --url https://host/` used to keep the
// trailing slash, producing double slashes in every concatenated path
// (https://host//ui/ai/settings/api-keys, //v1/...).
func TestResolveLoginURL_TrimsTrailingSlash(t *testing.T) {
	t.Setenv("HOME", t.TempDir()) // isolate credentials store

	tests := []struct {
		name    string
		flagURL string
		want    string
	}{
		{"trailing slash", "https://root.console.facets.cloud/", "https://root.console.facets.cloud"},
		{"multiple trailing slashes", "https://root.console.facets.cloud///", "https://root.console.facets.cloud"},
		{"no trailing slash unchanged", "https://root.console.facets.cloud", "https://root.console.facets.cloud"},
		{"surrounding whitespace", "  https://root.console.facets.cloud/ ", "https://root.console.facets.cloud"},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			got, err := resolveLoginURL("default", tt.flagURL)
			if err != nil {
				t.Fatalf("resolveLoginURL err = %v", err)
			}
			if got != tt.want {
				t.Errorf("resolveLoginURL(%q) = %q, want %q", tt.flagURL, got, tt.want)
			}
		})
	}
}

func TestResolveLoginURL_TrimsStoredProfileURL(t *testing.T) {
	t.Setenv("HOME", t.TempDir())
	if err := credentials.Save(map[string]credentials.Profile{
		"acme": {URL: "https://acme.example.com/", Token: "tok"},
	}); err != nil {
		t.Fatalf("seed credentials: %v", err)
	}

	got, err := resolveLoginURL("acme", "")
	if err != nil {
		t.Fatalf("resolveLoginURL err = %v", err)
	}
	if want := "https://acme.example.com"; got != want {
		t.Errorf("resolveLoginURL stored URL = %q, want %q", got, want)
	}
}
