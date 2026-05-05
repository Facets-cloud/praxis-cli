package cmd

import (
	"github.com/spf13/cobra"
)

func init() {
	skillInstallCmd.Flags().String("host", "", "target AI host: claude-code|cursor|gemini-cli|all (default: all detected)")
	skillInstallCmd.Flags().String("path", "", "override target install path")
	skillUninstallCmd.Flags().String("host", "", "remove from a specific host (default: all hosts where installed)")

	skillCmd.AddCommand(skillListCmd)
	skillCmd.AddCommand(skillShowCmd)
	skillCmd.AddCommand(skillInstallCmd)
	skillCmd.AddCommand(skillUninstallCmd)
	skillCmd.AddCommand(skillListInstalledCmd)
	skillCmd.AddCommand(skillRefreshCmd)
	rootCmd.AddCommand(skillCmd)
}

var skillCmd = &cobra.Command{
	Use:   "skill",
	Short: "Browse and install Praxis skills into your AI host",
	Long: `Praxis skills are playbooks the server publishes (release-debugging,
k8s-investigation, terraform-plan-explain, …). Install one and your
local Claude/Cursor/Gemini will know how to do that workflow using
praxis MCP commands.`,
}

var skillListCmd = &cobra.Command{
	Use:   "list",
	Short: "List skills available from your Praxis cloud",
	RunE: func(cmd *cobra.Command, args []string) error {
		notImplemented(3, "praxis skill list (server catalog fetch)")
		return nil
	},
}

var skillShowCmd = &cobra.Command{
	Use:   "show <name>",
	Short: "Print a skill's markdown (with praxis CLI nomenclature)",
	Args:  cobra.ExactArgs(1),
	RunE: func(cmd *cobra.Command, args []string) error {
		notImplemented(3, "praxis skill show (server fetch + translation)")
		return nil
	},
}

var skillInstallCmd = &cobra.Command{
	Use:   "install <name>",
	Short: "Install a skill pointer into one or more AI hosts",
	Long: `Write a thin pointer SKILL.md into each detected AI host's skill
directory. The pointer fetches fresh skill content via 'praxis skill show'
on each invocation, so updates are automatic.`,
	Args: cobra.ExactArgs(1),
	RunE: func(cmd *cobra.Command, args []string) error {
		notImplemented(2, "praxis skill install (multi-harness pointer write)")
		return nil
	},
}

var skillUninstallCmd = &cobra.Command{
	Use:   "uninstall <name>",
	Short: "Remove an installed skill from one or all AI hosts",
	Args:  cobra.ExactArgs(1),
	RunE: func(cmd *cobra.Command, args []string) error {
		notImplemented(2, "praxis skill uninstall")
		return nil
	},
}

var skillListInstalledCmd = &cobra.Command{
	Use:   "list-installed",
	Short: "Show what skills you've installed and where",
	RunE: func(cmd *cobra.Command, args []string) error {
		notImplemented(2, "praxis skill list-installed")
		return nil
	},
}

var skillRefreshCmd = &cobra.Command{
	Use:   "refresh",
	Short: "Re-write all installed skill pointers (useful after praxis update)",
	RunE: func(cmd *cobra.Command, args []string) error {
		notImplemented(2, "praxis skill refresh")
		return nil
	},
}
