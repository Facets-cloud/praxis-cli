package cmd

import (
	"os"
	"path/filepath"
	"testing"

	"github.com/Facets-cloud/praxis-cli/internal/credentials"
	"github.com/Facets-cloud/praxis-cli/internal/paths"
)

// `praxis login --local` pins the profile to the current directory tree by
// writing <cwd>/.facets/credentials — raptor's own local mode — with the
// section and a [default] copy, and installs project-scoped. The home store
// is left alone. Uses the token-reuse path so no browser / network is involved.
func TestLogin_Local_PinsProjectAndLeavesGlobalAlone(t *testing.T) {
	home := t.TempDir()
	t.Setenv("HOME", home)
	resetLoginFlags(t)
	clearFacetsEnv(t)

	seedPAT(t, "default", "https://g.test", "tg")
	seedPAT(t, "aurva", "https://aurva.test", "tok")
	stubAuthMeOK(t)
	stubPostAuth(t) // record-only; we're testing pinning/scoping, not install

	repo := filepath.Join(home, "repo")
	if err := os.MkdirAll(repo, 0o755); err != nil {
		t.Fatal(err)
	}
	t.Cleanup(paths.SetGetwdForTest(func() (string, error) { return repo, nil }))

	rootProfile = "aurva"
	loginLocal = true
	if _, err := runLoginRunE(t); err != nil {
		t.Fatalf("login --local err = %v", err)
	}

	tree := readFacetsFile(t, repo)
	if tree["aurva"]["token"] != "tok" || tree["default"]["token"] != "tok" {
		t.Errorf("tree file = %v, want aurva and a default copy", tree)
	}
	if _, err := os.Stat(filepath.Join(repo, ".praxis")); err != nil {
		t.Errorf("project receipt dir should exist: %v", err)
	}
	// Inside the repo both CLIs read the tree file, so default IS aurva there.
	t.Cleanup(credentials.SetGetwdForTest(func() (string, error) { return repo, nil }))
	if a, _ := credentials.ResolveActive(""); a.Name != "default" || a.Profile.URL != "https://aurva.test" {
		t.Errorf("in-repo resolution = %+v, want default with aurva's credentials", a)
	}
	// The home store is untouched — login --local must not switch it.
	if got := homeFacetsURL(t); got != "https://g.test" {
		t.Errorf("home [default] = %q; login --local must leave it alone", got)
	}
}

// login --local fails clearly (and pins nothing) when run outside the home
// subtree, where discovery could never find the tree again.
func TestLogin_Local_OutsideHome_Errors(t *testing.T) {
	t.Setenv("HOME", t.TempDir())
	resetLoginFlags(t)
	clearFacetsEnv(t)

	seedPAT(t, "aurva", "https://aurva.test", "tok")
	stubAuthMeOK(t)
	stubPostAuth(t)

	outside := t.TempDir()
	t.Cleanup(paths.SetGetwdForTest(func() (string, error) { return outside, nil }))

	rootProfile = "aurva"
	loginLocal = true
	if _, err := runLoginRunE(t); err == nil {
		t.Fatal("login --local outside home should return an error")
	}
	if _, err := os.Stat(filepath.Join(outside, ".facets")); !os.IsNotExist(err) {
		t.Error("nothing should be written outside home")
	}
}

// An API key cannot be pinned: raptor's file cannot hold it.
func TestLogin_Local_APIKeyIsRefused(t *testing.T) {
	home := t.TempDir()
	t.Setenv("HOME", home)
	resetLoginFlags(t)
	clearFacetsEnv(t)
	seedProfile(t, "key", "https://k.test", "sk")
	stubAuthMeOK(t)
	stubPostAuth(t)
	repoUnderHome(t, home)

	rootProfile = "key"
	loginLocal = true
	if _, err := runLoginRunE(t); err == nil {
		t.Fatal("login --local with an API-key profile should fail")
	}
}
