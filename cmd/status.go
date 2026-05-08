package cmd

import (
	"fmt"

	"github.com/Facets-cloud/praxis-cli/internal/credentials"
	"github.com/Facets-cloud/praxis-cli/internal/render"
	"github.com/Facets-cloud/praxis-cli/internal/skillinstall"
	"github.com/spf13/cobra"
)

var (
	statusJSON    bool
	statusRefresh bool
)

func init() {
	statusCmd.Flags().BoolVar(&statusJSON, "json", false, "JSON output")
	statusCmd.Flags().BoolVar(&statusRefresh, "refresh", false,
		"also call /ai-api/auth/me to verify the token is still valid")
	rootCmd.AddCommand(statusCmd)
}

var statusCmd = &cobra.Command{
	Use:   "status",
	Short: "Show active profile, auth state, installed skills",
	Long: `Read-only snapshot for AI hosts to inspect: which profile is
active, whether it has credentials, and which skills are installed.

By default this is a LOCAL-ONLY snapshot (no network calls). Pass
--refresh to also hit /ai-api/auth/me, which catches expired/revoked
tokens. The JSON output gains an "auth_check" field describing the
verification result.`,
	Args: cobra.NoArgs,
	RunE: func(cmd *cobra.Command, args []string) error {
		out := cmd.OutOrStdout()
		asJSON := render.UseJSON(statusJSON, false, out)

		active, _ := credentials.ResolveActive("")
		skills, _ := skillinstall.List()
		loggedIn := active.Loaded && active.Profile.Token != ""

		state := map[string]any{
			"profile":          active.Name,
			"profile_source":   active.Source,
			"url":              active.Profile.URL,
			"logged_in":        loggedIn,
			"username":         active.Profile.Username,
			"skills_installed": skills,
		}

		// --refresh: live token check via /auth/me. Folds in the
		// behavior of the (now deprecated) `whoami` command. Local-only
		// fields above are still returned even on auth-check failure.
		if statusRefresh && loggedIn {
			user, err := fetchAuthMe(active.Profile.URL, active.Profile.Token)
			if err != nil {
				state["auth_check"] = map[string]any{
					"ok":    false,
					"error": err.Error(),
				}
				// Don't os.Exit here — status is read-only diagnostic
				// info, callers should branch on the JSON shape.
			} else {
				state["auth_check"] = map[string]any{
					"ok":       true,
					"username": user.Email,
					"user_id":  user.UserID,
				}
				// Update username from server in case it changed.
				state["username"] = user.Email
			}
		}

		if asJSON {
			return render.JSON(out, state)
		}

		fmt.Fprintf(out, "profile:    %s (source: %s)\n", active.Name, active.Source)
		fmt.Fprintf(out, "url:        %s\n", active.Profile.URL)
		if loggedIn {
			fmt.Fprintf(out, "logged in:  yes (%s)\n", active.Profile.Username)
		} else {
			fmt.Fprintf(out, "logged in:  no — run `praxis login`\n")
		}
		if check, ok := state["auth_check"].(map[string]any); ok {
			if ok2, _ := check["ok"].(bool); ok2 {
				fmt.Fprintf(out, "auth check: ✓ token valid (%v)\n", check["username"])
			} else {
				fmt.Fprintf(out, "auth check: ✗ %v\n", check["error"])
			}
		}
		fmt.Fprintf(out, "skills:     %d installed\n", len(skills))
		for _, s := range skills {
			fmt.Fprintf(out, "  - %-30s %-12s @ %s\n", s.SkillName, s.Harness, s.Path)
		}
		return nil
	},
}
