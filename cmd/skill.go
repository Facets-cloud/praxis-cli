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

// skillName is the only skill v0.1 ships. Once the server-driven catalog
// lands, install/uninstall will accept an optional name argument to
// override this.
const skillName = "praxis"

func init() {
	rootCmd.AddCommand(installSkillCmd)
	rootCmd.AddCommand(uninstallSkillCmd)
	rootCmd.AddCommand(listSkillsCmd)
}

var installSkillCmd = &cobra.Command{
	Use:   "install-skill",
	Short: "Install the praxis skill into every detected AI host",
	Long: `Write the praxis SKILL.md (Agent Skills open-standard format) into
the user-scope skill directory of every detected AI host on this machine:

  Claude Code  →  ~/.claude/skills/praxis/SKILL.md
  Codex        →  ~/.agents/skills/praxis/SKILL.md
  Gemini CLI   →  ~/.gemini/skills/praxis/SKILL.md

Installations are recorded in ~/.praxis/installed.json so list-skills
and uninstall-skill can find them later. Cursor is not included — it
has no user-scope skill directory and needs per-repo install (planned
for a future release).

v0.1.x ships only the placeholder skill named "praxis"; no name argument
is needed. The real catalog lands in subsequent releases.`,
	Args: cobra.NoArgs,
	RunE: func(cmd *cobra.Command, args []string) error {
		out := cmd.OutOrStdout()

		hosts := detectHarnesses()
		if len(hosts) == 0 {
			fmt.Fprintln(out, "No supported AI hosts detected on this machine.")
			fmt.Fprintln(out, "Install Claude Code, Codex, or Gemini CLI first, then re-run.")
			return nil
		}

		results, err := installSkill(skillName, hosts)
		if err != nil {
			return err
		}
		for _, in := range results {
			fmt.Fprintf(out, "  ✓ %-12s installed at %s\n", in.Harness, in.Path)
		}
		fmt.Fprintf(out, "\nInstalled %q into %d host(s).\n", skillName, len(results))
		return nil
	},
}

var uninstallSkillCmd = &cobra.Command{
	Use:   "uninstall-skill",
	Short: "Remove the praxis skill from every host where it's installed",
	Args:  cobra.NoArgs,
	RunE: func(cmd *cobra.Command, args []string) error {
		out := cmd.OutOrStdout()
		removed, err := uninstallSkill(skillName)
		if err != nil {
			return err
		}
		if len(removed) == 0 {
			fmt.Fprintf(out, "No installations of %q found.\n", skillName)
			return nil
		}
		for _, in := range removed {
			fmt.Fprintf(out, "  ✓ %-12s removed from %s\n", in.Harness, in.Path)
		}
		fmt.Fprintf(out, "\nUninstalled %q from %d host(s).\n", skillName, len(removed))
		return nil
	},
}

var listSkillsCmd = &cobra.Command{
	Use:   "list-skills",
	Short: "Show installed skills and where they live",
	Args:  cobra.NoArgs,
	RunE: func(cmd *cobra.Command, args []string) error {
		out := cmd.OutOrStdout()
		entries, err := listInstalledSkill()
		if err != nil {
			return err
		}
		if len(entries) == 0 {
			fmt.Fprintln(out, "No skills installed. Try `praxis install-skill`.")
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
