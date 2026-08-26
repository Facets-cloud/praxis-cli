package cmd

import (
	"fmt"
	"os"

	"github.com/Facets-cloud/praxis-cli/internal/agentinstall"
	"github.com/Facets-cloud/praxis-cli/internal/claudehooks"
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

To remove a NON-active profile, use ` + "`praxis profiles rm NAME`" + ` — it
touches credentials only, and skips the double skill-cycle of switching just
to delete. Because at most one profile's org skills are on disk at a time,
logout REFUSES a profile selection that names a DIFFERENT profile than the
active one (exit 2, nothing removed) rather than delete one profile's
credentials while wiping another's: that covers the global ` + "`--profile`" + ` flag
AND ` + "`$PRAXIS_PROFILE`" + `. Naming the active profile is allowed — it asks for
exactly what a bare logout does.`,
	Args: cobra.NoArgs,
	RunE: func(cmd *cobra.Command, args []string) error {
		out := cmd.OutOrStdout()
		asJSON := render.UseJSON(logoutJSON, false, out)

		// Refuse BEFORE anything destructive, --all included: a diverging
		// --profile with --all is a contradiction ("that one" vs "every one"),
		// and honoring --all while ignoring --profile would wipe every profile
		// for a user who named exactly one. logout also removes the org skills
		// belonging to whichever profile is ACTIVE, so it can't delete a
		// different profile's credentials without leaving the two out of step —
		// which is why $PRAXIS_PROFILE is refused here too, not only the flag.
		//
		// Compared against the PERSISTED pointer, not ResolveActiveGlobal: the
		// pointer is what owns the org skills on disk, and resolving through the
		// environment would compare $PRAXIS_PROFILE with itself. Naming the
		// profile logout would remove anyway is not a redirect, so it proceeds.
		//
		// Resolved ONCE, here, and used as the deletion target below. Letting the
		// action re-resolve was a destructive bug: the guard compared the pointer
		// while the deletion read the full chain, so `-p default logout` under
		// PRAXIS_PROFILE=acme passed the check and deleted acme.
		target := credentials.PersistedActiveName()
		if refusedExplicitProfile(out, asJSON, "logout",
			"run `praxis profiles rm %s` to remove that profile's credentials "+
				"(no skill cycle) — logout only ever removes the active profile",
			target) {
			return nil
		}

		// logout is a GLOBAL lifecycle operation, mirroring login: pin the
		// active root to home so the org-skill wipe and snapshot removal
		// always target the user-level state, never a project root that
		// happens to be in the current directory's ancestry. To leave local
		// mode, delete the repo's .praxis dir instead.
		if home, herr := paths.Dir(); herr == nil {
			restore := paths.OverrideActiveRoot(home)
			defer restore()
		}

		if logoutAll {
			if err := credentials.DeleteAll(); err != nil {
				return err
			}
			var warnings []string
			removed, err := skillinstall.UninstallByPrefix("praxis-")
			if err != nil {
				warnings = append(warnings, fmt.Sprintf("removing org skills failed: %v", err))
				if !asJSON {
					fmt.Fprintf(out, "Warning: removing org skills failed: %v\n", err)
				}
			}
			removedAgents, agErr := agentinstall.UninstallByPrefix("praxis-")
			if agErr != nil {
				warnings = append(warnings, fmt.Sprintf("removing agents failed: %v", agErr))
				if !asJSON {
					fmt.Fprintf(out, "Warning: removing agents failed: %v\n", agErr)
				}
			}
			// Best-effort: drop the manifest snapshot too — it's tied to
			// whatever profile was last active and shouldn't survive a wipe.
			if p, perr := paths.MCPTools(); perr == nil {
				_ = os.Remove(p)
			}
			if hooksRemoved, hookWarn := unwirePraxisHooks(); hookWarn != "" {
				warnings = append(warnings, hookWarn)
				if !asJSON {
					fmt.Fprintf(out, "Warning: %s\n", hookWarn)
				}
			} else if hooksRemoved && !asJSON {
				fmt.Fprintln(out, "✓ Removed use-ig hooks.")
			}
			if asJSON {
				envelope := map[string]any{
					"removed":        "all",
					"removed_skills": liteResults(removed),
					"removed_agents": agentLogoutLite(removedAgents),
				}
				if len(warnings) > 0 {
					envelope["warnings"] = warnings
				}
				return render.JSON(out, envelope)
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

		// `target` is the persisted global pointer, resolved above alongside the
		// guard — the same name, by construction. logout is global (see the home
		// pin above), so a project pointer in the cwd can't redirect it, and
		// neither can $PRAXIS_PROFILE: the pointer is what owns the credentials
		// and the org skills this removes, together.
		store, _ := credentials.Load()
		credsPresent := false
		if _, ok := store[target]; ok {
			credsPresent = true
		}

		if credsPresent {
			if err := credentials.Delete(target); err != nil {
				return err
			}
		}

		// Wipe org skills + agents. With the v0.7 single-active-profile
		// model, both always belong to the currently-active profile, so
		// this is unambiguous.
		var warnings []string
		removed, err := skillinstall.UninstallByPrefix("praxis-")
		if err != nil {
			warnings = append(warnings, fmt.Sprintf("removing org skills failed: %v", err))
			if !asJSON {
				fmt.Fprintf(out, "Warning: removing org skills failed: %v\n", err)
			}
		}
		removedAgents, agErr := agentinstall.UninstallByPrefix("praxis-")
		if agErr != nil {
			warnings = append(warnings, fmt.Sprintf("removing agents failed: %v", agErr))
			if !asJSON {
				fmt.Fprintf(out, "Warning: removing agents failed: %v\n", agErr)
			}
		}
		if p, perr := paths.MCPTools(); perr == nil {
			_ = os.Remove(p)
		}
		if hooksRemoved, hookWarn := unwirePraxisHooks(); hookWarn != "" {
			warnings = append(warnings, hookWarn)
			if !asJSON {
				fmt.Fprintf(out, "Warning: %s\n", hookWarn)
			}
		} else if hooksRemoved && !asJSON {
			fmt.Fprintln(out, "✓ Removed use-ig hooks.")
		}

		if asJSON {
			envelope := map[string]any{
				"removed":        ifTrue(credsPresent, target),
				"removed_skills": liteResults(removed),
				"removed_agents": agentLogoutLite(removedAgents),
			}
			if len(warnings) > 0 {
				envelope["warnings"] = warnings
			}
			return render.JSON(out, envelope)
		}
		if credsPresent {
			fmt.Fprintf(out, "✓ Removed profile %q.\n", target)
		} else {
			fmt.Fprintf(out, "No credentials to remove for profile %q.\n", target)
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

// unwirePraxisHooks removes praxis's hooks from every host, the counterpart to
// the wiring `praxis login` does. Never fatal; returns a warning so a JSON
// logout does not claim success while hooks remain.
func unwirePraxisHooks() (removed bool, warning string) {
	home, err := os.UserHomeDir()
	if err != nil {
		return false, fmt.Sprintf("removing praxis hooks failed: %v", err)
	}
	for _, host := range claudehooks.Hosts(home) {
		changed, err := claudehooks.Uninstall(host)
		if err != nil {
			return removed, fmt.Sprintf("removing praxis hooks failed: %v", err)
		}
		removed = removed || changed
	}
	return removed, ""
}
