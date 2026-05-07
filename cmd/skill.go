package cmd

import (
	"fmt"
	"io"

	"github.com/Facets-cloud/praxis-cli/internal/credentials"
	"github.com/Facets-cloud/praxis-cli/internal/exitcode"
	"github.com/Facets-cloud/praxis-cli/internal/harness"
	"github.com/Facets-cloud/praxis-cli/internal/render"
	"github.com/Facets-cloud/praxis-cli/internal/skillcatalog"
	"github.com/Facets-cloud/praxis-cli/internal/skillinstall"
	"github.com/spf13/cobra"
)

// Package-level seams so tests can stub the harness/install layer
// without touching the real filesystem.
var (
	detectHarnesses    = harness.Detected
	installSkill       = skillinstall.Install
	installSkillBody   = skillinstall.InstallWithBody
	uninstallSkill     = skillinstall.Uninstall
	listInstalledSkill = skillinstall.List
	refreshSkills      = skillinstall.Refresh
	fetchCatalog       = skillcatalog.Fetch
)

// skillName is the only skill v0.1 ships. Once the server-driven catalog
// lands, install/uninstall will accept an optional name argument to
// override this.
const skillName = "praxis"

func init() {
	rootCmd.AddCommand(installSkillCmd)
	rootCmd.AddCommand(uninstallSkillCmd)
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
	return nil
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

var listSkillsCmd = &cobra.Command{
	Use:   "list-skills",
	Short: "Show installed skills and where they live",
	Args:  cobra.NoArgs,
	RunE: func(cmd *cobra.Command, args []string) error {
		out := cmd.OutOrStdout()
		entries, err := listInstalledSkill()
		if err != nil {
			return err
		}
		if len(entries) == 0 {
			fmt.Fprintln(out, "No skills installed. Try `praxis install-skill`.")
			return nil
		}
		fmt.Fprintf(out, "%-20s  %-12s  PATH\n", "SKILL", "HARNESS")
		fmt.Fprintln(out, "──────────────────────────────────────────────────────────────────────")
		for _, e := range entries {
			fmt.Fprintf(out, "%-20s  %-12s  %s\n", e.SkillName, e.Harness, e.Path)
		}
		return nil
	},
}

var refreshSkillsCmd = &cobra.Command{
	Use:   "refresh-skills",
	Short: "Re-write installed SKILL.md files with current content",
	Long: `Re-write the SKILL.md file for every installed skill using the
current binary's catalog content. Useful after manual edits to the
installed files, or to confirm the catalog hasn't changed under you.

praxis update calls this automatically after replacing the binary.`,
	Args: cobra.NoArgs,
	RunE: func(cmd *cobra.Command, args []string) error {
		out := cmd.OutOrStdout()
		refreshed, err := refreshSkills()
		if err != nil {
			return err
		}
		if len(refreshed) == 0 {
			fmt.Fprintln(out, "No skills installed — nothing to refresh.")
			return nil
		}
		for _, in := range refreshed {
			fmt.Fprintf(out, "  ✓ %-12s refreshed at %s\n", in.Harness, in.Path)
		}
		fmt.Fprintf(out, "\nRefreshed %d skill installation(s).\n", len(refreshed))
		return nil
	},
}
