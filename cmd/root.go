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
tool (Claude Code, Cursor, Gemini CLI). Skills will be sourced and run
inside your AI; MCP tools will execute server-side using org-managed
credentials. No AWS/kube/terraform credentials on your laptop.

This is an early release. Today the CLI only ships installation,
versioning, and self-update plumbing. Skill sourcing and MCP invocation
land in subsequent releases.

Run 'praxis <command> --help' for details on any command.`,
	SilenceUsage:  true,
	SilenceErrors: true,
	Version:       version,
}

// Execute runs the root command. Called from main.
func Execute() {
	if err := rootCmd.Execute(); err != nil {
		fmt.Fprintln(os.Stderr, "Error:", err)
		os.Exit(1)
	}
}
