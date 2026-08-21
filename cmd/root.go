// Package cmd is the cobra command tree for the praxis CLI.
package cmd

import (
	"fmt"
	"io"
	"os"
	"time"

	"github.com/Facets-cloud/praxis-cli/internal/credentials"
	"github.com/Facets-cloud/praxis-cli/internal/exitcode"
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

// rootProfile is the global --profile flag: the credentials profile this one
// invocation should use. It sits at the top of the resolution chain in
// credentials.resolveName (flag → project pointer → global pointer →
// "default"), so it overrides a local-mode tree without touching any pointer
// on disk — nothing is persisted and the next invocation resolves normally.
// Empty means "resolve normally". Commands consume it via activeOrAuthExit or
// by passing it to credentials.ResolveActive.
var rootProfile string

func init() {
	// Persistent, so it works in both positions an AI host might emit it:
	// `praxis --profile acme mcp ...` and `praxis mcp ... --profile acme`.
	rootCmd.PersistentFlags().StringVarP(&rootProfile, "profile", "p", "",
		"credentials profile to use for this invocation (overrides $"+credentials.EnvProfile+"; default: the active profile)")
}

// explicitProfile returns the profile this invocation explicitly selected and
// the mechanism that named it, or ("", "") when neither is present. The flag
// wins over the environment, matching credentials.resolveName's chain.
func explicitProfile() (string, string) {
	if rootProfile != "" {
		return rootProfile, "--profile"
	}
	if name := credentials.EnvProfileName(); name != "" {
		return name, credentials.EnvProfile
	}
	return "", ""
}

// refusedProfileFlag refuses an explicit -p/--profile only, leaving
// $PRAXIS_PROFILE alone. Use it where the environment is a legitimate ambient
// session setting but the flag would be ambiguous: `profiles use` names its
// target positionally, so a flag beside it is two answers to one question,
// whereas an exported variable is just the session the user is working in.
func refusedProfileFlag(out io.Writer, asJSON bool, what, hintFmt string) bool {
	if rootProfile == "" {
		return false
	}
	return refuseSelection(out, asJSON, what, hintFmt, rootProfile, "--profile")
}

// refusedExplicitProfile refuses BOTH -p/--profile and $PRAXIS_PROFILE.
//
// Use it where the command rewrites state belonging to the ACTIVE profile:
// `logout` deletes credentials and wipes org skills, `refresh-skills`
// reinstalls them. Acting on any other profile would leave the pointer and the
// skills on disk out of step. Ignoring the selection is the dangerous option,
// not the polite one — `praxis --profile acme logout` would delete the ACTIVE
// profile's credentials while the user believed they had named acme.
func refusedExplicitProfile(out io.Writer, asJSON bool, what, hintFmt string) bool {
	name, how := explicitProfile()
	if name == "" {
		return false
	}
	return refuseSelection(out, asJSON, what, hintFmt, name, how)
}

// refuseSelection prints the usage error and exits. hintFmt takes one %s: the
// selected profile name.
func refuseSelection(out io.Writer, asJSON bool, what, hintFmt, name, how string) bool {
	hint := fmt.Sprintf(hintFmt, name)
	if how == credentials.EnvProfile {
		hint = "unset " + credentials.EnvProfile + " for this command, or " + hint
	}
	render.PrintError(out, asJSON,
		fmt.Sprintf("%s can't be pointed at another profile (%s=%s)", what, how, name),
		hint, exitcode.Usage)
	osExit(exitcode.Usage)
	return true
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
