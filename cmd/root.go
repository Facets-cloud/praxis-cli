// Package cmd is the cobra command tree for the praxis CLI.
package cmd

import (
	"fmt"
	"io"
	"os"
	"strings"
	"time"

	"github.com/Facets-cloud/praxis-cli/internal/clifeed"
	"github.com/Facets-cloud/praxis-cli/internal/credentials"
	"github.com/Facets-cloud/praxis-cli/internal/exitcode"
	"github.com/Facets-cloud/praxis-cli/internal/httpclient"
	"github.com/Facets-cloud/praxis-cli/internal/paths"
	"github.com/Facets-cloud/praxis-cli/internal/render"
	"github.com/Facets-cloud/praxis-cli/internal/selfupdate"
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
tool (Claude Code, Codex, Gemini CLI, Antigravity). Skills are sourced and run
inside your AI; MCP tools execute server-side using org-managed
credentials. No AWS/kube/terraform credentials on your laptop.

Run 'praxis <command> --help' for details on any command.`,
	SilenceUsage:  true,
	SilenceErrors: true,
	Version:       version,
}

// rootProfile is the global --profile flag: the credentials profile this one
// invocation should use. It sits at the top of the resolution chain in
// credentials.resolveName (flag → $PRAXIS_PROFILE → $FACETS_PROFILE → [default]
// → sole section), so it overrides a local-mode tree without touching anything
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
		return name, credentials.EnvProfileVar()
	}
	return "", ""
}

// refusedProfileFlag refuses a diverging -p/--profile only, leaving
// $PRAXIS_PROFILE alone. Use it where the environment is a legitimate ambient
// session setting but the flag would be ambiguous: `profiles use` names its
// target positionally, so a flag naming a DIFFERENT profile beside it is two
// answers to one question, whereas an exported variable is just the session the
// user is working in.
func refusedProfileFlag(out io.Writer, asJSON bool, what, hintFmt, acts string) bool {
	if rootProfile == "" || rootProfile == acts {
		return false
	}
	return refuseSelection(out, asJSON, what, hintFmt, rootProfile, "--profile", acts)
}

// refusedExplicitProfile refuses BOTH -p/--profile and $PRAXIS_PROFILE when
// they diverge from `acts`.
//
// Use it where the command rewrites state belonging to the ACTIVE profile:
// `logout` deletes credentials and wipes org skills, `refresh-skills`
// reinstalls them. Acting on any other profile would leave [default] and the
// skills on disk out of step. Ignoring a diverging selection is the dangerous
// option, not the polite one — `praxis --profile acme logout` would delete the
// ACTIVE profile's credentials while the user believed they had named acme.
//
// `acts` must be resolved WITHOUT the flag and the environment (see
// credentials.OnDiskActiveName), or the comparison is
// against the selection itself and always matches. The CALLER must then act on
// that same name — a guard checked against the store while the action
// re-resolves the full chain is two different answers to "which profile?", and
// the action's one wins.
//
// Both mechanisms are checked INDEPENDENTLY, not collapsed the way
// explicitProfile / resolveName collapse them. A guard that only inspects the
// winner is satisfied by `-p <active>` while $PRAXIS_PROFILE still names
// something else, so the loser reaches the action anyway: `praxis -p default
// logout` under PRAXIS_PROFILE=acme passed the check and deleted acme — the one
// profile the user had not named.
func refusedExplicitProfile(out io.Writer, asJSON bool, what, hintFmt, acts string) bool {
	// A selection whose credentials equal the active section is the same
	// deployment under another name — [default] after `profiles use X` — and
	// is not a redirect.
	if rootProfile != "" && !credentials.SameCreds(rootProfile, acts) {
		return refuseSelection(out, asJSON, what, hintFmt, rootProfile, "--profile", acts)
	}
	if env := credentials.EnvProfileName(); env != "" && !credentials.SameCreds(env, acts) {
		return refuseSelection(out, asJSON, what, hintFmt, env, credentials.EnvProfileVar(), acts)
	}
	return false
}

// refuseSelection prints the usage error and exits. hintFmt takes one %s: the
// selected profile name.
//
// Reached only when the selection DIVERGES from `acts`, which is the whole
// property these refusals defend: a selection naming the profile the command
// would act on anyway asks for exactly the no-flag behaviour, so it is allowed
// through. Refusing on mere presence instead told the typical customer — one
// profile, named `default` — that `default` was "another profile", and hinted
// that they switch to the profile they were already on.
func refuseSelection(out io.Writer, asJSON bool, what, hintFmt, name, how, acts string) bool {
	hint := fmt.Sprintf(hintFmt, name)
	if how == credentials.EnvProfile || how == credentials.FacetsEnvProfile {
		hint = "unset " + how + " for this command, or " + hint
	}
	render.PrintError(out, asJSON,
		fmt.Sprintf("%s can't be pointed at another profile (%s=%s); it acts on %q", what, how, name, acts),
		hint, exitcode.Usage)
	osExit(exitcode.Usage)
	return true
}

// cleanupOldBinaries removes the copies that an earlier update on Windows
// renamed aside (selfupdate.CleanupOld). A no-op elsewhere.
func cleanupOldBinaries() {
	if self, err := os.Executable(); err == nil {
		selfupdate.CleanupOld(self)
	}
}

// Execute runs the root command. Called from main.
func Execute() {
	cleanupOldBinaries()
	// The temporary praxis of an in-place update does only the update.
	if len(os.Args) > 1 && os.Args[1] == finishUpdateArg {
		self, _ := os.Executable()
		if err := finishUpdate(os.Stdout, self, os.Args[2:]); err != nil {
			fmt.Fprintln(os.Stderr, "Error:", err)
			osExit(1)
		}
		return
	}
	// The detached praxis of an automatic update does only the update.
	if len(os.Args) > 1 && os.Args[1] == autoUpdateArg {
		httpclient.Version, httpclient.Command = version, autoUpdateArg
		if err := runAutoUpdate(os.Stdout); err != nil {
			fmt.Fprintln(os.Stderr, "Error:", err)
			osExit(1)
		}
		return
	}
	started := time.Now()
	httpclient.Version, httpclient.Command = version, commandPath(os.Args[1:])
	// The census must name the profile this command uses, and the background
	// check starts before cobra parses --profile.
	clifeed.SetProfileFlag(profileFlagFromArgs(os.Args[1:]))
	// First-run: land the pre-login GTM skill into the AI host so a freshly
	// installed praxis is discoverable before any login. Marker-gated (one
	// stat() after the first time) and skipped for machine-invoked commands;
	// never blocks the command it precedes.
	maybeFirstRunBootstrap(os.Args[1:])
	// After a brew upgrade, `praxis update` or a manual install, bring the
	// installed praxis skill to this binary's text. Silent; see the func doc.
	maybeRefreshEmbeddedSkills(os.Args[1:])
	// Once per version, run setup in the background: the hook repair, raptor
	// and its skill. The Homebrew cask no longer runs it.
	maybeStartSetup(os.Args[1:])
	// Control-plane PATs an older praxis kept in ~/.praxis/credentials move to
	// raptor's file, the shared store; its active-profile pointer becomes the
	// [default] section. Silent and best-effort.
	// Skipped for version/update/completion, which never read the store.
	var promoted, kept string
	if !skipUpdateCheck(os.Args[1:]) {
		_, _ = credentials.MigrateLegacyPATs()
		promoted, kept, _ = credentials.MigrateLegacyPointer()
	}
	if render.IsTTY(os.Stderr) && !skipUpdateCheck(os.Args[1:]) {
		if promoted != "" {
			note := fmt.Sprintf("Note: profile %q is now the [default] section (your old active-profile pointer was retired).", promoted)
			if kept != "" {
				note += fmt.Sprintf(" The previous default was kept as [%s].", kept)
			}
			fmt.Fprintln(os.Stderr, note)
		}
		// A pre-v1.11 per-directory pointer no longer pins anything; say so once
		// per run to a human, so a repo that used to be local mode is not a mystery.
		if p, profile, ok := paths.LegacyProjectPointer(); ok {
			fmt.Fprintf(os.Stderr, "Note: %s is an old per-directory pointer and is ignored. To pin this tree again run `praxis profiles use %s --local`, or delete that file.\n", p, profile)
		}
	}
	// Fire a background check for a newer release. For an interactive human
	// (stderr is a TTY) it checks praxis and raptor and prints a notice. When an
	// AI host or a script runs praxis, it only sends praxis's daily check to the
	// central feed, which counts who runs which version, and prints nothing.
	// Suppressed for version/update/completion and dev builds.
	//
	// The select returns the instant the result is ready, so the warm-cache path
	// doesn't wait; only the one cold fetch each day waits, bounded by
	// updateCheckMaxWait.
	var notify func()
	if !render.IsTTY(os.Stderr) && !skipUpdateCheck(os.Args[1:]) {
		done := make(chan struct{})
		go func() {
			quietDailyCheck(time.Now())
			close(done)
		}()
		wait := quietCheckWait()
		notify = func() {
			select {
			case <-done:
			case <-time.After(wait):
			}
		}
	}
	if render.IsTTY(os.Stderr) && !skipUpdateCheck(os.Args[1:]) {
		ch := make(chan []staleNag, 1)
		go func() { ch <- collectStaleNags() }()
		notify = func() {
			select {
			case nags := <-ch:
				for _, n := range nags {
					if n.Freshness.Tool == "praxis" && autoUpdateHandles(n.Freshness.Latest) {
						continue
					}
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
	// History and the report flush run before any os.Exit, so a failed command
	// is recorded too. Both are best-effort and silent.
	recordHistory(os.Args[1:], httpclient.Command, started, err != nil)
	flushQueuedReports(os.Args[1:], httpclient.Command)
	// Run the notification before any os.Exit so an error path still nags
	// (the deferred-then-Exit ordering is handled explicitly here).
	if notify != nil {
		notify()
	}
	if render.IsTTY(os.Stderr) && !skipUpdateCheck(os.Args[1:]) {
		if msg := autoUpdateNotice(); msg != "" {
			fmt.Fprintln(os.Stderr, msg)
		}
	}
	maybeAutoUpdate(os.Args[1:])
	if err != nil {
		fmt.Fprintln(os.Stderr, "Error:", err)
		os.Exit(1)
	}
}

// commandPath is the command that args resolve to, without "praxis".
func commandPath(args []string) string {
	c, _, err := rootCmd.Find(args)
	if err != nil || c == rootCmd {
		return ""
	}
	return strings.TrimPrefix(c.CommandPath(), rootCmd.Name()+" ")
}

// profileFlagFromArgs finds the --profile (-p) value in args, before "--", in
// any form that pflag accepts: "--profile X", "--profile=X", "-p X", "-pX".
// When the flag repeats, the last value wins, as it does in pflag. It returns
// "" when there is none.
func profileFlagFromArgs(args []string) string {
	value := ""
	for i := 0; i < len(args); i++ {
		a := args[i]
		switch {
		case a == "--":
			return value
		case a == "--profile" || a == "-p":
			if i+1 < len(args) {
				i++
				value = args[i]
			}
		case strings.HasPrefix(a, "--profile="):
			value = strings.TrimPrefix(a, "--profile=")
		case strings.HasPrefix(a, "-p") && !strings.HasPrefix(a, "--") && len(a) > 2:
			value = strings.TrimPrefix(strings.TrimPrefix(a, "-p"), "=")
		}
	}
	return value
}
