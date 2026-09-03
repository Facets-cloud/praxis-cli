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
		URL:      "https://default.test",
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

// Logout removes the active section and every section that holds the same
// credentials, and nothing else. After `profiles use acme`, [default] is a
// copy of acme: removing only the copy would leave [acme] as the sole section,
// which the store resolves as active again — logged in, with no skills. A
// sibling with other credentials is untouched.
func TestLogoutCmd_LeavesOtherProfilesAlone(t *testing.T) {
	t.Setenv("HOME", t.TempDir())
	resetLogoutFlags()
	defer resetLogoutFlags()

	_ = credentials.Put("default", credentials.Profile{URL: "x", Token: "t1"})
	_ = credentials.Put("acme", credentials.Profile{URL: "y", Token: "t2"})
	_ = credentials.Put("other", credentials.Profile{URL: "z", Token: "t3"})
	_, _ = credentials.SetDefault("acme")

	var buf bytes.Buffer
	logoutCmd.SetOut(&buf)
	if err := logoutCmd.RunE(logoutCmd, nil); err != nil {
		t.Fatalf("RunE err = %v", err)
	}
	if !strings.Contains(buf.String(), `"removed": "default"`) {
		t.Errorf("expected JSON default removal, got %q", buf.String())
	}
	if !strings.Contains(buf.String(), `"removed_copies"`) || !strings.Contains(buf.String(), `"acme"`) {
		t.Errorf("expected removed_copies naming acme, got %q", buf.String())
	}
	store, _ := credentials.Load()
	if _, ok := store["default"]; ok {
		t.Errorf("default (the active copy) should be gone")
	}
	if _, ok := store["acme"]; ok {
		t.Errorf("acme holds the same credentials as the removed [default]; it must go too, or the user is still logged in")
	}
	if _, ok := store["other"]; !ok {
		t.Errorf("other (different credentials) should remain")
	}
	if active, _ := credentials.ResolveActive(""); active.Loaded {
		t.Errorf("still logged in after logout: %+v", active)
	}
}

func TestLogoutCmd_All_WipesEverything(t *testing.T) {
	t.Setenv("HOME", t.TempDir())
	resetLogoutFlags()

	_ = credentials.Put("default", credentials.Profile{URL: "x", Token: "t1"})
	_ = credentials.Put("acme", credentials.Profile{URL: "y", Token: "t2"})
	_, _ = credentials.SetDefault("acme")

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

// Inside a local-mode tree the store is the tree's own credentials file, for
// praxis and raptor alike, so logout there removes the tree's [default] and
// leaves the home store alone.
func TestLogoutCmd_InProjectDir_RemovesTreeDefaultNotHome(t *testing.T) {
	home := t.TempDir()
	t.Setenv("HOME", home)
	resetLogoutFlags()
	defer resetLogoutFlags()
	clearFacetsEnv(t)

	seedPAT(t, "default", "https://home.test", "t1")
	seedPAT(t, "acme", "https://acme.test", "t2")
	repo := repoUnderHome(t, home)
	if err := credentials.SetDefaultLocal("acme", repo); err != nil {
		t.Fatal(err)
	}
	inDir(t, repo)

	var buf bytes.Buffer
	logoutCmd.SetOut(&buf)
	if err := logoutCmd.RunE(logoutCmd, nil); err != nil {
		t.Fatalf("RunE err = %v", err)
	}
	if !strings.Contains(buf.String(), `"removed": "default"`) {
		t.Errorf("logout in a local tree must remove the tree's default; got %q", buf.String())
	}
	tree := readFacetsFile(t, repo)
	if _, still := tree["default"]; still {
		t.Error("tree [default] should have been removed")
	}
	// The tree's [acme] is the section [default] was copied from: the same
	// credentials, so it goes too — else the tree resolves it as its sole
	// section and stays logged in.
	if _, still := tree["acme"]; still {
		t.Error("tree [acme] holds the removed credentials and must go too")
	}
	if got := homeFacetsURL(t); got != "https://home.test" {
		t.Errorf("home [default] = %q; a logout inside a tree must not touch the home store", got)
	}
}
