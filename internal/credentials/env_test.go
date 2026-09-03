package credentials

import (
	"os"
	"testing"
)

// A developer (or an agent session) may have PRAXIS_PROFILE exported, and it
// now outranks both pointers — so leaking it into the suite would make results
// depend on the shell that launched `go test`. Clear it once for the package.
func TestMain(m *testing.M) {
	for _, k := range []string{EnvProfile, FacetsEnvProfile, "CONTROL_PLANE_URL", "FACETS_USERNAME", "FACETS_TOKEN"} {
		_ = os.Unsetenv(k)
	}
	// The facets-file walk starts at cwd and climbs to /, which passes through
	// the developer's real home. Start it at the (faked) HOME instead.
	restore := SetGetwdForTest(func() (string, error) { return os.Getenv("HOME"), nil })
	code := m.Run()
	restore()
	os.Exit(code)
}

// The point of the env var: a session picks its profile without writing any
// shared file, so nothing another session does can move it.
func TestResolveActive_EnvSelectsProfile(t *testing.T) {
	withHome(t)
	if err := Put("acme", Profile{URL: "https://acme.test", Token: "ta"}); err != nil {
		t.Fatal(err)
	}
	t.Setenv(EnvProfile, "acme")

	a, err := ResolveActive("")
	if err != nil {
		t.Fatal(err)
	}
	if a.Name != "acme" || a.Source != SourceEnv {
		t.Errorf("got name=%q source=%s; want acme/env", a.Name, a.Source)
	}
	if !a.Loaded || a.Profile.Token != "ta" {
		t.Errorf("env-selected profile not loaded: %+v", a)
	}
}

// Whitespace is not a profile name. A shell that exports PRAXIS_PROFILE="" (or
// with a stray space) must resolve normally rather than look up "".
func TestResolveActive_BlankEnvIsIgnored(t *testing.T) {
	for _, blank := range []string{"", " ", "\t", "\n"} {
		t.Run("blank="+blankLabel(blank), func(t *testing.T) {
			withHome(t)
			if err := Put("g", Profile{URL: "https://g.test", Token: "tg"}); err != nil {
				t.Fatal(err)
			}
			t.Setenv(EnvProfile, blank)

			a, err := ResolveActive("")
			if err != nil {
				t.Fatal(err)
			}
			if a.Name != "g" || a.Source != SourceSole {
				t.Errorf("got %s/%s, want g/sole \u2014 blank env must not select a profile", a.Name, a.Source)
			}
		})
	}
}

// An ENV var naming an unknown profile must fail loudly: it's this session's
// explicit choice, and silently routing a typo to another org's gateway is
// worse than failing.
func TestResolveActive_UnknownEnvProfileFailsLoudly(t *testing.T) {
	withHome(t)
	if err := Put("g", Profile{URL: "https://g.test", Token: "tg"}); err != nil {
		t.Fatal(err)
	}
	t.Setenv(EnvProfile, "ghost")

	a, err := ResolveActive("")
	if err != nil {
		t.Fatal(err)
	}
	if a.Name != "ghost" || a.Source != SourceEnv {
		t.Errorf("got %s/%s, want ghost/env \u2014 no silent fallback for an explicit choice", a.Name, a.Source)
	}
	if a.Loaded {
		t.Error("Loaded should be false so the caller exits with an auth error")
	}
}

// The guarantee the env var exists to provide: one session switching the
// machine-global pointer cannot move an env-scoped session.
func TestEnvScopedSessionIsImmuneToAnotherSessionsSwitch(t *testing.T) {
	withHome(t)
	for _, n := range []string{"acme", "bigcorp"} {
		if err := Put(n, Profile{URL: "https://" + n + ".test", Token: "t" + n}); err != nil {
			t.Fatal(err)
		}
	}
	if _, err := SetDefault("acme"); err != nil {
		t.Fatal(err)
	}

	// Session B scopes itself to bigcorp via the environment.
	t.Setenv(EnvProfile, "bigcorp")
	before, err := ResolveActive("")
	if err != nil {
		t.Fatal(err)
	}

	// Session A switches the machine-global default. This is exactly what
	// `praxis profiles use` does, and it rewrites a file session B can see.
	if _, err := SetDefault("acme"); err != nil {
		t.Fatal(err)
	}

	after, err := ResolveActive("")
	if err != nil {
		t.Fatal(err)
	}
	if after.Name != before.Name || after.Name != "bigcorp" {
		t.Errorf("env-scoped session moved from %q to %q after another session switched", before.Name, after.Name)
	}
	if after.Profile.URL != "https://bigcorp.test" {
		t.Errorf("URL = %q, want bigcorp's \u2014 routing must not follow the other session", after.Profile.URL)
	}
}

// blankLabel renders a whitespace string visibly in a subtest name.
func blankLabel(s string) string {
	switch s {
	case "":
		return "empty"
	case " ":
		return "space"
	case "\t":
		return "tab"
	case "\n":
		return "newline"
	}
	return s
}

// Precedence, end to end: flag > PRAXIS_PROFILE > FACETS_PROFILE > [default]
// > sole section. There is no pointer file.
func TestResolveName_Precedence(t *testing.T) {
	tests := []struct {
		name       string
		flag       string
		env        string
		facetsEnv  string
		sections   []string
		wantName   string
		wantSource Source
	}{
		{name: "nothing set, no store", wantName: "default", wantSource: SourceDefault},
		{name: "default section", sections: []string{"default", "g"}, wantName: "default", wantSource: SourceDefault},
		{name: "sole section", sections: []string{"g"}, wantName: "g", wantSource: SourceSole},
		{name: "two sections and no default resolve nothing", sections: []string{"g", "p"}, wantName: "default", wantSource: SourceDefault},
		{name: "FACETS_PROFILE beats the store", facetsEnv: "p", sections: []string{"default", "p"}, wantName: "p", wantSource: SourceFacetsEnv},
		{name: "PRAXIS_PROFILE beats FACETS_PROFILE", env: "e", facetsEnv: "p", sections: []string{"e", "p"}, wantName: "e", wantSource: SourceEnv},
		{name: "flag beats everything", flag: "f", env: "e", facetsEnv: "p", sections: []string{"f", "e", "p"}, wantName: "f", wantSource: SourceFlag},
	}
	for _, tc := range tests {
		t.Run(tc.name, func(t *testing.T) {
			withHome(t)
			for _, n := range tc.sections {
				if err := Put(n, Profile{URL: "https://" + n + ".test", Token: "t" + n}); err != nil {
					t.Fatal(err)
				}
			}
			t.Setenv(EnvProfile, tc.env)
			t.Setenv(FacetsEnvProfile, tc.facetsEnv)
			a, err := ResolveActive(tc.flag)
			if err != nil {
				t.Fatal(err)
			}
			if a.Name != tc.wantName || a.Source != tc.wantSource {
				t.Errorf("ResolveActive(%q) = %s (%s), want %s (%s)", tc.flag, a.Name, a.Source, tc.wantName, tc.wantSource)
			}
		})
	}
}

// OnDiskActiveName answers "what would a bare command act on?", so it must
// ignore the two things an invocation CAN name. Comparing a selection against a
// resolution that honors it always matches, and a refusal that always matches
// is a refusal that never fires.
func TestOnDiskActiveName_IgnoresEnv(t *testing.T) {
	withHome(t)
	for _, n := range []string{"default", "e"} {
		if err := Put(n, Profile{URL: "https://" + n + ".test", Token: "t" + n}); err != nil {
			t.Fatal(err)
		}
	}
	t.Setenv(EnvProfile, "e")
	if got := OnDiskActiveName(); got != "default" {
		t.Errorf("OnDiskActiveName() = %q under PRAXIS_PROFILE=e, want default", got)
	}
	t.Setenv(FacetsEnvProfile, "e")
	if got := OnDiskActiveName(); got != "default" {
		t.Errorf("OnDiskActiveName() = %q under FACETS_PROFILE=e, want default", got)
	}
}
