package cmd

import (
	"bytes"
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"os"
	"os/exec"
	"path/filepath"
	"slices"
	"strings"
	"time"

	"github.com/Facets-cloud/praxis-cli/internal/clifeed"
	"github.com/Facets-cloud/praxis-cli/internal/httpclient"
	"github.com/Facets-cloud/praxis-cli/internal/paths"
	"github.com/Facets-cloud/praxis-cli/internal/selfupdate"
)

// praxis updates itself after a command when the daily check found a newer
// release. A detached praxis does the work, so the command never waits: it
// runs brew for a Homebrew install, and otherwise renames a verified download
// over the binary. It never writes into the running file (the Apple silicon
// kill), so an install that needs that write keeps the notice.
const (
	autoUpdateArg   = "__auto-update"
	autoUpdateEnv   = "PRAXIS_NO_AUTO_UPGRADE"
	autoUpdateState = "auto-update.json"
	autoUpdateClaim = "auto-update.claim"
	autoUpdateLog   = "auto-update.log"
	// autoUpdateRetryAfter is the pause after an attempt at a target that did
	// not bring this binary to it.
	autoUpdateRetryAfter = 24 * time.Hour
)

var (
	startAutoUpdate       = startBackgroundUpdate
	autoUpdateAsRoot      = func() bool { return os.Geteuid() == 0 }
	autoUpdateInContainer = clifeed.InContainer
)

type autoUpdateResult struct {
	From   string    `json:"from,omitempty"`
	Target string    `json:"target"`
	Result string    `json:"result"` // "upgraded" or "failed"
	Error  string    `json:"error,omitempty"`
	At     time.Time `json:"at"`
	Shown  bool      `json:"shown,omitempty"`
}

func init() { clifeed.AutoUpgradeState = autoUpdateSnapshot }

// autoUpdateMode returns how the binary at path updates itself ("rename" or
// "brew"), or "off" and the reason.
func autoUpdateMode(path string) (mode, reason string) {
	switch {
	case isDevBuild(version):
		return "off", "dev"
	case os.Getenv(autoUpdateEnv) != "" || os.Getenv("PRAXIS_NO_UPDATE_CHECK") != "":
		return "off", "env"
	case httpclient.CIName() != "":
		return "off", "ci"
	case autoUpdateInContainer():
		return "off", "container"
	case autoUpdateAsRoot():
		// sudo keeps HOME on macOS, so root would own files in the user's home.
		return "off", "root"
	case path == "":
		return "off", "folder"
	}
	if caskDir, ok := selfupdate.HomebrewCask(path); ok {
		if brew, _ := brewFor(caskDir); brew == "" {
			return "off", "brew"
		}
		return "brew", ""
	}
	if !selfupdate.Writable(filepath.Dir(path)) {
		return "off", "folder"
	}
	return "rename", ""
}

// autoUpdatePath is the real file of the running praxis, or "".
func autoUpdatePath() string {
	p, err := selfPath()
	if err != nil {
		return ""
	}
	if p, err = selfupdate.TargetPath(p); err != nil {
		return ""
	}
	return p
}

// cachedPraxisTarget is the newest praxis release the daily check stored.
func cachedPraxisTarget() string {
	c, err := readFreshnessCache()
	if err != nil {
		return ""
	}
	return strings.TrimPrefix(c["praxis"].LatestVersion, "v")
}

// maybeAutoUpdate starts the detached update when the cache names a newer
// release and every gate passes. One process starts it (the claim file).
func maybeAutoUpdate(args []string) {
	switch firstPositional(args) {
	case "completion", "__complete", "git-credential", "hook", "setup", "update", "upgrade", "version",
		autoUpdateArg, finishUpdateArg:
		return
	}
	if skipUpdateCheck(args) {
		return
	}
	target := cachedPraxisTarget()
	if target == "" || compareSemver(target, version) <= 0 {
		return
	}
	path := autoUpdatePath()
	if mode, _ := autoUpdateMode(path); mode == "off" {
		return
	}
	if last, err := readAutoUpdate(); err == nil && last.Target == target && time.Since(last.At) < autoUpdateRetryAfter {
		return
	}
	dir, err := paths.Dir()
	if err != nil {
		return
	}
	claim := filepath.Join(dir, autoUpdateClaim)
	if !claimSetup(claim, time.Now()) {
		return
	}
	if startAutoUpdate(path, dir) != nil {
		_ = os.Remove(claim)
	}
}

// startBackgroundUpdate runs `praxis __auto-update` in its own session, with
// its output in ~/.praxis/auto-update.log.
func startBackgroundUpdate(path, dir string) error {
	log, err := os.Create(filepath.Join(dir, autoUpdateLog))
	if err != nil {
		return err
	}
	defer log.Close()
	c := exec.Command(path, autoUpdateArg)
	c.Stdout, c.Stderr = log, log
	detach(c)
	if err := c.Start(); err != nil {
		return err
	}
	return c.Process.Release()
}

// runAutoUpdate is the detached praxis. It runs from the file at the path now,
// so after an earlier update it is the new version and does nothing.
func runAutoUpdate(out io.Writer) error {
	dir, err := paths.Dir()
	if err != nil {
		return err
	}
	defer os.Remove(filepath.Join(dir, autoUpdateClaim))
	target := cachedPraxisTarget()
	if target == "" || compareSemver(target, version) <= 0 {
		fmt.Fprintf(out, "praxis %s is not older than %q; nothing to do\n", version, target)
		return nil
	}
	path := autoUpdatePath()
	mode, reason := autoUpdateMode(path)
	switch mode {
	case "brew":
		err = autoBrewUpdate(out, path, target)
	case "rename":
		err = autoRenameUpdate(path)
	default:
		err = fmt.Errorf("automatic update is off: %s", reason)
	}
	res := autoUpdateResult{From: strings.TrimPrefix(version, "v"), Target: target, Result: "upgraded", At: time.Now().UTC()}
	if err != nil {
		res.Result, res.Error = "failed", err.Error()
	}
	if werr := writeAutoUpdate(res); err == nil {
		err = werr
	}
	fmt.Fprintf(out, "praxis %s → %s (%s): %s\n", res.From, target, mode, res.Result)
	return err
}

// autoBrewUpdate runs the prefix's brew. brew exits 0 also when its tap does
// not have the target yet, so brew must then list the target as installed.
func autoBrewUpdate(out io.Writer, path, target string) error {
	caskDir, _ := selfupdate.HomebrewCask(path)
	brew, token := brewFor(caskDir)
	for _, args := range [][]string{{"update", "--quiet"}, {"upgrade", "--cask", token}} {
		if err := runBrew(out, brew, args...); err != nil {
			return fmt.Errorf("brew %s: %w", strings.Join(args, " "), err)
		}
	}
	var listed bytes.Buffer
	if err := runBrew(&listed, brew, "list", "--cask", "--versions", token); err != nil {
		return fmt.Errorf("brew list: %w", err)
	}
	if !brewListed(listed.String(), token, target) {
		return fmt.Errorf("brew did not install %s: %s", target, strings.TrimSpace(listed.String()))
	}
	return nil
}

// brewListed reports whether `brew list --cask --versions` output names target
// on the line of token ("praxis 2.2.0"). brew's warnings share the output.
func brewListed(out, token, target string) bool {
	for _, line := range strings.Split(out, "\n") {
		if f := strings.Fields(line); len(f) > 1 && f[0] == token && slices.Contains(f[1:], target) {
			return true
		}
	}
	return false
}

// autoRenameUpdate downloads the release beside path, checks its SHA-256 and
// renames it over path. A release without a SHA-256 is refused.
func autoRenameUpdate(path string) error {
	rel, err := fetchLatestRelease()
	if err != nil {
		return err
	}
	// The release source can differ from the daily check's; never move down.
	if compareSemver(rel.TagName, version) <= 0 {
		return fmt.Errorf("the release source offers %s, which is not newer than %s", rel.TagName, version)
	}
	bin, sums, err := selfupdate.AssetForPlatform(rel)
	if err != nil {
		return err
	}
	expected, err := expectedChecksum(bin, sums)
	if err != nil {
		return err
	}
	if expected == "" {
		return errors.New("the release has no SHA-256")
	}
	tmp, err := downloadAsset(bin.BrowserDownloadURL, filepath.Dir(path))
	if err != nil {
		return err
	}
	defer os.Remove(tmp)
	if err := verifyChecksum(tmp, expected); err != nil {
		return err
	}
	return atomicReplace(path, tmp)
}

func readAutoUpdate() (autoUpdateResult, error) {
	var r autoUpdateResult
	dir, err := paths.Dir()
	if err != nil {
		return r, err
	}
	b, err := os.ReadFile(filepath.Join(dir, autoUpdateState))
	if err != nil {
		return r, err
	}
	return r, json.Unmarshal(b, &r)
}

func writeAutoUpdate(r autoUpdateResult) error {
	dir, err := paths.Dir()
	if err != nil {
		return err
	}
	b, err := json.Marshal(r)
	if err != nil {
		return err
	}
	if err := os.MkdirAll(dir, 0o755); err != nil {
		return err
	}
	return os.WriteFile(filepath.Join(dir, autoUpdateState), b, 0o644)
}

// autoUpdateNotice is the one line about the update that this binary came
// from. It prints once.
func autoUpdateNotice() string {
	r, err := readAutoUpdate()
	if err != nil || r.Shown || r.Result != "upgraded" || r.Target != strings.TrimPrefix(version, "v") {
		return ""
	}
	r.Shown = true
	if writeAutoUpdate(r) != nil {
		return ""
	}
	return fmt.Sprintf("praxis updated itself: %s → %s. Set %s=1 to stop automatic updates.", r.From, r.Target, autoUpdateEnv)
}

// autoUpdateHandles reports whether the automatic update takes care of latest,
// so the "update available" box is not necessary.
func autoUpdateHandles(latest string) bool {
	if mode, _ := autoUpdateMode(autoUpdatePath()); mode == "off" {
		return false
	}
	r, err := readAutoUpdate()
	return err != nil || r.Result != "failed" || r.Target != strings.TrimPrefix(latest, "v")
}

// autoUpdateSnapshot is the auto_upgrade object of the census snapshot.
func autoUpdateSnapshot() *clifeed.AutoUpgrade {
	mode, reason := autoUpdateMode(autoUpdatePath())
	a := &clifeed.AutoUpgrade{Mode: mode, OffReason: reason}
	if r, err := readAutoUpdate(); err == nil {
		a.Target, a.Result, a.Error, a.At = r.Target, r.Result, r.Error, r.At.UTC().Format(time.RFC3339)
	}
	return a
}
