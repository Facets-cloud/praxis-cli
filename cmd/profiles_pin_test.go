package cmd

import (
	"bytes"
	"os"
	"path/filepath"
	"strings"
	"testing"

	"github.com/Facets-cloud/praxis-cli/internal/credentials"
	"github.com/Facets-cloud/praxis-cli/internal/exitcode"
	"github.com/Facets-cloud/praxis-cli/internal/paths"
	"github.com/Facets-cloud/praxis-cli/internal/skillinstall"
)

func runPinCmd(t *testing.T, args ...string) (string, error) {
	t.Helper()
	cmd := profilesUnpinCmd
	if len(args) > 0 {
		cmd = profilesPinCmd
	}
	var buf bytes.Buffer
	cmd.SetOut(&buf)
	t.Cleanup(func() { cmd.SetOut(nil); profilesPinJSON = false })
	profilesPinJSON = true
	err := cmd.RunE(cmd, args)
	return buf.String(), err
}

// `profiles pin` writes the name, never a token, at the project tree, and
// `unpin` removes it; neither touches the store or the default.
func TestProfilesPinAndUnpin(t *testing.T) {
	home := t.TempDir()
	t.Setenv("HOME", home)
	clearFacetsEnv(t)
	setRootProfile(t, "")
	seedPAT(t, "default", "https://d.test", "td")
	seedPAT(t, "acme", "https://acme.test", "ta")
	repo := repoUnderHome(t, home)
	inDir(t, repo)

	out, err := runPinCmd(t, "acme")
	if err != nil {
		t.Fatal(err)
	}
	if got := decodeMap(t, out); got["pin_path"] != credentials.PinPathIn(repo) {
		t.Errorf("pin output = %v", got)
	}
	if b, _ := os.ReadFile(filepath.Join(repo, ".praxis", ".gitignore")); string(b) != "*\n" {
		t.Errorf(".praxis/.gitignore = %q, want everything ignored", b)
	}
	if a, _ := credentials.ResolveActive(""); a.Name != "acme" || a.Source != credentials.SourcePin {
		t.Errorf("after pin = %+v, want acme from the pin", a)
	}
	if got := homeFacetsURL(t); got != "https://d.test" {
		t.Errorf("home default = %q; a pin must not move it", got)
	}
	// From a subfolder the pin goes to the existing tree, not a nested one.
	sub := filepath.Join(repo, "sub")
	inDir(t, sub)
	if _, err := runPinCmd(t, "default"); err != nil {
		t.Fatal(err)
	}
	if _, err := os.Stat(credentials.PinPathIn(sub)); !os.IsNotExist(err) {
		t.Error("a pin from a subfolder must re-pin the tree, not nest a new one")
	}
	out, err = runPinCmd(t)
	if err != nil || decodeMap(t, out)["removed"] != credentials.PinPathIn(repo) {
		t.Errorf("unpin = %q, %v", out, err)
	}
	if a, _ := credentials.ResolveActive(""); a.Source != credentials.SourceDefault {
		t.Errorf("after unpin = %+v, want the default", a)
	}
}

func TestProfilesPin_RefusesUnknownAndHome(t *testing.T) {
	home := t.TempDir()
	t.Setenv("HOME", home)
	clearFacetsEnv(t)
	setRootProfile(t, "")
	seedPAT(t, "default", "https://d.test", "td")
	code := stubOsExit(t)

	inDir(t, filepath.Join(home, "repo"))
	if _, err := runPinCmd(t, "ghost"); err == nil || *code != exitcode.Usage {
		t.Errorf("pin of an unknown profile: err %v, code %d; want exit 2", err, *code)
	}
	*code = 0
	inDir(t, home)
	if _, err := runPinCmd(t, "default"); err == nil || *code != exitcode.Usage {
		t.Errorf("pin in home: err %v, code %d; want exit 2", err, *code)
	}
}

// A global login of a named profile saves credentials only: the default and
// the user-level org skills stay where they are.
func TestLogin_NamedProfile_LeavesDefaultAndSkillsAlone(t *testing.T) {
	isolateHome(t)
	resetLoginFlags(t)
	clearFacetsEnv(t)
	seedPAT(t, "default", "https://root.test", "tr")
	seedPAT(t, "acme", "https://acme.test", "ta")
	stubAuthMeOK(t)
	call := stubPostAuthCapture(t)

	rootProfile = "acme"
	loginJSON = true
	out, err := runLoginRunE(t)
	if err != nil {
		t.Fatal(err)
	}
	got := decodeMap(t, out)
	if got["skills_synced"] != false || !strings.Contains(got["hint"].(string), "profiles set-default acme") {
		t.Errorf("payload = %v; want credentials only and the hint", got)
	}
	if call.count != 0 {
		t.Errorf("postAuthSetup ran %d times; a named login must not touch the org skills", call.count)
	}
	if homeFacetsURL(t) != "https://root.test" {
		t.Error("a named login must not move [default]")
	}
}

// A re-login of the profile [default] copies keeps the copy current and
// re-syncs, so an older machine whose [default] is a copy does not split.
func TestLogin_ProfileThatDefaultCopies_RefreshesTheCopy(t *testing.T) {
	isolateHome(t)
	resetLoginFlags(t)
	clearFacetsEnv(t)
	seedPAT(t, "acme", "https://acme.test", "old")
	seedPAT(t, "default", "https://acme.test", "old")
	seedPAT(t, "zed", "https://zed.test", "tz")
	driveInteractivePAT(t, "u@x", "new")
	stubAuthMeOK(t)
	call := stubPostAuthCapture(t)

	rootProfile = "acme"
	loginURL = "https://acme.test"
	loginForce = true
	if _, err := runLoginRunE(t); err != nil {
		t.Fatal(err)
	}
	store, _ := credentials.LoadHome()
	if store["acme"].Token != "new" || store["default"].Token != "new" {
		t.Errorf("acme/default tokens = %q/%q, want both new", store["acme"].Token, store["default"].Token)
	}
	if call.count != 1 {
		t.Errorf("postAuthSetup ran %d times, want the re-sync", call.count)
	}
}

func TestImplicitProfileNote(t *testing.T) {
	isolateHome(t)
	clearFacetsEnv(t)
	seedPAT(t, "default", "https://root.test", "tr")
	active, _ := credentials.ResolveActive("")
	if note := implicitProfileNote(active, "claude-code"); note != "" {
		t.Errorf("one profile: note %q, want none", note)
	}
	seedPAT(t, "acme", "https://acme.test", "ta")
	active, _ = credentials.ResolveActive("")
	if note := implicitProfileNote(active, ""); note != "" {
		t.Errorf("a person: note %q, want none", note)
	}
	if note := implicitProfileNote(active, "claude-code"); !strings.Contains(note, `using profile "default"`) || !strings.Contains(note, "--profile") {
		t.Errorf("agent, two profiles, no selection: note %q", note)
	}
	flagged, _ := credentials.ResolveActive("acme")
	if note := implicitProfileNote(flagged, "claude-code"); note != "" {
		t.Errorf("an explicit selection needs no note, got %q", note)
	}
}

func TestOrgSkillsNote(t *testing.T) {
	home := t.TempDir()
	t.Setenv("HOME", home)
	clearFacetsEnv(t)
	seedPAT(t, "default", "https://root.test", "tr")
	seedPAT(t, "acme", "https://acme.test", "ta")
	inDir(t, home)
	acme, _ := credentials.ResolveActive("acme")
	if note := orgSkillsNote(acme); note != "" {
		t.Errorf("no recorded org: note %q, want none", note)
	}
	restore := paths.OverrideActiveRoot(filepath.Join(home, ".praxis"))
	if err := skillinstall.SetOrg("default", "https://root.test"); err != nil {
		t.Fatal(err)
	}
	restore()
	root, _ := credentials.ResolveActive("")
	if note := orgSkillsNote(root); note != "" {
		t.Errorf("the installed org: note %q, want none", note)
	}
	if note := orgSkillsNote(acme); !strings.Contains(note, `for profile "default"`) || !strings.Contains(note, "profiles pin acme") ||
		!strings.Contains(note, "profiles set-default acme") {
		t.Errorf("a --profile selection from another org: note %q, want pin or set-default", note)
	}
	// A pinned project with no org of its own falls back to the user level.
	repo := repoUnderHome(t, home)
	if err := credentials.WritePin(repo, "acme"); err != nil {
		t.Fatal(err)
	}
	inDir(t, repo)
	if org, ok := installedOrg(); !ok || org.Profile != "default" {
		t.Errorf("installedOrg in a bare pinned project = %+v, %v; want the user-level default", org, ok)
	}
}

func TestEnvConflictNote(t *testing.T) {
	t.Setenv(credentials.EnvProfile, "acme")
	t.Setenv(credentials.FacetsEnvProfile, "acme")
	if note := envConflictNote(); note != "" {
		t.Errorf("same name: %q", note)
	}
	t.Setenv(credentials.FacetsEnvProfile, "zed")
	if note := envConflictNote(); !strings.Contains(note, "using acme") {
		t.Errorf("conflict: %q, want PRAXIS_PROFILE to win", note)
	}
}

func TestNoCredentials_NamesThePinAndTheProfiles(t *testing.T) {
	isolateHome(t)
	seedPAT(t, "acme", "https://acme.test", "ta")
	msg, hint := noCredentials(credentials.Active{Name: "ghost", PinPath: "/r/.facets/profile"})
	if !strings.Contains(msg, "pinned by /r/.facets/profile") || !strings.Contains(hint, "one of: acme") {
		t.Errorf("msg %q, hint %q", msg, hint)
	}
}

func TestHostLabel(t *testing.T) {
	for in, want := range map[string]string{
		"https://acme.console.facets.cloud":  "acme",
		"https://default.test":               "",
		"not a url":                          "",
		"https://1489352795.facetsapp.cloud": "1489352795",
	} {
		if got := hostLabel(in); got != want {
			t.Errorf("hostLabel(%q) = %q, want %q", in, got, want)
		}
	}
}

// The listing names the selection's source, the pin, and the copies.
func TestProfilesCmd_ShowsSourcePinAndCopies(t *testing.T) {
	home := t.TempDir()
	t.Setenv("HOME", home)
	resetProfilesFlags()
	clearFacetsEnv(t)
	setRootProfile(t, "")
	seedPAT(t, "acme", "https://acme.test", "ta")
	seedPAT(t, "default", "https://acme.test", "ta")
	seedPAT(t, "zed", "https://zed.test", "tz")
	repo := repoUnderHome(t, home)
	if err := credentials.WritePin(repo, "zed"); err != nil {
		t.Fatal(err)
	}
	inDir(t, repo)

	var buf bytes.Buffer
	profilesCmd.SetOut(&buf)
	profilesJSON = true
	if err := profilesCmd.RunE(profilesCmd, nil); err != nil {
		t.Fatal(err)
	}
	out := decodeProfiles(t, buf.Bytes())
	if out.ActiveProfile != "zed" || out.ActiveSource != "pin" || out.PinPath != credentials.PinPathIn(repo) {
		t.Errorf("active = %s/%s/%s", out.ActiveProfile, out.ActiveSource, out.PinPath)
	}
	if p, _ := findProfile(out, "acme"); strings.Join(p.SameAs, ",") != "default" {
		t.Errorf("acme same_as = %v, want [default]", p.SameAs)
	}

	buf.Reset()
	if err := renderProfilesText(&buf, out); err != nil {
		t.Fatal(err)
	}
	if !strings.Contains(buf.String(), "Active here: zed (pin "+credentials.PinPathIn(repo)+")") || !strings.Contains(buf.String(), "SAME AS") {
		t.Errorf("text listing:\n%s", buf.String())
	}
}

// When [default] moved to another org (raptor set-default, a hand edit) the
// fix is a global refresh, and the note names sections, not "default".
func TestOrgSkillsNote_DefaultMovedToAnotherOrg(t *testing.T) {
	home := t.TempDir()
	t.Setenv("HOME", home)
	clearFacetsEnv(t)
	seedPAT(t, "root", "https://root.test", "tr")
	seedPAT(t, "acme", "https://acme.test", "ta")
	seedPAT(t, "default", "https://acme.test", "ta") // [default] now copies acme
	inDir(t, home)
	restore := paths.OverrideActiveRoot(filepath.Join(home, ".praxis"))
	if err := skillinstall.SetOrg("default", "https://root.test"); err != nil {
		t.Fatal(err)
	}
	restore()

	active, _ := credentials.ResolveActive("")
	note := orgSkillsNote(active)
	for _, want := range []string{`for profile "root"`, `not "acme"`, "Run `praxis refresh-skills` to install"} {
		if !strings.Contains(note, want) {
			t.Errorf("note %q lacks %q", note, want)
		}
	}
	if strings.Contains(note, "profiles pin") || strings.Contains(note, `"default"`) {
		t.Errorf("note %q must not suggest a pin or name the default section", note)
	}

	// In a pinned folder the fix is a refresh in that folder.
	repo := repoUnderHome(t, home)
	if err := credentials.WritePin(repo, "acme"); err != nil {
		t.Fatal(err)
	}
	inDir(t, repo)
	pinned, _ := credentials.ResolveActive("")
	if note := orgSkillsNote(pinned); !strings.Contains(note, "Run `praxis refresh-skills` in "+repo) {
		t.Errorf("pinned note %q, want a refresh in %s", note, repo)
	}
}

// The guard's "replace [default]" answer (pickProfile returns "default", see
// TestPickProfile_WhenItStepsIn) must not lose the old default: a [default]
// that no other section holds is kept under its host label first.
func TestLogin_ReplacingDefault_KeepsTheOldOne(t *testing.T) {
	isolateHome(t)
	clearFacetsEnv(t)
	seedPAT(t, "default", "https://root.test", "old-root")
	call := stubPostAuthCapture(t)

	var buf bytes.Buffer
	if err := persistAndSetup(&buf, true, "default", credentials.FacetsProfile("https://new.test", "u@x", "new"), "u@x", false); err != nil {
		t.Fatal(err)
	}
	store, _ := credentials.LoadHome()
	if store["default"].URL != "https://new.test" || store["default"].Token != "new" {
		t.Errorf("[default] = %+v, want the new control plane", store["default"])
	}
	if store["root"].Token != "old-root" || store["root"].URL != "https://root.test" {
		t.Errorf("[root] = %+v, want the old default kept with its token", store["root"])
	}
	if call.count != 1 {
		t.Errorf("postAuthSetup ran %d times; the new default re-syncs", call.count)
	}
}

// A --local login named "default" on the home store keeps the old default too.
func TestPutFor_DefaultKeepsTheOldDefault(t *testing.T) {
	home := t.TempDir()
	t.Setenv("HOME", home)
	seedPAT(t, "default", "https://root.test", "old-root")
	if _, err := credentials.PutFor(filepath.Join(home, "repo"), "default", credentials.FacetsProfile("https://acme.test", "u@x", "ta")); err != nil {
		t.Fatal(err)
	}
	store, _ := credentials.LoadHome()
	if store["root"].Token != "old-root" || store["default"].Token != "ta" {
		t.Errorf("store = %+v, want root kept and default replaced", store)
	}
}
