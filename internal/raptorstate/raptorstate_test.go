package raptorstate

import (
	"errors"
	"os"
	"path/filepath"
	"testing"
)

// writeCreds drops a raptor-style credentials file into a temp dir and
// returns its path.
func writeCreds(t *testing.T, body string) string {
	t.Helper()
	path := filepath.Join(t.TempDir(), "credentials")
	if err := os.WriteFile(path, []byte(body), 0o600); err != nil {
		t.Fatal(err)
	}
	return path
}

// clearRaptorEnv unsets every env var the resolver consults so tests are
// hermetic regardless of the developer's shell.
func clearRaptorEnv(t *testing.T) {
	t.Helper()
	for _, k := range []string{"CONTROL_PLANE_URL", "FACETS_USERNAME", "FACETS_TOKEN", "FACETS_PROFILE"} {
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

const twoProfiles = `
[default]
control_plane_url = https://root.console.facets.cloud
username = anuj@facets.cloud
token = pat-root

[acme]
control_plane_url = https://acme.console.facets.cloud
username = anuj@facets.cloud
token = pat-acme
`

func TestResolve_Chain(t *testing.T) {
	tests := []struct {
		name    string
		creds   string // credentials body; "" = no file
		pin     string
		env     map[string]string
		want    State
		wantURL string
	}{
		{
			name:  "env override wins over everything",
			creds: twoProfiles,
			pin:   "acme",
			env:   map[string]string{"CONTROL_PLANE_URL": "https://env.example.com", "FACETS_USERNAME": "env@x"},
			want:  State{Found: true, Profile: "env", Source: SourceEnv, ControlPlaneURL: "https://env.example.com", Username: "env@x"},
		},
		{
			name:  "pin beats FACETS_PROFILE",
			creds: twoProfiles,
			pin:   "acme",
			env:   map[string]string{"FACETS_PROFILE": "default"},
			want:  State{Found: true, Pinned: true, Profile: "acme", Source: SourcePin, ControlPlaneURL: "https://acme.console.facets.cloud", Username: "anuj@facets.cloud"},
		},
		{
			name:  "pin to missing profile reports the name, not found",
			creds: twoProfiles,
			pin:   "nope",
			want:  State{Found: false, Pinned: true, Profile: "nope", Source: SourcePin},
		},
		{
			name:  "FACETS_PROFILE selects named profile",
			creds: twoProfiles,
			env:   map[string]string{"FACETS_PROFILE": "acme"},
			want:  State{Found: true, Profile: "acme", Source: SourceEnvProfile, ControlPlaneURL: "https://acme.console.facets.cloud", Username: "anuj@facets.cloud"},
		},
		{
			name:  "FACETS_PROFILE naming a missing profile is reported unfound",
			creds: twoProfiles,
			env:   map[string]string{"FACETS_PROFILE": "ghost"},
			want:  State{Found: false, Profile: "ghost", Source: SourceEnvProfile},
		},
		{
			name:  "default section when nothing else selects",
			creds: twoProfiles,
			want:  State{Found: true, Profile: "default", Source: SourceDefault, ControlPlaneURL: "https://root.console.facets.cloud", Username: "anuj@facets.cloud"},
		},
		{
			name: "sole profile used when no default exists",
			creds: `
[onlyone]
control_plane_url = https://solo.console.facets.cloud
username = solo@x
token = pat
`,
			want: State{Found: true, Profile: "onlyone", Source: SourceSole, ControlPlaneURL: "https://solo.console.facets.cloud", Username: "solo@x"},
		},
		{
			name: "multiple profiles without default resolve nothing",
			creds: `
[a]
control_plane_url = https://a.example
username = u
token = t

[b]
control_plane_url = https://b.example
username = u
token = t
`,
			want: State{Found: false},
		},
		{
			name:  "no credentials file resolves nothing",
			creds: "",
			want:  State{Found: false},
		},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			clearRaptorEnv(t)
			for k, v := range tt.env {
				t.Setenv(k, v)
			}
			stubInstalled(t, true)

			path := ""
			if tt.creds != "" {
				path = writeCreds(t, tt.creds)
			}
			got := resolve(tt.pin, path)
			tt.want.Installed = true // stubbed above for every case
			if got != tt.want {
				t.Errorf("resolve() = %+v, want %+v", got, tt.want)
			}
		})
	}
}

func TestResolve_InstalledFlag(t *testing.T) {
	clearRaptorEnv(t)
	stubInstalled(t, false)
	got := resolve("", "")
	if got.Installed {
		t.Errorf("Installed = true with lookPath failing; want false")
	}
}

func TestResolve_DefaultPathMissingFile(t *testing.T) {
	// Resolve (the exported entry) against a faked HOME with no
	// ~/.facets/credentials must not error or find anything.
	clearRaptorEnv(t)
	stubInstalled(t, true)
	t.Setenv("HOME", t.TempDir())
	got := Resolve("")
	if got.Found {
		t.Errorf("Resolve() with no credentials file: Found = true, want false")
	}
}

func TestHasProfile(t *testing.T) {
	path := writeCreds(t, twoProfiles)

	ok, url := hasProfile("acme", path)
	if !ok || url != "https://acme.console.facets.cloud" {
		t.Errorf("hasProfile(acme) = (%v, %q), want (true, acme URL)", ok, url)
	}
	if ok, _ := hasProfile("ghost", path); ok {
		t.Errorf("hasProfile(ghost) = true, want false")
	}
	if ok, _ := hasProfile("acme", filepath.Join(t.TempDir(), "nope")); ok {
		t.Errorf("hasProfile with missing file = true, want false")
	}

	// Exported wrapper against a faked HOME.
	home := t.TempDir()
	t.Setenv("HOME", home)
	if err := os.MkdirAll(filepath.Join(home, ".facets"), 0o700); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(filepath.Join(home, ".facets", "credentials"), []byte(twoProfiles), 0o600); err != nil {
		t.Fatal(err)
	}
	ok, url = HasProfile("default")
	if !ok || url != "https://root.console.facets.cloud" {
		t.Errorf("HasProfile(default) = (%v, %q), want (true, root URL)", ok, url)
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
