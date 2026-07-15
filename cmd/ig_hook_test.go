package cmd

import (
	"encoding/json"
	"os"
	"path/filepath"
	"strings"
	"testing"
)

func writeCheckouts(t *testing.T, body string) string {
	t.Helper()
	p := filepath.Join(t.TempDir(), "ig-checkouts.json")
	if err := os.WriteFile(p, []byte(body), 0o644); err != nil {
		t.Fatal(err)
	}
	return p
}

const cpMemory = `{
  "https://github.com/org/control-plane.git": {
    "path": "/Users/me/src/control-plane",
    "member": "control-plane",
    "catalogs": ["capillary-cloud", "saas-cp"]
  }
}`

func TestRunIgHookMatchEmitsNudge(t *testing.T) {
	mem := writeCheckouts(t, cpMemory)
	tmp := t.TempDir()
	out, err := runIgHook("SessionStart", "sess-1", mem, tmp, "git@github.com:org/control-plane.git")
	if err != nil {
		t.Fatalf("runIgHook: %v", err)
	}
	if out == "" {
		t.Fatal("expected a nudge for a remembered checkout, got silence")
	}
	var payload struct {
		HookSpecificOutput struct {
			HookEventName     string `json:"hookEventName"`
			AdditionalContext string `json:"additionalContext"`
		} `json:"hookSpecificOutput"`
	}
	if err := json.Unmarshal([]byte(out), &payload); err != nil {
		t.Fatalf("output is not valid hook JSON: %v\n%s", err, out)
	}
	if payload.HookSpecificOutput.HookEventName != "SessionStart" {
		t.Errorf("hookEventName = %q, want SessionStart", payload.HookSpecificOutput.HookEventName)
	}
	for _, sub := range []string{"control-plane", "capillary-cloud", "use-ig"} {
		if !strings.Contains(payload.HookSpecificOutput.AdditionalContext, sub) {
			t.Errorf("additionalContext missing %q: %s", sub, payload.HookSpecificOutput.AdditionalContext)
		}
	}
}

func TestRunIgHookNoMatchIsSilent(t *testing.T) {
	mem := writeCheckouts(t, cpMemory)
	out, err := runIgHook("CwdChanged", "sess-1", mem, t.TempDir(), "https://github.com/org/unrelated.git")
	if err != nil {
		t.Fatal(err)
	}
	if out != "" {
		t.Errorf("unremembered repo must be silent, got: %s", out)
	}
}

func TestRunIgHookNoOriginIsSilent(t *testing.T) {
	mem := writeCheckouts(t, cpMemory)
	out, err := runIgHook("SessionStart", "sess-1", mem, t.TempDir(), "")
	if err != nil {
		t.Fatal(err)
	}
	if out != "" {
		t.Errorf("empty origin (not a git repo) must be silent, got: %s", out)
	}
}

func TestRunIgHookMissingMemoryIsSilent(t *testing.T) {
	out, err := runIgHook("SessionStart", "sess-1", filepath.Join(t.TempDir(), "none.json"), t.TempDir(), "git@github.com:org/control-plane.git")
	if err != nil {
		t.Fatal(err)
	}
	if out != "" {
		t.Errorf("no memory file must be silent, got: %s", out)
	}
}

func TestRunIgHookMalformedMemoryIsSilent(t *testing.T) {
	mem := writeCheckouts(t, `{ broken`)
	out, err := runIgHook("SessionStart", "sess-1", mem, t.TempDir(), "git@github.com:org/control-plane.git")
	if err != nil {
		t.Fatalf("malformed memory must not error the session: %v", err)
	}
	if out != "" {
		t.Errorf("malformed memory must be silent, got: %s", out)
	}
}

func TestRunIgHookDedupsPerSession(t *testing.T) {
	mem := writeCheckouts(t, cpMemory)
	tmp := t.TempDir()
	origin := "git@github.com:org/control-plane.git"
	first, _ := runIgHook("SessionStart", "sess-9", mem, tmp, origin)
	if first == "" {
		t.Fatal("first nudge should fire")
	}
	second, _ := runIgHook("CwdChanged", "sess-9", mem, tmp, origin)
	if second != "" {
		t.Errorf("same checkout in same session must not re-nudge, got: %s", second)
	}
}
