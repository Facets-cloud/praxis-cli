package agent

import (
	"errors"
	"testing"
)

func TestEnabledDefaultOff(t *testing.T) {
	t.Setenv(experimentalEnvVar, "")
	if Enabled() {
		t.Fatal("Enabled() = true, want false when PRAXIS_EXPERIMENTAL is unset")
	}
}

func TestEnabledEnvVar(t *testing.T) {
	t.Setenv(experimentalEnvVar, "1")
	if !Enabled() {
		t.Fatal("Enabled() = false, want true when PRAXIS_EXPERIMENTAL=1")
	}
}

func TestEnabledEnvVarTrue(t *testing.T) {
	t.Setenv(experimentalEnvVar, "true")
	if !Enabled() {
		t.Fatal("Enabled() = false, want true when PRAXIS_EXPERIMENTAL=true")
	}
}

func TestCheckEnabledReturnsErrorWhenOff(t *testing.T) {
	t.Setenv(experimentalEnvVar, "")
	err := CheckEnabled()
	if err == nil {
		t.Fatal("CheckEnabled() = nil, want error when experimental is off")
	}
	if !errors.Is(err, ErrExperimentalDisabled) {
		t.Fatalf("CheckEnabled() err = %v, want ErrExperimentalDisabled", err)
	}
}

func TestCheckEnabledReturnsNilWhenOn(t *testing.T) {
	t.Setenv(experimentalEnvVar, "1")
	err := CheckEnabled()
	if err != nil {
		t.Fatalf("CheckEnabled() = %v, want nil when experimental is on", err)
	}
}

func TestChatOptsToArgs(t *testing.T) {
	opts := ChatOptions{
		Model:          "opus",
		Thinking:       "high",
		PermissionMode: "auto",
		Cwd:            ".",
		SafeMode:       true,
		MaxTurns:       10,
	}
	args := chatOptsToArgs(opts)

	want := []string{"-model", "opus", "-thinking", "high", "-permission-mode", "auto", "-cwd", ".", "-safe", "-max-turns", "10"}
	if len(args) != len(want) {
		t.Fatalf("chatOptsToArgs returned %v (len %d), want %v (len %d)", args, len(args), want, len(want))
	}
	for i := range want {
		if args[i] != want[i] {
			t.Fatalf("args[%d] = %q, want %q (full args: %v)", i, args[i], want[i], args)
		}
	}
}

func TestChatOptsToArgsEmpty(t *testing.T) {
	args := chatOptsToArgs(ChatOptions{})
	if len(args) != 0 {
		t.Fatalf("chatOptsToArgs(empty) = %v, want empty slice", args)
	}
}

func TestHeadlessArgsToNativeArgs(t *testing.T) {
	ha := HeadlessArgs{
		Prompt:   "hello",
		Model:    "opus",
		Cwd:      ".",
		NoMCP:    true,
		MaxTurns: 5,
	}
	args := ha.ToNativeArgs()

	// Verify each flag AND its value (not just presence).
	type kv struct{ flag, val string }
	want := []kv{
		{"-prompt", "hello"},
		{"-model", "opus"},
		{"-cwd", "."},
		{"-max-turns", "5"},
	}
	for _, w := range want {
		found := false
		for i, a := range args {
			if a == w.flag && i+1 < len(args) && args[i+1] == w.val {
				found = true
				break
			}
		}
		if !found {
			t.Errorf("ToNativeArgs missing %s %s, got %v", w.flag, w.val, args)
		}
	}
	// Boolean flags: verify -no-mcp is present (no value).
	hasNoMCP := false
	for _, a := range args {
		if a == "-no-mcp" {
			hasNoMCP = true
		}
	}
	if !hasNoMCP {
		t.Errorf("ToNativeArgs missing -no-mcp, got %v", args)
	}
}

func TestEnable(t *testing.T) {
	t.Setenv(experimentalEnvVar, "")
	Enable()
	if !Enabled() {
		t.Fatal("Enable() did not set PRAXIS_EXPERIMENTAL")
	}
}

func TestLogoNotEmpty(t *testing.T) {
	logo := Logo()
	if logo == "" {
		t.Fatal("Logo() returned empty string")
	}
}
