package cmd

import (
	"bufio"
	"fmt"
	"os"
	"strings"

	"github.com/Facets-cloud/praxis-cli/internal/selfupdate"
	"github.com/spf13/cobra"
)

var updateYes bool

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

		rel, err := fetchLatestRelease()
		if err != nil {
			return fmt.Errorf("check for updates: %w", err)
		}

		latest := strings.TrimPrefix(rel.TagName, "v")
		current := strings.TrimPrefix(version, "v")
		if latest == current {
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

		fmt.Fprintf(out, "Update available: %s → %s\n", current, latest)
		fmt.Fprintf(out, "  release: %s\n", rel.HTMLURL)
		fmt.Fprintf(out, "  asset:   %s\n", binAsset.Name)
		fmt.Fprintf(out, "  target:  %s\n", myPath)

		if !updateYes {
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

		fmt.Fprintln(out, "Downloading…")
		tmpPath, err := downloadAsset(binAsset.BrowserDownloadURL)
		if err != nil {
			return fmt.Errorf("download: %w", err)
		}
		defer os.Remove(tmpPath)

		if expected != "" {
			fmt.Fprintln(out, "Verifying checksum…")
			if err := verifyChecksum(tmpPath, expected); err != nil {
				return err
			}
		} else {
			fmt.Fprintln(out, "(release has no checksums.txt — skipping verification)")
		}

		fmt.Fprintln(out, "Installing…")
		if err := atomicReplace(myPath, tmpPath); err != nil {
			return fmt.Errorf("install: %w", err)
		}

		fmt.Fprintf(out, "✓ Updated to %s.\n", latest)
		return nil
	},
}
