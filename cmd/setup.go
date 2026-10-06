package cmd

import (
	"errors"
	"fmt"
	"io"
	"os"
	"path/filepath"
	"strings"

	"github.com/Facets-cloud/praxis-cli/internal/claudehooks"
	"github.com/Facets-cloud/praxis-cli/internal/harness"
	"github.com/Facets-cloud/praxis-cli/internal/paths"
	"github.com/Facets-cloud/praxis-cli/internal/render"
	"github.com/Facets-cloud/praxis-cli/internal/skillinstall"
	"github.com/spf13/cobra"
)

// `praxis setup` also re-points hooks an older praxis wired from a path that an
// upgrade has since deleted — see repairPraxisHooks — and installs a missing
// raptor. First-run stays offline and does not.
//
// `praxis setup` and the first-run auto-install land the embedded praxis skill
// into the user's AI host the moment praxis is installed — WITHOUT a login, and
// retire the embedded skills it replaced. This solves the bootstrap
// chicken-and-egg: skills otherwise only appear after `praxis login`, so a
// freshly-installed praxis is invisible to the host and nothing tells it to log
// in. The skill is embedded (no network, no credentials), so this works offline
// and pre-auth. The brew hook runs setup on every upgrade, so this is also how
// a brew upgrade refreshes the skill.
//
// `setup` is hidden: it is the primitive the Homebrew post-install hook and
// first-run call. The user-facing GTM surface is the installed skill itself; the
// documented command surface stays unchanged. (`init`, the obvious name, was
// removed in the major-version cleanup and must not return — see root_test.go.)

// bootstrapMarker is bumped when the bootstrap skill content changes enough to
// warrant a one-time re-install on machines that already ran first-run.
const bootstrapMarker = ".bootstrap-v2"

var setupJSON bool

func init() {
	setupCmd.Flags().BoolVar(&setupJSON, "json", false, "JSON output")
	rootCmd.AddCommand(setupCmd)
}

var setupCmd = &cobra.Command{
	Use:    "setup",
	Short:  "Install the Praxis skill into your AI host (no login needed)",
	Hidden: true, // invoked by the brew post-install hook + first-run, not by hand
	Long: `Install the praxis skill into every detected AI host (Claude Code, Codex,
Gemini CLI) so your assistant knows what Praxis by Facets does, where to sign
up, and how to log in — before you authenticate. The embedded skills it
replaced (praxis-getting-started, praxis-memory, praxis-onboarding, use-ig) are
removed; changed copies are backed up under ~/.praxis/backups.

It also re-points any hook an older praxis wired from a path that an upgrade
has since deleted. It installs the Raptor CLI to ~/.local/bin when it is
missing and upgrades it otherwise, then has raptor refresh its own skill. No
hook is added and no credentials are required. This runs via the Homebrew
post-install hook, also on every upgrade; first use installs only the skill,
offline.

  Next: praxis login --url https://<your-account-id>.console.facets.cloud`,
	Args: cobra.NoArgs,
	RunE: func(cmd *cobra.Command, args []string) error {
		out := cmd.OutOrStdout()
		asJSON := render.UseJSON(setupJSON, false, out)
		repaired, repairWarn := repairPraxisHooks()
		printHookRepair(out, asJSON, repaired, repairWarn)
		raptor, raptorErr := prepareRaptor(out, asJSON)
		// The brew hook runs setup on every `brew upgrade`, so an existing
		// raptor is upgraded here too. A raptor installed just now is current.
		var upgrade *raptorUpgradeResult
		if raptor.Path != "" && !raptor.Installed {
			up, upErr := updateRaptor(out, asJSON, true)
			upgrade = &up
			raptorErr = errors.Join(raptorErr, upErr)
		}
		n, err := installBootstrapSkills(out, asJSON)
		if err != nil {
			return err
		}
		var raptorSkills []skillInstallationLite
		if raptor.Path != "" {
			var skillErr error
			raptorSkills, skillErr = refreshRaptorSkills(out, asJSON)
			raptorErr = errors.Join(raptorErr, skillErr)
		}
		if n > 0 {
			markBootstrapDone() // mark ONLY after a real install; a no-host run
			// stays retryable so first-run installs once a host appears.
		}
		if asJSON {
			payload := map[string]any{"installed": n, "raptor_binary": raptor, "raptor_skills": raptorSkills}
			if upgrade != nil {
				payload["raptor_upgrade"] = upgrade
			}
			if raptorErr != nil {
				payload["raptor_warning"] = raptorErr.Error()
			}
			return render.JSON(out, payload)
		}
		if n > 0 {
			fmt.Fprintf(out, "Installed the praxis skill into %d host target(s).\n", n)
			fmt.Fprintln(out, "Next: praxis login --url https://<your-account-id>.console.facets.cloud")
		}
		return nil
	},
}

// installBootstrapSkills installs every no-auth bootstrap meta-skill into every
// detected AI host. Returns the number of (skill × host) installs. No hosts is a
// clean no-op (exit 0), so the cask hook and first-run never fail on a machine
// with no AI host yet.
func installBootstrapSkills(out io.Writer, asJSON bool) (int, error) {
	hosts := harness.Detected()
	if len(hosts) == 0 {
		if !asJSON {
			fmt.Fprintln(out, "No supported AI hosts detected — nothing to install.")
		}
		return 0, nil
	}
	n := 0
	for _, name := range skillinstall.BootstrapSkillNames() {
		res, err := skillinstall.Install(name, hosts)
		if err != nil {
			return n, err
		}
		n += len(res)
		if !asJSON {
			for _, r := range res {
				fmt.Fprintf(out, "  ✓ %-12s @ %s\n", r.Harness, r.Path)
			}
		}
	}
	retired, err := retireLegacySkills(hosts)
	if !asJSON && len(retired) > 0 {
		fmt.Fprintf(out, "Removed %d replaced skill install(s).\n", len(retired))
	}
	return n, err
}

// repairPraxisHooks re-points already-wired hooks at the stable binary path. An
// upgrade deletes the version-stamped directory an older praxis wired from, so
// the cask post-install hook (which runs `praxis setup`) heals those hooks
// without a login. Two limits keep it from touching hooks it should not: it
// adds no hook, so a user who never logged in stays untouched; and it needs a
// PATH entry that IS the running binary, so a throwaway build (a repo `./praxis
// setup`, a downloaded `praxis update`) cannot repoint a real install at itself.
// Callers print, so the notice can follow their own result line.
func repairPraxisHooks() (repaired []string, warning string) {
	praxisPath, ok := claudehooks.StableBinaryPath()
	if !ok {
		return nil, ""
	}
	home, err := os.UserHomeDir()
	if err != nil {
		return nil, ""
	}
	var failed []string // one host must not hide another's failure
	for _, host := range claudehooks.Hosts(home) {
		changed, err := claudehooks.Repair(host, praxisPath)
		if err != nil {
			failed = append(failed, err.Error()) // e.g. hand-edited settings.json is not valid JSON
			continue
		}
		if changed {
			repaired = append(repaired, host.Harness)
		}
	}
	return repaired, strings.Join(failed, "; ")
}

// printHookRepair renders what repairPraxisHooks did, or stays silent.
func printHookRepair(out io.Writer, asJSON bool, repaired []string, warning string) {
	if asJSON {
		return
	}
	if len(repaired) > 0 {
		fmt.Fprintf(out, "  ✓ re-pointed hooks at the current praxis for: %s\n", strings.Join(repaired, ", "))
	}
	if warning != "" {
		fmt.Fprintf(out, "  ⚠ some hooks not re-pointed (re-run `praxis login` after fixing): %s\n", warning)
	}
}

// bootstrapMarkerPath is ~/.praxis/.bootstrap-v1, the sentinel that makes
// first-run auto-install a single stat() after the first time.
func bootstrapMarkerPath() (string, error) {
	dir, err := paths.Dir()
	if err != nil {
		return "", err
	}
	return filepath.Join(dir, bootstrapMarker), nil
}

// markBootstrapDone writes the first-run sentinel (best-effort).
func markBootstrapDone() {
	if mp, err := bootstrapMarkerPath(); err == nil {
		_ = os.MkdirAll(filepath.Dir(mp), 0o755)
		_ = os.WriteFile(mp, []byte("1"), 0o644)
	}
}

// valueTakingFlags are the persistent/global flags whose VALUE is a separate
// token (`--profile prod`), which must not be mistaken for the command name.
// The `--flag=value` form is self-contained and needs no special handling.
var valueTakingFlags = map[string]bool{
	"--profile": true, "-p": true,
	"--url": true, "--token": true, "--timeout": true,
}

// firstPositional returns the first non-flag argument (the command name), or ""
// when the invocation is flags-only (bare `praxis`, `praxis --help`). It skips
// the value of value-taking flags so `praxis --profile prod ig hook` resolves to
// `ig`, not `prod`.
func firstPositional(args []string) string {
	skipNext := false
	for _, a := range args {
		if skipNext {
			skipNext = false
			continue
		}
		if strings.HasPrefix(a, "-") {
			if valueTakingFlags[a] && !strings.Contains(a, "=") {
				skipNext = true // consume the following value token
			}
			continue
		}
		return a
	}
	return ""
}

// firstRunSkipped reports whether first-run bootstrap must be SKIPPED for this
// invocation. Machine-invoked and self-referential commands are excluded so a
// skill write never happens on a hot path (`ig hook`/`mcp` run mid-session and
// must stay side-effect-free) or redundantly (`setup` does it explicitly).
func firstRunSkipped(args []string) bool {
	switch firstPositional(args) {
	case "ig", "mcp", "completion", "__complete", "git-credential", "setup", "version", "update":
		return true
	}
	return false
}

// firstRunBootstrap installs bootstrap skills once, gated by markerPath, unless
// the command is machine-invoked. install() reports how many (skill × host)
// installs happened. The marker is written ONLY after a real install (n > 0): a
// no-host machine (0) or a failure leaves it UNWRITTEN so first-run retries once
// a host is installed or the network/state recovers. Returns whether it
// installed. Never blocks the real command.
func firstRunBootstrap(args []string, markerPath string, install func() (int, error)) bool {
	if firstRunSkipped(args) || markerPath == "" {
		return false
	}
	if _, err := os.Stat(markerPath); err == nil {
		return false // already bootstrapped
	}
	n, err := install()
	if err != nil || n == 0 {
		return false // failure or no host yet — do not mark; retry next time
	}
	_ = os.MkdirAll(filepath.Dir(markerPath), 0o755)
	_ = os.WriteFile(markerPath, []byte("1"), 0o644)
	return true
}

// maybeFirstRunBootstrap is the Execute()-time entry point: it wires the real
// marker path and a silent install. Never fatal.
func maybeFirstRunBootstrap(args []string) {
	mp, err := bootstrapMarkerPath()
	if err != nil {
		return
	}
	firstRunBootstrap(args, mp, func() (int, error) {
		return installBootstrapSkills(io.Discard, true)
	})
}
