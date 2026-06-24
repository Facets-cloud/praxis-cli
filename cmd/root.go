// Package cmd is the cobra command tree for the praxis CLI.
package cmd

import (
	"fmt"
	"os"
	"time"

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
	SilenceUsage:  true,
	SilenceErrors: true,
	Version:       version,
}

// Execute runs the root command. Called from main.
func Execute() {
	// Fire a non-blocking background check for a newer release. The notification
	// (if any) is printed after the command finishes, via a deferred select that
	// gives up after updateCheckMaxWait so it never delays the user. Suppressed
	// for version/update/completion and dev builds (see checkForUpdate).
	var notify func()
	if !skipUpdateCheck(os.Args[1:]) {
		ch := make(chan string, 1)
		go func() { ch <- checkForUpdate() }()
		notify = func() {
			select {
			case latest := <-ch:
				if latest != "" {
					printUpdateNotification(latest, os.Stderr)
				}
			case <-time.After(updateCheckMaxWait):
				// Background check didn't finish in time — skip for this run.
			}
		}
	}

	err := rootCmd.Execute()
	// Run the notification before any os.Exit so an error path still nags
	// (the deferred-then-Exit ordering is handled explicitly here).
	if notify != nil {
		notify()
	}
	if err != nil {
		fmt.Fprintln(os.Stderr, "Error:", err)
		os.Exit(1)
	}
}
