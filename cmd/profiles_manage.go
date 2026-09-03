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
	Long: `Rename a profile section, keeping its URL, username, and token, in
whichever credentials file holds it (~/.facets/credentials for a control-plane
PAT, ~/.praxis/credentials for a Praxis API key).

Installed skills are not touched: they belong to the profile's org, not its
name. No browser opens and no API key is created — this fixes the "re-login
just to rename" workaround that minted an orphaned key each time.

A [default] section that is a copy of OLD is not renamed; it stays the
active profile. Directory trees pinned with --local have their own
credentials file and are not touched.`,
	Args: cobra.ExactArgs(2),
	RunE: func(cmd *cobra.Command, args []string) error {
		out := cmd.OutOrStdout()
		asJSON := render.UseJSON(profilesRenameJSON, false, out)
		oldName, newName := args[0], args[1]

		err := credentials.Rename(oldName, newName)
		if err != nil {
			render.PrintError(out, asJSON, err.Error(),
				"run `praxis profiles` to see what exists", exitcode.Usage)
			osExit(exitcode.Usage)
			return err // reached only under test (osExit stubbed)
		}

		if asJSON {
			return render.JSON(out, map[string]any{
				"ok":           true,
				"renamed_from": oldName,
				"renamed_to":   newName,
			})
		}
		fmt.Fprintf(out, "✓ Renamed profile %q → %q\n", oldName, newName)
		return nil
	},
}

var profilesRmCmd = &cobra.Command{
	Use:   "rm NAME",
	Short: "Remove a non-active profile's credentials (no skill changes)",
	Long: `Delete one profile section from the credentials file that holds it
(~/.facets/credentials for a control-plane PAT, ~/.praxis/credentials for a
Praxis API key).

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

		// The store's own active profile decides what "active" means here, not
		// the environment. --profile and $PRAXIS_PROFILE select which deployment
		// a session TALKS to, while [default] is what owns the org skills on
		// disk, so it alone decides which profile can't be deleted out from under
		// them.
		// The same credentials under another name are the active profile too:
		// after `login -p acme`, [default] is a copy of [acme], and removing
		// [acme] alone would leave the user logged in while the output says
		// raptor lost it.
		if active := credentials.OnDiskActiveName(); name == active || credentials.SameCreds(name, active) {
			msg := fmt.Sprintf("%q is the active profile", name)
			if name != active {
				msg = fmt.Sprintf("%q holds the active profile's credentials ([%s] is a copy of it)", name, active)
			}
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
		deleted, err := credentials.Delete(name)
		if err != nil {
			return err
		}

		if asJSON {
			return render.JSON(out, map[string]any{
				"ok":                true,
				"removed":           name,
				"raptor_logged_out": deleted.Facets,
			})
		}
		if deleted.Facets {
			fmt.Fprintf(out, "✓ Removed profile %q from %s (raptor is logged out of it too — installed skills untouched)\n", name, deleted.FacetsPath)
			return nil
		}
		fmt.Fprintf(out, "✓ Removed profile %q (credentials only — installed skills untouched)\n", name)
		return nil
	},
}
