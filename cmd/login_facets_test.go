package cmd

import (
	"errors"
	"io"
	"os"
	"path/filepath"
	"testing"

	"github.com/Facets-cloud/praxis-cli/internal/credentials"
	"github.com/Facets-cloud/praxis-cli/internal/exitcode"
)

// captureStderr redirects os.Stderr for the duration of the test; the returned
// func reads what was written. login's fallback notices go to stderr so they
// never corrupt --json output on stdout.
func captureStderr(t *testing.T) func() string {
	t.Helper()
	r, w, err := os.Pipe()
	if err != nil {
		t.Fatal(err)
	}
	orig := os.Stderr
	os.Stderr = w
	t.Cleanup(func() { os.Stderr = orig })
	return func() string {
		w.Close()
		b, _ := io.ReadAll(r)
		return string(b)
	}
}

// clearFacetsEnv keeps raptor's env-based resolution (which raptorstate
// mirrors) out of the fake HOME the credentials file lives in.
func clearFacetsEnv(t *testing.T) {
	t.Helper()
	for _, k := range []string{"CONTROL_PLANE_URL", "FACETS_USERNAME", "FACETS_TOKEN", "FACETS_PROFILE"} {
		t.Setenv(k, "")
		os.Unsetenv(k)
	}
}

// seedRaptorCreds writes a raptor-style ~/.facets/credentials into the
// (already isolated) fake HOME — the shared store both CLIs read.
func seedRaptorCreds(t *testing.T, body string) {
	t.Helper()
	dir := filepath.Join(os.Getenv("HOME"), ".facets")
	if err := os.MkdirAll(dir, 0o700); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(filepath.Join(dir, "credentials"), []byte(body), 0o600); err != nil {
		t.Fatal(err)
	}
}

func mustLoadProfile(t *testing.T, name string) credentials.Profile {
	t.Helper()
	store, err := credentials.Load()
	if err != nil {
		t.Fatal(err)
	}
	return store[name]
}

func TestLoginRunE_FacetsPATIsPrimary(t *testing.T) {
	isolateHome(t)
	resetLoginFlags(t)
	clearFacetsEnv(t)
	seedRaptorCreds(t, "[default]\ncontrol_plane_url = https://cp.test\nusername = u@corp\ntoken = pat123\n")
	stubPostAuth(t)
	browsed := stubBrowserLogin(t)
	var gotAuth map[string]string
	stubAuthMe(t, func(_ string, auth map[string]string) (*authMeResponse, error) {
		gotAuth = auth
		return &authMeResponse{Email: "u@corp"}, nil
	})

	loginURL = "https://cp.test"
	if _, err := runLoginRunE(t); err != nil {
		t.Fatalf("login err: %v", err)
	}
	if *browsed {
		t.Error("browser opened despite a usable control-plane PAT")
	}
	if gotAuth["Authorization"] != "Bearer pat123" || gotAuth["X-Facets-Username"] != "u@corp" {
		t.Errorf("auth headers = %v, want Bearer pat123 + X-Facets-Username u@corp", gotAuth)
	}
	store, err := credentials.Load()
	if err != nil {
		t.Fatal(err)
	}
	prof := store["default"]
	if prof.Token != "pat123" || prof.Username != "u@corp" || prof.AuthMode != credentials.AuthModeBasic {
		t.Errorf("persisted profile = %+v, want the PAT in %q mode", prof, credentials.AuthModeBasic)
	}
}

// `praxis login` with NO flags: raptor's control plane supplies both the URL
// and the credential.
func TestLoginRunE_BareLoginAdoptsRaptorControlPlane(t *testing.T) {
	isolateHome(t)
	resetLoginFlags(t)
	clearFacetsEnv(t)
	seedRaptorCreds(t, "[default]\ncontrol_plane_url = https://cp.test\nusername = u@corp\ntoken = pat123\n")
	stubPostAuth(t)
	browsed := stubBrowserLogin(t)
	var gotURL string
	stubAuthMe(t, func(baseURL string, _ map[string]string) (*authMeResponse, error) {
		gotURL = baseURL
		return &authMeResponse{Email: "u@corp"}, nil
	})

	if _, err := runLoginRunE(t); err != nil {
		t.Fatalf("login err: %v", err)
	}
	if *browsed {
		t.Error("browser opened despite raptor being logged in")
	}
	if gotURL != "https://cp.test" {
		t.Errorf("verified against %q, want raptor's control plane", gotURL)
	}
	if got := mustLoadProfile(t, "default").URL; got != "https://cp.test" {
		t.Errorf("persisted URL = %q, want raptor's control plane", got)
	}
}

func TestLoginRunE_FallsBackToBrowserWhenPATRejected(t *testing.T) {
	isolateHome(t)
	resetLoginFlags(t)
	clearFacetsEnv(t)
	seedRaptorCreds(t, "[default]\ncontrol_plane_url = https://cp.test\nusername = u@corp\ntoken = pat123\n")
	stubPostAuth(t)
	browsed := stubBrowserLogin(t)
	stubAuthMe(t, func(_ string, _ map[string]string) (*authMeResponse, error) {
		return nil, errTokenRejected
	})

	loginURL = "https://cp.test"
	if _, err := runLoginRunE(t); err != nil {
		t.Fatalf("login err: %v", err)
	}
	if !*browsed {
		t.Error("browser did not open after the PAT was rejected")
	}
	// The rejected PAT is raptor's own section in the shared store; praxis
	// leaves it as it is and writes nothing of its own.
	if praxis := readPraxisFile(t); praxis != nil {
		t.Errorf("a rejected PAT must not be persisted; praxis file = %v", praxis)
	}
	if got := readFacetsFile(t, os.Getenv("HOME"))["default"]["token"]; got != "pat123" {
		t.Errorf("raptor's section changed by a failed login: %q", got)
	}
}

// A transient failure is not a verdict on the PAT. The shared store makes
// raptor's PAT this profile's stored token, so login takes the stored-token
// path: it refuses to clobber a possibly-valid credential over a network blip,
// leaves the store untouched, and exits with the network code.
func TestLoginRunE_TransientErrorIsNotAPATVerdict(t *testing.T) {
	isolateHome(t)
	resetLoginFlags(t)
	clearFacetsEnv(t)
	seedRaptorCreds(t, "[default]\ncontrol_plane_url = https://cp.test\nusername = u@corp\ntoken = pat123\n")
	stubPostAuth(t)
	browsed := stubBrowserLogin(t)
	code := stubOsExit(t)
	stubAuthMe(t, func(_ string, _ map[string]string) (*authMeResponse, error) {
		return nil, errors.New("dial tcp: i/o timeout")
	})

	loginURL = "https://cp.test"
	_, _ = runLoginRunE(t)
	if *code != exitcode.Network {
		t.Errorf("exit code = %d, want %d (network)", *code, exitcode.Network)
	}
	if *browsed {
		t.Error("browser opened over a transient failure; the stored PAT may still be valid")
	}
	if got := readFacetsFile(t, os.Getenv("HOME"))["default"]["token"]; got != "pat123" {
		t.Errorf("stored PAT changed by an unverified login: %q", got)
	}
	if praxis := readPraxisFile(t); praxis != nil {
		t.Errorf("praxis file written: %v", praxis)
	}
}

func TestLoginRunE_NoFacetsCredsGoesStraightToBrowser(t *testing.T) {
	isolateHome(t)
	resetLoginFlags(t)
	clearFacetsEnv(t)
	stubPostAuth(t)
	browsed := stubBrowserLogin(t)
	authMeCalls := 0
	stubAuthMe(t, func(_ string, _ map[string]string) (*authMeResponse, error) {
		authMeCalls++
		return &authMeResponse{Email: "u@corp"}, nil
	})

	loginURL = "https://cp.test"
	if _, err := runLoginRunE(t); err != nil {
		t.Fatalf("login err: %v", err)
	}
	if !*browsed {
		t.Error("browser did not open with no PAT and no stored token")
	}
	if authMeCalls != 0 {
		t.Errorf("auth/me called %d times with nothing to verify, want 0", authMeCalls)
	}
}

func TestFacetsPATCandidate(t *testing.T) {
	const twoSections = "[default]\ncontrol_plane_url = https://cp.test\nusername = u@corp\ntoken = pat\n\n" +
		"[acme]\ncontrol_plane_url = https://acme.test\nusername = u@acme\ntoken = pat2\n"
	tests := []struct {
		name        string
		creds       string // raptor credentials body; "" = no file
		env         string // FACETS_PROFILE
		baseURL     string
		wantSection string
		wantOK      bool
	}{
		{
			name:        "default section, host matches the control plane",
			creds:       "[default]\ncontrol_plane_url = https://cp.test\nusername = u@corp\ntoken = pat\n",
			baseURL:     "https://cp.test",
			wantSection: "default",
			wantOK:      true,
		},
		{
			name:        "sole non-default section",
			creds:       "[acme]\ncontrol_plane_url = https://cp.test\nusername = u@acme\ntoken = pat\n",
			baseURL:     "https://cp.test",
			wantSection: "acme",
			wantOK:      true,
		},
		{
			// Two sections and no selector: raptor itself resolves nothing.
			name:    "multiple sections without a default give no candidate",
			creds:   "[a]\ncontrol_plane_url = https://cp.test\nusername = u\ntoken = t\n\n[b]\ncontrol_plane_url = https://cp.test\nusername = u\ntoken = t\n",
			baseURL: "https://cp.test",
			wantOK:  false,
		},
		{
			name:        "FACETS_PROFILE selects the section",
			creds:       twoSections,
			env:         "acme",
			baseURL:     "https://acme.test",
			wantSection: "acme",
			wantOK:      true,
		},
		{
			name:        "loopback agent server is exempt from the host gate",
			creds:       "[default]\ncontrol_plane_url = https://cp.test\nusername = u@corp\ntoken = pat\n",
			baseURL:     "http://localhost:8000",
			wantSection: "default",
			wantOK:      true,
		},
		{
			// http:// to the CP would put the PAT on the wire in cleartext.
			name:    "plaintext http to the control plane gets no candidate",
			creds:   "[default]\ncontrol_plane_url = https://cp.test\nusername = u@corp\ntoken = pat\n",
			baseURL: "http://cp.test",
			wantOK:  false,
		},
		{
			// A control-plane PAT must never reach a host that isn't that CP.
			name:    "foreign host gets no candidate",
			creds:   "[default]\ncontrol_plane_url = https://cp.test\nusername = u@corp\ntoken = pat\n",
			baseURL: "https://other.example",
			wantOK:  false,
		},
		{
			name:    "section with no control_plane_url can't match any host",
			creds:   "[default]\nusername = u@corp\ntoken = pat\n",
			baseURL: "https://cp.test",
			wantOK:  false,
		},
		{
			name:    "no raptor credentials",
			baseURL: "https://cp.test",
			wantOK:  false,
		},
		{
			name:    "section without a token",
			creds:   "[default]\ncontrol_plane_url = https://cp.test\nusername = u@corp\n",
			baseURL: "https://cp.test",
			wantOK:  false,
		},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			isolateHome(t)
			resetLoginFlags(t)
			clearFacetsEnv(t)
			if tt.creds != "" {
				seedRaptorCreds(t, tt.creds)
			}
			if tt.env != "" {
				t.Setenv("FACETS_PROFILE", tt.env)
			}

			c, ok := facetsPATCandidate("default", tt.baseURL)
			if ok != tt.wantOK {
				t.Fatalf("ok = %v, want %v (candidate %+v)", ok, tt.wantOK, c)
			}
			if !ok {
				return
			}
			if c.section != tt.wantSection {
				t.Errorf("facets section = %q, want %q", c.section, tt.wantSection)
			}
			if c.url != tt.baseURL {
				t.Errorf("target url = %q, want %q", c.url, tt.baseURL)
			}
			if c.token == "" {
				t.Error("token is empty on an ok candidate")
			}
		})
	}
}

// With askpraxis.ai gone (#74), raptor's control plane is what makes a bare
// `praxis login` resolve to a URL at all.
func TestResolveLoginURL_RaptorControlPlaneFallback(t *testing.T) {
	tests := []struct {
		name     string
		creds    string
		storeURL string
		want     string
		wantErr  bool
	}{
		{
			name:  "raptor's control plane when nothing else names one",
			creds: "[default]\ncontrol_plane_url = https://cp.test/\nusername = u@corp\ntoken = pat\n",
			want:  "https://cp.test",
		},
		{
			name:     "a stored URL still wins",
			creds:    "[default]\ncontrol_plane_url = https://cp.test\nusername = u@corp\ntoken = pat\n",
			storeURL: "https://stored.test",
			want:     "https://stored.test",
		},
		{
			name:    "no raptor, no stored URL, no --url is still an error",
			wantErr: true,
		},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			isolateHome(t)
			resetLoginFlags(t)
			clearFacetsEnv(t)
			if tt.creds != "" {
				seedRaptorCreds(t, tt.creds)
			}
			if tt.storeURL != "" {
				if err := credentials.Put("default", credentials.Profile{URL: tt.storeURL}); err != nil {
					t.Fatal(err)
				}
			}
			got, err := resolveLoginURL("default", "")
			if (err != nil) != tt.wantErr {
				t.Fatalf("err = %v, wantErr %v", err, tt.wantErr)
			}
			if got != tt.want {
				t.Errorf("resolveLoginURL() = %q, want %q", got, tt.want)
			}
		})
	}
}
