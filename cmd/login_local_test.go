package cmd

import (
	"bytes"
	"os"
	"path/filepath"
	"testing"

	"github.com/Facets-cloud/praxis-cli/internal/credentials"
	"github.com/Facets-cloud/praxis-cli/internal/paths"
)

// `praxis login --local` pins the profile to the current directory tree with a
// name-only pin (<repo>/.facets/profile) and installs project-scoped. The
// token stays in the home store, no tree credentials file is written, and the
// machine default is left alone. Uses the token-reuse path so no browser /
// network is involved.
func TestLogin_Local_PinsProjectAndLeavesGlobalAlone(t *testing.T) {
	home := t.TempDir()
	t.Setenv("HOME", home)
	resetLoginFlags(t)
	clearFacetsEnv(t)

	seedPAT(t, "default", "https://g.test", "tg")
	seedPAT(t, "aurva", "https://aurva.test", "tok")
	stubAuthMeOK(t)
	call := stubPostAuthCapture(t)

	repo := filepath.Join(home, "repo")
	if err := os.MkdirAll(repo, 0o755); err != nil {
		t.Fatal(err)
	}
	inDir(t, repo)

	rootProfile = "aurva"
	loginLocal = true
	out, err := runLoginRunE(t)
	if err != nil {
		t.Fatalf("login --local err = %v", err)
	}

	if data, _ := os.ReadFile(credentials.PinPathIn(repo)); string(data) != "aurva\n" {
		t.Errorf("pin = %q, want the name only", data)
	}
	if _, err := os.Stat(credentials.FacetsPathIn(repo)); !os.IsNotExist(err) {
		t.Error("login --local must not copy a token into the tree")
	}
	if b, _ := os.ReadFile(filepath.Join(repo, ".praxis", ".gitignore")); string(b) != "*\n" {
		t.Errorf(".praxis/.gitignore = %q, want everything ignored", b)
	}
	if want := filepath.Join(repo, ".praxis"); call.activeRoot != want {
		t.Errorf("activeRoot during sync = %q, want the project root %q", call.activeRoot, want)
	}
	if a, _ := credentials.ResolveActive(""); a.Name != "aurva" || a.Source != credentials.SourcePin {
		t.Errorf("in-repo resolution = %+v, want aurva from the pin", a)
	}
	if got := homeFacetsURL(t); got != "https://g.test" {
		t.Errorf("home [default] = %q; login --local must leave it alone", got)
	}
	if got := decodeMap(t, out); got["pin_path"] != credentials.PinPathIn(repo) || got["scope"] != "local" {
		t.Errorf("payload = %v, want pin_path and local scope", got)
	}

	// A second --local login in the pinned folder re-logs the pinned profile.
	rootProfile = ""
	if _, err := runLoginRunE(t); err != nil {
		t.Fatalf("second login --local err = %v", err)
	}
	if call.count != 2 || call.baseURL != "https://aurva.test" {
		t.Errorf("second login synced %d times against %q, want the pinned aurva", call.count, call.baseURL)
	}
}

// In a legacy tree (an older login --local left a credentials file there),
// --local writes the section to the tree file, where the tree reads it.
func TestLogin_Local_LegacyTree_WritesTheTreeFile(t *testing.T) {
	home := t.TempDir()
	t.Setenv("HOME", home)
	resetLoginFlags(t)
	clearFacetsEnv(t)

	seedPAT(t, "default", "https://g.test", "tg")
	seedPAT(t, "aurva", "https://aurva.test", "tok")
	seedPAT(t, "zed", "https://zed.test", "tz")
	repo := filepath.Join(home, "repo")
	if err := os.MkdirAll(repo, 0o755); err != nil {
		t.Fatal(err)
	}
	seedLegacyTree(t, repo, "aurva")
	inDir(t, repo)
	stubPostAuthCapture(t)

	// zed exists only in the home store; the tree reads its own file.
	var buf bytes.Buffer
	prof := credentials.FacetsProfile("https://zed.test", "u@x", "tz")
	if err := persistAndSetup(&buf, true, "zed", prof, "u@x", true); err != nil {
		t.Fatalf("persist --local err = %v", err)
	}
	if tree := readFacetsFile(t, repo); tree["zed"]["control_plane_url"] != "https://zed.test" {
		t.Errorf("tree file = %v, want zed written where the tree reads", tree)
	}
	if a, _ := credentials.ResolveActive(""); a.Name != "zed" || !a.Loaded {
		t.Errorf("in-repo resolution = %+v, want the pinned zed, loaded", a)
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
