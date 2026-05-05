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
	Long: `Praxis CLI exposes your organization's Praxis cloud to your local AI tool
(Claude Code, Cursor, Gemini CLI). Skills are sourced and run inside your
AI; MCP tools execute server-side using org-managed credentials.

No AWS/kube/terraform credentials on your laptop.

Common commands:
  praxis login                              connect your Praxis account
  praxis skill install <name>               install a skill into your AI host
  praxis mcp <mcp> <fn> [--arg val ...]     invoke any Praxis MCP function
  praxis doctor                             diagnose your installation

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
