package cmd

import (
	"fmt"

	"github.com/Facets-cloud/praxis-cli/internal/credentials"
	"github.com/Facets-cloud/praxis-cli/internal/render"
	"github.com/spf13/cobra"
)

var (
	logoutProfile string
	logoutAll     bool
	logoutJSON    bool
)

func init() {
	logoutCmd.Flags().StringVar(&logoutProfile, "profile", "", "remove only this profile (default: active)")
	logoutCmd.Flags().BoolVar(&logoutAll, "all", false, "remove ALL profiles + active-profile pointer")
	logoutCmd.Flags().BoolVar(&logoutJSON, "json", false, "JSON output")
	rootCmd.AddCommand(logoutCmd)
}

var logoutCmd = &cobra.Command{
	Use:   "logout",
	Short: "Remove credentials for a profile (or --all)",
	Long: `Remove a profile from ~/.praxis/credentials. By default removes the
active profile; use --profile to target a specific one or --all to wipe
everything.`,
	Args: cobra.NoArgs,
	RunE: func(cmd *cobra.Command, args []string) error {
		out := cmd.OutOrStdout()
		asJSON := render.UseJSON(logoutJSON, false, out)

		if logoutAll {
			if err := credentials.DeleteAll(); err != nil {
				return err
			}
			if asJSON {
				return render.JSON(out, map[string]any{"removed": "all"})
			}
			fmt.Fprintln(out, "✓ Removed all profiles.")
			return nil
		}

		// Target either --profile X or the active profile.
		active, _ := credentials.ResolveActive(logoutProfile)
		store, _ := credentials.Load()
		if _, ok := store[active.Name]; !ok {
			if asJSON {
				return render.JSON(out, map[string]any{"removed": nil, "note": "profile not present"})
			}
			fmt.Fprintf(out, "No credentials to remove for profile %q.\n", active.Name)
			return nil
		}

		if err := credentials.Delete(active.Name); err != nil {
			return err
		}

		if asJSON {
			return render.JSON(out, map[string]any{"removed": active.Name})
		}
		fmt.Fprintf(out, "✓ Removed profile %q.\n", active.Name)
		return nil
	},
}
