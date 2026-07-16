package cmd

import (
	"encoding/json"
	"errors"
	"strings"
	"testing"
)

func nudgeContext(t *testing.T, out string) string {
	t.Helper()
	var payload struct {
		HookSpecificOutput struct {
			HookEventName     string `json:"hookEventName"`
			AdditionalContext string `json:"additionalContext"`
		} `json:"hookSpecificOutput"`
	}
	if err := json.Unmarshal([]byte(out), &payload); err != nil {
		t.Fatalf("output is not valid hook JSON: %v\n%s", err, out)
	}
	if payload.HookSpecificOutput.HookEventName == "" {
		t.Error("hookEventName missing")
	}
	return payload.HookSpecificOutput.AdditionalContext
}

func claimsReturning(cats []string, err error) claimsFunc {
	return func(string) ([]string, error) { return cats, err }
}

func TestRunIgHookMemberNudgesWithCatalogs(t *testing.T) {
	claims := claimsReturning([]string{"capillary-cloud", "saas-cp"}, nil)
	out, err := runIgHook("SessionStart", "s1", t.TempDir(), "github.com/org/control-plane", claims)
	if err != nil {
		t.Fatalf("runIgHook: %v", err)
	}
	if out == "" {
		t.Fatal("a catalog member must produce a nudge")
	}
	ctx := nudgeContext(t, out)
	for _, sub := range []string{"capillary-cloud", "saas-cp", "use-ig"} {
		if !strings.Contains(ctx, sub) {
			t.Errorf("nudge missing %q: %s", sub, ctx)
		}
	}
}

func TestRunIgHookNonMemberIsSilent(t *testing.T) {
	// A git repo the server does not claim → no nudge.
	out, err := runIgHook("CwdChanged", "s1", t.TempDir(), "github.com/org/random", claimsReturning(nil, nil))
	if err != nil {
		t.Fatal(err)
	}
	if out != "" {
		t.Errorf("non-member repo must be silent: %s", out)
	}
}

func TestRunIgHookNoOriginIsSilentAndSkipsServer(t *testing.T) {
	called := false
	claims := func(string) ([]string, error) { called = true; return []string{"x"}, nil }
	out, err := runIgHook("SessionStart", "s1", t.TempDir(), "", claims)
	if err != nil {
		t.Fatal(err)
	}
	if out != "" {
		t.Errorf("no origin must be silent: %s", out)
	}
	if called {
		t.Error("must not hit the server when cwd is not a git repo")
	}
}

func TestRunIgHookServerErrorIsSilentAndRetryable(t *testing.T) {
	tmp := t.TempDir()
	canon := "github.com/org/control-plane"
	// First call fails (offline): silent, and NOT marked processed.
	out, err := runIgHook("SessionStart", "s1", tmp, canon, claimsReturning(nil, errors.New("offline")))
	if err != nil {
		t.Fatalf("server error must not error the session: %v", err)
	}
	if out != "" {
		t.Errorf("server error must be silent: %s", out)
	}
	// Next cwd change (server back) must still check and nudge — proving the
	// failed call did not mark the repo processed.
	out, _ = runIgHook("CwdChanged", "s1", tmp, canon, claimsReturning([]string{"capillary-cloud"}, nil))
	if out == "" {
		t.Error("a recovered server must still nudge; failed check must be retryable")
	}
}

func TestRunIgHookDedupsPerSession(t *testing.T) {
	tmp := t.TempDir()
	canon := "github.com/org/control-plane"
	calls := 0
	claims := func(string) ([]string, error) { calls++; return []string{"capillary-cloud"}, nil }
	first, _ := runIgHook("SessionStart", "s9", tmp, canon, claims)
	if first == "" {
		t.Fatal("first encounter should nudge")
	}
	second, _ := runIgHook("CwdChanged", "s9", tmp, canon, claims)
	if second != "" {
		t.Errorf("same repo same session must not re-nudge: %s", second)
	}
	if calls != 1 {
		t.Errorf("server must be queried once per repo per session, got %d calls", calls)
	}
}
