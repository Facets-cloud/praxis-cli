package cmd

import (
	"fmt"
	"path/filepath"

	"github.com/Facets-cloud/praxis-cli/internal/credentials"
	"github.com/Facets-cloud/praxis-cli/internal/exitcode"
	"github.com/Facets-cloud/praxis-cli/internal/paths"
	"github.com/Facets-cloud/praxis-cli/internal/render"
	"github.com/spf13/cobra"
)

// Subcommands of `praxis profiles` for managing the credentials store
// without a login round-trip (issue #66: the only in-band alternatives were
// hand-editing the credentials file or re-running login). All are
// power-user tools: a single-profile user never needs them, and none
// touches installed skills or opens a browser.

var (
	profilesRenameJSON bool
	profilesRmJSON     bool
	profilesPinJSON    bool
)

func init() {
	profilesRenameCmd.Flags().BoolVar(&profilesRenameJSON, "json", false, "JSON output")
	profilesRmCmd.Flags().BoolVar(&profilesRmJSON, "json", false, "JSON output")
	profilesPinCmd.Flags().BoolVar(&profilesPinJSON, "json", false, "JSON output")
	profilesUnpinCmd.Flags().BoolVar(&profilesPinJSON, "json", false, "JSON output")
	profilesCmd.AddCommand(profilesRenameCmd, profilesRmCmd, profilesPinCmd, profilesUnpinCmd)
}

var profilesPinCmd = &cobra.Command{
	Use:   "pin <profile>",
	Short: "Use a profile in this folder, for praxis and raptor (writes .facets/profile)",
	Long: `Pin a profile to the current folder tree. Every praxis and raptor command
run in this folder, or below it, then uses the profile when nothing else
selects one (--profile, PRAXIS_PROFILE and FACETS_PROFILE still win). Other
folders, shells and agent sessions do not change.

The pin is <folder>/.facets/profile and holds only the profile name, never a
token. The folder is the project tree at or above the working directory, else
the working directory itself. The home directory cannot be pinned: [default]
is the machine default (` + "`praxis profiles set-default`" + `).

Installed skills are not touched. To install the profile's org skills into
this folder, run ` + "`praxis refresh-skills`" + ` here after the pin, or log in
with ` + "`praxis login --profile <name> --local`" + `, which does both.`,
	Args:              cobra.ExactArgs(1),
	ValidArgsFunction: completeProfileNames,
	RunE: func(cmd *cobra.Command, args []string) error {
		out := cmd.OutOrStdout()
		asJSON := render.UseJSON(profilesPinJSON, false, out)
		name := args[0]
		if refusedProfileFlag(out, asJSON, "profiles pin",
			"name the profile as the argument instead: `praxis profiles pin %s`", name) {
			return nil
		}
		dir, err := pinDir()
		if err == nil {
			err = credentials.WritePin(dir, name)
		}
		if err == nil {
			// A pinned folder is a project root; keep its state out of git.
			_, err = paths.InitProjectRoot(dir)
		}
		if err != nil {
			render.PrintError(out, asJSON, err.Error(),
				"run `praxis profiles` to see what exists; a pin needs a control-plane PAT profile", exitcode.Usage)
			osExit(exitcode.Usage)
			return err // reached only under test (osExit stubbed)
		}
		pin := credentials.PinPathIn(dir)
		if asJSON {
			return render.JSON(out, map[string]any{"ok": true, "profile": name, "pin_path": pin})
		}
		fmt.Fprintf(out, "✓ Pinned profile %q to %s (%s)\n", name, dir, pin)
		fmt.Fprintln(out, "  praxis and raptor use it in this folder. Run `praxis refresh-skills` here to install its org skills.")
		return nil
	},
}

var profilesUnpinCmd = &cobra.Command{
	Use:   "unpin",
	Short: "Remove the profile pin of this folder (.facets/profile)",
	Long: `Remove the nearest pin at or above the working directory. Commands in that
folder then use the machine default again. Credentials and installed skills
are not touched.`,
	Args: cobra.NoArgs,
	RunE: func(cmd *cobra.Command, args []string) error {
		out := cmd.OutOrStdout()
		asJSON := render.UseJSON(profilesPinJSON, false, out)
		path, err := credentials.RemovePin()
		if err != nil {
			return err
		}
		if asJSON {
			return render.JSON(out, map[string]any{"ok": true, "removed": path})
		}
		if path == "" {
			fmt.Fprintln(out, "No pin at or above this folder.")
			return nil
		}
		fmt.Fprintf(out, "✓ Removed %s\n", path)
		return nil
	},
}

// pinDir is where a new pin goes: the project tree at or above the working
// directory, so a pin from a subfolder does not nest a second tree, else the
// working directory.
func pinDir() (string, error) {
	if root, ok, _ := paths.ProjectRoot(); ok {
		return filepath.Dir(root), nil
	}
	return paths.Getwd()
}

var profilesRenameCmd = &cobra.Command{
	Use:   "rename OLD NEW",
	Short: "Rename a profile (credentials only — no browser, no skill changes)",
	Long: `Rename a profile section, keeping its URL, username, and token, in
whichever credentials file holds it (~/.facets/credentials for a control-plane
PAT, ~/.praxis/credentials for a Praxis API key).

Installed skills are not touched: they belong to the profile's org, not its
name. No browser opens — rename edits only the credentials file, so you don't
have to re-run login (which would re-sync skills) just to change a name.

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
