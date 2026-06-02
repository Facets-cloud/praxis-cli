package cmd

import (
	"fmt"
	"io"
	"text/tabwriter"

	"github.com/Facets-cloud/praxis-cli/internal/credentials"
	"github.com/Facets-cloud/praxis-cli/internal/render"
	"github.com/spf13/cobra"
)

var (
	profilesJSON    bool
	profilesRefresh bool
)

func init() {
	profilesCmd.Flags().BoolVar(&profilesJSON, "json", false, "JSON output")
	profilesCmd.Flags().BoolVar(&profilesRefresh, "refresh", false,
		"also call /ai-api/auth/me for each logged-in profile to verify its token")
	rootCmd.AddCommand(profilesCmd)
}

// authCheckResult is the per-profile live-verification outcome, populated
// only under --refresh. Omitted entirely for profiles with no token.
type authCheckResult struct {
	OK       bool   `json:"ok"`
	Username string `json:"username,omitempty"`
	Error    string `json:"error,omitempty"`
}

// profileEntry is one row of the profiles listing.
type profileEntry struct {
	Name      string           `json:"name"`
	URL       string           `json:"url"`
	Username  string           `json:"username"`
	Active    bool             `json:"active"`
	LoggedIn  bool             `json:"logged_in"`
	AuthCheck *authCheckResult `json:"auth_check,omitempty"`
}

// profilesOutput is the top-level JSON shape so AI hosts can dispatch on
// active_profile and iterate profiles without parsing English.
type profilesOutput struct {
	ActiveProfile string         `json:"active_profile"`
	Profiles      []profileEntry `json:"profiles"`
}

var profilesCmd = &cobra.Command{
	Use:   "profiles",
	Short: "List all profiles and their login state",
	Long: `List every profile in ~/.praxis/credentials with its URL, username,
active-profile marker, and login state.

By default this is a LOCAL-ONLY snapshot (no network calls): a profile is
"logged in" when it has a stored token. Pass --refresh to additionally hit
/ai-api/auth/me for each logged-in profile, catching expired/revoked tokens.
A failing check on one profile is reported inline and does not abort the
listing.

The active profile (the one used when no --profile/PRAXIS_PROFILE is given)
is marked with "*" and reported as active_profile in JSON output.`,
	Args: cobra.NoArgs,
	RunE: func(cmd *cobra.Command, args []string) error {
		out := cmd.OutOrStdout()
		asJSON := render.UseJSON(profilesJSON, false, out)

		// Resolve the active profile via the standard chain so the marker
		// matches what every other command would actually use.
		active, err := credentials.ResolveActive("")
		if err != nil {
			return err
		}
		store, err := credentials.Load()
		if err != nil {
			return err
		}
		names, err := credentials.List() // sorted: "default" first, then alpha
		if err != nil {
			return err
		}

		entries := make([]profileEntry, 0, len(names))
		for _, name := range names {
			p := store[name]
			e := profileEntry{
				Name:     name,
				URL:      p.URL,
				Username: p.Username,
				Active:   name == active.Name,
				LoggedIn: p.Token != "",
			}
			// --refresh: live-verify only profiles that actually hold a
			// token. A per-profile failure is recorded, never fatal — the
			// listing must stay complete even with one revoked token.
			if profilesRefresh && e.LoggedIn {
				if user, ferr := fetchAuthMe(p.URL, p.Token); ferr != nil {
					e.AuthCheck = &authCheckResult{OK: false, Error: ferr.Error()}
				} else {
					e.AuthCheck = &authCheckResult{OK: true, Username: user.Email}
				}
			}
			entries = append(entries, e)
		}

		result := profilesOutput{ActiveProfile: active.Name, Profiles: entries}
		if asJSON {
			return render.JSON(out, result)
		}
		return renderProfilesText(out, result)
	},
}

// renderProfilesText prints the human-readable table. Kept separate so the
// RunE body stays focused on data assembly.
func renderProfilesText(out io.Writer, result profilesOutput) error {
	if len(result.Profiles) == 0 {
		fmt.Fprintln(out, "No profiles configured.")
		fmt.Fprintln(out, "Run `praxis login` to create one.")
		return nil
	}

	tw := tabwriter.NewWriter(out, 0, 2, 2, ' ', 0)
	fmt.Fprintln(tw, "ACTIVE\tPROFILE\tURL\tUSERNAME\tLOGIN")
	for _, p := range result.Profiles {
		marker := ""
		if p.Active {
			marker = "*"
		}
		login := loginCell(p)
		fmt.Fprintf(tw, "%s\t%s\t%s\t%s\t%s\n",
			marker, p.Name, dashIfEmpty(p.URL), dashIfEmpty(p.Username), login)
	}
	return tw.Flush()
}

// loginCell renders the LOGIN column, folding in the --refresh verdict when
// present so a stored-but-revoked token reads as "no (revoked)" not "yes".
func loginCell(p profileEntry) string {
	if !p.LoggedIn {
		return "no"
	}
	if p.AuthCheck != nil {
		if p.AuthCheck.OK {
			return "yes (verified)"
		}
		return "no (token invalid)"
	}
	return "yes"
}

func dashIfEmpty(s string) string {
	if s == "" {
		return "-"
	}
	return s
}
