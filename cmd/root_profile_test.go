package cmd

import (
	"bytes"
	"fmt"
	"os"
	"strings"
	"testing"

	"github.com/Facets-cloud/praxis-cli/internal/credentials"
	"github.com/Facets-cloud/praxis-cli/internal/exitcode"
	"github.com/Facets-cloud/praxis-cli/internal/skillinstall"
)

// $PRAXIS_PROFILE now outranks both on-disk pointers, so a developer with it
// exported would silently change what this whole suite resolves. Clear it once.
func TestMain(m *testing.M) {
	os.Unsetenv(credentials.EnvProfile)
	// raptor's credentials walk starts at cwd and climbs to /, which passes
	// through the developer's real home. Start it at the (faked) HOME instead so
	// no test reads a live ~/.facets/credentials.
	restore := credentials.SetGetwdForTest(func() (string, error) { return os.Getenv("HOME"), nil })
	code := m.Run()
	restore()
	os.Exit(code)
}

// rootProfile is package state shared by every command, so any test that sets
// it must clear it or it leaks into the rest of the package.
func setRootProfile(t *testing.T, name string) {
	t.Helper()
	rootProfile = name
	t.Cleanup(func() { rootProfile = "" })
}

// The flag has to be registered ON THE ROOT as persistent, or it only works in
// one of the two positions an AI host might emit it.
func TestRootProfileFlag_IsPersistentWithShorthand(t *testing.T) {
	f := rootCmd.PersistentFlags().Lookup("profile")
	if f == nil {
		t.Fatal("--profile is not registered as a root persistent flag")
	}
	if f.Shorthand != "p" {
		t.Errorf("shorthand = %q, want p", f.Shorthand)
	}
	if f.DefValue != "" {
		t.Errorf("default = %q, want empty (resolve normally)", f.DefValue)
	}
	// Inherited by subcommands in both spellings.
	for _, c := range []string{"mcp", "memory", "duty", "status"} {
		sub, _, err := rootCmd.Find([]string{c})
		if err != nil {
			t.Fatalf("Find(%q): %v", c, err)
		}
		if sub.InheritedFlags().Lookup("profile") == nil {
			t.Errorf("%s does not inherit --profile", c)
		}
	}
	// Exactly one definition: a command redefining it locally would shadow the
	// root flag and silently split the value across two variables.
	for _, c := range []string{"login", "ig", "mcp", "status", "profiles"} {
		sub, _, err := rootCmd.Find([]string{c})
		if err != nil {
			t.Fatalf("Find(%q): %v", c, err)
		}
		// LocalFlags() alone is the check that works. Cobra excludes parent
		// persistent flags from it (via parentsPflags), so the root --profile
		// does NOT show up here -- but a subcommand's own `Flags().StringVar`
		// does, which is exactly how loginProfile/igProfile used to be
		// declared. Also requiring PersistentFlags() would make this
		// unfalsifiable: a locally-declared flag never lands there.
		if sub.LocalFlags().Lookup("profile") != nil {
			t.Errorf("%s defines its own --profile; it must use the root flag", c)
		}
	}
}

// Parsing through Execute proves cobra actually binds both positions to
// rootProfile — a unit test that assigns the var directly would not.
func TestRootProfileFlag_ParsedInEitherPosition(t *testing.T) {
	tests := []struct {
		name string
		args []string
	}{
		{name: "before the command", args: []string{"--profile", "acme", "version"}},
		{name: "after the command", args: []string{"version", "--profile", "acme"}},
		{name: "equals form", args: []string{"version", "--profile=acme"}},
		{name: "shorthand", args: []string{"-p", "acme", "version"}},
	}
	for _, tc := range tests {
		t.Run(tc.name, func(t *testing.T) {
			setRootProfile(t, "")
			var buf bytes.Buffer
			rootCmd.SetOut(&buf)
			rootCmd.SetArgs(tc.args)
			t.Cleanup(func() {
				rootCmd.SetArgs(nil)
				rootCmd.SetOut(nil)
			})
			if err := rootCmd.Execute(); err != nil {
				t.Fatalf("Execute(%v) err = %v", tc.args, err)
			}
			if rootProfile != "acme" {
				t.Errorf("rootProfile = %q after %v, want acme", rootProfile, tc.args)
			}
		})
	}
}

// status reports what the CLI would actually use, so the flag must show up
// there as profile_source=flag.
func TestStatus_HonorsRootProfileFlag(t *testing.T) {
	t.Setenv("HOME", t.TempDir())
	seedProfile(t, "default", "https://d.test", "td")
	seedProfile(t, "acme", "https://acme.test", "ta")
	setRootProfile(t, "acme")

	var buf bytes.Buffer
	statusCmd.SetOut(&buf)
	t.Cleanup(func() { statusCmd.SetOut(nil) })
	if err := statusCmd.RunE(statusCmd, nil); err != nil {
		t.Fatalf("status err = %v", err)
	}
	got := decodeMap(t, buf.String())
	if got["profile"] != "acme" {
		t.Errorf("profile = %v, want acme", got["profile"])
	}
	if got["profile_source"] != "flag" {
		t.Errorf("profile_source = %v, want flag", got["profile_source"])
	}
}

// profiles marks the profile commands would resolve to, so with the flag set
// the marker follows the flag.
func TestProfiles_HonorsRootProfileFlag(t *testing.T) {
	t.Setenv("HOME", t.TempDir())
	seedProfile(t, "default", "https://d.test", "td")
	seedProfile(t, "acme", "https://acme.test", "ta")
	setRootProfile(t, "acme")

	var buf bytes.Buffer
	profilesCmd.SetOut(&buf)
	t.Cleanup(func() { profilesCmd.SetOut(nil) })
	if err := profilesCmd.RunE(profilesCmd, nil); err != nil {
		t.Fatalf("profiles err = %v", err)
	}
	if got := decodeMap(t, buf.String()); got["active_profile"] != "acme" {
		t.Errorf("active_profile = %v, want acme", got["active_profile"])
	}
}

// activeOrAuthExit is the shared resolver behind memory, duty, ig, and agents;
// covering it covers all of them.
func TestActiveOrAuthExit_HonorsRootProfileFlag(t *testing.T) {
	t.Setenv("HOME", t.TempDir())
	seedProfile(t, "default", "https://d.test", "td")
	seedProfile(t, "acme", "https://acme.test", "ta")
	setRootProfile(t, "acme")

	active := activeOrAuthExit(&bytes.Buffer{})
	if active.Name != "acme" || active.Profile.URL != "https://acme.test" {
		t.Errorf("resolved %q at %q, want acme at its own URL", active.Name, active.Profile.URL)
	}
}

// Three commands cannot honor a --profile that names a DIFFERENT profile than
// the one they act on. Silently ignoring it is the dangerous option: `praxis
// --profile acme logout` would delete the ACTIVE profile's credentials while
// the user believed they had named acme.
func TestRootProfileFlag_RefusedWhereItCannotBeHonored(t *testing.T) {
	tests := []struct {
		name     string
		run      func(t *testing.T, out *bytes.Buffer) error
		wantWhat string
		wantHint string
	}{
		{
			name: "logout",
			run: func(t *testing.T, out *bytes.Buffer) error {
				logoutCmd.SetOut(out)
				t.Cleanup(func() { logoutCmd.SetOut(nil) })
				return logoutCmd.RunE(logoutCmd, nil)
			},
			wantWhat: "logout",
			// Not "switch to acme first": that's the double skill-cycle this
			// command's own help tells you to skip. `profiles rm` is the verb for
			// a profile you aren't on.
			wantHint: "profiles rm acme",
		},
		{
			name: "refresh-skills",
			run: func(t *testing.T, out *bytes.Buffer) error {
				refreshSkillsCmd.SetOut(out)
				t.Cleanup(func() { refreshSkillsCmd.SetOut(nil) })
				return refreshSkillsCmd.RunE(refreshSkillsCmd, nil)
			},
			wantWhat: "refresh-skills",
			wantHint: "profiles use acme",
		},
		{
			name: "profiles use",
			run: func(t *testing.T, out *bytes.Buffer) error {
				profilesUseCmd.SetOut(out)
				t.Cleanup(func() { profilesUseCmd.SetOut(nil) })
				return profilesUseCmd.RunE(profilesUseCmd, []string{"other"})
			},
			wantWhat: "profiles use",
			wantHint: "as the argument",
		},
	}

	for _, tc := range tests {
		t.Run(tc.name, func(t *testing.T) {
			t.Setenv("HOME", t.TempDir())
			resetProfilesUseFlags(t)
			seedProfile(t, "default", "https://d.test", "td")
			seedProfile(t, "acme", "https://acme.test", "ta")
			seedProfile(t, "other", "https://other.test", "tо")
			// Any refusal must happen before the network is touched.
			stubAuthMe(t, func(string, map[string]string) (*authMeResponse, error) {
				t.Error("a refused invocation must not reach the server")
				return nil, nil
			})
			call := stubPostAuthCapture(t)
			code := stubOsExit(t)
			setRootProfile(t, "acme")

			var buf bytes.Buffer
			if err := tc.run(t, &buf); err != nil {
				t.Fatalf("RunE should refuse via exit code, not error: %v", err)
			}
			if *code != exitcode.Usage {
				t.Errorf("exit code = %d, want %d", *code, exitcode.Usage)
			}
			got := decodeMap(t, buf.String())
			msg := strings.ToLower(fmt.Sprint(got["error"]))
			if !strings.Contains(msg, strings.ToLower(tc.wantWhat)) || !strings.Contains(msg, "--profile") {
				t.Errorf("error = %v, want it to name %q and --profile", got["error"], tc.wantWhat)
			}
			if !strings.Contains(fmt.Sprint(got["hint"]), tc.wantHint) {
				t.Errorf("hint = %v, want it to mention %q", got["hint"], tc.wantHint)
			}
			// Nothing may have happened.
			if got := onDiskActiveURL(t); got != "https://d.test" {
				t.Errorf("active profile moved to %q on a refused invocation", got)
			}
			if call.count != 0 {
				t.Errorf("postAuthSetup ran %d times on a refused invocation", call.count)
			}
			if store, _ := credentials.Load(); len(store) != 3 {
				t.Errorf("credential store has %d profiles, want 3 untouched", len(store))
			}
		})
	}
}

// The meta-skill's multi-profile gate is a seam skillinstall cannot fill
// itself (it must not read the credentials store, or its own tests would
// depend on the developer's ~/.praxis). cmd wires it — and if that wiring is
// ever dropped, the doctrine silently stops shipping to the users who need it
// while every skillinstall test keeps passing, because those set the seam
// directly.
func TestMultiProfileMachine_WiredToCredentialsStore(t *testing.T) {
	t.Setenv("HOME", t.TempDir())
	if skillinstall.MultiProfileMachine() {
		t.Error("empty store reports multi-profile")
	}
	seedProfile(t, "default", "https://d.test", "td")
	if skillinstall.MultiProfileMachine() {
		t.Error("one profile reports multi-profile — the typical customer would get doctrine they can't act on")
	}
	seedProfile(t, "acme", "https://acme.test", "ta")
	if !skillinstall.MultiProfileMachine() {
		t.Error("two profiles report single-profile — the meta-skill would omit the doctrine that matters")
	}
}

// The refusal exists to stop DIVERGENCE, not to police the flag's presence.
// `praxis -p default logout` on the single-profile machine that every customer
// has asks for precisely what a bare `logout` does, so refusing it — with
// "default is not another profile" and a hint to switch to the profile you are
// already on — was an error message about nothing.
func TestProfileSelection_AllowedWhenItNamesTheProfileActedOn(t *testing.T) {
	tests := []struct {
		name string
		// env selects via $PRAXIS_PROFILE instead of -p; both are refused on
		// divergence, so both have to be allowed on agreement.
		env    bool
		setup  func(t *testing.T) (selection string)
		run    func(t *testing.T, out *bytes.Buffer) error
		verify func(t *testing.T, call *postAuthCall)
	}{
		{
			// The single-profile customer: one profile, and it's `default`.
			name: "logout names the only profile there is",
			setup: func(t *testing.T) string {
				seedProfile(t, "default", "https://d.test", "td")
				return "default"
			},
			run: func(t *testing.T, out *bytes.Buffer) error {
				logoutCmd.SetOut(out)
				t.Cleanup(func() { logoutCmd.SetOut(nil) })
				return logoutCmd.RunE(logoutCmd, nil)
			},
			verify: func(t *testing.T, _ *postAuthCall) {
				if store, _ := credentials.Load(); len(store) != 0 {
					t.Errorf("logout left %d profile(s); it should have removed the one it named", len(store))
				}
			},
		},
		{
			// An exported PRAXIS_PROFILE is the normal state for a session
			// scoped to one deployment; it must not make logout unusable there.
			name: "logout under a matching $PRAXIS_PROFILE",
			env:  true,
			setup: func(t *testing.T) string {
				seedProfile(t, "default", "https://d.test", "td")
				seedProfile(t, "acme", "https://acme.test", "ta")
				if _, err := credentials.SetDefault("acme"); err != nil {
					t.Fatal(err)
				}
				return "acme"
			},
			run: func(t *testing.T, out *bytes.Buffer) error {
				logoutCmd.SetOut(out)
				t.Cleanup(func() { logoutCmd.SetOut(nil) })
				return logoutCmd.RunE(logoutCmd, nil)
			},
			verify: func(t *testing.T, _ *postAuthCall) {
				// [default] was the copy of acme the environment named; both hold
				// the credentials being removed, so both go — leaving [acme] would
				// make it the sole section and log the user straight back in.
				store, _ := credentials.Load()
				if _, gone := store["default"]; gone {
					t.Error("logout kept the active [default] copy")
				}
				if _, kept := store["acme"]; kept {
					t.Error("logout kept acme, the section [default] was a copy of; the user is still logged in")
				}
			},
		},
		{
			name: "refresh-skills names the active profile",
			setup: func(t *testing.T) string {
				seedProfile(t, "default", "https://d.test", "td")
				seedProfile(t, "acme", "https://acme.test", "ta")
				if _, err := credentials.SetDefault("acme"); err != nil {
					t.Fatal(err)
				}
				return "acme"
			},
			run: func(t *testing.T, out *bytes.Buffer) error {
				refreshSkillsCmd.SetOut(out)
				t.Cleanup(func() { refreshSkillsCmd.SetOut(nil) })
				return refreshSkillsCmd.RunE(refreshSkillsCmd, nil)
			},
			verify: func(t *testing.T, call *postAuthCall) {
				if call.count != 1 {
					t.Fatalf("postAuthSetup ran %d times, want 1 — the re-sync must happen", call.count)
				}
				if call.baseURL != "https://acme.test" {
					t.Errorf("re-synced against %q, want the named profile's URL", call.baseURL)
				}
			},
		},
		{
			// refresh-skills installs into the ACTIVE ROOT, so inside a pinned
			// tree the profile it acts on is the tree's [default] — a copy of
			// the pinned profile. Naming that profile is not a divergence.
			name: "refresh-skills names the project-pinned profile",
			setup: func(t *testing.T) string {
				seedPAT(t, "default", "https://d.test", "td")
				seedPAT(t, "pinned", "https://pinned.test", "tp")
				repo := repoUnderHome(t, os.Getenv("HOME"))
				if err := credentials.SetDefaultLocal("pinned", repo); err != nil {
					t.Fatal(err)
				}
				inDir(t, repo)
				return "pinned"
			},
			run: func(t *testing.T, out *bytes.Buffer) error {
				refreshSkillsCmd.SetOut(out)
				t.Cleanup(func() { refreshSkillsCmd.SetOut(nil) })
				return refreshSkillsCmd.RunE(refreshSkillsCmd, nil)
			},
			verify: func(t *testing.T, call *postAuthCall) {
				if call.count != 1 {
					t.Fatalf("postAuthSetup ran %d times, want 1", call.count)
				}
				if call.baseURL != "https://pinned.test" {
					t.Errorf("re-synced against %q, want the pinned profile's URL", call.baseURL)
				}
			},
		},
		{
			// One answer given twice is not two answers.
			name: "profiles use with -p naming the same target",
			setup: func(t *testing.T) string {
				seedProfile(t, "default", "https://d.test", "td")
				seedProfile(t, "acme", "https://acme.test", "ta")
				okAuthMe(t, "u@acme.test")
				return "acme"
			},
			run: func(t *testing.T, out *bytes.Buffer) error {
				profilesUseCmd.SetOut(out)
				t.Cleanup(func() { profilesUseCmd.SetOut(nil) })
				return profilesUseCmd.RunE(profilesUseCmd, []string{"acme"})
			},
			verify: func(t *testing.T, call *postAuthCall) {
				if got := onDiskActiveURL(t); got != "https://acme.test" {
					t.Errorf("active profile = %q, want the switch to have happened", got)
				}
				if call.count != 1 {
					t.Errorf("postAuthSetup ran %d times, want 1", call.count)
				}
			},
		},
	}

	for _, tc := range tests {
		t.Run(tc.name, func(t *testing.T) {
			t.Setenv("HOME", t.TempDir())
			resetProfilesUseFlags(t)
			call := stubPostAuthCapture(t)
			code := stubOsExit(t)

			selection := tc.setup(t)
			if tc.env {
				t.Setenv(credentials.EnvProfile, selection)
			} else {
				setRootProfile(t, selection)
			}

			var buf bytes.Buffer
			if err := tc.run(t, &buf); err != nil {
				t.Fatalf("RunE err = %v", err)
			}
			if *code == exitcode.Usage {
				t.Fatalf("refused a selection that named the profile acted on; output:\n%s", buf.String())
			}
			if *code != -1 {
				t.Fatalf("exited %d; output:\n%s", *code, buf.String())
			}
			tc.verify(t, call)
		})
	}
}

// A guard and its action must be the same decision. Divergence-only refusal
// opened a hole the blanket refusal had made unreachable: the guard compared
// the POINTER (correctly ignoring flag and env), while the action downstream
// re-resolved the FULL CHAIN. So `-p default` satisfied the guard and
// $PRAXIS_PROFILE=acme still won the action — logout deleted acme, the one
// profile the user had not named, while wiping default's org skills.
//
// Two independent defenses, both asserted here: the guard now checks the flag
// and the environment separately rather than only the flag-wins winner, and the
// action reuses the name the guard approved.
func TestProfileSelection_MatchingFlagCannotSmuggleADivergingEnv(t *testing.T) {
	tests := []struct {
		name   string
		run    func(t *testing.T, out *bytes.Buffer) error
		verify func(t *testing.T, call *postAuthCall)
	}{
		{
			name: "logout",
			run: func(t *testing.T, out *bytes.Buffer) error {
				logoutCmd.SetOut(out)
				t.Cleanup(func() { logoutCmd.SetOut(nil) })
				return logoutCmd.RunE(logoutCmd, nil)
			},
			verify: func(t *testing.T, _ *postAuthCall) {
				// The refusal must be total: nothing removed, either profile.
				store, _ := credentials.Load()
				for _, want := range []string{"default", "acme"} {
					if _, ok := store[want]; !ok {
						t.Errorf("profile %q was deleted; the refusal must change nothing", want)
					}
				}
			},
		},
		{
			name: "refresh-skills",
			run: func(t *testing.T, out *bytes.Buffer) error {
				refreshSkillsCmd.SetOut(out)
				t.Cleanup(func() { refreshSkillsCmd.SetOut(nil) })
				return refreshSkillsCmd.RunE(refreshSkillsCmd, nil)
			},
			verify: func(t *testing.T, call *postAuthCall) {
				if call.count != 0 {
					t.Errorf("synced %d time(s) against %q; the refusal must install nothing",
						call.count, call.baseURL)
				}
			},
		},
	}

	for _, tc := range tests {
		t.Run(tc.name, func(t *testing.T) {
			t.Setenv("HOME", t.TempDir())
			call := stubPostAuthCapture(t)
			code := stubOsExit(t)
			seedProfile(t, "default", "https://d.test", "td")
			seedProfile(t, "acme", "https://acme.test", "ta")

			// The flag agrees with the pointer, so the old guard waved it
			// through; the env names something else and used to win downstream.
			setRootProfile(t, "default")
			t.Setenv(credentials.EnvProfile, "acme")

			var buf bytes.Buffer
			if err := tc.run(t, &buf); err != nil {
				t.Fatalf("RunE err = %v", err)
			}
			if *code != exitcode.Usage {
				t.Fatalf("exit = %d, want %d (usage) — a diverging env must be refused even beside a matching flag; output:\n%s",
					*code, exitcode.Usage, buf.String())
			}
			// The message must name the mechanism that actually diverged, or it
			// sends the user to look at the flag they got right.
			if !strings.Contains(buf.String(), credentials.EnvProfile) {
				t.Errorf("refusal doesn't name %s as the diverging selection:\n%s", credentials.EnvProfile, buf.String())
			}
			tc.verify(t, call)
		})
	}
}

// The allowed half of the same scenario: an environment that AGREES with the
// pointer gets through the guard, so the action really runs, and it must land on
// that profile and no other.
//
// This does not isolate the action-side fix. Once the guard checks both
// mechanisms, no cobra-reachable input reaches the action with a diverging env,
// so acting on the pointer is defense-in-depth and only mutation testing can
// observe it alone (verified that way: with the guard reverted to the flag-wins
// winner, the action-side fix still deletes default and still syncs default's
// URL). What this test does pin is that neither fix broke the case that must
// keep working — a session scoped to the profile it is acting on.
func TestLogoutAndRefresh_ActOnThePointerUnderAMatchingEnv(t *testing.T) {
	t.Run("logout deletes the pointer's profile", func(t *testing.T) {
		t.Setenv("HOME", t.TempDir())
		stubPostAuthCapture(t)
		code := stubOsExit(t)
		seedProfile(t, "default", "https://d.test", "td")
		seedProfile(t, "acme", "https://acme.test", "ta")
		// Env matches the pointer, so the guard allows it and the action runs.
		t.Setenv(credentials.EnvProfile, "default")

		var buf bytes.Buffer
		logoutCmd.SetOut(&buf)
		t.Cleanup(func() { logoutCmd.SetOut(nil) })
		if err := logoutCmd.RunE(logoutCmd, nil); err != nil {
			t.Fatalf("RunE err = %v", err)
		}
		if *code != -1 {
			t.Fatalf("exit = %d, want no exit; output:\n%s", *code, buf.String())
		}
		store, _ := credentials.Load()
		if _, gone := store["default"]; gone {
			t.Error("logout did not remove default, the profile the pointer names")
		}
		if _, kept := store["acme"]; !kept {
			t.Error("logout removed acme; it must only ever touch the pointer's profile")
		}
	})

	t.Run("refresh-skills syncs the pointer's URL", func(t *testing.T) {
		t.Setenv("HOME", t.TempDir())
		call := stubPostAuthCapture(t)
		code := stubOsExit(t)
		seedProfile(t, "default", "https://d.test", "td")
		seedProfile(t, "acme", "https://acme.test", "ta")
		t.Setenv(credentials.EnvProfile, "default")

		var buf bytes.Buffer
		refreshSkillsCmd.SetOut(&buf)
		t.Cleanup(func() { refreshSkillsCmd.SetOut(nil) })
		if err := refreshSkillsCmd.RunE(refreshSkillsCmd, nil); err != nil {
			t.Fatalf("RunE err = %v", err)
		}
		if *code != -1 {
			t.Fatalf("exit = %d, want no exit; output:\n%s", *code, buf.String())
		}
		if call.count != 1 || call.baseURL != "https://d.test" {
			t.Errorf("synced %d time(s) against %q, want 1 against default's URL", call.count, call.baseURL)
		}
	})
}

// ─── per-session scoping via $PRAXIS_PROFILE ───────────────────────────

// The whole point: two concurrent agent sessions must not be able to move each
// other. Session B scopes itself with the env var, session A switches the
// machine-global pointer, B must not budge.
func TestEnvProfile_IsolatesConcurrentSessions(t *testing.T) {
	t.Setenv("HOME", t.TempDir())
	seedProfile(t, "acme", "https://acme.test", "ta")
	seedProfile(t, "bigcorp", "https://bigcorp.test", "tb")
	if _, err := credentials.SetDefault("acme"); err != nil {
		t.Fatal(err)
	}

	// Session B, scoped by environment.
	t.Setenv(credentials.EnvProfile, "bigcorp")
	if a := activeOrAuthExit(&bytes.Buffer{}); a.Name != "bigcorp" {
		t.Fatalf("session B resolved %q, want bigcorp", a.Name)
	}

	// Session A performs the machine-global switch (what `profiles use` writes).
	if _, err := credentials.SetDefault("acme"); err != nil {
		t.Fatal(err)
	}

	a := activeOrAuthExit(&bytes.Buffer{})
	if a.Name != "bigcorp" || a.Profile.URL != "https://bigcorp.test" {
		t.Errorf("session B moved to %q (%s) after another session switched", a.Name, a.Profile.URL)
	}
	if a.Source != credentials.SourceEnv {
		t.Errorf("source = %v, want env", a.Source)
	}
}

func TestStatus_ReportsEnvAsProfileSource(t *testing.T) {
	t.Setenv("HOME", t.TempDir())
	seedProfile(t, "default", "https://d.test", "td")
	seedProfile(t, "acme", "https://acme.test", "ta")
	t.Setenv(credentials.EnvProfile, "acme")

	var buf bytes.Buffer
	statusCmd.SetOut(&buf)
	t.Cleanup(func() { statusCmd.SetOut(nil) })
	if err := statusCmd.RunE(statusCmd, nil); err != nil {
		t.Fatalf("status err = %v", err)
	}
	got := decodeMap(t, buf.String())
	if got["profile"] != "acme" || got["profile_source"] != "env" {
		t.Errorf("profile/source = %v/%v, want acme/env", got["profile"], got["profile_source"])
	}
}

// -p must win over the environment, so one command can still escape a
// session-wide scope.
func TestRootProfileFlag_OutranksEnv(t *testing.T) {
	t.Setenv("HOME", t.TempDir())
	seedProfile(t, "envone", "https://envone.test", "te")
	seedProfile(t, "flagone", "https://flagone.test", "tf")
	t.Setenv(credentials.EnvProfile, "envone")
	setRootProfile(t, "flagone")

	if a := activeOrAuthExit(&bytes.Buffer{}); a.Name != "flagone" || a.Source != credentials.SourceFlag {
		t.Errorf("resolved %s/%s, want flagone/flag", a.Name, a.Source)
	}
}

// logout and refresh-skills rewrite the ACTIVE profile's state, so they refuse
// an env selection exactly as they refuse the flag — with a hint naming the
// variable, since the user may not realize it's exported.
func TestEnvProfile_RefusedByActiveProfileMutators(t *testing.T) {
	tests := []struct {
		name string
		run  func(t *testing.T, out *bytes.Buffer) error
	}{
		{
			name: "logout",
			run: func(t *testing.T, out *bytes.Buffer) error {
				logoutCmd.SetOut(out)
				t.Cleanup(func() { logoutCmd.SetOut(nil) })
				return logoutCmd.RunE(logoutCmd, nil)
			},
		},
		{
			name: "refresh-skills",
			run: func(t *testing.T, out *bytes.Buffer) error {
				refreshSkillsCmd.SetOut(out)
				t.Cleanup(func() { refreshSkillsCmd.SetOut(nil) })
				return refreshSkillsCmd.RunE(refreshSkillsCmd, nil)
			},
		},
	}
	for _, tc := range tests {
		t.Run(tc.name, func(t *testing.T) {
			t.Setenv("HOME", t.TempDir())
			seedProfile(t, "default", "https://d.test", "td")
			seedProfile(t, "acme", "https://acme.test", "ta")
			t.Setenv(credentials.EnvProfile, "acme")
			call := stubPostAuthCapture(t)
			code := stubOsExit(t)

			var buf bytes.Buffer
			if err := tc.run(t, &buf); err != nil {
				t.Fatalf("RunE err = %v", err)
			}
			if *code != exitcode.Usage {
				t.Errorf("exit code = %d, want %d", *code, exitcode.Usage)
			}
			got := decodeMap(t, buf.String())
			if !strings.Contains(fmt.Sprint(got["error"]), credentials.EnvProfile) {
				t.Errorf("error = %v, want it to name %s", got["error"], credentials.EnvProfile)
			}
			if !strings.Contains(fmt.Sprint(got["hint"]), "unset "+credentials.EnvProfile) {
				t.Errorf("hint = %v, want it to say how to unset the variable", got["hint"])
			}
			if call.count != 0 {
				t.Errorf("postAuthSetup ran %d times on a refused invocation", call.count)
			}
			if store, _ := credentials.Load(); len(store) != 2 {
				t.Errorf("store has %d profiles, want 2 untouched", len(store))
			}
		})
	}
}

// `profiles use` must NOT refuse the env var: naming a target positionally
// while this session is scoped elsewhere is a legitimate "change the default
// for everyone else". It does have to say the switch won't apply HERE, or the
// command looks like a no-op.
func TestProfilesUse_EnvShadowsTheSwitchButIsAllowed(t *testing.T) {
	t.Setenv("HOME", t.TempDir())
	resetProfilesUseFlags(t)
	seedProfile(t, "default", "https://d.test", "td")
	seedProfile(t, "acme", "https://acme.test", "ta")
	seedProfile(t, "scoped", "https://scoped.test", "ts")
	t.Setenv(credentials.EnvProfile, "scoped")
	okAuthMe(t, "u@x")
	stubPostAuthCapture(t)

	out, err := runProfilesUse(t, "acme")
	if err != nil {
		t.Fatalf("profiles use should be allowed with the env var set: %v", err)
	}
	got := decodeMap(t, out)
	if got["profile"] != "acme" {
		t.Errorf("profile = %v, want acme", got["profile"])
	}
	if got["shadowed_by_env"] != credentials.EnvProfile+"=scoped" {
		t.Errorf("shadowed_by_env = %v, want %s=scoped", got["shadowed_by_env"], credentials.EnvProfile)
	}
	if got["effective_profile"] != "scoped" {
		t.Errorf("effective_profile = %v, want scoped — this session still uses the env profile", got["effective_profile"])
	}
}

// A global switch changes every session on the machine and replaces the org
// skills on disk. The output must say so, or a user running several agent
// sessions has no way to know they just disrupted the others.
func TestProfilesUse_GlobalSwitchAnnouncesMachineWideScope(t *testing.T) {
	t.Setenv("HOME", t.TempDir())
	resetProfilesUseFlags(t)
	seedProfile(t, "default", "https://d.test", "td")
	seedProfile(t, "acme", "https://acme.test", "ta")
	okAuthMe(t, "u@x")
	stubPostAuthCapture(t)

	out, err := runProfilesUse(t, "acme")
	if err != nil {
		t.Fatal(err)
	}
	note := fmt.Sprint(decodeMap(t, out)["scope_note"])
	for _, want := range []string{"machine-global", credentials.EnvProfile + "=acme"} {
		if !strings.Contains(note, want) {
			t.Errorf("scope_note = %q, want it to mention %q", note, want)
		}
	}
}

// … and must NOT say so when there is only one profile. Nothing was disrupted:
// every session resolved to this profile before the command and still does. The
// note would be a warning about a hazard the user cannot have, and it teaches
// PRAXIS_PROFILE — whose only effect on a one-profile machine is to name the
// same profile a second time.
func TestProfilesUse_SingleProfileReSyncOmitsMachineWideScope(t *testing.T) {
	t.Setenv("HOME", t.TempDir())
	resetProfilesUseFlags(t)
	seedProfile(t, "default", "https://d.test", "td")
	okAuthMe(t, "u@x")
	call := stubPostAuthCapture(t)

	out, err := runProfilesUse(t, "default")
	if err != nil {
		t.Fatal(err)
	}
	got := decodeMap(t, out)
	if _, present := got["scope_note"]; present {
		t.Errorf("scope_note = %v on a single-profile machine; there is no other session to disturb", got["scope_note"])
	}
	// The re-sync itself still has to happen — gating the note must not gate the
	// work.
	if call.count != 1 {
		t.Errorf("postAuthSetup ran %d times, want 1", call.count)
	}
	if got["profile"] != "default" {
		t.Errorf("profile = %v, want default", got["profile"])
	}
}

func TestLogin_ReadsEnvProfile(t *testing.T) {
	isolateHome(t)
	resetLoginFlags(t)
	seedProfile(t, "acme", "https://acme.test", "ta")
	t.Setenv(credentials.EnvProfile, "acme")
	stubAuthMe(t, func(baseURL string, _ map[string]string) (*authMeResponse, error) {
		return &authMeResponse{Email: "u@x", canonicalBaseURL: baseURL}, nil
	})
	stubPostAuth(t)

	out, err := runLoginRunE(t)
	if err != nil {
		t.Fatalf("login err = %v", err)
	}
	if got := decodeMap(t, out); got["profile"] != "acme" {
		t.Errorf("profile = %v, want acme — a scoped session should log into its own profile", got["profile"])
	}
}

// `--profile X --all` is a contradiction ("that one" vs "every one"), and the
// refusal must come BEFORE the --all branch — honoring --all while ignoring
// --profile would wipe EVERY profile for a user who named exactly one.
func TestLogout_RefusesProfileFlagBeforeWipingAll(t *testing.T) {
	t.Setenv("HOME", t.TempDir())
	seedProfile(t, "default", "https://d.test", "td")
	seedProfile(t, "acme", "https://acme.test", "ta")
	code := stubOsExit(t)
	setRootProfile(t, "acme")

	logoutAll = true
	t.Cleanup(func() { logoutAll = false })

	var buf bytes.Buffer
	logoutCmd.SetOut(&buf)
	t.Cleanup(func() { logoutCmd.SetOut(nil) })
	if err := logoutCmd.RunE(logoutCmd, nil); err != nil {
		t.Fatalf("RunE err = %v", err)
	}

	if *code != exitcode.Usage {
		t.Errorf("exit code = %d, want %d", *code, exitcode.Usage)
	}
	store, err := credentials.Load()
	if err != nil {
		t.Fatal(err)
	}
	if len(store) != 2 {
		t.Errorf("credential store has %d profiles, want 2 — --all ran despite the refusal", len(store))
	}
}

// login has no --profile of its own any more: it reads the root flag, so both
// spellings name the profile to create or update.
func TestLogin_ReadsRootProfileFlag(t *testing.T) {
	isolateHome(t)
	resetLoginFlags(t)
	seedProfile(t, "acme", "https://acme.test", "ta")
	stubAuthMe(t, func(baseURL string, _ map[string]string) (*authMeResponse, error) {
		return &authMeResponse{Email: "u@x", canonicalBaseURL: baseURL}, nil
	})
	stubPostAuth(t)
	rootProfile = "acme" // resetLoginFlags clears it on cleanup

	out, err := runLoginRunE(t)
	if err != nil {
		t.Fatalf("login err = %v", err)
	}
	if got := decodeMap(t, out); got["profile"] != "acme" {
		t.Errorf("profile = %v, want acme (login must read the root flag)", got["profile"])
	}
	if got := onDiskActiveURL(t); got != "https://acme.test" {
		t.Errorf("active profile = %q, want acme's control plane", got)
	}
}

// The flag must sit at the TOP of the resolution chain — above the store's
// own [default] — so one command can escape a local-mode tree's pin. Inside
// that tree the store is the tree's file, so the flagged name resolves from
// there (raptor's rule): a name only the home file has does not load.
func TestRootProfileFlag_OutranksTreeDefault(t *testing.T) {
	home := t.TempDir()
	t.Setenv("HOME", home)
	clearFacetsEnv(t)
	seedPAT(t, "pinned", "https://pinned.test", "tp")
	seedPAT(t, "override", "https://override.test", "to")
	repo := repoUnderHome(t, home)
	if err := credentials.SetDefaultLocal("pinned", repo); err != nil {
		t.Fatal(err)
	}
	if err := credentials.PutLocal("override", credentials.FacetsProfile("https://override.test", "u@x", "to"), repo); err != nil {
		t.Fatal(err)
	}
	inDir(t, repo)

	// Baseline: inside the tree, the tree's [default] wins.
	if a, _ := credentials.ResolveActive(rootProfile); a.Profile.URL != "https://pinned.test" {
		t.Fatalf("without the flag, resolution = %+v, want pinned", a)
	}

	setRootProfile(t, "override")
	active, err := credentials.ResolveActive(rootProfile)
	if err != nil {
		t.Fatal(err)
	}
	if active.Name != "override" || active.Source != credentials.SourceFlag || active.Profile.URL != "https://override.test" {
		t.Errorf("with --profile, resolution = %+v, want override/flag", active)
	}
	// Nothing on disk moved: the flag is per-invocation only.
	if a, _ := credentials.ResolveActive(""); a.Profile.URL != "https://pinned.test" {
		t.Errorf("after the flagged call, on-disk resolution = %+v; the flag must not persist", a)
	}
}
