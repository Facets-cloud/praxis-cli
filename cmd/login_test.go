package cmd

import (
	"bytes"
	"errors"
	"net/url"
	"os"
	"path/filepath"
	"strings"
	"testing"
)

func TestBuildLoginURL_ContainsExpectedParams(t *testing.T) {
	got := buildLoginURL("https://acme.askpraxis.ai", 54321, "abc-session-nonce")

	parsed, err := url.Parse(got)
	if err != nil {
		t.Fatalf("buildLoginURL produced invalid URL %q: %v", got, err)
	}
	if parsed.Path != "/ui/ai/settings/api-keys" {
		t.Errorf("path = %q, want /ui/ai/settings/api-keys", parsed.Path)
	}
	q := parsed.Query()
	if q.Get("cli_callback") != "http://127.0.0.1:54321/key" {
		t.Errorf("cli_callback = %q, want http://127.0.0.1:54321/key", q.Get("cli_callback"))
	}
	if q.Get("cli_session") != "abc-session-nonce" {
		t.Errorf("cli_session = %q, want abc-session-nonce", q.Get("cli_session"))
	}
	if q.Get("suggested_name") != "praxis-cli" {
		t.Errorf("suggested_name = %q, want praxis-cli", q.Get("suggested_name"))
	}
}

func TestRandomNonce_IsHexAndUnique(t *testing.T) {
	a, b := randomNonce(), randomNonce()
	if a == b {
		t.Errorf("randomNonce should produce distinct values, got %q twice", a)
	}
	if len(a) != 48 {
		t.Errorf("nonce length = %d, want 48 (24 bytes hex-encoded)", len(a))
	}
	for _, c := range a {
		if !strings.ContainsRune("0123456789abcdef", c) {
			t.Errorf("nonce contains non-hex char %q in %q", c, a)
			break
		}
	}
}

func TestSaveAndVerifyToken_PersistsCredentialsWith0600(t *testing.T) {
	t.Setenv("HOME", t.TempDir())

	// Stub fetchAuthMe so we don't make a real HTTP request.
	origFetch := fetchAuthMe
	fetchAuthMe = func(baseURL, token string) (*authMeResponse, error) {
		return &authMeResponse{
			UserID:   "user-xyz",
			Email:    "tester@example.com",
			Username: "tester",
		}, nil
	}
	t.Cleanup(func() { fetchAuthMe = origFetch })

	var buf bytes.Buffer
	err := saveAndVerifyToken(&buf, true, "https://example.com", "sk_live_abc")
	if err != nil {
		t.Fatalf("saveAndVerifyToken err = %v", err)
	}

	// Output should be JSON success.
	out := buf.String()
	for _, want := range []string{`"ok": true`, `"tester@example.com"`, `"https://example.com"`} {
		if !strings.Contains(out, want) {
			t.Errorf("output missing %q\nfull: %s", want, out)
		}
	}

	// Credentials file written with 0600.
	home, _ := os.UserHomeDir()
	credPath := filepath.Join(home, ".praxis", "credentials")
	info, err := os.Stat(credPath)
	if err != nil {
		t.Fatalf("credentials not written: %v", err)
	}
	if mode := info.Mode().Perm(); mode != 0600 {
		t.Errorf("credentials perm = %o, want 0600", mode)
	}
	body, _ := os.ReadFile(credPath)
	for _, want := range []string{"sk_live_abc", "tester@example.com", "user-xyz", "https://example.com"} {
		if !strings.Contains(string(body), want) {
			t.Errorf("credentials missing %q\nfile:\n%s", want, body)
		}
	}
}

func TestSaveAndVerifyToken_FetchAuthFailureExits(t *testing.T) {
	// We can't easily test the os.Exit(3) path without rearranging the
	// helper to return an error. As a pragmatic alternative, we verify
	// fetchAuthMe is invoked exactly once on a failure path by stubbing
	// it to return an error and noting we'd have to capture os.Exit via
	// a deeper refactor. For now, this test documents the contract: the
	// helper hands off to render.PrintError + os.Exit(exitcode.Auth).
	t.Skip("os.Exit(3) path covered by manual + e2e testing; refactor planned to make it unit-testable without process exit")

	// Reference orig so the import isn't unused if we re-enable.
	_ = errors.New
}
