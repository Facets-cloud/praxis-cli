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

func resetSetDefaultFlags(t *testing.T) {
	t.Helper()
	// rootProfile too: set-default REFUSES a diverging --profile, so a value
	// leaked from another test would push every case down the exit-2 path.
	rootProfile = ""
	profilesSetDefaultJSON = false
	t.Cleanup(func() {
		rootProfile = ""
		profilesSetDefaultJSON = false
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

func runSetDefault(t *testing.T, name string) (string, error) {
	t.Helper()
	var buf bytes.Buffer
	profilesSetDefaultCmd.SetOut(&buf)
	err := profilesSetDefaultCmd.RunE(profilesSetDefaultCmd, []string{name})
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

func TestProfilesSetDefault_MovesDefaultAndSyncs(t *testing.T) {
	home := t.TempDir()
	t.Setenv("HOME", home)
	resetSetDefaultFlags(t)
	clearFacetsEnv(t)

	seedPAT(t, "default", "https://d.test", "td")
	seedPAT(t, "acme", "https://acme.test", "ta")
	okAuthMe(t, "u@x")
	call := stubPostAuthCapture(t)

	out, err := runSetDefault(t, "acme")
	if err != nil {
		t.Fatalf("RunE err = %v", err)
	}
	if got := onDiskActiveURL(t); got != "https://acme.test" {
		t.Errorf("default control plane = %q, want acme's", got)
	}
	// The unduplicated old [default] is kept under its host label.
	if store, _ := credentials.Load(); store["d"].Token != "td" {
		t.Errorf("old default not kept: %+v", store["d"])
	}
	if call.count != 1 || call.baseURL != "https://acme.test" || call.auth["Authorization"] != "Bearer ta" {
		t.Errorf("sync = %+v, want one run against acme with its token", call)
	}
	if want := filepath.Join(home, ".praxis"); call.activeRoot != want {
		t.Errorf("activeRoot during sync = %q, want home root %q", call.activeRoot, want)
	}
	got := decodeMap(t, out)
	if got["profile"] != "acme" || got["previous_default"] != "default" || got["scope"] != "global" {
		t.Errorf("profile/previous_default/scope = %v/%v/%v", got["profile"], got["previous_default"], got["scope"])
	}
	if !strings.Contains(fmt.Sprint(got["scope_note"]), "machine-global") {
		t.Errorf("scope_note = %v, want the machine-global warning", got["scope_note"])
	}
	if _, ok := got["effective_profile"]; ok {
		t.Error("nothing outranks [default] here; effective_profile must be absent")
	}
}

// Making the current default the default again is a re-sync, not an error.
func TestProfilesSetDefault_SameProfile_ReSyncs(t *testing.T) {
	t.Setenv("HOME", t.TempDir())
	resetSetDefaultFlags(t)
	clearFacetsEnv(t)

	seedPAT(t, "acme", "https://acme.test", "ta")
	if _, err := credentials.SetDefault("acme"); err != nil {
		t.Fatal(err)
	}
	okAuthMe(t, "u@x")
	call := stubPostAuthCapture(t)

	out, err := runSetDefault(t, "acme")
	if err != nil {
		t.Fatalf("RunE err = %v", err)
	}
	if call.count != 1 {
		t.Errorf("postAuthSetup called %d times, want 1 (re-sync)", call.count)
	}
	if got := decodeMap(t, out); got["previous_default"] != "acme" {
		t.Errorf("previous_default = %v, want acme", got["previous_default"])
	}
}

// In a pinned folder the machine default moves and installs user-level, but
// commands in the folder keep the pin, and the output says so.
func TestProfilesSetDefault_InPinnedFolder_StaysGlobalAndReportsThePin(t *testing.T) {
	home := t.TempDir()
	t.Setenv("HOME", home)
	resetSetDefaultFlags(t)
	clearFacetsEnv(t)

	seedPAT(t, "default", "https://d.test", "td")
	seedPAT(t, "acme", "https://acme.test", "ta")
	seedPAT(t, "vymo", "https://vymo.test", "tv")
	repo := repoUnderHome(t, home)
	if err := credentials.WritePin(repo, "acme"); err != nil {
		t.Fatal(err)
	}
	inDir(t, repo)
	okAuthMe(t, "u@x")
	call := stubPostAuthCapture(t)

	out, err := runSetDefault(t, "vymo")
	if err != nil {
		t.Fatalf("RunE err = %v", err)
	}
	if got := homeFacetsURL(t); got != "https://vymo.test" {
		t.Errorf("home default = %q, want vymo", got)
	}
	if a, _ := credentials.ResolveActive(""); a.Name != "acme" || a.Source != credentials.SourcePin {
		t.Errorf("in-folder resolution = %+v, want the pinned acme", a)
	}
	if want := filepath.Join(home, ".praxis"); call.activeRoot != want {
		t.Errorf("activeRoot during sync = %q, want home root %q — set-default must not install into a pinned folder", call.activeRoot, want)
	}
	if got := decodeMap(t, out); got["effective_profile"] != "acme (pin)" {
		t.Errorf("effective_profile = %v, want acme (pin)", got["effective_profile"])
	}
}

// A stale stored host self-heals to the post-redirect one (issue #19-A).
func TestProfilesSetDefault_HealsCanonicalURL(t *testing.T) {
	t.Setenv("HOME", t.TempDir())
	resetSetDefaultFlags(t)
	clearFacetsEnv(t)

	seedProfile(t, "acme", "https://apex.test", "ta")
	stubAuthMe(t, func(baseURL string, _ map[string]string) (*authMeResponse, error) {
		return &authMeResponse{Email: "u@x", canonicalBaseURL: "https://www.apex.test"}, nil
	})
	call := stubPostAuthCapture(t)

	out, err := runSetDefault(t, "acme")
	if err != nil {
		t.Fatalf("RunE err = %v", err)
	}
	store, err := credentials.Load()
	if err != nil {
		t.Fatal(err)
	}
	if store["acme"].URL != "https://www.apex.test" || store["acme"].Token != "ta" {
		t.Errorf("stored acme = %+v, want the canonical host and the same token", store["acme"])
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
// default and the installed skill set exactly as they were.
func TestProfilesSetDefault_Refusals_LeaveStateUntouched(t *testing.T) {
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
			resetSetDefaultFlags(t)
			clearFacetsEnv(t)

			seedProfile(t, "default", "https://d.test", "td")
			tc.seed(t)
			tc.authMe(t)
			call := stubPostAuthCapture(t)
			code := stubOsExit(t)

			out, err := runSetDefault(t, tc.target)
			if err != nil {
				t.Fatalf("RunE should report via exit code, not error: %v", err)
			}
			if *code != tc.wantCode {
				t.Errorf("exit code = %d, want %d", *code, tc.wantCode)
			}
			if got := decodeMap(t, out); !strings.Contains(fmt.Sprint(got["error"]), tc.wantMsg) {
				t.Errorf("error = %v, want it to mention %q", got["error"], tc.wantMsg)
			}
			if got := onDiskActiveURL(t); got != "https://d.test" {
				t.Errorf("default control plane moved to %q on a refused switch", got)
			}
			if call.count != 0 {
				t.Errorf("postAuthSetup ran %d times; a refused switch must not touch installed skills", call.count)
			}
		})
	}
}

// ─── contracts ───────────────────────────────────────────────────────────

// The JSON envelope must stay a superset of login's, so an AI host can read
// either command's output with one parser.
func TestProfilesSetDefault_JSONEnvelopeMatchesLogin(t *testing.T) {
	t.Setenv("HOME", t.TempDir())
	resetSetDefaultFlags(t)
	clearFacetsEnv(t)

	seedPAT(t, "acme", "https://acme.test", "ta")
	okAuthMe(t, "u@x")
	stubPostAuthCapture(t)

	out, err := runSetDefault(t, "acme")
	if err != nil {
		t.Fatalf("RunE err = %v", err)
	}
	got := decodeMap(t, out)
	for key := range setupPayload("acme", "u@x", "https://acme.test", "", false, postAuthState{}) {
		if _, ok := got[key]; !ok {
			t.Errorf("key %q from login's envelope missing from set-default output", key)
		}
	}
}

func TestProfilesSetDefault_ArgsAndCompletion(t *testing.T) {
	t.Setenv("HOME", t.TempDir())
	if err := profilesSetDefaultCmd.Args(profilesSetDefaultCmd, nil); err == nil {
		t.Error("zero args should be rejected")
	}
	if err := profilesSetDefaultCmd.Args(profilesSetDefaultCmd, []string{"a"}); err != nil {
		t.Errorf("one arg should be accepted, got %v", err)
	}
	if err := profilesCmd.Args(profilesCmd, []string{"junk"}); err == nil {
		t.Error("parent `profiles` should reject an unknown subcommand")
	}
	seedProfile(t, "acme", "https://acme.test", "ta")
	seedProfile(t, "bigcorp", "https://bigcorp.test", "tb")
	names, directive := profilesSetDefaultCmd.ValidArgsFunction(profilesSetDefaultCmd, nil, "")
	if strings.Join(names, ",") != "acme,bigcorp" || directive != cobra.ShellCompDirectiveNoFileComp {
		t.Errorf("completions = %v (%v), want the stored profiles, no file completion", names, directive)
	}
	if names, _ := profilesSetDefaultCmd.ValidArgsFunction(profilesSetDefaultCmd, []string{"acme"}, ""); names != nil {
		t.Errorf("second-arg completions = %v, want none", names)
	}
}

// `profiles use` and `switch` are gone: they answer with what replaced them,
// exit 2, and change nothing.
func TestProfilesUse_Removed_PointsAtTheReplacements(t *testing.T) {
	t.Setenv("HOME", t.TempDir())
	clearFacetsEnv(t)
	seedPAT(t, "default", "https://d.test", "td")
	seedPAT(t, "acme", "https://acme.test", "ta")
	call := stubPostAuthCapture(t)
	code := stubOsExit(t)

	for _, verb := range []string{"use", "switch"} {
		sub, _, err := profilesCmd.Find([]string{verb, "acme"})
		if err != nil || sub != profilesUseRemovedCmd || !sub.Hidden {
			t.Fatalf("%s resolves to %v (%v), want the hidden removal notice", verb, sub, err)
		}
		var buf bytes.Buffer
		sub.SetOut(&buf)
		if err := sub.RunE(sub, []string{"acme", "--local"}); err != nil {
			t.Fatal(err)
		}
		if *code != exitcode.Usage || !strings.Contains(buf.String(), "profiles set-default") || !strings.Contains(buf.String(), "profiles pin") {
			t.Errorf("%s: code %d, out %q; want exit 2 and the replacements", verb, *code, buf.String())
		}
	}
	if got := onDiskActiveURL(t); got != "https://d.test" || call.count != 0 {
		t.Errorf("default = %q, syncs = %d; the removed command must change nothing", got, call.count)
	}
}

func TestRenderSetDefaultText(t *testing.T) {
	tests := []struct{ previous, effective, want string }{
		{"acme", "", "is the machine default"},
		{"", "", "is now the machine default"},
		{"root", "", `changed from "root" to "acme"`},
		{"root", "zed (pin)", "Here, commands still use zed (pin)."},
	}
	for _, tc := range tests {
		var buf bytes.Buffer
		renderSetDefaultText(&buf, "acme", tc.previous, "https://acme.test", tc.effective, true)
		if !strings.Contains(buf.String(), tc.want) {
			t.Errorf("previous=%q effective=%q: %q lacks %q", tc.previous, tc.effective, buf.String(), tc.want)
		}
	}
}
