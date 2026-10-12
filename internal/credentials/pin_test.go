package credentials

import (
	"os"
	"path/filepath"
	"runtime"
	"strings"
	"testing"
)

// cwdAt points the facets-file and pin walks at dir for the test.
func cwdAt(t *testing.T, dir string) {
	t.Helper()
	if err := os.MkdirAll(dir, 0o755); err != nil {
		t.Fatal(err)
	}
	t.Cleanup(SetGetwdForTest(func() (string, error) { return dir, nil }))
}

// A pin selects a profile for its folder and everything below it, behind the
// flag and the environment, and changes nothing outside the folder.
func TestPin_SelectsTheProfileInsideItsFolderOnly(t *testing.T) {
	home := withHome(t)
	seedFacets(t, homeFacets(t), twoRaptorSections)
	repo := filepath.Join(home, "repo")
	if err := os.MkdirAll(repo, 0o755); err != nil {
		t.Fatal(err)
	}
	if err := WritePin(repo, "acme"); err != nil {
		t.Fatal(err)
	}
	if data, _ := os.ReadFile(PinPathIn(repo)); string(data) != "acme\n" {
		t.Errorf("pin file = %q, want the name only", data)
	}
	if data, _ := os.ReadFile(filepath.Join(repo, ".facets", ".gitignore")); string(data) != "*\n" {
		t.Errorf(".facets/.gitignore = %q, want the folder ignored", data)
	}

	cwdAt(t, filepath.Join(repo, "sub"))
	a, err := ResolveActive("")
	if err != nil || a.Name != "acme" || a.Source != SourcePin || !a.Loaded || !strings.HasSuffix(a.PinPath, filepath.Join("repo", ".facets", "profile")) {
		t.Errorf("ResolveActive in the pinned tree = %+v, %v; want acme from the pin", a, err)
	}
	if OnDiskActiveName() != "acme" {
		t.Errorf("OnDiskActiveName() = %q, want the pinned acme", OnDiskActiveName())
	}
	t.Setenv(FacetsEnvProfile, "default")
	if a, _ := ResolveActive(""); a.Name != "default" || a.Source != SourceFacetsEnv {
		t.Errorf("FACETS_PROFILE must beat the pin, got %+v", a)
	}
	if a, _ := ResolveActive("acme"); a.Source != SourceFlag {
		t.Errorf("the flag must beat everything, got %+v", a)
	}
	t.Setenv(FacetsEnvProfile, "")

	cwdAt(t, filepath.Join(home, "elsewhere"))
	if a, _ := ResolveActive(""); a.Name != "default" || a.Source != SourceDefault || a.PinPath != "" {
		t.Errorf("outside the pinned folder = %+v, want [default]", a)
	}
	// The pin is a read: the store itself never changed.
	if h := loadFacets(homeFacets(t)); h["default"].Token != "pat-root" || len(h) != 2 {
		t.Errorf("home store changed: %+v", h)
	}
}

// $HOME/.facets/profile is never a pin: [default] is the machine default.
func TestFindPin_IgnoresHomeAndEmptyFiles(t *testing.T) {
	home := withHome(t)
	seedFacets(t, homeFacets(t), twoRaptorSections)
	seedFacets(t, PinPathIn(home), "acme\n")
	cwdAt(t, filepath.Join(home, "proj"))
	if name, _, ok := FindPin(); ok {
		t.Errorf("FindPin found %q from the home pin; home must be skipped", name)
	}
	seedFacets(t, PinPathIn(filepath.Join(home, "proj")), "\n  \n")
	if _, _, ok := FindPin(); ok {
		t.Error("an empty pin file must not pin anything")
	}
	seedFacets(t, PinPathIn(filepath.Join(home, "proj")), "\n acme \n# note\n")
	if name, _, ok := FindPin(); !ok || name != "acme" {
		t.Errorf("FindPin = %q, %v; want the first non-empty line", name, ok)
	}
}

func TestWritePin_Refusals(t *testing.T) {
	home := withHome(t)
	seedFacets(t, homeFacets(t), twoRaptorSections)
	if err := Put("key", Profile{URL: "https://key.test", Username: "k", Token: "sk"}); err != nil {
		t.Fatal(err)
	}
	repo := filepath.Join(home, "repo")
	for _, tc := range []struct{ dir, name, want string }{
		{home, "acme", "home directory"},
		{repo, "ghost", "does not exist"},
		{repo, "key", "Praxis API key"},
		{repo, "bad name", "invalid profile name"},
	} {
		err := WritePin(tc.dir, tc.name)
		if err == nil || !strings.Contains(err.Error(), tc.want) {
			t.Errorf("WritePin(%s, %q) = %v, want an error about %q", tc.dir, tc.name, err, tc.want)
		}
	}
	if _, err := os.Stat(PinPathIn(repo)); !os.IsNotExist(err) {
		t.Error("a refused pin must write nothing")
	}
}

func TestRemovePin_RemovesTheNearestPin(t *testing.T) {
	home := withHome(t)
	seedFacets(t, homeFacets(t), twoRaptorSections)
	repo := filepath.Join(home, "repo")
	if err := os.MkdirAll(repo, 0o755); err != nil {
		t.Fatal(err)
	}
	if err := WritePin(repo, "acme"); err != nil {
		t.Fatal(err)
	}
	cwdAt(t, filepath.Join(repo, "deep", "er"))
	path, err := RemovePin()
	if err != nil || path != PinPathIn(repo) {
		t.Errorf("RemovePin = %q, %v; want %s", path, err, PinPathIn(repo))
	}
	if path, err := RemovePin(); path != "" || err != nil {
		t.Errorf("second RemovePin = %q, %v; want nothing to remove", path, err)
	}
}

// login --local writes a PAT where the tree reads it: a legacy tree file when
// one exists at or above the folder, else the home file.
func TestPutFor_WritesWhereTheTreeReads(t *testing.T) {
	home := withHome(t)
	seedFacets(t, homeFacets(t), twoRaptorSections)
	plain := filepath.Join(home, "plain")
	got, err := PutFor(plain, "new", FacetsProfile("https://new.test", "u@x", "pat-new"))
	if err != nil || got != homeFacets(t) {
		t.Errorf("PutFor without a tree file = %q, %v; want the home file", got, err)
	}
	legacy := filepath.Join(home, "legacy")
	seedFacets(t, FacetsPathIn(legacy), "[default]\ncontrol_plane_url = https://t.test\nusername = u@x\ntoken = pat-t\n")
	got, err = PutFor(filepath.Join(legacy, "sub"), "new", FacetsProfile("https://new.test", "u@x", "pat-new"))
	if err != nil || got != FacetsPathIn(legacy) {
		t.Errorf("PutFor in a legacy tree = %q, %v; want the tree file", got, err)
	}
	if loadFacets(FacetsPathIn(legacy))["new"].Token != "pat-new" {
		t.Error("the tree file should hold the new section")
	}
}

// A pin file holds no secret, but the credentials beside it are 0600.
func TestWritePin_ModesOnUnix(t *testing.T) {
	if runtime.GOOS == "windows" {
		t.Skip("Windows reports no Unix modes")
	}
	home := withHome(t)
	seedFacets(t, homeFacets(t), twoRaptorSections)
	repo := filepath.Join(home, "repo")
	if err := WritePin(repo, "acme"); err != nil {
		t.Fatal(err)
	}
	if fi, err := os.Stat(filepath.Join(repo, ".facets")); err != nil || fi.Mode().Perm() != 0o700 {
		t.Errorf(".facets mode = %v, %v; want 0700", fi.Mode().Perm(), err)
	}
}

// The home skip holds when the working directory is spelled through the
// resolved home (macOS: HOME=/var/..., cwd=/private/var/...).
func TestFindPin_IgnoresHomeInEitherSpelling(t *testing.T) {
	home := withHome(t)
	seedFacets(t, homeFacets(t), twoRaptorSections)
	seedFacets(t, PinPathIn(home), "acme\n")
	cwdAt(t, filepath.Join(resolvedDir(home), "proj"))
	if name, _, ok := FindPin(); ok {
		t.Errorf("FindPin found %q through the resolved home", name)
	}
}

// PutDefault keeps an unduplicated [default] for another identity, but a
// re-login of the same identity with a new token replaces it in place.
func TestPutDefault_KeepsOnlyAnotherIdentity(t *testing.T) {
	withHome(t)
	if err := Put("default", FacetsProfile("https://root.test", "u@x", "t1")); err != nil {
		t.Fatal(err)
	}
	if kept, err := PutDefault(FacetsProfile("https://root.test", "u@x", "t2")); err != nil || kept != "" {
		t.Errorf("rotated token: kept %q, %v; want nothing kept", kept, err)
	}
	kept, err := PutDefault(FacetsProfile("https://acme.test", "u@x", "ta"))
	if err != nil || kept != "root" {
		t.Fatalf("another control plane: kept %q, %v; want root", kept, err)
	}
	if h := loadFacets(homeFacets(t)); h["root"].Token != "t2" || h["default"].Token != "ta" {
		t.Errorf("home store = %+v", h)
	}
}

// A pin never reaches git: a new .facets/.gitignore ignores the folder, and an
// older one that ignores only `credentials` gets a `profile` line. Lines that
// already ignore the pin, and every other line, stay as they are.
func TestWritePin_KeepsThePinOutOfGit(t *testing.T) {
	home := withHome(t)
	seedFacets(t, homeFacets(t), twoRaptorSections)
	for _, tc := range []struct{ name, before, want string }{
		{"absent", "", "*\n"},
		{"older file", "credentials\n", "credentials\nprofile\n"},
		{"no final newline", "credentials", "credentials\nprofile\n"},
		{"already ignores all", "credentials\n*\n", "credentials\n*\n"},
		{"already ignores the pin", "/profile\n", "/profile\n"},
	} {
		repo := filepath.Join(home, tc.name)
		gi := filepath.Join(repo, ".facets", ".gitignore")
		if tc.before != "" {
			seedFacets(t, gi, tc.before)
		}
		if err := WritePin(repo, "acme"); err != nil {
			t.Fatal(err)
		}
		if b, _ := os.ReadFile(gi); string(b) != tc.want {
			t.Errorf("%s: .gitignore = %q, want %q", tc.name, b, tc.want)
		}
	}
}

func TestIdentities_CountsHostAndUser(t *testing.T) {
	store := map[string]Profile{
		"default": FacetsProfile("https://root.test", "u@x", "new"),
		"root":    FacetsProfile("https://ROOT.test/", "u@x", "old"),
		"key":     {URL: "https://root.test", Username: "u@x", Token: "sk"},
		"other":   FacetsProfile("https://root.test", "v@x", "t"),
		"acme":    FacetsProfile("https://acme.test", "u@x", "t"),
	}
	if n := Identities(store); n != 3 {
		t.Errorf("Identities = %d, want 3 (root/u, root/v, acme/u)", n)
	}
}
