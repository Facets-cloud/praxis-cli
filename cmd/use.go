package cmd

import (
	"fmt"
	"io"
	"os"

	"github.com/Facets-cloud/praxis-cli/internal/credentials"
	"github.com/Facets-cloud/praxis-cli/internal/exitcode"
	"github.com/Facets-cloud/praxis-cli/internal/render"
	"github.com/spf13/cobra"
)

var (
	useJSON  bool
	useLocal bool
)

func init() {
	useCmd.Flags().BoolVar(&useJSON, "json", false, "JSON output")
	useCmd.Flags().BoolVar(&useLocal, "local", false,
		"pin the profile to the current directory tree (writes <cwd>/.praxis) and install its skills project-scoped")
	rootCmd.AddCommand(useCmd)
}

var useCmd = &cobra.Command{
	Use:   "use <profile>",
	Short: "Set the active profile (kubectl-style)",
	Long: `Persist the active profile so subsequent commands use it without
--profile or PRAXIS_PROFILE.

By default this writes the GLOBAL pointer (~/.praxis/config.json) — the
profile applies everywhere.

With --local it pins the profile to the CURRENT directory tree instead:
it writes a project pointer at <cwd>/.praxis/config.json and installs
that profile's skills project-scoped (<cwd>/.claude/skills, ...). Any
praxis command run from inside that tree then resolves to this profile,
and its skills no longer clash with other profiles' skills installed in
other directories. Credentials stay shared in ~/.praxis/credentials.
Local mode requires running from a directory under your home directory.

The profile must exist in ~/.praxis/credentials (created by
` + "`praxis login --profile <name>`" + `).`,
	Args: cobra.ExactArgs(1),
	RunE: func(cmd *cobra.Command, args []string) error {
		out := cmd.OutOrStdout()
		asJSON := render.UseJSON(useJSON, false, out)
		name := args[0]

		store, err := credentials.Load()
		if err != nil {
			return err
		}
		prof, ok := store[name]
		if !ok {
			render.PrintError(out, asJSON,
				fmt.Sprintf("no profile named %q", name),
				"create it with `praxis login --profile "+name+"`",
				exitcode.NoConfig)
			os.Exit(exitcode.NoConfig)
		}

		if useLocal {
			return runUseLocal(out, asJSON, name, prof)
		}

		if err := credentials.SetActive(name); err != nil {
			return err
		}

		if asJSON {
			return render.JSON(out, map[string]any{"active_profile": name, "scope": "global"})
		}
		fmt.Fprintf(out, "✓ Active profile set to %q\n", name)
		return nil
	},
}

// runUseLocal pins name to the current directory tree and installs its
// skills project-scoped. The .praxis marker SetActiveLocal writes makes the
// working directory a project root, so the post-auth setup auto-detects
// project scope. A logged-out profile (no stored token) still gets pinned,
// but the skill install is skipped with a hint.
func runUseLocal(out io.Writer, asJSON bool, name string, prof credentials.Profile) error {
	root, err := credentials.SetActiveLocal(name)
	if err != nil {
		render.PrintError(out, asJSON, err.Error(),
			"run `praxis use --local` from inside a directory under your home directory",
			exitcode.Usage)
		os.Exit(exitcode.Usage)
	}

	var state postAuthState
	installed := false
	if prof.Token != "" && prof.URL != "" {
		state = runPostAuthSetup(out, asJSON, prof.URL, prof.Token, false)
		installed = true
	} else if !asJSON {
		// No usable credentials (logged-out, or a malformed profile missing
		// its URL): pin the directory but skip the install rather than
		// firing catalog/manifest fetches at an empty base URL.
		fmt.Fprintf(out, "Profile %q has no stored token/URL — pinned the directory, but skipped skill install.\n", name)
		fmt.Fprintf(out, "Run `praxis login --profile %s` to populate it.\n", name)
	}

	if asJSON {
		return render.JSON(out, map[string]any{
			"active_profile":   name,
			"scope":            "local",
			"project_root":     root,
			"skills_installed": installed,
			"catalog_skills":   state.catalogSkills,
			"agents":           state.agents,
			"snapshot_path":    state.snapshotPath,
		})
	}
	fmt.Fprintf(out, "\n✓ Pinned profile %q to %s\n", name, root)
	return nil
}
