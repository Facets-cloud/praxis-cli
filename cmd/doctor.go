package cmd

import (
	"fmt"
	"net/http"
	"os"
	"path/filepath"
	"time"

	"github.com/Facets-cloud/praxis-cli/internal/harness"
	"github.com/Facets-cloud/praxis-cli/internal/paths"
	"github.com/spf13/cobra"
)

func init() {
	rootCmd.AddCommand(doctorCmd)
}

var doctorCmd = &cobra.Command{
	Use:   "doctor",
	Short: "Diagnose your praxis installation",
	Long: `Run a battery of checks: ~/.praxis is writable, credentials present,
PRAXIS_API_URL set and reachable, supported AI hosts detected.`,
	RunE: func(cmd *cobra.Command, args []string) error {
		out := cmd.OutOrStdout()
		ok := true

		dir, err := paths.Ensure()
		if err != nil {
			fmt.Fprintf(out, "  ✗ ~/.praxis not writable: %v\n", err)
			ok = false
		} else {
			probe := filepath.Join(dir, ".write-probe")
			if err := os.WriteFile(probe, []byte("ok"), 0600); err != nil {
				fmt.Fprintf(out, "  ✗ ~/.praxis not writable: %v\n", err)
				ok = false
			} else {
				_ = os.Remove(probe)
				fmt.Fprintf(out, "  ✓ ~/.praxis writable (%s)\n", dir)
			}
		}

		credPath, _ := paths.Credentials()
		if _, err := os.Stat(credPath); err == nil {
			fmt.Fprintf(out, "  ✓ credentials present (%s)\n", credPath)
		} else {
			fmt.Fprintln(out, "  · credentials absent — run `praxis login` to authenticate")
		}

		apiURL := os.Getenv("PRAXIS_API_URL")
		if apiURL == "" {
			fmt.Fprintln(out, "  · PRAXIS_API_URL not set — you'll be prompted on first `praxis login`")
		} else {
			client := &http.Client{Timeout: 5 * time.Second}
			resp, err := client.Get(apiURL + "/health")
			if err != nil {
				fmt.Fprintf(out, "  ✗ PRAXIS_API_URL=%s unreachable: %v\n", apiURL, err)
				ok = false
			} else {
				_ = resp.Body.Close()
				if resp.StatusCode == 200 {
					fmt.Fprintf(out, "  ✓ PRAXIS_API_URL reachable (%s)\n", apiURL)
				} else {
					fmt.Fprintf(out, "  ✗ PRAXIS_API_URL=%s returned %s\n", apiURL, resp.Status)
					ok = false
				}
			}
		}

		fmt.Fprintln(out, "")
		fmt.Fprintln(out, "  AI host detection:")
		anyDetected := false
		for _, h := range harness.All() {
			mark := "·"
			if h.Detected {
				mark = "✓"
				anyDetected = true
			}
			fmt.Fprintf(out, "    %s %s\n", mark, h)
		}
		if !anyDetected {
			fmt.Fprintln(out, "    (no AI hosts detected — install Claude Code, Cursor, or Gemini CLI to use praxis skills)")
		}

		fmt.Fprintln(out, "")
		if ok {
			fmt.Fprintln(out, "All checks passed.")
			return nil
		}
		fmt.Fprintln(out, "Some checks failed. See messages above.")
		return fmt.Errorf("doctor reports problems")
	},
}
