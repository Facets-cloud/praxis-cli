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
	"github.com/Facets-cloud/praxis-cli/internal/mcpmanifest"
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
	uninstallSkill          = skillinstall.Uninstall
	listInstalledSkill      = skillinstall.List
	refreshSkills           = skillinstall.Refresh
	fetchCatalog            = skillcatalog.Fetch
	fetchAgents             = agentcatalog.Fetch
	installAgents           = agentinstall.Install
	uninstallAgentsByPrefix = agentinstall.UninstallByPrefix
	listInstalledAgents     = agentinstall.List
)

// skillName is the only skill v0.1 ships. Once the server-driven catalog
// lands, install/uninstall will accept an optional name argument to
// override this.
const skillName = "praxis"

func init() {
	rootCmd.AddCommand(installSkillCmd)
	rootCmd.AddCommand(uninstallSkillCmd)
	listSkillsCmd.Flags().BoolVar(&listSkillsJSON, "json", false,
		"JSON output (default when stdout is non-TTY)")
	rootCmd.AddCommand(listSkillsCmd)
	rootCmd.AddCommand(refreshSkillsCmd)
}

var installSkillCmd = &cobra.Command{
	Use:   "install-skill",
	Short: "Install the praxis skill into every detected AI host",
	Long: `Write the praxis SKILL.md (Agent Skills open-standard format) into
the user-scope skill directory of every detected AI host on this machine:

  Claude Code  →  ~/.claude/skills/praxis/SKILL.md
  Codex        →  ~/.agents/skills/praxis/SKILL.md
  Gemini CLI   →  ~/.gemini/skills/praxis/SKILL.md

Installations are recorded in ~/.praxis/installed.json so list-skills
and uninstall-skill can find them later. Cursor is not included — it
has no user-scope skill directory and needs per-repo install (planned
for a future release).

v0.1.x ships only the placeholder skill named "praxis"; no name argument
is needed. The real catalog lands in subsequent releases.`,
	Args: cobra.NoArgs,
	RunE: func(cmd *cobra.Command, args []string) error {
		out := cmd.OutOrStdout()

		hosts := detectHarnesses()
		if len(hosts) == 0 {
			fmt.Fprintln(out, "No supported AI hosts detected on this machine.")
			fmt.Fprintln(out, "Install Claude Code, Codex, or Gemini CLI first, then re-run.")
			return nil
		}

		results, err := installSkill(skillName, hosts)
		if err != nil {
			return err
		}
		for _, in := range results {
			fmt.Fprintf(out, "  ✓ %-12s installed at %s\n", in.Harness, in.Path)
		}
		fmt.Fprintf(out, "\nInstalled %q into %d host(s).\n", skillName, len(results))

		// Always pull org skills alongside the meta-skill so the user
		// gets the full Praxis surface. If they're not logged in we
		// skip with a hint — meta-only install is still useful by itself.
		return installCatalogSkills(out, hosts)
	},
}

// installCatalogSkills fetches the skill bundle for the active profile
// and installs every entry as a praxis-prefixed skill in each host.
//
// Not-logged-in is a soft skip — meta-only install is still useful, so
// we print a hint and continue without erroring. A real fetch failure
// (network, server error) DOES error out so the user sees the cause.
// Per-skill install failures are logged but don't abort the batch.
func installCatalogSkills(out io.Writer, hosts []harness.Harness) error {
	// Active profile is resolved via the standard chain:
	//   PRAXIS_PROFILE env → ~/.praxis/config.json (set by `praxis use`) → "default"
	active, err := credentials.ResolveActive("")
	if err != nil {
		return err
	}
	if !active.Loaded || active.Profile.Token == "" {
		fmt.Fprintf(out,
			"\nSkipping org skill catalog — not logged in for profile %q.\n"+
				"Run `praxis login` (or `praxis login --profile %s`) "+
				"and re-run install-skill to pull your org's catalog.\n",
			active.Name, active.Name)
		return nil
	}

	fmt.Fprintf(out, "\nFetching skill catalog from %s ...\n", active.Profile.URL)
	skills, err := fetchCatalog(active.Profile.URL, active.Profile.Token)
	if err != nil {
		render.PrintError(out, false,
			fmt.Sprintf("catalog fetch failed: %v", err),
			"check the profile URL and that your API key is still valid",
			exitcode.Network)
		return fmt.Errorf("catalog fetch failed: %w", err)
	}
	if len(skills) == 0 {
		fmt.Fprintln(out, "Catalog is empty for this org — nothing to install.")
		return nil
	}

	fmt.Fprintf(out, "Got %d catalog skill(s); installing as praxis-<name>:\n",
		len(skills))
	failures := 0
	for _, sk := range skills {
		prefixed := sk.PrefixedName()
		results, err := installSkillBody(prefixed, sk.RenderedContent(), hosts)
		if err != nil {
			fmt.Fprintf(out, "  ✗ %-40s failed: %v\n", prefixed, err)
			failures++
			continue
		}
		for _, in := range results {
			fmt.Fprintf(out, "  ✓ %-40s → %s\n", prefixed, in.Path)
		}
	}
	if failures > 0 {
		fmt.Fprintf(out, "\n%d catalog skill(s) failed to install.\n", failures)
	} else {
		fmt.Fprintf(out, "\nInstalled %d catalog skill(s) into %d host(s).\n",
			len(skills), len(hosts))
	}

	snapshotMCPManifest(out, active.Profile.URL, active.Profile.Token)
	return nil
}

// snapshotMCPManifest fetches the gateway's tool manifest and writes a
// snapshot to ~/.praxis/mcp-tools.json so AI hosts can grep the file
// instead of doing a live fetch on every turn. Best-effort — a failure
// here doesn't fail the parent command, since a stale or missing
// snapshot just means the AI host falls back to `praxis mcp` (live).
func snapshotMCPManifest(out io.Writer, url, token string) {
	raw, err := mcpmanifest.Fetch(url, token, mcpmanifest.DefaultTimeout)
	if err != nil {
		fmt.Fprintf(out, "\nMCP tool snapshot skipped: %v\n", err)
		return
	}
	dest, err := mcpmanifest.WriteSnapshot(raw)
	if err != nil {
		fmt.Fprintf(out, "\nMCP tool snapshot skipped: %v\n", err)
		return
	}
	fmt.Fprintf(out, "\nMCP tool snapshot written to %s\n", dest)
}

var uninstallSkillCmd = &cobra.Command{
	Use:   "uninstall-skill",
	Short: "Remove the praxis skill from every host where it's installed",
	Args:  cobra.NoArgs,
	RunE: func(cmd *cobra.Command, args []string) error {
		out := cmd.OutOrStdout()
		removed, err := uninstallSkill(skillName)
		if err != nil {
			return err
		}
		if len(removed) == 0 {
			fmt.Fprintf(out, "No installations of %q found.\n", skillName)
			return nil
		}
		for _, in := range removed {
			fmt.Fprintf(out, "  ✓ %-12s removed from %s\n", in.Harness, in.Path)
		}
		fmt.Fprintf(out, "\nUninstalled %q from %d host(s).\n", skillName, len(removed))
		return nil
	},
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
			fmt.Fprintln(out, "No skills installed. Try `praxis install-skill`.")
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
dir (~/.claude/skills, ~/.agents/skills, ~/.gemini/skills) — so the
skills apply across every repo. Pass --project to scope the install to
the current repo instead, writing to <cwd>/.claude/skills (and the
equivalent per-host project dirs). Use --project when you only want
Praxis skills active inside one repository and not globally.

Requires an existing valid login. Exits 3 if not logged in — run
` + "`praxis login`" + ` first.

For full setup including auth, use ` + "`praxis login`" + ` instead.`,
	Args: cobra.NoArgs,
	RunE: func(cmd *cobra.Command, args []string) error {
		out := cmd.OutOrStdout()
		asJSON := render.UseJSON(refreshSkillsJSON, false, out)

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

		state := runPostAuthSetup(out, asJSON, active.Profile.URL, active.Profile.Token)

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
