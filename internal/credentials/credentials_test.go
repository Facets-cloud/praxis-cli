package credentials

import (
	"os"
	"path/filepath"
	"strings"
	"testing"
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
