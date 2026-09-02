package credentials

import (
	"os"
	"path/filepath"
	"strings"
	"testing"

	"github.com/Facets-cloud/praxis-cli/internal/paths"
)

// seedFacets writes a raptor-style credentials file at path.
func seedFacets(t *testing.T, path, body string) {
	t.Helper()
	if err := os.MkdirAll(filepath.Dir(path), 0o700); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(path, []byte(body), 0o600); err != nil {
		t.Fatal(err)
	}
}

func homeFacets(t *testing.T) string {
	t.Helper()
	p, err := FacetsHome()
	if err != nil {
		t.Fatal(err)
	}
	return p
}

const twoRaptorSections = "[default]\ncontrol_plane_url = https://root.test\nusername = u@x\ntoken = pat-root\n\n[acme]\ncontrol_plane_url = https://acme.test\nusername = u@x\ntoken = pat-acme\ncontrol_plane_project = proj\n"

func TestLoad_MergesBothStoresFacetsWins(t *testing.T) {
	withHome(t)
	seedFacets(t, homeFacets(t), twoRaptorSections)
	// The praxis file holds an API key and a stale copy of "acme".
	if err := savePraxis(map[string]Profile{
		"apikey": {URL: "https://root.test", Username: "u@x", Token: "sk"},
		"acme":   {URL: "https://old.test", Username: "u@x", Token: "stale"},
	}); err != nil {
		t.Fatal(err)
	}

	store, err := Load()
	if err != nil {
		t.Fatal(err)
	}
	if len(store) != 3 {
		t.Fatalf("store = %v, want default, acme, apikey", store)
	}
	if p := store["default"]; p.Store != StoreFacets || p.AuthMode != AuthModeBasic || p.Token != "pat-root" || p.URL != "https://root.test" {
		t.Errorf("default = %+v, want a facets PAT profile", p)
	}
	if p := store["acme"]; p.Store != StoreFacets || p.Token != "pat-acme" {
		t.Errorf("acme = %+v; the facets section must win over the praxis copy", p)
	}
	if p := store["apikey"]; p.Store != StorePraxis || p.AuthMode != "" || p.Token != "sk" {
		t.Errorf("apikey = %+v, want a praxis API-key profile", p)
	}
	// Wire shape follows the store.
	if h := store["default"].Auth(); h["X-Facets-Username"] != "u@x" {
		t.Errorf("facets profile Auth() = %v, want the identity header", h)
	}
	if h := store["apikey"].Auth(); h["X-Facets-Username"] != "" {
		t.Errorf("API-key profile Auth() = %v, want plain Bearer", h)
	}
}

func TestPut_RoutesByCredentialType(t *testing.T) {
	tests := []struct {
		name       string
		prof       Profile
		wantFacets bool
	}{
		{name: "https PAT goes to the facets file", prof: FacetsProfile("https://cp.test", "u@x", "pat"), wantFacets: true},
		{name: "API key goes to the praxis file", prof: Profile{URL: "https://cp.test", Username: "u@x", Token: "sk"}},
		{name: "loopback PAT stays in the praxis file", prof: FacetsProfile("http://localhost:8000", "u@x", "pat")},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			withHome(t)
			if err := Put("p", tt.prof); err != nil {
				t.Fatalf("Put: %v", err)
			}
			facets := loadFacets(homeFacets(t))
			praxis, _ := loadPraxis()
			_, inFacets := facets["p"]
			_, inPraxis := praxis["p"]
			if inFacets != tt.wantFacets || inPraxis == tt.wantFacets {
				t.Errorf("inFacets=%v inPraxis=%v, want facets=%v", inFacets, inPraxis, tt.wantFacets)
			}
			if tt.wantFacets {
				fi, _ := os.Stat(homeFacets(t))
				if fi.Mode().Perm() != 0o600 {
					t.Errorf("facets file mode = %o, want 0600", fi.Mode().Perm())
				}
				if got := facets["p"]; got.URL != "https://cp.test" || got.Username != "u@x" || got.Token != "pat" {
					t.Errorf("facets section = %+v", got)
				}
			} else if got := praxis["p"]; got.AuthMode != tt.prof.AuthMode {
				t.Errorf("praxis auth_mode = %q, want %q", got.AuthMode, tt.prof.AuthMode)
			}
			// Load sees exactly one copy either way.
			store, _ := Load()
			if _, ok := store["p"]; !ok || len(store) != 1 {
				t.Errorf("Load() = %v, want exactly p", store)
			}
		})
	}
}

func TestPut_ProfileLivesInOneFile(t *testing.T) {
	withHome(t)
	// A legacy praxis-file PAT copy is dropped when the PAT goes to facets.
	if err := savePraxis(map[string]Profile{"default": FacetsProfile("https://cp.test", "u@x", "old")}); err != nil {
		t.Fatal(err)
	}
	if err := Put("default", FacetsProfile("https://cp.test", "u@x", "new")); err != nil {
		t.Fatal(err)
	}
	if praxis, _ := loadPraxis(); len(praxis) != 0 {
		t.Errorf("praxis file still has %v after a facets Put", praxis)
	}
	// And a facets section is dropped when the same name becomes an API key,
	// or the API key would be shadowed forever.
	if err := Put("default", Profile{URL: "https://cp.test", Username: "u@x", Token: "sk"}); err != nil {
		t.Fatal(err)
	}
	if facets := loadFacets(homeFacets(t)); len(facets) != 0 {
		t.Errorf("facets file still has %v after an API-key Put", facets)
	}
	store, _ := Load()
	if store["default"].Token != "sk" || store["default"].Store != StorePraxis {
		t.Errorf("Load() default = %+v", store["default"])
	}
}

func TestPut_PreservesOtherSectionsAndKeys(t *testing.T) {
	withHome(t)
	seedFacets(t, homeFacets(t), twoRaptorSections)
	if err := Put("default", FacetsProfile("https://new.test/prefix/", "n@x", "pat-new")); err != nil {
		t.Fatal(err)
	}
	data, _ := os.ReadFile(homeFacets(t))
	for _, want := range []string{
		"[acme]\n", "control_plane_project = proj\n", "token = pat-acme\n",
		"[default]\n", "control_plane_url = https://new.test\n", "username = n@x\n", "token = pat-new\n",
	} {
		if !strings.Contains(string(data), want) {
			t.Errorf("file missing %q:\n%s", want, data)
		}
	}
	// Every section still has raptor's three required keys.
	for name, kv := range rawFacets(homeFacets(t)) {
		for _, k := range []string{"control_plane_url", "username", "token"} {
			if kv[k] == "" {
				t.Errorf("section %q lost %q", name, k)
			}
		}
	}
}

func TestPutLocal_WritesProjectFacetsFile(t *testing.T) {
	home := withHome(t)
	repo := filepath.Join(home, "repo")
	if err := os.MkdirAll(repo, 0o755); err != nil {
		t.Fatal(err)
	}
	if err := PutLocal("acme", FacetsProfile("https://acme.test", "u@x", "pat"), repo); err != nil {
		t.Fatal(err)
	}
	if got := loadFacets(FacetsPathIn(repo))["acme"]; got.Token != "pat" {
		t.Errorf("repo facets = %+v", got)
	}
	if gi, err := os.ReadFile(filepath.Join(repo, ".facets", ".gitignore")); err != nil || string(gi) != "credentials\n" {
		t.Errorf(".gitignore = %q, %v", gi, err)
	}
	if _, err := os.Stat(homeFacets(t)); !os.IsNotExist(err) {
		t.Errorf("home facets file written by PutLocal: %v", err)
	}
	// An API key ignores dir: the praxis file is always global.
	if err := PutLocal("key", Profile{URL: "https://acme.test", Username: "u@x", Token: "sk"}, repo); err != nil {
		t.Fatal(err)
	}
	if praxis, _ := loadPraxis(); praxis["key"].Token != "sk" {
		t.Errorf("praxis file = %v, want key", praxis)
	}
}

func TestFacetsPath_WalksUpAndShadowsHome(t *testing.T) {
	home := withHome(t)
	seedFacets(t, homeFacets(t), "[default]\ncontrol_plane_url = https://home.test\nusername = h\ntoken = ht\n")
	repo := filepath.Join(home, "repo")
	seedFacets(t, FacetsPathIn(repo), "[acme]\ncontrol_plane_url = https://acme.test\nusername = a\ntoken = at\n")
	sub := filepath.Join(repo, "deep", "er")
	if err := os.MkdirAll(sub, 0o755); err != nil {
		t.Fatal(err)
	}

	tests := []struct {
		name, cwd, wantPath string
		wantNames           []string
	}{
		{"outside the repo the home file resolves", home, homeFacets(t), []string{"default"}},
		{"inside the repo the local file shadows home", sub, FacetsPathIn(repo), []string{"acme"}},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			t.Cleanup(SetGetwdForTest(func() (string, error) { return tt.cwd, nil }))
			got, err := FacetsPath()
			if err != nil || got != tt.wantPath {
				t.Fatalf("FacetsPath() = %q, %v; want %q", got, err, tt.wantPath)
			}
			store, err := Load()
			if err != nil {
				t.Fatal(err)
			}
			names := sortedKeys(store)
			if strings.Join(names, ",") != strings.Join(tt.wantNames, ",") {
				t.Errorf("Load() names = %v, want %v", names, tt.wantNames)
			}
		})
	}
}

func TestDelete_RemovesFromBothFilesAndReports(t *testing.T) {
	withHome(t)
	seedFacets(t, homeFacets(t), twoRaptorSections)
	if err := savePraxis(map[string]Profile{"acme": {URL: "https://x", Token: "stale"}, "key": {URL: "https://x", Token: "sk"}}); err != nil {
		t.Fatal(err)
	}

	d, err := Delete("acme")
	if err != nil {
		t.Fatal(err)
	}
	if !d.Praxis || !d.Facets || d.FacetsPath != homeFacets(t) {
		t.Errorf("Deleted = %+v, want both files", d)
	}
	store, _ := Load()
	if _, ok := store["acme"]; ok {
		t.Error("acme still loads")
	}
	if _, ok := store["default"]; !ok {
		t.Error("default was removed too")
	}

	d, err = Delete("key")
	if err != nil || d.Facets || !d.Praxis {
		t.Errorf("Delete(key) = %+v, %v; want praxis only", d, err)
	}
	d, err = Delete("ghost")
	if err != nil || d.Facets || d.Praxis {
		t.Errorf("Delete(ghost) = %+v, %v; want nothing", d, err)
	}

	// Deleting the last facets section removes the file, so raptor sees no
	// half-empty store.
	if _, err := Delete("default"); err != nil {
		t.Fatal(err)
	}
	if _, err := os.Stat(homeFacets(t)); !os.IsNotExist(err) {
		t.Errorf("empty facets file left behind: %v", err)
	}
}

func TestDeleteAll_WipesBothHomeFiles(t *testing.T) {
	withHome(t)
	seedFacets(t, homeFacets(t), twoRaptorSections)
	if err := Put("key", Profile{URL: "https://x", Token: "sk"}); err != nil {
		t.Fatal(err)
	}
	if err := DeleteAll(); err != nil {
		t.Fatal(err)
	}
	store, _ := Load()
	if len(store) != 0 {
		t.Errorf("Load() after DeleteAll = %v", store)
	}
}

func TestRename_FollowsTheStore(t *testing.T) {
	withHome(t)
	seedFacets(t, homeFacets(t), twoRaptorSections)
	if err := Put("key", Profile{URL: "https://x", Username: "u", Token: "sk"}); err != nil {
		t.Fatal(err)
	}

	if err := Rename("acme", "acme2"); err != nil {
		t.Fatal(err)
	}
	facets := loadFacets(homeFacets(t))
	if _, old := facets["acme"]; old || facets["acme2"].Token != "pat-acme" {
		t.Errorf("facets after rename = %v", facets)
	}
	if kv := rawFacets(homeFacets(t))["acme2"]; kv["control_plane_project"] != "proj" {
		t.Errorf("extra key lost on rename: %v", kv)
	}

	if err := Rename("key", "key2"); err != nil {
		t.Fatal(err)
	}
	praxis, _ := loadPraxis()
	if _, old := praxis["key"]; old || praxis["key2"].Token != "sk" {
		t.Errorf("praxis after rename = %v", praxis)
	}
	if err := Rename("key2", "default"); err == nil {
		t.Error("rename onto an existing facets section should fail")
	}
}

func TestMigrateLegacyPATs(t *testing.T) {
	withHome(t)
	if err := savePraxis(map[string]Profile{
		"root":  FacetsProfile("https://root.test", "u@x", "pat-root"),
		"dev":   FacetsProfile("http://localhost:8000", "u@x", "pat-dev"),
		"key":   {URL: "https://root.test", Username: "u@x", Token: "sk"},
		"other": FacetsProfile("https://other.test", "o@x", "pat-other"),
	}); err != nil {
		t.Fatal(err)
	}
	seedFacets(t, homeFacets(t), "[default]\ncontrol_plane_url = https://root.test\nusername = u@x\ntoken = pat\n")

	moved, err := MigrateLegacyPATs()
	if err != nil {
		t.Fatal(err)
	}
	if strings.Join(moved, ",") != "other,root" {
		t.Errorf("moved = %v, want other,root", moved)
	}
	facets := loadFacets(homeFacets(t))
	if facets["root"].Token != "pat-root" || facets["other"].Token != "pat-other" || facets["default"].Token != "pat" {
		t.Errorf("facets after migration = %v", facets)
	}
	praxis, _ := loadPraxis()
	if len(praxis) != 2 || praxis["dev"].Token != "pat-dev" || praxis["key"].Token != "sk" {
		t.Errorf("praxis after migration = %v, want dev and key only", praxis)
	}
	// Idempotent.
	if moved, err := MigrateLegacyPATs(); err != nil || moved != nil {
		t.Errorf("second run moved %v, %v", moved, err)
	}
}

func TestResolveActive_FacetsProfileEnvAndOverride(t *testing.T) {
	withHome(t)
	seedFacets(t, homeFacets(t), twoRaptorSections)

	t.Run("FACETS_PROFILE selects after PRAXIS_PROFILE", func(t *testing.T) {
		t.Setenv(FacetsEnvProfile, "acme")
		a, err := ResolveActive("")
		if err != nil || a.Name != "acme" || a.Source != SourceFacetsEnv || !a.Loaded {
			t.Errorf("ResolveActive = %+v, %v", a, err)
		}
		t.Setenv(EnvProfile, "default")
		if a, _ := ResolveActive(""); a.Name != "default" || a.Source != SourceEnv {
			t.Errorf("PRAXIS_PROFILE should outrank FACETS_PROFILE: %+v", a)
		}
	})
	t.Run("raptor env override is a complete profile", func(t *testing.T) {
		t.Setenv("CONTROL_PLANE_URL", "https://env.test/")
		t.Setenv("FACETS_USERNAME", "e@x")
		t.Setenv("FACETS_TOKEN", "env-pat")
		a, err := ResolveActive("")
		if err != nil || a.Name != "env" || a.Source != SourceEnvOverride || !a.Loaded {
			t.Fatalf("ResolveActive = %+v, %v", a, err)
		}
		if a.Profile.URL != "https://env.test" || a.Profile.Store != StoreEnv || a.Profile.Auth()["X-Facets-Username"] != "e@x" {
			t.Errorf("env profile = %+v", a.Profile)
		}
		// An explicit flag still wins.
		if a, _ := ResolveActive("acme"); a.Name != "acme" || a.Source != SourceFlag {
			t.Errorf("flag lost to env override: %+v", a)
		}
	})
	t.Run("partial override is ignored", func(t *testing.T) {
		t.Setenv("CONTROL_PLANE_URL", "https://env.test")
		if a, _ := ResolveActive(""); a.Source == SourceEnvOverride {
			t.Errorf("CONTROL_PLANE_URL alone must not make a profile: %+v", a)
		}
	})
}

func TestMigrateLegacyPointer(t *testing.T) {
	tests := []struct {
		name         string
		pointer      string // "" = no file
		wantPromoted string
		wantDefault  string // URL of [default] afterwards
	}{
		{name: "no pointer is a no-op", wantDefault: "https://root.test"},
		{name: "pointer to default changes nothing", pointer: "default", wantDefault: "https://root.test"},
		{name: "pointer to another profile becomes the default copy", pointer: "acme", wantPromoted: "acme", wantDefault: "https://acme.test"},
		{name: "pointer to a missing profile is dropped", pointer: "ghost", wantDefault: "https://root.test"},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			withHome(t)
			seedFacets(t, homeFacets(t), twoRaptorSections)
			legacy, _ := paths.LegacyConfig()
			if tt.pointer != "" {
				if err := os.MkdirAll(filepath.Dir(legacy), 0o700); err != nil {
					t.Fatal(err)
				}
				if err := os.WriteFile(legacy, []byte("[default]\nprofile = "+tt.pointer+"\n"), 0o600); err != nil {
					t.Fatal(err)
				}
			}
			promoted, err := MigrateLegacyPointer()
			if err != nil {
				t.Fatal(err)
			}
			if promoted != tt.wantPromoted {
				t.Errorf("promoted = %q, want %q", promoted, tt.wantPromoted)
			}
			if _, err := os.Stat(legacy); !os.IsNotExist(err) {
				t.Error("legacy pointer file still exists")
			}
			if got := mustLoad(t)["default"].URL; got != tt.wantDefault {
				t.Errorf("[default] = %q, want %q", got, tt.wantDefault)
			}
			// Idempotent.
			if p, err := MigrateLegacyPointer(); err != nil || p != "" {
				t.Errorf("second run = %q, %v", p, err)
			}
		})
	}
}
