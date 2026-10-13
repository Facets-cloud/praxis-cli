package raptorstate

import (
	"errors"
	"os"
	"path/filepath"
	"runtime"
	"testing"

	"github.com/Facets-cloud/praxis-cli/internal/credentials"
)

// TestMain starts the facets-file walk at the faked HOME so no test reads the
// developer's live ~/.facets/credentials.
func TestMain(m *testing.M) {
	restore := credentials.SetGetwdForTest(func() (string, error) { return os.Getenv("HOME"), nil })
	code := m.Run()
	restore()
	os.Exit(code)
}

// clearRaptorEnv unsets every env var the resolver consults so tests are
// hermetic regardless of the developer's shell.
func clearRaptorEnv(t *testing.T) {
	t.Helper()
	for _, k := range []string{"CONTROL_PLANE_URL", "FACETS_USERNAME", "FACETS_TOKEN", "FACETS_PROFILE", "PRAXIS_PROFILE"} {
		t.Setenv(k, "")
		os.Unsetenv(k)
	}
}

// stubInstalled makes lookPath report raptor as installed (or not).
func stubInstalled(t *testing.T, installed bool) {
	t.Helper()
	orig := lookPath
	t.Cleanup(func() { lookPath = orig })
	lookPath = func(string) (string, error) {
		if installed {
			return "/usr/local/bin/raptor", nil
		}
		return "", errors.New("not found")
	}
}

// seedHomeFacets writes a raptor-style ~/.facets/credentials into the faked HOME.
func seedHomeFacets(t *testing.T, body string) {
	t.Helper()
	dir := filepath.Join(os.Getenv("HOME"), ".facets")
	if err := os.MkdirAll(dir, 0o700); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(filepath.Join(dir, "credentials"), []byte(body), 0o600); err != nil {
		t.Fatal(err)
	}
}

func pat(url, user string) credentials.Profile {
	return credentials.FacetsProfile(url, user, "pat")
}

var twoProfiles = map[string]credentials.Profile{
	"default": pat("https://root.console.facets.cloud", "anuj@facets.cloud"),
	"acme":    pat("https://acme.console.facets.cloud", "anuj@facets.cloud"),
}

func TestResolve_Chain(t *testing.T) {
	tests := []struct {
		name     string
		profiles map[string]credentials.Profile
		env      map[string]string
		want     State
	}{
		{
			name:     "env override wins over everything",
			profiles: twoProfiles,
			env:      map[string]string{"CONTROL_PLANE_URL": "https://env.example.com", "FACETS_USERNAME": "env@x", "FACETS_PROFILE": "acme"},
			want:     State{Found: true, Profile: "env", Source: SourceEnv, ControlPlaneURL: "https://env.example.com", Username: "env@x"},
		},
		{
			name:     "FACETS_PROFILE selects named profile",
			profiles: twoProfiles,
			env:      map[string]string{"FACETS_PROFILE": "acme"},
			want:     State{Found: true, Profile: "acme", Source: SourceEnvProfile, ControlPlaneURL: "https://acme.console.facets.cloud", Username: "anuj@facets.cloud"},
		},
		{
			name:     "FACETS_PROFILE naming a missing profile is reported unfound",
			profiles: twoProfiles,
			env:      map[string]string{"FACETS_PROFILE": "ghost"},
			want:     State{Found: false, Profile: "ghost", Source: SourceEnvProfile},
		},
		{
			name:     "PRAXIS_PROFILE is read too, and wins over FACETS_PROFILE",
			profiles: twoProfiles,
			env:      map[string]string{"PRAXIS_PROFILE": "acme", "FACETS_PROFILE": "default"},
			want:     State{Found: true, Profile: "acme", Source: SourceEnvProfile, ControlPlaneURL: "https://acme.console.facets.cloud", Username: "anuj@facets.cloud"},
		},
		{
			name:     "default section when nothing else selects",
			profiles: twoProfiles,
			want:     State{Found: true, Profile: "default", Source: SourceDefault, ControlPlaneURL: "https://root.console.facets.cloud", Username: "anuj@facets.cloud"},
		},
		{
			name:     "sole profile used when no default exists",
			profiles: map[string]credentials.Profile{"onlyone": pat("https://solo.console.facets.cloud", "solo@x")},
			want:     State{Found: true, Profile: "onlyone", Source: SourceSole, ControlPlaneURL: "https://solo.console.facets.cloud", Username: "solo@x"},
		},
		{
			name:     "multiple profiles without default resolve nothing",
			profiles: map[string]credentials.Profile{"a": pat("https://a.example", "u"), "b": pat("https://b.example", "u")},
			want:     State{Found: false},
		},
		{
			name: "no store resolves nothing",
			want: State{Found: false},
		},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			clearRaptorEnv(t)
			for k, v := range tt.env {
				t.Setenv(k, v)
			}
			stubInstalled(t, true)
			got := resolve(tt.profiles)
			tt.want.Installed = true // stubbed above for every case
			if got != tt.want {
				t.Errorf("resolve() = %+v, want %+v", got, tt.want)
			}
		})
	}
}

func TestInstalled(t *testing.T) {
	stubInstalled(t, true)
	if !Installed() {
		t.Error("Installed() = false when raptor is on PATH; want true")
	}
	stubInstalled(t, false)
	if Installed() {
		t.Error("Installed() = true when raptor is absent; want false")
	}
}

func TestResolve_InstalledFlag(t *testing.T) {
	clearRaptorEnv(t)
	stubInstalled(t, false)
	if got := resolve(nil); got.Installed {
		t.Errorf("Installed = true with lookPath failing; want false")
	}
}

func TestResolve_ReadsTheSharedStore(t *testing.T) {
	// Resolve (the exported entry) reads the file raptor would: the home file
	// here, a local one inside a `raptor login --local` tree.
	clearRaptorEnv(t)
	stubInstalled(t, true)
	home := t.TempDir()
	t.Setenv("HOME", home)

	if got := Resolve(); got.Found {
		t.Errorf("Resolve() with no credentials file: Found = true, want false")
	}

	seedHomeFacets(t, "[default]\ncontrol_plane_url = https://root.test\nusername = u@x\ntoken = pat\n")
	if got := Resolve(); !got.Found || got.ControlPlaneURL != "https://root.test" || got.Source != SourceDefault {
		t.Errorf("Resolve() = %+v, want the home default", got)
	}

	repo := filepath.Join(home, "repo", "sub")
	if err := os.MkdirAll(repo, 0o755); err != nil {
		t.Fatal(err)
	}
	if err := credentials.PutLocal("acme", pat("https://acme.test", "a@x"), filepath.Join(home, "repo")); err != nil {
		t.Fatal(err)
	}
	t.Cleanup(credentials.SetGetwdForTest(func() (string, error) { return repo, nil }))
	if got := Resolve(); !got.Found || got.Profile != "acme" || got.Source != SourceSole {
		t.Errorf("Resolve() inside the repo = %+v, want the local sole profile", got)
	}
}

func TestPAT(t *testing.T) {
	body := `[default]
control_plane_url = https://root.console.facets.cloud
username = user@corp
token = pat_abc123

[acme]
control_plane_url = https://acme.console.facets.cloud
username = admin@acme

[empty]
control_plane_url = https://empty.test
token = pat_no_user
`
	tests := []struct {
		name, profile, wantUser, wantToken string
		wantOK                             bool
	}{
		{name: "default", profile: "default", wantUser: "user@corp", wantToken: "pat_abc123", wantOK: true},
		{name: "missing token", profile: "acme"},
		{name: "missing username", profile: "empty"},
		{name: "unknown profile", profile: "ghost"},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			t.Setenv("HOME", t.TempDir())
			seedHomeFacets(t, body)
			user, token, ok := PAT(tt.profile)
			if ok != tt.wantOK || user != tt.wantUser || token != tt.wantToken {
				t.Errorf("PAT(%q) = (%q, %q, %v), want (%q, %q, %v)",
					tt.profile, user, token, ok, tt.wantUser, tt.wantToken, tt.wantOK)
			}
		})
	}
}

func TestPAT_MissingFile(t *testing.T) {
	t.Setenv("HOME", t.TempDir())
	if _, _, ok := PAT("default"); ok {
		t.Error("PAT with no credentials file = ok, want false")
	}
}

func TestMatchesHost(t *testing.T) {
	tests := []struct {
		name             string
		praxisURL, cpURL string
		want             bool
	}{
		{"same host", "https://root.console.facets.cloud", "https://root.console.facets.cloud", true},
		{"case-insensitive", "https://Root.Console.Facets.Cloud", "https://root.console.facets.cloud", true},
		{"scheme and path ignored", "https://acme.console.facets.cloud/ai-api", "http://acme.console.facets.cloud", true},
		{"different hosts", "https://praxis.example.test", "https://root.console.facets.cloud", false},
		{"empty praxis URL", "", "https://root.console.facets.cloud", false},
		{"empty cp URL", "https://praxis.example.test", "", false},
		{"unparseable", "://bad", "https://root.console.facets.cloud", false},
		{"hostless (no scheme)", "root.console.facets.cloud", "https://root.console.facets.cloud", false},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			if got := MatchesHost(tt.praxisURL, tt.cpURL); got != tt.want {
				t.Errorf("MatchesHost(%q, %q) = %v, want %v", tt.praxisURL, tt.cpURL, got, tt.want)
			}
		})
	}
}

// A folder pin selects for raptor too: behind the environment, ahead of
// [default].
func TestResolve_FolderPin(t *testing.T) {
	home := t.TempDir()
	t.Setenv("HOME", home)
	clearRaptorEnv(t)
	stubInstalled(t, true)
	stubPinProbe(t, true)
	seedHomeFacets(t, "[default]\ncontrol_plane_url = https://root.test\nusername = u\ntoken = t\n\n[acme]\ncontrol_plane_url = https://acme.test\nusername = u\ntoken = t\n")
	repo := filepath.Join(home, "repo")
	if err := os.MkdirAll(repo, 0o755); err != nil {
		t.Fatal(err)
	}
	if err := credentials.WritePin(repo, "acme"); err != nil {
		t.Fatal(err)
	}
	t.Cleanup(credentials.SetGetwdForTest(func() (string, error) { return repo, nil }))
	if st := Resolve(); st.Profile != "acme" || st.Source != SourcePin || !st.Found {
		t.Errorf("Resolve in a pinned folder = %+v, want acme from the pin", st)
	}
	t.Setenv("FACETS_PROFILE", "default")
	if st := Resolve(); st.Profile != "default" || st.Source != SourceEnvProfile {
		t.Errorf("FACETS_PROFILE must beat the pin, got %+v", st)
	}
}

// stubPinProbe answers the pin probe without running raptor.
func stubPinProbe(t *testing.T, supported bool) {
	t.Helper()
	t.Cleanup(SetPinProbeForTest(func(string) bool { return supported }))
}

// fakeRaptor puts a raptor script on PATH that answers `raptor profiles`
// with exit 0 only when it supports pins.
func fakeRaptor(t *testing.T, supportsPins bool) string {
	t.Helper()
	if runtime.GOOS == "windows" {
		t.Skip("the fake raptor is a shell script")
	}
	dir := t.TempDir()
	body := "#!/bin/sh\nexit 1\n"
	if supportsPins {
		body = "#!/bin/sh\n[ \"$1\" = profiles ] && exit 0\nexit 1\n"
	}
	path := filepath.Join(dir, "raptor")
	if err := os.WriteFile(path, []byte(body), 0o700); err != nil {
		t.Fatal(err)
	}
	t.Setenv("PATH", dir)
	t.Cleanup(SetPinProbeForTest(ProbePins))
	return path
}

// An older raptor on PATH ignores pins: the state says what it really uses
// ([default]) and names the pin it ignores. A raptor with `profiles` follows it.
func TestResolve_PinFollowsTheRaptorOnPath(t *testing.T) {
	home := t.TempDir()
	t.Setenv("HOME", home)
	clearRaptorEnv(t)
	seedHomeFacets(t, "[default]\ncontrol_plane_url = https://root.test\nusername = u\ntoken = t\n\n[acme]\ncontrol_plane_url = https://acme.test\nusername = u\ntoken = t\n")
	repo := filepath.Join(home, "repo")
	if err := os.MkdirAll(repo, 0o755); err != nil {
		t.Fatal(err)
	}
	if err := credentials.WritePin(repo, "acme"); err != nil {
		t.Fatal(err)
	}
	t.Cleanup(credentials.SetGetwdForTest(func() (string, error) { return repo, nil }))

	old := fakeRaptor(t, false)
	if st := Resolve(); st.Profile != "default" || st.IgnoredPin != credentials.PinPathIn(repo) {
		t.Errorf("old raptor: %+v, want [default] and the ignored pin", st)
	}
	if path, supported, installed := PinSupport(); path != old || supported || !installed {
		t.Errorf("PinSupport = %q, %v, %v; want the old raptor, unsupported", path, supported, installed)
	}

	fakeRaptor(t, true)
	if st := Resolve(); st.Profile != "acme" || st.Source != SourcePin || st.IgnoredPin != "" {
		t.Errorf("new raptor: %+v, want acme from the pin", st)
	}
}
