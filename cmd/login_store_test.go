package cmd

import (
	"bytes"
	"encoding/json"
	"io"
	"os"
	"path/filepath"
	"strings"
	"testing"

	"github.com/Facets-cloud/praxis-cli/internal/credentials"
	"github.com/Facets-cloud/praxis-cli/internal/paths"
)

// readFacetsFile returns the sections of <dir>/.facets/credentials, or nil
// when the file does not exist.
func readFacetsFile(t *testing.T, dir string) map[string]map[string]string {
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

// readPraxisFile returns the sections of ~/.praxis/credentials, or nil.
func readPraxisFile(t *testing.T) map[string]map[string]string {
	t.Helper()
	p, err := paths.Credentials()
	if err != nil {
		t.Fatal(err)
	}
	data, err := os.ReadFile(p)
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

// driveInteractivePAT runs the real PAT prompt tier in human mode. RunE sees a
// non-TTY stdout under `go test` and would otherwise skip the prompt as a
// machine invocation. The browser tier is stubbed to fail the test.
func driveInteractivePAT(t *testing.T, username, token string) {
	t.Helper()
	clearFacetsEnv(t)
	stubPrompts(t, username, token)
	stubAuthMode(t, facetsAuthMode)
	stubOpenBrowser(t)
	orig := interactivePATFn
	interactivePATFn = func(out io.Writer, _ bool, profileName, baseURL string, local bool) (bool, error) {
		return tryInteractivePAT(out, false, profileName, baseURL, local)
	}
	t.Cleanup(func() { interactivePATFn = orig })
	browsed := stubBrowserLogin(t)
	t.Cleanup(func() {
		if *browsed {
			t.Error("login fell through to the API-key browser")
		}
	})
}

func TestLogin_PAT_SavedToSharedStoreOnly(t *testing.T) {
	tests := []struct {
		name    string
		profile string // "" = default
	}{
		{name: "default profile writes [default]"},
		{name: "named profile writes its own section", profile: "acme"},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			isolateHome(t)
			resetLoginFlags(t)
			stubPostAuth(t)
			stubAuthMeOK(t)
			driveInteractivePAT(t, "u@corp", "pat-pasted")

			rootProfile = tt.profile
			loginURL = "https://cp.test"
			out, err := runLoginRunE(t)
			if err != nil {
				t.Fatalf("login err: %v\n%s", err, out)
			}

			name := tt.profile
			if name == "" {
				name = credentials.DefaultProfileName
			}
			got := readFacetsFile(t, os.Getenv("HOME"))[name]
			if got == nil {
				t.Fatalf("facets section %q not written", name)
			}
			if got["control_plane_url"] != "https://cp.test" || got["username"] != "u@corp" || got["token"] != "pat-pasted" {
				t.Errorf("facets section = %v", got)
			}
			if praxis := readPraxisFile(t); praxis != nil {
				t.Errorf("praxis file written for a PAT login: %v", praxis)
			}
			if p := mustLoadProfile(t, name); p.Store != credentials.StoreFacets || p.AuthMode != credentials.AuthModeBasic {
				t.Errorf("Load() = %+v, want a facets PAT profile", p)
			}
			if !strings.Contains(out, "shared with raptor") {
				t.Errorf("output should say where the credential went:\n%s", out)
			}
		})
	}
}

func TestLogin_APIKey_SavedToPraxisFileOnly(t *testing.T) {
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
	if facets := readFacetsFile(t, os.Getenv("HOME")); facets != nil {
		t.Errorf("facets file written for an API key: %v", facets)
	}
	if praxis := readPraxisFile(t); praxis["default"]["token"] != "sk_live_key" {
		t.Errorf("praxis file = %v", praxis)
	}
	if p := mustLoadProfile(t, "default"); p.Store != credentials.StorePraxis || p.AuthMode != "" {
		t.Errorf("Load() = %+v, want a praxis API-key profile", p)
	}
	if strings.Contains(out, "shared with raptor") {
		t.Errorf("output claims raptor sharing for an API key:\n%s", out)
	}
}

func TestLogin_RaptorLoginIsAlreadyAPraxisLogin(t *testing.T) {
	// A `raptor login` writes the shared store; a bare `praxis login` then
	// needs no URL and no prompt, and writes nothing new.
	isolateHome(t)
	resetLoginFlags(t)
	clearFacetsEnv(t)
	stubPostAuth(t)
	browsed := stubBrowserLogin(t)
	stubAuthMeOK(t)
	seedRaptorCreds(t, "[default]\ncontrol_plane_url = https://cp.test\nusername = u@corp\ntoken = pat\n")
	before, _ := os.ReadFile(filepath.Join(os.Getenv("HOME"), ".facets", "credentials"))

	if _, err := runLoginRunE(t); err != nil {
		t.Fatalf("login err: %v", err)
	}
	if *browsed {
		t.Error("browser opened despite raptor being logged in")
	}
	after, _ := os.ReadFile(filepath.Join(os.Getenv("HOME"), ".facets", "credentials"))
	if !bytes.Equal(before, after) {
		t.Errorf("facets file rewritten by a reuse login:\n%s", after)
	}
	if praxis := readPraxisFile(t); praxis != nil {
		t.Errorf("praxis file written: %v", praxis)
	}
}

func TestLogin_NamedProfile_AdoptsRaptorsPATForSameHost(t *testing.T) {
	// `praxis login -p acme --url <raptor's CP>`: the PAT raptor holds for that
	// host is reused and a second section is written for the new name.
	isolateHome(t)
	resetLoginFlags(t)
	clearFacetsEnv(t)
	stubPostAuth(t)
	browsed := stubBrowserLogin(t)
	stubAuthMeOK(t)
	seedRaptorCreds(t, "[default]\ncontrol_plane_url = https://cp.test\nusername = u@corp\ntoken = pat\n")

	rootProfile = "acme"
	loginURL = "https://cp.test"
	if _, err := runLoginRunE(t); err != nil {
		t.Fatalf("login err: %v", err)
	}
	if *browsed {
		t.Error("browser opened despite a usable PAT for this host")
	}
	facets := readFacetsFile(t, os.Getenv("HOME"))
	if facets["acme"]["token"] != "pat" || facets["default"]["token"] != "pat" {
		t.Errorf("facets = %v, want both sections", facets)
	}
}

func TestLogin_PAT_ReplacesForeignSection(t *testing.T) {
	// Decision: match `raptor login` — the section is replaced, and the other
	// sections are untouched. The stored PAT is rejected first so the login
	// reaches the prompt.
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
	seedRaptorCreds(t, "[default]\ncontrol_plane_url = https://cp.test\nusername = u@corp\ntoken = pat-old\n\n[keep]\ncontrol_plane_url = https://keep.test\nusername = k\ntoken = kt\ncontrol_plane_project = p\n")

	loginURL = "https://cp.test"
	if _, err := runLoginRunE(t); err != nil {
		t.Fatalf("login err: %v", err)
	}
	facets := readFacetsFile(t, os.Getenv("HOME"))
	if facets["default"]["token"] != "pat-new" {
		t.Errorf("[default] not replaced: %v", facets["default"])
	}
	if facets["keep"]["token"] != "kt" || facets["keep"]["control_plane_project"] != "p" {
		t.Errorf("[keep] damaged: %v", facets["keep"])
	}
}

func TestLogin_Local_WritesProjectFacetsFile(t *testing.T) {
	// --local is raptor's local mode: the PAT lands in <cwd>/.facets/credentials
	// with a .gitignore, and the home file is not touched.
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

	got := readFacetsFile(t, repo)["acme"]
	if got["token"] != "pat-local" || got["control_plane_url"] != "https://acme.test" {
		t.Errorf("repo facets [acme] = %v", got)
	}
	if _, err := os.Stat(filepath.Join(repo, ".facets", ".gitignore")); err != nil {
		t.Errorf(".gitignore missing: %v", err)
	}
	if facets := readFacetsFile(t, home); facets != nil {
		t.Errorf("home facets file written by a --local login: %v", facets)
	}
	// Inside the tree, praxis reads the local file — the same one raptor does.
	t.Cleanup(credentials.SetGetwdForTest(func() (string, error) { return repo, nil }))
	if a, _ := credentials.ResolveActive(""); a.Name != "acme" || !a.Loaded || a.Profile.Store != credentials.StoreFacets {
		t.Errorf("in-repo resolution = %+v, want acme from the local facets file", a)
	}
}

func TestLogin_JSON_ReportsStore(t *testing.T) {
	isolateHome(t)
	resetLoginFlags(t)
	clearFacetsEnv(t)
	stubPostAuth(t)
	stubAuthMeOK(t)
	seedRaptorCreds(t, "[default]\ncontrol_plane_url = https://cp.test\nusername = u@corp\ntoken = pat\n")

	loginJSON = true
	out, err := runLoginRunE(t)
	if err != nil {
		t.Fatalf("login err: %v", err)
	}
	var payload struct {
		Path   string `json:"credentials_path"`
		Shared bool   `json:"shared_with_raptor"`
	}
	if err := json.Unmarshal([]byte(out), &payload); err != nil {
		t.Fatalf("bad JSON: %v\n%s", err, out)
	}
	want := filepath.Join(os.Getenv("HOME"), ".facets", "credentials")
	if payload.Path != want || !payload.Shared {
		t.Errorf("payload = %+v, want %s shared", payload, want)
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
			stubPrompts(t, tt.typed, "")
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
	stubPrompts(t, "https://cp.test", "")

	if _, err := runLoginRunE(t); err == nil || !strings.Contains(err.Error(), "--url") {
		t.Errorf("err = %v, want the --url usage error", err)
	}
}

func TestResolveLoginURL_NamedProfileOnlyInheritsItsOwnSection(t *testing.T) {
	const both = "[default]\ncontrol_plane_url = https://cp.test\nusername = u\ntoken = t\n\n[acme]\ncontrol_plane_url = https://acme.test\nusername = u\ntoken = t\n"
	tests := []struct {
		name, creds, profile, want string
		wantErr                    bool
	}{
		{name: "default profile takes raptor's default", creds: both, profile: "default", want: "https://cp.test"},
		{name: "default profile takes a sole section", creds: "[root]\ncontrol_plane_url = https://root.test\nusername = u\ntoken = t\n", profile: "default", want: "https://root.test"},
		{name: "named profile takes its same-named section", creds: both, profile: "acme", want: "https://acme.test"},
		{name: "named profile does not inherit raptor's default", creds: "[default]\ncontrol_plane_url = https://cp.test\nusername = u\ntoken = t\n", profile: "snabbit", wantErr: true},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			isolateHome(t)
			resetLoginFlags(t)
			clearFacetsEnv(t)
			seedRaptorCreds(t, tt.creds)
			got, err := resolveLoginURL(tt.profile, "")
			if (err != nil) != tt.wantErr || got != tt.want {
				t.Errorf("resolveLoginURL(%q) = %q, %v; want %q, wantErr %v", tt.profile, got, err, tt.want, tt.wantErr)
			}
		})
	}
}

func TestLoginDryRun_ReportsStoreEffect(t *testing.T) {
	tests := []struct {
		name  string
		creds string // shared store body
		key   bool   // seed a praxis API key instead
		local bool
		want  string
	}{
		{name: "raptor PAT reuse stays in the shared store", creds: "[default]\ncontrol_plane_url = https://cp.test\nusername = u\ntoken = pat\n", want: "~/.facets/credentials [default] (shared with raptor)"},
		{name: "stored API key stays in the praxis file", key: true, want: "~/.praxis/credentials [default] (Praxis API key; raptor unchanged)"},
		{name: "local scope names the project file", creds: "[default]\ncontrol_plane_url = https://cp.test\nusername = u\ntoken = pat\n", local: true, want: "<cwd>/.facets/credentials [default] (shared with raptor)"},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			isolateHome(t)
			resetLoginFlags(t)
			clearFacetsEnv(t)
			stubAuthMeOK(t)
			if tt.creds != "" {
				seedRaptorCreds(t, tt.creds)
			}
			if tt.key {
				if err := credentials.Put("default", credentials.Profile{URL: "https://cp.test", Username: "u", Token: "sk"}); err != nil {
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
			effect, _ := report["store_effect"].(string)
			if !strings.Contains(effect, tt.want) {
				t.Errorf("store_effect = %q, want it to contain %q (action %v)", effect, tt.want, report["action"])
			}
		})
	}
}

func TestLogout_RemovesSharedSectionAndSaysSo(t *testing.T) {
	isolateHome(t)
	clearFacetsEnv(t)
	seedRaptorCreds(t, "[default]\ncontrol_plane_url = https://cp.test\nusername = u\ntoken = pat\n\n[keep]\ncontrol_plane_url = https://keep.test\nusername = k\ntoken = kt\n")
	if err := credentials.SetActive("default"); err != nil {
		t.Fatal(err)
	}
	logoutAll, logoutJSON = false, true
	t.Cleanup(func() { logoutAll, logoutJSON = false, false })

	var buf bytes.Buffer
	logoutCmd.SetOut(&buf)
	if err := logoutCmd.RunE(logoutCmd, nil); err != nil {
		t.Fatalf("logout err: %v", err)
	}
	var out map[string]any
	if err := json.Unmarshal(buf.Bytes(), &out); err != nil {
		t.Fatalf("bad JSON: %v\n%s", err, buf.String())
	}
	if out["removed"] != "default" || out["raptor_logged_out"] != true {
		t.Errorf("logout payload = %v", out)
	}
	facets := readFacetsFile(t, os.Getenv("HOME"))
	if _, gone := facets["default"]; gone || facets["keep"]["token"] != "kt" {
		t.Errorf("facets after logout = %v, want only keep", facets)
	}
}
