package cmd

import (
	"fmt"

	"github.com/Facets-cloud/praxis-cli/internal/credentials"
	"github.com/Facets-cloud/praxis-cli/internal/exitcode"
	"github.com/Facets-cloud/praxis-cli/internal/render"
	"github.com/spf13/cobra"
	"os"
)

var useJSON bool

func init() {
	useCmd.Flags().BoolVar(&useJSON, "json", false, "JSON output")
	rootCmd.AddCommand(useCmd)
}

var useCmd = &cobra.Command{
	Use:   "use <profile>",
	Short: "Set the active profile (kubectl-style)",
	Long: `Persist the active profile in ~/.praxis/config so all subsequent
commands use it without --profile or PRAXIS_PROFILE.

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
		if _, ok := store[name]; !ok {
			render.PrintError(out, asJSON,
				fmt.Sprintf("no profile named %q", name),
				"create it with `praxis login --profile "+name+"`",
				exitcode.NoConfig)
			os.Exit(exitcode.NoConfig)
		}

		if err := credentials.SetActive(name); err != nil {
			return err
		}

		if asJSON {
			return render.JSON(out, map[string]any{"active_profile": name})
		}
		fmt.Fprintf(out, "✓ Active profile set to %q\n", name)
		return nil
	},
}
