package cmd

import (
	"fmt"

	"github.com/Facets-cloud/praxis-cli/internal/credentials"
	"github.com/Facets-cloud/praxis-cli/internal/harness"
	"github.com/Facets-cloud/praxis-cli/internal/render"
	"github.com/spf13/cobra"
)

var (
	initJSON bool
)

func init() {
	initCmd.Flags().BoolVar(&initJSON, "json", false, "JSON output")
	rootCmd.AddCommand(initCmd)
}

var initCmd = &cobra.Command{
	Use:   "init",
	Short: "First-run bootstrap (install skill into all detected AI hosts + report state)",
	Long: `Idempotent setup. Installs the praxis skill into every detected
AI host (so your AI knows how to use praxis) and prints the resulting
state as JSON. Designed to be run by your AI host on first contact:

  paste into your AI:
    "Run praxis init and tell me what's needed next."

The AI runs init, reads the JSON, and either reports "all set" or tells
you to run praxis login (which the AI can also do directly).`,
	Args: cobra.NoArgs,
	RunE: func(cmd *cobra.Command, args []string) error {
		out := cmd.OutOrStdout()
		asJSON := render.UseJSON(initJSON, false, out)

		// Step 1 — install-skill (idempotent; refresh if already installed).
		hosts := detectHarnesses()
		var skillResults []skillInstallationLite
		if len(hosts) > 0 {
			results, err := installSkill(skillName, hosts)
			if err != nil {
				return fmt.Errorf("install-skill: %w", err)
			}
			for _, r := range results {
				skillResults = append(skillResults, skillInstallationLite{
					Harness: r.Harness,
					Path:    r.Path,
				})
			}
		}

		// Step 2 — report state (URL + auth) so the caller can decide
		// whether to run `praxis login` next.
		active, _ := credentials.ResolveActive("")

		var nextSteps []string
		if !active.Loaded || active.Profile.Token == "" {
			nextSteps = append(nextSteps, "run `praxis login` (browser will open; user clicks once)")
		}

		state := map[string]any{
			"profile":          active.Name,
			"profile_source":   active.Source,
			"url":              active.Profile.URL,
			"logged_in":        active.Loaded && active.Profile.Token != "",
			"username":         active.Profile.Username,
			"hosts_detected":   harnessNames(hosts),
			"skills_installed": skillResults,
			"next_steps":       nextSteps,
		}

		if asJSON {
			return render.JSON(out, state)
		}

		fmt.Fprintln(out, "Praxis init")
		fmt.Fprintf(out, "  profile:   %s (source: %s)\n", active.Name, active.Source)
		fmt.Fprintf(out, "  url:       %s\n", active.Profile.URL)
		if active.Loaded && active.Profile.Token != "" {
			fmt.Fprintf(out, "  logged in: yes (%s)\n", active.Profile.Username)
		} else {
			fmt.Fprintln(out, "  logged in: no")
		}
		fmt.Fprintf(out, "  skills:    %d installed\n", len(skillResults))
		for _, r := range skillResults {
			fmt.Fprintf(out, "    ✓ %s @ %s\n", r.Harness, r.Path)
		}
		if len(nextSteps) > 0 {
			fmt.Fprintln(out, "\nNext:")
			for _, s := range nextSteps {
				fmt.Fprintf(out, "  → %s\n", s)
			}
		}
		return nil
	},
}

// skillInstallationLite is a trimmed JSON shape — drops InstalledAt because
// init is run frequently and the timestamp churn would clutter output.
type skillInstallationLite struct {
	Harness string `json:"harness"`
	Path    string `json:"path"`
}

func harnessNames(hosts []harness.Harness) []string {
	out := make([]string, 0, len(hosts))
	for _, h := range hosts {
		out = append(out, h.Name)
	}
	return out
}
