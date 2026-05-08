// Package cmd is the cobra command tree for the praxis CLI.
package cmd

import (
	"fmt"
	"os"

	"github.com/spf13/cobra"
)

// version metadata is injected at build time via -ldflags. See Makefile.
var (
	version = "dev"
	commit  = "none"
	date    = "unknown"
)

var rootCmd = &cobra.Command{
	Use:   "praxis",
	Short: "Bring Praxis cloud capabilities to any local AI host",
	Long: `Praxis CLI exposes your organization's Praxis cloud to your local AI
tool (Claude Code, Cursor, Gemini CLI). Skills are sourced and run
inside your AI; MCP tools execute server-side using org-managed
credentials. No AWS/kube/terraform credentials on your laptop.

Run 'praxis <command> --help' for details on any command.`,
	SilenceUsage:     true,
	SilenceErrors:    true,
	Version:          version,
	PersistentPreRun: warnDeprecatedEnvVars,
}

// warnDeprecatedEnvVars prints stderr warnings for legacy environment
// variables that are deprecated in v0.7. The variables continue to work
// for one minor version so existing scripts have time to migrate; they
// will be removed in v0.8.
func warnDeprecatedEnvVars(cmd *cobra.Command, args []string) {
	if v := os.Getenv("PRAXIS_PROFILE"); v != "" {
		fmt.Fprintf(os.Stderr,
			"warning: PRAXIS_PROFILE env var is deprecated and will be ignored in v0.8.\n"+
				"         Use `praxis login --profile %s` instead — login is the only way\n"+
				"         to switch the active profile in v0.7+ (it also refreshes skills).\n",
			v)
	}
}

// Execute runs the root command. Called from main.
func Execute() {
	if err := rootCmd.Execute(); err != nil {
		fmt.Fprintln(os.Stderr, "Error:", err)
		os.Exit(1)
	}
}
