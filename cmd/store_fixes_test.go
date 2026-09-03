package cmd

import (
	"bytes"
	"os"
	"path/filepath"
	"strings"
	"testing"

	"github.com/Facets-cloud/praxis-cli/internal/credentials"
	"github.com/Facets-cloud/praxis-cli/internal/exitcode"
	"github.com/Facets-cloud/praxis-cli/internal/raptorstate"
	"github.com/Facets-cloud/praxis-cli/internal/skillinstall"
)

// Inside a tree whose file holds only [default], logout removes that file —
// the local-mode marker. The skill wipe must still target the tree's root,
// not fall back to home once the marker is gone.
func TestLogoutCmd_InTree_OnlyDefault_WipesTheTreeNotHome(t *testing.T) {
	home := t.TempDir()
	t.Setenv("HOME", home)
	resetLogoutFlags()
	defer resetLogoutFlags()
	clearFacetsEnv(t)

	seedPAT(t, "default", "https://home.test", "t1")
	repo := repoUnderHome(t, home)
	if err := credentials.SetDefaultLocal("default", repo); err != nil {
		t.Fatal(err)
	}
	for _, root := range []string{filepath.Join(home, ".praxis"), filepath.Join(repo, ".praxis")} {
		if err := os.MkdirAll(root, 0o700); err != nil {
			t.Fatal(err)
		}
		if err := os.WriteFile(filepath.Join(root, "mcp-tools.json"), []byte("{}"), 0o600); err != nil {
			t.Fatal(err)
		}
	}
	inDir(t, repo)

	var buf bytes.Buffer
	logoutCmd.SetOut(&buf)
	if err := logoutCmd.RunE(logoutCmd, nil); err != nil {
		t.Fatalf("RunE err = %v", err)
	}
	if _, err := os.Stat(filepath.Join(repo, ".facets", "credentials")); !os.IsNotExist(err) {
		t.Error("the tree's credentials file should be gone")
	}
	if _, err := os.Stat(filepath.Join(repo, ".praxis", "mcp-tools.json")); !os.IsNotExist(err) {
		t.Error("the tree's snapshot should have been removed")
	}
	if _, err := os.Stat(filepath.Join(home, ".praxis", "mcp-tools.json")); err != nil {
		t.Errorf("the home snapshot was removed by a logout inside a tree: %v", err)
	}
	if got := homeFacetsURL(t); got != "https://home.test" {
		t.Errorf("home [default] = %q; must survive", got)
	}
}

// After `login -p acme`, [default] is a copy of acme: one deployment, not two
// profiles, so a --local login has nothing to ask.
func TestPickProfile_DefaultCopyIsNotASecondProfile(t *testing.T) {
	t.Setenv("HOME", t.TempDir())
	clearFacetsEnv(t)
	seedPAT(t, "acme", "https://acme.test", "t")
	if _, err := credentials.SetDefault("acme"); err != nil {
		t.Fatal(err)
	}
	code := stubOsExit(t)
	var buf bytes.Buffer
	got, err := pickProfile(&buf, true, true, "")
	if err != nil || got != "" || *code != -1 {
		t.Errorf("pickProfile = %q, %v, exit %d; want no choice to make", got, err, *code)
	}
}

// With two deployments and no [default], a bare login would create [default]
// on a guess; it asks (or, without a terminal, refuses) like --local does.
func TestPickProfile_BareLoginWithNoDefaultDoesNotGuess(t *testing.T) {
	t.Setenv("HOME", t.TempDir())
	clearFacetsEnv(t)
	seedPAT(t, "acme", "https://acme.test", "t1")
	seedPAT(t, "zed", "https://zed.test", "t2")
	code := stubOsExit(t)
	var buf bytes.Buffer
	if _, err := pickProfile(&buf, true, false, ""); err == nil || *code != exitcode.Usage {
		t.Errorf("pickProfile err = %v, exit %d; want a refusal", err, *code)
	}
}

// A number that is not a position in the list is a slip, not a new profile
// named "0".
func TestPickProfile_NumberOutsideTheListIsRefused(t *testing.T) {
	t.Setenv("HOME", t.TempDir())
	clearFacetsEnv(t)
	seedPAT(t, "default", "https://d.test", "t1")
	seedPAT(t, "acme", "https://acme.test", "t2")
	code := stubOsExit(t)
	origTTY, origLine := stdinIsTTY, readLine
	stdinIsTTY = func() bool { return true }
	readLine = func() (string, error) { return "0", nil }
	t.Cleanup(func() { stdinIsTTY, readLine = origTTY, origLine })

	var buf bytes.Buffer
	got, err := pickProfile(&buf, false, true, "")
	if err == nil || got != "" || *code != exitcode.Usage || !strings.Contains(err.Error(), "not a position") {
		t.Errorf("pickProfile = %q, %v, exit %d; want the number refused", got, err, *code)
	}
	if store, _ := credentials.Load(); len(store) != 2 {
		t.Errorf("store changed: %v", store)
	}
}

// `profiles rm X` refuses an X whose credentials are the active section's:
// removing it would claim raptor is logged out while [default] still holds
// the same token.
func TestProfilesRm_RefusesTheActiveProfilesCopy(t *testing.T) {
	t.Setenv("HOME", t.TempDir())
	clearFacetsEnv(t)
	seedPAT(t, "acme", "https://acme.test", "t")
	if _, err := credentials.SetDefault("acme"); err != nil {
		t.Fatal(err)
	}
	code := stubOsExit(t)
	var buf bytes.Buffer
	profilesRmCmd.SetOut(&buf)
	t.Cleanup(func() { profilesRmCmd.SetOut(nil) })
	_ = profilesRmCmd.RunE(profilesRmCmd, []string{"acme"})
	if *code != exitcode.Usage || !strings.Contains(buf.String(), "copy") {
		t.Errorf("exit %d, out %q; want a refusal naming the copy", *code, buf.String())
	}
	if store, _ := credentials.Load(); store["acme"].Token != "t" {
		t.Error("acme was removed")
	}
}

// A --local login that would end with a Praxis API key is refused before any
// credential is written: --token is always an API key.
func TestLogin_LocalWithToken_RefusedBeforeAnyWrite(t *testing.T) {
	isolateHome(t)
	resetLoginFlags(t)
	clearFacetsEnv(t)
	code := stubOsExit(t)
	stubAuthMe(t, func(string, map[string]string) (*authMeResponse, error) {
		t.Fatal("login verified a token it must refuse to save")
		return nil, nil
	})
	loginLocal = true
	loginToken = "sk_live_x"
	loginURL = "https://x.test"
	out, err := runLoginRunE(t)
	if err == nil || *code != exitcode.Usage || !strings.Contains(out, "control-plane PAT") {
		t.Fatalf("err=%v code=%d out=%q; want a usage refusal", err, *code, out)
	}
	if store, _ := credentials.Load(); len(store) != 0 {
		t.Errorf("store written despite the refusal: %v", store)
	}
}

// The browser tier mints a Praxis API key, so a --local login that reaches it
// is refused there — no key minted, nothing saved.
func TestLogin_LocalFallingToBrowser_Refused(t *testing.T) {
	isolateHome(t)
	resetLoginFlags(t)
	clearFacetsEnv(t)
	code := stubOsExit(t)
	browsed := stubBrowserLogin(t)
	loginLocal = true
	loginURL = "https://x.test"
	_, err := runLoginRunE(t)
	if err == nil || *code != exitcode.Usage {
		t.Fatalf("err=%v code=%d; want a usage refusal", err, *code)
	}
	if *browsed {
		t.Error("the browser tier ran for a --local login")
	}
}

// Re-pointing an existing raptor section at another control plane is still
// saved (the user asked for it) and the section follows.
func TestPersistAndSetup_RepointsARaptorSection(t *testing.T) {
	isolateHome(t)
	clearFacetsEnv(t)
	stubPostAuth(t)
	seedPAT(t, "acme", "https://old.test", "T1")
	var buf bytes.Buffer
	if err := persistAndSetup(&buf, true, "acme", credentials.FacetsProfile("https://new.test", "u@x", "T2"), "u@x", false); err != nil {
		t.Fatal(err)
	}
	store, _ := credentials.Load()
	if store["acme"].URL != "https://new.test" || store["acme"].Token != "T2" {
		t.Errorf("acme = %+v, want re-pointed", store["acme"])
	}
}

// raptor resolved through CONTROL_PLANE_URL ignores FACETS_PROFILE, so no
// prefix is required even when the hosts differ; matches_praxis_url carries
// the disagreement.
func TestRaptorStatusBlock_EnvOverrideNeedsNoPrefix(t *testing.T) {
	active := credentials.Active{Name: "acme", Loaded: true,
		Profile: credentials.Profile{URL: "https://acme.test", Store: credentials.StoreFacets}}
	st := raptorstate.State{Installed: true, Found: true, Profile: "env", Source: raptorstate.SourceEnv, ControlPlaneURL: "https://other.test"}
	block := raptorStatusBlockFor(st, active, "darwin", "arm64")
	if block["prefix_required"] != false || block["matches_praxis_url"] != false {
		t.Errorf("block = %v; want prefix_required false, matches_praxis_url false", block)
	}
	if line := raptorStatusLine(st, active); strings.Contains(line, "FACETS_PROFILE") {
		t.Errorf("status line offers a prefix that cannot work: %q", line)
	}
}

// The picker lists one row per deployment and names the copies beside it; a
// number picks that row.
func TestPickProfile_ListsCopiesOnceAndTakesANumber(t *testing.T) {
	t.Setenv("HOME", t.TempDir())
	clearFacetsEnv(t)
	seedPAT(t, "acme", "https://acme.test", "t1")
	if _, err := credentials.SetDefault("acme"); err != nil {
		t.Fatal(err)
	}
	seedPAT(t, "zed", "https://zed.test", "t2")
	stubOsExit(t)
	origTTY, origLine := stdinIsTTY, readLine
	stdinIsTTY = func() bool { return true }
	readLine = func() (string, error) { return "2", nil }
	t.Cleanup(func() { stdinIsTTY, readLine = origTTY, origLine })

	var buf bytes.Buffer
	got, err := pickProfile(&buf, false, true, "")
	if err != nil || got != "zed" {
		t.Errorf("pickProfile = %q, %v; want zed (row 2: default, zed)", got, err)
	}
	if !skillinstall.MultiProfileMachine() {
		t.Error("two deployments are a multi-profile machine")
	}
	if _, err := credentials.Delete("zed"); err != nil {
		t.Fatal(err)
	}
	if skillinstall.MultiProfileMachine() {
		t.Error("[acme] and its [default] copy are one deployment, not a multi-profile machine")
	}
}

// raptor's env credential outranks every file for both CLIs, so a switch made
// under it is reported as shadowed by CONTROL_PLANE_URL, not silently.
func TestProfilesUse_UnderEnvOverride_ReportsTheShadow(t *testing.T) {
	t.Setenv("HOME", t.TempDir())
	resetProfilesUseFlags(t)
	clearFacetsEnv(t)
	seedPAT(t, "default", "https://d.test", "td")
	seedPAT(t, "acme", "https://acme.test", "ta")
	t.Setenv("CONTROL_PLANE_URL", "https://env.test")
	t.Setenv("FACETS_USERNAME", "env@x")
	t.Setenv("FACETS_TOKEN", "te")
	okAuthMe(t, "u@x")
	stubPostAuthCapture(t)

	out, err := runProfilesUse(t, "acme")
	if err != nil {
		t.Fatalf("RunE err = %v", err)
	}
	got := decodeMap(t, out)
	if got["shadowed_by_env"] != "CONTROL_PLANE_URL=env" || got["effective_profile"] != "env" {
		t.Errorf("payload = %v; want shadowed_by_env CONTROL_PLANE_URL=env, effective_profile env", got)
	}
}
