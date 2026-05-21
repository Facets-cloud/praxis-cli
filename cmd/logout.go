package cmd

import (
	"fmt"
	"os"

	"github.com/Facets-cloud/praxis-cli/internal/agentinstall"
	"github.com/Facets-cloud/praxis-cli/internal/credentials"
	"github.com/Facets-cloud/praxis-cli/internal/paths"
	"github.com/Facets-cloud/praxis-cli/internal/render"
	"github.com/Facets-cloud/praxis-cli/internal/skillinstall"
	"github.com/spf13/cobra"
)

var (
	logoutAll  bool
	logoutJSON bool
)

func init() {
	logoutCmd.Flags().BoolVar(&logoutAll, "all", false, "remove ALL profiles + active-profile pointer")
	logoutCmd.Flags().BoolVar(&logoutJSON, "json", false, "JSON output")
	rootCmd.AddCommand(logoutCmd)
}

var logoutCmd = &cobra.Command{
	Use:   "logout",
	Short: "Remove credentials and org skills for the active profile (or --all)",
	Long: `Remove credentials AND uninstall org skills (praxis-* prefix) from
every AI host. The praxis meta-skill stays installed so the AI host
still knows how to log back in.

  praxis logout         active profile: creds + org skills + manifest
  praxis logout --all   every profile's creds + every host's org skills

To remove a non-active profile's credentials specifically, switch to it
first with ` + "`praxis login --profile X`" + ` and then run logout. With v0.7's
invariant that at most one profile's org skills are on disk at a time,
there's no way (and no need) to target a non-active profile directly.`,
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
			removedAgents, agErr := agentinstall.UninstallByPrefix("praxis-")
			if agErr != nil && !asJSON {
				fmt.Fprintf(out, "Warning: removing agents failed: %v\n", agErr)
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
					"removed_agents": agentLogoutLite(removedAgents),
				})
			}
			fmt.Fprintln(out, "✓ Removed all profiles.")
			if len(removed) > 0 {
				fmt.Fprintf(out, "✓ Removed %d org skill(s) from %d host(s).\n",
					countSkills(removed), countHosts(removed))
			}
			if len(removedAgents) > 0 {
				fmt.Fprintf(out, "✓ Removed %d agent file(s).\n", len(removedAgents))
			}
			return nil
		}

		// Target the active profile only — v0.7 dropped --profile from
		// logout (see Long). To remove a non-active profile, login to
		// it first.
		active, _ := credentials.ResolveActive("")
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

		// Wipe org skills + agents. With the v0.7 single-active-profile
		// model, both always belong to the currently-active profile, so
		// this is unambiguous.
		removed, err := skillinstall.UninstallByPrefix("praxis-")
		if err != nil {
			if !asJSON {
				fmt.Fprintf(out, "Warning: removing org skills failed: %v\n", err)
			}
		}
		removedAgents, agErr := agentinstall.UninstallByPrefix("praxis-")
		if agErr != nil && !asJSON {
			fmt.Fprintf(out, "Warning: removing agents failed: %v\n", agErr)
		}
		if p, perr := paths.MCPTools(); perr == nil {
			_ = os.Remove(p)
		}

		if asJSON {
			return render.JSON(out, map[string]any{
				"removed":        ifTrue(credsPresent, active.Name),
				"removed_skills": liteResults(removed),
				"removed_agents": agentLogoutLite(removedAgents),
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
		if len(removedAgents) > 0 {
			fmt.Fprintf(out, "✓ Removed %d agent file(s).\n", len(removedAgents))
		}
		return nil
	},
}

// agentLogoutLite shapes the JSON output for removed_agents to match
// the agentInstallationLite shape login uses.
func agentLogoutLite(in []skillinstall.AgentInstallation) []agentInstallationLite {
	out := make([]agentInstallationLite, 0, len(in))
	for _, r := range in {
		out = append(out, agentInstallationLite{
			AgentName: r.AgentName,
			Kind:      r.Kind,
			Harness:   r.Harness,
			Path:      r.Path,
		})
	}
	return out
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
