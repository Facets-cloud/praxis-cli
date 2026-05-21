package cmd

import (
	"bytes"
	"encoding/json"
	"strings"
	"testing"

	"github.com/Facets-cloud/praxis-cli/internal/credentials"
)

func resetStatusFlags() {
	statusJSON = false
}

func TestStatusCmd_NotLoggedIn_DefaultProfile(t *testing.T) {
	t.Setenv("HOME", t.TempDir())
	t.Setenv("PRAXIS_PROFILE", "")
	resetStatusFlags()

	var buf bytes.Buffer
	statusCmd.SetOut(&buf)
	if err := statusCmd.RunE(statusCmd, nil); err != nil {
		t.Fatalf("RunE err = %v", err)
	}
	out := buf.String()
	for _, want := range []string{`"profile": "default"`, `"profile_source": "default"`, `"logged_in": false`} {
		if !strings.Contains(out, want) {
			t.Errorf("output missing %q\nfull: %s", want, out)
		}
	}
}

func TestStatusCmd_LoggedIn_ReportsUsernameAndURL(t *testing.T) {
	t.Setenv("HOME", t.TempDir())
	t.Setenv("PRAXIS_PROFILE", "")
	resetStatusFlags()

	_ = credentials.Put("default", credentials.Profile{
		URL:      "https://x.test",
		Username: "anshul@facets.cloud",
		Token:    "sk_live_t",
	})

	var buf bytes.Buffer
	statusCmd.SetOut(&buf)
	if err := statusCmd.RunE(statusCmd, nil); err != nil {
		t.Fatalf("RunE err = %v", err)
	}
	out := buf.String()
	for _, want := range []string{`"logged_in": true`, `"username": "anshul@facets.cloud"`, `"url": "https://x.test"`} {
		if !strings.Contains(out, want) {
			t.Errorf("output missing %q\nfull: %s", want, out)
		}
	}
}

func TestStatusCmd_DoesNotCallNetwork(t *testing.T) {
	t.Setenv("HOME", t.TempDir())
	t.Setenv("PRAXIS_PROFILE", "")
	resetStatusFlags()

	// Sentinel: if status calls fetchAuthMe, this test would deadlock /
	// fail because we set it to error.
	called := false
	orig := fetchAuthMe
	fetchAuthMe = func(string, string) (*authMeResponse, error) {
		called = true
		return nil, nil
	}
	defer func() { fetchAuthMe = orig }()

	_ = credentials.Put("default", credentials.Profile{URL: "https://x", Token: "t"})

	statusCmd.SetOut(&bytes.Buffer{})
	if err := statusCmd.RunE(statusCmd, nil); err != nil {
		t.Fatalf("RunE err = %v", err)
	}
	if called {
		t.Errorf("status must not call fetchAuthMe (it's a read-only local snapshot)")
	}
}

func TestStatusCmd_IncludesAgentsInstalled(t *testing.T) {
	t.Setenv("HOME", t.TempDir())
	t.Setenv("PRAXIS_PROFILE", "")
	resetStatusFlags()

	var buf bytes.Buffer
	statusCmd.SetOut(&buf)
	if err := statusCmd.RunE(statusCmd, nil); err != nil {
		t.Fatalf("RunE err = %v", err)
	}
	var out map[string]interface{}
	if err := json.Unmarshal(buf.Bytes(), &out); err != nil {
		t.Fatalf("status output should be valid JSON: %v\noutput:\n%s", err, buf.String())
	}
	if _, ok := out["agents_installed"]; !ok {
		t.Errorf("status JSON should include agents_installed key, got keys: %v", mapKeys(out))
	}
}

// mapKeys returns the keys of a map for diagnostic output. Tiny helper
// kept local to this test file rather than pulled in as a dep.
func mapKeys(m map[string]interface{}) []string {
	keys := make([]string, 0, len(m))
	for k := range m {
		keys = append(keys, k)
	}
	return keys
}

func TestStatusCmd_HonorsActiveProfileFromUseConfig(t *testing.T) {
	// `praxis use acme` is the documented way to switch profiles —
	// status must reflect that without any flag.
	t.Setenv("HOME", t.TempDir())
	t.Setenv("PRAXIS_PROFILE", "")
	resetStatusFlags()

	_ = credentials.Put("default", credentials.Profile{URL: "https://default.test", Token: "td"})
	_ = credentials.Put("acme", credentials.Profile{URL: "https://acme.test", Token: "ta"})
	if err := credentials.SetActive("acme"); err != nil {
		t.Fatal(err)
	}

	var buf bytes.Buffer
	statusCmd.SetOut(&buf)
	if err := statusCmd.RunE(statusCmd, nil); err != nil {
		t.Fatalf("RunE err = %v", err)
	}
	if !strings.Contains(buf.String(), `"profile": "acme"`) ||
		!strings.Contains(buf.String(), `"url": "https://acme.test"`) {
		t.Errorf("`praxis use acme` not honored, got %q", buf.String())
	}
}
