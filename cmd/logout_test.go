package cmd

import (
	"bytes"
	"os"
	"path/filepath"
	"strings"
	"testing"

	"github.com/Facets-cloud/praxis-cli/internal/credentials"
	"github.com/Facets-cloud/praxis-cli/internal/paths"
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

// TestLogoutCmd_InProjectDir_RemovesGlobalNotProjectProfile is the
// regression test for the local-mode logout fix: logout is a GLOBAL
// operation, so run from inside a local-mode repo it must remove the
// globally-active profile — never the project-pinned one — so a stray or
// teammate-committed <repo>/.praxis can't redirect a destructive logout.
func TestLogoutCmd_InProjectDir_RemovesGlobalNotProjectProfile(t *testing.T) {
	home := t.TempDir()
	t.Setenv("HOME", home)
	resetLogoutFlags()
	defer resetLogoutFlags()

	_ = credentials.Put("default", credentials.Profile{URL: "x", Token: "t1"})
	_ = credentials.Put("acme", credentials.Profile{URL: "y", Token: "t2"})
	_ = credentials.SetActive("default") // global active = default

	repo := filepath.Join(home, "repo")
	if err := os.MkdirAll(repo, 0o755); err != nil {
		t.Fatal(err)
	}
	t.Cleanup(paths.SetGetwdForTest(func() (string, error) { return repo, nil }))
	if _, err := credentials.SetActiveLocal("acme"); err != nil { // project pinned to acme
		t.Fatal(err)
	}

	var buf bytes.Buffer
	logoutCmd.SetOut(&buf)
	if err := logoutCmd.RunE(logoutCmd, nil); err != nil {
		t.Fatalf("RunE err = %v", err)
	}
	// Must remove the GLOBAL profile (default), not the project pointer (acme).
	if !strings.Contains(buf.String(), `"removed": "default"`) {
		t.Errorf("logout in a project dir must remove the global profile; got %q", buf.String())
	}
	store, _ := credentials.Load()
	if _, ok := store["default"]; ok {
		t.Error("global profile 'default' should have been removed")
	}
	if _, ok := store["acme"]; !ok {
		t.Error("project-pinned profile 'acme' must NOT be removed by a global logout")
	}
}

func TestLogoutCmd_All_WipesEverything(t *testing.T) {
	t.Setenv("HOME", t.TempDir())
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
