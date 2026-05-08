package cmd

import (
	"bytes"
	"strings"
	"testing"

	"github.com/Facets-cloud/praxis-cli/internal/credentials"
)

// resetLogoutFlags clears flag state between tests since cobra commands
// are package globals.
func resetLogoutFlags() {
	logoutAll = false
	logoutJSON = false
}

// Tests pass a bytes.Buffer to capture output, which is non-TTY → render
// auto-emits JSON. So assertions check JSON content; the human-readable
// text path is exercised manually + via the e2e test against a TTY.

func TestLogoutCmd_NoCredentials_Default(t *testing.T) {
	t.Setenv("HOME", t.TempDir())
	t.Setenv("PRAXIS_PROFILE", "")
	resetLogoutFlags()

	var buf bytes.Buffer
	logoutCmd.SetOut(&buf)
	if err := logoutCmd.RunE(logoutCmd, nil); err != nil {
		t.Fatalf("RunE err = %v", err)
	}
	// v0.7: no-creds path → removed=null. The "note: profile not present"
	// field was dropped when the JSON shape was tightened to a fixed
	// {removed, removed_skills} shape.
	if !strings.Contains(buf.String(), `"removed": null`) {
		t.Errorf("output missing 'removed: null'; full: %s", buf.String())
	}
}

func TestLogoutCmd_RemovesActive(t *testing.T) {
	t.Setenv("HOME", t.TempDir())
	t.Setenv("PRAXIS_PROFILE", "")
	resetLogoutFlags()

	if err := credentials.Put("default", credentials.Profile{
		URL:      "https://askpraxis.ai",
		Username: "x@x.com",
		Token:    "sk_live_abc",
	}); err != nil {
		t.Fatal(err)
	}

	var buf bytes.Buffer
	logoutCmd.SetOut(&buf)
	if err := logoutCmd.RunE(logoutCmd, nil); err != nil {
		t.Fatalf("RunE err = %v", err)
	}
	if !strings.Contains(buf.String(), `"removed": "default"`) {
		t.Errorf("output = %q, want JSON 'removed: default'", buf.String())
	}

	store, _ := credentials.Load()
	if _, ok := store["default"]; ok {
		t.Errorf("default profile should be gone after logout")
	}
}

// TestLogoutCmd_LeavesOtherProfilesAlone pins the v0.7 logout contract:
// only the active profile's credentials are removed; sibling profiles
// in the credentials file are untouched. v0.7 dropped --profile from
// logout, so this test sets active to "acme" first via SetActive, then
// runs logout and verifies "default" survives.
func TestLogoutCmd_LeavesOtherProfilesAlone(t *testing.T) {
	t.Setenv("HOME", t.TempDir())
	t.Setenv("PRAXIS_PROFILE", "")
	resetLogoutFlags()
	defer resetLogoutFlags()

	_ = credentials.Put("default", credentials.Profile{URL: "x", Token: "t1"})
	_ = credentials.Put("acme", credentials.Profile{URL: "y", Token: "t2"})
	_ = credentials.SetActive("acme")

	var buf bytes.Buffer
	logoutCmd.SetOut(&buf)
	if err := logoutCmd.RunE(logoutCmd, nil); err != nil {
		t.Fatalf("RunE err = %v", err)
	}
	if !strings.Contains(buf.String(), `"removed": "acme"`) {
		t.Errorf("expected JSON acme removal, got %q", buf.String())
	}
	store, _ := credentials.Load()
	if _, ok := store["acme"]; ok {
		t.Errorf("acme should be gone")
	}
	if _, ok := store["default"]; !ok {
		t.Errorf("default should remain")
	}
}

func TestLogoutCmd_All_WipesEverything(t *testing.T) {
	t.Setenv("HOME", t.TempDir())
	t.Setenv("PRAXIS_PROFILE", "")
	resetLogoutFlags()

	_ = credentials.Put("default", credentials.Profile{URL: "x", Token: "t1"})
	_ = credentials.Put("acme", credentials.Profile{URL: "y", Token: "t2"})
	_ = credentials.SetActive("acme")

	logoutAll = true
	defer resetLogoutFlags()
	var buf bytes.Buffer
	logoutCmd.SetOut(&buf)
	if err := logoutCmd.RunE(logoutCmd, nil); err != nil {
		t.Fatalf("RunE err = %v", err)
	}
	if !strings.Contains(buf.String(), `"removed": "all"`) {
		t.Errorf("expected JSON removed: all, got %q", buf.String())
	}
	store, _ := credentials.Load()
	if len(store) != 0 {
		t.Errorf("store should be empty, got %d", len(store))
	}
}
