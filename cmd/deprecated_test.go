package cmd

import (
	"testing"

	"github.com/spf13/cobra"
)

// TestDeprecatedCommands_Hidden pins the v0.7 surface contract: every
// command we deprecated must (a) still exist on the command tree, so
// existing scripts keep working, and (b) be marked Hidden so it doesn't
// clutter --help.
func TestDeprecatedCommands_Hidden(t *testing.T) {
	deprecated := map[string]string{
		"init":            "praxis login",
		"install-skill":   "praxis login",
		"uninstall-skill": "praxis logout",
		"whoami":          "praxis status --refresh",
		"use":             "praxis login --profile",
		"list-skills":     "praxis status",
	}
	for name := range deprecated {
		cmd, _, err := rootCmd.Find([]string{name})
		if err != nil {
			t.Errorf("deprecated command %q must still exist on tree: %v", name, err)
			continue
		}
		if !cmd.Hidden {
			t.Errorf("command %q must be Hidden=true in v0.7", name)
		}
	}
}

// TestVisibleCommands_NotHidden is the dual contract — the v0.7 ship
// surface must NOT be marked Hidden by accident.
func TestVisibleCommands_NotHidden(t *testing.T) {
	visible := []string{
		"login", "logout", "status", "mcp", "refresh-skills",
		"update", "version", "completion",
	}
	for _, name := range visible {
		cmd, _, err := rootCmd.Find([]string{name})
		if err != nil {
			t.Errorf("v0.7 surface command %q missing: %v", name, err)
			continue
		}
		if cmd.Hidden {
			t.Errorf("v0.7 surface command %q must not be Hidden", name)
		}
	}
}

// TestDeprecatedCommandsCallable verifies the wrapped RunE chain still
// invokes the original handler. We only sanity-check that calling RunE
// doesn't return an unexpected error for a no-op friendly command;
// the deeper behavioral tests live next to each command.
func TestDeprecatedCommandsCallable(t *testing.T) {
	cmd, _, err := rootCmd.Find([]string{"list-skills"})
	if err != nil {
		t.Fatal(err)
	}
	// list-skills with empty receipt is the safest cross-shell call —
	// it doesn't touch credentials, doesn't network, just reads the
	// receipt and prints. Should still work after the deprecate wrapper.
	t.Setenv("HOME", t.TempDir())
	if cmd.RunE == nil {
		t.Fatal("list-skills RunE should be set after deprecate()")
	}
	if err := cmd.RunE(cmd, []string{}); err != nil {
		t.Errorf("deprecated list-skills returned error: %v", err)
	}
	// Sanity: the cobra.Command type still has a Run field, but the
	// deprecate() wrapper should have nilled it out so the wrapper's
	// RunE is the only execution path.
	_ = cobra.Command{}
	if cmd.Run != nil {
		t.Error("deprecate() should nil out the original Run to prevent double-invocation")
	}
}
