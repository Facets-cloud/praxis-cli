package cmd

import (
	"fmt"
	"io"
	"os"

	"github.com/Facets-cloud/praxis-cli/internal/agentcatalog"
	"github.com/Facets-cloud/praxis-cli/internal/agentinstall"
	"github.com/Facets-cloud/praxis-cli/internal/credentials"
	"github.com/Facets-cloud/praxis-cli/internal/exitcode"
	"github.com/Facets-cloud/praxis-cli/internal/harness"
	"github.com/Facets-cloud/praxis-cli/internal/paths"
	"github.com/Facets-cloud/praxis-cli/internal/render"
	"github.com/Facets-cloud/praxis-cli/internal/skillcatalog"
	"github.com/Facets-cloud/praxis-cli/internal/skillinstall"
	"github.com/spf13/cobra"
)

// Package-level seams so tests can stub the harness/install layer
// without touching the real filesystem.
var (
	detectHarnesses         = harness.Detected
	installSkill            = skillinstall.Install
	installSkillBody        = skillinstall.InstallWithBody
	installSkillTree        = skillinstall.InstallTreeWithBodies
	listInstalledSkill      = skillinstall.List
	refreshSkills           = skillinstall.Refresh
	fetchCatalog            = skillcatalog.Fetch
	fetchAgents             = agentcatalog.Fetch
	installAgents           = agentinstall.Install
	uninstallAgentsByPrefix = agentinstall.UninstallByPrefix
	listInstalledAgents     = agentinstall.List
)

func init() {
	listSkillsCmd.Flags().BoolVar(&listSkillsJSON, "json", false,
		"JSON output (default when stdout is non-TTY)")
	rootCmd.AddCommand(listSkillsCmd)
	rootCmd.AddCommand(refreshSkillsCmd)
}

var listSkillsJSON bool

var listSkillsCmd = &cobra.Command{
	Use:   "list-skills",
	Short: "Show installed skills and where they live",
	Args:  cobra.NoArgs,
	RunE: func(cmd *cobra.Command, args []string) error {
		out := cmd.OutOrStdout()
		asJSON := render.UseJSON(listSkillsJSON, false, out)

		entries, err := listInstalledSkill()
		if err != nil {
			return err
		}
		shaped := toSkillOutputShape(entries)
		if asJSON {
			// Always emit `[]` (never `null`) so AI host JSON parsers
			// don't have to handle two empty shapes.
			return render.JSON(out, shaped)
		}
		if len(shaped) == 0 {
			fmt.Fprintln(out, "No skills installed. Run `praxis login` to install your org's skills.")
			return nil
		}
		printSkillsPretty(out, shaped)
		return nil
	},
}

func printSkillsPretty(out io.Writer, entries []skillEntryForOutput) {
	fmt.Fprintf(out, "%-20s  %-12s  PATH\n", "SKILL", "HARNESS")
	fmt.Fprintln(out, "──────────────────────────────────────────────────────────────────────")
	for _, e := range entries {
		fmt.Fprintf(out, "%-20s  %-12s  %s\n", e.SkillName, e.Harness, e.Path)
	}
}

// skillEntryForOutput mirrors agentEntryForOutput: the stable JSON shape
// for AI hosts, without the receipt-internal timestamp.
type skillEntryForOutput struct {
	SkillName string `json:"skill_name"`
	Harness   string `json:"harness"`
	Path      string `json:"path"`
}

func toSkillOutputShape(entries []skillinstall.Installation) []skillEntryForOutput {
	out := make([]skillEntryForOutput, 0, len(entries))
	for _, e := range entries {
		out = append(out, skillEntryForOutput{
			SkillName: e.SkillName,
			Harness:   e.Harness,
			Path:      e.Path,
		})
	}
	return out
}

var (
	refreshSkillsJSON    bool
	refreshSkillsProject bool
)

func init() {
	refreshSkillsCmd.Flags().BoolVar(&refreshSkillsJSON, "json", false, "JSON output")
	refreshSkillsCmd.Flags().BoolVar(&refreshSkillsProject, "project", false,
		"install into the current repo (<cwd>/.claude/skills, ...) instead of the user-level home dir")
}

var refreshSkillsCmd = &cobra.Command{
	Use:   "refresh-skills",
	Short: "Re-fetch this profile's catalog and rewrite skill files + MCP snapshot",
	Long: `Re-run the post-login setup against the active profile, without
re-authenticating. Equivalent to ` + "`praxis login`" + ` minus the browser
flow — useful when you're already logged in and just want to:

  • pick up new skill content the org has published
  • rebuild the MCP tool snapshot after the gateway exposed a new tool
  • re-write the meta-skill body after ` + "`brew upgrade praxis`" + `
    (so AI hosts see the new on-disk content immediately)

By default this installs at USER level — the host's home-scope skill
dir (~/.claude/skills, and ~/.agents/skills shared by Codex and Gemini
CLI) — so the skills apply across every repo. Pass --project to scope
the install to the current repo instead, writing to <cwd>/.claude/skills (and the
equivalent per-host project dirs). Use --project when you only want
Praxis skills active inside one repository and not globally.

Requires an existing valid login. Exits 3 if not logged in — run
` + "`praxis login`" + ` first.

This always re-syncs the ACTIVE profile, so a global ` + "`--profile`" + ` (or
` + "`$PRAXIS_PROFILE`" + `) naming a DIFFERENT profile is REFUSED (exit 2,
nothing changed): installing one profile's skills while the pointer names
another is the exact state this flow prevents. Naming the active profile is
allowed — it asks for the re-sync that was going to happen anyway. To change
profiles, use ` + "`praxis profiles use X`" + ` — it switches and re-syncs in
one step.

For full setup including auth, use ` + "`praxis login`" + ` instead.`,
	Args: cobra.NoArgs,
	RunE: func(cmd *cobra.Command, args []string) error {
		out := cmd.OutOrStdout()
		asJSON := render.UseJSON(refreshSkillsJSON, false, out)

		// Installing profile X's skills while the pointer still names Y is the
		// one state the post-auth flow exists to prevent, so refresh-skills
		// only ever re-syncs the ACTIVE profile — by flag or by environment.
		// Switching is a switch.
		//
		// Only a DIVERGING selection is a switch, though: compared against the
		// applicable pointer (project when this tree is pinned, else global,
		// matching the root this command installs into), so naming the profile
		// it would re-sync anyway is the no-op it looks like.
		acts, err := credentials.PointerActiveName()
		if err != nil {
			return err
		}
		if refusedExplicitProfile(out, asJSON, "refresh-skills",
			"run `praxis profiles use %s` — it switches AND re-syncs the skills in one step",
			acts) {
			return nil
		}

		active, err := credentials.ResolveActive("")
		if err != nil {
			return err
		}
		if !active.Loaded || active.Profile.Token == "" {
			render.PrintError(out, asJSON,
				fmt.Sprintf("not logged in for profile %q", active.Name),
				"run `praxis login` first",
				exitcode.Auth)
			os.Exit(exitcode.Auth)
		}

		// --project pins this repo to the active profile (writes
		// <cwd>/.praxis) and scopes the install there. Otherwise the
		// install follows the resolved active root: project when we're
		// already inside a local-mode tree, else user-level.
		if refreshSkillsProject {
			root, perr := credentials.SetActiveLocal(active.Name)
			if perr != nil {
				render.PrintError(out, asJSON,
					fmt.Sprintf("cannot scope to this directory: %v", perr),
					"run `praxis refresh-skills --project` from a directory under your home directory",
					exitcode.Usage)
				osExit(exitcode.Usage)
				return nil // reached only under test (osExit stubbed)
			}
			restore := paths.OverrideActiveRoot(root)
			defer restore()
		}

		// Through the seam, like login and `profiles use` — calling
		// runPostAuthSetup directly made this the one sync path tests could not
		// intercept, so nothing pinned that refresh-skills syncs at all.
		state := postAuthSetup(out, asJSON, active.Profile.URL, active.Profile.Auth())

		// Report the *effective* scope (where files actually landed).
		scope := "user"
		if state.projectScoped {
			scope = "project"
		}
		if asJSON {
			return render.JSON(out, map[string]any{
				"profile":          active.Name,
				"scope":            scope,
				"meta_skill":       state.metaSkill,
				"removed_skills":   state.removedSkills,
				"catalog_skills":   state.catalogSkills,
				"agents":           state.agents,
				"removed_agents":   state.removedAgents,
				"snapshot_path":    state.snapshotPath,
				"snapshot_warning": state.snapshotWarning,
			})
		}
		fmt.Fprintf(out, "\n✓ Refreshed profile %q (%s-level).\n", active.Name, scope)
		return nil
	},
}
