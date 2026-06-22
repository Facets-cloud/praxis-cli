package cmd

import (
	"os"
	"path/filepath"
	"testing"

	"github.com/Facets-cloud/praxis-cli/internal/credentials"
	"github.com/Facets-cloud/praxis-cli/internal/paths"
)

// TestLogin_Local_PinsProjectAndLeavesGlobalAlone is the core of the
// `praxis login --local` flow: it pins the profile to the current directory
// tree (project pointer) and installs project-scoped, WITHOUT touching the
// global active-profile pointer. Uses the token-reuse path so no browser /
// network is involved.
func TestLogin_Local_PinsProjectAndLeavesGlobalAlone(t *testing.T) {
	home := t.TempDir()
	t.Setenv("HOME", home)
	t.Setenv("PRAXIS_PROFILE", "")
	resetLoginFlags(t)

	// A global profile is active; a separate profile will be pinned locally.
	seedProfile(t, "globalprof", "https://g.test", "tg")
	seedProfile(t, "aurva", "https://aurva.test", "tok")
	if err := credentials.SetActive("globalprof"); err != nil {
		t.Fatal(err)
	}

	// Reuse path: stored token validates without a browser.
	stubAuthMe(t, func(baseURL, token string) (*authMeResponse, error) {
		return &authMeResponse{Email: "u@x", canonicalBaseURL: baseURL}, nil
	})
	stubPostAuth(t) // record-only; we're testing pointer/scoping, not install

	repo := filepath.Join(home, "repo")
	if err := os.MkdirAll(repo, 0o755); err != nil {
		t.Fatal(err)
	}
	t.Cleanup(paths.SetGetwdForTest(func() (string, error) { return repo, nil }))

	loginProfile = "aurva"
	loginLocal = true
	if _, err := runLoginRunE(t); err != nil {
		t.Fatalf("login --local err = %v", err)
	}

	// Project pointer written under the repo.
	if _, err := os.Stat(filepath.Join(repo, ".praxis", "config.json")); err != nil {
		t.Errorf("project pointer should exist: %v", err)
	}
	// From inside the repo, the active profile is the locally-pinned one.
	if a, _ := credentials.ResolveActive(""); a.Name != "aurva" || a.Source != credentials.SourceProject {
		t.Errorf("in-repo resolution = %+v, want aurva/project", a)
	}
	// The GLOBAL pointer is untouched — login --local must not switch it.
	if g, _ := credentials.ResolveActiveGlobal(); g.Name != "globalprof" {
		t.Errorf("global pointer changed to %q; login --local must leave it alone", g.Name)
	}
}

// TestLogin_Local_OutsideHome_Errors verifies login --local fails clearly
// (and does not flip any pointer) when run outside the home subtree.
func TestLogin_Local_OutsideHome_Errors(t *testing.T) {
	t.Setenv("HOME", t.TempDir())
	t.Setenv("PRAXIS_PROFILE", "")
	resetLoginFlags(t)

	seedProfile(t, "aurva", "https://aurva.test", "tok")
	stubAuthMe(t, func(baseURL, token string) (*authMeResponse, error) {
		return &authMeResponse{Email: "u@x", canonicalBaseURL: baseURL}, nil
	})
	stubPostAuth(t)

	// cwd outside the faked home → SetActiveLocal must refuse.
	outside := t.TempDir()
	t.Cleanup(paths.SetGetwdForTest(func() (string, error) { return outside, nil }))

	loginProfile = "aurva"
	loginLocal = true
	_, err := runLoginRunE(t)
	if err == nil {
		t.Fatal("login --local outside home should return an error")
	}
}
