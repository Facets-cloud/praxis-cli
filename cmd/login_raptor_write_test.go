package cmd

import (
	"bytes"
	"encoding/json"
	"io"
	"os"
	"path/filepath"
	"strings"
	"testing"
	"time"

	"github.com/Facets-cloud/praxis-cli/internal/credentials"
	"github.com/Facets-cloud/praxis-cli/internal/paths"
)

// readRaptorCreds returns the sections of <dir>/.facets/credentials, or nil
// when the file does not exist.
func readRaptorCreds(t *testing.T, dir string) map[string]map[string]string {
	t.Helper()
	data, err := os.ReadFile(filepath.Join(dir, ".facets", "credentials"))
	if os.IsNotExist(err) {
		return nil
	}
	if err != nil {
		t.Fatal(err)
	}
	return credentials.ParseRawINI(data)
}

// stubAuthMeOK verifies any token and echoes the URL as canonical.
func stubAuthMeOK(t *testing.T) {
	t.Helper()
	stubAuthMe(t, func(baseURL string, _ map[string]string) (*authMeResponse, error) {
		return &authMeResponse{Email: "u@x", canonicalBaseURL: baseURL}, nil
	})
}

// driveInteractivePAT runs the real control-plane PAT tier, with the browser
// handshake stubbed to deposit immediately. The API-key tier is stubbed to fail
// the test, so anything that falls through is caught.
func driveInteractivePAT(t *testing.T, username, token string) {
	t.Helper()
	clearFacetsEnv(t)
	stubHandshake(t, sessionCredential{Token: token, Username: username}, nil)
	stubAuthMode(t, facetsAuthMode)
	stubOpenBrowser(t)
	orig := interactivePATFn
	interactivePATFn = func(out io.Writer, _ bool, profileName, baseURL string, timeout time.Duration, local bool) (bool, error) {
		return tryInteractivePAT(out, false, profileName, baseURL, time.Minute, local)
	}
	t.Cleanup(func() { interactivePATFn = orig })
	browsed := stubBrowserLogin(t)
	t.Cleanup(func() {
		if *browsed {
			t.Error("login fell through to the API-key browser")
		}
	})
}

func TestLogin_PAT_WritesRaptorProfile(t *testing.T) {
	tests := []struct {
		name        string
		profile     string // "" = default
		pin         string // --raptor-profile
		wantSection string
		wantPin     string
	}{
		{name: "default profile writes [default], no pin", wantSection: "default"},
		{name: "named profile writes its own section and pins it", profile: "acme", wantSection: "acme", wantPin: "acme"},
		{name: "explicit pairing names the section", profile: "acme", pin: "cp-acme", wantSection: "cp-acme", wantPin: "cp-acme"},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			isolateHome(t)
			resetLoginFlags(t)
			stubPostAuth(t)
			stubAuthMeOK(t)
			driveInteractivePAT(t, "u@corp", "pat-pasted")

			rootProfile = tt.profile
			loginRaptorProfile = tt.pin
			loginURL = "https://cp.test"
			out, err := runLoginRunE(t)
			if err != nil {
				t.Fatalf("login err: %v\n%s", err, out)
			}

			secs := readRaptorCreds(t, os.Getenv("HOME"))
			got := secs[tt.wantSection]
			if got == nil {
				t.Fatalf("raptor section %q not written; sections: %v", tt.wantSection, secs)
			}
			if got["control_plane_url"] != "https://cp.test" || got["username"] != "u@corp" || got["token"] != "pat-pasted" {
				t.Errorf("raptor section = %v", got)
			}
			name := tt.profile
			if name == "" {
				name = credentials.DefaultProfileName
			}
			if pin := mustLoadProfile(t, name).RaptorProfile; pin != tt.wantPin {
				t.Errorf("raptor_profile pin = %q, want %q", pin, tt.wantPin)
			}
			if !strings.Contains(out, "raptor profile") {
				t.Errorf("output should report the raptor write:\n%s", out)
			}
		})
	}
}

func TestLogin_APIKey_LeavesRaptorAlone(t *testing.T) {
	// --token is a Praxis API key: raptor cannot use it, so nothing is written
	// and the profile gets no pairing it cannot honor.
	isolateHome(t)
	resetLoginFlags(t)
	clearFacetsEnv(t)
	stubPostAuth(t)
	stubAuthMeOK(t)

	loginURL = "https://cp.test"
	loginToken = "sk_live_key"
	out, err := runLoginRunE(t)
	if err != nil {
		t.Fatalf("login err: %v", err)
	}
	if secs := readRaptorCreds(t, os.Getenv("HOME")); secs != nil {
		t.Errorf("raptor credentials written for an API key: %v", secs)
	}
	if pin := mustLoadProfile(t, "default").RaptorProfile; pin != "" {
		t.Errorf("API-key profile got a raptor pin %q", pin)
	}
	if strings.Contains(out, "raptor_credentials") {
		t.Errorf("output claims a raptor write:\n%s", out)
	}
}

func TestLogin_StoredPATReuse_RestoresRaptorProfile(t *testing.T) {
	// A facets profile whose raptor section went missing gets it back on the
	// next login through the no-browser reuse path.
	isolateHome(t)
	resetLoginFlags(t)
	clearFacetsEnv(t)
	stubPostAuth(t)
	stubAuthMeOK(t)
	if err := credentials.Put("default", credentials.FacetsProfile("https://cp.test", "u@corp", "pat")); err != nil {
		t.Fatal(err)
	}

	if _, err := runLoginRunE(t); err != nil {
		t.Fatalf("login err: %v", err)
	}
	got := readRaptorCreds(t, os.Getenv("HOME"))["default"]
	if got["token"] != "pat" || got["control_plane_url"] != "https://cp.test" {
		t.Errorf("raptor [default] = %v", got)
	}
}

func TestLogin_PAT_OverwritesForeignDefault(t *testing.T) {
	// Decision: match `raptor login` — the section is replaced, and the other
	// raptor sections are untouched. The stored raptor PAT is rejected first
	// so the login reaches the prompt.
	isolateHome(t)
	resetLoginFlags(t)
	stubPostAuth(t)
	driveInteractivePAT(t, "u@corp", "pat-new")
	stubAuthMe(t, func(_ string, auth map[string]string) (*authMeResponse, error) {
		if auth["Authorization"] == "Bearer pat-old" {
			return nil, errTokenRejected
		}
		return &authMeResponse{Email: "u@corp"}, nil
	})
	seedRaptorCreds(t, "[default]\ncontrol_plane_url = https://cp.test\nusername = u@corp\ntoken = pat-old\n\n[keep]\ncontrol_plane_url = https://keep.test\nusername = k\ntoken = kt\n")

	loginURL = "https://cp.test"
	if _, err := runLoginRunE(t); err != nil {
		t.Fatalf("login err: %v", err)
	}
	secs := readRaptorCreds(t, os.Getenv("HOME"))
	if secs["default"]["token"] != "pat-new" {
		t.Errorf("[default] not replaced: %v", secs["default"])
	}
	if secs["keep"]["token"] != "kt" || secs["keep"]["control_plane_url"] != "https://keep.test" {
		t.Errorf("[keep] damaged: %v", secs["keep"])
	}
}

func TestLogin_Local_WritesRaptorCredsInRepo(t *testing.T) {
	// --local mirrors `raptor login --local`: the PAT lands in
	// <cwd>/.facets/credentials with a .gitignore, and the home file is not
	// touched.
	home := t.TempDir()
	t.Setenv("HOME", home)
	resetLoginFlags(t)
	stubPostAuth(t)
	stubAuthMeOK(t)
	driveInteractivePAT(t, "u@corp", "pat-local")

	repo := filepath.Join(home, "repo")
	if err := os.MkdirAll(repo, 0o755); err != nil {
		t.Fatal(err)
	}
	t.Cleanup(paths.SetGetwdForTest(func() (string, error) { return repo, nil }))

	rootProfile = "acme"
	loginLocal = true
	loginURL = "https://acme.test"
	if _, err := runLoginRunE(t); err != nil {
		t.Fatalf("login --local err: %v", err)
	}

	got := readRaptorCreds(t, repo)["acme"]
	if got["token"] != "pat-local" || got["control_plane_url"] != "https://acme.test" {
		t.Errorf("repo raptor [acme] = %v", got)
	}
	if _, err := os.Stat(filepath.Join(repo, ".facets", ".gitignore")); err != nil {
		t.Errorf(".gitignore missing: %v", err)
	}
	if secs := readRaptorCreds(t, home); secs != nil {
		t.Errorf("home raptor credentials written by a --local login: %v", secs)
	}
}

func TestLogin_JSON_ReportsRaptorWrite(t *testing.T) {
	isolateHome(t)
	resetLoginFlags(t)
	clearFacetsEnv(t)
	stubPostAuth(t)
	stubAuthMeOK(t)
	if err := credentials.Put("default", credentials.FacetsProfile("https://cp.test", "u@corp", "pat")); err != nil {
		t.Fatal(err)
	}

	loginJSON = true
	out, err := runLoginRunE(t)
	if err != nil {
		t.Fatalf("login err: %v", err)
	}
	var payload struct {
		Raptor struct {
			Path    string `json:"path"`
			Section string `json:"section"`
			Changed bool   `json:"changed"`
		} `json:"raptor_credentials"`
	}
	if err := json.Unmarshal([]byte(out), &payload); err != nil {
		t.Fatalf("bad JSON: %v\n%s", err, out)
	}
	want := filepath.Join(os.Getenv("HOME"), ".facets", "credentials")
	if payload.Raptor.Path != want || payload.Raptor.Section != "default" || !payload.Raptor.Changed {
		t.Errorf("raptor_credentials = %+v", payload.Raptor)
	}
}

func TestPromptLoginURL(t *testing.T) {
	tests := []struct {
		name   string
		asJSON bool
		tty    bool
		typed  string
		want   string
	}{
		{name: "bare host gets https", tty: true, typed: "acme.console.facets.cloud", want: "https://acme.console.facets.cloud"},
		{name: "trailing slash trimmed", tty: true, typed: "https://acme.test/", want: "https://acme.test"},
		{name: "http kept for a developer server", tty: true, typed: "http://localhost:8000", want: "http://localhost:8000"},
		{name: "empty answer keeps the usage error", tty: true, typed: "", want: ""},
		{name: "no tty never prompts", tty: false, typed: "https://acme.test", want: ""},
		{name: "json never prompts", asJSON: true, tty: true, typed: "https://acme.test", want: ""},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			stubPrompts(t, tt.typed)
			stdinIsTTY = func() bool { return tt.tty }
			if got := promptLoginURL(tt.asJSON); got != tt.want {
				t.Errorf("promptLoginURL() = %q, want %q", got, tt.want)
			}
		})
	}
}

func TestLogin_NoURL_MachineMode_StillUsageError(t *testing.T) {
	// A non-TTY invocation (what an AI host does) never blocks on a prompt.
	isolateHome(t)
	resetLoginFlags(t)
	clearFacetsEnv(t)
	stubPrompts(t, "https://cp.test")

	if _, err := runLoginRunE(t); err == nil || !strings.Contains(err.Error(), "--url") {
		t.Errorf("err = %v, want the --url usage error", err)
	}
}

func TestResolveLoginURL_NamedProfileOnlyInheritsItsOwnRaptorSection(t *testing.T) {
	const both = "[default]\ncontrol_plane_url = https://cp.test\nusername = u\ntoken = t\n\n[acme]\ncontrol_plane_url = https://acme.test\nusername = u\ntoken = t\n"
	tests := []struct {
		name, creds, profile, flagPin, want string
		wantErr                             bool
	}{
		{name: "default profile takes raptor's default", creds: both, profile: "default", want: "https://cp.test"},
		{name: "default profile takes a sole raptor profile", creds: "[root]\ncontrol_plane_url = https://root.test\nusername = u\ntoken = t\n", profile: "default", want: "https://root.test"},
		{name: "named profile takes its same-named section", creds: both, profile: "acme", want: "https://acme.test"},
		{name: "named profile does not inherit raptor's default", creds: "[default]\ncontrol_plane_url = https://cp.test\nusername = u\ntoken = t\n", profile: "snabbit", wantErr: true},
		{name: "explicit pin still wins for a named profile", creds: both, profile: "snabbit", flagPin: "default", want: "https://cp.test"},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			isolateHome(t)
			resetLoginFlags(t)
			clearFacetsEnv(t)
			seedRaptorCreds(t, tt.creds)
			loginRaptorProfile = tt.flagPin
			got, err := resolveLoginURL(tt.profile, "")
			if (err != nil) != tt.wantErr || got != tt.want {
				t.Errorf("resolveLoginURL(%q) = %q, %v; want %q, wantErr %v", tt.profile, got, err, tt.want, tt.wantErr)
			}
		})
	}
}

func TestLoginDryRun_ReportsRaptorEffect(t *testing.T) {
	tests := []struct {
		name        string
		raptorCreds string
		stored      *credentials.Profile
		local       bool
		want        string
	}{
		{
			name:        "raptor PAT reuse writes the section back",
			raptorCreds: "[default]\ncontrol_plane_url = https://cp.test\nusername = u\ntoken = pat\n",
			want:        "raptor profile [default] written to ~/.facets/credentials",
		},
		{
			name:   "stored API key leaves raptor alone",
			stored: &credentials.Profile{URL: "https://cp.test", Username: "u", Token: "sk"},
			want:   "unchanged",
		},
		{
			name:   "stored PAT reuse writes the section",
			stored: &credentials.Profile{URL: "https://cp.test", Username: "u", Token: "pat", AuthMode: credentials.AuthModeBasic},
			want:   "raptor profile [default] written to ~/.facets/credentials",
		},
		{
			name:   "local scope names the project file",
			stored: &credentials.Profile{URL: "https://cp.test", Username: "u", Token: "pat", AuthMode: credentials.AuthModeBasic},
			local:  true,
			want:   "written to <cwd>/.facets/credentials",
		},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			isolateHome(t)
			resetLoginFlags(t)
			clearFacetsEnv(t)
			stubAuthMeOK(t)
			if tt.raptorCreds != "" {
				seedRaptorCreds(t, tt.raptorCreds)
			}
			if tt.stored != nil {
				if err := credentials.Put("default", *tt.stored); err != nil {
					t.Fatal(err)
				}
			}
			var buf bytes.Buffer
			if err := runLoginDryRun(&buf, true, "default", "https://cp.test", tt.local); err != nil {
				t.Fatalf("dry run: %v", err)
			}
			var report map[string]any
			if err := json.Unmarshal(buf.Bytes(), &report); err != nil {
				t.Fatalf("bad JSON: %v\n%s", err, buf.String())
			}
			effect, _ := report["raptor_effect"].(string)
			if !strings.Contains(effect, tt.want) {
				t.Errorf("raptor_effect = %q, want it to contain %q (action %v)", effect, tt.want, report["action"])
			}
		})
	}
}
