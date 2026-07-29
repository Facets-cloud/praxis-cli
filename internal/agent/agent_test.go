package agent

import (
	"os"
	"testing"
)

func TestEnabledDefaultOff(t *testing.T) {
	old := os.Getenv(experimentalEnvVar)
	os.Unsetenv(experimentalEnvVar)
	defer os.Setenv(experimentalEnvVar, old)

	if Enabled() {
		t.Fatal("Enabled() = true, want false when PRAXIS_EXPERIMENTAL is unset")
	}
}

func TestEnabledEnvVar(t *testing.T) {
	old := os.Getenv(experimentalEnvVar)
	os.Setenv(experimentalEnvVar, "1")
	defer os.Setenv(experimentalEnvVar, old)

	if !Enabled() {
		t.Fatal("Enabled() = false, want true when PRAXIS_EXPERIMENTAL=1")
	}
}

func TestEnabledEnvVarTrue(t *testing.T) {
	old := os.Getenv(experimentalEnvVar)
	os.Setenv(experimentalEnvVar, "true")
	defer os.Setenv(experimentalEnvVar, old)

	if !Enabled() {
		t.Fatal("Enabled() = false, want true when PRAXIS_EXPERIMENTAL=true")
	}
}

func TestCheckEnabledReturnsErrorWhenOff(t *testing.T) {
	old := os.Getenv(experimentalEnvVar)
	os.Unsetenv(experimentalEnvVar)
	defer os.Setenv(experimentalEnvVar, old)

	err := CheckEnabled()
	if err == nil {
		t.Fatal("CheckEnabled() = nil, want error when experimental is off")
	}
}

func TestCheckEnabledReturnsNilWhenOn(t *testing.T) {
	old := os.Getenv(experimentalEnvVar)
	os.Setenv(experimentalEnvVar, "1")
	defer os.Setenv(experimentalEnvVar, old)

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

	// Check key flags are present
	hasFlag := func(flag string) bool {
		for _, a := range args {
			if a == flag {
				return true
			}
		}
		return false
	}

	if !hasFlag("-prompt") {
		t.Errorf("ToNativeArgs missing -prompt, got %v", args)
	}
	if !hasFlag("-model") {
		t.Errorf("ToNativeArgs missing -model, got %v", args)
	}
	if !hasFlag("-cwd") {
		t.Errorf("ToNativeArgs missing -cwd, got %v", args)
	}
	if !hasFlag("-no-mcp") {
		t.Errorf("ToNativeArgs missing -no-mcp, got %v", args)
	}
	if !hasFlag("-max-turns") {
		t.Errorf("ToNativeArgs missing -max-turns, got %v", args)
	}
}

func TestEnable(t *testing.T) {
	old := os.Getenv(experimentalEnvVar)
	os.Unsetenv(experimentalEnvVar)
	defer os.Setenv(experimentalEnvVar, old)

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
