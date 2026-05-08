package cmd

import (
	"fmt"
	"os"

	"github.com/spf13/cobra"
)

// deprecate marks a command as hidden from --help and wraps its RunE
// (or runs an existing PreRun) so invoking it prints a stderr warning
// pointing the user at the v0.7 replacement. The original behavior is
// preserved — these commands keep working through v0.7 so existing
// scripts don't break, then get removed in v0.8.
func deprecate(cmd *cobra.Command, replacement string) {
	cmd.Hidden = true
	prevRunE := cmd.RunE
	prevRun := cmd.Run

	wrapper := func(c *cobra.Command, args []string) error {
		fmt.Fprintf(os.Stderr,
			"warning: `praxis %s` is deprecated in v0.7 and will be removed in v0.8.\n"+
				"         Use `%s` instead.\n",
			cmd.Name(), replacement)
		if prevRunE != nil {
			return prevRunE(c, args)
		}
		if prevRun != nil {
			prevRun(c, args)
		}
		return nil
	}
	cmd.RunE = wrapper
	cmd.Run = nil
}

func init() {
	// Wire up after the deprecated commands' own init() runs. cobra
	// init order is alphabetical by file, and "deprecated.go" comes
	// after the targets we wrap (init.go, install_skill.go, etc.) —
	// good enough. If a command is added later that we need to
	// deprecate, mirror the pattern below.
	deprecate(initCmd, "praxis login")
	deprecate(installSkillCmd, "praxis login")
	deprecate(uninstallSkillCmd, "praxis logout")
	deprecate(whoamiCmd, "praxis status --refresh")
	deprecate(useCmd, "praxis login --profile <name>")
	deprecate(listSkillsCmd, "praxis status")
	// refresh-skills is NOT deprecated in v0.7 — it stays as a
	// first-class command (auth-skipping refresh path).
}
