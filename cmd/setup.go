package cmd

import (
	"fmt"
	"io"
	"os"
	"path/filepath"
	"strings"

	"github.com/Facets-cloud/praxis-cli/internal/harness"
	"github.com/Facets-cloud/praxis-cli/internal/paths"
	"github.com/Facets-cloud/praxis-cli/internal/render"
	"github.com/Facets-cloud/praxis-cli/internal/skillinstall"
	"github.com/spf13/cobra"
)

// `praxis setup` and the first-run auto-install land the pre-login GTM skill
// (praxis-getting-started) into the user's AI host the moment praxis is
// installed — WITHOUT a login. This solves the bootstrap chicken-and-egg: skills
// otherwise only appear after `praxis login`, so a freshly-installed praxis is
// invisible to the host and nothing tells it to log in. The skill is embedded
// (no network, no credentials), so this works offline and pre-auth.
//
// `setup` is hidden: it is the primitive the Homebrew post-install hook and
// first-run call. The user-facing GTM surface is the installed skill itself; the
// documented command surface stays unchanged. (`init`, the obvious name, was
// removed in the major-version cleanup and must not return — see root_test.go.)

// bootstrapMarker is bumped when the bootstrap skill content changes enough to
// warrant a one-time re-install on machines that already ran first-run.
const bootstrapMarker = ".bootstrap-v1"

var setupJSON bool

func init() {
	setupCmd.Flags().BoolVar(&setupJSON, "json", false, "JSON output")
	rootCmd.AddCommand(setupCmd)
}

var setupCmd = &cobra.Command{
	Use:    "setup",
	Short:  "Install the Praxis getting-started skill into your AI host (no login needed)",
	Hidden: true, // invoked by the brew post-install hook + first-run, not by hand
	Long: `Install the pre-login "getting started" skill into every detected AI host
(Claude Code, Codex, Gemini CLI) so your assistant knows what Praxis by
Facets does, where to sign up, and how to log in — before you authenticate.

No credentials or network are required. This runs automatically on first use
and via the Homebrew post-install hook.

  Next: praxis login --url https://<your-account-id>.console.facets.cloud`,
	Args: cobra.NoArgs,
	RunE: func(cmd *cobra.Command, args []string) error {
		out := cmd.OutOrStdout()
		asJSON := render.UseJSON(setupJSON, false, out)
		n, err := installBootstrapSkills(out, asJSON)
		if err != nil {
			return err
		}
		if n > 0 {
			markBootstrapDone() // mark ONLY after a real install; a no-host run
			// stays retryable so first-run installs once a host appears.
		}
		if asJSON {
			return render.JSON(out, map[string]any{"installed": n})
		}
		if n > 0 {
			fmt.Fprintf(out, "Installed the getting-started skill into %d host target(s).\n", n)
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
	return n, nil
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
