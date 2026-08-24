package cmd

import (
	"errors"
	"fmt"
	"io"

	"github.com/Facets-cloud/praxis-cli/internal/credentials"
	"github.com/Facets-cloud/praxis-cli/internal/exitcode"
	"github.com/Facets-cloud/praxis-cli/internal/paths"
	"github.com/Facets-cloud/praxis-cli/internal/render"
	"github.com/spf13/cobra"
)

var (
	profilesUseJSON  bool
	profilesUseLocal bool
)

func init() {
	profilesUseCmd.Flags().BoolVar(&profilesUseJSON, "json", false, "JSON output (default when stdout is non-TTY)")
	profilesUseCmd.Flags().BoolVar(&profilesUseLocal, "local", false,
		"pin the profile to the current directory tree (writes <cwd>/.praxis) and install its skills project-scoped, instead of switching the global profile")
	profilesCmd.AddCommand(profilesUseCmd)
}

var profilesUseCmd = &cobra.Command{
	Use:     "use <profile>",
	Aliases: []string{"switch"},
	Short:   "Switch the active profile and re-sync its skills",
	Long: `Make an already-authenticated profile active, without re-authenticating.

Switching is not just a pointer flip. At most one profile's org skills
(praxis-*) are on disk at a time, so ` + "`use`" + ` does the same post-auth sync
as ` + "`praxis login`" + `: wipe the previous profile's org skills and agents,
install this profile's catalog, and refresh the MCP tool snapshot. Editing
~/.praxis/config.json by hand skips that and leaves you with one profile
active and another profile's skills installed.

The profile's stored token is verified against its deployment BEFORE
anything is touched:

  • token rejected (expired/revoked) → exits 3, nothing changed; run
    ` + "`praxis login --profile <name>`" + ` to mint a fresh key
  • deployment unreachable          → exits 5, nothing changed; retry

By default this switches the GLOBAL profile (~/.praxis/config.json) and
installs skills user-level. Pass --local to pin the profile to the current
directory tree instead (writes <cwd>/.praxis, leaving the global pointer
alone) and install its skills project-scoped — the same scoping as
` + "`praxis login --local`" + `.

  praxis profiles           list profiles and see which is active
  praxis profiles use acme  switch globally
  praxis profiles use acme --local   pin to this repo only

This switch is MACHINE-GLOBAL: it repoints every other shell and agent
session on this machine and replaces the installed praxis-* skills those
sessions may already have read. To scope one session or one command
instead — writing nothing, invisible to other sessions — use:

  export PRAXIS_PROFILE=acme    every command in THIS session
  praxis -p acme duty list      one command

Run with $PRAXIS_PROFILE set and the switch still applies to everyone
else, but the output reports ` + "`shadowed_by_env`" + ` and
` + "`effective_profile`" + ` because your own session keeps using the variable.
A ` + "`-p`" + ` flag naming a different profile is refused here: this command's
target is its argument.

Credentials are never moved: every profile stays in ~/.praxis/credentials.`,
	Args: cobra.ExactArgs(1),
	// The one argument is a closed set the CLI already knows, so complete it
	// from the store: a typo would otherwise cost a round trip through exit 2.
	ValidArgsFunction: func(cmd *cobra.Command, args []string, toComplete string) ([]string, cobra.ShellCompDirective) {
		if len(args) > 0 {
			return nil, cobra.ShellCompDirectiveNoFileComp
		}
		names, err := credentials.List()
		if err != nil {
			return nil, cobra.ShellCompDirectiveError
		}
		return names, cobra.ShellCompDirectiveNoFileComp
	},
	RunE: func(cmd *cobra.Command, args []string) error {
		out := cmd.OutOrStdout()
		asJSON := render.UseJSON(profilesUseJSON, false, out)
		name := args[0]

		// The target is the positional argument; a global --profile naming a
		// DIFFERENT profile is two answers to the same question. `-p acme
		// profiles use acme` gives one answer twice, so it just proceeds.
		if refusedProfileFlag(out, asJSON, "profiles use",
			"name the profile as the argument instead: `praxis profiles use %s`", name) {
			return nil
		}

		store, err := credentials.Load()
		if err != nil {
			return err
		}
		prof, ok := store[name]
		if !ok {
			render.PrintError(out, asJSON,
				fmt.Sprintf("no profile named %q", name),
				"run `praxis profiles` to list profiles, or `praxis login --profile "+name+" --url <https://your-deployment>` to create it",
				exitcode.Usage)
			osExit(exitcode.Usage)
			return nil // reached only under test (osExit stubbed)
		}
		if prof.Token == "" {
			render.PrintError(out, asJSON,
				fmt.Sprintf("profile %q has no stored token", name),
				"run `praxis login --profile "+name+"` to authenticate it",
				exitcode.Auth)
			osExit(exitcode.Auth)
			return nil
		}

		// What was active before, so the summary can report the switch and
		// detect a project pointer that will shadow a global one.
		prior, _ := credentials.ResolveActive("")

		// Verify BEFORE mutating anything. A switch that flips the pointer
		// and then fails to fetch the catalog would leave the previous
		// profile's org skills on disk under the new profile's name — the
		// one state this command exists to prevent.
		// Auth(), not the bare token: a control-plane PAT profile authenticates
		// as Bearer + X-Facets-Username, and verifying it any other way would
		// 401 and mis-report a perfectly good profile as revoked.
		user, err := fetchAuthMe(prof.URL, prof.Auth())
		if err != nil {
			if errors.Is(err, errTokenRejected) {
				render.PrintError(out, asJSON,
					fmt.Sprintf("stored token for profile %q is no longer valid: %v", name, err),
					"run `praxis login --profile "+name+"` to mint a fresh key; nothing was changed",
					exitcode.Auth)
				osExit(exitcode.Auth)
				return nil
			}
			// Transient: the token's validity is unknown, so don't wipe the
			// current profile's skills on the strength of a flaky network.
			render.PrintError(out, asJSON,
				fmt.Sprintf("couldn't reach %s to verify profile %q: %v", prof.URL, name, err),
				"check your connection and retry; nothing was changed",
				exitcode.Network)
			osExit(exitcode.Network)
			return nil
		}

		// Self-heal a stale stored host (e.g. an apex URL that 301s to www)
		// now that we know where /auth/me actually landed, so later MCP
		// invokes through this profile don't pay the redirect (issue #19-A).
		baseURL := prof.URL
		if user.canonicalBaseURL != "" && user.canonicalBaseURL != baseURL {
			baseURL = user.canonicalBaseURL
			prof.URL = baseURL
			if err := credentials.Put(name, prof); err != nil {
				return fmt.Errorf("update profile URL: %w", err)
			}
		}

		projectRoot, restore, err := activateProfile(name, profilesUseLocal)
		if err != nil {
			// The realistic failure is --local outside the home subtree, which
			// SetActiveLocal refuses (project-root discovery is home-bounded).
			hint := "nothing was changed"
			if profilesUseLocal {
				hint = "run it from a directory under your home directory, or drop --local to switch globally; nothing was changed"
			}
			render.PrintError(out, asJSON, err.Error(), hint, exitcode.Usage)
			osExit(exitcode.Usage)
			return nil
		}
		defer restore()

		// Same sync as login: meta-skill, wipe previous org skills, install
		// this profile's catalog, refresh the MCP snapshot.
		state := postAuthSetup(out, asJSON, baseURL, prof.Auth())

		summary := switchSummary{
			Profile:     name,
			Previous:    prior.Name,
			URL:         baseURL,
			ProjectRoot: projectRoot,
			// store is the credentials file this command already loaded, so the
			// count is free.
			MultiProfile: len(store) > 1,
		}
		// The switch is real but can be invisible where it was run, by two
		// different shadows. Report whichever actually wins — env outranks the
		// project pointer, matching credentials.resolveName.
		switch {
		case prior.Source == credentials.SourceEnv && prior.Name != name:
			summary.ShadowedByEnv = prior.Name
		case !profilesUseLocal && prior.Source == credentials.SourceProject && prior.Name != name:
			if root, ok, _ := paths.ProjectRoot(); ok {
				summary.ShadowedRoot = root
			}
		}

		// Mirror login's display fallback: /auth/me returns no email for a
		// control-plane PAT profile, whose identity is its stored username.
		displayName := user.Email
		if displayName == "" {
			displayName = prof.Username
		}

		if asJSON {
			payload := setupPayload(name, displayName, baseURL, projectRoot, prof.RaptorProfile, profilesUseLocal, state)
			payload["previous_profile"] = summary.Previous
			// A global switch is machine-wide: every other shell and agent
			// session on this machine now resolves to it, and the org skills on
			// disk were just replaced under them. Say so, so a caller can pick
			// $PRAXIS_PROFILE instead when it only meant to scope itself.
			if !profilesUseLocal && summary.MultiProfile {
				payload["scope_note"] = "machine-global: affects every shell and agent session on this machine; use " +
					credentials.EnvProfile + "=" + name + " to scope one session instead"
			}
			if summary.ShadowedRoot != "" {
				payload["shadowed_by_project_root"] = summary.ShadowedRoot
				// The pointer really did move, but resolution here does not.
				// Spell out what commands in this cwd will actually use.
				payload["effective_profile"] = summary.Previous
			}
			if summary.ShadowedByEnv != "" {
				payload["shadowed_by_env"] = credentials.EnvProfile + "=" + summary.ShadowedByEnv
				payload["effective_profile"] = summary.ShadowedByEnv
			}
			return render.JSON(out, payload)
		}
		renderProfileSwitchText(out, summary)
		return nil
	},
}

// switchSummary is the outcome of a profile switch, in the shape both
// renderers need. ProjectRoot is non-empty exactly when --local pinned the
// profile to a directory tree. At most one Shadowed* field is set, naming what
// still outranks the switch where it was run: a project pointer in this tree,
// or $PRAXIS_PROFILE in this session.
//
// MultiProfile gates the machine-global warning. With one profile in the store
// there is nothing for a global switch to disturb — every session resolves to
// the same profile before and after — so telling a re-syncing single-profile
// user that other sessions just had their skills replaced is noise, and the
// PRAXIS_PROFILE habit it teaches is what several commands then refuse.
type switchSummary struct {
	Profile       string
	Previous      string
	URL           string
	ProjectRoot   string
	ShadowedRoot  string
	ShadowedByEnv string
	MultiProfile  bool
}

func renderProfileSwitchText(out io.Writer, s switchSummary) {
	switch {
	case s.ProjectRoot != "":
		fmt.Fprintf(out, "\n✓ Pinned profile %q to %s (url: %s)\n", s.Profile, s.ProjectRoot, s.URL)
		return // a local pin is never shadowed — it IS the project pointer
	case s.Previous == s.Profile:
		fmt.Fprintf(out, "\n✓ Profile %q is active (url: %s) — skills re-synced\n", s.Profile, s.URL)
	default:
		fmt.Fprintf(out, "\n✓ Switched to profile %q from %q (url: %s)\n", s.Profile, s.Previous, s.URL)
	}
	if s.ShadowedRoot != "" {
		fmt.Fprintf(out, "\nNote: this directory tree is pinned to profile %q by %s,\n"+
			"      so commands run here still use %q. Run `praxis profiles use %s --local`\n"+
			"      to repin this tree, or delete that directory to leave local mode.\n",
			s.Previous, s.ShadowedRoot, s.Previous, s.Profile)
	}
	if s.ShadowedByEnv != "" {
		fmt.Fprintf(out, "\nNote: %s=%s is set in this shell, so commands HERE still use %q.\n"+
			"      Other sessions now get %q. Unset it to follow the switch.\n",
			credentials.EnvProfile, s.ShadowedByEnv, s.ShadowedByEnv, s.Profile)
	}
	// A global switch is machine-wide. Anyone running several agent sessions
	// needs to know their skills just changed underneath them.
	if s.ShadowedByEnv == "" && s.MultiProfile {
		fmt.Fprintf(out, "\nThis is machine-global: every other shell and agent session now resolves to\n"+
			"%q, and the installed praxis-* skills were replaced. To scope just one\n"+
			"session instead, leave the active profile alone and export %s=%s.\n",
			s.Profile, credentials.EnvProfile, s.Profile)
	}
}
