package cmd

import (
	"encoding/json"
	"fmt"
	"io"
	"os"
	"os/exec"
	"strings"

	"github.com/Facets-cloud/praxis-cli/internal/igcheckouts"
	"github.com/spf13/cobra"
)

// `praxis ig hook session-start|cwd-changed` — wired into Claude Code's
// settings.json by `praxis login` (see internal/claudehooks). It reads the
// session's cwd + id off stdin, and if cwd is a REMEMBERED ig catalog checkout
// (~/.praxis/ig-checkouts.json) it injects a one-line nudge toward the use-ig
// skill. Silent + exit 0 otherwise — a hook must never block a session.
//
// Unlike `ig hook`, which gates on a locally-built catalog, praxis has no local
// catalog: it nudges only once a checkout has been recorded, so it stays quiet
// until the agent has resolved that repo at least once.

// igHookEventName maps the CLI arg to the Claude event key echoed back in the
// hook payload.
func igHookEventName(arg string) (string, error) {
	switch arg {
	case "session-start":
		return "SessionStart", nil
	case "cwd-changed":
		return "CwdChanged", nil
	}
	return "", fmt.Errorf("unknown hook %q (want session-start|cwd-changed)", arg)
}

// repoOriginURL is dir's origin remote, or "" when dir is not a git repo (or has
// no origin) — which the hook treats as "nothing to nudge about".
func repoOriginURL(dir string) string {
	out, err := exec.Command("git", "-C", dir, "remote", "get-url", "origin").Output()
	if err != nil {
		return ""
	}
	return strings.TrimSpace(string(out))
}

// runIgHook is the pure core: given the resolved event key, session id, memory
// path, tmp dir (for per-session dedup), and the cwd's origin URL, it returns
// the hook JSON to print (or "" for silence). It never errors on a
// missing/malformed memory file — a hook must not fail a session.
func runIgHook(event, session, memoryPath, tmpDir, originURL string) (string, error) {
	if originURL == "" {
		return "", nil
	}
	entries, err := igcheckouts.Load(memoryPath)
	if err != nil {
		return "", nil // malformed memory → stay silent
	}
	e, ok := igcheckouts.Lookup(entries, originURL)
	if !ok {
		return "", nil
	}
	if igcheckouts.AlreadyNudged(tmpDir, session, igcheckouts.CanonicalGitURL(originURL)) {
		return "", nil
	}
	payload := map[string]any{"hookSpecificOutput": map[string]string{
		"hookEventName":     event,
		"additionalContext": igcheckouts.NudgeContext(e),
	}}
	b, err := json.Marshal(payload)
	if err != nil {
		return "", err
	}
	return string(b), nil
}

var igHookCmd = &cobra.Command{
	Use:    "hook <session-start|cwd-changed>",
	Short:  "Claude Code hook: nudge toward use-ig inside a remembered ig checkout",
	Hidden: true, // wired by `praxis login`, not called by hand
	Args:   cobra.ExactArgs(1),
	// SilenceErrors/Usage: a hook's stderr must stay quiet so it never clutters
	// a session; a bad arg is a wiring bug, surfaced by the returned error only.
	SilenceUsage:  true,
	SilenceErrors: true,
	RunE: func(cmd *cobra.Command, args []string) error {
		event, err := igHookEventName(args[0])
		if err != nil {
			return err
		}
		var p struct {
			Cwd       string `json:"cwd"`
			SessionID string `json:"session_id"`
		}
		if b, rErr := io.ReadAll(cmd.InOrStdin()); rErr == nil && len(b) > 0 {
			_ = json.Unmarshal(b, &p)
		}
		if p.Cwd == "" {
			p.Cwd, _ = os.Getwd()
		}
		out, err := runIgHook(event, p.SessionID, igcheckouts.DefaultPath(), os.TempDir(), repoOriginURL(p.Cwd))
		if err != nil {
			return err
		}
		if out != "" {
			fmt.Fprintln(cmd.OutOrStdout(), out)
		}
		return nil
	},
}
