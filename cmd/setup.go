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
// upgrade has since deleted — see repairPraxisHooks.
//
// Silent first-run installs embedded Praxis offline. Explicit setup also
// bootstraps Raptor and installs its independently sourced skill, without login.
//
// `setup` is hidden: it is the primitive the Homebrew post-install hook and
// first-run call. The user-facing GTM surface is the installed skill itself; the
// documented command surface stays unchanged. (`init`, the obvious name, was
// removed in the major-version cleanup and must not return — see root_test.go.)

// bootstrapMarker is bumped when the bootstrap skill content changes enough to
// warrant a one-time re-install on machines that already ran first-run.
const bootstrapMarker = ".bootstrap-praxis-v1"

var setupJSON bool

func init() {
	setupCmd.Flags().BoolVar(&setupJSON, "json", false, "JSON output")
	rootCmd.AddCommand(setupCmd)
}

var setupCmd = &cobra.Command{
	Use:    "setup",
	Short:  "Install Praxis and Raptor skills into your AI host (no login needed)",
	Hidden: true, // invoked by the brew post-install hook + first-run, not by hand
	Long: `Install the complete Praxis and Raptor skills into every detected AI host.
Praxis is embedded in this binary. Raptor is exported from its own CLI, installing
that CLI to ~/.local/bin first if missing. A missing binary needs network access;
an existing binary can export offline. Existing valid Raptor skills are preserved.

It also re-points any hook an older praxis wired from a path that an upgrade
has since deleted. No hook is added and no credentials are required.
The Homebrew post-install hook runs setup; silent first use installs only the
embedded Praxis package, without downloading or invoking Raptor.

  Next: praxis login --url https://<your-account-id>.console.facets.cloud`,
	Args: cobra.NoArgs,
	RunE: func(cmd *cobra.Command, args []string) error {
		out := cmd.OutOrStdout()
		asJSON := render.UseJSON(setupJSON, false, out)
		repaired, repairWarn := repairPraxisHooks()
		printHookRepair(out, asJSON, repaired, repairWarn)
		raptor, raptorErr := prepareRaptor(out, asJSON)
		n, err := installBootstrapSkills(out, asJSON)
		err = errors.Join(raptorErr, err)
		if n > 0 && err == nil {
			markBootstrapDone() // mark ONLY after a real install; a no-host run
			// stays retryable so first-run installs once a host appears.
		}
		if asJSON {
			warning := ""
			if err != nil {
				warning = err.Error()
			}
			return errors.Join(err, render.JSON(out, map[string]any{"installed": n, "raptor_binary": raptor, "skill_warning": warning, "skill_sync_complete": err == nil, "skill_recovery_directory": skillinstall.RecoveryDirectory(), "hook_warning": repairWarn}))
		}
		if n > 0 {
			if recovery := skillinstall.RecoveryDirectory(); recovery != "" {
				fmt.Fprintf(out, "Preserved skill content/provenance (if migrated): %s\n", recovery)
			}
			fmt.Fprintf(out, "Installed %d skill/host target(s).\n", n)
			fmt.Fprintln(out, "Next: praxis login --url https://<your-account-id>.console.facets.cloud")
		}
		return err
	},
}

// installBootstrapSkills installs both canonical packages without authentication.
// Returns the number of (skill × host) writes, including partial success.
func installBootstrapSkills(out io.Writer, asJSON bool) (int, error) {
	return installBootstrap(out, asJSON, true)
}

func installBootstrap(out io.Writer, asJSON, includeRaptor bool) (int, error) {
	hosts := harness.Detected()
	if project, inProject := resolveProjectScope(); inProject {
		for i := range hosts {
			hosts[i] = hosts[i].ProjectScoped(project)
		}
	}
	if len(hosts) == 0 {
		if !asJSON {
			fmt.Fprintln(out, "No supported AI hosts detected — nothing to install.")
		}
		return 0, nil
	}
	res, err := skillinstall.RefreshForHosts(hosts)
	if err != nil {
		return 0, err
	}
	var raptorErr error
	if includeRaptor {
		var raptor []skillinstall.Installation
		raptor, raptorErr = installRaptorSkills(hosts)
		res = append(res, raptor...)
	}
	if !asJSON {
		for _, r := range res {
			fmt.Fprintf(out, "  ✓ %-12s @ %s\n", r.Harness, r.Path)
		}
	}
	return len(res), raptorErr
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
	dir, err := paths.ActiveRoot()
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
		return installBootstrap(io.Discard, true, false)
	})
}
