package cmd

import (
	"fmt"
	"os"

	"github.com/Facets-cloud/praxis-cli/internal/credentials"
	"github.com/Facets-cloud/praxis-cli/internal/paths"
	"github.com/Facets-cloud/praxis-cli/internal/render"
	"github.com/Facets-cloud/praxis-cli/internal/skillinstall"
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
	Short: "Remove credentials and org skills for a profile (or --all)",
	Long: `Remove credentials AND uninstall this profile's org skills
(praxis-* prefix) from every AI host. The praxis meta-skill stays
installed so the AI host still knows how to log back in.

Default: target the active profile.
  --profile X    target a specific profile
  --all          wipe every profile's credentials + every praxis-* org
                 skill from every host (meta-skill stays).`,
	Args: cobra.NoArgs,
	RunE: func(cmd *cobra.Command, args []string) error {
		out := cmd.OutOrStdout()
		asJSON := render.UseJSON(logoutJSON, false, out)

		if logoutAll {
			if err := credentials.DeleteAll(); err != nil {
				return err
			}
			removed, err := skillinstall.UninstallByPrefix("praxis-")
			if err != nil {
				if !asJSON {
					fmt.Fprintf(out, "Warning: removing org skills failed: %v\n", err)
				}
			}
			// Best-effort: drop the manifest snapshot too — it's tied to
			// whatever profile was last active and shouldn't survive a wipe.
			if p, perr := paths.MCPTools(); perr == nil {
				_ = os.Remove(p)
			}
			if asJSON {
				return render.JSON(out, map[string]any{
					"removed":        "all",
					"removed_skills": liteResults(removed),
				})
			}
			fmt.Fprintln(out, "✓ Removed all profiles.")
			if len(removed) > 0 {
				fmt.Fprintf(out, "✓ Removed %d org skill(s) from %d host(s).\n",
					countSkills(removed), countHosts(removed))
			}
			return nil
		}

		// Target either --profile X or the active profile.
		active, _ := credentials.ResolveActive(logoutProfile)
		store, _ := credentials.Load()
		credsPresent := false
		if _, ok := store[active.Name]; ok {
			credsPresent = true
		}

		if credsPresent {
			if err := credentials.Delete(active.Name); err != nil {
				return err
			}
		}

		// Wipe org skills for whichever profile was active. Note: with the
		// v0.7 single-active-profile model, org skills always belong to the
		// currently-active profile, so wiping them here is correct whether
		// the user targeted active or --profile X (X may not be active,
		// but at most ONE profile's org skills are on disk at a time).
		removed, err := skillinstall.UninstallByPrefix("praxis-")
		if err != nil {
			if !asJSON {
				fmt.Fprintf(out, "Warning: removing org skills failed: %v\n", err)
			}
		}
		if p, perr := paths.MCPTools(); perr == nil {
			_ = os.Remove(p)
		}

		if asJSON {
			return render.JSON(out, map[string]any{
				"removed":        ifTrue(credsPresent, active.Name),
				"removed_skills": liteResults(removed),
			})
		}
		if credsPresent {
			fmt.Fprintf(out, "✓ Removed profile %q.\n", active.Name)
		} else {
			fmt.Fprintf(out, "No credentials to remove for profile %q.\n", active.Name)
		}
		if len(removed) > 0 {
			fmt.Fprintf(out, "✓ Removed %d org skill(s) from %d host(s).\n",
				countSkills(removed), countHosts(removed))
		}
		return nil
	},
}

// countSkills returns the number of distinct skill names in the list.
// (One skill installed across N hosts shows as N entries.)
func countSkills(in []skillinstall.Installation) int {
	seen := map[string]struct{}{}
	for _, e := range in {
		seen[e.SkillName] = struct{}{}
	}
	return len(seen)
}

func countHosts(in []skillinstall.Installation) int {
	seen := map[string]struct{}{}
	for _, e := range in {
		seen[e.Harness] = struct{}{}
	}
	return len(seen)
}

// ifTrue is a tiny helper so the JSON branch can omit the field when
// no credentials were actually present.
func ifTrue(cond bool, v string) any {
	if cond {
		return v
	}
	return nil
}
