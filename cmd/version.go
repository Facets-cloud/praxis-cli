package cmd

import (
	"fmt"
	"runtime"

	"github.com/spf13/cobra"
)

func init() {
	rootCmd.Version = version
	rootCmd.SetVersionTemplate("praxis version " + version + "\n")
	rootCmd.AddCommand(versionCmd)
}

var versionCmd = &cobra.Command{
	Use:   "version",
	Short: "Show version, commit, and build metadata",
	Run: func(cmd *cobra.Command, args []string) {
		out := cmd.OutOrStdout()
		fmt.Fprintf(out, "praxis version %s\n", version)
		fmt.Fprintf(out, "  commit:  %s\n", commit)
		fmt.Fprintf(out, "  built:   %s\n", date)
		fmt.Fprintf(out, "  go:      %s\n", runtime.Version())
		fmt.Fprintf(out, "  os/arch: %s/%s\n", runtime.GOOS, runtime.GOARCH)
	},
}
