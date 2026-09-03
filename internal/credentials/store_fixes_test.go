package credentials

import (
	"os"
	"path/filepath"
	"strings"
	"testing"
)

// inTree points both discovery walks at dir.
func inTree(t *testing.T, dir string) {
	t.Helper()
	setCwd(t, dir)
	t.Cleanup(SetGetwdForTest(func() (string, error) { return dir, nil }))
}

// A raptor section that holds other credentials is never overwritten by the
// migration: the praxis copy moves under a name derived from its host.
func TestMigrateLegacyPATs_NeverOverwritesARaptorSection(t *testing.T) {
	home := withHome(t)
	inTree(t, home)
	if err := os.MkdirAll(filepath.Join(home, ".praxis"), 0o700); err != nil {
		t.Fatal(err)
	}
	praxisFile := filepath.Join(home, ".praxis", "credentials")
	if err := os.WriteFile(praxisFile, []byte("[default]\nurl = https://b.test\nusername = u\ntoken = T2\nauth_mode = basic\n"), 0o600); err != nil {
		t.Fatal(err)
	}
	seedFacets(t, homeFacets(t), "[default]\ncontrol_plane_url = https://c.test\nusername = u\ntoken = T1\n")

	moved, err := MigrateLegacyPATs()
	if err != nil {
		t.Fatal(err)
	}
	if len(moved) != 1 || moved[0] != "default→b" {
		t.Errorf("moved = %v, want [default→b]", moved)
	}
	facets := loadFacets(homeFacets(t))
	if facets["default"].URL != "https://c.test" || facets["default"].Token != "T1" {
		t.Errorf("raptor's [default] was overwritten: %+v", facets["default"])
	}
	if facets["b"].Token != "T2" {
		t.Errorf("praxis copy not kept: %+v", facets)
	}
	if praxis, _ := loadPraxis(); len(praxis) != 0 {
		t.Errorf("praxis file still holds %v", praxis)
	}
}

// One unusable legacy section does not block the others.
func TestMigrateLegacyPATs_ContinuesPastABadSection(t *testing.T) {
	home := withHome(t)
	inTree(t, home)
	if err := os.MkdirAll(filepath.Join(home, ".praxis"), 0o700); err != nil {
		t.Fatal(err)
	}
	body := "[acme]\nurl = https://acme.test\ntoken = T1\nauth_mode = basic\n\n[zed]\nurl = https://zed.test\nusername = u\ntoken = T2\nauth_mode = basic\n"
	if err := os.WriteFile(filepath.Join(home, ".praxis", "credentials"), []byte(body), 0o600); err != nil {
		t.Fatal(err)
	}
	moved, err := MigrateLegacyPATs()
	if err == nil || !strings.Contains(err.Error(), `"acme"`) {
		t.Errorf("err = %v, want the acme failure reported", err)
	}
	if len(moved) != 1 || moved[0] != "zed" {
		t.Errorf("moved = %v, want [zed]", moved)
	}
	if loadFacets(homeFacets(t))["zed"].Token != "T2" {
		t.Error("zed was not moved")
	}
	praxis, _ := loadPraxis()
	if _, still := praxis["acme"]; !still {
		t.Error("the unusable acme section must stay in the praxis file")
	}
}

// Inside a local tree, Delete removes the tree's section only: the praxis
// file is global and may hold a same-named API key the tree never saw.
func TestDelete_InsideATree_LeavesTheGlobalPraxisFileAlone(t *testing.T) {
	home := withHome(t)
	repo := filepath.Join(home, "repo")
	if err := Put("default", Profile{URL: "https://home.test", Username: "ci", Token: "sk_home"}); err != nil {
		t.Fatal(err)
	}
	seedFacets(t, FacetsPathIn(repo), "[default]\ncontrol_plane_url = https://tree.test\nusername = u\ntoken = T\n")
	inTree(t, repo)

	d, err := Delete("default")
	if err != nil {
		t.Fatal(err)
	}
	if !d.Facets || d.Praxis || d.FacetsPath != FacetsPathIn(repo) {
		t.Errorf("Deleted = %+v", d)
	}
	if praxis, _ := loadPraxis(); praxis["default"].Token != "sk_home" {
		t.Errorf("global API key deleted by a logout inside a tree: %v", praxis)
	}
}

// A re-login with a rotated token replaces the [default] copy; it does not
// keep the stale copy under a synthetic name.
func TestSetDefault_RotatedTokenDoesNotKeepTheStaleCopy(t *testing.T) {
	home := withHome(t)
	inTree(t, home)
	seedFacets(t, homeFacets(t), "[acme]\ncontrol_plane_url = https://acme.test\nusername = u\ntoken = T1\n\n[default]\ncontrol_plane_url = https://acme.test\nusername = u\ntoken = T1\n")
	if err := Put("acme", FacetsProfile("https://acme.test", "u", "T2")); err != nil {
		t.Fatal(err)
	}
	kept, err := SetDefault("acme")
	if err != nil {
		t.Fatal(err)
	}
	if kept != "" {
		t.Errorf("kept = %q; a rotated token is the same profile, not a displaced default", kept)
	}
	facets := loadFacets(homeFacets(t))
	if len(facets) != 2 || facets["default"].Token != "T2" {
		t.Errorf("facets = %+v", facets)
	}
}

// raptor accepts http:// control planes, so those PATs are shared too; only
// a loopback URL (a developer's agent server) stays in the praxis file.
func TestIsFacetsCredential_HTTPAndLoopback(t *testing.T) {
	cases := map[string]bool{
		"https://acme.test":      true,
		"http://cp.internal":     true,
		"http://localhost:8000":  false,
		"http://127.0.0.1:8000":  false,
		"https://localhost:8443": false,
		"acme.test":              false,
	}
	for url, want := range cases {
		if got := isFacetsCredential(FacetsProfile(url, "u", "t")); got != want {
			t.Errorf("isFacetsCredential(%q) = %v, want %v", url, got, want)
		}
	}
	if isFacetsCredential(Profile{URL: "https://acme.test", Token: "sk"}) {
		t.Error("an API key is never a facets credential")
	}
}

// Writing a praxis-file profile keeps a raptor section of the same name that
// belongs to ANOTHER control plane, under a host-derived name; one for the
// same control plane is this deployment re-logged in and goes.
func TestPut_PraxisProfileKeepsRaptorsForeignSection(t *testing.T) {
	home := withHome(t)
	inTree(t, home)
	seedFacets(t, homeFacets(t), "[default]\ncontrol_plane_url = https://acme.test\nusername = u\ntoken = T1\n")
	if err := Put("default", FacetsProfile("http://localhost:5000", "u", "L")); err != nil {
		t.Fatal(err)
	}
	facets := loadFacets(homeFacets(t))
	if _, still := facets["default"]; still {
		t.Error("[default] must be released to the praxis-file profile")
	}
	if facets["acme"].Token != "T1" {
		t.Errorf("raptor's section for another control plane was lost: %+v", facets)
	}
	if err := Put("acme", Profile{URL: "https://acme.test", Username: "u", Token: "sk"}); err != nil {
		t.Fatal(err)
	}
	if _, still := loadFacets(homeFacets(t))["acme"]; still {
		t.Error("a same-host re-login must replace the raptor section")
	}
}

// logout --all inside a tree removes the tree's file too, so the tree cannot
// stay logged in after "Removed all profiles".
func TestDeleteAll_InsideATree_RemovesTheTreeFile(t *testing.T) {
	home := withHome(t)
	repo := filepath.Join(home, "repo")
	seedFacets(t, homeFacets(t), "[default]\ncontrol_plane_url = https://home.test\nusername = u\ntoken = T\n")
	seedFacets(t, FacetsPathIn(repo), "[default]\ncontrol_plane_url = https://tree.test\nusername = u\ntoken = T\n")
	inTree(t, repo)
	if err := DeleteAll(); err != nil {
		t.Fatal(err)
	}
	for _, f := range []string{homeFacets(t), FacetsPathIn(repo)} {
		if _, err := os.Stat(f); !os.IsNotExist(err) {
			t.Errorf("%s survived DeleteAll", f)
		}
	}
}

// Identical credentials under two names count once.
func TestDistinct_CollapsesCopies(t *testing.T) {
	store := map[string]Profile{
		"default": FacetsProfile("https://acme.test", "u", "T"),
		"acme":    FacetsProfile("https://acme.test", "u", "T"),
		"zed":     FacetsProfile("https://zed.test", "u", "Z"),
	}
	got := Distinct(store)
	if len(got) != 2 || got[0] != "default" || got[1] != "zed" {
		t.Errorf("Distinct = %v, want [default zed]", got)
	}
}
