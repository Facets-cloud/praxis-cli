package cmd

import (
	"errors"
	"fmt"
	"io"

	"github.com/Facets-cloud/praxis-cli/internal/credentials"
	"github.com/Facets-cloud/praxis-cli/internal/exitcode"
	"github.com/Facets-cloud/praxis-cli/internal/render"
	"github.com/spf13/cobra"
)

var profilesSetDefaultJSON bool

func init() {
	profilesSetDefaultCmd.Flags().BoolVar(&profilesSetDefaultJSON, "json", false, "JSON output (default when stdout is non-TTY)")
	profilesCmd.AddCommand(profilesSetDefaultCmd, profilesUseRemovedCmd)
}

var profilesSetDefaultCmd = &cobra.Command{
	Use:   "set-default <profile>",
	Short: "Make a profile the machine default and re-sync its skills",
	Long: `Make an already-authenticated profile the machine default, without
re-authenticating. This is the only command that moves [default] to another
profile.

It copies the profile's section over [default] in ~/.facets/credentials — the
section a bare praxis or raptor command uses when nothing selects a profile —
so both CLIs move together. A [default] that no other section holds is kept
first under a name from its host. The user-level org skills (praxis-*) follow
[default], so set-default does the same sync as ` + "`praxis login`" + `: wipe
the previous profile's org skills and agents, install this profile's catalog,
and refresh the MCP tool snapshot.

The profile's stored token is verified against its deployment BEFORE
anything is touched:

  • token rejected (expired/revoked) → exits 3, nothing changed; run
    ` + "`praxis login --profile <name>`" + ` to log in again
  • deployment unreachable          → exits 5, nothing changed; retry

This is MACHINE-GLOBAL: every shell and agent session on this machine that
selects no profile now uses it, and their installed praxis-* skills change.
To use a profile without that, select it instead — nothing is written:

  praxis --profile acme duty list   one command
  export FACETS_PROFILE=acme        every command in this shell (both CLIs)
  praxis profiles pin acme          every command in this folder (both CLIs)`,
	Args: cobra.ExactArgs(1),
	// The one argument is a closed set the CLI already knows, so complete it
	// from the store: a typo would otherwise cost a round trip through exit 2.
	ValidArgsFunction: completeProfileNames,
	RunE: func(cmd *cobra.Command, args []string) error {
		out := cmd.OutOrStdout()
		asJSON := render.UseJSON(profilesSetDefaultJSON, false, out)
		name := args[0]

		// The target is the positional argument; a global --profile naming a
		// DIFFERENT profile is two answers to the same question.
		if refusedProfileFlag(out, asJSON, "profiles set-default",
			"name the profile as the argument instead: `praxis profiles set-default %s`", name) {
			return nil
		}

		// The home store: [default] lives there, whatever folder this runs in.
		store, err := credentials.LoadHome()
		if err != nil {
			return err
		}
		prof, ok := store[name]
		if !ok {
			render.PrintError(out, asJSON,
				fmt.Sprintf("no profile named %q in ~/.facets/credentials or ~/.praxis/credentials", name),
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
		previous := homeDefaultName(store)
		// One identity (copies, rotated tokens, an old API key for the same
		// deployment) has no other session to disturb: no machine-wide warning.
		multi := credentials.Identities(store) > 1

		// Verify BEFORE mutating anything. A switch that rewrites [default]
		// and then fails to fetch the catalog would leave the previous
		// profile's org skills on disk under the new profile's name.
		// Auth(), not the bare token: a control-plane PAT profile authenticates
		// as Bearer + X-Facets-Username.
		user, err := fetchAuthMe(prof.URL, prof.Auth())
		if err != nil {
			if errors.Is(err, errTokenRejected) {
				render.PrintError(out, asJSON,
					fmt.Sprintf("stored token for profile %q is no longer valid: %v", name, err),
					"run `praxis login --profile "+name+"` to log in again; nothing was changed",
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
		// now that we know where /auth/me actually landed (issue #19-A).
		if user.canonicalBaseURL != "" && user.canonicalBaseURL != prof.URL {
			prof.URL = user.canonicalBaseURL
			if err := credentials.Put(name, prof); err != nil {
				return fmt.Errorf("update profile URL: %w", err)
			}
		}

		restore, err := setGlobalDefault(name)
		if err != nil {
			render.PrintError(out, asJSON, err.Error(), "nothing was changed", exitcode.Usage)
			osExit(exitcode.Usage)
			return nil
		}
		defer restore()

		// Same sync as login: meta-skill, wipe previous org skills, install
		// this profile's catalog, refresh the MCP snapshot.
		state := postAuthSetup(out, asJSON, prof.URL, prof.Auth())
		recordOrg(name, prof.URL, state)

		// Where this runs, a selection or a pin can still outrank [default].
		effective := ""
		if here, rerr := credentials.ResolveActive(rootProfile); rerr == nil && !credentials.SameCreds(here.Name, name) {
			effective = here.Name + " (" + string(here.Source) + ")"
		}

		displayName := user.Email
		if displayName == "" {
			displayName = prof.Username
		}
		if asJSON {
			payload := setupPayload(name, displayName, prof.URL, "", false, state)
			payload["previous_default"] = previous
			if multi {
				payload["scope_note"] = "machine-global: every shell and agent session that selects no profile now uses " + name
			}
			if effective != "" {
				payload["effective_profile"] = effective
			}
			return render.JSON(out, payload)
		}
		renderSetDefaultText(out, name, previous, prof.URL, effective, multi)
		return nil
	},
}

// profilesUseRemovedCmd answers the removed `profiles use` (and `switch`) with
// what replaced it, so a host that still has old skill text recovers in one
// step. It changes nothing.
var profilesUseRemovedCmd = &cobra.Command{
	Use:                "use",
	Aliases:            []string{"switch"},
	Hidden:             true,
	DisableFlagParsing: true,
	RunE: func(cmd *cobra.Command, args []string) error {
		out := cmd.OutOrStdout()
		render.PrintError(out, render.UseJSON(false, false, out),
			"`praxis profiles use` was removed: selecting a profile no longer changes the machine default",
			"pass --profile NAME, export FACETS_PROFILE=NAME, pin a folder with `praxis profiles pin NAME`, "+
				"or change the machine default with `praxis profiles set-default NAME`",
			exitcode.Usage)
		osExit(exitcode.Usage)
		return nil
	},
}

// completeProfileNames completes a profile-name argument from the store.
func completeProfileNames(cmd *cobra.Command, args []string, toComplete string) ([]string, cobra.ShellCompDirective) {
	if len(args) > 0 {
		return nil, cobra.ShellCompDirectiveNoFileComp
	}
	names, err := credentials.List()
	if err != nil {
		return nil, cobra.ShellCompDirectiveError
	}
	return names, cobra.ShellCompDirectiveNoFileComp
}

// homeDefaultName names the profile the home [default] holds: the section it
// copies when there is one, else "default", else the sole section, else "".
func homeDefaultName(store map[string]credentials.Profile) string {
	if _, ok := store[credentials.DefaultProfileName]; !ok {
		if len(store) == 1 {
			for n := range store {
				return n
			}
		}
		return ""
	}
	if same := credentials.SameAs(store, credentials.DefaultProfileName); len(same) > 0 {
		return same[0]
	}
	return credentials.DefaultProfileName
}

func renderSetDefaultText(out io.Writer, name, previous, url, effective string, multi bool) {
	switch previous {
	case name:
		fmt.Fprintf(out, "\n✓ Profile %q is the machine default (url: %s) — skills re-synced\n", name, url)
	case "":
		fmt.Fprintf(out, "\n✓ Profile %q is now the machine default (url: %s)\n", name, url)
	default:
		fmt.Fprintf(out, "\n✓ Machine default changed from %q to %q (url: %s)\n", previous, name, url)
	}
	if multi {
		fmt.Fprintln(out, "  Every shell and agent session that selects no profile now uses it.")
	}
	if effective != "" {
		fmt.Fprintf(out, "  Here, commands still use %s.\n", effective)
	}
}
