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
	t.Setenv("PRAXIS_PROFILE", "")
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
	t.Setenv("PRAXIS_PROFILE", "from-env")
	if err := SetActive("from-config"); err != nil {
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

func TestResolveActive_ConfigBeatsEnv(t *testing.T) {
	// `praxis use` is an explicit, persistent choice — it should be
	// sticky and not get silently overridden by PRAXIS_PROFILE.
	withHome(t)
	t.Setenv("PRAXIS_PROFILE", "from-env")
	if err := SetActive("from-config"); err != nil {
		t.Fatal(err)
	}
	a, err := ResolveActive("")
	if err != nil {
		t.Fatal(err)
	}
	if a.Name != "from-config" || a.Source != SourceConfig {
		t.Errorf("config (set by `praxis use`) should beat env, got %+v", a)
	}
}

func TestResolveActive_EnvBeatsDefault(t *testing.T) {
	// When `praxis use` hasn't been called, PRAXIS_PROFILE is the
	// next signal before falling through to "default".
	withHome(t)
	t.Setenv("PRAXIS_PROFILE", "from-env")
	a, err := ResolveActive("")
	if err != nil {
		t.Fatal(err)
	}
	if a.Name != "from-env" || a.Source != SourceEnv {
		t.Errorf("env should beat default, got %+v", a)
	}
}

func TestResolveActive_ConfigBeatsDefault(t *testing.T) {
	withHome(t)
	if err := SetActive("acme"); err != nil {
		t.Fatal(err)
	}
	a, err := ResolveActive("")
	if err != nil {
		t.Fatal(err)
	}
	if a.Name != "acme" || a.Source != SourceConfig {
		t.Errorf("config should beat default, got %+v", a)
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
	if got != want {
		t.Errorf("round-trip mismatch: got %+v want %+v", got, want)
	}
}

func TestPut_AddsSecondProfileWithoutClobberingFirst(t *testing.T) {
	withHome(t)
	if err := Put("default", Profile{URL: "https://askpraxis.ai", Username: "a@x", Token: "t1"}); err != nil {
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
	if err := Delete("acme"); err != nil {
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
	if err := Delete("never-there"); err != nil {
		t.Errorf("delete of non-existent profile should not error: %v", err)
	}
}

func TestDeleteAll_AlsoClearsActivePointer(t *testing.T) {
	withHome(t)
	_ = Put("default", Profile{URL: "x", Token: "t"})
	_ = SetActive("default")

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
	_ = Put("default", Profile{URL: "https://askpraxis.ai", Username: "a@x", Token: "t"})
	home, _ := os.UserHomeDir()
	body, _ := os.ReadFile(filepath.Join(home, ".praxis", "credentials"))
	for _, want := range []string{"[default]", "url      = https://askpraxis.ai", "username = a@x", "token    = t"} {
		if !strings.Contains(string(body), want) {
			t.Errorf("ini output missing %q\nfile:\n%s", want, body)
		}
	}
}

func TestSetActive_WritesAndIsRead(t *testing.T) {
	withHome(t)
	if err := SetActive("acme"); err != nil {
		t.Fatal(err)
	}
	cfg, err := loadConfig()
	if err != nil {
		t.Fatal(err)
	}
	if cfg.Profile != "acme" {
		t.Errorf("loaded profile = %q, want acme", cfg.Profile)
	}
}

func TestClearActive_AfterSetActive_FallsBackToDefault(t *testing.T) {
	withHome(t)
	_ = SetActive("acme")
	if err := ClearActive(); err != nil {
		t.Fatal(err)
	}
	a, _ := ResolveActive("")
	if a.Name != "default" || a.Source != SourceDefault {
		t.Errorf("after ClearActive, expected default/default, got %+v", a)
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
			if err2 := SetActive(c.in); err2 == nil {
				t.Errorf("SetActive(%q) returned nil error; want validation failure", c.in)
			}
			if err3 := Delete(c.in); err3 == nil {
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

// TestDefaultURL_IsCanonicalHost guards issue #18: the apex
// https://askpraxis.ai 301-redirects to www, which (before the callMCP
// redirect fix) broke every MCP invoke on a fresh install. Default to
// the canonical host so fresh logins don't redirect at all.
func TestDefaultURL_IsCanonicalHost(t *testing.T) {
	const want = "https://www.askpraxis.ai"
	if DefaultURL != want {
		t.Errorf("DefaultURL = %q, want %q (canonical host, no 301 redirect)", DefaultURL, want)
	}
}

// ─── Project-local (local mode) resolution ──────────────────────────────

// setCwd is a helper to point project-root discovery at dir for the test.
func setCwd(t *testing.T, dir string) {
	t.Helper()
	t.Cleanup(paths.SetGetwdForTest(func() (string, error) { return dir, nil }))
}

func TestResolveActive_ProjectBeatsConfigAndEnv(t *testing.T) {
	home := withHome(t)
	t.Setenv("PRAXIS_PROFILE", "from-env")
	if err := SetActive("from-config"); err != nil {
		t.Fatal(err)
	}
	// The project profile must EXIST in the store for the pointer to win
	// (an unknown profile gracefully falls back to global — see
	// TestResolveActive_ProjectPointerToMissingProfile_FallsBack).
	if err := Put("from-project", Profile{URL: "https://p.test", Token: "t"}); err != nil {
		t.Fatal(err)
	}
	repo := filepath.Join(home, "repo")
	if err := os.MkdirAll(repo, 0o755); err != nil {
		t.Fatal(err)
	}
	setCwd(t, repo)
	if _, err := SetActiveLocal("from-project"); err != nil {
		t.Fatalf("SetActiveLocal: %v", err)
	}

	a, err := ResolveActive("")
	if err != nil {
		t.Fatal(err)
	}
	if a.Name != "from-project" || a.Source != SourceProject {
		t.Errorf("project pointer should win, got name=%q source=%s", a.Name, a.Source)
	}
}

// TestResolveActive_ProjectPointerToMissingProfile_FallsBack is the
// regression test for the teammate-hijack fix: a project pointer naming a
// profile this machine doesn't have (e.g. a committed <repo>/.praxis) must
// NOT win — it falls back to the global resolution so a normal user isn't
// locked into a profile they never created.
func TestResolveActive_ProjectPointerToMissingProfile_FallsBack(t *testing.T) {
	home := withHome(t)
	if err := SetActive("from-config"); err != nil {
		t.Fatal(err)
	}
	repo := filepath.Join(home, "repo")
	if err := os.MkdirAll(repo, 0o755); err != nil {
		t.Fatal(err)
	}
	setCwd(t, repo)
	// Pointer to "ghost" — but no such profile is ever Put into the store.
	if _, err := SetActiveLocal("ghost"); err != nil {
		t.Fatalf("SetActiveLocal: %v", err)
	}

	a, err := ResolveActive("")
	if err != nil {
		t.Fatal(err)
	}
	if a.Name != "from-config" || a.Source != SourceConfig {
		t.Errorf("missing project profile should fall back to global config, got name=%q source=%s", a.Name, a.Source)
	}
}

// TestResolveActiveGlobal_IgnoresProjectPointer pins that the global resolver
// (used by `praxis logout`) never honors a project pointer, even one naming a
// real profile.
func TestResolveActiveGlobal_IgnoresProjectPointer(t *testing.T) {
	home := withHome(t)
	if err := SetActive("from-config"); err != nil {
		t.Fatal(err)
	}
	if err := Put("from-project", Profile{URL: "https://p.test", Token: "t"}); err != nil {
		t.Fatal(err)
	}
	repo := filepath.Join(home, "repo")
	if err := os.MkdirAll(repo, 0o755); err != nil {
		t.Fatal(err)
	}
	setCwd(t, repo)
	if _, err := SetActiveLocal("from-project"); err != nil {
		t.Fatal(err)
	}

	// ResolveActive honors the project pointer...
	if a, _ := ResolveActive(""); a.Name != "from-project" {
		t.Errorf("ResolveActive should see project profile, got %q", a.Name)
	}
	// ...but the global resolver ignores it.
	g, err := ResolveActiveGlobal()
	if err != nil {
		t.Fatal(err)
	}
	if g.Name != "from-config" || g.Source != SourceConfig {
		t.Errorf("ResolveActiveGlobal must ignore project pointer, got name=%q source=%s", g.Name, g.Source)
	}
}

func TestResolveActive_FlagStillBeatsProject(t *testing.T) {
	home := withHome(t)
	repo := filepath.Join(home, "repo")
	if err := os.MkdirAll(repo, 0o755); err != nil {
		t.Fatal(err)
	}
	setCwd(t, repo)
	if _, err := SetActiveLocal("from-project"); err != nil {
		t.Fatal(err)
	}
	a, _ := ResolveActive("from-flag")
	if a.Name != "from-flag" || a.Source != SourceFlag {
		t.Errorf("--profile flag must beat project pointer, got %+v", a)
	}
}

func TestSetActiveLocal_CreatesMarkerAndConfig(t *testing.T) {
	home := withHome(t)
	repo := filepath.Join(home, "repo")
	if err := os.MkdirAll(repo, 0o755); err != nil {
		t.Fatal(err)
	}
	setCwd(t, repo)

	root, err := SetActiveLocal("acme")
	if err != nil {
		t.Fatalf("SetActiveLocal: %v", err)
	}
	if want := filepath.Join(repo, ".praxis"); root != want {
		t.Errorf("root = %q, want %q", root, want)
	}
	cfgPath := filepath.Join(root, "config.json")
	data, err := os.ReadFile(cfgPath)
	if err != nil {
		t.Fatalf("read project config: %v", err)
	}
	if !strings.Contains(string(data), "profile = acme") {
		t.Errorf("project config missing pointer; got %q", string(data))
	}
	// Credentials file must NOT have been created by a local use.
	credPath, _ := paths.Credentials()
	if _, err := os.Stat(credPath); !os.IsNotExist(err) {
		t.Errorf("SetActiveLocal must not touch the global credentials file (err=%v)", err)
	}
}

func TestSetActiveLocal_ReusesAncestorRoot(t *testing.T) {
	home := withHome(t)
	repo := filepath.Join(home, "repo")
	sub := filepath.Join(repo, "deep", "nested")
	if err := os.MkdirAll(sub, 0o755); err != nil {
		t.Fatal(err)
	}
	// Pre-existing project root at the repo level.
	if err := os.MkdirAll(filepath.Join(repo, ".praxis"), 0o700); err != nil {
		t.Fatal(err)
	}
	setCwd(t, sub)

	root, err := SetActiveLocal("acme")
	if err != nil {
		t.Fatal(err)
	}
	// Must reuse the ancestor's .praxis, not create one in the nested cwd.
	if want := filepath.Join(repo, ".praxis"); root != want {
		t.Errorf("root = %q, want ancestor %q (no nested marker)", root, want)
	}
	if _, err := os.Stat(filepath.Join(sub, ".praxis")); !os.IsNotExist(err) {
		t.Errorf("must not create a nested .praxis in cwd (err=%v)", err)
	}
}

func TestSetActiveLocal_OutsideHome_Errors(t *testing.T) {
	withHome(t)
	outside := t.TempDir()
	setCwd(t, outside)
	_, err := SetActiveLocal("acme")
	if err == nil {
		t.Fatal("SetActiveLocal outside home should error, got nil")
	}
	if !strings.Contains(err.Error(), "under your home directory") {
		t.Errorf("error should explain the home-subtree requirement; got %q", err.Error())
	}
}
