package cmd

import (
	"fmt"
	"os"

	"github.com/Facets-cloud/praxis-cli/internal/paths"
	"github.com/spf13/cobra"
)

func init() {
	rootCmd.AddCommand(logoutCmd)
}

var logoutCmd = &cobra.Command{
	Use:   "logout",
	Short: "Remove the stored Praxis token",
	Long:  `Delete ~/.praxis/credentials. Future Praxis commands will be unauthenticated until you log in again.`,
	RunE: func(cmd *cobra.Command, args []string) error {
		out := cmd.OutOrStdout()
		credPath, err := paths.Credentials()
		if err != nil {
			return err
		}
		if err := os.Remove(credPath); err != nil {
			if os.IsNotExist(err) {
				fmt.Fprintln(out, "No credentials to remove (already logged out).")
				return nil
			}
			return fmt.Errorf("remove credentials: %w", err)
		}
		fmt.Fprintf(out, "Removed %s.\n", credPath)
		return nil
	},
}
