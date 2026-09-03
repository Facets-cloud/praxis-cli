package cmd

import (
	"context"
	"encoding/json"
	"os"
	"path/filepath"
	"strings"
	"testing"

	"github.com/Facets-cloud/praxis-cli/internal/credentials"
	"github.com/Facets-cloud/praxis-cli/internal/exitcode"
)

// runDryRunJSON drives `praxis login --dry-run --json` end to end through
// RunE and decodes the report.
func runDryRunJSON(t *testing.T) map[string]any {
	t.Helper()
	loginDryRun, loginJSON = true, true
	out, err := runLoginRunE(t)
	if err != nil {
		t.Fatalf("dry-run login err: %v", err)
	}
	var report map[string]any
	if jerr := json.Unmarshal([]byte(out), &report); jerr != nil {
		t.Fatalf("dry-run output not JSON: %v\n%s", jerr, out)
	}
	return report
}

func TestLoginDryRun_StoredValidToken_ReportsReuse(t *testing.T) {
	isolateHome(t)
	resetLoginFlags(t)
	t.Cleanup(func() { loginDryRun = false })
	seedProfile(t, "default", "https://stored.test", "tok")
	browser := stubBrowserLogin(t)
	setup := stubPostAuth(t)
	stubAuthMe(t, func(_ string, _ map[string]string) (*authMeResponse, error) {
		return &authMeResponse{Email: "u@x"}, nil
	})

	report := runDryRunJSON(t)
	for k, want := range map[string]any{
		"ok":           true,
		"dry_run":      true,
		"profile":      "default",
		"reachable":    true,
		"token_status": "stored-valid",
		"action":       "reuse-token (no browser)",
	} {
		if got := report[k]; got != want {
			t.Errorf("report[%q] = %v, want %v", k, got, want)
		}
	}
	if *browser || *setup {
		t.Error("dry-run must not run the browser flow or post-auth setup")
	}
}

func TestLoginDryRun_HasNoSideEffects(t *testing.T) {
	isolateHome(t)
	resetLoginFlags(t)
	t.Cleanup(func() { loginDryRun = false })
	seedProfile(t, "default", "https://stored.test", "tok")
	stubBrowserLogin(t)
	stubPostAuth(t)
	stubAuthMe(t, func(_ string, _ map[string]string) (*authMeResponse, error) {
		return &authMeResponse{Email: "u@x"}, nil
	})

	credsPath := filepath.Join(os.Getenv("HOME"), ".praxis", "credentials")
	before, err := os.ReadFile(credsPath)
	if err != nil {
		t.Fatal(err)
	}

	// Aim at a DIFFERENT profile+URL — the most side-effect-prone shape.
	rootProfile, loginURL = "probe", "https://probe.test"
	runDryRunJSON(t)

	after, err := os.ReadFile(credsPath)
	if err != nil {
		t.Fatal(err)
	}
	if string(before) != string(after) {
		t.Errorf("dry-run modified the credentials file:\nbefore: %s\nafter: %s", before, after)
	}
	if _, err := os.Stat(filepath.Join(os.Getenv("HOME"), ".praxis", "config.json")); !os.IsNotExist(err) {
		t.Error("dry-run wrote the active-profile pointer")
	}
}

func TestLoginDryRun_TokenAndReachabilityMatrix(t *testing.T) {
	tests := []struct {
		name        string
		seedToken   string // stored token for [default] at https://stored.test; "" = none
		suppliedTok string // --token value
		force       bool
		authErr     error // fetchAuthMe result (nil = 200)
		wantStatus  string
		wantAction  string
		wantOK      bool
		wantExit    int // expected osExit code; -1 = not called
	}{
		{
			name: "no token, reachable server (401 on empty probe)", authErr: errTokenRejected,
			wantStatus: "none", wantAction: "browser", wantOK: true, wantExit: -1,
		},
		{
			name: "stored token rejected falls back to browser", seedToken: "dead", authErr: errTokenRejected,
			wantStatus: "stored-invalid", wantAction: "browser", wantOK: true, wantExit: -1,
		},
		{
			name: "stored token valid with --force still browsers", seedToken: "tok", force: true,
			wantStatus: "stored-valid", wantAction: "browser (--force)", wantOK: true, wantExit: -1,
		},
		{
			name: "supplied token valid", suppliedTok: "sk_new",
			wantStatus: "supplied-valid", wantAction: "save-token (no browser)", wantOK: true, wantExit: -1,
		},
		{
			name: "supplied token rejected", suppliedTok: "sk_bad", authErr: errTokenRejected,
			wantStatus: "supplied-invalid", wantAction: "fail (supplied token rejected)", wantOK: true, wantExit: -1,
		},
		{
			name: "unreachable server", seedToken: "tok", authErr: context.DeadlineExceeded,
			wantStatus: "stored-unverified", wantAction: "unknown (server unreachable)", wantOK: false,
			wantExit: exitcode.Network,
		},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			isolateHome(t)
			resetLoginFlags(t)
			t.Cleanup(func() { loginDryRun = false })
			if tt.seedToken != "" {
				seedProfile(t, "default", "https://stored.test", tt.seedToken)
			} else {
				seedProfile(t, "default", "https://stored.test", "")
			}
			stubBrowserLogin(t)
			stubPostAuth(t)
			exit := stubOsExit(t)
			stubAuthMe(t, func(_ string, _ map[string]string) (*authMeResponse, error) {
				if tt.authErr != nil {
					return nil, tt.authErr
				}
				return &authMeResponse{Email: "u@x"}, nil
			})
			loginToken = tt.suppliedTok
			loginForce = tt.force

			report := runDryRunJSON(t)
			if got := report["token_status"]; got != tt.wantStatus {
				t.Errorf("token_status = %v, want %v", got, tt.wantStatus)
			}
			if got := report["action"]; got != tt.wantAction {
				t.Errorf("action = %v, want %v", got, tt.wantAction)
			}
			if got := report["ok"]; got != tt.wantOK {
				t.Errorf("ok = %v, want %v", got, tt.wantOK)
			}
			if *exit != tt.wantExit {
				t.Errorf("osExit code = %d, want %d", *exit, tt.wantExit)
			}
		})
	}
}

func TestLoginDryRun_ProfileSwitchSkillsEffect(t *testing.T) {
	isolateHome(t)
	resetLoginFlags(t)
	t.Cleanup(func() { loginDryRun = false })
	seedProfile(t, "default", "https://stored.test", "tok")
	seedProfile(t, "acme", "https://acme.test", "tok2")
	stubBrowserLogin(t)
	stubPostAuth(t)
	stubAuthMe(t, func(_ string, _ map[string]string) (*authMeResponse, error) {
		return &authMeResponse{Email: "u@x"}, nil
	})

	rootProfile = "acme"
	report := runDryRunJSON(t)
	effect, _ := report["skills_effect"].(string)
	if effect == "" || report["active_profile"] != "default" {
		t.Fatalf("unexpected report: %v", report)
	}
	for _, want := range []string{`"default"`, `"acme"`, "wiped"} {
		if !strings.Contains(effect, want) {
			t.Errorf("skills_effect %q missing %q", effect, want)
		}
	}
}

// "Whose skills are on disk" is a question about POINTERS. $PRAXIS_PROFILE
// routes commands; it installs nothing. Resolving the dry run's baseline
// through it meant `PRAXIS_PROFILE=acme praxis login --profile acme --dry-run`
// reported "no profile switch" for a login that was about to move the pointer
// off default and wipe default's org skills — the exact prediction --dry-run
// exists to make, inverted, on the safe-probe path.
func TestLoginDryRun_EnvDoesNotHideAPendingProfileSwitch(t *testing.T) {
	isolateHome(t)
	resetLoginFlags(t)
	t.Cleanup(func() { loginDryRun = false })
	seedProfile(t, "default", "https://stored.test", "tok")
	seedProfile(t, "acme", "https://acme.test", "tok2")
	stubBrowserLogin(t)
	stubPostAuth(t)
	stubAuthMe(t, func(_ string, _ map[string]string) (*authMeResponse, error) {
		return &authMeResponse{Email: "u@x"}, nil
	})

	// The session is scoped to acme, but the pointer — and the installed
	// catalog — still belong to default.
	t.Setenv(credentials.EnvProfile, "acme")
	rootProfile = "acme"

	report := runDryRunJSON(t)
	if report["active_profile"] != "default" {
		t.Fatalf("active_profile = %v, want default — the env var owns no skills", report["active_profile"])
	}
	effect, _ := report["skills_effect"].(string)
	if strings.Contains(effect, "no profile switch") {
		t.Errorf("skills_effect %q claims no switch; login will move the pointer and wipe default's skills", effect)
	}
	for _, want := range []string{`"default"`, `"acme"`, "wiped"} {
		if !strings.Contains(effect, want) {
			t.Errorf("skills_effect %q missing %q", effect, want)
		}
	}
}

// The mirror case: with [default] already a copy of acme, an env var naming
// acme changes nothing, and the report must not invent a switch either.
func TestLoginDryRun_EnvMatchingThePointerReportsNoSwitch(t *testing.T) {
	isolateHome(t)
	resetLoginFlags(t)
	t.Cleanup(func() { loginDryRun = false })
	seedProfile(t, "default", "https://stored.test", "tok")
	seedProfile(t, "acme", "https://acme.test", "tok2")
	if _, err := credentials.SetDefault("acme"); err != nil {
		t.Fatal(err)
	}
	stubBrowserLogin(t)
	stubPostAuth(t)
	stubAuthMe(t, func(_ string, _ map[string]string) (*authMeResponse, error) {
		return &authMeResponse{Email: "u@x"}, nil
	})

	t.Setenv(credentials.EnvProfile, "acme")
	rootProfile = "acme"

	report := runDryRunJSON(t)
	if report["active_profile"] != "default" {
		t.Fatalf("active_profile = %v, want default (a copy of acme)", report["active_profile"])
	}
	effect, _ := report["skills_effect"].(string)
	if !strings.Contains(effect, "no profile switch") {
		t.Errorf("skills_effect %q should report no switch — default already holds acme's credentials", effect)
	}
}
