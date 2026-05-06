package cmd

import (
	"fmt"

	"github.com/Facets-cloud/praxis-cli/internal/harness"
	"github.com/Facets-cloud/praxis-cli/internal/skillinstall"
	"github.com/spf13/cobra"
)

// Package-level seams so tests can stub the harness/install layer
// without touching the real filesystem.
var (
	detectHarnesses    = harness.Detected
	installSkill       = skillinstall.Install
	uninstallSkill     = skillinstall.Uninstall
	listInstalledSkill = skillinstall.List
)

func init() {
	skillCmd.AddCommand(skillInstallCmd)
	skillCmd.AddCommand(skillUninstallCmd)
	skillCmd.AddCommand(skillListInstalledCmd)
	rootCmd.AddCommand(skillCmd)
}

var skillCmd = &cobra.Command{
	Use:   "skill",
	Short: "Install and manage Praxis skills in your AI hosts",
	Long: `A "skill" is a markdown file (SKILL.md) following the Agent Skills
open standard (agentskills.io). Install one and your local Claude Code,
Codex, and Gemini CLI will know how to do that workflow.

v0.1.x ships only one placeholder skill named "praxis" so you can test
the multi-harness install machinery. The real catalog lands in
subsequent CLI releases as the Praxis cloud gateway ships.`,
}

// defaultSkillName is the only skill v0.1 ships. Once the server-driven
// catalog lands, install/uninstall will accept an optional name argument
// to override this default.
const defaultSkillName = "praxis"

var skillInstallCmd = &cobra.Command{
	Use:   "install [name]",
	Short: "Install the praxis skill into every detected AI host",
	Long: `Write the SKILL.md file into the user-scope skill directory of every
detected AI host on this machine:

  Claude Code  →  ~/.claude/skills/praxis/SKILL.md
  Codex        →  ~/.agents/skills/praxis/SKILL.md
  Gemini CLI   →  ~/.gemini/skills/praxis/SKILL.md

Installations are recorded in ~/.praxis/installed.json so list-installed
and uninstall can find them later. Cursor is not included — it has no
user-scope skills directory and needs per-repo install (planned for a
future release).

v0.1.x only ships the placeholder skill named "praxis", so the [name]
argument is optional. Once the server-driven catalog lands, you'll pass
a real skill name here.`,
	Args: cobra.MaximumNArgs(1),
	RunE: func(cmd *cobra.Command, args []string) error {
		out := cmd.OutOrStdout()
		name := defaultSkillName
		if len(args) == 1 {
			name = args[0]
		}

		hosts := detectHarnesses()
		if len(hosts) == 0 {
			fmt.Fprintln(out, "No supported AI hosts detected on this machine.")
			fmt.Fprintln(out, "Install Claude Code, Codex, or Gemini CLI first, then re-run.")
			return nil
		}

		results, err := installSkill(name, hosts)
		if err != nil {
			return err
		}
		for _, in := range results {
			fmt.Fprintf(out, "  ✓ %-12s installed at %s\n", in.Harness, in.Path)
		}
		fmt.Fprintf(out, "\nInstalled %q into %d host(s).\n", name, len(results))
		return nil
	},
}

var skillUninstallCmd = &cobra.Command{
	Use:   "uninstall [name]",
	Short: "Remove the praxis skill from every host where it's installed",
	Args:  cobra.MaximumNArgs(1),
	RunE: func(cmd *cobra.Command, args []string) error {
		out := cmd.OutOrStdout()
		name := defaultSkillName
		if len(args) == 1 {
			name = args[0]
		}
		removed, err := uninstallSkill(name)
		if err != nil {
			return err
		}
		if len(removed) == 0 {
			fmt.Fprintf(out, "No installations of %q found.\n", name)
			return nil
		}
		for _, in := range removed {
			fmt.Fprintf(out, "  ✓ %-12s removed from %s\n", in.Harness, in.Path)
		}
		fmt.Fprintf(out, "\nUninstalled %q from %d host(s).\n", name, len(removed))
		return nil
	},
}

var skillListInstalledCmd = &cobra.Command{
	Use:   "list-installed",
	Short: "Show installed skills and where they live",
	RunE: func(cmd *cobra.Command, args []string) error {
		out := cmd.OutOrStdout()
		entries, err := listInstalledSkill()
		if err != nil {
			return err
		}
		if len(entries) == 0 {
			fmt.Fprintln(out, "No skills installed. Try `praxis skill install praxis`.")
			return nil
		}
		fmt.Fprintf(out, "%-20s  %-12s  PATH\n", "SKILL", "HARNESS")
		fmt.Fprintln(out, "──────────────────────────────────────────────────────────────────────")
		for _, e := range entries {
			fmt.Fprintf(out, "%-20s  %-12s  %s\n", e.SkillName, e.Harness, e.Path)
		}
		return nil
	},
}
