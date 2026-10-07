package cmd

import (
	"bufio"
	"fmt"
	"os"
	"strings"

	"github.com/Facets-cloud/praxis-cli/internal/clifeed"
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
	fetchLatestRelease = latestPraxisRelease
	downloadAsset      = selfupdate.Download
	fetchTextBody      = selfupdate.FetchText
	verifyChecksum     = selfupdate.VerifyChecksum
	parseChecksums     = selfupdate.ParseChecksums
	atomicReplace      = selfupdate.AtomicReplace
	selfPath           = os.Executable

	// Freshness-engine seams (see cmd/update_check.go). raptor is a second tool
	// the same engine tracks; these let tests stub its release + local version.
	fetchRaptorTag = func() (string, error) {
		if r, err := clifeed.Target("raptor"); err == nil {
			return "v" + strings.TrimPrefix(r.Target, "v"), nil
		}
		return selfupdate.LatestReleaseTagFor("Facets-cloud/raptor-releases")
	}
	raptorLocalVersion = execRaptorVersion // (version, installed) from `raptor --version`
)

func init() {
	updateCmd.Flags().BoolVarP(&updateYes, "yes", "y", false, "skip confirmation prompt")
	updateCmd.Flags().BoolVar(&updateJSON, "json", false, "JSON output (implies --yes)")
	rootCmd.AddCommand(updateCmd)
}

var updateCmd = &cobra.Command{
	Use:   "update",
	Short: "Update Praxis and Raptor to their latest releases",
	Long: `Check for a newer version of praxis. If found, download the asset for
this OS/arch, verify its checksum, and atomically replace the running binary.

Then run 'raptor upgrade', also when praxis is already current, and have the
new raptor refresh its skill. A missing raptor is installed to ~/.local/bin
first. --yes and --json also pass --yes to raptor.

Homebrew installs are left to Homebrew: this command refuses them and names
'brew upgrade --cask praxis', so brew's recorded version stays true.`,
	// raptor names the same operation `raptor upgrade`, and Homebrew names it
	// `brew upgrade`. Accept both verbs here so neither habit hits an error.
	Aliases: []string{"upgrade"},
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
		// Treat "not strictly newer" as already-latest: an equal version, or a
		// release that is older than the installed build (so we never offer a
		// downgrade). Uses the same comparator as the background update nag
		// (cmd/update_check.go) so the two can't disagree.
		if compareSemver(rel.TagName, version) <= 0 {
			if !asJSON {
				fmt.Fprintf(out, "Already on the latest Praxis version (%s).\n", current)
			}
			return finishToolUpdate(out, asJSON, autoYes, map[string]any{
				"updated": false,
				"reason":  "already_latest",
				"version": current,
			})
		}

		binAsset, sumAsset, err := selfupdate.AssetForPlatform(rel)
		if err != nil {
			return err
		}

		myPath, err := selfPath()
		if err != nil {
			return fmt.Errorf("locate self: %w", err)
		}
		// Replace the real file, not a link to it (see selfupdate.TargetPath).
		myPath, err = selfupdate.TargetPath(myPath)
		if err != nil {
			return fmt.Errorf("locate self: %w", err)
		}
		// Homebrew owns its own tree; writing into it desyncs brew's recorded
		// version from the file on disk. Refuse and name the right command.
		if _, ok := selfupdate.HomebrewCask(myPath); ok {
			if asJSON {
				return finishToolUpdate(out, asJSON, autoYes, map[string]any{
					"updated":  false,
					"reason":   "homebrew_managed",
					"version":  current,
					"latest":   latest,
					"run":      "brew upgrade --cask praxis",
					"location": myPath,
				})
			}
			fmt.Fprintf(out, "Update available: %s → %s\n", current, latest)
			fmt.Fprintf(out, "\nHomebrew installed this praxis (%s).\n", myPath)
			fmt.Fprintln(out, "Run this instead, so brew keeps track of the version:")
			fmt.Fprintln(out, "\n  brew update && brew upgrade --cask praxis")
			return finishToolUpdate(out, asJSON, autoYes, nil)
		}

		if !asJSON {
			fmt.Fprintf(out, "Update available: %s → %s\n", current, latest)
			fmt.Fprintf(out, "  release: %s\n", rel.HTMLURL)
			fmt.Fprintf(out, "  asset:   %s\n", binAsset.Name)
			fmt.Fprintf(out, "  target:  %s\n", myPath)
		}

		if !autoYes {
			fmt.Fprint(out, "\nProceed with Praxis and Raptor updates? [y/N] ")
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
		} else if d, ok := strings.CutPrefix(binAsset.Digest, "sha256:"); ok {
			expected = d
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
			fmt.Fprintln(out, "(release has no checksum — skipping verification)")
		}

		if !asJSON {
			fmt.Fprintln(out, "Installing…")
		}
		if err := atomicReplace(myPath, tmpPath); err != nil {
			return fmt.Errorf("install: %w", err)
		}

		// No skill refresh here: this process still holds the old skill text.
		// The new binary rewrites the praxis skill on its next run
		// (maybeRefreshEmbeddedSkills).

		// The binary just moved for anyone whose hooks were wired from a
		// version-stamped path; re-point them so the update does not leave a
		// hook pointing at a directory that no longer exists.
		repaired, repairWarn := repairPraxisHooks()

		if asJSON {
			payload := map[string]any{
				"updated":      true,
				"from_version": current,
				"to_version":   latest,
			}
			return finishToolUpdate(out, asJSON, true, payload)
		}

		fmt.Fprintf(out, "✓ Updated to %s.\n", latest)
		printHookRepair(out, asJSON, repaired, repairWarn)
		fmt.Fprintln(out, "  The praxis skill updates on the next praxis command. For catalog changes, run `praxis refresh-skills`.")
		return finishToolUpdate(out, asJSON, true, nil)
	},
}

// latestPraxisRelease returns the release praxis must move to: from the
// central feed, else from the GitHub API. A feed release without a verifiable
// asset for this platform also falls back to GitHub, whose release carries
// checksums.txt.
func latestPraxisRelease() (*selfupdate.Release, error) {
	if r, err := clifeed.Target("praxis"); err == nil {
		rel := feedRelease(r)
		if _, _, err := selfupdate.AssetForPlatform(rel); err == nil {
			return rel, nil
		}
	}
	return selfupdate.LatestRelease()
}

// feedRelease puts a feed release in the GitHub shape the update code reads.
// It drops an asset without a SHA-256, so the download is always verified.
func feedRelease(r clifeed.Release) *selfupdate.Release {
	tag := "v" + strings.TrimPrefix(r.Target, "v")
	rel := &selfupdate.Release{
		TagName: tag,
		HTMLURL: "https://github.com/Facets-cloud/praxis-cli/releases/tag/" + tag,
	}
	for platform, a := range r.Assets {
		goos, goarch, ok := strings.Cut(platform, "/")
		if !ok || a.URL == "" || a.SHA256 == "" {
			continue
		}
		rel.Assets = append(rel.Assets, selfupdate.Asset{
			Name:               "praxis_" + goos + "_" + goarch,
			BrowserDownloadURL: a.URL,
			Digest:             "sha256:" + a.SHA256,
		})
	}
	return rel
}
