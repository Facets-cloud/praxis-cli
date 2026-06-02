package cmd

import (
	"bytes"
	"encoding/json"
	"errors"
	"strings"
	"testing"

	"github.com/Facets-cloud/praxis-cli/internal/credentials"
)

var errTokenRevoked = errors.New("token revoked")

func resetProfilesFlags() {
	profilesJSON = false
	profilesRefresh = false
}

// decodeProfiles unmarshals the command's JSON output into the typed
// shape so assertions are structural, not substring-fragile.
func decodeProfiles(t *testing.T, b []byte) profilesOutput {
	t.Helper()
	var out profilesOutput
	if err := json.Unmarshal(b, &out); err != nil {
		t.Fatalf("profiles output should be valid JSON: %v\noutput:\n%s", err, b)
	}
	return out
}

func findProfile(out profilesOutput, name string) (profileEntry, bool) {
	for _, p := range out.Profiles {
		if p.Name == name {
			return p, true
		}
	}
	return profileEntry{}, false
}

func TestProfilesCmd_ListsAllProfiles(t *testing.T) {
	t.Setenv("HOME", t.TempDir())
	t.Setenv("PRAXIS_PROFILE", "")
	resetProfilesFlags()

	_ = credentials.Put("default", credentials.Profile{
		URL: "https://default.test", Username: "anshul@facets.cloud", Token: "td",
	})
	_ = credentials.Put("dev", credentials.Profile{
		URL: "https://dev.test", Username: "dev@facets.cloud", Token: "tdev",
	})
	// A profile with no token — configured but not logged in.
	_ = credentials.Put("root", credentials.Profile{URL: "https://root.test"})

	var buf bytes.Buffer
	profilesCmd.SetOut(&buf)
	if err := profilesCmd.RunE(profilesCmd, nil); err != nil {
		t.Fatalf("RunE err = %v", err)
	}
	out := decodeProfiles(t, buf.Bytes())

	if len(out.Profiles) != 3 {
		t.Fatalf("want 3 profiles, got %d: %+v", len(out.Profiles), out.Profiles)
	}
	d, ok := findProfile(out, "default")
	if !ok {
		t.Fatal("default profile missing from output")
	}
	if d.URL != "https://default.test" || d.Username != "anshul@facets.cloud" || !d.LoggedIn {
		t.Errorf("default entry wrong: %+v", d)
	}
	r, _ := findProfile(out, "root")
	if r.LoggedIn {
		t.Errorf("root has no token; logged_in should be false, got %+v", r)
	}
}

func TestProfilesCmd_ActiveMarker(t *testing.T) {
	t.Setenv("HOME", t.TempDir())
	t.Setenv("PRAXIS_PROFILE", "")
	resetProfilesFlags()

	_ = credentials.Put("default", credentials.Profile{URL: "https://default.test", Token: "td"})
	_ = credentials.Put("dev", credentials.Profile{URL: "https://dev.test", Token: "tdev"})
	if err := credentials.SetActive("dev"); err != nil {
		t.Fatal(err)
	}

	var buf bytes.Buffer
	profilesCmd.SetOut(&buf)
	if err := profilesCmd.RunE(profilesCmd, nil); err != nil {
		t.Fatalf("RunE err = %v", err)
	}
	out := decodeProfiles(t, buf.Bytes())

	if out.ActiveProfile != "dev" {
		t.Errorf("active_profile = %q, want dev", out.ActiveProfile)
	}
	dev, _ := findProfile(out, "dev")
	def, _ := findProfile(out, "default")
	if !dev.Active {
		t.Error("dev should be marked active")
	}
	if def.Active {
		t.Error("default should not be marked active")
	}
}

func TestProfilesCmd_SingleProfile(t *testing.T) {
	t.Setenv("HOME", t.TempDir())
	t.Setenv("PRAXIS_PROFILE", "")
	resetProfilesFlags()

	_ = credentials.Put("default", credentials.Profile{
		URL: "https://x.test", Username: "anshul@facets.cloud", Token: "t",
	})

	var buf bytes.Buffer
	profilesCmd.SetOut(&buf)
	if err := profilesCmd.RunE(profilesCmd, nil); err != nil {
		t.Fatalf("RunE err = %v", err)
	}
	out := decodeProfiles(t, buf.Bytes())
	if len(out.Profiles) != 1 {
		t.Fatalf("want 1 profile, got %d", len(out.Profiles))
	}
	// The lone default profile resolves as active by the default fallback.
	if !out.Profiles[0].Active {
		t.Errorf("single default profile should be active: %+v", out.Profiles[0])
	}
}

func TestProfilesCmd_NoConfig(t *testing.T) {
	t.Setenv("HOME", t.TempDir())
	t.Setenv("PRAXIS_PROFILE", "")
	resetProfilesFlags()

	var buf bytes.Buffer
	profilesCmd.SetOut(&buf)
	// No credentials file at all — must not error, just an empty list.
	if err := profilesCmd.RunE(profilesCmd, nil); err != nil {
		t.Fatalf("no-config should not error, got %v", err)
	}
	out := decodeProfiles(t, buf.Bytes())
	if len(out.Profiles) != 0 {
		t.Errorf("want 0 profiles for empty store, got %d", len(out.Profiles))
	}
}

func TestProfilesCmd_DoesNotCallNetworkByDefault(t *testing.T) {
	t.Setenv("HOME", t.TempDir())
	t.Setenv("PRAXIS_PROFILE", "")
	resetProfilesFlags()

	called := false
	orig := fetchAuthMe
	fetchAuthMe = func(string, string) (*authMeResponse, error) {
		called = true
		return nil, nil
	}
	defer func() { fetchAuthMe = orig }()

	_ = credentials.Put("default", credentials.Profile{URL: "https://x", Token: "t"})

	profilesCmd.SetOut(&bytes.Buffer{})
	if err := profilesCmd.RunE(profilesCmd, nil); err != nil {
		t.Fatalf("RunE err = %v", err)
	}
	if called {
		t.Error("profiles must not call fetchAuthMe without --refresh (local-only snapshot)")
	}
}

func TestProfilesCmd_Refresh_VerifiesEachLoggedInProfile(t *testing.T) {
	t.Setenv("HOME", t.TempDir())
	t.Setenv("PRAXIS_PROFILE", "")
	resetProfilesFlags()
	profilesRefresh = true

	var seen []string
	orig := fetchAuthMe
	fetchAuthMe = func(baseURL, token string) (*authMeResponse, error) {
		seen = append(seen, token)
		return &authMeResponse{Email: "verified@facets.cloud", UserID: "u1"}, nil
	}
	defer func() { fetchAuthMe = orig }()

	_ = credentials.Put("default", credentials.Profile{URL: "https://d.test", Token: "td"})
	_ = credentials.Put("dev", credentials.Profile{URL: "https://dev.test", Token: "tdev"})
	// No token — must be skipped by --refresh (nothing to verify).
	_ = credentials.Put("root", credentials.Profile{URL: "https://root.test"})

	var buf bytes.Buffer
	profilesCmd.SetOut(&buf)
	if err := profilesCmd.RunE(profilesCmd, nil); err != nil {
		t.Fatalf("RunE err = %v", err)
	}
	if len(seen) != 2 {
		t.Errorf("want fetchAuthMe called for 2 logged-in profiles, got %d (%v)", len(seen), seen)
	}
	out := decodeProfiles(t, buf.Bytes())
	d, _ := findProfile(out, "default")
	if d.AuthCheck == nil || !d.AuthCheck.OK || d.AuthCheck.Username != "verified@facets.cloud" {
		t.Errorf("default auth_check wrong: %+v", d.AuthCheck)
	}
	r, _ := findProfile(out, "root")
	if r.AuthCheck != nil {
		t.Errorf("root has no token; auth_check should be omitted, got %+v", r.AuthCheck)
	}
}

func TestRenderProfilesText_Table(t *testing.T) {
	result := profilesOutput{
		ActiveProfile: "dev",
		Profiles: []profileEntry{
			{Name: "default", URL: "https://d.test", Username: "anshul@facets.cloud", LoggedIn: true},
			{Name: "dev", URL: "https://dev.test", Username: "dev@facets.cloud", Active: true, LoggedIn: true,
				AuthCheck: &authCheckResult{OK: true, Username: "dev@facets.cloud"}},
			{Name: "stale", URL: "https://s.test", Username: "x@facets.cloud", LoggedIn: true,
				AuthCheck: &authCheckResult{OK: false, Error: "HTTP 401"}},
			// No token, no username — exercises the dash fallbacks.
			{Name: "empty", LoggedIn: false},
		},
	}
	var buf bytes.Buffer
	if err := renderProfilesText(&buf, result); err != nil {
		t.Fatalf("renderProfilesText err = %v", err)
	}
	out := buf.String()
	for _, want := range []string{
		"ACTIVE", "PROFILE", "URL", "USERNAME", "LOGIN", // header
		"*",                  // active marker on dev
		"yes (verified)",     // dev passed --refresh check
		"no (token invalid)", // stale failed --refresh check
		"yes",                // default: token present, no refresh
		"no",                 // empty: no token
	} {
		if !strings.Contains(out, want) {
			t.Errorf("table missing %q\nfull:\n%s", want, out)
		}
	}
	// The active marker must sit on dev's row, not default's.
	for _, line := range strings.Split(out, "\n") {
		if strings.Contains(line, "default") && strings.HasPrefix(strings.TrimSpace(line), "*") {
			t.Errorf("active marker on wrong row: %q", line)
		}
	}
	// Empty URL/username render as a dash, never blank columns.
	if !strings.Contains(out, "-") {
		t.Errorf("empty fields should render as '-'\nfull:\n%s", out)
	}
}

func TestRenderProfilesText_Empty(t *testing.T) {
	var buf bytes.Buffer
	if err := renderProfilesText(&buf, profilesOutput{ActiveProfile: "default"}); err != nil {
		t.Fatalf("renderProfilesText err = %v", err)
	}
	out := buf.String()
	if !strings.Contains(out, "No profiles configured") {
		t.Errorf("empty listing should explain there are no profiles, got %q", out)
	}
	if !strings.Contains(out, "praxis login") {
		t.Errorf("empty listing should hint at `praxis login`, got %q", out)
	}
}

func TestProfilesCmd_Refresh_RecordsTokenFailure(t *testing.T) {
	t.Setenv("HOME", t.TempDir())
	t.Setenv("PRAXIS_PROFILE", "")
	resetProfilesFlags()
	profilesRefresh = true

	orig := fetchAuthMe
	fetchAuthMe = func(string, string) (*authMeResponse, error) {
		return nil, errTokenRevoked
	}
	defer func() { fetchAuthMe = orig }()

	_ = credentials.Put("default", credentials.Profile{URL: "https://d.test", Token: "td"})

	var buf bytes.Buffer
	profilesCmd.SetOut(&buf)
	// A revoked token on one profile must NOT abort the whole listing.
	if err := profilesCmd.RunE(profilesCmd, nil); err != nil {
		t.Fatalf("refresh failure should be reported per-profile, not returned: %v", err)
	}
	out := decodeProfiles(t, buf.Bytes())
	d, _ := findProfile(out, "default")
	if d.AuthCheck == nil || d.AuthCheck.OK {
		t.Fatalf("default auth_check should be present and not ok: %+v", d.AuthCheck)
	}
	if !strings.Contains(d.AuthCheck.Error, "revoked") {
		t.Errorf("auth_check error should surface the failure, got %q", d.AuthCheck.Error)
	}
}
