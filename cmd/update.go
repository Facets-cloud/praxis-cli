package cmd

import (
	"bufio"
	"fmt"
	"os"
	"strings"

	"github.com/Facets-cloud/praxis-cli/internal/render"
	"github.com/Facets-cloud/praxis-cli/internal/selfupdate"
	"github.com/spf13/cobra"
)

var (
	updateYes  bool
	updateJSON bool
)

// Package-level seams so unit tests can stub network + filesystem deps
// without spawning a subprocess. Tests assign and restore via defer.
var (
	fetchLatestRelease = selfupdate.LatestRelease
	downloadAsset      = selfupdate.Download
	fetchTextBody      = selfupdate.FetchText
	verifyChecksum     = selfupdate.VerifyChecksum
	parseChecksums     = selfupdate.ParseChecksums
	atomicReplace      = selfupdate.AtomicReplace
)

func init() {
	updateCmd.Flags().BoolVarP(&updateYes, "yes", "y", false, "skip confirmation prompt")
	updateCmd.Flags().BoolVar(&updateJSON, "json", false, "JSON output (implies --yes)")
	rootCmd.AddCommand(updateCmd)
}

var updateCmd = &cobra.Command{
	Use:   "update",
	Short: "Self-update to the latest published release",
	Long: `Check GitHub Releases for a newer version of praxis. If found,
download the asset for this OS/arch, verify its checksum against the release's
checksums.txt, and atomically replace the running binary.

Homebrew users: prefer 'brew upgrade praxis' so brew tracks the version.`,
	RunE: func(cmd *cobra.Command, args []string) error {
		out := cmd.OutOrStdout()
		asJSON := render.UseJSON(updateJSON, false, out)
		// JSON callers (AI hosts) can't answer an interactive prompt;
		// --json implies --yes for the same reason `praxis mcp` doesn't
		// ask for confirmation when --json is set.
		autoYes := updateYes || asJSON

		rel, err := fetchLatestRelease()
		if err != nil {
			return fmt.Errorf("check for updates: %w", err)
		}

		latest := strings.TrimPrefix(rel.TagName, "v")
		current := strings.TrimPrefix(version, "v")
		if latest == current {
			if asJSON {
				return render.JSON(out, map[string]any{
					"updated": false,
					"reason":  "already_latest",
					"version": current,
				})
			}
			fmt.Fprintf(out, "Already on the latest version (%s).\n", current)
			return nil
		}

		binAsset, sumAsset, err := selfupdate.AssetForPlatform(rel)
		if err != nil {
			return err
		}

		myPath, err := os.Executable()
		if err != nil {
			return fmt.Errorf("locate self: %w", err)
		}

		if !asJSON {
			fmt.Fprintf(out, "Update available: %s → %s\n", current, latest)
			fmt.Fprintf(out, "  release: %s\n", rel.HTMLURL)
			fmt.Fprintf(out, "  asset:   %s\n", binAsset.Name)
			fmt.Fprintf(out, "  target:  %s\n", myPath)
		}

		if !autoYes {
			fmt.Fprint(out, "\nProceed? [y/N] ")
			line, _ := bufio.NewReader(os.Stdin).ReadString('\n')
			if !strings.HasPrefix(strings.ToLower(strings.TrimSpace(line)), "y") {
				fmt.Fprintln(out, "Aborted.")
				return nil
			}
		}

		var expected string
		if sumAsset != nil {
			body, err := fetchTextBody(sumAsset.BrowserDownloadURL)
			if err != nil {
				return fmt.Errorf("fetch checksums: %w", err)
			}
			expected, err = parseChecksums(body, binAsset.Name)
			if err != nil {
				return err
			}
		}

		if !asJSON {
			fmt.Fprintln(out, "Downloading…")
		}
		tmpPath, err := downloadAsset(binAsset.BrowserDownloadURL)
		if err != nil {
			return fmt.Errorf("download: %w", err)
		}
		defer os.Remove(tmpPath)

		if expected != "" {
			if !asJSON {
				fmt.Fprintln(out, "Verifying checksum…")
			}
			if err := verifyChecksum(tmpPath, expected); err != nil {
				return err
			}
		} else if !asJSON {
			fmt.Fprintln(out, "(release has no checksums.txt — skipping verification)")
		}

		if !asJSON {
			fmt.Fprintln(out, "Installing…")
		}
		if err := atomicReplace(myPath, tmpPath); err != nil {
			return fmt.Errorf("install: %w", err)
		}

		// Refresh installed skill files with the (still-running) old
		// binary's embedded content. The new binary won't take effect
		// for the meta-skill until the user re-runs login/refresh from
		// the new binary, but this catches simple in-binary content
		// changes and is also where v0.6's `refresh-skills` behavior
		// folds in. Best-effort: a refresh failure does not roll back
		// the binary update.
		refreshed, refreshErr := refreshSkills()

		if asJSON {
			payload := map[string]any{
				"updated":         true,
				"from_version":    current,
				"to_version":      latest,
				"refreshed_count": len(refreshed),
			}
			if refreshErr != nil {
				payload["refresh_error"] = refreshErr.Error()
			}
			return render.JSON(out, payload)
		}

		fmt.Fprintf(out, "✓ Updated to %s.\n", latest)
		if refreshErr != nil {
			fmt.Fprintf(out, "  ⚠ skill refresh skipped: %v\n", refreshErr)
		} else if len(refreshed) > 0 {
			fmt.Fprintf(out, "  ✓ refreshed %d installed skill(s)\n", len(refreshed))
			fmt.Fprintln(out, "\nFor catalog changes, run `praxis login` to re-fetch.")
		}
		return nil
	},
}
