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
	"github.com/Facets-cloud/praxis-cli/internal/raptorstate"
	"github.com/Facets-cloud/praxis-cli/internal/render"
	"github.com/Facets-cloud/praxis-cli/internal/selfupdate"
	"github.com/Facets-cloud/praxis-cli/internal/skillinstall"
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

// profileNoteShown keeps the profile notes to one set per process.
var profileNoteShown bool

// noteProfileChoice prints, to stderr and once per process, what a machine
// with several profiles needs to know about the profile a command resolved:
// an agent command that selected none got the machine default, and a profile
// whose org is not the one whose skills are installed here. Both are silent on
// a single-profile machine.
func noteProfileChoice(active credentials.Active) {
	if profileNoteShown || !active.Loaded {
		return
	}
	profileNoteShown = true
	for _, line := range []string{implicitProfileNote(active, httpclient.AgentHost()), orgSkillsNote(active)} {
		if line != "" {
			fmt.Fprintln(os.Stderr, line)
		}
	}
}

// implicitProfileNote is the agent guard: an AI host that selected no profile
// on a machine with more than one got the machine default, which is a guess.
func implicitProfileNote(active credentials.Active, host string) string {
	if host == "" || (active.Source != credentials.SourceDefault && active.Source != credentials.SourceSole) {
		return ""
	}
	if store, err := credentials.Load(); err != nil || len(credentials.Distinct(store)) < 2 {
		return ""
	}
	return fmt.Sprintf("praxis: using profile %q (%s); pass --profile NAME to choose another", active.Name, active.Profile.URL)
}

// orgSkillsNote reports a profile from another org than the org skills
// installed here: the skill text the host reads is not this profile's.
func orgSkillsNote(active credentials.Active) string {
	org, ok := installedOrg()
	if !ok || raptorstate.MatchesHost(org.URL, active.Profile.URL) {
		return ""
	}
	return fmt.Sprintf("praxis: the org skills installed here are for profile %q (%s), not %q. "+
		"To install %q's skills in a folder, run `praxis profiles pin %s` and then `praxis refresh-skills` there.",
		org.Profile, org.URL, active.Name, active.Name, active.Name)
}

// installedOrg is the org whose skills an AI host sees here: the active root's,
// else — in a project tree with no org of its own — the user-level one.
func installedOrg() (skillinstall.InstalledOrg, bool) {
	root, err := paths.ActiveRoot()
	if err != nil {
		return skillinstall.InstalledOrg{}, false
	}
	if org, ok := skillinstall.OrgAt(root); ok {
		return org, true
	}
	home, err := paths.Dir()
	if err != nil || home == root {
		return skillinstall.InstalledOrg{}, false
	}
	return skillinstall.OrgAt(home)
}

// noCredentials explains a resolved profile with no token: what selected it,
// and the profiles that exist.
func noCredentials(active credentials.Active) (string, string) {
	msg := fmt.Sprintf("no credentials for profile %q", active.Name)
	if active.PinPath != "" {
		msg += " (pinned by " + active.PinPath + ")"
	}
	hint := "run `praxis login` (or `praxis login --profile " + active.Name + "`)"
	if names, err := credentials.List(); err == nil && len(names) > 0 {
		hint += "; or pass --profile NAME, one of: " + strings.Join(names, ", ")
	}
	return msg, hint
}

// envConflictNote says which variable wins when PRAXIS_PROFILE and
// FACETS_PROFILE name different profiles, or "". raptor follows the same order.
func envConflictNote() string {
	p, f := strings.TrimSpace(os.Getenv(credentials.EnvProfile)), strings.TrimSpace(os.Getenv(credentials.FacetsEnvProfile))
	if p == "" || f == "" || p == f {
		return ""
	}
	return fmt.Sprintf("praxis: %s=%s and %s=%s differ; using %s", credentials.EnvProfile, p, credentials.FacetsEnvProfile, f, p)
}

// refusedProfileFlag refuses a diverging -p/--profile only, leaving
// $PRAXIS_PROFILE alone. Use it where the environment is a legitimate ambient
// session setting but the flag would be ambiguous: `profiles set-default` names its
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
	// deployment under another name — [default] after `profiles set-default X` — and
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
	if note := envConflictNote(); note != "" {
		fmt.Fprintln(os.Stderr, note)
	}
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
			fmt.Fprintf(os.Stderr, "Note: %s is an old per-directory pointer and is ignored. To pin this tree again run `praxis profiles pin %s`, or delete that file.\n", p, profile)
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
