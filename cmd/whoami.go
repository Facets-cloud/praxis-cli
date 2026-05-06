package cmd

import (
	"encoding/json"
	"fmt"
	"net/http"
	"os"
	"time"

	"github.com/Facets-cloud/praxis-cli/internal/credentials"
	"github.com/Facets-cloud/praxis-cli/internal/exitcode"
	"github.com/Facets-cloud/praxis-cli/internal/render"
	"github.com/spf13/cobra"
)

var (
	whoamiProfile string
	whoamiJSON    bool
)

func init() {
	whoamiCmd.Flags().StringVar(&whoamiProfile, "profile", "", "use this profile (default: active)")
	whoamiCmd.Flags().BoolVar(&whoamiJSON, "json", false, "JSON output")
	rootCmd.AddCommand(whoamiCmd)
}

var whoamiCmd = &cobra.Command{
	Use:   "whoami",
	Short: "Show authenticated user for the active profile",
	Args:  cobra.NoArgs,
	RunE: func(cmd *cobra.Command, args []string) error {
		out := cmd.OutOrStdout()
		asJSON := render.UseJSON(whoamiJSON, false, out)

		active, err := credentials.ResolveActive(whoamiProfile)
		if err != nil {
			return err
		}
		if !active.Loaded || active.Profile.Token == "" {
			render.PrintError(out, asJSON,
				fmt.Sprintf("no credentials for profile %q", active.Name),
				"run `praxis login` (or `praxis login --profile "+active.Name+"`)",
				exitcode.Auth)
			os.Exit(exitcode.Auth)
		}

		// Live verify against /auth/me. Catches expired/revoked tokens.
		user, err := fetchAuthMe(active.Profile.URL, active.Profile.Token)
		if err != nil {
			render.PrintError(out, asJSON,
				fmt.Sprintf("token check failed: %v", err),
				"the token may have been revoked; run `praxis login --profile "+active.Name+"`",
				exitcode.Auth)
			os.Exit(exitcode.Auth)
		}

		if asJSON {
			return render.JSON(out, map[string]any{
				"profile":  active.Name,
				"source":   active.Source,
				"url":      active.Profile.URL,
				"username": user.Email,
				"user_id":  user.UserID,
				"role":     "", // server includes more in a future enrichment
			})
		}
		fmt.Fprintf(out, "user:    %s\n", user.Email)
		fmt.Fprintf(out, "profile: %s (source: %s)\n", active.Name, active.Source)
		fmt.Fprintf(out, "url:     %s\n", active.Profile.URL)
		return nil
	},
}

// httpHelpers (whoami doesn't need anything beyond fetchAuthMe defined in login.go)
var _ = http.MethodGet
var _ = json.Marshal
var _ = time.Second
