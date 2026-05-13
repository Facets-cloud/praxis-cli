package cmd

import (
	"context"
	"net/http"
	"net/http/httptest"
	"strings"
	"sync/atomic"
	"testing"
	"time"
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
	// Lower bound is intentionally loose. The test's goal is to prove
	// the retry path runs at all; the exact count is timing-sensitive
	// and would flake under a starved CI scheduler.
	if calls.Load() < 2 {
		t.Errorf("expected at least 2 polls before success, got %d", calls.Load())
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

func TestPollSessionKey_FatalOn401(t *testing.T) {
	// 401 / 403 / 410 must short-circuit instead of looping until the
	// 90s caller timeout. A non-400/404 4xx means "the server understood
	// and is telling you no" — retry will never help.
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		w.WriteHeader(http.StatusUnauthorized)
	}))
	defer srv.Close()

	ctx, cancel := context.WithTimeout(context.Background(), 500*time.Millisecond)
	defer cancel()
	_, err := pollSessionKey(ctx, srv.URL, "n", fastPoll)
	if err == nil {
		t.Fatal("expected fatal error on 401, got nil")
	}
	if !strings.Contains(err.Error(), "401") {
		t.Errorf("error should mention 401 status, got: %v", err)
	}
}

func TestPollSessionOnce_ClientTimeoutIsTransient(t *testing.T) {
	// Regression: when http.Client.Timeout fires before the caller's
	// ctx deadline, errors.Is(err, context.DeadlineExceeded) returns
	// true even though the caller's context is still valid. Using
	// ctx.Err() to discriminate prevents that misclassification —
	// the call must come back transient so the outer loop retries.
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		time.Sleep(100 * time.Millisecond)
		w.WriteHeader(http.StatusNoContent)
	}))
	defer srv.Close()

	// Caller ctx is generous; client.Timeout is tiny → client fires first.
	ctx, cancel := context.WithTimeout(context.Background(), 5*time.Second)
	defer cancel()
	client := &http.Client{Timeout: 5 * time.Millisecond}

	_, status, err := pollSessionOnce(ctx, client, srv.URL+"/ai-api/v1/cli-session/n/key")
	if err != nil {
		t.Fatalf("client-side timeout must be transient (no err), got: %v", err)
	}
	if status != pollTransient {
		t.Errorf("expected pollTransient on client.Timeout, got status=%d", status)
	}
	if ctx.Err() != nil {
		t.Errorf("caller ctx should still be valid, got ctx.Err() = %v", ctx.Err())
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
