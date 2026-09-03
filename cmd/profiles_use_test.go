package cmd

import (
	"bytes"
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"os"
	"path/filepath"
	"strings"
	"testing"

	"github.com/Facets-cloud/praxis-cli/internal/credentials"
	"github.com/Facets-cloud/praxis-cli/internal/exitcode"
	"github.com/Facets-cloud/praxis-cli/internal/paths"
	"github.com/spf13/cobra"
)

// ─── helpers ─────────────────────────────────────────────────────────────

func resetProfilesUseFlags(t *testing.T) {
	t.Helper()
	// rootProfile too: `profiles use` REFUSES an explicitly selected profile,
	// so a value leaked from another test would push every case here down the
	// exit-2 path and fail for a reason that has nothing to do with the test.
	rootProfile = ""
	profilesUseJSON, profilesUseLocal = false, false
	t.Cleanup(func() {
		rootProfile = ""
		profilesUseJSON, profilesUseLocal = false, false
	})
}

// postAuthCall records what postAuthSetup was invoked with — including the
// ActiveRoot in force at that moment, which IS the install-scope decision the
// command made. Asserting on it is how the scope invariant gets tested
// without actually writing skills to disk.
type postAuthCall struct {
	count      int
	baseURL    string
	auth       map[string]string
	activeRoot string
}

func stubPostAuthCapture(t *testing.T) *postAuthCall {
	t.Helper()
	call := &postAuthCall{}
	orig := postAuthSetup
	postAuthSetup = func(out io.Writer, asJSON bool, baseURL string, auth map[string]string) postAuthState {
		call.count++
		call.baseURL, call.auth = baseURL, auth
		if root, err := paths.ActiveRoot(); err == nil {
			call.activeRoot = root
		}
		return postAuthState{}
	}
	t.Cleanup(func() { postAuthSetup = orig })
	return call
}

// okAuthMe stubs a server that vouches for any token and reports the URL it
// was called with as canonical (i.e. no redirect).
func okAuthMe(t *testing.T, email string) {
	t.Helper()
	stubAuthMe(t, func(baseURL string, _ map[string]string) (*authMeResponse, error) {
		return &authMeResponse{Email: email, canonicalBaseURL: baseURL}, nil
	})
}

func runProfilesUse(t *testing.T, name string) (string, error) {
	t.Helper()
	var buf bytes.Buffer
	profilesUseCmd.SetOut(&buf)
	err := profilesUseCmd.RunE(profilesUseCmd, []string{name})
	return buf.String(), err
}

func decodeMap(t *testing.T, s string) map[string]any {
	t.Helper()
	var m map[string]any
	if err := json.Unmarshal([]byte(s), &m); err != nil {
		t.Fatalf("output should be valid JSON: %v\noutput:\n%s", err, s)
	}
	return m
}

// repoUnderHome creates <home>/repo and points project-root discovery at it.
// The credentials walk is left at HOME (see TestMain); tests that need the
// tree's store visible call inDir.
func repoUnderHome(t *testing.T, home string) string {
	t.Helper()
	repo := filepath.Join(home, "repo")
	if err := os.MkdirAll(repo, 0o755); err != nil {
		t.Fatal(err)
	}
	t.Cleanup(paths.SetGetwdForTest(func() (string, error) { return repo, nil }))
	return repo
}

// ─── happy paths ─────────────────────────────────────────────────────────

func TestProfilesUse_GlobalSwitch_FlipsPointerAndSyncs(t *testing.T) {
	home := t.TempDir()
	t.Setenv("HOME", home)
	resetProfilesUseFlags(t)

	seedProfile(t, "default", "https://d.test", "td")
	seedProfile(t, "acme", "https://acme.test", "ta")
	okAuthMe(t, "u@x")
	call := stubPostAuthCapture(t)

	out, err := runProfilesUse(t, "acme")
	if err != nil {
		t.Fatalf("RunE err = %v", err)
	}

	if got := onDiskActiveURL(t); got != "https://acme.test" {
		t.Errorf("active control plane = %q, want acme's", got)
	}
	// The sync must run against the NEW profile's deployment and token —
	// otherwise the pointer and the installed skills disagree.
	if call.count != 1 {
		t.Fatalf("postAuthSetup called %d times, want 1", call.count)
	}
	// Assert the full auth header, not a bare token: a control-plane PAT
	// profile must reach postAuthSetup as Bearer + X-Facets-Username.
	if call.baseURL != "https://acme.test" || call.auth["Authorization"] != "Bearer ta" {
		t.Errorf("sync ran with (%q, %q), want acme's url + bearer token", call.baseURL, call.auth["Authorization"])
	}
	if want := filepath.Join(home, ".praxis"); call.activeRoot != want {
		t.Errorf("activeRoot during sync = %q, want home root %q", call.activeRoot, want)
	}

	got := decodeMap(t, out)
	if got["profile"] != "acme" || got["previous_profile"] != "default" {
		t.Errorf("profile/previous_profile = %v/%v, want acme/default", got["profile"], got["previous_profile"])
	}
	if got["scope"] != "global" {
		t.Errorf("scope = %v, want global", got["scope"])
	}
	if got["url"] != "https://acme.test" || got["username"] != "u@x" {
		t.Errorf("url/username = %v/%v", got["url"], got["username"])
	}
	if _, ok := got["shadowed_by_project_root"]; ok {
		t.Error("no project pointer exists; shadowed_by_project_root must be absent")
	}
}

// Switching to the profile that is already active is a legitimate re-sync
// (equivalent to refresh-skills), not an error.
func TestProfilesUse_SameProfile_ReSyncs(t *testing.T) {
	t.Setenv("HOME", t.TempDir())
	resetProfilesUseFlags(t)

	seedProfile(t, "acme", "https://acme.test", "ta")
	if _, err := credentials.SetDefault("acme"); err != nil {
		t.Fatal(err)
	}
	okAuthMe(t, "u@x")
	call := stubPostAuthCapture(t)

	out, err := runProfilesUse(t, "acme")
	if err != nil {
		t.Fatalf("RunE err = %v", err)
	}
	if call.count != 1 {
		t.Errorf("postAuthSetup called %d times, want 1 (re-sync)", call.count)
	}
	if got := decodeMap(t, out); got["previous_profile"] != "acme" {
		t.Errorf("previous_profile = %v, want acme", got["previous_profile"])
	}
}

func TestProfilesUse_Local_PinsTreeAndLeavesGlobalAlone(t *testing.T) {
	home := t.TempDir()
	t.Setenv("HOME", home)
	resetProfilesUseFlags(t)
	clearFacetsEnv(t)

	seedPAT(t, "default", "https://d.test", "td")
	seedPAT(t, "acme", "https://acme.test", "ta")
	repo := repoUnderHome(t, home)
	okAuthMe(t, "u@x")
	call := stubPostAuthCapture(t)

	profilesUseLocal = true
	out, err := runProfilesUse(t, "acme")
	if err != nil {
		t.Fatalf("RunE err = %v", err)
	}

	tree := readFacetsFile(t, repo)
	if tree["acme"]["token"] != "ta" || tree["default"]["token"] != "ta" {
		t.Errorf("tree file = %v, want acme and a default copy", tree)
	}
	inDir(t, repo)
	if a, _ := credentials.ResolveActive(""); a.Name != "default" || a.Profile.URL != "https://acme.test" {
		t.Errorf("in-repo resolution = %+v, want default with acme's credentials", a)
	}
	if got := homeFacetsURL(t); got != "https://d.test" {
		t.Errorf("home [default] = %q; --local must not switch it", got)
	}
	// Skills must install project-scoped, i.e. the receipt follows the repo.
	if want := filepath.Join(repo, ".praxis"); call.activeRoot != want {
		t.Errorf("activeRoot during sync = %q, want project root %q", call.activeRoot, want)
	}
	got := decodeMap(t, out)
	if got["scope"] != "local" {
		t.Errorf("scope = %v, want local", got["scope"])
	}
	if got["project_root"] != filepath.Join(repo, ".praxis") {
		t.Errorf("project_root = %v", got["project_root"])
	}
}

// A global switch made from inside a local-mode tree must (a) still install
// user-level — never into the repo pinned to another profile — and (b) tell
// the user their cwd still resolves to the tree's profile. Inside the tree the
// shared store is the tree's file, so the target has to be visible there: a
// Praxis API key (always global) is.
func TestProfilesUse_GlobalSwitch_InsideLocalTree_IsScopedToHomeAndFlagged(t *testing.T) {
	home := t.TempDir()
	t.Setenv("HOME", home)
	resetProfilesUseFlags(t)
	clearFacetsEnv(t)

	seedPAT(t, "default", "https://d.test", "td")
	seedPAT(t, "acme", "https://acme.test", "ta")
	seedProfile(t, "vymo", "https://vymo.test", "tv")
	repo := repoUnderHome(t, home)
	if err := credentials.SetDefaultLocal("acme", repo); err != nil {
		t.Fatal(err)
	}
	inDir(t, repo)
	okAuthMe(t, "u@x")
	call := stubPostAuthCapture(t)

	out, err := runProfilesUse(t, "vymo")
	if err != nil {
		t.Fatalf("RunE err = %v", err)
	}

	// The HOME default is now vymo (an API key, in the praxis file)...
	if praxis := readPraxisFile(t); praxis["default"]["token"] != "tv" {
		t.Errorf("home default = %v, want vymo's API key", praxis["default"])
	}
	// ...and the repo stays pinned to acme — a global switch must not repin it.
	if a, _ := credentials.ResolveActive(""); a.Profile.URL != "https://acme.test" {
		t.Errorf("in-repo resolution = %+v, want acme (the tree's file wins)", a)
	}
	if want := filepath.Join(home, ".praxis"); call.activeRoot != want {
		t.Errorf("activeRoot during sync = %q, want home root %q — a global switch must not install into a repo pinned to another profile", call.activeRoot, want)
	}
	got := decodeMap(t, out)
	if got["shadowed_by_project_root"] != filepath.Join(repo, ".praxis") {
		t.Errorf("shadowed_by_project_root = %v, want %s", got["shadowed_by_project_root"], filepath.Join(repo, ".praxis"))
	}
	// The tree's own [default] (a copy of acme) is what commands here use.
	if got["effective_profile"] != "default" {
		t.Errorf("effective_profile = %v, want default (the tree's section)", got["effective_profile"])
	}
}

// A stale stored host self-heals to the post-redirect one, so later MCP
// invokes through this profile don't pay the redirect (issue #19-A).
func TestProfilesUse_HealsCanonicalURL(t *testing.T) {
	t.Setenv("HOME", t.TempDir())
	resetProfilesUseFlags(t)

	seedProfile(t, "acme", "https://apex.test", "ta")
	stubAuthMe(t, func(baseURL string, _ map[string]string) (*authMeResponse, error) {
		return &authMeResponse{Email: "u@x", canonicalBaseURL: "https://www.apex.test"}, nil
	})
	call := stubPostAuthCapture(t)

	out, err := runProfilesUse(t, "acme")
	if err != nil {
		t.Fatalf("RunE err = %v", err)
	}
	store, err := credentials.Load()
	if err != nil {
		t.Fatal(err)
	}
	if store["acme"].URL != "https://www.apex.test" {
		t.Errorf("stored URL = %q, want the canonical host", store["acme"].URL)
	}
	if store["acme"].Token != "ta" {
		t.Errorf("token clobbered by the URL heal: %q", store["acme"].Token)
	}
	if call.baseURL != "https://www.apex.test" {
		t.Errorf("sync ran against %q, want the canonical host", call.baseURL)
	}
	if got := decodeMap(t, out); got["url"] != "https://www.apex.test" {
		t.Errorf("reported url = %v, want canonical", got["url"])
	}
}

// ─── refusals: nothing may change ────────────────────────────────────────

// Every refusal shares one contract: exit with the right code AND leave the
// pointer and the installed skill set exactly as they were.
func TestProfilesUse_Refusals_LeaveStateUntouched(t *testing.T) {
	tests := []struct {
		name     string
		target   string
		seed     func(t *testing.T)
		authMe   func(t *testing.T)
		wantCode int
		wantMsg  string
	}{
		{
			name:   "unknown profile",
			target: "ghost",
			seed:   func(t *testing.T) {},
			authMe: func(t *testing.T) {
				stubAuthMe(t, func(string, map[string]string) (*authMeResponse, error) {
					t.Error("must not call the server for a profile that doesn't exist")
					return nil, errors.New("unreachable")
				})
			},
			wantCode: exitcode.Usage,
			wantMsg:  `no profile named "ghost"`,
		},
		{
			name:   "profile has no token",
			target: "empty",
			seed: func(t *testing.T) {
				if err := credentials.Put("empty", credentials.Profile{URL: "https://e.test"}); err != nil {
					t.Fatal(err)
				}
			},
			authMe: func(t *testing.T) {
				stubAuthMe(t, func(string, map[string]string) (*authMeResponse, error) {
					t.Error("must not call the server without a token")
					return nil, errors.New("unreachable")
				})
			},
			wantCode: exitcode.Auth,
			wantMsg:  "has no stored token",
		},
		{
			name:   "token rejected by server",
			target: "acme",
			seed:   func(t *testing.T) { seedProfile(t, "acme", "https://acme.test", "dead") },
			authMe: func(t *testing.T) {
				stubAuthMe(t, func(string, map[string]string) (*authMeResponse, error) {
					return nil, fmt.Errorf("%w (HTTP 401)", errTokenRejected)
				})
			},
			wantCode: exitcode.Auth,
			wantMsg:  "no longer valid",
		},
		{
			name:   "deployment unreachable",
			target: "acme",
			seed:   func(t *testing.T) { seedProfile(t, "acme", "https://acme.test", "ta") },
			authMe: func(t *testing.T) {
				stubAuthMe(t, func(string, map[string]string) (*authMeResponse, error) {
					return nil, errors.New("dial tcp: connection refused")
				})
			},
			wantCode: exitcode.Network,
			wantMsg:  "couldn't reach",
		},
	}

	for _, tc := range tests {
		t.Run(tc.name, func(t *testing.T) {
			t.Setenv("HOME", t.TempDir())
			resetProfilesUseFlags(t)

			seedProfile(t, "default", "https://d.test", "td")
			tc.seed(t)
			tc.authMe(t)
			call := stubPostAuthCapture(t)
			code := stubOsExit(t)

			out, err := runProfilesUse(t, tc.target)
			if err != nil {
				t.Fatalf("RunE should report via exit code, not error: %v", err)
			}
			if *code != tc.wantCode {
				t.Errorf("exit code = %d, want %d", *code, tc.wantCode)
			}
			if got := decodeMap(t, out); !strings.Contains(fmt.Sprint(got["error"]), tc.wantMsg) {
				t.Errorf("error = %v, want it to mention %q", got["error"], tc.wantMsg)
			}
			// The two things that must not move.
			if got := onDiskActiveURL(t); got != "https://d.test" {
				t.Errorf("active control plane moved to %q on a refused switch", got)
			}
			if call.count != 0 {
				t.Errorf("postAuthSetup ran %d times; a refused switch must not touch installed skills", call.count)
			}
		})
	}
}

func TestProfilesUse_Local_OutsideHome_Refuses(t *testing.T) {
	t.Setenv("HOME", t.TempDir())
	resetProfilesUseFlags(t)

	seedProfile(t, "default", "https://d.test", "td")
	seedProfile(t, "acme", "https://acme.test", "ta")
	// Project-root discovery is bounded to the home subtree.
	outside := t.TempDir()
	t.Cleanup(paths.SetGetwdForTest(func() (string, error) { return outside, nil }))
	okAuthMe(t, "u@x")
	call := stubPostAuthCapture(t)
	code := stubOsExit(t)

	profilesUseLocal = true
	out, err := runProfilesUse(t, "acme")
	if err != nil {
		t.Fatalf("RunE err = %v", err)
	}
	if *code != exitcode.Usage {
		t.Errorf("exit code = %d, want %d", *code, exitcode.Usage)
	}
	if call.count != 0 {
		t.Error("no skills should be synced when the pin fails")
	}
	if got := onDiskActiveURL(t); got != "https://d.test" {
		t.Errorf("active control plane = %q; a failed --local must not switch it", got)
	}
	if got := decodeMap(t, out); !strings.Contains(fmt.Sprint(got["hint"]), "home directory") {
		t.Errorf("hint = %v, want the home-directory guidance", got["hint"])
	}
}

// ─── contracts ───────────────────────────────────────────────────────────

// The JSON envelope must stay a superset of login's, so an AI host can read
// either command's output with one parser.
func TestProfilesUse_JSONEnvelopeMatchesLogin(t *testing.T) {
	t.Setenv("HOME", t.TempDir())
	resetProfilesUseFlags(t)

	if err := credentials.Put("acme", credentials.Profile{
		URL: "https://acme.test", Username: "u@x", Token: "ta",
	}); err != nil {
		t.Fatal(err)
	}
	okAuthMe(t, "u@x")
	stubPostAuthCapture(t)

	out, err := runProfilesUse(t, "acme")
	if err != nil {
		t.Fatalf("RunE err = %v", err)
	}
	got := decodeMap(t, out)
	for key := range setupPayload("acme", "u@x", "https://acme.test", "", false, postAuthState{}) {
		if _, ok := got[key]; !ok {
			t.Errorf("key %q from login's envelope missing from `profiles use` output", key)
		}
	}
	if _, ok := got["previous_profile"]; !ok {
		t.Error("previous_profile missing")
	}
}

func TestProfilesUse_ArgValidation(t *testing.T) {
	if err := profilesUseCmd.Args(profilesUseCmd, nil); err == nil {
		t.Error("zero args should be rejected")
	}
	if err := profilesUseCmd.Args(profilesUseCmd, []string{"a", "b"}); err == nil {
		t.Error("two args should be rejected")
	}
	if err := profilesUseCmd.Args(profilesUseCmd, []string{"a"}); err != nil {
		t.Errorf("one arg should be accepted, got %v", err)
	}
	// Adding a subcommand must not make `praxis profiles <junk>` silently
	// list profiles — the parent still rejects stray args.
	if err := profilesCmd.Args(profilesCmd, []string{"junk"}); err == nil {
		t.Error("parent `profiles` should reject an unknown subcommand")
	}
	// Order-independent: cobra sorts subcommands and `profiles` also carries
	// rename/rm, so assert resolution — what a user actually types — rather
	// than a position in the list.
	sub, _, err := profilesCmd.Find([]string{"use"})
	if err != nil || sub.Name() != "use" {
		names := make([]string, 0, len(profilesCmd.Commands()))
		for _, c := range profilesCmd.Commands() {
			names = append(names, c.Name())
		}
		t.Errorf("use should resolve under profiles, got %v (err %v)", names, err)
	}
}

func TestProfilesUse_CompletesProfileNames(t *testing.T) {
	t.Setenv("HOME", t.TempDir())
	seedProfile(t, "acme", "https://acme.test", "ta")
	seedProfile(t, "bigcorp", "https://bigcorp.test", "tb")

	names, directive := profilesUseCmd.ValidArgsFunction(profilesUseCmd, nil, "")
	if strings.Join(names, ",") != "acme,bigcorp" {
		t.Errorf("completions = %v, want the stored profiles, sorted", names)
	}
	if directive != cobra.ShellCompDirectiveNoFileComp {
		t.Errorf("directive = %v, want NoFileComp (a profile is not a path)", directive)
	}

	// The command takes exactly one arg; don't offer a second.
	if names, _ := profilesUseCmd.ValidArgsFunction(profilesUseCmd, []string{"acme"}, ""); names != nil {
		t.Errorf("second-arg completions = %v, want none", names)
	}
}

func TestRenderProfileSwitchText(t *testing.T) {
	tests := []struct {
		name     string
		in       switchSummary
		contains []string
		absent   []string
	}{
		{
			name: "global switch",
			in:   switchSummary{Profile: "acme", Previous: "default", URL: "https://acme.test", MultiProfile: true},
			contains: []string{`Switched to profile "acme"`, `from "default"`, "https://acme.test",
				"machine-global", "agent session", "export " + credentials.EnvProfile},
			absent: []string{"Note:"},
		},
		{
			// The single-profile customer re-pointing at their only profile has no
			// other session to disturb: everything resolved to this profile before
			// and after. Warning them anyway is noise that teaches PRAXIS_PROFILE
			// for a problem they don't have.
			name:     "global switch on a single-profile machine",
			in:       switchSummary{Profile: "default", Previous: "default", URL: "https://d.test"},
			contains: []string{`Profile "default" is active`},
			absent:   []string{"machine-global", "agent session", credentials.EnvProfile},
		},
		{
			name:     "re-sync of the active profile",
			in:       switchSummary{Profile: "acme", Previous: "acme", URL: "https://acme.test"},
			contains: []string{`Profile "acme" is active`, "re-synced"},
			absent:   []string{"Switched"},
		},
		{
			name:     "local pin",
			in:       switchSummary{Profile: "acme", Previous: "default", URL: "https://acme.test", ProjectRoot: "/h/repo/.praxis", MultiProfile: true},
			contains: []string{`Pinned profile "acme"`, "/h/repo/.praxis"},
			// A pin is scoped to this tree by construction, so the machine-global
			// warning must stay out even on a multi-profile machine.
			absent: []string{"Switched", "Note:", "machine-global"},
		},
		{
			name: "shadowed global switch",
			in: switchSummary{
				Profile: "vymo", Previous: "acme", URL: "https://vymo.test",
				ShadowedRoot: "/h/repo/.praxis", MultiProfile: true,
			},
			contains: []string{`Switched to profile "vymo"`, "Note:", "/h/repo/.facets/credentials", `still use "acme"`, "--local"},
		},
	}
	for _, tc := range tests {
		t.Run(tc.name, func(t *testing.T) {
			var buf bytes.Buffer
			renderProfileSwitchText(&buf, tc.in)
			got := buf.String()
			for _, want := range tc.contains {
				if !strings.Contains(got, want) {
					t.Errorf("output missing %q:\n%s", want, got)
				}
			}
			for _, no := range tc.absent {
				if strings.Contains(got, no) {
					t.Errorf("output should not contain %q:\n%s", no, got)
				}
			}
		})
	}
}

// activateProfile is the shared seam; assert the pairing it guarantees —
// pointer flipped AND root pinned to the matching scope — and that the
// returned restore func unwinds the pin.
func TestActivateProfile_PairsPointerAndRoot(t *testing.T) {
	home := t.TempDir()
	t.Setenv("HOME", home)
	clearFacetsEnv(t)
	seedPAT(t, "acme", "https://acme.test", "ta")
	repo := repoUnderHome(t, home)

	t.Run("global pins home", func(t *testing.T) {
		root, restore, err := activateProfile("acme", false)
		if err != nil {
			t.Fatal(err)
		}
		if root != "" {
			t.Errorf("global activate returned project root %q, want empty", root)
		}
		if got, _ := paths.ActiveRoot(); got != filepath.Join(home, ".praxis") {
			t.Errorf("ActiveRoot = %q, want home root", got)
		}
		restore()
		if paths.RootIsPinned() {
			t.Error("restore() must unpin ActiveRoot")
		}
		if got := onDiskActiveURL(t); got != "https://acme.test" {
			t.Errorf("active control plane = %q, want acme's", got)
		}
	})

	t.Run("local pins project", func(t *testing.T) {
		root, restore, err := activateProfile("acme", true)
		if err != nil {
			t.Fatal(err)
		}
		defer restore()
		if want := filepath.Join(repo, ".praxis"); root != want {
			t.Errorf("project root = %q, want %q", root, want)
		}
		if got, _ := paths.ActiveRoot(); got != root {
			t.Errorf("ActiveRoot = %q, want the project root %q", got, root)
		}
	})
}
