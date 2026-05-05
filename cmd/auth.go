package cmd

import (
	"fmt"
	"os"

	"github.com/Facets-cloud/praxis-cli/internal/paths"
	"github.com/spf13/cobra"
)

func init() {
	loginCmd.Flags().String("url", "", "PRAXIS_API_URL to log into (overrides env + saved config)")
	rootCmd.AddCommand(loginCmd)
	rootCmd.AddCommand(logoutCmd)
	rootCmd.AddCommand(whoamiCmd)
}

var loginCmd = &cobra.Command{
	Use:   "login",
	Short: "Connect your Praxis account",
	Long: `Open a browser window to authenticate against your Praxis cloud
deployment. The resulting token is stored in ~/.praxis/credentials.

If --url is omitted and PRAXIS_API_URL is unset, you'll be prompted
interactively for your deployment URL (e.g. https://acme.askpraxis.ai).`,
	RunE: func(cmd *cobra.Command, args []string) error {
		notImplemented(3, "praxis login (OAuth flow)")
		return nil
	},
}

var logoutCmd = &cobra.Command{
	Use:   "logout",
	Short: "Remove the stored Praxis token",
	Long:  `Delete ~/.praxis/credentials. You'll need to run 'praxis login' to authenticate again.`,
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

var whoamiCmd = &cobra.Command{
	Use:   "whoami",
	Short: "Show your authenticated identity",
	Long:  `Print user, org, role, and token expiry as resolved by the Praxis server.`,
	RunE: func(cmd *cobra.Command, args []string) error {
		notImplemented(3, "praxis whoami (server identity lookup)")
		return nil
	},
}
