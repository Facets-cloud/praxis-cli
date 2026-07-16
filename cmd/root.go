// Package cmd is the cobra command tree for the praxis CLI.
package cmd

import (
	"fmt"
	"os"
	"time"

	"github.com/Facets-cloud/praxis-cli/internal/render"
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
	// First-run: land the pre-login GTM skill into the AI host so a freshly
	// installed praxis is discoverable before any login. Marker-gated (one
	// stat() after the first time) and skipped for machine-invoked commands;
	// never blocks the command it precedes.
	maybeFirstRunBootstrap(os.Args[1:])
	// Fire a background check for a newer release, but only for an interactive
	// human (stderr is a TTY). When praxis is spawned by an AI host or a script,
	// stderr is piped — we skip entirely so the check never delays automation
	// and never adds stderr noise to a parsed invocation. Also suppressed for
	// version/update/completion and dev builds (see checkForUpdate).
	//
	// The notice prints after the command finishes. The select returns the
	// instant the result is ready, so the warm-cache path doesn't wait; only a
	// cold network fetch waits, bounded by updateCheckMaxWait.
	var notify func()
	if render.IsTTY(os.Stderr) && !skipUpdateCheck(os.Args[1:]) {
		ch := make(chan []staleNag, 1)
		go func() { ch <- collectStaleNags() }()
		notify = func() {
			select {
			case nags := <-ch:
				for _, n := range nags {
					printFreshnessBox(n.Freshness, n.Action, os.Stderr)
				}
			case <-time.After(updateCheckMaxWait):
				// Cold fetch still in flight — skip the notice for this run.
				// The goroutine keeps running long enough to refresh the cache
				// in the common case, so the notice surfaces next invocation.
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
