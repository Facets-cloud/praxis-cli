package mcpmanifest

import (
	"net/http"
	"net/http/httptest"
	"os"
	"path/filepath"
	"strings"
	"testing"
	"time"
)

func TestFetch_HappyPath(t *testing.T) {
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		if r.URL.Path != "/ai-api/v1/mcp/manifest" {
			t.Errorf("unexpected path %q", r.URL.Path)
		}
		if got := r.Header.Get("Authorization"); got != "Bearer sk_test" {
			t.Errorf("auth header = %q", got)
		}
		_, _ = w.Write([]byte(`{"mcps":{"k8s_cli":{}}}`))
	}))
	defer srv.Close()

	raw, err := Fetch(srv.URL, "sk_test", 5*time.Second)
	if err != nil {
		t.Fatalf("Fetch err = %v", err)
	}
	if !strings.Contains(string(raw), "k8s_cli") {
		t.Errorf("body = %s", raw)
	}
}

func TestFetch_NoURL(t *testing.T) {
	if _, err := Fetch("", "tok", 0); err == nil {
		t.Fatal("expected error for empty URL")
	}
}

func TestFetch_NoToken(t *testing.T) {
	if _, err := Fetch("https://x.test", "", 0); err == nil {
		t.Fatal("expected error for empty token")
	}
}

// Negative timeout must be treated as "use the default", not as
// "no deadline" (which is what http.Client does for any non-positive
// duration). Caught by CodeRabbit on PR #2.
func TestFetch_NegativeTimeoutDefaulted(t *testing.T) {
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		_, _ = w.Write([]byte(`{"mcps":{}}`))
	}))
	defer srv.Close()
	// If the negative value were passed through unchanged, http.Client
	// would set no deadline and the request would still succeed against
	// our fast test server — so we can't catch the bug by behavior alone.
	// Instead, we just exercise the path and assert success: the regression
	// would be a panic or a timeout error, not silent success.
	if _, err := Fetch(srv.URL, "tok", -1*time.Second); err != nil {
		t.Fatalf("Fetch with negative timeout should default and succeed; got %v", err)
	}
}

func TestFetch_NonOKStatusReturnsError(t *testing.T) {
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		w.WriteHeader(http.StatusUnauthorized)
		_, _ = w.Write([]byte(`{"detail":"bad key"}`))
	}))
	defer srv.Close()
	_, err := Fetch(srv.URL, "tok", 5*time.Second)
	if err == nil {
		t.Fatal("expected error on 401")
	}
	if !strings.Contains(err.Error(), "HTTP 401") {
		t.Errorf("err = %v (expected to mention 401)", err)
	}
}

func TestWriteSnapshot_AtomicWriteAndPath(t *testing.T) {
	home := t.TempDir()
	t.Setenv("HOME", home)

	raw := []byte(`{"mcps":{"k8s_cli":{}}}`)
	dest, err := WriteSnapshot(raw)
	if err != nil {
		t.Fatalf("WriteSnapshot err = %v", err)
	}

	wantPath := filepath.Join(home, ".praxis", "mcp-tools.json")
	if dest != wantPath {
		t.Errorf("dest = %q, want %q", dest, wantPath)
	}

	got, err := os.ReadFile(dest)
	if err != nil {
		t.Fatalf("read snapshot err = %v", err)
	}
	if string(got) != string(raw) {
		t.Errorf("snapshot mismatch:\n got: %s\nwant: %s", got, raw)
	}

	// File mode should be 0600 — token snapshots are user-private.
	info, _ := os.Stat(dest)
	if perm := info.Mode().Perm(); perm != 0o600 {
		t.Errorf("file perm = %o, want 0600", perm)
	}
}

func TestWriteSnapshot_OverwritesExisting(t *testing.T) {
	home := t.TempDir()
	t.Setenv("HOME", home)

	if _, err := WriteSnapshot([]byte("old")); err != nil {
		t.Fatal(err)
	}
	if _, err := WriteSnapshot([]byte("new")); err != nil {
		t.Fatal(err)
	}
	dest := filepath.Join(home, ".praxis", "mcp-tools.json")
	got, _ := os.ReadFile(dest)
	if string(got) != "new" {
		t.Errorf("expected overwrite, got %q", got)
	}
}
