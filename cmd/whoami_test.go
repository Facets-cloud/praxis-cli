package cmd

import (
	"bytes"
	"errors"
	"strings"
	"testing"

	"github.com/Facets-cloud/praxis-cli/internal/credentials"
)

func resetWhoamiFlags() {
	whoamiJSON = false
}

func TestWhoamiCmd_NoProfileLoggedIn(t *testing.T) {
	t.Setenv("HOME", t.TempDir())
	t.Setenv("PRAXIS_PROFILE", "")
	resetWhoamiFlags()

	var buf bytes.Buffer
	whoamiCmd.SetOut(&buf)
	whoamiCmd.SetErr(&buf)

	// Default profile has no credentials → expect error path. Since the
	// command os.Exit(3)s, we can't unit-test the exit; this test is
	// covered via e2e instead. Documenting the contract:
	t.Skip("os.Exit(3) when not logged in; covered by manual + e2e testing")
}

func TestWhoamiCmd_LoggedIn_LiveCheckOK(t *testing.T) {
	t.Setenv("HOME", t.TempDir())
	t.Setenv("PRAXIS_PROFILE", "")
	resetWhoamiFlags()

	if err := credentials.Put("default", credentials.Profile{
		URL:      "https://x.test",
		Username: "saved@x.com",
		Token:    "sk_live_t",
	}); err != nil {
		t.Fatal(err)
	}

	// Stub fetchAuthMe so the command does not hit the network.
	orig := fetchAuthMe
	fetchAuthMe = func(baseURL, token string) (*authMeResponse, error) {
		if baseURL != "https://x.test" || token != "sk_live_t" {
			return nil, errors.New("unexpected URL/token")
		}
		return &authMeResponse{UserID: "u1", Email: "anshul@facets.cloud"}, nil
	}
	defer func() { fetchAuthMe = orig }()

	var buf bytes.Buffer
	whoamiCmd.SetOut(&buf)
	if err := whoamiCmd.RunE(whoamiCmd, nil); err != nil {
		t.Fatalf("RunE err = %v", err)
	}
	out := buf.String()
	for _, want := range []string{`"profile": "default"`, `"username": "anshul@facets.cloud"`, `"url": "https://x.test"`} {
		if !strings.Contains(out, want) {
			t.Errorf("output missing %q\nfull: %s", want, out)
		}
	}
}

func TestWhoamiCmd_HonorsActiveProfileFromUseConfig(t *testing.T) {
	// `praxis use acme` is the documented way to switch profiles —
	// whoami must reflect that without any flag.
	t.Setenv("HOME", t.TempDir())
	t.Setenv("PRAXIS_PROFILE", "")
	resetWhoamiFlags()

	_ = credentials.Put("default", credentials.Profile{URL: "https://default.test", Token: "td"})
	_ = credentials.Put("acme", credentials.Profile{URL: "https://acme.test", Token: "ta", Username: "support@acme.com"})
	if err := credentials.SetActive("acme"); err != nil {
		t.Fatal(err)
	}

	var capturedURL string
	orig := fetchAuthMe
	fetchAuthMe = func(baseURL, token string) (*authMeResponse, error) {
		capturedURL = baseURL
		return &authMeResponse{Email: "support@acme.com"}, nil
	}
	defer func() { fetchAuthMe = orig }()

	whoamiCmd.SetOut(&bytes.Buffer{})
	if err := whoamiCmd.RunE(whoamiCmd, nil); err != nil {
		t.Fatalf("RunE err = %v", err)
	}
	if capturedURL != "https://acme.test" {
		t.Errorf("`praxis use acme` should hit acme URL, got %q", capturedURL)
	}
}
