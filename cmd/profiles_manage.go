package cmd

import (
	"fmt"

	"github.com/Facets-cloud/praxis-cli/internal/credentials"
	"github.com/Facets-cloud/praxis-cli/internal/exitcode"
	"github.com/Facets-cloud/praxis-cli/internal/render"
	"github.com/spf13/cobra"
)

// Subcommands of `praxis profiles` for managing the credentials store
// without a login round-trip (issue #66: the only in-band alternatives were
// hand-editing ~/.praxis/credentials or minting another API key). Both are
// power-user tools: a single-profile user never needs them, and neither
// touches installed skills or opens a browser.

var (
	profilesRenameJSON bool
	profilesRmJSON     bool
)

func init() {
	profilesRenameCmd.Flags().BoolVar(&profilesRenameJSON, "json", false, "JSON output")
	profilesRmCmd.Flags().BoolVar(&profilesRmJSON, "json", false, "JSON output")
	profilesCmd.AddCommand(profilesRenameCmd)
	profilesCmd.AddCommand(profilesRmCmd)
}

var profilesRenameCmd = &cobra.Command{
	Use:   "rename OLD NEW",
	Short: "Rename a profile (credentials only — no browser, no skill changes)",
	Long: `Rename a profile section in ~/.praxis/credentials, keeping its URL,
username, token, and raptor pairing. If the global active-profile pointer
named OLD it follows to NEW automatically.

Installed skills are not touched: they belong to the profile's org, not its
name. No browser opens and no API key is created — this fixes the "re-login
just to rename" workaround that minted an orphaned key each time.

Directory trees pinned with 'praxis login --profile OLD --local' keep a
project pointer naming OLD; those trees silently fall back to the global
profile until re-pinned with 'praxis login --profile NEW --local'.`,
	Args: cobra.ExactArgs(2),
	RunE: func(cmd *cobra.Command, args []string) error {
		out := cmd.OutOrStdout()
		asJSON := render.UseJSON(profilesRenameJSON, false, out)
		oldName, newName := args[0], args[1]

		pointerUpdated, err := credentials.Rename(oldName, newName)
		if err != nil {
			render.PrintError(out, asJSON, err.Error(),
				"run `praxis profiles` to see what exists", exitcode.Usage)
			osExit(exitcode.Usage)
			return err // reached only under test (osExit stubbed)
		}

		if asJSON {
			return render.JSON(out, map[string]any{
				"ok":                     true,
				"renamed_from":           oldName,
				"renamed_to":             newName,
				"active_pointer_updated": pointerUpdated,
			})
		}
		fmt.Fprintf(out, "✓ Renamed profile %q → %q\n", oldName, newName)
		if pointerUpdated {
			fmt.Fprintf(out, "  Active-profile pointer updated to %q.\n", newName)
		}
		return nil
	},
}

var profilesRmCmd = &cobra.Command{
	Use:   "rm NAME",
	Short: "Remove a non-active profile's credentials (no skill changes)",
	Long: `Delete one profile section from ~/.praxis/credentials.

Only non-active profiles can be removed here: the active profile owns the
org skills installed on disk, so removing it goes through 'praxis logout'
(which also cleans those up). Removing a non-active profile touches
credentials only — installed skills and the active profile are unaffected,
so there is no double skill-cycle from switching just to delete.`,
	Args: cobra.ExactArgs(1),
	RunE: func(cmd *cobra.Command, args []string) error {
		out := cmd.OutOrStdout()
		asJSON := render.UseJSON(profilesRmJSON, false, out)
		name := args[0]

		// The PERSISTED pointer decides what "active" means here -- not
		// ResolveActiveGlobal, and not the project pointer. --profile and
		// $PRAXIS_PROFILE select which deployment a session TALKS to, while the
		// pointer is what owns the org skills on disk, so it alone decides which
		// profile can't be deleted out from under them. Resolving through an
		// override would make `PRAXIS_PROFILE=B praxis profiles rm A` delete the
		// profile the pointer and the installed skills still refer to.
		if name == credentials.PersistedActiveName() {
			msg := fmt.Sprintf("%q is the active profile", name)
			render.PrintError(out, asJSON, msg,
				"use `praxis logout` to remove the active profile (it also removes its installed org skills)",
				exitcode.Usage)
			osExit(exitcode.Usage)
			return fmt.Errorf("%s", msg)
		}
		store, err := credentials.Load()
		if err != nil {
			return err
		}
		if _, ok := store[name]; !ok {
			msg := fmt.Sprintf("profile %q does not exist", name)
			render.PrintError(out, asJSON, msg,
				"run `praxis profiles` to see what exists", exitcode.Usage)
			osExit(exitcode.Usage)
			return fmt.Errorf("%s", msg)
		}
		if err := credentials.Delete(name); err != nil {
			return err
		}

		if asJSON {
			return render.JSON(out, map[string]any{
				"ok":      true,
				"removed": name,
			})
		}
		fmt.Fprintf(out, "✓ Removed profile %q (credentials only — installed skills untouched)\n", name)
		return nil
	},
}
