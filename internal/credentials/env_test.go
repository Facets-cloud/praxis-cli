package credentials

import (
	"os"
	"path/filepath"
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

// Precedence, end to end: flag > env > project pointer > global pointer >
// default. The env slot has to sit ABOVE the project pointer or a pinned repo
// couldn't be overridden for one session.
func TestResolveName_Precedence(t *testing.T) {
	tests := []struct {
		name       string
		flag       string
		env        string
		project    string // profile named by <cwd>/.praxis, "" for none
		global     string // profile named by ~/.praxis, "" for none
		wantName   string
		wantSource Source
	}{
		{name: "nothing set", wantName: "default", wantSource: SourceDefault},
		{name: "global only", global: "g", wantName: "g", wantSource: SourceConfig},
		{name: "project beats global", project: "p", global: "g", wantName: "p", wantSource: SourceProject},
		{name: "env beats project", env: "e", project: "p", global: "g", wantName: "e", wantSource: SourceEnv},
		{name: "env beats global", env: "e", global: "g", wantName: "e", wantSource: SourceEnv},
		{name: "flag beats env", flag: "f", env: "e", project: "p", global: "g", wantName: "f", wantSource: SourceFlag},
	}

	for _, tc := range tests {
		t.Run(tc.name, func(t *testing.T) {
			home := withHome(t)
			for _, n := range []string{"f", "e", "p", "g"} {
				if err := Put(n, Profile{URL: "https://" + n + ".test", Token: "t" + n}); err != nil {
					t.Fatal(err)
				}
			}
			if tc.global != "" {
				if err := SetActive(tc.global); err != nil {
					t.Fatal(err)
				}
			}
			if tc.project != "" {
				repo := filepath.Join(home, "repo")
				if err := os.MkdirAll(repo, 0o755); err != nil {
					t.Fatal(err)
				}
				setCwd(t, repo)
				if _, err := SetActiveLocal(tc.project); err != nil {
					t.Fatal(err)
				}
			}
			// Always set it explicitly: "" must read as unset, not as a
			// profile named "".
			t.Setenv(EnvProfile, tc.env)

			a, err := ResolveActive(tc.flag)
			if err != nil {
				t.Fatal(err)
			}
			if a.Name != tc.wantName || a.Source != tc.wantSource {
				t.Errorf("got %s/%s, want %s/%s", a.Name, a.Source, tc.wantName, tc.wantSource)
			}
		})
	}
}

// PointerActiveName answers "what would this command act on if the invocation
// named nothing?", so it must ignore the two things an invocation CAN name.
// Comparing a selection against a resolution that honors it always matches, and
// a refusal that always matches is a refusal that never fires.
func TestPointerActiveName_IgnoresFlagAndEnvButFollowsPointers(t *testing.T) {
	tests := []struct {
		name    string
		env     string
		project string // profile named by <cwd>/.praxis, "" for none
		global  string // profile named by ~/.praxis, "" for none
		want    string
	}{
		{name: "nothing set", want: "default"},
		{name: "global pointer", global: "g", want: "g"},
		{name: "project pointer beats global", project: "p", global: "g", want: "p"},
		// The divergence that matters: ResolveActive would answer "e" here.
		{name: "env is ignored", env: "e", project: "p", global: "g", want: "p"},
		{name: "env is ignored with no project", env: "e", global: "g", want: "g"},
		// A .praxis a teammate committed, or one left behind by logout, names a
		// profile this machine doesn't have. It's inert everywhere else, so it
		// must be inert here too.
		{name: "unknown project pointer falls through", project: "gone", global: "g", want: "g"},
	}

	for _, tc := range tests {
		t.Run(tc.name, func(t *testing.T) {
			home := withHome(t)
			for _, n := range []string{"e", "p", "g"} {
				if err := Put(n, Profile{URL: "https://" + n + ".test", Token: "t" + n}); err != nil {
					t.Fatal(err)
				}
			}
			if tc.global != "" {
				if err := SetActive(tc.global); err != nil {
					t.Fatal(err)
				}
			}
			if tc.project != "" {
				repo := filepath.Join(home, "repo")
				if err := os.MkdirAll(repo, 0o755); err != nil {
					t.Fatal(err)
				}
				setCwd(t, repo)
				// SetActiveLocal validates the name, so write the pointer to an
				// absent profile the way a stale checkout would: via a real pin
				// that is then removed.
				if tc.project == "gone" {
					if err := Put("gone", Profile{URL: "https://gone.test", Token: "tg"}); err != nil {
						t.Fatal(err)
					}
					if _, err := SetActiveLocal("gone"); err != nil {
						t.Fatal(err)
					}
					if _, err := Delete("gone"); err != nil {
						t.Fatal(err)
					}
				} else if _, err := SetActiveLocal(tc.project); err != nil {
					t.Fatal(err)
				}
			}
			t.Setenv(EnvProfile, tc.env)

			// The flag is a caller-side argument, not state: PointerActiveName
			// takes none, which is the property being asserted.
			got, err := PointerActiveName()
			if err != nil {
				t.Fatal(err)
			}
			if got != tc.want {
				t.Errorf("PointerActiveName() = %q, want %q", got, tc.want)
			}
		})
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
			if err := SetActive("g"); err != nil {
				t.Fatal(err)
			}
			t.Setenv(EnvProfile, blank)

			a, err := ResolveActive("")
			if err != nil {
				t.Fatal(err)
			}
			if a.Name != "g" || a.Source != SourceConfig {
				t.Errorf("got %s/%s, want g/config \u2014 blank env must not select a profile", a.Name, a.Source)
			}
		})
	}
}

// A project pointer naming an unknown profile falls back to the global chain,
// because a teammate's committed .praxis must stay inert. An ENV var must NOT
// do that: it's this session's explicit choice, and silently routing a typo to
// another org's gateway is worse than failing.
func TestResolveActive_UnknownEnvProfileFailsLoudly(t *testing.T) {
	withHome(t)
	if err := Put("g", Profile{URL: "https://g.test", Token: "tg"}); err != nil {
		t.Fatal(err)
	}
	if err := SetActive("g"); err != nil {
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
	if err := SetActive("acme"); err != nil {
		t.Fatal(err)
	}

	// Session B scopes itself to bigcorp via the environment.
	t.Setenv(EnvProfile, "bigcorp")
	before, err := ResolveActive("")
	if err != nil {
		t.Fatal(err)
	}

	// Session A switches the machine-global pointer. This is exactly what
	// `praxis profiles use` does, and it rewrites a file session B can see.
	if err := SetActive("acme"); err != nil {
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
