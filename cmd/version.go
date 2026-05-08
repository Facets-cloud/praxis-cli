package cmd

import (
	"fmt"
	"runtime"

	"github.com/Facets-cloud/praxis-cli/internal/render"
	"github.com/spf13/cobra"
)

var versionJSON bool

func init() {
	rootCmd.Version = version
	rootCmd.SetVersionTemplate("praxis version " + version + "\n")
	versionCmd.Flags().BoolVar(&versionJSON, "json", false, "JSON output")
	rootCmd.AddCommand(versionCmd)
}

var versionCmd = &cobra.Command{
	Use:   "version",
	Short: "Show version, commit, and build metadata",
	RunE: func(cmd *cobra.Command, args []string) error {
		out := cmd.OutOrStdout()
		asJSON := render.UseJSON(versionJSON, false, out)
		if asJSON {
			return render.JSON(out, map[string]any{
				"version": version,
				"commit":  commit,
				"built":   date,
				"go":      runtime.Version(),
				"os":      runtime.GOOS,
				"arch":    runtime.GOARCH,
			})
		}
		fmt.Fprintf(out, "praxis version %s\n", version)
		fmt.Fprintf(out, "  commit:  %s\n", commit)
		fmt.Fprintf(out, "  built:   %s\n", date)
		fmt.Fprintf(out, "  go:      %s\n", runtime.Version())
		fmt.Fprintf(out, "  os/arch: %s/%s\n", runtime.GOOS, runtime.GOARCH)
		return nil
	},
}
