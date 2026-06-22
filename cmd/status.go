package cmd

import (
	"fmt"
	"slices"

	"github.com/Facets-cloud/praxis-cli/internal/agentinstall"
	"github.com/Facets-cloud/praxis-cli/internal/credentials"
	"github.com/Facets-cloud/praxis-cli/internal/paths"
	"github.com/Facets-cloud/praxis-cli/internal/render"
	"github.com/Facets-cloud/praxis-cli/internal/skillinstall"
	"github.com/spf13/cobra"
)

var (
	statusJSON    bool
	statusRefresh bool
	statusFull    bool
)

func init() {
	statusCmd.Flags().BoolVar(&statusJSON, "json", false, "JSON output")
	statusCmd.Flags().BoolVar(&statusRefresh, "refresh", false,
		"also call /ai-api/auth/me to verify the token is still valid")
	statusCmd.Flags().BoolVar(&statusFull, "full", false,
		"include per-harness install detail (paths) in JSON output")
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
		agents, _ := agentinstall.List()
		loggedIn := active.Loaded && active.Profile.Token != ""

		state := map[string]any{
			"profile":        active.Name,
			"profile_source": active.Source,
			"url":            active.Profile.URL,
			"logged_in":      loggedIn,
			"username":       active.Profile.Username,
		}
		// Surface project (local) mode so AI hosts and users can see that
		// skills/receipt are scoped to this directory tree, not global.
		// Only when the active profile actually RESOLVED from the project
		// pointer — a bare/stray .praxis dir (or one whose pointer named a
		// profile we don't have) must not masquerade as local mode.
		projectRoot := ""
		if active.Source == credentials.SourceProject {
			if root, ok, _ := paths.ProjectRoot(); ok {
				projectRoot = root
				state["project_root"] = root
			}
		}
		if asJSON {
			if statusFull {
				// Same shaped schema as `list-skills --json` and
				// `praxis agents --json` — the receipt structs (with
				// their internal timestamp) stay off the wire.
				state["skills_installed"] = toSkillOutputShape(skills)
				state["agents_installed"] = toAgentOutputShape(agents)
			} else {
				// Status is read at the start of every AI conversation —
				// keep it small. Names only; per-harness paths live behind
				// --full, `praxis agents --json`, and `list-skills --json`.
				skillNames, agentNames := summarizeInstalls(skills, agents)
				state["skills_installed"] = skillNames
				state["agents_installed"] = agentNames
			}
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
		if projectRoot != "" {
			fmt.Fprintf(out, "local mode: %s\n", projectRoot)
		}
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
		fmt.Fprintf(out, "agents:     %d installed\n", len(agents))
		for _, a := range agents {
			fmt.Fprintf(out, "  - %-30s %-9s %-12s @ %s\n", a.AgentName, a.Kind, a.Harness, a.Path)
		}
		return nil
	},
}

// summarizeInstalls collapses the per-(name, harness) receipt entries into
// deduped, sorted name lists. Slices are always non-nil so JSON marshals
// `[]`, never `null`.
func summarizeInstalls(
	skills []skillinstall.Installation,
	agents []skillinstall.AgentInstallation,
) (skillNames, agentNames []string) {
	skillNames = make([]string, 0, len(skills))
	for _, s := range skills {
		skillNames = append(skillNames, s.SkillName)
	}
	agentNames = make([]string, 0, len(agents))
	for _, a := range agents {
		agentNames = append(agentNames, a.AgentName)
	}
	slices.Sort(skillNames)
	slices.Sort(agentNames)
	return slices.Compact(skillNames), slices.Compact(agentNames)
}
