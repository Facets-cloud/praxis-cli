package cmd

import (
	"bufio"
	"bytes"
	"fmt"
	"io"
	"os"
	"os/exec"
	"path/filepath"
	"strings"
	"syscall"

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
	handOffUpdate      = startUpdateHelper
	runBrew            = execBrew

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

A Homebrew install is updated with Homebrew ('brew update', then 'brew upgrade
--cask praxis'), so brew's recorded version stays true. When praxis cannot run
that brew, it names the command instead.`,
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
		// version from the file on disk, so brew does the update. Without a brew
		// that can do it, name the right command.
		caskDir, isBrew := selfupdate.HomebrewCask(myPath)
		brew, token := "", ""
		if isBrew {
			brew, token = brewFor(caskDir)
		} else if !selfupdate.CanReplace(myPath) {
			return notReplaceableError(myPath)
		}
		if isBrew && brew == "" {
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
			if isBrew {
				fmt.Fprintf(out, "  with:    %s upgrade --cask %s\n", brew, token)
			} else {
				fmt.Fprintf(out, "  asset:   %s\n", binAsset.Name)
				fmt.Fprintf(out, "  target:  %s\n", myPath)
			}
		}

		if !autoYes {
			fmt.Fprint(out, "\nProceed with Praxis and Raptor updates? [y/N] ")
			line, _ := bufio.NewReader(os.Stdin).ReadString('\n')
			if !strings.HasPrefix(strings.ToLower(strings.TrimSpace(line)), "y") {
				fmt.Fprintln(out, "Aborted.")
				return nil
			}
		}

		if isBrew {
			return brewUpdate(out, asJSON, brew, token, current, latest)
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
		tmpPath, err := downloadAsset(binAsset.BrowserDownloadURL, filepath.Dir(myPath))
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
			if !selfupdate.WriteInPlaceSafe || !selfupdate.Writable(myPath) {
				return fmt.Errorf("install: %w", err)
			}
			// Returns only on failure: the helper finishes the update.
			return handOffUpdate(tmpPath, myPath, current, latest, asJSON)
		}
		return afterUpdate(out, asJSON, current, latest)
	},
}

// afterUpdate reports a replaced binary and runs the raptor step.
func afterUpdate(out io.Writer, asJSON bool, from, to string) error {
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
			"from_version": from,
			"to_version":   to,
		}
		return finishToolUpdate(out, asJSON, true, payload)
	}

	fmt.Fprintf(out, "✓ Updated to %s.\n", to)
	printHookRepair(out, asJSON, repaired, repairWarn)
	fmt.Fprintln(out, "  The praxis skill updates on the next praxis command. For catalog changes, run `praxis refresh-skills`.")
	return finishToolUpdate(out, asJSON, true, nil)
}

// notReplaceableError tells the user why praxis cannot replace its own file and
// how to fix it.
func notReplaceableError(path string) error {
	reason := "you cannot write to this file or to its folder"
	switch {
	case selfupdate.OwnedByRoot(path):
		reason = "root owns this file, and you cannot write to its folder"
	case !selfupdate.WriteInPlaceSafe && selfupdate.Writable(path):
		reason = "you cannot write to its folder, and on this system praxis cannot change its own file while it runs"
	}
	return fmt.Errorf(`praxis cannot update %s: %s.
Do one of these:
  - Run: sudo praxis update
  - Remove this copy (sudo rm %s), then install praxis again in ~/.local/bin. You can update a copy there without sudo`,
		path, reason, path)
}

// finishUpdateArg is the first argument of the temporary praxis that
// startUpdateHelper starts. Execute sends it to finishUpdate before anything
// else.
const (
	finishUpdateArg     = "__finish-update"
	updateHelperPrefix  = "praxis-upgrade-helper-"
	updateHelperArgsLen = 5 // download, binary, from, to, "json" or "text"
)

// startUpdateHelper copies the running praxis to a temporary file and execs it,
// so the copy writes the download into the binary (finishUpdate) while no
// process runs from the binary. On Apple silicon, a binary that writes its own
// file has its next runs killed by macOS for about 40 seconds. The process keeps
// its PID. It returns only on failure.
func startUpdateHelper(tmpPath, target, from, to string, asJSON bool) error {
	helper, err := os.CreateTemp("", updateHelperPrefix+"*")
	if err != nil {
		return fmt.Errorf("install: %w", err)
	}
	self, err := os.Open(target)
	if err == nil {
		_, err = io.Copy(helper, self)
		self.Close()
	}
	if cerr := helper.Close(); err == nil {
		err = cerr
	}
	if err == nil {
		err = os.Chmod(helper.Name(), 0o755)
	}
	format := "text"
	if asJSON {
		format = "json"
	}
	if err == nil {
		err = syscall.Exec(helper.Name(), []string{helper.Name(), finishUpdateArg, tmpPath, target, from, to, format}, os.Environ())
	}
	os.Remove(helper.Name())
	return fmt.Errorf("install: %w", err)
}

// finishUpdate runs in the temporary praxis that startUpdateHelper starts. It
// writes the download into the binary, removes the download and itself, and
// finishes the update. self is a copy of the old praxis, so a failed write puts
// the old binary back. It acts only as a helper file with its arguments.
func finishUpdate(out io.Writer, self string, args []string) error {
	if len(args) != updateHelperArgsLen || !strings.HasPrefix(filepath.Base(self), updateHelperPrefix) {
		return fmt.Errorf("%s is for praxis update only", finishUpdateArg)
	}
	defer os.Remove(self)
	defer os.Remove(args[0])
	if err := selfupdate.WriteInPlace(args[0], args[1]); err != nil {
		if rerr := selfupdate.WriteInPlace(self, args[1]); rerr != nil {
			return fmt.Errorf("install: %w; restoring the old binary also failed: %w", err, rerr)
		}
		return fmt.Errorf("install: %w", err)
	}
	return afterUpdate(out, args[4] == "json", args[2], args[3])
}

// brewFor returns the brew of the Homebrew prefix that staged caskDir
// (<prefix>/Caskroom/<token>/<version>) and the cask token. It returns "" when
// that brew is missing or the user cannot write to the cask's folder.
func brewFor(caskDir string) (brew, token string) {
	tokenDir := filepath.Dir(caskDir)
	brew = filepath.Join(filepath.Dir(filepath.Dir(tokenDir)), "bin", "brew")
	if info, err := os.Stat(brew); err != nil || info.Mode()&0o111 == 0 || !selfupdate.Writable(tokenDir) {
		return "", ""
	}
	return brew, filepath.Base(tokenDir)
}

// brewUpdate updates a Homebrew install with brew itself, so brew's recorded
// version stays true, then runs the raptor step. A JSON caller gets brew's
// output only when brew fails.
func brewUpdate(out io.Writer, asJSON bool, brew, token, from, to string) error {
	var log bytes.Buffer
	w := out
	if asJSON {
		w = &log
	}
	for _, args := range [][]string{{"update", "--quiet"}, {"upgrade", "--cask", token}} {
		if err := runBrew(w, brew, args...); err != nil {
			return fmt.Errorf("brew %s: %w\n%s", strings.Join(args, " "), err, log.String())
		}
	}
	if asJSON {
		return finishToolUpdate(out, asJSON, true, map[string]any{
			"updated":      true,
			"from_version": from,
			"to_version":   to,
			"via":          "homebrew",
		})
	}
	fmt.Fprintf(out, "✓ Updated with Homebrew (%s).\n", token)
	fmt.Fprintln(out, "  The praxis skill updates on the next praxis command. For catalog changes, run `praxis refresh-skills`.")
	return finishToolUpdate(out, asJSON, true, nil)
}

// execBrew runs brew without its own auto-update, cleanup and hints.
func execBrew(w io.Writer, brew string, args ...string) error {
	c := exec.Command(brew, args...)
	c.Env = append(os.Environ(), "HOMEBREW_NO_AUTO_UPDATE=1", "HOMEBREW_NO_INSTALL_CLEANUP=1", "HOMEBREW_NO_ENV_HINTS=1")
	c.Stdout, c.Stderr = w, w
	return c.Run()
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
