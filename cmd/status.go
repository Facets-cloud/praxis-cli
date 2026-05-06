package cmd

import (
	"fmt"

	"github.com/Facets-cloud/praxis-cli/internal/credentials"
	"github.com/Facets-cloud/praxis-cli/internal/render"
	"github.com/Facets-cloud/praxis-cli/internal/skillinstall"
	"github.com/spf13/cobra"
)

var (
	statusProfile string
	statusJSON    bool
)

func init() {
	statusCmd.Flags().StringVar(&statusProfile, "profile", "", "use this profile (default: active)")
	statusCmd.Flags().BoolVar(&statusJSON, "json", false, "JSON output")
	rootCmd.AddCommand(statusCmd)
}

var statusCmd = &cobra.Command{
	Use:   "status",
	Short: "Show active profile, auth state, installed skills",
	Long: `Read-only snapshot for AI hosts to inspect: which profile is
active, whether it has credentials, and which skills are installed in
which AI hosts. Does NOT call the server.`,
	Args: cobra.NoArgs,
	RunE: func(cmd *cobra.Command, args []string) error {
		out := cmd.OutOrStdout()
		asJSON := render.UseJSON(statusJSON, false, out)

		active, _ := credentials.ResolveActive(statusProfile)
		skills, _ := skillinstall.List()

		state := map[string]any{
			"profile":          active.Name,
			"profile_source":   active.Source,
			"url":              active.Profile.URL,
			"logged_in":        active.Loaded && active.Profile.Token != "",
			"username":         active.Profile.Username,
			"skills_installed": skills,
		}

		if asJSON {
			return render.JSON(out, state)
		}

		fmt.Fprintf(out, "profile:    %s (source: %s)\n", active.Name, active.Source)
		fmt.Fprintf(out, "url:        %s\n", active.Profile.URL)
		if active.Loaded && active.Profile.Token != "" {
			fmt.Fprintf(out, "logged in:  yes (%s)\n", active.Profile.Username)
		} else {
			fmt.Fprintf(out, "logged in:  no — run `praxis login`\n")
		}
		fmt.Fprintf(out, "skills:     %d installed\n", len(skills))
		for _, s := range skills {
			fmt.Fprintf(out, "  - %s @ %s\n", s.Harness, s.Path)
		}
		return nil
	},
}
