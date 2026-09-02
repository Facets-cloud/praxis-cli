package credentials

import (
	"os"
	"path/filepath"
	"strings"
	"testing"

	"github.com/Facets-cloud/praxis-cli/internal/paths"
)

func withHome(t *testing.T) string {
	t.Helper()
	home := t.TempDir()
	t.Setenv("HOME", home)
	return home
}

func TestResolveActive_DefaultWhenNothingSet(t *testing.T) {
	withHome(t)
	a, err := ResolveActive("")
	if err != nil {
		t.Fatal(err)
	}
	if a.Name != "default" || a.Source != SourceDefault {
		t.Errorf("got name=%q source=%s; want default/default", a.Name, a.Source)
	}
	if a.Loaded {
		t.Errorf("Loaded should be false on empty store")
	}
}

func TestResolveActive_FlagWinsAll(t *testing.T) {
	withHome(t)
	if err := Put("default", Profile{URL: "https://d.test", Token: "t"}); err != nil {
		t.Fatal(err)
	}
	a, err := ResolveActive("from-flag")
	if err != nil {
		t.Fatal(err)
	}
	if a.Name != "from-flag" || a.Source != SourceFlag {
		t.Errorf("flag should win, got %+v", a)
	}
}

func TestResolveActive_LoadedTrueWhenProfileExists(t *testing.T) {
	withHome(t)
	if err := Put("default", Profile{URL: "https://x.test", Username: "x@x", Token: "t"}); err != nil {
		t.Fatal(err)
	}
	a, err := ResolveActive("")
	if err != nil {
		t.Fatal(err)
	}
	if !a.Loaded {
		t.Errorf("Loaded should be true after Put('default', …)")
	}
	if a.Profile.URL != "https://x.test" {
		t.Errorf("Profile.URL = %q, want https://x.test", a.Profile.URL)
	}
}

func TestPutLoadGet_RoundTrip(t *testing.T) {
	withHome(t)
	want := Profile{URL: "https://acme.test", Username: "support@acme.com", Token: "sk_live_abc"}
	if err := Put("acme", want); err != nil {
		t.Fatal(err)
	}
	store, err := Load()
	if err != nil {
		t.Fatal(err)
	}
	got, ok := store["acme"]
	if !ok {
		t.Fatal("acme profile missing after Put")
	}
	want.Store = StorePraxis // an API key lives in the praxis file
	if got != want {
		t.Errorf("round-trip mismatch: got %+v want %+v", got, want)
	}
}

func TestAuth(t *testing.T) {
	cases := []struct {
		name string
		prof Profile
		want map[string]string
	}{
		{"bearer default mode", Profile{Username: "u@x", Token: "sk_live_abc"}, map[string]string{"Authorization": "Bearer sk_live_abc"}},
		{"bearer explicit non-basic mode", Profile{Username: "u@x", Token: "sk_live_abc", AuthMode: "bearer"}, map[string]string{"Authorization": "Bearer sk_live_abc"}},
		{"facets/basic mode", Profile{Username: "user@corp", Token: "pat123", AuthMode: "basic"}, map[string]string{"Authorization": "Bearer pat123", "X-Facets-Username": "user@corp"}},
		{"empty token", Profile{Username: "u@x", AuthMode: "basic"}, nil},
	}
	for _, c := range cases {
		t.Run(c.name, func(t *testing.T) {
			got := c.prof.Auth()
			if len(got) != len(c.want) {
				t.Fatalf("Auth() = %v; want %v", got, c.want)
			}
			for k, v := range c.want {
				if got[k] != v {
					t.Errorf("Auth()[%q] = %q; want %q", k, got[k], v)
				}
			}
		})
	}
}

func TestAuthMode_RoundTripsThroughINI(t *testing.T) {
	withHome(t)
	// An https PAT lands in the shared store, where AuthMode is implied.
	want := Profile{URL: "https://cp.test", Username: "user@corp", Token: "pat123", AuthMode: "basic"}
	if err := Put("facets", want); err != nil {
		t.Fatal(err)
	}
	store, err := Load()
	if err != nil {
		t.Fatal(err)
	}
	got, ok := store["facets"]
	if !ok {
		t.Fatal("facets profile missing after Put")
	}
	want.Store = StoreFacets
	if got != want {
		t.Errorf("round-trip mismatch: got %+v want %+v", got, want)
	}
	// A loopback PAT stays in the praxis file, so auth_mode must persist there;
	// a profile with no AuthMode must NOT emit an auth_mode line.
	_ = Put("dev", Profile{URL: "http://localhost:8000", Username: "user@corp", Token: "pat", AuthMode: "basic"})
	_ = Put("bearer", Profile{URL: "https://x.test", Token: "sk"})
	path, _ := paths.Credentials()
	raw, _ := os.ReadFile(path)
	if strings.Contains(string(raw), "auth_mode = \n") || strings.Contains(string(raw), "auth_mode = bearer") {
		t.Errorf("empty AuthMode should be omitted from INI; got:\n%s", raw)
	}
	if !strings.Contains(string(raw), "auth_mode = basic") {
		t.Errorf("basic AuthMode not persisted for the loopback PAT; got:\n%s", raw)
	}
	if store, _ = Load(); store["dev"].AuthMode != "basic" || store["dev"].Store != StorePraxis {
		t.Errorf("loopback PAT = %+v", store["dev"])
	}
}

func TestPut_AddsSecondProfileWithoutClobberingFirst(t *testing.T) {
	withHome(t)
	if err := Put("default", Profile{URL: "https://default.test", Username: "a@x", Token: "t1"}); err != nil {
		t.Fatal(err)
	}
	if err := Put("acme", Profile{URL: "https://acme.test", Username: "b@x", Token: "t2"}); err != nil {
		t.Fatal(err)
	}
	store, _ := Load()
	if len(store) != 2 {
		t.Errorf("expected 2 profiles, got %d", len(store))
	}
	if store["default"].Token != "t1" || store["acme"].Token != "t2" {
		t.Errorf("Put clobbered profiles: %+v", store)
	}
}

func TestDelete_RemovesEntry(t *testing.T) {
	withHome(t)
	_ = Put("default", Profile{URL: "x", Token: "t"})
	_ = Put("acme", Profile{URL: "y", Token: "t"})
	if _, err := Delete("acme"); err != nil {
		t.Fatal(err)
	}
	store, _ := Load()
	if _, ok := store["acme"]; ok {
		t.Errorf("acme should be gone after Delete")
	}
	if _, ok := store["default"]; !ok {
		t.Errorf("default should remain after deleting acme")
	}
}

func TestDelete_NonExistent_NoError(t *testing.T) {
	withHome(t)
	if _, err := Delete("never-there"); err != nil {
		t.Errorf("delete of non-existent profile should not error: %v", err)
	}
}

func TestDeleteAll_AlsoClearsActivePointer(t *testing.T) {
	withHome(t)
	_ = Put("default", Profile{URL: "x", Token: "t"})
	legacy, _ := paths.LegacyConfig()
	if err := os.WriteFile(legacy, []byte("[default]\nprofile = default\n"), 0o600); err != nil {
		t.Fatal(err)
	}

	if err := DeleteAll(); err != nil {
		t.Fatal(err)
	}

	// Both files should be gone.
	home, _ := os.UserHomeDir()
	if _, err := os.Stat(filepath.Join(home, ".praxis", "credentials")); !os.IsNotExist(err) {
		t.Errorf("credentials file still exists after DeleteAll")
	}
	if _, err := os.Stat(filepath.Join(home, ".praxis", "config.json")); !os.IsNotExist(err) {
		t.Errorf("config.json file still exists after DeleteAll")
	}
	a, _ := ResolveActive("")
	if a.Source != SourceDefault {
		t.Errorf("after DeleteAll, source should fall back to default; got %s", a.Source)
	}
}

func TestList_DefaultFirstThenAlphabetical(t *testing.T) {
	withHome(t)
	for _, name := range []string{"vymo", "acme", "default", "refold"} {
		_ = Put(name, Profile{URL: "x"})
	}
	got, err := List()
	if err != nil {
		t.Fatal(err)
	}
	want := []string{"default", "acme", "refold", "vymo"}
	if len(got) != len(want) {
		t.Fatalf("got %d profiles, want %d", len(got), len(want))
	}
	for i := range got {
		if got[i] != want[i] {
			t.Errorf("got[%d] = %q, want %q", i, got[i], want[i])
		}
	}
}

func TestSave_FilePerm0600(t *testing.T) {
	withHome(t)
	_ = Put("default", Profile{URL: "x", Token: "t"})
	home, _ := os.UserHomeDir()
	info, err := os.Stat(filepath.Join(home, ".praxis", "credentials"))
	if err != nil {
		t.Fatal(err)
	}
	if mode := info.Mode().Perm(); mode != 0600 {
		t.Errorf("perm = %o, want 0600", mode)
	}
}

func TestINI_FormatMatchesFacetsConvention(t *testing.T) {
	withHome(t)
	_ = Put("default", Profile{URL: "https://default.test", Username: "a@x", Token: "t"})
	home, _ := os.UserHomeDir()
	body, _ := os.ReadFile(filepath.Join(home, ".praxis", "credentials"))
	for _, want := range []string{"[default]", "url      = https://default.test", "username = a@x", "token    = t"} {
		if !strings.Contains(string(body), want) {
			t.Errorf("ini output missing %q\nfile:\n%s", want, body)
		}
	}
}

func TestPutRejectsInvalidNames(t *testing.T) {
	withHome(t)
	cases := []struct {
		name string
		in   string
	}{
		{"empty", ""},
		{"newline", "ac\nme"},
		{"closing-bracket", "ac]me"},
		{"opening-bracket", "ac[me"},
		{"equals", "a=b"},
		{"space", "ac me"},
		{"leading-dot", ".acme"},
		{"leading-dash", "-acme"},
	}
	for _, c := range cases {
		t.Run(c.name, func(t *testing.T) {
			err := Put(c.in, Profile{URL: "https://x", Token: "t"})
			if err == nil {
				t.Errorf("Put(%q) returned nil error; want validation failure", c.in)
			}
			if _, err3 := Delete(c.in); err3 == nil {
				t.Errorf("Delete(%q) returned nil error; want validation failure", c.in)
			}
		})
	}

	// Sanity-check: a valid name still works.
	if err := Put("acme-prod.1", Profile{URL: "https://x", Token: "t"}); err != nil {
		t.Errorf("Put valid name failed: %v", err)
	}
}

func TestParseRawINI_HandlesCommentsAndBlanks(t *testing.T) {
	body := []byte(`# top comment
; semicolon comment

[default]
url = https://x

# inline comment between sections
[acme]
url = https://y
`)
	got := parseRawINI(body)
	if got["default"]["url"] != "https://x" {
		t.Errorf("default.url = %q", got["default"]["url"])
	}
	if got["acme"]["url"] != "https://y" {
		t.Errorf("acme.url = %q", got["acme"]["url"])
	}
}

// ─── Project-local (local mode) resolution ──────────────────────────────

// setCwd is a helper to point project-root discovery at dir for the test.
func setCwd(t *testing.T, dir string) {
	t.Helper()
	t.Cleanup(paths.SetGetwdForTest(func() (string, error) { return dir, nil }))
}

func TestRename(t *testing.T) {
	seed := func(t *testing.T) {
		t.Helper()
		t.Setenv("HOME", t.TempDir())
		if err := Put("old", Profile{URL: "https://old.test", Username: "u@x", Token: "tok"}); err != nil {
			t.Fatal(err)
		}
		if err := Put("other", Profile{URL: "https://other.test", Token: "tok2"}); err != nil {
			t.Fatal(err)
		}
	}

	t.Run("moves every field", func(t *testing.T) {
		seed(t)
		if err := Rename("old", "new"); err != nil {
			t.Fatal(err)
		}
		store, _ := Load()
		if _, ok := store["old"]; ok {
			t.Error("old section still present")
		}
		got := store["new"]
		want := Profile{URL: "https://old.test", Username: "u@x", Token: "tok", Store: StorePraxis}
		if got != want {
			t.Errorf("renamed profile = %+v, want %+v", got, want)
		}
	})

	t.Run("a [default] copy of the renamed profile stays active", func(t *testing.T) {
		seed(t)
		if _, err := SetDefault("old"); err != nil {
			t.Fatal(err)
		}
		if err := Rename("old", "new"); err != nil {
			t.Fatal(err)
		}
		active, _ := ResolveActive("")
		if active.Name != "default" || active.Profile.URL != "https://old.test" {
			t.Errorf("active = %+v, want default with old's credentials", active)
		}
		if same := SameAs(mustLoad(t), "default"); len(same) != 1 || same[0] != "new" {
			t.Errorf("SameAs(default) = %v, want [new]", same)
		}
	})

	t.Run("errors", func(t *testing.T) {
		seed(t)
		for name, pair := range map[string][2]string{
			"missing old":      {"ghost", "new"},
			"existing new":     {"old", "other"},
			"same name":        {"old", "old"},
			"invalid new name": {"old", "has space"},
			"invalid old name": {"[x]", "new"},
		} {
			if err := Rename(pair[0], pair[1]); err == nil {
				t.Errorf("%s: Rename(%q, %q) succeeded, want error", name, pair[0], pair[1])
			}
		}
	})
}

func mustLoad(t *testing.T) map[string]Profile {
	t.Helper()
	store, err := Load()
	if err != nil {
		t.Fatal(err)
	}
	return store
}

// Raptor's rule, applied by praxis: [default], else the sole section.
func TestResolveActive_SoleSectionWhenNoDefault(t *testing.T) {
	withHome(t)
	if err := Put("acme", Profile{URL: "https://acme.test", Username: "u", Token: "t"}); err != nil {
		t.Fatal(err)
	}
	a, err := ResolveActive("")
	if err != nil || a.Name != "acme" || a.Source != SourceSole || !a.Loaded {
		t.Errorf("ResolveActive = %+v, %v; want the sole section", a, err)
	}
	if OnDiskActiveName() != "acme" {
		t.Errorf("OnDiskActiveName() = %q, want acme", OnDiskActiveName())
	}
	// Two sections and no default: nothing resolves, like raptor.
	if err := Put("zed", Profile{URL: "https://zed.test", Username: "u", Token: "t"}); err != nil {
		t.Fatal(err)
	}
	a, _ = ResolveActive("")
	if a.Name != "default" || a.Loaded {
		t.Errorf("ResolveActive with two sections and no default = %+v, want an unloaded default", a)
	}
}

func TestSetDefault_CopiesSectionSoBothCLIsMove(t *testing.T) {
	withHome(t)
	seedFacets(t, homeFacets(t), twoRaptorSections)
	if err := Put("key", Profile{URL: "https://key.test", Username: "k", Token: "sk"}); err != nil {
		t.Fatal(err)
	}

	if _, err := SetDefault("acme"); err != nil {
		t.Fatal(err)
	}
	store := mustLoad(t)
	if store["default"].URL != "https://acme.test" || store["default"].Token != "pat-acme" || store["default"].Store != StoreFacets {
		t.Errorf("[default] = %+v, want a copy of acme in the facets file", store["default"])
	}
	if _, ok := store["acme"]; !ok {
		t.Error("acme itself must survive the copy")
	}
	if same := SameAs(store, "default"); len(same) != 1 || same[0] != "acme" {
		t.Errorf("SameAs(default) = %v, want [acme]", same)
	}

	// An API key becomes default in the praxis file, and the facets [default]
	// goes away so it cannot shadow it.
	if _, err := SetDefault("key"); err != nil {
		t.Fatal(err)
	}
	store = mustLoad(t)
	if store["default"].Token != "sk" || store["default"].Store != StorePraxis {
		t.Errorf("[default] = %+v, want the API key", store["default"])
	}
	if _, still := loadFacets(homeFacets(t))["default"]; still {
		t.Error("facets [default] should have been removed")
	}

	if _, err := SetDefault("default"); err != nil {
		t.Errorf("SetDefault(default) = %v, want no-op", err)
	}
	if _, err := SetDefault("ghost"); err == nil {
		t.Error("SetDefault(ghost) should fail")
	}
}

func TestSetDefaultLocal_WritesTreeFileWithDefaultCopy(t *testing.T) {
	home := withHome(t)
	seedFacets(t, homeFacets(t), twoRaptorSections)
	if err := Put("key", Profile{URL: "https://key.test", Username: "k", Token: "sk"}); err != nil {
		t.Fatal(err)
	}
	repo := filepath.Join(home, "repo")
	if err := os.MkdirAll(repo, 0o755); err != nil {
		t.Fatal(err)
	}

	if err := SetDefaultLocal("acme", repo); err != nil {
		t.Fatal(err)
	}
	local := loadFacets(FacetsPathIn(repo))
	if local["acme"].Token != "pat-acme" || local["default"].Token != "pat-acme" || len(local) != 2 {
		t.Errorf("tree file = %v, want acme and a default copy", local)
	}
	// Home file untouched.
	if h := loadFacets(homeFacets(t)); h["default"].Token != "pat-root" {
		t.Errorf("home [default] changed: %+v", h["default"])
	}
	// Inside the tree both CLIs' store is the tree file, and default resolves.
	t.Cleanup(SetGetwdForTest(func() (string, error) { return repo, nil }))
	a, _ := ResolveActive("")
	if a.Name != "default" || a.Profile.URL != "https://acme.test" {
		t.Errorf("in-tree ResolveActive = %+v", a)
	}
	if names := sortedKeys(mustLoad(t)); strings.Join(names, ",") != "default,acme,key" {
		t.Errorf("in-tree Load() = %v; the tree file shadows home, API keys stay visible", names)
	}

	if err := SetDefaultLocal("key", repo); err == nil {
		t.Error("an API key cannot be pinned to a tree; want error")
	}
}

func TestSetDefault_KeepsAnUnduplicatedDefault(t *testing.T) {
	withHome(t)
	// A bare first login wrote only [default]; a second, named login must not
	// destroy it when it takes the default slot.
	if err := Put("default", FacetsProfile("https://facetsdemo.console.facets.cloud", "u@x", "pat-demo")); err != nil {
		t.Fatal(err)
	}
	if err := Put("snabbit", FacetsProfile("https://6711429322.facetsapp.cloud", "u@x", "pat-snab")); err != nil {
		t.Fatal(err)
	}
	kept, err := SetDefault("snabbit")
	if err != nil {
		t.Fatal(err)
	}
	store := mustLoad(t)
	if kept != "facetsdemo" || store["facetsdemo"].Token != "pat-demo" {
		t.Errorf("kept = %q, store[facetsdemo] = %+v; want the old default preserved by host label", kept, store["facetsdemo"])
	}
	if store["default"].Token != "pat-snab" {
		t.Errorf("[default] = %+v, want snabbit's copy", store["default"])
	}
	// Switching back keeps nothing: [default] is already duplicated by snabbit.
	if kept, _ := SetDefault("facetsdemo"); kept != "" {
		t.Errorf("kept %q on a switch whose default was a copy", kept)
	}
	// A numeric host label works; a collision gets a suffix.
	if err := Put("default", FacetsProfile("https://6711429322.facetsapp.cloud", "o@x", "other")); err != nil {
		t.Fatal(err)
	}
	if kept, _ := SetDefault("facetsdemo"); kept != "6711429322" {
		t.Errorf("kept = %q, want the numeric label", kept)
	}
	if err := Put("default", FacetsProfile("https://facetsdemo.console.facets.cloud", "z@x", "z")); err != nil {
		t.Fatal(err)
	}
	if kept, _ := SetDefault("snabbit"); kept != "facetsdemo-2" {
		t.Errorf("kept = %q, want facetsdemo-2 on collision", kept)
	}
}
