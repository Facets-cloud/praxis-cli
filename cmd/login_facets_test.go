package cmd

import (
	"testing"

	"github.com/Facets-cloud/praxis-cli/internal/credentials"
)

// clearFacetsEnv keeps raptor's env-based resolution (which raptorstate
// mirrors) out of the fake HOME the credentials file lives in.
func clearFacetsEnv(t *testing.T) {
	t.Helper()
	t.Setenv("CONTROL_PLANE_URL", "")
	t.Setenv("FACETS_PROFILE", "")
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
	if store, _ := credentials.Load(); store["default"].Token != "" {
		t.Errorf("a rejected PAT must not be persisted; got %+v", store["default"])
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
		pin         string // praxis profile's stored raptor_profile
		flagPin     string // --raptor-profile
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
			name:        "stored praxis pin wins over default",
			creds:       twoSections,
			pin:         "acme",
			baseURL:     "https://acme.test",
			wantSection: "acme",
			wantOK:      true,
		},
		{
			name:        "--raptor-profile wins over a stored pin",
			creds:       twoSections,
			pin:         "default",
			flagPin:     "acme",
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
		{
			name:    "pin names a section raptor does not have",
			creds:   "[default]\ncontrol_plane_url = https://cp.test\nusername = u@corp\ntoken = pat\n",
			pin:     "ghost",
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
			if err := credentials.Put("default", credentials.Profile{RaptorProfile: tt.pin}); err != nil {
				t.Fatal(err)
			}
			loginRaptorProfile = tt.flagPin

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
		flagPin  string
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
			name:    "--raptor-profile selects which control plane",
			creds:   "[default]\ncontrol_plane_url = https://cp.test\nusername = u@corp\ntoken = pat\n\n[acme]\ncontrol_plane_url = https://acme.test\nusername = u@acme\ntoken = pat2\n",
			flagPin: "acme",
			want:    "https://acme.test",
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
			loginRaptorProfile = tt.flagPin

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
