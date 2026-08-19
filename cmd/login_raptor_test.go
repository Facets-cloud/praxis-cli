package cmd

import (
	"os"
	"path/filepath"
	"testing"

	"github.com/Facets-cloud/praxis-cli/internal/credentials"
	"github.com/spf13/pflag"
)

// seedRaptorCreds writes a raptor-style ~/.facets/credentials into the
// (already isolated) fake HOME.
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

func TestLoginRunE_RaptorProfileFlagStoresPairing(t *testing.T) {
	isolateHome(t)
	resetLoginFlags(t)
	seedProfile(t, "default", "https://root.test", "tok")
	seedRaptorCreds(t, "[root]\ncontrol_plane_url = https://root.test\nusername = u@x\ntoken = pat\n")
	stubPostAuth(t)
	stubAuthMe(t, func(_ string, _ map[string]string) (*authMeResponse, error) {
		return &authMeResponse{Email: "u@x"}, nil
	})

	loginRaptorProfile = "root"
	if _, err := runLoginRunE(t); err != nil {
		t.Fatalf("login err: %v", err)
	}
	store, err := credentials.Load()
	if err != nil {
		t.Fatal(err)
	}
	if got := store["default"].RaptorProfile; got != "root" {
		t.Errorf("RaptorProfile = %q, want %q", got, "root")
	}
}

func TestLoginRunE_RaptorPairingPreservedOnRelogin(t *testing.T) {
	isolateHome(t)
	resetLoginFlags(t)
	if err := credentials.Put("default", credentials.Profile{
		URL: "https://root.test", Username: "u@x", Token: "tok", RaptorProfile: "root",
	}); err != nil {
		t.Fatal(err)
	}
	stubPostAuth(t)
	stubAuthMe(t, func(_ string, _ map[string]string) (*authMeResponse, error) {
		return &authMeResponse{Email: "u@x"}, nil
	})

	// Re-login WITHOUT --raptor-profile: the pairing must survive.
	if _, err := runLoginRunE(t); err != nil {
		t.Fatalf("login err: %v", err)
	}
	store, err := credentials.Load()
	if err != nil {
		t.Fatal(err)
	}
	if got := store["default"].RaptorProfile; got != "root" {
		t.Errorf("RaptorProfile after re-login = %q, want preserved %q", got, "root")
	}
}

func TestResolveRaptorPairing_Warnings(t *testing.T) {
	// The warning branches are advisory (stderr) — assert the RETURN value:
	// the pairing is saved even when raptor's store disagrees, because the
	// user may fix raptor's side afterwards.
	tests := []struct {
		name  string
		creds string // raptor credentials body; "" = no file
		flag  string
		want  string
	}{
		{
			name:  "matching host, no warning path",
			creds: "[root]\ncontrol_plane_url = https://root.test\nusername = u\ntoken = t\n",
			flag:  "root",
			want:  "root",
		},
		{
			name: "missing raptor profile still saved",
			flag: "ghost",
			want: "ghost",
		},
		{
			name:  "host mismatch still saved",
			creds: "[other]\ncontrol_plane_url = https://other.test\nusername = u\ntoken = t\n",
			flag:  "other",
			want:  "other",
		},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			isolateHome(t)
			resetLoginFlags(t)
			if tt.creds != "" {
				seedRaptorCreds(t, tt.creds)
			}
			loginRaptorProfile = tt.flag
			if got := resolveRaptorPairing("default", "https://root.test"); got != tt.want {
				t.Errorf("resolveRaptorPairing() = %q, want %q", got, tt.want)
			}
		})
	}
}

func TestResolveRaptorPairing_NoFlagNoProfile(t *testing.T) {
	isolateHome(t)
	resetLoginFlags(t)
	if got := resolveRaptorPairing("default", "https://root.test"); got != "" {
		t.Errorf("resolveRaptorPairing() with nothing stored = %q, want empty", got)
	}
}

func TestLoginFlags_HelpRendersProperTypes(t *testing.T) {
	// pflag treats a backticked phrase inside a usage string as the flag's
	// value placeholder — a stray backtick renders nonsense like
	// `--raptor-profile praxis status` in the help table (cf. issue #66's
	// missing-flag confusion). Every string flag must present as `string`.
	for _, name := range []string{"profile", "url", "token", "raptor-profile"} {
		f := loginCmd.Flags().Lookup(name)
		if f == nil {
			t.Fatalf("flag --%s not registered", name)
		}
		if placeholder, _ := pflag.UnquoteUsage(f); placeholder != "string" {
			t.Errorf("--%s help placeholder = %q, want %q (backtick in usage string?)", name, placeholder, "string")
		}
	}
}
